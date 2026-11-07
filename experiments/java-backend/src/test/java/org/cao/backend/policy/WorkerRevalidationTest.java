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
import org.junit.jupiter.api.Test;

class WorkerRevalidationTest {
    private static final String CONTROL = "acme/control";
    private static final String SHA = "0123456789abcdef0123456789abcdef01234567";
    private static final Clock CLOCK = Clock.fixed(Instant.parse("2026-01-02T03:04:05Z"), ZoneOffset.UTC);
    private static final String WORKERS = "\"workers\":{\"planner\":{\"workflow\":\"dep-planner\"}}";
    private static final ResolutionRequest REQUEST =
            new ResolutionRequest(Role.WORKER, CONTROL, "dep", "planner", "acme/web", null, null, null);

    private static ControlPolicy policy(String campaign) {
        String json = "{\"version\":1,\"control-plane\":{\"campaigns\":{\"dep\":" + campaign + "}}}";
        return PolicyParser.parse(json.getBytes(StandardCharsets.UTF_8));
    }

    private static DispatchEnvelope parentOf(ControlPolicy parentPolicy) {
        return DispatchEnvelope.from(PolicyResolver.resolve(parentPolicy, REQUEST));
    }

    @Test
    void narrowedPolicyNarrowsOutstandingDispatch() {
        DispatchEnvelope parent = parentOf(policy("{\"mode\":\"live\",\"max-repositories\":5," + WORKERS + "}"));
        Resolution now = WorkerRevalidator.revalidate(
                policy("{\"mode\":\"review\",\"max-repositories\":2," + WORKERS + "}"), REQUEST, parent);
        assertEquals(Mode.REVIEW, now.mode());
        assertEquals(2, now.maxRepositories());
    }

    @Test
    void broadenedPolicyDoesNotWidenParentEnvelope() {
        DispatchEnvelope parent = parentOf(policy("{\"mode\":\"review\",\"max-repositories\":2," + WORKERS + "}"));
        Resolution now = WorkerRevalidator.revalidate(
                policy("{\"mode\":\"live\",\"max-repositories\":9,\"rollout-percent\":100," + WORKERS + "}"),
                REQUEST, parent);
        assertEquals(Mode.REVIEW, now.mode());
        assertEquals(2, now.maxRepositories());
    }

    @Test
    void revokedWorkerIsDeniedAndMismatchedEnvelopeIsFatal() {
        DispatchEnvelope parent = parentOf(policy("{" + WORKERS + "}"));
        Resolution revoked = WorkerRevalidator.revalidate(policy(
                "{\"workers\":{\"planner\":{\"workflow\":\"dep-planner\",\"enabled\":false}}}"), REQUEST, parent);
        assertFalse(revoked.authorized());
        assertEquals("worker-disabled", revoked.reason());
        ResolutionRequest other =
                new ResolutionRequest(Role.WORKER, CONTROL, "dep", "planner", "acme/other", null, null, null);
        ControlPolicy current = policy("{" + WORKERS + "}");
        assertThrows(PolicyException.class, () -> WorkerRevalidator.revalidate(current, other, parent));
    }

    @Test
    void requestCannotWidenBeyondCurrentPolicyEvenWithWideEnvelope() {
        DispatchEnvelope wide = new DispatchEnvelope(CONTROL, "dep", "planner", "acme/web", Mode.LIVE, 9, 100, "x");
        ResolutionRequest widening =
                new ResolutionRequest(Role.WORKER, CONTROL, "dep", "planner", "acme/web", "live", null, null);
        ControlPolicy current = policy("{\"mode\":\"review\"," + WORKERS + "}");
        assertThrows(PolicyException.class, () -> WorkerRevalidator.revalidate(current, widening, wide));
    }

    @Test
    void envelopeCannotBeDerivedFromDenial() {
        Resolution denied = PolicyResolver.resolve(
                policy("{\"workers\":{\"planner\":{\"workflow\":\"dep-planner\",\"enabled\":false}}}"), REQUEST);
        assertThrows(PolicyException.class, () -> DispatchEnvelope.from(denied));
    }

    @Test
    void authorizedWorkerRecordCarriesRequiredFields() {
        ControlPolicy p = policy("{\"mode\":\"live\"," + WORKERS + "}");
        DispatchEnvelope parent = parentOf(p);
        ObjectNode record = EffectiveRecord.build(p, WorkerRevalidator.revalidate(p, REQUEST, parent),
                parent, SHA, CLOCK);
        assertTrue(record.get("authorized").asBoolean());
        assertEquals("live", record.get("safe_output_mode").asText());
        assertEquals("acme/web", record.get("target_repo").asText());
        assertEquals(SHA, record.get("workflow_sha").asText());
        assertEquals(p.digest(), record.get("policy_sha256").asText());
        assertEquals("2026-01-02T03:04:05Z", record.get("resolved_at").asText());
        assertEquals("live", record.get("dispatch_envelope").get("mode").asText());
        assertEquals(0, record.get("candidate_repositories").size());
    }

    @Test
    void deniedRecordHasNoEnvelopeOrSafeOutputRepo() {
        ControlPolicy p = policy("{\"workers\":{\"planner\":{\"workflow\":\"dep-planner\",\"enabled\":false}}}");
        Resolution denied = PolicyResolver.resolve(p, REQUEST);
        ObjectNode record = EffectiveRecord.build(p, denied, null, SHA, CLOCK);
        assertFalse(record.get("authorized").asBoolean());
        assertEquals("worker-disabled", record.get("reason").asText());
        assertEquals("review", record.get("safe_output_mode").asText());
        assertEquals("", record.get("safe_output_repo").asText());
        assertFalse(record.has("dispatch_envelope"));
    }

    @Test
    void invalidShaOrchestratorAndMissingEnvelopeAreRejected() {
        ControlPolicy p = policy("{" + WORKERS + "}");
        Resolution r = PolicyResolver.resolve(p, REQUEST);
        DispatchEnvelope parent = parentOf(p);
        assertThrows(PolicyException.class, () -> EffectiveRecord.build(p, r, parent, "abc", CLOCK));
        Resolution orch = PolicyResolver.resolve(p,
                new ResolutionRequest(Role.ORCHESTRATOR, CONTROL, "dep", null, null, null, null, null));
        assertThrows(PolicyException.class, () -> EffectiveRecord.build(p, orch, null, SHA, CLOCK));
        assertThrows(PolicyException.class, () -> EffectiveRecord.build(p, r, null, SHA, CLOCK));
    }
}
