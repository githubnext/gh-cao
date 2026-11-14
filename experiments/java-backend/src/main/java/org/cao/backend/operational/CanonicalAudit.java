package org.cao.backend.operational;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Instant;
import java.time.ZoneOffset;
import java.time.ZonedDateTime;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;

/**
 * The exact bytes hashed into the audit chain. Output must equal Go's {@code encoding/json}
 * (HTML-safe escaping, map keys in UTF-8 byte order, RFC 3339 nano time with trimmed zeros,
 * {@code omitempty} actor and attributes) so Go and Java verify each other's rows.
 */
public final class CanonicalAudit {
    private static final char[] HEX = "0123456789abcdef".toCharArray();
    /** Code point order equals UTF-8 byte order, unlike UTF-16 order for supplementary characters. */
    private static final Comparator<String> BYTE_ORDER = (a, b) -> {
        int i = 0;
        int j = 0;
        while (i < a.length() && j < b.length()) {
            int x = a.codePointAt(i);
            int y = b.codePointAt(j);
            if (x != y) {
                return Integer.compare(x, y);
            }
            i += Character.charCount(x);
            j += Character.charCount(y);
        }
        return Integer.compare(a.length() - i, b.length() - j);
    };

    private CanonicalAudit() { }

    public static String genesisHex() {
        return "0".repeat(64);
    }

    public static byte[] genesis() {
        return new byte[32];
    }

    public static byte[] record(AuditEvent event) {
        StringBuilder out = new StringBuilder(128);
        out.append("{\"sequence\":").append(event.sequence())
            .append(",\"occurred_at\":\"").append(time(event.occurredAt())).append('"')
            .append(",\"event\":");
        string(out, event.event());
        out.append(",\"outcome\":");
        string(out, event.outcome());
        if (!event.actor().isEmpty()) {
            out.append(",\"actor\":");
            string(out, event.actor());
        }
        if (!event.attributes().isEmpty()) {
            out.append(",\"attributes\":{");
            List<String> keys = new ArrayList<>(event.attributes().keySet());
            keys.sort(BYTE_ORDER);
            boolean first = true;
            for (String key : keys) {
                if (!first) {
                    out.append(',');
                }
                first = false;
                string(out, key);
                out.append(':');
                string(out, event.attributes().get(key));
            }
            out.append('}');
        }
        out.append('}');
        byte[] bytes = out.toString().getBytes(StandardCharsets.UTF_8);
        if (bytes.length > AuditRules.MAX_RECORD_BYTES) {
            throw new AuditInputException("audit record exceeds payload limit");
        }
        return bytes;
    }

    public static byte[] chainHash(byte[] previous, byte[] record) {
        try {
            MessageDigest digest = MessageDigest.getInstance("SHA-256");
            digest.update(previous);
            digest.update(record);
            return digest.digest();
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException("SHA-256 unavailable", e);
        }
    }

    public static String hex(byte[] bytes) {
        char[] out = new char[bytes.length * 2];
        for (int i = 0; i < bytes.length; i++) {
            out[2 * i] = HEX[(bytes[i] >> 4) & 0xF];
            out[2 * i + 1] = HEX[bytes[i] & 0xF];
        }
        return new String(out);
    }

    /** RFC 3339 UTC with the shortest fractional second, as Go's time.Time marshals. */
    static String time(Instant instant) {
        ZonedDateTime t = instant.atZone(ZoneOffset.UTC);
        StringBuilder out = new StringBuilder(32);
        out.append(String.format("%04d-%02d-%02dT%02d:%02d:%02d", t.getYear(), t.getMonthValue(),
            t.getDayOfMonth(), t.getHour(), t.getMinute(), t.getSecond()));
        if (t.getNano() != 0) {
            String fraction = String.format("%09d", t.getNano());
            int end = fraction.length();
            while (fraction.charAt(end - 1) == '0') {
                end--;
            }
            out.append('.').append(fraction, 0, end);
        }
        return out.append('Z').toString();
    }

    private static void string(StringBuilder out, String value) {
        out.append('"');
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            switch (c) {
                case '"' -> out.append("\\\"");
                case '\\' -> out.append("\\\\");
                case '\b' -> out.append("\\b");
                case '\f' -> out.append("\\f");
                case '\n' -> out.append("\\n");
                case '\r' -> out.append("\\r");
                case '\t' -> out.append("\\t");
                case '<', '>', '&', '\u2028', '\u2029' -> unicode(out, c);
                default -> {
                    if (c < 0x20) {
                        unicode(out, c);
                    } else {
                        out.append(c);
                    }
                }
            }
        }
        out.append('"');
    }

    private static void unicode(StringBuilder out, char c) {
        out.append("\\u").append(HEX[(c >> 12) & 0xF]).append(HEX[(c >> 8) & 0xF])
            .append(HEX[(c >> 4) & 0xF]).append(HEX[c & 0xF]);
    }
}
