package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInitNativePolicy(t *testing.T) {
	t.Chdir(t.TempDir())
	var calls [][]string
	root, stdout, _ := testCommand(t, func(_ context.Context, args ...string) (string, string, error) {
		calls = append(calls, args)
		switch args[0] {
		case "aw":
			return "", "gh-aw version v0.92.0\n", nil
		case "repo":
			return "Acme-Org/ops.tools\n", "", nil
		default:
			t.Fatalf("unexpected command: %q", args)
			return "", "", nil
		}
	}, nil)
	root.SetArgs([]string{"init"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	wantCalls := [][]string{{"aw", "version"}, {"repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner"}}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("gh calls = %q", calls)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["command"] != "init" || result["gh-aw-version"] != "v0.92.0" || result["policy"] != policyPath {
		t.Fatalf("result = %v", result)
	}
	policy, err := readPolicy()
	if err != nil {
		t.Fatal(err)
	}
	control := policy["control-plane"].(map[string]any)
	scope := control["scope"].(map[string]any)
	if !reflect.DeepEqual(scope, map[string]any{
		"allowed-owners": []any{"Acme-Org"}, "allowed-repositories": []any{"Acme-Org/ops.tools"},
	}) {
		t.Fatalf("unexpected authority: %v", scope)
	}
	if len(control["campaigns"].(map[string]any)) != 0 {
		t.Fatal("init must not install or enable campaigns")
	}
	registries := control["marketplace"].(map[string]any)["registries"].([]any)
	if len(registries) != 2 || policy["$schema"] != schemaURL {
		t.Fatalf("missing default marketplace or schema: %v", policy)
	}
}

func TestInitNeverOverwrites(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestFile(t, policyPath, "operator-owned bytes\n")
	root, stdout, _ := testCommand(t, nil, nil)
	root.SetArgs([]string{"init"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v, want existing policy error", err)
	}
	content, err := os.ReadFile(policyPath)
	if err != nil || string(content) != "operator-owned bytes\n" || stdout.Len() != 0 {
		t.Fatalf("existing policy or output changed: %q, %v", content, err)
	}
	if err := writePolicy(map[string]any{"version": 1}, true); err == nil {
		t.Fatal("atomic creation must not overwrite a concurrently created policy")
	}
}

func TestInitFailuresWriteNothing(t *testing.T) {
	tests := []struct {
		name, version, repository string
		fail                      bool
	}{
		{"missing version", "", "owner/repo", false},
		{"missing repository", "v1.2.3", "", false},
		{"broad repository", "v1.2.3", "owner/*", false},
		{"invalid repository", "v1.2.3", "owner/repo/extra", false},
		{"GitHub failure", "v1.2.3", "owner/repo", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			root, stdout, _ := testCommand(t, func(_ context.Context, args ...string) (string, string, error) {
				if tt.fail {
					return "", "command unavailable", errors.New("failed")
				}
				if args[0] == "aw" {
					return tt.version, "", nil
				}
				return tt.repository, "", nil
			}, nil)
			root.SetArgs([]string{"init"})
			if err := root.Execute(); err == nil {
				t.Fatal("expected failure")
			}
			if _, err := os.Stat(policyPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("policy created on failure: %v", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("failed initialization reported success: %s", stdout)
			}
		})
	}
}

const testPolicy = `{
  "version": 1,
  "gh-aw-version": "v0.92.0",
  "large-exact-number": 9007199254740993,
  "control-plane": {
    "scope": {"allowed-owners": ["owner"], "allowed-repositories": ["owner/repo"]},
    "campaigns": {
      "one": {"mode": "live", "rollout-percent": 7, "workers": {"worker": {"enabled": false}}},
      "two": {"enabled": false, "mode": "live"}
    }
  }
}`

func TestModePreservesAuthorityAndSettings(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestFile(t, policyPath, testPolicy)
	root, stdout, _ := testCommand(t, nil, nil)
	root.SetArgs([]string{"campaigns", "mode", "preview", "two", "one", "two"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	got, err := readPolicy()
	if err != nil {
		t.Fatal(err)
	}
	if got["large-exact-number"] != json.Number("9007199254740993") {
		t.Fatal("policy integer precision was lost")
	}
	campaigns := got["control-plane"].(map[string]any)["campaigns"].(map[string]any)
	if campaigns["one"].(map[string]any)["mode"] != "review" || campaigns["two"].(map[string]any)["mode"] != "review" {
		t.Fatalf("preview did not map to review: %v", campaigns)
	}
	one := campaigns["one"].(map[string]any)
	if one["rollout-percent"] != json.Number("7") || one["workers"].(map[string]any)["worker"].(map[string]any)["enabled"] != false {
		t.Fatal("campaign settings changed")
	}
	content, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(string(content), `"version"`) > strings.Index(string(content), `"control-plane"`) ||
		strings.Index(string(content), `"scope"`) > strings.Index(string(content), `"campaigns"`) ||
		strings.Index(string(content), `"mode"`) > strings.Index(string(content), `"rollout-percent"`) {
		t.Fatal("mode change reordered operator-owned policy keys")
	}
	var result struct {
		Mode      string   `json:"mode"`
		Campaigns []string `json:"campaigns"`
	}

	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Mode != "preview" || !reflect.DeepEqual(result.Campaigns, []string{"two", "one"}) {
		t.Fatalf("mode output changed: %v", result)
	}
}

func TestModeAppendsMissingModeWithoutChangingOtherFields(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestFile(t, policyPath, `{"version":1e0,"control-plane":{"campaigns":{"one":{"z":1,"settings":{"last":null,"first":"<>&"},"items":[{"z":2,"a":1}],"a":2}}}}`)
	root, _, _ := testCommand(t, nil, nil)
	root.SetArgs([]string{"campaigns", "mode", "preview", "one"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if strings.Index(text, `"a": 2`) > strings.Index(text, `"mode": "review"`) ||
		strings.Index(text, `"last"`) > strings.Index(text, `"first"`) ||
		!strings.Contains(text, `"version": 1e0`) || !strings.Contains(text, `"<>&"`) {
		t.Fatalf("policy formatting or data changed: %s", text)
	}
}

func TestModeValidatesAllCampaignsBeforeWriting(t *testing.T) {
	for _, names := range [][]string{{"one", "missing"}, {"one", "../two"}} {
		t.Run(strings.Join(names, " "), func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeTestFile(t, policyPath, testPolicy)
			root, _, _ := testCommand(t, nil, nil)
			root.SetArgs(append([]string{"campaigns", "mode", "preview"}, names...))
			if err := root.Execute(); err == nil {
				t.Fatal("expected invalid campaign error")
			}
			content, _ := os.ReadFile(policyPath)
			if string(content) != testPolicy {
				t.Fatal("policy changed before all names were validated")
			}
		})
	}
}

func TestModeRejectsMalformedPolicy(t *testing.T) {
	for _, content := range []string{
		"null", "[]", "{", `{"version":2}`, `{"version":"1"}`, `{"version":1} {}`,
		`{"version":1,"control-plane":null}`, `{"version":1,"control-plane":{"campaigns":[]}}`,
		`{"version":1,"control-plane":{"campaigns":{"one":null}}}`,
	} {
		t.Run(content, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeTestFile(t, policyPath, content)
			root, _, _ := testCommand(t, nil, nil)
			root.SetArgs([]string{"campaigns", "mode", "preview", "one"})
			if err := root.Execute(); err == nil {
				t.Fatal("expected invalid policy error")
			}
			got, _ := os.ReadFile(policyPath)
			if string(got) != content {
				t.Fatal("invalid policy was rewritten")
			}
		})
	}
}

func TestModeLiveAuthentication(t *testing.T) {
	for _, profile := range []string{"app", "pat", "none", "unavailable"} {
		t.Run(profile, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("CI", "true")
			writeTestFile(t, policyPath, testPolicy)
			root, _, stderr := testCommand(t, func(_ context.Context, args ...string) (string, string, error) {
				if profile == "unavailable" {
					return "", "private response must not be logged", errors.New("forbidden")
				}
				if args[0] == "repo" {
					return "owner/repo", "", nil
				}
				endpoint := args[1]
				switch {
				case strings.Contains(endpoint, "variables?"):
					return "GH_AW_GITHUB_AUTH_MODE\nGH_AW_GITHUB_WRITE_APP_ID\nGH_AW_GITHUB_WRITE_PAT_REPOSITORIES", "", nil
				case strings.Contains(endpoint, "secrets?"):
					return "GH_AW_GITHUB_WRITE_APP_PRIVATE_KEY\nGH_AW_GITHUB_WRITE_PAT_OWNER", "", nil
				case strings.HasSuffix(endpoint, "GH_AW_GITHUB_AUTH_MODE"):
					return profile, "", nil
				case strings.HasSuffix(endpoint, "GH_AW_GITHUB_WRITE_APP_ID"):
					return "client-id", "", nil
				case strings.HasSuffix(endpoint, "GH_AW_GITHUB_WRITE_PAT_REPOSITORIES"):
					return `{"owner/repo":"GH_AW_GITHUB_WRITE_PAT_OWNER"}`, "", nil
				default:
					t.Fatalf("unexpected GitHub command: %q", args)
					return "", "", nil
				}
			}, nil)
			root.SetArgs([]string{"campaigns", "mode", "live", "one"})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			warning := strings.Contains(stderr.String(), "Live write authentication is not configured")
			if warning != (profile == "none") {
				t.Fatalf("unexpected authentication warning: %q", stderr)
			}
			policy, _ := readPolicy()
			campaign := policy["control-plane"].(map[string]any)["campaigns"].(map[string]any)["one"].(map[string]any)
			if campaign["mode"] != "live" {
				t.Fatal("live mode not written")
			}
		})
	}
}

func TestCampaignWorkflowOrderAndDeduplication(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestFile(t, "one/cao.json", `{"campaign":"one","orchestrator":"one","workers":{"z":"z-worker","a":"shared"}}`)
	writeTestFile(t, "two/cao.json", `{"campaign":"two","orchestrator":"two","workers":{"other":"shared"}}`)
	for _, action := range []string{"enable", "disable"} {
		t.Run(action, func(t *testing.T) {
			var calls [][]string
			root, stdout, _ := testCommand(t, func(_ context.Context, args ...string) (string, string, error) {
				calls = append(calls, args)
				return "", "", nil
			}, nil)
			root.SetArgs([]string{"campaigns", action, "one", "two", "one"})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			want := [][]string{
				{"workflow", action, "one.lock.yml"}, {"workflow", action, "z-worker.lock.yml"},
				{"workflow", action, "shared.lock.yml"}, {"workflow", action, "two.lock.yml"},
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("workflow order or deduplication changed: %q", calls)
			}
			if !strings.Contains(stdout.String(), `"workflows"`) {
				t.Fatal("missing workflow output")
			}
		})
	}
}

func TestCampaignDeclarationsFailBeforeWorkflowChanges(t *testing.T) {
	for _, declaration := range []string{
		`null`, `{}`, `{"campaign":"one","orchestrator":"one","workers":{}}`,
		`{"campaign":"other","orchestrator":"one","workers":{"worker":"worker"}}`,
		`{"campaign":"one","orchestrator":"../one","workers":{"worker":"worker"}}`,
		`{"campaign":"one","orchestrator":"one","workers":{"Bad":"worker"}}`,
		`{"campaign":"one","orchestrator":"one","workers":{"a":"shared","b":"shared"}}`,
		`{"campaign":"one","orchestrator":"one","workers":{"a":"first","a":"second"}}`,
		`{"campaign":"one","orchestrator":"one","workers":[]}`,
		`{"campaign":"one","orchestrator":"one","workers":{"worker":42}}`,
		`{"campaign":"one","orchestrator":"one","workers":{"worker":"worker"},"unknown":true}`,
	} {
		t.Run(declaration, func(t *testing.T) {
			t.Chdir(t.TempDir())
			writeTestFile(t, "one/cao.json", declaration)
			writeTestFile(t, "valid/cao.json", `{"campaign":"valid","orchestrator":"valid","workers":{"worker":"valid-worker"}}`)
			root, _, _ := testCommand(t, nil, nil)
			root.SetArgs([]string{"campaigns", "enable", "valid", "one"})
			if err := root.Execute(); err == nil {
				t.Fatal("expected declaration error before any workflow changes")
			}
		})
	}
}

func TestCampaignWorkflowFailureIsNotSuccess(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestFile(t, "one/cao.json", `{"campaign":"one","orchestrator":"one","workers":{"worker":"worker"}}`)
	calls := 0
	root, stdout, _ := testCommand(t, func(_ context.Context, args ...string) (string, string, error) {
		calls++
		return "", "workflow unavailable", errors.New("failed")
	}, nil)
	root.SetArgs([]string{"campaigns", "disable", "one"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "workflow unavailable") {
		t.Fatalf("expected actionable GitHub failure, got %v", err)
	}
	if calls != 1 || stdout.Len() != 0 {
		t.Fatalf("continued or reported success on workflow failure: calls=%d, stdout=%q", calls, stdout)
	}
}

func TestNativePolicyValidatesTokenBroker(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "cao.json"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := nativeDecodeJSON(content)
	if err != nil {
		t.Fatal(err)
	}
	if err := nativeValidatePolicy(value.(map[string]any)); err != nil {
		t.Fatalf("checked-in policy rejected: %v", err)
	}
	rule := map[string]any{
		"repository-id": "1", "owner-id": "2", "workflow-sha": strings.Repeat("a", 40),
		"workflow-ref": "octo/control/.github/workflows/agent.lock.yml@refs/heads/main",
	}
	broker := func(workflows ...any) map[string]any {
		policy, _ := nativeDecodeJSON(content)
		policy.(map[string]any)["control-plane"].(map[string]any)["token-broker"] = map[string]any{"copilot-requests": map[string]any{"workflows": workflows}}
		return policy.(map[string]any)
	}
	if err := nativeValidatePolicy(broker(rule)); err != nil {
		t.Fatalf("valid broker rejected: %v", err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"zero id": func(r map[string]any) { r["owner-id"] = "0" },
		"lock ref": func(r map[string]any) {
			r["workflow-ref"] = "octo/control/.github/workflows/agent.yml@refs/heads/x.lock"
		},
		"traversal":   func(r map[string]any) { r["workflow-ref"] = "octo/control/.github/workflows/agent.yml@refs/heads/a..b" },
		"short sha":   func(r map[string]any) { r["workflow-sha"] = "abc" },
		"unknown key": func(r map[string]any) { r["extra"] = true },
	} {
		invalid := map[string]any{}
		for key, value := range rule {
			invalid[key] = value
		}
		mutate(invalid)
		if nativeValidatePolicy(broker(invalid)) == nil {
			t.Fatalf("%s broker accepted", name)
		}
	}
	if nativeValidatePolicy(broker(rule, rule)) == nil {
		t.Fatal("duplicate broker workflow accepted")
	}
}

func TestNativePolicyBoundsMultiTargetWorkerOptIn(t *testing.T) {
	policy := func(limit any) map[string]any {
		worker := map[string]any{"workflow": "demo-worker"}
		if limit != nil {
			worker["max-targets-per-run"] = limit
		}
		value, err := nativeDecodeJSON([]byte(`{"version":1,"gh-aw-version":"v1.2.3","control-plane":{"campaigns":{"demo":{"workers":{}}}}}`))
		if err != nil {
			t.Fatal(err)
		}
		value.(map[string]any)["control-plane"].(map[string]any)["campaigns"].(map[string]any)["demo"].(map[string]any)["workers"] = map[string]any{"worker": worker}
		return value.(map[string]any)
	}
	for _, valid := range []any{nil, 2, 20} {
		if err := nativeValidatePolicy(policy(valid)); err != nil {
			t.Fatalf("max-targets-per-run %v rejected: %v", valid, err)
		}
	}
	for _, invalid := range []any{0, 1, 21, 2.5, "3", true} {
		if nativeValidatePolicy(policy(invalid)) == nil {
			t.Fatalf("max-targets-per-run %v accepted", invalid)
		}
	}
}
