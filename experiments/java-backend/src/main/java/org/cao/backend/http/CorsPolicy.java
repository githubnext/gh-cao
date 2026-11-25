package org.cao.backend.http;

import com.fasterxml.jackson.databind.JsonNode;
import java.net.InetAddress;
import java.net.UnknownHostException;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Locale;
import java.util.Set;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.cao.backend.policy.PolicyException;

/**
 * Reviewed cross-origin policy ({@code control-plane.web.host.cors}, specs/server-cors.md §3).
 * The same-origin default has no allowed origins and emits no {@code Access-Control-*} header.
 * Credentialed CORS is not representable: there is no member for it (§3.5).
 */
public record CorsPolicy(List<String> allowedOrigins, int maxAge) {
    public static final int DEFAULT_MAX_AGE = 600;
    public static final int MAX_MAX_AGE = 86400;
    public static final int MAX_ORIGINS = 32;

    private static final Pattern ORIGIN = Pattern.compile(
            "([A-Za-z]+)://(\\[[0-9A-Fa-f:.]+\\]|[A-Za-z0-9.-]+)(?::([1-9][0-9]{0,4}))?/?");
    private static final Pattern DNS_NAME = Pattern.compile("[A-Za-z0-9-]+(?:\\.[A-Za-z0-9-]+)*");
    private static final Pattern IPV4 = Pattern.compile("[0-9]{1,3}(?:\\.[0-9]{1,3}){3}");

    public CorsPolicy {
        allowedOrigins = List.copyOf(allowedOrigins);
    }

    /** The default: strictly same-origin. */
    public static CorsPolicy sameOrigin() {
        return new CorsPolicy(List.of(), DEFAULT_MAX_AGE);
    }

    public boolean enabled() {
        return !allowedOrigins.isEmpty();
    }

    /** Exact string comparison against normalized origins; no prefix, suffix or pattern matching. */
    public boolean allows(String origin) {
        return allowedOrigins.contains(origin);
    }

    /** Validates and normalizes entries (§3.3, §3.4); entries equal after normalization are merged. */
    public static CorsPolicy of(List<String> origins, Integer maxAge) {
        if (origins.size() > MAX_ORIGINS) {
            throw new PolicyException("cors allowed-origins accepts at most " + MAX_ORIGINS + " origins");
        }
        if (maxAge != null && (maxAge < 1 || maxAge > MAX_MAX_AGE)) {
            throw new PolicyException("cors max-age must be between 1 and " + MAX_MAX_AGE + " seconds");
        }
        Set<String> normalized = new LinkedHashSet<>();
        for (String origin : origins) {
            normalized.add(normalize(origin));
        }
        return new CorsPolicy(new ArrayList<>(normalized), maxAge == null ? DEFAULT_MAX_AGE : maxAge);
    }

    /** Parses the {@code cors} member of a host policy document; unknown members are rejected. */
    static CorsPolicy fromMember(JsonNode cors, String path) {
        if (cors == null || !cors.isObject()) {
            throw new PolicyException(path + " must be a mapping");
        }
        cors.fieldNames().forEachRemaining(name -> {
            if (!"allowed-origins".equals(name) && !"max-age".equals(name)) {
                throw new PolicyException("unknown property " + path + "." + name);
            }
        });
        List<String> origins = new ArrayList<>();
        JsonNode list = cors.get("allowed-origins");
        if (list != null) {
            if (!list.isArray()) {
                throw new PolicyException(path + ".allowed-origins must be an array");
            }
            Set<String> folded = new HashSet<>();
            for (JsonNode item : list) {
                if (!item.isTextual()) {
                    throw new PolicyException(path + ".allowed-origins entries must be strings");
                }
                if (!folded.add(item.asText().toLowerCase(Locale.ROOT))) {
                    throw new PolicyException(path + ".allowed-origins has entries equal ignoring case");
                }
                origins.add(item.asText());
            }
        }
        Integer maxAge = null;
        JsonNode age = cors.get("max-age");
        if (age != null) {
            if (!age.isIntegralNumber() || !age.canConvertToInt()) {
                throw new PolicyException(path + ".max-age must be an integer");
            }
            maxAge = age.asInt();
        }
        return of(origins, maxAge);
    }

    /** Returns the serialized origin browsers send: lowercase scheme and host, default port elided. */
    static String normalize(String value) {
        Matcher m = ORIGIN.matcher(value);
        if (!m.matches()) {
            throw invalid();
        }
        String scheme = m.group(1).toLowerCase(Locale.ROOT);
        String host = m.group(2).toLowerCase(Locale.ROOT);
        String port = m.group(3);
        if (!"https".equals(scheme) && !"http".equals(scheme)) {
            throw invalid();
        }
        boolean bracketed = host.startsWith("[");
        if (bracketed) {
            if (!host.contains(":") || !isIpv6Literal(host.substring(1, host.length() - 1))) {
                throw invalid();
            }
        } else if (!DNS_NAME.matcher(host).matches()) {
            throw invalid();
        }
        if ("http".equals(scheme) && !isLoopbackHost(host, bracketed)) {
            throw invalid();
        }
        if (port != null && Integer.parseInt(port) > 65535) {
            throw invalid();
        }
        boolean defaultPort = port == null
                || ("https".equals(scheme) && "443".equals(port))
                || ("http".equals(scheme) && "80".equals(port));
        return scheme + "://" + host + (defaultPort ? "" : ":" + port);
    }

    private static boolean isLoopbackHost(String host, boolean bracketed) {
        if (bracketed) {
            return ipv6(host.substring(1, host.length() - 1)).map(InetAddress::isLoopbackAddress).orElse(false);
        }
        if ("localhost".equals(host)) {
            return true;
        }
        if (IPV4.matcher(host).matches()) {
            String[] octets = host.split("\\.");
            for (String octet : octets) {
                if (Integer.parseInt(octet) > 255) {
                    return false;
                }
            }
            return "127".equals(octets[0]);
        }
        return false;
    }

    private static boolean isIpv6Literal(String literal) {
        return ipv6(literal).isPresent();
    }

    /** Parses only literals (the caller guarantees a colon), so no DNS lookup can occur. */
    static java.util.Optional<InetAddress> ipv6(String literal) {
        if (!literal.contains(":")) {
            return java.util.Optional.empty();
        }
        try {
            return java.util.Optional.of(InetAddress.getByName(literal));
        } catch (UnknownHostException e) {
            return java.util.Optional.empty();
        }
    }

    private static PolicyException invalid() {
        return new PolicyException(
                "cors allowed-origins entries must be exact https origins such as https://example.com");
    }
}
