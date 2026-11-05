package collect

import (
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

func TestNativeReporterIncludesExactDiscoveryControlAndDelayedWork(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, _, _ := nativeRunQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	scope := nativeScope(t, enrollment, "octo/api")
	exact := historicalNativeTask()
	exact.ScopeEpoch = scope.Epoch
	if _, err := queue.EnqueueRun(t.Context(), exact); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Truncate(time.Second)
	if _, err := queue.EnqueueRun(t.Context(), RunTask{Repository: scope.Repository, InstallationID: scope.InstallationID,
		ScopeEpoch: scope.Epoch, Enumeration: &RunEnumeration{Page: 1, WindowDays: 1,
			After: before.Add(-24 * time.Hour), Before: before}}); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.enqueueNativeControl(t.Context(), Task{Repository: scope.Repository, InstallationID: scope.InstallationID,
		ScopeEpoch: scope.Epoch, ValueRefresh: true, EnqueuedAt: before}, IntentCollect, "", 0); err != nil {
		t.Fatal(err)
	}
	services := queue.Tasks.(operational.Store).OperationalServices()
	reporter := Reporter{Enrollment: enrollment, Queue: queue, Data: data,
		Backfill: Backfill{StateStore: services.State}, Metrics: services.IngestionMetrics}
	status, err := reporter.Snapshot(t.Context())
	if err != nil || status.QueueDepth != 3 || status.PendingTasks != 0 || status.Health != "recovering" {
		t.Fatalf("native queues were reported as an idle legacy queue: %+v %v", status, err)
	}
	leases, err := queue.nativeControlLease(t.Context(), "status-consumer", time.Hour)
	if err != nil || len(leases) != 1 {
		t.Fatalf("native control lease unavailable: %+v %v", leases, err)
	}
	status, err = reporter.Snapshot(t.Context())
	if err != nil || status.QueueDepth != 2 || status.PendingTasks != 1 {
		t.Fatalf("native pending receipt omitted from status: %+v %v", status, err)
	}
	leases[0].Task.NotBefore = before.Add(time.Hour)
	if err := queue.retryNativeControl(t.Context(), leases[0], false); err != nil {
		t.Fatal(err)
	}
	status, err = reporter.Snapshot(t.Context())
	if err != nil || status.QueueDepth != 3 || status.PendingTasks != 0 {
		t.Fatalf("native scheduled work omitted from status: %+v %v", status, err)
	}
	invalid := leases[0].Task
	invalid.Lifecycle, invalid.ScopeEpoch = "deleted", 0
	if validNativeControl(invalid) {
		t.Fatal("mixed lifecycle/control envelope was accepted")
	}
}
