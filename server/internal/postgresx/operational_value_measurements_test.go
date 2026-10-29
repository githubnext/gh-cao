package postgresx

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func TestReplaceOperationalValuesPublishesAndRejectsStaleRevision(t *testing.T) {
	store, _ := nativeTestStore(t)
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []struct {
		source string
		row    model.Row
	}{
		{"$repositories", model.Row{
			"id": "repository:octo%2Fapi", "owner": "octo", "name": "api",
		}},
		{"$campaigns", model.Row{
			"id": "campaign:optimization", "slug": "optimization", "name": "Optimization",
		}},
	} {
		if err := writer.Append(t.Context(), record.source, record.row); err != nil {
			writer.Abort(t.Context())
			t.Fatal(err)
		}
	}
	initial, err := writer.Publish(t.Context(), "activity-1")
	if err != nil {
		t.Fatal(err)
	}
	row := model.Row{
		"id":           "operational-value:%3Aocto%2Fapi%3Aworker.efficiency%3A2026-01-01T00%3A00%3A00Z",
		"repositoryId": "repository:octo%2Fapi", "repository": "octo/api",
		"campaignId": "campaign:optimization", "campaign": "optimization",
		"valueId": "worker.efficiency", "value": nil,
		"operational-value-role": "primary", "operational-value-name": "Efficiency",
		"operational-value-direction": "increase", "maturity-status": "unavailable",
		"coverage-status": "missing", "missing-reason": "no-eligible-runs",
		"timestamp": "2026-01-01T00:00:00Z",
	}
	published, err := store.ReplaceOperationalValues(t.Context(), initial.Revision, []model.Row{row})
	if err != nil {
		t.Fatal(err)
	}
	if published.Revision != initial.Revision+1 || published.DataRevision != initial.DataRevision {
		t.Fatalf("publication state = %+v, initial = %+v", published, initial)
	}
	err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader NativeReader) error {
		sources, _, err := reader.ExecuteSQLPlan(ctx, []query.Definition{{
			Name: "values", From: "$operationalValues",
			Select: []query.SelectedField{{Field: "repository"}, {Field: "valueId"},
				{Field: "value"}, {Field: "coverage-status"}, {Field: "missing-reason"}},
		}}, []string{"values"})
		if err != nil {
			return err
		}
		rows := sources["values"].Rows
		if len(rows) != 1 || rows[0]["repository"] != "octo/api" ||
			rows[0]["coverage-status"] != "missing" || rows[0]["missing-reason"] != "no-eligible-runs" {
			t.Fatalf("published measurements = %#v", rows)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceOperationalValues(t.Context(), initial.Revision, nil); !errors.Is(err, ErrOperationalValueRevisionStale) {
		t.Fatalf("stale publication error = %v", err)
	}
}

func TestHostedSQLPlanRepositoryScopePrecedesAggregation(t *testing.T) {
	store, _ := nativeTestStore(t)
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []struct {
		source string
		row    model.Row
	}{
		{"$repositories", model.Row{"id": "repo-api", "owner": "octo", "name": "api"}},
		{"$repositories", model.Row{"id": "repo-secret", "owner": "octo", "name": "secret"}},
		{"$workflows", model.Row{"id": "workflow-api", "repositoryId": "repo-api", "path": ".github/workflows/test.md"}},
		{"$workflows", model.Row{"id": "workflow-secret", "repositoryId": "repo-secret", "path": ".github/workflows/test.md"}},
		{"$runs", model.Row{"id": "run-api", "repositoryId": "repo-api", "workflowId": "workflow-api",
			"owner": "octo", "repository": "api", "status": "completed", "conclusion": "success"}},
		{"$runs", model.Row{"id": "run-secret", "repositoryId": "repo-secret", "workflowId": "workflow-secret",
			"owner": "octo", "repository": "secret", "status": "completed", "conclusion": "failure"}},
	} {
		if err := writer.Append(t.Context(), record.source, record.row); err != nil {
			writer.Abort(t.Context())
			t.Fatal(err)
		}
	}
	if _, err := writer.Publish(t.Context(), "activity-1"); err != nil {
		t.Fatal(err)
	}
	err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader NativeReader) error {
		sources, _, err := reader.ExecuteSQLPlanWithOptions(ctx, []query.Definition{{
			Name: "totals", From: "$runs",
			Aggregate: &query.Aggregate{
				Values: []query.AggregateValue{{Field: "id", As: "runs", Reducer: "count"}},
			},
		}}, []string{"totals"}, SQLExecutionOptions{Repositories: []string{"octo/api"}})
		if err != nil {
			return err
		}
		rows := sources["totals"].Rows
		if len(rows) != 1 || fmt.Sprint(rows[0]["runs"]) != "1" {
			t.Fatalf("scoped aggregate = %#v", rows)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
