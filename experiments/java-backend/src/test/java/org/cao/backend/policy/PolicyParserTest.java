package org.cao.backend.policy;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.nio.charset.StandardCharsets;
import org.junit.jupiter.api.Test;

class PolicyParserTest {
    static byte[] bytes(String json) {
        return json.getBytes(StandardCharsets.UTF_8);
    }

    // T-CFG-001: spec Appendix A.1
    static final String MINIMAL = """
            {"version":1,"control-plane":{"scope":{"allowed-repositories":["acme/payments-api"]},
            "campaigns":{"dependabot":{"workers":{"update-planner":{"workflow":"dependabot-update-planner"}}}}}}
            """;

    @Test
    void acceptsMinimalReviewPolicyAndRecordsDigest() {
        ControlPolicy policy = PolicyParser.parse(bytes(MINIMAL));
        assertEquals(64, policy.digest().length());
        assertEquals(java.util.List.of("acme/payments-api"), policy.scope().repositories());
        assertEquals("dependabot-update-planner",
                policy.campaigns().get("dependabot").workers().get("update-planner").workflow());
    }

    @Test
    void rejectsUnknownPropertiesAndDuplicateKeys() {
        assertThrows(PolicyException.class, () -> PolicyParser.parse(bytes("{\"version\":1,\"control-plane\":{},\"x\":1}")));
        assertThrows(PolicyException.class, () -> PolicyParser.parse(bytes(
                "{\"version\":1,\"control-plane\":{\"defaults\":{\"mode\":\"review\",\"mode\":\"live\"}}}")));
        assertThrows(PolicyException.class, () -> PolicyParser.parse(bytes(
                "{\"version\":1,\"control-plane\":{\"campaigns\":{\"a\":{\"surprise\":true}}}}")));
    }

    @Test
    void rejectsExpressionsWildcardsAndBadIdentifiers() {
        assertThrows(PolicyException.class, () -> PolicyParser.parse(bytes(
                "{\"version\":1,\"control-plane\":{\"scope\":{\"allowed-repositories\":[\"${{ vars.X }}/a\"]}}}")));
        assertThrows(PolicyException.class, () -> PolicyParser.parse(bytes(
                "{\"version\":1,\"control-plane\":{\"scope\":{\"allowed-owners\":[\"*\"]}}}")));
        assertThrows(PolicyException.class, () -> PolicyParser.parse(bytes(
                "{\"version\":1,\"control-plane\":{\"campaigns\":{\"Bad_Name\":{}}}}")));
        assertThrows(PolicyException.class, () -> PolicyParser.parse(bytes(
                "{\"version\":1,\"control-plane\":{\"campaigns\":{\"c\":{\"workers\":{\"w\":{\"workflow\":\"Upper\"}}}}}}")));
    }

    @Test
    void rejectsExecutionFieldsFromAppendixA2() {
        assertThrows(PolicyException.class, () -> PolicyParser.parse(bytes(
                "{\"version\":1,\"control-plane\":{\"engine\":\"copilot\",\"max-ai-credits\":500,"
                        + "\"permissions\":{\"contents\":\"write\"},\"safe-outputs\":{\"create-pull-request\":{}}}}")));
    }

    @Test
    void rejectsDebugInEveryPersistentModeField() {
        String[] documents = {
            "{\"version\":1,\"control-plane\":{\"defaults\":{\"mode\":\"debug\"}}}",
            "{\"version\":1,\"control-plane\":{\"campaigns\":{\"c\":{\"mode\":\"debug\"}}}}",
            "{\"version\":1,\"control-plane\":{\"campaigns\":{\"c\":{\"targets\":{\"a/b\":{\"mode\":\"debug\"}}}}}}",
            "{\"version\":1,\"control-plane\":{\"campaigns\":{\"c\":{\"workers\":{\"w\":{\"workflow\":\"w\",\"max-mode\":\"debug\"}}}}}}",
        };
        for (String document : documents) {
            assertThrows(PolicyException.class, () -> PolicyParser.parse(bytes(document)), document);
        }
    }

    @Test
    void rejectsBadVersionBoundsAndUnsupportedSections() {
        assertThrows(PolicyException.class, () -> PolicyParser.parse(bytes("{\"control-plane\":{}}")));
        assertThrows(PolicyException.class, () -> PolicyParser.parse(bytes("{\"version\":2,\"control-plane\":{}}")));
        assertThrows(PolicyException.class, () -> PolicyParser.parse(bytes("{\"version\":1}")));
        assertThrows(PolicyException.class, () -> PolicyParser.parse(bytes(
                "{\"version\":1,\"control-plane\":{\"defaults\":{\"max-repositories\":1001}}}")));
        assertThrows(PolicyException.class, () -> PolicyParser.parse(bytes(
                "{\"version\":1,\"control-plane\":{\"defaults\":{\"rollout-percent\":0}}}")));
        assertThrows(PolicyException.class, () -> PolicyParser.parse(bytes(
                "{\"version\":1,\"control-plane\":{\"web\":{}}}")));
        assertThrows(PolicyException.class, () -> PolicyParser.parse(new byte[] {(byte) 0xff, '{'}));
        assertThrows(PolicyException.class, () -> PolicyParser.parse(bytes("{} {}")));
    }

    @Test
    void duplicateWorkflowIdentitiesInOneCampaignAreRejected() {
        assertThrows(PolicyException.class, () -> PolicyParser.parse(bytes(
                "{\"version\":1,\"control-plane\":{\"campaigns\":{\"c\":{\"workers\":"
                        + "{\"a\":{\"workflow\":\"w\"},\"b\":{\"workflow\":\"w\"}}}}}}")));
    }
}
