package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestEmptyPostgresRebuildAndFailedIngestionPreservesCurrentData(t *testing.T) {
	ctx, store := ingestTestStore(t)
	directory, _ := testutil.RecentSnapshot(t, "../../testdata/deployed-subset")
	options := Options{DatabaseQueriesPath: "../../../dashboard/site/src/data/queries/database.json"}
	before, err := Run(ctx, store, directory, options)
	if err != nil {
		t.Fatal(err)
	}
	name := "gh-aw-logs-records/subset.jsonl"
	// #nosec G304 -- this path belongs to this test's owned scratch directory.
	content, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		t.Fatal(err)
	}
	content = append(content, []byte("{\"kind\":\"record\",\"collection\":\"domains\",\"record\":{\"id\":\"orphan\",\"runId\":\"missing\"}}\n")...)
	writeTestFile(t, filepath.Join(directory, name), content)
	// #nosec G304 -- this path belongs to this test's owned scratch directory.
	manifestContent, err := os.ReadFile(filepath.Join(directory, "payload-hashes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestContent, &manifest); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	manifest[name] = hex.EncodeToString(sum[:])
	manifestContent, _ = json.Marshal(manifest)
	writeTestFile(t, filepath.Join(directory, "payload-hashes.json"), manifestContent)
	if _, err := Run(ctx, store, directory, options); err == nil {
		t.Fatal("orphan shard published")
	}
	after, err := store.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.DataRevision != before.DataRevision || !reflect.DeepEqual(after.Counts, before.Counts) {
		t.Fatalf("failed ingestion changed publication: %+v", after)
	}
}
