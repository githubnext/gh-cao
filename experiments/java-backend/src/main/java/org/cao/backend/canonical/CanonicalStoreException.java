package org.cao.backend.canonical;

/** Canonical storage is unavailable, unprovisioned, or refused the operation; never success-shaped. */
public final class CanonicalStoreException extends RuntimeException {
    private static final long serialVersionUID = 1L;

    public CanonicalStoreException(String message) {
        super(message);
    }
}
