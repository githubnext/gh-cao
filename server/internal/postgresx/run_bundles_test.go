package postgresx

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func seedBundleRuns(t *testing.T, store *Store, ids ...string) time.Time {
	t.Helper()
	at := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	sources := map[string][]model.Row{
		"$repositories": {{"id": "repository", "owner": "octo", "name": "api"}},
		"$workflows":    {{"id": "workflow", "repositoryId": "repository", "path": ".github/workflows/test.md"}},
	}
	if len(ids) == 0 {
		ids = []string{"run-a", "run-b"}
	}
	for index, id := range ids {
		sources["$runs"] = append(sources["$runs"], model.Row{
			"id": id, "githubRunId": strconv.Itoa(index + 101), "attempt": 1, "repositoryId": "repository", "workflowId": "workflow",
			"owner": "octo", "repository": "api", "workflowPath": ".github/workflows/test.md",
			"createdAt": at, "startedAt": at, "updatedAt": at, "status": "in_progress", "inputTokens": 10,
		})
		sources["$audits"] = append(sources["$audits"], model.Row{
			"id": "audit:" + id, "runId": id, "source": "session", "type": "agent.step", "timestamp": at,
		})
		sources["$tools"] = append(sources["$tools"], model.Row{"id": "tool:" + id, "runId": id, "name": "read"})
	}
	for _, source := range tableNames() {
		for _, row := range sources[source] {
			if err := writer.Append(t.Context(), source, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := writer.Publish(t.Context(), "bundle-baseline"); err != nil {
		t.Fatal(err)
	}
	return at
}

func bundleUpdate(at time.Time) RunBundlePublication {
	return RunBundlePublication{
		Version: 1, ObservedAt: time.Now().UTC().Truncate(time.Microsecond),
		Runs: []RunBundle{{Run: model.Row{
			"id": "run-a", "attempt": 1, "repositoryId": "repository", "workflowId": "workflow",
			"updatedAt": at.Add(time.Minute), "status": "completed", "conclusion": "failure",
		}}},
		Parents: map[string][]model.Row{
			"repositories": {{"id": "repository", "owner": "octo", "name": "api"}},
			"workflows":    {{"id": "workflow", "repositoryId": "repository", "path": ".github/workflows/test.md"}},
		},
	}
}

func bundleTuple(t *testing.T, store *Store, table, id string) string {
	t.Helper()
	var tuple string
	if err := store.db.QueryRowContext(t.Context(), "SELECT tableoid::text||':'||ctid::text||':'||xmin::text FROM "+
		query.SQLIdentifier(table)+" WHERE namespace=$1 AND id=$2", store.namespace, id).Scan(&tuple); err != nil {
		t.Fatal(err)
	}
	return tuple
}

func TestRunBundlesScopedPublicationCoverageAndReplay(t *testing.T) {
	store, _ := nativeTestStore(t)
	at := seedBundleRuns(t, store)
	runTuple, workflowTuple := bundleTuple(t, store, "runs", "run-b"), bundleTuple(t, store, "workflows", "workflow")
	input := bundleUpdate(at)
	input.Runs[0].Run["inputTokens"] = nil
	input.Runs[0].Collections = map[string]RunBundleCollection{
		"tools":  {Coverage: "complete", Rows: []model.Row{}},
		"audits": {Coverage: "unavailable"},
	}
	state, err := store.PublishRunBundles(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if state.Counts["$runs"] != 2 || state.Counts["$tools"] != 1 || state.Counts["$audits"] != 2 {
		t.Fatalf("delta replaced unrelated evidence or treated unavailable as empty: %+v", state)
	}
	if runTuple != bundleTuple(t, store, "runs", "run-b") || workflowTuple != bundleTuple(t, store, "workflows", "workflow") {
		t.Fatal("one-Run delta rewrote an unrelated Run or unchanged shared parent")
	}
	var missing, explicitNull bool
	if err := store.db.QueryRowContext(t.Context(), `SELECT created_at=$2,input_tokens IS NULL
		FROM runs WHERE namespace=$1 AND id='run-a'`, store.namespace, at).Scan(&missing, &explicitNull); err != nil ||
		!missing || !explicitNull {
		t.Fatalf("missing/null merge semantics changed: %v %v %v", missing, explicitNull, err)
	}
	replayed, err := store.PublishRunBundles(t.Context(), input)
	if err != nil || !reflect.DeepEqual(replayed, state) {
		t.Fatalf("identical accepted bundle republished: %+v %+v %v", state, replayed, err)
	}
	input.Runs[0].Run["status"] = "queued"
	if _, err := store.PublishRunBundles(t.Context(), input); err == nil {
		t.Fatal("conflicting same-version evidence was accepted")
	}
	input.ObservedAt = input.ObservedAt.Add(-time.Second)
	stale, err := store.PublishRunBundles(t.Context(), input)
	if err != nil || stale.Revision != state.Revision {
		t.Fatalf("out-of-order source observation changed publication: %+v %v", stale, err)
	}
	actual, err := store.State(t.Context())
	if err != nil || !reflect.DeepEqual(actual.Counts, state.Counts) {
		t.Fatalf("receipt counts differ from canonical rows: %+v %+v %v", actual, state, err)
	}
}

func TestRunBundlesWinningAttemptAndCrossDayMovement(t *testing.T) {
	store, _ := nativeTestStore(t)
	at := seedBundleRuns(t, store)
	input := bundleUpdate(at)
	moved := at.AddDate(0, 0, -1)
	input.Runs[0].Run["createdAt"], input.Runs[0].Run["startedAt"] = moved, moved
	state, err := store.PublishRunBundles(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	var children int
	if err := store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM tools
		WHERE namespace=$1 AND run_id='run-a' AND run_at=$2`, store.namespace, moved).Scan(&children); err != nil || children != 1 {
		t.Fatalf("cross-day move lost retained same-attempt children: %d %v", children, err)
	}
	input.ObservedAt = input.ObservedAt.Add(time.Second)
	input.Runs[0].Run = model.Row{"id": "run-a", "attempt": json.Number("9007199254740993"),
		"repositoryId": "repository", "workflowId": "workflow", "createdAt": at, "startedAt": at, "status": "queued"}
	newer, err := store.PublishRunBundles(t.Context(), input)
	if err != nil || newer.Revision != state.Revision+1 || newer.Counts["$tools"] != 1 || newer.Counts["$audits"] != 1 {
		t.Fatalf("winning attempt mixed old children: %+v %v", newer, err)
	}
	var attempt string
	var noOldMetrics bool
	if err := store.db.QueryRowContext(t.Context(), `SELECT attempt::text,input_tokens IS NULL AND conclusion IS NULL
		FROM runs WHERE namespace=$1 AND id='run-a'`, store.namespace).Scan(&attempt, &noOldMetrics); err != nil ||
		attempt != "9007199254740993" || !noOldMetrics {
		t.Fatalf("new attempt lost precision or inherited old facts: %s %v %v", attempt, noOldMetrics, err)
	}
	input.ObservedAt = input.ObservedAt.Add(time.Second)
	input.Runs[0].Run["attempt"] = 2
	older, err := store.PublishRunBundles(t.Context(), input)
	if err != nil || older.Revision != newer.Revision {
		t.Fatalf("older attempt won by arrival time: %+v %v", older, err)
	}
}

func TestRunBundlesAtomicRejectionAndCapacity(t *testing.T) {
	store, _ := nativeTestStore(t)
	at := seedBundleRuns(t, store)
	before, err := store.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	input := bundleUpdate(at)
	input.Runs[0].Collections = map[string]RunBundleCollection{
		"graderObservations": {Coverage: "complete", Rows: []model.Row{
			{"id": "orphan", "runId": "run-a", "graderId": "missing"},
		}},
	}
	if _, err := store.PublishRunBundles(t.Context(), input); err == nil {
		t.Fatal("missing parent did not reject the delta")
	}
	input.Runs[0].Collections = nil
	store.runCapacity = runCapacity{rows: 2, bytes: 1}
	if _, err := store.PublishRunBundles(t.Context(), input); err == nil {
		t.Fatal("oversized newest subtree was accepted")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.PublishRunBundles(canceled, input); err == nil {
		t.Fatal("canceled delta was accepted")
	}
	after, err := store.State(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected publication changed canonical state: %+v %+v %v", before, after, err)
	}
	store.runCapacity = runCapacity{rows: 2, bytes: query.MaxWorkingBytes}
	input.Runs[0].Run["id"] = "run-c"
	input.Runs[0].Run["createdAt"] = at.Add(2 * time.Minute)
	input.Runs[0].Run["startedAt"] = at.Add(2 * time.Minute)
	input.Runs[0].Run["owner"], input.Runs[0].Run["repository"] = "octo", "api"
	input.Runs[0].Run["workflowPath"] = ".github/workflows/test.md"
	state, err := store.PublishRunBundles(t.Context(), input)
	if err != nil || state.Counts["$runs"] != 2 {
		t.Fatalf("capacity admission failed: %+v %v", state, err)
	}
	var daily int
	if err := store.db.QueryRowContext(t.Context(), `SELECT sum(total) FROM daily_run_counts WHERE namespace=$1`,
		store.namespace).Scan(&daily); err != nil || daily != 3 {
		t.Fatalf("eviction erased historical daily contributions: %d %v", daily, err)
	}
	for source, count := range state.Counts {
		var actual int
		if err := store.db.QueryRowContext(t.Context(), "SELECT count(*) FROM "+query.SQLIdentifier(entityTables[source].name)+
			" WHERE namespace=$1", store.namespace).Scan(&actual); err != nil || actual != count {
			t.Fatalf("%s reported=%d actual=%d: %v", source, count, actual, err)
		}
	}
}
