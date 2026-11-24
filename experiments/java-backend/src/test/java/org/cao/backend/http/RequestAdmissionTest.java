package org.cao.backend.http;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.net.InetAddress;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicInteger;
import org.cao.backend.operational.OperationalStoreException;
import org.cao.backend.ratelimit.RateLimitConfig;
import org.cao.backend.ratelimit.RateLimitResult;
import org.cao.backend.ratelimit.RequestLimiter;
import org.cao.backend.ratelimit.TokenBucket;
import org.junit.jupiter.api.Test;

/**
 * Admission semantics with an explicitly selected in-memory test limiter that applies the shared
 * {@link TokenBucket} rules. It exercises the contract only; the PostgreSQL adapter is verified
 * separately against a real database.
 */
class RequestAdmissionTest {
    private static final ProxyPolicy POLICY = new ProxyPolicy(List.of("cao.example.com"), true, true,
        List.of(ProxyPolicy.Cidr.parse("10.0.0.0/8")));

    /** Records every key it is asked about; never persists anything. */
    private static final class MemoryLimiter implements RequestLimiter {
        final java.util.Map<String, TokenBucket> buckets = new java.util.HashMap<>();
        final List<String> keys = new ArrayList<>();
        final AtomicInteger calls = new AtomicInteger();
        java.time.Instant now = java.time.Instant.parse("2026-01-01T00:00:00Z");
        boolean failing;

        @Override
        public RateLimitResult take(String name, long capacity, long cost, Duration period,
            Duration deadline) {
            calls.incrementAndGet();
            keys.add(name);
            if (failing) {
                throw new OperationalStoreException("down");
            }
            TokenBucket bucket = buckets.getOrDefault(name, new TokenBucket(capacity, now));
            TokenBucket.Transition t = bucket.advance(now, capacity, cost, period);
            buckets.put(name, t.bucket());
            return t.result();
        }
    }

    private static InetAddress ip(String text) {
        return IpLiteral.parseWithOptionalPort(text).orElseThrow();
    }

    private static RequestAdmission.Request request(String method, String path, String forwardedFor) {
        return new RequestAdmission.Request(method, path, "cao.example.com", false, ip("10.0.0.5"),
            forwardedFor == null ? List.of() : List.of(forwardedFor), List.of(), List.of(),
            List.of("https"));
    }

    private static RequestAdmission hosted(MemoryLimiter limiter, int edge, int probe) {
        RateLimitConfig config = RateLimitConfig.defaults()
            .with(new RateLimitConfig.Policy("edge", edge, Duration.ofMinutes(1)))
            .with(new RateLimitConfig.Policy("public", probe, Duration.ofMinutes(1)));
        return RequestAdmission.hosted(POLICY, limiter, config);
    }

    @Test
    void defaultsMatchTheNormativeTable() {
        RateLimitConfig d = RateLimitConfig.defaults();
        assertEquals(1200, d.edge().capacity());
        assertEquals("1200;w=60", d.edge().headerPolicy());
        assertEquals(600, d.publicProbe().capacity());
        assertEquals("600;w=60", d.publicProbe().headerPolicy());
    }

    @Test
    void probesUseTheSharedPerClientPublicBucketAcrossPathsAndHead() {
        MemoryLimiter limiter = new MemoryLimiter();
        RequestAdmission admission = hosted(limiter, 1200, 3);
        assertTrue(admission.admit(request("GET", "/api/health", "203.0.113.9")).proceed());
        assertTrue(admission.admit(request("HEAD", "/api/readiness", "203.0.113.9")).proceed());
        RequestAdmission.Decision third = admission.admit(request("GET", "/api/health", "203.0.113.9"));
        assertTrue(third.proceed());
        assertEquals("0", third.headers().get("RateLimit-Remaining"));
        RequestAdmission.Decision fourth = admission.admit(request("GET", "/api/readiness", "203.0.113.9"));
        assertFalse(fourth.proceed());
        assertEquals(429, fourth.status());
        assertEquals(1, limiter.keys.stream().distinct().count(), "one key shared across probe paths");
        assertTrue(limiter.keys.get(0).startsWith("public:"));
        // A different client has its own bucket.
        assertTrue(admission.admit(request("GET", "/api/health", "203.0.113.10")).proceed());
    }

    @Test
    void exhaustedBucketReturns429WithEveryRequiredHeaderAndPositiveRetryAfter() {
        MemoryLimiter limiter = new MemoryLimiter();
        RequestAdmission admission = hosted(limiter, 2, 600);
        admission.admit(request("GET", "/dashboard", "203.0.113.9"));
        admission.admit(request("GET", "/dashboard", "203.0.113.9"));
        RequestAdmission.Decision denied = admission.admit(request("GET", "/dashboard", "203.0.113.9"));
        assertEquals(429, denied.status());
        assertFalse(denied.proceed());
        Map<String, String> h = denied.headers();
        assertEquals("2", h.get("RateLimit-Limit"));
        assertEquals("0", h.get("RateLimit-Remaining"));
        assertEquals("2;w=60", h.get("RateLimit-Policy"));
        assertTrue(Long.parseLong(h.get("RateLimit-Reset")) >= 1);
        assertEquals("30", h.get("Retry-After"));
    }

    @Test
    void allowedResponsesCarryTheFourHeadersAndNoRetryAfter() {
        RequestAdmission.Decision ok = hosted(new MemoryLimiter(), 1200, 600)
            .admit(request("GET", "/", "203.0.113.9"));
        assertTrue(ok.proceed());
        assertEquals("1200", ok.headers().get("RateLimit-Limit"));
        assertEquals("1199", ok.headers().get("RateLimit-Remaining"));
        assertEquals("1200;w=60", ok.headers().get("RateLimit-Policy"));
        assertNull(ok.headers().get("Retry-After"));
        assertTrue(ok.headers().containsKey("RateLimit-Reset"));
    }

    @Test
    void invalidBoundaryIsRejectedWithoutConsultingTheLimiter() {
        MemoryLimiter limiter = new MemoryLimiter();
        RequestAdmission admission = hosted(limiter, 1200, 600);
        RequestAdmission.Request badHost = new RequestAdmission.Request("GET", "/api/health",
            "cao.example.com", false, ip("10.0.0.5"), List.of(), List.of(), List.of("evil.example"),
            List.of("https"));
        assertEquals(421, admission.admit(badHost).status());
        RequestAdmission.Request insecure = new RequestAdmission.Request("GET", "/", "cao.example.com",
            false, ip("10.0.0.5"), List.of(), List.of(), List.of("cao.example.com"), List.of("http"));
        assertEquals(421, admission.admit(insecure).status());
        RequestAdmission.Request untrusted = new RequestAdmission.Request("GET", "/", "cao.example.com",
            true, ip("192.0.2.7"), List.of(), List.of(), List.of(), List.of("https"));
        assertEquals(421, admission.admit(untrusted).status());
        assertEquals(0, limiter.calls.get());
    }

    @Test
    void storageFailureFailsClosedWith503AndLeaksNothing() {
        MemoryLimiter limiter = new MemoryLimiter();
        limiter.failing = true;
        RequestAdmission.Decision d = hosted(limiter, 1200, 600).admit(request("GET", "/api/health", null));
        assertEquals(503, d.status());
        assertFalse(d.proceed());
        assertTrue(d.headers().isEmpty());
        assertEquals("request rate limiter is unavailable", d.message());
    }

    @Test
    void bucketKeysNeverContainTheClientAddress() {
        MemoryLimiter limiter = new MemoryLimiter();
        hosted(limiter, 1200, 600).admit(request("GET", "/", "203.0.113.9"));
        String key = limiter.keys.get(0);
        assertTrue(key.matches("edge:[0-9a-f]{64}"), key);
        assertFalse(key.contains("203"));
        assertEquals(RequestAdmission.sha256Hex("client:203.0.113.9"), key.substring("edge:".length()));
    }

    @Test
    void probeWritesAreNotProbeMeteredAndOtherPathsUseTheEdgeBucket() {
        MemoryLimiter limiter = new MemoryLimiter();
        RequestAdmission admission = hosted(limiter, 1200, 600);
        assertTrue(admission.admit(request("POST", "/api/health", null)).proceed());
        assertEquals(0, limiter.calls.get(), "a POST to a probe path is rejected later by the route, unmetered");
        admission.admit(request("GET", "/api/v1/health", "203.0.113.9"));
        assertTrue(limiter.keys.get(0).startsWith("public:"), "the v1 alias shares the probe bucket");
        admission.admit(request("GET", "/api/v1/ingestion/health", "203.0.113.9"));
        assertTrue(limiter.keys.get(1).startsWith("edge:"), "other paths use the edge bucket");
    }

    @Test
    void localProfileIsUnmeteredAndHostedRequiresALimiterAndPolicy() {
        RequestAdmission local = RequestAdmission.local();
        assertFalse(local.enforcing());
        assertTrue(local.admit(request("GET", "/api/health", null)).proceed());
        assertThrows(IllegalArgumentException.class,
            () -> RequestAdmission.hosted(POLICY, null, RateLimitConfig.defaults()));
        assertThrows(IllegalArgumentException.class,
            () -> RequestAdmission.hosted(null, new MemoryLimiter(), RateLimitConfig.defaults()));
    }

    @Test
    void cooldownRoundsUpAndNeverBelowOneSecond() {
        assertEquals(1, RequestAdmission.cooldownSeconds(Duration.ZERO));
        assertEquals(1, RequestAdmission.cooldownSeconds(Duration.ofMillis(1)));
        assertEquals(1, RequestAdmission.cooldownSeconds(Duration.ofMillis(1000)));
        assertEquals(2, RequestAdmission.cooldownSeconds(Duration.ofMillis(1001)));
    }
}
