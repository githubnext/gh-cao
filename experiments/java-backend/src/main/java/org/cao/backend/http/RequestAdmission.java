package org.cao.backend.http;

import java.net.InetAddress;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Duration;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import org.cao.backend.ratelimit.RateLimitConfig;
import org.cao.backend.ratelimit.RateLimitResult;
import org.cao.backend.ratelimit.RequestLimiter;

/**
 * Pre-handler admission for inbound requests (specs/server-rate-limiting.md §§2, 4, 5). A hosted
 * profile validates the proxy boundary without touching the limiter, meters public probes in the
 * shared per-client "public" bucket and everything else in the per-client "edge" bucket, and fails
 * closed with 503 when storage cannot enforce a limit in time. The loopback local profile is
 * unmetered, which the specification permits only for a listener that admits just the local operator.
 */
public final class RequestAdmission {
    /** Storage deadline; shorter than any server request timeout (§5). */
    static final Duration LIMITER_DEADLINE = Duration.ofSeconds(2);
    private static final System.Logger LOG = System.getLogger("cao.ratelimit");

    /** The parts of a request that admission may see; credentials and bodies never reach it. */
    public record Request(String method, String path, String host, boolean tls, InetAddress peer,
        List<String> forwardedFor, List<String> forwarded, List<String> forwardedHost,
        List<String> forwardedProto) { }

    /** Either proceed (with headers to attach) or answer immediately. */
    public record Decision(boolean proceed, int status, String message, Map<String, String> headers) {
        static Decision proceed(Map<String, String> headers) {
            return new Decision(true, 200, "", headers);
        }

        static Decision reject(int status, String message, Map<String, String> headers) {
            return new Decision(false, status, message, headers);
        }
    }

    private final ProxyPolicy proxy;
    private final RequestLimiter limiter;
    private final RateLimitConfig limits;

    private RequestAdmission(ProxyPolicy proxy, RequestLimiter limiter, RateLimitConfig limits) {
        this.proxy = proxy;
        this.limiter = limiter;
        this.limits = limits;
    }

    /** Local development: loopback only, nothing metered, no forwarding header ever trusted. */
    public static RequestAdmission local() {
        return new RequestAdmission(null, null, null);
    }

    /** Hosted profile with a distributed limiter, which is mandatory. */
    public static RequestAdmission hosted(ProxyPolicy proxy, RequestLimiter limiter,
        RateLimitConfig limits) {
        if (proxy == null || limiter == null || limits == null) {
            throw new IllegalArgumentException("hosted mode requires a proxy policy and a request limiter");
        }
        return new RequestAdmission(proxy, limiter, limits);
    }

    /** Whether request limits are enforced, reported as the {@code requestLimits} capability. */
    public boolean enforcing() {
        return limiter != null;
    }

    public Decision admit(Request request) {
        if (!enforcing()) {
            return Decision.proceed(Map.of());
        }
        ProxyPolicy.Rejection rejection = proxy.classify(request.host(), request.tls(),
            request.forwardedHost(), request.forwardedProto(), request.peer());
        if (rejection != ProxyPolicy.Rejection.NONE) {
            LOG.log(System.Logger.Level.INFO, "proxy request rejected reason=" + rejection);
            return Decision.reject(421, "invalid forwarded request host", Map.of());
        }
        boolean probe = isProbePath(request.path());
        boolean read = "GET".equals(request.method()) || "HEAD".equals(request.method());
        if (probe && !read) {
            return Decision.proceed(Map.of());
        }
        RateLimitConfig.Policy policy = probe ? limits.publicProbe() : limits.edge();
        ClientAddress.Resolved client = ClientAddress.resolve(proxy, request.peer(),
            request.forwardedFor(), request.forwarded());
        return enforce(policy, "client:" + client.address());
    }

    static boolean isProbePath(String path) {
        return "/api/health".equals(path) || "/api/v1/health".equals(path) || "/api/readiness".equals(path);
    }

    private Decision enforce(RateLimitConfig.Policy policy, String subject) {
        String key = policy.name() + ":" + sha256Hex(subject);
        RateLimitResult result;
        try {
            result = limiter.take(key, policy.capacity(), 1, policy.window(), LIMITER_DEADLINE);
        } catch (RuntimeException e) {
            LOG.log(System.Logger.Level.WARNING, "rate limit unavailable policy=" + policy.name());
            return Decision.reject(503, "request rate limiter is unavailable", Map.of());
        }
        Map<String, String> headers = new LinkedHashMap<>();
        headers.put("RateLimit-Limit", Integer.toString(policy.capacity()));
        headers.put("RateLimit-Remaining", Long.toString(result.remaining()));
        headers.put("RateLimit-Reset", Long.toString(cooldownSeconds(result.resetAfter())));
        headers.put("RateLimit-Policy", policy.headerPolicy());
        if (!result.allowed()) {
            headers.put("Retry-After", Long.toString(cooldownSeconds(result.retryAfter())));
            return Decision.reject(429, "rate limit exceeded", headers);
        }
        return Decision.proceed(headers);
    }

    /** Whole seconds rounded up, never below one (RFC 9110 Retry-After must be positive). */
    static long cooldownSeconds(Duration duration) {
        long millis = duration.toMillis();
        return Math.max(1, (millis + 999) / 1000);
    }

    /** Bucket keys carry only a digest of the subject, never the address itself. */
    static String sha256Hex(String value) {
        try {
            return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256")
                .digest(value.getBytes(StandardCharsets.UTF_8)));
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }

    /** Exposed for configuration validation and tests. */
    Optional<ProxyPolicy> proxyPolicy() {
        return Optional.ofNullable(proxy);
    }
}
