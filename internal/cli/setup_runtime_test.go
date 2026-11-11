package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/internal/ghcli"
)

func setupBootstrapGH(t *testing.T, failure string, calls *[]string) ghExecutor {
	t.Helper()
	revision := strings.Repeat("c", 40)
	installed := failure != "missing gh-aw"
	return func(_ context.Context, args ...string) (string, string, error) {
		call := strings.Join(args, " ")
		*calls = append(*calls, call)
		switch {
		case call == "auth status":
			return "", "", nil
		case call == "repo view --json nameWithOwner --jq .nameWithOwner":
			return "org/control", "", nil
		case call == "repo view org/control --json nameWithOwner,visibility":
			return `{"nameWithOwner":"org/control","visibility":"PUBLIC"}`, "", nil
		case call == "release view --repo github.com/githubnext/gh-cao --json tagName --jq .tagName":
			return "v1.2.3", "", nil
		case strings.Contains(call, "/commits/v1.2.3"):
			return revision, "", nil
		case strings.Contains(call, "/contents/aw.yml?ref="+revision):
			return "min-version: v0.93.1\n", "", nil
		case call == "aw version":
			if !installed {
				return "", "unknown command aw", errors.New("missing")
			}
			if failure == "old gh-aw" {
				return "v0.92.0", "", nil
			}
			return "v0.93.1", "", nil
		case call == "extension list":
			return "", "", nil
		case call == "extension install github/gh-aw --pin v0.93.1":
			installed = true
			return "", "", nil
		case call == "aw add githubnext/gh-cao@v1.2.3 --no-security-scanner --no-gitattributes":
			if failure == "package" {
				return "", "failed package", errors.New("failed")
			}
			matches, err := filepath.Glob(".cao-setup-*/checkout")
			if err != nil || len(matches) != 1 {
				t.Fatalf("isolated installation missing: %v %v", matches, err)
			}
			base := matches[0]
			for _, name := range setupRuntimeFiles {
				if strings.HasPrefix(name, ".github/workflows/") {
					writeTestFile(t, filepath.Join(base, name), "package: "+name)
				}
			}
			writeTestFile(t, filepath.Join(base, ".github/aw/packages/root.json"),
				`{"package":"githubnext/gh-cao","source":"githubnext/gh-cao@v1.2.3","resolvedCommit":"`+revision+`"}`)
			writeTestFile(t, filepath.Join(base, "AGENTS.md"), "do not install agent instructions")
			return "", "", nil
		case strings.HasPrefix(call, "release download "):
			for i, arg := range args {
				if arg == "--dir" {
					writeTestFile(t, filepath.Join(args[i+1], "cao-runtime-"+revision+".tgz"),
						nativeRuntimeArchiveFixture(t, revision, failure == "hash"))
				}
			}
			return "", "", nil
		case strings.HasPrefix(call, "attestation verify "):
			if failure == "attestation" {
				return "", "invalid attestation", errors.New("rejected")
			}
			return "", "", nil
		case strings.HasPrefix(call, "api repos/org/control/actions/"):
			return "", "", nil
		case call == "variable set GH_AW_GITHUB_AUTH_MODE --repo org/control --body workflow-token":
			return "", "", nil
		default:
			t.Fatalf("unexpected gh call (no repository creation, publication, dispatch, or secret writes allowed): %q", call)
			return "", "", nil
		}
	}
}

func TestSetupBootstrapsFreshControlCheckout(t *testing.T) {
	for _, guided := range []bool{false, true} {
		t.Run(map[bool]string{false: "unattended", true: "guided"}[guided], func(t *testing.T) {
			nativeSetupTestDirectory(t)
			writeTestFile(t, "AGENTS.md", "consumer instructions")
			writeTestFile(t, ".github/workflows/consumer.yml", "consumer workflow")
			writeTestFile(t, "dashboard/consumer.txt", "consumer dashboard file")
			var calls []string
			gh := setupBootstrapGH(t, "", &calls)
			var prompt nativeSetupPrompt
			if guided {
				prompt = &nativeSetupTestPrompt{texts: []string{"org/control"}, confirms: []bool{true}}
			}
			result, err := nativeSetupControl(nativeSetupTestCommand(), gh, prompt)
			if err != nil || result["controlRepository"] != "org/control" {
				t.Fatalf("fresh setup: %#v, %v", result, err)
			}
			complete, err := setupRuntimeComplete()
			if err != nil || !complete {
				t.Fatalf("incomplete runtime: %v", err)
			}
			policy, err := readPolicy()
			if err != nil {
				t.Fatal(err)
			}
			control := nativeSetupMap(policy["control-plane"])
			scope, _ := nativeSetupStrings(nativeSetupMap(control["scope"])["allowed-repositories"])
			if !reflect.DeepEqual(scope, []string{"org/control"}) || len(nativeSetupMap(control["campaigns"])) != 0 || policy["gh-aw-version"] != "v0.93.1" {
				t.Fatalf("unexpected initial authority: %#v", policy)
			}
			before, _ := os.ReadFile(policyPath)
			calls = nil
			if _, err := nativeSetupControl(nativeSetupTestCommand(), gh, nil); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(policyPath)
			if string(before) != string(after) {
				t.Fatal("rerun changed the policy")
			}
			for _, call := range calls {
				if strings.HasPrefix(call, "aw add ") || strings.HasPrefix(call, "release ") || strings.HasPrefix(call, "extension ") {
					t.Fatalf("rerun reinstalled components: %s", call)
				}
			}
			for name, want := range map[string]string{
				"AGENTS.md": "consumer instructions", ".github/workflows/consumer.yml": "consumer workflow",
				"dashboard/consumer.txt": "consumer dashboard file",
			} {
				got, err := os.ReadFile(name)
				if err != nil || string(got) != want {
					t.Fatalf("consumer file %s changed: %q %v", name, got, err)
				}
			}
			staging, _ := filepath.Glob(".cao-setup-*")
			if len(staging) != 0 {
				t.Fatalf("staging not removed: %v", staging)
			}
		})
	}
}

func TestSetupBootstrapFailures(t *testing.T) {
	for _, failure := range []string{"package", "hash", "attestation", "old gh-aw", "conflict", "malformed policy"} {
		t.Run(failure, func(t *testing.T) {
			nativeSetupTestDirectory(t)
			if failure == "conflict" {
				writeTestFile(t, "activity/cao.mjs", "consumer original")
			}
			if failure == "malformed policy" {
				writeTestFile(t, policyPath, "{invalid")
			}
			var calls []string
			_, err := nativeSetupControl(nativeSetupTestCommand(), setupBootstrapGH(t, failure, &calls), nil)
			if err == nil {
				t.Fatal("failure reported success")
			}
			for _, call := range calls {
				if strings.Contains(call, "/pages") || strings.HasPrefix(call, "variable ") || strings.HasPrefix(call, "secret ") {
					t.Fatalf("failed preparation changed external settings: %s", call)
				}
			}
			if failure == "malformed policy" {
				content, _ := os.ReadFile(policyPath)
				if string(content) != "{invalid" || len(calls) != 3 {
					t.Fatalf("malformed policy was not preserved before preparation: %q, %v", content, calls)
				}
			} else if _, err := os.Stat(policyPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed setup published policy")
			}
			if failure == "conflict" {
				content, _ := os.ReadFile("activity/cao.mjs")
				if string(content) != "consumer original" {
					t.Fatal("runtime conflict overwritten")
				}
			}
			if _, err := os.Stat(".github/workflows/cao-activity.yml"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed preflight left installed workflows")
			}
		})
	}
}

func TestSetupInstallsOnlyMissingGHAW(t *testing.T) {
	nativeSetupTestDirectory(t)
	var calls []string
	if _, err := nativeSetupControl(nativeSetupTestCommand(), setupBootstrapGH(t, "missing gh-aw", &calls), nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(calls, "\n"), "extension install github/gh-aw --pin v0.93.1") {
		t.Fatal("missing gh-aw was not installed at the catalog minimum")
	}
}

func TestSetupProgressPrecedesSlowOperations(t *testing.T) {
	t.Setenv("CI", "")
	nativeSetupTestDirectory(t)
	cmd := nativeSetupTestCommand()
	var calls []string
	run := setupBootstrapGH(t, "missing gh-aw", &calls)
	progress := map[string]string{
		"release view ":                  "Fetching CAO release...",
		"aw version":                     "Checking gh-aw...",
		"extension install":              "Installing gh-aw v0.93.1...",
		"aw add ":                        "Preparing CAO workflows...",
		"release download ":              "Downloading and verifying CAO runtime...",
		"attestation verify":             "Downloading and verifying CAO runtime...",
		"api repos/org/control/actions/": "Checking Actions token defaults...",
	}
	seen := map[string]bool{}
	gh := func(ctx context.Context, args ...string) (string, string, error) {
		call := strings.Join(args, " ")
		for prefix, message := range progress {
			if strings.HasPrefix(call, prefix) {
				if !strings.Contains(cmd.ErrOrStderr().(*bytes.Buffer).String(), message+"\n") {
					t.Fatalf("slow operation %q started without progress %q", call, message)
				}
				seen[message] = true
			}
		}
		return run(ctx, args...)
	}
	prompt := &nativeSetupTestPrompt{texts: []string{"org/control"}, confirms: []bool{true}}
	if _, err := nativeSetupControl(cmd, gh, prompt); err != nil {
		t.Fatal(err)
	}
	for _, message := range progress {
		if !seen[message] {
			t.Fatalf("progress step was not exercised: %q", message)
		}
	}
	output := cmd.ErrOrStderr().(*bytes.Buffer).String()
	position := -1
	for _, message := range []string{
		"Checking CAO runtime...",
		"Fetching CAO release...",
		"Checking gh-aw...",
		"Installing gh-aw v0.93.1...",
		"Preparing CAO workflows...",
		"Downloading and verifying CAO runtime...",
		"Adding CAO runtime files...",
		"Checking Actions token defaults...",
		"Saving CAO policy...",
		"Preparing change summary...",
	} {
		next := strings.Index(output, message+"\n")
		if next <= position {
			t.Fatalf("progress missing or out of order: %q\n%s", message, output)
		}
		position = next
	}
	if cmd.OutOrStdout().(*bytes.Buffer).Len() != 0 {
		t.Fatal("setup progress must not contaminate stdout")
	}
}

func TestSetupRejectsSubdirectory(t *testing.T) {
	nativeSetupTestDirectory(t)
	if err := os.Mkdir("nested", 0o755); err != nil {
		t.Fatal(err)
	}

	t.Chdir("nested")
	_, err := nativeSetupControl(nativeSetupTestCommand(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "root of an existing") {
		t.Fatalf("subdirectory accepted: %v", err)
	}
}

func TestSetupRuntimePublishedRelease(t *testing.T) {
	if os.Getenv("CAO_SETUP_RUNTIME_INTEGRATION") != "1" {
		t.Skip("set CAO_SETUP_RUNTIME_INTEGRATION=1 for read-only release/runtime verification")
	}
	cmd, publicationGH, remote, _ := setupPublishFixture(t, false)
	gh := func(ctx context.Context, args ...string) (string, string, error) {
		if args[0] == "extension" {
			t.Fatal("release verification must not install or replace global extensions")
		}
		return ghcli.Exec(ctx, args...)
	}
	files, err := setupRuntime(cmd, gh)
	if err != nil {
		t.Fatal(err)
	}
	if complete, err := setupRuntimeComplete(); err != nil || !complete {
		t.Fatalf("published runtime incomplete: %v", err)
	}
	policy := initialPolicy("org/control", "")
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, policyPath, string(data))
	result, err := setupPublish(cmd, publicationGH, &setupPublishPrompt{approve: true}, "org/control", append(files, policyPath))
	if err != nil || result["status"] != "published" {
		t.Fatalf("published runtime could not be committed/pushed to local fixture: %#v: %v", result, err)
	}
	if setupPublishTestGit(t, cmd, "--git-dir="+remote, "rev-parse", "main") != result["commit"] {
		t.Fatal("published runtime did not reach the local fixture remote")
	}
	t.Logf("verified %d real prepared runtime/package paths and published them only to a local bare fixture", len(files))
}

func TestSetupFreshCommandJSON(t *testing.T) {
	nativeSetupTestDirectory(t)
	t.Setenv("CI", "true")
	var calls []string
	gh := setupBootstrapGH(t, "", &calls)
	root, output, _ := testCommand(t, gh, nativeOperator(gh))
	root.SetArgs([]string{"setup", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("setup stdout is not one JSON result: %s: %v", output, err)
	}
	if result["command"] != "setup" || result["nonInteractive"] != true || result["controlRepository"] != "org/control" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestSetupMissingRuntimePreservesExistingPolicy(t *testing.T) {
	nativeSetupTestDirectory(t)
	policy := nativeSetupTestPolicy()
	control := nativeSetupMap(policy["control-plane"])
	control["activity"] = map[string]any{"retention-days": 31}
	control["custom-metadata"] = map[string]any{"preserve": true}
	nativeSetupTestSave(t, policy)
	// Remove only fixture runtime markers, leaving policy for a partially prepared checkout.
	for _, name := range append(append([]string{}, setupRuntimeFiles...), nativeRuntimeRequiredFiles...) {
		if err := os.Remove(name); err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	_, err := nativeSetupControl(nativeSetupTestCommand(), setupBootstrapGH(t, "", &calls),
		&nativeSetupTestPrompt{texts: []string{"org/control"}, confirms: []bool{true, true}})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := readPolicy()
	if err != nil {
		t.Fatal(err)
	}
	control = nativeSetupMap(saved["control-plane"])
	if nativeSetupMap(control["activity"])["retention-days"] != json.Number("31") ||
		nativeSetupMap(control["custom-metadata"])["preserve"] != true ||
		saved["gh-aw-version"] != "v0.92.0" {
		t.Fatalf("existing policy metadata or compiler pin was replaced: %#v", saved)
	}
}

func TestSetupRejectsSymlinkDestinations(t *testing.T) {
	for _, name := range []string{policyPath, ".github", "activity"} {
		t.Run(name, func(t *testing.T) {
			nativeSetupTestDirectory(t)
			if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir("consumer", 0o755); err != nil {
				t.Fatal(err)
			}
			target, err := filepath.Abs("consumer")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, name); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			var calls []string
			_, err = nativeSetupControl(nativeSetupTestCommand(), setupBootstrapGH(t, "", &calls), nil)
			if err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("symlink accepted: %v", err)
			}
			for _, call := range calls {
				if strings.Contains(call, "/pages") || strings.HasPrefix(call, "variable ") {
					t.Fatalf("symlink changed external settings: %s", call)
				}
			}
		})
	}
}
