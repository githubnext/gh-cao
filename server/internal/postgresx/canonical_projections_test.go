package postgresx

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func canonicalProjectionDefinitions(t *testing.T) []query.Definition {
	t.Helper()
	content, err := os.ReadFile("../../../dashboard/site/src/data/queries/database.json")
	if err != nil {
		t.Fatal(err)
	}
	var definitions []query.Definition
	if err := json.Unmarshal(content, &definitions); err != nil {
		t.Fatal(err)
	}
	return definitions
}

func TestCanonicalProjectionsCompileWithoutReportSources(t *testing.T) {
	reader := &readTransaction{store: &Store{namespace: "canonical-projections"}}
	var sources []string
	_, err := query.CompileSQL(canonicalProjectionDefinitions(t), []string{
		"work-items", "detection-observations", "safe-output-performance", "mcp-calls",
	}, func(name string) (query.SQLRelation, error) {
		sources = append(sources, name)
		return reader.entitySQLSource(name)
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(sources)
	if !reflect.DeepEqual(sources, []string{
		"$audits", "$campaigns", "$issues", "$outputLifecycles", "$repositories", "$runs", "$tools", "$workflows",
	}) {
		t.Fatalf("projections must read canonical evidence only: %v", sources)
	}
}

func TestOutputLifecycleEventClocksExecuteNatively(t *testing.T) {
	store, _ := nativeTestStore(t)
	content, err := os.ReadFile("../../../dashboard/site/test/fixtures/canonical-work-items.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string][]model.Row
	if err := json.Unmarshal(content, &fixture); err != nil {
		t.Fatal(err)
	}
	asOf, err := time.Parse(time.RFC3339, fixture["repositories"][0]["observedAt"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RunPartitionMaintenance(t.Context(), asOf, 90, store.linkedRetentionDays); err != nil {
		t.Fatal(err)
	}
	acceptedAt := "2026-09-17T13:00:00Z"
	fixture["outputLifecycles"][1]["adoptionState"] = "accepted"
	fixture["outputLifecycles"][1]["adoptionSignal"] = "completed"
	fixture["outputLifecycles"][1]["adoptionAt"] = acceptedAt
	fixture["outputLifecycles"][1]["adoptionObservedAt"] = acceptedAt
	fixture["outputLifecycles"][1]["adoptionSource"] = "github-api"
	fixture["outputLifecycles"][1]["observedAt"] = acceptedAt

	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	for _, name := range []string{"repositories", "workflows", "runs", "outputLifecycles"} {
		for _, row := range fixture[name] {
			if err := writer.Append(t.Context(), "$"+name, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := writer.Publish(t.Context(), "output-lifecycle-event-clocks"); err != nil {
		t.Fatal(err)
	}

	results, metrics, err := store.ExecuteSQLPlan(t.Context(), canonicalProjectionDefinitions(t), []string{
		"output-delivery-events", "output-adoption-events",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics.FallbackOperations) != 0 || !slices.Contains(metrics.PushedDown, "sql-plan") {
		t.Fatalf("output lifecycle clocks must execute entirely in native SQL: %#v", metrics)
	}
	if len(results["output-delivery-events"].Rows) != 2 {
		t.Fatalf("delivery events = %#v", results["output-delivery-events"].Rows)
	}
	if len(results["output-adoption-events"].Rows) != 1 {
		t.Fatalf("adoption events = %#v", results["output-adoption-events"].Rows)
	}
	var delivery model.Row
	for _, row := range results["output-delivery-events"].Rows {
		if row["safe-output"] == "output:current" {
			delivery = row
			break
		}
	}
	if delivery["safe-output"] != "output:current" ||
		delivery["observed-at"] != fixture["outputLifecycles"][1]["deliveredAt"] {
		t.Fatalf("delivery clock changed: %#v", delivery)
	}
	adoption := results["output-adoption-events"].Rows[0]
	if adoption["safe-output"] != "output:current" || adoption["observed-at"] != acceptedAt {
		t.Fatalf("adoption clock changed: %#v", adoption)
	}
}

func TestCanonicalWorkItemContractExecutesNatively(t *testing.T) {
	store, _ := nativeTestStore(t)
	content, err := os.ReadFile("../../../dashboard/site/test/fixtures/canonical-work-items.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string][]model.Row
	if err := json.Unmarshal(content, &fixture); err != nil {
		t.Fatal(err)
	}
	asOf, err := time.Parse(time.RFC3339, fixture["repositories"][0]["observedAt"].(string))
	if err != nil {
		t.Fatal(err)
	}
	fixture["runs"][1]["startedAt"] = nil
	if err := store.RunPartitionMaintenance(t.Context(), asOf, 90, store.linkedRetentionDays); err != nil {
		t.Fatal(err)
	}
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	writer.ingestedAt = asOf
	for _, name := range []string{"campaigns", "repositories", "workflows", "runs", "issues", "outputLifecycles"} {
		for _, row := range fixture[name] {
			if err := writer.Append(t.Context(), "$"+name, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := writer.Publish(t.Context(), "canonical-work-items"); err != nil {
		t.Fatal(err)
	}
	results, metrics, err := store.ExecuteSQLPlan(t.Context(), canonicalProjectionDefinitions(t), []string{
		"work-items", "work-item-run-outcomes", "outcomes",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics.FallbackOperations) != 0 || !slices.Contains(metrics.PushedDown, "sql-plan") {
		t.Fatalf("work items must execute entirely in native SQL: %#v", metrics)
	}
	if len(results["outcomes"].Rows) != 2 || len(results["work-item-run-outcomes"].Rows) != 2 {
		t.Fatal("retry outputs must retain separate canonical run attempts")
	}
	rows := results["work-items"].Rows
	if len(rows) != 2 {
		t.Fatalf("expected one work item per canonical workflow: %#v", rows)
	}
	for _, row := range rows {
		if row["workflow"] == ".github/workflows/worker.md" {
			if row["lifecycle-state"] != "review" || row["name"] != "Worker · Current attempt" ||
				row["outcome-state"] != "pending" || row["verification-state"] != "pending" ||
				row["started-at"] != fixture["runs"][1]["createdAt"] {
				t.Fatalf("native latest-attempt contract changed: %#v", row)
			}
			link, ok := row["evidence-link"].(map[string]any)
			if !ok || link["href"] != "https://github.com/owner/repo/issues/7" {
				t.Fatalf("native output evidence changed: %#v", row["evidence-link"])
			}
			if !reflect.DeepEqual(row["repository-link"], fixture["workflows"][0]["repositoryLink"]) {
				t.Fatalf("native repository link changed: %#v", row["repository-link"])
			}
		} else if row["lifecycle-state"] != "unknown" || row["repository-link"] != nil {
			t.Fatalf("missing run evidence must remain unknown: %#v", row)
		}
	}
}
