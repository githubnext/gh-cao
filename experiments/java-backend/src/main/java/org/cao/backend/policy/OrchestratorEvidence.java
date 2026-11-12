package org.cao.backend.policy;

import java.util.List;

/**
 * Caller-supplied evidence for an orchestrator record. Nothing here is fetched by the Java backend.
 *
 * @param inventory enrolled repositories as read by the caller; {@code null} when discovery failed
 * @param inventoryError non-secret discovery failure description, used only when inventory is null
 * @param explicitTarget true when a single target was named, which bypasses cell selection
 * @param dispatchMaximum requested per-run dispatch ceiling, 1..1000
 * @param workflows installed worker workflow evidence keyed by configured workflow slug
 */
public record OrchestratorEvidence(
        List<InventoryPartition.Repository> inventory,
        String inventoryError,
        boolean explicitTarget,
        int dispatchMaximum,
        List<WorkerWorkflow> workflows) {

    /** Installed workflow state; {@code state} follows the GitHub value, where {@code disabled*} is inactive. */
    public record WorkerWorkflow(String configured, boolean installed, String state) { }
}
