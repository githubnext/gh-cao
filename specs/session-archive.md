# Private unified-session archive

## Status and scope

This is the implementation contract for the proposed
[Parquet session archive ADR](../adr/parquet-unified-session-storage.md).
An implemented interface is not evidence that either deployment scenario has
passed all acceptance gates. The ADR remains proposed until the required
correctness, recovery, resource, and destination evidence exists.

The archive is independently default-off, private, additional analytics:

| Scenario | Evidence authority | Query engine |
| --- | --- | --- |
| Local persistent server | Private immutable Parquet files and filesystem generations | Pinned DuckDB CLI |
| Hosted persistent server | Private immutable Blob objects and conditionally published generations | Approved generation-bound ADX external-table queries |

Existing PostgreSQL schemas, canonical queries, ingestion, retention, and
operational-state storage MUST remain unchanged. The archive MUST NOT use
PostgreSQL as a catalog, outbox, staging service, receipt store, or source of
full traces. Dashboard SQLite/overlay, IndexedDB, JSONL publication, and ordinary
dashboard query behavior MUST remain unchanged. There is no frontend or
automatic cross-store join.

## 1. Acquisition and authority

Archive enablement MUST NOT grant acquisition authority. Capture receives the
existing authorized visit to `usage/aw_session.jsonl`, before session evidence
normalization, compaction, or temporary-workspace cleanup. Local acquisition is
bounded by the immutable SQLite snapshot's exact repository scope; co-resident
hosted acquisition is bounded by existing collection enrollment and policy.
Archive configuration further narrows that scope to exact repositories.

Capture MUST reuse `adxtrace` approval, redaction, artifact identity, and event
projection primitives, not another parser or policy engine. Native ADX export
and native trace export remain independently selected profiles. Local archive
operation MUST NOT require either switch, Azure credentials, PostgreSQL, or
an operational archive database.

The archive MUST durably admit approved content before the acquisition metadata
cache can mark an attempt complete. Cache staging MUST leave unadmitted attempts
eligible for reacquisition within the same authorized collection window. When
multiple trace destinations are enabled, a cached attempt is reusable only when
all required destinations admitted it. Failure in one destination MUST remain
visible and MUST NOT prevent another destination from recording a withdrawal.
No filtered cache may replace canonical source evidence.

Repository unenrollment MUST propagate withdrawal to every enabled archive.
Local source-cache expiration is not archive expiration. Ordinary retries MUST
retain admitted private evidence independently of per-pass collection cleanup.

## 2. Evidence and physical projection

`server/spec/trace.tsp`, `Cao.Trace.Archive.Event`, is the shared logical event
contract. Its emitter owns explicit Parquet types and generated Go conversion
metadata. HTTP event responses reference that same TypeSpec model. There MUST
NOT be independently maintained field lists for Go, Parquet, SQL, and KQL.

One logical row preserves event order, immutable projection identity, exact
namespace/repository/run/attempt, recorded lineage and clocks, policy/artifact
digests, omissions, approved payload JSON, and provenance. The JSON payload MUST
preserve approved structure, types, explicit nulls, and content. This does not
promise original whitespace, unrestricted producer extensions, complete upstream
coverage, or pre-redaction bytes. Reasoning is included only where the closed
content policy specifically approves it. Unknown or incompatible schema versions
MUST fail explicitly.

The initial profile admits complete approved traces. It MUST NOT silently
subsample events. Sampling requires the existing frozen whole-trace membership
semantics; an unsupported sampled profile MUST fail closed, not invent new
membership or claim full capture.

Immutable Zstandard Parquet packs traces within namespace, repository, and UTC
completion-day partitions. Missing completion time MUST remain explicitly
undated. Publication membership, not a filename or global `DISTINCT`, selects at
most one active approved projection for each canonical run attempt.

Conversion MUST use the pinned DuckDB v1.4.3 CLI with explicit generated types,
not inferred JSON schemas or an embedded DuckDB database. The converter verifies
the runtime version. Installation MUST verify the release checksum. Subprocess
memory, concurrency, threads, timeout, result, and temporary-disk budgets apply
before activation. A subprocess is not an arbitrary-SQL sandbox.

The initial shared implementation enforces these concrete ceilings:

| Resource | Initial policy |
| --- | --- |
| Logical events per trace | 32768; no event thinning |
| Raw trace bytes | 8 MiB default; approved projection/staging expansion may additionally reject above four times the configured raw trace budget |
| Staging | 128 MiB default and hard upper bound |
| Admitted Parquet bytes | 4 GiB default; independent configured archive budget |
| Immutable file | At most 128 MiB; flush latency may produce smaller packs |
| DuckDB operation | 256 MiB engine memory, 256 MiB temporary disk, one thread, one CLI operation at a time |
| CLI execution/output | 30-second default, two-minute maximum, 32 MiB captured output; HTTP is tighter |
| Pinned local readers | At most 64, each no longer than configured query timeout |
| Local retirement | Five-minute grace; restart may retire unreferenced previous-owner data because those readers cannot survive host lifetime |
| Replay fences | Permanent within bounded `MaxEntries`; admission stops rather than discarding a required fence |

The DuckDB engine-memory setting is not a process RSS ceiling. Active packed
files may retain physically withdrawn rows until their other active members
also retire; exact logical membership remains mandatory for every query.
Hosted acknowledgement removes local historical bodies and retains private
receipt/fence metadata only. Hosted limits may be stricter, including at most
4096 returned rows and one concurrent analytical query.

## 3. Durable publication and recovery

One owner is permitted per archive. Local storage requires a pre-existing
private POSIX durable directory, owner-only regular files, a whole-life OS
`flock`, atomic replacement and filesystem durability barriers.
Unsupported filesystems or insufficient permissions MUST fail closed. The
hosted profile additionally requires a persistent private staging volume, one
Blob lease owner, and conditional active-pointer updates.

Publication MUST follow this order:

1. Durably stage bounded approved evidence, identity, and replay fences.
2. Convert immutable packed files and verify the explicit schema, complete trace
   counts, ordering, identity, and integrity.
3. Durably record the candidate generation and exact active membership.
4. Publish the new generation with stale-writer rejection. Hosted publication
   uploads immutable objects and manifests before conditionally moving the
   active pointer.
5. Acknowledge delivery and release staging only after durable publication.
6. Retire unreferenced files only after readers, restart recovery, and the
   configured conservative retirement grace permit deletion.

Interrupted conversion/publication MUST be retried without another logical
projection or resurrection of withdrawn input. Staging, archive, manifest,
membership, and replay state each require hard bounds. Reaching a bound MUST
reject admission/publication or report an explicit gap; it MUST NOT emit a
truncated ready trace. No archive state belongs in PostgreSQL.

The server owns publication retries and maintenance within its existing tracked
background-task lifecycle. Shutdown cancels and joins publisher/acquisition
tasks before closing the archive owner. Unacknowledged durable staging remains
recoverable after restart. Retry/status failures are archive failures; they MUST
NOT change the meaning of ordinary dashboard readiness.

## 4. Corrections, withdrawal, and retention

Corrections publish new immutable projections and active membership. Queries
pin a generation; ordinary replacement may allow existing readers to finish.
Withdrawal and authorization revocation override that convenience: affected
queries MUST be canceled or denied before results are delivered.

The withdrawal-aware reader MUST remain registered through result serialization
and caller reauthorization. The final content-access check and bounded HTTP
response write MUST be serialized with withdrawal; completing the backend query
alone MUST NOT release that protection. Every response exit MUST close its reader.

Withdrawn identities MUST remain replay-fenced for the supported replay horizon.
If required fences cannot fit, admission MUST stop or reject older inputs.
Old input MUST NOT silently become current because a content file or cache was
removed. Withdrawal applies to active and historical access; user-selected old
generations MUST NOT bypass it.

Archive retention and disk/Blob budgets are independent of dashboard/source
retention. Logical exclusion is not physical erasure. Garbage collection MUST
account for readers, obsolete generations, recovery, backups, Blob versions,
and derived exports. Already delivered results cannot be recalled.

## 5. Authenticated analytical access

The existing Go server authentication boundary protects the archive endpoints.
Full-trace-read is a distinct explicit grant. Normal dashboard membership,
administrator status, repository/installation credentials, Actions identities,
and Blob/ADX roles alone do not grant full-content access.

For local serving, the operator MUST separately approve full-trace-read in
addition to the existing bearer capability. For hosted serving, the caller MUST
have a current authorized OAuth session, existing fresh strong-authentication
assurance, and membership in the exact archive-reader login allowlist. That
allowlist explicitly grants the reviewed archive repository scope, not arbitrary
repositories visible through the caller's GitHub account. Authorization is
checked again before releasing results. Hosted POST requests retain existing
CSRF protection.

| Endpoint | Contract |
| --- | --- |
| `GET /api/v1/session-archive/status` | Disabled availability or authorized bounded archive counters; no paths, credentials, or content |
| `POST /api/v1/session-archive/query` | Trusted `timeline` or `summary` template for one exact approved repository |

Timeline requires a positive GitHub run ID and exact positive attempt.
Summary rejects run/attempt selectors. Neither accepts SQL/KQL, directories,
object names, raw filenames, namespace overrides, or caller-selected generations.
Requests are at most 4096 bytes. Responses are at most 8 MiB and 10000 rows.
Execution is at most 30 seconds, with tighter backend budgets preserved.
Exceeding a complete-result budget returns an error, not partial success.
An unknown trace is distinct from a complete empty trace.

Archive queries MUST NOT route through, replace, or fall back to ordinary
SQLite/PostgreSQL dashboard queries. Query failures return explicit unavailable
or budget errors. Metadata summaries count retained approved event rows at their
declared grain, not tool invocations, token totals, accepted value, or population
estimates.

## 6. Hosted query activation

An ADX external table does not honor a Blob generation automatically.
The approved layout uses virtual `ArchiveNamespace`, `ArchiveRepository`,
`ArchiveDay`, and `ArchiveObject` partitions on immutable
`objects/<namespace-hash>/<repository-hash>/<UTC-day-or-undated>/<file-hash>/`
paths. Trusted queries MUST restrict those exact pinned partitions/file hashes
and exact active projection membership before analytical operations. Runtime
MUST validate the external-table layout against the configured private root.
A broad wildcard with an unbounded late deduplication is not publication.

Hosted query enablement requires real destination conformance evidence bound to
the exact approved Blob/ADX configuration. Local simulators prove protocol
behavior only. Successful upload, credential access, mock KQL output, and an
acceptance flag without actual evidence MUST NOT be presented as live Azure
acceptance. Without that evidence, hosted querying remains unavailable.

Setup privileges remain separate from runtime credentials. No runtime
provisioning or credential changes are implied. Per-request Azure Functions,
ephemeral staging, standalone acquisition roles without an owning publisher,
and multi-replica archives are unsupported.

## 7. Required evidence

The dedicated `.github/workflows/session-archive.yml` Actions workflow runs on
relevant pull requests, main pushes, and the daily full suite. It installs a pinned,
checksum-verified native DuckDB and executes production capture, conversion,
publication, and authenticated query tests. Required tests MUST fail when the
runtime is missing; mocks or skipped tests are not conformance.

`TestDuckDBArchiveMillionEventBenchmark` verifies exactly 1,048,576 event rows
across 128 traces and 16 files, including separate high-entropy 64 KiB event
samples. It requires `CAO_SESSION_ARCHIVE_BENCHMARK=1` and required DuckDB mode.
Reported peak Go heap and DuckDB temporary-disk use are 20 ms samples, not
continuous process RSS measurements or proof of an OS-enforced memory ceiling.
The focused Actions job runs this bounded test without race instrumentation.

The gate covers approved payload reconstruction and order, default-off behavior,
scope and permission rejection, restart/retry, correction and replay withdrawal,
concurrent generation ownership, complete-result/resource limits, and unchanged
ordinary dashboard serving. Archive acceptance additionally needs representative
millions-of-events capacity measurements and hosted live Azure conformance.
The CI gate alone MUST NOT be described as completing those deployment gates.

Real Kusto membership conformance uses the pinned official emulator through
`scripts/test-kusto-emulator.sh`, with `CAO_SESSION_ARCHIVE_REQUIRE_KUSTO=1`.
Its manual Actions job requires an explicit default-false EULA acceptance input;
ordinary CI does not accept terms. The emulator proves functional KQL behavior,
not live Blob/external-table access, authentication, retention, or performance.
Live Azure conformance remains separately required in an approved dedicated
empty `conformance-*` namespace; missing required-mode evidence MUST fail.

Compaction, arbitrary SQL/KQL, distributed ownership, new outcome acquisition,
training, replayed tool execution, and automatic policy changes are deferred.
