# RFC-035: Go server SOC 2 readiness and evidence

**Status:** assessment specification; readiness undetermined. This is not an
attestation, certification, deployment acceptance, or operating-effectiveness
opinion.

**Scope:** source-bound Go server readiness assessments, maintained evidence
and decision records, and explicitly authorized bounded remediation.

**Safety default:** read-only assessment, unknown evidence remains unknown,
no inferred production or remediation authority.

**Record schema version:** 1. RFC editorial form does not change existing
record identifiers, statuses, decisions, or closure semantics.

**Maintainer:** the server assessment coordinator. Service owners and the
independent auditor retain organizational and engagement approval authority.

## Abstract

CAO needs a repeatable way to inspect server controls without confusing source
code, synthetic tests, proposed patches, deployed configuration, and historical
operation. This RFC defines conformance requirements for assessors, evidence
records, operator decisions, consolidation, and a daily SelfCare worker.
It preserves uncertainty and human authority while allowing source-verifiable
technical findings to progress through reviewed remediation.

This is a repository RFC, not an IETF standard or an AICPA SOC 2 report.
Current observations and decisions belong in the
[assessment register](../server/SOC2.md); dated changes belong in the
[append-only changelog](../server/SOC2-CHANGELOG.md), not in RFC status prose.

## Conformance and terminology

The key words **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHALL NOT**,
**SHOULD**, **SHOULD NOT**, **RECOMMENDED**, **NOT RECOMMENDED**, **MAY**, and
**OPTIONAL** are interpreted as in BCP 14
([RFC 2119](https://www.rfc-editor.org/rfc/rfc2119),
[RFC 8174](https://www.rfc-editor.org/rfc/rfc8174)) when capitalized.

Sections 1-12 define normative requirements. The abstract, references and open
questions are informative except where a normative section explicitly adopts
a referenced contract. Examples and rationale do not establish control
satisfaction or implementation status.

The conformance classes are **assessor**, **assessment coordinator**,
**record publisher**, and **SelfCare worker**. A conforming implementation
MUST satisfy requirements applicable to each role it performs. The
**operator** and **independent auditor** supply approvals and external evidence;
the implementation MUST NOT impersonate either role.

| Term | Meaning |
| --- | --- |
| Assessment baseline | Exact source SHA and declared profiles against which an observation was made |
| Control claim | Narrow, scoped property with an identified authority and evidence requirements |
| Design evidence | Documentation/implementation evidence of a property, not proof of deployment or sustained operation |
| Operating evidence | Attributed observation of actual controls in an identified environment and period |
| Finding | Stable-ID record of a design gap, evidence request, or proposed applicability exclusion |
| Decision | Explicit authorized disposition, independent of evidence status and finding lifecycle |
| Remediation proposal | Reviewed-source change or PR; neither integration nor deployment is implied |
| Closure | Recorded decision and verified evidence satisfying the specific finding's closure conditions |

Requirement identifiers prefixed `S2-` are stable within this RFC. They
identify assessment obligations, not AICPA criteria or a compliance score.

## 1. Purpose and authority

**S2-A01** This specification defines how CAO maintains a systematic SOC 2 readiness
assessment for the Go server. It MUST NOT be represented as an attestation,
certification, auditor opinion, or proof of operating effectiveness.

**S2-A02** The assessment MUST use the applicable AICPA Trust Services Criteria agreed
with the operator/auditor. The initial framework reference is the
[2017 Trust Services Criteria with revised points of focus, 2022](https://www.aicpa-cima.com/resources/download/2017-trust-services-criteria-with-revised-points-of-focus-2022).
Mappings here are working assessment mappings, not the standard's complete
requirements or an approved engagement control matrix.

**S2-A03** This contract governs assessment records, not new runtime behavior.
`server/SECURITY.md` and domain specifications remain authoritative for
implementation requirements. A discovered mismatch MUST cite the relevant
requirement; proposed hardening MUST NOT silently become a mandatory existing
control. Assessment work MUST NOT change policy, credentials, permissions,
retention, deployment, or enabled features without separate authorization.

## 2. Maintained records

| Record | Purpose | Maintenance rule |
| --- | --- | --- |
| This RFC | Stable scope, evidence model and assessment lifecycle | Update when assessment requirements change; increment record schema version for incompatible record/decision semantics |
| [Assessment register](../server/SOC2.md) | Current component inventory, investigation coverage, findings and operator decision register | Update as reports arrive and decisions/evidence change; explicitly retain pending work |
| [Assessment changelog](../server/SOC2-CHANGELOG.md) | Append-only assessment and decision history | Add dated events for each material assessment/specification change |
| [Assessment skill](../.github/skills/soc2-server-assessment/SKILL.md) | Repeatable investigation procedure | Keep aligned with this RFC; it MUST NOT weaken evidence or approval gates |
| Session report artifacts | Detailed bounded investigation evidence | Preserve source SHA, report provenance and accessible references when consolidating |

**S2-R01** The specification, current register, and changelog MUST link to each other.
The current register MUST be understandable without retrieving a transient
session. Material findings, limitations and pending decisions MUST be
consolidated there; session-only paths MAY retain detailed supporting output
but MUST NOT be the sole durable description of a finding.

## 3. System scope and applicability

**S2-S01** Each assessment MUST record its UTC date, source SHA, assessment/specification
version, component owners, evaluated profiles/features, data classes, system
boundaries, external dependencies, explicit exclusions, and deployed evidence
availability. The component inventory in `server/SOC2.md` is the coverage index.

**S2-S02** Local PostgreSQL/capability access, hosted/external listeners, Azure Functions,
PostgreSQL/Redis/memory operational adapters, admission-only operation,
gateway ingress/dispatch, and experimental CLI executor platforms MUST be
distinguished. Optional features MUST NOT be assumed enabled. A checked-in
template MUST NOT be treated as deployed configuration.

**S2-S03** Security/Common Criteria CC1-CC9 is the initial readiness baseline. Additional
categories MUST remain explicit applicability decisions until approved:

| Working family | Required assessment coverage |
| --- | --- |
| CC1-CC2 | Accountability, personnel/security responsibilities, communication and policy evidence |
| CC3-CC5 | Risk assessment, control selection, monitoring of controls and documented ownership |
| CC6 | Identity lifecycle, logical/privileged access, credentials, data protection and access boundaries |
| CC7 | Detection, event evaluation, response, recovery and evidence completeness |
| CC8 | Reviewed changes, tested releases, provenance, deployment approvals and rollback |
| CC9 | Vendor/dependency risks, continuity and risk mitigation |
| A1 | Proposed availability commitments, capacity, backups and tested recovery objectives |
| PI1 | Proposed processing commitments, validated inputs, completeness, accuracy and replay/atomicity semantics |
| C1 | Proposed confidential-data classification, access, retention and disposal commitments |
| P1-P8 | Proposed personal-information lifecycle, notices/consent, use/disclosure, rights and monitoring obligations |

This table is a coverage prompt, not satisfaction of a criterion. Governance,
personnel, provider, deployment, and historical-operation claims require
organizational evidence; source review alone cannot establish them.

**S2-S04** The operator/auditor MUST approve the examination objective, system
description, commitments, category applicability, complementary user-entity
controls and any service-provider carve-outs. Type 1 design evidence at a
specified date MUST be distinguished from Type 2 operating-effectiveness
evidence over a specified period. Until agreed, these fields remain unknown.

## 4. Control and evidence records

**S2-E01** Every scoped control claim MUST contain a stable identifier, component/profile,
working TSC mapping, expected property and its authority, evidence status,
implementation citations, test execution status, operating-evidence status,
limitations and requested closure evidence.

**S2-E02** Evidence status MUST be one of:

| Status | Meaning |
| --- | --- |
| `design-supported` | Cited implementation supports a narrowly stated design property |
| `design-gap` | Evidence demonstrates a specific missing or contradicted expected design property |
| `evidence-needed` | Required implementation, deployment, organization or period evidence is unavailable |
| `not-applicable-proposed` | A reasoned exclusion is proposed but has not been approved |

**S2-E03** A status MUST NOT imply whole-criterion coverage. Test status MUST separately
be `passed`, `failed`, `skipped`, or `not-run`. Operating effectiveness MUST
separately be `not-assessed` or a bounded observation identifying the actual
environment, observation period, sampling method and limitations.

**S2-E04** Source citations MUST identify `path:line` ranges at the recorded SHA. Evidence
records MUST distinguish documentation intent, implementation design, executed
test observations and actual operation. Commands MUST record their outcome,
prerequisites and skipped coverage. An overall successful command with skipped
database/platform tests MUST NOT be reported as verified coverage of those
paths. An unavailable check MUST remain visible.

**S2-E05** Sanitized evidence references SHOULD include provenance, collection date,
responsible role and applicable period. Secrets, credential-bearing URLs,
raw customer records, personal access-review lists and sensitive production
outputs MUST NOT be committed. Restricted organizational evidence MAY remain
in an approved operator-controlled system with a non-sensitive reference here.

## 5. Findings and operator decisions

**S2-F01** Findings MUST have stable workstream-prefixed IDs (`IAM`, `DATA`, `EXEC`, `OPS`);
cross-cutting scope/governance/evidence requests MAY retain their existing
register prefixes. IDs MUST NOT be recycled or renumbered after publication.

**S2-F02** Each finding MUST record title, affected profile/feature conditions, working
TSC mapping, severity (`high`, `medium`, `low`, `informational`), confidence
(`high`, `medium`, `low`), evidence status, expected/observed property, source
and test evidence, impact, counter-evidence, compensating controls, proposed
action/tradeoffs, suggested accountable role and closure evidence.

**S2-F03** Hypotheses and conditional provider-lifecycle scenarios MUST be labelled.
They MUST NOT be counted as demonstrated deployed failures. Duplicate reports
MUST be linked to a single owning finding while preserving counter-evidence.
Discovery of a design gap does not establish that an incident occurred.

**S2-D01** Decision state MUST be recorded independently of evidence status using
one of the following values:

| Decision state | Required meaning/evidence |
| --- | --- |
| `pending` | No authorized operator decision supplied |
| `investigate` | Operator chose further bounded investigation; scope/owner recorded |
| `collect-evidence` | Operator assigned the evidence request and owner |
| `remediate` | Operator authorized a defined remediation scope and owner |
| `risk-accepted` | Accountable approver, rationale, scope, compensating controls, expiry and reassessment trigger recorded |
| `exclusion-approved` | Operator/auditor approval and rationale recorded for the scoped exclusion |

**S2-D02** The assessor MUST NOT infer approval from silence, test success, a source
commit, this specification, or an instruction to start an assessment.
Suggested actions/owners remain suggestions until assigned.

**S2-D03** Finding lifecycle MUST be recorded separately as `open`, `in-progress`,
`closed`, or `reopened`. Closure MUST identify the authoritative decision,
closure evidence, verification date and verifying role. A code fix alone
MUST NOT close missing deployment or period evidence. Risk acceptance records
the decision; it MUST NOT erase the gap or convert it to `design-supported`.
Expired acceptance MUST reopen the unresolved risk for operator review.

## 6. Investigation and consolidation lifecycle

**S2-L01** The assessment coordinator MUST apply the following procedure:

1. Freeze the source baseline and record scope/applicability unknowns.
2. Map every server component and assign primary investigation ownership.
3. Apply the assessment skill to implementation, contracts and focused tests.
4. Preserve each report's provenance and execution limitations; compare SHAs
   before combining evidence. Documentation-only drift MAY be reconciled
   explicitly; behavior drift requires revalidation of affected claims.
5. Consolidate received findings into `server/SOC2.md`, deduplicate shared
   concerns, and retain pending or unreviewed surfaces.
6. Append a changelog entry and present prioritized operator decisions.
7. Record authorized decisions and later verification without rewriting the
   original observation. Reassess affected components after material changes.

**S2-L02** A completed technical assessment MUST account for every mapped component,
in-scope criterion family, skipped/unexecuted check, unresolved finding and
operational evidence request. It MAY conclude readiness is undetermined.
No compliance percentage or automatic all-controls-passed conclusion is
permitted.

## 7. Change history and reassessment

**S2-H01** The changelog MUST append UTC-dated events with a stable event ID, change type,
source/evidence baseline, affected specification version, related finding
IDs, actual decision/authorization, and remaining limitations. New events
MUST NOT invent approvals or claim work is complete while reports are pending.
Corrections MUST be new events referencing the original event.

**S2-H02** The coordinator MUST update the register and changelog when a component report arrives, evidence
changes a conclusion, an operator makes a decision, remediation is verified,
an acceptance expires, or scope/specification changes. Material authentication,
authorization, data/retention, audit, execution, dependency, deployment or
provider changes require an affected-scope reassessment entry, not a silent
carry-forward of earlier evidence.

**S2-H03** Documentation validation MUST include link/record consistency and the
repository's required workflow compilation after Markdown changes. Runtime
changes separately require their applicable tests/lint/type checks. Passing
documentation validation MUST NOT be recorded as validation of server controls.

## 8. Daily SelfCare reassessment and bounded remediation

The operator authorized authoring a daily SelfCare worker on 2026-10-21.
This section specifies its required behavior; it MUST NOT be taken as
evidence that the workflow has been merged, installed, enabled or run.
Default read-only assessment remains unchanged outside explicit remediation
authorization.

**S2-W01** The worker MUST reuse the existing SelfCare control-precompute, campaign
authority and bounded daily dispatch convention. It MUST act only for the
existing exact authorized repository and resolved mode. It MUST NOT
self-discover targets, dispatch more work, widen enrollment or promote a
review candidate to live. Cadence history unavailable or ambiguous MUST
fail closed. Changing unrelated campaign scope/mode is not authorized.

**S2-W02** Each eligible daily run MUST reassess current source through the assessment
skill and distinguish confirmed code issues from operator/period evidence
requests. It MUST record current SHA, coverage, outcomes and incomplete
checks. Existing draft/remediation PRs MUST prevent duplicate fixes without
being misrepresented as completed/deployed controls.

**S2-W03** The worker MUST rank actionable issues by severity, confidence and
a deterministic stable tie-break. It MUST propose fixes for at most
**three** safely testable issues, not
three invented issues or three forced changes to evidence-only records.
Incompatible changes or changes outside the declared safe-output/file/resource
boundary MUST be deferred with explicit reasons. Fewer than three is valid.
Missing organizational/deployment evidence MUST remain an evidence request.

**S2-W04** Repository changes MUST use existing declared safe outputs: at most one
bounded draft PR per run, with focused regression evidence and complete
required validation for the touched scope. Agent GitHub tools MUST remain
read-only. The worker MUST NOT modify credentials/authority/rollout or its
own workflow/policy protections through a remediation PR, deploy, merge,
accept risk, approve exclusions, or claim certification. Untrusted repository
content and finding descriptions MUST NOT override these boundaries.

**S2-W05** Daily assessment results MUST remain explicit even when a duplicate-open-PR
guard prevents a new fix PR. Record noop, incomplete, partial or proposed-fix
outcomes honestly. Material new findings/changed evidence MUST update the
register and append the changelog through the authorized output boundary;
never mark a finding closed solely because a patch is proposed.

**S2-W06** Contract tests MUST cover registration/cadence, exact target/mode gating,
bounded selection/ranking, duplicate handling, draft safe-output limits and
failure behavior. Editable workflow source, policy registration, manifests,
dispatch ceilings and generated locks MUST be reviewed together at the exact
workflow SHA. No operational control is effective merely because authoring
or compilation succeeds.

## 9. Security considerations

**S2-SEC01** Assessors and automated workers MUST treat repository content,
findings, issue/PR text, downloaded evidence and external-tool output as
untrusted input. Evidence MUST NOT grant tools, scope, credentials, execution
authority or permission to override reviewed control policy.

**S2-SEC02** A report publisher MUST distinguish source-integrity evidence
from publisher identity, evidence completeness and independent custody.
Hashes, hash chains, test passes and successful delivery acknowledgements
MUST NOT be represented as protection against threats outside their actual
trust boundary. Missing independent anchors, retained history, alert routing
or recovery evidence MUST remain explicit.

**S2-SEC03** An assessor MUST NOT execute exploits, probe production, start
unapproved services, read secrets or change runtime authority merely to
obtain evidence. Separately authorized synthetic/disposable checks MUST
record their isolation, prerequisites and limitations.

## 10. Privacy considerations

**S2-P01** Record publishers MUST apply S2-E05 to reports, changelogs, CI
artifacts, summaries, cache state and PR bodies. Redaction of known token
formats MUST NOT be treated as universal personal-data/content redaction.

**S2-P02** Retention and deletion claims MUST identify affected data classes,
derived copies, independently preserved evidence, backups and applicable
holds. This RFC MUST NOT itself be interpreted as a legal basis, consent,
approved retention schedule, privacy attestation or authority to erase
protected evidence. Privacy-category applicability remains an operator/
auditor decision under S2-S03 and S2-S04.

## 11. Compatibility and change control

**S2-C01** This RFC MUST remain available at the established
`specs/server-soc2-compliance.md` path. RFC numbering and requirement IDs
MUST NOT renumber findings, rewrite historical decisions or invalidate
existing section-8 references and assessment-skill routing.

**S2-C02** Editorial RFC conversion preserves record schema version 1.
Incompatible changes to record or decision semantics MUST increment that
version and document migration/interpretation of existing records. A source
change MUST NOT silently revise evidence collected at an older baseline.

## 12. Conformance and acceptance

A conformance claim under this RFC concerns assessment process requirements,
not SOC 2 compliance. The following checks are REQUIRED:

| Class | Required acceptance evidence |
| --- | --- |
| Assessor | Declared source/profile scope, attributed citations, separate design/test/operation status, bounded commands and explicit skipped/unreviewed coverage |
| Assessment coordinator | Complete component accounting, deduplicated findings, source-drift handling, attributed decisions and evidence-backed closure |
| Record publisher | Stable IDs, linked durable register/changelog, corrections as new events, sensitive-data exclusion and schema compatibility |
| SelfCare worker | Exact authority/cadence gates, deterministic top-three bounds, duplicate-safe daily reporting, draft safe-output protections and recorded incomplete outcomes |

**S2-V01** A claim MUST identify the applicable classes, requirement IDs,
evidence baseline and unverified requirements. Passing a subset of checks
MUST NOT be labelled full conformance. Documentation/contract tests MUST NOT
substitute for actual provider or period evidence.

**S2-V02** Maintainers MUST validate document links, stable requirement/
record IDs, unchanged enum values and referenced workflow contracts when
updating this RFC. Required workflow compilation and affected-scope tests
remain separate gates under S2-H03 and the repository's contributor rules.

## 13. References

### 13.1 Normative references

- [RFC 2119: Key words for use in RFCs to Indicate Requirement Levels](https://www.rfc-editor.org/rfc/rfc2119).
- [RFC 8174: Ambiguity of Uppercase vs Lowercase in RFC 2119 Key Words](https://www.rfc-editor.org/rfc/rfc8174).
- [AICPA 2017 Trust Services Criteria, revised points of focus 2022](https://www.aicpa-cima.com/resources/download/2017-trust-services-criteria-with-revised-points-of-focus-2022);
  applicability/edition subject to S2-A02 and engagement approval.
- [Server security boundary](../server/SECURITY.md).
- [Server operational storage](server-operational-storage.md).
- [Server ingestion](server-ingestion.md).
- [Hosted CLI actions](server-hosted-cli.md).

### 13.2 Informative references

- [RFC-033: Policy-governed webhook gateway](rfc-033-webhook-gateway.md).
- [RFC-034: Local webhook gateway simulation and PostgreSQL conformance](rfc-034-local-webhook-gateway.md).
- [Current assessment register](../server/SOC2.md).
- [Assessment changelog](../server/SOC2-CHANGELOG.md).
- [Assessment skill](../.github/skills/soc2-server-assessment/SKILL.md).

## 14. Open questions and external dependencies

The following remain engagement/operator decisions, not implicit approvals:

- What exact system boundary, service commitments, TSC edition/categories,
  examination date/period, provider carve-outs and complementary user-entity
  controls apply?
- Which roles approve scope, remediation, exceptions, exclusions and closure,
  and what operating-evidence sampling/retention policy do they require?
- Which deployed identity/database/executor/proxy/audit/telemetry/backup
  configurations and exercised failure/recovery paths support those claims?
- Where must blocked daily register/changelog deltas be consolidated and
  examination-period evidence independently preserved?

There are no IANA registrations or wire-protocol changes requested by this RFC.
The normative evidence and decision requirements keep these questions visible;
this informative list does not resolve them.
