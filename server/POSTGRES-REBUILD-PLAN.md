# PostgreSQL ingestion/query architecture status and verification

## Current architecture

`spec/storage.tsp` is the physical storage contract; its generated
`internal/postgresx/schema.sql` is the standalone representation. Hosted
ingestion and query execution use native relational tables. The generic
canonical/source machinery and compatibility readers have been removed. The
server starts with a fresh database: **no old-database migration, backward
compatibility, dual writes, alternate readers, or old-format fallback.**

This document records the original native-Postgres cutover goals and the
remaining verification work. The production server now executes hosted query
plans as SQL; the historical step descriptions below are not a claim that the
pre-cutover fallback architecture remains active.

## Starting point

The current implementation initializes fifteen TypeSpec-generated native
entity tables, uses compact missing/null presence bits, and stores canonical
data without generic row/value tables or serialized documents. Ingestion streams
manifested shards through a bounded batched writer and publishes entities,
quality metadata, and revision atomically. The hosted SQL compiler executes
Dashboard Language plans in PostgreSQL; the separate operational adapter owns
operational state and a bounded response cache.

The work still to replace is concrete:

| Surface | Current implementation | Replacement |
| --- | --- | --- |
| Ingestion | Manifest-verified phased streaming into bounded native-table batches | Keep generated table bindings and source validation aligned; verify memory bounds and atomic publication |
| Structured evidence | Query-consumed fields are represented in the TypeSpec-generated entity tables; no generic value trees/documents | Keep schema/query field coverage current; use typed columns or relational fields, never generic persisted values |
| Auxiliary data and metadata | Explicit typed tables, runtime providers, quality metadata, and revision state | Preserve the typed persistence/runtime boundary |
| State and diagnostics | Native state/quality tables and SQL-derived diagnostics | Keep integrity checks bounded and fail closed |
| Hosted queries | Validated Dashboard Language DAGs compile to PostgreSQL SQL; unsupported shapes fail closed | Prove deployed-corpus execution and browser/Postgres semantic parity |
| Read APIs | Generated table bindings and SQL-backed query result reads | Keep all hosted entity reads on the PostgreSQL SQL boundary |

## Target rules

- One authoritative Postgres representation per entity; no generic canonical
  row store, EAV/tree codec, JSON documents, or stored report-derived copies.
- Retain only fields consumed by declared queries, relationships required to
  produce them, and necessary identity/publication state. Any added column must
  have a query or lifecycle consumer.
- Keep schema ownership in TypeSpec. Generate DDL, Go input/result bindings,
  column registries, and relationship/index declarations together; fail the
  build if generated output drifts.
- Treat the database as disposable derived state. Recreate it and ingest
  authoritative deployed inputs after a physical contract change; do not
  introduce conversion code.
- Queries, joins, calculations, grouping, filtering, ordering, and pagination
  execute in Postgres. Go validates, binds parameters, coordinates transactions,
  enforces limits, and serializes bounded results; it does not evaluate row
  operations or reconstruct business relationships.
- Preserve explicit empty, unavailable, partial, stale, zero, absent, and null
  states. Do not infer outcomes, graders, or value from successful execution.
- Preserve namespace isolation, server-side credentials, authentication,
  read-only GitHub authority, and atomic publication. The operational adapter owns operational
  state, not a second canonical entity store.

## Completed cutover

The hosted implementation now has a generated relational schema and typed
PostgreSQL ingestion path. Ingestion streams manifested inputs in bounded
batches and publishes entity rows, quality metadata, fingerprint, evaluation
time, and revision atomically. There are no persistent publication generations,
dual-format tables, or compatibility reads.

The hosted query endpoint, entity/detail reads, MCP query tools, diagnostics,
and query-cost path use PostgreSQL. The query package retains request
vocabulary, validation, dependency planning, limits, and byte estimation; the
Go row evaluator has been removed, and unsupported query shapes fail closed.
The response contract still includes the legacy `fallbackOperations` metric as
an empty compatibility field. Operational storage owns operational state and a bounded
completed-response cache; it does not store canonical entities or execute
dashboard queries.

## Remaining verification

- Run the PostgreSQL integration test that loads the active root dashboard
  fragments and canonical database query definitions, then executes each
  definition through the production SQL-plan path. Its empty runtime-source
  fixtures prove SQL compilation and execution, not provider behavior or
  result-value parity.
- Keep the browser/PostgreSQL parity harness as the semantic check for browser
  behavior. It covers selected parity cases; it is not a substitute for
  executing every deployed definition through PostgreSQL.
- Run TypeSpec generation and contract checks after API-contract changes, and
  keep generated OpenAPI/schema artifacts synchronized with the source.
- Continue testing atomic publication, namespace isolation, bounded ingestion
  and query resources, cancellation, pagination, and fresh-database rebuilds.
- Use the deployed query-cost benchmark to investigate meaningful regressions.
  Corpus execution is coverage of declared definitions, not a claim about
  production traffic volume or workload performance.

The completion gates are: every active deployed query definition is validated
and executes through PostgreSQL SQL; browser/server semantics remain covered
by the parity harness; generated storage/API artifacts are current; and
publication and query execution remain bounded, atomic, and fail-closed.
