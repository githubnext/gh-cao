package org.cao.backend.http;

import java.net.InetAddress;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.Optional;

/**
 * Trusted enterprise proxy boundary (specs/server-rate-limiting.md §4). Forwarding headers are read
 * only after the request crossed a configured trusted proxy boundary and its effective host and
 * HTTPS protocol validated; a directly exposed listener ignores them.
 */
public record ProxyPolicy(List<String> allowedHosts, boolean requireHttps, boolean trustForwarded,
    List<Cidr> trustedProxies) {

    /** Why a request failed the boundary. Stable and free of host or header values for logging. */
    public enum Rejection { NONE, UNTRUSTED_PEER, EMPTY_HOST, HOST_NOT_ALLOWED, INSECURE_REQUEST }

    public ProxyPolicy {
        allowedHosts = List.copyOf(allowedHosts);
        trustedProxies = List.copyOf(trustedProxies);
        if (allowedHosts.isEmpty()) {
            throw new IllegalArgumentException("hosted mode requires an explicit allowed host policy");
        }
        if (trustForwarded && trustedProxies.isEmpty()) {
            throw new IllegalArgumentException(
                "forwarded headers require explicit trusted proxy CIDRs; a header name is not a boundary");
        }
    }

    /** An address prefix. Parsed without DNS. */
    public record Cidr(byte[] network, int bits) {
        public static Cidr parse(String text) {
            int slash = text.indexOf('/');
            Optional<InetAddress> address = IpLiteral.parseWithOptionalPort(
                slash < 0 ? text : text.substring(0, slash));
            if (address.isEmpty() || text.contains("[") || text.contains("%")) {
                throw new IllegalArgumentException("invalid trusted proxy CIDR");
            }
            byte[] bytes = address.get().getAddress();
            int bits = bytes.length * 8;
            if (slash >= 0) {
                try {
                    bits = Integer.parseInt(text.substring(slash + 1));
                } catch (NumberFormatException e) {
                    throw new IllegalArgumentException("invalid trusted proxy CIDR", e);
                }
                if (bits < 0 || bits > bytes.length * 8) {
                    throw new IllegalArgumentException("invalid trusted proxy CIDR");
                }
            }
            return new Cidr(bytes, bits);
        }

        boolean contains(InetAddress candidate) {
            byte[] other = candidate.getAddress();
            if (other.length != network.length) {
                return false;
            }
            for (int i = 0; i < bits; i++) {
                int mask = 0x80 >> (i % 8);
                if ((network[i / 8] & mask) != (other[i / 8] & mask)) {
                    return false;
                }
            }
            return true;
        }
    }

    /** Splits a comma separated list, trimming and dropping empty items. */
    public static List<String> splitCsv(String value) {
        List<String> out = new ArrayList<>();
        if (value != null) {
            for (String item : value.split(",")) {
                if (!item.isBlank()) {
                    out.add(item.strip());
                }
            }
        }
        return out;
    }

    /** True when {@code peer} lies inside a configured trusted proxy prefix. */
    boolean trustedPeer(InetAddress peer) {
        return trustedProxies.stream().anyMatch(c -> c.contains(peer));
    }

    /** True when forwarding headers may be read for a request from {@code peer}. */
    boolean boundaryTrusted(InetAddress peer) {
        return trustForwarded && trustedPeer(peer);
    }

    /**
     * Validates the effective host and protocol. When forwarded headers are trusted the peer must be
     * a trusted proxy, and the final {@code X-Forwarded-Host}/{@code X-Forwarded-Proto} values apply.
     */
    public Rejection classify(String hostHeader, boolean directTls, List<String> forwardedHost,
        List<String> forwardedProto, InetAddress peer) {
        String host = hostHeader == null ? "" : hostHeader;
        boolean secure = directTls;
        if (trustForwarded) {
            if (!trustedPeer(peer)) {
                return Rejection.UNTRUSTED_PEER;
            }
            String forwarded = ClientAddress.finalValue(forwardedHost);
            if (!forwarded.isEmpty()) {
                host = forwarded;
            }
            secure = "https".equals(ClientAddress.finalValue(forwardedProto).toLowerCase(Locale.ROOT));
        }
        host = host.split(",", -1)[0].strip().toLowerCase(Locale.ROOT);
        if (host.isEmpty()) {
            return Rejection.EMPTY_HOST;
        }
        host = stripPort(host);
        boolean allowed = false;
        for (String candidate : allowedHosts) {
            allowed |= candidate.strip().equalsIgnoreCase(host);
        }
        if (!allowed) {
            return Rejection.HOST_NOT_ALLOWED;
        }
        return requireHttps && !secure ? Rejection.INSECURE_REQUEST : Rejection.NONE;
    }

    private static String stripPort(String host) {
        if (host.startsWith("[")) {
            int close = host.indexOf(']');
            return close < 0 ? host : host.substring(0, close + 1);
        }
        int colon = host.indexOf(':');
        return colon < 0 ? host : host.substring(0, colon);
    }
}
