package postgresx

import (
	"maps"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func TestRunBundlePublicationMatchesAuthoritativeSnapshot(t *testing.T) {
	store, config := nativeTestStore(t)
	ids := []string{"github:run:octo/api:101", "github:run:octo/api:102"}
	at := seedBundleRuns(t, store, ids...)
	reference, err := NewConfig(t.Context(), config, "bundle-reference")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reference.Close() }()
	if seededAt := seedBundleRuns(t, reference, ids...); !seededAt.Equal(at) {
		t.Fatal("fixture clock crossed an hour boundary")
	}
	input := bundleUpdate(at)
	input.Runs[0].Run["id"] = ids[0]
	input.Runs[0].Run["inputTokens"] = nil
	delta, err := store.PublishRunBundles(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := reference.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	for source, rows := range map[string][]model.Row{
		"$repositories": input.Parents["repositories"], "$workflows": input.Parents["workflows"],
	} {
		for _, row := range rows {
			if err := writer.Append(t.Context(), source, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	for index, id := range ids {
		run := model.Row{"id": id, "attempt": 1, "repositoryId": "repository", "workflowId": "workflow",
			"githubRunId": strconv.Itoa(index + 101),
			"owner":       "octo", "repository": "api", "workflowPath": ".github/workflows/test.md",
			"createdAt": at, "startedAt": at, "updatedAt": at, "status": "in_progress", "inputTokens": 10}
		if id == ids[0] {
			maps.Copy(run, input.Runs[0].Run)
		}
		for source, row := range map[string]model.Row{
			"$runs": run,
			"$audits": {"id": "audit:" + id, "runId": id, "source": "session", "type": "agent.step",
				"timestamp": at},
			"$tools": {"id": "tool:" + id, "runId": id, "name": "read"},
		} {
			if err := writer.Append(t.Context(), source, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Quality(t.Context(), "$runs", model.Metadata{
		"completeness": "partial", "freshness": "unknown", "as-of": at.Add(time.Minute).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := writer.Publish(t.Context(), "equivalent-snapshot")
	if err != nil || !reflect.DeepEqual(delta.Counts, snapshot.Counts) || !delta.EvaluatedAt.Equal(snapshot.EvaluatedAt) {
		t.Fatalf("delta counts or evidence clocks diverged: %+v %+v %v", delta, snapshot, err)
	}
	for _, table := range []string{"runs", "audits", "tools", "workflows", "repositories",
		"daily_run_counts", "run_count_contributions", "cao_quality"} {
		rows := func(store *Store) string {
			t.Helper()
			var content string
			if err := store.db.QueryRowContext(t.Context(), `SELECT coalesce(jsonb_agg(
				to_jsonb(t)-'namespace'-'ordinal' ORDER BY to_jsonb(t)-'namespace'-'ordinal'),'[]'::jsonb)::text
				FROM `+query.SQLIdentifier(table)+` t WHERE namespace=$1`, store.namespace).Scan(&content); err != nil {
				t.Fatal(err)
			}
			return content
		}
		if actual, expected := rows(store), rows(reference); actual != expected {
			t.Fatalf("%s delta differs from authoritative snapshot:\n%s\n%s", table, actual, expected)
		}
	}
	actual, expected := statisticalResults(t, store), statisticalResults(t, reference)
	for _, source := range []string{"runs", "tools", "statistical-runs", "statistical-tools", "statistical-audits"} {
		for _, result := range []model.Source{actual[source], expected[source]} {
			for _, row := range result.Rows {
				// Publication identity differs; population, selection, weights and evidence must not.
				delete(row, "statistical-frame-id")
			}
		}
		if !reflect.DeepEqual(actual[source].Rows, expected[source].Rows) {
			t.Fatalf("%s query payload diverged:\n%+v\n%+v", source, actual[source].Rows, expected[source].Rows)
		}
	}
	frame := actual["statistical-runs"].Metadata["statistical-frame"].(map[string]any)
	if len(actual["statistical-runs"].Rows) != 1 || frame["source-data-revision"] != delta.DataRevision {
		t.Fatalf("delta published stale membership or frame provenance: %+v", frame)
	}
}

func TestRunBundlesReconstructAfterRetention(t *testing.T) {
	store, _ := nativeTestStore(t)
	at := seedBundleRuns(t, store)
	clock := time.Now().UTC().Truncate(time.Microsecond)
	input := bundleUpdate(at)
	old := at.AddDate(0, 0, -8)
	input.Runs[0].Run["createdAt"], input.Runs[0].Run["startedAt"] = old, old
	input.Runs[0].Run["updatedAt"] = clock
	if _, err := store.PublishRunBundles(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	var children int
	if err := store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM tools WHERE namespace=$1 AND run_id='run-a'`,
		store.namespace).Scan(&children); err != nil || children != 0 {
		t.Fatalf("moving beyond linked retention kept old detail: %d %v", children, err)
	}
	if err := store.RunPartitionMaintenance(t.Context(), clock.Add(24*time.Hour), 30, 7); err != nil {
		t.Fatal(err)
	}
	input = bundleUpdate(at)
	input.Runs[0].Run["id"] = "run-b"
	input.Runs[0].Run["updatedAt"] = clock.Add(time.Minute)
	state, err := store.PublishRunBundles(t.Context(), input)
	if err != nil || state.Counts["$runs"] != 2 || state.Counts["$tools"] != 1 {
		t.Fatalf("retention left a trusted stale Run footprint: %+v %v", state, err)
	}
}
