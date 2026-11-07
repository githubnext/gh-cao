package collect

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/mining"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

const nativeCompletedPayload = `{"action":"completed","installation":{"id":7},"repository":{"full_name":"octo/api"},
	"workflow_run":{"id":303,"run_attempt":3,"path":".github/workflows/test.lock.yml","status":"completed"}}`

type nativeRefreshFixture struct {
	inventories, values, outcomes int
}

func (f *nativeRefreshFixture) RefreshInventory(context.Context, collectnative.EnrollmentScope) error {
	f.inventories++
	return nil
}
func (f *nativeRefreshFixture) InvalidateValue(context.Context, collectnative.EnrollmentScope) error {
	f.values++
	return nil
}
func (f *nativeRefreshFixture) RecordOutcome(context.Context, collectnative.EnrollmentScope, mining.Outcome) error {
	f.outcomes++
	return nil
}

func nativeEventQueue(t *testing.T) Queue {
	t.Helper()
	queue, _, _ := nativeRunQueue(t)
	services := queue.Tasks.(operational.Store).OperationalServices()
	queue.Admission, queue.Deliveries = services.Admission, services.Deliveries
	queue.Metadata, queue.Leases, queue.Metrics = services.Collection, services.Leases, services.IngestionMetrics
	return queue
}

func nativeEventWorker(queue Queue, enrollment Enrollment, hooks *nativeRefreshFixture) NativeWorker {
	client := nativeScopeFixture{installations: []githubapp.Installation{{ID: 7}},
		repositories: map[int64][]githubapp.Repository{7: {{FullName: "octo/api"}}}}
	return NativeWorker{Queue: queue, Enrollment: enrollment, Consumer: "native-control",
		Client: client, Inventory: hooks, Values: hooks, Outcomes: hooks, WindowDays: 7,
		ScopeRefresher: ScopeRefresher{Client: client, Quota: &fakeRunQuota{known: true}, QuotaApp: "github-app-123"}}
}

type completedNativeProvider struct {
	historicalNativeProvider
	calls int
}

func (p *completedNativeProvider) Attempt(ctx context.Context, identity collectnative.Identity) (collectnative.Run, error) {
	p.calls++
	return p.historicalNativeProvider.Attempt(ctx, identity)
}

func TestNativeCompletedWebhookRefreshesHistoricalAttemptWithoutLegacyTasks(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue := nativeEventQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	task := historicalNativeTask()
	task.ScopeEpoch = nativeScope(t, enrollment, task.Repository).Epoch
	if _, err := queue.EnqueueRun(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	provider := &completedNativeProvider{historicalNativeProvider: historicalNativeProvider{
		recorded: time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)}}
	runs := RunWorker{Queue: queue, Consumer: "native-events",
		Collector: collectnative.Collector{Provider: provider, Store: data.Acquisition()}}
	t.Setenv("PATH", "/nonexistent")
	if more, err := runs.Step(t.Context()); err != nil || !more {
		t.Fatalf("historical attempt: %v %v", more, err)
	}
	if count, err := data.Acquisition().Replay(t.Context(), 10); err != nil || count != 1 {
		t.Fatalf("historical publication: %d %v", count, err)
	}
	provider.recorded = time.Now().UTC().Format(time.RFC3339)
	admitter := Admitter{Enrollment: enrollment, Queue: queue}
	first, err := admitter.AdmitDelivery(t.Context(), "workflow_run", []byte(nativeCompletedPayload), "completed-1", time.Hour)
	if err != nil || !first.Enqueued || first.Duplicate {
		t.Fatalf("native completion admission: %+v %v", first, err)
	}
	duplicate, err := admitter.AdmitDelivery(t.Context(), "workflow_run", []byte(nativeCompletedPayload), "completed-1", time.Hour)
	if err != nil || !duplicate.Duplicate || duplicate.Enqueued {
		t.Fatalf("native completion deduplication: %+v %v", duplicate, err)
	}
	if more, err := runs.Step(t.Context()); err != nil || !more || provider.calls != 2 {
		t.Fatalf("historical identity suppressed completion refresh: more=%v calls=%d err=%v", more, provider.calls, err)
	}
	if count, err := data.Acquisition().Replay(t.Context(), 10); err != nil || count != 1 {
		t.Fatalf("completion publication: %d %v", count, err)
	}
	hooks := &nativeRefreshFixture{}
	if more, err := nativeEventWorker(queue, enrollment, hooks).Step(t.Context()); err != nil || !more || hooks.values != 1 {
		t.Fatalf("completion lost its durable Value invalidation: more=%v values=%d err=%v", more, hooks.values, err)
	}
	if stats, err := queue.Tasks.QueueStats(t.Context(), taskStream, ""); err != nil || stats.Length != 0 {
		t.Fatalf("native admission retained legacy repository work: %+v %v", stats, err)
	}
	nativeCanonicalCount(t, data, "$runs", 1)
}

func TestNativeDeliveryRetriesAfterPartialAdmissionAndFencesControlACK(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue := nativeEventQueue(t)
	queue.MaxLength = 1
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	scope := nativeScope(t, enrollment, "octo/api")
	if _, err := queue.enqueueNativeControl(t.Context(), Task{Repository: scope.Repository, InstallationID: 7,
		ScopeEpoch: scope.Epoch, ValueRefresh: true}, IntentValueRefresh, "", 0); err != nil {
		t.Fatal(err)
	}
	admitter := Admitter{Enrollment: enrollment, Queue: queue}
	if _, err := admitter.AdmitDelivery(t.Context(), "workflow_run", []byte(nativeCompletedPayload), "partial-1", time.Hour); !errors.Is(err, operational.ErrCapacity) {
		t.Fatalf("full control queue falsely completed admission: %v", err)
	}
	old, err := queue.nativeControlLease(t.Context(), "same-name", 0)
	if err != nil || len(old) != 1 {
		t.Fatalf("initial control lease: %v %v", old, err)
	}
	fresh, err := queue.nativeControlLease(t.Context(), "same-name", 0)
	if err != nil || len(fresh) != 1 {
		t.Fatalf("reclaimed control lease: %v %v", fresh, err)
	}
	if err := queue.completeNativeControl(t.Context(), old[0]); !errors.Is(err, operational.ErrTaskLeaseLost) {
		t.Fatalf("old control lease acknowledged its successor: %v", err)
	}
	if err := queue.completeNativeControl(t.Context(), fresh[0]); err != nil {
		t.Fatal(err)
	}
	retry, err := admitter.AdmitDelivery(t.Context(), "workflow_run", []byte(nativeCompletedPayload), "partial-1", time.Hour)
	if err != nil || !retry.Enqueued || retry.Duplicate {
		t.Fatalf("partial admission was not retryable: %+v %v", retry, err)
	}
	if stats, err := queue.Tasks.QueueStats(t.Context(), runTaskStream, ""); err != nil || stats.Length != 1 {
		t.Fatalf("retry duplicated or lost the exact attempt: %+v %v", stats, err)
	}
}

func TestNativeQueuedInvalidationCannotRetargetReenrollment(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue := nativeEventQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	admitter := Admitter{Enrollment: enrollment, Queue: queue}
	if _, err := admitter.AdmitDelivery(t.Context(), "workflow_run", []byte(nativeCompletedPayload), "old-epoch", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := enrollment.RemoveRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	hooks := &nativeRefreshFixture{}
	if more, err := nativeEventWorker(queue, enrollment, hooks).Step(t.Context()); err != nil || !more || hooks.values != 0 {
		t.Fatalf("old consent invalidated the new enrollment: %v %v values=%d", more, err, hooks.values)
	}
	replayed, err := admitter.AdmitDelivery(t.Context(), "workflow_run", []byte(nativeCompletedPayload), "old-epoch", time.Hour)
	if err != nil || !replayed.Duplicate {
		t.Fatalf("old delivery reacquired authority: %+v %v", replayed, err)
	}
	if leases, err := queue.LeaseRuns(t.Context(), "inspect", 10); err != nil || len(leases) != 1 ||
		leases[0].Task.ScopeEpoch == nativeScope(t, enrollment, "octo/api").Epoch {
		t.Fatalf("delivery replay retargeted its exact attempt: %+v %v", leases, err)
	}
}

func TestNativeLifecycleUsesFreshAppCoverageInsteadOfStaleWebhook(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue := nativeEventQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	admitter := Admitter{Enrollment: enrollment, Queue: queue}
	if _, err := admitter.AdmitDelivery(t.Context(), "installation", []byte(`{"action":"deleted","installation":{"id":7}}`),
		"stale-deletion", time.Hour); err != nil {
		t.Fatal(err)
	}
	hooks := &nativeRefreshFixture{}
	worker := nativeEventWorker(queue, enrollment, hooks)
	t.Setenv("PATH", "/nonexistent")
	if more, err := worker.Step(t.Context()); err != nil || !more || hooks.inventories != 1 {
		t.Fatalf("fresh native lifecycle reconciliation: %v %v inventories=%d", more, err, hooks.inventories)
	}
	if scope := nativeScope(t, enrollment, "octo/api"); scope.InstallationID != 7 {
		t.Fatal("late webhook erased current authenticated scope")
	}
	if _, err := admitter.AdmitDelivery(t.Context(), "installation", []byte(`{"action":"created","installation":{"id":7}}`),
		"stale-addition", time.Hour); err != nil {
		t.Fatal(err)
	}
	worker.Client = nativeScopeFixture{}
	if more, err := worker.Step(t.Context()); err != nil || !more {
		t.Fatalf("current App withdrawal: %v %v", more, err)
	}
	if _, err := enrollment.Authority.CurrentScope(t.Context(), "octo/api"); !errors.Is(err, collectnative.ErrIdentity) {
		t.Fatalf("stale addition revived withdrawn scope: %v", err)
	}
}
