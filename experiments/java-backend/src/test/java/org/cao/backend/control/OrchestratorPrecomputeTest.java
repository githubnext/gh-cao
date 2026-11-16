package org.cao.backend.control;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import java.io.IOException;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.time.Clock;
import java.time.Instant;
import java.time.ZoneOffset;
import java.util.ArrayList;
import java.util.List;
import java.util.Set;
import java.util.stream.Stream;
import org.cao.backend.policy.ControlPolicy;
import org.cao.backend.policy.PolicyException;
import org.cao.backend.policy.PolicyParser;
import org.cao.backend.policy.ResolutionRequest;
import org.cao.backend.policy.Role;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.MethodSource;

class OrchestratorPrecomputeTest {
    private static final ObjectMapper MAPPER = new ObjectMapper();
    private static final String SHA = "1111111111111111111111111111111111111111";
    private static final String REF = "acme/control/.github/workflows/dependabot.lock.yml@refs/heads/main";
    private static final Clock CLOCK = Clock.fixed(Instant.parse("2026-01-02T03:04:05Z"), ZoneOffset.UTC);
    // Deprecated credit/budget accounting fields: CAO ignores them (T-ARC-003) and Java does not emit them.
    private static final Set<String> NOT_EMITTED = Set.of("bundle", "orchestrator_credits",
            "worker_credits_per_target", "monthly_credit_budget", "monthly_ai_credits_spent",
            "monthly_ai_credits_remaining", "monthly_budget_error", "monthly_budget_target_cap");

    /** Recording fake that serves the synthetic evidence of one vector. */
    private static class Fakes implements RepositoryInventory, WorkflowCatalog {
        final List<RepositoryRecord> repositories;
        final List<WorkflowEntry> entries;
        final String source;
        final List<String> calls = new ArrayList<>();
        RuntimeException failure;

        Fakes(List<RepositoryRecord> repositories, List<WorkflowEntry> entries, List<String> configured) {
            this.repositories = repositories;
            this.entries = entries;
            this.source = "---\nsafe-outputs:\n  dispatch-workflow:\n    workflows: [" + String.join(", ", configured)
                    + "]\n---\n";
        }

        @Override
        public List<RepositoryRecord> load(List<String> exactRepositories) {
            calls.add("load:" + String.join(",", exactRepositories));
            if (failure != null) {
                throw failure;
            }
            return repositories;
        }

        @Override
        public List<WorkflowEntry> list() {
            calls.add("list");
            return entries;
        }

        @Override
        public String source(String path, String sha) {
            calls.add("source:" + path + "@" + sha);
            return source;
        }
    }

    private static JsonNode vectors() throws IOException {
        try (InputStream in = OrchestratorPrecomputeTest.class.getResourceAsStream(
                "/org/cao/backend/policy/orchestrator-vectors.json")) {
            return MAPPER.readTree(in);
        }
    }

    static Stream<JsonNode> vectorStream() throws IOException {
        List<JsonNode> all = new ArrayList<>();
        vectors().get("vectors").forEach(all::add);
        return all.stream();
    }

    private static RepositoryRecord repository(JsonNode n) {
        return new RepositoryRecord(n.get("id").asLong(), n.get("full_name").asText(), n.get("archived").asBoolean(),
                n.get("disabled").asBoolean(), n.get("private").asBoolean(),
                n.get("pushed_at").isNull() ? null : n.get("pushed_at").asText(),
                n.get("default_branch").isNull() ? null : n.get("default_branch").asText());
    }

    private static Fakes fakes(JsonNode vector) {
        List<RepositoryRecord> repositories = new ArrayList<>();
        vector.get("repositories").forEach(n -> repositories.add(repository(n)));
        List<WorkflowEntry> entries = new ArrayList<>();
        vector.get("actionsWorkflows").forEach(n -> entries.add(new WorkflowEntry(n.get("id").asLong(),
                n.get("name").asText(), n.get("path").asText(), n.get("state").asText())));
        List<String> configured = new ArrayList<>();
        vector.get("configuredWorkers").forEach(n -> configured.add(n.asText()));
        return new Fakes(repositories, entries, configured);
    }

    private static ResolutionRequest request(JsonNode vector) {
        JsonNode r = vector.get("request");
        return new ResolutionRequest(Role.ORCHESTRATOR, vector.get("controlRepository").asText(), "dependabot", null,
                null, r.has("mode") ? r.get("mode").asText() : null,
                r.has("maxRepositories") ? Integer.valueOf(r.get("maxRepositories").asText()) : null,
                r.has("rolloutPercent") ? Integer.valueOf(r.get("rolloutPercent").asText()) : null);
    }

    private static OrchestratorPrecompute.Context context(JsonNode vector) {
        return new OrchestratorPrecompute.Context(SHA, REF, vector.get("dispatchMax").asInt(),
                vector.get("safeOutputRepository").asText(), CLOCK);
    }

    private static ControlPolicy policy(String json) {
        return PolicyParser.parse(json.getBytes(StandardCharsets.UTF_8));
    }

    private static final String WORKERS = "\"workers\":{\"planner\":{\"workflow\":\"dependabot-planner\"}}";

    private static String doc(String inventory, String campaign) {
        return "{\"version\":1,\"control-plane\":{\"scope\":{\"allowed-repositories\":[\"acme/a\",\"acme/b\"]},"
                + inventory + "\"campaigns\":{\"dependabot\":{" + campaign + WORKERS + "}}}}";
    }

    private static Fakes simple() {
        return new Fakes(List.of(
                new RepositoryRecord(1, "acme/a", false, false, true, null, "main"),
                new RepositoryRecord(2, "acme/b", false, false, true, null, "main")),
                List.of(new WorkflowEntry(9, "p", ".github/workflows/dependabot-planner.lock.yml", "active")),
                List.of("dependabot-planner"));
    }

    private static ResolutionRequest plain() {
        return new ResolutionRequest(Role.ORCHESTRATOR, "acme/control", "dependabot", null, null, null, null, null);
    }

    private static OrchestratorPrecompute.Context ctx() {
        return new OrchestratorPrecompute.Context(SHA, REF, 2, "acme/control", CLOCK);
    }

    // T-RES-007 / CAO-CFG-003..: field-for-field agreement with the production resolver output.
    @ParameterizedTest(name = "{0}")
    @MethodSource("vectorStream")
    void matchesProductionPrecomputeVector(JsonNode vector) {
        Fakes fakes = fakes(vector);
        OrchestratorPrecompute.Outcome outcome = new OrchestratorPrecompute(fakes, fakes)
                .run(policy(vector.get("policy").asText()), request(vector), context(vector));
        ObjectNode actual = outcome.record();
        JsonNode expected = vector.get("expected");
        assertEquals(expected.get("activation_ready").asBoolean(), outcome.activationReady());
        expected.fieldNames().forEachRemaining(name -> {
            if (!NOT_EMITTED.contains(name)) {
                assertTrue(actual.has(name), "missing field " + name);
                assertEquals(expected.get(name).toString(), actual.get(name).toString(), name);
            }
        });
        assertEquals(SHA, actual.get("workflow_sha").asText());
        assertEquals(64, actual.get("policy_sha256").asText().length());
        assertEquals("2026-01-02T03:04:05Z", actual.get("resolved_at").asText());
        assertEquals(List.of("load:" + String.join(",", policy(vector.get("policy").asText()).scope().repositories()),
                "source:.github/workflows/dependabot.md@" + SHA, "list"), fakes.calls);
    }

    @Test
    void expectedDenialTouchesNoRemoteEvidence() {
        Fakes fakes = simple();
        ControlPolicy denied = policy(doc("", "\"enabled\":false,"));
        OrchestratorPrecompute.Outcome outcome = new OrchestratorPrecompute(fakes, fakes).run(denied, plain(), ctx());
        assertFalse(outcome.activationReady());
        assertFalse(outcome.record().get("authorized").asBoolean());
        assertEquals("campaign-disabled", outcome.record().get("reason").asText());
        assertEquals(0, outcome.record().get("effective_max_repos").asInt());
        assertEquals(0, outcome.record().get("candidate_repositories").size());
        assertEquals(List.of(), fakes.calls);
        ResolutionRequest undeclared =
                new ResolutionRequest(Role.ORCHESTRATOR, "acme/control", "other", null, null, null, null, null);
        assertEquals("campaign-undeclared", new OrchestratorPrecompute(fakes, fakes)
                .run(denied, undeclared, ctx()).record().get("reason").asText());
        assertEquals(List.of(), fakes.calls);
    }

    @Test
    void unavailableInventoryFailsClosedWithIncompleteRecord() {
        Fakes fakes = simple();
        fakes.failure = new InventoryUnavailableException("inventory-credential-unavailable", "no credential");
        OrchestratorPrecompute.Outcome outcome =
                new OrchestratorPrecompute(fakes, fakes).run(policy(doc("", "")), plain(), ctx());
        assertFalse(outcome.activationReady());
        ObjectNode record = outcome.record();
        assertEquals("incomplete", record.get("inventory_status").asText());
        assertEquals("inventory-credential-unavailable", record.get("reason").asText());
        assertFalse(record.get("activation_ready").asBoolean());
        assertEquals(0, record.get("candidate_repositories").size());
        assertEquals(0, record.get("worker_workflows").size());
        assertEquals(0, record.get("effective_max_repos").asInt());
    }

    @Test
    void missingOrUnexpectedRepositoryEvidenceIsIncomplete() {
        Fakes missing = new Fakes(List.of(new RepositoryRecord(1, "acme/a", false, false, true, null, "main")),
                simple().entries, List.of("dependabot-planner"));
        assertEquals("inventory-unavailable", new OrchestratorPrecompute(missing, missing)
                .run(policy(doc("", "")), plain(), ctx()).record().get("reason").asText());
        Fakes extra = new Fakes(List.of(
                new RepositoryRecord(1, "acme/a", false, false, true, null, "main"),
                new RepositoryRecord(2, "acme/b", false, false, true, null, "main"),
                new RepositoryRecord(3, "acme/z", false, false, true, null, "main")),
                simple().entries, List.of("dependabot-planner"));
        OrchestratorPrecompute.Outcome outcome =
                new OrchestratorPrecompute(extra, extra).run(policy(doc("", "")), plain(), ctx());
        assertEquals("inventory-response-invalid", outcome.record().get("reason").asText());
        assertFalse(outcome.activationReady());
    }

    @Test
    void ownerWideDiscoveryIsNotSupported() {
        Fakes fakes = simple();
        ControlPolicy noEnrollment = policy("{\"version\":1,\"control-plane\":{\"campaigns\":{\"dependabot\":{"
                + WORKERS + "}}}}");
        OrchestratorPrecompute.Outcome outcome =
                new OrchestratorPrecompute(fakes, fakes).run(noEnrollment, plain(), ctx());
        assertEquals("exact-enrollment-required", outcome.record().get("reason").asText());
        assertFalse(outcome.activationReady());
        assertEquals(List.of(), fakes.calls);
    }

    @Test
    void enrollmentOutsideOwnerScopeIsIncomplete() {
        ControlPolicy outside = policy("{\"version\":1,\"control-plane\":{\"scope\":{\"allowed-owners\":[\"acme\"],"
                + "\"allowed-repositories\":[\"other/a\"]},\"campaigns\":{\"dependabot\":{" + WORKERS + "}}}}");
        Fakes fakes = simple();
        OrchestratorPrecompute.Outcome outcome = new OrchestratorPrecompute(fakes, fakes).run(outside, plain(), ctx());
        assertEquals("target-outside-scope", outcome.record().get("reason").asText());
        assertEquals(List.of(), fakes.calls);
    }

    @Test
    void wideningAndInvalidRequestsAreFatal() {
        Fakes fakes = simple();
        OrchestratorPrecompute subject = new OrchestratorPrecompute(fakes, fakes);
        ControlPolicy review = policy(doc("", ""));
        assertThrows(PolicyException.class, () -> subject.run(review, new ResolutionRequest(Role.ORCHESTRATOR,
                "acme/control", "dependabot", null, null, "live", null, null), ctx()));
        assertThrows(PolicyException.class, () -> subject.run(review, new ResolutionRequest(Role.ORCHESTRATOR,
                "acme/control", "dependabot", null, null, null, 2, null), ctx()));
        assertThrows(PolicyException.class, () -> subject.run(review, new ResolutionRequest(Role.ORCHESTRATOR,
                "acme/control", "dependabot", null, null, "debug", null, null), ctx()));
        assertThrows(PolicyException.class, () -> subject.run(review, new ResolutionRequest(Role.WORKER,
                "acme/control", "dependabot", "planner", "acme/a", null, null, null), ctx()));
        assertEquals(List.of(), fakes.calls);
    }

    @Test
    void destinationAndContextBoundsAreEnforced() {
        Fakes fakes = simple();
        OrchestratorPrecompute subject = new OrchestratorPrecompute(fakes, fakes);
        ControlPolicy review = policy(doc("", ""));
        for (String destination : new String[] {"acme/elsewhere", "outside/control", "not-a-repository"}) {
            assertThrows(PolicyException.class, () -> subject.run(review, plain(),
                    new OrchestratorPrecompute.Context(SHA, REF, 2, destination, CLOCK)));
        }
        assertThrows(PolicyException.class, () -> subject.run(review, plain(),
                new OrchestratorPrecompute.Context("abc", REF, 2, "acme/control", CLOCK)));
        for (Integer max : new Integer[] {0, 1001}) {
            assertThrows(PolicyException.class, () -> subject.run(review, plain(),
                    new OrchestratorPrecompute.Context(SHA, REF, max, "acme/control", CLOCK)));
        }
        assertEquals(List.of(), fakes.calls);
    }

    @Test
    void liveCampaignAcceptsTargetBoundDestinationAndDefaultsDispatchMaxToOne() {
        Fakes fakes = simple();
        ObjectNode record = new OrchestratorPrecompute(fakes, fakes).run(
                policy(doc("", "\"mode\":\"live\",\"max-repositories\":5,")), plain(),
                new OrchestratorPrecompute.Context(SHA, REF, null, "", CLOCK)).record();
        assertEquals("live", record.get("safe_output_mode").asText());
        assertEquals(1, record.get("dispatch_max").asInt());
        assertEquals(1, record.get("effective_max_repos").asInt());
        assertEquals("live", record.get("candidate_repositories").get(0).get("safe_output_mode").asText());
    }

    @Test
    void batchBeyondSelectedCellAndScanOverflowAreFatal() {
        Fakes fakes = simple();
        OrchestratorPrecompute subject = new OrchestratorPrecompute(fakes, fakes);
        assertThrows(PolicyException.class, () -> subject.run(
                policy(doc("\"inventory\":{\"max-scan-repositories\":1},", "")), plain(), ctx()));
        assertEquals(List.of(), fakes.calls);
        assertThrows(PolicyException.class, () -> subject.run(
                policy(doc("\"inventory\":{\"batch-size\":1,\"batch-index\":2},", "")), plain(), ctx()));
    }

    @Test
    void workerSourceWithoutDispatchDeclarationIsFatalAndCatalogFailureIsIncomplete() {
        Fakes noDispatch = new Fakes(simple().repositories, simple().entries, List.of());
        assertThrows(PolicyException.class,
                () -> new OrchestratorPrecompute(noDispatch, noDispatch).run(policy(doc("", "")), plain(), ctx()));
        Fakes failing = new Fakes(simple().repositories, simple().entries, List.of("dependabot-planner")) {
            @Override
            public List<WorkflowEntry> list() {
                throw new InventoryUnavailableException("workflow-credential-unavailable", "unavailable");
            }
        };
        OrchestratorPrecompute.Outcome outcome =
                new OrchestratorPrecompute(failing, failing).run(policy(doc("", "")), plain(), ctx());
        assertEquals("workflow-credential-unavailable", outcome.record().get("reason").asText());
        assertFalse(outcome.activationReady());
    }

    @Test
    void sourcePathIsBoundToTheCompiledWorkflowInTheControlRepository() {
        assertEquals(".github/workflows/dependabot.md", OrchestratorPrecompute.sourcePath("acme/control", REF));
        assertEquals(".github/workflows/x.md", OrchestratorPrecompute.sourcePath("acme/control",
                ".github/workflows/x.yml@main"));
        for (String bad : new String[] {"", "acme/control/.github/../x.lock.yml@main",
            "acme/control/src/x.lock.yml@main", "acme/control/.github/workflows/x.md@main"}) {
            assertThrows(PolicyException.class, () -> OrchestratorPrecompute.sourcePath("acme/control", bad), bad);
        }
    }

    @Test
    void portsAreRequired() {
        Fakes fakes = simple();
        assertThrows(PolicyException.class, () -> new OrchestratorPrecompute(null, fakes));
        assertThrows(PolicyException.class, () -> new OrchestratorPrecompute(fakes, null));
    }
}
