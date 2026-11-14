package org.cao.backend.operational;

import java.util.List;

/**
 * Append-only, tamper-evident audit service. Appends assign a gap-free per-namespace sequence and
 * chain each record to its predecessor with SHA-256 (specs/server-operational-storage.md).
 */
public interface AuditLog {

    /** Stores one event, returning it with its assigned sequence and database-clock time. */
    AuditEvent append(AuditEvent event);

    List<AuditEvent> list(AuditFilter filter);

    /** Recomputes the chain over retained events; anchors must still match. */
    AuditVerification verify(List<AuditAnchor> anchors);
}
