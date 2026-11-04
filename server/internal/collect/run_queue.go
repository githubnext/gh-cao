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
	delayedRunTaskSet   = "collect:delayed-run-tasks"
	runDeadLetterStream = "collect:run-dead-letters"
)

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
	return queue.EnsureQueue(ctx, runTaskStream, q.group())
}

func (q Queue) LeaseRuns(ctx context.Context, consumer string, count int) ([]RunLease, error) {
	queue, err := q.fencedRunQueue()
	if err != nil {
		return nil, err
	}
	if _, err := queue.PromoteTasks(ctx, delayedRunTaskSet, runTaskStream, time.Now().UTC(), count); err != nil {
		return nil, err
	}
	messages, err := queue.ReadTasks(ctx, operational.QueueRead{
		Queue: runTaskStream, Group: q.group(), Consumer: consumer, Count: count,
	})
	if err != nil {
		return nil, err
	}
	return q.runLeases(ctx, queue, messages)
}

func (q Queue) ReclaimRuns(ctx context.Context, consumer string, idle time.Duration, count int) ([]RunLease, error) {
	queue, err := q.fencedRunQueue()
	if err != nil {
		return nil, err
	}
	messages, err := queue.ClaimTasks(ctx, operational.QueueRead{
		Queue: runTaskStream, Group: q.group(), Consumer: consumer, Count: count,
	}, idle)
	if err != nil {
		return nil, err
	}
	return q.runLeases(ctx, queue, messages)
}

func validRunTask(task RunTask, fields operational.TaskFields) bool {
	repository, err := NormalizeRepository(task.Repository)
	return err == nil && task.Repository == repository && fields.Repository == repository &&
		task.InstallationID > 0 && task.RunID > 0 && task.Attempt > 0 && task.Failures >= 0 &&
		task.Key == runTaskIdentity(repository, task.RunID, task.Attempt) && fields.Key == task.Key
}

func (q Queue) runLeases(ctx context.Context, queue operational.FencedTaskQueue, messages []operational.TaskMessage) ([]RunLease, error) {
	result := make([]RunLease, 0, len(messages))
	for _, message := range messages {
		if message.Receipt == "" {
			return result, operational.ErrTaskLeaseLost
		}
		var task RunTask
		if err := json.Unmarshal([]byte(message.Fields.Task), &task); err != nil || !validRunTask(task, message.Fields) {
			fields := message.Fields
			fields.Reason = "invalid historical run task"
			fields.RecordedAt = time.Now().UTC().Format(time.RFC3339Nano)
			if err := queue.ReplaceTaskFenced(ctx, operational.Replacement{
				Source: runTaskStream, Group: q.group(), ID: message.ID,
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
	return queue.CompleteTaskFenced(ctx, runTaskStream, q.group(), lease.MessageID, lease.Receipt)
}

// RetryRun atomically replaces a failed lease, keeping the GitHub attempt
// unchanged. A source receipt cannot complete or reschedule a successor's work.
func (q Queue) RetryRun(ctx context.Context, lease RunLease) error {
	lease.Task.Failures++
	if lease.Task.Failures >= q.maxAttempts() {
		return q.replaceRun(ctx, lease, true)
	}
	lease.Task.NotBefore = time.Now().UTC().Add(backoff(lease.Task.Failures))
	return q.replaceRun(ctx, lease, false)
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
		Source: runTaskStream, Group: q.group(), ID: lease.MessageID,
		Destination: runTaskStream, Delayed: delayedRunTaskSet, Capacity: q.MaxLength, Fields: fields,
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
