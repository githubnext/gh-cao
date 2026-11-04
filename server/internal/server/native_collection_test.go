package server

import (
	"errors"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
)

func TestNativeCollectionCannotActivateFilesystemDependencies(t *testing.T) {
	for _, admissionOnly := range []bool{false, true} {
		config := CollectorConfig{NativeAcquisition: true, AdmitOnly: admissionOnly}
		if err := config.Validate(); !errors.Is(err, collectnative.ErrUnsupported) {
			t.Fatalf("native collection silently activated the legacy filesystem path: %v", err)
		}
	}
}
