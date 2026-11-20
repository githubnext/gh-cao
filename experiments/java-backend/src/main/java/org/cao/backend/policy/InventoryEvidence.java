package org.cao.backend.policy;

import java.util.List;

/**
 * Bounded repository inventory read by an adapter, or an explicit unavailable marker. Unavailable
 * evidence never degrades to an empty inventory: standalone precompute fails closed.
 */
public record InventoryEvidence(Source source, List<RepositoryRecord> repositories, String error) {
    /** Where the repositories were read from, mirroring the Actions precompute's selection order. */
    public enum Source {
        TARGET_REPO("target_repo"),
        ALLOWED_REPOS("allowed_repos"),
        ORGANIZATION("organization");

        private final String wire;

        Source(String wire) {
            this.wire = wire;
        }

        public String wire() {
            return wire;
        }
    }

    public InventoryEvidence {
        repositories = repositories == null ? List.of() : List.copyOf(repositories);
    }

    public static InventoryEvidence of(Source source, List<RepositoryRecord> repositories) {
        return new InventoryEvidence(source, repositories, "");
    }

    public static InventoryEvidence unavailable(Source source, String error) {
        return new InventoryEvidence(source, List.of(), error == null || error.isEmpty() ? "unavailable" : error);
    }

    public boolean available() {
        return error.isEmpty();
    }
}
