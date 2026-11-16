package org.cao.backend.control;

/**
 * Required inventory evidence could not be produced. Callers must not execute fleet work; the
 * orchestrator turns it into an explicit incomplete effective record (specs/control-architecture.md §7).
 */
public final class InventoryUnavailableException extends RuntimeException {
    private static final long serialVersionUID = 1L;

    private final String code;

    public InventoryUnavailableException(String code, String message) {
        super(message);
        this.code = code;
    }

    public String code() {
        return code;
    }
}
