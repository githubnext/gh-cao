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
import java.util.ArrayList;
import java.util.List;
import org.junit.jupiter.api.Test;

class OrchestratorRecordTest {
    private static final String CONTROL = "acme/control";
    private static final String SHA = "0123456789abcdef0123456789abcdef01234567";
    private static final Clock CLOCK = Clock.fixed(Instant.parse("2026-01-02T03:04:05Z"), ZoneOffset.UTC);
    private static final ResolutionRequest REQUEST =
            new ResolutionRequest(Role.ORCHESTRATOR, CONTROL, "dep", null, null, null, null, null);

    private static ControlPolicy policy(String inventory, String campaign) {
        String json = "{\"version\":1,\"control-plane\":{" + inventory + "\"campaigns\":{\"dep\":" + campaign + "}}}";
        return PolicyParser.parse(json.getBytes(StandardCharsets.UTF_8));
    }

    private static List<InventoryPartition.Repository> repos(int count) {
        List<InventoryPartition.Repository> list = new ArrayList<>();
        for (int i = 1; i <= count; i++) {
            list.add(new InventoryPartition.Repository(i, "acme/r" + i, false, false));
        }
        return list;
    }

    private static OrchestratorEvidence evidence(List<InventoryPartition.Repository> inventory, int dispatchMax) {
        return new OrchestratorEvidence(inventory, "", false, dispatchMax, List.of(
                new OrchestratorEvidence.WorkerWorkflow("dep-planner", true, "active"),
                new OrchestratorEvidence.WorkerWorkflow("dep-off", true, "active"),
                new OrchestratorEvidence.WorkerWorkflow("dep-gone", false, ""),
                new OrchestratorEvidence.WorkerWorkflow("dep-paused", true, "disabled_manually"),
                new OrchestratorEvidence.WorkerWorkflow("stranger", true, "active")));
    }

    private static final String CAMPAIGN = "{\"mode\":\"live\",\"max-repositories\":50,\"rollout-percent\":50,"
            + "\"targets\":{\"acme/r2\":{\"mode\":\"review\"}},\"workers\":{"
            + "\"planner\":{\"workflow\":\"dep-planner\",\"max-mode\":\"live\"},"
            + "\"off\":{\"workflow\":\"dep-off\",\"enabled\":false},"
            + "\"gone\":{\"workflow\":\"dep-gone\"},\"paused\":{\"workflow\":\"dep-paused\"}}}";

    private static ObjectNode build(ControlPolicy policy, OrchestratorEvidence evidence) {
        return EffectiveRecord.buildOrchestrator(policy, PolicyResolver.resolve(policy, REQUEST), evidence, SHA, CLOCK);
    }

    @Test
    void recordsCapsCandidatesAndWorkerSkipReasons() {
        ObjectNode record = build(policy("", CAMPAIGN), evidence(repos(10), 4));
        assertTrue(record.get("authorized").asBoolean());
        assertEquals("orchestrator", record.get("control_role").asText());
        assertEquals(50, record.get("max_repos").asInt());
        // 50% of 10 candidates = 5; one eligible worker -> dispatch cap 4; policy cap 50.
        assertEquals(4, record.get("effective_max_repos").asInt());
        assertEquals(10, record.get("candidate_repositories").size());
        assertEquals("review", record.get("candidate_repositories").get(1).get("safe_output_mode").asText());
        assertEquals("live", record.get("candidate_repositories").get(0).get("safe_output_mode").asText());
        assertTrue(record.get("inventory_version").asText().startsWith("sha256:"));
        assertEquals(1, record.get("batch_count").asInt());
        var workers = record.get("worker_workflows");
        assertTrue(workers.get(0).get("eligible").asBoolean());
        assertEquals("worker disabled by control-plane policy", workers.get(1).get("skip_reason").asText());
        assertEquals("worker workflow unavailable", workers.get(2).get("skip_reason").asText());
        assertEquals("worker workflow disabled", workers.get(3).get("skip_reason").asText());
        assertEquals("worker is not part of installed campaign", workers.get(4).get("skip_reason").asText());
    }

    @Test
    void noEligibleWorkersMeansNoDispatchCapacity() {
        ControlPolicy policy = policy("", "{\"workers\":{\"off\":{\"workflow\":\"dep-off\",\"enabled\":false}}}");
        ObjectNode record = build(policy, evidence(repos(3), 5));
        assertEquals(0, record.get("effective_max_repos").asInt());
    }

    @Test
    void partitionIsDeterministicAndSelectsCellAndBatch() {
        ControlPolicy policy = policy("\"inventory\":{\"cell-count\":2,\"cell-index\":1,\"batch-size\":2,"
                + "\"batch-index\":1},", CAMPAIGN);
        List<InventoryPartition.Repository> shuffled = new ArrayList<>(repos(9));
        java.util.Collections.reverse(shuffled);
        ObjectNode first = build(policy, evidence(shuffled, 4));
        ObjectNode second = build(policy, evidence(repos(9), 4));
        assertEquals(first, second);
        // cell 1 holds ids 1,3,5,7,9 -> batches [1,3] [5,7] [9]; batch 1 is [5,7].
        assertEquals(3, first.get("batch_count").asInt());
        assertEquals(5, first.get("cell_repository_count").asInt());
        assertEquals(5, first.get("candidate_repositories").get(0).get("id").asInt());
        assertEquals(7, first.get("candidate_repositories").get(1).get("id").asInt());
        assertTrue(first.get("batch_id").asText().endsWith(":cell-1-of-2:batch-1-of-3"));
    }

    @Test
    void explicitTargetBypassesCellSelection() {
        ControlPolicy policy = policy("\"inventory\":{\"cell-count\":2,\"cell-index\":1},", CAMPAIGN);
        OrchestratorEvidence base = evidence(repos(4), 2);
        OrchestratorEvidence targeted = new OrchestratorEvidence(base.inventory(), "", true, 2, base.workflows());
        assertEquals(4, build(policy, targeted).get("cell_repository_count").asInt());
        assertEquals(2, build(policy, base).get("cell_repository_count").asInt());
    }

    @Test
    void failsClosedOnBadEvidence() {
        ControlPolicy policy = policy("\"inventory\":{\"max-scan-repositories\":3,\"batch-size\":2,"
                + "\"batch-index\":5},", CAMPAIGN);
        assertThrows(PolicyException.class, () -> build(policy, evidence(repos(4), 2)));
        assertThrows(PolicyException.class, () -> build(policy, evidence(repos(2), 2)));
        assertThrows(PolicyException.class, () -> build(policy, evidence(null, 2)));
        assertThrows(PolicyException.class, () -> build(policy, evidence(repos(2), 0)));
        assertThrows(PolicyException.class, () -> build(policy, null));
    }

    @Test
    void outOfScopeInventoryIsRejected() {
        ControlPolicy policy = policy("", CAMPAIGN);
        List<InventoryPartition.Repository> foreign =
                List.of(new InventoryPartition.Repository(1, "evil/repo", false, false));
        assertThrows(PolicyException.class, () -> build(policy, evidence(foreign, 2)));
    }

    @Test
    void deniedCampaignRecordsNoCandidates() {
        ControlPolicy policy = policy("", "{\"enabled\":false}");
        ObjectNode record = build(policy, null);
        assertFalse(record.get("authorized").asBoolean());
        assertEquals("campaign-disabled", record.get("reason").asText());
        assertEquals(0, record.get("candidate_repositories").size());
        assertEquals(0, record.get("effective_max_repos").asInt());
    }

    @Test
    void inventoryPolicyValidation() {
        for (String bad : new String[] {"{\"cell-count\":2,\"cell-index\":2}", "{\"cell-count\":0}",
            "{\"batch-size\":100001}", "{\"max-scan-repositories\":0}", "{\"batch-index\":-1}", "{\"x\":1}"}) {
            assertThrows(PolicyException.class, () -> policy("\"inventory\":" + bad + ",", CAMPAIGN), bad);
        }
        assertEquals(1000, policy("", CAMPAIGN).inventory().maxScanRepositories());
    }
}
