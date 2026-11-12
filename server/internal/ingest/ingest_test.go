package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

func scratchDirectory(t *testing.T) string {
	t.Helper()
	path := filepath.Join(".scratch", fmt.Sprintf("native-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(path) })
	return path
}
func writeTestFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	// #nosec G703 -- paths are owned fixtures below this test's scratch directory.
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestValidateManifestVerifiesHashesAndRunShard(t *testing.T) {
	directory := scratchDirectory(t)
	name := "gh-aw-logs-runs/runs.jsonl"
	content := []byte("{\"kind\":\"metadata\"}\n")
	writeTestFile(t, filepath.Join(directory, name), content)
	sum := sha256.Sum256(content)
	manifest, _ := json.Marshal(Manifest{name: hex.EncodeToString(sum[:])})
	writeTestFile(t, filepath.Join(directory, "payload-hashes.json"), manifest)
	_, runs, records, err := ValidateManifest(directory)
	if err != nil || len(runs) != 1 || len(records) != 0 {
		t.Fatalf("invalid phases: %v %v %v", runs, records, err)
	}
	writeTestFile(t, filepath.Join(directory, name), []byte("changed\n"))
	if _, _, _, err := ValidateManifest(directory); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("hash mismatch accepted: %v", err)
	}
}
func TestValidateManifestFailsClosed(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "gh-aw-logs-shards/raw.jsonl", "inventory-sources.json"} {
		directory := scratchDirectory(t)
		content, _ := json.Marshal(Manifest{name: strings.Repeat("a", 64)})
		writeTestFile(t, filepath.Join(directory, "payload-hashes.json"), content)
		if _, _, _, err := ValidateManifest(directory); err == nil {
			t.Fatalf("unsafe/incomplete manifest accepted: %s", name)
		}
	}
}

func TestLocalIngestionReceiptsRejectForeignTransactions(t *testing.T) {
	for _, row := range []model.Row{
		{"id": "unrelated", "kind": "local-dashboard-ingestion"},
		{"id": "local-dashboard-ingestion", "kind": "unrelated"},
	} {
		t.Run(row["id"].(string)+"/"+row["kind"].(string), func(t *testing.T) {
			ctx, store := ingestTestStore(t)
			directory := scratchDirectory(t)
			record, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			manifest := Manifest{}
			for name, content := range map[string][]byte{
				"inventory-sources.json":      []byte("{}"),
				"gh-aw-logs-runs/local.jsonl": []byte("{\"kind\":\"metadata\",\"phase\":\"runs\",\"records\":0}\n"),
				"gh-aw-logs-records/local.jsonl": []byte("{\"kind\":\"metadata\",\"phase\":\"records\",\"records\":1}\n" +
					"{\"kind\":\"record\",\"collection\":\"transactions\",\"record\":" + string(record) + "}\n"),
			} {
				writeTestFile(t, filepath.Join(directory, name), content)
				sum := sha256.Sum256(content)
				manifest[name] = hex.EncodeToString(sum[:])
			}
			content, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(directory, "payload-hashes.json"), content)
			_, err = Run(ctx, store, directory, Options{
				DatabaseQueriesPath: "../../../dashboard/site/src/data/queries/database.json",
			})
			if err == nil || !strings.Contains(err.Error(), "only the local ingestion receipt") {
				t.Fatalf("foreign bookkeeping transaction was not rejected: %v", err)
			}
		})
	}
}

func TestInventoryAdapterHasDeterministicNativeIdentities(t *testing.T) {
	source, row, err := inventoryRow("workflows", model.Row{"organization": "Octo", "repository": "API", "workflow": ".github/workflows/BUILD.lock.yml", "workflow-active": "true"})
	if err != nil {
		t.Fatal(err)
	}

	if source != "$workflows" || row["id"] != "workflow:octo%2Fapi%3A.github%2Fworkflows%2Fbuild.md" || row["repositoryId"] != "repository:octo%2Fapi" || row["state"] != "active" {
		t.Fatalf("normalization differs from declared inventory identity: %#v", row)
	}
	source, row, err = inventoryRow("campaigns", model.Row{
		"campaign":      "maintenance",
		"campaign-name": "Maintenance",
	})
	if err != nil {
		t.Fatal(err)
	}
	if source != "$campaigns" || row["id"] != "campaign:dashboard-sources:maintenance" ||
		row["slug"] != "maintenance" || row["name"] != "Maintenance" {
		t.Fatalf("campaign inventory does not map to canonical Postgres rows: %#v", row)
	}
}

func TestInventoryAdapterRejectsReportProjections(t *testing.T) {
	for _, name := range []string{"work-items", "security-findings", "outcomes", "mcp-calls"} {
		if _, _, err := inventoryRow(name, model.Row{"id": "supplied-report-row"}); err == nil {
			t.Fatalf("report projection %q admitted as canonical inventory", name)
		}
	}
}
