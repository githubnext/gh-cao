package org.cao.backend.operational;

import java.nio.charset.StandardCharsets;
import java.util.Map;

/** Bounds every AuditLog enforces (comparison: server/internal/operational/audit.go). */
public final class AuditRules {
    static final int MAX_NAME_BYTES = 1024;
    static final int MAX_ATTRIBUTES = 64;
    static final int MAX_VALUE_BYTES = 4096;
    static final int MAX_RECORD_BYTES = 64 << 10;

    private AuditRules() { }

    public static void validate(AuditEvent event) {
        if (!name(event.event())) {
            throw new AuditInputException("invalid audit event name");
        }
        switch (event.outcome() == null ? "" : event.outcome()) {
            case "success", "denied", "failure" -> { }
            default -> throw new AuditInputException(
                "audit outcome must be success, denied, or failure");
        }
        if (!text(event.actor(), MAX_NAME_BYTES)) {
            throw new AuditInputException("invalid audit actor");
        }
        if (event.attributes().size() > MAX_ATTRIBUTES) {
            throw new AuditInputException("too many audit attributes");
        }
        for (Map.Entry<String, String> attribute : event.attributes().entrySet()) {
            if (!name(attribute.getKey()) || attribute.getValue() == null
                || !text(attribute.getValue(), MAX_VALUE_BYTES)) {
                throw new AuditInputException("oversized or NUL-containing audit attribute");
            }
        }
    }

    private static boolean name(String value) {
        return value != null && !value.isBlank() && text(value, MAX_NAME_BYTES);
    }

    /** Bounded UTF-8 text without NUL; lone surrogates cannot be stored faithfully and are rejected. */
    private static boolean text(String value, int maxBytes) {
        if (value.indexOf('\0') >= 0) {
            return false;
        }
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            if (Character.isHighSurrogate(c) && i + 1 < value.length()
                && Character.isLowSurrogate(value.charAt(i + 1))) {
                i++;
            } else if (Character.isSurrogate(c)) {
                return false;
            }
        }
        return value.getBytes(StandardCharsets.UTF_8).length <= maxBytes;
    }
}
