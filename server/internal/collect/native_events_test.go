package collect

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/mining"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/query"
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
func (f *nativeRefreshFixture) Due(context.Context, collectnative.EnrollmentScope, time.Time) (bool, error) {
	return false, nil
}
func (f *nativeRefreshFixture) Refresh(context.Context, collectnative.EnrollmentScope, bool) (bool, error) {
	f.outcomes++
	return false, nil
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
		Client: client, Inventory: hooks, Values: hooks, Lifecycles: hooks, WindowDays: 7,
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

func TestNativeIssueWebhooksCoalesceIntoAuthoritativeLifecycleRefresh(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue := nativeEventQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"action":"closed","installation":{"id":7},"repository":{"full_name":"octo/api"},
		"issue":{"number":7,"state":"closed","state_reason":"completed",
		"closed_at":"2026-11-15T12:00:00Z","updated_at":"2026-11-15T12:00:00Z",
		"html_url":"https://github.com/octo/api/issues/7"}}`)
	admitter := Admitter{Enrollment: enrollment, Queue: queue}
	first, err := admitter.AdmitDelivery(t.Context(), "issues", payload, "lifecycle-1", time.Hour)
	if err != nil || !first.Enqueued {
		t.Fatalf("first lifecycle hint = %+v %v", first, err)
	}
	second, err := admitter.AdmitDelivery(t.Context(), "issues", payload, "lifecycle-2", time.Hour)
	if err != nil || second.Enqueued || second.Duplicate {
		t.Fatalf("coalesced lifecycle hint = %+v %v", second, err)
	}
	hooks := &nativeRefreshFixture{}
	if more, err := nativeEventWorker(queue, enrollment, hooks).Step(t.Context()); err != nil || !more ||
		hooks.outcomes != 1 {
		t.Fatalf("authoritative lifecycle refresh = %v %v refreshes=%d", more, err, hooks.outcomes)
	}
}

func TestNativeIssueWebhookRefreshesCrossRepositoryProducingLifecycle(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	now := time.Now().UTC().Truncate(time.Microsecond)
	runID := "github:run:octo/control:303"
	lifecycle, err := mining.OutputLifecycleRow(
		"safe-output", "producer", "workflow", runID, "create_issue", "Cross-repository proposal",
		"octo/target", "https://github.com/octo/target/issues/42",
		now.Add(-time.Hour), now.Add(-time.Hour), "gh-aw-logs",
	)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := data.BeginIngestion(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	for _, record := range []struct {
		source string
		row    model.Row
	}{
		{"$repositories", model.Row{"id": "producer", "owner": "octo", "name": "control"}},
		{"$workflows", model.Row{"id": "workflow", "repositoryId": "producer", "path": ".github/workflows/test.md"}},
		{"$runs", model.Row{"id": runID, "repositoryId": "producer", "workflowId": "workflow",
			"owner": "octo", "repository": "control", "githubRunId": "303"}},
		{"$outputLifecycles", lifecycle},
	} {
		if err := writer.Append(t.Context(), record.source, record.row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Publish(t.Context(), "cross-repository-lifecycle"); err != nil {
		t.Fatal(err)
	}

	queue := nativeEventQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 8, []string{"octo/control"}); err != nil {
		t.Fatal(err)
	}
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/target"}); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"action":"closed","installation":{"id":7},"repository":{"full_name":"octo/target"},
		"issue":{"number":42,"state":"closed","state_reason":"completed",
		"closed_at":"2026-11-15T12:00:00Z","updated_at":"2026-11-15T12:00:00Z",
		"html_url":"https://github.com/octo/target/issues/42"}}`)
	admitter := Admitter{Enrollment: enrollment, Queue: queue}
	admission, err := admitter.AdmitDelivery(t.Context(), "issues", payload, "cross-lifecycle", time.Hour)
	if err != nil || !admission.Enqueued {
		t.Fatalf("cross-repository lifecycle hint = %+v %v", admission, err)
	}
	dispositionAt := now.Add(-time.Minute)
	reader := &lifecycleReaderFixture{outcome: mining.Outcome{
		Repository: "octo/target", Number: 42, URL: "https://github.com/octo/target/issues/42",
		State: "closed", StateReason: "completed", CloseActor: "User",
		DispositionAt: &dispositionAt, UpdatedAt: dispositionAt,
		ObservedAt: now, Source: "github-api",
	}}
	hooks := &nativeRefreshFixture{}
	client := nativeScopeFixture{
		installations: []githubapp.Installation{{ID: 7}, {ID: 8}},
		repositories: map[int64][]githubapp.Repository{
			7: {{FullName: "octo/target"}},
			8: {{FullName: "octo/control"}},
		},
	}
	worker := NativeWorker{
		Queue: queue, Enrollment: enrollment, Consumer: "native-control",
		Client: client, Inventory: hooks, Values: hooks,
		Lifecycles: NativeLifecycleService{
			Store: data.Acquisition(), Client: reader,
			Quota: &fakeRunQuota{known: true}, QuotaApp: "github-app-123",
		},
		WindowDays: 7,
		ScopeRefresher: ScopeRefresher{
			Client: client, Quota: &fakeRunQuota{known: true}, QuotaApp: "github-app-123",
		},
	}
	if more, err := worker.Step(t.Context()); err != nil || !more {
		t.Fatalf("cross-repository lifecycle worker = more:%t err:%v", more, err)
	}
	results, _, err := data.ExecuteSQLPlan(t.Context(), []query.Definition{
		{Name: "lifecycles", From: "$outputLifecycles"},
	}, []string{"lifecycles"})
	if err != nil {
		t.Fatal(err)
	}
	rows := results["lifecycles"].Rows
	if len(rows) != 1 || rows[0]["repositoryId"] != "producer" ||
		rows[0]["targetRepository"] != "octo/target" || rows[0]["adoptionState"] != "accepted" ||
		reader.calls != 1 || reader.installation != 7 || reader.target.Repository != "octo/target" {
		t.Fatalf("cross-repository lifecycle result = rows:%+v reader:%+v", rows, reader)
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
