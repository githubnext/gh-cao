package postgresx

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
)

func TestNativeInventoryRestoresExactSharedReviewedDocument(t *testing.T) {
	store, config := nativeTestStore(t)
	snapshot, _ := nativeInventoryFixture(t, "Security")
	if err := store.Acquisition().PinInventoryConfiguration(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	replica, err := NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = replica.Close() }()
	restored, err := replica.Acquisition().LoadInventoryConfiguration(t.Context(), snapshot.Digest)
	if err != nil || restored.Provenance != snapshot.Provenance || restored.Digest != snapshot.Digest ||
		restored.ControlRepository() != snapshot.ControlRepository() || !bytes.Equal(restored.Bytes(), snapshot.Bytes()) {
		t.Fatalf("shared configuration changed bytes or provenance: %+v %v", restored, err)
	}
	copied := restored.Bytes()
	copied[0] = 'x'
	if err := restored.Validate(); err != nil {
		t.Fatalf("caller mutated the reviewed shared document: %v", err)
	}
	if _, err := replica.Acquisition().LoadInventoryConfiguration(t.Context(), strings.Repeat("f", 64)); !errors.Is(err, collectnative.ErrClaimLost) {
		t.Fatalf("replica silently chose a different reviewed deployment: %v", err)
	}
	if _, err := store.db.ExecContext(t.Context(), `UPDATE cao_acquisition_inventory_config SET content=$2
		WHERE namespace=$1`, store.namespace, []byte(`{"corrupt":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.Acquisition().LoadInventoryConfiguration(t.Context(), snapshot.Digest); err == nil {
		t.Fatal("corrupt shared configuration bypassed its reviewed digest")
	}
}
