# Implementation status

Source SHA: `3d406df041cb02d60e05f23697c0e2cd5c90f082` (policy kernel from earlier accepted slice; health slice added on this SHA)
Governing contract: `specs/control-architecture.md` (working draft v1.0.0), with
`.github/workflows/shared/cao.schema.json`, `policy.mjs` and `authority.mjs` as comparison evidence only.

## Implemented and verified (`mvn verify`: 37 tests, 0 failures, 0 Checkstyle violations)

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
| `server/spec/generated/openapi.json` Health_health / Health_healthV1 (comparison: Go `/api/readiness`, absent from OpenAPI) | JDK `HttpServer` on configurable numeric host (default `127.0.0.1`), `GET/HEAD /api/health`, `/api/v1/health`, `/api/readiness`; HealthResponse shape (`status`, deprecated `redis`, `operational` with 8 capabilities, `data`); 200 healthy / 503 unhealthy; probe failure or timeout is unhealthy and never leaks error text; 404/405 elsewhere; security headers and `no-store`; no authorized-only `revision`/`counts` fields; graceful shutdown with 503 while draining |
| Packaging (unexecuted) | `Dockerfile` (non-root uid 10001, `HEALTHCHECK`, env-configured port), runtime deps copied to `target/lib` |

## Partial

- Health: the production `Main` wires placeholder probes reporting *unconfigured/no data* (always 503); no PostgreSQL or operational adapter exists. Rate limiting (429), authorized details, and the Go "externally hosted handler" startup state are not implemented.
- Container: Dockerfile was not built (no Docker in the sandbox); packaged jar start was verified, a curl smoke check was denied by the sandbox, so HTTP behavior is evidenced only by in-process JUnit tests against the real listener on 127.0.0.1.

- Debug mode: always rejected (no verified manual-dispatch evidence model yet).
- Policy schema: only `scope`, `defaults`, `campaigns` (mode, caps, targets, workers). Not a full JSON Schema validation.

## Not implemented

Orchestrator/standalone effective records, standalone workflows and operations,
`inventory`, `web.host`, host extensions, `target-authority`, authority envelopes,
exact-SHA policy fetch, canonical ingestion/storage (PostgreSQL), Dashboard Language
execution, query/ingestion HTTP API, authentication, operational state, rate limiting, hosted assets, revision events.

## Blocked

None. Environment note: the sandbox JDK truststore was empty, so Maven needed a
truststore built from the system CA bundle to reach Maven Central (local only).

## Next bounded slice

PostgreSQL canonical-storage adapter (`DataProbe` backed by real active-dataset state, tested against a
local PostgreSQL), then bearer-token authentication gating health details; orchestrator effective records
once inventory is modeled.
