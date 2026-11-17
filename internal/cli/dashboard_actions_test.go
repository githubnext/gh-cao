package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func dashboardTestActionExecutable(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("native dashboard commands are unavailable on Windows")
	}
	executable := filepath.Join(t.TempDir(), "native-command")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf 'Native command executed without terminal input\\n'\nprintf '%s\\n' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return executable
}

func TestDashboardEnrollmentExecutesWithoutTerminalApproval(t *testing.T) {
	filename, cleanup, err := startDashboardActionAdapterWithExecutable(t.Context(), []string{"octo/control"}, t.TempDir(),
		dashboardTestActionExecutable(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var config struct{ AdapterURL string }
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, config.AdapterURL+"/execute",
		strings.NewReader(`["repos","add","octo/new-repository"]`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Command-ID", "1")
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("queued enrollment did not execute without terminal input: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK ||
		!bytes.Contains(body, []byte("Native command executed without terminal input")) ||
		!bytes.Contains(body, []byte(`"data":"repos\n"`)) ||
		!bytes.Contains(body, []byte(`"data":"add\n"`)) ||
		!bytes.Contains(body, []byte(`"data":"octo/new-repository\n"`)) ||
		!bytes.Contains(body, []byte(`"complete":true,"exitCode":0`)) {
		t.Fatalf("enrollment did not execute through the native queue adapter: %s %v", body, err)
	}
}

func TestDashboardSetupExecutesWithoutTerminalInput(t *testing.T) {
	filename, cleanup, err := startDashboardActionAdapterWithExecutable(t.Context(), []string{"octo/control"}, t.TempDir(),
		dashboardTestActionExecutable(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var config struct{ AdapterURL string }
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, config.AdapterURL+"/execute",
		strings.NewReader(`["setup","--repository","octo/control"]`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Command-ID", "2")
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("queued setup did not execute without terminal input: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK ||
		!bytes.Contains(body, []byte(`"data":"setup\n"`)) ||
		!bytes.Contains(body, []byte(`"data":"--repository\n"`)) ||
		!bytes.Contains(body, []byte(`"data":"octo/control\n"`)) ||
		!bytes.Contains(body, []byte(`"complete":true,"exitCode":0`)) {
		t.Fatalf("setup did not execute through the native queue adapter: %s %v", body, err)
	}
}

func TestDashboardActionScopeAndPublication(t *testing.T) {
	scope := []string{"octo/control"}
	for _, args := range [][]string{
		{"upgrade", "--repo", "octo/control", "--create-pull-request"},
		{"upgrade", "--repo", "octo/control", "--create-pull-request", "--pre-releases"},
		{"update", "https://github.com/octo/source/workflow", "--repo", "octo/control", "--create-pull-request"},
		{"update", ".github/workflows/review.md", "--repo", "octo/control", "--create-pull-request"},
		{"update", "review.v2.md", "--repo", "octo/control", "--create-pull-request"},
		{"disable", "review.v2", "--repo", "octo/control"},
		{"update", "--repo", "octo/control", "--create-pull-request"},
		{"disable", "review", "--repo", "octo/control"},
		{"disable", "--repo", "octo/control"},
		{"repos", "add", "octo/new-service"},
		{"repos", "add", "octo/service-*"},
		{"setup", "--repository", "octo/control"},
		{"setup", "--repository", "octo/*"},
	} {
		if err := dashboardActionArgs(args, scope); err != nil {
			t.Fatalf("valid maintenance rejected: %v", err)
		}
	}
	for _, args := range [][]string{
		{"upgrade", "--repo", "octo/other", "--create-pull-request"},
		{"upgrade", "--repo", "octo/control"},
		{"upgrade", "--repo", "octo/control", "--repo", "octo/other", "--create-pull-request"},
		{"upgrade", "--org", "github", "--create-pull-request"},
		{"upgrade", "--repo", "octo/control", "--create-pull-request", "--auto-merge"},
		{"update", "review.md", "--repo", "octo/control", "--pr-message", "Reviewed update", "--create-pull-request"},
		{"update", "https://evil.example/source", "--repo", "octo/control", "--create-pull-request"},
		{"update", "https://github.com/octo/source/../other", "--repo", "octo/control", "--create-pull-request"},
		{"update", "https://github.com/octo/source", "--repo", "octo/control", "--create-pull-request", "--pre-releases"},
		{"workflow", "run", "agent.yml", "--repo", "octo/control"},
		{"disable"},
		{"disable", "review* ", "--repo", "octo/control"},
		{"disable", "review..v2", "--repo", "octo/control"},
		{"disable", "review", "review-more", "--repo", "octo/control"},
		{"disable", "review", "--repo", "octo/other"},
		{"disable", "review", "--repo", "octo/control", "--create-pull-request"},
		{"update", "../review.md", "--repo", "octo/control", "--create-pull-request"},
		{"repos", "add"},
		{"repos", "add", "octo/a", "octo/b"},
		{"repos", "add", "octo/../other"},
		{"repos", "add", "--repo", "octo/api"},
		{"repos", "add", "octo"},
		{"setup"},
		{"setup", "--repository"},
		{"setup", "--repository", "octo/../other"},
		{"setup", "--repo", "octo/control"},
		{"setup", "--repository", "octo"},
	} {
		if dashboardActionArgs(args, scope) == nil {
			t.Fatalf("unscoped or unsupported maintenance accepted: %v", args)
		}
	}
}

func TestDashboardOperationsExcludeUpstreamButNotSiblingRepositories(t *testing.T) {
	scope := []string{"github/gh-aw", "github/gh-aw-actions"}
	for _, args := range [][]string{
		{"upgrade", "--repo", "github/gh-aw", "--create-pull-request"},
		{"update", "--repo", "GitHub/GH-AW", "--create-pull-request"},
		{"disable", "--repo", "github/gh-aw"},
		{"repos", "add", "github/gh-aw"},
		{"setup", "--repository", "github/gh-aw"},
	} {
		if dashboardActionArgs(args, scope) == nil {
			t.Fatalf("upstream repository operation was allowed: %v", args)
		}
	}
	if err := dashboardActionArgs([]string{"upgrade", "--repo", "github/gh-aw-actions", "--create-pull-request"}, scope); err != nil {
		t.Fatalf("upstream exclusion incorrectly blocked a sibling repository: %v", err)
	}
}

func TestDashboardDisableEnvironmentConfinesAmbientRepository(t *testing.T) {
	environment := dashboardDisableEnvironment([]string{
		"GH_TOKEN=test-credential", "GH_CONFIG_DIR=config", "PATH=bin",
		"GH_REPO=octo/other", "GH_HOST=evil.example",
		"GH_AW_WORKFLOWS_DIR=other/workflows",
		"GIT_DIR=other/.git", "GIT_WORK_TREE=other", "GIT_COMMON_DIR=other",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=remote.origin.url", "GIT_CONFIG_VALUE_0=other",
	})
	joined := strings.Join(environment, "\n")
	for _, name := range []string{"GH_REPO=", "GH_AW_WORKFLOWS_DIR=", "GIT_DIR=", "GIT_WORK_TREE=", "GIT_COMMON_DIR=", "GIT_CONFIG_"} {
		if strings.Contains(joined, name) {
			t.Fatalf("ambient repository override retained: %s", name)
		}
	}
	for _, entry := range []string{"GH_TOKEN=test-credential", "GH_CONFIG_DIR=config", "GH_HOST=github.com", "GIT_TERMINAL_PROMPT=0"} {
		if !strings.Contains(joined, entry) {
			t.Fatalf("required authentication or confinement missing: %s", entry)
		}
	}
	command, _, err := dashboardActionCommand().Find([]string{"disable"})
	if err != nil || command.Name() != "disable" || command.Flags().Lookup("repo") == nil {
		t.Fatal("native disable command is not registered")
	}
}

func TestDashboardActionOutputRedactsSplitCredentials(t *testing.T) {
	var events []dashboardActionEvent
	writer := &dashboardActionWriter{stream: "stdout", secrets: []string{"private-test-value"}, send: func(event dashboardActionEvent) error {
		events = append(events, event)
		return nil
	}}
	for _, part := range []string{"ordinary private-", "test-value\n", strings.Repeat("x", 9000), "\n"} {
		if _, err := writer.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 2 || events[0].Data != "ordinary [REDACTED]\n" ||
		events[1].Data != "[Overlong command output line suppressed.]\n" {
		t.Fatalf("unsafe output frames: %+v", events)
	}
}

func TestDashboardActionAdapterRequiresScopeAndPrivateCapability(t *testing.T) {
	filename, cleanup, err := startDashboardActionAdapterWithExecutable(t.Context(), []string{"octo/control"}, t.TempDir(),
		dashboardTestActionExecutable(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		AdapterURL string `json:"adapterUrl"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		args   string
		status int
	}{
		{`["upgrade","--repo","octo/other","--create-pull-request"]`, 403},
		{`["upgrade","--repo","octo/control","--create-pull-request"]`, 200},
	} {
		request, err := http.NewRequest(http.MethodPost, config.AdapterURL+"/execute", bytes.NewBufferString(test.args))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Command-ID", "1")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != test.status {
			t.Fatalf("adapter status: %d %v", response.StatusCode, readErr)
		}
		if test.status == 200 && (!bytes.Contains(body, []byte(`"complete":true`)) ||
			!bytes.Contains(body, []byte(`"exitCode":0`))) {
			t.Fatalf("scoped queued command must execute without terminal approval: %s", body)
		}
	}
	response, err := http.Post(config.AdapterURL+"/ingest", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("ingestion trigger without a serve loop must be unavailable: %d", response.StatusCode)
	}
}

func TestDashboardActionAdapterCoalescesIngestionRequests(t *testing.T) {
	trigger := make(chan struct{}, 1)
	filename, cleanup, err := startDashboardActionAdapter(t.Context(), []string{"octo/control"}, t.TempDir(), trigger)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		AdapterURL string `json:"adapterUrl"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	base := config.AdapterURL[:strings.LastIndex(config.AdapterURL, "/")]
	for _, target := range []string{config.AdapterURL + "/ingest", config.AdapterURL + "/ingest", base + "/ingest"} {
		response, err := http.Post(target, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		want := http.StatusAccepted
		if target == base+"/ingest" {
			want = http.StatusNotFound
		}
		if response.StatusCode != want {
			t.Fatalf("%s: status %d, want %d", target, response.StatusCode, want)
		}
	}
	if len(trigger) != 1 {
		t.Fatalf("ingestion requests must coalesce into one pending pass: %d", len(trigger))
	}
}
