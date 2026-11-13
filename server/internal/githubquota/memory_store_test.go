package githubquota

import (
	"context"
	"sync"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

// memoryStore is a deterministic clock-controlled quota service fixture.
type memoryStore struct {
	mu      sync.Mutex
	now     time.Time
	buckets map[string]*memoryBucket
	usage   map[time.Time]map[string]operational.GitHubQuotaUsageSample
	// usageErr makes usage history operations fail.
	usageErr error
}

type memoryBucket struct {
	known        bool
	limit        int64
	remaining    int64
	reset        time.Time
	observed     time.Time
	parked       time.Time
	parkReason   string
	reservations map[string]memoryReservation
}

type memoryReservation struct {
	amount  int64
	expires time.Time
}

func newMemoryStore(now time.Time) *memoryStore {
	return &memoryStore{
		now: now, buckets: map[string]*memoryBucket{},
		usage: map[time.Time]map[string]operational.GitHubQuotaUsageSample{},
	}
}

func (m *memoryStore) advance(duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = m.now.Add(duration)
}

func (m *memoryStore) bucket(key string) *memoryBucket {
	bucket, ok := m.buckets[key]
	if !ok {
		bucket = &memoryBucket{reservations: map[string]memoryReservation{}}
		m.buckets[key] = bucket
	}
	for id, reservation := range bucket.reservations {
		if !reservation.expires.After(m.now) {
			delete(bucket.reservations, id)
		}
	}
	return bucket
}

func (m *memoryStore) state(bucket *memoryBucket) operational.GitHubQuotaState {
	var reserved int64
	for _, reservation := range bucket.reservations {
		reserved += reservation.amount
	}
	return operational.GitHubQuotaState{
		Known: bucket.known, Limit: bucket.limit, Remaining: bucket.remaining, Reserved: reserved,
		ResetAt: bucket.reset, ObservedAt: bucket.observed, ParkedUntil: bucket.parked,
		ParkReason: bucket.parkReason, Now: m.now,
	}
}

func (m *memoryStore) GitHubQuotaSnapshot(_ context.Context, key string) (operational.GitHubQuotaState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state(m.bucket(key)), nil
}

func (m *memoryStore) ObserveGitHubQuota(
	_ context.Context, key string, observation operational.GitHubQuotaObservation, releaseID string,
) (operational.GitHubQuotaObserveOutcome, bool, operational.GitHubQuotaState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket := m.bucket(key)
	released := false
	if releaseID != "" {
		_, released = bucket.reservations[releaseID]
		delete(bucket.reservations, releaseID)
	}
	if observation.ObservedAt.IsZero() {
		observation.ObservedAt = m.now
	}
	outcome := operational.GitHubQuotaObservationStale
	reset := time.UnixMilli(observation.ResetAt.UnixMilli()).UTC()
	switch {
	case !bucket.known || reset.After(bucket.reset):
		bucket.known, bucket.limit, bucket.remaining = true, observation.Limit, observation.Remaining
		bucket.reset, bucket.observed = reset, observation.ObservedAt
		outcome = operational.GitHubQuotaObservationReplaced
	case reset.Equal(bucket.reset):
		bucket.limit = observation.Limit
		bucket.remaining = min(bucket.remaining, observation.Remaining)
		if observation.ObservedAt.After(bucket.observed) {
			bucket.observed = observation.ObservedAt
		}
		outcome = operational.GitHubQuotaObservationReconciled
	}
	return outcome, released, m.state(bucket), nil
}

func (m *memoryStore) ReserveGitHubQuota(
	_ context.Context, key, id string, cost, minimumRemain int64, ttl time.Duration,
) (operational.GitHubQuotaAdmission, time.Time, operational.GitHubQuotaState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket := m.bucket(key)
	state := m.state(bucket)
	switch {
	case bucket.parked.After(m.now):
		return operational.GitHubQuotaParked, time.Time{}, state, nil
	case !bucket.known || !bucket.reset.After(m.now):
		return operational.GitHubQuotaUnknown, time.Time{}, state, nil
	case bucket.reservations[id] != (memoryReservation{}):
		return operational.GitHubQuotaDuplicateReservation, time.Time{}, state, nil
	case bucket.remaining-state.Reserved-cost < minimumRemain:
		return operational.GitHubQuotaExhausted, time.Time{}, state, nil
	}
	expires := m.now.Add(ttl)
	bucket.reservations[id] = memoryReservation{amount: cost, expires: expires}
	return operational.GitHubQuotaAdmitted, expires, m.state(bucket), nil
}

func (m *memoryStore) ReleaseGitHubQuota(_ context.Context, key, id string) (bool, operational.GitHubQuotaState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket := m.bucket(key)
	_, released := bucket.reservations[id]
	delete(bucket.reservations, id)
	return released, m.state(bucket), nil
}

func (m *memoryStore) ParkGitHubQuota(
	_ context.Context, key string, until time.Time, reason string,
) (bool, operational.GitHubQuotaState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket := m.bucket(key)
	if !until.After(bucket.parked) {
		return false, m.state(bucket), nil
	}
	bucket.parked, bucket.parkReason = until, reason
	return true, m.state(bucket), nil
}

func (m *memoryStore) UnparkGitHubQuota(_ context.Context, key string) (operational.GitHubQuotaState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket := m.bucket(key)
	bucket.parked, bucket.parkReason = time.Time{}, ""
	return m.state(bucket), nil
}

func (m *memoryStore) RecordGitHubQuotaUsage(
	_ context.Context, key string, at time.Time, limit, used, reserved int64,
) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.usageErr != nil {
		return false, m.usageErr
	}
	slot := at.UTC().Truncate(operational.GitHubQuotaUsageInterval)
	if !slot.Add(operational.GitHubQuotaUsageInterval).After(m.now.Add(-operational.GitHubQuotaUsageRetention)) ||
		slot.After(m.now.Add(operational.GitHubQuotaUsageInterval)) {
		return false, nil
	}
	samples, ok := m.usage[slot]
	if !ok {
		samples = map[string]operational.GitHubQuotaUsageSample{}
		m.usage[slot] = samples
	}
	sample := operational.GitHubQuotaUsageSample{Bucket: key, Slot: slot, Limit: limit, Used: used, Reserved: reserved}
	if current, ok := samples[key]; ok {
		if current.Used*limit > used*current.Limit || (current.Used*limit == used*current.Limit && current.Used > used) {
			sample.Limit, sample.Used = current.Limit, current.Used
		}
		sample.Reserved = max(sample.Reserved, current.Reserved)
	}
	samples[key] = sample
	return true, nil
}

func (m *memoryStore) GitHubQuotaUsage(context.Context) ([]operational.GitHubQuotaUsageSample, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.usageErr != nil {
		return nil, time.Time{}, m.usageErr
	}
	samples := []operational.GitHubQuotaUsageSample{}
	for slot, bucketSamples := range m.usage {
		if !slot.Add(operational.GitHubQuotaUsageInterval).After(m.now.Add(-operational.GitHubQuotaUsageRetention)) {
			continue
		}
		for _, sample := range bucketSamples {
			samples = append(samples, sample)
		}
	}
	return samples, m.now, nil
}
