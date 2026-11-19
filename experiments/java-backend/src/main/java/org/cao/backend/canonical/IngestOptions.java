package org.cao.backend.canonical;

import java.util.Optional;
import java.util.function.BooleanSupplier;

/**
 * Per-ingestion controls. {@code expectedRevision} is a compare-and-swap precondition on the active
 * revision; {@code cancelled} is polled for every input line; {@code force} re-ingests an identical
 * data revision instead of treating it as already current (INV-007).
 */
public record IngestOptions(Optional<Long> expectedRevision, BooleanSupplier cancelled, boolean force,
    int maxLineBytes) {
    /** Mirrors server/internal/ingest maxJSONRecordBytes. */
    public static final int DEFAULT_MAX_LINE_BYTES = 16 << 20;

    public static IngestOptions defaults() {
        return new IngestOptions(Optional.empty(), () -> false, false, DEFAULT_MAX_LINE_BYTES);
    }

    public IngestOptions withExpectedRevision(long revision) {
        return new IngestOptions(Optional.of(revision), cancelled, force, maxLineBytes);
    }

    public IngestOptions withCancellation(BooleanSupplier supplier) {
        return new IngestOptions(expectedRevision, supplier, force, maxLineBytes);
    }

    public IngestOptions withForce() {
        return new IngestOptions(expectedRevision, cancelled, true, maxLineBytes);
    }

    public IngestOptions withMaxLineBytes(int bytes) {
        return new IngestOptions(expectedRevision, cancelled, force, bytes);
    }
}
