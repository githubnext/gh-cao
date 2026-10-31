package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func brokerSetupArgs() []string {
	return []string{"setup", "token-broker", "--url", "https://broker.example/api/token-broker",
		"--app-id", "123", "--installation-id", "456", "--owner-id", "789",
		"--key-vault-secret-url", "https://cao-vault.vault.azure.net/secrets/copilot-app",
		"--single-replica", "--single-process"}
}

func brokerSettings() map[string]string {
	return map[string]string{
		"CAO_TOKEN_BROKER_ENABLED": "true", "CAO_TOKEN_BROKER_SINGLE_PROCESS": "true",
		"CAO_TOKEN_BROKER_URL":    "https://broker.example/api/token-broker",
		"CAO_TOKEN_BROKER_APP_ID": "123", "CAO_TOKEN_BROKER_INSTALLATION_ID": "456",
		"CAO_TOKEN_BROKER_OWNER_ID":             "789",
		"CAO_TOKEN_BROKER_KEY_VAULT_SECRET_URL": "https://cao-vault.vault.azure.net/secrets/copilot-app",
	}
}

func brokerRule() map[string]any {
	return map[string]any{"repository-id": "321", "owner-id": "789",
		"workflow-ref": "org/repo/.github/workflows/activity.lock.yml@refs/heads/main",
		"workflow-sha": strings.Repeat("a", 40)}
}

func brokerPolicy(rules ...any) map[string]any {
	if rules == nil {
		rules = []any{}
	}
	return map[string]any{"version": 1, "gh-aw-version": "v0.92.0", "control-plane": map[string]any{
		"token-broker": map[string]any{"copilot-requests": map[string]any{"workflows": rules}},
		"web":          map[string]any{"host": map[string]any{"target": map[string]any{"module": "container", "replicas": 1}}},
	}}
}

func brokerWriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, string(content))
}

func brokerOfflineCommand(t *testing.T) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	fail := func() { t.Helper(); t.Fatal("offline token broker command made an external/runtime call") }
	root := newCommand("test", func(context.Context) (string, error) {
		fail()
		return "", errors.New("external")
	}, func(context.Context, ...string) (string, string, error) {
		fail()
		return "", "", errors.New("external")
	}, func(*cobra.Command, []string) error {
		fail()
		return errors.New("runtime")
	})
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(stderr)
	return root, stdout, stderr
}

func TestTokenBrokerSetupOffline(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "")
	t.Setenv("CAO_TOKEN_BROKER_APP_ID", "ambient-secret-sentinel")
	root, stdout, stderr := brokerOfflineCommand(t)
	root.SetArgs(brokerSetupArgs())
	if err := root.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, brokerSettings()) {
		t.Fatalf("settings = %#v", got)
	}
	if strings.Contains(stdout.String()+stderr.String(), "ambient-secret-sentinel") ||
		!strings.Contains(stderr.String(), "nothing deployed or verified") ||
		!strings.Contains(stderr.String(), "policy and scope are unchanged") {
		t.Fatal("unsafe output or missing handoff")
	}
	if entries, err := os.ReadDir("."); err != nil || len(entries) != 0 {
		t.Fatal("offline preparation changed checkout")
	}
}

func TestTokenBrokerHelpOffline(t *testing.T) {
	t.Setenv("PATH", "")
	for _, prefix := range [][]string{{"setup"}, {"doctor"}} {
		root, stdout, _ := brokerOfflineCommand(t)
		root.SetArgs(append(prefix, "token-broker", "--help"))
		if err := root.ExecuteContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"offline", "volatile", "policy"} {
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("help omitted %q", want)
			}
		}
	}
}

func TestTokenBrokerSetupInvalidAndExplicit(t *testing.T) {
	for _, tt := range []struct{ flag, value string }{
		{"--url", "http://broker.example/api/token-broker"},
		{"--url", "******broker.example/api/token-broker"},
		{"--url", "https://broker.example:443/api/token-broker"},
		{"--url", "https://broker.example:/api/token-broker"},
		{"--url", "https://broker.example/api/token-broker/"},
		{"--url", "https://broker.example/api/%74oken-broker"},
		{"--url", "https://broker.example/api/token-broker?token=secret-sentinel"},
		{"--url", "https://broker.example/api/token-broker?"},
		{"--url", "https://broker.example/api/token-broker#"},
		{"--url", "https://broker.example/api/token-broker#secret-sentinel"},
		{"--url", "https://broker.example\\evil/api/token-broker"},
		{"--url", "https://broker.example/api/token-broker\n"},
		{"--url", "https://broker.example/redirect"},
		{"--app-id", "0"}, {"--app-id", "-1"}, {"--app-id", "+1"}, {"--app-id", "01"},
		{"--app-id", "9223372036854775808"}, {"--installation-id", "1.2"},
		{"--owner-id", "owner"}, {"--owner-id", " 1"}, {"--owner-id", strings.Repeat("1", 21)},
		{"--key-vault-secret-url", "https://cao-vault.vault.azure.net.evil/secrets/key"},
		{"--key-vault-secret-url", "https://cao--vault.vault.azure.net/secrets/key"},
		{"--key-vault-secret-url", "https://cao-vault.vault.azure.net/keys/key"},
		{"--key-vault-secret-url", "https://cao-vault.vault.azure.net/secrets/key?token=secret-sentinel"},
		{"--key-vault-secret-url", "https://secret-sentinel@cao-vault.vault.azure.net/secrets/key"},
		{"--key-vault-secret-url", "https://cao-vault.vault.azure.net/secrets/%6bey"},
		{"--key-vault-secret-url", "https://cao-vault.vault.azure.net:443/secrets/key"},
	} {
		t.Run(tt.flag+"/"+tt.value, func(t *testing.T) {
			args := brokerSetupArgs()
			for i, value := range args {
				if value == tt.flag {
					args[i+1] = tt.value
					break
				}
			}
			path := filepath.Join(t.TempDir(), "settings.json")
			root, stdout, stderr := brokerOfflineCommand(t)
			root.SetArgs(append(args, "--output", path))
			err := root.ExecuteContext(t.Context())
			if err == nil || stdout.Len() != 0 || strings.Contains(err.Error()+stderr.String(), "secret-sentinel") {
				t.Fatalf("unsafe validation: %v %s", err, stderr)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("invalid configuration created output")
			}
		})
	}
	for _, flag := range []string{"--url", "--app-id", "--installation-id", "--owner-id", "--key-vault-secret-url", "--single-replica", "--single-process"} {
		args := brokerSetupArgs()
		for i, value := range args {
			if value == flag {
				end := i + 1
				if i < len(args)-2 {
					end++
				}
				args = append(args[:i], args[end:]...)
				break
			}
		}
		root, stdout, _ := brokerOfflineCommand(t)
		root.SetArgs(args)
		if err := root.ExecuteContext(t.Context()); err == nil || stdout.Len() != 0 {
			t.Fatalf("accepted missing explicit %s", flag)
		}
	}
	for _, flags := range [][]string{
		{"--managed-identity-client-id", ""}, {"--managed-identity-client-id", "secret-sentinel"},
		{"--single-replica=false"}, {"--single-process=false"},
	} {
		root, stdout, stderr := brokerOfflineCommand(t)
		root.SetArgs(append(brokerSetupArgs(), flags...))
		err := root.ExecuteContext(t.Context())
		if err == nil || stdout.Len() != 0 || strings.Contains(err.Error()+stderr.String(), "secret-sentinel") {
			t.Fatalf("accepted invalid flags: %v", err)
		}
	}
}

func TestTokenBrokerSetupOutputSafety(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestFile(t, "policy.json", "policy-sentinel")
	for _, mode := range []string{"create", "existing", "symlink", "dry-run"} {
		t.Run(mode, func(t *testing.T) {
			path := mode + ".json"
			if mode == "existing" {
				writeTestFile(t, path, "secret-sentinel")
			}
			if mode == "symlink" {
				if err := os.Symlink("policy.json", path); err != nil {
					t.Fatal(err)
				}
			}
			root, stdout, stderr := brokerOfflineCommand(t)
			args := append(brokerSetupArgs(), "--output", path, "--managed-identity-client-id", "11111111-1111-1111-1111-111111111111")
			if mode == "dry-run" {
				args = append(args, "--dry-run")
			}
			root.SetArgs(args)
			err := root.ExecuteContext(t.Context())
			if mode == "existing" || mode == "symlink" {
				if err == nil || stdout.Len() != 0 || strings.Contains(err.Error()+stderr.String(), "secret-sentinel") {
					t.Fatal("overwrote file or symlink")
				}
			} else if err != nil {
				t.Fatal(err)
			} else if mode == "create" {
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("output mode: %v %v", info, err)
				}
				content, _ := os.ReadFile(path)
				if !bytes.Equal(content, stdout.Bytes()) {
					t.Fatal("file and stdout differ")
				}
			} else if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("dry run created file")
			}
		})
	}
	if content, _ := os.ReadFile("policy.json"); string(content) != "policy-sentinel" {
		t.Fatal("policy mutated")
	}
	if content, _ := os.ReadFile("existing.json"); string(content) != "secret-sentinel" {
		t.Fatal("existing output mutated")
	}
}

func TestTokenBrokerDoctorOffline(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "")
	t.Setenv("CAO_TOKEN_BROKER_ENABLED", "secret-sentinel")
	brokerWriteJSON(t, "settings.json", brokerSettings())
	brokerWriteJSON(t, "policy.json", brokerPolicy(brokerRule()))
	before, _ := os.ReadFile("policy.json")
	root, stdout, stderr := brokerOfflineCommand(t)
	root.SetArgs([]string{"doctor", "token-broker", "--settings", "settings.json", "--policy", "policy.json", "--json"})
	if err := root.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.OK || report.Command != "doctor token-broker" || !strings.Contains(stdout.String(), "Runtime verification was not performed") ||
		strings.Contains(stdout.String()+stderr.String(), "secret-sentinel") {
		t.Fatalf("report = %+v", report)
	}
	after, _ := os.ReadFile("policy.json")
	if !bytes.Equal(before, after) {
		t.Fatal("doctor mutated policy")
	}
}

func TestTokenBrokerDoctorSettings(t *testing.T) {
	t.Chdir(t.TempDir())
	brokerWriteJSON(t, "policy.json", brokerPolicy(brokerRule()))
	for _, tt := range []struct {
		name, content string
		ok            bool
	}{
		{"default", `{}`, true},
		{"disabled", `{"CAO_TOKEN_BROKER_ENABLED":"false","CAO_TOKEN_BROKER_URL":"secret-sentinel"}`, true},
		{"bad-enable", `{"CAO_TOKEN_BROKER_ENABLED":"secret-sentinel"}`, false},
		{"boolean", `{"CAO_TOKEN_BROKER_ENABLED":true}`, false},
		{"null", `null`, false},
		{"shell", `export CAO_TOKEN_BROKER_ENABLED=secret-sentinel`, false},
		{"trailing", `{} {}`, false},
		{"duplicate", `{"CAO_TOKEN_BROKER_ENABLED":"false","CAO_TOKEN_BROKER_ENABLED":"true"}`, false},
		{"bad-key", `{"secret-sentinel":"value"}`, false},
		{"oversize", strings.Repeat(" ", tokenBrokerDocumentLimit+1), false},
		{"nesting", `{"NESTED":` + strings.Repeat("[", 65) + `0` + strings.Repeat("]", 65) + `}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			writeTestFile(t, "settings.json", tt.content)
			root, stdout, stderr := brokerOfflineCommand(t)
			root.SetArgs([]string{"doctor", "token-broker", "--settings", "settings.json", "--policy", "policy.json", "--json"})
			err := root.ExecuteContext(t.Context())
			var report doctorReport
			if json.Unmarshal(stdout.Bytes(), &report) != nil || report.OK != tt.ok || (err == nil) != tt.ok {
				t.Fatalf("report = %s, err = %v", stdout, err)
			}
			if strings.Contains(stdout.String()+stderr.String(), "secret-sentinel") {
				t.Fatal("doctor leaked raw input")
			}
		})
	}
	for key, value := range brokerSettings() {
		if key == tokenBrokerEnvPrefix+"ENABLED" {
			continue
		}
		t.Run(key, func(t *testing.T) {
			settings := brokerSettings()
			settings[key] = "secret-sentinel"
			brokerWriteJSON(t, "settings.json", settings)
			report := diagnoseTokenBroker("settings.json", "policy.json")
			if report.OK {
				t.Fatalf("accepted invalid %s (original %s)", key, value)
			}
			content, _ := json.Marshal(report)
			if bytes.Contains(content, []byte("secret-sentinel")) {
				t.Fatal("doctor leaked setting")
			}
		})
	}
	settings := brokerSettings()
	settings[tokenBrokerEnvPrefix+"MANAGED_IDENTITY_CLIENT_ID"] = "secret-sentinel"
	brokerWriteJSON(t, "settings.json", settings)
	if diagnoseTokenBroker("settings.json", "policy.json").OK {
		t.Fatal("accepted invalid managed identity")
	}
	for _, tt := range []struct {
		hosts string
		ok    bool
	}{{"broker.example", true}, {"OTHER.EXAMPLE, BROKER.EXAMPLE", true}, {"other.example", false}, {"", false}, {"*.example", false}} {
		settings := brokerSettings()
		settings["CAO_ALLOWED_HOSTS"] = tt.hosts
		brokerWriteJSON(t, "settings.json", settings)
		if report := diagnoseTokenBroker("settings.json", "policy.json"); report.OK != tt.ok {
			t.Fatalf("allowed hosts report = %+v", report)
		}
	}
}

func TestTokenBrokerDoctorPolicy(t *testing.T) {
	t.Chdir(t.TempDir())
	brokerWriteJSON(t, "settings.json", brokerSettings())
	var tooMany []any
	for range 129 {
		tooMany = append(tooMany, brokerRule())
	}
	for _, rules := range [][]any{{}, {nil}, {brokerRule(), brokerRule()}, tooMany} {
		brokerWriteJSON(t, "policy.json", brokerPolicy(rules...))
		if diagnoseTokenBroker("settings.json", "policy.json").OK {
			t.Fatalf("accepted invalid rule count/shape %d", len(rules))
		}
	}
	for _, tt := range []struct {
		key   string
		value any
	}{
		{"repository-id", 321}, {"repository-id", "0"}, {"repository-id", "01"},
		{"owner-id", "different-sentinel"}, {"owner-id", "123"},
		{"workflow-ref", "org/repo/.github/workflows/a.yml@refs/heads/*"},
		{"workflow-ref", "org/repo/.github/workflows/a.yml@refs/heads/main.lock"},
		{"workflow-ref", "org/repo/.github/workflows/../a.yml@refs/heads/main"},
		{"workflow-sha", strings.Repeat("A", 40)}, {"workflow-sha", "main"}, {"extra", "secret-sentinel"},
	} {
		rule := brokerRule()
		rule[tt.key] = tt.value
		brokerWriteJSON(t, "policy.json", brokerPolicy(rule))
		report := diagnoseTokenBroker("settings.json", "policy.json")
		content, _ := json.Marshal(report)
		if report.OK || bytes.Contains(content, []byte("sentinel")) {
			t.Fatalf("accepted/leaked invalid rule %s", tt.key)
		}
	}
	for _, content := range []string{
		`{`, `null`, `{} {}`, `{"control-plane":null}`, `{"control-plane":{"token-broker":null}}`,
		`{"control-plane":{"token-broker":{"copilot-requests":{"workflows":null}}}}`,
		`{"control-plane":{"token-broker":{"copilot-requests":{"workflows":[],"secret-sentinel":true}}}}`,
		`{"control-plane":{"token-broker":{"copilot-requests":{"workflows":[],"workflows":[]}}}}`,
		`{"control-plane":{"token-broker":{"copilot-requests":{"workflows":[]},"other":{}}}}`,
		strings.Repeat(" ", tokenBrokerDocumentLimit+1),
	} {
		writeTestFile(t, "policy.json", content)
		report := diagnoseTokenBroker("settings.json", "policy.json")
		data, _ := json.Marshal(report)
		if report.OK || bytes.Contains(data, []byte("sentinel")) {
			t.Fatal("accepted malformed broker policy or leaked raw fields")
		}
	}
	if diagnoseTokenBroker("settings.json", "missing.json").OK {
		t.Fatal("enabled broker accepted missing policy")
	}
	brokerWriteJSON(t, "settings.json", map[string]string{})
	brokerWriteJSON(t, "policy.json", brokerPolicy())
	if !diagnoseTokenBroker("settings.json", "policy.json").OK || !diagnoseTokenBroker("settings.json", "missing.json").OK {
		t.Fatal("disabled broker failed empty or missing allowlist")
	}
	writeTestFile(t, "policy.json", `{"control-plane":{"token-broker":null}}`)
	if diagnoseTokenBroker("settings.json", "policy.json").OK {
		t.Fatal("disabled broker accepted malformed policy")
	}
	brokerWriteJSON(t, "settings.json", brokerSettings())
	var maximum []any
	for i := range 128 {
		rule := brokerRule()
		rule["repository-id"] = strconv.Itoa(i + 1)
		maximum = append(maximum, rule)
	}
	brokerWriteJSON(t, "policy.json", brokerPolicy(maximum...))
	if !diagnoseTokenBroker("settings.json", "policy.json").OK {
		t.Fatal("rejected maximum valid allowlist")
	}
}

func TestTokenBrokerDoctorHostProfiles(t *testing.T) {
	t.Chdir(t.TempDir())
	brokerWriteJSON(t, "settings.json", brokerSettings())
	for _, target := range []map[string]any{
		{"module": "container", "replicas": 2},
		{"module": "azure-functions", "replicas": 1},
		{"module": "generic", "authentication": "github-oauth", "listener": "external", "replicas": 1, "supports-single-replica": true},
		{"module": "generic", "authentication": "local", "listener": "process", "replicas": 1, "supports-single-replica": true},
		{"module": "generic", "authentication": "github-oauth", "listener": "process", "replicas": 1, "supports-single-replica": true, "require-https": false},
		{"module": "container", "replicas": 1, "authentication": "github-oauth"},
		{"module": "container"},
	} {
		policy := brokerPolicy(brokerRule())
		policy["control-plane"].(map[string]any)["web"] = map[string]any{"host": map[string]any{"target": target}}
		brokerWriteJSON(t, "policy.json", policy)
		if diagnoseTokenBroker("settings.json", "policy.json").OK {
			t.Fatalf("accepted invalid host profile %v", target)
		}
	}
	policy := brokerPolicy(brokerRule())
	policy["extends"] = "secret-sentinel.json"
	brokerWriteJSON(t, "policy.json", policy)
	if diagnoseTokenBroker("settings.json", "policy.json").OK {
		t.Fatal("inferred unresolved host profile")
	}
	for _, operational := range []map[string]any{
		{"backend": "memory"},
		{"backend": "memory", "single-process": true, "allow-volatile": false},
		{"backend": "postgres", "single-process": true},
		{"backend": "redis", "postgres": map[string]any{}},
		{"backend": "invalid-sentinel"},
	} {
		policy := brokerPolicy(brokerRule())
		policy["control-plane"].(map[string]any)["web"].(map[string]any)["host"].(map[string]any)["operational-store"] = operational
		brokerWriteJSON(t, "policy.json", policy)
		if diagnoseTokenBroker("settings.json", "policy.json").OK {
			t.Fatalf("accepted incompatible operational profile %v", operational)
		}
	}
	policy = brokerPolicy(brokerRule())
	policy["control-plane"].(map[string]any)["web"] = map[string]any{"host": map[string]any{
		"target":            map[string]any{"module": "generic", "authentication": "github-oauth", "listener": "process", "supports-single-replica": true, "replicas": 1},
		"operational-store": map[string]any{"backend": "memory", "single-process": true, "allow-volatile": true},
	}}
	brokerWriteJSON(t, "policy.json", policy)
	if !diagnoseTokenBroker("settings.json", "policy.json").OK {
		t.Fatal("rejected compatible generic single-process profile")
	}
}

func TestTokenBrokerRegularDoctorPolicy(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, tt := range []struct {
		policy any
		ok     bool
		count  int
	}{
		{map[string]any{"version": 1, "gh-aw-version": "v0.92.0"}, true, 5},
		{brokerPolicy(), true, 6},
		{brokerPolicy(brokerRule()), true, 6},
		{brokerPolicy(brokerRule(), brokerRule()), false, 6},
	} {
		brokerWriteJSON(t, policyPath, tt.policy)
		report, err := diagnose(t.Context(), func(context.Context) (string, error) { return "gh version 2.80.0", nil },
			func(_ context.Context, args ...string) (string, string, error) {
				if args[0] == "aw" {
					return "v0.92.0", "", nil
				}
				return "", "", nil
			})
		if err != nil || report.OK != tt.ok || len(report.Checks) != tt.count {
			t.Fatalf("report = %+v, err = %v", report, err)
		}
	}
	writeTestFile(t, policyPath, `{"version":1,"gh-aw-version":"v0.92.0","control-plane":{"token-broker":null,"token-broker":{}}}`)
	if _, err := readDoctorPolicy(); err == nil {
		t.Fatal("regular doctor accepted duplicate broker policy")
	}
}
