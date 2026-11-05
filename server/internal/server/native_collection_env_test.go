package server

import (
	"strings"
	"testing"
)

func TestNativeCollectionEnvironmentSelectionFailsClosed(t *testing.T) {
	t.Setenv("CAO_COLLECT_APP_ID", "")
	t.Setenv("CAO_COLLECT_NATIVE", "true")
	t.Setenv("CAO_COLLECT_NATIVE_VALUES", "false")
	if config, err := CollectorConfigFromEnv(); config != nil || err == nil {
		t.Fatal("explicit native selection silently became Actions snapshot ingestion")
	}
	t.Setenv("CAO_COLLECT_APP_ID", "7")
	t.Setenv("CAO_COLLECT_ADMIT_ONLY", "true")
	if config, err := CollectorConfigFromEnv(); err != nil || config == nil ||
		!config.NativeAcquisition || !config.AdmitOnly || config.NativeValues {
		t.Fatalf("explicit native admission required legacy dependencies: %+v %v", config, err)
	}
}

func TestNativeCollectionFeatureBooleansRejectMalformedSelection(t *testing.T) {
	for _, name := range []string{"CAO_COLLECT_NATIVE", "CAO_COLLECT_NATIVE_VALUES"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CAO_COLLECT_APP_ID", "")
			t.Setenv("CAO_COLLECT_NATIVE", "")
			t.Setenv("CAO_COLLECT_NATIVE_VALUES", "")
			t.Setenv(name, "enabled-ish")
			if config, err := CollectorConfigFromEnv(); config != nil || err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("malformed feature selection was ignored: %+v %v", config, err)
			}
		})
	}
}

func TestNativeValueSelectionCannotSilentlyUseLegacyCollection(t *testing.T) {
	config := &CollectorConfig{AppID: 7, AdmitOnly: true, NativeValues: true}
	if err := config.Validate(); err == nil {
		t.Fatal("native-only feature silently became legacy collection")
	}
}

func TestNativeInventorySelectionCannotSilentlyUseActionsProfile(t *testing.T) {
	t.Setenv("CAO_COLLECT_APP_ID", "")
	t.Setenv("CAO_COLLECT_NATIVE", "")
	t.Setenv("CAO_COLLECT_NATIVE_VALUES", "")
	t.Setenv("CAO_COLLECT_NATIVE_INVENTORY_DIGEST", strings.Repeat("a", 64))
	if config, err := CollectorConfigFromEnv(); config != nil || err == nil {
		t.Fatal("explicit native inventory selection silently became Actions snapshot ingestion")
	}
}

func TestNativeCollectionEnvironmentRejectsMalformedSettings(t *testing.T) {
	for _, name := range []string{
		"CAO_COLLECT_WORKERS", "CAO_COLLECT_WINDOW_DAYS", "CAO_COLLECT_INVENTORY_LIMIT",
		"CAO_COLLECT_QUEUE_MAX_LENGTH", "CAO_COLLECT_RATE_LIMIT_FLOOR", "CAO_COLLECT_RUN_LIMIT",
		"CAO_COLLECT_MAX_STORAGE_MB", "CAO_COLLECT_REQUEST_TIMEOUT_MINUTES",
		"CAO_COLLECT_PROJECTION_INTERVAL", "CAO_COLLECT_REPOSITORY_REFRESH_INTERVAL", "CAO_COLLECT_TIMEOUT",
		"CAO_COLLECT_ADMIT_ONLY", "CAO_COLLECT_RECOVER_DELIVERIES",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CAO_COLLECT_NATIVE", "true")
			t.Setenv("CAO_COLLECT_NATIVE_VALUES", "false")
			t.Setenv("CAO_COLLECT_APP_ID", "7")
			t.Setenv("CAO_COLLECT_ADMIT_ONLY", "true")
			t.Setenv(name, "invalid")
			if config, err := CollectorConfigFromEnv(); config != nil || err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("malformed native setting silently selected a default: %+v %v", config, err)
			}
		})
	}
}
