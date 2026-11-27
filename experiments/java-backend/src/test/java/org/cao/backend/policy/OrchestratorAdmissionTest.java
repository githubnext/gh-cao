package org.cao.backend.policy;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.fasterxml.jackson.databind.JsonNode;
import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import org.cao.backend.inventory.InventoryException;
import org.cao.backend.inventory.InventoryRepository;
import org.cao.backend.inventory.InventorySource;
import org.cao.backend.inventory.InventoryUnavailableException;
import org.junit.jupiter.api.Test;

class OrchestratorAdmissionTest {
    private static final String SHA = "0123456789abcdef0123456789abcdef01234567";
    private static final Clock CLOCK = Clock.fixed(Instant.parse("2026-01-02T03:04:05Z"), ZoneOffset.UTC);
    private static final ResolutionRequest REQUEST =
            new ResolutionRequest(Role.ORCHESTRATOR, "acme/control", "dep", null, null, null, null, null);
    private static final String CAMPAIGN = "\"dep\":{\"mode\":\"live\",\"max-repositories\":10,"
            + "\"targets\":{\"acme/r2\":{\"mode\":\"review\"}},"
            + "\"workers\":{\"planner\":{\"workflow\":\"dep-planner\"},"
            + "\"off\":{\"workflow\":\"dep-off\",\"enabled\":false},"
            + "\"capped\":{\"workflow\":\"dep-capped\",\"max-mode\":\"review\"}}}";

    private static ControlPolicy policy(String control) {
        return PolicyParser.parse(("{\"version\":1,\"control-plane\":" + control + "}")
                .getBytes(StandardCharsets.UTF_8));
    }

    private static ControlPolicy defaultPolicy() {
        return policy("{\"campaigns\":{" + CAMPAIGN + "}}");
    }

    private static List<InventoryRepository> numbered(int count) {
        List<InventoryRepository> repositories = new ArrayList<>();
        for (int i = 1; i <= count; i++) {
            repositories.add(new InventoryRepository(i, "acme/r" + i, false, false, true, null, "main"));
        }
        return repositories;
    }

    private static OrchestratorAdmission.Context context(int dispatchMax, String... workflows) {
        return new OrchestratorAdmission.Context(SHA, "acme/control", dispatchMax, List.of(workflows),
                Map.of("dep-planner", "active", "dep-off", "active", "dep-capped", "active"));
    }

    private static InventorySource returning(List<InventoryRepository> repositories) {
        return request -> repositories;
    }

    private static OrchestratorAdmission.Result admit(ControlPolicy policy, ResolutionRequest request,
            OrchestratorAdmission.Context context, InventorySource source) {
        return OrchestratorAdmission.admit(policy, request, context, source, CLOCK);
    }

    @Test
    void authorizedRecordCarriesOrchestratorProvenance() {
        OrchestratorAdmission.Result result = admit(defaultPolicy(), REQUEST,
                context(4, "dep-planner", "dep-off"), returning(numbered(5)));
        JsonNode record = result.record();
        assertTrue(result.activationReady());
        assertTrue(record.get("authorized").asBoolean());
        assertEquals("orchestrator", record.get("control_role").asText());
        assertEquals("dep", record.get("campaign").asText());
        assertEquals("live", record.get("safe_output_mode").asText());
        assertEquals("acme/control", record.get("control_repository").asText());
        assertEquals(SHA, record.get("policy_source").get("sha").asText());
        assertEquals(64, record.get("policy_sha256").asText().length());
        assertEquals("2026-01-02T03:04:05Z", record.get("resolved_at").asText());
        assertEquals("organization", record.get("repo_source").asText());
        assertTrue(record.get("inventory_version").asText().startsWith("sha256:"));
        assertEquals(5, record.get("inventory_repository_count").asInt());
        assertEquals(5, record.get("candidate_repositories").size());
        assertEquals("1000", record.get("max_scan_repos").asText());
        assertTrue(record.get("batch_id").asText().endsWith(":cell-0-of-1:batch-0-of-1"));
    }

    @Test
    void exactTargetPolicyShapesEachCandidateMode() {
        JsonNode candidates = admit(defaultPolicy(), REQUEST, context(4, "dep-planner"), returning(numbered(3)))
                .record().get("candidate_repositories");
        assertEquals("live", candidates.get(0).get("safe_output_mode").asText());
        assertEquals("review", candidates.get(1).get("safe_output_mode").asText());
    }

    @Test
    void requestedReviewLowersEveryCandidateButLiveCannotRaise() {
        ResolutionRequest review = new ResolutionRequest(Role.ORCHESTRATOR, "acme/control", "dep", null, null,
                "review", null, null);
        JsonNode record = admit(defaultPolicy(), review, context(4, "dep-planner"), returning(numbered(3))).record();
        assertEquals("review", record.get("safe_output_mode").asText());
        record.get("candidate_repositories").forEach(c -> assertEquals("review", c.get("safe_output_mode").asText()));

        ControlPolicy reviewPolicy = policy("{\"campaigns\":{\"dep\":{\"mode\":\"review\","
                + "\"targets\":{\"acme/r1\":{\"mode\":\"live\"}},\"workers\":{\"planner\":{\"workflow\":\"dep-planner\"}}}}}");
        ResolutionRequest live = new ResolutionRequest(Role.ORCHESTRATOR, "acme/control", "dep", null, null,
                "live", null, null);
        assertThrows(PolicyException.class,
                () -> admit(reviewPolicy, live, context(4, "dep-planner"), returning(numbered(1))));
    }

    @Test
    void capsAreTheMinimumOfPolicyRolloutAndDispatchBudget() {
        ControlPolicy capped = policy("{\"defaults\":{\"rollout-percent\":50},\"campaigns\":{\"dep\":"
                + "{\"max-repositories\":6,\"workers\":{\"a\":{\"workflow\":\"dep-a\"},\"b\":{\"workflow\":\"dep-b\"}}}}}");
        OrchestratorAdmission.Context ctx = new OrchestratorAdmission.Context(SHA, "acme/control", 20,
                List.of("dep-a", "dep-b"), Map.of("dep-a", "active", "dep-b", "active"));
        // 10 candidates: policy 6, rollout ceil(10*50/100)=5, dispatch floor(20/min(2,20))=10
        assertEquals(5, admit(capped, REQUEST, ctx, returning(numbered(10))).record().get("effective_max_repos").asInt());
        OrchestratorAdmission.Context tight = new OrchestratorAdmission.Context(SHA, "acme/control", 4,
                List.of("dep-a", "dep-b"), Map.of("dep-a", "active", "dep-b", "active"));
        // dispatch budget 4 across 2 workers per target admits 2 targets
        assertEquals(2, admit(capped, REQUEST, tight, returning(numbered(10))).record().get("effective_max_repos").asInt());
        ResolutionRequest lower = new ResolutionRequest(Role.ORCHESTRATOR, "acme/control", "dep", null, null,
                null, 1, 40);
        assertEquals(1, admit(capped, lower, ctx, returning(numbered(10))).record().get("effective_max_repos").asInt());
        ResolutionRequest widen = new ResolutionRequest(Role.ORCHESTRATOR, "acme/control", "dep", null, null,
                null, null, 100);
        assertThrows(PolicyException.class, () -> admit(capped, widen, ctx, returning(numbered(10))));
    }

    @Test
    void rolloutCapRoundsUpButIsZeroForEmptyInventory() {
        ControlPolicy tiny = policy("{\"defaults\":{\"rollout-percent\":1},\"campaigns\":{\"dep\":"
                + "{\"max-repositories\":9,\"workers\":{\"a\":{\"workflow\":\"dep-a\"}}}}}");
        OrchestratorAdmission.Context ctx = new OrchestratorAdmission.Context(SHA, "acme/control", 9,
                List.of("dep-a"), Map.of("dep-a", "active"));
        assertEquals(1, admit(tiny, REQUEST, ctx, returning(numbered(3))).record().get("effective_max_repos").asInt());
        assertEquals(0, admit(tiny, REQUEST, ctx, returning(List.of())).record().get("effective_max_repos").asInt());
    }

    @Test
    void workerEligibilityExplainsEverySkip() {
        OrchestratorAdmission.Context ctx = new OrchestratorAdmission.Context(SHA, "acme/control", 5,
                List.of("dep-planner", "dep-off", "dep-ghost", "dep-capped", "dep-unlisted"),
                Map.of("dep-planner", "active", "dep-off", "active", "dep-capped", "disabled_manually",
                        "dep-unlisted", "active"));
        JsonNode workers = admit(defaultPolicy(), REQUEST, ctx, returning(numbered(2))).record()
                .get("worker_workflows");
        assertTrue(workers.get(0).get("eligible").asBoolean());
        assertEquals("worker disabled by control-plane policy", workers.get(1).get("skip_reason").asText());
        assertEquals("worker is not part of installed campaign", workers.get(2).get("skip_reason").asText());
        assertEquals("worker workflow disabled", workers.get(3).get("skip_reason").asText());
        assertEquals("review", workers.get(3).get("max_mode").asText());
        assertEquals("worker is not part of installed campaign", workers.get(4).get("skip_reason").asText());
    }

    @Test
    void noEligibleWorkerAdmitsNoRepositories() {
        OrchestratorAdmission.Context ctx = new OrchestratorAdmission.Context(SHA, "acme/control", 5,
                List.of("dep-off"), Map.of("dep-off", "active"));
        OrchestratorAdmission.Result result = admit(defaultPolicy(), REQUEST, ctx, returning(numbered(3)));
        assertEquals(0, result.record().get("effective_max_repos").asInt());
        assertTrue(result.record().get("worker_workflows").get(0).get("skip_reason").asText().contains("disabled"));
    }

    @Test
    void missingWorkflowIsUnavailableNotEligible() {
        OrchestratorAdmission.Context ctx = new OrchestratorAdmission.Context(SHA, "acme/control", 5,
                List.of("dep-planner"), Map.of());
        JsonNode worker = admit(defaultPolicy(), REQUEST, ctx, returning(numbered(2))).record()
                .get("worker_workflows").get(0);
        assertFalse(worker.get("eligible").asBoolean());
        assertEquals("worker workflow unavailable", worker.get("skip_reason").asText());
    }

    @Test
    void cellAndBatchPolicyDeterminesCandidates() {
        ControlPolicy cells = policy("{\"inventory\":{\"cell-count\":2,\"cell-index\":1,\"batch-size\":2,"
                + "\"batch-index\":1},\"campaigns\":{" + CAMPAIGN + "}}");
        JsonNode record = admit(cells, REQUEST, context(4, "dep-planner"), returning(numbered(10))).record();
        assertEquals(5, record.get("cell_repository_count").asInt());
        assertEquals(3, record.get("batch_count").asInt());
        assertEquals("acme/r5", record.get("candidate_repositories").get(0).get("full_name").asText());
        assertEquals(2, record.get("candidate_repositories").size());
        ControlPolicy beyond = policy("{\"inventory\":{\"batch-index\":3,\"batch-size\":4},\"campaigns\":{"
                + CAMPAIGN + "}}");
        assertThrows(InventoryException.class,
                () -> admit(beyond, REQUEST, context(4, "dep-planner"), returning(numbered(10))));
    }

    @Test
    void allowListSelectsExactlyEnrolledRepositories() {
        ControlPolicy scoped = policy("{\"scope\":{\"allowed-repositories\":[\"acme/r1\",\"acme/r3\"]},"
                + "\"campaigns\":{" + CAMPAIGN + "}}");
        List<InventorySource.Request> seen = new ArrayList<>();
        InventorySource source = request -> {
            seen.add(request);
            return List.of(numbered(3).get(0), numbered(3).get(2));
        };
        JsonNode record = admit(scoped, REQUEST, context(4, "dep-planner"), source).record();
        assertEquals("allowed_repos", record.get("repo_source").asText());
        assertEquals(InventorySource.Kind.ALLOWED_REPOSITORIES, seen.get(0).kind());
        assertEquals(List.of("acme/r1", "acme/r3"), seen.get(0).repositories());
        assertEquals(2, record.get("candidate_repositories").size());
    }

    @Test
    void sourceCannotWidenBeyondEnrollmentOrOwner() {
        ControlPolicy scoped = policy("{\"scope\":{\"allowed-repositories\":[\"acme/r1\"]},\"campaigns\":{"
                + CAMPAIGN + "}}");
        assertThrows(InventoryException.class,
                () -> admit(scoped, REQUEST, context(4, "dep-planner"), returning(numbered(2))));
        List<InventoryRepository> foreign = List.of(
                new InventoryRepository(9, "evil/steal", false, false, false, null, "main"));
        assertThrows(InventoryException.class,
                () -> admit(defaultPolicy(), REQUEST, context(4, "dep-planner"), returning(foreign)));
    }

    @Test
    void incompleteEnrolledReadFailsClosedAsUnavailable() {
        ControlPolicy scoped = policy("{\"scope\":{\"allowed-repositories\":[\"acme/r1\",\"acme/r2\"]},"
                + "\"campaigns\":{" + CAMPAIGN + "}}");
        OrchestratorAdmission.Result result = admit(scoped, REQUEST, context(4, "dep-planner"),
                returning(numbered(1)));
        assertFalse(result.activationReady());
        assertEquals("incomplete", result.record().get("inventory_status").asText());
        assertEquals(0, result.record().get("candidate_repositories").size());
    }

    @Test
    void unavailableSourceAndScanOverflowReportIncompleteWithoutPartialFleet() {
        InventorySource down = request -> {
            throw new InventoryUnavailableException("rate limited with secret-token-123");
        };
        OrchestratorAdmission.Result result = admit(defaultPolicy(), REQUEST, context(4, "dep-planner"), down);
        assertFalse(result.activationReady());
        assertEquals("inventory-unavailable", result.record().get("reason").asText());
        assertEquals(OrchestratorAdmission.INCOMPLETE_MESSAGE, result.record().get("repo_error").asText());
        assertFalse(result.record().toString().contains("secret-token-123"));
        assertEquals(0, result.record().get("effective_max_repos").asInt());
        assertEquals(0, result.record().get("worker_workflows").size());

        ControlPolicy small = policy("{\"inventory\":{\"max-scan-repositories\":2},\"campaigns\":{" + CAMPAIGN + "}}");
        assertFalse(admit(small, REQUEST, context(4, "dep-planner"), returning(numbered(3))).activationReady());
    }

    @Test
    void allowListLargerThanScanBoundIsFatal() {
        ControlPolicy scoped = policy("{\"scope\":{\"allowed-repositories\":[\"acme/r1\",\"acme/r2\"]},"
                + "\"inventory\":{\"max-scan-repositories\":1},\"campaigns\":{" + CAMPAIGN + "}}");
        assertThrows(PolicyException.class,
                () -> admit(scoped, REQUEST, context(4, "dep-planner"), returning(numbered(2))));
    }

    @Test
    void policyDenialsNeverReadInventory() {
        InventorySource forbidden = request -> {
            throw new AssertionError("inventory must not be read after a denial");
        };
        ControlPolicy paused = policy("{\"campaigns\":{\"dep\":{\"enabled\":false}}}");
        OrchestratorAdmission.Result disabled = admit(paused, REQUEST, context(4, "dep-planner"), forbidden);
        assertFalse(disabled.activationReady());
        assertFalse(disabled.record().get("authorized").asBoolean());
        assertEquals("campaign-disabled", disabled.record().get("reason").asText());
        assertEquals(0, disabled.record().get("candidate_repositories").size());
        ResolutionRequest unknown = new ResolutionRequest(Role.ORCHESTRATOR, "acme/control", "nope", null, null,
                null, null, null);
        assertEquals("campaign-undeclared",
                admit(defaultPolicy(), unknown, context(4, "dep-planner"), forbidden).record().get("reason").asText());
    }

    @Test
    void identityDestinationAndBoundsFailClosed() {
        ResolutionRequest worker = new ResolutionRequest(Role.WORKER, "acme/control", "dep", "planner",
                "acme/r1", null, null, null);
        assertThrows(PolicyException.class,
                () -> admit(defaultPolicy(), worker, context(4, "dep-planner"), returning(numbered(1))));
        assertThrows(PolicyException.class, () -> admit(defaultPolicy(), REQUEST,
                new OrchestratorAdmission.Context("bad", "acme/control", 4, List.of("dep-planner"), Map.of()),
                returning(numbered(1))));
        assertThrows(PolicyException.class,
                () -> admit(defaultPolicy(), REQUEST, context(0, "dep-planner"), returning(numbered(1))));
        assertThrows(PolicyException.class,
                () -> admit(defaultPolicy(), REQUEST, context(1001, "dep-planner"), returning(numbered(1))));
        assertThrows(PolicyException.class,
                () -> admit(defaultPolicy(), REQUEST, context(4), returning(numbered(1))));
        ControlPolicy review = policy("{\"campaigns\":{\"dep\":{\"workers\":{\"p\":{\"workflow\":\"dep-planner\"}}}}}");
        OrchestratorAdmission.Context elsewhere = new OrchestratorAdmission.Context(SHA, "acme/review-sink", 4,
                List.of("dep-planner"), Map.of("dep-planner", "active"));
        assertThrows(PolicyException.class, () -> admit(review, REQUEST, elsewhere, returning(numbered(1))));
        OrchestratorAdmission.Context foreignOwner = new OrchestratorAdmission.Context(SHA, "other/control", 4,
                List.of("dep-planner"), Map.of("dep-planner", "active"));
        assertThrows(PolicyException.class, () -> admit(review, REQUEST, foreignOwner, returning(numbered(1))));
    }

    @Test
    void inventoryPolicyIsValidated() {
        for (String inventory : new String[] {
            "{\"cell-count\":2,\"cell-index\":2}", "{\"cell-count\":1001}", "{\"max-scan-repositories\":0}",
            "{\"max-scan-repositories\":100001}", "{\"batch-size\":0}", "{\"batch-index\":-1}", "{\"extra\":1}",
            "{\"cell-index\":1}", "{\"batch-size\":\"3\"}", "[]"}) {
            assertThrows(PolicyException.class, () -> policy("{\"inventory\":" + inventory + "}"), inventory);
        }
        ControlPolicy.Inventory parsed = policy("{\"inventory\":{\"cell-count\":4,\"cell-index\":3}}").inventory();
        assertEquals(new ControlPolicy.Inventory(1000, 4, 3, 100_000, 0), parsed);
        assertEquals(ControlPolicy.Inventory.defaults(), policy("{}").inventory());
    }
}
