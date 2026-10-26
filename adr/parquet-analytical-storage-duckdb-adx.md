# ADR: Parquet-Based Analytical Storage with DuckDB and Azure Data Explorer

## Status

Proposed — 2026-10-25.

**Project:** GitHub Central Agentic Ops (CAO)

**Scope:** Activity ingestion, analytical storage, local server, ADX integration,
dashboard query language.

This record proposes an architectural direction, not an implemented feature or
authorization to enable collection, export, or live rollout. Existing normative
contracts remain authoritative until separately updated.

Related contracts and decisions:

* [Activity specification](../specs/activity.md).
* [Dashboard data specification](../specs/dashboard-data.md).
* [Server ingestion specification](../specs/server-ingestion.md).
* [Optional ADX session analytics](../server/ADX-INGEST.md).
* [Unified agent traces in ADX](unified-agent-traces-in-adx.md).
* [Three-tier operational storage](three-tier-operational-storage.md).

## Context

CAO collects execution data from GitHub Agentic Workflows across potentially
thousands of repositories and millions of workflow runs.

The current architecture uses JSONL shards, SQLite/PostgreSQL operational storage,
a declarative dashboard query language, and an optional Azure Data Explorer (ADX)
integration.

These mechanisms support operational reporting, but large-scale analysis of Agent
Unified Session events requires more efficient historical storage and analytical
query execution.

We want to support questions such as:

* Which agent behaviors correlate with successful outcomes?
* Which tools, models, or harness configurations produce excessive failures or costs?
* Are agents repeatedly executing inefficient sequences of actions?
* How do performance and grader results change across harness versions?
* What can we learn from thousands of executions to improve future runs?

The solution must support both local CAO deployments and hosted environments
without requiring a cloud analytics service for local operation.

CAO already generates and manages JSONL shards. These should remain compatible
with existing browser and IndexedDB consumers.

We want to add Parquet as an efficient, portable analytical representation without
creating another independent ingestion pipeline or unnecessarily increasing
PostgreSQL storage.

## Decision

Adopt Apache Parquet as CAO's shared historical analytical file format, with two
query engines:

* **DuckDB:** Local analytical queries over Parquet files.
* **Azure Data Explorer:** Optional hosted analytical queries over Parquet files
  stored in Azure Blob Storage.

Keep existing JSONL shards for backward compatibility and browser consumption.

Parquet files are derived from the existing canonical data and share the same
shard identities, partitioning, and lifecycle.

Extend CAO's declarative query language with analytical capabilities, while
allowing backends to advertise which operations they support.

### 1. Canonical data and Parquet projection

The Agent Unified Session schema remains authoritative for event definitions.

Create one logical normalized Parquet events dataset.

Each event becomes one row containing:

* Canonical repository, run, attempt, and session identities.
* Event identity and sequence number.
* Event type, name, and timestamp.
* Common analytical dimensions.
* Tool identity, execution status, and correlation identifiers.
* Model and harness identity when available.
* Duration, token accounting, and AIC cost when available.
* Schema-defined event-specific structured fields.
* Link to the originating GitHub Actions run.

Use typed columns and nullable fields.

Preserve all supported structured analytical data, but exclude raw prompts,
message bodies, tool inputs, and tool outputs.

The projection must be deterministic, versioned, and derived from the existing
schema rather than maintained as an independent event definition.

The same projected records must be interpretable by DuckDB and ADX.

### 2. Dual-format CAO Activity shards

Extend the existing JavaScript-based CAO Activity collection process.

Maintain the existing JSONL shard generation and retention behavior.

Add a corresponding Parquet file for each eligible JSONL shard.

Example:

```text
runs-2026-10-25.jsonl
runs-2026-10-25.parquet
```

The Parquet filename and logical identity derive from the JSONL shard.

Use the DuckDB CLI to generate Parquet from JSONL, avoiding additional JavaScript
Parquet dependencies.

The CLI must use an explicitly defined projection schema, not uncontrolled JSON
type inference.

Generate the Parquet file when:

* Its JSONL source exists and Parquet is missing.
* The JSONL source has changed.
* The projection schema version has changed.

Write to a temporary file, validate the result, and atomically publish the
completed Parquet file.

Use the existing shard metadata or content digests to detect stale files.

### 3. Shared local shard lifecycle

JSONL remains authoritative for CAO Activity's local shard inventory.

Parquet follows the same lifecycle:

* JSONL creation may trigger Parquet creation.
* JSONL replacement invalidates the corresponding Parquet file.
* JSONL deletion removes its Parquet companion.
* Reconciliation removes orphaned or outdated Parquet files.

Do not introduce another retention database or independent shard management
system for Activity.

CAO Activity operates as a batch producer and does not host DuckDB queries. It
therefore does not need to coordinate its local cleanup with a DuckDB query
process.

### 4. Go local-server ingestion

The local Go server also ingests workflow runs independently, including through
webhook-driven download queues.

Reuse the same Parquet schema and logical shard conventions.

The server must:

* Deduplicate downloaded runs using canonical run and attempt identities.
* Generate or update analytical Parquet shards.
* Batch multiple sessions into a shard when appropriate.
* Use existing durable ingestion and retry mechanisms.
* Publish completed files atomically.
* Preserve correction and withdrawal semantics.
* Avoid exposing partially written or duplicate event projections.

Activity ingestion and local-server ingestion are separate execution
environments. They must produce compatible analytical data, but do not need a
shared runtime coordinator.

### 5. Immutable publication and query snapshots

Parquet files must be immutable after publication.

Maintain a lightweight catalog of published files using existing local-server
operational storage.

A query obtains a consistent snapshot containing the exact Parquet files it is
authorized to read.

When ingestion produces new files:

1. Write the replacement or additional files.
2. Validate their integrity and logical contents.
3. Publish a new file-list generation atomically.
4. Direct new queries to that generation.
5. Retire obsolete files after queries referencing the previous generation have
   finished.

Do not block ingestion by draining all queries or restarting DuckDB.

Existing queries complete against their original file snapshot while new queries
use the updated snapshot.

For deduplication, corrections, and withdrawal, the active snapshot must expose
at most one approved projection of each canonical run attempt.

A file-level manifest alone cannot guarantee this if different files contain
overlapping runs. Publication must either exclude overlapping projections
physically or provide an equivalent correctness-preserving active-projection
filter.

Avoid expensive global `DISTINCT` operations on every analytical query.

### 6. DuckDB local analytical backend

Add DuckDB support to the CAO local Go server.

DuckDB queries the published Parquet files directly using SQL without importing
them into a persistent DuckDB database.

For example:

```sql
SELECT
    name,
    COUNT(*) AS calls,
    COUNT(*) FILTER (WHERE success = false) AS failures
FROM AgentEvents
WHERE event_type = 'tool.execution_complete'
GROUP BY name
ORDER BY failures DESC;
```

`AgentEvents` is a logical view backed by the active Parquet file snapshot.

Use the official DuckDB Go driver for trusted server-generated queries where
supported.

Configure:

* Memory limits.
* Query concurrency.
* Thread limits.
* Temporary disk usage.
* Execution timeout.
* Result-size limits.

DuckDB reads Parquet selectively using column projection, row-group statistics,
and vectorized processing. It must not require all historical files to fit in RAM.

Do not persist a second full copy of event data in native DuckDB tables.

Keep SQLite for operational state and the existing browser storage architecture.

### 7. Isolated agent-generated SQL

Allow AI agents to perform exploratory analytical queries without restricting
them to a small predefined query catalog.

However, untrusted agent-generated SQL must not execute inside the main CAO
server process.

Provide an isolated DuckDB query worker.

The worker receives:

* The submitted SQL.
* An authorized logical dataset.
* A consistent published-file snapshot.
* Resource and result limits.

The worker runs in a separate process with operating-system isolation.

Security requirements:

* Read-only access to explicitly authorized Parquet files.
* No access to CAO secrets or operational databases.
* No network access.
* No unauthorized filesystem access.
* Disabled extension installation and unsafe external capabilities.
* Bounded memory, CPU, temporary storage, and execution time.
* Query cancellation and worker termination.
* Output size and row-count limits.

DuckDB settings and SQL restrictions are defense-in-depth measures. The
operating-system sandbox is the primary security boundary.

Start with a supported Linux isolation implementation.

On platforms without an adequate sandbox, disable arbitrary agent-generated SQL
while retaining trusted application-generated queries.

Expose a convenient agent-facing interface for schema discovery, SQL execution,
query-plan inspection, and structured results.

### 8. Azure Blob Storage and ADX integration

Extend CAO's existing ADX integration to optionally publish the Parquet events
dataset to Azure Blob Storage.

Keep the current ADX native tables and ingestion functionality.

Do not introduce redundant native ADX tables.

Use an ADX external table to query the published Parquet files through KQL.

Reuse canonical event, run, and projection identities so archived events can be
joined with existing ADX session facts, grader observations, evaluations, and
outcome data.

Configure Azure Blob Storage retention independently of local JSONL shard
retention.

Support optional daily compaction:

* Leave the most recent 24 hours untouched.
* Compact eligible closed-day partitions once.
* Target a configurable maximum file size, initially 256 MB.
* Publish compacted files only after verification.
* Retire superseded files safely.
* Leave late arrivals in additional files rather than repeatedly reopening
  completed partitions.

Azure Blob lifecycle policies handle eventual historical expiration.

The external-table publication strategy must guarantee that readers do not
observe both original and compacted files as duplicate events.

Do not assume that an ADX external table over a mutable directory provides
transactional snapshot semantics. Use a correct publication and replacement
strategy.

### 9. Extend the CAO declarative query language

CAO already has a declarative JSON query model for its dashboard.

Extend this model incrementally to support analytical operations over the event
dataset.

Capabilities to consider include:

* Selecting logical analytical datasets.
* Typed filtering.
* Grouping and aggregation.
* Statistical and percentile functions.
* Window functions.
* Ordered event sequences.
* Joins across compatible datasets.
* Cost and performance calculations.
* Query limits and sorting.
* Backend-specific analytical expressions where necessary.

Preserve existing query definitions and browser compatibility.

The schema should distinguish:

* **Portable declarative queries:** Operations that can be evaluated across
  existing supported backends.
* **Advanced analytical queries:** Operations requiring DuckDB or ADX capabilities.
* **Exploratory SQL:** Arbitrary SQL submitted through the isolated DuckDB query
  interface, outside the portable declarative query language.

Do not build a universal SQL-to-KQL compiler.

Instead, reuse logical dataset identities, metric semantics, and schema
definitions while providing backend-specific SQL and KQL implementations.

### 10. Query capability discovery

The backend must advertise its analytical capabilities to the dashboard.

For example:

| Capability | Browser / IndexedDB | Local DuckDB | Hosted ADX |
| --- | --- | --- | --- |
| Existing declarative queries | Yes | Yes | Yes |
| Advanced analytical aggregations | Existing subset | Yes | Yes |
| Event-sequence analytics | Limited | Yes | Yes |
| Arbitrary DuckDB SQL | No | Sandboxed | No |
| KQL queries | No | No | Yes |
| Historical Parquet analytics | No | Yes | Yes |

The dashboard must use capability discovery rather than assuming every backend
supports every query.

Unsupported operations should be visibly disabled or show a clear explanation.

For example:

> This query requires DuckDB or ADX and is unavailable in the standalone browser.

Do not silently downgrade query semantics or return incomplete results.

When a supported local or hosted backend is connected, the dashboard should
expose the corresponding analytical functionality.

### 11. Query safety and authorization

The analytical layer must preserve CAO's existing authorization boundaries.

Queries may only access repositories, runs, and event data authorized for the
requesting user or agent.

Do not rely on filenames or agent-provided filters alone to enforce access
control.

Authorization must be applied when constructing the permitted file snapshot or
server-side analytical scope.

For hosted ADX, use appropriate Azure identities, query permissions, and
server-side authorization.

Enforce query timeouts, concurrency controls, bounded result sizes, and resource
limits across both backends.

SQL execution permissions must remain separate from CAO's operational mutation
permissions.

### 12. Schema evolution

The Parquet event schema must be explicitly versioned.

New event types and structured fields should not silently disappear from
analytical exports.

Compatible additions should preserve existing queries.

Incompatible changes require an explicit migration or compatibility strategy.

Both DuckDB and ADX must handle mixed historical schema versions according to
documented rules.

Schema compatibility must be tested with representative historical shards.

### 13. Observability and performance

Reuse CAO's existing OpenTelemetry instrumentation.

Monitor:

* JSONL-to-Parquet conversion duration.
* Parquet file size and compression ratio.
* Publication and reconciliation failures.
* Local shard storage growth.
* DuckDB query latency and memory usage.
* Query cancellation and resource-limit failures.
* Active file-snapshot generations.
* ADX external-query latency.
* Blob upload and compaction cost.

Benchmark analytical queries against representative datasets containing millions
of events.

Verify that Parquet storage does not cause PostgreSQL or SQLite to grow
proportionally with historical event volume.

## Alternatives Considered

### Keep JSONL and SQLite only

Rejected as the sole analytical solution because it does not provide the desired
portable columnar storage and efficient large-scale analytical execution.

Retain these technologies for their existing operational and browser
responsibilities.

### Replace JSONL with Parquet immediately

Deferred.

The browser and existing replication pipeline depend on JSONL. Dual-format
publication preserves compatibility and allows a gradual migration.

### Embed DuckDB in CAO Activity

Rejected.

Activity only needs to create Parquet files. It can invoke the DuckDB CLI as a
batch conversion tool.

### Use DuckDB as the canonical database

Rejected.

DuckDB is the local query engine, not the authoritative operational store.
Immutable Parquet files remain the portable analytical representation.

### Ingest every event into PostgreSQL

Rejected.

This would substantially increase hot-database size and create unnecessary
operational and analytical coupling.

### Use ADX exclusively

Rejected.

Local CAO deployments should support substantial analytical workloads without
Azure connectivity or cloud infrastructure.

### Use ClickHouse or another hosted analytical database

Deferred.

ClickHouse is a credible future hosted backend, but the current ADX integration
already provides cloud analytics. Additional providers can be evaluated without
changing the Parquet format.

### Expose unrestricted DuckDB SQL from the main Go process

Rejected.

Arbitrary SQL is not a safe trust boundary even when the underlying files are
read-only.

Use an isolated worker for agent-generated SQL.

### Implement a full transactional lakehouse

Rejected for the initial implementation.

The expected workload can be handled using existing operational storage,
immutable file publication, lightweight file catalogs, and stable snapshots.

Reconsider a formal lakehouse table format only if concurrency, corrections, or
multi-writer requirements outgrow this model.

## Consequences

### Positive

* One portable analytical data representation.
* Efficient storage for large event histories.
* Local analytics without Azure.
* Cloud-scale analytics through existing ADX integration.
* No duplicate native analytical event database.
* Minimal disruption to browser and IndexedDB.
* Better support for agent-driven exploration.
* Independent query and ingestion lifecycles.
* Ability to introduce additional analytical engines later.
* PostgreSQL remains focused on operational data.

### Negative

* Temporary duplication between JSONL and Parquet.
* Additional DuckDB runtime dependency in the local server.
* Different SQL and KQL implementations for some queries.
* Need to coordinate file publication and query snapshots.
* OS-level sandboxing adds implementation and testing complexity.
* External Parquet queries may be slower than indexed native ADX tables.
* Historical schema compatibility must be maintained.

### Risks

* Duplicate event projections after retries or corrections.
* Queries observing partially replaced file sets.
* File deletion while queries are still reading.
* Untrusted SQL escaping its authorized dataset scope.
* Excessive memory or temporary-disk use.
* Schema drift between DuckDB and ADX.
* Azure object-store publication and compaction exposing overlapping files.

Mitigate these risks through deterministic identities, atomic publication,
immutable files, stable query snapshots, bounded execution, and integration tests.

## Implementation Plan

Implement incrementally.

### Phase 1 — Parquet generation

Extend existing JSONL shard generation to produce matching Parquet files using
DuckDB CLI.

Define the canonical event projection and verify DuckDB/ADX schema compatibility.

### Phase 2 — Local storage lifecycle

Integrate Parquet with existing shard generation, invalidation, retention, and
deletion.

Support the local-server ingestion queue and immutable publication.

### Phase 3 — DuckDB backend

Add local DuckDB queries over published Parquet snapshots.

Provide logical dataset views, schema discovery, and resource limits.

### Phase 4 — Secure agent queries

Introduce an isolated DuckDB query worker with OS sandboxing.

Expose bounded agent-facing SQL execution.

### Phase 5 — Query-language integration

Extend the declarative query schema and backend capability discovery.

Enable advanced analytical queries where supported and clearly disable them
elsewhere.

### Phase 6 — ADX integration

Publish compatible Parquet shards to Azure Blob Storage through the existing ADX
pipeline.

Add external-table query support, daily compaction, and retention integration.

### Phase 7 — Validation

Run cross-backend correctness tests, large-scale benchmarks, security tests, and
performance comparisons.

Validate that the same logical event dataset produces equivalent analytical
results in DuckDB and ADX.

## Acceptance Criteria

This ADR is satisfied when:

1. CAO produces deterministic Parquet event shards compatible with its existing
   JSONL data.
2. Parquet shards share the existing JSONL lifecycle in CAO Activity.
3. Local-server ingestion deduplicates run projections and publishes files safely.
4. DuckDB queries local Parquet directly without requiring event import.
5. Agent-generated SQL executes only inside an adequately isolated worker.
6. Queries use stable file snapshots and tolerate concurrent ingestion.
7. The dashboard discovers backend capabilities and disables unsupported
   analytical operations.
8. Existing browser and IndexedDB functionality remains unchanged.
9. Azure Blob Storage and ADX can optionally consume the same logical event schema.
10. Existing ADX tables and query behavior remain compatible.
11. PostgreSQL and SQLite do not need to retain full historical event populations.
12. Schema evolution, corrections, withdrawal, and retries preserve analytical
    correctness.
13. Tests demonstrate equivalent results across DuckDB and ADX for shared metrics.

## Decision Summary

CAO will retain JSONL for existing compatibility while adding Apache Parquet as
its portable historical analytical format.

DuckDB will provide local SQL analytics; Azure Data Explorer will provide optional
hosted analytics.

Immutable files, canonical run identities, and published query snapshots will
provide consistency without requiring a new transactional lakehouse system.

The declarative dashboard query language will support backend capability
discovery, enabling advanced analytical queries only where supported.

Agent-generated SQL will execute in a separately isolated DuckDB worker rather
than inside the main CAO server.

The design prioritizes incremental adoption, analytical scalability, portability,
and minimal additional operational infrastructure.
