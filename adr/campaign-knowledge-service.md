# ADR: Lazy, storage-independent campaign knowledge services

## Status

Proposed - 2026-11-09.

This record proposes a shared knowledge capability for CAO campaigns. It does
not implement a service, select or provision a backend, install models, register
campaign schemas, or authorize collection, writes, dispatch, or live rollout.
Existing specifications and reviewed policy remain authoritative.

The [supporting research](research/campaign-knowledge-service.md) preserves the
literature, project and API comparisons, 120 references, capacity assumptions,
and non-normative examples behind this decision.

## Context

Campaigns repeatedly analyze repositories and produce useful structured data,
findings, relationships, and tested lessons. Reacquiring that knowledge is costly;
persisting unreviewed output without scope, evidence, and correction semantics
can instead amplify stale or false claims.

The motivating workload is a few campaign/agent families operating across up to
100,000 repositories, with small experimental variants. It is not 100,000
continuously reasoning assistants. Data can remain persisted without maintaining
or indexing every repository on every run.

A generic campaign knowledge database and logical graph is more useful than a
prose-only memory API. Campaigns may have different schemas and proprietary
embedding algorithms. Neither Git, one database engine, a vector index, nor a
universal embedding model should determine the consumer contract.

CAO already separates
[canonical dashboard data](../specs/dashboard-data.md) from
[focused operational services](../specs/server-operational-storage.md).
The existing repository-memory collector and loader provide bounded ref/file
transport, not this proposed knowledge lifecycle. The operational adapter named
`memory` is volatile process state, not durable campaign knowledge.

## Decision

### 1. Own small campaign-facing contracts

Provide focused knowledge record, graph, and representation services, separate
from evidence acquisition, policy/authorization, maintenance coordination, and
dispatch. Runtime consumers retain only the contracts they use. Composition
selects adapters and validates required capabilities.

Campaigns store schema-versioned structured data, not only facts rewritten into
natural language. The logical model separates:

| Component | Responsibility |
| --- | --- |
| Identity/head | Stable scoped key or ID, current accepted revision, serving block |
| Revision | Immutable bounded payload, schema/type, applicability, writer and operation identity |
| Evidence | Exact source/version/locator, support or contradiction, reverse cleanup lookup |
| Relationships | Version-specific provenance/dependencies and separately typed campaign edges |
| Representation space | Compatible algorithm/profile identity, source revision, projection checkpoint |

These are logical responsibilities, not a required table count. Lifecycle
dependencies need eligibility and acyclic derivation handling; campaign semantic
graphs can contain cycles and require bounded traversal.

Resolve tenant, campaign/version, namespace, repository or shared-pack scope,
family, and experiment arm from trusted context. A path, partition key, schema,
vector, or caller-supplied namespace is not an authorization grant. Shared lesson
promotion is an explicit disclosure decision, not an automatic consequence of
similarity or popularity.

### 2. Make routine maintenance demand-driven

Routine work requires **permitted + active + dirty + budget-admitted**:

- Permitted means current reviewed policy allows the exact capability and scope.
- Active means an actual authorized consumer or explicit bounded maintenance
  request holds an activation window for that scope.
- Dirty means eligible input versions differ from the completed checkpoint.

Stored records in an `active` lifecycle state, installed campaigns, and newly
published models do not activate maintenance. One repository or representation
space must not activate its siblings or the whole campaign.

Reuse suitable durable job, lease, fencing, and budget services. Coalesce
activation renewals and dirty events; do not write a knowledge revision or Git
commit on every read. Check activation expiry at access/admission without
per-scope timers. Expired work loads no model, and stale workers cannot publish.
Reconcile exact relevant inputs on next use so missed events do not imply
unchanged state.

Server startup registers adapters and bounded configuration. It does not
enumerate all families, clone repositories, hydrate all records, replay complete
history, or load embedding models. Exact record reads require no model. Cache
and model resources have explicit limits and lifetimes.

Required revocation, compromise response, retention, and erasure are independent
of activation. Block serving and stale queued writes immediately, then complete
targeted durable cleanup to the required deadline, including inactive indexes
and known copies.

### 3. Keep backing structures behind explicit capabilities

Allow relational, key/value, document, object/manifest, log/snapshot, graph, Git,
or deliberate composite storage where the required semantics hold. Prefer
suitable existing deployment resources before introducing another service.

Require scoped bounded access, immutable version identity, conditional
publication without lost edits, idempotency, explicit conflicts and errors,
current revocation/deletion barriers, declared durability, cancellation, and
resource bounds. An adapter must reject unsupported enabled requirements.

One scope has one authoritative composition. Immutable payloads, a conditional
head registry, and a disposable search projection can be separate components,
but successful independent writes are not a cross-resource transaction.
Publication and job delivery need an explicit durable atomicity or reconciliation
protocol. No dual-master writes or silent backend/stale-cache fallback.

Git remains an optional low-churn storage or review/export adapter. Repository
ACLs are not per-record ACLs; a ref update is not a general expected-head CAS;
deleting a file does not erase historical blobs, clones, forks, or caches.
Likewise, a vector index alone does not supply the canonical lifecycle.

Keep provider SDKs, database handles, SQL, Git commands, and physical layouts
inside owned adapters. Reusing an existing PostgreSQL deployment does not merge
knowledge ownership with canonical dashboard entities or operational state.

### 4. Support campaign-owned proprietary embeddings

Campaign workers may compute representations with their reviewed algorithms,
code, weights, and permitted providers. Private implementation artifacts stay
in approved private execution/storage, and credentials remain in owned secret
services. The HTTP knowledge service does not run unreviewed campaign callbacks.

Separate a selected algorithm profile from an immutable embedding-space ID.
Compatibility includes algorithm/model release, query/document encoders, input
schema and preprocessing, representation shape/encoding, metric, normalization,
and relevant task conditioning. Equal dimensions do not imply compatibility.
Use opaque profile identities where naming would disclose proprietary details.

Publish outputs against exact record revisions with producer and operation
provenance. Validate ownership, representation shape, size, supported metric,
finite values where applicable, and current source eligibility. Dense, sparse,
multi-vector, and custom representations are explicit capabilities, not silent
conversions to one default vector format.

A semantic query selects its space and compatible query representation/encoder.
Provider failure does not switch to a public model, another space, or lexical
results. A separately declared multi-channel request can combine honestly labeled
results; distances across independent spaces are not inherently comparable.

Model changes create side-by-side spaces and staged selectors. Rebuild only
activated eligible inputs, preserve experiment isolation, and prevent stale
vectors or old jobs from resurrecting erased revisions.

### 5. Separate extraction, graph traversal, recall, and synthesis

Use distinct versioned operations for:

- exact record reads;
- reviewed structured selection/projection/aggregation;
- bounded graph traversal;
- explicit-space relevance search;
- paginated or job-backed export; and
- optional generated synthesis with citations.

REST, SDKs, GraphQL, and MCP are transport choices, not query or authority
semantics. Ordinary agents do not receive unrestricted native query strings or
database credentials.

Reuse [Dashboard Language](../docs/dashboard-language-specification.md) semantics
for compatible reviewed structured sources. Its current grammar does not by
itself establish arbitrary campaign graph traversal or proprietary-space search.
Those require explicit owned contracts, not injected source names, scripts,
another general-purpose DSL, or main-thread query callbacks.

Results distinguish availability, exact versus ranked selection, pagination,
and evidence coverage. A top-k recall or generated answer is not exhaustive
extraction. Bind opaque cursors to scope, query/operands, stable ordering,
snapshot/version and expiry; recheck current authority on every page. Reject a
required snapshot when the adapter cannot provide it.

Bound input work, graph expansion, candidates, encoder calls, emitted bytes,
time, and cancellation, not only output rows. Preserve the existing query
engine's fail-closed excess/cancellation semantics. Partial progress is available
only through a separately declared and accepted operation contract.

### 6. Keep admission and knowledge mutation evidence-backed

Treat models as proposal producers. Authorize and sanitize inputs before model
submission; perform cheap identity/deduplication first; use optional utility or
relation triage; generate only needed supported changes; rescan outputs; then
publish through the trusted service.

Recheck expected versions, policy/source eligibility, namespace/arm, serving
blocks, fencing, and deletion epochs at publication. Keep model calls outside
short locks/transactions. Contradictions without an authoritative winner remain
conflicted; repeated retrieval is not independent corroboration.

Use immutable shared releases, compact local records and sparse experiment
overlays rather than copying a brain per repository/variant. Sticky assignment,
separate evolving arm stores, and overlay tombstones preserve comparison
boundaries. Promotion needs held-out outcomes and disclosure review.

Never let utility, popularity, a confidence score, or a remembered procedure
grant execution authority. Required-control failures halt persistence; quality
fallbacks must satisfy the same controls. Scanner/verifier success is bounded
evidence, not a guarantee of truth, privacy, or compliance.

## Alternatives considered

| Alternative | Why not the default |
| --- | --- |
| Git as the mandatory database | Useful history/review, but repository-level disclosure, write limits, contention and historical erasure do not fit every campaign |
| One relational schema as the consumer API | A viable adapter, but couples campaigns to physical tables and does not accommodate all representation/query needs |
| One universal vector model/index | Couples algorithms and silently conflates incompatible spaces; relevance does not implement record/graph lifecycle |
| One store/model per repository or variant | Multiplies resident resources and copied state; sparse overlays and scope-local activation are simpler |
| Nightly reflection over the entire fleet | Costs scale with enrollment rather than demand; synthesis can degrade evidence and recall |
| Adopt a complete memory framework unchanged | Current project defaults and evidence differ on locality, maintenance, concurrency, isolation, and erase semantics |
| A universal policy/storage/dispatch service | Mixes authority and persistence; domain-owned contracts preserve separate responsibilities |

## Consequences

Campaigns can share infrastructure without sharing private data, algorithms,
or experimental state by default. Storage and embedding implementations can
change without redefining every consumer. Minimal idle work and exact scoped
access come before expensive semantic indexing.

The cost is explicit capability validation, provenance/reverse locators,
publication recovery, schema/profile versioning, and multi-adapter conformance.
Not every backend supports every feature; uniform interfaces must preserve
intentional limitations rather than hide them.

Provider storage costs and required safety maintenance remain even when routine
CAO work is idle. No throughput, erasure latency, model quality, or cost reduction
is established by this decision. The research's four-million-record and
40,000-expensive-run scenarios are arithmetic, not a proposed daily memory scan
or rollout authorization.

## Acceptance and follow-through

Before any implementation claims readiness:

1. Define the owned knowledge/graph/representation contracts, approved schema and
   profile registration, exact source eligibility, and atomicity/recovery
   semantics. Existing operational and dashboard contracts remain separate.
2. Select a suitable real adapter and test concurrent updates, next-read
   visibility, retries, partial publication, reverse lookup, revocation/erase,
   cancellation, namespace/arm isolation, and unsupported capabilities.
3. Demonstrate inactive-fleet zero routine content/model/provider work,
   coalescing, fencing, bounded resource lifetimes, no sibling-space fan-out,
   and safety cleanup independent of activation.
4. Validate exact queries, snapshots/cursors, graph cycles/budgets, incompatible
   spaces, query/document encoders, stale vector heads, and provider errors
   without fallback. Missing/skipped backends are incomplete evidence.
5. Evaluate accepted task outcomes and total cost against exact-key/rule-based
   baselines before enabling broader retrieval or learned maintenance.

Implementation must update the relevant normative specifications and generated
HTTP/schema contracts when those boundaries are actually introduced. The
research's memory-only SQL sketch does not encode or validate the complete
generic campaign model. This ADR alone adds no server route, selector, workflow
capability, dependency, policy grant, or production fallback.
