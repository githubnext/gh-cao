package postgresx

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/internal/dashboardquery"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func TestOversizedStatisticalInputIsCappedBeforePublication(t *testing.T) {
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
			"createdAt": time.Now().UTC().Format(time.RFC3339), "status": "completed", "conclusion": "success"}},
	} {
		if err := writer.Append(t.Context(), input.name, input.row); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.tx.Exec(t.Context(), `INSERT INTO runs(namespace,ordinal,present_fields,id,repository_id,workflow_id,run_at,created_at,status,conclusion)
		SELECT namespace,n,present_fields,'github:run:o/r:' || n,repository_id,workflow_id,run_at,created_at,status,conclusion
		FROM runs CROSS JOIN generate_series(2,$2) AS n
		WHERE namespace=$1 AND id='github:run:o/r:1'`,
		store.namespace, dashboardquery.StatisticalMaxInputRows+1); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.statisticalCandidates(t.Context()); !errors.Is(err, dashboardquery.ErrStatisticalBudget) {
		t.Fatalf("unpublished oversized input bypassed the sampling guard: %v", err)
	}
	if err := writer.Quality(t.Context(), "$runs", model.Metadata{
		"as-of":        time.Now().UTC().Add(time.Minute).Format(time.RFC3339),
		"availability": "available", "completeness": "complete",
	}); err != nil {
		t.Fatal(err)
	}
	state, err := writer.Publish(t.Context(), "oversized-statistical-frame")
	if err != nil || state.Counts["$runs"] != query.MaxInputRows {
		t.Fatalf("publication did not cap retained input: %+v %v", state, err)
	}
	results, _, err := store.ExecuteSQLPlan(t.Context(), []query.Definition{
		{Name: "sample", From: "$statisticalMembership"},
		{Name: "exact", From: "$repositories"},
		{Name: "retained", From: "$runs", Aggregate: &query.Aggregate{
			Values: []query.AggregateValue{{Field: "id", As: "runs", Reducer: "count"}},
		}},
	}, []string{"sample", "exact", "retained"})
	if err != nil {
		t.Fatal(err)
	}
	if results["sample"].Metadata["availability"] != "available" || len(results["sample"].Rows) == 0 {
		t.Fatalf("bounded retained frame became unavailable: %+v", results["sample"].Metadata)
	}
	if rows := results["retained"].Rows; len(rows) != 1 || fmt.Sprint(rows[0]["runs"]) != fmt.Sprint(query.MaxInputRows) {
		t.Fatalf("retained exact query was not usable at capacity: %+v", results["retained"])
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
