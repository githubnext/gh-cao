package postgres

import (
	"context"
	"regexp"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

var quotaLog = logger.New("cao:operational:postgres:quota")

const maxQuotaValue = int64(1 << 40)

var quotaIdentifier = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,256}$`)

// quotaObservationOutcomeName converts a GitHubQuotaObserveOutcome into the
// stable diagnostic label ObserveGitHubQuota logs, so the three-way
// stale/replaced/reconciled decision is independently testable without
// constructing a *Store or invoking ObserveGitHubQuota. It mirrors the
// memory package's quotaObservationOutcomeName.
func quotaObservationOutcomeName(outcome operational.GitHubQuotaObserveOutcome) string {
	switch outcome {
	case operational.GitHubQuotaObservationReplaced:
		return "replaced"
	case operational.GitHubQuotaObservationReconciled:
		return "reconciled"
	default:
		return "stale"
	}
}

// quotaAdmissionName converts a GitHubQuotaAdmission into the stable
// diagnostic label ReserveGitHubQuota logs, so each admission branch is
// independently testable without constructing a *Store or invoking
// ReserveGitHubQuota. It mirrors the memory package's quotaAdmissionName.
func quotaAdmissionName(code operational.GitHubQuotaAdmission) string {
	switch code {
	case operational.GitHubQuotaAdmitted:
		return "admitted"
	case operational.GitHubQuotaParked:
		return "parked"
	case operational.GitHubQuotaExhausted:
		return "exhausted"
	case operational.GitHubQuotaUnknown:
		return "unknown"
	case operational.GitHubQuotaDuplicateReservation:
		return "duplicate-reservation"
	default:
		return "unrecognized"
	}
}

type reservation struct {
	Cost    int64
	Expires time.Time
}

type quota struct {
	State        operational.GitHubQuotaState
	Reservations map[string]reservation
}

func quotaNames(values ...string) error {
	for _, v := range values {
		if !quotaIdentifier.MatchString(v) {
			return invalid("invalid quota identifier")
		}
	}
	return nil
}

func (t *transaction) quota(bucket string) (quota, error) {
	q := quota{Reservations: map[string]reservation{}}
	_, err := t.json(key{"quota", bucket, ""}, &q)
	if err != nil {
		return q, err
	}
	if q.Reservations == nil {
		return q, invalid("corrupt quota reservations")
	}
	q.State.Now, q.State.Reserved = t.now, 0
	for id, r := range q.Reservations {
		if !r.Expires.After(t.now) {
			delete(q.Reservations, id)
		} else {
			q.State.Reserved += r.Cost
		}
	}
	return q, nil
}

func (t *transaction) saveQuota(bucket string, q quota) error {
	q.State.Reserved = 0
	for _, r := range q.Reservations {
		q.State.Reserved += r.Cost
	}
	q.State.Now = t.now
	horizon := t.now
	if q.State.ResetAt.After(horizon) {
		horizon = q.State.ResetAt
	}
	if q.State.ParkedUntil.After(horizon) {
		horizon = q.State.ParkedUntil
	}
	return t.putJSON(key{"quota", bucket, ""}, q, horizon.Add(24*time.Hour))
}

func (s *Store) GitHubQuotaSnapshot(ctx context.Context, bucket string) (operational.GitHubQuotaState, error) {
	if err := quotaNames(bucket); err != nil {
		return operational.GitHubQuotaState{}, err
	}
	var state operational.GitHubQuotaState
	err := s.transact(ctx, func(t *transaction) error {
		q, err := t.quota(bucket)
		state = q.State
		return err
	})
	return state, err
}

func (s *Store) ObserveGitHubQuota(ctx context.Context, bucket string, observation operational.GitHubQuotaObservation, releaseID string) (operational.GitHubQuotaObserveOutcome, bool, operational.GitHubQuotaState, error) {
	if err := quotaNames(bucket); err != nil {
		return 0, false, operational.GitHubQuotaState{}, err
	}
	if releaseID != "" {
		if err := quotaNames(releaseID); err != nil {
			return 0, false, operational.GitHubQuotaState{}, err
		}
	}
	if observation.Limit < 0 || observation.Limit > maxQuotaValue || observation.Remaining < 0 ||
		observation.Remaining > maxQuotaValue || observation.ResetAt.IsZero() || observation.ResetAt.UnixMilli() <= 0 ||
		(!observation.ObservedAt.IsZero() && observation.ObservedAt.UnixMilli() <= 0) {
		return 0, false, operational.GitHubQuotaState{}, invalid("invalid quota observation")
	}
	outcome := operational.GitHubQuotaObservationStale
	var released bool
	var state operational.GitHubQuotaState
	err := s.transact(ctx, func(t *transaction) error {
		q, err := t.quota(bucket)
		if err != nil {
			return err
		}
		reset, observed := observation.ResetAt.Truncate(time.Millisecond), observation.ObservedAt.Truncate(time.Millisecond)
		if observed.IsZero() {
			observed = t.now
		}
		if !q.State.Known || reset.After(q.State.ResetAt) {
			q.State.Known, q.State.Limit, q.State.Remaining = true, observation.Limit, observation.Remaining
			q.State.ResetAt, q.State.ObservedAt = reset, observed
			outcome = operational.GitHubQuotaObservationReplaced
		} else if reset.Equal(q.State.ResetAt) {
			q.State.Limit, q.State.Remaining = observation.Limit, min(q.State.Remaining, observation.Remaining)
			if observed.After(q.State.ObservedAt) {
				q.State.ObservedAt = observed
			}
			outcome = operational.GitHubQuotaObservationReconciled
		}
		_, released = q.Reservations[releaseID]
		delete(q.Reservations, releaseID)
		if err := t.saveQuota(bucket, q); err != nil {
			return err
		}
		q, err = t.quota(bucket)
		state = q.State
		return err
	})
	if err == nil {
		quotaLog.Printf("quota observation outcome=%s released=%t", quotaObservationOutcomeName(outcome), released)
	}
	return outcome, released && err == nil, state, err
}

func (s *Store) ReserveGitHubQuota(ctx context.Context, bucket, id string, cost, minimum int64, ttl time.Duration) (operational.GitHubQuotaAdmission, time.Time, operational.GitHubQuotaState, error) {
	if err := quotaNames(bucket, id); err != nil {
		return 0, time.Time{}, operational.GitHubQuotaState{}, err
	}
	if cost <= 0 || cost > maxQuotaValue || minimum < 0 || minimum > maxQuotaValue || ttl < time.Millisecond || ttl > 24*time.Hour {
		return 0, time.Time{}, operational.GitHubQuotaState{}, invalid("invalid quota cost, floor, or TTL")
	}
	var state operational.GitHubQuotaState
	var expires time.Time
	admission := operational.GitHubQuotaAdmitted
	err := s.transact(ctx, func(t *transaction) error {
		q, err := t.quota(bucket)
		if err != nil {
			return err
		}
		state = q.State
		switch {
		case state.ParkedUntil.After(t.now):
			admission = operational.GitHubQuotaParked
		case !state.Known || !state.ResetAt.After(t.now):
			admission = operational.GitHubQuotaUnknown
		case q.Reservations[id].Cost != 0:
			admission = operational.GitHubQuotaDuplicateReservation
		case state.Remaining-state.Reserved-cost < minimum:
			admission = operational.GitHubQuotaExhausted
		}
		if admission != operational.GitHubQuotaAdmitted {
			quotaLog.Printf("quota reservation denied outcome=%s", quotaAdmissionName(admission))
			return nil
		}
		expires = t.now.Add(ttl.Truncate(time.Millisecond))
		q.Reservations[id] = reservation{Cost: cost, Expires: expires}
		if err := t.saveQuota(bucket, q); err != nil {
			return err
		}
		q, err = t.quota(bucket)
		state = q.State
		return err
	})
	if err != nil {
		return operational.GitHubQuotaUnknown, time.Time{}, state, err
	}
	return admission, expires, state, nil
}

func (s *Store) ReleaseGitHubQuota(ctx context.Context, bucket, id string) (bool, operational.GitHubQuotaState, error) {
	if err := quotaNames(bucket, id); err != nil {
		return false, operational.GitHubQuotaState{}, err
	}
	var released bool
	state, err := s.updateQuota(ctx, bucket, func(q *quota) (bool, error) {
		_, released = q.Reservations[id]
		delete(q.Reservations, id)
		return released, nil
	})
	return released && err == nil, state, err
}

func (s *Store) ParkGitHubQuota(ctx context.Context, bucket string, until time.Time, reason string) (bool, operational.GitHubQuotaState, error) {
	if until.IsZero() || until.UnixMilli() <= 0 || len(reason) > 256 {
		return false, operational.GitHubQuotaState{}, invalid("invalid parking time or reason")
	}
	var extended bool
	state, err := s.updateQuota(ctx, bucket, func(q *quota) (bool, error) {
		until = until.Truncate(time.Millisecond)
		if until.After(q.State.ParkedUntil) {
			q.State.ParkedUntil, q.State.ParkReason = until, reason
			extended = true
		}
		return extended, nil
	})
	return extended && err == nil, state, err
}

func (s *Store) UnparkGitHubQuota(ctx context.Context, bucket string) (operational.GitHubQuotaState, error) {
	return s.updateQuota(ctx, bucket, func(q *quota) (bool, error) {
		changed := !q.State.ParkedUntil.IsZero() || q.State.ParkReason != ""
		q.State.ParkedUntil, q.State.ParkReason = time.Time{}, ""
		return changed, nil
	})
}

// updateQuota persists the bucket only when change reports a mutation, so a
// no-op release, unpark or parking extension never creates or rewrites a
// protected record, matching the memory and Redis adapters.
func (s *Store) updateQuota(ctx context.Context, bucket string, change func(*quota) (bool, error)) (operational.GitHubQuotaState, error) {
	if err := quotaNames(bucket); err != nil {
		return operational.GitHubQuotaState{}, err
	}
	var state operational.GitHubQuotaState
	err := s.transact(ctx, func(t *transaction) error {
		q, err := t.quota(bucket)
		if err != nil {
			return err
		}
		changed, err := change(&q)
		if err != nil {
			return err
		}
		if changed {
			if err := t.saveQuota(bucket, q); err != nil {
				return err
			}
			if q, err = t.quota(bucket); err != nil {
				return err
			}
		}
		state = q.State
		return nil
	})
	return state, err
}
