package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func ingestTestStore(t *testing.T) (context.Context, *postgresx.Store) {
	t.Helper()
	ctx, store, _ := ingestTestStoreConfig(t)
	return ctx, store
}

func ingestTestStoreConfig(t *testing.T) (context.Context, *postgresx.Store, *pgx.ConnConfig) {
	t.Helper()
	endpoint := os.Getenv("POSTGRES_URL")
	if endpoint == "" {
		t.Skip("POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	t.Cleanup(cancel)
	admin, err := sql.Open("pgx", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := fmt.Sprintf("cao_ingest_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
	})
	config, err := pgx.ParseConfig(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = schema
	store, err := postgresx.NewConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return ctx, store, config
}

func TestFreshNativeIngestionReuseForceAndQueries(t *testing.T) {
	ctx, store := ingestTestStore(t)
	options := Options{DatabaseQueriesPath: "../../../dashboard/site/src/data/queries/database.json"}
	directory, _ := testutil.RecentSnapshot(t, "../../testdata/deployed-subset")
	first, err := Run(ctx, store, directory, options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Run(ctx, store, directory, options)
	if err != nil || second.Revision != first.Revision || second.DataRevision != first.DataRevision {
		t.Fatalf("unchanged revision advanced: %+v %v", second, err)
	}
	options.Force = true
	third, err := Run(ctx, store, directory, options)
	if err != nil || third.Revision != first.Revision+1 || third.DataRevision != first.DataRevision {
		t.Fatalf("force semantics changed: %+v %v", third, err)
	}
	definitions, err := loadDefinitions(options.DatabaseQueriesPath)
	if err != nil {
		t.Fatal(err)
	}
	sources, metrics, err := store.ExecuteSQLPlan(ctx, definitions, []string{"repositories", "runs", "domains", "tools", "audits"})
	if err != nil {
		t.Fatal(err)
	}
	if len(sources["runs"].Rows) != 1 || len(sources["repositories"].Rows) != 1 {
		t.Fatalf("native corpus did not resolve canonical rows: %+v %+v", sources, metrics)
	}
	for _, name := range []string{"unknown", "usage", "repository-memory-manifest"} {
		if _, _, err := store.ExecuteSQLPlan(ctx, []query.Definition{{Name: "fail", From: name}}, []string{"fail"}); err == nil {
			t.Fatalf("arbitrary source %q admitted", name)
		}
	}
}
