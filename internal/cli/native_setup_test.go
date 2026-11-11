package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func nativeSetupTestDirectory(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp(".", ".native-setup-test-")
	if err != nil {
		t.Fatal(err)
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(absolute); err != nil {
			t.Error(err)
		}
	})
	t.Chdir(absolute)
	if output, err := exec.Command("git", "init", "--quiet", ".").CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", output, err)
	}
	return absolute
}

func nativeSetupTestJSON(t *testing.T, writer io.Writer, value any) {
	t.Helper()
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Error(err)
	}
}

func nativeSetupTestPolicy() map[string]any {
	return map[string]any{
		"version": 1, "gh-aw-version": "v0.92.0",
		"control-plane": map[string]any{
			"scope":     map[string]any{"allowed-owners": []string{"org"}, "allowed-repositories": []string{"org/control"}},
			"campaigns": map[string]any{},
		},
	}
}

func nativeSetupTestSave(t *testing.T, policy map[string]any) {
	t.Helper()
	for _, name := range append(append([]string{}, setupRuntimeFiles...), nativeRuntimeRequiredFiles...) {
		writeTestFile(t, name, "installed runtime: "+name)
	}
	if err := os.MkdirAll(filepath.Dir(policyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func nativeSetupTestCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "setup"}
	cmd.SetContext(context.Background())
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	return cmd
}

func TestNativeSetupBoundedPolicy(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(map[string]any)
		want bool
	}{
		{"exact", func(map[string]any) {}, true},
		{"absent scope", func(c map[string]any) { delete(c, "scope") }, true},
		{"case insensitive", func(c map[string]any) {
			c["scope"] = map[string]any{"allowed-owners": []string{"ORG"}, "allowed-repositories": []string{"ORG/Control"}}
		}, true},
		{"wildcard", func(c map[string]any) { nativeSetupMap(c["scope"])["allowed-repositories"] = []string{"org/*"} }, false},
		{"additional repo", func(c map[string]any) {
			nativeSetupMap(c["scope"])["allowed-repositories"] = []string{"org/control", "org/target"}
		}, false},
		{"unknown scope", func(c map[string]any) { nativeSetupMap(c["scope"])["future-authority"] = true }, false},
		{"broad activity", func(c map[string]any) { c["activity"] = map[string]any{"repositories": []string{"org/target"}} }, false},
		{"malformed activity", func(c map[string]any) { c["activity"] = nil }, false},
		{"campaign", func(c map[string]any) { c["campaigns"] = map[string]any{"repo-assist": map[string]any{}} }, false},
		{"standalone workflow", func(c map[string]any) { c["workflows"] = map[string]any{"audit": map[string]any{}} }, false},
		{"empty standalone workflows", func(c map[string]any) { c["workflows"] = map[string]any{} }, true},
		{"malformed standalone workflows", func(c map[string]any) { c["workflows"] = nil }, false},
		{"direct operation", func(c map[string]any) { c["operations"] = map[string]any{"audit": map[string]any{}} }, false},
		{"direct operation with empty workflows", func(c map[string]any) {
			c["workflows"] = map[string]any{}
			c["operations"] = map[string]any{"audit": map[string]any{}}
		}, false},
		{"empty operations", func(c map[string]any) { c["operations"] = map[string]any{} }, true},
		{"malformed operations", func(c map[string]any) { c["operations"] = nil }, false},
		{"missing campaigns", func(c map[string]any) { delete(c, "campaigns") }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := nativeSetupTestPolicy()
			test.edit(nativeSetupMap(policy["control-plane"]))
			if err := nativeSetupBoundedPolicy(policy, "org/control"); (err == nil) != test.want {
				t.Fatalf("valid = %v, want %v: %v", err == nil, test.want, err)
			}
		})
	}
}

func TestNativeSetupNonInteractive(t *testing.T) {
	for _, failure := range []string{"", "mode"} {
		t.Run("failure="+failure, func(t *testing.T) {
			nativeSetupTestDirectory(t)
			policy := nativeSetupTestPolicy()
			control := nativeSetupMap(policy["control-plane"])
			control["activity"] = map[string]any{"repositories": []string{"org/control"}, "retention-days": 31}
			nativeSetupTestSave(t, policy)
			before, _ := os.ReadFile(policyPath)
			calls := []string{}
			gh := func(_ context.Context, args ...string) (string, string, error) {
				call := strings.Join(args, " ")
				calls = append(calls, call)
				switch {
				case call == "auth status":
					return "", "", nil
				case call == "repo view --json nameWithOwner --jq .nameWithOwner":
					return "org/control", "", nil
				case call == "repo view org/control --json nameWithOwner,visibility":
					return `{"nameWithOwner":"org/control","visibility":"PUBLIC"}`, "", nil
				case strings.HasPrefix(call, "api repos/org/control/actions/"):
					if failure == "mode" {
						return `{"value":"app"}`, "", nil
					}
					return "", "", nil
				case strings.HasPrefix(call, "variable set "):
					if failure == "mode" {
						return "", "denied", errors.New("denied")
					}
					return "", "", nil
				}
				t.Fatalf("unexpected gh call %q", call)
				return "", "", nil
			}
			result, err := nativeSetupControl(nativeSetupTestCommand(), gh, nil)
			after, _ := os.ReadFile(policyPath)
			if failure != "" {
				if err == nil || !strings.Contains(err.Error(), "local policy was not changed") {
					t.Fatalf("missing partial-side-effect recovery: %v", err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("failed setup rewrote policy")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result["nonInteractive"] != true || result["profile"] != "workflow-token" {
				t.Fatalf("unexpected result: %#v", result)
			}
			saved, err := readPolicy()
			if err != nil {
				t.Fatal(err)
			}
			activity := nativeSetupMap(nativeSetupMap(saved["control-plane"])["activity"])
			if activity["retention-days"] != json.Number("31") {
				t.Fatal("Activity metadata was lost")
			}
			for _, call := range calls {
				if strings.Contains(call, "workflow run") || strings.Contains(call, "secret") || strings.HasPrefix(call, "variable ") {
					t.Fatalf("bounded setup unexpectedly dispatched work or changed secrets: %s", call)
				}
			}
		})
	}
}

func TestNativeSetupPublicMonitoring(t *testing.T) {
	for _, visibility := range []string{"PUBLIC", "PRIVATE", "INTERNAL"} {
		t.Run(visibility, func(t *testing.T) {
			nativeSetupTestDirectory(t)
			nativeSetupTestSave(t, nativeSetupTestPolicy())
			before, _ := os.ReadFile(policyPath)
			cmd := nativeSetupTestCommand()
			cmd.Flags().StringArray("repository", []string{"other/*"}, "")
			var mutations []string
			gh := func(_ context.Context, args ...string) (string, string, error) {
				call := strings.Join(args, " ")
				switch call {
				case "auth status":
					return "", "", nil
				case "repo view --json nameWithOwner --jq .nameWithOwner":
					return "org/control", "", nil
				case "repo view org/control --json nameWithOwner,visibility":
					return fmt.Sprintf(`{"nameWithOwner":"org/control","visibility":%q}`, visibility), "", nil
				case "repo list other --json nameWithOwner,visibility --limit 1000":
					return `[{"nameWithOwner":"other/public","visibility":"PUBLIC"},{"nameWithOwner":"other/private","visibility":"PRIVATE"},{"nameWithOwner":"other/internal","visibility":"INTERNAL"}]`, "", nil
				case "repo view other/public --json nameWithOwner,visibility":
					return `{"nameWithOwner":"other/public","visibility":"PUBLIC"}`, "", nil
				case `api repos/org/control/actions/variables?per_page=30 --paginate --jq .variables[] | select(.name == "GH_AW_GITHUB_AUTH_MODE") | {value}`,
					`api repos/org/control/actions/organization-variables?per_page=30 --paginate --jq .variables[] | select(.name == "GH_AW_GITHUB_AUTH_MODE") | {value}`:
					return "", "", nil
				case "variable set GH_AW_GITHUB_AUTH_MODE --repo org/control --body workflow-token":
					mutations = append(mutations, call)
					return "", "", nil
				}
				t.Fatalf("unexpected command (no App/PAT or target mutations allowed): %s", call)
				return "", "", nil
			}
			result, err := nativeSetupControl(cmd, gh, nil)
			if visibility != "PUBLIC" {
				after, _ := os.ReadFile(policyPath)
				if err == nil || len(mutations) != 0 || !bytes.Equal(before, after) {
					t.Fatalf("non-public setup did not fail before mutation: %v", err)
				}
				return
			}
			if err != nil || result["profile"] != "public-only" {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			want := []string{"org/control", "other/public"}
			if !reflect.DeepEqual(result["repositories"], want) {
				t.Fatalf("scope=%v want=%v", result["repositories"], want)
			}
			saved, err := readPolicy()
			if err != nil {
				t.Fatal(err)
			}
			control := nativeSetupMap(saved["control-plane"])
			if nativeSetupMap(control["activity"])["public-only"] != true || len(nativeSetupMap(control["campaigns"])) != 0 {
				t.Fatalf("wrong observation-only policy: %#v", control)
			}
			// Rerunning without scope arguments preserves and revalidates enrollment.
			repeated, err := nativeSetupControl(nativeSetupTestCommand(), gh, nil)
			if err != nil || !reflect.DeepEqual(repeated["repositories"], want) {
				t.Fatalf("repeat result=%#v err=%v", repeated, err)
			}
			guided, err := nativeSetupControl(nativeSetupTestCommand(), gh, &nativeSetupTestPrompt{
				texts: []string{"other/public"}, confirms: []bool{true},
			})
			if err != nil || guided["profile"] != "public-only" || !reflect.DeepEqual(guided["repositories"], want) {
				t.Fatalf("guided result=%#v err=%v", guided, err)
			}
		})
	}
}

type nativeSetupTestPrompt struct {
	texts    []string
	confirms []bool
	labels   []string
}

func (p *nativeSetupTestPrompt) Text(label string, _ string) (string, error) {
	p.labels = append(p.labels, label)
	if len(p.texts) == 0 {
		return "", errNativeSetupCancelled
	}
	answer := p.texts[0]
	p.texts = p.texts[1:]
	return answer, nil
}

func (p *nativeSetupTestPrompt) Confirm(string, bool) (bool, error) {
	if len(p.confirms) == 0 {
		return false, errNativeSetupCancelled
	}
	answer := p.confirms[0]
	p.confirms = p.confirms[1:]
	return answer, nil
}

func TestNativeSetupTerminalPresentation(t *testing.T) {
	for _, styled := range []bool{false, true} {
		t.Run(fmt.Sprintf("styled=%v", styled), func(t *testing.T) {
			var output bytes.Buffer
			prompt := &nativeSetupTerminalPrompt{
				reader: bufio.NewReader(strings.NewReader("\nIv1.read\n\n")),
				output: &output, styled: styled,
			}
			answer, err := prompt.Text("Repositories", "org/control")
			if err != nil || answer != "org/control" {
				t.Fatalf("default repository changed: %q, %v", answer, err)
			}
			answer, err = prompt.Text("Read App client ID", "")
			if err != nil || answer != "Iv1.read" {
				t.Fatalf("client ID changed: %q, %v", answer, err)
			}
			confirmed, err := prompt.Confirm("Apply this setup?", false)
			if err != nil || confirmed {
				t.Fatalf("confirmation no longer defaults to No: %v, %v", confirmed, err)
			}
			want := "Repositories [org/control]: Read App client ID: Apply this setup? (y/n) [n]: "
			if styled {
				want = "\x1b[36m?\x1b[0m Repositories\x1b[2m [org/control]\x1b[0m: " +
					"\x1b[36m?\x1b[0m Read App client ID: " +
					"\x1b[36m?\x1b[0m Apply this setup? (y/n)\x1b[2m [n]\x1b[0m: "
			}
			if output.String() != want {
				t.Fatalf("prompt output = %q, want %q", output.String(), want)
			}
		})
	}
	t.Setenv("GH_FORCE_TTY", "1")
	t.Setenv("CLICOLOR_FORCE", "1")
	cmd := nativeSetupTestCommand()
	if nativeSetupStyled(cmd) {
		t.Fatal("redirected output must stay plain even when color is forced")
	}
	if err := nativeSetupNote(cmd, "Control: %s\nMonitoring only.\n", "org/control"); err != nil {
		t.Fatal(err)
	}
	if cmd.ErrOrStderr().(*bytes.Buffer).String() != "Control: org/control\nMonitoring only.\n" {
		t.Fatal("plain notes contain unexpected formatting")
	}
}

func TestNativeSetupGuidedAuthenticationByVisibility(t *testing.T) {
	for _, test := range []struct {
		name, scope, controlVisibility, targetVisibility string
		lookupFailure, wantError, wantAuth               bool
	}{
		{name: "public control only", scope: "org/control", controlVisibility: "PUBLIC"},
		{name: "public exact", scope: "org/target", controlVisibility: "PUBLIC", targetVisibility: "PUBLIC"},
		{name: "public wildcard", scope: "org/*", controlVisibility: "PUBLIC", targetVisibility: "PUBLIC"},
		{name: "public multiple owners", scope: "other/*", controlVisibility: "PUBLIC", targetVisibility: "PUBLIC"},
		{name: "private control only", scope: "org/control", controlVisibility: "PRIVATE"},
		{name: "private control public exact", scope: "org/target", controlVisibility: "PRIVATE", targetVisibility: "PUBLIC"},
		{name: "private control public wildcard", scope: "org/*", controlVisibility: "PRIVATE", targetVisibility: "PUBLIC"},
		{name: "private control public multiple owners", scope: "other/*", controlVisibility: "PRIVATE", targetVisibility: "PUBLIC"},
		{name: "internal control public wildcard", scope: "org/*", controlVisibility: "INTERNAL", targetVisibility: "PUBLIC"},
		{name: "unknown control", scope: "org/target", controlVisibility: "UNKNOWN", targetVisibility: "PUBLIC", wantAuth: true},
		{name: "private exact", scope: "org/target", controlVisibility: "PRIVATE", targetVisibility: "PRIVATE", wantAuth: true},
		{name: "internal wildcard", scope: "org/*", controlVisibility: "PRIVATE", targetVisibility: "INTERNAL", wantAuth: true},
		{name: "unknown exact", scope: "org/target", controlVisibility: "PRIVATE", targetVisibility: "UNKNOWN", wantAuth: true},
		{name: "unknown wildcard", scope: "other/*", controlVisibility: "PRIVATE", targetVisibility: "UNKNOWN", wantAuth: true},
		{name: "missing exact visibility", scope: "org/target", controlVisibility: "PUBLIC", wantError: true},
		{name: "missing wildcard visibility", scope: "org/*", controlVisibility: "PUBLIC", wantError: true},
		{name: "failed exact lookup", scope: "org/target", controlVisibility: "PUBLIC", lookupFailure: true, wantError: true},
		{name: "failed wildcard lookup", scope: "org/*", controlVisibility: "PUBLIC", lookupFailure: true, wantError: true},
		{name: "public control private target", scope: "org/*", controlVisibility: "PUBLIC", targetVisibility: "PRIVATE", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			nativeSetupTestDirectory(t)
			nativeSetupTestSave(t, nativeSetupTestPolicy())
			before, err := os.ReadFile(policyPath)
			if err != nil {
				t.Fatal(err)
			}
			cmd := nativeSetupTestCommand()
			prompt := &nativeSetupTestPrompt{texts: []string{test.scope}, confirms: []bool{true}}
			if test.wantAuth {
				prompt.texts = append(prompt.texts, "token")
				prompt.confirms = []bool{false}
			}
			target := strings.Split(test.scope, "/")[0] + "/target"
			mutations := 0
			gh := func(_ context.Context, args ...string) (string, string, error) {
				call := strings.Join(args, " ")
				switch {
				case call == "auth status":
					return "", "", nil
				case call == "repo view --json nameWithOwner --jq .nameWithOwner":
					return "org/control", "", nil
				case call == "repo view org/control --json nameWithOwner,visibility":
					return fmt.Sprintf(`{"nameWithOwner":"org/control","visibility":%q}`, test.controlVisibility), "", nil
				case strings.HasPrefix(call, "repo list ") || strings.HasPrefix(call, "repo view "+target+" "):
					if test.lookupFailure {
						return "", "lookup denied", errors.New("denied")
					}
					document := fmt.Sprintf(`{"nameWithOwner":%q,"visibility":%q}`, target, test.targetVisibility)
					if args[1] == "list" {
						document = "[" + document + "]"
					}
					return document, "", nil
				case strings.HasPrefix(call, "api repos/org/control/actions/"):
					return "", "", nil
				case call == "variable set GH_AW_GITHUB_AUTH_MODE --repo org/control --body workflow-token":
					mutations++
					return "", "", nil
				}
				t.Fatalf("unexpected gh call: %s", call)
				return "", "", nil
			}
			result, err := nativeSetupControl(cmd, gh, prompt)
			if (err != nil) != test.wantError {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			authPrompted := len(prompt.labels) > 1
			if authPrompted != test.wantAuth {
				t.Fatalf("authentication prompted=%v want=%v: %v", authPrompted, test.wantAuth, prompt.labels)
			}
			if test.wantError || test.wantAuth {
				after, err := os.ReadFile(policyPath)
				if err != nil {
					t.Fatal(err)
				}
				if mutations != 0 || !bytes.Equal(before, after) {
					t.Fatal("failed or declined setup changed policy or credentials")
				}
				if test.wantAuth {
					appProfile := "github-app"
					if strings.HasPrefix(test.scope, "other/") {
						appProfile = "enterprise-app"
					}
					if prompt.labels[1] != "Authentication ("+appProfile+", approved-app, token)" {
						t.Fatalf("wrong authentication choices: %s", prompt.labels[1])
					}
				}
				return
			}
			if result["profile"] != "public-only" || mutations != 0 {
				t.Fatalf("public setup did not use workflow token: %#v", result)
			}
			wantRepos := []string{"org/control"}
			if test.scope != "org/control" {
				wantRepos = append(wantRepos, target)
			}
			saved, err := readPolicy()
			if err != nil {
				t.Fatal(err)
			}
			control := nativeSetupMap(saved["control-plane"])
			actualRepos, _ := nativeSetupStrings(nativeSetupMap(control["scope"])["allowed-repositories"])
			if !reflect.DeepEqual(actualRepos, wantRepos) || nativeSetupMap(control["activity"])["public-only"] != true {
				t.Fatalf("incorrect public monitoring policy: %#v", control)
			}
		})
	}
}

func TestNativeSetupDoesNotUsePages(t *testing.T) {
	for _, visibility := range []string{"PUBLIC", "PRIVATE"} {
		t.Run(visibility, func(t *testing.T) {
			nativeSetupTestDirectory(t)
			nativeSetupTestSave(t, nativeSetupTestPolicy())
			gh := func(_ context.Context, args ...string) (string, string, error) {
				call := strings.Join(args, " ")
				switch call {
				case "auth status":
					return "", "", nil
				case "repo view --json nameWithOwner --jq .nameWithOwner":
					return "org/control", "", nil
				case "repo view org/control --json nameWithOwner,visibility":
					return fmt.Sprintf(`{"nameWithOwner":"org/control","visibility":%q}`, visibility), "", nil
				case `api repos/org/control/actions/variables?per_page=30 --paginate --jq .variables[] | select(.name == "GH_AW_GITHUB_AUTH_MODE") | {value}`,
					`api repos/org/control/actions/organization-variables?per_page=30 --paginate --jq .variables[] | select(.name == "GH_AW_GITHUB_AUTH_MODE") | {value}`:
					return "", "", nil
				default:
					t.Fatalf("setup must not inspect or configure Pages: %s", call)
					return "", "", nil
				}
			}
			cmd := nativeSetupTestCommand()
			prompt := &nativeSetupTestPrompt{texts: []string{"org/control"}, confirms: []bool{true}}
			result, err := nativeSetupControl(cmd, gh, prompt)
			if err != nil || result["profile"] != "public-only" || result["cancelled"] == true {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			output := string(encoded) + cmd.ErrOrStderr().(*bytes.Buffer).String()
			for _, obsolete := range []string{"Pages", "pages", "dashboardURL", ".github.io"} {
				if strings.Contains(output, obsolete) {
					t.Fatalf("setup retains obsolete Pages output %q: %s", obsolete, output)
				}
			}
		})
	}
}

func TestNativeSetupTokenDryRun(t *testing.T) {
	nativeSetupTestDirectory(t)
	policy := nativeSetupTestPolicy()
	nativeSetupMap(nativeSetupMap(policy["control-plane"])["scope"])["allowed-repositories"] = []string{"org/control", "other/target", "org/read-only"}
	options := nativeSetupAuthOptions{repo: "org/control", write: []string{"third/reviews"}, expires: "30", dryRun: true}
	gh := func(context.Context, ...string) (string, string, error) {
		t.Fatal("dry run called GitHub")
		return "", "", nil
	}
	result, err := nativeSetupAuthenticate(nativeSetupTestCommand(), gh, "token", options, policy)
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"command":"setup-auth","profile":"fine-grained-token","secrets":[{"owner":"org","role":"read","secret":"GH_AW_GITHUB_READ_PAT_ORG"},{"owner":"other","role":"read","secret":"GH_AW_GITHUB_READ_PAT_OTHER"},{"owner":"third","role":"write","secret":"GH_AW_GITHUB_WRITE_PAT_THIRD"}],"repo":"org/control","repositories":{"read":{"org/control":"GH_AW_GITHUB_READ_PAT_ORG","org/read-only":"GH_AW_GITHUB_READ_PAT_ORG","other/target":"GH_AW_GITHUB_READ_PAT_OTHER"},"write":{"third/reviews":"GH_AW_GITHUB_WRITE_PAT_THIRD"}}}`
	actual, _ := json.Marshal(result)
	var actualValue, expectedValue any
	if err := json.Unmarshal(actual, &actualValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(expected), &expectedValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actualValue, expectedValue) {
		t.Fatalf("dry-run contract changed: %s", actual)
	}
	for _, invalid := range []string{"0", "367", "01", "+30", "30.0", " 30"} {
		options.expires = invalid
		if _, err := nativeSetupAuthenticate(nativeSetupTestCommand(), gh, "token", options, policy); err == nil {
			t.Fatalf("accepted expiration %q", invalid)
		}
	}
	nativeSetupMap(nativeSetupMap(policy["control-plane"])["scope"])["allowed-repositories"] = []string{"org/*"}
	if _, err := nativeSetupAuthenticate(nativeSetupTestCommand(), gh, "token", options, policy); err == nil {
		t.Fatal("wildcard credential reach accepted")
	}
}

func TestNativeSetupAppDryRunAndCrossOwner(t *testing.T) {
	gh := func(context.Context, ...string) (string, string, error) {
		t.Fatal("dry run called GitHub")
		return "", "", nil
	}
	options := nativeSetupAuthOptions{repo: "org/control", write: []string{"org/reviews"}, dryRun: true}
	policy := nativeSetupTestPolicy()
	result, err := nativeSetupAuthenticate(nativeSetupTestCommand(), gh, "github-app", options, policy)
	if err != nil {
		t.Fatal(err)
	}
	apps := result["apps"].([]map[string]any)
	if len(apps) != 2 || apps[0]["secret"] != "GH_AW_GITHUB_READ_APP_PRIVATE_KEY" {
		t.Fatalf("wrong App result: %#v", result)
	}
	manifest := apps[0]["manifest"].(map[string]any)
	if manifest["public"] != false || manifest["redirect_url"] != "http://127.0.0.1:0/callback" || manifest["request_oauth_on_install"] != false {
		t.Fatalf("unsafe manifest: %#v", manifest)
	}
	permissions := manifest["default_permissions"].(map[string]string)
	for _, level := range permissions {
		if level != "read" {
			t.Fatal("read App has write permission")
		}
	}
	options.write = []string{"other/reviews"}
	if _, err := nativeSetupAuthenticate(nativeSetupTestCommand(), gh, "github-app", options, policy); err == nil {
		t.Fatal("new organization App accepted external target")
	}
	options.readClient, options.writeClient = "Iv1.read", "Iv1.write"
	result, err = nativeSetupAuthenticate(nativeSetupTestCommand(), gh, "github-app", options, policy)
	if err != nil {
		t.Fatal(err)
	}
	if result["apps"].([]map[string]any)[0]["clientId"] != "Iv1.read" {
		t.Fatal("existing App selection not preserved")
	}
	options.writeClient = options.readClient
	if _, err := nativeSetupAuthenticate(nativeSetupTestCommand(), gh, "github-app", options, policy); err == nil {
		t.Fatal("same read/write client accepted")
	}
}

func TestNativeSetupTokenModeOnlyAfterAllSecretsAndMaps(t *testing.T) {
	nativeSetupTestDirectory(t)
	for _, fail := range []bool{false, true} {
		cmd := nativeSetupTestCommand()
		events := []string{}
		cmd.SetContext(context.WithValue(cmd.Context(), nativeSetupExecKey{}, nativeSetupExecFunc(func(_ *cobra.Command, name string, input io.Reader, args ...string) (string, error) {
			if name != "gh" || args[0] != "secret" || input != nil {
				t.Fatalf("invalid credential invocation: %s %v", name, args)
			}
			events = append(events, "secret:"+args[2])
			if fail && strings.Contains(args[2], "WRITE") {
				return "", errors.New("simulated private error")
			}
			return "", nil
		})))
		gh := func(_ context.Context, args ...string) (string, string, error) {
			if args[0] == "variable" {
				events = append(events, args[2])
			}
			return "", "", nil
		}
		_, err := nativeSetupAuthenticate(cmd, gh, "token", nativeSetupAuthOptions{repo: "org/control", replace: true, noOpen: true}, nativeSetupTestPolicy())
		if fail {
			if err == nil || strings.Contains(err.Error(), "simulated private error") {
				t.Fatalf("secret failure not sanitized: %v", err)
			}
			if len(events) != 2 {
				t.Fatalf("mode/maps changed before credentials completed: %v", events)
			}
		} else if err != nil || len(events) != 5 || events[len(events)-1] != "GH_AW_GITHUB_AUTH_MODE" {
			t.Fatalf("credential ordering %v: %v", events, err)
		}
	}
}

func nativeSetupTestKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

func TestNativeSetupAppAuthenticationAndInstallationScope(t *testing.T) {
	key := nativeSetupTestKey(t)
	profile := nativeSetupCredentials(false)[0]
	selection := "selected"
	permissions := profile.Permissions
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer ") {
			t.Error("missing bearer credential")
		}
		switch req.URL.Path {
		case "/app":
			nativeSetupTestJSON(t, w, map[string]any{"id": 123, "client_id": "Iv1.read", "slug": "read-app", "public": false, "permissions": permissions})
		case "/orgs/org/installation":
			nativeSetupTestJSON(t, w, map[string]any{"id": 456, "app_id": 123, "account": map[string]string{"login": "ORG"}, "repository_selection": selection})
		case "/app/installations/456/access_tokens":
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			if !reflect.DeepEqual(body, map[string]any{"permissions": map[string]any{"metadata": "read"}}) {
				t.Errorf("verification token was not metadata-only: %#v", body)
			}
			nativeSetupTestJSON(t, w, map[string]any{"token": "ephemeral-test-token"})
		case "/installation/repositories":
			nativeSetupTestJSON(t, w, map[string]any{"total_count": 1, "repositories": []any{map[string]string{"full_name": "org/control"}}})
		default:
			t.Errorf("unexpected path %s", req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer server.Close()
	api := nativeSetupAppAPI{base: server.URL, client: server.Client()}
	app, err := nativeSetupAuthenticateApp(context.Background(), api, nativeSetupApp{ClientID: "Iv1.read", PEM: key}, profile)
	if err != nil {
		t.Fatal(err)
	}
	if installation, err := api.covers(context.Background(), app, nativeSetupOwnerGroup{Owner: "org", Repositories: []string{"control"}}); err != nil || installation == nil {
		t.Fatalf("exact installation was rejected: %v", err)
	}
	if installation, err := api.covers(context.Background(), app, nativeSetupOwnerGroup{Owner: "org", Repositories: []string{"missing"}}); err != nil || installation != nil {
		t.Fatalf("missing repository was accepted: %v", err)
	}
	selection = "all"
	if _, err := api.covers(context.Background(), app, nativeSetupOwnerGroup{Owner: "org", Repositories: []string{"control"}}); !errors.Is(err, errNativeSetupInstallationScope) {
		t.Fatalf("all-repository installation accepted: %v", err)
	}
	permissions = map[string]string{}
	for permission, level := range profile.Permissions {
		permissions[permission] = level
	}
	permissions["contents"] = "write"
	if _, err := nativeSetupAuthenticateApp(context.Background(), api, nativeSetupApp{ClientID: "Iv1.read", PEM: key}, profile); err == nil {
		t.Fatal("read App with write permission accepted")
	}
	jwt, err := nativeSetupAppJWT(app, time.Unix(1_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	claims, _ := base64.RawURLEncoding.DecodeString(strings.Split(jwt, ".")[1])
	if !strings.Contains(string(claims), `"iss":"Iv1.read"`) || !strings.Contains(string(claims), `"iat":999940`) {
		t.Fatalf("invalid JWT claims: %s", claims)
	}
}

func TestNativeSetupWildcardExpansionFailsClosed(t *testing.T) {
	cmd := nativeSetupTestCommand()
	gh := func(_ context.Context, args ...string) (string, string, error) {
		call := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(call, "api /user/orgs"):
			return `[{"login":"org"},{"login":"other"}]`, "", nil
		case strings.HasPrefix(call, "repo list org"):
			return `[{"nameWithOwner":"org/control","visibility":"PUBLIC"},{"nameWithOwner":"org/target","visibility":"PRIVATE"}]`, "", nil
		default:
			return "", "", errors.New("unexpected GitHub call")
		}
	}

	result, err := nativeSetupExpand(cmd, gh, nativeSetupRepository{Repository: "org/control", Visibility: "public"}, "o*g/*")
	if err != nil || len(result) != 2 || result[1].Visibility != "private" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	for _, input := range []string{"missing/*", "org/no-match*", "org/../../escape", "org/?"} {
		if _, err := nativeSetupExpand(cmd, gh, nativeSetupRepository{Repository: "org/control"}, input); err == nil {
			t.Fatalf("unsafe/empty expansion accepted: %s", input)
		}
	}
}

func TestNativeSetupAppManifestBrowserProtocol(t *testing.T) {
	for _, test := range []struct {
		name, owner, appName, homepage, host string
		invalid                              bool
	}{
		{"valid", "org", "test-app", "https://github.com/org/control", "github.com", false},
		{"invalid state", "org", "test-app", "https://github.com/org/control", "github.com", true},
		{"quoted HTML and JSON", "org", `app"></form><script>alert("manifest")</script><input name="injected" value="`, "https://github.com/org/control?q=\"'<>&\\\n\u2028\u2029", "github.com", false},
		{"quoted action URL", `org"><script>alert("action")</script><input name="injected`, "test-app", "https://github.example.test/org/control", "github.example.test", false},
		{"data residency", "org", "test-app", "https://contoso.ghe.com/org/control", "contoso.ghe.com", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GH_HOST", test.host)
			cmd := nativeSetupTestCommand()
			key := nativeSetupTestKey(t)
			conversions := 0
			gh := func(_ context.Context, args ...string) (string, string, error) {
				if args[0] == "browse" {
					response, err := http.Get(args[1])
					if err != nil {
						t.Fatal(err)
					}
					page, err := io.ReadAll(response.Body)
					if closeErr := response.Body.Close(); closeErr != nil {
						t.Fatal(closeErr)
					}
					if err != nil {
						t.Fatal(err)
					}
					action := regexp.MustCompile(`action="([^"]+)"`).FindStringSubmatch(string(page))
					manifest := regexp.MustCompile(`name="manifest" value="([^"]+)"`).FindStringSubmatch(string(page))
					if len(action) != 2 || len(manifest) != 2 {
						t.Fatalf("browser registration form missing: %s", page)
					}
					for _, element := range []string{"<form ", "<input ", "<script>"} {
						if strings.Count(string(page), element) != 1 {
							t.Fatalf("manifest escaped its HTML attribute: %s", page)
						}
					}
					parsed, err := url.Parse(html.UnescapeString(action[1]))
					if err != nil || parsed.Path != "/organizations/"+test.owner+"/settings/apps/new" {
						t.Fatalf("invalid registration URL: %v", parsed)
					}
					var body map[string]any
					if err := json.Unmarshal([]byte(html.UnescapeString(manifest[1])), &body); err != nil {
						t.Fatal(err)
					}
					if body["public"] != false || nativeSetupMap(body["hook_attributes"])["active"] != false {
						t.Fatalf("unsafe manifest: %#v", body)
					}
					if body["name"] != test.appName || body["url"] != test.homepage || nativeSetupMap(body["hook_attributes"])["url"] != test.homepage {
						t.Fatalf("manifest did not round-trip HTML and JSON encoding: %#v", body)
					}
					callback := body["redirect_url"].(string)
					state := parsed.Query().Get("state")
					if len(state) != 32 {
						t.Fatal("manifest callback has no unpredictable state")
					}
					if test.invalid {
						state = "invalid-state"
					}
					client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
					result, err := client.Get(callback + "?code=manifest-code&state=" + state)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := result.Body.Close(); err != nil {
							t.Error(err)
						}
					}()
					expected := http.StatusFound
					if test.invalid {
						expected = http.StatusBadRequest
					}
					if result.StatusCode != expected {
						t.Fatalf("callback status %d, expected %d", result.StatusCode, expected)
					}
					if !test.invalid {
						location, err := result.Location()
						if err != nil {
							t.Fatal(err)
						}
						if test.host == "github.com" {
							if location.Host != "github.com" || location.Path != "/apps/read-app/installations/new/permissions" ||
								!reflect.DeepEqual(location.Query(), url.Values{"suggested_target_id": {"42"}, "repository_ids[]": {"101", "102"}}) {
								t.Fatalf("creation callback lost repository preselection: %s", location)
							}
						} else {
							path := "/apps/read-app/installations/new"
							if strings.HasSuffix(test.host, ".ghe.com") {
								path = "/organizations/org/settings/apps/read-app/installations"
							}
							if location.Host != test.host || location.Path != path || location.RawQuery != "" {
								t.Fatalf("unsupported-host installation URL changed: %s", location)
							}
						}
					}
					return "", "", nil
				}
				if test.host == "github.com" && len(args) == 2 && args[0] == "api" {
					switch args[1] {
					case "/repos/org/control":
						return `{"id":101,"full_name":"org/control","owner":{"id":42,"login":"org"}}`, "", nil
					case "/repos/org/evidence":
						return `{"id":102,"full_name":"org/evidence","owner":{"id":42,"login":"org"}}`, "", nil
					}
				}
				if strings.Join(args, " ") == "api -X POST -H Accept: application/vnd.github+json /app-manifests/manifest-code/conversions" {
					conversions++
					data, _ := json.Marshal(map[string]any{"id": 123, "client_id": "Iv1.read", "pem": key, "slug": "read-app"})
					return string(data), "", nil
				}
				t.Fatalf("unexpected gh operation: %v", args)
				return "", "", nil
			}
			target := nativeSetupOwnerGroup{Owner: test.owner, Repositories: []string{"control", "evidence"}}
			app, err := nativeSetupCreateApp(cmd, gh, target, test.appName, test.homepage, nativeSetupCredentials(false)[0], false)
			if test.invalid {
				if err == nil || conversions != 0 {
					t.Fatalf("invalid callback exchanged a code: %v conversions=%d", err, conversions)
				}
			} else if err != nil || app.PEM != key || conversions != 1 {
				t.Fatalf("manifest flow did not return credentials: %v", err)
			}
			if strings.Contains(cmd.ErrOrStderr().(*bytes.Buffer).String(), key) || strings.Contains(cmd.OutOrStdout().(*bytes.Buffer).String(), key) {
				t.Fatal("private key leaked to setup output")
			}
		})
	}
}

func TestNativeSetupRefusesOperationsBeforeMutation(t *testing.T) {
	for _, workflows := range []bool{false, true} {
		for _, guided := range []bool{false, true} {
			t.Run(fmt.Sprintf("workflows=%t/guided=%t", workflows, guided), func(t *testing.T) {
				nativeSetupTestDirectory(t)
				policy := nativeSetupTestPolicy()
				control := nativeSetupMap(policy["control-plane"])
				control["operations"] = map[string]any{"audit": map[string]any{"enabled": false}}
				if workflows {
					control["workflows"] = map[string]any{}
				}
				nativeSetupTestSave(t, policy)
				before, err := os.ReadFile(policyPath)
				if err != nil {
					t.Fatal(err)
				}
				gh := func(_ context.Context, args ...string) (string, string, error) {
					switch strings.Join(args, " ") {
					case "auth status":
						return "", "", nil
					case "repo view --json nameWithOwner --jq .nameWithOwner":
						return "org/control", "", nil
					case "repo view org/control --json nameWithOwner,visibility":
						return `{"nameWithOwner":"org/control","visibility":"PUBLIC"}`, "", nil
					default:
						t.Fatalf("setup reached a mutation or unexpected read: %v", args)
						return "", "", nil
					}
				}
				var prompt nativeSetupPrompt
				if guided {
					prompt = &nativeSetupTestPrompt{}
				}
				_, err = nativeSetupControl(nativeSetupTestCommand(), gh, prompt)
				if err == nil || !strings.Contains(err.Error(), "no declared operations") {
					t.Fatalf("expected operation refusal, got %v", err)
				}
				after, err := os.ReadFile(policyPath)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("setup changed policy: %v", err)
				}
			})
		}
	}
}

func TestNativeSetupEnterpriseDryRunAndCredentialFailure(t *testing.T) {
	options := nativeSetupAuthOptions{repo: "org/control", readClient: "Iv1.read", writeClient: "Iv1.write", write: []string{"reviews/output"}, dryRun: true}
	cmd := nativeSetupTestCommand()
	result, err := nativeSetupAuthenticate(cmd, func(context.Context, ...string) (string, string, error) {
		t.Fatal("dry run invoked GitHub")
		return "", "", nil
	}, "enterprise-app", options, nil)
	if err != nil {
		t.Fatal(err)
	}
	credentials := result["credentials"].([]map[string]any)
	if len(credentials) != 2 || credentials[0]["clientId"] != "Iv1.read" || credentials[1]["secret"] != "GH_AW_GITHUB_WRITE_APP_PRIVATE_KEY" {
		t.Fatalf("enterprise dry-run shape: %#v", result)
	}
	options.dryRun = false
	modeSelected := false
	cmd.SetContext(context.WithValue(cmd.Context(), nativeSetupExecKey{}, nativeSetupExecFunc(func(*cobra.Command, string, io.Reader, ...string) (string, error) {
		return "", errors.New("do not expose private prompt error")
	})))
	_, err = nativeSetupAuthenticate(cmd, func(_ context.Context, args ...string) (string, string, error) {
		if strings.Contains(strings.Join(args, " "), "GH_AW_GITHUB_AUTH_MODE") {
			modeSelected = true
		}
		return "", "", nil
	}, "enterprise-app", options, nil)
	if err == nil || modeSelected || strings.Contains(err.Error(), "do not expose") {
		t.Fatalf("credential failure leaked detail or changed mode: %v mode=%v", err, modeSelected)
	}
}

func TestNativeSetupGuidedTokenAppliesOnlyConsentedScope(t *testing.T) {
	nativeSetupTestDirectory(t)
	nativeSetupTestSave(t, nativeSetupTestPolicy())
	cmd := nativeSetupTestCommand()
	secretCalls := 0
	cmd.SetContext(context.WithValue(cmd.Context(), nativeSetupExecKey{}, nativeSetupExecFunc(func(_ *cobra.Command, name string, input io.Reader, args ...string) (string, error) {
		if name != "gh" || strings.Join(args[:2], " ") != "secret set" || input != nil {
			t.Fatalf("unexpected secure invocation: %s %v", name, args)
		}
		secretCalls++
		return "", nil
	})))
	writeMap := ""
	gh := func(_ context.Context, args ...string) (string, string, error) {
		call := strings.Join(args, " ")
		switch {
		case call == "auth status", strings.HasPrefix(call, "browse "):
			return "", "", nil
		case call == "repo view --json nameWithOwner --jq .nameWithOwner":
			return "org/control", "", nil
		case call == "repo view org/control --json nameWithOwner,visibility":
			return `{"nameWithOwner":"org/control","visibility":"PRIVATE"}`, "", nil
		case call == "repo view other/target --json nameWithOwner,visibility":
			return `{"nameWithOwner":"other/target","visibility":"PRIVATE"}`, "", nil
		case call == "secret list --repo org/control --json name":
			return "[]", "", nil
		case strings.HasPrefix(call, "variable set "):
			if args[2] == "GH_AW_GITHUB_WRITE_PAT_REPOSITORIES" {
				writeMap = args[len(args)-1]
			}
			return "", "", nil
		default:
			t.Fatalf("unexpected gh operation: %s", call)
			return "", "", nil
		}
	}
	prompt := &nativeSetupTestPrompt{texts: []string{"other/target", "token"}, confirms: []bool{true}}
	result, err := nativeSetupControl(cmd, gh, prompt)
	if err != nil {
		t.Fatal(err)
	}
	if result["profile"] != "token" || secretCalls != 3 {
		t.Fatalf("guided result=%#v secrets=%d", result, secretCalls)
	}
	if writeMap != `{"org/control":"GH_AW_GITHUB_WRITE_PAT_ORG"}` {
		t.Fatalf("read target was granted write credentials: %s", writeMap)
	}
	policy, err := readPolicy()
	if err != nil {
		t.Fatal(err)
	}
	control := nativeSetupMap(policy["control-plane"])
	repos, _ := nativeSetupStrings(nativeSetupMap(control["scope"])["allowed-repositories"])
	if !reflect.DeepEqual(repos, []string{"org/control", "other/target"}) {
		t.Fatalf("wrong approved read scope: %v", repos)
	}
	if len(nativeSetupMap(control["campaigns"])) != 0 {
		t.Fatal("guided setup installed a campaign")
	}
}
