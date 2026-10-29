package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func testSetupOptions(t *testing.T, ctx context.Context, root, catalog, binary string, runtimeFiles []string) {
	t.Helper()
	for _, method := range []string{"token", "github-app", "enterprise-app"} {
		t.Run("auth "+method+" dry-run", func(t *testing.T) {
			f := newSetupFixture(t, ctx, root, catalog, binary, runtimeFiles)
			f.control()["scope"].(map[string]any)["allowed-repositories"] = []string{"platform/control", "platform/alpha"}
			policy, err := json.Marshal(f.policy)
			if err != nil {
				t.Fatal(err)
			}
			setupWrite(t, f.policyPath(), policy)
			before, tree := f.state(t), setupCheckoutState(t, f.checkout)
			args := []string{"auth", method, "--repo", "platform/control", "--policy", f.policyPath(),
				"--write-repository", "platform/control", "--dry-run"}
			if method != "token" {
				args = append(args, "--read-client-id", "simulator-read-app", "--write-client-id", "simulator-write-app")
			} else {
				args = append(args, "--expires-in", "7", "--no-open")
			}
			result := setupOptionRun(t, ctx, f, args...)
			if result["repo"] != "platform/control" {
				t.Fatalf("unexpected credential plan: %#v", result)
			}
			switch method {
			case "token":
				if result["command"] != "setup-auth" || result["profile"] != "fine-grained-token" {
					t.Fatalf("unexpected token profile: %#v", result)
				}
				setupAssertJSON(t, result["repositories"], map[string]any{
					"read": map[string]string{
						"platform/control": "GH_AW_GITHUB_READ_PAT_PLATFORM",
						"platform/alpha":   "GH_AW_GITHUB_READ_PAT_PLATFORM",
					},
					"write": map[string]string{"platform/control": "GH_AW_GITHUB_WRITE_PAT_PLATFORM"},
				})
				setupAssertJSON(t, result["secrets"], []map[string]string{
					{"owner": "platform", "role": "read", "secret": "GH_AW_GITHUB_READ_PAT_PLATFORM"},   // #nosec G101 -- Actions secret name, not a credential value.
					{"owner": "platform", "role": "write", "secret": "GH_AW_GITHUB_WRITE_PAT_PLATFORM"}, // #nosec G101 -- Actions secret name, not a credential value.
				})
			case "github-app":
				setupAssertJSON(t, result["installationTargets"], map[string]any{
					"read":  []map[string]any{{"owner": "platform", "repositories": []string{"alpha", "control"}}},
					"write": []map[string]any{{"owner": "platform", "repositories": []string{"control"}}},
				})
				apps, ok := result["apps"].([]any)
				if !ok || len(apps) != 2 {
					t.Fatalf("expected separate read/write App plans: %#v", result)
				}
				for i, role := range []string{"read", "write"} {
					app := apps[i].(map[string]any)
					prefix := "GH_AW_GITHUB_" + strings.ToUpper(role) + "_APP"
					if app["role"] != role || app["clientId"] != "simulator-"+role+"-app" ||
						app["variable"] != prefix+"_ID" || app["secret"] != prefix+"_PRIVATE_KEY" {
						t.Fatalf("unexpected App credential plan: %#v", app)
					}
					wantPermission := "read"
					if role == "write" {
						wantPermission = "write"
					}
					if app["permissions"].(map[string]any)["contents"] != wantPermission {
						t.Fatalf("incorrect App permissions for %s: %#v", role, app)
					}
				}
			case "enterprise-app":
				if result["command"] != "setup-auth" || result["profile"] != method {
					t.Fatalf("unexpected enterprise profile: %#v", result)
				}
				setupAssertJSON(t, result["repositories"], map[string]any{
					"read": []string{"platform/control", "platform/alpha"}, "write": []string{"platform/control"},
				})
				setupAssertJSON(t, result["credentials"], []map[string]string{
					{"role": "read", "clientId": "simulator-read-app", "variable": "GH_AW_GITHUB_READ_APP_ID", "secret": "GH_AW_GITHUB_READ_APP_PRIVATE_KEY"},     // #nosec G101 -- Actions secret name and inert fixture App ID.
					{"role": "write", "clientId": "simulator-write-app", "variable": "GH_AW_GITHUB_WRITE_APP_ID", "secret": "GH_AW_GITHUB_WRITE_APP_PRIVATE_KEY"}, // #nosec G101 -- Actions secret name and inert fixture App ID.
				})
			}
			if !bytes.Equal(policy, setupRead(t, f.policyPath())) || !bytes.Equal(before, f.state(t)) ||
				!reflect.DeepEqual(tree, setupCheckoutState(t, f.checkout)) {
				t.Fatal("credential dry-run changed local files or remote state")
			}
			f.assertRequests(t, ctx, "", false)
			if len(f.api.Requests) != 0 {
				t.Fatalf("explicit credential dry-run unexpectedly accessed GitHub: %v", f.api.Requests)
			}
		})
	}
	for _, invalid := range []bool{false, true} {
		name, failure := "missing auth policy", "no such file or directory"
		if invalid {
			name, failure = "invalid auth policy", "contains invalid JSON"
		}
		t.Run(name, func(t *testing.T) {
			f := newSetupFixture(t, ctx, root, catalog, binary, runtimeFiles)
			policyPath := filepath.Join(f.root, "selected-policy.json")
			if invalid {
				setupWrite(t, policyPath, []byte(`{"version":`))
			}
			before, tree := f.state(t), setupCheckoutState(t, f.checkout)
			command := exec.CommandContext(ctx, "gh", "cao", "setup", "auth", "token",
				"--repo", "platform/control", "--policy", policyPath, "--dry-run") // #nosec G204 -- fixed real executable and test-owned policy fixture.
			command.Dir, command.Env = f.checkout, f.env
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), failure) {
				t.Fatalf("want policy failure %q, got %v\n%s", failure, err, output)
			}
			if !bytes.Equal(before, f.state(t)) || !reflect.DeepEqual(tree, setupCheckoutState(t, f.checkout)) {
				t.Fatal("failed credential dry-run changed files or remote state")
			}
			f.assertRequests(t, ctx, "", true)
		})
	}
	t.Run("admission dry-run apply and repeat", func(t *testing.T) {
		f := newSetupFixture(t, ctx, root, catalog, binary, runtimeFiles)
		policy, err := json.Marshal(f.policy)
		if err != nil {
			t.Fatal(err)
		}
		setupWrite(t, f.policyPath(), policy)
		f.api.Repositories["platform/control"].Variables["GH_AW_GITHUB_AUTH_MODE"] = "workflow-token"
		before, tree := f.state(t), setupCheckoutState(t, f.checkout)
		args := []string{"admission", "--repo", "platform/control", "--url", "https://authority.example.invalid/api/admission", "--json"}
		result := setupOptionRun(t, ctx, f, slices.Concat(args, []string{"--dry-run"})...)
		if result["applied"] != false || !bytes.Equal(before, f.state(t)) {
			t.Fatal("admission dry-run mutated remote settings")
		}
		expectedSettings := map[string]string{
			"url": "https://authority.example.invalid/api/admission", "audience": "https://authority.example.invalid/api/admission", "onOutage": "block",
		}
		setupAssertJSON(t, result["settings"], expectedSettings)
		for range 2 {
			result = setupOptionRun(t, ctx, f, args...)
			if result["command"] != "setup admission" || result["repository"] != "platform/control" ||
				result["scope"] != "repository" || result["enabled"] != true || result["applied"] != true {
				t.Fatalf("unexpected admission report: %#v", result)
			}
			setupAssertJSON(t, result["settings"], expectedSettings)
		}
		var expected map[string]*BootstrapRepository
		if err := json.Unmarshal(before, &expected); err != nil {
			t.Fatal(err)
		}
		for name, value := range map[string]string{
			"GH_AW_DEFAULT_ADMISSION_URL":       expectedSettings["url"],
			"GH_AW_DEFAULT_ADMISSION_AUDIENCE":  expectedSettings["audience"],
			"GH_AW_DEFAULT_ADMISSION_ON_OUTAGE": "block",
		} {
			expected["platform/control"].Variables[name] = value
		}
		var actual map[string]*BootstrapRepository
		if err := json.Unmarshal(f.state(t), &actual); err != nil {
			t.Fatal(err)
		}
		setupAssertJSON(t, actual, expected)
		if !bytes.Equal(policy, setupRead(t, f.policyPath())) || !reflect.DeepEqual(tree, setupCheckoutState(t, f.checkout)) {
			t.Fatal("admission setup changed policy authority or runtime")
		}
		f.api.mu.Lock()
		defer f.api.mu.Unlock()
		if len(f.api.Unhandled) != 0 {
			t.Fatalf("unsupported admission routes: %v", f.api.Unhandled)
		}
		var writes []string
		for _, request := range f.api.Requests {
			switch request {
			case "GET /repos/platform/control/actions/variables":
			case "POST /repos/platform/control/actions/variables",
				"PATCH /repos/platform/control/actions/variables/GH_AW_DEFAULT_ADMISSION_ON_OUTAGE",
				"PATCH /repos/platform/control/actions/variables/GH_AW_DEFAULT_ADMISSION_AUDIENCE",
				"PATCH /repos/platform/control/actions/variables/GH_AW_DEFAULT_ADMISSION_URL":
				writes = append(writes, request)
			default:
				t.Errorf("unexpected admission route: %s", request)
			}
		}
		setupAssertJSON(t, writes, []string{
			"POST /repos/platform/control/actions/variables", "POST /repos/platform/control/actions/variables", "POST /repos/platform/control/actions/variables",
			"PATCH /repos/platform/control/actions/variables/GH_AW_DEFAULT_ADMISSION_ON_OUTAGE",
			"PATCH /repos/platform/control/actions/variables/GH_AW_DEFAULT_ADMISSION_AUDIENCE",
			"PATCH /repos/platform/control/actions/variables/GH_AW_DEFAULT_ADMISSION_URL",
		})
	})
}

func setupOptionRun(t *testing.T, ctx context.Context, f *setupFixture, args ...string) map[string]any {
	t.Helper()
	command := exec.CommandContext(ctx, "gh", append([]string{"cao", "setup"}, args...)...) // #nosec G204 -- real executable with fixed test-owned setup options.
	command.Dir, command.Env = f.checkout, f.env
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("setup %s: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, &stdout, &stderr)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("setup options did not return JSON: %v\n%s", err, &stdout)
	}
	return result
}

func setupAssertJSON(t *testing.T, actual, expected any) {
	t.Helper()
	got, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("JSON mismatch:\ngot  %s\nwant %s", got, want)
	}
}
