package collect

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/githubquota"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

func TestNativeRunQuotaExhaustionPreservesAttemptAndFailureBudget(t *testing.T) {
	queue, _, _ := nativeRunQueue(t)
	services := queue.Tasks.(operational.Store).OperationalServices()
	quota, err := githubquota.New(services.GitHubQuota, githubquota.Options{SafetyReserve: 10})
	if err != nil {
		t.Fatal(err)
	}
	bucket := githubquota.BucketID{App: "native-test", Installation: 7, Resource: "core"}
	reset := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	if err := quota.Observe(t.Context(), bucket, githubquota.Observation{Limit: 100, Remaining: 0, ResetAt: reset}); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.EnqueueRun(t.Context(), historicalNativeTask()); err != nil {
		t.Fatal(err)
	}
	worker := RunWorker{Queue: queue, Consumer: "quota", Collector: runAttemptFunc(func(ctx context.Context, _ collectnative.Identity) error {
		_, err := quota.Reserve(ctx, bucket, githubquota.ReservationRequest{EstimatedCost: 1})
		return err
	})}
	if more, err := worker.Step(t.Context()); !more || !errors.Is(err, githubquota.ErrExhausted) {
		t.Fatalf("quota exhaustion was not explicit: %t %v", more, err)
	}
	if count, err := queue.Tasks.PromoteTasks(t.Context(), delayedRunTaskSet, runTaskStream, reset.Add(time.Minute), 1); err != nil || count != 1 {
		t.Fatalf("quota task not durably deferred: %d %v", count, err)
	}
	leases, err := queue.LeaseRuns(t.Context(), "inspect", 1)
	if err != nil || len(leases) != 1 || leases[0].Task.Failures != 0 || leases[0].Task.Attempt != 3 ||
		leases[0].Task.NotBefore.Before(reset) {
		t.Fatalf("quota deferral changed attempt or charged a failure: %v %v", leases, err)
	}
}
