package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
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
