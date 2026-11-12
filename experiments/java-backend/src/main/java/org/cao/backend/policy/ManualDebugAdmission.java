package org.cao.backend.policy;

import com.fasterxml.jackson.databind.node.ObjectNode;
import java.time.Clock;
import java.util.Locale;
import java.util.Set;

/**
 * Admission of a single-run manual debug worker (specs/control-architecture.md CAO-EXE-004, §6.3,
 * T-EXE-002..005, T-RES-008). Debug is never a policy value: ordinary policy resolution (identity,
 * enablement, scope, limits) still applies and the debug mode only stages safe outputs.
 */
public final class ManualDebugAdmission {
    private static final Set<String> WRITE_PERMISSIONS = Set.of("write", "maintain", "admin");
    private static final Set<String> DISPATCHER_INPUTS = Set.of("correlation_id", "central_repo", "control_plane_run_url");
    private static final Set<String> ALLOWED_INPUTS =
            Set.of("target_repo", "safe_output_repo", "safe_output_mode", "batch_label");

    private ManualDebugAdmission() { }

    /**
     * Returns the effective record for an admitted or expected-denied run; throws {@link PolicyException}
     * for integrity failures. {@code request.safeOutputMode()} must be null: debug comes only from evidence.
     */
    public static ObjectNode admit(ControlPolicy policy, ResolutionRequest request, ManualDebugEvidence evidence,
            String workflowSha, Clock clock) {
        if (policy == null || request == null || evidence == null) {
            throw new PolicyException("policy, request and launch evidence are required");
        }
        if (request.role() != Role.WORKER) {
            throw new PolicyException("debug is only available to an individually launched worker");
        }
        if (request.safeOutputMode() != null) {
            throw new PolicyException("debug must come from launch evidence, not the resolution request");
        }
        verifyLaunch(request, evidence);
        Resolution resolution = PolicyResolver.resolve(policy, request);
        if (!resolution.authorized()) {
            return EffectiveRecord.build(policy, resolution, null, workflowSha, clock);
        }
        ObjectNode record = EffectiveRecord.build(policy, resolution, DispatchEnvelope.from(resolution),
                workflowSha, clock);
        record.remove("dispatch_envelope");
        record.put("safe_output_mode", "debug");
        record.put("launch_kind", "manual-debug");
        record.put("debug_actor", evidence.actor());
        record.put("safe_outputs_staged", true);
        record.put("correlation_id", "");
        record.put("central_repo", "");
        record.put("control_plane_run_url", "");
        return record;
    }

    private static void verifyLaunch(ResolutionRequest request, ManualDebugEvidence evidence) {
        if (!"workflow_dispatch".equals(evidence.eventName())) {
            throw new PolicyException("debug requires a workflow_dispatch event");
        }
        if (blank(evidence.actor()) || blank(evidence.triggeringActor()) || blank(evidence.actorType())
                || blank(evidence.actorPermission()) || evidence.inputs() == null) {
            throw new PolicyException("debug launch evidence is unavailable");
        }
        if (!"User".equals(evidence.actorType()) || evidence.actor().endsWith("[bot]")) {
            throw new PolicyException("debug requires a human actor");
        }
        if (!evidence.actor().equals(evidence.triggeringActor())) {
            throw new PolicyException("debug rerun by a different triggering actor is not admitted");
        }
        if (!WRITE_PERMISSIONS.contains(evidence.actorPermission().toLowerCase(Locale.ROOT))) {
            throw new PolicyException("debug actor lacks write access to the control repository");
        }
        for (String key : evidence.inputs().keySet()) {
            if (DISPATCHER_INPUTS.contains(key) && !blank(evidence.inputs().get(key))) {
                throw new PolicyException("dispatcher correlation input " + key + " must be absent for debug");
            }
            if (!DISPATCHER_INPUTS.contains(key) && !ALLOWED_INPUTS.contains(key)) {
                throw new PolicyException("unexpected debug input: " + key);
            }
        }
        String target = evidence.inputs().get("target_repo");
        if (blank(target) || !target.equalsIgnoreCase(request.targetRepository())) {
            throw new PolicyException("debug target_repo must match the requested target");
        }
        if (!target.equalsIgnoreCase(evidence.inputs().get("safe_output_repo"))) {
            throw new PolicyException("debug safe_output_repo must match target_repo");
        }
        if (!"debug".equals(evidence.inputs().get("safe_output_mode"))) {
            throw new PolicyException("event safe_output_mode must be debug");
        }
    }

    private static boolean blank(String value) {
        return value == null || value.isBlank();
    }
}
