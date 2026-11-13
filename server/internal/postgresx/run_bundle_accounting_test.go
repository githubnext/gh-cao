package postgresx

import (
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

func TestRunBundlesCuratedClockAndAccountingInvalidation(t *testing.T) {
	store, _ := nativeTestStore(t)
	at := seedBundleRuns(t, store)
	input := bundleUpdate(at)
	clock := time.Now().UTC().Truncate(time.Microsecond).Add(2 * time.Hour)
	input.Runs[0].Collections = map[string]RunBundleCollection{"audits": {Coverage: "partial", Rows: []model.Row{
		{"id": "curatable", "runId": "run-a", "source": "gh-aw-logs", "type": "workflow_run_comparison",
			"status": "unavailable", "summary": "No baseline comparison", "timestamp": clock},
	}}}
	state, err := store.PublishRunBundles(t.Context(), input)
	if err != nil || state.Counts["$audits"] != 2 || !state.EvaluatedAt.Equal(clock) {
		t.Fatalf("input clock or curated output count lost: %+v %v", state, err)
	}
	if err := store.RecordAdmittedRun(t.Context(), admissionIdentity()); err != nil {
		t.Fatal(err)
	}
	input.ObservedAt = input.ObservedAt.Add(time.Microsecond)
	input.Runs[0].Collections = nil
	input.Runs[0].Run["updatedAt"] = at.Add(2 * time.Minute)
	state, err = store.PublishRunBundles(t.Context(), input)
	if err != nil || !state.EvaluatedAt.Equal(clock) || state.Counts["$runs"] != 3 {
		t.Fatalf("independent writer invalidation lost authoritative counts or input clocks: %+v %v", state, err)
	}
	input.ObservedAt = input.ObservedAt.Add(time.Microsecond)
	input.Runs[0].Run["updatedAt"] = at.Add(3 * time.Minute)
	input.Runs[0].Collections = map[string]RunBundleCollection{"audits": {Coverage: "complete"}}
	state, err = store.PublishRunBundles(t.Context(), input)
	if err != nil || state.Counts["$audits"] != 1 || !state.EvaluatedAt.Before(clock) {
		t.Fatalf("complete replacement kept a removed maximum clock: %+v %v", state, err)
	}
	actual, err := store.State(t.Context())
	if err != nil || actual.Counts["$audits"] != state.Counts["$audits"] {
		t.Fatalf("revision-qualified bookkeeping diverged from native rows: %+v %v", actual, err)
	}
}

func TestRunBundlesCompleteCollectionDeletesNullSource(t *testing.T) {
	store, _ := nativeTestStore(t)
	at := seedBundleRuns(t, store)
	input := bundleUpdate(at)
	input.Runs[0].Collections = map[string]RunBundleCollection{"audits": {Coverage: "partial", Rows: []model.Row{
		{"id": "no-source", "runId": "run-a", "source": nil, "type": "audit.finding", "timestamp": at},
	}}}
	if _, err := store.PublishRunBundles(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	input.ObservedAt = input.ObservedAt.Add(time.Microsecond)
	input.Runs[0].Collections["audits"] = RunBundleCollection{Coverage: "complete"}
	state, err := store.PublishRunBundles(t.Context(), input)
	if err != nil || state.Counts["$audits"] != 1 {
		t.Fatalf("NULL source incorrectly acquired protected ownership: %+v %v", state, err)
	}
}
