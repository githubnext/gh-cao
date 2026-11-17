package redisx

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

var gitHubQuotaLog = logger.New("cao:redis:githubquota")

// GitHubQuotaState is the persisted state of one independently metered
// GitHub API quota bucket as read inside an atomic Redis script. Remaining is
// the last authoritative GitHub observation; Reserved is the sum of the
// unexpired reservations that have not yet been committed or released. Now is
// the Redis server clock so every replica classifies the state identically.
type GitHubQuotaState = operational.GitHubQuotaState

// GitHubQuotaObservation is one authoritative rate-limit observation taken
// from GitHub response metadata.
type GitHubQuotaObservation = operational.GitHubQuotaObservation

// GitHubQuotaObserveOutcome reports how an observation was reconciled.
type GitHubQuotaObserveOutcome = operational.GitHubQuotaObserveOutcome

const (
	// GitHubQuotaObservationStale means the observation belongs to an older
	// reset window and was ignored.
	GitHubQuotaObservationStale = operational.GitHubQuotaObservationStale
	// GitHubQuotaObservationReplaced means the observation started a new reset
	// window (or the first known window) and replaced the recorded state.
	GitHubQuotaObservationReplaced = operational.GitHubQuotaObservationReplaced
	// GitHubQuotaObservationReconciled means the observation belongs to the
	// recorded window and was merged without increasing remaining quota.
	GitHubQuotaObservationReconciled = operational.GitHubQuotaObservationReconciled
)

// GitHubQuotaAdmission is the result of an atomic reservation attempt.
type GitHubQuotaAdmission = operational.GitHubQuotaAdmission

const (
	GitHubQuotaAdmitted             = operational.GitHubQuotaAdmitted
	GitHubQuotaParked               = operational.GitHubQuotaParked
	GitHubQuotaExhausted            = operational.GitHubQuotaExhausted
	GitHubQuotaUnknown              = operational.GitHubQuotaUnknown
	GitHubQuotaDuplicateReservation = operational.GitHubQuotaDuplicateReservation
)

var gitHubQuotaIdentifier = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,256}$`)

const (
	maxGitHubQuotaParkReason = 256
	maxGitHubQuotaCost       = 1 << 40
	maxGitHubQuotaTTL        = 24 * time.Hour
)

// gitHubQuotaPrelude prunes expired reservations using the Redis clock and
// defines helpers shared by every quota script. KEYS[1] is the bucket state
// hash, KEYS[2] maps reservation IDs to amounts, and KEYS[3] orders
// reservation IDs by expiry. Reserved capacity is always recomputed from the
// live reservations so accounting cannot drift.
const gitHubQuotaPrelude = `
local clock = redis.call("TIME")
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
for _, id in ipairs(redis.call("ZRANGEBYSCORE", KEYS[3], "-inf", now)) do
  redis.call("HDEL", KEYS[2], id)
  redis.call("ZREM", KEYS[3], id)
end
local function reserved_total()
  local total = 0
  for _, amount in ipairs(redis.call("HVALS", KEYS[2])) do
    total = total + (tonumber(amount) or 0)
  end
  return total
end
local function touch()
  local s = redis.call("HMGET", KEYS[1], "reset", "parked")
  local horizon = math.max(tonumber(s[1]) or 0, tonumber(s[2]) or 0, now)
  if redis.call("EXISTS", KEYS[1]) == 1 then
    redis.call("PEXPIRE", KEYS[1], horizon - now + 86400000)
  end
  local latest = redis.call("ZRANGE", KEYS[3], -1, -1, "WITHSCORES")
  if latest[2] then
    local ttl = math.max(tonumber(latest[2]) - now, 0) + 60000
    redis.call("PEXPIRE", KEYS[2], ttl)
    redis.call("PEXPIRE", KEYS[3], ttl)
  else
    redis.call("DEL", KEYS[2], KEYS[3])
  end
end
local function state_reply(code, extra)
  local s = redis.call("HMGET", KEYS[1], "limit", "remaining", "reset", "observed", "parked", "park_reason")
  local known = 0
  if tonumber(s[2]) then known = 1 end
  return {code, extra, tonumber(s[1]) or 0, tonumber(s[2]) or 0, tonumber(s[3]) or 0,
    tonumber(s[4]) or 0, tonumber(s[5]) or 0, reserved_total(), now, known, s[6] or ""}
end
`

const gitHubQuotaSnapshotScript = gitHubQuotaPrelude + `
touch()
return state_reply(0, 0)`

// Observations from a newer reset window replace the recorded window.
// Observations from the recorded window never increase remaining quota, so an
// older or concurrent response cannot restore capacity that GitHub already
// reported as consumed. Observations from an older window are ignored.
const gitHubQuotaObserveScript = gitHubQuotaPrelude + `
local released = 0
if ARGV[5] ~= "" then
  released = redis.call("HDEL", KEYS[2], ARGV[5])
  redis.call("ZREM", KEYS[3], ARGV[5])
end
local s = redis.call("HMGET", KEYS[1], "remaining", "reset", "observed")
local current = tonumber(s[1])
local current_reset = tonumber(s[2])
local current_observed = tonumber(s[3]) or 0
local remaining = tonumber(ARGV[2])
local reset = tonumber(ARGV[3])
local observed = tonumber(ARGV[4])
local observed_arg = ARGV[4]
if observed <= 0 then
  observed = now
  observed_arg = now
end
local outcome = 0
if not current or not current_reset or reset > current_reset then
  redis.call("HSET", KEYS[1], "limit", ARGV[1], "remaining", ARGV[2], "reset", ARGV[3], "observed", observed_arg)
  outcome = 1
elseif reset == current_reset then
  redis.call("HSET", KEYS[1], "limit", ARGV[1], "remaining", math.min(current, remaining),
    "observed", math.max(current_observed, observed))
  outcome = 2
end
touch()
return state_reply(outcome, released)`

// Admission requires remaining - reserved - cost >= minimum so a reservation
// can never cross the requested floor.
const gitHubQuotaReserveScript = gitHubQuotaPrelude + `
local s = redis.call("HMGET", KEYS[1], "remaining", "reset", "parked")
local code = 0
local expires = 0
if (tonumber(s[3]) or 0) > now then
  code = 1
elseif not tonumber(s[1]) or (tonumber(s[2]) or 0) <= now then
  code = 3
elseif redis.call("HEXISTS", KEYS[2], ARGV[1]) == 1 then
  code = 4
elseif tonumber(s[1]) - reserved_total() - tonumber(ARGV[2]) < tonumber(ARGV[3]) then
  code = 2
else
  expires = now + tonumber(ARGV[4])
  redis.call("HSET", KEYS[2], ARGV[1], ARGV[2])
  redis.call("ZADD", KEYS[3], expires, ARGV[1])
end
touch()
return state_reply(code, expires)`

const gitHubQuotaReleaseScript = gitHubQuotaPrelude + `
local released = redis.call("HDEL", KEYS[2], ARGV[1])
redis.call("ZREM", KEYS[3], ARGV[1])
touch()
return state_reply(released, 0)`

// Parking only ever extends the parked-until instant and never changes the
// primary quota observation.
const gitHubQuotaParkScript = gitHubQuotaPrelude + `
local parked = tonumber(redis.call("HGET", KEYS[1], "parked")) or 0
local extended = 0
if tonumber(ARGV[1]) > parked then
  redis.call("HSET", KEYS[1], "parked", ARGV[1], "park_reason", ARGV[2])
  extended = 1
end
touch()
return state_reply(extended, 0)`

const gitHubQuotaUnparkScript = gitHubQuotaPrelude + `
local cleared = redis.call("HDEL", KEYS[1], "parked", "park_reason")
touch()
return state_reply(cleared, 0)`

// GitHubQuotaSnapshot atomically prunes expired reservations and returns the
// bucket state.
func (s *Store) GitHubQuotaSnapshot(ctx context.Context, bucket string) (GitHubQuotaState, error) {
	_, _, state, err := s.evalGitHubQuota(ctx, gitHubQuotaSnapshotScript, bucket)
	return state, err
}

// ObserveGitHubQuota atomically records an authoritative GitHub observation.
// A zero ObservedAt records the Redis clock so replicas share one time source.
// When releaseID is not empty the matching reservation is removed in the same
// atomic step so committed work is never counted twice or not at all.
func (s *Store) ObserveGitHubQuota(
	ctx context.Context, bucket string, observation GitHubQuotaObservation, releaseID string,
) (GitHubQuotaObserveOutcome, bool, GitHubQuotaState, error) {
	if observation.Limit < 0 || observation.Remaining < 0 ||
		observation.Limit > maxGitHubQuotaCost || observation.Remaining > maxGitHubQuotaCost {
		return 0, false, GitHubQuotaState{}, errors.New("github quota observation must be non-negative and bounded")
	}
	if observation.ResetAt.IsZero() || observation.ResetAt.UnixMilli() <= 0 {
		return 0, false, GitHubQuotaState{}, errors.New("github quota observation requires a reset time after the Unix epoch")
	}
	observedAt := int64(0)
	if !observation.ObservedAt.IsZero() {
		observedAt = observation.ObservedAt.UnixMilli()
		if observedAt <= 0 {
			return 0, false, GitHubQuotaState{}, errors.New("github quota observation time must be after the Unix epoch")
		}
	}
	if releaseID != "" && !gitHubQuotaIdentifier.MatchString(releaseID) {
		return 0, false, GitHubQuotaState{}, errors.New("github quota reservation ID is invalid")
	}
	code, released, state, err := s.evalGitHubQuota(
		ctx, gitHubQuotaObserveScript, bucket,
		strconv.FormatInt(observation.Limit, 10),
		strconv.FormatInt(observation.Remaining, 10),
		strconv.FormatInt(observation.ResetAt.UnixMilli(), 10),
		strconv.FormatInt(observedAt, 10),
		releaseID,
	)
	if err != nil {
		return 0, false, GitHubQuotaState{}, err
	}
	if code < 0 || code > 2 || released < 0 || released > 1 {
		return 0, false, GitHubQuotaState{}, errors.New("redis github quota observation returned an invalid response")
	}
	return GitHubQuotaObserveOutcome(code), released == 1, state, nil
}

// ReserveGitHubQuota atomically admits or denies a reservation of cost units
// that expires after ttl on the Redis clock.
func (s *Store) ReserveGitHubQuota(
	ctx context.Context, bucket, id string, cost, minimumRemain int64, ttl time.Duration,
) (GitHubQuotaAdmission, time.Time, GitHubQuotaState, error) {
	if !gitHubQuotaIdentifier.MatchString(id) {
		return 0, time.Time{}, GitHubQuotaState{}, errors.New("github quota reservation ID is invalid")
	}
	if cost <= 0 || cost > maxGitHubQuotaCost || minimumRemain < 0 || minimumRemain > maxGitHubQuotaCost {
		return 0, time.Time{}, GitHubQuotaState{}, errors.New("github quota reservation cost must be positive and minimum remain non-negative")
	}
	if ttl < time.Millisecond || ttl > maxGitHubQuotaTTL {
		return 0, time.Time{}, GitHubQuotaState{}, errors.New("github quota reservation TTL must be between one millisecond and 24 hours")
	}
	code, expires, state, err := s.evalGitHubQuota(
		ctx, gitHubQuotaReserveScript, bucket, id,
		strconv.FormatInt(cost, 10),
		strconv.FormatInt(minimumRemain, 10),
		strconv.FormatInt(ttl.Milliseconds(), 10),
	)
	if err != nil {
		return 0, time.Time{}, GitHubQuotaState{}, err
	}
	if code < int64(GitHubQuotaAdmitted) || code > int64(GitHubQuotaDuplicateReservation) ||
		(code == int64(GitHubQuotaAdmitted)) != (expires > 0) {
		return 0, time.Time{}, GitHubQuotaState{}, errors.New("redis github quota reservation returned an invalid response")
	}
	return GitHubQuotaAdmission(code), instantFromMillis(expires), state, nil
}

// ReleaseGitHubQuota atomically removes an unused reservation. It reports
// whether the reservation still existed.
func (s *Store) ReleaseGitHubQuota(ctx context.Context, bucket, id string) (bool, GitHubQuotaState, error) {
	if !gitHubQuotaIdentifier.MatchString(id) {
		return false, GitHubQuotaState{}, errors.New("github quota reservation ID is invalid")
	}
	code, _, state, err := s.evalGitHubQuota(ctx, gitHubQuotaReleaseScript, bucket, id)
	if err != nil {
		return false, GitHubQuotaState{}, err
	}
	return code == 1, state, nil
}

// ParkGitHubQuota atomically extends a bucket's parking without changing its
// recorded quota. It reports whether the parking was extended.
func (s *Store) ParkGitHubQuota(
	ctx context.Context, bucket string, until time.Time, reason string,
) (bool, GitHubQuotaState, error) {
	if until.IsZero() || until.UnixMilli() <= 0 {
		return false, GitHubQuotaState{}, errors.New("github quota parking requires an end time after the Unix epoch")
	}
	if len(reason) > maxGitHubQuotaParkReason {
		return false, GitHubQuotaState{}, errors.New("github quota parking reason is too long")
	}
	code, _, state, err := s.evalGitHubQuota(
		ctx, gitHubQuotaParkScript, bucket, strconv.FormatInt(until.UnixMilli(), 10), reason)
	if err != nil {
		return false, GitHubQuotaState{}, err
	}
	return code == 1, state, nil
}

// UnparkGitHubQuota atomically clears a bucket's parking.
func (s *Store) UnparkGitHubQuota(ctx context.Context, bucket string) (GitHubQuotaState, error) {
	_, _, state, err := s.evalGitHubQuota(ctx, gitHubQuotaUnparkScript, bucket)
	return state, err
}

func (s *Store) gitHubQuotaKeys(bucket string) []string {
	// The hash tag keeps every key of one bucket in the same cluster slot so
	// the scripts remain atomic on clustered Redis deployments.
	base := s.Key("github:quota:{" + bucket + "}")
	return []string{base + ":state", base + ":reservations", base + ":expiries"}
}

func (s *Store) evalGitHubQuota(
	ctx context.Context, script, bucket string, arguments ...string,
) (int64, int64, GitHubQuotaState, error) {
	if !gitHubQuotaIdentifier.MatchString(bucket) {
		return 0, 0, GitHubQuotaState{}, errors.New("github quota bucket key is invalid")
	}
	command := append([]string{"EVAL", script, "3"}, s.gitHubQuotaKeys(bucket)...)
	started := time.Now()
	value, err := s.Client.Do(ctx, append(command, arguments...)...)
	if err != nil {
		gitHubQuotaLog.Printf("quota script failed bucket=%s duration_ms=%d", bucket, time.Since(started).Milliseconds())
		return 0, 0, GitHubQuotaState{}, fmt.Errorf("update Redis github quota: %w", err)
	}
	code, extra, state, err := parseGitHubQuotaReply(value)
	if err != nil {
		gitHubQuotaLog.Printf("quota script returned an invalid reply bucket=%s", bucket)
		return 0, 0, GitHubQuotaState{}, err
	}
	gitHubQuotaLog.Printf("quota script completed bucket=%s code=%d known=%t remaining=%d reserved=%d duration_ms=%d",
		bucket, code, state.Known, state.Remaining, state.Reserved, time.Since(started).Milliseconds())
	return code, extra, state, nil
}

// parseGitHubQuotaReply decodes the shared quota script reply: a result code,
// an operation-specific value, and the bucket state. It is a pure function so
// the reply contract is testable without Redis.
func parseGitHubQuotaReply(value any) (int64, int64, GitHubQuotaState, error) {
	invalid := errors.New("redis github quota returned an invalid response")
	items, ok := value.([]any)
	if !ok || len(items) != 11 {
		return 0, 0, GitHubQuotaState{}, invalid
	}
	numbers := make([]int64, 10)
	for index := range numbers {
		parsed, err := strconv.ParseInt(fmt.Sprint(items[index]), 10, 64)
		if err != nil {
			return 0, 0, GitHubQuotaState{}, invalid
		}
		numbers[index] = parsed
	}
	for _, number := range numbers[2:9] {
		if number < 0 {
			return 0, 0, GitHubQuotaState{}, invalid
		}
	}
	if numbers[9] != 0 && numbers[9] != 1 || numbers[8] == 0 {
		return 0, 0, GitHubQuotaState{}, invalid
	}
	reason, ok := items[10].(string)
	if !ok {
		if items[10] != nil {
			return 0, 0, GitHubQuotaState{}, invalid
		}
		reason = ""
	}
	return numbers[0], numbers[1], GitHubQuotaState{
		Known:       numbers[9] == 1,
		Limit:       numbers[2],
		Remaining:   numbers[3],
		ResetAt:     instantFromMillis(numbers[4]),
		ObservedAt:  instantFromMillis(numbers[5]),
		ParkedUntil: instantFromMillis(numbers[6]),
		Reserved:    numbers[7],
		Now:         instantFromMillis(numbers[8]),
		ParkReason:  reason,
	}, nil
}

func instantFromMillis(milliseconds int64) time.Time {
	if milliseconds <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(milliseconds).UTC()
}
