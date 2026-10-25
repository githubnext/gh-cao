package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
)

func TestADXSchemaCommandIsOffline(t *testing.T) {
	t.Setenv("CAO_ADX_ENABLED", "true")
	command := newRootCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"adx", "schema"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), ".create-merge table ") != 15 ||
		!strings.Contains(output.String(), "cao_ReadySessions()") {
		t.Fatal("ADX setup schema is incomplete")
	}
}

func TestTraceSettingsRequiresEnabledDestination(t *testing.T) {
	dir := t.TempDir()
	policy := filepath.Join(dir, "policy.json")
	body := `{"version":"v1","contentApproved":true,"rightsApproved":true,"events":{"run.completed":["status"]}}`
	if err := os.WriteFile(policy, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	enabled := map[string]string{
		"CAO_ADX_TRACES_ENABLED":        "true",
		"CAO_ADX_TRACE_DIRECTORY":       filepath.Join(dir, "spool"),
		"CAO_ADX_TRACE_DURABLE_STORAGE": "true",
		"CAO_ADX_TRACE_REPOSITORIES":    "octo/repo",
		"CAO_ADX_TRACE_MODE":            "full",
		"CAO_ADX_TRACE_POLICY":          policy,
		"CAO_ADX_ENDPOINT":              "https://adx.example.test",
		"CAO_ADX_DATABASE":              "cao",
	}
	env := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	config, namespace, err := traceSettings(env(enabled))
	if err != nil || !config.Enabled {
		t.Fatalf("settings = %+v %q %v", config, namespace, err)
	}
	if want := adxtrace.Namespace("https://adx.example.test", "cao"); namespace != want {
		t.Fatalf("namespace = %q, want %q", namespace, want)
	}
	for _, missing := range []string{"CAO_ADX_TRACES_ENABLED", "CAO_ADX_ENDPOINT", "CAO_ADX_DATABASE"} {
		values := map[string]string{}
		for k, v := range enabled {
			if k != missing {
				values[k] = v
			}
		}
		if _, _, err := traceSettings(env(values)); err == nil {
			t.Errorf("missing %s: expected error", missing)
		}
	}
	if _, _, err := traceSettings(env(map[string]string{"CAO_ADX_TRACES_ENABLED": "maybe"})); err == nil {
		t.Error("invalid enabled flag: expected error")
	}
}
