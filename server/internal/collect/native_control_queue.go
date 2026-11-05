package collect

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

const (
	nativeControlStream  = "collect:native-control"
	nativeControlDelayed = "collect:native-control-delayed"
	nativeControlDead    = "collect:native-control-dead"
)

func validNativeControl(task Task) bool {
	name, err := NormalizeRepository(task.Repository)
	if err != nil || name != task.Repository || task.InstallationID <= 0 || task.Attempt < 0 || task.Erase {
		return false
	}
	if task.Scope != nil {
		return isScopeIntent(task.Scope.Kind) && task.Repository == "installation/"+strconv.FormatInt(task.InstallationID, 10) &&
			task.ScopeEpoch == 0 && task.Lifecycle == "" && !task.InventoryRefresh && !task.ValueRefresh &&
			task.Outcome == nil && len(task.Scope.Repositories) == 0
	}
	if task.Lifecycle != "" {
		return (task.Lifecycle == "active" || task.Lifecycle == "archived" || task.Lifecycle == "deleted") && task.ScopeEpoch >= 0 &&
			!task.InventoryRefresh && !task.ValueRefresh && task.Outcome == nil
	}
	if task.Outcome != nil && (task.Outcome.Validate() != nil || task.Outcome.Repository != task.Repository) {
		return false
	}
	return task.ScopeEpoch > 0 && (task.InventoryRefresh || task.ValueRefresh || task.Outcome != nil)
}

func (q Queue) enqueueNativeControl(ctx context.Context, task Task, kind IntentKind, delivery string, ttl time.Duration) (Admission, error) {
	if !validNativeControl(task) {
		return Admission{}, collectnative.ErrIdentity
	}
	if _, err := q.fencedRunQueue(); err != nil {
		return Admission{}, err
	}
	task.Traceparent = refreshTraceparent(ctx)
	name, content, err := q.prepareTask(task)
	if err != nil {
		return Admission{}, err
	}
	request := operational.EnqueueRequest{Queue: nativeControlStream, Delayed: nativeControlDelayed,
		Capacity: q.MaxLength, Fields: taskFields(name, content)}
	if delivery == "" {
		enqueued, err := q.Tasks.EnqueueTask(ctx, request)
		return Admission{Kind: kind, Enqueued: enqueued}, err
	}
	if q.Admission == nil {
		return Admission{}, collectnative.ErrUnsupported
	}
	result, err := q.Admission.AdmitDelivery(ctx, operational.DeliveryRequest{
		EnqueueRequest: request, Delivery: delivery, DeliveryTTL: ttl,
	})
	if err == nil && result != operational.DeliveryEnqueued && result != operational.DeliveryDuplicate {
		return Admission{}, errors.New("unexpected native control admission result")
	}
	return Admission{Kind: kind, Enqueued: result == operational.DeliveryEnqueued,
		Duplicate: result == operational.DeliveryDuplicate}, err
}

func (q Queue) nativeControlLease(ctx context.Context, consumer string, idle time.Duration) ([]Lease, error) {
	queue, err := q.fencedRunQueue()
	if err != nil {
		return nil, err
	}
	if err := queue.EnsureQueue(ctx, nativeControlStream, q.group()); err != nil {
		return nil, err
	}
	if _, err := queue.PromoteTasks(ctx, nativeControlDelayed, nativeControlStream, time.Now().UTC(), 1); err != nil {
		return nil, err
	}
	request := operational.QueueRead{Queue: nativeControlStream, Group: q.group(), Consumer: consumer, Count: 1}
	messages, err := queue.ClaimTasks(ctx, request, idle)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		messages, err = queue.ReadTasks(ctx, request)
	}
	if err != nil {
		return nil, err
	}
	var result []Lease
	for _, message := range messages {
		if message.Receipt == "" {
			return nil, operational.ErrTaskLeaseLost
		}
		var task Task
		if err := json.Unmarshal([]byte(message.Fields.Task), &task); err != nil ||
			!validNativeControl(task) || task.EnqueuedAt.IsZero() || task.Repository != message.Fields.Repository {
			fields := message.Fields
			fields.Reason, fields.RecordedAt = "invalid native control task", time.Now().UTC().Format(time.RFC3339Nano)
			if err := queue.ReplaceTaskFenced(ctx, operational.Replacement{
				Source: nativeControlStream, Group: q.group(), ID: message.ID, Destination: nativeControlDead,
				Capacity: q.MaxLength, Fields: fields,
			}, message.Receipt); err != nil {
				return nil, err
			}
			continue
		}
		result = append(result, Lease{Task: task, MessageID: message.ID, Receipt: message.Receipt})
	}
	return result, nil
}

func (q Queue) completeNativeControl(ctx context.Context, lease Lease) error {
	queue, err := q.fencedRunQueue()
	if err != nil {
		return err
	}
	if err := queue.CompleteTaskFenced(ctx, nativeControlStream, q.group(), lease.MessageID, lease.Receipt); err != nil {
		return err
	}
	return q.nativeHealth(ctx, true, false)
}

func (q Queue) retryNativeControl(ctx context.Context, lease Lease, failure bool) error {
	queue, err := q.fencedRunQueue()
	if err != nil {
		return err
	}
	if failure {
		lease.Task.Attempt++
	}
	if !validNativeControl(lease.Task) {
		return errors.New("invalid native control replacement")
	}
	content, err := json.Marshal(lease.Task)
	if err != nil {
		return err
	}
	replacement := operational.Replacement{Source: nativeControlStream, Group: q.group(), ID: lease.MessageID,
		Destination: nativeControlStream, Delayed: nativeControlDelayed, Capacity: q.MaxLength,
		Fields: taskFields(lease.Task.Repository, content)}
	if lease.Task.Attempt >= q.maxAttempts() {
		replacement.Destination = nativeControlDead
		replacement.Fields.Reason, replacement.Fields.RecordedAt = "native control processing attempts exhausted", time.Now().UTC().Format(time.RFC3339Nano)
	} else {
		replacement.Due = lease.Task.NotBefore
		if !replacement.Due.After(time.Now().UTC()) {
			replacement.Due = time.Now().UTC().Add(backoff(max(lease.Task.Attempt, 1)))
		}
	}
	if err := queue.ReplaceTaskFenced(ctx, replacement, lease.Receipt); err != nil {
		return err
	}
	if failure {
		return q.nativeHealth(ctx, false, replacement.Destination == nativeControlDead)
	}
	return nil
}
