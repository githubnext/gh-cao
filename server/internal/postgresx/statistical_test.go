package postgresx

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func TestStatisticalNativeSQLBoundary(t *testing.T) {
	store, _ := nativeTestStore(t)
	publishStatisticalFixture(t, store, "statistical-boundary", "")
	corpus, err := os.ReadFile("../../../dashboard/site/src/data/queries/database.json")
	if err != nil {
		t.Fatal(err)
	}
	var definitions []query.Definition
	if err := json.Unmarshal(corpus, &definitions); err != nil {
		t.Fatal(err)
	}
	var consumers []query.Definition
	if err := json.Unmarshal([]byte(`[
		{"name":"sample-success","from":"statistical-runs","filter":{"predicates":[{"field":"run-conclusion","equals":"success"}]}},
		{"name":"sample-empty","from":"statistical-runs","filter":{"predicates":[{"field":"run","equals":"not-present"}]}}
	]`), &consumers); err != nil {
		t.Fatal(err)
	}
	definitions = slices.Concat(definitions, consumers)
	result, metrics, err := store.ExecuteSQLPlan(t.Context(), definitions, []string{
		"sample-success", "sample-empty", "runs", "statistical-tools", "statistical-domains", "statistical-skills", "statistical-audits",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result["sample-success"].Rows) != 5 || len(result["sample-empty"].Rows) != 0 || len(result["runs"].Rows) != 9 {
		t.Fatalf("statistical selection changed exact evidence: %+v", result)
	}
	for _, name := range []string{"sample-success", "sample-empty"} {
		frame, ok := result[name].Metadata["statistical-frame"].(map[string]any)
		if !ok || frame["population-count"] != 6 || frame["selected-count"] != 6 || frame["capacity"] != 64000 {
			t.Fatalf("consumer predicates changed frozen frame: %v", result[name].Metadata)
		}
	}
	if len(metrics.FallbackOperations) != 0 {
		t.Fatalf("statistical consumer bypassed native SQL: %+v", metrics)
	}
	for _, row := range result["sample-success"].Rows {
		frame := result["sample-success"].Metadata["statistical-frame"].(map[string]any)
		if row["statistical-frame-id"] != frame["frame-id"] {
			t.Fatal("selected run lost its frame binding")
		}
		if row["sample-weight"] != row["statistical-weight"] || row["sample-stratum"] != row["statistical-stratum"] ||
			row["sample-probability"] != row["statistical-probability"] {
			t.Fatalf("public sample fields do not preserve frozen weights: %v", row)
		}
	}
	for _, source := range []string{"statistical-tools", "statistical-domains", "statistical-skills", "statistical-audits"} {
		if len(result[source].Rows) != 2 {
			t.Fatalf("owned observations were independently sampled or expired children retained: %s %+v", source, result[source])
		}
		for _, row := range result[source].Rows {
			frame := result[source].Metadata["statistical-frame"].(map[string]any)
			if row["statistical-frame-id"] != frame["frame-id"] {
				t.Fatalf("owned observation lost its run's frame binding: %s", source)
			}
		}
	}
}

func publishStatisticalFixture(t *testing.T, store *Store, revision, watermark string) {
	t.Helper()
	content, err := os.ReadFile("../../../dashboard/site/test/fixtures/statistical-sampling.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Rows      []model.Row
		Watermark string
	}
	if err := json.Unmarshal(content, &fixture); err != nil {
		t.Fatal(err)
	}
	if watermark != "" {
		fixture.Watermark = watermark
	}
	asOf := time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)
	if err := store.RunPartitionMaintenance(t.Context(), asOf, 30, store.linkedRetentionDays); err != nil {
		t.Fatal(err)
	}
	w, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort(t.Context())
	w.ingestedAt = asOf
	for _, input := range []struct {
		source string
		row    model.Row
	}{
		{"$campaigns", model.Row{"id": "campaign", "slug": "sample"}},
		{"$repositories", model.Row{"id": "repo", "owner": "o", "name": "r"}},
		{"$workflows", model.Row{"id": "workflow", "repositoryId": "repo", "campaignId": "campaign", "path": "worker.md"}},
		{"$workflows", model.Row{"id": "experiment-workflow", "repositoryId": "repo", "campaignId": "campaign", "path": "experiment.md"}},
		{"$experiments", model.Row{"id": "assignment-experiment", "workflowId": "workflow"}},
		{"$experiments", model.Row{"id": "cross-experiment", "workflowId": "experiment-workflow"}},
		{"$graders", model.Row{"id": "grader", "workflowId": "workflow"}},
		{"$evals", model.Row{"id": "eval", "workflowId": "workflow"}},
	} {
		if err := w.Append(t.Context(), input.source, input.row); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range fixture.Rows {
		row["repositoryId"], row["workflowId"] = "repo", "workflow"
		if err := w.Append(t.Context(), "$runs", row); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []string{"$tools", "$domains", "$skills", "$audits", "$friction", "$issues",
		"$experimentAssignments", "$graderObservations", "$evalObservations"} {
		for _, row := range []model.Row{
			{"id": "a", "runId": fixture.Rows[0]["id"]}, {"id": "b", "runId": fixture.Rows[0]["id"]},
			{"id": "expired", "runId": fixture.Rows[7]["id"]},
		} {
			switch source {
			case "$experimentAssignments":
				row["experimentId"] = "assignment-experiment"
			case "$graderObservations":
				row["graderId"], row["experimentId"] = "grader", "cross-experiment"
			case "$evalObservations":
				row["evalId"], row["experimentId"] = "eval", "cross-experiment"
			}
			if err := w.Append(t.Context(), source, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Quality(t.Context(), "$runs", model.Metadata{
		"as-of": fixture.Watermark, "completeness": "complete", "availability": "available",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Publish(t.Context(), revision); err != nil {
		t.Fatal(err)
	}
}
