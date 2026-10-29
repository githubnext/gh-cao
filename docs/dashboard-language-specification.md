---
title: Dashboard Language Specification
description: A declarative YAML language for agentic workflow dashboards.
sidebar:
  order: 1362
---

**Version:** 0.1.0
**Status:** Working Draft
**Editor:** GitHub Agentic Workflows Team

---

## Abstract

This specification defines a small, declarative, YAML-based language for describing dashboards about organizations, repositories, centrally managed campaigns, agentic workflows, runs, experiments, graders, evals, usage, findings, and operational value. A dashboard contains built-in pages or custom pages. Custom pages use a constrained Vega-inspired model composed of `data`, `mark`, and mark-specific configuration. This specification defines intrinsic domain semantics, aggregation and filtering rules, route-bound custom-page allocation, provenance and freshness requirements, explicit unavailable-data states, links, conformance, and compliance tests. It does not define data retrieval, implementation architecture, or rendering technology.

## Status of This Document

This document is a Working Draft and may be updated, replaced, or made obsolete. It is intended for review and implementation feedback and is not a final recommendation.

For an introduction and a small query example, start with the
[Dashboard Language guide](dashboard-language.md). Use this specification when
implementing a validator or presenter, authoring advanced dashboard documents,
or checking conformance requirements.

The GitHub Agentic Workflows Team maintains this document. Version numbers follow Semantic Versioning. Working Draft publication does not imply endorsement by any standards body.

Sections containing numbered requirements are normative. Examples, notes, rationales, and appendices identified as informative are non-normative unless stated otherwise.

<span id="table-of-contents"></span>

---

## 1. Introduction

### 1.1 Purpose

The Dashboard Language provides a portable vocabulary for defining what an agentic-operations dashboard communicates without prescribing how data is fetched, stored, cached, deployed, or rendered.

### 1.2 Scope

This specification covers:

- a single-document YAML format;
- built-in and custom dashboard pages;
- intrinsic agentic-operations entities and observations;
- dimensions, measures, aggregation, scope, time, and filters;
- declarative derived queries over database tables or earlier queries;
- constrained hash-query routing for custom pages;
- provenance, freshness, missing-data semantics, and links; and
- validation, conformance, and compliance testing.

This specification does not cover:

- arbitrary scripts, SQL text, general-purpose expressions, or content templates;
- plugins, themes, renderer details, or implementation architecture;
- deployment-level routing, fetching, authentication, caching, or storage;
- campaign or experiment management; or
- causal inference.

The canvas authoring surface additionally registers `query-editor`, an
interaction-only named element with `data.sources: []`. It is available only in
a Copilot canvas and is exempt from chart-first layout because it is an editor,
not an evidence view. Its form and generation lifecycle cannot be expressed by
data marks. Generated evidence previews use ordinary Dashboard Language queries
and data marks, validated and executed by the canonical data worker; the element
does not derive or query source rows.

The authorized local command surface also registers `command-terminal` with
`data.sources: []`. It is an interaction-only terminal, not an evidence view:
execution details, output attachment, cancellation and retry cross the data
worker's queue transport. Queue history, status filtering, ordering and counts
remain declarative queries over the registered operational sources. Published
dashboards neither enable this transport nor request queue data.

The `storage-info` element also declares `data.sources: []`. It presents deployment
configuration rather than activity evidence: browser-local IndexedDB, local-server
SQLite, or server PostgreSQL. Unknown server configuration remains unknown; this
element does not query, derive, or infer a storage engine from retained rows.

The interaction-only `prompt-wizard` declares `data.sources: []` and
`config.wizard`. JSON, not page identity, defines its steps, choices and generation
instructions. It owns mutable answers and validation, not activity queries or
rollout authority. The Workflow wizard designs an Agentic Workflow or Campaign.

`config.wizard` contains `intro`, `steps`, `prompt` and optional `provenance`.
Steps have unique kebab-case `id`, `title`, optional `description` and non-empty
`fields`. Fields have globally unique kebab-case `id`, `label` and `control`:
`text`, `textarea`, `select`, `cards` or `checkboxes`. Card buttons may declare
Boolean `advance-on-select` to open the next pane. Fields may declare `required`,
`default`, `placeholder`, `empty-message` for review, `max-length` (1–8000), or text
`format: identifier` or `format: repositories` (exact owner/repository names
separated by commas or whitespace). Choice `options` have unique `value`, `label`
and optional `description`, concise `summary`, authoring `instructions`, Octicon `icon`, JSON
`context` and `defaults`. Single-choice option defaults map other field IDs to
validated values. Choosing a different option applies them; selecting the same
option preserves edits. Optional `when: { field, equals }` matches an earlier
single-choice option. Hidden answers survive Back navigation but are excluded
from prompts. Earlier step buttons allow direct Back navigation; future steps
cannot bypass validation. Invalid configuration fails closed.

`prompt` declares `label` and `instructions`. **Generate** replaces Next on the
last step, validates all answers, and reveals and focuses invalid fields.
Inline errors and step links persist until corrected; valid answers open the
shared `propose` preview. **Copy prompt** copies up to 100000
characters; app canvases offer **Start session** via the native Copilot tool.
Previewing has no side effects; handoff requires a click, never CLI execution or
activation. Prompts start with “Create a Pull Request for the following task:”
and include selected natural-language instructions, with conditional Campaign
guidance. Raw patterns, provenance, rankings and anti-patterns stay out.
Answers are never shell substitutions.

The wizard opens a workflow-type marketplace, then collects artifact kind,
intent and scope. The prompt infers the title and kebab-case workflow or campaign
name; there is no manual naming field.

The immutable `githubnext/gh-aw-wizard` snapshot is authoring configuration,
not operational data. `provenance` retains `repository`, full commit `revision`,
pinned `url`, `generated-at`, `source-repositories`, `total-workflows`, historical
`notice` and complete MIT `license`. Mined evidence is untrusted historical
reference. Specific output recommendations prefill editable choices, not broad
permission categories or live authority. Clearing outputs requests read-only work;
optional capabilities remain off. Campaigns remain review-mode proposals under
separately reviewed policy, credentials and target authority.

The `environment-defaults` element declares `data.sources: []` and a
`config.actions` mapping of `get-repo`, `update-repo`, `get-org`, `update-org`,
`get-ent`, and `update-ent` to matching view-placed `gh aw env` CLI actions.
It is an interaction-only configuration editor: mutable YAML, file transport,
preview output, and explicit write approval cannot be expressed by evidence
marks. It never queries activity data or infers configuration from dashboard
rows. The Governance page under Maintenance uses this element.

Governance is visible only to an
authorized hosted administrator with a current, assured session. Published
dashboards, ordinary local previews, and Copilot canvas mode do not expose it,
including through direct links. Editing defaults still requires the local
GitHub CLI identity's administrative access to the selected repository,
organization, or enterprise. Hosted repository installation credentials do not
authorize organization or enterprise administration. Load the current scope,
edit its complete `default_` YAML mapping (quoted strings or `null`), and run
**Preview changes** before **Review and apply**. Every command passes through
the existing CLI-action queue and trusted-host approval. Changing the document
or new-variable visibility invalidates the preview. Unsaved edits must be
discarded before changing the target.

These actions name `cao-governance.yml`. Local execution stages that file in a
private temporary directory, supplies update YAML from the approved queue input,
and returns downloaded YAML as stdout, separate from command diagnostics. It
never reads or overwrites a checkout file or interprets YAML as shell commands.
The installed gh-aw compiler owns the supported defaults registry; the editor
does not maintain a partial list. Unknown keys are rejected by gh-aw.

`gh aw env update` replaces the managed defaults at the selected scope:
omitted and `null` fields delete overrides. Organization and enterprise
variables can affect repositories outside CAO, and narrower overrides remain
effective. New variables may use `all` or `private` visibility; existing
visibility and selected-repository access lists are preserved. Reload after
applying to verify saved values, and recompile workflows to incorporate changed
compiler defaults. This edits non-secret Actions variables, not CAO rollout
policy, credentials, or live authority.

### 1.3 Design Goals

The language is designed to be minimal, deterministic, auditable, and safe to validate. Built-in pages provide useful defaults. Custom pages provide only metric, table, chart, and time-series views.

### 1.4 Basis and Domain Additions

The built-in page requirements are grounded in reviewed Central Agentic Ops surfaces: an overview organized by runtime, security and controls, value and outcomes, episodes and autonomy, cost and efficiency, and evidence quality; rollout-mode filtering; repository and workflow inventory; campaign AIC utilization; campaign-run trends; run status and conclusion trends and counts; repository and workflow rankings; largest AIC spenders; linked findings; operational-value timelines; explicit provenance and freshness; and empty or unavailable states.

Engine, engine-version, requested-model, and resolved-model dimensions are GitHub Agentic Workflows domain requirements. Central Agentic Ops surfaces them where retained telemetry or durable-output provenance makes them available, and reports `unknown` rather than inferring missing values.

---

## 2. Conformance

### 2.1 Requirements Notation

> The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in [RFC 2119](https://www.ietf.org/rfc/rfc2119.txt).

### 2.2 Conformance Classes

This specification defines three conformance classes:

1. **Dashboard document:** one YAML document claiming this language version.
2. **Validator:** parses a dashboard document and reports validity.
3. **Presenter:** consumes a valid document and conforming logical data to expose the specified information.

### 2.3 Normative Conformance Requirements

- **DLS-CONF-001:** A conformance claim **MUST** identify its class, specification version, implementation version when applicable, and test-suite result.
- **DLS-CONF-002:** A conforming dashboard document **MUST** satisfy all `DLS-DOC-*` requirements.
- **DLS-CONF-003:** A conforming validator **MUST** enforce all `DLS-DOC-*`, `DLS-VAL-*`, and parser-applicable `DLS-SAFE-*` requirements.
- **DLS-CONF-004:** A conforming presenter **MUST** satisfy all `DLS-SEM-*`, `DLS-CTX-*`, `DLS-AGG-*`, `DLS-DATA-*`, `DLS-LINK-*`, `DLS-PAGE-*`, `DLS-VIEW-*`, presenter-applicable `DLS-SAFE-*`, and `DLS-TEST-*` requirements.
- **DLS-CONF-005:** A non-conforming implementation **MAY** document supported subsets but **MUST NOT** claim conformance to this specification.

---

## 3. Terminology and Conceptual Model

### 3.1 Terms

| Term | Meaning |
|---|---|
| Dashboard | One named collection of ordered pages and shared defaults. |
| Site-wide callout | One dashboard-level text notice shown independently of the active page and dismissible for the lifetime of the loaded document. |
| Built-in page | A page whose semantic content is defined by Section 10. |
| Custom page | A page containing one or more declarative views. |
| Campaign | A repository-scoped group containing one centrally managed orchestrator workflow and one or more worker workflows. |
| Workflow role | A workflow's role as `orchestrator`, `worker`, or `standalone`; standalone workflows do not belong to a campaign. |
| Campaign AIC allowance | The sum of configured per-run AI Credit limits for one complete campaign attempt. |
| Dimension | A categorical, identifying, or temporal value used to group or filter observations. |
| Measure | A numeric observation that may be aggregated only according to its declared semantics. |
| Observation | A recorded value with time, provenance, and data-quality metadata. |
| Raw token | A provider-reported token count in one token class; not a currency or normalized cost. |
| AI Credits (`aic`) | A normalized usage or accounting measure supplied by an authoritative source; not a token count. |
| Run conclusion | The terminal GitHub Actions result of a completed run. |
| Outcome | A later repository-state evaluation of a safe output, distinct from run status and conclusion. |
| Operational value | A campaign-defined, timestamped numeric metric for one repository and campaign. |
| Operational grader | An ordered set of native finite numeric metrics or `null` published by gh-aw for one run; the first metric is primary and later metrics are diagnostics. |

### 3.2 Entity Relationships

```text
organization
  └─ repository
       ├─ operational-value observation → campaign
       ├─ campaign
       │    ├─ orchestrator workflow
       │    └─ worker workflow
       └─ standalone workflow
            └─ run
                 ├─ experiment assignment
                 ├─ usage observations
                 ├─ grader observations
                 ├─ eval observations
                 ├─ outcome observations
                 ├─ findings
                 └─ operational-grader observations
```

Every workflow role may have runs and their associated observations; the diagram expands that relationship once for brevity. Graders and evals are definitions. Grader observations and eval observations are records produced using those definitions. An experiment assignment associates one run with one named variant, but this language does not manage experiments.

### 3.3 Normative Semantic Foundations

- **DLS-SEM-001:** An implementation **MUST** model an organization as the parent scope of zero or more repositories.
- **DLS-SEM-002:** An implementation **MUST** model a repository as belonging to exactly one organization and as containing zero or more workflows.
- **DLS-SEM-003:** An implementation **MUST** model a workflow as belonging to exactly one repository and a run as an execution of exactly one workflow.
- **DLS-SEM-004:** Workflow active state **MUST** use `true`, `false`, or `unknown`; `unknown` **MUST NOT** be treated as either Boolean value.
- **DLS-SEM-005:** Run lifecycle status **MUST** use `queued`, `in-progress`, `completed`, or `unknown`; upstream `in_progress` **MUST** normalize to `in-progress`.
- **DLS-SEM-006:** Run conclusion **MUST** use `success`, `failure`, `cancelled`, `timed-out`, `action-required`, `neutral`, `skipped`, `stale`, `startup-failure`, or `unknown`; upstream underscore-separated values **MUST** normalize to kebab-case. A non-completed run **MUST** have conclusion `unknown`.
- **DLS-SEM-007:** An experiment assignment **MUST** identify an experiment, variant, and run; absence of an assignment **MUST NOT** imply membership in a control or treatment group.
- **DLS-SEM-008:** A grader observation **MUST** identify its grader, observed subject, observation time, `value`, and `status`; status **MUST** use `pass`, `fail`, `error`, or `unavailable`. It **MUST NOT** be represented as an eval observation.
- **DLS-SEM-009:** An eval observation **MUST** identify its eval, observed subject, observation time, and BinEval result of `YES`, `NO`, or `UNKNOWN`; it **MUST NOT** be represented as a grader observation.
- **DLS-SEM-010:** A usage observation **MUST** retain raw `input-tokens`, `output-tokens`, `cache-read-tokens`, `cache-write-tokens`, and `reasoning-tokens` as separate measures and **MUST NOT** label any of them as `aic`.
- **DLS-SEM-011:** An AIC observation **MUST** be represented by `aic` and **MUST NOT** be inferred from raw tokens unless the data provenance identifies an authoritative conversion.
- **DLS-SEM-012:** A run-associated usage observation **MUST** preserve `engine`, `requested-model`, and `resolved-model` as distinct dimensions; an unavailable value **MUST** be `unknown`.
- **DLS-SEM-013:** An operational-grader observation **MUST** preserve the ordered metrics published by gh-aw. Each metric **MUST** retain its identifier and native finite numeric value or `null`; the first metric is primary and later metrics are diagnostics. The grader's declared name, unit, and direction **MUST** remain associated with the primary metric.
- **DLS-SEM-014:** An implementation **MUST NOT** present experiment, grader, operational-grader, eval, usage, outcome, finding, or operational-value associations as causal conclusions.
- **DLS-SEM-015:** An outcome observation **MUST** identify its safe output and use `accepted`, `rejected`, `ignored`, `pending`, `lifecycle`, or `lifecycle-close`; upstream `lifecycle_close` **MUST** normalize to `lifecycle-close`. It **MUST NOT** be represented as a run conclusion.
- **DLS-SEM-016:** A dashboard **MUST NOT** normalize, clamp, rescale, replay, mature, or infer a baseline for operational-grader metrics. It **MUST NOT** replace `null` with zero.

---

## 4. YAML Document Model

### 4.1 Root Structure

The media type is not assigned by this specification. Files conventionally use `.yaml` or `.yml`.

```yaml
language-version: "0.1.0"
dashboard:
  id: example-dashboard
  title: Example Dashboard
  github-url-base: https://github.com
  callouts:
    - id: maintenance-notice
      title: Scheduled maintenance
      description: Dashboard data will not refresh between 02:00 and 03:00 UTC.
      icon: alert
  horizon:
    label: Horizon
    tooltip:
      label: Horizon details
      description: Dashboard data is included from the start time up to, but not including, the end time.
      icon: question
  defaults:
    scope: {}
    time: {}
    filters: {}
  pages: []
```

### 4.2 Vocabulary

Language keys and enumerated values use canonical kebab-case. Human-readable titles and descriptions are unrestricted Unicode strings. Domain identifiers such as `owner/repository` and workflow paths retain their domain syntax.

| Mapping | Allowed keys |
|---|---|
| Root | `language-version`, `dashboard` |
| `dashboard` | `id`, `title`, `description`, `github-url-base`, `repository`, `callouts`, `cli-actions`, `horizon`, `data-status`, `defaults`, `units`, `queries`, `card-templates`, `views`, `pages`, `navigation` |
| Card template | `id`, `icon`, `icon-field`, `title`, `labels`, `details`, `drill` |
| Site-wide callout | `id`, `title`, `description`, `icon`, `navigation-page`, `visible-when` |
| Callout `visible-when` | `source`, `field`, `equals`, `older-than-hours` |
| Dashboard `horizon` | `label`, `tooltip` |
| Dashboard CLI action | `id`, `label`, `description`, `icon`, `command`, `placement`, `arguments`, `selector` |
| Dashboard CLI action argument | `id`, `label`, `description`, `type`, `flag`, `default` |
| Tooltip | `label`, `description`, `icon` |
| `defaults` | `scope`, `time`, `filters` |
| Unit definition | `name`, `symbol`, `significant`, `format` |
| Query definition | `name`, `subject`, `objective`, `acceptance`, `description`, `parameters`, `from`, `union`, `time`, `joins`, `filter`, `compute`, `temporal-series`, `aggregate`, `predict`, `select`, `order-by`, `limit`, `facet` |
| Query `facet` | `field`, `row`, `column`, `as` |
| Chart facet field | `field`, `type`, `title`, `format` |
| Query parameter | `name`, `type` |
| Query `joins` entry | `source`, `type`, `on`, `fields` |
| Query join key | `left`, `right` |
| Query join field | `field`, `as` |
| Query `filter` | `predicates` |
| Query predicate | `field`, `equals`, `in`, `includes` |
| Query computed field | `as`, `function`, `args` |
| Query computed argument | exactly one of `field`, `value`, `context`, or `parameter`; `context` is `time-start` or `time-end` |
| Query `aggregate` | `by`, `values` |
| Query aggregate value | `field`, `as`, `reducer` |
| Query `predict` entry | `field`, `on`, `method`, `order`, `groupby`, `as` |
| Query `temporal-series` | `time`, `series`, `shape`, `link`, `carry`, `point-carry`, `measures`, `maps`, `trend` |
| Query `temporal-series.measures[]` | `field`, `key`, `kind` |
| Query `temporal-series.maps[]` | `field`, `definitions`, `group`, `kind` |
| Query `select` entry | `field`, `as` |
| Built-in page | `id`, `kind`, `page`, `title`, `navigation-label`, `navigation-indicator`, `description`, `intent`, `subject`, `objective`, `acceptance`, `icon`, `class-name`, `experimental`, `filter-bar`, `view-mode-control`, `pull-refresh`, `mode-indicator`, `query-binding`, `requires`, `form`, `definition` |
| Custom page | `id`, `kind`, `title`, `navigation-label`, `navigation-indicator`, `description`, `intent`, `subject`, `objective`, `acceptance`, `icon`, `class-name`, `experimental`, `filter-bar`, `view-mode-control`, `pull-refresh`, `mode-indicator`, `query-binding`, `requires`, `form`, `route`, `views`, `sections` |
| Page `requires` | `capability` |
| Page `form` | `title`, `description`, `update`, `fields` |
| Form `update` | `strategy`, `delay-ms` |
| Form field | `id`, `label`, `description`, `control`, `default`, `min`, `max`, `step`, `options` |
| Radio option | `value`, `label` |
| Navigation section | `label`, `pages`, `experimental`, `placement` |
| Page section | `id`, `title`, `description`, `layout`, `views`, `count-source`, `count-sources`, `count-field`, `count-label` |
| Custom page `route` | `hash-query-parameter`, `navigation-page`, `breadcrumbs`, `availability-view`, `availability-message`, `partial-message`, `title-format`, `tabs-class-name`, `tab`, `tabs` |
| View | `id`, `title`, `show-title`, `description`, `subject`, `objective`, `acceptance`, `locked`, `requires`, `data`, `mark`, `element`, `config`, `callout`, `chart`, `facet`, `columns`, `treemap`, `scatter`, `layer`, `resolve`, `metric`, `list`, `tree`, `layout`, `disclosure`, `controls`, `filter-bar`, `lazy-list`, `column-summaries`, `empty-message`, `title-link`, `encoding` |
| Scatter options | `cluster` |
| Chart layer | `chart`, `encoding`, `layer` |
| Layer resolution | `scale` with `y: shared` or `y: independent` |
| Treemap options | `method`, `ratio`, `padding` |
| View `filter-bar` | `filters` |
| View filter control | `id`, `label`, `groups` |
| View filter group | `label`, `field`, `source`, `value-field`, `label-field` |
| View `requires` | `backend`, `message`, `on-unavailable` |
| View `data` | `source` or `sources`, `partial-source`, `scope`, `time`, `filters`, `arguments`, `limit`, `order-by` |
| View data argument | `name`, `field` |
| View `config` | `body`, `sections`, `labels`, `measure-source`, `empty-message`, `actions` |
| View `list` | `style`, `layout`, `icon`, `action`, `action-context`, `actions`, `card`, `drill` |
| List `drill` | `type`, `field`, `page`, `query`, `title-field`, `arguments` |
| Query drill argument | `name`, `field` |
| Plural text variable | `singular`, `plural` |
| View `title-link` | `href-field`, `identifier-field` |
| Row action | `intent`, `presentation`, `icon`, `label`, `context`, `when` |
| Row action `when` | `field`, `equals` |
| Field definition | `field`, `type`, `aggregate`, `time-unit`, `title`, `as` (only when `aggregate` is not `none`), `display`, `format`, `unit` |

### 4.3 Normative Document Requirements

- **DLS-DOC-001:** A dashboard file **MUST** be valid YAML 1.2 and contain exactly one YAML document whose root is a mapping.
- **DLS-DOC-002:** The root **MUST** contain exactly `language-version` and `dashboard`.
- **DLS-DOC-003:** `language-version` **MUST** be the quoted string `"0.1.0"`.
- **DLS-DOC-004:** `dashboard` **MUST** contain a non-empty `id`, non-empty `title`, and non-empty `pages` sequence.
- **DLS-DOC-005:** Dashboard, page, and view IDs **MUST** match `^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$` and page IDs and view IDs **MUST** each be unique within their containing sequence.
- **DLS-DOC-006:** Language keys and enumerated values defined by this specification **MUST** use their exact canonical kebab-case spelling.
- **DLS-DOC-007:** A validator **MUST** reject unknown keys, unknown enumerated values, and duplicate mapping keys.
- **DLS-DOC-008:** `defaults`, when present, **MUST** be a mapping containing only `scope`, `time`, and `filters`.
- **DLS-DOC-009:** Every page **MUST** set `kind` to `built-in` or `custom` and satisfy the corresponding page shape in Sections 10 or 11.
- **DLS-DOC-009a:** `config`, when present, **MUST** appear only on `mark: element` views. Version 0.1.0 defines `config.body` only for the `workflow-route-page`, `campaign-route`, and `outcome-detail-section` elements. For `workflow-route-page`, it **MUST** be one of `insights`, `reports`, or `runs`. For `campaign-route`, it **MUST** be one of `overview`, `workflows`, `runs`, `issues`, `pull-requests`, `repositories`, `insights`, or `reports`; the legacy value `dispatches` **MUST** remain accepted as an alias for `runs`. A campaign route presenter **MUST** expose `overview`, `workflows`, and `runs` as the initial campaign-scoped resource navigation: horizontal tabs on desktop and touch-sized vertical navigation buttons on narrow mobile viewports. Every navigation target **MUST** retain the campaign route binding, and each selected page's views **MUST** apply that binding through the declarative query boundary. For `outcome-detail-section`, `config.body` **MUST** be `discussion` or `metadata`. A conforming presenter **MUST** derive companion navigation, route targets, labels, and icons for route-aware compositions from canonical body values instead of hard-coding page-specific identities in a view component. Version 0.1.0 defines `config.stations` for `factory-floor`; it **MUST** be a non-empty list of unique values selected from `campaigns`, `repositories`, `issues`, and `value-gains`, in the order those stations appear. Version 0.1.0 defines `config.labels` and `config.animate: number` for `factory-floor`; labels **MUST** be a non-empty mapping keyed by canonical kebab-case label identifiers whose values are plural text variables containing exactly the non-empty strings `singular` and `plural`. Version 0.1.0 defines `config.sources` for `factory-header` and `factory-floor`; it **MUST** be a non-empty mapping from canonical presentation role identifiers to declared source names, using only the roles `presentation` and `rhythm` for `factory-header`, and `campaigns`, `repositories`, `issues`, and `value-gains` for `factory-floor`. A role not overridden through `config.sources` **MUST** resolve to that element's default source name for the overview page. The bound sources **MUST** contain presentation-ready values; the presenter **MUST NOT** join source records, calculate operational metrics, or reconstruct business relationships. A conforming presenter **MUST** resolve every presentation role solely through `config.sources`, never through a hard-coded source name, so `factory-header` and `factory-floor` can be bound to differently named sources on any page. The generic `markdown` element **MUST** declare `config.content-field`; it **MAY** declare `config.path-field`, `config.base-link-field`, and `config.empty-message`. Relative links **MUST** resolve only beneath the safe HTTPS repository identified by the configured relation-specific link field.
- **DLS-DOC-010:** Titles and descriptions **MUST** be strings; IDs, references, and timestamps **MUST NOT** rely on YAML implicit type coercion.
- **DLS-DOC-011:** `github-url-base`, when present, **MUST** be an absolute HTTPS URL without credentials, query, or fragment. It identifies the GitHub web URL base used to resolve GitHub-addressable entity links and defaults to `https://github.com`.
- **DLS-DOC-012:** `repository`, when present, **MUST** be a non-empty `owner/repo` slug identifying the GitHub repository hosting the dashboard. A presenter **MUST NOT** fabricate a report action toolbar's GitHub repository link when `repository` is absent.
- **DLS-DOC-013:** `units`, when present, **MUST** be a non-empty mapping keyed by unique canonical identifiers. Each value **MUST** contain the non-empty string `name`, the non-empty string `symbol`, and the finite positive number `significant`, and **MAY** contain `format`.
- **DLS-DOC-014:** A tooltip **MUST** contain exactly the non-empty human-readable strings `label` and `description` and **MAY** contain one canonical Octicon `icon`. A presenter **MUST** expose a tooltip as a keyboard-focusable help control named by `label`, associate its explanatory content with the control, and make that content available on both pointer hover and keyboard focus. `horizon`, when present, **MUST** contain exactly a non-empty human-readable `label` and one `tooltip`; the presenter **MUST** expose an icon-only clock control whose accessible name includes the horizon label and resolved duration. Pointer hover and keyboard focus **MUST** disclose the resolved duration. Activating the control **MUST** expose a compact horizon picker and a keyboard-accessible coverage disclosure containing the configured description with the precise resolved start, exclusive end, and duration.
- **DLS-DOC-014a:** A `mark: list` view **MUST** declare `list.style` as `cards` or `issues`, and **MUST** declare a canonical Octicon `list.icon`. `list.action`, when present, **MUST** reference a view-placed CLI action. `encoding.href`, when present on a list view, **MUST** reference one relation-specific link field and binds the primary card title to that link.
- **DLS-DOC-015:** `callouts`, when present, **MUST** be a non-empty sequence of mappings with unique canonical `id` values and non-empty `title` and `description` strings. A callout **MAY** contain one canonical Octicon `icon` and one `navigation-page` referencing a declared dashboard page. `visible-when`, when present, **MUST** contain exactly one canonical `source`, one `field` declared by that source, and either one scalar `equals` value or one positive finite `older-than-hours` value, but not both. Equality matches at least one source row. Age visibility matches at least one valid ISO timestamp or finite Unix-millisecond value whose age is at least the declared hours, using the current wall clock independently of page filters. Null, invalid, future, missing, or unavailable evidence **MUST NOT** establish staleness. The presenter **MUST** subscribe to declared callout sources for the dashboard lifetime and request worker reevaluation of clock-based visibility at least once per minute, without navigation. The data worker **MUST** execute declared visibility conditions in its query layer. The shared callout renderer **MUST** consume only the resulting visibility states; it **MUST NOT** inspect source rows or compare evidence timestamps on the main thread.

The default dashboard warns after 24 hours without new retained workflow activity,
using the newest upstream update timestamp (or creation timestamp when absent),
not the browser refresh or shard import time. The warning links to Ingestion,
whose shard receipts expose source paths, import status and time, record counts,
and payload hashes. Inactivity is a reason to inspect ingestion, not proof of a
collection failure; absent shard receipts leave coverage unknown.

Callout query definitions and their dependencies remain in the core dashboard
document even when page views are lazy-loaded. Startup resolves their bounded
results before the first dashboard render so an existing stale-data warning
does not appear late and shift the active page; subsequent results stay subscribed.

- **DLS-DOC-016:** A navigation section **MUST** contain a non-empty `pages` sequence referencing declared page IDs. An optional `label` **MUST** be a non-empty string. `experimental`, when present, **MUST** be Boolean and defaults to `false`. `placement`, when present, **MUST** be `bottom` and the section **MUST** declare a non-empty `label`; it anchors the section after standard navigation sections. A presenter **MUST** render an unlabeled navigation section's pages directly without section chrome.

---

## 5. Intrinsic Semantic Model

### 5.1 Database Tables and Grain

Sources are closed: tables below, daily projections (§5.1.2),
[statistical snapshots](../specs/dashboard-data.md#511-independent-statistical-run-projection),
and runtime sources (§5.4). Bind sources or declared queries with `data.source`.

| Table | One row represents | Core fields |
|---|---|---|
| `organizations` | organization | `organization`, `organization-name`, `observed-at`, `organization-link` |
| `campaigns` | installed starter campaign | `id`, `campaign`, `campaign-name`, `campaign-description`, `campaign-icon`, `campaign-version`, `campaign-current-version`, `campaign-update-state`, `observed-at`, `campaign-link` |
| `repositories` | repository | `id`, `organization`, `repository`, `repository-name`, `rollout-mode`, `observed-at`, `organization-link`, `repository-link` |
| `workflows` | workflow | `id`, `organization`, `repository`, optional `campaign` and `campaign-name`, `workflow`, `workflow-name`, `workflow-role`, `workflow-active`, `gh-aw-version`, `gh-aw-current-version`, `gh-aw-version-label`, `gh-aw-update-state`, complete `gh-aw-metadata` and `gh-aw-manifest` JSON payloads, `rollout-mode`, `max-ai-credits`, `campaign-aic-allowance`, `campaign-worker-count`, `campaign-inventory-warnings`, `inventory-ready`, `observed-at`, `organization-link`, `repository-link`, `workflow-link` |
| `work-items` | stable delegated work item | `work-item-id`, `name`, `objective`, scope IDs, `workflow-name`, `workflow-icon`, `scope`, `domain`, `work-type`, `lifecycle-state`, `phase`, `reason`, `reason-evidence-class`, `next-action`, `next-actor`, `waiting-on`, `waiting-since`, `owner`, `consequence-tier`, `verification-state`, `outcome-state`, `started-at`, `ended-at`, `observed-at`, `evidence-link`, `repository-link`, `run-link` |
| `agent-assignments` | agent assignment to a work item | `assignment-id`, `agent-id`, `agent-name`, `agent-icon`, `agent-description`, `permissions`, `agent-state`, `work-item-id`, `objective`, `assignment-state`, `handoff-state`, `dependency-state`, `conflict-state`, `run-count`, `total-runtime-seconds`, `last-observed-at`, `long-running`, `stale`, `observed-at`, `evidence-link`, `repository-link`, `run-link` |
| `agent-smells` | evidence-backed smell observation for one workflow or run | `smell-observation-id`, `smell-id`, `smell-name`, `smell-category`, `smell-severity`, `smell-summary`, `smell-evidence`, `smell-recommendation`, scope IDs, `run`, `observed-at`, `evidence-link`, `repository-link`, `workflow-link`, `run-link` |
| `workflow-smells` | deterministic workflow configuration smell | smell fields, scope IDs, `run`, `observed-at`, `evidence-link`, `repository-link`, `workflow-link`, `run-link` |
| `security-findings` | detected unsafe or untrusted workflow behavior | smell fields, scope IDs, `run`, `observed-at`, `evidence-link`, `repository-link`, `workflow-link`, `run-link` |
| `control-plane-smells` | control policy, rollout, or campaign inventory smell | smell fields, scope IDs, `run`, `observed-at`, `evidence-link`, `repository-link`, `workflow-link`, `run-link` |
| `runs` | run | `id`, `organization`, `repository`, `workflow`, `run`, `started-at`, `ended-at`, `run-status`, `run-conclusion`, `failure-job`, `failure-message`, `failure-step`, `failure-detail`, `rollout-mode`, `engine`, `engine-version`, `requested-model`, `resolved-model`, `organization-link`, `repository-link`, `workflow-link`, `run-link` |
| `run-performance` | completed workflow run | `organization`, `repository`, `workflow`, `run`, `started-at`, `run-conclusion`, `rollout-mode`, `run-duration-seconds`, `sandbox-runtime`, `engine`, `model`, `run-link` |
| `job-performance` | workflow job | `organization`, `repository`, `workflow`, `run`, `started-at`, `run-conclusion`, `rollout-mode`, `job`, `job-status`, `job-conclusion`, `job-duration-seconds`, `runner`, `runner-name`, `runner-group`, `sandbox-runtime`, `engine`, `model`, `run-link` |
| `experiments` | experiment | `experiment`, `experiment-name`, `observed-at` |
| `experiment-assignments` | experiment assignment | scope IDs, `run`, `experiment`, `variant`, `observed-at` |
| `graders` | grader definition | `grader`, `grader-name`, `observed-at` |
| `grader-observations` | grader observation | scope IDs, `run`, `experiment`, `grader`, `value`, `status`, `role`, `unit`, `direction`, `rollout-mode`, `observed-at`, `run-link` |
| `evals` | eval definition | `eval`, `eval-name`, `eval-question`, `requested-model`, `observed-at` |
| `eval-observations` | eval observation | scope IDs, `run`, `experiment`, `eval`, `eval-result`, `requested-model`, `resolved-model`, `rollout-mode`, `observed-at` |
| `usage` | model invocation | scope IDs, `run`, `invocation`, `engine`, `engine-version`, `requested-model`, `resolved-model`, `rollout-mode`, `input-tokens`, `output-tokens`, `cache-read-tokens`, `cache-write-tokens`, `reasoning-tokens`, `aic`, `estimated-usd`, `observed-at`, `organization-link`, `repository-link`, `workflow-link`, `run-link` |
| `firewall-observations` | one run-level destination decision or explicit evidence-state observation | scope IDs, `run`, `firewall-observation`, `run-conclusion`, `rollout-mode`, run-level agent, engine, model, and runtime fields, `observed-at`, `firewall-expected`, `firewall-enabled`, `firewall-evidence-available`, `evidence-state`, `evidence-completeness`, `evidence-freshness`, evidence and requested horizon fields, `evidence-coverage-percent`, `last-successful-collection-at`, `gh-aw-firewall-version`, policy provenance fields, `domain`, `host`, `port`, `protocol`, `decision`, `request-count`, policy-rule attribution fields, baseline and drift fields, `review-state`, `review-priority`, `run-link`, `evidence-link` |
| `firewall-policy-rules` | one effective firewall policy rule and domain pattern for one run | scope IDs, `run`, run-level agent, engine, model, and runtime fields, `observed-at`, `rule-id`, `rule-order`, `action`, `protocol`, `domain-pattern`, `description`, `hit-count`, `ssl-bump-enabled`, `dlp-enabled`, `host-access-enabled`, `policy-source`, `policy-manifest-identity`, `run-link`, `evidence-link` |
| `model-usage-summary` | model usage summary | `model`, `resolved-model`, `engine`, `requested-model`, `runs`, `invocations`, `total-aic`, `estimated-usd`, `pricing` |
| `engine-usage-summary` | agentic engine usage summary | `engine`, `runs`, `invocations`, `total-aic`, `estimated-usd`, `min-engine-version`, `max-engine-version`, `models` |
| `outcomes` | safe-output outcome observation | scope IDs, `campaign`, `runtime-repository`, `workflow-name`, `run`, `run-conclusion`, `safe-output`, `outcome-number`, `outcome-title`, `outcome-summary`, `outcome-body-html`, `outcome-category`, `outcome-status`, `outcome-state`, `evidence-strength`, `rollout-mode`, `engine`, `engine-version`, `requested-model`, `resolved-model`, `published-at`, `observed-at`, `issue-link`, `pull-request-link`, `run-link`, `external-link`, `organization-link`, `repository-link`, `workflow-link` |
| `findings` | finding | scope IDs, `run`, `finding`, `finding-severity`, `finding-status`, `finding-summary`, `observed-at`, `engine`, `engine-version`, `requested-model`, `resolved-model`, `issue-link`, `pull-request-link`, `run-link`, `external-link`, `organization-link`, `repository-link`, `workflow-link` |
| `operational-values` | Campaign-defined repository metric | repository and campaign scope IDs, `operational-value`, `operational-value-definition`, `operational-value-role`, `operational-value-name`, `operational-value-direction`, `maturity-status`, `observed-at`, `organization-link`, `repository-link`, `campaign-link`, optional `run-link` for run-attributed observations |
| `token-efficiency-opportunities` | one stable frozen token-efficiency opportunity | scope IDs, `opportunity-id`, `opportunity-kind`, `assignment-run`, `experiment`, `evidence-window-start`, `evidence-window-end`, `evidence-state`, `evidence-confidence`, `cost-grain`, `observed-at`, `evidence-link`, `repository-link`, `workflow-link`, `run-link` |
| `token-efficiency-interventions` | one append-only lifecycle observation for one token-efficiency intervention | scope IDs, `opportunity-id`, `intervention-id`, `lifecycle-observation-id`, `previous-intervention-state`, `intervention-state`, `previous-recommendation-disposition`, `recommendation-disposition`, `supersedes-intervention-id`, `superseded-by-intervention-id`, `recommendation-churn-count`, `recommendation-churn-rate`, `experiment`, `control-variant`, `optimized-variant`, `proposed-savings-aic`, `evidence-state`, `missing-reason`, `safe-output-id`, `implementation-change-id`, `implementation-run-ids`, `accepted-at`, `implementation-started-at`, `implementation-completed-at`, `rejected-at`, `superseded-at`, `observed-at`, `issue-link`, `pull-request-link`, `evidence-link`, `repository-link`, `workflow-link`, `run-link` |
| `token-efficiency-comparisons` | one matured or pending comparison for one intervention and evidence cutoff | scope IDs, `opportunity-id`, `intervention-id`, `comparison-id`, `experiment`, `evaluator-digest`, `evidence-state`, `cost-grain`, `baseline-aic-per-accepted-outcome`, `optimized-aic-per-accepted-outcome`, `accepted-target-outcome-count`, `baseline-failure-rate`, `optimized-failure-rate`, `outcome-quality-preserved`, `gross-realized-savings-aic`, `optimization-overhead-aic`, `net-realized-savings-aic`, `verified-net-gain`, `maturity-at`, `evidence-cutoff`, `observed-at`, `evidence-link`, `repository-link`, `workflow-link`, `run-link` |
| `github-api-rate-limits` | one observation of one GitHub API quota resource at one checkpoint for one credential | `observation-id`, `operation-execution-id`, `observed-at`, `credential`, `credential-type`, `resource`, `bucket`, `maximum-lane`, `history-series`, `remaining`, `limit`, `used`, `remaining-percent`, `reset-at`, `minutes-to-reset`, `consumed-since-previous`, `burn-rate-per-minute`, `projected-remaining-at-reset`, `projected-exhaustion-at`, `runway-ratio`, `risk-status`, `risk-order`, `is-current`, `operation`, `phase`, `outcome`, `attribution-status`, `operation-consumed` |
| `github-api-collector-health` | one GitHub API telemetry collection checkpoint | `operation-execution-id`, `observed-at`, `credential`, `operation`, `phase`, `outcome`, `cache-hydrated`, `cache-bytes`, `cache-entries`, `cache-folders`, `rate-limit-error` |
| `github-api-call-stacks` | one JavaScript stack frame from one GitHub API telemetry collection checkpoint | `operation-execution-id`, `observed-at`, `credential`, `operation`, `phase`, `outcome`, `stack-frame-id`, `stack-parent-id`, `stack-depth`, `stack-frame` |

“Scope IDs” means the applicable `organization`, `repository`, and `workflow` fields. Fields that do not apply to an observation are absent rather than fabricated. Link-bearing source fields are relation-specific optional fields whose intrinsic type is one Section 9.1 link object. `organization-link`, `repository-link`, `workflow-link`, `issue-link`, `pull-request-link`, `run-link`, `evidence-link`, and `external-link` correspond to the `organization`, `repository`, `workflow`, `issue`, `pull-request`, `run`, `evidence`, and `external` link relations, respectively; a source row MUST NOT encode multiple link relations inside one field.

For `outcomes`, `repository` identifies the target repository that owns the durable safe output, while `runtime-repository` identifies the repository where the attributed workflow ran.

#### 5.1.1 Smell Classification

A smell is an evidence-backed warning signal, not a synonym for every unhealthy state and not necessarily a defect. The source identifies what the finding is about:

| Source | Classification boundary | Evidence authority |
|---|---|---|
| `agent-smells` | Agent execution behavior, control quality, reducibility, or resource choice | Native `gh aw audit` agentic assessments |
| `workflow-smells` | Static workflow configuration or supply-chain defects | Deterministic inspection of compiled workflow metadata and manifests |
| `security-findings` | Observed unsafe or untrusted behavior | Threat-detection verdicts |
| `control-plane-smells` | Control policy, campaign inventory, rollout, or governance defects | Deterministic control policy and installed-campaign validation |

Agent smells and security findings are intentionally distinct. An agent smell indicates questionable efficiency, reducibility, model choice, or behavioral control; it does not assert compromise or exploitability. A security finding records an observed threat verdict and requires security review. The same workflow may have both without either finding implying the other.

The following smell IDs are currently emitted:

| Source | `smell-id` | Meaning | Category | Severity |
|---|---|---|---|---|
| `agent-smells` | `overkill-for-agentic` | Deterministic automation would be simpler than an agentic run. | `design` | Preserved from audit |
| `agent-smells` | `resource-heavy-for-domain` | Turns, tools, duration, or writes are excessive for the inferred task domain. | `cost-memory-and-value` | Preserved from audit |
| `agent-smells` | `poor-agentic-control` | Exploration, failures, missing evidence, or writes indicate weak behavioral control. | `design` | Preserved from audit |
| `agent-smells` | `partially-reducible` | A material share of data gathering can move to deterministic steps. | `design` | Preserved from audit |
| `agent-smells` | `model-downgrade-available` | A less expensive model is likely sufficient for the observed task. | `cost-memory-and-value` | Preserved from audit |
| `workflow-smells` | `strict-disabled` | Strict workflow validation is disabled, weakening fail-closed behavior. | `configuration` | `high` |
| `workflow-smells` | `unpinned-dependencies` | An action lacks a commit SHA or a container lacks an immutable digest. | `supply-chain` | `high` |
| `security-findings` | `threat-detection-prompt-injection` | Threat detection observed prompt-injection behavior. | `trust-and-security` | `high` |
| `security-findings` | `threat-detection-secret-leak` | Threat detection observed secret-leak behavior. | `trust-and-security` | `high` |
| `security-findings` | `threat-detection-malicious-patch` | Threat detection observed a malicious patch. | `trust-and-security` | `high` |
| `control-plane-smells` | `inventory-incomplete` | Declared campaign orchestration or worker inventory is missing. | `control-plane` | `high` |
| `control-plane-smells` | `policy-diagnostic` | Control policy validation emitted an error or warning requiring review. | `control-plane` | `high` for errors; `medium` for warnings |

All smell rows use `low`, `medium`, or `high` severity. `low` identifies an optimization opportunity with limited immediate consequence. `medium` identifies a material maintainability, cost, or governance concern. `high` identifies fail-open configuration, supply-chain exposure, detected unsafe behavior, or a control-plane defect that can invalidate safe operation. Severity controls Home attention priority but does not replace the source-specific meaning.

#### 5.1.2 Daily Run-Count Projection Source

`daily-run-counts` persists a projection, not an entity/runtime provider.
Contract: checked-out `specs/dashboard-data.md` §72.2
([online main](https://github.com/githubnext/gh-cao/blob/main/specs/dashboard-data.md#722-additive-daily-run-counts)).

Grain: namespace / UTC execution-start `day` / `organization` / `repository` /
`workflow`. Six nonnegative integer counters are sum-only, not bucket counts.
`total` equals five exclusive classes: `successful` (`success`), `failed`
(`failure`, `startup-failure`, `stale`, `timed-out`), `approval` (`action-required`),
`pending` (otherwise unclassified, nonempty noncompleted status), `other`
(remainder). Underscore aliases match. Attempts replace contributions;
pending is last-observed, success is not accepted value.

Only migrated additive queries use Organization/Repository/Workflow/whole UTC
days at start-day midnight. Unsupported dimensions (including rollout mode)
fail; costs, distinct counts, Run predicates and incidents stay on detail.
Joins preserve provenance/partial coverage. Unavailable is not empty/zero;
zero is not confirmed inactivity. No Run unions, detail fallback or invented days.

Buckets/receipts expire outside the current UTC Monday-start week and two prior
weeks, independent of detail overrides; expired replay cannot reopen them.
Bounded checkpointed backfill is partial and cannot recover evicted history.
No other persisted daily/metric/computation projection is allowed.

### 5.2 Raw Token Classes

The canonical raw-token measures are `input-tokens`, `output-tokens`, `cache-read-tokens`, `cache-write-tokens`, and `reasoning-tokens`. They remain separate because provider reporting conventions may overlap.

### 5.3 Campaigns, Graders, Evals, and Operational Value

A campaign groups one orchestrator and one or more workers that execute centrally managed operations. `max-ai-credits` is the configured per-run limit for one workflow; `campaign-aic-allowance` is the sum of those limits for one complete campaign attempt and is not actual usage. `campaign-inventory-warnings` is the campaign-level count of missing compiled orchestration and declared worker inventory. A grader applies a named grading criterion and produces a deterministic grader observation. An operational grader is the ordered native metric result published by gh-aw's `operational-value` protocol for one run. Operational value is a separate campaign-defined repository metric linked to its campaign. An eval is a binary evaluation question and produces a `yes`, `no`, or `unknown` observation; it may use an AI model. These concepts are not interchangeable.

### 5.4 Normative Source Requirements

The server may expose a bounded operational source through the existing
authorized query boundary. It is not a database table and is not stored in
browser IndexedDB. The registered sources are:

| Source | One row represents | Fields |
|---|---|---|
| `collection-health` | the current administrator-only CAO webhook and collection health snapshot | `configured`, `health`, `health-revision`, `queue-depth`, `pending-tasks`, `oldest-pending-age`, `dead-letters`, `backfill`, `backfill-failures`, `backfill-queued-run-tasks`, `webhook-load`, `collection-load`, `failure-load`, `last-projected`, `last-webhook-at`, `last-failure-at`, `last-failure-code`, `last-success-at`, `webhook-received`, `webhook-duplicate`, `webhook-admission-failed`, `task-queued`, `task-coalesced`, `collection-succeeded`, `collection-failed`, `collection-retried`, `collection-dead-lettered` |
| `github-quota-usage` | the peak GitHub API quota usage of one bucket, or the limit-weighted aggregate of every bucket, during one 15-minute slot of the last 24 hours | `observed-at`, `scope`, `bucket`, `app`, `installation`, `resource`, `buckets`, `limit`, `used`, `reserved`, `usage-percent` |
| `webhook-status` | the administrator-only live webhook counters for the current server process | `configured`, `scope`, `received`, `in-flight`, `verified`, `accepted`, `rejected`, `failed`, `duplicates`, `pings`, `last-received-at`, `last-accepted-at`, `last-failure-at`, `last-ping-at` |
| `cli-commands` | one persistent command from the authorized local server's bounded, filtered history page | `id`, `actionId`, `command`, `status`, `createdAt`, `startedAt`, `completedAt`, `exitCode`, `error`, `retryOf`, `outputTruncated` |
| `cli-command-summary` | the authorized local queue's authoritative counts and admission state, independent of history pagination | `running`, `total`, `paused-reason`, `revision` |
| `cli-command-statuses` | one supported command lifecycle status, for declarative filter options | `status` |

The `collection-health` provider returns exactly one row for an authorized
request. The `github-quota-usage` provider returns one `aggregate` row per
observed slot and one `bucket` row per observed bucket and slot; `scope`
distinguishes them. Other clients receive an unavailable source, and the
providers store no raw error messages or credentials in the source. Runtime
sources are consumed only through a declared Dashboard Language query; view
code and UI elements do not fetch or derive this data independently.

The `webhook-status` provider reads a bounded in-memory snapshot, never GitHub,
collection queues or durable ingestion metrics. Counters reset on restart and
are not aggregated across replicas. `verified` counts valid HMAC signatures,
even if headers are subsequently rejected; `accepted` counts HTTP
acknowledgements, not completed reconciliation. Duplicates and signed ping
acknowledgements overlap accepted requests. Middleware rejections are excluded.
Missing timestamps remain absent, not fabricated observations. Neither
`configured` nor a signed ping proves current GitHub-side registration.
The active view refreshes every 30 seconds under an abort-scoped subscription,
even without canonical revision changes; results bypass the server query cache.

The `cli-*` providers are available only through the data worker in authorized
local admin or trusted canvas sessions and are never ingested into browser
IndexedDB. Their statuses are exactly `queued`, `running`, `completed`, `failed`,
`canceled`, and `timed-out`. Unavailable queue authority is not an empty queue.
Published dashboards **MUST NOT** request these providers.

- **DLS-SEM-016a:** `simulation-days` is a built-in synthetic query input, not
  a canonical table or provider source. It **MUST** yield exactly 30 rows with
  numeric `day` values from 1 through 30 in order and a temporal `date` field
  of ISO UTC midnight timestamps from `2025-01-01T00:00:00.000Z` through
  `2025-01-30T00:00:00.000Z` (a fixed, deterministic plotting axis).
  The query worker **MUST** materialize it without reading canonical storage,
  regardless of page identity or supplied logical sources. Queries **MAY**
  use `from: simulation-days` and derive additional fields through ordinary
  Dashboard Language clauses; a query **MUST NOT** shadow its name. Its rows
  have no observed operational provenance and **MUST NOT** be represented as
  measured activity.

- **DLS-SEM-017:** Views **MUST** use only §5.1 tables/projection, §5.4 sources, `simulation-days`, or declared queries. `metric`/`table`/`list`/`chart` `data.source` names exactly one; `element` `data.sources` lists unique sources, nonempty except for `query-editor`, `command-terminal`, `environment-defaults`, `prompt-wizard`, `storage-info`. Optional `data.route-field` **MUST** name a source field.
- **DLS-SEM-018:** Each database table **MUST** preserve the grain declared in Section 5.1; duplicated observations **MUST** retain distinct observation identifiers in provenance.
- **DLS-SEM-019:** A `usage` row **MUST** represent one model invocation and **MUST NOT** repeat invocation-level AIC across token-class rows.
- **DLS-SEM-020:** Grader values, operational-grader results, eval results, AIC, each raw-token measure, outcome states, and operational value **MUST** remain separately named throughout filtering, aggregation, and presentation.
- **DLS-SEM-021:** `rollout-mode` **MUST** use `review`, `live`, or `unknown`.
- **DLS-SEM-022:** `workflow-role` **MUST** use `orchestrator`, `worker`, or `standalone`. An orchestrator or worker workflow **MUST** identify its `campaign`; a standalone workflow **MUST NOT** identify a campaign.
- **DLS-SEM-023:** `max-ai-credits` and `campaign-aic-allowance`, when available, **MUST** be non-negative. `campaign-aic-allowance` **MUST** equal the sum of the campaign's available configured per-run workflow limits and **MUST NOT** be presented as actual AIC usage.
- **DLS-SEM-024:** A `github-api-rate-limits` row **MUST** preserve the grain declared in Section 5.1 and **MUST NOT** contain cache or collector-health fields. `remaining-percent` **MUST** equal `remaining / limit * 100`, and `used` **MUST** equal `limit - remaining` when GitHub does not supply it.
- **DLS-SEM-025:** Rate-limit burn and forecast fields **MUST** be materialized before dashboard evaluation. Calculations **MUST** partition observations by `credential`, `resource`, and `reset-at`; they **MUST NOT** cross reset boundaries. `risk-status` **MUST** be `critical`, `warning`, `healthy`, or `unknown`, with centralized and testable thresholds. Stale, partial, unavailable, or insufficient observations **MUST NOT** be presented with a fabricated healthy forecast.
- **DLS-SEM-026:** Operation consumption attribution **MUST** use paired `before` and `after` observations with the same stable `operation-execution-id`, credential, resource, and reset window. When this evidence is absent or inconsistent, `attribution-status` **MUST** be `unavailable` and `operation-consumed` **MUST** be null.
- **DLS-SEM-027:** `github-api-collector-health` **MUST** remain distinct from GitHub quota health. A credential identifier **MUST** be a non-secret operational alias or role and **MUST NOT** contain a token or credential value.
- **DLS-SEM-028:** A `github-api-call-stacks` row **MUST** preserve one captured JavaScript frame as text, identify its checkpoint, and use `stack-frame-id` and `stack-parent-id` to preserve call order without fabricating unavailable frames.
- **DLS-SEM-029:** Every smell row **MUST** identify exactly one source classification from Section 5.1.1 and **MUST** use `low`, `medium`, or `high` severity. Producers **MUST NOT** reclassify a security verdict as an agent smell or infer a security finding from cost, duration, or tool breadth.
- **DLS-SEM-030:** A smell **MUST** carry observed evidence or deterministic configuration evidence. Disabled, blocked, slow, stale, expensive, or unsuccessful state alone **MUST NOT** be classified as a smell.
- **DLS-SEM-031:** Native agentic assessment kind names **MUST** be normalized from snake case to canonical kebab-case `smell-id` values without changing their audit-provided severity, summary, evidence, or recommendation.
- **DLS-SEM-032:** The canonical Home attention view **MUST** preserve smell source classification, evidence attribution, severity, expected actor, and remediation when normalizing smell rows into attention signals.
- **DLS-SEM-033:** `opportunity-id`, `intervention-id`, and `comparison-id` **MUST** use the canonical identities in `specs/dashboard-data.md` Section 5.5. Display names and control-repository identity **MUST NOT** replace target Repository, Workflow, assignment Run, frozen window, or experiment identity.
- **DLS-SEM-034:** `evidence-state` for token-efficiency opportunity and comparison sources **MUST** be `complete`, `incomplete`, `incomparable`, `unmatured`, or `unavailable`. Only `complete` comparisons **MAY** contain non-null gross, overhead, net, or verified-net measures.
- **DLS-SEM-035:** `opportunity-kind`, `intervention-state`, and `recommendation-disposition` **MUST** use the closed vocabularies in `specs/dashboard-data.md` Section 5.5.4. High AIC alone **MUST NOT** create an opportunity. Supersession lineage **MUST NOT** be inferred from display text.
- **DLS-SEM-036:** A token-efficiency query **MUST** preserve AIC, every raw token class, reliability, outcome quality, proposed savings, gross realized savings, optimization overhead, net realized savings, verified net gain, and recommendation churn as separately named measures. It **MUST NOT** combine invocation and aggregate AIC, synthesize a total from raw token classes, present proposed savings as realized value, or charge unrelated portfolio work to one intervention.
- **DLS-SEM-037:** Workload comparability, overhead attribution, recommendation disposition, churn, and verified net gain **MUST** be materialized by the canonical data layer under `specs/dashboard-data.md` Section 5.5. Views, presenters, effects, and components **MUST NOT** calculate or override them.

### 5.5 Declarative Queries

`dashboard.queries` declares reusable, closed projections over §5.1 tables/projection, §5.4 sources, or earlier queries; no SQL, scripts, callbacks, templates, or general-purpose expressions.

Each query retains a non-empty `subject` describing what its data is about or intended to show. The optional `objective` explains why the query exists or what to improve or understand, and `acceptance` states the conditions that make it acceptable. This authoring metadata does not affect execution or presentation.

A query may declare typed scalar `parameters`. Parameter references are inert,
tagged values resolved from the active page form before the query graph enters
the data worker. Parameters change scalar operands only; they cannot select a
source, field, join, reducer, limit, or other query structure.

```yaml
queries:
  - name: workflow-aic-totals
    subject: Summarize observed AI Credit usage by declared workflow.
    description: Observed AI Credit totals for each declared workflow.
    from: usage
    aggregate:
      by: [organization, repository, workflow]
      values:
        - field: aic
          as: aic
          reducer: sum
  - name: workflow-inventory
    subject: List declared workflows with their observed AI Credit totals.
    from: workflows
    joins:
      - source: workflow-aic-totals
        type: left
        on:
          - { left: organization, right: organization }
          - { left: repository, right: repository }
          - { left: workflow, right: workflow }
        fields:
          - { field: aic, as: observed-aic }
    compute:
      - as: total-aic
        function: coalesce
        args:
          - { field: observed-aic }
          - { value: 0 }
    select:
      - { field: workflow }
      - { field: total-aic, as: aic }
    order-by:
      - { field: workflow, direction: asc }
    limit: 5000
```

#### Aggregate-Local Filters

An aggregate value may declare a `filter` containing `predicates`. This filter selects that value's contributing rows without changing the query's groups or any sibling aggregate. Predicates are conjunctive and each declares a pre-aggregation scalar `field` plus exactly one scalar `equals` value or non-empty `in` sequence:

```yaml
queries:
  - name: domain-and-blocked-request-counts
    subject: Count all domain observations and sum firewall-blocked requests by repository.
    from: domains
    aggregate:
      by: [organization, repository]
      values:
        - { field: event, as: all-domain-observations, reducer: count }
        - field: request-count
          as: blocked-requests
          reducer: sum
          filter:
            predicates:
              - { field: event-type, equals: firewall.request.blocked }
        - field: event
          as: policy-events
          reducer: count
          filter:
            predicates:
              - { field: event-type, in: [firewall.request.allowed, firewall.request.blocked] }
```

Firewall event arity is the numeric `request-count` field. Counting matching event records is not equivalent to summing blocked requests when one event represents multiple requests.

Query-level filtering and computation execute first. Group tuples are then formed from every remaining row. For each group and aggregate value independently, its aggregate-local filter is applied to the group's pre-aggregation rows and the reducer consumes only matching measure values. Aggregate-local filters do not remove a group, filter another aggregate, read an aggregate output, or imply event ordering, adjacency, windows, correlation, or any other sequence semantics.

#### 5.5.1 Computed-Field Vocabulary

Computed fields use only the following typed, deterministic functions with the stated inclusive argument counts. Each argument is exactly one `field` reference valid at that point in the query, one scalar `value` literal (including explicit `null`), or an execution `context` value: `time-start` or `time-end`.

| Function | Arguments | Result |
|---|---|---|
| `coalesce` | 2–8 | first argument that is not null, empty text, or a structured value |
| `link-href` | 1 | text `href` read from a structured link field, or null |
| `concat` | 2–8 | text |
| `literal` | 1 | scalar value |
| `lower`, `upper`, `title-case`, `trim`, `url-encode` | 1 | text |
| `date-day` | 1 | UTC calendar date text |
| `activity-shard-day` | 1 | UTC activity date from a normalized shard filename (`YYYY-MM-DD-NNNN-<16 hex digits>.jsonl`), optionally in a path or URL; invalid dates, the structural `0000-00-00` bucket, and undated filenames produce null, never an import-time fallback |
| `date-bucket` | 2 | UTC bucket-start timestamp text for a timestamp and literal `hour`, `day`, `week` (Monday), or `month` |
| `calendar-week-point` | 3 | serializable calendar point from timestamp, `time-end`, and an inclusion boolean or run conclusion; `true` includes the run regardless of conclusion, `false` excludes it, and conclusion strings include only `success` |
| `equals-any` | 2–8 | whether the first argument equals any later argument |
| `greater-than` | 2 | whether the first numeric argument is greater than the second |
| `if` | 3 | second argument when the first is true; otherwise the third |
| `format-count`, `format-percent` | 1 | locale-stable display text |
| `number` | 1 | finite number or null |
| `timestamp` | 1 | UTC epoch milliseconds for valid RFC 3339 timestamp text with an explicit zone, or null; rejects invalid calendar dates, relative expressions and non-text values, and never reads the clock |
| `iso-timestamp` | 1 | UTC ISO timestamp text from finite epoch milliseconds in years 0001 through 9999; missing or out-of-range values produce null |
| `sqrt` | 1 | Square root of a known finite nonnegative numeric value; negative or unknown input produces null |
| `sum`, `product` | 2–8 | finite number or null |
| `difference`, `quotient` | 2 | finite number or null |

#### 5.5.2 Prediction Vocabulary

`predict` fits deterministic mathematical models to the rows at that point in the query and appends each prediction as a numeric field. Its names and regression behavior mirror the Vega regression transform where their contracts overlap: `field` is the dependent field, `on` identifies one predictor field or a sequence of predictor fields, `method` selects the fit, `groupby` fits an independent model for each group, and `as` names the appended prediction. The default method is `linear`.

| Method | Predictors | Model |
|---|---:|---|
| `linear` | 1–8 | Ordinary least-squares linear regression with an intercept |
| `log` | 1 | `a + b × log(x)` |
| `exp` | 1 | `a × exp(b × x)` |
| `pow` | 1 | `a × x^b` |
| `quad` | 1 | Second-order polynomial regression |
| `poly` | 1 | Polynomial regression; `order` is 1–10 and defaults to 3 |

Unlike Vega's regression transform, which emits a fitted line as replacement tuples, `predict` preserves each input row and appends the fitted value. This makes observed and predicted values available together to any encoding. A row with a null target is excluded from model fitting but receives a prediction when its predictor fields are usable, allowing declarative forecasts over rows representing future points. Invalid numeric inputs, an underdetermined or singular model, and out-of-domain logarithmic or power inputs produce null rather than `NaN`, infinity, or an error.

The following JSON uses `run` as a numeric sequence, fits one trend per workflow, and displays observed AIC together with the predicted reference values:

```json
{
  "dashboard": {
    "queries": [
      {
        "name": "workflow-aic-predictions",
        "subject": "Compare observed workflow AIC with a linear trend.",
        "from": "usage",
        "predict": [
          {
            "field": "aic",
            "on": "run",
            "method": "linear",
            "groupby": ["workflow"],
            "as": "predicted-aic"
          }
        ]
      }
    ],
    "pages": [
      {
        "id": "forecast",
        "kind": "custom",
        "views": [
          {
            "id": "observed-and-predicted-aic",
            "data": { "source": "workflow-aic-predictions" },
            "mark": "chart",
            "chart": "dot",
            "encoding": {
              "x": { "field": "observed-at", "type": "temporal" },
              "y": { "field": "aic", "type": "quantitative" },
              "reference": { "field": "predicted-aic", "type": "quantitative" },
              "color": { "field": "workflow", "type": "nominal" }
            }
          }
        ]
      }
    ]
  }
}
```

The built-in methods require no registry or external configuration. Future language versions may admit externally registered model identifiers while preserving the same field, grouping, and output contract; implementations must not interpret an unknown method as registered without an explicit future declaration mechanism.

#### 5.5.3 Temporal-Series Vocabulary

`temporal-series` reshapes wide observations into tidy rows suitable for charts, statistics, and browser-side modeling. `time` names the timestamp field, `series` names the series dimension, and `carry` retains up to 16 scalar dimensions. Each `measures` entry reads one scalar numeric `field`; optional `key` names the field containing its metric identity. Each `maps` entry expands numeric properties from one mapping field; optional `definitions` names an array of `{ id, name }` definitions and optional `group` names its parent metric. The default `shape: tidy` emits the carried fields plus `time`, `series`, `metric`, `metric-key`, `metric-name`, `metric-kind`, `metric-group`, and `value`. `shape: groups` emits one row per carried-field and metric tuple with those metric fields and a chart-ready, timestamp-ordered `points` sequence. Invalid timestamps, null values, and non-finite values produce no point.

`shape: panels` emits one row per carried-field and metric tuple with a `series` sequence instead of `points`. Each series has `id`, `label`, and timestamp-ordered `points`; series are ordered by their labels. Optional `link` names a source link field retained on each point, without making provenance a grouping key. This allows multi-repository measure histories to render query-owned panels without regrouping observations in UI code. Panels do not accept `trend`; trends remain a `groups` feature.

Panels may declare `point-carry`, a sequence of 1 to 16 scalar field names. Each point retains the selected fields in its `metadata` mapping without adding them to panel identity. Absent fields remain absent and explicit nulls remain null. This supports one plot per native measure with provenance and query-declared continuity boundaries on individual observations; it does not establish comparability or permit connecting across unknown boundaries.

A grouped temporal series may declare `trend.direction` as the name of a carried field whose value is `increase`, `decrease`, `maintain`, or `target`. The worker orders each group's valid points by timestamp and appends the first value, last value, native delta, relative percentage when the first value is nonzero, observed direction, direction-aware assessment, and observation count. Fewer than two valid observations produce `trend-assessment: insufficient`. The assessment is deterministic selected-horizon change, not statistical significance or a causal claim.

The transform preserves source-row order and measure declaration or map-property order. It emits no more than 64 mapped metrics per input observation and no more than 100000 rows. It executes in the data Web Worker through the same serializable tidy pipeline as filtering, aggregation, and prediction.

Execution is backend-specific without changing the declaration: browser
IndexedDB queries use the data worker, local SQLite queries use the shared Go
evaluator, and hosted PostgreSQL queries compile to SQL. The worker and Go
evaluator group points during expansion instead of retaining a second tidy
table, parse each observation's timestamp once, and stably order cached numeric
timestamps. Trend summaries reuse that ordering. SQLite projects the complete
declared temporal inputs, including metric definitions and point metadata,
rather than unrelated source fields; explicitly requested raw sources remain
complete. PostgreSQL expands scalar measures with a lateral `VALUES` relation
and maps with lateral branches over one input scan. Trend endpoints share one
ascending window with an explicit full-partition frame. All paths enforce the
100000-point expansion limit before returning grouped results; grouping does
not relax that limit.

#### 5.5.4 Observation Windows

A query may declare a `window` sequence after `aggregate` and `predict`. Each
entry appends one field without changing the number or order of rows.
Entries execute in declaration order, so a later entry can refer to a prior
entry's output. An entry names its input `field`, unique output `as`, an
`operation` (`rolling`, `change`, `previous`, or `row-number`), an ascending or descending `order-by`
sequence, and optional `groupby` dimensions. Each group is independent. Sort
ties retain input order; authors who need ingestion-order-independent results
should provide a unique final ordering key. Windows operate over **observations**,
not calendar durations; bucket and aggregate first when the desired input is
one observation per day.

`previous` preserves the preceding observation's scalar type, including text,
and returns null for the first observation of each group. `row-number` returns
the one-based observation position within each group. Neither accepts rolling
frames, reducers, alignment, change modes, or time-unit options.

`rolling` takes a positive integer `frame` (the number of observations), a
`reducer` of `mean` (default), `sum`, `min`, or `max`, and an `alignment` of
`trailing` (default) or `centered`. A trailing seven-observation frame is
`[-6, 0]`; a centered seven-observation frame is `[-3, 3]`. Centered frames
require an odd frame size and produce null at either boundary unless the full
frame is present; the newest points on a live dashboard are therefore not
misrepresented as complete smoothed estimates. A trailing frame uses the
available observations at the beginning of a series. Missing numeric inputs
do not contribute to a rolling reduction; a frame with no valid inputs yields
null, not zero.

`change` compares its input to the **immediately preceding observation** in
the same group. Its `mode` is `absolute` (default), `percentage`, or `rate`.
Absolute change is current minus previous. Percentage change is 100 times
that difference divided by previous; when previous is zero the result is null.
A `rate` names a temporal `time-field` and a `unit` (`second`, `minute`, `hour`,
or `day`); it divides the difference by the actual elapsed time expressed in
that unit. Equal, invalid, or decreasing timestamps cannot establish a
positive elapsed interval and yield null. The first observation, a missing
current or previous value, or a non-finite result likewise yields null. Null
is not equivalent to a plotted zero. A renderer using Vega-Lite can compile
these declarative operations to partitioned, sorted window and calculate
transforms; CAO executes them in the browser data worker or the hosted SQL
query engine, not in view-specific JavaScript. A backend that does not
implement observation windows must reject such queries rather than silently
return the untransformed observations.

#### 5.5.5 Normative Query Requirements

- **DLS-QUERY-001:** `queries`, when present, **MUST** be a non-empty sequence of mappings. Each query **MUST** declare a `name` matching the canonical identifier pattern in **DLS-DOC-005**, a non-empty `subject` describing what its data is about or intended to show, and one `from` input; **MAY** declare non-empty `objective` and `acceptance` semantic annotations, `description`, `parameters`, `union`, `time`, `joins`, `filter`, `compute`, `temporal-series`, `aggregate`, `predict`, `window`, `select`, `order-by`, `limit`, and `facet`; and **MUST NOT** declare any other key. The combined `subject`, `objective`, and `acceptance` of each query **MUST NOT** exceed 512 Unicode characters. Query execution **MUST** treat semantic annotations as inert metadata.
- **DLS-QUERY-002:** A query `name` **MUST** be unique among queries and **MUST NOT** shadow a Section 5.1 database table or registered Section 5.4 runtime source. A declared query name **MAY** be used wherever a view selects data.
- **DLS-QUERY-003:** `from`, every `union[]`, and every `joins[].source` **MUST** name one Section 5.1 database table, registered Section 5.4 runtime source, built-in `simulation-days` source, or query declared earlier in the sequence. Forward references, self references, and cycles **MUST** be rejected. `union`, when present, **MUST** be a non-empty sequence; its rows are appended in declaration order, fields from every unioned table, runtime source, or query are available to later clauses, and a field absent from one row has a null value for query operations.
- **DLS-QUERY-004:** Clause execution order **MUST** be `from`, then `union` in declaration order, then `joins` in declaration order, then `filter`, `compute` in declaration order, `temporal-series`, `aggregate`, `predict` in declaration order, `window` in declaration order, `select`, `order-by`, `limit`, and finally `facet`.
- **DLS-QUERY-005:** A join **MUST** declare `source`, a non-empty `on` sequence of `left`/`right` equality key pairs, and a non-empty `fields` sequence of aliased fields imported from the joined source. `type` **MUST** be `inner`, `left`, or `inner-many` and defaults to `inner`. `inner-many` explicitly expands matching right rows in source order, retaining left input order, within the same row, operation, cancellation and memory budgets. No keyless cross joins or join expressions are defined. A query **MUST NOT** declare more than four joins.
- **DLS-QUERY-006:** Join keys **MUST** address table or query fields, not canonical entity identities. `left` **MUST** name a field available after the preceding clauses and `right` **MUST** name a field declared by the joined table or query. Key values **MUST** be compared as trimmed text; a null, missing, empty, or structured key value **MUST NOT** match any row.
- **DLS-QUERY-007:** For `inner` and `left`, the joined source **MUST** contain at most one row per join key. A duplicate join key **MUST** fail the query. Only an explicit `inner-many` join may expand rows; it **MUST** fail closed when expansion exceeds query resource budgets.
- **DLS-QUERY-008:** For an unmatched `left` join row, every imported join field **MUST** be null; unmatched `inner` and `inner-many` rows **MUST** be dropped.
- **DLS-QUERY-009:** Every output name **MUST** be unique. A join field alias, computed field name, aggregate output name, or `select` alias that collides with an existing output name **MUST** be rejected.
- **DLS-QUERY-010:** Computed fields **MUST** use only the Section 5.5.1 vocabulary with a valid argument count. An argument **MUST** declare exactly one valid `field`, scalar `value`, or `context`; `time-start` resolves to the active query window's inclusive UTC start, or empty text when unbounded. `time-end` resolves to its exclusive UTC endpoint. A missing, null, empty, or structured input to a numeric function, a non-numeric text input, and division by zero **MUST** produce null. Text functions **MUST** treat null, missing, and structured inputs as empty text. A computation **MUST NOT** produce `NaN`, `Infinity`, or an error value.
- **DLS-QUERY-011:** `filter`, `aggregate`, `order-by`, and `limit` **MUST** use the same deterministic semantics as Sections 6, 7, and 11.2. Query aggregates additionally permit `distinct-list`, which returns distinct non-null scalar values sorted as text and joined with `, `, and `calendar-week-rhythm`, which consumes `calendar-week-point` values and returns seven Monday-to-Sunday UTC slots containing current-week and matching previous-week counts. An aggregate value **MAY** declare the aggregate-local `filter` defined above. `select` **MUST** project and optionally rename fields and **MUST** drop every field it does not name.
- **DLS-QUERY-012:** The output field schema of a query **MUST** be statically derivable from its declaration so encodings, filters, and `order-by` references can be validated before execution. A reference to a field the preceding clauses do not produce **MUST** be rejected with `DLS-E010`.
- **DLS-QUERY-013:** A query **MUST NOT** read more than 200000 input rows per source, produce more than 200000 joined rows, or produce more than 100000 output rows; `limit` **MUST NOT** exceed 100000. Exceeding a limit **MUST** fail the query closed and **MUST NOT** truncate results silently.
- **DLS-QUERY-014:** A derived source's metadata **MUST** compose its inputs' provenance: it **MUST** report `source-kind` `derived`, the oldest input `as-of` and `retrieved-at`, and the weakest input completeness and freshness. A missing or unavailable `from`, `union`, or `inner`-join input **MUST** produce `unavailable` availability. A missing or unavailable `left`-join input **MUST** be evaluated as an empty enrichment, preserve the primary rows with null imported fields under **DLS-QUERY-008**, and degrade completeness without making a non-empty result unavailable. An executed query with zero output rows **MUST** report `empty` availability under **DLS-DATA-004**.
- **DLS-QUERY-015:** A failed query **MUST** produce zero rows, `unavailable` availability, and one explicit diagnostic identifying the dashboard path of the failing query. A diagnostic **MUST NOT** contain source payloads, row values, credentials, or secrets.
- **DLS-QUERY-016:** A presenter **MUST** resolve the query dependency graph before requesting a page projection so every input source required by a requested derived source is loaded while unrelated sources remain excluded, and **MUST** execute queries in the data-processing layer defined by Section 7.5 without a main-thread fallback.
- **DLS-QUERY-017:** An execution layer **MUST NOT** assume its query definitions were validated. Before it reads any rows it **MUST** reject a query that reads itself, participates in a dependency cycle, reads a query declared later in the sequence, shares its name with another query, declares a join without equality keys, declares more joins than **DLS-QUERY-005** permits, or declares a `limit` outside **DLS-QUERY-013**. Every query that reads a rejected query **MUST** also be rejected. Rejected queries **MUST** fail closed under **DLS-QUERY-015**, and a query that does not depend on a rejected query **MUST** still execute.
- **DLS-QUERY-018:** Query execution **MUST** be cancelable from outside the execution layer through an abort signal, **MUST** stop after 60000 milliseconds of execution, and **MUST** stop after 5000000 row operations. A stopped execution **MUST NOT** report a partial projection: it **MUST** surface an explicit cancellation distinct from a query fault, and **MUST** identify only the cancellation cause without source payloads or secrets. A presenter **MUST** offer a command that cancels a runaway computation, and **MUST** terminate a data worker that does not acknowledge cancellation.
- **DLS-QUERY-019:** A validator **MUST** reject a query whose field references are incompatible with the table or earlier-query schema, with `DLS-E011`. A field that only exists after a presenter derives it from an executed projection **MUST NOT** be read by a query. A structured link field **MUST NOT** be a join key, filter field, computed-field argument, grouping field, numeric aggregate measure, or `order-by` field; a query **MAY** project one or retain it using `unique`, which returns the single distinct non-null value and otherwise `null`. A temporal field **MUST NOT** be a numeric computed-field argument or the measure of a `sum`, `mean`, `min`, or `max` reducer.
- **DLS-QUERY-020:** Query `time`, when present, **MUST** satisfy Section 6 time syntax. A relative query range **MUST** resolve against the active view window's exclusive end and replace only that view's temporal bounds for the query; scope, route, and dimension filters **MUST** remain in force.
- **DLS-QUERY-021:** `predict` **MUST** be a non-empty sequence. Each entry **MUST** declare one numeric `field`, one `on` predictor field or a sequence of one to eight numeric predictor fields, and a unique `as` output name; **MAY** declare `method`, `groupby`, and `order`; and **MUST NOT** declare another key. `method` **MUST** be one Section 5.5.2 built-in and defaults to `linear`. Only `linear` **MAY** declare more than one predictor. `order` **MAY** appear only with `poly`.
- **DLS-QUERY-022:** A prediction **MUST** fit independently for each distinct `groupby` tuple, or once for all rows when `groupby` is absent. Fitting **MUST** ignore rows whose target or predictor input is not a finite number. Prediction **MUST** preserve row count and order, MUST NOT mutate an input row, and **MUST** append a finite numeric value or null under the Section 5.5.2 semantics.
- **DLS-QUERY-023:** Prediction fitting and evaluation **MUST** execute in the data Web Worker, remain subject to the cancellation, duration, and operation budgets in **DLS-QUERY-018**, and require no network, external configuration, or model registry. A future registered model extension **MUST** fail closed when its explicit registry or model is unavailable and **MUST NOT** change built-in method behavior.
- **DLS-QUERY-024:** An aggregate-local `filter` **MUST** contain only `predicates`, with one to eight entries. Each predicate **MUST** contain only `field` and exactly one of `equals` or `in`; `equals` **MUST** be a finite number, text, or boolean, and `in` **MUST** contain one to 32 such literals. The referenced field **MUST** exist immediately before aggregation and **MUST** be scalar. Structured links, aggregate outputs, and fields produced by later clauses **MUST** be rejected with `DLS-E011` or `DLS-E010` as applicable.
- **DLS-QUERY-025:** A query **MUST NOT** declare more than 64 aggregate values. Aggregate-local predicate evaluation **MUST** count against the operation budget in **DLS-QUERY-018**. It **MUST NOT** expand rows, alter group formation, access external state, execute code, or introduce sequence semantics.
- **DLS-QUERY-026:** `temporal-series` **MUST** declare valid scalar `time` and `series` fields and at least one bounded `measures` or `maps` entry. It **MUST** emit only finite numeric values with valid timestamps, preserve deterministic order, remain within **DLS-QUERY-013** limits, execute in the data Web Worker, and expose the statically known tidy output fields defined in Section 5.5.3.
- **DLS-QUERY-027:** `parameters`, when present, **MUST** be a non-empty sequence of mappings containing exactly a unique canonical `name` and a `type` of `number`, `string`, or `boolean`. A parameter reference **MUST** contain exactly `parameter` naming one parameter declared by the same query. Version 0.1.0 permits parameter references as query predicate `equals`, `gte`, or `lt` operands and as computed-field arguments. A parameter **MUST NOT** alter query topology or output schema.
- **DLS-QUERY-028:** Before query execution, the presenter **MUST** resolve every parameter reference to one finite number, string, or Boolean value supplied by the active page form. A computed-field parameter reference resolves as a literal computed argument. A missing, structured, non-finite, undeclared, or type-incompatible value **MUST** fail the page projection closed and **MUST NOT** execute a broader query with the predicate or computation removed.
- **DLS-QUERY-029:** A named-query agent transport **MAY** invoke a parameterized query by identifier with explicitly supplied scalar operands. It **MUST** discover parameter declarations through the query's transitive dependencies, validate each supplied value against its declared type and any bound page-form constraints, reject missing or unexpected operands, and substitute only scalar values into the reviewed query before executing through the bounded query engine. Parameter input **MUST NOT** alter query topology or execute authored scripts. A synthetic input such as `simulation-days` **MUST** remain labeled as modeled rather than measured evidence.
- **DLS-QUERY-030:** A `window` **MUST** contain one to eight entries with unique `as` names and valid input fields; `rolling` and `change` require numeric inputs, while `previous` and `row-number` accept scalar inputs; each entry **MUST** declare `operation`, `field`, `as`, and a non-empty `order-by`, and **MAY** declare `groupby`. The available fields of each entry include outputs of earlier entries but not later ones. Rolling entries **MUST** specify a positive integer `frame`, **MAY** specify `reducer` and `alignment`, and **MUST NOT** specify change-only fields. Change entries **MAY** specify `mode`; `rate` **MUST** specify a temporal `time-field` and a valid `unit`, and other change modes **MUST NOT** specify these fields.
- **DLS-QUERY-031:** Window evaluation **MUST** follow the Section 5.5.4 observation, partition, stable ordering, alignment, missing-value, division-by-zero, and elapsed-time rules. It **MUST** preserve input row order and count, and append only finite numbers or null. Window calculations **MUST** execute within the same cancellation and row-operation budgets as other declarative queries.

### 5.6 Parameterized Page Forms

A built-in or custom page may declare one `form` that supplies typed scalar
values to parameterized queries selected by that page. Form state belongs to
the current rendered page instance. Version 0.1.0 does not serialize it into
the URL or persistent browser storage.

```json
{
  "dashboard": {
    "queries": [
      {
        "name": "simulated-usage",
        "subject": "Estimate observed AIC under an operator-selected multiplier.",
        "parameters": [
          { "name": "multiplier", "type": "number" },
          { "name": "include-live", "type": "boolean" },
          { "name": "profile", "type": "string" }
        ],
        "from": "usage",
        "compute": [
          {
            "as": "selected-aic",
            "function": "if",
            "args": [
              { "parameter": "include-live" },
              { "field": "aic" },
              { "value": 0 }
            ]
          },
          {
            "as": "simulated-aic",
            "function": "product",
            "args": [
              { "field": "selected-aic" },
              { "parameter": "multiplier" }
            ]
          },
          {
            "as": "profile-label",
            "function": "coalesce",
            "args": [
              { "parameter": "profile" },
              { "value": "balanced" }
            ]
          }
        ]
      }
    ],
    "pages": [
      {
        "id": "simulator",
        "kind": "custom",
        "title": "Performance simulator",
        "form": {
          "title": "Scenario",
          "update": { "strategy": "debounce", "delay-ms": 250 },
          "fields": [
            { "id": "multiplier", "label": "AIC multiplier", "control": "slider", "default": 1, "min": 0, "max": 4, "step": 0.25 },
            { "id": "include-live", "label": "Include live runs", "control": "checkbox", "default": true },
            {
              "id": "profile",
              "label": "Profile",
              "control": "radio",
              "default": "balanced",
              "options": [
                { "value": "balanced", "label": "Balanced" },
                { "value": "fast", "label": "Fast" }
              ]
            }
          ]
        },
        "views": [
          {
            "id": "simulated-aic",
            "data": { "source": "simulated-usage" },
            "mark": "chart",
            "chart": "line",
            "encoding": {
              "x": { "field": "observed-at", "type": "temporal", "time-unit": "day" },
              "y": { "field": "simulated-aic", "type": "quantitative" }
            }
          }
        ]
      }
    ]
  }
}
```

- **DLS-FORM-001:** `form` **MUST** contain a non-empty `fields` sequence and **MAY** contain non-empty `title` and `description` strings and one `update` mapping. Field IDs **MUST** be unique canonical identifiers and **MUST** name a parameter declared by a dashboard query.
- **DLS-FORM-002:** A form field **MUST** declare a non-empty `label`, one `control` of `slider`, `checkbox`, `radio`, or `text`, and a typed `default`. `text` requires a string default and MUST NOT declare numeric bounds or options; its debounced scalar value is bound to a query parameter, never interpreted as query code. `slider` **MUST** declare finite numeric `min`, `max`, and positive `step`, with `min < max` and `default` inside the inclusive range. `checkbox` **MUST** have a Boolean default. `radio` **MUST** declare 2 to 12 uniquely valued, consistently typed scalar options and a default matching one option.
- **DLS-FORM-003:** Form fields **MUST** render in declaration order in an automatic responsive layout. Authors **MUST NOT** declare coordinates, columns, or breakpoints. Every control **MUST** expose its label, description when present, current value or checked state, keyboard operation, and native accessibility semantics.
- **DLS-FORM-004:** `update.strategy` **MUST** be `debounce` or `throttle`; `delay-ms` **MUST** be an integer from 50 through 2000. The defaults are `debounce` and 250 milliseconds. Rapid updates **MUST** coalesce at the declared cadence, preserve the latest complete form state, cancel superseded requests or subscriptions, and prevent stale results from replacing newer results.
- **DLS-FORM-005:** Form interaction code **MUST NOT** query data, filter rows, compute business state, or reconstruct relationships. It may own typed form state and DOM synchronization only. Query substitution and execution **MUST** remain inside the canonical page projection and data-worker boundary.
- **DLS-FORM-006:** Form values **MUST** remain in memory for the active dashboard document and **MUST** reset to authored defaults when that document or page state is recreated. Version 0.1.0 presenters **MUST NOT** persist form values in URLs or browser storage.

---

## 6. Scope, Time, and Filters

### 6.1 Scope

`scope` is a mapping whose allowed keys are `organizations`, `repositories`, and `workflows`. Each value is a non-empty sequence of domain identifiers. A missing key is unbounded at that scope level.

### 6.2 Time

`time` is a mapping containing either `range` or optional `start` and `end` RFC 3339 timestamps. `range` is a positive integer followed by `h`, `d`, or `w`, such as `30d`. A relative range resolves to `[evaluated-at - range, evaluated-at)`, where the presenter exposes one RFC 3339 `evaluated-at` instant for the dashboard. Absolute `start` is inclusive and `end` is exclusive. Missing absolute bounds are unbounded. Time comparisons use instants; calendar time units use UTC.

### 6.3 Filters

`filters` maps a canonical dimension to either one scalar value or a non-empty sequence of values. Values within a sequence are alternatives; separate filter keys are conjunctive. `rollout-mode` is an ordinary dimension and follows the same rules as every other filterable dimension.

### 6.4 Context Composition

Dashboard defaults establish the initial context. A custom view's `data` context narrows that context. It cannot expand it.

- **DLS-CTX-001:** Scope constraints at different levels **MUST** be combined by intersection and **MUST** preserve organization–repository–workflow ancestry.
- **DLS-CTX-002:** `time.start` and `time.end` **MUST** be RFC 3339 timestamps, and `start` **MUST** precede `end`.
- **DLS-CTX-003:** Time filtering **MUST** include observations at `start` and exclude observations at `end`.
- **DLS-CTX-004:** A scalar filter **MUST** use equality; sequence values **MUST** use logical OR; distinct filter keys **MUST** use logical AND.
- **DLS-CTX-005:** A view context **MUST** inherit omitted dashboard defaults and **MUST** combine supplied scope, time, and filters by intersection.
- **DLS-CTX-006:** `rollout-mode` **MUST** be filterable, groupable, and displayable by the same mechanisms as other dimensions.
- **DLS-CTX-007:** Missing or `unknown` dimension values **MUST NOT** match a concrete filter value and **MUST** match the explicit value `unknown`.
- **DLS-CTX-008:** Filtering **MUST** occur before aggregation, ordering, and limiting.
- **DLS-CTX-009:** `time.range` **MUST** be `all` or match `^[1-9][0-9]*(h|d|w)$` and **MUST NOT** appear with `start` or `end`. `all` removes the display-window lower bound from that query's input dependencies while retaining any selected exclusive endpoint. It allows a visible period to use an earlier retained reference without changing the evaluator window. Output view scope remains in effect. Without a selected endpoint, retained input is unbounded and `context: time-end` still resolves to the source assessment endpoint; future assessments cannot establish current movement.
- **DLS-CTX-010:** A presenter resolving `time.range` **MUST** expose `evaluated-at` and use it consistently for every page and view in the dashboard.

---

## 7. Dimensions, Measures, and Aggregation

### 7.1 Canonical Dimensions

Canonical dimensions include entity IDs, `campaign`, `workflow-role`, `variant`, `workflow-active`, `run-status`, `run-conclusion`, `outcome-state`, `rollout-mode`, `engine`, `requested-model`, `resolved-model`, operational-value definition, categorical observation results, and temporal fields.

GitHub API rate-limit dimensions additionally include `credential`, `credential-type`, `resource`, `bucket`, `maximum-lane`, `history-series`, `operation`, `phase`, `outcome`, `risk-status`, `is-current`, and `attribution-status`. `bucket` identifies one resource and credential. `maximum-lane` preserves that identity together with its quota maximum so observations with the same maximum lane receive a stable series color. `history-series` preserves the bucket identity while starting a new visual segment after an unavailable collection checkpoint so a line does not bridge a known evidence gap. None of these fields changes the observation grain.

### 7.2 Canonical Measures

| Measure | Meaning | Additivity |
|---|---|---|
| `input-tokens` | Provider-reported input tokens | Additive |
| `output-tokens` | Provider-reported output tokens | Additive |
| `cache-read-tokens` | Provider-reported cache-read tokens | Additive |
| `cache-write-tokens` | Provider-reported cache-write tokens | Additive |
| `reasoning-tokens` | Provider-reported reasoning tokens | Additive |
| `aic` | Authoritatively supplied AI Credits | Additive |
| `value` on `grader-observations` | Value emitted by a grader | Non-additive by default |
| `operational-value` | Native primary metric under its gh-aw metric identifier | Non-additive by default |
| `limit` | GitHub quota capacity for one resource and credential window | Non-additive |
| `used` | GitHub-reported or arithmetically derived requests used in the current window | Non-additive |
| `remaining` | Requests remaining in the current window | Non-additive |
| `remaining-percent` | Remaining capacity normalized to the resource limit | Non-additive |
| `minutes-to-reset` | Observation-relative minutes until the exact reset timestamp | Non-additive |
| `consumed-since-previous` | Requests consumed since the previous same-window observation | Non-additive |
| `burn-rate-per-minute` | Reset-safe estimated requests consumed per minute | Non-additive |
| `projected-remaining-at-reset` | Forecast remaining capacity at reset | Non-additive |
| `runway-ratio` | Estimated time to exhaustion divided by time to reset | Non-additive |
| `operation-consumed` | Reliably paired requests consumed by one operation execution | Non-additive |

Entity counts are obtained with `count` or `distinct-count`; they are not stored measures.

### 7.2.1 Units

Without a declared unit or format, presenters round floating-point values to at most three fractional digits for display. This does not alter stored values or query results; declared unit significance and formats retain their own precision rules.

A dashboard may declare reusable units in `dashboard.units`. A field definition selects one declared unit by setting `unit` to its identifier. The unit `name` is its human-readable name, `symbol` is the compact label appended to presented values, and `significant` is the smallest presentation increment. Presenters round a unit-bearing value to the nearest multiple of `significant`, with halfway cases rounded away from zero, without changing the value used for filtering, aggregation, or ordering. The decimal places implied by `significant` are retained. The optional `format` selects a defined presentation format and defaults to ordinary numeric formatting when omitted. For example:

```yaml
units:
  aic:
    name: AICc($)
    symbol: cAIC
    significant: 2
    format: aicc
  usd:
    name: US dollars
    symbol: USD
    significant: 0.001
    format: usd
  human-duration:
    name: Human-friendly duration
    symbol: s
    significant: 1
    format: duration
```

The AIC definition uses a significance of `2`. Its `aicc` format divides the rounded AIC value by `100` and presents the result as US currency through the browser internationalization API. The `number` format presents the rounded numeric value without appending the unit symbol. The `duration` format interprets values as seconds and presents compact cascading components. It presents values below one minute as seconds (`45s`), values below one hour as minutes and seconds (`1m 30s`), values below one day as hours and minutes (`1h 23m`), and longer values as days and hours (`1d 3h`). The lower component is retained when zero, and components below the selected precision are omitted. The `usd` format presents US dollars with a dollar sign, two fractional digits when fewer are needed, and no more than three fractional digits. Values requiring more than three fractional digits are rounded upward at the third fractional digit.

- **DLS-UNIT-001:** A field `unit`, when present, **MUST** reference exactly one unit declared by `dashboard.units`.
- **DLS-UNIT-002:** Unit formatting **MUST** affect presentation only and **MUST NOT** change filtering, aggregation, ordering, limiting, source data, or provenance.
- **DLS-UNIT-003:** For a unit without `format`, a presenter **MUST** append the declared `symbol` to a unit-bearing value and round it to the nearest multiple of `significant`, with halfway cases rounded away from zero. A `number` unit **MUST** apply the same rounding without appending the symbol.
- **DLS-UNIT-004:** `format`, when present, **MUST** be `aicc`, `duration`, `number`, or `usd`. An `aicc` unit **MUST** declare `name: AICc($)` and `significant: 2`. A presenter **MUST** round its AIC value to the declared significance, divide it by `100`, and present the result as `USD` currency using the browser internationalization API. A `duration` unit **MUST** declare `symbol: s` and `significant: 1`. A presenter **MUST** round its value to the nearest whole second with halfway cases rounded away from zero, preserve the sign, and present its absolute components using the compact cascading form defined above. Component suffixes are intrinsic to these formats, so the presenter **MUST NOT** append another `symbol`.
- **DLS-UNIT-005:** A `usd` unit **MUST** declare `symbol: USD` and `significant: 0.001`. A presenter **MUST** prefix its value with the dollar sign, retain at least two and no more than three fractional digits, and round upward at the third fractional digit. The currency marker is intrinsic to this format, so the presenter **MUST NOT** append another `symbol`.

### 7.3 Aggregates

Allowed aggregate values are `count`, `distinct-count`, `sum`, `mean`, `min`, `max`, and `none`. Omitted `aggregate` means `none`. `count` counts non-null field values. `distinct-count` counts distinct non-null values.

Allowed `time-unit` values are `hour`, `day`, `week`, and `month`. Buckets are half-open UTC intervals. Weeks begin Monday at 00:00:00Z; months begin on the first day.

Unaggregated dimensions in an encoding form the grouping key. Aggregated fields are computed once per resulting group. A metric with no unaggregated dimension computes one value over its effective context.

A field definition may also include an optional `as` property to name the aggregate output for subsequent references. This allows a view to refer to a derived metric by a stable identifier instead of inferring an implementation-specific name. When `as` is omitted, the canonical identifier is `<aggregate>-<field>`. See **DLS-AGG-009** and **DLS-AGG-010** for the normative validation rules.

An **output row** is the post-aggregation result of applying grouping and aggregation to a view's encoding. An output row is **entity-grain** when its output identifier is a canonical entity ID (for example, one row per repository or one row per run); otherwise it is **group-grain**, and its output identifier is the tuple of its remaining unaggregated output dimensions (for example `(day, run-conclusion)`), each taken after time bucketing.

The **canonical post-aggregation row order** is defined for every output grain, entity-grain or group-grain, as follows:

1. Apply each declared `order-by` clause in sequence, comparing each row's resolved output identifier value ascending or descending as declared.
2. Break any ties remaining after step 1, or order all rows when `order-by` is entirely omitted, by the view's remaining unaggregated output dimensions that are not already fully determined by step 1. The grouping-capable encoding channels defined in Section 11.1 (`x`, `y`, `color`, `section`, and each `columns` entry), plus chart facet fields, can hold an unaggregated output dimension; `value` and `href` are excluded because they do not participate in grouping. Consider these channels in that fixed declaration order (`x`, then `y`, then `color`, then `section`, then each `columns` entry in its declared sequence, then the single facet field or facet `row` followed by `column`), each compared ascending by canonical field value after time bucketing.
3. Break any ties still remaining after step 2 by canonical entity ID ascending, when a canonical entity ID is present at the output grain; an entity-grain output row always has a canonical entity ID available for this step.

A presenter **MUST** apply `limit` only after the canonical post-aggregation row order from steps 1 through 3 is fully resolved.

### 7.4 Normative Aggregation Requirements

- **DLS-AGG-001:** An implementation **MUST** group only by dimensions and **MUST** aggregate only measures or entity identifiers compatible with the selected aggregate.
- **DLS-AGG-002:** `sum` **MUST** be accepted only for the five raw-token measures and `aic`.
- **DLS-AGG-003:** Different raw-token measures **MUST NOT** be combined into a derived total because provider reporting classes may overlap; a combined presentation **MUST** retain separate measures.
- **DLS-AGG-004:** AIC aggregation **MUST** sum only available, non-negative AIC observations, retain all contributing provenance, and **MUST NOT** substitute zero for missing AIC.
- **DLS-AGG-005:** Grader `value`, `operational-grader`, and `operational-value` **MUST** use `none`, `mean`, `min`, or `max`, and aggregation **MUST** retain grader identity, operational-grader definition, or operational-value definition, respectively.
- **DLS-AGG-006:** `count` and `distinct-count` **MUST** ignore absent values and **MUST NOT** substitute zero.
- **DLS-AGG-007:** A time unit **MUST** be applied before grouping and **MUST** use the UTC boundaries in Section 7.3.
- **DLS-AGG-008:** Rankings **MUST** disclose the ranked measure, direction, filters, time range, scope, and tie behavior; the ranking key **MUST** be resolved against the post-aggregation output identifier before applying `limit`, and ties **MUST** then be broken using the canonical post-aggregation row order defined above for every post-aggregation output grain, entity-grain or group-grain, not only entity-grain outputs.
- **DLS-AGG-009:** A field definition with `aggregate` other than `none` **MAY** include `as`; if omitted, the validator **MUST** derive a canonical output identifier as `<aggregate>-<field>`. A field definition with `aggregate: none` **MUST NOT** include `as`.
- **DLS-AGG-010:** A view **MUST** reject duplicate aggregate-output identifiers within the same view and **MUST** reject ambiguous or invalid `data.order-by.field` references that do not resolve to exactly one source field at the output grain or one aggregate-output identifier; such failures **MUST** use `DLS-E010`.
- **DLS-AGG-011:** If, after applying steps 1 and 2 of the canonical post-aggregation row order, one or more output rows remains tied and no canonical entity ID is available at the output grain to complete step 3, or if any remaining unaggregated output dimension used in step 2 has no canonical comparison defined by this specification for its declared or intrinsic type, a validator **MUST** reject the view with `DLS-E010` rather than leaving the presenter to invent an ordering.

### 7.5 Presenter Data-Processing Language

A presenter may compile the declarative view context and encoding into the following small row-processing language. This language is an implementation interface, not an additional dashboard-document vocabulary. Its operators are plain structured data so that a presenter can transfer source rows and an operator sequence to a Web Worker without transferring executable code.

Every request contains `data`, a sequence of row mappings, and `operators`, an ordered sequence. Processing returns a new sequence and does not mutate `data`.

| Operator | Shape | Result |
|---|---|---|
| `filter` | `predicates` and optional `search` | Retains rows matching every predicate and the optional case-insensitive search. A predicate has `field` and exactly one of `equals`, `in`, or `includes`. `search` has `fields` and `query`. |
| `summarize` | optional `by` and required `values` | Produces one row per distinct `by` tuple, or one row for the full input when `by` is omitted. Each value has `field`, `as`, and a `reducer`, and may have an aggregate-local `filter`. |
| `arrange` | ordered `by` entries | Orders rows by each `field`; `direction` is `asc` or `desc`. |
| `compute` | required `values` | Appends deterministic computed fields in declaration order. |
| `temporal-series` | `time`, `series`, optional `carry`, and bounded `measures` or `maps` | Reshapes wide observations into tidy temporal metric rows for charts, statistics, and modeling. |
| `predict` | required `values` | Fits grouped built-in regression models and appends finite predictions or null without changing row order. |
| `window` | required `values` | Appends partitioned, deterministically ordered rolling or change values without changing row order. |
| `select` | required `fields` | Projects and optionally renames fields. |
| `slice` | `limit` and optional `offset` | Retains the requested contiguous range. |

The `summarize` reducers include `count`, `distinct-count`, `distinct-list`, `unique`, `sum`, `mean`, `min`, `max`, `median`, `p75`, `p90`, and `p95`, with the semantics in Section 7.3 and Section 5.5.3. Quantiles ignore missing numeric values, return null for no known values, and use continuous linear interpolation at position `(n - 1) * p` in ascending order. Within `summarize`, each aggregate-local filter executes after grouping and before its reducer. A missing or null filter field does not match a concrete literal; the explicit text value `unknown` matches a missing or null field. A matching row whose measure is missing, null, or empty is ignored by the reducer. An empty filtered input yields zero for `count`, `distinct-count`, and `sum`, and `null` for `mean`, `min`, and `max`; an observed numeric zero remains a contributing value. Thus zero and an absent aggregate result remain distinct. Operators execute from first to last; therefore a conforming compilation places query-level filtering before summarization, prediction, arrangement, and slicing.

```json
{
  "data": [
    { "repository": "example/api", "status": "open", "score": 0.8 }
  ],
  "operators": [
    { "op": "filter", "predicates": [{ "field": "status", "equals": "open" }] },
    {
      "op": "summarize",
      "by": ["repository"],
      "values": [{ "field": "score", "as": "mean-score", "reducer": "mean" }]
    },
    { "op": "arrange", "by": [{ "field": "mean-score", "direction": "desc" }] },
    { "op": "slice", "limit": 10 }
  ]
}
```

- **DLS-PROC-001:** A processing request **MUST** contain only structured-clone-compatible data and **MUST NOT** contain source text or executable callbacks.
- **DLS-PROC-002:** A processor **MUST** apply operators in declaration order and **MUST NOT** mutate the supplied rows.
- **DLS-PROC-003:** A processor **MUST** implement filter conjunction, alternative values, missing-value behavior, aggregation, and ordering consistently with Sections 6.3 and 7.3.
- **DLS-PROC-004:** A presenter using a worker **MUST** correlate responses with requests and **MUST NOT** apply a superseded response over a newer user interaction.

---

## 8. Provenance, Freshness, and Data States

### Global ingestion indicator

`dashboard.data-status` optionally binds the shell indicator to a declared
`freshness-source` query exposing one row with `latest-data-at` (ISO timestamp or
Unix milliseconds), a positive finite `stale-after-hours` threshold, and an
optional `health-source` query exposing one row with `configured` and `health`.
Both queries remain shell dependencies when page definitions are chunked.
The health query is requested only on server-backed dashboards and respects
the existing authorization boundary.

The indicator subscribes to these query results for its mounted lifetime and
reevaluates evidence age once per minute. Green means evidence is fresh and,
when hosted health is configured, collection reports `healthy`. Amber means
evidence is at least the threshold age or collection reports `recovering`.
Red means collection reports `degraded`, a dashboard refresh failed, or requests
are blocked by rate limiting. Missing, invalid, future, or unavailable evidence
and unavailable hosted health remain unknown, not healthy. Stale evidence alone
does not prove ingestion failure. The accessible label and tooltip explain the
state, using upstream evidence time rather than browser import time.

This clock comparison and lifecycle feedback are shared shell presentation:
Dashboard Language still owns evidence selection, aggregation, and collection
health projection; the renderer must not query or reconstruct source rows.

### 8.1 Data-Set Metadata

Each database table or query result is accompanied by metadata outside the dashboard YAML:

| Field | Meaning |
|---|---|
| `source-id` | Stable identifier for the supplying source |
| `source-kind` | Human-readable source category |
| `as-of` | Latest observation time represented |
| `retrieved-at` | Time the data was made available to the presenter |
| `coverage-start`, `coverage-end` | Known half-open coverage interval |
| `completeness` | `complete`, `partial`, or `unknown` |
| `freshness` | `fresh`, `stale`, or `unknown` |
| `provenance-link` | Optional safe link to source evidence |

Freshness is an asserted data property. This specification does not define a cache or infer a universal staleness threshold.

### 8.2 Data States

Data quality has three independent axes:

| Axis | Values |
|---|---|
| Availability | `available`, `empty`, `unavailable` |
| Completeness | `complete`, `partial`, `unknown` |
| Freshness | `fresh`, `stale`, `unknown` |

`empty` means a valid selection returned no observations. It may be partial, stale, or unknown on the other axes. `unavailable` means no usable result exists.

### 8.3 Normative Data Requirements

- **DLS-DATA-001:** Every consumed database table or query result **MUST** provide `source-id`, `source-kind`, `as-of`, `retrieved-at`, `completeness`, and `freshness`.
- **DLS-DATA-002:** Provenance and freshness **MUST** remain associated with derived metrics, tables, charts, rankings, and links.
- **DLS-DATA-003:** A presenter **MUST** expose `as-of`, availability, and source identity for every page or view. Source metadata is runtime input outside the dashboard YAML.
- **DLS-DATA-004:** An empty query-level selection **MUST** have availability `empty`; `count` and `distinct-count` over that selection **MUST** produce zero, while other view aggregates **MUST** remain absent. An empty aggregate-local selection does not make a non-empty query result `empty`; its value follows the zero-versus-null rules in Section 7.5.
- **DLS-DATA-005:** An unavailable result **MUST** identify the affected source and **MUST NOT** fabricate observations or carry forward an unmarked previous value.
- **DLS-DATA-006:** A partial result **MUST** identify known missing scope or time coverage and **MUST NOT** be labeled complete.
- **DLS-DATA-007:** A stale result **MUST** retain its original `as-of` value and **MUST** be explicitly identified as stale.
- **DLS-DATA-008:** Availability, completeness, and freshness **MUST** remain separate; `unknown` completeness or freshness **MUST** remain distinct from every known value on the same axis.

---

## 9. Links and Findings

### 9.1 Link Model

A link has `relation`, `href`, and `label`. Allowed relations are `organization`, `repository`, `workflow`, `run`, `issue`, `pull-request`, `evidence`, and `external`. When a relation-specific link field from Section 5.1 is present on a source row, it contains exactly one link object whose `relation` matches the field name.

`dashboard.github-url-base` selects the GitHub web URL base for GitHub-addressable entity links. A deployment that omits it uses `https://github.com`; an enterprise deployment sets it to its GitHub Enterprise web URL base. A presenter **MUST** resolve generated GitHub links against that base, rather than assuming GitHub.com.

A finding is an observation with a stable finding ID, summary, status, severity, observation time, provenance, applicable scope, and zero or more relation-specific link fields. Finding status uses `open`, `resolved`, `dismissed`, or `unknown`. Severity uses `critical`, `high`, `medium`, `low`, `informational`, or `unknown`.

### 9.2 Normative Link Requirements

- **DLS-LINK-001:** Every link **MUST** contain one allowed `relation`, an absolute HTTPS `href`, and a non-empty `label`.
- **DLS-LINK-002:** Links **MUST** retain the provenance and subject association from which they were derived.
- **DLS-LINK-003:** A finding or outcome **MUST** expose relation-specific links to its associated issue, pull request, or run when those associations are available.
- **DLS-LINK-004:** A finding, outcome, or operational-value observation without an available link association **MUST** remain valid and **MUST NOT** contain a fabricated link.
- **DLS-LINK-005:** A relation-specific link field, when present, **MUST** contain exactly one Section 9.1 link object and **MUST NOT** contain a sequence, mapping of multiple relations, or scalar URL.
- **DLS-LINK-006:** A presenter **MUST** render every GitHub-addressable entity exposed in the user experience as a link to that entity when its address is available. This requirement applies wherever the entity is rendered, including identifiers, names, and labels in metrics, tables, charts, rankings, filters, and detail views. A presenter **MUST NOT** fabricate a link when an entity address is unavailable.
- **DLS-LINK-007:** A presenter **MUST**, wherever sufficient GitHub identity is available, resolve and render organizations, repositories, workflows, runs, issues, and pull requests as links to their GitHub web views. It **MUST** use `dashboard.github-url-base` when configured, or `https://github.com` otherwise. A presenter **MUST NOT** infer a link when the entity identity is insufficient or ambiguous.

---

## 10. Built-in Pages

### 10.1 Syntax

```yaml
- id: runs
  kind: built-in
  page: runs
  title: Runs
  icon: play
  class-name: runs-page
```

Allowed built-in page names are:

`overview`, `organizations`, `repositories`, `campaigns`, `workflows`, `runs`, `experiments`, `graders`, `evals`, `usage`, `engines-models`, `operational-value`, `findings`, and `issues`.

The optional page `icon` is the canonical name of an Octicon supported by the presenter. It controls navigation presentation without changing page semantics and defaults to `server`. A validator **MUST** reject names outside the presenter's canonical Octicon set.

The optional page `navigation-label` provides a concise sidebar label when the page title is more descriptive. A dashboard `navigation` section may reference a focused subset of declared pages; omitted pages remain available as deep-link destinations.

A page may set `experimental: true`. Presenters render a visible **Experimental** label with the page in navigation and beside its active page title. This metadata is informational only and does not grant authorization or access to data.

A page may declare `navigation-indicator` with a non-empty `label` and an `any` sequence of bounded source names. Presenters subscribe only to those declared indicator sources, set the navigation item's accessible label to include the indicator label when any source returns a row, and render a status dot as a supporting visual cue. Indicator sources **MUST** be declarative query outputs that already encode the business condition.

A navigation section may set `experimental: true`. Presenters combine pages from all experimental sections into one visible **Experimental** navigation section that is collapsed by default. Activating a direct deep link to an experimental page expands that section. This metadata changes navigation presentation only and does not grant authorization or access to data.

The optional page `class-name` is a canonical identifier that a renderer adds to the page container. It lets a document opt into page-specific presentation without requiring the renderer to infer styling from a page ID or built-in page name. The optional Boolean page `filter-bar` defaults to `false`; `true` adds the shared filter bar while retaining view-mode controls in the page chrome. The optional Boolean `view-mode-control` defaults to `true`; `false` fixes the page to its default presentation and omits desktop and mobile presentation-mode controls. The optional Boolean page `pull-refresh` defaults to `false`; `true` enables a mobile pull-down-to-refresh gesture while that page is active and already scrolled to its top.

For pages that opt in to `filter-bar: true`, the presenter renders a filter bar in the view chrome. The global header horizon control owns a compact picker independently of the active page's filter bar. Its presets include hours, days, and weeks up to a maximum of 30 days, without an all-time option. Custom start/stop inputs appear only for a custom range and reject windows longer than 30 days with a visible validation error. Previously saved longer or all-time horizons resolve to 30 days. Coverage description and precise bounds remain available under a separate disclosure. This shell interaction is implemented by shared presenter controls, not a view-specific mark. The presenter applies edits automatically to matching source fields, treating values for one field as alternatives and filters for different fields as conjunctive. Time-horizon and rollout-mode selections are global client-side settings persisted in local storage. All rollout modes are active by default.

### 10.2 Required Content

- **DLS-PAGE-001:** A built-in page **MUST** contain `id`, `kind: built-in`, and one allowed `page`; an optional title **MUST** be a non-empty string, and an omitted title **MUST** default to the page name with words capitalized.
- **DLS-PAGE-002:** The `overview` page **MUST** expose distinct runtime, security and controls, value and outcomes, episodes and autonomy, cost and efficiency, and evidence-quality summaries ordered by urgency. Each summary **MUST** identify its state, material observed value or unavailable prerequisite, and an investigation target, with provenance and freshness available from its declared sources.
- **DLS-PAGE-003:** The `organizations` page **MUST** expose organization inventory, repository count, workflow count, run count, available usage measures, and data availability by organization.
- **DLS-PAGE-004:** The `repositories` page **MUST** expose repository inventory and rankings by run count, AIC, and available operational value without combining different operational-value definitions.
- **DLS-PAGE-005:** The `workflows` page **MUST** expose declared workflow inventory, active state, rollout mode, latest retained run status and time, and links to dedicated workflow pages. The inventory **MUST** order workflows by latest run time descending, with stable repository and workflow tie-breaks and unobserved workflows last. Run counts, conclusion totals, downstream outcomes, usage, findings, and operational value **MAY** be exposed on the dedicated workflow pages rather than computed for every inventory item. Missing retained runs **MUST NOT** be presented as zero successful or failed executions.
- **DLS-PAGE-006:** The `runs` page **MUST** expose run status trends and counts, terminal conclusions, scope, rollout mode, engine, requested model, resolved model, time, and run links.
- **DLS-PAGE-007:** The `experiments` page **MUST** expose experiment definitions and observed run-to-variant assignments, grader observations, eval observations, outcomes, usage, and operational value without claiming causation.
- **DLS-PAGE-008:** The `graders` page **MUST** keep grader definitions and grader observations distinguishable and expose observed subject, result, score when present, time, and provenance.
- **DLS-PAGE-009:** The `evals` page **MUST** keep eval definitions and eval observations distinguishable and expose observed subject, `YES`, `NO`, or `UNKNOWN` result, evaluation model when available, time, and provenance.
- **DLS-PAGE-010:** The `usage` page **MUST** present each raw-token measure separately from AIC and expose estimated USD, engine, engine version, requested model, resolved model, scope, rollout mode, time, and provenance.
- **DLS-PAGE-011:** The `engines-models` page **MUST** expose model and agentic-engine summaries with AIC totals, estimated pricing, run counts, engine version ranges, plus run-level engine, requested model, and resolved model evidence where available.
- **DLS-PAGE-012:** The `operational-value` page **MUST** expose time-ordered campaign-defined repository metrics with their value identifiers, campaigns, repositories, and observation timestamps. Null observations **MUST** remain unavailable rather than becoming zero.
- **DLS-PAGE-013:** The `findings` page **MUST** expose finding summary, severity, status, scope, time, provenance, and available issue, pull-request, and run links.
- **DLS-PAGE-014:** Every built-in page **MUST** honor the dashboard scope, time, and filters and expose availability.
- **DLS-PAGE-015:** The `campaigns` page **MUST** expose centrally managed campaign inventory, rollout-mode filtering, actual campaign AIC against summed per-run limits without treating missing usage as zero, the complete-attempt AIC allowance, retained usage coverage, and time-ordered successful, failed, and cancelled campaign-run trends.
- **DLS-PAGE-016:** `experimental`, when present on a page, **MUST** be Boolean and defaults to `false`. A presenter **MUST** render an **Experimental** label in every navigation item for that page and beside the active page title.
- **DLS-PAGE-016:** When `class-name` is present, it **MUST** be a canonical identifier and a renderer **MUST** add it to the page container without deriving additional CSS class names from `id` or `page`.
- **DLS-PAGE-017:** The `issues` page **MUST** use the predefined built-in page configuration and the reusable `issue` entity-card definition, bind to a declared query with issue arguments, and drill to each issue's safe GitHub URL.
- **DLS-PAGE-017:** A presenter **MUST** render one filter bar in the view chrome only when that page declares `filter-bar: true`, toggle its tuning controls from the horizon text, and apply valid filter edits automatically. A presenter **MUST** persist time-horizon and rollout-mode settings globally in local storage and activate all rollout modes by default. Unless the page declares `view-mode-control: false`, available view-mode controls **MUST** remain in page chrome when the filter bar is omitted.
- **DLS-PAGE-018:** A routed custom page **MAY** declare `route.title-format: title-case`. Before route-owned data resolves, a presenter **MUST** format the route value by capitalizing its hyphen- or underscore-separated words instead of exposing the raw route slug as page identity. A later route allocation **MUST** replace that provisional identity with the authoritative title.
- **DLS-PAGE-019:** A routed custom page with declared tabs **MAY** declare a canonical `route.tabs-class-name`. A presenter **MUST** apply that class to both loading and hydrated tab sets so route chrome remains structurally and visually stable while data resolves.
- **DLS-PAGE-020:** `view-mode-control`, when present, **MUST** be Boolean. When `false`, a presenter **MUST** use the page's default presentation and **MUST NOT** expose desktop or mobile controls for switching among chart, card, and table presentations.
- **DLS-PAGE-021:** Unless `view-mode-control` is `false`, a presenter **MUST** expose every presentation mode represented by the page's essential views, add a card presentation for every table, and order available controls as **Chart**, **Cards**, **Table**. It **MUST** expose the same modes through keyboard-operable desktop and mobile controls when two or more modes are available, and **MUST NOT** expose a mode control when only one mode is available. Supplemental views remain discoverable through their disclosure controls and **MUST NOT** add presentation modes.
- **DLS-PAGE-022:** `pull-refresh`, when present, **MUST** be Boolean and defaults to `false`. When `true`, a presenter **MUST** enable a mobile pull-down gesture that requests a dashboard refresh only while that page is active and its scroller is already at its top; the presenter **MUST NOT** wire the gesture to any other page or infer eligibility from a page `id` or built-in page name.
- **DLS-PAGE-023:** `mode-indicator`, when present, **MUST** be Boolean and defaults to `false`. When `true`, a presenter **MUST** read a `mode` value from the page's URL query string and render a **Review** or **Live** badge beside the page title only when that value is exactly `review` or `live`; the presenter **MUST NOT** show the badge for any page that does not declare `mode-indicator: true`, and **MUST NOT** infer eligibility from a page `id` or built-in page name.
- **DLS-PAGE-024:** A page's `query-binding`, when present, **MUST** be `view` or `page` and defaults to `view`. `page` **MUST** bind all ordinary views on the active page, across presentation modes, through one abort-scoped canonical subscription, preserve distinct view payload aliases and query contexts, and reevaluate on database changes. Continuation requests **MUST** retain the originating subscription's compilation scope and source set. Authored `data.partial-source` previews **MUST** retain their backend eligibility, phase-gated publication, and pending-cell semantics; publishing a ready preview **MUST NOT** evaluate another view's deferred complete graph. Equivalent dependencies **MAY** share evaluation only within that request. Named elements that own subscriptions **MUST** retain their ownership.

---

## 11. Custom Pages

### 11.1 Syntax and View Classes

A custom page contains a non-empty `views` sequence. Each view has one `data` mapping and one mark. Data marks use an `encoding`; named UI elements use `element`. Any view may declare `subject`, `objective`, and `acceptance` semantic metadata. These annotations compose with those of its named query dependencies; they are not rendered verbatim as visible or accessible content. Any view, including any chart type, may declare `prompt` as `auto` (the default), `none`, or `always`. `auto` enables the shared prompt button when all three effective semantic values are available; `none` hides it; `always` shows it even if semantic values are incomplete. In every mode, the button remains hidden while the view or its independently bound widgets are loading. The generated action defaults to `prompt-level: propose`, since its request can produce a pull request; a view may declare `prompt-level: explore` for read-only investigation. The shared preview uses the composed values without duplicating view metadata. The prompt level is validated against `explore`, `propose`, and `operate`.

**Chart layering.** Inspired by [Vega-Lite's layer composition](https://vega.github.io/vega-lite/docs/layer.html), a `mark: chart` view may replace its single `chart` widget with `layer`. Each leaf selects `chart: area`, `bar`, `line`, `dot`, or `rule`. A group instead contains another `layer` array. Parent encodings are inherited by channel; a child's field definition replaces the entire inherited channel. All leaves share the view's one worker-produced `data.source`, one responsive SVG plot, and one merged color legend. Later leaves paint above earlier leaves.

```json
{
  "id": "observed-credits",
  "mark": "chart",
  "data": { "source": "credit-observations" },
  "encoding": {
    "x": { "field": "observed-at", "type": "temporal" },
    "y": { "field": "aic", "type": "quantitative", "title": "AI credits" }
  },
  "layer": [
    { "chart": "area" },
    { "layer": [{ "chart": "line" }, { "chart": "dot" }] },
    {
      "chart": "rule",
      "encoding": {
        "y": { "field": "reference-aic", "type": "quantitative", "title": "Reference" }
      }
    }
  ]
}
```

The example's `credit-observations` query must declare `observed-at`, `aic`, and `reference-aic`. Compute references, joins, filters, aggregates, time buckets, sorting, and limits in `dashboard.queries`, not in layers. A rule spans the horizontal plot at each distinct observed quantitative `y` value per color series and needs no `x`. Other leaves require one `x` and one quantitative `y`; supported channels are `x`, `y`, `color`, and `href`. Layers have at most eight leaves and four array nesting levels. Single-widget chart behavior is unchanged.

Bar and rule layers accept at most 2,000 source observations per leaf; a larger payload produces an explicit limit message rather than silently truncating observations. Bound these sources with a query `limit`. Line, area, and dot layers retain the shared renderer's bounded mark sampling for dense plots. Invalid or absent numeric observations remain gaps rather than zeroes.

Scale domains default to unions across all leaves, including stacked area totals, with shared axes and a merged legend. Temporal positions are proportional to timestamp; categorical positions share one category domain. Explicit `x` types must be nominal, ordinal, or temporal and retain the selected widget's type constraints; quantitative x scales are not supported. Shared `x` types and `y` units must be compatible. `resolve: { "scale": { "y": "independent" } }` uses a separate quantitative domain and value formatting for each leaf. The presenter replaces the common numeric y labels with an explicit per-layer scale key showing its lower, midpoint, and upper values; guides indicate the same fractional plot positions. This bounded implementation is not a general Vega-Lite interpreter: independent x/color scales, projections, per-layer data/transforms, and other chart widgets are rejected.

A layered view may declare a top-level facet using the view or encoding syntax
below. Facet channels are not inherited into leaves and remain forbidden inside
layer specifications. The worker partitions the already-shaped query result
without reordering, aggregating, or limiting it; each panel renders all leaves
with its own scale domains. Layer bounds and scale resolution apply within each
panel, and sorting and limits remain the responsibility of `dashboard.queries`.

Any view may include the optional Boolean `locked` authoring hint. When `true`, an agent evolving the dashboard should preserve the view and modify it only to correct bugs. `locked` does not affect presentation, accessibility, data processing, or validation of the view's other fields.

Any view may declare `requires` with a `backend` of `static` or `hosted`.
This is a prerequisite on the configured data backend, not on source contents
or authorization. When it is unmet, the presenter shows the required non-empty
`message` instead of loading the view by default. Setting `on-unavailable` to
`hide` omits the view entirely (and `message` may be omitted); `message` is
required for the default `message` behavior. Empty sections containing only
hidden views are omitted. In either case, the page loader, worker compiler,
and view subscriptions exclude that view's query dependencies.
An enabled view sharing a source still loads its own dependencies. A satisfied
hosted prerequisite does not grant permissions: unauthorized or unavailable
runtime sources retain their normal explicit failure states.

Any page may declare `requires: { capability: cli-queue }` or
`requires: { capability: admin }`. The mapping accepts only the required
`capability` key. `cli-queue` uses the same explicit transport capability as CLI
actions; `admin` requires the server-issued administrator capability from an
authorized hosted administrator session with current
step-up assurance. Neither is inferred from URL parameters, the data backend
type, or credential reach. Without the capability, the presenter omits
the page from desktop and mobile navigation and route activation, and source
selection excludes its queries and pagination. A direct link falls back to
the default available page without loading the unsupported page. Requirements
remain in lazy page stubs. Commands and command-output pages require `cli-queue`;
published static dashboards and hosted backends without the local
queue do not expose them. Governance requires `admin`, independently of CLI queue
availability. These presentation gates grant no execution authority.

A table or list encoding may declare row `actions`. A `copy-prompt` action materializes an intent as an icon-and-label button and may reference a row-placed `gh agent-task create --from-file -` CLI action by `action` ID. In canvas CLI-action mode the prompt preview then starts an agent task after explicit approval; in other modes it retains the clipboard control. A `cli-action` action references a dashboard CLI action by `action` ID and is rendered only in canvas CLI-action mode; its `context` fields provide validated scalar values for `{{field}}` tokens in the declared command. Each action declares `level: explore | propose | operate` independently of presentation, and may declare a concrete `verb` and canonical Octicon `icon`; omitted icons resolve from the semantic level and verb. Existing actions without a level migrate as `propose` for prompts and `operate` for CLI commands. The dashboard's `cli-actions` declarations use the same `level` and optional `verb` keys; an explicit row action may override the referenced command's level. An `explore` CLI command must be a supported read-only operation without optional boolean arguments; this is a safety validation, not a rule for inferring level. The `label` may be omitted to use `Investigate` or `Propose fix`; operational actions must supply a concrete label. Each action has a non-empty `presentation` and a non-empty `context` sequence of unique source fields. An action may use `when` with a source field and scalar `equals` value to limit the action to rows whose field has the same scalar type and value. Activating a prompt action opens a modal preview containing the complete prompt; opening the preview does not write to the clipboard or start a task. Activating a CLI action opens the same per-run command approval used by dashboard-level CLI actions. The prompt contains the intent followed by only the available scalar values selected by `context`, serialized as an ordered JSON object and explicitly identified as untrusted data. Link objects contribute only their HTTPS `href`. Authors do not interpolate row values into prompt intents, and undeclared row fields are not copied.

A custom page may also contain a non-empty `sections` sequence that groups its views for presentation. Each section contains a unique canonical `id`, optional `title` and `description`, one `layout` value of `full`, `wide`, `narrow`, or `horizontal`, and a non-empty `views` sequence. `horizontal` spans the available width and presents its views as adjacent boxes that wrap responsively while preserving source order. Section view references must name every view on the page exactly once and preserve view declaration order. An omitted section title defaults from its section ID. A section may pair `count-source` with a non-empty `count-label` to expose the effective source row count in its heading. Alternatively, `count-sources` pairs with `count-field` and `count-label` to sum the available numeric metric values into the heading; unavailable sources do not contribute a fabricated zero. A horizontal section presents this count summary as its primary heading.

The shared action model has a separate `ui` level for native dashboard-local
and account-session operations, such as resetting local storage, clearing the
cached app, logging out, or switching accounts. These are `type: ui` actions,
not read-only investigations, proposals, or control-plane operations. They
never generate agent prompts or execute CLI commands. Account-session actions
may call the dashboard's existing authenticated session endpoints; `ui` does
not grant repository or operational authority. Native controls retain their
browser/session lifecycle handling because those interactions cannot be
expressed by a query or a prompt. Their labels and icons use the same normalized
action presentation as other levels. UI actions do not require an agent preview
or operational approval; destructive local actions still require their existing
confirmation. CLI declarations, row prompt/CLI/external-link actions, and
`prompt-level` must reject `ui` rather than treating it as another execution tier.

#### Treemap charts

A chart may additionally declare `chart: treemap`, extending the widget and
`section` vocabulary in **DLS-VIEW-005**. Its unaggregated nominal
or ordinal `x` supplies leaf labels and its unaggregated quantitative `y`
supplies area. Optional nominal or ordinal `color` supplies categorical color;
optional nominal or ordinal `section` nests leaves inside one labeled group
rectangle per distinct field value. Without `section`, all leaves share one
flat layout. This is a Vega-inspired treemap layout, not a native Vega-Lite
mark or an arbitrary Vega expression interface. It does not consume
parent/child IDs or infer hierarchy from labels.

`treemap` options are allowed only on treemap charts. `method` is `squarify`
(default), `binary`, or `slicedice`. `ratio` is a finite number from 1 to 5,
defaulting to 1.618, and controls the preferred aspect ratio in squarify only.
`padding` is a finite number from 0 to 10, defaulting to 0.5, expressed in a
100-by-60 drawing plane. Slice-dice alternates orientation between group and
leaf levels. Input query order determines deterministic layout order.

- **DLS-VIEW-046:** A treemap **MUST** use the field roles above, **MUST NOT**
  aggregate encoded fields in the presenter, and **MUST** declare `data.limit`
  from 1 to 100. Aggregation, filtering, ordering, and limiting **MUST** execute
  through declarative queries in the data worker before geometric tiling.
  The active view **MUST** remain subscribed with an abort-scoped lifetime.
- **DLS-VIEW-047:** Positive finite values **MUST** determine proportional
  rectangle area before padding and optional group headings. Missing and
  zero values **MUST NOT** receive area and **MUST** be reported explicitly.
  Negative or non-finite values, or more than 100 input leaves, **MUST** render
  an explanatory unavailable state rather than silently clamp or truncate.
  Every rendered leaf **MUST** expose its label, group when present, and
  formatted value without relying on color alone. Encoded safe links **MUST**
  remain keyboard-operable. A small rectangle **MAY** omit visible text but
  **MUST** retain its complete accessible name and visible, viewport-bounded
  tooltip on hover, keyboard focus, or tap. Escape **MUST** dismiss the tooltip.

#### 11.1.1 Route-Bound Page Templates

A custom page may declare a constrained route binding, a navigation parent, or both:

```yaml
- id: repository-detail
  kind: custom
  title: Repository
  route:
    hash-query-parameter: repository
  views:
    - id: repository-authored-workflows
      data:
        sources: [workflows]
      mark: element
      element: repository-workflows
```

The route selects the page through `#page-<page-id>?<parameter>=<value>`, with the page ID and query components percent-encoded as defined by URI syntax. For `#page-repository-detail?repository=github%2Fgh-aw`, the decoded, trimmed route value is `github/gh-aw`. A non-empty route value allocates that custom-page instance: the presenter uses it as the page title and final breadcrumb label and supplies it as an opaque route binding to route-aware named elements. A missing or empty value leaves the declared page title in place and supplies an empty binding.

This binding is constrained templating, not general string interpolation. A presenter treats the value as text, never as markup or executable content, and does not substitute it into arbitrary document fields. A named element may apply stricter domain validation before using the value for filtering or links. A route-aware named element may replace the provisional route-value title and description with human-readable text from its selected declared-source row; the presenter must apply the same text-only treatment to that allocation.

An element view may declare a compact `title-link` beside an allocated page title. Its `href-field` names one relation-specific link field and its `identifier-field` names one scalar field declared by the same selected source. When both values are present, the presenter renders the identifier as `#<identifier>` and uses the link object's HTTPS `href` as the target. Missing or invalid runtime values leave the title unlinked. This supports issue and pull request numbers as well as workflow run IDs without parsing identifiers from URLs.

`navigation-page` identifies a different declared page whose navigation item remains current while the custom page is active. The presenter uses that page as the custom page's parent breadcrumb. This supports detail and diagnostic subpages without requiring presentation components to contain navigation policy.

For a stable view title across hierarchical drill-downs, `route.breadcrumbs`
declares a sequence of one to eight `{page, parameters?}` mappings. Each `page`
must name a declared page and must appear at most once. Optional `parameters`
is a sequence of one to eight unique route parameter names. The presenter keeps
the active page's declared title (including during loading and route allocation)
and renders this chain instead of its inferred breadcrumbs. A breadcrumb with no
parameters uses its page's declared title; otherwise its decoded, trimmed values
are joined with `/`, applying that page's `title-format` when declared. Missing
values stop the chain rather than creating a misleading or unbound link.
Parent links accumulate only the declared parameters of preceding and current
entries, percent-encoded through URL query serialization. The active entry is
text with `aria-current="page"`. Labels remain text, not templates or markup.
The navigation parent remains independent of this presentation hierarchy.

For example, a report drill-down can retain the title **Value report**
and declare:

```yaml
route:
  navigation-page: value
  hash-query-parameter: report
  breadcrumbs:
    - page: value
    - page: value-report
      parameters: [report]
```

A custom page may set `retain-on-navigation: true` to keep its loaded view visible
while another route is active and until a new subscription rebinds on return.
Scroll and disclosure state are restored on re-entry. A route with
`navigation-page` may set `availability-view` to the ID of a view on that page:
when its declared source cannot produce a selected row, the presenter shows a
single recovery state instead of the ordinary sections. An incomplete but
available source retains the page content with an explicit partial-data notice.
Routes may supply `availability-message` and `partial-message` to explain
those states in user-facing terms.

A built-in or custom page may declare `query-binding: page` to evaluate all its ordinary
views together in one abort-scoped canonical query subscription across presentation
modes, without marking inactive views unavailable. The default,
`query-binding: view`, keeps ordinary views independently subscribed. Page
binding is useful when multiple widgets consume the same expensive derived
source. Equivalent scoped dependency graphs may be evaluated once within that
request, but each view retains its own payload alias, filters, ordering, limits,
and evidence quality. Different route, time, search, or local-filter contexts
must not be conflated. Named elements that own their subscriptions retain that
ownership. Database changes refresh the active subscription; leaving the page
or changing its query context cancels and replaces it. This is request-local
work sharing, not a cross-request result cache. Repository overview and Campaigns
both opt in to this same mechanism; neither requires a worker result cache.
Their page declarations are JSON-only. The generic presenter interprets the
binding flag, and the canonical compiler shares equivalent scoped dependency
graphs without inferring behavior from page, view, or source identifiers.

| Semantic view | `mark` values | Required encoding |
|---|---|---|
| Metric | `metric` | `value` |
| Table | `table` | `columns` |
| Chart | `chart` | `x`, `y` |
| Named UI element | `element` | no encoding; one `element` name |
| Callout | `callout` | no encoding or data; one `callout` mapping |

Allowed encoding channels are `value`, `columns`, `x`, `y`, `color`, `section`, `reference`, `href`, and the chart-only `facet`, `row`, and `column` facet channels. `columns` is a non-empty sequence of field definitions. A line chart's `y` channel may be a sequence of two to eight quantitative field definitions, rendered as one named series per field; such a chart does not also encode `color`. Other channels contain one field definition. The `href` channel references one relation-specific link field or one declared `campaign-dashboard-link`, `repository-dashboard-link`, `workflow-dashboard-link`, or `command-dashboard-link` field; it does not select from multiple links. Command dashboard links navigate to local operational history, not canonical evidence. The quantitative `reference` channel is available only to dot charts and renders each distinct value as a horizontal reference line in the corresponding color series. The nominal or ordinal `section` channel is available to horizontal bar charts for section headings and to treemap charts for nested group rectangles, in first-appearance order.

Scatter charts may declare `scatter: { cluster: false }` when every observation must remain individually represented, including observations with identical timestamps and values. `scatter` is a mapping allowed only on scatter charts; its only key, `cluster`, is Boolean and defaults to `true`. An explicit `false` disables both clustering and render-time point sampling, preserving the precise timestamp and value of every query-produced observation. Authors should consider the rendering cost of their retained dataset before disabling reduction.

Chart presenters MUST assign each non-semantic nominal category to a stable palette slot from its identity rather than its row, sort, or visibility position. The shared presenter normalizes the identity with Unicode NFKC, trimming, and lowercase conversion, then applies 32-bit FNV-1a and selects one of the twelve chart palette slots by modulo. This deterministic hash is the fallback for categories not previously observed, including new campaign and repository identities. Semantic status colors override the resulting palette color. The reserved `value` identity represents an ungrouped series and always uses the first palette slot; only an empty identity falls back to its local series position. Marks and legend swatches MUST resolve the same identity through the same mapping.

A metric view may select the reusable card widget with a `metric` mapping. The mapping uses `style: card`, one canonical Octicon `icon`, one `tone` value of `attention`, `danger`, `neutral`, or `review`, and a canonical `navigation-page`. A card may opt into a CSS number animation with `animate: number`; whole-number values then increment from zero to the declared target, while assistive technology receives the final value. A metric card renders unavailable evidence as an absent value rather than zero and links to the declared dashboard page without deriving navigation from the view or source identity.

Field `type` values are `nominal`, `ordinal`, `quantitative`, and `temporal`. When omitted, type defaults to the intrinsic field type. A field title defaults to its kebab-case field name with words capitalized. A temporal field may set `format: human-friendly-timestamp` to present recent values relative to the present (`just now`, `5 minutes ago`, `yesterday`, or their future equivalents), values less than seven days away by relative day, and older values as a concise UTC date with the year omitted when it matches the current UTC year. Presenters must retain the original timestamp semantically and expose the exact UTC date and time alongside relative or abbreviated text. A nominal or ordinal field may set `format: workflow-relative-path` to remove a leading `.github/workflows/` from its presented value while retaining the `.md` extension. A nominal or ordinal field may set `format: workflow-identity-label` to present a `{owner}/{repository}:.github/workflows/{workflow}` identity as `{workflow} ({owner}/{repository})`. A nominal or ordinal table column may set `format: workflow-run-url` to present a canonical `https://github.com/{owner}/{repository}/actions/runs/{run-id}` string as an external link whose text is the workflow run ID. A nominal or ordinal table column may set `format: shortened-url` to present an HTTPS URL as an external link whose intermediate path after the origin is replaced by `...`, while retaining the final path segment. Values that do not match the selected URL format remain plain text. Formatting affects presentation only; filtering, grouping, aggregation, ordering, links, and source values continue to use the complete value. A field may reference one dashboard unit through `unit`; the unit applies to metric, table, and chart value presentation. `published-at` is temporal.

Callout views declare static explanatory content rather than logical-source observations. A callout requires non-empty view `title` and `description` fields plus a `callout` mapping with a non-empty `label` and canonical Octicon `icon`; it does not declare `data` or `encoding`.

A smell row identifies one detected condition with a stable `smell-id` and unique `smell-observation-id`. `smell-category` classifies the condition, `smell-severity` expresses its consequence, `smell-summary` states the finding, `smell-evidence` records the supporting observation, and optional `smell-recommendation` carries bounded remediation guidance. `agent-smells` contains native behavioral audit assessments, `workflow-smells` contains deterministic workflow configuration defects, `security-findings` contains detected unsafe behavior, and `control-plane-smells` contains policy, rollout, or campaign inventory defects. Producers **MUST NOT** emit a smell solely from an agent's disabled, blocked, slow, or stale runtime state. A workflow-attributable smell **SHOULD** include the narrowest available evidence link. The canonical Home attention view normalizes all four smell sources into independently attributable attention signals.

The optional table-column field `display` is `text`, `status`, `grader-status`, `mode`, `active-state`, `label`, `ref`, `digest`, `outcome-link`, `run-link`, `repository-link`, `workflow-link`, or `evidence-link` and defaults to `text`. It selects presentation independently from the field name. The optional table-column field `filter` is boolean and defaults to `true`; `false` excludes the column from interactive facet filters without changing its display or sorting. Named UI element values are `campaign-route`, `workflow-route-page`, `outcome-detail`, `outcome-detail-section`, `problem-detail`, `entity-route`, `configuration-policy`, `measure-history`, `factory-header`, `factory-floor`, `link-button-list`, and `markdown`; renderers dispatch these values without inferring behavior from page IDs, view IDs, or source contents.

A dashboard CLI action placement is `toolbar`, `settings`, `view`, or `row`. A `view` action is rendered only by the view that references it and is not duplicated in Settings or the global toolbar.

CLI command text supports literal `{{field}}` substitutions, not expressions or
shell evaluation. Row fields remain bounded, validated command tokens.
`{{cao-url}}`, `{{cao-repository}}`, and `{{cao-repository-url}}` are reserved
host-context placeholders: the public dashboard base URL with a trailing slash,
the control repository slug, and its GitHub repository URL, respectively.
They MUST NOT be required as query-produced row fields, supplied by selectors,
or overridden by browser request values. Public dashboards resolve their own
page base URL; local executable hosts resolve the published Pages URL or an
explicit public URL override. A local capability URL MUST NOT become a backlink.
Context URLs MUST use HTTPS without credentials, query, fragment, whitespace,
or command delimiters. Preview and queue resolution MUST use the same context;
missing context MUST disable execution and fail queue admission. Retries MUST
re-resolve current context and reject a changed approved command.

A list may bind its primary `action` to every worker-produced source row with
`list.action-context`, a non-empty sequence of unique scalar field names.
The approval dialog lists one fully resolved CLI command per row, and explicit
approval enqueues each invocation independently. The source query owns target
selection, distinctness, and ordering; display limits must not narrow the batch.
Empty, unavailable, truncated, paginated, or invalid target sets cannot execute
and must never fall back to the dashboard's control repository. This binding
requires a non-lazy list. Additional `list.actions` remain independent controls.
Partial inventory evidence does not truncate a fully returned query result:
the dialog MUST warn that only the explicitly listed targets are included,
and MUST NOT imply that unobserved repositories or campaigns will be targeted.
Unknown completeness remains unavailable for batch approval.

An action MAY declare `preview-command`, a non-empty, single-line token prefix of its
`command`, for a compact batch preview. The dialog shows that basic command for
the first target with the selected argument flags and the total target count.
All fully resolved commands MUST remain available in an expandable disclosure;
copying and approval MUST use the full commands, never the abbreviated preview.

Batch actions with boolean arguments open the shared approval dialog before
queuing any commands. Each checkbox choice applies to every target in the
displayed batch. The built-in update and upgrade actions declare an **Auto merge**
checkbox, unchecked by default, which adds `--auto-merge` only when selected.
This gh-aw option requires **Create pull request**.

For CLI-action presentation and confirmation, trusted canvas authoring and
authorized hosted execution use explicit host capabilities; there is no
standalone Node admin preview.
These capabilities do not change the document vocabulary, copy-only
restrictions, action authority, or per-run confirmation requirements. Commands
start headlessly after confirmation in the web interface, without a second
confirmation in the Copilot app. The approval dialog executes at most once and
opens the command's full-page, read-only console, with no separate
terminal-window option. Its first output line shows the executed command.
Terminal emulation cannot be expressed by data marks and does not query or
derive dashboard evidence.
Displayed `gh aw` and `gh cao` commands use a pseudo-terminal with ANSI color
enabled, combining stdout/stderr in terminal order.
Prompts and pagers are disabled. Stdin-backed commands and
preparation reads retain separate pipes.
The console preserves standard, bright, indexed, and true-color ANSI sequences
across streamed chunks and retained replay on both stdout and stderr, without
overriding command-provided colors or assuming that stderr always means failure.
Authentication and machine-readable preparation reads do not force terminal
formatting.
Completion, failure, timeout, or cancellation leaves the console open.
Back navigation detaches without canceling work, and retained output remains
available in Commands. Another run requires reopening the approval dialog.
The console offers **Copy** instead of a cancellation button. It copies the last
HTTPS pull-request URL found in retained output, or the transcript when no PR
URL is available. Copied transcripts are plain text with ANSI formatting removed;
displayed and retained output keeps its colors. Result URLs support GitHub Enterprise hosts, ports, and path
prefixes, exclude credentials, query strings, and fragments, and persist with
the command. **Open pull request** opens the result separately without opener
access. Other terminal URLs use HTTP/HTTPS link detection with the same
separate-tab behavior.

A list may declare `list.actions`, a non-empty sequence of unique view-placed
CLI action IDs, alongside its existing `list.action`. Actions render in declared
order beside the list heading, including when the list has no rows.

A list shows its declared description inline in the header. Presenters do not
repeat that same explanation in a separate heading tooltip.
View descriptions do not automatically create help icon buttons. Section
descriptions remain available as accessible descriptions; explicitly declared
tooltips retain their configured controls.

A view-placed `operate` CLI action may declare a reusable `selector` dialog
configuration: `label`, optional `placeholder`, `debounce-ms` (100–2000),
`query: { command: "gh search repos", fields: [...], limit: 1–50 }`, and
`mapping: { value, label, description?, target }`. Mapping fields must be selected
by the query; `target` must name one command template token. For example:

```json
{
  "id": "add-repository",
  "level": "operate",
  "label": "Add repository",
  "icon": "plus",
  "placement": "view",
  "command": "gh cao repos add {{repository}}",
  "selector": {
    "label": "Repository",
    "debounce-ms": 300,
    "query": { "command": "gh search repos", "fields": ["fullName", "description"], "limit": 20 },
    "mapping": { "value": "fullName", "label": "fullName", "description": "description", "target": "repository" }
  }
}
```

Selectors render only in executable canvas mode. Lookup is a bounded read-only
GitHub CLI search on the canvas server, not a query over canonical dashboard
evidence. The server resolves the query and mapping from the declared action ID;
clients cannot supply a command, fields, or mapping. Search input is a separate
argv value, never shell-interpolated. These transient external choices do not
become dashboard sources. Changing input immediately invalidates prior selection
and cancels stale lookups. Closing or detaching the dialog cancels pending work.
The presenter must label the textbox, announce loading, empty, and failed lookup
states, and keep results in a viewport-bounded, independently scrollable list.
Queries return at most their declared `limit`, even when hundreds of repositories
match. At that limit, the presenter tells users to refine their search rather
than implying that every match is shown. Slow lookups retain an explicit loading
state and, after two seconds, explain that users can continue typing or cancel.
The server bounds each GitHub CLI lookup to 15 seconds. Search input and
cancellation remain available while waiting; abandoned lookups and their
slow-search timers must not update later searches. Presenters support keyboard
selection and disable execution until a mapped value
passes command-template validation. Selection never executes a command; fresh,
exact per-invocation approval in the canvas remains mandatory. The native
extension validates the reviewed command without a second host confirmation prompt.

A CLI action may declare `copy-only: true`. Presenters must expose its resolved
command for copying and must not execute it.

A dashboard CLI action command is a single-line GitHub CLI invocation.
Presenters recognize `gh cao ...`, `gh aw ...`, `gh workflow run <workflow> ...`, and
`gh agent-task create --from-file -` by default; the workflow command triggers a workflow that declares `workflow_dispatch`, while the agent-task command receives the reviewed prompt through standard input.
`gh cao <command>` is a first-class cli-actions command, not a shell alias.
Local and Copilot hosts MUST launch the configured GitHub CLI executable with
an argument vector beginning with `cao`, preserving the approved subcommand and
arguments. They MUST NOT rewrite it to a repository shell script or a Node.js operator command.
Native CAO actions use the same declaration resolution, per-invocation approval,
durable queue, output streaming, cancellation, and retry protocol as other CLI
actions. Recognizing the prefix MUST NOT widen the experimental
[hosted executor's allowlist](../specs/server-hosted-cli.md): commands that only
edit a local checkout remain local-only.
The agent-task invocation follows the [GitHub CLI `agent-task create` reference](https://cli.github.com/manual/gh_agent-task_create), where `--from-file -` reads the task description from standard input.
Workflow dispatch commands may use `--repo`, `--ref`, and non-file-backed
inputs through `--raw-field` (`-f`). Presenters reject file-reading workflow inputs
such as `--field` (`-F`); the only stdin-backed input is the reviewed prompt for
`gh agent-task create --from-file -`. Presenters execute commands directly without
a shell and require explicit approval for every invocation. Workflow dispatch actions do not declare
boolean `arguments`; inputs belong directly in the command as raw fields.

A list view declares `list.style: cards`, a canonical Octicon `list.icon`, and one view-placed dashboard CLI action through `list.action`. It encodes a non-empty `columns` sequence: the first column is the card title and remaining columns are labeled details. Optional row actions use the same declarative action contract as tables. The view-level action is rendered once beside the list description; each matching row action is rendered on its card. Empty and unavailable states remain distinct.

The dashboard may declare reusable `card-templates` and reusable `views`. Each card template has a canonical `id`, a fallback canonical Octicon `icon`, one title field definition, and sequences of label and detail field definitions. An optional `icon-field` selects a row-provided canonical Octicon and falls back to `icon` when the field is absent or empty. A declared `subtitle` is presented as secondary text beneath the card title; it is omitted when its field is absent from the presented row or resolves to an empty value. An optional `detail-labels` value of `hidden` (the default) or `visible` selects whether detail titles are presented beside their values instead of being exposed only to assistive technology. A card template may also declare `status`, `timing`, `actions`, and a default `drill`. The presenter uses the template drill only when its entity-card list or responsive table card does not declare a drill; an explicit view drill takes precedence. `status` is a mapping containing `field`, an optional `fallback-field`, and an optional `title`; the presenter resolves the observed status or conclusion value to a status icon and tone in place of the template icon, using `fallback-field` when the primary field has no observed value, and exposes the value as text. `timing` is a non-empty sequence of field definitions that each add a canonical Octicon `icon`; the presenter renders them beside the card in declared order and omits any entry whose value is unobserved. `actions` is a non-empty sequence of row-placed CLI action references, each declaring its string row-field `context` and an optional field/equality `when` condition; the presenter renders a labeled action button only when its condition matches. A label field definition declaring `display: ref` presents a Git reference such as a branch name. Subtitle, status, timing, icon, action, and reference presentation is display treatment only and **MUST NOT** rewrite source evidence. Cards show at most four label and metric pills combined, in declaration order (labels before metrics); remaining values stay available in the underlying table data. Each reusable view is a complete view definition with a unique canonical `id`. A page may look it up by placing that identifier in its `views` sequence. The resolved view owns its title, query selection and arguments, card template, actions, and optional drill behavior; presenters must not supply or override those behaviors with entity-specific implementation code.

An entity-card list declares `list.style: entity-cards` and references one declared template through `list.card`. It may define explicit `list.drill` behavior. Optional `list.layout` is `rows` or `grid` and defaults to `rows`; `grid` changes responsive placement only and preserves card content and drill semantics. Optional `list.appearance` is `grouped` or `marketplace`. `grouped` renders the list as a single rounded, bordered container with hairline row separators and a trailing disclosure chevron on each drillable row, matching a native grouped list presentation. `marketplace` renders a bordered package directory with prominent row icons, descriptions, publisher metadata, labels, actions, and disclosure chevrons. Both appearances change container and row chrome only and preserve card content and drill semantics. An `external` drill names the safe HTTPS, `#page-` dashboard route, or dashboard-link field opened by the card. A `query` drill names a declared destination page and query, a scalar `title-field`, and one or more name/field argument bindings. The presenter places the query, row-derived title, and arguments in the hash route, so each destination remains reloadable and browser history can continue through an arbitrary number of declared query drills. A destination view binds those named route values to query fields with `data.arguments`; every declared argument becomes an equality predicate in the data worker, and a missing value fails closed to an empty match. The destination title is accepted only when the query is declared; otherwise the page keeps its configured title. Optional `list.view-all` declares a trailing navigation footer with a required `page` that must reference a declared dashboard page and an optional `label` that defaults to `View all`; it adds one declared dashboard route link beneath the list and changes neither card content nor drill semantics.

The `link-button-list` element presents one declared source as an inset grouped list of navigation rows, matching a native mobile settings list: an uppercase section caption, a rounded container, one tinted Octicon badge per row, hairline separators inset to the label, and a trailing disclosure chevron. Its config declares `label-field`, `link-field`, `fallback-icon`, an optional `icon-field`, optional `label-badge-field`, optional `indicator-field` and `indicator-label-field`, and an optional `empty-message`; link values use the same safe dashboard-link or HTTPS-link contract as other dashboard links. A non-empty `label-badge-field` row value appears as a neutral text badge beside the row label. When an indicator is present, it is rendered as an accessible trailing status cue without changing the declared link target.

A list or interactive table view may declare a `filter-bar` top row. This is distinct from the page-level Boolean `filter-bar`. Its non-empty `filters` sequence defines dropdown controls in display order. Each control has a unique canonical `id`, a human-readable `label`, and non-empty `groups`. Each group declares its caption (`label`), the exact view-source `field` to filter, a separate option `source`, its string `value-field`, and an optional string `label-field` (defaulting to `value-field`). Filter fields must be unique within a view. Option sources must be registered canonical sources or declarative queries, never values discovered from rendered cards.

The Issues view declares separate Labels and Status dropdowns in the declarative filter bar. Status filters by the observed open state or closing reason, including Unknown when status evidence is missing. Labels filters existing safe-output card badges, **not GitHub issue labels**, which are not retained in the canonical issue evidence:

In full-view card mode, the shared filter row stays above the independently scrolling cards. Desktop uses compact Primer dropdown buttons; mobile uses touch-sized rounded controls and grouped options with explicit apply and clear actions.

An entity-card list may set `lazy-list: true` to request bounded canonical query pages and append cards as the load boundary approaches the viewport. An accessible **Load more** button remains available without an intersection observer; failed requests expose a manual retry. Existing cards retain focus and identity, and detaching the owning view disconnects its observer and ignores late responses. This changes presentation and transport only, not query ordering, filtering, or evidence completeness.

A table with `lazy-list: true` may declare `encoding.card` as a reusable dashboard card-template identifier to present its rows as cards at every viewport width, rather than only in responsive card mode. Other marks, non-lazy tables, and unknown template references fail validation. The declared columns still define the view payload and available card fields; lazy-list pagination and explicit card drill behavior remain unchanged. The Commands table uses this presentation, with its durable command route opening the interaction-only `command-terminal` element as a modal console log. Closing or escaping the console returns to Commands and detaches output without cancelling execution; cancellation and retry remain separate explicit controls.

```json
{
  "filter-bar": {
    "filters": [{
      "id": "labels",
      "label": "Labels",
      "groups": [
        {
          "label": "Safe output",
          "field": "safe-output-type",
          "source": "issue-safe-output-label-options",
          "value-field": "safe-output-type"
        }
      ]
    }, {
      "id": "status",
      "label": "Status",
      "groups": [
        {
          "label": "Status",
          "field": "issue-status-detail",
          "source": "issue-status-label-options",
          "value-field": "issue-status-detail"
        }
      ]
    }]
  }
}
```

- **DLS-VIEW-031:** A view filter row **MUST** be configured exclusively by `filter-bar.filters` and **MUST NOT** infer behavior from a page, view, source, or card-template identifier.
- **DLS-VIEW-032:** Option selection, distinctness, ordering, and source derivation **MUST** be declared in Dashboard Language queries and executed through the production query boundary. Option sources **MUST** retain the view's declared scope, route arguments, global filters, and time window, but **MUST NOT** be narrowed by local selections, result search, result ordering, or card pagination.
- **DLS-VIEW-033:** Applying selected values **MUST** compile exact `in` predicates for the declared fields, with OR within each field and AND between fields. Local filters **MUST** affect only the owning view, apply before limiting or pagination, and fail closed for missing row fields. Clearing a field removes its local predicate; clearing the row **MUST NOT** clear page-wide filters or another view's selection.
- **DLS-VIEW-034:** Presenters **MUST** provide labeled, keyboard-operable grouped checkboxes, an explicit apply action, selected counts, a clear action, Escape dismissal, and honest loading, empty, partial, invalid, and unavailable option states. The row and clear action **MUST** remain available when no cards match. Local selections **MUST** survive worker refreshes and view-mode changes; active results and option sources **MUST** remain subscribed for the active page's abort-scoped lifetime.

The `all-campaign-memory` element presents the repository-memory branches for every campaign in one page. Its declared source supplies `campaign` and `campaign-name`; selecting a campaign updates the existing repository-memory file browser in place and does not navigate to a campaign route.

The named element owns the interactive file tree, which is not expressible with the standard marks: folder chevrons reflect expansion, each nesting level uses a consistent indent, and single-line filenames truncate with an ellipsis. File icons describe content (`markdown` for Markdown, `file-code` for JSON, JSONL, and YAML, and `file` for plain text); they do not imply an unobserved diff status. The full path and file size remain available through the accessible name and tooltip.

An element that presents counted summary boxes may declare `config.labels`, a mapping of canonical label identifiers to **plural text variables**. A plural text variable is a mapping containing exactly the non-empty strings `singular` and `plural`. The presenter selects `singular` when the presented count has an absolute value of one and `plural` otherwise, so a box reading `1 Repositories` becomes `1 Repository`. A plural text variable is author-declared display text only: it does not change counting, filtering, aggregation, ordering, or source values, and an undeclared label keeps the element's built-in text. The `factory-floor` element declares the `repositories`, `successful-runs`, `dispatches`, and `value-gains` labels for its overview boxes. It may set `config.animate: number` to increment whole-number counter values from zero to their declared targets with CSS. The separate `factory-header` element owns campaign status, retained-output summary, work-in-motion state, and weekly rhythm. Authors reconstruct the complete campaign overview by declaring these elements as independent views in the required order. The `factory-header` and `factory-floor` identifiers are retained for Dashboard Language compatibility.

`factory-header` and `factory-floor` are independently loading named elements. Their page shell does not wait for a combined page projection: each declared presentation query is bound to the view separately. An unresolved source places only its dependent widget in a loading state; resolving or refreshing one source updates only widgets that consume that source. A page-level subscription must not emit aliases for independently bound sources it did not request.

The Overview floor shows campaign health, repository coverage, **Issues & PRs**,
and **Value gains**. The restored issue counter counts retained canonical issue
and pull request safe-output records and links to Issues. The legacy Value gains
counter counts observed grader results and links to Graders; its supporting text
explicitly identifies observations rather than verified improvements. Both
counters receive bounded, presentation-ready Dashboard Language query results.
Missing or unavailable evidence remains unavailable, distinct from an observed
zero. Four stations form one row on desktop and two rows on narrow screens.

`factory-header` may declare the boolean `config.browser-first-load` (default
`false`) to present the browser's first-import lifecycle instead of campaign
status until a complete local snapshot is available. This flag is supported
only by `factory-header`. It does not change the element's declared queries:
browser-local import progress and dismissal are transient interaction state,
not canonical campaign evidence. An incomplete or failed first import must not
be represented as an idle campaign. Backend-backed dashboards and refreshes
with an existing complete snapshot must not enter this first-import state.

`factory-header` may declare the boolean `config.data-status` (default `false`)
to show a labeled dashboard refresh/import indicator using the same icons and
feedback as the top-right dashboard status. This flag is supported only by
`factory-header`. The indicator observes transient pipeline and rate-limit
state, not canonical query evidence or upstream collection health. It MUST
distinguish an unconfirmed or failed import from a completed refresh, retain
the last complete snapshot time when available, update without navigation,
and stop reacting when its owning header is disposed.

The `problem-detail` element consumes its declared, route-scoped problem query
through the active page's abort-scoped subscription and updates retained
evidence without rebuilding its repair control or resetting log scroll position.
Its specialized definition-list and
raw-log presentation cannot be expressed by the existing table or card marks.
Loading, incomplete, unavailable, and complete empty evidence remain distinct;
missing telemetry must not imply automatic model selection or an initialization
failure. The element and its repair prompt release reactive resources when
their owned roots detach. Campaign navigation consumes already route-scoped
worker payloads rather than filtering campaign rows in the browser.

A chart may set `chart` to `area`, `line`, `dot`, `bar`, `horizontal-bar`, `pie`, `heatmap`, `histogram`, `scatter`, `swimlane`, or `treemap`. When `chart` is omitted, temporal `x` has a line time-series default and any other valid chart has a bar default. Area charts show a quantitative `y` over an ordinal or temporal `x`; temporal values use the same explicit UTC bucketing and ordering machinery as line charts. An area chart without `color` fills from zero to its values. When `color` is present, the presenter groups by that nominal or ordinal field and stacks the non-negative series in deterministic series order, matching Vega-Lite's normal stacked-area behavior. Version 0.1.0 exposes no separate stack control; authors use existing aggregation on `y` and must not use area charts for negative contributions. Line, dot, and scatter charts use temporal `x`. Dot charts preserve exact timestamps, do not connect observations, and may encode quantitative `reference` values as horizontal lines. Scatter charts preserve exact timestamps as proportional positions on the time axis and do not connect observations. A pie chart uses nominal or ordinal `x` for categories and quantitative `y` for values. A horizontal bar chart uses nominal or ordinal `x` for text labels, quantitative `y` for left-aligned bars, and must declare `data.limit` no greater than 100. A histogram uses nominal or ordinal `x` to identify each sample and automatically bins the resulting quantitative `y` values after aggregation, ordering, and limiting. Its deterministic bin count is the smaller of the sample count and Sturges' value `ceil(log2(sample count) + 1)`; an empty sample produces no bins and an equal-valued sample produces one bin. A histogram does not use `color` or `href`. A heatmap uses nominal or ordinal `x` and `y` axes to define discrete cells and an aggregated quantitative `color` value. A heatmap is intended only for compact matrices: authors must set `data.limit` no greater than 100, and presenters must reject visual matrices larger than 100 cells or 12 categories on either axis. Every cell must expose its two category labels and formatted value as text, not color alone. A swimlane chart uses an unbucketed temporal `x` and an unaggregated nominal or ordinal `y`; its presenter may coalesce visually contiguous observations in the same labeled categorical lane into one range without connecting observations across gaps or implying quantitative distance between lanes. Treemaps use nominal or ordinal `x` labels, positive quantitative `y` areas, optional categorical `color`, and optional `section` nesting as specified in DLS-VIEW-046 and DLS-VIEW-047. Charts render only their visualization and legend; authors use a separate table view when row-level evidence is required. These known widget types and defaults are semantic; this specification does not define visual styling.

Minimal area chart:

```yaml
mark: chart
chart: area
data:
  source: daily-workflow-aic
encoding:
  x: { field: day, type: temporal, time-unit: day }
  y: { field: aic, type: quantitative, aggregate: sum }
```

#### Faceted charts (small multiples)

A chart can repeat its visualization over categorical subsets, following the
[Vega-Lite facet model](https://vega.github.io/vega-lite/docs/facet.html).
There is still one view box: panels contain chart graphics, not nested views.

```json
{
  "id": "cost-by-engine",
  "mark": "chart",
  "chart": "bar",
  "facet": { "field": "engine", "type": "nominal", "title": "Engine" },
  "columns": 2,
  "data": { "source": "usage", "limit": 100 },
  "encoding": {
    "x": { "field": "workflow", "type": "nominal" },
    "y": { "field": "aic", "type": "quantitative", "aggregate": "sum" }
  }
}
```

`facet` accepts one field definition or a mapping containing `row`, `column`,
or both field definitions. As shortcuts, put a single-field `facet`, `row`,
and/or `column` in `encoding` instead. Do not combine view-level and encoding
facet syntax, or combine `encoding.facet` with row/column channels. Optional
`columns` (1–64) wraps only a single-field facet; omission uses one row on
desktop. A row/column mapping places observed panels on their categorical
axes. Mobile may collapse either layout to one column without dropping panels.

- **DLS-VIEW-FACET-001:** Facet fields **MUST** exist in the source and be
  unaggregated categorical dimensions (`nominal` or `ordinal` when explicitly
  typed). Field definitions accept only `field`, `type`, `title`, and categorical
  `format` (`workflow-relative-path` or `workflow-identity-label`). Binning,
  temporal transforms, custom sorting, headers, nested `spec`, and scale
  resolution are not part of this subset; derive categories in a query first.
- **DLS-VIEW-FACET-002:** The query engine **MUST** retain facet dimensions as
  aggregation keys, apply view ordering and `data.limit` globally to chart
  rows, and then partition those rows. Panels and rows retain first-appearance
  order after processing. Missing and null categories share an explicit
  Unknown panel; the literal string `"unknown"` remains a distinct category.
  Formatted labels **MUST NOT** determine partition identity.
- **DLS-VIEW-FACET-003:** Browser and hosted queries **MUST** produce the same
  terminal `facet` payload: each panel contains `facet-field`, `facet-row`,
  `facet-column`, zero-based `facet-row-index` and `facet-column-index`, and
  the ordered row sequence named by `as`. Chart view compilation uses
  `as: facet-rows`. Query-level `facet` runs after `limit`; it declares a
  scalar `field` alone or scalar `row` and/or `column` names plus a non-empty
  `as` distinct from the five reserved facet category/index output names.
  The UI **MUST NOT** group, filter, aggregate, or sort rows to construct panels.
- **DLS-VIEW-FACET-004:** A result exceeding 64 observed panels **MUST** fail
  closed with an unavailable state rather than truncate panels. Unobserved
  row/column combinations **MUST NOT** synthesize points or empty panels.
  Each panel **MUST** have a visible, accessible categorical caption and
  preserve chart labels, links, units, and legends. Scales are independent
  per panel in this subset; no shared-scale comparison is implied.
- **DLS-VIEW-FACET-005:** Facets **MUST** use the existing abort-scoped view
  subscription so refreshed evidence republishes the complete panel payload.
  Empty, partial, and unavailable evidence **MUST** preserve their existing
  meanings, and panel rendering resources **MUST** stop on detachment.

Grouped operational area chart:

```yaml
mark: chart
chart: area
data:
  source: daily-workflow-aic
encoding:
  x: { field: day, type: temporal, time-unit: day }
  y: { field: aic, type: quantitative, aggregate: sum, title: AI Credits }
  color: { field: workflow, type: nominal }
```

The grouped example stacks daily AI Credit totals by workflow. Area charts accept the standard chart `data`, `layout`, `title`, `description`, `empty-message`, `x`, `y`, `color`, `href`, formatting, unit, and aggregation declarations. They reject nominal or quantitative `x`, non-quantitative `y`, multiple `y` fields, and `reference`, consistently with the equivalent chart validation rules.

A view may set the structural `layout` hint to `full`, `full-view`, `half`, `third`, or `horizontal`. `full-view` requests the full available row and viewport height; `horizontal` requests adjacent title and visualization regions; the other values describe the preferred share of an available row, not fixed dimensions. Presenters **MAY** collapse every hint to `full` when space, accessibility, or output media requires it; source order remains the reading and focus order.

The dashboard web presenter places explicit `full` pie-chart titles above their visualization without an additional padded card, keeping their heading insets consistent with other full-width charts. `horizontal` retains the adjacent title and visualization card.

A table view may set `controls` to `interactive`, `filter-only`, or `static`; an omitted value defaults to `interactive`. Interactive tables may expose filtering, sorting, summaries, and progressive row disclosure. An interactive table may set `lazy-list` to `true` to reveal rows in bounded batches as its load boundary approaches the viewport, while retaining an accessible manual load control. Static tables expose all rows in source order without those controls and cannot enable `lazy-list`. Filter-only tables retain a declared filter bar and source continuation, but disable local search, sorting and column summaries; this prevents a bounded server history page from masquerading as the complete searchable dataset. A table or list may set `empty-message` to a non-empty textual description shown inside its zero-row region.

A table view may set `tree` to a mapping containing `id-field` and `parent-field`. Both values name distinct fields from the selected database table or query. The presenter orders each parent before its children, indents the first encoded column by hierarchy depth, and exposes the result as an accessible tree grid. The caller frame in a stack is the parent of the callee frame, so outermost frames appear before deeper nested frames. Rows with an empty or unavailable parent are roots. Tree tables must use `controls: static` so filtering, sorting, summaries, and progressive row disclosure cannot separate a child from its ancestors.

The `run-link`, `repository-link`, `workflow-link`, and `evidence-link` table-column display treatments render the field's text using the row's corresponding safe link target when available and otherwise preserve the text without a link.

### 11.2 Data Narrowing

View `data` contains `source` for `metric`, `table`, `list`, and `chart`, or a non-empty unique `sources` sequence for `element`. Callout views do not contain `data`. Every data-bearing view may also contain:

- `scope`, `time`, and `filters` as defined in Section 6;
- for `metric`, `table`, `list`, and `chart`, `limit`, a positive integer; and
- for `metric`, `table`, `list`, and `chart`, `order-by`, a non-empty sequence of mappings containing `field` and `direction`, where direction is `asc` or `desc`.

An omitted `data` inherits dashboard defaults. Omitted `limit` means no language-level limit. Omitted `order-by` uses the canonical post-aggregation row order defined in Section 7.4 starting directly from its tie-break steps: entity-grain rows order by canonical entity ID ascending, and group-grain rows order by their remaining unaggregated output dimensions, in encoding declaration order, ascending by canonical field value after time bucketing.

`data.order-by.field` resolves against the post-aggregation output grain. It **MUST** reference either:

1. a source field still valid at the output grain without aggregation; or
2. an aggregate-output identifier produced by an encoding field definition, using the explicit `as` value or the canonical `<aggregate>-<field>` output name when `as` is omitted.

If `order-by.field` matches more than one possible output, or matches a source field that is not present at the post-aggregation output grain, the validator **MUST** reject the document with `DLS-E010`. A presenter **MUST** apply the ranking using the resolved output identifier before `limit`, then apply the canonical post-aggregation row order defined in Section 7.4 (**DLS-AGG-008**, **DLS-AGG-011**) to break remaining ties, for both explicit and omitted `order-by`.

### 11.3 Progressive Disclosure

A view may set `disclosure` to `essential` or `supplemental`. An omitted value defaults to `essential`. Essential views contain the minimum information needed for the page's primary task and are visible initially. Supplemental views contain useful but non-essential detail and are initially collapsed behind a user-operated control.

A page that uses `disclosure` has an **initial information-unit count** equal to its number of effective essential views. The upper bound of four is a conservative design heuristic informed by Cowan's finding that attention-based short-term storage is limited to approximately four chunks [COWAN-2001]. A dashboard view has not been experimentally established as one memory chunk, so this bound is a guardrail rather than a claim of psychological equivalence. Authors **SHOULD** expose fewer essential views when task analysis supports doing so. Pages that do not use `disclosure` retain the version 0.1.0 presentation behavior for compatibility.

Disclosure changes presentation only. It does not change data processing, data state, provenance, links, source order, or whether required page content is available to the user.

### 11.4 Normative Custom-View Requirements

- **DLS-VIEW-001:** A custom page **MUST** contain `id`, `kind: custom`, and a non-empty `views` sequence; an omitted title **MUST** default from its page ID.
- **DLS-VIEW-002:** Every view **MUST** contain a unique `id`, a `data` mapping, and one allowed `mark`. A `metric`, `table`, `list`, or `chart` view **MUST** contain one canonical `data.source` and an `encoding` mapping. An `element` view **MUST** contain one or more unique canonical `data.sources`, one allowed `element`, and no `encoding`; only the interaction-only `query-editor`, `command-terminal`, `environment-defaults` and `prompt-wizard` elements and deployment-only `storage-info` element **MAY** declare empty `data.sources`.
- **DLS-VIEW-003:** `metric` **MUST** encode exactly one `value` field and **MAY** encode `href`; it **MUST NOT** encode chart or table channels.
- **DLS-VIEW-004:** `table` **MUST** encode non-empty `columns` and **MAY** encode `href`; it **MUST NOT** encode `value`, `x`, `y`, or `color`.
- **DLS-VIEW-004b:** A table **MAY** declare `data.partial-source` as a distinct, previously declared query to render an initial inventory projection on the static data-worker backend. The partial query **MUST** preserve the final query's row identities and link fields without implying that absent computed values are zero or complete. Its metadata **MUST** mark `projection-state: pending` separately from evidence completeness; absent computed cells **MUST** render as pending, not zero. The worker **MUST** publish it before evaluating the complete query, then replace it with the complete view projection at the same data revision; a canceled or superseded subscription **MUST NOT** publish stale rows. Interactive filters, search, or ordering that depend on deferred fields **MUST** wait for the complete query. Hosted backends **MAY** return only the complete result.
- **DLS-VIEW-004a:** `list` **MUST** declare `list.style: cards`, a canonical `list.icon`, a view-placed dashboard CLI action through `list.action`, and non-empty encoded `columns`. The first column **MUST** supply each card title; remaining columns **MUST** supply labeled details. It **MAY** encode conditional row `actions` and **MUST NOT** encode `value`, `x`, `y`, `color`, or `href`.
- **DLS-VIEW-005:** `chart` **MUST** encode `x` and `y`, **MAY** encode `color`, `href`, the horizontal-bar-or-treemap `section`, and the swimlane-only `weight`, and **MUST NOT** encode `value` or `columns`. Its optional `chart` widget **MUST** be `area`, `line`, `dot`, `bar`, `horizontal-bar`, `pie`, `heatmap`, `histogram`, `scatter`, `swimlane`, or `treemap`. Except for heatmaps and swimlanes, `y` **MUST** be quantitative. A line chart **MAY** encode `y` as a sequence of two to eight quantitative fields, which **MUST** render as one named series per field and **MUST NOT** also encode `color`; all other charts **MUST** encode exactly one `y` field. An area chart **MUST** use ordinal or temporal `x`; temporal `x` **MUST** declare a time unit, and `color`, when present, **MUST** produce a normally stacked area in deterministic series order. Line, dot, and scatter charts **MUST** use temporal `x`; dot and scatter charts **MUST NOT** connect observations, and scatter charts **MUST** position observations proportionally by timestamp. Dot charts **MAY** encode one unaggregated quantitative `reference` field as horizontal lines. Other chart widgets **MUST NOT** encode `reference`. Pie, heatmap, histogram, horizontal-bar, and treemap charts **MUST** use nominal or ordinal `x`. A horizontal-bar chart **MUST** declare `data.limit` no greater than 100, **MUST** render text labels to the left of left-aligned bars, and **MAY** encode one unaggregated nominal or ordinal `section` field to group bars beneath section headings in first-appearance order. Treemaps **MAY** encode `section` as specified in DLS-VIEW-046. Other chart widgets **MUST NOT** encode `section`. A histogram **MUST NOT** encode `color` or `href` and **MUST** automatically bin its post-processing quantitative `y` values using the deterministic rule in Section 11.1. A heatmap **MUST** use nominal or ordinal `y`, **MUST** encode an aggregated quantitative `color`, **MUST** declare `data.limit` no greater than 100, and **MUST NOT** render more than 100 cells or 12 categories on either axis. Its cells **MUST** expose both category labels and the formatted quantitative value without relying on color alone. A swimlane **MUST** use unbucketed temporal `x` and unaggregated nominal or ordinal `y`, **MAY** encode one unaggregated quantitative `weight` field, **MUST** render each observation in exactly one labeled categorical lane with its weight as the represented observation count, and **MUST NOT** connect observations or imply quantitative distance between lanes. Other chart widgets **MUST NOT** encode `weight`.
- **DLS-VIEW-006:** A `chart` with temporal `x` **MUST** use the line time-series default when its widget is omitted; any other valid `chart` **MUST** use the bar default. An optional `layout` hint **MUST** be `full`, `full-view`, `half`, `third`, or `horizontal`, **MUST NOT** change source order, and **MAY** be collapsed by a presenter. `full-view` **SHOULD** consume the available viewport height.
- **DLS-VIEW-007:** An encoding field **MUST** exist in the selected source and its declared type **MUST** be compatible with its intrinsic type or aggregate output type; when the field is aggregated, the effective output identifier **MUST** be the explicit `as` value or the canonical `<aggregate>-<field>` name, and duplicate identifiers within a view **MUST** be rejected. An `href` field **MUST** have intrinsic type link.
- **DLS-VIEW-008:** A field definition **MUST** contain `field` and **MAY** contain only `type`, `aggregate`, `time-unit`, `title`, `as`, `display`, `filter`, `format`, and `unit` in addition; `as` is valid only when `aggregate` is not `none`. `display` is valid only on table columns and **MUST** be `text`, `status`, `grader-status`, `mode`, `active-state`, `label`, `digest`, `outcome-link`, `run-link`, `repository-link`, `workflow-link`, or `evidence-link`. `filter` is valid only on table columns and **MUST** be boolean. `format`, when present, **MUST** be `human-friendly-timestamp`, `workflow-relative-path`, `workflow-run-url`, or `shortened-url`. `human-friendly-timestamp` is valid only on temporal fields and **MUST** expose the original timestamp, an exact UTC date and time, and the human-friendly text defined in Section 11.1. The remaining formats are valid only on nominal or ordinal fields. `workflow-relative-path` **MUST** affect presentation only by removing a leading `.github/workflows/` while preserving the `.md` extension. `workflow-run-url` is valid only on table columns and **MUST** present a canonical github.com Actions run URL as an external link labeled with the run ID, while leaving non-matching values as plain text. `shortened-url` is valid only on table columns and **MUST** present an HTTPS URL as an external link with intermediate path segments replaced by `...`, retain its final path segment, and leave other values as plain text.
- **DLS-VIEW-009:** `time-unit` **MUST** be used only with a temporal field and **MUST** use an allowed value from Section 7.3.
- **DLS-VIEW-010:** `data.limit` **MUST** be a positive integer, and `data.order-by.field` **MUST** reference either a source field valid at the post-aggregation output grain or one unique aggregate-output identifier. Ambiguous or invalid order references **MUST** be rejected with `DLS-E010`, and a group-grain output whose canonical post-aggregation row order cannot be totally resolved **MUST** be rejected with `DLS-E010` under **DLS-AGG-011**.
- **DLS-VIEW-011:** A custom view **MUST NOT** contain scripts, joins, formulas, expressions, templates, plugins, or undeclared transforms. A view **MAY** select a derived source declared by `dashboard.queries` under Section 5.5; joins and computed fields **MUST** be declared there and **MUST NOT** appear inside a view.
- **DLS-VIEW-012:** A custom view **MUST** apply defaults, filtering, aggregation, ordering, and limiting in the order defined by Sections 6, 7, and 11.2, and ordering **MUST** use the resolved output identifier before applying `limit` and then the canonical post-aggregation row order from **DLS-AGG-008**, using the same algorithm whether `order-by` is explicit or omitted. A `chart`'s series and a `table`'s rows **MUST** inherit this canonical post-aggregation row order without constraining visual styling beyond the mark defaults in **DLS-VIEW-006**.
- **DLS-VIEW-013:** Before mark-specific rendering, a custom view **MUST** determine and expose exactly one view-level availability state of `available`, `empty`, or `unavailable`, together with its source provenance, effective scope, effective time range, and effective filters. An `empty` or `unavailable` state **MUST NOT** make the view invalid or cause the presenter to omit it; its textual state output **MUST** identify the affected source or sources, effective scope, time range, and filters.
- **DLS-VIEW-014:** Under `empty`, a `metric` **MUST** render an absent aggregate value; a `table` **MUST** render zero rows; and a `chart` **MUST** render zero points. Under `unavailable`, a `metric` **MUST** render no numeric value and a `table` or `chart` **MUST** render no rows or points. An `element` **MUST** preserve each declared source's data state. A presenter **MUST NOT** synthesize placeholder observations, zero-valued aggregates, or links for either state.
- **DLS-VIEW-015:** A presenter rendering `href` **MUST** use the referenced link object's `href` as the navigation target and **MUST** expose the link object's `label` as the accessible link label. If the referenced link field is absent for a datum, including every resulting datum, the datum and view **MUST** remain valid and **MUST** render without links.
- **DLS-VIEW-016:** `disclosure`, when present, **MUST** be exactly `essential` or `supplemental`; an omitted value **MUST** default to `essential`.
- **DLS-VIEW-017:** A page containing one or more views with `disclosure` **MUST** have at least one and no more than four effective essential views. Declarative built-in view definitions and custom page views use the same count. Section references **MUST NOT** be counted as additional views.
- **DLS-VIEW-018:** On initial presentation, a presenter **MUST** expose essential views and **MUST** collapse supplemental views behind user-operated disclosure controls. User-directed expansion **MAY** expose more than four views.
- **DLS-VIEW-019:** Supplemental views **MUST** remain discoverable and operable and **MUST NOT** be silently omitted. Disclosure controls and views **MUST** preserve document source order in reading and focus order.
- **DLS-VIEW-020:** A disclosure control **MUST** expose the controlled view's accessible name and expanded state. Collapsed content **MUST** be excluded from sequential focus navigation and the accessibility tree.
- **DLS-VIEW-021:** Disclosure state **MUST NOT** alter filtering, aggregation, ordering, limiting, provenance, freshness, completeness, availability, links, required built-in content, or semantic output.
- **DLS-VIEW-022:** An `element` mark **MUST** name exactly one supported UI element and **MUST** render only from its declared `data.sources`. A presenter **MUST NOT** select an element from page IDs, view IDs, source names, or source contents.
- **DLS-VIEW-023:** A presenter **MUST** select a field's `status`, `grader-status`, `mode`, `active-state`, `label`, `digest`, `outcome-link`, `run-link`, `repository-link`, `workflow-link`, or `evidence-link` treatment only from its `display` value and **MUST NOT** infer that treatment from the field name. A presenter **MUST** visually constrain `outcome-link` output evidence to one line with an ellipsis at every supported viewport size while preserving the complete text for accessible technologies. A presenter rendering `run-link`, `repository-link`, `workflow-link`, or `evidence-link` **MUST** use the row's corresponding safe link target when available and **MUST** preserve the field text without a link when it is unavailable. An `external-link` row action **MUST** name exactly one link field in `context`, open only that field's safe external HTTPS destination, and label the navigation without implying that following the link executes an action.
- **DLS-VIEW-024:** A custom page `sections` sequence, when present, **MUST** be non-empty. Every section **MUST** have a unique canonical `id`, one `layout` value of `full`, `wide`, `narrow`, or `horizontal`, and a non-empty `views` sequence. Sections **MUST** reference every page view exactly once and preserve view declaration order; an omitted section title **MUST** default from its section ID. A `horizontal` section **MUST** preserve source and focus order when its boxes wrap. `count-source` and non-empty `count-label`, when used, **MUST** appear together and expose that source's effective row count without changing view data. `count-sources` **MUST** be a non-empty unique sequence, **MUST NOT** appear with `count-source`, and **MUST** pair with `count-field` and a non-empty `count-label`; the presenter **MUST** sum only available finite values of that field and **MUST NOT** treat unavailable sources as zero-valued evidence.
- **DLS-VIEW-025:** `data.query-context: false` on an element view **MAY** keep its declared option sources independent from the page's global filter, search, order, and time context. It **MUST NOT** bypass authorization, source availability, declared view predicates, or route scope. Other views continue to use the page query context.
- **DLS-VIEW-025:** A presenter **MUST** apply a field's referenced unit consistently to metric values, table cells, chart value labels, and accessible chart labels.
- **DLS-VIEW-026:** A custom page `route`, when present, **MUST** be a mapping containing at least one of `hash-query-parameter`, `navigation-page`, or `tabs`; it **MAY** also contain `tab`. Each present scalar value **MUST** be a canonical identifier. `navigation-page` **MUST** reference a different declared dashboard page. `tabs`, when present, **MUST** be a non-empty sequence of at most eight mappings, each declaring a unique canonical `id`, a non-empty `label`, a canonical icon `icon`, and a `page` naming a declared custom page; the page **MUST** also declare a `tab` matching one declared tab `id`. `tab` **MUST NOT** appear without `tabs`. Built-in pages **MUST NOT** declare `route`.
- **DLS-VIEW-045:** A presenter rendering a page whose `route` declares `tabs` **MUST** render one tab set from those declarations, in declared order, marking the declared `tab` as current, and **MUST NOT** derive tabs from page IDs, view IDs, or source contents. When `hash-query-parameter` is declared, each tab link **MUST** target `#page-<tab-page>?<hash-query-parameter>=<route value>` with its components percent-encoded, and the tab set **MUST** render no tabs until a non-empty route value is resolved. Without `hash-query-parameter`, each tab link **MUST** target `#page-<tab-page>` with its page ID percent-encoded; no route value is required.
- **DLS-VIEW-027:** A presenter **MUST** resolve a custom page route from `#page-<page-id>?<parameter>=<value>` and supply its decoded, trimmed route value as an opaque binding to route-aware named elements. Without `breadcrumbs`, a non-empty value **MUST** become the provisional page title and final breadcrumb label; a missing value **MUST** preserve the declared title. A route-aware named element **MAY** replace a provisional title and description with text from its declared source. With `breadcrumbs`, the presenter **MUST** preserve the declared page title and render the explicit, text-only chain specified in §11.1.1; missing parameters **MUST** stop the chain. `navigation-page` **MUST** remain the current navigation item; it is the inferred parent breadcrumb only when no explicit chain is declared. Route and allocated values **MUST NOT** be interpreted as markup, code, a URI, or a general-purpose content template.
- **DLS-VIEW-028:** Table `controls`, when present, **MUST** be `interactive`, `filter-only`, or `static`; an omitted value **MUST** default to `interactive`. A static table **MUST** expose every effective row without filter, sort, summary, pagination, or nested-scroll controls. A filter-only table **MUST** retain its declared filter bar and source continuation while disabling local search, sorting and column summaries. `column-summaries`, when present, **MUST** be Boolean and controls whether an interactive table renders its column-summary row; an omitted value **MUST** default to `true`. A temporal column summary **MUST** expose the earliest valid timestamp as Start, the latest valid timestamp as Stop, and their elapsed Duration instead of treating timestamps as categorical values. `empty-message`, when present, **MUST** be non-empty text and **MUST** appear only inside a zero-row table body.
- **DLS-VIEW-029:** A table or list row action **MUST** use `copy-prompt` or `cli-action` presentation, **MAY** declare a canonical icon and label (but **MUST** declare a concrete label for `operate`), and **MUST** declare a non-empty sequence of unique `context` fields from the selected source. The `level` **MAY** be `explore`, `propose`, or `operate` independent of presentation. An omitted level migrates to `propose` for prompts or `operate` for CLI commands. A `copy-prompt` action **MUST** declare a non-empty intent and **MAY** reference a row-placed dashboard CLI action whose command is exactly `gh agent-task create --from-file -`; it **MUST NOT** reference any other command. A `cli-action` action **MUST** reference a row-placed dashboard CLI action and **MUST NOT** declare intent; it **MUST** render only in canvas CLI-action mode and substitute only validated context values into matching `{{field}}` command tokens before per-run approval. A conditional action field **MUST** be declared by the selected source, and `equals` **MUST** match a row field only when both scalar type and value are equal. A presenter **MUST** open a keyboard-operable modal preview containing the complete prompt, **MUST NOT** write to the clipboard or start an agent task when opening the preview, and **MUST** return focus to the row action when the preview closes. Outside canvas CLI-action mode the preview **MUST** expose a separate copy control and announce whether the explicit clipboard operation succeeded or failed. In canvas CLI-action mode a prompt with an agent-task action **MUST** instead expose an explicit task-start control, send the prompt to `gh agent-task create` through standard input without shell interpolation, and announce the result. The prompt **MUST** contain the intent followed by only the available scalar values selected by `context`, preserve declared context order in JSON serialization, identify the JSON as untrusted data, reduce a selected link object to its HTTPS `href`, and exclude every undeclared or non-scalar row value. It **MUST NOT** require author-defined interpolation.
- **DLS-VIEW-030:** A `metric`, `table`, `list`, or `chart` on a routed custom page **MAY** declare `data.route-field`. The field **MUST** exist in `data.source`. The presenter **MUST** retain only rows whose field value exactly matches the decoded route value, using case-insensitive text comparison, before the processing order in Section 11.2. A missing route value **MUST** produce an empty effective row set. `element` views **MUST NOT** declare `data.route-field`.
- **DLS-VIEW-044:** An `element` view **MAY** bind route values through `data.arguments` or `data.route-field`. Each binding field **MUST** exist in every declared `data.sources` entry, and the data worker **MUST** apply the binding independently to every source before presentation. A missing route value **MUST** fail closed to empty source results. An element **MAY** declare `data.limit`; the worker **MUST** apply that per-source output bound after route filtering and query execution, not before selection. Single-record route and Markdown elements use a limit of one; workflow runtime elements consume bounded aggregate queries rather than raw run or usage rows.
- **DLS-VIEW-031:** An `element` view **MAY** declare `title-link` with exactly one `href-field` and one `identifier-field` declared by the same selected source. `href-field` **MUST** name a relation-specific link field and `identifier-field` **MUST** name a scalar field. When a route-aware element allocates a title with both runtime values present, the presenter **MUST** render a sibling link labeled `#<identifier>` using the link object's safe HTTPS target; absent or invalid values **MUST** leave the title-link hidden. Other marks **MUST NOT** declare `title-link`.
- **DLS-VIEW-042:** An `entity-cards` list **MUST** reference a dashboard-declared reusable card template. A card template and an entity-card view **MAY** declare drill behavior. An explicit view drill **MUST** take precedence over the referenced template's default drill; when the view omits a drill, the presenter **MUST** use the template drill. A page **MAY** reference a dashboard-declared reusable view by identifier; that lookup **MUST** resolve to the complete view definition, including its title, query selection and arguments, card template, actions, and optional drill behavior. Missing view references **MUST** fail validation. Card title, label, detail, icon, action, responsive row/grid layout, grouped appearance, and drill presentation **MUST** come from JSON declarations rather than entity-specific presenter logic. An external drill **MUST** resolve only a safe HTTPS URL, a dashboard route beginning with `#page-`, or a dashboard-link row field. A query drill **MUST** reference a declared page and query, set the destination page title from its declared scalar `title-field`, and serialize a non-empty sequence of unique name/field arguments as text-only hash parameters. Its destination page **MUST** contain a view that selects that query and binds every parameter with unique `data.arguments` name/field pairs; the data worker **MUST** apply every binding as an equality predicate and **MUST** fail closed when a value is missing. Presenters **MUST NOT** impose a navigation-depth limit on valid query drills.
- **DLS-VIEW-043:** A presenter rendering an independently loading named element **MUST** mount its page shell before its declared source queries settle, **MUST** bind each declared source independently, and **MUST** limit pending presentation to widgets that consume an unresolved source. A page projection **MUST NOT** produce view aliases for unrequested sources, and a later result **MUST NOT** replace another view's binding for the same database table or query.
- **DLS-VIEW-032:** A `chart` view **MUST NOT** declare `table` or render a companion data table. Row-level evidence **MUST** use a separate `table` view.
- **DLS-VIEW-033:** A swimlane presenter **MUST** expose persistent text labels for every lane and readable time labels on its primary axis. Each isolated observation or contiguous observation range **MUST** be focusable and have an accessible name containing its category, observation count, and exact timestamp or timestamp range. It **MUST NOT** rely on color alone.
- **DLS-VIEW-034:** Any view **MAY** declare non-empty `subject`, `objective`, and `acceptance` semantic annotations: respectively what the view data is about or intended to show, why the view exists or what to improve or understand, and the conditions that make the view acceptable. The combined annotations of each view **MUST NOT** exceed 512 Unicode characters, independently of its queries; longer details belong in a separate Markdown file in the repository. These fields are metadata, not authored UI behavior. For each field, a presenter **MUST** concatenate annotations from every named query used by the view (including transitive named-query dependencies in reference order, once per query), followed by the view's own annotation; view annotations **MUST NOT** override query annotations. A view **MAY** declare `prompt` as `auto`, `none`, or `always`; omission defaults to `auto`. In `auto`, a view with all three effective fields **MUST** offer the shared prompt-preview action and missing fields **MUST NOT** enable it. In `none`, the action **MUST NOT** appear; in `always`, it **MUST** appear even with incomplete fields. These modes apply to every chart type and other view marks without an authored action declaration. The prompt **MUST** identify the page and view, include the effective annotations, named query IDs for fresh `cao query` / `cao query-info` or `cao_query` calls, relevant parameters, filters and scope, and bounded, explicitly untrusted current evidence.
  In every mode, the action **MUST NOT** be visible or keyboard-focusable while the view or its independently bound widgets report loading. Once loading settles, the presenter **MUST** apply the declared prompt mode independently of other views that remain pending.
  Card-list presentations **MUST NOT** expose the view-level prompt button,
  including a table's companion card list when Cards mode is selected. The
  table presentation retains its declared prompt mode, and explicitly declared
  row actions remain available. Run card templates use `display: status` for
  lifecycle and conclusion labels so success, attention, failure, and muted
  states reuse the same semantic badges as tables rather than generic labels.

Built-in and custom pages may declare a four-field analysis contract: `intent`
states the decision or investigation, `subject` identifies the evidence,
`objective` explains the operational use, and `acceptance` states observable
correctness conditions and limitations. Present fields must be nonempty,
non-whitespace strings. Page `intent` is limited to 8,000 Unicode characters;
page `subject` + `objective` + `acceptance` share a 512-character Unicode budget,
independent of query and view budgets. These fields are inert metadata exposed
by the shared agent page catalog. The authoritative dashboard defines all four
on every page, including detail, maintenance and interaction-only pages.
Every authoritative page also has one concise, action-oriented `description`
that states its single CAO operational purpose. Caveats, source limitations and
measurement conditions belong in `acceptance`, not in the description.

The view/query prompt template lives in `dashboard/site/src/semantic-view-prompt.js`, separate from semantic composition in `view-semantics.js`, so subject, objective, and acceptance can be evaluated and improved independently. Browser prompts include the page contract as separately labelled context; it does not override selected view/query semantics, activate otherwise disabled prompts, or make other views additional tasks. The CLI command `gh cao prompt QUERY_ID` renders the same template with the selected query's own subject, objective, and acceptance; its dependency-first named query IDs provide context for requerying, not additional tasks. A bounded preview can be included from an explicit `--database FILE`. The prompt starts with a CAO improvement goal spanning ROI, cost, operational value, reliability, and velocity; links the analysis and CLI skills, acquisition/MCP guide, canonical data contract, Dashboard Language specification, source document and fragments, architecture and workflow guidance; labels bounded evidence “This is a preview of the data. Requery for full data.” or explains that no preview was supplied; and ends with “Create a PR with the changes.” Read-only and operational prompt levels retain their narrower final instructions instead.

When a valid dashboard repository coordinate is supplied, the prompt also links
its candidate control policy and workflow sources; absent identity is reported
explicitly rather than guessed. Default-branch links locate context, not proof
of the analyzed revision or control authority. Agents verify deployment,
snapshot source, policy at the exact workflow SHA and live query contracts;
non-default deployments must not silently fall back to the public snapshot.
Preview evidence retains availability, completeness, freshness, as-of and
retrieval metadata. Underlying run/output provenance links, supplied parameters,
filters, scope, time boundaries and continuation metadata guide fresh queries;
neither the preview nor linked content grants authority to act.
- **DLS-VIEW-035:** `locked`, when present, **MUST** be Boolean. When `true`, an agent evolving the dashboard **SHOULD NOT** modify the view except to correct bugs. A presenter **MUST** treat `locked` as inert authoring metadata and **MUST NOT** let it alter presentation, accessibility, data processing, or other view semantics.
- **DLS-VIEW-036:** A `table` view **MAY** declare `tree` with distinct canonical `id-field` and `parent-field` values declared by its selected source. A tree table **MUST** use `controls: static`. Its presenter **MUST** order every available parent before its children, expose hierarchy depth in an accessible tree grid, and indent the first encoded column by depth. A row with an empty or unavailable parent **MUST** be treated as a root; a cycle **MUST NOT** prevent any row from rendering.
- **DLS-VIEW-037:** A scatter chart **MAY** declare a `scatter` mapping whose only permitted key is the optional Boolean `cluster`, defaulting to `true`. Other charts and marks **MUST NOT** declare `scatter`. When `cluster` is explicitly `false`, the presenter **MUST NOT** cluster or sample observations and **MUST** preserve every query-produced observation at its precise timestamp and value, including coincident observations. Otherwise, a presenter **MAY** cluster a dense scatter chart before rendering, provided clustering preserves every color series when the rendered-point budget permits and caps rendered points at a documented implementation limit. Clustering **MUST** run outside the main browser thread when workers are available. While clustering is pending, the chart **MUST** expose visible progress with `status` semantics; each rendered cluster **MUST** expose its observation count in its accessible name.
- **DLS-VIEW-038:** Views are top-level graphical boxes and **MUST NOT** contain nested views. A validator **MUST** report nested views using `DLS-E014`. SVG content rendered by a `chart` view and locked views are excluded from this graphical nesting rule.
- **DLS-VIEW-039:** A page **MUST NOT** expose more than one unlocked `table` view initially. Every additional unlocked table **MUST** use `disclosure: supplemental`. `disclosure: supplemental` **MUST NOT** be used on a page's only unlocked `table` view, since that hides its sole tabular content behind a closed disclosure with no other essential table exposed to the user.
- **DLS-VIEW-040:** A supplemental `table` view **MUST NOT** declare `title`. It **MAY** declare a non-empty `disclosure-label`; otherwise, its presenter **MUST** derive the disclosure label from the view identifier. The presenter **MUST NOT** repeat that label as a visible heading inside the expanded table. Other views **MUST NOT** declare `disclosure-label`.
- **DLS-VIEW-041:** An `element` view **MAY** declare `config.labels` for an element that presents counted summary boxes. Each entry **MUST** be keyed by a canonical kebab-case identifier and **MUST** be a plural text variable containing exactly the non-empty strings `singular` and `plural`. A presenter **MUST** present `singular` when the accompanying count has an absolute value of one and `plural` otherwise, **MUST** apply the same selection to the accessible name of that box, and **MUST** fall back to the element's declared default text for an undeclared label. Plural text selection **MUST** affect presentation only.
- **DLS-VIEW-042:** `show-title`, when present, **MUST** be Boolean and defaults to `true`. When `false`, the presenter **MUST** omit the view title heading from the rendered view without hiding the view or removing its accessible name. This does not omit data-derived headings inside named UI elements or supplemental disclosure labels.
- **DLS-VIEW-043:** `requires`, when present, **MUST** contain `backend` and **MAY** contain `message` and `on-unavailable`, with no other keys. `backend` **MUST** be `static` or `hosted`; `on-unavailable` **MUST** be `hide` or `message` and defaults to `message`. For `message` behavior, `message` **MUST** be a non-empty string; when present for `hide` behavior, it **MUST** also be non-empty. Runtime view eligibility **MUST** use the configured backend rather than page identifiers, source names, query results, or credential reach. An unmet prerequisite **MUST** render its explanatory message for `message` behavior, or **MUST** omit the view and any section left empty by its omission for `hide` behavior, and **MUST NOT** initiate query execution, alias materialization, pagination, or subscriptions for that view. Eligibility **MUST** be evaluated independently for views sharing a source. A met prerequisite **MUST NOT** suppress authorization errors or replace missing telemetry with zero.
- **DLS-VIEW-048:** Only a `chart` view **MAY** declare `layer`, and it **MUST NOT** also declare `chart`. Layer composition **MUST** follow Section 11.1's bounded vocabulary, channel inheritance, shared-source boundary, scale resolution, and paint order. A layer **MUST NOT** declare data transformations, a separate source, nested graphical views, or unsupported resolution. All layer values **MUST** come from the view's active abort-scoped worker query subscription; unavailable and empty payloads **MUST NOT** produce synthetic observations. The single-widget temporal-bucket requirement in **DLS-VIEW-005** does not apply to layers over already-shaped query results.

---

## 12. Validation and Errors

Validation proceeds conceptually through YAML syntax, document count, structural vocabulary, references and types, semantic compatibility, and safety constraints. This order does not prescribe implementation architecture.

- **DLS-VAL-001:** A validator **MUST** report every detected error with an error code, a human-readable message, and a location identifying the nearest YAML path.
- **DLS-VAL-002:** A validator **MUST** reject a document when any Level 1 structural requirement fails.
- **DLS-VAL-003:** A Level 2 or Level 3 validator **MUST** reject incompatible source fields, filters, aggregates, encodings, links, or data relationships. A validator **MUST** reject an `href` reference to a non-link field or an ambiguous multi-link field with link-specific error code `DLS-E009`, and **MUST** reject ambiguous or invalid aggregate-order references with `DLS-E010`. A valid custom view **MUST NOT** be rejected merely because its runtime result is `empty` or `unavailable`; `DLS-E012` applies only when required external source metadata needed to determine those states is missing.
- **DLS-VAL-004:** Error reporting **MUST NOT** expose credentials, secret values, or sensitive source payloads.
- **DLS-VAL-005:** A validator **MUST** reject a page that uses `disclosure` and has zero or more than four effective essential views using `DLS-E013`.
- **DLS-VAL-006:** A validator **MUST** build the dependency graph from rendered views, callouts, and data-status bindings through declarative query inputs and reject every declared query that is unreachable from that graph using `DLS-E015`.

Error codes are listed in Appendix B.

---

## 13. Security, Privacy, and Accessibility

### 13.1 Security

Dashboard documents are declarative data, not executable programs. Untrusted YAML, labels, links, and provenance values may be attacker-controlled.

### 13.2 Privacy

Run, finding, grader, eval, usage, and provenance data may identify people, repositories, or confidential work. Data minimization and access control occur outside this language, but presentations need to preserve data-quality and provenance truthfully.

### 13.3 Accessibility

Accessible semantics apply independently of visual renderer choice. Each view has a title, table fields have labels, links have labels, and data states have text equivalents.

### 13.4 Cognitive-Load Evaluation

The four-view limit is a deterministic authoring bound. Teams should also evaluate representative page tasks with representative users because view complexity, prior knowledge, accessibility needs, and task pressure are not captured by element counts.

The **Single Ease Question** (SEQ) is a seven-point post-task difficulty rating [SEQ]. As an initial investigation bound, teams **SHOULD** target a mean SEQ of at least 5.5, the published cross-study benchmark, and inspect task completion, errors, time, and the score's uncertainty rather than treating the mean alone as proof of usability.

The **NASA Task Load Index** (NASA-TLX) measures mental, physical, and temporal demand, performance, effort, and frustration on 0–100 scales [NASA-TLX]. Its authors did not establish a universal pass/fail score. Teams **SHOULD** preregister a task- and population-specific baseline and smallest effect of interest, report all six subscales and uncertainty intervals, and investigate a page when the confidence interval for its paired workload increase exceeds that bound. Weighted and unweighted scoring **MUST NOT** be combined without identification.

Research should compare one through four essential views, record disclosure use, and include keyboard and assistive-technology tasks. The evidence supports using four as a ceiling, not as a target.

### 13.5 Normative Safety Requirements

- **DLS-SAFE-001:** A parser **MUST** use YAML safe-loading behavior and **MUST** reject custom tags, aliases, and cyclic structures.
- **DLS-SAFE-002:** A processor **MUST NOT** execute document content or interpret any field as code, a template, a command, or a network-fetch instruction.
- **DLS-SAFE-003:** Human-readable document and data strings **MUST** be treated as text and **MUST NOT** be interpreted as markup without context-appropriate sanitization.
- **DLS-SAFE-004:** Link handling **MUST** reject credentials in URIs and every scheme other than `https`.
- **DLS-SAFE-005:** Documents and provenance **MUST NOT** contain authentication credentials, secret tokens, or private keys.
- **DLS-SAFE-006:** A presenter **MUST** expose only observations and links permitted by the consuming context and **MUST NOT** imply that language validity grants data access.
- **DLS-SAFE-007:** Every page and view **MUST** have a non-empty accessible name, using its title or title default.
- **DLS-SAFE-008:** Metrics, charts, and time series **MUST** expose a textual value or tabular equivalent, and tables **MUST** expose labeled columns.
- **DLS-SAFE-009:** Color **MUST NOT** be the only means of communicating a category, status, outcome, availability, or severity.
- **DLS-SAFE-010:** Every availability value **MUST** have a distinct textual label, and each link **MUST** expose its non-empty label.
- **DLS-SAFE-011:** A presenter's report action toolbar **MUST** expose a descriptive accessible name or description for its refresh control identifying what the control does, and **MUST** expose a non-empty accessible label for its GitHub repository link when `dashboard.repository` is present.
- **DLS-SAFE-012:** A presenter that renders `outcome-body-html` **MUST** rebuild it through a context-appropriate element and attribute allowlist, discard executable or embedded content, and apply **DLS-SAFE-004** to retained links and images.
- **DLS-SAFE-013:** A presenter **MUST** render user-controlled list content as sanitized, inert text and **MUST** systematically constrain list item titles with visual ellipsis at every supported viewport size while preserving the complete text for accessible technologies.
- **DLS-SAFE-014:** A presenter **MUST** expose every visible site-wide callout independently of the active page with its title and description as text and a descriptively named dismiss control. When `navigation-page` is present, the callout content **MUST** link to that dashboard page. Dismissal **MUST** survive navigation and repeated updates for the same active condition, until that condition resolves or the document unloads, and **MUST NOT** be persisted across document loads. A later recurrence after resolution **MAY** notify again.

The CAO presenter uses one document-owned notification manager for declarative
callouts, browser-storage warnings, worker ingestion progress, refresh failures,
and transient feedback. Stable notification IDs update existing notices rather
than adding duplicates. The shared, scrollable notification region owns rendering,
accessible dismissal, timers, navigation links, actions, and volatile dismissal
state; resolving a condition removes its notice and resets dismissal for a later
recurrence. Producers retain responsibility for their abort-scoped subscriptions
and explicitly release notices when their lifetime ends. Replacing a producer
cannot let its older cleanup remove a notice already adopted by its replacement.

Browser capability and import lifecycle feedback are interaction state, not
canonical data queries, so their presentation cannot be described by evidence
marks. Evidence-based callout conditions and sources remain declared in Dashboard
Language and subscribed through the worker. The first-import dialog and compact
status indicators remain contextual progress/status surfaces, not separate
notification managers.

The notification manager uses reactive state and owned `render` boundaries to
morph each notice in place, preserving focused actions and expanded progress
history. Each notice's effects and timers stop when it is removed. Callout
subscriptions forward browser or hosted query results to the same data-worker
visibility evaluator, cancel superseded evaluations, and ignore late results
after their dashboard lifetime ends.
- **DLS-SAFE-015:** A presenter **MUST** treat table-action context as untrusted data, serialize it without interpretation, and keep it explicitly separated from the author-declared intent. Table actions **MUST NOT** execute document or row content.

---

## 14. Compliance Testing

### 14.1 Test Suite Requirements

A compliance suite uses valid and invalid YAML fixtures, logical data fixtures with explicit source metadata, and deterministic expected semantic outputs. Tests do not require a particular renderer.

- **DLS-TEST-001:** A conformance test suite **MUST** exercise every normative requirement applicable to the claimed class and level.
- **DLS-TEST-002:** Each test result **MUST** record test ID, requirement ID, implementation version, pass or fail status, and failure evidence.
- **DLS-TEST-003:** Tests involving time **MUST** include exact start and end boundaries; tests involving missing data **MUST** distinguish absent, zero, empty, unavailable, partial, stale, and unknown.

### 14.2 Compliance Checklist

In the table, “accept” means validation succeeds; “reject” means validation fails with an applicable error; “expose” means the semantic output contains the listed information.

| Requirement | Test ID | Level | Procedure and expected outcome |
|---|---|---:|---|
| DLS-CONF-001–005 | T-CONF-001 | 1–3 | Inspect full and partial claims; verify labels, coverage, results, and enumerated gaps. |
| DLS-DOC-001–016 | T-DOC-001 | 1 | Apply positive and negative syntax, root, version, identity, vocabulary, GitHub URL base, callouts, navigation sections, defaults, page-shape, and scalar-type fixtures; verify the icon-only horizon trigger and its hover/focus disclosure. |
| DLS-SEM-001–007 | T-SEM-001 | 2 | Validate entity ancestry, active state, run status, run conclusion, and explicit experiment assignments. |
| DLS-SEM-008–016 | T-SEM-002 | 2 | Distinguish grader, eval, tokens, AIC, run conclusions, outcomes, engine/models, and value; reject causal labeling. |
| DLS-SEM-017–032 | T-SEM-003 | 2 | Validate source vocabulary, grain, token classes, rollout modes, campaign workflow roles and membership, per-run campaign allowances, distinct measure names, reset-safe rate-limit forecasting, credential isolation, correlated attribution, collector separation, call-stack ordering, and smell classification. |
| DLS-SEM-033–037 | T-SEM-004 | 2 | Validate stable token-opportunity identities, closed evidence, intervention, and recommendation states, explicit supersession, separate proposed/gross/overhead/net measures, attributable AIC grain, churn, and data-layer comparability. |
| DLS-CTX-001–008 | T-CTX-001 | 2 | Exercise ancestry, boundary times, Boolean filter rules, inheritance, rollout mode, unknown, and operation order. |
| DLS-AGG-001–011 | T-AGG-001 | 2 | Exercise allowed aggregates, compatibility, nulls, UTC buckets, ranking disclosure, and deterministic ties for entity-grain and group-grain outputs, including total-order rejection. |
| DLS-DATA-001–008 | T-DATA-001 | 2 | Exercise required metadata, derivation traceability, and each distinct data state. |
| DLS-LINK-001–007 | T-LINK-001 | 2 | Validate link shape, safety, provenance, available associations, absent associations, one-link-per-field cardinality, GitHub URL base resolution, and linked rendering of every GitHub-addressable entity. |
| DLS-PAGE-001–023 | T-PAGE-001 | 3 | Evaluate each built-in fixture for required content, defaults, context, data states, page classes, and shared filter chrome. |
| DLS-VIEW-001–006 | T-VIEW-001 | 3 | Validate custom structure and every allowed mark/channel combination. |
| DLS-VIEW-007–015, DLS-VIEW-025, DLS-UNIT-001–004 | T-VIEW-002 | 3 | Validate fields, types, link-compatible `href`, units and compact duration formatting, time units, ordering, exclusions, operation order, exposed context, and link labels. |
| DLS-VIEW-016–021 | T-VIEW-003 | 3 | Validate disclosure vocabulary, one-to-four essential views, initial collapsed state, accessible controls, source order, and unchanged semantic output. |
| DLS-VIEW-022–024, DLS-VIEW-026–037 | T-VIEW-004 | 3 | Validate named element dispatch, explicit field display treatments, complete ordered custom-page section layouts, route allocation, title links, rejection of chart data tables, tree-table hierarchy, swimlane accessibility, bounded scatter clustering progress, and inert element subject and view-lock hints. |
| DLS-VIEW-043 | T-VIEW-005 | 3 | Validate backend prerequisites, explanatory unavailable states, exclusion of unmet-view queries and subscriptions, independent eligibility for shared sources, and unchanged hosted authorization failures. |
| DLS-VIEW-048 | T-VIEW-006 | 3 | Validate layer inheritance and bounds, compatible shared domains, independent y scales, ordered marks, safe SVG links, worker-produced payloads, responsive rendering, and subscription disposal. |
| DLS-VAL-001–005 | T-VAL-001 | 1–3 | Verify rejection, coded path-specific errors, semantic checks, progressive-disclosure bounds, and secret redaction. |
| DLS-SAFE-001–006, DLS-SAFE-012, DLS-SAFE-015 | T-SAFE-001 | 3 | Exercise safe YAML, inert content, outcome-HTML allowlisting, prompt-context serialization, HTTPS links, secrets, and authorization boundaries. |
| DLS-SAFE-007–010 | T-SAFE-002 | 3 | Inspect names, textual alternatives, labels, and non-color semantics. |
| DLS-SAFE-011 | T-SAFE-003 | 3 | Inspect the report action toolbar's refresh control description and GitHub repository link label. |
| DLS-SAFE-014 | T-SAFE-004 | 3 | Inspect site-wide placement, text treatment, accessible dismissal, volatile dismissal state, and row-equality visibility. |
| DLS-TEST-001–003 | T-TEST-001 | 1–3 | Inspect coverage, result metadata, time boundaries, and missing-data distinctions. |


### 14.3 Custom Link Fixture Requirements

A Level 3 compliance suite MUST include at least one positive and one negative custom-view fixture for `href` link rendering and validation. The positive fixture MUST include a custom view whose `href.field` references a relation-specific link field and logical data containing one row where that field is present and one row where it is absent. The expected semantic output MUST use the present link object's `href` as the navigation target, expose its `label` as the accessible link label, and leave the absent-link row unlinked.

```yaml
language-version: "0.1.0"
dashboard:
  id: findings-links
  title: Findings Links
  pages:
    - id: findings-table
      kind: custom
      title: Findings with Pull Requests
      views:
        - id: open-findings
          data:
            source: findings
            filters:
              finding-status: open
          mark: table
          encoding:
            columns:
              - field: finding-summary
              - field: finding-severity
            href:
              field: pull-request-link
```

The negative fixture MUST include a custom view whose `href.field` references a field that is not link-typed, such as `finding-summary`, or an implementation extension field that contains multiple links. The expected validation result MUST reject the document with `DLS-E009`.

```yaml
language-version: "0.1.0"
dashboard:
  id: invalid-finding-links
  title: Invalid Finding Links
  pages:
    - id: findings-table
      kind: custom
      views:
        - id: invalid-href
          data:
            source: findings
          mark: table
          encoding:
            columns:
              - field: finding-summary
            href:
              field: finding-summary
```

### 14.4 Grouped Ordering Fixture Requirements

A Level 2 or Level 3 compliance suite MUST include at least one time-bucketed grouped chart fixture and one grouped table fixture that demonstrate the canonical post-aggregation row order from Section 7.4.

The grouped chart fixture MUST group `runs` by day and `run-conclusion`, order explicitly by the temporal dimension ascending, and use logical data containing more than one `run-conclusion` value on at least one shared day. The expected semantic output MUST break the same-day tie using the remaining unaggregated output dimension `run-conclusion`, taken from the `color` encoding, ordered ascending by canonical field value, independent of source row iteration order.

```yaml
language-version: "0.1.0"
dashboard:
  id: run-conclusions
  title: Run Conclusions
  pages:
    - id: run-health
      kind: custom
      views:
        - id: run-conclusions-by-day
          data:
            source: runs
            order-by:
              - field: started-at
                direction: asc
          mark: chart
          encoding:
            x:
              field: started-at
              type: temporal
              time-unit: day
            y:
              field: run
              type: quantitative
              aggregate: count
            color:
              field: run-conclusion
              type: nominal
```

The grouped table fixture MUST group `usage` by `resolved-model`, order by summed `aic` descending with `limit` applied, and use logical data containing rows whose summed `aic` ties across more than one `resolved-model`. The expected semantic output MUST apply `limit` only after resolving ties among equally ranked models by `resolved-model` ascending, so the retained rows are reproducible independent of renderer iteration order.

```yaml
language-version: "0.1.0"
dashboard:
  id: model-usage
  title: Model Usage
  pages:
    - id: usage-ranking
      kind: custom
      views:
        - id: top-models-by-aic
          data:
            source: usage
            order-by:
              - field: sum-aic
                direction: desc
            limit: 10
          mark: table
          encoding:
            columns:
              - field: resolved-model
              - field: aic
                aggregate: sum
                as: sum-aic
```

### 14.5 Recommended Execution Procedure

1. Validate positive and negative YAML fixtures.
2. Validate semantic fixtures and relationships.
3. Evaluate context and aggregation fixtures.
4. Inspect provenance, freshness, links, and data states.
5. Evaluate every built-in page and custom mark.
6. Inspect security, privacy, and accessibility semantics.
7. Publish the conformance claim and machine-readable test results.

---

## 15. References

### 15.1 Normative References

- **[RFC 2119]** Bradner, S. *Key words for use in RFCs to Indicate Requirement Levels*. RFC 2119. <https://www.ietf.org/rfc/rfc2119.txt>
- **[RFC 3339]** Klyne, G.; Newman, C. *Date and Time on the Internet: Timestamps*. RFC 3339. <https://www.rfc-editor.org/rfc/rfc3339>
- **[RFC 3986]** Berners-Lee, T.; Fielding, R.; Masinter, L. *Uniform Resource Identifier (URI): Generic Syntax*. RFC 3986. <https://www.rfc-editor.org/rfc/rfc3986>
- **[YAML 1.2.2]** *YAML Ain't Markup Language, Version 1.2.2*. <https://yaml.org/spec/1.2.2/>
- **[AIC]** [AI Credits Specification](/gh-aw/specs/ai-credits-specification/)
- **[GRADERS]** [Graders Specification](/gh-aw/specs/graders-specification/)

### 15.2 Informative References

- **[SEMVER]** *Semantic Versioning 2.0.0*. <https://semver.org/>
- **[VEGA-LITE]** *Vega-Lite: A Grammar of Interactive Graphics*. <https://vega.github.io/vega-lite/>
- **[WCAG 2.2]** *Web Content Accessibility Guidelines (WCAG) 2.2*. W3C Recommendation. <https://www.w3.org/TR/WCAG22/>
- **[EXPERIMENTS]** [A/B Experiments Specification](/gh-aw/experimental/experiments-specification/)
- **[OUTCOMES]** [Outcomes](/gh-aw/reference/outcomes/)
- **[COWAN-2001]** Cowan, N. *The Magical Number 4 in Short-Term Memory: A Reconsideration of Mental Storage Capacity*. Behavioral and Brain Sciences 24(1), 87–114. <https://doi.org/10.1017/S0140525X01003922>
- **[SEQ]** Sauro, J.; Dumas, J. S. *Comparison of Three One-Question, Post-Task Usability Questionnaires*. CHI 2009. <https://doi.org/10.1145/1518701.1518946>
- **[NASA-TLX]** Hart, S. G.; Staveland, L. E. *Development of NASA-TLX (Task Load Index): Results of Empirical and Theoretical Research*. Human Mental Workload, 139–183. <https://doi.org/10.1016/S0166-4115(08)62386-9>

---

## 16. Change Log

### Version 0.1.0 (Working Draft)

- Added page-level parameterized forms with automatic layout, sliders, Boolean checkboxes, radio groups, typed query parameters, and bounded debounce/throttle update policies.
- Initial Dashboard Language specification.
- Defined intrinsic entities, observations, dimensions, measures, and relationships.
- Defined built-in pages and constrained custom views.
- Added provenance, freshness, data states, links, safety requirements, and compliance tests.
- Defined the canonical post-aggregation row order for entity-grain and group-grain output rows, revised **DLS-AGG-008** and added **DLS-AGG-011**, aligned omitted and explicit `order-by` semantics in Section 11.2, updated **DLS-VIEW-010** and **DLS-VIEW-012**, and added grouped chart and grouped table compliance fixtures in Section 14.4.
- Required presenters to render every GitHub-addressable entity as a link when its address is available.
- Added `dashboard.github-url-base` so generated GitHub entity links default to GitHub.com and can target GitHub Enterprise deployments.
- Added essential and supplemental view disclosure, a four-essential-view authoring bound, accessible presentation requirements, and SEQ and NASA-TLX user-research guidance.
- Added centrally managed campaign semantics and the `campaigns` built-in page for mode-filtered campaign AIC utilization and campaign-run trends.
- Added `dashboard.repository` and **DLS-DOC-012** so a presenter's report action toolbar can expose a GitHub repository link, and added **DLS-SAFE-011** requiring a descriptive refresh control and a labeled repository link.
- Added the `github-api-rate-limits`, `github-api-collector-health`, and `github-api-call-stacks` database tables, reset-safe derived measures, correlated operation attribution, call-site evidence, risk semantics, and strict separation of GitHub quota state from collector/cache health.
- Added constrained custom-page hash-query routing and route-bound templating through **DLS-VIEW-026** and **DLS-VIEW-027**.
- Added route-aware human-readable title allocation and allowlisted `outcome-body-html` presentation through **DLS-SAFE-012**.
- Added dashboard-level site-wide callouts, optional source-row visibility conditions, and volatile accessible dismissal through **DLS-DOC-015** and **DLS-SAFE-014**.
- Updated the complete example to declare repository AIC distribution as a linked, ordered pie chart.
- Added the optional unit `format` and deterministic compact `duration` presentation for human-friendly elapsed times.
- Added the `human-friendly-timestamp` temporal field format for scannable relative and concise date presentation with exact UTC timestamp access.
- Added the `work-items` source fields and `work-project-view` element for GitHub Projects-style Board, Tasks, and Roadmap presentation of workflow work.
- Added declarative operation Marketplace cards using reusable card templates, responsive entity-card grids, row-selected icons, and worker-executed queries.
- Added the attention-first `signal-list` Home presentation, four-state Work display mapping, and composed `insights-overview` element with explicit outcome, AIC, detection, and experiment evidence boundaries.
- Aligned page icons with the presenter's canonical Octicon set and defined the icon-only, tooltip-backed horizon control.
- Added declarative `dashboard.queries` query results in Section 5.5 with required original-specification `subject` metadata, constrained equality joins, an allowlisted computed-field vocabulary, static output schemas, documented resource limits, composed data states, and worker-only execution through **DLS-QUERY-001** to **DLS-QUERY-019**, and revised Section 1.2, **DLS-VIEW-011**, and Appendix C.3 accordingly. `queries` is an optional additive key, so conforming `"0.1.0"` documents without queries remain valid and the language version is unchanged.
- Added first-class `predict` query transforms with Vega-aligned `linear`, `log`, `exp`, `pow`, `quad`, and `poly` regression methods, grouped and multivariate linear fitting, forecast rows, static output fields, and worker-only execution through **DLS-QUERY-021** to **DLS-QUERY-023**.
- Added bounded aggregate-local `filter` predicates for conditional query aggregation, with deterministic per-group execution, explicit empty and null semantics, static validation, and operation-budget accounting through **DLS-QUERY-024** and **DLS-QUERY-025**.

---

## Appendices

### Appendix A: Complete Example (Informative)

```yaml
language-version: "0.1.0"
dashboard:
  id: agentic-operations
  title: Agentic Operations
  description: Workflow activity, usage, findings, and operational value.
  github-url-base: https://github.com
  repository: octo-org/agentic-operations
  defaults:
    scope:
      organizations:
        - octo-org
    time:
      range: 30d
    filters:
      rollout-mode:
        - review
        - live
  units:
    aic:
      name: AI Credits
      symbol: AIC
      significant: 1
      format: number
  pages:
    - id: overview
      kind: built-in
      page: overview
    - id: workflows
      kind: built-in
      page: workflows
    - id: usage-by-repository
      kind: custom
      title: Usage by Repository
      views:
        - id: total-aic
          title: Total AI Credits
          data:
            source: usage
          mark: metric
          encoding:
            value:
              field: aic
              type: quantitative
              aggregate: sum
              unit: aic
        - id: daily-runs
          title: Daily Runs
          data:
            source: runs
          mark: chart
          encoding:
            x:
              field: started-at
              type: temporal
              time-unit: day
            y:
              field: run
              type: quantitative
              aggregate: count
            color:
              field: rollout-mode
              type: nominal
        - id: largest-spenders
          title: Largest AIC Spenders
          data:
            source: usage
            order-by:
              - field: sum-aic
                direction: desc
          mark: chart
          chart: pie
          encoding:
            x:
              field: repository
              type: nominal
            y:
              field: aic
              type: quantitative
              aggregate: sum
              as: sum-aic
            href:
              field: repository-link
```

### Appendix B: Error Codes (Normative)

| Code | Meaning |
|---|---|
| `DLS-E001` | Invalid YAML syntax |
| `DLS-E002` | Invalid YAML document count or root |
| `DLS-E003` | Missing or invalid required field |
| `DLS-E004` | Unknown or duplicate key |
| `DLS-E005` | Non-canonical vocabulary or identifier |
| `DLS-E006` | Unknown source, field, or reference |
| `DLS-E007` | Incompatible mark, channel, type, or time unit |
| `DLS-E008` | Forbidden executable or transformation feature |
| `DLS-E009` | Unsafe, invalid, ambiguous, or incompatible link or `href` reference |
| `DLS-E010` | Invalid scope, filter, time range, aggregation, or aggregate-order reference |
| `DLS-E011` | Invalid entity relationship or source grain |
| `DLS-E012` | Missing required external provenance or data-state metadata; not an `empty` or `unavailable` runtime result |
| `DLS-E013` | Invalid progressive-disclosure configuration or essential-view count |
| `DLS-E014` | Invalid graphical nesting |
| `DLS-E015` | Declared query is unused by rendered dashboard content or another retained query |

### Appendix C: Invalid Examples (Informative)

#### C.1 Multiple YAML Documents

```yaml
language-version: "0.1.0"
dashboard: {}
---
language-version: "0.1.0"
dashboard: {}
```

Invalid because a file contains more than one YAML document.

#### C.2 Non-Canonical ID

```yaml
language-version: "0.1.0"
dashboard:
  id: Agentic_Operations
  title: Agentic Operations
  pages: []
```

Invalid because the ID is not kebab-case and `pages` is empty.

#### C.3 Forbidden Join and Expression

```yaml
- id: combined-usage
  kind: custom
  views:
    - id: calculated-cost
      data:
        source: usage
      join: runs
      mark: metric
      encoding:
        value:
          expression: raw-token-count * rate
```

Invalid because a view **MUST NOT** declare a join or an expression. A relationship between tables or queries and a derived measure are declared instead as a `dashboard.queries` entry under Section 5.5, and the view selects that query by name.

Invalid because `join` and `expression` are not language vocabulary and arbitrary joins and expressions are excluded.

#### C.4 Incompatible Measure

```yaml
- id: summed-value
  kind: custom
  views:
    - id: value-total
      data:
        source: operational-values
      mark: metric
      encoding:
        value:
          field: operational-value
          aggregate: sum
```

Invalid because operational value is non-additive and cannot use `sum`.

### Appendix D: Semantic Distinctions (Informative)

| Concept | Example question answered | Not equivalent to |
|---|---|---|
| Raw tokens | How many provider-reported input tokens were observed? | AIC, cost, outcome, or value |
| AIC | How many authoritative AI Credits were attributed? | Raw tokens, operational grader, or operational value |
| Run conclusion | Did the completed run succeed, fail, time out, or end another way? | Downstream outcome, grader result, or eval result |
| Outcome | Was a safe output later accepted, rejected, pending, ignored, or otherwise classified? | Run conclusion, operational grader, or operational value |
| Grader observation | What result did a named grading criterion emit? | Eval observation or run conclusion |
| Eval observation | Did a named binary evaluation return `yes`, `no`, or `unknown`? | Grader observation, operational grader, or operational value |
| Operational grader | What native metric value did gh-aw publish for one run under the `operational-value` protocol? | Repository operational value, AIC, outcome, normalized score, or causal impact |
| Operational value | What campaign-defined metric was observed for one repository and campaign? | Run-scoped operational grader, AIC, outcome, or causal impact |

----
