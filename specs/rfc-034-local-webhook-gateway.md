# RFC-034: Local webhook gateway simulation and PostgreSQL conformance

**Status:** implementation specification; bounded formal model and executable
conformance vectors accompany this document. This is not a claim of production
activation, live GitHub end-to-end coverage, or implementation refinement proof.

**Relationship:** extends [RFC-033](rfc-033-webhook-gateway.md) only with a
non-authoritative local testing profile. RFC-033 remains the production contract.

## 1. Conformance and terminology

The key words **MUST**, **MUST NOT**, **REQUIRED**, **SHALL**, **SHALL NOT**,
**SHOULD**, **SHOULD NOT**, **RECOMMENDED**, **NOT RECOMMENDED**, **MAY**, and
**OPTIONAL** are interpreted as in BCP 14
([RFC 2119](https://www.rfc-editor.org/rfc/rfc2119),
[RFC 8174](https://www.rfc-editor.org/rfc/rfc8174)) when capitalized.

This specification uses the W3C-style distinction between normative requirements,
conformance classes, and non-normative examples. A conforming **local host**,
**polling adapter**, and **PostgreSQL queue** MUST satisfy their applicable numbered
requirements. An implementation MAY combine those classes in one process.

A *simulation* is a local, signed HTTP request evaluated by the real gateway
ingress and durable worker, with synthetic local authorization and acceptance.
A *remote write* is a GitHub request other than a read-only GET. A queue
`succeeded` observation in this profile means **simulated dispatch acceptance**,
never a remote workflow run or operational outcome.

## 2. Local host profile

**LG-H01** The operator MUST select `--role=local`, an explicit `OWNER/REPO`,
and an explicit PostgreSQL connection and namespace. Receive/dispatch roles MUST NOT accept local
storage/polling options. No role has a permissive default.

**LG-H02** The local host MUST reject production App key-file and webhook-secret
environment variables. It MAY use `GH_TOKEN` for read-only GitHub access. It
MUST NOT load a production policy, issue GitHub dispatch requests, discover
additional repositories, alter reviewed policy, or activate production workflows.

**LG-H03** The host MUST label both startup and outcome reporting as simulation.
Its generated fixture policy, random in-memory HMAC secret, synthetic App and
installation IDs, and `local-simulation` revision provenance MUST NOT be
represented as compiler output, actual App installation evidence, or live
authorization. The fixture projects only `simulation: true` plus the real
ingress's reserved provenance into durable dispatch JSON.

**LG-H04** All work MUST be cancellation-scoped. Polling failures MUST stop
visibly without skipping failed admissions. The host MUST join the polling
worker before closing PostgreSQL. One-shot mode MUST report retained deferred work
as incomplete, not successful completion.
Rate-budget exhaustion MUST retain deferred work without stopping the continuous
host; it MUST wait before retrying. One-shot exhaustion MUST still emit the
simulated outcome counts and report incomplete.

The initial CLI profile deliberately uses a generated fixture, not arbitrary
production subscription artifacts. Custom filters, Projects authorization, and
production authority remain covered by RFC-033's domain/host suites; this local
CLI does not claim to exercise those integrations.

## 3. Read-only polling and delivery

**LG-P01** The polling adapter MUST resolve the explicitly named repository and
bind its immutable database ID and owner ID/type. Each poll MUST recheck those
identities. A redirect, deletion/name reuse, owner change, failed read, malformed
response, or unverifiable identity MUST fail closed. API URLs MUST use HTTPS,
except numeric-loopback HTTP test servers. Redirects MUST NOT be followed.

**LG-P02** A poll MUST read at most three pages of 100 events and bound each
response to 8 MiB. Only explicitly supported, selected event families MAY be
adapted. Unselected/unsupported feed events MUST be counted as ignored.
An Events API response MUST be a JSON array; `null` MUST NOT be treated as an
empty successful feed or advance its ETag.
The adapter MUST NOT invent missing action/filter operands, project identities,
status transitions, or relationship data.

**LG-P03** Delivery IDs MUST deterministically bind API host, immutable repository
ID, and GitHub event ID. Newly observed events MUST enter the production `Ingress.ServeHTTP`
path as JSON POST requests with a valid HMAC-SHA256 signature and GitHub event
and delivery headers. Raw feed/event bodies MUST remain ephemeral.
Only the repository/organization envelope and a **synthetic** installation ID
are added; this is not byte-equivalent to a real GitHub webhook.

**LG-P04** Only a completely admitted feed snapshot MAY advance its conditional
ETag. A failed delivery MUST retain the previous ETag, allowing stable-ID replay.
ETags are a volatile optimization; durable SQLite receipts, not ETags or a
high-water event ID, prevent duplicate admission after restart. Older events
appearing late in the bounded feed MUST NOT be skipped by an ID cursor.
Because feed payloads can change for an existing event ID, the adapter MUST
check committed, retained admission receipts before signing an event. A known
identity MUST be counted as replayed without resubmitting changed bytes or
replacing its receipt or jobs. A receipt lookup failure MUST fail the poll
without advancing its ETag. Events without a retained receipt MUST still pass
the real ingress; LG-Q13's digest-conflict rule remains unchanged.

**LG-P05** The poll loop MUST honor cancellation and the greater of the configured
interval and a valid `X-Poll-Interval` (1 through 86400 seconds). A conditional
304 MUST NOT create work. HTTP errors, including rate limiting, MUST be reported
without raw response bodies, credentials, or payload contents.

### 3.1 Coverage limitations (normative)

The repository Events API is a bounded, delayed, best-effort activity feed, not
a lossless webhook journal. A conforming implementation MUST NOT claim complete
event capture, gap recovery, exactly-once processing, real webhook equivalence,
or Projects v2 coverage. Starting/restarting MAY replay currently visible history
within the deduplication window. Events falling outside the feed cannot be
recovered by this protocol.

The initial allowlist is `issues`, `issue_comment`, `pull_request`,
`pull_request_review`, `pull_request_review_comment`, `push`, `create`, `delete`,
`release`, `fork`, and `watch`. These names are adapter mappings, not production
gateway routing or authorization special cases.

## 4. Durable PostgreSQL queue

The queue MUST implement the consumer-owned RFC-033 `Queue` interface. It MUST
use PostgreSQL transactions for every mutation, database time for lease/retention
decisions, role-separated storage, relational integrity, and cross-connection
serialization. The namespace and queue configuration MUST NOT be silently
rebound on reopening. It uses the existing operational PostgreSQL adapter,
not a second backend or an authorization service.

**LG-Q01** Admission receipts and job state MUST survive close/reopen. A replay
with the same delivery ID and body digest MUST return the existing atomic batch,
including terminal outcomes, without adding jobs or resetting attempts.

**LG-Q02** A delivery receipt and every fanout job MUST commit atomically or not
at all. Row and byte capacity MUST include retained jobs and receipts, with
per-tenant/source receipt shares and destination limits. Capacity failure MUST
roll back the complete batch, including its deduplication receipt.

**LG-Q03** Mutations MUST require exact job ID, owner, generation, CAS version,
and an unexpired lease. Renewal MUST advance the CAS version. A stale mutation
MUST NOT alter state or spend a rate token.

**LG-Q04** An expired *claimed* lease MAY recover to ready with a newer fence.
A previous owner MUST NOT finish or dispatch the recovered claim.

**LG-Q05** An expired *dispatching* lease MUST recover to terminal ambiguous,
including after restart. Neither replay nor recovery MAY resend that job.

**LG-Q06** A higher policy revision MUST atomically cancel ready/claimed jobs.
Admission and BeginDispatch MUST require the current exact, enabled revision.
Dispatching jobs retain only fenced completion or ambiguity recovery.

**LG-Q07** Lower revisions and equal revisions with a different digest/enabled
flag MUST be rejected. Applying an identical tuple MUST be a no-op.

**LG-Q08** Global/App/tenant/destination/workflow token budgets and the attempt
increment MUST be committed atomically with BeginDispatch. Exhaustion MUST
leave the job claimed without consuming an attempt; budget state MUST survive
reopening. RFC-033 §13.1's production batch worker MUST charge once per external
batch, count active claim groups for concurrency, and rotate tenant/subscription
routes. The individual-job transition primitives remain available to this
historical queue-trace suite, not as a singleton wire transport fallback.

**LG-Q09** TTL expiry and bounded maintenance MUST release safe expired work.
Terminal jobs MUST remain until their retention deadline; receipts MUST remain
through the deduplication window and while referenced by retained jobs. Cleanup
MUST NOT delete a referenced receipt or exceed its requested job/receipt bound.

**LG-Q10** A successful observation MUST require a preceding fenced dispatch
reservation and an accepted RFC-033 response. It MUST NOT be fabricated by
finishing a mere claim. Local acceptance remains explicitly simulated.

**LG-Q11** An unknown transport observation MUST NOT become ready. Lost
completion after a durable dispatch reservation MUST settle ambiguous.

**LG-Q12** Only classifier-proven `not-sent` or `rate-limited` responses MAY
retry dispatched work. Backoff, TTL, maximum attempts, and current revision
MUST be enforced before replay.

**LG-Q13** Reusing a retained delivery identity with a different body digest
MUST conflict without replacing the receipt or any existing job.

The local role MUST use RFC-033's batch-only handler contract, compatible-group
claims, unique `aw_context` arrays, all-member reservation/completion and
ambiguous recovery. Long-running simulation uses the two-second coalescing
threshold; explicit one-shot mode immediately batches the already-polled
backlog. Overflow remains queued. The additional shared batch conformance
vectors and SQLite/PostgreSQL tests in RFC-033 §13.1 cover these requirements;
the original local model below abstracts individual-member transitions only.

## 5. Formal model (normative artifact; bounded evidence)

[`tla/LocalWebhookGateway.tla`](tla/LocalWebhookGateway.tla) models durable
receipts/jobs/revisions, volatile polling checkpoints, atomic fanout admission,
claims, fences, dispatch reservation, safe/unknown outcomes, revision changes,
lease expiry, and restart. Remote writes are structurally absent.

The checked invariants are `TypeOK`, `Bounded`, `NoSkippedProgress`,
`NoRemoteWrites`, `DispatchWasReserved`, `NoStaleRevisionReservation`, and
`SuccessRequiresSimulation`, plus `NoUnknownReplay`. Temporal safety properties are `TerminalStable`
and `FenceMonotonic`. There is no unconditional liveness claim: API failure,
revocation, exhausted capacity/budgets, or halted time can prevent progress.

The finite configuration explores two deliveries, one subscriber, two workers,
two generations, two revisions, and two clock ticks. The fanout configuration
explores one delivery and two subscribers. Retention is outside the modeled
deduplication horizon; its boundary has executable queue cases instead.
This abstraction does not model HTTP bytes, cryptography, SQLite internals,
GitHub availability, or prove the Go implementation refines TLA+.
Hierarchical token accounting, real-time backoff, and byte ceilings are checked
by executable conformance tests rather than modeled as additional state.

Run with an official TLA+ tools JAR and Java:

```sh
java -cp "$TLA2TOOLS_JAR" tlc2.TLC -workers 1 \
  -config specs/tla/LocalWebhookGateway.cfg specs/tla/LocalWebhookGateway.tla
java -cp "$TLA2TOOLS_JAR" tlc2.TLC -workers 1 \
  -config specs/tla/LocalWebhookGateway.fanout.cfg specs/tla/LocalWebhookGateway.tla
```

## 6. Specification-derived executable suites

The versioned [queue trace vectors](fixtures/local-gateway-queue.json) are
normative conformance inputs. `TestSpecQueueTraces` generates Go subtests from
each requirement ID and ordered action/expected-result trace; unknown operations,
unknown error expectations, missing requirements, and undocumented IDs fail.
The [polling vectors](fixtures/local-gateway-polling.json) similarly generate
`TestSpecPolling` subtests against HTTP fixtures, real signed ingress, the PostgreSQL
adapter, and the production `Worker.RunOnce` path.

| Requirements | Model actions | Executable evidence |
|---|---|---|
| LG-Q01, LG-Q13 | `Admit`, `Restart`, `Replay` | durable replay; digest-conflict negative case |
| LG-Q02 | `Admit` | fanout capacity rollback and restart |
| LG-Q03, LG-Q04 | `Claim`, `Renew`, `Recover`, `Finish` | stale CAS/owner and lease recovery |
| LG-Q05, LG-Q11 | `Begin`, `Recover`, `Replay` | crash after dispatch reservation; unsafe retry denial |
| LG-Q06, LG-Q07 | `Revise`, `Begin` | disabled/stale/conflicting revisions |
| LG-Q08, LG-Q10, LG-Q12 | `Begin`, `Finish` | persisted rates, valid acceptance, safe backoff |
| LG-Q09 | `Tick`, `Recover` | TTL and bounded retention cleanup |
| LG-P01 through LG-P05 | `Poll`, `Admit`, `Replay`, `Checkpoint`, `Restart` | scoped GETs, signing, paging, ETag rollback, changed-payload restart replay, late arrivals, cancellation |
| LG-H01 through LG-H04 | `Restart`, `NoRemoteWrites` | CLI role isolation, one-shot outcome reporting, shutdown |

The vectors are intentionally specification-derived examples and negative
traces, **not** automatically extracted TLC counterexamples or an assertion of
complete trace coverage. Cross-connection claims, private-file checks, raw-body
non-persistence, and CLI lifecycle tests supplement those generated suites.

TLC 1.7.4 completed both supplied configurations with no reported violations:
3,493,592 distinct states for the two-delivery configuration and 812,694 for
atomic fanout. These counts describe only these finite bounds and model version.

```sh
POSTGRES_URL=... go -C server test -race ./internal/operational/postgres \
  ./internal/webhookgatewaylocal ./cmd/cao-webhook-gateway
```

## 7. Security and privacy considerations

An Events API token MAY possess permissions beyond those needed; the local
client MUST still emit only scoped GETs and MUST NOT print token values.
Operators SHOULD use a least-privileged read token. This simulation does not
validate a real App installation or production live authorization. Local database
access still reveals declared projections and identities; use a private directory,
protect backups, and remove expired derived state. HMAC verification here tests
the real ingress but does not make a synthetic event a genuine GitHub delivery.
