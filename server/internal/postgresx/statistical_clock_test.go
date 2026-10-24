package postgresx

import (
	"testing"
	"time"

	"github.com/githubnext/gh-cao/internal/dashboardquery"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func TestOversizedStatisticalFramePreservesExactQueries(t *testing.T) {
	store, _ := nativeTestStore(t)
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	for _, input := range []struct {
		name string
		row  model.Row
	}{
		{"$repositories", model.Row{"id": "repository", "owner": "o", "name": "r"}},
		{"$workflows", model.Row{"id": "workflow", "repositoryId": "repository", "path": "worker.md"}},
		{"$runs", model.Row{"id": "github:run:o/r:1", "repositoryId": "repository", "workflowId": "workflow",
			"createdAt": time.Now().UTC().Format(time.RFC3339)}},
	} {
		if err := writer.Append(t.Context(), input.name, input.row); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.tx.Exec(t.Context(), `INSERT INTO runs(namespace,ordinal,present_fields,id,repository_id,workflow_id,run_at)
		SELECT namespace,n,present_fields,'github:run:o/r:' || n,repository_id,workflow_id,run_at
		FROM runs CROSS JOIN generate_series(2,$2) AS n
		WHERE namespace=$1 AND id='github:run:o/r:1'`,
		store.namespace, dashboardquery.StatisticalMaxInputRows+1); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Publish(t.Context(), "oversized-statistical-frame"); err != nil {
		t.Fatal(err)
	}
	results, _, err := store.ExecuteSQLPlan(t.Context(), []query.Definition{
		{Name: "sample", From: "$statisticalMembership"},
		{Name: "exact", From: "$repositories"},
	}, []string{"sample", "exact"})
	if err != nil {
		t.Fatal(err)
	}
	if results["sample"].Metadata["availability"] != "unavailable" || len(results["sample"].Rows) != 0 ||
		results["sample"].Metadata["error"] != "statistical candidate frame exceeds input budget" {
		t.Fatalf("oversized frame was presented as a usable sample: %+v", results["sample"])
	}
	if len(results["exact"].Rows) != 1 || results["exact"].Rows[0]["id"] != "repository" {
		t.Fatalf("sampling budget changed exact evidence: %+v", results["exact"])
	}
}

func TestMissingStatisticalClockPreservesExactQueries(t *testing.T) {
	store, _ := nativeTestStore(t)
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	if err := writer.Append(t.Context(), "$repositories", model.Row{"id": "repository", "owner": "example", "name": "control"}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Publish(t.Context(), "missing-statistical-clock"); err != nil {
		t.Fatal(err)
	}
	results, _, err := store.ExecuteSQLPlan(t.Context(), []query.Definition{
		{Name: "sample", From: "$statisticalMembership"},
		{Name: "exact", From: "$repositories"},
	}, []string{"sample", "exact"})
	if err != nil {
		t.Fatal(err)
	}
	if results["sample"].Metadata["availability"] != "unavailable" || len(results["sample"].Rows) != 0 {
		t.Fatalf("missing sampling clock was presented as a usable sample: %+v", results["sample"])
	}
	if len(results["exact"].Rows) != 1 || results["exact"].Rows[0]["id"] != "repository" {
		t.Fatalf("sampling unavailability changed exact evidence: %+v", results["exact"])
	}
}
