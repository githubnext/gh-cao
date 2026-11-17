package org.cao.backend.policy;

/**
 * Repository identity evidence supplied by an inventory or lookup adapter. The policy kernel performs
 * no discovery or external request; it only judges evidence it is handed.
 *
 * @param fullName canonical {@code owner/repository} name reported by the source
 * @param id immutable numeric repository identifier (must be positive)
 */
public record RepositoryEvidence(String fullName, long id, boolean archived, boolean disabled) {

    /** Domain-owned lookup contract for explicit targets; an empty result is missing evidence. */
    @FunctionalInterface
    public interface Directory {
        java.util.Optional<RepositoryEvidence> load(String repository);
    }
}
