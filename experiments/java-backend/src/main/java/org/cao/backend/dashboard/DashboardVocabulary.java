package org.cao.backend.dashboard;

import java.util.List;
import java.util.Set;
import java.util.regex.Pattern;

/**
 * Closed vocabulary of Dashboard Language 0.1.0 taken from docs/dashboard-language-specification.md
 * (§4.2 vocabulary table, §5.1/§5.4 sources, §5.5 queries, §7 aggregates, Appendix B codes).
 * Where the specification table omits a key that its own prose defines, the prose key is included
 * and marked in a comment.
 */
final class DashboardVocabulary {
    static final String LANGUAGE_VERSION = "0.1.0";
    static final Pattern IDENTIFIER = Pattern.compile("^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$");
    static final Pattern RANGE = Pattern.compile("^(all|[1-9][0-9]*[hdw])$");
    static final Pattern SLUG = Pattern.compile("^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9._-]+$");

    static final Set<String> ROOT_KEYS = Set.of("language-version", "dashboard");
    static final Set<String> DASHBOARD_KEYS = Set.of(
            "id", "title", "description", "github-url-base", "repository", "callouts", "cli-actions", "horizon",
            "data-status", "defaults", "units", "queries", "card-templates", "views", "pages", "navigation");
    static final Set<String> DEFAULTS_KEYS = Set.of("scope", "time", "filters");
    static final Set<String> SCOPE_KEYS = Set.of("organizations", "repositories", "workflows");
    static final Set<String> TIME_KEYS = Set.of("range", "start", "end");
    static final Set<String> UNIT_KEYS = Set.of("name", "symbol", "significant", "format");
    static final Set<String> UNIT_FORMATS = Set.of("aicc", "duration", "number", "usd", "bytes", "compact-number");
    static final Set<String> TOOLTIP_KEYS = Set.of("label", "description", "icon");
    static final Set<String> HORIZON_KEYS = Set.of("label", "tooltip");
    static final Set<String> CALLOUT_KEYS = Set.of("id", "title", "description", "icon", "navigation-page", "visible-when");
    static final Set<String> VISIBLE_WHEN_KEYS = Set.of("source", "field", "equals", "older-than-hours");
    static final Set<String> NAVIGATION_SECTION_KEYS = Set.of("label", "pages", "experimental", "placement");
    // §8 prose: freshness-source, health-source and ingestion-source.
    static final Set<String> DATA_STATUS_KEYS = Set.of("freshness-source", "health-source", "ingestion-source");

    static final Set<String> BUILT_IN_PAGES = Set.of(
            "overview", "organizations", "repositories", "campaigns", "workflows", "runs", "experiments", "graders",
            "evals", "usage", "engines-models", "operational-value", "findings", "issues");
    private static final Set<String> COMMON_PAGE_KEYS = Set.of(
            "id", "kind", "title", "navigation-label", "navigation-indicator", "description", "tooltip", "intent",
            "subject", "objective", "acceptance", "icon", "class-name", "experimental", "filter-bar",
            "view-mode-control", "pull-refresh", "mode-indicator", "query-binding", "requires", "form");
    static final Set<String> BUILT_IN_PAGE_KEYS = union(COMMON_PAGE_KEYS, Set.of("page", "definition"));
    // retain-on-navigation is defined by §11.1 prose rather than the §4.2 table.
    static final Set<String> CUSTOM_PAGE_KEYS = union(
            COMMON_PAGE_KEYS, Set.of("route", "views", "sections", "retain-on-navigation"));
    static final Set<String> PAGE_SECTION_KEYS = Set.of(
            "id", "title", "description", "layout", "views", "count-source", "count-sources", "count-field",
            "count-label");

    // prompt, prompt-level and disclosure-label are defined by §11.1 / DLS-VIEW-040 prose.
    static final Set<String> VIEW_KEYS = Set.of(
            "id", "title", "show-title", "description", "subject", "objective", "acceptance", "locked", "requires",
            "data", "mark", "element", "config", "callout", "chart", "facet", "columns", "treemap", "scatter", "layer",
            "resolve", "metric", "list", "tree", "layout", "disclosure", "controls", "filter-bar", "lazy-list",
            "column-summaries", "empty-message", "title-link", "encoding", "prompt", "prompt-level", "disclosure-label");
    static final Set<String> VIEW_DATA_KEYS = Set.of(
            "source", "sources", "partial-source", "scope", "time", "filters", "arguments", "route-field", "limit",
            "order-by");
    static final Set<String> MARKS = Set.of("metric", "table", "list", "chart", "element", "callout");
    static final Set<String> LAYOUTS = Set.of("full", "full-view", "half", "third", "horizontal");
    static final Set<String> DISCLOSURE = Set.of("essential", "supplemental");
    static final Set<String> CONTROLS = Set.of("interactive", "filter-only", "static");
    static final Set<String> CHART_WIDGETS = Set.of(
            "area", "line", "dot", "bar", "horizontal-bar", "pie", "heatmap", "histogram", "scatter", "swimlane",
            "treemap");
    static final Set<String> LIST_STYLES = Set.of("cards", "issues");
    static final Set<String> ELEMENTS = Set.of(
            "campaign-route", "workflow-route-page", "outcome-detail", "outcome-detail-section", "problem-detail",
            "entity-route", "configuration-policy", "measure-history", "workflow-value-report",
            "workflow-value-snapshot", "factory-header", "factory-floor", "all-campaign-memory", "link-button-list",
            "markdown", "query-editor", "command-terminal", "environment-defaults", "prompt-wizard", "storage-info");
    /** DLS-VIEW-002: interaction-only and deployment-only elements that may declare empty data.sources. */
    static final Set<String> EMPTY_SOURCE_ELEMENTS = Set.of(
            "query-editor", "command-terminal", "environment-defaults", "prompt-wizard", "storage-info");
    static final Set<String> ENCODING_KEYS = Set.of(
            "value", "columns", "card", "x", "y", "color", "section", "weight", "reference", "href", "actions",
            "facet", "row", "column");
    static final Set<String> FIELD_KEYS = Set.of(
            "field", "type", "aggregate", "time-unit", "title", "as", "display", "filter", "format", "unit");
    static final Set<String> FIELD_TYPES = Set.of("nominal", "ordinal", "quantitative", "temporal");
    static final Set<String> AGGREGATES = Set.of("count", "distinct-count", "sum", "mean", "min", "max", "none");
    static final Set<String> TIME_UNITS = Set.of("hour", "day", "week", "month");
    static final Set<String> DISPLAYS = Set.of(
            "text", "status", "grader-status", "mode", "active-state", "label", "ref", "digest", "outcome-link",
            "run-link", "repository-link", "workflow-link", "evidence-link");
    static final Set<String> DIRECTIONS = Set.of("asc", "desc");
    static final Set<String> VIEW_FORBIDDEN_KEYS = Set.of(
            "join", "joins", "expression", "expressions", "formula", "script", "scripts", "template", "plugin",
            "plugins", "transform", "transforms", "compute", "filter-expression");
    /** DLS-AGG-002: the only measures `sum` accepts on a database table. */
    static final Set<String> ADDITIVE_MEASURES = Set.of(
            "input-tokens", "output-tokens", "cache-read-tokens", "cache-write-tokens", "reasoning-tokens", "aic");

    /** §5.1 tables and daily/statistical projections, §5.4 runtime sources and simulation-days. */
    static final Set<String> SOURCES = Set.of(
            "organizations", "campaigns", "repositories", "workflows", "work-items", "agent-assignments",
            "agent-smells", "workflow-smells", "security-findings", "control-plane-smells", "runs", "run-performance",
            "job-performance", "experiments", "experiment-assignments", "graders", "grader-observations", "evals",
            "eval-observations", "usage", "firewall-observations", "firewall-policy-rules", "model-usage-summary",
            "engine-usage-summary", "output-delivery-events", "output-adoption-events", "outcomes", "findings",
            "operational-values", "token-efficiency-opportunities", "token-efficiency-interventions",
            "token-efficiency-comparisons", "github-api-rate-limits", "github-api-collector-health",
            "github-api-call-stacks", "daily-run-counts", "statistical-runs", "collection-health",
            "github-quota-usage", "webhook-status", "cli-commands", "cli-command-summary", "cli-command-statuses",
            "simulation-days");

    static final Set<String> QUERY_KEYS = Set.of(
            "name", "subject", "objective", "acceptance", "description", "parameters", "from", "union", "time", "joins",
            "filter", "compute", "temporal-series", "aggregate", "predict", "window", "select", "order-by", "page",
            "limit", "facet");
    static final Set<String> JOIN_TYPES = Set.of("inner", "left", "inner-many");
    static final Set<String> PREDICATE_KEYS = Set.of("field", "equals", "in", "includes", "gte", "lt", "optional");
    static final Set<String> REDUCERS = Set.of(
            "count", "distinct-count", "distinct-list", "distinct-values", "calendar-week-rhythm",
            "latest-failure-streak", "sum", "mean", "min", "max", "unique", "median", "p75", "p90", "p95");
    static final Set<String> PREDICT_METHODS = Set.of("linear", "log", "exp", "pow", "quad", "poly");
    static final Set<String> PARAMETER_TYPES = Set.of("number", "string", "boolean");
    static final Set<String> CONTEXT_ARGS = Set.of("time-start", "time-end");

    /** §5.5.1 functions with inclusive argument counts {min, max}. */
    static final java.util.Map<String, int[]> FUNCTIONS = java.util.Map.ofEntries(
            java.util.Map.entry("coalesce", new int[] {2, 8}),
            java.util.Map.entry("link-href", new int[] {1, 1}),
            java.util.Map.entry("concat", new int[] {2, 8}),
            java.util.Map.entry("literal", new int[] {1, 1}),
            java.util.Map.entry("lower", new int[] {1, 1}),
            java.util.Map.entry("upper", new int[] {1, 1}),
            java.util.Map.entry("title-case", new int[] {1, 1}),
            java.util.Map.entry("trim", new int[] {1, 1}),
            java.util.Map.entry("url-encode", new int[] {1, 1}),
            java.util.Map.entry("date-day", new int[] {1, 1}),
            java.util.Map.entry("activity-shard-day", new int[] {1, 1}),
            java.util.Map.entry("date-bucket", new int[] {2, 2}),
            java.util.Map.entry("calendar-week-point", new int[] {3, 3}),
            java.util.Map.entry("equals-any", new int[] {2, 8}),
            java.util.Map.entry("greater-than", new int[] {2, 2}),
            java.util.Map.entry("if", new int[] {3, 3}),
            java.util.Map.entry("format-count", new int[] {1, 1}),
            java.util.Map.entry("format-percent", new int[] {1, 1}),
            java.util.Map.entry("number", new int[] {1, 1}),
            java.util.Map.entry("timestamp", new int[] {1, 1}),
            java.util.Map.entry("iso-timestamp", new int[] {1, 1}),
            java.util.Map.entry("sqrt", new int[] {1, 1}),
            java.util.Map.entry("sum", new int[] {2, 8}),
            java.util.Map.entry("product", new int[] {2, 8}),
            java.util.Map.entry("difference", new int[] {2, 2}),
            java.util.Map.entry("quotient", new int[] {2, 2}));

    static final int MAX_JOINS = 4;
    static final int MAX_AGGREGATES = 64;
    static final long MAX_QUERY_LIMIT = 100_000;
    static final int MAX_METADATA_CHARS = 512;
    static final int MAX_ESSENTIAL_VIEWS = 4;

    /** Page-level and dashboard-level members accepted without deeper validation (reported as limitations). */
    static final List<String> UNCHECKED_PAGE_MEMBERS = List.of(
            "navigation-indicator", "tooltip", "requires", "form", "route", "definition", "filter-bar");

    private DashboardVocabulary() { }

    private static Set<String> union(Set<String> a, Set<String> b) {
        java.util.HashSet<String> all = new java.util.HashSet<>(a);
        all.addAll(b);
        return Set.copyOf(all);
    }
}
