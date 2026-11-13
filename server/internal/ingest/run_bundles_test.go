package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestManifestedRunBundlesAndExplicitReconciliation(t *testing.T) {
	ctx, store, config := ingestTestStoreConfig(t)
	directory, _ := testutil.RecentSnapshot(t, "../../testdata/deployed-subset")
	options := Options{DatabaseQueriesPath: "../../../dashboard/site/src/data/queries/database.json"}
	before, err := Run(ctx, store, directory, options)
	if err != nil {
		t.Fatal(err)
	}
	selected, _, err := store.ExecuteSQLPlan(ctx, []query.Definition{{
		Name: "bundle-run", From: "$runs", Select: []query.SelectedField{
			{Field: "id"}, {Field: "repositoryId"}, {Field: "workflowId"}, {Field: "attempt"},
		},
	}}, []string{"bundle-run"})
	if err != nil || len(selected["bundle-run"].Rows) != 1 {
		t.Fatalf("fixture Run unavailable: %v", err)
	}
	run := selected["bundle-run"].Rows[0]
	if run["attempt"] == nil {
		run["attempt"] = 1
	}
	run["inputTokens"] = 987654
	run["updatedAt"] = time.Now().UTC().Truncate(time.Microsecond)
	publication := postgresx.RunBundlePublication{
		Version: 1, ObservedAt: time.Now().UTC().Truncate(time.Microsecond),
		Runs: []postgresx.RunBundle{{Run: run}},
	}
	content, err := json.Marshal(publication)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, _, err := ValidateManifest(directory)
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- Fixed manifest basename in the test-owned temporary snapshot.
	original, err := os.ReadFile(filepath.Join(directory, "payload-hashes.json"))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(content)
	manifest[RunBundlesFile] = hex.EncodeToString(hash[:])
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []struct {
		content []byte
		message string
	}{
		{bytes.Replace(content, []byte(`"version":1`), []byte(`"version":1,"unexpected":true`), 1), `unknown field "unexpected"`},
		{append(bytes.Clone(content), []byte(` {}`)...), "trailing JSON"},
	} {
		digest := sha256.Sum256(invalid.content)
		manifest[RunBundlesFile] = hex.EncodeToString(digest[:])
		data, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filepath.Join(directory, "payload-hashes.json"), data)
		writeTestFile(t, filepath.Join(directory, RunBundlesFile), invalid.content)
		if _, err := RunBundles(ctx, store, directory, options); err == nil || !strings.Contains(err.Error(), invalid.message) {
			t.Fatalf("malformed envelope was not rejected with %q: %v", invalid.message, err)
		}
	}
	writeTestFile(t, filepath.Join(directory, "payload-hashes.json"), encoded)
	writeTestFile(t, filepath.Join(directory, RunBundlesFile), append(content, '\n'))
	if _, err := RunBundles(ctx, store, directory, options); err == nil {
		t.Fatal("tampered delta bytes were accepted")
	}
	unchanged, err := store.State(ctx)
	if err != nil || unchanged.Revision != before.Revision {
		t.Fatalf("rejected bytes changed publication: %+v %v", unchanged, err)
	}
	writeTestFile(t, filepath.Join(directory, RunBundlesFile), content)
	_, runs, records, err := readManifest(directory)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := snapshotIdentity(directory, manifest, runs, records)
	if err != nil {
		t.Fatal(err)
	}
	// A new adapter must recover the durable receipt, without a process cache.
	reopened, err := postgresx.NewConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if verified, err := reopened.HasVerifiedSnapshot(ctx, identity); err != nil || !verified {
		t.Fatalf("verified snapshot identity was not durable: %v %v", verified, err)
	}
	store = reopened
	shardPath := filepath.Join(directory, filepath.FromSlash(runs[0]))
	// #nosec G304 -- Validated manifest path beneath the test-owned snapshot.
	shard, err := os.ReadFile(shardPath)
	if err != nil {
		t.Fatal(err)
	}
	// Zero unrelated shard reads: the delta uses verified database content even
	// when its recovery file is unavailable. A full snapshot may not do so.
	if err := os.Remove(shardPath); err != nil {
		t.Fatal(err)
	}
	after, err := RunBundles(ctx, store, directory, options)
	if err != nil || after.Revision != before.Revision+1 || after.Counts["$runs"] != before.Counts["$runs"] {
		t.Fatalf("verified delta was not published: %+v %v", after, err)
	}
	replayed, err := RunBundles(ctx, store, directory, options)
	if err != nil || !reflect.DeepEqual(after, replayed) {
		t.Fatalf("verified replay advanced publication: %+v %v", replayed, err)
	}
	if _, err := Run(ctx, store, directory, options); err == nil {
		t.Fatal("full snapshot trusted a missing recovery shard")
	}
	writeTestFile(t, shardPath, []byte("corrupt recovery shard\n"))
	if _, err := Run(ctx, store, directory, options); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("full snapshot trusted corrupt recovery bytes: %v", err)
	}
	manifest[runs[0]] = strings.Repeat("a", 64)
	changed, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(directory, "payload-hashes.json"), changed)
	if _, err := RunBundles(ctx, store, directory, options); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("changed snapshot identity bypassed verification: %v", err)
	}
	unchanged, err = store.State(ctx)
	if err != nil || unchanged.Revision != after.Revision {
		t.Fatalf("failed recovery changed publication: %+v %v", unchanged, err)
	}
	changedShard := append(bytes.Clone(shard), '\n')
	changedHash := sha256.Sum256(changedShard)
	manifest[runs[0]] = hex.EncodeToString(changedHash[:])
	manifest[RunBundlesFile] = hex.EncodeToString(hash[:])
	changed, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, shardPath, changedShard)
	writeTestFile(t, filepath.Join(directory, "payload-hashes.json"), changed)
	if recovered, err := RunBundles(ctx, store, directory, options); err != nil || recovered.Revision != after.Revision {
		t.Fatalf("verified changed snapshot did not recover: %+v %v", recovered, err)
	}
	writeTestFile(t, shardPath, []byte("corrupt changed recovery shard\n"))
	if _, err := RunBundles(ctx, store, directory, options); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("fallback granted an unchecked mutable-path receipt: %v", err)
	}
	writeTestFile(t, shardPath, shard)
	writeTestFile(t, filepath.Join(directory, "payload-hashes.json"), encoded)
	if recovered, err := RunBundles(ctx, store, directory, options); err != nil || recovered.Revision != after.Revision {
		t.Fatalf("restored snapshot did not recover: %+v %v", recovered, err)
	}
	if _, err := store.PublishVerifiedRunBundles(ctx, publication, "foreign-snapshot"); !errors.Is(err, postgresx.ErrSnapshotIdentityChanged) {
		t.Fatalf("locked publication did not fence a foreign snapshot: %v", err)
	}
	writeTestFile(t, filepath.Join(directory, "payload-hashes.json"), original)
	if _, err := RunBundles(ctx, store, directory, options); err == nil {
		t.Fatal("unmanifested stale sidecar was reused")
	}

	reconciled, err := Run(ctx, store, directory, options)
	if err != nil || reconciled.Revision != after.Revision+1 ||
		reconciled.DataRevision != before.DataRevision || !reflect.DeepEqual(reconciled.Counts, before.Counts) {
		t.Fatalf("explicit final snapshot did not reconcile authoritative input: %+v %v", reconciled, err)
	}
	foreign, err := postgresx.NewConfig(ctx, config, "another-source")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = foreign.Close() })
	if verified, err := foreign.HasVerifiedSnapshot(ctx, identity); err != nil || verified {
		t.Fatalf("verification receipt leaked across namespaces: %v %v", verified, err)
	}
	writer, err := store.BeginIngestion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(ctx)
	if _, err := writer.Publish(ctx, "another-full-writer"); err != nil {
		t.Fatal(err)
	}
	if verified, err := store.HasVerifiedSnapshot(ctx, identity); err != nil || verified {
		t.Fatalf("unverified full writer retained receipt: %v %v", verified, err)
	}
	if _, err := store.PublishVerifiedRunBundles(ctx, publication, identity); !errors.Is(err, postgresx.ErrSnapshotIdentityChanged) {
		t.Fatalf("replacement full writer did not fence prior identity: %v", err)
	}
}

func TestSnapshotIdentityScopesVerifiedContent(t *testing.T) {
	first, second := scratchDirectory(t), scratchDirectory(t)
	runs := []string{"gh-aw-logs-runs/a.jsonl"}
	manifest := Manifest{runs[0]: strings.Repeat("a", 64)}
	identity, err := snapshotIdentity(first, manifest, runs)
	if err != nil {
		t.Fatal(err)
	}
	other, err := snapshotIdentity(second, manifest, runs)
	if err != nil || other == identity {
		t.Fatalf("snapshot identity did not bind source: %q %v", other, err)
	}
	manifest[RunBundlesFile] = strings.Repeat("b", 64)
	same, err := snapshotIdentity(first, manifest, runs)
	if err != nil || same != identity {
		t.Fatalf("sidecar invalidated immutable snapshot identity: %q %v", same, err)
	}
	manifest[runs[0]] = strings.Repeat("c", 64)
	changed, err := snapshotIdentity(first, manifest, runs)
	if err != nil || changed == identity {
		t.Fatalf("changed shard retained verified identity: %q %v", changed, err)
	}
}

func TestRunBundleValidationPrecedesMutation(t *testing.T) {
	ctx, store := ingestTestStore(t)
	for _, publication := range []postgresx.RunBundlePublication{
		{Version: 1, ObservedAt: time.Now(), Runs: []postgresx.RunBundle{{Run: model.Row{"id": "run"}}}},
		{Version: 2, ObservedAt: time.Now().UTC().Truncate(time.Microsecond)},
	} {
		if _, err := store.PublishRunBundles(ctx, publication); err == nil {
			t.Fatal("invalid delta contract was accepted")
		}
	}
	state, err := store.State(ctx)
	if err != nil || state.Ready {
		t.Fatalf("invalid input initialized canonical publication: %+v %v", state, err)
	}
}
