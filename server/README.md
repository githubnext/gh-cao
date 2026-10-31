# Go dashboard server with Postgres, local SQLite, and operational storage

The [TypeSpec HTTP contract](spec/README.md) is the source for generated
OpenAPI 3.1 and selected JSON Schemas; consult it before changing server routes.

The `server/` module is the unified serving runtime for the Central Agentic Ops
dashboard with server-owned persistence and query execution. It ingests the
same compacted data published with the deployed dashboard, stores current
native entity rows in PostgreSQL and executes hosted Dashboard Language plans
as SQL through the bounded PostgreSQL query engine,
and serves the built dashboard either over loopback HTTP or through an
authenticated host-neutral service profile. Local `serve` defaults to a cached
SQLite snapshot, downloading deployed data only when the file is missing.
PostgreSQL is the hosted default operational adapter, so the default deployment
requires no Redis service.
Explicit Redis configuration remains supported; an acknowledged single-process memory adapter supports the
same OAuth, signed-webhook, collection, quota, and backfill services with volatile
state. The PostgreSQL operational adapter supports durable,
deployment-scoped services in separate tables. No operational adapter holds or
queries dashboard entities.

Operational composition uses only the focused `OperationalServices` bundle.
The provider owns lifecycle through `Backend`; consumers retain just the cache,
limiter, session, invalidation, revocation, lease, state, delivery, queue,
admission, metadata, quota, health, or metrics contracts they use. The old
`Services()` bundle and aggregate operational interfaces are removed.
Separately injected services preserve the same backend's atomic transitions;
the split is not permission to put session invalidation or delivery admission
across unrelated stores. All three adapters share behavioral conformance over
the focused contracts.

The browser never connects directly to Postgres or Redis and never receives
database credentials. It communicates only with the same-origin HTTP(S) API.

### Local development and Pages simulation

From the repository root, `npm run dev` or `npm run dashboard:local` builds
the dashboard and starts Go `serve` with its verified SQLite cache. The Node
server is retired; only an internal loopback canvas authoring bridge remains.

The Go local SQLite server also runs natively on Windows, without PostgreSQL,
Redis, Docker, or Linux process features. From PowerShell at the repository root,
build the dashboard with `npm --prefix dashboard/site run build -- dist`, then
run `go -C server run ./cmd/cao-dashboard serve`. The POSIX environment-assignment
syntax in the root npm launch commands is not supported by PowerShell's default
npm shell; use the direct Go command there. Windows CI builds the native command
and exercises snapshot download, offline startup, HTTP queries, and shutdown with
local fixtures. Evidence-lake writers retain cross-process exclusion using
Windows file locks instead of Unix `flock`; locking errors still fail closed.
Local SQLite URLs preserve drive-letter paths and escaped spaces or `#` in names.

Local serving resolves Marketplace through the same read-only registry resolver
as hosted serving, even when the cached SQLite snapshot has no packages.
The npm launch commands select this checkout's `.github/workflows/cao.json`;
set `CAO_MARKETPLACE_POLICY_PATH` to an absolute path to use another policy.
Provide the credentials named by its registries (for example, `GH_TOKEN`).
Registry results use the process-local operational cache and never modify the
SQLite snapshot. Registry failures remain explicit in source availability or
registry diagnostics; they never trigger synthetic packages or snapshot-inventory
fallback.

For explicit Pages/IndexedDB testing:

```sh
npm run dashboard:pages:build -- --fixture
npm run dashboard:pages -- --base-path /gh-cao/
npm run test:e2e:dashboard-pages
```

Use `--activity-dir PATH` instead of `--fixture` to prepare an existing Activity
snapshot offline. Preparation runs production normalization, SQLite ingestion,
hashing and Dashboard building; fixture data is explicitly synthetic. It does
not simulate collection, Actions permissions, cache/artifact transport, TLS or
GitHub Pages infrastructure. The current Actions runtime simulator accepts
compiled dry-run agentic locks, not the ordinary Activity/Dashboard workflows.

`serve-pages --site ../.tmp/dashboard-pages --listen 127.0.0.1:8080`
serves the prepared artifact unchanged, optionally below `--base-path /REPO/`.
It provides no hosted bootstrap, authentication or query API, so the page uses
its real browser IndexedDB data path. The listener is numeric-loopback-only,
and directory listings and escaping symlinks are refused. Paths in npm Go
commands resolve relative to `server/`. Static Pages is an explicit mode,
never a fallback for hosted serving.

### Daily agentic maintainer journeys

The repository-private `dashboard-maintainer-simulation` agentic workflow runs
daily and supports manual dispatch. It generates ten maintainer scenarios before
testing, attempts them through Chromium on the real local Go/SQLite dashboard,
and preserves the frozen plan, all ten outcomes, screenshots and logs in a
14-day artifact. It files at most three deduplicated issues with reproduction
steps and potential fixes; blocked scenarios remain incomplete, not passed.
No campaign installation, rollout-policy change, live repository mutation, or
production dashboard access is involved.

The harness reuses `simulate-api` to obtain 36 successful historical runs for
three fictional repositories, adds explicitly synthetic cost/tool metadata,
and uses production normalization and SQLite ingestion. It does not exercise
hosted OAuth/PostgreSQL, live collection, dispatch or Actions execution, and
the snapshot does not subscribe to subsequent simulator changes. Missing
fixture data or hosted-only capabilities are coverage limits, not defects.

To prepare and start the same environment locally after installing the root and
dashboard dependencies:

```sh
mkdir -p .tmp/maintainer-simulation
go -C server build -o "$PWD/.tmp/maintainer-simulation/cao-dashboard" ./cmd/cao-dashboard
node scripts/maintainer-simulation.mjs prepare
node scripts/maintainer-simulation.mjs serve
```

Read `.tmp/maintainer-simulation/environment.json` for the browser URL and
fixture limits. The two servers bind only to `127.0.0.1` (ports 18443 and 18081);
Ctrl-C stops both. Preparation reuses the Pages artifact builder only to
produce the snapshot/assets; the dashboard is served with the `server-http`
bootstrap and Go query API, not as a Pages/IndexedDB application.

## Local GitHub bootstrap integration

Run `npm run test:integration:bootstrap` from the repository root with Go,
Node.js 24 or newer, Git, Bash, curl, GitHub CLI, and the root manifest's
minimum gh-aw version installed. Set `CAO_BOOTSTRAP_GH_AW_BINARY` if gh-aw is
not in the standard GitHub CLI extension directory.
The `cao-bootstrap.yml` workflow runs this test daily, on relevant pull
requests, and manually, retaining the test log for 14 days.

The test builds the current CAO CLI as a local release fixture, then runs the
unmodified `install-gh-cao.sh --create-repo` and `install-gh-cao.sh --setup`, real `gh`
commands, and HTTPS Git push/clone against the simulator's test-only
`BootstrapAPI` fixture. These bootstrap endpoints and their GraphQL selection
reader are not compiled into production binaries. The fixture's
stateful surface implements repository creation and visibility, the GraphQL
selections used by gh, release metadata and checksum-verified asset downloads,
workflow-sourced Pages, and Actions variables. Unknown routes and fields fail
explicitly. The root campaign is installed and materialized from the current
checkout, not a published CAO revision.

An allowlisted CONNECT proxy maps the installer's fixed `github.com` and
`api.github.com` URLs to one loopback simulator with an ephemeral trusted
certificate. It never forwards to GitHub. A local Git HTTP backend receives
the committed files, and a fresh clone must contain the exact verified policy.
The test checks that private non-interactive setup fails without changing
policy, repeated public monitoring setup is idempotent, and no campaign or
agent operation is enabled. Home, Git configuration, CLI extensions, and
credentials are isolated; only a synthetic token is used in the test.
The shared bootstrap/setup environment explicitly disables CLI telemetry with
`DO_NOT_TRACK=1` and `GH_TELEMETRY=0`; the proxy still rejects every destination
outside its allowlist.
The workflow and every job grant only `contents: read`. The integration step
explicitly clears real GitHub token environment variables; the read-only
workflow token is used only to download the gh-aw prerequisite, never for
repository creation, configuration, or Git publication.

This covers control-repository bootstrap and publication, not execution of
GitHub Actions jobs, rendering of a deployed Pages site, GitHub App consent,
or Azure infrastructure provisioning. The fast bootstrap API regression tests
also run with the ordinary simulator unit suite; the external-tool integration
is opt-in outside its dedicated npm command and CI job.

## Local GitHub setup topology integration

`npm run test:integration:setup` builds the current native extension and runs
real `gh cao setup`/GitHub CLI processes against the same strict loopback
`BootstrapAPI`. Interactive cases reuse `tests/helpers/terminal.mjs` (Linux
`script` or macOS `python3`); Node.js is a test-driver dependency, not a native
setup dependency. The Go Extension CI setup job runs this full suite only on
path-matched pushes to `main`, not pull requests. PRs retain the extension quality,
unit, build, smoke, and authenticated doctor checks.

The topology matrix covers public/private GitHub.com controls and
private/internal GHE.com controls, public/private/internal/mixed targets where
available on that host, and same/cross/mixed resource owners. Public controls
reject non-public evidence. PAT cases retain simulated existing secret names
and check exact owner-scoped read maps and control-only writes. App previews
cover both hosts, separate permissions, reuse plans, and cross-owner rejection
for newly created organization Apps. Capability failures exercise denied Pages,
secret lookup and variable writes, declined public Pages, and unchanged consumer
files/index. The recorded public monitoring flow and a private GHE.com PAT flow
also publish reviewed files through real HTTPS Git to local bare repositories.
GHE.com requests must remain on its selected web/API hosts.

This is command/protocol end-to-end evidence, not live GitHub role, credential,
Actions execution, or deployed Pages validation. Public repositories do not
exist on GHE.com; no public GHE.com scenario is claimed. The simulator does not
issue or validate real PATs, approve Apps, or prove restricted Pages entitlement.
See [setup combinations and live gaps](../docs/setup-compatibility.md).

## Local Azure setup integration

Run `npm run test:integration:setup-azure` with Go 1.27.3 or newer on PATH, Git, GitHub CLI, the
policy-pinned gh-aw prerequisite, and Docker installed. The Go Extension CI
setup job runs this command alongside the GitHub-only setup suite on path-matched
pushes to `main`, not pull requests. It uses the
same digest-pinned official Azurite image as `scripts/azure-local/azure-local.sh`,
with a disposable account, loopback-only dynamic port, and automatic cleanup.
Prerequisites must already be local: run `go mod download`,
`go -C server mod download`, and pull the exact Azurite image declared in
`scripts/azure-local/azure-local.sh` before entering the simulation. CI does this
in a separate prerequisite step. The simulation disables module downloads and
implicit toolchain/image pulls; missing prerequisites fail instead of accessing a registry.
Without Docker, set `CAO_SETUP_AZURITE_BINARY` to an installed official
`azurite-blob` executable; its process and storage are also isolated and removed.
Set `POSTGRES_URL` to a disposable numeric-loopback PostgreSQL instance. The
successful deployment builds `cao-dashboard` from the exact simulated ACR
context, runs real database provisioning and empty-evidence ingestion in an
isolated schema, and starts `cao-dashboard azure-functions` when the Azure
simulator restarts the Function App. The deployment's HTTPS health, readiness,
signed-out API and OAuth checks pass through simulated Azure ingress to that
real process, followed by a verified graceful SIGTERM shutdown. GitHub setup
still uses the GitHub simulator; no live GitHub or Azure credentials are used.

The test builds the current native extension and runs real `gh cao setup`,
then `gh cao deploy azure --preflight`, `--plan`, and `--apply`. Azure deployment
is a separate command, not a `setup azure` subcommand. The existing GitHub
simulator handles setup. A test-only `az` subprocess adapter supplies the
stateful ARM, ACR, Container Apps jobs, Key Vault reference status, and HTTPS
ingress behavior that Azurite does not emulate. Azurite receives actual signed
blob writes and reads for synthetic build manifests and deployment state.
Unknown commands and destinations fail closed; no Azure CLI installation,
cloud login, real credentials, GitHub writes, or Azure subscription is used.
The `az` adapter and an allowlist of local tools are the entire command PATH:
there is no fallback to a real Azure CLI. Git network protocols are disabled.
GitHub and Functions HTTPS names terminate at the loopback CONNECT proxy with
an ephemeral CA; the proxy never forwards to those hosts. Azure command and
storage clients use exact numeric-loopback transports, rejecting redirects,
environment proxies, metadata-service addresses, and nonlocal destinations.
The Azurite container uses a disposable internal-only Docker network and a local
Unix Docker socket; remote Docker contexts and TCP/SSH daemons are refused.
Docker does not publish ports on internal-only networks, so a host-loopback
relay carries blob traffic through `docker exec` to the container's loopback
listener without adding a network with external access or publishing a port.
Regression probes verify live GitHub, ARM, Entra, and metadata destinations are
rejected or handled only by local fixtures, and ambient GitHub/Azure/OIDC
credentials are not inherited.

Assertions cover read-only preflight/planning, plan-drift rejection before
writes, locked per-target digest bindings, provisioning-before-ingestion,
provisioning failure without verification or a receipt, delayed Key Vault
readiness, six ingress/OAuth checks, secret-free persisted receipts, existing
instance/repeated-apply rejection, and unchanged GitHub policy and consumer files.
This is command/protocol coverage: image builds, job execution, PostgreSQL,
managed identities, and hosted OAuth are simulated, not deployed or attested.
The separate Azure-local harness exercises the real Functions host.

## Configurable company ingestion stress

Run `npm run test:stress:company -- --preset large` with disposable local
PostgreSQL to measure ingestion and queries from generated AW evidence, without
running agents. See [Company stress testing](COMPANY-STRESS.md) for configurable
company workloads, reports, and the dashboard calculator.

## Secret-free fleet stress simulation

Run `npm run test:stress:cao` from the repository root. `cao-fleet-stress.yml`
runs daily and manually, not on PRs, with a 60-minute test/65-minute job budget.
Three simulated days start with 1,000 repositories and 5,000 developers producing
2/6/10/14/18 PRs/day (p50 10, exactly 50,000/day); 10% arrive in a daily burst.
At day boundaries, 1% of active repositories are archived/deleted (alternating)
and 2% created, rounding down. Only drained targets retire; no PR is exempt from SLA.

The compiler-manifest vulnerability-review fixture subscribes to
`pull_request: ready_for_review`, exercising production discovery, policy
intersection, signatures, admission, batching, durable SQLite queues, fencing,
retry classification and dispatch. One reviewed policy, registry and queue serve
one control repository and enrolled targets with empty workflow inventories.
Enrollment/route ceilings (2,048), registry size (1 MiB), operational budgets,
API limits, eight-repository discovery rounds and scope remain unchanged.
SQLite batch rotation, capacity, recovery and retention use indexed plans,
avoiding repeated retained-payload parsing. Future IDs are pre-enrolled but
inactive until created. Registry/authority facts are in-memory fixtures,
not PostgreSQL durability or GitHub authentication evidence.

Discovery and reviews share installation REST budgets: 12,500 requests/hour,
900 points/minute (reads 1, writes 5), primary 403/reset and secondary
429/Retry-After. Other abuse, GraphQL and content-generation limits are unmodeled.
Signed lifecycle webhooks invalidate inventory through production discovery hints;
quota exhaustion/bursts exercise throttling and webhook replays exercise deduplication.

Each dispatch batch executes production token-broker `copilot-request` exchange
and invalidation with an immutable control-repository ID, workflow ref/SHA and
distinct fixture job identity. Ephemeral App keys sign issuer requests; an
in-process transport enforces `copilot_requests: read` for that repository only.
Identity verification and issuance/revocation are fixtures, not OIDC, hosted
HTTP/TLS, Key Vault or GitHub authentication evidence; issuer calls do not use
fleet REST quota. Unchanged broker rate/context limits defer throttled exchanges.
Batches mint once, revoke after all reviews, prove idempotent invalidation and
deny reminting. `tokenBroker` report counts must match jobs, exchanges,
invalidations and revocations; full stress must exercise broker throttling.

No agents, vulnerability analysis, inference or real GitHub requests run.
Synthetic reviews complete two simulated minutes after dispatch plus quota/broker
delay; acceptance is not completion. Each ready PR needs exactly one review
within 3,600 simulated seconds from its original ready time, including discovery
and admission retries. Missing, duplicate or late reviews fail.
Manual time controls ingress, discovery, authorization, broker, queue
leases/retention/backoff and API windows without sleeping; I/O deadlines and
cancellation remain wall-clock based.

Network clients use only the exact numeric-loopback endpoint with proxies off;
the broker's in-process transport cannot send network traffic. No secrets are read:
public HMAC fixtures and ephemeral App keys have no external authority.
Installation-token routes accept positive int64 IDs and format tokens from
parsed IDs, never raw paths. `CAO_FLEET_STRESS_REPORT_DIR` retains
`fleet-stress.json`; Actions uploads it and logs, including incomplete failures.
`TestGitHubSimulatorFleetSmoke` runs in ordinary Go tests.

## Optional ADX analytics

The default-off [ADX session analytics add-on](ADX-INGEST.md) exports bounded
snapshots of retained canonical metadata from the existing Go host to an ADX
free cluster. It reuses PostgreSQL and protected operational delivery state,
with no additional collector, paid queue, Blob account or service deployment.
It requires explicit exact repository scope, non-personal/non-regulated data
approval and a single persistent exporter replica. Print its offline,
operator-applied KQL setup with `cao-dashboard adx schema`.

## Hosted activation admission

The optional [cooperative activation-admission service](ADMISSION.md) exposes
the gh-aw OIDC-only `POST /api/admission` and compact-session
`POST /api/conclusion` protocols on that same hosted origin. Completion populates
canonical Run/Audit and available grader data as partial evidence before
acknowledgement, with durable invocation deduplication and retry repair. It neither settles billing nor
automatically fetch full-session artifacts.
It requires explicit enrollment, fresh protected accounting input, and durable
PostgreSQL replay records; it is disabled by default and does not replace CAO
control-policy authorization.
The [operator walkthrough](../docs/admission-gate.md) covers local
`cao-dashboard admission validate`, canary setup, bulk rollout, and recovery.

**Hosted mode serves the dashboard from Go, not from GitHub Pages.** The Go
handler owns the built HTML and assets as well as OAuth, query APIs, and revision
events on the same origin. A reverse proxy or platform listener must forward
the complete application, not just `/api/`. Do not deploy the frontend separately
to Pages, a static website host, or an object-storage website endpoint.
Pages is a different deployment mode whose browser owns ingestion and queries.

Hosted revision streams reconnect before membership authorization, token-refresh
eligibility or administrator health entitlement expires. A 20-second session
check also closes revoked sessions and fails closed on session-store outages.
Reconnection uses ordinary OAuth authentication and can renew cookies; an
already-flushed stream cannot. See [stream authorization bounds](SECURITY.md)
for revocation latency and proxy-delivery limitations. Local bearer streams
remain open until cancellation or server drain.

## Local SQLite snapshot serving

The Copilot canvas uses this same local Go host. Its internal `--canvas-config`
file selects a random capability path, a private numeric-loopback integration
adapter and downloaded read-only memory/inventory-quality evidence. Go owns all
browser assets and canonical APIs; only JSON reload, authoring, approved command
and local-user GitHub routes are forwarded to the adapter. This mode cannot
enable hosted OAuth, PostgreSQL collection or hosted App authority. Missing
inventory quality stays unknown. Inventory rows never replace SQLite query data.
The owner receives a machine-readable readiness URL and keeps stdin open for
the lifetime of the child; EOF shuts the host down. This is an internal extension
contract, not a second dashboard CLI. Canvas startup requires a verified
published SQLite artifact and never falls back to Node queries or browser ingestion.

Build the dashboard once, then run the local server without PostgreSQL, Redis,
`gh`, or a separate download command:

```bash
# From the repository root:
npm ci
npm --prefix dashboard/site ci
npm --prefix dashboard/site run build
go -C server run ./cmd/cao-dashboard serve
```

Plain `serve` reuses `.cao/gh-aw-logs.sqlite` in this checkout without downloading
it again. If that cache is missing, startup downloads the public CAO Pages snapshot,
or `DASHBOARD_DATA_URL` when set. `--sqlite PATH` selects another cache and has the
same download-if-missing behavior. Database service environment variables alone
do not change this local default.

The downloaded snapshot stays **read-only**. SQLite's native `ATTACH DATABASE`
links it to a separate writable delta, defaulting to the snapshot path plus
`.local.sqlite`; `--sqlite-overlay PATH` selects another local file. Only schema
metadata is copied, not the pre-indexed records. Queries read the union, with
local upserts and deletion markers taking precedence over the snapshot.

Local backfill starts in the background immediately and repeats five minutes
after each pass finishes (`--backfill-interval` changes the delay). It uses
`GH_TOKEN`, then `GITHUB_TOKEN`, then `gh auth token --hostname github.com`.
Install Node.js and the `gh aw` extension for collection and canonical ingestion.
The runtime catalog is resolved relative to `--database-queries`; keep the
repository's Activity runtime alongside those query definitions.
Missing tools or credentials and collection/ingestion failures are reported
explicitly without preventing the committed dashboard data from being served.
The token is passed only to the GitHub collection subprocess, never stored in
SQLite or sent to the browser.

Refresh scope is exclusively the repositories already declared in the deployed
snapshot, capped at 10,000 repositories. Credential access never adds targets or
changes rollout policy. Collection uses each repository's latest retained run
with a one-day overlap and includes older unfinished runs, within a bounded
30-day window. Each repository invocation collects at most 10,000 runs with a
five-minute deadline; a pass has a 30-minute deadline. This is recent evidence
refresh, not an exhaustive historical archive or a completeness guarantee.
Successful repository collections use the existing Activity compactor and
canonical ingester against a staged copy of **only the local delta**. Failed
repository collections are discarded and reported; they do not block successful
repositories. Failed ingestion leaves committed data unchanged. Successful
publication is atomic, rejects concurrent stale writers, and notifies active
revision subscribers. The private per-pass evidence directory is removed on
completion or failure. Shutdown cancels collection and ingestion.

Use `--backfill=false` for offline serving without GitHub requests. Existing
local deltas remain visible. The internal Copilot canvas profile continues to
serve its verified snapshot without enabling this backfill.

Use `--download-sqlite` to explicitly refresh the cache, even when it exists.
Its optional URL overrides `DASHBOARD_DATA_URL` and accepts any GitHub Pages site,
including project sites and custom domains:

```bash
go -C server run ./cmd/cao-dashboard serve \
  --download-sqlite https://OWNER.github.io/REPOSITORY/

# A dashboard directory or direct payload-hashes.json URL also works.
go -C server run ./cmd/cao-dashboard serve \
  --download-sqlite=https://dashboard.example.org/cao/ \
  --sqlite ../.cao/custom.sqlite
```

For a site URL, the downloader looks for `payload-hashes.json` in that directory,
then under `cao/` if the first location returns HTTP 404. The image is fetched
beside the discovered manifest; the hostname is not restricted to
`githubnext.github.io`. URLs must use HTTP or HTTPS without embedded credentials
or fragments. HTTPS redirects cannot downgrade to HTTP.

The download streams only the SQLite image, not inventory or JSONL shards.
It verifies the manifest's SHA-256 checksum and canonical database version
before atomically replacing the selected cache. Transient failures and
deployment-time checksum mismatches retry up to three times with fresh manifest
requests; failures leave an existing cache unchanged and do not start a server
against stale data. Requests have a five-minute timeout within a ten-minute
overall deadline; manifests are limited to 8 MiB and images to 2 GiB.
An unbuilt `--site` fails before downloading. Stop any existing snapshot users
before refreshing; symlink destinations and existing SQLite journal sidecars
are rejected rather than risking corruption. Checkpoint a locally modified
snapshot with its owning tool, or select a different `--sqlite` path.
An overlay is bound to the snapshot's canonical content revision. After replacing
the snapshot with different data, choose a new `--sqlite-overlay` file. A
mismatched overlay fails closed and is preserved, rather than applying stale
overrides to a different base or silently deleting local evidence.

For offline startup, use plain `serve` with an existing default cache, or select
another existing snapshot:

```bash
go -C server run ./cmd/cao-dashboard serve \
  --sqlite ../.cao/gh-aw-logs.sqlite \
  --backfill=false \
  --listen 127.0.0.1:8443
```

The built site defaults to `dashboard/site/dist`. Use `--site` for a different
build directory and `--mcp-enabled` to expose the existing read-only MCP endpoint.
Paths in this example resolve from `server/` because of `go -C server`.
`gh cao download` prepares the default CAO snapshot; use `--url` with another
dashboard's `payload-hashes.json` URL to select different data.
The snapshot must use
the canonical IndexedDB-compatible SQLite layout and database version 45.
Only a genuinely missing cache triggers automatic acquisition. Unreadable or
invalid existing files, dangling symlinks, unsupported versions, and missing
requested stores fail closed without a repair download. Startup never creates
an empty snapshot, repairs, migrates, or ingests the downloaded file. Acquisition installs
a verified file before the read-only server opens it; it is not ingestion or a
fallback from a failed database connection.

This is the default **local bearer-only, read-only request** profile.
Only loopback listeners are accepted; HTTP is supported locally, with optional
`--cert` and `--key` for HTTPS. Existing access-token authentication and the HTTP
query, canonical API, diagnostics, revision events, and optional MCP protocols
remain in use. Queries execute in Go against one consistent SQLite read
transaction, using the same bounded local Dashboard Language evaluator as
`gh cao`. Both storage paths share the server's query validation, dependency
traversal, generated runtime-source registry, and source-quality propagation;
only storage reads and query execution differ. The local snapshot profile admits
500,000 input/intermediate rows and 50 million operations per query plan, with
1 GiB per working source and 2 GiB / 2 million rows of simultaneously retained
results. Completed dependency inputs are released after their final consumer.
Shared server dependency traversal is capped at 32 queries deep, accommodating
the shipped routing-attribution plans without changing join, row, byte or
operation budgets.
The planner traces live fields across consumers, prunes unused row-local
computations, and projects only required properties inside SQLite. Requested
raw sources and unknown result shapes keep full documents; original-record byte
limits, JSON types, missing fields, and canonical UTF-16 key order are preserved.
Read-only SQLite memory mapping is capped at 1 GiB to reduce page-read overhead.
These are bounded accounting limits, not an RSS guarantee; large snapshots can
require several GiB of developer-machine memory. Hosted PostgreSQL and CLI
row/memory defaults are unchanged. Wire results remain bounded to 100,000 rows per source;
ordinary continuation pagination remains available. Local query admission has
a 300-unit/minute token bucket so expensive snapshot plans do not exhaust the
hosted 30-unit burst during ordinary page navigation; per-plan cost accounting,
other request limits, and explicit embedding overrides remain enforced. Snapshot
quality without explicit published inventory metadata is reported as unknown
rather than inferred complete or current.
In-place SQLite commits update revisions and active subscriptions without a
server restart. Restart after replacing the snapshot file itself.

After building the site, profile the production Repositories query against an
existing snapshot without modifying it:

```sh
CAO_SQLITE_SNAPSHOT=/absolute/path/to/gh-aw-logs.sqlite \
  go -C server test ./internal/sqlitex -run '^$' \
  -bench '^BenchmarkSnapshotRepositoryActivity$' -benchtime=1x -benchmem
```

Repository queries reduce row-local counts and positive recorded AIC before
repository-grain joins, and carry only run-attempt keys into import matching.
PostgreSQL compiles the declarative plan to SQL; IndexedDB and the Node SQLite
facade use bounded native storage scans and exact streaming reductions where
eligible. The local Go SQLite server pushes field projection into SQL and runs
the remaining bounded declarative operators in its shared Go evaluator.

Operational request services are bounded, process-local, and volatile. Query
result caching is disabled so a changed snapshot cannot serve stale results.
OAuth, collection, ingestion, rebuilds, webhooks, and persisted GitHub-resource
caching are unavailable in this profile. Neither `--sqlite` nor `--download-sqlite` can be combined with
`--postgres-url`, `--redis-url`, or `--source`; database service environment
variables are not used to select its storage. Reviewed hosted operational-store
selection still requires `serve-hosted` and cannot be overridden by local serving.
To select PostgreSQL locally, pass `--postgres-url="$CAO_POSTGRES_URL"`; an
explicit empty `--postgres-url=` reads that environment variable instead.
The existing PostgreSQL-specific `--source` and `--redis-url` flags also select
the PostgreSQL profile and require its configured database. Hosted deployments
and PostgreSQL SQL execution remain unchanged; SQLite is never a fallback from
a failed PostgreSQL connection.

## Single-process memory operational storage

PostgreSQL remains the hosted operational-storage default. To select memory in
reviewed `control-plane.web.host` configuration, replace legacy `host.redis` with:

```json
{
  "target": { "module": "container", "replicas": 1 },
  "operational-store": {
    "backend": "memory",
    "single-process": true,
    "allow-volatile": true
  }
}
```

Invoke Go directly for each reviewed hosted backend:

```bash
CAO_POLICY_PATH="$PWD/.github/workflows/cao.redis.json" go -C server run ./cmd/cao-dashboard serve-hosted --operational-store redis
CAO_POLICY_PATH="$PWD/.github/workflows/cao.memory.json" go -C server run ./cmd/cao-dashboard serve-hosted --operational-store memory
CAO_POLICY_PATH="$PWD/.github/workflows/cao.postgres.json" go -C server run ./cmd/cao-dashboard serve-hosted --operational-store postgres
```

Supply your existing reviewed policy/profile through `CAO_POLICY_PATH` (the paths
above are examples). There is no Node launcher;
the requested backend must match the resolved policy and cannot override it.
It preserves the ordinary hosted authentication and transport requirements.
Direct TLS uses the actual TLS connection without requiring forwarded headers
unless `CAO_TRUSTED_PROXY_CIDRS` explicitly configures a proxy.
Ordinary `npm run dashboard:local` instead selects local Go/SQLite serving.

Use `serve-hosted` with the ordinary mandatory OAuth, HTTPS/proxy, organization
or team, administrator, and webhook-secret configuration. Keep admission,
collection workers, projection, and backfill in that process. Enable the
collection profile with `CAO_COLLECT_APP_ID` and its existing App/evidence
configuration, and set `CAO_COLLECT_WORKERS` to a positive count. Do not use
`CAO_COLLECT_ADMIT_ONLY`, separate `collect`/`backfill` roles, an externally or
platform-owned listener, or multiple replicas. PostgreSQL is still required for
canonical dashboard data. Memory does not need `CAO_REDIS_URL`. Do not specify
both legacy `host.redis` and `host.operational-store`.

All operational state is lost on restart, including sessions, pending login,
revocation retries, limits, enrollment, delivery markers, unfinished accepted
tasks, and backfill checkpoints. Old cookies and OAuth callbacks are rejected;
GitHub tokens themselves are **not** expired or revoked. Signed webhooks receive
retryable errors until complete fresh scope reconstruction succeeds. Workers
then reuse retained evidence and the normal collection/backfill pipeline.
The existing protected `POST /api/admin/rebuild` reruns scope and backfill even
with a populated lake. Recovery cannot recover expired artifacts or every
historical delivery, and capacity exhaustion is reported as incomplete work.

`CAO_OPERATIONAL_CACHE_MAX_BYTES`, `CAO_OPERATIONAL_CACHE_MAX_VALUE_BYTES`, and
`CAO_OPERATIONAL_CACHE_MAX_ENTRIES` bound disposable cache accounting.
`CAO_OPERATIONAL_PROTECTED_MAX_BYTES` and `CAO_OPERATIONAL_PROTECTED_MAX_ENTRIES`
bound protected state; `CAO_OPERATIONAL_TASK_MAX_BYTES`,
`CAO_OPERATIONAL_MAX_QUEUED_TASKS`, and
`CAO_OPERATIONAL_MAX_RATE_LIMIT_SUBJECTS` apply narrower limits. Values must be
positive and internally consistent. Defaults are 32 MiB/1,024 cache entries,
128 MiB/200,000 protected entries, 32 MiB/10,000 queued tasks, and 10,000 limiter
subjects. These are accounting budgets, not RSS. Cache eviction never evicts
protected work or active limiter buckets. `CAO_OPERATIONAL_NAMESPACE` names
operational diagnostics independently of the existing `REDIS_NAMESPACE`
PostgreSQL identity.

Public health/readiness includes `operational.capabilities` and
`operational.recoveryReady`; legacy `redis.connected` is a deprecated alias for
the selected operational dependency. A standalone `doctor` cannot inspect a
different process's live memory state and reports that limitation rather than
constructing an empty store. See the [normative operational contract](../specs/server-operational-storage.md).

## PostgreSQL operational storage

**Provision before startup.** Production and local PostgreSQL entrypoints open
existing state without DDL. Follow [PostgreSQL privilege separation and rollout](POSTGRES.md)
to run owner-only `database bootstrap`, apply runtime/evidence/retention grants,
and schedule separate canonical partition and audit-retention jobs. Do not mount
owner credentials in runtime processes. Local SQLite hosted-CLI debugging uses
the same explicit bootstrap/retention commands with `--sqlite PATH`; see that
guide for the no-PostgreSQL local test command and the limits of SQLite testing.

PostgreSQL is the default when a reviewed host has no backend selector or legacy
`redis` configuration. No Redis connection or
volatile-memory acknowledgement is required:

```json
{
  "target": { "module": "container" },
  "operational-store": {
    "backend": "postgres",
    "namespace-env": "CAO_OPERATIONAL_NAMESPACE",
    "postgres": { "url-env": "CAO_OPERATIONAL_POSTGRES_URL" }
  }
}
```

Omit `postgres.url-env` to use `CAO_POSTGRES_URL`, allowing both adapters to use
the same database without sharing tables or lifecycle. Keep connection strings
in server-side secrets, not policy. Verified TLS (`sslmode=verify-full`) is required
for non-loopback TCP connections, including every configured fallback. Encryption
without certificate and hostname verification (`require` or `verify-ca`) is rejected.
Use `sslrootcert` for a private CA rather than disabling verification. The canonical namespace
remains the existing `REDIS_NAMESPACE`; changing the operational namespace
does not move canonical data. The explicit PostgreSQL profile of local bearer
`serve` also defaults to PostgreSQL operational storage; supplying `--redis-url`
opts in to Redis instead. Plain `serve` uses SQLite with bounded volatile
request services and requires neither database service. Hosted
`serve-hosted`, external/platform hosts, collection roles, and `doctor` resolve
the reviewed policy; the presence of a Redis credential never overrides it.

The adapter owns `cao_operational_namespaces`, `cao_operational_records`, and
`cao_operational_cache`. These are separate from the TypeSpec-generated
canonical entity schema. A namespace row lock serializes each bounded
operational transaction across replicas: session CAS/logout, admission and
deduplication, queue replacement, owner-checked locks, quota reservation, and
protected-state accounting commit or roll back together. Queue waits release
the transaction and poll every 50 ms with cancellation. Sessions, revocations,
accepted tasks, enrollment, checkpoints, quota, and leases survive process
restart; expired authority never becomes valid again. This is durability under
the database's configured persistence guarantees, not a GitHub delivery journal.

The PostgreSQL adapter uses the common `CAO_OPERATIONAL_CACHE_MAX_*` and
`CAO_OPERATIONAL_PROTECTED_MAX_*` bounds above. It bounds protected records at
1 MiB each; the memory-only task and limiter sub-budgets do not apply. Cache and
protected accounting are independent, and session admission reserves space
for atomic logout/revocation staging. All replicas of a namespace must use the
same resolved bounds; startup rejects mismatches rather than letting one
replica widen admission. Limits are logical accounting, not database disk,
allocator, or RSS budgets. Maintenance reclaims up to 512 expired protected
records and 512 cache entries per pass, never unfinished work.

Standalone collector and backfill roles can share this deployment-scoped
adapter. OAuth, HTTPS/proxy, authorization, signatures, quota governance, and
campaign rollout authority remain unchanged. Selecting PostgreSQL does not migrate
Redis or memory state, enable collection, provide
automatic fallback, or dual-write. Use a new operational namespace for a new
configuration and plan an explicit cutover; changing namespaces invalidates
existing CAO sessions and does not transfer unfinished work or token revocations.
`doctor` opens only existing PostgreSQL operational state without creating
tables or a namespace, and reports neutral health/capabilities rather than
Redis-only provider probes.

Run `POSTGRES_URL=... go -C server test -race ./internal/operational/...` against
a disposable local PostgreSQL database for shared adapter conformance and
cross-connection durability, isolation, cancellation, and capacity checks.
Tests create random namespaces and remove their operational state afterward.

## Webhook gateway operations

The [RFC-033 contract and executed queue model](../specs/rfc-033-webhook-gateway.md)
describe a separate, opt-in gateway, not the dashboard collector's webhook
endpoint. It is disabled by default. This change does **not** activate the
gateway in the root policy, enroll repositories, approve subscriptions, or grant
Actions write access.

### Local SQLite and GitHub polling

The [RFC-034 local simulation contract](../specs/rfc-034-local-webhook-gateway.md)
defines the local profile, TLA+ model, and specification-derived conformance
suites. No PostgreSQL, App installation, webhook registration, compiler emitter,
or dispatch/write token is required:

```bash
# From the repository root; the parent directory must already exist and be private.
# Optional GH_TOKEN supplies read-only access for private repositories/rate limits.
go -C server run ./cmd/cao-webhook-gateway --role=local \
  --repository=OWNER/REPO --sqlite="$PWD/local-gateway.sqlite" --once

# Omit --once for a cancellation-scoped polling worker (Ctrl-C stops and joins it).
go -C server run ./cmd/cao-webhook-gateway --role=local \
  --repository=OWNER/REPO --sqlite="$PWD/local-gateway.sqlite" \
  --events=issues,issue_comment,pull_request --poll-interval=60s

# Inspect aggregate simulated outcomes without exposing projected payloads.
sqlite3 local-gateway.sqlite 'SELECT state,count(*) FROM jobs GROUP BY state;'
```

The local client makes only GETs to that repository's metadata and Events API.
`--api-url` supports an explicit HTTPS enterprise API base (including `/api/v3`);
numeric-loopback HTTP is allowed for fixture servers, never hostname-based
plaintext or redirects. The queue identity binds the API base, immutable
repository/owner identities, and selected subscriptions. Reuse the same file and
event selection to replay safely; use a different private file for a different
configuration. Files must be private regular files (0600); SQLite WAL and durable
receipts retain deduplication and fenced state across restart.

Each newly observed event becomes an HMAC-signed **in-process HTTP request** to the real ingress;
there is no local network listener. The real worker claims, renews, authorizes,
reserves budgets, and finishes against SQLite. Only authorization evidence and
dispatch acceptance are synthetic: the dispatcher cannot send GitHub writes.
Startup/output explicitly say simulation; `succeeded` does not mean a remote
workflow ran. The generated fixture projects only `simulation: true` and ingress
provenance. This CLI does not load `--policy` or arbitrary compiler subscriptions
and does not replace the production authorization/filter test suites.

Do not provide `CAO_GATEWAY_RECEIVE_KEY_FILE`, `CAO_GATEWAY_DISPATCH_KEY_FILE`,
`CAO_GATEWAY_WEBHOOK_SECRET`, or `CAO_GATEWAY_PREVIOUS_WEBHOOK_SECRET` to local
mode; startup rejects production credentials. `GH_TOKEN` is optional and is never
printed or persisted. The local fixture does not grant production authority or
modify `cao.json`.

Polling reads at most 300 visible events per pass, honors GitHub's
`X-Poll-Interval`, and stops explicitly on API/admission failures. Restart retries
the retained snapshot safely; an ETag advances only after all selected deliveries
are admitted. Retained SQLite receipts recognize known event IDs even if GitHub
changes their feed payloads; existing jobs are neither replaced nor replayed.
New deliveries still undergo the ingress's body-digest conflict checks.
There is no high-water ID cursor that skips late events.
This is a delayed, bounded, best-effort feed, **not a lossless webhook journal**.
It cannot recover feed gaps or simulate Projects v2 webhooks. Missing actions,
filter operands, or transition data are not invented. Supported families are
listed in RFC-034; one-shot mode reports deferred leased/rate-limited work as
incomplete rather than claiming it was dispatched. Continuous mode waits for
rate-budget recovery instead of stopping the host on normal backpressure.
Malformed feeds, including JSON `null` instead of an array, do not advance
polling progress.

### Isolated production roles

Run two isolated deployments of `server/cmd/cao-webhook-gateway`: a public
receive role and a private dispatch role. Each uses an absolute path to its
reviewed `cao.json` and the same durable PostgreSQL operational namespace. The
commands below show separate process roles; they are not evidence that a live
deployment or compiler integration was tested:

```bash
# Receive deployment only: webhook ingress; no private App keys.
go -C server run ./cmd/cao-webhook-gateway \
  --role=receive --policy=/etc/cao/cao.json

# Separate dispatch deployment: source-read and destination dispatch credentials.
go -C server run ./cmd/cao-webhook-gateway \
  --role=dispatch --policy=/etc/cao/cao.json
```

Before starting either role, an operator must run
`cao-dashboard database bootstrap --store operational --namespace NAMESPACE`
with separate `CAO_DATABASE_ADMIN_URL` credentials and apply the runtime grants
in [POSTGRES.md](POSTGRES.md). Use the exact namespace from the reviewed gateway
policy. Gateway startup opens existing state only; it cannot provision or repair
tables. Mount only the runtime `CAO_POSTGRES_URL` in the gateway processes.

The role has no permissive default: an unset role fails closed. For a lean
container without Node or dashboard assets, build the dedicated nonroot
(UID 65532) target; the Dockerfile's default final dashboard image is unchanged:

```bash
docker build -f server/Dockerfile --target webhook-gateway-runtime \
  -t cao-webhook-gateway .
```

Mount the reviewed policy and role-specific secrets read-only in independently
configured receive/dispatch deployments. Plain HTTP ingress is allowed only on
a numeric loopback listener; non-loopback receive listeners require both
`--tls-cert` and `--tls-key`. Do not assume a public plaintext listener or
forwarded headers satisfy this requirement.

The receive listener exposes `GET /healthz` (204) and `GET /readyz` (204 only
when the current policy is valid/enabled, its revision is applied, and queue
depth is reachable; otherwise 503). These routes do not consume webhook-handler
slots. A shared limit of 64 simultaneous webhook handlers rejects excess work
with 503 **before reading the body**. Accepted handlers have an 8-second context
deadline; HTTP ReadTimeout and WriteTimeout are both 10 seconds.

Keep `CAO_POSTGRES_URL` in each deployment's secret environment. Give only the
receive deployment `CAO_GATEWAY_WEBHOOK_SECRET` and, during bounded rotation overlap,
`CAO_GATEWAY_PREVIOUS_WEBHOOK_SECRET`. Give only the dispatch deployment
`CAO_GATEWAY_RECEIVE_KEY_FILE` and `CAO_GATEWAY_DISPATCH_KEY_FILE`.
The receive process MUST NOT have either private App key.
Startup rejects either App key-file environment variable in receive mode and
rejects current/previous webhook secrets in dispatch mode.
Key-file variables point to mounted secret
files; never put key contents or webhook secrets in policy. Receiving and
dispatch App IDs come from reviewed policy (`receivingAppId` and
`dispatchAppId`), not a `CAO_GATEWAY_RECEIVE_APP_ID` environment override.
The dispatch process uses the receiver App only for narrowly scoped source
authorization reads and the dispatch App for approved destination dispatches.
Rotate each private key independently by replacing its mounted secret file and
restarting the dispatch role; do not combine private-key rotation with webhook
HMAC rotation. For webhook rollover, install the new current secret and bounded
previous-secret overlap in the receive deployment, restart that role, then remove
the previous secret after the approved overlap and restart again.

The dispatch role publishes short-lived authorization proofs (30 seconds) to
the existing operational `StateStore`, using one bounded fixed key per
subscription ID. Its proof primer runs even when the queue is empty.
The primer rotates across deadline-limited rounds so slow subscriptions cannot
always starve later routes; incomplete refreshes are reported with
`DEBUG=cao:webhook-gateway:authority`.
Receive admits only with a fresh proof bound to both the current queue policy revision
and the exact trusted subscription revision;
missing, expired, mismatched, or incomplete proofs fail closed. No private key is
delegated to ingress. Before each outbound attempt, dispatch always performs
fresh live installation/repository checks for both source and destination,
current root-policy scope checks, and grant validation; admission proofs are not
an egress authorization cache.
An omitted source `repositoryId` binds only organization-level deliveries
without a repository envelope. It is not an organization-wide repository
wildcard: repository-bearing events require an exact approved source ID.
Quota-observation storage failures during final preflight block the POST;
failures after a known HTTP acceptance never convert it into a retry.

The queue stores only payload-free `Job.Event` identity and bounded compiled
`Job.Payload` dispatch JSON, never original matched or filtered event bodies.
Ingress evaluates filters/constraints on ephemeral authenticated bytes, builds
the approved leaf allowlist plus reserved provenance, then discards raw input.
Workers authorize against stored identity and immutable approved metadata and
combine compatible immutable projections into one bounded batch; they never
parse compiled JSON as a source event or rerun ingress filters.
The original delivery hash remains admission dedup evidence, independent of
projected payload bytes. Restrict database access, require deployment-provided
encryption at rest, and enforce reviewed byte/retention bounds. Raw events must
never be persisted, forwarded, or logged.
**Handlers always receive batches, including singleton flushes.** Compiler v2
uses exactly `client_payload:{schema_version:2,aw_context:{webhook:{event,
deliveries:[{delivery_id,payload},...]}}}`. All distinct deliveries are retained;
the compiler derives the unique affected-resource array at `aw_context.items`.
It does not trust a supplied item list or infer latest state. Compiler security
payloads require `{"url":"<canonical GitHub API alert URL>","type":["<original action>"]}`
plus only declared matching `filters` when predicates exist,
under the required `resource-url-types-filters/v2` projection policy. Native alert bodies,
URL-only legacy payloads, and additional resource details are rejected.
Approved artifact revision
evidence stays in immutable durable provenance and the typed compiler-dispatch
argument, never additional wire fields. Missing evidence/capability blocks send.

Generic approved repository-dispatch routes use a separate contract, not a
compiler-runtime fallback, with five properties: `schema_version: 2`,
the original `event` kind, sorted unique `delivery_ids`, a nonempty `aw_context`
array of unique approved projections, and runtime-owned `_gateway` provenance.
Delivery IDs do not participate in context uniqueness, so repeated equivalent
contexts collapse without losing delivery provenance. Distinct context/activity
values remain distinct; this is not a latest-state or chronological event feed.
Only events sharing the complete subscription, revision, source identity and
event kind can batch together.

Provenance carries `version: "cao.webhook-batch/v1"`, source/subscription IDs,
the complete approved Revision, and a SHA-256 batch identity over sorted durable
job IDs. It is an object for repository dispatch. Workflow dispatch uses the
same five properties under `inputs`, JSON-encoding all but `event` as strings.
Its metadata must assert both `destination.gatewayInput: true` and
`destination.batchInput: true`, confirming all reserved inputs exist. Missing
batch capability or legacy singleton envelopes fail closed, without a
one-workflow-per-event fallback. The internal stored member projection retains
`cao.webhook/v1` provenance but is never sent as the wire envelope.

Long-running roles coalesce for two seconds before a route's oldest eligible
event can be claimed; singletons then flush subject to quota/authority. Local
one-shot mode immediately batches its already-polled backlog. Each batch has
at most 64 jobs and must fit under 64 KiB, including provenance and framing,
plus compiler byte ceilings where applicable. Overflow stays queued.
The durable cursor rotates between `(tenant, subscription)` workflow routes.
Replica claims are exclusive, and concurrency measures batches rather than
member rows. This lets the server balance workflow dispatch across replicas.
Every member fence/authorization is checked before one atomic reservation and
one rate charge per external request. Completion is atomic for every member;
unknown/lost acceptance makes all reserved members ambiguous, never automatic
per-event replay.
Both dispatch endpoints explicitly pin REST API `2026-03-10`; workflow acceptance
supports HTTP 200 and legacy 204. Safe queue outcomes retain accepted HTTP status and a validated numeric
workflow-run ID when returned, not the raw response body or run URLs. A
bodyless legacy response need not provide an ID; acceptance is not execution
success.
Production workers must receive the shared-quota `Budget` adapter even though
the shell allows it to be absent in pure tests. Its acquire/release lifecycle
wraps durable dispatch reservation; the paired dispatcher reconciles actual
GitHub response quota without changing a known accepted dispatch into a retry.
Cancellation after a known response or a proven unsent request preserves that
observation; only an uncertain transport outcome becomes `ambiguous`.

The reviewed `control-plane.webhook-gateway` section binds enabled state,
monotonic revision, App identities, namespace, queue/worker bounds, explicit
administrator grants, and approved compiler artifacts. Each artifact is pinned
by its exact SHA-256 digest plus repository identity, commit SHA, and compiler
identity. Discovered metadata is untrusted and cannot approve itself. Source,
destination, workflow, visibility, and project constraints must agree with the
reviewed grant and immutable subscription revision. Repository names and event
names do not create authority.
Root `control-plane.scope.allowed-repositories` and `allowed-owners` constrain
gateway scope by **intersection**, never union. Recognizing the gateway in the
shared policy loader/schema does not enable it. Root-scope changes must also
bump the gateway revision; changing the effective digest at the same revision
conflicts rather than silently replacing authority.
Subscription metadata requires explicit `provider: "github"`; missing or
unsupported providers are rejected. The configured source route ID is separate
from provider identity.
Project IDs are GraphQL node-ID strings (`projectIds: ["PVT_..."]`), not numeric
IDs; compiler-declared generic constraints must bind the exact approved set to
`/projects_v2_item/project_node_id`, not the item's `node_id`.
Current authenticated GraphQL authorization checks exact project identity and
organization ownership only, without fetching project content or enriching
missing filter values. User-owned/unverifiable project nodes fail closed.
The dispatch role records project responses in the receiving App installation's
shared `graphql` quota bucket, including backoff; unavailable quota storage
defers authorization.
For Projects item edited events, GitHub's published field-change example includes
`changes.field_value.field_name`, `field_type`, and single-select `from.name` /
`to.name`. The manifest mapper preserves complete transition pairs and requires
delivered operands. Missing status/label/transition operands remain nonmatches;
do not invent fields or fetch missing values to make a subscription match.

Read policy afresh for every ingress/dispatch decision. Metadata may be cached
only as immutable bytes bound to its exact approved digest; cached metadata
does not replace fresh policy/grant checks.
Adding static source routes, adding/removing managed discovery, or changing
namespace, App IDs, queue capacity options, or worker count requires a role
restart. The immutable configuration cache fails
closed on incompatible changes; do not expect a hot reload to rebuild topology.
Missing, changed, invalid, inaccessible, disabled, or unsupported configuration
fails closed; there is no last-known-good authority fallback. Apply the loaded
`QueueRevision` through the durable queue's `ApplyRevision` before admission or
worker action. Database comparison rejects older revisions and conflicting
equal revisions, so a stale replica cannot revert an already-applied
revocation. A newer revision conservatively cancels older ready/claimed jobs.
External GitHub revocation is observed through authorization calls and signed
repository/update invalidation hints; a
30-second admission proof and even a fresh egress check cannot eliminate the
remote check-to-send race. Do not promise immediate global revocation.
Dispatching is an irreversible local reservation: it cannot be recalled by a
later revoke, and unknown outcomes become terminal `ambiguous`, not a replay.
Only positively known nonacceptance, including an explicit rate-limit refusal,
can retry within the original deadline and bounded attempts.

### Gateway observability and alerts

Reuse the server's existing OpenTelemetry setup:
`OTEL_EXPORTER_OTLP_ENDPOINT` (or signal-specific endpoints), role-specific
`OTEL_SERVICE_NAME`, and the existing collector/export pipeline. An absent
endpoint or `OTEL_SDK_DISABLED=true` means no exported telemetry; instrumentation
alone does not establish monitoring. When unset, `OTEL_SERVICE_NAME` defaults to
`cao-webhook-gateway`; deployments MAY override it to distinguish roles.
The gateway's actual OTel instrument names
are:

- `cao_webhookgateway.operation.count` and
  `cao_webhookgateway.operation.duration` (seconds), labeled by
  `cao_webhookgateway.stage`, `cao_webhookgateway.event_family`, and
  `cao_webhookgateway.outcome`;
- `cao_webhookgateway.queue.age` (seconds);
- `cao_webhookgateway.queue.depth`;
- `cao_webhookgateway.retry.count`.

When the queue provides aggregate `QueueStatsProvider` observations, the worker
also emits `cao_webhookgateway.queue.retained.rows`,
`cao_webhookgateway.queue.retained.bytes`,
`cao_webhookgateway.queue.oldest_age` (seconds),
`cao_webhookgateway.workers.active`,
`cao_webhookgateway.workers.utilization` (ratio), and
`cao_webhookgateway.tenant.utilization` (ratio histogram without tenant labels).
Bounded maintenance reports feed `cao_webhookgateway.lease.expiration.count`
and `cao_webhookgateway.queue.recovered.count`,
`cao_webhookgateway.queue.expired.count`,
`cao_webhookgateway.queue.ambiguous.count`, and
`cao_webhookgateway.queue.deleted.count`. Metrics/observer failures do not widen
authority or change durable dispatch outcomes.

Spans use `cao_webhookgateway.<stage>`, including `ingress`, `signature`,
`matching`, `admission_authorization`, `admission`, `worker`, `claim`, `authorization`,
`quota`, `renewal`, `begin_dispatch`, `dispatch`, and `finish`.
For Prometheus, export these through the existing OTel Collector pipeline and
confirm its dot/underscore and counter-suffix normalization before writing
PromQL; do not assume a new gateway `/metrics` listener exists.
Queue depth is emitted only when the composed queue implements optional
`QueueStatsProvider` or `QueueDepthProvider`; absent samples are not a zero-depth
guarantee.

Alert on any `operation.count` increase with stage `finish` and outcome
`ambiguous`; investigate before any manually approved redrive. Alert on sustained
error outcomes in authorization, quota, admission, or dispatch, increasing retry
count, queue depth approaching the reviewed capacity, and queue-age histogram
tails approaching the original job expiry. Also alert on absent telemetry or
unhealthy role/storage dependencies, rather than interpreting missing samples
as an empty queue. Queue age is sampled on work observation, not a proof that
every stalled job is being measured. Keep delivery/job identities in approved
traces only, never metric labels; never export webhook bodies, secrets, tokens,
private titles, or arbitrary payload attributes.
Also investigate sustained `auth_denied`, `auth_deferred`, `capacity`, and
`unavailable` outcomes. These are operational recommendations; this change does
not install Prometheus alert rules or other alert resources.

### Compiler bridge and validation limits

Trigger discovery reads `webhook_subscriptions` from the single-line
`# gh-aw-manifest: {...}` header proposed by
[github/gh-aw#69772](https://github.com/github/gh-aw/pull/69772), inspected at
`5113c42dda961e53fb08fdb92a84fb46dc87aa1f`:

```bash
go -C server run ./cmd/cao-webhook-gateway --role=discover \
  --workflow="$PWD/.github/workflows/example.lock.yml"
```

Repeat `--workflow` for each explicitly selected compiled workflow in one
repository. The read-only, offline role emits a deterministic
`cao.webhook-trigger-mapping/v1` event-to-workflow index with
`discoveryOnly: true`, complete typed declarations, paired `any_of` groups,
dispatch/context paths and limits, and whole-lock SHA-256 identities.
Native-only workflows contribute no webhook routes. Invalid, conflicting, or
over-budget inputs reject the entire inventory. It reads no workflow steps or
frontmatter and rejects production gateway App/HMAC credentials and mixed-role options.
Bounds and matching semantics are specified in RFC-033 section 6.1.

Offline discovery is **not activation**. Production activation is a separate,
explicitly reviewed delegation for private repositories. Merge a configuration
like this fragment into `control-plane.webhook-gateway`, replace the illustrative
identities with verified IDs, and increment its reviewed `revision`:

```json
{
  "discovery": {
    "activateSecurity": true,
    "intervalSeconds": 300,
    "repositories": [
      {
        "repository": "acme/private-repo",
        "repositoryId": 42,
        "receivingInstallationId": 7,
        "dispatchInstallationId": 8
      }
    ]
  },
  "projectAssignments": [
    {
      "projectId": "PVT_example",
      "organizationId": 99,
      "repositoryId": 42
    }
  ]
}
```

This fragment does not replace the required gateway identity, queue, artifact,
grant, root scope, or enablement configuration. No checked-in rollout is changed.
Both owner and repository allowlists intersect when present. Public and internal
repositories are denied at live admission/egress checks; RFC-034 local simulation
remains non-authoritative and may read a public Events API.

The dispatch role backfills only these repositories and routinely scans their
current default branch using fresh repository-restricted receiving-App tokens.
It reads regular top-level compiled locks at one exact commit and rechecks the
head before publication. The default rescan threshold is 300 seconds
(30–3600 configurable). Ten-second wakeups process at most eight due repositories
per round, with 30 seconds per repository; backpressure can delay completion.
Private inventories expire after twice their configured interval.
Failures suspend routes rather than retain indefinitely active old metadata.
Directory and lock-size/count bounds are specified in RFC-033 section 6.2.

Configure the receiving App's webhook URL as `/webhooks/compiler`, subscribing
to `push`, `repository`, and the selected security families. The route exists
before any lock is discovered. Signed lock-update and repository-change hints
atomically invalidate the enrolled repository before HTTP 202; storage failure
returns a failure response. Hints never supply a fetch URL, ref, or authority.
The receiver still holds no App private keys; its database invalidation wakes
the dispatch role through routine polling. CAS publication fences in-flight
older scans, including repeated hints on already-dirty records.

With gateway enablement and `activateSecurity: true`, valid **batch-capable v2**
discovered `dependabot_alert` and `code_scanning_alert` declarations
become subscriptions with bounded expiring grants and workflow-specific compiler
dispatch routes. Without `repos`, activation remains same-repository. An explicit
compiler declaration can select multiple source repositories:

```yaml
on:
  gateway-dispatch:
    repos:
      - bar/baz
      - foo/*
    security-alerts: {}
```

The upstream compiler places all gateway declarations under `on.gateway-dispatch`;
roles/bots and other non-trigger settings remain outside that mapping under `on`.
Gateway dispatch is exclusive: all other workflow triggers are rejected.
Shared `repos` belongs inside `gateway-dispatch`, not inside a trigger, and
applies to GitHub notifications, code-scanning/Dependabot and native command subscriptions.
The repository still independently requires `"gateway-dispatch": true` in
`.github/workflows/aw.json`.
These are compilation gates, not gateway activation or dispatch authority.
The former workflow `on.webhook-gateway` flag, flat triggers, obsolete `on.gateway`,
singular Projects/security spellings, and old repository `webhook-triggers` opt-in
are rejected. Explicit `workflow_dispatch` or `repository_dispatch` declarations
cannot accompany `gateway-dispatch`; a shared-`repos`-only mapping is also invalid.
For the supported security families, this authoring change leaves the normalized
lock manifest and closed v2 dispatch envelope unchanged; the runtime conformance
pin below does not claim installed-compiler emission of this syntax.

Selectors are lowercase, sorted, unique exact names or literal-owner `/*`
patterns, intersected with reviewed `discovery.repositories` and root scope.
They never enroll repositories or broaden authority. Both source and workflow
repository need fresh private inventories; an authenticated source inventory
may have no workflows. Each source receives an independent grant and batch
route to the declaring workflow, expiring at the earlier inventory bound.
An empty `security-alerts: {}` with shared `repos` selects both supported security
families; explicit `sources` narrows them. CAO reads only the compiled manifest.

The receiving App needs contents-read
and the corresponding security-event read permission; the independent dispatch
App needs contents-write. Each projected security payload contains exactly its
canonical GitHub API `url` and `type` array containing the original action,
plus only explicitly selected matching filter literals, never raw alerts or
unselected resource details. Filters run
against the authenticated original delivery before projection. The bridge checks
the signed source identity at ingress and binds every URL to the authenticated
current source repository before dispatch, and reapplies selectors. Cross-source
events never share one batch. Source preflight uses the receiving App while
the destination's approved workflow artifact uses the dispatch App; source or
destination access loss blocks sending. API origins other than
`https://api.github.com` are not supported. Both compiler byte limits apply to
the complete framed request before deduplication.

Declaration v2 must specify `dispatch.batch:{mode:"same-event",
deduplicate_by:"delivery_id"}`, exactly
`dispatch.payload_projection:{"policy":"resource-url-types-filters/v2"}`, and the fixed
context paths in RFC-033 §6.1. Missing/unknown projection policies do not activate.
All additional resource data must be retrieved inside the workflow through
configured provider-authenticated readers; notification URLs do not grant access
or justify attaching credentials to arbitrary origins.
Legacy declaration v1 remains discoverable but cannot activate. The bridge
matches [github/gh-aw#69772](https://github.com/github/gh-aw/pull/69772) at
`fe4f420f3f4f40bb1ebbcca85af0903d3a967bf3`; optional interoperability tests
execute that commit's actual runtime validator/normalizer, including exact and
wildcard source selection, projected filter evidence, and rejection of any out-of-selector delivery. An installed
compiler emitting v2 is still required. Secret-scanning triggers are not
supported by that compiler and do not autoactivate. See RFC-033 §13.1 for the
wire contract, test reproduction, and bounded TLA+ result.
Every delivery now follows `resource-url-types-filters/v2`: `url` and `type` are
required, and `filters` is required when predicates or a selected Azure lifecycle
match exist. Its flat keys are compiled condition names, plus the applicable Azure
`closed-states` key, and its values are nonempty distinct homogeneous scalar
arrays containing only matching literals actually observed in that delivery.
Include all shared conditions and one complete matching `any_of` group, never
cross-combined transition pairs or unselected source-array elements. With no
predicates or a selected Azure lifecycle match, omit `filters` except for command
subscriptions' explicit event/token or label evidence below. Both old
`resource-url-types/v1` and `resource-url-types-filters/v1` inventories are
nonactivating. Severity remains an ordinary compiled condition; only matching
literals appear in `filters.severity`.

Optional shared `on.gateway-dispatch.orgs` selects GitHub organizations with
literal names or `*` patterns (case-insensitive, 1–39 ASCII letters/digits/hyphens/
stars, no leading/trailing hyphen). Compiled selectors are lowercase/sorted/unique.
They intersect `repos`, reviewed enrollment and project scope; `*` grants nothing.
Activation and egress verify actual Organization owner type/ID through authenticated
source binding, not merely a URL owner name. External triggers ignore this shared
authoring option and do not receive `orgs` metadata.

### Native slash and label commands

The bridge consumes compiled `on.gateway-dispatch.slash-command` and
`label-command` declarations from the pinned runtime above. It filters original,
authenticated GitHub deliveries, including issue/PR comment splitting,
position-zero slash tokens, exact/catch-all/trailing-star command matching, and
exact label matching. Native command strategies are retained metadata, not
authority to create listeners or routes. Shared `repos` and `orgs` intersect
reviewed source enrollment and private inventories.

Activation requires a separately reviewed `discovery.activateCommands:true`;
omission remains off. Optional `discovery.serverUrl` defaults to
`https://github.com` and binds the receiving API client's HTTPS origin and clean
base path. Original HTML item links have no anchor; comment links require the
catalog's exact original `#issuecomment-N`, `#discussion_rN`, or
`#discussioncomment-N`. Reviews, submitted/ready-for-review/review-requested
events, and arbitrary anchors never qualify. See RFC-033 §6.2.3 for the catalog.

Wire payloads contain only `url`, selected singleton `type`, and
`filters:{"event":["<semantic source>"],"command":["<actual token>"]}` or the
equivalent exact `label` literal. No command body/arguments are stored or
forwarded. `gateway.events` is compiler-local access to normalized deliveries,
not another wire field or an authority channel.

Gateway label commands require compiled `remove_label:true`; false metadata is
rejected, not a read-only activation option. Authoring omission defaults to true.
The pinned published contract rejects nonconsuming gateway labels.
Compiled `remove_label:true` alone cannot write. The exact source row must also
have independently reviewed `allowLabelRemoval:true`, and the receiving App
must currently have the selected event category's write permission. This does
not change checked-in rollout policy. Only the dispatch role receives the label
consumer; receive and local simulation acquire no mutation capability.
Consumption follows atomic durable batch admission and a fresh authority/quota
check, using authenticated REST for issues/PRs or minimal GraphQL for discussions.
Protected attempt markers and expiring confirmed receipts prevent repeated
removal across retries/restarts. Ambiguous outcomes never retry a mutation or
dispatch the workflow, protecting a subsequently reapplied label.

`TestCommandPublishedRuntimeInteroperability` runs actual pinned validator and
normalizer code for all seven slash sources and three label sources, including
approved enterprise base paths and whole-batch rejection of unselected
literals/extra fields/old policies. This is local conformance, not deployed or
hosted interoperability.

### GitHub notifications

The published ten-family catalog covers issues, pull requests, discussions,
issue/PR conversation comments, PR reviews, review comments, review threads,
discussion comments and commit comments. Activation requires separately reviewed
`discovery.activateNotifications:true`; omission remains off, independently of
security and command activation. Source permissions are read-only and neither
notifications nor their metadata grant label writes.

The receiver checks original event/action, exact typed catalog filters and
issue-versus-PR source evidence. It binds canonical original HTML URLs to the
authenticated source repository, parent number or commit SHA, original comment/
review ID, configured GitHub origin and exact Enterprise base path. Required
anchors include `issuecomment-N`, `pullrequestreview-N`, `discussion_rN`,
`discussioncomment-N` and `commitcomment-N`. Review threads require
`thread.comments[0].html_url` and its original ID; missing evidence never causes
hydration. Payloads retain only `url`, the selected singleton action in `type`,
and actual matching declared literals in `filters` when predicates exist.

`ready_for_review`, `review_requested` and submitted reviews are notifications,
not slash-command sources. The embedded catalog's semantic fields and all actions
are checked against the actual pinned validator/normalizer in
`TestNotificationPublishedRuntimeInteroperability`, with public and Enterprise
base-path cases. These local checks do not claim deployment or hosted acceptance.

### Enrolled Jira, Linear, and Azure DevOps sources

The same compiler-v2 path supports `jira_issue`, `linear_issue`,
`azure_devops_issue` declarations. Each requires independent
reviewed `control-plane.webhook-gateway.externalSources` enrollment, managed
discovery, gateway enablement, and a fresh compiled declaration in an explicitly
enrolled private destination. This is separate from `activateSecurity`; that flag
does not grant external source authority. No provider registration or rollout is
automatic.

Each source has a unique `id` (its `/webhooks/<id>` endpoint), `event`,
canonical HTTPS `resourceUrlPrefix` ending `/`, `secretEnv`,
`destinationRepositoryIds` selected only from `discovery.repositories`, and
`expiresAt`. Provider sources additionally require:

| Family | Reviewed source binding | Native authentication |
| --- | --- | --- |
| `jira_issue` | `scope`: project key; prefix `https://TENANT.atlassian.net/rest/api/3/issue/` (API 2 also supported) | `X-Hub-Signature: sha256=<hex HMAC-SHA256 of raw body>`; `X-Atlassian-Webhook-Identifier` |
| `linear_issue` | `scope`: organization ID; prefix `https://linear.app/WORKSPACE/issue/` | `Linear-Signature` raw hex HMAC-SHA256; `Linear-Delivery`; signed `webhookTimestamp` within one minute |
| `azure_devops_issue` | `scope`: project ID; `collectionId`: collection ID; collection-qualified canonical work item API prefix | Service-hook HTTPS Basic authentication, username `cao`, password from `secretEnv`; HTTPS-authenticated body `id` |

Azure uses native `workItemId` for updates, never the update/revision ID; version-1
snapshot fallback requires the upstream contract's snapshot evidence. Azure native
bodies are protected by HTTPS and Basic authentication, not a claimed provider HMAC.
Jira resolution/label changelogs, Linear `updatedFrom`, and Azure state/tag deltas
are interpreted only in the receiver under the compiled normalization policy.
Normalization derives all established activities, including `edited` for updates,
then retains only the subscription's selected types. Every forwarded type must
be selected; an empty intersection or contradictory closed/reopened evidence
cannot qualify. Missing or
ambiguous evidence does not imply a lifecycle transition. Native bodies are
discarded; only the reference, activities and declared matching literals persist.
For selected Azure `closed`/`reopened`, `filters["closed-states"]` contains exactly
one configured terminal name: entered for closure, departed for reopening.
It never contains the unmatched nonterminal side or the complete configured set,
and is omitted without a selected lifecycle activity. `closed_states` is
normalization metadata, not a source-path equality condition.

Custom/arbitrary webhooks are excluded from this published revision. Compiler
`event:"webhook"`, authoring `webhooks` arrays and generic `externalSources`
enrollment reject, including persisted old enrollments. No compatibility fallback
to a full-body or separately approved manual route is provided.

Secret names must use `CAO_GATEWAY_SOURCE_...`; put values (32–512 bytes) only in
the receive process environment. Optional `<secretEnv>_PREVIOUS` permits rotation.
Dispatch, discover and local roles reject provider secret variables. All source
routes require HTTPS outside numeric loopback; configure native webhook setup
explicitly in each provider. Source additions require receiver restart; removal,
expiry, revision changes and inventory invalidation revoke/fence queued authority.
Linear responds HTTP 200 only after successful durable admission or filtering;
storage/policy/authority failures never receive a success acknowledgement.

External source authority is current reviewed enrollment plus native
authentication, **not** a provider resource-access or visibility probe. References
are conservatively restricted/private, destinations remain private and freshly
authorized, and workflows must fetch all other data using authenticated provider
readers. URLs are neither credentials nor permission grants. See
`specs/fixtures/webhook-external-deliveries-v2.json` for all four illustrative
enrollments and `TestExternalGatewayPostgresConformance` for durable local coverage;
neither establishes hosted provider delivery or deployment.

`projectAssignments` maps an organization project to exactly one enrolled
repository. It constrains separately approved project subscriptions at admission
and egress; it does **not** automatically activate compiler Projects declarations.
Keep their explicit artifacts/grants and project identity constraints.

The shared PostgreSQL operational registry atomically publishes inventory with
a separately monotonic effective queue revision. Policy changes clear discovered
authority; routing changes cancel older ready/claimed jobs. Freshness-only
refreshes advance registry CAS without cancelling valid work. Protected-record
storage needs no new schema/table. Production roles must use the registry loader's
effective `QueueRevision`, never apply the reviewed file's numeric counter
directly after binding the registry.

After upgrading from an obsolete projection contract, incompatible persisted
inventories fail closed at the current policy revision. A higher reviewed gateway
policy revision atomically revokes the old inventory and ready/claimed work,
then permits fresh bounded discovery within the same enrollment. It does not
reinterpret old payloads or grant new authority; this change does not edit rollout
policy automatically.

Both receiving-side authority and final dispatch preflight recheck the current
head and whole-lock digest. Repository dispatch still runs GitHub's default
branch; preflight does **not** guarantee immutable execution across a later ref
change. Establish trust in the reviewed/protected default branch and complete
deployment acceptance before enabling dispatch. The hand-authored fixture,
mock GitHub tests, local simulations, and TLC results do not prove actual
compiler emission or live signed delivery-to-workflow execution.

The host demonstration uses real PostgreSQL, signed HMAC ingress, and mock
GitHub HTTP—not live GitHub or a real subscription compiler. Its targeted command
is:

```bash
# Set POSTGRES_URL to a disposable local PostgreSQL database first.
go -C server test -p 1 ./internal/webhookgatewayhost \
  -run '^TestProjectsV2GatewayPostgresDemonstration$'
```

The command is an operational recipe, not a claim that this documentation change
ran the demonstration or established production end-to-end readiness.

## Offline query validation

From `server/`, run `go run ./cmd/cao-dashboard compile-queries` to check every
query in `dashboard/site/dashboard.json`, `dashboard/site/dashboard-fragments/`,
and the canonical database query file. Use `--format json` for
machine-readable results. This command validates definitions offline; it does
not compile or execute SQL plans.

Hosted queries are compiled to SQL and executed in PostgreSQL within the
request's repeatable-read transaction. The server does not fall back to a Go
row evaluator: unsupported definitions and unregistered sources fail closed.
Generated TypeSpec identifiers are used, values and namespace are
parameterized, missing values remain distinct from explicit null, and SQL
resource checks enforce operation, output, retained-row, and memory budgets.
Full-plan cardinality and byte accounting share one materialized SQL execution
with join-key uniqueness checks, so validation does not rerun the upstream
dependency graph for every join. These checks cover all rows before returning
the requested result page. Read transactions prefer hash/merge joins by disabling
nested-loop plans locally: PostgreSQL 16 can estimate window-filtered intermediate
relations as one row and otherwise rescan thousands of derived rows per match.
This planner preference, like disabled JIT, is reset on commit or rollback and
does not change query semantics or resource budgets.
Explicitly registered operational sources are provided at the server boundary
and participate as bounded SQL input relations.

The deployed query corpus is exercised through the SQL executor by the
`TestDashboardQueryCorpusUsesPostgres` integration test. It requires
`POSTGRES_URL`; the dashboard-query-parity CI job additionally compares
representative HTTP/MCP results with the browser evaluator over the deployed
unparameterized named-query corpus.

With a disposable fresh database, run
`POSTGRES_URL=... go test ./internal/postgresx -run '^$'
-bench '^BenchmarkNativeFilterPlan$' -benchmem -benchtime=100x -count=1`
from `server/` to compare typed SQL and hand-written SQL retrieval over 5,000
domain records. The benchmark reports
`EXPLAIN (ANALYZE, BUFFERS)`, table/index bytes, ingestion time, allocations,
and p50/p95; it asserts zero canonical scalar copies in generic value storage.
These synthetic measurements do not establish production traffic coverage.

The loopback-only `benchmark-queries` command measures downloaded deployed
artifacts in a disposable namespace. Its execution limits admit up to 500,000
input rows, 50 million operations, and 1 GiB of intermediate SQL working data;
hosted queries retain their production limits. Each query measures its first
500-row result page, retaining the full plan's cardinality and cost checks and
reporting page rows and total result rows separately. Output and retained-data
limits remain unchanged. The query-cost test independently enforces its
duration, operation, and retained-byte thresholds. Before ingestion, set
`CAO_POSTGRES_RUN_RETENTION_DAYS` to cover the artifact timestamps and
`CAO_POSTGRES_LINKED_RETENTION_DAYS` when historical linked evidence is needed;
deployed query-cost CI provisions 90-day windows without changing hosted retention.

### Redis memory budget

Every Redis server profile has a whole-node Redis cache-pressure budget of
**200,000,000 bytes** by default. Override it with `CAO_REDIS_MAX_BYTES` (a
positive byte count), or `server.Config.RedisMaxBytes` when embedding. Startup
and 30-second maintenance reclaim only disposable caches. Supported providers
atomically constrain admission and cached-response allocation. Providers
prohibiting scripted memory introspection explicitly disable disposable-cache
admission and serving, while retaining protected operations and pressure
reclamation. Operational state and other namespaces are
protected, and unreclaimable pressure is reported rather than silently deleting
sessions or queued work. The effective budget never exceeds 80% of a reported
Redis `maxmemory`. See the
[managed Redis deployment guide](../docs/deployment-managed-redis.md#redis-memory-budget).
The read-only `doctor` command resolves the same budget override and reports
configured/effective limits, not only provider utilization.

Memory-pressure integration tests require an isolated, disposable Redis node.
When `REDIS_URL` is set, serialize package execution with `go test -p 1 ./...`;
namespaces do not isolate whole-node memory measurements.
`npm run dashboard:server:test` applies this automatically.

### Debug logging

The server includes the namespace logger helpers from `github/gh-aw`. Debug
logs are disabled by default and go to stderr when enabled. Enable selected
components with `DEBUG`, for example:

```bash
DEBUG=cao:server,cao:query npm run dashboard:server:serve
DEBUG=cao:* npm run dashboard:server:ingest -- --source DIRECTORY
DEBUG='cao:*,-cao:redis' npm run dashboard:server:serve
```

Available namespaces are `cao:cli`, `cao:server`, `cao:ingest`, `cao:query`,
and `cao:redis`. `ACTIONS_RUNNER_DEBUG=true` enables all namespaces when
`DEBUG` is unset. Logs contain operation names, counts, timings, and status;
they do not include access tokens, OAuth credentials, Redis credentials,
query payloads, or source records. Authentication paths emit fixed
`oauth branch=<operation>.<outcome>` identifiers for every decision and outcome;
the identifiers never contain user, request, session, or credential values.
In the hosted dashboard, add `?debug=auth` to enable matching client-side
authentication branch events through `dashboard/site/src/debug.js`; these
events likewise contain fixed identifiers only.

To also export enabled namespaces as OTLP logs, set
`CAO_OTEL_LOGS_ENABLED=true` and configure `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT`
or `OTEL_EXPORTER_OTLP_ENDPOINT`. `DEBUG` still selects the same namespaces
for both stderr and OTLP; setting an endpoint alone does not enable log export.
The exporter batches records and flushes at shutdown. `slog` error records
retain their severity and active trace/span context. Only enable reviewed,
non-sensitive namespaces when exporting to an external collector.

For a local OTLP/HTTP collector, export only server debug logs without enabling
trace or metric export:

```bash
export DEBUG=cao:server
export CAO_OTEL_LOGS_ENABLED=true
export OTEL_EXPORTER_OTLP_LOGS_ENDPOINT=http://127.0.0.1:4318/v1/logs
npm run dashboard:server:serve
```

The log-specific endpoint is a complete URL, including the collector's path.
The shared `OTEL_EXPORTER_OTLP_ENDPOINT` instead appends `/v1/logs` and also
enables trace and metric export. If both are set, the log-specific endpoint
wins. Configure collector authentication through secret-managed
`OTEL_EXPORTER_OTLP_LOGS_HEADERS` (or shared `OTEL_EXPORTER_OTLP_HEADERS`);
log-specific headers take priority. `OTEL_SDK_DISABLED=true` disables export
without disabling stderr logs.

> [!IMPORTANT]
> The default `serve` command remains local-only: it uses a local bearer
> capability and intentionally rejects non-loopback listen addresses. Remote
> Hosted use requires the explicit `serve-hosted` or Azure Functions profile, which
> replaces the local capability with GitHub OAuth, refresh-token-backed
> server-side sessions, explicit GitHub organization/team authorization, and an
> Azure trusted-proxy policy. PATs are not supported. The Azure Functions
> profile is experimental: deploy it only after security review, staging
> validation, and rollback planning for your Azure tenant.

## Local Actions and OIDC simulator

Run the credential-free, localhost-only Actions/admission/webhook integration:

```bash
npm run test:integration:actions-simulator
# Equivalent without Node/npm (also suitable for a GitHub Actions run step):
go -C server test -race ./internal/simulator ./internal/admission \
  ./internal/webhookgateway ./cmd/cao-webhook-gateway \
  -run '^(TestLocalActions|TestSpecLocalHost|TestGatewayAPI|TestIssueServiceSimulator($|HTTP|GatewaySQLite)|TestGitHubSimulator(Gateway|Discovery)SQLite)' \
  -count=1 -timeout=2m -v
```

The tests start real HTTP listeners on ephemeral numeric-loopback ports, use a
private temporary SQLite gateway queue, and close every listener and database
on completion. No GitHub credentials, `id-token: write`, Docker, PostgreSQL,
Redis, public callback URL, or live GitHub API is required. Go dependencies
must already be available or downloadable, as for the ordinary Go test suite.
The Go CI `server-quality` job runs the same command in the explicitly named
Local Actions, OIDC, admission, and webhook gateway integration step on pull
requests and pushes. Its permissions remain `contents: read`, with no service
containers or credentials required by this integration.

The reusable implementation is in `internal/simulator/`:

- `NewActionsAPI` composes the existing scenario-backed mini GitHub REST API
  and an `OIDCBroker` on one listener without changing REST-only callers.
- The broker binds an opaque per-run bootstrap credential to immutable claims,
  exposes the Actions-style `/token?audience=...` response and public
  `/.well-known/jwks`, and issues five-minute RS256 tokens from an ephemeral key.
  Only numeric-loopback callers are accepted. HTTP callers cannot choose
  repository, workflow, actor, or run claims. `Actions.Environment()` supplies
  the two `ACTIONS_ID_TOKEN_REQUEST_*` variables for local OIDC clients; the map
  contains a credential and must never be printed.
- `Actions.Run` requests admission, validates the primary credit allowance,
  runs a supplied local job callback, then posts `conclusion_admission` using a
  fresh token and the **same admission audience**. Rejected admission never
  executes the callback. Failed admitted work still attempts conclusion.
  `Actions.Post` and `DeliverWebhook` support explicit retries and signed
  webhook replays. Clients reject non-loopback destinations, disable proxies,
  and do not follow redirects or silently retry requests.
- `Trace.Events()` returns ordered stage/status events without tokens, claims,
  bodies, secrets, or URLs. Traces cap at 10,000 events with an explicit
  `trace.truncated` marker. Inject a fixed broker clock and use fixed run and
  delivery identities for repeatable scenarios. Fresh cryptographic keys and
  bootstrap credentials are intentionally random; they are not trace output.

The integration runs the **production admission and conclusion HTTP handlers**
and verifier, including real HTTP JWKS retrieval. Only a test-owned transport
maps the verifier's fixed GitHub issuer JWKS URL to the local broker. Production
issuer trust and constructors are unchanged; simulator keys never become
production authority. Policy, admission-decision, and conclusion-receipt stores
are isolated in-memory fixtures, not a substitute for PostgreSQL durability tests.

An admitted job posts signed events to a real local webhook gateway ingress;
the real SQLite queue and worker reject a bad signature, deduplicate replay,
and finish exactly one simulated dispatch. The lifecycle trace is printed by
the verbose test runner. Failure coverage includes expired/future tokens,
incorrect audience/signature, malformed tokens, JWKS outage, failed bootstrap
exchange, request/claim mismatch, current-policy denial, invalid budgets,
redirects, and failing job callbacks. This is a protocol/job simulator, not a
general workflow-YAML executor or a claim that upstream gh-aw JavaScript ran.
For real PostgreSQL persistence, point the integration at a **disposable local**
database owned by the test user (numeric loopback or a Unix socket only):

```bash
POSTGRES_URL='postgresql://postgres@127.0.0.1:5432/cao_actions_test?sslmode=disable' \
  npm run test:integration:actions-simulator-postgres
```

This variant runs the production admission/conclusion handlers with the real
PostgreSQL decision and receipt store, plus a PostgreSQL-backed webhook queue
and a separately opened dispatch replica. It verifies allowance replay across
reopen, pending-publication repair, completed-receipt deduplication, conflicting
conclusion rejection, persistent admission revocation, and exactly one
successful queued dispatch for duplicate signed deliveries. Canonical publication
is a controlled test sink; this does not exercise the dashboard entity schema.
Each run provisions a random operational namespace and removes it afterward.
Gateway suites also create and remove private PostgreSQL schemas so their
migrations cannot deadlock with admission writes in another test package.
Provisioning requires the database owner's DDL privileges; never point this at
a shared production database. Missing configuration fails the dedicated command
instead of silently skipping; ordinary unit runs skip this variant when
`POSTGRES_URL` is unset.

The Go CI `actions-simulator-postgres` job supplies a health-checked local
Postgres 16 service on a dynamically allocated port and runs this integration
with the race detector on pull requests and pushes. It uses no deployed service,
GitHub credential, or live GitHub API.

### Local Jira, Linear, and Azure DevOps issue webhooks

Run just the self-contained issue-service simulator and gateway integration:

```bash
npm run test:integration:issue-services
```

`simulator.NewIssueService("jira" | "linear" | "azure-devops")` creates an
ephemeral receive secret and synthetic tenant/resource identities.
`Delivery(id, issueNumber, activity, timestamp)` deterministically generates
native opened, edited, closed, reopened, labeled, unlabeled, and deleted issue
payloads; Azure DevOps also supports restored work items. Updates contain native
before/after evidence, including Azure revision IDs distinct from work item IDs.
`DeliverWebhook` signs Jira and Linear HMAC deliveries or supplies Azure service
hook Basic authentication, using the same exact numeric-loopback-only transport
as the Actions simulator. Proxy settings and redirects cannot escape it.

These are webhook protocol simulators, not full issue-service API emulators.
Provider-shaped HTTPS resource URLs are inert payload references and are never
fetched. No Jira, Linear, Azure DevOps, or GitHub account, token, external
callback, service container, or deployed service is required. All fixtures,
listeners, secrets, and SQLite state are test-owned and cleaned up afterward.
The ordinary local Actions integration command and Go CI also run these suites.

The gateway tests discover compiled external declarations from the local GitHub
API simulator, intersect them with independent receiver enrollment, and exercise
the production ingress, durable queue, live destination authorization, worker,
and GitHub compiler dispatcher. They verify every supported lifecycle activity,
provider-isolated batching, canonical resource URLs and filter evidence, native
authentication failures, tenant/collection/resource mismatches, Linear timestamp
freshness, replay deduplication across restart, minimal persisted/dispatched
payloads, and cancellation when the discovered workflow changes.
The PostgreSQL command above runs the same gateway suite against isolated local
PostgreSQL state; it is optional for the fully self-contained SQLite command.

### Discovered agentic workflows, webhooks, and dispatch

The same commands also run shared gateway suites for SQLite and PostgreSQL.
`NewGatewayAPI` provides an opt-in private `simulator/repo-00001` fixture with
separate receiving and dispatch installations, repository-scoped token mints,
numeric repository lookups, default-branch refs, commit-pinned workflow listings
and raw lock-file reads, and observable repository-dispatch POSTs. `Publish`
advances the synthetic default branch without mutating earlier commits. Fixture
storage is bounded to eight revisions of 16 MiB each and 128 dispatches below
64 KiB each. Observations contain synthetic dispatch data, not credentials;
App JWT signatures are deliberately not verified.

The discovery suites fetch compiled `gh-aw-manifest` headers through the
production GitHub client, apply reviewed discovery policy, receive signed
security webhooks, and run the production live-authority checks and compiler
dispatcher over loopback HTTP. PostgreSQL also exercises the production
discovery worker and durable registry across queue reopen; SQLite has no hosted
registry and keeps its discovered inventory in the test while using the same
HTTP discovery client and policy compiler. No simulator metadata grants
production authority.

Assertions cover batched minimal payloads, delivery IDs, API-version pinning,
replay deduplication after restart, stale workflow revocation, discovery API
outages, and non-replay of ambiguous dispatch server errors. Faults use the
existing local `FaultProxy`, rather than replacing dispatcher outcomes. The
separate queue suites cover concurrent replicas, capacity and database outages,
policy disablement, and bounded retries. These tests do not execute workflow
YAML or prove that a real GitHub Actions run started.

The separately pinned upstream client conformance tests remain available via
`GH_AW_ADMISSION_PROTOCOL_PATH` and `GH_AW_CONCLUSION_RUNTIME_DIR`.

### Docker samples runtime

Run the small, test-only workflow executor and its signed-webhook integration:

```bash
npm ci
docker pull node:24-bookworm
npm run test:integration:actions-runtime
```

Prerequisites are Node.js 24+, Go at the version in `server/go.mod`, the pinned
`gh aw` compiler, Git, curl, tar, and a local Docker daemon supporting bind mounts.
The first run downloads the immutable `github/gh-aw-actions` setup revision
declared by the compiled lock and a checksum-verified jq binary for the image's
architecture. Downloads are cached under `.tmp/actions-runtime/`. No GitHub
credentials, inference subscription, PostgreSQL, or Redis are required.

`tests/helpers/actions-runtime/` is intentionally **not an alternative to act
or a general Actions framework**. It accepts only gh-aw locks compiled with
`--use-samples --dry-run`, with threat detection disabled. The fixtures opt into
`features.samples` and declare `safe-outputs.threat-detection: false`. Compilation
uses a fresh credential-free Git configuration and a strict `gh run list`
fixture returning no historical runs, so the compiler's mandatory dry-run model
check does not contact GitHub. A preliminary compilation establishes the
reviewed shared-control secret-reference baseline; only the subsequent dry-run
locks are committed into the synthetic revision and executed.

The executor implements sequential dependency-ordered jobs, the small expression
subset those locks use, Bash steps, conditions and failure propagation,
`GITHUB_OUTPUT`/`GITHUB_ENV`/`GITHUB_PATH`, pinned setup and github-script actions,
fixture checkout, and local artifact upload/download (literal names and brace
lists only). Each job gets a fresh workspace, home and runner directory.
The **real upstream setup, activation, sample replay, stdio safe-output MCP
server, ingestion, staged safe-output handlers and conclusion scripts execute**.
CAO uses the real shared control import, exact-revision policy reads, admission,
precompute, and its jq handoff checks. Unknown actions, expression features,
GitHub API requests, matrix/service/container/reusable jobs and live/inference
locks fail explicitly rather than being treated as successful.

Seven sample-only infrastructure adapters are reported in `report.simulated`:
Copilot installation, AWF installation, image download, network MCP gateway
start/stop, MCP CLI mounting, and online compiler-version checks. These are not
coverage of inference, firewall isolation, network MCP transport, hosted
OIDC, or live GitHub writes. Samples use the real stdio MCP server instead.
The runner's loopback-only GitHub API supplies bounded fixture reads and
rejects every mutation. Docker has no network, Docker socket, host credentials,
privileges, or writable host checkout; only the explicit results directory is
writable, with bounded memory, CPUs, PIDs and temporary storage. This runs
trusted test fixtures, not arbitrary untrusted workflows.
The container uses the invoking host user's UID and GID so the results bind
mount remains writable on Linux without restoring capabilities or broadening
directory permissions. The report records that identity, and integration
assertions verify it.

The integration first uses production discovery, signed ingress, SQLite queue
and authenticated GitHub HTTP dispatch against `NewGatewayAPI`. It rejects a
bad signature and proves replay deduplication across queue reopen, then feeds
the exact accepted compiler batch envelope to both compiled AWI and CAO fixtures.
The webhook declaration comes from the existing versioned compiler-manifest
conformance fixture: this does not claim that the pinned compiler generated
native webhook declarations. Assertions require every workflow job to run,
exact sample output, `dry_run` and staged evidence, zero GitHub mutations, and
CAO policy denial preventing agent execution.

The standalone `actions-runtime.yml` workflow runs this on pull requests
and main pushes only for changes under `server/`, excluding Markdown files.
Manual dispatch remains available, with `contents: read` only.
Containers are removed even after timeout.
Successful fixture directories are deleted; failures retain per-step logs,
summaries, artifacts and `report.json` under `.tmp/actions-runtime/e2e-*/`.
CI uploads failure diagnostics, not screenshots or generated locks.

## Ingestion reliability simulator

The `simulate-api` and `simulate-webhooks` commands exercise the actual
collection API and webhook admission paths with synthetic repositories. The
scenario at `testdata/ingestion-scenarios/five-hour-outage.json` models 10,000
repositories, a five-hour GitHub API outage, recovery rate limiting, signed
duplicate/replayed deliveries, out-of-order activity, and a delayed delivery
batch. Scenarios are strict JSON; supported traffic distributions are uniform,
hot-repository, long-tail, and synchronized. Repository counts are bounded at
50,000 and generated workflow and issue events combined at one million. Set
`issue_events_per_repository` in a scenario to generate ordered `issues`
`opened`, `closed`, `reopened`, and `edited` webhook deliveries for each synthetic
repository; the default is zero. The optional
`history` object adds deterministic installation/repository pagination and
historical workflow runs to the same GitHub API simulator:

```json
{
  "name": "historical-backfill",
  "repositories": 1000,
  "history": {
    "days": 14,
    "runs_per_day": 1,
    "as_of": "2026-10-01T12:00:00Z"
  },
  "faults": [
    {
      "path": "/repos/simulator/repo-00001/actions/runs",
      "page": 2,
      "count": 1,
      "mode": "service-unavailable"
    }
  ],
  "webhook_retry_limit": 20
}
```

Start a healthy `simulate-api` origin, then start a second `simulate-api`
with `--proxy-upstream http://127.0.0.1:PORT` and a scenario containing
`faults`. The proxy can inject bounded 5xx, authorization, malformed-response,
connection-loss, timeout, missing-quota, primary-limit, and secondary-limit
failures on selected paths/pages. Each fault expires after its declared count.
Historical APIs honor GitHub's inclusive `created=START..END` filter and return
at most 100 rows per page without materializing the complete enterprise corpus.
`webhook_retry_limit` defaults to zero; explicit retries preserve signed
delivery identities, apply only to transient responses/transport loss, and
are reported separately.

Simulation webhook delivery and proxy transports require explicit numeric
loopback HTTP endpoints. They reject hostnames, credentials in URLs, external
redirects, and environment proxies. Never point these tools at live GitHub or
production services.

Start the fake GitHub API in one terminal. `--time-scale 3600` advances one
scenario hour per wall-clock second:

```bash
cd server
go run ./cmd/cao-dashboard simulate-api \
  --scenario testdata/ingestion-scenarios/five-hour-outage.json \
  --listen 127.0.0.1:8081 --time-scale 3600
```

Start the CAO server and Redis using the normal collection setup, setting
`CAO_COLLECT_GITHUB_API_URL` (and, when needed,
`CAO_COLLECT_GITHUB_UPLOAD_URL`) to `http://127.0.0.1:8081`. Then deliver the
webhooks to its normal `/api/github/webhook` endpoint:

```bash
go run ./cmd/cao-dashboard simulate-webhooks \
  --scenario testdata/ingestion-scenarios/five-hour-outage.json \
  --endpoint http://127.0.0.1:8080/api/github/webhook
```

The simulator reads the signing secret from `CAO_GITHUB_WEBHOOK_SECRET` and
does not print it. Webhook installation and repository-add events are sent
before workflow events so admission is correctly scoped; repository removals
are sent after activity. The fake API implements the small REST surface needed
for app validation, installation tokens, rate-limit inspection, repositories,
and workflow runs, and returns GitHub-style transient errors and rate-limit
headers for configured windows. The collection runner passes the configured
API base URL to `gh aw logs --audit`; collection, queueing, retries, and
projection remain on the production path.

`go test ./internal/server ./internal/simulator` includes an end-to-end signed
webhook test through the production HTTP handler and admission-only collection
queue. It uses the server test Redis protocol fixture so it runs without an
external service. To run that test against a local Redis instance instead,
start Redis with `npm run dashboard:server:redis-up` from the repository root
and set `CAO_SIMULATOR_REDIS_URL=redis://127.0.0.1:6379/0` before running
`go test ./internal/server -run TestSimulatorUsesProductionWebhookAdmissionAndCollectionQueue`.
`TestSimulatorExercisesGoServerWithRedis` sends a deterministic recovery-burst
scenario through the same Go server webhook handler and checks its authorized
ingestion-health response, queue depth, and persisted counters. It requires a
real Redis instance; run it with:

```bash
CAO_SIMULATOR_REDIS_URL=redis://127.0.0.1:6379/0 \
  go test ./internal/server -run TestSimulatorExercisesGoServerWithRedis
```

The Redis-backed CI job runs this integration test as part of `go test ./...`.
For manual load runs, point the normal CAO server and worker at a local Redis
instance as well as the simulator API.

## Rate-limit stress harness

`internal/server/stress_test.go` drives the production HTTP handler (access
control, inbound rate limiting, and the lazy repository-memory resolver) with
real Redis and the simulator's fake GitHub API. It configures deliberately low
limits so every run exhausts them:

- **Inbound**: `Config.RateLimits` lowers the general policy to 50 requests per
  second and floods one endpoint. Admission must never exceed the token bucket,
  and every rejection must be a `429` with `Retry-After` and `RateLimit-*`
  headers.
- **GitHub primary limit**: the scenario `rate_limit` field (for example,
  `{"limit": 40, "window": "3s"}`) meters the fake API with a fixed-window
  budget and GitHub-style `X-RateLimit-*` headers. Cache-missing reads must
  stop at the governor floor so the simulator never rejects a request, and
  reads must resume after the window resets.
- **GitHub secondary limit**: a `secondary-rate-limit` window rejects the
  installation-token mint and, separately, REST calls behind a minted token.
  The server must park the installation, answer `429` with GitHub's
  `Retry-After`, and send no more than the in-flight requests upstream.

Each scenario also enforces a p95 latency budget and a throughput floor and
writes a JSON report. `BenchmarkStress*` benchmarks measure the throttled,
cached, and governor-exhausted paths with `-benchmem`. The tests skip unless
`CAO_STRESS_REDIS_URL` is set; use a disposable Redis database:

```bash
npm run dashboard:server:redis-up   # from the repository root
cd server
mkdir -p ../.tmp/go-stress
CAO_STRESS_REDIS_URL=redis://127.0.0.1:6379/0 \
CAO_STRESS_REPORT_DIR=../.tmp/go-stress \
  go test ./internal/server -run '^TestStress' -bench '^BenchmarkStress' \
    -benchmem -benchtime=2000x -count=1 \
    -cpuprofile=cpu.pprof -memprofile=mem.pprof \
    -blockprofile=block.pprof -mutexprofile=mutex.pprof -trace=trace.out \
    -outputdir=../.tmp/go-stress
go tool pprof -top ../.tmp/go-stress/cpu.pprof
```

`CAO_STRESS_MAX_P95_MS` (default 250) and `CAO_STRESS_MIN_RPS` (default 200)
override the thresholds. The `Go rate-limit stress and profiles` job in
`.github/workflows/cgo.yml` runs the harness in its own job and uploads the
`go-server-stress-profiles` artifact: per-scenario JSON reports, raw CPU,
allocation, block, and mutex profiles, an execution trace, `pprof -top`
summaries, and benchstat-compatible `bench.txt`. Compare `bench.txt` files
with `benchstat` and inspect profiles with `go tool pprof` or
`go tool trace` when optimizing the server.

## Architecture

```mermaid
flowchart LR
  Artifact["Deployed dashboard artifact<br/>inventory + run JSONL + record JSONL"]
  Ingest["Go ingester<br/>verify, parse, project"]
  Postgres["Postgres<br/>native entity tables"]
  Redis["Redis<br/>operational state"]
  API["Go HTTP(S) server<br/>bounded PostgreSQL SQL plans"]
  Browser["Dashboard browser app<br/>render bounded view payloads"]

  Artifact --> Ingest
  Ingest -->|"transactional replacement"| Postgres
  Postgres --> API
  Browser -->|"POST /api/v1/query"| API
  API -->|"source reads"| Postgres
  API -->|"sessions, queues, caches"| Redis
  API -->|"LogicalSourceInput JSON"| Browser
  API -->|"SSE revision events"| Browser
```

### Components

| Component | Location | Responsibility |
| --- | --- | --- |
| CLI | `cmd/cao-dashboard/` | Implements the `ingest` and `serve` commands and keeps Postgres and Redis configuration in the server process. |
| Artifact ingestion | `internal/ingest/` | Validates deployed manifests and hashes, streams run shards before record shards, and writes canonical rows through the native Postgres writer. |
| Query engine | `internal/query/` | Validates Dashboard Language definitions and compiles hosted query plans to bounded SQL. |
| Postgres entity storage | `internal/postgresx/` | Initializes fresh TypeSpec-generated tables and transactionally replaces native entities, quality metadata, diagnostics, and revision; executes query plans in repeatable-read snapshots. |
| Redis operations | `internal/redisx/` | Supports caches, queues, and sessions. |
| HTTP(S)/API server | `internal/server/` | Enforces loopback binding, optionally terminates operator-configured TLS, serves static dashboard assets, handles API requests, and publishes revision events. |
| Externally hosted service | `hosting/` | Exposes a listener-independent application lifecycle and the complete hosted HTTP handler to other Go HTTP hosts. |
| Azure Functions profile | `internal/server/azure.go` | Builds the same HTTP handler without starting a listener, validates Azure app settings, requires `rediss://` Redis, and trusts forwarded host/protocol headers only for configured Azure hosts. |
| GitHub OAuth sessions | `internal/server/oauth.go` | Implements the GitHub OAuth authorization-code flow, active organization/team authorization, refresh-token rotation, server-side encrypted sessions in Redis, logout revocation, and CSRF protection for mutating requests. |
| Shared API model | `internal/model/` | Defines logical sources, diagnostics, and query metrics. |
| Telemetry | `internal/telemetry/` | Configures OpenTelemetry trace, metric, and opt-in log providers, exposes the server's tracer, and writes W3C trace/span id response headers. |
| Local Redis | `docker-compose.yml` | Runs plain Redis on `127.0.0.1:6379`. |
| Container image | `Dockerfile` | Builds the dashboard and Go service into a non-root image for host-neutral deployments. |

### Host capability profiles

`internal/server/host_modules.go` independently resolves an app server target
module and a declarative Redis provider module. Their capabilities compose into
the profile validated by `internal/server/host_profile.go`. Target modules own
authentication, listener, HTTPS, and proxy behavior; Redis modules own
environment mappings, connection semantics, namespace isolation, replica
constraints, and collection support.

Hosted deployments normally declare these capabilities under
`control-plane.web.host` in `.github/workflows/cao.json`. The declaration names
environment variables for Redis connection and verified TLS inputs; secret
values remain in the deployment environment. New hosting platforms add a target
module without changing Redis providers; new Redis providers add declarative
environment defaults without changing target modules. `server.New` validates the
composed profile.
Do not add provider-name branches to shared authentication, proxy, ingestion,
or Redis enforcement. Startup rejects unsupported capability combinations, a
serialized profile backed by a pooled Redis client, collection on an
artifact-only profile, and process-listener settings on a platform-listener
profile.

The `generic` target also accepts `listener: external` for a Go host that owns
the HTTP server instead of CAO's `Serve` command. Use the public `hosting.New`
constructor with explicit paths to the built site and query documents, call
`Start(ctx)` before serving `Handler()`, then call `Drain()` to stop admitting
new requests and end SSE streams. Call the host's `http.Server.Shutdown` to
drain ordinary requests, then `Stop(ctx)` to cancel and await CAO background
tasks and close the hosted PostgreSQL pool. Pass a context with the host's
shutdown deadline; if it expires before tasks exit, the pool remains open and
`Stop` may be retried with a fresh context. Databases supplied to `server.New`
remain caller-owned. Keep the startup
context alive through HTTP shutdown. `Handler()`
includes all CAO authentication, trusted-host, CSRF, rate-limit, webhook, and
telemetry middleware; it returns `503` before startup or after drain.
There is no alternate raw router or automatic trust of an embedding host's
identity headers. The existing process-owned `serve-hosted` and Azure handlers
do not select this mode.

SSE streams share one PostgreSQL revision observation per replica while any
clients are connected. Authorized streams also share the Redis ingestion-health
observation. Each replica polls the durable state so updates originating on
another replica are delivered even without a local notification; local updates
are broadcast immediately. `Drain` still ends active streams.

`CAO_SSE_MEASURE=1 POSTGRES_URL=... go test ./internal/server -run
^TestSSEFanoutMeasurements$ -count=1 -v` measures connections, PostgreSQL
committed transactions per second (an approximate query-rate proxy), Go heap,
and stream drain against disposable PostgreSQL. On one local PostgreSQL instance
with a simulated Redis server, pre-change measurements for 1/12 clients on one
replica were 4/6 connections and 2.8/16.2 transactions per second; for 2/24
clients on two replicas, 7/8 connections and 5.0/28.4 transactions per second.
After shared observation, the same scenarios measured 4/5 connections and
3.6/5.8 transactions per second, and 7/8 connections and 5.6/9.4 transactions
per second. Heap after the 12-client and 24-client sampling windows was
2,615,304/3,364,256 bytes before and 2,102,776/2,840,400 bytes after.
Active-stream drain completed in 394/656 microseconds for 12/24 clients after
the change (the original harness only timed drain after closing its clients).
These are local observations, not performance budgets.
Generic OAuth targets cannot disable HTTPS in policy; Azure's explicit local
simulation is a separate platform-only case.

The external host must supply TLS or a private, explicitly trusted proxy
boundary, preserve request `Host`, direct peer address, TLS state, and
`http.Flusher`/response deadline control, and keep a long-lived
`/api/v1/events` stream outside finite whole-request timeouts. CAO's built-in
listener removes its ordinary write deadline only for that streaming route;
other routes retain bounded HTTP timeouts. A host's global request timeout
cannot be repaired by CAO after it cancels the request context. Use a dedicated
hostname: the browser's OAuth, API, and static paths are rooted at `/`.
Keep separate limits for concurrent streams, connections, and overall host
memory in the embedding deployment; CAO's Redis token buckets bound request
arrival, not the lifetime of an accepted stream.

## Hosted service profile

The configured `--site` directory must contain a readable, non-empty built
`index.html`; hosted startup rejects an API-only or unbuilt deployment. The
container supplies its bundled UI at `/app/site`. Source-built deployments
must build `dashboard/site/` first and pass that build directory when using a
different location. This requirement also applies to Azure Functions and the
`server/hosting` external-listener facade.

The Go handler injects `dashboard-data-backend=server-http`,
`dashboard-storage-backend=postgres`, and the GitHub authentication bootstrap
into the dashboard HTML. Those session-specific responses use `Cache-Control:
no-store`. The shared browser UI selects the same-origin query API and disables
static-dashboard PWA state; hosted queries do not ingest static shards or run
against IndexedDB. PostgreSQL or API failures remain visible rather than
switching the browser to the Pages data path.

`serve-hosted` runs the same stateless Go service on a container, VM,
Kubernetes workload, or comparable host. It defaults to
`127.0.0.1:8080`, where a same-host or same-pod HTTPS proxy may forward requests.
A non-loopback listener is accepted only when `--cert` and `--key` configure
TLS at the CAO service itself. It is not coupled to a Redis provider or cloud
SDK. Set `CAO_POLICY_PATH` when loading a reviewed deployment-specific host
extension or when the policy is mounted somewhere other than
`.github/workflows/cao.json`. See
[`docs/deployment-managed-redis.md`](../docs/deployment-managed-redis.md) for
the generic `REDIS_URL` and TLS contract and provider modules.

Every hosted process requires `control-plane.web.host` in the composed policy. Redis
provider selection, TLS behavior, environment-variable names, namespace
selection, connection semantics, and replica count come only from that policy.
The selected environment variables hold secret values; they do not select or
override host behavior. Other hosted service inputs remain process environment
settings:

| Variable | Purpose |
| --- | --- |
| `CAO_ALLOWED_HOSTS` | Required comma-separated trusted public host names. |
| `CAO_TRUSTED_PROXY_CIDRS` | Required when a non-loopback `serve-hosted` listener relies on a TLS proxy. Only private CIDRs are accepted, and forwarded headers are rejected unless the direct peer is in one of them. |
| `CAO_GITHUB_CLIENT_ID`, `CAO_GITHUB_CLIENT_SECRET`, `CAO_GITHUB_REDIRECT_URL` | GitHub OAuth application. |
| `CAO_SESSION_SECRET` | Current session encryption/signing secret of at least 32 characters. |
| `CAO_SESSION_SECRET_PREVIOUS` | Optional previous session secret retained only during controlled rotation. |
| `CAO_GITHUB_ALLOWED_ORGS`, `CAO_GITHUB_ALLOWED_TEAMS` | Explicit authorization policy. |
| `CAO_GITHUB_LOCAL_USER` | Optional exact developer login for [single-user local OAuth](#local-oauth-without-organization-access); removes organization scopes/checks only on a direct loopback HTTPS listener. Cannot be combined with organization/team/SSO policy. |
| `CAO_GITHUB_ADMIN_USERS` | Required comma-separated GitHub logins allowed to trigger rebuilds. |
| `CAO_GITHUB_WEBHOOK_SECRET` | Required GitHub webhook signature secret of at least 32 characters. |
| `CAO_SOURCE_DIRECTORY` | Required authoritative deployed gh-aw artifact directory used by rebuild/reconciliation. |

Hosted HTTPS enforcement cannot be disabled. Forwarded host and protocol
headers are trusted only when the service is bound to loopback; externally
reachable listeners validate their direct TLS connection and `Host` header.
Supply secrets through the deployment platform's secret manager (for example,
Key Vault references, Kubernetes Secrets mounted into the process environment,
or an equivalent managed facility), never command-line arguments or checked-in
configuration.

### Administrator step-up

Every hosted administrator operation requires a fresh, assured interactive
OAuth authentication. This covers `/mcp`, collection, quota, operations, and
hosted CLI routes. Administrators re-authenticate through `/auth/login`.

| Variable | Purpose |
| --- | --- |
| `CAO_ADMIN_REAUTH_MAX_AGE` | Maximum age of the interactive authentication. The default is `15m`, and the value must be between `1m` and `1h`. |
| `CAO_GITHUB_SSO_ORGANIZATIONS` | Comma-separated organizations that the operator attests enforce SAML SSO. A successful membership read with the fresh token counts as assurance. |

Without SSO attestation, GitHub must explicitly report two-factor
authentication for the user. A stale or unassured administrator receives `401`
with a `CAO-Step-Up` challenge. Everyone else receives `403`. See
[`SECURITY.md`](SECURITY.md#administrator-step-up).

#### OAuth permissions and onboarding

Classic OAuth Apps request `read:org read:user`: membership access plus the
least user scope that lets `/user` disclose `two_factor_authentication`.
GitHub omits that private field from a `read:org`-only token. After upgrading
from that grant, sign out and complete `/auth/login` again, consenting to the
new scope. Refreshing an existing token or revalidating membership cannot add
scopes or establish new step-up evidence. If consent omits `read:user`, or
GitHub reports 2FA disabled, non-SSO administrator access still fails closed.

GitHub App clients (`Iv...`) instead request only `offline_access`; configure
organization **Members: read** and approved installations in each allowed
organization. OAuth scopes do not grant App permissions. Do not assume a
GitHub App user token discloses 2FA: if the field is absent, administrator
access requires a successfully verified membership in an operator-attested
`CAO_GITHUB_SSO_ORGANIZATIONS` organization. Otherwise that configuration
cannot satisfy administrator step-up.

Neither `read:user` nor `prompt=select_account` forces an MFA challenge.
Step-up records recent OAuth completion with 2FA enabled or attested SSO
access, not proof that the user just performed MFA.

### Local OAuth without organization access

For dashboard-only development, `cao-dashboard serve` already uses a local
bearer capability and does not require GitHub OAuth or organization access.
To exercise the OAuth and hosted CLI paths locally with a personal GitHub
account, opt into **single-user development OAuth** with
`CAO_GITHUB_LOCAL_USER`. Classic OAuth requests only `read:user`, and login,
refresh, and revalidation check that exact GitHub login instead of reading
organization or team membership. GitHub App clients retain `offline_access`.

From `server/`, with the ordinary database, reviewed host policy, OAuth App
credentials, session secret, webhook secret, source/site, and trusted local
TLS certificate already configured:

```bash
export CAO_GITHUB_LOCAL_USER=YOUR_GITHUB_LOGIN
export CAO_GITHUB_ADMIN_USERS="$CAO_GITHUB_LOCAL_USER"
export CAO_ALLOWED_HOSTS=127.0.0.1
export CAO_GITHUB_REDIRECT_URL=https://127.0.0.1:8443/auth/callback
unset CAO_GITHUB_ALLOWED_ORGS CAO_GITHUB_ALLOWED_TEAMS
unset CAO_GITHUB_SSO_ORGANIZATIONS CAO_TRUSTED_PROXY_CIDRS
go run ./cmd/cao-dashboard serve-hosted --listen 127.0.0.1:8443 \
  --cert ./localhost.pem --key ./localhost-key.pem
```

Register that exact callback on a development OAuth App and use a certificate
trusted by your browser for the chosen loopback host. `localhost` is also
supported when the listener, allowed host, and callback all use it. Startup
rejects public/private-network listeners, nonmatching callbacks, reverse
proxies, CORS, external/platform listeners, and mixed organization/team/SSO
authorization. Requests must arrive directly from loopback over TLS. Do not
expose this development server through a tunnel or reverse proxy. Production
hosted authorization is unchanged when `CAO_GITHUB_LOCAL_USER` is unset.

Only the configured developer can sign in; this is not anonymous access.
Secure cookies, CSRF, administrator allowlisting, 2FA evidence, and live
target-repository writer checks remain required for their respective
operations. To test hosted CLI, keep both `--experimental` and
`--hosted-cli-actions`, then use `/auth/login?hosted_cli=1` for explicit
classic `read:user repo` consent. No `read:org` is requested. A GitHub App
that does not disclose 2FA cannot satisfy administrator assurance in this
no-SSO development mode; use a classic development OAuth App for that path.

Changing between development and ordinary hosted mode, or changing the
configured developer, requires a new sign-in: existing sessions cannot cross
those authorization boundaries. Use a separate development operational
namespace and secrets. Removing a mode or scope does not revoke previously
issued GitHub grants; revoke the development App authorization when finished.

### Hosted CLI actions

Hosted CLI actions are an experimental feature and are off by default. To
enable them, run the web server with both `--experimental` and
`--hosted-cli-actions` (and optionally `--cli-actions-catalog`), plus the
PostgreSQL operational store and OAuth. Then run a separate executor:

```bash
gh cao setup auth hosted-cli --repo OWNER/CONTROL \
  --target-repository OWNER/TARGET --private-key-out ./hosted-cli.pem
cao-dashboard serve-hosted --experimental --hosted-cli-actions ...
cao-dashboard execute-cli --max-pooled-tokens 8
```

Ordinary hosted **classic OAuth App** sign-in requests only
`read:org read:user`, even when hosted CLI is enabled and even for
administrators; the single-user local mode above omits `read:org`.
To authorize private-target probes, first sign in normally,
then open `/auth/login?hosted_cli=1` while signed in as a configured
`CAO_GITHUB_ADMIN_USERS` administrator and consent to the additional `repo`
scope. This explicit flow is unavailable to anonymous users and viewers,
and when hosted CLI is disabled. Sessions created before account-ID binding
was introduced must complete ordinary sign-in again first.

The signed, HttpOnly state cookie binds consent to the initiating session,
GitHub numeric account ID, login, and a ten-minute expiry. Keep the same
account selected in GitHub's account chooser. The callback rejects switched
accounts (including another allowlisted administrator), missing/replaced
sessions, removed administrator access, disabled hosted CLI, and missing
factor evidence; rejected issued credentials are revoked or queued for
revocation. A query flag alone grants no authority. Private-target probes
remain denied until the user's token actually has the required access.

GitHub's classic `repo` scope is broad read/write access to the consenting
administrator's accessible public and private repositories, not a metadata-only
or selected-repository grant. Prefer a separate **GitHub App for browser sign-in** to
avoid that broad grant: retain **Members: read**, approve its installation
for the selected private targets with **Metadata: read**, and ensure its
user token's live repository response reports the requester's `admin`,
`maintain`, or `push` permission. An App installation alone never establishes
the user's write authority. OAuth App access restrictions and SSO approval
must also permit the token to access the organization.

The server only reads repository permissions with the browser user's token;
execution still uses the separately scoped hosted CLI App installation token.
Missing repository access or permission fields fail closed without falling
back to that executor token. `offline_access` remains unchanged for GitHub
App sign-ins and grants no repository permissions. Disabling the feature
blocks new administrator consent but does not revoke previously granted GitHub scopes;
revoke the OAuth App authorization in GitHub when removing that access.

In production, all web and executor replicas must share the same PostgreSQL
operational namespace. Web replicas can admit work concurrently; executor
replicas coordinate through the durable, fenced queue lease, and a persisted
execution claim prevents another replica from claiming an already-running
command. The configured concurrency limit applies across executor replicas.
Command deadlines and queue state survive process restarts.

Queued commands retain the requester's numeric GitHub account ID as well as
the admission login. Before prefetching or handing out any work token, the
executor requires the live collaborator-permission response to identify that
same account with write/admin access. The login remains attribution, not
identity proof: a reused login, absent provider ID, inaccessible renamed login,
or revoked writer fails closed.

For an upgrade from login-only queue records, pause admissions, stop and
reconcile old executors, and upgrade every web and executor replica together
before resuming. The invocation JSON is additive; there is no SQL schema or
namespace-version migration. Do not backfill IDs by looking up current logins.
Legacy pending commands without IDs remain readable and become explicit failed
commands without starting work or minting credentials. Review them and, if still
intended, sign in and explicitly retry through fresh admission. The retry
records the current requester ID and ancestry without rewriting the original
attribution. Normal terminal retention still applies; this is not authorization
to delete pending work or deploy a production migration.

If an executor crashes or loses its lease while a subprocess may still be
running, the replacement marks the outcome interrupted and blocks new claims.
It will not replay the command automatically because external side effects may
already have occurred. After confirming that the old subprocess has stopped,
an operator must explicitly acknowledge recovery before queue processing
resumes. This provides at-most-once dispatch, not exactly-once external effects.

The executor plans commands in Go and runs only `gh` or the native Go `gh-aw`
CLI inside the digest-pinned `ghcr.io/github/gh-aw` container. Campaign-package
and standalone-workflow source update actions are translated to `gh-aw update`;
the server never invokes
repository shell scripts or checks out and executes control-repository code. The executor
requires a local Docker daemon using its `default` context. Each container has a
read-only root filesystem, no added capabilities, and only its per-command
workspace mounted writable. A temporary mode-`0600` environment file passes the
scoped token; the executor's home, Docker socket, App key, and database
credentials are not mounted or passed into the container. A fixed in-container
bootstrap initializes the private Git root required by `gh-aw update/upgrade
--repo`, then execs the planned CLI with unchanged positional arguments.
Cancellation, deadlines, and lease loss attempt bounded forced removal.
The container name and original Docker daemon identity are stored before
creation. Docker CLI exit is not workload exit evidence: failed confirmation
retains the execution claim and workspace, preventing reuse of that capacity.
Recovery acknowledgement requires the old executor and its Docker requests to
have stopped and successful reconciliation on the original daemon. Remove any
retained workspace only after reconciliation; never switch Docker contexts to
bypass recovery.

`ContainerRunner` delegates runtime identity, creation, startup, inspection,
stopping, and output collection to `ContainerLifecycle`. Docker remains the
production adapter: it starts detached, then replays and follows logs while
workload exit evidence remains a separate inspection. SQLite-only local
development may explicitly use Apple container or Windows Sandbox as described
below. These profiles do not enable Podman or managed jobs, change retry policy,
or promise exactly-once effects. Idempotent updates may overlap within the
configured concurrency limit; workflow dispatch does not inherit that
idempotency assumption.

`gh cao setup auth hosted-cli` runs natively in Go without Node.js or an installed
control checkout. It preserves explicit same-owner targets, GitHub browser approval,
and selected-repository installation verification. It does not activate hosted
execution or change rollout policy. `--dry-run` previews the manifest offline;
`--no-open` prints browser URLs. The command writes the downloaded key to a temporary local
bootstrap file; that file is not a vault. Import it directly into the secret
manager used by the `execute-cli` deployment, then securely remove the local
copy. At runtime, expose the secret only to the executor, preferably as a
secret-manager-mounted file configured with `CAO_HOSTED_CLI_PRIVATE_KEY_FILE`.
Never put the key in checked-in configuration, command arguments, logs, or the
`serve-hosted` process. The Azure Function App deployment does not host
`execute-cli`; do not add this key to its settings.

| Variable | Purpose |
| --- | --- |
| `CAO_HOSTED_CLI_CONTROL_REPOSITORY` | Control repository, given as `OWNER/REPOSITORY`. It falls back to `CAO_COLLECT_CONTROL_REPOSITORY`. |
| `CAO_HOSTED_CLI_APP_ID` | Hosted CLI GitHub App ID (`execute-cli` only). |
| `CAO_HOSTED_CLI_PRIVATE_KEY` (`_FILE`, `_BASE64`) | App private key (`execute-cli` only). |
| `CAO_HOSTED_CLI_GITHUB_API_URL`, `CAO_HOSTED_CLI_GITHUB_URL` | GitHub API and web URLs for GHES. |
| `CAO_HOSTED_CLI_DASHBOARD_URL` | Dashboard URL used in command backlinks. |
| `CAO_HOSTED_CLI_CONTAINER_IMAGE` | Optional digest-pinned gh-aw image or approved mirror; defaults to `ghcr.io/github/gh-aw@sha256:7ff77dc03182d4edcebc902fa94b0239eb9cde65ea212509aef700bcab354090`. |
| `CAO_HOSTED_CLI_DOCKER` | Optional Docker CLI path; defaults to `docker`. |
| `CAO_HOSTED_CLI_CONTAINER_RUNTIME` | `docker` (default), `apple`, or `windows-sandbox`. Native profiles require `execute-cli --sqlite` and the matching OS; there is no automatic fallback. |
| `CAO_HOSTED_CLI_EGRESS_POLICY_FILE` | Required production Docker exact-host HTTPS policy file; see the rollout contract below. No implicit allowed hosts. |
| `CAO_HOSTED_CLI_EGRESS_RELAY` | Required production path to the static Linux relay artifact built from the reviewed server source. |
| `CAO_HOSTED_CLI_EGRESS_RELAY_SHA256` | Required reviewed SHA-256 of that relay artifact. Startup snapshots and verifies the bytes; each create verifies the Linux daemon architecture. |
| `CAO_HOSTED_CLI_APPLE_CONTAINER` | Optional Apple container CLI path for the `apple` development profile; defaults to `container`. |
| `CAO_HOSTED_CLI_WINDOWS_SANDBOX` | Optional Windows Sandbox CLI path for the `windows-sandbox` development profile. |
| `CAO_HOSTED_CLI_WINDOWS_BUNDLE` | Required path to the approved ZIP archive containing Windows gh, gh-aw, and portable Git, including dependencies. |
| `CAO_HOSTED_CLI_WINDOWS_BUNDLE_SHA256` | Required reviewed SHA-256 digest of the Windows tools archive. The Windows profile rejects `CAO_HOSTED_CLI_CONTAINER_IMAGE` rather than silently replacing an OCI image. |
| `CAO_HOSTED_CLI_WORK_ROOT` | Executor workspace root. |
| `CAO_SESSION_SECRET`, `CAO_SESSION_SECRET_PREVIOUS` | Required current encryption secret (at least 32 characters) and optional previous key for the executor's durable token cleanup journal. Keep previous keys until their cleanup debt is cleared or expires. |
| `CAO_AUDIT_LOG` | Optional append-only file (mode `0600`) for the JSON mirror of audit events from `serve-hosted` and `execute-cli`; defaults to standard error. The authoritative trail is the PostgreSQL `cao_operational_audit_events` table. |

#### Production Docker egress rollout

**Migration gate:** production `execute-cli` now refuses startup without all
three `CAO_HOSTED_CLI_EGRESS_*` settings above. Before upgrading, stop admission
or drain the queue, build and approve the relay and policy with the executor
release, and configure every standby replica. Do not disable isolation to
recover an interrupted command. Keep the previous executor stopped and use the
existing original-daemon reconciliation procedure; changing egress configuration
never releases unsettled claims. No policy, campaign, or global feature enablement
is implied by deploying these files.

Each production workload uses Docker **`--network none`**: it has only loopback,
not a bridge interface, default route, or directly usable external DNS. A
per-command mode-`0600` Unix socket in a mode-`0700` directory is the only egress
path. The directory contains only the verified relay and socket, is bind-mounted
read-only at `/cao-egress`, and its writable workspace alias is hidden behind a
read-only tmpfs. The relay makes that socket available as a loopback HTTP proxy
for `gh`, `gh-aw`, and HTTPS Git; it holds no policy or host credentials. Clearing
proxy variables, connecting by IP, using IPv6, UDP, custom DNS, or selecting SSH
does not bypass Docker's network namespace. The executor does not modify global
Docker configuration or shared host networks.

The executor-side proxy permits **CONNECT to exact approved DNS names on port
443 only**, never plaintext HTTP, arbitrary ports, IP-literal authorities, or
wildcards. It validates every resolved address and dials a validated numeric
address without a second DNS lookup. Private, loopback, link-local, multicast,
special-use, transition, and known metadata addresses are denied; IPv4-mapped
IPv6 is normalized before validation. A private GHES exception must pair an
exact hostname with exact RFC1918/ULA addresses; every DNS answer must match an
approved address. This never enables a private subnet or permits known metadata/loopback/link-local
addresses. The guarantee is **CONNECT destination control, not TLS SNI or
application-content inspection**. An allowed GitHub endpoint can still receive
data, and network policy does not grant repository permissions.

Approve only endpoints required by the selected commands. A GitHub.com starting
policy for API, Git, package contents, and release redirects is:

```json
{
  "hosts": [
    "github.com", "api.github.com", "codeload.github.com",
    "raw.githubusercontent.com", "objects.githubusercontent.com",
    "release-assets.githubusercontent.com"
  ]
}
```

For GHES, add its API/web hostname to `hosts` when it resolves publicly, or an
explicit entry such as `"privateEndpoints": [{"host": "github.corp.example",
"addresses": ["10.20.30.40"]}]` for a private deployment. Redirected asset hosts
must be independently approved. HTTPS certificate validation in gh/Git remains
enabled; custom GHES trust must already be in the approved pinned workload
image. Artifact registries used by the Docker daemon to pull that image are not
workload egress. No package-manager or arbitrary HTTP/SSH reach is implied.

Build the authenticated relay artifact **from the reviewed release checkout**
using the existing pinned build image (repository root, choose the daemon's
`linux/amd64` or `linux/arm64` platform):

```bash
docker --context default buildx build --platform linux/amd64 \
  --file server/Dockerfile --target hosted-cli-relay-artifact \
  --build-arg REVISION="$(git rev-parse HEAD)" \
  --output type=local,dest=./.tmp/hosted-cli-relay .
```

The target requires a 40-character source revision and exports `cao-cli-relay`,
`cao-cli-relay.sha256`, and `cao-cli-relay.json` (source revision, platform and
digest). The manifest is build metadata, not an independent attestation.
Bind the artifact
digest, platform, source commit and build provenance to the reviewed executor
release in your deployment artifact store; distribute it through that trusted
release, not an arbitrary binary download. Set the relay path and the approved
checksum separately in deployment configuration. Do not compute the expected
checksum from an untrusted installed file at startup. The loader rejects
digest mismatch, non-ELF/dynamically linked binaries, and unsupported
architectures, snapshots verified bytes to prevent later path substitution,
and refuses a mismatched daemon at command creation. Upgrades require a newly
reviewed digest. The gh-aw image remains separately OCI-digest-pinned.

Run `execute-cli` on the same Linux host/kernel and filesystem as the local
default Docker daemon, with a short `CAO_HOSTED_CLI_WORK_ROOT` (for example a
private directory under `/var/lib/cao`) so Unix socket paths fit the OS limit.
The host must allow execution of the read-only relay bind mount. Docker Desktop
socket forwarding or remote filesystems are not production substitutes; failure
to mount/connect/execute the relay fails the command with no bridge fallback.
The proxy has 32 connection slots per command, 8 KiB configured HTTP headers,
a 5-second header timeout, 10-second DNS/dial limits, 30-second tunnel I/O idle
limits, and a maximum 15-minute lifetime additionally bounded by command
cancellation. Closing the proxy or losing the executor kills tunnels but does
not prove workload termination: the retained identity, claim, token-revocation
obligations, and workspace follow existing bounded stop/recovery rules.

SQLite-only Docker development keeps its previous bridge behavior **only when
all three egress settings are absent**. Supplying any setting opts into the
complete boundary and fails closed if anything is unavailable. Apple and Windows
remain explicit SQLite-only development profiles, not production fallbacks.

Before enabling production admission, run the credential-free real-boundary
test on the intended dedicated Linux executor:

```bash
cd server
CAO_TEST_DOCKER_EGRESS=1 go test ./internal/hostedcli \
  -run '^TestDockerEgressRealBoundary$' -count=1 -v
```

It uses only its own named container/workspace, checks loopback-only interfaces
and direct TCP/UDP/DNS denial, protected relay mounts, approved GitHub HTTPS Git,
gh reaching GitHub's expected unauthenticated `401`, and denied CONNECT targets.
It does not write to GitHub or use real credentials. Missing
`/var/run/docker.sock` is an explicit skip, not successful runtime validation;
do not switch contexts to avoid it. Deterministic policy, proxy and lifecycle
tests run without Docker but are not evidence of the kernel boundary.

#### Native runtimes for local development

Native runtime selection changes only execution isolation. Keep the existing
OAuth/App configuration, durable session-secret configuration, scoped tokens,
and loopback-only SQLite debug server; never substitute a disposable secret or
run the hosted command directly on the host. Native profiles are rejected for
the production PostgreSQL executor.

On **Apple Silicon macOS 26**, install Apple's
[`container` CLI](https://github.com/apple/container) and start its services:

```bash
container system start --enable-kernel-install
CAO_HOSTED_CLI_CONTAINER_RUNTIME=apple \
  cao-dashboard execute-cli --sqlite ./.tmp/hosted-cli.sqlite
```

The adapter targets the verified container 1.2.2 CLI contract. It runs the
existing AMD64 digest-pinned Linux image with Rosetta, using a private VM,
read-only root, no capabilities, in-guest `no-new-privileges`, two CPUs, 1 GiB
memory, a 256-process user limit, and bounded `/tmp`. Only the command workspace
is mapped. It preserves stdout/stderr from startup instead of relying on
`container logs --follow`, which does not signal workload exit. An identity-bound
guest receipt supplies the exit code, but termination additionally requires
the native container to be stopped and its launchd VM service absent.

Native recovery is bound to the service-reported data root and a private
durable identity under that root. Do not delete or replace the native runtime's
data root to bypass recovery. The executor never starts global runtime services
or installs Rosetta automatically. To exercise the installed runtime without
GitHub writes, run from `server/`:

```bash
TMPDIR=/private/tmp CAO_TEST_APPLE_CONTAINER=1 \
  go test -race ./internal/hostedcli -run '^TestAppleContainerNative' -v
```

On **Windows 11 24H2 or later**, enable
[Windows Sandbox](https://learn.microsoft.com/en-us/windows/security/application-security/application-isolation/windows-sandbox/)
on a supported Windows edition and ensure its `wsb` CLI is available. Windows
Sandbox cannot execute the Linux image: this explicit development profile
instead requires an approved ZIP archive of Windows gh, gh-aw, and portable
Git with a reviewed SHA-256 digest. All tool execution stays inside Sandbox,
and the archive is checked both before launch and in the guest.

The ZIP must have these root-relative paths, without an enclosing directory:

```text
gh/gh.exe
gh-aw/gh-aw.exe
git/cmd/git.exe
git/usr/bin/sh.exe
```

Include the matching vendor-relative DLLs, certificates, Git helpers, and
supporting files; the executable architecture must match the Windows host.
The digest covers the entire archive. Limits are 512 MiB compressed and 1 GiB
expanded. The fixed bootstrap runs as System inside the VM; this does not
provide a restricted-user boundary inside the guest.

```powershell
$env:CAO_HOSTED_CLI_CONTAINER_RUNTIME = 'windows-sandbox'
$env:CAO_HOSTED_CLI_WINDOWS_BUNDLE = 'C:\dev\approved-hosted-cli-tools.zip'
$env:CAO_HOSTED_CLI_WINDOWS_BUNDLE_SHA256 = '<reviewed SHA-256>'
$env:CAO_HOSTED_CLI_WORK_ROOT = 'C:\dev\cao-private-work'
cao-dashboard execute-cli --sqlite .\.tmp\hosted-cli.sqlite
```

Windows Sandbox permits only one instance. This profile serializes execution
across local executor namespaces and refuses an unrelated running Sandbox;
it never adopts or terminates the user's session. Clipboard, device, printer,
and GPU sharing are disabled. Only private command inputs and workspace are
shared. Guest output/results do not release capacity until the owned VM is
independently confirmed stopped or absent. Failed ownership or termination
checks remain blocked for explicit recovery.

This conservative profile also requires **no other HCS-managed VM**, including
WSL2 or Docker Desktop VMs. It checks Windows Host Compute Service inventory
before launch and after exact-owned-ID shutdown; it never stops foreign VMs.
The provider does not assume Sandbox IDs equal host-compute VM IDs. Missing
host-compute inventory access fails closed rather than relying on a guest
receipt, launcher exit, or current-user CLI inventory alone.

The executor account must be able to access the machine-global mutex and its
private `%ProgramData%\CAO.WindowsSandboxExecutor.v1` ownership directory.
The directory's owner and protected ACL must match the executor account, SYSTEM,
and Administrators. Host-compute inventory requires an **elevated administrator
or effective Hyper-V Administrators membership**. Enabling Sandbox requires
administrator privileges; local policy may also require administrator
provisioning for the ownership directory. The executor never elevates or changes
these policies.

Pre-provision `CAO_HOSTED_CLI_WORK_ROOT` with a protected, inheritable private
DACL before starting the executor. Supported SDDL, substituting the executor
account's SID for `<SID>`:

```text
O:<SID>D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;<SID>)
```

The adapter validates the root and every existing workspace descendant before
staging and immediately before VM creation, accepting inherited private child
ACLs and ownership by the account, SYSTEM, or Administrators. It rejects
reparse paths and unsafe ACLs rather than changing permissions. Unix `0700`
modes alone do not establish Windows privacy.

Set the same runtime selector for both `serve-hosted --hosted-cli-sqlite` and
`execute-cli --sqlite`. Windows uses a queue concurrency limit of one, so use
a separate SQLite file when switching from another profile: existing queue
configuration is not silently rewritten. Never discard a database or ownership
record to bypass unsettled execution recovery.

An indeterminate `starting` reservation remains fenced even when both inventories
are empty: a delayed launch request cannot be ruled out. The current recovery
acknowledgment does not clear that reservation.

The Windows adapter is protocol-tested and cross-compiled, but has not been
exercised on a Windows host. Microsoft documents `wsb --raw` without publishing
its JSON schema. This adapter accepts only an array of records with `Id`,
`Status` (`running` or `stopped`), and optional string `Uptime`; unknown formats
fail closed. Native CLI, ACL, mapping, and bootstrap compatibility must be
confirmed on the installed Windows build before using this development profile.

#### Rotate the hosted CLI App key

GitHub permits two active private keys for an App. The executor reads its
configured key at startup, so changing the secret or key file does not update a
running executor. Use a two-key rollover:

1. Generate a new private key in the hosted CLI App's **Private keys** settings
   and save it to a new, versioned secret in the executor's secret manager. Do
   not overwrite the old key or leave the downloaded bootstrap file on disk.
2. Update the `execute-cli` deployment to use the new key through
   `CAO_HOSTED_CLI_PRIVATE_KEY_FILE`, `_BASE64`, or the inline variable. Do not
   expose it to `serve-hosted`.
3. Pause new CLI admissions and wait until queued and running commands have
   finished before restarting the executor. Shutdown cancels in-flight
   commands. Restart every executor instance so none depends on the old key.
4. Resume admissions for a controlled validation. Confirm the restarted
   executor can mint a token and complete a safe, catalog-declared action. If
   validation fails, pause admissions and restore the old configuration while
   both keys are still active.
5. After validation succeeds and commands have drained, pause admissions again,
   revoke the old private key in the App settings, then resume admissions.

Never put either key in logs, command output, or checked-in configuration.

For local hosted-CLI storage debugging, both processes can share one SQLite
file holding the queue, audit table, and encrypted token cleanup journal. `--hosted-cli-sqlite` requires
`--experimental`, `--hosted-cli-actions`, and a loopback `--listen`, and the
explicit bootstrap creates the file with mode `0600`. Anyone who can write the SQLite file can
drop its triggers, so never use this in production:

```bash
mkdir -p .tmp
cao-dashboard database bootstrap --store operational --sqlite ./.tmp/hosted-cli.sqlite
cao-dashboard serve-hosted --listen 127.0.0.1:8443 --experimental --hosted-cli-actions \
  --hosted-cli-sqlite ./.tmp/hosted-cli.sqlite ...
cao-dashboard execute-cli --sqlite ./.tmp/hosted-cli.sqlite
```

Runtime now requires the provisioned file and never performs audit retention.
Use `database retain-audit --store operational --sqlite PATH` explicitly.
`audit deliver --sqlite PATH` reads and verifies existing state through a
read-only SQLite connection; `audit verify --sqlite PATH` still appends its own
verification result. This only replaces hosted-CLI storage, not the full
dashboard's canonical PostgreSQL backend or authentication configuration.
See [POSTGRES.md](POSTGRES.md#local-sqlite-testing) for local end-to-end tests.

Each command runs with a single-use installation token scoped to its target
repository, and the token is revoked when the command finishes. The broker
pool is bounded: `--max-pooled-tokens` sets the total, `0` disables pooling,
and pooled tokens are capped at two per scope with a two-minute idle lifetime.
`--prefetch` (default 2) sets how many queued commands get tokens minted
ahead of execution.

Every received token, including rejected grants, is journaled in the protected
revocation store before it can be used. The journal is encrypted with the same
current/previous-key envelope as sessions, in a separate hosted-token namespace.
Only the queue-lease holder retries recovered tokens; its own active tokens
remain owned until release. Failed audit appends never drop cleanup ownership.
Revocation failures survive executor restarts and remain visible until confirmed
revocation or expiry. If both journaling and revocation fail, the broker refuses
further token issuance (including pooled tokens), and graceful shutdown keeps
retrying until cleanup is durable or the token expires. An explicitly canceled
`Broker.Close` reports failure and retains
its in-memory owner. The executor's graceful cleanup ignores command cancellation
and may wait up to the last token's one-hour lifetime from receipt, plus bounded
in-flight mint, storage, revocation, and audit requests (storage/revocation use a
ten-second request timeout). Missing or excessive expiry evidence rejects use
and retains that conservative one-hour cleanup bound. A forced process kill
between GitHub issuing a token and local durable persistence cannot be made
atomic; the token's GitHub-enforced lifetime remains the final bound.

DevOps can inspect or cancel queue entries directly through the backend CLI.
These commands use the configured operational store, require durable audit
storage, and intentionally return command summaries without input values or
output:

```bash
cao-dashboard hosted-cli queue list --status queued --limit 50
cao-dashboard hosted-cli queue list --status queued --limit 50 \
  --continuation-token TOKEN_FROM_PREVIOUS_PAGE
cao-dashboard hosted-cli queue show 42
cao-dashboard hosted-cli queue cancel 42 --actor octocat
```

The list response includes `limit`, `hasMore`, and, when another page is
available, `nextContinuationToken`. Pass that opaque token to
`--continuation-token` with the same status filters to retrieve the next page.
The cursor follows command IDs and does not count or skip preceding rows.

Cancellation is a privileged backend operation and does not perform the
dashboard user's repository-permission check. The supplied GitHub login is
recorded in the append-only audit trail as a self-reported identity, not proof
of the caller's identity. Only trusted operators should have access to the
backend database and CLI environment. `--sqlite PATH` is for local debugging
only.

Verify and export the audit trail with the same store settings (PostgreSQL
from the environment, or `--sqlite PATH`):

```bash
cao-dashboard audit verify                          # prints lastSequence and lastHash
cao-dashboard audit verify --anchor 4182:<lastHash> # fails on rewrite or rollback
cao-dashboard audit export --event cli_command.admitted --actor octocat > audit.jsonl
```

Schedule `audit verify` and keep the printed heads in write-once storage, so
later runs can pass them as anchors. Hosted CLI spans, metrics, and OTLP
audit logs are listed in
[`specs/server-hosted-cli.md`](../specs/server-hosted-cli.md#7a-telemetry).
For bounded, restart-safe replay of committed rows, use the opt-in
[`audit deliver` job](audit-delivery/README.md). It requires an independently
administered HTTPS object adapter, reads back events before advancing a
destination-resident CAS checkpoint, and fails closed on missing/stale anchors
or retention gaps. The deployment guide includes explicit enrollment, an
executable adapter conformance test, disabled-by-default scheduling templates,
and monitoring/evidence requirements. Neither a local JSON mirror nor an OTLP
emission confirms independent delivery or write-once retention.
The normative contract is [`specs/server-hosted-cli.md`](../specs/server-hosted-cli.md).

### Upstash Redis provider

The `upstash` Redis module composes with the `container` target for Upstash
Redis's per-TCP-session causal consistency. It serializes every command through
one connection, never recycles that connection, disables transparent retries,
and permanently fails closed after transport loss. A process restart is required
to recover.

Every start derives a random internal namespace from `REDIS_NAMESPACE`.
This prevents state written through an earlier TCP session from reappearing
after restart, and deliberately invalidates existing OAuth sessions. Superseded
namespaces remain disposable data and consume capacity until an operator removes
them while the application is stopped.

This module requires exactly one application replica, a dedicated Upstash
database, artifact ingestion through `CAO_SOURCE_DIRECTORY`, and verified TLS.
Server-side collection and standalone collection roles are rejected. Policy
validation requires `target.replicas: 1`, and operators must set the hosting
platform itself to the same replica count. Do not configure the Upstash REST URL
or REST token. For the operator procedure, see [Deploying the dashboard with Upstash
Redis](https://github.com/githubnext/gh-cao/blob/main/docs/deployment-upstash.md).

### Container image

The host-neutral image uses
`serve-hosted`, so GitHub OAuth, explicit organization/team authorization,
server-side encrypted sessions, CSRF checks, webhook signature verification and
deduplication, shared Redis rate limits, and secret-redacting logging remain the
same.

Build from the repository root:

```bash
docker build -f server/Dockerfile \
  --build-arg VERSION=0.0.0-alpha \
  --build-arg REVISION="$(git rev-parse HEAD)" \
  --build-arg CREATED="$(git show -s --format=%cI HEAD)" \
  -t cao-dashboard:test .
```

The official `.github/workflows/cao-package.yml` workflow publishes the same
image as `ghcr.io/githubnext/gh-cao/cao-server`. Pushes to `main` receive an
immutable `sha-<full-commit>` identity; published releases receive their exact
Docker-safe semantic version tag. Use the resulting digest in downstream
Compose files. Multiple services can share that digest and select
`serve-hosted`, `collect`, `backfill`, or `doctor` through `command`.
Maintainers and administrators may also dispatch the workflow from current
protected `main` and provide an exact source branch through the required input;
these builds use a non-colliding `dispatch-<full-commit>` identity.
Publication is gated by Hadolint, actionlint, zizmor, Trivy, Grype, Dockle,
source tests, and protected-main ancestry. Syft produces an SPDX SBOM, and the
protected-main `.github/workflows/cao-package-publish.yml` reusable workflow
revalidates caller authority, source metadata, checksums, and OCI labels before
attesting both build provenance and the SBOM for the exact OCI digest.
Those package guarantees apply to consumers of the published image, not to
locally source-built images.

#### Owner-selected release evidence collection

Use the native Go extension to collect a bounded, machine-readable record without
publishing, promoting, deploying, or changing workflow authority:

```sh
gh cao evidence release \
  --repo githubnext/gh-cao \
  --revision FULL_40_CHARACTER_SOURCE_SHA \
  --image ghcr.io/githubnext/gh-cao/cao-server@sha256:EXACT_64_CHARACTER_DIGEST \
  --package-run PACKAGE_RUN_ID \
  --output /secure/evidence/release-evidence.json
```

The `gh cao evidence` group hosts operator evidence collection. No Node.js,
repository-local scripts or checkout are required. Use a release containing this
command; merging source does not update an already installed extension.

Use the existing package workflow run ID and immutable published image digest,
not a tag. Optional `--promotion-run RUN_ID` collects references from the existing
prerelease-to-latest workflow; its workflow SHA is **not** the product source
SHA. Optional `--azure-receipt /secure/evidence/deploy-azure-receipt.json` consumes
the existing `cao deploy azure` receipt, requiring its source revision to match
the selected revision. It preserves development-only assurance, dirty-worktree
state, context/plan hashes, per-target digests and reported verification outcomes.
Private registry references are hashed; cloud resource identifiers, raw responses,
actor names, comments, job/step names, secrets and credential-bearing URLs are
not copied. No Azure API or deployment is invoked.

With `--verify-provenance`, the collector invokes the installed GitHub CLI's
`gh attestation verify` for the exact OCI digest, repository, reusable publisher,
source-digest constraint and SLSA v1 predicate. It additionally requires the
verified statement's image subject and package run-attempt invocation to match.
Registry authentication and read access must already be configured; no credentials
are accepted as collector arguments. A failed/unavailable verifier or mismatched
receipt leaves provenance and image/source binding unknown. Manual builds whose
certificate source identity differs from the selected product revision can fail
this constraint; the collector does not relax verification to accept them.
The normalized receipt includes the verification command and raw receipt hash,
but does not retain raw attestation bundles or assert builder isolation.

The collector currently targets GitHub.com and GHCR. Schema version 1
(`kind: cao.release.evidence`) records the collector binary hash, extension/Go/GitHub CLI
versions and collection time, and always reports `incomplete`.
Run/job/check references are attempt-scoped; artifacts and approval history are
run-scoped with **unknown** attempt binding. Approval records do not establish
independent human approval or effective environment policy. Unavailable/invalid
optional API evidence is explicitly unknown; invalid selected run identities or
deployment receipts fail collection. API reads stop after five 100-record pages,
marking bounded inventories incomplete; responses are limited to 2 MiB with a
60-second command timeout, local receipts to 1 MiB, and step lists to 100.
The complete operation has a 30-minute deadline and honors cancellation.
The new output uses mode `0600` where supported and never overwrites an existing
file. Operators must restrict destination-directory ACLs, especially on Windows
where POSIX mode bits do not enforce private access.

This is collection-time evidence, not a governance decision, production acceptance,
proof of running image measurements, independent retention, or Type 2 operating
effectiveness. Azure configured-reference read-back is not a measured running
image digest, and its development images do not inherit official-package scans,
SBOM or provenance. Owners must retain the original candidate metadata/checksums,
SBOM, signed bundles and verification receipts through their approved evidence
channel, along with this JSON and exact collector/tool versions. Existing Actions
artifact references/expiry metadata are not independent delivery receipts; export
before expiry, verify destination retention and access, supply actual deployment
measurements and approval/check evidence, and agree the examination period and
governance requirements with the auditor. Nothing is uploaded automatically.

The Dockerfile includes the base `cao.json` policy. Configure a reviewed
`control-plane.web.host` before building, or mount a reviewed policy and set
`CAO_POLICY_PATH` at runtime. Supply database, OAuth, session, and webhook
credentials through the host's secret manager.

The `dashboard-runtime` target contains the public server without collection
tools. The `collector-runtime` target adds Node, the GitHub CLI, a
checksum-verified policy-pinned gh-aw binary, the Activity projection catalog,
a deterministic source-bound control-plane inventory, and the shared policy
resolver. Deploy standalone collection roles without public ingress, keep the
collection App private key out of admission-only web replicas, and share a
persistent evidence volume between collector and backfill roles.

The external artifact volume is authoritative input, not checked-in deployment
data. Before the first start, populate an unattached staging volume with a
complete `payload-hashes.json` and every referenced inventory, run, and record
file from a trusted dashboard build. Verify every manifest hash, then atomically
mount that completed volume read-only at `/app/source`; never copy
individual files into the volume attached to a running service. Apply updates
the same way with a newly staged volume.

For private proxy ingress, configure the host so only the trusted proxy can
reach the application. The app accepts
`X-Forwarded-Host` and `X-Forwarded-Proto` only when the direct connection comes
from `CAO_TRUSTED_PROXY_CIDRS`; it still requires the forwarded protocol to be
`https` and the forwarded host to match `CAO_ALLOWED_HOSTS`. Startup fails when
the CIDR is absent, malformed, or public. Use the narrow proxy subnet,
not `0.0.0.0/0` or an entire RFC1918 range.

Use `rediss://` whenever the Redis provider offers TLS. A
deployment-managed Redis service may use plaintext only on the private service
network when `control-plane.web.host.redis.allow-private-plaintext` is `true`
and the endpoint uses a private service hostname or IP. This policy does not
affect Azure: Azure Functions continues to require `rediss://`.

Deployment promotion and rollback belong to the application host. Review
source changes, isolate source builders, and verify readiness before promotion.

#### Rollback

Redeploy the last known-good image digest through the host's deployment process.
Confirm `/api/readiness`, OAuth login and authorization, a bounded query,
webhook signature handling, and rate-limit behavior. Redis is disposable: if
the new binary wrote an unusable projection, clear only that deployment
namespace and rebuild from the retained trusted artifact. Rolling back source
does not roll back OAuth, webhook, or session secrets.

The hosted server exposes canonical repository/run APIs, verifies and
deduplicates webhook deliveries, and coordinates projection updates with a
Redis lease so multiple replicas do not rebuild concurrently. Webhooks trigger
authoritative re-ingestion; they are not treated as complete canonical records.
Validated webhook and rebuild requests return `202` before ingestion work
continues under a bounded, request-independent context. Only explicitly listed
administrators may call `POST /api/admin/rebuild`; it validates input and
atomically replaces current Postgres sources and state. A failed replacement
leaves the previous committed state intact.

Hosted rebuild admission and worker start require the durable operational audit
service. Redis or volatile-memory hosting without a separately supplied durable
audit service returns `503` for this administrative action; automatic collection,
startup ingestion and signed webhook reconciliation are unchanged. The loopback
bearer profile remains compatible, identifying the actor as `local-bearer`
(capability attribution, not a verified person).

`admin.rebuild.admitted`, `admin.rebuild.started` and `admin.rebuild.completed`
retain the authenticated actor, a fresh `rebuild_id`, prior revision, timestamps,
fixed outcome/reason and successful revision/data revision. `rebuildId` joins
the `202` receipt and replaceable status to independent retained audit history;
it is not the private lease token. Status `auditState` is `pending`, `recorded`
(durable), `local` (no durability guarantee), or `unavailable`. A successful
publication with unavailable terminal evidence is still published and notified,
not rolled back or automatically retried. Status persistence failures separately
attempt `admin.rebuild.status_failed`. Store and mirror errors are logged without
payloads or raw source errors; mirror-only failures do not invalidate confirmed
durable evidence.

Forward committed rows with the existing audit export/delivery facilities and
verify them with independent read-only access and external anchors. Alert on
audit `stored=false`, status-write failures and admitted/started IDs without a
terminal event. A crash or simultaneous outage can leave an incomplete
lifecycle: reconcile canonical revision state and independent logs manually;
neither status reads nor a later rebuild reconstruct a missing outcome.
See [administrative rebuild evidence](../specs/server-operational-storage.md#administrative-rebuild-evidence).

#### Missing rebuild terminal evidence

Use the configured PostgreSQL operational namespace with the **SELECT-only
evidence-reader credential** described in [POSTGRES.md](POSTGRES.md), not the
runtime or schema-owner credential:

```bash
cao-dashboard audit lifecycles --grace 1h --max-events 10000 --timeout 2m
# Local debugging only, against an already provisioned file:
cao-dashboard audit lifecycles --sqlite .tmp/hosted-cli.sqlite
```

This is a one-shot read-only check; it does not append a verification event,
open the JSON audit mirror, repair status/history, replay a rebuild, or enable
a deployment schedule or service. JSON goes to stdout, including a scan failure
result when reads fail after opening; command errors go to stderr. It exits
nonzero for `attention`, `incomplete`, invalid configuration or unavailable
storage. Store the result, source/deployment/namespace identity and exit status
in an independent monitoring destination. Open/configuration failures may have
no JSON result; absence of a result is not a healthy heartbeat.

Route alerts on `missing_terminal > 0`, per-ID issues (including
`status_write_failed`, duplicate or conflicting events), and `status=incomplete`.
`pending` means the first observed event is within grace and may still be active;
it is not a failure or a liveness assertion. The minimum grace is 35 minutes,
above the current work timeout/lease; the default is one hour. Approve a larger
grace if deployment clock skew or cleanup latency requires it, and review this
bound whenever execution deadlines change. Age uses stored timestamps; large
future timestamps are clock uncertainty, not successful completion.

The command scans at most the configured rows plus one lookahead, paginating
all audit event types in sequence order. It does not run full chain verification.
If `event_limit` is reported, increase the budget within the 100,000-row hard
limit and rerun **from the same starting point**. Do not use `last_sequence` as
a checkpoint: unresolved admissions would be forgotten. A truncated scan
reports their classification as `unknown`, not a missing terminal alert.
`--after SEQUENCE` explicitly selects a suffix and always reports incomplete
historical coverage. A retained oldest prefix (`retained_prefix`) likewise
requires independent evidence; an interior `history_gap` blocks missing-terminal
classification. For a contiguous observed suffix, missing terminals for IDs
actually observed can still be reported, but IDs wholly outside it are unknown.
Use the independent full committed-row pipeline for history beyond the bound.
Verification with approved external anchors and `audit deliver` remain separate
checks; `history_complete` describes sequence coverage, not authenticity.

On an alert, preserve the JSON, anchored verification/delivery evidence and
relevant `stored=false` audit telemetry first. Rerun the check to account for a
late terminal append. Correlate each server-generated `rebuild_id` and sequence
range with the original receipt, authenticated audit events, trace-correlated
logs, canonical prior/current revisions, and replaceable status. A newer
canonical revision or expired lease alone cannot establish that ID's outcome:
another operation may have published, or work may remain uncertain. Record the
reconciliation, evidence references, uncertainty and accountable disposition
in the incident/evidence system. Never synthesize a completion event, rewrite
history, retry ingestion automatically, or treat a canceled/failed completion
as a success. Any separately authorized rebuild must receive a fresh ID.
The operator must separately approve cadence, monitor missing-check heartbeats,
and exercise alert acknowledgement and outage/retention-gap response.

#### Operational-value publication

`POST /api/v1/value` publishes one prepared operational-value evidence envelope
(JSON or one JSONL record, at most 8 MiB). It requires PostgreSQL and an already
observed repository/workflow; it neither enrolls targets nor runs uploaded code.
Hosted calls require a listed administrator with current step-up assurance and
the session's `X-CSRF-Token`. Local PostgreSQL uses the existing loopback bearer
capability; local SQLite and static Pages do not support this write.
Use `gh cao value publish OWNER/REPO WORKFLOW --server URL --input FILE`.
The receipt preserves `id` and `observedAt` and returns `revision` and `updated`.
An exact retry is a no-op; stale/conflicting captures, contract drift and
immutable-history rewrites fail. Namespace-scoped publication and snapshot
replacement share the writer lock; directly published packets, facts and
required observed parents survive replacement. Only a new committed capture
advances the revision. Value-dependent queries key their bounded result cache
by that revision; other query caches retain their existing fixed lifetime.
This endpoint does not activate ongoing collection: explicitly register a
reviewed collector in policy through `gh cao value enable`, then deploy policy
and collector together. Recurring remote acquisition remains deferred pending
enforceable shared-budget admission. Health publication is credential-free and
keeps enabled registrations visibly deferred, with original capture/cutoff dates.
It does not mint a token or treat a quota preflight as a shared reservation.

Bounded local-only computation has its own `value-local` lease, a verified private
Activity snapshot and one attempt per completed projection generation. It does
not hold the projection lease or evidence lock while computing. The two-minute
batch/30-second adapter budgets cancel the owned process tree. Computation uses
a frozen policy copy; final publication uses a short evidence lock, rechecks
enrollment, and compares the input generation, manifest, policy and retained
evidence with its starting state. A changed precondition defers the result
rather than overwriting newer accepted evidence. Completed output waits for
the next ordinary projection, avoiding a Value-generated retry loop. An
unsupported adapter, missing input, timeout or failure preserves accepted history.
This improves health visibility and core isolation, not guaranteed fresh Value
collection or separate capacity for external consumers.

Health reconciliation keeps the newest decision and newest actual attempt
independently, so replay order and missing attempt metadata cannot erase known
history or revive an older registration. Same-clock contradictions fail closed;
measurement capture/cutoff dates remain exclusively those of accepted evidence.

The hosted dashboard shows a user icon at the lower left of the navigation.
It appears only after the server confirms an authenticated GitHub session and
opens a user view with account switching and logout. Logout remains on a
non-cacheable signed-out page until the user explicitly starts another login.
“Use another GitHub account” clears and revokes the current CAO session, then
starts a fresh OAuth flow with GitHub's account chooser. Only the newly selected
account is retained in the browser session; tokens for every account remain
server-side.

`internal/server/auth_model_test.go` defines the browser authentication state
model and generates every valid login, switch-account, and logout path through
three transitions. Each generated path drives the real HTTP handlers and checks
the canonical session endpoint after every transition.

The Redis command client reuses a bounded connection pool, applies operation
deadlines, and retries read-only commands once when a pooled connection has
gone stale. Write commands are not replayed automatically.

### Request rate limiting

The HTTP server uses atomic Redis token buckets to enforce limits consistently
across replicas. Authenticated requests are keyed by a SHA-256 digest of the
GitHub login; OAuth login requests are keyed by a digest of the client IP, and
valid callbacks by a digest of their signed state. Forwarding headers are
considered only at the configured trusted-proxy boundary. Raw logins, client
addresses, and OAuth state are not stored in rate-limit keys.
Query requests reserve one cost unit before execution. Completed queries cost
the greatest of execution duration, measured operations and SQL input rows, peak
working rows, and estimated bytes. Structural complexity is bounded separately
and emitted as privacy-preserving telemetry. Cost is capped at the query bucket
capacity; the additional cost is charged atomically before the result is
returned.
Enterprise proxy boundaries may supply either `X-Forwarded-For` or RFC 7239
`Forwarded`; only the final value written by the trusted boundary is accepted.
Authenticated quotas are per GitHub login, and OAuth callbacks use their opaque
state cookie, so users sharing a corporate egress address do not share those
quotas. The normative contract is `specs/server-rate-limiting.md`.

| Request class | Capacity | Refill period |
| --- | ---: | ---: |
| Dashboard query (`POST /api/v1/query`) | 30 | 1 minute |
| OAuth entry and callback | 10 | 5 minutes |
| Other API and auth requests | 120 | 1 minute |
| Hosted pre-authentication edge | 1,200 per client IP | 1 minute |
| Hosted public health/readiness (shared across probe paths) | 600 per client IP | 1 minute |

Local health/readiness probes remain exempt. Hosted probes can return `429`
when their shared per-client bucket is exhausted, or `503` when Redis cannot
enforce it. GitHub webhooks are exempt from user/API
quotas but use the high-capacity edge bucket before signature validation.
Hosted dashboard assets use that edge bucket so session loading remains bounded
without applying the tighter API quota to page loads.
Every limited response includes `RateLimit-Limit`,
`RateLimit-Remaining`, `RateLimit-Reset`, and `RateLimit-Policy`. An exhausted
bucket returns `429 Too Many Requests` with `Retry-After` in seconds. If Redis
cannot enforce a limit within two seconds, the request fails closed with
`503 Service Unavailable`. The edge bucket runs before session loading so
invalid, expired, and unauthenticated requests cannot bypass Redis enforcement.

### Hosted Azure architecture

> [!WARNING]
> The hosted Azure architecture is an experimental production-readiness profile,
> not a turnkey production certification. Treat the Bicep, OAuth policy, Redis
> topology, and Key Vault access model as a reviewed baseline that must be
> validated against your organization's Azure, GitHub, compliance, monitoring,
> incident-response, and data-retention requirements before live use.

The Azure Functions profile keeps the dashboard browser isolated from Postgres,
Redis, GitHub tokens, refresh tokens, database credentials, and Key Vault secret values.
The Function App is the only public application boundary and the only component
that talks to GitHub APIs, Key Vault references, Postgres, and Azure Managed Redis.

```mermaid
flowchart LR
  Browser["Authorized user's browser"]
  Edge["Azure HTTPS edge / App Service front end<br/>sets forwarded host + proto"]
  Function["Function App<br/>Go dashboard HTTP handler<br/>GitHub OAuth sessions + CSRF"]
  GitHubOAuth["GitHub OAuth + API<br/>login, refresh, org/team membership"]
  KeyVault["Azure Key Vault<br/>OAuth secret, session secret, Redis URL"]
  Postgres["Postgres<br/>current dashboard entities"]
  Redis["Azure Managed Redis<br/>TLS<br/>operational state"]
  Storage["Functions storage account<br/>runtime state only"]
  Insights["Application Insights<br/>non-secret operational telemetry"]
  OTel["Operator-managed OTel collector<br/>TLS + authentication"]
  Operators["Control-plane operators<br/>deploy Bicep + rotate secrets"]

  Browser -->|"HTTPS static assets + API + best-effort SSE"| Edge
  Edge -->|"trusted forwarded host/proto only when allow-listed"| Function
  Function -->|"OAuth code, refresh, membership checks"| GitHubOAuth
  Function -->|"Key Vault references resolved by managed identity"| KeyVault
  Function -->|"rediss:// operational commands"| Redis
  Function -->|"entity source reads/writes"| Postgres
  Function -->|"runtime binding state"| Storage
  Function -->|"opt-in OTLP/HTTP, bounded metadata only"| OTel
  OTel -->|"Azure Monitor exporter"| Insights
  Operators -->|"reviewed Bicep + secret rotation"| KeyVault
  Operators -->|"deploy package + app settings"| Function

  classDef boundary fill:#eef6ff,stroke:#0969da,stroke-width:2px;
  class Function,KeyVault,Postgres,Redis boundary;
```

Primary actors and responsibilities:

- **Dashboard user**: authenticates through GitHub OAuth, must satisfy the
  configured organization/team authorization policy, and receives only
  same-origin dashboard HTML/API responses.
- **Azure platform**: terminates HTTPS, invokes the custom Functions handler,
  resolves Key Vault references through managed identity, and may cold-start,
  scale in, or terminate long-lived SSE requests.
- **GitHub OAuth/API**: issues expiring access/refresh tokens and confirms
  organization/team membership; GitHub tokens never leave the server.
- **Postgres**: stores the current dashboard entity sources and diagnostics;
  replacement is transactional, without generations or snapshots.
- **Redis**: stores operational queues, caches, and sessions, not
  dashboard entities.
- **Control-plane operator**: reviews Bicep/app settings, keeps Key Vault
  mandatory, rotates credentials, and validates compliance evidence.

Trust boundaries:

- Browser ↔ Function App: authenticated same-origin HTTPS with `Secure`,
  `HttpOnly`, `SameSite=Lax` session cookies and CSRF headers for mutation.
- Function App ↔ GitHub: server-side OAuth/token refresh/membership calls; no
  PATs and no GitHub tokens forwarded to the browser.
- Function App ↔ Key Vault: managed identity and RBAC only; no secret values in
  Bicep outputs, logs, checked-in parameters, or browser-readable state.
- Function App ↔ Redis: TLS-only `rediss://` and a deployment
  namespace for disposable derived data.

## Data ingestion

The `ingest` command consumes a directory with the deployed dashboard data
contract:

```text
payload-hashes.json
inventory-sources.json
gh-aw-logs-runs/*.jsonl
gh-aw-logs-records/*.jsonl
```

Ingestion fails closed when the manifest is missing, a manifested SHA-256 hash
does not match, a path is unsafe, run-information shards are absent, raw
Activity shards are present, a JSONL record is malformed, or projection fails.

The ingestion sequence is:

1. Validate every manifest path and content hash.
2. Require and read all compacted run-information shards.
3. Read compacted run-linked record shards.
4. Load `inventory-sources.json` as already-logical published sources.
5. Project canonical Campaign, Repository, Workflow, Run, Domain, Tool, Audit,
   Issue, and Operational Value records through
   `dashboard/site/src/data/queries/database.json`.
6. Transactionally replace current Postgres logical sources, metadata,
   diagnostics, and revision state.

An ingestion with the same artifact revision can reuse current state.
Failure before commit leaves the previous Postgres state available.

The Postgres state also records an authoritative `evaluatedAt` timestamp
derived from the latest canonical row or source metadata timestamp. Relative
dashboard time windows use this value rather than browser wall-clock time.

### Ingestion profiles

Evidence reaches the server through exactly one of two profiles. They are
alternatives, not layers, because a canonical database has one writer.

| | Published snapshots (default) | Server collection (optional) |
|---|---|---|
| Evidence acquired by | `cao-activity.yml` in GitHub Actions | the server's collection workers |
| Server configuration | `CAO_SOURCE_DIRECTORY` | `CAO_COLLECT_APP_ID` and the other `CAO_COLLECT_*` settings |
| GitHub credentials | held by the workflow | a GitHub App held by the deployment |
| Event source | workflow schedule | webhook deliveries |
| Deployment | Function App only | Function App plus Container Apps workers |

The default profile is unchanged: with only `CAO_SOURCE_DIRECTORY` configured
the server behaves exactly as before and performs no GitHub collection.

Configuring both is rejected at startup rather than resolved silently, so a
deployment can never have two writers for one database.

To switch a deployment to the collection profile, unset `CAO_SOURCE_DIRECTORY`,
set the `CAO_COLLECT_*` settings, and deploy `collectorImage`. To switch back,
reverse both. Switching does not lose data: the canonical database is
repopulated from whichever evidence the selected profile retains.

### Collection profile

Collection separates three concerns that fail differently:

1. **Admission.** The existing `POST /api/github/webhook` endpoint verifies the
   signature and atomically deduplicates the delivery, applies repository
   debounce, and appends one task to a Redis stream. A successful response
   therefore means durable admission. Admission is constant-time and takes no
   projection lease, so a delivery burst cannot block the endpoint.
   Signed `issues` lifecycle events for enrolled repositories enqueue
   repository collection rather than mutating stored issue entities directly.
   Default-branch `push` events enqueue inventory-only refreshes when added,
   modified, or removed paths match `.github/workflows/*.lock.yml` (top-level
   files only). Markdown sources, ordinary workflow YAML, other branches, and
   tags do not trigger these refreshes. Truncated push commit lists conservatively
   request refresh rather than fetching commit details in the receiver.
   Inventory refresh and run collection have separate debounce keys, so neither
   can suppress the other.
2. **Collection.** Workers lease tasks and run the same
   `gh aw logs --audit` and `activity/cao.mjs` commands the Activity workflow
   runs, writing into the evidence lake. One repository is collected at a time,
   and GitHub budget is reserved per installation before each collection.
3. **Canonical ingestion.** Collected evidence is written to PostgreSQL by the existing
   `internal/ingest` package. Before finalizing the payload manifest, projection
   resolves the reviewed deployment policy, overlays the exact operational enrollment
   set, and enriches the packaged source-bound inventory with a short-lived
   token for the control repository installation. The first activation fails
   without valid campaign inventory; later transient inventory refresh failures
   retain the previous valid inventory. Projection then
   builds a temporary Activity database and runs the native Go
   operational-value orchestrator, whose output is equivalence-tested against
   `cao operational-value`. Campaign adapters remain shared JavaScript modules,
   invoked with one installation-scoped token per repository. Operational-value
   failure remains best-effort and does not block newer Activity evidence.
   Canonical replacement is coalesced behind a dirty flag and a minimum
   interval, so ingestion cost follows the collection rate rather than the
   event rate.

The evidence lake is laid out byte-compatibly with a snapshot published by the
Activity workflow:

```text
gh-aw-logs-shards/     collected, not yet compacted
gh-aw-logs-runs/*.jsonl
gh-aw-logs-records/*.jsonl
payload-hashes.json
control-settings.json
control-plane-inventory.json
inventory-sources.json
```

That is what makes the profiles interchangeable, and it is why cold start needs
no GitHub access: a retained lake is replayed directly.

Enrollment is derived from GitHub App installations. A delivery for a
repository outside the enrollment set is acknowledged and dropped rather than
collected. Signed repository lifecycle events for a known installation
may instead queue discovery; they never infer enrollment from payload contents.
Installation additions and repository discovery are grouped by installation,
with a two-second coalescing window. Background workers enumerate all pages of
current installation coverage, reserve shared GitHub quota for each page, and
publish enrollment only after complete enumeration. Discovery fails, rather
than truncates, above `CAO_COLLECT_INVENTORY_LIMIT` (100,000 repositories per
installation when unset).

Installation removals and repository access withdrawals are independently queued,
then applied by workers with durable erasure work persisted before acknowledgment.
Repository creation, unarchive, rename, transfer, archive, and deletion events queue
independent hints that reconcile current authenticated installation coverage.
Complete discovery removes archived or absent coordinates, persists erasure work
before withdrawing enrollment, and queues collection of verified active repositories.
Renamed-away coordinates and archived/deleted repositories are evicted from the
evidence lake and retained inventory, then removed by canonical projection.
Delayed or reordered webhooks cannot restore stale names or historical lifecycle
states. No webhook performs GitHub calls, enrollment mutations, or projection
inline. Refresh workers persist projection requests before acknowledging work;
transient inventory failures keep the previous inventory and retain a pending
refresh for the next projection interval.

Workers also refresh the App's active installation list on startup and every
hour, then queue the same quota-governed repository reconciliation used
by webhooks. A shared lease and persisted timestamp coordinate replicas and
restarts. This repairs missing create/archive/delete/rename or installation
webhooks without scanning historical runs or recollecting unchanged repositories.
Force a repository-list repair with `cao-dashboard backfill --repositories-only`;
the command reports queued refreshes and withdrawals, which collection workers
then apply. Set `CAO_COLLECT_REPOSITORY_REFRESH_INTERVAL` to a positive Go duration
such as `30m` or `2h` to override the default `1h` cadence.

Configure the collection App's webhook URL as `/api/github/webhook` and subscribe
to `push`, `repository`, `installation`, `installation_repositories`, `workflow_run`,
`issues`, and `pull_request` events. Existing read-only App permissions and installation coverage
remain the scope boundary; these notifications do not authorize workflow execution
or change CAO rollout policy.

The server also mines recorded gh-aw episode identity and created issue/PR
dispositions. Episode membership comes only from `context.episode_id` in compact
`aw_info.json` already downloaded by collection or retained run envelopes;
caller, root, and hop references are preserved without heuristic grouping or
additional discovery requests. Outcome webhooks retain bounded object facts
and join them only to exact `create_issue` / `create_pull_request` safe-output
references. Merge/completed, rejection, bot lifecycle closure, pending, and
unknown remain distinct from workflow success. Other safe-output action types
are not evaluated by this lifecycle-only profile.

Replicas coordinate a server-only repair pass every projection interval
(five minutes by default): at most ten REST calls total, including quota probes,
with a one-minute deadline and a durable output cursor. Repair reads only
already attributed objects in currently enrolled repositories and shares the
installation quota governor. It does not enumerate issue inventories, fetch
comments/reviews, or expand episode scope. Missing evidence remains partial.

Mined facts reuse existing canonical Audit tables (`episode.membership`,
`episode.root`, `outcome.observed`) and participate atomically in snapshot
publication and revision notifications. No schema migration is needed.
Bounded packets under the evidence lake's `server-mining/` directory support
API-free replay and are erased on scope withdrawal, including volatile restart
repair. This feature is not installed in or run by `cao-activity`. See
`specs/dashboard-data.md` section 11.2 for the field mapping.

Rate limits are governed per installation. Each collection reserves budget
before it starts and passes the reserve to the collection subprocess, so the
subprocess stops before exhausting the installation. An installation that
receives a rate-limit response is parked with jitter; other installations keep
running.

Failed collections are retried with backoff and moved to a dead-letter stream
after the attempt limit, where they remain visible in collection status rather
than disappearing.

OpenTelemetry exports admission and refresh spans
(`cao_dashboard.collection.webhook.admit`, `cao_dashboard.collection.refresh`)
and `cao_dashboard.collection.refresh.count` /
`cao_dashboard.collection.refresh.duration` and
`cao_dashboard.collection.refresh.queue_age` (seconds), with bounded stage, kind,
and outcome attributes. Admission outcomes distinguish queued, coalesced,
duplicate, ignored, and failed work. Queue tasks carry only W3C `traceparent`
correlation, never baggage or payload contents. Quota exhaustion and secondary
limits defer discovery until the shared bucket's retry time without consuming
its failure-attempt budget.

Collection is at-least-once. Retry, deferral, blocked-repository requeue, and
dead-letter transitions append their replacement before acknowledging the
leased entry in one Redis operation. Completed entries are acknowledged and
deleted atomically. The task stream is never `MAXLEN`-trimmed: its configured
limit applies admission backpressure, returning a retriable webhook failure
instead of discarding undelivered or pending work. The same configured limit
bounds the dead-letter stream by trimming its oldest diagnostic records.

### Collection roles

The same binary runs every role:

| Command | Role |
|---|---|
| `cao-dashboard serve-hosted` | serve the dashboard and admit webhook deliveries |
| `cao-dashboard collect` | lease tasks, collect repositories, and project |
| `cao-dashboard backfill` | cold start: replay the lake, enumerate installations, seed tasks |
| `cao-dashboard backfill --repositories-only` | force repository-list repair without replaying history; workers apply queued reconciliation |
| `cao-dashboard backfill --replay-only` | repopulate the database from retained evidence with no GitHub requests |
| `cao-dashboard doctor` | run a read-only, systematic check-up of Redis, canonical data, queries, and collection |
| `cao-dashboard otel-smoke` | verify that one live CAO request and, when configured or required, the stable Go runtime metrics are indexed by OpenObserve without reporting credentials or identifiers |

Set `CAO_COLLECT_WINDOW_DAYS=7` to restrict historical run discovery to a
seven-day GitHub `created` range. Zero/unset preserves the existing unbounded
discovery behavior. The window limits new run-task admissions, not the
authoritative retained lake replay.

`GET /api/admin/collection/status` reports enrollment coverage, shared collection queue
depth (including webhook and backfill tasks), in-flight tasks, cumulative
backfill run admissions, dead letters, cold-start phase,
and per-installation rate-limit headroom. Its `load` values (`webhook`,
`collection`, `failure`) are distributed event counts with a 60-second
half-life: one new event contributes 1, then its contribution halves each
minute without requiring periodic cleanup. They are estimates of recent
activity, not exact per-minute rates. The Ingestion dashboard compares
current outstanding work and cumulative backfill admissions as clearly labeled
horizontal bars and shows these loads to administrators. In the
default profile the endpoint reports `{"configured": false}` rather than
failing.
The existing Redis-backed ingestion counters remain available in the
administrator-only `collection-health` dashboard source; OTLP metrics do not
replace them.

### Diagnose a deployment

`cao-dashboard doctor` runs a read-only check-up of the current server
configuration. It does not contact GitHub, write to Redis or Postgres, repair data, or
report secret values. Every check has a stable identifier such as
`redis.memory` or `data.active`, a severity, observed facts, and an
operator remedy. The default text report is intended to be readable by both a
person and an agent:

```bash
go -C server run ./cmd/cao-dashboard doctor \
  --redis-url "$CAO_REDIS_URL" \
  --postgres-url "$CAO_POSTGRES_URL" \
  --redis-namespace production-dashboard
```

The standard check-up covers:

- build and runtime identity, external collection tooling, and profile
  exclusivity;
- Redis connectivity, latency, TLS posture, server state, clients,
  persistence, memory headroom, `noeviction`, and namespace contents;
- Postgres data age, schema compatibility, source counts, referential
  integrity, and duplicate identifiers;
- the canonical Dashboard Language query document;
- collection configuration without reading secrets, enrollment coverage,
  queue backlog, pending work, dead letters, cold-start state, rate-limit
  headroom, evidence-lake replayability, and projection activity.

Add `--deep` to read every current source through the production Postgres loading
path and confirm that rows decode, recorded counts match, and no source is
approaching the 200,000-row fail-closed limit. This can read every stored entity row, so it is deliberately opt-in.

```bash
go -C server run ./cmd/cao-dashboard doctor \
  --redis-url "$CAO_REDIS_URL" \
  --postgres-url "$CAO_POSTGRES_URL" \
  --redis-namespace production-dashboard \
  --deep
```

`redis.persistence` observes `INFO persistence` without requiring `CONFIG`
permissions. Missing or malformed observations warn rather than imply healthy
writes. An explicitly enabled AOF with a successful last write passes this
write-health check, unless another reported persistence status is invalid or
reports failure. With AOF disabled, even a successful last background save
warns: a historical save status does not establish an active snapshot policy.
These warnings do not establish backup failure, and a pass does not verify
durability or recoverability. Independently verify snapshot policy or
provider-managed persistence, backup retention, and restore evidence with the
Redis operator or provider management plane.

Use `--format json` for a versioned agent/automation contract. JSON and text
contain the same observations and stable check identifiers. The command exits
non-zero when any check fails; `--strict` also makes warnings non-zero, which
is useful as a deployment gate. Each check is bounded independently by
`--timeout` (10 seconds by default), so one unavailable diagnostic surface
cannot hang the whole report.

The report prints only a redacted Redis endpoint. It reports whether a webhook
secret or App private key is configured, never its value, and it only `stat`s a
private-key file rather than reading it.

### Verify OpenObserve ingestion

Run `cao-dashboard otel-smoke` inside the live CAO container after configuring
the standard `OTEL_*` variables. The command requests the local readiness
endpoint, confirms that the response carries valid trace and span identifiers,
checks the OpenObserve `/healthz` endpoint derived from the OTLP traces
endpoint, and polls the `default` trace stream for the emitted trace. When a
metrics endpoint is configured, it also executes bounded SQL queries against
OpenObserve's normalized `go_memory_allocated` and `go_goroutine_count`
streams.

```bash
/app/cao-dashboard otel-smoke --require-metrics --timeout 2m
```

Use `--trace-stream` when OpenObserve stores CAO traces outside `default`.
`--require-metrics` makes a missing metrics endpoint fail the smoke;
`--timeout` bounds the complete test and `--poll-interval` controls lookup
frequency. When metrics are configured, their query failure fails the smoke
even without `--require-metrics`. The versioned JSON report distinguishes
endpoint presence, search statuses, and whether both runtime streams contain
recent samples. It contains only HTTP status codes and booleans; it never
includes the readiness URL, OTLP endpoint, authorization header, trace ID, or
span ID.

### Collection settings

| Setting | Meaning |
|---|---|
| `CAO_COLLECT_APP_ID` | GitHub App identifier; unset selects the default profile |
| `CAO_COLLECT_PRIVATE_KEY` / `CAO_COLLECT_PRIVATE_KEY_BASE64` / `CAO_COLLECT_PRIVATE_KEY_FILE` | App private key as inline PEM, single-line standard base64, or a mounted PEM file |
| `CAO_COLLECT_LAKE_DIRECTORY` | evidence lake directory, shared by workers |
| `CAO_COLLECT_CATALOG_ROOT` | directory containing `activity/cao.mjs` |
| `CAO_COLLECT_CONTROL_REPOSITORY` | control repository used for inventory discovery |
| `CAO_COLLECT_STATIC_INVENTORY` | source-bound control-plane inventory packaged with the collector |
| `CAO_POLICY_PATH` | reviewed base policy or deployment profile resolved before inventory discovery |
| `CAO_COLLECT_WORKERS` | in-process workers; zero when workers scale separately |
| `CAO_COLLECT_RATE_LIMIT_FLOOR` | requests reserved per installation |
| `CAO_COLLECT_PROJECTION_INTERVAL` | minimum interval between projections (default 5 minutes) |
| `CAO_COLLECT_REPOSITORY_REFRESH_INTERVAL` | repository-list reconciliation cadence as a Go duration (default `1h`); runs on collection workers |
| `CAO_COLLECT_INVENTORY_LIMIT` | optional cap on enrolled repositories; exceeding it fails the projection |
| `CAO_COLLECT_RECOVER_DELIVERIES` | replay failed webhook deliveries to close gaps |
| `CAO_COLLECT_QUEUE_MAX_LENGTH` | admission backpressure limit for outstanding collection tasks (default 200 000); tasks are never trimmed |
| `CAO_COLLECT_ADMIT_ONLY` | admit deliveries without collecting; requires no private key |

### Admission-only front ends

A process that only receives webhooks does not need collection credentials.
Setting `CAO_COLLECT_ADMIT_ONLY` verifies deliveries and enqueues work while
refusing an App private key, workers, and delivery replay, so the
internet-facing front end holds no credential it cannot use. The Azure Function
App is deployed this way; the collection workers hold the key.

Withdrawing scope still takes effect: an admission-only process queues erasure
for a worker that has the evidence lake, and administrative rebuild fails
closed with an instruction to run the `collect` or `backfill` role.

### Retention and erasure

The evidence lake is retained collected evidence, not a cache: cold start
replays it without contacting GitHub. Retention therefore has to be governed
deliberately.

Leaving ingestion scope erases evidence. When an installation is deleted or
suspended, or repositories are removed from it, the server deletes that
repository's shards from the lake and requests a projection, so the canonical
database stops reporting it. Uninstalling the GitHub App is the supported way
to withdraw consent, and it takes effect without operator action.

Erasure workers persist the projection request before acknowledging the task.
If that write fails or is cancelled, the task stays leased and reclaimable.
Projection retains its requested generation until canonical publication succeeds,
then records the completed generation separately. Interrupted publication or
completion writes therefore leave recoverable work, and a newer request arriving
during projection remains pending for the next pass. Recovery can repeat a
projection that already committed; it does not require restoring a cleared marker.

`specs/server-ingestion.md` is the normative contract, and
`adr/server-webhook-driven-ingestion.md` records why the design is shaped this
way.

### Cost and sizing

Steady-state ingestion cost scales with retained evidence; unchanged artifact
revisions may be skipped. Postgres stores only current entity sources and state,
not staging or rollback generations. Size Postgres for current source rows and
transactional replacement; size Redis separately for operational queues,
sessions, and caches.

The evidence lake is *many small per-repository shards*, so it is bound by file
metadata operations rather than throughput. The lake share therefore defaults
to a premium (provisioned SSD) file share; a Standard share's IOPS scale only
with provisioned size and become the projection bottleneck well before capacity
does. Set `collectorLakeStorageSku` to a `Standard_*` value only for small
deployments where cost matters more than projection latency.

Known limits, in the order they will be felt at scale:

- Every projection rehashes the whole lake twice before it can decide whether
  anything changed: once in `activity/cao.mjs hash-payloads` and once in the
  Go manifest validation. That is the structural ceiling on projection
  frequency, and it is why `CAO_COLLECT_PROJECTION_INTERVAL` defaults to five
  minutes rather than to seconds.
- Redis is still an always-on operational cost. Size it for queues, sessions,
  and caches. Size Postgres separately for current dashboard entity sources.
- The Elastic Premium Function plan is always-on. It is sized for webhook
  admission, which is constant-time, so the smallest plan that meets the
  tenant's network requirements is the right one.
- Collection workers do scale to zero: `minimumWorkers` defaults to zero and
  KEDA scales on stream backlog, so an idle deployment pays for storage, Redis,
  and the Function plan only.

## Entity and operational storage

Postgres stores the current dashboard entity sources as native typed fields,
alongside their metadata, canonical diagnostics, and one revision/evaluation
state. An ingestion replaces these atomically: failed transactions leave the
previous committed state untouched. There are no Postgres generations,
projections, or snapshots.
Before validating cross-entity relationships, publication refreshes PostgreSQL
planner statistics for the replacement rows, including partitioned parents.
This avoids cold-start integrity joins using stale empty-table estimates;
autovacuum cannot analyze those uncommitted rows or their partitioned parents.
The editable representation is `spec/storage.tsp`; its emitter produces the
standalone `internal/postgresx/schema.sql` and matching Go bindings.
Eighteen entity tables retain query-consumed fields and identity/storage keys
only. Scalars use native SQL types; timestamps are returned as UTC RFC3339
strings. Nested scalar values are represented by generated columns in their
owning entity tables. No canonical JSON/JSONB or serialized documents are
stored, and canonical scalar rows are not copied into generic source/value
tables.
`runs` and its run-owned records (including sessions and their events) use
matching UTC midnight-to-midnight PostgreSQL RANGE partitions named
`<table>_dYYYYMMDD`. `run_at` uses the
run's `createdAt`, then `startedAt`, `completedAt`, or `updatedAt`; records
without any of these use ingestion time. Linked records inherit their run's
partition timestamp. PostgreSQL routes parent-table inserts; missing days
fail closed rather than creating partitions during ingestion. The separately
scheduled owner-run `database maintain --store canonical` checks existing
partitions, creates any missing current and 28 future days first, and removes complete
run days ending at or before the cutoff set by
`CAO_POSTGRES_RUN_RETENTION_DAYS` (default 30, allowed 7–3650).
Run-linked tables (audits, domains, eval observations, experiment assignments,
friction, grader observations, issues, skills, and tools) independently expire
complete daily shards ending at or before the cutoff set by
`CAO_POSTGRES_LINKED_RETENTION_DAYS` (default 7,
allowed 7 up to the run retention). A partial cutoff day remains until the entire
shard is past its cutoff. Maintenance detaches and drops linked shards before
expired run shards, updating affected source counts and revisions. Repeated maintenance
is a no-op when partitions are already present and none have expired. Run it
at least daily and before restarting after downtime; runtime startup checks the
full provisioned window but never repairs it. Alert on missed maintenance before
the 28-day runway expires. Choose a window
that covers all authoritative run timestamps before ingestion; historical linked
records outside the linked window are skipped while their retained runs are
still ingested. For a seven-day window for both runs and detail, explicitly set
both retention variables to `7`; existing defaults and overrides are unchanged.
Whole-day expiry can retain less than one extra day at the maintenance instant;
schedule lag can extend physical retention further.

Owner-run `database bootstrap --store canonical` initializes the schema and its
partitions. Runtime opens it without DDL. There is no old-layout conversion,
backfill, or backward-compatible import. Use a new database and
re-ingest authoritative inputs when changing the physical contract. Existing
`<table>_wYYYYMMDD` weekly layouts are rejected without conversion; rebuild
canonical storage in a fresh schema or database. Never drop
operational audit/queue state if it shares the old database.

The daily run-count layer is a separately generated additive extension, not a
replacement native detail contract. To add it to an existing compatible database,
run owner-only `database bootstrap --store canonical --namespace NAMESPACE` with
the deployment's owner credentials, then apply the updated
`server/postgres/canonical-grants.sql` grants. Runtime does not create its tables;
without the extension, `daily-run-counts` is unavailable while existing detail
queries remain usable. Its independent receipts preserve daily counts when detail
expires. Initial backfill can cover only still-retained runs, not previously
evicted history. Counts use UTC run-start dates, exclude undated evidence with
incomplete coverage, and retain all admitted dates in this initial implementation;
no new detail-retention, sampling, or aggregate-expiry policy is enabled.

Redis remains namespaced operational storage for caches, queues, sessions, and
it does not hold dashboard entity rows or query indexes.
Neither store grants control-plane authority. Credentials stay server-side.

## Query execution

The browser sends declarative query definitions and requested source names to
`POST /api/v1/query`. The server validates the query graph and resource limits
before loading data.
The validated query dependency graph is compiled to SQL and executed in
PostgreSQL. The Go server validates and binds the plan, enforces resource limits,
and serializes bounded results; it does not filter, join, aggregate, compute,
sort, or paginate rows. Missing/null, coercion, ordering, and resource semantics
are enforced by the SQL compiler and query engine. Unsupported definitions fail
closed rather than switching to another evaluator.
Execution fails closed when a requested plan exceeds 16 dependency levels, 256
derived queries, or 16 joins along one dependency path. Independent queries in a
batch do not consume one another's structural join allowance. Runtime guards cap
a query at 5 million row operations, 500,000 simultaneously referenced or
retained rows, 256 MiB of estimated working row data, and 512 MiB of estimated
retained row data. A retained-byte limit failure returns HTTP 422 with
`code: "query_plan_too_large"`, the failing `queryId`, and
`boundary: "retained_bytes"` alongside a readable `error` message. The response
does not expose the numeric boundary; other query limits remain independently enforced. Per-query
input, join, output, and operator limits remain independently enforced.
Expensive stages, including sorting, are charged against the operation budget
before they allocate or run.

The SQL engine reads current native entity tables from PostgreSQL and evaluates
Dashboard Language operators there. The Go server validates requests, binds
parameters, enforces resource limits, and serializes bounded results; Redis is
not a query backend.

SQL `coalesce`, `if`, and union branches must have compatible declared types.
Run identifiers are text, so the workflow list uses a textual `"0"` missing-run
fallback before the numeric `greater-than` comparison. Its shared contract
fixture is checked against both the browser worker and PostgreSQL, including
queued runs, reruns, and workflows with no observed run.

The engine rejects unsupported prediction queries and enforces limits on query
definitions, joins, predicates, observation-window frames, input rows, output
rows, and total operations. Rolling and change windows execute as partitioned,
ordered PostgreSQL window expressions after aggregation and before projection;
the same fixture under `internal/query/testdata/window-parity.json` is checked
against browser-worker results and hosted SQL.
It does not silently truncate or return partial success for invalid queries.

## HTTP(S) and browser transport

`serve` binds to `127.0.0.1:8443` by default. Non-loopback listen addresses are
rejected. Local debugging uses HTTP and the server never generates
certificates. Provide both `--cert` and `--key` to enable HTTPS with an existing
operator-managed certificate.

The server injects:

```html
<meta name="dashboard-data-backend" content="server-http">
```

into the dashboard HTML. The browser then uses the server API instead of
IndexedDB ingestion:

| Endpoint | Purpose |
| --- | --- |
| `GET /api/v1/health` | Public readiness reports database dependency and data availability; capability-authenticated requests can receive revision and source/row counts. |
| `GET /api/health` | Public health includes Redis and Postgres dependencies. |
| `GET /api/readiness` | Public readiness; returns 503 until current Postgres data is available. |
| `POST /api/v1/query` | Execute requested Dashboard Language queries and return bounded logical sources plus metrics. |
| `POST /api/v1/refresh` | Return the current revision and authoritative evaluation time without ingesting data. |
| `GET /api/v1/events` | Server-Sent Events stream that notifies active views when the data revision changes. |
| `GET /api/v1/diagnostics` | Canonical schema counts, relationship errors, and duplicate IDs from current Postgres data. |
| `GET /api/v1/github-quota/usage` | Administrator-only GitHub API quota usage for the last 24 hours: the peak observed usage of each bucket (App, installation, resource) and the limit-weighted aggregate per 15-minute slot. The same data is the `github-quota-usage` runtime source behind the Ingestion page chart. No credentials are included. |
| `GET /api/repositories` and `GET /api/repositories/:id` | Return canonical repository objects. |
| `GET /api/repositories/:id/runs` and `GET /api/workflows/:id/runs` | Return related canonical runs. |
| `POST /api/github/webhook` | Verify, deduplicate, and reconcile a GitHub delivery. |
| `POST /api/admin/rebuild` | Force a staged full rebuild and atomic activation. |
| `GET /api/admin/rebuild/status` | Return shared rebuild state for all replicas. |

API responses use `Cache-Control: no-store`. The dashboard service worker
excludes `/api/` so query results and event streams are never placed in browser
caches.

### Cross-origin requests (CORS)

The dashboard is same-origin by default: the server emits no
`Access-Control-*` headers. The dashboard requests its web app manifest with
`crossorigin="use-credentials"` so the session cookie is sent, and the hosted
server answers unauthenticated subresource requests (any `Sec-Fetch-Mode`
other than `navigate`) with `401` instead of redirecting them to the
cross-origin GitHub authorize endpoint, which browsers block by CORS. Only
top-level navigations are redirected to `/auth/login`.

To let another reviewed origin read anonymous responses (such as the public
health endpoints), declare it in `control-plane.web.host.cors` in `cao.json` or
its host overlay:

```json
"cors": {
  "allowed-origins": ["https://tools.example.com"],
  "max-age": 600
}
```

CORS is credential-less by design. The server never sends
`Access-Control-Allow-Credentials`, so browsers refuse to expose any response
to a cross-origin request made with cookies. A listed origin therefore cannot
act as the signed-in user or read session data such as the CSRF token from
`/api/auth/session`. `allow-credentials` is not a supported field and is
rejected. Cross-origin integrations that need user data must use a
server-to-server path, not the browser session.

| Field | Default | Purpose |
| --- | --- | --- |
| `allowed-origins` | none | Exact origins echoed in `Access-Control-Allow-Origin`. Wildcards, `null`, paths, user info, and plaintext origins other than loopback are rejected; at most 32. |
| `max-age` | `600` | Preflight cache lifetime in seconds (1-86400). |

Preflights from listed origins are answered with `204` before authentication
and allow only `GET` and `HEAD` with the `Traceparent` header. Unlisted origins receive no CORS headers. CORS never
bypasses authentication, CSRF checks, or host validation. The normative contract
is `specs/server-cors.md`.

## OAuth sign-in troubleshooting

If the OAuth callback shows a sign-in error, select **Sign out and try again**.
This attempts the existing CSRF-protected logout (including server-side token
revocation). The signed-out page clears CAO session, CSRF and OAuth state
cookies and the dashboard IndexedDB cache, then offers an explicit sign-in
that requests GitHub's account chooser. Every CAO login requests account
selection, including login redirects after a failed session. CAO cannot clear
GitHub's own cookies: if GitHub shows only the existing account's permission
dialogue, use GitHub's profile menu to add or switch to another account,
then retry sign-in.

The older `{"error":"GitHub authorization failed"}` response corresponds to
an authorization failure; current versions show a help page instead. This
failure can mean the selected account is not an active member of an allowed
organization or team, or that GitHub membership could not be verified.
Try an authorized account, or ask your dashboard administrator to check the
allowed organizations and teams and your active membership. Do not send
OAuth callback URLs, codes, tokens, or cookies when requesting help.

If other open tabs block browser data deletion, close them and wait for the
signed-out page to finish before signing in. If logout cannot be confirmed,
the page keeps the error visible; clear this site's cookies before retrying,
or contact your dashboard administrator. Try a GitHub account that is an
active member of an organization or team permitted by the dashboard.
Signing in does not itself grant access.

The error page shows a request ID when tracing is enabled. Administrators can
search for that W3C trace ID in their OpenTelemetry backend and inspect the
`GET /auth/callback` span's fixed `error.type` classification. The same ID is
sent as `X-Trace-Id` on the response. Neither the page nor the span reveals
the OAuth code, state, credentials, account, membership details, or raw
provider errors. If no trace ID appears, configure an OTLP trace endpoint as
described below before expecting backend correlation; avoid sending callback
URLs, cookies, codes, or tokens when requesting support.

All generic server HTTP spans use a redacted copy of each request: the server
does not export peer/client IP addresses, user-agent strings, query strings,
arbitrary URL paths, W3C baggage, or client-provided tracestate, including
metadata inherited from an external HTTP host. The original
request still reaches the authentication and rate-limiting code unchanged.
Raw user-agent strings may identify or fingerprint a browser, so they remain
excluded from telemetry rather than assuming their collection is GDPR compliant.
OAuth callbacks continue to use their own fixed-attribute span and extract
only W3C trace context, not baggage. Trace IDs are correlation identifiers,
not user identities. For GDPR-sensitive deployments, operators must also
limit collector/exporter access and retention, review any upstream proxy
logging and configured resource attributes, and avoid attaching identifiers
in custom instrumentation. This application-level minimization does not
certify the entire deployment's GDPR compliance.

## Telemetry

The server is instrumented with standard, vendor-neutral
[OpenTelemetry](https://opentelemetry.io/) tracing and metrics
(`internal/telemetry/`). HTTP requests other than `GET /auth/callback` are
wrapped with `otelhttp` using a redacted request, which supplies bounded
OpenTelemetry HTTP semantic-convention attributes and the standard
`http.server.request.duration`, `http.server.request.body.size`, and
`http.server.response.body.size` metrics. Registered API/auth endpoints also
attach their registered `http.route` template to HTTP spans and metrics after
routing, including `HEAD` requests and parameterized paths such as
`/api/repositories/{id}`. Parameter values are never exported; arbitrary or
unmatched paths omit that attribute. OAuth callbacks instead emit a
dedicated W3C-context-propagating server span named `GET /auth/callback` with
only fixed `http.route` and `http.request.method` attributes, plus
`cao_dashboard.auth.callback.count` (unit `{callback}`). Both the span and
counter use `cao_dashboard.auth.callback.outcome` (`success` or `failure`);
failures additionally use a fixed, bounded `error.type` (`invalid_state`,
`missing_code`, `provider_denied`, `exchange_failed`, `authorization_failed`,
`session_id_generation_failed`, `csrf_generation_failed`, or
`session_save_failed`). Only server-side failures mark the span as an error;
raw exceptions and provider error descriptions are never recorded. This
separate instrumentation avoids exposing callback request metadata. The callback
retains
`X-Trace-Id` and `X-Span-Id` correlation headers; no OAuth code, state, cookie,
token, login, provider message, query string, or other user identifier is
added to its telemetry. Authorization rechecks start a
`cao_dashboard.auth.revalidate` span before the protected request is served;
failed callbacks and rechecks emit `cao_dashboard.auth.decision` events and
`cao_dashboard.auth.decision.count` (unit `{decision}`). Only fixed
`cao_dashboard.auth.operation` (`callback_cleanup` or `session_revalidation`)
and `cao_dashboard.auth.outcome` values are recorded, distinguishing accepted,
rejected, concurrent, and failed rechecks from revoked, queued, or failed
callback credential cleanup. No account, membership, token, cookie, or provider
message is attached to these signals. The query engine and ingestion paths
start dedicated `cao_dashboard.query.execute` and `cao_dashboard.ingest.run`
spans. Every Postgres statement emits a child `cao_dashboard.postgres.query`
client span and `cao_dashboard.postgres.query.count` (unit `{query}`) and
`cao_dashboard.postgres.query.duration` (seconds) metrics. Duration measures
statement execution after acquiring a connection, not pool wait or row
decoding. Existing CAO-specific metrics record fixed `db.operation.name`
(`select`, `insert`, `update`,
`delete`, `copy`, or `other`) and `cao_dashboard.postgres.outcome` (`success`
or `error`) values are recorded; SQL, parameters, error messages, source
names, and database connection details are excluded. Existing pgx tracers
are preserved. PostgreSQL queries and ingestion `COPY` calls additionally emit
the standard [`db.client.operation.duration`](https://opentelemetry.io/docs/specs/semconv/db/database-metrics/)
histogram (unit `s`, recommended boundaries
`0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10`). The standard metric and client
spans use `db.system.name=postgresql` and uppercase `db.operation.name`
(`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `COPY`); unrecognized SQL operations
omit the standard operation attribute. Existing CAO-specific metrics retain
their lowercase operation and outcome attributes for compatibility.
Failures add `error.type`, using a validated PostgreSQL SQLSTATE and
`db.response.status_code` when available, otherwise `canceled`, `timeout`, or
`_OTHER`. The histogram duration matches the client span duration.

Redis calls emit the same standard duration histogram with `db.system.name=redis`
and allowlisted uppercase commands, alongside client spans named for those
commands. Unknown commands use `_OTHER`. Pipelines produce one observation per
call, named `PIPELINE <COMMAND>` for homogeneous batches or `PIPELINE` for mixed
or empty batches; a one-command pipeline is a single operation. Batch sizes are
span-only `db.operation.batch.size` attributes, not metric dimensions. Redis
duration includes connection acquisition, authentication, retries, and response
reading; recovered retries count as one successful operation. Failures add
`error.type` and, for allowlisted Redis error prefixes, `db.response.status_code`.
Other failures use `canceled`, `timeout`, or `_OTHER`. SQL text, Redis keys,
values, Lua scripts, raw error messages, database indexes, and connection
details are excluded. These signals use the existing optional OTLP providers;
no additional exporter or dependency is required.

The GitHub API quota service (`internal/githubquota/`) starts
`cao_githubquota.<operation>` spans (`observe`, `commit`, `reserve`, `release`,
`park`, `unpark`, `state`, `select`, `usage`) and records
`cao_githubquota.operation.count` and `cao_githubquota.operation.duration`
by operation, bucket App and resource, and a fixed `cao_githubquota.outcome`.
Per-bucket `cao_githubquota.bucket.remaining`, `.reserved`, `.available`, and
`.parked` gauges are keyed by App, installation, and resource. Reservation IDs,
tokens, and free-form parking reasons are never recorded. Its debug logs use the
`cao:githubquota` and `cao:redis:githubquota` namespaces. Quota failure spans use
bounded `error.type` values (`invalid_request`, `canceled`, `timeout`, `_OTHER`)
and fixed status descriptions, never raw exception events. Invalid bucket
identities are excluded from spans and metrics until validation succeeds.
MCP requests use the OpenTelemetry MCP semantic conventions, including
`mcp.method.name`, `mcp.protocol.version`, `gen_ai.operation.name`, and
`gen_ai.tool.name`; tool arguments, results, session identifiers, untrusted
tracestate and baggage are never recorded. MCP methods are allowlisted and
protocol versions must have the standard date shape. Error spans use fixed descriptions
and bounded classifications rather than raw exception messages. Application
attributes are limited to non-secret aggregate counts, revisions, durations,
operation and row counts, rate-limit cost, and structural operator counts. Query
names, source names, fields, predicates, literals, route parameters, result
values, Redis URLs, credentials, GitHub tokens, and row contents are never
recorded. Identifiers follow the W3C Trace Context specification: the tracer
provider installs `propagation.TraceContext` so a client-sent HTTP `traceparent`
continues an existing transport trace. MCP spans use trace context from
`params._meta.traceparent` as their remote parent and link the ambient HTTP span
without retaining its tracestate. OAuth callback and revalidation spans use the
same traceparent-only extraction; all request boundaries discard inherited
baggage and tracestate while preserving cancellation and trace correlation. Every API
response also echoes the active request's ids as `X-Trace-Id` / `X-Span-Id`
headers for correlating a client-visible request with exported spans.
JSON error responses repeat those identifiers as `traceId` and `spanId`, and
the bounded server error log records the status plus the same identifiers.
Browser error views show only the trace ID as a request ID; they do not expose
span attributes, internal exceptions, query payloads, or storage details.

Telemetry uses standard OpenTelemetry SDK environment variables for exporters,
plus `CAO_OTEL_LOGS_ENABLED` for the additional log-export opt-in. Traces and
metrics are independently optional; logs additionally require that opt-in:

| Variable | Effect |
| --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Enables trace and metric OTLP/HTTP exporters and sets their shared destination; also supplies the log destination when log export is enabled. |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | Enables the OTLP/HTTP trace exporter and sets its destination. Spans remain no-ops when neither this nor the shared endpoint is set. |
| `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` | Enables the OTLP/HTTP metric exporter and sets its destination. In addition to CAO application instruments, the exporter reports stable Go runtime metrics including `go.memory.allocated`, `go.memory.used`, and `go.goroutine.count`. |
| `CAO_OTEL_LOGS_ENABLED` | Set to `true` to export `DEBUG`-selected server log namespaces when a log endpoint is configured. Stderr behavior is unchanged. |
| `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` | Log-specific OTLP/HTTP destination; takes priority over the shared endpoint and includes the complete `/v1/logs` path. |
| `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_EXPORTER_OTLP_TRACES_HEADERS`, `OTEL_EXPORTER_OTLP_METRICS_HEADERS`, `OTEL_EXPORTER_OTLP_LOGS_HEADERS` | Authentication headers read directly by the corresponding OTLP exporter. Supply them through the deployment platform's secret manager; never place values in command-line arguments, checked-in configuration, or logs. |
| `OTEL_SERVICE_NAME` | Overrides the default `cao-dashboard` `service.name` resource attribute. |
| `OTEL_RESOURCE_ATTRIBUTES` | Adds deployment-selected resource attributes; only configure reviewed, non-identifying values. Hostname detection is not enabled by default. |
| `OTEL_SDK_DISABLED` | Set to `true` to keep all providers as no-ops even when endpoints are configured. |

The stable upstream Go runtime instrumentation does not currently emit a
GC-pause distribution. A missing GC-pause metric therefore means that signal
is not instrumented, not that pauses are zero.

There is no Azure-specific exporter linked into the binary. To ship telemetry to
Application Insights, point `OTEL_EXPORTER_OTLP_ENDPOINT` at an OpenTelemetry
Collector configured with the `azuremonitorexporter`; supply that collector's
Application Insights connection string from its secret manager. The
`APPLICATIONINSIGHTS_CONNECTION_STRING` on the Function App does not activate
Go exports or configure an external collector. The Azure template supports an
explicit `otlpEndpoint`, `otlpLogsEnabled`, and secure `otlpHeaders` overlay for
the Function App and optional collection/backfill roles. See the
[Azure telemetry route and deployment receipts](../docs/deployment-azure.md#explicit-go-telemetry-route).
The Function App itself never imports an Azure Monitor SDK, and telemetry
delivery is not a request-readiness dependency.

## Security boundaries

- All data APIs except minimal readiness require a cryptographically random
  capability token. The bootstrap URL stores it in origin-scoped
  `localStorage`, cleans the current URL without navigating, and browser requests attach it
  explicitly as an `Authorization: Bearer` header. The token is never placed in
  a host-scoped cookie.
- Only loopback listeners are accepted.
- Non-loopback `Host` headers are rejected to reduce DNS rebinding exposure.
- Redis configuration is accepted only by the CLI and is never serialized into
  HTML, browser configuration, API payloads, or URLs.
- Plaintext `redis://` URLs default to literal loopback IP addresses or
  `localhost`. Hosted mode additionally accepts a private IP or single-label
  service hostname only when the Redis module sets
  `allow-private-plaintext: true`.
- Every other remote Redis connection requires `rediss://`, standard
  certificate-chain and hostname verification, and TLS 1.2 or newer. Azure
  always requires this path. There is no insecure skip-verification option.
- Redis namespaces isolate this server's operational keys, but are not a
  substitute for dedicated Redis credentials with narrow ACL key patterns or a
  dedicated Redis database or instance.
- The HTTP(S) server sets content-type, frame, referrer, permissions, and
  cross-origin resource policy headers.
- Request bodies, query structures, row counts, output counts, and operation
  counts are bounded.
- Deployed artifact paths and SHA-256 hashes are validated before parsing.
- Static files are served only from the configured built-site directory, with
  SPA fallback to that directory's `index.html`.
- Missing Redis, Postgres data, manifests, shards, or diagnostics fail
  closed.

The local capability profile is not suitable for remote or multi-user
deployment. The capability authorizes its holder to read the full active
dashboard data; it provides no user identity or per-source authorization.

The Azure Functions profile is the experimental remote profile. Run
`cao-dashboard azure-functions` to start its custom HTTP handler using
`FUNCTIONS_CUSTOMHANDLER_PORT` and the existing Azure environment settings.
It calls `NewAzureFunctionsHandlerFromEnv` in the shared server package;
`serve` does not enable this profile. Azure
mode fails closed unless all of the following are configured:

- a readable `cao.json` with the `azure-functions` target and a Redis provider;
- the policy-selected Redis URL environment variable with a `rediss://` URL;
- the policy-selected Redis namespace environment variable;
- `CAO_AZURE_ALLOWED_HOSTS` and HTTPS forwarded-protocol enforcement;
- GitHub OAuth App or GitHub App client ID/secret and redirect URL;
- at least 32 characters of `CAO_SESSION_SECRET`;
- at least one explicit `CAO_GITHUB_ALLOWED_ORGS` or
  `CAO_GITHUB_ALLOWED_TEAMS` value.

For Linux-only local integration testing, the repository-owned harness starts
Azure Functions Core Tools, Azurite, and Redis and drives the Functions HTTP
surface without Azure credentials:

```bash
./scripts/azure-local/azure-local.sh run
```

See [`scripts/azure-local/README.md`](../scripts/azure-local/README.md) for
prerequisites, individual lifecycle commands, logs, process isolation, and the
local-vs-cloud test boundary. The harness sets the explicit
`CAO_AZURE_LOCAL_SIMULATION=1` seam, which permits only loopback plaintext Redis
and local HTTP. Without that setting, Azure mode continues to require
`rediss://` Redis and HTTPS exactly as production does.

Azure mode does not accept the local bearer capability and does not support
PATs. Login alone does not grant access: after exchanging the OAuth code, the
server verifies active organization or team membership. OAuth App clients
request `read:org read:user` for membership and disclosed 2FA-enabled evidence;
GitHub App clients (`Iv...`) request `offline_access` for
expiring user tokens and server-side refresh even if the App's global
expiration setting is disabled. For a GitHub App, configure organization
**Members: read** on the registration; scopes do not grant App permissions.
Install the App in each organization whose membership authorizes dashboard access.
This scope requires GitHub.com or GHES 3.23 or later. The browser sign-in App
need not be the collection or workflow App, and its credentials do not grant
campaign authority. Access and refresh tokens remain server-side,
encrypted before storage in Redis, and are never written to browser-readable
storage, URLs, API payloads, or Bicep outputs. Session cookies are `Secure`,
`HttpOnly`, and `SameSite=Lax`; mutating API requests must include the
session-bound CSRF token.

`GET /api/v1/events` is available through Azure Functions only while the
platform keeps the invocation alive. Clients must treat SSE as best-effort and
fall back to `/api/v1/refresh` because Functions instances may cold-start,
scale in, or terminate long-running requests. Cold starts rebuild the Go app
from app settings and check Redis before serving requests.
Request cancellation propagates through `request.Context()` to Redis queries.

The Bicep deployment in `server/azure/main.bicep` provisions a Function App,
Key Vault, Application Insights, storage, and module-free Azure Managed Redis.
Every secret-bearing app setting—including Functions runtime storage—uses a
versionless Key Vault reference so ordinary credential rotation
does not require rewriting application configuration. Session-key rotation uses
the optional secure `previousSessionSecret` deployment parameter: deploy the old
key as previous and the new key as current, wait for active sessions and queued
revocations (including expired-session cleanup) to drain, then remove the
previous key. Unknown-expiry GitHub tokens have no local time-based drain
deadline: retain their key until successful revocation or independently
confirmed provider expiry. New session records preserve cleanup ownership
after session authority expires, so outstanding debt consumes protected
capacity and can backpressure sign-in. Stop old replicas before rolling out
the new Redis session-envelope format, preserve the stable revocation prefix,
and reconcile pre-upgrade sessions/abandoned grants separately. Do not treat
deployment or the local session TTL as provider revocation. See
[OAuth cleanup and rotation](SECURITY.md#github-oauth-and-authorization) for
outage ownership and restart limitations. Encrypted records carry a
key identifier, and the server can read both keys during that window. The
template outputs only non-secret host names, redirect URI, Redis database name,
and Key Vault URI. Redis access keys
are an unavoidable path for Azure Managed Redis client authentication today;
store the `rediss://` URL in Key Vault, rotate the Redis key in Azure, publish a
new Key Vault secret version, and allow the platform to refresh the reference.

### Azure secure-computing baseline

Treat the Bicep file as the minimum secure baseline for remote dashboard
hosting:

- Do not use PATs. Azure mode supports only the GitHub OAuth
  authorization-code flow and configured organization/team authorization.
- Keep Key Vault mandatory for every secret-bearing value, including the GitHub
  OAuth client secret, `CAO_SESSION_SECRET`, and `CAO_REDIS_URL`. Do not replace
  Key Vault references with literal app settings, deployment outputs, or
  checked-in parameter files.
- Use the Function App's system-assigned managed identity with Key Vault RBAC
  to read secrets. Do not copy Key Vault secret values into logs, telemetry,
  tickets, dashboard documents, or browser-readable configuration.
- Keep HTTPS-only Functions, TLS-only Redis, disabled FTPS, disabled Redis
  public network access, storage HTTPS enforcement, Key Vault soft delete, and
  non-secret Bicep outputs enabled for compliance review.
- Treat the Postgres entity store as rebuildable from authoritative inputs. Compliance evidence
  comes from the checked-in Bicep, GitHub OAuth authorization policy, Key Vault
  access controls, Azure activity logs, Application Insights without secrets,
  and the CAO source artifacts that feed Redis.
- Rotate OAuth, session, storage, and Redis credentials through Key Vault and
  Azure platform controls; then restart the Function App so current secret
  versions are resolved.

Do not remove the experimental designation until the hosted profile has passed
a deployment-specific security review, compliance review, load/cost validation,
incident-response exercise, backup/rollback exercise, and Azure network-access
review for the target tenant.

See [`SECURITY.md`](SECURITY.md) for the complete protection model, operational
guidance, limitations, and private vulnerability-reporting process.

## Run locally

The module targets and pins Go 1.27.3.

On a MacBook, install Homebrew first and run the idempotent project setup:

```bash
npm run dashboard:server:setup:macos
```

The command installs Homebrew Go, Node.js 24, the Docker CLI, Docker Compose,
Colima, and the dashboard npm dependencies. Docker and the optional
PostgreSQL/Redis stack in `server/docker-compose.yml` are not required for the
default SQLite profile; the setup does not install or start a native Redis
service.

From the repository root, start with the default SQLite cache:

```bash
npm --prefix dashboard/site ci
npm run dashboard:server:build

go -C server run ./cmd/cao-dashboard serve
```

The server downloads the deployed snapshot if missing and reuses it offline on
later starts. To exercise PostgreSQL instead, start the optional database stack,
configure `CAO_POSTGRES_URL`, and provision storage as described in
[`POSTGRES.md`](POSTGRES.md) before ingesting:

```bash
docker compose -f server/docker-compose.yml up -d

go -C server run ./cmd/cao-dashboard ingest \
  --source /absolute/path/to/deployed-dashboard

go -C server run ./cmd/cao-dashboard serve --postgres-url="$CAO_POSTGRES_URL"
```

To inspect local traces and metrics, start the optional
[OpenObserve self-hosted instance](https://openobserve.ai/docs/getting-started/)
on `http://127.0.0.1:5080` via the separate `server/otel-compose.yml`.
It is not started by the Redis/Postgres Compose stack.
Set an email and a locally held password (8–128 characters with uppercase,
lowercase, digit, and special characters) before its first startup
(do not commit them or put them in command-line arguments):

```bash
read -rp 'OpenObserve email: ' CAO_LOCAL_OTEL_EMAIL
read -rsp 'OpenObserve password: ' CAO_LOCAL_OTEL_PASSWORD; echo
export CAO_LOCAL_OTEL_EMAIL CAO_LOCAL_OTEL_PASSWORD
npm run dashboard:server:otel-up
```

In the same shell, configure the Go server's OTLP/HTTP exporters before
running `serve` or `ingest`. OpenObserve requires Basic authentication and
uses signal-specific endpoints; the shared base OTLP endpoint would append
the wrong paths. The credentials are needed again after a restart to export
telemetry, even though OpenObserve only uses them for account creation on
first startup.

```bash
export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://127.0.0.1:5080/api/default/v1/traces
export OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://127.0.0.1:5080/api/default/v1/metrics
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic%20$(printf '%s' "$CAO_LOCAL_OTEL_EMAIL:$CAO_LOCAL_OTEL_PASSWORD" | base64 | tr -d '\n')"
export OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=local
go -C server run ./cmd/cao-dashboard serve
```

To exercise both exporters from the local Go server integration test (after
starting OpenObserve and setting the variables above in the same shell):

```bash
CAO_LOCAL_OTEL_INTEGRATION=1 go -C server test ./internal/telemetry -run '^TestLocalOpenObserveExport$' -count=1
```

The test skips during ordinary `go test ./...` runs; when enabled it fails if
either the trace or metric export is rejected or unreachable.

Sign in to OpenObserve with the same credentials and select the `default`
organization. The local instance stores data in the `openobserve-data` Docker
volume; `docker-compose -f server/otel-compose.yml down` stops it without
deleting that volume. Do not use `down -v` unless you intend to erase its data.
For hosted deployments, supply exporter authentication through the deployment
secret manager instead of exporting it from an interactive shell.

### PostgreSQL backfill integration

From the repository root, run:

```bash
npm run test:integration:dashboard-backfill
```

The harness requires Go, Docker, Compose (plugin or standalone), curl, and
OpenSSL. It starts isolated PostgreSQL, Redis, and OpenObserve containers using
`server/backfill-compose.yml`, generates ephemeral credentials, waits for
readiness, and starts a real loopback Go HTTP server inside the test. Random
host ports and a unique Compose project avoid interfering with existing local
services. All containers and their disposable data are removed on exit; your
normal dashboard and OpenObserve volumes are untouched.

The replay suite uses the checked-in deployed snapshot and the production
GitHub App client against the local synthetic API and fault proxy; it needs
no GitHub token or live repository access. It verifies cold-start
native rows and relationships, durable repository/run admissions, idempotent
reruns and enumeration checkpoints, replay after PostgreSQL projection loss
without GitHub access, empty-lake rejection, and preservation of committed rows
and HTTP readiness after a hash-valid malformed replacement. A repaired replay
must clear the retry marker without advancing an unchanged data revision.
OpenObserve searches must find ingestion, PostgreSQL, and query spans, match
the HTTP response's W3C trace ID, and contain positive PostgreSQL and backfill
queue/deduplication metric samples. Fault tests exercise paginated resume after
5xx, malformed JSON, unauthorized responses, connection loss, timeouts,
primary/secondary limits, bootstrap/token-mint failures, and cancellation.
Missing quota headers must leave unknown request cost reserved.

If the OpenObserve image registry rate-limits its pull, the harness reports the
reason and runs all PostgreSQL/Redis replay, fault, and stress assertions without
OpenObserve. Only telemetry export, searches, and telemetry artifacts are
disabled for that run. The pull log is retained as `openobserve-pull.log`.
Other image-pull errors and service readiness failures remain fatal.

Service-backed replay/fault tests also run during ordinary Go tests when
Postgres and Redis endpoints are configured. Outside the explicit registry
rate-limit fallback, OTEL runs fail rather than skip when a required service
or exporter configuration is missing. The
separate **Postgres backfill and OpenObserve integration** job in
`.github/workflows/cgo.yml` runs the same harness and uploads Go results and
service logs to `go-server-backfill`. Local reports are in `.tmp/go-backfill/`
(override with `CAO_BACKFILL_REPORT_DIR`).

The previous collection-only replay test is consolidated into this suite.
Collection/HTTP tests share a private-schema Postgres fixture; profile parity
compares actual native rows and quality metadata, not only counts.

### Synthetic enterprise backfill stress

The daily `ci-daily.yml` workflow invokes `backfill-stress.yml` at 06:37 UTC with separate jobs for
1,000, 10,000, and 50,000 repositories. Every repository has at least
one synthetic run per day in 14 days of API history; the real backfill admits
only the configured seven-day window. The reusable workflow also supports manual
dispatch, but has no PR or push trigger. Scheduled runs append a combined report and
artifact links to the tracking issue; the test jobs remain read-only, and a
separate reporting job owns issue-write permission. Local runs can select
more daily runs within the five-million-history-run safety bound.

```bash
CAO_BACKFILL_REPOSITORIES=10000 CAO_BACKFILL_RUNS_PER_DAY=1 \
  npm run test:stress:dashboard-backfill
```

The harness streams a seven-day canonical evidence lake into fresh Postgres,
uses the production client/quota/admission paths to discover durable historical
run tasks, and concurrently delivers signed, shuffled, duplicated, and replayed
webhooks through the live Go HTTP handler. It checks every durable run identity,
rejects out-of-window or duplicate admissions, verifies native row counts and
bounded native HTTP queries, and requires webhook/backfill overlap and an
idempotent rerun. At one run per day, the expected totals are 7,000, 70,000,
and 350,000 durable run tasks. Retained canonical detail is capped independently
at 200,000 Runs (or less under linked-source/byte pressure). This measures lake recovery and historical discovery/
admission, not `gh aw logs` artifact downloads or run-task worker execution.

The 50,000 case crosses the native query engine's 200,000-input-row boundary.
Publication evicts oldest complete Run subtrees instead of disabling fresh
Run queries or raising query guards. The harness verifies recent Runs across
the fleet and an exact retained-detail count. Durable run-task identities still
match the complete seven-day discovery dataset; canonical counts describe only
retained evidence.

#### Monthly agent budget scenario

The same reusable workflow also runs a separate monthly PostgreSQL scenario
as part of `ci-daily.yml` (daily at 06:37 UTC), or on manual dispatch:

```bash
# Disposable local PostgreSQL; the test creates and removes a private schema.
POSTGRES_URL=postgres://postgres@127.0.0.1:5432/cao_test?sslmode=disable \
  npm run test:stress:monthly-budget
```

It simulates **30 complete UTC days across 10,000 repositories**, spending at
most **$25,000 per day**. Run costs are reproducible integer cents drawn from a
half-normal bell: `$1 + abs(N(0, $1.25))`, rejecting values above $5 rather than
clipping the tail. The mode is $1; each day stops before its next run would
exceed the budget, with less than $5 left unused. Every repository runs at least
once per day. The fixed seed is 42. No agents, inference, or live spending occur.
USD is fixture accounting used to determine volume, **not a conversion to AIC**;
canonical AIC and token values are independently synthetic.

The ingestion clock advances through 30 daily snapshots without waiting for
wall-clock days. Each snapshot streams the observed workload through the
production native PostgreSQL writer, with a run-owned Audit record per Run
and the ordinary **30-day Run / seven-day linked-detail retention**.
The real partition-maintenance path runs at every simulated rollover.
Independent SQL verifies exact retained identities, daily fleet/run totals and
decimal AIC sums, structural parents, complete recent audit bundles, no expired
audit bundles, and no orphan records. Age-expired detail is not replayed into
later snapshots; capacity-evicted Runs are replayed to prove that old history
does not displace newer evidence or duplicate daily counts.

After the month, the clock walks one second before and exactly onto the oldest
retained Run's expiration boundary, repeats maintenance, then advances beyond the last
Run's retention window. It checks that cutoff days survive until expiration,
whole expired subtrees disappear, and the database still serves bounded fleet
queries after all Runs expire. Daily snapshot queries check the native
Dashboard Language boundary rather than a test-only query implementation.

Production PostgreSQL writes enforce **200,000 rows per Run-owned source and
256 MiB of combined logical typed-row bytes**. Oldest complete subtrees leave
atomically while fresh Runs keep arriving. Structural inventory, independent
Operational Value, operational state, and daily contribution receipts/counters
are not capacity-evicted. Execution status does not pin disposable canonical
detail; writes that cannot fit the newest bundle fail and roll back.
Coverage becomes partial and population sampling weights become invalid rather
than presenting retained detail as complete history.

Exact retained-Run queries, statistical queries, fleet inventory, and additive
daily health must remain usable throughout the month. The test never accepts
query unavailability as a successful pressure response. Unchanged daily
contribution receipts are skipped during reconciliation instead of repeatedly
rewriting their counters; expired, unreceipted execution days are excluded before
contribution updates. Receipt comparisons use indexed, analyzed 512-key snapshots
to avoid quadratic cold-start joins as receipts grow within a transaction.
Fresh statistics precede reconciliation, and counts,
clocks, and capacity measurements use separate source-local plans to avoid
partition-horizon planner amplification. This is not proof of full-month detail retention,
browser ingestion, agent concurrency, or GitHub API throughput; the logical
byte boundary is not a physical disk, transaction-peak, or RSS guarantee.

The gate has a 35-minute deadline and a 512 MiB post-ingestion Go heap cap
(not an RSS or ingestion-peak guarantee). `monthly-budget.json` includes each
day's budget ledger, cost histogram, retained counts, revision, boundary state,
heap measurement, and duration. Actions retains it and the Go log in
`postgres-monthly-budget-stress`; local output defaults to
`test-results/monthly-budget-stress/` (override with
`CAO_MONTHLY_BUDGET_REPORT_DIR`). A failed run without a completed report is
explicitly incomplete, never passing evidence.

All simulated GitHub traffic is pinned to the exact numeric-loopback
origin/proxy, and inherited GitHub credentials are removed. A default live
GitHub base URL or a redirect to another port is blocked before dialing.
Postgres/Redis/OpenObserve use isolated containers and random loopback ports.

OpenObserve evidence is downloaded before teardown on success or failure:
bounded recent traces, correlated summary/debug logs, and counter samples.
Root backfill spans include window, phase, repository/run counts, and failure
counts; a lifecycle counter and duration histogram provide low-cardinality
timing evidence. The `backfill` CLI now initializes and flushes the standard
OTEL exporters too. Reviewed namespaces are enabled only by the local harness;
production logging remains opt-in. Reports and service logs are uploaded by
the workflow or retained in `.tmp/go-backfill/` locally.

Concurrent installation webhooks and backfill share an enrollment mutation
lease. Backfill waits cancellation-safely for that lease; enrollment ownership
reads/writes are pipelined in bounded batches so a large installation does not
hold it across hundreds of thousands of serial Redis round trips.

### Local serving

`serve` prints a capability URL such as:

```text
http://127.0.0.1:8443/?access_token=<random-token>
```

Open that exact URL. The server removes the token from the address bar after
initializing origin-scoped browser local storage. Refreshes continue to work
without restoring the token in the URL. Use `--access-token` with a
value of at least 32 characters only when deterministic automation requires it.

To ingest into PostgreSQL and serve in one process, configure and provision
`CAO_POSTGRES_URL` first; `--source` explicitly selects that profile:

```bash
go -C server run ./cmd/cao-dashboard serve \
  --source /absolute/path/to/deployed-dashboard \
  --access-token 0123456789abcdef0123456789abcdef
```

### Local MCP endpoint

The Go server can expose the dashboard's reviewed, read-only agent catalog and
named queries on the same listener:

```bash
go -C server run ./cmd/cao-dashboard serve --mcp-enabled
```

`/mcp` is not registered unless `--mcp-enabled` is present. Clients authenticate
with the same bearer capability as the local JSON APIs.

In GitHub Actions, `serve` also reads the standard `GITHUB_TOKEN` and
`GITHUB_ACTOR` environment variables. When both are available, an MCP client can
send the token in the bearer `Authorization` header and the actor in
`X-GitHub-Actor`.
The configured pair is accepted at `/mcp` and, only when
`CAO_SERVER_LOGS_ENABLED=true`, at `/api/admin/logs`; it does not grant access
to other JSON APIs. Before enabling this gate, the server probes the current
`GITHUB_REPOSITORY` through `GITHUB_API_URL` and fails closed unless the token
can read Actions runs, contents, issues, and pull requests. Grant exactly these
permissions:

```yaml
permissions:
  actions: read
  contents: read
  issues: read
  pull-requests: read
```

Keep the token in the Actions environment rather than passing it on a command
line.

The endpoint exposes `cao_catalog` and `cao_query`, plus `cao_logs` when
`CAO_SERVER_LOGS_ENABLED=true`. The `cao_logs` tool takes no arguments and
returns the same `logs` and `redis` snapshot as `GET /api/admin/logs`, using
the same administrator-or-validated-Actions authorization. Its schema is in
the shared MCP contract's `serverTools` section; browser WebMCP and the
Node snapshot MCP server continue to expose only the catalog and query tools.
The endpoint accepts no SQL, arbitrary query definitions, refresh, rebuild,
webhook, administrative mutations, or repository mutation operations.

Hosted deployments can opt in with `serve-hosted --mcp-enabled`. This does not
expose the local bearer capability or grant access to other APIs. The hosted
`/mcp` endpoint accepts administrator GitHub OAuth sessions with
`X-CSRF-Token`, or GitHub Actions callers with a bearer `GITHUB_TOKEN`
and a GitHub Actions OIDC token in `X-GitHub-OIDC-Token`. Mint the OIDC token
with `id-token: write` and the audience `https://cao.githubnext.com`; grant
the Actions token `actions: read`, `contents: read`, `issues: read`, and
`pull-requests: read`. The server queries the repository's default branch and
verifies the signed OIDC source ref and subject against it, along with repository
identity and the Actions token's repository read permissions, before
serving MCP requests. Keep both tokens in the Actions process environment,
never in a URL, commit, or log. Browser access continues to use the hosted
GitHub OAuth session. Non-administrator browser sessions cannot access `/mcp`.
MCP responses use `Cache-Control: no-store`, including log snapshots.

Set `CAO_SERVER_LOGS_ENABLED=true` to retain up to 10,000 recent `DEBUG`-selected
server records in memory and mount `GET /api/admin/logs`. The response is an
`application/json` attachment containing `logs` (an array of timestamped log
objects) and `redis` (availability, fixed numeric ingestion counters, and,
when collection is enabled, queue depth, pending tasks, and dead letters).
Redis keys, event details, credentials, and raw values are excluded. When Redis
is unavailable, the log download still succeeds with `redis.status` set to
`unavailable`. The response uses `Cache-Control: no-store`. Hosted access requires either an authorized
administrator session or the same bearer GitHub Actions token and OIDC
provenance required by hosted MCP; local access uses the dashboard bearer
capability or the configured Actions token and actor. The buffer covers the
current process only and does not include logs emitted before it was enabled.
On the hosted GitHub-authenticated dashboard, administrators can open **Settings → Logs →
Server logs** to inspect this snapshot as a full-page, preformatted view and
refresh it. The entry is absent on GitHub Pages and the local server, or when
the endpoint is disabled or access is denied.
These process-local diagnostics are not canonical dashboard data, so the
settings viewer reads the protected runtime endpoint rather than a Dashboard
Language data query.

Use `--redis-url` and optional `--redis-namespace` only on the server command
line. The same namespace must be supplied to `ingest` and `serve` when
overriding the checkout-derived default:

```bash
go -C server run ./cmd/cao-dashboard serve \
  --redis-url redis://127.0.0.1:6379/0 \
  --redis-namespace local-dashboard
```

Use verified TLS for non-local Redis:

```bash
go -C server run ./cmd/cao-dashboard serve \
  --redis-url rediss://redis.example.com:6379/0 \
  --redis-namespace production-dashboard
```

Verify the local endpoint:

```bash
curl http://127.0.0.1:8443/api/v1/health

curl http://127.0.0.1:8443/api/v1/query \
  -H 'authorization: Bearer 0123456789abcdef0123456789abcdef' \
  -H 'content-type: application/json' \
  --data '{"sourceNames":["runs"],"queries":[]}'
```

Stop Redis:

```bash
docker-compose -f server/docker-compose.yml down
```

## Validation and CI

Install the pinned linter and run the local quality checks:

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
npm run dashboard:server:lint
npm run dashboard:server:test
```

The Redis-backed end-to-end test uses the deployed-format subset under
`server/testdata/deployed-subset`, starts the HTTP server, and verifies that
the Runs, Workflows, and Repositories views render populated rows. It also
visits every declared dashboard page against the Go server and checks that
visible views and their Redis-backed HTTP queries resolve. CI uploads a
per-page report and posts the results to same-repository pull requests:

```bash
docker-compose -f server/docker-compose.yml up -d
npm ci
npm --prefix dashboard/site ci
npm run dashboard:server:build
npx playwright install chromium
npm run test:e2e:dashboard-server
```

`.github/workflows/cgo.yml` keeps server validation separate from the static
dashboard CI:

- **Go format, lint, and tests** runs Go 1.27.3, golangci-lint v2.14.0, and the
  server unit suite.
- **Redis dashboard integration** starts Redis, runs the Redis integration
  tests, builds the dashboard and server, ingests the deployed shard subset,
  launches the localhost HTTP server, and executes the Playwright view assertions.
