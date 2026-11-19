package org.cao.backend.canonical;

import java.nio.file.Path;
import java.util.Map;

/**
 * Domain contract for canonical storage (specs/dashboard-data.md INV-007, INV-009; server-ingestion.md
 * §7). Implementations replace the active dataset atomically: a failed, stale or cancelled ingestion
 * leaves the previously committed dataset serving.
 */
public interface CanonicalStore {

    /** Header of the active dataset; revision 0 when nothing has been published. */
    CanonicalState state();

    /**
     * Verifies a manifest-described snapshot directory, then publishes it as the next revision in one
     * transaction. Identical content (same data revision) is a no-op unless {@code force} is set.
     *
     * @throws SnapshotException the input is invalid, unverifiable or unsupported
     * @throws StaleRevisionException {@code expectedRevision} no longer matches the active revision
     * @throws IngestCancelledException cancellation was requested before publication
     * @throws CanonicalStoreException storage is unavailable or unprovisioned
     */
    IngestResult ingest(Path snapshotDirectory, IngestOptions options);

    /** Row counts of the active dataset keyed by canonical collection name. */
    Map<String, Long> counts();
}
