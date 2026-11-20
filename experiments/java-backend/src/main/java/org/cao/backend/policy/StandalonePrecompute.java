package org.cao.backend.policy;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ArrayNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.time.Clock;
import java.time.Instant;
import java.time.temporal.ChronoUnit;
import java.util.List;
import java.util.Locale;

/**
 * Standalone effective record (specs/control-architecture.md §6.3, CAO-EXE-005): bounded inventory,
 * deterministic cell/batch partition, archived/disabled exclusion, {@code max-repositories} and
 * {@code rollout-percent} caps, and a per-target mode and output destination. Evidence is supplied by an
 * adapter; this class performs no discovery, credential use or network access.
 */
public final class StandalonePrecompute {
    public static final String POLICY_PATH = ".github/workflows/cao.json";
    private static final ObjectMapper MAPPER = new ObjectMapper();

    private StandalonePrecompute() { }

    public static ObjectNode build(StandaloneResolution resolution, StandaloneRequest request,
            InventoryEvidence evidence, ReviewDestinations destinations, Clock clock) {
        if (resolution == null || request == null || evidence == null || destinations == null) {
            throw new PolicyException("resolution, request, inventory evidence and destinations are required");
        }
        if (!resolution.authorized()) {
            throw new PolicyException("a denied standalone resolution produces a denial record, not a precompute");
        }
        if (!resolution.workflow().equals(request.workflow())
                || !resolution.controlRepository().equals(request.controlRepository())) {
            throw new PolicyException("standalone precompute identity must match admitted policy");
        }
        boolean hasTarget = StandaloneResolver.isPresent(request.targetRepository());
        requireOwner("target_repo", request.targetRepository(), resolution.allowedOwners());
        requireOwner("safe_output_repo", request.safeOutputRepository(), resolution.allowedOwners());
        ControlPolicy.Inventory limits = resolution.inventory();
        if (!evidence.available()) {
            throw new PolicyException("standalone inventory unavailable");
        }
        if (hasTarget) {
            requireExactTarget(evidence, request.targetRepository());
        } else if (evidence.source() == InventoryEvidence.Source.TARGET_REPO) {
            throw new PolicyException("target_repo inventory evidence was supplied without a requested target");
        }
        if (resolution.allowedRepositories().size() > limits.maxScanRepositories()
                || evidence.repositories().size() > limits.maxScanRepositories()) {
            throw new PolicyException("inventory exceeds max-scan-repositories");
        }

        InventoryPartition partition = InventoryPartition.create(limits, evidence.repositories(), hasTarget);
        List<RepositoryRecord> eligible = partition.candidates().stream()
                .filter(repository -> !repository.archived() && !repository.disabled()).toList();
        int percentCap = eligible.isEmpty() ? 0
                : Math.max(1, (eligible.size() * resolution.rolloutPercent() + 99) / 100);
        int maximum = Math.min(resolution.maxRepositories(), percentCap);

        ArrayNode admitted = MAPPER.createArrayNode();
        for (RepositoryRecord repository : eligible.subList(0, Math.min(maximum, eligible.size()))) {
            Mode mode = resolution.targetModes().getOrDefault(repository.fullName().toLowerCase(Locale.ROOT),
                    resolution.mode());
            String destination = mode == Mode.LIVE ? repository.fullName() : request.safeOutputRepository();
            PolicyResolver.validateScope(new ControlPolicy.Scope(resolution.allowedOwners(),
                    resolution.allowedRepositories()), request.controlRepository(), repository.fullName());
            validateDestination(mode, destination, repository.fullName(), request.controlRepository(), destinations);
            ObjectNode node = admitted.addObject();
            node.put("id", repository.id());
            node.put("full_name", repository.fullName());
            node.put("archived", repository.archived());
            node.put("disabled", repository.disabled());
            node.put("private", repository.isPrivate());
            node.put("pushed_at", repository.pushedAt());
            node.put("default_branch", repository.defaultBranch());
            node.put("safe_output_mode", mode.wire());
            node.put("safe_output_repo", destination);
        }

        ObjectNode node = MAPPER.createObjectNode();
        node.put("schema_version", EffectiveRecord.SCHEMA_VERSION);
        node.put("authorized", true);
        node.put("reason", "authorized");
        node.put("control_role", "standalone");
        node.put("workflow", resolution.workflow());
        node.put("campaign", "");
        node.put("worker", "");
        node.put("enabled", true);
        node.put("target_repo", hasTarget ? request.targetRepository() : "");
        node.put("safe_output_mode", resolution.mode().wire());
        node.put("safe_output_repo", StandaloneResolver.isPresent(request.safeOutputRepository())
                ? request.safeOutputRepository() : "");
        node.put("max_repos", resolution.maxRepositories());
        node.put("rollout_percent", resolution.rolloutPercent());
        node.put("effective_max_repos", maximum);
        node.put("repo_source", evidence.source().wire());
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
        node.set("candidate_repositories", admitted);
        node.putArray("worker_workflows");
        node.put("control_repository", resolution.controlRepository());
        node.put("workflow_sha", request.workflowSha());
        node.put("policy_sha256", resolution.policyDigest());
        node.put("resolved_at", Instant.now(clock).truncatedTo(ChronoUnit.SECONDS).toString());
        ObjectNode source = node.putObject("policy_source");
        source.put("repository", resolution.controlRepository());
        source.put("path", POLICY_PATH);
        source.put("sha", request.workflowSha());
        return node;
    }

    private static void requireExactTarget(InventoryEvidence evidence, String target) {
        if (evidence.source() != InventoryEvidence.Source.TARGET_REPO || evidence.repositories().size() != 1
                || !evidence.repositories().get(0).fullName().equalsIgnoreCase(target)) {
            throw new PolicyException("inventory evidence does not match the enrolled target identity");
        }
    }

    private static void requireOwner(String label, String repository, List<String> owners) {
        if (!StandaloneResolver.isPresent(repository)) {
            return;
        }
        PolicyResolver.requireRepository(repository, label);
        String owner = repository.substring(0, repository.indexOf('/'));
        if (!owners.isEmpty() && owners.stream().noneMatch(allowed -> allowed.equalsIgnoreCase(owner))) {
            throw new PolicyException(label + " owner is outside control-plane.scope.allowed-owners");
        }
    }

    /** Mirrors the Actions precompute: live is target-bound; review must be central or a readable private repo. */
    private static void validateDestination(Mode mode, String destination, String target, String control,
            ReviewDestinations destinations) {
        if (mode == Mode.LIVE) {
            return;
        }
        if (!StandaloneResolver.isPresent(destination)) {
            throw new PolicyException("review safe_output_repo must be accessible");
        }
        PolicyResolver.requireRepository(destination, "safe_output_repo");
        if (destination.equalsIgnoreCase(control)) {
            return;
        }
        if (destination.equalsIgnoreCase(target)) {
            throw new PolicyException("review safe_output_repo must differ from target_repo");
        }
        Boolean isPrivate = destinations.isPrivate(destination).orElseThrow(
                () -> new PolicyException("review safe_output_repo must be accessible"));
        if (!isPrivate) {
            throw new PolicyException("non-central review safe_output_repo must be private");
        }
    }
}
