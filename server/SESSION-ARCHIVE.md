# Private Parquet session archive

The [archive specification](../specs/session-archive.md) defines an additional,
default-off analytics service. Local servers use private Parquet plus DuckDB;
persistent hosted servers can use private Blob Parquet plus generation-bound
ADX queries. Existing dashboard SQLite/PostgreSQL and native ADX profiles are
not replaced. The [ADR](../adr/parquet-unified-session-storage.md) remains proposed
until its deployment acceptance gates have evidence.

## Before enabling

Approve exact collection scope, a versioned closed content/redaction policy,
rights, full-content readers, retention/replay horizons, destination suitability,
and resource budgets. Use a private persistent POSIX volume with verified lock,
rename, and durability semantics. Do not use a public shard directory, ephemeral
container filesystem, per-request Functions host, or a shared multi-replica mount.
Archive configuration never authorizes new GitHub acquisition.

The supplied standard Alpine images do not imply DuckDB compatibility or
provision a private archive volume. Use a reviewed persistent native host or
explicitly prepared image with a compatible pinned converter. Missing runtime
or durable storage fails startup; the server does not install binaries or
provision Azure resources.

## Pinned DuckDB

Use DuckDB CLI **v1.4.3**. Verify the release ZIP before extraction:

| Release asset | SHA-256 |
| --- | --- |
| `duckdb_cli-linux-amd64.zip` | `7a9e36141a71a353f54dfbeed607a5218c3425ce2d417610da08443259a12f7c` |
| `duckdb_cli-osx-universal.zip` | `f44e488b1c67d007b6b12af9f1ad9122fad0f63a606caf2bfb10ca945042a3f6` |

Download from `https://github.com/duckdb/duckdb/releases/download/v1.4.3/`.
Configure the reviewed executable path with the archive runtime configuration.
The converter checks `SELECT version()`; a different version is not silently
accepted. SQL comes only from generated trusted definitions, never request text.

## Local persistent server

Use `cao-dashboard serve` with the ordinary SQLite snapshot/overlay and local
GitHub credentials. `CAO_SESSION_ARCHIVE_ENABLED` is the independent opt-in.
The `CAO_SESSION_ARCHIVE_*` configuration owns a separate private archive
directory, namespace, exact repository allowlist, durable-storage approval,
content policy, runtime path, and budgets. Refer to
`internal/sessionarchive/config.go` for the complete validated configuration.
No existing ADX export environment variables are required.

Keep every archive repository within the SQLite snapshot's collection scope.
The existing backfill visits those repositories; capture occurs before
normalization and temporary cleanup. Failed archive admission leaves the
attempt eligible for retry. A successfully admitted trace survives cleanup of
the ordinary collection workspace. `--backfill=false` permits offline archive
reads and maintenance, not new acquisition.

Read access requires `CAO_SESSION_ARCHIVE_READ_APPROVED=true` in addition to
the ordinary loopback bearer capability. Treat that capability as granting
the explicitly reviewed archive content scope; do not share it as a
metadata-only dashboard token.

## Hosted persistent server

Use the existing OAuth host with one confirmed persistent replica and
co-resident collection workers. Native ADX export switches are independent.
Blob destination configuration uses `CAO_SESSION_ARCHIVE_BLOB_*`; the shared
archive configuration supplies private durable staging, content policy,
converter, and exact scope. No PostgreSQL archive tables or operational records
are created.

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

## Queries and operations

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
Review those recovery/deletion horizons separately. Source-cache deletion does
not delete archive history.

## Evidence and rollout

The dedicated `Parquet and Session Archive CI` workflow
(`.github/workflows/session-archive.yml`) runs real production paths with a
checksum-verified runtime and rejects skipped conformance. Its
`Real DuckDB session archive` job runs on relevant pull requests, main pushes,
and the daily full suite. To require the same
runtime locally, set `DUCKDB_PATH` and
`CAO_SESSION_ARCHIVE_REQUIRE_DUCKDB=1` when running archive integration tests.
Run the fixed 1,048,576-event test with
`CAO_SESSION_ARCHIVE_BENCHMARK=1` and the same required runtime, selecting
`^TestDuckDBArchiveMillionEventBenchmark$`. Its sampled heap/temp figures are
not RSS guarantees.

Functional Kusto evidence is opt-in: review the emulator license, then explicitly
set `ACCEPT_EULA=Y` for `scripts/test-kusto-emulator.sh`, or select the default-false
acceptance input when manually dispatching Parquet and Session Archive CI. This runs the
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
