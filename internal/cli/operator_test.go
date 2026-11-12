package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func testCommand(t *testing.T, gh ghExecutor, runtime runtimeExecutor) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	if gh == nil {
		gh = func(context.Context, ...string) (string, string, error) {
			t.Fatal("unexpected GitHub CLI execution")
			return "", "", nil
		}
	}
	if runtime == nil {
		runtime = func(*cobra.Command, []string) error {
			t.Fatal("unexpected runtime execution")
			return nil
		}
	}
	root := newCommand("test", func(context.Context) (string, error) {
		t.Fatal("unexpected local diagnostic")
		return "", nil
	}, gh, runtime)
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(stderr)
	return root, stdout, stderr
}

func TestOperatorCommandInventory(t *testing.T) {
	root, _, _ := testCommand(t, nil, nil)
	var names []string
	for _, command := range root.Commands() {
		names = append(names, command.Name())
	}
	slices.Sort(names)
	want := []string{
		"__dashboard_aw", "add", "admission", "api-cost", "campaigns", "computation", "dashboard", "deploy", "doctor", "download", "evidence", "gh", "init",
		"mcp", "pages", "prompt", "queries", "query", "query-info",
		"repos", "setup", "update", "upgrade-gh-aw", "value", "version",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("command inventory = %v, want %v", names, want)
	}
}

func TestCampaignCommandInventory(t *testing.T) {
	root, _, _ := testCommand(t, nil, nil)
	campaigns, _, err := root.Find([]string{"campaigns"})
	if err != nil || campaigns.Name() != "campaigns" {
		t.Fatalf("campaigns command not found: %v", err)
	}
	var names []string
	for _, command := range campaigns.Commands() {
		names = append(names, command.Name())
	}
	slices.Sort(names)
	if want := []string{"disable", "enable", "mode"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("campaign command inventory = %v, want %v", names, want)
	}
}

func TestCampaignHelpExcludesInstallation(t *testing.T) {
	root, stdout, _ := testCommand(t, nil, nil)
	root.SetArgs([]string{"campaigns", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "\n  add ") {
		t.Fatalf("campaign installation must use the root add command: %s", stdout)
	}
}

func TestAddHelpCoversWorkflowsAndCampaignsOffline(t *testing.T) {
	root, stdout, _ := testCommand(t, nil, nil)
	root.SetArgs([]string{"add", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"add WORKFLOW_OR_CAMPAIGN", "gh cao add daily-repo-status", "gh cao add githubnext/gh-cao/dependabot", "gh cao add ./dependabot"} {
		if !strings.Contains(stdout.String(), text) {
			t.Fatalf("add help is missing %q: %s", text, stdout)
		}
	}
}

func TestQuickstartIsNotAnExtensionCommand(t *testing.T) {
	for _, args := range [][]string{
		{"quickstart"}, {"quickstart", "--target", "owner/repo"},
		{"quickstart", "--step", "run"}, {"quickstart", "--step", "collect"},
		{"quickstart", "--step", "dashboard"}, {"quickstart", "--wait", "--target", "owner/repo"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root, stdout, stderr := testCommand(t, nil, nil)
			root.SetArgs(args)
			if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "unknown") {
				t.Fatalf("expected unknown quickstart command, got %v", err)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("unexpected output: stdout=%q stderr=%q", stdout, stderr)
			}
		})
	}
	root, stdout, _ := testCommand(t, nil, nil)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil || strings.Contains(stdout.String(), "quickstart") {
		t.Fatalf("removed quickstart command advertised in help: %v\n%s", err, stdout)
	}
}

func TestActionsValidationIsNotAnOperatorCommand(t *testing.T) {
	for _, args := range [][]string{
		{"validate"}, {"validate", "--json"}, {"validate", "--strict-warnings"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root, stdout, stderr := testCommand(t, nil, nil)
			root.SetArgs(args)
			if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "unknown") {
				t.Fatalf("expected unknown validation command, got %v", err)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("unexpected output: stdout=%q stderr=%q", stdout, stderr)
			}
		})
	}
	root, stdout, _ := testCommand(t, nil, nil)
	root.SetArgs([]string{"validate", "--help"})
	if err := root.Execute(); err != nil || strings.Contains(stdout.String(), "gh cao validate") {
		t.Fatalf("removed validation command has help: %v\n%s", err, stdout)
	}
}

func TestCampaignControlsAreNotRootCommands(t *testing.T) {
	for _, args := range [][]string{
		{"mode", "preview", "one"}, {"enable", "one"}, {"disable", "one"},
	} {
		t.Run(args[0], func(t *testing.T) {
			root, stdout, stderr := testCommand(t, nil, nil)
			root.SetArgs(args)
			if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "unknown command") {
				t.Fatalf("expected unknown root command, got %v", err)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("unexpected output: stdout=%q stderr=%q", stdout, stderr)
			}
		})
	}
}

func TestAllOperatorHelpIsOffline(t *testing.T) {
	root, _, _ := testCommand(t, nil, nil)
	var paths [][]string
	var walk func(*cobra.Command, []string)
	walk = func(parent *cobra.Command, path []string) {
		for _, cmd := range parent.Commands() {
			next := append(slices.Clone(path), cmd.Name())
			paths = append(paths, next)
			walk(cmd, next)
		}
	}
	walk(root, nil)
	for _, path := range paths {
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			root, stdout, stderr := testCommand(t, nil, nil)
			root.SetArgs(append(slices.Clone(path), "--help"))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stdout.String(), "gh cao "+strings.Join(path, " ")) {
				t.Fatalf("missing command-specific help: %s", stdout)
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %s", stderr)
			}
		})
	}
}

func TestOperatorRuntimeArguments(t *testing.T) {
	tests := []struct {
		args, want []string
	}{
		{[]string{"setup", "--json"}, []string{"setup", "--json"}},
		{[]string{"setup", "--json=false"}, []string{"setup"}},
		{[]string{"download", "--url=https://example.test/cao/payload-hashes.json", "--output", "space dir"}, []string{"download", "--output", "space dir", "--url", "https://example.test/cao/payload-hashes.json"}},
		{[]string{"pages", "--json", "overview"}, []string{"pages", "overview", "--json"}},
		{[]string{"queries", "--dashboard", "custom.json"}, []string{"queries", "--dashboard", "custom.json"}},
		{[]string{"query-info", "cost-by-campaign", "--json"}, []string{"query-info", "cost-by-campaign", "--json"}},
		{[]string{"prompt", "campaign-runs", "--param", "campaign=one,two", "--param", "label=a=b"}, []string{"prompt", "campaign-runs", "--param", "campaign=one,two", "--param", "label=a=b"}},
		{[]string{"query", "--database", "local.sqlite", "--stdin"}, []string{"query", "--database", "local.sqlite", "--stdin"}},
		{[]string{"query", "--collection", "runs", "--where", "status=failure", "--where", "workflowId=a,b"}, []string{"query", "--collection", "runs", "--where", "status=failure", "--where", "workflowId=a,b"}},
		{[]string{"gh", "runs", "-R", "owner/repo", "-w", "activity", "-s", "failure", "-L", "5"}, []string{"gh", "runs", "--limit", "5", "--repo", "owner/repo", "--status", "failure", "--workflow", "activity"}},
		{[]string{"gh", "prs", "--since", "2026-09-01"}, []string{"gh", "prs", "--since", "2026-09-01"}},
		{[]string{"computation", "runtime-health", "--campaign", "example", "--diagnose"}, []string{"computation", "runtime-health", "--campaign", "example", "--diagnose"}},
		{[]string{"computation", "intelligence", "--previous", "prior.json", "--feedback", "feedback.json"}, []string{"computation", "intelligence", "--feedback", "feedback.json", "--previous", "prior.json"}},
		{[]string{"mcp", "--port", "9010"}, []string{"mcp", "--port", "9010"}},
		{[]string{"setup", "auth", "token", "--repo", "owner/repo", "--write-repository", "a/one", "--write-repository", "b/two", "--dry-run"}, []string{"setup-auth", "token", "--dry-run", "--repo", "owner/repo", "--write-repository", "a/one", "--write-repository", "b/two"}},
		{[]string{"setup", "auth", "token", "--expires-in", "366", "--keep-existing", "--no-open", "--dry-run=false"}, []string{"setup-auth", "token", "--expires-in", "366", "--keep-existing", "--no-open"}},
		{[]string{"setup", "auth", "token", "--replace-existing"}, []string{"setup-auth", "token", "--replace-existing"}},
		{[]string{"setup", "auth", "enterprise-app", "--repo", "owner/repo", "--read-client-id", "one", "--write-client-id", "two"}, []string{"setup-auth", "enterprise-app", "--read-client-id", "one", "--repo", "owner/repo", "--write-client-id", "two"}},
		{[]string{"setup", "auth", "github-app", "--read-app-name", "cao-read", "--write-app-name", "cao-write", "--force", "--no-open", "--policy", "space dir/policy.json"}, []string{"setup-auth", "github-app", "--force", "--no-open", "--policy", "space dir/policy.json", "--read-app-name", "cao-read", "--write-app-name", "cao-write"}},
		{[]string{"upgrade-gh-aw", "v1.2.3"}, []string{"upgrade-gh-aw", "v1.2.3"}},
		{[]string{"update", "--campaign", "dependabot", "main", "--engine", "copilot"}, []string{"update", "--campaign", "dependabot", "main", "--engine", "copilot"}},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			called := false
			root, _, _ := testCommand(t, nil, func(_ *cobra.Command, got []string) error {
				called = true
				if !reflect.DeepEqual(got, tt.want) {
					t.Fatalf("runtime args = %q, want %q", got, tt.want)
				}
				return nil
			})
			root.SetArgs(tt.args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if !called {
				t.Fatal("runtime was not called")
			}
		})
	}
}

func TestOperatorInvalidInputDoesNotExecute(t *testing.T) {
	for _, args := range [][]string{
		{"setup", "extra"}, {"setup", "--unknown"}, {"query-info"}, {"prompt"},
		{"download", "--url", "one", "--url", "two"}, {"query", "--stdin", "--collection", "runs"},
		{"query", "--stdin", "--where", "x=y"}, {"query", "--stdin", "--limit", "5"},
		{"gh", "runs", "extra"}, {"gh", "issues", "--status", "open"},
		{"computation", "runtime-health", "--diagnose"}, {"computation", "intelligence", "--diagnose"},
		{"setup", "auth"}, {"setup", "auth", "unknown"}, {"setup", "unknown"},
		{"setup", "auth", "token", "--keep-existing", "--replace-existing"},
		{"setup", "auth", "token", "--keep-existing=false", "--replace-existing"},
		{"setup", "auth", "token", "--repo", "one/repo", "--repo", "two/repo"},
		{"setup", "auth", "token", "--expires-in", "1", "--expires-in", "2"},
		{"setup", "auth", "token", "--unknown"}, {"setup", "auth", "token", "extra"},
		{"setup", "auth", "token", "--json"}, {"setup", "auth", "github-app", "--expires-in", "30"},
		{"setup", "auth", "enterprise-app", "--force"},
		{"campaigns"}, {"campaigns", "unknown"}, {"campaigns", "mode", "preview"},
		{"campaigns", "enable"}, {"campaigns", "disable"},
		{"campaigns", "mode", "debug", "one"}, {"campaigns", "enable", "../one"},
		{"setup-auth"}, {"gh"}, {"computation"}, {"add"}, {"add", "--repository"},
		{"add", "--repository", "owner/*"}, {"add", "--repository", "owner/repo", "extra"},
		{"add", "githubnext/gh-cao"}, {"add", "githubnext/gh-cao", "--force"},
		{"campaigns", "add"}, {"campaigns", "add", "--repository", "owner/repo"},
		{"campaigns", "add", "githubnext/gh-cao/demo"}, {"campaigns", "add", "./demo"},
		{"repos"}, {"repos", "unknown"}, {"repos", "add"}, {"repos", "add", "owner/*"},
		{"repos", "add", "owner/repo", "extra"}, {"repos", "add", "--repository", "owner/repo"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root, stdout, stderr := testCommand(t, nil, nil)
			root.SetArgs(args)
			if err := root.Execute(); err == nil {
				t.Fatal("expected input error")
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("errors should be rendered once by main: stdout=%q stderr=%q", stdout, stderr)
			}
		})
	}
}

func TestPipelineCommandsAreNotMigrated(t *testing.T) {
	for _, name := range []string{
		"audit-jsonl", "cluster-problems", "compact-jsonl",
		"dashboard-complexity", "discover-workflows", "hash-payloads", "ingest",
		"ingest-jsonl", "issue-status", "operational-value", "prune-dashboard",
		"recycle-cache",
	} {
		t.Run(name, func(t *testing.T) {
			root, _, _ := testCommand(t, nil, nil)
			root.SetArgs([]string{name})
			if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "unknown command") {
				t.Fatalf("pipeline command must stay in Node: %v", err)
			}
		})
	}
}

func TestRuntimeIOContextAndErrors(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "marker")
	want := errors.New("runtime failure")
	root, stdout, stderr := testCommand(t, nil, func(cmd *cobra.Command, args []string) error {
		if cmd.Context().Value(key{}) != "marker" {
			t.Fatal("context was not propagated")
		}
		var request map[string]string
		if err := json.NewDecoder(cmd.InOrStdin()).Decode(&request); err != nil || request["name"] != "query" {
			t.Fatalf("stdin not propagated: %v", err)
		}
		cmd.Print("runtime stdout")
		cmd.PrintErr("runtime stderr")
		return want
	})
	root.SetIn(strings.NewReader(`{"name":"query"}`))
	root.SetArgs([]string{"query", "--stdin"})
	if err := root.ExecuteContext(ctx); !errors.Is(err, want) {
		t.Fatalf("error = %v, want runtime failure", err)
	}
	if stdout.String() != "runtime stdout" || stderr.String() != "runtime stderr" {
		t.Fatalf("streams changed: %q, %q", stdout, stderr)
	}
}

func TestOperatorHelpDoesNotRequireRuntime(t *testing.T) {
	t.Setenv("CAO_RUNTIME_ROOT", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("PATH", "")
	root := NewRootCommand("test")
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs([]string{"setup", "auth", "token", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "--keep-existing") {
		t.Fatal("missing authentication help")
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
