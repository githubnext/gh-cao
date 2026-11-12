package collect

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestWorkerRetriesAFailedCollection proves a failing repository is retried
// and never silently dropped, and that failure does not activate a projection.
func TestWorkerRetriesAFailedCollection(t *testing.T) {
	store, ctx := integrationStore(t)
	workspace := t.TempDir()
	catalogRoot := filepath.Join(workspace, "catalog")
	writeValidCollectionCatalog(t, catalogRoot)
	gh := writeFakeBinary(t, workspace, "gh", `echo "rate limited" >&2; exit 1`)
	// Forward the collection wrapper's arguments to gh so its failure is exercised.
	node := writeFakeBinary(t, workspace, "node", "shift\nexec \"$@\"")
	lake := Lake{Directory: filepath.Join(workspace, "lake")}
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store, MaxAttempts: 2}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, Task{Repository: "octo/api", InstallationID: 7}); err != nil {
		t.Fatal(err)
	}
	worker := Worker{
		Queue: queue,
		Runner: Runner{
			Lake: lake, CatalogRoot: catalogRoot,
			GitHubBinary: gh, NodeBinary: node,
			Tokens: fakeTokens{token: "installation-token"},
		},
		Values: NativeValueService{State: store, Leases: store, Enrollment: Enrollment{Metadata: store, Leases: store},
			MinInterval: time.Hour,
		},
		Consumer:      "test-worker",
		BlockInterval: 50 * time.Millisecond,
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- worker.Run(runCtx) }()

	deadline := time.Now().Add(8 * time.Second)
	var dead int64
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("worker stopped before retaining the exhausted task: %v", err)
		default:
		}
		var err error
		if dead, err = queue.DeadLetters(ctx); err != nil {
			t.Fatal(err)
		}
		if dead > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	<-done
	if dead != 1 {
		t.Fatalf("dead letters = %d, want the exhausted task to be retained for inspection", dead)
	}
	counters, _, err := store.IngestionHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, expected := range map[string]int64{
		"collectionFailed":       2,
		"collectionRetried":      1,
		"collectionDeadLettered": 1,
		"collectionSucceeded":    0,
	} {
		if counters[name] != expected {
			t.Errorf("%s = %d, want %d", name, counters[name], expected)
		}
	}
	depth, err := queue.Depth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if depth != 0 {
		t.Fatalf("queue depth = %d, want no remaining retries after exhaustion", depth)
	}
	leased, err := queue.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if leased != 0 {
		t.Fatalf("pending = %d, want the exhausted task acknowledged", leased)
	}
}
