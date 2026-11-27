package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func TestFencedTaskReceiptsSurviveReclaimAndReplacement(t *testing.T) {
	first, dsn := testStore(t, Config{})
	second, err := Open(t.Context(), dsn, first.namespace, Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	ctx := t.Context()
	if err := first.EnsureQueue(ctx, "runs", "workers"); err != nil {
		t.Fatal(err)
	}
	if _, err := first.EnqueueUniqueTask(ctx, "runs", "attempt", 1, operational.TaskFields{Task: "exact attempt"}); err != nil {
		t.Fatal(err)
	}
	read := operational.QueueRead{Queue: "runs", Group: "workers", Consumer: "same-consumer", Count: 1}
	original, err := first.ReadTasks(ctx, read)
	if err != nil || len(original) != 1 || original[0].Receipt == "" {
		t.Fatalf("initial receipt: %v %v", original, err)
	}
	current, err := second.ClaimTasks(ctx, read, 0)
	if err != nil || len(current) != 1 || current[0].Receipt == original[0].Receipt {
		t.Fatalf("reclaim reused a receipt: %v %v", current, err)
	}
	id := original[0].ID
	for _, receipt := range []string{"", original[0].Receipt} {
		if err := first.CompleteTaskFenced(ctx, "runs", "workers", id, receipt); !errors.Is(err, operational.ErrTaskLeaseLost) {
			t.Fatalf("stale completion: %v", err)
		}
	}
	replacement := operational.Replacement{
		Source: "runs", Group: "workers", ID: id, Destination: "runs",
		Delayed: "later", Due: time.Now().Add(time.Hour), Capacity: 1,
		Fields: operational.TaskFields{Task: "retry exact attempt"},
	}
	if err := first.ReplaceTaskFenced(ctx, replacement, original[0].Receipt); !errors.Is(err, operational.ErrTaskLeaseLost) {
		t.Fatalf("stale replacement: %v", err)
	}
	stats, err := second.QueueStats(ctx, "runs", "workers")
	if err != nil || stats.Pending != 1 || stats.Length != 1 {
		t.Fatalf("stale owner changed current work: %v %v", stats, err)
	}
	if err := second.ReplaceTaskFenced(ctx, replacement, current[0].Receipt); err != nil {
		t.Fatal(err)
	}
	if err := second.ReplaceTaskFenced(ctx, replacement, current[0].Receipt); !errors.Is(err, operational.ErrTaskLeaseLost) {
		t.Fatalf("replacement receipt reused: %v", err)
	}
	if depth, err := first.DelayedDepth(ctx, "later"); err != nil || depth != 1 {
		t.Fatalf("durable delayed replacement: %d %v", depth, err)
	}
	if promoted, err := first.PromoteTasks(ctx, "later", "runs", time.Now().Add(2*time.Hour), 1); err != nil || promoted != 1 {
		t.Fatalf("promotion: %d %v", promoted, err)
	}
	retried, err := first.ReadTasks(ctx, read)
	if err != nil || len(retried) != 1 || retried[0].ID == id || retried[0].Receipt == current[0].Receipt {
		t.Fatalf("replacement did not receive a new identity and receipt: %v %v", retried, err)
	}
	if err := second.CompleteTaskFenced(ctx, "runs", "workers", retried[0].ID, retried[0].Receipt); err != nil {
		t.Fatal(err)
	}
}

func TestFencedTaskReplacementCapacityRollsBackReceipt(t *testing.T) {
	store, _ := testStore(t, Config{})
	ctx := t.Context()
	if err := store.EnsureQueue(ctx, "runs", "workers"); err != nil {
		t.Fatal(err)
	}
	for _, queue := range []string{"runs", "dead"} {
		if _, err := store.EnqueueTask(ctx, operational.EnqueueRequest{Queue: queue, Fields: operational.TaskFields{Task: "bounded"}}); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := store.ReadTasks(ctx, operational.QueueRead{Queue: "runs", Group: "workers", Consumer: "worker", Count: 1})
	if err != nil || len(messages) != 1 {
		t.Fatalf("read: %v %v", messages, err)
	}
	message := messages[0]
	if err := store.ReplaceTaskFenced(ctx, operational.Replacement{
		Source: "runs", Group: "workers", ID: message.ID, Destination: "dead",
		Capacity: 1, Fields: operational.TaskFields{Task: "failed"},
	}, message.Receipt); !errors.Is(err, operational.ErrCapacity) {
		t.Fatalf("capacity was not enforced: %v", err)
	}
	if err := store.CompleteTaskFenced(ctx, "runs", "workers", message.ID, message.Receipt); err != nil {
		t.Fatalf("failed replacement destroyed the current lease: %v", err)
	}
}

func TestPendingTaskIdentitySurvivesRetryAndReleasesOnTerminalSettlement(t *testing.T) {
	first, dsn := testStore(t, Config{})
	second, err := Open(t.Context(), dsn, first.namespace, Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	ctx := t.Context()
	request := operational.EnqueueRequest{Queue: "sync", Delayed: "later", Capacity: 1,
		Fields: operational.TaskFields{Task: "inventory-only"}}
	enqueue := func(store *Store, want bool) {
		t.Helper()
		if got, err := store.EnqueuePendingTask(ctx, request, "repo:epoch"); err != nil || got != want {
			t.Fatalf("pending enqueue = %v, %v; want %v", got, err, want)
		}
	}
	if err := first.EnsureQueue(ctx, "sync", "workers"); err != nil {
		t.Fatal(err)
	}
	enqueue(first, true)
	enqueue(second, false)
	read := operational.QueueRead{Queue: "sync", Group: "workers", Consumer: "same-consumer", Count: 1}
	original, err := first.ReadTasks(ctx, read)
	if err != nil || len(original) != 1 {
		t.Fatalf("read: %v %v", original, err)
	}
	current, err := second.ClaimTasks(ctx, read, 0)
	if err != nil || len(current) != 1 {
		t.Fatalf("reclaim: %v %v", current, err)
	}
	if err := first.CompleteTaskFenced(ctx, "sync", "workers", original[0].ID, original[0].Receipt); !errors.Is(err, operational.ErrTaskLeaseLost) {
		t.Fatalf("stale completion = %v", err)
	}
	enqueue(first, false)
	replacement := operational.Replacement{Source: "sync", Group: "workers", ID: current[0].ID,
		Destination: "sync", Delayed: "later", Due: time.Now().Add(time.Hour), Capacity: 1, Fields: request.Fields}
	if err := first.ReplaceTaskFenced(ctx, replacement, original[0].Receipt); !errors.Is(err, operational.ErrTaskLeaseLost) {
		t.Fatalf("stale retry = %v", err)
	}
	if err := second.ReplaceTaskFenced(ctx, replacement, current[0].Receipt); err != nil {
		t.Fatal(err)
	}
	enqueue(first, false)
	if promoted, err := first.PromoteTasks(ctx, "later", "sync", time.Now().Add(2*time.Hour), 1); err != nil || promoted != 1 {
		t.Fatalf("promote = %d %v", promoted, err)
	}
	enqueue(second, false)
	retried, err := first.ReadTasks(ctx, read)
	if err != nil || len(retried) != 1 {
		t.Fatalf("retry read: %v %v", retried, err)
	}
	if err := second.CompleteTaskFenced(ctx, "sync", "workers", retried[0].ID, retried[0].Receipt); err != nil {
		t.Fatal(err)
	}
	enqueue(first, true)
	fresh, err := first.ReadTasks(ctx, read)
	if err != nil || len(fresh) != 1 {
		t.Fatalf("fresh read: %v %v", fresh, err)
	}
	replacement.ID, replacement.Destination, replacement.Due = fresh[0].ID, "dead", time.Time{}
	if _, err := first.EnqueueTask(ctx, operational.EnqueueRequest{Queue: "dead", Fields: request.Fields}); err != nil {
		t.Fatal(err)
	}
	if err := first.ReplaceTaskFenced(ctx, replacement, fresh[0].Receipt); !errors.Is(err, operational.ErrCapacity) {
		t.Fatalf("failed terminal replacement = %v", err)
	}
	enqueue(second, false)
	replacement.Capacity = 2
	if err := first.ReplaceTaskFenced(ctx, replacement, fresh[0].Receipt); err != nil {
		t.Fatal(err)
	}
	enqueue(second, true)
	if err := first.CompleteTaskFenced(ctx, "sync", "workers", fresh[0].ID, fresh[0].Receipt); !errors.Is(err, operational.ErrTaskLeaseLost) {
		t.Fatalf("old terminal receipt = %v", err)
	}
	enqueue(first, false)
}
