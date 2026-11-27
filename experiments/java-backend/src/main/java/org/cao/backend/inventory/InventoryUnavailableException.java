package org.cao.backend.inventory;

/**
 * The inventory source could not supply complete evidence (credentials, capacity, availability).
 * This is an honest "unavailable" outcome, distinct from malformed evidence.
 */
public final class InventoryUnavailableException extends RuntimeException {
    private static final long serialVersionUID = 1L;

    public InventoryUnavailableException(String message) {
        super(message);
    }
}
