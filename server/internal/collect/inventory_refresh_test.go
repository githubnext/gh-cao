package collect

import (
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestManualInventoryRefreshQueuesOnlyInventoryAndFencesEnrollment(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue := nativeEventQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	scope := nativeScope(t, enrollment, "octo/api")
	first, err := queue.RequestInventoryRefresh(t.Context(), scope)
	if err != nil || !first.Enqueued {
		t.Fatalf("admission = %+v, %v", first, err)
	}
	repeated, err := queue.RequestInventoryRefresh(t.Context(), scope)
	if err != nil || !repeated.Duplicate || repeated.Enqueued {
		t.Fatalf("duplicate admission = %+v, %v", repeated, err)
	}
	leases, err := queue.nativeControlLease(t.Context(), "manual-test", time.Minute)
	if err != nil || len(leases) != 1 {
		t.Fatalf("lease = %+v, %v", leases, err)
	}
	task := leases[0].Task
	if !task.InventoryRefresh || task.ValueRefresh || task.LifecycleRefresh || task.Scope != nil || task.ScopeEpoch != scope.Epoch {
		t.Fatalf("manual refresh broadened task: %+v", task)
	}
	hooks := &nativeRefreshFixture{}
	worker := nativeEventWorker(queue, enrollment, hooks)
	if err := worker.process(t.Context(), task); err != nil || hooks.inventories != 1 || hooks.values != 0 {
		t.Fatalf("inventory-only execution: %+v, %v", hooks, err)
	}
	if _, err := enrollment.RemoveRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	if err := worker.process(t.Context(), task); err != nil || hooks.inventories != 1 {
		t.Fatalf("withdrawn task acquired inventory: %+v, %v", hooks, err)
	}
	if _, err := queue.RequestInventoryRefresh(t.Context(), collectnative.EnrollmentScope{}); err == nil {
		t.Fatal("missing committed identity admitted")
	}
}

func TestNativeInventoryScheduleAtLeastDaily(t *testing.T) {
	for _, interval := range []time.Duration{0, time.Hour, 24 * time.Hour, 48 * time.Hour} {
		worker := NativeWorker{RefreshInterval: interval}
		if worker.interval() <= 0 || worker.interval() > 24*time.Hour {
			t.Fatalf("interval %s schedules slower than daily: %s", interval, worker.interval())
		}
	}
}
