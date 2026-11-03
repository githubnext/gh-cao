---
name: soc2-server-assessment
description: Assess Go server SOC 2 readiness with component-scoped evidence, reproducible control checks, operational evidence requests, and operator decisions. Does not attest compliance or apply remediation.
---

# Go server SOC 2 readiness assessment

Use for systematic SOC 2 readiness checks of the Go server, or a bounded
component investigation. This is a readiness assessment, not an auditor's
opinion, certification, penetration test, or authorization to change production.
Do not label the system "SOC 2 compliant" based on source, tests, or this skill.

## Scope and authority

Read `ARCHITECTURE.md`, `specs/server-soc2-compliance.md`, `server/SOC2.md`,
`server/SOC2-CHANGELOG.md`, `server/SECURITY.md`, and the relevant normative
specifications before assessing a component. RFC-035 at the established SOC 2
specification path defines the maintained evidence, decision and closure
contract using BCP 14 and stable `S2-` requirement IDs. Applicable assessment
roles and unverified requirements must remain explicit; RFC conformance does
not establish SOC 2 compliance. Start from existing
controls, including the hosted CLI SOC 2 evidence boundary and independent audit
delivery contract; do not create a competing security specification.

Record the UTC assessment date, exact source SHA, component, deployment profile,
enabled features, trust boundaries, data classes, external dependencies, and
exclusions. Compare source SHAs before combining reports. Do not assume a
checked-in profile is deployed, a default is effective, or an optional feature
is enabled. Unknown deployed configuration remains unknown.

Security/Common Criteria (CC1-CC9) is the initial readiness baseline. Track
Availability (A1), Processing Integrity (PI1), Confidentiality (C1), and Privacy
(P1-P8) as applicability decisions, not silently excluded or implicitly
attested categories. Confirm commitments, system boundary, service providers,
complementary user-entity controls, and examination period with the operator
and independent auditor. Type 1 concerns design at a specified date; Type 2
also requires operating-effectiveness evidence throughout the specified period.
Tests run today cannot establish that historical operation.

Use the AICPA
[2017 Trust Services Criteria, revised points of focus 2022](https://www.aicpa-cima.com/resources/download/2017-trust-services-criteria-with-revised-points-of-focus-2022)
as the framework reference. Check the applicable edition with the auditor.
Criterion mappings below are working mappings, not a reproduction of the
standard or a substitute for the organization's approved control matrix.

## Procedure

1. Map the component's entry points, identities, authorization decisions,
   credentials, stores, outbound calls, privileged actions, and failure paths.
   Include local/hosted differences and optional adapters. Follow relevant
   callers and tests, not just package names or security documentation.
2. Build a control/evidence matrix. Cover logical and privileged access,
   revocation, encryption/key lifecycle, input and processing integrity,
   resource limits, change/release control, monitoring, incident response,
   recovery, retention/deletion, and vendor dependencies as applicable.
   Include organizational CC1-CC5 and CC9 controls as operator evidence
   requests rather than claiming source code satisfies governance.
3. Trace each claimed safeguard from requirement to implementation and test.
   Cite repository-relative `path:line` ranges tied to the recorded SHA.
   Documentation is intent evidence; implementation supports design;
   executed tests support only their stated scenario. Contradictions become
   findings. An absent search match is not proof of an absent control.
4. Use the narrowest existing deterministic test commands. Record exact
   command, exit status, passed/failed/skipped coverage, environment
   prerequisites, and evidence location. A skipped PostgreSQL/Redis or
   platform test is unverified, not a pass. Do not install dependencies until
   a selected check reports missing dependencies. Do not run full fleet stress
   tests, start services, or invoke live integrations merely for completeness.
5. Review errors, fail-closed behavior, recovery, replay/idempotency, privilege
   separation, and evidence completeness. Distinguish a confirmed design gap
   from an untested hypothesis and from missing deployed evidence.
6. Produce findings and operator decisions using the formats below. Preserve
   counter-evidence, compensating controls, residual risk, and limitations.
   Do not invent findings to fill the matrix or reduce a whole criterion to
   one automated test. Do not calculate a misleading compliance percentage.

## Evidence statuses

Use exactly one status per scoped control claim:

| Status | Meaning |
| --- | --- |
| `design-supported` | Cited implementation supports the stated design; no assertion about deployment or sustained operation |
| `design-gap` | A specific expected control property is demonstrably missing or contradicted |
| `evidence-needed` | Deployment, organization, period evidence, or implementation verification is missing |
| `not-applicable-proposed` | Exclusion has a rationale but awaits operator/auditor approval |

Record test execution separately as `passed`, `failed`, `skipped`, or `not-run`.
Record operating effectiveness separately as `not-assessed` or a precisely
bounded observation; never infer it from a design-supported status.

## Report contract

Begin with scope/SHA/profile/date, a concise component/data-flow map, reviewed
files and exclusions, and an honest conclusion. Include:

| Control claim | Working TSC mapping | Status | Source evidence | Test evidence | Deployment/period evidence needed |
| --- | --- | --- | --- | --- | --- |

Every finding must contain:

- Stable ID prefixed by the assigned workstream (`IAM`, `DATA`, `EXEC`, `OPS`).
- Title, severity (`high`, `medium`, `low`, `informational`), confidence
  (`high`, `medium`, `low`), status, affected profile, and working TSC mapping.
- Expected control property; observed behavior with exact source citations;
  condition and impact; counter-evidence and existing compensating controls.
- Reproduction or check and its actual outcome, including skipped coverage.
- Required operator evidence; proposed action and tradeoff; suggested
  accountable role and urgency; decision (`pending`, unless explicitly made).

Severity reflects control impact and context, not a claim of exploitability.
Keep hypotheses clearly marked and out of confirmed-gap counts.

End with a decision table:

| Finding/request | Operator choice | Suggested owner | Evidence needed to close | Decision |
| --- | --- | --- | --- | --- |

Choices can include investigate, remediate, collect evidence, accept a
time-bounded risk, or approve an exclusion. Risk acceptance needs an accountable
approver, rationale, compensating controls, expiry, and reassessment trigger.
The assessor must not accept risk or approve exclusions for the operator.
Request only sanitized evidence summaries/references, not secrets or raw
personal records.

## Delegated investigations

When the user requests sessions, use coordinated read-only component sessions
with non-overlapping primary ownership from `server/SOC2.md`. Provide the exact
source SHA and this skill. A worker may follow cross-boundary calls but must
report cross-component concerns to the coordinator rather than duplicate
another workstream's investigation. Do not spawn additional workers.

Save a Markdown report in the session artifact directory, not the repository,
unless the operator requested a checked-in report. Return its absolute path
and a concise handoff with findings, evidence requests, commands/outcomes,
limitations, and decisions. The coordinator preserves the source SHA and
citations while consolidating and deduplicating reports. Pending sessions
remain explicitly pending, not silently counted as reviewed.

When consolidating a received report, the coordinator MUST update
`server/SOC2.md` with material findings, limitations and pending decisions, and
append an event to `server/SOC2-CHANGELOG.md`. Follow the specification's stable
IDs, separate finding lifecycle and decision states, and evidence-backed
closure rules. Preserve original observations; corrections and decision
changes are new history events. Do not rely solely on session artifacts for
the durable finding description.

## Safety and completion gates

Assessment is read-only except explicitly requested skills/reports. Do not
change runtime code, rollout policy, permissions, retention, credentials,
deployment, or enabled features. Do not open issues, PRs, or remediation
sessions without authorization. Do not access production, exploit endpoints,
send repository contents to external services, read secret values, or log
tokens, cookies, prompts, payloads, personal records, or credential-bearing URLs.
Local synthetic tests are not production probes.

Before calling an assessment complete, account for every mapped component,
every in-scope criterion family, unexecuted checks, unresolved findings and
operator evidence requests. A complete readiness report may conclude that
compliance cannot be determined. Keep organizational and operating-evidence
gaps visible even if all code checks pass.
