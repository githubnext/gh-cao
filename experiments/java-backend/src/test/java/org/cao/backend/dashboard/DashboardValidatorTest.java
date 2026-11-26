package org.cao.backend.dashboard;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.stream.Stream;
import org.junit.jupiter.api.Test;

class DashboardValidatorTest {
    private static final String HEAD = "language-version: \"0.1.0\"\ndashboard:\n  id: demo\n  title: Demo\n";
    private static final String FAKE_TOKEN = "ghp" + "_" + "abcdefghijklmnopqrstuvwxyz0123456789";
    private static final String PAGE = "  pages:\n    - id: runs\n      kind: built-in\n      page: runs\n";

    private static ValidationResult check(String yaml) {
        return DashboardValidator.validate(yaml.getBytes(StandardCharsets.UTF_8));
    }

    private static void assertRejected(String yaml, String code) {
        ValidationResult r = check(yaml);
        assertFalse(r.ok(), () -> "expected " + code + " but accepted");
        assertTrue(r.hasCode(code), () -> "expected " + code + " in " + r.errors());
    }

    @Test
    void acceptsMinimalBuiltInDocument() {
        ValidationResult r = check(HEAD + PAGE);
        assertTrue(r.ok(), r.errors().toString());
    }

    @Test
    void acceptsSharedCorpusExamplesReadOnly() throws IOException {
        Path dir = Path.of("../../.github/skills/generate-dashboard-ir/corpus/examples");
        int seen = 0;
        try (Stream<Path> files = Files.list(dir)) {
            for (Path f : files.filter(p -> p.toString().endsWith(".dashboard.yml")).toList()) {
                ValidationResult r = DashboardValidator.validate(Files.readAllBytes(f));
                assertTrue(r.ok(), f.getFileName() + ": " + r.errors());
                seen++;
            }
        }
        assertEquals(4, seen);
    }

    @Test
    void rejectsYamlSafetyViolations() {
        assertRejected("language-version: \"0.1.0\"\ndashboard: {}\n---\nlanguage-version: \"0.1.0\"\n", "DLS-E002");
        assertRejected("- a\n- b\n", "DLS-E003");
        assertRejected("a: [unclosed\n", "DLS-E001");
        assertRejected(HEAD.replace("title: Demo", "title: &t Demo") + PAGE, "DLS-E008");
        assertRejected(HEAD.replace("title: Demo", "title: *t") + PAGE, "DLS-E008");
        assertRejected(HEAD.replace("title: Demo", "title: !!python/object Demo") + PAGE, "DLS-E008");
        assertRejected(HEAD + "  title: Again\n" + PAGE, "DLS-E004");
        assertRejected("", "DLS-E002");
    }

    @Test
    void rejectsRootAndVersionErrors() {
        assertRejected(HEAD.replace("\"0.1.0\"", "0.1.0") + PAGE, "DLS-E003");
        assertRejected(HEAD.replace("\"0.1.0\"", "\"0.2.0\"") + PAGE, "DLS-E005");
        assertRejected(HEAD + PAGE + "extra: 1\n", "DLS-E004");
        assertRejected(HEAD.replace("id: demo", "id: Demo_Id") + PAGE, "DLS-E005");
        assertRejected(HEAD + "  pages: []\n", "DLS-E003");
    }

    @Test
    void rejectsUnsafeLinksAndCredentials() {
        assertRejected(HEAD + "  github-url-base: http://github.com\n" + PAGE, "DLS-E009");
        assertRejected(HEAD + "  github-url-base: https://user:pw@github.com\n" + PAGE, "DLS-E009");
        assertRejected(HEAD.replace("title: Demo", "title: javascript:alert(1)") + PAGE, "DLS-E009");
        assertRejected(HEAD.replace("title: Demo", "title: " + FAKE_TOKEN) + PAGE, "DLS-E003");
    }

    @Test
    void errorsNeverEchoDocumentValues() {
        ValidationResult r = check(HEAD.replace("title: Demo", "title: " + FAKE_TOKEN) + PAGE);
        assertFalse(r.errors().toString().contains("abcdefghij"));
    }

    @Test
    void validatesPagesAndNavigation() {
        assertRejected(HEAD + PAGE + "    - id: runs\n      kind: built-in\n      page: usage\n", "DLS-E004");
        assertRejected(HEAD + "  pages:\n    - id: x\n      kind: built-in\n      page: nope\n", "DLS-E005");
        assertRejected(HEAD + PAGE + "  navigation:\n    - pages: [ghost]\n", "DLS-E006");
        assertRejected(HEAD + PAGE + "  navigation:\n    - placement: bottom\n      pages: [runs]\n", "DLS-E003");
        assertTrue(check(HEAD + PAGE + "  navigation:\n    - label: Main\n      pages: [runs]\n").ok());
    }

    private static final String VIEW = HEAD + "  pages:\n    - id: p\n      kind: custom\n      views:\n"
            + "        - id: v\n          data:\n            source: usage\n          mark: metric\n"
            + "          encoding:\n            value:\n              field: aic\n              aggregate: sum\n";

    @Test
    void validatesViews() {
        assertTrue(check(VIEW).ok(), check(VIEW).errors().toString());
        assertRejected(VIEW.replace("source: usage", "source: nowhere"), "DLS-E006");
        assertRejected(VIEW.replace("mark: metric", "mark: gauge"), "DLS-E005");
        assertRejected(VIEW.replace("field: aic", "field: operational-value"), "DLS-E010");
        assertRejected(VIEW.replace("field: aic", "field: value\n              unit: ghost"), "DLS-E006");
        assertRejected(VIEW + "          join: runs\n", "DLS-E008");
        assertRejected(VIEW + "          expression: a * b\n", "DLS-E008");
        assertRejected(VIEW.replace("mark: metric", "mark: table"), "DLS-E007");
        assertRejected(VIEW.replace("aggregate: sum", "aggregate: none\n              as: x"), "DLS-E010");
        assertRejected(VIEW.replace("aggregate: sum", "aggregate: median"), "DLS-E005");
    }

    @Test
    void enforcesDisclosureBoundAndUniqueViewIds() {
        StringBuilder many = new StringBuilder(HEAD + "  pages:\n    - id: p\n      kind: custom\n      views:\n");
        for (int i = 0; i < 5; i++) {
            many.append("        - id: v").append(i).append("\n          disclosure: essential\n")
                    .append("          data:\n            source: usage\n          mark: metric\n")
                    .append("          encoding:\n            value:\n              field: aic\n");
        }
        assertRejected(many.toString(), "DLS-E013");
        assertRejected(VIEW + "        - id: v\n          data:\n            source: usage\n          mark: metric\n"
                + "          encoding:\n            value:\n              field: aic\n", "DLS-E004");
    }

    private static final String QUERIES = HEAD + "  queries:\n    - name: totals\n      subject: Totals.\n"
            + "      from: usage\n      aggregate:\n        by: [repository]\n        values:\n"
            + "          - { field: aic, as: aic, reducer: sum }\n" + PAGE.replace("built-in\n      page: runs",
            "custom\n      views:\n        - id: v\n          data:\n            source: totals\n"
            + "          mark: metric\n          encoding:\n            value:\n              field: aic\n");

    @Test
    void validatesQueries() {
        assertTrue(check(QUERIES).ok(), check(QUERIES).errors().toString());
        assertRejected(QUERIES.replace("name: totals", "name: usage").replace("source: totals", "source: usage"), "DLS-E005");
        assertRejected(QUERIES.replace("      subject: Totals.\n", ""), "DLS-E003");
        assertRejected(QUERIES.replace("from: usage", "from: later"), "DLS-E006");
        assertRejected(QUERIES.replace("from: usage", "from: totals"), "DLS-E006");
        assertRejected(QUERIES.replace("reducer: sum", "reducer: avg"), "DLS-E005");
        assertRejected(QUERIES.replace("      from: usage\n", "      from: usage\n      limit: 100001\n"), "DLS-E010");
        assertRejected(QUERIES.replace("      from: usage\n", "      from: usage\n      sql: select 1\n"), "DLS-E004");
        assertRejected(QUERIES.replace("source: totals", "source: runs"), "DLS-E015");
    }

    @Test
    void validatesComputeJoinsAndTime() {
        String base = HEAD + "  queries:\n    - name: q\n      subject: S.\n      from: usage\n";
        String view = PAGE.replace("built-in\n      page: runs", "custom\n      views:\n        - id: v\n"
                + "          data:\n            source: q\n          mark: metric\n          encoding:\n"
                + "            value:\n              field: aic\n");
        String compute = "      compute:\n        - as: c\n          function: %s\n          args:\n%s";
        assertTrue(check(base + String.format(compute, "coalesce", "            - field: a\n            - value: 0\n") + view).ok());
        assertRejected(base + String.format(compute, "eval", "            - field: a\n") + view, "DLS-E008");
        assertRejected(base + String.format(compute, "coalesce", "            - field: a\n") + view, "DLS-E003");
        assertRejected(base + String.format(compute, "lower", "            - { field: a, value: 1 }\n") + view, "DLS-E003");
        assertRejected(base + "      time: { range: 30d, start: \"2026-01-01T00:00:00Z\" }\n" + view, "DLS-E010");
        assertRejected(base + "      time: { start: \"2026-02-01T00:00:00Z\", end: \"2026-01-01T00:00:00Z\" }\n" + view, "DLS-E010");
        assertRejected(base + "      time: { range: 0d }\n" + view, "DLS-E010");
        assertRejected(base + "      joins:\n        - { source: runs, on: [], fields: [] }\n" + view, "DLS-E003");
    }

    @Test
    void doesNotClaimConformance() {
        assertTrue(check(VIEW).ok());
        assertFalse(DashboardValidator.notImplemented().isEmpty());
    }
}
