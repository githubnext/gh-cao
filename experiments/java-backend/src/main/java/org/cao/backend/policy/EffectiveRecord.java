package org.cao.backend.policy;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.time.Clock;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.Locale;
import java.util.Map;
import java.util.regex.Pattern;

/**
 * Builds the derived effective record of specs/control-architecture.md §6.3. Worker and orchestrator
 * records are supported; standalone records are not.
 */
public final class EffectiveRecord {
    public static final int SCHEMA_VERSION = 1;
    private static final Pattern SHA = Pattern.compile("^[0-9a-f]{40}$");
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private EffectiveRecord() { }

    public static ObjectNode build(ControlPolicy policy, Resolution resolution, DispatchEnvelope parent,
            String workflowSha, Clock clock) {
        if (workflowSha == null || !SHA.matcher(workflowSha).matches()) {
            throw new PolicyException("workflow SHA must be a 40-character lowercase hex commit");
        }
        if (resolution.role() != Role.WORKER) {
            throw new PolicyException("use buildOrchestrator for orchestrator records");
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

    /**
     * Orchestrator record (§6.3): inventory version and batch identity, configured and effective caps,
     * discovery status, bounded candidates and eligible workers with skip reasons.
     */
    public static ObjectNode buildOrchestrator(ControlPolicy policy, Resolution resolution,
            OrchestratorEvidence evidence, String workflowSha, Clock clock) {
        if (workflowSha == null || !SHA.matcher(workflowSha).matches()) {
            throw new PolicyException("workflow SHA must be a 40-character lowercase hex commit");
        }
        if (resolution.role() != Role.ORCHESTRATOR) {
            throw new PolicyException("not an orchestrator resolution");
        }
        ObjectNode node = MAPPER.createObjectNode();
        node.put("schema_version", SCHEMA_VERSION);
        node.put("authorized", resolution.authorized());
        node.put("reason", resolution.reason());
        node.put("control_role", "orchestrator");
        node.put("campaign", resolution.campaign());
        node.put("worker", "");
        node.put("control_repository", resolution.controlRepository());
        node.put("workflow_sha", workflowSha);
        node.put("policy_sha256", resolution.policyDigest());
        node.put("resolved_at", Instant.now(clock).truncatedTo(ChronoUnit.SECONDS).toString());
        if (!resolution.authorized()) {
            node.put("safe_output_mode", Mode.REVIEW.wire());
            node.put("effective_max_repos", 0);
            node.putArray("candidate_repositories");
            node.putArray("worker_workflows");
            return node;
        }
        if (evidence == null) {
            throw new PolicyException("orchestrator evidence is required");
        }
        if (evidence.inventory() == null) {
            throw new PolicyException("inventory-unavailable: required repository inventory could not be read");
        }
        if (evidence.dispatchMaximum() < 1 || evidence.dispatchMaximum() > 1000) {
            throw new PolicyException("dispatch_max must be an integer from 1 through 1000");
        }
        ControlPolicy.Campaign campaign = policy.campaigns().get(resolution.campaign());
        for (InventoryPartition.Repository repository : evidence.inventory()) {
            PolicyResolver.validateScope(policy.scope(), resolution.controlRepository(), repository.fullName());
        }
        InventoryPartition.Result partition =
                InventoryPartition.partition(evidence.inventory(), policy.inventory(), evidence.explicitTarget());

        ArrayNode workers = MAPPER.createArrayNode();
        int eligible = 0;
        for (OrchestratorEvidence.WorkerWorkflow installed : evidence.workflows()) {
            Map.Entry<String, ControlPolicy.Worker> declared = campaign.workers().entrySet().stream()
                    .filter(entry -> entry.getValue().workflow().equals(installed.configured()))
                    .findFirst().orElse(null);
            boolean enabled = declared != null && declared.getValue().enabled();
            boolean active = installed.installed() && installed.state() != null
                    && !installed.state().startsWith("disabled");
            String skip = declared == null ? "worker is not part of installed campaign"
                    : !enabled ? "worker disabled by control-plane policy"
                    : !installed.installed() ? "worker workflow unavailable"
                    : !active ? "worker workflow disabled" : null;
            ObjectNode entry = workers.addObject();
            entry.put("configured", installed.configured());
            entry.put("worker", declared == null ? "" : declared.getKey());
            entry.put("policy_enabled", enabled);
            entry.put("max_mode", declared == null || declared.getValue().maxMode() == null
                    ? "" : declared.getValue().maxMode().wire());
            entry.put("matched", installed.installed());
            entry.put("state", installed.state() == null ? "" : installed.state());
            entry.put("eligible", skip == null);
            entry.put("skip_reason", skip == null ? "" : skip);
            if (skip == null) {
                eligible++;
            }
        }
        int candidates = partition.candidates().size();
        int percentCap = candidates == 0 ? 0
                : Math.max(1, (int) Math.ceil(candidates * resolution.rolloutPercent() / 100.0));
        int perTarget = Math.min(eligible, evidence.dispatchMaximum());
        int dispatchCap = perTarget == 0 ? 0 : evidence.dispatchMaximum() / perTarget;
        int effective = Math.min(resolution.maxRepositories(), Math.min(percentCap, dispatchCap));

        node.put("safe_output_mode", resolution.mode().wire());
        node.put("max_repos", resolution.maxRepositories());
        node.put("rollout_percent", resolution.rolloutPercent());
        node.put("dispatch_max", evidence.dispatchMaximum());
        node.put("effective_max_repos", effective);
        node.put("repo_source", "caller-supplied-inventory");
        node.put("repo_error", "");
        node.put("inventory_version", partition.version());
        node.put("inventory_repository_count", partition.inventoryCount());
        node.put("cell_count", partition.settings().cellCount());
        node.put("cell_index", partition.settings().cellIndex());
        node.put("cell_repository_count", partition.cellRepositoryCount());
        node.put("batch_size", partition.settings().batchSize());
        node.put("batch_index", partition.settings().batchIndex());
        node.put("batch_count", partition.batchCount());
        node.put("batch_id", partition.batchId());
        ArrayNode list = node.putArray("candidate_repositories");
        for (InventoryPartition.Repository repository : partition.candidates()) {
            ObjectNode candidate = list.addObject();
            candidate.put("id", repository.id());
            candidate.put("full_name", repository.fullName());
            candidate.put("archived", repository.archived());
            candidate.put("disabled", repository.disabled());
            Mode mode = campaign.targets().get(repository.fullName().toLowerCase(Locale.ROOT));
            candidate.put("safe_output_mode", (mode == null ? resolution.mode() : mode).wire());
        }
        node.set("worker_workflows", workers);
        return node;
    }
}
