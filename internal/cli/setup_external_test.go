package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func setupExternalTestArgs(provider string) []string {
	prefix := map[string]string{
		"jira":         "https://example.atlassian.net/rest/api/3/issue/",
		"linear":       "https://linear.app/acme/issue/",
		"ado":          "https://dev.azure.com/acme/_apis/wit/workItems/",
		"azure-devops": "https://dev.azure.com/acme/_apis/wit/workItems/",
	}[provider]
	args := []string{"setup", provider, "--gateway-url", "https://gateway.example/deployment/",
		"--source-id", "team-source", "--resource-url-prefix", prefix, "--scope", "native-scope",
		"--destination-repository-id", "11", "--destination-repository-id", "9007199254740991",
		"--expires-at", time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)}
	if provider == "ado" || provider == "azure-devops" {
		args = append(args, "--collection-id", "native-collection")
	}
	return args
}

func setupExternalReplace(args []string, flag, value string) []string {
	for i, arg := range args {
		if arg == "--"+flag {
			args[i+1] = value
			return args
		}
	}
	return append(args, "--"+flag, value)
}

func TestSetupExternalOffline(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "")
	for _, env := range []string{"GH_TOKEN", "GITHUB_TOKEN", "LINEAR_API_KEY", "JIRA_API_TOKEN", "AZURE_DEVOPS_EXT_PAT", "CAO_GATEWAY_SOURCE_TEAM_SOURCE"} {
		t.Setenv(env, "ambient-secret-never-read")
	}
	for _, provider := range []string{"jira", "linear", "ado", "azure-devops"} {
		t.Run(provider, func(t *testing.T) {
			root, stdout, stderr := testCommand(t, nil, nil)
			root.SetArgs(append(setupExternalTestArgs(provider), "--json"))
			if err := root.ExecuteContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			var report setupExternalReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			wantProvider := provider
			if provider == "azure-devops" {
				wantProvider = "ado"
			}
			if report.Provider != wantProvider || report.Status != "prepared-only" ||
				report.WebhookURL != "https://gateway.example/deployment/webhooks/team-source" ||
				len(report.ExternalSources) != 1 || len(report.Instructions) < 5 {
				t.Fatalf("unexpected report: %+v", report)
			}
			source := report.ExternalSources[0]
			if source.ID != "team-source" || source.Scope != "native-scope" ||
				source.SecretEnv != "CAO_GATEWAY_SOURCE_TEAM_SOURCE" || !source.ExpiresAt.After(time.Now()) ||
				!reflect.DeepEqual(source.DestinationRepositoryIDs, []int64{11, 9007199254740991}) {
				t.Fatalf("unexpected enrollment: %+v", source)
			}
			wantEvent := map[string]string{"jira": "jira_issue", "linear": "linear_issue", "ado": "azure_devops_issue"}[wantProvider]
			if source.Event != wantEvent || (source.CollectionID != "") != (wantProvider == "ado") {
				t.Fatalf("incorrect provider binding: %+v", source)
			}
			wantAuth := map[string][]string{
				"jira":   {"X-Hub-Signature: sha256=<hex>", "X-Atlassian-Webhook-Identifier", "jira:issue_updated", "trigger jira_issue with its project filter"},
				"linear": {"Linear-Signature", "Linear-Delivery", "webhookTimestamp", "one minute", "Issue events", "trigger linear_issue with its organization-id filter"},
				"ado":    {"workitem.created", "workitem.updated", "workitem.deleted", "username cao", "body id", "do not claim native HMAC", "trigger azure_devops_issue with its project_id filter"},
			}[wantProvider]
			for _, want := range append(wantAuth, "32-512", "secret manager", "managed discovery", "Restart", "no authority", "v0.92.0", "Never reuse", `filter set exactly to "native-scope"`) {
				text := strings.Join(report.Instructions, "\n") + report.OutboundSafeOutputs.VerifiedVersion + report.OutboundSafeOutputs.Instructions
				if !strings.Contains(text, want) {
					t.Errorf("report omits %q", want)
				}
			}
			if strings.Contains(stdout.String()+stderr.String(), "ambient-secret-never-read") || stderr.Len() != 0 {
				t.Fatal("secret leaked or unexpected stderr")
			}
		})
	}
	entries, err := os.ReadDir(".")
	if err != nil || len(entries) != 0 {
		t.Fatalf("offline preparation modified the checkout: %v %v", entries, err)
	}
}

func TestSetupExternalHumanAndHelp(t *testing.T) {
	t.Setenv("PATH", "")
	for _, provider := range []string{"jira", "linear", "ado", "azure-devops"} {
		for _, help := range []bool{false, true} {
			t.Run(provider+"/help="+strconv.FormatBool(help), func(t *testing.T) {
				root, stdout, _ := testCommand(t, nil, nil)
				args := setupExternalTestArgs(provider)
				if help {
					args = []string{"setup", provider, "--help"}
				}
				root.SetArgs(args)
				if err := root.ExecuteContext(t.Context()); err != nil {
					t.Fatal(err)
				}
				wants := []string{"prepared only", "Webhook URL:", "secret manager", "Separate outbound safe outputs", "Use --json"}
				if help {
					wants = []string{"--gateway-url", "--source-id", "--scope", "--resource-url-prefix", "--destination-repository-id", "--expires-at",
						"--secret-env", "--json", "--output", "--dry-run", "32-512", "does not contact", "not configured"}
				}
				for _, want := range wants {
					if !strings.Contains(stdout.String(), want) {
						t.Errorf("output omits %q: %s", want, stdout)
					}
				}
				if !help {
					trigger := map[string]string{
						"jira":         "trigger jira_issue with its project filter",
						"linear":       "trigger linear_issue with its organization-id filter",
						"ado":          "trigger azure_devops_issue with its project_id filter",
						"azure-devops": "trigger azure_devops_issue with its project_id filter",
					}[provider]
					if !strings.Contains(stdout.String(), trigger+` set exactly to "native-scope"`) {
						t.Fatal("human guide omits the exact scoped gh-aw trigger")
					}
					marker := "Enrollment JSON (merge into reviewed gateway policy; preserve existing entries):\n"
					_, fragment, found := strings.Cut(stdout.String(), marker)
					if !found {
						t.Fatal("human guide omits enrollment JSON")
					}
					var enrollment struct {
						ExternalSources []setupExternalSource `json:"externalSources"`
					}
					if err := json.Unmarshal([]byte(fragment), &enrollment); err != nil {
						t.Fatalf("human guide enrollment JSON is not valid: %v", err)
					}
					if len(enrollment.ExternalSources) != 1 || enrollment.ExternalSources[0].Scope != "native-scope" {
						t.Fatalf("human enrollment binding lost: %+v", enrollment)
					}
				}
			})
		}
	}
}

func TestSetupExternalRejectsMalformedInputs(t *testing.T) {
	for _, tt := range []struct{ provider, flag, value string }{
		{"jira", "gateway-url", "http://gateway.example"},
		{"jira", "gateway-url", "******gateway.example"},
		{"jira", "gateway-url", "https://gateway.example?"},
		{"jira", "gateway-url", "https://gateway.example?token=secret"},
		{"jira", "gateway-url", "https://gateway.example/#"},
		{"jira", "gateway-url", "https://gateway.example/#fragment"},
		{"jira", "gateway-url", "https://gateway.example:"},
		{"jira", "gateway-url", "https://gateway.example:65536"},
		{"jira", "gateway-url", "https://gateway.example:0"},
		{"jira", "gateway-url", "https://bad_host"},
		{"jira", "gateway-url", "https:///path"},
		{"jira", "gateway-url", "https://gateway.example//path"},
		{"jira", "gateway-url", "https://gateway.example/a/../b"},
		{"jira", "gateway-url", "https://gateway.example/a%2fb"},
		{"jira", "gateway-url", "https://gateway.example/%0a"},
		{"jira", "gateway-url", " https://gateway.example"},
		{"jira", "gateway-url", "https://gateway.example\\evil"},
		{"jira", "gateway-url", "https://GATEWAY.example"},
		{"jira", "source-id", "compiler"}, {"jira", "source-id", "github"},
		{"jira", "source-id", "."}, {"jira", "source-id", ".."},
		{"jira", "source-id", "a/b"}, {"jira", "source-id", "a%2fb"},
		{"jira", "source-id", ""}, {"jira", "source-id", strings.Repeat("a", 65)},
		{"jira", "source-id", "name previous"}, {"jira", "source-id", "name\n"},
		{"jira", "resource-url-prefix", "https://example.atlassian.net/browse/"},
		{"jira", "resource-url-prefix", "https://example.atlassian.net/rest/api/3/issue"},
		{"jira", "resource-url-prefix", "https://example.atlassian.net:443/rest/api/3/issue/"},
		{"jira", "resource-url-prefix", "https://example.atlassian.net/rest/api/3/issue/../"},
		{"jira", "resource-url-prefix", "******example.atlassian.net/rest/api/3/issue/"},
		{"jira", "resource-url-prefix", "https://example.atlassian.net/rest/api/3/issue/?token=x"},
		{"linear", "resource-url-prefix", "https://evil.example/acme/issue/"},
		{"linear", "resource-url-prefix", "https://linear.app/issue/"},
		{"linear", "resource-url-prefix", "https://linear.app/acme/issue/APP-1/"},
		{"ado", "resource-url-prefix", "https://dev.azure.com.evil/acme/_apis/wit/workItems/"},
		{"ado", "resource-url-prefix", "https://dev.azure.com/acme/_apis/wit/workItems/42/updates/"},
		{"jira", "scope", ""}, {"linear", "scope", " scope"}, {"jira", "scope", "a b"},
		{"jira", "scope", "a\tb"}, {"jira", "scope", "a\u0085b"},
		{"jira", "scope", strings.Repeat("a", 257)},
		{"ado", "collection-id", ""}, {"ado", "collection-id", "collection\n"},
		{"ado", "collection-id", "collection id"}, {"ado", "collection-id", strings.Repeat("a", 257)},
		{"jira", "secret-env", ""}, {"jira", "secret-env", "SECRET"},
		{"jira", "secret-env", "CAO_GATEWAY_SOURCE_lower"},
		{"jira", "secret-env", "CAO_GATEWAY_SOURCE_CURRENT_PREVIOUS"},
		{"jira", "secret-env", "CAO_GATEWAY_SOURCE_" + strings.Repeat("A", 111)},
		{"jira", "destination-repository-id", "0"}, {"jira", "destination-repository-id", "-1"},
		{"jira", "destination-repository-id", "+1"}, {"jira", "destination-repository-id", "01"},
		{"jira", "destination-repository-id", "1e3"}, {"jira", "destination-repository-id", "1.0"},
		{"jira", "destination-repository-id", "1,2"}, {"jira", "destination-repository-id", "1 "},
		{"jira", "destination-repository-id", "9007199254740992"},
		{"jira", "destination-repository-id", "99999999999999999999999999"},
		{"jira", "destination-repository-id", "9007199254740991"},
		{"jira", "destination-repository-id", "owner/repo"},
		{"jira", "expires-at", "2020-01-01T00:00:00Z"},
		{"jira", "expires-at", "tomorrow"}, {"jira", "expires-at", "2099-01-01"},
		{"jira", "expires-at", "2099-01-01T00:00:00Z\n"},
		{"jira", "output", ""}, {"jira", "output", " report.json"},
		{"jira", "output", "report\n.json"},
	} {
		t.Run(tt.provider+"/"+tt.flag+"/"+tt.value, func(t *testing.T) {
			args := setupExternalReplace(setupExternalTestArgs(tt.provider), tt.flag, tt.value)
			root, stdout, _ := testCommand(t, nil, nil)
			root.SetArgs(args)
			err := root.ExecuteContext(t.Context())
			if err == nil || !strings.Contains(err.Error(), "--"+tt.flag) || stdout.Len() != 0 {
				t.Fatalf("accepted invalid --%s: err=%v stdout=%s", tt.flag, err, stdout)
			}
		})
	}
}

func TestSetupExternalBoundsAndExplicitFlags(t *testing.T) {
	for _, provider := range []string{"jira", "linear", "ado"} {
		for _, flag := range []string{"gateway-url", "source-id", "resource-url-prefix", "scope", "destination-repository-id", "expires-at", "collection-id"} {
			if flag == "collection-id" && provider != "ado" {
				continue
			}
			args := setupExternalTestArgs(provider)
			var filtered []string
			for i := 0; i < len(args); i++ {
				if args[i] == "--"+flag {
					i++
				} else {
					filtered = append(filtered, args[i])
				}
			}
			root, stdout, _ := testCommand(t, nil, nil)
			root.SetArgs(filtered)
			if err := root.ExecuteContext(t.Context()); err == nil || stdout.Len() != 0 {
				t.Fatalf("%s accepted missing --%s", provider, flag)
			}
		}
	}
	for _, count := range []int{64, 65} {
		args := setupExternalTestArgs("jira")
		for i := 2; i < count; i++ {
			args = append(args, "--destination-repository-id", strconv.Itoa(100+i))
		}
		root, _, _ := testCommand(t, nil, nil)
		root.SetArgs(args)
		if err := root.ExecuteContext(t.Context()); (err == nil) != (count == 64) {
			t.Fatalf("count %d: %v", count, err)
		}
	}
	for _, flags := range [][]string{
		{"--secret", "must-not-accept"}, {"--token", "must-not-accept"},
		{"--password", "must-not-accept"}, {"--collection-id", "not-jira"},
		{"--destination-repository-id", "11"}, {"extra-argument"},
	} {
		root, stdout, _ := testCommand(t, nil, nil)
		root.SetArgs(append(setupExternalTestArgs("jira"), flags...))
		if err := root.ExecuteContext(t.Context()); err == nil || stdout.Len() != 0 {
			t.Fatalf("accepted invalid flags %v", flags)
		}
	}
}

func TestSetupExternalAcceptedURLShapes(t *testing.T) {
	for _, tt := range []struct{ provider, flag, value string }{
		{"jira", "gateway-url", "https://gateway.example"},
		{"jira", "gateway-url", "https://gateway.example/"},
		{"jira", "gateway-url", "https://gateway.example:443/nested/deployment"},
		{"jira", "gateway-url", "https://127.0.0.1:8443"},
		{"jira", "gateway-url", "https://[::1]:8443/"},
		{"jira", "resource-url-prefix", "https://jira.example:8443/rest/api/2/issue/"},
		{"ado", "resource-url-prefix", "https://dev.azure.com/acme/My%20Project/_apis/wit/workitems/"},
		{"ado", "resource-url-prefix", "https://acme.visualstudio.com/Project/_apis/wit/workItems/"},
		{"jira", "source-id", strings.Repeat("a", 64)},
		{"jira", "scope", strings.Repeat("a", 256)},
		{"jira", "secret-env", "CAO_GATEWAY_SOURCE_CUSTOM"},
	} {
		root, _, _ := testCommand(t, nil, nil)
		root.SetArgs(setupExternalReplace(setupExternalTestArgs(tt.provider), tt.flag, tt.value))
		if err := root.ExecuteContext(t.Context()); err != nil {
			t.Errorf("%s --%s %s: %v", tt.provider, tt.flag, tt.value, err)
		}
	}
}

func TestSetupExternalSecretDerivationAndExpiry(t *testing.T) {
	for _, tt := range []struct{ id, override, want string }{
		{"Mixed.Case-1", "", "CAO_GATEWAY_SOURCE_MIXED_CASE_1"},
		{"team_previous", "", ""},
		{"team_previous", "CAO_GATEWAY_SOURCE_CUSTOM", "CAO_GATEWAY_SOURCE_CUSTOM"},
	} {
		root, stdout, _ := testCommand(t, nil, nil)
		args := setupExternalReplace(setupExternalTestArgs("linear"), "source-id", tt.id)
		if tt.override != "" {
			args = append(args, "--secret-env", tt.override)
		}
		root.SetArgs(append(args, "--json"))
		err := root.ExecuteContext(t.Context())
		if tt.want == "" {
			if err == nil || !strings.Contains(err.Error(), "--secret-env") {
				t.Fatalf("accepted reserved derived secret name: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var report setupExternalReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if report.ExternalSources[0].SecretEnv != tt.want {
			t.Fatalf("derived secret name = %q, want %q", report.ExternalSources[0].SecretEnv, tt.want)
		}
	}
	cmd := setupExternalCommand("jira")
	now := time.Now().UTC().Truncate(time.Second)
	args := setupExternalReplace(setupExternalTestArgs("jira"), "expires-at", now.Format(time.RFC3339))[2:]
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareSetupExternal(cmd, "jira", now); err == nil {
		t.Fatal("accepted enrollment expiring exactly now")
	}
	if _, err := prepareSetupExternal(cmd, "jira", now.Add(-time.Second)); err != nil {
		t.Fatalf("rejected enrollment expiring in the future: %v", err)
	}
}

func TestSetupExternalFixtureBindings(t *testing.T) {
	data, err := os.ReadFile("../../specs/fixtures/webhook-external-deliveries-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Sources []setupExternalSource `json:"sources"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, source := range fixture.Sources {
		provider := map[string]string{"jira_issue": "jira", "linear_issue": "linear", "azure_devops_issue": "ado"}[source.Event]
		root, stdout, _ := testCommand(t, nil, nil)
		args := []string{"setup", provider, "--gateway-url", "https://gateway.example", "--source-id", source.ID,
			"--resource-url-prefix", source.ResourceURLPrefix, "--scope", source.Scope, "--secret-env", source.SecretEnv,
			"--expires-at", source.ExpiresAt.Format(time.RFC3339), "--destination-repository-id", "11", "--json"}
		if provider == "ado" {
			args = append(args, "--collection-id", source.CollectionID)
		}
		root.SetArgs(args)
		if err := root.ExecuteContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		var report setupExternalReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(report.ExternalSources, []setupExternalSource{source}) {
			t.Fatalf("fixture source changed: %+v", report.ExternalSources)
		}
	}
}

func TestSetupExternalOutputFile(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "report.json")
	for _, dry := range []bool{true, false} {
		root, stdout, _ := testCommand(t, nil, nil)
		args := append(setupExternalTestArgs("linear"), "--output", filename, "--json")
		if dry {
			args = append(args, "--dry-run")
		}
		root.SetArgs(args)
		if err := root.ExecuteContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		stat, err := os.Stat(filename)
		if dry {
			if !os.IsNotExist(err) {
				t.Fatalf("dry run created output: %v", err)
			}
			continue
		}
		if err != nil || stat.Mode().Perm() != 0o600 {
			t.Fatalf("report permissions: %v %v", stat, err)
		}
		data, err := os.ReadFile(filename)
		if err != nil || string(data) != stdout.String() {
			t.Fatalf("output differs from JSON report: %v", err)
		}
	}
	symlink := filepath.Join(dir, "symlink.json")
	if err := os.Symlink(filename, symlink); err != nil {
		t.Fatal(err)
	}
	for _, destination := range []string{filename, symlink, dir, filepath.Join(dir, "missing", "report.json")} {
		root, stdout, _ := testCommand(t, nil, nil)
		root.SetArgs(append(setupExternalTestArgs("ado"), "--output", destination))
		if err := root.ExecuteContext(t.Context()); err == nil || stdout.Len() != 0 {
			t.Fatalf("accepted nonexclusive/unwritable output %s: %v", destination, err)
		}
	}
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	root, _, _ := testCommand(t, nil, nil)
	root.SetArgs(append(setupExternalTestArgs("jira"), "--output", symlink, "--dry-run"))
	if err := root.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filename)
	if err != nil || string(before) != string(after) {
		t.Fatalf("existing report changed: %v", err)
	}
	invalid := filepath.Join(dir, "invalid.json")
	root, _, _ = testCommand(t, nil, nil)
	root.SetArgs(append(setupExternalReplace(setupExternalTestArgs("jira"), "scope", ""), "--output", invalid))
	if err := root.ExecuteContext(t.Context()); err == nil {
		t.Fatal("invalid input succeeded")
	}
	if _, err := os.Stat(invalid); !os.IsNotExist(err) {
		t.Fatalf("invalid input created a file: %v", err)
	}
}
