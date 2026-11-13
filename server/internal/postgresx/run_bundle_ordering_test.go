package postgresx

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

func TestRunBundlesFenceSharedParentsAndSnapshotReplay(t *testing.T) {
	store, _ := nativeTestStore(t)
	at := seedBundleRuns(t, store)
	older := bundleUpdate(at)
	newer := bundleUpdate(at)
	newer.Runs[0].Run["id"] = "run-b"
	newer.Parents["workflows"][0]["name"] = "newer observation"
	if _, err := store.PublishRunBundles(t.Context(), newer); err != nil {
		t.Fatal(err)
	}
	older.Parents["workflows"][0]["name"] = "older observation"
	state, err := store.PublishRunBundles(t.Context(), older)
	if err != nil {
		t.Fatal(err)
	}
	var name string
	if err := store.db.QueryRowContext(t.Context(), `SELECT name FROM workflows WHERE namespace=$1 AND id='workflow'`,
		store.namespace).Scan(&name); err != nil || name != "newer observation" {
		t.Fatalf("another Run rewound a shared parent: %s %v", name, err)
	}
	older.ObservedAt = newer.ObservedAt
	if _, err := store.PublishRunBundles(t.Context(), older); err == nil {
		t.Fatal("conflicting same-version shared parent was accepted")
	}
	seedBundleRuns(t, store)
	snapshot, err := store.State(t.Context())
	if err != nil || snapshot.Revision != state.Revision+1 {
		t.Fatalf("snapshot did not establish the next authoritative publication: %+v %v", snapshot, err)
	}
	snapshot.EvaluatedAt = snapshot.EvaluatedAt.UTC()
	replayed, err := store.PublishRunBundles(t.Context(), newer)
	if err != nil || !reflect.DeepEqual(replayed, snapshot) {
		t.Fatalf("old delta crossed the full-reconciliation fence: %+v %v", replayed, err)
	}
}

func TestRunBundlesConcurrentReplayCancellationAndRuntimeScope(t *testing.T) {
	owner, config := nativeTestStore(t)
	store := valueRuntimeRoleStore(t, owner, config)
	at := seedBundleRuns(t, store)
	foreign, err := NewConfig(t.Context(), config, "another-bundle-namespace")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = foreign.Close() }()
	seedBundleRuns(t, foreign)
	foreignBefore, err := foreign.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	foreignTuple := bundleTuple(t, foreign, "runs", "run-a")
	input := bundleUpdate(at)
	var wait sync.WaitGroup
	failures := make(chan error, 4)
	for range 4 {
		wait.Go(func() {
			// Each writer owns its decoded input, as independent stream requests do.
			request := bundleUpdate(at)
			request.ObservedAt = input.ObservedAt
			state, err := store.PublishRunBundles(t.Context(), request)
			if err == nil && state.Revision != 2 {
				err = errors.New("concurrent replay published more than one revision")
			}
			failures <- err
		})
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.ExecContext(t.Context(), `SELECT cao_analyze_native(ARRAY['$runs','not-a-source'])`); err == nil {
		t.Fatal("runtime statistics capability accepted an arbitrary relation")
	}
	locked, err := store.beginCanonicalWrite(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	input.ObservedAt = input.ObservedAt.Add(time.Microsecond)
	input.Runs[0].Run["inputTokens"] = 20
	_, err = store.PublishRunBundles(ctx, input)
	locked.Abort(t.Context())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation did not interrupt the namespace lock: %v", err)
	}
	after, err := store.State(t.Context())
	if err != nil || after.Revision != 2 {
		t.Fatalf("canceled writer advanced canonical publication: %+v %v", after, err)
	}
	foreignAfter, err := foreign.State(t.Context())
	if err != nil || !reflect.DeepEqual(foreignAfter, foreignBefore) ||
		foreignTuple != bundleTuple(t, foreign, "runs", "run-a") {
		t.Fatalf("delta escaped its configured namespace: %+v %v", foreignAfter, err)
	}
}

func TestRunBundlesPreserveIndependentConclusionAndValue(t *testing.T) {
	store, _ := nativeTestStore(t)
	at := seedBundleRuns(t, store)
	state, err := store.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	value := model.Row{"id": "measurement", "repositoryId": "repository", "repository": "octo/api",
		"valueId": "worker.efficiency", "value": nil, "operational-value-role": "primary",
		"operational-value-name": "Efficiency", "operational-value-direction": "increase",
		"maturity-status": "unavailable", "coverage-status": "missing", "missing-reason": "no-eligible-runs",
		"timestamp": at.Format(time.RFC3339Nano)}
	if _, err := store.ReplaceOperationalValues(t.Context(), state.Revision, []model.Row{value}); err != nil {
		t.Fatal(err)
	}
	valueTuple := bundleTuple(t, store, "operational_values", "measurement")
	identity := admissionIdentity()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.RecordConcludedRun(t.Context(), identity, canonicalConclusionInput(identity), now); err != nil {
		t.Fatal(err)
	}
	var id, repository, workflow string
	if err := store.db.QueryRowContext(t.Context(), `SELECT id,repository_id,workflow_id FROM runs
		WHERE namespace=$1 AND github_run_id=$2`, store.namespace, identity.RunID).Scan(&id, &repository, &workflow); err != nil {
		t.Fatal(err)
	}
	input := bundleUpdate(at)
	input.Parents = nil
	input.Runs[0] = RunBundle{
		Run: model.Row{"id": id, "attempt": 2, "repositoryId": repository, "workflowId": workflow,
			"updatedAt": now.Add(time.Minute), "status": "completed", "conclusion": "success", "inputTokens": nil},
		Collections: map[string]RunBundleCollection{"audits": {Coverage: "complete"}, "graderObservations": {Coverage: "complete"}},
	}
	if _, err := store.PublishRunBundles(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	var tokens, observations int
	if err := store.db.QueryRowContext(t.Context(), `SELECT input_tokens FROM runs WHERE namespace=$1 AND id=$2`,
		store.namespace, id).Scan(&tokens); err != nil || tokens != 30 {
		t.Fatalf("API-only null erased independently collected usage: %d %v", tokens, err)
	}
	if err := store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM grader_observations WHERE namespace=$1 AND run_id=$2`,
		store.namespace, id).Scan(&observations); err != nil || observations != 1 {
		t.Fatalf("complete source-owned replacement removed conclusion-owned evidence: %d %v", observations, err)
	}
	input.ObservedAt = input.ObservedAt.Add(time.Microsecond)
	input.Runs[0].Collections["graderObservations"] = RunBundleCollection{Coverage: "partial", Rows: []model.Row{
		{"id": "grader-observation:conclusion-admission:forged", "runId": id, "graderId": "unused"},
	}}
	if _, err := store.PublishRunBundles(t.Context(), input); err == nil {
		t.Fatal("delta impersonated independently owned conclusion evidence")
	}
	input.Runs[0].Collections = nil
	input.Runs[0].Run["attempt"] = 3
	if _, err := store.PublishRunBundles(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM grader_observations WHERE namespace=$1 AND run_id=$2`,
		store.namespace, id).Scan(&observations); err != nil || observations != 0 {
		t.Fatalf("winning attempt inherited old independent observations: %d %v", observations, err)
	}
	if valueTuple != bundleTuple(t, store, "operational_values", "measurement") {
		t.Fatal("delta rewrote independently owned Value evidence")
	}
}
