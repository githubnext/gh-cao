package server

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/simulator"
)

func assertCompanyCalculator(t *testing.T, ctx context.Context, local *httptest.Server, company simulator.Company, totals simulator.CompanyTotals) {
	t.Helper()
	data, err := os.ReadFile("../../../dashboard/site/dashboard-fragments/simulators.json")
	if err != nil {
		t.Fatal(err)
	}
	var fragment struct {
		Queries []query.Definition `json:"queries"`
	}
	if err := json.Unmarshal(data, &fragment); err != nil {
		t.Fatal(err)
	}
	parameters := map[string]any{
		"company-repositories": company.Repositories, "developers": company.Developers,
		"prs-per-developer-per-day": company.PRsPerDeveloperPerDay, "aw-runs-per-pr": company.AWRunsPerPR,
		"days": company.Days, "aic-per-run": company.AICPerRun,
	}
	input := queryRequest{SourceNames: []string{"simulator-pr-summary"}}
	for _, definition := range fragment.Queries {
		if !strings.HasPrefix(definition.Name, "simulator-pr-") {
			continue
		}
		for i := range definition.Compute {
			for j, argument := range definition.Compute[i].Args {
				if argument.Parameter == "" {
					continue
				}
				value, exists := parameters[argument.Parameter]
				if !exists {
					t.Fatalf("unknown calculator parameter %s", argument.Parameter)
				}
				definition.Compute[i].Args[j] = query.Argument{Value: value}
			}
		}
		input.Queries = append(input.Queries, definition)
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var result queryResponse
	backfillRequest(t, ctx, local, http.MethodPost, "/api/v1/query", body, http.StatusOK, &result)
	rows := result.Sources["simulator-pr-summary"].Rows
	if len(rows) != 3 {
		t.Fatalf("calculator did not resolve through Go runtime/query sources: %+v", result)
	}
	for index, expected := range [][2]float64{
		{float64(totals.DailyPRs), float64(totals.PRs)},
		{float64(totals.DailyRuns), float64(totals.Runs)},
		{totals.DailyAIC, totals.AIC},
	} {
		for fieldIndex, field := range []string{"daily", "total"} {
			value, ok := rows[index][field].(float64)
			if !ok || math.Abs(value-expected[fieldIndex]) > max(1e-6, expected[fieldIndex]*1e-12) {
				t.Fatalf("Go calculator row %d %s=%v want=%v", index, field, rows[index][field], expected[fieldIndex])
			}
		}
	}
}
