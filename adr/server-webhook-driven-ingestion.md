# ADR: Optional server-side, webhook-driven CAO ingestion

## Status

Accepted

## Context

CAO Activity acquires evidence from a scheduled GitHub Actions workflow.
`.github/workflows/cao-activity.yml` iterates the repositories named in
`cao.json`, runs `gh aw logs --audit` for each, compacts the result with
`gh cao`, and publishes an immutable snapshot directory. The hosted
Go server in `server/` then ingests that published directory through
`DirectoryReconciler` and `internal/ingest`.

That design is correct and is the supported default. It has two properties that
bound it:

- Discovery cost scales with enrollment, not with activity. A scheduled sweep
  issues requests proportional to the number of enrolled repositories on every
  tick, whether or not anything happened.
- Refresh granularity is the whole snapshot. Freshness is bounded by the
  schedule interval, and recovery means re-running the workflow.

Those bounds are comfortable at the scale the Actions profile targets: tens of
repositories named in `cao.json`, hundreds of runs per day, and a 15-minute
snapshot. They are not comfortable for a hosted operator who wants to observe
10 000 – 100 000 repositories producing on the order of 10 000 runs per day with
per-run freshness. Sweeping 100 000 repositories to find 10 000 runs spends far
more GitHub API budget on discovery than on evidence.

Such an operator already runs the Go server, Redis, and a GitHub App for
sign-in. The question this ADR answers is whether that server can acquire
evidence directly, and on what terms.

## Decision

Add an **optional** server-side collector that acquires evidence from a GitHub
App using webhooks, and make it an **alternative** to the Actions profile rather
than a replacement for it or a layer on top of it.

### 1. The Actions profile is unchanged and remains the default

`.github/workflows/cao-activity.yml`, `activity/`, the published snapshot
contract, `specs/server-ingestion.md`, and browser-side ingestion are not modified,
deprecated, or superseded. A deployment with no collector configuration behaves
exactly as it does today, including `DirectoryReconciler`. The Actions-only
story stays intact for users with no hosted infrastructure.

### 2. The two profiles are mutually exclusive

A deployment selects exactly one acquisition profile. Configuring both a source
directory and the collector is a startup error, not a merge of two evidence
sources. Two writers of one canonical dataset would duplicate records, race on
generation activation, and make completeness metadata meaningless. Migration
between profiles is a discrete switch: stop the old profile, reconfigure, cold
start the new one.

### 3. Reuse first; no parallel implementation

Every capability the collector needs already has an owner in the tree. The
collector calls those owners and extends them where a parameter is missing. Only
three genuinely new packages are justified, because nothing in the tree performs
their function at all: GitHub App authentication, the task queue, and the
enrollment set.

| Capability | Reused owner |
| --- | --- |
| Log acquisition and audit fidelity | `gh aw logs --audit`, invoked as a subprocess |
| JSONL compaction, payload hashing, shard layout | `gh cao` (`compact-jsonl`, `hash-payloads`), invoked as a subprocess |
| Canonical mapping, twelve collections, diagnostics | `internal/ingest` |
| Generation staging and atomic activation | `internal/ingest` and the existing generation keys |
| Redis access, pooling, deadlines, retries | `internal/redisx` `Do` / `DoMany` |
| Mutual exclusion and leader election | the existing `TryLock` / `Unlock` / `LockHeld` helpers |
| Operational state and rebuild status | `SetOperationalState`, `POST /api/admin/rebuild`, `GET /api/admin/rebuild/status` |
| Webhook receipt, HMAC, dedupe, body cap, `202` | the existing `POST /api/github/webhook` handler |
| Evidence-path selection | the existing `Reconciler` interface |
| Revision bump and SSE fan-out | the existing event hub |
| Configuration and secrets | `serve-hosted` `CAO_*` plumbing, Key Vault, managed identity |
| Spans and category logging | `internal/telemetry`, `internal/logger` |
| App JWT and installation tokens | `go-github` and `ghinstallation` |

The most consequential instance of this rule is collection itself. Workers do
not reimplement `gh aw logs --audit` fidelity in Go; they invoke the same CLI
and the same compactor that Activity invokes. One implementation of the mapping
serves either profile, which is what makes the profiles equivalent and what
makes equivalence testable.

### 4. The evidence lake is a published-snapshot directory

The collector writes compacted shards into a durable **evidence lake** laid out
exactly like an Activity-published snapshot directory: `gh-aw-logs-runs/`,
`gh-aw-logs-records/`, `payload-hashes.json`, and `inventory-sources.json`.

This is the decision that collapses most of the projected complexity. Because
the lake is byte-compatible with what `internal/ingest` already consumes,
projection is `ingest.Run` over the lake, unchanged. There is no second
projector, no second canonical record store, and no incremental upsert path that
could observe a dangling relationship and fail `validateDiagnostics`. It also
makes cold start free: replaying the lake repopulates an empty Redis with zero
GitHub requests.

Incrementality therefore lives at the **collection** grain — one repository's
shard is rewritten when that repository has new runs — while projection is
**coalesced**: a debounce window collapses a burst of collections into one
generation. Coalescing, not per-run projection, is what keeps projection cost
sublinear in run volume.

### 5. Webhooks are the only routine discovery path

The App subscribes to `workflow_run`, `installation`, and
`installation_repositories`. There is no steady-state polling. Enumeration
exists for cold start and operator-triggered repair only. Gap recovery after an
outage reads `GET /app/hook/deliveries` and requests redelivery of missing or
failed deliveries — a handful of paginated requests instead of a
100 000-repository sweep.

### 6. Webhook receipt enqueues; it does not project

The existing webhook handler takes a single global `projection` lease and runs a
full rebuild inline. At ~7 events per minute against minutes-long collection,
that lease would reject most deliveries. When the collector profile is selected,
the handler instead admits the installation and repository against the
enrollment set, appends a task to a Redis Stream, and returns `202`. Tasks are
keyed by repository and debounced, so a burst of runs in one repository becomes
one `gh aw logs` invocation and unrelated repositories proceed in parallel. When
the collector is not selected, the handler's behavior is unchanged.

### 7. Scope is GitHub App installations

In the collector profile, every repository the App is installed on is enrolled.
`cao.json` remains rollout policy in either profile and never becomes ingestion
scope. Rate limits are metered per installation, so budget is reserved per
installation from a Redis token bucket seeded and corrected by
`x-ratelimit-remaining` and `x-ratelimit-reset`, and the derived reserve is
passed to `gh aw logs --max-github-api-rate-limit` so the subprocess fails
closed rather than exhausting the installation.

### 8. One binary, additional roles

The front end keeps the API, OAuth, and the webhook endpoint. An optional
KEDA-scaled Azure Container Apps worker runs a `collect` role from the same
image; `backfill` performs cold start; local development can run every role in
one process.

### 9. Generations are reclaimed at activation, not by an operator

Every projection writes a complete new canonical generation and activation only
flips a pointer. Reuse of the Actions projector therefore inherited an
assumption that no longer holds: under Actions a projection happens once per
published snapshot, so leaked generations were slow enough to be invisible.
Collection projects continuously, which turns that leak into memory exhaustion
of a `NoEviction` Redis, and every subsequent write — collection included —
fails.

Reclamation is therefore part of activation rather than a scheduled sweep or an
operator runbook. A registry of activated generations makes superseded ones
findable without a keyspace scan; a bounded retention count keeps a rollback
target; a grace period keeps readers that already resolved the previous
generation from having it pulled out from under them mid-query. The Actions
profile gets the same reclamation, because it is the same projector.

The complementary decision is that a projection which would change nothing does
not run at all: the lake's data revision is content-addressed, a collection
usually adds nothing to the lake, so the common case short-circuits. Only an
explicit operator rebuild forces a rewrite, because an operator rebuilding is
repairing Redis rather than publishing new evidence.

## Alternatives considered

**Replace `cao-activity.yml`.** Rejected. The Actions-only story must stay intact
for users with no hosted infrastructure, and a single path would force every
consumer into an operator model they did not ask for.

**Run both profiles against one deployment.** Rejected. Duplicate records,
generation races, and meaningless completeness metadata. Profile selection is
exclusive and validated at startup.

**Build a standalone collector service.** Rejected. It would duplicate
configuration, secret resolution, Redis access, telemetry, canonical mapping,
and deployment plumbing the server already owns.

**Reimplement `gh aw logs --audit` fidelity in Go.** Rejected. A large,
high-churn surface with real risk of divergence between profiles. Sharing the
CLI is what makes equivalence testable.

**A bespoke incremental canonical record store with per-run upserts.** Rejected
in favor of the byte-compatible evidence lake. Per-run upserts must be
entity-complete or `validateDiagnostics` fails on a dangling reference, which
makes every write path a correctness hazard; re-projecting a coalesced batch
from the lake is always internally consistent.

**Scheduled reconciliation sweeps as the correctness backstop.** Rejected as the
routine path. Webhook delivery replay recovers gaps at a fraction of the request
cost. Enumeration remains available for explicit repair.

**Azure Storage Queues instead of Redis Streams.** Rejected. Redis is already
required, consumer groups provide the lease and claim semantics needed, and
stream depth is a direct KEDA scaling signal.

## Consequences

- Two supported acquisition profiles mean two paths to keep correct. A profile
  equivalence test — Actions-published shards and server-collected shards must
  yield identical canonical records for the same window — is what makes that
  sustainable, and is non-negotiable. Because the profiles are alternatives and
  never layered, no deployment carries the cost of both.
- In the collector profile, discovery cost tracks activity rather than
  enrollment, so repository count can grow by orders of magnitude without a
  proportional request cost.
- The collector depends on webhook delivery. GitHub's delivery retention window
  bounds unattended outage recovery; beyond it, an operator runs enumeration
  repair.
- Workers need disk, `unzip`, Node.js, `gh`, and `gh aw`, so the worker image is
  heavier than the front end and is pinned.
- Projection remains whole-generation. Coalescing bounds how often that cost is
  paid, but a deployment with a very large retention window still pays it; if
  that becomes the binding constraint, per-run-grain projection is a follow-up
  with a known cost, not a prerequisite.
- The collector is read-only. It never writes to an observed repository, and it
  fails closed on missing credentials, missing installation access, an exhausted
  rate limit, or a failed subprocess: the task is retried or dead-lettered, the
  previous active generation keeps serving, and completeness metadata records the
  gap.

Normative requirements for the optional profile are in
`specs/server-ingestion.md`. That specification conforms to `specs/server-ingestion.md`;
it supersedes nothing.
