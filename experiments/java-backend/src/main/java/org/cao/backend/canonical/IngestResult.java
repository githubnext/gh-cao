package org.cao.backend.canonical;

import java.util.Map;

/** Outcome of one ingestion: the active state, per-collection counts, and whether data was replaced. */
public record IngestResult(CanonicalState state, Map<String, Long> counts, boolean replaced) { }
