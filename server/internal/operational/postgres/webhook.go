package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
	wg "github.com/githubnext/gh-cao/server/internal/webhookgateway"
)

type webhookQueue struct {
	store   *Store
	name    string
	options wg.QueueOptions
}

var _ wg.Queue = (*webhookQueue)(nil)

// HasDelivery reads durable polling progress without changing admission state.
func (q *webhookQueue) HasDelivery(ctx context.Context, deliveryID string) (bool, error) {
	if ctx == nil || names(deliveryID) != nil {
		return false, wg.ErrInvalid
	}
	var exists bool
	err := q.store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cao_operational_webhook_deliveries WHERE namespace=$1 AND queue=$2 AND delivery_id=$3 AND retain_until>clock_timestamp())`, q.store.namespace, q.name, deliveryID).Scan(&exists)
	return exists, err
}

// Counts reports retained states without exposing job documents or identities.
func (q *webhookQueue) Counts(ctx context.Context) (map[wg.JobState]int64, error) {
	if ctx == nil {
		return nil, wg.ErrInvalid
	}
	rows, err := q.store.db.QueryContext(ctx, `SELECT state,count(*) FROM cao_operational_webhook_jobs WHERE namespace=$1 AND queue=$2 GROUP BY state`, q.store.namespace, q.name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	counts := make(map[wg.JobState]int64)
	for rows.Next() {
		var state wg.JobState
		var count int64
		if err := rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		counts[state] = count
	}
	return counts, rows.Err()
}

func (q *webhookQueue) QueueDepth(ctx context.Context) (int64, error) {
	if ctx == nil {
		return 0, wg.ErrInvalid
	}
	var depth int64
	err := q.store.db.QueryRowContext(ctx, `SELECT count(*) FROM cao_operational_webhook_jobs WHERE namespace=$1 AND queue=$2 AND state IN ('ready','claimed','dispatching')`,
		q.store.namespace, q.name).Scan(&depth)
	return depth, err
}

func (s *Store) OpenWebhookQueue(ctx context.Context, name string, options wg.QueueOptions) (wg.Queue, error) {
	if err := names(name); err != nil {
		return nil, wg.ErrInvalid
	}
	if err := wg.ValidateQueueOptions(options); err != nil {
		return nil, err
	}
	config, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	err = s.transact(ctx, func(t *transaction) error {
		var previous []byte
		err := t.tx.QueryRowContext(ctx, `SELECT config FROM cao_operational_webhook_queues WHERE namespace=$1 AND name=$2`, t.namespace, name).Scan(&previous)
		if err == nil {
			if !bytes.Equal(previous, config) {
				return wg.ErrConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := t.reserveCLI(int64(1024+len(name)+len(config)), 1); err != nil {
			return err
		}
		_, err = t.tx.ExecContext(ctx, `INSERT INTO cao_operational_webhook_queues(namespace,name,config) VALUES($1,$2,$3)`, t.namespace, name, config)
		return err
	})
	if err != nil {
		return nil, webhookError(err)
	}
	return &webhookQueue{store: s, name: name, options: options}, nil
}

func webhookError(err error) error {
	if errors.Is(err, operational.ErrCapacity) {
		return wg.ErrCapacity
	}
	return err
}

func (q *webhookQueue) transact(ctx context.Context, fn func(*transaction) error) error {
	return webhookError(q.store.transact(ctx, fn))
}

func (q *webhookQueue) policy(t *transaction) (wg.PolicyRevision, error) {
	var policy wg.PolicyRevision
	err := t.tx.QueryRowContext(t.ctx, `SELECT policy_revision,policy_digest,enabled FROM cao_operational_webhook_queues WHERE namespace=$1 AND name=$2`,
		t.namespace, q.name).Scan(&policy.Revision, &policy.Digest, &policy.Enabled)
	return policy, err
}

func (q *webhookQueue) ApplyRevision(ctx context.Context, next wg.PolicyRevision) error {
	if next.Revision < 1 || len(next.Digest) != sha256.Size*2 {
		return wg.ErrInvalid
	}
	if _, err := hex.DecodeString(next.Digest); err != nil {
		return wg.ErrInvalid
	}
	return q.transact(ctx, func(t *transaction) error {
		return q.applyRevision(t, next)
	})
}

func (q *webhookQueue) applyRevision(t *transaction, next wg.PolicyRevision) error {
	current, err := q.policy(t)
	if err != nil {
		return err
	}
	if next.Revision < current.Revision || (next.Revision == current.Revision && next != current) {
		return wg.ErrConflict
	}
	if next == current {
		return nil
	}
	if _, err := t.tx.ExecContext(t.ctx, `UPDATE cao_operational_webhook_queues SET policy_revision=$3,policy_digest=$4,enabled=$5 WHERE namespace=$1 AND name=$2`,
		t.namespace, q.name, next.Revision, next.Digest, next.Enabled); err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, `UPDATE cao_operational_webhook_jobs SET state='cancelled',version=version+1,fence=fence+1,owner='',lease_until=NULL,terminal_at=$3,retain_until=greatest(retain_until,$4),reason='policy-revision-changed' WHERE namespace=$1 AND queue=$2 AND state IN ('ready','claimed')`, t.namespace, q.name, t.now, t.now.Add(q.options.TerminalRetention))
	return err
}

func (q *webhookQueue) detail(t *transaction, id string) (wg.Job, error) {
	var job wg.Job
	var document []byte
	var lease sql.NullTime
	err := t.tx.QueryRowContext(t.ctx, `SELECT document,state,version,fence,owner,attempt,created_at,expires_at,not_before,lease_until,reason,accepted_status,workflow_run_id FROM cao_operational_webhook_jobs WHERE namespace=$1 AND queue=$2 AND id=$3`,
		t.namespace, q.name, id).Scan(&document, &job.State, &job.Version, &job.Fence, &job.ClaimID, &job.Attempt,
		&job.CreatedAt, &job.ExpiresAt, &job.NotBefore, &lease, &job.Reason, &job.AcceptedStatus, &job.WorkflowRunID)
	if errors.Is(err, sql.ErrNoRows) {
		return wg.Job{}, wg.ErrNotFound
	}
	if err != nil {
		return wg.Job{}, err
	}
	var immutable wg.Job
	if err := json.Unmarshal(document, &immutable); err != nil {
		return wg.Job{}, fmt.Errorf("corrupt webhook job: %w", err)
	}
	immutable.State, immutable.Version, immutable.Fence = job.State, job.Version, job.Fence
	immutable.ClaimID, immutable.Attempt = job.ClaimID, job.Attempt
	immutable.CreatedAt, immutable.ExpiresAt, immutable.NotBefore = job.CreatedAt, job.ExpiresAt, job.NotBefore
	immutable.Reason, immutable.LeaseUntil = job.Reason, time.Time{}
	immutable.AcceptedStatus, immutable.WorkflowRunID = job.AcceptedStatus, job.WorkflowRunID
	if lease.Valid {
		immutable.LeaseUntil = lease.Time.UTC()
	}
	return immutable, nil
}

func webhookFence(job wg.Job, fence wg.Fence, now time.Time) error {
	if fence.JobID != job.ID || fence.Owner == "" || fence.Owner != job.ClaimID ||
		fence.Generation == 0 || fence.Generation != job.Fence || fence.Version != job.Version ||
		!now.Before(job.LeaseUntil) || (job.State != wg.Claimed && job.State != wg.Dispatching) {
		return wg.ErrConflict
	}
	if job.Version >= math.MaxInt64-1 || job.Fence >= math.MaxInt64-1 {
		return wg.ErrConflict
	}
	return nil
}

func webhookTerminal(state wg.JobState) bool {
	switch state {
	case wg.Succeeded, wg.Expired, wg.Rejected, wg.Cancelled, wg.Failed, wg.Ambiguous:
		return true
	default:
		return false
	}
}

func (q *webhookQueue) Renew(ctx context.Context, fence wg.Fence, ttl time.Duration) (wg.Job, error) {
	if ttl < time.Millisecond || ttl > q.options.LeaseDuration {
		return wg.Job{}, wg.ErrInvalid
	}
	var job wg.Job
	err := q.transact(ctx, func(t *transaction) error {
		var err error
		job, err = q.detail(t, fence.JobID)
		if err != nil {
			return err
		}
		if err := webhookFence(job, fence, t.now); err != nil {
			return err
		}
		if job.State == wg.Dispatching && strings.HasPrefix(job.ClaimID, "batch.") {
			return wg.ErrConflict
		}
		until := t.now.Add(ttl)
		subscriptionUntil := t.now.Add(time.Duration(job.Subscription.Delivery.LeaseSeconds) * time.Second)
		if subscriptionUntil.Before(until) {
			until = subscriptionUntil
		}
		if job.ExpiresAt.Before(until) {
			until = job.ExpiresAt
		}
		if !t.now.Before(until) {
			return wg.ErrConflict
		}
		_, err = t.tx.ExecContext(ctx, `UPDATE cao_operational_webhook_jobs SET version=version+1,lease_until=$4 WHERE namespace=$1 AND queue=$2 AND id=$3`, t.namespace, q.name, job.ID, until)
		if err != nil {
			return err
		}
		job, err = q.detail(t, job.ID)
		return err
	})
	if err != nil {
		return wg.Job{}, err
	}
	return job, nil
}
