package org.cao.backend.policy;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

class StandaloneResolverTest {
    private static final String CONTROL = "acme/control";
    private static final String SHA = "0123456789abcdef0123456789abcdef01234567";

    private static ControlPolicy parse(String workflowJson, String controlExtras) {
        return PolicyParser.parse(("""
                {"version":1,"gh-aw-version":"v0.92.5","control-plane":{
                  "scope":{"allowed-owners":["acme"],"allowed-repositories":["acme/one","acme/two"]},
                  "workflows":{"audit":%s}%s}}
                """).formatted(workflowJson, controlExtras).getBytes(StandardCharsets.UTF_8));
    }

    private static StandaloneRequest request(String workflow, String mode, Integer max, Integer rollout) {
        return new StandaloneRequest(CONTROL, workflow,
                CONTROL + "/.github/workflows/" + workflow + ".lock.yml@refs/heads/main", SHA, null, mode, max,
                rollout, null, null, null, null);
    }

    private static StandaloneRequest target(String workflow, String target, String mode) {
        return new StandaloneRequest(CONTROL, workflow,
                CONTROL + "/.github/workflows/" + workflow + ".lock.yml@refs/heads/main", SHA, target, mode, null,
                null, null, null, null, null);
    }

    @Test
    void resolvesDefaultsWithoutCampaignOrWorker() {
        StandaloneResolution r = StandaloneResolver.resolve(parse("{}", ""), request("audit", null, null, null));
        assertTrue(r.authorized());
        assertEquals("authorized", r.reason());
        assertEquals(Mode.REVIEW, r.mode());
        assertEquals(1, r.maxRepositories());
        assertEquals(100, r.rolloutPercent());
        assertEquals(List.of("acme"), r.allowedOwners());
        assertEquals(List.of("acme/one", "acme/two"), r.allowedRepositories());
        assertEquals(ControlPolicy.Inventory.defaults(), r.inventory());
    }

    @Test
    void undeclaredAndDisabledDenyAndNeverFallBackToCampaigns() {
        ControlPolicy onlyCampaign = PolicyParser.parse(
                "{\"version\":1,\"control-plane\":{\"campaigns\":{\"audit\":{}}}}".getBytes(StandardCharsets.UTF_8));
        StandaloneResolution undeclared = StandaloneResolver.resolve(onlyCampaign, request("audit", null, null, null));
        assertFalse(undeclared.authorized());
        assertEquals("workflow-undeclared", undeclared.reason());
        assertEquals(Mode.REVIEW, undeclared.mode());
        assertEquals(0, undeclared.maxRepositories());
        assertEquals("workflow-disabled",
                StandaloneResolver.resolve(parse("{\"enabled\":false}", ""), request("audit", null, null, null))
                        .reason());
    }

    @Test
    void identityMustMatchStaticSlugAndWorkflowReference() {
        ControlPolicy policy = parse("{}", "");
        for (String slug : List.of("../audit", "Audit", "", "constructor_x")) {
            assertThrows(PolicyException.class,
                    () -> StandaloneResolver.resolve(policy, request(slug, null, null, null)), slug);
        }
        StandaloneRequest wrongRef = new StandaloneRequest(CONTROL, "audit",
                CONTROL + "/.github/workflows/other.lock.yml@refs/heads/main", SHA, null, null, null, null, null,
                null, null, null);
        assertThrows(PolicyException.class, () -> StandaloneResolver.resolve(policy, wrongRef));
        StandaloneRequest otherRepo = new StandaloneRequest(CONTROL, "audit",
                "evil/control/.github/workflows/audit.lock.yml@refs/heads/main", SHA, null, null, null, null, null,
                null, null, null);
        assertThrows(PolicyException.class, () -> StandaloneResolver.resolve(policy, otherRepo));
        StandaloneRequest emptyRevision = new StandaloneRequest(CONTROL, "audit",
                CONTROL + "/.github/workflows/audit.lock.yml@", SHA, null, null, null, null, null, null, null, null);
        assertThrows(PolicyException.class, () -> StandaloneResolver.resolve(policy, emptyRevision));
        StandaloneRequest badSha = new StandaloneRequest(CONTROL, "audit",
                CONTROL + "/.github/workflows/audit.lock.yml@refs/heads/main", "main", null, null, null, null, null,
                null, null, null);
        assertThrows(PolicyException.class, () -> StandaloneResolver.resolve(policy, badSha));
    }

    @Test
    void dispatcherEnvelopeAndDebugAreRejected() {
        ControlPolicy policy = parse("{}", "");
        StandaloneRequest withCorrelation = new StandaloneRequest(CONTROL, "audit",
                CONTROL + "/.github/workflows/audit.lock.yml@refs/heads/main", SHA, null, null, null, null, null,
                "corr-1", null, null);
        assertThrows(PolicyException.class, () -> StandaloneResolver.resolve(policy, withCorrelation));
        assertThrows(PolicyException.class,
                () -> StandaloneResolver.resolve(policy, request("audit", "debug", null, null)));
    }

    @Test
    void defaultsTargetsCeilingAndNarrowingAreCumulative() {
        ControlPolicy policy = parse("""
                {"max-repositories":8,"rollout-percent":50,"targets":{"acme/one":{"mode":"live"}}}""",
                ",\"defaults\":{\"mode\":\"review\",\"max-repositories\":10,\"rollout-percent\":80}");
        StandaloneResolution inherited = StandaloneResolver.resolve(policy, request("audit", null, null, null));
        assertEquals(Mode.REVIEW, inherited.mode());
        assertEquals(Mode.LIVE, inherited.targetModes().get("acme/one"));
        assertEquals(8, inherited.maxRepositories());
        assertEquals(50, inherited.rolloutPercent());
        assertEquals(Mode.LIVE, StandaloneResolver.resolve(policy, target("audit", "ACME/ONE", null)).mode());
        StandaloneResolution narrowed = StandaloneResolver.resolve(policy, request("audit", "review", 3, 25));
        assertEquals(Mode.REVIEW, narrowed.targetModes().get("acme/one"));
        assertEquals(3, narrowed.maxRepositories());
        assertEquals(25, narrowed.rolloutPercent());
    }

    @Test
    void workflowCeilingNarrowsEveryTargetAndLiveRequestWidens() {
        ControlPolicy policy = parse("""
                {"max-mode":"review","mode":"live","targets":{"acme/one":{"mode":"live"}}}""", "");
        StandaloneResolution r = StandaloneResolver.resolve(policy, target("audit", "acme/one", null));
        assertEquals(Mode.REVIEW, r.mode());
        assertEquals(Mode.REVIEW, r.targetModes().get("acme/one"));
        PolicyException e = assertThrows(PolicyException.class,
                () -> StandaloneResolver.resolve(policy, target("audit", "acme/one", "live")));
        assertTrue(e.getMessage().contains("exceeds checked-in policy"));
    }

    @Test
    void widenedLimitsAreRejected() {
        ControlPolicy policy = parse("{}", "");
        assertThrows(PolicyException.class,
                () -> StandaloneResolver.resolve(policy, request("audit", null, 2, null)));
        assertThrows(PolicyException.class,
                () -> StandaloneResolver.resolve(policy, request("audit", null, null, 101)));
        assertThrows(PolicyException.class,
                () -> StandaloneResolver.resolve(policy, request("audit", "live", null, null)));
        assertThrows(PolicyException.class,
                () -> StandaloneResolver.resolve(policy, request("audit", "bogus", null, null)));
    }

    @Test
    void outOfScopeTargetIsRejected() {
        ControlPolicy policy = parse("{}", "");
        assertThrows(PolicyException.class,
                () -> StandaloneResolver.resolve(policy, target("audit", "acme/three", null)));
        assertThrows(PolicyException.class,
                () -> StandaloneResolver.resolve(policy, target("audit", "other/one", null)));
    }

    @Test
    void ownerDefaultsToControlRepositoryOwner() {
        ControlPolicy policy = PolicyParser.parse(
                "{\"version\":1,\"control-plane\":{\"workflows\":{\"audit\":{}}}}".getBytes(StandardCharsets.UTF_8));
        assertEquals(List.of("acme"),
                StandaloneResolver.resolve(policy, request("audit", null, null, null)).allowedOwners());
        assertThrows(PolicyException.class,
                () -> StandaloneResolver.resolve(policy, target("audit", "other/x", null)));
    }

    @Test
    void parserRejectsCapabilitiesTopologyScopeEscapesAndPersistentDebug() {
        List<String> invalid = List.of(
                "{\"workers\":{}}", "{\"engine\":\"copilot\"}", "{\"tools\":{}}", "{\"permissions\":{}}",
                "{\"max-ai-credits\":100}", "{\"workflow\":\"other\"}", "{\"deploy\":true}", "{\"enabled\":\"true\"}",
                "{\"mode\":\"debug\"}", "{\"max-mode\":\"debug\"}", "{\"max-repositories\":1001}",
                "{\"rollout-percent\":0}", "{\"targets\":{\"outside/target\":{\"mode\":\"review\"}}}",
                "{\"targets\":{\"acme/three\":{\"mode\":\"live\"}}}",
                "{\"targets\":{\"acme/one\":{\"mode\":\"debug\"}}}",
                "{\"targets\":{\"acme/one\":{\"mode\":\"review\"},\"ACME/ONE\":{\"mode\":\"live\"}}}");
        for (String workflow : invalid) {
            assertThrows(PolicyException.class, () -> parse(workflow, ""), workflow);
        }
    }

    @Test
    void parserValidatesInventoryBounds() {
        assertEquals(new ControlPolicy.Inventory(50, 4, 3, 10, 2),
                parse("{}", ",\"inventory\":{\"max-scan-repositories\":50,\"cell-count\":4,\"cell-index\":3,"
                        + "\"batch-size\":10,\"batch-index\":2}").inventory());
        for (String inventory : List.of(
                "{\"max-scan-repositories\":0}", "{\"max-scan-repositories\":100001}", "{\"cell-count\":1001}",
                "{\"cell-count\":2,\"cell-index\":2}", "{\"cell-index\":1}", "{\"batch-size\":0}",
                "{\"batch-index\":-1}", "{\"unknown\":1}", "[]")) {
            assertThrows(PolicyException.class, () -> parse("{}", ",\"inventory\":" + inventory), inventory);
        }
    }

    @Test
    void campaignPoliciesAreUnchangedByWorkflowDeclarations() {
        ControlPolicy policy = parse("{}", ",\"campaigns\":{\"c\":{\"workers\":{\"w\":{\"workflow\":\"audit\"}}}}");
        assertEquals(Map.of("audit", policy.workflows().get("audit")), policy.workflows());
        assertTrue(policy.campaigns().containsKey("c"));
    }
}
