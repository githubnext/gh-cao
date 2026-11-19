package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/marketplace"
)

func TestMarketplaceResolverReportsMissingCredentialWithoutLeakingEnvironment(t *testing.T) {
	t.Setenv("CAO_TEST_MARKETPLACE_PAT", "")
	t.Setenv("GH_TOKEN", "unrelated-secret-must-not-be-used")
	policy := filepath.Join(t.TempDir(), "cao.json")
	content := `{"control-plane":{"marketplace":{"registries":[
		{"id":"private","repository":"octo/catalog","ref":"main","auth":{"type":"pat","secret":"CAO_TEST_MARKETPLACE_PAT"}}
	]}}}`
	if err := os.WriteFile(policy, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"__marketplace", "--policy", policy})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result marketplace.Result
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Packages) != 0 || len(result.Diagnostics) != 1 || result.Diagnostics[0].Status != "unavailable" {
		t.Fatalf("missing registry secret did not fail closed: %+v", result)
	}
	if strings.Contains(output.String(), "unrelated-secret") || strings.Contains(output.String(), "CAO_TEST_MARKETPLACE_PAT") {
		t.Fatal("resolver exposed registry environment")
	}
}
