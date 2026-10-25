package collect

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

var queueLog = logger.New("cao:collect:queue")

const (
	taskStream       = "collect:tasks"
	runTaskStream    = "collect:run-tasks"
	delayedTaskSet   = "collect:delayed-tasks"
	deadLetterStream = "collect:dead-letters"
	defaultGroup     = "collectors"
)

// Task is one repository's pending collection. Tasks are keyed by repository,
// never by run, so a burst of runs in one repository becomes one collection.
type Task struct {
	Repository     string    `json:"repository"`
	InstallationID int64     `json:"installationId"`
	Reason         string    `json:"reason"`
	EnqueuedAt     time.Time `json:"enqueuedAt"`
	Attempt        int       `json:"attempt"`
	// NotBefore delays a retried task in the durable delayed-task set.
	NotBefore time.Time `json:"notBefore,omitempty"`
	// Erase requests deletion of the repository's retained evidence instead of
	// collection. An admission-only process has no evidence lake, so
	// withdrawing consent is queued for a worker that does.
	Erase            bool         `json:"erase,omitempty"`
	RepositoryID     int64        `json:"repositoryId,omitempty"`
	Lifecycle        string       `json:"lifecycle,omitempty"`
	InventoryRefresh bool         `json:"inventoryRefresh,omitempty"`
	Scope            *ScopeChange `json:"scope,omitempty"`
	Traceparent      string       `json:"traceparent,omitempty"`
}

type ScopeChange struct {
	Kind         IntentKind `json:"kind"`
	Repositories []string   `json:"repositories,omitempty"`
}

func (task Task) debounceKey() string {
	if task.Scope != nil {
		if task.Scope.Kind != IntentEnroll {
			return ""
		}
		return "collect:scope-debounce:" + strconv.FormatInt(task.InstallationID, 10)
	}
	if task.InventoryRefresh {
		return "collect:inventory-debounce:" + task.Repository
	}
	if task.Lifecycle != "" || task.Erase {
		return ""
	}
	return debounceKey(task.Repository)
}

func (task Task) lockKey() string {
	if task.Scope != nil || task.Lifecycle != "" {
		return "scope:" + strconv.FormatInt(task.InstallationID, 10)
	}
	return task.Repository
}

// RunTask is one durable historical workflow-run item. Key is the stable
// repository/run/attempt identity; it is not a claim that run artifacts have
// been parsed or collected.
type RunTask struct {
	Key            string    `json:"key"`
	Repository     string    `json:"repository"`
	InstallationID int64     `json:"installationId"`
	RunID          int64     `json:"runId"`
	Attempt        int       `json:"attempt"`
	CreatedAt      time.Time `json:"createdAt,omitempty"`
	EnqueuedAt     time.Time `json:"enqueuedAt"`
}

// Lease is a task delivered to one consumer and not yet acknowledged.
type Lease struct {
	Task      Task
	MessageID string
}

// Queue is the durable, debounced collection queue.
//
// Admission enqueues; it never projects. Queue depth reports ready and
// scheduled work, while consumer-group claims recover work from a stopped
// worker.
type Queue struct {
	Tasks      operational.TaskQueue
	Admission  operational.DeliveryAdmissionStore
	Metadata   operational.CollectionMetadata
	Leases     operational.LeaseStore
	Deliveries operational.DeliveryDeduplicator
	Metrics    operational.IngestionMetrics
	// Group is the consumer group name; empty selects the default.
	Group string
	// Debounce collapses repeated events for one repository into one task.
	Debounce time.Duration
	// MaxLength applies admission backpressure. Outstanding work is never
	// trimmed to satisfy this limit.
	MaxLength int64
	// MaxAttempts bounds retries before a task is dead-lettered.
	MaxAttempts int
}

func (q Queue) group() string {
	if q.Group == "" {
		return defaultGroup
	}
	return q.Group
}

func (q Queue) debounce() time.Duration {
	if q.Debounce <= 0 {
		return 90 * time.Second
	}
	return q.Debounce
}

func (q Queue) maxAttempts() int {
	if q.MaxAttempts <= 0 {
		return 5
	}
	return q.MaxAttempts
}

// Ensure creates the consumer group, tolerating an existing one.
func (q Queue) Ensure(ctx context.Context) error {
	return q.Tasks.EnsureQueue(ctx, taskStream, q.group())
}

// Enqueue appends a task unless one is already pending for the repository.
// It reports whether a new task was appended; a collapsed event is not an
// error, it is the debounce working.
func (q Queue) Enqueue(ctx context.Context, task Task) (bool, error) {
	return q.enqueue(ctx, task)
}

// EnqueueRun durably admits one historical workflow run. Unlike repository
// collection tasks, run tasks are never coalesced by repository.
func (q Queue) EnqueueRun(ctx context.Context, task RunTask) (bool, error) {
	repository, err := NormalizeRepository(task.Repository)
	if err != nil {
		return false, err
	}
	if task.InstallationID <= 0 || task.RunID <= 0 || task.Attempt <= 0 {
		return false, errors.New("run tasks require an installation, run id, and positive attempt")
	}
	task.Repository = repository
	task.Key = runTaskIdentity(repository, task.RunID, task.Attempt)
	if task.EnqueuedAt.IsZero() {
		task.EnqueuedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(task)
	if err != nil {
		return false, err
	}
	return q.Tasks.EnqueueUniqueTask(
		ctx, runTaskStream, task.Key, q.MaxLength,
		operational.TaskFields{Key: task.Key, Repository: repository, Task: string(payload)},
	)
}

func runTaskIdentity(repository string, runID int64, attempt int) string {
	return repository + ":" + strconv.FormatInt(runID, 10) + ":" + strconv.Itoa(attempt)
}

// EnqueueDelivery atomically deduplicates a GitHub delivery and durably
// admits its collection task.
func (q Queue) EnqueueDelivery(ctx context.Context, task Task, delivery string, deliveryTTL time.Duration) (bool, bool, error) {
	task.Traceparent = refreshTraceparent(ctx)
	repository, payload, err := q.prepareTask(task)
	if err != nil {
		return false, false, err
	}
	task.Repository = repository
	debounce := task.debounceKey()
	result, err := q.Admission.AdmitDelivery(ctx, operational.DeliveryRequest{
		Delivery: delivery, DeliveryTTL: deliveryTTL,
		EnqueueRequest: operational.EnqueueRequest{
			Queue: taskStream, Delayed: delayedTaskSet, Debounce: debounce,
			DebounceTTL: q.debounce(), Capacity: q.MaxLength, Fields: taskFields(repository, payload),
		},
	})
	if err != nil {
		return false, false, err
	}
	switch result {
	case operational.DeliveryDuplicate:
	case operational.DeliveryEnqueued:
		_ = q.Metrics.IncrementIngestionCounter(ctx, "taskQueued")
	case operational.DeliveryCoalesced:
		_ = q.Metrics.IncrementIngestionCounter(ctx, "taskCoalesced")
	}
	return result == operational.DeliveryEnqueued, result == operational.DeliveryDuplicate, nil
}

func (q Queue) enqueue(ctx context.Context, task Task) (bool, error) {
	task.Traceparent = refreshTraceparent(ctx)
	repository, payload, err := q.prepareTask(task)
	if err != nil {
		return false, err
	}
	task.Repository = repository
	debounce := ""
	if task.Attempt == 0 {
		debounce = task.debounceKey()
	}
	enqueued, err := q.Tasks.EnqueueTask(ctx, operational.EnqueueRequest{
		Queue: taskStream, Delayed: delayedTaskSet, Debounce: debounce,
		DebounceTTL: q.debounce(), Capacity: q.MaxLength, Fields: taskFields(repository, payload),
	})
	if err != nil {
		return false, err
	}
	if !enqueued {
		_ = q.Metrics.IncrementIngestionCounter(ctx, "taskCoalesced")
		queueLog.Printf("collapsed duplicate task repository=%s", repository)
		return false, nil
	}
	_ = q.Metrics.IncrementIngestionCounter(ctx, "taskQueued")
	queueLog.Printf("enqueued task repository=%s attempt=%d", repository, task.Attempt)
	return true, nil
}

func (q Queue) prepareTask(task Task) (string, []byte, error) {
	repository, err := NormalizeRepository(task.Repository)
	if err != nil {
		return "", nil, err
	}
	task.Repository = repository
	if task.EnqueuedAt.IsZero() {
		task.EnqueuedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(task)
	if err != nil {
		return "", nil, err
	}
	return repository, payload, nil
}

func taskFields(repository string, payload []byte) operational.TaskFields {
	return operational.TaskFields{Repository: repository, Task: string(payload)}
}

// Lease reads undelivered tasks for one consumer.
func (q Queue) Lease(ctx context.Context, consumer string, count int, block time.Duration) ([]Lease, error) {
	if _, err := q.promoteDue(ctx, time.Now().UTC(), count); err != nil {
		return nil, err
	}
	messages, err := q.Tasks.ReadTasks(ctx, operational.QueueRead{
		Queue: taskStream, Group: q.group(), Consumer: consumer, Count: count, Block: block,
	})
	if err != nil {
		return nil, err
	}
	return q.leases(ctx, messages)
}

// Reclaim takes over tasks abandoned by a consumer that stopped, so a worker
// crash does not strand a repository.
func (q Queue) Reclaim(ctx context.Context, consumer string, minIdle time.Duration, count int) ([]Lease, error) {
	messages, err := q.Tasks.ClaimTasks(ctx, operational.QueueRead{
		Queue: taskStream, Group: q.group(), Consumer: consumer, Count: count,
	}, minIdle)
	if err != nil {
		return nil, err
	}
	leases, err := q.leases(ctx, messages)
	if len(leases) > 0 {
		queueLog.Printf("reclaimed tasks count=%d", len(leases))
	}
	return leases, err
}

func (q Queue) leases(ctx context.Context, messages []operational.TaskMessage) ([]Lease, error) {
	leases := make([]Lease, 0, len(messages))
	for _, message := range messages {
		var task Task
		if err := json.Unmarshal([]byte(message.Fields.Task), &task); err != nil {
			if err := q.deadLetterLease(ctx, message.ID, Task{Repository: message.Fields.Repository}, "unparseable task entry"); err != nil {
				return leases, err
			}
			continue
		}
		leases = append(leases, Lease{Task: task, MessageID: message.ID})
	}
	return leases, nil
}

// Admit clears the debounce marker so events arriving during collection
// enqueue a follow-up task rather than being silently collapsed into a
// collection that already started.
func (q Queue) Admit(ctx context.Context, task Task) error {
	key := task.debounceKey()
	if key == "" {
		return nil
	}
	return q.Metadata.Clear(ctx, key)
}

// Defer returns a task that is not yet due, without counting an attempt.
func (q Queue) Defer(ctx context.Context, lease Lease) error {
	task := lease.Task
	task.Attempt = max(task.Attempt, 1)
	return q.replace(ctx, lease.MessageID, task)
}

// Complete acknowledges a finished task.
func (q Queue) Complete(ctx context.Context, lease Lease) error {
	if err := q.Tasks.CompleteTask(ctx, taskStream, q.group(), lease.MessageID); err != nil {
		return err
	}
	_ = q.Metrics.IncrementIngestionCounter(ctx, "collectionSucceeded")
	_ = q.Metrics.RecordIngestionHealthEvent(ctx, "success", "", time.Now().UTC())
	return nil
}

// retryDecision is the pure outcome of applying bounded retry counting to one
// failed task: either rescheduled with a backoff-delayed NotBefore, or
// dead-lettered with a recorded reason once attempts are exhausted.
type retryDecision struct {
	task       Task
	deadLetter bool
	reason     string
}

// decideRetry increments a failed task's attempt count and decides whether it
// is rescheduled or dead-lettered. It is a pure function, so the
// exhausted-attempts boundary and the default dead-letter reason are testable
// without a backing store or the jittered backoff clock.
func decideRetry(task Task, cause error, maxAttempts int, now time.Time, backoffFor func(int) time.Duration) retryDecision {
	task.Attempt++
	if task.Attempt >= maxAttempts {
		reason := "unknown failure"
		if cause != nil {
			reason = cause.Error()
		}
		return retryDecision{task: task, deadLetter: true, reason: reason}
	}
	task.NotBefore = now.Add(backoffFor(task.Attempt))
	return retryDecision{task: task}
}

// Retry acknowledges a failed task and re-enqueues it with bounded attempts
// and jittered backoff, dead-lettering it when the attempts are exhausted. A
// failed task never partially replaces a repository's existing evidence.
func (q Queue) Retry(ctx context.Context, lease Lease, cause error) error {
	now := time.Now().UTC()
	decision := decideRetry(lease.Task, cause, q.maxAttempts(), now, backoff)
	if decision.deadLetter {
		queueLog.Printf("dead-lettering task repository=%s attempts=%d", decision.task.Repository, decision.task.Attempt)
		if err := q.deadLetterLease(ctx, lease.MessageID, decision.task, decision.reason); err != nil {
			return err
		}
		_ = q.Metrics.IncrementIngestionCounter(ctx, "collectionFailed")
		_ = q.Metrics.IncrementIngestionCounter(ctx, "collectionDeadLettered")
		_ = q.Metrics.RecordIngestionHealthEvent(ctx, "failure", "collection", now)
		return nil
	}
	queueLog.Printf("rescheduling task repository=%s attempt=%d", decision.task.Repository, decision.task.Attempt)
	if err := q.replace(ctx, lease.MessageID, decision.task); err != nil {
		return err
	}
	_ = q.Metrics.IncrementIngestionCounter(ctx, "collectionFailed")
	_ = q.Metrics.IncrementIngestionCounter(ctx, "collectionRetried")
	_ = q.Metrics.RecordIngestionHealthEvent(ctx, "failure", "collection", now)
	return nil
}

// RequeueBlocked returns a task to the queue without counting an attempt. It
// is used when another worker holds the repository lease, so per-repository
// exclusion never consumes the retry budget.
func (q Queue) RequeueBlocked(ctx context.Context, lease Lease) error {
	task := lease.Task
	task.Attempt = max(task.Attempt, 1)
	return q.replace(ctx, lease.MessageID, task)
}

func (q Queue) replace(ctx context.Context, messageID string, task Task) error {
	payload, err := json.Marshal(task)
	if err != nil {
		return err
	}
	replacement := operational.Replacement{
		Source: taskStream, Group: q.group(), ID: messageID,
		Destination: taskStream, Delayed: delayedTaskSet, Fields: taskFields(task.Repository, payload),
	}
	if task.NotBefore.After(time.Now().UTC()) {
		replacement.Due = task.NotBefore
	}
	return q.Tasks.ReplaceTask(ctx, replacement)
}

func (q Queue) promoteDue(ctx context.Context, now time.Time, count int) (int64, error) {
	return q.Tasks.PromoteTasks(ctx, delayedTaskSet, taskStream, now, count)
}

func (q Queue) deadLetterLease(ctx context.Context, messageID string, task Task, reason string) error {
	payload, err := json.Marshal(task)
	if err != nil {
		return err
	}
	return q.Tasks.ReplaceTask(ctx, operational.Replacement{
		Source: taskStream, Group: q.group(), ID: messageID,
		Destination: deadLetterStream, Capacity: q.MaxLength,
		Fields: operational.TaskFields{
			Repository: task.Repository, Task: string(payload),
			Reason: reason, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano),
		},
	})
}

// Depth reports ready and scheduled work, excluding already leased tasks.
func (q Queue) Depth(ctx context.Context) (int64, error) {
	stats, err := q.Tasks.QueueStats(ctx, taskStream, q.group())
	if err != nil {
		return 0, err
	}
	delayed, err := q.Tasks.DelayedDepth(ctx, delayedTaskSet)
	if err != nil {
		return 0, err
	}
	return stats.Backlog + delayed, nil
}

// Pending reports delivered but unacknowledged tasks, which is processing lag.
func (q Queue) Pending(ctx context.Context) (int64, error) {
	stats, err := q.Tasks.QueueStats(ctx, taskStream, q.group())
	return stats.Pending, err
}

func (q Queue) OldestPendingAge(ctx context.Context) (time.Duration, error) {
	stats, err := q.Tasks.QueueStats(ctx, taskStream, q.group())
	return stats.OldestPendingAge, err
}

// DeadLetters reports how many tasks exhausted their retries.
func (q Queue) DeadLetters(ctx context.Context) (int64, error) {
	stats, err := q.Tasks.QueueStats(ctx, deadLetterStream, "")
	return stats.Length, err
}

// ErrRepositoryBusy reports that another worker holds a repository's lease.
var ErrRepositoryBusy = errors.New("repository collection is already running")

// LockRepository takes the per-repository lease. Exclusion is per repository,
// so unrelated repositories proceed in parallel.
func (q Queue) LockRepository(ctx context.Context, repository, token string, ttl time.Duration) error {
	acquired, err := q.Leases.TryLock(ctx, repositoryLockName(repository), token, ttl)
	if err != nil {
		return err
	}
	if !acquired {
		return ErrRepositoryBusy
	}
	return nil
}

// UnlockRepository releases the per-repository lease.
func (q Queue) UnlockRepository(ctx context.Context, repository, token string) error {
	return q.Leases.Unlock(ctx, repositoryLockName(repository), token)
}

func repositoryLockName(repository string) string {
	return "collect:repository:" + repository
}

func debounceKey(repository string) string {
	return "collect:debounce:" + repository
}

func backoff(attempt int) time.Duration {
	base := time.Duration(1<<min(attempt, 6)) * time.Second
	// #nosec G404 -- jitter only de-synchronizes retries; it is not a secret.
	jitter := time.Duration(rand.Int64N(int64(base / 2)))
	return base + jitter
}
