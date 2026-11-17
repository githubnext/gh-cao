package org.cao.backend.policy;

import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Optional;
import java.util.Set;
import java.util.function.Predicate;
import java.util.regex.Pattern;

/**
 * Role-independent authority kernel for direct operations declared under {@code control-plane.operations}
 * (specs/control-architecture.md CAO-EXE-006, §4.6, §5.3, §5.4). It performs no discovery, credential
 * acquisition, external request or dispatch: requests and the verified execution binding are separate inputs.
 */
public final class DirectOperationResolver {
    private static final Pattern SLUG = Pattern.compile("^[a-z0-9]+(?:-[a-z0-9]+)*$");

    private DirectOperationResolver() { }

    /**
     * Explicit target request; absent optional values are null.
     *
     * @param mode requested mode; null means review (direct operations default to review)
     * @param destination requested output destination; null selects the target (live) or the control repository
     * @param rolloutPercent must be absent: direct operations have no rollout percentage
     */
    public record Request(int version, String operation, List<String> targets, String mode, String destination,
            Integer maxTargets, Integer rolloutPercent, DispatcherInputs dispatcher) { }

    /** Stable outcome; {@code envelope} is present only when authorized. */
    public record Decision(boolean authorized, String reason, String operation, AuthorityEnvelope envelope) { }

    public static Decision resolve(ControlPolicy policy, Request request, ExecutionBinding binding) {
        if (policy == null || request == null || binding == null) {
            throw new PolicyException("policy, request and execution binding are required");
        }
        if (request.version() != AuthorityEnvelope.VERSION) {
            throw new PolicyException("unsupported operation request version");
        }
        String operation = request.operation();
        if (operation == null || !SLUG.matcher(operation).matches()) {
            throw new PolicyException("operation requires an exact workflow identity");
        }
        binding.requireWorkflowIdentity(operation, "operation");
        (request.dispatcher() == null ? DispatcherInputs.none() : request.dispatcher())
                .requireAbsent("direct operations");
        if (request.rolloutPercent() != null) {
            throw new PolicyException("direct operations do not use rollout_percent");
        }
        if ("debug".equals(request.mode())) {
            throw new PolicyException("debug is restricted to manual single-target workers");
        }
        ControlPolicy.Declaration declaration = policy.operations().get(operation);
        if (declaration == null) {
            return denied("operation-undeclared", operation);
        }
        if (!declaration.enabled()) {
            return denied("operation-disabled", operation);
        }
        List<String> targets = request.targets();
        if (targets == null || targets.isEmpty() || targets.size() > 1000) {
            throw new PolicyException("operation requires 1 through 1000 explicit targets");
        }
        Mode base = PolicyResolver.first(declaration.mode(), policy.defaults().mode(), Mode.REVIEW);
        if (declaration.maxMode() != null) {
            base = base.lesser(declaration.maxMode());
        }
        Mode requested = request.mode() == null || request.mode().isEmpty()
                ? Mode.REVIEW : Mode.parse(request.mode(), "safe_output_mode");
        if (requested.ordinal() > base.ordinal()) {
            throw new PolicyException("safe_output_mode exceeds checked-in policy");
        }
        int maxTargets = PolicyResolver.narrow(PolicyResolver.first(declaration.maxRepositories(),
                policy.defaults().maxRepositories(), 1), request.maxTargets(), "max_repositories", 1000);
        if (targets.size() > maxTargets) {
            throw new PolicyException("operation targets exceed checked-in policy");
        }
        Map<String, Mode> exact = new LinkedHashMap<>(declaration.targets());
        List<AuthorityEnvelope.Grant> grants = new ArrayList<>();
        Set<String> seen = new HashSet<>();
        for (String target : targets) {
            PolicyResolver.requireRepository(target, "target_repo");
            PolicyResolver.validateScope(policy.scope(), binding.repository(), target);
            if (!seen.add(target.toLowerCase(Locale.ROOT))) {
                throw new PolicyException("duplicate envelope target");
            }
            Mode targetMode = exact.getOrDefault(target.toLowerCase(Locale.ROOT), requested)
                    .lesser(requested);
            if (declaration.maxMode() != null) {
                targetMode = targetMode.lesser(declaration.maxMode());
            }
            String destination = request.destination() == null || request.destination().isEmpty()
                    ? (targetMode == Mode.LIVE ? target : binding.repository()) : request.destination();
            PolicyResolver.requireRepository(destination, "output destination");
            StandaloneResolver.requireDestinationOwner(policy.scope(), binding.repository(), destination);
            if (targetMode == Mode.LIVE && !destination.equalsIgnoreCase(target)) {
                throw new PolicyException("live operation destination must equal target");
            }
            grants.add(new AuthorityEnvelope.Grant(target, targetMode, destination));
        }
        AuthorityEnvelope envelope = new AuthorityEnvelope(AuthorityEnvelope.VERSION, operation, binding,
                maxTargets, grants, AuthorityEnvelope.REQUIRED_CONTROLS, false)
                .validate(binding, AuthorityEnvelope.REQUIRED_CONTROLS);
        AuthorityEnvelope.Grant first = grants.get(0);
        if (first.mode() != requested) {
            throw new PolicyException("requested mode is not permitted for every operation target");
        }
        if (grants.stream().anyMatch(grant -> grant.mode() != first.mode()
                || !grant.destination().equalsIgnoreCase(first.destination()))) {
            throw new PolicyException("gh-aw direct operations require a common mode and output destination");
        }
        if (first.mode() == Mode.LIVE && grants.size() != 1) {
            throw new PolicyException("live direct operations require one target_repo");
        }
        return new Decision(true, "authorized", operation, envelope);
    }

    /**
     * Re-resolves current policy and compares it with the admitted envelope (precompute). Any difference, in
     * either direction, fails closed: the admitted envelope must equal the one the current request and
     * exact-revision policy produce.
     */
    public static void requireHandoff(AuthorityEnvelope admitted, Decision resolved, ExecutionBinding binding) {
        if (admitted == null || !resolved.authorized()) {
            throw new PolicyException("operation handoff is not authorized");
        }
        admitted.validate(binding, AuthorityEnvelope.REQUIRED_CONTROLS);
        AuthorityEnvelope.requireNarrowing(resolved.envelope(), admitted, AuthorityEnvelope.REQUIRED_CONTROLS);
        if (!admitted.equals(resolved.envelope())) {
            throw new PolicyException("operation handoff does not match current request and exact-revision policy");
        }
    }

    /**
     * Checks required repository identity evidence for every grant before execution: redirects (renamed
     * repositories), archived or disabled targets, duplicate identities and missing evidence all fail closed.
     * The destination check is a caller-supplied adapter (for example proving review-repository access).
     */
    public static List<RepositoryEvidence> prepare(AuthorityEnvelope envelope, RepositoryEvidence.Directory directory,
            Predicate<AuthorityEnvelope.Grant> destinationAllowed) {
        envelope.validate(envelope.binding(), AuthorityEnvelope.REQUIRED_CONTROLS);
        List<RepositoryEvidence> admitted = new ArrayList<>();
        Set<Long> ids = new HashSet<>();
        for (AuthorityEnvelope.Grant grant : envelope.grants()) {
            Optional<RepositoryEvidence> evidence = directory.load(grant.target());
            if (evidence.isEmpty() || evidence.get().fullName() == null
                    || !evidence.get().fullName().equalsIgnoreCase(grant.target())
                    || evidence.get().id() < 1 || evidence.get().archived() || evidence.get().disabled()) {
                throw new PolicyException(
                        "operation target is redirected, archived, disabled or lacks required evidence");
            }
            if (!ids.add(evidence.get().id())) {
                throw new PolicyException("operation targets resolve to duplicate repositories");
            }
            if (!destinationAllowed.test(grant)) {
                throw new PolicyException("output destination is not allowed");
            }
            admitted.add(evidence.get());
        }
        return List.copyOf(admitted);
    }

    private static Decision denied(String reason, String operation) {
        return new Decision(false, reason, operation, null);
    }
}
