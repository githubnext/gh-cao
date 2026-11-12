package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cgi" // #nosec G504 -- local Git fixture only.
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// These cases assert capabilities, not GitHub role names. GitHub decides whether
// an enterprise/org owner, repository admin, or PAT holder has each capability.
func testSetupTopology(t *testing.T, ctx context.Context, root, catalog, binary string, runtimeFiles []string) {
	t.Helper()
	for _, host := range []string{"github.com", "octocorp.ghe.com"} {
		controls, targetsByVisibility := []string{"public", "private"}, []string{"public", "private", "internal", "mixed"}
		if host != "github.com" {
			// GHE.com uses Enterprise Managed Users: public repositories do not
			// exist there. Mixed visibility means private plus internal.
			controls, targetsByVisibility = []string{"private", "internal"}, []string{"private", "internal", "mixed"}
		}
		for _, controlVisibility := range controls {
			for _, visibility := range targetsByVisibility {
				for _, ownership := range []string{"same", "cross", "mixed"} {
					name := host + "/" + controlVisibility + "/" + visibility + "/" + ownership
					t.Run("topology/"+name, func(t *testing.T) {
						f := newSetupFixture(t, ctx, root, catalog, binary, runtimeFiles, host)
						private := controlVisibility != "public"
						f.api.Repositories["platform/control"].Private = private
						f.api.Repositories["platform/control"].Internal = controlVisibility == "internal"
						targets := make([]string, 1, 2)
						targets[0] = "platform/alpha"
						switch ownership {
						case "cross":
							targets[0] = "other/app"
						case "mixed":
							targets = append(targets, "other/app")
						}
						if visibility == "mixed" && len(targets) == 1 {
							owner, _, _ := strings.Cut(targets[0], "/")
							repo, err := f.api.create(owner, "mixed-second", false)
							if err != nil {
								t.Fatal(err)
							}
							targets = append(targets, repo.Owner+"/"+repo.Name)
						}
						for i, target := range targets {
							repo := f.api.Repositories[target]
							repo.Private = visibility != "public" && (visibility != "mixed" || i == 0)
							repo.Internal = visibility == "internal"
							if host != "github.com" && visibility == "mixed" {
								repo.Private, repo.Internal = true, i != 0
							}
						}
						setupSaveFixturePolicy(t, f)
						before, tree := setupRead(t, f.policyPath()), setupCheckoutState(t, f.checkout)
						// Deliberately staged consumer work blocks optional publication.
						bootstrapRun(t, ctx, f.checkout, f.env, "git", "add", "consumer.txt")
						index := bootstrapRun(t, ctx, f.checkout, f.env, "git", "ls-files", "--stage")
						steps := [][2]string{{"Repositories (", strings.Join(targets, ",")}}
						failure := ""
						if !private && visibility != "public" {
							failure = "public control repository cannot be configured for non-public"
						} else {
							if visibility != "public" {
								steps = append(steps, [2]string{"Authentication (", "token"})
								setupExistingPATs(f, targets)
							}
							steps = append(steps, [2]string{"Apply this setup?", "y"})
							if visibility != "public" {
								for _, secret := range f.api.Repositories["platform/control"].Secrets {
									steps = append(steps, [2]string{"Repository secret " + secret + " already exists.", "y"})
								}
							}
						}
						// Capture fixture credentials only as names, before setup.
						state := f.state(t)
						output := setupWalkTerminal(t, ctx, f, catalog, steps, failure)
						if failure != "" {
							if !bytes.Equal(before, setupRead(t, f.policyPath())) || !bytes.Equal(state, f.state(t)) {
								t.Fatal("rejected topology mutated policy or repository state")
							}
						} else {
							setupAssertTopology(t, f, targets, visibility != "public")
							if !strings.Contains(output, `"status": "blocked"`) {
								t.Fatalf("publication did not preserve staged consumer work: %s", output)
							}
							setupAssertTargetsUnchanged(t, f, state)
						}
						if !reflect.DeepEqual(tree, setupCheckoutState(t, f.checkout)) ||
							index != bootstrapRun(t, ctx, f.checkout, f.env, "git", "ls-files", "--stage") {
							t.Fatal("setup changed consumer files, runtime or index")
						}
						setupAssertTopologyRequests(t, f)
					})
				}
			}
		}
	}
	testSetupCapabilityFailures(t, ctx, root, catalog, binary, runtimeFiles)
	for _, test := range []struct{ input, failure string }{
		{"platform/*", "public control repository cannot be configured for non-public"},
		{"other/missing", "Could not resolve to a Repository"},
		{"https://github.com/other/app", "owner/repository format"},
	} {
		t.Run("scope rejection/"+test.input, func(t *testing.T) {
			f := newSetupFixture(t, ctx, root, catalog, binary, runtimeFiles)
			setupSaveFixturePolicy(t, f)
			before, state := setupRead(t, f.policyPath()), f.state(t)
			setupWalkTerminal(t, ctx, f, catalog, [][2]string{{"Repositories (", test.input}}, test.failure)
			if !bytes.Equal(before, setupRead(t, f.policyPath())) || !bytes.Equal(state, f.state(t)) {
				t.Fatal("invalid or inaccessible scope changed policy or repository state")
			}
			setupAssertTopologyRequests(t, f)
		})
	}
	testSetupQuickstartPublication(t, ctx, root, catalog, binary, runtimeFiles)
}

func setupAssertTargetsUnchanged(t *testing.T, f *setupFixture, before []byte) {
	t.Helper()
	var previous, current map[string]json.RawMessage
	if err := json.Unmarshal(before, &previous); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(f.state(t), &current); err != nil {
		t.Fatal(err)
	}
	delete(previous, "platform/control")
	delete(current, "platform/control")
	setupAssertJSON(t, current, previous)
}

func setupSaveFixturePolicy(t *testing.T, f *setupFixture) {
	t.Helper()
	body, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	setupWrite(t, f.policyPath(), body)
}

func setupExistingPATs(f *setupFixture, targets []string) {
	owners := []string{"platform"}
	for _, target := range targets {
		owner, _, _ := strings.Cut(target, "/")
		if !slices.Contains(owners, owner) {
			owners = append(owners, owner)
		}
	}
	control := f.api.Repositories["platform/control"]
	for _, owner := range owners {
		control.Secrets = append(control.Secrets, "GH_AW_GITHUB_READ_PAT_"+strings.ToUpper(owner))
	}
	control.Secrets = append(control.Secrets, "GH_AW_GITHUB_WRITE_PAT_PLATFORM")
}

func setupWalkTerminal(t *testing.T, ctx context.Context, f *setupFixture, catalog string, steps [][2]string, failure string) string {
	t.Helper()
	input, err := json.Marshal(steps)
	if err != nil {
		t.Fatal(err)
	}
	output := bootstrapRun(t, ctx, catalog, f.env, "node", filepath.Join(catalog, "tests/helpers/setup-simulator-terminal.mjs"), f.checkout, string(input))
	var result struct {
		Code               int
		Signal             string
		Stdout, Stderr     string
		Answered, Expected int
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("invalid terminal result: %v\n%s", err, output)
	}
	if result.Signal != "" || result.Answered != result.Expected ||
		(failure == "" && result.Code != 0) || (failure != "" && (result.Code == 0 || !strings.Contains(result.Stdout, failure))) {
		t.Fatalf("terminal setup failed: %+v", result)
	}
	return result.Stdout
}

func setupAssertTopology(t *testing.T, f *setupFixture, targets []string, pat bool) {
	t.Helper()
	var policy map[string]any
	if err := json.Unmarshal(setupRead(t, f.policyPath()), &policy); err != nil {
		t.Fatal(err)
	}
	control := policy["control-plane"].(map[string]any)
	repositories := append([]string{"platform/control"}, targets...)
	setupAssertJSON(t, control["scope"].(map[string]any)["allowed-repositories"], repositories)
	owners := []string{"platform"}
	for _, target := range targets {
		owner, _, _ := strings.Cut(target, "/")
		if !slices.Contains(owners, owner) {
			owners = append(owners, owner)
		}
	}
	setupAssertJSON(t, control["scope"].(map[string]any)["allowed-owners"], owners)
	setupAssertJSON(t, control["campaigns"], map[string]any{})
	for _, kind := range []string{"workflows", "operations"} {
		if value, exists := control[kind]; exists {
			setupAssertJSON(t, value, map[string]any{})
		}
	}
	remote := f.api.Repositories["platform/control"]
	if !pat {
		setupAssertJSON(t, control["activity"].(map[string]any)["repositories"], repositories)
		if control["activity"].(map[string]any)["public-only"] != true || len(remote.Variables) != 0 {
			t.Fatal("public targets unnecessarily configured credentials")
		}
		return
	}
	if remote.Variables["GH_AW_GITHUB_AUTH_MODE"] != "pat" {
		t.Fatal("PAT mode was not selected after owner maps")
	}
	read := map[string]string{}
	for _, repository := range repositories {
		owner, _, _ := strings.Cut(repository, "/")
		read[repository] = "GH_AW_GITHUB_READ_PAT_" + strings.ToUpper(owner)
	}
	for role, want := range map[string]map[string]string{
		"READ": read, "WRITE": {"platform/control": "GH_AW_GITHUB_WRITE_PAT_PLATFORM"},
	} {
		var got map[string]string
		if err := json.Unmarshal([]byte(remote.Variables["GH_AW_GITHUB_"+role+"_PAT_REPOSITORIES"]), &got); err != nil {
			t.Fatal(err)
		}
		setupAssertJSON(t, got, want)
	}
}

func setupAssertTopologyRequests(t *testing.T, f *setupFixture) {
	t.Helper()
	f.api.mu.Lock()
	defer f.api.mu.Unlock()
	if len(f.api.Unhandled) != 0 {
		t.Fatalf("unhandled simulator routes: %v", f.api.Unhandled)
	}
	for _, request := range f.api.Requests {
		if strings.HasSuffix(request, "/pages") {
			t.Errorf("operational setup used Pages: %s", request)
			continue
		}
		if request == "POST /graphql" || strings.HasPrefix(request, "GET ") {
			continue
		}
		if request == "POST /repos/platform/control/actions/variables" ||
			strings.HasPrefix(request, "PATCH /repos/platform/control/actions/variables/GH_AW_GITHUB_") {
			continue
		}
		t.Errorf("setup mutated a target, dispatched work, or used an unexpected capability: %s", request)
	}
}

func testSetupCapabilityFailures(t *testing.T, ctx context.Context, root, catalog, binary string, runtimeFiles []string) {
	t.Helper()
	for _, test := range []struct {
		name, path, failure string
		private, pat        bool
	}{
		{name: "owner without credential permission", private: true, pat: true, path: "/repos/platform/control/actions/secrets", failure: "HTTP 403"},
		{name: "PAT cannot override inherited mode", path: "/repos/platform/control/actions/variables", failure: "HTTP 403"},
	} {
		t.Run("capability/"+test.name, func(t *testing.T) {
			f := newSetupFixture(t, ctx, root, catalog, binary, runtimeFiles)
			f.api.Repositories["platform/control"].Private = test.private
			f.api.Repositories["platform/control"].Variables["GH_AW_GITHUB_AUTH_MODE"] = "app"
			target := "other/app"
			f.api.Repositories[target].Private = test.pat
			f.setHandler(func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != test.path {
					return false
				}
				// A variable read succeeds; only the required write is denied.
				if strings.Contains(test.path, "variables") && r.Method == http.MethodGet {
					return false
				}
				f.api.mu.Lock()
				f.api.Requests = append(f.api.Requests, r.Method+" "+r.URL.Path)
				f.api.mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "Simulator capability denied"})
				return true
			})
			setupSaveFixturePolicy(t, f)
			before, state := setupRead(t, f.policyPath()), f.state(t)
			steps := [][2]string{{"Repositories (", target}}
			if test.pat {
				steps = append(steps, [2]string{"Authentication (", "token"})
			}
			steps = append(steps, [2]string{"Apply this setup?", "y"})
			setupWalkTerminal(t, ctx, f, catalog, steps, test.failure)
			if !bytes.Equal(before, setupRead(t, f.policyPath())) || !bytes.Equal(state, f.state(t)) {
				t.Fatal("missing capability changed policy or repository state")
			}
			setupAssertTopologyRequests(t, f)
		})
	}
}

func testSetupQuickstartPublication(t *testing.T, ctx context.Context, root, catalog, binary string, runtimeFiles []string) {
	t.Helper()
	for _, host := range []string{"github.com", "octocorp.ghe.com"} {
		t.Run("recorded quickstart publication/"+host, func(t *testing.T) {
			f := newSetupFixture(t, ctx, root, catalog, binary, runtimeFiles, host)
			enterprise := host != "github.com"
			if enterprise {
				f.api.Repositories["platform/control"].Private = true
				f.api.Repositories["platform/alpha"].Private = true
				f.api.Repositories["platform/zeta"].Private = true
				f.api.Repositories["platform/zeta"].Internal = true
				setupExistingPATs(f, []string{"platform/alpha", "platform/zeta"})
			}
			setupSaveFixturePolicy(t, f)
			git, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			remote := filepath.Join(f.root, "remotes/platform/control.git")
			bootstrapRun(t, ctx, f.root, f.env, "git", "init", "--bare", "--initial-branch=main", remote)
			bootstrapRun(t, ctx, f.root, f.env, "git", "--git-dir="+remote, "config", "http.receivepack", "true")
			gitHandler := &cgi.Handler{Path: git, Args: []string{"http-backend"}, Root: "/", Env: []string{
				"GIT_PROJECT_ROOT=" + filepath.Join(f.root, "remotes"), "GIT_HTTP_EXPORT_ALL=1",
				"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
				"GIT_EXEC_PATH=" + strings.TrimSpace(bootstrapRun(t, ctx, f.root, f.env, "git", "--exec-path")),
			}}
			f.api.mu.Lock()
			f.api.Git = gitHandler
			f.api.mu.Unlock()
			bootstrapRun(t, ctx, f.checkout, f.env, "git", "config", "user.name", "CAO simulator")
			bootstrapRun(t, ctx, f.checkout, f.env, "git", "config", "user.email", "simulator@example.invalid")
			bootstrapRun(t, ctx, f.checkout, f.env, "git", "add", ".github", "activity", "dashboard")
			bootstrapRun(t, ctx, f.checkout, f.env, "git", "commit", "-qm", "Installed runtime fixture")
			bootstrapRun(t, ctx, f.checkout, f.env, "git", "push", "origin", "HEAD:main")
			// The GitHub.com case follows the recording; GHE.com uses a private
			// control, non-public scope and retained PATs.
			f.api.mu.Lock()
			delete(f.api.Repositories, "platform/secret")
			f.api.mu.Unlock()
			steps := [][2]string{{"Repositories (", "platform/*"}}
			if enterprise {
				steps = append(steps, [2]string{"Authentication (", "token"})
			}
			steps = append(steps, [2]string{"Apply this setup?", "y"})
			if enterprise {
				for _, secret := range f.api.Repositories["platform/control"].Secrets {
					steps = append(steps, [2]string{"Repository secret " + secret + " already exists.", "y"})
				}
			}
			steps = append(steps, [2]string{"Commit and push these CAO monitoring changes?", "y"})
			output := setupWalkTerminal(t, ctx, f, catalog, steps, "")
			if !strings.Contains(output, `"status": "published"`) || !strings.Contains(output, `"actionsStatus": "pending"`) {
				t.Fatalf("quickstart did not return pending Actions status after publication: %s", output)
			}
			published := bootstrapRun(t, ctx, f.root, f.env, "git", "--git-dir="+remote, "show", "main:.github/workflows/cao.json")
			if strings.TrimSpace(published) != strings.TrimSpace(string(setupRead(t, f.policyPath()))) {
				t.Fatal("published policy differs from the reviewed local configuration")
			}
			for _, workflow := range []string{"cao-validate.yml"} {
				bootstrapRun(t, ctx, f.root, f.env, "git", "--git-dir="+remote, "show", "main:.github/workflows/"+workflow)
			}
			if bootstrapRun(t, ctx, f.checkout, f.env, "git", "status", "--short", "--", "consumer.txt") != "?? consumer.txt\n" {
				t.Fatal("optional publication included unrelated consumer work")
			}
			f.api.mu.Lock()
			defer f.api.mu.Unlock()
			if len(f.api.Unhandled) != 0 {
				t.Fatalf("unhandled publication routes: %v", f.api.Unhandled)
			}
			for _, request := range f.api.Requests {
				if strings.Contains(request, "/dispatches") || (strings.Contains(request, "/secrets") && !enterprise) {
					t.Errorf("quickstart dispatched work or configured credentials: %s", request)
				}
			}
		})
	}
}
