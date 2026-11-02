package dashboardstore_test

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/sqlitex"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type readFixture struct {
	store   dashboardstore.Store
	publish func(*testing.T)
}

func TestReadContract(t *testing.T) {
	repositories := []model.Row{
		{"id": "repo:1", "owner": "octo", "name": "one"},
		{"id": "repo:2", "owner": "octo", "name": "two"},
	}
	factories := map[string]func(*testing.T) readFixture{
		"postgres": func(t *testing.T) readFixture {
			t.Helper()
			store := testutil.Postgres(t, t.Context(), "POSTGRES_URL")
			revision := "initial"
			publish := func(t *testing.T) {
				t.Helper()
				writer, err := store.BeginIngestion(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer writer.Abort(t.Context())
				for _, row := range repositories {
					row = maps.Clone(row)
					if revision != "initial" {
						row["name"] = "updated"
					}
					if err := writer.Append(t.Context(), "$repositories", row); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := writer.Publish(t.Context(), revision); err != nil {
					t.Fatal(err)
				}
				revision = "updated"
			}
			publish(t)
			return readFixture{store: store, publish: publish}
		},
		"sqlite": func(t *testing.T) readFixture {
			t.Helper()
			path := testutil.SQLiteSnapshot(t, map[string][]model.Row{"repositories": repositories})
			store, err := sqlitex.Open(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			return readFixture{store: store, publish: func(t *testing.T) {
				t.Helper()
				writer, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = writer.Close() }()
				if _, err := writer.ExecContext(t.Context(), `UPDATE __idb_records
					SET value=json_set(value,'$.name','updated') WHERE store_name='repositories'`); err != nil {
					t.Fatal(err)
				}
			}}
		},
	}
	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			fixture := factory(t)
			readConformance(t, fixture, name)
		})
	}
}

func readConformance(t *testing.T, fixture readFixture, backend string) {
	t.Helper()
	store := fixture.store
	if !store.Open() {
		t.Fatal("initialized store is not open")
	}
	initial, err := store.State(t.Context())
	if err != nil || !initial.Ready || initial.Revision == 0 || initial.DataRevision == "" ||
		initial.EvaluatedAt.IsZero() || initial.Counts["$repositories"] != 2 {
		t.Fatalf("invalid initial state: %+v, %v", initial, err)
	}
	t.Run("state-query-diagnostics", func(t *testing.T) {
		err := store.WithReadTransaction(t.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > time.Minute {
				t.Fatal("reader has no bounded transaction context")
			}
			state, err := reader.State(ctx)
			if err != nil || !reflect.DeepEqual(state, initial) {
				t.Fatalf("transaction state: %+v, %v", state, err)
			}
			definitions := []query.Definition{{Name: "picked", From: "$repositories",
				Select:  []query.SelectedField{{Field: "id"}, {Field: "name"}},
				OrderBy: []query.OrderField{{Field: "name"}},
			}}
			sources, metrics, err := reader.ExecuteSQLPlanWithOptions(ctx, definitions, []string{"picked"}, dashboardstore.ExecutionOptions{
				Pages: map[string]dashboardstore.Page{"picked": {Offset: 1, Limit: 1}},
			})
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(sources["picked"].Rows, []model.Row{{"id": "repo:2", "name": "two"}}) ||
				sources["picked"].Metadata["total-row-count"] != 2 {
				t.Fatalf("query/page contract: %+v", sources["picked"])
			}
			if backend == "postgres" {
				if !slices.Contains(metrics.PushedDown, "sql-plan") || len(metrics.FallbackOperations) != 0 {
					t.Fatalf("PostgreSQL no longer reports native SQL execution: %+v", metrics)
				}
			} else if !slices.Contains(metrics.FallbackOperations, "local-go-plan") || len(metrics.PushedDown) != 0 {
				t.Fatalf("SQLite no longer reports local Go execution: %+v", metrics)
			}
			diagnostics, err := reader.Diagnostics(ctx)
			if err != nil {
				return err
			}
			if diagnostics.SchemaVersion != model.SchemaVersion || diagnostics.Counts["repositories"] != 2 ||
				len(diagnostics.RelationshipErrors) != 0 || len(diagnostics.DuplicateRecordIDs["repositories"]) != 0 {
				t.Fatalf("diagnostics: %+v", diagnostics)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		diagnostics, err := store.Diagnostics(t.Context())
		if err != nil || diagnostics.Counts["repositories"] != initial.Counts["$repositories"] {
			t.Fatalf("store diagnostics: %+v, %v", diagnostics, err)
		}
	})
	t.Run("callback-and-query-errors", func(t *testing.T) {
		sentinel := errors.New("callback failure")
		if err := store.WithReadTransaction(t.Context(), func(context.Context, dashboardstore.Reader) error {
			return sentinel
		}); !errors.Is(err, sentinel) {
			t.Fatalf("callback error was replaced: %v", err)
		}
		err := store.WithReadTransaction(t.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
			_, _, err := reader.ExecuteSQLPlan(ctx, nil, []string{"$notRegistered"})
			if err == nil {
				t.Fatal("unknown source produced a successful result")
			}
			_, _, err = reader.ExecuteSQLPlanWithOptions(ctx, nil, []string{"$marketplacePackages"}, dashboardstore.ExecutionOptions{
				Runtime: func(context.Context, string) (model.Source, error) { return model.Source{}, sentinel },
			})
			return err
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("runtime provider error was lost: %v", err)
		}
	})
	t.Run("unavailable-source", func(t *testing.T) {
		err := store.WithReadTransaction(t.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
			sources, _, err := reader.ExecuteSQLPlanWithOptions(ctx,
				[]query.Definition{{Name: "unavailable", From: "$marketplacePackages"}},
				[]string{"unavailable"}, dashboardstore.ExecutionOptions{
					Runtime: func(context.Context, string) (model.Source, error) {
						return model.Source{Source: "$marketplacePackages", Metadata: model.Metadata{
							"availability": "unavailable", "completeness": "unknown", "freshness": "unknown",
						}}, nil
					},
				})
			if err != nil {
				return err
			}
			if sources["unavailable"].Metadata["availability"] != "unavailable" || len(sources["unavailable"].Rows) != 0 {
				t.Fatalf("unavailable source became successful empty evidence: %+v", sources["unavailable"])
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("resource-limit-differences", func(t *testing.T) {
		err := store.WithReadTransaction(t.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
			_, _, err := reader.ExecuteSQLPlanWithOptions(ctx, nil, []string{"$repositories"}, dashboardstore.ExecutionOptions{
				ResourceLimits: &dashboardstore.ResourceLimits{
					MaxInputRows: query.MaxInputRows, MaxOperations: query.MaxOperations, MaxWorkingBytes: query.MaxWorkingBytes,
				},
			})
			return err
		})
		if backend == "postgres" && err != nil {
			t.Fatal(err)
		}
		if backend == "sqlite" && (err == nil || !strings.Contains(err.Error(), "do not support overriding resource limits")) {
			t.Fatalf("SQLite resource override must fail explicitly: %v", err)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		called := false
		err := store.WithReadTransaction(ctx, func(context.Context, dashboardstore.Reader) error {
			called = true
			return nil
		})
		if !errors.Is(err, context.Canceled) || called {
			t.Fatalf("cancelled admission: callback=%t, err=%v", called, err)
		}
		err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
			cancelled, stop := context.WithCancel(ctx)
			stop()
			_, err := reader.State(cancelled)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled state read: %v", err)
			}
			_, _, err = reader.ExecuteSQLPlan(cancelled, nil, []string{"$repositories"})
			return err
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled query: %v", err)
		}
	})
	t.Run("consistent-snapshot-and-next-revision", func(t *testing.T) {
		err := store.WithReadTransaction(t.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
			before, err := reader.State(ctx)
			if err != nil {
				return err
			}
			sources, _, err := reader.ExecuteSQLPlan(ctx, nil, []string{"$repositories"})
			if err != nil {
				return err
			}
			fixture.publish(t)
			after, err := reader.State(ctx)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("concurrent publication changed active snapshot: before=%+v after=%+v", before, after)
			}
			current, _, err := reader.ExecuteSQLPlan(ctx, nil, []string{"$repositories"})
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(sources["$repositories"].Rows, current["$repositories"].Rows) {
				t.Fatal("concurrent publication changed active query rows")
			}
			diagnostics, err := reader.Diagnostics(ctx)
			if err != nil {
				return err
			}
			if diagnostics.Counts["repositories"] != before.Counts["$repositories"] {
				t.Fatalf("diagnostics left active snapshot: %+v", diagnostics)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		current, err := store.State(t.Context())
		if err != nil || current.Revision == initial.Revision || current.DataRevision == initial.DataRevision {
			t.Fatalf("next read did not observe publication: %+v, %v", current, err)
		}
		err = store.WithReadTransaction(t.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
			sources, _, err := reader.ExecuteSQLPlan(ctx, nil, []string{"$repositories"})
			if err != nil {
				return err
			}
			if len(sources["$repositories"].Rows) != 2 || sources["$repositories"].Rows[0]["name"] != "updated" {
				t.Fatalf("next query did not observe published rows: %+v", sources["$repositories"])
			}
			return nil
		})
		if err != nil {
			t.Fatalf("store did not recover after callback errors/cancellation: %v", err)
		}
	})
	t.Run("unavailable-storage", func(t *testing.T) {
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := store.State(t.Context()); err == nil {
			t.Fatal("unavailable storage returned successful state")
		}
		if _, err := store.Diagnostics(t.Context()); err == nil {
			t.Fatal("unavailable storage returned successful diagnostics")
		}
		called := false
		if err := store.WithReadTransaction(t.Context(), func(context.Context, dashboardstore.Reader) error {
			called = true
			return nil
		}); err == nil || called {
			t.Fatalf("unavailable storage admitted a read: callback=%t, err=%v", called, err)
		}
	})
}
