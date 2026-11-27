package org.cao.backend.policy;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.time.Clock;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.regex.Pattern;
import org.cao.backend.inventory.InventoryException;
import org.cao.backend.inventory.InventoryPartitioner;
import org.cao.backend.inventory.InventoryRepository;
import org.cao.backend.inventory.InventorySource;
import org.cao.backend.inventory.InventoryUnavailableException;

/**
 * Orchestrator admission and effective record (specs/control-architecture.md §4.4 CAO-EXE-001, §5.3
 * inventory, §6.2 steps 4-7, §6.3 orchestrator record, §7). It resolves policy, reads a bounded
 * inventory through {@link InventorySource}, partitions it deterministically, caps the admitted
 * repositories, and describes which declared workers may be dispatched. It dispatches nothing and
 * calls no remote service itself.
 *
 * <p>Expected denials and unavailable inventory return records; malformed or contradictory evidence
 * and widening requests throw.
 */
public final class OrchestratorAdmission {
    public static final String INCOMPLETE_MESSAGE =
            "Required repository inventory could not be read; verify credentials, exact enrollment, API capacity"
                    + " and GitHub availability.";
    private static final Pattern SHA = Pattern.compile("^[0-9a-f]{40}$");
    private static final Pattern SLUG = Pattern.compile("^[a-z0-9]+(?:-[a-z0-9]+)*$");
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private OrchestratorAdmission() { }

    /**
     * Caller-supplied, unverified evidence. The compiled workflow's dispatch list and the workflow
     * states come from the workflow source and GitHub; this class does not obtain or trust them
     * beyond shape validation.
     *
     * @param workflowSha exact commit the policy was read at
     * @param safeOutputRepository review destination; only the control repository is supported
     * @param dispatchMax declared dispatch bound, 1..1000
     * @param configuredWorkflows compiled worker workflow slugs the orchestrator may dispatch
     * @param workflowStates workflow slug to GitHub state; absent means the workflow was not found
     */
    public record Context(
            String workflowSha,
            String safeOutputRepository,
            int dispatchMax,
            List<String> configuredWorkflows,
            Map<String, String> workflowStates) { }

    /** The record, and whether activation and dispatch may proceed. */
    public record Result(ObjectNode record, boolean activationReady) { }

    public static Result admit(ControlPolicy policy, ResolutionRequest request, Context context,
            InventorySource source, Clock clock) {
        if (policy == null || request == null || context == null || source == null || clock == null) {
            throw new PolicyException("policy, request, context, inventory source and clock are required");
        }
        if (request.role() != Role.ORCHESTRATOR) {
            throw new PolicyException("only orchestrators perform inventory admission");
        }
        if (context.workflowSha() == null || !SHA.matcher(context.workflowSha()).matches()) {
            throw new PolicyException("workflow SHA must be a 40-character lowercase hex commit");
        }
        Resolution resolution = PolicyResolver.resolve(policy, request);
        if (!resolution.authorized()) {
            return new Result(denied(resolution, context, clock), false);
        }
        if (context.dispatchMax() < 1 || context.dispatchMax() > 1000) {
            throw new PolicyException("dispatch_max must be an integer from 1 through 1000");
        }
        if (context.configuredWorkflows() == null || context.configuredWorkflows().isEmpty()
                || context.configuredWorkflows().stream().anyMatch(w -> w == null || !SLUG.matcher(w).matches())) {
            throw new PolicyException("an orchestrator requires its compiled, well-formed worker workflow list");
        }
        String destination = context.safeOutputRepository();
        if (destination == null || !destination.matches("^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$")) {
            throw new PolicyException("safe_output_repo must use owner/repository form");
        }
        PolicyResolver.validateOwner(policy.scope(), request.controlRepository(),
                destination.substring(0, destination.indexOf('/')));
        if (resolution.mode() == Mode.REVIEW && !destination.equalsIgnoreCase(request.controlRepository())) {
            throw new PolicyException("review safe_output_repo other than the control repository needs destination"
                    + " privacy evidence, which is not implemented");
        }

        ControlPolicy.Inventory limits = policy.inventory();
        InventorySource.Request sourceRequest = sourceRequest(policy, request, limits);
        InventoryPartitioner.Partition partition;
        try {
            List<InventoryRepository> loaded = source.load(sourceRequest);
            verify(sourceRequest, loaded);
            partition = InventoryPartitioner.partition(loaded, new InventoryPartitioner.Limits(
                    limits.cellCount(), limits.cellIndex(), limits.batchSize(), limits.batchIndex()));
        } catch (InventoryUnavailableException e) {
            return new Result(incomplete(resolution, context, clock), false);
        }

        ArrayNode workers = MAPPER.createArrayNode();
        int eligible = 0;
        ControlPolicy.Campaign campaign = policy.campaigns().get(request.campaign());
        for (String configured : context.configuredWorkflows()) {
            ObjectNode node = workers.addObject();
            Map.Entry<String, ControlPolicy.Worker> declared = campaign.workers().entrySet().stream()
                    .filter(entry -> entry.getValue().workflow().equals(configured)).findFirst().orElse(null);
            String state = context.workflowStates().get(configured);
            boolean enabled = declared != null && declared.getValue().enabled();
            boolean active = state != null && !state.startsWith("disabled");
            boolean ok = enabled && active;
            eligible += ok ? 1 : 0;
            node.put("configured", configured);
            node.put("matched", state != null);
            node.put("worker", declared == null ? null : declared.getKey());
            node.put("policy_enabled", enabled);
            Mode ceiling = declared == null ? null : declared.getValue().maxMode();
            node.put("max_mode", ceiling == null ? null : ceiling.wire());
            node.put("state", state == null ? "" : state);
            node.put("eligible", ok);
            node.put("skip_reason", declared == null ? "worker is not part of installed campaign"
                    : !enabled ? "worker disabled by control-plane policy"
                    : state == null ? "worker workflow unavailable"
                    : !active ? "worker workflow disabled" : null);
        }

        int candidates = partition.candidates().size();
        long rolloutCap = candidates == 0 ? 0 : Math.max(1, ((long) candidates * resolution.rolloutPercent() + 99) / 100);
        int perTarget = Math.min(eligible, context.dispatchMax());
        int dispatchCap = perTarget == 0 ? 0 : context.dispatchMax() / perTarget;
        int effective = (int) Math.min(resolution.maxRepositories(), Math.min(rolloutCap, dispatchCap));

        ObjectNode node = base(resolution, context, clock);
        node.put("authorized", true);
        node.put("reason", "authorized");
        node.put("enabled", true);
        node.put("target_repo", "");
        node.put("organization", request.controlRepository().substring(0, request.controlRepository().indexOf('/')));
        node.put("max_repos", resolution.maxRepositories());
        node.put("max_scan_repos", limits.maxScanRepositories());
        node.put("dispatch_max", context.dispatchMax());
        node.put("rollout_percent", resolution.rolloutPercent());
        node.put("effective_max_repos", effective);
        node.put("safe_output_mode", resolution.mode().wire());
        node.put("safe_output_repo", destination);
        node.put("repo_source", sourceRequest.kind().wire());
        node.put("repo_error", "");
        node.put("inventory_version", partition.version());
        node.put("inventory_repository_count", partition.repositoryCount());
        node.put("cell_count", partition.cellCount());
        node.put("cell_index", partition.cellIndex());
        node.put("cell_repository_count", partition.cellRepositoryCount());
        node.put("batch_size", partition.batchSize());
        node.put("batch_index", partition.batchIndex());
        node.put("batch_count", partition.batchCount());
        node.put("batch_id", partition.batchId());
        node.put("total_repositories_scanned", partition.repositoryCount());
        ArrayNode list = node.putArray("candidate_repositories");
        Mode requested = request.safeOutputMode() == null || request.safeOutputMode().isEmpty()
                ? null : Mode.parse(request.safeOutputMode(), "safe_output_mode");
        for (InventoryRepository repository : partition.candidates()) {
            Mode mode = campaign.targets().getOrDefault(repository.fullName().toLowerCase(Locale.ROOT),
                    resolution.mode());
            ObjectNode item = list.addObject();
            item.put("id", repository.id());
            item.put("full_name", repository.fullName());
            item.put("archived", repository.archived());
            item.put("disabled", repository.disabled());
            item.put("private", repository.isPrivate());
            item.put("pushed_at", repository.pushedAt());
            item.put("default_branch", repository.defaultBranch());
            item.put("safe_output_mode", (requested == null ? mode : mode.lesser(requested)).wire());
        }
        node.set("worker_workflows", workers);
        return new Result(node, true);
    }

    private static InventorySource.Request sourceRequest(ControlPolicy policy, ResolutionRequest request,
            ControlPolicy.Inventory limits) {
        List<String> allowed = policy.scope().repositories();
        if (!allowed.isEmpty()) {
            if (allowed.size() > limits.maxScanRepositories()) {
                throw new PolicyException("allowed repos exceed max_scan_repos");
            }
            return new InventorySource.Request(InventorySource.Kind.ALLOWED_REPOSITORIES, null, allowed,
                    limits.maxScanRepositories());
        }
        String owner = request.controlRepository().substring(0, request.controlRepository().indexOf('/'));
        PolicyResolver.validateOwner(policy.scope(), request.controlRepository(), owner);
        return new InventorySource.Request(InventorySource.Kind.OWNER, owner, List.of(),
                limits.maxScanRepositories());
    }

    /** Source output must match what was asked for; a source cannot widen the inventory. */
    private static void verify(InventorySource.Request request, List<InventoryRepository> loaded) {
        if (loaded == null) {
            throw new InventoryException("inventory source returned no result");
        }
        if (loaded.size() > request.maxScan()) {
            throw new InventoryUnavailableException("inventory exceeds max-scan-repositories");
        }
        if (request.kind() == InventorySource.Kind.OWNER) {
            for (InventoryRepository repository : loaded) {
                String owner = repository.fullName().substring(0, repository.fullName().indexOf('/'));
                if (!owner.equalsIgnoreCase(request.owner())) {
                    throw new InventoryException("inventory contains a repository outside the requested owner");
                }
            }
            return;
        }
        Set<String> expected = new HashSet<>();
        request.repositories().forEach(name -> expected.add(name.toLowerCase(Locale.ROOT)));
        Set<String> seen = new HashSet<>();
        for (InventoryRepository repository : loaded) {
            String name = repository.fullName().toLowerCase(Locale.ROOT);
            if (!expected.contains(name)) {
                throw new InventoryException("inventory contains a repository that is not enrolled");
            }
            seen.add(name);
        }
        if (!seen.equals(expected)) {
            throw new InventoryUnavailableException("an enrolled repository could not be read");
        }
    }

    private static ObjectNode base(Resolution resolution, Context context, Clock clock) {
        ObjectNode node = MAPPER.createObjectNode();
        node.put("schema_version", EffectiveRecord.SCHEMA_VERSION);
        node.put("control_role", "orchestrator");
        node.put("campaign", resolution.campaign());
        node.put("bundle", resolution.campaign());
        node.put("worker", "");
        node.put("control_repository", resolution.controlRepository());
        node.put("workflow_sha", context.workflowSha());
        node.put("policy_sha256", resolution.policyDigest());
        node.put("resolved_at", Instant.now(clock).truncatedTo(ChronoUnit.SECONDS).toString());
        ObjectNode source = node.putObject("policy_source");
        source.put("repository", resolution.controlRepository());
        source.put("path", ".github/workflows/cao.json");
        source.put("sha", context.workflowSha());
        return node;
    }

    private static ObjectNode denied(Resolution resolution, Context context, Clock clock) {
        ObjectNode node = base(resolution, context, clock);
        node.put("authorized", false);
        node.put("reason", resolution.reason());
        node.put("enabled", false);
        node.put("effective_max_repos", 0);
        node.putArray("candidate_repositories");
        node.putArray("worker_workflows");
        return node;
    }

    /** Policy admitted the run but inventory is unavailable: no partial fleet execution. */
    private static ObjectNode incomplete(Resolution resolution, Context context, Clock clock) {
        ObjectNode node = base(resolution, context, clock);
        node.put("authorized", true);
        node.put("activation_ready", false);
        node.put("reason", "inventory-unavailable");
        node.put("enabled", true);
        node.put("inventory_status", "incomplete");
        node.put("repo_error", INCOMPLETE_MESSAGE);
        node.put("effective_max_repos", 0);
        node.putArray("candidate_repositories");
        node.putArray("worker_workflows");
        return node;
    }
}
