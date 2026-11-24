package org.cao.backend.ratelimit;

import java.time.Duration;
import java.time.Instant;

/**
 * Continuously refilling token bucket (specs/server-rate-limiting.md §3). {@code tokens} keeps its
 * fraction, {@code at} never moves backwards, and a clock that moves backwards creates no tokens.
 * The arithmetic mirrors {@code advanceTokenBucket} in the Go adapter so a row written by either
 * implementation continues correctly in the other.
 */
public record TokenBucket(double tokens, Instant at) {

    /** Refills for the time elapsed since {@code at}, then spends {@code cost} if available. */
    public Transition advance(Instant now, long capacity, long cost, Duration period) {
        double millis = (double) period.toMillis();
        double elapsed = Math.max(0, (double) Duration.between(at, now).toMillis());
        double refilled = Math.min((double) capacity, tokens + elapsed * capacity / millis);
        boolean allowed = refilled >= cost;
        double left = refilled;
        if (allowed) {
            left -= cost;
        } else if (cost > 1) {
            left = 0;
        }
        long reset = (long) Math.ceil((capacity - left) * millis / capacity);
        long retry = allowed ? 0 : (long) Math.ceil((cost - left) * millis / capacity);
        RateLimitResult result = new RateLimitResult(allowed, (long) Math.floor(left),
            Duration.ofMillis(retry), Duration.ofMillis(reset));
        return new Transition(new TokenBucket(left, now.isAfter(at) ? now : at), result);
    }

    /** Updated bucket and the decision for the request that produced it. */
    public record Transition(TokenBucket bucket, RateLimitResult result) { }
}
