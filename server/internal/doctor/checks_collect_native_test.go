package doctor

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func nativeDoctorEnvironment() map[string]string {
	return map[string]string{
		"CAO_COLLECT_NATIVE":                  "true",
		"CAO_COLLECT_APP_ID":                  "42",
		"CAO_COLLECT_PRIVATE_KEY":             "configured-not-read",
		"CAO_COLLECT_CONTROL_REPOSITORY":      "octo/control",
		"CAO_COLLECT_NATIVE_INVENTORY_DIGEST": strings.Repeat("a", 64),
		"CAO_GITHUB_WEBHOOK_SECRET":           "configured-not-read",
		"CAO_COLLECT_LAKE_DIRECTORY":          "/must-not-read",
		"CAO_COLLECT_CATALOG_ROOT":            "/must-not-read",
		"CAO_COLLECT_STATIC_INVENTORY":        "/must-not-read",
		"CAO_POLICY_PATH":                     "/must-not-read",
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
	for _, check := range []Check{d.checkLake(t.Context()), d.checkProjectionLock(t.Context())} {
		if check.Status != StatusSkip {
			t.Errorf("%s consulted legacy ownership/storage: %+v", check.ID, check)
		}
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
	values["CAO_COLLECT_NODE_BINARY"] = filepath.Join(root, "node")
	d := Doctor{Getenv: func(name string) string { return values[name] }}
	if check := d.checkTooling(t.Context()); check.Status != StatusFail || !strings.Contains(check.Summary, "requires Node") {
		t.Fatalf("selected Value ignored its Node dependency: %+v", check)
	}
	if err := os.WriteFile(values["CAO_COLLECT_NODE_BINARY"], []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil { // #nosec G306 -- executable test-owned dependency probe, never launched.
		t.Fatal(err)
	}
	assets := []string{
		values["CAO_POLICY_PATH"],
		filepath.Join(root, "activity", "local-values.mjs"),
		filepath.Join(root, "activity", "workflow-values.mjs"),
		filepath.Join(root, ".github", "workflows", "shared", "policy.mjs"),
	}
	for _, path := range assets {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if check := d.checkTooling(t.Context()); check.Status != StatusPass {
		t.Fatalf("selected Value incorrectly required gh or activity/cao.mjs: %+v", check)
	}
	for _, path := range assets {
		t.Run(filepath.Base(path), func(t *testing.T) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := os.WriteFile(path, nil, 0o600); err != nil { // #nosec G703 -- fixed asset path under t.TempDir, not external input.
					t.Error(err)
				}
			})
			if check := d.checkTooling(t.Context()); check.Status != StatusFail {
				t.Fatalf("selected Value ignored a missing reviewed asset: %+v", check)
			}
		})
	}
	unselected := maps.Clone(values)
	unselected["CAO_COLLECT_NATIVE_VALUES"] = "false"
	unselected["CAO_COLLECT_NODE_BINARY"] = "/nonexistent"
	d.Getenv = func(name string) string { return unselected[name] }
	if check := d.checkTooling(t.Context()); check.Status != StatusPass {
		t.Fatalf("unselected Value retained a Node requirement: %+v", check)
	}
}
