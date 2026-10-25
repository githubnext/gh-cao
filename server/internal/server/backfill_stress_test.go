package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collect"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/simulator"
)

func TestPostgresBackfillStress(t *testing.T) {
	if os.Getenv("CAO_BACKFILL_STRESS") != "1" {
		t.Skip("run npm run test:stress:dashboard-backfill for synthetic enterprise backfill")
	}
	backfillServices(t)
	repositories := backfillStressInteger(t, "CAO_BACKFILL_REPOSITORIES", 1000)
	runsPerDay := backfillStressInteger(t, "CAO_BACKFILL_RUNS_PER_DAY", 1)
	const horizon = 7
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Minute)
	defer cancel()
	started := time.Now()
	service := fmt.Sprintf("cao-backfill-stress-%d", started.UnixNano())
	flush := backfillTelemetry(t, ctx, service)
	scenario := simulator.Scenario{
		Name: "backfill-stress", Repositories: repositories, EventsPerRepository: 1, Seed: 42,
		DuplicateEvery: 10, ReplayCount: min(100, repositories), OutOfOrder: true,
		WebhookRetryLimit: 20,
		History: &simulator.History{
			Days: 14, RunsPerDay: runsPerDay, AsOf: started.Add(-time.Minute).UTC().Format(time.RFC3339),
		},
		RateLimit: &simulator.APIRateLimit{Limit: 1_000_000, Window: "1h"},
	}
	h := newSyntheticBackfill(t, ctx, scenario)
	h.backfill.Queue.Debounce = time.Hour
	secret := "synthetic-backfill-webhook-signing-secret"
	app, err := New(ctx, h.ops, Config{
		Database: h.data, DatabaseQueriesPath: backfillDatabaseQueries,
		Listen: "127.0.0.1:0", SiteDirectory: t.TempDir(), AccessToken: testAccessToken,
		Collector:     &CollectorConfig{AppID: 1, AdmitOnly: true, QueueMaxLength: 5_000_000},
		WebhookSecret: secret, QueryCache: QueryCacheConfig{Disabled: true},
		RateLimits: RateLimitConfig{
			General: RateLimitPolicy{Capacity: 2_000_000, Window: time.Minute},
			Edge:    RateLimitPolicy{Capacity: 2_000_000, Window: time.Minute},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	var active atomic.Bool
	var overlapping atomic.Int64
	local := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/github/webhook" && active.Load() {
			overlapping.Add(1)
		}
		handler.ServeHTTP(writer, request)
	}))
	defer local.Close()
	transport, err := simulator.NewLocalTransport(local.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer transport.CloseIdleConnections()
	webhookClient := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	backfillReadiness(t, ctx, local, http.StatusServiceUnavailable)
	type completed struct {
		state    collect.BackfillState
		err      error
		duration time.Duration
	}
	done := make(chan completed, 1)
	active.Store(true)
	backfillStarted := time.Now()
	go func() {
		state, err := h.backfill.Run(ctx)
		active.Store(false)
		done <- completed{state: state, err: err, duration: time.Since(backfillStarted)}
	}()
	traffic, trafficErr := scenario.Deliver(ctx, webhookClient, local.URL+"/api/github/webhook", secret, 16)
	result := <-done
	backfillDuration := result.duration
	if result.err != nil || result.state.Phase != "collecting" || result.state.EnumerationFailures != 0 {
		t.Fatalf("enterprise backfill failed: %+v, %v", result.state, result.err)
	}
	if trafficErr != nil || traffic.Failed != 0 || traffic.Accepted != traffic.Attempts || overlapping.Load() == 0 {
		t.Fatalf("webhook traffic did not succeed concurrently with backfill: %+v overlapping=%d err=%v",
			traffic, overlapping.Load(), trafficErr)
	}
	expected := repositories * horizon * runsPerDay
	state := backfillState(t, ctx, h.data)
	if !state.Ready || state.Counts["$repositories"] != repositories ||
		state.Counts["$workflows"] != repositories || state.Counts["$runs"] != min(expected, query.MaxInputRows) ||
		result.state.QueuedRunTasks != expected {
		t.Fatalf("incorrect seven-day projection/admissions: counts=%v backfill=%+v expectedRuns=%d",
			state.Counts, result.state, expected)
	}
	coverage, err := h.backfill.Enrollment.Coverage(ctx)
	if err != nil || coverage.Repositories != int64(repositories) {
		t.Fatalf("synthetic enrollment coverage = %+v, %v", coverage, err)
	}
	validateSyntheticRunTasks(t, ctx, h.backfill, scenario, horizon)
	requests := h.proxy.RequestCount(syntheticRunPath, 1)
	rerun, err := h.backfill.Run(ctx)
	if err != nil || rerun.QueuedRunTasks != 0 || rerun.Revision != state.Revision ||
		h.proxy.RequestCount(syntheticRunPath, 1) != requests {
		t.Fatalf("enterprise rerun ignored completion cursors or duplicated historical work: %+v, %v", rerun, err)
	}
	depth, err := taskQueueLength(ctx, h.backfill.Queue.Tasks, "collect:run-tasks")
	if err != nil || depth != int64(expected) {
		t.Fatalf("durable run stream = %d, want %d: %v", depth, expected, err)
	}
	backfillReadiness(t, ctx, local, http.StatusOK)
	traceID := backfillStressSamples(t, ctx, local, scenario, horizon)
	if flush != nil {
		flush(t)
		assertBackfillTelemetry(t, ctx, service, traceID, started)
	}
	report := map[string]any{
		"repositories": repositories, "runsPerDay": runsPerDay, "horizonDays": horizon,
		"expectedRuns": expected, "queuedRuns": depth, "counts": state.Counts,
		"backfillDurationMs": backfillDuration.Milliseconds(), "durationMs": time.Since(started).Milliseconds(),
		"api": h.api.Stats(), "webhooks": traffic, "webhooksDuringBackfill": overlapping.Load(),
	}
	directory := os.Getenv("CAO_BACKFILL_REPORT_DIR")
	if directory == "" {
		directory = "../../../.tmp/go-backfill"
	}
	if err := os.MkdirAll(directory, 0o750); err != nil { // #nosec G703 -- operator-selected local report directory.
		t.Fatal(err)
	}
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G703 -- the operator selects the local report directory; the filename contains only a validated integer.
	if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("stress-%d.json", repositories)), content, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("repos=%d runs=%d backfill=%s concurrent_webhooks=%d", repositories, expected, backfillDuration, overlapping.Load())
}

func backfillStressInteger(t *testing.T, name string, fallback int) int {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 || number > 50_000 {
		t.Fatalf("%s must be a positive integer at most 50000", name)
	}
	return number
}

func backfillStressSamples(t *testing.T, ctx context.Context, local *httptest.Server, scenario simulator.Scenario, horizon int) string {
	t.Helper()
	input := queryRequest{}
	for index, run := range []simulator.HistoricalRun{
		scenario.History.Run(0, 0),
		scenario.History.Run(scenario.Repositories-1, 0),
	} {
		name := fmt.Sprintf("stress-sample-%d", index)
		input.Queries = append(input.Queries, query.Definition{
			Name: name, From: "$runs",
			Filter: &query.Filter{Predicates: []query.Predicate{{Field: "id", Equals: fmt.Sprintf("run:%d", run.ID)}}},
		})
		input.SourceNames = append(input.SourceNames, name)
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var result queryResponse
	traceID := backfillRequest(t, ctx, local, http.MethodPost, "/api/v1/query", body, http.StatusOK, &result)
	for _, name := range input.SourceNames {
		if len(result.Sources[name].Rows) != 1 {
			t.Fatalf("native SQL lost boundary run %s", name)
		}
	}
	expected := min(scenario.Repositories*horizon*scenario.History.RunsPerDay, query.MaxInputRows)
	input = queryRequest{
		Queries: []query.Definition{{Name: "retained-runs", From: "$runs",
			Aggregate: &query.Aggregate{Values: []query.AggregateValue{{Field: "id", As: "runs", Reducer: "count"}}}}},
		SourceNames: []string{"retained-runs"},
	}
	body, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	backfillRequest(t, ctx, local, http.MethodPost, "/api/v1/query", body, http.StatusOK, &result)
	if rows := result.Sources["retained-runs"].Rows; len(rows) != 1 || fmt.Sprint(rows[0]["runs"]) != fmt.Sprint(expected) {
		t.Fatalf("bounded retained-run query lost fresh evidence: %+v want=%d", result, expected)
	}
	return traceID
}
