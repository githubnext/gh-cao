package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestDashboardActionScopeAndPublication(t *testing.T) {
	scope := []string{"github/gh-aw"}
	for _, args := range [][]string{
		{"upgrade", "--repo", "github/gh-aw", "--create-pull-request"},
		{"upgrade", "--repo", "github/gh-aw", "--create-pull-request", "--pre-releases"},
		{"update", "https://github.com/octo/source/workflow", "--repo", "github/gh-aw", "--create-pull-request"},
		{"update", ".github/workflows/review.md", "--repo", "github/gh-aw", "--create-pull-request"},
		{"update", "review.v2.md", "--repo", "github/gh-aw", "--create-pull-request"},
		{"disable", "review.v2", "--repo", "github/gh-aw"},
		{"update", "--repo", "github/gh-aw", "--create-pull-request"},
		{"disable", "review", "--repo", "github/gh-aw"},
		{"disable", "--repo", "github/gh-aw"},
		{"repos", "add", "octo/new-service"},
		{"repos", "add", "octo/service-*"},
	} {
		if err := dashboardActionArgs(args, scope); err != nil {
			t.Fatalf("valid maintenance rejected: %v", err)
		}
	}
	for _, args := range [][]string{
		{"upgrade", "--repo", "octo/other", "--create-pull-request"},
		{"upgrade", "--repo", "github/gh-aw"},
		{"upgrade", "--repo", "github/gh-aw", "--repo", "octo/other", "--create-pull-request"},
		{"upgrade", "--org", "github", "--create-pull-request"},
		{"upgrade", "--repo", "github/gh-aw", "--create-pull-request", "--auto-merge"},
		{"update", "review.md", "--repo", "github/gh-aw", "--pr-message", "Reviewed update", "--create-pull-request"},
		{"update", "https://evil.example/source", "--repo", "github/gh-aw", "--create-pull-request"},
		{"update", "https://github.com/octo/source/../other", "--repo", "github/gh-aw", "--create-pull-request"},
		{"update", "https://github.com/octo/source", "--repo", "github/gh-aw", "--create-pull-request", "--pre-releases"},
		{"workflow", "run", "agent.yml", "--repo", "github/gh-aw"},
		{"disable"},
		{"disable", "review* ", "--repo", "github/gh-aw"},
		{"disable", "review..v2", "--repo", "github/gh-aw"},
		{"disable", "review", "review-more", "--repo", "github/gh-aw"},
		{"disable", "review", "--repo", "octo/other"},
		{"disable", "review", "--repo", "github/gh-aw", "--create-pull-request"},
		{"update", "../review.md", "--repo", "github/gh-aw", "--create-pull-request"},
		{"repos", "add"},
		{"repos", "add", "octo/a", "octo/b"},
		{"repos", "add", "octo/../other"},
		{"repos", "add", "--repo", "octo/api"},
		{"repos", "add", "octo"},
	} {
		if dashboardActionArgs(args, scope) == nil {
			t.Fatalf("unscoped or unsupported maintenance accepted: %v", args)
		}
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

func TestDashboardActionAdapterRequiresApprovalAndPrivateCapability(t *testing.T) {
	approvals := 0
	filename, cleanup, err := startDashboardActionAdapterWithApproval(t.Context(), []string{"github/gh-aw"}, t.TempDir(),
		func(context.Context, []string) (bool, error) { approvals++; return false, nil }, nil)
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
		{`["upgrade","--repo","github/gh-aw","--create-pull-request"]`, 200},
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
			!bytes.Contains(body, []byte(`"exitCode":1`))) {
			t.Fatalf("declined approval must return a failed execution receipt: %s", body)
		}
	}
	if approvals != 1 {
		t.Fatalf("out-of-scope command reached terminal approval: %d", approvals)
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
	filename, cleanup, err := startDashboardActionAdapterWithApproval(t.Context(), []string{"github/gh-aw"}, t.TempDir(),
		func(context.Context, []string) (bool, error) { return false, nil }, trigger)
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
