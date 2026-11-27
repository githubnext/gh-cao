package org.cao.backend.inventory;

/** Malformed or inconsistent inventory evidence. Callers must fail closed. */
public final class InventoryException extends RuntimeException {
    private static final long serialVersionUID = 1L;

    public InventoryException(String message) {
        super(message);
    }
}
