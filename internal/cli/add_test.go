package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const testCampaignDeclaration = `{"campaign":"demo","orchestrator":"demo","workers":{"second":"demo-second","first":"demo-first"}}`
const testCampaignSHA = "1234567890abcdef1234567890abcdef12345678"

func campaignRecord(t *testing.T, directory, revision string) {
	t.Helper()
	writeTestFile(t, filepath.Join(".github", "aw", directory, "demo.json"),
		`{"package":"githubnext/gh-cao/demo","resolvedCommit":"`+revision+`"}`)
}

func campaignGH(t *testing.T, archive string, calls *[][]string) ghExecutor {
	t.Helper()
	return func(_ context.Context, args ...string) (string, string, error) {
		*calls = append(*calls, args)
		switch {
		case reflect.DeepEqual(args, []string{"aw", "version"}):
			return "", "gh-aw v0.92.4\n", nil
		case args[0] == "aw" && args[1] == "add":
			campaignRecord(t, "packages", testCampaignSHA)
			return "", "", nil
		case reflect.DeepEqual(args, []string{"api", "--hostname", "github.com", "/repos/" + catalogRepository + "/tarball/" + testCampaignSHA}):
			return archive, "", nil
		case reflect.DeepEqual(args, []string{"repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner"}):
			return "acme/control\n", "", nil
		default:
			t.Fatalf("unexpected gh call: %q", args)
			return "", "", nil
		}
	}
}

func TestAddCampaignNative(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("CAO_RUNTIME_ROOT", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("PATH", "")
	original := `{"version":1,"gh-aw-version":"v0.92.4","large":9007199254740993,
 "control-plane":{"scope":{"allowed-owners":["acme"],"allowed-repositories":["acme/control"]},
 "activity":{"repositories":["acme/control"]},"defaults":{"mode":"live"},
 "campaigns":{"other":{"mode":"review"},"demo":{"enabled":false,"mode":"review",
 "targets":{"acme/control":{"mode":"review"}},"workers":{
 "second":{"workflow":"old","enabled":false,"max-mode":"review","discard":"old"},
 "first":{"workflow":"old","enabled":true,"max-mode":"live"},
 "stale":{"workflow":"old-stale"}}}}}}`
	writeTestFile(t, policyPath, original)
	writeTestFile(t, "demo/stale.txt", "must disappear")
	writeTestFile(t, "other/preserve.txt", "must survive")
	writeTestFile(t, "activity/preserve.txt", "runtime must survive")
	archive := campaignArchive(t, map[string]string{
		"catalog/demo/cao.json":                                testCampaignDeclaration,
		"catalog/demo/run.sh":                                  "#!/bin/sh\nexit 0\n",
		"catalog/demo/intelligence.json":                       `{"campaign":"demo"}`,
		"catalog/demo/squad/.github/agents/squad.agent.md":     "native coordinator",
		"catalog/demo/squad/.squad/agents/engineer/charter.md": "specialist",
		"catalog/activity/preserve.txt":                        "must not copy",
	})
	var calls [][]string
	root, stdout, stderr := testCommand(t, campaignGH(t, archive, &calls), nil)
	args := []string{"add", "githubnext/gh-cao/demo@feature/config", "--engine", "copilot", "--unknown-future-flag"}
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{
		append([]string{"aw"}, args...),
		{"api", "--hostname", "github.com", "/repos/" + catalogRepository + "/tarball/" + testCampaignSHA},
	}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("gh calls = %q, want %q", calls, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{
		"command": "add", "campaign": "demo", "orchestrator": "demo",
		"workers": []any{"second", "first"}, "policy": policyPath,
	}; !reflect.DeepEqual(result, want) {
		t.Fatalf("result = %v, want %v", result, want)
	}
	policy, err := readPolicy()
	if err != nil {
		t.Fatal(err)
	}
	control := policy["control-plane"].(map[string]any)
	demo := control["campaigns"].(map[string]any)["demo"].(map[string]any)
	workers := demo["workers"].(map[string]any)
	if want := map[string]any{
		"second": map[string]any{"workflow": "demo-second", "enabled": false, "max-mode": "review"},
		"first":  map[string]any{"workflow": "demo-first", "enabled": true, "max-mode": "live"},
	}; !reflect.DeepEqual(workers, want) {
		t.Fatalf("worker policy = %v, want %v", workers, want)
	}
	var before map[string]any
	decoder := json.NewDecoder(strings.NewReader(original))
	decoder.UseNumber()
	if err := decoder.Decode(&before); err != nil {
		t.Fatal(err)
	}
	before["control-plane"].(map[string]any)["campaigns"].(map[string]any)["demo"].(map[string]any)["workers"] = workers
	if !reflect.DeepEqual(policy, before) {
		t.Fatalf("policy outside declared workers changed: %v", policy)
	}
	for file, want := range map[string]string{
		"other/preserve.txt":                           "must survive",
		"activity/preserve.txt":                        "runtime must survive",
		".github/cao/intelligence/demo.json":           `{"campaign":"demo"}`,
		"demo/run.sh":                                  "#!/bin/sh\nexit 0\n",
		"demo/squad/.github/agents/squad.agent.md":     "native coordinator",
		"demo/squad/.squad/agents/engineer/charter.md": "specialist",
	} {
		if content, err := os.ReadFile(file); err != nil || string(content) != want {
			t.Fatalf("%s = %q, %v", file, content, err)
		}
	}
	if _, err := os.Stat("demo/stale.txt"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale resource survived: %v", err)
	}
	if matches, _ := filepath.Glob(".cao-campaign-*"); len(matches) != 0 {
		t.Fatalf("staging files leaked: %v", matches)
	}
}

func TestAddCampaignCreatesBoundedPolicy(t *testing.T) {
	t.Chdir(t.TempDir())
	archive := campaignArchive(t, map[string]string{"catalog/demo/cao.json": testCampaignDeclaration})
	var calls [][]string
	root, _, _ := testCommand(t, campaignGH(t, archive, &calls), nil)
	root.SetArgs([]string{"add", "githubnext/gh-cao/demo@v1.2.3"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	policy, err := readPolicy()
	if err != nil {
		t.Fatal(err)
	}
	control := policy["control-plane"].(map[string]any)
	if want := map[string]any{
		"allowed-owners": []any{"acme"}, "allowed-repositories": []any{"acme/control"},
	}; !reflect.DeepEqual(control["scope"], want) {
		t.Fatalf("scope = %v", control["scope"])
	}
	demo := control["campaigns"].(map[string]any)["demo"].(map[string]any)
	if _, exists := demo["mode"]; exists {
		t.Fatal("add must not promote a campaign")
	}
	if _, exists := demo["enabled"]; exists {
		t.Fatal("add must not enable a campaign")
	}
	if len(calls) != 4 || policy["gh-aw-version"] != "v0.92.4" {
		t.Fatalf("missing initialization contract: %q, %v", calls, policy)
	}
}

func TestAddLocalCampaignCopiesOnlyTrackedFiles(t *testing.T) {
	catalog := t.TempDir()
	writeTestFile(t, filepath.Join(catalog, "demo", "cao.json"), testCampaignDeclaration)
	writeTestFile(t, filepath.Join(catalog, "demo", "tracked.mjs"), "export const tracked = true;\n")
	writeTestFile(t, filepath.Join(catalog, "demo", "squad", ".github", "agents", "squad.agent.md"), "native coordinator")
	writeTestFile(t, filepath.Join(catalog, "demo", "squad", ".squad", "agents", "engineer", "charter.md"), "specialist")
	for _, args := range [][]string{{"init", "--quiet", catalog}, {"-C", catalog, "add", "demo"}} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v: %s", err, output)
		}
	}
	writeTestFile(t, filepath.Join(catalog, "demo", "untracked.txt"), "do not copy")
	t.Chdir(t.TempDir())
	writeTestFile(t, policyPath, `{"version":1}`)
	writeTestFile(t, ".github/cao/intelligence/demo.json", "stale metadata")
	var calls [][]string
	root, _, _ := testCommand(t, campaignGH(t, "", &calls), nil)
	current, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	source, err := filepath.Rel(current, filepath.Join(catalog, "demo"))
	if err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"add", source, "--force"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("local installation downloaded code: %q", calls)
	}
	for _, file := range []string{"demo/untracked.txt", ".github/cao/intelligence/demo.json"} {
		if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected file %s: %v", file, err)
		}
	}
	if content, err := os.ReadFile("demo/tracked.mjs"); err != nil || !strings.Contains(string(content), "tracked = true") {
		t.Fatalf("tracked source not copied: %q %v", content, err)
	}
	for file, want := range map[string]string{
		"demo/squad/.github/agents/squad.agent.md":     "native coordinator",
		"demo/squad/.squad/agents/engineer/charter.md": "specialist",
	} {
		if content, err := os.ReadFile(file); err != nil || string(content) != want {
			t.Fatalf("hidden team asset %s = %q, %v", file, content, err)
		}
	}
}

func TestAddCampaignFailuresPreservePolicy(t *testing.T) {
	for _, tt := range []struct {
		name, policy, archive, want string
		installFails                bool
	}{
		{"install fails", `{"version":1}`, "", "installation failed", true},
		{"bad archive", `{"version":1}`, "invalid", "invalid CAO archive", false},
		{"bad policy", `{"version":2}`, "", "must declare version 1", false},
		{"invalid declaration", `{"version":1}`, `{"campaign":"other","orchestrator":"demo","workers":{"one":"demo-one"}}`, "expected demo", false},
		{"unknown declaration key", `{"version":1}`, `{"campaign":"demo","orchestrator":"demo","workers":{"one":"demo-one"},"extra":true}`, "unknown field", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeTestFile(t, policyPath, tt.policy)
			writeTestFile(t, "demo/preserved.txt", "original")
			archive := tt.archive
			if archive == "" {
				archive = testCampaignDeclaration
			}
			if strings.HasPrefix(archive, "{") {
				archive = campaignArchive(t, map[string]string{"catalog/demo/cao.json": archive})
			}
			var calls [][]string
			gh := campaignGH(t, archive, &calls)
			if tt.installFails {
				gh = func(context.Context, ...string) (string, string, error) {
					return "", "installation failed", errors.New("exit 1")
				}
			}
			root, stdout, _ := testCommand(t, gh, nil)
			root.SetArgs([]string{"add", "githubnext/gh-cao/demo"})
			if err := root.Execute(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %s", err, tt.want)
			}
			if content, err := os.ReadFile(policyPath); err != nil || string(content) != tt.policy || stdout.Len() != 0 {
				t.Fatalf("policy changed or success reported: %q %v %s", content, err, stdout)
			}
			if tt.name != "bad policy" {
				if content, err := os.ReadFile("demo/preserved.txt"); err != nil || string(content) != "original" {
					t.Fatalf("invalid materialization replaced campaign: %q %v", content, err)
				}
			}
		})
	}
}

func TestAddRejectsRootAndInvalidSpecsBeforeExecution(t *testing.T) {
	for _, spec := range []string{"githubnext/gh-cao", "githubnext/gh-cao@v1", "../Bad_Name", "--force", "githubnext/gh-cao/../"} {
		t.Run(spec, func(t *testing.T) {
			root, _, _ := testCommand(t, nil, nil)
			root.SetArgs([]string{"add", spec})
			if err := root.Execute(); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}
