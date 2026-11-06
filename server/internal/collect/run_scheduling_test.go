package collect

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type scheduledNativeHistory struct {
	*nativeHistoryFixture
	exact      *atomic.Int64
	discovered chan int64
}

func (p scheduledNativeHistory) DiscoveryWorkflows(ctx context.Context, scope collectnative.EnrollmentScope, page int) ([]int64, int, error) {
	select {
	case p.discovered <- p.exact.Load():
	default:
	}
	return p.nativeHistoryFixture.DiscoveryWorkflows(ctx, scope, page)
}

func TestNativeRunningWorkerDoesNotStarveHistoryBehindExactAttempts(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, _, _ := nativeRunQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}

	for run := int64(303); run < 309; run++ {
		if _, err := queue.EnqueueRun(t.Context(), RunTask{
			Repository: "octo/api", InstallationID: 7, ScopeEpoch: nativeScope(t, enrollment, "octo/api").Epoch,
			RunID: run, Attempt: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	before := time.Now().UTC().Truncate(time.Second)
	backfill := Backfill{Queue: queue, WindowDays: 7, AcquisitionScopes: enrollment.Authority, runBefore: before}
	if _, _, err := backfill.enqueueHistoricalRuns(t.Context(), []enrolledRepository{{name: "octo/api", installationID: 7}}); err != nil {
		t.Fatal(err)
	}
	var exact atomic.Int64
	discovered := make(chan int64, 1)
	worker := RunWorker{Queue: queue, Consumer: "scheduler", Scopes: enrollment.Authority,
		Collector: runAttemptFunc(func(context.Context, collectnative.Identity) error {
			exact.Add(1)
			return nil
		}),
		Discovery: scheduledNativeHistory{nativeHistoryFixture: &nativeHistoryFixture{
			after: before.Add(-7 * 24 * time.Hour), before: before,
		}, exact: &exact, discovered: discovered}}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	select {
	case count := <-discovered:
		cancel()
		if count != 1 {
			t.Errorf("history waited for %d exact attempts instead of the next scheduling turn", count)
		}
	case <-ctx.Done():
		t.Error("running worker starved durable history")
	}
	if err := <-done; !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("worker did not shut down: %v", err)
	}
}

func TestNativeFreshHistoryTurnPrecedesReclaimableExactAttempt(t *testing.T) {
	data := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, _, _ := nativeRunQueue(t)
	enrollment := nativeEnrollmentFixture(t, data, queue)
	if err := enrollment.AddRepositories(t.Context(), 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	task := historicalNativeTask()
	task.ScopeEpoch = nativeScope(t, enrollment, task.Repository).Epoch
	if _, err := queue.EnqueueRun(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	abandoned, err := queue.LeaseRuns(t.Context(), "terminated", 1)
	if err != nil || len(abandoned) != 1 {
		t.Fatalf("initial exact-attempt lease: %v %v", abandoned, err)
	}
	before := time.Now().UTC().Truncate(time.Second)
	backfill := Backfill{Queue: queue, WindowDays: 7, AcquisitionScopes: enrollment.Authority, runBefore: before}
	if _, _, err := backfill.enqueueHistoricalRuns(t.Context(), []enrolledRepository{{name: "octo/api", installationID: 7}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2100 * time.Millisecond)
	exact := 0
	fixture := &nativeHistoryFixture{after: before.Add(-7 * 24 * time.Hour), before: before}
	worker := RunWorker{Queue: queue, Consumer: "successor", Scopes: enrollment.Authority, Discovery: fixture,
		AcquisitionTimeout: time.Second, ReclaimAfter: 2 * time.Second,
		Collector: runAttemptFunc(func(context.Context, collectnative.Identity) error {
			exact++
			return nil
		})}
	if more, err := worker.step(t.Context(), true); err != nil || !more || fixture.calls != 1 || exact != 0 {
		t.Fatalf("exact reclaim preempted the fresh history turn: more=%v discovery=%d exact=%d err=%v",
			more, fixture.calls, exact, err)
	}
	if more, err := worker.step(t.Context(), false); err != nil || !more || exact != 1 {
		t.Fatalf("fair scheduling lost abandoned exact-attempt recovery: more=%v exact=%d err=%v", more, exact, err)
	}
}
