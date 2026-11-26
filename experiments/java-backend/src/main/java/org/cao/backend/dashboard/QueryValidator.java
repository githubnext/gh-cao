package org.cao.backend.dashboard;

import java.time.OffsetDateTime;
import java.time.format.DateTimeParseException;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.function.Consumer;

/**
 * Structural validation of {@code dashboard.queries} (DLS-QUERY-001..011, 021, 024..027, 032).
 *
 * <p>Field references are not checked against source schemas, so DLS-QUERY-012/019 (DLS-E010/E011 on a
 * field a clause does not produce) remain unimplemented; see {@link DashboardValidator#notImplemented}.
 */
final class QueryValidator {
    private final Check check;
    private final Consumer<String> limitations;
    private final Map<String, List<String>> inputs = new LinkedHashMap<>();
    private final Set<String> declared = new LinkedHashSet<>();

    QueryValidator(Check check, Consumer<String> limitations) {
        this.check = check;
        this.limitations = limitations;
    }

    /** Names of declared queries in declaration order. */
    Set<String> names() {
        return declared;
    }

    /** Query name to the sources it reads (from, union, joins), for reachability (DLS-VAL-006). */
    Map<String, List<String>> inputs() {
        return inputs;
    }

    void validate(YamlNode node, String path) {
        YamlNode.YamlSeq queries = check.seq(node, path, "queries", true);
        if (queries == null) {
            return;
        }
        Set<String> allNames = new HashSet<>();
        for (YamlNode item : queries.items()) {
            if (item instanceof YamlNode.YamlMap m && m.get("name") instanceof YamlNode.YamlScalar s && s.isString()) {
                allNames.add(s.text());
            }
        }
        for (int i = 0; i < queries.items().size(); i++) {
            validateQuery(queries.items().get(i), Check.index(path, i), allNames);
        }
    }

    private void validateQuery(YamlNode node, String path, Set<String> allNames) {
        YamlNode.YamlMap q = check.map(node, path, "query");
        if (q == null) {
            return;
        }
        check.keys(q, DashboardVocabulary.QUERY_KEYS, path);
        String name = check.identifier(q, "name", path, true);
        if (name != null) {
            if (DashboardVocabulary.SOURCES.contains(name)) {
                check.error(Check.E005, "query name must not shadow a database table or runtime source",
                        Check.child(path, "name"));
            } else if (declared.contains(name)) {
                check.error(Check.E004, "query name must be unique", Check.child(path, "name"));
            }
        }
        check.string(q, "subject", path, true);
        check.string(q, "objective", path, false);
        check.string(q, "acceptance", path, false);
        check.string(q, "description", path, false);
        check.metadataBound(q, path);

        Set<String> params = parameters(q.get("parameters"), Check.child(path, "parameters"));
        List<String> read = new ArrayList<>();
        reference(q.get("from"), Check.child(path, "from"), name, allNames, read, true);
        if (q.get("union") != null) {
            YamlNode.YamlSeq union = check.seq(q.get("union"), Check.child(path, "union"), "union", true);
            if (union != null) {
                for (int i = 0; i < union.items().size(); i++) {
                    reference(union.items().get(i), Check.index(Check.child(path, "union"), i), name, allNames, read, false);
                }
            }
        }
        Set<String> outputs = new HashSet<>();
        joins(q.get("joins"), Check.child(path, "joins"), name, allNames, read, outputs);
        if (q.get("filter") != null) {
            filter(q.get("filter"), Check.child(path, "filter"), params);
        }
        compute(q.get("compute"), Check.child(path, "compute"), params, outputs);
        predict(q.get("predict"), Check.child(path, "predict"), outputs);
        aggregate(q.get("aggregate"), Check.child(path, "aggregate"));
        select(q.get("select"), Check.child(path, "select"));
        orderBy(q.get("order-by"), Check.child(path, "order-by"));
        if (q.get("limit") != null) {
            Long limit = check.positiveInt(q.get("limit"), Check.child(path, "limit"), "limit", Check.E010);
            if (limit != null && limit > DashboardVocabulary.MAX_QUERY_LIMIT) {
                check.error(Check.E010, "limit must not exceed 100000", Check.child(path, "limit"));
            }
        }
        for (String key : List.of("temporal-series", "window", "facet")) {
            if (q.get(key) != null) {
                limitations.accept("query clause " + key + " is accepted without clause-level validation");
            }
        }
        if (q.get("time") != null) {
            time(q.get("time"), Check.child(path, "time"), check);
        }
        if (q.get("page") != null) {
            page(q, Check.child(path, "page"));
        }
        if (name != null && !DashboardVocabulary.SOURCES.contains(name) && declared.add(name)) {
            inputs.put(name, read);
        }
    }

    private Set<String> parameters(YamlNode node, String path) {
        Set<String> names = new HashSet<>();
        if (node == null) {
            return names;
        }
        YamlNode.YamlSeq seq = check.seq(node, path, "parameters", true);
        if (seq == null) {
            return names;
        }
        for (int i = 0; i < seq.items().size(); i++) {
            String at = Check.index(path, i);
            YamlNode.YamlMap p = check.map(seq.items().get(i), at, "parameter");
            if (p == null) {
                continue;
            }
            check.keys(p, Set.of("name", "type"), at);
            String pn = check.identifier(p, "name", at, true);
            check.oneOf(p, "type", DashboardVocabulary.PARAMETER_TYPES, at, true);
            if (pn != null && !names.add(pn)) {
                check.error(Check.E004, "parameter name must be unique", Check.child(at, "name"));
            }
        }
        return names;
    }

    /** DLS-QUERY-003: only tables, runtime sources, or earlier queries; forward and self references fail. */
    private void reference(YamlNode node, String path, String self, Set<String> allNames, List<String> read,
            boolean required) {
        if (node == null) {
            if (required) {
                check.error(Check.E003, "missing required field", path);
            }
            return;
        }
        String ref = check.stringValue(node, path, "source");
        if (ref == null) {
            return;
        }
        if (ref.equals(self) && allNames.contains(ref)) {
            check.error(Check.E006, "query must not read itself", path);
        } else if (DashboardVocabulary.SOURCES.contains(ref) || declared.contains(ref)) {
            read.add(ref);
        } else if (allNames.contains(ref)) {
            check.error(Check.E006, "query reads a query declared later", path);
        } else {
            check.error(Check.E006, "unknown source", path);
        }
    }

    private void joins(YamlNode node, String path, String self, Set<String> allNames, List<String> read,
            Set<String> outputs) {
        if (node == null) {
            return;
        }
        YamlNode.YamlSeq joins = check.seq(node, path, "joins", true);
        if (joins == null) {
            return;
        }
        if (joins.items().size() > DashboardVocabulary.MAX_JOINS) {
            check.error(Check.E003, "a query must not declare more than four joins", path);
        }
        for (int i = 0; i < joins.items().size(); i++) {
            String at = Check.index(path, i);
            YamlNode.YamlMap j = check.map(joins.items().get(i), at, "join");
            if (j == null) {
                continue;
            }
            check.keys(j, Set.of("source", "type", "on", "fields"), at);
            reference(j.get("source"), Check.child(at, "source"), self, allNames, read, true);
            if (j.get("type") != null) {
                check.oneOf(j, "type", DashboardVocabulary.JOIN_TYPES, at, false);
            }
            YamlNode.YamlSeq on = check.seq(j.get("on"), Check.child(at, "on"), "on", true);
            if (on != null) {
                for (int k = 0; k < on.items().size(); k++) {
                    String onAt = Check.index(Check.child(at, "on"), k);
                    YamlNode.YamlMap pair = check.map(on.items().get(k), onAt, "join key");
                    if (pair != null) {
                        check.keys(pair, Set.of("left", "right"), onAt);
                        check.string(pair, "left", onAt, true);
                        check.string(pair, "right", onAt, true);
                    }
                }
            }
            YamlNode.YamlSeq fields = check.seq(j.get("fields"), Check.child(at, "fields"), "fields", true);
            if (fields != null) {
                for (int k = 0; k < fields.items().size(); k++) {
                    String fAt = Check.index(Check.child(at, "fields"), k);
                    YamlNode.YamlMap f = check.map(fields.items().get(k), fAt, "join field");
                    if (f != null) {
                        check.keys(f, Set.of("field", "as"), fAt);
                        check.string(f, "field", fAt, true);
                        output(check.string(f, "as", fAt, true), Check.child(fAt, "as"), outputs);
                    }
                }
            }
        }
    }

    /** DLS-QUERY-009: output names must be unique. */
    private void output(String name, String path, Set<String> outputs) {
        if (name != null && !outputs.add(name)) {
            check.error(Check.E005, "output names must be unique within a query", path);
        }
    }

    private void filter(YamlNode node, String path, Set<String> params) {
        YamlNode.YamlMap f = check.map(node, path, "filter");
        if (f == null) {
            return;
        }
        check.keys(f, Set.of("predicates"), path);
        YamlNode.YamlSeq preds = check.seq(f.get("predicates"), Check.child(path, "predicates"), "predicates", true);
        if (preds == null) {
            return;
        }
        for (int i = 0; i < preds.items().size(); i++) {
            String at = Check.index(Check.child(path, "predicates"), i);
            YamlNode.YamlMap p = check.map(preds.items().get(i), at, "predicate");
            if (p == null) {
                continue;
            }
            check.keys(p, DashboardVocabulary.PREDICATE_KEYS, at);
            check.string(p, "field", at, true);
            int operators = 0;
            for (String op : List.of("equals", "in", "includes", "gte", "lt")) {
                YamlNode operand = p.get(op);
                if (operand == null) {
                    continue;
                }
                operators++;
                String opAt = Check.child(at, op);
                if (op.equals("in")) {
                    YamlNode.YamlSeq values = check.seq(operand, opAt, "in", true);
                    if (values != null) {
                        for (YamlNode v : values.items()) {
                            if (!Check.isScalarLiteral(v)) {
                                check.error(Check.E003, "in values must be scalar literals", opAt);
                                break;
                            }
                        }
                    }
                } else {
                    operand(operand, opAt, params);
                }
            }
            if (operators != 1) {
                check.error(Check.E003, "predicate must declare exactly one operator", at);
            }
        }
    }

    /** A scalar literal or an inert parameter reference naming a declared parameter (DLS-QUERY-027). */
    private void operand(YamlNode node, String path, Set<String> params) {
        if (node instanceof YamlNode.YamlMap m) {
            if (m.entries().size() == 1 && m.get("parameter") instanceof YamlNode.YamlScalar s && s.isString()) {
                if (!params.contains(s.text())) {
                    check.error(Check.E006, "parameter is not declared by this query", path);
                }
                return;
            }
            check.error(Check.E003, "operand must be a scalar or exactly one parameter reference", path);
            return;
        }
        if (!Check.isScalarLiteral(node)) {
            check.error(Check.E003, "operand must be a scalar literal", path);
        }
    }

    private void compute(YamlNode node, String path, Set<String> params, Set<String> outputs) {
        if (node == null) {
            return;
        }
        YamlNode.YamlSeq seq = check.seq(node, path, "compute", true);
        if (seq == null) {
            return;
        }
        for (int i = 0; i < seq.items().size(); i++) {
            String at = Check.index(path, i);
            YamlNode.YamlMap c = check.map(seq.items().get(i), at, "computed field");
            if (c == null) {
                continue;
            }
            check.keys(c, Set.of("as", "function", "args"), at);
            output(check.string(c, "as", at, true), Check.child(at, "as"), outputs);
            String fn = check.string(c, "function", at, true);
            int[] arity = fn == null ? null : DashboardVocabulary.FUNCTIONS.get(fn);
            if (fn != null && arity == null) {
                check.error(Check.E008, "function is not part of the computed-field vocabulary",
                        Check.child(at, "function"));
            }
            YamlNode.YamlSeq args = check.seq(c.get("args"), Check.child(at, "args"), "args", false);
            if (args == null) {
                continue;
            }
            if (arity != null && (args.items().size() < arity[0] || args.items().size() > arity[1])) {
                check.error(Check.E003, "argument count is outside the function's range", Check.child(at, "args"));
            }
            for (int k = 0; k < args.items().size(); k++) {
                computeArgument(args.items().get(k), Check.index(Check.child(at, "args"), k), params);
            }
            if (fn != null && fn.equals("date-bucket") && args.items().size() == 2
                    && args.items().get(1) instanceof YamlNode.YamlMap unit
                    && unit.get("value") instanceof YamlNode.YamlScalar us
                    && !DashboardVocabulary.TIME_UNITS.contains(us.text())) {
                check.error(Check.E007, "date-bucket unit must be hour, day, week or month",
                        Check.index(Check.child(at, "args"), 1));
            }
        }
    }

    /** DLS-QUERY-010: exactly one of field, value, context or parameter. */
    private void computeArgument(YamlNode node, String path, Set<String> params) {
        YamlNode.YamlMap a = check.map(node, path, "argument");
        if (a == null) {
            return;
        }
        check.keys(a, Set.of("field", "value", "context", "parameter"), path);
        int present = 0;
        for (String key : List.of("field", "value", "context", "parameter")) {
            if (a.get(key) != null) {
                present++;
            }
        }
        if (present != 1) {
            check.error(Check.E003, "argument must declare exactly one of field, value, context or parameter", path);
            return;
        }
        if (a.get("field") != null) {
            check.string(a, "field", path, false);
        } else if (a.get("value") != null && !(a.get("value") instanceof YamlNode.YamlScalar)) {
            check.error(Check.E003, "value must be a scalar literal", Check.child(path, "value"));
        } else if (a.get("context") != null) {
            check.oneOf(a, "context", DashboardVocabulary.CONTEXT_ARGS, path, false);
        } else if (a.get("parameter") != null) {
            String p = check.string(a, "parameter", path, false);
            if (p != null && !params.contains(p)) {
                check.error(Check.E006, "parameter is not declared by this query", Check.child(path, "parameter"));
            }
        }
    }

    private void predict(YamlNode node, String path, Set<String> outputs) {
        if (node == null) {
            return;
        }
        YamlNode.YamlSeq seq = check.seq(node, path, "predict", true);
        if (seq == null) {
            return;
        }
        for (int i = 0; i < seq.items().size(); i++) {
            String at = Check.index(path, i);
            YamlNode.YamlMap p = check.map(seq.items().get(i), at, "prediction");
            if (p == null) {
                continue;
            }
            check.keys(p, Set.of("field", "on", "method", "order", "groupby", "as"), at);
            check.string(p, "field", at, true);
            output(check.string(p, "as", at, true), Check.child(at, "as"), outputs);
            String method = "linear";
            if (p.get("method") != null) {
                String m = check.oneOf(p, "method", DashboardVocabulary.PREDICT_METHODS, at, false);
                method = m == null ? "linear" : m;
            }
            YamlNode on = p.get("on");
            int predictors = 1;
            if (on instanceof YamlNode.YamlSeq onSeq) {
                predictors = onSeq.items().size();
                if (predictors < 1 || predictors > 8) {
                    check.error(Check.E003, "on must name one to eight predictor fields", Check.child(at, "on"));
                }
            } else if (on == null) {
                check.error(Check.E003, "missing required field", Check.child(at, "on"));
            } else {
                check.stringValue(on, Check.child(at, "on"), "on");
            }
            if (predictors > 1 && !method.equals("linear")) {
                check.error(Check.E003, "only linear may declare more than one predictor", Check.child(at, "on"));
            }
            if (p.get("order") != null) {
                if (!method.equals("poly")) {
                    check.error(Check.E003, "order may appear only with poly", Check.child(at, "order"));
                } else if (!(p.get("order") instanceof YamlNode.YamlScalar o) || o.asLong().isEmpty()
                        || o.asLong().getAsLong() < 1 || o.asLong().getAsLong() > 10) {
                    check.error(Check.E003, "order must be an integer from 1 to 10", Check.child(at, "order"));
                }
            }
        }
    }

    private void aggregate(YamlNode node, String path) {
        if (node == null) {
            return;
        }
        YamlNode.YamlMap a = check.map(node, path, "aggregate");
        if (a == null) {
            return;
        }
        check.keys(a, Set.of("by", "values"), path);
        Set<String> outputs = new HashSet<>();
        if (a.get("by") != null) {
            YamlNode.YamlSeq by = check.seq(a.get("by"), Check.child(path, "by"), "by", true);
            if (by != null) {
                for (int i = 0; i < by.items().size(); i++) {
                    output(check.stringValue(by.items().get(i), Check.index(Check.child(path, "by"), i), "by"),
                            Check.index(Check.child(path, "by"), i), outputs);
                }
            }
        }
        YamlNode.YamlSeq values = check.seq(a.get("values"), Check.child(path, "values"), "values", true);
        if (values == null) {
            return;
        }
        if (values.items().size() > DashboardVocabulary.MAX_AGGREGATES) {
            check.error(Check.E003, "a query must not declare more than 64 aggregate values", Check.child(path, "values"));
        }
        for (int i = 0; i < values.items().size(); i++) {
            String at = Check.index(Check.child(path, "values"), i);
            YamlNode.YamlMap v = check.map(values.items().get(i), at, "aggregate value");
            if (v == null) {
                continue;
            }
            check.keys(v, Set.of("field", "as", "reducer", "filter"), at);
            check.string(v, "field", at, true);
            output(check.string(v, "as", at, true), Check.child(at, "as"), outputs);
            check.oneOf(v, "reducer", DashboardVocabulary.REDUCERS, at, true);
            if (v.get("filter") != null) {
                aggregateFilter(v.get("filter"), Check.child(at, "filter"));
            }
        }
    }

    /** DLS-QUERY-024: one to eight predicates, exactly one of equals or in, finite scalars. */
    private void aggregateFilter(YamlNode node, String path) {
        YamlNode.YamlMap f = check.map(node, path, "aggregate filter");
        if (f == null) {
            return;
        }
        check.keys(f, Set.of("predicates"), path);
        YamlNode.YamlSeq preds = check.seq(f.get("predicates"), Check.child(path, "predicates"), "predicates", true);
        if (preds == null) {
            return;
        }
        if (preds.items().size() > 8) {
            check.error(Check.E003, "an aggregate filter declares at most eight predicates", Check.child(path, "predicates"));
        }
        for (int i = 0; i < preds.items().size(); i++) {
            String at = Check.index(Check.child(path, "predicates"), i);
            YamlNode.YamlMap p = check.map(preds.items().get(i), at, "predicate");
            if (p == null) {
                continue;
            }
            check.keys(p, Set.of("field", "equals", "in"), at);
            check.string(p, "field", at, true);
            boolean equals = p.get("equals") != null;
            boolean in = p.get("in") != null;
            if (equals == in) {
                check.error(Check.E003, "predicate must declare exactly one of equals or in", at);
            } else if (equals && !Check.isScalarLiteral(p.get("equals"))) {
                check.error(Check.E003, "equals must be a finite scalar", Check.child(at, "equals"));
            } else if (in) {
                YamlNode.YamlSeq values = check.seq(p.get("in"), Check.child(at, "in"), "in", true);
                if (values != null) {
                    boolean bad = values.items().size() > 32;
                    for (YamlNode v : values.items()) {
                        bad |= !Check.isScalarLiteral(v);
                    }
                    if (bad) {
                        check.error(Check.E003, "in must hold one to 32 finite scalars", Check.child(at, "in"));
                    }
                }
            }
        }
    }

    private void select(YamlNode node, String path) {
        if (node == null) {
            return;
        }
        YamlNode.YamlSeq seq = check.seq(node, path, "select", true);
        if (seq == null) {
            return;
        }
        Set<String> outputs = new HashSet<>();
        for (int i = 0; i < seq.items().size(); i++) {
            String at = Check.index(path, i);
            YamlNode.YamlMap s = check.map(seq.items().get(i), at, "select entry");
            if (s == null) {
                continue;
            }
            check.keys(s, Set.of("field", "as"), at);
            String field = check.string(s, "field", at, true);
            String as = check.string(s, "as", at, false);
            output(as != null ? as : field, Check.child(at, as != null ? "as" : "field"), outputs);
        }
    }

    private void orderBy(YamlNode node, String path) {
        if (node == null) {
            return;
        }
        YamlNode.YamlSeq seq = check.seq(node, path, "order-by", true);
        if (seq == null) {
            return;
        }
        for (int i = 0; i < seq.items().size(); i++) {
            String at = Check.index(path, i);
            YamlNode.YamlMap o = check.map(seq.items().get(i), at, "order-by entry");
            if (o == null) {
                continue;
            }
            check.keys(o, Set.of("field", "direction"), at);
            check.string(o, "field", at, true);
            if (o.get("direction") != null) {
                check.oneOf(o, "direction", DashboardVocabulary.DIRECTIONS, at, false);
            }
        }
    }

    /** DLS-QUERY-032: exactly keys, revision and comparator; limit and facet are forbidden. */
    private void page(YamlNode.YamlMap query, String path) {
        YamlNode.YamlMap p = check.map(query.get("page"), path, "page");
        if (p == null) {
            return;
        }
        check.keys(p, Set.of("keys", "revision", "comparator"), path);
        YamlNode.YamlSeq keys = check.seq(p.get("keys"), Check.child(path, "keys"), "keys", true);
        if (keys != null) {
            Set<String> seen = new HashSet<>();
            boolean bad = keys.items().size() > 4;
            for (YamlNode k : keys.items()) {
                bad |= !(k instanceof YamlNode.YamlScalar s && s.isString() && seen.add(s.text()));
            }
            if (bad) {
                check.error(Check.E010, "keys must be one to four unique scalar fields", Check.child(path, "keys"));
            }
        }
        if (!(p.get("revision") instanceof YamlNode.YamlScalar r) || !"workflow-subjects".equals(r.text())) {
            check.error(Check.E010, "revision must be workflow-subjects", Check.child(path, "revision"));
        }
        if (!(p.get("comparator") instanceof YamlNode.YamlScalar c) || !"utf8-bytewise-v1".equals(c.text())) {
            check.error(Check.E010, "comparator must be utf8-bytewise-v1", Check.child(path, "comparator"));
        }
        if (query.get("limit") != null || query.get("facet") != null) {
            check.error(Check.E010, "a paged query must not declare limit or facet", path);
        }
    }

    /** DLS-CTX-002 / DLS-CTX-009: {@code range} xor RFC 3339 {@code start}/{@code end}, start before end. */
    static void time(YamlNode node, String path, Check check) {
        YamlNode.YamlMap t = check.map(node, path, "time");
        if (t == null) {
            return;
        }
        check.keys(t, DashboardVocabulary.TIME_KEYS, path);
        boolean absolute = t.get("start") != null || t.get("end") != null;
        if (t.get("range") != null) {
            String range = check.stringValue(t.get("range"), Check.child(path, "range"), "range");
            if (range != null && !DashboardVocabulary.RANGE.matcher(range).matches()) {
                check.error(Check.E010, "range must be all or a positive integer followed by h, d or w",
                        Check.child(path, "range"));
            }
            if (absolute) {
                check.error(Check.E010, "range must not appear with start or end", path);
            }
        }
        OffsetDateTime start = instant(t, "start", path, check);
        OffsetDateTime end = instant(t, "end", path, check);
        if (start != null && end != null && !start.toInstant().isBefore(end.toInstant())) {
            check.error(Check.E010, "start must precede end", path);
        }
    }

    private static OffsetDateTime instant(YamlNode.YamlMap t, String key, String path, Check check) {
        if (t.get(key) == null) {
            return null;
        }
        String text = check.stringValue(t.get(key), Check.child(path, key), key);
        if (text == null) {
            return null;
        }
        try {
            return OffsetDateTime.parse(text);
        } catch (DateTimeParseException e) {
            check.error(Check.E010, key + " must be an RFC 3339 timestamp", Check.child(path, key));
            return null;
        }
    }
}
