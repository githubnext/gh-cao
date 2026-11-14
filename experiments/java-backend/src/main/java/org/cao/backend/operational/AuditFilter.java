package org.cao.backend.operational;

/** Selects stored audit events in ascending sequence order; empty strings match everything. */
public record AuditFilter(long afterSequence, String event, String actor, int limit) {

    public static final int MAX_LIMIT = 1000;

    public AuditFilter {
        event = event == null ? "" : event;
        actor = actor == null ? "" : actor;
    }

    public static AuditFilter all() {
        return new AuditFilter(0, "", "", MAX_LIMIT);
    }

    int effectiveLimit() {
        return limit <= 0 || limit > MAX_LIMIT ? MAX_LIMIT : limit;
    }
}
