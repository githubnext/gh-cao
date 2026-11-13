package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	ctx, store := ingestTestStore(t)
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
	after, err := RunBundles(ctx, store, directory, options)
	if err != nil || after.Revision != before.Revision+1 || after.Counts["$runs"] != before.Counts["$runs"] {
		t.Fatalf("verified delta was not published: %+v %v", after, err)
	}
	replayed, err := RunBundles(ctx, store, directory, options)
	if err != nil || !reflect.DeepEqual(after, replayed) {
		t.Fatalf("verified replay advanced publication: %+v %v", replayed, err)
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
