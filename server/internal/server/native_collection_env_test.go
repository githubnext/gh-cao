package server

import (
	"errors"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
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
	if config, err := CollectorConfigFromEnv(); config != nil || !errors.Is(err, collectnative.ErrUnsupported) {
		t.Fatalf("public activation bypassed the remaining audit gate: %+v %v", config, err)
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
