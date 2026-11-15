# Implementation status

Source SHA: `24f570e7eba32138d327c2b0a6aaac5e84bf443e` (this slice; previous slices authored at `7cb5134d…` / `93614994…` / `2fbe39c5…` / `a19872d3…`)
Governing contracts: `specs/control-architecture.md` (working draft v1.0.0) for the policy kernel;
`specs/server-operational-storage.md` (PostgreSQL operational adapter, `AuditLog`) for the storage slice.
`.github/workflows/shared/*`, `server/internal/operational/**` and `server/postgres/operational-grants.sql` are comparison evidence only.

## Implemented and verified (`mvn verify`: 86 tests, 0 failures, 0 skipped; baseline at this SHA was 73)

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
| CAO-EXE-005, §4.5, §5.3 `workflows` / `inventory`, §6.3 standalone record | `StandaloneResolver` (pure; no discovery, credentials or dispatch): `control-plane.workflows.<slug>` parsed strictly (enabled, mode, max-mode, caps, targets; workers/execution fields/`debug` rejected; declared targets must satisfy global scope) and `control-plane.inventory` bounds (1..100000 / 1..1000 / `cell-index < cell-count`). Identity must match `GITHUB_WORKFLOW_REF` (`<control>/.github/workflows/<slug>.lock.yml@…`); campaign, worker and dispatcher-envelope inputs rejected; undeclared/disabled workflow → `workflow-undeclared`/`workflow-disabled` denial record (no candidates, review mode). Precedence defaults < workflow < exact target < `max-mode`; requested mode/`max_repositories`/`rollout_percent` narrow only (widening and `debug` throw). Caller-supplied `InventoryEvidence` is the only repository source: missing, errored, wrong-source, out-of-allowed-list, oversize, or wrong-target evidence fails closed. Deterministic dedupe by id, id-ordered sort, `id % cell-count` cell, batch slice, archived/disabled exclusion, cap `min(max-repositories, ceil(n·rollout/100))` (empty set ⇒ 0 ⇒ no-op), per-target mode and destination (live → target; review → admitted destination, central or caller-attested private, never equal to target, owner-scoped). Record fields match §6.3 (`control_role: standalone`, empty campaign/worker, `candidate_repositories` with per-target `safe_output_mode`/`safe_output_repo`, empty `worker_workflows`, batch id, `policy_source`); `inventory_version` equals the Node.js resolver's sha256 over the sorted JSON lines (vector computed from a copy of that formula, asserted in `StandaloneResolverTest`) |
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

- Standalone: repository inventory and destination privacy are caller-supplied evidence; no GitHub API adapter reads or authenticates them, `archived`/`disabled` filtering trusts that evidence, and `GITHUB_WORKFLOW_REF`/`GITHUB_WORKFLOW_SHA` are not verified against the running workflow. The policy is not fetched at the exact SHA by Java. Only the `workflows` and `inventory` sections of the schema are parsed, not full JSON Schema validation; `activity`, `web`, `publishing` etc. are still rejected. Credential-reach checks (§6.2 step 6) and API-capacity checks are not implemented. The comparison with the Node.js resolver is limited to the inventory digest vector and reading its logic; no differential run of the whole record was performed.

- Operational adapter covers only the `AuditLog` service and its tables. Capabilities in health/readiness remain `unsupported` because no other operational service (cache, limiter, sessions, revocations, leases, queue, quota, CLI commands) exists; the Go `Audit` capability has no field in the OpenAPI `OperationalCapabilities`.
- Audit: no `admin.rebuild.*` lifecycle callers, no atomic audited operations, no evidence-reader/retention role provisioning (`operational-grants.sql` is only mirrored for the audit grants in tests), no `RetainAudit` Java wrapper (retention SQL is exercised directly), no connection pool (bounded per-call connections, max 4), no cancellation propagation beyond JDBC query timeouts, and the fixed default capacity config is the only accepted one.
- Schema owner DDL is an audit-only subset of `server/internal/operational/postgres/schema.sql`; the Go store's full contract (records, cache, queues, webhook tables) is not reproduced, so a database provisioned by Go and one by Java are not interchangeable beyond the audit tables.
- Real TLS to a remote PostgreSQL was not exercised (only URL validation); the packaged-jar smoke (BootstrapMain → Main → Java HTTP client on 127.0.0.1) used trust auth on numeric loopback and observed 503 with `operational.connected=true`; an unprovisioned namespace exited 1. A curl smoke was denied by the sandbox, so a Java client was used.

- Health/readiness: no per-client rate limiting (429), no authorized `revision`/`counts` details. curl smoke of the packaged jar was denied by the sandbox; only startup (`listening on 127.0.0.1:18080`) was observed. HTTP behavior is verified by in-process tests over real loopback sockets.

- Debug mode: admission logic takes caller-supplied `ManualDebugEvidence`; nothing yet reads the authoritative GitHub event or checks permission live (T-RES-008 recheck at precompute is the caller's duty), and gh-aw staging (T-GHA-004) is out of scope. Ordinary `PolicyResolver` still rejects `debug`; persistent debug policy stays invalid.
- Policy schema: only `scope`, `defaults`, `campaigns` (mode, caps, targets, workers). Not a full JSON Schema validation.

## Not implemented

Orchestrator effective records, `control-plane.operations` and authority envelopes (§4.6),
`web.host`, host extensions, `target-authority`, authority envelopes,
exact-SHA policy fetch, canonical ingestion/storage (PostgreSQL), Dashboard Language
execution, all other HTTP routes, authentication, operational state beyond `AuditLog` (sessions, revocations, leases, queues, quota, rate limiting, CLI commands), canonical PostgreSQL adapter, container image build verification.

## Blocked

None. Environment note: the sandbox JDK truststore was empty and `~/.m2` unwritable, so Maven ran with a truststore built from the system CA bundle (`MAVEN_OPTS=-Djavax.net.ssl.trustStore=…`) and `-Dmaven.repo.local` under /tmp (local only). PostgreSQL server binaries (`/usr/lib/postgresql/<n>/bin` or `CAO_TEST_POSTGRES_BIN`) are required for `verify`; their absence fails the build rather than skipping.

## Next bounded slice

Either canonical PostgreSQL storage (read `server/spec/storage.tsp` and `specs/dashboard-data.md` ingestion
sections; separate canonical namespace; ingest a synthetic canonical JSONL fixture through a Java ingestion contract;
drive `data.available`/`evaluatedAt` from the published revision) or the orchestrator effective record
(§6.3: worker eligibility/skip reasons and the standard dispatch envelope, reusing `StandaloneResolver`'s inventory partitioning).
