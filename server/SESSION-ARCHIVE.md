# Private Parquet session archive

The [archive specification](../specs/session-archive.md) defines an additional,
default-off analytics service. Local servers use private Parquet plus DuckDB;
persistent hosted servers can use private Blob Parquet plus generation-bound
ADX queries. Existing dashboard PostgreSQL and native ADX profiles are
not replaced. The [ADR](../adr/parquet-unified-session-storage.md) remains proposed
until its deployment acceptance gates have evidence.

## Before enabling

Approve exact collection scope, a versioned closed content/redaction policy,
rights, full-content readers, retention/replay horizons, destination suitability,
and resource budgets. Use a private persistent POSIX volume with verified lock,
rename, and durability semantics. Do not use a public shard directory, ephemeral
container filesystem, per-request host, or a shared multi-replica mount.
Archive configuration never authorizes new GitHub acquisition.

The original-evidence Parquet contract is **version 2**. Use fresh archive
storage and a freshly provisioned, reviewed external table/prefix; there is no
old-schema reader or migration. Regenerate destination-bound legacy/native and
analytical acceptance after real conformance. Do not copy old acceptance,
relabel old enrollment epochs, erase privacy registrations, or discard pending
withdrawals while replacing data. Old storage retirement requires a separately
scoped operator action; no automatic storage deletion occurs on schema rejection.

The generated scalar columns are qualified by `PresentFields`, `NullFields`,
`InvalidFields` and bounded `OmissionReasons`; consult approved `PayloadJSON`
for original structured evidence. Physical empty strings are not proof of source
absence, and nullable/invalid measurements are not zero. The
[fresh evidence contract](../specs/session-archive.md#21-fresh-original-evidence-contract)
defines the 15 conditional grader-evidence families. Twenty-two upstream
built-in graders are reviewed declarative `session-query` definitions under
`internal/sessionarchive/testdata/graders/`, accepted on local DuckDB and real
Kusto: the four usage formulas; objective coverage, premature termination,
grounding accuracy and state revisit over item sources; chained recurrence rate
and artifact production; joined dependency order, evidence saturation and the
recurrence laminarity, trapping-time and determinism metrics; exploration
error, exploitation error and tool-output consumption over the E1 collections;
code search recall, code read precision and code edit precision over the
reference patch, resources and actions; and event entropy rate over IR events,
which uses the closed `log2` function and is compared within a declared 2^-39
tolerance on its `value` only (user-directed; every other field and definition
is bit-exact). Five attempted candidates (the three
tool-call graders, retries and policy near-miss) remain unsupported. The
[grader query feasibility tracker](../specs/session-archive-grader-queries.md)
classifies all 35 upstream built-ins and the extensions each one needs.
It preserves every original observation rather than reconciling invocations,
usage totals, receipts or trajectory visits.

Explicit policy `provenanceFields: ["attemptId", "startupAttemptId"]` may retain
those otherwise excluded harness provenance identifiers. It defaults to empty
and does not authorize other provenance or new collection. `events` still
approves whole selected top-level data subtrees: approving a trajectory
collection, usage or attempt history can retain extra nested values. Review
that scope and text/error redaction explicitly; there is no nested-path
allowlist. Typed values are projected only after approval and redaction.
By user-directed decision E1, the trajectory-IR `toolCalls`, `observations`,
`resources`, `provenanceEdges` and `reference` collections are also approvable
whole subtrees with generated families
([Section 2.2](../specs/session-archive.md#22-trajectory-ir-collection-approval-user-directed)).
They stay off unless the reviewed policy names them; approving `toolCalls`
retains its tool arguments. The `cao-approved-events-v3` transform records their
exclusion and redaction. Archive contract version 3 drops v2 data: v2
projections, local archives, Parquet files and hosted acceptance fail closed and
require fresh provisioning.

The supplied standard Alpine images do not imply DuckDB compatibility or
provision a private archive volume. Use a reviewed persistent native host or
explicitly prepared image with a compatible pinned converter. Missing runtime
or durable storage fails startup; the server does not install binaries or
provision Azure resources.

## DuckDB runtime

Use DuckDB CLI **v2.0.0 or newer**. CI tests v2.0.0; verify release archives
before extraction:

| v2.0.0 release asset | SHA-256 |
| --- | --- |
| `duckdb-cli-linux-amd64.tar.gz` | `c6507206baf7793c6013dbd82368469a45f20dc0e17a6f92f204602f652ed2bc` |
| `duckdb-cli-linux-arm64.tar.gz` | `859e1875d2a9515fbb4bf8871061437a14af82320da68c753f094c4b776348fe` |
| `duckdb-cli-osx-universal.tar.gz` | `8a8f67e7c7f7d1392fa63ba3971948391392fe2a25fca63e7008e33c50beb4c2` |

Download from `https://github.com/duckdb/duckdb/releases/download/v2.0.0/`.
For newer releases, verify the digest GitHub publishes for the asset.
Configure the reviewed executable path with the archive runtime configuration.
The converter checks `SELECT version()` and rejects releases older than
v2.0.0. SQL comes only from generated trusted definitions, never request text.

## Local persistent server

Use `cao-dashboard serve` with the ordinary PostgreSQL namespace.
`CAO_SESSION_ARCHIVE_ENABLED` is the independent opt-in.
The `CAO_SESSION_ARCHIVE_*` configuration owns a separate private archive
directory, namespace, exact repository allowlist, durable-storage approval,
content policy, runtime path, and budgets. Refer to
`internal/sessionarchive/config.go` for the complete validated configuration.
No existing ADX export environment variables are required.

Keep every archive repository within the PostgreSQL namespace's inventory.
Local serving reads and maintains independently captured archive evidence; it
does not start GitHub acquisition or implicit backfill. Approved evidence must be
captured by a separately authorized producer before its collection workspace
is removed. Hosted co-resident workers retain their independent acquisition
and pre-normalization capture lifecycle.

Read access requires `CAO_SESSION_ARCHIVE_READ_APPROVED=true` in addition to
the ordinary loopback bearer capability. Treat that capability as granting
the explicitly reviewed archive content scope; do not share it as a
metadata-only dashboard token.

### Local dashboard (`gh cao dashboard --session-archive`)

`gh cao dashboard` is the local producer. It enables the archive automatically
when DuckDB v2.0.0 or newer is on `PATH` and an approved policy exists at
`.github/cao/session-archive-policy.json` (or `--session-archive-policy`);
`--no-session-archive` disables it and `--session-archive` requires it. The CLI copies the approved policy to a private file,
writes one identity-only run frame per completed run whose `gh aw logs`
artifact directory exists, and configures only its serving child with
`CAO_SESSION_ARCHIVE_*`. The serving process remains the sole archive owner and
receives capture requests over stdin (`cao-dashboard serve --session-archive-stdin`),
so no second process opens the DuckDB store. Because startup checks archive
scope against the canonical inventory, the CLI ingests one collection pass
before starting the server. Missing or unapproved session logs are recorded as
acquisition gaps, not content.

## Hosted persistent server

Use the existing OAuth host with one confirmed persistent replica and
co-resident collection workers. Native ADX export switches are independent.
Blob destination configuration uses `CAO_SESSION_ARCHIVE_BLOB_*`; the shared
archive configuration supplies private durable staging, content policy,
converter, and exact scope. Legacy archive capture creates no PostgreSQL archive
records. The explicitly selected native producer instead uses bounded
PostgreSQL enrollment/privacy registrations and cleanup metadata, never session
content, as described below.

The destination must be a dedicated private namespace with an approved ADX
external-table layout. Operator setup and real Azure conformance must establish
exact generation-bound object/membership selection, permissions, conditional
publication, query cancellation, lease recovery, and retention. Configuration
acceptance must bind the actual verified destination. Do not fabricate an
acceptance marker to bypass this gate. Without evidence, hosted querying is
unavailable even if upload succeeds.

Use the generated setup contract from `sessionarchiveblob.ExternalTableDDL`,
not a manually maintained event field list. The four virtual partitions are
`ArchiveNamespace`, `ArchiveRepository`, `ArchiveDay`, and `ArchiveObject`;
immutable files live under
`objects/<namespace-hash>/<repository-hash>/<UTC-day-or-undated>/<file-hash>/`.
Review and SHA-256 the exact provisioned external-table Properties string and
configure `CAO_SESSION_ARCHIVE_BLOB_EXTERNAL_PROPERTIES_DIGEST`. The destination
acceptance binding includes that digest. Runtime verifies it without executing
DDL and never falls back to native ingestion or local historical bodies.

Set `CAO_SESSION_ARCHIVE_READ_APPROVED=true` and
`CAO_SESSION_ARCHIVE_READ_USERS` to the exact comma-separated GitHub logins
approved for the archive's full repository scope. These are distinct from
`AdminUsers` and ordinary OAuth membership. Readers also need a current session,
fresh existing strong-authentication assurance, and CSRF on POST requests.
Actions tokens and storage credentials are not archive-reader identities.

## Synthetic grader replay

Run `node scripts/replay-session-archive-graders.mjs [private-report-directory]`
from the repository root to exercise the 26 attempted candidates against the
pinned gh-aw revision recorded in the evidence fixture manifest. It reads only
public pinned grader code through `gh`, verifies downloaded Git blob identities,
and compares grader values/errors on original approved events and real DuckDB
Parquet reconstructions. The focused Go regression is
`CAO_SESSION_ARCHIVE_REQUIRE_DUCKDB=1 go -C server test ./internal/sessionarchive -run '^TestGraderReplayEvidence$' -count=1`.

Reports separate approved-input preservation, pinned-grader replay, and attempted
full public JSON query support. The twenty-two definitions in
`internal/sessionarchive/testdata/graders/` execute in real DuckDB and compare
their values and unavailable states with the pinned upstream graders, exactly
except for `event-entropy-rate`'s `value`, which must be within its declared
2^-39 `log2` tolerance (reported with the observed maximum difference). Reason
codes are query qualifications; upstream v2 provides status but no reason
codes. Successful scalar component queries for the five unsupported candidates
are not complete metrics. Every definition must also fit the 8192-byte request
budget with the maximum GitHub repository shape (39-character owner, slash,
100-character name). Missing provenance, invalid measurements, missing/null inputs,
excluded fields and redacted descriptions are not normalized to make replay
succeed. Reports are private ignored test artifacts, not persisted archive
scores, dashboard sources, live collection or Kusto/Azure acceptance evidence.
The original preservation fixture intentionally lacks required upstream envelope
provenance; its lookup error is retained, not counted as metric parity. A
separate valid-envelope fixture tests the same measurement edge cases.
Each scenario also includes another exact run attempt selecting aggregate usage
to guard against cross-trace family/measurement leakage.

With an explicitly approved real `CAO_KUSTO_EMULATOR_URL`, the same runner also
executes `TestKustoUsageGraderReplay` against that engine. Without it, Kusto
parity is unavailable. Never accept an emulator license or start an engine
implicitly. `scripts/test-kusto-emulator.sh` includes paired replay and keeps
its `ACCEPT_EULA=Y` gate.
For separately approved live destination acceptance, set
`CAO_SESSION_ARCHIVE_GRADER_REPLAY_DIR` to the retained replay directory containing
`evidence.json`; the live gate (`verifyLiveAnalytical`) replays every
scenario's full DuckDB result for all twenty-two reviewed definitions through
real Kusto, requiring the recorded definition to equal the current reviewed file,
before emitting the new analytical binding. `TestKustoUsageGraderReplay` runs
the same check against the emulator in the `session-archive-kusto` job. This is not production
ingestion, persisted grading or proof of complete acquired evidence.

## Queries and operations

### Native hosted producer (independent activation gates)

`CAO_SESSION_ARCHIVE_PRODUCER=native` explicitly selects the file-free Go
producer. Supply the approved closed policy as
`CAO_SESSION_ARCHIVE_POLICY_JSON`; do not set `DIRECTORY`, `DUCKDB` or
`DURABLE_STORAGE`. Namespace, exact repository scope, replay/retention and byte
budgets remain required as for legacy capture. Full capture is the only
supported archive mode. A native configuration cannot fall back to local
staging or a DuckDB subprocess.

The existing server selects `sessionarchiveblob.NewNative` only for an
explicitly native hosted collector with the existing OAuth, Blob acceptance,
single-replica and co-resident worker gates. `CollectorConfig.NativeAcquisition`
remains fail-closed; producer implementation alone is not deployment readiness.
The native collector obtains
`sessionArchiveService.NativeDestination(dispatchScope)` before lossy
normalization, preserving the queue item's original installation and epoch.
Unscoped `CaptureSession`, legacy acquisition-cache methods and bare repository
withdrawal callbacks reject native use. Production configuration automatically
uses the canonical PostgreSQL store's `ArchiveAuthority()` implementation of
`internal/sessionarchive/authority.go`. Missing, unopened or unavailable canonical
PostgreSQL rejects startup before opening Blob. `Config.SessionArchiveAuthority`
remains an explicit adapter-injection seam; operators do not need to supply an
adapter to select the production implementation.

The minimal PG record is keyed by
`(namespace,destination,repository,source_epoch)` and stores `installation_id`,
`withdrawn`, and `cleanup_pending` only. Exact-epoch registration must commit
before upload. Enrollment withdrawal/replacement must mark every old-epoch
registration withdrawn/pending in the SAME source enrollment transaction;
canonical erasure must preserve these rows. They require a hard permanent-row
capacity and appropriate private runtime-role grants.

Native queries use current registered PG authority, source-tagged Blob
membership, and a final `WithRead` source/registration lock through response
delivery. Withdrawal therefore denies new delivery immediately at the committed
PG boundary even when Azure is unavailable. Reenrollment never relabels old
Blob data. The existing background publisher drains durable withdrawals through
`WithdrawSource`, retaining retries until Blob tombstone/cleanup and PG
acknowledgement succeed. Blob tombstones reject late old-epoch admissions and
survive restart. There is no cross-database transaction: stale publication is
not acknowledged, and no PG cleanup acknowledgement precedes Blob success.
Database unavailability fails closed rather than authorizing a Blob-only read.
An unresolved Blob withdrawal failure poisons the current lease owner to deny
stale reads. Restore destination availability and restart that archive owner
before retrying its pending PG outbox rows; the ordinary publisher loop cannot
recover a poisoned lease in place. Cleanup acknowledgement means durable logical
exclusion and retirement inventory, not immediate physical deletion. Physical
retirement still waits for the persisted reader horizon and recovery grace.

The PG adapter and enrollment hook have separate-client transaction/locking
tests; supplying a test probe is not deployment acceptance. Native acquisition
remains disabled until dispatch-time destination composition and the other
collector gates are verified. Archive authority selection does not enable that
collector or relax any Blob acceptance, OAuth or single-replica restriction.

Native capture acknowledges only after immutable object verification and Blob
generation publication. An interrupted unacknowledged upload may require
authorized exact-attempt reacquisition after restart. Durable Blob reservations
retain object inventory; raw sessions never enter ordinary metadata stores.
Source gaps persist as explicit metadata but return an error, preserving retry
eligibility. Corrected versions, retention, withdrawal, pinned-reader revocation,
permanent replay fences and conservative physical retirement remain enforced.

Run the deterministic file-free tests with:

```sh
cd server
go test -race ./internal/sessionarchive ./internal/sessionarchiveblob -run Native
```

Real native Azure acceptance is a **separate** gate, not satisfied by the legacy
DuckDB suite or HTTP protocol model. On an empty dedicated `conformance-*`
destination with the reviewed external table and synthetic repository, run:

```sh
CAO_SESSION_ARCHIVE_REQUIRE_AZURE_NATIVE=1 \
CAO_SESSION_ARCHIVE_AZURE_ALLOW_WRITES=1 \
go test ./internal/sessionarchiveblob -run '^TestAzureNativeArchiveConformance$' \
  -timeout 15m -v
```

Provide the existing `CAO_SESSION_ARCHIVE_BLOB_*` destination configuration.
Only a successful real run emits `Config.NativeAcceptanceBinding()` for
operator review and subsequent `CAO_SESSION_ARCHIVE_BLOB_ACCEPTANCE`
configuration. Legacy acceptance cannot activate this producer. Missing Azure
credentials, skipped tests or unavailable external-table execution are incomplete
evidence, not parity.

Authenticated status is `GET /api/v1/session-archive/status`.
An active stage describes the lifecycle, not proof of complete source coverage
or a successful latest publication. Retrying is explicit and independent of
ordinary dashboard readiness. Status does not expose raw files or credentials.

Send a trusted query to `POST /api/v1/session-archive/query`:

```json
{"kind":"timeline","repository":"octo/api","runId":"123","attempt":1,"limit":10000}
```

```json
{"kind":"summary","repository":"octo/api","limit":10000}
```

For analytical session queries on DuckDB or ADX, use the explicit `session-query` kind:

```json
{
  "kind": "session-query",
  "language-version": "0.2.0",
  "repository": "octo/api",
  "query": {
    "name": "session-event-counts",
    "subject": "Count retained approved session events by native type.",
    "from": "archive-events",
    "aggregate": {
      "by": ["Type"],
      "values": [{ "field": "Sequence", "as": "events", "reducer": "count" }]
    },
    "order-by": [{ "field": "events", "direction": "desc" }],
    "limit": 100
  }
}
```

Status advertises supported `queryKinds` and `analytical` capabilities, including
the generated scalar field vocabulary, predicates, exact reducers, and output
budget (10000 rows on DuckDB, 4096 on ADX). Both backends execute the same
versioned JSON profile through a typed plan and their own trusted SQL/KQL
lowerers. HTTP and MCP accept only `0.2.0`; `0.1.0` and other versions are
rejected without compatibility fallback. Unsupported operators or explicit limits
fail without fallback. See the [analytical contract](../specs/session-archive.md#5-authenticated-analytical-access)
for filtered aggregates, generated field-state predicates, numeric ordered
`first`, wide versus sequential sums and the explicit bounded post-aggregate
phase. No reducer reads `PayloadJSON`; null first values are retained.
Status also lists `analytical.sources`: item sources such as
`archive-objectives`, `archive-actions`, `archive-states`, `archive-resources`
and `archive-reference-patch-files` flatten each trace's last agent-phase
`trajectory.ir` collection at query time, and `analytical.chaining`: a
definition may read up to eight earlier definitions in `queries`, each keyed by
complete trace and projection identity, lowered to one DuckDB statement or
one KQL query of materialized `let` stages with no persisted intermediate.
Status also advertises `analytical.compute`, `analytical.windows`
(`previous`, `row-number`) and `analytical.stateFamilies` (`Error`, `Usage`,
`Provenance`): row-level computations, trace-bound windows over a total row
order, and field-state predicates over structured families without exposing
their values. DuckDB and ADX advertise each construct only after its lowering
passed the real Kusto acceptance job; otherwise they reject such plans.
Results include
typed columns, rows, and metadata separating intentional top-N selection from
known evidence gaps and unknown upstream coverage, plus query digest and compiler
implementation/profile identity (`0.2.0`, the only accepted request version).
The reviewed grader rows retain policy digest,
chosen usage family and explicit measurement qualification. These counts are event
records, not tool calls or sessions. Full approved payloads remain available
through `timeline`, not the analytical projection.

Exact distinct counting on ADX uses `count_distinctif`; an engine lacking it
reports failure rather than substituting an approximate count. Column pruning
excludes full payloads, and gap metadata is assessed before user filtering.
Hosted `session-query` is independently default-off. A successful dedicated live
Azure conformance run emits `Config.AnalyticalAcceptanceBinding()` separately;
review it and configure `CAO_SESSION_ARCHIVE_BLOB_ANALYTICAL_ACCEPTANCE` to
enable the analytical profile. Existing capture acceptance, timeline/summary
access and native privacy-ledger destination identity remain unchanged.
Previous analytical bindings do not enable `0.2.0`. No real Kusto or Azure
acceptance is claimed by a local DuckDB run.
No archive, reader grant, or rollout policy is enabled by this implementation.

When MCP is enabled on the Go server, `cao_session_query` is a separate
read-only tool for this private session data lake. `cao_query` continues to run
named dashboard queries; there is no cross-source fallback. Discovery exposes
the session tool only when the analytical backend and the caller's separate
full-trace-read grant are available. Hosted MCP retains its existing OAuth,
administrator and CSRF gates; administrator or Actions access alone does not
grant archive reads. The standalone `gh cao mcp` canonical-snapshot server does
not expose the session tool.

Call `cao_session_query` with the `repository`, `language-version`, and `query`
fields from the JSON example above, omitting `kind`. Responses include the
same generation, columns, rows and coverage metadata as HTTP, in both MCP text
and structured content. Argument and complete-response budgets remain
8192 bytes and 8 MiB. The reader survives SDK serialization through the actual
HTTP response write, with withdrawal and caller authorization rechecked before
delivery; source-epoch fencing remains in the hosted archive adapter.

Results bind one pinned generation. Timeline events use the shared trace
contract and carry approved payload JSON; full approved content is not
necessarily a complete native log. Requests cannot supply SQL/KQL, paths,
namespace overrides, or historical generations. Limits fail explicitly rather
than truncating a trace. Existing `/api/v1/query` remains unchanged.

Monitor staging backlog/bytes, published generation and event counts, retry
status, admission gaps, disk/Blob capacity, and withdrawal/retirement state.
Logs record failures without trace payloads or destination credentials.
Restore private archive state as a unit, including generations and replay
fences; never restore content alone over newer withdrawals. Stop the owning
server cleanly before moving the volume. Shutdown joins owned work and closes
the archive lock. Failed pending publication is retried after restart.

Logical withdrawal blocks access and replay; it is not immediate deletion from
backups, obsolete generations, Blob versions, or already delivered results.
Completed queries retain withdrawal-aware readers until the HTTP handler exits.
The final content check and bounded response write share the withdrawal lock:
withdrawal that wins this response-commit boundary denies the buffered result.
Review those recovery/deletion horizons separately. Source-cache deletion does
not delete archive history.

When withdrawal invalidates a pending hosted publication, recovery checks the
leased durable Blob generation before acknowledging or replacing that candidate.
An unresolved destination receipt retains pending evidence for retry. Expired
or withdrawn staging bodies remain protected while durable state references
them; cleanup releases them only after those references are durably cleared.

Hosted queries fail explicitly when publication owns the snapshot lock rather
than waiting beyond the query deadline. A failed or canceled query releases its
single-reader slot without waiting for publication.

## Evidence and rollout

The dedicated `Parquet and Session Archive CI` workflow
(`.github/workflows/session-archive.yml`) runs real production paths with a
checksum-verified runtime and rejects skipped conformance. A dedicated PostgreSQL
service also exercises native archive authority against canonical storage; each
test creates and removes an isolated schema. Its
`Real DuckDB session archive` job runs on relevant pull requests, main pushes,
and the daily full suite. To require the same
runtime locally, set `DUCKDB_PATH` and
`CAO_SESSION_ARCHIVE_REQUIRE_DUCKDB=1` when running archive integration tests.
Set `CAO_TEST_POSTGRES_URL` to a test PostgreSQL database whose role can create
schemas to include the native authority integration test without skips.
Run the fixed 1,048,576-event test with
`CAO_SESSION_ARCHIVE_BENCHMARK=1` and the same required runtime, selecting
`^TestDuckDBArchiveMillionEventBenchmark$`. Its sampled heap/temp figures are
not RSS guarantees.

Locally, functional Kusto evidence is opt-in: review the emulator license, then
explicitly set `ACCEPT_EULA=Y` for `scripts/test-kusto-emulator.sh` on an x86-64
host with SSE4.2 and AVX2 (ARM hosts are unsupported). Parquet and Session Archive
CI runs the same script on every trigger in its `session-archive-kusto` job, under
the repository's reviewed license acceptance recorded in the workflow. This runs the
production membership query template against the pinned official engine, not
live Azure storage. The script does not benchmark the emulator.

`TestAzureArchiveConformance` requires reviewed Blob/ADX configuration,
`CAO_SESSION_ARCHIVE_REQUIRE_AZURE=1`, and the separate
`CAO_SESSION_ARCHIVE_AZURE_ALLOW_WRITES=1` approval for a dedicated empty
`conformance-*` prefix and preprovisioned table, one exact synthetic repository,
and `CAO_SESSION_ARCHIVE_BLOB_RETIREMENT_GRACE=5m`. Run with `-timeout=15m`:
the persisted reader horizon and retirement grace require approximately
seven to nine minutes. It performs writes only when explicitly invoked
against that isolated approved destination. Required mode fails on missing
configuration; ordinary unit-test skipping is not evidence.

Do not mark the ADR accepted based only on unit tests or a simulated Azure
service. Production activation still requires the scenario's resource,
recovery, and destination evidence. Compaction and arbitrary SQL remain deferred.
