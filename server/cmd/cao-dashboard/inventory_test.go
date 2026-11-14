package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
)

func TestReadNativeInventoryValidatesBeforeProvisioning(t *testing.T) {
	document := fmt.Sprintf(`{
		"version":1,"sourceRepository":"octo/ops","sourceSHA":%q,"policySHA":%q,
		"controlRepository":"octo/ops","policyResolution":"available",
		"sources":{
			"campaigns":{"source":"campaigns","rows":[]},
			"repositories":{"source":"repositories","rows":[]},
			"workflows":{"source":"workflows","rows":[]},
			"configuration-policy":{"source":"configuration-policy","rows":[{
				"path":".github/workflows/cao.json",
				"raw":"{\"version\":1,\"control-plane\":{\"campaigns\":{}}}",
				"document":{"version":1,"control-plane":{"campaigns":{}}},
				"diagnostics":[{"severity":"valid","title":"Policy is valid"}]
			}]}
		}
	}`, strings.Repeat("a", 40), strings.Repeat("b", 40))
	sum := sha256.Sum256([]byte(document))
	digest := hex.EncodeToString(sum[:])
	snapshot, err := readNativeInventory(strings.NewReader(document), digest)
	if err != nil || snapshot.Digest != digest {
		t.Fatalf("valid reviewed document: %v", err)
	}
	for _, content := range []string{
		document + " ",
		strings.Replace(document, `"available"`, `"unavailable"`, 1),
		`{"version":1}`,
	} {
		if _, err := readNativeInventory(strings.NewReader(content), digest); err == nil {
			t.Fatal("changed or incomplete inventory was accepted")
		}
	}
	unavailable := strings.Replace(document, `"available"`, `"unavailable"`, 1)
	sum = sha256.Sum256([]byte(unavailable))
	if _, err := readNativeInventory(strings.NewReader(unavailable), hex.EncodeToString(sum[:])); err == nil {
		t.Fatal("unavailable policy evidence was provisioned")
	}
}

func TestReadBoundedInventoryEnforcesCapacity(t *testing.T) {
	exact := strings.Repeat("x", collectnative.InventoryMaxBytes)
	content, err := readBoundedInventory(strings.NewReader(exact))
	if err != nil || len(content) != collectnative.InventoryMaxBytes {
		t.Fatalf("input at capacity: len=%d err=%v", len(content), err)
	}
	if _, err := readBoundedInventory(strings.NewReader(exact + "x")); !errors.Is(err, collectnative.ErrCapacity) {
		t.Fatalf("input over capacity error = %v, want ErrCapacity", err)
	}
	if _, err := readBoundedInventory(strings.NewReader("")); err != nil {
		t.Fatalf("empty input must be left to snapshot validation: %v", err)
	}
}

func TestReadBoundedInventoryPropagatesReadFailure(t *testing.T) {
	failure := errors.New("read failed")
	if _, err := readBoundedInventory(iotest.ErrReader(failure)); !errors.Is(err, failure) {
		t.Fatalf("read failure error = %v, want %v", err, failure)
	}
	if _, err := readNativeInventory(io.MultiReader(strings.NewReader("{"), iotest.ErrReader(failure)), strings.Repeat("0", 64)); !errors.Is(err, failure) {
		t.Fatalf("readNativeInventory read failure error = %v, want %v", err, failure)
	}
}

func TestDecodeNativeInventoryRejectsInvalidDocuments(t *testing.T) {
	digest := strings.Repeat("0", 64)
	if _, err := decodeNativeInventory([]byte("not json"), digest); err == nil || err.Error() != "invalid native inventory document" {
		t.Fatalf("malformed JSON error = %v", err)
	}
	if _, err := decodeNativeInventory([]byte(`{"version":1}`), digest); err == nil {
		t.Fatal("incomplete document with a non-matching digest was accepted")
	}
	if _, err := readNativeInventory(strings.NewReader(strings.Repeat("x", collectnative.InventoryMaxBytes+1)), digest); !errors.Is(err, collectnative.ErrCapacity) {
		t.Fatalf("oversized document error = %v, want ErrCapacity", err)
	}
}
