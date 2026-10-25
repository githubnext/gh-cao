---
title: Central Agentic Ops Server Ingestion Specification
description: Normative requirements for the optional server-side, webhook-driven CAO evidence acquisition profile.
version: 1.0.0
status: Working Draft
editors:
  - GitHub Next
---

# Central Agentic Ops Server Ingestion Specification

**Version:** 1.0.0
**Status:** Working Draft
**Latest Version:** https://github.com/githubnext/gh-cao/blob/main/specs/server-ingestion.md
**Editors:** GitHub Next

## Abstract

This specification defines an **optional** acquisition profile in which the CAO
server collects Activity evidence directly from a GitHub App, driven by
webhooks, instead of consuming a snapshot published by GitHub Actions.

It defines profile selection and exclusivity, enrollment, event admission,
collection, the evidence lake, projection, cold start, gap recovery, rate-limit
governance, authority, and failure behavior.

Its rate-limit governance applies to outbound GitHub API capacity. Inbound
dashboard request abuse protection is defined separately by
`specs/server-rate-limiting.md`.

It does not define dashboard presentation, workflow rollout policy, or durable
operational outcomes. It does not define the canonical data model, the shard
format, or the `gh aw` audit mapping: those remain owned by
`specs/activity.md` and `specs/dashboard-gh-aw-jsonl-mapping.md`.

This specification **conforms to** `specs/activity.md`. It supersedes nothing.
Nothing in this document changes the obligations of the Actions profile.

## 1. Status and conformance

The key words MUST, MUST NOT, REQUIRED, SHALL, SHALL NOT, SHOULD, SHOULD NOT,
RECOMMENDED, MAY, and OPTIONAL are to be interpreted as described in RFC 2119.

A **conforming server ingestion implementation** satisfies every MUST in this
document. An implementation that does not implement this profile at all remains
conforming to CAO; this profile is OPTIONAL.

The default deployment uses PostgreSQL operational storage and preserves this
document's durability and multi-process requirements. Explicit Redis deployments
preserve those same requirements. An explicitly acknowledged single-process memory
deployment MAY use the volatile exception in
[`server-operational-storage.md`](server-operational-storage.md). Only restart
durability and deployment-wide coordination are relaxed: atomic webhook
admission, scope/ownership checks, signatures, OAuth, quota governance, bounded
work, and explicit incomplete recovery remain mandatory. Its startup gates
admission on complete fresh scope enumeration and reuses the same evidence lake,
projector, collector, and backfill, not a second ingestion implementation.

## 2. Profiles

CAO defines two acquisition profiles.

- The **Actions profile** is the default. Activity publishes an immutable
  snapshot directory, and the server ingests it. It is specified by
  `specs/activity.md` and is unaffected by this document.
- The **collector profile** is defined here. The server acquires evidence
  directly from a GitHub App.

### 2.1 Exclusivity

A deployment MUST select exactly one acquisition profile.

- An implementation MUST treat a configuration that selects both profiles as a
  fatal startup error.
- An implementation MUST NOT allow both profiles to write the same canonical
  dataset, concurrently or interleaved.
- When no collector configuration is present, the implementation MUST behave as
  the Actions profile, including its default reconciler.

### 2.2 Equivalence

Both profiles MUST produce identical canonical records for the same observation
window and the same underlying GitHub activity.

- The collector profile MUST acquire logs with the same `gh aw logs --audit`
  invocation and compact them with the same `activity/cao.mjs` commands the
  Actions profile uses.
- The collector profile MUST reconstruct retained operational-value observations
  with a native Go implementation conforming to
  `specs/operational-value-history.md`, using the same campaign adapter contract,
  retention window, historical campaign selection, and installation-scoped
  GitHub credentials as the Actions profile.
- An implementation MUST NOT maintain a second implementation of the canonical
  mapping, the shard format, or the payload-hash manifest.
- A conforming implementation MUST provide a profile equivalence test that
  asserts identical canonical records from an Actions-published directory and a
  server-collected evidence lake covering the same window.

### 2.3 HTTP host independence

The acquisition profile is independent of which process owns the HTTP
listener. When an external host serves CAO's handler, it MUST start the
selected ingestion profile before admitting requests and MUST allow CAO's
background work to stop only after the HTTP host has drained. The external
host MUST NOT bypass CAO's webhook verification or independently infer
collection scope from requests. The mutual-exclusion, enrollment, durable
admission, and recovery requirements in this document remain unchanged.

## 3. Enrollment

In the collector profile the ingestion scope is the set of repositories on which
the GitHub App is installed.

- The implementation MUST maintain a durable enrollment set of installations and
  their repositories.
- The enrollment set MUST be updated from `installation` and
  `installation_repositories` events and from cold-start enumeration.
- `cao.json` is rollout policy. An implementation MUST NOT derive ingestion
  scope from `cao.json` in this profile, and MUST NOT treat enrollment as
  authority to act on a repository.
- Enrollment coverage MUST be reported as source metadata so partial coverage is
  visible rather than rendered as a complete zero.

## 4. Event admission

The implementation MUST receive events on the existing CAO webhook endpoint.

- The endpoint MUST verify `X-Hub-Signature-256` in constant time, require
  `X-GitHub-Delivery` and `X-GitHub-Event`, bound the request body, deduplicate
  deliveries, and respond `202` before performing work. These obligations are
  unchanged by this profile.
- A delivery whose signature is invalid, whose installation is unknown, or whose
  repository is not enrolled MUST be rejected. An implementation MUST NOT infer
  enrollment from the payload.
- When the collector profile is selected, admission MUST enqueue a collection
  task and MUST NOT perform projection in the request path, and MUST NOT take a
  global projection lease.
- A default-branch `push` MUST enqueue an inventory-only refresh when any added,
  modified, or removed file matches the top-level
  `.github/workflows/*.lock.yml` pattern. Other branches, tags, deleted refs,
  Markdown sources, ordinary `.yml` workflows, `.yaml` files, and nested paths
  MUST NOT trigger refresh. Incomplete/truncated commit lists MAY conservatively
  request refresh; admission MUST NOT fetch commit details from GitHub.
- Inventory-only refresh tasks MUST have independent debounce keys from run
  collection tasks. Workers MUST recheck installation ownership and persist the
  coalesced projection request before acknowledging the task, without acquiring
  run logs for a push.
- Installation scope changes MUST be queued atomically with delivery
  deduplication, not applied by the HTTP receiver. Positive discovery hints MUST
  coalesce by installation and enumerate current authenticated coverage rather
  than reuse the first event's repository list. Discovery MUST reserve shared
  installation quota for each page and publish only complete, bounded results.
  Withdrawals MUST remain independently queued and persist their erasure work
  before acknowledgment. Repository lifecycle hints from a known
  installation MAY queue discovery of a not-yet-enrolled repository, but MUST
  NOT directly grant enrollment.
  Complete enumeration MUST reconcile the previous installation membership,
  excluding archived repositories and removing coordinates no longer returned.
  Durable erasure work MUST precede membership removal; failed or incomplete
  enumeration MUST NOT remove repositories. Verified active repositories MUST
  have durable initial collection work before the refresh is acknowledged.
- Admission and background refresh MUST export OpenTelemetry spans, counts,
  durations, and bounded outcomes, including deduplication and coalescing.
  Persisted trace correlation MUST contain only W3C `traceparent`, not baggage,
  raw webhook data, credentials, or per-repository metric labels.
- Run-collection tasks MUST be keyed by repository. An implementation MUST
  collapse multiple pending run events for one repository into a single
  collection. Issue and pull-request disposition observations MUST instead
  retain each admitted object's bounded facts atomically with delivery
  deduplication; repository debounce MUST NOT discard a different object or a
  later lifecycle transition.
- Mutual exclusion MUST be per repository. Collection of one repository MUST NOT
  block or reject collection of another.

### 4.1 Server-only episode and outcome mining

The server collector MUST mine episodes using gh-aw's recorded
`context.episode_id`. It MUST NOT synthesize membership from names, time
proximity, run success, or GitHub triggering relationships. Recorded `hop_id`,
`parent_hop_id`, caller coordinates, and root coordinates MUST remain distinct.
Membership is evidence about selected retained runs, never proof of complete
episode discovery; absent context remains unavailable. Reusable-workflow hops
sharing a run ID MUST NOT allocate or duplicate that run's cost.

The collector MUST extract compact `aw_info.json` already downloaded during
ordinary collection and backfill available context from its retained run
envelopes, including `episode_hops` carrying recorded episode evidence, without
additional GitHub requests. Hops MUST match their enclosing run and repository;
missing coordinates MAY be enriched by compatible recorded contexts, never
guessed. Compact artifacts MUST match the
collected repository, run, and attempt. Conflicting recorded contexts for one
run/attempt/hop MUST fail rather than select an episode by arrival order.
Projection MUST discard memberships whose run or selected attempt is absent.

Verified `issues` and `pull_request` deliveries MUST queue bounded lifecycle
observations without acquiring logs or calling GitHub in the receiver.
Workers MUST recheck current installation ownership. An observation MUST join
an agent output only through an exact retained safe-output URL, including host,
repository, object category, and number; unrelated objects MUST NOT be attributed
to an agent. This lifecycle profile MUST evaluate only `create_issue` and
`create_pull_request` actions; edits, comments, reviews, and close actions require
their own evidence rules. Newer object `updated_at` observations supersede older ones.
Contradictory same-time observations MUST fail closed.

PR merge is accepted; PR closure without merge is rejected. Issue closure as
`completed` is accepted. Issue closure as `not_planned` by a bot is lifecycle,
not rejection; a known human close is rejected, and a missing closing actor
remains unknown. Open objects are pending. Unknown resolution reasons remain
unknown. Run success MUST NOT establish any of these dispositions. Comment,
review, engagement, zero-touch, and operational-value evidence MUST NOT be
inferred from this bounded lifecycle profile.

The server MUST repair dispositions over retained exact output references, not
GitHub searches or repository issue inventories. The default repair pass has a
one-minute deadline and a hard cap of ten REST calls, including quota
initialization, across the entire pass. Every object read MUST reserve and
reconcile shared installation quota, including errors and backoff. A persisted
namespace-wide cadence (default five minutes) and lease MUST prevent replicas
from multiplying this budget. A durable lexicographic output cursor MUST resume
the next bounded page; quota refusal MUST NOT advance the blocked target.
Missing objects and failed requests MUST report partial/unavailable evidence,
not acceptance, zero activity, or full-episode completeness.

Only currently enrolled target repositories may be repaired; caller/root
references and credential reach MUST NOT expand scope. Scope withdrawal MUST
erase server-mined evidence, including on volatile-state reconstruction.
Server-owned evidence packets MUST be bounded, atomically replaced, and replayable
without GitHub calls. Their content revision and canonical projection MUST
participate in the same ingestion transaction and revision notification as the
snapshot. Mining MUST NOT run in `cao-activity` or change its published contract.

The implementation uses existing typed Audit columns; it does not require a
new table or a physical schema change. See `dashboard-data.md` section 11.2 for
the field mapping.

## 5. Collection

A collection task acquires evidence for exactly one repository.

- A worker MUST lease a task before collecting and MUST NOT collect a repository
  that another worker holds.
- A worker MUST bound each collection with a timeout, an output size cap, and a
  working directory it owns and removes.
- A worker MUST pass an explicit GitHub API rate-limit reserve to the collection
  subprocess so the subprocess fails closed rather than exhausting the
  installation.
- On failure a worker MUST retry with bounded attempts and jittered backoff, and
  MUST dead-letter a task that exhausts them. A failed task MUST NOT corrupt or
  partially replace a repository's existing evidence.
- A task whose retry time has not arrived MUST NOT be immediately reinserted and
  reclaimed in a hot loop. Workers MUST honor its not-before time or use an
  equivalent delayed-work mechanism; repository-lock contention MUST also be
  retried without consuming the collection attempt budget.

## 6. The evidence lake

Collected evidence MUST be persisted in a durable **evidence lake**.

- The lake MUST use the Activity snapshot directory layout: compacted run shards
  under `gh-aw-logs-runs/`, record shards under `gh-aw-logs-records/`, a
  `payload-hashes.json` manifest, and `inventory-sources.json`. A source-built
  collector MUST also retain the resolved `control-settings.json` and
  source-bound `control-plane-inventory.json` that produced those inventory
  sources.
- Shard writes MUST be atomic per repository: a reader MUST observe either the
  previous shard or the complete new shard.
- Repository shard prefixes MUST preserve the complete normalized coordinate,
  be prefix-free, and occupy a namespace distinct from legacy slash-to-hyphen
  filenames. Acquisition, cache selection, and compaction MUST use the same
  identity. The collector uses `repo-v2~owner~repository~-logs-` with lowercase
  segments; `~` cannot occur in repository segments or legacy coordinates,
  making both separators and the terminator unambiguous without expanding
  maximum-length GitHub coordinates beyond filesystem basename limits.
- Existing raw shards MAY migrate into that namespace only after validating
  all declared repository identities in their content. Migration MUST preserve
  unrelated colliding repositories, MUST NOT overwrite an existing destination,
  and MUST support retry after interrupted publication. Legacy and versioned
  retained evidence MUST remain replayable without GitHub requests.
- Missing, malformed, or conflicting legacy identity MUST fail collection or
  erasure closed, preserving the evidence for operator repair rather than
  guessing ownership from filenames or enrollment. Compaction MUST reject
  conflicting identities across or within records before mutating any group.
- The manifest MUST be regenerated whenever shards change, and MUST list a
  SHA-256 hash for every shard it names.
- The lake MUST NOT contain prompts, tokens, secrets, or credentials.
- The lake MUST be sufficient to repopulate an empty canonical database with no
  GitHub requests.

## 7. Projection

Projection converts the evidence lake into the current canonical PostgreSQL
dataset.

- Projection MUST reuse the Actions profile's ingestion implementation over the
  evidence lake directory. An implementation MUST NOT define a second projector.
- Projection MUST be coalesced: an implementation MUST collapse collections that
  complete within a configured debounce window into one replacement.
- A replacement MUST commit all current sources, source documents, diagnostics,
  revision state, and evaluation time in one PostgreSQL transaction. A failed
  projection MUST leave the previously committed dataset serving.
- Relationship errors and duplicate record identities MUST fail the projection
  rather than commit a dataset known to be inconsistent.
- On successful activation the implementation MUST increment the revision and
  notify connected clients through the existing revision channel.
- Projection MUST short-circuit when the lake's content-addressed data revision
  already matches the current dataset. A collection re-enumerates a window and
  usually adds nothing, so rewriting an identical dataset would be the dominant
  steady-state cost. An explicit operator rebuild MAY bypass this short-circuit.
- PostgreSQL MUST be the only dashboard entity and query store. Operational
  adapters MAY hold projection coordination and health state, but MUST NOT hold entity rows,
  canonical source documents, query indexes, persistent query projections, or
  execute dashboard data queries.
- Inventory discovery MUST NOT silently truncate the enrolled repository set. An
  implementation MAY enforce a configured bound, but exceeding it MUST fail the
  projection rather than publish a partial inventory.
- A source-built collector MUST generate its static control-plane inventory
  deterministically from the exact source revision during image build. Runtime
  inventory discovery MUST resolve the reviewed deployment policy, reject an
  unavailable policy resolution, replace only its repository scope with the
  exact operational enrollment set, and enrich the source-bound inventory with a
  short-lived installation token for the control repository.
- The first canonical activation MUST fail when it cannot produce valid inventory evidence
  with at least one campaign. After one valid inventory has been retained, a
  transient policy, GitHub, or enrichment failure MAY reuse that previous
  inventory while projecting newer run evidence; an empty or malformed
  inventory seed MUST NOT qualify as a previous valid inventory.
- Reusing previous inventory after a refresh failure MUST retain a pending
  refresh request for the next coalesced projection interval.

## 8. Cold start

Cold start MUST be resumable and MUST report progress through the existing
administrative rebuild surface.

An implementation MUST support cold start in this order:

1. **Replay the evidence lake.** When a lake is present, the implementation MUST
   repopulate from it without issuing GitHub requests. This is the normal
   recovery path.
2. **Enumerate installations** and their repositories into the enrollment set,
   checkpointed per page cursor.
3. **Seed backfill tasks**, ordered so that recently active repositories become
   queryable first.
4. **Collect per repository** for the retention window, with a per-repository
   cursor, so an interruption loses at most one repository's in-flight work.

During cold start the implementation MUST continue to accept webhooks and MUST
apply them without loss or double application. Window completeness and
backfilled repository count MUST be published as source metadata.

An empty source-built lake MUST produce and retain valid inventory evidence
before its first canonical projection. A retained lake with valid inventory
evidence remains replayable without GitHub requests.

An explicitly configured historical backfill window MUST be sent as an
inclusive UTC `created=START..END` range to GitHub's run-list API; a provider
that cannot apply that range MUST fail closed. An unset window preserves
unbounded discovery. Windowed admission MUST NOT truncate retained lake replay.
Transient contention with a webhook-held enrollment mutation lease MUST wait
cancellation-safely rather than abort cold start. Enrollment ownership updates
MUST preserve transfer/withdrawal semantics when pipelined in bounded batches.
Checkpoint write failures MUST NOT be reported as successful backfill;
cancellation MUST persist a terminal failed state with bounded cleanup.

## 9. Gap recovery

Activity gap recovery MUST use webhook delivery replay rather than routinely
enumerating workflow runs across the enrollment set. Repository-list repair is
independent of activity replay.

- Collection workers MUST reconcile the App's active installation list on startup
  and periodically (default one hour), independently of webhook delivery.
  `CAO_COLLECT_REPOSITORY_REFRESH_INTERVAL` MUST allow operators to configure
  this cadence using a positive Go duration.
  A shared operational lease and persisted admission timestamp MUST coordinate
  replicas and restarts. Each pass MUST queue the ordinary quota-governed scope
  refreshes and missing-installation withdrawals. Failed App enumeration MUST
  NOT infer withdrawals or advance the successful-admission timestamp.
- An operator MUST be able to force the same repository-list repair without
  replaying the evidence lake or enumerating historical workflow runs:
  `cao-dashboard backfill --repositories-only`. The command reports admitted
  work; collection workers apply the queued reconciliation. Unchanged active
  repositories MUST NOT be recollected just because their list was refreshed.
- After downtime or dead-lettered work, the implementation MUST recover missing
  events by listing App webhook deliveries from the last processed delivery and
  requesting redelivery of missing or failed deliveries.
- Recovery MUST persist pagination progress when a bounded pass cannot reach the
  prior delivery boundary, and MUST resume from that page before advancing its
  high-water mark. A failed redelivery request MUST NOT advance the mark beyond
  that delivery.
- Per-repository enumeration MUST remain available for cold start and for
  operator-triggered repair.
- When a gap cannot be recovered, the implementation MUST record the gap in
  completeness metadata rather than present the window as complete.

## 10. Rate-limit governance

GitHub meters App traffic per installation. An implementation MUST govern budget
per installation.

- Installation tokens MUST be minted per installation, cached no longer than
  shortly before expiry, and never logged or persisted to the evidence lake.
- Live inventory enrichment MUST use a short-lived token for the control
  repository installation and MUST pass the same explicit rate-limit reserve as
  other collector GitHub subprocesses.
- The implementation MUST maintain a per-installation budget corrected from
  `x-ratelimit-remaining` and `x-ratelimit-reset` on every response.
- A worker MUST reserve budget before starting a task and MUST stop at a
  configured floor rather than exhaust an installation.
- `Retry-After` and secondary rate-limit responses MUST park the installation and
  requeue with jittered exponential backoff.
- Enumeration SHOULD use conditional requests.

## 11. Authority and safety

- The GitHub App MUST be read-only. Ingestion MUST NOT write to any observed
  repository, and MUST NOT dispatch work.
- The App private key, webhook secret, and database URLs MUST be resolved from a
  platform secret manager and MUST NOT appear in configuration files, workflow
  inputs, commits, logs, telemetry, or chat.
- A process that only admits deliveries MUST NOT be given the App private key.
  An implementation MUST support admitting deliveries without collection
  credentials, and MUST refuse a private key, workers, or delivery replay in
  that mode rather than accept an unusable credential.
- Prompts, tokens, and secrets MUST NOT be written to the evidence lake, the
  canonical database, or telemetry.
- Missing credentials, missing installation access, an exhausted rate limit, or a
  failed subprocess MUST fail closed. An implementation MUST NOT infer broader
  authority or present partial evidence as complete.
- A misconfigured collector MUST fail startup or degrade to the Actions profile.
  It MUST NOT degrade to a partial collector.
- Before collection begins, the collector MUST enumerate the complete App
  installation scope and validate repository visibility: when the control
  repository is public, it MUST fail startup if the credential's scope
  includes any non-public repository, rather than collect from it. A
  visibility failure MUST report only the count of rejected repositories and
  MUST NOT include non-public repository names or other identifying details.

## 11b. Retention and erasure

The evidence lake is retained evidence rather than a cache, so retention is a
governed property and not an accident of disk usage.

- A signed `repository` delivery with action `created`, `deleted`, `archived`,
  `unarchived`, `renamed`, or `transferred` and a valid repository ID and
  installation MUST durably queue an independent discovery hint for a known
  installation. A repository covered by a different installation MUST NOT be
  admitted. The receiver MUST NOT project, call GitHub, or change enrollment
  inline. Workers MUST reconcile current authenticated installation coverage,
  not apply the webhook's historical state as current truth. Delayed creation,
  archive, rename, and delete deliveries MUST NOT revive stale coordinates or
  evict a repository that is currently active.
- Deleted, archived, transferred-out, and renamed-away coordinates MUST be
  evicted from enrollment and the evidence lake, including retained repository
  and workflow inventory. Canonical projection MUST remove their evidence;
  lifecycle metadata MUST NOT recreate absent canonical repository rows.
  Unarchive and rename discovery MUST enroll and collect the current verified
  coordinate. Repository deletion or archiving changes collection eligibility,
  not rollout authority. Consent
  withdrawal (`installation.deleted`, `installation.suspend`, or
  `installation_repositories.removed`) still takes precedence and MUST erase
  evidence immediately under the rules below.
- Leaving ingestion scope MUST erase retained evidence. When an installation is
  deleted or suspended, or repositories are removed from it, an implementation
  MUST delete that repository's shards from the evidence lake and MUST request a
  projection so the canonical database stops reporting it.
- Collector-owned shared operational-value observations MUST be filtered by
  exact, case-insensitive repository identity, not by shard filename. Withdrawal
  MUST preserve unrelated raw observations, including their extension metadata.
  Affected disposable normalized shards and normalization-cache files MUST be
  invalidated; compaction regenerates their unrelated rows from retained raw
  evidence. A snapshot whose manifested payload was invalidated MUST NOT replay
  successfully until recompaction.
- Shared-file erasure and collector projection MUST serialize across processes
  sharing the evidence lake. The Go collector uses an advisory filesystem lock;
  the configured lake filesystem MUST support that lock across its writers.
  Before best-effort operational-value acquisition, projection MUST filter
  collector-owned observations against current enrollment, including an empty
  enrollment set. Credential or adapter failures MUST NOT bypass that filtering
  or restore withdrawn observations through the retained-value merge.
- Successful canonical replacement advances the revision. Queries depending on
  collector operational values MUST include that revision in their result-cache
  identity so an older cached result cannot serve after replacement. Unreachable
  older query-result cache entries still follow the bounded cache TTL; this is
  not a claim of immediate physical disposal of every cache or backup copy.
- Independently published operational-value evidence packets and protected
  publication-owned facts have a separate publication authority contract.
  Collector enrollment withdrawal MUST NOT infer permission to delete those
  facts. Their repository scope, disposal authority, retention and any hold
  obligations require an explicit operator decision; this rule does not invent
  a hold or authorize indefinite retention.
- Repository-prefixed shard erasure MUST preflight candidate ownership across
  raw, run, and record directories before deleting any file. A legacy filename
  match alone MUST NOT authorize deletion: unambiguously unrelated evidence
  MUST remain untouched, and ambiguous evidence MUST block successful erasure.
- Erasure MUST be driven by the same webhook admission path as enrollment, so
  withdrawing consent takes effect without operator action. A process without an
  evidence lake MUST queue erasure rather than skip it.
- Removal MUST be scoped to the installation that currently covers a repository.
  A repository transferred between installations MUST keep its enrollment and
  its evidence when a later removal or deletion event arrives for the
  installation that no longer covers it; that event MUST clear only its own
  installation membership.
- The implementation MUST durably enqueue erasure work before removing
  enrollment, so a queue failure cannot make a withdrawal unrecoverable on
  delivery replay. Erasure work MUST be serialized with collection by the
  repository lease and MUST wait until the repository is no longer covered by
  the installation that requested removal. A worker MUST recheck current
  enrollment after acquiring the lease and MUST erase, rather than collect, an
  already-queued task for a repository that has left scope. Enrollment additions
  and removals MUST be serialized so the membership snapshot used to queue
  erasure is the same snapshot that is removed.
- A synchronous erasure failure MUST fail the delivery. A durably queued
  erasure MAY be acknowledged before completion, but MUST remain visible and
  recoverable until deletion and projection succeed; it MUST NOT be treated as
  completed merely because it was queued.
- Collection MUST be at-least-once. Retry, deferral, blocked-repository requeue,
  malformed-entry handling, and dead-letter transitions MUST persist the
  replacement before acknowledging the original in one atomic storage operation.
- Future retry and deferral work MUST be durably scheduled outside the ready
  stream, promoted only when due, and included in queue capacity and depth.
  Workers MUST NOT wait synchronously on a future task while ready work remains
  available.
- A successful collection-webhook response MUST mean delivery deduplication and
  durable admission were committed atomically. Operational storage failures MUST return a
  retriable non-success response without consuming the delivery identity.
- A scope-withdrawal response MUST be sent only after each affected repository
  has either been erased or has a durable erasure task committed before its
  enrollment is removed. In the queued case the response confirms durable
  admission, not completed erasure; worker failures MUST remain retryable or
  durably dead-lettered and visible in health status.
- The task queue MUST apply bounded admission backpressure and MUST NOT trim
  undelivered or pending entries. Completed entries MAY be deleted after ACK;
  dead letters remain durably recorded under the configured bounded retention.

## 12. Observability

An implementation MUST expose collection status reporting at least enrollment
coverage, queue depth, processing lag, rate-limit headroom, and dead-letter
count.

- In the Actions profile the same surface MUST report that collection is not
  configured, and MUST NOT fail.
- Status MUST NOT include tokens, secrets, prompts, or payload contents.
- Collection acts unattended on customer repositories, so it MUST be traceable:
  enqueue, collection, dead-lettering, redelivery, and erasure MUST each record
  the repository or delivery they concern. A deployment MUST route these records
  and the process's telemetry to a durable sink.
- The server MUST provide a read-only check-up that reports the selected
  profile, operational connectivity and guarantees, PostgreSQL readiness and
  revision state, canonical integrity, query-definition validity,
  and, when collection is configured, enrollment, queue, cold-start,
  rate-limit, evidence-lake, and tooling state.
- Each check MUST have a stable machine-readable identifier, severity,
  human-readable summary, observed facts, and a remedy for non-passing states.
  The same observations MUST be available in a versioned structured format.
- The check-up MUST NOT contact GitHub, mutate operational storage, repair data, read secret
  contents unnecessarily, or report tokens, passwords, private keys, webhook
  secrets, payload contents, or prompts. Dependency checks MUST be bounded so
  an unavailable surface cannot hang the full report.
- Expensive checks that read every current PostgreSQL source MUST be explicit and
  MUST use the production source-loading path and its fail-closed row bounds.

## 13. Reliability simulator

The repository's `simulate-api` and `simulate-webhooks` commands are a local
validation tool for the collector profile. They MUST exercise the same GitHub
webhook endpoint, signature verification, delivery deduplication, durable queue
admission, worker coordination, collection runner, and projection code used in
production. The simulator MUST NOT add a bypass path to production handlers or
substitute an in-memory queue for the selected durable operational backend.

### 13.1 Scenario contract

Scenarios are strict JSON documents. Unknown fields MUST be rejected before
simulation starts. A scenario MUST have a name, a repository count between 1
and 50,000, and no more than 1,000,000 generated workflow/issue events combined. API windows
MUST have valid, increasing, non-overlapping `from` and `to` durations.

The supported scenario controls are:

- `seed` for repeatable event IDs, payloads, ordering, and distribution;
- `distribution`: `uniform`, `hot`, `long-tail`, or `synchronized`;
- `out_of_order`, `duplicate_every`, `drop_every`, `delay_every`, and `delay`
  for webhook delivery faults;
- `replay_count` to redeliver the most recent generated window using its
  original delivery IDs; and
- `remove_repositories` to emit a repository-removal event after workflow
  traffic; and
- `create_repositories`, `archive_repositories`, `unarchive_repositories`,
  `rename_repositories`, and `delete_repositories` to emit signed lifecycle deliveries for the first
  synthetic repository. Unarchiving requires archiving. The fake GitHub API
  MUST represent the resulting renamed/archived/deleted state in paginated
  installation enumeration and repository/run endpoints, with or without history.
  Renames MUST preserve the immutable GitHub repository ID.

  An optional `history` object MAY generate 1-31 days with a positive
  `runs_per_day` and an explicit RFC3339 `as_of`. Historical generation MUST be
  bounded to five million run identities and generate API pages on demand.
  Installation, repository, and run pages MUST follow the production GitHub
  pagination contract, including the `created` time range.

  An optional `faults` list MAY configure the local reverse proxy by path, page,
  bounded request count, mode, and backoff. Faults MUST expire at their declared
  count. Supported proxy faults include 5xx, authorization, malformed JSON,
  connection loss, timeout, missing quota headers, and primary/secondary limits.
  `webhook_retry_limit` MAY enable bounded transient delivery retries; retries
  MUST reuse the signed delivery identity and be counted separately.

  All simulation HTTP transports MUST pin an exact numeric-loopback endpoint,
  disable environment proxies and DNS-based host selection, and reject redirects
  to other hosts/ports before dialing. Stress harnesses MUST create their own
  synthetic origins and ephemeral App keys, remove inherited GitHub credentials,
  and MUST NOT send simulated traffic to live GitHub APIs.

  The enterprise backfill workflow MUST exercise 1,000, 10,000, and
  50,000 repositories, at least one run per day, and a seven-day horizon.
  Signed webhook traffic MUST overlap backfill. Assertions MUST cover all durable
  run identities, exact counts, the time window, deduplication, and native
  Postgres queries; no in-memory queue or production-handler bypass is permitted.
  Diagnostic artifacts MUST retain bounded local OTEL traces, logs, and counters
  before teardown on success or failure. Aggregate backfill telemetry MUST carry
  fixed phase/count/window attributes, not credentials, prompts, source rows,
  or per-repository metric labels.

API windows select a behavior for elapsed scenario time. Supported modes are
`healthy`, `latency`, `timeout`, `connection-failure`, `rate-limited`,
`secondary-rate-limit`, `internal-error`, `bad-gateway`,
`service-unavailable`, `unavailable`, and `intermittent`. Intermittent windows
MAY set `failure_rate` or `fail_every`; rate-limit windows MAY set
`rate_limit_remaining` and `rate_limit_reset_after_seconds`. Outside a declared
window, the fake API MUST return healthy responses. `--time-scale` maps scenario
time to wall-clock time and MUST be positive and bounded.

`simulate-webhooks` MUST sign each request with the configured
`CAO_GITHUB_WEBHOOK_SECRET` and send it to the normal CAO webhook endpoint.
Installation and repository-add events MUST precede workflow events so the
normal enrollment checks apply. Duplicate and replay requests MUST reuse the
original delivery ID; explicitly dropped events MUST NOT be sent. Concurrent
request count and per-request timeout MUST be bounded.

### 13.2 API surface and operating limits

The fake GitHub API implements only the REST interactions needed by the
collector, including App validation, installation-token creation, rate-limit
inspection, repository lookup, and workflow-run/log requests. It MUST return
GitHub-shaped status codes and rate-limit headers for configured fault windows.
It is not a general GitHub API emulator, and passing a simulator scenario MUST
NOT be treated as proof of correctness against every GitHub API response.

The API URL MUST be routed through the normal collection runner into collection
subprocesses. A simulator run MUST use synthetic repositories, isolated test
credentials, and a test Redis instance; operators MUST NOT point it at live
repositories or expose the fake API listener to an untrusted network. The
simulator MUST NOT log webhook secrets, App credentials, or payload contents.

The simulator validates bounded-load admission, duplicate handling, selected
API fault responses, and recovery behavior through the real server path. It
does not establish production capacity or guarantee losslessness for failures
outside the exercised scenario; operators SHOULD measure queue depth, pending
work, dead letters, processing lag, and the collection-health status surface
during and after each run.

The Go server integration test MUST deliver signed scenario traffic through the
production webhook handler with a real Redis instance, then verify the health
API reports persisted webhook counters and queued recovery work. This test MUST
run in the Redis-backed server integration job; unit tests without Redis MAY
skip it.

## 14. Conformance checklist

A conforming implementation:

1. selects exactly one profile and fails startup when both are configured;
2. leaves the Actions profile's behavior unchanged when the collector is absent;
3. derives scope from App installations, never from `cao.json`;
4. admits and enqueues webhooks without projecting in the request path;
5. collects with the shared `gh aw logs --audit` and `activity/cao.mjs`
   implementations;
6. persists a snapshot-compatible evidence lake sufficient for cold start;
7. projects by reusing the Actions profile's ingestion implementation, coalesced
   and atomically activated;
8. recovers activity gaps by webhook delivery replay and repairs repository lists
   through periodic and operator-triggered authenticated discovery;
9. governs GitHub budget per installation and fails closed;
10. passes a profile equivalence test against an Actions-published directory;
11. erases retained evidence for repositories that leave ingestion scope, and
    bounds its queues;
12. admits deliveries without collection credentials, and fails closed when an
    admission-only process is asked to collect or project;
13. short-circuits projection for an unchanged lake, transactionally replaces
    the current PostgreSQL dataset, and refuses to publish a truncated inventory.
