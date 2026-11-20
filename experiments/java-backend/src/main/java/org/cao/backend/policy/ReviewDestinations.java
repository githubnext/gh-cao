package org.cao.backend.policy;

import java.util.Optional;

/**
 * Domain-owned evidence about review output repositories other than the control repository.
 * An empty result means the repository was not readable; it never implies privacy.
 */
@FunctionalInterface
public interface ReviewDestinations {
    Optional<Boolean> isPrivate(String repository);

    /** No evidence: every non-central review destination is rejected. */
    static ReviewDestinations none() {
        return repository -> Optional.empty();
    }
}
