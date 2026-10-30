package server

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/ingest"
	operationalpostgres "github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/simulator"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestCompanyIngestionSmoke(t *testing.T) {
	runCompanyStress(t, false)
}

func TestCompanyStress(t *testing.T) {
	if os.Getenv("CAO_COMPANY_STRESS") != "1" {
		t.Skip("run npm run test:stress:company -- --preset large for opt-in company stress")
	}
	runCompanyStress(t, true)
}

func runCompanyStress(t *testing.T, explicit bool) {
	t.Helper()
	started := time.Now()
	report := map[string]any{
		"status": "incomplete", "stage": "configuration", "synthetic": true,
		"coverage": map[string]bool{
			"generation": true, "ingestion": true, "postgres": true, "httpQueries": true,
			"awExecution": false, "awCompletion": false, "collection": false, "webhooks": false, "githubQuotas": false,
		},
		"ingestionBatchRows": postgresx.BatchRows,
	}
	directory := os.Getenv("CAO_COMPANY_REPORT_DIR")
	t.Cleanup(func() {
		if t.Failed() {
			report["status"] = "failed"
		}
		report["wallSeconds"] = time.Since(started).Seconds()
		if directory != "" {
			if err := os.MkdirAll(directory, 0o750); err != nil { // #nosec G703 -- operator-selected local test report directory.
				t.Error(err)
				return
			}
			data, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				t.Error(err)
			} else if err := os.WriteFile(filepath.Join(directory, "company-stress.json"), append(data, '\n'), 0o600); err != nil { // #nosec G703 -- fixed filename in operator-selected test directory.
				t.Error(err)
			}
		}
		t.Logf("company stress: status=%v generated=%v retained=%v wall=%s",
			report["status"], report["generatedRuns"], report["retainedRuns"], time.Since(started))
	})
	input := []byte(os.Getenv("CAO_COMPANY_SCENARIO"))
	if !explicit || len(input) == 0 {
		var err error
		input, err = os.ReadFile("../../../tests/fixtures/company-stress/small.json")
		if err != nil {
			t.Fatal(err)
		}
	}
	company, err := simulator.LoadCompany(input)
	if err != nil {
		t.Fatal(err)
	}
	totals, err := company.Totals()
	if err != nil {
		t.Fatal(err)
	}
	report["scenario"], report["expected"] = company, totals
	if os.Getenv("POSTGRES_URL") == "" && explicit {
		t.Fatal("POSTGRES_URL must name a disposable local PostgreSQL database")
	}
	if os.Getenv("POSTGRES_URL") == "" {
		t.Skip("set POSTGRES_URL for the company ingestion smoke test")
	}
	ctx := t.Context()
	report["stage"] = "database"
	data, connection := testutil.PostgresWithConfig(t, ctx, "POSTGRES_URL")
	report["stage"] = "generation"
	lake := t.TempDir()
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -company.Days)
	step := time.Now()
	if _, err := company.WriteLake(ctx, lake, start); err != nil {
		t.Fatal(err)
	}
	report["generationSeconds"], report["generatedRuns"] = time.Since(step).Seconds(), totals.Runs
	report["stage"] = "ingestion"
	step = time.Now()
	options := ingest.Options{DatabaseQueriesPath: backfillDatabaseQueries}
	state, err := ingest.Run(ctx, data, lake, options)
	if err != nil {
		t.Fatal(err)
	}
	seconds := time.Since(step).Seconds()
	report["ingestionSeconds"], report["ingestedRuns"] = seconds, totals.Runs
	report["ingestedRunsPerSecond"] = float64(totals.Runs) / seconds
	report["counts"], report["revision"] = state.Counts, state.Revision
	report["retainedRuns"] = state.Counts["$runs"]
	report["evictedOrExpiredRuns"] = totals.Runs - int64(state.Counts["$runs"])
	// This fixture has no child records and fits below the logical-byte budget.
	// The production 30-day cutoff and row ceiling remain unmodified.
	want := min(totals.Runs, totals.DailyRuns*30, int64(query.MaxInputRows))
	if int64(state.Counts["$runs"]) != want || state.Counts["$repositories"] != company.Repositories ||
		state.Counts["$workflows"] != company.Repositories {
		t.Fatalf("retention/inventory mismatch: counts=%v wantRuns=%d", state.Counts, want)
	}
	var heap runtime.MemStats
	runtime.ReadMemStats(&heap)
	report["postIngestionGoHeapBytes"] = heap.HeapAlloc
	report["stage"] = "queries"
	ops, err := operationalpostgres.New(ctx, connection.ConnString(), "company", operationalpostgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ops.Close() })
	app, err := New(ctx, ops, Config{
		Database: data, DatabaseQueriesPath: backfillDatabaseQueries, Listen: "127.0.0.1:0",
		SiteDirectory: t.TempDir(), AccessToken: testAccessToken, QueryCache: QueryCacheConfig{Disabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	local := httptest.NewServer(app.Handler())
	defer local.Close()
	inputQuery := queryRequest{
		Queries: []query.Definition{{
			Name: "company-retained", From: "$runs",
			Aggregate: &query.Aggregate{Values: []query.AggregateValue{
				{Field: "id", As: "runs", Reducer: "count"},
				{Field: "id", As: "uniqueRuns", Reducer: "distinct-count"},
				{Field: "aicTotal", As: "aic", Reducer: "sum"},
			}},
		}},
		SourceNames: []string{"company-retained"},
	}
	body, err := json.Marshal(inputQuery)
	if err != nil {
		t.Fatal(err)
	}
	var result queryResponse
	latencies := make([]float64, 0, 3)
	for range 3 {
		step = time.Now()
		backfillRequest(t, ctx, local, http.MethodPost, "/api/v1/query", body, http.StatusOK, &result)
		latencies = append(latencies, float64(time.Since(step).Microseconds())/1000)
		source := result.Sources["company-retained"]
		if want == 0 && len(source.Rows) == 0 {
			continue
		}
		if len(source.Rows) != 1 || fmt.Sprint(source.Rows[0]["runs"]) != fmt.Sprint(want) ||
			fmt.Sprint(source.Rows[0]["uniqueRuns"]) != fmt.Sprint(want) {
			t.Fatalf("retained query mismatch: %+v", source)
		}
		aic, ok := source.Rows[0]["aic"].(float64)
		expectedAIC := float64(want) * company.AICPerRun
		if !ok || math.Abs(aic-expectedAIC) > max(1e-6, expectedAIC*1e-12) {
			t.Fatalf("retained modeled AIC = %v, want %v", source.Rows[0]["aic"], expectedAIC)
		}
		if totals.Runs > want && source.Metadata["completeness"] != "partial" {
			t.Fatalf("evicted detail claimed complete coverage: %v", source.Metadata)
		}
	}
	report["queryLatencyMs"], report["queryResult"] = latencies, result.Sources
	assertCompanyCalculator(t, ctx, local, company, totals)
	report["dashboardCalculatorParity"] = true
	report["stage"] = "idempotency"
	again, err := ingest.Run(ctx, data, lake, options)
	if err != nil || again.Revision != state.Revision {
		t.Fatalf("identical fixture advanced revision: %+v %v", again, err)
	}
	report["idempotentReplay"] = true
	report["stage"] = "cancellation"
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ingest.Run(canceled, data, lake, options); err == nil {
		t.Fatal("canceled ingestion succeeded")
	}
	after, err := data.State(ctx)
	if err != nil || after.Revision != state.Revision {
		t.Fatal("canceled ingestion changed canonical state")
	}
	report["stage"], report["status"] = "complete", "complete"
}
