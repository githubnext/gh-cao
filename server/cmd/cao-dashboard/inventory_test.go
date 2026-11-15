package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
	limit := collectnative.InventoryMaxBytes
	content, err := readBoundedInventory(strings.NewReader(strings.Repeat("x", limit)))
	if err != nil || len(content) != limit {
		t.Fatalf("input at the limit: len=%d err=%v", len(content), err)
	}
	if _, err := readBoundedInventory(strings.NewReader(strings.Repeat("x", limit+1))); !errors.Is(err, collectnative.ErrCapacity) {
		t.Fatalf("oversized input error = %v, want ErrCapacity", err)
	}
	failure := errors.New("read failed")
	if _, err := readBoundedInventory(iotest.ErrReader(failure)); !errors.Is(err, failure) {
		t.Fatalf("reader error = %v, want %v", err, failure)
	}
}

func TestBuildNativeInventoryRejectsMalformedAndMismatchedDigest(t *testing.T) {
	if _, err := buildNativeInventory([]byte("not json"), strings.Repeat("0", 64)); err == nil ||
		err.Error() != "invalid native inventory document" {
		t.Fatalf("malformed document error = %v", err)
	}
	document := fmt.Sprintf(`{"version":1,"sourceRepository":"octo/ops","sourceSHA":%q,"policySHA":%q,"controlRepository":"octo/ops","policyResolution":"available","sources":{}}`,
		strings.Repeat("a", 40), strings.Repeat("b", 40))
	if _, err := buildNativeInventory([]byte(document), strings.Repeat("0", 64)); err == nil {
		t.Fatal("document with a mismatched digest was accepted")
	}
}
