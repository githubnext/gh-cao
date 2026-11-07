package postgres

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

type task struct {
	Fields  operational.TaskFields
	Created time.Time
	Delayed string
	Due     time.Time
}

func fieldsValid(f operational.TaskFields) error {
	size := len(f.Repository) + len(f.Task) + len(f.Key) + len(f.Reason) + len(f.RecordedAt)
	if size == 0 || size > 1<<20 {
		return invalid("empty or oversized task envelope")
	}
	return nil
}

func enqueueInput(r operational.EnqueueRequest) error {
	if err := names(r.Queue); err != nil {
		return err
	}
	if r.Delayed != "" {
		if err := names(r.Delayed); err != nil {
			return err
		}
		if r.Delayed == r.Queue {
			return invalid("ready and delayed queues must differ")
		}
	}
	if r.Debounce != "" {
		if err := names(r.Debounce); err != nil {
			return err
		}
		if err := ttlValid(r.DebounceTTL); err != nil {
			return err
		}
	}
	if r.Capacity < 0 {
		return invalid("negative queue capacity")
	}
	return fieldsValid(r.Fields)
}

func (s *Store) EnsureQueue(ctx context.Context, queue, group string) error {
	if err := names(queue, group); err != nil {
		return err
	}
	return s.transact(ctx, func(t *transaction) error {
		k := key{"group", queue, group}
		v, err := t.get(k)
		if err != nil || v != nil {
			return err
		}
		return t.putJSON(k, int64(0), time.Time{})
	})
}

func (t *transaction) outstanding(queue string) (int64, error) {
	var count int64
	err := t.tx.QueryRowContext(t.ctx, `SELECT count(*) FROM cao_operational_records WHERE namespace=$1 AND kind IN ('task','scheduled') AND name=$2`, t.namespace, queue).Scan(&count)
	return count, err
}

func (t *transaction) addTask(queue string, data task) error {
	var position int64
	if err := t.tx.QueryRowContext(t.ctx, `SELECT nextval(pg_get_serial_sequence('cao_operational_records','position'))`).Scan(&position); err != nil {
		return err
	}
	id := fmt.Sprintf("%d-%d", t.now.UnixMilli(), position)
	kind := "task"
	if data.Delayed != "" {
		kind = "scheduled"
	}
	return t.putJSON(key{kind, queue, id}, data, time.Time{})
}

func (t *transaction) enqueue(r operational.EnqueueRequest) error {
	count, err := t.outstanding(r.Queue)
	if err != nil {
		return err
	}
	if r.Capacity > 0 && count >= r.Capacity {
		return operational.ErrCapacity
	}
	if err := t.addTask(r.Queue, task{Fields: r.Fields, Created: t.now}); err != nil {
		return err
	}
	if r.Debounce != "" {
		return t.put(key{"debounce", r.Debounce, ""}, []byte{1}, t.now.Add(r.DebounceTTL))
	}
	return nil
}

func (s *Store) EnqueueTask(ctx context.Context, r operational.EnqueueRequest) (bool, error) {
	if err := enqueueInput(r); err != nil {
		return false, err
	}
	enqueued := false
	err := s.transact(ctx, func(t *transaction) error {
		if r.Debounce != "" {
			value, err := t.get(key{"debounce", r.Debounce, ""})
			if err != nil || value != nil {
				return err
			}
		}
		if err := t.enqueue(r); err != nil {
			return err
		}
		enqueued = true
		return nil
	})
	return enqueued && err == nil, err
}

func (s *Store) EnqueueUniqueTask(ctx context.Context, queue, identity string, capacity int64, fields operational.TaskFields) (bool, error) {
	r := operational.EnqueueRequest{Queue: queue, Capacity: capacity, Fields: fields}
	if err := enqueueInput(r); err != nil {
		return false, err
	}
	if err := names(identity); err != nil {
		return false, err
	}
	enqueued := false
	err := s.transact(ctx, func(t *transaction) error {
		k := key{"identity", queue, identity}
		value, err := t.get(k)
		if err != nil || value != nil {
			return err
		}
		if err := t.enqueue(r); err != nil {
			return err
		}
		if err := t.put(k, []byte{1}, time.Time{}); err != nil {
			return err
		}
		enqueued = true
		return nil
	})
	return enqueued && err == nil, err
}

func (s *Store) AdmitDelivery(ctx context.Context, r operational.DeliveryRequest) (operational.DeliveryAdmission, error) {
	if err := enqueueInput(r.EnqueueRequest); err != nil {
		return 0, err
	}
	if err := names(r.Delivery); err != nil {
		return 0, err
	}
	if err := ttlValid(r.DeliveryTTL); err != nil {
		return 0, err
	}
	result := operational.DeliveryDuplicate
	err := s.transact(ctx, func(t *transaction) error {
		k := key{"delivery", r.Delivery, ""}
		value, err := t.get(k)
		if err != nil || value != nil {
			return err
		}
		coalesced := false
		if r.Debounce != "" {
			value, err := t.get(key{"debounce", r.Debounce, ""})
			if err != nil {
				return err
			}
			coalesced = value != nil
		}
		if !coalesced {
			if err := t.enqueue(r.EnqueueRequest); err != nil {
				return err
			}
			result = operational.DeliveryEnqueued
		} else {
			result = operational.DeliveryCoalesced
		}
		return t.put(k, []byte{1}, t.now.Add(r.DeliveryTTL))
	})
	if err != nil {
		return operational.DeliveryDuplicate, err
	}
	return result, nil
}

type lease struct {
	Consumer string
	At       time.Time
	Receipt  string
}

func leaseName(queue, group string) string {
	data, _ := json.Marshal([]string{queue, group})
	return string(data)
}

func readInput(r operational.QueueRead) error {
	if err := names(r.Queue, r.Group, r.Consumer); err != nil {
		return err
	}
	if r.Count <= 0 || r.Count > 10000 || r.Block < 0 {
		return invalid("invalid read count or block duration")
	}
	return nil
}

func (t *transaction) read(r operational.QueueRead, idle *time.Duration) ([]operational.TaskMessage, error) {
	groupKey := key{"group", r.Queue, r.Group}
	var cursor int64
	found, err := t.json(groupKey, &cursor)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("queue group not initialized: %w", operational.ErrUnavailable)
	}
	var records []record
	if idle == nil {
		records, err = t.readyTasks(r.Queue, cursor, r.Count)
		if err != nil {
			return nil, err
		}
	} else {
		pending, err := t.list("lease", leaseName(r.Queue, r.Group))
		if err != nil {
			return nil, err
		}
		for _, rec := range pending {
			var l lease
			if err := json.Unmarshal(rec.value, &l); err != nil {
				return nil, err
			}
			if t.now.Sub(l.At) < *idle {
				continue
			}
			value, err := t.get(key{"task", r.Queue, rec.field})
			if err != nil {
				return nil, err
			}
			if value == nil {
				continue
			}
			records = append(records, record{key: key{"task", r.Queue, rec.field}, value: value})
			if len(records) == r.Count {
				break
			}
		}
	}
	result := []operational.TaskMessage{}
	for _, rec := range records {
		var data task
		if err := json.Unmarshal(rec.value, &data); err != nil {
			return nil, err
		}
		receipt := rand.Text()
		if err := t.putJSON(key{"lease", leaseName(r.Queue, r.Group), rec.field},
			lease{Consumer: r.Consumer, At: t.now, Receipt: receipt}, time.Time{}); err != nil {
			return nil, err
		}
		if idle == nil {
			cursor = rec.position
		}
		result = append(result, operational.TaskMessage{ID: rec.field, Fields: data.Fields, Receipt: receipt})
	}
	if idle == nil && len(records) > 0 {
		if err := t.putJSON(groupKey, cursor, time.Time{}); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (t *transaction) readyTasks(queue string, cursor int64, count int) ([]record, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT field,value,position FROM cao_operational_records WHERE namespace=$1 AND kind='task' AND name=$2 AND position>$3 ORDER BY position LIMIT $4`, t.namespace, queue, cursor, count)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var records []record
	for rows.Next() {
		rec := record{key: key{"task", queue, ""}}
		if err := rows.Scan(&rec.field, &rec.value, &rec.position); err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}

func (s *Store) ReadTasks(ctx context.Context, r operational.QueueRead) ([]operational.TaskMessage, error) {
	if err := readInput(r); err != nil {
		return nil, err
	}
	var timeout <-chan time.Time
	if r.Block > 0 {
		timer := time.NewTimer(r.Block)
		defer timer.Stop()
		timeout = timer.C
	}
	for {
		var result []operational.TaskMessage
		err := s.transact(ctx, func(t *transaction) error {
			var err error
			result, err = t.read(r, nil)
			return err
		})
		if err != nil || len(result) > 0 || r.Block == 0 {
			return result, err
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timeout:
			timer.Stop()
			return []operational.TaskMessage{}, nil
		case <-timer.C:
		}
	}
}

func (s *Store) ClaimTasks(ctx context.Context, r operational.QueueRead, idle time.Duration) ([]operational.TaskMessage, error) {
	if err := readInput(r); err != nil {
		return nil, err
	}
	if idle < 0 {
		return nil, invalid("negative claim idle time")
	}
	var result []operational.TaskMessage
	err := s.transact(ctx, func(t *transaction) error {
		var err error
		result, err = t.read(r, &idle)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (t *transaction) complete(queue, id string) error {
	groups, err := t.list("group", queue)
	if err != nil {
		return err
	}
	for _, group := range groups {
		if err := t.remove(key{"lease", leaseName(queue, group.field), id}); err != nil {
			return err
		}
	}
	return t.remove(key{"task", queue, id})
}

func (s *Store) CompleteTask(ctx context.Context, queue, group, id string) error {
	if err := names(queue, group, id); err != nil {
		return err
	}
	return s.transact(ctx, func(t *transaction) error {
		value, err := t.get(key{"lease", leaseName(queue, group), id})
		if err != nil || value == nil {
			return err
		}
		return t.complete(queue, id)
	})
}

func (t *transaction) checkTaskReceipt(queue, group, id, receipt string) error {
	if receipt == "" {
		return operational.ErrTaskLeaseLost
	}
	var current lease
	found, err := t.json(key{"lease", leaseName(queue, group), id}, &current)
	if err != nil {
		return err
	}
	if !found || current.Receipt != receipt {
		return operational.ErrTaskLeaseLost
	}
	return nil
}

func (s *Store) CompleteTaskFenced(ctx context.Context, queue, group, id, receipt string) error {
	if err := names(queue, group, id); err != nil {
		return err
	}
	return s.transact(ctx, func(t *transaction) error {
		if err := t.checkTaskReceipt(queue, group, id, receipt); err != nil {
			return err
		}
		return t.complete(queue, id)
	})
}
