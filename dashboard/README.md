# Central Agentic Ops Dashboard

> [!NOTE]
> **Research prototype:** Features and interfaces may change as the project evolves.

The shared dashboard runtime consumes canonical operational evidence. Production serves the dashboard through same-origin Go/PostgreSQL;
local development uses the same Go server and views with PostgreSQL.

> [!NOTE]
> GitHub Pages is only the **Synthetic enterprise demo**. It uses generated raw
> samples through production normalization, canonical ingestion, queries, and
> shared views, with no live services or operational-data input.

> [!CAUTION]
> Operational artifacts may contain non-public evidence. Keep them bounded and
> access-controlled; never copy them into Pages output. Changing source does not
> remove an older deployed site or historical/browser copies.

## Contents

- `.github/workflows/shared/policy.mjs`: dependency-free checked-in policy parser and resolver.
- `.github/workflows/shared/control.mjs`: deterministic policy command adapter used by the build workflow.
- `dashboard/report`: deterministic collection modules executed by the activity action plus Dashboard Language source adaptation.
- `dashboard/site`: the bundled Dashboard Language validator, presenter, configuration, and browser runtime.
- `server/`: unified Go/PostgreSQL serving locally and hosted, plus synthetic-only Pages preview.

The legacy Actions collector and dashboard artifact publisher are removed.
Hosted collection uses reviewed Go adapters. Local tools consume explicitly
prepared, checksum-verified snapshots, with no latest-artifact discovery,
empty bootstrap, or legacy dashboard-bundle fallback. Missing evidence remains
unavailable; Pages is never an operational input.

The **GitHub API** view reads the activity snapshot's `cao-gh.jsonl` ledger. It charts rate-limit capacity and lists the before/after credential class and aggregate cache-hydration state for each instrumented collection operation.

If authoritative control policy resolution fails, the build remains fail-closed to the control repository and publishes the resolver diagnostic on the dashboard's Coverage diagnostics page. Valid policy that omits or disables an installed campaign or worker is shown as an admission gate in Overview attention and Security & controls. A latest failed run blocked by pre-activation GitHub REST API capacity is shown separately with its reset time, wait estimate, and official GitHub rate-limit guidance.

## Install

The root Central Agentic Ops package installs validation and the shared runtime. Install the
extension with GitHub CLI, then let guided setup prepare the runtime from the
exact revision recorded by gh-aw:

```bash
gh extension install githubnext/gh-cao
gh cao setup
```

The retired Activity and Dashboard component manifests are not installed.
`gh cao setup` configures scope and authentication, not Pages, and asks for no
public-publication consent.

> [!TIP]
> **SAML-token installation failure:** If GitHub CLI cannot download the extension
> because of cross-organization SAML authorization, use
> `curl -fsSL https://raw.githubusercontent.com/githubnext/gh-cao/main/install-gh-cao.sh | bash`.
> The fallback requires Bash, curl, and Node.js 24+; public tool downloads are
> anonymous without changing stored credentials.

To refresh the extension and the installed root runtime:

```bash
gh extension upgrade gh-cao
gh cao update --campaign root
```

Use `gh cao update` to move installed campaigns to a newer reviewed
release. It runs gh-aw updates and rematerializes canonical runtime files from
the immutable resolved commit recorded by gh-aw.

## Local preview

From a catalog checkout with Go and npm dependencies installed, build and serve
the dashboard through the Go server:

```bash
npm ci
npm --prefix dashboard/site ci
gh run download RUN_ID --repo OWNER/CONTROL --name cao-activity-index --dir .cao
npm run dashboard:local -- --source ../.cao --postgres-url "$CAO_POSTGRES_URL"
```

`npm run dev` is the same entrypoint. The build composes the campaign documents
and fragments. Go `serve` ingests verified canonical JSONL into the selected,
provisioned PostgreSQL namespace. Explicit artifact acquisition can use
`DASHBOARD_DATA_URL`; there is no default Pages source or database-file import.
URLs must end in `/payload-hashes.json`, not a site root. Synthetic manifests,
missing canonical checksums and invalid inputs fail closed.
Use `--listen 127.0.0.1:8443`, `--postgres-url URL`, or `--site PATH` to override the
listener, connection, or built site. Paths resolve from the `server/` module.
For managed local setup, use `gh cao dashboard`: Docker then Podman is tried
(use `--container-engine` or `CAO_CONTAINER_ENGINE` to select one exclusively),
PostgreSQL is started and provisioned, and data is bind-mounted under the ignored
`.github/cao/dashboard/<scope>/postgres/` cache. Explicit connections bypass setup.
Open the access URL printed by Go. Artifact acquisition is separate from
read-only serving; there is no standalone Node preview, admin flag, automatic
process replacement, or Node Go launcher.

### Synthetic Pages demo preview

To exercise the synthetic enterprise demo and browser IndexedDB, not the hosted
HTTP query backend:

```bash
npm run dashboard:pages:build
npm run dashboard:pages -- --base-path /gh-cao/
```

Preparation generates synthetic enterprise raw samples through production
Activity normalization, canonical ingestion, hashes, and Dashboard build steps.
There is no real-data input option. Published output contains no SQLite snapshot
or agent summary. It does not contact GitHub or simulate Actions permissions,
caches/artifact transport, or Pages infrastructure.
The existing Actions runtime simulator supports compiled dry-run agentic locks,
not the ordinary Activity/Dashboard workflows' cache and artifact transport.

The output defaults to `.tmp/dashboard-pages`; only owned simulator output may
be replaced. Go `serve-pages` serves those bytes unchanged on loopback, including
repository base paths, with no hosted bootstrap, bearer authentication, query
API, or fallback. Use `--site ../.tmp/OTHER` to select other prepared output.

The declarative **Synthetic enterprise demo** callout identifies the sample.
Data URLs live under `demo-data/`; browser storage keys use
`synthetic-demo` and service-worker caches use `demo`. Old same-origin
real-data responses cannot be selected through the changed data URLs.
IndexedDB, Web Worker, service-worker caching, and cross-adapter conformance
cost remain despite removing live publication and implicit acquisition paths.

### Hosted serving and local maintenance

Hosted deployments invoke `go -C server run ./cmd/cao-dashboard serve-hosted`.
PostgreSQL owns canonical entities and query execution; OAuth, authorization,
and HTTPS/proxy protections remain required. See [`../server/README.md`](../server/README.md).

The plugin registers no canvas. Run `gh cao dashboard` in an interactive terminal
and open its private loopback URL in a browser. The native CLI owns acquisition,
managed PostgreSQL, cached evidence, and terminal-approved maintenance. There is
no Node authoring server, preview reload socket, staged site, or execution adapter.

### CLI command queue

Local maintenance uses the Go server's durable PostgreSQL command queue. It
admits only supported dashboard-declared update/upgrade actions for explicitly
selected repositories, requires pull-request publication, and obtains fresh
approval in the owning native CLI terminal before each execution. Missing
terminal access fails closed. Compile, workflow dispatch, agent tasks, repository
enrollment, and compiler-default administration are not enabled by local access.

Commands continue independently of browser connections while the native CLI runs.
The server owns queue history, ordered output replay, explicit cancellation, and
manual retries. Cancellation is not proof of subprocess exit: execution remains
claimed until the native adapter confirms settlement. Hosted CLI execution is
separately enabled and authorized; see [`../specs/server-hosted-cli.md`](../specs/server-hosted-cli.md).

Catalog contributors can run `npm run dashboard:local`; the build composes
top-level campaign documents. Select a cached operational snapshot or set
`DASHBOARD_DATA_URL` to its direct trusted manifest URL. Use the synthetic demo
above for IndexedDB.

To measure the dashboard's mobile DOM budget with an explicitly selected
operational snapshot, run:

```bash
npm run dashboard:local:mobile
```

This starts the local preview through Playwright with the Pixel 7 profile and a 256 MiB V8 heap limit, then records DOM analysis, accessibility, trace, and screenshot evidence under `test-results/chromium-low-memory/`. The check fails when the rendered page exceeds 6,000 elements.

## Pages publication and migration

The catalog's `docs.yml` rebuilds documentation and the synthetic enterprise
demo daily, independently of operational collection or Dashboard artifact
freshness. Operational snapshots are explicit private inputs, not Pages artifacts. Bounded operational artifacts remain necessary for authorized
local analysis and hosted ingestion.

An upgrade alone does not scrub an old Pages deployment or browser cache.
Rebuild and redeploy the demo to replace current site artifacts, or unpublish
the old site. Review historical artifacts and downloaded copies separately.
Old real-data browser databases are unused disposable state, not automatically
deleted: the demo only owns its scoped namespace. Explicitly clear affected
legacy site data when required.

## Configure

Set `control-plane.scope.allowed-repositories` in `.github/workflows/cao.json`
when collection should be limited to an explicit repository allowlist. The
former `control-plane.campaigns.dashboard.deploy` field is removed; it is not
a hosting selector. See [Actions artifacts and the Pages demo](../docs/deployment-actions.md)
for the acquisition and publication boundaries.
