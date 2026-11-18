package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDashboardIngestionBrowserStress(t *testing.T) {
	if os.Getenv("CAO_DASHBOARD_BROWSER_STRESS") != "1" {
		t.Skip("run npm run test:stress:dashboard-local-ingestion with disposable local POSTGRES_URL")
	}
	admin, site := os.Getenv("POSTGRES_URL"), os.Getenv("CAO_DASHBOARD_TEST_SITE")
	binary := os.Getenv("CAO_DASHBOARD_TEST_SERVER")
	if admin == "" || !filepath.IsAbs(site) || !filepath.IsAbs(binary) {
		t.Fatal("selected browser stress requires POSTGRES_URL and absolute built site/server paths")
	}
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN",
		"ACTIONS_RUNTIME_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_URL", "CAO_POSTGRES_URL"} {
		t.Setenv(name, "")
	}
	t.Setenv("DEBUG", "cao:dashboard:ingest-stream,cao:ingest")
	t.Setenv("DEBUG_COLORS", "0")
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	report := os.Getenv("CAO_DASHBOARD_STRESS_REPORT_DIR")
	if report == "" {
		report = filepath.Join(root, "test-results", "dashboard-local-ingestion-stress")
	}
	report, err = filepath.Abs(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(report, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	postgres := provisionDashboardServingPostgres(t, ctx, binary, admin)
	workspace, err := os.MkdirTemp(filepath.Join(root, ".tmp"), "dashboard-browser-stress-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(workspace); err != nil {
			t.Error(err)
		}
	})
	t.Chdir(workspace)
	cache := filepath.Join(workspace, "canonical")
	github := newDashboardColdStartGitHub(t)
	github.artifactHandler = github.stressArtifacts
	acquire := make(chan struct{})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(acquire) }) })
	gh := func(ctx context.Context, args ...string) (string, string, error) {
		if len(args) == 4 && args[0] == "api" && args[3] == "rate_limit" {
			select {
			case <-acquire:
			case <-ctx.Done():
				return "", "", ctx.Err()
			}
		}
		return github.execute(ctx, args...)
	}
	output := &dashboardServingOutput{ready: make(chan string, 1)}
	command := dashboardCommand("browser-stress", gh, github.stressLogs)
	command.SilenceUsage, command.SilenceErrors = true, true
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs([]string{"--repo", "octo/api", "--cache-dir", cache, "--server-executable", binary,
		"--site", site, "--listen", "127.0.0.1:0", "--postgres-url", postgres,
		"--duration", "4m", "--refresh", "5m", "--count", "200"})
	session, stop := context.WithCancel(ctx)
	defer stop()
	exited := make(chan error, 1)
	go func() { exited <- command.ExecuteContext(session) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-exited:
			if err != nil {
				t.Errorf("dashboard shutdown: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("dashboard stress left its native processes running")
		}
		// Native debug logs deliberately contain no capability or PostgreSQL URL.
		log := output.diagnostics()
		if err := os.WriteFile(filepath.Join(report, "ingestion.log"), []byte(log), 0o600); err != nil {
			t.Error(err)
		}
	})
	var address string
	select {
	case address = <-output.ready:
	case err := <-exited:
		exited <- err
		t.Fatalf("dashboard exited before readiness: %v\n%s", err, output.diagnostics())
	case <-ctx.Done():
		t.Fatalf("dashboard startup: %v\n%s", ctx.Err(), output.diagnostics())
	}
	capability, err := url.Parse(address)
	if err != nil || capability.Hostname() != "127.0.0.1" || capability.Query().Get("access_token") == "" {
		t.Fatal("dashboard did not announce a numeric-loopback capability")
	}
	browserInput, browserDone := startDashboardStressBrowser(t, ctx, root, workspace, report, address, len(github.expected))
	waitDashboardStressFile(t, ctx, filepath.Join(workspace, "browser-ready"), browserDone)
	release.Do(func() { close(acquire) })
	started := time.Now()
	client := dashboardColdStartClient(t)
	for {
		status, err := dashboardStressStatus(ctx, client, capability)
		if err != nil {
			t.Fatalf("query persisted ingestion progress: %v\n%s", err, output.diagnostics())
		}
		if status == "failed" {
			t.Fatalf("ingestion entered the five-minute failure retry path\n%s", output.diagnostics())
		}
		if status == "watching" {
			state, err := readDashboardIngestion(ctx, cache)
			if err != nil || len(state.Cursors) != 1 || !state.Cursors["octo/api"].Done {
				t.Fatal("ingestion reported watching without completing its history cursor")
			}
			break
		}
		if time.Since(started) > 150*time.Second {
			t.Fatalf("ingestion did not complete before the five-minute retry: status=%s error=%v\n%s",
				status, err, output.diagnostics())
		}
		select {
		case err := <-exited:
			exited <- err
			t.Fatalf("dashboard exited during ingestion: %v", err)
		case err := <-browserDone:
			browserDone <- err
			t.Fatalf("browser exited during ingestion: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	sources, err := dashboardStressSources(ctx, client, capability,
		`{"sourceNames":["tools","issues","output-lifecycles"],"pagination":{"tools":{"limit":1000}}}`)
	if err != nil {
		t.Fatal(err)
	}
	for name, expected := range map[string]int{"tools": len(github.expected), "issues": 1, "output-lifecycles": 2} {
		if got := len(sources[name].Rows); got != expected {
			t.Fatalf("reconciled %s evidence: got %d rows, want %d", name, got, expected)
		}
	}
	if _, err := browserInput.Write([]byte("complete\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-browserDone:
		browserDone <- err
		if err != nil {
			t.Fatalf("browser stress failed; see browser.log and report.json: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	log := output.diagnostics()
	for _, required := range []string{
		`authoritative reconciliation required: collection="$issues"`,
		`mode="run-bundles" ok=true`, "duration_ms=", "revision=",
	} {
		if !strings.Contains(log, required) {
			t.Fatalf("stress did not exercise/log %q\n%s", required, log)
		}
	}
	if strings.Contains(log, "ok=false") || strings.Contains(log, capability.Query().Get("access_token")) {
		t.Fatal("ingestion failed or exposed its capability in debug logs")
	}
	github.mutex.Lock()
	defer github.mutex.Unlock()
	if github.historyPages < 16 || github.oversized == 0 || len(github.passes) < 2 ||
		github.artifactRequests < len(github.expected) || len(github.violations) != 0 {
		t.Fatalf("incomplete synthetic history/artifact coverage: pages=%d oversized=%d passes=%d artifacts=%d violations=%v",
			github.historyPages, github.oversized, len(github.passes), github.artifactRequests, github.violations)
	}
	t.Logf("1600 synthetic upstream runs; %d executed runs with artifacts; %d history pages; completed in %s with Chromium open",
		len(github.expected), github.historyPages, time.Since(started).Round(time.Millisecond))
}

func dashboardStressStatus(ctx context.Context, client *http.Client, capability *url.URL) (string, error) {
	sources, err := dashboardStressSources(ctx, client, capability, `{"sourceNames":["local-ingestion-progress"]}`)
	if err != nil {
		return "", err
	}
	rows := sources["local-ingestion-progress"].Rows
	if len(rows) == 0 {
		return "", nil
	}
	var result struct{ Status string }
	if err := json.Unmarshal(rows[0], &result); err != nil {
		return "", err
	}
	return result.Status, nil
}

type dashboardStressSource struct {
	Rows []json.RawMessage `json:"rows"`
}

func dashboardStressSources(ctx context.Context, client *http.Client, capability *url.URL, input string) (map[string]dashboardStressSource, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		capability.Scheme+"://"+capability.Host+"/api/v1/query",
		strings.NewReader(input))
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
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ingestion query returned %d", response.StatusCode)
	}
	var result struct {
		Sources map[string]dashboardStressSource `json:"sources"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return nil, err
	}
	return result.Sources, nil
}

func startDashboardStressBrowser(t *testing.T, ctx context.Context, root, workspace, report, address string, runs int) (*os.File, chan error) {
	t.Helper()
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(report, "browser.log"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(root, "tests/helpers/dashboard-ingestion-browser.mjs"))
	command.Dir = root
	command.Env = append(dashboardServerEnvironment(), "CAO_STRESS_URL="+address,
		"CAO_STRESS_WORKSPACE="+workspace, "CAO_STRESS_REPORT_DIR="+report,
		fmt.Sprintf("CAO_STRESS_RUNS=%d", runs))
	command.Stdin, command.Stdout, command.Stderr = input, log, log
	command.WaitDelay = 5 * time.Second
	if err := command.Start(); err != nil {
		_ = input.Close()
		_ = writer.Close()
		_ = log.Close()
		t.Fatal(err)
	}
	_ = input.Close()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		_ = writer.Close()
		select {
		case err := <-done:
			done <- err
		case <-time.After(15 * time.Second):
			if err := command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Error(err)
			}
			<-done
		}
		_ = log.Close()
	})
	return writer, done
}

func waitDashboardStressFile(t *testing.T, ctx context.Context, path string, done chan error) {
	t.Helper()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("browser exited before readiness; see browser.log: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
