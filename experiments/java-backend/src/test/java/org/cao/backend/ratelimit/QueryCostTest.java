package org.cao.backend.ratelimit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.time.Duration;
import org.junit.jupiter.api.Test;

class QueryCostTest {
    private static final long MIB = 1L << 20;

    @Test
    void cheapQueriesCostOne() {
        assertEquals(1, QueryCost.of(0, 0, 0, 0, 30));
        assertEquals(1, QueryCost.of(1, 1, 1, 1, 30));
        assertEquals(1, QueryCost.of(1000, 250_000, 100_000, 16 * MIB, 30));
    }

    @Test
    void eachFactorRoundsUpInItsOwnUnit() {
        assertEquals(2, QueryCost.of(1001, 0, 0, 0, 30));
        assertEquals(2, QueryCost.of(0, 250_001, 0, 0, 30));
        assertEquals(2, QueryCost.of(0, 0, 100_001, 0, 30));
        assertEquals(2, QueryCost.of(0, 0, 0, 16 * MIB + 1, 30));
    }

    @Test
    void theGreatestFactorWins() {
        assertEquals(7, QueryCost.of(3000, 1_500_001, 200_000, 17 * MIB, 30));
        assertEquals(12, QueryCost.of(3000, 250_000, 1_100_001, 17 * MIB, 30));
    }

    @Test
    void costIsCappedAtTheBucketCapacityAndIgnoresNegativeMeasurements() {
        assertEquals(30, QueryCost.of(10_000_000, 0, 0, 0, 30));
        assertEquals(30, QueryCost.of(0, Long.MAX_VALUE, 0, 0, 30));
        assertEquals(1, QueryCost.of(-5, -5, -5, -5, 30));
    }

    @Test
    void limiterArgumentsAreValidatedLikeTheGoAdapter() {
        Duration p = Duration.ofMinutes(1);
        Duration d = Duration.ofSeconds(1);
        RequestLimiter.validate("k", 30, 1, p, d);
        assertThrows(IllegalArgumentException.class, () -> RequestLimiter.validate("", 30, 1, p, d));
        assertThrows(IllegalArgumentException.class, () -> RequestLimiter.validate("a\0b", 30, 1, p, d));
        assertThrows(IllegalArgumentException.class, () -> RequestLimiter.validate("k", 0, 1, p, d));
        assertThrows(IllegalArgumentException.class, () -> RequestLimiter.validate("k", 30, 0, p, d));
        assertThrows(IllegalArgumentException.class, () -> RequestLimiter.validate("k", 30, 31, p, d));
        assertThrows(IllegalArgumentException.class,
            () -> RequestLimiter.validate("k", 30, 1, Duration.ofNanos(999_999), d));
        assertThrows(IllegalArgumentException.class,
            () -> RequestLimiter.validate("k", (1L << 40) + 1, 1, p, d));
        assertThrows(IllegalArgumentException.class,
            () -> RequestLimiter.validate("k".repeat(1025), 30, 1, p, d));
        assertThrows(IllegalArgumentException.class,
            () -> RequestLimiter.validate("k", 30, 1, p, Duration.ZERO));
    }
}
