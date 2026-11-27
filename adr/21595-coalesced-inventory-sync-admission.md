# ADR 21595: Coalesced Inventory Sync Admission

## Status

Draft

## Context

Pull request #21595 adds an on-demand repository and campaign inventory refresh to the Updates pages and keeps the periodic refresh bounded:

- Before this pull request, `specs/server-ingestion.md` required collection workers to reconcile the App's active installation list on startup and periodically (default one hour), with a positive Go duration configurable through `CAO_COLLECT_REPOSITORY_REFRESH_INTERVAL`. The interval had no upper bound.
- The pull request adds a **Sync** control to Repositories, Workflows, and Campaigns that calls `POST /api/admin/collection/sync` without a request body.
- The handler in `server/internal/server/collecting.go` requires administrative authorization, rejects any request body, returns `503` when native collection is not configured, and returns `202` with `{"queued": true}` after admission.
- `Queue.SyncInventory` in `server/internal/collect/native_control_queue.go` pages through committed repository scopes (`ScanRepositories`, `CurrentScope`), skips identity mismatches, and admits one `inventory-sync` task per repository through the native control queue. It returns a capacity error above 100000 repositories and rejects a non-advancing cursor.
- `EnqueuePendingTask` in `server/internal/operational/postgres/queue.go` stores a `pending-identity` key of the form `inventory-sync:<repository>:<scopeEpoch>`. While that key is outstanding, a second admission for the same identity is not enqueued. `complete()` releases the key on terminal settlement, and `queue_retry.go` carries it across same-queue retries.
- `specs/server-ingestion.md` now requires that the endpoint acquire no GitHub evidence inline, change no enrollment or rollout policy, and request no historical runs or Value work. It also requires that the periodic refresh cover existing inventory, not only membership, and caps the configured interval at 24 hours.

The pull request body and the diff do not record a discussion of the alternatives that were rejected. The reasons for each boundary below are therefore either stated in the specification text or marked as not inferable.

## Decision

1. `POST /api/admin/collection/sync` admits inventory-only queue tasks for current committed repository scopes. It MUST NOT acquire GitHub evidence inline, change enrollment or rollout policy, or request historical runs or Value work.
2. Admission is durably coalesced per repository and scope epoch. An outstanding request for the same identity is not duplicated, and a fresh request is admitted after the earlier task reaches terminal settlement.
3. A `202` response with `queued: true` confirms admission only, not completed collection. Active views observe publication through their existing canonical revision subscriptions.
4. Missing native collection or failed admission returns `503`. The static demo MUST NOT offer live collection.
5. The periodic reconciliation keeps hourly as its default and rejects configured intervals longer than 24 hours. Each pass refreshes inventory for currently enrolled repositories.

## Alternatives Considered

- **Acquire inventory inline in the request handler.** Not chosen. The specification forbids inline GitHub evidence acquisition for this endpoint. The reason it was rejected (latency, rate limits, or failure isolation) is Not inferable from current pull request evidence.
- **Enqueue without durable coalescing.** Not chosen. The specification requires durable coalescing for outstanding requests, and the implementation enforces it with the `pending-identity` key. The reason for choosing a queue-level identity over a request-level debounce is Not inferable from current pull request evidence. The pull request code rejects `Debounce` together with a pending identity, so the two mechanisms are not combined.

No other alternative is supported by the pull request evidence.

## Consequences

**Positive:**

- Repeated clicks produce at most one outstanding inventory task per repository and scope epoch, which bounds queue growth.
- The endpoint does only queue admission, so the request path does not depend on GitHub availability.
- The existing fenced queue, capacity limit, and retry semantics apply to sync work without a new queue type.
- The 24-hour cap keeps the periodic inventory refresh within the daily guarantee the specification describes.

**Negative and operational:**

- A `202` response is not completion. Operators must watch the Updates view or revision events to see results.
- The pending-identity key is released only when the task settles through `complete()`. A task that is neither completed nor replaced keeps its key and suppresses further sync requests for that repository and scope epoch. The pull request tests release on terminal settlement and retry carry-over, but the queue code has no dead-letter path that releases the key, and the pull request does not test that case.
- Admission can return `503` when queue capacity is reached, even though enrollment is valid. The response does not distinguish these causes.
- A repository scope-epoch change produces a new identity, so a request made around an epoch change is admitted rather than coalesced into the earlier task. This is intended by the identity format but is not discussed in the specification.
