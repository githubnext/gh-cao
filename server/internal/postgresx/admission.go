package postgresx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/admission"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/sqlbuilder"
)

// RecordAdmittedRun adds sparse canonical evidence before agent execution. It
// shares ingestion's namespace lock and never downgrades collected run evidence.
func (s *Store) RecordAdmittedRun(ctx context.Context, identity admission.Identity) error {
	run, err := invocationRun(identity)
	if err != nil {
		return err
	}
	runID := run["id"]
	tx, err := s.beginInvocation(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM runs WHERE namespace=$1 AND id=$2)`, s.namespace, runID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return tx.Commit()
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return err
	}
	if err := s.invocationParents(ctx, tx, run, now); err != nil {
		return err
	}
	run["status"], run["admissionStatus"], run["updatedAt"] = "admitted", "admitted", now
	if err = insertInvocationEntity(ctx, tx, s.namespace, "$runs", run, now); err != nil {
		return err
	}
	if err := s.reconcileDailyInvocation(ctx, tx, fmt.Sprint(runID)); err != nil {
		return err
	}
	if err := s.capInvocationRuns(ctx, tx); err != nil {
		return err
	}
	for _, source := range []string{"$repositories", "$workflows", "$runs"} {
		if err := partialInvocationQuality(ctx, tx, s.namespace, source); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE cao_state SET revision=revision+1,evaluated_at=greatest(evaluated_at,$2) WHERE namespace=$1`, s.namespace, now); err != nil {
		return err
	}
	return tx.Commit()
}

func invocationRun(identity admission.Identity) (model.Row, error) {
	owner, name, ok := parseRepositoryCoordinate(identity.Repository)
	prefix := identity.Repository + "/"
	path, _, hasRef := strings.Cut(strings.TrimPrefix(identity.WorkflowRef, prefix), "@")
	if !ok || !strings.HasPrefix(identity.WorkflowRef, prefix) || !hasRef ||
		!strings.HasPrefix(path, ".github/workflows/") || identity.RunID == "" {
		return nil, errors.New("invalid verified admission run identity")
	}
	path = strings.ToLower(path)
	if strings.HasSuffix(path, ".lock.yml") {
		path = strings.TrimSuffix(path, ".lock.yml") + ".md"
	}
	coordinate := strings.ToLower(identity.Repository)
	repositoryID := "repository:" + model.EncodeCoordinate(coordinate)
	workflowID := "workflow:" + model.EncodeCoordinate(coordinate+":"+path)
	runID := "github:run:" + coordinate + ":" + identity.RunID
	run := model.Row{
		"id": runID, "repositoryId": repositoryID, "workflowId": workflowID, "owner": owner, "repository": name,
		"githubRunId": identity.RunID, "workflowPath": path, "event": identity.Event,
	}
	if attempt, err := strconv.ParseInt(identity.RunAttempt, 10, 64); err == nil && attempt > 0 {
		run["attempt"] = attempt
	} else {
		postgresxLog.Printf("admission run attempt is outside the canonical numeric range; projected attempt remains unknown")
	}
	return run, nil
}

func (s *Store) beginInvocation(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(712083241,17484)`); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':' || $1,0))`, s.namespace); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO cao_state(namespace,revision,data_revision,evaluated_at,schema_version)
		VALUES($1,0,'','epoch',$2) ON CONFLICT(namespace) DO NOTHING`, s.namespace, model.SchemaVersion); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s *Store) invocationParents(ctx context.Context, tx *sql.Tx, run model.Row, now time.Time) error {
	owner, _ := run["owner"].(string)
	name, _ := run["repository"].(string)
	path, _ := run["workflowPath"].(string)
	repositoryID, _ := run["repositoryId"].(string)
	workflowID, _ := run["workflowId"].(string)
	// Prefer existing canonical inventory identities, including numeric GitHub
	// identities from another supported evidence adapter.
	if err := tx.QueryRowContext(ctx, `SELECT id FROM repositories WHERE namespace=$1
		AND lower(owner)=$2 AND lower(name)=$3 ORDER BY (id=$4) DESC,id LIMIT 1`,
		s.namespace, strings.ToLower(owner), strings.ToLower(name), repositoryID).Scan(&repositoryID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := insertInvocationEntity(ctx, tx, s.namespace, "$repositories", model.Row{
		"id": repositoryID, "owner": owner, "name": name,
	}, now); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM workflows WHERE namespace=$1 AND repository_id=$2
		AND lower(path)=$3 ORDER BY (id=$4) DESC,id LIMIT 1`, s.namespace, repositoryID, path, workflowID).Scan(&workflowID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := insertInvocationEntity(ctx, tx, s.namespace, "$workflows", model.Row{
		"id": workflowID, "repositoryId": repositoryID, "path": path,
	}, now); err != nil {
		return err
	}
	run["repositoryId"], run["workflowId"] = repositoryID, workflowID
	return nil
}

func partialInvocationQuality(ctx context.Context, tx *sql.Tx, namespace, source string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO cao_quality(namespace,collection,availability,completeness,freshness)
		VALUES($1,$2,'available','partial','unknown') ON CONFLICT(namespace,collection)
		DO UPDATE SET availability='available',completeness='partial'`, namespace, source)
	return err
}

func insertInvocationEntity(ctx context.Context, tx *sql.Tx, namespace, source string, row model.Row, now time.Time) error {
	table := entityTables[source]
	columns := []string{"namespace", "ordinal", "present_fields"}
	args := []any{namespace, stringPresence(table, row)}
	values := []string{"$1", "(SELECT coalesce(max(ordinal),-1)+1 FROM " + query.SQLIdentifier(table.name) + " WHERE namespace=$1)", "$2::bit varying"}
	for _, column := range table.columns {
		value, present := entityFieldValue(row, column.field)
		if !present {
			continue
		}
		bound, err := column.bind(value)
		if err != nil {
			return fmt.Errorf("admission %s.%s: %w", source, column.field, err)
		}
		args = append(args, bound)
		columns = append(columns, query.SQLIdentifier(column.name))
		values = append(values, fmt.Sprintf("$%d", len(args)))
	}
	if table.partitioned {
		columns = append(columns, "run_at")
		args = append(args, now)
		values = append(values, fmt.Sprintf("$%d", len(args)))
	}
	conflict := " ON CONFLICT(namespace,id) DO NOTHING"
	if table.partitioned {
		conflict = " ON CONFLICT(namespace,id,run_at) DO NOTHING"
	}
	statement, _, err := sqlbuilder.Build("INSERT INTO {}({}) VALUES({}){}",
		sqlbuilder.Identifier(table.name), sqlbuilder.Fragment(strings.Join(columns, ",")),
		sqlbuilder.Fragment(strings.Join(values, ",")), sqlbuilder.Fragment(conflict))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, statement, args...)
	return err
}

func stringPresence(table entityTable, row model.Row) string {
	bits := make([]byte, len(table.columns))
	for index, column := range table.columns {
		bits[index] = '0'
		if _, exists := entityFieldValue(row, column.field); exists {
			bits[index] = '1'
		}
	}
	return string(bits)
}

// Preserve sparse invocation observations during unrelated replacement, not a
// second persistent entity store. Collected evidence at the winning attempt wins.
func (w *Writer) stageAdmittedRuns(ctx context.Context) error {
	for _, source := range []string{"$runs", "$workflows", "$repositories"} {
		table := entityTables[source]
		filter := `(status='admitted' AND admission_status='admitted' OR EXISTS(
			SELECT 1 FROM audits a WHERE a.namespace=runs.namespace AND a.run_id=runs.id
				AND a.source='conclusion-admission' AND a.source_sequence=0))`
		if source == "$workflows" {
			filter = "id IN (SELECT workflow_id FROM admitted_runs)"
		}
		if source == "$repositories" {
			filter = "id IN (SELECT repository_id FROM admitted_runs)"
		}
		statement := "CREATE TEMP TABLE " + query.SQLIdentifier("admitted_"+table.name) + " ON COMMIT DROP AS SELECT * FROM " +
			query.SQLIdentifier(table.name) + " WHERE namespace=$1 AND " + filter
		if _, err := w.tx.Exec(ctx, statement, w.store.namespace); err != nil {
			return err
		}
	}
	return w.stageConcludedRecords(ctx)
}

func (w *Writer) restoreAdmittedRuns(ctx context.Context) error {
	if err := w.prepareConcludedReplacement(ctx); err != nil {
		return err
	}
	for _, source := range []string{"$repositories", "$workflows", "$runs"} {
		table := entityTables[source]
		columns := []string{"namespace", "ordinal", "present_fields"}
		values := []string{"a.namespace", "(SELECT coalesce(max(ordinal),-1) FROM " + query.SQLIdentifier(table.name) + " WHERE namespace=$1)+row_number() OVER(ORDER BY a.id)", "a.present_fields"}
		args := []any{w.store.namespace}
		identityFields := model.Row{"id": true, "owner": true, "name": true}
		if source == "$workflows" {
			identityFields = model.Row{"id": true, "repositoryId": true, "path": true}
		}
		if source != "$runs" {
			// A missing inventory parent only carries identity, not stale
			// campaign membership, rollout settings or enablement.
			args = append(args, stringPresence(table, identityFields))
			values[2] = "$2::bit varying"
		}
		for _, column := range table.columns {
			if source != "$runs" && identityFields[column.field] == nil {
				continue
			}
			columns = append(columns, query.SQLIdentifier(column.name))
			values = append(values, "a."+query.SQLIdentifier(column.name))
		}
		if table.name == "runs" {
			columns = append(columns, "run_at")
			values = append(values, "a.run_at")
		}
		filter := "NOT EXISTS(SELECT 1 FROM " + query.SQLIdentifier(table.name) + " t WHERE t.namespace=a.namespace AND t.id=a.id)"
		// Do not resurrect sparse parents when their run was replaced by input.
		if source != "$runs" {
			link := "repository_id"
			if source == "$workflows" {
				link = "workflow_id"
			}
			filter += " AND EXISTS(SELECT 1 FROM admitted_runs p WHERE p." + link + "=a.id AND NOT EXISTS(SELECT 1 FROM runs r WHERE r.namespace=p.namespace AND r.id=p.id))"
		}
		statement := "INSERT INTO " + query.SQLIdentifier(table.name) + "(" + strings.Join(columns, ",") + ") SELECT " +
			strings.Join(values, ",") + " FROM " + query.SQLIdentifier("admitted_"+table.name) + " a WHERE a.namespace=$1 AND " + filter
		if _, err := w.tx.Exec(ctx, statement, args...); err != nil {
			return err
		}
	}
	return w.restoreConcludedRecords(ctx)
}

var _ admission.AdmittedRuns = (*Store)(nil)
