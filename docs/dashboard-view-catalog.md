---
title: Dashboard view catalog
description: Find every standardized CAO dashboard experience, Dashboard Language page, view mark, chart, and named UI element.
---

This catalog helps dashboard builders choose an existing page, mark, chart, or
named UI element before creating something new. It describes the available
presentation vocabulary and the purpose of each option.

The executable vocabulary remains authoritative in
`dashboard/site/src/specification.js`; named element implementations are
registered in `dashboard/site/src/components/ui-elements.js`.

For a concrete ownership and responsive-layout example, see the [Overview component model](dashboard-overview-components.md).

## Primary product views

These are the default product destinations. They are custom pages composed from named elements so their data remains declarative while each specialized interaction has one DOM owner.

| Page ID | Navigation title | Named element | Purpose |
| --- | --- | --- | --- |
| `overview` | Overview | `factory-header`, `factory-floor`, `link-button-list` | Summarizes campaign health, repository coverage, weekly rhythm, and campaign shortcuts. |
| `configuration` | Settings | `configuration-policy` | Presents checked-in control policy and its editable settings surface. |
| `governance` | Governance | `environment-defaults` | Visible only to authorized hosted administrators; loads, previews, and explicitly approves gh-aw compiler default variables through authorized CLI actions. |
| `wizard` | Workflow wizard | `prompt-wizard` | Designs reviewed generation prompts for an Agentic Workflow or Campaign from pinned upstream patterns, without installing or activating work. |
| `value` | Value | Shared entity cards | Trophy-marked entry point for workflow results and their native evidence reports. |
| `workflow-value` | Workflow value detail | Shared entity cards | Internal detail opened from top-level Value collection cards; it is not a standard workflow tab. |
| `value-report` | Workflow value report | `workflow-value-report` | Connects consolidated intent, safe outputs, distinct accepted implementations and independent repository snapshots. |
| `value-snapshot` | Measured snapshot | `workflow-value-snapshot` | Opens the exact retained items counted at one observation. |

## Built-in pages

Built-in pages carry renderer-defined semantic requirements and required source contracts.

| Page | Purpose |
| --- | --- |
| `overview` | Cross-domain operational overview with availability and filter context. |
| `organizations` | Organization inventory and aggregate repository, workflow, run, and usage activity. |
| `repositories` | Repository activity, workflow coverage, failures, and usage. |
| `campaigns` | Installed campaign inventory, registration, modes, runs, and utilization. |
| `workflows` | Workflow inventory, role, rollout mode, activity, runs, and usage. |
| `runs` | Workflow run status, conclusion, model, engine, timing, and repository context. |
| `audits` | Ordered audit evidence linked directly to retained runs. |
| `experiments` | Experiment definitions and observed variants. |
| `graders` | Grader definitions and scored run observations. |
| `evals` | Evaluation definitions and run-level results. |
| `usage` | Token, AIC, estimated cost, model, engine, and scope usage. |
| `engines-models` | Model and engine utilization plus run aggregates. |
| `operational-value` | Campaign-defined repository metrics. |
| `findings` | Linked security and quality findings with status and severity. |
| `issues` | Reusable issue entity cards bound to safe-output queries with explicit drill behavior. |
| `cost` | Observed AI Credit cost across campaigns, repositories, and workflows. |
| `memory` | Browses repository memory published by centrally managed campaigns, with Raw and Table tabs for JSONL files. |
| `skills` (experimental) | Observed skill invocations and the workflows that invoked them. |
| `marketplace` | Read-only CAO campaign packages from the configured registries. See [Browse campaign packages](marketplace.md). |
| `indexing` | Dashboard and server-side collection health, Activity ingestion, and retained database transactions. |

JSONL memory files expose their top-level fields as table columns, with physical
line numbers and the shared sorting, filtering, and load-more controls. Nested
values remain JSON text; missing fields remain blank. Invalid records report
their line number without hiding them from the Raw view. This file viewer uses
a named element because memory-file schemas are discovered only after loading,
outside the canonical dashboard sources. Both Raw pretty-printing and table
parsing run exclusively in the data worker, including for hosted memory files.

## Declarative marks

| Mark | Purpose |
| --- | --- |
| `metric` | One summarized value with optional tone, icon, navigation, and number animation. |
| `table` | Structured rows with declared columns, formats, links, summaries, and actions. |
| `list` | Repeated records rendered as cards or issue-style rows. |
| `chart` | A declared graphical encoding rendered by one of the standard chart types. |
| `element` | A named reusable component for interaction or presentation beyond the generic marks. |
| `callout` | A concise labeled status or attention message. |

## Standard charts

| Chart | Purpose |
| --- | --- |
| `area` | Show quantitative change over an ordinal or bucketed temporal axis, optionally stacking non-negative contributions by categorical color. |
| `bar` | Compare quantitative values across categories. |
| `dot` | Show exact quantitative observations over time without connecting points; optionally encode horizontal quantitative reference lines. |
| `heatmap` | Show aggregated quantitative intensity across two categorical axes, with at most 100 cells and 12 categories per axis. |
| `histogram` | Show the distribution of quantitative samples using automatic bins after aggregation, ordering, and limiting; categorical `x` identifies each sample. |
| `horizontal-bar` | Compare up to 100 labeled quantitative values with labels on the left and bars aligned on the right. |
| `line` | Show quantitative change over a bucketed temporal axis, using categorical color series or two to eight quantitative `y` fields. |
| `pie` | Show a bounded part-to-whole composition. |
| `scatter` | Show exact quantitative observations at proportional timestamp positions without connecting points; `x` is temporal, not quantitative. |
| `swimlane` | Show observations over unbucketed time in categorical lanes, optionally weighted by observation count; contiguous observations may be coalesced without connecting gaps. |
| `treemap` | Show up to 100 rectangles with area proportional to positive finite values, optionally nested by one categorical group; use Vega-style squarify, binary, or slice-dice layouts. See [Treemap authoring](dashboard-language.md#show-a-treemap). |

Dot, line, and scatter charts require temporal `x`; heatmaps require categorical
`x` and `y`. Heatmaps, horizontal bars, and treemaps must declare `data.limit`
no greater than 100. Charts present graphics and legends, not row-level evidence
tables. Prefer horizontal bars for labeled rankings, pies for meaningful
part-to-whole composition, and treemaps for positive proportional contributions.
An average or rate is not an additive contribution merely because it is numeric.

## Chart composition and layout

These features compose existing charts; they are not additional chart types or
a general Vega/Vega-Lite interpreter.

| Feature | Purpose and supported boundary |
| --- | --- |
| `facet` | Repeat a chart over categorical subsets using one field or a row/column matrix. The worker partitions globally ordered and limited rows into at most 64 observed panels, each with independent scales. Single-field facets may wrap with view `columns` from 1 to 64. See [Faceted charts](dashboard-language-specification.md#faceted-charts-small-multiples). |
| `layer` | Overlay `area`, `bar`, `line`, `dot`, or layer-only `rule` marks over one already-shaped query source. Encodings inherit by channel and later leaves paint above earlier leaves. Supports at most eight leaves and four nesting levels; declare `layer` instead of top-level `chart`. |
| `resolve` | Select shared or independent quantitative y scales within a layered chart using `resolve.scale.y`. Independent x/color scales and facet-wide shared scales are not supported. |
| View `layout` | Request `full`, `full-view`, `half`, `third`, or `horizontal` placement. Hints may collapse responsively while preserving reading and focus order; they are not fixed dimensions. |
| Page section `layout` | Group views using `full`, `wide`, `narrow`, or `horizontal`. Horizontal sections wrap adjacent view boxes. Sections reference every page view exactly once in declaration order; do not add a section solely to frame a chart. |

Layer aggregation, time bucketing, ordering, and limits belong in
`dashboard.queries`, not individual layers. A layered chart may also declare a
top-level facet; each panel's layers share that panel's source. Facet panels are
chart graphics inside one view, not nested views. All active charts retain
abort-scoped worker query subscriptions and preserve explicit empty, partial,
and unavailable evidence states.

See [Custom pages](dashboard-language-specification.md#syntax-and-view-classes)
for the complete layer, encoding, layout, and validation contracts.

## Named UI elements

Named elements own specialized DOM, accessibility, interaction, local state, and cleanup. Their source shaping remains in Dashboard Language queries.

| Element | Purpose |
| --- | --- |
| `campaign-route` | Resolves and composes a route-selected campaign experience. |
| `workflow-route-page` | Composes a complete routed workflow page. |
| `outcome-detail` | Presents one outcome and its linked evidence. |
| `outcome-detail-section` | Presents a declared section within an outcome detail. |
| `problem-detail` | Presents one campaign runtime problem with its failure evidence, scope, environment, and repair action. |
| `entity-route` | Allocates a route-selected entity title and native GitHub link. |
| `configuration-policy` | Presents and edits checked-in CAO policy. |
| `environment-defaults` | Edits the full gh-aw defaults YAML through declared `gh aw env` actions, with scope selection, dry-run preview, explicit approval, and private temporary-file transport; does not query activity evidence or grant rollout authority. |
| `prompt-wizard` | Renders JSON-declared authoring steps and conditional fields, validates explicit choices, and uses the standard prompt button to review and copy an AW or Campaign generation prompt; has no operational data sources or command execution. |
| `measure-history` | Presents reusable grouped temporal-measure history from declarative query results. |
| `workflow-value-report` | Presents query-prepared workflow value, a linked snapshot plot and an evidence disclosure; owns only presentation, scoped reveal/focus and reactive cleanup. |
| `workflow-value-snapshot` | Presents exact immutable item links and distinguishes omitted item evidence from an explicitly empty population. |
| `factory-header` | Presents campaign status, retained-output context, work in motion, and weekly rhythm. |
| `factory-floor` | Presents linked repository, run, dispatch, and value stations. |
| `all-campaign-memory` | Browses every campaign repository-memory branch in place without route navigation. |
| `link-button-list` | Presents one source as an inset grouped list of Octicon navigation rows with disclosure chevrons. |
| `markdown` | Presents retained Markdown from a declared source field with safe repository-relative links. |
| `query-editor` | Authors ephemeral query-and-view previews through tool-free Copilot SDK sessions; available only in SDK-enabled canvases. |
| `command-terminal` | Replays and follows durable local CLI output by command ID, with explicit Cancel and Retry actions through the data worker; detachment never cancels execution. |
| `storage-info` | Identifies the configured dashboard storage engine and location without querying activity evidence; supports browser IndexedDB, local-server SQLite, server PostgreSQL, and unknown configuration. |

## Testing standard

The value report uses named elements because standard marks cannot compose its
scoped evidence-reveal buttons, focus preservation and exact-item snapshot
navigation into one compact report. Its counts, time periods, relationships,
coverage checks, direction and comparison labels remain Dashboard Language
queries in `dashboard-fragments/workflow-value.json`. The Value card and full
report share the same summary query and frozen evidence identity. The
first-to-last snapshot comparison is descriptive, not an alert policy or causal
credit; missing data never becomes zero.

Generic marks are covered by validator and presenter tests. Named elements require focused unit tests for states and cleanup, plus Playwright when behavior depends on browser layout, navigation, focus, scrolling, workers, or responsive interaction. The integrated local visual fixture is available at `?fixtures=1#page-overview` for Overview and equivalent page hashes for other destinations.