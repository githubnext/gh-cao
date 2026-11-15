package org.cao.backend.policy;

import java.util.List;
import java.util.Set;

/**
 * Repository inventory supplied by an owning adapter. The kernel performs no discovery or network
 * access; evidence that is unavailable must carry a non-empty {@code error} and fails closed.
 *
 * @param source {@code target_repo}, {@code allowed_repos} or {@code organization}
 * @param error empty when the evidence is complete
 */
public record InventoryEvidence(String source, List<InventoryRepository> repositories, String error) {
    private static final Set<String> SOURCES = Set.of("target_repo", "allowed_repos", "organization");

    public InventoryEvidence {
        if (!SOURCES.contains(source) || repositories == null || error == null) {
            throw new PolicyException("inventory evidence is malformed");
        }
        repositories = List.copyOf(repositories);
    }
}
