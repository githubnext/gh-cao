package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

// Postgres creates a private schema, including when initialization fails.
// Service-backed tests never replace another test's or the operator's data.
func Postgres(t testing.TB, parent context.Context, environment ...string) *postgresx.Store {
	t.Helper()
	store, _ := PostgresWithConfig(t, parent, environment...)
	return store
}

// PostgresWithConfig also exposes the isolated schema connection for operational
// adapters that need their own tables in the same disposable database.
func PostgresWithConfig(t testing.TB, parent context.Context, environment ...string) (*postgresx.Store, *pgx.ConnConfig) {
	t.Helper()
	endpoint := ""
	for _, name := range environment {
		if endpoint = os.Getenv(name); endpoint != "" {
			break
		}
	}
	if endpoint == "" {
		t.Skipf("set one of %v to run Postgres integration tests", environment)
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", endpoint)
	if err != nil {
		t.Fatal("configure Postgres test connection")
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := fmt.Sprintf("cao_test_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal("create isolated Postgres test schema")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
		defer stop()
		// Drop partitioned parents in separate transactions so their child
		// partitions do not exhaust PostgreSQL's shared lock table.
		rows, err := admin.QueryContext(cleanup, `SELECT c.relname FROM pg_class c
			JOIN pg_namespace n ON n.oid=c.relnamespace
			WHERE n.nspname=$1 AND c.relkind='p'`, schema)
		if err != nil {
			t.Error("list isolated Postgres partitions")
			return
		}
		defer func() { _ = rows.Close() }()
		var parents []string
		for rows.Next() {
			var name string
			if err = rows.Scan(&name); err != nil {
				break
			}
			parents = append(parents, name)
		}
		if err == nil {
			err = rows.Err()
		}
		_ = rows.Close()
		if err != nil {
			t.Error("list isolated Postgres partitions")
			return
		}
		for _, name := range parents {
			if _, err := admin.ExecContext(cleanup, "DROP TABLE "+pgx.Identifier{schema, name}.Sanitize()+" CASCADE"); err != nil {
				t.Error("remove isolated Postgres partitioned table")
				return
			}
		}
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error("remove isolated Postgres test schema")
		}
	})
	config, err := pgx.ParseConfig(endpoint)
	if err != nil {
		t.Fatal("parse Postgres test configuration")
	}
	config.RuntimeParams["search_path"] = schema
	store, err := postgresx.NewConfig(ctx, config)
	if err != nil {
		t.Fatal("initialize isolated Postgres test schema")
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, config
}
