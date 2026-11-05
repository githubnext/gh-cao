package server

import (
	"strings"
	"testing"
)

func TestNativeCollectionCannotFallBackToFilesystemDependencies(t *testing.T) {
	for _, admissionOnly := range []bool{false, true} {
		config := CollectorConfig{NativeAcquisition: true, AdmitOnly: admissionOnly}
		if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "GitHub App identifier") {
			t.Fatalf("incomplete native configuration fell back to the legacy filesystem path: %v", err)
		}
	}
}
