package main

import (
	"bytes"
	"context"
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

func TestResolveMarketplacePolicy(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("policy without marketplace resolves no registries", func(t *testing.T) {
		result, err := resolveMarketplacePolicy(context.Background(), write("empty.json", `{}`))
		if err != nil || result == nil {
			t.Fatalf("got %+v, %v", result, err)
		}
		if len(result.Packages) != 0 || len(result.Diagnostics) != 0 {
			t.Fatalf("expected empty result, got %+v", result)
		}
	})

	t.Run("unavailable registry yields one diagnostic", func(t *testing.T) {
		t.Setenv("CAO_TEST_MARKETPLACE_UNSET_PAT", "")
		policy := write("private.json", `{"control-plane":{"marketplace":{"registries":[
			{"id":"private","repository":"octo/catalog","ref":"main","auth":{"type":"pat","secret":"CAO_TEST_MARKETPLACE_UNSET_PAT"}}
		]}}}`)
		result, err := resolveMarketplacePolicy(context.Background(), policy)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Packages) != 0 || len(result.Diagnostics) != 1 || result.Diagnostics[0].RegistryID != "private" || result.Diagnostics[0].Status != "unavailable" {
			t.Fatalf("got %+v", result)
		}
	})

	t.Run("missing policy file is rejected", func(t *testing.T) {
		result, err := resolveMarketplacePolicy(context.Background(), filepath.Join(dir, "missing.json"))
		if err == nil || result != nil {
			t.Fatalf("got %+v, %v; want error", result, err)
		}
	})

	t.Run("invalid policy JSON is rejected", func(t *testing.T) {
		result, err := resolveMarketplacePolicy(context.Background(), write("invalid.json", `{"control-plane":`))
		if err == nil || result != nil {
			t.Fatalf("got %+v, %v; want error", result, err)
		}
	})
}
