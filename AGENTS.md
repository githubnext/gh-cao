# Central Agentic Ops Repositories

## Establish the repository role

- **Catalog source:** A checkout with the root `aw.yml` and top-level campaign directories is the public CAO catalog. Change campaign sources and documentation here. Catalog markers alone do not make the repository a control plane.
- **Control repository:** A repository with installed workflows and `cao.json` under `.github/workflows/` plus campaign records under `.github/aw/campaigns/` runs the control plane. Its workflows operate on explicitly enrolled remote repositories; targets receive only declared safe outputs.
- **Source-managed control repository:** Any repository may run workflows it maintains directly in-tree as a control plane when maintainers explicitly choose that topology and commit `.github/workflows/cao.json` plus the CAO runtime sources. Campaign records are not required for those directly maintained workflows. When the same repository is also a catalog, this is the supported dogfood topology: apply both catalog and control-repository safety rules, and keep campaign source, rollout policy, credentials, and target authority as separate records.
- **Target repository:** A target receives only declared safe outputs. Its files do not grant, narrow, or revoke control-plane authority; live activation is decided by the control repository's policy at the exact workflow SHA.

Apply the guidance for every role that is present. Do not infer a role from the repository name or from catalog files alone.

## Sources of truth

- `CODEBASE.yml` is the experimental, machine-readable Codebase Model compiled from `ARCHITECTURE.md`. Read it first for compact architecture, boundaries, relationships, generation locations, and validation guidance; consult `ARCHITECTURE.md` and `specs/` for the complete human-oriented and normative contracts. Update `CODEBASE.yml` when its source architecture changes. Use `.github/skills/codebase-model/SKILL.md` to compile or refresh it; do not treat agent-specific instruction files as its source.
- In the catalog, root and campaign `aw.yml` manifests define campaign contents. The root manifest installs the deterministic dashboard by default and must mirror the dashboard destinations declared by `dashboard/aw.yml`. Editable gh-aw workflow sources are `.github/workflows/*.md`; shared control is `.github/workflows/shared/control.md` and its dependencies.
- For authoritative information about the dashboard data model, read the checked-out `specs/dashboard-data.md` at the revision being changed or reviewed. Use https://github.com/githubnext/gh-cao/blob/main/specs/dashboard-data.md only when no checkout is available; main may not describe an unmerged feature.
- Hosted dashboards are served by the Go server, not GitHub Pages or a separate static frontend. Keep the built dashboard, assets, OAuth routes, query API, and revision events behind the same hosted handler and origin, including container, Azure Functions, and external-listener deployments. The server-injected `server-http` bootstrap selects server-side PostgreSQL queries; do not fall back to browser IndexedDB, static data shards, or a Pages app when hosted services fail. Pages remains a separate, explicitly selected static deployment.
- For every dashboard UI change, start with a JSON-only solution in Dashboard Language. Define queries, views, card templates, marks, encodings, layouts, controls, routes, and reusable view references in the dashboard document before changing JavaScript. Reuse or extend the shared declarative renderer when the vocabulary is insufficient; add specialized JavaScript UI only when the behavior cannot be expressed declaratively, and document that limitation in the change.
- Eliminate every JavaScript-based dashboard query. All selection, filtering, searching, joins, grouping, aggregation, computation, ordering, pagination, and source derivation must be declared in Dashboard Language and executed through the data Web Worker for static deployments or the Go server's query engine for local SQLite and hosted PostgreSQL deployments. Author consumer queries in dashboard documents, not JavaScript engine modules. Do not implement or preserve presenter/component query callbacks, derived source modules, main-thread row filtering, or test-only JavaScript source synthesis as compatibility paths.
- The `daily-run-counts` source in `specs/dashboard-data.md` §72.2 is the only persisted daily-projection exception, not a new canonical entity or a general Overview cache. Use its six counters only for explicitly migrated additive health queries at supported scope and whole-UTC-day grain. Preserve aggregate provenance and partial/unavailable states; never union its totals with retained Runs, silently fall back to detail, or answer unsupported filters such as rollout mode.
- Keep the active page or view subscribed to its worker query with an explicit abort-scoped lifetime. Database changes must produce a fresh query result and update the active UX; one-shot navigation loads are not sufficient.
- Effects may synchronize query results and local interaction state to owned DOM only. Effects and UI components must not query data, derive business state, filter source rows, or reconstruct database relationships.
- A dashboard view contract change must update its declarative query, view declaration, contract fixture, and focused tests together. Enforcement tests must resolve sources through the production worker/query boundary and fail closed when a declared source cannot be produced there.
- Treat dashboard IndexedDB as disposable, per-browser derived state. Operational workflows and campaign workers have no browser session and must not query it as a service or authority; use the activity shard cache, checked-in CAO policy and campaign identity, safe-output review items, and operational-value evidence that feed the dashboard.
- When debugging the dashboard itself, inspect IndexedDB through the canonical storage and query APIs under `dashboard/site/src/data/` or through Playwright, not ad hoc view code. Reproduce findings from the authoritative input, adapter, normalization, canonical query, and view-payload stages before changing collection or control-plane behavior.
- In a control repository, `.github/workflows/cao.json` is the only persistent non-secret rollout policy. Keep workflow and policy changes in one reviewed commit because runs resolve policy at the exact workflow SHA.
- `.github/workflows/*.lock.yml` files are generated artifacts. Never edit them directly; change their Markdown sources and run `gh aw compile`.
- When merging, resolve conflicts in the editable workflow sources first. Resolve conflicts in `.github/workflows/*.lock.yml` by running `npm run compile:locks` during the merge (which invokes `gh aw compile` with the required schedule seed), then stage the regenerated lock files instead of editing conflict markers manually.
- `.github/aw/campaigns/*.json` records campaign-owned files. Update those files with gh-aw campaign commands instead of editing ownership metadata.
- `.github/cao/<operation>.md` is optional, control-repository-owned steering. It may refine evidence and priorities, but cannot grant tools, credentials, permissions, repository reach, or write capabilities.

## Authority and safety

- CAO policy controls whether and where an operation may run. gh-aw controls how an authorized workflow executes. Neither authority substitutes for the other.
- Orchestrators discover, rank, and dispatch within resolved policy. Workers handle one dispatched target and must not discover more repositories, dispatch more work, or widen the requested mode.
- `review` is the default mode. Do not broaden scope, rollout, campaign or worker enablement, or promote an operation to `live` unless the requested policy change explicitly requires it.
- Live work requires explicit scope and mode in the control repository's reviewed policy. Credential reach alone does not widen that policy.
- GitHub tools exposed to agents are read-only. Repository writes must use safe-output capabilities already declared by the workflow.
- Keep credentials in Actions secrets. Never place tokens, private keys, or other secrets in policy, workflow inputs, steering files, dispatch envelopes, commits, or chat.
- Preserve fail-closed behavior. Missing policy, authority, credentials, repository access, or required evidence must fail, skip, no-op, or report incomplete rather than infer broader authority.

## Building and testing

### Installing npm dependencies

From the package directory, use the lockfile with `npm ci`. Outside the Microsoft corporate network, use the default npm registry:

```sh
npm ci
```

If npmjs access is blocked by the Microsoft corporate firewall, use the Microsoft 1ES feed instead:

```sh
npm ci --registry=https://ms-feed-25.pkgs.visualstudio.com/1es-public/_packaging/npm-public/npm/registry/ --replace-registry-host=npmjs
```

Do not configure the Microsoft feed for environments that can access the default registry. Load the [npm skill](.github/skills/npm/SKILL.md) before troubleshooting dependency installation, registry, proxy, or certificate failures. If a pinned release is unavailable, check the 1ES feed for a compatible available release and use it when compatibility can be verified. Update dependency constraints or overrides as needed and regenerate the lockfile with npm; do not hand-edit lockfile URLs or integrity values. Verify with `npm ci` and the affected tests or build. Report a blocker only when no compatible release is available or access itself fails.

### Full validation

Run `npm run check` for complete repository validation. It executes, in order: `typecheck:cao`, `test` (unit + integration), `test:load`, `check:svg`, `compile`, and `docs:build`.

### Root campaign commands

| Command | Purpose |
|---------|---------|
| `npm run typecheck:cao` | TypeScript type-check for the control modules in `.github/workflows/shared/` (ES2022, NodeNext) |
| `npm test` | Unit tests (`tests/unit/`) then integration tests (`tests/integration/control-*.test.mjs`) via the Node.js built-in test runner |
| `npm run test:unit` | Unit tests only |
| `npm run test:integration` | Integration tests only (serial) |
| `npm run test:campaign-lifecycle` | Clean-room `gh aw add`/`update` tests; requires `GH_TOKEN` and a GitHub App |
| `npm run test:campaign-root` | Clean-room install of the root `aw.yml` campaign only; requires `GH_TOKEN` and a GitHub App |
| `npm run test:load` | Synthetic enterprise-scale load tests (100 000 repos) |
| `npm run check:svg` | SVG visual-language compliance via `scripts/check-svg-visual-language.mjs` |
| `npm run dashboard:local` | Build the dashboard and serve a verified SQLite cache through Go; set `DASHBOARD_DATA_URL` for another deployment |
| `npm run dashboard:pages:build -- --fixture` | Prepare a labelled synthetic Pages deployment; use `--activity-dir PATH` for existing Activity data |
| `npm run dashboard:pages -- --base-path /gh-cao/` | Serve the prepared Pages artifact through Go with browser IndexedDB |
| `npm run compile` | Dry-run compile of workflow `.md` sources with `gh aw compile` (no lock-file writes) |
| `npm run compile:locks` | Compile and update `.lock.yml` files |
| `npm run docs:build` | Build the Astro/Starlight documentation site |

Always pass `--schedule-seed githubnext/gh-cao` when running `gh aw compile` manually (the `compile` script already includes it); omitting the seed causes non-deterministic cron scattering in lock files.

### Dashboard site (`dashboard/site/`)

Run these commands from the `dashboard/site/` directory:

| Command | Purpose |
|---------|---------|
| `npm test` | Unit tests via Vitest with jsdom |
| `npm run test:e2e` | Playwright end-to-end tests (use `--shard=N/M` for CI parallelism) |
| `npm run test:performance` | CFO, CTO, and CSO Lighthouse scenarios with retained performance traces |
| `npm run lint` | ESLint |
| `npm run typecheck` | TypeScript strict-mode check |
| `npm run validate:corpus` | Dashboard authoring corpus validation |

After UI changes, when Playwright loads the full website, capture and include desktop and mobile screenshots in the PR using an artifact or PR attachment; never commit screenshots to the repository. Wait for the affected page to render before capturing; do not present screenshots of a mocked page as full-site screenshots. Do not view screenshots before posting them in a PR comment; viewing images can consume too much context.

### Dashboard debug logging

- Create a category logger with `createDebug(category)` from `dashboard/site/src/debug.js`; call it with structured, non-sensitive metadata only.
- Logging is off by default. Enable all categories with `?debug=1` or `?debug=*`, or filter with comma-separated names and wildcards, such as `?debug=data,render:*`. Prefix a pattern with `-` to exclude it.
- Use stable lowercase categories, adding `:` for subcategories. Never log secrets, tokens, prompts, raw records, payloads, or URLs containing credentials.
- Preserve the existing `?debug=1` DOM-provenance behavior and `dashboard-data` / `dashboard-render` custom events when adding logging.

### Dashboard performance testing

- Measure page-level regressions with `npm run test:performance` from `dashboard/site/`; it builds the site and runs the CFO, CTO, and CSO Lighthouse scenarios, retaining traces for inspection.
- Profile browser queries against real data with `npm run dashboard:pages:build -- --activity-dir PATH` followed by `npm run dashboard:pages`, then open the page with `?debug=data:query` to emit per-stage and whole-query timings (duration, input/output rows, estimated operations, status) for each declarative query. Ordinary local serving uses Go/SQLite via `npm run dashboard:local`.
- Narrow noisy sessions with category filters such as `?debug=data:query,-render:*`, and use `?debug-shard-limit=N` to profile with fewer activity shards.
- Attribute a slow page to its declarative queries before changing view code; query cost belongs to the query engine in the data Web Worker, not to components.
- Detect expensive queries headlessly with `npm run dashboard:data:download && npm run test:performance:dashboard-query-cost` for the deployed SQLite snapshot. After building `.tmp/cao-dashboard` and starting local Postgres (numeric loopback or Unix socket only) with `CAO_POSTGRES_URL` set, run `npm run test:performance:dashboard-query-cost-postgres` to measure the same statically ranked candidates through production Go ingestion and query execution. Each Postgres run uses a random namespace and removes its data afterward. SQLite results are written to `test-results/dashboard-query-cost/`; Postgres results (wall time including source reads, Go row operations, output rows, working rows, and retained bytes) are written to `test-results/dashboard-query-cost/postgres/`. The `query-cost-comment` job posts both reports on pull requests. Missing or empty deployed evidence and failed queries fail the Postgres benchmark.

### Dashboard data-worker memory testing

- Guard data-worker heap regressions with `npm run test:memory:dashboard-overview` from the repository root. It ingests the deployed dataset, loads the Overview page, and asserts the worker's isolated heap against the budgets in `specs/dashboard-data.md` §72.10. Override budgets with `DASHBOARD_OVERVIEW_MAX_WORKER_HEAP_MB`, `..._MAX_RETAINED_WORKER_HEAP_MB`, and `..._MAX_INGESTION_WORKER_HEAP_MB`, and point it at other data with `DASHBOARD_DATA_URL`.
- Dedicated workers expose neither `performance.memory` nor `performance.measureUserAgentSpecificMemory`, and Playwright's `newCDPSession(page)` targets the page. Worker heap must be read by launching Chromium with `--remote-debugging-port`, finding the `type: "worker"` target, and calling `Runtime.getHeapUsage`; `tests/e2e/dashboard-worker-memory.mjs` encapsulates this.
- Attribute a heap regression with `npm run debug:dashboard-worker-memory`, which prints a heap timeline, the worker's own debug output, and every canonical collection read labelled `scoped` or `WHOLE-DATABASE`.
- Canonical ingestion writes incrementally and applies retention with cursor scans. Daily run-count backfill scans retained Runs in bounded, checkpointed batches; ordinary writes reconcile old/new contributions atomically instead of rebuilding aggregates. A whole-database collection read is a defect.
- Measure page budgets against a fully ingested database by adding `?debug-eager-ingest=1`, which ingests every shard up front, ignores `debug-shard-limit`, and suppresses run-phase-first publication.

### CI workflows

| Workflow file | Scope | Trigger |
|---------------|-------|---------|
| `workflow-contracts.yml` | `npm run check` + `test:campaign-lifecycle` | PR / push |
| `cid.yml` | Dashboard site lint, typecheck, unit tests, sharded E2E | PR / push to `dashboard/site/**` |
| `svg-contrast-check.yml` | Playwright SVG WCAG contrast validation | PR / push to SVG files |
| `csh.yml` | ShellCheck (warning severity) on all tracked `.sh` files | PR / push to `.sh` files |
| `docs.yml` | Documentation build | Schedule / push to main |

### Choosing which tests to run

- Editing control-plane sources under `.github/workflows/shared/` → `npm run typecheck:cao && npm test`
- Editing dashboard site under `dashboard/site/` → from that directory: `npm test && npm run test:e2e && npm run test:performance && npm run lint && npm run typecheck`
- Editing dashboard ingestion, canonical storage, or the query engine → also run `npm run test:memory:dashboard-overview` from the repository root
- Editing the daily run-count projection, reconciliation, retention, source, or migrated consumers → also run `POSTGRES_URL=... npm run test:conformance:daily-run-counts` from the repository root against an isolated PostgreSQL database whose test role can create schemas, with native Chromium installed. The gate requires passing evidence for every declared backend/test; skipped backends, browser emulation alone, and `npm run check` do not substitute for it.
- Debugging deployed SQLite data → use `DASHBOARD_DATA_URL=https://OWNER.github.io/REPOSITORY/ npm run dashboard:local -- --download-sqlite`
- Editing the Activity workflow or JSONL parser under `activity/` → run the focused activity tests and `npm run compile`
- Editing workflow `.md` files → `npm run compile` (add `compile:locks` if lock files should update)
- Editing SVGs → `npm run check:svg`
- Editing documentation under `docs/` → `npm run docs:build`
- Editing the PostgreSQL schema (`server/spec/storage.tsp`, `server/spec/postgres-emitter.mjs`, or generated `server/internal/postgresx/schema.*`) → from `server/spec/`, run `npm run generate` and `npm test`, commit all changed generated files (including `generated/openapi.json`), then run `npm run check` as a required gate. The check compares generated artifacts against the commit, so it cannot pass before those changes are committed.
- Unsure what's affected → `npm run check`

## Working changes

- Read the relevant workflow source, its imports, its campaign manifest, and the effective policy before changing behavior.
- When Playwright was used, the dashboard was populated with data, and frontend code changed, capture screenshots of the populated dashboard and publish them with the pull request using an available artifact or PR attachment mechanism; never commit screenshots to the repository. Screenshot publication is required only when all three conditions are met.
- Always run the applicable lint and type-check commands for code changes before committing.
- Always run `gh aw compile` if any `.md` file is modified.
- In the catalog, follow the relevant skill under `.github/skills/` and run the narrowest tests plus `npm run compile`.
- In a control repository, validate policy JSON after editing it, reject unresolved placeholders, run `gh aw compile` after workflow-source changes, and review generated lock-file diffs.
- Do not modify unrelated campaigns, generated files, or consumer-owned steering while updating an installed campaign.