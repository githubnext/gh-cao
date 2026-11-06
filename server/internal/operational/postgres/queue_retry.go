package postgres

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

var queueLog = logger.New("cao:operational:postgres:queue")

type dueTask struct {
	record record
	task   task
}

// roundUpMillis normalizes a due time to UTC at millisecond precision,
// rounding up so a task is never promoted before its requested time.
func roundUpMillis(due time.Time) time.Time {
	rounded := due.UTC().Truncate(time.Millisecond)
	if rounded.Before(due) {
		rounded = rounded.Add(time.Millisecond)
	}
	return rounded
}

// selectDue returns up to count scheduled tasks for the delayed queue that are
// due at or before at, ordered by due time and then queue position.
func selectDue(records []record, delayed string, at time.Time, count int) ([]dueTask, error) {
	cutoff := at.Truncate(time.Millisecond)
	var due []dueTask
	for _, r := range records {
		var data task
		if err := json.Unmarshal(r.value, &data); err != nil {
			return nil, err
		}
		if data.Delayed == delayed && !data.Due.After(cutoff) {
			due = append(due, dueTask{r, data})
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].task.Due.Equal(due[j].task.Due) {
			return due[i].record.position < due[j].record.position
		}
		return due[i].task.Due.Before(due[j].task.Due)
	})
	return due[:min(count, len(due))], nil
}

func (s *Store) ReplaceTask(ctx context.Context, r operational.Replacement) error {
	return s.replaceTask(ctx, r, "")
}

func (s *Store) ReplaceTaskFenced(ctx context.Context, r operational.Replacement, receipt string) error {
	if receipt == "" {
		return operational.ErrTaskLeaseLost
	}
	return s.replaceTask(ctx, r, receipt)
}

func (s *Store) replaceTask(ctx context.Context, r operational.Replacement, receipt string) error {
	if r.Destination == "" && !r.Due.IsZero() {
		r.Destination = r.Source
	}
	if err := names(r.Source, r.Group, r.ID, r.Destination); err != nil {
		return err
	}
	if err := fieldsValid(r.Fields); err != nil {
		return err
	}
	if r.Capacity < 0 {
		return invalid("negative replacement capacity")
	}
	if !r.Due.IsZero() {
		if err := names(r.Delayed); err != nil {
			return err
		}
		if r.Delayed == r.Destination {
			return invalid("ready and delayed queues must differ")
		}
	}
	return s.transact(ctx, func(t *transaction) error {
		if receipt != "" {
			if err := t.checkTaskReceipt(r.Source, r.Group, r.ID, receipt); err != nil {
				return err
			}
		}
		value, err := t.get(key{"lease", leaseName(r.Source, r.Group), r.ID})
		if err != nil || value == nil {
			return err
		}
		count, err := t.outstanding(r.Destination)
		if err != nil {
			return err
		}
		if r.Source == r.Destination {
			count--
		}
		if r.Capacity > 0 && count >= r.Capacity {
			return operational.ErrCapacity
		}
		// Removing first releases accounting; rollback preserves the original
		// task and every lease if the replacement cannot be admitted.
		if err := t.complete(r.Source, r.ID); err != nil {
			return err
		}
		next := task{Fields: r.Fields, Created: t.now}
		if !r.Due.IsZero() {
			next.Delayed = r.Delayed
			next.Due = roundUpMillis(r.Due)
		}
		return t.addTask(r.Destination, next)
	})
}

func (s *Store) PromoteTasks(ctx context.Context, delayed, queue string, at time.Time, count int) (int64, error) {
	if err := names(delayed, queue); err != nil {
		return 0, err
	}
	if count <= 0 || count > 10000 || at.IsZero() || delayed == queue {
		return 0, invalid("invalid promotion count, time, or names")
	}
	var promoted int64
	err := s.transact(ctx, func(t *transaction) error {
		records, err := t.list("scheduled", queue)
		if err != nil {
			return err
		}
		due, err := selectDue(records, delayed, at, count)
		if err != nil {
			return err
		}
		for _, c := range due {
			if err := t.remove(c.record.key); err != nil {
				return err
			}
			if err := t.addTask(queue, task{Fields: c.task.Fields, Created: t.now}); err != nil {
				return err
			}
			promoted++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	queueLog.Printf("promoted delayed tasks promoted=%d limit=%d", promoted, count)
	return promoted, nil
}

func (s *Store) DelayedDepth(ctx context.Context, delayed string) (int64, error) {
	if err := names(delayed); err != nil {
		return 0, err
	}
	var depth int64
	err := s.transact(ctx, func(t *transaction) error {
		return t.tx.QueryRowContext(ctx, `SELECT count(*) FROM cao_operational_records WHERE namespace=$1 AND kind='scheduled' AND convert_from(value,'UTF8')::jsonb->>'Delayed'=$2`, s.namespace, delayed).Scan(&depth)
	})
	return depth, err
}

func (s *Store) QueueStats(ctx context.Context, queue, group string) (operational.QueueStats, error) {
	if err := names(queue); err != nil {
		return operational.QueueStats{}, err
	}
	if group != "" {
		if err := names(group); err != nil {
			return operational.QueueStats{}, err
		}
	}
	var stats operational.QueueStats
	err := s.transact(ctx, func(t *transaction) error {
		var err error
		stats.Length, err = t.count("task", queue)
		if err != nil || group == "" {
			return err
		}
		var cursor int64
		if _, err := t.json(key{"group", queue, group}, &cursor); err != nil {
			return err
		}
		if err := t.tx.QueryRowContext(ctx, `SELECT count(*) FROM cao_operational_records WHERE namespace=$1 AND kind='task' AND name=$2 AND position>$3`, s.namespace, queue, cursor).Scan(&stats.Backlog); err != nil {
			return err
		}
		leases, err := t.list("lease", leaseName(queue, group))
		if err != nil {
			return err
		}
		stats.Pending = int64(len(leases))
		for _, r := range leases {
			var data task
			if found, err := t.json(key{"task", queue, r.field}, &data); err != nil {
				return err
			} else if found {
				stats.OldestPendingAge = max(stats.OldestPendingAge, t.now.Sub(data.Created))
			}
		}
		return nil
	})
	return stats, err
}
