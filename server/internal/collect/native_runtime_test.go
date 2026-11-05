package collect

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/githubapp"
	"github.com/githubnext/gh-cao/server/internal/mining"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type nativeRuntimeHooks struct {
	values chan collectnative.EnrollmentScope
}

func (nativeRuntimeHooks) RefreshInventory(context.Context, collectnative.EnrollmentScope) error {
	return nil
}
func (nativeRuntimeHooks) RecordOutcome(context.Context, collectnative.EnrollmentScope, mining.Outcome) error {
	return nil
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

func TestExistingWorkerRunsNativeWebhookPublicationAndControlWithoutFiles(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue := nativeEventQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	admitter := Admitter{Enrollment: enrollment, Queue: queue}
	if _, err := admitter.AdmitDelivery(t.Context(), "workflow_run", []byte(nativeCompletedPayload), "runtime-completion", time.Hour); err != nil {
		t.Fatal(err)
	}
	client := nativeScopeFixture{installations: []githubapp.Installation{{ID: 7}},
		repositories: map[int64][]githubapp.Repository{7: {{FullName: "octo/api"}}}}
	hooks := nativeRuntimeHooks{values: make(chan collectnative.EnrollmentScope, 1)}
	published := make(chan int, 1)
	native := NativeWorker{Queue: queue, Enrollment: enrollment, Client: client, Inventory: hooks, Values: hooks, Outcomes: hooks,
		Consumer: "existing-native-runtime", WindowDays: 7,
		ScopeRefresher: ScopeRefresher{Client: client, Quota: &fakeRunQuota{known: true}, QuotaApp: "github-app-123"},
		Runs: RunWorker{Collector: collectnative.Collector{
			Provider: historicalNativeProvider{recorded: time.Now().UTC().Format(time.RFC3339)}, Store: data.Acquisition()},
			Discovery: emptyNativeHistory{}, Publications: notifyingNativePublications{PublicationConsumer: data.Acquisition(), done: published}}}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	t.Setenv("PATH", "/nonexistent")
	done := make(chan error, 1)
	go func() { done <- (Worker{Native: &native}).Run(ctx) }()
	select {
	case <-published:
	case <-ctx.Done():
		t.Error("existing Worker did not run native publication")
	}
	select {
	case scope := <-hooks.values:
		if scope.Repository != "octo/api" || scope.InstallationID != 7 || scope.Epoch <= 0 {
			t.Errorf("native Value invalidation lost exact scope: %+v", scope)
		}
	case <-ctx.Done():
		t.Error("existing Worker did not run native control")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("native roles failed to join on cancellation: %v", err)
	}
	nativeCanonicalCount(t, data, "$runs", 1)
	if stats, err := queue.Tasks.QueueStats(t.Context(), taskStream, ""); err != nil || stats.Length != 0 {
		t.Fatalf("existing worker fell back to the legacy pipeline: %+v %v", stats, err)
	}
	if err := (Worker{Enrollment: enrollment}).Run(t.Context()); !errors.Is(err, collectnative.ErrUnsupported) {
		t.Fatalf("native enrollment silently selected the legacy worker: %v", err)
	}
}

func TestNativePeriodicRepairDeduplicatesAcrossReplicasAndUsesNewObservation(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue := nativeEventQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	worker := nativeEventWorker(queue, enrollment, &nativeRefreshFixture{})
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
	leases, err := queue.LeaseRuns(t.Context(), "inspect-repair", 1)
	if err != nil || len(leases) != 1 || leases[0].Task.Enumeration == nil || leases[0].Task.Observation == "" ||
		!validRunObservation(leases[0].Task.Observation) {
		t.Fatalf("repair reused the once-per-enrollment historical identity: %+v %v", leases, err)
	}
}
