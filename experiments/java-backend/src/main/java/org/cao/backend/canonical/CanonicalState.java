package org.cao.backend.canonical;

import java.time.Instant;
import java.util.Optional;

/**
 * Header of the active canonical dataset. {@code revision} 0 means nothing has been published.
 * {@code evaluatedAt} is the newest source observation time and is absent when the published
 * records carry no timestamp (it is never invented).
 */
public record CanonicalState(long revision, String dataRevision, Optional<Instant> evaluatedAt) {

    public boolean available() {
        return revision > 0;
    }
}
