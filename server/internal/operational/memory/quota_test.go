package memory

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func observation(clock *testClock, remaining int64) operational.GitHubQuotaObservation {
	return operational.GitHubQuotaObservation{Limit: 100, Remaining: remaining, ResetAt: clock.Now().Add(time.Hour)}
}

func TestQuotaUnknownParkingAndObservationReconciliation(t *testing.T) {
	s, clock := fixture(t, Config{})
	ctx := t.Context()
	state, err := s.GitHubQuotaSnapshot(ctx, "installation:1:core")
	must(t, err)
	if state.Known || !state.Now.Equal(clock.Now()) {
		t.Fatalf("unknown state = %#v", state)
	}
	code, expires, _, err := s.ReserveGitHubQuota(ctx, "installation:1:core", "r1", 1, 0, time.Minute)
	must(t, err)
	if code != operational.GitHubQuotaUnknown || !expires.IsZero() {
		t.Fatalf("unknown admission = %v", code)
	}
	ok, state, err := s.ParkGitHubQuota(ctx, "installation:1:core", clock.Now().Add(time.Minute), "secondary")
	must(t, err)
	if !ok || state.Known {
		t.Fatal("park manufactured a known quota")
	}
	code, _, _, err = s.ReserveGitHubQuota(ctx, "installation:1:core", "r1", 1, 0, time.Minute)
	must(t, err)
	if code != operational.GitHubQuotaParked {
		t.Fatal("parking not honored before unknown")
	}
	obs := observation(clock, 20)
	outcome, released, state, err := s.ObserveGitHubQuota(ctx, "installation:1:core", obs, "")
	must(t, err)
	if outcome != operational.GitHubQuotaObservationReplaced || released || !state.Known ||
		state.Remaining != 20 || state.ParkReason != "secondary" {
		t.Fatalf("observation = %v %t %#v", outcome, released, state)
	}
	_, _, err = s.ParkGitHubQuota(ctx, "installation:1:core", clock.Now().Add(30*time.Second), "shorter")
	must(t, err)
	state, err = s.UnparkGitHubQuota(ctx, "installation:1:core")
	must(t, err)
	if !state.ParkedUntil.IsZero() || state.ParkReason != "" {
		t.Fatal("unpark retained parking")
	}
	code, expires, state, err = s.ReserveGitHubQuota(ctx, "installation:1:core", "r1", 5, 10, time.Minute)
	must(t, err)
	if code != operational.GitHubQuotaAdmitted || state.Reserved != 5 || !expires.Equal(clock.Now().Add(time.Minute)) {
		t.Fatalf("reserve = %v %#v", code, state)
	}
	obs.Remaining = 15
	outcome, released, state, err = s.ObserveGitHubQuota(ctx, "installation:1:core", obs, "r1")
	must(t, err)
	if outcome != operational.GitHubQuotaObservationReconciled || !released || state.Reserved != 0 || state.Remaining != 15 {
		t.Fatalf("commit = %v %t %#v", outcome, released, state)
	}
	obs.Remaining = 99
	_, _, state, err = s.ObserveGitHubQuota(ctx, "installation:1:core", obs, "")
	must(t, err)
	if state.Remaining != 15 {
		t.Fatal("same-window response replenished remaining")
	}
	older := obs
	older.ResetAt = obs.ResetAt.Add(-time.Minute)
	outcome, _, state, err = s.ObserveGitHubQuota(ctx, "installation:1:core", older, "")
	must(t, err)
	if outcome != operational.GitHubQuotaObservationStale || state.Remaining != 15 {
		t.Fatal("stale observation replaced quota")
	}
	obs.ResetAt = obs.ResetAt.Add(time.Hour)
	outcome, _, state, err = s.ObserveGitHubQuota(ctx, "installation:1:core", obs, "")
	must(t, err)
	if outcome != operational.GitHubQuotaObservationReplaced || state.Remaining != 99 {
		t.Fatal("new window not admitted")
	}
	clock.Add(3 * time.Hour)
	code, _, _, err = s.ReserveGitHubQuota(ctx, "installation:1:core", "expired-window", 1, 0, time.Minute)
	must(t, err)
	if code != operational.GitHubQuotaUnknown {
		t.Fatal("expired known window used for admission")
	}
	invariant(t, s)
}

func TestQuotaConcurrentReservationsExpiryReleaseAndCapacity(t *testing.T) {
	config := DefaultConfig()
	config.MaxQuotaBuckets = 1
	config.MaxQuotaReservations = 10
	s, clock := fixture(t, config)
	ctx := t.Context()
	obs := observation(clock, 10)
	_, _, _, err := s.ObserveGitHubQuota(ctx, "core", obs, "")
	must(t, err)
	var admitted atomic.Int64
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _, _, err := s.ReserveGitHubQuota(ctx, "core", fmt.Sprintf("r%d", i), 1, 2, time.Second)
			if err != nil {
				t.Error(err)
			} else if code == operational.GitHubQuotaAdmitted {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 8 {
		t.Fatalf("reserved %d, want 8", admitted.Load())
	}
	state, err := s.GitHubQuotaSnapshot(ctx, "core")
	must(t, err)
	if state.Reserved != 8 || state.Remaining != 10 {
		t.Fatalf("accounting = %#v", state)
	}
	_, _, _, err = s.ObserveGitHubQuota(ctx, "other", obs, "")
	wantError(t, err, operational.ErrCapacity)
	clock.Add(time.Second)
	state, err = s.GitHubQuotaSnapshot(ctx, "core")
	must(t, err)
	if state.Reserved != 0 {
		t.Fatal("expired reservations retained")
	}
	code, _, _, err := s.ReserveGitHubQuota(ctx, "core", "r", 2, 0, time.Minute)
	must(t, err)
	if code != operational.GitHubQuotaAdmitted {
		t.Fatal(code)
	}
	code, _, _, err = s.ReserveGitHubQuota(ctx, "core", "r", 2, 0, time.Minute)
	must(t, err)
	if code != operational.GitHubQuotaDuplicateReservation {
		t.Fatal("duplicate charged twice")
	}
	released, state, err := s.ReleaseGitHubQuota(ctx, "core", "r")
	must(t, err)
	if !released || state.Reserved != 0 {
		t.Fatal("release failed")
	}
	released, _, err = s.ReleaseGitHubQuota(ctx, "core", "r")
	must(t, err)
	if released {
		t.Fatal("release not idempotent")
	}
	clock.Add(26 * time.Hour)
	must(t, s.Maintain(ctx))
	state, err = s.GitHubQuotaSnapshot(ctx, "core")
	must(t, err)
	if state.Known {
		t.Fatal("quota TTL did not expire")
	}
	_, _, _, err = s.ObserveGitHubQuota(ctx, "other", observation(clock, 1), "")
	must(t, err)
	invariant(t, s)
}

func TestQuotaReservationLimitDoesNotEvictActiveReservations(t *testing.T) {
	config := DefaultConfig()
	config.MaxQuotaReservations = 1
	s, clock := fixture(t, config)
	ctx := t.Context()
	_, _, _, err := s.ObserveGitHubQuota(ctx, "core", observation(clock, 100), "")
	must(t, err)
	_, _, _, err = s.ReserveGitHubQuota(ctx, "core", "one", 1, 0, time.Second)
	must(t, err)
	_, _, state, err := s.ReserveGitHubQuota(ctx, "core", "two", 1, 0, time.Second)
	wantError(t, err, operational.ErrCapacity)
	if state.Reserved != 1 {
		t.Fatal("capacity error evicted active reservation")
	}
	clock.Add(time.Second)
	code, _, _, err := s.ReserveGitHubQuota(ctx, "core", "two", 1, 0, time.Second)
	must(t, err)
	if code != operational.GitHubQuotaAdmitted {
		t.Fatal("expiration did not free reservation capacity")
	}
	invariant(t, s)
}

func TestQuotaUsagePeaksRetentionBoundsAndCopies(t *testing.T) {
	config := DefaultConfig()
	config.MaxQuotaUsageSamples = 1
	s, clock := fixture(t, config)
	ctx := t.Context()
	for _, sample := range [][3]int64{{100, 80, 1}, {10, 9, 2}, {100, 80, 3}} {
		recorded, err := s.RecordGitHubQuotaUsage(ctx, "core", clock.Now(), sample[0], sample[1], sample[2])
		must(t, err)
		if !recorded {
			t.Fatal("valid usage ignored")
		}
	}
	samples, at, err := s.GitHubQuotaUsage(ctx)
	must(t, err)
	if len(samples) != 1 || samples[0].Limit != 10 || samples[0].Used != 9 || samples[0].Reserved != 3 || !at.Equal(clock.Now()) {
		t.Fatalf("peak usage = %#v", samples)
	}
	samples[0].Used = 0
	samples, _, _ = s.GitHubQuotaUsage(ctx)
	if samples[0].Used != 9 {
		t.Fatal("usage output aliased")
	}
	_, err = s.RecordGitHubQuotaUsage(ctx, "other", clock.Now(), 1, 1, 1)
	wantError(t, err, operational.ErrCapacity)
	recorded, err := s.RecordGitHubQuotaUsage(ctx, "old", clock.Now().Add(-25*time.Hour), 1, 1, 1)
	must(t, err)
	if recorded {
		t.Fatal("old usage extended retention")
	}
	clock.Add(24*time.Hour + operational.GitHubQuotaUsageInterval)
	samples, _, err = s.GitHubQuotaUsage(ctx)
	must(t, err)
	if len(samples) != 0 {
		t.Fatal("usage retained beyond 24-hour horizon")
	}
	for _, sample := range [][3]int64{{maxQuotaValue, maxQuotaValue - 10, 1}, {maxQuotaValue - 10, maxQuotaValue - 10, 2}} {
		_, err := s.RecordGitHubQuotaUsage(ctx, "large", clock.Now(), sample[0], sample[1], sample[2])
		must(t, err)
	}
	samples, _, _ = s.GitHubQuotaUsage(ctx)
	if len(samples) != 1 || samples[0].Limit != maxQuotaValue-10 || samples[0].Reserved != 2 {
		t.Fatal("large ratio multiplication overflowed")
	}
	invariant(t, s)
}

func TestLegacyBudgetSerializationAndSameWindowFloor(t *testing.T) {
	s, clock := fixture(t, Config{})
	ctx := t.Context()
	now := clock.Now().Unix()
	result, err := s.ReserveRateLimit(ctx, "collect:rate-limit", "1", 10, 5, now)
	must(t, err)
	if result != 3 {
		t.Fatal("missing legacy budget not unknown")
	}
	must(t, s.ObserveRateLimit(ctx, "collect:rate-limit", "1", 20, now+3600))
	result, err = s.ReserveRateLimit(ctx, "collect:rate-limit", "1", 10, 15, now)
	must(t, err)
	if result != 0 {
		t.Fatal("legacy Operational floor-before-cost semantics changed")
	}
	must(t, s.ObserveRateLimit(ctx, "collect:rate-limit", "1", 20, now+3600))
	value, err := s.ReadAttribute(ctx, "collect:rate-limit", "1")
	must(t, err)
	if value != fmt.Sprintf("5|%d|0", now+3600) {
		t.Fatalf("serialized budget = %q", value)
	}
	result, err = s.ReserveRateLimit(ctx, "collect:rate-limit", "1", 10, 1, now)
	must(t, err)
	if result != 2 {
		t.Fatal("same-window observation replenished budget")
	}
	must(t, s.ParkRateLimit(ctx, "collect:rate-limit", "1", now+60))
	must(t, s.ParkRateLimit(ctx, "collect:rate-limit", "1", now+30))
	result, err = s.ReserveRateLimit(ctx, "collect:rate-limit", "1", 10, 1, now)
	must(t, err)
	if result != 1 {
		t.Fatal("parking ignored")
	}
	result, err = s.ReserveRateLimit(ctx, "collect:rate-limit", "1", 10, 1, now+3601)
	must(t, err)
	if result != 3 {
		t.Fatal("expired legacy window used")
	}
	invariant(t, s)
}

func TestQuotaObservationOutcomeName(t *testing.T) {
	cases := []struct {
		outcome operational.GitHubQuotaObserveOutcome
		want    string
	}{
		{operational.GitHubQuotaObservationStale, "stale"},
		{operational.GitHubQuotaObservationReplaced, "replaced"},
		{operational.GitHubQuotaObservationReconciled, "reconciled"},
		{operational.GitHubQuotaObserveOutcome(99), "stale"},
	}
	for _, c := range cases {
		if got := quotaObservationOutcomeName(c.outcome); got != c.want {
			t.Fatalf("quotaObservationOutcomeName(%v) = %q, want %q", c.outcome, got, c.want)
		}
	}
}

func TestQuotaAdmissionName(t *testing.T) {
	cases := []struct {
		code operational.GitHubQuotaAdmission
		want string
	}{
		{operational.GitHubQuotaAdmitted, "admitted"},
		{operational.GitHubQuotaParked, "parked"},
		{operational.GitHubQuotaExhausted, "exhausted"},
		{operational.GitHubQuotaUnknown, "unknown"},
		{operational.GitHubQuotaDuplicateReservation, "duplicate-reservation"},
		{operational.GitHubQuotaAdmission(99), "unrecognized"},
	}
	for _, c := range cases {
		if got := quotaAdmissionName(c.code); got != c.want {
			t.Fatalf("quotaAdmissionName(%v) = %q, want %q", c.code, got, c.want)
		}
	}
}
