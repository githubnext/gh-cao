package postgresx

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

type monthlyCheckpoint struct {
	Clock       string         `json:"clock"`
	Counts      map[string]int `json:"counts"`
	Revision    int64          `json:"revision"`
	DurationMS  int64          `json:"durationMs"`
	RunQuery    string         `json:"runQuery"`
	SampleQuery string         `json:"sampleQuery"`
	HeapBytes   uint64         `json:"heapBytes"`
}

func TestMonthlyBudgetStress(t *testing.T) {
	if os.Getenv("CAO_MONTHLY_BUDGET_STRESS") != "1" {
		t.Skip("run npm run test:stress:monthly-budget for the simulated month")
	}
	if os.Getenv("POSTGRES_URL") == "" {
		t.Fatal("POSTGRES_URL must name a disposable local PostgreSQL database")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 35*time.Minute)
	defer cancel()
	started := time.Now()
	store, _ := nativeTestStore(t)
	// Start at the owner's initial clock and move forward only: persisted
	// projection watermarks deliberately reject clock regression.
	end := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, monthlyDays)
	days := monthlyBudgetWorkload(end, func(monthlyBudgetRun) {})
	definitions := canonicalProjectionDefinitions(t)
	checkpoints := make([]monthlyCheckpoint, 0, monthlyDays+4)
	observed, firstEvictionDay, monthEndRuns := 0, 0, 0
	for day := range monthlyDays {
		asOf := end.AddDate(0, 0, day+1-monthlyDays)
		stepStarted := time.Now()
		if err := store.RunPartitionMaintenance(ctx, asOf, defaultRunRetentionDays, defaultLinkedRetentionDays); err != nil {
			t.Fatal(err)
		}
		state := publishMonthlySnapshot(t, ctx, store, end, asOf)
		observed += days[day].Runs
		monthEndRuns = state.Counts["$runs"]
		if firstEvictionDay == 0 && monthEndRuns < observed {
			firstEvictionDay = day + 1
		}
		assertMonthlyStoredEvidence(t, ctx, store, days[:day+1], asOf)
		runQuery, sampleQuery := assertMonthlyQueryBoundaries(t, ctx, store, definitions, state.Counts["$runs"])
		var heap runtime.MemStats
		runtime.ReadMemStats(&heap)
		if heap.HeapAlloc > 512<<20 {
			t.Fatalf("daily ingestion heap exceeded 512 MiB: %d", heap.HeapAlloc)
		}
		checkpoints = append(checkpoints, monthlyCheckpoint{
			Clock: asOf.Format(time.RFC3339), Counts: state.Counts, Revision: state.Revision,
			DurationMS: time.Since(stepStarted).Milliseconds(), RunQuery: runQuery, SampleQuery: sampleQuery,
			HeapBytes: heap.HeapAlloc,
		})
		t.Logf("day=%d runs=%d audits=%d duration=%s run-query=%s sample-query=%s",
			day+1, state.Counts["$runs"], state.Counts["$audits"], time.Since(stepStarted), runQuery, sampleQuery)
	}
	if firstEvictionDay == 0 {
		t.Fatal("monthly workload never exercised capacity eviction")
	}
	var oldest time.Time
	if err := store.db.QueryRowContext(ctx, "SELECT min(run_at) FROM runs WHERE namespace=$1", store.namespace).Scan(&oldest); err != nil {
		t.Fatal(err)
	}
	expiry := dayStart(oldest).AddDate(0, 0, defaultRunRetentionDays+1)
	// Walk both sides of the oldest retained run's expiration. Maintenance must
	// remove whole subtrees, retain the cutoff day, and remain idempotent.
	var previousClock time.Time
	for _, asOf := range []time.Time{expiry.Add(-time.Second), expiry, expiry, end.AddDate(0, 0, 31)} {
		before, err := store.State(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RunPartitionMaintenance(ctx, asOf, defaultRunRetentionDays, defaultLinkedRetentionDays); err != nil {
			t.Fatal(err)
		}
		assertMonthlyStoredEvidence(t, ctx, store, days, asOf)
		after, err := store.State(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertMonthlyQueryBoundaries(t, ctx, store, definitions, after.Counts["$runs"])
		if asOf.Equal(previousClock) && before.Revision != after.Revision {
			t.Fatal("idempotent maintenance advanced the revision")
		}
		previousClock = asOf
		checkpoints = append(checkpoints, monthlyCheckpoint{
			Clock: asOf.Format(time.RFC3339), Counts: after.Counts, Revision: after.Revision,
		})
	}
	report := map[string]any{
		"scenario": "monthly-agent-budget", "synthetic": true, "seed": monthlySeed,
		"repositories": monthlyRepositories, "days": monthlyDays, "dailyBudgetCents": monthlyBudgetCents,
		"currency": "USD", "costDistribution": "half-normal: 100 + abs(N(0,125)) cents; reject above 500",
		"usdIsCanonicalAic": false, "ledger": days, "checkpoints": checkpoints,
		"runRetentionDays": defaultRunRetentionDays, "linkedRetentionDays": defaultLinkedRetentionDays,
		"capacityRowsPerSource": store.capacity().rows, "capacityLogicalBytes": store.capacity().bytes,
		"firstCapacityEvictionDay": firstEvictionDay, "historicalRuns": observed,
		"retainedRunsAtMonthEnd": monthEndRuns, "capacityEvictedRunsAtMonthEnd": observed - monthEndRuns,
		"durationMs": time.Since(started).Milliseconds(),
	}
	directory := os.Getenv("CAO_MONTHLY_BUDGET_REPORT_DIR")
	if directory == "" {
		directory = "../../../test-results/monthly-budget-stress"
	}
	if err := os.MkdirAll(directory, 0o750); err != nil { // #nosec G703 -- operator-selected local report directory.
		t.Fatal(err)
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G703 -- fixed report name under the operator-selected test artifact directory.
	if err := os.WriteFile(filepath.Join(directory, "monthly-budget.json"), content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func publishMonthlySnapshot(t *testing.T, ctx context.Context, store *Store, end, asOf time.Time) State {
	t.Helper()
	writer, err := store.BeginIngestion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(ctx)
	writer.ingestedAt = asOf
	appendRow := func(source string, row model.Row) {
		t.Helper()
		if err := writer.Append(ctx, source, row); err != nil {
			t.Fatal(err)
		}
	}
	for repo := range monthlyRepositories {
		repository, workflow := monthlyParentIDs(repo)
		appendRow("$repositories", model.Row{"id": repository, "owner": "simulator", "name": fmt.Sprintf("repo-%05d", repo)})
		appendRow("$workflows", model.Row{
			"id": workflow, "repositoryId": repository, "path": ".github/workflows/synthetic.md",
			"name": "Synthetic monthly agent", "state": "active", "role": "worker",
		})
	}
	monthlyBudgetWorkload(end, func(run monthlyBudgetRun) {
		if !run.At.Before(asOf) || run.At.Before(dayStart(asOf.AddDate(0, 0, -defaultRunRetentionDays))) {
			return
		}
		repository, workflow := monthlyParentIDs(run.Repository)
		id, at := fmt.Sprintf("run:%d", run.ID), run.At.Format(time.RFC3339)
		conclusion := "success"
		if run.ID%10 == 0 {
			conclusion = "failure"
		}
		appendRow("$runs", model.Row{
			"id": id, "repositoryId": repository, "workflowId": workflow,
			"owner": "simulator", "repository": fmt.Sprintf("repo-%05d", run.Repository),
			"workflowPath": ".github/workflows/synthetic.md", "githubRunId": fmt.Sprint(run.ID),
			"attempt": 1, "status": "completed", "conclusion": conclusion,
			"createdAt": at, "startedAt": at, "completedAt": run.At.Add(time.Minute).Format(time.RFC3339),
			"updatedAt": at, "observedAt": at, "aicTotal": float64(run.AicHundredths) / 100,
			"inputTokens": 4_000 + run.ID%1000, "outputTokens": 500 + run.ID%100,
			"engine": "copilot", "modelId": fmt.Sprintf("synthetic-model-%d", run.ID%4),
		})
		if !run.At.Before(dayStart(asOf.AddDate(0, 0, -defaultLinkedRetentionDays))) {
			appendRow("$audits", model.Row{
				"id": fmt.Sprintf("audit:%d", run.ID), "runId": id,
				"source": "session", "type": "agent.step", "timestamp": at,
			})
		}
	})
	if err := writer.Quality(ctx, "$runs", model.Metadata{
		"as-of": asOf.Format(time.RFC3339), "availability": "available", "completeness": "complete",
	}); err != nil {
		t.Fatal(err)
	}
	state, err := writer.Publish(ctx, "synthetic-monthly-budget:"+asOf.Format(time.DateOnly))
	if err != nil {
		t.Fatal(err)
	}
	if !state.Ready || state.Counts["$repositories"] != monthlyRepositories || state.Counts["$workflows"] != monthlyRepositories {
		t.Fatalf("snapshot lost usable fleet inventory: %+v", state)
	}
	return state
}

func monthlyParentIDs(repository int) (string, string) {
	name := fmt.Sprintf("simulator/repo-%05d", repository)
	return "repository:" + name, "workflow:" + name + ":synthetic"
}

func assertMonthlyQueryBoundaries(t *testing.T, ctx context.Context, store *Store, definitions []query.Definition, runs int) (string, string) {
	t.Helper()
	queries := []query.Definition{
		{Name: "fleet", From: "$repositories", Aggregate: &query.Aggregate{Values: []query.AggregateValue{{Field: "id", As: "repositories", Reducer: "count"}}}},
		{Name: "exact", From: "$runs", Aggregate: &query.Aggregate{Values: []query.AggregateValue{{Field: "id", As: "runs", Reducer: "count"}}}},
		{Name: "sample", From: "statistical-runs", Aggregate: &query.Aggregate{Values: []query.AggregateValue{{Field: "run", As: "runs", Reducer: "count"}}}},
	}
	definitions = append(append([]query.Definition{}, definitions...), queries...)
	result, _, err := store.ExecuteSQLPlan(ctx, definitions, []string{"fleet"})
	if err != nil || len(result["fleet"].Rows) != 1 || fmt.Sprint(result["fleet"].Rows[0]["repositories"]) != "10000" {
		t.Fatalf("bounded fleet query became unusable: %v %v", result, err)
	}
	result, _, err = store.ExecuteSQLPlan(ctx, definitions, []string{"exact"})
	exact := "available"
	if err != nil || runs > query.MaxInputRows || len(result["exact"].Rows) != 1 ||
		fmt.Sprint(result["exact"].Rows[0]["runs"]) != fmt.Sprint(runs) {
		t.Fatalf("capacity eviction did not preserve bounded exact queries: rows=%v retained=%d err=%v", result, runs, err)
	}
	result, metrics, err := store.ExecuteSQLPlan(ctx, definitions, []string{"sample"})
	if err != nil || len(metrics.FallbackOperations) != 0 {
		t.Fatalf("statistical query did not handle pressure safely: %v %+v", err, metrics)
	}
	sample := "available"
	if runs > 0 && result["sample"].Metadata["availability"] != "available" {
		t.Fatalf("bounded statistical frame became unavailable: %v", result["sample"].Metadata)
	}
	return exact, sample
}
