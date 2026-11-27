package marketplace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsSafePathTable(t *testing.T) {
	cases := []struct {
		path string
		safe bool
	}{
		{"", true},
		{"packages", true},
		{"packages/official", true},
		{".config/packages", true},
		{"/etc/passwd", false},
		{"..", false},
		{"../secrets", false},
		{"packages/..", false},
		{"packages/../../etc", false},
		{"packages/..secret", true}, // "..secret" is a valid segment name, not a ".." traversal
		{"packages\x00null", false}, // NUL and other control bytes are outside the allowed charset
	}
	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			if got := isSafePath(testCase.path); got != testCase.safe {
				t.Fatalf("isSafePath(%q) = %v, want %v", testCase.path, got, testCase.safe)
			}
		})
	}
}

func TestSecretNamePatternTable(t *testing.T) {
	cases := []struct {
		name  string
		valid bool
	}{
		{"REGISTRY_PAT", true},
		{"A", true},
		{"APP_ID_2", true},
		{"literal-token", false},
		{"lowercase", false},
		{"2LEADS_WITH_DIGIT", false},
		{"HAS SPACE", false},
		{"", false},
		{strings.Repeat("A", 129), false},
		{strings.Repeat("A", 128), true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := secretNamePattern.MatchString(testCase.name); got != testCase.valid {
				t.Fatalf("secretNamePattern.MatchString(%q) = %v, want %v", testCase.name, got, testCase.valid)
			}
		})
	}
}

// TestPackageAndDiagnosticJSONNeverExposeAuthFields is a structural guarantee:
// the safe DTOs returned to dashboard clients carry no field that could hold
// a secret name, secret value, or bearer token, so no future change to
// serialization can accidentally start leaking one.
func TestPackageAndDiagnosticJSONNeverExposeAuthFields(t *testing.T) {
	pkg := Package{ID: "official:example@" + fakeCommitSHA, RegistryID: "official", Name: "Demo"}
	data, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"auth", "secret", "token", "private-key", "app-id"} {
		if strings.Contains(strings.ToLower(string(data)), forbidden) {
			t.Fatalf("Package JSON unexpectedly contains %q: %s", forbidden, data)
		}
	}
	diagnostic := RegistryDiagnostic{RegistryID: "official", Status: "available", Packages: 3}
	data, err = json.Marshal(diagnostic)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"auth", "secret", "token"} {
		if strings.Contains(strings.ToLower(string(data)), forbidden) {
			t.Fatalf("RegistryDiagnostic JSON unexpectedly contains %q: %s", forbidden, data)
		}
	}
}

func TestPackageRowNeverExposesAuthKeys(t *testing.T) {
	row := Package{ID: "x", Contents: []string{"a.md"}}.Row()
	for key := range row {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "auth") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") {
			t.Fatalf("Package.Row() unexpectedly contains a credential-shaped key: %q", key)
		}
	}
}

func TestRegistryDiagnosticRowOmitsPackagesCountWhenUnavailable(t *testing.T) {
	row := RegistryDiagnostic{RegistryID: "x", Status: "unavailable", Message: "boom"}.Row()
	if _, present := row["packages"]; present {
		t.Fatal("expected an unavailable diagnostic to omit the packages count")
	}
}

func TestLoadConfigFileDegradesGracefullyOnAMissingFile(t *testing.T) {
	if _, err := LoadConfigFile(filepath.Join(t.TempDir(), "does-not-exist.json")); err == nil {
		t.Fatal("expected a missing policy file to return an error the caller can degrade on")
	}
}

func TestLoadConfigFileRejectsOversizedPolicyWithoutMaterializingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cao.json")
	file, err := os.Create(path) // #nosec G304 -- test-owned temporary path.
	if err != nil {
		t.Fatal(err)
	}
	// A sparse file far above the limit costs no disk but would cost its full
	// size in memory if read before the limit check.
	const fileBytes = 128 << 20
	if err := file.Truncate(fileBytes); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err = LoadConfigFile(path)
	runtime.ReadMemStats(&after)

	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected an oversized policy rejection, got: %v", err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > fileBytes/4 {
		t.Fatalf("oversized policy was materialized: allocated %d bytes for a %d byte limit", allocated, maxPolicyBytes)
	}
}

func TestLoadConfigFileAcceptsPolicyAtTheSizeLimit(t *testing.T) {
	prefix, suffix := `{"padding":"`, `"}`
	padding := strings.Repeat("x", maxPolicyBytes-len(prefix)-len(suffix))
	path := filepath.Join(t.TempDir(), "cao.json")
	if err := os.WriteFile(path, []byte(prefix+padding+suffix), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("a policy exactly at the limit must be accepted: %v", err)
	}
	if len(config.Registries) != 0 {
		t.Fatalf("unexpected registries: %#v", config.Registries)
	}
}

func TestLoadConfigFileParsesARealPolicyDocumentFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cao.json")
	document := `{
		"version": 1,
		"control-plane": {
			"marketplace": {
				"cache-ttl-seconds": 300,
				"registries": [{"id": "official", "repository": "example/packages", "ref": "main"}]
			}
		}
	}`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Registries) != 1 || config.Registries[0].ID != "official" {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestValidateAuthAcceptsAllSupportedTypesAndRejectsUnknownOnes(t *testing.T) {
	for _, auth := range []Auth{
		{Type: ""},
		{Type: AuthNone},
		{Type: AuthPAT, Secret: "REGISTRY_PAT"},
		{Type: AuthGitHubApp, AppIDSecret: "APP_ID", PrivateKeySecret: "APP_KEY", InstallationIDSecret: "INSTALLATION_ID"},
	} {
		if err := validateAuth(auth); err != nil {
			t.Fatalf("expected auth %#v to be valid: %v", auth, err)
		}
	}
	if err := validateAuth(Auth{Type: "basic"}); err == nil {
		t.Fatal("expected an unknown auth type to be rejected")
	}
}
