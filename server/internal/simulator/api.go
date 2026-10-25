package simulator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type compiledWindow struct {
	start time.Duration
	end   time.Duration
	spec  APIWindow
}

// APIStats counts requests served by the fake GitHub API. Metered requests
// are the ones charged against the scenario rate limit; RateLimited counts
// metered requests rejected because that budget was exhausted.
type APIStats struct {
	Requests    uint64 `json:"requests"`
	Metered     uint64 `json:"metered"`
	RateLimited uint64 `json:"rateLimited"`
}

// API is a scenario-controlled fake GitHub REST API.
type API struct {
	handler     http.Handler
	requests    atomic.Uint64
	metered     atomic.Uint64
	rateLimited atomic.Uint64
	limiter     *primaryLimiter
	started     time.Time
}

// Started reports when scenario time began; API windows are measured from it.
func (a *API) Started() time.Time {
	return a.started
}

// ServeHTTP implements http.Handler.
// Requests under the GitHub Enterprise Server "/api/v3" prefix are served
// as their github.com equivalents, so go-github clients configured with the
// simulator as an enterprise base URL reach the same routes.
func (a *API) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if trimmed, found := strings.CutPrefix(request.URL.Path, "/api/v3/"); found {
		request = request.Clone(request.Context())
		request.URL.Path = "/" + trimmed
		request.URL.RawPath = ""
	}
	a.handler.ServeHTTP(writer, request)
}

// Stats returns a snapshot of the request counters.
func (a *API) Stats() APIStats {
	return APIStats{
		Requests:    a.requests.Load(),
		Metered:     a.metered.Load(),
		RateLimited: a.rateLimited.Load(),
	}
}

type rateHeaders struct {
	limit     int
	remaining int
	used      int
	reset     time.Time
}

// primaryLimiter is a fixed-window request budget shared by every caller,
// modelling GitHub's per-installation primary rate limit.
type primaryLimiter struct {
	mutex     sync.Mutex
	limit     int
	window    time.Duration
	timeScale float64
	started   time.Time
	index     int64
	used      int
}

func (l *primaryLimiter) take(now time.Time) (rateHeaders, bool) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	elapsed := time.Duration(float64(now.Sub(l.started)) * l.timeScale)
	index := int64(elapsed / l.window)
	if index != l.index {
		l.index = index
		l.used = 0
	}
	realWindow := time.Duration(float64(l.window) / l.timeScale)
	reset := l.started.Add(time.Duration(index+1) * realWindow)
	allowed := l.used < l.limit
	if allowed {
		l.used++
	}
	return rateHeaders{limit: l.limit, remaining: l.limit - l.used, used: l.used, reset: reset}, allowed
}

func (l *primaryLimiter) peek(now time.Time) rateHeaders {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	elapsed := time.Duration(float64(now.Sub(l.started)) * l.timeScale)
	index := int64(elapsed / l.window)
	used := l.used
	if index != l.index {
		used = 0
	}
	realWindow := time.Duration(float64(l.window) / l.timeScale)
	reset := l.started.Add(time.Duration(index+1) * realWindow)
	return rateHeaders{limit: l.limit, remaining: l.limit - used, used: used, reset: reset}
}

// NewAPIHandler creates a fake GitHub REST API backed by the scenario's
// time-varying failure windows and optional primary rate limit.
func NewAPIHandler(scenario Scenario, timeScale float64) (*API, error) {
	scenario.defaults()
	if err := scenario.Validate(); err != nil {
		return nil, err
	}
	if timeScale <= 0 || timeScale > 1_000_000 {
		return nil, errors.New("simulator API time scale must be greater than 0 and at most 1000000")
	}
	windows := make([]compiledWindow, 0, len(scenario.API))
	for _, window := range scenario.API {
		start, _ := time.ParseDuration(window.From)
		end, _ := time.ParseDuration(window.To)
		windows = append(windows, compiledWindow{start: start, end: end, spec: window})
	}
	started := time.Now()
	api := &API{started: started}
	if scenario.RateLimit != nil {
		window, _ := time.ParseDuration(scenario.RateLimit.Window)
		api.limiter = &primaryLimiter{
			limit: scenario.RateLimit.Limit, window: window, timeScale: timeScale, started: started,
		}
	}
	mux := http.NewServeMux()
	success := func(writer http.ResponseWriter, request *http.Request, rate rateHeaders) {
		if strings.HasPrefix(request.URL.Path, "/repos/") {
			parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/repos/"), "/")
			if len(parts) >= 2 {
				name := parts[0] + "/" + parts[1]
				first := repositoryName(0)
				if scenario.RenameRepositories {
					first += "-renamed"
				}
				if (scenario.RenameRepositories && name == repositoryName(0)) ||
					(scenario.DeleteRepositories && name == first) {
					setRateHeaders(writer, rate)
					writeNotFound(writer)
					return
				}
			}
		}
		payload, handled, err := scenario.historyResponse(writer, request)
		if handled {
			setRateHeaders(writer, rate)
			writer.Header().Set("Content-Type", "application/json")
			if err != nil {
				writer.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(writer).Encode(map[string]string{"message": err.Error()})
				return
			}
			_ = json.NewEncoder(writer).Encode(payload)
			return
		}
		writeGitHubSuccess(writer, request, rate, scenario)
	}
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
		count := api.requests.Add(1)
		var rate *rateHeaders
		if api.limiter != nil {
			now := time.Now()
			if metered(request) {
				api.metered.Add(1)
				headers, allowed := api.limiter.take(now)
				if !allowed {
					api.rateLimited.Add(1)
					writePrimaryRateLimit(writer, headers)
					return
				}
				rate = &headers
			} else {
				headers := api.limiter.peek(now)
				rate = &headers
			}
		}
		elapsed := time.Duration(float64(time.Since(started)) * timeScale)
		window, ok := windowAt(windows, elapsed)
		if !ok {
			success(writer, request, successRate(APIWindow{}, rate))
			return
		}
		if applyFailure(writer, request, window.spec, count) {
			return
		}
		success(writer, request, successRate(window.spec, rate))
	})
	api.handler = mux
	return api, nil
}

// metered reports whether GitHub charges a request against the installation
// primary rate limit. Rate-limit inspection and App-authenticated calls are
// not charged.
func metered(request *http.Request) bool {
	switch {
	case request.URL.Path == "/rate_limit", request.URL.Path == "/app":
		return false
	case strings.HasPrefix(request.URL.Path, "/app/"):
		return false
	default:
		return true
	}
}

func successRate(window APIWindow, limited *rateHeaders) rateHeaders {
	if limited != nil {
		return *limited
	}
	remaining := window.RateLimitRemaining
	if remaining == 0 {
		remaining = 5000
	}
	resetAfter := window.RateLimitResetAfter
	if resetAfter == 0 {
		resetAfter = 3600
	}
	return rateHeaders{
		limit: 5000, remaining: remaining, used: 5000 - remaining,
		reset: time.Now().Add(time.Duration(resetAfter) * time.Second),
	}
}

func setRateHeaders(writer http.ResponseWriter, rate rateHeaders) {
	writer.Header().Set("X-RateLimit-Limit", strconv.Itoa(rate.limit))
	writer.Header().Set("X-RateLimit-Remaining", strconv.Itoa(rate.remaining))
	writer.Header().Set("X-RateLimit-Used", strconv.Itoa(rate.used))
	writer.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetUnix(rate.reset), 10))
	writer.Header().Set("X-RateLimit-Resource", "core")
}

// resetUnix rounds the reset instant up so clients never resume early.
func resetUnix(reset time.Time) int64 {
	seconds := reset.Unix()
	if reset.After(time.Unix(seconds, 0)) {
		seconds++
	}
	return seconds
}

func writePrimaryRateLimit(writer http.ResponseWriter, rate rateHeaders) {
	setRateHeaders(writer, rate)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(writer).Encode(map[string]string{
		"message":           "API rate limit exceeded for installation ID 1.",
		"documentation_url": "https://docs.github.com/rest/overview/resources-in-the-rest-api#rate-limiting",
	})
}

func windowAt(windows []compiledWindow, elapsed time.Duration) (compiledWindow, bool) {
	for _, window := range windows {
		if elapsed >= window.start && elapsed < window.end {
			return window, true
		}
	}
	return compiledWindow{}, false
}

func applyFailure(writer http.ResponseWriter, request *http.Request, window APIWindow, requestNumber uint64) bool {
	switch window.Mode {
	case "healthy":
		return false
	case "latency":
		latency, _ := time.ParseDuration(window.Latency)
		timer := time.NewTimer(latency)
		defer timer.Stop()
		select {
		case <-request.Context().Done():
			return true
		case <-timer.C:
			return false
		}
	case "timeout":
		<-request.Context().Done()
		return true
	case "connection-failure":
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			http.Error(writer, "simulated connection failure", http.StatusServiceUnavailable)
			return true
		}
		connection, _, err := hijacker.Hijack()
		if err == nil {
			_ = connection.Close()
		}
		return true
	case "intermittent":
		rate := window.FailureRate
		if rate == 0 {
			rate = 0.25
		}
		if window.FailEvery > 0 && requestNumber%uint64(window.FailEvery) == 0 {
			return writeFailure(writer, window)
		}
		if float64(requestNumber%10000)/10000 < rate {
			return writeFailure(writer, window)
		}
		return false
	default:
		return writeFailure(writer, window)
	}
}

func writeFailure(writer http.ResponseWriter, window APIWindow) bool {
	status, supported := map[string]int{
		"rate-limited":         http.StatusTooManyRequests,
		"secondary-rate-limit": http.StatusForbidden,
		"internal-error":       http.StatusInternalServerError,
		"bad-gateway":          http.StatusBadGateway,
		"service-unavailable":  http.StatusServiceUnavailable,
		"unavailable":          http.StatusServiceUnavailable,
		"intermittent":         http.StatusServiceUnavailable,
	}[window.Mode]
	if !supported {
		return false
	}
	resetAfter := window.RateLimitResetAfter
	if resetAfter == 0 {
		resetAfter = 60
	}
	remaining := window.RateLimitRemaining
	if remaining == 0 {
		remaining = 5000
	}
	switch status {
	case http.StatusTooManyRequests:
		writer.Header().Set("Retry-After", strconv.Itoa(resetAfter))
		writer.Header().Set("X-RateLimit-Limit", "5000")
		writer.Header().Set("X-RateLimit-Remaining", "0")
		writer.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Duration(resetAfter)*time.Second).Unix(), 10))
	case http.StatusForbidden:
		// GitHub secondary limits leave primary headroom intact and signal
		// backoff through Retry-After.
		writer.Header().Set("Retry-After", strconv.Itoa(resetAfter))
		writer.Header().Set("X-RateLimit-Limit", "5000")
		writer.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
		writer.Header().Set("X-GitHub-Request-Id", "SIMULATED")
	default:
		writer.Header().Set("X-RateLimit-Limit", "5000")
		writer.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	message := "simulated GitHub API failure"
	documentation := "https://docs.github.com/rest/overview/resources-in-the-rest-api#rate-limiting"
	switch status {
	case http.StatusTooManyRequests:
		message = "API rate limit exceeded"
	case http.StatusForbidden:
		message = "You have exceeded a secondary rate limit"
		documentation = "https://docs.github.com/rest/overview/rate-limits-for-the-rest-api#about-secondary-rate-limits"
	}
	_ = json.NewEncoder(writer).Encode(map[string]string{
		"message":           message,
		"documentation_url": documentation,
	})
	return true
}

func writeGitHubSuccess(writer http.ResponseWriter, request *http.Request, rate rateHeaders, scenario Scenario) {
	setRateHeaders(writer, rate)
	writer.Header().Set("Content-Type", "application/json")
	switch {
	case request.URL.Path == "/rate_limit":
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"resources": map[string]any{"core": map[string]any{
				"limit": rate.limit, "remaining": rate.remaining, "used": rate.used,
				"reset": resetUnix(rate.reset),
			}},
		})
	case request.URL.Path == "/app":
		_ = json.NewEncoder(writer).Encode(map[string]any{"id": 1, "name": "CAO Simulator"})
	case request.Method == http.MethodPost && strings.Contains(request.URL.Path, "/access_tokens"):
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"token":       "simulator-installation-token",
			"expires_at":  time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"permissions": map[string]string{"actions": "read", "metadata": "read"},
		})
	case strings.HasSuffix(request.URL.Path, "/actions/runs"):
		_ = json.NewEncoder(writer).Encode(map[string]any{"total_count": 0, "workflow_runs": []any{}})
	case strings.HasSuffix(request.URL.Path, "/actions/runs/"):
		writer.WriteHeader(http.StatusNoContent)
	case strings.HasSuffix(request.URL.Path, "/logs"):
		writer.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(request.URL.Path, "/repos/") && strings.Contains(request.URL.Path, "/git/"):
		// Simulated repositories have no Git refs, trees, or blobs, so memory
		// branch lookups resolve as absent.
		writeNotFound(writer)
	case strings.HasPrefix(request.URL.Path, "/repos/"):
		parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
		if len(parts) >= 3 {
			first := repositoryName(0)
			if scenario.RenameRepositories {
				first += "-renamed"
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"id":        1,
				"full_name": parts[1] + "/" + parts[2],
				"private":   false,
				"archived":  scenario.ArchiveRepositories && !scenario.UnarchiveRepositories && parts[1]+"/"+parts[2] == first,
				"owner":     map[string]string{"login": parts[1]},
			})
			return
		}
		writeNotFound(writer)
	case request.URL.Path == "/graphql":
		_ = json.NewEncoder(writer).Encode(map[string]any{"data": map[string]any{}})
	default:
		writeNotFound(writer)
	}
}

func writeNotFound(writer http.ResponseWriter) {
	writer.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(writer).Encode(map[string]string{
		"message": "Not Found",
	})
}

// Listen starts the fake API listener and reports only its address on failure.
func Listen(ctx context.Context, address string, handler http.Handler) (*http.Server, net.Listener, error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		return nil, nil, fmt.Errorf("listen for simulator API: %w", err)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	return server, listener, nil
}
