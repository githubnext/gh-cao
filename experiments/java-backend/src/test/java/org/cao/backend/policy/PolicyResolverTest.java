package org.cao.backend.policy;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.charset.StandardCharsets;
import org.junit.jupiter.api.Test;

class PolicyResolverTest {
    private static final String CONTROL = "acme/control";

    private static final ControlPolicy LIVE_POLICY = PolicyParser.parse("""
            {"version":1,"control-plane":{
              "scope":{"allowed-repositories":["acme/payments-api","acme/web"]},
              "defaults":{"max-repositories":5,"rollout-percent":50,"monthly-ai-credit-budget":99},
              "campaigns":{
                "dependabot":{"mode":"live","max-repositories":3,
                  "targets":{"acme/web":{"mode":"review"}},
                  "workers":{"planner":{"workflow":"dependabot-planner"},
                             "capped":{"workflow":"dependabot-capped","max-mode":"review"},
                             "off":{"workflow":"dependabot-off","enabled":false}}},
                "paused":{"enabled":false}}}}
            """.getBytes(StandardCharsets.UTF_8));

    private static ResolutionRequest worker(String name, String target, String mode, Integer max, Integer rollout) {
        return new ResolutionRequest(Role.WORKER, CONTROL, "dependabot", name, target, mode, max, rollout);
    }

    private static ResolutionRequest orchestrator(String campaign, String mode, Integer max, Integer rollout) {
        return new ResolutionRequest(Role.ORCHESTRATOR, CONTROL, campaign, null, null, mode, max, rollout);
    }

    @Test
    void absentCampaignDefaultsToReviewDenied() {
        Resolution r = PolicyResolver.resolve(LIVE_POLICY, orchestrator("unknown", null, null, null));
        assertFalse(r.authorized());
        assertEquals("campaign-undeclared", r.reason());
        assertEquals(Mode.REVIEW, r.mode());
    }

    @Test
    void disabledCampaignAndWorkerAreDenied() {
        assertEquals("campaign-disabled",
                PolicyResolver.resolve(LIVE_POLICY, orchestrator("paused", null, null, null)).reason());
        assertEquals("worker-disabled",
                PolicyResolver.resolve(LIVE_POLICY, worker("off", "acme/payments-api", null, null, null)).reason());
    }

    @Test
    void unknownWorkerIsFatal() {
        assertThrows(PolicyException.class,
                () -> PolicyResolver.resolve(LIVE_POLICY, worker("ghost", "acme/payments-api", null, null, null)));
    }

    @Test
    void resolvesCampaignModeCapsAndDigest() {
        Resolution r = PolicyResolver.resolve(LIVE_POLICY, worker("planner", "acme/payments-api", null, null, null));
        assertTrue(r.authorized());
        assertEquals(Mode.LIVE, r.mode());
        assertEquals(3, r.maxRepositories());
        assertEquals(50, r.rolloutPercent());
        assertEquals(LIVE_POLICY.digest(), r.policyDigest());
    }

    @Test
    void exactTargetPolicyAndWorkerCeilingNarrowMode() {
        assertEquals(Mode.REVIEW,
                PolicyResolver.resolve(LIVE_POLICY, worker("planner", "acme/WEB", null, null, null)).mode());
        assertEquals(Mode.REVIEW,
                PolicyResolver.resolve(LIVE_POLICY, worker("capped", "acme/payments-api", null, null, null)).mode());
    }

    // T-RES-002
    @Test
    void liveRequestUnderReviewCeilingIsRejectedAsWidening() {
        assertThrows(PolicyException.class,
                () -> PolicyResolver.resolve(LIVE_POLICY, worker("capped", "acme/payments-api", "live", null, null)));
        assertThrows(PolicyException.class,
                () -> PolicyResolver.resolve(LIVE_POLICY, worker("planner", "acme/web", "live", null, null)));
    }

    // T-RES-003
    @Test
    void lowerRequestsAreAcceptedAndRaisedRequestsRejected() {
        Resolution r = PolicyResolver.resolve(LIVE_POLICY, worker("planner", "acme/payments-api", "review", 2, 10));
        assertEquals(Mode.REVIEW, r.mode());
        assertEquals(2, r.maxRepositories());
        assertEquals(10, r.rolloutPercent());
        assertThrows(PolicyException.class,
                () -> PolicyResolver.resolve(LIVE_POLICY, worker("planner", "acme/payments-api", null, 4, null)));
        assertThrows(PolicyException.class,
                () -> PolicyResolver.resolve(LIVE_POLICY, worker("planner", "acme/payments-api", null, null, 51)));
        assertThrows(PolicyException.class,
                () -> PolicyResolver.resolve(LIVE_POLICY, worker("planner", "acme/payments-api", null, 0, null)));
    }

    // T-ARC-001, T-EXE-005
    @Test
    void targetsOutsideScopeAreRejected() {
        assertThrows(PolicyException.class,
                () -> PolicyResolver.resolve(LIVE_POLICY, worker("planner", "acme/other", null, null, null)));
        assertThrows(PolicyException.class,
                () -> PolicyResolver.resolve(LIVE_POLICY, worker("planner", "evil/payments-api", null, null, null)));
        assertThrows(PolicyException.class,
                () -> PolicyResolver.resolve(LIVE_POLICY, worker("planner", "not a repo", null, null, null)));
        assertThrows(PolicyException.class,
                () -> PolicyResolver.resolve(LIVE_POLICY, worker("planner", null, null, null, null)));
    }

    @Test
    void ownerScopeDefaultsToControlOwnerAndIsCumulativeWithRepositories() {
        ControlPolicy open = PolicyParser.parse("{\"version\":1,\"control-plane\":{\"campaigns\":{\"c\":{\"workers\":{\"w\":{\"workflow\":\"w\"}}}}}}"
                .getBytes(StandardCharsets.UTF_8));
        ResolutionRequest inOwner = new ResolutionRequest(Role.WORKER, CONTROL, "c", "w", "acme/x", null, null, null);
        assertEquals(Mode.REVIEW, PolicyResolver.resolve(open, inOwner).mode());
        assertEquals(1, PolicyResolver.resolve(open, inOwner).maxRepositories());
        assertThrows(PolicyException.class, () -> PolicyResolver.resolve(open,
                new ResolutionRequest(Role.WORKER, CONTROL, "c", "w", "other/x", null, null, null)));
    }

    // T-CFG-005 / CAO-EXE-003: never debug through ordinary resolution
    @Test
    void debugIsNotAdmittedWithoutVerifiedManualDispatch() {
        assertThrows(PolicyException.class,
                () -> PolicyResolver.resolve(LIVE_POLICY, worker("planner", "acme/payments-api", "debug", null, null)));
        assertThrows(PolicyException.class,
                () -> PolicyResolver.resolve(LIVE_POLICY, orchestrator("dependabot", "debug", null, null)));
    }

    @Test
    void orchestratorMustNotNameWorkerOrTarget() {
        assertThrows(PolicyException.class, () -> PolicyResolver.resolve(LIVE_POLICY,
                new ResolutionRequest(Role.ORCHESTRATOR, CONTROL, "dependabot", "planner", null, null, null, null)));
        assertThrows(PolicyException.class, () -> PolicyResolver.resolve(LIVE_POLICY,
                new ResolutionRequest(Role.ORCHESTRATOR, CONTROL, "dependabot", null, "acme/web", null, null, null)));
    }

    @Test
    void identityMustBeStaticSlugAndRepositoryWellFormed() {
        assertThrows(PolicyException.class, () -> PolicyResolver.resolve(LIVE_POLICY, orchestrator("Bad Name", null, null, null)));
        assertThrows(PolicyException.class, () -> PolicyResolver.resolve(LIVE_POLICY,
                new ResolutionRequest(Role.ORCHESTRATOR, "bogus", "dependabot", null, null, null, null, null)));
    }

    // T-ARC-003: deprecated budget has no admission effect
    @Test
    void monthlyBudgetHasNoAdmissionEffect() {
        assertTrue(PolicyResolver.resolve(LIVE_POLICY, orchestrator("dependabot", null, null, null)).authorized());
    }
}
