# Local Kusto functional acceptance

Run the generated snapshot and unified-trace KQL against Microsoft's real
[Kusto emulator](https://learn.microsoft.com/en-us/azure/data-explorer/kusto-emulator-overview),
without Azure resources, live credentials, collection enrollment or training:

```sh
ACCEPT_EULA=Y bash scripts/test-kusto-emulator.sh
```

Review the [Microsoft license](https://aka.ms/adx.emulator.license) first.
**Benchmarks are prohibited.** This suite checks functional correctness only;
it makes no throughput, latency, storage-cost or capacity claims.

The script requires Docker through a local Unix socket, sufficient free image
extraction space and an x86 processor with SSE4.2/AVX2. Microsoft does not support
ARM. `DOCKER_CONTEXT` or `DOCKER_HOST` may select an existing isolated local
engine; the script never changes the active context, prunes images, restarts a
shared VM or provisions cloud services. The official image is pinned by digest.
It publishes only an ephemeral numeric-loopback port, records the image identity
and engine version, and removes its own container on exit.

For an already-running official emulator bound only to numeric loopback:

```sh
CAO_KUSTO_EMULATOR_URL=http://127.0.0.1:8080 \
  go -C server test -race ./internal/adxingest -run '^TestKustoEmulator' \
  -count=1 -timeout=15m -v
```

Each acceptance case creates a random isolated synthetic database and drops it
on completion. With no endpoint configured, ordinary Go tests explicitly skip
the engine cases. A configured but unavailable engine fails; there is no mock or
alternate-query-language fallback.

## Real and simulated boundaries

The suite reuses the existing Azure simulator's `LocalTransport` to confine
requests to exact numeric-loopback endpoints and prevent proxies, redirects and
DNS escape. `simulator.Kusto` supplies only the missing ADX transport: production
MultiJSON streaming requests become `.ingest inline` management commands using
the generated `cao_v1` JSON mappings. Production query bodies and responses pass
through unchanged. Kusto itself performs JSON mapping, scalar typing, query
parsing, joins, ordering, aggregation, SHA-256 checks, deduplication and generated
function execution. Bridge unit tests are protocol evidence only, not engine
acceptance.

The production scalar exporter and trace acquisition, private spool, delivery
worker, batching, restart, verification, correction, offline dataset transform
and withdrawal paths operate on synthetic inputs. Snapshot fixtures use the
existing bounded session-reader and operational-service test fixtures; traces
enter through real `Capture` using synthetic `aw_info.json`, collection shards
and `usage/aw_session.jsonl` files. This is pipeline acceptance, not another Azure
installer or collection-authority test. Existing Azure setup/deployment journeys
remain separate.

Covered assertions include scalar zero/null/absent fields, quality, retry
duplicates and immutable corrections; multipart UTF-8 whole-event reconstruction
against every production content digest; bounded timeline prefixes; recorded
harness/episode context; error, large-response and repeated-tool candidates;
full and frozen probability samples (including explicit zero selection and
source-gap exclusion from ready populations);
reasoning exclusion from a synthetic dataset; correction-driven frame/dataset
invalidation; and latest-revision withdrawal hiding retained historical rows.

Mining cases exercise the nine-table trace schema with synthetic bounded
`MiningReader` snapshots: exact canonical namespace/run/attempt/output bindings,
recorded repeated hops counted once per producing attempt, unknown producing
attempts without structured creation evidence, ambiguous outcome delivery,
independent reopen, as-of reconstruction, no-op source revisions preserving
semantic versions, regression invalidation, outcome dataset lineage and
withdrawal. Wrong host/category, superseded or unknown attempt, contradictory
context and missing selected source fail closed. Denominator fixtures retain
accepted, rejected, pending, bot-lifecycle and unknown resolutions separately.
The lifecycle case waits through two production minimum polling intervals; it
does not edit the spool ledger or relax runtime validation to accelerate tests.

The emulator has **no streaming or managed ingestion, Entra authentication,
authorization or transport encryption**, and its retention policies are not
honored. Streaming policy commands are explicitly skipped; retention declarations
are applied only as metadata. No test proves production streaming visibility,
identity/RBAC, retention, physical erasure, storage durability, upstream source
coverage or actual Azure installation. The bridge never changes production
HTTPS endpoint or credential validation. Destination/principal operational
acceptance remains required.

## Recorded functional evidence

On 2026-10-25, the full functional suite passed against the official image
`mcr.microsoft.com/azuredataexplorer/kustainer-linux@sha256:230588624dbd95b8575f2493c0a7599d42d099b01b5875c239704f7cdfc65192`.
The engine reported build `1.0.9791.31285`, product
`2026.10.22.1714-2642-db4842a-master`, built `2026-10-22T17:22:50Z`.
It ran in an isolated local Colima VM using Rosetta AMD64 translation on an ARM64
Mac. That is an observed successful run, **not** Microsoft support for ARM.

Real engine execution exposed the reserved `latest` KQL binding and real-valued
extent-size response that protocol mocks had missed. Those production fixes,
plus v1 response table-of-contents/status validation, are covered by the runtime
and acceptance suites. Real result truncation is also required to fail closed.
Recorded evidence is functional only; repeat the suite after contract changes
and perform the separate operational acceptance on the intended Azure service.
