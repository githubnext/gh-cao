package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
)

func TestDashboardQueryCorpusUsesPostgres(t *testing.T) {
	assertDashboardQueriesUsePostgres(t)
}

// TestRepositoryQueriesUsePostgres guards that repository queries stay in the
// deployed corpus, which TestDashboardQueryCorpusUsesPostgres executes in full.
func TestRepositoryQueriesUsePostgres(t *testing.T) {
	definitions, _, err := dashboardSQLCorpus()
	if err != nil {
		t.Fatal(err)
	}
	deployed := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		deployed[definition.Name] = true
	}
	for _, name := range []string{
		"repository-run-statistics", "repository-run-identities",
		"repository-import-totals", "repository-run-totals",
		"repository-aic-totals", "repository-activity",
		"repository-observed-run-counts", "repository-run-inventory",
	} {
		if !deployed[name] {
			t.Errorf("repository query %q is missing from the deployed dashboard corpus", name)
		}
	}
}

func assertDashboardQueriesUsePostgres(t *testing.T) {
	t.Helper()
	store := integrationDatabase(t)
	definitions, parameters, err := dashboardSQLCorpus()
	if err != nil {
		t.Fatal(err)
	}
	if err := bindDashboardSQLCorpusInputs(definitions, parameters); err != nil {
		t.Fatal(err)
	}
	if err := query.Validate(definitions); err != nil {
		t.Fatalf("validate deployed dashboard query corpus: %v", err)
	}
	requested := make([]string, len(definitions))
	for index, definition := range definitions {
		requested[index] = definition.Name
	}
	runtimeSource := func(_ context.Context, name string) (model.Source, error) {
		return model.Source{
			Source: name,
			Rows:   []model.Row{},
			Metadata: model.Metadata{
				"availability": "empty",
				"completeness": "complete",
				"freshness":    "current",
			},
		}, nil
	}
	err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader postgresx.NativeReader) error {
		for _, name := range requested {
			sources, metrics, err := reader.ExecuteSQLPlanWithOptions(
				ctx, definitions, []string{name},
				postgresx.SQLExecutionOptions{Runtime: runtimeSource},
			)
			if err != nil {
				return fmt.Errorf("execute dashboard query %q through PostgreSQL: %w", name, err)
			}
			if _, ok := sources[name]; !ok {
				return fmt.Errorf("PostgreSQL SQL plan omitted dashboard query %q", name)
			}
			if len(metrics.FallbackOperations) != 0 {
				return fmt.Errorf("dashboard query %q used Go fallback operations: %v",
					name, metrics.FallbackOperations)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowListContractUsesPostgres(t *testing.T) {
	content, err := os.ReadFile("../../../dashboard/site/test/fixtures/workflow-list-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Cases []struct {
			Repository, Workflow, Run, Status string
			HasObservedRun                    bool
		}
	}
	if err := json.Unmarshal(content, &contract); err != nil {
		t.Fatal(err)
	}
	if len(contract.Cases) == 0 {
		t.Fatal("workflow list contract has no cases")
	}
	inputs := map[string]model.Source{
		"$repositories": {Rows: []model.Row{}},
		"$workflows":    {Rows: []model.Row{}},
		"$runs":         {Rows: []model.Row{}},
	}
	repositories := map[string]bool{}
	base := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Hour)
	for index, testcase := range contract.Cases {
		owner, repository, valid := strings.Cut(testcase.Repository, "/")
		if !valid || owner == "" || repository == "" {
			t.Fatalf("invalid fixture repository %q", testcase.Repository)
		}
		if !repositories[testcase.Repository] {
			source := inputs["$repositories"]
			source.Rows = append(source.Rows, model.Row{"id": testcase.Repository, "owner": owner, "name": repository})
			inputs["$repositories"] = source
			repositories[testcase.Repository] = true
		}
		workflowID := testcase.Repository + ":" + testcase.Workflow
		workflows := inputs["$workflows"]
		workflows.Rows = append(workflows.Rows, model.Row{
			"id": workflowID, "repositoryId": testcase.Repository, "path": testcase.Workflow,
			"name": testcase.Workflow, "role": "standalone", "state": "active", "rolloutMode": "review",
		})
		inputs["$workflows"] = workflows
		if testcase.Run == "" {
			continue
		}
		at := base.Add(-time.Duration(index) * time.Hour).Format(time.RFC3339)
		run := model.Row{
			"id": workflowID + ":latest", "repositoryId": testcase.Repository, "workflowId": workflowID,
			"owner": owner, "repository": repository, "githubRunId": testcase.Run,
			"attempt": 2, "createdAt": at, "status": testcase.Status,
		}
		if testcase.Status != "queued" {
			run["startedAt"] = at
		}
		if testcase.Status == "success" || testcase.Status == "failure" {
			run["status"], run["conclusion"] = "completed", testcase.Status
		}
		runs := inputs["$runs"]
		runs.Rows = append(runs.Rows, run, model.Row{
			"id": workflowID + ":earlier-attempt", "repositoryId": testcase.Repository, "workflowId": workflowID,
			"owner": owner, "repository": repository, "githubRunId": testcase.Run,
			"attempt": 1, "startedAt": base.Add(-24 * time.Hour).Format(time.RFC3339), "status": "completed", "conclusion": "failure",
		}, model.Row{
			"id": workflowID + ":older-run", "repositoryId": testcase.Repository, "workflowId": workflowID,
			"owner": owner, "repository": repository, "githubRunId": "1000",
			"attempt": 1, "startedAt": base.Add(-48 * time.Hour).Format(time.RFC3339), "status": "completed", "conclusion": "success",
		})
		inputs["$runs"] = runs
	}
	store := integrationDatabase(t)
	seedDatabase(t, store, inputs)
	definitions, parameters, err := dashboardSQLCorpus()
	if err != nil {
		t.Fatal(err)
	}
	if err := bindDashboardSQLCorpusInputs(definitions, parameters); err != nil {
		t.Fatal(err)
	}
	err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader postgresx.NativeReader) error {
		sources, metrics, err := reader.ExecuteSQLPlan(ctx, definitions, []string{"workflow-list"})
		if err != nil {
			return err
		}
		if len(metrics.FallbackOperations) != 0 {
			t.Fatalf("workflow list used Go fallback operations: %v", metrics.FallbackOperations)
		}
		rows := sources["workflow-list"].Rows
		if len(rows) != len(contract.Cases) {
			t.Fatalf("workflow list has %d rows, want %d", len(rows), len(contract.Cases))
		}
		for index, testcase := range contract.Cases {
			row := rows[index]
			if row["repository"] != testcase.Repository || row["workflow"] != testcase.Workflow ||
				row["latest-run-status"] != testcase.Status || row["has-observed-run"] != testcase.HasObservedRun {
				t.Errorf("workflow list row %d = %+v, want %+v", index, row, testcase)
			}
			if testcase.HasObservedRun {
				if row["latest-run"] != testcase.Run {
					t.Errorf("workflow list run %d = %#v, want textual %q", index, row["latest-run"], testcase.Run)
				}
			} else if row["latest-run"] != nil || row["latest-run-at"] != nil {
				t.Errorf("unobserved workflow has run evidence: %+v", row)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func dashboardSQLCorpus() ([]query.Definition, map[string]any, error) {
	dashboardPath := "../../../dashboard/site/dashboard.json"
	content, err := os.ReadFile(dashboardPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read deployed dashboard document: %w", err)
	}
	var dashboard struct {
		Fragments []string `json:"fragments"`
		Dashboard struct {
			Queries []query.Definition `json:"queries"`
		} `json:"dashboard"`
	}
	if err := json.Unmarshal(content, &dashboard); err != nil {
		return nil, nil, fmt.Errorf("parse deployed dashboard document: %w", err)
	}
	definitions := append([]query.Definition{}, dashboard.Dashboard.Queries...)
	parameters := map[string]any{}
	for _, fragment := range dashboard.Fragments {
		path := filepath.Join("../../../dashboard/site", fragment)
		content, err := os.ReadFile(path) // #nosec G304 -- fragments come from the checked-in dashboard manifest in this repository test.
		if err != nil {
			return nil, nil, fmt.Errorf("read deployed dashboard fragment %q: %w", fragment, err)
		}
		var document struct {
			Queries []query.Definition `json:"queries"`
			Pages   []struct {
				Form struct {
					Fields []struct {
						ID      string `json:"id"`
						Default any    `json:"default"`
					} `json:"fields"`
				} `json:"form"`
			} `json:"pages"`
		}
		if err := json.Unmarshal(content, &document); err != nil {
			return nil, nil, fmt.Errorf("parse deployed dashboard fragment %q: %w", fragment, err)
		}
		definitions = append(definitions, document.Queries...)
		for _, page := range document.Pages {
			for _, field := range page.Form.Fields {
				if field.ID != "" && field.Default != nil {
					parameters[field.ID] = field.Default
				}
			}
		}
	}
	databaseQueries, err := ParseDashboardQueries("../../../dashboard/site/src/data/queries/database.json")
	if err != nil {
		return nil, nil, fmt.Errorf("read canonical database queries: %w", err)
	}
	definitions = append(definitions, databaseQueries...)
	if len(definitions) == 0 {
		return nil, nil, fmt.Errorf("deployed dashboard query corpus is empty")
	}
	return definitions, parameters, nil
}

func bindDashboardSQLCorpusInputs(definitions []query.Definition, parameters map[string]any) error {
	for definitionIndex := range definitions {
		definitions[definitionIndex].Compute = append([]query.ComputedField(nil), definitions[definitionIndex].Compute...)
		for computedIndex := range definitions[definitionIndex].Compute {
			computed := &definitions[definitionIndex].Compute[computedIndex]
			computed.Args = append([]query.Argument(nil), computed.Args...)
			for argumentIndex := range computed.Args {
				argument := &computed.Args[argumentIndex]
				if argument.Parameter == "" {
					continue
				}
				value, ok := parameters[argument.Parameter]
				if !ok {
					return fmt.Errorf("dashboard query %q requires parameter %q with no deployed form default",
						definitions[definitionIndex].Name, argument.Parameter)
				}
				*argument = query.Argument{Value: value}
			}
		}
	}
	ResolveQueryContext(definitions, "2026-10-02T00:00:00Z")
	return nil
}
