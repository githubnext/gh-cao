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
import java.util.Optional;
import org.junit.jupiter.api.Test;

class StandalonePrecomputeTest {
    private static final String CONTROL = "acme/control";
    private static final String SHA = "0123456789abcdef0123456789abcdef01234567";
    private static final Clock CLOCK = Clock.fixed(Instant.parse("2026-11-20T21:00:00.789Z"), ZoneOffset.UTC);
    // sha256 of the three rows below as produced by the Actions resolver's inventoryDigest (JSON.stringify).
    private static final String JS_DIGEST = "sha256:f131d23c47a754fb96dfc96d33fb0dd3b5aeb742670b83414b8903edda1fd96b";

    private static RepositoryRecord repo(long id, String name, boolean archived, boolean disabled) {
        return new RepositoryRecord(id, name, archived, disabled, true, null, "main");
    }

    private static ControlPolicy policy(String workflow, String inventory) {
        return PolicyParser.parse(("""
                {"version":1,"control-plane":{
                  "scope":{"allowed-owners":["acme"]},%s
                  "workflows":{"audit":%s}}}
                """).formatted(inventory, workflow).getBytes(StandardCharsets.UTF_8));
    }

    private static StandaloneRequest request(String target, String destination) {
        return new StandaloneRequest(CONTROL, "audit",
                CONTROL + "/.github/workflows/audit.lock.yml@refs/heads/main", SHA, target, null, null, null,
                destination, null, null, null);
    }

    private static JsonNode build(ControlPolicy policy, StandaloneRequest request, InventoryEvidence evidence,
            ReviewDestinations destinations) {
        return StandalonePrecompute.build(StandaloneResolver.resolve(policy, request), request, evidence,
                destinations, CLOCK);
    }

    private static List<RepositoryRecord> many(int count) {
        List<RepositoryRecord> list = new ArrayList<>();
        for (int i = 1; i <= count; i++) {
            list.add(repo(i, "acme/r" + i, false, false));
        }
        return list;
    }

    @Test
    void inventoryDigestMatchesActionsResolverBytes() {
        List<RepositoryRecord> rows = List.of(
                new RepositoryRecord(7, "acme/two", false, false, true, "2026-01-02T03:04:05Z", "main"),
                new RepositoryRecord(3, "acme/one", false, false, false, null, "ma\"in\u00e9\u2028"),
                new RepositoryRecord(5, "acme/old", true, false, true, null, null));
        InventoryPartition partition = InventoryPartition.create(ControlPolicy.Inventory.defaults(), rows, false);
        assertEquals(JS_DIGEST, partition.version());
        assertEquals(3, partition.repositoryCount());
    }

    @Test
    void admitsDeterministicSubsetWithRecordFieldsAndModes() {
        ControlPolicy policy = policy("{\"max-repositories\":10,\"rollout-percent\":50,"
                + "\"targets\":{\"acme/r2\":{\"mode\":\"live\"}}}", "");
        JsonNode record = build(policy, request(null, CONTROL),
                InventoryEvidence.of(InventoryEvidence.Source.ORGANIZATION, many(6)), ReviewDestinations.none());
        assertEquals("standalone", record.get("control_role").asText());
        assertEquals("audit", record.get("workflow").asText());
        assertEquals("", record.get("campaign").asText());
        assertEquals("", record.get("worker").asText());
        assertEquals(0, record.get("worker_workflows").size());
        assertEquals(3, record.get("effective_max_repos").asInt());
        assertEquals(10, record.get("max_repos").asInt());
        assertEquals("organization", record.get("repo_source").asText());
        assertEquals(SHA, record.get("workflow_sha").asText());
        assertEquals(64, record.get("policy_sha256").asText().length());
        assertEquals("2026-11-20T21:00:00Z", record.get("resolved_at").asText());
        assertEquals(CONTROL, record.get("policy_source").get("repository").asText());
        JsonNode candidates = record.get("candidate_repositories");
        assertEquals(3, candidates.size());
        assertEquals("acme/r1", candidates.get(0).get("full_name").asText());
        assertEquals("review", candidates.get(0).get("safe_output_mode").asText());
        assertEquals(CONTROL, candidates.get(0).get("safe_output_repo").asText());
        assertEquals("live", candidates.get(1).get("safe_output_mode").asText());
        assertEquals("acme/r2", candidates.get(1).get("safe_output_repo").asText());
        assertEquals(record.get("inventory_version").asText() + ":cell-0-of-1:batch-0-of-1",
                record.get("batch_id").asText());
    }

    @Test
    void archivedAndDisabledRepositoriesAreExcludedAndEmptySetIsNoop() {
        ControlPolicy policy = policy("{\"max-repositories\":5}", "");
        JsonNode record = build(policy, request(null, CONTROL), InventoryEvidence.of(
                InventoryEvidence.Source.ORGANIZATION,
                List.of(repo(1, "acme/a", true, false), repo(2, "acme/b", false, true), repo(3, "acme/c", false, false))),
                ReviewDestinations.none());
        assertEquals(1, record.get("candidate_repositories").size());
        assertEquals("acme/c", record.get("candidate_repositories").get(0).get("full_name").asText());
        JsonNode empty = build(policy, request(null, CONTROL), InventoryEvidence.of(
                InventoryEvidence.Source.ORGANIZATION, List.of(repo(1, "acme/a", true, true))),
                ReviewDestinations.none());
        assertEquals(0, empty.get("candidate_repositories").size());
        assertEquals(0, empty.get("effective_max_repos").asInt());
    }

    @Test
    void cellAndBatchPartitionIsDeterministicAndBounded() {
        ControlPolicy policy = policy("{\"max-repositories\":100}",
                "\"inventory\":{\"cell-count\":2,\"cell-index\":1,\"batch-size\":2,\"batch-index\":1},");
        InventoryEvidence evidence = InventoryEvidence.of(InventoryEvidence.Source.ORGANIZATION, many(12));
        JsonNode first = build(policy, request(null, CONTROL), evidence, ReviewDestinations.none());
        JsonNode second = build(policy, request(null, CONTROL),
                InventoryEvidence.of(InventoryEvidence.Source.ORGANIZATION, reversed(many(12))),
                ReviewDestinations.none());
        assertEquals(first.get("candidate_repositories"), second.get("candidate_repositories"));
        assertEquals(first.get("inventory_version"), second.get("inventory_version"));
        assertEquals(6, first.get("cell_repository_count").asInt());
        assertEquals(3, first.get("batch_count").asInt());
        assertEquals("acme/r5", first.get("candidate_repositories").get(0).get("full_name").asText());
        assertEquals("acme/r7", first.get("candidate_repositories").get(1).get("full_name").asText());

        ControlPolicy outOfRange = policy("{}",
                "\"inventory\":{\"cell-count\":2,\"cell-index\":1,\"batch-size\":2,\"batch-index\":3},");
        assertThrows(PolicyException.class,
                () -> build(outOfRange, request(null, CONTROL), evidence, ReviewDestinations.none()));
    }

    private static List<RepositoryRecord> reversed(List<RepositoryRecord> rows) {
        List<RepositoryRecord> copy = new ArrayList<>(rows);
        java.util.Collections.reverse(copy);
        return copy;
    }

    @Test
    void duplicateIdentitiesCollapseToOne() {
        ControlPolicy policy = policy("{\"max-repositories\":5}", "");
        JsonNode record = build(policy, request(null, CONTROL), InventoryEvidence.of(
                InventoryEvidence.Source.ORGANIZATION,
                List.of(repo(1, "acme/a", false, false), repo(1, "acme/a", false, false))), ReviewDestinations.none());
        assertEquals(1, record.get("inventory_repository_count").asInt());
    }

    @Test
    void unavailableInventoryFailsClosed() {
        ControlPolicy policy = policy("{}", "");
        assertThrows(PolicyException.class, () -> build(policy, request(null, CONTROL),
                InventoryEvidence.unavailable(InventoryEvidence.Source.ORGANIZATION, "api down"),
                ReviewDestinations.none()));
        assertThrows(PolicyException.class, () -> build(policy, request(null, CONTROL),
                InventoryEvidence.unavailable(InventoryEvidence.Source.ORGANIZATION, null),
                ReviewDestinations.none()));
        assertFalse(InventoryEvidence.unavailable(InventoryEvidence.Source.ALLOWED_REPOS, "").available());
    }

    @Test
    void scanBoundIsEnforced() {
        ControlPolicy policy = policy("{}", "\"inventory\":{\"max-scan-repositories\":2},");
        assertThrows(PolicyException.class, () -> build(policy, request(null, CONTROL),
                InventoryEvidence.of(InventoryEvidence.Source.ORGANIZATION, many(3)), ReviewDestinations.none()));
    }

    @Test
    void reviewDestinationRules() {
        ControlPolicy policy = policy("{}", "");
        InventoryEvidence evidence = InventoryEvidence.of(InventoryEvidence.Source.ORGANIZATION, many(1));
        assertThrows(PolicyException.class,
                () -> build(policy, request(null, "acme/r1"), evidence, ReviewDestinations.none()));
        assertThrows(PolicyException.class,
                () -> build(policy, request(null, null), evidence, ReviewDestinations.none()));
        assertThrows(PolicyException.class,
                () -> build(policy, request(null, "acme/review"), evidence, ReviewDestinations.none()));
        assertThrows(PolicyException.class, () -> build(policy, request(null, "acme/review"), evidence,
                repository -> Optional.of(false)));
        assertThrows(PolicyException.class,
                () -> build(policy, request(null, "other/review"), evidence, repository -> Optional.of(true)));
        JsonNode ok = build(policy, request(null, "acme/review"), evidence, repository -> Optional.of(true));
        assertEquals("acme/review", ok.get("candidate_repositories").get(0).get("safe_output_repo").asText());
    }

    @Test
    void liveTargetsAreBoundToTheirOwnRepository() {
        ControlPolicy policy = policy("{\"mode\":\"live\",\"max-repositories\":2}", "");
        JsonNode record = build(policy, request(null, null),
                InventoryEvidence.of(InventoryEvidence.Source.ORGANIZATION, many(2)), ReviewDestinations.none());
        for (JsonNode candidate : record.get("candidate_repositories")) {
            assertEquals("live", candidate.get("safe_output_mode").asText());
            assertEquals(candidate.get("full_name").asText(), candidate.get("safe_output_repo").asText());
        }
    }

    @Test
    void targetEvidenceMustMatchTheEnrolledIdentity() {
        ControlPolicy policy = PolicyParser.parse(
                ("{\"version\":1,\"control-plane\":{\"scope\":{\"allowed-repositories\":[\"acme/r1\",\"acme/r2\"]},"
                        + "\"workflows\":{\"audit\":{}}}}").getBytes(StandardCharsets.UTF_8));
        JsonNode record = build(policy, request("ACME/R1", CONTROL),
                InventoryEvidence.of(InventoryEvidence.Source.TARGET_REPO, many(1)), ReviewDestinations.none());
        assertEquals("ACME/R1", record.get("target_repo").asText());
        assertEquals(1, record.get("candidate_repositories").size());
        assertThrows(PolicyException.class, () -> build(policy, request("acme/r1", CONTROL),
                InventoryEvidence.of(InventoryEvidence.Source.TARGET_REPO, List.of(repo(2, "acme/r2", false, false))),
                ReviewDestinations.none()));
        assertThrows(PolicyException.class, () -> build(policy, request("acme/r1", CONTROL),
                InventoryEvidence.of(InventoryEvidence.Source.ORGANIZATION, many(1)), ReviewDestinations.none()));
        assertThrows(PolicyException.class, () -> build(policy, request(null, CONTROL),
                InventoryEvidence.of(InventoryEvidence.Source.TARGET_REPO, many(1)), ReviewDestinations.none()));
        assertThrows(PolicyException.class, () -> build(policy, request("acme/r1", CONTROL),
                InventoryEvidence.of(InventoryEvidence.Source.TARGET_REPO, many(2)), ReviewDestinations.none()));
    }

    @Test
    void inventoryOutsideScopeIsRejectedNotSilentlyAdmitted() {
        ControlPolicy policy = PolicyParser.parse(
                ("{\"version\":1,\"control-plane\":{\"scope\":{\"allowed-repositories\":[\"acme/r1\"]},"
                        + "\"workflows\":{\"audit\":{\"max-repositories\":5}}}}").getBytes(StandardCharsets.UTF_8));
        assertThrows(PolicyException.class, () -> build(policy, request(null, CONTROL),
                InventoryEvidence.of(InventoryEvidence.Source.ALLOWED_REPOS, many(2)), ReviewDestinations.none()));
        assertThrows(PolicyException.class, () -> build(policy, request(null, "other/review"),
                InventoryEvidence.of(InventoryEvidence.Source.ALLOWED_REPOS, many(1)), ReviewDestinations.none()));
    }

    @Test
    void deniedResolutionAndMismatchedIdentityCannotProduceRecords() {
        ControlPolicy policy = policy("{\"enabled\":false}", "");
        StandaloneRequest request = request(null, CONTROL);
        StandaloneResolution denied = StandaloneResolver.resolve(policy, request);
        assertFalse(denied.authorized());
        assertThrows(PolicyException.class, () -> StandalonePrecompute.build(denied, request,
                InventoryEvidence.of(InventoryEvidence.Source.ORGANIZATION, many(1)), ReviewDestinations.none(), CLOCK));
        ControlPolicy enabled = policy("{}", "");
        StandaloneResolution ok = StandaloneResolver.resolve(enabled, request);
        StandaloneRequest other = new StandaloneRequest("acme/other", "audit",
                "acme/other/.github/workflows/audit.lock.yml@refs/heads/main", SHA, null, null, null, null, CONTROL,
                null, null, null);
        assertThrows(PolicyException.class, () -> StandalonePrecompute.build(ok, other,
                InventoryEvidence.of(InventoryEvidence.Source.ORGANIZATION, many(1)), ReviewDestinations.none(), CLOCK));
        assertTrue(ok.authorized());
    }

    @Test
    void repositoryRecordRejectsMalformedMetadata() {
        assertThrows(PolicyException.class, () -> new RepositoryRecord(0, "acme/a", false, false, false, null, null));
        assertThrows(PolicyException.class, () -> new RepositoryRecord(1, "bad", false, false, false, null, null));
    }
}
