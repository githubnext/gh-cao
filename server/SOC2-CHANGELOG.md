# SOC 2 readiness assessment changelog

This append-only history tracks assessment/specification changes and operator
decisions, not product releases or certification.
See the [assessment specification](../specs/server-soc2-compliance.md), the
[current register](SOC2.md), and the
[assessment procedure](../.github/skills/soc2-server-assessment/SKILL.md).
Dates are UTC. Corrections are new events; original observations remain.

## 2026-10-21 - SOC2-LOG-001 - Assessment initiated

- Type: scope and assessment baseline.
- Source baseline: `ef7d9fb65ea952826f9d81a94bd5890d3cfd5634`;
  procedure/map baseline: `e3daa5d7ad4fa537e6432359076b44f4a90be171`.
- Specification version: not yet established at this event; procedure captured
  in the initial skill and assessment map.
- Recorded the Go server component/profile inventory, evidence statuses and
  initial scope/governance/operational evidence requests in `SOC2.md`.
- Launched four read-only IAM, DATA, EXEC and OPS investigation sessions;
  identifiers were persisted in commit `05c152f21`.
- Related requests: SCOPE-01, SCOPE-02, GOV-01, IAM-OP-01, DATA-OP-01,
  OPS-OP-01, REL-OP-01 and BCP-OP-01.
- Authorization: operator requested assessment, a reusable skill, component
  investigations and findings for operator decisions. No remediation, risk
  acceptance or exclusions approved.
- Limitations: component reports pending; no deployed configuration,
  organizational evidence or operating-period evidence supplied. Readiness
  undetermined.

## 2026-10-21 - SOC2-LOG-002 - Maintained specification established

- Type: assessment specification and maintenance process.
- Source/evidence baseline: unchanged runtime revision
  `ef7d9fb65ea952826f9d81a94bd5890d3cfd5634`; investigations continue against
  `e3daa5d7ad4fa537e6432359076b44f4a90be171`.
- Specification version: 1, `specs/server-soc2-compliance.md`.
- Established maintained record ownership, criterion-family coverage,
  evidence/test/operating-status separation, stable finding IDs, operator
  decision and closure semantics, append-only history and reassessment
  triggers. Aligned the skill and current register with the specification.
- Related requests: all initial evidence requests remain pending.
- Authorization: operator requested maintaining a SOC 2 compliance
  specification and changelogs. This authorizes documentation maintenance,
  not runtime or policy changes.
- Limitations: preliminary cross-component observations are still being
  investigated; no consolidated finding or operator disposition is implied
  by this event. Readiness remains undetermined.

## 2026-10-21 - SOC2-LOG-003 - OPS investigation consolidated

- Type: component findings and operational evidence requests.
- Source/evidence baseline: assessed
  `e3daa5d7ad4fa537e6432359076b44f4a90be171`, runtime
  `ef7d9fb65ea952826f9d81a94bd5890d3cfd5634`; later coordinator changes
  are assessment documentation only.
- Specification version: 1.
- Received OPS report from session
  `76a730cd-672d-4c67-9e03-cb2c3c0460fe`; consolidated material source
  evidence, counter-evidence, verification limitations, suggested owners and
  closure decisions into `SOC2.md`.
- Findings: OPS-01 through OPS-04 are four medium-severity, high-confidence
  scoped design gaps (successful rebuild attribution, Azure template-only
  OTLP route, production use of development source-built images without an
  equivalent assurance gate, and retired-backend doctor false assurance).
- Evidence requests: OPS-05 through OPS-08 concern independent audit
  operation, recovery commitments/exercises, effective cloud/key boundaries,
  and release/change/scan-exception period evidence. High severity for
  OPS-05/OPS-06 is conditional on the corresponding control commitments.
- Authorization/decisions: all findings open; all operator decisions pending.
  No remediation, risk acceptance, exclusion or production change approved.
- Limitations: focused synthetic/source/contract tests support bounded
  design observations only. Real destinations/providers, several
  PostgreSQL paths, scanning, generated-artifact reproducibility, actual
  deployment, restore and historical operation remain unverified.
  IAM/DATA/EXEC reports pending; readiness undetermined.

## 2026-10-21 - SOC2-LOG-004 - IAM and EXEC investigations consolidated

- Type: component findings, reproducible observations and evidence requests.
- Source/evidence baseline: both reports assessed exact
  `e3daa5d7ad4fa537e6432359076b44f4a90be171`; runtime
  `ef7d9fb65ea952826f9d81a94bd5890d3cfd5634` unchanged.
- Specification version: 1.
- Received IAM and EXEC reports from the registered sessions; consolidated
  findings, source/counter-evidence, suggested choices/owners and closure
  requirements into `SOC2.md`.
- Findings: IAM-001 (medium) unknown-expiry credential cleanup abandonment;
  IAM-002 (low) post-revocation revision-stream metadata; EXEC-01 (medium)
  login-only queued requester identity, with conditional provider-lifecycle
  impact. Three synthetic IAM observations reproduced adverse current
  behavior with and without race detection; no fix was applied.
- Evidence requests: IAM-003 through IAM-006 and EXEC-02 through EXEC-08
  remain open. High priorities concern effective audience/platform isolation
  and enabled production executor isolation/secret separation, not proven
  deployed defects.
- Deduplication: immutable delayed-job identity belongs to EXEC-01; IAM
  callback/consent binding and active rename denial are counter-evidence,
  not closure. Storage and cloud requests retain DATA/OPS dependencies.
- Authorization/decisions: all new records open, all operator decisions
  pending. No remediation, risk acceptance, exclusion or feature activation.
- Limitations: synthetic/platform-limited tests only; real adapters,
  intended executor kernels, provider lifecycle, deployed settings and
  historical operation remain unverified. IAM selectors overlap; test
  counts are not unique controls. DATA report pending; readiness undetermined.

## 2026-10-21 - SOC2-LOG-005 - DATA report and initial technical pass consolidated

- Type: component findings, failed validation and assessment milestone.
- Source/evidence baseline: DATA assessed exact
  `e3daa5d7ad4fa537e6432359076b44f4a90be171`; runtime
  `ef7d9fb65ea952826f9d81a94bd5890d3cfd5634` unchanged.
- Specification version: 1.
- Received DATA report from the registered session and consolidated all
  findings, citations, counter-evidence, closure requirements and limitations.
- Findings: DATA-01 high-severity cross-repository shard-prefix erasure;
  DATA-02 medium shared operational-value retention after withdrawal;
  DATA-03 medium erasure ACK before durable projection intent. Artifact-only
  production-helper observations reproduced all three; no production loss,
  canonical reappearance or real crash-loss was asserted.
- Evidence/applicability: DATA-04 through DATA-08 and DATA-10 need evidence;
  DATA-09 proposes a profile exclusion, not approved. DATA-08 records the
  existing failing temporal campaign SQL contract test; the main selected
  package run exited 1 and is not a passing validation gate.
- Milestone: all four bounded technical reports consolidated. Register has
  10 scoped design gaps (one high, eight medium, one low), 21 evidence-needed
  workstream records and one proposed exclusion, plus initial shared
  requests. Linked/shared dependencies are not independent criterion counts.
- Authorization/decisions: every finding/request remains open with operator
  decision pending. No fixes, production access, exclusions, feature
  activation or risk acceptance authorized.
- Limitations: real databases/providers/executor kernels, deployed settings,
  organizational evidence and historical operation remain unverified.
  Initial technical assessment delivered; SOC 2 readiness undetermined.

## 2026-10-21 - SOC2-LOG-006 - Remediation and daily worker authoring authorized

- Type: explicit operator decision and coordinated implementation launch.
- Source/evidence baseline: remediation sessions start from
  `748093b701ef9cf22e0323ac84fc6b026ea526ac`, containing the consolidated
  assessment. Original runtime observations remain tied to
  `ef7d9fb65ea952826f9d81a94bd5890d3cfd5634`.
- Specification version: 1; added the daily SelfCare worker contract without
  changing evidence/decision record semantics.
- Authorization: operator directed "start session to fix each findings" and
  "create an self care agentic workfler worker that does this check again
  and fixes the top 3 issues daily".
- Decisions: DATA-01/02/03, IAM-001/002, EXEC-01 and OPS-01/02/03/04 now
  `remediate` / `in-progress`; DATA-08 is authorized for contract/root-cause
  investigation and remediation under the same disposition.
- Launched eleven dedicated repository fix sessions and a twelfth session to
  author/register the daily SelfCare assessment/top-three-remediation worker.
  Exact session IDs are maintained in `SOC2.md`.
- Limits: draft PR proposals only, no merge/deployment/production access or
  secret reads. Existing sole-target authority must not widen. Evidence-only
  findings remain open/pending, except DATA-08's concrete failed check;
  proposed exclusions remain unapproved. No risk acceptance inferred.
- Outcome: repository work underway; no fix verified, no finding closed.
  Daily workflow is not active until reviewed/merged/installed. SOC 2
  readiness remains undetermined.

## 2026-10-21 - SOC2-LOG-007 - OPS-04 validated draft remediation received

- Type: proposed code remediation and validation evidence.
- Source/evidence: commit `c480c3027785444a53e814802bf9776bb9f02fbc`,
  draft [#19304](https://github.com/githubnext/gh-cao/pull/19304), targeting
  the assessment branch. Coordinator checked GitHub open/draft/head/base
  identity and commit scope; it did not rerun the session's test suite.
- Specification version: 1. Related finding: OPS-04.
- Proposed change: missing/malformed retired-backend persistence evidence warns;
  unknown AOF status remains unknown; historical RDB success does not assert
  active snapshot policy; enabled-AOF write success is narrowly described.
  Added classification/INFO boundary tests and operator documentation.
- Reported validation: formatting, doctor normal/race tests, vet/build,
  full server golangci-lint (zero issues), workflow compilation and diff
  checks passed.
- Decision/lifecycle: `remediate` / `in-progress`; draft awaiting review,
  not merged/deployed and not closed. Original assessment evidence preserved.
- Limitations: no live CONFIG/provider access or restore exercise.
  DATA-07/OPS-06 provider durability/recovery evidence remains pending.

## 2026-10-21 - SOC2-LOG-008 - DATA-08 contract correction proposed

- Type: validation root-cause disposition and draft test-only remediation.
- Source/evidence: commit `d2d640bd3301617d27919e6634ef63c2b50f321f`,
  draft [#19305](https://github.com/githubnext/gh-cao/pull/19305).
  Coordinator checked open/draft/head/base and one-file scope, not rerun
  results independently.
- Specification version: 1. Related finding: DATA-08.
- Root cause: legacy Operational Value pages were intentionally retired;
  the temporal SQL test still searched the removed page instead of the
  shared native-measure fixture. No runtime processing defect inferred.
- Proposed change: correct fixture/test naming, preserve existing semantic
  assertions and strengthen source/metric/point/dimension/row-bound checks.
- Reported validation: original failure reproduced; corrected targeted
  check and PostgreSQL-backed full query suite passed (214 passing
  test/subtest events, zero failures/skips); 74 related dashboard contract
  tests, query vet/build/lint, formatting, workflow compile and diff checks
  passed. Disposable database removed; no deployed service examined.
- Decision/lifecycle: `remediate` / `in-progress`; review/integration and
  post-integration gate evidence pending. Historical baseline failure is
  preserved and no other provider/period evidence is closed.

## 2026-10-21 - SOC2-LOG-009 - Review-state refresh and DATA-08 CI dependency

- Type: current review state and integration limitation.
- Source/evidence: GitHub refresh around 15:39 UTC reports
  [#19304](https://github.com/githubnext/gh-cao/pull/19304) and
  [#19305](https://github.com/githubnext/gh-cao/pull/19305) open/non-draft;
  original proposal head commits remain unchanged. Initial draft-creation
  events remain accurate history; no review-state changes were made here.
- Specification version: 1. Related findings: OPS-04 and DATA-08.
- OPS-04 Go CI has one failing completed check. Its session's log review
  and query/fixture baseline tree comparison attribute failure to the
  pre-existing DATA-08 temporal assertion, not the doctor change.
  The separate DATA-08 correction is proposed in #19305.
- Decision/lifecycle: both remain `remediate` / `in-progress`.
  No cross-PR code copy, merge, CI rerun or closure authorized/performed.
  Integration must resolve the known dependency and produce clean relevant
  gate evidence before closure; provider/period limitations remain.

## 2026-10-21 - SOC2-LOG-010 - Two corrections integrated on assessment branch

- Type: observed remote integration and coordinator verification.
- Source/evidence: GitHub reports OPS-04
  [#19304](https://github.com/githubnext/gh-cao/pull/19304) merged at
  15:40:18 UTC as `90b865397`, DATA-08
  [#19305](https://github.com/githubnext/gh-cao/pull/19305) merged at
  15:40:41 UTC as `c85b8dfe7`, both into the assessment branch.
  This supersedes SOC2-LOG-009's earlier open-state observation.
- Specification version: 1. Related findings: OPS-04 and DATA-08.
- Coordinator's push detected remote advancement; it preserved both
  externally merged fixes and rebased its unpublished documentation commit.
  No remote merge, force push or PR-state change was performed by coordinator.
- Integrated check:
  `go -C server test ./internal/doctor ./internal/query -count=1 -json`
  passed with 311 passing test/subtest events, 18 PostgreSQL-dependent
  skipped cases and zero failures. Native-measure contract and doctor cases
  passed; this is not real-provider or full-repository validation.
- Decision/lifecycle: `remediate` / `in-progress` pending parent/default-branch
  integration and final required gates; no production/period control closed.
  Original design/failed-gate observations and skip limitations preserved.

## 2026-10-21 - SOC2-LOG-011 - Daily SelfCare worker authored and proposed

- Type: recurring workflow authoring/contract-validation milestone.
- Source/evidence: commit `c132cc6d01809d2b99fb86e36dd56430355a44ef`,
  draft [#19310](https://github.com/githubnext/gh-cao/pull/19310).
  Coordinator verified GitHub draft/head/base, changed-file scope and policy
  registration diff; authoring-session validation is separately attributed.
- Specification version: 1, section 8.
- Proposal: `self-care-server-soc2`, rolling 24-hour orchestrator eligibility
  and manual duplicate-day guard; exact existing target/live authority only.
  Worker registered across inventory/manifests/policy/dispatch; ceiling
  20 to 21 workers, no target/mode/credit-ceiling widening.
- Behavior: daily assessment/report even with an existing fix PR; at most
  three ranked current verified compatible code issues in one bounded draft
  PR; open/addressed remediation exclusion, no fabricated operational evidence.
  One-package/12-file/50-KiB/path limits protect workflow/policy/credentials.
  Blocked durable record deltas require explicit human consolidation.
- Reported validation: 303 workflow/policy and 258 control integration
  tests passed without skips; lint/both type checks passed; 73 workflows
  compiled and locks regenerated with fixed seed and inspected.
- Activation: pending review/default-control-branch integration, prerequisites,
  credentials and Actions configuration. No run/merge/deployment performed
  by authoring session or coordinator. Artifact retention is not proof of
  examination-period retention or effective controls.
- Decisions/findings: no risk accepted, exclusion approved or prior finding
  closed. SOC 2 readiness remains undetermined.

## 2026-10-21 - SOC2-LOG-012 - Stream and development-assurance proposals received

- Type: two bounded draft remediation proposals and validation handoffs.
- Source/evidence: IAM-002 commit
  `6839329d8c442f58f5ab552ddda792c104bc2c59`,
  draft [#19318](https://github.com/githubnext/gh-cao/pull/19318);
  OPS-03 commit `bb897f7e76932704aa5c22640257abfaa940237b`,
  draft [#19317](https://github.com/githubnext/gh-cao/pull/19317).
  Coordinator verified open/draft identities and scopes; session validation
  remains separately attributed.
- Specification version: 1.
- IAM-002 proposal: fixed OAuth stream authorization deadlines and bounded
  heartbeat session reads; existing helpers handle reconnect. Reported
  real-boundary/failure tests and repeated race checks pass; full server
  result retains database-dependent skips. Proxy/buffer/backend operation
  evidence remains required.
- OPS-03 proposal: reviewed config and plan explicitly restrict source-build
  apply to development; separate target digests/read-back and honest
  assurance receipts added. No production scanner/promotion pipeline
  claimed. Reported native check/race, Bicep/assets/package contracts,
  lint/type/compile/docs checks pass with mocked Azure calls.
- Integration dependencies: preserve IAM-001 expired-read semantics with
  stream checks; preserve separate image parameters when combining OPS-02
  telemetry templates; regenerate combined ARM.
- Decision/lifecycle: both `remediate` / `in-progress`, unmerged and
  undeployed. No production assurance/effectiveness or prior finding closed.

## 2026-10-21 - SOC2-LOG-013 - Durable erasure follow-up proposal received

- Type: DATA-03 draft remediation and durable-backend local verification.
- Source/evidence: commit `79830714dd4b40453b0c15eefa1bf6569c475af7`,
  draft [#19322](https://github.com/githubnext/gh-cao/pull/19322).
  Coordinator verified open/draft/head/base and five-file scope.
- Specification version: 1.
- Proposal: one erasure helper persists follow-up before ACK; separate
  requested/completed generations consume only captured intent after
  canonical commit, retaining concurrent and interrupted work.
- Session-reported validation: full collector race suite with isolated then-supported operational backends
  and PostgreSQL, 260 passing test/subtest events and zero failures/skips;
  failure/cancel/reclaim/rollback/crash-equivalent/concurrent-publication
  cases covered. Loaded-host timeout in initial existing parity test was
  resolved by unchanged bounded-concurrency rerun, not weakened assertions.
  Server lint/format/build, collector vet and workflow compile passed.
- Integration: preserve DATA-01/02 cancellation-aware ForgetContext in the
  new single helper; no duplicate identity/shared-evidence scope.
- Decision/lifecycle: `remediate` / `in-progress`; proposal unmerged,
  undeployed, no finding closed. Provider power-loss, actual recovery,
  principal/encryption and period evidence remain separately unverified.

## 2026-10-21 - SOC2-LOG-014 - Daily worker integrated on assessment branch

- Type: observed integration and coordinator contract verification.
- Source/evidence: GitHub reports
  [#19310](https://github.com/githubnext/gh-cao/pull/19310) merged at
  15:50:10 UTC as `4b63f3dcb6478dbc42f948d262131b2b32593a6e`.
  Coordinator preserved the remote merge and rebased unpublished docs.
- Specification version: 1, section 8.
- Integrated check: `node --test tests/unit/workflow-contract-selfcare-soc2.test.mjs`,
  seven passed, zero failures/skips. Initial missing locked `yaml` dependency
  was restored with `npm ci --ignore-scripts --no-audit --no-fund`, then rerun;
  no manifest/lock change.
- Activation: still pending default/active-control-branch integration and
  actual prerequisites; no dispatch/run/deploy or effectiveness claim.
  This supersedes only SOC2-LOG-011's unmerged-proposal state.

## 2026-10-21 - SOC2-LOG-015 - Four further remediation proposals recorded

- Type: DATA-01/02 and OPS-01/02 draft implementation/validation handoffs.
- Source/evidence: DATA-01 `03c5fdb5c89a9b45752151ee4094cb878fb4520a`
  [#19325](https://github.com/githubnext/gh-cao/pull/19325);
  DATA-02 `ca720d64091b12efb6408489b8c3e3f4f6c3c010`
  [#19326](https://github.com/githubnext/gh-cao/pull/19326);
  OPS-01 `0f0a42612a1b3cf9e561d37b31be92cd324ae375`
  [#19323](https://github.com/githubnext/gh-cao/pull/19323);
  OPS-02 `2296bdd84ca101d2a9632819020bc502dad87826`
  [#19324](https://github.com/githubnext/gh-cao/pull/19324).
  Coordinator verified each open/draft/head/base and scoped files.
- Specification version: 1.
- DATA-01: prefix-free versioned repository encoding, identity-verified
  legacy migration/preflight erasure and mixed-identity rejection; ambiguous
  data left intact. Reported Activity and then-supported backend collector/ingest/
  simulator race suites pass without skips; rollout inventory still required.
- DATA-02: shared/derived collector-value erasure, cross-process lake lock,
  zero-enrollment pruning and revision-keyed caches. Reported end-to-end
  webhook/Activity/PostgreSQL withdrawal and scoped race/cache cases pass;
  protected independent packets/holds remain an operator scope decision.
- OPS-01: durable rebuild lifecycle attribution and explicit storage failures;
  hosted rebuild without durable audit now refuses, API 2.10.0. Reported
  role/reader-chain and outage tests pass; old baseline full-suite failure
  DATA-08 separately corrected in parent. Cross-store/crash evidence is not
  atomic; no outbox or missing-terminal reconstruction claimed.
- OPS-02: reviewed optional HTTPS OTLP/log route, Key Vault-only headers and
  secret-scoped grants; synthetic TLS receipt/failure/config/template tests
  reportedly pass. Actual deployment/collector/alert operation remains unknown.
- Integration dependencies: combine ForgetContext preflight/filter/lock with
  DATA-03's single erasure helper/generations; preserve OPS-03 digest and
  development-only apply controls when regenerating OPS-02/03 ARM.
- Decision/lifecycle: all `remediate` / `in-progress`, unmerged/undeployed;
  validation is session-reported, not whole-system or historical assurance.

## 2026-10-21 - SOC2-LOG-016 - Assessment specification converted to RFC form

- Type: user-directed specification format and conformance clarification.
- Authorization: operator directed "sco2 spec should be written in RFC mode".
- Specification: RFC-035 at the existing
  `specs/server-soc2-compliance.md` path; record schema version remains 1.
- Added RFC status/scope/safety metadata, abstract, BCP 14 / RFC 2119 and
  RFC 8174 keyword interpretation, role-specific conformance classes,
  terminology, stable `S2-` requirement identifiers, security/privacy and
  compatibility considerations, acceptance criteria, references and open
  dependencies.
- Preserved existing evidence/decision/lifecycle enums, finding identifiers,
  section-8 routing and historical observations/authorizations. Updated
  register and skill references to the RFC; no runtime/workflow/policy or
  deployment authority change.
- Limitations: RFC editorial/process conformance is not certification,
  engagement approval, deployed control evidence or finding closure.

## 2026-10-21 - SOC2-LOG-017 - OAuth cleanup lifecycle proposal received

- Type: IAM-001 draft remediation and real-adapter verification handoff.
- Source/evidence: commit `6926c313c9c059af4a8e4f9ebb06d26dbeac0df8`,
  draft [#19327](https://github.com/githubnext/gh-cao/pull/19327).
  Coordinator verified open/draft/head/base and 22-file scope.
- Specification: RFC-035, record schema version 1.
- Proposal: explicit provider expiry, CAS cleanup handoff, inactive-session
  ownership retention/atomic expiry staging across adapters, protected
  transition capacity and bounded temporary cleanup ownership.
  Expired records still cannot authenticate or CAS back into authority.
- Session-reported evidence: affected server/operational/backend suites with
  isolated real then-supported backends and roles, serialized/race lifecycle checks,
  focused 48 pass/zero fail/zero skip, build/lint/format/file-size/compile.
  Earlier environment concurrency/address failures are preserved and resolved.
- Rollout limits: stop old retired-backend-envelope replicas, retain prefixes/data/keys,
  preserve unknown-expiry legacy debt. Pre-existing abandoned grants need
  separate reconciliation. Provider issuance-to-durable-write kill window
  and volatile-memory restart loss remain explicit, not universal cleanup.
- Decision/lifecycle: `remediate` / `in-progress`, unmerged/undeployed.
  No grants reconciled, live provider requests or finding closure.

## 2026-10-21 - SOC2-LOG-018 - Immutable queued requester proposal received

- Type: EXEC-01 draft remediation and final workstream handoff.
- Source/evidence: commit `6ecfccfe666e607887e20c9b94333fb234997e94`,
  draft [#19328](https://github.com/githubnext/gh-cao/pull/19328).
  Coordinator verified open/draft/head/base and 21-file scope.
- Specification: RFC-035, record schema version 1.
- Proposal: numeric ActorID survives admission/queue/idempotency/claims and
  is verified in the permission response before credential use. Legacy
  unknown-ID work remains readable but denies execution and needs explicit
  fresh admission; no backfill/deletion or SQL migration.
- Session-reported validation: serial affected suites, real PostgreSQL/
  SQLite identity/operational cases, focused race, build/vet/lint,
  29 TypeSpec checks/no generated drift, workflow compile and three bounded
  TLC models. Concurrent native fixture failure passed serial rerun.
- Rollout: coordinated web/executor upgrade with old executor reconciliation;
  runtime contract 1.1.0, mixed old replicas unsafe. No production migration,
  provider behavior proof or deployment occurred.
- Decision/lifecycle: `remediate` / `in-progress`; no finding closed.

## 2026-10-21 - SOC2-LOG-019 - All fix proposals delivered; review states refreshed

- Type: coordination milestone and observed review/integration state.
- Specification: RFC-035, record schema version 1.
- All eleven finding-specific fix sessions have delivered reviewable PRs.
  Eight remain open; three corrections and the daily worker are integrated
  into the assessment branch. This is not default-branch/deployment closure.
- GitHub reports IAM-002
  [#19318](https://github.com/githubnext/gh-cao/pull/19318) merged at
  15:52:18 UTC as `24db44b03`; this remote merge was preserved by the
  execution host during the coordinator's documentation work.
- Current non-draft open proposals: DATA-01/02/03 and OPS-01/02.
  IAM-001, EXEC-01 and OPS-03 remain draft. Earlier draft observations remain
  historical; coordinator did not override changed review state.
- Decisions/lifecycles remain as recorded; no external evidence, exclusion,
  risk acceptance or operating-effectiveness closure inferred.

## 2026-10-21 - SOC2-LOG-020 - Durable erasure correction integrated remotely

- Type: observed assessment-branch integration.
- Source/evidence: remote commit `c5625fcf5` integrates DATA-03
  [#19322](https://github.com/githubnext/gh-cao/pull/19322) while the
  coordinator was publishing documentation. Remote code was preserved and
  only the unpublished documentation commit rebased; no force push or
  coordinator-initiated PR merge.
- Specification: RFC-035, record schema version 1.
- This supersedes the immediately preceding snapshot's open DATA-03 state.
  Source correction is integrated; combined DATA-01/02 and final required
  validation/default-branch/deployment evidence remain pending.
- Decision/lifecycle: `remediate` / `in-progress`; no operating control closed.

## 2026-10-21 - SOC2-LOG-021 - Integrated-base shard proposal and two remote merges

- Type: integration follow-up, review refresh and observed source integration.
- Source/evidence: DATA-01 [#19325](https://github.com/githubnext/gh-cao/pull/19325)
  head `dfdd11f3cb8dad7fce79e43602446ccce7ba8a6b`, assessment base
  `a643b5f17`; coordinator verified head/open/non-draft and in-progress CI.
- Specification: RFC-035, record schema version 1.
- DATA-01 preserves DATA-03's single helper/durable generations, uses
  ForgetContext and validates normalized repository identity before erasure.
  Session reports 271 real then-supported backend collector race tests with zero
  skips/failures, lint/type/build and 73-workflow compilation; CI still pending.
- Remote integration: OPS-02 [#19324](https://github.com/githubnext/gh-cao/pull/19324)
  merged at 15:59:15 UTC as `a643b5f17`; IAM-001
  [#19327](https://github.com/githubnext/gh-cao/pull/19327) at 15:59:48 UTC
  as `775d5ee7e`. Coordinator fast-forwarded externally merged code only.
- Current snapshot: six of eleven source corrections and daily worker
  integrated on assessment branch; DATA-01/02, EXEC-01 and OPS-01/03 remain
  open, with EXEC-01 now non-draft. Earlier status snapshots are history.
- Decision/lifecycle: remediation remains in progress. No deployed identity,
  collector/alert, legacy migration, key/debt reconciliation, provider/period
  control or main/control-branch activation closed.

## 2026-10-22 - SOC2-LOG-022 - Current-source readiness reassessment

- Type: user-requested read-only reassessment and prioritized remaining work.
- Source baseline: `53458a46d983356cdc2b8c5f27903026ca707a82`;
  compared with original investigation
  `e3daa5d7ad4fa537e6432359076b44f4a90be171` and runtime
  `ef7d9fb65ea952826f9d81a94bd5890d3cfd5634`.
- Specification: RFC-035, record schema version 1.
- Current implementation includes all ten original technical corrections
  and DATA-08's maintained fixture. The register's new leading section
  records narrowly scoped design-supported claims and supersedes older
  unintegrated-proposal descriptions only for current-source presence.
  Original observations, approvals and lifecycle history are preserved.
- Evidence: five bounded Go invocations exited 0: component 3,149 pass /
  250 skip, HTTP 332 pass / 19 skip, mocked deployment 44 pass / zero skip,
  data support 447 pass / 56 skip, shared query 1,125 pass / zero skip
  test/subtest events. Counts are not unique controls or a compliance score.
  Raw JSONL evidence is in the assessment session artifact directory
  referenced from the current register.
- Coverage: component inventory, CC1-CC9 evidence obligations, separate
  optional profiles and A1/PI1/C1/P1-P8 applicability accounted for.
  Prioritized scope/governance, rollout/principal/key, independent audit,
  restore, executor, retention, release and period-evidence work.
- Authorization: assessment/report maintenance only. No runtime, rollout,
  retention, credential, permission or deployment change; no issue, PR,
  remediation session, provider probe or service startup.
- Decisions/lifecycle: existing remediate/in-progress records retained;
  evidence-only and applicability decisions remain pending. No risk
  acceptance, exclusion, finding closure or operating-effectiveness claim.
- Limitations: real durable backends, intended executor platforms, external
  audit receiver/upstream runtimes, production configuration, scanning and
  organizational/period evidence remain unverified. No new demonstrated
  runtime design gap established; readiness remains undetermined.

## 2026-10-22 - SOC2-LOG-023 - Follow-up hardening and metadata proposal

- Type: operator-directed bounded hardening and received component report.
- Authorization: operator directed "start separate sessions to apply
  improvements". Five independent sessions own metadata byte bounds,
  PostgreSQL budgets, missing-terminal audit detection, release evidence
  collection and key custody/rotation. No merge or production authority.
- Specification: RFC-035, record schema version 1.
- Received DATA-10 metadata report at commit
  `7f22e61b5f81b8891b2be01023b486d25cc73cf2`, base
  `4beb309dd0e415e7bb82e156cdc463d6c21fb55f`.
  Coordinator verified [#19422](https://github.com/githubnext/gh-cao/pull/19422)
  open/draft/head/base. Target files were reported unchanged from assessment
  `53458a46d983356cdc2b8c5f27903026ca707a82` before the patch.
- Proposal: predecode manifest 8 MiB and generic marketplace JSON 16 MiB
  bounds, complete-document validation and explicit failure semantics.
  Narrow claims are design-supported at the proposal SHA, not integrated
  on the assessment branch or demonstrated deployed.
- Session-reported verification: 215 focused race test/subtest passes,
  zero skips/failures; selected consumers six passes/six PostgreSQL skips;
  scoped lint/vet/build, whitespace and 73-workflow compilation passed.
  Coordinator did not rerun runtime validation.
- Decision/lifecycle: DATA-10 authorized slices remediate/in-progress;
  broader evidence-needed status and outstanding closure requests retained.
  Other four final reports remain pending.
- Limitations: intentional larger-input rejection needs workload review;
  decoded heap, concurrency/physical DB/temp budgets, file I/O deadlines,
  deployment and historical operation remain unverified. No finding closed,
  risk accepted, exclusion approved or compliance assertion made.

## 2026-10-22 - SOC2-LOG-024 - Release evidence collector proposal received

- Type: received bounded hardening report; RFC-035 record schema version 1.
- Source: proposal `fde438115bfc8bfec9ea2a5db81d6704baa72659`, base
  `4beb309dd0e415e7bb82e156cdc463d6c21fb55f`, descendant of assessed
  `53458a46d983356cdc2b8c5f27903026ca707a82`.
  Coordinator verified [#19423](https://github.com/githubnext/gh-cao/pull/19423)
  open/draft/head/base identity.
- Related requests: REL-OP-01 / OPS-08. Owner-invoked read-only collection of
  bounded run/artifact/approval references, optional exact-identity
  attestation checks and sanitized existing Azure development receipts.
  Versioned reports explicitly retain incomplete evidence boundaries.
- Session-reported validation: 73 synthetic tests passed without skips;
  lint, CAO typecheck, whitespace and 73-workflow compilation passed.
  No real registry/API/deployment verification; coordinator did not rerun tests.
- Decision/lifecycle: collector implementation authorized by the follow-up
  directive, remediate/in-progress; broader evidence-needed records remain
  open. Three other final implementation reports remain pending.
- Limits: no independent original-evidence archive, actual running-image
  measurement, approval independence, enforced governance, scan-exception
  disposition or period proof. No runtime/control closure, deployment,
  permissions, retention, risk acceptance or compliance assertion.

## 2026-10-22 - SOC2-LOG-025 - Offline App key preflight proposal received

- Type: bounded hardening report; RFC-035 record schema version 1.
- Proposal source: `eb1d079ad24c62402692b9bfea2263c831e96b15`, base
  `4beb309dd0e415e7bb82e156cdc463d6c21fb55f`. Coordinator verified
  [#19424](https://github.com/githubnext/gh-cao/pull/19424) open/draft/head/base.
- Related requests: EXEC-05 / OPS-07 and IAM-006 key lifecycle evidence.
  Authorized fallback implements bounded offline RSA key consistency,
  public-fingerprint pinning and changed-key preflight. Runtime signing
  and deployed custody are unchanged.
- Managed signing remains unimplemented: provider/import/version/RS256,
  identity/grant/deployment and context-bounded no-fallback integration
  need explicit design decisions; a dependency signer seam is insufficient.
- Session-reported validation: 1,317 race test/subtest passes, 12 platform/
  database/deployed-data skips; scoped lint zero issues, fmt/vet/focused race
  and 73-workflow compilation passed. No real GitHub/KMS/production test;
  coordinator did not rerun runtime tests.
- Disposition: narrow preflight design-supported at proposal SHA; source
  remediation slice in-progress, not merged/deployed. Broader custody,
  retirement/token-cleanup and period evidence remains needed/open;
  managed-signing decisions pending. Two other final reports remain pending.
- No risk acceptance, exclusion, finding closure, provider/key/permission
  action or operating-effectiveness/compliance assertion.

## 2026-10-22 - SOC2-LOG-026 - PostgreSQL read-budget proposal received

- Type: bounded DATA-10 hardening report; RFC-035 record schema version 1.
- Proposal `95d5da40f14c75b8c12d0436419e9e2980b0afd7`, base
  `4beb309dd0e415e7bb82e156cdc463d6c21fb55f`; coordinator verified
  [#19425](https://github.com/githubnext/gh-cao/pull/19425) open/draft/head/base.
- Narrow source support: whole canonical-read deadline begins before pool
  wait; transaction-local timeouts preserve stricter operator ceilings.
  No new memory/disk/worker policy or deployed settings.
- Session-reported owned-local PostgreSQL 18.6 evidence: full store suite
  passed in 587.663s; focused race suites passed without skips. Measured
  statement/pool/caller cancellation, production statistics lock rejection,
  synthetic physical sort spill and inherited temp-file-limit rejection.
  Query/sqlbuilder/command tests, vet/build, full server lint and seeded
  73-workflow compilation passed. Cluster stopped and removed.
- Coordinator did not rerun provider tests. Deployed-data benchmark absent;
  local measurements do not establish PostgreSQL 16/managed-provider/fleet
  capacity, total physical cost or historical operation.
- Decision/lifecycle: source slice remediate/in-progress, not merged/deployed;
  broader DATA-10 evidence-needed/open. One final audit-detection report
  remains pending. No control closure, risk acceptance or deployment action.

## 2026-10-22 - SOC2-LOG-027 - Audit detector and revised metadata proposal

- Type: final follow-up report and user-directed proposal revision;
  RFC-035 record schema version 1.
- Audit detector source: `8346491ecaa735bc902b327d41f601124a066bad`,
  base `4beb309dd0e415e7bb82e156cdc463d6c21fb55f`;
  [#19426](https://github.com/githubnext/gh-cao/pull/19426) open/draft/head/base
  verified. Bounded read-only lifecycle scanning preserves capped/suffix/gap/
  duplicate uncertainty and does not repair history or replay outcomes.
- Session-reported detector race events: 34 pass; CLI 276 pass/three
  PostgreSQL skips; focused independent PostgreSQL history also skipped.
  Audit/SQLite, vet, full-server lint and 73-workflow compile passed.
  OPS-01/OPS-05 operating/independent-custody evidence remains unresolved.
- Metadata revision: verified draft
  [#19422](https://github.com/githubnext/gh-cao/pull/19422) head
  `51a569977074f87c7191c80621817424f060e2bf`. User-directed smaller,
  centralized configurable budgets supersede SOC2-LOG-023's initial
  8 MiB/16 MiB values: manifest 2 MiB, ordinary JSON 512 KiB, recursive
  repository tree 8 MiB. Independent positive-byte environment overrides
  fail explicitly when invalid before relevant I/O; effective deployment
  limits remain unknown.
- Revised metadata session reports 283 race events passing without skips,
  scoped lint/vet/build and final 73-workflow compile passed. Prior six
  PostgreSQL consumer skips were not rerun; original history preserved.
- Coordinator verified proposal identities and consolidated material evidence,
  not runtime test execution. All five follow-up draft proposals delivered;
  combined release validation/integration/deployment not asserted.
- Decisions/limitations: source slices remediate/in-progress, broader requests
  evidence-needed/open; operator closure/risk/exclusion decisions pending.
  No production change, permission widening, independent operating evidence
  or compliance conclusion.

## 2026-10-22 - SOC2-LOG-028 - Offline key preflight parser correction

- Type: follow-up proposal correction; RFC-035 record schema version 1.
- Verified [#19424](https://github.com/githubnext/gh-cao/pull/19424) remains
  open/draft against main at
  `451e6e94e00d6b089e321c1d1c57e711c1fc986e`, superseding
  `eb1d079ad24c62402692b9bfea2263c831e96b15` for current-source citations.
  SOC2-LOG-025 remains the original observation.
- Published diff adds single-BEGIN-marker validation to the offline parser,
  rejecting malformed/unterminated leading same-type PEM blocks that
  `pem.Decode` could skip before accepting a later valid key. Parser and
  command no-success-output regressions accompany the fix.
- Session reports reproduced failures before remediation and passing full
  changed-package race suites, vet, pinned formatter and targeted lint
  (zero issues). Coordinator verified proposal identity and inspected the
  diff; runtime tests were not independently rerun.
- EXEC-05/OPS-07 and related IAM-006 remain evidence-needed/open; no new
  runtime-signing vulnerability or closure is inferred. Runtime signing,
  managed-provider decisions, custody evidence and operating effectiveness
  (not-assessed) are unchanged. No merge, deployment or key lifecycle action.

## 2026-10-22 - SOC2-LOG-029 - PostgreSQL constant and native collector revision

- Type: follow-up source revision and pending user-directed implementation
  correction; RFC-035 record schema version 1.
- Verified draft [#19425](https://github.com/githubnext/gh-cao/pull/19425)
  head `c623141efe611da61117875b2a004319c2b7787d`. Session reports merging
  main `bf11b39b0`; follow-up diff centralizes the unchanged 60-second
  PostgreSQL timeout in inputlimits without a resource-policy change.
- Session reports post-merge inputlimits/postgresx/query tests, scoped lint
  and build passing; database-backed tests skipped. SOC2-LOG-026 physical
  observations remain bound to `95d5da40f14c75b8c12d0436419e9e2980b0afd7`.
  Coordinator inspected the published delta, not a combined database rerun.
- User-directed release collector correction requires native Go
  `gh cao evidence release`, replacing the repository-local JavaScript tool.
  Verified [#19423](https://github.com/githubnext/gh-cao/pull/19423) remains
  open/draft at observed head `4b5caa6483cb861df2988b70d7036fdbb7b7ddd6`;
  final native source/validation handoff pending. Original JavaScript
  citations/results remain historical, not evidence for its replacement.
- DATA-10, REL-OP-01 and OPS-08 remain evidence-needed/open; source slices
  remediate/in-progress. Authority, retention, operator decisions and
  operating effectiveness (not-assessed) are unchanged. No deployment or
  compliance conclusion.

## 2026-10-22 - SOC2-LOG-030 - Native release evidence collector handoff

- Type: final user-directed native implementation revision;
  RFC-035 record schema version 1.
- Verified [#19423](https://github.com/githubnext/gh-cao/pull/19423) open/draft
  at `ef1e3356ca4aff3aff20f2a3e64e69f0c9d5799b`. Native Go
  `gh cao evidence release` replaces the JavaScript collector and tests,
  reuses existing Azure receipt types and shared bounded gh execution, and
  adds no Node/dashboard-server dependency. This supersedes SOC2-LOG-029's
  pending native handoff; original JavaScript evidence remains historical.
- Current source citations, exact race command, log and exclusions are in
  the maintained register. Session reports 1,322 passing race test/subtest
  events and one published-runtime integration skip; root lint/typecheck,
  Go lint, compile (73), docs build and standalone build/help passed.
  Coordinator verified identity/change surfaces, not runtime execution.
- REL-OP-01/OPS-08 remain evidence-needed/open. Native collection is narrowly
  design-supported; incomplete reports do not establish independent approval,
  original-bundle retention, running-image identity or operating effectiveness.
  Operator destination ACL enforcement remains required; 0600 is
  platform-dependent. Actual registry/API verification, deployment,
  retention/rollback/governance and period evidence remain unverified.
  No merge, production/authority change or finding closure.

## 2026-10-22 - SOC2-LOG-031 - Native collector usefulness review

- Type: user-requested component usefulness review;
  RFC-035 record schema version 1.
- Same source `ef1e3356ca4aff3aff20f2a3e64e69f0c9d5799b`; coordinator
  verified [#19423](https://github.com/githubnext/gh-cao/pull/19423)
  open/ready and not merged. Earlier draft observations remain historical.
- Review concludes useful bounded evidence index/exact-identity checkpoint,
  not a self-contained independently retained CC8 audit packet.
- Added OPS-USE-01 through OPS-USE-04 to the maintained register: replayable
  signed-evidence retention; candidate/promotion/source/assets/approval
  binding; allowlisted semantic gate/policy/result references; sanitized
  actionable reasons and an opt-in required-evidence profile. All are
  evidence-needed/open with pending operator decisions, not confirmed bugs.
  Source citations, counter-evidence, owners, tradeoffs and closure evidence
  are recorded there.
- Session reports focused native collector/helper/command/inventory tests
  and whitespace checks passing; security/fix-first reviews found no
  high-confidence vulnerability or implementation defect requiring a fix.
  Coordinator read the report and verified source identity, not those tests
  or an independent security review.
- No real registry/attestation/Azure/archive/period observation or runtime,
  policy, deployment change. REL-OP-01/OPS-08 remain unresolved;
  operating effectiveness not-assessed; no new implementation authorization
  or finding closure.
