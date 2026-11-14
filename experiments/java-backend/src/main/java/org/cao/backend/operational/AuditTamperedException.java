package org.cao.backend.operational;

/** The audit sequence or hash chain does not verify. */
public final class AuditTamperedException extends RuntimeException {
    private static final long serialVersionUID = 1L;

    public AuditTamperedException(String message) {
        super("audit trail integrity check failed: " + message);
    }
}
