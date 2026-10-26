---
argument-hint: Dashboard page or view id to migrate
description: Migrate a CAO dashboard view from published or derived logical-source files to the unified canonical data model. Use when redoing, converting, or moving a dashboard view to the view -> data query -> Web Worker -> view payload architecture.
metadata:
    local-path: /home/runner/work/gh-cao/gh-cao/.github/skills/migrate-dashboard-view
name: migrate-dashboard-view
---
# Migrate Dashboard View

Migrate one view at a time to a request-scoped declarative query over canonical evidence or an explicitly permitted projection, preserving the source-shaped payload expected by the renderer.

## Procedure

1. Locate the view in `dashboard/site/dashboard.json` or one of its declared files under `dashboard/site/dashboard-fragments/`. Record its page id, view id, `data.source`, filters, ordering, route fields, and every field consumed by its encoding or UI element.
2. Read the relevant contracts in the checked-out `specs/dashboard-data.md` and `docs/dashboard-language-specification.md` at the revision being migrated. Trace the source only far enough to identify its current producer, grain, and registered canonical or permitted projection source. `daily-run-counts` (§72.2) is the only persisted daily-projection exception; it is not a new canonical entity or a general Overview cache.
3. Add the smallest query under `dashboard.queries` in the owning dashboard document or fragment. Reuse registered sources and earlier queries; preserve field names and metadata semantics at the renderer boundary. Base canonical mappings belong in `dashboard/site/src/data/queries/database.json`. Do not add view-specific query definitions or callbacks to `declarative.js` or `index.js`; extend the shared engine or indexed storage access only when the declared vocabulary or access pattern is missing.
4. Make the query request-scoped. A view request must execute only its required canonical reads and return only requested source payloads. Do not materialize every collection or send unrelated metadata shells.
5. Use the production query boundary: `dashboard/site/src/data-processor.js` and `dashboard/site/src/data-worker.js` for static deployments, or the Go server query API for local SQLite and hosted PostgreSQL deployments. Keep canonical records and IndexedDB access inside the worker; send serializable query inputs in and bounded source-shaped payloads out. Keep the active view subscribed with an explicit abort-scoped lifetime so revision changes refresh it and detachment cancels work.
6. Remove a superseded legacy derivation only when no unmigrated view still owns it. Do not add a compatibility fallback, and do not rewrite unrelated views, generated data, or published source producers in the same migration.
7. Update the declarative query, view declaration, contract fixture, and focused tests together. Resolve sources through the production worker/query boundary, never test-only source synthesis; fail closed when a declared source cannot be produced. Add or extend a Playwright test under `dashboard/site/test/e2e/` to exercise the real module worker, IndexedDB generation, navigation, revision refresh, and detachment.
8. Run the focused unit test, focused Playwright spec, `npm run typecheck`, and `npm run lint` from `dashboard/site/`. For daily run-count projection, reconciliation, retention, source, or migrated-consumer changes, also run `POSTGRES_URL=... npm run test:conformance:daily-run-counts` from the repository root with isolated PostgreSQL (test role can create schemas) and native Chromium. Skipped backend/test evidence, browser emulation alone, or `npm run check` is not a substitute. Report any broader validation not run.

## Daily run-count consumers

For `workflow-runtime-health-summary`, `runs-daily-conclusions`, and
`repository-run-inventory`, preserve the §72.2 contract: sum daily counters at
their declared grain, keep detail tables and usage on their existing sources,
and preserve aggregate provenance and partial/unavailable states through joins.
Time bounds select whole UTC start-day buckets by midnight, not exact sub-day
executions. Only organization, Repository, and Workflow context filters are
supported; reject rollout-mode and other unsupported selections explicitly.
Never union daily totals with retained Runs, substitute detail when the daily
source is unavailable, or turn an unavailable join into zeros. Daily buckets and
receipts retain the current UTC Monday-start week and its two preceding weeks
independently of detail-retention overrides; backfill cannot recover evicted
history.

## Completion Contract

- The migrated view renders from registered canonical sources or the explicitly permitted §72.2 projection, not legacy precomputed view rows.
- The production query boundary performs the query and returns only the requested view payload.
- An abort-scoped subscription refreshes the active view on revisions and releases detached work.
- The query, view, contract fixture, and focused tests agree on grain, coverage, and unavailable behavior.
- Existing renderer behavior and unmigrated views remain compatible.
- Unit and browser tests prove the end-to-end path.
