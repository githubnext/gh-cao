package org.cao.backend.http;

import java.time.Instant;
import java.util.Optional;

/** Provider-neutral operational and canonical-data facts exposed by health and readiness. */
public interface ServiceState {

    /** Selected operational dependency state (OperationalState in the HTTP contract). */
    record Operational(boolean connected, boolean recoveryReady) { }

    /** Canonical dataset state (DataState in the HTTP contract). */
    record Data(boolean available, boolean rebuildRequired, Optional<Instant> evaluatedAt) { }

    Operational operational();

    Data data();

    /** The only implementation today: no operational store or canonical data exists yet. */
    static ServiceState unconfigured() {
        return new ServiceState() {
            @Override
            public Operational operational() {
                return new Operational(false, false);
            }

            @Override
            public Data data() {
                return new Data(false, false, Optional.empty());
            }
        };
    }
}
