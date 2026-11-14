package org.cao.backend.policy;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.time.Clock;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.regex.Pattern;

/**
 * Builds the derived effective record of specs/control-architecture.md §6.3. Only denials and worker
 * records are supported; orchestrator and standalone records need inventory and are rejected explicitly.
 */
public final class EffectiveRecord {
    public static final int SCHEMA_VERSION = 1;
    private static final Pattern SHA = Pattern.compile("^[0-9a-f]{40}$");
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private EffectiveRecord() { }

    private static ObjectNode deniedOrchestrator(Resolution resolution, String workflowSha, Clock clock) {
        ObjectNode node = MAPPER.createObjectNode();
        common(node, resolution, workflowSha, clock);
        node.put("control_role", "orchestrator");
        node.put("worker", "");
        node.put("enabled", false);
        node.put("safe_output_mode", resolution.mode().wire());
        node.put("safe_output_repo", "");
        node.put("effective_max_repos", 0);
        node.put("effective_rollout_percent", 0);
        node.put("repo_error", "");
        node.putArray("candidate_repositories");
        node.putArray("worker_workflows");
        return node;
    }

    /**
     * Authorized orchestrator record (specs/control-architecture.md §6.3): inventory version and batch
     * identity, configured and effective caps, discovery status, bounded candidates with their own mode and
     * routing, and eligible worker workflows with skip reasons.
     */
    public static ObjectNode buildOrchestrator(OrchestratorPlanner.Plan plan, String workflowSha, Clock clock) {
        if (workflowSha == null || !SHA.matcher(workflowSha).matches()) {
            throw new PolicyException("workflow SHA must be a 40-character lowercase hex commit");
        }
        Resolution resolution = plan.resolution();
        if (!resolution.authorized() || resolution.role() != Role.ORCHESTRATOR) {
            throw new PolicyException("plan must come from an authorized orchestrator resolution");
        }
        ObjectNode node = MAPPER.createObjectNode();
        common(node, resolution, workflowSha, clock);
        node.put("control_role", "orchestrator");
        node.put("worker", "");
        node.put("enabled", plan.campaignEnabled());
        node.put("organization", resolution.controlRepository().substring(0, resolution.controlRepository().indexOf('/')));
        node.put("safe_output_mode", resolution.mode().wire());
        node.put("safe_output_repo", plan.safeOutputRepository());
        node.put("max_repos", resolution.maxRepositories());
        node.put("max_scan_repos", plan.limits().maxScanRepositories());
        node.put("dispatch_max", plan.dispatchMax());
        node.put("rollout_percent", resolution.rolloutPercent());
        node.put("effective_max_repos", plan.effectiveMaxRepositories());
        node.put("effective_rollout_percent", resolution.rolloutPercent());
        node.put("repo_source", plan.source().wire());
        node.put("repo_error", "");
        node.put("inventory_status", "complete");
        node.put("activation_ready", true);
        InventoryPartition partition = plan.partition();
        node.put("inventory_version", partition.version());
        node.put("inventory_repository_count", partition.inventoryRepositoryCount());
        node.put("total_repositories_scanned", partition.inventoryRepositoryCount());
        node.put("cell_count", partition.cellCount());
        node.put("cell_index", partition.cellIndex());
        node.put("cell_repository_count", partition.cellRepositoryCount());
        node.put("batch_size", partition.batchSize());
        node.put("batch_index", partition.batchIndex());
        node.put("batch_count", partition.batchCount());
        node.put("batch_id", partition.batchId());
        var candidates = node.putArray("candidate_repositories");
        for (OrchestratorPlanner.Candidate candidate : plan.candidates()) {
            RepositoryMetadata repository = candidate.repository();
            ObjectNode entry = candidates.addObject();
            entry.put("id", repository.id());
            entry.put("full_name", repository.fullName());
            entry.put("archived", repository.archived());
            entry.put("disabled", repository.disabled());
            entry.put("private", repository.isPrivate());
            entry.put("pushed_at", repository.pushedAt());
            entry.put("default_branch", repository.defaultBranch());
            entry.put("safe_output_mode", candidate.mode().wire());
        }
        var workers = node.putArray("worker_workflows");
        for (OrchestratorPlanner.WorkerWorkflow worker : plan.workers()) {
            ObjectNode entry = workers.addObject();
            entry.put("configured", worker.configured());
            entry.put("matched", worker.matched());
            entry.put("worker", worker.worker());
            entry.put("policy_enabled", worker.policyEnabled());
            entry.put("max_mode", worker.maxMode() == null ? null : worker.maxMode().wire());
            var installed = worker.installed();
            entry.put("id", installed == null ? null : installed.id());
            entry.put("name", installed == null ? null : installed.name());
            entry.put("path", installed == null ? null : installed.path());
            entry.put("state", installed == null ? "" : installed.state());
            entry.put("eligible", worker.eligible());
            entry.put("skip_reason", worker.skipReason());
        }
        return node;
    }

    private static void common(ObjectNode node, Resolution resolution, String workflowSha, Clock clock) {
        node.put("schema_version", SCHEMA_VERSION);
        node.put("authorized", resolution.authorized());
        node.put("reason", resolution.reason());
        node.put("campaign", resolution.campaign());
        node.put("control_repository", resolution.controlRepository());
        node.put("workflow_sha", workflowSha);
        node.put("policy_sha256", resolution.policyDigest());
        node.put("effective_max_repos", resolution.maxRepositories());
        node.put("effective_rollout_percent", resolution.rolloutPercent());
        node.put("resolved_at", Instant.now(clock).truncatedTo(ChronoUnit.SECONDS).toString());
    }

    public static ObjectNode build(ControlPolicy policy, Resolution resolution, DispatchEnvelope parent,
            String workflowSha, Clock clock) {
        if (workflowSha == null || !SHA.matcher(workflowSha).matches()) {
            throw new PolicyException("workflow SHA must be a 40-character lowercase hex commit");
        }
        if (resolution.role() == Role.ORCHESTRATOR && !resolution.authorized()) {
            return deniedOrchestrator(resolution, workflowSha, clock);
        }
        if (resolution.role() != Role.WORKER) {
            throw new PolicyException("an authorized orchestrator record requires an OrchestratorPlanner plan");
        }
        if (resolution.authorized() && parent == null) {
            throw new PolicyException("an authorized worker record requires the dispatch envelope");
        }
        ObjectNode node = MAPPER.createObjectNode();
        node.put("schema_version", SCHEMA_VERSION);
        node.put("authorized", resolution.authorized());
        node.put("reason", resolution.reason());
        node.put("control_role", "worker");
        node.put("campaign", resolution.campaign());
        node.put("worker", resolution.worker());
        node.put("target_repo", resolution.targetRepository());
        node.put("safe_output_mode", resolution.mode().wire());
        node.put("safe_output_repo", resolution.authorized() ? resolution.targetRepository() : "");
        node.put("control_repository", resolution.controlRepository());
        node.put("workflow_sha", workflowSha);
        node.put("policy_sha256", resolution.policyDigest());
        node.put("effective_max_repos", resolution.maxRepositories());
        node.put("effective_rollout_percent", resolution.rolloutPercent());
        node.put("resolved_at", Instant.now(clock).truncatedTo(ChronoUnit.SECONDS).toString());
        ControlPolicy.Worker worker = policy.campaigns().get(resolution.campaign()) == null ? null
                : policy.campaigns().get(resolution.campaign()).workers().get(resolution.worker());
        node.put("worker_enabled", worker != null && worker.enabled());
        node.put("worker_max_mode", worker == null || worker.maxMode() == null ? "" : worker.maxMode().wire());
        if (resolution.authorized()) {
            ObjectNode envelope = node.putObject("dispatch_envelope");
            envelope.put("mode", parent.mode().wire());
            envelope.put("max_repositories", parent.maxRepositories());
            envelope.put("rollout_percent", parent.rolloutPercent());
            envelope.put("policy_sha256", parent.policyDigest());
        }
        node.putArray("candidate_repositories");
        node.putArray("worker_workflows");
        return node;
    }
}
