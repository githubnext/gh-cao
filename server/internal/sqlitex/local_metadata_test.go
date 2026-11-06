package sqlitex

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/internal/dashboardquery"
	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestLocalDashboardCoverageSurvivesCanonicalQueryBoundary(t *testing.T) {
	now := time.Now().UTC()
	batch := map[string][]model.Row{
		"runs": {{"id": "github:run:octo/api:1", "githubRunId": "1", "status": "in_progress"}},
		"dailyRunCounts": {{
			"id": `["2026-01-02","repo","workflow"]`, "day": "2026-01-02", "repositoryId": "repo", "workflowId": "workflow",
			"owner": "octo", "repository": "api", "workflowPath": ".github/workflows/agent.md",
			"total": 1, "successful": 0, "failed": 0, "approval": 0, "pending": 1, "other": 0,
		}},
		"runCountContributions": {},
		"dailyRunCountState": {{
			"id": "daily-run-counts", "status": "ready", "coverage": "bounded-gh-aw-observations",
		}},
		"transactions": {{
			"id": "local-dashboard", "kind": "local-dashboard", "createdAt": now.Format(time.RFC3339Nano),
			"windowStart": now.Add(-48 * time.Hour).Format(time.RFC3339Nano), "windowEnd": now.Format(time.RFC3339Nano),
			"repositories": []string{"octo/api"}, "completeness": "partial", "coverage": "Bounded gh-aw activity.",
		}},
	}
	path := testutil.SQLiteSnapshot(t, batch)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"dailyRunCounts", "runCountContributions", "dailyRunCountState"} {
		if _, err := db.Exec(`INSERT INTO __idb_stores VALUES(?,?,'id')`, dashboardquery.DatabaseName, name); err != nil {
			t.Fatal(err)
		}
		for _, record := range batch[name] {
			key, err := json.Marshal(record["id"])
			if err != nil {
				t.Fatal(err)
			}
			value, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO __idb_records VALUES(?,?,?,?)`, dashboardquery.DatabaseName, name, string(key), string(value)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
		result, _, err := reader.ExecuteSQLPlan(ctx, []query.Definition{
			{Name: "local-runs", From: "$runs"}, {Name: "local-daily", From: "daily-run-counts"},
		}, []string{"local-runs", "local-daily"})
		if err != nil {
			return err
		}
		for _, name := range []string{"local-runs", "local-daily"} {
			source := result[name]
			if len(source.Rows) != 1 || source.Metadata["completeness"] != "partial" ||
				source.Metadata["as-of"] != now.Format(time.RFC3339Nano) {
				t.Fatalf("lost local acquisition evidence: %+v", source)
			}
		}
		if result["local-daily"].Metadata["aggregate-coverage"] != "bounded-gh-aw-observations" {
			t.Fatalf("lost local daily aggregate provenance: %+v", result["local-daily"])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
