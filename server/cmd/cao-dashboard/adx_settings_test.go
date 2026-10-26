package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
)

func envMap(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

// validTraceEnv writes a private approved policy and returns an environment
// that adxtrace.FromEnv accepts.
func validTraceEnv(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	policy := `{"version":"approved-v1","contentApproved":true,"rightsApproved":true,"events":{"user.message":["content"]}}`
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"CAO_ADX_TRACES_ENABLED": "true", "CAO_ADX_TRACE_DIRECTORY": dir,
		"CAO_ADX_TRACE_DURABLE_STORAGE": "true", "CAO_ADX_TRACE_POLICY": path,
		"CAO_ADX_TRACE_MODE": "full", "CAO_ADX_TRACE_REPOSITORIES": "octo/api",
	}
}

func TestTraceSettingsRequiresEnabledTracingAndDestination(t *testing.T) {
	cases := map[string]map[string]string{
		"disabled":         {"CAO_ADX_ENDPOINT": "https://example.invalid", "CAO_ADX_DATABASE": "db"},
		"missing endpoint": {"CAO_ADX_DATABASE": "db"},
		"missing database": {"CAO_ADX_ENDPOINT": "https://example.invalid"},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			env := map[string]string{}
			if name != "disabled" {
				env = validTraceEnv(t)
			}
			for k, v := range extra {
				env[k] = v
			}
			if _, _, err := traceSettings(envMap(env)); err == nil || !strings.Contains(err.Error(), "destination identity are required") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestTraceSettingsRejectsInvalidConfig(t *testing.T) {
	_, _, err := traceSettings(envMap(map[string]string{"CAO_ADX_TRACES_ENABLED": "maybe"}))
	if err == nil || !strings.Contains(err.Error(), "must be true or false") {
		t.Fatalf("error = %v", err)
	}
}

func TestTraceSettingsDerivesNamespace(t *testing.T) {
	env := validTraceEnv(t)
	env["CAO_ADX_ENDPOINT"], env["CAO_ADX_DATABASE"] = "https://example.invalid", "db"
	config, namespace, err := traceSettings(envMap(env))
	if err != nil {
		t.Fatal(err)
	}
	if !config.Enabled || namespace == "" || namespace != adxtrace.Namespace(env["CAO_ADX_ENDPOINT"], env["CAO_ADX_DATABASE"]) {
		t.Fatalf("config=%+v namespace=%q", config, namespace)
	}
}
