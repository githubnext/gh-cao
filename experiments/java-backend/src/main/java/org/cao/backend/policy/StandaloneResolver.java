package org.cao.backend.policy;

import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.regex.Pattern;

/**
 * Fail-closed resolver for standalone agentic workflows declared under {@code control-plane.workflows}
 * (specs/control-architecture.md CAO-EXE-005, §5.3, §5.4). A standalone workflow has no campaign, worker or
 * dispatcher envelope; it processes only the deterministic subset admitted from bounded inventory evidence.
 */
public final class StandaloneResolver {
    private static final Pattern SLUG = Pattern.compile("^[a-z0-9]+(?:-[a-z0-9]+)*$");

    private StandaloneResolver() { }

    /**
     * Static identity plus narrowing-only requests; request fields are null when absent.
     *
     * @param workflowRef the {@code GITHUB_WORKFLOW_REF} value the slug must match
     * @param reviewRepository requested review destination, defaulting to the control repository
     */
    public record Request(String controlRepository, String workflow, String workflowRef, String safeOutputMode,
            Integer maxRepositories, Integer rolloutPercent, String reviewRepository,
            DispatcherInputs dispatcher) { }

    /** Result of policy resolution; denials carry a stable Appendix B style reason. */
    public record Resolution(boolean authorized, String reason, String workflow, Mode mode, int maxRepositories,
            int rolloutPercent, Map<String, Mode> targetModes, String reviewRepository, String controlRepository,
            String policyDigest) { }

    /** One admitted target; every target carries its own mode and output destination. */
    public record Admitted(String fullName, long id, Mode mode, String destination) { }

    public static Resolution resolve(ControlPolicy policy, Request request) {
        if (policy == null || request == null) {
            throw new PolicyException("policy and request are required");
        }
        PolicyResolver.requireRepository(request.controlRepository(), "control repository");
        if (request.workflow() == null || !SLUG.matcher(request.workflow()).matches()) {
            throw new PolicyException("workflow identity must be a static kebab-case slug");
        }
        String prefix = request.controlRepository() + "/.github/workflows/" + request.workflow() + ".lock.yml@";
        if (request.workflowRef() == null || !request.workflowRef().startsWith(prefix)
                || request.workflowRef().length() == prefix.length()) {
            throw new PolicyException("standalone workflow identity must match GITHUB_WORKFLOW_REF");
        }
        (request.dispatcher() == null ? DispatcherInputs.none() : request.dispatcher())
                .requireAbsent("standalone workflows");
        if (policy.campaigns().containsKey(request.workflow()) || policy.operations().containsKey(request.workflow())
                || policy.campaigns().values().stream().anyMatch(campaign -> campaign.workers().values().stream()
                        .anyMatch(worker -> worker.workflow().equals(request.workflow())))) {
            throw new PolicyException("workflow identity conflicts with a campaign or operation declaration");
        }
        ControlPolicy.Declaration declaration = policy.workflows().get(request.workflow());
        if (declaration == null) {
            return denied("workflow-undeclared", request, policy.digest());
        }
        if (!declaration.enabled()) {
            return denied("workflow-disabled", request, policy.digest());
        }
        if ("debug".equals(request.safeOutputMode())) {
            throw new PolicyException("debug is restricted to manual single-target workers");
        }
        Mode base = PolicyResolver.first(declaration.mode(), policy.defaults().mode(), Mode.REVIEW);
        if (declaration.maxMode() != null) {
            base = base.lesser(declaration.maxMode());
        }
        Mode requested = null;
        if (request.safeOutputMode() != null && !request.safeOutputMode().isEmpty()) {
            requested = Mode.parse(request.safeOutputMode(), "safe_output_mode");
            if (requested.ordinal() > base.ordinal()) {
                throw new PolicyException("safe_output_mode exceeds checked-in policy");
            }
        }
        Map<String, Mode> targetModes = new java.util.LinkedHashMap<>();
        for (Map.Entry<String, Mode> target : declaration.targets().entrySet()) {
            Mode mode = declaration.maxMode() == null ? target.getValue() : target.getValue().lesser(declaration.maxMode());
            targetModes.put(target.getKey(), requested == null ? mode : mode.lesser(requested));
        }
        int maxRepositories = PolicyResolver.narrow(PolicyResolver.first(declaration.maxRepositories(),
                policy.defaults().maxRepositories(), 1), request.maxRepositories(), "max_repositories", 1000);
        int rollout = PolicyResolver.narrow(PolicyResolver.first(declaration.rolloutPercent(),
                policy.defaults().rolloutPercent(), 100), request.rolloutPercent(), "rollout_percent", 100);
        String review = request.reviewRepository() == null || request.reviewRepository().isEmpty()
                ? request.controlRepository() : request.reviewRepository();
        PolicyResolver.requireRepository(review, "safe_output_repo");
        requireDestinationOwner(policy.scope(), request.controlRepository(), review);
        return new Resolution(true, "authorized", request.workflow(), requested == null ? base : requested,
                maxRepositories, rollout, Map.copyOf(targetModes), review, request.controlRepository(),
                policy.digest());
    }

    /**
     * Admits the deterministic subset of the supplied inventory. A null inventory is unavailable evidence and
     * fails closed; an empty result permits only no-op execution. Archived and disabled repositories are
     * excluded, candidates are ordered by lowercase name, and the subset is capped by {@code max-repositories}
     * and {@code rollout-percent}. Cell and batch partitioning of the inventory is not implemented.
     */
    public static List<Admitted> admit(ControlPolicy policy, Resolution resolution,
            List<RepositoryEvidence> inventory) {
        if (!resolution.authorized()) {
            throw new PolicyException("a denied resolution admits no targets");
        }
        if (inventory == null) {
            throw new PolicyException("standalone inventory unavailable");
        }
        List<RepositoryEvidence> candidates = new ArrayList<>();
        for (RepositoryEvidence evidence : inventory) {
            PolicyResolver.requireRepository(evidence.fullName(), "inventory repository");
            if (evidence.id() < 1) {
                throw new PolicyException("inventory repository lacks required identity evidence");
            }
            if (!evidence.archived() && !evidence.disabled()) {
                candidates.add(evidence);
            }
        }
        java.util.Set<Long> ids = new java.util.HashSet<>();
        for (RepositoryEvidence candidate : candidates) {
            if (!ids.add(candidate.id())) {
                throw new PolicyException("inventory resolves duplicate repositories");
            }
        }
        candidates.sort(Comparator.comparing(candidate -> candidate.fullName().toLowerCase(Locale.ROOT)));
        long cap = candidates.isEmpty() ? 0
                : Math.max(1, (candidates.size() * (long) resolution.rolloutPercent() + 99) / 100);
        int maximum = (int) Math.min(resolution.maxRepositories(), cap);
        List<Admitted> admitted = new ArrayList<>();
        for (RepositoryEvidence candidate : candidates.subList(0, maximum)) {
            PolicyResolver.validateScope(policy.scope(), resolution.controlRepository(), candidate.fullName());
            Mode mode = resolution.targetModes().getOrDefault(candidate.fullName().toLowerCase(Locale.ROOT),
                    resolution.mode());
            admitted.add(new Admitted(candidate.fullName(), candidate.id(), mode,
                    mode == Mode.LIVE ? candidate.fullName() : resolution.reviewRepository()));
        }
        return List.copyOf(admitted);
    }

    static void requireDestinationOwner(ControlPolicy.Scope scope, String controlRepository, String destination) {
        String owner = destination.substring(0, destination.indexOf('/'));
        List<String> owners = scope.owners().isEmpty()
                ? List.of(controlRepository.substring(0, controlRepository.indexOf('/'))) : scope.owners();
        if (owners.stream().noneMatch(candidate -> candidate.equalsIgnoreCase(owner))) {
            throw new PolicyException("output destination is not allowed");
        }
    }

    private static Resolution denied(String reason, Request request, String digest) {
        return new Resolution(false, reason, request.workflow(), Mode.REVIEW, 0, 0, Map.of(), "",
                request.controlRepository(), digest);
    }
}
