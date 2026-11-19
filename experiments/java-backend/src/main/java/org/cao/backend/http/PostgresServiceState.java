package org.cao.backend.http;

import java.util.Optional;
import org.cao.backend.canonical.CanonicalState;
import org.cao.backend.canonical.CanonicalStore;
import org.cao.backend.canonical.CanonicalStoreException;
import org.cao.backend.operational.OperationalStoreException;
import org.cao.backend.operational.PostgresOperationalStore;

/**
 * Readiness facts backed by real PostgreSQL stores. Connectivity is probed on every call, so a lost
 * database turns health and readiness unavailable immediately. Canonical data is available only once a
 * verified snapshot has been published; with no canonical store it is always reported unavailable.
 * When the canonical store cannot be read, data is unavailable but a rebuild is not claimed to be
 * required (the Go server reports {@code rebuildRequired = !ready}; an unreachable database says
 * nothing about the dataset).
 */
public final class PostgresServiceState implements ServiceState {
    private final PostgresOperationalStore operationalStore;
    private final CanonicalStore canonicalStore;

    public PostgresServiceState(PostgresOperationalStore operationalStore) {
        this(operationalStore, null);
    }

    public PostgresServiceState(PostgresOperationalStore operationalStore, CanonicalStore canonicalStore) {
        this.operationalStore = operationalStore;
        this.canonicalStore = canonicalStore;
    }

    @Override
    public Operational operational() {
        try {
            operationalStore.ping();
            return new Operational(true, true);
        } catch (OperationalStoreException e) {
            return new Operational(false, false);
        }
    }

    @Override
    public Data data() {
        if (canonicalStore == null) {
            return new Data(false, false, Optional.empty());
        }
        try {
            CanonicalState state = canonicalStore.state();
            return new Data(state.available(), !state.available(),
                state.available() ? state.evaluatedAt() : Optional.empty());
        } catch (CanonicalStoreException e) {
            return new Data(false, false, Optional.empty());
        }
    }
}
