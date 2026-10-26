# ADR: Parquet storage for Agent Unified Sessions

## Status

Proposed - 2026-10-25.

This decision adds a private historical archive for full approved Agent Unified
Session evidence. It does not implement or activate capture, provision storage,
broaden collection authority, or authorize training.

It extends the storage choices described in the accepted
[unified agent traces ADR](unified-agent-traces-in-adx.md), reusing its evidence
model and safeguards. It does not supersede that decision or replace the
existing [native ADX snapshot](../server/ADX-INGEST.md) and
[trace export](../server/ADX-TRACES.md) profiles. Their implementation and
activation requirements remain unchanged.

## Context

Agent Unified Sessions contain the event sequences needed to investigate agent
behavior, tool failures, model usage, harness changes, and execution outcomes.
Useful investigations require messages, tool inputs/results, and other approved
structured evidence, not only summary metrics.

CAO's dashboard deliberately normalizes and bounds retained evidence. Neither
its canonical PostgreSQL rows nor its public browser JSONL shards can reconstruct
full sessions after that loss. The existing trace architecture instead captures
approved session evidence at the acquisition boundary before normalization and
workspace cleanup.

We need efficient historical storage for that evidence in two major scenarios:

| Scenario | Durable trace storage | Analytical engine |
| --- | --- | --- |
| Local server | Private local Parquet files | DuckDB |
| Hosted server | Private Azure Blob Parquet objects | ADX external tables |

Parquet provides the shared columnar representation. DuckDB and ADX provide query
execution; they are not the archive's source of truth. Scale depends on measured
file layout, query pruning, destination capacity, and resource limits, not the
file format alone.

## Decision

Archive full approved unified-session evidence in immutable Parquet files.
Reuse one acquisition boundary and one versioned evidence contract, with small
deployment-specific storage and query adapters.

**Existing PostgreSQL is unmodified.** This feature adds no PostgreSQL tables,
columns, archive catalog, outbox, delivery ledger, query path, retention behavior,
or operational-state dependency. PostgreSQL is not the source of full traces.
Existing PostgreSQL-backed services continue operating as before.

Existing dashboard SQLite, browser IndexedDB, JSONL publication, and operational
queries also remain unchanged. The archive has its own private lifecycle.

### 1. Shared acquisition and evidence contract

Reuse the existing authorized visit to `usage/aw_session.jsonl`; do not add a
second collector or reconstruct sessions from dashboard records. Capture the
approved archive projection before lossy normalization and temporary evidence
cleanup. Archive enablement is independent of the existing ADX export switches:
local archiving must not require Azure or native ADX ingestion.

Reuse the parsing, identity validation, approval/redaction, and durable capture
mechanisms underlying the existing trace profile. Separate destination coupling
where necessary rather than cloning another parser, policy engine, or spool.
Activity may reuse this adapter when it owns an authorized acquisition visit;
it is not a third analytical deployment architecture.

The upstream Agent Unified Session schema owns event meaning.
[`server/spec/trace.tsp`](../server/spec/trace.tsp) owns CAO's existing trace
contract. Extend that contract and its generation tooling for the Parquet
projection, rather than maintaining independent JavaScript, Go, SQL, and KQL
field lists.

Preserve full approved events, including approved messages, reasoning only where
specifically permitted, tool inputs/results/errors, usage, routing, configuration,
and supplied lineage. "Full" means full approved acquired evidence, not lossless
native engine logs, unrestricted producer extensions, or complete upstream
coverage. Unsupported evidence, omissions, and redactions remain explicit.

Each archived event retains:

- Canonical namespace, execution repository, run, and exact attempt bindings.
- Immutable trace/projection identity, event identity, and original event order.
- Native type, supplied clocks and correlations, session and episode/hop context.
- Approved event payload with JSON types, explicit nulls, and content preserved.
- Source artifact, policy, schema, integrity, and coverage provenance.

Do not infer lineage from timestamps, copy another attempt's evidence, or equate
hashed trace identities with canonical Run IDs. Reuse existing full-versus-sampled
trace semantics and frozen membership; do not thin events or invent a new
sampling system.

### 2. Logical events and efficient physical storage

Use one logical event row with typed common analytical columns and the complete
approved event payload encoded as UTF-8 JSON. Promote frequently queried,
schema-defined dimensions and measures to typed columns through the shared
contract. Do not duplicate every payload field into another parallel column
model. Payload inspection remains available without eagerly decoding it for
ordinary metadata queries.

The approved logical event must be reconstructable; original artifact whitespace
and pre-redaction bytes are not promised. Native ADX streaming content parts are
a transport representation, not a required Parquet row layout. Preserve the
logical contract while avoiding unnecessary streaming-size fragmentation.

Partition by namespace, execution repository, and UTC completion day. Undated
sessions use an explicit undated partition, not an invented completion clock.
Pack multiple traces into immutable files; do not produce one file per event,
session, or small browser JSONL shard. Preserve trace sequence inside each
projection and store manifest membership sufficient to select complete traces.

Start with Zstandard compression and a target file size around 128 MiB; flush
smaller batches when needed for publication latency. Select row-group sizing,
trace admission ceilings, and hard resource limits using representative
benchmarks before activation. These are bounded implementation policies, not
claims of measured compression or reasons to add many operator controls.
Oversized traces produce explicit admission gaps, never truncated ready traces.

Reuse one pinned DuckDB CLI runtime and generated conversion definitions from
both JavaScript acquisition adapters and the Go host. Avoid introducing separate
JavaScript Parquet libraries and an embedded Go/CGo DuckDB dependency initially.
Conversion uses explicit types, not uncontrolled JSON inference; generated
artifacts are validated before publication.

### 3. Local server: local Parquet and DuckDB

The local server owns a private archive directory containing approved staging
evidence, immutable Parquet files, versioned manifests, and replay/withdrawal
state. Use the existing private filesystem/spool primitives where suitable,
without coupling local operation to ADX credentials or hosted PostgreSQL.

Start with one archive owner protected by an OS file lock. Persist manifests and
publication state through atomic replacement and appropriate durability barriers.
Recover interrupted staging and orphan files before admitting another writer.
The archive volume must support the required locking, rename, and durability
semantics; unsupported storage fails closed.

DuckDB reads the authorized published Parquet snapshot directly. Do not import
another full event copy into persistent DuckDB tables. Execute only trusted,
server-generated parameterized queries using bounded CLI subprocesses with
memory, threads, concurrency, temporary-disk, execution-time, and result limits.
Parameters cannot supply SQL syntax or filesystem paths.

Existing operational views continue to use the SQLite snapshot/overlay backend.
Analytical capabilities are additional and explicit. They do not replace local
dashboard storage or require Azure connectivity.

### 4. Hosted server: Blob Parquet and ADX

The hosted server reuses its existing authorized acquisition visit and the same
approved projection. A bounded private durable staging area and one archive
publisher convert and upload immutable Parquet objects to a dedicated private
Blob namespace. No full historical trace population is retained in PostgreSQL.

Keep publication manifests, delivery receipts, and replay/withdrawal state in
the archive's Blob namespace. Use immutable manifests and conditional publication
of the active pointer, with a Blob lease for the single archive owner and
restart overlap. Do not add a database or use existing PostgreSQL as an archive
coordination service.

The initial supported profile is a persistent host with a suitable converter and
durable staging lifecycle. Do not assume an ephemeral container filesystem or
per-request Azure Functions invocation supplies a durable spool or publisher.
Destination outage, capacity, or archive admission failures remain explicit
archive failures and must not claim readiness or alter core dashboard evidence.

ADX queries Parquet through external tables. Preserve the existing native ADX
profiles unchanged; enabling this archive must not also ingest the same full
trace bodies into native tables by default. A separately enabled native profile
remains an explicit operator choice with its own costs and lifecycle.

Hosted queries pass through the existing authenticated Go server and use trusted
KQL implementations. ADX and Blob identity permissions do not replace CAO's
caller authorization. Keep setup privileges separate from runtime access.

### 5. Publication, snapshots, and archive-owned state

Use the same logical publication protocol in both scenarios, implemented through
filesystem and Blob adapters:

1. Durably admit approved evidence within the archive's bounded staging budget.
2. Write immutable Parquet files and verify schema, identities, complete selected
   trace counts, ordering, and integrity.
3. Write an immutable manifest naming exact files and active projection
   membership, including supersessions and withdrawals.
4. Atomically publish the new active generation with a stale-writer check.
5. Acknowledge archive delivery and release staging only after durable
   publication. Recover retries without creating another logical projection.
6. Retire unreferenced data only after readers and the recovery grace permit it.

A query pins an authorized generation. Ordinary ingestion must not drain queries
or restart DuckDB. A generation exposes at most one approved projection per
canonical run attempt. Packed files may physically retain superseded rows;
trusted query plans must enforce active membership before analytical operations.
Avoid global per-query `DISTINCT` as a substitute for publication correctness.

**An ADX external table does not automatically honor a Blob manifest.** Hosted
activation requires a demonstrated generation-bound implementation that restricts
the query to selected objects and/or enforces the exact active membership without
exceeding bounded query budgets. A mutable directory wildcard alone is
insufficient. Do not copy the full archive for each generation to simulate
transactions. If this protocol cannot be demonstrated, hosted querying remains
unavailable; successful upload is not successful analytical publication.

Start with one archive/query-owning server replica per archive. Reader tracking,
remote query completion, cancellation, bounded lease expiry, restart recovery,
and a conservative retirement grace are part of acceptance. Blob lifecycle
expiration must not independently delete files still eligible for active
queries. Multi-replica coordination is deferred.

### 6. Independent retention, corrections, and withdrawal

The Parquet archive is retained approved evidence, not a disposable projection
of a browser cache. JSONL cache replacement or deletion does not expire archived
traces. Configure archive retention and local disk/Blob budgets independently
of dashboard detail and source-cache retention.

Corrections create immutable projections and publish new membership. Withdrawals
block replay and remove affected evidence from permitted active and historical
queries. Retain compact supersession/withdrawal fences for the supported replay
horizon; do not discard them while older input can still be admitted. If the
required fence state cannot be retained, stop admission or reject older inputs
instead of silently resurrecting history.

Ordinary replacement allows pinned readers to finish. Authorization revocation
and content withdrawal override that convenience: revalidate access and cancel
or deny affected queries. Already delivered results and external copies cannot
be recalled by a manifest update.

Garbage collection separates logical withdrawal from physical deletion and
accounts for staged files, obsolete generations, backups, Blob versions, and
derived exports. Retention policies are not a promise of immediate erasure.
Approve actual destination deletion capabilities and recovery horizons before
activation.

### 7. Access, analytical semantics, and compatibility

Keep full content private and behind separately approved trace-read access.
Repository-homogeneous files aid isolation but do not establish authorization.
Bind manifests and trusted query scope to validated canonical identities and
the caller's permitted repositories, runs, and projections; do not trust names
or caller-supplied filters. Do not expose broad Blob credentials, raw directory
access, or unrestricted ADX access through this query interface.

Reuse Dashboard Language validation and planning where applicable, adding only
analytical sources/operators required by concrete consumers. Discover dataset
availability and backend capabilities explicitly. Existing SQLite/PostgreSQL
queries remain on their current backends; no silent fallback or cross-store join
is implied. Shared metric semantics have backend-specific SQL/KQL implementations,
not a universal SQL-to-KQL compiler.

Preserve absent, null, unknown, and observed zero through the approved payload
and explicit projected presence metadata where needed. Preserve observed clocks,
units, source quality, and accounting grain. Reuse source precedence: do not sum
cumulative usage with per-request costs, count starts and completions as separate
calls, or duplicate run cost across episode hops. Success and accepted output
disposition are not proof of correctness or business value.

Version schema and projection rules explicitly. Additive and incompatible
changes need tested historical-read behavior, including DuckDB/ADX differences.
Unknown versions must not silently drop fields or produce success-shaped partial
answers. Cross-backend fixtures cover reconstruction, presence, ordering,
accounting, selected projections, sampling, and incomplete evidence.

### 8. Explicitly deferred scope

- Arbitrary agent-generated SQL/KQL. Trusted subprocess execution is not an OS
  sandbox; arbitrary SQL needs a separate isolation and non-bypassable dataset
  authorization decision.
- Compaction. Initial batching provides efficient files without a second
  replacement protocol. Add compaction only after measured file-count/query
  costs justify it and concurrent-reader correctness is proven.
- Distributed publishers, multi-replica query ownership, a transactional
  lakehouse platform, universal query compilation, and cross-store joins.
- New outcome acquisition, dataset training, replayed tool execution, or automatic
  operational changes. Existing outcome lineage can be referenced without
  modifying PostgreSQL or inventing another collector.

## Alternatives and consequences

| Alternative | Decision and rationale |
| --- | --- |
| Metadata-only exports or canonical PostgreSQL snapshots | Insufficient for full-session investigation; normalization already removed content. |
| Full traces in PostgreSQL | Rejected; existing PostgreSQL is unchanged and historical content belongs in the archive. |
| One Parquet companion per JSONL shard | Rejected; couples archival retention and analytical file sizing to browser/cache needs. |
| Separate local and hosted evidence models | Rejected; duplicates parsing, approval, identities, and compatibility work. |
| Native ADX ingestion as the only archive | Retained as an existing optional profile, not required for local analytics or this Blob-backed archive. |
| Persistent DuckDB event tables | Rejected initially; duplicates the historical population without improving portability. |
| Separate Parquet libraries for each producer | Rejected initially; share a pinned converter and generated contract instead. |
| Immediate compaction, arbitrary SQL, or lakehouse deployment | Deferred; adds protocols and operational surface before the basic archive is demonstrated. |

This choice adds a portable full-trace archive without changing operational
storage. Column pruning can avoid reading large payloads for metadata analytics;
full-content scans still incur their actual I/O and decoding costs. Tradeoffs
include temporary staging/JSONL duplication, DuckDB packaging, Blob and ADX
provisioning, archive-owned recovery state, sensitive-content governance, and
different SQL/KQL implementations. No production throughput, compression ratio,
cost, or complete-history guarantee is established by this ADR.

## Delivery and acceptance gates

Deliver a shared contract/converter foundation followed by independently
accepted local and hosted tracks. Local analytics must not wait for Azure;
hosted querying must not require embedded DuckDB query execution. Correctness,
resource, and recovery checks accompany each increment, not a final cleanup phase.

1. **Shared foundation:** representative approved sessions round-trip with
   ordering, JSON types, content, identity, gaps, and provenance intact. Validate
   explicit schemas and historical compatibility. Demonstrate unchanged
   PostgreSQL and browser/operational paths, no broader acquisition, and
   independently default-off archive activation.
2. **Local track:** demonstrate private durable capture, crash/retry recovery,
   packed immutable files, bounded trusted DuckDB queries, concurrent publication,
   reader-safe retirement, correction, withdrawal, replay fencing, and independent
   cache/archive expiration. Verify clean offline operation without Azure,
   PostgreSQL, or native ADX export.
3. **Hosted track:** demonstrate bounded durable staging, immutable uploads,
   conditional publication and lease recovery, correct generation-bound ADX
   execution, authorization, correction/withdrawal, and retention without
   PostgreSQL archive state. Verify actual destination suitability and execution;
   local simulators cannot establish live ADX publication semantics.
4. **Analytical utility and capacity:** compare shared DuckDB/ADX metric results
   and reconstructed approved events. Benchmark millions of representative events,
   realistic large payloads, file counts, and concurrent ingestion. Record
   latency, bytes read, conversion/upload throughput, peak memory and temporary
   disk, publication lag, and recovery behavior against explicit deployment
   budgets. No silent truncation or incomplete result may pass as complete.

Reuse existing structured logging and OpenTelemetry. Record archive conversion,
publication, gaps, retries, backlog, storage, query limits, and withdrawal status
without logging trace content or credentials. Enable a scenario only after its
own gates pass and its operator approves scope, content, destination, retention,
and resource budgets.
