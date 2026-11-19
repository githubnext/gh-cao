package org.cao.backend.canonical;

import java.util.Set;

/**
 * Canonical collection registry mirrored from the {@code canonical: true} entities of
 * {@code server/spec/storage.tsp} at the source revision, with their snapshot shard phase
 * (specs/dashboard-data.md §6.1, server/internal/ingest). Only {@link #IMPLEMENTED} collections can
 * be stored; every other registered collection is rejected explicitly rather than dropped.
 */
final class CanonicalCollections {
    static final Set<String> RUN_PHASE = Set.of("campaigns", "repositories", "workflows", "runs",
        "experiments", "experimentAssignments");
    static final Set<String> RECORD_PHASE = Set.of("audits", "domains", "evals", "evalObservations",
        "friction", "graders", "graderObservations", "issues", "operationalValueEvidence",
        "operationalValues", "outputLifecycles", "skills", "tools", "workflowValueCollections");
    static final Set<String> IMPLEMENTED = Set.of("campaigns", "repositories", "workflows", "runs");

    private CanonicalCollections() { }

    static boolean registered(String collection) {
        return RUN_PHASE.contains(collection) || RECORD_PHASE.contains(collection);
    }

    /** Whether {@code collection} belongs to the {@code runs} shard phase (otherwise {@code records}). */
    static boolean runPhase(String collection) {
        return RUN_PHASE.contains(collection);
    }
}
