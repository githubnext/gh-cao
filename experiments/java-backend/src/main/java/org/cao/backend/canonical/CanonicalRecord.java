package org.cao.backend.canonical;

import com.fasterxml.jackson.databind.JsonNode;
import java.time.Instant;
import java.util.Optional;

/**
 * One validated canonical row. {@code repositoryId}, {@code workflowId} and {@code campaignId} are the
 * relationship keys required by specs/dashboard-data.md §36.4 (Workflow -> Repository, Run -> Workflow,
 * Workflow -> Campaign); {@code observed} is the newest source timestamp in the row.
 */
record CanonicalRecord(String collection, String id, String repositoryId, String workflowId,
    String campaignId, JsonNode body, Optional<Instant> observed) { }
