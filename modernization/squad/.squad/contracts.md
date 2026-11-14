# Shared modernization contracts

The [CAO lifecycle](process.md) and [governance](governance.md) apply at every
phase. Retain the detailed source evidence, compatibility, preservation,
data-transition and independent-approval contracts below.

## CAO admission envelope

Before any implementation or executable validation, require:

- Control repository, exact workflow SHA and effective policy reference; explicit
  scope/mode and admission identity. Default review mode grants no live authority.
- Exactly one target repository/source revision and approved issue identity.
- Exact central proposal revision, human approval identity/time/conditions,
  selected option/destination requirement and per-repository approved work scope.
- Allowed read/write paths, actions, commands, environment/account limits,
  exclusions, duration/expiry or revalidation trigger and stop conditions.
- Dependency DAG node, predecessor artifact/interface revisions, accepted
  evidence references and explicit gate-satisfaction result. An issue/PR existing
  is not proof that its dependency is satisfied.
- One qualified accountable specialist, independent reviewer and independent
  verifier identities; exact input/output artifact and diff digests.
- Preservation/acceptance criteria, evidence methods, rollback/data constraints,
  expected safe-output type `draft-pull-request` and no automatic merge.

Check against actual workflow authority, not a self-asserted target file.
Missing or mismatched fields block the affected action. The envelope cannot
grant tools or safe outputs the workflow does not declare. Cross-repository
planning/verification consumes explicitly authorized observations centrally;
each executable repository task is admitted separately for one target.

## Evidence envelope

Every artifact records:

| Field | Required content |
|-------|------------------|
| Identity | Artifact ID, revision or content digest, author, date/time and timezone, phase, scope. |
| Source | Repository and commit SHA; relevant paths; dirty-worktree diff digest if applicable. Never label a dirty inspection as committed evidence. |
| Method | Tool name/version or manual inspection method, command/query when safe, inspected scope, filters, exclusions, sample size and collection date. |
| Findings | Distinguish observed facts, derived results, assumptions, unknowns, not-measured, and not-attempted. Link each consequential claim to evidence. |
| Measurements | Value, unit, denominator, population/sample, environment, interval, baseline and uncertainty. A missing measurement is not zero. |
| Limits | Inaccessible repositories/files, missing credentials, unsupported tooling, failed builds, sampling gaps and stale evidence; explain decision impact. |
| References | Source citations with revision/path/line or dated official URLs; redact secrets and sensitive payloads. |

Inventory includes languages, frameworks, build/dependency systems, runtime and
hosting assumptions, APIs, serialization, identity, stores, integrations,
desktop/client flows, deployment topology, and existing tests where observable.
Counts, coverage, complexity, coupling, confidence and effort are optional only
when meaningful and supported by a reproducible method. Do not manufacture
precise scores or treat a guessed endpoint count as a measurement.

## Option and plan contracts

Classification addresses Rehost, Replatform, Refactor, Repurchase, Retire and
Retain individually, including why an option is infeasible or unresolved.
Compare smallest viable change, supported in-place upgrade and coexistence before
assuming replacement. Record constraints, benefits, risks, source/destination
compatibility, dependencies, and stakeholder decision. Uncertainty can prevent
ranking; do not force numbers for an incomplete evidence base.

Each plan phase has measurable success criteria: observable result, threshold
or exact expected shape, measurement method, environment, accountable owner,
independent verifier and required evidence. Include dependency order, bounded
change set, authorized repositories/environments, stop criteria, rollback
triggers and ownership, and how to resume after a rejected gate.

Cost and effort estimates state their date, source, currency, region, sizing,
load, environments, availability/recovery posture, licensing, storage,
network/egress, observability and operational labor assumptions. Label scenarios
and estimates as such. Explain uncertainty and exclusions; use unknown or
not-measured when inputs are missing. Budget approval does not authorize spend.

## Handoff envelope

Every handoff identifies:

- Sending phase/owner and one accountable receiving owner.
- Exact input/output artifact revisions and source revisions.
- Decision made, decision owner, unresolved questions and evidence limitations.
- Invariants and acceptance criteria that must survive the next phase.
- Dependencies, data/control boundaries, preconditions and blocked items.
- Permitted actions, repositories, paths, environments, duration and exclusions;
  link the approval record. No permission is conveyed by a handoff alone.
- Expected output shape, validation method, review gate, escalation and stop
  conditions.

Receiver confirms inputs and permissions before acting. New source evidence or
changed scope is returned for impact analysis, not silently folded into an old
approval. No pass-through handoff may expand authority.

## Approval envelope

Record approval ID, human approver or explicitly delegated authority, timestamp,
purpose (quality / option selection / execution / publication / cutover /
rollback), exact artifact revision or diff digest, repository/source revision,
scope and permitted actions, environment/account boundary, conditions and expiry
or revalidation trigger. Record rejection and supersession as new records.

Quality approval cannot authorize writes, command execution, cloud operations,
publication or traffic/data changes. Every material artifact, scope, dependency
or environment change requires impact review and renewed affected grants.
Independent reviewers and validators must not be the author/executor of the
work they certify. If an independent qualified reviewer is unavailable, report
the gate blocked rather than have the author self-certify.

## Preservation and compatibility envelope

Planner and domain owners enumerate observable invariants, including:

| Surface | Examples of required invariants and evidence |
|---------|--------------------------------------------|
| APIs and messaging | Routes, status/error semantics, SOAP/WSDL, event schemas, ordering, retries, idempotency, version negotiation and consumer behavior. |
| Serialization | Field names, defaults, nulls, casing, enums, dates/timezones, numbers/precision, binary/XML/JSON formats and stored payload compatibility. |
| Identity and security | Authentication flows, authorization/claims, session semantics, federation, certificates/secrets handling, service identities and audit continuity. |
| Data | Schema semantics, collation, keys, transactions, consistency, lineage, retention, reconciliation tolerances, ownership and recovery objectives. |
| Desktop and UX | WinForms/WPF/Silverlight/VB.NET and web interactions, bindings, events, offline behavior, printing/reporting, keyboard/focus, accessibility, installer/update and OS integration. |
| Operations | Configuration, hosting lifecycle, jobs, files, cache/session behavior, observability, performance, capacity, availability and recovery. |

Capture baseline and characterization evidence before changing the affected
surface. Differential tests compare old/new behavior with representative inputs
and explicit allowed differences; sanitize fixtures. Include negative cases,
consumer contract tests and deployment/desktop interaction tests as applicable.
Record which proof was static versus executed. Unsupported legacy baselines
need isolated risk-controlled evidence collection, not unauthorized execution.

Intentional breaking changes need a separately selected and approved contract,
consumer transition, coexistence/versioning and rollback plan. Do not promise
transparent compatibility based only on compilation or matching type names.

## Data transition and cutover envelope

Name the authoritative writer for each entity at every phase. Define copy/
backfill/CDC strategy, transaction boundaries, ordering, deletes/tombstones,
idempotency, schema evolution, lag limits, reconciliation counts/checksums and
business aggregates, sampling limits, exception handling and sign-off owners.
Dual-write is not a default and requires an explicit consistency/failure design.

Plan rehearsal, traffic switch, write freeze or ownership transfer, go/no-go
criteria, backup/restore verification, rollback triggers and deadline, reverse
data handling and post-cutover monitoring. State if data loss, irreversible
transformations or divergent writes make rollback impossible; do not describe
redeploying old code as data rollback. Retirement requires verified consumer
removal, retention/export and shutdown authorization.

Planner integrates this plan; Data Transition Assurance authors cross-store
controls with each database expert; Validator independently verifies results.
Operators/user authorize actual cutover and rollback actions.

## Lifecycle and compatibility freshness

For each critical source and destination dependency, record product/edition,
exact version, OS/runtime/provider context, check date, official lifecycle and
compatibility URLs, support status, support end dates if published, constraints
and uncertainty. Consult vendor/platform documentation and official repositories,
not an unverified summary. Do not encode frozen minimum-version floors.

Recheck at planning, before execution if the plan/evidence has aged or changed,
and before release/cutover. Record a risk-appropriate expiry/recheck trigger in
the plan rather than assuming a source stays current forever. Unknown support
status is explicit and may block the affected decision.

For Aspire, use **only `https://aspire.dev` and the official
`https://github.com/dotnet/aspire` repository** as authoritative sources.
Aspire expertise covers polyglot distributed applications, not only .NET
applications; the distinct language/runtime experts retain their own contracts.
Any technology can be source or destination when the selected option and dated
compatibility evidence support it; expertise is not a target recommendation.

## Specialist charter contract

Every subsequently authored charter must contain:

1. Stable identity, role, expertise and decision ownership, using preview.4
   charter headings such as Identity, What I Own, How I Work and Boundaries.
2. Concrete demand triggers and source/destination competence.
3. Scope, exclusions and escalation coverage; no duplicate workflow ownership.
4. Required inputs and refusal/stop conditions for incomplete evidence.
5. Evidence outputs and exact receiving-owner handoffs.
6. Execution permissions, default read-only consultation and revision-bound gates.
7. Domain-specific behavior invariants, lifecycle checks, characterization/
   differential validation and rollback/data-transition concerns.
8. Shared-contract references, collaboration and independent review.

Use no static technology floors, invented estimates, unconditional cloud
deployment, autonomous publication/cutover, or implied authority from expertise.
Skills teach focused techniques; they do not create new owners or permissions.
