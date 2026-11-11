# Implementation status

Source SHA: `2fbe39c5cfd0cf1af046a5e3e21ff67f327f20ac` (policy kernel authored at `a19872d3939b084a99a40a017d1fa4c59b0f491a`)
Governing contract: `specs/control-architecture.md` (working draft v1.0.0), with
`.github/workflows/shared/cao.schema.json`, `policy.mjs` and `authority.mjs` as comparison evidence only.

## Implemented and verified (unit tests, `mvn verify`: 34 tests, 0 failures)

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
| §7, Appendix B | `campaign-undeclared`, `campaign-disabled`, `worker-disabled` denials; unknown worker fatal; policy SHA-256 digest recorded |
| `server/spec/generated/openapi.json` `/api/health`, `/api/readiness` | JDK `HttpServer` on numeric loopback (non-loopback needs `CAO_ALLOW_NON_LOOPBACK=true`); HealthResponse/ReadinessResponse/OperationalState/DataState shapes; 503 when operational store or canonical data is missing (always today: `ServiceState.unconfigured()`); all capabilities `unsupported`; 404/405 otherwise; `Cache-Control: no-store`; 503 once shutdown begins; env config validation |
| Packaging (`Dockerfile`) | Non-root uid 10001, env-configured port, shutdown hook. Image NOT built (no Docker); not deployed |

## Partial

- Health/readiness: no per-client rate limiting (429), no authorized `revision`/`counts` details. curl smoke of the packaged jar was denied by the sandbox; only startup (`listening on 127.0.0.1:18080`) was observed. HTTP behavior is verified by in-process tests over real loopback sockets.

- Debug mode: always rejected (no verified manual-dispatch evidence model yet).
- Policy schema: only `scope`, `defaults`, `campaigns` (mode, caps, targets, workers). Not a full JSON Schema validation.

## Not implemented

Orchestrator/standalone effective records, standalone workflows and operations,
`inventory`, `web.host`, host extensions, `target-authority`, authority envelopes,
exact-SHA policy fetch, canonical ingestion/storage (PostgreSQL), Dashboard Language
execution, all other HTTP routes, authentication, operational state, PostgreSQL adapters, container image build verification.

## Blocked

None. Environment note: the sandbox JDK truststore was empty, so Maven needed a
truststore built from the system CA bundle to reach Maven Central (local only).

## Next bounded slice

Real PostgreSQL adapter (disposable local instance) behind a storage contract feeding `ServiceState`,
using the `storage.tsp` schema, so readiness reflects real connectivity; then orchestrator effective records.
