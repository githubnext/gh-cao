package org.cao.backend.http;

import java.net.InetAddress;
import java.util.List;
import java.util.Locale;
import java.util.Optional;

/**
 * Client address resolution for rate limiting (specs/server-rate-limiting.md §4). At a trusted
 * boundary only the final non-empty value of the final header field is considered; there is never a
 * leftward scan. A malformed, obfuscated or absent final value falls back to the direct peer, not to
 * an earlier forwarded value. {@code X-Forwarded-For} takes precedence over RFC 7239 {@code Forwarded}.
 */
final class ClientAddress {
    /** Which input produced the address; safe to log, unlike the address itself. */
    enum Source { UNTRUSTED_BOUNDARY, X_FORWARDED_FOR, FORWARDED, REMOTE_FALLBACK }

    record Resolved(String address, Source source) { }

    private ClientAddress() { }

    static Resolved resolve(ProxyPolicy policy, InetAddress peer, List<String> forwardedFor,
        List<String> forwarded) {
        String direct = IpLiteral.format(peer);
        if (!policy.boundaryTrusted(peer)) {
            return new Resolved(direct, Source.UNTRUSTED_BOUNDARY);
        }
        String xff = finalValue(forwardedFor);
        if (!xff.isEmpty()) {
            return IpLiteral.parseWithOptionalPort(xff)
                .map(a -> new Resolved(IpLiteral.format(a), Source.X_FORWARDED_FOR))
                .orElseGet(() -> new Resolved(direct, Source.REMOTE_FALLBACK));
        }
        return forwardedFor(finalValue(forwarded))
            .map(a -> new Resolved(IpLiteral.format(a), Source.FORWARDED))
            .orElseGet(() -> new Resolved(direct, Source.REMOTE_FALLBACK));
    }

    /** The final non-empty comma separated element of the final non-empty header field. */
    static String finalValue(List<String> fields) {
        for (int i = fields.size() - 1; i >= 0; i--) {
            String[] parts = fields.get(i).split(",", -1);
            for (int j = parts.length - 1; j >= 0; j--) {
                if (!parts[j].isBlank()) {
                    return parts[j].strip();
                }
            }
        }
        return "";
    }

    /** The {@code for=} parameter of one RFC 7239 element; quoted IPv6 with a port is accepted. */
    private static Optional<InetAddress> forwardedFor(String element) {
        for (String parameter : element.split(";", -1)) {
            int eq = parameter.indexOf('=');
            if (eq < 0 || !"for".equals(parameter.substring(0, eq).strip().toLowerCase(Locale.ROOT))) {
                continue;
            }
            String candidate = parameter.substring(eq + 1).strip();
            if (candidate.length() >= 2 && candidate.startsWith("\"") && candidate.endsWith("\"")) {
                candidate = candidate.substring(1, candidate.length() - 1);
            }
            return IpLiteral.parseWithOptionalPort(candidate);
        }
        return Optional.empty();
    }
}
