package org.cao.backend.policy;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.fasterxml.jackson.databind.node.ObjectNode;
import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.HashMap;
import java.util.Map;
import java.util.function.UnaryOperator;
import org.junit.jupiter.api.Test;

class ManualDebugAdmissionTest {
    private static final String SHA = "0123456789abcdef0123456789abcdef01234567";
    private static final Clock CLOCK = Clock.fixed(Instant.parse("2026-01-02T03:04:05Z"), ZoneOffset.UTC);
    private static final ControlPolicy POLICY = parse("{\"scope\":{\"allowed-owners\":[\"acme\"]},"
            + "\"campaigns\":{\"dep\":{\"mode\":\"live\",\"workers\":{\"planner\":{\"workflow\":\"dep-planner\"},"
            + "\"off\":{\"workflow\":\"dep-off\",\"enabled\":false}}}}}");
    private static final ResolutionRequest REQUEST =
            new ResolutionRequest(Role.WORKER, "acme/control", "dep", "planner", "acme/web", null, null, null);

    private static ControlPolicy parse(String control) {
        return PolicyParser.parse(("{\"version\":1,\"control-plane\":" + control + "}").getBytes(StandardCharsets.UTF_8));
    }

    private static Map<String, String> inputs() {
        Map<String, String> inputs = new HashMap<>();
        inputs.put("target_repo", "acme/web");
        inputs.put("safe_output_repo", "acme/web");
        inputs.put("safe_output_mode", "debug");
        return inputs;
    }

    private static ManualDebugEvidence evidence(UnaryOperator<Map<String, String>> change) {
        return new ManualDebugEvidence("workflow_dispatch", "octo", "User", "octo", "write", change.apply(inputs()));
    }

    private static ManualDebugEvidence valid() {
        return evidence(i -> i);
    }

    private static ObjectNode admit(ResolutionRequest request, ManualDebugEvidence evidence) {
        return ManualDebugAdmission.admit(POLICY, request, evidence, SHA, CLOCK);
    }

    @Test
    void admitsHumanWriterWithDebugProvenanceAndNoDispatcherEnvelope() {
        ObjectNode record = admit(REQUEST, valid());
        assertTrue(record.get("authorized").asBoolean());
        assertEquals("debug", record.get("safe_output_mode").asText());
        assertEquals("manual-debug", record.get("launch_kind").asText());
        assertEquals("octo", record.get("debug_actor").asText());
        assertTrue(record.get("safe_outputs_staged").asBoolean());
        assertEquals("acme/control", record.get("control_repository").asText());
        assertEquals("", record.get("correlation_id").asText());
        assertEquals("", record.get("central_repo").asText());
        assertEquals("", record.get("control_plane_run_url").asText());
        assertFalse(record.has("dispatch_envelope"));
        assertEquals(0, record.get("candidate_repositories").size());
        assertEquals(0, record.get("worker_workflows").size());
    }

    @Test
    void rejectsOrchestratorAndModeInRequest() {
        ResolutionRequest orchestrator =
                new ResolutionRequest(Role.ORCHESTRATOR, "acme/control", "dep", null, null, null, null, null);
        assertThrows(PolicyException.class, () -> admit(orchestrator, valid()));
        ResolutionRequest withMode =
                new ResolutionRequest(Role.WORKER, "acme/control", "dep", "planner", "acme/web", "debug", null, null);
        assertThrows(PolicyException.class, () -> admit(withMode, valid()));
    }

    @Test
    void failsClosedOnLaunchEvidence() {
        for (ManualDebugEvidence bad : new ManualDebugEvidence[] {
            new ManualDebugEvidence("schedule", "octo", "User", "octo", "write", inputs()),
            new ManualDebugEvidence("workflow_dispatch", "ci[bot]", "Bot", "ci[bot]", "write", inputs()),
            new ManualDebugEvidence("workflow_dispatch", "ci[bot]", "User", "ci[bot]", "write", inputs()),
            new ManualDebugEvidence("workflow_dispatch", "octo", "User", "other", "write", inputs()),
            new ManualDebugEvidence("workflow_dispatch", "octo", "User", "octo", "read", inputs()),
            new ManualDebugEvidence("workflow_dispatch", "octo", "User", "octo", "triage", inputs()),
            new ManualDebugEvidence("workflow_dispatch", "octo", "User", "octo", null, inputs()),
            new ManualDebugEvidence("workflow_dispatch", null, "User", "octo", "write", inputs()),
            new ManualDebugEvidence("workflow_dispatch", "octo", "User", "octo", "write", null)}) {
            assertThrows(PolicyException.class, () -> admit(REQUEST, bad));
        }
        assertTrue(admit(REQUEST, new ManualDebugEvidence(
                "workflow_dispatch", "octo", "User", "octo", "Admin", inputs())).get("authorized").asBoolean());
    }

    @Test
    void rejectsDispatcherFieldsAndMismatchedInputs() {
        assertThrows(PolicyException.class, () -> admit(REQUEST, evidence(i -> {
            i.put("correlation_id", "abc");
            return i;
        })));
        assertThrows(PolicyException.class, () -> admit(REQUEST, evidence(i -> {
            i.put("central_repo", "acme/control");
            return i;
        })));
        assertThrows(PolicyException.class, () -> admit(REQUEST, evidence(i -> {
            i.put("target_repo", "acme/other");
            return i;
        })));
        assertThrows(PolicyException.class, () -> admit(REQUEST, evidence(i -> {
            i.put("safe_output_repo", "acme/other");
            return i;
        })));
        assertThrows(PolicyException.class, () -> admit(REQUEST, evidence(i -> {
            i.put("safe_output_mode", "live");
            return i;
        })));
        assertThrows(PolicyException.class, () -> admit(REQUEST, evidence(i -> {
            i.remove("safe_output_repo");
            return i;
        })));
        assertThrows(PolicyException.class, () -> admit(REQUEST, evidence(i -> {
            i.put("surprise", "x");
            return i;
        })));
    }

    @Test
    void debugDoesNotBypassPolicy() {
        ResolutionRequest outOfScope =
                new ResolutionRequest(Role.WORKER, "acme/control", "dep", "planner", "evil/web", null, null, null);
        ManualDebugEvidence outEvidence = evidence(i -> {
            i.put("target_repo", "evil/web");
            i.put("safe_output_repo", "evil/web");
            return i;
        });
        assertThrows(PolicyException.class, () -> admit(outOfScope, outEvidence));
        ResolutionRequest unknown =
                new ResolutionRequest(Role.WORKER, "acme/control", "dep", "ghost", "acme/web", null, null, null);
        assertThrows(PolicyException.class, () -> admit(unknown, valid()));
        ResolutionRequest raised =
                new ResolutionRequest(Role.WORKER, "acme/control", "dep", "planner", "acme/web", null, 5000, null);
        assertThrows(PolicyException.class, () -> admit(raised, valid()));
    }

    @Test
    void disabledOrUndeclaredIsExpectedDenialWithoutDebugProvenance() {
        ResolutionRequest disabled =
                new ResolutionRequest(Role.WORKER, "acme/control", "dep", "off", "acme/web", null, null, null);
        ObjectNode record = admit(disabled, valid());
        assertFalse(record.get("authorized").asBoolean());
        assertEquals("worker-disabled", record.get("reason").asText());
        assertFalse(record.has("launch_kind"));
        ResolutionRequest undeclared =
                new ResolutionRequest(Role.WORKER, "acme/control", "nope", "planner", "acme/web", null, null, null);
        assertEquals("campaign-undeclared", admit(undeclared, valid()).get("reason").asText());
    }

    @Test
    void persistentDebugPolicyStillRejected() {
        assertThrows(PolicyException.class,
                () -> parse("{\"campaigns\":{\"dep\":{\"mode\":\"debug\"}}}"));
    }
}
