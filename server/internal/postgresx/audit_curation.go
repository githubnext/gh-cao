package postgresx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

var auditCurationLog = logger.New("cao:postgresx:audit_curation")

func auditMetadataField(field string) bool {
	switch field {
	case "id", "runId", "timestamp", "source", "type", "summary", "status",
		"observedAt", "provenance", "sequence", "sourceSequence", "payloadRef":
		return true
	}
	return false
}

func validateAuditReferenceProjection(source string, row model.Row) error {
	switch source {
	case "$experimentAssignments", "$graderObservations", "$evalObservations":
	default:
		return nil
	}
	id, valid := row["auditId"].(string)
	if !valid || id == "" {
		return nil
	}
	for _, column := range entityTables[source].columns {
		if column.field == "auditId" {
			return nil
		}
	}
	return fmt.Errorf("%s.auditId is not supported by native storage; refusing lossy audit reference projection", source)
}

// auditCurationCandidate reports whether a row's source/type/status/summary
// shape matches one of the known metadata-only patterns native audit
// curation recognizes. It is extracted from validateAuditProjection so the
// shape decision is a single, independently testable unit boundary, separate
// from the per-column storage check that follows it.
func auditCurationCandidate(row model.Row) bool {
	source, _ := row["source"].(string)
	kind, _ := row["type"].(string)
	status, _ := row["status"].(string)
	summary, _ := row["summary"].(string)
	if auditCurationMarker(row) ||
		source == "audit" && kind == "audit.finding" && status == "critical" && summary == "Workflow Failed" && row["code"] == "workflow_failed" {
		return true
	}
	if source != "gh-aw-logs" {
		return false
	}
	switch kind {
	case "workflow_run_comparison":
		return status == "unavailable" && summary == "No baseline comparison"
	case "workflow_run_working_set":
		return status == "observed" && summary == "Working set measured"
	case "workflow_run_started", "workflow_run_completed":
		return status != ""
	case "workflow_run_failed":
		return status == "failure"
	case "workflow_run_usage", "workflow_run_safe_outputs":
		return status == "observed"
	}
	return false
}

// Native storage cannot preserve unknown evidence for a later maintenance pass.
// Reject potentially eligible rows before projection rather than erase that
// evidence and subsequently mistake the row for a pure metadata copy.
func validateAuditProjection(row model.Row) error {
	if row["timestamp"] == nil {
		return nil
	}
	if discardAuditCopy(row, nil) {
		return nil
	}
	if !auditCurationCandidate(row) {
		return nil
	}
	for _, column := range entityTables["$audits"].columns {
		if !auditMetadataField(column.field) && column.field != "code" {
			if bound, err := column.bind(row[column.field]); err == nil && bound != nil {
				return nil
			}
		}
	}
	source, _ := row["source"].(string)
	kind, _ := row["type"].(string)
	if row["code"] != nil && (source != "audit" || kind != "audit.finding" || row["code"] != "workflow_failed") {
		return nil
	}
	for field, value := range row {
		if value == nil || auditMetadataField(field) {
			continue
		}
		stored := false
		for _, column := range entityTables["$audits"].columns {
			if column.field == field {
				stored = true
				break
			}
		}
		if !stored {
			auditCurationLog.Printf("audit projection rejected unsupported evidence field=%q", field)
			return fmt.Errorf("audit evidence field %q is not supported by native storage; refusing lossy curation", field)
		}
	}
	return nil
}

func auditCurationStatement() string {
	var metadata []string
	for _, column := range entityTables["$audits"].columns {
		if !auditMetadataField(column.field) && column.field != "code" {
			metadata = append(metadata, "a."+query.SQLIdentifier(column.name)+" IS NULL")
		}
	}
	var references []string
	for _, source := range []string{"$experimentAssignments", "$graderObservations", "$evalObservations"} {
		table := entityTables[source]
		for _, column := range table.columns {
			if column.field == "auditId" {
				references = append(references, "NOT EXISTS (SELECT 1 FROM "+query.SQLIdentifier(table.name)+
					" o WHERE o.namespace=a.namespace AND o."+query.SQLIdentifier(column.name)+"=a.id)")
			}
		}
	}
	// Restrict decimal/exponent length before casting: malformed or extreme
	// summaries must retain their rows, not abort a namespace's cleanup.
	usage := fmt.Sprintf("CASE WHEN length(a.summary)<=%d AND a.summary ~ '^AIC %s$' THEN substring(a.summary FROM 5)::numeric END", auditSummaryLimit, auditNumberPattern)
	items := fmt.Sprintf("CASE WHEN length(a.summary)<=%d AND a.summary ~ '^(0|[1-9][0-9]*) safe output items$' THEN left(a.summary,length(a.summary)-18)::numeric END", auditSummaryLimit)
	return `DELETE FROM audits a WHERE a.namespace=$1 AND a.timestamp IS NOT NULL AND isfinite(a.timestamp)
		AND ` + strings.Join(metadata, " AND ") + `
		AND (a.code IS NULL OR (a.source='audit' AND a.type='audit.finding'
			AND a.status='critical' AND a.code='workflow_failed' AND a.summary='Workflow Failed'))
		AND ` + strings.Join(references, " AND ") + `
		AND (
			(a.source,a.type,a.status,a.summary) IN (
				('gh-aw-logs','workflow_run_comparison','unavailable','No baseline comparison'),
				('gh-aw-logs','workflow_run_working_set','observed','Working set measured'),
				('agent','agent.session','completed',''),
				('audit','audit.observability','low','1 anomalous event pattern(s) detected'),
				('audit','audit.recommendation','low','Monitor workflow performance over time')
			)
			OR EXISTS (SELECT 1 FROM runs r WHERE r.namespace=a.namespace AND r.id=a.run_id AND (
				(a.source='gh-aw-logs' AND (
					(a.type='workflow_run_started' AND isfinite(a.timestamp)
						AND isfinite(coalesce(r.started_at,r.created_at))
						AND a.timestamp=coalesce(r.started_at,r.created_at)
						AND nullif(r.status,'') IS NOT NULL AND a.status=r.status)
					OR (a.type='workflow_run_completed' AND isfinite(a.timestamp) AND isfinite(r.completed_at)
						AND a.timestamp=r.completed_at AND nullif(r.conclusion,'') IS NOT NULL
						AND a.status=r.conclusion AND a.summary=coalesce(nullif(r.classification,''),r.conclusion))
					OR (a.type='workflow_run_failed' AND a.status='failure'
						AND (r.conclusion='failure' OR nullif(r.failure_kind,'') IS NOT NULL)
						AND a.summary=coalesce(nullif(r.failure_kind,''),'workflow run failed'))
					OR (a.type='workflow_run_usage' AND a.status='observed' AND r.aic_total IS NOT NULL
						AND ` + usage + ` BETWEEN 0 AND 1.7976931348623157e308::numeric
						AND ` + usage + `=r.aic_total)
					OR (a.type='workflow_run_safe_outputs' AND a.status='observed' AND r.safe_items_count IS NOT NULL
						AND ` + items + ` BETWEEN 0 AND 9007199254740991::numeric
						AND trunc(` + items + `)=` + items + ` AND ` + items + `=r.safe_items_count)
				))
				OR (a.source='audit' AND a.type='audit.finding' AND a.status='critical'
					AND a.code='workflow_failed' AND a.summary='Workflow Failed' AND r.conclusion='failure')
			))
		)`
}

// CurateAudits atomically maintains one published namespace, including when its
// artifact revision is unchanged. Evidence clocks and artifact identity do not
// change; only a deletion advances the published database revision.
func (s *Store) CurateAudits(ctx context.Context) (State, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return State{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':' || $1, 0))`, s.namespace); err != nil {
		return State{}, fmt.Errorf("lock audit curation: %w", err)
	}
	reader := &readTransaction{store: s, tx: tx}
	state, err := reader.stateHeader(ctx)
	if err != nil {
		return State{}, err
	}
	if state.Ready {
		state, err = s.applyAuditCuration(ctx, tx, reader)
		if err != nil {
			return State{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return State{}, err
	}
	return state, nil
}

// applyAuditCuration deletes metadata-only audit rows for one ready
// namespace, republishes quality availability, and advances the revision
// only when a deletion actually occurred. It is extracted from CurateAudits
// so the delete-then-publish decision is a single, independently testable
// unit boundary against a real transaction, separate from CurateAudits'
// readiness gate and advisory lock.
func (s *Store) applyAuditCuration(ctx context.Context, tx *sql.Tx, reader *readTransaction) (State, error) {
	result, err := tx.ExecContext(ctx, auditCurationStatement(), s.namespace)
	if err != nil {
		return State{}, fmt.Errorf("curate native audits: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return State{}, err
	}
	if deleted == 0 {
		auditCurationLog.Printf("audit curation found nothing to delete namespace=%s", s.namespace)
		return reader.State(ctx)
	}
	result, err = tx.ExecContext(ctx, `UPDATE cao_quality SET
		availability=CASE WHEN availability='unavailable' THEN availability
			WHEN EXISTS(SELECT 1 FROM audits WHERE namespace=$1) THEN 'available' ELSE 'empty' END
		WHERE namespace=$1 AND collection='$audits'`, s.namespace)
	if err != nil {
		return State{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return State{}, err
	}
	if updated != 1 {
		return State{}, errors.New("published native audits are missing quality metadata")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cao_state SET revision=revision+1 WHERE namespace=$1`, s.namespace); err != nil {
		return State{}, err
	}
	auditCurationLog.Printf("audit curation deleted rows namespace=%s deleted=%d", s.namespace, deleted)
	return reader.State(ctx)
}
