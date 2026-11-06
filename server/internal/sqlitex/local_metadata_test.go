package sqlitex

import (
	"context"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestLocalDashboardCoverageSurvivesCanonicalQueryBoundary(t *testing.T) {
	now := time.Now().UTC()
	path := testutil.SQLiteSnapshot(t, map[string][]model.Row{
		"runs": {{"id": "github:run:octo/api:1", "githubRunId": "1", "status": "in_progress"}},
		"transactions": {{
			"id": "local-dashboard", "kind": "local-dashboard", "createdAt": now.Format(time.RFC3339Nano),
			"windowStart": now.Add(-48 * time.Hour).Format(time.RFC3339Nano), "windowEnd": now.Format(time.RFC3339Nano),
			"repositories": []string{"octo/api"}, "completeness": "partial", "coverage": "Bounded gh-aw activity.",
		}},
	})
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
		result, _, err := reader.ExecuteSQLPlan(ctx, []query.Definition{{Name: "local-runs", From: "$runs"}}, []string{"local-runs"})
		if err != nil {
			return err
		}
		source := result["local-runs"]
		if len(source.Rows) != 1 || source.Metadata["completeness"] != "partial" ||
			source.Metadata["as-of"] != now.Format(time.RFC3339Nano) {
			t.Fatalf("lost local acquisition evidence: %+v", source)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
