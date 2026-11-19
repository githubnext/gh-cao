package org.cao.backend.canonical;

/** A compare-and-swap precondition on the active canonical revision no longer holds. */
public final class StaleRevisionException extends RuntimeException {
    private static final long serialVersionUID = 1L;

    public StaleRevisionException(long expected, long actual) {
        super("canonical revision is " + actual + ", expected " + expected);
    }
}
