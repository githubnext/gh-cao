---
title: Central Agentic Ops Value Specification
description: Normative cross-subsystem contract for defining, collecting, retaining, enriching, and presenting workflow Value.
version: 1.0.0
status: Working Draft
editors:
  - GitHub Next
---

# Central Agentic Ops Value Specification

**Version:** 1.0.0  
**Status:** Working Draft  
**Latest Version:** https://github.com/githubnext/gh-cao/blob/main/specs/value.md

## 1. Scope and conformance

This specification defines the cross-subsystem contract for **Value** in Central
Agentic Ops. Value is the degree to which a workflow's intended repository
outcome is attained across eligible opportunities, demonstrated by accepted
evidence under a frozen measurement contract.

Value measures repository outcomes, not workflow execution, output volume,
runtime success, an operational grader result, or an agent's assessment. The
same repository outcome attained by a person or another system MUST count under
the same contract.

Sections 2 through 11 are normative. The key words "MUST", "MUST NOT",
"REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED",
"NOT RECOMMENDED", "MAY", and "OPTIONAL" are interpreted as in RFC 2119.

This specification owns the integration invariants between the detailed
contracts below:

- [`activity.md`](activity.md) owns the ordinary Activity snapshot boundary.
- [`operational-value-history.md`](operational-value-history.md) owns compact
  observation reconstruction, cadence compaction, and publication.
- [`dashboard-data.md`](dashboard-data.md) owns canonical entities, logical
  sources, retention, Dashboard Language queries, rich evidence packets, and
  collection-health records.
- [`session-archive.md`](session-archive.md) owns the independently enabled
  private unified-session archive.

Those specifications remain authoritative for their internal formats and
algorithms. This specification is authoritative when connecting their
authority, clocks, retention, collection lanes, and evidence meanings.
Contradictory requirements are specification defects and MUST NOT be resolved
by silently weakening authority, completeness, freshness, or privacy.

## 2. Value contract

Every Value measure MUST freeze, before outcome inspection:

- the exact repository and workflow identity;
- the intended repository outcome;
- the eligible-opportunity population;
- the accepted-evidence rule;
- metric identifier, version, native unit, direction, role, and formula;
- interval or snapshot grain, duration, cadence, and maturation;
- coverage and zero-versus-missing behavior; and
- evaluator and evidence provenance.

Exactly one metric is primary. Additional metrics are diagnostics and MUST NOT
be averaged into an invented composite. Native values MUST NOT be normalized,
clamped, or converted to a dimensionless score merely for presentation.

Value MUST count the repository outcome regardless of actor. Workflow-produced
issues, pull requests, reports, traces, recommendations, and runs are mechanism
or lineage evidence; their creation alone is not accepted Value. A linked
contribution MUST follow explicit output-to-implementation lineage and evaluate
the repository-native condition at the accepted revision after any frozen
maturation period.

Missing, partial, interim, stale, unavailable, zero, pending, rejected, and
unknown are distinct states. A newer unavailable assessment MUST remain visible
and MUST NOT reveal an older numeric assessment as current.

## 3. Canonical Value populations

The canonical model contains three separate Value populations:

1. `operationalValues`: compact repository-scoped native metric assessments at
   the frozen cadence.
2. `operationalValueEvidence`: one bounded current rich packet per exact target
   repository, workflow filename stem, contribution metric identifier, and
   metric version.
3. `workflowValueCollections`: the latest reviewed registration and collection
   decision, with independently reconciled actual-attempt facts.

Run-scoped gh-aw operational-grader metrics are a fourth, distinct population.
They MUST NOT be relabeled or aggregated as repository-scoped Value.

Rich evidence packets MAY contain the frozen contract, intent sources, outputs,
accepted implementations, lineage, independent snapshots, snapshot items,
quality checks, coverage, and limitations. A packet replacement MUST be atomic.
Facts from different packet revisions MUST NOT be merged. Imports and
publication MUST NOT enroll a repository, register a collector, execute code,
or grant rollout authority.

Collection health MUST NOT promote evidence completeness or freshness. A
registration decision, deferral, failure, or webhook hint is not a measurement.

## 4. Clocks, retention, and storage tiers

Implementations MUST preserve these clocks independently:

- policy/collection decision observation;
- actual collector attempt;
- accepted packet capture;
- evidence window end or snapshot cutoff;
- opportunity creation;
- implementation acceptance and maturation; and
- private archive generation and observation.

No clock MAY be substituted for another. Replaying retained evidence MUST NOT
advance its capture or cutoff.

Default canonical retention is:

- run-linked detail: seven days;
- Run summaries: 30 days;
- compact `operationalValues`: at least 365 days, independently of Run and
  run-detail retention; and
- rich evidence and collection health: their bounded current canonical records,
  independently of Run-detail eviction.

Explicitly longer historical SQLite windows MUST preserve the corresponding
longer compact Value history. Shortening Run or detail retention MUST NOT
shorten compact Value retention. The seven-day `statistical-runs` frame is an
independent frozen statistical sample; it does not replace exact sources,
extend historical evidence, or change Value retention.

The private Parquet session archive has independent retention, authority, and
recovery. It is not a warm or cold tier for canonical Value, Run, or dashboard
storage.

## 5. Collection and publication lanes

Value has separate lanes with different authority:

### 5.1 Explicit evaluation and publication

An operator MAY evaluate a verified frozen function and publish its original
schema-v2 evidence. Hosted publication requires the authenticated Value API;
static publication uses the reviewed Activity input boundary. Publication does
not register or schedule a collector.

### 5.2 Compact current and historical observations

Current compact collection evaluates the latest frozen cadence observation.
Historical reconstruction is an explicit bounded action governed by
`operational-value-history.md`; it is never triggered by dashboard navigation.
Expired source evidence remains missing unless a contemporaneous accepted
snapshot or immutable reconstruction is available.

### 5.3 Recurring remote collection

Recurring remote collection requires an exact reviewed repository/workflow
registration and a confined collector deployed with the same reviewed policy.
It runs only inside effective Activity scope and the frozen cadence. Function or
collector presence alone grants no execution authority.

A recurring collector SHOULD freeze a bounded acquisition manifest with one or
more requirements. Each requirement identifies its source, quota pool,
versioned primitive, exact population scope, and permitted cache lifetime.
Registration MUST copy that normalized manifest into reviewed policy, and
execution MUST fail closed when the deployed collector's manifest differs.
The manifest describes required evidence; it does not grant credentials,
repository reach, request capacity, archive access, mutation, or publication
authority.

Hosted collection MUST compile all enabled manifests for one repository under
the repository lease before invoking their collectors. Identical requirements
MAY be unioned for planning. When at least two workflows declare the same
invocation-cacheable GitHub requirement, the repository-confined quota bridge
MAY return one byte-identical successful REST, Search, artifact, or GraphQL
response to later collectors in that repository collection. Each collector
MUST retain its own request ceiling, timeout, proof workspace, starting-evidence
check, output validation, publication CAS, and failure state.
Every reusable request MUST identify the exact reviewed requirement that
produced it. The bridge MUST partition responses by that requirement identity
and native quota pool. Missing, unknown, or pool-incompatible identities MUST
remain metered and isolated rather than borrowing another requirement's cache.

The hosted collector MUST keep accepted reusable proof in private PostgreSQL
storage rather than relying on one worker's filesystem. Proof is partitioned by
the exact repository/workflow deployment, bounded by file count and bytes,
restored only into a private invocation workspace, and atomically replaced only
in the transaction that accepts a valid evidence packet. A failed acquisition,
proof write, or publication MUST retain both the previous accepted packet and
the previous database proof. Repository withdrawal MUST erase both the
canonical evidence and its private proof. Proof bytes are neither a Dashboard
Language source nor accepted outcome evidence.

Every hosted attempt MUST also append a credential-free daily cost aggregate in
PostgreSQL. The aggregate separates Core, Search, and GraphQL request counts,
GraphQL points, exact-response cache hits, quota preflights, wall time, output
bytes, transport bytes, success/failure counts, and a bounded failure class.
These private aggregates MAY be exposed to authorized operators for capacity
calibration; they MUST NOT expose credentials, request bodies, response bodies,
proof content, or arbitrary stderr.

The native hosted acquisition profile MAY separately select Value execution
with `CollectorConfig.NativeValues`. That lane MUST resolve the reviewed
catalog and policy and execute supported acquisition and local computation in
Go; it MUST reject unsupported collector registrations rather than invoke a
Node program. Git subprocesses MAY be used only for reviewed Git transport
requirements. An unselected Value execution lane MUST NOT inspect its catalog
or policy assets.

The Go hosted local evaluator supports the reviewed Optimization canonical-query
measurement. A selected Dependabot measurement requiring remote alert and
pull-request lifecycle evidence that the Go lane cannot acquire MUST publish
explicit missing/unavailable measurements, not omit them or infer zero. The
separate Actions snapshot pipeline retains its own reviewed collector.

Selected native remote invocations MUST restore the starting packet and proof
from one PostgreSQL snapshot pinned to their original installation and enrollment
epoch. Private per-invocation scratch and local transport sockets are disposable
implementation details, never recovery or coordination authorities. Publication
MUST check that same epoch and starting packet under the canonical writer
transaction before replacing packet and proof together. An identical committed
packet/proof retry MAY succeed idempotently; it MUST NOT replace different proof.
Withdrawal MUST erase private proof and publication ownership with the canonical
graph, and reenrollment MUST NOT let an old invocation read or publish against
the new owner's proof. Concurrent invalidations MUST remain pending when an
invocation acknowledges only its original dirty generation.

### 5.4 Local-only collection

A separately admitted local-only lane MAY query an already published canonical
snapshot through the production Dashboard Language boundary. It MUST NOT use
target credentials, perform remote acquisition, backfill history
automatically, or delay ordinary Activity publication.

### 5.5 Hosted webhook-hinted collection

Only the explicitly selected hosted GitHub App profile MAY turn verified,
deduplicated issue, pull-request, completed-run, and default-branch push
deliveries into durable Value refresh hints. Section 7 defines the additional
requirements.

No lane MAY introduce a per-workflow schedule, model reassessment, navigation
side effect, or implicit registration.

## 6. Admission, budgets, and failure safety

A remote collector MUST declare a reviewed integer `api-requests` ceiling from
1 through 1,000. Actions Activity MUST finish ordinary owner-scoped collection
before considering the collector. It MAY invoke the collector only when the
entire ceiling fits above the selected credential's protected core reserve.

The admitted collector MUST receive a bounded secret-free durable grant.
One unit MUST be durably consumed before every uncached REST, Search, artifact,
or GraphQL request. Cached reads consume no new unit. A failed request MAY
remain counted, but no request may execute before its count is persisted.
Git transport remains under separate bounded immutable-object controls.

Shared response reuse MUST remain bounded to one repository, installation, API
origin, policy snapshot, and collection invocation. The bridge MUST validate
the later collector's endpoint or GraphQL scope before reading the cache. It
MUST NOT cache transport failures, responses without authoritative quota
metadata, GraphQL results with errors or unverified point cost, or HTTP server
errors. Cache saturation MUST reduce reuse rather than fail collection or
silently widen memory.

Hosted fleet scheduling MUST preserve installation boundaries in addition to
repository leases. Due repositories SHOULD be interleaved across installations,
and one worker process MUST NOT run more than one remote Value acquisition at a
time for the same installation. Global worker parallelism remains bounded, and
the shared quota services remain authoritative even when multiple worker
processes or replicas are present.

Hosted collectors MUST keep installation credentials outside the collector
process and forward requests only through the repository-confined shared quota
bridge. Core, Search, and GraphQL resources MUST retain their native quota
semantics; GraphQL points MUST NOT be treated as request count.

Missing enforcement, insufficient or unknown capacity, exhausted grants,
changed policy or enrollment, superseded starting evidence, timeout, invalid
output, incomplete evidence, or publication conflict MUST fail or defer closed.
They MUST retain the previous accepted packet, proof, immutable facts, capture,
and cutoff. They MUST NOT publish partial evidence, fabricate zero, or make
retained evidence appear fresh.

Optional Value work MUST NOT delay or invalidate ordinary Activity publication.
A separate process or worker is not separate quota.

## 7. Webhook semantics

A webhook is a prompt to inspect evidence. It is not accepted evidence,
registration, cadence satisfaction, freshness, or proof that the intended
outcome occurred.

Hosted refresh hints MUST:

- be accepted only for verified event types and enrolled repositories;
- remain separate from Run and inventory collection;
- preserve a newer generation arriving during acquisition;
- be repaired by an independent daily sweep;
- use bounded worker concurrency and a distributed single-owner repository
  lease;
- recheck enrollment, registration, policy, cadence, starting evidence, and
  canonical revision before publication; and
- erase private proof and pending hint state on withdrawal.

The default Actions profile has no inbound webhook receiver. Missed, delayed,
duplicated, or reordered deliveries MUST NOT create false freshness or prevent
the daily repair path from revisiting a due registration.

## 8. Private archive linkage

The independently default-off session archive MAY provide approved retained
session context for exact producer references. It MUST NOT become the outcome
database, canonical Value history, Git baseline, accepted-output receipt,
quality proof, or freshness authority.

A registered collector MAY receive a bounded capability equivalent to:

```js
request.archive.link([{ outputId, runId, attempt }])
```

Each producer MUST name an exact output identifier and Run identifier. A known
attempt MUST be an exact positive integer; a missing or unknown attempt remains
unknown and MUST NOT be queried or inferred. The capability MUST be fixed to the
registration's exact repository, MUST NOT enumerate repositories, and MUST NOT
accept arbitrary SQL, KQL, paths, or caller-selected historical generations.
It returns linkage availability and bounded metadata only; raw session payloads
MUST NOT enter public shards or canonical Value packets.

Archive linkage requires separate operator approval, exact repository
allowlisting, archive availability, and the archive service's own full-trace
read authorization. `disabled`, `unavailable`, `no-exact-producer`, `partial`,
and `available` describe linkage availability, not outcome acceptance or
historical completeness. Failure or denial MUST remain explicit and MUST NOT
trigger a GitHub recollection fallback.

## 9. Diagnostics and private cost evidence

Collector failures MUST expose a bounded non-sensitive category without
exposing credentials, raw stderr, evidence payloads, private URLs, or archive
content.

The cross-runtime worker marker is one UTF-8 stderr line:

```text
CAO_WORKFLOW_VALUE_FAILURE {JSON}
```

The JSON object MUST contain `reasonCode` equal to one of:

- `api-budget`: the admitted request grant was exhausted;
- `budget-unavailable`: required grant state could not be verified;
- `timeout`: the collector command exceeded its time budget; or
- `collector-failed`: another bounded collector failure occurred.

An optional worker `detail` MUST be static, non-sensitive, and no longer than
160 characters. A host MAY ignore it and generate fixed host-owned text.
Malformed markers, unknown reason codes, and arbitrary stderr MUST map to
`collector-failed`; they MUST NOT be forwarded to users.

The host MAY additionally classify `output-budget` when its own stdout or
stderr bound is exceeded. Host-owned timeout and output-budget evidence takes
precedence over a child marker.

Private per-pair cost history MAY retain the allowlisted failure category,
actual metered API requests, capacity checks, observed GraphQL points and
unknown-point counts, wall time, and received/output bytes. It MUST distinguish
unknown from zero and MUST NOT call wall time CPU or billing cost. Unknown
failure categories MUST be rejected or normalized to `collector-failed`.

Diagnostics do not alter accepted evidence. Decision, attempt, capture, and
cutoff clocks remain independent.

## 10. Query and presentation

All Value selection, filtering, grouping, comparison, aggregation, movement,
and presentation state MUST be expressed in Dashboard Language and executed
through the production browser worker or server query engine. UI components and
effects MUST NOT acquire evidence or derive business state.

The latest period and assessment MUST be selected before considering numeric
value or coverage. Recent movement requires complete mature evidence for the
latest period and its immediately preceding equal-duration compatible period.
Interval periods must abut; snapshot comparisons use declared cutoffs.
Changing the display horizon MUST NOT change the frozen evaluator.

Value presentation MUST:

- preserve native units and direction;
- distinguish outcome state from linked contribution diagnostics;
- expose coverage, maturity, population, evidence, and evaluator identity;
- keep collection health separate from result cards;
- describe temporal movement as observed association, not causation; and
- retain unavailable or stale latest evidence rather than presenting an older
  all-clear.

## 11. Required conformance coverage

Implementations MUST retain automated coverage for:

- 365-day compact Value retention independent of seven-day detail and 30-day
  Run retention;
- atomic rich-packet replacement and stale/conflicting capture rejection;
- independent decision, attempt, capture, and cutoff clocks;
- exact-pair registration, explicit disable, and effective-scope enforcement;
- full remote grant admission above the protected reserve and durable
  per-request consumption;
- prior-evidence retention after capacity failure, exhaustion, timeout, output
  overflow, invalid evidence, superseded input, or publication conflict;
- webhook deduplication, generation preservation, cadence gating, and daily
  repair;
- archive-link authority, response bounds, unavailable states, and raw-content
  isolation;
- allowlisted worker diagnostics and raw-stderr isolation; and
- equivalent declarative Value results across supported canonical query
  backends.

## 12. References

- [Operational-Value History Reconstruction Specification](operational-value-history.md)
- [Dashboard Data Architecture Specification](dashboard-data.md)
- [CAO Activity Specification](activity.md)
- [Private Unified-Session Archive](session-archive.md)
- [Dashboard Product Specification](dashboard.md)
