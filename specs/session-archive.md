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

Existing canonical PostgreSQL schemas, canonical queries, ingestion, retention, and
operational-state storage MUST remain unchanged. The archive MUST NOT use
PostgreSQL as a physical archive catalog, content staging service, Blob delivery
receipt store, or source of full traces. The native source-enrollment privacy
ledger and withdrawal outbox below contain only enrollment/destination metadata,
never archive contents, manifests, objects, events or run-attempt receipts.
Dashboard SQLite, IndexedDB, JSONL publication, and ordinary
dashboard query behavior MUST remain unchanged. There is no frontend or
automatic cross-store join.

When a Value collector requests exact producer linkage, the additional
authority, evidence-meaning, and privacy boundary is defined by
[`value.md`](value.md#8-private-archive-linkage). Archive availability never
establishes outcome acceptance, canonical Value freshness, or Git evidence.

## 1. Acquisition and authority

Archive enablement MUST NOT grant acquisition authority. Capture receives the
existing authorized visit to `usage/aw_session.jsonl`, before session evidence
normalization, compaction, or temporary-workspace cleanup. Local archive access is
bounded by the SQLite snapshot's exact repository scope; the local Go server
MUST NOT start SQLite evidence collection or backfill. Co-resident
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

### 2.1 Fresh original-evidence contract

Archive `ContractVersion` is **2**, independent of the native ADX trace tables'
unchanged version 1. There is no backward-compatible reader, old-schema adapter
or in-place migration. Provision a fresh archive directory and a fresh reviewed
Blob prefix/external table. `EventSchema()` changes both `AcceptanceBinding()`
and `NativeAcceptanceBinding()`; analytical activation also requires its newly
bound acceptance. Old destination acceptance MUST NOT activate this layout.
Rejection MUST NOT erase old objects, privacy registrations, pending withdrawals
or source-epoch fences. Retiring previous data is a separate explicitly scoped
operator operation; fresh provisioning is not permission to delete those
records or bypass cleanup obligations.

The TypeSpec `@evidence` annotations generate scalar bindings, Go/Parquet/SQL/KQL
types and structured-family metadata. Typed projection runs only on the approved,
redacted original event. The shared preservation transform is
`cao-approved-events-v2`; it participates in policy/evidence identity, and archive
projection identity additionally binds the Parquet contract version. Replay MUST
NOT reuse a projection produced under previous preservation semantics.

Every promoted field and declared family has generated-name entries in
`PresentFields`, `NullFields` and `InvalidFields`. Invalid includes incompatible
types, invalid numeric measurements or an unusable parent; it never converts
such measurements to zero. Counts require a nonnegative safe integer; durations
retain finite nonnegative fractional values as `DOUBLE`/`real`. Original numeric
lexemes, including invalid or unrepresentable measurements, remain in approved
`PayloadJSON`. Scalar numeric/boolean columns are nullable. Physical strings
use `""` on both engines because Kusto strings cannot be null; metadata
distinguishes missing, explicit null, invalid, redacted and originally empty
strings. String/number alternatives for RPC/request IDs and producer versions
have separate typed columns, with their original JSON type retained in payload.
`OmissionReasons` records only bounded generated paths with `excluded` or
`redacted`, never excluded values. Excluded evidence does not reveal its nullness.
Redacting one family member MUST NOT discard an unaffected sibling's typed value.

The evidence-only fixtures are anchored to `github/gh-aw`
`5ac122295e5e839aa15a727ecf4762f3e680cc24`, specifically
`actions/setup/js/grader_session.cjs`, `trace_graders.cjs` and
`trajectory_graders.cjs`. They support the following 15 candidates **only when
the original evidence was supplied and explicitly approved without relevant
redaction**:

| Candidates | Preserved original evidence |
| --- | --- |
| `tool-failure-count`, `tool-success-rate`, `trajectory-efficiency` | Separate agent-phase native starts/completions and gateway RPC/tool observations, tool/session/subagent identity, success/status/isError and approved error state, provenance and harness attempt scopes |
| `execution-step-count`, `execution-duration`, `working-set-rebuild-factor`, `context-growth` | Every proxy usage, aggregate usage and execution-result observation in recorded order; original input/output tokens, duration, phase, validity and source state |
| `retries` | MCP event/retryHint and approved message; separate harness attempt/retry observations, startup/harness identity, layer, strategy, startedAt and supplied compatibility histories |
| `artifact-production` | Separate safe-output dispatch and applied results, including failed applied receipts and missing/null status, with original phase/path |
| `objective-coverage`, `premature-termination-gap`, `policy-near-miss` | Every supplied trajectory objectives/events collection, descriptions and original satisfaction/event indices, including zero and IR safe-output observations |
| `grounding-accuracy` | Supplied actions and their original identity/index/explicit validAtIssueTime |
| `state-revisit-probability-rep`, `recurrence-rate` | Every supplied state visit, repeated ID, representation, firstEventIndex and original IR event mapping |

No capture-time invocation reconciliation, usage precedence choice, native retry
inclusion decision, objective matching, guard classification, inferred state
identity or score is persisted. Compatibility histories remain distinguishable
from canonical harness events. Native `engine.retry` is not automatically
included in the reviewed retries candidate. Harness/startup attempts, GitHub
attempts and enrollment epochs remain distinct. No arguments, tool output
bodies, models, cache counters or safe-output titles/URLs are newly approved
merely to support these candidates.

Approval remains a closed event policy selecting entire **top-level data
subtrees**, not arbitrary nested paths. Approving `usage`, `objectives`,
`actions`, `states`, `events`, `attemptResults` or `startupAttemptResults` can
retain extra nested values; operators MUST explicitly review that actual privacy
scope. Textual retry messages/objective descriptions and original errors need
specific event-field approval and retain redaction qualification.
`provenanceFields: ["attemptId", "startupAttemptId"]` separately and explicitly
admits only those two provenance names. Its default is empty, and other names,
duplicates and nested-path approvals fail. This grants no content beyond the
named provenance or collection authority. Unsupplied, excluded or materially
redacted required evidence remains unavailable; it MUST NOT be inferred or
fetched from repository files.

The initial profile admits complete approved traces. It MUST NOT silently
subsample events. Sampling requires the existing frozen whole-trace membership
semantics; an unsupported sampled profile MUST fail closed, not invent new
membership or claim full capture.

Immutable Zstandard Parquet packs traces within namespace, repository, and UTC
completion-day partitions. Missing completion time MUST remain explicitly
undated. Publication membership, not a filename or global `DISTINCT`, selects at
most one active approved projection for each canonical run attempt.

Legacy local/POSIX conversion MUST use a DuckDB v2.0.0-or-newer CLI with explicit generated types,
not inferred JSON schemas or an embedded DuckDB database. The converter verifies
the runtime version. Installation MUST verify the release checksum. Subprocess
memory, concurrency, threads, timeout, result, and temporary-disk budgets apply
before activation. A subprocess is not an arbitrary-SQL sandbox.

The separately selected native hosted producer uses the pinned Go Parquet
library in `server/go.mod`, Zstandard compression, and the same generated
logical event schema. It MUST verify the complete schema, event round trip,
ordering, counts and immutable digest before publication. Conversion uses
bounded memory, at most 1024 rows per row group, one compression worker,
the existing 32768-event and four-times-raw projection limits, and at most
128 MiB of encoded bytes. Capacity rejection MUST NOT acknowledge a partial
trace. This profile supports full capture only; sampled capture fails closed.
It MUST NOT execute DuckDB or consult local files, locks, caches or raw-session
operational records.

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
legacy hosted profile additionally requires a persistent private staging volume, one
Blob lease owner, and conditional active-pointer updates.

Direct Blob and ADX access MUST cross the owned `BlobStore` and `QueryService`
interfaces. Consumers MUST NOT receive raw HTTP responses or concrete provider
clients. The composition root selects the production adapters; explicitly
injected services use the same activation, destination, scope, ownership,
bounded-result and recovery contracts. `NewWithServices` and
`NewNativeWithServices` MUST reject missing services and MUST NOT substitute a
different backend after an adapter failure. Blob contracts preserve immutable
content verification, conditional ETag/lease pointer publication and explicit
not-found/conflict errors. Query contracts return complete bounded results or
an error; protocol fixtures cannot establish real Azure conformance.

The explicit native hosted profile instead owns one Blob lease and conditional
active-pointer updates, without a POSIX staging volume. It does not relax the
single-exporter, OAuth, persistent-host lifecycle or real-destination acceptance
gates. It reserves bounded immutable-object inventory in Blob before uploading
the verified approved Parquet bytes, then publishes exact generation membership
and private metadata-only admission receipts. No successful capture
acknowledgement precedes durable publication. Before that acknowledgement a
crash may require authorized exact-attempt reacquisition; the caller MUST NOT
mark the attempt complete. Partial uploads remain inventoried and are
deduplicated or conservatively retired on successful retry. A retry resolves
the durable pointer before selecting another generation. Content is never
stored in the receipt, manifest, acquisition record or PostgreSQL.

Native receipts record source gaps without acknowledging complete capture;
complete retries replace gaps, corrections fence old versions, and both
attempt and repository tombstones override readers and survive restart.
Retention removes logical membership and preserves permanent bounded fences,
while physical retirement observes the persisted reader horizon and grace.
Legacy and native producers MUST NOT open each other's durable profile.
Native Azure acceptance uses `Config.NativeAcceptanceBinding()`, distinct from
legacy conversion acceptance. Passing protocol fixtures is not that acceptance.
The server's `CollectorConfig.NativeAcquisition` remains independently
fail-closed until enrollment, queue, backfill, inventory, archive, job-log and
audit integration meet their supported contracts.

### Native source-enrollment privacy authority

The server MUST explicitly receive `sessionarchive.NativeAuthority` backed by
the same PostgreSQL source enrollment namespace and transaction boundary. Missing
authority rejects native startup; a process-local implementation, a current-scope
lookup alone, or a separately committed withdrawal callback is not a substitute.
The minimal durable record is keyed by namespace, exact destination binding,
repository and source epoch, with installation ID, withdrawn and cleanup-pending
flags. It is a bounded permanent privacy registration/outbox, not an archive
catalog. Reaching capacity MUST reject admission; required rows MUST NOT be
evicted or deleted by canonical repository erasure.

`Register` MUST commit that metadata under an exact current source installation
and epoch check **before** any Blob I/O. It MUST NOT infer the dispatch epoch or
reactivate a withdrawn registration. Every enrollment withdrawal or owner/epoch
replacement MUST atomically mark every registered old source withdrawn and
cleanup-pending in its source enrollment transaction. Thus a crash or PG
connection loss during later Blob I/O cannot lose the cleanup obligation.
Publication and PostgreSQL are not a cross-backend transaction: capture checks
current registered source again after Blob publication and MUST NOT acknowledge
a source that changed while the upload ran.

Every native Blob admission receipt records the explicit dispatch-time source.
Unscoped capture and querying MUST fail on the source-fenced profile. Legacy
unscoped native evidence MUST NOT be relabeled with current enrollment. A new
enrollment may read only freshly admitted matching-epoch evidence; it cannot
expose a previous epoch while cleanup is pending. A colliding old identity
requires old-source cleanup before fresh acquisition can replace it.

Native queries MUST first resolve `Current` from authoritative enrollment joined
to a nonwithdrawn destination/source registration. Missing registration,
withdrawn scope, or unavailable authority fails closed, without a Blob-only
fallback. They MUST select exact source-tagged Blob membership before issuing
ADX queries. Final delivery additionally uses `WithRead` to recheck that same
source while holding shared source and registration row locks through the
bounded response callback. Enrollment takes conflicting locks in the same
source-then-registration order. Loss of that authority transaction MUST cancel
its callback context; callback cancellation is checked before delivery. This
does not retract bytes already delivered under a valid preceding authorization.

The existing archive publisher lifecycle drains bounded durable withdrawals.
`DrainWithdrawals` MUST use shared database claims/locks, retain pending rows on
callback or acknowledgement failure, and acknowledge only after idempotent
`WithdrawSource` succeeds. Blob cleanup permanently tombstones the old source,
revokes its readers, removes only that source's membership and preserves
immutable-object retirement inventory/grace. The source tombstone also rejects
a stale uploader resuming after PG disconnection and earlier cleanup. A cleanup
acknowledgement never reactivates the PG registration. Newer source epochs and
their approved data MUST remain unaffected by old cleanup retries.

PostgreSQL enrollment-atomic registration/withdrawal, runtime-role grants,
separate-client read/withdrawal serialization, restart/retry and source-epoch
conformance remain required deployment evidence in addition to the native Azure
destination gate. Server/interface tests with explicit probes do not establish
those database properties.

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
projection or resurrection of withdrawn input.

When withdrawal invalidates a pending hosted publication candidate, recovery
MUST read the leased durable Blob pointer before acknowledging or replacing the
candidate. A destination receipt that cannot be resolved MUST retain the pending
evidence for retry. Staging bodies that durable state still references MUST
remain protected, whether expired or withdrawn, and MAY be released only after
those references are durably cleared. Staging, archive, manifest,
membership, and replay state each require hard bounds. Reaching a bound MUST
reject admission/publication or report an explicit gap; it MUST NOT emit a
truncated ready trace. Physical archive catalog, content staging and Blob
delivery receipts MUST NOT move into PostgreSQL; the metadata-only source
privacy ledger/outbox is the explicit native enrollment exception.

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

A hosted query MUST fail explicitly with a capacity error, rather than wait past
its deadline, when publication owns the snapshot lock. A failed or canceled
query MUST release its single-reader slot without waiting for publication.

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
| `POST /api/v1/session-archive/query` | Trusted `timeline` or `summary` template, or shared analytical `session-query`, for one exact approved repository |

Timeline requires a positive GitHub run ID and exact positive attempt.
Summary rejects run/attempt selectors. Neither accepts SQL/KQL, directories,
object names, raw filenames, namespace overrides, or caller-selected generations.
Requests are at most 4096 bytes. Responses are at most 8 MiB and 10000 rows.
Execution is at most 30 seconds, with tighter backend budgets preserved.
Exceeding a complete-result budget returns an error, not partial success.
An unknown trace is distinct from a complete empty trace.

The analytical operation is explicitly named `session-query`, not the generic
`query`. It accepts `language-version: "0.2.0"` and one Dashboard Language
`query` definition over `archive-events`, with generated scalar event columns,
typed conjunctive predicates, explicit projection, grouping, exact `count` and
`distinct-count`, numeric `sum`/`max`, filtered measures, ordered `first`,
bounded `post-aggregate` scalar computation, deterministic ordering, and optional
top-N selection. The count-only `0.1.0` request subset remains supported;
it MUST reject the additional operators rather than implicitly grant them.
`PayloadJSON`, arbitrary JSON paths, joins, scripts, and caller-selected storage
are not supported. An explicit `query.limit` reports omitted matching rows with
`metadata.limited`; it MUST NOT hide a resource-budget failure. Source coverage
remains unknown, with repository admission gaps and retained traces with recorded
omissions reported independently of selection. Both backends advertise
`session-query` in `queryKinds` when supported; hosted analytics require the
separate default-off analytical acceptance gate below. Status then exposes `analytical` capabilities:
the language version, generated scalar fields, source, predicates, exact
reducers, and maximum output rows (10000 for DuckDB, 4096 for ADX). Unsupported
operators or explicit limits MUST fail rather than silently emulate or clamp.

The additional vocabulary is deliberately bounded: at most 16 measures, 64
predicates across source and measure filters, 32 post-aggregate computations,
256 expression nodes and nesting depth 8. Functions use the existing
`function`/`args` and `field`/`value` vocabulary: `if`, `equals`, `gt`, `lte`,
`and`, `sum`, `quotient`, `coalesce`, and `is-null`. Computations read only group
keys, aggregate results and preceding computations. Null is allowed only in a
typed numeric literal such as `{"value":null,"type":"number"}`, never as a
missing filter, predicate, selector or argument. Division by zero produces
typed null; comparisons with null are false.

`first` is numeric and requires ascending original `Sequence` and the complete
`Namespace`, `Repository`, `RunId`, `RunAttempt`, `TraceId`, `ProjectionId`
group binding. It preserves the first selected row's null value. Real-valued
`sum` requires the same binding/order and sequential IEEE arithmetic; integer
`sum` accumulates widely before one Number/real conversion. Empty/all-null sums
are null, not zero. Native DuckDB uses ordered lists and explicit reduction;
ADX uses sorted, serialized, scan-bounded lists, typed conditional aggregates
and scalar `extend`. No approximate cardinality or unordered `take_any`
is permitted. ADX's million-row scan fence bounds those internal lists.
These lowerers are not a universal compiler or a new canonical resolver.

The `state` predicate accepts only generated promoted scalar names and inspects
`PresentFields`, `NullFields`, `InvalidFields` and `OmissionReasons` using
generated source paths. Supported states are present, absent, null, invalid,
excluded, redacted and valid. Absent excludes known exclusions/redactions;
valid requires presence without null, invalid or omission. Metadata arrays are
not public scalar fields, and MUST NOT be stringified or traversed arbitrarily.

Four separate reviewed definitions under
`server/internal/sessionarchive/testdata/graders/` recompute execution step
count, execution duration, working-set rebuild factor and context growth.
Every definition retains all active trace rows before counting agent-phase
proxy/report observations separately. Any proxy presence selects proxy for all
counts, completeness, totals and first values, regardless of validity.
Headers-only traces remain represented with unavailable steps. Duration uses
complete nonnegative selected observations within the upstream safe sum bound,
then the first valid agent execution result as fallback. Rebuild accepts only
complete valid input observations with nonzero peak, including wide cumulative
totals; context independently requires complete input AND output, safe
per-direction totals and nonzero original first-record tokens. Limit/order is
applied only after the metric.

Rows expose value, availability, reason, chosen family and exact trace/projection
identity; policy digest qualifies configuration. Query metadata adds a query
digest and compiler implementation/profile provenance. These are recomputed
scalar observations, distinct from recorded `grader.result`, threshold
assessment and acquisition proof. Upstream v2 emits unavailable status but no
reason codes; query reason labels are explicitly separate qualifications
checked against upstream lookup state. Gaps and upstream coverage remain
independent. Backend failure MUST be an error, not a null grading result.

Validation produces one backend-neutral typed plan; separate trusted SQL and
KQL lowerers implement it. Neither translates caller SQL nor exposes storage
syntax. ADX uses exact `count_distinctif`, not approximate `dcount`; unavailable
engine functions fail explicitly. String counts exclude empty strings,
integer counts exclude null, and empty strings sort last in either direction.
ADX materializes only needed scalar columns and `Gaps` after exact partition
and membership selection. Unfiltered gap metadata and filtered analytical rows
share that bounded source and return one result table. An authorized empty
archive preserves zero ungrouped counts and empty grouped/projected results.

The analytical plan version has a separate destination acceptance binding,
`Config.AnalyticalAcceptanceBinding()`. A successful real destination conformance
run emits that binding for operator review and
`CAO_SESSION_ARCHIVE_BLOB_ANALYTICAL_ACCEPTANCE`. Without it, hosted analytical
queries MUST remain unavailable. Existing capture acceptance and native
privacy-ledger destination identity MUST remain unchanged. Native analytical readers retain
the same dispatch-source membership and authoritative final-delivery fencing.
The `0.2.0` analytical binding invalidates previous analytical acceptance only,
not capture/native acceptance. Real destination acceptance additionally requires
the pinned upstream replay artifact and execution of all four current definitions
on the destination's actual Kusto engine before a binding can be emitted.
No operator flag, emulator result or protocol mock substitutes for that evidence.

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
provisioning or credential changes are implied. Per-request hosts,
ephemeral staging, standalone acquisition roles without an owning publisher,
and multi-replica archives are unsupported.

## 7. Required evidence

The dedicated `.github/workflows/session-archive.yml` Actions workflow runs on
relevant pull requests, main pushes, and the daily full suite. It installs a pinned,
checksum-verified native DuckDB and executes production capture, conversion,
publication, and authenticated query tests. Required tests MUST fail when the
runtime is missing; mocks or skipped tests are not conformance.

`TestDuckDBArchiveMillionEventBenchmark` verifies exactly 1,048,576 event rows
across 128 traces and 19 files, including separate high-entropy 64 KiB event
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
ordinary CI does not accept terms. The shared `server/internal/sessionarchive/testdata/analytical.json` corpus
executes through production DuckDB archive queries and the production KQL
lowerer against the real emulator. It covers exact counts, integer precision,
case-sensitive filters, lexical ordering, empty strings, aliases, hidden ties,
top-N disclosure, and empty results. Protocol tests separately exercise
reader lifetime, source fencing, malformed responses and resource rejection.
The emulator proves functional KQL behavior,
not live Blob/external-table access, authentication, retention, or performance.
The same opt-in harness runs the four full usage definitions against real DuckDB
and real Kusto using identical synthetic approved events. Without an approved
real engine endpoint, the replay reports Kusto unavailable; it MUST NOT claim
backend parity. The ordinary DuckDB gate runs all 15 pinned upstream replays and
compares the four implemented full metric values, unavailable states and separate
reason labels. An intentionally malformed envelope fixture retains its upstream
lookup error and cannot establish metric parity; a separate explicit valid
envelope scenario is compared without normalizing the original.
Live Azure conformance remains separately required in an approved dedicated
empty `conformance-*` namespace; missing required-mode evidence MUST fail.

Compaction, arbitrary SQL/KQL, distributed ownership, new outcome acquisition,
training, replayed tool execution, and automatic policy changes are deferred.
