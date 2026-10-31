# Server contracts

`server/spec/` is the editable TypeSpec contract for the Go dashboard server.
`main.tsp` imports independent domains: shared response/auth types, Dashboard
Language queries and SSE, health, agent discovery, hosted sessions, canonical
entities, repository memory, ingestion/administration, explicit workflow-value
publication, the optional organization Copilot requests token broker, and MCP HTTP
transport. MCP protocol messages and tool schemas, and static dashboard assets,
are intentionally out of scope.

`token-broker.tsp` specifies the OIDC-in-body exchange and job invalidation
protocol, independently of dashboard session authentication and activation
admission. It supports `copilot-request` only; no task credential is issued.

`storage.tsp` is a separate, **fresh-only** physical Postgres contract.
`storage.tspconfig.yaml` runs the repository-owned `postgres-emitter.mjs` to
generate `../internal/postgresx/schema.sql` and `schema.gen.go`. The SQL file is
standalone DDL for downstream implementations; the Go file supplies the same
entity names, fields, and native types to ingestion and reads. Both are generated
artifacts. Edit TypeSpec, never the generated SQL or bindings.

`adx.tsp` separately defines the optional ADX analytics export: five allowlisted
canonical fact families and the ready-version manifest, including native types,
nullable observations, presence metadata and contract version. Its
`@source` annotations name canonical collections; each own fact property maps
to the canonical field with a lowercase initial. `adx-emitter.mjs` generates
`../internal/adxingest/schema.gen.go` (the runtime projection allowlist) and
`schema.gen.kql` (tables, JSON mappings, streaming/retention policies and curated
functions). The Go CLI embeds that KQL for offline operator setup; runtime
never provisions ADX. Edit TypeSpec, not generated output. Run
`npm run generate:adx` for ADX alone; ordinary `generate`, `test` and `check`
include it. `adx.test.mjs` verifies canonical field/type compatibility,
deterministic generation and fail-closed rejection of opaque evidence.
This contract adds no PostgreSQL tables or public HTTP operations.

Each canonical collection has one root table. Only fields
consumed by `dashboard/site/src/data/queries/database.json` and its joins are
retained, plus identity/storage keys. Counters use `BIGINT`, fractional measures
use `NUMERIC`, booleans use `BOOLEAN`, timestamps use `TIMESTAMPTZ`, and identifiers
use `TEXT`. Optional-field presence costs one bit per scalar column, preserving
absent versus explicit null. Query-required structured evidence uses entity-owned
relational child values; no JSON, JSONB, serialized row documents, or duplicate
generic scalar rows are stored.

`@sampledBundle` declares private typed copies of canonical collections for
PostgreSQL's independent statistical snapshot. The emitter generates
non-partitioned `cao_sample_*` tables and a separate internal binding registry,
not additional canonical ingestion sources. `Storage.Statistical` declares typed
frame, membership, and stratum metadata; copied quality and structural parents
keep statistical queries independent of mutable canonical detail. Snapshot
publication replaces these tables atomically under the namespace writer lock,
with whole-bundle admission limits. Incremental writes only make the publication
stale; a complete snapshot rebuilds its seven-day frame. Exact retention, browser
and SQLite sampling, hot-tier eviction, and daily contributions are unchanged.

`operationalValueEvidence.facts` is an entity-owned typed array. The emitter
generates its private relational child table and native scalar bindings.
The canonical packet and all its facts publish in one transaction;
`operational-value-evidence` projects the child facts with their original
packet identity, source path, contract digest and capture/cutoff timestamps.
It never stores report HTML, counts, deltas, movement or attention labels.
Changing this physical contract requires a fresh PostgreSQL schema.

`value.tsp` describes the bounded administrator publication endpoint, not a
collector execution API. `cao_value_publications` holds typed packet ownership
metadata only; original facts remain in the canonical entity and child tables.
Snapshot replacement and direct publication share the namespace writer lock,
preserving owned evidence and structural parents. Recurring collection is
separately selected by reviewed policy and invokes only trusted local code.

The `Storage` namespace describes the current source metadata, auxiliary
sources, revision, and diagnostics scaffolding, separately from entity models.
It is not an alternate canonical representation. Startup only initializes this
schema; there are no old-layout migrations or backward-compatible imports.
Use a fresh database when changing its physical layout.

`cao_github_resources` retains allowlisted repository metadata and latest-release
fields in typed columns, with ETags and validation timestamps, outside canonical
evidence. It is namespace- and credential-partitioned, bounded to 256 rows per
namespace, and expires after 24 hours plus five minutes. The local preview keeps
its bounded resource cache in the same SQLite database as its dashboard data;
the browser worker keeps normalized receipts in the existing IndexedDB
transaction store. Process memory holds active subscriptions and in-flight
coordination only, not a separate resource cache. GitHub credentials stay on the
server. Browser resource requests use HttpOnly cookies; local Go bootstrap issues
a Secure, SameSite=Strict cookie restricted to read-only GitHub resource routes.
GitHub API requests execute exclusively on the local or hosted server and pin
`X-GitHub-Api-Version` to `2026-03-10`. The
browser worker only requests the dashboard's `/api/v1/github/` resource routes
(with an optional deployment prefix), rejects upstream API URLs, and refuses
redirects. Static Pages without a backend reports unavailable instead of
falling back to direct GitHub API access.
The resource service is read-only: both server adapters accept only `GET`
resource requests and issue only bodyless `GET` reads of allowlisted GitHub
resources (plus the hosted rate-limit probe). Callers cannot supply an upstream
method, request body, or arbitrary API path. Cache writes affect only the local
data stores, never GitHub repositories.

The local SQLite evidence mirrors likewise retain typed query columns and
observation metadata, but no `record_json` copy. The complete document lives
once in `__idb_records.value`. Outdated mirrors are rebuilt transactionally
from those canonical records, never from a second stored representation.
Canonical normalization and writes also prune unused raw Run payload copies,
Workflow declaration copies, Issue lifecycle aliases, and the unused
`isSkill` flag. The explicit exclusions live in
`dashboard/site/src/data/model/fields.js`; provenance and unrecognized
evidence remain intact.

Query-reconstructible values are excluded from TypeSpec and canonical writes.
Workflow stores only an optional `campaignId` foreign key to
`campaigns(namespace, id)`. Campaign metadata and the slug are joined from
Campaign; gh-aw version labels are
computed from installed/current versions. Grader/eval source IDs are joined
from definitions independently of their names, result timestamps are projected from one stored
timestamp, and Audit target coordinates are concatenated from their components.
Browser-only repository coordinate strings and Eval answer/model copies are
also pruned; SQLite mirrors do not retain derived assignment/result observation
ranges. Source copies may supply missing underlying facts during ingestion,
but conflicting copies fail rather than silently overwrite them.
Physical identity/integrity keys and independent historical observations are
not display projections and remain stored. Retained row counts are calculated
from entity tables in the same namespace-scoped read snapshot, not stored
in quality metadata. A physical contract change requires
a fresh PostgreSQL schema; browser IndexedDB rebuilds its disposable stores.

### Tool structure assessment

The current logical `tools` collection is execution-event evidence: one
invocation can emit a start, result, error, or policy-block observation.
A future SQL dictionary can separate repeated observed tool identity
(`server`, `name`, tool type, server/protocol versions) from event facts
(`runId`, correlation ID, timestamp, status, request/response sizes, latency,
and provenance). Dictionary identity must be namespace-scoped and distinguish
versions and missing values, not key on tool name alone.

Do not rename those events to `toolCalls` or merge them into one invocation
without a correlation and aggregation contract: doing so changes counts,
ordering, failure evidence, and size measurements. Preserve the existing
logical query source through a SQL join if introducing a physical dictionary.
Observed identities are not configured tool definitions; the latter require
independent workflow-declaration evidence. This split is an assessed follow-on,
not part of the current schema.

Jobs, sessions, and events are not stored as canonical entity tables.
The `Runtime` namespace declares typed CLI command history, summary, and
status projections for query planning, without canonical DDL or execution
endpoints. Hosted providers report these sources as unavailable: durable
operational queue storage alone does not grant local execution authority.

Run `npm run generate:storage` for storage only, or `npm run generate` for
storage, ADX and HTTP contracts. `storage.test.mjs` checks one-to-one table coverage,
required query fields, absence of speculative columns, and document-free DDL.
The follow-on removal and rebuild plan is [Postgres rebuild plan](../POSTGRES-REBUILD-PLAN.md).

The compiled `generated/openapi.json` is OpenAPI **3.1** and can be consumed by
another server implementation. Its `info.version` records the HTTP contract
version; review and update it when changing externally observable API signatures.
`generated/schemas/` contains JSON Schema 2020-12
documents for admission requests/allowances, conclusion requests and compaction
coverage, query definitions, requests and results, SSE data frames, and webhook
acknowledgements. Representative safe examples are embedded in the TypeSpec
models and emitted into OpenAPI/JSON Schema. `RevisionEvent.json` describes the
JSON *inside* each SSE `data:` frame; the stream itself is `text/event-stream`.
`POST /api/conclusion` acknowledges only after durable receipt and canonical
compact-session population; this uses existing typed Run/Audit/grader columns,
not a raw-session canonical table or complete-usage/billing assertion.
Canonical entity rows and GitHub webhook event bodies are deliberately open
objects because their fields are owned by the canonical data schema and GitHub
event types, respectively; do not infer Redis storage or MCP internals from them.

`blackbox-fixtures.json` records observable behavior outside the covered
TypeSpec operations: OAuth redirects and cookie attributes, trusted-proxy and
CORS decisions, SSE frame bytes, static asset fallback, and MCP discovery.
`go -C server test ./internal/server -run TestBlackbox` exercises those fixtures
through the complete HTTP handler (the SSE case needs `POSTGRES_URL` and skips
without it). The other server tests exercise hosted listener startup/drain and
additional edge cases. These fixtures are not an OpenAPI extension and do not
describe OAuth tokens or MCP protocol internals. Reuse the existing
`SourceReader` and `hosting.Service` seams when testing another implementation;
add a new port only when a second implementation or parity test needs one.

## Spec-writer workflow

1. Read the appropriate domain `.tsp` file, the registered handlers in
   `server/internal/server/server.go`, and the underlying handler/model types.
   For query vocabulary, also read `server/internal/query/types.go` and
   `specs/dashboard-data.md`. Keep new paths in the appropriate domain file;
   don't expand `main.tsp` into a monolith.
2. **Edit TypeSpec first** to propose the API change. Describe authentication
   (local bearer, hosted OAuth cookie, public, or signed webhook), request and
   response bodies, statuses, profile availability, and an illustrative
   non-sensitive example. Never model server-side OAuth tokens as API fields.
3. Update the Go handler and caller as needed to conform to the proposed
   contract. Spec changes alone do not deploy new endpoints: an implementation
   must pass the contract checks, and CI compares registered routes and key
   query fields with the generated artifacts.
4. In `server/spec/`, run `npm ci`, then `npm run generate` and `npm test`.
   From the repository root, `npm run dashboard:server:spec` runs the same
   TypeSpec generator after the spec dependencies are installed.
   Commit the `.tsp` source and refreshed `generated/` outputs together.
   `npm run check` regenerates and fails if the committed artifacts drift.
   Run `go -C server test ./...` when changing the implementation. The
   `server-spec` job in `.github/workflows/cgo.yml` runs on server changes.
   The root `dashboard:server:build` and `check` scripts also verify committed
   generated API signatures before proceeding; install spec dependencies with
   `npm --prefix server/spec ci` before running either command.

The Go server has **two authentication profiles**: the loopback local profile
requires an `Authorization: Bearer` capability for protected `/api/` routes;
hosted mode requires an authorized `cao_session` cookie and `X-CSRF-Token` on
mutations. Health/readiness and `GET`/`HEAD /llms.txt` are public, while the webhook
authenticates the raw body using `X-Hub-Signature-256`. Collection and quota
endpoints require an admin-authorized session in hosted mode. Hosted-only `/auth/` routes do not exist
in the local profile. `302` OAuth callback failures return an HTML error page,
not the JSON error body used by ordinary API endpoints. Limits and runtime
authorization rules are enforced by the implementation, not by JSON Schema.
In hosted mode, CAO may own the HTTP listener or supply its complete handler
to an external Go host. Before startup and after shutdown, the externally
hosted handler returns a `503` error body even for health/readiness. The host
must preserve SSE flushing and must not impose a finite whole-request timeout
on `/api/v1/events`.
