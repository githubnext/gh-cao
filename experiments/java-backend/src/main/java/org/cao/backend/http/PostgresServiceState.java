package org.cao.backend.http;

import java.util.Optional;
import org.cao.backend.operational.OperationalStoreException;
import org.cao.backend.operational.PostgresOperationalStore;

/**
 * Readiness facts backed by a real operational PostgreSQL store. Connectivity is probed on every
 * call, so a lost database turns health and readiness unavailable immediately. Canonical data does
 * not exist in this implementation yet, so it is always reported unavailable.
 */
public final class PostgresServiceState implements ServiceState {
    private final PostgresOperationalStore store;

    public PostgresServiceState(PostgresOperationalStore store) {
        this.store = store;
    }

    @Override
    public Operational operational() {
        try {
            store.ping();
            return new Operational(true, true);
        } catch (OperationalStoreException e) {
            return new Operational(false, false);
        }
    }

    @Override
    public Data data() {
        return new Data(false, false, Optional.empty());
    }
}
