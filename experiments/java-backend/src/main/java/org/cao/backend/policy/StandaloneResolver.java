package org.cao.backend.policy;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.regex.Pattern;

/**
 * Fail-closed resolver for standalone workflows (specs/control-architecture.md CAO-EXE-005, §5.4, §6.2).
 *
 * <p>The workflow slug must come from static source and match the compiled workflow reference. Only
 * {@code control-plane.workflows.<slug>} is consulted: campaign declarations never satisfy a standalone
 * identity. Dispatch requests can only narrow the persistent result.
 */
public final class StandaloneResolver {
    private static final Pattern SLUG = Pattern.compile("^[a-z0-9]+(?:-[a-z0-9]+)*$");
    private static final Pattern SHA = Pattern.compile("^[0-9a-f]{40}$");

    private StandaloneResolver() { }

    public static StandaloneResolution resolve(ControlPolicy policy, StandaloneRequest request) {
        if (policy == null || request == null) {
            throw new PolicyException("policy and request are required");
        }
        PolicyResolver.requireRepository(request.controlRepository(), "control repository");
        if (request.workflow() == null || !SLUG.matcher(request.workflow()).matches()) {
            throw new PolicyException("workflow identity must be a static kebab-case slug");
        }
        requireWorkflowIdentity(request);
        if (request.workflowSha() == null || !SHA.matcher(request.workflowSha()).matches()) {
            throw new PolicyException("workflow SHA must be a 40-character lowercase hex commit");
        }
        if (isPresent(request.correlationId()) || isPresent(request.centralRepository())
                || isPresent(request.controlPlaneRunUrl())) {
            throw new PolicyException("standalone workflows must not carry a dispatcher envelope");
        }
        if ("debug".equals(request.safeOutputMode())) {
            throw new PolicyException("debug requires a manual workflow_dispatch worker run");
        }
        ControlPolicy.Workflow workflow = policy.workflows().get(request.workflow());
        if (workflow == null) {
            return StandaloneResolution.denied("workflow-undeclared", request, policy.digest());
        }
        if (!workflow.enabled()) {
            return StandaloneResolution.denied("workflow-disabled", request, policy.digest());
        }
        if (isPresent(request.targetRepository())) {
            PolicyResolver.requireRepository(request.targetRepository(), "target_repo");
            PolicyResolver.validateScope(policy.scope(), request.controlRepository(), request.targetRepository());
        }

        Mode mode = PolicyResolver.first(workflow.mode(), policy.defaults().mode(), Mode.REVIEW);
        Map<String, Mode> targets = new LinkedHashMap<>(workflow.targets());
        if (isPresent(request.targetRepository())) {
            Mode targetMode = targets.get(request.targetRepository().toLowerCase(Locale.ROOT));
            if (targetMode != null) {
                mode = targetMode;
            }
        }
        if (workflow.maxMode() != null) {
            mode = mode.lesser(workflow.maxMode());
            for (Map.Entry<String, Mode> entry : targets.entrySet()) {
                entry.setValue(entry.getValue().lesser(workflow.maxMode()));
            }
        }
        if (isPresent(request.safeOutputMode())) {
            Mode requested = Mode.parse(request.safeOutputMode(), "safe_output_mode");
            if (requested.ordinal() > mode.ordinal()) {
                throw new PolicyException("safe_output_mode exceeds checked-in policy");
            }
            mode = requested;
            for (Map.Entry<String, Mode> entry : targets.entrySet()) {
                entry.setValue(entry.getValue().lesser(requested));
            }
        }
        int maxRepositories = PolicyResolver.narrow(
                PolicyResolver.first(workflow.maxRepositories(), policy.defaults().maxRepositories(), 1),
                request.maxRepositories(), "max_repositories", 1000);
        int rollout = PolicyResolver.narrow(
                PolicyResolver.first(workflow.rolloutPercent(), policy.defaults().rolloutPercent(), 100),
                request.rolloutPercent(), "rollout_percent", 100);

        List<String> owners = policy.scope().owners().isEmpty()
                ? List.of(request.controlRepository().substring(0, request.controlRepository().indexOf('/')))
                : policy.scope().owners();
        return new StandaloneResolution(true, "authorized", request.workflow(), mode, maxRepositories, rollout,
                Map.copyOf(targets), owners, policy.scope().repositories(), policy.inventory(),
                request.controlRepository(), policy.digest());
    }

    private static void requireWorkflowIdentity(StandaloneRequest request) {
        String prefix = request.controlRepository() + "/.github/workflows/" + request.workflow() + ".lock.yml@";
        String ref = request.workflowRef();
        if (ref == null || !ref.startsWith(prefix) || ref.length() == prefix.length()) {
            throw new PolicyException("standalone workflow identity must match GITHUB_WORKFLOW_REF");
        }
    }

    static boolean isPresent(String value) {
        return value != null && !value.isEmpty();
    }
}
