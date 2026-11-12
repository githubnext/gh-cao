package collect

import (
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/redisx"
)

type failingCommandClient struct {
	delegate redisx.CommandClient
	mu       sync.Mutex
	failEval bool
}

func (c *failingCommandClient) Do(ctx context.Context, command ...string) (any, error) {
	c.mu.Lock()
	if c.failEval && len(command) > 0 && command[0] == "EVAL" {
		c.failEval = false
		c.mu.Unlock()
		return nil, errors.New("injected Redis failure")
	}
	c.mu.Unlock()
	return c.delegate.Do(ctx, command...)
}

func (c *failingCommandClient) DoMany(ctx context.Context, commands [][]string) ([]any, error) {
	return c.delegate.DoMany(ctx, commands)
}

type ambiguousCommandClient struct {
	delegate redisx.CommandClient
	once     sync.Once
}

func (c *ambiguousCommandClient) Do(ctx context.Context, command ...string) (any, error) {
	value, err := c.delegate.Do(ctx, command...)
	ambiguous := false
	if len(command) > 0 && command[0] == "EVAL" {
		c.once.Do(func() { ambiguous = true })
	}
	if ambiguous && err == nil {
		return nil, errors.New("injected connection loss after Redis commit")
	}
	return value, err
}

func (c *ambiguousCommandClient) DoMany(ctx context.Context, commands [][]string) ([]any, error) {
	return c.delegate.DoMany(ctx, commands)
}

func integrationStore(t *testing.T) (*redisx.Store, context.Context) {
	t.Helper()
	rawURL := os.Getenv("REDIS_URL")
	if rawURL == "" {
		t.Skip("REDIS_URL is not set")
	}
	client, err := redisx.New(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	namespace, err := redisx.NormalizeNamespace("collect-" + strconv.FormatInt(time.Now().UnixNano(), 36))
	if err != nil {
		t.Fatal(err)
	}
	store := redisx.NewStore(client, namespace)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	if err := store.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	// Each test uses a unique namespace, so leftover keys never cross tests.
	return store, ctx
}

func TestEnrollmentTracksInstallationCoverage(t *testing.T) {
	store, ctx := integrationStore(t)
	enrollment := Enrollment{Metadata: store, Leases: store}
	if err := enrollment.AddRepositories(ctx, 7, []string{"Octo/API", "octo/web"}); err != nil {
		t.Fatal(err)
	}
	enrolled, err := enrollment.Enrolled(ctx, "octo/api")
	if err != nil {
		t.Fatal(err)
	}
	if !enrolled {
		t.Fatal("expected the repository to be enrolled")
	}
	installation, err := enrollment.InstallationFor(ctx, "octo/api")
	if err != nil {
		t.Fatal(err)
	}
	if installation != 7 {
		t.Fatalf("installation = %d, want 7", installation)
	}
	coverage, err := enrollment.Coverage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if coverage.Repositories != 2 || coverage.Installations != 1 {
		t.Fatalf("unexpected coverage %+v", coverage)
	}
	if _, err := enrollment.RemoveInstallation(ctx, 7); err != nil {
		t.Fatal(err)
	}
	enrolled, err = enrollment.Enrolled(ctx, "octo/api")
	if err != nil {
		t.Fatal(err)
	}
	if enrolled {
		t.Fatal("removing an installation must unenroll its repositories")
	}
}

func TestQueueDebouncesAndLeasesExactlyOnce(t *testing.T) {
	store, ctx := integrationStore(t)
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store, Debounce: time.Minute}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	task := Task{Repository: "octo/api", InstallationID: 7, Reason: "workflow_run"}
	first, err := queue.Enqueue(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	second, err := queue.Enqueue(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	if !first || second {
		t.Fatalf("expected a burst to collapse to one task, got first=%t second=%t", first, second)
	}
	depth, err := queue.Depth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if depth != 1 {
		t.Fatalf("depth = %d, want 1", depth)
	}
	leases, err := queue.Lease(ctx, "worker-a", 10, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 {
		t.Fatalf("leased %d tasks, want 1", len(leases))
	}
	other, err := queue.Lease(ctx, "worker-b", 10, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("a leased task must not be delivered twice, got %d", len(other))
	}
	// Admission clears the debounce marker so events arriving during a
	// collection schedule a follow-up instead of being lost.
	if err := queue.Admit(ctx, leases[0].Task); err != nil {
		t.Fatal(err)
	}
	followUp, err := queue.Enqueue(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	if !followUp {
		t.Fatal("an event during collection must schedule a follow-up")
	}
	if err := queue.Complete(ctx, leases[0]); err != nil {
		t.Fatal(err)
	}
	pending, err := queue.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("pending = %d, want 0 after completion", pending)
	}
}

func TestQueueDurablyDeduplicatesRunTasksByRepositoryRunAndAttempt(t *testing.T) {
	store, ctx := integrationStore(t)
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	task := RunTask{
		Repository: "Octo/API", InstallationID: 7, RunID: 4242, Attempt: 1,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	first, err := queue.EnqueueRun(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := queue.EnqueueRun(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	secondAttempt := task
	secondAttempt.Attempt = 2
	distinctAttempt, err := queue.EnqueueRun(ctx, secondAttempt)
	if err != nil {
		t.Fatal(err)
	}
	if !first || duplicate || !distinctAttempt {
		t.Fatalf("enqueue outcomes first=%t duplicate=%t secondAttempt=%t, want true/false/true",
			first, duplicate, distinctAttempt)
	}
	length, err := store.StreamLength(ctx, runTaskStream)
	if err != nil {
		t.Fatal(err)
	}
	if length != 2 {
		t.Fatalf("run-task stream length=%d, want 2", length)
	}
	if got := runTaskIdentity("octo/api", task.RunID, task.Attempt); got != "octo/api:4242:1" {
		t.Fatalf("run-task identity=%q", got)
	}
}

func TestQueueReclaimsAbandonedWork(t *testing.T) {
	store, ctx := integrationStore(t)
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, Task{Repository: "octo/api", InstallationID: 7}); err != nil {
		t.Fatal(err)
	}
	leases, err := queue.Lease(ctx, "crashed-worker", 10, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 {
		t.Fatalf("leased %d tasks, want 1", len(leases))
	}
	time.Sleep(50 * time.Millisecond)
	reclaimed, err := queue.Reclaim(ctx, "healthy-worker", 10*time.Millisecond, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 1 || reclaimed[0].Task.Repository != "octo/api" {
		t.Fatalf("expected the abandoned task to be reclaimed, got %+v", reclaimed)
	}
}

func TestRetryTransitionFailureLeavesOriginalPending(t *testing.T) {
	store, ctx := integrationStore(t)
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := queue.Enqueue(ctx, Task{Repository: "octo/api"}); err != nil {
		t.Fatal(err)
	}
	leases, err := queue.Lease(ctx, "worker-a", 1, 0)
	if err != nil || len(leases) != 1 {
		t.Fatalf("lease = %+v, err = %v", leases, err)
	}
	original := store.Client
	store.Client = &failingCommandClient{delegate: original, failEval: true}
	if err := queue.Retry(ctx, leases[0], errors.New("collect failed")); err == nil {
		t.Fatal("expected injected transition failure")
	}
	store.Client = original
	pending, err := queue.Pending(ctx)
	if err != nil || pending != 1 {
		t.Fatalf("pending = %d, err = %v; want original pending", pending, err)
	}
	reclaimed, err := queue.Reclaim(ctx, "worker-b", 0, 1)
	if err != nil || len(reclaimed) != 1 {
		t.Fatalf("reclaimed = %+v, err = %v", reclaimed, err)
	}
}

func TestDeadLetterFailureLeavesOriginalPending(t *testing.T) {
	store, ctx := integrationStore(t)
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store, MaxAttempts: 1}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, Task{Repository: "octo/api"}); err != nil {
		t.Fatal(err)
	}
	leases, err := queue.Lease(ctx, "worker-a", 1, 0)
	if err != nil || len(leases) != 1 {
		t.Fatalf("lease = %+v, err = %v", leases, err)
	}
	original := store.Client
	store.Client = &failingCommandClient{delegate: original, failEval: true}
	if err := queue.Retry(ctx, leases[0], errors.New("permanent")); err == nil {
		t.Fatal("expected injected dead-letter failure")
	}
	store.Client = original
	pending, err := queue.Pending(ctx)
	if err != nil || pending != 1 {
		t.Fatalf("pending = %d, err = %v; want original pending", pending, err)
	}
	dead, err := queue.DeadLetters(ctx)
	if err != nil || dead != 0 {
		t.Fatalf("dead letters = %d, err = %v; want none", dead, err)
	}
}

func TestMalformedTaskMovesToDeadLetterBeforeAck(t *testing.T) {
	store, ctx := integrationStore(t)
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.StreamAdd(ctx, taskStream, 0, map[string]string{
		"repository": "octo/api", "task": "{",
	}); err != nil {
		t.Fatal(err)
	}
	leases, err := queue.Lease(ctx, "worker", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 0 {
		t.Fatalf("leases = %+v, want malformed entry withheld", leases)
	}
	pending, err := queue.Pending(ctx)
	if err != nil || pending != 0 {
		t.Fatalf("pending = %d, err = %v", pending, err)
	}
	dead, err := queue.DeadLetters(ctx)
	if err != nil || dead != 1 {
		t.Fatalf("dead letters = %d, err = %v", dead, err)
	}
}

func TestConcurrentDeliveryAdmissionEnqueuesOnce(t *testing.T) {
	store, ctx := integrationStore(t)
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	const callers = 12
	results := make(chan error, callers)
	var wait sync.WaitGroup
	enqueued := 0
	var resultMu sync.Mutex
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			added, _, err := queue.EnqueueDelivery(ctx, Task{Repository: "octo/api"}, "same-delivery", time.Hour)
			if added {
				resultMu.Lock()
				enqueued++
				resultMu.Unlock()
			}
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if enqueued != 1 {
		t.Fatalf("enqueued = %d, want exactly one", enqueued)
	}
	depth, err := queue.Depth(ctx)
	if err != nil || depth != 1 {
		t.Fatalf("depth = %d, err = %v", depth, err)
	}
}

func TestQueueCapacityBackpressuresWithoutTrimmingPendingWork(t *testing.T) {
	store, ctx := integrationStore(t)
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store, MaxLength: 1}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := queue.Enqueue(ctx, Task{Repository: "octo/first"}); err != nil {
		t.Fatal(err)
	}
	leases, err := queue.Lease(ctx, "crashed", 1, 0)
	if err != nil || len(leases) != 1 {
		t.Fatalf("lease = %+v, err = %v", leases, err)
	}
	if _, err := queue.Enqueue(ctx, Task{Repository: "octo/second"}); !errors.Is(err, redisx.ErrStreamCapacity) {
		t.Fatalf("enqueue error = %v, want capacity backpressure", err)
	}
	reclaimed, err := queue.Reclaim(ctx, "healthy", 0, 1)
	if err != nil || len(reclaimed) != 1 || reclaimed[0].Task.Repository != "octo/first" {
		t.Fatalf("reclaimed = %+v, err = %v", reclaimed, err)
	}
}

func TestQueueCapacityCountsScheduledRetries(t *testing.T) {
	store, ctx := integrationStore(t)
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store, MaxLength: 1}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, Task{Repository: "octo/retry"}); err != nil {
		t.Fatal(err)
	}
	leases, err := queue.Lease(ctx, "worker", 1, 0)
	if err != nil || len(leases) != 1 {
		t.Fatalf("lease = %+v, err = %v", leases, err)
	}
	leases[0].Task.NotBefore = time.Now().UTC().Add(time.Hour)
	if err := queue.Defer(ctx, leases[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, Task{Repository: "octo/overflow"}); !errors.Is(err, redisx.ErrStreamCapacity) {
		t.Fatalf("enqueue error = %v, want capacity backpressure for scheduled task", err)
	}
	if depth, err := queue.Depth(ctx); err != nil || depth != 1 {
		t.Fatalf("depth = %d, err = %v; want delayed task counted in queue depth", depth, err)
	}
}

func TestQueueDeadLettersAfterRepeatedFailures(t *testing.T) {
	store, ctx := integrationStore(t)
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store, MaxAttempts: 2}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := queue.Enqueue(ctx, Task{Repository: "octo/api", InstallationID: 7}); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("collection failed")
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			if _, err := queue.promoteDue(ctx, time.Now().UTC().Add(time.Hour), 10); err != nil {
				t.Fatal(err)
			}
		}
		leases, err := queue.Lease(ctx, "worker", 10, 50*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if len(leases) == 0 {
			break
		}
		if err := queue.Retry(ctx, leases[0], failure); err != nil {
			t.Fatal(err)
		}
	}
	dead, err := queue.DeadLetters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dead != 1 {
		t.Fatalf("dead letters = %d, want 1 after exhausting attempts", dead)
	}
}

func TestDeadLetterRetentionIsBounded(t *testing.T) {
	store, ctx := integrationStore(t)
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store, MaxAttempts: 1, MaxLength: 1}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	for _, repository := range []string{"octo/first", "octo/second"} {
		if _, err := queue.Enqueue(ctx, Task{Repository: repository}); err != nil {
			t.Fatal(err)
		}
		leases, err := queue.Lease(ctx, "worker", 1, 0)
		if err != nil || len(leases) != 1 {
			t.Fatalf("lease = %+v, err = %v", leases, err)
		}
		if err := queue.Retry(ctx, leases[0], errors.New("permanent")); err != nil {
			t.Fatal(err)
		}
	}
	dead, err := queue.DeadLetters(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dead != 1 {
		t.Fatalf("dead letters = %d, want configured retention limit 1", dead)
	}
}
func TestQueueSerializesOneRepository(t *testing.T) {
	store, ctx := integrationStore(t)
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	if err := queue.LockRepository(ctx, "octo/api", "token-a", time.Minute); err != nil {
		t.Fatal(err)
	}
	err := queue.LockRepository(ctx, "octo/api", "token-b", time.Minute)
	if !errors.Is(err, ErrRepositoryBusy) {
		t.Fatalf("expected a busy repository, got %v", err)
	}
	if err := queue.UnlockRepository(ctx, "octo/api", "token-a"); err != nil {
		t.Fatal(err)
	}
	if err := queue.LockRepository(ctx, "octo/api", "token-b", time.Minute); err != nil {
		t.Fatalf("expected the lock to be released: %v", err)
	}
}

func TestWorkerCancellationPreservesNotBeforeTask(t *testing.T) {
	store, ctx := integrationStore(t)
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	notBefore := time.Now().UTC().Add(time.Hour)
	if _, err := queue.Enqueue(ctx, Task{
		Repository: "octo/api", InstallationID: 7, NotBefore: notBefore,
	}); err != nil {
		t.Fatal(err)
	}
	leases, err := queue.Lease(ctx, "worker", 1, 0)
	if err != nil || len(leases) != 1 {
		t.Fatalf("lease = %+v, err = %v; want delayed task", leases, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	worker := Worker{Queue: queue}
	if worker.process(cancelled, leases[0]) {
		t.Fatal("cancelled delayed task should not be processed")
	}
	pending, err := queue.Pending(ctx)
	if err != nil || pending != 0 {
		t.Fatalf("pending = %d, err = %v; expected the original lease to be scheduled", pending, err)
	}
	if depth, err := queue.Depth(ctx); err != nil || depth != 1 {
		t.Fatalf("depth = %d, err = %v; want one durably scheduled task", depth, err)
	}
	promoted, err := queue.promoteDue(ctx, notBefore.Add(time.Millisecond), 1)
	if err != nil || promoted != 1 {
		t.Fatalf("promoted = %d, err = %v; want delayed task to become ready", promoted, err)
	}
	requeued, err := queue.Lease(ctx, "replacement-worker", 1, 0)
	if err != nil || len(requeued) != 1 {
		t.Fatalf("requeued task = %+v, err = %v", requeued, err)
	}
	if !requeued[0].Task.NotBefore.Equal(notBefore) {
		t.Fatalf("notBefore = %s, want %s", requeued[0].Task.NotBefore, notBefore)
	}
}

func TestDelayedTaskDoesNotBlockReadyQueueWork(t *testing.T) {
	store, ctx := integrationStore(t)
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, Task{
		Repository: "octo/delayed", NotBefore: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, Task{Repository: "octo/ready"}); err != nil {
		t.Fatal(err)
	}
	leases, err := queue.Lease(ctx, "worker", 1, 0)
	if err != nil || len(leases) != 1 || leases[0].Task.Repository != "octo/delayed" {
		t.Fatalf("first lease = %+v, err = %v; want delayed task", leases, err)
	}
	worker := Worker{Queue: queue}
	processingCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		worker.process(processingCtx, leases[0])
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("scheduling a delayed task blocked the worker")
	}
	ready, err := queue.Lease(ctx, "ready-worker", 1, 0)
	if err != nil || len(ready) != 1 || ready[0].Task.Repository != "octo/ready" {
		t.Fatalf("ready lease = %+v, err = %v; delayed task blocked ready work", ready, err)
	}
}

func TestAdmitterRefusesRepositoriesOutsideScope(t *testing.T) {
	store, ctx := integrationStore(t)
	enrollment := Enrollment{Metadata: store, Leases: store}
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	admitter := Admitter{Enrollment: enrollment, Queue: queue}
	payload := []byte(`{"action":"completed","installation":{"id":7},
		"repository":{"full_name":"stranger/repo"}}`)
	if _, err := admitter.Admit(ctx, "workflow_run", payload); !errors.Is(err, ErrNotEnrolled) {
		t.Fatalf("expected an unenrolled repository to be refused, got %v", err)
	}
	depth, err := queue.Depth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if depth != 0 {
		t.Fatalf("depth = %d, want 0 for an out-of-scope delivery", depth)
	}
	enroll := []byte(`{"action":"created","installation":{"id":7},
		"repositories":[{"full_name":"octo/api"}]}`)
	if _, err := admitter.Admit(ctx, "installation", enroll); err != nil {
		t.Fatal(err)
	}
	// Enrollment is never inferred by admission. A background discovery must
	// establish coverage before workflow events can collect the repository.
	if _, err := admitter.Admit(ctx, "workflow_run", []byte(`{"action":"completed","installation":{"id":7},"repository":{"full_name":"octo/api"}}`)); !errors.Is(err, ErrNotEnrolled) {
		t.Fatalf("unverified enrollment accepted: %v", err)
	}
	if err := queue.Complete(ctx, refreshLease(t, queue)); err != nil {
		t.Fatal(err)
	}
	if err := enrollment.AddRepositories(ctx, 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	collect := []byte(`{"action":"completed","installation":{"id":7},
		"repository":{"full_name":"octo/api"}}`)
	admission, err := admitter.Admit(ctx, "workflow_run", collect)
	if err != nil {
		t.Fatal(err)
	}
	if !admission.Enqueued || admission.Kind != IntentCollect {
		t.Fatalf("unexpected admission %+v", admission)
	}
}

// Un-enrollment is a withdrawal of consent, so it must erase retained
// evidence rather than merely stop collecting.
func TestAdmitterErasesEvidenceWhenScopeIsWithdrawn(t *testing.T) {
	store, ctx := integrationStore(t)
	lake := Lake{Directory: t.TempDir()}
	if err := lake.Prepare(); err != nil {
		t.Fatal(err)
	}
	enrollment := Enrollment{Metadata: store, Leases: store}
	if err := enrollment.AddRepositories(ctx, 11, []string{"acme/kept", "acme/withdrawn"}); err != nil {
		t.Fatal(err)
	}
	values := NativeValueService{State: store, Leases: store, Enrollment: enrollment}
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	admitter := Admitter{
		Enrollment: enrollment,
		Queue:      queue,
	}
	if _, err := queue.Enqueue(ctx, Task{Repository: "acme/withdrawn", InstallationID: 11}); err != nil {
		t.Fatal(err)
	}
	shards := map[string]string{
		lake.ShardDirectory(): "raw",
		lake.RunsDirectory():  "runs",
	}
	for directory := range shards {
		for _, repository := range []string{"acme/kept", "acme/withdrawn"} {
			name := directory + "/" + lake.ShardPrefix(repository) + "0001.jsonl"
			if err := WriteFileAtomic(name, []byte(`{"request":{"repository":"`+repository+`"}}`+"\n")); err != nil {
				t.Fatal(err)
			}
		}
	}
	payload := []byte(`{"action":"removed","installation":{"id":11},` +
		`"repositories_removed":[{"full_name":"acme/withdrawn"}]}`)
	admission, err := admitter.Admit(ctx, "installation_repositories", payload)
	if err != nil {
		t.Fatal(err)
	}
	if !admission.Enqueued {
		t.Fatal("scope withdrawal was not durably queued")
	}
	worker := Worker{
		Queue: queue, Runner: Runner{Lake: lake}, Enrollment: enrollment, Values: values,
	}
	// The earlier collection is already leased when the queued scope change
	// is processed; it must still recheck enrollment before reading evidence.
	leases, err := queue.Lease(ctx, "erasure-test", 1, 0)
	if err != nil || len(leases) != 1 {
		t.Fatalf("lease = %+v, err = %v; want queued collection task", leases, err)
	}
	scope := refreshLease(t, queue)
	if err := worker.processRefresh(ctx, scope); err != nil {
		t.Fatal(err)
	}
	if !worker.process(ctx, leases[0]) {
		t.Fatal("withdrawn collection task did not erase its retained evidence")
	}
	for directory := range shards {
		withdrawn := directory + "/" + lake.ShardPrefix("acme/withdrawn") + "0001.jsonl"
		if _, err := os.Stat(withdrawn); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("withdrawn evidence survived in %s: %v", directory, err)
		}
		kept := directory + "/" + lake.ShardPrefix("acme/kept") + "0001.jsonl"
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("enrolled evidence was erased from %s: %v", directory, err)
		}
	}
}

// An admission-only process has no evidence lake, so withdrawing consent must
// queue erasure for a worker that does rather than silently retain evidence.
func TestAdmitterQueuesErasureWithoutALake(t *testing.T) {
	store, ctx := integrationStore(t)
	enrollment := Enrollment{Metadata: store, Leases: store}
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := enrollment.AddRepositories(ctx, 12, []string{"acme/withdrawn"}); err != nil {
		t.Fatal(err)
	}
	admitter := Admitter{Enrollment: enrollment, Queue: queue}
	payload := []byte(`{"action":"deleted","installation":{"id":12}}`)
	if _, err := admitter.Admit(ctx, "installation", payload); err != nil {
		t.Fatal(err)
	}
	worker := Worker{Queue: queue, Enrollment: enrollment, Values: NativeValueService{State: store}}
	if err := worker.processRefresh(ctx, refreshLease(t, queue)); err != nil {
		t.Fatal(err)
	}
	leases, err := queue.Lease(ctx, "erasure-test", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 {
		t.Fatalf("leased %d tasks, want 1", len(leases))
	}
	if !leases[0].Task.Erase {
		t.Fatal("queued task does not request erasure")
	}
	if leases[0].Task.Repository != "acme/withdrawn" {
		t.Fatalf("queued erasure for %q", leases[0].Task.Repository)
	}
}

func TestInstallationRemovalSerializesMembershipSnapshot(t *testing.T) {
	store, ctx := integrationStore(t)
	enrollment := Enrollment{Metadata: store, Leases: store}
	if err := enrollment.AddRepositories(ctx, 17, []string{"Acme/first", "acme/second"}); err != nil {
		t.Fatal(err)
	}
	var snapshot []string
	removed, err := enrollment.RemoveInstallationBefore(ctx, 17, func(ctx context.Context, repositories []string) error {
		snapshot = append([]string(nil), repositories...)
		if err := enrollment.AddRepositories(ctx, 18, []string{"acme/racing"}); !errors.Is(err, ErrEnrollmentMutationBusy) {
			t.Fatalf("concurrent enrollment mutation error = %v, want busy", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot) != 2 || snapshot[0] != "acme/first" || snapshot[1] != "acme/second" {
		t.Fatalf("prepared erasure snapshot = %v", snapshot)
	}
	if len(removed) != len(snapshot) || removed[0] != snapshot[0] || removed[1] != snapshot[1] {
		t.Fatalf("removed repositories = %v, want prepared snapshot %v", removed, snapshot)
	}
	if enrolled, err := enrollment.Enrolled(ctx, "acme/racing"); err != nil || enrolled {
		t.Fatalf("repository from blocked mutation enrolled=%t, err=%v", enrolled, err)
	}
}

func TestTransferredRepositoryIgnoresStaleInstallationRemoval(t *testing.T) {
	store, ctx := integrationStore(t)
	enrollment := Enrollment{Metadata: store, Leases: store}
	if err := enrollment.AddRepositories(ctx, 11, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	if err := enrollment.AddRepositories(ctx, 12, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	removed, err := enrollment.RemoveInstallation(ctx, 11)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want no evidence erasure for the stale installation", removed)
	}
	enrolled, err := enrollment.Enrolled(ctx, "octo/api")
	if err != nil {
		t.Fatal(err)
	}
	if !enrolled {
		t.Fatal("expected the repository to stay enrolled under its current installation")
	}
	installation, err := enrollment.InstallationFor(ctx, "octo/api")
	if err != nil {
		t.Fatal(err)
	}
	if installation != 12 {
		t.Fatalf("installation = %d, want 12", installation)
	}
	removed, err = enrollment.RemoveRepositories(ctx, 12, []string{"octo/api"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "octo/api" {
		t.Fatalf("removed = %v, want the repository erased by its current installation", removed)
	}
}

func TestStaleRemovalErasureDoesNotDeleteTransferredEvidence(t *testing.T) {
	store, ctx := integrationStore(t)
	enrollment := Enrollment{Metadata: store, Leases: store}
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := enrollment.AddRepositories(ctx, 11, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	if err := enrollment.AddRepositories(ctx, 12, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	lake := Lake{Directory: t.TempDir()}
	if err := lake.Prepare(); err != nil {
		t.Fatal(err)
	}
	shard := lake.ShardDirectory() + "/" + lake.ShardPrefix("octo/api") + "0001.jsonl"
	if err := WriteFileAtomic(shard, []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	admitter := Admitter{Enrollment: enrollment, Queue: queue}
	payload := []byte(`{"action":"removed","installation":{"id":11},` +
		`"repositories_removed":[{"full_name":"octo/api"}]}`)
	if _, err := admitter.Admit(ctx, "installation_repositories", payload); err != nil {
		t.Fatal(err)
	}
	leases, err := queue.Lease(ctx, "stale-erasure-test", 1, 0)
	if err != nil || len(leases) != 1 {
		t.Fatalf("lease = %+v, err = %v; want stale erasure task", leases, err)
	}
	worker := Worker{
		Queue: queue, Runner: Runner{Lake: lake}, Enrollment: enrollment, Values: NativeValueService{State: store},
	}
	if worker.process(ctx, leases[0]) {
		t.Fatal("stale erasure should not report evidence changed")
	}
	if _, err := os.Stat(shard); err != nil {
		t.Fatalf("transferred repository evidence was erased: %v", err)
	}
}

func TestReplacementPersistsWhenAckCannotComplete(t *testing.T) {
	store, ctx := integrationStore(t)
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, Task{Repository: "octo/api"}); err != nil {
		t.Fatal(err)
	}
	leases, err := queue.Lease(ctx, "worker-a", 1, 0)
	if err != nil || len(leases) != 1 {
		t.Fatalf("lease = %+v, err = %v", leases, err)
	}
	original := store.Client
	store.Client = &ambiguousCommandClient{delegate: original}
	err = queue.Retry(ctx, leases[0], errors.New("collect failed"))
	if err == nil {
		t.Fatal("expected ambiguous connection failure")
	}
	store.Client = original
	pending, err := queue.Pending(ctx)
	if err != nil || pending != 0 {
		t.Fatalf("pending = %d, err = %v; want original acknowledged", pending, err)
	}
	if _, err := queue.promoteDue(ctx, time.Now().UTC().Add(time.Hour), 1); err != nil {
		t.Fatal(err)
	}
	replacement, err := queue.Lease(ctx, "worker-b", 1, 0)
	if err != nil || len(replacement) != 1 {
		t.Fatalf("replacement = %+v, err = %v; want durable duplicate", replacement, err)
	}
}

func TestDeliveryAdmissionFailureDoesNotConsumeDelivery(t *testing.T) {
	store, ctx := integrationStore(t)
	enrollment := Enrollment{Metadata: store, Leases: store}
	if err := enrollment.AddRepositories(ctx, 7, []string{"octo/api"}); err != nil {
		t.Fatal(err)
	}
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	admitter := Admitter{Enrollment: enrollment, Queue: queue}
	payload := []byte(`{"action":"completed","installation":{"id":7},"repository":{"full_name":"octo/api"}}`)
	original := store.Client
	store.Client = &failingCommandClient{delegate: original, failEval: true}
	if _, err := admitter.AdmitDelivery(ctx, "workflow_run", payload, "delivery-1", time.Hour); err == nil {
		t.Fatal("expected injected admission failure")
	}
	store.Client = original
	admission, err := admitter.AdmitDelivery(ctx, "workflow_run", payload, "delivery-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !admission.Enqueued || admission.Duplicate {
		t.Fatalf("retry admission = %+v, want newly enqueued", admission)
	}
}

func TestFinalAckFailureLeavesCompletedTaskReclaimable(t *testing.T) {
	store, ctx := integrationStore(t)
	queue := Queue{Tasks: store, Admission: store, Metadata: store, Leases: store, Deliveries: store, Metrics: store}
	if err := queue.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(ctx, Task{Repository: "octo/api"}); err != nil {
		t.Fatal(err)
	}
	leases, err := queue.Lease(ctx, "worker-a", 1, 0)
	if err != nil || len(leases) != 1 {
		t.Fatalf("lease = %+v, err = %v", leases, err)
	}
	original := store.Client
	store.Client = &failingCommandClient{delegate: original, failEval: true}
	if err := queue.Complete(ctx, leases[0]); err == nil {
		t.Fatal("expected injected final ACK failure")
	}
	store.Client = original
	reclaimed, err := queue.Reclaim(ctx, "worker-b", 0, 1)
	if err != nil || len(reclaimed) != 1 {
		t.Fatalf("reclaimed = %+v, err = %v", reclaimed, err)
	}
	if reclaimed[0].Task.Attempt != leases[0].Task.Attempt {
		t.Fatal("reclaim must not consume a retry attempt")
	}
}
