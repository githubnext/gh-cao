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

	"github.com/spf13/cobra"
)

const nativeManagementPolicy = `{
  "version": 1,
  "gh-aw-version": "v0.92.0",
  "control-plane": {
    "scope": {"allowed-owners": ["owner"], "allowed-repositories": ["owner/repo"]},
    "activity": {"repositories": ["owner/repo"]},
    "defaults": {"mode": "live", "max-repositories": 3},
    "campaigns": {
      "one": {"mode": "live", "targets": {"owner/repo": {"mode": "live"}}, "workers": {"worker": {"workflow": "one-worker", "enabled": false, "max-mode": "review"}}},
      "two": {"enabled": false, "mode": "live"}
    }
  }
}`

func nativeManagementDirectory(t *testing.T) {
	t.Helper()
	directory, err := os.MkdirTemp(".", ".native-management-test-")
	if err != nil {
		t.Fatal(err)
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Error(err)
		}
	})
	t.Chdir(directory)
}

func nativeManagementCommand() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	output := &bytes.Buffer{}
	cmd.SetOut(output)
	cmd.SetErr(output)
	cmd.SetContext(context.Background())
	return cmd, output
}

func TestNativeManagementRepositoryEnrollmentPinsEveryCampaign(t *testing.T) {
	nativeManagementDirectory(t)
	writeTestFile(t, policyPath, nativeManagementPolicy)
	calls := 0
	gh := func(_ context.Context, args ...string) (string, string, error) {
		calls++
		if !reflect.DeepEqual(args, []string{"repo", "view", "other/new", "--json", "nameWithOwner", "--jq", ".nameWithOwner"}) {
			t.Fatalf("lookup = %q", args)
		}
		return "Other/New\n", "", nil
	}
	cmd, output, _ := testCommand(t, gh, nil)
	cmd.SetArgs([]string{"repos", "add", "other/new"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	policy, err := nativeReadValidatedPolicy()
	if err != nil {
		t.Fatal(err)
	}
	control := policy["control-plane"].(map[string]any)
	scope := control["scope"].(map[string]any)
	if !reflect.DeepEqual(scope["allowed-repositories"], []any{"owner/repo", "Other/New"}) ||
		!reflect.DeepEqual(scope["allowed-owners"], []any{"owner", "Other"}) {
		t.Fatalf("unexpected enrolled scope: %#v", scope)
	}
	for name, value := range control["campaigns"].(map[string]any) {
		campaign := value.(map[string]any)
		target := campaign["targets"].(map[string]any)["Other/New"].(map[string]any)
		if target["mode"] != "review" || campaign["mode"] != "live" {
			t.Fatalf("campaign %s changed authority: %#v", name, campaign)
		}
	}
	if !reflect.DeepEqual(control["activity"], map[string]any{"repositories": []any{"owner/repo"}}) {
		t.Fatalf("activity scope changed: %#v", control["activity"])
	}
	original, err := nativeDecodeJSON([]byte(nativeManagementPolicy))
	if err != nil {
		t.Fatal(err)
	}
	originalControl := original.(map[string]any)["control-plane"].(map[string]any)
	if !reflect.DeepEqual(control["defaults"], originalControl["defaults"]) {
		t.Fatal("enrollment changed defaults")
	}
	campaigns := control["campaigns"].(map[string]any)
	for name, value := range originalControl["campaigns"].(map[string]any) {
		for key, setting := range value.(map[string]any) {
			if key == "targets" {
				targets := campaigns[name].(map[string]any)[key].(map[string]any)
				for repository, target := range setting.(map[string]any) {
					if !reflect.DeepEqual(targets[repository], target) {
						t.Fatalf("enrollment changed existing campaign %s target %s", name, repository)
					}
				}
				continue
			}
			if !reflect.DeepEqual(campaigns[name].(map[string]any)[key], setting) {
				t.Fatalf("enrollment changed campaign %s setting %s", name, key)
			}
		}
	}
	if !strings.Contains(output.String(), `"alreadyEnrolled": false`) || calls != 1 {
		t.Fatalf("output = %s; calls = %d", output, calls)
	}
	before, _ := os.ReadFile(policyPath)
	cmd, output, _ = testCommand(t, gh, nil)
	cmd.SetArgs([]string{"repos", "add", "other/new"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(policyPath)
	if !bytes.Equal(before, after) || !strings.Contains(output.String(), `"alreadyEnrolled": true`) {
		t.Fatal("already enrolled policy was rewritten")
	}
}

func TestNativeManagementEnrollmentReusesOwnerCaseInsensitively(t *testing.T) {
	nativeManagementDirectory(t)
	writeTestFile(t, policyPath, nativeManagementPolicy)
	gh := func(_ context.Context, args ...string) (string, string, error) {
		if !reflect.DeepEqual(args, []string{"repo", "view", "OWNER/new", "--json", "nameWithOwner", "--jq", ".nameWithOwner"}) {
			t.Fatalf("lookup = %q", args)
		}
		return "Owner/New\n", "", nil
	}
	cmd, output, _ := testCommand(t, gh, nil)
	cmd.SetArgs([]string{"repos", "add", "OWNER/new"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	policy, err := nativeReadValidatedPolicy()
	if err != nil {
		t.Fatal(err)
	}
	scope := policy["control-plane"].(map[string]any)["scope"].(map[string]any)
	if !reflect.DeepEqual(scope["allowed-owners"], []any{"owner"}) ||
		!reflect.DeepEqual(scope["allowed-repositories"], []any{"owner/repo", "Owner/New"}) {
		t.Fatalf("unexpected scope: %#v", scope)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["repository"] != "Owner/New" || result["alreadyEnrolled"] != false || result["policy"] != policyPath {
		t.Fatalf("unexpected enrollment result: %#v", result)
	}
}

func TestNativeManagementEnrollmentFailsClosed(t *testing.T) {
	cases := []struct {
		name, source, requested, lookup string
		lookupErr                       bool
	}{
		{"invalid repository", nativeManagementPolicy, "owner/*", "owner/new", false},
		{"option as repository", nativeManagementPolicy, "--help", "owner/new", false},
		{"shell metacharacter", nativeManagementPolicy, "owner/repo;echo", "owner/new", false},
		{"trailing newline", nativeManagementPolicy, "owner/repo\n", "owner/new", false},
		{"oversized identity", nativeManagementPolicy, "owner/" + strings.Repeat("x", 200), "owner/new", false},
		{"redirected repository", nativeManagementPolicy, "owner/new", "owner/renamed", false},
		{"failed access", nativeManagementPolicy, "owner/new", "", true},
		{"duplicate authority", strings.Replace(nativeManagementPolicy, `"defaults":`, `"defaults": {"mode":"review"}, "defaults":`, 1), "owner/new", "owner/new", false},
		{"unknown field", strings.Replace(nativeManagementPolicy, `"version": 1`, `"version": 1, "typo": true`, 1), "owner/new", "owner/new", false},
		{"broad discovery", strings.Replace(nativeManagementPolicy, `"allowed-repositories": ["owner/repo"]`, `"allowed-repositories": null`, 1), "owner/new", "owner/new", false},
		{"missing owners", strings.Replace(nativeManagementPolicy, `"allowed-owners": ["owner"], `, "", 1), "owner/new", "owner/new", false},
		{"invalid repository list", strings.Replace(nativeManagementPolicy, `["owner/repo"]`, `[12]`, 1), "owner/new", "owner/new", false},
		{"invalid existing campaign", strings.Replace(nativeManagementPolicy, `"mode": "live"`, `"mode": "debug"`, 1), "owner/new", "owner/new", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			nativeManagementDirectory(t)
			writeTestFile(t, policyPath, tt.source)
			gh := func(context.Context, ...string) (string, string, error) {
				if tt.lookupErr {
					return "", "permission denied", errors.New("denied")
				}
				return tt.lookup, "", nil
			}
			cmd, output, _ := testCommand(t, gh, nil)
			cmd.SetArgs([]string{"repos", "add", "--", tt.requested})
			if err := cmd.Execute(); err == nil {
				t.Fatal("unsafe enrollment succeeded")
			}
			content, _ := os.ReadFile(policyPath)
			if string(content) != tt.source || output.Len() != 0 {
				t.Fatal("failed enrollment modified policy or reported success")
			}
		})
	}
}

func TestNativeManagementEnrollmentRequiresExistingPolicy(t *testing.T) {
	t.Chdir(t.TempDir())
	cmd, output, _ := testCommand(t, nil, nil)
	cmd.SetArgs([]string{"repos", "add", "owner/new"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "required for repository enrollment") {
		t.Fatalf("expected missing enrollment policy error, got %v", err)
	}
	if _, err := os.Stat(policyPath); !errors.Is(err, os.ErrNotExist) || output.Len() != 0 {
		t.Fatal("missing policy was created or enrollment reported success")
	}
}

func TestNativeManagementPolicyValidation(t *testing.T) {
	cases := []struct {
		name, source string
		valid        bool
	}{
		{"valid", nativeManagementPolicy, true},
		{"removed dashboard deployment", strings.Replace(nativeManagementPolicy, `"campaigns": {`, `"campaigns": {"dashboard": {"deploy": false},`, 1), false},
		{"removed campaign deployment", strings.Replace(nativeManagementPolicy, `"two": {`, `"two": {"deploy": true,`, 1), false},
		{"expression", strings.Replace(nativeManagementPolicy, `"v0.92.0"`, `"${{ secrets.PIN }}"`, 1), false},
		{"outside campaign target", strings.Replace(nativeManagementPolicy, `"two": {`, `"two": {"targets":{"other/repo":{"mode":"review"}},`, 1), false},
		{"case duplicate target", strings.Replace(nativeManagementPolicy, `"two": {`, `"two": {"targets":{"owner/repo":{"mode":"review"},"OWNER/REPO":{"mode":"live"}},`, 1), false},
		{"activity outside scope", strings.Replace(nativeManagementPolicy, `"activity": {"repositories": ["owner/repo"]}`, `"activity": {"repositories": ["other/repo"]}`, 1), false},
		{"inventory bad cell", strings.Replace(nativeManagementPolicy, `"defaults":`, `"inventory":{"cell-count":2,"cell-index":2},"defaults":`, 1), false},
		{"publishing needs reviewer", strings.Replace(nativeManagementPolicy, `"defaults":`, `"publishing":{"enabled":true},"defaults":`, 1), false},
		{"worker requires workflow", strings.Replace(nativeManagementPolicy, `"workflow": "one-worker",`, "", 1), false},
		{"volatile topology", strings.Replace(nativeManagementPolicy, `"defaults":`, `"web":{"host":{"target":{"module":"container","replicas":2},"operational-store":{"backend":"memory","single-process":true,"allow-volatile":true}}},"defaults":`, 1), false},
		{"memory acknowledged", strings.Replace(nativeManagementPolicy, `"defaults":`, `"web":{"host":{"target":{"module":"container","replicas":1},"operational-store":{"backend":"memory","single-process":true,"allow-volatile":true}}},"defaults":`, 1), true},
		{"registry credentials", strings.Replace(nativeManagementPolicy, `"defaults":`, `"marketplace":{"registries":[{"id":"x","repository":"owner/repo","ref":"main","api-url":"https://user:pass@example.com"}]},"defaults":`, 1), false},
		{"registry optional anonymous auth", strings.Replace(nativeManagementPolicy, `"defaults":`, `"marketplace":{"registries":[{"id":"x","repository":"owner/repo","ref":"main"}]},"defaults":`, 1), true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			value, err := nativeDecodeJSON([]byte(tt.source))
			if err == nil {
				err = nativeValidatePolicy(value.(map[string]any))
			}
			if (err == nil) != tt.valid {
				t.Fatalf("valid = %v, error = %v", tt.valid, err)
			}
		})
	}
	constructed := map[string]any{"version": 1, "gh-aw-version": "v1.0.0", "control-plane": map[string]any{"scope": map[string]any{"allowed-owners": []string{"owner"}, "allowed-repositories": []string{"owner/repo"}}}}
	if err := nativeValidatePolicy(constructed); err != nil {
		t.Fatalf("Go-created policy rejected: %v", err)
	}
}

func TestNativeManagementUpgradeOrderingAndFailure(t *testing.T) {
	for _, failure := range []string{"", "downgrade", "wrong-installed", "upgrade"} {
		t.Run(failure, func(t *testing.T) {
			nativeManagementDirectory(t)
			writeTestFile(t, policyPath, nativeManagementPolicy)
			cmd, output := nativeManagementCommand()
			requested := "v1.0.0"
			if failure == "downgrade" {
				requested = "v0.91.0"
			}
			calls, installed := [][]string{}, "v0.92.0"
			gh := func(_ context.Context, args ...string) (string, string, error) {
				calls = append(calls, append([]string{}, args...))
				switch strings.Join(args[:2], " ") {
				case "aw version":
					return "", "gh-aw " + installed, nil
				case "extension install":
					if failure != "wrong-installed" {
						installed = requested
					}
					return "", "", nil
				case "aw upgrade":
					if failure == "upgrade" {
						return "", "compilation failed", errors.New("failed")
					}
					return "", "", nil
				default:
					t.Fatalf("unexpected call %q", args)
					return "", "", nil
				}
			}
			err := nativeUpgrade(cmd, requested, gh)
			if (err == nil) != (failure == "") {
				t.Fatalf("error = %v", err)
			}
			content, _ := os.ReadFile(policyPath)
			if failure != "" {
				if string(content) != nativeManagementPolicy || output.Len() != 0 {
					t.Fatal("failed upgrade updated pin")
				}
			} else {
				if !strings.Contains(string(content), `"gh-aw-version": "v1.0.0"`) || !strings.Contains(output.String(), `"previous": "v0.92.0"`) {
					t.Fatal("upgrade pin/result missing")
				}
				if !reflect.DeepEqual(calls[1], []string{"extension", "install", "github/gh-aw", "--pin", "v1.0.0", "--force"}) {
					t.Fatalf("native installer = %q", calls[1])
				}
			}
			if failure == "downgrade" && len(calls) != 0 {
				t.Fatal("downgrade invoked installer")
			}
		})
	}
}

func TestNativeManagementUpdateArgumentParity(t *testing.T) {
	for _, args := range [][]string{
		{"latest", "--major"}, {"latest", "--repo", "owner/repo"}, {"--campaign", "../unsafe"},
		{"--campaign=one", "--campaign=two"}, {"v1.0.0"}, {"latest", "main"}, {"--engine"},
		{"main", "--pre-releases"}, {"main", "--force=true"},
	} {
		if _, err := nativeParseUpdate(args); err == nil {
			t.Errorf("accepted invalid update arguments %q", args)
		}
	}
	got, err := nativeParseUpdate([]string{"--campaign=one", "abcdef0", "--force", "--engine=copilot", "--append", "instruction"})
	if err != nil || got.ref != "abcdef0" || got.campaign != "one" || !reflect.DeepEqual(got.options, []string{"--force", "--engine=copilot", "--append", "instruction"}) {
		t.Fatalf("parsed = %#v, %v", got, err)
	}
}

func TestNativeManagementExactUpdatePreservesAuthority(t *testing.T) {
	nativeManagementDirectory(t)
	writeTestFile(t, policyPath, nativeManagementPolicy)
	revision := strings.Repeat("b", 40)
	recordPath := ".github/aw/packages/one.json"
	record := `{"package":"githubnext/gh-cao/one","source":"githubnext/gh-cao/one@main","resolvedCommit":"` + strings.Repeat("a", 40) + `"}`
	writeTestFile(t, recordPath, record)
	archive := campaignArchive(t, map[string]string{
		"repository/one/cao.json":     `{"campaign":"one","orchestrator":"one","workers":{"worker":"one-worker","new-worker":"one-new-worker"}}`,
		"repository/one/resource.txt": "updated",
	})
	cmd, output := nativeManagementCommand()
	var calls [][]string
	gh := func(_ context.Context, args ...string) (string, string, error) {
		calls = append(calls, append([]string{}, args...))
		switch {
		case reflect.DeepEqual(args, []string{"aw", "version"}):
			return "v0.92.0", "", nil
		case len(args) > 1 && args[0] == "api" && strings.Contains(strings.Join(args, " "), "/commits/"):
			return revision, "", nil
		case len(args) > 1 && args[0] == "aw" && args[1] == "add":
			writeTestFile(t, recordPath, strings.ReplaceAll(record, strings.Repeat("a", 40), revision))
			return "", "", nil
		case args[0] == "api" && strings.Contains(strings.Join(args, " "), "/tarball/"):
			return archive, "", nil
		default:
			t.Fatalf("unexpected update call %q", args)
			return "", "", nil
		}
	}
	if err := nativeUpdate(cmd, []string{revision[:7], "--campaign", "one"}, gh); err != nil {
		t.Fatal(err)
	}
	policy, err := nativeReadValidatedPolicy()
	if err != nil {
		t.Fatal(err)
	}
	control := policy["control-plane"].(map[string]any)
	one := control["campaigns"].(map[string]any)["one"].(map[string]any)
	worker := one["workers"].(map[string]any)["worker"].(map[string]any)
	if one["mode"] != "live" || worker["enabled"] != false || worker["max-mode"] != "review" {
		t.Fatalf("update changed existing authority: %#v", one)
	}
	if !reflect.DeepEqual(control["scope"], map[string]any{"allowed-owners": []any{"owner"}, "allowed-repositories": []any{"owner/repo"}}) {
		t.Fatal("update widened scope")
	}
	if !reflect.DeepEqual(calls[2], []string{"aw", "add", "githubnext/gh-cao/one@" + revision, "--force"}) || !strings.Contains(output.String(), `"resolvedCommit": "`+revision+`"`) {
		t.Fatalf("update call/output mismatch: %q\n%s", calls, output)
	}
}

func TestNativeManagementUpdateRejectsWrongRecordedRevision(t *testing.T) {
	nativeManagementDirectory(t)
	writeTestFile(t, policyPath, nativeManagementPolicy)
	writeTestFile(t, ".github/aw/packages/one.json", `{"package":"githubnext/gh-cao/one","resolvedCommit":"`+strings.Repeat("a", 40)+`"}`)
	cmd, output := nativeManagementCommand()
	gh := func(_ context.Context, args ...string) (string, string, error) {
		if args[0] == "aw" {
			if args[1] == "version" {
				return "v0.92.0", "", nil
			}
			return "", "", nil
		}
		if strings.Contains(strings.Join(args, " "), "/tarball/") {
			t.Fatal("materialized an unverified revision")
		}
		return strings.Repeat("b", 40), "", nil
	}
	if err := nativeUpdate(cmd, []string{"bbbbbbb"}, gh); err == nil || !strings.Contains(err.Error(), "refusing to materialize") {
		t.Fatalf("error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatal("failed update reported success")
	}
}

func TestNativeManagementPreparedReleaseRollback(t *testing.T) {
	nativeManagementDirectory(t)
	revision := strings.Repeat("a", 40)
	recordPath := ".github/aw/packages/one.json"
	workflow := ".github/workflows/one.md"
	original := "source: githubnext/gh-cao/one@" + revision + "\r\n# original\r\n"
	writeTestFile(t, workflow, original)
	source := `{"package":"githubnext/gh-cao/one","source":"githubnext/gh-cao/one@` + revision + `","resolvedCommit":"` + revision + `","files":[{"destination":"` + workflow + `","sha256":"old"}]}`
	writeTestFile(t, recordPath, source)
	records, err := nativeInstalledPackages()
	if err != nil {
		t.Fatal(err)
	}
	gh := func(_ context.Context, args ...string) (string, string, error) { return "v1.2.3", "", nil }
	prepared, err := nativePrepareRelease(context.Background(), gh, records[0], nativeUpdateOptions{}, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(workflow)
	if string(content) != "source: githubnext/gh-cao/one@v1.2.3\r\n# original\r\n" {
		t.Fatalf("prepared workflow = %q", content)
	}
	var document map[string]any
	content, _ = os.ReadFile(recordPath)
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	fileHash, _ := nativeHashFile(workflow)
	if document["files"].([]any)[0].(map[string]any)["sha256"] != fileHash {
		t.Fatal("prepared ownership hash was not updated")
	}
	if err := nativeRestorePackage(prepared); err != nil {
		t.Fatal(err)
	}
	content, _ = os.ReadFile(workflow)
	recordContent, _ := os.ReadFile(recordPath)
	if string(content) != original || string(recordContent) != source {
		t.Fatal("release preparation did not roll back exactly")
	}
}
