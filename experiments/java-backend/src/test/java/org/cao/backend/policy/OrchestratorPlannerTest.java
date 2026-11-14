package org.cao.backend.policy;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

class OrchestratorPlannerTest {
    private static final String CONTROL = "acme/control";
    private static final String SHA = "0123456789abcdef0123456789abcdef01234567";
    private static final Clock CLOCK = Clock.fixed(Instant.parse("2026-01-02T03:04:05Z"), ZoneOffset.UTC);
    private static final String CAMPAIGN = """
            {"version":1,"control-plane":{
              "defaults":{"max-repositories":10,"rollout-percent":50},
              "campaigns":{"dep":{"mode":"live","targets":{"acme/gamma":{"mode":"review"}},
                "workers":{"planner":{"workflow":"dep-planner"},
                           "off":{"workflow":"dep-off","enabled":false},
                           "gone":{"workflow":"dep-gone"},
                           "paused":{"workflow":"dep-paused"}}},
                "dead":{"enabled":false}}}}
            """;
    private static final ControlPolicy POLICY = PolicyParser.parse(CAMPAIGN.getBytes(StandardCharsets.UTF_8));
    private static final ResolutionRequest REQUEST =
            new ResolutionRequest(Role.ORCHESTRATOR, CONTROL, "dep", null, null, null, null, null);

    private static OrchestratorEvidence.WorkflowState installed(long id, String slug, boolean active) {
        return new OrchestratorEvidence.WorkflowState(id, slug, ".github/workflows/" + slug + ".lock.yml",
                active ? "active" : "disabled_manually", active);
    }

    private static OrchestratorEvidence evidence(List<RepositoryMetadata> repositories) {
        return new OrchestratorEvidence(OrchestratorEvidence.Source.ORGANIZATION, repositories,
                List.of("dep-planner", "dep-off", "dep-gone", "dep-undeclared", "dep-paused"),
                Map.of("dep-planner", installed(1, "dep-planner", true),
                        "dep-off", installed(2, "dep-off", true),
                        "dep-undeclared", installed(3, "dep-undeclared", true),
                        "dep-paused", installed(4, "dep-paused", false)),
                4, CONTROL, null);
    }

    private static OrchestratorEvidence withWorkers(List<String> configured, Map<String,
            OrchestratorEvidence.WorkflowState> state, int dispatchMax) {
        return new OrchestratorEvidence(OrchestratorEvidence.Source.ORGANIZATION, InventoryPartitionTest.fixture(),
                configured, state, dispatchMax, CONTROL, null);
    }

    @Test
    void plansBoundedCandidatesPerTargetModeAndWorkerEligibility() {
        OrchestratorPlanner.Plan plan = OrchestratorPlanner.plan(POLICY, REQUEST,
                evidence(InventoryPartitionTest.fixture()));
        // archived beta and disabled delta are excluded; alpha, gamma, epsilon remain.
        assertEquals(List.of("acme/alpha", "acme/gamma", "acme/epsilon"),
                plan.candidates().stream().map(c -> c.repository().fullName()).toList());
        assertEquals(List.of(Mode.LIVE, Mode.REVIEW, Mode.LIVE),
                plan.candidates().stream().map(OrchestratorPlanner.Candidate::mode).toList());
        assertEquals(List.of(true, false, false, false, false),
                plan.workers().stream().map(OrchestratorPlanner.WorkerWorkflow::eligible).toList());
        assertEquals(List.of("worker disabled by control-plane policy", "worker workflow unavailable",
                "worker is not part of installed campaign", "worker workflow disabled"),
                plan.workers().stream().skip(1).map(OrchestratorPlanner.WorkerWorkflow::skipReason).toList());
        // min(policy 10, rollout ceil(3*50/100)=2, dispatch floor(4/1)=4)
        assertEquals(2, plan.effectiveMaxRepositories());
    }

    @Test
    void effectiveCapIsZeroWithoutEligibleWorkersOrCandidates() {
        OrchestratorPlanner.Plan none = OrchestratorPlanner.plan(POLICY, REQUEST,
                withWorkers(List.of("dep-off"), Map.of("dep-off", installed(2, "dep-off", true)), 4));
        assertEquals(0, none.effectiveMaxRepositories());
        OrchestratorPlanner.Plan empty = OrchestratorPlanner.plan(POLICY, REQUEST, evidence(List.of()));
        assertEquals(0, empty.effectiveMaxRepositories());
        assertTrue(empty.candidates().isEmpty());
    }

    @Test
    void dispatchCeilingDividesAcrossEligibleWorkersPerTarget() {
        ControlPolicy two = PolicyParser.parse("""
                {"version":1,"control-plane":{"defaults":{"max-repositories":10,"rollout-percent":100},
                 "campaigns":{"dep":{"workers":{"a":{"workflow":"dep-a"},"b":{"workflow":"dep-b"}}}}}}
                """.getBytes(StandardCharsets.UTF_8));
        Map<String, OrchestratorEvidence.WorkflowState> state =
                Map.of("dep-a", installed(1, "dep-a", true), "dep-b", installed(2, "dep-b", true));
        OrchestratorPlanner.Plan plan = OrchestratorPlanner.plan(two, REQUEST,
                withWorkers(List.of("dep-a", "dep-b"), state, 5));
        assertEquals(2, plan.effectiveMaxRepositories()); // floor(5 / min(2,5))
    }

    @Test
    void requestedReviewLowersEveryCandidateButNeverRaises() {
        ResolutionRequest review = new ResolutionRequest(Role.ORCHESTRATOR, CONTROL, "dep", null, null, "review", 1,
                null);
        OrchestratorPlanner.Plan plan = OrchestratorPlanner.plan(POLICY, review,
                evidence(InventoryPartitionTest.fixture()));
        assertTrue(plan.candidates().stream().allMatch(c -> c.mode() == Mode.REVIEW));
        assertEquals(1, plan.effectiveMaxRepositories());
        ControlPolicy reviewOnly = PolicyParser.parse(
                "{\"version\":1,\"control-plane\":{\"campaigns\":{\"dep\":{\"workers\":{\"p\":{\"workflow\":\"dep-planner\"}}}}}}"
                        .getBytes(StandardCharsets.UTF_8));
        ResolutionRequest live = new ResolutionRequest(Role.ORCHESTRATOR, CONTROL, "dep", null, null, "live", null,
                null);
        assertThrows(PolicyException.class, () -> OrchestratorPlanner.plan(reviewOnly, live, evidence(List.of())));
    }

    @Test
    void failsClosedWithoutProvableEvidence() {
        assertThrows(PolicyException.class, () -> OrchestratorPlanner.plan(POLICY, REQUEST, null));
        assertThrows(PolicyException.class, () -> OrchestratorPlanner.plan(POLICY, REQUEST, evidence(null)));
        assertThrows(PolicyException.class, () -> OrchestratorPlanner.plan(POLICY, REQUEST,
                new OrchestratorEvidence(OrchestratorEvidence.Source.ORGANIZATION, List.of(), List.of(), Map.of(), 1,
                        CONTROL, null)));
        for (int bad : new int[] {0, 1001}) {
            assertThrows(PolicyException.class, () -> OrchestratorPlanner.plan(POLICY, REQUEST,
                    new OrchestratorEvidence(OrchestratorEvidence.Source.ORGANIZATION, List.of(),
                            List.of("dep-planner"), Map.of(), bad, CONTROL, null)));
        }
        assertThrows(PolicyException.class, () -> OrchestratorPlanner.plan(POLICY, new ResolutionRequest(
                Role.ORCHESTRATOR, CONTROL, "dead", null, null, null, null, null), evidence(List.of())));
        assertThrows(PolicyException.class, () -> OrchestratorPlanner.plan(POLICY, new ResolutionRequest(
                Role.WORKER, CONTROL, "dep", "planner", "acme/alpha", null, null, null), evidence(List.of())));
    }

    @Test
    void scopeIsEnforcedOnInventoryAndDestination() {
        ControlPolicy scoped = PolicyParser.parse("""
                {"version":1,"control-plane":{"scope":{"allowed-owners":["acme"],"allowed-repositories":["acme/alpha"]},
                 "campaigns":{"dep":{"workers":{"p":{"workflow":"dep-planner"}}}}}}
                """.getBytes(StandardCharsets.UTF_8));
        RepositoryMetadata alpha = InventoryPartitionTest.fixture().get(1);
        List<String> workers = List.of("dep-planner");
        Map<String, OrchestratorEvidence.WorkflowState> state = Map.of("dep-planner", installed(1, "dep-planner", true));
        OrchestratorPlanner.Plan ok = OrchestratorPlanner.plan(scoped, REQUEST, new OrchestratorEvidence(
                OrchestratorEvidence.Source.ALLOWED_REPOSITORIES, List.of(alpha), workers, state, 1, CONTROL, null));
        assertEquals(1, ok.effectiveMaxRepositories());
        // an organization listing under an exact allow-list, an extra repository, or a missing one is rejected
        assertThrows(PolicyException.class, () -> OrchestratorPlanner.plan(scoped, REQUEST, new OrchestratorEvidence(
                OrchestratorEvidence.Source.ORGANIZATION, List.of(alpha), workers, state, 1, CONTROL, null)));
        assertThrows(PolicyException.class, () -> OrchestratorPlanner.plan(scoped, REQUEST, new OrchestratorEvidence(
                OrchestratorEvidence.Source.ALLOWED_REPOSITORIES, InventoryPartitionTest.fixture().subList(0, 2),
                workers, state, 1, CONTROL, null)));
        assertThrows(PolicyException.class, () -> OrchestratorPlanner.plan(scoped, REQUEST, new OrchestratorEvidence(
                OrchestratorEvidence.Source.ALLOWED_REPOSITORIES, List.of(), workers, state, 1, CONTROL, null)));
        RepositoryMetadata foreign = new RepositoryMetadata(7, "evil/alpha", false, false, false, null, null);
        assertThrows(PolicyException.class, () -> OrchestratorPlanner.plan(POLICY, REQUEST, evidence(List.of(foreign))));
        assertThrows(PolicyException.class, () -> OrchestratorPlanner.plan(POLICY, REQUEST, new OrchestratorEvidence(
                OrchestratorEvidence.Source.ORGANIZATION, List.of(), workers, state, 1, "evil/review", true)));
    }

    @Test
    void reviewDestinationMustBeControlOrVerifiedPrivate() {
        ControlPolicy review = PolicyParser.parse(
                "{\"version\":1,\"control-plane\":{\"campaigns\":{\"dep\":{\"workers\":{\"p\":{\"workflow\":\"dep-planner\"}}}}}}"
                        .getBytes(StandardCharsets.UTF_8));
        List<String> workers = List.of("dep-planner");
        Map<String, OrchestratorEvidence.WorkflowState> state = Map.of("dep-planner", installed(1, "dep-planner", true));
        for (Boolean isPrivate : new Boolean[] {null, Boolean.FALSE}) {
            assertThrows(PolicyException.class, () -> OrchestratorPlanner.plan(review, REQUEST,
                    new OrchestratorEvidence(OrchestratorEvidence.Source.ORGANIZATION, List.of(), workers, state, 1,
                            "acme/reviews", isPrivate)));
        }
        assertEquals("acme/reviews", OrchestratorPlanner.plan(review, REQUEST, new OrchestratorEvidence(
                OrchestratorEvidence.Source.ORGANIZATION, List.of(), workers, state, 1, "acme/reviews", true))
                .safeOutputRepository());
    }

    @Test
    void authorizedRecordCarriesEveryRequiredOrchestratorField() {
        OrchestratorPlanner.Plan plan = OrchestratorPlanner.plan(POLICY, REQUEST,
                evidence(InventoryPartitionTest.fixture()));
        ObjectNode record = EffectiveRecord.buildOrchestrator(plan, SHA, CLOCK);
        assertTrue(record.get("authorized").asBoolean());
        assertEquals("authorized", record.get("reason").asText());
        assertEquals("orchestrator", record.get("control_role").asText());
        assertEquals("dep", record.get("campaign").asText());
        assertEquals("", record.get("worker").asText());
        assertEquals(CONTROL, record.get("control_repository").asText());
        assertEquals(SHA, record.get("workflow_sha").asText());
        assertEquals(POLICY.digest(), record.get("policy_sha256").asText());
        assertEquals(1, record.get("schema_version").asInt());
        assertEquals("2026-01-02T03:04:05Z", record.get("resolved_at").asText());
        assertEquals(plan.partition().version(), record.get("inventory_version").asText());
        assertEquals(plan.partition().batchId(), record.get("batch_id").asText());
        assertEquals(10, record.get("max_repos").asInt());
        assertEquals(50, record.get("rollout_percent").asInt());
        assertEquals(2, record.get("effective_max_repos").asInt());
        assertEquals(1000, record.get("max_scan_repos").asInt());
        assertEquals("organization", record.get("repo_source").asText());
        assertEquals("complete", record.get("inventory_status").asText());
        assertEquals("live", record.get("safe_output_mode").asText());
        assertEquals(CONTROL, record.get("safe_output_repo").asText());
        JsonNode first = record.get("candidate_repositories").get(0);
        assertEquals("acme/alpha", first.get("full_name").asText());
        assertEquals("live", first.get("safe_output_mode").asText());
        assertTrue(first.get("pushed_at").isNull());
        assertEquals("review", record.get("candidate_repositories").get(1).get("safe_output_mode").asText());
        JsonNode workers = record.get("worker_workflows");
        assertEquals(5, workers.size());
        assertTrue(workers.get(0).get("eligible").asBoolean());
        assertTrue(workers.get(0).get("skip_reason").isNull());
        assertEquals("worker is not part of installed campaign", workers.get(3).get("skip_reason").asText());
        assertFalse(record.has("dispatch_envelope"));
        assertFalse(record.has("correlation_id"));
    }

    @Test
    void deniedOrchestratorRecordHasNoCandidatesOrWorkers() {
        ResolutionRequest request = new ResolutionRequest(Role.ORCHESTRATOR, CONTROL, "dead", null, null, null, null,
                null);
        Resolution denied = PolicyResolver.resolve(POLICY, request);
        ObjectNode record = EffectiveRecord.build(POLICY, denied, null, SHA, CLOCK);
        assertFalse(record.get("authorized").asBoolean());
        assertEquals("campaign-disabled", record.get("reason").asText());
        assertEquals("orchestrator", record.get("control_role").asText());
        assertEquals(0, record.get("effective_max_repos").asInt());
        assertEquals(0, record.get("candidate_repositories").size());
        assertEquals(0, record.get("worker_workflows").size());
        assertEquals("", record.get("safe_output_repo").asText());
        assertFalse(record.has("inventory_version"));
        Resolution undeclared = PolicyResolver.resolve(POLICY, new ResolutionRequest(Role.ORCHESTRATOR, CONTROL,
                "ghost", null, null, null, null, null));
        assertEquals("campaign-undeclared", EffectiveRecord.build(POLICY, undeclared, null, SHA, CLOCK)
                .get("reason").asText());
    }

    @Test
    void recordBuildersRejectBadShaAndForeignResolutions() {
        OrchestratorPlanner.Plan plan = OrchestratorPlanner.plan(POLICY, REQUEST, evidence(List.of()));
        assertThrows(PolicyException.class, () -> EffectiveRecord.buildOrchestrator(plan, "abc", CLOCK));
        Resolution denied = PolicyResolver.resolve(POLICY, new ResolutionRequest(Role.ORCHESTRATOR, CONTROL, "dead",
                null, null, null, null, null));
        OrchestratorPlanner.Plan forged = new OrchestratorPlanner.Plan(denied, plan.source(), plan.limits(),
                plan.partition(), plan.candidates(), plan.workers(), 1, 1, CONTROL, true);
        assertThrows(PolicyException.class, () -> EffectiveRecord.buildOrchestrator(forged, SHA, CLOCK));
        assertThrows(PolicyException.class, () -> EffectiveRecord.build(POLICY, plan.resolution(), null, SHA, CLOCK));
    }
}
