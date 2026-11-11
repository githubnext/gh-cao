package simulator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestCAOSetupIntegration exercises the installed-runtime setup path. Runtime
// package installation and release verification belong to the bootstrap suite.
func TestCAOSetupIntegration(t *testing.T) {
	if os.Getenv("CAO_SETUP_INTEGRATION") != "1" {
		t.Skip("run npm run test:integration:setup to exercise real gh cao setup")
	}
	for _, name := range []string{"go", "gh", "git"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	binary := filepath.Join(root, "gh-cao")
	bootstrapRun(t, ctx, catalog, nil, "go", "build", "-mod=readonly", "-o", binary, "./cmd/gh-cao")
	runtimeFiles := setupRuntimeFixtureFiles(t, catalog)

	tests := []struct {
		name    string
		args    []string
		change  func(*testing.T, *setupFixture)
		want    []string
		profile string
		failure string
		repeat  bool
		plain   bool
	}{
		{name: "default public and idempotent without Pages", want: []string{"platform/control"}, profile: "workflow-token", repeat: true},
		{name: "plain setup and JSON retry", want: []string{"platform/control"}, profile: "workflow-token", plain: true},
		{name: "existing App mode switches once", change: func(t *testing.T, f *setupFixture) {
			f.api.Repositories["platform/control"].Variables["GH_AW_GITHUB_AUTH_MODE"] = "app"
			f.expectedVariables["GH_AW_GITHUB_AUTH_MODE"] = "workflow-token"
			f.authModeWrites = 2 // gh variable set attempts creation before updating an existing variable.
		}, want: []string{"platform/control"}, profile: "workflow-token", repeat: true},
		{name: "inherited PAT mode receives repository override", change: func(t *testing.T, f *setupFixture) {
			f.api.Repositories["platform/control"].OrganizationVariables = map[string]string{"GH_AW_GITHUB_AUTH_MODE": "pat"}
			f.expectedVariables["GH_AW_GITHUB_AUTH_MODE"] = "workflow-token"
			f.authModeWrites = 1
		}, want: []string{"platform/control"}, profile: "workflow-token", repeat: true},
		{name: "repository default masks inherited App mode", change: func(t *testing.T, f *setupFixture) {
			f.api.Repositories["platform/control"].Variables["GH_AW_GITHUB_AUTH_MODE"] = ""
			f.api.Repositories["platform/control"].OrganizationVariables = map[string]string{"GH_AW_GITHUB_AUTH_MODE": "app"}
			f.expectedVariables["GH_AW_GITHUB_AUTH_MODE"] = ""
		}, want: []string{"platform/control"}, profile: "workflow-token"},
		{name: "repeated exact scope deduplicates across owners", args: []string{"--repository", "platform/alpha", "--repository", "platform/alpha", "--repository", "other/app"},
			want: []string{"platform/control", "platform/alpha", "other/app"}, profile: "public-only", repeat: true},
		{name: "wildcard snapshots only public repositories", args: []string{"--repository", "platform/*", "--repository", "platform/alpha"},
			want: []string{"platform/control", "platform/alpha", "platform/zeta"}, profile: "public-only", repeat: true},
		{name: "existing policy metadata preserved", change: func(t *testing.T, f *setupFixture) {
			f.policy["operator-note"] = "retain reviewed metadata"
			f.control()["activity"] = map[string]any{"repositories": []string{"platform/control"}, "retention-days": 30}
		}, want: []string{"platform/control"}, profile: "workflow-token"},
		{name: "invalid coordinate", args: []string{"--repository", "platform"}, failure: "owner/repository format"},
		{name: "owner wildcard refused", args: []string{"--repository", "*/*"}, failure: "accepts exact OWNER/REPO or OWNER/*"},
		{name: "no wildcard match", args: []string{"--repository", "platform/absent*"}, failure: "matched no accessible repositories"},
		{name: "private-only wildcard", args: []string{"--repository", "platform/secret*"}, failure: "matched no accessible repositories"},
		{name: "private exact target", args: []string{"--repository", "platform/secret"}, failure: "public-only monitoring cannot include"},
		{name: "missing exact target", args: []string{"--repository", "platform/missing"}, failure: "Could not resolve to a Repository"},
		{name: "authentication rejected", change: func(t *testing.T, f *setupFixture) {
			f.authError = true
		}, failure: "GitHub CLI authentication check failed"},
		{name: "private control", change: func(t *testing.T, f *setupFixture) {
			f.api.Repositories["platform/control"].Private = true
		}, failure: "requires a public control repository"},
		{name: "existing broader scope refused", change: func(t *testing.T, f *setupFixture) {
			f.control()["scope"].(map[string]any)["allowed-repositories"] = []string{"platform/*"}
		}, failure: "refuses to replace existing repository scope"},
		{name: "explicit scope cannot replace reviewed authority", args: []string{"--repository", "platform/alpha"}, change: func(t *testing.T, f *setupFixture) {
			f.control()["scope"].(map[string]any)["allowed-repositories"] = []string{"platform/*"}
		}, failure: "refuses to replace existing repository scope"},
		{name: "existing Activity scope refused", change: func(t *testing.T, f *setupFixture) {
			f.control()["activity"] = map[string]any{"repositories": []string{"platform/alpha"}}
		}, failure: "refuses to replace existing Activity scope"},
		{name: "campaign authority refused", change: func(t *testing.T, f *setupFixture) {
			f.control()["campaigns"] = map[string]any{"audit": map[string]any{"enabled": false}}
		}, failure: "no declared campaigns"},
		{name: "standalone authority refused", change: func(t *testing.T, f *setupFixture) {
			f.control()["workflows"] = map[string]any{"audit": map[string]any{"mode": "review"}}
		}, failure: "no declared standalone workflows"},
		{name: "operation authority refused", change: func(t *testing.T, f *setupFixture) {
			f.control()["operations"] = map[string]any{"audit": map[string]any{"mode": "review"}}
		}, failure: "no declared operations"},
		{name: "invalid existing policy preserved", change: func(t *testing.T, f *setupFixture) {
			setupWrite(t, f.policyPath(), []byte(`{"version":`))
		}, failure: "CAO policy is invalid"},
		{name: "unsupported policy version preserved", change: func(t *testing.T, f *setupFixture) {
			f.policy["version"] = 2
		}, failure: "must declare version 1"},
		{name: "runtime directory conflict", change: func(t *testing.T, f *setupFixture) {
			name := filepath.Join(f.checkout, ".github/workflows/cao-activity.yml")
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(name, 0o700); err != nil {
				t.Fatal(err)
			}
		}, failure: "requires a regular runtime file"},
		{name: "policy symlink refused", change: func(t *testing.T, f *setupFixture) {
			target := filepath.Join(f.root, "policy-target.json")
			setupWrite(t, target, []byte(`{"version":1}`))
			if err := os.Symlink(target, f.policyPath()); err != nil {
				t.Fatal(err)
			}
		}, failure: "destination escapes the repository"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newSetupFixture(t, ctx, root, catalog, binary, runtimeFiles)
			if test.change != nil {
				test.change(t, f)
			}
			if _, err := os.Lstat(f.policyPath()); os.IsNotExist(err) {
				content, err := json.MarshalIndent(f.policy, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				setupWrite(t, f.policyPath(), content)
			}
			before := setupRead(t, f.policyPath())
			tree := setupCheckoutState(t, f.checkout)
			state := f.state(t)
			var plainPolicy []byte
			if test.plain {
				output := bootstrapRun(t, ctx, f.checkout, f.env, "gh", "cao", "setup")
				if !strings.Contains(output, "Monitoring configured locally. No commit or push was performed.") ||
					!strings.Contains(output, "Next: gh cao doctor") {
					t.Fatalf("plain setup did not explain local-only completion: %s", output)
				}
				plainPolicy = setupRead(t, f.policyPath())
			}
			result := f.run(t, ctx, test.failure, test.args...)
			if test.plain && !bytes.Equal(plainPolicy, setupRead(t, f.policyPath())) {
				t.Fatal("JSON retry changed policy produced by plain setup")
			}
			if test.failure != "" {
				if !bytes.Equal(before, setupRead(t, f.policyPath())) || !bytes.Equal(state, f.state(t)) {
					t.Fatal("failed setup changed reviewed policy or simulator repository state")
				}
			} else {
				f.assertSuccess(t, result, test.want, test.profile)
				after := setupRead(t, f.policyPath())
				if test.repeat {
					// No flags on retry: persisted public-only scope must be reused.
					f.assertSuccess(t, f.run(t, ctx, ""), test.want, test.profile)
					if !bytes.Equal(after, setupRead(t, f.policyPath())) {
						t.Fatal("idempotent setup rewrote policy")
					}
				}
			}
			f.assertRequests(t, ctx, test.failure != "")
			if !reflect.DeepEqual(tree, setupCheckoutState(t, f.checkout)) {
				t.Fatal("setup changed files other than the reviewed policy")
			}
		})
	}
	testSetupOptions(t, ctx, root, catalog, binary, runtimeFiles)
	for _, host := range []string{"github.com", "octocorp.ghe.com"} {
		t.Run("App preview host/"+host, func(t *testing.T) {
			testSetupGitHubApp(t, ctx, root, catalog, binary, runtimeFiles, host)
		})
	}
	testSetupTopology(t, ctx, root, catalog, binary, runtimeFiles)
}

type setupFixture struct {
	root, checkout    string
	env               []string
	api               *BootstrapAPI
	policy            map[string]any
	authError         bool
	expectedVariables map[string]string
	authModeWrites    int
	handleMu          sync.RWMutex
	handle            func(http.ResponseWriter, *http.Request) bool
}

func (f *setupFixture) setHandler(handle func(http.ResponseWriter, *http.Request) bool) {
	f.handleMu.Lock()
	defer f.handleMu.Unlock()
	f.handle = handle
}

func newSetupFixture(t *testing.T, ctx context.Context, root, catalog, binary string, runtimeFiles []string, hosts ...string) *setupFixture {
	t.Helper()
	directory := t.TempDir()
	f := &setupFixture{root: directory, checkout: filepath.Join(directory, "checkout"), api: NewBootstrapAPI("v0.0.1", nil), expectedVariables: map[string]string{}}
	host := "github.com"
	if len(hosts) > 0 {
		host = hosts[0]
		f.api.ServerURL = "https://" + host
	}
	for _, name := range []string{"platform/control", "platform/zeta", "platform/alpha", "platform/secret", "other/app"} {
		owner, repo, _ := strings.Cut(name, "/")
		if _, err := f.api.create(owner, repo, repo == "secret"); err != nil {
			t.Fatal(err)
		}
	}
	proxy, ca := bootstrapProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if host != "github.com" && r.Host != host && r.Host != "api."+host {
			t.Errorf("enterprise setup used another GitHub host: %s", r.Host)
			http.Error(w, "wrong GitHub host", http.StatusForbidden)
			return
		}
		f.handleMu.RLock()
		handle := f.handle
		f.handleMu.RUnlock()
		if handle != nil && handle(w, r) {
			return
		}
		if f.authError && (r.URL.Path == "/user" || r.URL.Path == "/graphql") {
			f.api.mu.Lock()
			f.api.Requests = append(f.api.Requests, r.Method+" "+r.URL.Path)
			f.api.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "Bad credentials"})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/pages") {
			t.Errorf("setup must not use Pages: %s %s", r.Method, r.URL.Path)
			http.Error(w, "Pages is not an operational setup capability", http.StatusForbidden)
			return
		}
		f.api.ServeHTTP(w, r)
	}), host, "api."+host)
	f.env = append(bootstrapEnvironment(t, directory, proxy, ca), "TMPDIR="+root) //nolint:contextcheck // Shared environment helper uses this test's own lifetime.
	if host != "github.com" {
		for i, item := range f.env {
			if item == "GH_HOST=github.com" {
				f.env[i] = "GH_HOST=" + host
			}
			if strings.HasPrefix(item, "GH_TOKEN=") {
				f.env[i] = strings.Replace(item, "GH_TOKEN=", "GH_ENTERPRISE_TOKEN=", 1)
			}
		}
	}
	bootstrapInstallAW(t, f.env)
	for _, item := range f.env {
		if data, ok := strings.CutPrefix(item, "XDG_DATA_HOME="); ok {
			destination := filepath.Join(data, "gh/extensions/gh-cao/gh-cao")
			if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(binary, destination); err != nil {
				t.Fatal(err)
			}
		}
	}
	bootstrapRun(t, ctx, directory, f.env, "git", "init", "--quiet", "--initial-branch=main", f.checkout)
	bootstrapRun(t, ctx, f.checkout, f.env, "git", "remote", "add", "origin", "https://"+host+"/platform/control.git")
	for _, name := range runtimeFiles {
		setupWrite(t, filepath.Join(f.checkout, name), setupRead(t, filepath.Join(catalog, name)))
	}
	setupWrite(t, filepath.Join(f.checkout, "consumer.txt"), []byte("unrelated consumer-owned file\n"))
	var catalogPolicy struct {
		GHAWVersion string `json:"gh-aw-version"`
	}
	if err := json.Unmarshal(setupRead(t, filepath.Join(catalog, ".github/workflows/cao.json")), &catalogPolicy); err != nil {
		t.Fatal(err)
	}
	if catalogPolicy.GHAWVersion == "" {
		t.Fatal("catalog policy must declare gh-aw-version")
	}
	f.policy = map[string]any{"version": 1, "gh-aw-version": catalogPolicy.GHAWVersion, "control-plane": map[string]any{
		"scope":     map[string]any{"allowed-owners": []string{"platform"}, "allowed-repositories": []string{"platform/control"}},
		"campaigns": map[string]any{},
	}}
	return f
}

func (f *setupFixture) control() map[string]any { return f.policy["control-plane"].(map[string]any) }
func (f *setupFixture) policyPath() string {
	return filepath.Join(f.checkout, ".github/workflows/cao.json")
}

func (f *setupFixture) state(t *testing.T) []byte {
	t.Helper()
	f.api.mu.Lock()
	defer f.api.mu.Unlock()
	content, err := json.Marshal(f.api.Repositories)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func (f *setupFixture) run(t *testing.T, ctx context.Context, failure string, args ...string) map[string]any {
	t.Helper()
	command := exec.CommandContext(ctx, "gh", append([]string{"cao", "setup", "--json"}, args...)...) // #nosec G204 -- fixed executable and test-owned options in an isolated environment.
	command.Dir, command.Env = f.checkout, f.env
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if failure != "" {
		if err == nil || !strings.Contains(stderr.String(), failure) {
			t.Fatalf("want failure %q, got %v\nstdout: %s\nstderr: %s", failure, err, &stdout, &stderr)
		}
		return nil
	}
	if err != nil {
		t.Fatalf("setup: %v\nstdout: %s\nstderr: %s\nrequests: %v", err, &stdout, &stderr, f.api.Requests)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("setup stdout is not JSON: %v\n%s", err, &stdout)
	}
	return result
}

func (f *setupFixture) assertSuccess(t *testing.T, result map[string]any, repositories []string, profile string) {
	t.Helper()
	decode := func(value any) any {
		content, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var normalized any
		if err := json.Unmarshal(content, &normalized); err != nil {
			t.Fatal(err)
		}
		return normalized
	}
	expected := map[string]any{
		"command": "setup", "controlRepository": "platform/control", "visibility": "public",
		"repositories": repositories, "profile": profile, "nonInteractive": true,
		"authentication": map[string]any{"mode": "workflow-token", "repository": "platform/control"},
		"campaigns":      []string{},
		"publication":    map[string]any{"status": "not-requested"},
	}
	if !reflect.DeepEqual(result, decode(expected)) {
		t.Fatalf("unexpected setup JSON: %#v", result)
	}
	expectedPolicy := decode(f.policy).(map[string]any)
	control := expectedPolicy["control-plane"].(map[string]any)
	owners := []string{}
	for _, repo := range repositories {
		owner, _, _ := strings.Cut(repo, "/")
		if !slices.Contains(owners, owner) {
			owners = append(owners, owner)
		}
	}
	control["scope"] = decode(map[string]any{"allowed-owners": owners, "allowed-repositories": repositories})
	activity, _ := control["activity"].(map[string]any)
	if activity == nil {
		activity = map[string]any{}
	}
	activity["repositories"] = decode(repositories)
	if profile == "public-only" {
		activity["public-only"] = true
	}
	control["activity"] = activity
	var actual map[string]any
	if err := json.Unmarshal(setupRead(t, f.policyPath()), &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expectedPolicy) {
		t.Fatalf("setup changed authority or metadata beyond exact monitoring scope:\n%s", setupRead(t, f.policyPath()))
	}
	f.api.mu.Lock()
	defer f.api.mu.Unlock()
	if len(f.api.Repositories) != 5 {
		t.Fatal("setup created or removed a repository")
	}
	for name, repo := range f.api.Repositories {
		if name == "platform/control" {
			if repo.Private ||
				!reflect.DeepEqual(repo.Variables, f.expectedVariables) {
				t.Fatalf("unexpected control repository changes: %+v", repo)
			}
		} else if len(repo.Variables) != 0 || repo.Private != (name == "platform/secret") {
			t.Fatalf("setup modified target %s: %+v", name, repo)
		}
	}
}

func (f *setupFixture) assertRequests(t *testing.T, ctx context.Context, failed bool) {
	t.Helper()
	f.api.mu.Lock()
	defer f.api.mu.Unlock()
	if len(f.api.Unhandled) != 0 {
		t.Fatalf("unsupported API routes: %v", f.api.Unhandled)
	}
	modeWrites := 0
	for _, request := range f.api.Requests {
		switch request {
		case "GET /", "GET /user", "POST /graphql",
			"GET /repos/platform/control/actions/variables",
			"GET /repos/platform/control/actions/organization-variables",
			"GET /repos/platform/control/actions/variables/GH_AW_GITHUB_AUTH_MODE":
		case "POST /repos/platform/control/actions/variables", "PATCH /repos/platform/control/actions/variables/GH_AW_GITHUB_AUTH_MODE":
			modeWrites++
			if failed {
				t.Errorf("failed setup wrote credentials: %s", request)
			}
		default:
			t.Errorf("unexpected setup API request: %s", request)
		}
	}
	if modeWrites != f.authModeWrites {
		t.Errorf("authentication mode writes = %d, want %d", modeWrites, f.authModeWrites)
	}
	if got := string(setupRead(t, filepath.Join(f.checkout, "consumer.txt"))); got != "unrelated consumer-owned file\n" {
		t.Fatal("setup modified unrelated consumer file")
	}
	index := bootstrapRun(t, ctx, f.checkout, f.env, "git", "ls-files", "--stage")
	if index != "" {
		t.Fatal("unattended setup staged or committed files")
	}
	head := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "HEAD")
	head.Dir, head.Env = f.checkout, f.env
	if err := head.Run(); err == nil {
		t.Fatal("unattended setup created a commit")
	}
}

func setupCheckoutState(t *testing.T, checkout string) map[string][sha256.Size]byte {
	t.Helper()
	state := map[string][sha256.Size]byte{}
	if err := filepath.WalkDir(checkout, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(checkout, name)
		if err != nil {
			return err
		}
		if filepath.ToSlash(relative) != ".github/workflows/cao.json" {
			state[relative] = sha256.Sum256(setupRead(t, name))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return state
}

// Read only the two declared file inventories, not entire source trees (which
// could include credentials, local build output, or agent instruction files).
func setupRuntimeFixtureFiles(t *testing.T, catalog string) []string {
	t.Helper()
	var result []string
	for _, inventory := range []struct{ file, name string }{
		{"setup_runtime.go", "setupRuntimeFiles"},
		{"native_management_runtime.go", "nativeRuntimeRequiredFiles"},
	} {
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(catalog, "internal/cli", inventory.file), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		ast.Inspect(file, func(node ast.Node) bool {
			spec, ok := node.(*ast.ValueSpec)
			if !ok || len(spec.Names) != 1 || spec.Names[0].Name != inventory.name {
				return true
			}
			found = true
			list, ok := spec.Values[0].(*ast.CompositeLit)
			if !ok {
				t.Fatal("runtime inventory must be a literal file list")
			}
			for _, element := range list.Elts {
				value, ok := element.(*ast.BasicLit)
				if !ok {
					t.Fatal("runtime inventory must contain literal paths")
				}
				name, err := strconv.Unquote(value.Value)
				if err != nil || !filepath.IsLocal(name) || strings.HasPrefix(name, ".github/agents/") {
					t.Fatalf("invalid runtime inventory path %q", name)
				}
				result = append(result, name)
			}
			return false
		})
		if !found {
			t.Fatalf("missing runtime file inventory %s", inventory.name)
		}
	}
	return result
}

func setupRead(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(name) // #nosec G304,G703 -- test-owned fixture files inside the checkout or isolated test root.
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func setupWrite(t *testing.T, name string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
