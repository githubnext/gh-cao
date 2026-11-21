# ADR 21371: Treat gh-aw v2 `job_id` as diagnostic and bind broker authority to signed identity

## Status

Draft

## Context

- Pull request 21371 aligns the request-only organization GitHub App token broker in `server/` with gh-aw's Token Broker Specification v2.0.0 (2026-11-19). The PR body and `server/spec/token-broker.tsp` describe the v2 exchange and invalidation envelopes, which add an optional string `job_id`.
- Before this PR, the executor's job isolation depended on signed claims only. `executionKey` in `server/internal/tokenbroker/service.go` hashes the signed issuer, owner and repository IDs, workflow reference and SHA, ref, event, run ID, run attempt and `check_run_id`. The PR leaves that key unchanged and does not include `job_id`.
- `server/internal/tokenbroker/protocol.go` now validates every member of the request as a JSON string, rejects `null`, and deletes `job_id` before the request is read. `TestDiagnosticJobDoesNotSelectCredentialContext` checks that a caller-supplied job name cannot select another signed job's credentials or cleanup scope.
- The PR adds signed-claim gates in `server/internal/tokenbroker/policy.go` and `server/internal/admission/identity.go`. A signed `ref_type` must match the enrolled ref (`refs/heads/` as `branch`, `refs/tags/` as `tag`). A signed `runner_environment` must be `github-hosted`. Self-hosted or missing values fail closed (`TestJobAndPolicyBinding`).
- `server/internal/admission/identity.go` rejects JWT headers `jku`, `x5u`, `jwk` and `x5c` before any key discovery. `TestVerifierRejectsUntrustedKeyHeaders` asserts that rejected headers trigger no discovery.
- `server/internal/tokenbroker/service.go` now returns 403 for a non-invalidation exchange after the job has been invalidated. `copilot-task` and `github-app` return 501 only after identity and enrollment checks, and no credential fallback exists.
- The supported capability set remains `copilot-request` only. `ARCHITECTURE.md`, `docs/authentication.md`, `server/SECURITY.md` and `server/spec/README.md` state that no GitHub tool or safe-output credential is issued.

Not inferable from current pull request evidence: the gh-aw release or deployment that first emits `ref_type` and `runner_environment` claims, the operator's enrolled workflow list, and whether any existing self-hosted runner enrollment must be preserved.

## Decision

Accept the optional string `job_id` on gh-aw v2 exchange and invalidation envelopes as diagnostic metadata only. The broker never uses it for authorization, credential selection, registry keys or cleanup scope. The signed `check_run_id`, together with the existing signed run and workflow identifiers, remains the sole job-isolation boundary.

In the same change, the broker requires signed evidence before issuance:

- `ref_type` must match the enrolled branch or tag ref;
- `runner_environment` must be `github-hosted`;
- JWT key-selection headers (`jku`, `x5u`, `jwk`, `x5c`) are rejected.

The supported capability set stays `copilot-request` only. Known `copilot-task` and `github-app` requests return 501 after identity and enrollment checks. Exchanges after invalidation return 403.

The decision is limited to the organization-billed `copilot-request` broker profile. It grants no rollout, admission, repository-write, GitHub-tool or safe-output authority, and it does not change `.github/workflows/cao.json` policy semantics.

## Alternatives Considered

1. **Use the caller-supplied `job_id` as the job key.** This would let a diagnostic name select registry entries and cleanup scope. The PR rejects it: `server/SECURITY.md` says neither `job_id`, baggage, nor trace headers authorize issuance or select cleanup scope, and `TestDiagnosticJobDoesNotSelectCredentialContext` enforces that rule. Its weaknesses are that the value is unsigned and that gh-aw job names are not isolation evidence.
2. Not inferable from current pull request evidence. The PR accepts `job_id` as valid and states that requests without it remain valid. No rejected option that refuses v2 envelopes containing `job_id` is recorded.

## Consequences

**Positive**

- gh-aw v2 clients that send `job_id` are accepted, and requests without it stay valid. Compatibility does not widen authority.
- Job isolation still rests on signed identity claims that the executor already verifies, so revocation and cleanup keep the same key.
- Signed runner and ref-type checks narrow issuance to GitHub-hosted, branch or tag executions that match the enrolled ref.
- Rejecting key-selection headers closes a route where a token could direct key discovery to an external location.
- Known unsupported capabilities return a deterministic 501 after authentication and enrollment, without issuing a fallback credential.

**Negative and tradeoffs**

- Any enrolled workflow on a self-hosted runner, or any token without `runner_environment` or a matching `ref_type`, is now denied. Operators must confirm their gh-aw release emits these claims before enrolling workflows.
- `job_id` is not verified. It can be wrong or misleading in diagnostics and must not be used as evidence.
- Runner loss still leaves issued credentials usable until downstream expiry, because job-completion webhook cleanup (SV-37) is not implemented, as `server/SECURITY.md` records.
- The normative contract is split across `server/spec/token-broker.tsp`, the generated `server/spec/generated/openapi.json`, `ARCHITECTURE.md` and `server/SECURITY.md`. This ADR records rationale only and does not duplicate those requirements.
- Maintainers own acceptance of this draft. The PR's offline tests do not establish real GitHub OIDC acceptance; `server/SECURITY.md` requires that evidence before claiming integrated support.
