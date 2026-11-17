package org.cao.backend.policy;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.List;
import org.junit.jupiter.api.Test;

/** Standalone workflows: CAO-EXE-005, specs/control-architecture.md §5.3, §5.4, §6.3. */
class StandaloneResolverTest {
    private static final String CONTROL = "acme/control";
    private static final String SHA = "0123456789abcdef0123456789abcdef01234567";

    private static ControlPolicy parse(String controlPlane) {
        return PolicyParser.parse(("{\"version\":1,\"control-plane\":" + controlPlane + "}")
                .getBytes(StandardCharsets.UTF_8));
    }

    private static final ControlPolicy POLICY = parse("""
            {"scope":{"allowed-owners":["acme"]},
             "defaults":{"max-repositories":10,"rollout-percent":100},
             "workflows":{
               "repo-health":{"mode":"live","max-repositories":4,"targets":{"acme/web":{"mode":"review"}}},
               "capped":{"mode":"live","max-mode":"review"},
               "paused":{"enabled":false}},
             "campaigns":{"dependabot":{"workers":{"planner":{"workflow":"dependabot-planner"}}}}}
            """);

    private static StandaloneResolver.Request request(String workflow, String mode, Integer max, Integer rollout) {
        return new StandaloneResolver.Request(CONTROL, workflow,
                CONTROL + "/.github/workflows/" + workflow + ".lock.yml@refs/heads/main", mode, max, rollout, null,
                null);
    }

    private static RepositoryEvidence repo(String name, long id, boolean archived, boolean disabled) {
        return new RepositoryEvidence(name, id, archived, disabled);
    }

    @Test
    void undeclaredAndDisabledWorkflowsAreDenied() {
        StandaloneResolver.Resolution undeclared = StandaloneResolver.resolve(POLICY, request("unknown", null, null, null));
        assertFalse(undeclared.authorized());
        assertEquals("workflow-undeclared", undeclared.reason());
        assertEquals("workflow-disabled",
                StandaloneResolver.resolve(POLICY, request("paused", null, null, null)).reason());
    }

    @Test
    void resolvesPrecedenceAndPerTargetModes() {
        StandaloneResolver.Resolution r = StandaloneResolver.resolve(POLICY, request("repo-health", null, null, null));
        assertTrue(r.authorized());
        assertEquals(Mode.LIVE, r.mode());
        assertEquals(4, r.maxRepositories());
        assertEquals(100, r.rolloutPercent());
        assertEquals(Mode.REVIEW, r.targetModes().get("acme/web"));
        assertEquals(CONTROL, r.reviewRepository());
    }

    @Test
    void maxModeNarrowsEveryTarget() {
        StandaloneResolver.Resolution r = StandaloneResolver.resolve(POLICY, request("capped", null, null, null));
        assertEquals(Mode.REVIEW, r.mode());
        assertThrows(PolicyException.class,
                () -> StandaloneResolver.resolve(POLICY, request("capped", "live", null, null)));
    }

    @Test
    void requestsOnlyNarrow() {
        StandaloneResolver.Resolution r = StandaloneResolver.resolve(POLICY, request("repo-health", "review", 2, 10));
        assertEquals(Mode.REVIEW, r.mode());
        assertEquals(2, r.maxRepositories());
        assertEquals(10, r.rolloutPercent());
        assertThrows(PolicyException.class,
                () -> StandaloneResolver.resolve(POLICY, request("repo-health", null, 5, null)));
        assertThrows(PolicyException.class,
                () -> StandaloneResolver.resolve(POLICY, request("repo-health", null, null, 101)));
    }

    @Test
    void identityMustMatchWorkflowRefAndBeUnambiguous() {
        StandaloneResolver.Request mismatch = new StandaloneResolver.Request(CONTROL, "repo-health",
                CONTROL + "/.github/workflows/other.lock.yml@refs/heads/main", null, null, null, null, null);
        assertThrows(PolicyException.class, () -> StandaloneResolver.resolve(POLICY, mismatch));
        StandaloneResolver.Request noRef = new StandaloneResolver.Request(CONTROL, "repo-health",
                CONTROL + "/.github/workflows/repo-health.lock.yml@", null, null, null, null, null);
        assertThrows(PolicyException.class, () -> StandaloneResolver.resolve(POLICY, noRef));
        assertThrows(PolicyException.class,
                () -> StandaloneResolver.resolve(POLICY, request("dependabot-planner", null, null, null)));
        assertThrows(PolicyException.class, () -> StandaloneResolver.resolve(parse(
                "{\"workflows\":{\"x\":{}},\"campaigns\":{\"c\":{\"workers\":{\"w\":{\"workflow\":\"x\"}}}}}"),
                request("x", null, null, null)));
    }

    @Test
    void dispatcherEnvelopeAndDebugAreRejected() {
        StandaloneResolver.Request carried = new StandaloneResolver.Request(CONTROL, "repo-health",
                CONTROL + "/.github/workflows/repo-health.lock.yml@refs/heads/main", null, null, null, null,
                new DispatcherInputs("corr-1", null, null));
        assertThrows(PolicyException.class, () -> StandaloneResolver.resolve(POLICY, carried));
        assertThrows(PolicyException.class,
                () -> StandaloneResolver.resolve(POLICY, request("repo-health", "debug", null, null)));
    }

    @Test
    void admitsDeterministicCappedSubsetExcludingArchivedAndDisabled() {
        StandaloneResolver.Resolution r = StandaloneResolver.resolve(POLICY, request("repo-health", null, null, 50));
        List<RepositoryEvidence> inventory = List.of(
                repo("acme/zeta", 6, false, false), repo("acme/web", 2, false, false),
                repo("acme/old", 3, true, false), repo("acme/off", 4, false, true),
                repo("acme/alpha", 1, false, false), repo("acme/mid", 5, false, false));
        List<StandaloneResolver.Admitted> admitted = StandaloneResolver.admit(POLICY, r, inventory);
        // 4 candidates * 50% = 2, ordered by name
        assertEquals(List.of("acme/alpha", "acme/mid"),
                admitted.stream().map(StandaloneResolver.Admitted::fullName).toList());
        assertEquals(admitted, StandaloneResolver.admit(POLICY, r, inventory.reversed()));
        StandaloneResolver.Resolution all = StandaloneResolver.resolve(POLICY, request("repo-health", null, null, null));
        List<StandaloneResolver.Admitted> everyone = StandaloneResolver.admit(POLICY, all, inventory);
        assertEquals(4, everyone.size());
        StandaloneResolver.Admitted web = everyone.stream().filter(a -> a.fullName().equals("acme/web")).findFirst().get();
        assertEquals(Mode.REVIEW, web.mode());
        assertEquals(CONTROL, web.destination());
        StandaloneResolver.Admitted alpha = everyone.get(0);
        assertEquals(Mode.LIVE, alpha.mode());
        assertEquals("acme/alpha", alpha.destination());
    }

    @Test
    void emptyInventoryNoOpsAndUnavailableInventoryFailsClosed() {
        StandaloneResolver.Resolution r = StandaloneResolver.resolve(POLICY, request("repo-health", null, null, null));
        assertTrue(StandaloneResolver.admit(POLICY, r, List.of()).isEmpty());
        assertTrue(StandaloneResolver.admit(POLICY, r, List.of(repo("acme/old", 3, true, false))).isEmpty());
        assertThrows(PolicyException.class, () -> StandaloneResolver.admit(POLICY, r, null));
        assertThrows(PolicyException.class,
                () -> StandaloneResolver.admit(POLICY, r, List.of(repo("acme/web", 0, false, false))));
        assertThrows(PolicyException.class, () -> StandaloneResolver.admit(POLICY, r,
                List.of(repo("acme/a", 7, false, false), repo("acme/b", 7, false, false))));
    }

    @Test
    void outOfScopeInventoryIsNeverAdmitted() {
        StandaloneResolver.Resolution r = StandaloneResolver.resolve(POLICY, request("repo-health", null, 4, 100));
        assertThrows(PolicyException.class,
                () -> StandaloneResolver.admit(POLICY, r, List.of(repo("evil/steal", 9, false, false))));
    }

    @Test
    void deniedResolutionAdmitsNothing() {
        StandaloneResolver.Resolution denied = StandaloneResolver.resolve(POLICY, request("unknown", null, null, null));
        assertThrows(PolicyException.class,
                () -> StandaloneResolver.admit(POLICY, denied, List.of(repo("acme/web", 2, false, false))));
    }

    @Test
    void reviewDestinationMustStayInOwnerScope() {
        StandaloneResolver.Request outside = new StandaloneResolver.Request(CONTROL, "repo-health",
                CONTROL + "/.github/workflows/repo-health.lock.yml@refs/heads/main", null, null, null, "evil/inbox", null);
        assertThrows(PolicyException.class, () -> StandaloneResolver.resolve(POLICY, outside));
    }

    @Test
    void effectiveRecordCarriesStandaloneProvenance() {
        StandaloneResolver.Resolution r = StandaloneResolver.resolve(POLICY, request("repo-health", null, null, 100));
        List<StandaloneResolver.Admitted> admitted = StandaloneResolver.admit(POLICY, r,
                List.of(repo("acme/web", 2, false, false), repo("acme/api", 1, false, false)));
        Clock clock = Clock.fixed(Instant.parse("2026-01-02T03:04:05Z"), ZoneOffset.UTC);
        var record = EffectiveRecord.buildStandalone(r, admitted, SHA, clock);
        assertEquals("standalone", record.get("control_role").asText());
        assertEquals("repo-health", record.get("workflow").asText());
        assertEquals("", record.get("campaign").asText());
        assertEquals("", record.get("worker").asText());
        assertEquals(0, record.get("worker_workflows").size());
        assertEquals(2, record.get("candidate_repositories").size());
        assertEquals("acme/api", record.get("candidate_repositories").get(0).get("full_name").asText());
        assertEquals("live", record.get("candidate_repositories").get(0).get("safe_output_mode").asText());
        assertEquals(POLICY.digest(), record.get("policy_sha256").asText());
        assertEquals("2026-01-02T03:04:05Z", record.get("resolved_at").asText());
        assertThrows(PolicyException.class, () -> EffectiveRecord.buildStandalone(r, admitted, "main", clock));
        StandaloneResolver.Resolution denied = StandaloneResolver.resolve(POLICY, request("unknown", null, null, null));
        var deniedRecord = EffectiveRecord.buildStandalone(denied, List.of(), SHA, clock);
        assertFalse(deniedRecord.get("authorized").asBoolean());
        assertEquals("workflow-undeclared", deniedRecord.get("reason").asText());
        assertEquals(0, deniedRecord.get("candidate_repositories").size());
        assertThrows(PolicyException.class, () -> EffectiveRecord.buildStandalone(denied, admitted, SHA, clock));
    }
}
