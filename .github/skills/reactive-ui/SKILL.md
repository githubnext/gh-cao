---
argument-hint: Dashboard page, view, or UI element
description: Build or revise CAO dashboard UI with Dashboard Language, reusable UI elements, and the reactive.js framework.
metadata:
    local-path: /home/runner/work/gh-cao/gh-cao/.github/skills/reactive-ui
name: reactive-ui
---
# Reactive UI

Build dashboard experiences declaratively first. Use JavaScript only for interaction or presentation that Dashboard Language and the shared presenter cannot express.

## Decision order

1. Read `docs/dashboard-language-specification.md`, the affected dashboard document, and the existing presenter and component patterns.
2. Express data shaping under `dashboard.queries`; keep joins, projections, aggregates, filters, ordering, and computed fields out of view-specific JavaScript.
3. Prefer an existing Dashboard Language mark, encoding, layout, section, route, control, or named UI element. For lists of items that may grow large, prefer a full-view lazy card list (`lazy-list: true`, `layout: full-view`, a declared card template) over eager tables or custom list rendering.
4. Extend a shared declarative renderer when the missing behavior applies to an existing mark.
5. Add a named UI element only when the experience requires reusable interaction or presentation that the declarative vocabulary cannot represent. Record a concise `intent` and declare every source it consumes.
6. Do not infer specialized behavior from page IDs, view IDs, source names, or source contents.

## Filters on full-view lazy lists

Full-view lazy card lists (`mark: list` with `list.style: entity-cards`, or `mark: table` with `encoding.card`, plus `lazy-list: true` and `layout: full-view`) get dropdown filters from the view-level `filter-bar` object (spec §11, DLS-VIEW-031–034). It is JSON-only: the presenter, worker, and Go query engine already compile selections into exact `in` predicates (OR within a field, AND across fields) before pagination. Do not add presenter callbacks or JavaScript row filtering. The page-level Boolean `filter-bar` is the unrelated global horizon/rollout bar.

1. **Expose each filter field on the view source.** In `dashboard.queries`, compute every filterable value as a stable, human-readable label and `select` it, such as `Enabled`/`Disabled`/`Unknown`. Keep missing evidence as a distinct `Unknown`-style value; never coerce it into a known state. Derive per-entity flags from child rows with aggregate-local filters, for example `{ "field": "workflow", "as": "enabled-workflow-count", "reducer": "count", "filter": { "predicates": [{ "field": "workflow-active", "equals": "true" }] } }`. Then `coalesce` the counts after left joins and use `if` to compute the label.
2. **Declare one option query per filter.** It reads `from` the view source, aggregates `by` the filter field with a `count` value, and declares `order-by`. Options must come from the same rows and labels as the view, so every option matches at least one row exactly. Joined `distinct-list` values, such as `v1, v2`, are separate options.
3. **Declare the view `filter-bar`.** Add `filters` in display order. Give each filter a unique `id`, a `label`, and `groups` containing `{ label, field, source, value-field }`. Add `label-field` only when display text differs from the value. Each `field` may appear once per view.
4. **Show the field on the card.** Add it to `encoding.columns`, and add it to the card template's `labels` or `details` in `dashboard.json`, so users can see why a card matched.
5. **Test and regenerate.** Extend the query test in `dashboard/site/test/unit/data-queries.test.js` with view rows and the option-source values. Assert the `filter-bar` field mapping in the page's view test. Run `npm run generate:agent-catalog` from the repository root, then run `npm run lint`, `npm run typecheck`, and `npm run validate:corpus` from `dashboard/site/`.

Example: the Repository maintenance (`#page-maintenance`) view filters `upgrade-state`, `workflow-state`, and `gh-aw-version` through the `maintenance-repository-*-options` queries.

## Named UI elements

- Register elements in `dashboard/site/src/components/ui-elements.js` and render only from the supplied `ElementRenderContext`.
- Put element-specific DOM creation, updates, event handling, accessibility, loading, empty, partial, unavailable, and error states inside the UI element or reusable component it delegates to.
- Keep specialized views declarative: they select sources and an element but do not directly query, create, replace, or mutate DOM nodes.
- Reuse `dashboard/site/src/dom.js`, shared UI primitives, chart elements, badges, formatters, Octicons, and view chrome instead of duplicating markup or behavior.
- Return one owned root element. Keep selectors and events scoped to that root, preserve semantic HTML and keyboard operation, and expose accessible names and state.
- Add the element to `EMPTY_AWARE_ELEMENTS` only when it renders honest empty or unavailable states itself.
- Use lazy registration for substantial elements that are not needed on every page.

## Reactive state

Use `dashboard/site/src/reactive.js` for state-driven updates:

- `state` owns mutable local state; use its functional setter when the next value depends on the current value.
- `derived` computes values from reactive dependencies. Dispose it when its owner is removed.
- `effect` performs the smallest DOM update needed from reactive state. Stop the handle when work is complete or the owner is removed.
- `render(root, update, { signal }?)` renders reactive output into an isolated shadow tree and morphs it into an owned root with the fewest mutations, preserving focus, scroll position, and control state without hand-written diffing. Prefer it over a raw `effect` plus `replaceChildren` whenever a component's entire visible output is a function of its reactive state, as in `dashboard/site/src/components/animated-number.js`, `factory-station.js`, and `factory-header.js`.
- `onCleanup` removes listeners, observers, timers, and other resources created by an effect.
- `batch` groups related writes so dependents observe one consistent final state.
- Pass an `AbortSignal` to effects whose lifetime follows a page, view, request, or asynchronous operation. Use `createFactoryScope()` from `dashboard/site/src/components/factory-elements.js` to obtain that signal and bind it to detachment of an owned root instead of writing a bespoke `MutationObserver`/`wasConnected` pair; every new element should stop its effects and requests once its root leaves the document.
- Use stable keyed rendering from `dashboard/site/src/dom.js` for changing collections; do not rebuild an entire view when only state or list membership changed.
- Do not put source derivation, querying, or business rules in effects. Perform those operations in Dashboard Language or the data worker and react only to their results.

## DOM ownership

- Build markup with `h` and shared render helpers. Avoid `innerHTML`, inline event attributes, document-wide selectors, and ad hoc `document.createElement` calls in specialized views.
- A component that creates a node owns its updates and cleanup. Parent views compose component roots rather than reaching into their descendants.
- Keep asynchronous rendering race-safe: ignore or abort stale work and never update a detached or superseded element.
- Preserve focus, scroll position, and control state across reactive updates. Replace only the smallest owned subtree.
- Publish transient user feedback through `dashboard/site/src/notification-service.js`. Do not create view-specific toast containers or duplicate their lifecycle, accessibility, or responsive behavior.
- Data-worker code may call `publishWorkerNotification`; keep worker notifications serializable and let the main-thread notification service own presentation and actions.

## Styles and Primer

- Put dashboard component styles in the focused `dashboard/site/src/styles-*.js` modules composed by `styles-primer.js`; `styles.js` remains the shared minified stylesheet entry point. Preserve composition order and keep responsive and accessibility overrides last. Do not add inline styles or element-local style injection for ordinary views.
- Reuse existing classes and CSS custom properties before adding selectors or tokens.
- Follow the established Primer visual language: system font stack, semantic canvas/foreground/border/accent/status tokens, Octicons, six-pixel control radii, restrained elevation, compact spacing, and visible focus rings.
- Support light, dark, and explicit dashboard themes through existing semantic tokens. Do not hard-code a color where a token exists.
- Use color as a supporting cue, never the only status signal. Maintain readable contrast, reduced-motion behavior, responsive layouts, and touch-sized interactive targets.
- Scope new selectors to the reusable component or named element. Do not encode page or view identity into shared styling.

## Validation

1. Add focused unit tests for reactive transitions, cleanup, empty and unavailable states, and accessible output.
2. Add or extend a Playwright test when behavior depends on real browser layout, navigation, workers, focus, scrolling, or responsive interaction.
3. From `dashboard/site/`, run the focused tests, `npm test`, `npm run lint`, and `npm run typecheck`. Run focused end-to-end tests for browser-facing changes.
4. Validate every changed dashboard document with the repository validator. If a workflow Markdown source changes, run the repository workflow compile command.
5. For daily run-count projection, reconciliation, retention, source, or migrated-consumer changes, also run `POSTGRES_URL=... npm run test:conformance:daily-run-counts` from the repository root with isolated PostgreSQL (test role can create schemas) and native Chromium. The contract is in the checked-out `specs/dashboard-data.md` §72.2. Skipped backends/tests, browser emulation alone, or `npm run check` is not a substitute for this gate.

## Completion contract

- Dashboard Language owns all representable queries and view composition.
- Named UI elements own specialized DOM and interaction.
- Reactive resources have explicit lifetimes and cleanup.
- Styles are centralized, reusable, responsive, accessible, and Primer-aligned.
- Tests cover the declarative boundary and every new reactive behavior.
