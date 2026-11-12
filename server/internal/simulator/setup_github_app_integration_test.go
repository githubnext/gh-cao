package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testSetupGitHubApp(t *testing.T, ctx context.Context, root, catalog, binary string, runtimeFiles []string, host string) {
	t.Helper()
	t.Run("native GitHub App setup", func(t *testing.T) {
		for _, test := range []struct {
			name         string
			flags        []string
			externalRead bool
			explicitRepo bool
			authError    bool
			existing     bool
			readName     string
			writeName    string
			writeTargets []string
			failure      string
		}{
			{name: "new manifests infer current repository"},
			{name: "custom names and independent write scope", explicitRepo: true,
				flags: []string{"--read-app-name", "approved-read", "--write-app-name", "approved-write",
					"--write-repository", "platform/zeta", "--write-repository", "platform/alpha", "--write-repository", "platform/zeta"},
				readName: "approved-read", writeName: "approved-write", writeTargets: []string{"alpha", "zeta"}},
			{name: "reuse approved Apps across owners", externalRead: true, existing: true,
				flags: []string{"--read-client-id", "simulator-read-app", "--write-client-id", "simulator-write-app"}},
			{name: "new Apps reject cross-owner read scope", externalRead: true,
				failure: "private organization-owned Apps cannot be installed on other"},
			{name: "new Apps reject cross-owner write scope",
				flags:   []string{"--write-repository", "other/app"},
				failure: "private organization-owned Apps cannot be installed on other"},
			{name: "existing Apps require both client IDs",
				flags: []string{"--read-client-id", "simulator-read-app"}, failure: "must be supplied together"},
			{name: "existing Apps require distinct client IDs",
				flags: []string{"--read-client-id", "simulator-app", "--write-client-id", "simulator-app"}, failure: "must be distinct"},
			{name: "existing Apps cannot request replacement",
				flags:   []string{"--read-client-id", "simulator-read-app", "--write-client-id", "simulator-write-app", "--force"},
				failure: "cannot be combined"},
			{name: "repository identity lookup fails closed", authError: true, failure: "Bad credentials"},
		} {
			t.Run(test.name, func(t *testing.T) {
				f := newSetupFixture(t, ctx, root, catalog, binary, runtimeFiles, host)
				if host != "github.com" {
					for _, repo := range f.api.Repositories {
						repo.Private = true
					}
				}
				setupGitHubAppNativePath(t, f)
				f.env = append(f.env, "GITHUB_REPOSITORY=unrelated/ambient")
				f.authError = test.authError
				scope := f.control()["scope"].(map[string]any)
				scope["allowed-owners"] = []string{"platform", "other"}
				repository := "platform/alpha"
				if test.externalRead {
					repository = "other/app"
				}
				scope["allowed-repositories"] = []string{repository}
				policy, err := json.Marshal(f.policy)
				if err != nil {
					t.Fatal(err)
				}
				setupWrite(t, f.policyPath(), policy)
				before, tree := f.state(t), setupCheckoutState(t, f.checkout)
				args := []string{"cao", "setup", "auth", "github-app", "--dry-run", "--no-open"}
				if test.explicitRepo {
					args = append(args, "--repo", "platform/control", "--policy", f.policyPath())
				}
				args = append(args, test.flags...)
				var previous []byte
				for attempt := range 2 {
					command := exec.CommandContext(ctx, "gh", args...) // #nosec G204 -- fixed executable and test-owned setup options; all GitHub traffic terminates at the simulator.
					command.Dir, command.Env = f.checkout, f.env
					var stdout, stderr bytes.Buffer
					command.Stdout, command.Stderr = &stdout, &stderr
					err := command.Run()
					if test.failure != "" {
						if err == nil || stdout.Len() != 0 || !strings.Contains(stderr.String(), test.failure) {
							t.Fatalf("want failure %q, got %v\nstdout: %s\nstderr: %s", test.failure, err, &stdout, &stderr)
						}
						break
					}
					if err != nil || stderr.Len() != 0 {
						t.Fatalf("native App setup: %v\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
					}
					var result map[string]any
					if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
						t.Fatalf("native App preview is not JSON: %v\n%s", err, &stdout)
					}
					readTargets := []map[string]any{{"owner": "platform", "repositories": []string{"alpha", "control"}}}
					if test.externalRead {
						readTargets = []map[string]any{
							{"owner": "platform", "repositories": []string{"control"}},
							{"owner": "other", "repositories": []string{"app"}},
						}
					}
					writeTargets := test.writeTargets
					if writeTargets == nil {
						writeTargets = []string{"control"}
					}
					if result["repo"] != "platform/control" {
						t.Fatalf("setup used ambient identity instead of the current repository: %#v", result)
					}
					setupAssertJSON(t, result["installationTargets"], map[string]any{
						"read": readTargets, "write": []map[string]any{{"owner": "platform", "repositories": writeTargets}},
					})
					setupAssertGitHubAppPlans(t, result, test.existing, test.readName, test.writeName, host)
					if attempt > 0 && !bytes.Equal(previous, stdout.Bytes()) {
						t.Fatal("repeated App preview changed the reviewed plan")
					}
					previous = append(previous[:0], stdout.Bytes()...)
				}
				if !bytes.Equal(policy, setupRead(t, f.policyPath())) || !bytes.Equal(before, f.state(t)) ||
					!reflect.DeepEqual(tree, setupCheckoutState(t, f.checkout)) {
					t.Fatal("App preview or rejected setup changed local files or simulator state")
				}
				f.assertRequests(t, ctx, test.failure != "")
				f.api.mu.Lock()
				defer f.api.mu.Unlock()
				if !test.explicitRepo && len(f.api.Requests) == 0 {
					t.Fatal("repository inference did not exercise the GitHub simulator")
				}
				for _, request := range f.api.Requests {
					if request != "POST /graphql" {
						t.Errorf("App preview must only resolve repository identity, got %s", request)
					}
				}
			})
		}
	})
}

func setupGitHubAppNativePath(t *testing.T, f *setupFixture) {
	t.Helper()
	directory := filepath.Join(f.root, "native-bin")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	// The installed extension must work without Node.js or the deleted script.
	for _, name := range []string{"gh", "git"} {
		source, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(source, filepath.Join(directory, name)); err != nil {
			t.Fatal(err)
		}
	}
	for i, item := range f.env {
		if strings.HasPrefix(item, "PATH=") {
			f.env[i] = "PATH=" + directory
		}
	}
	for _, name := range []string{"setup-github-apps.mjs", "github-apps.mjs"} {
		if _, err := os.Lstat(filepath.Join(f.checkout, ".github/workflows/shared", name)); !os.IsNotExist(err) {
			t.Fatalf("native setup fixture must not contain %s: %v", name, err)
		}
	}
}

func setupAssertGitHubAppPlans(t *testing.T, result map[string]any, existing bool, readName, writeName, host string) {
	t.Helper()
	apps, ok := result["apps"].([]any)
	if !ok || len(apps) != 2 {
		t.Fatalf("expected separate read/write App plans: %#v", result)
	}
	for i, role := range []string{"read", "write"} {
		app := apps[i].(map[string]any)
		prefix := "GH_AW_GITHUB_" + strings.ToUpper(role) + "_APP"
		if app["role"] != role || app["variable"] != prefix+"_ID" || app["secret"] != prefix+"_PRIVATE_KEY" {
			t.Fatalf("unexpected App credential profile: %#v", app)
		}
		if _, exists := app["pem"]; exists {
			t.Fatal("App preview exposed private-key material")
		}
		var permissions map[string]any
		if existing {
			if app["clientId"] != "simulator-"+role+"-app" || app["manifest"] != nil {
				t.Fatalf("approved App preview must not register replacements: %#v", app)
			}
			permissions = app["permissions"].(map[string]any)
		} else {
			name := readName
			if role == "write" {
				name = writeName
			}
			if name == "" {
				name = "cao-platform-control-" + role
			}
			manifest := app["manifest"].(map[string]any)
			for key, expected := range map[string]any{
				"name": name, "url": "https://" + host + "/platform/control", "redirect_url": "http://127.0.0.1:0/callback",
				"public": false, "request_oauth_on_install": false, "default_events": []any{},
				"hook_attributes": map[string]any{"url": "https://" + host + "/platform/control", "active": false},
			} {
				setupAssertJSON(t, manifest[key], expected)
			}
			permissions = manifest["default_permissions"].(map[string]any)
		}
		if len(permissions) == 0 {
			t.Fatal("App preview omitted its permission ceiling")
		}
		for permission, level := range permissions {
			if role == "read" && level != "read" {
				t.Fatalf("read App requests %s: %v", permission, level)
			}
		}
		if role == "write" && (permissions["contents"] != "write" || permissions["pull_requests"] != "write") {
			t.Fatalf("write App lost its safe-output permissions: %#v", permissions)
		}
	}
}
