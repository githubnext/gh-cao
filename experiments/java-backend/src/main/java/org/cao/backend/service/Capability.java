package org.cao.backend.service;

import java.util.Objects;

/** One provider-neutral operational capability. */
public record Capability(Scope scope, Persistence persistence) {
    public static final Capability UNSUPPORTED = new Capability(Scope.UNSUPPORTED, Persistence.VOLATILE);

    public Capability {
        Objects.requireNonNull(scope, "scope");
        Objects.requireNonNull(persistence, "persistence");
    }
}
