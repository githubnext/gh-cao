package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostPolicyFileIsRequired(t *testing.T) {
	t.Setenv("CAO_POLICY_PATH", "")
	t.Setenv(marketplacePolicyPathEnv, "")
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	if _, err := loadHostPolicyFromEnv(); err == nil {
		t.Fatal("missing default cao.json was accepted")
	}
	t.Chdir(workingDirectory)

	t.Setenv("CAO_POLICY_PATH", filepath.Join(t.TempDir(), "missing.json"))
	if _, err := loadHostPolicyFromEnv(); err == nil {
		t.Fatal("explicitly configured missing policy was accepted")
	}
}

func TestConfiguredHostPolicyReadsCaoJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cao.json")
	document := `{
		"version": 1,
		"control-plane": {
			"scope": {"allowed-owners": ["example"]},
			"web": {
				"host": {
					"target": {"module": "container", "name": "render"},
					"operational-store": {"backend": "postgres"}
				}
			}
		}
	}`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_POLICY_PATH", path)
	t.Setenv("CAO_POSTGRES_URL", "postgres://127.0.0.1/db?sslmode=disable")
	resolved, err := loadHostPolicyFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Profile.Name != "render" || resolved.OperationalBackend != "postgres" {
		t.Fatalf("unexpected resolved policy: %+v", resolved)
	}
}

func TestConfiguredHostPolicyComposesDeploymentProfile(t *testing.T) {
	directory := t.TempDir()
	basePath := filepath.Join(directory, "cao.json")
	profilePath := filepath.Join(directory, "cao.test.json")
	base := `{
		"version": 1,
		"control-plane": {
			"scope": {"allowed-owners": ["example"]},
			"web": {"experimental": true}
		}
	}`
	profile := `{
		"extends": "cao.json",
		"control-plane": {
			"web": {
				"host": {
					"target": {"module": "container", "name": "test", "replicas": 1},
					"operational-store": {"backend": "postgres"}
				}
			}
		}
	}`
	if err := os.WriteFile(basePath, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profilePath, []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_POLICY_PATH", profilePath)
	t.Setenv("CAO_POSTGRES_URL", "postgres://127.0.0.1/db?sslmode=disable")
	resolved, err := loadHostPolicyFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Profile.Name != "test" || resolved.OperationalBackend != "postgres" {
		t.Fatalf("unexpected resolved deployment profile: %+v", resolved)
	}
}

func TestDeploymentProfilesRejectAuthorityOverridesAndCycles(t *testing.T) {
	directory := t.TempDir()
	basePath := filepath.Join(directory, "cao.json")
	if err := os.WriteFile(basePath, []byte(`{"control-plane":{"web":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	wideningPath := filepath.Join(directory, "cao.widening.json")
	if err := os.WriteFile(wideningPath, []byte(`{
		"extends": "cao.json",
		"control-plane": {
			"campaigns": {"example": {"mode": "live"}},
			"web": {"host": {}}
		}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadComposedHostPolicy(wideningPath); err == nil ||
		!strings.Contains(err.Error(), "only extend control-plane.web.host") {
		t.Fatalf("authority override was not rejected: %v", err)
	}

	aPath := filepath.Join(directory, "cao.a.json")
	bPath := filepath.Join(directory, "cao.b.json")
	if err := os.WriteFile(aPath, []byte(`{
		"extends": "cao.b.json",
		"control-plane": {"web": {"host": {}}}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte(`{
		"extends": "cao.a.json",
		"control-plane": {"web": {"host": {}}}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadComposedHostPolicy(aPath); err == nil ||
		!strings.Contains(err.Error(), "cycle") {
		t.Fatalf("deployment profile cycle was not rejected: %v", err)
	}

	outsidePath := filepath.Join(filepath.Dir(directory), "cao.outside.json")
	if err := os.WriteFile(outsidePath, []byte(`{"control-plane":{"web":{}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	escapePath := filepath.Join(directory, "cao.escape.json")
	if err := os.WriteFile(escapePath, []byte(`{
		"extends": "../cao.outside.json",
		"control-plane": {"web": {"host": {}}}
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadComposedHostPolicy(escapePath); err == nil ||
		!strings.Contains(err.Error(), "within the policy directory") {
		t.Fatalf("deployment profile traversal was not rejected: %v", err)
	}
}

func TestHostPolicyRejectsFlatHostFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cao.json")
	document := `{
		"control-plane": {
			"web": {
				"host": {
					"name": "legacy",
					"authentication": "github-oauth",
					"listener": "process",
					"operational-store": {"backend": "postgres"}
				}
			}
		}
	}`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAO_POLICY_PATH", path)
	if _, err := loadHostPolicyFromEnv(); err == nil {
		t.Fatal("flat host policy was accepted")
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
