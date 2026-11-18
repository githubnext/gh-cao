---
name: generate-dashboard-ir
description: Generate valid low-level Dashboard Language YAML from user intent using the provided dashboard specification and validator entry point.
---

# Generate Dashboard IR

Generate one complete Dashboard Language YAML document from the user's dashboard intent. Use the provided Dashboard Language specification as the semantic authority and the provided validator entry point as the syntax and structural validation authority.

Do not introduce a new intermediate language. Do not output an intermediate semantic plan unless explicitly requested.

## Inputs

The working context provides:

- user intent;
- a Dashboard Language specification file;
- a validator entry point;
- the built-in dashboard configuration at `dashboard/site/dashboard.json`;
- optionally, an existing dashboard YAML document.

Read the specification and built-in dashboard configuration before generating or modifying dashboard YAML. Use the built-in configuration as the reference for established rich views, including useful columns, links, status displays, and interactive controls. Do not rely on remembered Dashboard Language syntax when the provided specification defines it.

## Output

Produce one complete Dashboard Language YAML document that:

- represents the user's intent;
- conforms to the provided specification;
- passes the provided validator entry point;
- contains only supported Dashboard Language vocabulary;
- contains no unresolved placeholders or speculative fields.

## Procedure

1. Read the specification (by default `docs/dashboard-language-specification.md`) and `dashboard/site/dashboard.json`, then identify the relevant root structure, database tables and grains, fields, dimensions, measures, aggregates, marks, encodings, pages, filters, links, routing, data-state semantics, defaults, and validation constraints. For CAO data sources, also read the relevant sections of the checked-out `specs/dashboard-data.md` at the revision being authored; main's online specification may not describe an unmerged feature. For charts, also read [Declarative charts](references/declarative-charts.md) for facet, layer, treemap, and responsive layout guidance. Reuse an established built-in view pattern when it satisfies the intent, adapting only its scope and labels. When prior knowledge differs, follow the provided specification.
2. Interpret the minimum information necessary to satisfy the intent: purpose, questions, entities, measures, dimensions, time range, filters, comparisons, rankings, trends, and drilldown needs. Prefer a small set of views that directly answers the requested questions.
  Internally trace activation and execution, every required operator action, and accepted-success evidence to a view, source grain, canonical fields, scope, time, filters, and links. A generic run, finding, or outcome count does not cover task-specific evidence.
3. Prefer a built-in page when it satisfies the intent. Use a custom page only when the requested analysis is not built in, requires a specific combination of measures or dimensions, or explicitly requests a custom presentation.
4. For every custom view, select the database table or query whose grain matches the analysis. Do not choose an input only because it has a similarly named field, and do not fabricate joins unless the specification supports them.
   If database tables and fields cannot represent an essential intent requirement, report that constraint instead of substituting a generic proxy. When table availability is provided, do not silently reference an absent table.
   Before adding a join, write down the identity keys and grain of each side; aggregate the many-side first when necessary, and join on all applicable scope keys. A link field may be projected for navigation but is not an identity key.
   For explicitly migrated additive run-health consumers, use the registered `daily-run-counts` projection under data specification §72.2 and language specification §5.1.2. Sum its counters, not bucket rows. Preserve its supported scope, whole-UTC-day semantics, independent three-week retention, and partial/unavailable provenance through joins. Never answer rollout-mode or other unsupported filters, infer accepted outcomes, union totals with retained Runs, or fall back to detail when daily evidence is unavailable. Other analyses remain on their declared sources.
5. Map user terminology to canonical fields from the specification. Verify every field name, semantic, and enum value; never invent fields, aliases, dimensions, or measures.
6. Use only aggregates allowed for the selected fields. Respect non-additive measures and use explicit output aliases when required for correctness or later references such as ordering.
7. Select the simplest valid view. Prefer a metric for a single aggregate, a table for inventory or records, a table or bar chart for rankings, a line chart for time trends, and a bar chart for categorical comparisons when the specification supports them. Prefer a pie chart for aggregate distributions of discrete states, such as run outcomes, because the aggregate composition is a better fit than a temporal line chart.
   Use facets for categorical small multiples, layers for complementary marks over one already-shaped source, and treemaps for positive proportional contributions with optional one-level categorical nesting. Select explicit structural layout hints; do not invent Vega-Lite transforms, nested specs, pixel sizes, or breakpoints.
   For an actionable queue, prefer a filtered record table that exposes identity, required evidence or status, scope, and a repository, run, issue, or pull-request link. Use a categorical chart only when the distribution itself answers the intent; an outcome-state count alone does not establish accepted evidence.
   When a page's primary content is a list of items that can grow unbounded (repositories, workflows, campaigns, commands), prefer a full-view lazy card list: either `mark: list` with `list.style: entity-cards` and a declared `list.card` template, or a `table` with `encoding.card`; in both cases set `lazy-list: true`, `layout: full-view`, and `show-title: false` when the page title already names the list. Declare filters in the view's `filter-bar` so they render in the card toolbar. Never render every row eagerly or rely on a fixed row limit for such lists.
8. Generate explicit low-level Dashboard Language YAML. Resolve all applicable sources, scopes, time ranges, filters, ordering, limits, marks, chart types, encodings, field types, aggregates, aliases, time units, layouts, sections, links, routes, disclosure, and units. Give each view and named query a concise `subject` describing what its data is about or intended to show; use `objective` for why it exists or what to improve or understand, and `acceptance` for the conditions that make it acceptable. These are non-visible authoring metadata for future agentic mutation. Do not leave values such as `auto`, `TODO`, or `TBD`.
  For gh-aw grader metrics, preserve the exact metric ID, native value, unit, direction, ordering, and null semantics supplied by the database table. Do not normalize, clamp, reinterpret, or combine unlike metrics.
9. Run the provided validator entry point against the complete document. Repair every reported error and rerun validation until it passes. If the intent cannot be represented with supported vocabulary, report that constraint instead of inventing syntax.
   Treat diagnostics as a correction loop: locate each reported path, check suggested identifiers against the actual source schema or allowed keys, repair the source of the error (not just downstream symptoms), and validate again. After structural validation, check the result against the original intent: source grain, evidence/status and link columns for actionable queues, null versus zero, and whether an empty or unavailable source is represented honestly. Passing validation alone does not prove these semantics.

Return only the validated complete Dashboard Language YAML document unless the user explicitly requests an explanation.

## Chart authoring boundary

Dashboard Language owns chart selection and data shaping. Use `facet`, `layer`, `resolve`, `treemap`, `encoding`, view `layout`, and page `sections` only as defined in Section 11.1 of the specification and the [chart reference](references/declarative-charts.md). These are bounded Vega-inspired features, not a general Vega or Vega-Lite interpreter.

Keep grouping, filtering, joins, aggregation, temporal bucketing, ordering, and limits in the query engine. Layers and treemaps require pre-shaped declarative queries; facets are partitioned by the worker after global ordering and limiting, never reconstructed by UI code. Every active view retains an abort-scoped query subscription so refreshed evidence updates the complete chart, and detachment releases rendering resources.

Validate complete documents through `validateDashboardDocument` in `dashboard/site/src/validator.js`, not isolated chart fragments. For maintained chart examples, use the production worker/query boundary as well as document validation; structural acceptance alone does not prove correct grouping, limits, units, or missing-data behavior.

## Declarative queries

When the intent needs a projection, a relationship between two tables or queries, or a derived measure that no table provides, declare it once under `dashboard.queries` (Dashboard Language Specification Section 5.5) and select the query by name from the views. Never add view-specific code, joins, or expressions.

Valid:

```yaml
queries:
  - name: workflow-aic-totals
    from: usage
    aggregate:
      by: [organization, repository, workflow]
      values:
        - { field: aic, as: aic, reducer: sum }
  - name: workflow-cost-inventory
    from: workflows
    joins:
      - source: workflow-aic-totals
        type: left
        on:
          - { left: workflow, right: workflow }
        fields:
          - { field: aic, as: observed-aic }
    compute:
      - as: total-aic
        function: coalesce
        args: [{ field: observed-aic }, { value: 0 }]
    select:
      - { field: workflow }
      - { field: total-aic, as: aic }
```

Invalid:

```yaml
queries:
  - name: workflow-cost-inventory
    from: workflows
    joins:
      - source: usage
        type: cross
        on: [{ left: workflow, right: workflow }]
        fields: [{ field: aic, as: aic }]
    compute:
      - as: cost
        function: eval
        args: [{ value: "aic * rate" }]
```

Invalid because `cross` is not a supported join type, `usage` has more than one row per join key, and `eval` is outside the closed computed-field vocabulary. Queries must reference only database tables or earlier queries, keep every output name unique, and reference only fields the preceding clauses produce.

Also invalid:

```yaml
queries:
  - name: workflow-totals
    from: workflow-inventory
  - name: workflow-inventory
    from: workflows
    joins:
      - source: workflow-totals
        on: []
        fields: [{ field: aic, as: aic }]
```

Invalid because the two queries form a dependency cycle, `workflow-totals` reads a query declared after it, and the keyless `on` would expand every row against every other row. Queries must form an acyclic graph in declaration order, and every join must declare at least one equality key pair.

Also invalid:

```yaml
queries:
  - name: workflow-activity
    from: runs
    joins:
      - source: workflows
        on: [{ left: workflow-link, right: workflow-link }]
        fields: [{ field: campaign, as: campaign }]
    aggregate:
      by: [workflow]
      values: [{ field: started-at, as: first-start, reducer: min }]
```

Invalid because the join keys and the `min` measure are incompatible with the source schema: `workflow-link` is a structured link field with no scalar value to compare, and `started-at` is a temporal field rather than a numeric measure. A query may project a link field, but must not filter, join, group, order, or compute with one, and must not read a field a presenter only derives after the query runs, such as `campaign-link`.

## Corpus procedure

When the working context requests a training-corpus example:

1. Read `corpus/index.json` first and open only examples relevant to the candidate task. Reject a duplicate intent, opportunity, metric, or view composition.
   Compare the candidate with existing examples on grain, failure modes, and view composition; prefer a case that exercises a gap rather than a cosmetic rewrite.
2. Add one metadata JSON file and its paired `.dashboard.yml` file under `corpus/examples/`, then add the entry to `corpus/index.json` in ascending `id` order.
3. Record the synthetic task, intent conditions, logical-source fixture, and relative dashboard path in metadata. Keep only Dashboard Language vocabulary in the dashboard file.
4. Do not claim observed attainment, fabricate repository evidence, or define evaluator semantics in dashboard metadata. Treat gh-aw grader records as authoritative inputs.
5. Run `npm --prefix dashboard/site run validate:corpus` from the repository root. Keep only candidates that pass dashboard-document and logical-source validation.

Corpus examples use `.dashboard.yml`; production dashboards may use strict JSON in `.json` files. Both serialize the same YAML 1.2 data model with the same canonical kebab-case vocabulary, types, defaults, validation rules, and semantics.
