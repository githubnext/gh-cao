package collect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/memory"
	operationalpostgres "github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type runAttemptFunc func(context.Context, collectnative.Identity) error

func (f runAttemptFunc) CollectAttempt(ctx context.Context, identity collectnative.Identity) error {
	return f(ctx, identity)
}

func nativeRunQueue(t *testing.T) (Queue, string, string) {
	t.Helper()
	dsn := os.Getenv("CAO_TEST_POSTGRES_URL")
	if dsn == "" {
		dsn = os.Getenv("POSTGRES_URL")
	}
	if dsn == "" {
		t.Skip("real PostgreSQL is required for historical native worker evidence")
	}
	namespace := fmt.Sprintf("native-run-worker-%d", time.Now().UnixNano())
	store, err := operationalpostgres.New(t.Context(), dsn, namespace, operationalpostgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := store.DeleteNamespace(ctx); err != nil {
			t.Error(err)
		}
		_ = store.Close()
	})
	queue := Queue{Tasks: store, MaxLength: 10, MaxAttempts: 2}
	if err := queue.EnsureRuns(t.Context()); err != nil {
		t.Fatal(err)
	}
	return queue, dsn, namespace
}

func historicalNativeTask() RunTask {
	return RunTask{Repository: "octo/api", InstallationID: 7, RunID: 303, Attempt: 3}
}

type historicalNativeProvider struct {
	recorded string
}

func (p historicalNativeProvider) Runs(context.Context, string, int64, time.Time, func(collectnative.Run) error) error {
	return errors.New("historical acquisition must not discover runs")
}

func (p historicalNativeProvider) Attempt(_ context.Context, identity collectnative.Identity) (collectnative.Run, error) {
	task := historicalNativeTask()
	if identity.Repository != task.Repository || identity.InstallationID != task.InstallationID || identity.RunID != task.RunID || identity.Attempt != task.Attempt {
		return collectnative.Run{}, collectnative.ErrIdentity
	}
	return collectnative.Run{Identity: identity, WorkflowPath: ".github/workflows/test.lock.yml", Fields: map[string]any{
		"name": "Test", "status": "completed", "conclusion": "success",
		"created_at": p.recorded, "updated_at": p.recorded,
	}}, nil
}

func (p historicalNativeProvider) Artifacts(context.Context, collectnative.Run, func(collectnative.Artifact) error) error {
	return nil
}

type interruptedRunACK struct {
	operational.FencedTaskQueue
	before func(context.Context, string, string) error
}

func (q interruptedRunACK) CompleteTaskFenced(ctx context.Context, _, _, id, receipt string) error {
	return q.before(ctx, id, receipt)
}

func TestNativeRunWorkerRejectsUnfencedBackend(t *testing.T) {
	store, err := memory.New(memory.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	queue := Queue{Tasks: store}
	if err := queue.EnsureRuns(t.Context()); !errors.Is(err, operational.ErrUnsupported) {
		t.Fatalf("unfenced queue accepted: %v", err)
	}
}

func TestNativeRunWorkerCrashAfterPublicationRecoversInAnotherProcess(t *testing.T) {
	canonical, config := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, dsn, namespace := nativeRunQueue(t)
	task := historicalNativeTask()
	if err := canonical.Acquisition().SetEnrollment(t.Context(), task.Repository, task.InstallationID); err != nil {
		t.Fatal(err)
	}
	if admitted, err := queue.EnqueueRun(t.Context(), task); err != nil || !admitted {
		t.Fatalf("historical admission: %t %v", admitted, err)
	}
	if admitted, err := queue.EnqueueRun(t.Context(), task); err != nil || admitted {
		t.Fatalf("duplicate historical identity: %t %v", admitted, err)
	}
	recorded := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	collector := collectnative.Collector{Provider: historicalNativeProvider{recorded}, Store: canonical.Acquisition()}
	t.Setenv("PATH", "/nonexistent")
	task.Key = runTaskIdentity(task.Repository, task.RunID, task.Attempt)
	original := RunLease{Task: task}
	interrupted := errors.New("process lost after publication before ACK")
	faultQueue := queue
	faultQueue.Tasks = interruptedRunACK{
		FencedTaskQueue: queue.Tasks.(operational.FencedTaskQueue),
		before: func(ctx context.Context, id, receipt string) error {
			original.MessageID, original.Receipt = id, receipt
			if pending, err := canonical.Acquisition().Publications(ctx, 1); err != nil || len(pending) != 1 {
				t.Fatalf("worker attempted ACK before durable publication: %v %v", pending, err)
			}
			return interrupted
		},
	}
	worker := RunWorker{Queue: faultQueue, Collector: collector, Consumer: "crashed"}
	if more, err := worker.Step(t.Context()); !more || !errors.Is(err, interrupted) {
		t.Fatalf("publication/ACK interruption: %t %v", more, err)
	}
	if pending, err := canonical.Acquisition().Publications(t.Context(), 1); err != nil || len(pending) != 1 {
		t.Fatalf("no durable intent before simulated process loss: %v %v", pending, err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G204 -- invokes this test executable, never external collection tooling.
	child := exec.CommandContext(t.Context(), executable, "-test.run=^TestNativeRunWorkerRecoverySubprocess$", "-test.v")
	child.Env = []string{
		"PATH=/nonexistent", "CAO_NATIVE_RUN_DSN=" + dsn, "CAO_NATIVE_RUN_NAMESPACE=" + namespace,
		"CAO_NATIVE_RUN_SCHEMA=" + config.RuntimeParams["search_path"], "CAO_NATIVE_RUN_RECORDED=" + recorded,
	}
	output, err := child.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "historical durable handoff verified") {
		t.Fatalf("separate process recovery: %v %s", err, output)
	}
	if err := queue.CompleteRun(t.Context(), original); !errors.Is(err, operational.ErrTaskLeaseLost) {
		t.Fatalf("crashed worker ACK accepted: %v", err)
	}
	if err := queue.RetryRun(t.Context(), original); !errors.Is(err, operational.ErrTaskLeaseLost) {
		t.Fatalf("crashed worker retry accepted: %v", err)
	}
	stats, err := queue.Tasks.QueueStats(t.Context(), runTaskStream, queue.group())
	if err != nil || stats.Length != 0 || stats.Pending != 0 {
		t.Fatalf("durable task was not completed: %v %v", stats, err)
	}
	results, _, err := canonical.ExecuteSQLPlan(t.Context(), []query.Definition{{Name: "run", From: "$runs"}}, []string{"run"})
	if err != nil || len(results["run"].Rows) != 1 {
		t.Fatalf("canonical consumer lost the recovered run: %v", err)
	}
}

func TestNativeRunWorkerRecoverySubprocess(t *testing.T) {
	dsn := os.Getenv("CAO_NATIVE_RUN_DSN")
	if dsn == "" {
		t.Skip("invoked by the separate-process historical recovery test")
	}
	ctx := t.Context()
	store, err := operationalpostgres.Open(ctx, dsn, os.Getenv("CAO_NATIVE_RUN_NAMESPACE"), operationalpostgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	queue := Queue{Tasks: store}
	leases, err := queue.ReclaimRuns(ctx, "recovered", 0, 1)
	if err != nil || len(leases) != 1 || leases[0].Task.Attempt != 3 || leases[0].Task.Failures != 0 {
		t.Fatalf("recovered exact historical attempt: %v %v", leases, err)
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.RuntimeParams["search_path"] = os.Getenv("CAO_NATIVE_RUN_SCHEMA")
	canonical, err := postgresx.NewConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = canonical.Close() }()
	collector := collectnative.Collector{Provider: historicalNativeProvider{os.Getenv("CAO_NATIVE_RUN_RECORDED")}, Store: canonical.Acquisition()}
	worker := RunWorker{Queue: queue, Collector: collector, Consumer: "recovered"}
	if err := worker.process(ctx, leases[0]); err != nil {
		t.Fatal(err)
	}
	if activated, err := canonical.Acquisition().Replay(ctx, 1); err != nil || activated != 1 {
		t.Fatalf("post-ACK publication intent replay: %d %v", activated, err)
	}
	t.Log("historical durable handoff verified")
}

func TestNativeRunWorkerRetriesWithoutChangingRunAttempt(t *testing.T) {
	queue, _, _ := nativeRunQueue(t)
	task := historicalNativeTask()
	if _, err := queue.EnqueueRun(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	failed := errors.New("provider unavailable")
	worker := RunWorker{Queue: queue, Consumer: "worker", Collector: runAttemptFunc(func(_ context.Context, identity collectnative.Identity) error {
		if identity.Attempt != task.Attempt || identity.RunID != task.RunID {
			t.Fatalf("processing retry changed run identity: %v", identity)
		}
		return failed
	})}
	if more, err := worker.Step(t.Context()); !more || !errors.Is(err, failed) {
		t.Fatalf("failed acquisition: %t %v", more, err)
	}
	if promoted, err := queue.Tasks.PromoteTasks(t.Context(), delayedRunTaskSet, runTaskStream, time.Now().Add(time.Hour), 1); err != nil || promoted != 1 {
		t.Fatalf("durable retry: %d %v", promoted, err)
	}
	retried, err := queue.LeaseRuns(t.Context(), "inspect", 1)
	if err != nil || len(retried) != 1 || retried[0].Task.Failures != 1 || retried[0].Task.Attempt != 3 {
		t.Fatalf("retry identity/count: %v %v", retried, err)
	}
	retried[0].Task.NotBefore = time.Time{}
	if err := queue.replaceRun(t.Context(), retried[0], false); err != nil {
		t.Fatal(err)
	}
	if more, err := worker.Step(t.Context()); !more || !errors.Is(err, failed) {
		t.Fatalf("exhausted acquisition: %t %v", more, err)
	}
	stats, err := queue.Tasks.QueueStats(t.Context(), runDeadLetterStream, "")
	if err != nil || stats.Length != 1 {
		t.Fatalf("bounded failures did not dead-letter: %v %v", stats, err)
	}
}

func TestNativeRunWorkerShutdownRetainsUnacknowledgedWork(t *testing.T) {
	queue, _, _ := nativeRunQueue(t)
	if _, err := queue.EnqueueRun(t.Context(), historicalNativeTask()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	worker := RunWorker{Queue: queue, Consumer: "worker", Collector: runAttemptFunc(func(context.Context, collectnative.Identity) error {
		cancel()
		return context.Canceled
	})}
	if more, err := worker.Step(ctx); !more || !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown: %t %v", more, err)
	}
	leases, err := queue.ReclaimRuns(t.Context(), "successor", 0, 1)
	if err != nil || len(leases) != 1 || leases[0].Task.Failures != 0 {
		t.Fatalf("shutdown destroyed or charged the task: %v %v", leases, err)
	}
}

func TestNativeRunWorkerClaimContentionDoesNotSpendRetryBudget(t *testing.T) {
	queue, _, _ := nativeRunQueue(t)
	queue.MaxAttempts = 1
	if _, err := queue.EnqueueRun(t.Context(), historicalNativeTask()); err != nil {
		t.Fatal(err)
	}
	worker := RunWorker{Queue: queue, Consumer: "worker", Collector: runAttemptFunc(func(context.Context, collectnative.Identity) error {
		return collectnative.ErrBusy
	})}
	if more, err := worker.Step(t.Context()); !more || !errors.Is(err, collectnative.ErrBusy) {
		t.Fatalf("claim contention: %t %v", more, err)
	}
	if promoted, err := queue.Tasks.PromoteTasks(t.Context(), delayedRunTaskSet, runTaskStream, time.Now().Add(time.Hour), 1); err != nil || promoted != 1 {
		t.Fatalf("contention was not deferred: %d %v", promoted, err)
	}
	leases, err := queue.LeaseRuns(t.Context(), "inspect", 1)
	if err != nil || len(leases) != 1 || leases[0].Task.Failures != 0 || leases[0].Task.Attempt != 3 {
		t.Fatalf("contention charged or changed run attempt: %v %v", leases, err)
	}
}

func TestNativeRunQueueRejectsForeignAndMalformedEnvelopes(t *testing.T) {
	queue, _, _ := nativeRunQueue(t)
	for _, fields := range []operational.TaskFields{
		{Repository: "octo/api", Task: "{", Key: "octo/api:303:3"},
		{Repository: "octo/other", Task: `{"repository":"octo/api","installationId":7,"runId":303,"attempt":3,"key":"octo/api:303:3"}`, Key: "octo/api:303:3"},
		{Repository: "octo/api", Task: `{"repository":"octo/api","installationId":7,"runId":303,"attempt":3,"key":"octo/api:303:3"}`, Key: "octo/api:303:2"},
	} {
		if _, err := queue.Tasks.EnqueueTask(t.Context(), operational.EnqueueRequest{Queue: runTaskStream, Fields: fields}); err != nil {
			t.Fatal(err)
		}
	}
	if leases, err := queue.LeaseRuns(t.Context(), "worker", 3); err != nil || len(leases) != 0 {
		t.Fatalf("foreign or malformed envelope was executable: %v %v", leases, err)
	}
	stats, err := queue.Tasks.QueueStats(t.Context(), runDeadLetterStream, "")
	if err != nil || stats.Length != 3 {
		t.Fatalf("invalid task did not leave explicit durable evidence: %v %v", stats, err)
	}
}

func TestNativeRunWorkersElectOneDurableAttemptAcrossClients(t *testing.T) {
	first, config := testutil.PostgresWithConfig(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	second, err := postgresx.NewConfig(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	queue, dsn, namespace := nativeRunQueue(t)
	other, err := operationalpostgres.Open(t.Context(), dsn, namespace, operationalpostgres.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	task := historicalNativeTask()
	if err := first.Acquisition().SetEnrollment(t.Context(), task.Repository, task.InstallationID); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.EnqueueRun(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	recorded := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	start := make(chan struct{})
	type result struct {
		more bool
		err  error
	}
	results := make(chan result, 2)
	peer := queue
	peer.Tasks = other
	for i, store := range []*postgresx.Store{first, second} {
		selected := []Queue{queue, peer}[i]
		worker := RunWorker{Queue: selected, Consumer: "shared-consumer-name",
			Collector: collectnative.Collector{Provider: historicalNativeProvider{recorded}, Store: store.Acquisition()}}
		go func() {
			<-start
			more, err := worker.Step(t.Context())
			results <- result{more, err}
		}()
	}
	close(start)
	processed := 0
	for range 2 {
		outcome := <-results
		if outcome.err != nil {
			t.Fatal(outcome.err)
		}
		if outcome.more {
			processed++
		}
	}
	if processed != 1 {
		t.Fatalf("concurrent consumers processed %d copies", processed)
	}
	if pending, err := second.Acquisition().Publications(t.Context(), 2); err != nil || len(pending) != 1 {
		t.Fatalf("concurrent task ACK lost publication intent: %v %v", pending, err)
	}
}

func TestNativeRunWorkerCannotEnrollItsOwnTask(t *testing.T) {
	canonical := testutil.Postgres(t, t.Context(), "CAO_TEST_POSTGRES_URL", "POSTGRES_URL")
	queue, _, _ := nativeRunQueue(t)
	task := historicalNativeTask()
	if _, err := queue.EnqueueRun(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	worker := RunWorker{Queue: queue, Consumer: "worker", Collector: collectnative.Collector{
		Provider: historicalNativeProvider{time.Now().UTC().Format(time.RFC3339)}, Store: canonical.Acquisition(),
	}}
	if more, err := worker.Step(t.Context()); !more || !errors.Is(err, collectnative.ErrIdentity) {
		t.Fatalf("unenrolled task was executable: %t %v", more, err)
	}
	if pending, err := canonical.Acquisition().Publications(t.Context(), 1); err != nil || len(pending) != 0 {
		t.Fatalf("unenrolled task published evidence: %v %v", pending, err)
	}
	identity := collectnative.Identity{Repository: task.Repository, InstallationID: task.InstallationID, RunID: task.RunID, Attempt: task.Attempt}
	if _, err := canonical.Acquisition().Claim(t.Context(), identity, time.Minute); !errors.Is(err, collectnative.ErrIdentity) {
		t.Fatalf("worker inferred enrollment from task: %v", err)
	}
}
