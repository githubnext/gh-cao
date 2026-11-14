package org.cao.backend.operational;

/** The operational store is unavailable, unprovisioned or rejected the operation; never success-shaped. */
public final class OperationalStoreException extends RuntimeException {
    private static final long serialVersionUID = 1L;

    public OperationalStoreException(String message) {
        super(message);
    }

    public OperationalStoreException(String message, Throwable cause) {
        super(message, cause);
    }
}
