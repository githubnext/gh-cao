package org.cao.backend.policy;

import java.util.List;
import java.util.Locale;
import java.util.regex.Pattern;

/**
 * Fail-closed resolver for campaign orchestrators and workers
 * (specs/control-architecture.md §5.4, §6.2, §7, Appendix B).
 *
 * <p>Precedence: schema defaults, control defaults, campaign, exact target, worker ceiling; dispatch
 * requests can then only narrow the result.
 */
public final class PolicyResolver {
    private static final Pattern REPOSITORY = Pattern.compile("^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$");
    private static final Pattern SLUG = Pattern.compile("^[a-z0-9]+(?:-[a-z0-9]+)*$");

    private PolicyResolver() { }

    public static Resolution resolve(ControlPolicy policy, ResolutionRequest request) {
        if (policy == null || request == null || request.role() == null) {
            throw new PolicyException("policy and request are required");
        }
        requireRepository(request.controlRepository(), "control repository");
        if (request.campaign() == null || !SLUG.matcher(request.campaign()).matches()) {
            throw new PolicyException("campaign identity must be a static kebab-case slug");
        }
        if ("debug".equals(request.safeOutputMode())) {
            throw new PolicyException("debug requires verified manual-dispatch evidence, which is not supported yet");
        }
        ControlPolicy.Campaign campaign = policy.campaigns().get(request.campaign());
        if (campaign == null) {
            return Resolution.denied("campaign-undeclared", request, policy.digest());
        }
        if (!campaign.enabled()) {
            return Resolution.denied("campaign-disabled", request, policy.digest());
        }

        Mode ceiling = null;
        if (request.role() == Role.WORKER) {
            if (request.worker() == null || !SLUG.matcher(request.worker()).matches()) {
                throw new PolicyException("worker identity must be a static kebab-case slug");
            }
            ControlPolicy.Worker worker = campaign.workers().get(request.worker());
            if (worker == null) {
                throw new PolicyException("unknown worker: " + request.campaign() + "/" + request.worker());
            }
            if (!worker.enabled()) {
                return Resolution.denied("worker-disabled", request, policy.digest());
            }
            ceiling = worker.maxMode();
            requireRepository(request.targetRepository(), "target_repo");
            validateScope(policy.scope(), request.controlRepository(), request.targetRepository());
        } else if (request.worker() != null || request.targetRepository() != null) {
            throw new PolicyException("an orchestrator request must not name a worker or a single target");
        }

        Mode mode = first(campaign.mode(), policy.defaults().mode(), Mode.REVIEW);
        if (request.role() == Role.WORKER) {
            Mode targetMode = campaign.targets().get(request.targetRepository().toLowerCase(Locale.ROOT));
            if (targetMode != null) {
                mode = targetMode;
            }
        }
        if (ceiling != null) {
            mode = mode.lesser(ceiling);
        }
        if (request.safeOutputMode() != null && !request.safeOutputMode().isEmpty()) {
            Mode requested = Mode.parse(request.safeOutputMode(), "safe_output_mode");
            if (requested.ordinal() > mode.ordinal()) {
                throw new PolicyException("safe_output_mode exceeds checked-in policy");
            }
            mode = requested;
        }
        int maxRepositories = narrow(first(campaign.maxRepositories(), policy.defaults().maxRepositories(), 1),
                request.maxRepositories(), "max_repositories", 1000);
        int rollout = narrow(first(campaign.rolloutPercent(), policy.defaults().rolloutPercent(), 100),
                request.rolloutPercent(), "rollout_percent", 100);
        return new Resolution(true, "authorized", request.role(), request.campaign(), request.worker(),
                request.targetRepository(), mode, maxRepositories, rollout, request.controlRepository(),
                policy.digest());
    }

    /** Owners default to the control repository owner; owner and repository lists are cumulative. */
    static void validateScope(ControlPolicy.Scope scope, String controlRepository, String target) {
        String normalized = target.toLowerCase(Locale.ROOT);
        String owner = normalized.substring(0, normalized.indexOf('/'));
        List<String> owners = scope.owners().isEmpty()
                ? List.of(controlRepository.substring(0, controlRepository.indexOf('/')))
                : scope.owners();
        if (owners.stream().noneMatch(candidate -> candidate.equalsIgnoreCase(owner))) {
            throw new PolicyException("target_repo owner is outside control-plane.scope.allowed-owners");
        }
        if (!scope.repositories().isEmpty()
                && scope.repositories().stream().noneMatch(candidate -> candidate.equalsIgnoreCase(normalized))) {
            throw new PolicyException("target_repo is not allowed");
        }
    }

    private static int narrow(int limit, Integer requested, String name, int maximum) {
        if (requested == null) {
            return limit;
        }
        if (requested < 1 || requested > maximum) {
            throw new PolicyException(name + " must be an integer in 1.." + maximum);
        }
        if (requested > limit) {
            throw new PolicyException(name + " exceeds checked-in policy");
        }
        return requested;
    }

    private static void requireRepository(String value, String name) {
        if (value == null || !REPOSITORY.matcher(value).matches()) {
            throw new PolicyException(name + " must use owner/repository form");
        }
    }

    @SafeVarargs
    private static <T> T first(T... values) {
        for (T value : values) {
            if (value != null) {
                return value;
            }
        }
        throw new IllegalStateException("no default");
    }
}
