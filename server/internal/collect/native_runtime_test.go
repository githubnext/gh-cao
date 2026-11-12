package collect

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
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
