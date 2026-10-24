package sqlitex

import (
	"context"
	"database/sql"
	"testing"

	"github.com/githubnext/gh-cao/internal/dashboardquery"

	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestOversizedStatisticalFramePreservesExactQueries(t *testing.T) {
	path := testutil.SQLiteSnapshot(t, map[string][]model.Row{
		"runs": {}, "repositories": {{"id": "repository", "name": "control"}},
	})
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(t.Context(), `WITH RECURSIVE candidates(n) AS (
		SELECT 1 UNION ALL SELECT n+1 FROM candidates WHERE n < ?
	) INSERT INTO __idb_records SELECT ?, 'runs', json_quote('github:run:o/r:' || n),
		json_object('id','github:run:o/r:' || n) FROM candidates`,
		dashboardquery.StatisticalMaxInputRows+1, dashboardquery.DatabaseName)
	if closeErr := db.Close(); err != nil || closeErr != nil {
		t.Fatalf("prepare oversized frame: %v %v", err, closeErr)
	}
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
		results, _, err := reader.ExecuteSQLPlan(ctx, []query.Definition{
			{Name: "sample", From: "$statisticalMembership"},
			{Name: "exact", From: "$repositories"},
		}, []string{"sample", "exact"})
		if err != nil {
			return err
		}
		if results["sample"].Metadata["availability"] != "unavailable" || len(results["sample"].Rows) != 0 ||
			results["sample"].Metadata["error"] != "statistical candidate frame exceeds input budget" {
			t.Fatalf("oversized frame was presented as a usable sample: %+v", results["sample"])
		}
		if len(results["exact"].Rows) != 1 || results["exact"].Rows[0]["id"] != "repository" {
			t.Fatalf("sampling budget changed exact evidence: %+v", results["exact"])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMissingStatisticalClockPreservesExactQueries(t *testing.T) {
	path := testutil.SQLiteSnapshot(t, map[string][]model.Row{
		"runs": {}, "repositories": {{"id": "repository", "name": "control"}},
	})
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
		results, _, err := reader.ExecuteSQLPlan(ctx, []query.Definition{
			{Name: "sample", From: "$statisticalMembership"},
			{Name: "exact", From: "$repositories"},
		}, []string{"sample", "exact"})
		if err != nil {
			return err
		}
		if results["sample"].Metadata["availability"] != "unavailable" || len(results["sample"].Rows) != 0 {
			t.Fatalf("missing sampling clock was presented as a usable sample: %+v", results["sample"])
		}
		if len(results["exact"].Rows) != 1 || results["exact"].Rows[0]["id"] != "repository" {
			t.Fatalf("sampling unavailability changed exact evidence: %+v", results["exact"])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
