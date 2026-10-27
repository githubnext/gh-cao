package sqlitex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/internal/dashboardquery"

	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestSnapshotDSNUsesLocalFilePaths(t *testing.T) {
	for _, test := range []struct {
		path string
		want string
	}{
		{"/tmp/snapshot #1.sqlite", "/tmp/snapshot #1.sqlite"},
		{"C:/Users/developer/Local Data/snapshot #1.sqlite", "/C:/Users/developer/Local Data/snapshot #1.sqlite"},
		{"//localhost/share/snapshot.sqlite", "//localhost/share/snapshot.sqlite"},
	} {
		t.Run(test.path, func(t *testing.T) {
			location, err := url.Parse(snapshotDSN(test.path))
			if err != nil {
				t.Fatal(err)
			}
			if location.Scheme != "file" || location.Host != "" || location.Path != test.want ||
				location.Fragment != "" || location.Query().Get("mode") != "ro" {
				t.Fatalf("invalid local read-only snapshot URI: %s", location)
			}
		})
	}
}

func TestSnapshotSQLProjectionPreservesJSONAndFullRecordBudgets(t *testing.T) {
	path := testutil.SQLiteSnapshot(t, map[string][]model.Row{"repositories": {{
		"id": "repo", "yes": true, "no": false, "null": nil, "number": 1.5, "text": "quoted \"",
		"array": []any{"value", true}, "object": map[string]any{"key": false},
		"unused": strings.Repeat("x", 4096), "_storage": map[string]any{"private": true},
	}}})
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	err = store.WithReadTransaction(t.Context(), func(ctx context.Context, r dashboardstore.Reader) error {
		tx := r.(*reader).tx
		full, err := dashboardquery.ReadCollection(ctx, tx, "repositories", 10, 1<<20)
		if err != nil {
			return err
		}
		fields := []string{"id", "yes", "no", "null", "number", "text", "array", "object", "missing"}
		projected, err := dashboardquery.ReadCollectionFields(ctx, tx, "repositories", fields, 10, 1<<20)
		if err != nil {
			return err
		}
		delete(full[0], "unused")
		if !reflect.DeepEqual(projected, full) {
			t.Fatalf("SQL projection changed JSON types or missing fields: got=%v want=%v", projected, full)
		}
		if _, err := dashboardquery.ReadCollectionFields(ctx, tx, "repositories", []string{"id"}, 10, 100); err == nil {
			t.Fatal("projecting a small result bypassed the full-record input byte budget")
		}
		empty, err := dashboardquery.ReadCollectionFields(ctx, tx, "repositories", []string{}, 10, 1<<20)
		if err != nil || len(empty) != 1 || len(empty[0]) != 0 {
			t.Fatalf("empty projection changed cardinality: %v %v", empty, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotProjectionUnionsConsumerFieldsWithoutChangingRequestedSources(t *testing.T) {
	store, _ := localSnapshot(t)
	definitions := []query.Definition{
		{Name: "base", From: "$runs"},
		{Name: "one", From: "base", Select: []query.SelectedField{{Field: "id"}, {Field: "explicit"}}},
		{Name: "two", From: "base", Select: []query.SelectedField{{Field: "duration"}}},
	}
	err := store.WithReadTransaction(t.Context(), func(ctx context.Context, r dashboardstore.Reader) error {
		result, _, err := r.ExecuteSQLPlan(ctx, definitions, []string{"one", "two", "$runs"})
		if err != nil {
			return err
		}
		if result["two"].Rows[0]["duration"] != float64(2) || result["$runs"].Rows[0]["workflowId"] != "workflow:1" {
			t.Fatalf("consumer fields were dropped: %+v", result)
		}
		if _, present := result["one"].Rows[0]["explicit"]; present {
			t.Fatal("missing value became explicit null")
		}
		if value, present := result["one"].Rows[1]["explicit"]; !present || value != nil {
			t.Fatal("explicit null became absent")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotPlanReleasesCompletedInputs(t *testing.T) {
	store, _ := localSnapshot(t)
	definitions := make([]query.Definition, 12)
	from := "$repositories"
	for index := range definitions {
		definitions[index] = query.Definition{Name: fmt.Sprintf("step-%d", index), From: from}
		from = definitions[index].Name
	}
	err := store.WithReadTransaction(t.Context(), func(ctx context.Context, r dashboardstore.Reader) error {
		sources, metrics, err := r.ExecuteSQLPlan(ctx, definitions, []string{"$repositories", from})
		if err != nil {
			return err
		}
		if len(sources[from].Rows) != 2 || len(sources["$repositories"].Rows) != 2 {
			t.Fatalf("requested sources were released too early: %+v", sources)
		}
		if metrics.RetainedRows != 6 {
			t.Fatalf("completed DAG inputs remained retained: peak rows=%d, want 6", metrics.RetainedRows)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func localSnapshot(t *testing.T) (*Store, string) {
	t.Helper()
	path := testutil.SQLiteSnapshot(t, map[string][]model.Row{
		"repositories": {{"id": "repo:1", "owner": "octo", "name": "one"}, {"id": "repo:2", "owner": "octo", "name": "two"}},
		"workflows":    {{"id": "workflow:1", "repositoryId": "repo:1", "path": "one.md"}},
		"runs": {
			{"id": "run:2", "workflowId": "workflow:1", "repositoryId": "repo:1", "startedAt": "2026-10-01T12:00:00Z", "duration": 6, "explicit": nil},
			{"id": "run:1", "workflowId": "workflow:1", "repositoryId": "repo:1", "startedAt": "2026-10-01T10:00:00Z", "duration": 2, "_storage": map[string]any{"internal": true}},
		},
		"transactions": {{"id": "ingestion:1", "kind": "ingest-normalized-jsonl", "createdAt": "2026-10-02T00:00:00Z"}},
	})
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store, path
}

func TestSnapshotPlansAndReadOnlyState(t *testing.T) {
	store, path := localSnapshot(t)
	active, err := store.State(t.Context())
	if err != nil || !active.Ready || active.Counts["$runs"] != 2 || active.Counts["$transactions"] != 1 {
		t.Fatalf("state=%+v error=%v", active, err)
	}
	if active.EvaluatedAt.Format("2006-01-02") != "2026-10-02" || active.Revision > 1<<53 {
		t.Fatalf("invalid evaluation/revision: %+v", active)
	}
	definitions := []query.Definition{
		{Name: "joined", From: "$runs", Joins: []query.Join{{
			Source: "$repositories", Type: "left", On: []query.JoinKey{{Left: "repositoryId", Right: "id"}},
			Fields: []query.SelectedField{{Field: "name", As: "repository"}},
		}}, Filter: &query.Filter{Search: &query.Search{Fields: []string{"repository"}, Query: "ONE"}}},
		{Name: "summary", From: "joined", Aggregate: &query.Aggregate{
			By: []string{"repository"}, Values: []query.AggregateValue{{Field: "duration", As: "total", Reducer: "sum"}},
		}},
	}
	err = store.WithReadTransaction(t.Context(), func(ctx context.Context, r dashboardstore.Reader) error {
		sources, metrics, err := r.ExecuteSQLPlanWithOptions(ctx, definitions, []string{"$runs", "summary"}, dashboardstore.ExecutionOptions{
			Pages: map[string]dashboardstore.Page{"$runs": {Limit: 1}},
		})
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(sources["summary"].Rows, []model.Row{{"repository": "one", "total": float64(8)}}) {
			t.Fatalf("incorrect aggregation: %+v", sources["summary"])
		}
		run := sources["$runs"]
		if len(run.Rows) != 1 || run.Rows[0]["id"] != "run:1" || run.Metadata["total-row-count"] != 2 {
			t.Fatalf("incorrect key order/page: %+v", run)
		}
		if _, exists := run.Rows[0]["_storage"]; exists {
			t.Fatal("storage-only fields escaped")
		}
		if metrics.QueryCount != 2 || metrics.Operations == 0 || sources["summary"].Metadata["completeness"] != "unknown" {
			t.Fatalf("invalid metrics/quality: %+v %+v", metrics, sources["summary"].Metadata)
		}
		if _, err := r.(*reader).tx.ExecContext(ctx, "DELETE FROM __idb_records"); err == nil {
			t.Fatal("read-only snapshot accepted a write")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	state, err := reopened.State(t.Context())
	if err != nil || state.Revision != active.Revision || state.DataRevision != active.DataRevision {
		t.Fatalf("revision did not survive reopening: %+v %v", state, err)
	}
	active.Counts["$runs"] = 999
	state, err = store.State(t.Context())
	if err != nil || state.Counts["$runs"] != 2 {
		t.Fatal("borrowed state map mutated the store")
	}
}

func TestSnapshotChangesAreObservedWithoutRestart(t *testing.T) {
	store, path := localSnapshot(t)
	before, err := store.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	err = store.WithReadTransaction(t.Context(), func(ctx context.Context, r dashboardstore.Reader) error {
		if _, err := r.State(ctx); err != nil {
			return err
		}
		if _, err := writer.ExecContext(ctx, `UPDATE __idb_records SET value=json_set(value,'$.duration',20)
			WHERE store_name='runs' AND json_extract(value,'$.id')='run:1'`); err != nil {
			return err
		}
		sources, _, err := r.ExecuteSQLPlan(ctx, nil, []string{"$runs"})
		if err != nil {
			return err
		}
		if sources["$runs"].Rows[0]["duration"] != float64(2) {
			t.Fatal("read transaction observed a concurrent update")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.State(t.Context())
	if err != nil || before.Revision == after.Revision || before.DataRevision == after.DataRevision {
		t.Fatalf("snapshot update not observed: before=%+v after=%+v error=%v", before, after, err)
	}
}

func TestSnapshotFailsClosed(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.sqlite")
	if _, err := Open(t.Context(), missing); err == nil {
		t.Fatal("missing snapshot was accepted")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing snapshot was created")
	}
	store, _ := localSnapshot(t)
	if err := store.WithReadTransaction(t.Context(), func(ctx context.Context, r dashboardstore.Reader) error {
		for _, definitions := range [][]query.Definition{
			{{Name: "bad", From: "$notRegistered"}},
			{{Name: "bad", From: "$runs", Union: []string{"$notRegistered"}}},
			{{Name: "bad", From: "$runs", Joins: []query.Join{{Source: "$notRegistered", Type: "left",
				On: []query.JoinKey{{Left: "id", Right: "id"}}}}}},
		} {
			if _, _, err := r.ExecuteSQLPlan(ctx, definitions, []string{"bad"}); err == nil || !strings.Contains(err.Error(), "not registered") {
				t.Fatalf("unregistered input produced success: %v", err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.State(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
}

func TestSnapshotAcceptsAdditiveDatabaseVersions(t *testing.T) {
	for version := 44; version <= dashboardquery.DatabaseVersion; version++ {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			path := testutil.SQLiteSnapshot(t, nil)
			writer, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.ExecContext(t.Context(), `UPDATE __idb_databases SET version=?`, version); err != nil {
				_ = writer.Close()
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			store, err := Open(t.Context(), path)
			if err != nil {
				t.Fatalf("additive database version %d was rejected: %v", version, err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSnapshotDiagnostics(t *testing.T) {
	store, path := localSnapshot(t)
	diagnostics, err := store.Diagnostics(t.Context())
	if err != nil || diagnostics.Counts["runs"] != 2 || len(diagnostics.RelationshipErrors) != 0 {
		t.Fatalf("diagnostics=%+v error=%v", diagnostics, err)
	}
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	if _, err := writer.ExecContext(t.Context(), `UPDATE __idb_records SET value=json_set(value,'$.workflowId','missing') WHERE store_name='runs'`); err != nil {
		t.Fatal(err)
	}
	diagnostics, err = store.Diagnostics(t.Context())
	if err != nil || len(diagnostics.RelationshipErrors) != 2 {
		t.Fatalf("broken references were not diagnosed: %+v %v", diagnostics, err)
	}
}

func TestSnapshotRuntimeSourcesUseServerRegistry(t *testing.T) {
	store, _ := localSnapshot(t)
	err := store.WithReadTransaction(t.Context(), func(ctx context.Context, r dashboardstore.Reader) error {
		for _, name := range []string{
			"collection-health", "github-quota-usage", "simulation-days",
			"cli-commands", "cli-command-summary", "cli-command-statuses",
			"$marketplacePackages",
		} {
			t.Run(name, func(t *testing.T) {
				calls := 0
				sources, _, err := r.ExecuteSQLPlanWithOptions(ctx, nil, []string{name}, dashboardstore.ExecutionOptions{
					Runtime: func(_ context.Context, requested string) (model.Source, error) {
						calls++
						if requested != name {
							t.Fatalf("wrong runtime source %q", requested)
						}
						return model.Source{Source: name, Rows: []model.Row{{"id": name}},
							Metadata: model.Metadata{"availability": "available", "completeness": "complete", "freshness": "current"}}, nil
					},
				})
				if err != nil || calls != 1 || len(sources[name].Rows) != 1 {
					t.Fatalf("registered source was not resolved: calls=%d sources=%v err=%v", calls, sources, err)
				}
			})
		}
		calls := 0
		_, _, err := r.ExecuteSQLPlanWithOptions(ctx, nil, []string{"$notRegistered"}, dashboardstore.ExecutionOptions{
			Runtime: func(context.Context, string) (model.Source, error) {
				calls++
				return model.Source{}, nil
			},
		})
		if err == nil || calls != 0 {
			t.Fatalf("unknown root reached the runtime provider: calls=%d err=%v", calls, err)
		}
		unavailable := errors.New("registry unavailable")
		_, _, err = r.ExecuteSQLPlanWithOptions(ctx, nil, []string{"$marketplacePackages"}, dashboardstore.ExecutionOptions{
			Runtime: func(context.Context, string) (model.Source, error) {
				return model.Source{}, unavailable
			},
		})
		if !errors.Is(err, unavailable) {
			t.Fatalf("runtime failure fell back to snapshot inventory: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotMarketplaceInventoryRemainsAvailableWithoutRuntimeProvider(t *testing.T) {
	path := testutil.SQLiteSnapshot(t, map[string][]model.Row{
		"marketplacePackages": {{"id": "retained", "name": "Retained package"}},
	})
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	err = store.WithReadTransaction(t.Context(), func(ctx context.Context, r dashboardstore.Reader) error {
		sources, _, err := r.ExecuteSQLPlan(ctx, nil, []string{"$marketplacePackages"})
		if err != nil {
			return err
		}
		if rows := sources["$marketplacePackages"].Rows; len(rows) != 1 || rows[0]["name"] != "Retained package" {
			t.Fatalf("snapshot-only reader lost retained inventory: %v", rows)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotRejectsUnsupportedSchemaAndTracksStoreChanges(t *testing.T) {
	store, path := localSnapshot(t)
	before, err := store.State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	if _, err := writer.ExecContext(t.Context(), `DELETE FROM __idb_stores WHERE name='evals'`); err != nil {
		t.Fatal(err)
	}
	after, err := store.State(t.Context())
	if err != nil || before.Revision == after.Revision {
		t.Fatalf("store availability change did not affect revision: %+v %v", after, err)
	}
	if err := store.WithReadTransaction(t.Context(), func(ctx context.Context, r dashboardstore.Reader) error {
		_, _, err := r.ExecuteSQLPlan(ctx, nil, []string{"$evals"})
		if err == nil || !strings.Contains(err.Error(), "not present") {
			t.Fatalf("missing declared store produced empty success: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(t.Context(), `UPDATE __idb_databases SET version=999`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.State(t.Context()); err == nil {
		t.Fatal("unsupported schema replaced valid state without an error")
	}
	if _, err := Open(t.Context(), path); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported snapshot opened: %v", err)
	}
}
