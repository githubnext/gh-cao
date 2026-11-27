package redisx

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

var rateLimitLog = logger.New("cao:redis:ratelimit")

// RateLimitResult describes the state of a token bucket after one attempted
// token consumption.
type RateLimitResult = operational.RateLimitResult

// TakeRateLimitToken atomically consumes one token from a Redis-backed token
// bucket. Redis TIME is used so every server replica observes the same clock.
func (s *Store) TakeRateLimitToken(
	ctx context.Context,
	key string,
	capacity int,
	refillPeriod time.Duration,
) (RateLimitResult, error) {
	return s.TakeRateLimitTokens(ctx, key, capacity, refillPeriod, 1)
}

// TakeRateLimitTokens atomically consumes a weighted cost from a Redis-backed
// token bucket. A denied weighted charge drains the available tokens so an
// expensive request cannot repeatedly avoid its assessed cost.
func (s *Store) TakeRateLimitTokens(
	ctx context.Context,
	key string,
	capacity int,
	refillPeriod time.Duration,
	cost int,
) (RateLimitResult, error) {
	if err := collectionName(key); err != nil {
		return RateLimitResult{}, err
	}
	// The bounds match the memory and PostgreSQL adapters.
	if capacity <= 0 || int64(capacity) > maxRateLimitValue || cost <= 0 || cost > capacity ||
		refillPeriod < time.Millisecond || refillPeriod > time.Duration(math.MaxInt64/2) {
		return RateLimitResult{}, fmt.Errorf("rate limit capacity and cost must be positive and bounded, cost must not exceed capacity, and refill period must be between one millisecond and the supported maximum: %w", ErrInvalid)
	}
	script := `
local current = redis.call("TIME")
local now = current[1] * 1000 + math.floor(current[2] / 1000)
local state = redis.call("HMGET", KEYS[1], "tokens", "updated")
local tokens = tonumber(state[1]) or tonumber(ARGV[1])
local updated = tonumber(state[2]) or now
local elapsed = math.max(0, now - updated)
local capacity = tonumber(ARGV[1])
local period = tonumber(ARGV[2])
local cost = tonumber(ARGV[3])
tokens = math.min(capacity, tokens + elapsed * capacity / period)
local allowed = 0
if tokens >= cost then
  tokens = tokens - cost
  allowed = 1
elseif cost > 1 then
  tokens = 0
end
local retry = 0
if allowed == 0 then
  retry = math.ceil((cost - tokens) * period / capacity)
end
local reset = math.ceil((capacity - tokens) * period / capacity)
redis.call("HSET", KEYS[1], "tokens", tokens, "updated", now)
redis.call("PEXPIRE", KEYS[1], math.ceil(period * 2))
return {allowed, math.floor(tokens), retry, reset}`
	value, err := s.Client.Do(
		ctx,
		"EVAL",
		script,
		"1",
		s.Key("rate-limit:"+key),
		strconv.Itoa(capacity),
		strconv.FormatInt(refillPeriod.Milliseconds(), 10),
		strconv.Itoa(cost),
	)
	if err != nil {
		return RateLimitResult{}, fmt.Errorf("update Redis rate limit: %w", err)
	}
	result, err := parseRateLimitReply(value, capacity, refillPeriod)
	if err != nil {
		return RateLimitResult{}, err
	}
	if !result.Allowed {
		rateLimitLog.Printf("rate limit denied remaining=%d retry_after_ms=%d", result.Remaining, result.RetryAfter.Milliseconds())
	}
	return result, nil
}

// parseRateLimitReply decodes the four-element EVAL reply the rate-limit
// script returns (allowed, remaining tokens, retry-after, reset-after) into a
// RateLimitResult, rejecting a reply that is malformed or out of the bounds
// capacity and refillPeriod allow. It is a pure function so the script's
// reply contract is testable without a real or fake Redis client.
func parseRateLimitReply(value any, capacity int, refillPeriod time.Duration) (RateLimitResult, error) {
	items, ok := value.([]any)
	if !ok || len(items) != 4 {
		return RateLimitResult{}, errors.New("redis rate limit returned an invalid response")
	}
	parsed := make([]int64, len(items))
	for index, item := range items {
		value, err := strconv.ParseInt(fmt.Sprint(item), 10, 64)
		if err != nil {
			return RateLimitResult{}, errors.New("redis rate limit returned an invalid response")
		}
		parsed[index] = value
	}
	if parsed[0] != 0 && parsed[0] != 1 {
		return RateLimitResult{}, errors.New("redis rate limit returned an invalid response")
	}
	maximumDuration := refillPeriod.Milliseconds()
	if parsed[1] < 0 || parsed[1] > int64(capacity) ||
		parsed[2] < 0 || parsed[2] > maximumDuration ||
		parsed[3] < 0 || parsed[3] > maximumDuration {
		return RateLimitResult{}, errors.New("redis rate limit returned an invalid response")
	}
	return RateLimitResult{
		Allowed:    parsed[0] == 1,
		Remaining:  parsed[1],
		RetryAfter: time.Duration(parsed[2]) * time.Millisecond,
		ResetAfter: time.Duration(parsed[3]) * time.Millisecond,
	}, nil
}
