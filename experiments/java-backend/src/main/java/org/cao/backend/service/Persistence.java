package org.cao.backend.service;

/** Capability persistence, matching OpenAPI OperationalCapability.persistence. */
public enum Persistence {
    VOLATILE("volatile"),
    RESTART("restart");

    private final String wire;

    Persistence(String wire) {
        this.wire = wire;
    }

    public String wire() {
        return wire;
    }
}
