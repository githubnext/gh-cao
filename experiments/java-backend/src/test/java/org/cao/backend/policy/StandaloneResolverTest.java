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
import java.util.List;
import java.util.function.UnaryOperator;
import org.junit.jupiter.api.Test;

class StandaloneResolverTest {
    private static final String SHA = "0123456789abcdef0123456789abcdef01234567";
    private static final Clock CLOCK = Clock.fixed(Instant.parse("2026-01-02T03:04:05Z"), ZoneOffset.UTC);
    private static final String REF = "acme/control/.github/workflows/audit.lock.yml@refs/heads/main";

    private static ControlPolicy policy(String control) {
        return PolicyParser.parse(("{\"version\":1,\"control-plane\":" + control + "}").getBytes(StandardCharsets.UTF_8));
    }

    private static final ControlPolicy OPEN = policy("{\"scope\":{\"allowed-owners\":[\"acme\"]},"
            + "\"defaults\":{\"max-repositories\":10},"
            + "\"workflows\":{\"audit\":{\"mode\":\"review\",\"max-repositories\":5,\"rollout-percent\":50,"
            + "\"targets\":{\"acme/alpha\":{\"mode\":\"live\"}}},\"paused\":{\"enabled\":false}}}");

    private static InventoryRepository repo(long id, String name, boolean archived, boolean disabled) {
        return new InventoryRepository(id, name, archived, disabled, true, null, "main");
    }

    private static StandaloneRequest request(UnaryOperator<StandaloneRequest> change) {
        return change.apply(new StandaloneRequest("acme/control", REF, "audit", null, null, null, null, null,
                null, null, "acme/control", null, null, null));
    }

    private static InventoryEvidence org(InventoryRepository... repositories) {
        return new InventoryEvidence("organization", List.of(repositories), "");
    }

    private static JsonNode resolve(ControlPolicy policy, StandaloneRequest request, InventoryEvidence evidence) {
        return StandaloneResolver.resolve(policy, request, evidence, SHA, CLOCK);
    }

    @Test
    void admitsDeterministicSubsetWithPerTargetModeAndDestination() {
        JsonNode record = resolve(OPEN, request(r -> r), org(
                repo(4, "acme/delta", false, false), repo(2, "acme/beta", false, false),
                repo(1, "acme/alpha", false, false), repo(3, "acme/gamma", false, false)));
        assertTrue(record.get("authorized").asBoolean());
        assertEquals("standalone", record.get("control_role").asText());
        assertEquals("audit", record.get("workflow").asText());
        assertEquals("", record.get("campaign").asText());
        assertEquals("", record.get("worker").asText());
        assertEquals(0, record.get("worker_workflows").size());
        assertEquals(SHA, record.get("policy_source").get("sha").asText());
        assertEquals(4, record.get("inventory_repository_count").asInt());
        assertEquals(2, record.get("effective_max_repos").asInt());
        JsonNode candidates = record.get("candidate_repositories");
        assertEquals(2, candidates.size());
        assertEquals("acme/alpha", candidates.get(0).get("full_name").asText());
        assertEquals("live", candidates.get(0).get("safe_output_mode").asText());
        assertEquals("acme/alpha", candidates.get(0).get("safe_output_repo").asText());
        assertEquals("acme/beta", candidates.get(1).get("full_name").asText());
        assertEquals("review", candidates.get(1).get("safe_output_mode").asText());
        assertEquals("acme/control", candidates.get(1).get("safe_output_repo").asText());
        assertEquals("2026-01-02T03:04:05Z", record.get("resolved_at").asText());
    }

    @Test
    void inventoryVersionMatchesNodeResolverDigestVector() {
        JsonNode record = resolve(OPEN, request(r -> r), org(
                new InventoryRepository(30, "acme/gamma", false, false, true, null, "main"),
                new InventoryRepository(10, "acme/alpha", false, false, false, "2026-01-02T03:04:05Z", "main"),
                new InventoryRepository(20, "acme/beta", true, false, true, "2025-12-31T00:00:00Z", "trunk")));
        assertEquals("sha256:20e34cdea1f4d19a547e56a9d2d1c1d204eb2f59ae04780d1b1ef5317aac14b4",
                record.get("inventory_version").asText());
        assertTrue(record.get("batch_id").asText().endsWith(":cell-0-of-1:batch-0-of-1"));
    }

    @Test
    void excludesArchivedAndDisabledAndAnEmptySetPermitsOnlyNoop() {
        JsonNode record = resolve(OPEN, request(r -> r), org(
                repo(1, "acme/alpha", true, false), repo(2, "acme/beta", false, true)));
        assertTrue(record.get("authorized").asBoolean());
        assertEquals(0, record.get("effective_max_repos").asInt());
        assertEquals(0, record.get("candidate_repositories").size());
    }

    @Test
    void partitionsByCellAndBatchDeterministically() {
        ControlPolicy cells = policy("{\"scope\":{\"allowed-owners\":[\"acme\"]},"
                + "\"inventory\":{\"cell-count\":2,\"cell-index\":1,\"batch-size\":2,\"batch-index\":1},"
                + "\"workflows\":{\"audit\":{\"max-repositories\":100}}}");
        InventoryRepository[] all = new InventoryRepository[10];
        for (int i = 0; i < all.length; i++) {
            all[i] = repo(i + 1, "acme/r" + (i + 1), false, false);
        }
        JsonNode record = resolve(cells, request(r -> r), org(all));
        assertEquals(5, record.get("cell_repository_count").asInt());
        assertEquals(3, record.get("batch_count").asInt());
        assertEquals("acme/r5", record.get("candidate_repositories").get(0).get("full_name").asText());
        assertEquals("acme/r7", record.get("candidate_repositories").get(1).get("full_name").asText());
        JsonNode again = resolve(cells, request(r -> r), org(all));
        assertEquals(record.get("batch_id"), again.get("batch_id"));
        ControlPolicy beyond = policy("{\"scope\":{\"allowed-owners\":[\"acme\"]},"
                + "\"inventory\":{\"cell-count\":2,\"cell-index\":1,\"batch-size\":2,\"batch-index\":3},"
                + "\"workflows\":{\"audit\":{}}}");
        assertThrows(PolicyException.class, () -> resolve(beyond, request(r -> r), org(all)));
    }

    @Test
    void requestOnlyNarrowsAndWideningFailsClosed() {
        InventoryEvidence evidence = org(repo(1, "acme/alpha", false, false), repo(2, "acme/beta", false, false));
        JsonNode narrowed = resolve(OPEN, request(r -> new StandaloneRequest(r.controlRepository(), r.workflowRef(),
                r.workflow(), null, null, null, null, null, null, "review", "acme/control", null, 1, 50)), evidence);
        assertEquals("review", narrowed.get("candidate_repositories").get(0).get("safe_output_mode").asText());
        assertEquals(1, narrowed.get("effective_max_repos").asInt());
        assertThrows(PolicyException.class, () -> resolve(OPEN, request(r -> withMode(r, "live")), evidence));
        assertThrows(PolicyException.class, () -> resolve(OPEN, request(r -> withLimits(r, 6, null)), evidence));
        assertThrows(PolicyException.class, () -> resolve(OPEN, request(r -> withLimits(r, null, 51)), evidence));
        assertThrows(PolicyException.class, () -> resolve(OPEN, request(r -> withMode(r, "debug")), evidence));
    }

    private static StandaloneRequest withMode(StandaloneRequest r, String mode) {
        return new StandaloneRequest(r.controlRepository(), r.workflowRef(), r.workflow(), null, null, null, null,
                null, null, mode, r.safeOutputRepository(), null, null, null);
    }

    private static StandaloneRequest withLimits(StandaloneRequest r, Integer max, Integer rollout) {
        return new StandaloneRequest(r.controlRepository(), r.workflowRef(), r.workflow(), null, null, null, null,
                null, null, null, r.safeOutputRepository(), null, max, rollout);
    }

    @Test
    void maxModeCeilingNarrowsLiveTargets() {
        ControlPolicy capped = policy("{\"scope\":{\"allowed-owners\":[\"acme\"]},\"workflows\":{\"audit\":{"
                + "\"mode\":\"live\",\"max-mode\":\"review\",\"max-repositories\":3,"
                + "\"targets\":{\"acme/alpha\":{\"mode\":\"live\"}}}}}");
        JsonNode record = resolve(capped, request(r -> r), org(repo(1, "acme/alpha", false, false)));
        assertEquals("review", record.get("safe_output_mode").asText());
        assertEquals("review", record.get("candidate_repositories").get(0).get("safe_output_mode").asText());
        assertEquals("acme/control", record.get("candidate_repositories").get(0).get("safe_output_repo").asText());
    }

    @Test
    void undeclaredAndDisabledWorkflowsAreDeniedWithoutCandidates() {
        for (String[] pair : new String[][] {{"missing", "workflow-undeclared"}, {"paused", "workflow-disabled"}}) {
            String ref = "acme/control/.github/workflows/" + pair[0] + ".lock.yml@refs/heads/main";
            JsonNode record = resolve(OPEN, new StandaloneRequest("acme/control", ref, pair[0], null, null, null,
                    null, null, null, null, "acme/control", null, null, null), null);
            assertFalse(record.get("authorized").asBoolean());
            assertEquals(pair[1], record.get("reason").asText());
            assertEquals(0, record.get("candidate_repositories").size());
            assertEquals("review", record.get("safe_output_mode").asText());
        }
        ControlPolicy campaignOnly = policy("{\"campaigns\":{\"audit\":{}}}");
        assertEquals("workflow-undeclared", resolve(campaignOnly, request(r -> r), null).get("reason").asText());
    }

    @Test
    void identityMustMatchWorkflowRefAndRejectCampaignWorkerOrDispatcherInputs() {
        InventoryEvidence evidence = org(repo(1, "acme/alpha", false, false));
        assertThrows(PolicyException.class, () -> resolve(OPEN, new StandaloneRequest("acme/control",
                "acme/control/.github/workflows/other.lock.yml@refs/heads/main", "audit", null, null, null, null,
                null, null, null, "acme/control", null, null, null), evidence));
        assertThrows(PolicyException.class, () -> resolve(OPEN, new StandaloneRequest("acme/control",
                "acme/control/.github/workflows/audit.lock.yml@", "audit", null, null, null, null,
                null, null, null, "acme/control", null, null, null), evidence));
        assertThrows(PolicyException.class, () -> resolve(OPEN, new StandaloneRequest("acme/control", REF, "audit",
                "camp", null, null, null, null, null, null, "acme/control", null, null, null), evidence));
        assertThrows(PolicyException.class, () -> resolve(OPEN, new StandaloneRequest("acme/control", REF, "audit",
                null, "worker", null, null, null, null, null, "acme/control", null, null, null), evidence));
        assertThrows(PolicyException.class, () -> resolve(OPEN, new StandaloneRequest("acme/control", REF, "audit",
                null, null, "1-1", null, null, null, null, "acme/control", null, null, null), evidence));
        assertThrows(PolicyException.class, () -> resolve(OPEN, new StandaloneRequest("acme/control", REF, "audit",
                null, null, null, "acme/control", null, null, null, "acme/control", null, null, null), evidence));
        assertThrows(PolicyException.class, () -> resolve(OPEN, new StandaloneRequest("acme/control", REF, "audit",
                null, null, null, null, "https://github.com/acme/control/actions/runs/1", null, null,
                "acme/control", null, null, null), evidence));
    }

    @Test
    void missingOrUnavailableInventoryEvidenceFailsClosed() {
        assertThrows(PolicyException.class, () -> resolve(OPEN, request(r -> r), null));
        assertThrows(PolicyException.class, () -> resolve(OPEN, request(r -> r),
                new InventoryEvidence("organization", List.of(), "rate limited")));
        assertThrows(PolicyException.class, () -> resolve(OPEN, request(r -> r),
                new InventoryEvidence("allowed_repos", List.of(repo(1, "acme/alpha", false, false)), "")));
    }

    @Test
    void allowedRepositoriesBoundTheInventoryExactly() {
        ControlPolicy allowed = policy("{\"scope\":{\"allowed-repositories\":[\"acme/alpha\"]},"
                + "\"workflows\":{\"audit\":{\"max-repositories\":5}}}");
        InventoryEvidence good = new InventoryEvidence("allowed_repos", List.of(repo(1, "acme/alpha", false, false)), "");
        assertEquals(1, resolve(allowed, request(r -> r), good).get("candidate_repositories").size());
        assertThrows(PolicyException.class, () -> resolve(allowed, request(r -> r),
                new InventoryEvidence("allowed_repos", List.of(repo(2, "acme/beta", false, false)), "")));
        assertThrows(PolicyException.class, () -> resolve(allowed, request(r -> r),
                org(repo(1, "acme/alpha", false, false))));
        ControlPolicy tiny = policy("{\"scope\":{\"allowed-repositories\":[\"acme/alpha\",\"acme/beta\"]},"
                + "\"inventory\":{\"max-scan-repositories\":1},\"workflows\":{\"audit\":{}}}");
        assertThrows(PolicyException.class, () -> resolve(tiny, request(r -> r), good));
    }

    @Test
    void targetShortcutAdmitsOnlyTheScopedTargetAndRejectsOutOfScope() {
        ControlPolicy scoped = policy("{\"scope\":{\"allowed-repositories\":[\"acme/alpha\",\"acme/beta\"]},"
                + "\"workflows\":{\"audit\":{\"max-repositories\":5}}}");
        InventoryEvidence one = new InventoryEvidence("target_repo", List.of(repo(2, "acme/beta", false, false)), "");
        JsonNode record = resolve(scoped, request(r -> new StandaloneRequest(r.controlRepository(), r.workflowRef(),
                r.workflow(), null, null, null, null, null, "ACME/beta", null, "acme/control", null, null, null)), one);
        assertEquals("acme/beta", record.get("candidate_repositories").get(0).get("full_name").asText());
        assertEquals("target_repo", record.get("repo_source").asText());
        assertThrows(PolicyException.class, () -> resolve(scoped, request(r -> new StandaloneRequest(
                r.controlRepository(), r.workflowRef(), r.workflow(), null, null, null, null, null, "acme/other",
                null, "acme/control", null, null, null)),
                new InventoryEvidence("target_repo", List.of(repo(3, "acme/other", false, false)), "")));
        assertThrows(PolicyException.class, () -> resolve(OPEN, request(r -> new StandaloneRequest(
                r.controlRepository(), r.workflowRef(), r.workflow(), null, null, null, null, null, "evil/repo",
                null, "acme/control", null, null, null)),
                new InventoryEvidence("target_repo", List.of(repo(3, "evil/repo", false, false)), "")));
        assertThrows(PolicyException.class, () -> resolve(scoped, request(r -> new StandaloneRequest(
                r.controlRepository(), r.workflowRef(), r.workflow(), null, null, null, null, null, "acme/beta",
                null, "acme/control", null, null, null)),
                new InventoryEvidence("target_repo", List.of(repo(1, "acme/alpha", false, false)), "")));
    }

    @Test
    void reviewDestinationRules() {
        InventoryEvidence evidence = org(repo(2, "acme/beta", false, false));
        assertThrows(PolicyException.class, () -> resolve(OPEN, request(r -> withDestination(r, "acme/beta", true)),
                evidence));
        assertThrows(PolicyException.class, () -> resolve(OPEN, request(r -> withDestination(r, "acme/review", null)),
                evidence));
        assertThrows(PolicyException.class, () -> resolve(OPEN, request(r -> withDestination(r, "acme/review", false)),
                evidence));
        assertThrows(PolicyException.class, () -> resolve(OPEN, request(r -> withDestination(r, "elsewhere/review", true)),
                evidence));
        assertThrows(PolicyException.class, () -> resolve(OPEN, request(r -> withDestination(r, "", true)), evidence));
        JsonNode record = resolve(OPEN, request(r -> withDestination(r, "acme/review", true)), evidence);
        assertEquals("acme/review", record.get("candidate_repositories").get(0).get("safe_output_repo").asText());
    }

    private static StandaloneRequest withDestination(StandaloneRequest r, String destination, Boolean isPrivate) {
        return new StandaloneRequest(r.controlRepository(), r.workflowRef(), r.workflow(), null, null, null, null,
                null, null, null, destination, isPrivate, null, null);
    }

    @Test
    void parserValidatesWorkflowAndInventoryDeclarations() {
        for (String bad : new String[] {
            "{\"workflows\":{\"Audit\":{}}}",
            "{\"workflows\":{\"a\":{\"mode\":\"debug\"}}}",
            "{\"workflows\":{\"a\":{\"max-mode\":\"debug\"}}}",
            "{\"workflows\":{\"a\":{\"workers\":{}}}}",
            "{\"workflows\":{\"a\":{\"engine\":\"x\"}}}",
            "{\"workflows\":{\"a\":{\"targets\":{\"acme/x\":{}}}}}",
            "{\"scope\":{\"allowed-owners\":[\"acme\"]},\"workflows\":{\"a\":{\"targets\":{\"other/x\":{\"mode\":\"review\"}}}}}",
            "{\"scope\":{\"allowed-repositories\":[\"acme/y\"]},\"workflows\":{\"a\":{\"targets\":{\"acme/x\":{\"mode\":\"review\"}}}}}",
            "{\"inventory\":{\"cell-count\":2,\"cell-index\":2}}",
            "{\"inventory\":{\"cell-count\":1001}}",
            "{\"inventory\":{\"batch-size\":0}}",
            "{\"inventory\":{\"max-scan-repositories\":100001}}",
            "{\"inventory\":{\"unknown\":1}}"}) {
            assertThrows(PolicyException.class, () -> policy(bad), bad);
        }
        ControlPolicy ok = policy("{\"workflows\":{\"a\":{\"monthly-ai-credit-budget\":7}}}");
        assertEquals(ControlPolicy.Inventory.DEFAULTS, ok.inventory());
        assertTrue(ok.workflows().get("a").enabled());
    }
}
