# Optional ADX session analytics

CAO can export retained canonical session metadata to an existing
Azure Data Explorer destination, including a suitable
[free cluster](https://learn.microsoft.com/en-us/azure/data-explorer/start-for-free).
The add-on is Go code in the existing server, **disabled by default**. It reuses
canonical PostgreSQL, protected operational state, host lifecycle and metrics.
It introduces no webhook, GitHub acquisition, Node process, database schema
migration, queue platform, Blob account, Event Hub, or separate service.

[Unified trace capture](ADX-TRACES.md) is a separate default-off option using the
existing collection visit and private durable storage, not canonical snapshot
rows. It supports the same approved evidence at any destination capacity.
The snapshot-only scope and deletion limitations below do not describe that
option's separate trace/tombstone lifecycle.

## Scope and guarantees

This deliberately small v1 exports **snapshots**, not a lossless event log.
After ordinary CAO ingestion, one worker walks completed retained Runs in exact
configured repository scope. It reads each Run and its available observations
in a bounded, repeatable-read transaction, then closes that transaction before
any Azure operation. Changes appearing behind the cursor are picked up in the
next revision sweep. Intermediate versions replaced between sweeps, deleted
Runs, expired evidence, in-progress Runs, and evidence never collected by CAO
are not reconstructed or exported.

Before contacting ADX, the worker persists one immutable, allowlisted projection
and its cursor in existing protected operational state. Pending payload is at
most 256 KiB, total state at most 512 KiB, and a session at most 1,024 rows.
Destination failures retain that pending payload across restarts and canonical
replacement. Pending delivery expires after seven days and becomes an explicit
gap. Oversized sessions also advance the cursor with a durable gap count and
last-gap Run/revision; they are never published as truncated complete sessions.
This bounded summary is not an exhaustive historical gap ledger.

An unchanged canonical revision causes no ADX calls. Changed snapshots reuse
content-addressed projection IDs; a one-hour delivery marker avoids resending
unchanged facts, with exact visibility checks before reuse. Retrying a request
can create physical duplicates. Curated KQL deduplicates them and selects the
latest ready source revision, rather than summing raw append-only rows.

The destination has six tables:

| Table | Evidence |
| --- | --- |
| `SessionFacts` | Run identity, attempt, status, conclusion, clocks, model/engine and canonical accounting |
| `ToolEvents` | Native tool observations; starts and completions are not invented invocations |
| `FirewallEvents` | Canonical domain/request observations, including decision, counts and latency when retained |
| `GraderObservations` | Run-linked grader values, status and evaluator/experiment identity |
| `EvalObservations` | Explicit canonical answers, including UNKNOWN |
| `SessionVersions` | Ready source revision, expected per-family counts and content identity |

The editable model is [`spec/adx.tsp`](spec/adx.tsp), separate from the HTTP and
PostgreSQL contracts. TypeSpec generates both the Go projection allowlist and
the operator KQL, so column types and canonical field selection share one source
of truth. Regenerate with `npm --prefix server/spec run generate:adx` after
installing the pinned spec dependencies with `npm --prefix server/spec ci`.
Do not hand-edit `internal/adxingest/schema.gen.go` or `schema.gen.kql`.
Ordinary server-spec generation/checks include ADX; contract tests verify
canonical field/type compatibility and exact generated-artifact parity.

`PresentFields` preserves absent versus present scalar evidence; `NullFields`
records explicit nulls, including strings that Kusto otherwise coerces to empty.
Zero remains zero; native event and observed clocks stay distinct. Source-level
availability/completeness/freshness remain qualified, not upgraded to full
session coverage. A compact conclusion is not full-session evidence or an
independently verified GitHub outcome.

Only already persisted canonical scalar evidence is exported. In particular,
there is no raw session or per-request `UsageObservations` table, grader
definition/unit export, prompt, argument, tool result, failure text, URL, title,
or arbitrary JSON extension. Grader values must remain grouped by their grader
identity; do not compare units that were not exported. Run accounting is
reconciled source evidence, not authoritative billing.

## Free-cluster prerequisites

The free offering advertises approximately 100 GB uncompressed / 20 GB
compressed, ten databases, 100 tables/database and 200 columns/table. It has
no SLA, autoscale, private-network enterprise features or purge. Access is
granted for one year and may renew, expire, be suspended or be discontinued.
Its terms advise against processing personal or legally/regulatorily controlled
data and exclude relevant DPA protections.

**Use synthetic data first.** A real deployment requires an operator assessment
that the approved metadata is non-personal and non-regulated. Even repository
IDs, run IDs, domains, tool names and correlation IDs may be identifying.
Hashing them is not automatically anonymization. Setting the acknowledgment
below records operator intent; it is not a PII detector or a compliance
attestation. If private networking, physical purge or other unavailable
guarantees are required, do not enable this free-compatible add-on for that data.

1. Create the free cluster/database using its supported UI. No Azure
   subscription is required by the free ADX destination itself.
2. Register a Microsoft Entra application and grant it destination database
   ingestion and read/size-inspection permissions. The worker needs to query
   its facts and `.show database ... extents`; verify the exact principal's
   access. Keep database-owner setup privileges separate from runtime.
3. Print the setup script, review it, and run its KQL in the destination:

   ```sh
   go -C server run ./cmd/cao-dashboard adx schema
   ```

   It creates/merges six snapshot and nine trace tables, JSON mappings, streaming policies,
   seven-day soft-delete retention and deduplicating KQL functions.
   Schema, mappings and projection metadata are generated from `spec/adx.tsp`.
   This command is offline; the runtime never runs DDL. Wait for streaming
   schema/mapping propagation before enabling the worker.
4. Deploy exactly **one exporter replica** with a persistent worker lifecycle
   and durable deployment-scoped operational coordination/state. The existing
   PostgreSQL operational backend is recommended. Volatile memory, local
   SQLite/Pages, admission-only hosts and per-request platform workers are
   unsupported. Normal hosted OAuth/authorization requirements still apply.

The public-cloud query endpoint must be HTTPS under `.kusto.windows.net`,
without a path, port, credentials or query string. Do not use an `ingest-`
endpoint. The Go HTTP client uses the documented direct streaming protocol and
existing OAuth2 client-credentials library, rather than adding another Azure
SDK dependency or a managed-streaming fallback to queued/Blob ingestion.
Requests use the public-cloud Kusto token audience. Redirects are rejected.
Application secrets must use existing environment/secret-store injection,
never reviewed policy files or command arguments. Certificate and managed
identity credential modes are not implemented in v1.

## Configuration

Add these settings to the reviewed CAO deployment; leave rollout/collection
authority unchanged. Repository IDs are exact **canonical IDs**, not patterns
or an invitation to discover new repositories. They must already be collected
and published in this CAO namespace.

| Environment variable | Value |
| --- | --- |
| `CAO_ADX_ENABLED` | `true`; unset or `false` disables all add-on work |
| `CAO_ADX_ENDPOINT` | Existing public-cloud cluster query endpoint |
| `CAO_ADX_DATABASE` | Existing database, ASCII letter followed by letters/digits/underscores, at most 100 characters |
| `CAO_ADX_TENANT_ID` | Entra tenant GUID |
| `CAO_ADX_CLIENT_ID` | Explicit application GUID |
| `CAO_ADX_CLIENT_SECRET` | Secret-store-injected application secret |
| `CAO_ADX_REPOSITORY_IDS` | Comma-separated unique canonical repository IDs, at most 1,000 |
| `CAO_ADX_NON_PERSONAL_DATA` | `true` only after dataset suitability approval |
| `CAO_ADX_DATA_APPROVED` | Alternative universal approval that corpus/use and actual destination satisfy applicable terms, privacy and governance; required if the legacy non-personal-data acknowledgment is not used |
| `CAO_ADX_SINGLE_REPLICA` | `true` only with exactly one exporter replica |
| `CAO_ADX_INTERVAL` | Retry/poll interval, default `30s`, range `1s` to `1h` |
| `CAO_ADX_MAX_STORAGE_BYTES` | Uncompressed extent-data budget across all owned snapshot/trace tables, default 50,000,000,000, range 1 MiB to 1 TiB; size it for the actual destination |

Configuration is validated before starting the worker. Invalid enabled
configuration fails startup, with no alternate credential/destination fallback.
Disabled mode does not construct destination clients, obtain tokens, read or
write export state, start a worker, or alter collection and canonical retention.
Explicitly disabling programmatic `server.Config.ADX` overrides environment.

## Capacity, health and retries

The worker is serial, processes at most 100 sessions per pass, bounds each step
to 30 seconds and each streaming request to 1 MiB. It checks destination extent
`OriginalSize` against its budget before submission. Unavailable size evidence,
query errors and capacity exhaustion stop submission rather than bypass the
check. Extents do **not** include all streaming-buffer/index overhead or other
databases: use a dedicated destination or reserve that headroom by lowering the
budget. Fifty GB is an application ceiling, not a guarantee that the free
cluster can ingest it at a particular compression ratio. No throughput or
indefinite retention guarantee is implied.

ADX throttling, outages and ambiguous writes retry at the configured interval;
there is no tight retry loop, recollection, paid fallback or automatic upgrade.
Visibility verification compares the exact deduplicated table/row-digest set
and rejects partial query failures. A ready manifest is submitted last and is
itself verified before the local cursor advances. The marker is not an ADX
multi-table transaction. Use `cao_SessionFacts()`, `cao_ToolEvents()`, etc.,
rather than querying retryable raw tables for totals.

`App.ADXStatus()` reports separate stage/degraded state and historical gap count.
Existing ingestion counters include `adxExported`, `adxFailure` and `adxGap`;
they do not change core readiness or assert complete source coverage.
Warnings contain reason codes, not payloads/credentials. `DEBUG=cao:adx`
enables additional debug lifecycle messages. Normal dashboard and collection
readiness do not depend on destination availability.

Freshness includes existing acquisition, CAO's default five-minute canonical
projection cadence, export sweeps, throttling and query visibility. A free
cluster cannot be assumed to sustain the earlier million-sessions/day scenario.
For example, at hypothetical 100 KB/session, 10,000 sessions/day is 1 GB/day,
100,000 is 10 GB/day, and one million is 100 GB/day **before** retry/index/version
overhead. Shorter retention or less scope requires explicit operator approval.
The free cluster charge is $0 under its terms; existing CAO hosting, operational
storage, CPU, network and secret-store operations are not guaranteed free.

## Disable, recovery and deletion

Set `CAO_ADX_ENABLED=false` and restart/drain the existing host to stop capture
and remote writes. Pending state is retained and can resume with the identical
destination/scope/contract only while unexpired. Changes to scope/destination
get separate protected state; previously pending jobs are not silently sent
under new configuration. Remove abandoned state through the existing backend's
operator procedure after confirming no worker owns it.

Disabling is **not erasure**. V1 does not propagate canonical deletions or
policy withdrawal as destination tombstones, and does not implement physical
purge. Apply the reviewed ADX retention/soft-delete or operator table/database
removal procedures independently. Logical deletion is not proof of physical
erasure. Do not enable this exporter where automatic withdrawal/deletion
propagation is a prerequisite. If rebuilding a canonical namespace from scratch,
use a fresh export namespace/destination or explicitly reconcile old destination
versions; source revisions are monotonic only within the original namespace.

## Local simulator validation

The ordinary unit tests use a loopback HTTP protocol simulator. It checks the
actual REST paths, mappings, authorization, verification queries and manifests,
preserves duplicate rows, and injects ambiguous writes, partial query failures,
missing rows and capacity exhaustion. It is **not a Kusto engine** and does not
validate KQL execution, free-cluster permissions or Azure capacity.

```sh
go -C server test -race ./internal/adxingest ./internal/server ./cmd/cao-dashboard
```

To include the production canonical reader and durable PostgreSQL restart path,
point `POSTGRES_URL` at an isolated local PostgreSQL database whose test role
can create schemas:

```sh
POSTGRES_URL='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable' \
  go -C server test -race ./internal/adxingest -count=1
```

Tests use a private canonical schema and a unique operational namespace.
Before a real enablement, apply the generated schema and exercise synthetic
direct ingestion, exact visibility, corrected versions, size permissions,
retention and disable/resume using the actual free cluster/principal.
Simulator success alone is not that free-cluster acceptance evidence.
