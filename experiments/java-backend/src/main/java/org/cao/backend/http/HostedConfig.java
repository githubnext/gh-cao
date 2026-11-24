package org.cao.backend.http;

import java.net.InetAddress;
import java.net.UnknownHostException;
import java.util.List;
import java.util.Map;
import java.util.stream.Collectors;
import org.cao.backend.ratelimit.RateLimitConfig;
import org.cao.backend.ratelimit.RequestLimiter;

/**
 * Hosted-profile settings from the environment. Hosted mode is explicit ({@code CAO_HOSTED=true})
 * and requires an allowed-host list, a distributed limiter, and an explicit trusted-proxy CIDR list
 * before forwarding headers are believed. Anything missing or invalid fails startup.
 */
public final class HostedConfig {
    public static final String HOSTED = "CAO_HOSTED";
    public static final String ALLOWED_HOSTS = "CAO_ALLOWED_HOSTS";
    public static final String REQUIRE_HTTPS = "CAO_REQUIRE_HTTPS";
    public static final String TRUST_FORWARDED = "CAO_TRUST_FORWARDED";
    public static final String TRUSTED_PROXIES = "CAO_TRUSTED_PROXY_CIDRS";

    private HostedConfig() { }

    /** True when the environment selects the hosted profile. */
    public static boolean selected(Map<String, String> env) {
        return "true".equals(env.get(HOSTED));
    }

    /**
     * Builds hosted admission over {@code limiter}. {@code CAO_REQUIRE_HTTPS} defaults to true:
     * hosted traffic must arrive over HTTPS via a trusted proxy that sets {@code X-Forwarded-Proto}.
     */
    public static RequestAdmission admission(Map<String, String> env, RequestLimiter limiter) {
        if (limiter == null) {
            throw new IllegalArgumentException(
                "hosted mode requires a distributed request limiter (set CAO_POSTGRES_URL)");
        }
        List<String> hosts = ProxyPolicy.splitCsv(env.get(ALLOWED_HOSTS));
        boolean trust = bool(env, TRUST_FORWARDED, false);
        boolean https = bool(env, REQUIRE_HTTPS, true);
        List<ProxyPolicy.Cidr> proxies = ProxyPolicy.splitCsv(env.get(TRUSTED_PROXIES)).stream()
            .map(ProxyPolicy.Cidr::parse).collect(Collectors.toList());
        return RequestAdmission.hosted(new ProxyPolicy(hosts, https, trust, proxies), limiter,
            RateLimitConfig.defaults());
    }

    /** A non-loopback listener is only acceptable when it is hosted and therefore metered. */
    public static void requireHostedForExposure(ServerConfig config, Map<String, String> env) {
        if (selected(env)) {
            return;
        }
        try {
            if (!InetAddress.getByName(config.bind()).isLoopbackAddress()) {
                throw new IllegalArgumentException("a non-loopback listener requires " + HOSTED
                    + "=true with an allowed host policy and request limiter");
            }
        } catch (UnknownHostException e) {
            throw new IllegalArgumentException(ServerConfig.BIND + " is not a valid IP address", e);
        }
    }

    private static boolean bool(Map<String, String> env, String name, boolean fallback) {
        String value = env.get(name);
        if (value == null || value.isBlank()) {
            return fallback;
        }
        return switch (value) {
            case "true" -> true;
            case "false" -> false;
            default -> throw new IllegalArgumentException(name + " must be true or false");
        };
    }
}
