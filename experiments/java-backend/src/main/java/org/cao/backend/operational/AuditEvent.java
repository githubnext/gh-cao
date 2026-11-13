package org.cao.backend.operational;

import java.time.Instant;
import java.util.Map;
import java.util.TreeMap;

/**
 * One security audit record. Attributes must never carry credentials. {@code sequence} and
 * {@code occurredAt} are assigned by the store on append; caller-supplied values are ignored.
 */
public record AuditEvent(long sequence, Instant occurredAt, String event, String outcome,
                         String actor, Map<String, String> attributes) {

    public AuditEvent {
        actor = actor == null ? "" : actor;
        attributes = attributes == null ? Map.of() : java.util.Collections.unmodifiableMap(
            new TreeMap<>(attributes));
    }

    /** A new, not yet stored event. */
    public static AuditEvent of(String event, String outcome, String actor,
                                Map<String, String> attributes) {
        return new AuditEvent(0, Instant.EPOCH, event, outcome, actor, attributes);
    }
}
