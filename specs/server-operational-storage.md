# Server operational storage

`server/internal/operational/` owns the provider-neutral operational contracts.
PostgreSQL is the default implementation, providing deployment-scoped
restart-persistent operational services without requiring Redis.
Redis and bounded volatile single-process memory remain explicit alternatives.
Its operational adapter MUST remain separate from the canonical dashboard
entity/query adapter, tables, namespace, schema generation, and lifecycle.

## Contracts and composition

`Store` exposes immutable per-feature capabilities, typed services, read-only
health, one bounded cancellation-aware maintenance pass, and close. The
application owns maintenance scheduling and MUST stop owned work before close.
Hosted deployments MAY explicitly set `CAO_EXTERNAL_INGESTION=true`
when a separately authorized job owns canonical PostgreSQL publication. This
mode MUST reject a simultaneous source directory or collector and MUST NOT
ingest, reset or synthesize evidence during web startup. Omitted/false preserves
the existing source-or-collector requirement; invalid selections MUST fail.
Local snapshot rebuild/reconciliation is unavailable in this mode. Readiness
MUST reflect published canonical state, active revision subscriptions MUST
observe external publications, and OAuth revocation maintenance MUST run
independently of acquisition ownership. This selection grants no ingestion,
collection, dispatch or database-bootstrap authority.

`OperationalServices` separates backend lifecycle, cache, request limiting,
sessions, atomic session invalidation, revocations, leases, state, delivery
deduplication, task queue, atomic delivery admission, collection metadata,
GitHub quota, rate-limit state, health, ingestion metrics, and optional durable
CLI command queues. One backend MAY
implement multiple services; the split does not relax atomic transitions.
Only composition boundaries MAY consume `Store` and its
`OperationalServices()` bundle. Runtime consumers MUST retain the focused
contracts they actually use: OAuth separates sessions, atomic invalidation,
and revocations; collection separates metadata, leases, state, task queues,
delivery admission, deduplication, and metrics; the legacy rate-limit governor
separates rate-limit state from attribute reads. The obsolete `Services()`
bundle and aggregate OAuth, collection, quota, coordination, and diagnostic
interfaces MUST NOT be reintroduced as compatibility paths.
Shared adapter conformance MUST execute through these same focused services,
including transitions that span sessions and revocations or queues and delivery
admission. Raw Redis commands, physical keys,
streams, scripts, adapter assertions, and backend-name decisions MUST remain
inside adapters and construction/configuration code.

Each capability identifies unsupported, process-scoped, or deployment-scoped
coordination and volatile or restart-persistent state. Composition MUST reject
missing or typed-nil services and incompatible enabled requirements before
serving. Atomicity, compare-and-swap, owner-checked leases, isolation, context
cancellation, and explicit errors are required semantics, not optional flags.
A future backend MUST implement these contracts and their conformance tests;
it MUST NOT add backend branches to request, OAuth, quota, or collection logic.

### PostgreSQL operational adapter

`server/internal/operational/postgres/` implements every typed service. It owns
only `cao_operational_*` tables, not canonical entities or query execution.
Reviewed `operational-store.backend: "postgres"` selects it explicitly, and a
reviewed host with neither a selector nor legacy Redis configuration defaults
to PostgreSQL. An existing explicit Redis configuration MUST remain Redis;
credentials alone MUST NOT change selection.
`postgres.url-env` MAY reference a server-side connection secret; omission uses
`CAO_POSTGRES_URL`. Redis configuration and volatile-memory options MUST NOT
be combined with this backend. Non-loopback PostgreSQL TCP transport and all
fallbacks MUST use verified TLS: certificate-chain and hostname verification
MUST be enabled (`sslmode=verify-full`), and a configuration that skips
verification or omits the server name MUST be rejected. Encryption alone
(`sslmode=require` or `verify-ca`) MUST NOT satisfy this requirement. Unix
socket, `localhost`, and loopback-address hosts are exempt. Selection MUST NOT change scope, enable collection,
move the canonical namespace, migrate existing operational state, provide
automatic fallback, or dual-write.

Transactions MUST use database time and serialize multi-record transitions and
accounting through the namespace row. Protected records and disposable caches
MUST have independent byte/entry bounds. Replica capacity configurations MUST
match the existing namespace; mismatch MUST fail startup. Session admission
MUST reserve capacity for atomic invalidation/revocation staging. Capacity
failures MUST roll back delivery identity, task append, and replacement
together. Expired sessions and leases MUST never grant authority. Accepted work,
enrollment, checkpoints, and pending revocations MUST NOT be evicted by cache
pressure or maintenance.

Session writes and CAS MUST include the deployment's opaque cleanup namespace.
Session TTL MUST expire authority without deleting the final encrypted
credential owner. Expired reads and CAS MUST fail closed even before cleanup
runs. `PendingRevocation` MUST atomically transfer a bounded batch of expired
sessions into that namespace's protected revocation queue. Invalidation and
direct deletion MUST also transfer ownership rather than erase credentials;
failed transitions MUST retain the previous owner. Maintenance MUST NOT delete
expired session credentials or their reserved cleanup destination. Capacity for
the configured destination MUST be reserved at admission, including bytes and
entries; pending debt MUST backpressure admission instead of being evicted.

Provider expiry MUST be recorded separately from local access, refresh, cookie,
and session deadlines. Unknown/nonexpiring credentials and legacy records
without provider evidence MUST remain cleanup debt until revocation succeeds.
Expiry alone MAY release a record only when every present credential has a
provider-confirmed elapsed expiry. Rotation MUST NOT copy a predecessor's
provider expiry to a different credential. Missing decryption keys and provider
or persistence outages MUST NOT complete or evict pending cleanup.

Blocking queue reads MUST release their transaction between bounded polling
attempts and honor cancellation. Maintenance MUST be application-owned and
bounded to at most 512 expired protected records and 512 expired cache records
per pass. Operational durability depends on the database's persistence
configuration; it does not reconstruct every external GitHub delivery.
Diagnostics MUST open existing state without creating tables or namespaces.
Production runtime MUST also open pre-provisioned state without DDL. Only an
explicit schema-owner bootstrap operation may create namespaces, migrate the
operational contract, or replace audit functions/triggers. Runtime credentials
MUST NOT own tables/functions, inherit privileged roles, create permanent
objects, change triggers, truncate audit history, or destroy namespaces.
Canonical bootstrap and partition maintenance MUST be separate operator jobs;
runtime MUST validate the canonical contract and provisioned partition window.
The fixed canonical statistics capability MAY grant runtime ANALYZE without
granting ownership or caller-selected SQL/object names.
The shared conformance suite and real PostgreSQL cross-connection tests MUST
cover atomicity, isolation, capacity rollback, restart, and cancellation.

OAuth logout MUST remove active authority and stage encrypted credentials
atomically. Refresh MUST compare the encrypted session version and MUST NOT
resurrect a session superseded by logout. Conditional revocation completion MUST
not remove a newer record. Redis preserves its existing namespace, TTL, and
stable pending-revocation prefix contracts, including the Upstash restrictions.

Collection admission MUST atomically reserve capacity, consume delivery
identity, and enqueue or coalesce work. Full capacity MUST NOT consume the
delivery. Ready, leased, delayed, and dead-letter state MUST be bounded without
trimming accepted work. Replacement or delayed retry MUST commit before removing
the original lease, in one atomic transition. Delay MUST NOT block unrelated
ready work. Enrollment changes MUST serialize and preserve current ownership
when repositories transfer between installations.

The PostgreSQL adapter also provides the `AuditLog` service in
`cao_operational_audit_events`. Appends MUST serialize on the namespace row,
assign a gap-free per-namespace sequence, and chain
`hash = SHA-256(prev_hash || record)` over the exact stored canonical record. A
trigger MUST reject updates and MUST reject deletes except owner-authorized
retention and namespace removal. A namespace-delete guard MUST check the
initiating caller before foreign-key cascades assume owner identity.
Runtime MUST have only SELECT/INSERT on audit history; evidence readers MUST
have only SELECT. Audit retention MUST NOT run in ordinary `Maintain`.
A separately granted SECURITY DEFINER function MUST pin a safe search path,
qualify its tables, revoke PUBLIC execution, reject nonexistent namespaces,
and accept no caller-supplied cutoff or object name.
Audit rows are not charged to protected-record capacity;
retention maintenance MUST keep at least 400 days, delete at most 512 rows per
pass as a contiguous oldest prefix, and always keep the newest row. Redis and
volatile memory do not provide this service, so hosted CLI actions require
PostgreSQL. `server/POSTGRES.md` defines operator provisioning and rollout.
`specs/server-hosted-cli.md` §7 defines the events.

For local debugging only, `server/internal/operational/sqlite` provides the
`CLICommandQueues` and `AuditLog` contracts in one SQLite file (mode `0600`,
WAL, immediate transactions) shared by `serve-hosted --hosted-cli-sqlite` and
`execute-cli --sqlite` on one host. It is not an `operational.Store` backend,
has one global namespace and no capacity accounting, uses host-clock
timestamps, and its append-only triggers can be dropped by anyone who can
write the file; the hash chain still detects edits that do not rewrite every
later hash. `specs/server-hosted-cli.md` §2 restricts it to loopback
listeners.

## Administrative rebuild evidence

Hosted administrative rebuilds MUST require a durable `AuditLog`, in addition
to existing access, administrator step-up and CSRF checks. Redis and memory
alone do not supply this service: their hosted rebuild endpoint MUST return
unavailable, not substitute a JSON log for durable admission. This restriction
does not change automatic collection, webhook reconciliation, startup ingestion,
or their existing coordination/publication rules. Loopback bearer rebuilds MAY
retain the local non-durable exception; `local-bearer` identifies a capability,
not a verified person, and status MUST NOT claim durable evidence.

After acquiring the existing owner-checked rebuild lease and reading canonical
revision state, the handler MUST record `admin.rebuild.admitted` before
persisting running status or admitting background work. The worker MUST record
`admin.rebuild.started` before calling its reconciler. Either audit failure MUST
prevent reconciliation. Every admitted lifecycle MUST attempt a bounded,
cancellation-independent `admin.rebuild.completed` append, including status
admission failure, service drain, start-audit failure, reconciliation failure,
timeout, cancellation, and success. Events MUST retain the initiating
authenticated login, a fresh server-generated `rebuild_id` independent of the
private lease token, prior revision, timestamps, state, fixed reason code and
returned revision/data revision on success. Active trace correlation MUST
survive asynchronous execution. Events MUST NOT include credentials, request
payloads, source rows, collection counts, or raw errors.

Denied hosted administrator decisions MUST continue to record `admin.denied`
with actor, reason and available trace correlation. Audit failure MUST leave
access denied and return unavailable; a confirmed durable append with a
mirror-only failure MAY retain the normal denied response.

Canonical publication, operational status and audit history are separate
commits, not one cross-store transaction. A terminal audit failure after
publication MUST NOT roll back canonical data, suppress revision notification,
or retry ingestion. Status MUST distinguish the operation's outcome from its
`auditState`: `pending`, `recorded` (durable), `local` (no durable guarantee),
or `unavailable`. Status write failures MUST be logged and independently
attempt `admin.rebuild.status_failed`; audit failures MUST be logged and flow
through existing audit failure telemetry. Confirmed durable appends MUST NOT
be invalidated by mirror-only failures; joined storage/mirror failures MUST NOT
be treated as mirror-only success.

Replaceable status is not the historical trail. Operators MUST forward and
verify committed audit rows through the existing independent evidence reader,
export/delivery and external-anchor facilities. Process death, uncertain audit
commit errors, and simultaneous store outages can leave an admitted/started ID
without terminal evidence. No automatic replay or read-time status repair may
invent its outcome. Operators MUST reconcile these IDs against canonical
revision state and independent telemetry, preserve the resulting incident
evidence, and monitor `stored=false` audit events and missing terminal events.
A subsequent rebuild MUST use a new ID and MUST NOT erase retained history.

### Read-only missing-terminal detection

`cao-dashboard audit lifecycles` provides a bounded operator check through
`OpenAuditReader` and `ListAudit`. It MUST NOT append audit events (including
its own result), acquire leases, replay work, write status, mutate canonical
data, perform maintenance, or infer a cross-store transaction. PostgreSQL
operators MUST use the independent SELECT-only evidence role. `--sqlite`
uses the existing read-only local debugging adapter, not a production substitute.

The check reads ascending, unfiltered sequence pages of at most 1,000 rows,
up to `--max-events` (default 10,000; hard maximum 100,000) plus one lookahead
row. Per-ID summaries and output are bounded by that row budget. A whole-call
`--timeout` (default two minutes; one second to thirty minutes) bounds reads.
It MUST NOT invoke unbounded hash-chain verification inside that budget.
Chain verification, independently enrolled external anchors and complete
committed-row delivery remain separate requirements; this check does not
authenticate history or detect a consistent whole-chain rewrite.

The default grace is one hour; `--grace` accepts thirty-five minutes to seven
days. The minimum exceeds the current twenty-five-minute work timeout and
thirty-minute lease, allowing terminal cleanup and clock margin. Age uses the
first observed stored event timestamp, not replaceable status, a caller's
`started_at`, or the most recent duplicate. Within grace, an unresolved ID is
`pending` (possibly active, not proven active). Beyond grace and after reaching
the observed tail without an interior sequence gap, it is `missing_terminal`
(missing evidence, not proof of execution failure or death). A later terminal
append can resolve the observation; operators SHOULD reread before disposition.
Success, failure and cancellation terminal states are all recognized.
`admin.rebuild.status_failed` MUST NOT substitute for a completion. Duplicate
admissions, starts or completions, conflicting terminal states/revisions,
unobserved admission, invalid terminal state/outcome, invalid timestamps,
clock uncertainty and unexpected event order require explicit diagnostics.
Conflicting terminals MUST NOT select a winning outcome.

The JSON result reports `status`, observation time, grace, observed sequence
range, rows scanned, `tail_reached`, `history_complete`, aggregate completed,
pending and missing-terminal counts, coverage issues and per-ID findings.
It excludes actors, raw errors, arbitrary attributes, rows and payloads.
`ok` means no actionable issue among the observed retained rows; it does not
attest execution, liveness, integrity, independent delivery or compliance.
`attention` identifies missing evidence or per-ID uncertainty.
`incomplete` identifies a row cap, read failure, invalid identity, selected
suffix or history gap. Only `ok` exits zero. Invalid options or inability to
open the reader fail without claiming a successful observation.

Retention can remove an oldest prefix, and `--after` deliberately omits one.
Both MUST report incomplete historical coverage even if the retained tail is
reached. Observed admissions/starts can still have missing terminal evidence
within a contiguous suffix; identities wholly outside it remain unknown.
An interior gap or row cap MUST NOT classify unresolved IDs as missing
terminal: a completion may be in unread history. `last_sequence` is an
observation boundary, NOT a safe monitoring checkpoint. Operators MUST revisit
unresolved admissions; blindly continuing from the last sequence can lose them.
For history beyond the hard bound, use independently retained committed-row
evidence with an approved correlation pipeline; a selected suffix is not a
complete-history workaround.

Operators own execution cadence, independent result retention, alert routing,
missing-check heartbeats and reconciliation. No scheduler or service is enabled
by this command. The response procedure is documented in
[`server/README.md`](../server/README.md#missing-rebuild-terminal-evidence).

## Durable CLI command queue service

`OperationalServices.CLICommands` exposes `CLICommandQueues`, whose
`OpenCLICommandQueue` returns a focused `CLICommandQueue`. It is separate from
the collection `TaskQueue`. PostgreSQL advertises deployment-scoped restart
persistence; Redis and memory leave this capability unsupported. A composition
requiring CLI commands MUST set `Requirements.CLICommands` and reject an absent,
typed-nil, unsupported, or volatile service. Storage support MUST NOT enable
hosted execution, expose new HTTP actions, bypass declared-action validation or
trusted approval, or provide credentials, quota admission, or subprocess control.
The existing local-admin/canvas SQLite queue remains unchanged. Hosted
execution is a separate opt-in contract defined by
[`server-hosted-cli.md`](server-hosted-cli.md).

Queue configuration is immutable across connections. Commands and ordered
stdout/stderr chunks live in namespace-isolated `cao_operational_cli_*` tables,
separate from canonical entities and disposable caches. Per-queue limits MUST
bound jobs, input, chunks, output bytes, concurrency, execution deadlines, and
retention, in addition to the namespace's protected byte and entry budgets.
Capacity failures MUST roll back identity and output accounting atomically.
An explicit output-truncation result and retained truncation flag distinguish
intentional bounded output from storage errors.

Enqueue accepts only caller-validated, approved invocations. Callers MUST remove
credential values known to their execution environment and frame/redact output
before storage; the service additionally rejects recognized credential patterns,
invalid UTF-8, and NULs. Credentials MUST NOT enter queue configuration,
invocations, output, or diagnostics. Reusing an idempotency key MUST return the
same command only for the same invocation and retry ancestry. Retry creates a
new command only from failed or timed-out work; the caller MUST revalidate its
current declaration and obtain fresh approval before enqueueing it.

Attributed invocations MUST encode a positive numeric GitHub `ActorID`
(`actorId` in invocation JSON) alongside their `Actor` login. Unattributed
local invocations MAY omit both. PostgreSQL and SQLite MUST preserve the ID
through detail, list/history, and claim reads. Idempotency fingerprints MUST
include it; the same login with a different ID is a different requester.
A fresh retry MAY replace both attribution fields in the new command, but
MUST NOT mutate the original identity or any action/input/target fields.
Invocation JSON is stored opaquely, so this addition requires no SQL schema
or namespace-version change. Old records without the field MUST remain
readable as unknown identity; storage MUST NOT infer or backfill an account ID
from a login. The hosted executor's explicit failure and re-admission policy
is defined in `server-hosted-cli.md` section 4.2.

Only a live database-time owner lease and its monotonic fence MAY claim FIFO
work, append output, finish, or settle execution. Owners MUST renew their lease
and abort executors if renewal fails. Expiry MUST NOT be interpreted as proof
of subprocess termination. A replacement owner marks interrupted running work
failed, preserves all uncertain execution claims, and blocks starts until
`AcknowledgeRecovery` explicitly records operator reconciliation. Stale owners
MUST NOT renew, append, settle, or acknowledge recovery.

Cancellation and timeouts retain execution claims until `Settle` confirms that
the executor exited; late completion MUST NOT overwrite a terminal state.
History filtering and counts MUST precede bounded SQL pagination. Output
replay MUST use a persistent sequence cursor. Browser detachment does not own
execution lifetime. `Retain` and application-owned `Maintain` remove at most
512 fully expired terminal commands per pass, preserving partial cutoff weeks
and every active or uncertain claim and cascading only their owned output.

## Volatile memory exception

Memory requires explicit `single-process` and `allow-volatile` acknowledgements
in reviewed host configuration. HTTP, OAuth, quota, collection workers,
projection, and backfill MUST share one instance in one process. Multiple
goroutines MAY process work, and enabled collection MUST configure at least one
co-resident worker. Multiple replicas, platform-owned listeners,
separate collector/backfill processes, and admission-only memory topology MUST
be rejected. OAuth, HTTPS, trusted-host/proxy validation, organization/team
authorization, explicit administrators, secure cookies, CSRF, signatures, and
quota governance remain mandatory.

All operational state is lost on restart: sessions, pending login state,
revocations, limits, enrollment, delivery deduplication, accepted unfinished
tasks, leases, quota reservations, and checkpoints. Old session/CSRF cookies
and OAuth callbacks MUST NOT restore authority. This invalidates CAO sessions;
it does **not** expire or revoke GitHub-issued tokens. GitHub revocation is
best-effort while the process lives and pending retries are lost on restart.

This is an explicit exception to restart durability and deployment-wide
coordination requirements in the durable ingestion and rate-limit
contracts. At-least-once admission/processing applies only within one process
lifetime. It is not a distributed or durable deployment. The evidence lake
remains persistent authoritative evidence, not an operational journal.

The adapter MUST bound cache accounting bytes and entries independently of
protected-state accounting bytes and entries. These are accounting budgets,
not allocator/RSS measurements. Disposable-cache pressure MUST NOT evict
protected state. Active limiter buckets MUST NOT be evicted to reset limits.
Session admission MUST preserve capacity for its atomic revocation transition.
Exhausted capacity MUST fail explicitly rather than silently discard work.

## Unified Go serving modes

The Go `cao-dashboard` executable is the serving runtime. There is no Node
local-dashboard CLI or JavaScript Go launcher.

| Invocation | Runtime | Operational authority |
| --- | --- | --- |
| `serve` | Go local SQLite snapshot and same-origin HTTP query API | Loopback bearer profile; no hosted collection or OAuth |
| `serve-pages` | Go static serving of an explicitly prepared Pages artifact | No API or operational services; browser IndexedDB |
| `serve-hosted --operational-store redis` | Go hosted service | Resolved reviewed host policy selecting Redis |
| `serve-hosted --operational-store memory` | Go hosted service | Resolved reviewed host policy selecting memory with every volatile-memory guard above |
| `serve-hosted --operational-store postgres` | Go hosted service | Resolved reviewed host policy selecting the durable PostgreSQL operational adapter |

The selector MUST NOT rewrite, synthesize, or override rollout policy. The
requested backend MUST match the backend resolved by the production Go policy
loader, including deployment-profile imports. A mismatch, invalid backend,
missing policy, invalid topology, or missing required credentials MUST fail
closed. Legacy `host.redis` remains an explicit Redis selection;
neither Redis nor memory may become an
automatic fallback. PostgreSQL is the default for selector-free reviewed hosts.

Hosted policy is selected through `CAO_POLICY_PATH` or the supported
`CAO_MARKETPLACE_POLICY_PATH` configuration. The Go loader remains the sole
authority for profile composition and configuration validation. npm commands
invoke `go -C server`; their relative server paths resolve from that module,
not from the repository root.

Hosted mode requires PostgreSQL for canonical entities and query execution,
the built dashboard, and the ordinary hosted OAuth and HTTPS/proxy protections.
Serving MUST NOT substitute the local bearer-capability profile
for hosted authentication. `--site`, `--listen`, and paired
`--cert`/`--key` MAY configure the built site and listener without relaxing those
protections. A direct TLS listener MUST use its actual TLS transport without
requiring forwarded headers unless trusted proxy CIDRs are explicitly
configured. A loopback listener without direct TLS retains the existing
trusted-proxy profile; an HTTPS-looking header from an untrusted peer MUST NOT
authorize access. Explicit `serve-pages` MUST serve prepared static bytes
without injecting hosted bootstrap, authentication, or API routes. It MUST
accept only loopback listeners and confine file access to its selected site;
directory listings and escaping symlinks MUST be rejected. It MUST NOT become
a fallback for a failed hosted or SQLite service.

`dashboard/canvas-server.mjs` remains an internal, loopback-only authoring
bridge. It has no standalone CLI, admin mode or process-replacement facility.
Its command queue requires paired trusted execution and approval callbacks;
read-only canvas authoring MUST NOT initialize a command queue.
The Go application MUST cancel and join owned workers before store close.

## Restart and manual recovery

Before volatile collection can acknowledge scope-dependent deliveries or start
workers, it MUST reconstruct current scope from fresh GitHub App enumeration.
Enumeration MUST serialize with webhook enrollment mutations. Partial or failed
enumeration MUST keep admission retryable; absence from partial evidence MUST
NOT authorize erasure or an ignored out-of-scope response.

Complete enumeration MAY repair missed scope withdrawals by comparing prior
enrollment and identities read from retained raw shard contents. Filenames and
browser IndexedDB MUST NOT establish repository identity or authority. Erasure
MUST fail closed if raw shard identities conflict with each other or their
layout, or if current and retained identities collide under the lake's lossy
filename prefix, before scope mutation or destructive deletion. Erasure
MUST take the same owner-checked repository lease as collection. Retained
evidence is replayed through the existing projector and canonical ingestion.
Unknown quota MUST require fresh GitHub observations before spending capacity.

The ordinary protected administrative rebuild action MUST rerun full scope
reconstruction and backfill for a volatile collector even when its lake is
populated. Existing signatures, administrator checks, CSRF, and bounded
asynchronous execution remain unchanged. Backfill checkpoints advance only
after a page is admitted; capacity exhaustion or unavailable history MUST
report incomplete work, never successful truncation.

Recovery repairs current observable state and available workflow-run history.
It cannot recreate every historical webhook ordering, expired artifact,
lost revocation, or unavailable GitHub observation. Operators accepting memory
MUST accept these limitations. Memory has no disk journal, Azure orchestration,
automatic fallback, dual writing, or hidden PostgreSQL persistence.

Public health/readiness MAY expose provider-neutral capabilities and bootstrap
readiness, never keys, record values, credentials, or endpoints. The legacy
`redis.connected` response is retained as a deprecated compatibility alias for
the selected operational dependency; `operational` is the neutral health field.

## Formal model

[`tla/ServerOperationalStorage.tla`](tla/ServerOperationalStorage.tla) models
browser/Redis/memory selection, immutable policy matching, memory topology
guards, bootstrap-gated admission, bounded atomic delivery/task admission,
owner-checked work, admitted backfill checkpoints, session epochs, drain/close,
and restart. The Redis and memory configurations check safety and conditional
bootstrap/shutdown progress separately. The model preserves evidence across
both modes but deliberately permits loss of acknowledged unfinished work and
session authority when memory closes or restarts.

The bounded model is an abstraction, not a proof of the implementation or of
external GitHub availability, signature verification, encryption, TLS, storage
durability, complete historical replay, or retained-shard identity validation.
The PostgreSQL adapter is covered by implementation/conformance tests, not by
the Redis/memory selection model.
See [`tla/README.md`](tla/README.md) for bounds, assumptions, commands, and the
implementation/test correspondence.
