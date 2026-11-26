package org.cao.backend.dashboard;

import java.util.HashSet;
import java.util.List;
import java.util.Set;
import java.util.function.BiConsumer;

/** Validation of pages, sections and views (DLS-DOC-009/005, DLS-PAGE-001/014..024, DLS-VIEW-001..011, DLS-VAL-005). */
final class PageValidator {
    private final Check check;
    private final QueryValidator queries;
    private final Set<String> units;
    private final Set<String> referencedSources;
    private final BiConsumer<String, YamlNode> unchecked;

    PageValidator(Check check, QueryValidator queries, Set<String> units, Set<String> referencedSources,
            BiConsumer<String, YamlNode> unchecked) {
        this.check = check;
        this.queries = queries;
        this.units = units;
        this.referencedSources = referencedSources;
        this.unchecked = unchecked;
    }

    /** DLS-DOC-014: exactly non-empty label and description, optional icon. */
    static void tooltip(Check check, YamlNode node, String path) {
        YamlNode.YamlMap t = check.map(node, path, "tooltip");
        if (t == null) {
            return;
        }
        check.keys(t, DashboardVocabulary.TOOLTIP_KEYS, path);
        check.string(t, "label", path, true);
        check.string(t, "description", path, true);
        check.string(t, "icon", path, false);
    }

    void validate(YamlNode node, String path, Set<String> seenIds) {
        YamlNode.YamlMap page = check.map(node, path, "page");
        if (page == null) {
            return;
        }
        String id = check.identifier(page, "id", path, true);
        if (id != null && !seenIds.add(id)) {
            check.error(Check.E004, "page ids must be unique", Check.child(path, "id"));
        }
        String kind = check.oneOf(page, "kind", Set.of("built-in", "custom"), path, true);
        if (kind == null) {
            return;
        }
        boolean custom = kind.equals("custom");
        check.keys(page, custom ? DashboardVocabulary.CUSTOM_PAGE_KEYS : DashboardVocabulary.BUILT_IN_PAGE_KEYS, path);
        check.string(page, "title", path, false);
        check.string(page, "description", path, false);
        check.string(page, "navigation-label", path, false);
        check.string(page, "icon", path, false);
        check.metadataBound(page, path);
        if (page.get("class-name") != null) {
            check.identifier(page, "class-name", path, false);
        }
        for (String flag : List.of("experimental", "view-mode-control", "pull-refresh", "mode-indicator",
                "retain-on-navigation", "filter-bar")) {
            if (page.get(flag) != null && !(flag.equals("filter-bar") && page.get(flag) instanceof YamlNode.YamlMap)) {
                check.bool(page, flag, path);
            }
        }
        if (page.get("query-binding") != null) {
            check.oneOf(page, "query-binding", Set.of("view", "page"), path, false);
        }
        for (String member : DashboardVocabulary.UNCHECKED_PAGE_MEMBERS) {
            if (page.get(member) != null) {
                unchecked.accept("page " + member, page.get(member));
            }
        }
        if (custom) {
            customPage(page, path);
        } else {
            check.oneOf(page, "page", DashboardVocabulary.BUILT_IN_PAGES, path, true);
        }
    }

    private void customPage(YamlNode.YamlMap page, String path) {
        YamlNode.YamlSeq views = check.seq(page.get("views"), Check.child(path, "views"), "views", true);
        if (views == null) {
            return;
        }
        Set<String> viewIds = new HashSet<>();
        int essential = 0;
        boolean usesDisclosure = false;
        for (int i = 0; i < views.items().size(); i++) {
            String at = Check.index(Check.child(path, "views"), i);
            YamlNode.YamlMap view = check.map(views.items().get(i), at, "view");
            if (view == null) {
                continue;
            }
            String id = check.identifier(view, "id", at, true);
            if (id != null && !viewIds.add(id)) {
                check.error(Check.E004, "view ids must be unique within a page", Check.child(at, "id"));
            }
            validateView(view, at);
            if (view.get("disclosure") != null) {
                usesDisclosure = true;
            }
        }
        for (YamlNode item : views.items()) {
            if (item instanceof YamlNode.YamlMap v && !"supplemental".equals(scalar(v.get("disclosure")))) {
                essential++;
            }
        }
        if (usesDisclosure && (essential == 0 || essential > DashboardVocabulary.MAX_ESSENTIAL_VIEWS)) {
            check.error(Check.E013, "a page using disclosure must have one to four essential views", path);
        }
        if (page.get("sections") != null) {
            sections(page.get("sections"), Check.child(path, "sections"), viewIds);
        }
    }

    private static String scalar(YamlNode node) {
        return node instanceof YamlNode.YamlScalar s ? s.text() : null;
    }

    private void sections(YamlNode node, String path, Set<String> viewIds) {
        YamlNode.YamlSeq seq = check.seq(node, path, "sections", true);
        if (seq == null) {
            return;
        }
        Set<String> ids = new HashSet<>();
        for (int i = 0; i < seq.items().size(); i++) {
            String at = Check.index(path, i);
            YamlNode.YamlMap s = check.map(seq.items().get(i), at, "section");
            if (s == null) {
                continue;
            }
            check.keys(s, DashboardVocabulary.PAGE_SECTION_KEYS, at);
            String id = check.identifier(s, "id", at, true);
            if (id != null && !ids.add(id)) {
                check.error(Check.E004, "section ids must be unique", Check.child(at, "id"));
            }
            check.string(s, "title", at, false);
            check.string(s, "description", at, false);
            YamlNode.YamlSeq refs = check.seq(s.get("views"), Check.child(at, "views"), "views", true);
            if (refs != null) {
                for (int k = 0; k < refs.items().size(); k++) {
                    String ref = check.stringValue(refs.items().get(k), Check.index(Check.child(at, "views"), k), "view id");
                    if (ref != null && !viewIds.contains(ref)) {
                        check.error(Check.E006, "section references an undeclared view", Check.index(Check.child(at, "views"), k));
                    }
                }
            }
        }
    }

    private void validateView(YamlNode.YamlMap view, String path) {
        for (String key : view.entries().keySet()) {
            if (DashboardVocabulary.VIEW_FORBIDDEN_KEYS.contains(key)) {
                check.error(Check.E008, "views must not declare joins, expressions or other transforms",
                        Check.child(path, key));
            } else if (!DashboardVocabulary.VIEW_KEYS.contains(key)) {
                check.error(Check.E004, "unknown key", Check.child(path, key));
            }
        }
        check.string(view, "title", path, false);
        check.string(view, "description", path, false);
        check.metadataBound(view, path);
        for (String enumKey : List.of("layout", "disclosure", "controls")) {
            if (view.get(enumKey) != null) {
                check.oneOf(view, enumKey, switch (enumKey) {
                    case "layout" -> DashboardVocabulary.LAYOUTS;
                    case "disclosure" -> DashboardVocabulary.DISCLOSURE;
                    default -> DashboardVocabulary.CONTROLS;
                }, path, false);
            }
        }
        if (view.get("chart") != null) {
            check.oneOf(view, "chart", DashboardVocabulary.CHART_WIDGETS, path, false);
        }
        for (String member : List.of("requires", "facet", "treemap", "scatter", "layer", "resolve", "metric", "tree",
                "filter-bar", "title-link", "config", "column-summaries")) {
            if (view.get(member) != null) {
                unchecked.accept("view " + member, view.get(member));
            }
        }
        String mark = check.oneOf(view, "mark", DashboardVocabulary.MARKS, path, true);
        if (mark == null) {
            return;
        }
        if (mark.equals("callout")) {
            callout(view, path);
            return;
        }
        data(view, mark, path);
        encoding(view, mark, path);
        if (mark.equals("element")) {
            element(view, path);
        }
        if (mark.equals("list")) {
            list(view, path);
        } else if (view.get("list") != null) {
            check.error(Check.E004, "list is valid only on list views", Check.child(path, "list"));
        }
        if (!mark.equals("element") && view.get("config") != null) {
            check.error(Check.E004, "config is valid only on element views", Check.child(path, "config"));
        }
    }

    /** DLS-DOC-014a: list.style and list.icon (the icon set itself is not checked). */
    private void list(YamlNode.YamlMap view, String path) {
        YamlNode.YamlMap list = check.map(view.get("list"), Check.child(path, "list"), "list");
        if (list != null) {
            check.oneOf(list, "style", DashboardVocabulary.LIST_STYLES, Check.child(path, "list"), true);
            check.string(list, "icon", Check.child(path, "list"), true);
            unchecked.accept("view list options other than style and icon", list);
        }
    }

    private void callout(YamlNode.YamlMap view, String path) {
        check.string(view, "title", path, true);
        check.string(view, "description", path, true);
        YamlNode.YamlMap c = check.map(view.get("callout"), Check.child(path, "callout"), "callout");
        if (c != null) {
            check.keys(c, Set.of("label", "icon"), Check.child(path, "callout"));
            check.string(c, "label", Check.child(path, "callout"), true);
            check.string(c, "icon", Check.child(path, "callout"), true);
        }
        if (view.get("data") != null || view.get("encoding") != null) {
            check.error(Check.E003, "a callout view must not declare data or encoding", path);
        }
    }

    /** DLS-VIEW-002, DLS-SEM-017, DLS-VIEW-010. */
    private void data(YamlNode.YamlMap view, String mark, String path) {
        String at = Check.child(path, "data");
        YamlNode.YamlMap data = check.map(view.get("data"), at, "data");
        if (data == null) {
            return;
        }
        check.keys(data, DashboardVocabulary.VIEW_DATA_KEYS, at);
        if (mark.equals("element")) {
            if (data.get("source") != null) {
                check.error(Check.E003, "an element view declares data.sources, not data.source", Check.child(at, "source"));
            }
            elementSources(view, data, at);
        } else {
            if (data.get("sources") != null) {
                check.error(Check.E003, "this mark declares one data.source", Check.child(at, "sources"));
            }
            String source = check.string(data, "source", at, true);
            if (source != null) {
                source(source, Check.child(at, "source"));
            }
            String partial = check.string(data, "partial-source", at, false);
            if (partial != null) {
                source(partial, Check.child(at, "partial-source"));
            }
        }
        ContextChecks.context(check, data.get("scope"), data.get("time"), data.get("filters"), at);
        if (data.get("limit") != null) {
            Long limit = check.positiveInt(data.get("limit"), Check.child(at, "limit"), "limit", Check.E010);
            if ("horizontal-bar".equals(scalar(view.get("chart"))) && limit != null && limit > 100) {
                check.error(Check.E010, "a horizontal-bar chart limit must not exceed 100", Check.child(at, "limit"));
            }
        } else if ("horizontal-bar".equals(scalar(view.get("chart"))) || "heatmap".equals(scalar(view.get("chart")))) {
            check.error(Check.E010, "this chart must declare data.limit of at most 100", at);
        }
        orderBy(data.get("order-by"), Check.child(at, "order-by"));
        if (data.get("arguments") != null) {
            unchecked.accept("view data.arguments", data.get("arguments"));
        }
    }

    private void elementSources(YamlNode.YamlMap view, YamlNode.YamlMap data, String at) {
        YamlNode.YamlSeq sources = check.seq(data.get("sources"), Check.child(at, "sources"), "sources", false);
        if (sources == null) {
            return;
        }
        Set<String> seen = new HashSet<>();
        for (int i = 0; i < sources.items().size(); i++) {
            String name = check.stringValue(sources.items().get(i), Check.index(Check.child(at, "sources"), i), "source");
            if (name == null) {
                continue;
            }
            if (!seen.add(name)) {
                check.error(Check.E004, "data.sources must not contain duplicates", Check.index(Check.child(at, "sources"), i));
            }
            source(name, Check.index(Check.child(at, "sources"), i));
        }
        if (sources.items().isEmpty() && !DashboardVocabulary.EMPTY_SOURCE_ELEMENTS.contains(scalar(view.get("element")))) {
            check.error(Check.E003, "this element requires at least one data source", Check.child(at, "sources"));
        }
    }

    private void source(String name, String path) {
        if (DashboardVocabulary.SOURCES.contains(name) || queries.names().contains(name)) {
            referencedSources.add(name);
        } else {
            check.error(Check.E006, "unknown source", path);
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
            if (o != null) {
                check.keys(o, Set.of("field", "direction"), at);
                check.string(o, "field", at, true);
                if (o.get("direction") != null) {
                    check.oneOf(o, "direction", DashboardVocabulary.DIRECTIONS, at, false);
                }
            }
        }
    }

    private void element(YamlNode.YamlMap view, String path) {
        check.oneOf(view, "element", DashboardVocabulary.ELEMENTS, path, true);
        if (view.get("encoding") != null) {
            check.error(Check.E003, "an element view must not declare encoding", Check.child(path, "encoding"));
        }
    }

    /** DLS-VIEW-003..005, DLS-VIEW-008/009, DLS-AGG-002/005/009/010, DLS-UNIT-001. */
    private void encoding(YamlNode.YamlMap view, String mark, String path) {
        if (mark.equals("element")) {
            return;
        }
        String at = Check.child(path, "encoding");
        YamlNode.YamlMap enc = check.map(view.get("encoding"), at, "encoding");
        if (enc == null) {
            return;
        }
        check.keys(enc, DashboardVocabulary.ENCODING_KEYS, at);
        String source = scalar(view.get("data") instanceof YamlNode.YamlMap d ? d.get("source") : null);
        Set<String> outputs = new HashSet<>();
        for (var entry : enc.entries().entrySet()) {
            String channel = entry.getKey();
            String channelAt = Check.child(at, channel);
            if (channel.equals("columns")) {
                YamlNode.YamlSeq columns = check.seq(entry.getValue(), channelAt, "columns", true);
                if (columns != null) {
                    for (int i = 0; i < columns.items().size(); i++) {
                        field(columns.items().get(i), Check.index(channelAt, i), true, source, outputs, false);
                    }
                }
            } else if (entry.getValue() instanceof YamlNode.YamlSeq seq && channel.equals("y")) {
                boolean line = "line".equals(scalar(view.get("chart")));
                if (!line || seq.items().size() < 2 || seq.items().size() > 8) {
                    check.error(Check.E007, "only a line chart may encode y as two to eight fields", channelAt);
                }
                for (int i = 0; i < seq.items().size(); i++) {
                    field(seq.items().get(i), Check.index(channelAt, i), false, source, outputs, true);
                }
            } else if (channel.equals("card") || channel.equals("actions") || channel.equals("facet")
                    || channel.equals("row") || channel.equals("column")) {
                unchecked.accept("view encoding." + channel, entry.getValue());
            } else {
                field(entry.getValue(), channelAt, false, source, outputs, channel.equals("y"));
            }
        }
        switch (mark) {
            case "metric" -> {
                if (enc.get("value") == null) {
                    check.error(Check.E007, "a metric must encode value", at);
                }
                for (String bad : List.of("columns", "x", "y", "color", "section", "weight", "reference")) {
                    if (enc.get(bad) != null) {
                        check.error(Check.E007, "a metric must not encode chart or table channels", Check.child(at, bad));
                    }
                }
            }
            case "table", "list" -> {
                if (enc.get("columns") == null) {
                    check.error(Check.E007, "this mark must encode columns", at);
                }
                for (String bad : List.of("value", "x", "y", "color")) {
                    if (enc.get(bad) != null) {
                        check.error(Check.E007, "this mark must not encode value, x, y or color", Check.child(at, bad));
                    }
                }
            }
            default -> {
                if (enc.get("x") == null || enc.get("y") == null) {
                    check.error(Check.E007, "a chart must encode x and y", at);
                }
                if (enc.get("columns") != null) {
                    check.error(Check.E007, "a chart must not encode columns", Check.child(at, "columns"));
                }
            }
        }
    }

    private void field(YamlNode node, String path, boolean column, String source, Set<String> outputs, boolean measure) {
        YamlNode.YamlMap f = check.map(node, path, "field definition");
        if (f == null) {
            return;
        }
        check.keys(f, DashboardVocabulary.FIELD_KEYS, path);
        String name = check.string(f, "field", path, true);
        String type = f.get("type") == null ? null : check.oneOf(f, "type", DashboardVocabulary.FIELD_TYPES, path, false);
        String aggregate = f.get("aggregate") == null ? "none"
                : check.oneOf(f, "aggregate", DashboardVocabulary.AGGREGATES, path, false);
        if (aggregate == null) {
            aggregate = "none";
        }
        check.string(f, "title", path, false);
        if (f.get("time-unit") != null) {
            check.oneOf(f, "time-unit", DashboardVocabulary.TIME_UNITS, path, false);
            if (type != null && !type.equals("temporal")) {
                check.error(Check.E007, "time-unit may be used only with a temporal field", Check.child(path, "time-unit"));
            }
        }
        if (f.get("display") != null) {
            check.oneOf(f, "display", DashboardVocabulary.DISPLAYS, path, false);
            if (!column) {
                check.error(Check.E007, "display is valid only on table columns", Check.child(path, "display"));
            }
        }
        if (f.get("filter") != null) {
            check.bool(f, "filter", path);
        }
        check.string(f, "format", path, false);
        if (f.get("unit") != null) {
            String unit = check.string(f, "unit", path, false);
            if (unit != null && !units.contains(unit)) {
                check.error(Check.E006, "unit must reference a declared dashboard unit", Check.child(path, "unit"));
            }
        }
        // DLS-AGG-009: as only with an aggregate; derived id is <aggregate>-<field>.
        String as = f.get("as") == null ? null : check.string(f, "as", path, false);
        if (f.get("as") != null && aggregate.equals("none")) {
            check.error(Check.E010, "as is valid only when aggregate is not none", Check.child(path, "as"));
        }
        if (!aggregate.equals("none") && name != null) {
            String id = as != null ? as : aggregate + "-" + name;
            if (!outputs.add(id)) {
                check.error(Check.E010, "aggregate output identifiers must be unique within a view", path);
            }
            // DLS-AGG-002/005: sum only for the raw-token measures and aic; a derived query defines its own output.
            if (aggregate.equals("sum") && !DashboardVocabulary.ADDITIVE_MEASURES.contains(name)
                    && !queries.names().contains(source)) {
                check.error(Check.E010, "sum is accepted only for the raw-token measures and aic",
                        Check.child(path, "aggregate"));
            }
        }
    }
}
