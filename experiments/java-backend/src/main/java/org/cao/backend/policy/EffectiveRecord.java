package org.cao.backend.policy;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.time.Clock;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.List;
import java.util.regex.Pattern;

/**
 * Builds the derived effective record of specs/control-architecture.md §6.3. Denials, worker records and
 * standalone records are supported; orchestrator records need inventory partitioning and are rejected explicitly.
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
            throw new PolicyException("orchestrator effective records are not implemented");
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
     * Standalone record (§6.3): {@code control_role: standalone} and the workflow, empty campaign and worker,
     * effective caps, and only the admitted candidates, each with its own mode and destination.
     */
    public static ObjectNode buildStandalone(StandaloneResolver.Resolution resolution,
            List<StandaloneResolver.Admitted> admitted, String workflowSha, Clock clock) {
        if (workflowSha == null || !SHA.matcher(workflowSha).matches()) {
            throw new PolicyException("workflow SHA must be a 40-character lowercase hex commit");
        }
        if (!resolution.authorized() && !admitted.isEmpty()) {
            throw new PolicyException("a denied record admits no candidates");
        }
        ObjectNode node = MAPPER.createObjectNode();
        node.put("schema_version", SCHEMA_VERSION);
        node.put("authorized", resolution.authorized());
        node.put("reason", resolution.reason());
        node.put("control_role", "standalone");
        node.put("workflow", resolution.workflow());
        node.put("campaign", "");
        node.put("worker", "");
        node.put("safe_output_mode", resolution.mode().wire());
        node.put("safe_output_repo", resolution.reviewRepository());
        node.put("control_repository", resolution.controlRepository());
        node.put("workflow_sha", workflowSha);
        node.put("policy_sha256", resolution.policyDigest());
        node.put("max_repos", resolution.maxRepositories());
        node.put("rollout_percent", resolution.rolloutPercent());
        node.put("effective_max_repos", admitted.size());
        node.put("resolved_at", Instant.now(clock).truncatedTo(ChronoUnit.SECONDS).toString());
        var candidates = node.putArray("candidate_repositories");
        for (StandaloneResolver.Admitted target : admitted) {
            var entry = candidates.addObject();
            entry.put("full_name", target.fullName());
            entry.put("id", target.id());
            entry.put("safe_output_mode", target.mode().wire());
            entry.put("safe_output_repo", target.destination());
        }
        node.putArray("worker_workflows");
        return node;
    }
}
