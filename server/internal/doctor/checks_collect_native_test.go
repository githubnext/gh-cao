package doctor

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/operational/memory"
)

func nativeDoctorEnvironment() map[string]string {
	return map[string]string{
		"CAO_COLLECT_NATIVE":                  "true",
		"CAO_COLLECT_APP_ID":                  "42",
		"CAO_COLLECT_PRIVATE_KEY":             "configured-not-read",
		"CAO_COLLECT_CONTROL_REPOSITORY":      "octo/control",
		"CAO_COLLECT_NATIVE_INVENTORY_DIGEST": strings.Repeat("a", 64),
		"CAO_GITHUB_WEBHOOK_SECRET":           "configured-not-read",
	}
}

func TestNativeDoctorDoesNotRequireLegacyFilesOrExecutables(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	values := nativeDoctorEnvironment()
	d := Doctor{Getenv: func(name string) string { return values[name] }}
	for _, check := range []Check{
		d.checkCollectionProfile(t.Context()),
		d.checkCollectionSettings(t.Context()),
		d.checkTooling(t.Context()),
	} {
		if check.Status != StatusPass {
			t.Errorf("%s unexpectedly requires a legacy dependency: %+v", check.ID, check)
		}
	}
	if check := d.checkCollectionStorage(t.Context()); check.Status != StatusFail || !strings.Contains(check.Summary, "PostgreSQL") {
		t.Fatalf("native collection accepted a missing PostgreSQL store: %+v", check)
	}
}

func TestNativeDoctorSettingsUseRuntimeValidation(t *testing.T) {
	for _, testcase := range []struct {
		name, field, value, message string
	}{
		{"missing inventory", "CAO_COLLECT_NATIVE_INVENTORY_DIGEST", "", "reviewed inventory"},
		{"invalid digest", "CAO_COLLECT_NATIVE_INVENTORY_DIGEST", strings.Repeat("A", 64), "lowercase SHA-256"},
		{"legacy limit", "CAO_COLLECT_MAX_STORAGE_MB", "1", "does not use legacy"},
		{"negative workers", "CAO_COLLECT_WORKERS", "-1", "must not be negative"},
		{"malformed workers", "CAO_COLLECT_WORKERS", "many", "must be an integer"},
		{"malformed duration", "CAO_COLLECT_TIMEOUT", "soon", "must be a Go duration"},
		{"malformed native flag", "CAO_COLLECT_NATIVE", "yes", "must be a boolean"},
		{"malformed Value flag", "CAO_COLLECT_NATIVE_VALUES", "yes", "must be a boolean"},
		{"malformed admission", "CAO_COLLECT_ADMIT_ONLY", "yes", "must be a boolean"},
		{"malformed recovery", "CAO_COLLECT_RECOVER_DELIVERIES", "yes", "must be a boolean"},
		{"retired lake", "CAO_COLLECT_LAKE_DIRECTORY", "/old-lake", "CAO_COLLECT_LAKE_DIRECTORY"},
		{"retired inventory", "CAO_COLLECT_STATIC_INVENTORY", "/old-inventory", "CAO_COLLECT_STATIC_INVENTORY"},
		{"retired gh", "CAO_COLLECT_GH_BINARY", "/usr/bin/gh", "CAO_COLLECT_GH_BINARY"},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			values := nativeDoctorEnvironment()
			values[testcase.field] = testcase.value
			d := Doctor{Getenv: func(name string) string { return values[name] }}
			check := d.checkCollectionSettings(t.Context())
			if check.Status != StatusFail || !strings.Contains(check.Summary, testcase.message) {
				t.Fatalf("native runtime validation was not preserved: %+v", check)
			}
		})
	}
}

func TestDoctorRejectsLegacyAppBeforeReadingKey(t *testing.T) {
	values := nativeDoctorEnvironment()
	delete(values, "CAO_COLLECT_NATIVE")
	values["CAO_COLLECT_PRIVATE_KEY_FILE"] = "/must-not-read"
	d := Doctor{Getenv: func(name string) string { return values[name] }}
	check := d.checkCollectionSettings(t.Context())
	if check.Status != StatusFail || !strings.Contains(check.Summary, "filesystem collector is retired") {
		t.Fatalf("legacy App configuration did not fail before reading key: %+v", check)
	}
	if strings.Contains(check.Summary, "/must-not-read") {
		t.Fatalf("legacy App configuration inspected key file: %+v", check)
	}
}

func TestDoctorActionsProfileKeepsCollectionOptional(t *testing.T) {
	d := Doctor{Getenv: func(string) string { return "" }}
	for _, check := range []Check{
		d.checkCollectionProfile(t.Context()),
		d.checkCollectionSettings(t.Context()),
		d.checkCollectionStorage(t.Context()),
		d.checkTooling(t.Context()),
	} {
		want := StatusSkip
		if check.ID == "collect.profile" {
			want = StatusPass
		}
		if check.Status != want {
			t.Errorf("%s status = %s, want %s: %+v", check.ID, check.Status, want, check)
		}
	}
}

func TestNativeDoctorReportUsesPostgresInsteadOfLegacyChecks(t *testing.T) {
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	values := nativeDoctorEnvironment()
	d := Doctor{Backend: "memory", Store: store, Getenv: func(name string) string { return values[name] }}
	report := d.Run(t.Context())
	foundStorage := false
	for _, check := range report.Checks {
		switch check.ID {
		case "collect.lake", "collect.projection":
			t.Errorf("retired check remains in report: %+v", check)
		case "collect.storage":
			foundStorage = true
			if check.Status != StatusFail || !strings.Contains(check.Summary, "PostgreSQL") {
				t.Errorf("native storage requirement was not diagnosed: %+v", check)
			}
		}
	}
	if !foundStorage {
		t.Fatal("native storage check missing from report")
	}
}

func TestNativeDoctorAdmissionDoesNotInspectWorkerCredentials(t *testing.T) {
	values := nativeDoctorEnvironment()
	values["CAO_COLLECT_ADMIT_ONLY"] = "true"
	values["CAO_COLLECT_PRIVATE_KEY_FILE"] = "/must-not-read"
	d := Doctor{Getenv: func(name string) string { return values[name] }}
	if check := d.checkCollectionSettings(t.Context()); check.Status != StatusPass {
		t.Fatalf("native admission inspected an unused credential source: %+v", check)
	}
}

func TestNativeDoctorRejectsFeaturesWithoutAnApp(t *testing.T) {
	for field, value := range map[string]string{
		"CAO_COLLECT_NATIVE":                  "true",
		"CAO_COLLECT_NATIVE_VALUES":           "true",
		"CAO_COLLECT_NATIVE_INVENTORY_DIGEST": strings.Repeat("a", 64),
	} {
		t.Run(field, func(t *testing.T) {
			values := map[string]string{field: value}
			d := Doctor{Getenv: func(name string) string { return values[name] }}
			if check := d.checkCollectionProfile(t.Context()); check.Status != StatusFail {
				t.Fatalf("native feature silently selected the Actions profile: %+v", check)
			}
		})
	}
}

func TestNativeDoctorValueToolingChecksOnlySelectedDependencies(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	root := t.TempDir()
	values := nativeDoctorEnvironment()
	values["CAO_COLLECT_NATIVE_VALUES"] = "true"
	values["CAO_COLLECT_CATALOG_ROOT"] = root
	values["CAO_POLICY_PATH"] = filepath.Join(root, "reviewed-policy.json")
	d := Doctor{Getenv: func(name string) string { return values[name] }}
	if check := d.checkTooling(t.Context()); check.Status != StatusFail {
		t.Fatalf("selected Value ignored its missing reviewed policy: %+v", check)
	}
	if err := os.WriteFile(values["CAO_POLICY_PATH"], []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if check := d.checkTooling(t.Context()); check.Status != StatusPass {
		t.Fatalf("selected Value rejected Go policy without Node: %+v", check)
	}
	if err := os.Remove(values["CAO_POLICY_PATH"]); err != nil {
		t.Fatal(err)
	}
	if check := d.checkTooling(t.Context()); check.Status != StatusFail {
		t.Fatalf("selected Value ignored removed reviewed policy: %+v", check)
	}
	unselected := maps.Clone(values)
	unselected["CAO_COLLECT_NATIVE_VALUES"] = "false"
	d.Getenv = func(name string) string { return unselected[name] }
	if check := d.checkTooling(t.Context()); check.Status != StatusPass {
		t.Fatalf("unselected Value inspected optional policy: %+v", check)
	}
}
