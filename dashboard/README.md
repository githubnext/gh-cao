# Central Agentic Ops Dashboard

> [!NOTE]
> **Research prototype:** Features and interfaces may change as the project evolves.

The dashboard campaign builds bounded operational artifacts from control-plane
evidence. Production serves the dashboard through same-origin Go/PostgreSQL;
local development uses the same Go server and views with a real SQLite snapshot.

> [!NOTE]
> GitHub Pages is only the **Synthetic enterprise demo**. It uses generated raw
> samples through production normalization, canonical ingestion, queries, and
> shared views, with no live services or operational-data input.

> [!CAUTION]
> Operational artifacts may contain non-public evidence. Keep them bounded and
> access-controlled; never copy them into Pages output. Changing source does not
> remove an older deployed site or historical/browser copies.

## Contents

- `.github/workflows/cao-dashboard.yml`: operational dashboard build and artifact publication, not Pages deployment.
- `.github/workflows/cao-activity.yml`: shared data collector and cache publisher installed by the core activity campaign.
- `.github/workflows/shared/policy.mjs`: dependency-free checked-in policy parser and resolver.
- `.github/workflows/shared/control.mjs`: deterministic policy command adapter used by the build workflow.
- `dashboard/report`: deterministic collection modules executed by the activity action plus Dashboard Language source adaptation.
- `dashboard/site`: the bundled Dashboard Language validator, presenter, configuration, and browser runtime.
- `server/`: unified Go serving for real local SQLite, hosted PostgreSQL, and synthetic-only Pages preview.
- `dashboard/canvas-server.mjs`: internal loopback-only Copilot canvas authoring bridge, with JSON live reload; not a standalone server.

The activity action reads trusted workflow data and writes a bounded JSONL and SQLite cache snapshot plus a deterministic `inventory-sources.json` sidecar from the reviewed control policy, installed campaign ownership records, local control workflow inventory, and paginated Actions workflow registries for resolved repositories. Campaign default-branch revisions, the latest stable gh-aw release, and generated lock compiler metadata are collected as independent version evidence. Registry and version lookup failures remain explicit partial evidence, and repository-owned workflows remain standalone rather than becoming campaign workers or rollout authority. An Activity-triggered dashboard build restores the exact triggering run and attempt's cache, or downloads that run's `cao-activity-index` artifact on a cache miss. It never substitutes another run's snapshot or an empty bootstrap. Manual builds can reuse the latest cached snapshot, falling back to a successful default-branch Activity run with a successful `index` job. Canonical ingestion includes inventory so configured campaigns, including campaigns without recent runs, are available to Dashboard Language queries. Operational artifacts are not public demo inputs. AI agents do not receive `pages: write`, `id-token: write`, or deployment authority.

The **GitHub API** view reads the activity snapshot's `cao-gh.jsonl` ledger. It charts rate-limit capacity and lists the before/after credential class and aggregate cache-hydration state for each instrumented collection operation.

If authoritative control policy resolution fails, the build remains fail-closed to the control repository and publishes the resolver diagnostic on the dashboard's Coverage diagnostics page. Valid policy that omits or disables an installed campaign or worker is shown as an admission gate in Overview attention and Security & controls. A latest failed run blocked by pre-activation GitHub REST API capacity is shown separately with its reset time, wait estimate, and official GitHub rate-limit guidance.

## Install

The root Central Agentic Ops campaign installs Activity, Dashboard, the trusted
materializer, and the local runtime verification action together. Install the
extension with GitHub CLI, then let guided setup prepare the runtime from the
exact revision recorded by gh-aw:

```bash
gh extension install githubnext/gh-cao
gh cao setup
```

Direct `gh aw add` of the component `activity/` or `dashboard/` manifests is not
a complete installation: gh-aw cannot run the required post-install materialization step
for component manifests, and those manifests intentionally contain no duplicate
runtime resources. The root installation adds the deterministic dashboard
automation without an additional enable variable. `gh cao setup` configures
scope and authentication, not Pages, and asks for no public-publication consent.

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
npm run dashboard:local -- --sqlite ../.cao/gh-aw-logs.sqlite
```

`npm run dev` is the same entrypoint. The build composes the campaign documents
and fragments, and Go `serve` uses a verified canonical SQLite snapshot cached
under `server/` by default, or at the explicit path above. Existing caches work
offline. A missing cache requires an explicit operational manifest URL through
`DASHBOARD_DATA_URL` or `--download-sqlite=URL`; there is no default Pages source.
URLs must end in `/payload-hashes.json`, not a site root. Synthetic manifests,
missing SQLite checksums, and invalid existing caches fail closed.
Use `--listen 127.0.0.1:8443`, `--sqlite PATH`, or `--site PATH` to override the
listener, cache, or built site. Paths resolve from the `server/` module.
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

### Hosted serving and canvas authoring

Hosted deployments invoke `go -C server run ./cmd/cao-dashboard serve-hosted`.
PostgreSQL owns canonical entities and query execution; reviewed policy selects
the operational adapter, with PostgreSQL the default and Redis or acknowledged
volatile memory explicit alternatives. OAuth, authorization and HTTPS/proxy
protections remain required. See [`../server/README.md`](../server/README.md).

The Copilot canvas launches the same Go server against the authenticated GitHub
CLI download's checksum-verified `cao-activity-index` SQLite snapshot from a
successful default-branch Activity run. Go owns its public
loopback origin, static assets, query/diagnostics/memory APIs and revision events.
It proxies only Copilot integration routes to a private Node adapter, which
composes and watches JSON documents/fragments, reloads valid edits over a
capability-protected WebSocket and runs trusted authoring/command callbacks.
Invalid JSON is reported while the last valid canvas remains available.
There is no Node data server, logical-source HTTP transport or browser ingestion
fallback. Published inventory quality is read as metadata only; absent quality
remains unknown. Snapshot compiler evidence is not mutated by live GitHub reads.
The plugin includes its Go module and native query sources; install compatible
Go or select an absolute `CAO_DASHBOARD_EXECUTABLE`. It never builds workspace
executable code. The owning adapter manages the child's stdin lifetime and
removes staged files only after stopping it, including failed startup.

### Canvas CLI actions

Dashboard documents may declare trusted canvas CLI actions.
They are hidden from the published site and ordinary local Go serving. In the
dashboard canvas, each
action appears in the **Actions** menu for a command preview. Native execution
also requires independent confirmation in the trusted Copilot host, bound to
the exact resolved command, working directory, and standard input. A page POST
alone cannot authorize execution. Approval authorizes only the exact queued invocation; hosts without interactive
elicitation fail closed. Approved commands run in the server-owned queue, not
the browser request. Closing a terminal or browser detaches its output stream
without canceling approved work. Pending approvals do not authorize execution.

### CLI command queue

The built-in campaign update and repository upgrade actions include a quoted
`--pr-message` (gh-aw v0.92.4 or later). Generated pull requests explain that they
were requested through CAO, identify the affected campaign or repository and the
reason for maintenance, and link back to the CAO project and dashboard. The
message is part of the declarative command shown for approval; existing
pull-request creation and auto-merge choices are unchanged. Backlinks use the
actual control repository and explicitly supplied `caoUrl` or
`CAO_DASHBOARD_URL`, not discovered Pages settings or the catalog demo.

Command text supports literal `{{field}}` substitutions for validated row
values. Three reserved placeholders come from the host, never browser-supplied
row values: `{{cao-url}}` is the public dashboard base URL (with a trailing
slash), `{{cao-repository}}` is its control repository slug, and
`{{cao-repository-url}}` is that repository's GitHub URL. For example:

```text
gh aw upgrade --repo {{repository}} --pr-message 'Requested through {{cao-repository}}. See {{cao-url}}#page-maintenance.'
```

Supply `caoUrl` explicitly to the canvas or set
`CAO_DASHBOARD_URL=https://ops.example.com/central/`; there is no GitHub Pages
API discovery or catalog-demo fallback. URLs must use
HTTPS and contain no credentials, query, fragment, whitespace, or command
delimiters. Hosted dashboards use their own page base URL. Local preview
capability URLs are never copied into PRs. Missing host context disables the
action with an explicit message; queue admission rejects missing or overridden
host context. Substitutions are literal, not expressions, and never run a shell.

Trusted canvas sessions share one durable FIFO queue per
checkout, stored in `.cao/cli-actions.sqlite`. The online dashboard and ordinary
read-only previews do not initialize a queue or request its APIs. Execution
continues while the local server is running, even with every browser closed.
Stopping the server terminates running work; interrupted executions are recorded
as failed rather than silently repeated, and queued commands survive restart.
After an unclean crash, termination of detached subprocesses cannot be assumed:
the queue preserves history but pauses execution across subsequent restarts.
Verify that every interrupted subprocess has stopped before explicitly starting
with the canvas host's `cliQueueAcknowledgeRecovery` option to resume queued work. Interrupted entries
remain failed and are never automatically retried. This acknowledgement is a
local operator action, not a browser API.

**Maintenance → Commands** lists queued, running, completed, failed, canceled,
and timed-out commands. Attach by command ID to replay retained stdout/stderr
and follow new output. Cancel explicitly to terminate work; Retry creates a new
entry linked to a failed or timed-out command.

The command service runs displayed `gh aw` and `gh cao` commands in a
pseudo-terminal through `node-pty`, capturing colors even when gh-aw requires
TTY detection. Install its native runtime with `npm ci` in `dashboard/site`.
The pseudo-terminal combines stdout and stderr in terminal order. Authentication,
JSON preparation, and stdin-backed commands retain separate pipes. Color settings
apply only to displayed commands, not authentication or JSON preparation reads.
The browser terminal preserves standard, bright, 256-color, and true-color
sequences across chunks and retained replay on both stdout and stderr; stderr
is not automatically recolored because it also carries progress and status.
Copying a transcript removes ANSI formatting without changing the terminal or
retained output; copying a pull-request result still copies only its URL.
GitHub CLI prompts and pagers are disabled. These terminal settings do not
change the parent shell environment.

The queue defaults to four concurrent executions (configure with
the canvas host's `cliQueueConcurrency` option, from 1 through 32) and a 15-minute execution deadline, beginning when
a command starts. GitHub-dependent work uses the existing
Go GitHub quota service through a local helper, refreshing quota with the same
GitHub CLI authentication context as execution. Missing quota evidence pauses
starts instead of bypassing protection. Running commands are not terminated
merely because quota falls. A source checkout can run the helper with Go.
Installed runtimes require a prebuilt `cao-cli-quota` on `PATH` or selected by
`CAO_CLI_QUOTA_EXECUTABLE`; without it GitHub-dependent work remains paused.

The capability-protected local API exposes:

- `POST __cli_commands`: enqueue a declared action; an `Idempotency-Key` header
  or `idempotencyKey` field deduplicates repeated requests.
- `GET __cli_commands`: list commands with `status`, `limit`, and `offset`.
- `GET __cli_commands/:id`: retrieve persistent execution details.
- `GET __cli_commands/:id/output?after=N`: replay and stream ordered output
  after sequence `N` as newline-delimited JSON.
- `POST __cli_commands/:id/cancel` and `POST __cli_commands/:id/retry`: explicit
  lifecycle operations; retries revalidate the current action and approval.

The `status` filter accepts one status or a comma-separated set; filtering and
the matching total count are applied before pagination.
Mutations require same-origin JSON requests as well as the preview capability.
Queue retention defaults to 30 days and removes terminal history only in wholly
expired UTC Monday-based seven-day windows, including while idle; active commands never expire. Queue
files contain operational command history and must remain local and uncommitted.
Storage is bounded to 10,000 retained entries, 1 MiB output per command, 64 MiB
retained output overall and a 128 MiB main SQLite database (excluding WAL
overhead). Output is framed by line for cross-chunk credential redaction;
overlong lines above 16 KiB and output beyond the budgets are discarded with an
explicit truncation warning. Terminal output can therefore lag until a line
ends or execution finishes.

```yaml
dashboard:
  cli-actions:
    - id: upgrade-repository
      label: Upgrade
      description: Upgrade the repository's Agentic Workflows files.
      icon: download
      command: gh aw upgrade --repo {{repository}}
      placement: settings
      arguments:
        - id: create-pull-request
          label: Create pull request
          description: Create a pull request with the generated workflow upgrades.
          type: boolean
          flag: --create-pull-request
          default: true
        - id: pre-releases
          label: Include pre-releases
          type: boolean
          flag: --pre-releases
          default: false
```

Commands must be single-line `gh aw ...` or `gh workflow run <workflow> ...`
invocations; the latter triggers workflows that declare `workflow_dispatch`.
Workflow dispatch actions may set `--repo`, `--ref`, and non-file-backed inputs
with `--raw-field` (`-f`). File-reading forms such as `--field` (`-F`) are
rejected. The canvas extension runs GitHub CLI directly without a shell and
supplies the user-approved `GITHUB_TOKEN` as `GH_TOKEN` to that process. For
`gh aw` commands, it also derives an ephemeral
author and committer identity from the currently authenticated GitHub CLI user
so pull-request actions can create commits without changing global or
repository Git configuration. When `gh aw` is unavailable, an approved action first attempts
`gh extension install github/gh-aw`. If that installation fails, it downloads
and runs the installer version declared by `gh-aw-version` in
`.github/workflows/cao.json` from the official `github/gh-aw`
repository, then verifies `gh aw` before continuing. Boolean action arguments
render as checkboxes and may append only their declared
canonical long option to the command preview and executed argv.

The canvas accepts prompts up to 100,000 UTF-16 code units and bounds each JSON
request to 1 MiB, including JSON escaping and UTF-8 overhead. Oversized requests
are rejected before approval or execution.

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
freshness. `cao-dashboard.yml` retains operational artifacts and never deploys
them to Pages. Bounded operational artifacts remain necessary for authorized
local analysis, canvas use, and hosted ingestion.

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
