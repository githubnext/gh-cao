package org.cao.backend.inventory;

import java.util.List;

/**
 * Domain-owned read-only contract for repository inventory. Implementations own transport and
 * credentials and must throw {@link InventoryUnavailableException} rather than return partial data
 * as complete. No implementation that calls GitHub exists yet.
 */
public interface InventorySource {

    /** Wire names match the comparison implementation's {@code repo_source}. */
    enum Kind {
        ALLOWED_REPOSITORIES("allowed_repos"),
        OWNER("organization");

        private final String wire;

        Kind(String wire) {
            this.wire = wire;
        }

        public String wire() {
            return wire;
        }
    }

    /**
     * @param kind the selected source
     * @param owner owner to list when {@code kind} is OWNER
     * @param repositories exact allow-list when {@code kind} is ALLOWED_REPOSITORIES
     * @param maxScan scan bound; a source may return one extra entry to signal overflow
     */
    record Request(Kind kind, String owner, List<String> repositories, int maxScan) { }

    List<InventoryRepository> load(Request request);
}
