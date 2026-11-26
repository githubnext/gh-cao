package org.cao.backend.dashboard;

import java.net.URI;
import java.net.URISyntaxException;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.OptionalDouble;
import java.util.Set;
import java.util.regex.Pattern;

/**
 * Validator for Dashboard Language 0.1.0 documents (docs/dashboard-language-specification.md).
 *
 * <p>Implements the structural subset listed in {@code implementation-status.md}. Anything it does not
 * validate is reported through {@link ValidationResult#limitations()}; a result with {@code ok == true}
 * is therefore not a conformance claim (DLS-CONF-001/005).
 */
public final class DashboardValidator {
    private static final Pattern SECRET = Pattern.compile(
            "gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|-----BEGIN [A-Z ]*PRIVATE KEY-----");
    private static final Pattern URI_LIKE = Pattern.compile("^(?:[A-Za-z][A-Za-z0-9+.-]*://\\S+|javascript:.*)$",
            Pattern.CASE_INSENSITIVE);

    private final Check check = new Check();
    private final Set<String> limitations = new LinkedHashSet<>();
    private final QueryValidator queries = new QueryValidator(check, this::limit);
    private final Set<String> pageIds = new LinkedHashSet<>();
    private final Set<String> units = new HashSet<>();
    private final Set<String> referencedSources = new HashSet<>();
    private final Set<String> uncheckedStrings = new HashSet<>();

    private DashboardValidator() { }

    public static ValidationResult validate(byte[] document) {
        DashboardValidator v = new DashboardValidator();
        return v.run(document);
    }

    private ValidationResult run(byte[] document) {
        SafeYaml.Parsed parsed;
        try {
            parsed = SafeYaml.parse(document);
        } catch (SafeYaml.YamlFailure e) {
            check.error(e.code(), e.getMessage(), e.path());
            return result();
        }
        YamlNode.YamlMap root = check.map(parsed.root(), "$", "root");
        check.errors().addAll(parsed.duplicateKeys());
        if (root == null) {
            return result();
        }
        scanSafety(parsed.root(), "$");
        validateRoot(root);
        return result();
    }

    private ValidationResult result() {
        return new ValidationResult(check.errors().isEmpty(), check.errors(), new ArrayList<>(limitations));
    }

    /** DLS-SAFE-004 and DLS-SAFE-005: no credentials or unsafe link schemes anywhere in the document. */
    private void scanSafety(YamlNode node, String path) {
        if (node instanceof YamlNode.YamlScalar s) {
            if (SECRET.matcher(s.text()).find()) {
                check.error(Check.E003, "document must not contain credentials or private keys", path);
            }
            if (s.isString() && URI_LIKE.matcher(s.text()).matches() && !safeLink(s.text())) {
                check.error(Check.E009, "links must be https URLs without credentials", path);
            }
        } else if (node instanceof YamlNode.YamlMap m) {
            m.entries().forEach((k, v) -> scanSafety(v, Check.child(path, k)));
        } else if (node instanceof YamlNode.YamlSeq q) {
            for (int i = 0; i < q.items().size(); i++) {
                scanSafety(q.items().get(i), Check.index(path, i));
            }
        }
    }

    static boolean safeLink(String text) {
        try {
            URI uri = new URI(text);
            return "https".equalsIgnoreCase(uri.getScheme()) && uri.getRawUserInfo() == null && uri.getHost() != null;
        } catch (URISyntaxException e) {
            return false;
        }
    }

    private void validateRoot(YamlNode.YamlMap root) {
        check.keys(root, DashboardVocabulary.ROOT_KEYS, "$");
        for (String required : DashboardVocabulary.ROOT_KEYS) {
            if (root.get(required) == null) {
                check.error(Check.E003, "missing required field", Check.child("$", required));
            }
        }
        YamlNode version = root.get("language-version");
        if (version != null) {
            if (!(version instanceof YamlNode.YamlScalar s) || !s.isString() || !s.quoted()) {
                check.error(Check.E003, "language-version must be the quoted string \"0.1.0\"", "$.language-version");
            } else if (!s.text().equals(DashboardVocabulary.LANGUAGE_VERSION)) {
                check.error(Check.E005, "language-version must use the exact canonical value", "$.language-version");
            }
        }
        YamlNode.YamlMap dashboard = root.get("dashboard") == null ? null
                : check.map(root.get("dashboard"), "$.dashboard", "dashboard");
        if (dashboard != null) {
            validateDashboard(dashboard);
        }
    }

    private void validateDashboard(YamlNode.YamlMap d) {
        String path = "$.dashboard";
        check.keys(d, DashboardVocabulary.DASHBOARD_KEYS, path);
        check.identifier(d, "id", path, true);
        check.string(d, "title", path, true);
        check.string(d, "description", path, false);
        githubUrlBase(d, path);
        repository(d, path);
        if (d.get("units") != null) {
            units(d.get("units"), Check.child(path, "units"));
        }
        if (d.get("defaults") != null) {
            defaults(d.get("defaults"), Check.child(path, "defaults"));
        }
        if (d.get("queries") != null) {
            queries.validate(d.get("queries"), Check.child(path, "queries"));
        }
        for (String key : List.of("card-templates", "cli-actions", "views")) {
            if (d.get(key) != null) {
                unchecked("dashboard." + key, d.get(key));
            }
        }
        pageIds(d, path);
        pages(d, path);
        if (d.get("horizon") != null) {
            horizon(d.get("horizon"), Check.child(path, "horizon"));
        }
        if (d.get("callouts") != null) {
            callouts(d.get("callouts"), Check.child(path, "callouts"));
        }
        if (d.get("navigation") != null) {
            navigation(d.get("navigation"), Check.child(path, "navigation"));
        }
        if (d.get("data-status") != null) {
            dataStatus(d.get("data-status"), Check.child(path, "data-status"));
        }
        unusedQueries();
    }

    /** DLS-DOC-011. */
    private void githubUrlBase(YamlNode.YamlMap d, String path) {
        String base = check.string(d, "github-url-base", path, false);
        if (base == null) {
            return;
        }
        boolean ok;
        try {
            URI uri = new URI(base);
            ok = "https".equalsIgnoreCase(uri.getScheme()) && uri.getHost() != null && uri.getRawUserInfo() == null
                    && uri.getRawQuery() == null && uri.getRawFragment() == null;
        } catch (URISyntaxException e) {
            ok = false;
        }
        if (!ok) {
            check.error(Check.E009, "github-url-base must be an absolute HTTPS URL without credentials, query or fragment",
                    Check.child(path, "github-url-base"));
        }
    }

    /** DLS-DOC-012. */
    private void repository(YamlNode.YamlMap d, String path) {
        String repo = check.string(d, "repository", path, false);
        if (repo != null && !DashboardVocabulary.SLUG.matcher(repo).matches()) {
            check.error(Check.E005, "repository must be an owner/repo slug", Check.child(path, "repository"));
        }
    }

    /** DLS-DOC-013, DLS-UNIT-004, DLS-UNIT-005. */
    private void units(YamlNode node, String path) {
        YamlNode.YamlMap map = check.map(node, path, "units");
        if (map == null) {
            return;
        }
        if (map.entries().isEmpty()) {
            check.error(Check.E003, "units must be a non-empty mapping", path);
        }
        for (var entry : map.entries().entrySet()) {
            String id = entry.getKey();
            String at = Check.child(path, id);
            if (!DashboardVocabulary.IDENTIFIER.matcher(id).matches()) {
                check.error(Check.E005, "unit identifiers must be canonical kebab-case", at);
            }
            units.add(id);
            YamlNode.YamlMap u = check.map(entry.getValue(), at, "unit");
            if (u == null) {
                continue;
            }
            check.keys(u, DashboardVocabulary.UNIT_KEYS, at);
            String name = check.string(u, "name", at, true);
            String symbol = check.string(u, "symbol", at, true);
            OptionalDouble significant = u.get("significant") instanceof YamlNode.YamlScalar s
                    ? s.asFiniteDouble() : OptionalDouble.empty();
            if (u.get("significant") == null) {
                check.error(Check.E003, "missing required field", Check.child(at, "significant"));
            } else if (significant.isEmpty() || significant.getAsDouble() <= 0) {
                check.error(Check.E003, "significant must be a finite positive number", Check.child(at, "significant"));
            }
            String format = u.get("format") == null ? null
                    : check.oneOf(u, "format", DashboardVocabulary.UNIT_FORMATS, at, false);
            if ("aicc".equals(format) && (!"AICc($)".equals(name)
                    || significant.isEmpty() || significant.getAsDouble() != 2.0)) {
                check.error(Check.E003, "an aicc unit must declare name AICc($) and significant 2", at);
            }
            if ("usd".equals(format) && (!"USD".equals(symbol)
                    || significant.isEmpty() || significant.getAsDouble() != 0.001)) {
                check.error(Check.E003, "a usd unit must declare symbol USD and significant 0.001", at);
            }
        }
    }

    /** DLS-DOC-008, §6.1-6.3. */
    private void defaults(YamlNode node, String path) {
        YamlNode.YamlMap d = check.map(node, path, "defaults");
        if (d == null) {
            return;
        }
        check.keys(d, DashboardVocabulary.DEFAULTS_KEYS, path);
        ContextChecks.context(check, d.get("scope"), d.get("time"), d.get("filters"), path);
    }

    private void pageIds(YamlNode.YamlMap d, String path) {
        if (!(d.get("pages") instanceof YamlNode.YamlSeq seq)) {
            return;
        }
        for (YamlNode item : seq.items()) {
            if (item instanceof YamlNode.YamlMap m && m.get("id") instanceof YamlNode.YamlScalar s && s.isString()) {
                pageIds.add(s.text());
            }
        }
    }

    private void pages(YamlNode.YamlMap d, String path) {
        String at = Check.child(path, "pages");
        YamlNode.YamlSeq pages = check.seq(d.get("pages"), at, "pages", true);
        if (pages == null) {
            if (d.get("pages") == null) {
                check.error(Check.E003, "missing required field", at);
            }
            return;
        }
        Set<String> seen = new HashSet<>();
        for (int i = 0; i < pages.items().size(); i++) {
            new PageValidator(check, queries, units, referencedSources, this::unchecked)
                    .validate(pages.items().get(i), Check.index(at, i), seen);
        }
    }

    /** DLS-DOC-014. */
    private void horizon(YamlNode node, String path) {
        YamlNode.YamlMap h = check.map(node, path, "horizon");
        if (h == null) {
            return;
        }
        check.keys(h, DashboardVocabulary.HORIZON_KEYS, path);
        check.string(h, "label", path, true);
        if (h.get("tooltip") == null) {
            check.error(Check.E003, "missing required field", Check.child(path, "tooltip"));
        } else {
            PageValidator.tooltip(check, h.get("tooltip"), Check.child(path, "tooltip"));
        }
    }

    /** DLS-DOC-015. */
    private void callouts(YamlNode node, String path) {
        YamlNode.YamlSeq seq = check.seq(node, path, "callouts", true);
        if (seq == null) {
            return;
        }
        Set<String> ids = new HashSet<>();
        for (int i = 0; i < seq.items().size(); i++) {
            String at = Check.index(path, i);
            YamlNode.YamlMap c = check.map(seq.items().get(i), at, "callout");
            if (c == null) {
                continue;
            }
            check.keys(c, DashboardVocabulary.CALLOUT_KEYS, at);
            String id = check.identifier(c, "id", at, true);
            if (id != null && !ids.add(id)) {
                check.error(Check.E004, "callout ids must be unique", Check.child(at, "id"));
            }
            check.string(c, "title", at, true);
            check.string(c, "description", at, true);
            check.string(c, "icon", at, false);
            String page = check.string(c, "navigation-page", at, false);
            if (page != null && !pageIds.contains(page)) {
                check.error(Check.E006, "navigation-page must reference a declared page", Check.child(at, "navigation-page"));
            }
            if (c.get("visible-when") != null) {
                visibleWhen(c.get("visible-when"), Check.child(at, "visible-when"));
            }
        }
    }

    private void visibleWhen(YamlNode node, String path) {
        YamlNode.YamlMap v = check.map(node, path, "visible-when");
        if (v == null) {
            return;
        }
        check.keys(v, DashboardVocabulary.VISIBLE_WHEN_KEYS, path);
        String source = check.string(v, "source", path, true);
        if (source != null) {
            sourceReference(source, Check.child(path, "source"));
        }
        check.string(v, "field", path, true);
        boolean equals = v.get("equals") != null;
        boolean older = v.get("older-than-hours") != null;
        if (equals == older) {
            check.error(Check.E003, "visible-when must declare exactly one of equals or older-than-hours", path);
        } else if (equals && !Check.isScalarLiteral(v.get("equals"))) {
            check.error(Check.E003, "equals must be a scalar", Check.child(path, "equals"));
        } else if (older) {
            OptionalDouble hours = v.get("older-than-hours") instanceof YamlNode.YamlScalar s
                    ? s.asFiniteDouble() : OptionalDouble.empty();
            if (hours.isEmpty() || hours.getAsDouble() <= 0) {
                check.error(Check.E003, "older-than-hours must be a positive finite number",
                        Check.child(path, "older-than-hours"));
            }
        }
    }

    /** §8 data-status: bindings must name declared queries. */
    private void dataStatus(YamlNode node, String path) {
        YamlNode.YamlMap m = check.map(node, path, "data-status");
        if (m == null) {
            return;
        }
        check.keys(m, DashboardVocabulary.DATA_STATUS_KEYS, path);
        for (String key : DashboardVocabulary.DATA_STATUS_KEYS) {
            String source = check.string(m, key, path, key.equals("freshness-source"));
            if (source != null) {
                sourceReference(source, Check.child(path, key));
            }
        }
    }

    private void sourceReference(String source, String path) {
        if (DashboardVocabulary.SOURCES.contains(source) || queries.names().contains(source)) {
            referencedSources.add(source);
        } else {
            check.error(Check.E006, "unknown source", path);
        }
    }

    /** DLS-DOC-016. */
    private void navigation(YamlNode node, String path) {
        YamlNode.YamlSeq sections = check.seq(node, path, "navigation", true);
        if (sections == null) {
            return;
        }
        for (int i = 0; i < sections.items().size(); i++) {
            String at = Check.index(path, i);
            YamlNode.YamlMap s = check.map(sections.items().get(i), at, "navigation section");
            if (s == null) {
                continue;
            }
            check.keys(s, DashboardVocabulary.NAVIGATION_SECTION_KEYS, at);
            String label = check.string(s, "label", at, false);
            check.bool(s, "experimental", at);
            if (s.get("placement") != null) {
                String placement = check.oneOf(s, "placement", Set.of("bottom"), at, false);
                if (placement != null && label == null) {
                    check.error(Check.E003, "placement requires a non-empty label", Check.child(at, "label"));
                }
            }
            YamlNode.YamlSeq pages = check.seq(s.get("pages"), Check.child(at, "pages"), "pages", true);
            if (pages != null) {
                for (int k = 0; k < pages.items().size(); k++) {
                    String id = check.stringValue(pages.items().get(k), Check.index(Check.child(at, "pages"), k), "page id");
                    if (id != null && !pageIds.contains(id)) {
                        check.error(Check.E006, "navigation must reference declared pages", Check.index(Check.child(at, "pages"), k));
                    }
                }
            }
        }
    }

    /**
     * DLS-VAL-006: reject a declared query unreachable from rendered views, callouts, data-status bindings
     * or another reachable query. References inside unchecked members are collected conservatively, so this
     * check can miss an unused query but never flags one that an unchecked member names.
     */
    private void unusedQueries() {
        Set<String> reachable = new HashSet<>(referencedSources);
        reachable.addAll(uncheckedStrings);
        List<String> pending = new ArrayList<>(reachable);
        while (!pending.isEmpty()) {
            String name = pending.remove(pending.size() - 1);
            for (String input : queries.inputs().getOrDefault(name, List.of())) {
                if (reachable.add(input)) {
                    pending.add(input);
                }
            }
        }
        for (String name : queries.names()) {
            if (!reachable.contains(name)) {
                check.error(Check.E015, "declared query is not used by rendered content or another retained query",
                        "$.dashboard.queries");
            }
        }
    }

    private void limit(String message) {
        limitations.add(message);
    }

    private void unchecked(String member, YamlNode node) {
        limitations.add(member + " is accepted without validation");
        collectStrings(node);
    }

    private void collectStrings(YamlNode node) {
        if (node instanceof YamlNode.YamlScalar s) {
            uncheckedStrings.add(s.text());
        } else if (node instanceof YamlNode.YamlMap m) {
            m.entries().values().forEach(this::collectStrings);
        } else if (node instanceof YamlNode.YamlSeq q) {
            q.items().forEach(this::collectStrings);
        }
    }

    /** Spec areas never validated here, independent of document content. */
    public static List<String> notImplemented() {
        return List.of(
                "field references are not checked against source schemas (DLS-QUERY-012/019, DLS-VIEW-007, DLS-E010/E011)",
                "Octicon names are not checked against a presenter icon set",
                "link-field compatibility (DLS-E009 for href fields) is not checked",
                "chart layering, facets, treemap/scatter options, route-bound pages and element config are not validated",
                "temporal-series, window and statistical query clauses are accepted without clause-level validation",
                "no data is read and nothing is executed: this is not a presenter and makes no DLS-DATA claim");
    }
}
