package postgresx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func (w *Writer) publishRunBundles(ctx context.Context, input RunBundlePublication, changed map[string]bool) (State, error) {
	if err := w.validateBundleRelationships(ctx); err != nil {
		return State{}, err
	}
	if err := w.captureAuditClocks(ctx, "affected_runs"); err != nil {
		return State{}, err
	}
	// Input clocks outlive curation. Only a complete replacement or a winning
	// new attempt can replace an older source clock with a smaller one.
	for _, statement := range []string{
		"CREATE UNIQUE INDEX ON ingestion_audit_clocks(run_id)",
		`INSERT INTO ingestion_audit_clocks(run_id,run_at,evidence_at)
			SELECT p.run_id,k.run_at,p.evidence_at FROM cao_run_accounting p
			JOIN affected_runs k ON k.id=p.run_id
			WHERE p.namespace=$1 AND p.collection='$audits' AND NOT k.new_attempt
			AND NOT EXISTS(SELECT 1 FROM run_bundle_coverage c
				WHERE c.id=k.id AND c.collection='$audits' AND c.coverage='complete')
			ON CONFLICT(run_id) DO UPDATE SET evidence_at=greatest(ingestion_audit_clocks.evidence_at,excluded.evidence_at)`,
	} {
		var args []any
		if statement[0] != 'C' {
			args = []any{w.store.namespace}
		}
		if _, err := w.tx.Exec(ctx, statement, args...); err != nil {
			return State{}, err
		}
	}
	curated, err := w.tx.Exec(ctx, auditCurationStatement()+` AND EXISTS(
		SELECT 1 FROM affected_runs k WHERE k.id=a.run_id AND k.run_at=a.run_at)`, w.store.namespace)
	if err != nil {
		return State{}, err
	}
	changed["$audits"] = changed["$audits"] || curated.RowsAffected() > 0
	var sources []string
	for source, touched := range changed {
		if touched {
			sources = append(sources, source)
		}
	}
	sort.Strings(sources)
	if len(sources) > 0 {
		if _, err := w.tx.Exec(ctx, "SELECT cao_analyze_native($1::text[])", sources); err != nil {
			return State{}, err
		}
	}
	if err := w.reconcileDailyRunBundles(ctx); err != nil {
		return State{}, err
	}
	for _, statement := range []string{
		`DELETE FROM cao_run_accounting a USING affected_runs k WHERE a.namespace=$1 AND a.run_id=k.id AND k.new_attempt`,
		`UPDATE cao_run_accounting a SET run_at=k.run_at FROM affected_runs k
			WHERE a.namespace=$1 AND a.run_id=k.id AND a.run_at<>k.run_at`,
	} {
		if _, err := w.tx.Exec(ctx, statement, w.store.namespace); err != nil {
			return State{}, err
		}
	}
	measured := w.capacitySQL(true)
	measured.sources = map[string]bool{}
	for source, touched := range changed {
		measured.sources[source] = touched
	}
	for _, bundle := range input.Runs {
		for name, collection := range bundle.Collections {
			if collection.Coverage == "complete" {
				measured.sources["$"+name] = true
			}
		}
	}
	if err := measureRunAccounting(ctx, w.store.namespace, "affected_runs", measured); err != nil {
		return State{}, err
	}
	if err := w.capRuns(ctx); err != nil {
		return State{}, err
	}
	if err := w.refreshBundleAccounting(ctx, changed); err != nil {
		return State{}, err
	}
	if err := w.updateBundleQuality(ctx, changed); err != nil {
		return State{}, err
	}
	if err := w.tx.QueryRow(ctx, `SELECT coalesce(greatest(
		(SELECT max(evidence_at) FROM cao_ingestion_accounting WHERE namespace=$1),
		(SELECT max(greatest(as_of,retrieved_at)) FROM cao_quality WHERE namespace=$1)),'epoch'::timestamptz)`,
		w.store.namespace).Scan(&w.evaluatedAt); err != nil {
		return State{}, err
	}
	content, err := inputIdentity(input)
	if err != nil {
		return State{}, err
	}
	digest := sha256.Sum256([]byte(w.PreviousDataRevision + "\x00run-bundles/v1\x00" + content))
	dataRevision := "sha256:" + hex.EncodeToString(digest[:])
	// Selection and bundles are still reconstructed synchronously, from the
	// complete retained namespace, not just this delta's selected Runs.
	if err := w.persistStatisticalSnapshot(ctx, dataRevision, query.MaxWorkingBytes); err != nil {
		return State{}, fmt.Errorf("publish run-bundle statistical snapshot: %w", err)
	}
	workflowDigest, workflowRevision := w.workflowSubjectDigest, w.workflowSubjectRevision
	if changed["$workflows"] {
		workflowDigest, err = workflowSubjectDigestPGX(ctx, w.tx, w.store.namespace)
		if err != nil {
			return State{}, err
		}
		if workflowDigest != w.workflowSubjectDigest {
			workflowRevision++
		}
	}
	if _, err := w.tx.Exec(ctx, `INSERT INTO cao_bundle_versions(namespace,collection,id,attempt,observed_at,digest)
		SELECT $1,'$runs',k.id,k.attempt,k.observed_at,k.digest FROM affected_runs k
		JOIN cao_run_accounting a ON a.namespace=$1 AND a.collection='$runs' AND a.run_id=k.id
		ON CONFLICT(namespace,collection,id) DO UPDATE SET attempt=excluded.attempt,observed_at=excluded.observed_at,digest=excluded.digest`,
		w.store.namespace); err != nil {
		return State{}, err
	}
	state := State{Ready: true, DataRevision: dataRevision, EvaluatedAt: w.evaluatedAt.UTC(),
		WorkflowSubjectDigest: workflowDigest, WorkflowSubjectRevision: workflowRevision, Counts: map[string]int{}}
	for source, value := range w.accounting {
		state.Counts[source] = int(value.records)
	}
	if err := w.tx.QueryRow(ctx, `UPDATE cao_state SET revision=revision+1,data_revision=$2,evaluated_at=$3,
		workflow_subject_revision=$4,workflow_subject_digest=$5,schema_version=$6
		WHERE namespace=$1 AND revision=$7 RETURNING revision`, w.store.namespace, dataRevision,
		w.evaluatedAt, workflowRevision, workflowDigest, model.SchemaVersion, w.revision).Scan(&state.Revision); err != nil {
		return State{}, err
	}
	if err := w.tx.Commit(ctx); err != nil {
		return State{}, err
	}
	w.tx = nil
	return state, nil
}

func (w *Writer) refreshBundleAccounting(ctx context.Context, changed map[string]bool) error {
	before := map[string]collectionAccounting{}
	for source, state := range w.accounting {
		if entityTables[source].partitioned {
			before[source] = *state
			state.records, state.evidenceAt = 0, nil
		}
	}
	rows, err := w.tx.Query(ctx, `SELECT collection,sum(records)::bigint,max(next_ordinal),max(evidence_at)
		FROM cao_run_accounting WHERE namespace=$1 GROUP BY collection`, w.store.namespace)
	if err != nil {
		return err
	}
	for rows.Next() {
		var source string
		var count, next int64
		var clock *time.Time
		if err := rows.Scan(&source, &count, &next, &clock); err != nil {
			rows.Close()
			return err
		}
		state := w.accounting[source]
		if state == nil {
			rows.Close()
			return errors.New("unknown Run-accounting source")
		}
		state.records, state.nextOrdinal, state.evidenceAt = count, max(next, state.nextOrdinal), clock
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for source, previous := range before {
		current := w.accounting[source]
		sameClock := previous.evidenceAt == nil && current.evidenceAt == nil ||
			previous.evidenceAt != nil && current.evidenceAt != nil && previous.evidenceAt.Equal(*current.evidenceAt)
		changed[source] = changed[source] || previous.records != current.records || !sameClock
	}
	for source, touched := range changed {
		table := entityTables[source]
		if !touched || table.partitioned {
			continue
		}
		if err := w.tx.QueryRow(ctx, "SELECT max("+entityClockSQL(table, "")+") FROM "+
			query.SQLIdentifier(table.name)+" WHERE namespace=$1", w.store.namespace).
			Scan(&w.accounting[source].evidenceAt); err != nil {
			return err
		}
	}
	return w.saveAccounting(ctx, w.revision+1)
}

func (w *Writer) updateBundleQuality(ctx context.Context, changed map[string]bool) error {
	var sources []string
	for source, touched := range changed {
		if touched {
			sources = append(sources, source)
		}
	}
	_, err := w.tx.Exec(ctx, `UPDATE cao_quality q SET
		availability=CASE WHEN a.records>0 THEN 'available'
			WHEN EXISTS(SELECT 1 FROM run_bundle_coverage c JOIN affected_runs k ON k.id=c.id
				WHERE c.collection=q.collection AND c.coverage='unavailable') THEN 'unavailable' ELSE 'empty' END,
		completeness='partial',
		freshness=CASE WHEN NOT EXISTS(SELECT 1 FROM run_bundle_coverage c JOIN affected_runs k ON k.id=c.id
			WHERE c.collection=q.collection AND c.coverage='complete') THEN 'unknown' ELSE q.freshness END,
		as_of=a.evidence_at
		FROM cao_ingestion_accounting a WHERE q.namespace=$1 AND a.namespace=q.namespace AND a.collection=q.collection
		AND (q.collection=ANY($2) OR EXISTS(SELECT 1 FROM run_bundle_coverage c JOIN affected_runs k ON k.id=c.id
			WHERE c.collection=q.collection))`, w.store.namespace, sources)
	return err
}

func (w *Writer) validateBundleRelationships(ctx context.Context) error {
	if _, err := w.tx.Exec(ctx, "SET CONSTRAINTS ALL IMMEDIATE"); err != nil {
		return fmt.Errorf("validate Run-bundle relationships: %w", err)
	}
	checks := []string{
		`SELECT 1 FROM runs r JOIN affected_runs k ON k.id=r.id AND k.run_at=r.run_at
			JOIN workflows p ON p.namespace=r.namespace AND p.id=r.workflow_id
			WHERE r.namespace=$1 AND r.repository_id<>p.repository_id LIMIT 1`,
	}
	for _, pair := range []struct{ child, parent, field string }{
		{"experiment_assignments", "experiments", "experiment_id"},
		{"grader_observations", "graders", "grader_id"},
		{"eval_observations", "evals", "eval_id"},
	} {
		checks = append(checks, "SELECT 1 FROM "+pair.child+` o JOIN affected_runs k ON k.id=o.run_id AND k.run_at=o.run_at
			JOIN runs r ON r.namespace=o.namespace AND r.id=o.run_id AND r.run_at=o.run_at
			JOIN `+pair.parent+` d ON d.namespace=o.namespace AND d.id=o.`+pair.field+`
			WHERE o.namespace=$1 AND r.workflow_id<>d.workflow_id LIMIT 1`)
	}
	for _, statement := range checks {
		var invalid int
		err := w.tx.QueryRow(ctx, statement, w.store.namespace).Scan(&invalid)
		if err == nil {
			return errors.New("run-bundle evidence references a different workflow or repository")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	return nil
}

func (w *Writer) reconcileDailyRunBundles(ctx context.Context) error {
	if !w.store.dailyCounts {
		return nil
	}
	return reconcileDailyCountKeys(ctx, w.store.namespace, "", "affected_runs", w.ingestedAt,
		w.capacitySQL(false).exec, func(ctx context.Context) (string, error) {
			var last string
			err := w.tx.QueryRow(ctx, "SELECT coalesce(max(id),'') FROM daily_run_keys").Scan(&last)
			return last, err
		})
}
