package org.cao.backend.dashboard;

import java.util.List;
import java.util.Set;

/** Scope, time and filter checks shared by dashboard defaults and view data (§6, DLS-CTX-*). */
final class ContextChecks {
    private ContextChecks() { }

    static void context(Check check, YamlNode scope, YamlNode time, YamlNode filters, String path) {
        if (scope != null) {
            scope(check, scope, Check.child(path, "scope"));
        }
        if (time != null) {
            QueryValidator.time(time, Check.child(path, "time"), check);
        }
        if (filters != null) {
            filters(check, filters, Check.child(path, "filters"));
        }
    }

    private static void scope(Check check, YamlNode node, String path) {
        YamlNode.YamlMap m = check.map(node, path, "scope");
        if (m == null) {
            return;
        }
        check.keys(m, DashboardVocabulary.SCOPE_KEYS, path);
        for (var entry : m.entries().entrySet()) {
            String at = Check.child(path, entry.getKey());
            YamlNode.YamlSeq seq = check.seq(entry.getValue(), at, "scope level", true);
            if (seq != null) {
                for (int i = 0; i < seq.items().size(); i++) {
                    check.stringValue(seq.items().get(i), Check.index(at, i), "scope value");
                }
            }
        }
    }

    /** §6.3 and DLS-SEM-021; dimension names are checked for shape only because the dimension set is open. */
    private static void filters(Check check, YamlNode node, String path) {
        YamlNode.YamlMap m = check.map(node, path, "filters");
        if (m == null) {
            return;
        }
        for (var entry : m.entries().entrySet()) {
            String at = Check.child(path, entry.getKey());
            if (!DashboardVocabulary.IDENTIFIER.matcher(entry.getKey()).matches()) {
                check.error(Check.E005, "filter dimensions must be canonical kebab-case", at);
            }
            List<YamlNode> values;
            if (entry.getValue() instanceof YamlNode.YamlSeq seq) {
                if (seq.items().isEmpty()) {
                    check.error(Check.E010, "a filter sequence must be non-empty", at);
                }
                values = seq.items();
            } else {
                values = List.of(entry.getValue());
            }
            for (YamlNode v : values) {
                if (!Check.isScalarLiteral(v)) {
                    check.error(Check.E010, "filter values must be scalars", at);
                    break;
                }
                if (entry.getKey().equals("rollout-mode") && !(v instanceof YamlNode.YamlScalar s
                        && Set.of("review", "live", "unknown").contains(s.text()))) {
                    check.error(Check.E010, "rollout-mode must be review, live or unknown", at);
                }
            }
        }
    }
}
