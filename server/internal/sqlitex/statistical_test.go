package sqlitex

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestStatisticalSnapshotProductionBoundary(t *testing.T) {
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
	inputs := map[string][]model.Row{"runs": fixture.Rows}
	for _, source := range []string{"tools", "domains", "skills", "audits"} {
		inputs[source] = []model.Row{
			{"id": "a", "runId": fixture.Rows[0]["id"]}, {"id": "b", "runId": fixture.Rows[0]["id"]},
			{"id": "expired", "runId": fixture.Rows[7]["id"]},
		}
	}
	path := testutil.SQLiteSnapshot(t, inputs)
	store, err := OpenWithMetadata(t.Context(), path, map[string]model.Metadata{
		"$runs": {"as-of": fixture.Watermark, "completeness": "complete", "availability": "available"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	corpus, err := os.ReadFile("../../../dashboard/site/src/data/queries/database.json")
	if err != nil {
		t.Fatal(err)
	}
	var definitions []query.Definition
	if err := json.Unmarshal(corpus, &definitions); err != nil {
		t.Fatal(err)
	}
	// Parse the consumer exactly as the production HTTP boundary does.
	var consumers []query.Definition
	if err := json.Unmarshal([]byte(`[
		{"name":"sample-success","from":"statistical-runs","filter":{"predicates":[{"field":"run-conclusion","equals":"success"}]}},
		{"name":"sample-empty","from":"statistical-runs","filter":{"predicates":[{"field":"run","equals":"not-present"}]}}
	]`), &consumers); err != nil {
		t.Fatal(err)
	}
	definitions = slices.Concat(definitions, consumers)
	err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
		result, _, err := reader.ExecuteSQLPlan(ctx, definitions, []string{
			"sample-success", "sample-empty", "runs", "statistical-tools", "statistical-domains", "statistical-skills", "statistical-audits",
		})
		if err != nil {
			return err
		}
		if len(result["sample-success"].Rows) != 5 || len(result["sample-empty"].Rows) != 0 || len(result["runs"].Rows) != 9 {
			t.Fatalf("statistical selection changed exact evidence: %+v", result)
		}
		for _, name := range []string{"sample-success", "sample-empty"} {
			frame, ok := result[name].Metadata["statistical-frame"].(map[string]any)
			if !ok || frame["population-count"] != 6 || frame["selected-count"] != 6 || frame["capacity"] != 8000 {
				t.Fatalf("consumer predicates changed frozen frame: %v", result[name].Metadata)
			}
		}
		for _, row := range result["sample-success"].Rows {
			frame := result["sample-success"].Metadata["statistical-frame"].(map[string]any)
			if row["statistical-frame-id"] != frame["frame-id"] {
				t.Fatal("selected run lost its frame binding")
			}
			if row["statistical-weight-valid"] != true || row["statistical-weight"] != float64(1) {
				t.Fatalf("complete census frame lost its weights: %v", row)
			}
			for _, name := range []string{"statistical-tools", "statistical-domains", "statistical-skills", "statistical-audits"} {
				frame := result[name].Metadata["statistical-frame"].(map[string]any)
				for _, row := range result[name].Rows {
					if row["statistical-frame-id"] != frame["frame-id"] {
						t.Fatalf("owned observation lost its run's frame binding: %s", name)
					}
				}
			}
			if row["sample-weight"] != row["statistical-weight"] || row["sample-stratum"] != row["statistical-stratum"] ||
				row["sample-probability"] != row["statistical-probability"] {
				t.Fatalf("public sample fields do not preserve frozen weights: %v", row)
			}
			for _, source := range []string{"statistical-tools", "statistical-domains", "statistical-skills", "statistical-audits"} {
				if len(result[source].Rows) != 2 {
					t.Fatalf("owned observations were independently sampled or expired children retained: %s %+v", source, result[source])
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStatisticalSnapshotCanonicalClockFallback(t *testing.T) {
	path := testutil.SQLiteSnapshot(t, map[string][]model.Row{"runs": {
		{"id": "github:run:o/r:1", "owner": "o", "repository": "r", "githubRunId": "1", "status": "completed",
			"conclusion": "success", "startedAt": "2026-10-02T00:00:00Z", "completedAt": "2026-10-02T01:00:00Z"},
	}})
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
		result, _, err := reader.ExecuteSQLPlan(ctx, nil, []string{"$statisticalMembership"})
		if err != nil {
			return err
		}
		source := result["$statisticalMembership"]
		frame := source.Metadata["statistical-frame"].(map[string]any)
		if frame["watermark"] != "2026-10-02T01:00:00.000Z" || frame["watermark-policy"] != "canonical-run-max" ||
			frame["weight-valid"] != false || len(source.Rows) != 1 {
			t.Fatalf("snapshot clock is not canonical and conservative: %+v", source)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
