# Dashboard Language Renderer

Production Dashboard Language validator and presenter for the Central Agentic Ops dashboard campaign.

The dashboard build workflow copies this directory to its configured `site-path` and bundles installed `<campaign>/dashboard.json` documents into a small core `dashboard.json` plus per-page `dashboard-pages/*.json` chunks. The browser loads the core shell first, fetches page chunks on demand, and renders query payloads from the canonical data layer without page-specific HTML generation or report-source builds.

The esbuild pipeline coalesces the application's static startup dependencies while preserving shared module instances, original source maps, and lazy route boundaries. Startup module preloads use low fetch priority so they do not compete with the render-blocking stylesheet on mobile connections. The declared initial page definition loads alongside the application; diagnostics, illustrative fixtures, optional UI elements, and the WebMCP adapter load only when needed. Pie chart loading skeletons retain their authored heading and description to reserve space before query results arrive.

## Source composition

An authoring `dashboard.json` may declare a top-level `fragments` array of JSON paths relative to that file. Each fragment is a partial `dashboard` object whose array fields are appended in declaration order. Keep coherent feature slices together: one fragment may contain multiple related `queries`, reusable `views`, and `pages`.

```json
{
  "language-version": "0.1.0",
  "fragments": ["dashboard-features/repositories.json"],
  "dashboard": {
    "id": "central-agentic-ops-dashboard",
    "title": "Central Agentic Ops"
  }
}
```

Fragment paths must remain within the root document's directory, including after symbolic-link resolution. Fragments cannot include other fragments. Builds and local previews fully compose authoring fragments before campaign composition, validation, and runtime page chunking, so the deployed `dashboard.json` and `dashboard-pages/` format is unchanged.

The built-in dashboard keeps its shared query foundations and its controls, repository, workflow/run, campaign, inventory, operations, overview, and entity surfaces under `dashboard-fragments/`. Each file stays below the repository's size limits and owns a coherent set of related queries, reusable views, and pages.

## Data pipeline

1. The activity action collects bounded raw inventory and run evidence for operational artifacts. The collectors under `dashboard/report/` retain evidence acquisition and normalization, not dashboard query projections.
2. Activity artifacts contain `inventory-sources.json`, normalized run and record JSONL shards, SQLite, and their `payload-hashes.json` manifest. Production uses same-origin Go/PostgreSQL; local development uses the same Go server with a real operational SQLite snapshot.
3. Pages instead generates synthetic enterprise raw samples through production normalization, then the data Web Worker incrementally ingests them into canonical IndexedDB. It has no operational input, SQLite download, agent summary, or live services.
4. Dashboard Language queries in `src/data/queries/database.json` and the composed dashboard document perform selection, joins, aggregation, and projection at the production data-layer boundary. Supplied report rows cannot override canonical producers; undeclared sources are unavailable.
5. The renderer loads the active page declaration and subscribes to its query payload with an abort-scoped lifetime. Database updates refresh the active view; only requested sources cross the worker boundary.

All profiles execute the same canonical Dashboard Language definitions and
shared views. The Pages demo is not presenter fixtures: its declarative
**Synthetic enterprise demo** callout accompanies a full canonical pipeline.
Demo data files live under `demo-data/`, not root `payload-hashes.json`;
browser storage keys use `synthetic-demo` and service-worker caches use
`demo`. The changed URLs prevent old same-origin cached real data from being
selected. Operational downloaders require an explicit direct manifest URL or
authenticated Activity artifact acquisition; Pages is not their default.

The browser caching and ingestion behavior below applies to the synthetic
demo, not hosted PostgreSQL or local Go/SQLite. Removing real-data Pages
publication does not eliminate IndexedDB, Web Worker, service-worker caching,
or cross-adapter conformance obligations.

The Ingestion page identifies browser-local IndexedDB, local-server SQLite, or server PostgreSQL storage from deployment configuration, not activity evidence. Server deployments declare `dashboard-storage-backend` metadata (`sqlite` or `postgres`); undeclared server storage is shown as unknown. Table counts use canonical table/object-store names.

Work-item queries rank runs by canonical `startedAt`, falling back to `createdAt`, then break ties by run identity and attempt. Inventory observation times are not Run timestamps.

Add `?online=1` to the dashboard URL to require fresh network responses for the page, its assets, and dashboard data instead of falling back to the service worker's offline cache. The worker bypasses the browser HTTP cache for these requests and redownloads unchanged activity shards during automatic data updates; if the network is unavailable, requests fail rather than showing cached files. Removing the parameter restores offline fallback.

The service worker caches dashboard JavaScript only while the dashboard is open as a pinned/installed app (standalone or minimal-ui display mode, including iOS Home Screen apps). Regular browser tabs fetch JavaScript from the network and never fall back to the app's cached scripts, even when an installed app is open alongside them. App-mode detection runs inline before the dashboard module loads so offline startup also works after the worker restarts; this browser lifecycle behavior is outside Dashboard Language.

Once the installed app has cached its assets successfully, the synthetic demo
shell, JavaScript, and page chunks are available offline. Previously downloaded
demo data remains in local storage; data never downloaded is unavailable offline.
On data-saving or slow connections, eligible cached files are preferred and
automatic downloads are deferred. `?clear-app=1` or **Clear app** in Settings
clears the app's scoped storage before reopening (network access is then required).

Redeploying the demo replaces current Pages artifacts, not historical
deployments or browser copies. Former real-data databases are unused disposable
state. The demo does not automatically delete databases or caches outside its
namespace; explicitly clear affected legacy site data when required.

If a service worker update breaks the app, open the dashboard URL with `?force-update=1` while connected. This recovery runs in the cached HTML before the dashboard JavaScript, checks that the worker script is reachable, unregisters the dashboard's worker, and removes cached app files without deleting IndexedDB or downloaded data. It then navigates to a fresh `?online=1` page. If even the cached page cannot load or run scripts, clear this site's service workers and cached files in the browser's site-data settings, then revisit the dashboard.

The [Overview component model](../../docs/dashboard-overview-components.md) documents that page's UI ownership boundaries, state coverage, and fixture-based visual testing convention. The [dashboard view catalog](../../docs/dashboard-view-catalog.md) indexes every standardized product view, built-in page, mark, chart, and named element.

## WebMCP adapter

Dashboard pages define the semantic capabilities of the dashboard. Human rendering and WebMCP are generated adapters over the same page definitions and the same query execution path, so there is no second tool catalog to maintain. See the [WebMCP documentation](../../docs/dashboard-webmcp.md) for the operator-facing guide.

- `src/webmcp/manifest.js` is a pure generator. It derives one read-only tool descriptor per agent-facing page: `page.id` becomes the `cao_<page_id>` tool name, `title` becomes the tool title, `description` or `intent` becomes the tool description, a `route.hash-query-parameter` becomes a required string property, and `form.fields` controls map to JSON Schema properties (`slider` to a bounded number, `checkbox` to a boolean, `radio` and `select` to an enum, `text` to a string). A page is agent facing when it appears in the declared navigation or is addressed by one route parameter.
- `src/webmcp/runtime.js` registers those descriptors with `document.modelContext` and executes them. Execution validates the declared arguments, navigates the dashboard to the page route, and reads the page projection through the same page source loader and data-worker boundary the rendered page uses. Registration passes an `AbortSignal` so tools unregister by aborting it, and the execute callback forwards the agent's cancellation signal to the query.

WebMCP is progressive enhancement. The runtime feature-detects `document.modelContext.registerTool` instead of sniffing user agents, ships no polyfill, and registers nothing when the API is absent, disabled, or its origin trial has expired; the dashboard then behaves exactly as before.

### Safety posture

- Tools are read-only. They annotate `readOnlyHint`, and the only effects an invocation has are a same-document hash navigation and one page projection read.
- Tool results annotate `untrustedContentHint` and are prefixed with the same untrusted-context framing the rendered dashboard applies before handing row JSON to an agent (DLS-SAFE-015). Dashboard rows carry text ingested from GitHub and from agentic workflow runs, so an agent must treat a result as data and never as instructions.
- Result rows are reduced to scalars and to link `href` values that clear the same `isSafeHttpsUrl` bar the renderer applies before a URL reaches the DOM, so a poisoned record cannot hand an agent a `data:`, plaintext, or credential-bearing URL that the rendered page would have dropped.
- Arguments are validated against the generated schema before any query runs: undeclared names, wrong types, out-of-range numbers, non-member enum values, and missing required values all fail early. Arguments become declarative predicate operands, never query structure, and reach the DOM only through `textContent`.
- Routes are always `#page-<encoded page id>` with `URLSearchParams`-encoded parameters, so an agent controls neither the scheme nor the origin.
- The catalog is bounded by the declared page definitions. Every registered tool consumes agent context, so keep new agent-facing pages deliberate.

### Trying it out

WebMCP is experimental and only reachable from a secure context (`https:` or `localhost`).

1. Use Chromium 146 or newer and enable `chrome://flags/#enable-webmcp-testing` (`edge://flags/#enable-webmcp-testing` on Edge), then restart the browser.
2. From the repository root, acquire a real operational snapshot and run `npm run dashboard:local -- --sqlite PATH` to build and serve through Go/SQLite (relative Go paths resolve from `server/`). To exercise the synthetic Pages demo and browser IndexedDB, run `npm run dashboard:pages:build` and then `npm run dashboard:pages -- --base-path /gh-cao/`. There is no real-data demo input option. The Node canvas authoring bridge is internal to the Copilot extension, not a standalone server.
3. Open the dashboard and confirm the API is present in DevTools:

   ```js
   await document.modelContext.getTools();
   ```

4. Run a tool the same way an in-page agent would:

   ```js
   const tools = await document.modelContext.getTools();
   const cost = tools.find((tool) => tool.name === 'cao_cost');
   console.log((await document.modelContext.executeTool(cost, {})).content[0].text);
   ```

   Parameterized pages take their route parameter as an argument, for example `executeTool(campaign, { campaign: 'self-care' })`.

Nothing registers when the flag is off; `document.modelContext` is simply `undefined` and the dashboard renders normally.

## Quality gates

```bash
npm install
npm run build
npm run typecheck
npm run lint
npm test
npm run test:e2e
npm run test:performance
```

The performance suite audits CFO, CTO, and CSO dashboard journeys with Lighthouse. It writes machine-readable reports, browser traces, and a summary under `test-results/lighthouse/`; CI retains that directory as the `dashboard-lighthouse-performance` artifact.

The production build bundles and minifies the application with esbuild and publishes external source maps alongside the JavaScript bundles.