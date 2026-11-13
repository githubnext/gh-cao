package server

// Rate-limit stress harness for the Go dashboard server.
//
// The harness drives the production HTTP handler (security headers, access
// control, inbound rate limiting, and the lazy repository-memory resolver)
// against real Operational and Postgres and the scenario-controlled GitHub API simulator. It
// deliberately configures very low inbound and upstream limits so every run
// exhausts them, then asserts that the server throttles cleanly: only 200 or
// 429 responses, standard Retry-After and RateLimit-* headers, no GitHub
// requests past the governed budget, and recovery once the budget resets.
//
// It runs only when POSTGRES_URL names a disposable PostgreSQL instance:
//
//	POSTGRES_URL=postgres://postgres@127.0.0.1:5432/cao?sslmode=disable \
//	  go test ./internal/server -run '^TestStress' -bench '^BenchmarkStress' -benchmem
//
// Optional environment:
//
//	CAO_STRESS_REPORT_DIR   directory for per-scenario JSON reports
//	CAO_STRESS_MAX_P95_MS   p95 latency budget in milliseconds (default 250)
//	CAO_STRESS_MIN_RPS      minimum sustained requests per second (default 200)

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
	operationalpostgres "github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/repositorymemory"
	"github.com/githubnext/gh-cao/server/internal/simulator"
)

const (
	stressInstallationID  = 1
	stressControlRepo     = "simulator/control"
	stressDefaultP95MS    = 250
	stressDefaultMinRPS   = 200
	stressThrottleMessage = "repository memory is temporarily throttled"
	stressInboundMessage  = "rate limit exceeded"
)

type stressOptions struct {
	scenario    simulator.Scenario
	rateLimits  RateLimitConfig
	budgetFloor int
}

type stressHarness struct {
	server    *httptest.Server
	simulator *simulator.API
	github    *githubapp.Client
	store     *operationalpostgres.Store
	governor  *githubapp.Budget
}

// stressAppKey generates the simulated App key once per process so key
// generation does not dominate the published CPU profile.
var stressAppKey = sync.OnceValues(func() ([]byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), nil
})

type fixedInstallation int64

func (f fixedInstallation) InstallationFor(context.Context, string) (int64, error) {
	return int64(f), nil
}

func newStressHarness(tb testing.TB, options stressOptions) *stressHarness {
	tb.Helper()

	if os.Getenv("POSTGRES_URL") == "" {
		tb.Skip("POSTGRES_URL is required for stress tests")
	}
	store, err := operationalpostgres.New(context.Background(), os.Getenv("POSTGRES_URL"), "stress-"+strconv.FormatInt(time.Now().UnixNano(), 36), operationalpostgres.Config{})
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = store.DeleteNamespace(context.Background()); _ = store.Close() })
	database := integrationDatabase(tb)

	api, err := simulator.NewAPIHandler(options.scenario, 1)
	if err != nil {
		tb.Fatal(err)
	}
	upstream := httptest.NewServer(api)
	tb.Cleanup(upstream.Close)

	privateKey, err := stressAppKey()
	if err != nil {
		tb.Fatal(err)
	}
	github, err := githubapp.New(githubapp.Config{
		AppID:          1,
		PrivateKeyPEM:  privateKey,
		BaseURL:        upstream.URL + "/",
		RequestTimeout: 10 * time.Second,
	})
	if err != nil {
		tb.Fatal(err)
	}

	site := tb.TempDir()
	if err := os.WriteFile(filepath.Join(site, "index.html"), []byte("<html><body></body></html>"), 0o600); err != nil {
		tb.Fatal(err)
	}
	app, err := New(context.Background(), store, Config{
		Database:      database,
		Listen:        "127.0.0.1:0",
		SiteDirectory: site,
		AccessToken:   testAccessToken,
		RateLimits:    options.rateLimits,
	})
	if err != nil {
		tb.Fatal(err)
	}
	floor := options.budgetFloor
	if floor <= 0 {
		floor = 1
	}
	governor := &githubapp.Budget{Metadata: store, Store: store, Floor: floor, Cost: 1}
	app.memory = &repositorymemory.RemoteResolver{
		Cache:             store,
		Installations:     fixedInstallation(stressInstallationID),
		Source:            github,
		Governor:          governor,
		ControlRepository: stressControlRepo,
	}
	server := httptest.NewServer(app.Handler())
	tb.Cleanup(server.Close)
	server.Client().Transport.(*http.Transport).MaxIdleConnsPerHost = 256
	return &stressHarness{server: server, simulator: api, github: github, store: store, governor: governor}
}

type stressResult struct {
	status     int
	latency    time.Duration
	retryAfter string
	policy     string
	remaining  string
	message    string
	err        error
}

func (h *stressHarness) get(ctx context.Context, path string) stressResult {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, h.server.URL+path, nil) // #nosec G704 -- httptest binds this endpoint to loopback.
	if err != nil {
		return stressResult{err: err}
	}
	request.Header.Set("Authorization", "Bearer "+testAccessToken)
	started := time.Now()
	response, err := h.server.Client().Do(request) // #nosec G704 -- the request targets the local httptest server.
	if err != nil {
		return stressResult{err: err, latency: time.Since(started)}
	}
	defer func() {
		_ = response.Body.Close()
	}()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	latency := time.Since(started)
	result := stressResult{
		status:     response.StatusCode,
		latency:    latency,
		retryAfter: response.Header.Get("Retry-After"),
		policy:     response.Header.Get("RateLimit-Policy"),
		remaining:  response.Header.Get("RateLimit-Remaining"),
		err:        err,
	}
	if response.StatusCode >= http.StatusBadRequest {
		var payload struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &payload)
		result.message = payload.Error
	}
	return result
}

// drive issues one GET per path with bounded concurrency and returns results
// in request order along with the wall-clock duration of the whole burst.
func (h *stressHarness) drive(ctx context.Context, paths []string, concurrency int) ([]stressResult, time.Duration) {
	results := make([]stressResult, len(paths))
	var next atomic.Int64
	var wait sync.WaitGroup
	started := time.Now()
	for range concurrency {
		wait.Go(func() {
			for {
				index := int(next.Add(1)) - 1
				if index >= len(paths) || ctx.Err() != nil {
					return
				}
				results[index] = h.get(ctx, paths[index])
			}
		})
	}
	wait.Wait()
	return results, time.Since(started)
}

type stressReport struct {
	Scenario       string             `json:"scenario"`
	Requests       int                `json:"requests"`
	Concurrency    int                `json:"concurrency"`
	DurationMS     float64            `json:"durationMs"`
	RequestsPerSec float64            `json:"requestsPerSecond"`
	Statuses       map[string]int     `json:"statuses"`
	LatencyMS      map[string]float64 `json:"latencyMs"`
	Upstream       simulator.APIStats `json:"upstream"`
	Thresholds     map[string]float64 `json:"thresholds"`
	Notes          map[string]any     `json:"notes,omitempty"`
}

func summarize(name string, results []stressResult, elapsed time.Duration, concurrency int) stressReport {
	latencies := make([]time.Duration, 0, len(results))
	statuses := map[string]int{}
	for _, result := range results {
		latencies = append(latencies, result.latency)
		key := strconv.Itoa(result.status)
		if result.err != nil {
			key = "error"
		}
		statuses[key]++
	}
	slices.Sort(latencies)
	percentile := func(p float64) float64 {
		if len(latencies) == 0 {
			return 0
		}
		index := min(len(latencies)-1, int(math.Ceil(p*float64(len(latencies))))-1)
		return float64(latencies[max(index, 0)].Microseconds()) / 1000
	}
	return stressReport{
		Scenario:       name,
		Requests:       len(results),
		Concurrency:    concurrency,
		DurationMS:     float64(elapsed.Microseconds()) / 1000,
		RequestsPerSec: float64(len(results)) / elapsed.Seconds(),
		Statuses:       statuses,
		LatencyMS: map[string]float64{
			"p50": percentile(0.50), "p90": percentile(0.90), "p95": percentile(0.95),
			"p99": percentile(0.99), "max": percentile(1),
		},
		Thresholds: map[string]float64{
			"maxP95Ms": float64(stressEnvInt("CAO_STRESS_MAX_P95_MS", stressDefaultP95MS)),
			"minRps":   float64(stressEnvInt("CAO_STRESS_MIN_RPS", stressDefaultMinRPS)),
		},
	}
}

func stressEnvInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// enforce publishes the report and fails the test when the burst breaches
// the configured latency or throughput threshold.
func (report stressReport) enforce(t *testing.T) {
	t.Helper()
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("stress report:\n%s", encoded)
	if directory := os.Getenv("CAO_STRESS_REPORT_DIR"); directory != "" {
		// #nosec G703 -- the operator chooses the local report directory.
		if err := os.MkdirAll(directory, 0o750); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, report.Scenario+".json")
		// #nosec G703 -- the file name is a fixed scenario identifier.
		if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if p95 := report.LatencyMS["p95"]; p95 > report.Thresholds["maxP95Ms"] {
		t.Errorf("p95 latency %.2fms exceeds the %.0fms budget", p95, report.Thresholds["maxP95Ms"])
	}
	if report.RequestsPerSec < report.Thresholds["minRps"] {
		t.Errorf("throughput %.1f req/s is below the %.0f req/s floor", report.RequestsPerSec, report.Thresholds["minRps"])
	}
}

func campaignPaths(prefix string, count int) []string {
	paths := make([]string, count)
	for index := range paths {
		paths[index] = fmt.Sprintf("/api/v1/memory/%s-%05d", prefix, index)
	}
	return paths
}

func repeatedPaths(path string, count int) []string {
	paths := make([]string, count)
	for index := range paths {
		paths[index] = path
	}
	return paths
}

// TestStressInboundRateLimit floods one cached endpoint past a deliberately
// low per-client token bucket and checks that admission never exceeds the
// bucket and every rejection is a well-formed 429.
func TestStressInboundRateLimit(t *testing.T) {
	const (
		capacity    = 50
		window      = time.Second
		requests    = 3000
		concurrency = 32
	)
	harness := newStressHarness(t, stressOptions{
		scenario: simulator.Scenario{Name: "stress-inbound", Repositories: 1},
		rateLimits: RateLimitConfig{
			General: RateLimitPolicy{Capacity: capacity, Window: window},
		},
	})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	// Resolve the campaign once so the flood measures admission, not the
	// single-flight GitHub lookup.
	if result := harness.get(ctx, "/api/v1/memory/stress-inbound"); result.status != http.StatusOK {
		t.Fatalf("warm-up status = %d (%s)", result.status, result.message)
	}
	results, elapsed := harness.drive(ctx, repeatedPaths("/api/v1/memory/stress-inbound", requests), concurrency)

	allowed, throttled := 0, 0
	for _, result := range results {
		switch {
		case result.err != nil:
			t.Fatalf("request failed: %v", result.err)
		case result.status == http.StatusOK:
			allowed++
		case result.status == http.StatusTooManyRequests:
			throttled++
			seconds, err := strconv.Atoi(result.retryAfter)
			if err != nil || seconds < 1 || seconds > int(window/time.Second)+1 {
				t.Fatalf("inbound 429 Retry-After = %q, want 1..%d", result.retryAfter, int(window/time.Second)+1)
			}
			if result.policy != fmt.Sprintf("%d;w=%d", capacity, int(window/time.Second)) || result.remaining != "0" ||
				result.message != stressInboundMessage {
				t.Fatalf("inbound 429 is malformed: policy=%q remaining=%q message=%q",
					result.policy, result.remaining, result.message)
			}
		default:
			t.Fatalf("unexpected status %d (%s) under inbound rate limiting", result.status, result.message)
		}
	}
	// A token bucket admits its capacity plus whatever refilled while the
	// burst ran; one extra token absorbs Operational/test clock rounding.
	ceiling := capacity + int(math.Ceil(float64(capacity)*elapsed.Seconds()/window.Seconds())) + 1
	if allowed < 1 || allowed > ceiling {
		t.Fatalf("admitted %d requests in %s, want 1..%d", allowed, elapsed, ceiling)
	}
	if throttled == 0 {
		t.Fatal("the burst never reached the inbound rate limit")
	}
	report := summarize("inbound-rate-limit", results, elapsed, concurrency)
	report.Upstream = harness.simulator.Stats()
	report.Notes = map[string]any{"allowed": allowed, "throttled": throttled, "admissionCeiling": ceiling}
	report.enforce(t)
}

// TestStressGitHubPrimaryRateLimit exhausts a low simulated GitHub primary
// budget through cache-missing repository-memory reads. The governor must stop
// calling GitHub at its floor, so the simulator never has to reject a request,
// and the server must recover once the simulated window resets.
func TestStressGitHubPrimaryRateLimit(t *testing.T) {
	const (
		limit       = 40
		floor       = 10
		window      = 3 * time.Second
		requests    = 1500
		concurrency = 32
	)
	harness := newStressHarness(t, stressOptions{
		scenario: simulator.Scenario{
			Name: "stress-primary", Repositories: 1,
			RateLimit: &simulator.APIRateLimit{Limit: limit, Window: window.String()},
		},
		rateLimits:  RateLimitConfig{General: RateLimitPolicy{Capacity: 1_000_000, Window: time.Minute}},
		budgetFloor: floor,
	})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	results, elapsed := harness.drive(ctx, campaignPaths("stress-primary", requests), concurrency)

	resolved, throttled := 0, 0
	for _, result := range results {
		switch {
		case result.err != nil:
			t.Fatalf("request failed: %v", result.err)
		case result.status == http.StatusOK:
			resolved++
		case result.status == http.StatusTooManyRequests && result.message == stressThrottleMessage:
			throttled++
			if seconds, err := strconv.Atoi(result.retryAfter); err != nil || seconds < 1 {
				t.Fatalf("throttled response Retry-After = %q, want a positive integer", result.retryAfter)
			}
		default:
			t.Fatalf("unexpected status %d (%s) under GitHub primary rate limiting", result.status, result.message)
		}
	}
	stats := harness.simulator.Stats()
	if stats.RateLimited != 0 {
		t.Fatalf("server sent %d GitHub requests past the exhausted primary budget", stats.RateLimited)
	}
	if throttled == 0 {
		t.Fatal("the burst never reached the governed GitHub budget")
	}
	if uint64(resolved) != stats.Metered {
		t.Fatalf("resolved %d cache misses with %d metered GitHub calls; each miss must cost exactly one call",
			resolved, stats.Metered)
	}
	windows := int(elapsed/window) + 1
	if resolved < 1 || resolved > windows*(limit-floor) {
		t.Fatalf("resolved %d requests across %d windows, want 1..%d", resolved, windows, windows*(limit-floor))
	}
	report := summarize("github-primary-rate-limit", results, elapsed, concurrency)
	report.Upstream = stats
	report.Notes = map[string]any{"resolved": resolved, "throttled": throttled, "windows": windows}

	// The governor treats an expired reset as unknown headroom and re-reads
	// GitHub's rate limit, so fresh reads must succeed after the window rolls.
	recovered := false
	deadline := time.Now().Add(3*window + 2*time.Second)
	for attempt := 0; !recovered && time.Now().Before(deadline); attempt++ {
		result := harness.get(ctx, fmt.Sprintf("/api/v1/memory/stress-recovery-%05d", attempt))
		switch {
		case result.err != nil:
			t.Fatalf("recovery request failed: %v", result.err)
		case result.status == http.StatusOK:
			recovered = true
		case result.status != http.StatusTooManyRequests:
			t.Fatalf("unexpected recovery status %d (%s)", result.status, result.message)
		default:
			time.Sleep(250 * time.Millisecond)
		}
	}
	if !recovered {
		t.Fatalf("server did not resume GitHub reads within %s of the budget reset", 3*window+2*time.Second)
	}
	if stats := harness.simulator.Stats(); stats.RateLimited != 0 {
		t.Fatalf("recovery sent %d GitHub requests past the primary budget", stats.RateLimited)
	}
	report.Notes["recovered"] = recovered
	report.enforce(t)
}

// TestStressGitHubSecondaryRateLimit sends a burst while GitHub answers with
// secondary rate limits, first on the installation-token mint and then on the
// REST calls behind an already minted token. The server must park the
// installation, answer 429 with a Retry-After that honours GitHub's, and stop
// calling GitHub.
func TestStressGitHubSecondaryRateLimit(t *testing.T) {
	t.Run("token-mint", func(t *testing.T) {
		stressSecondaryRateLimit(t, "github-secondary-rate-limit-token", false)
	})
	t.Run("rest-api", func(t *testing.T) {
		stressSecondaryRateLimit(t, "github-secondary-rate-limit-rest", true)
	})
}

func stressSecondaryRateLimit(t *testing.T, name string, mintFirst bool) {
	const (
		retryAfter  = 30
		requests    = 1500
		concurrency = 32
		healthyFor  = 2 * time.Second
	)
	from := "0s"
	if mintFirst {
		from = healthyFor.String()
	}
	harness := newStressHarness(t, stressOptions{
		scenario: simulator.Scenario{
			Name: name, Repositories: 1,
			API: []simulator.APIWindow{{
				From: from, To: "1h", Mode: "secondary-rate-limit", RateLimitResetAfter: retryAfter,
			}},
		},
		rateLimits: RateLimitConfig{General: RateLimitPolicy{Capacity: 1_000_000, Window: time.Minute}},
	})
	// A previous collection observed ample headroom, so the first reads
	// reach GitHub and discover the secondary limit.
	if err := harness.governor.Observe(t.Context(), stressInstallationID, 5000, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if mintFirst {
		started := harness.simulator.Started()
		if _, err := harness.github.InstallationToken(t.Context(), stressInstallationID); err != nil {
			t.Fatalf("mint installation token before the secondary-limit window: %v", err)
		}
		if time.Since(started) >= healthyFor {
			t.Fatalf("harness setup took %s, longer than the %s healthy window", time.Since(started), healthyFor)
		}
		time.Sleep(healthyFor - time.Since(started) + 100*time.Millisecond)
	}
	before := harness.simulator.Stats()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	results, elapsed := harness.drive(ctx, campaignPaths(name, requests), concurrency)

	for _, result := range results {
		switch {
		case result.err != nil:
			t.Fatalf("request failed: %v", result.err)
		case result.status == http.StatusTooManyRequests && result.message == stressThrottleMessage:
			seconds, err := strconv.Atoi(result.retryAfter)
			if err != nil || seconds < retryAfter-5 {
				t.Fatalf("secondary-limit Retry-After = %q, want at least %d", result.retryAfter, retryAfter-5)
			}
		default:
			t.Fatalf("unexpected status %d (%s) under GitHub secondary rate limiting", result.status, result.message)
		}
	}
	// Only requests already in flight when the first secondary limit landed
	// may reach GitHub.
	stats := harness.simulator.Stats()
	if sent := stats.Requests - before.Requests; sent > concurrency {
		t.Fatalf("server sent %d GitHub requests after a secondary limit, want at most %d", sent, concurrency)
	}
	_, parkedTo, err := harness.governor.Headroom(t.Context(), stressInstallationID)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(parkedTo) < time.Duration(retryAfter-5)*time.Second {
		t.Fatalf("installation parked until %s, want at least %ds from now", parkedTo, retryAfter-5)
	}
	report := summarize(name, results, elapsed, concurrency)
	report.Upstream = stats
	report.Notes = map[string]any{"upstreamDuringBurst": stats.Requests - before.Requests}
	report.enforce(t)
}

// BenchmarkStressInboundThrottled measures the cost of rejecting a request at
// the inbound rate limiter, which dominates server work during a flood.
func BenchmarkStressInboundThrottled(b *testing.B) {
	harness := newStressHarness(b, stressOptions{
		scenario:   simulator.Scenario{Name: "bench-inbound", Repositories: 1},
		rateLimits: RateLimitConfig{General: RateLimitPolicy{Capacity: 1, Window: time.Hour}},
	})
	benchmarkStress(b, harness, func(int64) string { return "/api/v1/memory/bench-inbound" })
}

// BenchmarkStressMemoryCached measures an admitted repository-memory read
// served from the Operational cache without calling GitHub.
func BenchmarkStressMemoryCached(b *testing.B) {
	harness := newStressHarness(b, stressOptions{
		scenario:   simulator.Scenario{Name: "bench-cached", Repositories: 1},
		rateLimits: RateLimitConfig{General: RateLimitPolicy{Capacity: 1_000_000, Window: time.Second}},
	})
	if result := harness.get(b.Context(), "/api/v1/memory/bench-cached"); result.status != http.StatusOK {
		b.Fatalf("warm-up status = %d (%s)", result.status, result.message)
	}
	benchmarkStress(b, harness, func(int64) string { return "/api/v1/memory/bench-cached" })
}

// BenchmarkStressGovernorExhausted measures cache-missing reads rejected by
// the GitHub budget governor after the simulated primary budget is spent.
func BenchmarkStressGovernorExhausted(b *testing.B) {
	harness := newStressHarness(b, stressOptions{
		scenario: simulator.Scenario{
			Name: "bench-governor", Repositories: 1,
			RateLimit: &simulator.APIRateLimit{Limit: 5, Window: "1h"},
		},
		rateLimits:  RateLimitConfig{General: RateLimitPolicy{Capacity: 1_000_000, Window: time.Second}},
		budgetFloor: 4,
	})
	var sequence atomic.Int64
	benchmarkStress(b, harness, func(int64) string {
		return fmt.Sprintf("/api/v1/memory/bench-governor-%d", sequence.Add(1))
	})
	if stats := harness.simulator.Stats(); stats.RateLimited != 0 {
		b.Fatalf("server sent %d GitHub requests past the exhausted budget", stats.RateLimited)
	}
}

func benchmarkStress(b *testing.B, harness *stressHarness, path func(int64) string) {
	b.Helper()
	var throttled, failed atomic.Int64
	b.ReportAllocs()
	b.SetParallelism(4)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var index int64
		for pb.Next() {
			index++
			result := harness.get(b.Context(), path(index))
			switch {
			case result.err != nil:
				failed.Add(1)
			case result.status == http.StatusTooManyRequests:
				throttled.Add(1)
			case result.status != http.StatusOK:
				failed.Add(1)
			}
		}
	})
	b.StopTimer()
	if failed.Load() > 0 {
		b.Fatalf("%d requests failed with a status other than 200 or 429", failed.Load())
	}
	b.ReportMetric(float64(throttled.Load())/float64(b.N), "throttled/op")
}
