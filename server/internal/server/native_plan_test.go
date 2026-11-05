package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func nativeAPIFixture() map[string]model.Source {
	return map[string]model.Source{
		"$repositories": {Rows: []model.Row{{"id": "repo", "owner": "octo", "name": "api"}}},
		"$workflows":    {Rows: []model.Row{{"id": "workflow", "repositoryId": "repo"}}},
		"$runs":         {Rows: []model.Row{{"id": "run", "workflowId": "workflow", "repositoryId": "repo"}}},
		"$domains":      {Rows: []model.Row{{"id": "first", "runId": "run"}, {"id": "second", "runId": "run"}, {"id": "third", "runId": "run"}}},
	}
}

func TestNativePlanUsesExistingHTTPPaginationContract(t *testing.T) {
	store := integrationDatabase(t)
	seedDatabase(t, store, nativeAPIFixture())
	app := &App{database: store, databaseQueries: []query.Definition{{Name: "domains", From: "$domains"}}}
	input := queryRequest{SourceNames: []string{"pick"}, CompiledQueries: []query.Definition{{Name: "pick", From: "domains"}}, Pagination: map[string]paginationRequest{"pick": {Limit: 1}}}
	first, status, err := app.executeQuery(t.Context(), input, false)
	if err != nil || status != http.StatusOK || len(first.Sources["pick"].Rows) != 1 || first.Sources["pick"].Rows[0]["id"] != "first" ||
		first.Sources["pick"].Metadata["total-row-count"] != 3 || first.Sources["pick"].ContinuationToken == "" {
		t.Fatalf("first native page failed: %+v %d %v", first, status, err)
	}
	if first.Metrics.RetainedRows != 1 {
		t.Fatalf("pagination decoded whole source or used fallback: %+v", first.Metrics)
	}
	input.Pagination["pick"] = paginationRequest{Limit: 1, ContinuationToken: first.Sources["pick"].ContinuationToken}
	second, status, err := app.executeQuery(t.Context(), input, false)
	if err != nil || status != http.StatusOK || len(second.Sources["pick"].Rows) != 1 || second.Sources["pick"].Rows[0]["id"] != "second" {
		t.Fatalf("second native page failed: %+v %d %v", second, status, err)
	}
	seedDatabase(t, store, nativeAPIFixture())
	if _, _, err := app.executeQuery(t.Context(), input, false); err == nil {
		t.Fatal("stale revision-bound cursor accepted")
	}
}

func TestWorkflowKeysetPaginationAnchorsOnBoundaryRows(t *testing.T) {
	store := integrationDatabase(t)
	fixture := nativeAPIFixture()
	fixture["$repositories"] = model.Source{Rows: []model.Row{
		{"id": "repo-1", "owner": "octo", "name": "one"},
		{"id": "repo-2", "owner": "octo", "name": "two"},
		{"id": "repo-3", "owner": "octo", "name": "three"},
	}}
	fixture["$workflows"] = model.Source{Rows: []model.Row{
		{"id": "workflow-1", "repositoryId": "repo-1", "path": ".github/workflows/a.md"},
		{"id": "workflow-2", "repositoryId": "repo-2", "path": ".github/workflows/b.md"},
		{"id": "workflow-3", "repositoryId": "repo-3", "path": ".github/workflows/c.md"},
	}}
	fixture["$runs"] = model.Source{Rows: []model.Row{}}
	fixture["$domains"] = model.Source{Rows: []model.Row{}}
	seedDatabase(t, store, fixture)
	app := &App{database: store}
	definitions := []query.Definition{
		{
			Name: "workflow-subjects", From: "$workflows",
			Select:  []query.SelectedField{{Field: "repositoryId"}, {Field: "path"}, {Field: "id"}},
			OrderBy: []query.OrderField{{Field: "repositoryId"}, {Field: "path"}, {Field: "id"}},
			Page: &query.PageBoundary{
				Keys: []string{"repositoryId", "path", "id"}, Revision: "workflow-subjects",
				Comparator: "utf8-bytewise-v1",
			},
		},
		{
			Name: "visible", From: "workflow-subjects",
			Filter: &query.Filter{Predicates: []query.Predicate{{Field: "id", Equals: "workflow-2"}}},
		},
	}
	input := queryRequest{
		Queries: definitions, SourceNames: []string{"visible"},
		Pagination: map[string]paginationRequest{"visible": {Limit: 1}},
	}
	first, status, err := app.executeQuery(t.Context(), input, false)
	if err != nil || status != http.StatusOK || len(first.Sources["visible"].Rows) != 0 ||
		first.Sources["visible"].ContinuationToken == "" || first.Sources["visible"].Metadata["has-more"] != true {
		t.Fatalf("sparse first keyset page lost its boundary cursor: %+v %d %v", first, status, err)
	}
	input.Pagination["visible"] = paginationRequest{
		Limit: 1, ContinuationToken: first.Sources["visible"].ContinuationToken,
	}
	second, status, err := app.executeQuery(t.Context(), input, false)
	if err != nil || status != http.StatusOK || len(second.Sources["visible"].Rows) != 1 ||
		second.Sources["visible"].Rows[0]["id"] != "workflow-2" {
		t.Fatalf("deep keyset page did not resume from the boundary anchor: %+v %d %v", second, status, err)
	}
}

func TestNativeValuePreparationInvalidatesWorkflowCursorOnlyForNewParents(t *testing.T) {
	store := integrationDatabase(t)
	const repositoryID = "repository:octo%2Fexample"
	seedDatabase(t, store, map[string]model.Source{
		"$repositories": {Rows: []model.Row{{"id": repositoryID, "owner": "octo", "name": "example"}}},
		"$workflows": {Rows: []model.Row{
			{"id": "workflow-y", "repositoryId": repositoryID, "path": ".github/workflows/y.md"},
			{"id": "workflow-z", "repositoryId": repositoryID, "path": ".github/workflows/z.md"},
		}},
	})
	authority := store.NativeEnrollment()
	if err := authority.WithMutation(t.Context(), func(ctx context.Context) error {
		return authority.Enroll(ctx, 7, []string{"octo/example"})
	}); err != nil {
		t.Fatal(err)
	}
	scope, err := authority.CurrentScope(t.Context(), "octo/example")
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile("../../../dashboard/site/test/fixtures/workflow-value-collection.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Row model.Row `json:"workflow_value_collection"`
	}
	if err := json.Unmarshal(content, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Row["repositoryId"] = repositoryID
	envelope.Row["workflowId"] = "workflow:octo%2Fexample%3A.github%2Fworkflows%2Fverify-bugs.md"
	definitions := []query.Definition{{
		Name: "workflow-subjects", From: "$workflows",
		Select:  []query.SelectedField{{Field: "repositoryId"}, {Field: "path"}, {Field: "id"}},
		OrderBy: []query.OrderField{{Field: "repositoryId"}, {Field: "path"}, {Field: "id"}},
		Page: &query.PageBoundary{
			Keys: []string{"repositoryId", "path", "id"}, Revision: "workflow-subjects", Comparator: "utf8-bytewise-v1",
		},
	}}
	app := &App{database: store}
	input := queryRequest{Queries: definitions, SourceNames: []string{"workflow-subjects"},
		Pagination: map[string]paginationRequest{"workflow-subjects": {Limit: 1}}}
	first, status, err := app.executeQuery(t.Context(), input, false)
	if err != nil || status != http.StatusOK || first.Sources["workflow-subjects"].ContinuationToken == "" {
		t.Fatalf("initial workflow cursor: %+v %d %v", first, status, err)
	}
	state, err := store.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	revision, err := store.PublishNativeValuePreparation(t.Context(), state.Revision,
		[]collectnative.EnrollmentScope{scope}, []model.Row{envelope.Row}, nil)
	if err != nil {
		t.Fatal(err)
	}
	input.Pagination["workflow-subjects"] = paginationRequest{
		Limit: 1, ContinuationToken: first.Sources["workflow-subjects"].ContinuationToken,
	}
	if _, _, err := app.executeQuery(t.Context(), input, false); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("new workflow before the anchor did not invalidate its cursor: %v", err)
	}
	input.Pagination["workflow-subjects"] = paginationRequest{Limit: 1}
	fresh, status, err := app.executeQuery(t.Context(), input, false)
	if err != nil || status != http.StatusOK || len(fresh.Sources["workflow-subjects"].Rows) != 1 ||
		fresh.Sources["workflow-subjects"].Rows[0]["path"] != envelope.Row["sourcePath"] {
		t.Fatalf("new structural workflow was omitted: %+v %d %v", fresh, status, err)
	}
	if _, err := store.PublishNativeValuePreparation(t.Context(), revision,
		[]collectnative.EnrollmentScope{scope}, []model.Row{envelope.Row}, nil); err != nil {
		t.Fatal(err)
	}
	input.Pagination["workflow-subjects"] = paginationRequest{
		Limit: 1, ContinuationToken: fresh.Sources["workflow-subjects"].ContinuationToken,
	}
	next, status, err := app.executeQuery(t.Context(), input, false)
	if err != nil || status != http.StatusOK || len(next.Sources["workflow-subjects"].Rows) != 1 ||
		next.Sources["workflow-subjects"].Rows[0]["path"] != ".github/workflows/y.md" {
		t.Fatalf("health-only update invalidated a valid workflow cursor: %+v %d %v", next, status, err)
	}
}

func TestHostedSQLFailsClosedForUnsupportedDefinitions(t *testing.T) {
	store := integrationDatabase(t)
	seedDatabase(t, store, nil)
	app := &App{database: store}
	_, _, err := app.executeQuery(t.Context(), queryRequest{SourceNames: []string{"unsupported"}, Queries: []query.Definition{{Name: "unsupported", From: "arbitrary-legacy-source"}}}, false)
	if err == nil {
		t.Fatal("hosted source fallback admitted")
	}
	if strings.Contains(err.Error(), "fallback") {
		t.Fatalf("SQL cutover contains fallback: %v", err)
	}
}

func TestMetadataCompositionAndUnavailableLeftJoin(t *testing.T) {
	store := integrationDatabase(t)
	writer, err := store.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	for source, input := range nativeAPIFixture() {
		for _, row := range input.Rows {
			if err := writer.Append(t.Context(), source, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Quality(t.Context(), "$runs", model.Metadata{"as-of": "2026-02-03T00:00:00Z", "freshness": "stale"}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Quality(t.Context(), "$repositories", model.Metadata{"availability": "unavailable", "completeness": "partial", "as-of": "2026-02-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Publish(t.Context(), "metadata"); err != nil {
		t.Fatal(err)
	}
	definitions := []query.Definition{{Name: "enriched", From: "$runs", Joins: []query.Join{{Source: "$repositories", Type: "left", On: []query.JoinKey{{Left: "id", Right: "id"}}, Fields: []query.SelectedField{{Field: "owner", As: "repository-owner"}}}}}}
	err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader postgresx.NativeReader) error {
		result, _, err := reader.ExecuteSQLPlan(ctx, definitions, []string{"enriched"})
		if err != nil {
			return err
		}
		source := result["enriched"]
		if len(source.Rows) != 1 || source.Rows[0]["repository-owner"] != nil || source.Metadata["completeness"] != "partial" ||
			source.Metadata["freshness"] != "stale" || source.Metadata["as-of"] != "2026-02-01T00:00:00Z" {
			t.Errorf("native metadata composition failed: %+v", source)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
