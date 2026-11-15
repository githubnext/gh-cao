package org.cao.backend.policy;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.Clock;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashMap;
import java.util.HexFormat;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.regex.Pattern;

/**
 * Standalone workflow resolution and effective record (specs/control-architecture.md CAO-EXE-005,
 * §5.3 {@code workflows}, §5.4, §6.3, §7). The kernel performs no discovery, credential use or
 * dispatch: repository inventory and destination privacy arrive as caller-supplied evidence and
 * unavailable evidence fails closed.
 */
public final class StandaloneResolver {
    private static final Pattern SHA = Pattern.compile("^[0-9a-f]{40}$");
    private static final String POLICY_PATH = ".github/workflows/cao.json";
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private StandaloneResolver() { }

    /**
     * Returns the effective record for an admitted or expected-denied run and throws
     * {@link PolicyException} for identity, widening, scope, destination or evidence failures.
     */
    public static ObjectNode resolve(ControlPolicy policy, StandaloneRequest request, InventoryEvidence evidence,
            String workflowSha, Clock clock) {
        if (policy == null || request == null) {
            throw new PolicyException("policy and request are required");
        }
        PolicyResolver.requireRepository(request.controlRepository(), "control repository");
        if (workflowSha == null || !SHA.matcher(workflowSha).matches()) {
            throw new PolicyException("workflow SHA must be a 40-character lowercase hex commit");
        }
        verifyIdentity(request);
        if ("debug".equals(request.safeOutputMode())) {
            throw new PolicyException("debug requires a manually launched worker, not a standalone workflow");
        }
        ControlPolicy.Workflow workflow = policy.workflows().get(request.workflow());
        if (workflow == null || !workflow.enabled()) {
            return denied(workflow == null ? "workflow-undeclared" : "workflow-disabled", policy, request,
                    workflowSha, clock);
        }
        String target = blank(request.targetRepository()) ? "" : request.targetRepository();
        if (!target.isEmpty()) {
            PolicyResolver.requireRepository(target, "target_repo");
            PolicyResolver.validateScope(policy.scope(), request.controlRepository(), target);
        }
        String destination = blank(request.safeOutputRepository()) ? "" : request.safeOutputRepository();
        if (!destination.isEmpty()) {
            PolicyResolver.requireRepository(destination, "safe_output_repo");
            requireOwner(policy.scope(), request.controlRepository(), destination, "safe_output_repo");
        }

        Authority authority = authority(policy, workflow, request, target);
        int maxRepositories = PolicyResolver.narrow(
                PolicyResolver.first(workflow.maxRepositories(), policy.defaults().maxRepositories(), 1),
                request.maxRepositories(), "max_repositories", 1000);
        int rollout = PolicyResolver.narrow(
                PolicyResolver.first(workflow.rolloutPercent(), policy.defaults().rolloutPercent(), 100),
                request.rolloutPercent(), "rollout_percent", 100);

        List<InventoryRepository> repositories = verifiedInventory(policy, request, evidence, target);
        ControlPolicy.Inventory limits = policy.inventory();
        List<InventoryRepository> sorted = unique(repositories);
        List<InventoryRepository> cell = target.isEmpty() ? sorted.stream()
                .filter(repository -> repository.id() % limits.cellCount() == limits.cellIndex()).toList() : sorted;
        int batchCount = (int) ((cell.size() + (long) limits.batchSize() - 1) / limits.batchSize());
        if (batchCount > 0 && limits.batchIndex() >= batchCount) {
            throw new PolicyException("batch_index must be smaller than the selected cell batch count (" + batchCount + ")");
        }
        int from = (int) Math.min((long) limits.batchIndex() * limits.batchSize(), cell.size());
        int to = (int) Math.min((long) (limits.batchIndex() + 1) * limits.batchSize(), cell.size());
        List<InventoryRepository> active = cell.subList(from, to).stream()
                .filter(repository -> !repository.archived() && !repository.disabled()).toList();
        long percentCap = active.isEmpty() ? 0 : Math.max(1, ((long) active.size() * rollout + 99) / 100);
        int effectiveMax = (int) Math.min(maxRepositories, percentCap);

        ObjectNode record = base(policy, request, workflowSha, clock);
        record.put("authorized", true);
        record.put("reason", "authorized");
        record.put("enabled", true);
        record.put("target_repo", target);
        record.put("safe_output_mode", authority.mode.wire());
        record.put("safe_output_repo", destination);
        record.put("max_repos", maxRepositories);
        record.put("rollout_percent", rollout);
        record.put("effective_max_repos", effectiveMax);
        record.put("repo_source", evidence.source());
        record.put("repo_error", "");
        String version = "sha256:" + sha256(digestInput(sorted));
        record.put("inventory_version", version);
        record.put("inventory_repository_count", sorted.size());
        record.put("cell_count", limits.cellCount());
        record.put("cell_index", limits.cellIndex());
        record.put("cell_repository_count", cell.size());
        record.put("batch_size", limits.batchSize());
        record.put("batch_index", limits.batchIndex());
        record.put("batch_count", batchCount);
        record.put("batch_id", version + ":cell-" + limits.cellIndex() + "-of-" + limits.cellCount()
                + ":batch-" + limits.batchIndex() + "-of-" + batchCount);
        record.put("total_repositories_scanned", sorted.size());
        ArrayNode candidates = record.putArray("candidate_repositories");
        for (InventoryRepository repository : active.subList(0, effectiveMax)) {
            Mode mode = authority.targets.getOrDefault(repository.fullName().toLowerCase(Locale.ROOT), authority.mode);
            String output = mode == Mode.LIVE ? repository.fullName() : destination;
            PolicyResolver.validateScope(policy.scope(), request.controlRepository(), repository.fullName());
            validateDestination(mode, repository.fullName(), output, request, request.destinationPrivate());
            ObjectNode entry = repositoryNode(repository);
            entry.put("safe_output_mode", mode.wire());
            entry.put("safe_output_repo", output);
            candidates.add(entry);
        }
        record.putArray("worker_workflows");
        return record;
    }

    private static void verifyIdentity(StandaloneRequest request) {
        if (request.workflow() == null || !PolicyResolver.SLUG.matcher(request.workflow()).matches()) {
            throw new PolicyException("workflow identity must be a static kebab-case slug");
        }
        String reference = request.controlRepository() + "/.github/workflows/" + request.workflow() + ".lock.yml@";
        if (request.workflowRef() == null || !request.workflowRef().startsWith(reference)
                || request.workflowRef().length() == reference.length()) {
            throw new PolicyException("standalone workflow identity must match GITHUB_WORKFLOW_REF");
        }
        if (!blank(request.campaign()) || !blank(request.worker())) {
            throw new PolicyException("standalone workflows must not specify a campaign or worker identity");
        }
        if (!blank(request.correlationId()) || !blank(request.centralRepository())
                || !blank(request.controlPlaneRunUrl())) {
            throw new PolicyException("standalone workflows must not carry a dispatcher envelope");
        }
    }

    /** Resolved modes: ceiling and requests only narrow; the workflow-level mode honors an exact requested target. */
    private static Authority authority(ControlPolicy policy, ControlPolicy.Workflow workflow,
            StandaloneRequest request, String target) {
        Mode mode = PolicyResolver.first(workflow.mode(), policy.defaults().mode(), Mode.REVIEW);
        Map<String, Mode> targets = new HashMap<>(workflow.targets());
        if (!target.isEmpty() && targets.containsKey(target.toLowerCase(Locale.ROOT))) {
            mode = targets.get(target.toLowerCase(Locale.ROOT));
        }
        Mode ceiling = workflow.maxMode();
        if (ceiling != null) {
            mode = mode.lesser(ceiling);
            targets.replaceAll((repository, targetMode) -> targetMode.lesser(ceiling));
        }
        if (!blank(request.safeOutputMode())) {
            Mode requested = Mode.parse(request.safeOutputMode(), "safe_output_mode");
            if (requested.ordinal() > mode.ordinal()) {
                throw new PolicyException("safe_output_mode exceeds checked-in policy");
            }
            mode = requested;
            Mode narrowed = requested;
            targets.replaceAll((repository, targetMode) -> targetMode.lesser(narrowed));
        }
        return new Authority(mode, targets);
    }

    private static List<InventoryRepository> verifiedInventory(ControlPolicy policy, StandaloneRequest request,
            InventoryEvidence evidence, String target) {
        if (evidence == null) {
            throw new PolicyException("repository inventory evidence is unavailable");
        }
        if (!evidence.error().isEmpty()) {
            throw new PolicyException("required repository inventory could not be read");
        }
        ControlPolicy.Scope scope = policy.scope();
        int bound = policy.inventory().maxScanRepositories();
        if (!target.isEmpty()) {
            if (!"target_repo".equals(evidence.source()) || evidence.repositories().size() != 1
                    || !evidence.repositories().get(0).fullName().equalsIgnoreCase(target)) {
                throw new PolicyException("inventory evidence must be exactly the requested target_repo");
            }
        } else if (!scope.repositories().isEmpty()) {
            if (scope.repositories().size() > bound) {
                throw new PolicyException("allowed repos exceed max_scan_repos");
            }
            if (!"allowed_repos".equals(evidence.source())) {
                throw new PolicyException("inventory evidence must come from control-plane.scope.allowed-repositories");
            }
            for (InventoryRepository repository : evidence.repositories()) {
                if (scope.repositories().stream().noneMatch(allowed -> allowed.equalsIgnoreCase(repository.fullName()))) {
                    throw new PolicyException("inventory evidence contains a repository outside the allowed list");
                }
            }
        } else if (!"organization".equals(evidence.source())) {
            throw new PolicyException("inventory evidence must come from the control repository owner");
        }
        if (evidence.repositories().size() > bound) {
            throw new PolicyException("inventory evidence exceeds max-scan-repositories");
        }
        return evidence.repositories();
    }

    /** Deduplicates by repository id and orders by id then name, as the deterministic partition requires. */
    private static List<InventoryRepository> unique(List<InventoryRepository> repositories) {
        Map<Long, InventoryRepository> byId = new LinkedHashMap<>();
        for (InventoryRepository repository : repositories) {
            byId.put(repository.id(), repository);
        }
        List<InventoryRepository> sorted = new ArrayList<>(byId.values());
        sorted.sort(Comparator.comparingLong(InventoryRepository::id).thenComparing(InventoryRepository::fullName));
        return sorted;
    }

    private static void validateDestination(Mode mode, String target, String destination,
            StandaloneRequest request, Boolean destinationPrivate) {
        if (mode == Mode.LIVE) {
            if (!destination.equalsIgnoreCase(target)) {
                throw new PolicyException("live standalone safe_output_repo must equal target_repo");
            }
            return;
        }
        if (destination.isEmpty()) {
            throw new PolicyException("review safe_output_repo is required");
        }
        boolean central = destination.equalsIgnoreCase(request.controlRepository());
        if (destination.equalsIgnoreCase(target) && !central) {
            throw new PolicyException("review safe_output_repo must differ from target_repo");
        }
        if (!central && !Boolean.TRUE.equals(destinationPrivate)) {
            throw new PolicyException("non-central review safe_output_repo must be private and accessible");
        }
    }

    private static void requireOwner(ControlPolicy.Scope scope, String controlRepository, String repository,
            String name) {
        List<String> owners = scope.owners().isEmpty()
                ? List.of(controlRepository.substring(0, controlRepository.indexOf('/'))) : scope.owners();
        String owner = repository.substring(0, repository.indexOf('/'));
        if (owners.stream().noneMatch(allowed -> allowed.equalsIgnoreCase(owner))) {
            throw new PolicyException(name + " owner is outside control-plane.scope.allowed-owners");
        }
    }

    private static ObjectNode denied(String reason, ControlPolicy policy, StandaloneRequest request,
            String workflowSha, Clock clock) {
        ObjectNode record = base(policy, request, workflowSha, clock);
        record.put("authorized", false);
        record.put("reason", reason);
        record.put("enabled", false);
        record.put("target_repo", "");
        record.put("safe_output_mode", Mode.REVIEW.wire());
        record.put("safe_output_repo", "");
        record.put("effective_max_repos", 0);
        record.putArray("candidate_repositories");
        record.putArray("worker_workflows");
        return record;
    }

    private static ObjectNode base(ControlPolicy policy, StandaloneRequest request, String workflowSha, Clock clock) {
        ObjectNode node = MAPPER.createObjectNode();
        node.put("schema_version", EffectiveRecord.SCHEMA_VERSION);
        node.put("control_role", "standalone");
        node.put("workflow", request.workflow());
        node.put("campaign", "");
        node.put("bundle", "");
        node.put("worker", "");
        node.put("control_repository", request.controlRepository());
        node.put("workflow_sha", workflowSha);
        node.put("policy_sha256", policy.digest());
        node.put("resolved_at", Instant.now(clock).truncatedTo(ChronoUnit.SECONDS).toString());
        ObjectNode source = node.putObject("policy_source");
        source.put("repository", request.controlRepository());
        source.put("path", POLICY_PATH);
        source.put("sha", workflowSha);
        return node;
    }

    private static ObjectNode repositoryNode(InventoryRepository repository) {
        ObjectNode node = MAPPER.createObjectNode();
        node.put("id", repository.id());
        node.put("full_name", repository.fullName());
        node.put("archived", repository.archived());
        node.put("disabled", repository.disabled());
        node.put("private", repository.isPrivate());
        node.put("pushed_at", repository.pushedAt());
        node.put("default_branch", repository.defaultBranch());
        return node;
    }

    /** One compact JSON object per line over the sorted inventory, matching the Node.js resolver's version digest. */
    private static String digestInput(List<InventoryRepository> sorted) {
        StringBuilder input = new StringBuilder();
        for (InventoryRepository repository : sorted) {
            input.append(repositoryNode(repository)).append('\n');
        }
        return input.toString();
    }

    private static String sha256(String text) {
        try {
            return HexFormat.of().formatHex(MessageDigest.getInstance("SHA-256")
                    .digest(text.getBytes(StandardCharsets.UTF_8)));
        } catch (NoSuchAlgorithmException e) {
            throw new IllegalStateException(e);
        }
    }

    private static boolean blank(String value) {
        return value == null || value.isEmpty();
    }

    private record Authority(Mode mode, Map<String, Mode> targets) { }
}
