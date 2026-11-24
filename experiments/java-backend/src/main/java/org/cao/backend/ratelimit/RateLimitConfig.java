package org.cao.backend.ratelimit;

import java.time.Duration;

/**
 * The hosted inbound request classes of specs/server-rate-limiting.md §2 that this implementation
 * can enforce today: the pre-session edge bucket and the public probe bucket. The authenticated
 * query, OAuth and general classes are deferred until authentication exists. Defaults are the
 * normative capacities and refill periods; {@link #with} exists for deterministic tests.
 */
public record RateLimitConfig(Policy edge, Policy publicProbe) {

    /** One token bucket class. */
    public record Policy(String name, int capacity, Duration window) {
        public Policy {
            if (name == null || name.isBlank() || capacity <= 0
                || window.compareTo(Duration.ofMillis(1)) < 0) {
                throw new IllegalArgumentException("a rate limit needs a name, capacity and window");
            }
        }

        /** Header form {@code <capacity>;w=<seconds>}. */
        public String headerPolicy() {
            return capacity + ";w=" + window.toSeconds();
        }
    }

    public static RateLimitConfig defaults() {
        return new RateLimitConfig(new Policy("edge", 1200, Duration.ofMinutes(1)),
            new Policy("public", 600, Duration.ofMinutes(1)));
    }

    /** Returns a copy with one class replaced. */
    public RateLimitConfig with(Policy replacement) {
        return switch (replacement.name()) {
            case "edge" -> new RateLimitConfig(replacement, publicProbe);
            case "public" -> new RateLimitConfig(edge, replacement);
            default -> throw new IllegalArgumentException("unsupported rate limit class");
        };
    }
}
