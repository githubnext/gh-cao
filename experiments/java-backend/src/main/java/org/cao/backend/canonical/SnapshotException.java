package org.cao.backend.canonical;

/**
 * The snapshot input is invalid, unverifiable or unsupported. Messages name shards and line numbers
 * but never echo record content.
 */
public final class SnapshotException extends RuntimeException {
    private static final long serialVersionUID = 1L;

    public SnapshotException(String message) {
        super(message);
    }
}
