package collect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
	operationalpostgres "github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/redisx"
)

func durableErasureServices(t *testing.T, backend string) (operational.OperationalServices, func() operational.OperationalServices) {
	t.Helper()
	ctx := t.Context()
	namespace := fmt.Sprintf("erasure-%d", time.Now().UnixNano())
	if backend == "redis" {
		endpoint := os.Getenv("REDIS_URL")
		if endpoint == "" {
			t.Skip("REDIS_URL is required for durable erasure recovery tests")
		}
		reopen := func() operational.OperationalServices {
			client, err := redisx.New(endpoint)
			if err != nil {
				t.Fatal(err)
			}
			reopened := redisx.NewStore(client, namespace)
			t.Cleanup(func() { _ = reopened.Close() })
			return reopened.OperationalServices()
		}
		return reopen(), reopen
	}
	dsn := os.Getenv("POSTGRES_URL")
	if dsn == "" {
		t.Skip("POSTGRES_URL is required for durable erasure recovery tests")
	}
	store, err := operationalpostgres.New(ctx, dsn, namespace, operationalpostgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.DeleteNamespace(cleanup); err != nil {
			t.Error(err)
		}
		_ = store.Close()
	})
	reopen := func() operational.OperationalServices {
		reopened, err := operationalpostgres.Open(ctx, dsn, namespace, operationalpostgres.Config{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reopened.Close() })
		return reopened.OperationalServices()
	}
	return store.OperationalServices(), reopen
}

func erasureQueue(services operational.OperationalServices) Queue {
	return Queue{Tasks: services.Queue, Admission: services.Admission, Metadata: services.Collection,
		Leases: services.Leases, Deliveries: services.Deliveries, Metrics: services.IngestionMetrics}
}

// Keep these recovery fixtures already compacted: both projection attempts use
// the production replay/ingestion path, without inventory or shared-value scope.
func seedErasureProjection(t *testing.T, p Projector) {
	t.Helper()
	for _, repository := range []string{"octo/withdrawn", "octo/kept"} {
		record := fmt.Sprintf(`{"kind":"metadata","schemaVersion":%d,"ingestionVersion":3,"phase":"runs","records":1}`+"\n"+
			`{"kind":"record","collection":"repositories","record":{"id":"repository:%s","owner":"octo","name":%q}}`+"\n",
			model.SchemaVersion, repository, filepath.Base(repository))
		path := filepath.Join(p.Lake.RunsDirectory(), p.Lake.ShardPrefix(repository)+"1.jsonl")
		if err := WriteFileAtomic(path, []byte(record)); err != nil {
			t.Fatal(err)
		}
	}
	writeErasureManifest(t, p.Lake)
	if _, err := p.Project(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertErasureRows(t, t.Context(), p, 2)
}

func writeErasureManifest(t *testing.T, lake Lake) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(lake.RunsDirectory(), "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := map[string]string{}
	for _, path := range files {
		content, err := os.ReadFile(path) // #nosec G304 -- paths belong to this test's temporary evidence lake.
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		manifest["gh-aw-logs-runs/"+filepath.Base(path)] = hex.EncodeToString(sum[:])
	}
	content, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(lake.ManifestPath(), content); err != nil {
		t.Fatal(err)
	}
}

func assertErasureRows(t *testing.T, ctx context.Context, p Projector, count int) {
	t.Helper()
	results, _, err := p.Data.ExecuteSQLPlan(ctx,
		[]query.Definition{{Name: "erasure", From: "$repositories"}}, []string{"erasure"})
	if err != nil {
		t.Fatal(err)
	}
	rows := results["erasure"].Rows
	if len(rows) != count {
		t.Fatalf("canonical repositories = %+v, want %d", rows, count)
	}
	if count == 1 && rows[0]["id"] != "repository:octo/kept" {
		t.Fatalf("erasure removed unrelated canonical evidence: %+v", rows)
	}
}

func TestErasureRestartRecoveryThroughCanonicalCommit(t *testing.T) {
	for _, backend := range []string{"redis", "postgres"} {
		for _, failure := range []string{"marker-write", "cancel-before-marker", "cancel-after-marker", "none"} {
			t.Run(backend+"/"+failure, func(t *testing.T) {
				services, reopen := durableErasureServices(t, backend)
				ctx := t.Context()
				lake := Lake{Directory: t.TempDir()}
				if err := lake.Prepare(); err != nil {
					t.Fatal(err)
				}
				p := Projector{State: services.State, Leases: services.Leases, Lake: lake,
					Data: integrationPostgres(t, ctx), DatabaseQueriesPath: databaseQueries}
				seedErasureProjection(t, p)
				queue := erasureQueue(services)
				if err := queue.Ensure(ctx); err != nil {
					t.Fatal(err)
				}
				if _, err := queue.Enqueue(ctx, Task{Repository: "octo/withdrawn", Erase: true}); err != nil {
					t.Fatal(err)
				}
				lease := refreshLease(t, queue)
				processCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				fault := projectionFaultState{StateStore: services.State,
					beforeSet: func(ctx context.Context, key string, _ []byte) error {
						if key != projectionDirtyKey {
							return nil
						}
						if failure == "marker-write" {
							return errors.New("injected marker failure")
						}
						if failure == "cancel-before-marker" {
							cancel()
							return ctx.Err()
						}
						return nil
					},
					afterSet: func(key string) {
						if key == projectionDirtyKey && failure == "cancel-after-marker" {
							cancel()
						}
					},
				}
				worker := Worker{Queue: queue, Runner: Runner{Lake: lake}, Projector: p}
				worker.Projector.State = fault
				worker.process(processCtx, lease)
				// Discard the worker and reconnect to durable state, as a replacement
				// process would after interruption. Do not run the batch follow-up.
				restarted := reopen()
				p.State, p.Leases = restarted.State, restarted.Leases
				queue = erasureQueue(restarted)
				if failure != "none" {
					if pending, err := queue.Pending(ctx); err != nil || pending != 1 {
						t.Fatalf("interrupted erasure was acknowledged: %d, %v", pending, err)
					}
					leases, err := queue.Reclaim(ctx, "replacement", 0, 1)
					if err != nil || len(leases) != 1 {
						t.Fatalf("recover erasure lease = %+v, %v", leases, err)
					}
					worker = Worker{Queue: queue, Runner: Runner{Lake: lake}, Projector: p}
					worker.process(ctx, leases[0])
				}
				if depth, err := queue.Depth(ctx); err != nil || depth != 0 {
					t.Fatalf("recovered erasure was not acknowledged: %d, %v", depth, err)
				}
				if pending, err := p.PendingProjection(ctx); err != nil || !pending {
					t.Fatalf("ACK lost durable projection: %t, %v", pending, err)
				}
				assertErasureRows(t, ctx, p, 2) // Old canonical rows remain until publication.
				writeErasureManifest(t, lake)
				if _, err := p.Project(ctx); err != nil {
					t.Fatal(err)
				}
				assertErasureRows(t, ctx, p, 1)
				if pending, err := p.PendingProjection(ctx); err != nil || pending {
					t.Fatalf("committed generation remains pending: %t, %v", pending, err)
				}
			})
		}
	}
}

func TestProjectionGenerationRecoveryThroughCanonicalCommit(t *testing.T) {
	for _, failure := range []string{"ingestion", "cancellation", "crash-before-commit", "crash-after-commit",
		"completion-write", "completion-cancellation", "concurrent-before-commit", "concurrent-erasure"} {
		t.Run(failure, func(t *testing.T) {
			w := refreshWorker(t)
			p := w.Projector
			p.Data = integrationPostgres(t, t.Context())
			p.DatabaseQueriesPath = databaseQueries
			seedErasureProjection(t, p)
			state := p.State
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			concurrent := failure == "concurrent-before-commit" || failure == "concurrent-erasure"
			if !concurrent {
				if err := p.Lake.Forget("octo/withdrawn"); err != nil {
					t.Fatal(err)
				}
				writeErasureManifest(t, p.Lake)
			}
			if err := p.RequestProjection(ctx); err != nil {
				t.Fatal(err)
			}
			before, err := p.Data.State(ctx)
			if err != nil {
				t.Fatal(err)
			}
			erase := func(ctx context.Context) {
				if _, err := w.Queue.Enqueue(ctx, Task{Repository: "octo/withdrawn", Erase: true}); err != nil {
					t.Fatal(err)
				}
				w.Projector = p
				w.Projector.State = state
				leases, err := w.Queue.Lease(ctx, "concurrent-erasure", 1, 0)
				if err != nil || len(leases) != 1 {
					t.Fatalf("lease concurrent erasure: %+v, %v", leases, err)
				}
				w.process(ctx, leases[0])
				writeErasureManifest(t, p.Lake)
			}
			p.State = projectionFaultState{StateStore: state,
				afterRead: func(key string) {
					if key != projectionDirtyKey {
						return
					}
					switch failure {
					case "cancellation":
						cancel()
					case "crash-before-commit":
						panic("process stopped before canonical publication")
					case "concurrent-before-commit":
						erase(ctx)
					}
				},
				beforeSet: func(ctx context.Context, key string, _ []byte) error {
					if key != projectionCompletedKey {
						return nil
					}
					active, err := p.Data.State(ctx)
					if err != nil || active.Revision < before.Revision {
						t.Fatalf("completion precedes canonical publication: %+v, %v", active, err)
					}
					if failure == "completion-write" {
						assertErasureRows(t, ctx, p, 1)
						return errors.New("injected completion failure after canonical commit")
					}
					if failure == "crash-after-commit" {
						assertErasureRows(t, ctx, p, 1)
						panic("process stopped after canonical publication")
					}
					if failure == "completion-cancellation" {
						assertErasureRows(t, ctx, p, 1)
						cancel()
						return ctx.Err()
					}
					if failure == "concurrent-erasure" {
						assertErasureRows(t, ctx, p, 2)
						erase(ctx)
					}
					return nil
				},
			}
			if failure == "ingestion" {
				p.DatabaseQueriesPath = filepath.Join(t.TempDir(), "missing.json")
			}
			wantCrash := failure == "crash-before-commit" || failure == "crash-after-commit"
			crashed := false
			func() {
				defer func() {
					if value := recover(); value != nil {
						if !wantCrash {
							panic(value)
						}
						crashed = true
					}
				}()
				_, err = p.Project(ctx)
			}()
			if wantCrash && !crashed {
				t.Fatal("crash boundary was not reached")
			}
			if !wantCrash && (err == nil) != concurrent {
				t.Fatalf("interrupted projection error = %v", err)
			}
			// A fresh projector must see either failed work or the newer erasure,
			// including when canonical publication succeeded but completion did not.
			p.State = state
			p.DatabaseQueriesPath = databaseQueries
			if pending, err := p.PendingProjection(t.Context()); err != nil || !pending {
				t.Fatalf("projection consumed uncommitted/concurrent intent: %t, %v", pending, err)
			}
			if _, err := p.Project(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertErasureRows(t, t.Context(), p, 1)
			if pending, err := p.PendingProjection(t.Context()); err != nil || pending {
				t.Fatalf("recovery did not complete: %t, %v", pending, err)
			}
		})
	}
}
