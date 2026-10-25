package postgresx

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

//go:embed daily-schema.sql
var dailySchemaSQL string

const dailySource = "daily-run-counts"
const legacyDailyDigest = "b5671e0b61318d5cd2a1c1e270764cff46ef334dd6579bbe76c87eca4afd97b8"

func dailyDigest() string { return fmt.Sprintf("%x", sha256.Sum256([]byte(dailySchemaSQL))) }

// Only the owner bootstrap path provisions this independently versioned extension.
func initializeDailyCounts(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(712083241,17483)`); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('cao_daily_contract') IS NOT NULL`).Scan(&exists); err != nil {
		return err
	}
	if exists {
		var digest string
		if err := tx.QueryRowContext(ctx, `SELECT digest FROM cao_daily_contract`).Scan(&digest); err != nil {
			return err
		}
		if digest != dailyDigest() {
			if digest != legacyDailyDigest {
				return errors.New("daily outcome storage contract mismatch; provision the matching extension as owner")
			}
			// This owner-only upgrade preserves admitted counts. Runtime roles
			// never migrate storage or accept the old, unbounded contract.
			for _, statement := range []string{
				`ALTER TABLE run_count_contributions ADD COLUMN observed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()`,
				`ALTER TABLE run_count_contributions ALTER COLUMN observed_at DROP DEFAULT`,
				`ALTER TABLE run_count_contributions ADD COLUMN retention_at TIMESTAMPTZ`,
				`ALTER TABLE run_count_contributions ADD COLUMN known_fields BIGINT NOT NULL DEFAULT 0,
					ADD COLUMN conflict BOOLEAN NOT NULL DEFAULT false`,
				`UPDATE run_count_contributions SET known_fields=
					(repository_id IS NOT NULL)::int+2*(workflow_id IS NOT NULL)::int+4*(owner IS NOT NULL)::int
					+8*(repository IS NOT NULL)::int+16*(workflow_path IS NOT NULL)::int+32*(started_at IS NOT NULL)::int
					+64*(attempt IS NOT NULL)::int+128*(status IS NOT NULL)::int+256*(conclusion IS NOT NULL)::int
					+512*(updated_at IS NOT NULL)::int+1024*(completed_at IS NOT NULL)::int`,
				`ALTER TABLE run_count_contributions ALTER COLUMN known_fields DROP DEFAULT, ALTER COLUMN conflict DROP DEFAULT`,
				`ALTER TABLE daily_run_count_state ADD COLUMN watermark_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
					ADD COLUMN cutoff_at TIMESTAMPTZ NOT NULL DEFAULT ((date_trunc('week',clock_timestamp() AT TIME ZONE 'UTC') - interval '14 days') AT TIME ZONE 'UTC')`,
				`ALTER TABLE daily_run_count_state ALTER COLUMN watermark_at DROP DEFAULT, ALTER COLUMN cutoff_at DROP DEFAULT`,
			} {
				if _, err := tx.ExecContext(ctx, statement); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE cao_daily_contract SET digest=$1`, dailyDigest()); err != nil {
				return err
			}
		}
		return tx.Commit()
	}
	for _, statement := range strings.Split(dailySchemaSQL, ";") {
		if strings.TrimSpace(statement) != "" {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cao_daily_contract(digest) VALUES($1)`, dailyDigest()); err != nil {
		return err
	}
	return tx.Commit()
}

type dailyQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Missing optional capability does not disable existing detail queries.
func dailyCapability(ctx context.Context, db dailyQuerier) (bool, error) {
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('cao_daily_contract') IS NOT NULL
		AND to_regclass('daily_run_counts') IS NOT NULL AND to_regclass('run_count_contributions') IS NOT NULL
		AND to_regclass('daily_run_count_state') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		return false, err
	}
	var readable bool
	if err := db.QueryRowContext(ctx, `SELECT has_table_privilege('cao_daily_contract','SELECT')
		AND has_table_privilege('daily_run_counts','SELECT') AND has_table_privilege('run_count_contributions','SELECT')
		AND has_table_privilege('daily_run_count_state','SELECT')`).Scan(&readable); err != nil || !readable {
		return false, err
	}
	var digest string
	if err := db.QueryRowContext(ctx, `SELECT digest FROM cao_daily_contract`).Scan(&digest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return digest == dailyDigest(), nil
}

func dailyInitialized(ctx context.Context, db dailyQuerier, namespace string) (bool, error) {
	var initialized bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM daily_run_count_state WHERE namespace=$1 AND id='daily-run-counts')`, namespace).Scan(&initialized)
	return initialized, err
}

func (s *Store) backfillDailyOnce(ctx context.Context) error {
	initialized, err := dailyInitialized(ctx, s.db, s.namespace)
	if err != nil || initialized {
		return err
	}
	tx, err := s.beginInvocation(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Another replica may have completed the backfill while this one waited for
	// the namespace lock. Ordinary opens never acquire that writer lock.
	initialized, err = dailyInitialized(ctx, tx, s.namespace)
	if err != nil {
		return err
	}
	if !initialized {
		if err := s.reconcileDailyInvocation(ctx, tx, ""); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const dailyOutcomeSQL = `CASE WHEN conclusion='success' THEN 'successful'
	WHEN replace(conclusion,'_','-') IN ('failure','startup-failure','stale','timed-out') THEN 'failed'
	WHEN replace(conclusion,'_','-')='action-required' THEN 'approval'
	WHEN coalesce(status,'')<>'' AND status<>'completed' THEN 'pending' ELSE 'other' END`

func dailyCutoff(now time.Time) time.Time {
	day := dayStart(now)
	return day.AddDate(0, 0, -((int(day.Weekday())+6)%7)-14)
}

func maintainDailyCounts(ctx context.Context, namespace string, now time.Time, exec func(context.Context, string, ...any) error) error {
	if err := exec(ctx, `INSERT INTO daily_run_count_state AS s(namespace,id,initialized_at,watermark_at,cutoff_at)
		VALUES($1,'daily-run-counts',$2,$2,$3)
		ON CONFLICT(namespace,id) DO UPDATE SET watermark_at=greatest(s.watermark_at,excluded.watermark_at),
			cutoff_at=greatest(s.cutoff_at,excluded.cutoff_at)`, namespace, now.UTC(), dailyCutoff(now)); err != nil {
		return err
	}
	for _, statement := range []string{
		`DELETE FROM daily_run_counts d USING daily_run_count_state s
			WHERE d.namespace=$1 AND s.namespace=d.namespace AND s.id='daily-run-counts'
			AND (d.day<to_char(s.cutoff_at AT TIME ZONE 'UTC','YYYY-MM-DD')
				OR d.day>=to_char((s.cutoff_at AT TIME ZONE 'UTC')+interval '21 days','YYYY-MM-DD'))`,
		`DELETE FROM run_count_contributions p USING daily_run_count_state s
			WHERE p.namespace=$1 AND s.namespace=p.namespace AND s.id='daily-run-counts'
			AND (coalesce(p.retention_at,p.started_at,p.observed_at)<s.cutoff_at
				OR (p.started_at IS NOT NULL AND coalesce(p.retention_at,p.started_at)>=s.cutoff_at+interval '504 hours'))`,
	} {
		if err := exec(ctx, statement, namespace); err != nil {
			return err
		}
	}
	return nil
}

// Namespace writer locks are held by every caller. The old and new receipt
// contributions, and publication of detail, commit or roll back together.
// Work remains in SQL: no full-detail Go materialization or retained run payloads.
func reconcileDailyCounts(ctx context.Context, namespace, runID string, now time.Time, exec func(context.Context, string, ...any) error, batchEnd func(context.Context) (string, error)) error {
	if err := maintainDailyCounts(ctx, namespace, now, exec); err != nil {
		return err
	}
	columns := []string{"repository_id", "workflow_id", "owner", "repository", "workflow_path", "started_at", "attempt", "status", "conclusion", "updated_at", "completed_at"}
	merged := []string{"r.namespace", "r.id", "coalesce(p.observed_at,least(s.watermark_at,greatest(r.updated_at,r.completed_at))) AS observed_at",
		"p.id IS NOT NULL AS had_previous", "p.attempt AS previous_attempt",
		"greatest(p.updated_at,p.completed_at) AS previous_revision",
		"greatest(r.updated_at,r.completed_at) AS incoming_revision"}
	fieldPresence := func(name string) string {
		for index, column := range entityTables["$runs"].columns {
			if column.name == name {
				return fmt.Sprintf("get_bit(r.present_fields,%d)=1", index)
			}
		}
		panic("daily receipt field missing from native Run contract")
	}
	changed := []string{"p.id IS NULL"}
	for index, column := range columns {
		changed = append(changed, fmt.Sprintf("(%s AND ((p.known_fields & %d)=0 OR r.%s IS DISTINCT FROM p.%s))",
			fieldPresence(column), 1<<index, column, column))
	}
	// Freeze only changed identities before updating receipts. Replayed snapshots
	// otherwise pay every batch's joins and temporary-table writes again.
	if err := exec(ctx, `CREATE TEMP TABLE daily_reconcile_keys ON COMMIT DROP AS
		SELECT r.id FROM runs r LEFT JOIN run_count_contributions p ON p.namespace=r.namespace AND p.id=r.id
		JOIN daily_run_count_state s ON s.namespace=r.namespace AND s.id='daily-run-counts'
		WHERE r.namespace=$1 AND ($2='' OR r.id=$2)
		AND (p.id IS NOT NULL OR r.started_at IS NULL
			OR (r.started_at>=s.cutoff_at AND r.started_at<s.cutoff_at+interval '504 hours'))
		AND (`+strings.Join(changed, " OR ")+`)`, namespace, runID); err != nil {
		return err
	}
	defer func() { _ = exec(ctx, "DROP TABLE IF EXISTS daily_reconcile_keys") }()
	if err := exec(ctx, "CREATE UNIQUE INDEX ON daily_reconcile_keys(id)"); err != nil {
		return err
	}
	if err := exec(ctx, "ANALYZE daily_reconcile_keys"); err != nil {
		return err
	}
	mergedField := func(name string) string {
		return "CASE WHEN " + fieldPresence(name) + " THEN r." + name + " ELSE p." + name + " END"
	}
	attempt := "coalesce((" + mergedField("attempt") + "),1)"
	known := []string{}
	for index, column := range columns {
		value := mergedField(column)
		bit := fmt.Sprint(1 << index)
		presence := "(" + fieldPresence(column) + " OR (coalesce(p.known_fields,0)&" + bit + ")<>0)"
		if column == "started_at" || column == "status" || column == "conclusion" || column == "updated_at" || column == "completed_at" {
			value = "CASE WHEN " + attempt + ">coalesce(p.attempt,1) THEN r." + column + " ELSE " + value + " END"
			presence = "(CASE WHEN " + attempt + ">coalesce(p.attempt,1) THEN " + fieldPresence(column) + " ELSE " + presence + " END)"
		}
		value = "CASE WHEN p.id IS NOT NULL AND " + attempt + "=coalesce(p.attempt,1) AND greatest(p.updated_at,p.completed_at) IS NOT NULL" +
			" AND greatest(r.updated_at,r.completed_at) IS NULL THEN CASE WHEN (p.known_fields&" + bit + ")<>0 THEN p." + column + " ELSE r." + column + " END ELSE " + value + " END"
		merged = append(merged, value+" AS "+column)
		known = append(known, "CASE WHEN "+presence+" THEN "+bit+" ELSE 0 END")
	}
	merged = append(merged, "("+strings.Join(known, "+")+")::bigint AS known_fields")
	rank := func(alias string) string {
		return "CASE WHEN " + alias + ".status='completed' OR replace(" + alias + ".conclusion,'_','-') IN ('success','failure','startup-failure','stale','timed-out','action-required') THEN 1 ELSE 0 END"
	}
	key := func(alias string) string {
		values := make([]string, 0, 12)
		for _, column := range []string{"id", "repository_id", "workflow_id", "owner", "repository", "workflow_path", "started_at", "status", "conclusion", "attempt", "updated_at", "completed_at"} {
			value := alias + "." + column
			if column == "started_at" || column == "updated_at" || column == "completed_at" {
				value = "to_char(" + value + ` AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')`
			}
			values = append(values, value)
		}
		return "(jsonb_build_array(" + strings.Join(values, ",") + `)::text COLLATE "C")`
	}
	statements := []string{
		`CREATE TEMP TABLE daily_candidates ON COMMIT DROP AS SELECT m.*,
			to_char(m.started_at AT TIME ZONE 'UTC','YYYY-MM-DD') AS day,
			coalesce((m.had_previous AND coalesce(m.attempt,1)=coalesce(m.previous_attempt,1)
				AND m.previous_revision IS NOT NULL AND m.incoming_revision=m.previous_revision
				AND ` + key("o") + `<>` + key("m") + `)
				OR (o.conflict AND m.attempt IS NOT DISTINCT FROM o.attempt
					AND m.updated_at IS NOT DISTINCT FROM o.updated_at AND m.completed_at IS NOT DISTINCT FROM o.completed_at),false) AS conflict,
			` + strings.NewReplacer("conclusion", "m.conclusion", "status", "m.status").Replace(dailyOutcomeSQL) + ` AS outcome FROM (
			SELECT ` + strings.Join(merged, ",") + ` FROM runs r JOIN daily_run_keys k ON k.id=r.id
			JOIN daily_run_count_state s ON s.namespace=r.namespace AND s.id='daily-run-counts' LEFT JOIN daily_previous p
			ON p.namespace=r.namespace AND p.id=r.id WHERE r.namespace=$1
			AND (` + strings.Join(changed, " OR ") + `)
			AND (p.id IS NULL OR ` + attempt + `>=coalesce(p.attempt,1))
			AND NOT (p.id IS NOT NULL AND ` + attempt + `=coalesce(p.attempt,1)
				AND greatest(r.updated_at,r.completed_at) IS NOT NULL
				AND greatest(p.updated_at,p.completed_at) IS NOT NULL
				AND greatest(p.updated_at,p.completed_at)>greatest(r.updated_at,r.completed_at))) m
			JOIN daily_run_count_state s ON s.namespace=m.namespace AND s.id='daily-run-counts'
			LEFT JOIN daily_previous o ON o.namespace=m.namespace AND o.id=m.id
			WHERE ((m.started_at IS NULL AND (m.had_previous OR greatest(m.updated_at,m.completed_at) IS NULL
					OR greatest(m.updated_at,m.completed_at)>=s.cutoff_at))
				OR (m.started_at>=s.cutoff_at AND m.started_at<s.cutoff_at+interval '504 hours')
				OR (m.had_previous AND (coalesce(m.attempt,1)>coalesce(m.previous_attempt,1)
					OR greatest(m.updated_at,m.completed_at)>m.previous_revision)))`,
		`CREATE TEMP TABLE daily_changes ON COMMIT DROP AS SELECT m.* FROM daily_candidates m
			LEFT JOIN daily_previous o ON o.namespace=m.namespace AND o.id=m.id
			WHERE NOT (m.had_previous AND coalesce(m.attempt,1)=coalesce(m.previous_attempt,1)
				AND m.previous_revision IS NOT NULL AND m.incoming_revision IS NOT NULL AND m.incoming_revision=m.previous_revision
				AND (` + rank("o") + `,` + key("o") + `)>(` + rank("m") + `,` + key("m") + `))`,
		`UPDATE run_count_contributions p SET conflict=true FROM daily_candidates n
			WHERE n.namespace=p.namespace AND n.id=p.id AND n.conflict`,
		`WITH contributions AS (
			SELECT p.namespace,to_char(p.started_at AT TIME ZONE 'UTC','YYYY-MM-DD') AS day,
				p.repository_id,p.workflow_id,coalesce(p.owner,'') AS owner,coalesce(p.repository,'') AS repository,
				coalesce(p.workflow_path,'') AS workflow_path,` + strings.NewReplacer("conclusion", "p.conclusion", "status", "p.status").Replace(dailyOutcomeSQL) + ` AS outcome,-1::bigint AS delta
			FROM daily_previous p JOIN daily_changes n ON n.namespace=p.namespace AND n.id=p.id
			JOIN daily_run_count_state s ON s.namespace=p.namespace AND s.id='daily-run-counts'
			WHERE p.started_at>=s.cutoff_at AND p.started_at<s.cutoff_at+interval '504 hours'
				AND coalesce(p.owner,'')<>'' AND coalesce(p.repository,'')<>'' AND coalesce(p.workflow_path,'')<>''
			UNION ALL SELECT n.namespace,n.day,n.repository_id,n.workflow_id,coalesce(n.owner,''),coalesce(n.repository,''),
				coalesce(n.workflow_path,''),n.outcome,1::bigint FROM daily_changes n
				JOIN daily_run_count_state s ON s.namespace=n.namespace AND s.id='daily-run-counts'
				WHERE n.started_at>=s.cutoff_at AND n.started_at<s.cutoff_at+interval '504 hours'
				AND coalesce(n.owner,'')<>'' AND coalesce(n.repository,'')<>'' AND coalesce(n.workflow_path,'')<>''
		), deltas AS (
			SELECT namespace,day,repository_id,workflow_id,
				coalesce(max(owner) FILTER(WHERE delta=1),max(owner)) AS owner,
				coalesce(max(repository) FILTER(WHERE delta=1),max(repository)) AS repository,
				coalesce(max(workflow_path) FILTER(WHERE delta=1),max(workflow_path)) AS workflow_path,
				sum(delta)::bigint AS total,
				coalesce(sum(delta) FILTER(WHERE outcome='successful'),0)::bigint AS successful,
				coalesce(sum(delta) FILTER(WHERE outcome='failed'),0)::bigint AS failed,
				coalesce(sum(delta) FILTER(WHERE outcome='approval'),0)::bigint AS approval,
				coalesce(sum(delta) FILTER(WHERE outcome='pending'),0)::bigint AS pending,
				coalesce(sum(delta) FILTER(WHERE outcome='other'),0)::bigint AS other
			FROM contributions GROUP BY namespace,day,repository_id,workflow_id
		)
		INSERT INTO daily_run_counts AS d(namespace,id,day,repository_id,workflow_id,owner,repository,workflow_path,total,successful,failed,approval,pending,other)
		SELECT n.namespace,concat('[',to_json(n.day)::text,',',to_json(n.repository_id)::text,',',to_json(n.workflow_id)::text,']'),
			n.day,n.repository_id,n.workflow_id,n.owner,n.repository,n.workflow_path,
			coalesce(o.total,0)+n.total,coalesce(o.successful,0)+n.successful,coalesce(o.failed,0)+n.failed,
			coalesce(o.approval,0)+n.approval,coalesce(o.pending,0)+n.pending,coalesce(o.other,0)+n.other
		FROM deltas n LEFT JOIN daily_run_counts o ON o.namespace=n.namespace AND o.day=n.day
			AND o.repository_id=n.repository_id AND o.workflow_id=n.workflow_id
		ON CONFLICT(namespace,id) DO UPDATE SET owner=excluded.owner,repository=excluded.repository,workflow_path=excluded.workflow_path,
			total=excluded.total,successful=excluded.successful,failed=excluded.failed,
			approval=excluded.approval,pending=excluded.pending,other=excluded.other`,
		`INSERT INTO run_count_contributions AS p(namespace,id,` + strings.Join(columns, ",") + `,observed_at,known_fields,conflict,retention_at)
			SELECT n.namespace,n.id,n.` + strings.Join(columns, ",n.") + `,n.observed_at,n.known_fields,n.conflict,
				CASE WHEN n.started_at>=s.cutoff_at AND n.started_at<s.cutoff_at+interval '504 hours' THEN n.started_at
					ELSE coalesce(o.retention_at,o.started_at) END
			FROM daily_changes n JOIN daily_run_count_state s ON s.namespace=n.namespace AND s.id='daily-run-counts'
			LEFT JOIN daily_previous o ON o.namespace=n.namespace AND o.id=n.id
			ON CONFLICT(namespace,id) DO UPDATE SET ` + dailyReceiptUpdates(columns) + `,
				retention_at=excluded.retention_at,known_fields=excluded.known_fields,conflict=excluded.conflict`,
		`DELETE FROM daily_run_counts WHERE namespace=$1 AND total=0`,
		`DROP TABLE daily_changes`,
		`DROP TABLE daily_candidates`,
	}
	after := ""
	for {
		if err := exec(ctx, `CREATE TEMP TABLE daily_run_keys ON COMMIT DROP AS
			SELECT id FROM daily_reconcile_keys WHERE id>$1 ORDER BY id LIMIT 512`, after); err != nil {
			return err
		}
		last, err := batchEnd(ctx)
		if err != nil {
			return err
		}
		if last == "" {
			if err := exec(ctx, `DROP TABLE daily_run_keys`); err != nil {
				return err
			}
			return maintainDailyCounts(ctx, namespace, now, exec)
		}
		// Receipts grow inside this transaction, where autovacuum cannot
		// refresh their estimates. Bound every comparison to this batch so a
		// cold-start nested loop cannot repeatedly scan the full receipt set.
		for _, statement := range []string{
			"CREATE UNIQUE INDEX ON daily_run_keys(id)",
			"ANALYZE daily_run_keys",
			`CREATE TEMP TABLE daily_previous ON COMMIT DROP AS SELECT p.* FROM run_count_contributions p
				JOIN daily_run_keys k ON k.id=p.id WHERE p.namespace=$1`,
			"ANALYZE daily_previous",
		} {
			var args []any
			if strings.Contains(statement, "$1") {
				args = []any{namespace}
			}
			if err := exec(ctx, statement, args...); err != nil {
				return err
			}
		}
		for index, statement := range statements {
			var args []any
			if strings.Contains(statement, "$1") {
				args = []any{namespace}
			}
			if err := exec(ctx, statement, args...); err != nil {
				return fmt.Errorf("reconcile daily outcomes step %d: %w", index, err)
			}
		}
		if err := exec(ctx, `DROP TABLE daily_run_keys`); err != nil {
			return err
		}
		if err := exec(ctx, `DROP TABLE daily_previous`); err != nil {
			return err
		}
		after = last
		if runID != "" {
			return maintainDailyCounts(ctx, namespace, now, exec)
		}
	}
}

func dailyReceiptUpdates(columns []string) string {
	updates := make([]string, len(columns))
	for index, column := range columns {
		updates[index] = column + "=excluded." + column
	}
	return strings.Join(updates, ",")
}

func (s *Store) reconcileDailyInvocation(ctx context.Context, tx *sql.Tx, runID string) error {
	capable, err := dailyCapability(ctx, tx)
	if err != nil || !capable {
		return err
	}
	return reconcileDailyCounts(ctx, s.namespace, runID, time.Now(), func(ctx context.Context, statement string, args ...any) error {
		_, err := tx.ExecContext(ctx, statement, args...)
		return err
	}, func(ctx context.Context) (string, error) {
		var last string
		err := tx.QueryRowContext(ctx, `SELECT coalesce(max(id),'') FROM daily_run_keys`).Scan(&last)
		return last, err
	})
}

func (w *Writer) reconcileDaily(ctx context.Context) error {
	if !w.store.dailyCounts {
		return nil
	}
	return reconcileDailyCounts(ctx, w.store.namespace, "", w.ingestedAt, func(ctx context.Context, statement string, args ...any) error {
		_, err := w.tx.Exec(ctx, statement, args...)
		return err
	}, func(ctx context.Context) (string, error) {
		var last string
		err := w.tx.QueryRow(ctx, `SELECT coalesce(max(id),'') FROM daily_run_keys`).Scan(&last)
		return last, err
	})
}

func dailySQLSource(namespace string, available, raw bool) query.SQLRelation {
	fields := map[string]string{"day": "day", "organization": "owner", "repository": "repository", "workflow": "workflow_path",
		"total": "total", "successful": "successful", "failed": "failed", "approval": "approval", "pending": "pending", "other": "other"}
	if raw {
		delete(fields, "organization")
		delete(fields, "workflow")
		for field, column := range map[string]string{"id": "id", "repositoryId": "repository_id", "workflowId": "workflow_id", "owner": "owner", "workflowPath": "workflow_path"} {
			fields[field] = column
		}
	}
	columns := map[string]query.SQLColumn{}
	empty := make([]string, 0, len(fields))
	for field, physical := range fields {
		kind, cast := query.SQLText, "text"
		if field == "total" || field == "successful" || field == "failed" || field == "approval" || field == "pending" || field == "other" {
			kind, cast = query.SQLNumber, "bigint"
		}
		columns[field] = query.SQLColumn{Expression: "t." + physical, Presence: "TRUE", Kind: kind}
		empty = append(empty, "NULL::"+cast+" AS "+physical)
	}
	if !available {
		return query.SQLRelation{SQL: "(SELECT " + strings.Join(empty, ",") + " WHERE FALSE) AS t", Columns: columns, Order: "0::bigint"}
	}
	return query.SQLRelation{SQL: "(SELECT * FROM daily_run_counts WHERE namespace={}) AS t", Params: []any{namespace}, Columns: columns, Order: "t.id"}
}

func (r *readTransaction) dailyMetadata(ctx context.Context) (model.Metadata, error) {
	available, err := dailyCapability(ctx, r.tx)
	metadata := model.Metadata{"source-id": dailySource, "source-kind": "canonical", "availability": "unavailable", "completeness": "unknown", "freshness": "unknown",
		"aggregate-version": 1, "aggregate-time-grain": "utc-run-start-day", "aggregate-coverage": "absent"}
	if err != nil || !available {
		return metadata, err
	}
	if !r.store.dailyCounts {
		return metadata, nil
	}
	initialized, err := dailyInitialized(ctx, r.tx, r.store.namespace)
	if err != nil || !initialized {
		return metadata, err
	}
	var count, undated, conflicts int64
	if err := r.tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM daily_run_counts WHERE namespace=$1),
		(SELECT count(*) FROM run_count_contributions WHERE namespace=$1 AND started_at IS NULL),
		(SELECT count(*) FROM run_count_contributions WHERE namespace=$1 AND conflict)`, r.store.namespace).Scan(&count, &undated, &conflicts); err != nil {
		return nil, err
	}
	metadata["availability"], metadata["completeness"] = "available", "partial"
	if count == 0 {
		metadata["availability"] = "empty"
	}
	metadata["undated-run-count"] = undated
	metadata["conflicted-runs"] = conflicts
	var watermark, cutoff time.Time
	if err := r.tx.QueryRowContext(ctx, `SELECT watermark_at,cutoff_at FROM daily_run_count_state
		WHERE namespace=$1 AND id='daily-run-counts'`, r.store.namespace).Scan(&watermark, &cutoff); err != nil {
		return nil, err
	}
	metadata["retention"] = "three-utc-calendar-weeks"
	metadata["watermark"] = watermark.UTC().Format(time.RFC3339Nano)
	metadata["retention-cutoff"] = cutoff.UTC().Format("2006-01-02")
	metadata["aggregate-retention-watermark"] = metadata["watermark"]
	metadata["aggregate-retention-start"] = metadata["retention-cutoff"]
	metadata["backfill-coverage"] = "available-evidence-only"
	metadata["aggregate-coverage"] = "available-evidence-only"
	return metadata, nil
}
