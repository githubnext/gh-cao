package org.cao.backend.ratelimit;

import java.time.Duration;

/**
 * Domain-owned contract for distributed request limiting. An implementation MUST update a bucket
 * with one atomic storage operation under the storage clock and MUST throw, never return an allowed
 * result, when it cannot enforce the limit within {@code deadline}.
 */
public interface RequestLimiter {

    /** Largest accepted capacity, matching the Go adapter's quota bound. */
    long MAX_CAPACITY = 1L << 40;

    /** Takes {@code cost} tokens from the named bucket; a denied multi-token request drains it. */
    RateLimitResult take(String name, long capacity, long cost, Duration period, Duration deadline);

    /** Rejects arguments no bucket can honor, with the same bounds as the Go adapter. */
    static void validate(String name, long capacity, long cost, Duration period, Duration deadline) {
        if (name == null || name.isBlank() || name.length() > 1024 || name.indexOf('\0') >= 0) {
            throw new IllegalArgumentException("empty, oversized, or NUL-containing name");
        }
        if (capacity <= 0 || capacity > MAX_CAPACITY || cost <= 0 || cost > capacity
            || period == null || period.compareTo(Duration.ofMillis(1)) < 0
            || period.compareTo(Duration.ofNanos(Long.MAX_VALUE / 2)) > 0) {
            throw new IllegalArgumentException("invalid capacity, cost, or refill period");
        }
        if (deadline == null || deadline.compareTo(Duration.ofMillis(1)) < 0) {
            throw new IllegalArgumentException("a positive deadline is required");
        }
    }
}
