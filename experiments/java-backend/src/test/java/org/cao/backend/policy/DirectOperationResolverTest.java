package org.cao.backend.policy;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import org.junit.jupiter.api.Test;

/** Direct operations and authority envelopes: CAO-EXE-006, specs/control-architecture.md §4.6, §5.3. */
class DirectOperationResolverTest {
    private static final String CONTROL = "acme/control";
    private static final String SHA = "0123456789abcdef0123456789abcdef01234567";

    private static ControlPolicy parse(String controlPlane) {
        return PolicyParser.parse(("{\"version\":1,\"control-plane\":" + controlPlane + "}")
                .getBytes(StandardCharsets.UTF_8));
    }

    private static final ControlPolicy POLICY = parse("""
            {"scope":{"allowed-owners":["acme"]},
             "defaults":{"max-repositories":3},
             "operations":{
               "triage":{"mode":"live","max-repositories":3,"targets":{"acme/web":{"mode":"review"}}},
               "reviewer":{"mode":"review","max-repositories":2},
               "paused":{"enabled":false}}}
            """);

    private static ExecutionBinding binding(String slug) {
        return new ExecutionBinding(CONTROL, SHA, CONTROL + "/.github/workflows/" + slug + ".lock.yml@refs/heads/main",
                "42", "1");
    }

    private static DirectOperationResolver.Request request(String op, List<String> targets, String mode,
            String destination, Integer max) {
        return new DirectOperationResolver.Request(1, op, targets, mode, destination, max, null, null);
    }

    @Test
    void parserRejectsWorkersRolloutAndConflictsInOperations() {
        assertThrows(PolicyException.class, () -> parse("{\"operations\":{\"x\":{\"workers\":{}}}}"));
        assertThrows(PolicyException.class, () -> parse("{\"operations\":{\"x\":{\"rollout-percent\":5}}}"));
        assertThrows(PolicyException.class, () -> parse("{\"operations\":{\"x\":{\"mode\":\"debug\"}}}"));
        assertThrows(PolicyException.class, () -> parse("{\"operations\":{\"x\":{\"max-mode\":\"debug\"}}}"));
        assertThrows(PolicyException.class, () -> parse("{\"operations\":{\"x\":{\"engine\":\"copilot\"}}}"));
        assertThrows(PolicyException.class, () -> parse("{\"workflows\":{\"x\":{\"workers\":{}}}}"));
        assertThrows(PolicyException.class, () -> parse("{\"operations\":{\"Bad\":{}}}"));
        assertThrows(PolicyException.class, () -> parse("{\"operations\":{\"x\":{}},\"workflows\":{\"x\":{}}}"));
        assertThrows(PolicyException.class, () -> parse("{\"operations\":{\"x\":{}},\"campaigns\":{\"x\":{}}}"));
        assertThrows(PolicyException.class, () -> parse(
                "{\"operations\":{\"x\":{}},\"campaigns\":{\"c\":{\"workers\":{\"w\":{\"workflow\":\"x\"}}}}}"));
    }

    @Test
    void parserEnforcesScopeOnWorkflowAndOperationTargets() {
        assertThrows(PolicyException.class, () -> parse("{\"scope\":{\"allowed-repositories\":[\"acme/a\"]},"
                + "\"operations\":{\"x\":{\"targets\":{\"acme/b\":{\"mode\":\"review\"}}}}}"));
        assertThrows(PolicyException.class, () -> parse("{\"scope\":{\"allowed-owners\":[\"acme\"]},"
                + "\"workflows\":{\"x\":{\"targets\":{\"other/b\":{\"mode\":\"review\"}}}}}"));
        assertThrows(PolicyException.class, () -> parse("{\"scope\":{\"allowed-owners\":[\"acme\"]},"
                + "\"campaigns\":{\"c\":{\"targets\":{\"other/b\":{\"mode\":\"review\"}}}}}"));
    }

    @Test
    void undeclaredAndDisabledOperationsAreDenied() {
        DirectOperationResolver.Decision undeclared = DirectOperationResolver.resolve(POLICY,
                request("ghost", List.of("acme/web"), null, null, null), binding("ghost"));
        assertFalse(undeclared.authorized());
        assertEquals("operation-undeclared", undeclared.reason());
        assertEquals("operation-disabled", DirectOperationResolver.resolve(POLICY,
                request("paused", List.of("acme/web"), null, null, null), binding("paused")).reason());
    }

    @Test
    void identityMustMatchTheCompiledWorkflowReference() {
        assertThrows(PolicyException.class, () -> DirectOperationResolver.resolve(POLICY,
                request("triage", List.of("acme/api"), null, null, null), binding("reviewer")));
        assertThrows(PolicyException.class, () -> new ExecutionBinding(CONTROL, "short", "x", "1", "1"));
        assertThrows(PolicyException.class, () -> new ExecutionBinding(CONTROL, SHA,
                CONTROL + "/.github/workflows/triage.lock.yml@main", "0", "1"));
        assertThrows(PolicyException.class, () -> new ExecutionBinding(CONTROL, SHA,
                "other/repo/.github/workflows/triage.lock.yml@main", "1", "1"));
    }

    @Test
    void defaultsToReviewWithControlRepositoryDestination() {
        DirectOperationResolver.Decision d = DirectOperationResolver.resolve(POLICY,
                request("triage", List.of("acme/api", "acme/db"), null, null, null), binding("triage"));
        assertTrue(d.authorized());
        AuthorityEnvelope e = d.envelope();
        assertEquals(List.of(new AuthorityEnvelope.Grant("acme/api", Mode.REVIEW, CONTROL),
                new AuthorityEnvelope.Grant("acme/db", Mode.REVIEW, CONTROL)), e.grants());
        assertFalse(e.delegation());
        assertEquals(AuthorityEnvelope.REQUIRED_CONTROLS, e.requirements());
        assertEquals(binding("triage"), e.binding());
    }

    @Test
    void singleLiveTargetUsesTargetDestinationAndExactTargetCeilingApplies() {
        DirectOperationResolver.Decision live = DirectOperationResolver.resolve(POLICY,
                request("triage", List.of("acme/api"), "live", null, null), binding("triage"));
        assertEquals(new AuthorityEnvelope.Grant("acme/api", Mode.LIVE, "acme/api"), live.envelope().grants().get(0));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.resolve(POLICY,
                request("triage", List.of("acme/web"), "live", null, null), binding("triage")));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.resolve(POLICY,
                request("triage", List.of("acme/api"), "live", "acme/other", null), binding("triage")));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.resolve(POLICY,
                request("triage", List.of("acme/api", "acme/db"), "live", null, null), binding("triage")));
    }

    @Test
    void wideningAndScopeViolationsFailClosed() {
        assertThrows(PolicyException.class, () -> DirectOperationResolver.resolve(POLICY,
                request("reviewer", List.of("acme/api"), "live", null, null), binding("reviewer")));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.resolve(POLICY,
                request("reviewer", List.of("acme/a", "acme/b", "acme/c"), null, null, null), binding("reviewer")));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.resolve(POLICY,
                request("reviewer", List.of("acme/a"), null, null, 5), binding("reviewer")));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.resolve(POLICY,
                request("reviewer", List.of("evil/x"), null, null, null), binding("reviewer")));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.resolve(POLICY,
                request("reviewer", List.of("acme/a"), null, "evil/inbox", null), binding("reviewer")));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.resolve(POLICY,
                request("reviewer", List.of("acme/a", "ACME/A"), null, null, null), binding("reviewer")));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.resolve(POLICY,
                request("reviewer", List.of(), null, null, null), binding("reviewer")));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.resolve(POLICY,
                request("reviewer", null, null, null, null), binding("reviewer")));
    }

    @Test
    void exactTargetLiveIsCappedByTheRequestedModeSoGrantsStayCommon() {
        ControlPolicy mixed = parse("{\"operations\":{\"o\":{\"mode\":\"review\",\"max-repositories\":2,"
                + "\"targets\":{\"acme/a\":{\"mode\":\"live\"}}}}}");
        DirectOperationResolver.Decision d = DirectOperationResolver.resolve(mixed,
                request("o", List.of("acme/a", "acme/b"), null, null, null), binding("o"));
        assertTrue(d.authorized());
        assertEquals(Mode.REVIEW, d.envelope().grants().get(0).mode());
    }

    @Test
    void requestCannotCarryDispatcherEnvelopeRolloutOrDebugOrUnknownVersion() {
        assertThrows(PolicyException.class, () -> DirectOperationResolver.resolve(POLICY,
                new DirectOperationResolver.Request(1, "reviewer", List.of("acme/a"), null, null, null, null,
                        new DispatcherInputs(null, "acme/control", null)), binding("reviewer")));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.resolve(POLICY,
                new DirectOperationResolver.Request(1, "reviewer", List.of("acme/a"), null, null, null, 50, null),
                binding("reviewer")));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.resolve(POLICY,
                request("reviewer", List.of("acme/a"), "debug", null, null), binding("reviewer")));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.resolve(POLICY,
                new DirectOperationResolver.Request(2, "reviewer", List.of("acme/a"), null, null, null, null, null),
                binding("reviewer")));
    }

    @Test
    void handoffMustEqualTheCurrentRequestAndPolicy() {
        ExecutionBinding b = binding("reviewer");
        DirectOperationResolver.Decision current = DirectOperationResolver.resolve(POLICY,
                request("reviewer", List.of("acme/a", "acme/b"), null, null, null), b);
        DirectOperationResolver.requireHandoff(current.envelope(), current, b);

        // a narrower admitted envelope is still not what the current request produces
        DirectOperationResolver.Decision narrower = DirectOperationResolver.resolve(POLICY,
                request("reviewer", List.of("acme/a"), null, null, null), b);
        assertThrows(PolicyException.class,
                () -> DirectOperationResolver.requireHandoff(narrower.envelope(), current, b));

        // a widened admitted envelope is rejected as widening
        AuthorityEnvelope e = current.envelope();
        AuthorityEnvelope widened = new AuthorityEnvelope(1, e.operation(), b, 2,
                List.of(new AuthorityEnvelope.Grant("acme/a", Mode.LIVE, "acme/a"), e.grants().get(1)),
                e.requirements(), false);
        assertThrows(PolicyException.class, () -> DirectOperationResolver.requireHandoff(widened, current, b));

        // a binding mismatch (different run) is rejected
        ExecutionBinding other = new ExecutionBinding(CONTROL, SHA, b.workflowRef(), "43", "1");
        assertThrows(PolicyException.class, () -> DirectOperationResolver.requireHandoff(e, current, other));

        DirectOperationResolver.Decision denied = DirectOperationResolver.resolve(POLICY,
                request("ghost", List.of("acme/a"), null, null, null), binding("ghost"));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.requireHandoff(e, denied, b));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.requireHandoff(null, current, b));
    }

    @Test
    void envelopeValidationRejectsDelegationUnsupportedControlsAndDuplicates() {
        ExecutionBinding b = binding("reviewer");
        List<AuthorityEnvelope.Grant> grants = List.of(new AuthorityEnvelope.Grant("acme/a", Mode.REVIEW, CONTROL));
        List<String> controls = AuthorityEnvelope.REQUIRED_CONTROLS;
        assertThrows(PolicyException.class,
                () -> new AuthorityEnvelope(1, "reviewer", b, 1, grants, controls, true).validate(b, controls));
        assertThrows(PolicyException.class,
                () -> new AuthorityEnvelope(2, "reviewer", b, 1, grants, controls, false).validate(b, controls));
        assertThrows(PolicyException.class,
                () -> new AuthorityEnvelope(1, "reviewer", b, 1, grants, List.of("policy-revision"), false)
                        .validate(b, controls));
        assertThrows(PolicyException.class,
                () -> new AuthorityEnvelope(1, "reviewer", b, 1, grants,
                        List.of("policy-revision", "target-scope", "output-routing", "extra"), false)
                        .validate(b, controls));
        assertThrows(PolicyException.class, () -> new AuthorityEnvelope(1, "reviewer", b, 2,
                List.of(grants.get(0), new AuthorityEnvelope.Grant("ACME/a", Mode.REVIEW, CONTROL)), controls, false)
                .validate(b, controls));
        assertThrows(PolicyException.class,
                () -> new AuthorityEnvelope(1, "reviewer", b, 1, List.of(), controls, false).validate(b, controls));
    }

    @Test
    void prepareRejectsRedirectsArchivedDisabledDuplicatesAndMissingEvidence() {
        ExecutionBinding b = binding("reviewer");
        AuthorityEnvelope e = DirectOperationResolver.resolve(POLICY,
                request("reviewer", List.of("acme/a", "acme/b"), null, null, null), b).envelope();
        Map<String, RepositoryEvidence> good = Map.of(
                "acme/a", new RepositoryEvidence("acme/a", 1, false, false),
                "acme/b", new RepositoryEvidence("ACME/B", 2, false, false));
        assertEquals(2, DirectOperationResolver.prepare(e, name -> Optional.ofNullable(good.get(name)), g -> true).size());

        assertThrows(PolicyException.class, () -> DirectOperationResolver.prepare(e, name -> Optional.empty(), g -> true));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.prepare(e,
                name -> Optional.of(new RepositoryEvidence("acme/renamed", 1, false, false)), g -> true));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.prepare(e,
                name -> Optional.of(new RepositoryEvidence(name, 1, true, false)), g -> true));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.prepare(e,
                name -> Optional.of(new RepositoryEvidence(name, 1, false, true)), g -> true));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.prepare(e,
                name -> Optional.of(new RepositoryEvidence(name, 0, false, false)), g -> true));
        // two names resolving to one repository id
        assertThrows(PolicyException.class, () -> DirectOperationResolver.prepare(e,
                name -> Optional.of(new RepositoryEvidence(name, 9, false, false)), g -> true));
        assertThrows(PolicyException.class, () -> DirectOperationResolver.prepare(e,
                name -> Optional.ofNullable(good.get(name)), g -> false));
    }

    @Test
    void narrowingComparisonRejectsWideningChildren() {
        ExecutionBinding b = binding("triage");
        AuthorityEnvelope parent = DirectOperationResolver.resolve(POLICY,
                request("triage", List.of("acme/a", "acme/b"), null, null, null), b).envelope();
        AuthorityEnvelope child = DirectOperationResolver.resolve(POLICY,
                request("triage", List.of("acme/a"), null, null, 1), b).envelope();
        List<String> controls = AuthorityEnvelope.REQUIRED_CONTROLS;
        AuthorityEnvelope.requireNarrowing(parent, child, controls);
        AuthorityEnvelope extra = DirectOperationResolver.resolve(POLICY,
                request("triage", List.of("acme/c"), null, null, null), b).envelope();
        assertThrows(PolicyException.class, () -> AuthorityEnvelope.requireNarrowing(parent, extra, controls));
        assertThrows(PolicyException.class, () -> AuthorityEnvelope.requireNarrowing(child, parent, controls));
    }
}
