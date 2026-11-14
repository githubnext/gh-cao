# Implementation status

Source SHA: `a2b14dd4709a7c50ae1bf004d6c7148569bd87da` (previous slices authored at `7cb5134d…` / `93614994…` / `2fbe39c5…` / `a19872d3…`)
Governing contracts: `specs/control-architecture.md` (working draft v1.0.0) for the policy kernel (this slice: §4.4 CAO-EXE-001, §5.3 `inventory`, §5.4, §6.2 steps 4-5, §6.3 orchestrator record, T-RES-007);
`specs/server-operational-storage.md` (PostgreSQL operational adapter, `AuditLog`) for the storage slice.
`.github/workflows/shared/*`, `server/internal/operational/**` and `server/postgres/operational-grants.sql` are comparison evidence only.

## Implemented and verified (`mvn verify`: 90 tests, 0 failures, 0 skipped; baseline at this SHA was 73)

| Spec reference | Behavior |
| --- | --- |
| CAO-CFG-002/003/004, T-CFG-001/002 | Strict parse: UTF-8, object root, `version` 1, duplicate keys, unknown properties, expressions rejected; Appendix A.1 accepted |
| T-CFG-004, A.2 | Execution fields (`engine`, `permissions`, `safe-outputs`, ...) rejected |
| CAO-CFG-007, T-CFG-005 | `debug` rejected in defaults, campaign, target and worker `max-mode` |
| §5.3 | Wildcard-free owner/repository scope; bounds 1..1000 / 1..100; slug identifiers; unique workflow per campaign |
| §5.4, T-RES-002/003 | Precedence defaults < campaign < exact target < worker ceiling; dispatch narrowing only; widening throws |
| T-ARC-001, T-ARC-003 | Out-of-scope target rejected; deprecated budget validated and ignored |
| §6.4, T-RES-004/005 | `WorkerRevalidator`: current policy intersected with the parent `DispatchEnvelope`; narrowing/revocation honored, broadening never widens; identity/target mismatch fatal |
| §6.3, T-RES-006/007 (worker only) | `EffectiveRecord`: worker/denial record with status, reason, identity, mode, repo, workflow SHA, policy digest, schema version, time, envelope |
| CAO-EXE-004, §6.3, T-EXE-002..005 (evidence-model level) | `ManualDebugAdmission`: single-run debug worker admitted only for `workflow_dispatch`, human non-bot actor equal to triggering actor, write/maintain/admin permission, matching target/output repos, `debug` event mode, no dispatcher correlation inputs; policy resolution (identity, enablement, scope, limits) still applies; record carries `launch_kind: manual-debug`, `debug_actor`, `safe_outputs_staged: true`, empty correlation fields, no envelope, empty candidate/worker lists. Missing evidence fails closed. Expected denials return a plain denial record |
| §5.3 `inventory`, T-CFG-001/002 | `control-plane.inventory` parsed with schema defaults (1000 / 1 / 0 / 100000 / 0); bounds 1..100000, 1..1000, `cell-index` < `cell-count`, `batch-size` 1..100000, non-negative batch index; unknown keys and non-integers rejected |
| §5.3 deterministic partitioning, §4.5 | `InventoryPartition`: id de-duplication, id ordering, `id mod cell-count` cell, batch slice, `batch_id`; batch index beyond a non-empty cell and inventory above `max-scan-repositories` are fatal; empty cell yields an empty batch. `RepositoryMetadata.canonicalJson()` reproduces `JSON.stringify` (member order, escapes, `null`); the `sha256:` inventory version, batch ids and selected ids for the synthetic fixture equal values produced by `control.mjs`'s algorithm run under Node (asserted in `InventoryPartitionTest`); results are independent of discovery order |
| §4.4 CAO-EXE-001, §6.2 steps 4-5, T-ARC-001 | `OrchestratorPlanner` (pure; evidence is caller-supplied, no GitHub access): inventory source must be `allowed_repos` exactly when `allowed-repositories` is set and the list must equal that allow-list; every inventory repository must be inside owner/repository scope; `safe_output_repo` owner must be in scope, and a non-control review destination must be verified private (absent/false evidence is fatal); per-target mode (exact target over campaign, lowered — never raised — by the request); archived/disabled repositories excluded; eligible worker workflows with skip reasons; effective cap = min(policy cap, ceil(candidates × rollout%), floor(dispatch_max ÷ min(eligible workers, dispatch_max))); zero without eligible workers or candidates; unavailable inventory, bad `dispatch_max`, no configured workers, denied/worker requests fatal |
| §6.3, T-RES-006/007 (orchestrator) | `EffectiveRecord.buildOrchestrator`: authorized record with identity, status/reason, mode/routing, control repo, workflow SHA, policy digest, schema version, time, inventory version, batch identity, configured/effective caps, discovery status (`inventory_status: complete`), bounded candidates (each with its own `safe_output_mode`), `worker_workflows`; no dispatch envelope. `EffectiveRecord.build` now returns a denial record (no candidates/workers, cap 0) for denied orchestrators |
| §7, Appendix B | `campaign-undeclared`, `campaign-disabled`, `worker-disabled` denials; unknown worker fatal; policy SHA-256 digest recorded |
| `server/spec/generated/openapi.json` `/api/health`, `/api/readiness` | JDK `HttpServer` on numeric loopback (non-loopback needs `CAO_ALLOW_NON_LOOPBACK=true`); HealthResponse/ReadinessResponse/OperationalState/DataState shapes; 503 when operational store or canonical data is missing (always today: `ServiceState.unconfigured()`); all capabilities `unsupported`; 404/405 otherwise; `Cache-Control: no-store`; 503 once shutdown begins; env config validation |
| `specs/server-operational-storage.md` — PostgreSQL operational adapter, `AuditLog` | `PostgresOperationalStore` implements the audit service over real PostgreSQL (tested against a disposable loopback PostgreSQL 18 started by the tests, never skipped): append serializes on the locked `cao_operational_namespaces` row, gap-free per-namespace sequence, database `clock_timestamp()` truncated to ms, `hash = SHA-256(prev_hash ‖ record)` over the stored canonical record; `list` (filter by event/actor/after-sequence, limit ≤ 1000); `verify` (repeatable-read read-only) detects edits, gaps, broken chain, indexed-column divergence, anchor mismatch and head truncation; anchors before the retained window count as expired |
| Same — canonical record compatibility | `CanonicalAudit` reproduces Go `encoding/json` bytes (HTML-safe escapes, U+2028/2029, UTF-8 key order, shortest RFC 3339 fraction, `omitempty`); a TSV of rows generated by the real `audit.go` at the source SHA is asserted byte- and hash-equal, and rows with those exact bytes inserted into PostgreSQL verify in Java and can be extended by Java |
| Same — input bounds | event name/actor ≤ 1024 bytes, no NUL, outcome ∈ success/denied/failure, ≤ 64 attributes, values ≤ 4096 bytes, record ≤ 64 KiB; lone surrogates rejected |
| Same — runtime/owner separation | `open` runs no DDL and fails closed when the namespace is unprovisioned, at another schema version, or its stored capacity config differs; `OperationalBootstrap` (owner only, `BootstrapMain`) creates the audit subset of the Go schema (append-only trigger, owner-only namespace delete guard, `cao_operational_retain_audit` SECURITY DEFINER with pinned search path, PUBLIC revoked). Tests prove a SELECT/INSERT-only runtime role cannot run DDL, UPDATE/DELETE audit rows, delete namespaces or call retention; retention deletes a contiguous oldest prefix (≤ 512, ≥ 400 days kept, newest row always kept) |
| Same — transport | `PostgresEndpoint`: `postgres://` only, one TCP host, non-loopback requires `sslmode=verify-full` (`require`/`verify-ca`/`prefer` rejected), unknown URL parameters and Unix sockets rejected, password absent from `toString()` and errors |
| `/api/health`, `/api/readiness` with the real store | `PostgresServiceState` probes the store on each request: connected when the namespace is provisioned and reachable; stopping the database flips `operational.connected`/`recoveryReady` to false. Data stays unavailable (503) because canonical storage is not implemented. `Main` opens `CAO_POSTGRES_URL` (namespace `CAO_OPERATIONAL_NAMESPACE`, else `CAO_DATABASE_NAMESPACE`, else `hosted-dashboard`) and exits non-zero for an insecure, invalid or unprovisioned store |
| Packaging (`Dockerfile`) | Non-root uid 10001, env-configured port, shutdown hook. Image NOT built (no Docker); not deployed |

## Partial

- Operational adapter covers only the `AuditLog` service and its tables. Capabilities in health/readiness remain `unsupported` because no other operational service (cache, limiter, sessions, revocations, leases, queue, quota, CLI commands) exists; the Go `Audit` capability has no field in the OpenAPI `OperationalCapabilities`.
- Audit: no `admin.rebuild.*` lifecycle callers, no atomic audited operations, no evidence-reader/retention role provisioning (`operational-grants.sql` is only mirrored for the audit grants in tests), no `RetainAudit` Java wrapper (retention SQL is exercised directly), no connection pool (bounded per-call connections, max 4), no cancellation propagation beyond JDBC query timeouts, and the fixed default capacity config is the only accepted one.
- Schema owner DDL is an audit-only subset of `server/internal/operational/postgres/schema.sql`; the Go store's full contract (records, cache, queues, webhook tables) is not reproduced, so a database provisioned by Go and one by Java are not interchangeable beyond the audit tables.
- Real TLS to a remote PostgreSQL was not exercised (only URL validation); the packaged-jar smoke (BootstrapMain → Main → Java HTTP client on 127.0.0.1) used trust auth on numeric loopback and observed 503 with `operational.connected=true`; an unprovisioned namespace exited 1. A curl smoke was denied by the sandbox, so a Java client was used.

- Health/readiness: no per-client rate limiting (429), no authorized `revision`/`counts` details. curl smoke of the packaged jar was denied by the sandbox; only startup (`listening on 127.0.0.1:18080`) was observed. HTTP behavior is verified by in-process tests over real loopback sockets.

- Debug mode: admission logic takes caller-supplied `ManualDebugEvidence`; nothing yet reads the authoritative GitHub event or checks permission live (T-RES-008 recheck at precompute is the caller's duty), and gh-aw staging (T-GHA-004) is out of scope. Ordinary `PolicyResolver` still rejects `debug`; persistent debug policy stays invalid.
- Policy schema: only `scope`, `defaults`, `inventory`, `campaigns` (mode, caps, targets, workers). Not a full JSON Schema validation.

## Not implemented

Standalone effective records (`control_role: standalone`), standalone workflows and operations,
`web.host`, `activity`, `publishing`, host extensions, `target-authority`, authority envelopes,
exact-SHA policy fetch, canonical ingestion/storage (PostgreSQL), Dashboard Language
execution, all other HTTP routes, authentication, operational state beyond `AuditLog` (sessions, revocations, leases, queues, quota, rate limiting, CLI commands), canonical PostgreSQL adapter, container image build verification.

## Contract notes (this slice)

- Archived/disabled exclusion for orchestrators: §4.5 requires it for standalone runs and CAO-EXE-001 says an orchestrator "filters"; the comparison `control.mjs` only filters standalone candidates, so an orchestrator there counts and lists archived/disabled repositories. Java excludes them (narrowing only) and the rollout cap counts only retained candidates. This differs from the comparison implementation and needs maintainer confirmation.
- Java partitions before filtering (like `control.mjs`); the batch therefore may contain fewer admitted candidates than `batch-size`.
- `localeCompare` ties (same id) cannot occur after id de-duplication, so id ordering is sufficient.
- Not covered: orchestrator `workflow_dispatch` inputs `max_repos`/`rollout_percent` narrowing only via the existing `ResolutionRequest`; cell/batch dispatch inputs (§5.4 table) and `safe_output_repo` input selection are caller-supplied evidence, not parsed; the live-orchestrator output-destination rule is not separately checked; compiled-workflow worker discovery (`parseFrontmatterWorkers`), GitHub inventory loading, API-capacity and credential-reach checks (§6.2 step 6) and `activation_ready` gating on live credentials are not implemented — the record is produced from supplied evidence only.

## Blocked

None. Environment note: the sandbox JDK truststore was empty and `~/.m2` unwritable, so Maven ran with a truststore built from the system CA bundle (`MAVEN_OPTS=-Djavax.net.ssl.trustStore=…`) and `-Dmaven.repo.local` under /tmp (local only). PostgreSQL server binaries (`/usr/lib/postgresql/<n>/bin` or `CAO_TEST_POSTGRES_BIN`) are required for `verify`; their absence fails the build rather than skipping.

## Next bounded slice

Canonical PostgreSQL storage (unchanged choice; the policy kernel now covers orchestrator records, standalone records remain an alternative): read `server/spec/storage.tsp` and `specs/dashboard-data.md` §§ on ingestion,
bring up a separate canonical namespace/adapter, ingest a synthetic canonical JSONL fixture through a Java ingestion
contract, and drive `data.available`/`evaluatedAt` from the published revision. Alternatively, orchestrator/standalone
effective records (control-architecture §6.3).
