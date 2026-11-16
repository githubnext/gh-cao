package org.cao.backend.control;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.time.Clock;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.regex.Pattern;
import org.cao.backend.policy.ControlPolicy;
import org.cao.backend.policy.Mode;
import org.cao.backend.policy.PolicyException;
import org.cao.backend.policy.PolicyResolver;
import org.cao.backend.policy.Resolution;
import org.cao.backend.policy.ResolutionRequest;
import org.cao.backend.policy.Role;

/**
 * Orchestrator effective record (specs/control-architecture.md §6.2, §6.3, CAO-EXE-001/003, T-RES-006/007).
 *
 * <p>Resolves policy first; an expected denial returns a denial record without touching any remote
 * evidence. Otherwise it reads exactly enrolled repositories and the workflow catalog through the domain
 * ports, partitions deterministically, intersects the compiled worker list with policy, and derives the
 * stricter repository cap. Missing evidence yields an explicit incomplete record (activation blocked), never
 * a partial or guessed candidate list. Owner-wide discovery is not supported: exact enrollment is required.
 * The record is derived evidence, not authority: it grants no credential, tool or safe-output capability.
 */
public final class OrchestratorPrecompute {
    public static final int SCHEMA_VERSION = 1;
    private static final Pattern SHA = Pattern.compile("^[0-9a-f]{40}$");
    private static final Pattern REPOSITORY = Pattern.compile("^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$");
    private static final String POLICY_PATH = ".github/workflows/cao.json";
    private static final ObjectMapper MAPPER = new ObjectMapper();

    /**
     * Static run context.
     *
     * @param workflowSha exact {@code github.workflow_sha}
     * @param workflowReference {@code GITHUB_WORKFLOW_REF}, naming the importing compiled workflow
     * @param dispatchMax gh-aw {@code dispatch-workflow.max}; null means 1
     * @param safeOutputRepository review destination (or live routing hint); never trusted without checks
     */
    public record Context(String workflowSha, String workflowReference, Integer dispatchMax,
            String safeOutputRepository, Clock clock) { }

    /** A derived record plus whether activation and dispatch may proceed. */
    public record Outcome(ObjectNode record, boolean activationReady) { }

    private final RepositoryInventory inventory;
    private final WorkflowCatalog workflows;

    public OrchestratorPrecompute(RepositoryInventory inventory, WorkflowCatalog workflows) {
        if (inventory == null || workflows == null) {
            throw new PolicyException("inventory and workflow catalog ports are required");
        }
        this.inventory = inventory;
        this.workflows = workflows;
    }

    public Outcome run(ControlPolicy policy, ResolutionRequest request, Context context) {
        if (policy == null || request == null || context == null || request.role() != Role.ORCHESTRATOR) {
            throw new PolicyException("an orchestrator request, policy and context are required");
        }
        if (context.workflowSha() == null || !SHA.matcher(context.workflowSha()).matches()) {
            throw new PolicyException("workflow SHA must be a 40-character lowercase hex commit");
        }
        Resolution resolution = PolicyResolver.resolve(policy, request);
        if (!resolution.authorized()) {
            return new Outcome(denied(resolution, context), false);
        }
        int dispatchMax = context.dispatchMax() == null ? 1 : context.dispatchMax();
        if (dispatchMax < 1 || dispatchMax > 1000) {
            throw new PolicyException("dispatch_max must be an integer from 1 through 1000");
        }
        ControlPolicy.Inventory limits = policy.inventory();
        String destination = destination(policy, resolution, context.safeOutputRepository());
        List<String> enrolled = policy.scope().repositories();
        if (enrolled.size() > limits.maxScanRepositories()) {
            throw new PolicyException("allowed repos exceed max_scan_repos");
        }
        try {
            if (enrolled.isEmpty()) {
                throw new InventoryUnavailableException("exact-enrollment-required",
                        "Inventory requires exact allowed-repositories enrollment. Owner-wide discovery is not supported.");
            }
            for (String name : enrolled) {
                try {
                    PolicyResolver.validateScope(policy.scope(), resolution.controlRepository(), name);
                } catch (PolicyException e) {
                    throw new InventoryUnavailableException("target-outside-scope",
                            "An inventory target is outside the admitted owner/repository scope.");
                }
            }
            List<RepositoryRecord> loaded = exact(enrolled, inventory.load(List.copyOf(enrolled)));
            InventoryPartition partition = InventoryPartition.of(loaded, limits);
            String source = workflows.source(sourcePath(resolution.controlRepository(), context.workflowReference()),
                    context.workflowSha());
            List<String> configured = DispatchWorkflows.parse(source);
            if (configured.isEmpty()) {
                throw new PolicyException(
                        "shared/control.md role orchestrator requires safe-outputs.dispatch-workflow.workflows");
            }
            List<WorkflowEntry> catalog = workflows.list();
            return new Outcome(authorized(policy, request, resolution, context, dispatchMax, destination,
                    partition, configured, catalog), true);
        } catch (InventoryUnavailableException e) {
            return new Outcome(incomplete(resolution, context, e), false);
        }
    }

    private static List<RepositoryRecord> exact(List<String> enrolled, List<RepositoryRecord> loaded) {
        Map<String, RepositoryRecord> byName = new LinkedHashMap<>();
        for (RepositoryRecord repository : loaded) {
            if (byName.put(repository.fullName().toLowerCase(Locale.ROOT), repository) != null) {
                throw new InventoryUnavailableException("inventory-response-invalid",
                        "Repository metadata is malformed or does not match the enrolled repository identity.");
            }
        }
        List<RepositoryRecord> result = new ArrayList<>();
        for (String name : enrolled) {
            RepositoryRecord repository = byName.remove(name.toLowerCase(Locale.ROOT));
            if (repository == null) {
                throw new InventoryUnavailableException("inventory-unavailable",
                        "Required repository inventory could not be read for an enrolled repository.");
            }
            result.add(repository);
        }
        if (!byName.isEmpty()) {
            throw new InventoryUnavailableException("inventory-response-invalid",
                    "Repository metadata is malformed or does not match the enrolled repository identity.");
        }
        return result;
    }

    private static String destination(ControlPolicy policy, Resolution resolution, String requested) {
        String value = requested == null ? "" : requested;
        if (!value.isEmpty()) {
            if (!REPOSITORY.matcher(value).matches()) {
                throw new PolicyException("safe_output_repo must use owner/repository form");
            }
            String owner = value.substring(0, value.indexOf('/'));
            List<String> owners = policy.scope().owners().isEmpty()
                    ? List.of(resolution.controlRepository().substring(0, resolution.controlRepository().indexOf('/')))
                    : policy.scope().owners();
            if (owners.stream().noneMatch(candidate -> candidate.equalsIgnoreCase(owner))) {
                throw new PolicyException("safe_output_repo owner is outside control-plane.scope.allowed-owners");
            }
        }
        if (resolution.mode() == Mode.REVIEW && !value.equalsIgnoreCase(resolution.controlRepository())) {
            throw new PolicyException("a review destination other than the control repository requires "
                    + "private-repository evidence, which is not supported yet");
        }
        return value;
    }

    static String sourcePath(String controlRepository, String workflowReference) {
        if (workflowReference == null || workflowReference.isEmpty()) {
            throw new PolicyException("workflow reference is required");
        }
        String reference = workflowReference.regionMatches(true, 0, controlRepository + "/", 0,
                controlRepository.length() + 1) ? workflowReference.substring(controlRepository.length() + 1)
                : workflowReference;
        int at = reference.lastIndexOf('@');
        String path = at >= 0 ? reference.substring(0, at) : reference;
        if (!path.startsWith(".github/workflows/") || path.contains("..") || !path.endsWith(".yml")) {
            throw new PolicyException("workflow reference must name a compiled workflow in .github/workflows");
        }
        return path.endsWith(".lock.yml") ? path.substring(0, path.length() - ".lock.yml".length()) + ".md"
                : path.substring(0, path.length() - ".yml".length()) + ".md";
    }

    private ObjectNode authorized(ControlPolicy policy, ResolutionRequest request, Resolution resolution,
            Context context, int dispatchMax, String destination, InventoryPartition partition,
            List<String> configured, List<WorkflowEntry> catalog) {
        ControlPolicy.Campaign campaign = policy.campaigns().get(resolution.campaign());
        Map<String, Map.Entry<String, ControlPolicy.Worker>> byWorkflow = new LinkedHashMap<>();
        campaign.workers().forEach((name, worker) -> byWorkflow.put(worker.workflow(), Map.entry(name, worker)));
        Mode requested = request.safeOutputMode() == null || request.safeOutputMode().isEmpty() ? null
                : Mode.parse(request.safeOutputMode(), "safe_output_mode");

        ArrayNode candidates = MAPPER.createArrayNode();
        for (RepositoryRecord repository : partition.candidates()) {
            Mode mode = campaign.targets().get(repository.fullName().toLowerCase(Locale.ROOT));
            mode = mode == null ? resolution.mode() : requested == null ? mode : mode.lesser(requested);
            ObjectNode node = candidates.addObject();
            node.put("id", repository.id());
            node.put("full_name", repository.fullName());
            node.put("archived", repository.archived());
            node.put("disabled", repository.disabled());
            node.put("private", repository.isPrivate());
            putNullable(node, "pushed_at", repository.pushedAt());
            putNullable(node, "default_branch", repository.defaultBranch());
            node.put("safe_output_mode", mode.wire());
        }

        ArrayNode workerNodes = MAPPER.createArrayNode();
        int eligible = 0;
        for (String slug : configured) {
            WorkflowEntry match = catalog.stream()
                    .filter(entry -> (".github/workflows/" + slug + ".lock.yml").equals(entry.path()))
                    .findFirst().orElse(null);
            Map.Entry<String, ControlPolicy.Worker> declared = byWorkflow.get(slug);
            boolean enabled = declared != null && declared.getValue().enabled();
            boolean active = match != null && !String.valueOf(match.state()).startsWith("disabled");
            String skip = declared == null ? "worker is not part of installed campaign"
                    : !enabled ? "worker disabled by control-plane policy"
                    : match == null ? "worker workflow unavailable"
                    : !active ? "worker workflow disabled" : null;
            ObjectNode node = workerNodes.addObject();
            node.put("configured", slug);
            node.put("matched", match != null);
            putNullable(node, "worker", declared == null ? null : declared.getKey());
            node.put("policy_enabled", enabled);
            putNullable(node, "max_mode", declared == null || declared.getValue().maxMode() == null ? null
                    : declared.getValue().maxMode().wire());
            if (match == null) {
                node.putNull("id");
                node.putNull("name");
                node.putNull("path");
                node.put("state", "");
            } else {
                node.put("id", match.id());
                putNullable(node, "name", match.name());
                putNullable(node, "path", match.path());
                node.put("state", match.state() == null ? "" : match.state());
            }
            node.put("eligible", skip == null);
            putNullable(node, "skip_reason", skip);
            if (skip == null) {
                eligible++;
            }
        }

        int candidateCount = partition.candidates().size();
        long rolloutCap = candidateCount == 0 ? 0
                : Math.max(1, ((long) candidateCount * resolution.rolloutPercent() + 99) / 100);
        int workersPerTarget = Math.min(eligible, dispatchMax);
        int dispatchCap = workersPerTarget == 0 ? 0 : dispatchMax / workersPerTarget;
        int effective = (int) Math.min(Math.min(resolution.maxRepositories(), rolloutCap), dispatchCap);

        ObjectNode node = base(resolution, context, true, "authorized");
        node.put("enabled", true);
        node.put("target_repo", "");
        node.put("organization", resolution.controlRepository().substring(0, resolution.controlRepository().indexOf('/')));
        node.put("max_repos", resolution.maxRepositories());
        node.put("max_scan_repos", policy.inventory().maxScanRepositories());
        node.put("dispatch_max", dispatchMax);
        node.put("rollout_percent", resolution.rolloutPercent());
        node.put("effective_max_repos", effective);
        node.put("safe_output_mode", resolution.mode().wire());
        node.put("safe_output_repo", destination);
        node.put("repo_source", "allowed_repos");
        node.put("repo_error", "");
        node.put("inventory_version", partition.version());
        node.put("inventory_repository_count", partition.inventoryRepositoryCount());
        node.put("cell_count", partition.cellCount());
        node.put("cell_index", partition.cellIndex());
        node.put("cell_repository_count", partition.cellRepositoryCount());
        node.put("batch_size", partition.batchSize());
        node.put("batch_index", partition.batchIndex());
        node.put("batch_count", partition.batchCount());
        node.put("batch_id", partition.batchId());
        node.put("total_repositories_scanned", partition.inventoryRepositoryCount());
        node.set("candidate_repositories", candidates);
        node.set("worker_workflows", workerNodes);
        node.put("activation_ready", true);
        node.put("inventory_status", "complete");
        return node;
    }

    private static ObjectNode denied(Resolution resolution, Context context) {
        ObjectNode node = base(resolution, context, false, resolution.reason());
        node.put("enabled", false);
        node.put("effective_max_repos", 0);
        node.put("repo_error", "");
        node.putArray("candidate_repositories");
        node.putArray("worker_workflows");
        return node;
    }

    private static ObjectNode incomplete(Resolution resolution, Context context, InventoryUnavailableException e) {
        ObjectNode node = base(resolution, context, true, e.code());
        node.put("activation_ready", false);
        node.put("inventory_status", "incomplete");
        node.put("repo_error", e.getMessage());
        ObjectNode diagnostic = node.putArray("diagnostics").addObject();
        diagnostic.put("code", e.code());
        diagnostic.put("phase", "inventory");
        diagnostic.put("message", e.getMessage());
        node.put("effective_max_repos", 0);
        node.putArray("candidate_repositories");
        node.putArray("worker_workflows");
        return node;
    }

    private static ObjectNode base(Resolution resolution, Context context, boolean authorized, String reason) {
        ObjectNode node = MAPPER.createObjectNode();
        node.put("schema_version", SCHEMA_VERSION);
        node.put("authorized", authorized);
        node.put("reason", reason);
        node.put("control_role", "orchestrator");
        node.put("campaign", resolution.campaign());
        node.put("worker", "");
        node.put("control_repository", resolution.controlRepository());
        node.put("workflow_sha", context.workflowSha());
        node.put("policy_sha256", resolution.policyDigest());
        node.put("resolved_at", Instant.now(context.clock()).truncatedTo(ChronoUnit.SECONDS).toString());
        ObjectNode source = node.putObject("policy_source");
        source.put("repository", resolution.controlRepository());
        source.put("path", POLICY_PATH);
        source.put("sha", context.workflowSha());
        return node;
    }

    private static void putNullable(ObjectNode node, String name, String value) {
        if (value == null) {
            node.putNull(name);
        } else {
            node.put(name, value);
        }
    }
}
