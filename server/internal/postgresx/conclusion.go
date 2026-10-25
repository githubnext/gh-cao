package postgresx

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/admission"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/sqlbuilder"
)

// RecordConcludedRun publishes bounded callback evidence into the same typed
// canonical collections queried by the dashboard, under ingestion's writer lock.
func (s *Store) RecordConcludedRun(ctx context.Context, identity admission.Identity, input admission.ConclusionRequest, at time.Time) error {
	run, err := invocationRun(identity)
	if err != nil {
		return err
	}
	if at.IsZero() {
		return errors.New("conclusion observation time is required")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(append([]byte(identity.Key()+"\n"), body...))
	fingerprint := hex.EncodeToString(digest[:])
	tx, err := s.beginInvocation(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	runID, _ := run["id"].(string)
	var previousAttempt, previousStatus, previousCode, previousAdmission string
	var repositoryID, workflowID string
	var runAt time.Time
	err = tx.QueryRowContext(ctx, `SELECT coalesce(a.name,r.attempt::text,'1'),coalesce(r.status,''),
		coalesce(a.code,''),coalesce(r.admission_status,''),r.run_at,r.repository_id,r.workflow_id FROM runs r
		LEFT JOIN audits a ON a.namespace=r.namespace AND a.run_id=r.id AND a.id=$3
		WHERE r.namespace=$1 AND r.id=$2`, s.namespace, runID, conclusionID("audit", runID+":coverage")).
		Scan(&previousAttempt, &previousStatus, &previousCode, &previousAdmission, &runAt, &repositoryID, &workflowID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	enrich := false
	if err == nil {
		order := compareConclusionAttempts(identity.RunAttempt, previousAttempt)
		if order < 0 {
			return tx.Commit()
		}
		if order == 0 && previousCode != "" {
			if previousCode != fingerprint {
				return admission.ErrDenied
			}
			return tx.Commit()
		}
		if order == 0 && previousStatus != "admitted" {
			var collected bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM audits WHERE namespace=$1 AND run_id=$2 AND source='session')
				OR EXISTS(SELECT 1 FROM grader_observations WHERE namespace=$1 AND run_id=$2)`,
				s.namespace, runID).Scan(&collected); err != nil {
				return err
			}
			if collected {
				return tx.Commit()
			}
			// A webhook/API-only Run is not full session evidence. Preserve
			// its status and known values while filling its absent facts.
			enrich = true
			run["repositoryId"], run["workflowId"] = repositoryID, workflowID
		}
		if order == 0 && previousAdmission == "admitted" {
			run["admissionStatus"] = "admitted"
		}
		// Every run-owned fact belongs to the winning attempt. Never mix old
		// tool/audit/usage observations into a newly reported rerun.
		for _, source := range tableNames() {
			table := entityTables[source]
			if enrich || !table.partitioned || source == "$runs" {
				continue
			}
			statement, _, err := sqlbuilder.Build("DELETE FROM {} WHERE namespace=$1 AND run_id=$2", sqlbuilder.Identifier(table.name))
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, statement, s.namespace, runID); err != nil {
				return err
			}
		}
		if !enrich {
			if _, err := tx.ExecContext(ctx, `DELETE FROM runs WHERE namespace=$1 AND id=$2`, s.namespace, runID); err != nil {
				return err
			}
		}
	} else {
		runAt = at
	}
	if err := s.invocationParents(ctx, tx, run, at); err != nil {
		return err
	}
	projected, err := projectConclusion(identity, input, run, at)
	if err != nil {
		return err
	}
	projected.audits[0]["code"] = fingerprint
	for _, collection := range []struct {
		source string
		rows   []model.Row
	}{
		{"$runs", []model.Row{projected.run}},
		{"$audits", projected.audits},
		{"$graders", projected.graders},
		{"$graderObservations", projected.observations},
	} {
		for _, row := range collection.rows {
			if enrich && collection.source == "$runs" {
				if err := s.enrichConclusionRun(ctx, tx, row); err != nil {
					return err
				}
				continue
			}
			if err := insertInvocationEntity(ctx, tx, s.namespace, collection.source, row, runAt); err != nil {
				return err
			}
		}
		if len(collection.rows) > 0 {
			if err := partialInvocationQuality(ctx, tx, s.namespace, collection.source); err != nil {
				return err
			}
		}
	}
	for _, source := range []string{"$repositories", "$workflows"} {
		if err := partialInvocationQuality(ctx, tx, s.namespace, source); err != nil {
			return err
		}
		if err := s.reconcileDailyInvocation(ctx, tx, runID); err != nil {
			return err
		}
	}
	if err := s.capInvocationRuns(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cao_state SET revision=revision+1,evaluated_at=greatest(evaluated_at,$2) WHERE namespace=$1`, s.namespace, at); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) enrichConclusionRun(ctx context.Context, tx *sql.Tx, row model.Row) error {
	table := entityTables["$runs"]
	args := []any{s.namespace, row["id"], stringPresence(table, row)}
	updates := []string{"present_fields=present_fields | $3::bit varying"}
	for index, column := range table.columns {
		value, present := entityFieldValue(row, column.field)
		if !present || column.field == "id" {
			continue
		}
		bound, err := column.bind(value)
		if err != nil {
			return err
		}
		args = append(args, bound)
		name := query.SQLIdentifier(column.name)
		updates = append(updates, fmt.Sprintf("%s=CASE WHEN get_bit(present_fields,%d)=0 OR %s IS NULL THEN $%d ELSE %s END",
			name, index, name, len(args), name))
	}
	statement, _, err := sqlbuilder.Build("UPDATE runs SET {} WHERE namespace=$1 AND id=$2", sqlbuilder.Fragment(strings.Join(updates, ",")))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, statement, args...)
	return err
}

func compareConclusionAttempts(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

func (w *Writer) stageConcludedRecords(ctx context.Context) error {
	for _, source := range []string{"$audits", "$graderObservations", "$graders"} {
		table := entityTables[source]
		filter := "run_id IN (SELECT id FROM admitted_runs)"
		if source == "$graders" {
			filter = "id IN (SELECT grader_id FROM admitted_grader_observations)"
		}
		statement := "CREATE TEMP TABLE " + query.SQLIdentifier("admitted_"+table.name) +
			" ON COMMIT DROP AS SELECT * FROM " + query.SQLIdentifier(table.name) + " WHERE namespace=$1 AND " + filter
		if _, err := w.tx.Exec(ctx, statement, w.store.namespace); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) prepareConcludedReplacement(ctx context.Context) error {
	fullEvidence := "false"
	if w.staged["$audits"] {
		fullEvidence += ` OR EXISTS(SELECT 1 FROM audits_stage e WHERE e.namespace=r.namespace AND e.run_id=r.id AND e.source='session')`
	}
	if w.staged["$graderObservations"] {
		fullEvidence += ` OR EXISTS(SELECT 1 FROM grader_observations_stage e WHERE e.namespace=r.namespace AND e.run_id=r.id)`
	}
	if _, err := w.tx.Exec(ctx, `CREATE TEMP TABLE newer_conclusion_runs ON COMMIT DROP AS
		SELECT p.id FROM admitted_runs p JOIN admitted_audits a ON a.namespace=p.namespace AND a.run_id=p.id
		JOIN runs r ON r.namespace=p.namespace AND r.id=p.id
		WHERE p.namespace=$1 AND a.source='conclusion-admission' AND a.source_sequence=0
			AND CASE WHEN a.name ~ '^[1-9][0-9]*$' THEN a.name::numeric>coalesce(r.attempt,1) ELSE false END`,
		w.store.namespace); err != nil {
		return err
	}
	if _, err := w.tx.Exec(ctx, `CREATE TEMP TABLE enriched_conclusion_runs ON COMMIT DROP AS
		SELECT p.id FROM admitted_runs p JOIN admitted_audits a ON a.namespace=p.namespace AND a.run_id=p.id
		JOIN runs r ON r.namespace=p.namespace AND r.id=p.id WHERE p.namespace=$1
		AND a.source='conclusion-admission' AND a.source_sequence=0
		AND CASE WHEN a.name ~ '^[1-9][0-9]*$' THEN a.name::numeric=coalesce(r.attempt,1) ELSE false END
		AND NOT (`+fullEvidence+`)`, w.store.namespace); err != nil {
		return err
	}
	updates := []string{"run_at=r.run_at", "present_fields=p.present_fields | r.present_fields"}
	for index, column := range entityTables["$runs"].columns {
		if column.field == "id" {
			continue
		}
		name := query.SQLIdentifier(column.name)
		updates = append(updates, fmt.Sprintf("%s=CASE WHEN get_bit(r.present_fields,%d)=1 AND r.%s IS NOT NULL THEN r.%s ELSE p.%s END",
			name, index, name, name, name))
	}
	if _, err := w.tx.Exec(ctx, "UPDATE admitted_runs p SET "+strings.Join(updates, ",")+
		" FROM runs r WHERE p.namespace=$1 AND r.namespace=p.namespace AND r.id=p.id AND p.id IN (SELECT id FROM enriched_conclusion_runs)",
		w.store.namespace); err != nil {
		return err
	}
	if _, err := w.tx.Exec(ctx, `UPDATE admitted_runs p SET repository_id=r.repository_id,workflow_id=r.workflow_id
		FROM runs r WHERE p.namespace=$1 AND r.namespace=p.namespace AND r.id=p.id
		AND p.id IN (SELECT id FROM newer_conclusion_runs)`, w.store.namespace); err != nil {
		return err
	}
	// Rebind every retained invocation of a workflow together, not just the
	// run present in this snapshot: grader definitions are shared across runs.
	if _, err := w.tx.Exec(ctx, `UPDATE admitted_runs p SET (repository_id,workflow_id)=(
		SELECT r.id,w.id FROM repositories r JOIN workflows w ON w.namespace=r.namespace AND w.repository_id=r.id
		WHERE r.namespace=p.namespace AND lower(r.owner)=lower(p.owner) AND lower(r.name)=lower(p.repository)
		AND lower(w.path)=p.workflow_path ORDER BY (w.id=p.workflow_id) DESC,w.id LIMIT 1)
		WHERE p.namespace=$1 AND EXISTS(
			SELECT 1 FROM repositories r JOIN workflows w ON w.namespace=r.namespace AND w.repository_id=r.id
			WHERE r.namespace=p.namespace AND lower(r.owner)=lower(p.owner) AND lower(r.name)=lower(p.repository)
			AND lower(w.path)=p.workflow_path)`, w.store.namespace); err != nil {
		return err
	}
	if _, err := w.tx.Exec(ctx, `UPDATE admitted_graders g SET workflow_id=p.workflow_id FROM admitted_grader_observations o,admitted_runs p
		WHERE g.namespace=$1 AND o.namespace=g.namespace AND o.grader_id=g.id AND p.namespace=o.namespace AND p.id=o.run_id`, w.store.namespace); err != nil {
		return err
	}
	if _, err := w.tx.Exec(ctx, `UPDATE admitted_grader_observations o SET grader_id=g.id FROM admitted_graders a,graders g
		WHERE o.namespace=$1 AND a.namespace=o.namespace AND a.id=o.grader_id AND g.namespace=a.namespace
		AND g.workflow_id=a.workflow_id AND g.source_grader_id=a.source_grader_id`, w.store.namespace); err != nil {
		return err
	}
	for _, source := range tableNames() {
		table := entityTables[source]
		if table.partitioned && source != "$runs" && w.staged[source] {
			tag, err := w.tx.Exec(ctx, "DELETE FROM "+query.SQLIdentifier(table.name+"_stage")+" WHERE namespace=$1 AND run_id IN (SELECT id FROM newer_conclusion_runs)", w.store.namespace)
			if err != nil {
				return err
			}
			w.ordinals[source] -= tag.RowsAffected()
		}
	}
	if _, err := w.tx.Exec(ctx, `DELETE FROM runs WHERE namespace=$1 AND id IN (SELECT id FROM newer_conclusion_runs UNION SELECT id FROM enriched_conclusion_runs)`, w.store.namespace); err != nil {
		return err
	}
	_, err := w.tx.Exec(ctx, `CREATE TEMP TABLE incoming_conclusion_runs ON COMMIT DROP AS SELECT id FROM runs WHERE namespace=$1`, w.store.namespace)
	return err
}

func (w *Writer) restoreConcludedRecords(ctx context.Context) error {
	for _, source := range []string{"$graders", "$audits", "$graderObservations"} {
		table := entityTables[source]
		columns := []string{"namespace", "ordinal", "present_fields"}
		offset := "(SELECT coalesce(max(ordinal),-1) FROM " + query.SQLIdentifier(table.name) + " WHERE namespace=$1)"
		if w.staged[source] {
			offset = "greatest(" + offset + ",(SELECT coalesce(max(ordinal),-1) FROM " + query.SQLIdentifier(table.name+"_stage") + " WHERE namespace=$1))"
		}
		values := []string{"a.namespace", offset + "+row_number() OVER(ORDER BY a.id)", "a.present_fields"}
		for _, column := range table.columns {
			columns = append(columns, query.SQLIdentifier(column.name))
			values = append(values, "a."+query.SQLIdentifier(column.name))
		}
		if table.partitioned {
			columns = append(columns, "run_at")
			values = append(values, "(SELECT r.run_at FROM runs r WHERE r.namespace=a.namespace AND r.id=a.run_id)")
		}
		filter := `EXISTS(SELECT 1 FROM admitted_runs p JOIN runs r ON r.namespace=p.namespace AND r.id=p.id
			JOIN admitted_audits c ON c.namespace=p.namespace AND c.run_id=p.id AND c.source='conclusion-admission'
				AND c.source_sequence=0 WHERE p.namespace=a.namespace AND r.run_at=p.run_at
				AND NOT EXISTS(SELECT 1 FROM incoming_conclusion_runs i WHERE i.id=p.id)`
		if source == "$graders" {
			filter += ` AND EXISTS(SELECT 1 FROM admitted_grader_observations o WHERE o.namespace=p.namespace AND o.run_id=p.id AND o.grader_id=a.id))`
		} else {
			filter += ` AND p.id=a.run_id)`
		}
		if w.staged[source] {
			filter += " AND NOT EXISTS(SELECT 1 FROM " + query.SQLIdentifier(table.name+"_stage") + " e WHERE e.namespace=a.namespace AND e.id=a.id)"
		}
		statement := fmt.Sprintf("INSERT INTO %s(%s) SELECT %s FROM %s a WHERE a.namespace=$1 AND %s ON CONFLICT DO NOTHING",
			query.SQLIdentifier(table.name), strings.Join(columns, ","), strings.Join(values, ","),
			query.SQLIdentifier("admitted_"+table.name), filter)
		if _, err := w.tx.Exec(ctx, statement, w.store.namespace); err != nil {
			return err
		}
	}
	return nil
}

var _ admission.ConcludedRuns = (*Store)(nil)
