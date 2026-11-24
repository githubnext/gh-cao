package org.cao.backend.operational;

import org.cao.backend.ratelimit.TokenBucket;

/** Test-only access to the package-private Go-compatible limiter record codec. */
public final class LimiterRecordAccess {
    private LimiterRecordAccess() { }

    public static TokenBucket decode(String json) {
        return LimiterRecord.decode(json.getBytes(java.nio.charset.StandardCharsets.UTF_8));
    }

    public static String encode(TokenBucket bucket) {
        return new String(LimiterRecord.encode(bucket), java.nio.charset.StandardCharsets.UTF_8);
    }
}
