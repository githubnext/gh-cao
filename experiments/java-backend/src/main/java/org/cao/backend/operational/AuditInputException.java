package org.cao.backend.operational;

/** An audit event or anchor that cannot be stored or checked. */
public final class AuditInputException extends IllegalArgumentException {
    private static final long serialVersionUID = 1L;

    public AuditInputException(String message) {
        super(message);
    }
}
