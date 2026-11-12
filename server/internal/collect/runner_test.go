package collect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fakeTokens satisfies TokenProvider without contacting GitHub.

// writeFakeBinary installs an executable that records its arguments and
// environment so the test can assert the collection command line without
// running the real GitHub CLI.

func TestLakeUsesThePublishedSnapshotLayout(t *testing.T) {
	lake := Lake{Directory: t.TempDir()}
	if err := lake.Prepare(); err != nil {
		t.Fatal(err)
	}
	populated, err := lake.Populated()
	if err != nil {
		t.Fatal(err)
	}
	if populated {
		t.Fatal("an empty lake must not report as populated")
	}
	layout := map[string]string{
		"shards":   "gh-aw-logs-shards",
		"runs":     "gh-aw-logs-runs",
		"records":  "gh-aw-logs-records",
		"manifest": "payload-hashes.json",
		"sources":  "inventory-sources.json",
	}
	actual := map[string]string{
		"shards":   filepath.Base(lake.ShardDirectory()),
		"runs":     filepath.Base(lake.RunsDirectory()),
		"records":  filepath.Base(lake.RecordsDirectory()),
		"manifest": filepath.Base(lake.ManifestPath()),
		"sources":  filepath.Base(lake.InventoryPath()),
	}
	for key, want := range layout {
		if actual[key] != want {
			t.Fatalf("%s directory = %q, want %q", key, actual[key], want)
		}
	}
	// Populated means projectable: compacted run shards plus a manifest. Raw
	// collected shards alone are not enough, because projection validates the
	// manifest before reading anything.
	raw := filepath.Join(lake.ShardDirectory(), lake.ShardPrefix("octo/api")+"1.jsonl")
	if err := WriteFileAtomic(raw, []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	populated, err = lake.Populated()
	if err != nil {
		t.Fatal(err)
	}
	if populated {
		t.Fatal("raw shards without a manifest must not report as populated")
	}
	if err := WriteFileAtomic(filepath.Join(lake.RunsDirectory(), "runs.jsonl"), []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(lake.ManifestPath(), []byte("{}")); err != nil {
		t.Fatal(err)
	}
	populated, err = lake.Populated()
	if err != nil {
		t.Fatal(err)
	}
	if !populated {
		t.Fatal("a lake holding shards must report as populated")
	}
}

func TestWriteFileAtomicLeavesNoPartialFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "nested", "manifest.json")
	payload, err := json.Marshal(map[string]string{"a": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, payload); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "manifest.json" {
		t.Fatalf("unexpected directory contents: %v", entries)
	}
}
