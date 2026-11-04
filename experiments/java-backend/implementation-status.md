# Implementation status

Source SHA: `f5acee05a2a514a651702026e1a57fb4a50a3712`
Governing contract: `specs/control-architecture.md` (working draft v1.0.0), with
`.github/workflows/shared/cao.schema.json`, `policy.mjs` and `authority.mjs` as comparison evidence only.

## Implemented and verified (unit tests, `mvn verify`: 20 tests, 0 failures)

| Spec reference | Behavior |
| --- | --- |
| CAO-CFG-002/003/004, T-CFG-001/002 | Strict parse: UTF-8, object root, `version` 1, duplicate keys, unknown properties, expressions rejected; Appendix A.1 accepted |
| T-CFG-004, A.2 | Execution fields (`engine`, `permissions`, `safe-outputs`, ...) rejected |
| CAO-CFG-007, T-CFG-005 | `debug` rejected in defaults, campaign, target and worker `max-mode` |
| §5.3 | Wildcard-free owner/repository scope; bounds 1..1000 / 1..100; slug identifiers; unique workflow per campaign |
| §5.4, T-RES-002/003 | Precedence defaults < campaign < exact target < worker ceiling; dispatch narrowing only; widening throws |
| T-ARC-001, T-ARC-003 | Out-of-scope target rejected; deprecated budget validated and ignored |
| §7, Appendix B | `campaign-undeclared`, `campaign-disabled`, `worker-disabled` denials; unknown worker fatal; policy SHA-256 digest recorded |

## Partial

- Debug mode: always rejected (no verified manual-dispatch evidence model yet).
- Policy schema: only `scope`, `defaults`, `campaigns` (mode, caps, targets, workers). Not a full JSON Schema validation.

## Not implemented

Effective record (§6.3), worker revalidation (§6.4), standalone workflows and operations,
`inventory`, `web.host`, host extensions, `target-authority`, authority envelopes,
exact-SHA policy fetch, canonical ingestion/storage (PostgreSQL), Dashboard Language
execution, HTTP API, authentication, operational state, container packaging.

## Blocked

None. Environment note: the sandbox JDK truststore was empty, so Maven needed a
truststore built from the system CA bundle to reach Maven Central (local only).

## Next bounded slice

Effective record (§6.3) with worker revalidation against a parent envelope (T-RES-004/005, T-RES-007),
then a minimal HTTP health/readiness service with a non-root container.
