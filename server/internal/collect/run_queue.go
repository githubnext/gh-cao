package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

const (
	delayedRunTaskSet        = "collect:delayed-run-tasks"
	runDeadLetterStream      = "collect:run-dead-letters"
	runEnumerationStream     = "collect:run-enumerations"
	delayedRunEnumerationSet = "collect:delayed-run-enumerations"
)

func runTaskQueue(task RunTask) string {
	if task.Enumeration != nil {
		return runEnumerationStream
	}
	return runTaskStream
}

func delayedRunQueue(stream string) string {
	if stream == runEnumerationStream {
		return delayedRunEnumerationSet
	}
	return delayedRunTaskSet
}

type RunLease struct {
	Task               RunTask
	MessageID, Receipt string
}

func (q Queue) fencedRunQueue() (operational.FencedTaskQueue, error) {
	queue, ok := q.Tasks.(operational.FencedTaskQueue)
	if !ok {
		return nil, fmt.Errorf("historical run processing requires fenced task receipts: %w", operational.ErrUnsupported)
	}
	return queue, nil
}

func (q Queue) EnsureRuns(ctx context.Context) error {
	queue, err := q.fencedRunQueue()
	if err != nil {
		return err
	}
	for _, stream := range []string{runTaskStream, runEnumerationStream} {
		if err := queue.EnsureQueue(ctx, stream, q.group()); err != nil {
			return err
		}
	}
	return nil
}

func (q Queue) LeaseRuns(ctx context.Context, consumer string, count int) ([]RunLease, error) {
	if _, err := q.fencedRunQueue(); err != nil {
		return nil, err
	}
	var leases []RunLease
	for _, stream := range []string{runTaskStream, runEnumerationStream} {
		if len(leases) >= count {
			break
		}
		items, err := q.leaseRunStream(ctx, consumer, stream, count-len(leases))
		leases = append(leases, items...)
		if err != nil {
			return leases, err
		}
	}
	return leases, nil
}

func (q Queue) leaseRunStream(ctx context.Context, consumer, stream string, count int) ([]RunLease, error) {
	queue, err := q.fencedRunQueue()
	if err != nil {
		return nil, err
	}
	if _, err := queue.PromoteTasks(ctx, delayedRunQueue(stream), stream, time.Now().UTC(), count); err != nil {
		return nil, err
	}
	messages, err := queue.ReadTasks(ctx, operational.QueueRead{
		Queue: stream, Group: q.group(), Consumer: consumer, Count: count,
	})
	if err != nil {
		return nil, err
	}
	return q.runLeases(ctx, queue, messages, stream)
}

func (q Queue) ReclaimRuns(ctx context.Context, consumer string, idle time.Duration, count int) ([]RunLease, error) {
	if _, err := q.fencedRunQueue(); err != nil {
		return nil, err
	}
	var leases []RunLease
	for _, stream := range []string{runTaskStream, runEnumerationStream} {
		if len(leases) >= count {
			break
		}
		items, err := q.reclaimRunStream(ctx, consumer, stream, idle, count-len(leases))
		leases = append(leases, items...)
		if err != nil {
			return leases, err
		}
	}
	return leases, nil
}

func (q Queue) reclaimRunStream(ctx context.Context, consumer, stream string, idle time.Duration, count int) ([]RunLease, error) {
	queue, err := q.fencedRunQueue()
	if err != nil {
		return nil, err
	}
	messages, err := queue.ClaimTasks(ctx, operational.QueueRead{
		Queue: stream, Group: q.group(), Consumer: consumer, Count: count,
	}, idle)
	if err != nil {
		return nil, err
	}
	return q.runLeases(ctx, queue, messages, stream)
}

func validRunTask(task RunTask, fields operational.TaskFields) bool {
	repository, err := NormalizeRepository(task.Repository)
	return err == nil && task.Repository == repository && fields.Repository == repository &&
		task.InstallationID > 0 && (task.Enumeration != nil && validRunEnumeration(task) ||
		task.Enumeration == nil && task.RunID > 0 && task.Attempt > 0) && task.Failures >= 0 && task.ScopeEpoch > 0 &&
		validRunObservation(task.Observation) &&
		task.Key == runTaskKey(task) && fields.Key == task.Key
}

func (q Queue) runLeases(ctx context.Context, queue operational.FencedTaskQueue, messages []operational.TaskMessage, stream string) ([]RunLease, error) {
	result := make([]RunLease, 0, len(messages))
	for _, message := range messages {
		if message.Receipt == "" {
			return result, operational.ErrTaskLeaseLost
		}
		var task RunTask
		if err := json.Unmarshal([]byte(message.Fields.Task), &task); err != nil || !validRunTask(task, message.Fields) || runTaskQueue(task) != stream {
			fields := message.Fields
			fields.Reason = "invalid historical run task"
			fields.RecordedAt = time.Now().UTC().Format(time.RFC3339Nano)
			if err := queue.ReplaceTaskFenced(ctx, operational.Replacement{
				Source: stream, Group: q.group(), ID: message.ID,
				Destination: runDeadLetterStream, Capacity: q.MaxLength, Fields: fields,
			}, message.Receipt); err != nil {
				return result, err
			}
			continue
		}
		result = append(result, RunLease{Task: task, MessageID: message.ID, Receipt: message.Receipt})
	}
	return result, nil
}

func (q Queue) CompleteRun(ctx context.Context, lease RunLease) error {
	queue, err := q.fencedRunQueue()
	if err != nil {
		return err
	}
	if err := queue.CompleteTaskFenced(ctx, runTaskQueue(lease.Task), q.group(), lease.MessageID, lease.Receipt); err != nil {
		return err
	}
	return q.nativeHealth(ctx, true, false)
}

// RetryRun atomically replaces a failed lease, keeping the GitHub attempt
// unchanged. A source receipt cannot complete or reschedule a successor's work.
func (q Queue) RetryRun(ctx context.Context, lease RunLease) error {
	lease.Task.Failures++
	if lease.Task.Failures >= q.maxAttempts() {
		if err := q.replaceRun(ctx, lease, true); err != nil {
			return err
		}
		return q.nativeHealth(ctx, false, true)
	}
	lease.Task.NotBefore = time.Now().UTC().Add(backoff(lease.Task.Failures))
	if err := q.replaceRun(ctx, lease, false); err != nil {
		return err
	}
	return q.nativeHealth(ctx, false, false)
}

// DeferRun does not charge claim contention or an early delivery as a failure.
func (q Queue) DeferRun(ctx context.Context, lease RunLease) error {
	if !lease.Task.NotBefore.After(time.Now().UTC()) {
		lease.Task.NotBefore = time.Now().UTC().Add(backoff(1))
	}
	return q.replaceRun(ctx, lease, false)
}

func (q Queue) replaceRun(ctx context.Context, lease RunLease, dead bool) error {
	queue, err := q.fencedRunQueue()
	if err != nil {
		return err
	}
	if lease.MessageID == "" || lease.Receipt == "" {
		return operational.ErrTaskLeaseLost
	}
	payload, err := json.Marshal(lease.Task)
	if err != nil {
		return err
	}
	fields := operational.TaskFields{Key: lease.Task.Key, Repository: lease.Task.Repository, Task: string(payload)}
	if !validRunTask(lease.Task, fields) {
		return errors.New("invalid historical run replacement")
	}
	replacement := operational.Replacement{
		Source: runTaskQueue(lease.Task), Group: q.group(), ID: lease.MessageID,
		Destination: runTaskQueue(lease.Task), Delayed: delayedRunQueue(runTaskQueue(lease.Task)), Capacity: q.MaxLength, Fields: fields,
	}
	if dead {
		replacement.Destination = runDeadLetterStream
		replacement.Fields.Reason = "historical run processing attempts exhausted"
		replacement.Fields.RecordedAt = time.Now().UTC().Format(time.RFC3339Nano)
	} else if lease.Task.NotBefore.After(time.Now().UTC()) {
		replacement.Due = lease.Task.NotBefore
	}
	return queue.ReplaceTaskFenced(ctx, replacement, lease.Receipt)
}
