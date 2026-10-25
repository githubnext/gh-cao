---
title: Central Agentic Ops Dashboard Data Architecture Specification
description: Canonical data model, ingestion, IndexedDB persistence, consistency, recovery, and scale requirements for the gh-cao dashboard.
version: 1.8.3
status: Working Draft
editors:
  - GitHub Next
---

# Central Agentic Ops Dashboard Data Architecture Specification

**Version:** 1.8.3
**Status:** Working Draft
**Repository:** `githubnext/gh-cao`
**Target implementation:** Dashboard data subsystem
**Date:** 2026-10-11

Implementers changing canonical data, ingestion, storage, or query execution
should read `docs/dashboard-data-model.md` first, then this specification and
the production boundary under `dashboard/site/src/data/`. Computation changes
must also follow `specs/computations.md`.

### Model routing evidence

Model routing analytics SHALL read metadata projected from `usage/aw_session.jsonl`
into the canonical Audit collection, not native logs, cumulative run totals or
agent-written routing summaries. Ingestion SHALL materialize one compact routing
aggregate on the canonical Run using the shared Dashboard Language query engine,
not a JavaScript implementation of query or accounting semantics. The five
overview views SHALL read only these Run aggregates; their source reads and row
counts SHALL NOT grow with the number of model requests. The aggregate retains
the run's routing state, configured goal/mode, pick, health/accounting coverage,
recorded job outcome, baseline evidence and identifier-only problem causes.
It SHALL use the same selected attempt and proxy-first rules as the drill-down.
Canonical Run identity owns the aggregate, so refreshes replace rather than append
another aggregate record. Structural-only refreshes of the same attempt SHALL
preserve the aggregate; another attempt SHALL NOT inherit it.

Audit retains typed routing identifiers,
classifier labels, request identities, sub-agent ownership, token classes,
per-response AIC and collection receipts. It MUST NOT retain prompts, bodies,
free-form errors/reasons, raw tool payloads or content-derived hashes.
The canonical model remains version 35. IndexedDB version 46 SHALL preserve
version 44 and 45 databases with a `runs` store through the additive upgrade in
§5.1; incompatible older browser projections SHALL be rebuilt.
Hosted PostgreSQL deployments require a fresh schema with the generated Audit
and Run aggregate columns. Inventory (`model.inventory`) and generic proxy
(`firewall.event`) rows SHALL NOT be retained, including replayed cached
projections. Ingestion-only baseline and candidate-pool fields SHALL be removed
from Audit before persistence. Existing run-page request, attribution, timeline
and resolution tables retain run-scoped evidence: request rows explain deviations
and unknown request costs; accounting rows explain excluded traffic and unmatched
ownership. Deeper analysis SHALL use the Actions run's `usage` artifact and full
`aw_session.jsonl`, linked from the run-page accounting view.

The aggregate SHALL retain `model.baseline_estimate.baselineModel`, `aic` and
the explicitly recorded `complete` flag. The observed comparison SHALL use
the same main-agent proxy requests as T-UAS-081, never total agent-phase AIC.
The producer's complete estimate attests that `as_selected` agent-phase requests
do not match declared sub-agent models; classifier traffic, explicitly owned
sub-agent traffic, detection and evals are excluded. Request identities SHALL be
counted once, all paired observed AIC values SHALL be known, and their count SHALL
match the producer's `mainRequests`. This is a comparison-specific population:
it MUST NOT reclassify unknown-purpose traffic as main-agent traffic in general
accounting. Missing or mismatched request counts SHALL make the comparison
unknown. Only routed runs with a named baseline, `complete=true`, a positive
baseline estimate and matching known observed main-request cost SHALL contribute
to either comparison numerator or denominator. Workflow ratios SHALL divide
the sums over that identical eligible set, grouped separately by baseline model.
Incomplete or absent estimates, non-positive denominators and unknown observed
cost SHALL remain unknown, never zero or inferred savings. A recorded partial
estimate MAY appear on the run page, explicitly labelled incomplete.

The final proxy selection's complete `eligibleChoices` and `catalogueOverlap`
counters SHALL be validated as non-negative integers during ingestion. A
`candidates not rankable` problem SHALL occur only when both counters are known
and overlap is smaller than eligibility; it SHALL show the observed overlap/pool
counts. Unknown counters and equal counts SHALL NOT produce a problem. Neither
policy model-list length, inventory size nor a displayed ranking length is a
substitute denominator. Ranking rows and policy lists are not retained.
Each `firewall.upstream_error` SHALL contribute to the `upstream error` problem
kind, not a duplicate generic routing failure. Distinct safe provider error
codes SHALL be summarized once per run/kind; absent codes SHALL remain unknown.
No error body, message, header, type, parameter or source is added.

Dashboard Language queries SHALL resolve proxy-first routing state, choose one
accounting source per phase, and attribute each response to one exclusive bucket.
Engine request correlation requires a complete model/token quartet and ordered
one-to-one matching; exact ownership takes precedence. Unknown AIC SHALL remain
unknown, with known/unknown coverage beside recorded lower bounds. Complete-cost
quantiles require collection receipts, known response AIC and accounted or
provably local-zero requests. Classifier cost belongs to agent-phase totals;
detection and evals remain separate. Sparse comparisons default to a minimum of
ten complete runs and SHALL be described as observational, not causal.

Headerless sessions SHALL be marked legacy. Unsupported format versions SHALL
retain format coverage only, never interpret subsequent records as supported.
Unidentified sessions and untimed evidence SHALL remain visible in coverage,
but unidentified sessions SHALL NOT contribute to routing-state denominators.
The format observation SHALL carry the earliest valid clock across all matching
source records, including messages and tools, without copying their contents.
Bounded time filters SHALL exclude wholly untimed sessions rather than substituting
the GitHub run's creation or ingestion time.

Configured-model resolution SHALL be read from the runner-written
`model.resolution` events (unified session specification T-UAS-074, session
format version 1) that gh-aw projects from `agent/model-resolution.json`, one per
recorded role. Each event SHALL become one Audit record that retains only the
event `engine` (`sourceEngine`), `role` (`resolutionRole`), `configured_model`
(`resolutionConfiguredModel`), `resolved_model` (`resolutionResolvedModel`),
`provider`, `status` and `cause` (`code`) with its source provenance and order.
Every value SHALL be identifier-shaped; configured and resolved models SHALL be
kept exactly as recorded, including provider prefixes such as
`copilot/gpt-5-mini`, without served-model shortening. `status` SHALL be one of
`resolved`, `exact` or `failed` and `cause` one of `model_unavailable` or
`catalog_unavailable`; any other, unsafe or missing value SHALL remain unknown,
never an empty string, zero, a fallback model or inferred success. A failed
resolution keeps its resolved model unknown. Resolution records carry no
timestamp; ordering SHALL use the recorded session clock and source order, never
GitHub creation or ingestion time.

The Model routing page SHALL admit `model.resolution` into
`routing-session-observations` and apply the same latest-attempt selection and
Routing scope filters before joining resolution events. Failed resolutions
SHALL appear in its Problems view with their distinct causes and a link to the
run page; per-run resolution roles SHALL stay on the run page. Roles are
records, not runs: four roles from one run count as one run. A routed session is
expected to have no configured-model resolution; absence SHALL NOT classify a
run as routed, failed or successful, and legacy sessions without the event
SHALL report no resolution evidence. Any future alias view SHALL key alias
series by repository, workflow, engine, role, provider and configured model, so
unrelated resolutions cannot manufacture a move, and SHALL highlight a move only
between adjacent runs with recorded session clocks; untimed runs are counted but
cannot establish a transition. Alias moves are observations of resolution, not
served-model evidence or a cause. The page is not required to show alias
history. The `routing-run` drill-down SHALL list each run's
resolution roles with configured-to-resolved model, status, cause, engine and
provider, showing unknown values rather than manufacturing rows.

Run drill-down queries SHALL constrain complete run partitions before projecting
Audit evidence, using canonical run identities and the Audit run index. This
constraint MUST preserve all attempts until session selection, all events and
accounting phases within the selected run, and reactive refreshes. A constraint
may cross a window only on its partition keys and may narrow a joined source
only through a preserved mandatory equality key. Cross-run history and
observational baselines MUST retain their required evidence. Tables SHALL use
canonical pagination and fail-closed output budgets, not synthesize semantic
source-query limits before view scope. All authored query limits, including a
displayed source's limit, remain semantic boundaries and MUST NOT be moved across
scope.

The Model routing page currently provides five views. Routing health by
workflow answers whether routing is on and working: per repository and workflow
with a routed run, it shows routed runs, the share with a model selected, the
share with an agent-phase routing failure or degraded classification (no
separate fallback field exists), the share of runs whose agent-phase requests
followed the pick, the success rate over known agent-job outcomes and the median
AIC per complete run. Models picked over time answers what the router chooses,
counting proxy-selected runs with a known pick model per UTC ISO week.
Health also shows estimated main-request cost versus each named baseline.
Cost per run over time retains weekly median agent-phase accounting AIC per
routing state and adds paired observed main-request and explicitly estimated
baseline series, grouped by baseline model. Paired series use identical eligible
runs, UTC ISO weeks and the same minimum-sample floor. The baseline is a
counterfactual estimate, not a claim of causal savings; routed versus unrouted
accounting remains observational because these are different tasks.
Problems answers what needs attention
with one row per run and kind (routing failure, deviation, resolution failure,
upstream error or candidates not rankable),
its cause, a run-page link and the run's recorded observation time. Routing runs
lists identified runs and opens the run drill-down. A share or median without
evidence (a zero denominator or no complete cost) and a week with fewer complete
runs than the minimum sample SHALL be unknown or a gap, never zero. It does
not implement candidate ranking, inventory/router change context, prioritized deviation causes, failure clustering, or the full
safe-output/grader/delayed-outcome comparison surfaces of the upstream analytics
specification. Absent producer fields and these unimplemented analyses MUST NOT
be represented as zero or successful outcomes.
Every routing view SHALL describe its actionable decision in the supported
`subject`, `objective` and `acceptance` metadata. Page-level `intent` remains
supported; view-level `intent` is not part of Dashboard Language.

### Canvas query authoring boundary

The canvas-only query editor SHALL submit a complete Dashboard Language document
to the data worker for structural and semantic validation before rendering it.
Preview documents SHALL contain exactly one custom `query-preview` page with
data marks only, no new CLI capabilities or named UI elements, and an explicit
per-view output limit of at most 200 rows. Every view source, including filter
options, SHALL reference a declared query with `query.limit` between 1 and the
view's `data.limit`; layered charts SHALL omit `data.limit` and bound their
source query to at most 200 rows. The worker SHALL execute the query bound
before returning rows.
Preview documents SHALL NOT declare forms, lazy pagination, or drill navigation.
Invalid documents SHALL preserve the
last accepted preview and expose validation diagnostics.

Accepted previews SHALL execute through the existing canonical query and
view-payload compiler and retain an abort-scoped subscription. Canonical database
changes SHALL refresh their payloads; replacing the document or leaving the page
SHALL cancel old subscriptions and requests. No main-thread query fallback or
test-only logical-source synthesis is permitted.
Local canvas queries SHALL execute through the Go server over the verified
canonical SQLite snapshot without opening browser IndexedDB. The canvas SHALL
NOT offer a query-backend selector or a browser fallback. Document validation
SHALL remain in the browser data worker.

Authoring intent and drafts are local interaction state, not canonical entities.
The editor SHALL NOT add IndexedDB stores, alter retention or relationships, or
write preview rows to storage. A user-requested save SHALL persist only the
validated Dashboard Language document under the workspace-local
`.cao/dashboard/custom-views/` directory, not evidence rows or browser entities.
Saved queries, views, card templates, and units SHALL have isolated identifiers;
the local set SHALL survive canvas/server restarts and SHALL NOT be loaded by
static or hosted dashboards. Source JSON/YAML SHALL be an optional disclosure;
the rendered view SHALL be primary.
Hosted SQL ingestion and storage remain unchanged:
this feature is available only in a loopback Copilot canvas, not the static or
hosted dashboard. Its preview uses the existing browser/SQL Dashboard Language
contract rather than a separate query dialect or execution service. SDK generation
SHALL receive authoring text and trusted language/skill context, not canonical
source rows, and SHALL have no filesystem, shell, MCP, or repository-write tools.

| Browser storage | IndexedDB keeps all available run summaries and expires detailed run-linked records after 7 days. |
| --- | --- |

IndexedDB SHALL retain all available canonical Repository, Workflow, and Run
summaries so dashboard trends and run history can cover the complete published
source. It SHALL retain detailed run-linked records for the bounded 7-day
browser window. Expiring run-linked records MUST NOT remove
their retained Run or the Run's structural parents.

---

## Abstract

This specification defines a new dashboard-side data architecture for Central Agentic Ops.

The implementation SHALL introduce a clean architectural boundary between existing upstream data collection/publication and dashboard application state.

Existing GitHub, gh-aw, activity snapshot, log, SQL, and generated JSON representations SHALL be treated as input sources.

The dashboard SHALL normalize those sources into a new canonical domain model:

```text
Repository
  └── Workflow
       └── Run
            ├── Domain
            ├── Tool
            ├── Skill
            ├── Friction
            ├── Audit
            └── Issue
```

The dashboard data and query implementations are IndexedDB in the browser,
SQLite for local/headless consumers, and Postgres in the Go server. The static
deployment SHALL maintain the canonical model in IndexedDB. The Go server
profile SHALL transactionally replace current dashboard sources in Postgres
and SHALL execute Dashboard Language queries server-side. Redis SHALL hold
only operational caches, queues, and sessions, not dashboard entities or
execute dashboard queries.

PostgreSQL is the default operational adapter in a PostgreSQL-only deployment.
Redis remains an explicit alternative, not a mandatory dashboard data store.
The typed operational interface MAY use bounded volatile memory under the
explicit single-process exception,
as defined in [`server-operational-storage.md`](server-operational-storage.md).
The PostgreSQL operational adapter MUST own separate `cao_operational_*` tables
and MUST NOT extend the canonical entity/query adapter or its generated schema.
Dashboard storage, SQL execution, query-cache
identity, fixed expiry, and canonical ingestion remain unchanged.

The Go query boundary MAY cache expensive queries with compact output through
the selected operational adapter
using cache-aside lookup by a query ETag (SHA-256 of the query contract, parameters,
pagination, schema version, and current authorization class). It MUST NOT key
these results by ingestion revision or invalidate them on ingestion or data
egress. Results SHALL expire five minutes after admission, without sliding
renewal; their original evidence revision and evaluation time MUST remain
visible. Cache hits MUST NOT imply newly evaluated evidence or charge the
original execution's query-plan work. Admission MUST bound each encoded result,
total adapter-specific result/index accounting bytes, and entry count, retiring expired and then
oldest entries atomically. Current authentication and authorization MUST still
be enforced before cache lookup. Canonical entity APIs and readiness probes
MUST NOT use this result cache. Query ETags identify internal cache entries;
they do not change the HTTP query response contract.

The Go server SHALL apply a configurable whole-node Redis memory-pressure budget
to query-result, marketplace, and repository-memory caches. The default SHALL be
200,000,000 bytes; the effective budget MUST NOT exceed 80% of provider-reported
`maxmemory` when it is nonzero. Cache admission MUST atomically check actual
Redis usage and decline or reclaim disposable cache allocation under pressure
on providers supporting scripted memory introspection. Cached reads MUST reserve
conservative outgoing allocation before copying or returning a payload, separate
from the stored-cache limit. Providers prohibiting atomic memory introspection
MUST explicitly disable disposable-cache admission and serving rather than
permit concurrent callers to spend independently sampled headroom. Protected
operations SHALL remain enabled when only optional cache capabilities are
unsupported.
Startup and cancellation-scoped periodic maintenance SHALL reclaim disposable
caches without requiring cache traffic. Maintenance MUST NOT evict sessions,
queues, quota state, revocation retries, non-cache keys, or other namespaces,
and MUST NOT modify Redis configuration. Pressure scans SHALL traverse the
namespace once and refresh final memory evidence before reporting unreclaimable
pressure. If protected state prevents attainment,
the server MUST report pressure rather than imply the budget was enforced.
This budget is not a cluster-wide, process-RSS or persistence-disk limit.

IndexedDB and Postgres dashboard sources SHALL be reconstructable from
authoritative inputs and MUST NOT become authoritative evidence storage.

Domain records SHALL contain allowed and blocked firewall observations. Tool
records SHALL contain MCP and Bash execution events. Skill records SHALL contain
separately extracted skill activity, and Friction records SHALL contain
precomputed avoidable execution-cost attribution. Audit records SHALL contain
all other execution observations.
Issue records SHALL contain both issues and pull requests, distinguished by an
`isPullRequest` flag. Every record in these six tables SHALL reference its
owning Run.

The architecture SHALL support eventual consistency, idempotent conversion,
large datasets, bounded-memory ingestion, integrity verification, interruption
recovery, browser storage failures, schema evolution, Node.js testing, and
real-browser testing. Derived projections MAY use immutable generations and
atomic activation, but the canonical entity stores use bounded incremental
reconciliation and MUST NOT be described as generation-atomic.

Dashboard views SHALL consume only bounded results from the canonical query
layer and SHALL NOT parse upstream source formats directly. Redis endpoints and
credentials MUST NOT be exposed to browser code.

Row-local canonical table-to-source mappings MAY execute in bounded batches
when a retained table exceeds a Dashboard Language query's row limits. Each
batch MUST retain the complete join inputs and share the mapping's execution
budget; a failed batch MUST make the whole source unavailable rather than
publish a partial success. Aggregation, prediction, temporal expansion, sorting,
unions, and limited queries MUST NOT be partitioned this way. This adapter
batching MUST NOT raise or bypass the input and output limits of dashboard
queries or truncate canonical evidence.

Storage-native aggregate execution MAY scan a single canonical record store in
bounded batches when the query planner proves that its canonical projection and
all aggregate ancestors are row-local. The native plan MUST reuse the canonical
projection and authored filters and computations; it MUST NOT select behavior by
page, view, or query identity. Supported reducers are `count`, `distinct-count`,
and `sum`, including their authored conditional filters. One accumulator MUST
retain exact distinct identities across the entire scan and add numeric values in
source order, rather than combine independently evaluated batch aggregates.
Ancestors with aggregation, ordering, limits, joins, unions, windows, predictions,
or temporal expansion, and unsupported reducers, MUST retain ordinary execution.

Native scans MUST use bounded readonly storage batches, share the request's
cancellation, deadline, and operation budget, and bound both intermediate groups
and distinct identities per reducer by the query input-row limit. Final
projection, ordering, limiting, and output-row enforcement MUST remain in the
query engine. Equivalent aliases MAY reuse a result only within the same request;
different predicates MUST NOT share it. Canonical run lookups SHOULD be reused
without rebuilding the complete join index for every batch. A failed projection
or reduction MUST NOT publish partial counts, and unavailable canonical stores
MUST remain unavailable. This storage-native plan is distinct from partitioning
an authored aggregate with the row-local adapter batching described above; it
does not change the limits of ordinary Dashboard Language execution.

The MCP tool inventory SHALL aggregate calls by tool and workflow before
materializing its shared intermediate. Its final tool totals MUST preserve
observation counts, distinct calling workflows, request and response byte
totals, and exclusion of the internal safe-output server. Detail views MAY
retain separate observation-grain queries subject to the ordinary query limits.

Views that require a hosted runtime provider SHALL declare their configured
backend prerequisite. Static deployments SHALL explain that the telemetry is
hosted-only and MUST NOT query, synthesize, or subscribe to those providers.
Hosted deployments SHALL continue to enforce provider authorization and expose
unavailable evidence honestly; selecting the hosted backend grants no authority.

---

# 1. Status of This Document

This document is a Draft implementation specification.

It defines the target architecture for a clean dashboard data subsystem.

The implementation SHALL fully replace dashboard-side source-shaped/view-shaped state, caches, direct reads, and fallback rendering with the canonical subsystem defined here.

Development MAY proceed incrementally from the source-adapter boundary inward. The completed implementation MUST NOT retain a shadow, dual-read, alias, compatibility-cache, or fallback path to the replaced browser data system.

Functioning upstream collectors and Pages deployment MAY remain as authoritative data producers. This allowance does not permit the legacy browser data architecture to remain active.

---

# 2. Requirements Notation

The key words **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHALL NOT**, **SHOULD**, **SHOULD NOT**, **RECOMMENDED**, **NOT RECOMMENDED**, **MAY**, and **OPTIONAL** in this document are to be interpreted as described in RFC 2119.

---

# 3. Architectural Decision

## 3.1 Clean Dashboard-Side Start

The new dashboard data layer SHALL be designed independently of existing dashboard presentation structures.

Existing dashboard source schemas SHALL be treated as source inputs.

They SHALL NOT define the new canonical domain model.

The architectural boundary SHALL be:

```text
EXISTING / UPSTREAM

GitHub
gh-aw
activity snapshots
logs
SQL
published dashboard JSON
        |
        v
====================================
NEW DASHBOARD DATA ARCHITECTURE
====================================
        |
        v
source adapters
        |
        v
canonical normalization
        |
        v
canonical model
        |
        v
IndexedDB
        |
        v
query layer
        |
        v
views
```

## 3.2 Upstream Stability

The initial implementation SHOULD NOT rewrite working:

* GitHub collection;
* workflow discovery;
* activity collection;
* gh-aw log generation;
* policy enforcement;
* Pages deployment;
* existing source publication.

Those components SHALL initially be treated as data producers.

## 3.3 Source Inputs

The current dashboard source JSON MAY be used to bootstrap the new architecture.

However:

> Existing source representations are inputs. They are not the new product contract.

Accepting an existing representation at an adapter boundary MUST NOT expose that representation directly to views or preserve a second browser state path.

---

# 4. Core Invariants

The following requirements are architectural invariants.

## INV-001 — Canonical model

The dashboard MUST have one canonical internal domain model.

## INV-002 — Source independence

The canonical model MUST NOT mechanically mirror any single source representation.

## INV-003 — Views are source-agnostic

Views MUST NOT parse:

* raw logs;
* GitHub responses;
* SQL rows;
* firewall files;
* current source JSON schemas.

## INV-004 — IndexedDB is derived state

IndexedDB MUST be disposable and reconstructable.

The local SQLite projection MUST also be reconstructable from the deployed
dashboard artifact.

## INV-005 — Authoritative inputs remain external

Deletion of IndexedDB MUST NOT cause permanent information loss.

## INV-006 — Deterministic normalization

Identical authoritative observations MUST normalize to identical logical entities.

## INV-007 — Idempotent ingestion

Reprocessing identical input MUST NOT create duplicate entities or run-linked records.

## INV-008 — Eventual consistency

Partial observations MAY arrive at different times and MUST converge toward a coherent canonical state.

## INV-009 — Fail-safe ingestion status

An incomplete ingestion MUST NOT be recorded as current. Bounded canonical
writes MAY already have committed before a later shard or validation failure;
the failed phase MUST remain retryable, and record-dependent subscribers MUST
retain their last complete result until that phase succeeds.

## INV-010 — Bounded processing

Correctness MUST NOT require loading the complete historical dataset into browser memory.

The local server profile MUST execute compatible selection, range filtering,
aggregation, ordering, and limiting in PostgreSQL, with bounded Go fallbacks
where required.

## INV-011 — Test parity

Node tests and browser runtime SHOULD exercise the same canonical and persistence interfaces.

## INV-012 — Full replacement

Views MUST read only through the canonical query boundary. The completed
implementation MUST NOT fall back to a legacy cache, source object, alias, or
renderer data path.

---

# 5. Target Architecture

```mermaid
flowchart LR
  logs["gh aw logs"] --> source["JSONL<br/>authoritative input"]
  source --> sqlite["SQLite"]
  sqlite --> agents["Agents"]
  sqlite --> cli["CLI"]
  source --> indexeddb["IndexedDB<br/>browser"]
  indexeddb --> views["Dashboard views"]
  source --> postgres["Postgres<br/>hosted dashboard sources"]
  postgres --> go["Go HTTP(S) query server"]
  redis["Redis<br/>operational state"] --> go
  go --> views
```

The SQLite and IndexedDB projections SHALL be independently reconstructable
from authoritative external inputs. Neither projection SHALL become the source
for the other. Publishing SQLite MAY support headless consumers, but browser
ingestion SHALL continue to use the published JSONL and inventory inputs.

The deployed dashboard payload SHALL publish only compacted normalized
run-information and run-record JSONL shards for browser ingestion. Raw Activity
JSONL MAY remain in the private Activity cache for audit and rebuild operations,
but MUST NOT be copied into the dashboard artifact or listed in its deployed
payload manifest. The browser MUST fail closed when compacted run-information
shards are absent; it MUST NOT fall back to raw Activity JSONL.
A phase that has no records SHALL still publish exactly one consolidated shard
containing only its metadata header (`records: 0`), so a collection that
observed no agentic workflow runs is explicit. Such a header-only shard
satisfies the run-information requirement above; its absence does not.

Published phase shards SHALL be consolidated across source shards before
publication. A canonical record observed by more than one Activity source shard
SHALL be published exactly once, keyed by `(collection, id)`, applying the same
precedence as canonical ingestion: discovery owns repository and workflow
inventory fields, and every other collection is last-observation-wins. Per-shard
normalization output MAY be retained as an incremental cache, but that cache
SHALL live outside the published phase directories and MUST NOT be copied into
the dashboard artifact or listed in the deployed payload manifest.

Browser Activity ingestion MUST preserve existing workflow inventory metadata,
including worker roles and campaign membership, when an incoming structural
record omits it. Outcome projection MUST resolve campaign membership through
the producing run's workflow and canonical campaign collection, independently
of the repository receiving the issue. Campaign Reports MUST remain available
after incremental imports whose unchanged inventory is skipped.

Consolidated shards SHALL be partitioned deterministically so that unchanged
history remains byte-identical between publications. Structural collections
SHALL occupy a single leading partition that sorts before time-based partitions,
preserving run-information-before-run-record ingestion order. Because the
browser's skip receipt is keyed on payload content hash rather than shard name,
a partition whose records are unchanged MUST retain its content hash so the
browser reuses its cached ingestion instead of redownloading the shard.
Within each time partition, publishers SHALL serialize collections in canonical
collection order and records in canonical ID order after resolving observation
precedence. Reordering non-conflicting source observations MUST NOT change the
published partition's bytes. Published normalized shards SHOULD be bounded to
1 MiB, including the metadata header and UTF-8 encoding. A record that exceeds
the budget by itself MUST remain intact in a dedicated shard; publishers MUST
NOT truncate or split canonical records to satisfy the byte budget.

The local SQLite projection and browser IndexedDB projection SHALL use the same
adapters, identities, normalization, object-store definitions, and relationship
validation. The local SQLite file implements the IndexedDB subset used by the
canonical storage API. It additionally mirrors Campaign and the six experiment-evidence
stores into queryable relational tables within the same write transaction;
these mirrors are derived from canonical records, not an independent ingestion
or acquisition path. It MAY use a different retention window when explicitly
created as a historical archive.

The `gh-aw-cao.dashboard-sql-export` contract is a separate, static source
interchange. Its producer-owned relational tables or views SHALL be serialized
to JSON and adapted before canonical normalization. The SQL export contract
MUST NOT be confused with the local SQLite projection.

IndexedDB and the Activity SQLite database SHALL retain all available canonical
Repository, Workflow, and Run summaries. IndexedDB SHALL retain detailed
run-linked records for seven days and Operational Value records for 365 days;
the Activity SQLite database SHALL retain run-linked detail for seven days by
default, while retaining Run records for 30 days and Operational Value records
for at least 365 days by default. Shortening run or detail retention MUST NOT
shorten this compact observation history. Explicitly longer historical SQLite
run windows SHALL also preserve the corresponding longer outcome history.
Explicit per-store historical retention windows MAY override these defaults.
Expiring run-owned records MUST NOT remove their retained Run or the Run's
structural parents.

## 5.1 Implemented storage profile

The implementation profile defined by this specification is:

| Layer | Version | Physical structure |
| --- | ---: | --- |
| Canonical model | 35 | Campaign, Repository, Workflow, Run, Domain, Tool, Skill, Friction, Audit, Issue, Operational Value, Operational Value Evidence, Workflow Value Collection, Marketplace Package, Experiment, Experiment Assignment, Grader, Grader Observation, Eval, and Eval Observation records |
| Browser IndexedDB | 46 | Twenty canonical entity stores, `transactions`, disposable `storageShards` accounting, and three separate daily run-count projection stores |
| Local SQLite projection | IndexedDB 46 | `__idb_databases`, `__idb_stores`, `__idb_indexes`, and `__idb_records` for the same logical stores, plus a `campaigns` table and six transactional relational evidence mirrors without duplicate record documents |
| Go server Postgres sources | Canonical model 19 | Fresh TypeSpec-defined entity and input tables with query-required native columns, compact presence bits, entity-owned relational child values, and transactional diagnostics/revision state; no stored JSON documents or duplicate scalar row formats |
| Static SQL export | 3 | Versioned JSON interchange produced from upstream SQL tables or views |

### 5.1.1 Independent statistical run projection

`statistical-runs` is a separate, bounded statistical query layer over the current
canonical snapshot. It SHALL NOT replace `runs`, change existing exact Run or
linked-detail retention, evict evidence, or create daily aggregates. This is the
incremental sampling and PostgreSQL bundle-persistence portion of
[the three-tier proposal](../adr/three-tier-operational-storage.md), not an
implementation of the entire proposed three-tier architecture or online reservoir.
PostgreSQL's independently enforced capacity boundary is specified in §37;
additive daily contributions are specified in §72.2.

Selection SHALL execute inside the canonical worker/storage query boundary, or
PostgreSQL's canonical publication transaction, before consumer filters, joins,
grouping, or pagination. All consumers of a snapshot and sampling policy SHALL use the
same frame. UI code SHALL NOT reconstruct a sample from view rows.

The frame SHALL use a recorded logical UTC watermark, never query wall-clock
time. A recorded Run-source `as-of` takes precedence. Without it, the greatest
valid recorded Run `startedAt`, `createdAt`, `completedAt`, or `updatedAt` MAY define a
`canonical-run-max` watermark; the frame SHALL label that policy explicitly.
Neither import time nor query time is a fallback. Without either recorded clock,
the source SHALL be unavailable rather than an exact empty result.
Eligible records are terminal canonical Runs with a valid execution
timestamp in `[watermark - seven days, watermark)`. Pending, undated, and
unreconciled conflicting identities SHALL NOT become completed sample members.
Canonical Run identities, not attempts or mutable metrics, determine seeded,
versioned SHA-256 priorities. Strata are execution UTC date and outcome.
Selection SHALL use deterministic proportional largest-remainder allocation,
with a bounded ten-percent failure reserve and stable identity tie-breaking.
Statistical capacities are independent: PostgreSQL 64,000; SQLite 8,000;
IndexedDB 16,000. Explicit hot-identity exclusions MAY narrow the frame; this
increment leaves the exact layer unchanged and does not infer a hot census.

Candidate frames are bounded to 200,000 input rows and 256 MiB of serialized
input, including malformed identities. Bottom-k priority retention SHALL remain
bounded by the allocated sample capacity; browser priority hashing SHALL use
bounded batches rather than one promise per population member. Browser stores
needed only for sampling SHALL be read in bounded batches within one readonly
snapshot, with a shared byte budget. Exceeding sampling input limits SHALL mark
statistical sources unavailable without suppressing independent exact queries.
Database failures and caller cancellation SHALL NOT be disguised as empty samples.

Sampling diagnostics SHALL be disabled by default and contain only fixed event
labels, stages, counts, timings, and validity indicators, never identities,
payloads, seeds, or arbitrary error text. Enable browser summaries with
`?debug=data:sampling` (source-loading diagnostics use `?debug=database`).
Go backends use `DEBUG=cao:postgresx:sampling` or `DEBUG=cao:sqlitex:sampling`;
native CLI summaries use `DEBUG=cao:cli` and remain off JSON stdout.

The source SHALL retain ordinary Run fields and expose its sampling frame,
watermark, covered interval, candidate and selected counts, per-stratum
populations/allocations, and weight validity. Inclusion probability is `k/N`
and expansion weight is `N/k` only for a complete, frozen, valid frame of
available canonical evidence. This does not attest complete upstream collection.
The versioned frame identity SHALL cover every eligible canonical identity and
its stratum, including unselected identities and strata allocated zero records.
Identity ordering SHALL match canonical IndexedDB string ordering. Identical
replays and input ordering SHALL NOT change that identity; changing the candidate
population SHALL, even when selected identities, counts, and cutoffs stay equal.
Selection-priority versioning SHALL remain independent of frame-format versioning.
Each selected Run and its sampled linked observations SHALL carry
`statistical-frame-id`, matching the frame metadata, so probabilities and weights
remain bound to their population after row projection or filtering. This
identifies the sampling design, not a revision of mutable Run measurements.
Missing or conflicting evidence SHALL invalidate unsupported weights; strata
allocated zero records SHALL preclude a full-population estimate. There is no
expiration-without-refill probability claim: complete frames are reconstructed
from canonical snapshots rather than maintained as an online reservoir.

PostgreSQL SHALL atomically publish the frame, membership, strata, source
quality, selected Runs, all nine admitted Run-linked collections, and their
structural parent closure in private typed `cao_sample_*` tables. These tables
SHALL NOT be canonical ingestion inputs or daily retention partitions.
Statistical query dependencies SHALL read these frozen copies, including their
parent context; exact dependencies in the same plan SHALL continue to read
canonical tables. A manifest or membership cache joined to mutable canonical
detail does not constitute persisted bundles.

The persisted frame SHALL expose `storage-mode: persisted-bundles`,
`source-data-revision`, and `source-revision`. It describes its recorded
publication watermark and evidence revision, not the latest incremental state.
Incremental writes and partition maintenance SHALL NOT mutate or independently
expire sampled children, recompute weights, or refill membership. A subsequent
canonical revision SHALL make the frozen source stale, without changing its
historical weights. Only complete canonical snapshot publication SHALL replace
the frame and bundles, rebuilding the seven-day window at that snapshot's
recorded watermark. This is bounded snapshot persistence, not a continuously
maintained reservoir or an extension of exact retention.

Bundle admission SHALL be all-or-nothing and bounded to 200,000 records per
collection, 500,000 evidence/context records in total, and 256 MiB of expanded
typed values plus estimated membership bytes and a 1 MiB metadata reserve.
The byte envelope excludes PostgreSQL page/index overhead. Budget exhaustion
SHALL publish an unavailable statistical source with no retained bundle subset
or valid weights, while preserving independent exact publication. Database
errors, cancellation, or failed canonical validation SHALL roll back publication
and preserve the previous committed frame. Snapshot readers SHALL observe either
the previous complete publication or the next complete publication.

The Models & Agents sample-composition query consumes `statistical-runs`. Its
counts and chart shares SHALL describe the unweighted sample only, explicitly
not population totals or model shares; failure reserves can change composition.
Grouped sample-composition rows SHALL retain `statistical-frame-id`; consumer
filters, projections and aggregation SHALL retain unchanged `statistical-frame`
metadata even when no rows remain. A zero-allocation stratum contributes no
sample rows, not a measured zero for its population.
Exact usage summaries, AIC/token statistics, operational totals, failure
sequences, Run routes, and Operational Value queries SHALL retain their existing
sources. Additional migrations require their own acceptance/weighting review.

Conformance for this increment is declared in
[`tests/contracts/statistical-sampling.json`](../tests/contracts/statistical-sampling.json).
`npm run test:conformance:statistical-sampling` SHALL execute all declared suites:
Activity publication, IndexedDB worker and native Chromium, shared Go sampler
and CLI, SQLite query and live-backfill HTTP boundaries, real PostgreSQL bundle
lifecycle, and the bounded TLA+ model
[`StatisticalSampling.tla`](tla/StatisticalSampling.tla). It requires an isolated
`POSTGRES_URL` with schema-creation permission and `TLA2TOOLS_JAR`; missing tools,
skipped backends, omitted coverage tests, and incomplete model checks do not
constitute conformance. The model checks abstract single-stratum publication
safety, not the statistical quality of the SHA256 or allocation implementation.

Only the PostgreSQL profile persists independent bundles; browser and SQLite
projections cannot preserve detail already removed by canonical retention.
Persistence cannot recover detail missing at admission. No profile reduces
underlying canonical storage or shard download volume in this increment.

## 5.2 Go server profile

Go SHALL be the serving runtime for local SQLite, hosted PostgreSQL, and explicit
static Pages previews. The Node.js authoring bridge SHALL remain internal to
Copilot canvas development rather than provide a standalone serving mode. The
Go server data profile SHALL:

* ingest `inventory-sources.json`, `payload-hashes.json`, and compacted
  `gh-aw-logs-runs/*.jsonl` and `gh-aw-logs-records/*.jsonl` from a deployed
  dashboard artifact;
* verify every manifested shard hash and require run-information shards before
  activating a new generation;
* replace current Postgres sources, diagnostics, and revision atomically and
  preserve the prior committed state when ingestion fails, without persistent
  generations;
* initialize the fresh physical schema defined by `server/spec/storage.tsp`
  and emitted as `server/internal/postgresx/schema.sql`; preserve only fields
  consumed by declared queries plus identity/storage keys, use native SQL
  scalar types and relational child values, and never persist JSON/JSONB or
  serialized canonical row documents;
* provide no legacy conversion, backfill, old-layout import, or dual-format
  canonical storage path; incompatible database layouts require a fresh
  database rebuilt from authoritative deployed inputs;
* partition Runs and their run-owned records into matching UTC calendar days;
  owner maintenance SHALL provision retained history, the current day, and 28
  upcoming days, and expire only whole days ending at or before each configured
  retention cutoff, dropping linked partitions before their Runs; runtime
  SHALL validate the provisioned window without DDL, and weekly layouts SHALL
  require fresh canonical storage without removing operational audit history;
* serve the built dashboard and its query API over HTTP on loopback by default,
  or HTTPS only when the operator provides a certificate and key;
* keep Postgres and Redis credentials exclusively in the Go process; use Redis
  for operational state only;
* validate Dashboard Language before executing proven equivalent, bounded,
  parameterized SQL plans in a repeatable-read Postgres transaction; reject
  unsupported shapes with a fail-closed error instead of evaluating them in a
  Go row evaluator, which the hosted path no longer retains;
* keep active browser views subscribed to revision changes and return fresh,
  bounded query payloads after successful ingestion.

The default local profile is loopback-only. The separate hosted profile requires
GitHub OAuth and explicit organization/team authorization; neither profile
grants database access to clients.

Local SQLite serving outside the internal canvas MAY refresh recent GitHub
evidence using the operator's local token. The deployed snapshot MUST remain
attached read-only through native SQLite `ATTACH DATABASE`; a separate local
delta SHALL hold upserts and deletion markers, without copying historical
records. Requests MUST use query-only transactions over the merged view.
The delta MUST be bound to the snapshot's canonical content revision and
incompatible bases MUST fail closed without deleting local evidence.
Refresh scope MUST come exclusively from repositories in that snapshot, never
token reach or expanded control-plane authority. Collection SHALL reuse the
Activity pipeline and canonical ingestion, including winning-attempt and
run-linked replacement semantics. Publication MUST atomically activate a
successfully ingested staged local delta, reject concurrent stale writers, and
notify revision subscribers; incomplete repository collection and failed
ingestion MUST preserve previously committed evidence and remain explicit.
Credentials MUST NOT be stored in SQLite or exposed to clients. Backfill MUST
have bounded scope, lookback, run count, and execution deadlines, be canceled
and joined on shutdown, and support offline serving of both snapshot and local
data without GitHub requests. Local refresh MUST NOT change hosted PostgreSQL,
OAuth, webhook, collection-authority, or internal canvas contracts.

The optional hosted OIDC admission gate MAY publish early Run evidence only
after verified identity, policy approval and durable decision resolution.
Successful publication SHALL insert a sparse canonical `runs` row with
`status: admitted` and `admissionStatus: admitted`, using the existing
repository-scoped GitHub run ID and normalized workflow relationship.
Blocked, revoked, malformed or unauthenticated attempts SHALL create no Run
or parent rows. Missing repository/workflow identity parents MAY be inserted
sparsely; existing inventory identities SHALL be reused. The observation
timestamp is `updatedAt`, not a fabricated workflow start or creation time.
No conclusion, completion, actor, allowance or usage SHALL be synthesized.
Canonical attempts outside the BIGINT range SHALL remain unknown rather than
truncate; exact signed invocation identity remains in the independent decision
key and is not reconstructed from the projection.

Run publication SHALL be atomic with its missing parents and revision update,
idempotent across retries, and serialized with canonical replacement. Pending
admitted rows and required parents SHALL survive replacement snapshots that
have not observed their run yet; an incoming canonical row with that Run ID
SHALL take precedence. Late admission retries SHALL NOT downgrade collected
evidence or refresh the admission observation time. Source quality containing
these sparse rows SHALL remain partial. Admission is not proof of actual
execution, completion, outcome or spend. Projection failure SHALL return a
generic transient response without discarding the durable admission decision.

The separately authenticated gh-aw `conclusion_admission` callback
(`POST /api/conclusion`) SHALL populate the canonical database from its bounded
compact-session evidence before acknowledgement, not merely retain an operational
receipt. Verified invocation identity SHALL bind the existing Run and its parents.
Available Run usage/engine/model facts, native Audit events and individually
identified grader definitions/observations SHALL use the existing typed sources.
Compaction coverage and source quality SHALL explicitly remain partial; full
sessions, tools/messages omitted by the compact profile, unavailable metrics and
GitHub final workflow outcomes SHALL NOT be inferred or automatically hydrated.
Untimed evidence SHALL carry first-observation provenance, not an invented clock.
Explicit numeric zero SHALL remain distinct from unknown. Usage SHALL follow
the canonical session adapter's per-phase/per-dimension preference for measured
reports over firewall copies and the latest session snapshot; cumulative and
per-record AIC SHALL NOT be added together or treated as settled billing.

Conclusion publication SHALL atomically update canonical relationships, Run-owned
facts, partial quality and revision under the same namespace lock as replacement.
An existing API-only Run SHALL retain known facts while gaining absent compact
facts. Unrelated or API-only replacement SHALL preserve/enrich compact evidence;
collected session evidence at the winning attempt SHALL supersede it. Lower
attempts SHALL NOT replace newer evidence or mix their Run-owned records.
The permanent operational receipt SHALL record canonical completion only after
publication succeeds. Pending identical retries SHALL repair failed publication
idempotently, including failure between canonical commit and completion marking;
completed retries SHALL NOT duplicate rows/revisions or refresh observation time.
Conflicts SHALL NOT mutate either store. Compact request retention SHALL remain
separate from ordinary canonical evidence retention; no public raw-session
endpoint, additional credential grant or financial mutation is implied.

Hosted container, Azure Functions, and external-listener deployments SHALL
serve the built dashboard HTML and assets through the complete Go handler on
the same origin as OAuth, query APIs, and revision events. They SHALL NOT
require a separately hosted Pages or static frontend. Hosted startup SHALL
reject a missing, unreadable, or empty built `index.html`. Dashboard entry
responses SHALL select the `server-http` backend and SHALL NOT be cached as
static HTML. Hosted service failures SHALL NOT fall back to browser shard
ingestion, IndexedDB query execution, or the separate Pages deployment.

`gh-aw-cao-dashboard-data` is the logical database name. Every implemented
store uses `id` as its key path. The implemented secondary indexes are:

| Store | Indexes |
| --- | --- |
| `campaigns` | `bySlug -> slug` |
| `repositories` | none |
| `workflows` | `byRepository -> repositoryId`, `byCampaign -> campaignId` |
| `runs` | `byRepository -> repositoryId`, `byWorkflow -> workflowId`, `byConclusion -> conclusion`, `byEvent -> event`, `byEventConclusion -> [event, conclusion]` |
| `domains` | `byRun -> runId`, `byQuerySummary -> _queryKeys.byQuerySummary`, `byQueryDomain -> _queryKeys.byQueryDomain` |
| `tools` | `byRun -> runId`, `byQuerySummary -> _queryKeys.byQuerySummary`, `byQueryMcpIdentity -> _queryKeys.byQueryMcpIdentity`, `byTypeStatusRun -> [type, status, runId]`, `byTypeStatusRunSummary -> [type, status, runId, summary]` |
| `audits` | `byRun -> runId`, `byQuerySummary -> _queryKeys.byQuerySummary`, `byTypeStatusRun -> [type, status, runId]`, `byTypeStatusRunSummary -> [type, status, runId, summary]` |
| `issues` | `byRun -> runId`, `byQuerySummary -> _queryKeys.byQuerySummary` |
| `skills`, `friction` | `byRun -> runId` |
| `operationalValues` | `byRepository -> repositoryId`, `byValue -> valueId` |
| `operationalValueEvidence`, `workflowValueCollections` | `byRepository -> repositoryId`, `byWorkflow -> workflowId` |
| `marketplacePackages` | `byRegistry -> registryId`, `byRepository -> repository` |
| `experiments`, `graders`, `evals` | `byWorkflow -> workflowId` |
| `experimentAssignments` | `byRun -> runId`, `byExperiment -> experimentId` |
| `graderObservations` | `byRun -> runId`, `byGrader -> graderId` |
| `evalObservations` | `byRun -> runId`, `byEval -> evalId` |
| `transactions` | `byCreatedAt -> createdAt` |
| `storageShards` | none |
| `dailyRunCounts` | none |
| `runCountContributions` | none |
| `dailyRunCountState` | none |

Time-bounded stores additionally index `byStorageTimestamp -> [_storage.timestamp, id]`.
Run-linked stores additionally index `byStorageRun -> [runId, id]`
for bounded subtree deletion. Experiment assignments, grader
observations, and eval observations additionally index `byAudit -> auditId`
for scoped curation reference checks.

Canonical writes SHALL attach disposable `_storage` metadata identifying the
store's UTC-day shard, the exact retention timestamp, and a conservative byte
estimate. Structural records use a non-expiring shard. Canonical reads SHALL
remove `_storage`; neither it nor `storageShards` is a query source or evidence.
Shard counts, byte totals, dirty-audit markers, and global totals SHALL update
atomically with record inserts, overwrites, moves, and deletes. Routine maintenance
SHALL use these totals and indexed, at-most-1,000-record batches, never full entity
scans or reserialization of unaffected records. Only explicit relationship repair
or CLI legacy audit repair after unchanged ingestion with a missing curation receipt
MAY rebuild shard accounting with bounded scans.

The `_queryKeys` fields are disposable physical index projections, not canonical
evidence. They encode nullable raw `summary`, `domain`, or
`[source, type, mcpServer, mcpTool]` components without parsing computed labels.
Canonical write APIs SHALL prepare these keys after pruning redundant fields;
canonical read APIs SHALL remove them. Equivalent native query plans MAY
evaluate declared row-local expressions on distinct indexed keys, read only
matching observations, and obtain grouped event counts from indexes before
materializing bounded results. An unchanged single-field string equality MAY
seek its encoded tuple directly only after verifying index coverage and excluding
non-string tuples; nullable, coercible, structured, or computed values retain
normal dictionary evaluation. The Domain runs table SHALL consume its declared
`domain-run-records` query and return only the paginated view alias, not an
additional unbounded `domains` collection. Such plans MUST preserve observation grain,
run attempts, null and missing values, ordering, provenance, pagination, and
unavailable inputs. Unsupported plans retain normal fail-closed execution, and
an oversized selected scope MUST NOT be truncated or have its limits raised.

The three daily run-count stores SHALL implement only the separate projection
in §72.2, not additional canonical entities or general computation results.
`dailyRunCounts` SHALL hold UTC execution-start-day/Repository/Workflow buckets,
with `id` encoding `[day, repositoryId, workflowId]`; namespace is implicit in
the scoped database. `runCountContributions` SHALL use the logical Run ID as
`id` and retain compact count-relevant Run facts, the reconciled contribution,
and replay/conflict guards independently of detail. `dailyRunCountState` SHALL
use `id: "daily-run-counts"` for readiness, coverage, backfill checkpoint,
excluded/conflicted counts, byte accounting, and monotonic retention state.
All three stores use `id` keys without secondary indexes.

Physical upgrades from version 44 or 45 with an existing `runs` store SHALL
create only missing stores and preserve existing records and projection state.
A newly created daily state SHALL start `backfilling` with coverage
`retained-v44` or `retained-v45`. Backfill SHALL process at most 250 retained
Runs per transaction, atomically checkpointing contributions and progress;
completion SHALL set status `ready` without claiming complete historical
coverage. It SHALL NOT rewrite canonical detail or recover already-evicted
history. New databases SHALL initialize daily state as `ready` with `admitted`
coverage. Other older databases SHALL rebuild every store from authoritative
inputs because their browser state is disposable.
The SQLite-backed implementation SHALL preserve exactly the same
logical database version, store names, key paths, indexes, and record values.
The metadata tables are an emulation detail and MUST NOT be presented as
canonical domain tables.

Every canonical storage implementation MUST persist Campaign as a first-class
entity, including campaigns with no Workflows or Runs. IndexedDB SHALL use the
`campaigns` object store; SQLite and Postgres SHALL expose a physical `campaigns`
table. The key SHALL be `id` in IndexedDB, `(database_name, id)` in SQLite, and
`(namespace, id)` in Postgres. Campaign data MUST NOT be reconstructed from
Workflow classifications or stored only as incidental Workflow fields.

The local SQLite `campaigns` table SHALL mirror the canonical object store
transactionally. It SHALL expose `slug`, `name`, `description`, `icon`, `mode`,
`enabled`, `experimental`, `version`, `current_version`, `min_version`,
`update_state`, `readme_path`, `readme`, `worker_count`, `inventory_warnings`,
`max_repositories`, `rollout_percent`, `ai_credit_allowance`,
`monthly_ai_credit_budget`, `campaign_link`, and `intelligence_declaration`.
Boolean columns SHALL use SQLite integers; numeric columns SHALL use INTEGER
or REAL as appropriate. Structured links and declarations SHALL use JSON TEXT.
`observed_at` and JSON TEXT `provenance` SHALL preserve observation metadata.
The complete canonical observation SHALL be stored only in `__idb_records.value`;
relational mirrors MUST NOT duplicate it in `record_json`. Partial records MAY
omit observation metadata; the
mirror MUST NOT fabricate it. A `(database_name, slug)` index SHALL support
the same campaign lookup as IndexedDB's `bySlug` index.

Creating or rebuilding a SQLite mirror for an existing logical database SHALL
populate it from that database's canonical Campaign records without changing their IDs,
values, or logical schema version. Inserts, updates, replacement, deletion,
and rollback SHALL keep the mirror consistent with the object store. Store or
database deletion SHALL remove only the corresponding database's mirror rows.
Campaigns SHALL remain exempt from run-detail retention.
The SQLite `repair-database` command SHALL diagnose a missing Campaign table or required column
and restore the table from retained canonical records inside its backed-up
repair transaction.

The Postgres table SHALL use the query-required native fields declared in
`server/spec/storage.tsp`, with presence bits preserving missing versus null
values and the existing fresh-schema contract. Its generated DDL and Go
bindings MUST include `campaigns` and `$campaigns`, respectively. Storage
conformance tests MUST verify Campaign persistence, stable identity on refresh,
and deletion in each implementation.

The local SQLite evidence mirrors are `experiments`,
`experiment_assignments`, `graders`, `grader_observations`, `evals`, and
`eval_observations`, each keyed by `(database_name, id)`. Definitions expose
`workflow_id`, `name`, and `first_observed_at`/`last_observed_at`; assignments
expose `run_id`, `experiment_id`, and `variant`; grader observations expose
`run_id`, `grader_id`, `value`, `status`, and optional `experiment_id`/`variant`,
`audit_id`, and `evaluator_digest`; eval observations expose `run_id`,
`eval_id`, `eval_result`, `status`, and optional `experiment_id`/`variant`,
`audit_id`, and `timestamp`. Requested and resolved models SHALL be obtained from
the owning Run in query projections, not copied into Eval observations.
Optional inclusion and exclusion fields,
`observed_at` and `provenance` preserve observation metadata without requiring
queries to parse Audit JSON. The full record is available by joining
`__idb_records` on `database_name`, the canonical `store_name`, and
`record_key = json_quote(id)`; mirrors MUST NOT store another complete JSON copy.
Relationship-key indexes follow
the corresponding IndexedDB indexes. Store deletion and retention SHALL remove
mirror rows in the same SQLite transaction.
Outdated mirror layouts SHALL be rebuilt transactionally from
`__idb_records`, preserving every logical database's scope, rather than
converting or trusting the obsolete mirror's stored document. Failed rebuilds
MUST roll back both DDL and data changes. The SQLite `repair-database` command SHALL diagnose
missing or unexpected columns in all seven mirrors and use its backed-up
repair transaction to restore the current layout.

## 5.2 Completeness and archives

The scheduled Activity collection SHALL be treated as a rolling operational
snapshot, not as a complete historical archive. Completeness SHALL be reported
separately for:

* run-summary coverage from `workflow_runs` envelopes;
* enriched artifact coverage from `run` envelopes; and
* the requested historical time range.

Repeated source observations are expected when cached JSONL is refreshed.
Ingestion SHALL report repeated raw and enriched observations, deduplicate them
by the canonical identities defined in this specification, and MUST NOT create
duplicate canonical records.

A full-detail local archive SHALL use a separate SQLite file with a retention
window that covers the requested history. A later rolling Activity refresh MUST
NOT silently shorten that archive to the dashboard's default detail window.

Expired or unavailable GitHub Actions artifacts SHALL be reported as missing
enrichment. Their run summaries MAY still be present and MUST NOT be described
as fully enriched runs.

## 5.3 Maintenance inventory

Campaign inventory inputs MAY report `campaign-version`,
`campaign-current-version`, and `campaign-update-state` for an installed gh-aw
starter campaign. `campaign-update-state` MUST be `update-available`,
`up-to-date`, or `unknown`; missing or incomparable version evidence MUST
normalize to `unknown`. The campaign slug remains the stable identity, and a
refresh MUST enrich the existing Campaign record rather than create a
version-specific Campaign.

Campaign inventory inputs MAY also report a normalized
`campaign-intelligence-declaration` object from the Campaign-owned
`.github/cao/intelligence/<campaign>.json` materialized declaration. The canonical
Campaign record MUST preserve that envelope as `intelligenceDeclaration`
without reinterpreting Campaign descriptions, READMEs, workflow execution, or
policy as semantic declarations. The database projection MUST return the same
object without field loss. A malformed, mismatched, unsupported, or conflicting
declaration MUST fail inventory construction closed. The declaration is
descriptive evidence and MUST NOT grant rollout, target, credential, tool, or
write authority.

Workflow inventory inputs SHALL continue to report `gh-aw-version`,
`gh-aw-current-version`, and `gh-aw-update-state` as compiler evidence.
Repository maintenance projections MUST group that evidence by canonical
Repository identity. A repository requires an upgrade when at least one
workflow reports `update-available`; mixed workflow versions MUST remain
visible and MUST NOT be collapsed to a fabricated single version.

The SQLite Campaign projection and the IndexedDB `campaigns` object store MUST
preserve the same three campaign-maintenance fields with identical missing-data
semantics. Both projections remain disposable and reconstructable from campaign
inventory inputs. Incompatible canonical schema migrations MUST rebuild these
derived records; compatible additive upgrades SHALL follow §5.1. A failed
refresh MUST NOT publish a complete maintenance query payload. Campaign
and repository maintenance actions MUST use these canonical query results and
MUST NOT inspect upstream manifests or browser storage directly.

## 5.4 Activity acquisition and source roles

Activity acquisition SHALL use one runtime observation path:

```text
cao.json repository scope
  -> one gh aw logs --repo call per resolved repository
  -> repository-specific --cached-jsonl wildcard shards
  -> canonical JSONL ingestion
  -> SQLite and IndexedDB projections
  -> Dashboard Language queries
```

`cao.json` and its resolved control settings SHALL define collection authority
and repository scope. They MUST NOT be treated as evidence that a Workflow or
Run exists, executed, or executed in a campaign target repository.

Inventory inputs SHALL provide static declarations and maintenance evidence:
Campaign configuration, enrolled Repository metadata, declared control-plane
Workflow metadata, and bounded GitHub Actions workflow registry metadata for
each resolved Repository. Registry metadata MAY enrich Workflow path, display
name, active or disabled state, native identifier, and link without an observed
Run. Repository-owned registry Workflows MUST remain standalone and MUST NOT
inherit Campaign membership, worker status, admission, or rollout authority from
repository enrollment. Cached gh-aw JSONL SHALL provide observed runtime
evidence: Repository, Workflow, Run, Domain, Tool, Audit, and Issue observations.
A declared or registered Workflow MAY exist without an observed Run. Queries MUST
preserve that distinction and registry coverage metadata rather than fabricate
runtime or inventory completeness. Deleted registry entries SHALL NOT appear as
current Workflows, and local compilation SHALL NOT convert missing or unknown
registry evidence into an active runtime state.

Each resolved Repository SHALL have an independent `--cached-jsonl` wildcard
prefix in the shared shard directory. Repeated collection SHALL reuse known
shards, and canonical ingestion SHALL skip a shard whose content hash already
exists in the Transaction ledger. Repository collection MAY be serial to bound
concurrent GitHub API pressure and reuse shared analysis state. The consolidated
The `gh-aw-logs-shards/` directory SHALL retain the authoritative collection
transport. Dashboard publication SHOULD additionally provide
`gh-aw-logs-normalized/` JSON payloads containing content-addressed canonical
batches generated from those shards. Browsers SHALL prefer those payloads to
avoid source adaptation and normalization, use their `payload-hashes.json`
identities to skip unchanged downloads and imports, and fall back to the source
JSONL shards when normalized payloads are unavailable.

### 5.4.1 Canonical join contract

#### 5.4.1.1 Campaign resource navigation projections

Campaign resource pages SHALL resolve one campaign slug from the active route and
apply it as an equality predicate inside the dashboard query worker. Generated
issue and pull-request views SHALL select retained Outcome observations by
`outcome-category`; workflow-run views SHALL join canonical Run and Workflow
records through Workflow identity; repository views SHALL group the canonical
Repositories reached through campaign-classified Workflows. Missing campaign
relationships MUST produce an honest empty or unavailable result and MUST NOT
fall back to unscoped records.

The local SQLite projection and browser IndexedDB projection SHALL expose
equivalent Campaign-to-Workflow, Workflow-to-Run, and Workflow-to-Repository
relationships to these queries. Campaign resource navigation is presentation
configuration, not canonical operational evidence, and MUST NOT be persisted as
mutable browser state. Both projections remain disposable and reconstructable;
no schema migration is required for navigation-only changes.

The cached JSONL source does not expose immutable Repository and Workflow IDs
for every envelope. Until it does, Repository identity SHALL use normalized
`OWNER/REPOSITORY`; Workflow identity SHALL be scoped to that Repository and
use authoritative workflow path when available, otherwise a source-namespaced
workflow name. Run identity SHALL use normalized execution-repository
coordinates plus GitHub run ID. `attempt` SHALL remain a required observation
field used for source deduplication and diagnostics, but it SHALL NOT be part of
the canonical Run ID.

The mandatory execution joins are:

```text
Workflow.repositoryId -> Repository.id
Run.repositoryId      -> Repository.id
Run.workflowId        -> Workflow.id
Domain.runId           -> Run.id
Tool.runId             -> Run.id
Audit.runId            -> Run.id
Issue.runId            -> Run.id
```

`Run.repositoryId` SHALL identify the repository where GitHub Actions executed
the run. Campaign targets, dispatch envelopes, safe-output destinations, and
other repository-shaped payload fields MUST NOT override it. An inventory
Workflow and a runtime Workflow SHALL converge only when their canonical
Repository and workflow-path identities match.

Dashboard source projections MAY expose `organization` and `repository` as
denormalized join keys. Dashboard Language queries SHALL perform all selection,
grouping, aggregation, and joins in the data Web Worker. The Repositories view
SHALL begin with Repository inventory, aggregate Workflow and Run facts by
`organization` and `repository`, and left-join those results so an enrolled
Repository remains visible when it has no retained runtime observations.

## 5.5 Token-optimization evidence contract

The `optimization-token-optimizer` operation has one bounded task: evaluate one
assigned, evidence-complete token-efficiency opportunity for one target
Repository and Workflow, then measure whether an accepted intervention lowers
AI Credit (AIC) per comparable accepted outcome without reducing reliability or
outcome quality.

Activation requires a complete assignment, an authorized target Repository, an
authoritative Workflow identity, a frozen evidence window, an assignment Run,
and an experiment identity. The required effect is one immutable opportunity
observation and, when maintainers accept a recommendation, one intervention
whose experiment can mature into a comparison. A complete analysis with no
defensible opportunity is a no-op, not a zero-value intervention. Success is a
matured comparison with positive verified attainment. Uncertainty remains
explicit when evidence is incomplete, incomparable, unmatured, or unavailable.

### 5.5.1 Authoritative sources and grain

Token-optimization evidence SHALL enter through the Activity publication and
the existing source-adapter boundary. An optimizer or dashboard MUST NOT
independently redownload the same run corpus when the Activity generation is
complete for its scope and window.

The current source authority for gh-aw runtime fields is
[`logs-jsonl.schema.json`](https://github.com/github/gh-aw/blob/main/schemas/logs-jsonl.schema.json).
Its schema revision SHALL be retained in provenance. The adapter SHALL apply the
following source contract:

| Evidence | Authoritative input and grain | Required handling |
| --- | --- | --- |
| Invocation AIC and raw token classes | API-proxy token-usage record collected through enriched gh-aw artifacts; one API invocation | Emit one invocation-grain usage observation. Never repeat its AIC across token-class rows. |
| Run/model usage aggregate | `run.token_usage_summary`, including `by_model`; one Run or Run-and-model aggregate | Preserve aggregate grain. It MUST NOT fabricate invocation rows. Prefer complete invocation evidence for invocation queries, otherwise expose aggregate-only completeness. Never add aggregate and invocation AIC together. |
| Turns and requests | `run.turns` and `run.token_usage_summary.total_requests`; one Run | Keep turns and model requests distinct. Absence is unknown, not zero. |
| Cache evidence | Invocation token-usage fields or `token_usage_summary` cache fields at their declared grain | Preserve cache-read and cache-write tokens separately. `cache_efficiency` is a source observation, not a replacement for either token class. |
| Tool activity | `run.mcp_tool_usage.tool_calls[]`, with the documented audit fallback; one tool call | Preserve server, tool, status, timestamp, sizes, and correlation identity. A configured tool inventory is separate static evidence and MUST NOT be inferred from observed calls. |
| Run reliability | Canonical Run status and conclusion from `workflow_runs` and enriched `run` envelopes; one Run attempt | Failure rate uses distinct completed attempts only. Missing conclusions do not enter either numerator or denominator. |
| Experiment assignment | `run.experiments.assignments`; one experiment assignment per Run | Preserve experiment name and variant exactly. Cumulative counts are diagnostics and MUST NOT create assignments. |
| Accepted target outcome | Safe-output lifecycle plus authoritative GitHub disposition or the accepted-evidence rule of the frozen evaluator; one durable target-workflow outcome | Creating a safe output does not establish acceptance. Accepted identity and disposition MUST be distinct from the producing Run. |
| Recommendation disposition | Safe-output lifecycle, explicit supersession relation, implementation Run or pull request, and authoritative GitHub disposition; one optimizer recommendation | Preserve `applied`, `superseded`, `outdated`, `duplicate`, `unapplied`, `failed-start`, or `rejected`. A generated issue, assignment attempt, or open state alone does not establish acceptance or implementation. |
| Optimization overhead | Invocation or non-overlapping Run-aggregate AIC for auditor, optimizer, verifier, and replacement recommendations attributable to one frozen opportunity and intervention lineage | Deduplicate by Run attempt, preserve cost grain, and exclude unrelated repositories, workflows, opportunities, and portfolio dispatches. |
| Outcome quality | Frozen grader or eval observation with evaluator digest; one outcome or stable opportunity | Compare only observations produced by the same definition and evaluator digest. Missing quality evidence is unknown. |
| Operational grader | Current gh-aw `operational-value` grader result; one ordered metric array per Run | Preserve metric IDs, order, native finite values or `null`, unit, and direction without normalization, clamping, replay, inferred maturity, or local baselines. This run-scoped evidence is distinct from campaign-defined, repository-scoped Operational Value records. |
| Workflow declaration | Workflow inventory at the exact reviewed source revision | Supply configured tools, model, trigger, budget, and campaign classification. Static declarations MUST NOT prove runtime use. |

Source provenance for every observation SHALL include collection scope, source
kind, source identifier, source schema revision, observed time, generation,
completeness, and freshness. A later source observation MAY enrich the same
canonical fact but MUST NOT erase a known value with an absent field.

### 5.5.2 Identities and relationships

The canonical opportunity identity SHALL encode the six fields frozen by the
token-optimization observation contract:

```text
token-opportunity:
  <percent-encoded targetRepo>:
  <percent-encoded workflowPath>:
  <evidenceWindowStart>:
  <evidenceWindowEnd>:
  <assignmentRunId>:
  <percent-encoded experimentId>
```

Percent encoding SHALL use uppercase RFC 3986 hexadecimal escapes over UTF-8.
Timestamps SHALL use normalized RFC 3339 UTC seconds. `targetRepo` SHALL be the
normalized `OWNER/REPOSITORY` coordinate until an immutable GitHub Repository ID
is published; `workflowPath` SHALL be the normalized path in that Repository.
Display names MUST NOT participate in identity. A control repository is
provenance, not target identity, so observations collected by multiple control
repositories converge only when all six identity fields match.

An intervention identity SHALL be
`token-intervention:<opportunity-id>:<percent-encoded intervention-id>`, where
`intervention-id` is the immutable safe-output identity or reviewed experiment
change identity. A comparison identity SHALL be
`token-comparison:<intervention-id>:<evaluator-digest>:<evidence-cutoff>`.
Experiment variants SHALL relate to the same intervention through the frozen
experiment identity and SHALL retain `control` or `optimized` as roles separate
from their producer-defined variant names.

The mandatory relationships are:

```text
Opportunity.targetRepositoryId -> Repository.id
Opportunity.targetWorkflowId   -> Workflow.id
Opportunity.assignmentRunId    -> Run.id
Opportunity.experimentId       -> Experiment.id
Intervention.opportunityId      -> Opportunity.id
Intervention.safeOutputId       -> Outcome.id, when published
Intervention.supersedesId       -> Intervention.id, when replacing an earlier recommendation
Intervention.supersededById     -> Intervention.id, inverse when known
Comparison.interventionId       -> Intervention.id
Comparison.operationalValueId   -> OperationalValue.observationId
Comparison.controlVariant       -> ExperimentAssignment.variant
Comparison.optimizedVariant     -> ExperimentAssignment.variant
```

Repeated collection of the same source observation SHALL be deduplicated before
normalization. Multiple source observations for one canonical opportunity SHALL
retain distinct provenance observation IDs while enriching one opportunity.
Two records with different frozen windows, assignment Runs, or experiments are
different opportunities even when they recommend the same change.

### 5.5.3 Measures, comparability, and evidence states

AIC is the primary cost measure. The canonical raw-token measures remain
`input-tokens`, `output-tokens`, `cache-read-tokens`, `cache-write-tokens`, and
`reasoning-tokens`. Provider conventions may overlap, so these fields MUST NOT
be summed into a synthesized total. Turns, requests, tool calls, duration, and
cache efficiency remain separate diagnostics.

Target-workflow outcomes and optimizer recommendations are different entities.
`accepted-target-outcome-count` is the denominator for target-workflow
efficiency. `recommendation-disposition` determines whether the proposed
intervention was actually applied. Superseded, outdated, duplicate, unapplied,
failed-start, and rejected recommendations MUST NOT count as accepted target
outcomes or successful interventions.

For one experiment variant:

```text
AIC per accepted outcome =
  sum of distinct attributable invocation AIC
  / count of distinct accepted outcome identities
```

When complete invocation evidence is unavailable, a producer MAY use distinct
Run-level `total_aic` values and SHALL mark the cost grain `run-aggregate`.
Invocation and aggregate AIC MUST NOT be mixed in one numerator. A zero or
missing accepted-outcome count, a non-positive baseline denominator, or
unattributable AIC makes the comparison unavailable rather than zero.

Control and optimized variants are comparable only when all of the following
hold:

1. both variants belong to the frozen experiment and target Workflow;
2. the workload comparison key and acceptance rule were declared before result
   evaluation and are identical for both variants;
3. both variants meet the frozen minimum comparable sample size;
4. each variant has at least one distinct accepted outcome;
5. AIC and completed-Run conclusion evidence is complete for every included Run;
6. outcome-quality evidence uses the same definition and evaluator digest; and
7. the later of fourteen days after assignment or the minimum-sample threshold
   has been reached without passing the evidence cutoff;
8. recommendation disposition is authoritative and implementation completion
   is known; and
9. optimization-overhead AIC is complete for distinct optimizer-family Run
   attempts attributable to the same opportunity and intervention lineage.

Reliability SHALL be the completed-Run failure rate for each variant and SHALL
remain separate from cost. Outcome quality SHALL retain the frozen grader or
eval value and SHALL remain separate from both reliability and cost. Workload
comparison keys MUST NOT contain prompt text, response text, tool arguments, or
raw repository content.

`evidence-state` SHALL use exactly:

| State | Meaning |
| --- | --- |
| `complete` | Required evidence is authoritative, comparable, and mature. |
| `incomplete` | A required observation within an otherwise accessible source is absent or partial. |
| `incomparable` | Both evidence sets exist but violate one or more frozen comparability rules. |
| `unmatured` | Valid evidence has not reached time or sample maturation. |
| `unavailable` | A required source, identity, or attribution cannot be accessed or established. |

Only `complete` evidence MAY produce gross or net realized savings. Every other state SHALL
produce null attainment and a non-sensitive missing reason. Complete comparable
evidence scores zero when AIC per accepted outcome does not decrease,
completed-Run failure rate increases, outcome quality decreases, the
recommendation was not applied, implementation did not complete, or net savings
are non-positive. Otherwise:

```text
gross realized savings AIC =
  max(
    baseline AIC per accepted outcome - optimized AIC per accepted outcome,
    0
  )
  * optimized accepted-outcome count

optimization overhead AIC =
  sum of distinct auditor, optimizer, verifier, and replacement-recommendation
  Run AIC attributable to this opportunity and intervention lineage

net realized savings AIC =
  gross realized savings AIC - optimization overhead AIC

verified net gain ratio =
  net realized savings AIC
    / (baseline AIC per accepted outcome
        * optimized accepted-outcome count)
```

`gross-realized-savings-aic` measures the non-negative counterfactual target
Workflow AIC avoided for the optimized variant's accepted output volume.
`optimization-overhead-aic` measures only optimizer-family work attributable to
the same frozen opportunity and intervention lineage. It includes superseded
replacement recommendations in that lineage, but excludes unrelated portfolio
discovery and recommendations for other targets. `net-realized-savings-aic` is
gross savings less that overhead and MAY be negative for diagnostics.

`verified-net-gain` is a token-optimization comparison diagnostic, not a gh-aw
operational-grader metric. It retains the native ratio produced by the formula
without clamping or rescaling. Gross, overhead, and net values are null unless evidence is complete. A non-applied
recommendation, failed implementation start, reliability or quality regression,
or non-positive net result records zero verified net gain. The underlying gross
and overhead measurements remain visible so zero does not hide optimizer cost.

### 5.5.4 Opportunity and intervention vocabulary

`opportunity-kind` SHALL use this initial closed vocabulary:

| Value | Evidence boundary |
| --- | --- |
| `avoidable-agent-invocation` | A deterministic gate can safely avoid an agent invocation or turn. |
| `deterministic-data-gathering` | Observed reasoning-loop work can move to deterministic preprocessing. |
| `unused-tool-schema` | Declared tool schema is unused across the complete observation window. |
| `unbounded-context-growth` | Measured context grows beyond a declared bounded-read or working-set expectation. |
| `poor-cache-utilization` | Comparable stable context has materially low observed cache reuse. |
| `blocked-tool-retry-loop` | Correlated denied or failed operations cause repeated attempts without progress. |
| `model-or-subagent-mismatch` | Observed task shape and quality evidence support a cheaper bounded execution tier. |
| `avoidable-trigger-frequency` | Equivalent scheduled or event work can be safely batched or skipped. |
| `duplicated-work-across-repositories` | Privacy-safe resource fingerprints establish equivalent repeated work across Workflow executions. |

An opportunity kind is an evidence-backed classification, not a conclusion
derived from high cost alone. The producer SHALL preserve the evidence rule and
confidence used for classification.

`intervention-state` SHALL use exactly `proposed`, `accepted`, `running`,
`verified`, `regressed`, `inconclusive`, or `rejected`. `proposed-savings-aic`
is an estimate attached to a proposal.

`recommendation-disposition` SHALL use exactly `applied`, `superseded`,
`outdated`, `duplicate`, `unapplied`, `failed-start`, or `rejected`. Disposition
does not replace lifecycle state: for example, an intervention MAY be terminal
and `rejected` because its recommendation disposition is `duplicate`.
Supersession SHALL use explicit `supersedes-intervention-id` and
`superseded-by-intervention-id` relations emitted by the safe-output producer;
dashboard code MUST NOT infer lineage by parsing titles, bodies, or comments.

`recommendation-churn-count` is the count of distinct terminal non-applied
recommendations in one opportunity/intervention lineage.
`recommendation-churn-rate` is that count divided by all distinct terminal
recommendations in the lineage and is null when the denominator is zero.
`gross-realized-savings-aic`, `optimization-overhead-aic`,
`net-realized-savings-aic`, and `verified-net-gain` require a complete matured
comparison. Queries and views MUST NOT combine, coalesce, or label proposed
savings as gross or net realized value.

### 5.5.5 Canonical projection, SQL, and IndexedDB parity

Token-optimization observations SHALL use compact canonical Audit records in the
optimizer Run rather than new source-shaped object stores:

```text
optimization.opportunity.observed
optimization.intervention.updated
optimization.comparison.observed
```

The Audit payload SHALL contain only the stable IDs, enums, numeric measures,
evidence state, missing reason, timestamps, and relationship IDs defined above.
Repository, Workflow, Run, Outcome, experiment assignment, usage, and
operational-value facts remain in their existing canonical domains. Dashboard
logical sources SHALL be materialized from these canonical facts in the data
Web Worker; views and components MUST NOT reconstruct relationships.

The IndexedDB Audit representation SHALL use camel-case fields:
`opportunityId`, `opportunityKind`, `interventionId`, `lifecycleObservationId`,
`previousInterventionState`, `interventionState`,
`previousRecommendationDisposition`, `recommendationDisposition`,
`safeOutputId`, `implementationChangeId`, `implementationRunIds`,
`optimizerRunAttempt`, `optimizerWorkflowPath`, `optimizerWorkflowName`, `acceptedAt`,
`implementationStartedAt`, `implementationCompletedAt`, `rejectedAt`,
`supersededAt`, `missingReason`, `supersedesInterventionId`,
`supersededByInterventionId`, `comparisonId`, `experimentId`, `evidenceState`,
`costGrain`, `proposedSavingsAic`, `grossRealizedSavingsAic`,
`optimizationOverheadAic`, `netRealizedSavingsAic`, `verifiedNetGain`,
`recommendationChurnCount`, `recommendationChurnRate`,
`baselineAicPerAcceptedOutcome`, `optimizedAicPerAcceptedOutcome`,
`acceptedTargetOutcomeCount`, `baselineFailureRate`, `optimizedFailureRate`,
`outcomeQualityPreserved`, and the relationship IDs applicable to that Audit.

The `gh-aw-cao.dashboard-sql-export` representation SHALL emit the same Audits
with equivalent snake-case columns. Each SQL-export row SHALL retain
`entity_kind=audit`, `source_id`, `observed_at`, `github_run_id`, `run_attempt`, and the
applicable `optimization_*` columns. SQL and IndexedDB adapters SHALL normalize
to byte-identical canonical IDs and equivalent units, nullability, enums,
relationships, and query results. SQL tables, SQL text, and database credentials
MUST NOT be shipped to the browser.

Browser IndexedDB remains disposable. Migration of any identity, enum, or
measure semantics in this section SHALL increment the canonical schema and
rebuild token-optimization projections from authoritative inputs. A failed
rebuild MUST NOT create a successful ingestion receipt or publish a compatible
derived projection. A historical SQLite archive MAY use longer retention but
MUST apply the same adapter and normalization rules.

Browser IndexedDB retains run-linked intervention evidence, including active
interventions and compact comparisons, only within its seven-day detail window.
Longer-lived intervention tracking and historical backfills belong in a separate
SQLite archive, not browser IndexedDB.

Dashboard Language SHALL expose `token-efficiency-opportunities`,
`token-efficiency-interventions`, and `token-efficiency-comparisons`. All
selection, filtering, joins, workload grouping, aggregation, calculation,
ranking, ordering, and pagination SHALL execute as declarative queries in the
data Web Worker with an explicit abort-scoped subscription. JavaScript-derived
sources and main-thread compatibility calculations are prohibited.

### 5.5.6 Failure handling and data minimization

Normalization SHALL fail closed for an invalid source schema, malformed frozen
identity, unresolved mandatory relationship, duplicate canonical comparison,
unknown enum value, cyclic or cross-opportunity supersession, mixed AIC grain,
overhead attribution to an unrelated opportunity, or non-finite measure. An individual
opportunity with incomplete, incomparable, unmatured, or unavailable evidence
MAY remain queryable in that explicit state; it MUST NOT produce realized
savings or a healthy result.

Prompts, model responses, tool arguments, raw logs, credentials, safe-output
bodies, and artifact bodies MUST NOT enter token-optimization logical sources.
Cross-Run duplication MAY use a producer-generated resource fingerprint only
when it is:

* an HMAC over a normalized resource class and identifier;
* keyed before publication with a secret that is never published;
* stable only within the declared control-plane collection scope; and
* unable to reveal a path, URL, query, prompt, argument, or repository content.

Cross-repository duplication MUST remain unknown when a common scoped
fingerprint is unavailable. Hashing low-entropy identifiers without a secret is
not an acceptable privacy boundary.

---

# 6. Canonical Domain Model

## 6.1 Core Entities

Version 1 SHALL define:

```text
Campaign
Repository
Workflow
Run
Domain
Tool
Audit
Issue
```

The model MAY later add:

```text
Artifact
Outcome
Evaluation
Metric
```

without changing the core execution hierarchy.

## 6.2 Entity Relationship Diagram

```mermaid
erDiagram
  CAMPAIGN o|--o{ WORKFLOW : classifies
  REPOSITORY ||--o{ WORKFLOW : contains
  REPOSITORY ||--o{ RUN : executes
  WORKFLOW ||--o{ RUN : defines
  RUN ||--o{ DOMAIN : records
  RUN ||--o{ TOOL : records
  RUN ||--o{ SKILL : activates
  RUN ||--o{ AUDIT : records
  RUN ||--o{ ISSUE : creates

  CAMPAIGN {
    string id PK "canonical ID"
    string slug UK "stable campaign identity"
    string name
    string mode
    boolean enabled
    string observedAt
  }
  REPOSITORY {
    string id PK "canonical ID"
    number githubId UK "nullable immutable ID"
    string owner "normalized GitHub owner"
    string name "normalized repository name"
    string fullName UK "OWNER/REPOSITORY fallback identity"
    string visibility
    string observedAt
  }
  WORKFLOW {
    string id PK "canonical ID"
    string repositoryId FK "required execution repository"
    string campaignId FK "nullable campaign classification"
    number githubId UK "nullable immutable ID"
    string name "repository-scoped fallback identity"
    string path "normalized preferred identity"
    string state
    string observedAt
  }
  RUN {
    string id PK "repository coordinate plus githubRunId"
    string repositoryId FK "required execution repository"
    string targetRepositoryId FK "nullable worker target"
    string workflowId FK "required owning workflow"
    number githubRunId "repository-scoped natural key"
    number attempt "latest observed attempt"
    string status
    string conclusion
    string startedAt
    string observedAt
  }
  DOMAIN {
    string id PK "deterministic semantic ID"
    string runId FK "required owning run"
    string domain
    string decision "allowed or blocked"
  }
  TOOL {
    string id PK "deterministic semantic ID"
    string runId FK "required owning run"
    string toolType "mcp or bash"
    string name
  }
  SKILL {
    string id PK "deterministic semantic ID"
    string runId FK "required owning run"
    string name
    number invocationCount
    number failedCount
    string activationSource
  }
  AUDIT {
    string id PK "deterministic semantic ID"
    string runId FK "required owning run"
    string type
    string status
  }
  ISSUE {
    string id PK "deterministic semantic ID"
    string runId FK "required owning run"
    boolean isPullRequest
    string safeOutputType
    boolean closed
    string stateReason
    string closedAt
    string statusObservedAt
  }
```

The ERD shows structural fields and query keys, not every optional observation
field. `PK`, `FK`, and `UK` denote primary, foreign, and unique keys. Nullable
GitHub IDs are preferred immutable identities when present. When cached JSONL
does not expose them, Repository falls back to normalized `fullName`, Workflow
falls back to `(repositoryId, path)` or a source-namespaced
`(repositoryId, name)`, and Run uses
`(executionRepositoryCoordinate, githubRunId)`. These composite values are
encoded into the canonical string `id`; the individual components are not
independently unique.

Repository and Workflow references on Run MAY be denormalized for browser query
efficiency, but remain mandatory canonical relationships. Every Domain, Tool,
Audit, and Issue MUST reference exactly one Run. Partial observations MAY exist
during normalization; all mandatory relationships MUST resolve before a
complete batch is reconciled or a streamed record phase is marked current.

## 6.3 Campaign

A Campaign represents one installed campaign independently of execution
activity. Its stable slug defines a deterministic, source-namespaced canonical
`id`, such as `campaign:dashboard-sources:dependabot`. Version, name, mode, and
observation time are mutable evidence, not identity.

```js
{
  id: "campaign:dashboard-sources:dependabot",
  slug: "dependabot",
  name: "Dependabot",
  mode: "review",
  enabled: true,
  version: "v1",
  currentVersion: "v2",
  updateState: "update-available",
  observedAt: "2026-10-02T00:00:00Z"
}
```

**CAM-001** — Every implementation MUST define the Campaign store/table and
persist campaigns even when they have no workflows.

**CAM-002** — Refreshing mutable inventory fields MUST enrich the existing
Campaign rather than create a version-specific record.

**CAM-003** — Campaign records have no mandatory execution parent and MUST NOT
be deleted merely because Runs or run-owned details expire.

**CAM-004** — Stored mode, enablement, scope, and budgets are descriptive
evidence, not control-plane authority. Their presence MUST NOT authorize live
work or widen reviewed policy.

---

# 7. Repository

A Repository represents one GitHub repository.

Example:

```js
{
  id: "github:repository:123456",
  githubId: 123456,

  owner: "githubnext",
  name: "gh-cao",
  fullName: "githubnext/gh-cao",

  visibility: "public",

  observedAt: "...",
  updatedAt: "...",

  generation: "..."
}
```

### Requirements

**REP-001** — Immutable GitHub repository ID SHOULD define identity when available.

**REP-002** — Repository rename MUST NOT produce a second logical repository when immutable identity remains unchanged.

**REP-003** — Repository name MUST NOT be the sole canonical identity when GitHub ID is known.

---

# 8. Workflow

Example:

```js
{
  id: "github:workflow:98765",

  repositoryId: "github:repository:123456",
  githubId: 98765,

  name: "Dashboard",
  path: ".github/workflows/cao-dashboard.yml",

  state: "active",

  observedAt: "...",
  updatedAt: "...",

  generation: "..."
}
```

### Requirements

**WF-001** — Workflow MUST reference Repository.

**WF-002** — GitHub workflow ID SHOULD be preferred for identity.

**WF-003** — Workflow filename MUST NOT be treated as immutable identity.

---

# 9. Run

Example:

```js
{
  id: "github:run:githubnext/gh-cao:123456789",

  repositoryId: "...",
  targetRepositoryId: "...",
  workflowId: "...",

  githubRunId: 123456789,
  attempt: 1,

  event: "workflow_dispatch",

  status: "completed",
  conclusion: "success",

  createdAt: "...",
  startedAt: "...",
  completedAt: "...",

  agentId: "copilot",
  modelId: "gpt-5.4",
  agenticDurationSeconds: 10,
  firewallAllowedCalls: 4,
  firewallBlockedCalls: 2,
  mcpToolCalls: 2,
  mcpResponseBytes: 192,
  operationalGrader: 0.8,
  highPriorityAuditItems: 1,
  mediumPriorityAuditItems: 2,

  headSha: "...",
  headBranch: "main",

  generation: "..."
}
```

### Requirements

**RUN-001** — Source observations MUST preserve and deduplicate by attempt.
Canonical Run normalization SHALL converge those observations onto the
repository-scoped GitHub run identity and retain the winning observation's
`attempt`.

**RUN-002** — Canonical identity SHALL incorporate normalized execution
Repository coordinates and GitHub run ID. Attempt MUST NOT create a second
canonical Run.

**RUN-003** — Later observations MAY enrich incomplete Run records.

**RUN-004** — A completed Run SHOULD retain immutable `agentId`, `modelId`,
`agenticDurationSeconds`, `firewallAllowedCalls`, `firewallBlockedCalls`,
`mcpToolCalls`, `mcpResponseBytes`, `operationalGrader`,
`highPriorityAuditItems`, and `mediumPriorityAuditItems` values when the
corresponding source evidence is available at import time.

**RUN-005** — An unavailable aggregate MUST remain `null`. An observed evidence
class with no matching calls or audit items SHALL produce zero. Duration is
measured in seconds, and MCP response size is measured in bytes.

**RUN-006** — A worker Run SHOULD preserve its canonical
`targetRepositoryId`. Missing target association MUST remain null and MUST NOT
default to the execution Repository. Orchestrator Runs MUST leave
`targetRepositoryId` null.

---

# 10. Run Detail

Jobs and agent interaction contexts MAY remain upstream observations or
published logical sources, but they are not canonical entities and MUST NOT be
persisted in canonical SQLite or IndexedDB tables. Job-shaped performance data
MAY be projected directly from authoritative inputs. Run detail MUST be projected from Run and its Domain, Tool, Skill, Friction,
Audit, and Issue records.

Canonical Run records SHALL retain scalar query metrics and separately keyed
experiment, grader, and eval evidence rather than copying the original nested
payload. Unconsumed `tokenUsage`, `ambientContext`, `workingSet`,
`behaviorFingerprint`, `taskDomain`, `comparison`, `agenticAssessments`,
`graders`, `context`, `data`, `logsPayload`, `logsPath`, and `auditPath` remain
upstream, not in the derived Run. The unused GitHub run `number` and
`intentionalFailure` fields and duplicate `aic` SHALL NOT be persisted;
`githubRunId`, `attempt`, `classification`, `failureKind`, and `aicTotal` retain
their distinct meanings. Workflow `ghAwMetadata` and `ghAwManifest` remain
upstream declarations. Pruning SHALL occur during normalization and at the
storage write boundary, including ingestion of previously published shards.
Unknown evidence fields MUST NOT be removed by a speculative query-only
allowlist. Source provenance, explicit nulls, immutable Run aggregates, and
historical execution metadata SHALL be preserved.

## 10.1 Query-reconstructible fields

The editable TypeSpec storage contract SHALL retain facts rather than
materialized display or relationship projections. Workflow campaign names,
icons, README paths, AI-credit allowances, worker counts, and warning counts
SHALL come from the owning Campaign through declarative joins. The gh-aw
Workflow SHALL retain only an optional `campaignId` foreign key to Campaign;
the campaign slug SHALL also be projected from that parent, not copied into
Workflow. PostgreSQL SHALL enforce `(namespace, campaign_id)` against
`campaigns(namespace, id)`, and canonical relationship validation SHALL reject
an unresolved nonnull Campaign reference. The gh-aw
version label SHALL be computed from the installed and current versions.
These values MUST NOT be copied into Workflow storage.

Grader and Eval observations SHALL obtain their original source IDs from
the definition's `sourceGraderId` or `sourceEvalId`. Names and display names
are independently observed facts and MAY differ from those IDs. Each result SHALL retain one event `timestamp`;
`result-timestamp` and `observed-at` query outputs MAY project that same value.
The redundant observation-level `sourceGraderId`, `sourceEvalId`, and
`resultTimestamp` fields MUST NOT be stored. Eval `answer` duplicates `evalResult`; requested and
resolved models belong to the Run and SHALL be joined at query time.
SQLite mirrors MUST NOT materialize assignment/result first/last observation
ranges from `observed_at`; range facts belong to definitions only.

Repository `fullName`, Run `repositoryFullName`, and Audit `targetRepo`
SHALL be reconstructed from their retained owner and repository components.
Ingestion MAY accept source-shaped copies to preserve their underlying facts
before pruning them, but conflicting copies MUST fail explicitly rather than
erase independent evidence. Missing coordinate components SHALL yield a
missing query value, not a fabricated `/` coordinate.

This rule does not remove primary/foreign keys, namespace isolation, ordering,
presence bits, partition keys, provenance, physical lookup indexes, or
independently observed historical execution facts. Similar values at different
grains are not necessarily reconstructible: Run status and conclusion,
requested and resolved models, friction and total Run costs, token classes,
call and outcome events, and execution and dispatched-target repositories
retain their separate meanings. Browser schema upgrades SHALL follow §5.1,
preserving compatible stores during additive version 44/45 upgrades and
rebuilding incompatible projections; PostgreSQL requires a fresh schema matching TypeSpec.
Retained row counts SHALL be calculated from namespace-scoped entity tables
in the same read snapshot, not persisted in `cao_quality`.

---

# 11. Run-Owned Record Classification

A Run's observations SHALL be classified into:

```text
Domain — firewall-allowed and firewall-blocked network activity
Tool — MCP and Bash calls
Skill — extracted skill invocation usage
Friction — precomputed avoidable execution-cost attribution
Audit — lifecycle, agent, policy, grader, and other audit observations
Issue — issue and pull-request safe outputs
```

The canonical browser database and the SQLite-backed IndexedDB interchange
also retain six separate experiment-evidence collections: `experiments`,
`experimentAssignments`, `graders`, `graderObservations`, `evals`, and
`evalObservations`. Definitions reference a Workflow; observations and
assignments reference both a definition and the producing Run. Every row has
a stable ID and original source provenance. These collections are published
through run-information (experiment definitions and assignments) and record
(grader and eval definitions and observations) shards and
resolved directly by Dashboard Language queries. The SQLite adapter mirrors
them into six transactional relational tables with the corresponding
snake-case names, without requiring another API. These tables expose typed
relationship keys and observed values (variant, grader value/status, eval
result, requested/resolved model, first/last observation times, and optional
inclusion, exclusion reason, and audit identity); absent evidence stays NULL
rather than being inferred from a successful run.
Explicit grader and eval IDs SHALL be stored once on their definitions as
`sourceGraderId` and `sourceEvalId` and exposed through definition joins;
canonical definition keys also include the owning Workflow
to avoid collisions when two workflows reuse an ID. A grader result links
to an experiment variant only when its explicit experiment identity matches
an assignment for the same Run.
The Graders scatter view SHALL plot observation time against native value
divided by the maximum observed value for the same canonical grader in the
selected data. This denominator is not a theoretical score limit. Its
Dashboard Language queries SHALL recompute existing ratios when new evidence
changes the maximum. Missing values, zero or missing maxima, and invalid
timestamps SHALL produce no plotted point; the native ledger SHALL retain
those observations. Ratios SHALL NOT be clamped or inverted for lower-is-better
graders, including when negative maxima produce ratios greater than one.
An `experimentAssignments`, `graderObservations`, or `evalObservations` record
MUST reference a definition (`experiments`, `graders`, or `evals` respectively)
whose `workflowId` equals the `workflowId` of the Run named by the record's
`runId`; a record whose definition belongs to a different Workflow is not a
valid evidence relationship and MUST be excluded from every ingestion path,
including Activity's pre-publication consolidation, before the canonical
browser database, SQLite mirror, or Postgres store accepts it.
Repeated definitions keep the earliest and latest observed timestamps across
runs and normalized shards; later collection of an older run cannot erase the
latest definition. Browser shard ingestion writes bounded batches before its
versioned receipt, so a failed shard is replayed idempotently on retry, but
the entire shard is not an atomic transaction.
The hosted dashboard ingester admits the same six canonical collections and
projects those query definitions from the verified shards rather than trusting
inventory or report-derived rows.

Unified session version-1 `experiment.assignment`, run-matching
`experiment.state`, `grader.manifest`, `grader.result`, and `eval.result`
observations are the preferred evidence. Manifests enrich observed grader
definitions but do not invent results. Session evidence overrides cached run
evidence independently for each category; it MUST NOT be added a second time
from legacy audit projections. Run-scoped result identities and supplied
attempts MUST match the producing run. State ledgers for other runs and
cumulative counts MUST NOT establish assignments. Untimed evaluation evidence
is retained using ingestion observation time without inventing a timed session
event. Only allowlisted scalar metadata and numeric metrics survive projection;
prompts, messages, error bodies and raw payloads MUST NOT be retained.

In the absence of corresponding session evidence, explicit
`run.experiments.assignments` and `run.graders.results` evidence can populate
their respective entities. A supplied `run.evals[]` entry is accepted only with
an explicit ID, `YES`/`NO`/`UNKNOWN` answer, valid optional timestamp and
matching optional run identity and attempt; all supplied identity aliases MUST
agree. Collection MAY project an already-downloaded `evals.jsonl` into that
existing run input after verifying its repository, run and attempt against
`aw_info.json`, but MUST prefer session evals and MUST NOT introduce an extra
GitHub acquisition path. Invalid eval
candidates remain run-linked Audit observations without an inferred answer
or score; valid entries also produce a run-linked Audit observation and their
canonical Eval observation references that Audit's stable identity.
Eval audit identities MUST survive browser, SQLite and hosted PostgreSQL
projection and be exposed as `audit-id` by the eval-observations query.
Grader values and units MUST remain distinct from YES/NO eval answers in
dashboard views; a grader score or pass/fail status does not create an Eval.
Direct artifact ingestion SHALL apply the same classification and identity
checks to supplied `evals.jsonl` records when session eval evidence is absent.
Version-1 and version-2 `grader_results.json` documents SHALL use the same
allowlisted projection as session grader events, enriched by the adjacent
`grader_manifest.json`. Collection SHALL verify the artifact's repository,
run and attempt using `aw_info.json`; supplied result run identities and
attempts MUST also agree. Session results remain authoritative for duplicate
IDs; the downstream `graders` artifact MAY supplement them with previously
unrecorded custom results, without duplicating built-ins. Compact
`usage/graders/`, named or prefixed `graders` artifacts, and flattened files
SHALL be supported. Ordered metrics SHALL preserve explicit `null` values
as unavailable evidence, not discard them or promote a diagnostic metric.
The Evals observation ledger SHALL display retained `YES`, `NO`, and `UNKNOWN`
answers distinctly, preserving run evidence links and observation times rather
than hiding unknown answers behind a binary-only filter.

The unified session is also the preferred source for every supported telemetry
family: agent lifecycle and diagnostics, MCP calls/RPC/configuration, firewall
access and steering, model routing/inventory, skill activity/inventory/conflicts,
usage, execution and detection results, daily guardrails, workflow metadata and
job results, safe-output requests/results/errors, plugin installations, GitHub
rate limits, dynamic workflow progress, and collection diagnostics.
Only bounded, explicitly projected metadata is admitted. Prompts, message
bodies, tool inputs/outputs, raw errors, objectives, private paths and unknown
extension payloads remain upstream.

Untimed telemetry MUST retain ingestion `observedAt` and source line provenance
without inventing a native `timestamp`. Inventory entries MUST remain distinct
without being counted as invocations. Native event `type` identifiers MUST be
preserved verbatim from the AW unified session, including `mcp.tool_call`,
`tool.execution_start`, `tool.execution_complete`, `mcp.rpc.request`,
`mcp.rpc.response`, `firewall.http_access`, `skill.invoked`, `grader.result`,
`eval.result`, and `safe_output.result`. Outcomes and firewall decisions MUST
remain `status` and `decision` metadata, not synthesized type identifiers.
MCP queries MUST select invocation evidence using native types and explicit MCP
identity, rather than renaming all invocations to one CAO type. RPC request/response
pairs MAY supply calls and outcomes when combined tool-call observations are
absent, but MUST NOT double-count the two representations. Firewall method,
response-byte and latency measurements MUST survive browser and hosted storage.
Explicit firewall allow decisions MUST NOT become policy blocks merely because
the upstream endpoint returned an HTTP error.

Run accounting MUST prefer measured usage reports, then proxy usage, then root
engine-result snapshots, independently per phase and measured dimension.
Repeated request IDs are revisions, not additional usage. Cumulative AIC MUST
NOT be added to per-request AIC, and engine usage snapshots or native estimated
tokens MUST NOT be added to measured reports. Runtime metadata and actual
safe-output results take precedence over cached copies; safe-output requests
and errors MUST NOT establish created outcomes or issue status.

Records MUST remain independently addressable and MUST NOT be stored as one
ever-growing array inside the Run record. Issues and pull requests share the
Issue table; pull requests set `isPullRequest=true`.

## 11.1 First-pass Audit curation

Audit is an evidence table, not a second Run summary table. JavaScript canonical
ingestion, normalized Activity publication, and Go/Postgres ingestion SHALL apply
the same versioned, fail-closed curation rules. Existing IndexedDB, SQLite, and
Postgres databases SHALL apply those rules during maintenance, including when
the input payload hashes have not changed. Cleanup MUST precede size-based Run
eviction, use bounded dirty-shard indexed reads or namespace-scoped SQL, and publish any
changed database revision so active worker/hosted query subscriptions refresh.

IndexedDB Audit cleanup SHALL queue deletion keys in bounded batches within the
read transaction and update shard accounting in that same transaction. Only newly
written audit shards, audits belonging to changed Run curation facts, and audits
whose evidence references were removed need inspection; reference checks SHALL
use the `byAudit` indexes. Deleting each
row through its cursor invalidates Chromium's cursor prefetch and can stall a
deployed-data refresh; batching MUST preserve the same eligibility and reference
checks without materializing the Audit collection.

The first pass MAY discard only the following source/type/status/summary tuples:

| Source | Type | Status | Summary |
|---|---|---|---|
| `gh-aw-logs` | `workflow_run_comparison` | `unavailable` | `No baseline comparison` |
| `gh-aw-logs` | `workflow_run_working_set` | `observed` | `Working set measured` |
| `agent` | `agent.session` | `completed` | Empty string |
| `audit` | `audit.observability` | `low` | `1 anomalous event pattern(s) detected` |
| `audit` | `audit.recommendation` | `low` | `Monitor workflow performance over time` |

It MAY additionally discard these Run-backed copies only when the owning Run
exists and the applicable fields agree:

| Source/type | Required agreement |
|---|---|
| `gh-aw-logs` / `workflow_run_started` | Valid timestamp equals `startedAt` (or `createdAt` when start is unavailable), and status equals Run status. |
| `gh-aw-logs` / `workflow_run_completed` | Valid timestamp equals `completedAt`, status equals Run conclusion, and summary equals the nonempty Run classification or conclusion. |
| `gh-aw-logs` / `workflow_run_failed` | Status is `failure`; Run conclusion is `failure` or Run failure kind is nonempty; summary equals that failure kind or `workflow run failed`. |
| `gh-aw-logs` / `workflow_run_usage` | Status is `observed`; summary is `AIC N`, and finite nonnegative `N` equals the nonnull Run `aicTotal`. |
| `gh-aw-logs` / `workflow_run_safe_outputs` | Status is `observed`; summary is `N safe output items`, and nonnegative safe-integer `N` equals nonnull Run `safeItemsCount`. |
| `audit` / `audit.finding` | Status is `critical`, code is `workflow_failed`, summary is `Workflow Failed`, and Run conclusion is `failure`. |

Eligible rows MUST contain no additional nonnull evidence beyond shared record
identity, provenance, ordering, timestamps, source/type/status/summary, and the
specified failure code. Unexpected sources, statuses, summaries, invalid
numbers/timestamps, conflicting explicit attempts, and missing/null Run facts
MUST retain the Audit. Absence after curation MUST NOT establish zero source
activity, a successful outcome, or complete import coverage. Transport record
counts and checksums MUST still validate every input record; retained counts
SHALL reflect only rows actually stored. Database health diagnostics MUST permit
an empty Audit collection when the retained Run hierarchy is valid.

Numeric summary matching SHALL consume the entire string, without trailing
whitespace, and SHALL retain summaries longer than 1,024 ASCII characters or
scientific exponents longer than three digits. These shared conservative bounds
MUST prevent a malformed numeric summary from aborting existing-database cleanup.

This first pass MUST preserve grader/eval audits and their referenced identities,
safe-output events, security/policy evidence, specific findings and diagnostics,
and runtime facts that are missing from the Run. Behavior and assessment copies
remain retained because their complete comparison facts are not present in the
query-minimal native Postgres Run representation. Implementations MUST NOT add
duplicate Run payloads or speculative native columns merely to prune them.

---

# 12. Run-Owned Records

## 12.1 Shared Record Fields

Every Domain, Tool, Skill, Friction, Audit, and Issue MUST directly reference
its owning Run.
Records MAY retain common source evidence such as `sequence`, `timestamp`,
`source`, `type`, `status`, `correlationId`, and `payloadRef`.

Issue records SHALL preserve the safe-output action and canonical GitHub entity
type. The SQLite interchange SHALL expose these values as nullable
`safe_output_type` and `github_entity_type` columns. Missing entity-type
evidence MUST remain absent rather than be inferred as a generic issue.

Issue lifecycle fields SHALL use `state`, `closed`, `stateReason`, `closedAt`,
and `statusObservedAt`, without duplicate `issueState`, `issueClosed`,
`issueStateReason`, `issueClosedAt`, or `issueStatusObservedAt` copies.
Tool and Skill membership is determined by their separate collections; the
unused `isSkill` flag SHALL NOT be persisted or projected.

The current Tool collection and TypeSpec `tools` table are event-grain facts,
not a configured-tool inventory or one row per invocation. A `tool.execution_start` and
its correlated `tool.execution_complete` SHALL retain separate identities, timestamps,
status, and size evidence.

A future physical normalization MAY factor repeated observed tool identity
into a namespace-scoped dictionary and reference it from tool-event facts.
Dictionary identity MUST distinguish server, tool name, type, versions, and
missing values. It MUST NOT infer configured definitions from runtime use.
Existing logical Tool query payloads and observation ordering MUST be preserved
through the SQL query boundary. A `toolCalls` invocation table requires an
explicit correlation and aggregation contract before collapsing events;
renaming the current facts alone does not establish invocation grain.
This dictionary/invocation split is not implemented by the current storage profile.

Example:

```js
{
  id: "domain:01J...",

  runId: "github:run:githubnext/gh-cao:123456789",

  sequence: 17,
  timestamp: "...",

  source: "firewall",
  type: "firewall.http_access",

  actor: {
    type: "system",
    name: "firewall"
  },

  summary: "Outbound request blocked",

  correlationId: null,
  payloadRef: null,
  domain: "example.com",
  decision: "blocked",
  requestCount: 1,

  generation: "..."
}
```

## 12.2 Canonical Record Types

Run-owned session records SHALL use AW's version-1 unified-session identifiers,
not a parallel CAO taxonomy. Representative native identifiers include:

```text
session.start
user.message
assistant.message
assistant.reasoning
tool.execution_start
tool.execution_update
tool.execution_complete
mcp.tool_call
mcp.rpc.request
mcp.rpc.response
mcp.guard.blocked
mcp.difc.filtered
mcp.configuration
firewall.http_access
firewall.token_usage
firewall.steering
skill.invoked
skill.inventory
usage.report
experiment.state
experiment.assignment
grader.manifest
grader.result
eval.result
safe_output.request
safe_output.result
safe_output.error
github_api.rate_limit
execution.result
detection.result
workflow.info
workflow.job_result
session.result
session.error
```

Typed experiment, grader, and eval evidence SHALL populate their respective
definition and observation stores; retaining native identifiers does not move
these entities into one generic event table. CAO-only derived findings MAY
retain their own explicitly source-specific types.

Ingestion SHALL mine experiment assignments from the agent's unified session
(`aw_session.jsonl`) first, using `experiment.assignment` and run-matching
`experiment.state` events, including explicitly empty assignment maps.
Only when the unified session does not contain experiment assignments, ingestion SHALL
also accept gh-aw's explicit current-run `assignments.json` artifact, including
the compact `usage/experiment/` layout and prefixed artifact directories.
Both Activity and hosted collectors SHALL request the `usage,experiment` artifact
sets without widening repository scope or collection budgets.
Collectors SHALL enumerate the selected repository's registered `.lock.yml`
workflow paths and pass explicit repository-qualified targets to `gh aw logs`.
Bare `--repo` discovery SHALL NOT substitute checkout-local workflow names for
the selected repository's workflows. Failed discovery SHALL fail closed, and an
empty remote workflow catalog SHALL NOT fall back to the collector's catalog.
Collection SHALL verify repository, run ID and attempt against `aw_info.json`
before retaining these assignments. Unified assignments, including an explicitly
empty assignment map, take precedence. Identical assignment maps duplicated in
compact usage and named experiment artifacts SHALL be ingested once regardless
of JSON key order; conflicting or invalid copies SHALL be rejected. A standalone
experiment artifact MAY use the sibling usage artifact's `aw_info.json` when its
own and run-directory metadata are absent, subject to the same repository, run ID
and attempt checks. Ambiguous usage metadata SHALL be rejected.
Cumulative experiment state counts SHALL
NOT be used to infer a run's assigned variant. The Experiments page SHALL expose
retained assignment variants and run evidence links through canonical queries.

Ingestion SHALL canonicalize historical CAO aliases at the shared record
boundary, including normalized published shards. For example, `agent_tool_start`
becomes `tool.execution_start`, `tool.call` becomes `mcp.tool_call`,
`net_allowed` and `net_blocked` become `firewall.http_access`, and
`audit.skill_activation` becomes `skill.invoked`. Split outcome/decision aliases
SHALL retain their failure or decision facts as metadata. Historical aliases
provide an AW-equivalent classification, not recovery of an unavailable original
source event. Native input types SHALL never be rewritten. Record identity,
correlation, timestamps and source references SHALL remain stable.
Hosted PostgreSQL ingestion SHALL apply the same alignment before binding
native columns. Its snapshot revision SHALL include the normalization version
so unchanged published shards are re-ingested after this contract changes.

## 12.3 Correlation

Related records SHOULD use `correlationId`.

Example:

```text
tool.execution_start    correlationId=abc
mcp.rpc.request         correlationId=abc
firewall.http_access    correlationId=abc decision=allowed
mcp.rpc.response        correlationId=abc
tool.execution_complete correlationId=abc status=success
```

## 12.4 Ordering

Canonical record order MUST NOT depend solely on ingestion order.

Ordering SHOULD use:

```text
source sequence
```

when available.

Otherwise:

```text
source timestamp
+
deterministic tie-breaker
```

MUST be used.

---

# 13. Transaction Log Principle

A Run timeline MAY resemble:

```text
001 session.start
002 user.message
003 assistant.message
004 tool.execution_start
005 mcp.rpc.request
006 firewall.http_access decision=allowed
007 mcp.rpc.response
008 tool.execution_complete status=success
009 assistant.message
010 safe_output.request
011 safe_output.result
012 session.result
```

The same records SHALL support different projections.

### Conversation

```text
user.message
assistant.message
tool.execution_start
tool.execution_complete
```

### Security

```text
mcp.guard.blocked
firewall.http_access decision=denied
safe_output.error
```

### Network

```text
mcp.rpc.request
firewall.http_access
mcp.rpc.response
```

### Debugging

All run-owned records.

Views MUST NOT maintain independent copies of these datasets.

---

# 14. Source Adapters

## 14.1 Purpose

Adapters translate external representations into source-neutral observations.

Initial adapters SHOULD support:

```text
current dashboard source JSON
gh-aw log JSON
SQL exports conforming to the dashboard SQL export contract
```

Future adapters MAY include direct GitHub API representations.

## 14.2 Adapter Boundary

Adapters MAY understand source-specific names and structures.

Everything downstream of adapters SHOULD NOT.

## 14.3 SQL

SQL SHALL be treated as another source representation. The static dashboard SHALL NOT connect directly to a database.

Database owners SHALL map source-specific tables to the versioned `gh-aw-cao.dashboard-sql-export` interchange contract. A trusted workflow or offline tool SHALL serialize that result to JSON before Pages publication.

The IndexedDB model MUST NOT be produced by mechanically copying SQL tables.

```text
source-specific SQL tables
    |
    v
dashboard export view
  |
  v
static JSON publication
  |
  v
SQL-export adapter
    |
    v
canonical model
```

The version 3 JSON document SHALL contain:

```js
{
  contract: "gh-aw-cao.dashboard-sql-export",
  schema_version: 3,
  source: "stable-source-name",
  generation: "immutable-generation-id",
  exported_at: "RFC3339 timestamp",
  rows: []
}
```

Each row SHALL contain `entity_kind`, `source_id`, and `observed_at`. The
remaining nullable columns are defined by entity kind. GitHub-backed
relationships SHALL use immutable GitHub repository, workflow, and run IDs.
Domains, Tools, Audits, and Issues SHALL carry `github_run_id` and
`run_attempt`; normalization uses the execution Repository coordinates and
`github_run_id` for the canonical `runId`, while `run_attempt` preserves source
grain and validation context.

The relational interchange SHALL consist of one manifest row and denormalized entity rows. A producer MAY expose these as tables or views. Database-specific extraction queries and credentials remain upstream concerns and MUST NOT be shipped to the browser.

Token-optimization Audit rows SHALL additionally follow Section 5.5.5. SQL
producers SHALL NOT flatten invocation and Run-aggregate AIC into one repeated
measure or substitute display names for canonical relationship IDs.

## 14.4 Cached gh-aw JSONL

The normative mapping for cached schema-v2, schema-v4, and schema-v5 activity shard input is
defined in [Cached gh-aw JSONL Mapping](dashboard-gh-aw-jsonl-mapping.md).

---

# 15. Normalization

Normalization SHOULD be deterministic and side-effect free.

Preferred API:

```js
const observations = parseSource(input);

const batch = normalize(observations);
```

Example result:

```js
{
  repositories: [],
  workflows: [],
  runs: [],
  domains: [],
  tools: [],
  audits: [],
  issues: []
}
```

Persistence SHALL occur separately:

```js
await writer.write(batch);
```

## 15.1 Missing Data

A missing canonical data point MUST NOT immediately be treated as zero, empty, or unavailable.

Before classifying it as missing, an implementation agent SHALL inspect the current [`gh aw logs` schema](https://github.com/github/gh-aw/blob/main/schemas/logs.schema.json) and determine whether any field in the applicable output variant contains an authoritative observation that can be normalized into the canonical model. This inspection SHALL include nested and aggregate structures, not only fields whose names match the canonical property.

This discovery and normalization SHALL run in the activity-campaign JavaScript invoked by `.github/workflows/cao-activity.yml`, before the activity snapshot is published. The workflow YAML orchestrates that JavaScript and MUST NOT embed source-field mappings.

When the schema exposes suitable data, the activity-campaign source adapter SHOULD normalize it. The mapping MUST:

1. be explicit, deterministic, and covered by a fixture-based test;
2. preserve source provenance and the schema revision used to establish the mapping;
3. respect the field's scope, units, nullability, and required or optional status;
4. distinguish an absent field from an explicit `null`, empty collection, zero, and `false`; and
5. avoid deriving run-level facts from summaries unless the schema defines that attribution.

The `gh aw logs` schema is a discovery surface for source adapters, not a canonical dashboard contract. Views MUST NOT read its fields directly, and similarity of field names alone is insufficient evidence for a mapping.

If the schema defines a suitable field but the collected log does not contain it, the activity-campaign source adapter MUST preserve the data point as unknown and record why it is missing, including whether the cause is an unsupported schema variant, an older producer, an unavailable artifact, an uncollected optional field, or invalid source data. If no semantically valid field exists, the data point MUST remain explicitly unknown rather than being guessed or coerced.

Token-optimization evidence SHALL use the more specific completeness and
comparability rules in Section 5.5. An aggregate `token_usage_summary` does not
prove invocation coverage, a safe-output creation does not prove acceptance,
and an experiment name without assignments does not prove comparable variants.

---

# 16. Canonical Identity

IDs MUST be deterministic whenever stable upstream identifiers exist.

Examples:

```text
github:repository:<id>

github:workflow:<id>

github:run:<normalized-owner>/<normalized-repository>:<github-run-id>
```

Run-owned records SHOULD use stable source identifiers where available.

Otherwise deterministic IDs MAY be derived from stable source coordinates or content identifiers.

Random IDs MUST NOT be introduced during ingestion when doing so would break idempotence.

---

# 17. Provenance

Canonical records SHOULD retain source provenance when useful.

Example:

```js
{
  provenance: {
    source: "gh-aw-log",
    sourceId: "...",
    observedAt: "...",
    sourceRevision: "..."
  }
}
```

This SHOULD allow debugging questions such as:

```text
Which observation established this conclusion?

Did the firewall or the agent report this failure?

Which source supplied this timestamp?
```

---

# 18. Eventual Consistency

The canonical model SHALL be eventually consistent.

The system MUST support observation sequences such as:

```text
T0 run discovered
T1 runtime event discovered
T2 firewall data discovered
T3 usage discovered
T4 run completes
T5 safe-output result arrives
```

No source MUST provide a fully populated logical entity in one operation.

Later observations MAY enrich existing records.

---

# 19. Conflict Resolution

When observations disagree:

1. source precedence SHOULD be explicitly defined;
2. newer authoritative observations SHOULD supersede older observations;
3. arbitrary ingestion order MUST NOT be the sole deciding rule.

Unknown or incomplete information SHOULD remain explicitly unknown rather than being guessed.

---

# 20. Published Source Generations

## 20.1 Principle

Published dashboard inputs SHOULD eventually be represented as immutable generations.

A generation SHALL contain:

```text
manifest
+
immutable chunks
```

Example:

```json
{
  "version": 2,
  "schemaVersion": 1,
  "generation": "2026-09-09T05:00:00Z",
  "createdAt": "2026-09-09T05:00:00Z",

  "datasets": [
    {
      "kind": "audits",
      "path": "audits/audits-0042.json",
      "sha256": "...",
      "bytes": 3920211,
      "records": 5000,
      "minTimestamp": "...",
      "maxTimestamp": "..."
    }
  ]
}
```

## 20.2 Migration Requirement

Chunked publication SHALL NOT be required for the first canonical-data milestone.

Initial implementation MAY consume today's source files.

Recommended transition:

```text
current source JSON
        ↓
new adapters
        ↓
canonical model
```

Then later:

```text
chunked generation
        ↓
same adapters/model
```

---

# 21. Immutable Chunks

Large datasets SHOULD eventually be partitioned:

```text
sources/
  manifest.json

  repositories/
    repositories-0001.json

  workflows/
    workflows-0001.json

  runs/
    runs-0001.json
    runs-0002.json

  domains/
    domains-0001.json

  tools/
    tools-0001.json

  audits/
    audits-0001.json

  issues/
    issues-0001.json
```

Published chunks MUST be immutable within a generation.

---

# 22. Chunk Metadata

Every production chunk SHOULD identify:

```text
kind
path
SHA-256 digest
byte size
record count
```

Where useful it MAY also identify:

```text
minimum key
maximum key
minimum timestamp
maximum timestamp
```

---

# 23. Chunk Size

Chunks MUST be bounded.

Initial tuning targets SHOULD be approximately:

```text
1,000–10,000 source records
```

or approximately:

```text
1–8 MiB per uncompressed source chunk
```

whichever threshold is reached first.

These are operational defaults, not correctness requirements.

Benchmark data MAY change them.

---

# 24. Large Payloads

Large diagnostic payloads SHOULD NOT be duplicated into hot run-owned record rows.

Prefer:

```js
{
  id: "...",
  runId: "...",
  type: "tool.result",
  summary: "...",

  payloadRef: {
    chunk: "payloads/run-123-004.json",
    key: "record-442"
  }
}
```

IndexedDB SHOULD contain data required for:

```text
filtering
sorting
navigation
aggregation
timeline rendering
correlation
```

Raw payloads MAY remain in immutable source chunks.

---

# 25. IndexedDB Role

IndexedDB SHALL be a materialized browser projection.

The governing rule is:

> IndexedDB is cacheable derived state, not authority.

A completely empty IndexedDB MUST be recoverable.

---

# 26. Database Definition

The canonical browser database SHALL use:

```js
const DATABASE_NAME = "gh-aw-cao-dashboard-data";
const DATABASE_VERSION = 46;
```

The name MAY be scoped by deployment path to prevent unrelated dashboard
deployments on the same origin from sharing derived state. Version upgrades
SHALL follow Section 5.1: versions 44 and 45 with a `runs` store receive an
additive upgrade; incompatible older projections are recreated from inputs.

---

# 27. Object Stores

IndexedDB version 46 SHALL define:

```text
campaigns
repositories
workflows
runs
domains
tools
skills
friction
audits
issues
operationalValues
operationalValueEvidence
workflowValueCollections
marketplacePackages
experiments
experimentAssignments
graders
graderObservations
evals
evalObservations
transactions
storageShards
dailyRunCounts
runCountContributions
dailyRunCountState
```

Future physical versions MAY include:

```text
payloadCache
searchIndex
```

---

# 28. Core Indexes

Indexes SHOULD initially reflect known query paths.

### campaigns

```text
slug
```

### repositories

```text
none; shipped views enumerate the bounded repository inventory or address a
record by primary key
```

### workflows

```text
repositoryId
```

### runs

```text
repositoryId
workflowId
conclusion
```

Runtime computations are evaluated by request-scoped queries (§73); no
computation-result indexes are part of the canonical IndexedDB schema.

### run-linked tables

```text
domains: runId
tools: runId
skills: runId
friction: runId
audits: runId
issues: runId
experimentAssignments: runId, experimentId
graderObservations: runId, graderId
evalObservations: runId, evalId
experiments, graders, evals: workflowId
```

### operational values

```text
repositoryId
valueId
```

These indexes correspond to the shipped campaign lookup, repository/workflow
navigation, failed-run, and run-detail access paths. Presentation-level
Dashboard Language fields are projected after canonical reads and do not by
themselves justify canonical secondary indexes. Indexes SHOULD NOT be added
speculatively.

Every secondary index increases storage and write amplification.

---

# 29. Canonical Browser Reconciliation

Canonical entity rows SHALL use `id` as the primary key and SHALL NOT carry a
canonical generation key in IndexedDB version 30. Ingestion serializes writers,
normalizes source observations, validates complete in-memory batches where the
input mode permits it, and reconciles each store in bounded transactions.

Normalized JSONL streaming MAY commit bounded batches before the complete shard
has been received. Therefore a database read during ingestion MAY observe an
intermediate state. Query subscriptions that depend on the unfinished phase
MUST retain their prior complete payload until the phase succeeds.

Ingestion SHALL reject already-expired canonical records before storage writes,
including when final maintenance is deferred. It SHALL nevertheless parse and
validate every transport record and verify the metadata's declared count against
all parsed records, not just stored records. Retained Run summaries and structural
parents SHALL remain available independently of detail expiry. The worker SHALL
defer routine inventory and shard maintenance until one final pass after both
have committed; unchanged inputs still require expiry maintenance as time advances.

Published inventory metadata that no activity shard observes — marketplace
packages — SHALL be committed before any activity shard is downloaded, and the
worker SHALL publish that inventory phase as soon as the commit lands. Only
subscriptions whose resolved canonical sources are entirely satisfied by that
phase SHALL be published from it, so an inventory-only page such as Marketplace
renders without waiting for historical run ingestion. The complete inventory
payload SHALL still be committed after shard ingestion, so inventory continues
to resolve shared structural records last and the converged canonical state is
unchanged.

---

# 30. Ingestion Receipts and Retry

The `transactions` store SHALL record content-addressed ingestion receipts and
failures. A source shard MAY be skipped only when its payload identity,
ingestion version, adaptation context, and effective retention profile match a
successful receipt. A longer historical retention override SHALL replay the
authoritative shard rather than reuse a receipt created with shorter retention.

An interrupted or failed shard MUST remain retryable. A failed receipt MUST NOT
be interpreted as source freshness, completeness, or successful activation.
The ingestion lock is coordination state in the same store and MUST NOT be
treated as durable evidence.

---

# 31. Query-only derived results

## 31.1 Campaign outcome signals

The legacy Operational Value overview and repository drill-downs are no longer
shipped. The built-in **Value** page uses the report evidence in Section 31.2.
Canonical observations and their storage/retention semantics remain unchanged.
The comparison requirements below apply when authoring queries over retained,
repository-scoped primary Operational Value observations, not run conclusions.
SQL and IndexedDB query execution SHALL use the same Dashboard Language queries.
This presentation introduces no derived-result store or interactive acquisition.
Canonical operational-value records and typed storage preserve optional period,
coverage, missingness, cohort and evidence metadata; legacy records are not
backfilled with invented proof. Retention remains unchanged.

Movement SHALL compare complete, mature native measurements in the latest
period and the immediately preceding equal-duration period within the same
campaign, subject repository, measure, evaluator fingerprint, unit, direction,
measurement kind and cadence. Interval periods SHALL abut without overlap;
snapshot periods SHALL compare their declared cutoffs, not summed backlogs.
Adoption and evaluation mode describe historical context, not a requirement
that recent comparisons cross adoption. Exact period boundaries and coverage
proof are required; legacy metadata SHALL NOT be inferred. Unknown versions,
missing or stale periods, partial coverage, interim evidence and unsupported
goal directions SHALL remain insufficient evidence. New unavailable assessments
SHALL remain visible and SHALL NOT reveal an older result as the latest value.
Latest selection SHALL precede campaign aggregation so historical report volume
cannot inflate repository counts or hide a newer incomplete observation.

A repository SHALL count once per campaign. It SHALL be insufficient when any
latest retained primary measure lacks comparable movement; otherwise opposing
measure directions SHALL be Mixed, followed by Worsening, Improving, or Unchanged.
Campaign summaries SHALL expose all five counts and measured repository coverage,
rank worsening and mixed evidence before gains, and retain campaigns with no
measurements. Counts SHALL NOT imply complete enrolled-target coverage where an
authoritative target denominator is absent. Observation dates SHALL stay visible;
retained evidence SHALL NOT be represented as a fresh all-clear.

The default headline SHALL expose native change and its actual reference
period, separately from historical adoption association. Eligible cohort and
denominator changes SHALL be exposed when retained. Zero change SHALL NOT imply
target attainment or system health. No anomaly, sustained-drift or change-point
claim is permitted without a separately versioned calibrated assessment.
Measurement duration, cadence, display horizon and reference period SHALL remain
distinct. Changing the display horizon SHALL NOT recompute a frozen evaluator
with different semantics or create a growing lifetime average.

Direction distribution charts SHALL count each latest assessed repository once
under the same quality-gated period policy. They SHALL NOT fill missing
evidence, average native values across measures, infer sustained improvement, or
attribute movement causally to a campaign. Repository drill-downs SHALL preserve
native measurement history, adoption and actual available baseline evidence.
Campaign-wide run context SHALL be labeled as such, never as target-attributed
execution. Missing inputs SHALL propagate unavailable states through the
production query boundary. Active views SHALL requery on database changes within
their abort-scoped subscription lifetime.

Derived query results SHALL be computed from canonical records on demand.
No derived-result generation, metadata pointer, or persistent projection
store is required. Canonical ingestion transactions remain the authority
for atomic source-data updates.

## 31.2 Native Operational Value report evidence

Canonical schema 33 SHALL retain `operationalValueEvidence`: one bounded current
packet per exact target repository, workflow filename stem and frozen
contribution metric ID/version. Capture time is not part of identity. A newer
packet SHALL replace the entire fact array atomically, never merge fields or
facts from different revisions. Stale imports SHALL NOT roll back a newer
capture; a conflicting same-capture packet or changed contract digest under
the same metric version SHALL fail. Original accepted-event dates, native values
and immutable references SHALL remain evidence, not recalculated report state.
Existing `operationalValues` semantics and retention SHALL remain unchanged.

The skill's validated event ledger SHALL export a schema-v2
`operational_value_evidence` JSONL envelope. Normal Activity ingestion,
normalization, phase publication and browser/SQLite/PostgreSQL storage SHALL
retain it without a Run parent. Packets SHALL survive Run-detail expiry and
retain their repository and observed workflow parents; each identity holds only its current bounded
packet. Limits are 20,000 facts and 8 MiB per packet. Typed facts SHALL be stored
as entity-owned relational child values in PostgreSQL, not JSON documents or
persisted report/view models. Fact fields SHALL preserve native scalar types.
Imports SHALL NOT infer rollout authority or trigger evidence acquisition.
Normalized publications SHALL emit ingestion version 5. Version 4 remains
read-compatible within the supported canonical-schema range; optional new
evidence does not make existing phase envelopes invalid.
The retained source path and workflow name SHALL materialize only observed
Workflow identity with unknown state, never registration or live activation.

Explicit hosted publication SHALL use administrator-authenticated
`POST /api/v1/value` with one original schema-v2 envelope, bounded to 8 MiB.
It SHALL require PostgreSQL and already observed repository/workflow parents;
it SHALL NOT enroll targets, execute uploaded code or register a collector.
Hosted mutations SHALL require current administrator OAuth assurance and CSRF;
local PostgreSQL SHALL retain its existing loopback bearer boundary.
SQLite and static Pages SHALL NOT silently substitute for this write endpoint.
Publication and snapshot replacement SHALL share the namespace writer lock.
Typed ownership metadata SHALL preserve directly published packets, their
fact children and required structural parents across snapshot replacement,
without storing a second canonical document. Exact retries SHALL preserve the
revision. Stale/conflicting captures, changed frozen contracts and rewritten
immutable measurements SHALL fail without partial publication.
New committed captures SHALL advance the revision consumed by subscriptions.
Queries transitively reading this collection SHALL include the current
canonical revision in their bounded result-cache identity; ordinary query
cache lifetime and isolation SHALL remain unchanged.

Recurring acquisition SHALL require reviewed registration for an exact
repository/workflow under `control-plane.campaigns.activity.workflow-values`,
with a confined repository-local deterministic collector deployed together
with policy. The skill SHALL stage registration automatically only after the
frozen function and bounded collector pass verification, preserving existing
registrations and explicit `enabled: false` disables. Function presence alone
SHALL NOT grant execution authority or enroll a repository.
An admitted collection SHALL intersect
that selection with effective repository collection scope and the frozen
cadence. It SHALL preserve reusable immutable proof and actual capture/cutoff
timestamps. Collection failure SHALL retain the previous accepted evidence,
not advance freshness or fabricate zero. Disable SHALL stop future acquisition
without deleting retained history. Navigation, report publication and workflow
discovery SHALL NOT infer opt-in. No per-workflow schedule or model assessment
is required for continued measurement. The health-first admission and deferral
contract in section 31.3 governs automatic acquisition.

The single logical source `operational-value-evidence` SHALL purely normalize
these originals. Every row SHALL expose `report-id`, `organization`,
`repository`, `workflow` (filename stem), `workflow-path` (the validated original
Markdown source path), `observed-at` (capture), `window-start`, `window-end`
(evidence cutoff) and `contract-digest`. Original capture/cutoff boundaries
SHALL NOT advance on replay. `fact-kind` and `fact-id` identify each fact;
IDs SHALL be stable and unique within kind and optional parent.
Base-source metadata SHALL retain the oldest included packet cutoff as `as-of`
and its oldest capture as `retrieved-at`; these clocks alone SHALL NOT promote
completeness or freshness to known states.

| Fact kind | Acquired facts |
| --- | --- |
| `contract` | `goal`, `acceptance`, `workflow-name`, `metric-id/name/unit/direction/version/formula`, `result-label`, `measurement-basis`, `cadence-days`, optional `maturation-days`, snapshot metric identity/name/unit/direction/version/formula, output/implementation units, `output-scope`, `output-coverage`, and `outputs-available`, `repository-state-available`, `checks-available` booleans |
| `intent-source`, `requirement`, `limitation` | Source `label/url`, requirement `label`, or limitation `label/detail` |
| `output` | Original output `fact-id`, `label/title/url`, creation `at`, lifecycle `state`, evidence `origin` |
| `output-implementation`, `output-evidence` | Output `parent-id`, accepted-event `related-id`, or retained evidence `label/url` |
| `implementation` | Original event `fact-id`, original acceptance `at` and `accepted-at`, native `value`, `label/title/detail/url`, accepted revision `ref`, evidence-strength `origin` |
| `implementation-evidence`, `implementation-lineage` | Accepted-event `parent-id`, retained `label/url`, or explicit lineage relation `label`, output `related-id`, and separate relationship `at` |
| `snapshot`, `snapshot-item` | Stable snapshot identity, native `at/value/ref/url`, required `items-available` boolean; item `parent-id` names that exact snapshot with retained `label/url` |
| `quality-check` | Named `label`, explicit `status` (`verified` or `not-verified`), `detail` |
| `coverage` | Original interval `at`, `end-at`, `coverage-status` |

Unavailable optional evidence SHALL remain distinct from an explicitly empty
cohort, check array or snapshot-item population. Invalid identities, duplicate
facts, dangling edges, unsafe/non-HTTPS URLs, non-native/non-finite numbers,
mutable references and invalid timestamps SHALL fail rather than become missing.
Independent repository snapshots SHALL remain separate from linked contribution.
All selection, grouping, totals, comparisons, movement, attention and report
presentation SHALL be Dashboard Language queries over these factual rows,
re-executed within the active abort-scoped worker subscription. No navigation
handler SHALL fetch saved reports or acquire missing evidence.

## 31.3 Workflow Value collection health

Canonical schema 35 SHALL retain `workflowValueCollections`, independently of
`operationalValueEvidence`: one current bounded record per exact repository and
workflow filename stem. A schema-v2 `workflow_value_collection` JSONL envelope
SHALL carry the record through the ordinary Activity adapter, normalized
publication, browser IndexedDB, local SQLite and hosted PostgreSQL paths.
Registration and health are collection facts, not outcome measurements or
authorization to execute a target workflow.

The producer SHALL resolve the reviewed policy within effective Activity scope.
It SHALL publish `registration` (`enabled`, `disabled`, or `unconfigured`),
the confined collector path when configured (optional for an explicit disable), a policy digest, the actual
decision `observedAt`, `state`, `reasonCode` and a bounded non-sensitive reason.
States are `unconfigured`, `disabled`, `never-captured`, `deferred`, `failed`
and `available`. Unconfigured requires an observed workflow and an explicit
policy check; absent reports or absent collection records do not establish it.
Never-captured describes the absence of an accepted capture in the retained
collection history, not a claim about unavailable historical archives.
The latest decision replaces its registration, collector, state, reason and
policy facts; stale imports MUST NOT roll them back. Actual attempt history is
an independently reconciled fact, not an optional field overwrite. Health records
retain structural parents without requiring a Run. Decision and attempt clocks
SHALL use UTC with millisecond precision or coarser; equivalent timestamp
spellings SHALL compare as instants and normalize without discarding precision.

`lastAttemptAt` and `lastAttemptOutcome`, when present, SHALL describe an actual
collector process invocation, including a cadence check. Deferral before
invocation MUST NOT advance them. Disabled or removed registrations preserve
recorded attempts and accepted evidence. Last successful evidence capture and
cutoff SHALL come from original accepted packets, never the decision timestamp.
The `workflow-value-collections` logical source and declarative Value views SHALL
keep these clocks separate and show registration health before a first report.
No health observation promotes outcome completeness or freshness.
Explicit disables without a collector path SHALL remain disabled, not
unconfigured. Decision and attempt reconciliation SHALL independently retain
the greatest authoritative timestamp. An older decision may supply a later
known attempt without restoring older registration or policy facts. An older
attempt MUST NOT replace a newer known attempt, even when carried by a newer
decision. Conflicting decision facts at the same decision time, or conflicting
outcomes for the same attempt time, SHALL fail rather than select by arrival.
An absent attempt is unknown, not proof that collection never ran. Merging a
newer decision after cache loss SHALL retain an already known actual attempt;
it MUST NOT synthesize one from the new decision time.
For nonconflicting evidence, replay, permutation and regrouping SHALL produce
the same decision and attempt facts in browser, SQLite and hosted storage.
This bounded current-fact contract does not introduce a complete event ledger,
historical conflict quarantine, or the proposed three-tier storage policy.

Optional Value acquisition MUST NOT delay ordinary Activity publication, consume
its protected API headroom, or hold the hosted projection lease. A separate job
or process alone does not isolate GitHub quota. The existing recurring
subprocess collectors do not participate in shared, per-request REST, search and
GraphQL admission; automatic Activity and hosted cycles SHALL therefore publish
health and retain evidence without invoking remote acquisition. Observed API exhaustion,
fast mode and public-only mode retain their precise deferral reasons; otherwise
the decision SHALL name unavailable shared-budget enforcement. No quota probe,
token mint, historical seed, retry loop or backfill is needed to publish that
decision. Explicit operator evaluation and evidence publication remain separate.

This deferral SHALL NOT disable bounded cache-only or deterministic local
computations. An independent optional lane MAY compute against an already
published, policy-matched eligible Activity snapshot, without target credentials,
quota probes, downloads, automatic backfill, or remote collector execution.
Only the reviewed local-only-capable standard adapter may run there; older or
custom adapters that cannot enforce that contract SHALL defer. Modules must
explicitly declare their local capability; other modules may reuse exact,
version-matched retained observations but SHALL NOT acquire missing inputs.
Existing fast, public-only and API-limited snapshot gates SHALL remain intact.

Actions SHALL complete Activity publication before starting the optional local
workflow. A later Activity snapshot may import its validated, bounded results.
The hosted worker SHALL use its own single-owner lease and a verified private
snapshot, not the projection lease or a long-held core evidence lock. Computation
SHALL be bounded to two minutes per batch and thirty seconds per adapter.
Local output and retained imports SHALL remain within 16 MiB; the hosted private
snapshot SHALL remain within 256 MiB with individual inputs bounded to 16 MiB.
An optional result import failure MUST retain accepted evidence without failing
ordinary Activity publication.
Completed local results SHALL wait for the next ordinary projection, not create
a compute/publication retry loop. Computation SHALL use a frozen copy of policy.
A final short publication rechecks enrollment and compares the completed input
generation, manifest, policy and retained-result identity with the computation's
starting state. Changed inputs or accepted evidence SHALL defer publication,
not overwrite concurrent work. Optional Actions imports SHALL reject conflicting
measurements at the same observation identity without changing retained evidence.
Failure, timeout or withdrawal MUST NOT replace accepted evidence or hold up
ordinary Activity publication. These bounds do not claim isolation from unrelated
host CPU/I/O consumers, Actions transport requests, or external API consumers.

Resuming automatic acquisition requires request-accounted shared admission,
protected core headroom and bounded requests, elapsed time and concurrency,
including other work sharing the credential. Expensive historical acquisition
requires a separately bounded explicit action. Immutable facts may be reused
only under the frozen contract; old mutable observations MUST NOT become current
merely by replaying retained proof.

---

# 32. Bounded Ingestion

The complete source dataset MUST NOT be materialized in memory.

Processing SHALL occur incrementally:

```text
fetch chunk
    |
verify
    |
parse
    |
normalize
    |
batch write
    |
checkpoint
    |
release source objects
```

---

# 33. Write Batches

A million-record import MUST NOT use one giant IndexedDB transaction.

Canonical records SHOULD be committed in bounded batches.

Because browser IndexedDB is reconstructable cache state, implementations
SHOULD request `relaxed` durability for these bounded write transactions when
the browser supports the transaction option, and MUST retain a compatibility
path for implementations that reject it. Implementations MAY call
`transaction.commit()` after synchronously enqueueing a complete write batch.

Reconciliation SHOULD derive removals from the already loaded prior snapshot.
When no prior snapshot is available, it SHOULD traverse primary keys with a
key-only cursor rather than materializing every stored key.

An initial target MAY be:

```text
500–5,000 records per write transaction
```

The final value SHOULD be determined through load tests.

---

# 34. Ingestion Checkpoints

Every completed source chunk SHOULD create a durable checkpoint.

Example:

```js
{
  id: [
    "2026-09-09T05:00:00Z",
    "audits/audits-0042.json"
  ],

  generation: "2026-09-09T05:00:00Z",
  chunk: "audits/audits-0042.json",

  digest: "...",
  status: "committed",
  recordCount: 5000,

  committedAt: "..."
}
```

On restart, a chunk MAY be skipped only when:

```text
generation matches
AND
digest matches
AND
checkpoint status == committed
```

---

# 35. Web Worker Ingestion

Large:

```text
fetching
hashing
JSON parsing
normalization
IndexedDB writes
```

SHOULD occur in a Web Worker.

The main thread SHOULD remain available for:

```text
rendering
navigation
querying
progress UI
user input
```

Progress MAY be communicated as:

```js
{
  phase: "indexing",
  generation: "...",
  completedChunks: 42,
  totalChunks: 180,
  completedRecords: 210000
}
```

Low-overhead diagnostics SHOULD report transaction duration, request count,
records written, records deleted, keys or records scanned, records returned,
committed batches, aborted transactions, and unchanged shard skips. Diagnostic
collection MUST NOT add full-store reads to the measured workload.

Browser import progress MUST reserve at least 10% of the determinate range for
each post-shard stage: retention maintenance, inventory preparation, and active
query refresh. These allocations MUST NOT shrink as the shard count grows.
Both the first-load dialog and the top loading bar MUST consume the same
worker-owned weighted progress, and MUST NOT report completion before query
refresh finishes.

---

# 36. Integrity Validation

Before activation, a generation MUST pass validation.

## 36.1 Manifest

Verify:

```text
supported manifest version
supported source schema
required fields
valid paths
```

## 36.2 Chunks

Verify:

```text
digest
parse success
record-count plausibility
```

## 36.3 Canonical records

Verify:

```text
valid canonical IDs
required fields
expected entity types
```

## 36.4 Relationships

Verify required relationships such as:

```text
Workflow -> Repository
Run -> Workflow
Domain, Tool, Audit, and Issue -> Run
```

Where eventually consistent partial relationships are intentionally allowed, that behavior MUST be explicit.

## 36.5 Completeness

All required chunks MUST have committed checkpoints.

---

# 37. Storage Quota

The browser SHOULD inspect available storage before large ingestion:

```js
const storage = await navigator.storage.estimate();
```

The browser MAY request persistent storage:

```js
await navigator.storage.persist();
```

Correctness MUST NOT depend on persistent storage being granted.

Native PostgreSQL canonical publication SHALL cap the namespace's retained
Run subtrees at 200,000 rows per Run-owned source and 256 MiB of combined
logical typed-row bytes. Byte accounting uses 64 bytes plus the column count
per row and the UTF-8 text byte lengths of its typed column values; it is a
logical resource envelope, not a physical disk, transaction-peak, or RSS limit.
Query resource guards SHALL NOT be raised to accommodate larger history.

After reconciling additive daily contributions, a write SHALL evict the oldest
complete Run subtrees atomically until the newest retained prefix fits every
source and byte boundary. Run time, followed by bytewise Run identity, SHALL
determine deterministic newest-first ordering regardless of execution status.
Complete means relationship-complete, not necessarily execution-completed.
A write that cannot fit the newest subtree SHALL fail and roll back instead of
silently discarding it; durable execution/admission/queue state is not canonical
Run detail and SHALL remain protected independently.
Snapshot ingestion, sparse admission, and conclusion publication SHALL enforce
the same boundary under the namespace writer lock.

Capacity eviction SHALL preserve structural inventory, independent Operational
Value evidence, durable operational state, and daily contribution receipts and
counters. Evicted source coverage SHALL be partial; statistical publication
SHALL sample the retained population without claiming valid full-population
weights. Subsequent authoritative replay SHALL not displace newer Runs with
older subtrees or count surviving daily contributions twice.

---

# 38. Quota Failure

`QuotaExceededError` MUST be handled explicitly.

When storage is insufficient:

1. the failed bounded transaction MUST abort;
2. unpublished derived projection generations MAY be removed;
3. expendable payload/search caches MAY be removed;
4. the canonical retention set MAY be reduced by evicting complete oldest Run
   subtrees;
5. ingestion MAY retry with the smaller relationship-safe batch; and
6. the dashboard SHOULD surface a storage diagnostic.

A quota failure MUST NOT create a successful ingestion receipt or publish an
incomplete derived projection generation.

---

# 39. Storage Recovery Order

When space is required, cleanup SHOULD proceed in this order:

```text
1. unpublished derived projection generations
2. superseded derived projection generations
3. payload cache
4. search indexes
5. oldest complete Run subtrees
```

Canonical storage capping MUST remove a Run and all of its run-owned records
together. It MUST NOT leave orphaned Domain, Tool, Audit, or Issue records.

---

# 40. Browser Eviction

The application MUST remain correct if browser storage is:

```text
evicted
manually cleared
lost in private browsing
unavailable
corrupted
```

Recovery SHALL consist of rebuilding from published authoritative inputs.

---

# 41. Retention

Enterprise-scale deployments SHOULD support local data tiers.

Example:

```text
HOT
recent runs and run-owned records
fully indexed

WARM
summaries indexed
details fetched on demand

COLD
published chunks only
not retained locally
```

Exact periods SHALL remain a product/configuration decision.

The architecture MUST NOT require every browser to retain unlimited execution history.

Ingestion SHALL upsert each collected batch onto the records already retained
instead of replacing them, so a partial collection or worker restart never drops
observations the browser still retains. The browser retains run-linked records
observed within the last seven days and Operational Values within the last
30 days; records observed outside their window SHALL be pruned during the next
ingestion.

Retention pruning MUST remain relationship-safe: a retained record SHALL be
dropped when a mandatory parent no longer survives, and structural parents that
neither the current collection nor any retained descendant references SHALL be
collected.

Token-optimization retention SHALL also satisfy Section 5.5.5 so an active
intervention is not pruned before maturation and compact terminal evidence
remains bounded.

## 41.1 Resolved failure log excerpts

Canonical IndexedDB and its Node.js SQLite adapter SHALL reclaim the optional
Run `failureLog` excerpt after a later completed native success in the same
execution Repository, Workflow, campaign/role, target, event, branch and rollout
mode. Ordering SHALL use the first valid `startedAt`, `createdAt`, or `updatedAt`,
then numeric GitHub run ID and attempt, as in the runtime-health contract.
Unknown ordering or branch, missing worker target or rollout mode, unknown
workflow role without a target association, cancellation, missing conclusions,
and explicitly incomplete outcomes MUST NOT establish recovery.
Lower attempts and older observations MUST NOT overwrite the retained winner.

This first increment SHALL remove only the excerpt, not the Run, its failure
signature/message, accounting, or any run-linked record. Audit summaries and
counts, tool/domain activity, friction, safe outputs, experiment assignments,
grader/eval observations, and intervention/comparison evidence remain subject to
their existing retention rules. Missing excerpts MUST remain missing, not
successful outcomes or zero diagnostic activity.

Unresolved excerpts SHALL use the existing Audit detail window and whole-Run
size cap; a separate full-detail archive explicitly using unlimited detail
retention SHALL remain exempt. A storage-local, attempt-scoped pruning marker
SHALL prevent replay from restoring a discarded excerpt while its Run survives,
without suppressing a new attempt's diagnostic evidence.

Maintenance SHALL use bounded Run reads and accounted writes before size-based
Run eviction, including when source receipts are unchanged. It MUST NOT load
run-owned evidence collections or persist statistical aggregates. Excerpt-only
changes SHALL refresh active query results. This policy does not alter hosted
PostgreSQL, published JSONL, or GitHub runs and artifacts.

---

# 42. Search

IndexedDB indexes SHALL NOT be treated as a full-text search engine.

If full-text search over run-owned records is required, the implementation
SHOULD build a dedicated derived search index.

The search index MUST also be disposable and rebuildable.

---

# 43. Query Layer

Views SHALL use a stable query API.

Example:

```js
repositories.list();
repositories.get(id);

workflows.forRepository(repositoryId);

runs.forRepository(repositoryId);
runs.forWorkflow(workflowId);
runs.recentFailures();

domains.forRun(runId);
tools.forRun(runId);
audits.forRun(runId);
issues.forRun(runId);
```

Dashboard code SHOULD NOT directly scatter IndexedDB transaction logic through views.

The `domains`, `tools`, `skills`, `friction`, `audits`, and `issues` logical
sources SHALL each be an explicit Dashboard Language query over its own
canonical table, with declared joins for run context. Backends MUST NOT
synthesize these queries from a shared event projection or implicitly union
event families. Cross-family views MUST declare their unions in their queries.
This contract applies equally to browser IndexedDB, local SQLite, and hosted
PostgreSQL execution.

Firewall observations SHALL select firewall request events directly from the
canonical domain table and join only their run context. Request totals SHALL
use native grouping and conditional sums, preserving zero counts and the
one-request fallback for missing or null counts. Firewall queries MUST NOT
scan unrelated tool, audit, skill, friction, or issue tables. Run terminal
diagnoses SHALL use the recorded Run terminal outcome and detail, not fields
borrowed from another event family's schema.

Token-optimization pages SHALL query only the three database tables defined in
Section 5.5.5 and their declarative derivatives. They MUST NOT scan Audit
payloads, join source rows, calculate comparability, or rank opportunities in a
presenter or component.

---

# 44. Repository Structure

The completed implementation SHALL use the stable data namespace.

Example:

```text
dashboard/
  data/
    model/
      ids.mjs
      schema.mjs

    adapters/
      dashboard-sources.mjs
      gh-aw-logs.mjs
      sql.mjs

    normalize/
      repositories.mjs
      workflows.mjs
      runs.mjs
      domains.mjs
      tools.mjs
      audits.mjs
      issues.mjs

    storage/
      indexeddb.mjs
      schema.mjs
      generations.mjs
      checkpoints.mjs

    ingest/
      manifest.mjs
      chunks.mjs
      integrity.mjs
      worker.mjs
      coordinator.mjs

    queries/
      repositories.mjs
      workflows.mjs
      runs.mjs
      domains.mjs
      tools.mjs
      audits.mjs
      issues.mjs
```

Temporary parallel namespaces such as `data-v2` or table names such as `canonical-runs` MUST NOT remain after migration. Published source documents MAY remain inputs at the ingestion boundary, but presentation MUST receive only active-generation query results under stable table and query contracts.

After migration, implementations MUST NOT fall back to direct rendering of published source objects, a parallel whole-source browser cache, empty compatibility projections, or main-thread reprocessing after a worker failure. An unavailable or invalid requested generation MUST fail explicitly without bypassing normalization, validation, or activation.

---

# 45. Implementation Sequence

## Phase 1 — Canonical model

Implement only:

```text
Repository
Workflow
Run
Domain
Tool
Audit
Issue

canonical IDs
canonical timestamps
provenance
relationships
```

Do not redesign views.

### Exit criteria

* model definitions exist;
* IDs are deterministic;
* fixture tests pass.

---

## Phase 2 — Source adapters

Implement adapters for current real source inputs.

Priority:

```text
1. current dashboard sources
2. gh-aw logs JSON
3. versioned static SQL export
```

Adapters MUST produce source-neutral normalization inputs.

### Exit criteria

Real fixtures convert into canonical entities without UI involvement.

---

## Phase 3 — Pure normalization tests

Before IndexedDB, assert canonical objects directly.

Example:

```text
logs fixture
    |
adapter
    |
normalizer
    |
expected-model fixture
```

### Exit criteria

Normalization is deterministic and idempotent.

---

## Phase 4 — IndexedDB

Implement:

```text
stores
indexes
generation-aware keys
queries
```

Start with native IndexedDB semantics.

### Exit criteria

Canonical fixtures can be persisted and queried.

---

## Phase 5 — Node integration tests

Use an IndexedDB-compatible Node implementation such as `fake-indexeddb`.

Test:

```text
real fixture
   ↓
adapter
   ↓
normalization
   ↓
IndexedDB
   ↓
query layer
   ↓
assertions
```

### Exit criteria

The full new data path is testable without a browser.

---

## Phase 6 — Authoritative browser cutover

Make the canonical query layer authoritative for every view and remove the replaced browser cache, direct-source reads, aliases, and fallback behavior.

The integration MUST NOT fabricate canonical run-owned records from view-shaped summaries. Adapter-derived identities MUST remain explicitly namespaced when immutable upstream IDs are unavailable.

Compare:

```text
expected fixture semantics
vs
canonical query output
```

### Exit criteria

Every view reads the canonical query boundary, update failures are explicit,
and no legacy browser path remains.

---

## Phase 7 — Derived projection generation safety

Implement:

```text
projection-specific staging generations
metadata compatibility validation
atomic metadata publication
stale-generation cleanup
```

### Exit criteria

Interrupted or failed materialization cannot publish a partial derived
projection.

---

## Phase 8 — Scalable ingestion

Add:

```text
worker processing
bounded batches
checkpoints
quota handling
```

### Exit criteria

Large data can be processed without full-memory materialization.

---

## Phase 9 — Chunked publication

Only after the canonical path is proven, evolve upstream publication toward:

```text
manifest
immutable chunks
digests
```

### Exit criteria

Source ingestion is resumable and independently verifiable.

---

## Phase 10 — View conformance

Verify every dashboard view reads canonical queries after the authoritative cutover.

Views MUST NOT retain fallback source parsing once migrated.

---

## Phase 11 — Delete legacy dashboard data layer

When all supported views use the canonical query interface:

```text
remove replaced browser plumbing
rename data-v2 -> data
remove unused old view-shaped models
```

---

# 46. Node Testing

The new storage abstraction MUST run under Node tests.

A development-only IndexedDB implementation MAY provide the browser API.

Example:

```js
import "fake-indexeddb/auto";
```

Production browser code MUST use native browser IndexedDB.

The same application storage API SHOULD be exercised in both environments.

---

# 47. Required Model Tests

### T-MODEL-001 — Repository identity

Repository rename retains canonical identity.

### T-MODEL-002 — Workflow identity

Workflow path change does not incorrectly duplicate a known workflow.

### T-MODEL-003 — Run attempts

Repeated attempts remain visible in source evidence, and normalization
converges them to one repository-scoped Run identity with the winning
observation's attempt.

### T-MODEL-004 — Run-owned record composition

One Run may own:

```text
tool
domain
audit
issue
```

records.

### T-MODEL-005 — Record ordering

Out-of-order source observations result in deterministic canonical ordering.

### T-MODEL-006 — Partial entities

Incomplete entities can be enriched by later authoritative observations.

### T-MODEL-007 — Idempotence

Ingesting identical observations twice does not duplicate logical data.

### T-MODEL-008 — Unknown record

Unknown non-critical records do not invalidate the complete source dataset.

### T-MODEL-009 — Token opportunity identity

Equivalent frozen assignments from repeated collections and different control
repositories converge to one opportunity. A changed window, assignment Run, or
experiment produces a distinct opportunity.

### T-MODEL-010 — Token evidence comparability

Complete matched variants produce a native verified gain ratio. Incomplete,
incomparable, unmatured, and unavailable fixtures produce null attainment.
Reliability or outcome-quality regression produces zero.

### T-MODEL-011 — Token measure grain

Invocation and Run-aggregate fixtures never double count AIC. Raw token classes
remain separately named and are never synthesized into a total.

### T-MODEL-012 — Token observation idempotence

Repeated source observations preserve distinct provenance while producing one
canonical opportunity, intervention, and comparison identity.

---

# 48. Required Persistence Tests

### T-IDB-001 — Fresh database

A new IndexedDB database initializes correctly.

### T-IDB-002 — Query indexes

Required query paths return expected results.

### T-IDB-003 — Duplicate writes

Idempotent writes preserve record count.

### T-IDB-004 — Interrupted reconciliation

Interrupted bounded writes remain retryable and do not create a successful
ingestion receipt.

### T-IDB-005 — Derived projection activation

Changing a derived projection metadata pointer exposes either generation A or
B, never mixed projection records.

---

# 49. Required Failure Tests

### T-FAIL-001 — Interrupted ingestion

Stop processing after N chunks.

Restart.

Expected:

Committed chunks are reused and remaining chunks continue.

### T-FAIL-002 — Corrupt chunk

Modify chunk content without updating its digest.

Expected:

Generation never activates.

### T-FAIL-003 — Quota exceeded

Cause IndexedDB write failure.

Expected:

The failed transaction aborts, no successful receipt is written, and retry may
reduce storage by evicting complete oldest Run subtrees.

### T-FAIL-004 — Invalid schema

Provide unsupported source schema.

Expected:

Current active data remains available.

### T-FAIL-005 — Broken relationships

Provide invalid mandatory relationships.

Expected:

Generation validation fails.

### T-FAIL-006 — Browser termination before activation

Terminate after staging finishes but before activation.

Expected:

Previous generation remains active.

### T-FAIL-007 — Browser termination after activation

Terminate immediately after activation.

Expected:

New active-generation pointer remains internally valid.

### T-FAIL-008 — Malformed token-optimization evidence

Provide invalid identity fields, an unknown opportunity or intervention enum,
mixed AIC grains, an unresolved mandatory relationship, or a duplicate
comparison identity.

Expected:

The invalid comparison produces no realized savings. Generation activation
fails for structural corruption; evidence-level incompleteness remains visible
with its explicit non-complete state.

---

# 50. Browser Tests

Node-based IndexedDB tests SHALL NOT be the sole conformance test.

The implementation MUST include native browser tests.

At minimum:

```text
Chromium
```

SHALL be tested.

Where supported:

```text
Firefox
WebKit
```

SHOULD also be tested.

Required browser scenarios:

```text
cold start
reload using existing database
full rebuild
generation replacement
corrupt staging data
IndexedDB deletion
interrupted ingestion
```

---

# 51. Large-Data Tests

Synthetic tests SHOULD cover progressive scales.

### Baseline

```text
100 repositories
10,000 runs
100,000 run-owned records
```

### Enterprise candidate

```text
10,000 repositories
1,000,000 run-owned records
```

Additional tests MAY exceed these numbers.

The point is not an arbitrary maximum.

The goal is to measure behavior before declaring production scale.

---

# 52. Performance Measurements

Large-data tests SHOULD measure:

```text
normalization throughput
IndexedDB write throughput
cold-start duration
incremental update duration
query latency
peak memory
database size
chunk latency
activation latency
garbage-collection duration
```

Regression thresholds SHOULD be introduced after baseline measurements exist.

---

# 53. Low-Memory Requirements

Large ingestion MUST NOT require one giant JSON object graph.

The implementation SHOULD be validated under a constrained-memory browser environment.

Tests SHOULD demonstrate:

```text
bounded chunk processing
memory release between chunks
responsive main thread
page-scoped worker query results
no whole-dashboard structured clone
lazy run-owned record detail loading
```

---

# 54. Schema Versioning

The implementation SHALL distinguish:

```text
source schema version
canonical model version
IndexedDB structural version
```

These versions MUST NOT be assumed to advance together.

Example:

```text
source schema: 5
canonical schema: 2
IndexedDB version: 3
```

---

# 55. Schema Migration

Simple IndexedDB structural changes MAY use normal version upgrades.

For substantial canonical semantic changes, rebuilding derived state SHOULD be preferred over complex in-place migrations.

Because IndexedDB is not authoritative:

> Rebuildability is more important than preserving stale derived rows.

Physical schema upgrades SHALL follow §5.1: upgrades from version 44 or 45 with
a `runs` store SHALL preserve existing stores and create only missing stores;
incompatible older projections SHALL be rebuilt. Derived projections that
use generations SHOULD keep the published generation available until compatible
replacement metadata is committed.

---

# 56. Error Taxonomy

Stable ingestion error categories SHOULD include:

```text
SOURCE_FETCH_FAILED
SOURCE_CORRUPT
CHECKSUM_MISMATCH

SCHEMA_UNSUPPORTED
NORMALIZATION_FAILED
RELATIONSHIP_INVALID

TRANSACTION_ABORTED
QUOTA_EXCEEDED

GENERATION_INCOMPLETE
GENERATION_VALIDATION_FAILED

MIGRATION_FAILED
```

Errors SHOULD identify:

```text
generation
phase
source kind
chunk where applicable
human-readable detail
underlying cause where safe
```

---

# 57. Security Model

The data architecture SHALL NOT weaken existing access boundaries.

IndexedDB is NOT an authorization layer.

Any information fetched into the browser MUST already be authorized for the dashboard user.

Sensitive data MUST NOT be made safe merely by hiding it from a view.

---

# 58. Data Minimization

The canonical model SHOULD retain the fields required for dashboard behavior.

Large or highly sensitive raw payloads SHOULD remain out of the hot canonical store unless operationally required.

Do not duplicate source payloads merely because storage is available.

Token-optimization projections SHALL additionally enforce Section 5.5.6.

---

# 59. Full Rebuild Requirement

The following operation MUST always be valid:

```text
delete IndexedDB
        |
reload dashboard
        |
fetch current authoritative generation
        |
rebuild canonical model
        |
dashboard becomes usable
```

This is a primary disaster-recovery path.

---

# 60. Agent Implementation Contract

An implementation agent SHOULD receive:

1. this specification;
2. representative current dashboard JSON;
3. current dashboard source-schema specification;
4. real gh-aw log JSON;
5. representative firewall data;
6. representative safe-output data;
7. the versioned dashboard SQL export contract;
8. representative SQL and exported JSON rows;
9. expected canonical fixtures;
10. tests to satisfy.

The implementation instruction SHOULD be:

> Implement the canonical dashboard data subsystem before modifying dashboard presentation behavior.

Agents SHOULD work in this order:

```text
model
→ adapters
→ normalization
→ tests
→ IndexedDB
→ queries
→ generations
→ browser ingestion
→ views
```

---

# 61. Definition of Done — Canonical Foundation

The foundation is complete when:

* Repository, Workflow, Run, Domain, Tool, Audit, and Issue exist;
* canonical IDs are deterministic;
* run-owned records support multiple producers;
* real fixtures normalize correctly;
* duplicate ingestion is idempotent;
* no dashboard view is required for testing.

---

# 62. Definition of Done — New Data Subsystem

The subsystem is complete when:

* IndexedDB persistence works;
* query API works;
* Node tests exercise IndexedDB interfaces;
* native browser tests pass;
* interrupted canonical writes retry without a successful receipt;
* derived projection generation isolation works where materialized;
* recovery from empty IndexedDB works.

---

# 63. Definition of Done — Enterprise-Hardened

The implementation MUST NOT be described as enterprise-hardened until all of the following have passed:

```text
million-record load test
bounded-memory ingestion
real-browser IndexedDB testing
interruption recovery
chunk integrity rejection
quota-exceeded recovery
atomic generation activation
schema rebuild/upgrade
out-of-order run-owned records
duplicate ingestion
repository rename
workflow rename
run-attempt handling
known-good rollback behavior
```

---

# 64. Explicit Non-Goals for Initial Implementation

The first implementation SHOULD NOT attempt to solve:

```text
unlimited local historical retention
browser-side authoritative state
live browser SQL integration
full-text search
UI redesign
perfect source chunking
complex data migrations
multi-browser synchronization
```

Those concerns SHALL NOT delay establishment of the canonical model.

---

# 65. Cutover Principle

Before browser cutover, build and validate the canonical subsystem without connecting it to presentation:

```text
CURRENT SOURCES
        |
        +----------------> new adapters
                              |
                              v
                         canonical model
                              |
                              v
                           IndexedDB
```

At cutover, views SHALL switch directly to the canonical query layer:

```text
published inputs
  |
  v
 ingestion adapters
  |
  v
canonical queries
        |
        v
      views
```

The replaced browser data plumbing SHALL be deleted in the same completed implementation:

```text
old dashboard-side data plumbing
        |
        X delete
```

---

# 66. Architectural Rules for Code Review

A dashboard data change SHOULD be rejected if it:

* introduces raw log parsing into a view;
* creates a second canonical representation;
* relies on ingestion order for identity;
* makes IndexedDB authoritative;
* exposes an unpublished derived projection generation;
* requires a full dataset in memory;
* silently drops unknown observations;
* introduces random IDs for otherwise stable entities;
* bypasses normalization;
* directly mirrors SQL without domain justification;
* deletes known-good data before validating its replacement.

---

# 67. Recommended First Pull Requests

Implementation SHOULD be decomposed into small reviewable changes.

### PR 1 — Canonical domain model

```text
IDs
entity definitions
pure normalizers
fixtures
tests
```

### PR 2 — Current-source adapters

```text
current dashboard JSON
gh-aw log fixture
SQL fixture
```

### PR 3 — IndexedDB persistence

```text
schema
stores
indexes
query API
Node IndexedDB tests
```

### PR 4 — Authoritative browser cutover

```text
native IndexedDB
background ingestion
canonical query integration
replaced browser data-path deletion
```

### PR 5 — Derived projection generation isolation

```text
projection generations
metadata publication transaction
failure tests
```

### PR 6 — Enterprise ingestion

```text
worker
checkpoints
bounded transactions
quota handling
load tests
```

### PR 7 — Chunked publication

```text
manifest
immutable chunks
digests
```

### PR 8+ — View migration

One coherent dashboard surface at a time.

---

# 68. Final Architecture

```text
                  EXISTING SYSTEM

 GitHub / gh-aw / logs / SQL / activity collectors
                       |
                       v
              authoritative sources
                       |
                       v
==================================================
           CLEAN DASHBOARD DATA BOUNDARY
==================================================
                       |
                       v
                  adapters
                       |
                       v
                normalization
                       |
                       v
            canonical domain model

      Repository → Workflow → Run
                  ├── Domain*
                  ├── Tool*
                  ├── Audit*
                  └── Issue*
                       |
                       v
              generation ingestion
                       |
                       v
             staging IndexedDB state
                       |
                validate completely
                       |
                       v
                 atomic activation
                       |
                       v
                canonical queries
                       |
                       v
                  dashboard views
```

---

# 69. Governing Principles

The implementation SHALL be guided by the following rules:

> **Start clean at the dashboard data boundary.**

> **Existing data formats are inputs, not the new domain contract.**

> **Normalize once, index once, project many times.**

> **IndexedDB is an expendable materialized projection.**

> **Authoritative data must remain capable of rebuilding the browser completely.**

> **Each Run owns ordered Domain, Tool, Audit, and Issue records.**

> **Never replace known-good data with an incomplete generation.**

> **Large datasets must be processed incrementally with bounded memory.**

> **Optimize publication after the canonical model is proven—not before.**

> **Views should flow from the model rather than define it.**

---

# 70. Compliance Checklist

| Requirement                                    | Level                          |
| ---------------------------------------------- | ------------------------------ |
| Clean dashboard-side canonical model           | MUST                           |
| Source adapters                                | MUST                           |
| Deterministic canonical IDs                    | MUST                           |
| Idempotent normalization                       | MUST                           |
| Repository → Workflow → Run hierarchy          | MUST                           |
| Run → Domain, Tool, Audit, and Issue records   | MUST                           |
| Deterministic record classification            | MUST                           |
| IndexedDB derived state                        | MUST                           |
| Full reconstruction after database deletion    | MUST                           |
| Query layer between DB and views               | MUST                           |
| Native browser testing                         | MUST                           |
| Node IndexedDB testing                         | MUST                           |
| Content-addressed ingestion receipts           | MUST                           |
| Retry after interrupted bounded writes         | MUST                           |
| Derived projection generation isolation        | MUST where materialized        |
| Derived projection atomic activation           | MUST where materialized        |
| Bounded ingestion                              | MUST                           |
| Explicit quota handling                        | MUST                           |
| Chunk digest verification                      | MUST when chunking enabled     |
| Resume checkpoints                             | MUST for large-data mode       |
| Web Worker ingestion                           | SHOULD                         |
| Local retention tiers                          | SHOULD for large installations |
| Previous derived-generation retention          | SHOULD where storage permits   |
| Full-text derived search index                 | MAY                            |
| Chunked publishing before canonical model      | SHOULD NOT                     |

---

# 71. References

## Normative

**RFC 2119** — Key words for use in RFCs to Indicate Requirement Levels.

**IndexedDB** — Browser structured database used by the canonical materialized projection.

## Project

**Central Agentic Ops dashboard source schemas** — Existing published dashboard JSON contracts SHALL be treated as source inputs during migration.

**[`gh aw logs` schema](https://github.com/github/gh-aw/blob/main/schemas/logs.schema.json)** — The current command output contract SHALL be inspected for observations that can fill missing canonical data through source adapters.

**gh-aw operational artifacts** — Agent, tool, gateway, firewall, safe-output, and execution observations provide candidate run-owned records.

---

# 72. Request-scoped Overview aggregates

Overview counts and time series SHALL be computed on demand by the declarative
query engine in the data worker from canonical records. The additive daily
run-count layer in §72.2 is the only persisted daily projection; computation
results and other daily metrics SHALL NOT be persisted under this exception.
Queries MUST preserve the
requested time window, UTC day semantics, and source-quality metadata.
Distinct counts MUST NOT be approximated by summing daily distinct counts.

## 72.1 Indexed run aggregate pushdown

The declarative query planner SHALL push additive run aggregations down to
IndexedDB indexes whenever every grouping and filtering key is a queryable
run key path. The `runs` store exposes `byEvent` and `byEventConclusion` in
addition to `byRepository`, `byWorkflow`, and `byConclusion`; `event` is a
queryable string key path. This path is selected from the declarative query
shape, not the query name. Unsupported shapes execute the canonical query
without reading any derived aggregate store.

## 72.2 Additive daily run counts

The `daily-run-counts` source is a separate, versioned projection of observed
logical Runs, keyed by namespace, UTC execution-start date, Repository ID, and
Workflow ID. Namespace is implicit in a scoped browser or SQLite database and
explicit in hosted storage. It SHALL NOT change canonical Run fields, existing
detail storage, or detail-retention policy.

The source exposes `day`, `organization`, `repository`, `workflow`, `total`,
`successful`, `failed`, `approval`, `pending`, and `other`. Counters SHALL be
nonnegative integers. `successful` means conclusion `success`; `failed` means
`failure`, `startup-failure`, `stale`, or `timed-out`; `approval` means
`action-required`. GitHub's underscore spellings `startup_failure`, `timed_out`,
and `action_required` SHALL classify identically to their hyphenated equivalents
without rewriting the recorded Run or receipt conclusion.
An otherwise unclassified Run is `pending` only when status
is present and not `completed`; the remainder is `other`. These five counters
SHALL sum to `total`. Runtime success SHALL NOT imply accepted outcomes or
operational value. Pending counts describe last-observed state, not a live
execution census.

Each logical Run SHALL have one compact contribution receipt independent of
retained detail. A reconciled replacement SHALL atomically subtract the previous
contribution, add the replacement, and replace its receipt. Replays SHALL have
zero net effect; attempt changes SHALL NOT add another logical Run. Date and
Workflow corrections SHALL move contributions between keys. Detail eviction
SHALL NOT subtract contributions or delete receipts. Writes and backfill SHALL
preserve existing canonical records and retention settings.

Only a valid observed execution-start timestamp can assign a UTC day. Undated
evidence SHALL be excluded and reported as incomplete, never attributed to import
time. Exactness means exact counts for admitted, reconciled evidence, not proof
of complete upstream collection. Missing projection state SHALL be unavailable,
not a fabricated empty or zero population.

`workflow-runtime-health-summary` SHALL sum the six counters at
organization/Repository/Workflow grain through the production query boundary.
It SHALL NOT union daily totals with retained Runs or fall back to detail when
the source is unavailable. The existing workflow route element owns metric
labels rather than declarative encodings; its metadata-only presentation SHALL
label these as daily observations, distinguish partial coverage, and retain the
separate usage-telemetry window. It SHALL NOT derive counts or query data.
`runs-daily-conclusions` SHALL compose the five health classes from summed daily
counts through declarative queries, not raw conclusion strings or retained
Runs. Its chart SHALL label these as runtime health, preserve aggregate coverage
and unavailable states, and keep the retained Runs table on its detail source.
Time bounds select whole UTC start-day buckets by their midnight; they SHALL
NOT be presented as exact sub-day execution totals. The chart SHALL preserve
zero-valued classes for an observed day without fabricating unobserved days.
Selected context filters outside organization, Repository, and Workflow SHALL
fail the daily-chart request explicitly rather than silently include unselected
Runs; in particular the projection cannot answer rollout-mode selections.

`repository-run-inventory` SHALL preserve registered Repository identities,
drill links, stable top-100 ordering, and the separate full Table inventory,
while summing `repository-observed-run-counts` from the same daily UTC buckets.
Counts describe logical Runs, not attempts or retained-detail volume. Scope
filters SHALL reach the daily join, including explicit rejection of unsupported
dimensions. A daily aggregate joined to inventory SHALL retain its aggregate
provenance and partial coverage; unavailable aggregate evidence SHALL fail the
query rather than become left-join zeros. Zero counts mean no admitted dated
Runs in the selected buckets, not confirmed inactivity. Repository usage and
Table metrics SHALL remain on their existing sources.

Only supported dimensions and complete-day predicates
are eligible; sub-day filters, arbitrary Run predicates, distinct counts,
costs, token usage, and ordered incident evidence remain on their existing
sources. No other query is implicitly routed to the new layer.

Daily counts and their compact receipts SHALL retain the current UTC
Monday-start week and its two preceding weeks. Maintenance SHALL atomically
remove expired buckets and receipts and persist a monotonic retention watermark;
a clock regression SHALL NOT reopen an expired week. Browser IndexedDB and
Activity SQLite SHALL perform bounded logical cleanup independently of detail
retention, including the SQLite doctor publication path. Hosted PostgreSQL SHALL
apply the same logical retention within its namespace transaction. Future days
outside the current week SHALL NOT create retained buckets.

After rollover, replay of an expired execution-start day SHALL NOT recreate a
bucket. A correction crossing the retention boundary requires a surviving
receipt and authoritative revision evidence. A retired contribution SHALL
retain a compact replay guard until its original retained day expires, so a
stale in-window replay cannot resurrect it. Undated receipt retention SHALL use
observation evidence only for cleanup, never as an execution-start date.
Backfill SHALL checkpoint bounded batches atomically with their receipts and
resume without double counting. Existing databases cannot reconstruct
already-evicted history: backfill coverage SHALL remain partial and describe
available evidence rather than claim historical completeness. Sampling (§5.1.1)
and native capacity limits (§37) remain independently enforced contracts, not
new aggregate metrics. Full scope-level evidence reconciliation from the
[three-tier proposal](../adr/three-tier-operational-storage.md) remains separate work.

### Daily run-count conformance

Run `POSTGRES_URL=... npm run test:conformance:daily-run-counts` from the
repository root against an isolated PostgreSQL database whose test role can
create schemas. The suite requires real PostgreSQL and rejects skipped or
unexecuted Go tests; an unset connection is a failure, not a passing backend.
Docker must be running with `node:24-bookworm` available (`docker pull
node:24-bookworm`). The Activity runtime test downloads checksum-verified GitHub
CLI, jq, and the policy-pinned gh-aw collector before executing the checked-in
workflow in a credential-free, network-isolated container. It runs a loopback-only
GitHub simulator, not a mocked `gh aw logs` command; missing tools or Docker fail
the suite rather than skip coverage.
Chromium must be installed for the native-browser test; Node's IndexedDB
emulation alone is not conformance (§50). Browser reports also reject empty,
skipped, failed, or retry-only success. Node and Vitest reports reject empty,
skipped, or todo tests. Every runner MUST report a passing result for each
declared coverage test; a successful process with other passing tests does not
cover a required test omitted by inherited selectors such as `GOFLAGS=-skip`.
Tests create and remove isolated
PostgreSQL schemas. Scratch files use
`.tmp/drc/` within the checkout (short enough for Chromium's Unix socket).

The machine-readable coverage contract is
`tests/contracts/daily-run-counts.json`. Its enforcement tests require existing
test evidence for every listed requirement and ensure Go selectors include that
evidence. The command runs these existing production-boundary suites:

| Suite | Implemented contract exercised |
| --- | --- |
| Activity SQLite | CLI normalization, phased ingestion, doctor repair, replay, publication, and bounded legacy backfill |
| Activity Actions runtime | Full checked-in `cao-activity.yml` planning, collection with real gh-aw and simulated GitHub logs/artifacts, phased ingestion, SQLite digest publication, cache restoration, replay, higher-attempt correction, recycle-only reconstruction without API reads, and shipped daily health query after detail expiry |
| IndexedDB and worker | Shared reconciliation cases, exclusive UTC counters, atomic corrections, logical retention, resumed backfill, unavailable state, and all three migrated query consumers |
| Native Chromium worker | Real IndexedDB and a module Web Worker preserve all three migrated consumers through corrections, detail expiry, aggregate rollover, and unavailable state |
| Native CLI | Canonical SQLite daily source and unavailable legacy snapshots |
| Hosted Go SQLite | Read-only aggregate source, retained provenance, unavailable snapshots, and repository inventory joins |
| Hosted PostgreSQL | Native transactional reconciliation, explicit nulls, bounded backfill, namespace isolation, retention rollback/replay fences, and aggregate join provenance |

The worker suites ingest canonical Runs and resolve shipped queries through the
production worker boundary rather than synthesizing daily source rows. This
conformance command covers only §72.2's implemented additive projection. It does
not assert the proposed three-tier architecture's sampling, hot-tier limits,
additional metrics, native weekly aggregate partitions, or full scope-level
evidence reconciliation.

The `daily-run-counts-conformance` job in `.github/workflows/cgo.yml` runs this
same command with a PostgreSQL service, Docker, and native Chromium on relevant pull
requests and main-branch pushes. Root unit CI also enforces the coverage contract
and verifies that this cross-backend job remains wired.

## 72.10 Data-worker heap budgets

The data worker's peak heap SHALL be measured against the deployed
dataset rather than synthetic fixtures. Measurement MUST read the heap
of the dedicated-worker isolate itself, because `performance.memory`
and `performance.measureUserAgentSpecificMemory` are unavailable inside
a Chromium dedicated worker; the supported technique is a raw Chrome
DevTools Protocol connection to the worker target followed by
`Runtime.getHeapUsage`.

Measurement MUST fail closed: a run that records no worker heap sample
MUST fail rather than vacuously satisfy its budget. Budgets SHALL bound
Overview rendering, post-render retained heap, and whole-dataset
ingestion separately, because ingestion dominates the peak.

## 72.11 Eager ingestion diagnostics

The worker ingests activity shards lazily and publishes a run-phase
result before later phases complete. For diagnostics and measurement,
the `debug-eager-ingest` parameter SHALL force every published shard to
be ingested before results are published, disable any shard limit, and
suppress run-phase-only publication. It is a diagnostic control only
and MUST NOT change canonical results.

---

# 73. Request-scoped computations

The versioned measures defined by the
[CAO Computations Specification](computations.md) SHALL run as bounded,
request-scoped queries over canonical evidence inside the data worker. The
query boundary owns partition selection, measure evaluation, and bounded
result delivery; views and effects MUST NOT reconstruct measure logic.

Computation results MUST NOT be persisted as separate `computationResults`
or `computationMetadata` stores, or used to grant authority. A missing or
incomplete source MUST produce an explicit unavailable or partial result,
never a healthy zero. Failed runs drill-down SHALL request only the selected
partition and retain an abort-scoped subscription so canonical changes update
its result. An ineligible orchestrator gate MUST NOT imply a healthy worker
set or enumerate unrelated worker partitions.

---

# 74. Change Log

## Version 1.8.3 — Removed Go row evaluator fallback

* Corrected §5.2 to require a fail-closed error for unsupported Dashboard
  Language shapes instead of describing evaluation in a bounded Go query
  engine; the hosted PostgreSQL path no longer retains a Go row evaluator, and
  the response contract keeps `fallbackOperations` only as an always-empty
  legacy compatibility field.

## Version 1.8.2 — Cross-workflow evidence relationship rejection

* Required every `experimentAssignments`, `graderObservations`, and
  `evalObservations` record to reference a definition whose `workflowId`
  matches the owning Run's `workflowId`, and required every ingestion path,
  including Activity's pre-publication consolidation, to exclude a mismatched
  record before the canonical browser database, SQLite mirror, or Postgres
  store accepts it.

## Version 1.8.0 — Indexed run aggregate pushdown and worker heap budgets

* Added §72.9, requiring the declarative planner to push additive run
  aggregations down to IndexedDB indexes based on query shape alone, never on
  query name, alongside the existing materialized projection.
* Added the `byEvent` and `byEventConclusion` run indexes and the `event`
  queryable string key path, raising IndexedDB to version 21.
* Added §72.10, requiring deployed-data measurement of the data worker's heap
  through a raw DevTools Protocol connection to the worker isolate, and
  requiring unmeasured runs to fail rather than pass vacuously.
* Added §72.11, defining `debug-eager-ingest` as a diagnostic control that
  forces full shard ingestion without changing canonical results.

## Version 1.7.0 — Implemented storage alignment

* Defined the canonical model, IndexedDB, SQLite-backed IndexedDB, and SQL
  interchange as four separately versioned contracts.
* Recorded the exact IndexedDB version 20 stores, key paths, and indexes.
* Clarified that local SQLite mirrors IndexedDB logical stores through metadata
  and JSON record tables rather than defining a separate relational domain
  schema.
* Updated SQL export references to version 3.
* Aligned canonical Run identity with repository coordinates plus GitHub run ID
  and retained attempt as source evidence rather than identity.
* Replaced unimplemented canonical generation activation requirements with the
  bounded reconciliation, ingestion receipt, and retry contract.
* Scoped generation activation to materialized derived projections and marked
  the computation projection as a future physical-version contract.

## Version 1.6.0 — Orchestrator-first computation gate

* Required orchestrator runtime evaluation before worker-target enumeration.
* Defined worker-evaluation state in computation metadata.
* Required blocked and indeterminate gates to publish without scanning workers.
* Preserved worker Runs as historical evidence without allowing them to
  override the current orchestrator-gated Campaign result.
* Added ordered orchestrator and worker-target Run indexes with early
  success-boundary termination and ingestion-maintained fingerprints.
* Added canonical worker target identity to Run evidence.

## Version 1.5.0 — Target-aware computation partitions

* Defined worker computation partitions by Campaign, Workflow, and target
  Repository.
* Added target-aware result fields and a Campaign-target index.
* Required per-partition success boundaries and target-scoped invalidation.

## Version 1.4.0 — Materialized computation projection

* Defined generation-scoped computation result and metadata stores.
* Defined native-count, Campaign, scope, and readiness indexes.
* Defined phased runtime, clustering, audit, and action publication.
* Defined lazy selected-partition computation, cache compatibility,
  invalidation, failure preservation, and drill-down behavior.

## Version 1.3.0 — Daily Overview aggregate projection

* Classified Overview sources as additive, snapshot/global, or
  non-additive for daily-aggregate eligibility.
* Defined the daily overview aggregate projection, its storage schema,
  generation/versioning discipline, query-layer eligibility rules,
  diagnostics, and fail-closed behavior.

## Version 1.2.0 — Specialized run-owned records

* Replaced the Event table with Domain, Tool, Audit, and Issue tables.
* Classified MCP, Bash, and skill calls as Tools.
* Unified issues and pull requests using `isPullRequest`.
* Bumped derived browser and SQLite cache contracts without legacy recovery.

## Version 1.1.0 — Run-owned events

* Removed Job and Session from the canonical domain model and persistence.
* Linked every Event directly to its owning Run.
* Updated SQL export, IndexedDB, retention, query, and ingestion contracts.

## Version 1.0.0 — Draft

* Established clean dashboard-side data boundary.
* Classified current dashboard schemas as source inputs.
* Defined the initial canonical execution hierarchy.
* Defined heterogeneous operational transaction logs.
* Defined deterministic source adapters and normalization layer.
* Defined missing-data discovery and normalization from the `gh aw logs` schema.
* Defined IndexedDB as disposable derived state.
* Added generation-aware browser storage.
* Added staging and atomic active-generation replacement.
* Added bounded ingestion and checkpoints.
* Added immutable chunk publication model.
* Added integrity verification.
* Added Web Worker ingestion guidance.
* Added storage quota and eviction recovery.
* Added large-payload indirection.
* Defined token-optimization source authority, canonical identities,
  comparability, SQL/IndexedDB parity, retention, and fail-closed behavior.
* Added retention tiers.
* Added Node and real-browser test requirements.
* Added large-data and failure test suites.
* Added clean migration sequence and PR decomposition.
* Established enterprise-hardening definition of done.
