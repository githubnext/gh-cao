package collect

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type nativeRuntimeHooks struct {
	values chan collectnative.EnrollmentScope
}

func (nativeRuntimeHooks) RefreshInventory(context.Context, collectnative.EnrollmentScope) error {
	return nil
}
func (nativeRuntimeHooks) Due(context.Context, collectnative.EnrollmentScope, time.Time) (bool, error) {
	return false, nil
}
func (nativeRuntimeHooks) Refresh(context.Context, collectnative.EnrollmentScope, bool) (bool, error) {
	return false, nil
}
func (h nativeRuntimeHooks) InvalidateValue(ctx context.Context, scope collectnative.EnrollmentScope) error {
	select {
	case h.values <- scope:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type emptyNativeHistory struct{}

func (emptyNativeHistory) DiscoveryWorkflows(context.Context, collectnative.EnrollmentScope, int) ([]int64, int, error) {
	return nil, 0, nil
}
func (emptyNativeHistory) DiscoveryRuns(context.Context, collectnative.EnrollmentScope, int64, time.Time, time.Time, int) ([]collectnative.DiscoveredRun, int, error) {
	return nil, 0, errors.New("empty workflow inventory cannot discover runs")
}

func TestNativePeriodicRepairDeduplicatesAcrossReplicasAndUsesNewObservation(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue := nativeEventQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	hooks := &nativeRefreshFixture{}
	worker := nativeEventWorker(queue, enrollment, hooks)
	peer := worker
	peer.Consumer = "other-native-replica"
	for _, replica := range []NativeWorker{worker, peer} {
		if err := replica.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if stats, err := queue.Tasks.QueueStats(t.Context(), nativeControlStream, ""); err != nil || stats.Length != 1 {
		t.Fatalf("replicas amplified the same periodic repair: %+v %v", stats, err)
	}
	if more, err := peer.Step(t.Context()); err != nil || !more {
		t.Fatalf("periodic repair did not reconcile current source ownership: %v %v", more, err)
	}
	if hooks.inventories != 1 || hooks.values != 0 {
		t.Fatalf("periodic repair failed to refresh already-enrolled inventory: %+v", hooks)
	}
	leases, err := queue.LeaseRuns(t.Context(), "inspect-repair", 1)
	if err != nil || len(leases) != 1 || leases[0].Task.Enumeration == nil || leases[0].Task.Observation == "" ||
		!validRunObservation(leases[0].Task.Observation) {
		t.Fatalf("repair reused the once-per-enrollment historical identity: %+v %v", leases, err)
	}
}

func TestNativeInventorySyncQueuesIndependentRefreshAfterHourlyRepair(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue := nativeEventQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	hooks := &nativeRefreshFixture{}
	worker := nativeEventWorker(queue, enrollment, hooks)
	if err := worker.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if more, err := worker.Step(t.Context()); err != nil || !more || hooks.inventories != 1 {
		t.Fatalf("hourly repair: %v %v %+v", more, err, hooks)
	}
	before, err := queue.Tasks.QueueStats(t.Context(), runEnumerationStream, "")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := queue.SyncInventory(t.Context(), enrollment); err != nil {
			t.Fatal(err)
		}
	}
	if hooks.inventories != 1 {
		t.Fatal("manual admission fetched inventory inline")
	}
	if stats, err := queue.Tasks.QueueStats(t.Context(), nativeControlStream, ""); err != nil || stats.Length != 1 {
		t.Fatalf("pending sync did not coalesce: %+v %v", stats, err)
	}
	// A manual task must not require the App enumerator or scope reconciler.
	worker.Client = rejectingNativeScope{}
	worker.ScopeRefresher.Client = rejectingNativeScope{}
	if more, err := worker.Step(t.Context()); err != nil || !more || hooks.inventories != 2 {
		t.Fatalf("manual refresh suppressed by hourly receipt: %v %v %+v", more, err, hooks)
	}
	if err := queue.SyncInventory(t.Context(), enrollment); err != nil {
		t.Fatal(err)
	}
	if more, err := worker.Step(t.Context()); err != nil || !more || hooks.inventories != 3 {
		t.Fatalf("post-completion refresh suppressed: %v %v %+v", more, err, hooks)
	}
	after, err := queue.Tasks.QueueStats(t.Context(), runEnumerationStream, "")
	if err != nil || before.Length != after.Length || hooks.values != 0 || hooks.outcomes != 0 {
		t.Fatalf("inventory-only sync expanded work: before=%+v after=%+v hooks=%+v err=%v", before, after, hooks, err)
	}
}

func TestNativeInventorySyncFencesWithdrawnEnrollment(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue := nativeEventQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	if err := queue.SyncInventory(t.Context(), enrollment); err != nil {
		t.Fatal(err)
	}
	if _, err := enrollment.RemoveRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	hooks := &nativeRefreshFixture{}
	if more, err := nativeEventWorker(queue, enrollment, hooks).Step(t.Context()); err != nil || !more || hooks.inventories != 0 {
		t.Fatalf("old scope refreshed new enrollment: %v %v %+v", more, err, hooks)
	}
}

func TestNativeInventorySyncRejectsUnsupportedQueueAndScope(t *testing.T) {
	if err := (Queue{}).SyncInventory(t.Context(), Enrollment{}); !errors.Is(err, collectnative.ErrUnsupported) {
		t.Fatalf("missing capabilities = %v", err)
	}
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue := nativeEventQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	queue.Tasks = unsupportedPendingQueue{TaskQueue: queue.Tasks}
	if err := queue.SyncInventory(t.Context(), enrollment); !errors.Is(err, collectnative.ErrUnsupported) {
		t.Fatalf("unsupported queue = %v", err)
	}
}

type unsupportedPendingQueue struct{ operational.TaskQueue }

type rejectingNativeScope struct{ nativeScopeFixture }

func (rejectingNativeScope) ListInstallationsPage(context.Context, int) ([]githubapp.Installation, int, error) {
	return nil, 0, errors.New("manual sync must not enumerate App installations")
}

func TestNativeRefreshCadenceIsAtLeastDaily(t *testing.T) {
	if got := (NativeWorker{}).interval(); got != time.Hour {
		t.Fatalf("default cadence = %v", got)
	}
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue := nativeEventQueue(t)
	worker := nativeEventWorker(queue, nativeEnrollmentFixture(t, data, queue), &nativeRefreshFixture{})
	for _, duration := range []time.Duration{time.Hour, 24 * time.Hour} {
		worker.RefreshInterval = duration
		if err := worker.validate(); err != nil {
			t.Fatalf("supported cadence %v: %v", duration, err)
		}
	}
	worker.RefreshInterval = 24*time.Hour + time.Second
	if err := worker.validate(); !errors.Is(err, collectnative.ErrUnsupported) {
		t.Fatalf("slower-than-daily cadence = %v", err)
	}
}
