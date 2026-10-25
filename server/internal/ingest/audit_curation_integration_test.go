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

	"github.com/jackc/pgx/v5/stdlib"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestUnchangedArtifactRevisionCuratesExistingAudits(t *testing.T) {
	ctx, store, config := ingestTestStoreConfig(t)
	options := Options{DatabaseQueriesPath: "../../../dashboard/site/src/data/queries/database.json"}
	directory, _ := testutil.RecentSnapshot(t, "../../testdata/deployed-subset")
	first, err := Run(ctx, store, directory, options)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config.Copy())
	defer func() { _ = admin.Close() }()
	if _, err := admin.ExecContext(ctx, `INSERT INTO audits(namespace,ordinal,present_fields,id,run_id,run_at,source,type,status,summary,timestamp)
		SELECT namespace,ordinal+1,present_fields,'legacy-metadata',run_id,run_at,'agent','agent.session','completed','',timestamp
		FROM audits WHERE id='audit:424242:complete'`); err != nil {
		t.Fatal(err)
	}
	second, err := Run(ctx, store, directory, options)
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != first.Revision+1 || second.DataRevision != first.DataRevision || second.EvaluatedAt != first.EvaluatedAt || !reflect.DeepEqual(second.Counts, first.Counts) {
		t.Fatalf("unchanged input did not publish cleanup: before=%+v after=%+v", first, second)
	}
	third, err := Run(ctx, store, directory, options)
	if err != nil || !reflect.DeepEqual(third, second) {
		t.Fatalf("repeat cleanup was not idempotent: %+v %v", third, err)
	}
}

func TestAuditCurationValidatesEveryTransportedRecord(t *testing.T) {
	ctx, store := ingestTestStore(t)
	directory := scratchDirectory(t)
	recent, _ := testutil.RecentSnapshot(t, "../../testdata/deployed-subset")
	// #nosec G304 -- RecentSnapshot returns this test's owned fixture directory.
	runContent, err := os.ReadFile(filepath.Join(recent, "gh-aw-logs-runs/subset.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	recordContent := []byte("{\"kind\":\"metadata\",\"phase\":\"records\",\"records\":1}\n" +
		"{\"kind\":\"record\",\"collection\":\"audits\",\"record\":{\"id\":\"curate\",\"runId\":\"run:424242\",\"source\":\"agent\",\"type\":\"agent.session\",\"status\":\"completed\",\"summary\":\"\",\"timestamp\":\"2026-01-01T00:00:00Z\"}}\n")
	manifest := Manifest{}
	for name, content := range map[string][]byte{"gh-aw-logs-runs/subset.jsonl": runContent, "gh-aw-logs-records/subset.jsonl": recordContent} {
		writeTestFile(t, filepath.Join(directory, name), content)
		sum := sha256.Sum256(content)
		manifest[name] = hex.EncodeToString(sum[:])
	}
	manifestContent, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(directory, "payload-hashes.json"), manifestContent)
	writeTestFile(t, filepath.Join(directory, "inventory-sources.json"), []byte("{}"))
	options := Options{DatabaseQueriesPath: "../../../dashboard/site/src/data/queries/database.json"}
	first, err := Run(ctx, store, directory, options)
	if err != nil || first.Counts["$audits"] != 0 {
		t.Fatalf("curated ingestion failed: %+v %v", first, err)
	}
	sources, _, err := store.ExecuteSQLPlan(ctx, []query.Definition{{Name: "transport", From: "$transactions", Select: []query.SelectedField{
		{Field: "kind"}, {Field: "records"}, {Field: "committedRecords"},
	}}}, []string{"transport"})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, row := range sources["transport"].Rows {
		if row["kind"] == "records" {
			found = true
			if row["records"] != json.Number("1") || row["committedRecords"] != json.Number("1") {
				t.Fatalf("curation changed transport accounting: %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("record transport was not committed")
	}
	recordContent = bytes.Replace(recordContent, []byte(`"records":1`), []byte(`"records":0`), 1)
	writeTestFile(t, filepath.Join(directory, "gh-aw-logs-records/subset.jsonl"), recordContent)
	sum := sha256.Sum256(recordContent)
	manifest["gh-aw-logs-records/subset.jsonl"] = hex.EncodeToString(sum[:])
	manifestContent, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(directory, "payload-hashes.json"), manifestContent)
	if _, err := Run(ctx, store, directory, options); err == nil {
		t.Fatal("discardable records bypassed transport count validation")
	}
	after, err := store.State(ctx)
	if err != nil || !reflect.DeepEqual(stateResult(after), first) {
		t.Fatalf("invalid transport changed publication: %+v %v", after, err)
	}
}

func TestUnsupportedAuditEvidencePreservesPublishedIngestion(t *testing.T) {
	ctx, store := ingestTestStore(t)
	directory, _ := testutil.RecentSnapshot(t, "../../testdata/deployed-subset")
	first, err := Run(ctx, store, directory, Options{DatabaseQueriesPath: "../../../dashboard/site/src/data/queries/database.json"})
	if err != nil {
		t.Fatal(err)
	}
	writer, err := store.BeginIngestion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(ctx)
	if err := writer.Append(ctx, "$audits", model.Row{"id": "audit", "runId": "run", "source": "gh-aw-logs",
		"type": "workflow_run_usage", "status": "observed", "summary": "AIC 0", "runAttempt": 2, "timestamp": "2026-01-01T00:00:00Z"}); err == nil {
		t.Fatal("unsupported evidence was silently lost before curation")
	}
	writer.Abort(ctx)
	after, err := store.State(ctx)
	if err != nil || !reflect.DeepEqual(stateResult(after), first) {
		t.Fatalf("lossy projection failure changed publication: %+v %v", after, err)
	}
}

func TestAuditCurationDoesNotHideTransportedOrphan(t *testing.T) {
	ctx, store := ingestTestStore(t)
	options := Options{DatabaseQueriesPath: "../../../dashboard/site/src/data/queries/database.json"}
	recent, _ := testutil.RecentSnapshot(t, "../../testdata/deployed-subset")
	before, err := Run(ctx, store, recent, options)
	if err != nil {
		t.Fatal(err)
	}
	directory := scratchDirectory(t)
	// #nosec G304 -- RecentSnapshot returns this test's owned fixture directory.
	runContent, err := os.ReadFile(filepath.Join(recent, "gh-aw-logs-runs/subset.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	recordContent := []byte("{\"kind\":\"metadata\",\"phase\":\"records\",\"records\":1}\n" +
		"{\"kind\":\"record\",\"collection\":\"audits\",\"record\":{\"id\":\"eligible-orphan\",\"runId\":\"run:missing\",\"timestamp\":\"2026-01-01T00:00:00Z\",\"source\":\"gh-aw-logs\",\"type\":\"workflow_run_working_set\",\"status\":\"observed\",\"summary\":\"Working set measured\"}}\n")
	manifest := Manifest{}
	for name, content := range map[string][]byte{"gh-aw-logs-runs/subset.jsonl": runContent, "gh-aw-logs-records/subset.jsonl": recordContent} {
		writeTestFile(t, filepath.Join(directory, name), content)
		sum := sha256.Sum256(content)
		manifest[name] = hex.EncodeToString(sum[:])
	}
	manifestContent, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(directory, "payload-hashes.json"), manifestContent)
	writeTestFile(t, filepath.Join(directory, "inventory-sources.json"), []byte("{}"))
	if _, err := Run(ctx, store, directory, options); err == nil || !strings.Contains(err.Error(), "run-owned audits references a missing or ambiguous parent") {
		t.Fatalf("eligible orphan bypassed transported-record parent validation: %v", err)
	}
	after, err := store.State(ctx)
	if err != nil || !reflect.DeepEqual(stateResult(after), before) {
		t.Fatalf("eligible orphan changed the published namespace: %+v %v", after, err)
	}
}
