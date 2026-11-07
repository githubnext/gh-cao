package memory

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

var limiterLog = logger.New("cao:operational:memory:limiter")

type tokenBucket struct {
	tokens           float64
	updated, expires time.Time
}

// tokenBucketRejection identifies which precondition of
// TakeRateLimitTokens rejected a request, so a misbehaving caller is
// diagnosable without logging the subject key, limit, cost, or period
// themselves.
type tokenBucketRejection string

const (
	tokenBucketAccepted          tokenBucketRejection = "accepted"
	tokenBucketRejectedParameter tokenBucketRejection = "invalid-parameter"
	tokenBucketRejectedSubjects  tokenBucketRejection = "subject-capacity"
)

// classifyTokenBucketRequest reports which precondition, if any, rejects a
// token-bucket request before any bucket state is read or mutated. It is a
// pure function extracted from TakeRateLimitTokens so the capacity, cost,
// and period validation is independently testable against constructed
// values, without a Store or mutex.
func classifyTokenBucketRequest(limit int, period time.Duration, cost int, exists bool, bucketCount, maxSubjects int) tokenBucketRejection {
	if limit <= 0 || int64(limit) > maxQuotaValue || cost <= 0 || cost > limit ||
		period < time.Millisecond || period > time.Duration(math.MaxInt64/2) {
		return tokenBucketRejectedParameter
	}
	if !exists && bucketCount >= maxSubjects {
		return tokenBucketRejectedSubjects
	}
	return tokenBucketAccepted
}

func (s *Store) TakeRateLimitToken(ctx context.Context, key string, capacity int, period time.Duration) (operational.RateLimitResult, error) {
	return s.TakeRateLimitTokens(ctx, key, capacity, period, 1)
}

func (s *Store) TakeRateLimitTokens(ctx context.Context, key string, limit int, period time.Duration, cost int) (operational.RateLimitResult, error) {
	if err := s.enter(ctx); err != nil {
		return operational.RateLimitResult{}, err
	}
	defer s.mu.Unlock()
	if err := s.name(key); err != nil {
		return operational.RateLimitResult{}, err
	}
	now := s.config.Clock().Truncate(time.Millisecond)
	s.prune(now)
	v, exists := s.buckets[key]
	switch rejection := classifyTokenBucketRequest(limit, period, cost, exists, len(s.buckets), s.config.MaxRateLimitSubjects); rejection {
	case tokenBucketRejectedParameter:
		limiterLog.Printf("token bucket request rejected reason=%s", rejection)
		return operational.RateLimitResult{}, invalid("invalid token bucket capacity, cost, or refill period")
	case tokenBucketRejectedSubjects:
		limiterLog.Printf("token bucket request rejected reason=%s", rejection)
		return operational.RateLimitResult{}, capacity()
	case tokenBucketAccepted:
		break
	}
	if !exists {
		if err := s.reserve(map[recordKey]int64{{"bucket", key, ""}: charge(key)}); err != nil {
			return operational.RateLimitResult{}, err
		}
		v = tokenBucket{tokens: float64(limit), updated: now}
	}
	elapsed := math.Max(0, float64(now.Sub(v.updated).Milliseconds()))
	millis := float64(period.Milliseconds())
	v.tokens = math.Min(float64(limit), v.tokens+elapsed*float64(limit)/millis)
	allowed := v.tokens >= float64(cost)
	if allowed {
		v.tokens -= float64(cost)
	} else if cost > 1 {
		v.tokens = 0
	}
	result := operational.RateLimitResult{Allowed: allowed, Remaining: int64(math.Floor(v.tokens)),
		ResetAfter: time.Duration(math.Ceil((float64(limit)-v.tokens)*millis/float64(limit))) * time.Millisecond}
	if !allowed {
		result.RetryAfter = time.Duration(math.Ceil((float64(cost)-v.tokens)*millis/float64(limit))) * time.Millisecond
	}
	// Like the PostgreSQL adapter, never move the refill timestamp backwards.
	if now.After(v.updated) {
		v.updated = now
	}
	v.expires = now.Add(time.Duration(period.Milliseconds()*2) * time.Millisecond)
	s.buckets[strings.Clone(key)] = v
	return result, nil
}
