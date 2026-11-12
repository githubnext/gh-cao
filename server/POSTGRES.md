# PostgreSQL provisioning, runtime, and audit retention

Production startup opens existing state; it does not provision tables, replace
functions/triggers, create namespaces, retain audit history, or maintain
canonical partitions. This applies to `serve`, `serve-hosted`, external hosts,
`ingest`, `collect`, `execute-cli`, and production `cao-webhook-gateway` roles.
Provision before deploying this version.
There is no automatic privileged fallback. Local development uses the same
PostgreSQL bootstrap, runtime, retention and evidence-reader lifecycle.

## Local PostgreSQL development

Local development uses the production PostgreSQL canonical and operational
adapters. Start PostgreSQL with the existing server/docker-compose.yml or use
an explicitly selected local instance. Set CAO_POSTGRES_URL for runtime access
and CAO_DATABASE_ADMIN_URL only for separately invoked schema-owner bootstrap.

Provision both stores with cao-dashboard database bootstrap --store canonical
--namespace local and the same command with --store operational. Start serve
with --redis-namespace local and optionally --source DIRECTORY to ingest an
explicitly acquired, verified canonical JSONL artifact. Set
CAO_OPERATIONAL_NAMESPACE=local for independent executor and audit processes.
Native CLI queries use CAO_DATABASE_NAMESPACE=local.

There is no file-database adapter, automatic conversion or fallback. Runtime,
retention, schema-owner and SELECT-only audit-reader credentials retain their
separate authorities locally as well as in production. Readiness failures do
not create empty evidence or bootstrap missing storage.

## Canonical query resource bounds

Canonical reads use a read-only, repeatable-read transaction. Its 60-second
context deadline starts **before connection-pool admission** and covers setup,
metadata, SQL resource statistics, output reads, and commit. A shorter caller
deadline wins. Transaction-local `statement_timeout`, `lock_timeout`, and
`idle_in_transaction_session_timeout` are clamped to the remaining deadline;
any stricter nonzero operator role/session ceiling is preserved. Setup errors
abort the read rather than executing without these settings. Commit/rollback
restores the pooled session settings. Client cancellation requires cooperating
driver/database behavior; this is not a hard CPU-instruction or I/O-byte quota.

JIT and nested-loop planner preferences remain disabled locally as before.
Each canonical store's pool admits at most four connections (including writes
and maintenance), not four per user. Pool wait is deadline-bounded, not an
independent fleet-wide admission queue. Replicas, other stores/pools, parallel
query workers, and other database clients are outside that concurrency bound.
HTTP identity/edge rate limits remain separate controls, not a database
concurrency or memory quota.

Logical input/intermediate rows, estimated bytes, operations and output budgets
are checked using SQL statistics **after that SQL executes**. They do not cap
physical scan bytes, database working memory, temporary disk, total provider
cost, or server-wide resource use. A small result can require a large scan/sort.
The application does not widen or configure `work_mem`, `hash_mem_multiplier`,
`temp_file_limit` or parallel-worker ceilings. In particular, `work_mem` applies
per operation/worker, not per request; `temp_file_limit` is per PostgreSQL process
and excludes explicit temporary tables. Effective role/provider ceilings,
capacity, replica count and cancellation behavior require operator evidence.
Do not grant privileged database settings to runtime solely to run these tests.

Reproduce bounded synthetic cancellation/spill measurements against an **owned,
disposable local PostgreSQL** database, never production or a shared deployment:

```sh
go -C server test ./internal/postgresx \
  -run 'TestReadTransaction(ResourceCeilings|PhysicalCancellation|PhysicalSpillMeasurement|PlannerSettingsAreLocal)$' \
  -count=1 -v
```

Inject `POSTGRES_URL` separately using a private Unix socket or loopback endpoint.
The existing test fixture creates and removes an isolated schema and requires
bootstrap/partition privileges; the temporary-file rejection subtest additionally
requires permission to set `temp_file_limit`. Without `POSTGRES_URL`, these
service-backed tests explicitly skip; without that setting privilege, only the
temporary-file rejection subtest skips. Tests report elapsed cancellation time
for a 50ms statement ceiling, 50ms pool wait and statistics lock wait, plus a
100ms caller deadline. A 20,000-row generated sort under operator-selected
64kB `work_mem` reports `EXPLAIN (ANALYZE, BUFFERS)` temporary blocks, sort disk
space (PostgreSQL reports kB), and execution milliseconds; a 64kB operator
`temp_file_limit` must reject that spill with SQLSTATE `53400`. Values vary by
PostgreSQL version and host; this is a bounded reproduction, not a fleet capacity
benchmark or production operating-effectiveness evidence.

For representative workload regression measurements, the existing
`npm run test:performance:dashboard-query-cost-postgres` uses production
ingestion/query execution against a local database and downloaded artifacts.
Its wall time and logical Go metrics complement, but do not replace, physical
database/provider measurements. No deployed settings or rollout are changed.

## Authorities

Use a dedicated database/schema and fresh roles with no inherited privileged
membership. A schema is the database authorization boundary: application
namespaces isolate records, not mutually distrusting database principals.
Use separate schemas/databases and separate role sets for independently
authorized deployments. Do not grant runtime or evidence roles membership in
the owner, retention, `pg_write_all_data`, or other privileged roles.

| Authority | Allowed | Not allowed |
| --- | --- | --- |
| Bootstrap/partition operator | Assume the non-login schema owner; create/upgrade schema, provision namespaces, maintain canonical partitions, explicitly destroy a namespace | Credentials must not be available to web, executor, collector, or exporter processes |
| Runtime | Read/write operational records, cache, queues/output/execution identities; update only namespace budget columns; audit SELECT/INSERT; canonical ingestion/query DML; temporary staging tables; fixed canonical ANALYZE function | Audit UPDATE/DELETE/TRUNCATE, namespace DELETE/rename/config changes, permanent DDL, trigger changes, audit retention |
| Audit retention | SELECT namespace version/config; EXECUTE the fixed audit-retention function | Direct audit reads/writes/deletes, operational mutation, arbitrary cutoff, DDL, namespace destruction |
| Evidence reader | SELECT namespace version/config and audit rows; ListAudit/VerifyAudit | INSERT, UPDATE, DELETE, maintenance, retention, DDL |

Runtime row locks, budget accounting, sequencing, and audited queue mutations
remain in their original database transactions. `audit verify` additionally
appends its verification result, so that command needs the runtime audit grant;
direct `AuditLog.VerifyAudit` and `audit export` work with the evidence reader.
`audit lifecycles` also uses this SELECT-only reader, performs bounded
`ListAudit` calls without appending its result, and requires no operational or
canonical write grants. See the [missing-terminal runbook](README.md#missing-rebuild-terminal-evidence)
for row/deadline bounds, incomplete-history alerts and manual reconciliation.
The exported `OpenAuditStore` uses the existing-state diagnostics opener and
does not initialize or maintain storage.

## Fresh deployment

Have the database administrator create distinct non-login group roles and
assign separately authenticated, non-superuser logins. Authenticate via your
secret manager/provider; the following SQL intentionally contains no passwords:

```sql
CREATE ROLE cao_owner NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE cao_runtime NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE cao_retention NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE cao_evidence NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE SCHEMA cao AUTHORIZATION cao_owner;
```

The database administrator must also grant CONNECT to the service principals
and TEMPORARY to `cao_runtime` on the target database (replace `cao_dashboard`
with its actual name):

```sql
GRANT TEMPORARY ON DATABASE cao_dashboard TO cao_runtime;
```

This is only for ingestion staging tables, not permanent schema DDL. A schema
owner cannot grant database-level rights; do not rely on PUBLIC's default
TEMPORARY privilege in a hardened database.

Grant each service login only its corresponding group. Give the separately
controlled bootstrap login permission to `SET ROLE cao_owner`; provision while
that role is active so tables **and** security-definer functions have the same
non-login owner. Restrict database CONNECT and schema CREATE, review PUBLIC and
default privileges, and ensure no runtime-writable schema precedes `cao` in its
search path. Do not use blanket `GRANT ALL`, `ALL TABLES`, default table grants,
superuser service logins, or ownership as a shortcut. The grant scripts assume
fresh roles; they are not a general privilege scrubber for previously overgranted
roles or column ACLs.

Supply `CAO_DATABASE_ADMIN_URL` only to the short-lived operator job, selecting
the database/schema and owner role through connection options. It is a separate
input and never falls back to `CAO_POSTGRES_URL`. For example, when the URL is
already injected into the operator environment, `PGOPTIONS` can select the
owner and schema without placing credentials in arguments:

```sh
PGOPTIONS='-c role=cao_owner -c search_path=cao' \
  cao-dashboard database bootstrap --store operational --namespace hosted-dashboard
PGOPTIONS='-c role=cao_owner -c search_path=cao' \
  cao-dashboard database bootstrap --store canonical --namespace hosted-dashboard
```

Use the exact operational `CAO_OPERATIONAL_NAMESPACE` and resolved capacity
environment settings used by every replica. The canonical namespace is the
existing `REDIS_NAMESPACE` (the hosted default is `hosted-dashboard`).
With separate databases/schemas, supply the corresponding owner connection for
each command. Both bootstrap orders are supported in a shared schema: canonical
bootstrap's exact-name allowlist includes all operational tables, including
`cao_operational_cli_executions`; it still rejects unknown tables.

Apply the checked-in, explicit grants after bootstrap. For `psql`, configure
`PGHOST`, `PGPORT`, `PGDATABASE` (database name), `PGUSER`, and a restricted
`PGPASSFILE`, or use `PGSERVICE`/`PGSERVICEFILE`, separately from
`CAO_DATABASE_ADMIN_URL`. Preserve the deployment's TLS settings. Do not pass a
credential-bearing URL in arguments:

```sh
PGOPTIONS='-c role=cao_owner -c search_path=cao' \
  psql -X -v ON_ERROR_STOP=1 -v schema=cao -v runtime=cao_runtime \
  -v retention=cao_retention -v reader=cao_evidence \
  -f server/postgres/operational-grants.sql
PGOPTIONS='-c role=cao_owner -c search_path=cao' \
  psql -X -v ON_ERROR_STOP=1 -v schema=cao -v runtime=cao_runtime \
  -f server/postgres/canonical-grants.sql
```

Only apply the relevant script in each schema. Runtime then uses its own
connection through `CAO_POSTGRES_URL` and the policy-selected operational URL
environment variable. Neither policy nor service containers receive
`CAO_DATABASE_ADMIN_URL`. Grant scripts do not create logins or change passwords.
The canonical grant explicitly excludes operational tables, audit retention,
and namespace deletion even when both stores share a schema.

Canonical publication must refresh planner statistics inside its ingestion
transaction. PostgreSQL 16 silently skips direct ANALYZE by non-owners.
Bootstrap therefore defines `cao_analyze_native()` with only the fixed,
schema-qualified canonical entity tables. Runtime receives EXECUTE, not
ownership; PUBLIC and evidence readers do not. The existing ingestion context
bounds and cancels this operation, which accepts no SQL or object-name input.

## Existing deployment upgrade and rollback

1. Pause admissions, drain/stop web, executor, collector, and backfill processes.
   Preserve pending revocations and accepted work. Back up the operational
   database and record externally stored audit anchors. Do not discard the
   namespace or start a new one to avoid this migration.
2. Review current ownership, role memberships, schema/default privileges,
   column grants, and replication/superuser capabilities. Create fresh restricted
   service roles. Have the DBA transfer the exact CAO tables, associated
   sequences, and functions to the dedicated non-login owner if necessary.
   Do not run database-wide `REASSIGN OWNED` against shared unrelated objects.
3. Using the new binary as that owner, run operational bootstrap for the existing
   namespace and capacity configuration. Its transaction installs the owner-only
   namespace-delete guard, replaces the audit guard, installs the restricted
   retention function, and upgrades namespace version 1 to 2 without deleting
   records, changing budgets, or rewriting the audit chain. Runtime Open rejects
   version 1. Configuration mismatches abort the migration.
4. Run canonical bootstrap with the same configured retention windows, then
   apply both grant scripts as applicable. The canonical physical contract is
   unchanged by this release. If a different release changed that immutable
   contract, provision a **separate canonical database** and re-ingest; never
   drop a shared schema containing operational audit/queue/revocation state.
5. Schedule the two separate maintenance jobs below, switch service secrets to
   the restricted logins, and start one canary. Check readiness, audited
   admission/cancellation, queue recovery, canonical ingestion/statistics,
   external anchor verification, and maintenance job results before resuming.
   Retire the old owner-capable service credential and verify it is not mounted
   in runtime processes. Record the actual grants and rollout evidence.

Do not roll back to the pre-separation binary using an owner credential: it
replaces audit triggers on startup and performs retention in runtime maintenance.
Keep admissions stopped and use a reviewed forward fix, or an independently
reviewed restore procedure that preserves external anchors and unfinished work.
Applying this code alone does not remove previously distributed credentials or
establish SOC 2 compliance.

## Separate scheduled maintenance

Run canonical maintenance **at least daily** under the owner job, not a runtime
service. It provisions the configured retained historical window plus the
current and **28 upcoming UTC days**, then detaches/drops expired whole days.
Partitions are named `<table>_dYYYYMMDD` and cover midnight to midnight UTC.
Only days ending at or before the applicable retention cutoff are removed;
the partial cutoff day remains. Maintenance lag can extend physical retention.
Use the same `CAO_POSTGRES_RUN_RETENTION_DAYS` (default 30) and
`CAO_POSTGRES_LINKED_RETENTION_DAYS` (default 7) as runtime:

```sh
cao-dashboard database maintain --store canonical --namespace hosted-dashboard
```

For a seven-day window for both runs and linked detail, explicitly set both
retention variables to `7` in the owner job and runtime. Defaults and existing
overrides are unchanged. Weekly layouts (`<table>_wYYYYMMDD`) are rejected;
provision a fresh canonical schema or database and re-ingest authoritative
inputs. There is no in-place conversion. Preserve separately owned operational
audit, queue, and revocation state.

Runtime startup verifies the complete expected partition window without DDL.
Missing schema, statistics capability/grant, or partitions fail startup with an
operator action. Long-running processes do not repair partitions: alert on any
missed/failed daily maintenance job and repair before the 28-day runway is
exhausted. Missing ingestion days remain a hard error, not silent data loss.
After extended downtime, run maintenance before restarting. Parent-table
runtime grants cover future partitions; do not grant privileges on each child.

Run `retain-audit` using a **retention-role** connection injected as
`CAO_DATABASE_ADMIN_URL` in a different restricted job. Do not use the owner
credential for routine audit retention:

```sh
cao-dashboard database retain-audit --store operational --namespace hosted-dashboard
```

Each call reports `audit_events_removed` and deletes at most 512 events older
than 400 days as a contiguous oldest prefix. It always keeps the newest event,
so sequence numbers never restart. Schedule enough bounded invocations for
your event rate and alert on job failure/backlog; export and anchor before
retiring evidence, and suspend the retention job for a legal hold.

The SECURITY DEFINER function pins `search_path` to `pg_catalog, pg_temp`,
schema-qualifies its tables, serializes with appends on the namespace row, and
accepts only an existing namespace in its own schema. It accepts no cutoff or
object name. PUBLIC/runtime/evidence EXECUTE is revoked. A user-controlled
`cao.audit_retention` setting is not authority, and a namespace-delete guard
checks the caller before a foreign-key cascade can assume table-owner identity.
Owner/DBA break-glass destruction remains possible and must be separately
approved, logged, and covered by external evidence retention.

## Verification

Use an isolated loopback PostgreSQL administrator and `psql`, never production:

```sh
POSTGRES_URL=... go -C server test ./internal/server \
  -run 'TestPostgres(RuntimePrivilegeBoundary|RetentionPrivilegeBoundary|ProvisioningRequiredBeforeRuntime)' -count=1
```

These tests create real distinct logins and a non-login owner, apply the shipped
SQL, exercise runtime factory startup and atomic audited operations, prove
canonical statistics change during restricted ingestion, and reject direct
audit mutations, TRUNCATE, DDL, trigger changes, SET ROLE, retention calls, and
namespace cascades. They also cover SELECT-only export/verification, bounded
retention, temporary-table shadowing, owner-only upgrades, and missing-partition
repair. Existing operational conformance tests cover budgets, scoped queue
transactions, restart, and atomic rollback. The tests do not establish deployed
role grants, credential isolation, maintenance schedules, backups, or compliance.
