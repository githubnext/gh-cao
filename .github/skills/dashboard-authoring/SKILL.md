---
name: dashboard-authoring
description: Define one agentic-workflow dashboard intent from available canonical data.
---

# Dashboard Authoring

Create one dashboard that helps an operator make a bounded decision from the canonical evidence published by gh-aw and CAO.

## Procedure

1. Define one bounded workflow task and a concise, outcome-oriented intent.
2. Derive activation, required-effect, no-op, success, and uncertainty conditions.
3. Select only measures and dimensions declared by the database table contract. Preserve upstream metric IDs, native numeric values, units, directions, ordering, and nulls without inventing definitions, scales, baselines, maturity, or causality.
4. Define a compact dashboard intent with no more than four essential views per page. An analytical Insights view begins with a visual or ranked summary that answers its primary operator question at a glance on a phone. Home and entity-detail pages may begin with a ranked list or state summary when that better supports the task. Never add a chart or diagram when the available evidence cannot support its comparison, trend, distribution, categorical history, or structural relationship.
   - Choose a pie chart for a current distribution, a line chart for a quantitative temporal trend, a swimlane chart for categorical observations over time, or a treemap for positive proportional contributions with optional categorical nesting. Use facets for small multiples and layers for complementary marks over one source; follow the [declarative chart reference](../generate-dashboard-ir/references/declarative-charts.md) for bounds, independent scales, and responsive layouts. A semantic visualization may better answer lifecycle, provenance, coordination, attribution, or maturity questions when supported by the evidence and declared vocabulary. Put supporting metrics, tables, and detail views after the summary.
   - Avoid nested chart boxes. Do not add a section solely to frame a chart; use sections only when their heading or grouping adds operator context.
5. Pass the intent to `generate-dashboard-ir` with the provided Dashboard Language specification and validator.

## Graphical patterns

### Flat supplemental diagnostics

When a diagnostic page has one essential summary table and several supporting tables, render the summary first and make each supporting table a sibling `supplemental` disclosure. Omit layout sections so a table is not nested inside both a section and a disclosure.

## Data and database work

Before implementing a data-backed feature or modifying a database, write or update the normative requirements for both the SQL layer and the IndexedDB layer in [`specs/dashboard-data.md`](../../../specs/dashboard-data.md). Define the source contract, canonical mapping, identities and relationships, migration behavior, retention, failure handling, and SQL/IndexedDB parity before changing implementation code.

Follow the data-injection boundary documented in [`docs/dashboard-data-model.md`](../../../docs/dashboard-data-model.md): authoritative inputs pass through source adapters, canonical normalization, canonical storage, and the query layer before reaching a view. Views must not parse upstream logs or query storage directly. Treat browser IndexedDB and the local PostgreSQL projection as disposable, reconstructable derived state.

Read the checked-out data specification at the revision being authored, not only
main's online copy. Its §72.2 `daily-run-counts` source is the only persisted
daily-projection exception, not a new canonical entity. Explicitly migrated
additive run-health consumers sum its counters at supported scope and
whole-UTC-day grain; they preserve partial/unavailable provenance and never
union totals with retained Runs, silently fall back to detail, or accept
unsupported filters such as rollout mode. Other analyses keep their declared
sources.

Use the shared dashboard notification service only for brief, actionable runtime feedback. Keep durable findings, workflow outcomes, and operator attention items in Dashboard Language sources and views rather than transient notifications.

Use [`gh aw logs`](https://github.com/github/gh-aw/blob/main/docs/src/content/docs/troubleshooting/debugging.md) as the source of workflow-run evidence. The published activity shards use the gh-aw [`logs-jsonl.schema.json`](https://github.com/github/gh-aw/blob/main/schemas/logs-jsonl.schema.json); consult that schema rather than inferring fields from fixtures or individual deployed records.

Operational-grader design and result semantics belong to gh-aw's `operational-value` protocol. Dashboard work may consume the canonical grader result, but must not redefine, normalize, clamp, replay, mature, reconstruct its metrics, or confuse it with package-defined repository operational value.

For real feature-development evidence, download a selected `cao-activity-index` Actions artifact into `.cao/`, or run `cao download --url URL` with an explicit operational `payload-hashes.json` endpoint. There is no default remote source; Pages contains synthetic demo data only. Query the canonical PostgreSQL data with `cao query`. Real data is development evidence, not schema authority; follow the specifications and JSONL schema.

Before treating a source as complete, preserve canonical source availability and coverage metadata. Historical analysis requires explicitly retained evidence and a separately provisioned PostgreSQL namespace; never repair or reinterpret a database file. Browser IndexedDB is not an operational archive.

These flags preserve run/detail history only. Daily buckets and contribution
receipts retain the current UTC Monday-start week and its two preceding weeks
independently of detail retention; expired replay cannot reopen buckets and
backfill cannot recover already-evicted history. Use appropriate retained
canonical detail sources for historical or unsupported-dimension analysis,
without substituting them for the three migrated daily-count consumers.

For changes to the daily projection, reconciliation, retention, source, or
migrated consumers, run
`POSTGRES_URL=... npm run test:conformance:daily-run-counts` from the repository
root with isolated PostgreSQL (test role can create schemas) and native
Chromium. Skipped backends/tests, browser emulation alone, or `npm run check`
does not establish this cross-backend conformance.

## Campaign file convention

- Store an operation campaign's production Dashboard Language document at `<campaign>/dashboard.json`.
- Store it at `<campaign>/dashboard.json`, where `<campaign>` is the campaign's canonical identifier; the CAO materializer preserves the campaign directory at that same path.
- Keep each campaign dashboard independently valid. The dashboard campaign bundles installed `<campaign>/dashboard.json` documents into the single deployed `dashboard.json` that the browser loads.
- Do not add campaign pages directly to `dashboard/site/dashboard.json`; that file contains the built-in dashboard configuration.
