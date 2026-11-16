package postgresx

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/mining"
	"github.com/githubnext/gh-cao/server/internal/sqlbuilder"
)

const defaultRunRetentionDays = 30
const defaultLinkedRetentionDays = 7
const futureRunDays = 28
const partitionMaintenanceInterval = 24 * time.Hour

var partitionsLog = logger.New("cao:postgresx:partitions")
var partitionTables = []string{"audits", "domains", "eval_observations", "experiment_assignments", "friction", "grader_observations", "issues", "skills", "tools", "runs"}

func dayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// RunPartitionMaintenance is an administrative operation, never part of
// ingestion. Only whole days ending at or before each retention cutoff go.
func (s *Store) RunPartitionMaintenance(ctx context.Context, now time.Time, retentionDays, linkedRetentionDays int) error {
	if retentionDays < 7 || retentionDays > 3650 {
		return fmt.Errorf("run retention days must be between 7 and 3650")
	}
	if linkedRetentionDays < 7 || linkedRetentionDays > retentionDays {
		return fmt.Errorf("linked retention days must be between 7 and run retention days")
	}
	lock, err := pgx.ConnectConfig(ctx, s.config.Copy())
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = lock.Close(closeCtx)
	}()
	if _, err := lock.Exec(ctx, "SELECT pg_advisory_lock(712083241, 17484)"); err != nil {
		return err
	}
	tables := partitionTables
	cutoff := now.UTC().AddDate(0, 0, -retentionDays)
	linkedCutoff := now.UTC().AddDate(0, 0, -linkedRetentionDays)
	start := dayStart(cutoff)
	linkedStart := dayStart(linkedCutoff)
	current := dayStart(now)
	end := current.AddDate(0, 0, futureRunDays+1)
	existing, expired, err := s.existingPartitions(ctx, cutoff, linkedCutoff)
	if err != nil {
		return err
	}
	partitionsLog.Printf("partition maintenance started namespace=%s retention_days=%d linked_retention_days=%d expired_days=%d", s.namespace, retentionDays, linkedRetentionDays, len(expired))
	defer func() {
		partitionsLog.Printf("partition maintenance finished namespace=%s window_start=%s window_end=%s", s.namespace, start.Format("20060102"), end.Format("20060102"))
	}()
	// Provision the current and upcoming days before any historical backlog.
	for at := current; at.Before(end); at = at.AddDate(0, 0, 1) {
		if err := s.createDay(ctx, tables, at, existing); err != nil {
			return err
		}
	}
	for at := start; at.Before(current); at = at.AddDate(0, 0, 1) {
		needed := []string{"runs"}
		if !at.Before(linkedStart) {
			needed = tables
		}
		if err := s.createDay(ctx, needed, at, existing); err != nil {
			return err
		}
	}
	for _, at := range expired {
		var drop []string
		for _, table := range tables {
			name := table + "_d" + at.Format("20060102")
			tableCutoff := linkedCutoff
			if table == "runs" {
				tableCutoff = cutoff
			}
			if existing[name] && !at.AddDate(0, 0, 1).After(tableCutoff) {
				drop = append(drop, table)
			}
		}
		if err := s.maintainDay(ctx, drop, at, true); err != nil {
			return err
		}
	}
	return s.purgeInactiveRepositoriesAt(ctx, cutoff, now)
}

func (s *Store) purgeInactiveRepositoriesAt(ctx context.Context, cutoff, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':' || $1, 0))`, s.namespace); err != nil {
		return err
	}
	capable, err := dailyCapability(ctx, tx)
	if err != nil {
		return err
	}
	changed := false
	if capable {
		if err := maintainDailyCounts(ctx, s.namespace, now, func(ctx context.Context, statement string, args ...any) error {
			result, err := tx.ExecContext(ctx, statement, args...)
			if err == nil && strings.HasPrefix(statement, "DELETE") {
				count, err := result.RowsAffected()
				if err != nil {
					return err
				}
				changed = changed || count > 0
			}
			return err
		}); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM output_lifecycles
		WHERE namespace=$1 AND observed_at<$2`, s.namespace,
		now.UTC().AddDate(0, 0, -mining.OutputLifecycleRetentionDays))
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else {
		changed = changed || count > 0
	}
	// Keep the identity while any historical or inventory record still refers
	// to it. Retired identities become eligible only after the retention window.
	rows, err := tx.QueryContext(ctx, `SELECT l.id FROM cao_repository_lifecycle l
		WHERE l.namespace=$1 AND l.lifecycle IN ('deleted','archived') AND l.changed_at<$2
		AND NOT EXISTS (SELECT 1 FROM workflows w WHERE w.namespace=l.namespace AND w.repository_id=l.id)
		AND NOT EXISTS (SELECT 1 FROM runs r WHERE r.namespace=l.namespace AND r.repository_id=l.id)
		AND NOT EXISTS (SELECT 1 FROM operational_values v WHERE v.namespace=l.namespace AND v.repository_id=l.id)
		AND NOT EXISTS (SELECT 1 FROM output_lifecycles o WHERE o.namespace=l.namespace AND o.repository_id=l.id)`,
		s.namespace, cutoff)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM repositories WHERE namespace=$1 AND id=$2`, s.namespace, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM cao_repository_lifecycle WHERE namespace=$1 AND id=$2`, s.namespace, id); err != nil {
			return err
		}
	}
	if len(ids) > 0 {
		changed = true
	}
	if changed {
		if _, err := tx.ExecContext(ctx, `UPDATE cao_state SET revision=revision+1 WHERE namespace=$1`, s.namespace); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) existingPartitions(ctx context.Context, cutoff, linkedCutoff time.Time) (map[string]bool, []time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.relname,p.relname FROM pg_inherits i
		JOIN pg_class c ON c.oid=i.inhrelid JOIN pg_class p ON p.oid=i.inhparent
		WHERE p.relnamespace=current_schema()::regnamespace AND p.relname IN
		('audits','domains','eval_observations','experiment_assignments',
		 'friction','grader_observations','issues','skills','tools','runs')`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	existing := make(map[string]bool)
	var expired []time.Time
	seen := make(map[time.Time]bool)
	for rows.Next() {
		var name, table string
		if err := rows.Scan(&name, &table); err != nil {
			return nil, nil, err
		}
		existing[name] = true
		if !strings.HasPrefix(name, table+"_d") || len(name) != len(table+"_d")+8 {
			return nil, nil, fmt.Errorf("unexpected %s partition: daily partitions required; rebuild canonical storage in a fresh schema without removing operational audit history", table)
		}
		date, err := time.Parse("20060102", strings.TrimPrefix(name, table+"_d"))
		if err != nil {
			return nil, nil, err
		}
		tableCutoff := linkedCutoff
		if table == "runs" {
			tableCutoff = cutoff
		}
		if !date.AddDate(0, 0, 1).After(tableCutoff) && !seen[date] {
			expired = append(expired, date)
			seen[date] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	sort.Slice(expired, func(i, j int) bool { return expired[i].Before(expired[j]) })
	return existing, expired, nil
}

func (s *Store) createDay(ctx context.Context, tables []string, at time.Time, existing map[string]bool) error {
	var missing []string
	for _, table := range tables {
		if !existing[table+"_d"+at.Format("20060102")] {
			missing = append(missing, table)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if err := s.maintainDay(ctx, missing, at, false); err != nil {
		return err
	}
	for _, table := range missing {
		existing[table+"_d"+at.Format("20060102")] = true
	}
	return nil
}

func (s *Store) maintainDay(ctx context.Context, tables []string, at time.Time, drop bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var namespaces []string
	if drop {
		seen := make(map[string]bool)
		for _, table := range tables {
			found, err := partitionNamespaces(ctx, tx, table+"_d"+at.Format("20060102"))
			if err != nil {
				return err
			}
			for _, namespace := range found {
				if !seen[namespace] {
					namespaces = append(namespaces, namespace)
					seen[namespace] = true
				}
			}
		}
		for _, namespace := range namespaces {
			if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':' || $1, 0))`, namespace); err != nil {
				return err
			}
		}
	}
	for _, table := range tables {
		name := table + "_d" + at.Format("20060102")
		if drop {
			if _, err := tx.ExecContext(ctx, "ALTER TABLE "+table+" DETACH PARTITION "+name); err != nil {
				return fmt.Errorf("detach expired %s: %w", name, err)
			}
			if _, err := tx.ExecContext(ctx, "DROP TABLE "+name); err != nil {
				return fmt.Errorf("drop expired %s: %w", name, err)
			}
		} else {
			statement, _, err := sqlbuilder.Build("CREATE TABLE {} PARTITION OF {} FOR VALUES FROM ({}) TO ({})",
				sqlbuilder.Identifier(name), sqlbuilder.Identifier(table),
				sqlbuilder.Fragment("'"+at.Format(time.RFC3339)+"'"),
				sqlbuilder.Fragment("'"+at.AddDate(0, 0, 1).Format(time.RFC3339)+"'"))
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("create daily %s: %w", name, err)
			}
		}
	}
	for _, namespace := range namespaces {
		for _, table := range tables {
			var count int
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE namespace=$1", namespace).Scan(&count); err != nil {
				return err
			}
			source := ""
			for key, entity := range entityTables {
				if entity.name == table {
					source = key
					break
				}
			}
			if source == "" {
				return fmt.Errorf("unregistered run partition table %s", table)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE cao_quality SET availability=CASE WHEN availability='unavailable' THEN 'unavailable' ELSE $1 END WHERE namespace=$2 AND collection=$3`,
				map[bool]string{true: "empty", false: "available"}[count == 0], namespace, source); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE cao_state SET revision=revision+1 WHERE namespace=$1", namespace); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func partitionNamespaces(ctx context.Context, tx *sql.Tx, name string) ([]string, error) {
	statement, _, err := sqlbuilder.Build("SELECT DISTINCT namespace FROM {}", sqlbuilder.Identifier(name))
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var namespaces []string
	for rows.Next() {
		var namespace string
		if err := rows.Scan(&namespace); err != nil {
			return nil, err
		}
		namespaces = append(namespaces, namespace)
	}
	return namespaces, rows.Err()
}

func configuredRetentionDays() (int, error) {
	value := strings.TrimSpace(os.Getenv("CAO_POSTGRES_RUN_RETENTION_DAYS"))
	if value == "" {
		return defaultRunRetentionDays, nil
	}
	days, err := strconv.Atoi(value)
	if err != nil || days < 7 || days > 3650 {
		return 0, fmt.Errorf("CAO_POSTGRES_RUN_RETENTION_DAYS must be between 7 and 3650")
	}
	return days, nil
}

func configuredLinkedRetentionDays() (int, error) {
	value := strings.TrimSpace(os.Getenv("CAO_POSTGRES_LINKED_RETENTION_DAYS"))
	if value == "" {
		return defaultLinkedRetentionDays, nil
	}
	days, err := strconv.Atoi(value)
	if err != nil || days < 7 || days > 3650 {
		return 0, fmt.Errorf("CAO_POSTGRES_LINKED_RETENTION_DAYS must be between 7 and 3650")
	}
	return days, nil
}
