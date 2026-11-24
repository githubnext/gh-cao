package org.cao.backend.ratelimit;

import java.time.Duration;

/**
 * Outcome of one atomic token-bucket transition (specs/server-rate-limiting.md §3): the remaining
 * whole-token count and the durations until a retry is possible and until the bucket is full.
 */
public record RateLimitResult(boolean allowed, long remaining, Duration retryAfter, Duration resetAfter) { }
