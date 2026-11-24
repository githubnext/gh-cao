package org.cao.backend.ratelimit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.time.Instant;
import java.util.List;
import org.junit.jupiter.api.Test;

class TokenBucketTest {
    private static final Instant T0 = Instant.parse("2026-01-01T00:00:00Z");
    private static final Duration MINUTE = Duration.ofMinutes(1);

    @Test
    void firstRequestConsumesExactlyOneToken() {
        TokenBucket.Transition t = new TokenBucket(30, T0).advance(T0, 30, 1, MINUTE);
        assertTrue(t.result().allowed());
        assertEquals(29, t.result().remaining());
        assertEquals(29.0, t.bucket().tokens());
        assertEquals(Duration.ZERO, t.result().retryAfter());
    }

    @Test
    void exhaustedBucketDeniesWithPositiveRetryAndKeepsFraction() {
        TokenBucket b = new TokenBucket(0.25, T0);
        TokenBucket.Transition t = b.advance(T0, 30, 1, MINUTE);
        assertFalse(t.result().allowed());
        assertEquals(0.25, t.bucket().tokens(), 0.0, "an ordinary denial preserves the fraction");
        assertEquals(0, t.result().remaining());
        assertEquals(Duration.ofMillis(1500), t.result().retryAfter());
    }

    @Test
    void deniedMultiTokenChargeDrainsTheBucket() {
        TokenBucket.Transition t = new TokenBucket(3.5, T0).advance(T0, 30, 10, MINUTE);
        assertFalse(t.result().allowed());
        assertEquals(0.0, t.bucket().tokens());
        // Go computes the retry from the drained bucket, so the wait is a full cost/capacity share.
        assertEquals(Duration.ofMillis(20000), t.result().retryAfter());
    }

    @Test
    void refillIsContinuousAndCappedAtCapacity() {
        TokenBucket.Transition half = new TokenBucket(0, T0).advance(T0.plusSeconds(30), 30, 1, MINUTE);
        assertEquals(14, half.result().remaining());
        TokenBucket.Transition full = new TokenBucket(0, T0).advance(T0.plus(Duration.ofHours(1)), 30, 1, MINUTE);
        assertEquals(29, full.result().remaining());
    }

    @Test
    void backwardsClockCreatesNoTokensAndNeverMovesTheTimestampBack() {
        TokenBucket.Transition t = new TokenBucket(0, T0).advance(T0.minusSeconds(120), 10, 1, MINUTE);
        assertFalse(t.result().allowed());
        assertEquals(0.0, t.bucket().tokens());
        assertEquals(T0, t.bucket().at());
    }

    @Test
    void javaReproducesEveryGoVectorExactly() throws IOException {
        List<String> rows;
        try (InputStream in = TokenBucketTest.class.getResourceAsStream("go-token-bucket-vectors.tsv")) {
            rows = new String(in.readAllBytes(), StandardCharsets.UTF_8).lines()
                .filter(l -> !l.startsWith("#") && !l.isBlank()).toList();
        }
        assertTrue(rows.size() >= 80, "vector file must carry the generated corpus");
        for (String row : rows) {
            String[] f = row.split("\t", -1);
            TokenBucket in = org.cao.backend.operational.LimiterRecordAccess.decode(f[0]);
            Instant now = Instant.ofEpochMilli(Long.parseLong(f[1]));
            long capacity = Long.parseLong(f[2]);
            long cost = Long.parseLong(f[3]);
            Duration period = Duration.ofMillis(Long.parseLong(f[4]));
            TokenBucket.Transition t = in.advance(now, capacity, cost, period);
            assertEquals(f[5], org.cao.backend.operational.LimiterRecordAccess.encode(t.bucket()), row);
            assertEquals(Boolean.parseBoolean(f[6]), t.result().allowed(), row);
            assertEquals(Long.parseLong(f[7]), t.result().remaining(), row);
            assertEquals(Long.parseLong(f[8]), t.result().resetAfter().toMillis(), row);
            assertEquals(Long.parseLong(f[9]), t.result().retryAfter().toMillis(), row);
        }
    }
}
