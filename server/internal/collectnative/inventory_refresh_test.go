package collectnative

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestNativeInventoryRefreshPrefersObservedCompilerOverReviewedInstallation(t *testing.T) {
	snapshot := inventoryTestSnapshot(t, "available")
	content := bytes.ReplaceAll(snapshot.Bytes(), []byte(`"gh-aw-version":"unknown"`), []byte(`"gh-aw-version":"v0.9.0"`))
	digest := sha256.Sum256(content)
	snapshot, err := NewInventorySnapshot(content, hex.EncodeToString(digest[:]), snapshot.Provenance)
	if err != nil {
		t.Fatal(err)
	}
	provider, requests := inventoryTestGitHub(t, false)
	for range 2 {
		evidence, err := provider.BuildInventory(t.Context(), EnrollmentScope{Repository: "octo/api", InstallationID: 7, Epoch: 1}, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if got := evidence.Sources["workflows"].Rows[1]["gh-aw-version"]; got != "v1.1.0" {
			t.Fatalf("fresh compiler was masked by reviewed installation: %v", got)
		}
		if evidence.Sources["campaigns"].Metadata["freshness"] != "unknown" {
			t.Fatal("registry refresh claimed campaign freshness")
		}
	}
	if requests.Load() != 10 {
		t.Fatalf("forced inventory skipped remote reread: %d", requests.Load())
	}
}
