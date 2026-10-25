# ADR: Three-tier operational storage, deterministic ingestion, and query optimization

## Status

Proposed — 2026-10-23.

This record proposes an architectural direction and implementation gates. It
does not authorize a rollout or claim that the acceptance criteria below already
pass. The separately specified [daily run-count layer](../specs/dashboard-data.md#722-additive-daily-run-counts)
implements an initial additive slice, migrates `workflow-runtime-health-summary`
the Runs daily chart, and the repository run-volume chart, and applies three-week logical aggregate eviction with
replay fencing across browser IndexedDB, Activity SQLite, and hosted PostgreSQL.
It does not implement the remaining metrics or native weekly aggregate
partitions. Independently implemented [statistical sampling](../specs/dashboard-data.md#511-independent-statistical-run-projection)
and [native Run-subtree capacity](../specs/dashboard-data.md#37-storage-quota)
do not make this full proposal implemented or relax its remaining gates.

In particular, this proposal conflicts with the current
[dashboard data specification](../specs/dashboard-data.md): Section 5 retains
all available browser Run summaries, and Section 72 permits only the separately
specified daily run-count projection, not the full aggregate tier below. Those
contracts remain authoritative. The PostgreSQL contract currently partitions run
detail daily; this proposal adds weekly **aggregate** partitions, not weekly
run-detail partitions. Existing Operational Value evidence retention and
operational audit, queue, authentication, and coordination storage are outside
this decision.

## Context

CAO is a short-term operational health and investigation system, not a data
warehouse. At 10,000 registered repositories, a $25,000–$100,000 daily AI budget
and an assumed $1 per run imply 25,000–100,000 runs per day. At 50 tool calls and
five domain calls per run, that is 1.25–5 million tool observations and
125,000–500,000 domain observations per day, before audits and enrichment.
Retaining every execution and observation is inconsistent with a lightweight
browser and a small local database.

The current architecture offers reusable seams, but not this three-tier model:

| Existing owner | Relevant behavior |
| --- | --- |
| `dashboard/site/src/data/model/` and `normalize/` | Canonical entities, relationships, and normalization shared by local projections. |
| `dashboard/site/src/data/ingest/coordinator.js` | Coordinates canonical shard ingestion and progress. |
| `dashboard/site/src/data/storage/indexeddb.js` and `shards.js` | Browser transactions, indexes, and disposable shard accounting. |
| `dashboard/site/src/data/storage/sqlite-indexeddb.js` | SQLite implementation of the canonical IndexedDB storage subset. |
| `dashboard/site/src/data/storage/retention.js` | Browser Run summaries have no time expiry; linked detail defaults to seven days; byte capping removes whole run subtrees. No statistical tier. |
| `activity/sqlite-retention.mjs` | SQLite defaults to 30-day Run summaries and seven-day detail, independently of browser defaults. |
| `server/internal/ingest/`, `dashboardstore/`, and `postgresx/` | Go ingestion and native relational PostgreSQL query/storage boundaries; atomic publication and revision state; oldest-first complete-subtree capacity eviction preserves fresh Runs and independent daily counts. |
| `server/spec/storage.tsp` and its emitter | Source of generated PostgreSQL schema, not hand-edited SQL. |
| `dashboard/site/dashboard.json` and `dashboard-fragments/` | Composed Dashboard Language queries, including Subject, Objective, and Acceptance contracts. |
| `dashboard/site/src/data/queries/` and `internal/dashboardquery/` | Existing declarative query execution boundaries; no component-side query replacement. |

The accepted [server ingestion ADR](server-webhook-driven-ingestion.md) explains
why acquisition reuses Activity's evidence format. It is historical rationale,
not a substitute for the current native PostgreSQL storage contract.

## Decision

### Formal data model

Use **typed relational algebra with a guarded state-transition system**. Relations
express keys, ownership, and additive accounting directly across all three
backends; transitions express atomicity, replay, and publication without assuming
SQL transactions or IndexedDB APIs. This is a mathematical specification of the
proposal, not executable TLA+, an implemented schema, or a model-checked proof.
The numbered decisions below refine its policy parameters.

#### Domains, identities, and relations

Let `N`, `R`, `F`, `G`, `A`, `I`, `T`, and `D` denote namespaces, repository
identities, workflow identities, GitHub run identities, attempt numbers,
observation identities, UTC instants, and UTC dates. Identifiers use canonical
encoding and bytewise ordering. Define:

```text
RunKey         = N × R × G
ExecutionKey   = RunKey × A
ObservationKey = ExecutionKey × Kind × I
AggregateKey   = N × D × R × F
Tier           = {hot, statistical}
FieldValue(X)  = Absent | Unknown | Null | Present(X)
```

These are tagged alternatives: `Present(0) ≠ Absent ≠ Unknown ≠ Null`.
`RunKey` is the namespaced logical form of the existing canonical Run ID;
`ExecutionKey` distinguishes source attempts, not additional canonical Runs.
`Kind` ranges over the existing run-owned observation types.

The following are logical relations; implementations may combine tables or
stores without changing their keys or dependencies. `key → attributes` denotes
a functional dependency after reconciliation.

| Relation | Key and dependent attributes |
| --- | --- |
| `Repository` | `(n, r) → canonical repository attributes` |
| `Workflow` | `(n, r, f) → canonical workflow attributes` |
| `Run` | `u: RunKey → workflow, winningAttempt, executionTime, status, outcome, completeness, revision` |
| `Observation` | `o: ObservationKey → payload, revision, provenance` |
| `Membership` | `u: RunKey → tier, policyFingerprint` |
| `Sample` | `u: RunKey → stratum, priority, frameId, inclusionProbability?, weightValidity` |
| `Contribution` | `u: RunKey → revision, sparse contribution vector, reconciliation fingerprint` |
| `Daily` | `g: AggregateKey → additive metric vector, coverage metadata` |
| `Frame` | `(scope, stratum) → populationCount, allocation, seedVersion, watermark, validity` |
| `Import` | `scope → manifestDigest, policyFingerprint, watermark, phase, expectedActiveRevision` |
| `Progress` | `(scope, shardDigest, phase) → committedPosition, receipt` |

Each Run references a Workflow in the same namespace and repository. Each
retained Observation references its Run and preserves its source attempt.
Membership and Sample reference retained Runs; `Sample` exists exactly for
statistical members. Contribution receipts do **not** require a retained Run:
their keys and compact reconciliation state survive detail eviction. Daily
rows and retained detail keep their structural parents reachable.

#### Evidence reconciliation

Let `E` be the finite **set** of normalized evidence revisions in a completed
scope, identified by source identity, revision, and canonical payload digest.
Duplicates do not add elements. Let `ρᵥ(E)` be the canonical reconciliation at
version `v`, including deterministic diagnostics for unresolved conflicts.

Incremental reconciliation must implement a merge `⊔` satisfying:

```text
x ⊔ x             = x                         (idempotence)
x ⊔ y             = y ⊔ x                     (commutativity)
(x ⊔ y) ⊔ z       = x ⊔ (y ⊔ z)               (associativity)
ρᵥ(E₁ ∪ E₂)       = ρᵥ(E₁) ⊔ ρᵥ(E₂)
```

This requires enough revision/provenance state to distinguish “field absent”
from an authoritative tombstone and to preserve precedence when batches merge.
A naive field overwrite or “latest arrival wins” does not satisfy the model.
Unsupported conflicts produce the same quarantine result for every ordering,
not an arbitrary usable value. The equations are obligations for the proposed
reconciler, not claims about the existing implementation.

#### Exact contributions and conservation

Let `M` be the fixed set of additive metric coordinates, including outcome
counters and the observation counts for each measured quantity. Use agreed
integer base units or exact fixed-point representations; subtraction operates
in `ℤ^M`. Persisted observed counts remain nonnegative. Incomparable units
must not occupy the same coordinate.

Let `Cᵥ(u, ρᵥ(E))` be a finite sparse map `AggregateKey → ℤ^M`, calculated for
one canonical Run and its reconciled observations. Its date is the reconciled
execution date. Its outcome coordinates sum to one for a counted Run, including
the explicit pending/unknown classes, not one per attempt. Unknown measurements
add neither a measured value nor a measurement count; an observed zero adds
zero value and the appropriate positive observation count. Unsupported or
undated evidence is diagnosed rather than assigned a made-up date.

For watermark `W`, let `week(W)` be its UTC Monday-starting week and let:

```text
Q(W) = dates in week(W), week(W) - 1 week, or week(W) - 2 weeks
L[u] = last committed contribution receipt for u, restricted to Q(W)
Daily[g] = Σu L[u][g]                          for date(g) ∈ Q(W)
```

Missing map entries are zero. `L` denotes compact receipts, not historical
execution detail. For an admissible reconciled update to `u`, commit together:

```text
old       := L[u]                             (zero if never admitted)
new       := Cᵥ(u, updatedEvidence) restricted to Q(W)
Daily'    := Daily - old + new
L'[u]     := new
```

A date, workflow, or winning-attempt correction uses the same equation: it can
remove one retained key and add another without counting a second Run.
Replaying an unchanged revision has `old = new`, hence zero delta. Deleting a
hot or statistical bundle changes neither `L` nor `Daily`. Aggregate rollover
removes expired Daily coordinates and the corresponding receipt coordinates
together; it does not subtract detail from retained dates.

Updates involving an expired date require a surviving receipt and authoritative
revision evidence sufficient to distinguish correction from replay. Otherwise
reject/quarantine the update; do not apply `new` with an assumed zero `old`.
Sealed-scope replay guards prevent expired inputs from recreating history.
Thus conservation is exact for admitted, reconcilable evidence, not a claim of
complete upstream collection.

#### Tier selection and sampling frame

Fix policy `P = (h, s, p, budget, seed, v, allocationVersion)` for one backend
and namespace: hot/statistical/pending capacities, byte envelope, seed, and
algorithm versions. `W` is an explicit logical watermark, never wall-clock
import time. Let `B(E)` be the set of eligible complete terminal Run bundles
after reconciliation; incomplete/running bundles are in a separate set `U`.
Let `t(u)` be the reconciled execution time. Define:

```text
order(u) = (t(u), canonicalByteEncoding(u))
H        = greatest min(h, |B(E)|) elements of B(E) by order
C        = {u ∈ B(E) \ H : W - 7 days ≤ t(u) < W}
Cj       = {u ∈ C : stratum(u) = j}
Nj       = |Cj|
(kj)     = allocateP((Nj)) with 0 ≤ kj ≤ Nj and Σj kj ≤ s
priority(u) = (Hash(seedVersion, seed, framedEncoding(u)), encoding(u))
Sj       = least kj elements of Cj by priority
S        = union of Sj
```

Stratum is execution UTC date × outcome class. `allocateP` is the versioned,
deterministic proportional/failure-reserve policy in Section 3. It depends on
reconciled population counts, not arrival order. For a completed frame:

```text
H ∩ S = ∅                 |H| ≤ h                 |S| ≤ s
S ⊆ C                     |Uadmitted| ≤ p
retained detail = bundles(H ∪ S) plus bounded pending staging
```

Bundle retention preserves all admitted associated observations together, not
independently sampled children. Retention cannot turn a partial bundle into a
complete one. Byte limits additionally constrain all protected state and staging;
inability to fit aborts admission or causes a recorded policy reduction, never
a silent violation of exact accounting or a partial bundle deletion.

Under the ideal uniform-priority model, conditional on a complete, frozen frame
with `Nj > 0`, inclusion probability for `u ∈ Cj` is `πu = kj/Nj`.
For a sampled measurement `yu`, a supported candidate-population total estimator
is `Σu∈S yu/πu`, only for populations whose contributing strata have positive
inclusion probability. Hashing is a deterministic approximation to this random
priority model; quality is assessed across seeds. This estimator is not used
instead of exact Daily totals.

Store probability with its frame identity. Online reclassification, allocation
changes, and expiration without refill do not establish a new complete frame:
set validity to unsupported unless the surviving design's probabilities can be
derived. In particular, recomputing `π = survivingCount/currentPopulation` is not
justified. The model deliberately does not assert online/bulk sample equivalence.

#### Atomic state transitions and convergence

Represent persisted logical state as:

```text
Σ = (activeRevision, activeProjection, stagingScopes, progress,
     contributionReceipts, samplingState, policy, watermark, replayGuards)
```

`Begin(scope)` fixes the manifest, policy fingerprint, reconciliation version,
seed, and watermark. `Apply(scope, batch)` atomically merges evidence, applies
admissible contribution deltas to that scope's working state, and checkpoints
progress and sampling state. Uncommitted writes have no effect after a crash;
retrying committed evidence is idempotent. A staged bulk scope is not visible
through the active query projection.

`Finalize(scope)` is enabled only after all manifested shards are verified,
required metadata is reconciled, selection is complete, and budget/integrity
checks pass. `Activate(scope)` atomically publishes its complete projection and
accounting state only if `expectedActiveRevision = activeRevision`; otherwise
the stale scope must be reconciled or rejected, not overwrite newer state.
`Expire(W')` requires `W' ≥ W` and atomically expires samples and rolls aggregate
weeks while preserving the conservation equation. `EvictDetail(u)` removes its
bundle and obsolete membership, not its contribution, and is enabled only
after selection establishes `u ∉ H ∪ S` and it is not protected pending evidence.
Displacement, expiration, or budget-driven policy changes must commit the new
selection and any affected sampling-validity changes atomically with eviction;
a current hot winner or frozen-frame sample cannot be deleted independently.
Online commits obey the same atomic accounting rule but retain the weaker
sampling contract.

Let `Π` discard physical row order, local timing, and other noncanonical
telemetry. For the same admissible starting state, complete evidence set `E`,
policy `P`, versions, and cutoff `W`, successful bulk finalization must satisfy:

```text
support(flatten(batches₁)) = support(flatten(batches₂)) = E

Π(Finalize(Apply*(Σ₀, batches₁), P, W))
  = Π(Finalize(Apply*(Σ₀, batches₂), P, W))
```

`flatten` concatenates batches; `support` takes their set of evidence revisions,
discarding multiplicity. `batches₁` and `batches₂` may use different ordering,
duplication, grouping, concurrency, and crash/retry schedules.
Backend implementations refine this logical result under identical policies;
differing defaults are not a parity failure. This is a safety/convergence
obligation for successful imports, not a
liveness guarantee under insufficient storage or invalid evidence. Golden
fixtures and permutation/crash tests must establish these properties before
rollout; no formal verification tool or proof is introduced by this ADR.

### 1. Three complementary layers, one logical contract

Use complete recent execution detail for debugging, representative older detail
for investigation and supported estimates, and exact daily sufficient statistics
for operational totals, rates, and averages.

| Policy | PostgreSQL | SQLite | IndexedDB |
| --- | ---: | ---: | ---: |
| Hot execution capacity | 16,000 | 2,000 | 1,000 |
| Statistical execution capacity | 64,000 | 8,000 | 16,000 |
| Statistical horizon | Seven days | Seven days | Seven days |
| Aggregate retention | Three UTC calendar weeks | Three UTC calendar weeks | Three UTC calendar weeks |
| Storage constraint | Configurable byte budget | Configurable, at most 1 GB | Minimize downloads, persisted bytes, startup time, and heap |

Each backend has its own independently configurable hot and statistical
capacities and byte limits. One backend's override must not change another's.
Validate nonnegative capacities and bounded maintenance settings at startup.
The seven-day horizon and minimum aggregate coverage are invariants, not
accidental consequences of a capacity default.

SQLite capacities are provisional, not evidence that the database fits.
Measure real records, indexes, contribution receipts, WAL, temporary writes,
and maintenance overhead. Treat 1 GB as 1,000,000,000 bytes and a hard total
deployment storage envelope, not just the main file size. Choose a normal
operating target substantially below it after calibration.

Retain the current UTC Monday-starting week and the preceding two weeks. This
provides at least fourteen full days plus the current partial day, approaching
twenty-one days just before rollover. Use equivalent logical week deletion on
SQLite and IndexedDB; only PostgreSQL requires native weekly partitions.

### 2. Stable identities, ordering, and reconciliation precede retention

Reuse the repository-scoped canonical Run identity, which excludes attempt
under RUN-001/RUN-002 in the current specification. Distinguish source execution
evidence with `(canonical Run ID, attempt)` so rerun observations do not collide,
then reconcile them into one canonical Run with its winning attempt. Namespace
every identity. Observation identity must come from an immutable source identity
or a documented canonical identity tuple, never ingestion position or arrival
time.

Capacities, hot ordering, sample membership, and run-outcome counts operate on
canonical Runs, not a second Run per attempt. A retained bundle preserves the
attempt keys of its associated observations. Queries explicitly about attempts
must retain that grain; they cannot substitute canonical Run counts. A changed
winning attempt updates the existing contribution rather than adding a run.
Measurements spanning attempts need their own documented observation-level
deduplication and contribution semantics, not an implicit change to Run identity.

Define one versioned reconciliation contract shared by JavaScript and Go:

- Compare producer revision and authoritative source precedence before a
  canonical payload digest tie-breaker. A digest makes ties deterministic; it
  does not establish which conflicting value is true.
- Merge partial fields without letting missing data erase known data. Explicit
  correction or deletion requires an authoritative revision/tombstone.
- Keep unknown, absent, explicit null, and observed zero distinct.
- Resolve conflicting outcomes, timestamps, and observation revisions before
  calculating contributions or selecting tiers. Quarantine unsupported conflicts
  with coverage diagnostics rather than choosing the first arrival.
- Specify UTC timestamp precision, identifier encoding, hash input framing, and
  bytewise identity ordering. Do not depend on locale collation or backend
  floating-point differences; use common numeric units and rounding rules.

The ordering key is a reconciled execution timestamp with a documented fallback
from execution start to creation time, followed by stable run identity. A local
import timestamp is never a fallback. Missing execution time remains diagnostic
evidence and is not silently placed in an arbitrary retention day.

Retain the newest complete execution bundles under that global ordering. A
bundle contains its tools, domains, audits, and other run-owned debugging
observations. Completeness records source availability; it does not fabricate
missing logs. Running and incomplete runs use a separately bounded pending
staging allowance, with explicit timeout and overflow diagnostics. They contribute
known aggregate values but cannot consume unbounded hot slots. Terminal,
partially observed bundles remain distinguishable from complete bundles.

Capacity is not a guaranteed two-day window. An old hot bundle is considered
for sampling only when displaced, and only if it is in the statistical horizon.
Delete an unselected bundle and its owned observations together. Preserve
structural parents required by either retained detail or aggregates.

### 3. Deterministic, stratified bottom-k sampling

Use established bottom-k priority sampling without replacement, with UTC time
buckets and outcome stratification, rather than a new reservoir heuristic.
Derive a uniform hash priority from the versioned seed and immutable run identity;
break hash ties by identity. Never hash mutable cost, outcome, or arrival order
into the priority. Store algorithm version and seed identifier with the policy.
See [bottom-k sketches](https://doi.org/10.1145/1281192.1281205) for the sampling
family, not a claim that an ordinary reservoir solves expiration.

Candidate population: reconciled, displaced terminal execution bundles in the
half-open interval `[watermark - seven days, watermark)`, excluding hot bundles.
Pending or undated evidence is not part of a distribution of completed runs.
Use at most eight intersecting UTC date buckets and a fixed, small set of
outcome classes, including an explicit unknown class. Do not create a reservoir
for every registered repository or workflow.

For a completed import scope:

1. Count eligible candidates per date/outcome stratum using compact metadata.
2. Allocate the statistical budget proportionally to observed candidate volume,
   with a bounded, configured reserve for failure classes. Use deterministic
   largest-remainder rounding, cap allocations at population sizes, and
   redistribute unused slots deterministically.
3. Select the lowest priorities within each stratum, after final hot selection.
   A streaming bounded heap per stratum is sufficient once allocations are frozen.
4. Persist stratum population `N`, selected count `k`, policy/cutoff, and inclusion
   probability `k/N` for selected runs when the complete population is known.
   The corresponding expansion weight is `N/k`.

Repository and workflow representation is activity-proportional within outcome
strata: a busy workflow supplies more candidates, while inactive repositories
receive no allocation. Failure reserves improve rare-outcome visibility, not
guaranteed coverage of every rare repository, failure code, model, or workflow.
Measure those coverage limits explicitly. Bounded capacity cannot guarantee
precise estimates for 10,000 small repository populations.

Time buckets do not justify keeping expired samples. Expire at the exact
watermark boundary; a partially expired boundary bucket can become underfilled
after discarded candidates are gone. Likewise, increasing allocations, changing
strata through corrections, or removing hot runs cannot recover discarded detail.
Do not invent replacement samples or assume a fresh `k/N` probability after
such changes.

For continuous evidence, freeze bucket allocations for an admission epoch,
checkpoint heaps, counters, and displacement decisions atomically, and retain
the same priority rule. Deduplicated late arrivals may compete while their
stratum remains open. Once detail has been discarded, allocation increases and
boundary expiration are forward-only; underfill and activity shifts introduce
coverage bias. Reclassification can invalidate the original sampling frame.
Expose these limitations and mark weights unavailable unless inclusion
probabilities are justified for the actual surviving population. Do not present
an online reservoir as equivalent to a completed bulk reconstruction.

For valid samples, weighted distribution estimates must name their population,
coverage, effective sample size, and uncertainty method. Weighted quantiles and
small-group ratios are estimates, not exact values or automatically unbiased.
Strata with zero inclusion probability cannot support population estimates.

### 4. Exact daily contributions survive detail eviction

Introduce a canonical daily aggregate with key:

`(namespace, UTC date, repository_id, workflow_id)`.

Keep the dimensions fixed. Store additive sufficient statistics and the
appropriate observation counts, rather than averages of averages:

- Runs by mutually exclusive outcome, including unknown and pending states.
- AIC and monetary cost separately, preserving units and currency comparability.
- Input, output, reasoning, and cached tokens, with availability counts.
- Duration sum and duration observation count.
- Tool calls, failures, retries, domain calls, and security blocks.
- Errors and evaluation-result counts where existing semantics are additive.
- Additional additive measurements needed by reviewed existing queries, with
  explicit denominators, evidence quality, and contribution version.

Attribute a run to its reconciled execution UTC date; linked measurements follow
that run's date. Corrections that change the date or parent apply a negative old
contribution and a positive new contribution in one transaction. Specify this
attribution in query contracts; do not silently change an event-time query into
a run-start-time query.

Maintain a compact contribution receipt for each logical execution revision,
independent of retained detail. Reconcile the new canonical contribution and
apply `new - previously applied`, then replace the receipt atomically.
Observation revisions must first reconcile into that execution contribution.
Duplicate shards and retries apply zero delta. Partial runs contribute only
observed fields; enrichment adds deltas; terminal corrections move outcome
counters rather than incrementing a second run.

Eviction never subtracts a contribution. Receipts and replay guards live as
long as their aggregate partition can be corrected or replayed, not just as
long as hot detail. Bounded per-run contribution vectors and authoritative
revisioned run summaries are preferred to an unbounded ledger of every tool
event. Producers must supply enough normalized summary evidence to compute
exact contributions even for runs whose detail will not be downloaded.

An isolated late event cannot safely revise a discarded run without a prior
contribution and a reconcilable revision. Require the producer's authoritative
replacement contribution or retain the necessary compact reconciliation state.
Otherwise report the correction as incomplete; never approximate an exact
counter. Reject evidence for expired aggregate partitions without recreating
them. Bound retry receipts by sealed scope/partition, with durable replay
rejection after expiry.

Exact means exact for reconciled available evidence, with coverage diagnostics,
not proof that all upstream executions were collected. Distinct counts,
percentiles, arbitrary dimension filters, and event sequences are not additive.
Do not sum daily distinct counts or add dimensions to make an incompatible query
appear supported.

### 5. Completed shard imports are deterministic transactions of scope

For the same complete shard set, configuration, seed, reconciliation version,
and logical watermark, SQLite and IndexedDB must publish the same canonical
records, tier memberships, aggregate values, and valid sample metadata.
Different backend defaults intentionally produce different memberships; parity
tests use identical policies. PostgreSQL follows the same logical contract.

Define an import scope by immutable manifest identity, verified shard hashes,
policy fingerprint, and explicit watermark. The watermark is part of the
published data contract, not `Date.now()`. Advancing it is an explicit state
transition even if no new shards arrive.

Use compact run-information metadata to reconcile identities, contributions,
global hot ordering, candidate counts, and frozen statistical selections before
fetching selected detail. When metadata is insufficient, preserve candidates in
bounded durable staging or perform a second pass over the **current supplied
shard set** before publishing. That second pass is not historical backfill.
Do not permanently discard a possible winner after the first arriving shard.

Account for staging and replacement generations in byte and browser budgets.
If an input lacks sufficient metadata and cannot fit a bounded import strategy,
fail the import with diagnostics and preserve the prior active dataset; do not
quietly weaken determinism or require an unbounded browser candidate store.

Within each committed batch, canonical changes, contribution deltas, candidate
state, and shard position/receipt commit together. Use durable scope/phase
checkpoints for multi-transaction work and atomically activate only a completed,
validated scope. Recovery resumes or discards staging, never treats an incomplete
receipt as success. Concurrent writers serialize or compare-and-swap the scope
revision; a stale worker cannot activate over a newer completed scope.

Import order, batch boundaries, worker concurrency, retries, and duplicate
shards must not change the activated result. Local progress timestamps and
performance telemetry may differ but cannot participate in canonical decisions.
Continuous ingestion has the explicitly weaker forward-only sampling contract
above; it does not relax aggregate correction or transactional replay safety.

### 6. Shared processing and backend responsibilities

Reuse the canonical model, adapters, ingestion coordinator, and serving
boundaries. One logical pipeline performs:

1. Normalize canonical evidence.
2. Deduplicate and reconcile partial/conflicting revisions.
3. Apply exact daily contribution deltas.
4. Maintain globally ordered bounded hot bundles.
5. Evaluate displaced eligible bundles for sampling.
6. Persist statistical bundles and justified sampling metadata.
7. Expire by logical watermark and compact within budget.
8. Commit progress and sampling state atomically with the affected writes.

Keep reconciliation, contribution calculation, ordering, allocation, and
priority decisions pure. Share JavaScript logic between SQLite and IndexedDB;
use language-neutral fixtures to enforce equivalent Go decisions. Backends own
atomic persistence, indexed selection, bounded batch deletion, and maintenance.
Avoid whole-database materialization and per-record cleanup transactions.

**PostgreSQL:** extend the TypeSpec-owned native schema with relational tier,
receipt, and aggregate structures. Index namespace, execution order, stratum,
priority, and aggregate keys. Update contributions and tier ownership
transactionally. Provision/drop weekly aggregate partitions through owner-run
maintenance, not runtime DDL. Keep linked-detail expiration compatible with the
existing daily partitions. Use bounded set-based cleanup and measured SQL plans.

**SQLite:** preserve canonical storage/adapters and atomic shard receipts. Measure
page count/size, indexes, freelist, WAL, temporary writes, and compaction peak.
Under pressure reduce statistical capacity first with a recorded deterministic
policy change; do not delete exact aggregates to save detail. Persist the
effective capacity so replay and parity compare the same policy rather than
device-dependent free space. If minimum protected state or a single execution
bundle cannot fit, stop admission explicitly. Checkpoint/compact with bounded
headroom; `VACUUM` is not free storage reclamation.

**IndexedDB:** load compact identity/contribution manifests first and download
only selected detail where shard metadata supports it. Coarse shards may still
require overfetch; measure this rather than claiming zero waste. Use compact
records, indexed cursors, incremental writes, and bounded worker batches.
Account for browser quota independently of estimated serialized bytes. Keep
the active view abort-scoped and subscribed to worker query revisions.

Hosted pages continue querying the same-origin Go service and PostgreSQL; no
fallback to IndexedDB or static shards when hosted services fail. Explicit
Pages deployments retain their browser ingestion path.

### 7. Query routing follows intent, not the query's name

Review the composed dashboard document and every referenced query's Subject,
Objective, Acceptance, source dependencies, filters, time grain, and output.
Preserve IDs, navigation, and evidence meaning. This ADR changes no query.

The discovery inventory below identifies migration candidates and blockers,
not completed migrations. Paths are under `dashboard/site/dashboard-fragments/`.
“Detail” means hot for recent exact inspection and explicitly labelled
statistical context for older retained examples, never an exact population.

| Fragment / existing query IDs | Proposed layer / disposition |
| --- | --- |
| `overview.json`: `overview-run-summary` | Mixed: daily outcome totals plus current pending state; rollout-mode predicates need supported counters or remain a blocker. |
| `overview.json`: `cost-by-repository`, `cost-by-workflow`, `cost-per-workflow-run` | Aggregate candidates: preserve AIC units, observed-cost denominators, and quality. |
| `overview.json`: `overview-delivery-summary`, `overview-repository-coverage`, `overview-workflow-health-groups` | Mixed/blocked: distinct targets, worker dispatch filters, and latest workflow state cannot be reconstructed by summing daily totals. |
| `overview.json`: `overview-value-summary`, `overview-issue-summary`, status/presentation queries | Retain dedicated evidence sources; route dependencies, not presentation objects, to new tiers. |
| `inventory.json`: `runs-daily-conclusions` | Migrated to the isolated daily run-count layer as runtime health classes, not raw conclusions; whole UTC start-day buckets and aggregate coverage remain explicit. |
| `inventory.json`: `workflow-aic-per-run` | Blocked for run-count-only migration: positive-AIC attempt denominators must remain aligned with retained cost telemetry. |
| `inventory.json`: `workflow-ordered-runs`, `workflow-latest-runs`, `runs-table`, `entity-events` | Detail: latest identity, ordered attempts, and individual observations are not daily counters. |
| `workflow-runs.json`: `workflow-runtime-health-summary`, `workflow-runtime-usage-summary`, `workflow-failure-trend` | Aggregate candidates; pending state and usage-record counts require their own exact contribution semantics. |
| `workflow-runs.json`: `entity-runs`, `workflow-runs`, `workflow-reports`, `outcome-route-record` | Detail/dedicated outcome evidence; preserve route keys and retained execution links. |
| `repositories.json`: `entity-workflow-run-totals`, `repository-run-totals`; `shared-inventory.json`: `repository-aic-totals` | Aggregate candidates for additive counts and cost; retain declaration joins. |
| `repositories.json`: `repository-run-inventory`, `repository-observed-run-counts` | Migrated chart-only count branch: logical Runs in selected whole UTC start-day buckets, with inventory links and aggregate coverage; the Table and usage queries remain unchanged. |
| `repositories.json`: `repository-run-identities`, `repository-import-totals`, `repository-runs` | Detail/mixed: import coverage and run identity must not become sampled population counts. |
| `shared-events.json`: `event-base`, `audit-event-runs`, `domain-event-runs`, `tool-event-runs`, `issue-event-runs`, `event-runs`, `run-import-status` | Detail: exact retained-event/import diagnostics, with bounded recent coverage. |
| `shared-events.json`: `workflow-run-totals`, `workflow-aic-run-totals`, `workflow-aic-totals` | Aggregate candidates with explicit run-versus-telemetry denominators. |
| `shared-inventory.json`: `repository-run-statistics`, `campaign-workflow-execution-totals` | Mixed: exact supported counters plus structural inventory; unsupported grouping remains blocked. |
| `shared-inventory.json`, `shared-campaigns.json`: declarations, issue labels, inventory and maintenance projections | Keep structural/dedicated evidence sources; do not infer declarations or authority from sampled executions. |
| `shared-campaigns.json`: `campaign-runtime-health-groups`, `campaign-unobserved-orchestrators`, `dispatches` | Detail/mixed: consecutive failures, absence, and dispatched target identity need ordered or complete evidence. |
| `campaign-runtime.json`: `campaign-performance-baseline-totals`, `campaign-performance-baseline`; `campaign-insights.json`: `campaign-baseline-outcome-distribution` | Mixed aggregate candidates only where campaign membership and selected-horizon outcome semantics remain derivable. |
| `campaigns.json`: `audit-event-summary-buckets`; `campaign-problems.json`: error groups and latest target runs; `campaign-insights.json`: audit plots | Detail/statistical examples: summary text, severity, and sequence are outside the fixed aggregate dimensions. |
| `controls.json`: `firewall-domain-totals`, `firewall-most-blocked-domains`, `mcp-top-tools`, `steering-type-totals`; `shared-events.json`: `mcp-tool-calls` | Detail; named domain/tool/type breakdowns cannot use a workflow/day total. Older distributions need valid weights and estimate labels. |
| `entities.json`: route records, run records, and entity insights | Detail; preserve navigation to retained bundles, explicitly explain unavailable detail. |
| `model-routing.json`: `routing-session-*`, `routing-request-*`, `routing-accounting`, `routing-exact-accounting`, `routing-correlated-accounting` | Detail/mixed: session deduplication and request correlation remain necessary; a precomputed run sum is not a substitute. |
| `model-routing.json`: `routing-weekly-cost`, `routing-weekly-models`, `routing-weekly-paired-cost`, `routing-weekly-baseline-estimate` | Mixed/blocked: model, phase, paired baseline, and session semantics exceed the aggregate key. Statistical comparison only where its acceptance permits estimates. |
| `operations.json`: `engines-models-usage`, skill breakdowns, experiment coverage, grader/eval ledgers and distributions | Detail/dedicated evidence or valid statistical distributions; no new model/skill/experiment aggregate dimensions. Preserve existing evidence retention. |
| `operations.json`: ingestion health, API usage, shard receipts, table counts, marketplace; `commands.json`: `command-queue` | Keep operational/structural sources. Physical retained-row counts are not population activity totals. |
| `workflow-value.json`: all `value-*` queries | Keep dedicated Operational Value evidence contracts and retention; no inference of accepted outcomes from execution success. |
| `simulators.json`: all `simulator-*` queries | Keep explicitly synthetic inputs; revise obsolete modeled retention assumptions when implementing this architecture. |
| `overview-attention.json`: `failed-runs`, `overview-repeated-failure-groups`, `overview-needs-attention` | Detail/mixed: recent incident identity and failure streaks require hot sequence evidence; sampled context cannot prove an uninterrupted streak. |
| `wizard.json`, `governance.json`, `controls.json`, `overview.json` and remaining inventory/presentation dependencies | Preserve controls, declarations, and presentation-only composition; recursively classify every dependency before migration. |

This is a family-level discovery inventory. Implementation must produce a
complete per-query ledger from the composed root and all 21 referenced fragments,
including intermediate queries: ID, source, semantic metadata, required fields,
filters, exactness, selected layer, blockers, and regression fixture. No query
is considered migrated because its family appears here.

Push filters, joins, and aggregation into the existing backend query engines.
Do not add JavaScript row filtering, presenter callbacks, or a parallel query
language. Daily aggregates answer only supported complete-day predicates.
Sub-day boundaries require complete retained detail or an explicit incomplete
result; samples cannot fill exact gaps. Forecasts use exact additive series
and statistically valid distributions, with uncertainty and limited history.

Avoid double-counting: totals come from contributions, not aggregates plus hot
rows; detail unions deduplicate by canonical run identity; samples exclude hot
membership. If combining a hot census and sampled remainder for a valid
estimate, weight the census at one and only the eligible sampled remainder by
its justified inclusion probability.

Expose exact/estimated/incomplete status, covered dates, watermark, sample
coverage, and missing detail in payloads and views. Keep retained execution
routes usable. Never silently replace “all selected-horizon records” with
“available sample,” nor label a truncated history as an exact zero.

### 8. Delivery and validation gates

Implement in separately reviewable phases:

1. **Discovery:** complete the per-query ledger and measure existing schemas,
   ingestion, detail sizes, and query plans against real evidence.
2. **Model:** specify tier/configuration metadata, identities, precedence, UTC
   ordering, contribution schema, cutoffs, and versioned migration contracts.
3. **Sampling:** implement pure bottom-k allocation/selection and test statistical
   quality, bias, expiration, and unsupported weighting conditions.
4. **Aggregates:** implement correction-safe receipts and contribution deltas,
   including partial execution and duplicate/revision behavior.
5. **Backends:** integrate native PostgreSQL and canonical SQLite/IndexedDB stores.
6. **Import:** implement scope staging, deterministic finalization, checkpoints,
   and restart-safe concurrent ingestion.
7. **Queries:** migrate only semantically compatible queries with fixtures,
   estimate labels, route preservation, and backend pushdown.
8. **Retention:** enforce capacities, exact seven-day expiry, weekly rollover,
   calibrated storage budgets, and pressure behavior.
9. **Validation:** qualify correctness, parity, quality, and resource use before
   enabling the new profile.

Update `specs/dashboard-data.md` and related ingestion/query contracts first in
the implementation series, then `ARCHITECTURE.md` and its compiled
`CODEBASE.yml` when the architecture changes. Update canonical/browser schema
versions, TypeSpec-generated PostgreSQL artifacts, query/view declarations,
fixtures, and generated agent query catalogs when affected. Existing
incompatible PostgreSQL layouts require fresh canonical storage under the
current contract; do not migrate by deleting operational audit or queue state.
No existing deployment is switched by this ADR.

Test the stated 10,000-repository workload at both 25,000 and 100,000 daily
runs, including idle repositories, skewed activity, rare failures, oversized
bundles, and at least a complete three-week rollover. Test continuous admission
separately from completed bulk imports.

Use deterministic golden fixtures and randomized shard permutations, duplicate
shards, changed batching, interrupted phases, crash-before/after-commit, partial
enrichment, conflicting revisions, outcome/date corrections, concurrent writers,
seed/configuration changes, and watermark-only advancement. Compare canonical
results across SQLite, IndexedDB, and PostgreSQL with identical policy.

Measure database and maintenance peak bytes, browser download volume and heap,
startup cost, ingestion throughput, rollover duration, query latency/plans, and
coverage by repository/workflow/outcome. Across multiple fixed seeds, check
inclusion-frequency calibration, weighted estimates against a known population,
rare-failure coverage, and coverage loss after expiration or reallocation.
Set numerical performance and coverage budgets from the measured baseline before
rollout; these measurements are required work, not results established here.

Use the existing unit/integration and dashboard query-parity suites, PostgreSQL
query-cost tests, browser memory tests, and dashboard performance tests. Do not
substitute synthetic JavaScript source generation for production query-boundary
tests.

#### Implementation acceptance checklist

- [ ] All three tiers work across PostgreSQL, SQLite, and IndexedDB.
- [ ] Defaults match the backend table; hot/statistical capacities are independent.
- [ ] Statistical bundles expire at seven days against the explicit watermark.
- [ ] Exact daily contributions survive detail eviction and cover at least fourteen days.
- [ ] Duplicate/reordered complete imports converge under the same policy and seed.
- [ ] Partial runs, enrichment, corrections, retries, and replay never double-count.
- [ ] Import checkpoints recover safely and stale concurrent writers cannot publish.
- [ ] SQLite stays within its configured total budget under measured workloads.
- [ ] IndexedDB download, storage, startup, and heap overhead meet calibrated budgets.
- [ ] Every query has a mapping and preserves its intent or records a blocking incompatibility.
- [ ] Exact, estimated, incomplete, and unavailable-detail results are distinguishable.
- [ ] Statistical quality, parity, query regression, and performance gates are automated.

## Alternatives considered

- **Keep all summaries and time-expire detail:** simple, but summary growth remains
  unbounded and exact detail-dependent metrics vanish with detail.
- **Keep only recent runs:** bounded and useful for debugging, but cannot answer
  two-week operational totals or provide representative older context.
- **Keep only a uniform reservoir:** misses rare failures at high skew, does not
  preserve exact counters, and is not by itself a sliding-window algorithm.
- **Equal quotas per repository/workflow:** spends state on inactivity and
  undersamples the workflows producing most executions.
- **Aggregate from whatever detail remains:** makes totals depend on eviction,
  shard order, and sampling; rejected.
- **Separate backend-specific semantics:** easier locally, but violates parity,
  reproducibility, and portable dashboard meaning.
- **Warehouse all history in CAO:** outside its operational role. Long-term
  analytics belong in OpenTelemetry; this decision does not redesign it.

## Consequences and non-goals

The design bounds expensive detail while preserving exact supported operational
metrics and deterministic completed imports. It adds compact reconciliation
state, import-scope coordination, sampling provenance, and query-planning work.
Execution counts alone do not bound bytes; unusually large bundles, receipt
volume, and staging overhead can force admission failure even below run caps.

Sampling does not guarantee examples for every repository, accurate small-group
estimates, arbitrary historical filters, or lossless sliding-window refill.
Some existing queries cannot migrate to the fixed aggregate dimensions without
an explicit product decision; they remain visible blockers, not opportunities to
silently weaken semantics.

Non-goals are historical warehousing, backfill or reconstruction of discarded
history, new database engines, new aggregation dimensions, redesigning
OpenTelemetry, changing control-plane authority or rollout policy, and replacing
dashboard behavior unrelated to retention and query efficiency.
