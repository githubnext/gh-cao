package githubquota

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SecondaryRateLimitBackoff is the minimum wait GitHub recommends after a
// secondary rate limit that does not state a Retry-After.
const SecondaryRateLimitBackoff = time.Minute

// Parking reasons recorded from GitHub responses.
const (
	ParkReasonRetryAfter         = "retry-after"
	ParkReasonSecondaryRateLimit = "secondary-rate-limit"
)

// ResponseQuota is the quota metadata carried by one GitHub API response.
type ResponseQuota struct {
	// HasResponse distinguishes an API response without quota headers from a
	// transport failure where the request outcome is unknown.
	HasResponse bool
	// Resource is the rate-limit resource the response was metered against.
	Resource string
	// Observation is valid when HasObservation is true.
	Observation    Observation
	HasObservation bool
	// ParkUntil is non-zero when the response asks callers to back off.
	ParkUntil  time.Time
	ParkReason string
}

// ParseResponse extracts authoritative quota metadata from a GitHub API
// response's status and headers. Only rate-limit headers are read.
func ParseResponse(header http.Header, statusCode int, now time.Time) ResponseQuota {
	result := ResponseQuota{HasResponse: true, Resource: ResourceCore}
	if resource := strings.ToLower(strings.TrimSpace(header.Get("X-RateLimit-Resource"))); identifierPattern.MatchString(resource) {
		result.Resource = resource
	}
	limit, limitErr := strconv.Atoi(strings.TrimSpace(header.Get("X-RateLimit-Limit")))
	remaining, remainingErr := strconv.Atoi(strings.TrimSpace(header.Get("X-RateLimit-Remaining")))
	reset, resetErr := strconv.ParseInt(strings.TrimSpace(header.Get("X-RateLimit-Reset")), 10, 64)
	if limitErr == nil && remainingErr == nil && resetErr == nil &&
		limit >= 0 && remaining >= 0 && remaining <= limit && reset > 0 {
		result.HasObservation = true
		result.Observation = Observation{
			Limit:      limit,
			Remaining:  remaining,
			ResetAt:    time.Unix(reset, 0).UTC(),
			ObservedAt: now,
		}
	}
	if statusCode < http.StatusBadRequest {
		return result
	}
	retryAfter, hasRetryAfter := parseRetryAfter(header.Get("Retry-After"), now)
	result.ParkUntil, result.ParkReason = parkDecision(
		statusCode, retryAfter, hasRetryAfter, result.HasObservation, result.Observation.Remaining, now)
	if result.ParkReason != "" {
		quotaLog.Printf("response parked status=%d reason=%s", statusCode, result.ParkReason)
	}
	return result
}

// parkDecision classifies one error-status GitHub response into a parking
// instruction, applying the same Retry-After-versus-secondary-rate-limit
// precedence ParseResponse previously computed inline. It is a pure function
// extracted from ParseResponse so each precedence rule is independently
// testable without constructing an http.Header.
//
// A 429 without Retry-After and with primary quota left is a secondary rate
// limit. A 403 alone is ambiguous with authorization failures, so it parks
// only when it carries Retry-After.
func parkDecision(
	statusCode int, retryAfter time.Time, hasRetryAfter, hasObservation bool, remaining int, now time.Time,
) (time.Time, string) {
	if hasRetryAfter {
		reason := ParkReasonRetryAfter
		if statusCode == http.StatusForbidden || statusCode == http.StatusTooManyRequests {
			reason = ParkReasonSecondaryRateLimit
		}
		return retryAfter, reason
	}
	if statusCode == http.StatusTooManyRequests && (!hasObservation || remaining > 0) {
		return now.Add(SecondaryRateLimitBackoff), ParkReasonSecondaryRateLimit
	}
	return time.Time{}, ""
}

func parseRetryAfter(value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 || seconds > int64((24*time.Hour)/time.Second) {
			return time.Time{}, false
		}
		return now.Add(time.Duration(seconds) * time.Second), true
	}
	if instant, err := http.ParseTime(value); err == nil {
		if !instant.After(now) {
			return now, true
		}
		if instant.After(now.Add(24 * time.Hour)) {
			return time.Time{}, false
		}
		return instant, true
	}
	return time.Time{}, false
}

// RecordResponse records one GitHub response's quota metadata against the
// bucket (app, installation, response resource): the authoritative
// observation when present, and parking when the response requests backoff.
func (s *Service) RecordResponse(
	ctx context.Context, app string, installation int64, header http.Header, statusCode int,
) (ResponseQuota, error) {
	quota := ParseResponse(header, statusCode, time.Now())
	bucket := BucketID{App: app, Installation: installation, Resource: quota.Resource}
	quotaLog.Printf("recording response bucket=%s status=%d observation=%t park_reason=%q",
		bucket.Normalize(), statusCode, quota.HasObservation, quota.ParkReason)
	if quota.HasObservation {
		// Record the shared operational storage clock rather than this replica's clock.
		observation := quota.Observation
		observation.ObservedAt = time.Time{}
		if err := s.Observe(ctx, bucket, observation); err != nil {
			return quota, err
		}
	}
	if !quota.ParkUntil.IsZero() {
		if err := s.Park(ctx, bucket, quota.ParkUntil, quota.ParkReason); err != nil {
			return quota, err
		}
	}
	return quota, nil
}
