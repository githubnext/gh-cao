package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// This opt-in gate needs no GitHub credentials: acquisition crosses a strict
// loopback HTTP simulator, then production normalization, ingestion and queries.
func TestDashboardColdStartIntegration(t *testing.T) {
	if os.Getenv("CAO_DASHBOARD_COLD_START_INTEGRATION") != "1" {
		t.Skip("set CAO_DASHBOARD_COLD_START_INTEGRATION=1 with CAO_DASHBOARD_TEST_POSTGRES_URL and CAO_DASHBOARD_TEST_SITE")
	}
	admin, site := os.Getenv("CAO_DASHBOARD_TEST_POSTGRES_URL"), os.Getenv("CAO_DASHBOARD_TEST_SITE")
	if admin == "" || !filepath.IsAbs(site) {
		t.Fatal("selected cold-start gate requires disposable loopback CAO_DASHBOARD_TEST_POSTGRES_URL and absolute CAO_DASHBOARD_TEST_SITE")
	}
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN",
		"ACTIONS_RUNTIME_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_URL", "CAO_POSTGRES_URL"} {
		t.Setenv(name, "")
	}
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(repository, ".tmp")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(workspace, "dashboard-cold-start-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove cold-start workspace: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	binary := os.Getenv("CAO_DASHBOARD_TEST_SERVER")
	if binary == "" {
		binary = filepath.Join(directory, "cao-dashboard")
		build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-o", binary, "./cmd/cao-dashboard")
		build.Dir = filepath.Join(repository, "server")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build native dashboard: %v\n%s", err, output)
		}
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("CAO_DASHBOARD_TEST_SERVER must be absolute")
	}
	if _, _, cleanup, err := dashboardServerAssets(dashboardOptions{server: binary, site: site}); err != nil {
		t.Fatalf("selected cold-start gate requires real built assets: %v", err)
	} else {
		cleanup()
	}
	postgres := provisionDashboardServingPostgres(t, ctx, binary, admin)
	t.Chdir(directory)
	cache := filepath.Join(directory, "canonical")
	simulator := newDashboardColdStartGitHub(t)
	client := dashboardColdStartClient(t)

	// Hold acquisition until the real server proves its newly provisioned
	// namespace is empty, rather than seeding canonical evidence as a fixture.
	startCollection := make(chan struct{})
	firstPublication := make(chan struct{})
	var releaseInitial, releaseProgress sync.Once
	t.Cleanup(func() {
		releaseInitial.Do(func() { close(startCollection) })
		releaseProgress.Do(func() { close(firstPublication) })
	})
	gh := func(ctx context.Context, args ...string) (string, string, error) {
		if len(args) == 4 && args[0] == "api" && args[3] == "rate_limit" {
			select {
			case <-startCollection:
			case <-ctx.Done():
				return "", "", ctx.Err()
			}
		}
		return simulator.execute(ctx, args...)
	}
	simulator.beforeSecondPass = func(ctx context.Context) error {
		select {
		case <-firstPublication:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	started := time.Now()
	for phase := range 2 {
		phaseStarted := time.Now()
		simulator.mutex.Lock()
		passesBefore := len(simulator.passes)
		simulator.mutex.Unlock()
		output := &dashboardServingOutput{ready: make(chan string, 1)}
		session, stop := context.WithCancel(ctx)
		command := dashboardCommand("cold-start-test", gh, simulator.logs)
		command.SilenceUsage, command.SilenceErrors = true, true
		command.SetOut(output)
		command.SetErr(output)
		command.SetArgs([]string{"--repo", "octo/api", "--cache-dir", cache, "--server-executable", binary,
			"--site", site, "--listen", "127.0.0.1:0", "--postgres-url", postgres,
			"--duration", "3m", "--refresh", "1m", "--count", "200"})
		exited := make(chan struct{})
		var commandErr error
		go func() {
			commandErr = command.ExecuteContext(session)
			close(exited)
		}()
		t.Cleanup(func() {
			stop()
			select {
			case <-exited:
			case <-time.After(15 * time.Second):
				t.Error("cold-start command failed to reap native child")
			}
		})
		var address string
		select {
		case address = <-output.ready:
		case <-exited:
			t.Fatalf("dashboard exited before readiness: %v\n%s", commandErr, output.diagnostics())
		case <-time.After(30 * time.Second):
			t.Fatalf("dashboard did not serve promptly\n%s", output.diagnostics())
		}
		capability, err := url.Parse(address)
		if err != nil || capability.Scheme != "http" || capability.User != nil ||
			net.ParseIP(capability.Hostname()) == nil || !net.ParseIP(capability.Hostname()).IsLoopback() ||
			capability.Query().Get("access_token") == "" {
			t.Fatal("dashboard readiness did not return a loopback capability")
		}
		query := func() ([]map[string]any, error) {
			request, err := http.NewRequestWithContext(session, http.MethodPost,
				capability.Scheme+"://"+capability.Host+"/api/v1/query",
				strings.NewReader(`{"sourceNames":["runs"],"pagination":{"runs":{"limit":1000}}}`))
			if err != nil {
				return nil, err
			}
			request.Header.Set("Authorization", "Bearer "+capability.Query().Get("access_token"))
			request.Header.Set("Content-Type", "application/json")
			response, err := client.Do(request)
			if err != nil {
				return nil, err
			}
			defer func() { _ = response.Body.Close() }()
			var result struct {
				Sources map[string]struct {
					Rows []map[string]any `json:"rows"`
				} `json:"sources"`
			}
			if response.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("canonical runs query returned %d", response.StatusCode)
			}
			if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&result); err != nil {
				return nil, err
			}
			source, ok := result.Sources["runs"]
			if !ok {
				return nil, fmt.Errorf("canonical query omitted runs source")
			}
			return source.Rows, nil
		}
		if phase == 0 {
			rows, err := query()
			if err != nil || len(rows) != 0 {
				t.Fatalf("cold start must begin with empty PostgreSQL runs: rows=%d error=%v", len(rows), err)
			}
			releaseInitial.Do(func() { close(startCollection) })
		}
		deadline := time.Now().Add(45 * time.Second)
		progressive := phase != 0
		for {
			rows, err := query()
			if err != nil {
				t.Fatalf("production canonical query: %v\n%s", err, output.diagnostics())
			}
			simulator.assertRows(t, rows, false)
			if !progressive && len(rows) > 0 && len(rows) < len(simulator.expected) {
				if time.Since(started) > 45*time.Second {
					t.Fatal("initial partial evidence was not published promptly")
				}
				progressive = true
				t.Logf("first PostgreSQL publication: %d/%d executed runs after %s; upstream has 1600 runs",
					len(rows), len(simulator.expected), time.Since(started).Round(time.Millisecond))
				releaseProgress.Do(func() { close(firstPublication) })
			}
			state, stateErr := readDashboardIngestion(session, cache)
			observed, _ := time.Parse(time.RFC3339Nano, state.CreatedAt)
			simulator.mutex.Lock()
			freshPass := len(simulator.passes) > passesBefore
			simulator.mutex.Unlock()
			if stateErr == nil && state.Status == "watching" && observed.After(phaseStarted) &&
				freshPass && len(rows) == len(simulator.expected) {
				simulator.assertRows(t, rows, true)
				if !progressive {
					t.Fatal("history completed without progressive PostgreSQL publication")
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("history failed to backfill before normal 1m refresh: phase=%d rows=%d/%d state=%s error=%v\n%s",
					phase, len(rows), len(simulator.expected), state.Status, stateErr, output.diagnostics())
			}
			select {
			case <-exited:
				t.Fatalf("dashboard exited during collection: %v\n%s", commandErr, output.diagnostics())
			case <-time.After(100 * time.Millisecond):
			}
		}
		stop()
		select {
		case <-exited:
			if commandErr != nil {
				t.Fatalf("dashboard cancellation: %v\n%s", commandErr, output.diagnostics())
			}
		case <-time.After(15 * time.Second):
			t.Fatal("dashboard cancellation left child running")
		}
	}
	simulator.mutex.Lock()
	defer simulator.mutex.Unlock()
	if simulator.historyPages < 16 || simulator.oversized == 0 || len(simulator.passes) < 3 || simulator.artifactRequests == 0 {
		t.Fatalf("test did not exercise split, paginated history, artifacts and restart: pages=%d oversized=%d passes=%d artifacts=%d",
			simulator.historyPages, simulator.oversized, len(simulator.passes), simulator.artifactRequests)
	}
	if gap := simulator.passes[1].Sub(simulator.passes[0]); gap >= 30*time.Second {
		t.Fatalf("history waited for normal refresh instead of back-to-back passes: %s", gap)
	}
	if len(simulator.violations) != 0 {
		t.Fatalf("simulator rejected acquisition: %v", simulator.violations)
	}
}

func dashboardColdStartClient(t *testing.T) *http.Client {
	t.Helper()
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
				return nil, fmt.Errorf("cold-start test forbids non-loopback HTTP")
			}
			return (&net.Dialer{}).DialContext(ctx, network, address)
		},
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("cold-start test forbids redirects")
		}}
}

type dashboardColdStartGitHub struct {
	t                *testing.T
	server           *httptest.Server
	client           *http.Client
	runs             []map[string]any
	expected         map[string][2]string
	beforeSecondPass func(context.Context) error
	mutex            sync.Mutex
	passes           []time.Time
	historyPages     int
	oversized        int
	artifactRequests int
	violations       []string
}

func newDashboardColdStartGitHub(t *testing.T) *dashboardColdStartGitHub {
	t.Helper()
	github := &dashboardColdStartGitHub{t: t, client: dashboardColdStartClient(t), expected: map[string][2]string{}}
	now := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	for index := range 1600 {
		id := strconv.Itoa(10000 + index)
		created := now.Add(-time.Duration(index/4) * 20 * time.Minute).Format(time.RFC3339)
		status, conclusion := "completed", any("skipped")
		if index%2 == 1 {
			status, conclusion = "waiting", nil
		}
		// The recent 200 are entirely non-executed, and older pages contain
		// same-second ties plus completed, running and queued real attempts.
		if index >= 200 && index%5 == 0 {
			status, conclusion = "completed", "success"
			switch index % 20 {
			case 5:
				conclusion = "failure"
			case 10:
				status, conclusion = "in_progress", nil
			case 15:
				status, conclusion = "queued", nil
			}
			github.expected[id] = [2]string{status, nativeText(conclusion)}
		}
		github.runs = append(github.runs, map[string]any{
			"id": 10000 + index, "workflow_id": 42, "name": "Agent", "display_title": "Synthetic cold start",
			"status": status, "conclusion": conclusion, "run_attempt": 1, "event": "schedule",
			"head_branch": "main", "head_sha": strings.Repeat("a", 40),
			"created_at": created, "run_started_at": created, "updated_at": created,
			"html_url": "https://github.com/octo/api/actions/runs/" + id,
		})
	}
	github.server = httptest.NewServer(http.HandlerFunc(github.serveHTTP))
	t.Cleanup(github.server.Close)
	return github
}

func (g *dashboardColdStartGitHub) reject(w http.ResponseWriter, message string) {
	g.mutex.Lock()
	g.violations = append(g.violations, message)
	g.mutex.Unlock()
	http.Error(w, message, http.StatusForbidden)
}

func (g *dashboardColdStartGitHub) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" ||
		r.URL.User != nil || r.Host != strings.TrimPrefix(g.server.URL, "http://") {
		g.reject(w, "unexpected method, authority or credentials")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	var result any
	switch r.URL.Path {
	case "/rate_limit":
		result = map[string]any{"resources": map[string]any{"core": dashboardQuota{
			Limit: 5000, Remaining: 5000, Reset: time.Now().Add(time.Hour).Unix(),
		}}}
	case "/repos/octo/api":
		result = map[string]any{"nameWithOwner": "octo/api", "url": "https://github.com/octo/api", "id": "R_synthetic"}
	case "/repos/octo/api/actions/workflows":
		result = []dashboardWorkflow{{42, "Agent", ".github/workflows/agent.lock.yml", "active"}}
	case "/repos/octo/api/actions/runs":
		query := r.URL.Query()
		size, sizeErr := strconv.Atoi(query.Get("per_page"))
		page, pageErr := strconv.Atoi(query.Get("page"))
		created := query.Get("created")
		if len(query) != 3 || sizeErr != nil || pageErr != nil || size < 1 || size > 200 || page < 1 {
			g.reject(w, "unbounded or unexpected Actions query")
			return
		}
		var selected []map[string]any
		if strings.HasPrefix(created, ">=") {
			g.mutex.Lock()
			g.passes = append(g.passes, time.Now())
			pass := len(g.passes)
			g.mutex.Unlock()
			if pass == 2 && g.beforeSecondPass != nil {
				if err := g.beforeSecondPass(r.Context()); err != nil {
					return
				}
			}
			selected = g.runs
		} else {
			from, through, ok := strings.Cut(created, "..")
			start, startErr := time.Parse(time.RFC3339, from)
			end, endErr := time.Parse(time.RFC3339, through)
			if !ok || startErr != nil || endErr != nil || end.Before(start) || size > 100 {
				g.reject(w, "invalid fixed history interval")
				return
			}
			for _, run := range g.runs {
				at, _ := time.Parse(time.RFC3339, run["created_at"].(string))
				if !at.Before(start) && !at.After(end) {
					selected = append(selected, run)
				}
			}
			g.mutex.Lock()
			g.historyPages++
			if len(selected) > 1000 {
				g.oversized++
			}
			g.mutex.Unlock()
		}
		begin := min((page-1)*size, len(selected))
		rows := append([]map[string]any{}, selected[begin:min(begin+size, len(selected))]...)
		result = map[string]any{"total_count": len(selected), "workflow_runs": rows}
	case "/repos/octo/api/actions/artifacts":
		result = map[string]any{"total_count": 0, "artifacts": []any{}}
	default:
		g.reject(w, "unexpected GitHub resource: "+r.URL.Path)
		return
	}
	if err := json.NewEncoder(w).Encode(result); err != nil && r.Context().Err() == nil {
		g.t.Errorf("simulated GitHub response: %v", err)
	}
}

func (g *dashboardColdStartGitHub) get(ctx context.Context, path string) ([]byte, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\r\n#") {
		return nil, fmt.Errorf("unexpected simulator path")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, g.server.URL+path, nil)
	if err != nil {
		return nil, err
	}
	response, err := g.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("simulated GitHub rejected request: %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 8<<20))
}

func (g *dashboardColdStartGitHub) execute(ctx context.Context, args ...string) (string, string, error) {
	var path string
	switch {
	case reflect.DeepEqual(args, []string{"repo", "view", "--json", "nameWithOwner,url"}),
		reflect.DeepEqual(args, []string{"repo", "view", "github.com/octo/api", "--json", "nameWithOwner,url,id"}):
		path = "/repos/octo/api"
	case reflect.DeepEqual(args, []string{"workflow", "list", "--repo", "github.com/octo/api", "--all", "--limit", "1000", "--json", "id,name,path,state"}):
		path = "/repos/octo/api/actions/workflows"
	case len(args) == 4 && reflect.DeepEqual(args[:3], []string{"api", "--hostname", "github.com"}):
		if args[3] != "rate_limit" && !strings.HasPrefix(args[3], "repos/octo/api/actions/runs?") {
			return "", "", fmt.Errorf("forbidden GitHub API resource: %v", args)
		}
		path = "/" + args[3]
	case len(args) == 10 && reflect.DeepEqual(args[:5], []string{"run", "list", "--repo", "github.com/octo/api", "--created"}) &&
		args[6] == "--limit" && args[7] == "200" && args[8] == "--json" &&
		args[9] == "databaseId,workflowDatabaseId,workflowName,displayTitle,status,conclusion,event,headBranch,headSha,createdAt,startedAt,updatedAt,url,attempt":
		path = "/repos/octo/api/actions/runs?" + url.Values{"created": {args[5]}, "per_page": {"200"}, "page": {"1"}}.Encode()
	default:
		return "", "", fmt.Errorf("forbidden GitHub command (no subprocess fallback): %v", args)
	}
	content, err := g.get(ctx, path)
	if err == nil && args[0] == "run" {
		var response struct {
			Runs []map[string]any `json:"workflow_runs"`
		}
		if err = json.Unmarshal(content, &response); err == nil {
			rows := make([]map[string]any, 0, len(response.Runs))
			for _, raw := range response.Runs {
				row := map[string]any{}
				for from, to := range map[string]string{
					"id": "databaseId", "workflow_id": "workflowDatabaseId", "name": "workflowName",
					"display_title": "displayTitle", "status": "status", "conclusion": "conclusion",
					"event": "event", "head_branch": "headBranch", "head_sha": "headSha", "run_attempt": "attempt",
					"created_at": "createdAt", "run_started_at": "startedAt", "updated_at": "updatedAt", "html_url": "url",
				} {
					row[to] = raw[from]
				}
				rows = append(rows, row)
			}
			content, err = json.Marshal(rows)
		}
	}
	return string(content), "", err
}

func (g *dashboardColdStartGitHub) logs(ctx context.Context, request dashboardLogRequest) error {
	if request.Repository != "octo/api" || request.Count < 1 || request.Count > 200 || request.StartDate == "" {
		return fmt.Errorf("forbidden or unbounded artifact acquisition")
	}
	selected := 0
	for id := range g.expected {
		number, _ := strconv.ParseInt(id, 10, 64)
		if number > request.AfterRunID && number < request.BeforeRunID {
			selected++
		}
	}
	first := strconv.FormatInt(request.BeforeRunID-1, 10)
	last := strconv.FormatInt(request.AfterRunID+1, 10)
	_, firstExecuted := g.expected[first]
	_, lastExecuted := g.expected[last]
	if !firstExecuted || !lastExecuted || selected == 0 || selected > request.Count ||
		len(request.WorkflowCounts) != 1 || request.WorkflowCounts["42"] != selected {
		g.mutex.Lock()
		g.violations = append(g.violations, "artifact selection included skipped/waiting or out-of-scope attempts")
		g.mutex.Unlock()
		return fmt.Errorf("artifact selection included skipped/waiting or out-of-scope attempts")
	}
	g.mutex.Lock()
	g.artifactRequests++
	g.mutex.Unlock()
	// No artifacts is a valid upstream observation; never invoke gh-aw or gh.
	if _, err := g.get(ctx, "/repos/octo/api/actions/artifacts"); err != nil {
		return err
	}
	return os.WriteFile(request.Cache, nil, 0o600)
}

func (g *dashboardColdStartGitHub) assertRows(t *testing.T, rows []map[string]any, complete bool) {
	t.Helper()
	seen := map[string]bool{}
	for _, row := range rows {
		id := nativeText(row["run"])
		want, ok := g.expected[id]
		if !ok || seen[id] || row["organization"] != "octo" || row["repository"] != "api" ||
			row["run-status"] != want[0] || nativeText(row["run-conclusion"]) != want[1] {
			t.Fatalf("canonical query lost status/scope/identity or admitted skipped/waiting run: %v", row)
		}
		seen[id] = true
	}
	if complete && len(seen) != len(g.expected) {
		t.Fatalf("canonical executed population: got %d want %d", len(seen), len(g.expected))
	}
}
