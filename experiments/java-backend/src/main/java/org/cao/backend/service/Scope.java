package org.cao.backend.service;

/** Capability scope, matching OpenAPI OperationalCapability.scope. */
public enum Scope {
    UNSUPPORTED("unsupported"),
    PROCESS("process"),
    DEPLOYMENT("deployment");

    private final String wire;

    Scope(String wire) {
        this.wire = wire;
    }

    public String wire() {
        return wire;
    }
}
