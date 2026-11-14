package org.cao.backend.policy;

import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Locale;
import java.util.Set;
import java.util.regex.Pattern;

/**
 * Orchestrator resolution beyond campaign admission (specs/control-architecture.md §4.4, §5.3, §6.2 steps 4-5,
 * §6.3): bounded inventory, deterministic partition, per-target mode, eligible worker workflows and the
 * effective repository cap. Archived and disabled repositories are excluded after partitioning, which only
 * narrows the candidate set. Every input that cannot be proven fails closed with {@link PolicyException}.
 */
public final class OrchestratorPlanner {
    private static final Pattern REPOSITORY = Pattern.compile("^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$");
    private static final Pattern SLUG = Pattern.compile("^[a-z0-9]+(?:-[a-z0-9]+)*$");

    private OrchestratorPlanner() { }

    /** An admitted candidate with the mode that applies to it alone. */
    public record Candidate(RepositoryMetadata repository, Mode mode) { }

    /** Result of resolving one configured worker workflow. */
    public record WorkerWorkflow(
            String configured,
            boolean matched,
            String worker,
            boolean policyEnabled,
            Mode maxMode,
            OrchestratorEvidence.WorkflowState installed,
            boolean eligible,
            String skipReason) { }

    public record Plan(
            Resolution resolution,
            OrchestratorEvidence.Source source,
            ControlPolicy.Inventory limits,
            InventoryPartition partition,
            List<Candidate> candidates,
            List<WorkerWorkflow> workers,
            int dispatchMax,
            int effectiveMaxRepositories,
            String safeOutputRepository,
            boolean campaignEnabled) { }

    public static Plan plan(ControlPolicy policy, ResolutionRequest request, OrchestratorEvidence evidence) {
        if (request == null || request.role() != Role.ORCHESTRATOR) {
            throw new PolicyException("orchestrator planning requires an orchestrator request");
        }
        Resolution resolution = PolicyResolver.resolve(policy, request);
        if (!resolution.authorized()) {
            throw new PolicyException("a denied orchestrator has no plan: " + resolution.reason());
        }
        if (evidence == null || evidence.repositories() == null) {
            throw new PolicyException("required repository inventory is unavailable");
        }
        if (evidence.dispatchMax() < 1 || evidence.dispatchMax() > 1000) {
            throw new PolicyException("dispatch_max must be an integer from 1 through 1000");
        }
        ControlPolicy.Campaign campaign = policy.campaigns().get(request.campaign());
        checkInventoryScope(policy.scope(), request.controlRepository(), evidence);
        checkDestination(policy.scope(), resolution, request.controlRepository(), evidence);

        InventoryPartition partition = InventoryPartition.of(evidence.repositories(), policy.inventory());
        List<Candidate> candidates = new ArrayList<>();
        Mode requested = request.safeOutputMode() == null || request.safeOutputMode().isEmpty()
                ? null : Mode.parse(request.safeOutputMode(), "safe_output_mode");
        for (RepositoryMetadata repository : partition.candidates()) {
            if (repository.archived() || repository.disabled()) {
                continue;
            }
            Mode target = campaign.targets().get(repository.fullName().toLowerCase(Locale.ROOT));
            Mode mode = target == null ? resolution.mode() : requested == null ? target : target.lesser(requested);
            candidates.add(new Candidate(repository, mode));
        }
        List<WorkerWorkflow> workers = workers(campaign, evidence);
        int eligible = (int) workers.stream().filter(WorkerWorkflow::eligible).count();
        int percentCap = candidates.isEmpty() ? 0
                : Math.max(1, (candidates.size() * resolution.rolloutPercent() + 99) / 100);
        int perTarget = Math.min(eligible, evidence.dispatchMax());
        int dispatchCap = perTarget == 0 ? 0 : evidence.dispatchMax() / perTarget;
        int effective = Math.min(resolution.maxRepositories(), Math.min(percentCap, dispatchCap));
        return new Plan(resolution, evidence.source(), policy.inventory(), partition, List.copyOf(candidates),
                List.copyOf(workers), evidence.dispatchMax(), effective, evidence.safeOutputRepository(),
                campaign.enabled());
    }

    private static void checkInventoryScope(ControlPolicy.Scope scope, String control, OrchestratorEvidence evidence) {
        boolean exactList = !scope.repositories().isEmpty();
        if (exactList != (evidence.source() == OrchestratorEvidence.Source.ALLOWED_REPOSITORIES)) {
            throw new PolicyException("inventory source must be allowed_repos exactly when allowed-repositories is set");
        }
        Set<String> seen = new HashSet<>();
        for (RepositoryMetadata repository : evidence.repositories()) {
            if (!PolicyResolver.inScope(scope, control, repository.fullName())) {
                throw new PolicyException("inventory contains a repository outside control-plane.scope");
            }
            seen.add(repository.fullName().toLowerCase(Locale.ROOT));
        }
        if (exactList) {
            Set<String> expected = new HashSet<>();
            scope.repositories().forEach(name -> expected.add(name.toLowerCase(Locale.ROOT)));
            if (!expected.equals(seen)) {
                throw new PolicyException("inventory must contain exactly the allowed repositories");
            }
        }
    }

    private static void checkDestination(ControlPolicy.Scope scope, Resolution resolution, String control,
            OrchestratorEvidence evidence) {
        String destination = evidence.safeOutputRepository();
        if (destination == null || !REPOSITORY.matcher(destination).matches()) {
            throw new PolicyException("safe_output_repo must use owner/repository form");
        }
        String owner = destination.substring(0, destination.indexOf('/')).toLowerCase(Locale.ROOT);
        List<String> owners = scope.owners().isEmpty()
                ? List.of(control.substring(0, control.indexOf('/'))) : scope.owners();
        if (owners.stream().noneMatch(candidate -> candidate.equalsIgnoreCase(owner))) {
            throw new PolicyException("safe_output_repo owner is outside control-plane.scope.allowed-owners");
        }
        // Review routing must reach the control repository or a verified private repository.
        if (resolution.mode() == Mode.REVIEW && !destination.equalsIgnoreCase(control)
                && !Boolean.TRUE.equals(evidence.safeOutputRepositoryPrivate())) {
            throw new PolicyException("non-central review safe_output_repo must be a verified private repository");
        }
    }

    private static List<WorkerWorkflow> workers(ControlPolicy.Campaign campaign, OrchestratorEvidence evidence) {
        if (evidence.configuredWorkflows() == null || evidence.configuredWorkflows().isEmpty()) {
            throw new PolicyException("an orchestrator requires at least one configured worker workflow");
        }
        Set<String> seen = new HashSet<>();
        List<WorkerWorkflow> result = new ArrayList<>();
        for (String configured : evidence.configuredWorkflows()) {
            if (configured == null || !SLUG.matcher(configured).matches() || !seen.add(configured)) {
                throw new PolicyException("configured worker workflows must be unique kebab-case slugs");
            }
            String workerName = null;
            ControlPolicy.Worker policy = null;
            for (var entry : campaign.workers().entrySet()) {
                if (entry.getValue().workflow().equals(configured)) {
                    workerName = entry.getKey();
                    policy = entry.getValue();
                }
            }
            OrchestratorEvidence.WorkflowState installed = evidence.installedWorkflows() == null
                    ? null : evidence.installedWorkflows().get(configured);
            boolean enabled = policy != null && policy.enabled();
            boolean active = installed != null && installed.active();
            String skip = policy == null ? "worker is not part of installed campaign"
                    : !enabled ? "worker disabled by control-plane policy"
                    : installed == null ? "worker workflow unavailable"
                    : !active ? "worker workflow disabled" : null;
            result.add(new WorkerWorkflow(configured, installed != null, workerName, enabled,
                    policy == null ? null : policy.maxMode(), installed, skip == null, skip));
        }
        return result;
    }
}
