package org.cao.backend.policy;

/** Fatal policy or authority error. Callers must fail before any model invocation. */
public final class PolicyException extends RuntimeException {
    private static final long serialVersionUID = 1L;

    public PolicyException(String message) {
        super(message);
    }
}
