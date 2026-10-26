package sqlitex

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func TestNativeBatchMeasurementProductionPath(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	run := func(args ...string) {
		command := exec.CommandContext(t.Context(), "node", args...)
		command.Dir = root
		if content, err := command.CombinedOutput(); err != nil {
			t.Fatalf("native producer failed: %v\n%s", err, content)
		}
	}
	run(".github/skills/add-operational-value/scripts/evaluate.mjs", "--native",
		"--function", "tests/fixtures/operational-value/native-workflow.mjs",
		"--output-dir", output, "--end", "2026-10-15T00:00:00Z", "example/service", "incident-response")
	database := filepath.Join(output, "native.sqlite")
	run("activity/cao.mjs", "ingest-jsonl", "--database", database,
		"--input", filepath.Join(output, "example-service", "incident-response-measurements.jsonl"), "--retention-days", "all")
	store, err := Open(t.Context(), database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	content, err := os.ReadFile(filepath.Join(root, "dashboard/site/src/data/queries/database.json"))
	if err != nil {
		t.Fatal(err)
	}
	var definitions []query.Definition
	if err := json.Unmarshal(content, &definitions); err != nil {
		t.Fatal(err)
	}
	definitions = append(definitions, query.Definition{
		Name: "selected-measurements", From: "workflow-value-measurements",
		Filter: &query.Filter{Predicates: []query.Predicate{
			{Field: "workflow-route", Equals: "example/service:.github/workflows/incident-response.md"},
		}},
	})
	err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
		sources, metrics, err := reader.ExecuteSQLPlan(ctx, definitions, []string{"selected-measurements"})
		if err != nil {
			return err
		}
		rows := sources["selected-measurements"].Rows
		if len(rows) != 2 || rows[0]["value"] != float64(0) || rows[1]["value"] != nil ||
			rows[1]["measurement-kind"] != "interval" || rows[1]["maturity-status"] != "unavailable" {
			t.Fatalf("native ordinary measurement semantics lost: %+v", rows)
		}
		if metrics.PeakWorkingRows > 2 {
			t.Fatal("unrelated source rows entered native measurement execution")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
