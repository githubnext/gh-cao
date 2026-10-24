# Operational server mode model

`ServerOperationalStorage.tla` is the bounded TLA+ companion to
[`../server-operational-storage.md`](../server-operational-storage.md).
It checks the local browser preview and both explicit Go operational backends
against one immutable resolved policy.

## Bounds and assumptions

Both TLC configurations use two distinct jobs, one pending-work slot, and one
application-process restart. Initial states enumerate all three requested modes
and nine topology records: a valid hosted topology and independent violations
of OAuth, HTTPS, process-owned listener, one process, one replica, volatile
acknowledgement, co-location, or positive collection worker count. Collection
is enabled in this model. Redis may use separately owned workers and does not
require memory's one-process acknowledgements.

The Redis configuration assumes a restart-persistent operational namespace
survives an **application** restart. It does not prove Redis-server crash
durability or apply that assumption to a process-isolated provider profile.
The memory configuration intentionally clears operational state on close.
Evidence remains durable in both configurations, outside operational storage.
Session identities are abstracted to their issuing process epoch; GitHub-issued
credentials are outside the model and are not claimed to expire on restart.

Jobs represent currently authorized work under the immutable resolved policy.
Redis startup assumes usable authorized enrollment has been restored from its
restart-persistent namespace; it does not impose memory's new bootstrap gate.
`Bootstrap` abstracts memory's complete fresh authorized enumeration. Partial enumeration
cannot take that action. Bootstrap progress assumes such enumeration eventually
succeeds and is weakly fairly scheduled. Shutdown progress assumes worker join
and store close are weakly fairly scheduled. Completion and restarts are not
assumed fair, so the model does **not** claim every accepted job eventually
completes, particularly across a memory restart.

Terminal browser, rejected, and closed states intentionally have no required
transition. TLC deadlock reporting is therefore disabled; explicit bootstrap
and shutdown temporal properties check the required progress instead.

## Checked properties and implementation correspondence

| Property | Implementation boundary |
| --- | --- |
| Policy authority; browser isolation | Local CLI mode parser, `serve-hosted --operational-store`, production composed policy loader |
| Memory topology and security | Host-policy resolution, operational factory, centralized capability validation |
| Bootstrap/admission gates | Volatile recovery and collection HTTP admission |
| Pending-work capacity; atomic delivery/task identity | Shared operational adapter admission contracts and conformance tests |
| Backfill checkpoint after admission | Collection/backfill checkpoint transitions |
| Owner-checked work | Lease ownership and completion transitions |
| Memory session epochs and explicit loss | Fresh login/session state and bounded volatile adapter |
| Join before close; eventual shutdown | Go application/worker lifecycle and launcher child-close handling |
| Retained evidence; Redis restart state | Evidence lake outside adapters; restart-persistent Redis namespace |

The model abstracts away clock-driven TTLs, encryption, refresh CAS, remote
revocation, signature verification, GitHub scope identities and shard collisions,
database ingestion/query correctness, allocator memory, and network failures.
It is a checked specification of these transitions, not a proof that the Go,
JavaScript, Redis scripts, or external systems implement them. Adapter, HTTP,
startup/recovery, policy-parity, and launcher tests remain required.

## Running TLC

Use Java 21 and the official
[`tla2tools.jar` v1.7.4](https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar).
Keep downloaded tools and TLC state outside tracked source files. From the
repository root, set `TLA2TOOLS_JAR` to that local jar and run:

```bash
java -Xmx512m -cp "$TLA2TOOLS_JAR" tlc2.TLC -workers 1 \
  -metadir test-results/tla/redis \
  -config specs/tla/ServerOperationalStorage.redis.cfg \
  specs/tla/ServerOperationalStorage.tla
java -Xmx512m -cp "$TLA2TOOLS_JAR" tlc2.TLC -workers 1 \
  -metadir test-results/tla/memory \
  -config specs/tla/ServerOperationalStorage.memory.cfg \
  specs/tla/ServerOperationalStorage.tla
```

A successful run must report no invariant or temporal-property violations.
Both configurations are required: memory intentionally weakens restart
durability, not admission atomicity, authorization, capacity, or ownership.

# Hosted CLI actions model

[`HostedCLIActions.tla`](HostedCLIActions.tla) models the optional
`serve-hosted --experimental --hosted-cli-actions` mode specified in
[`../server-hosted-cli.md`](../server-hosted-cli.md): admin step-up admission,
the queued executor, and the bounded single-use installation-token broker.
`HostedCLIActions.enabled.cfg` checks the enabled mode with two users (one
listed admin), one repository, two commands, two tokens, pool size one, a
step-up maximum age of two ticks, and three clock values.
`HostedCLIActions.disabled.cfg` checks that the mode without both required
server flags admits nothing.

| Property | Implementation boundary |
| --- | --- |
| `DisabledAdmitsNothing` | `--experimental` and `--hosted-cli-actions` gates; `/__cli_commands` routes are not registered unless both flags are present |
| `AdmissionAuthorized` | `adminAuthorizedContext` step-up (`oauth.adminAssured`) and the requester's live repository permission in `hosted_cli.go` |
| `ExecutionRechecked` | Executor collaborator-permission recheck before running in `hostedcli/executor.go` |
| `WorkTokenOnlyAfterVerification` | `Executor.verifyRequester` checks write access with a revoked `metadata:read` token before `Acquire` or `Prefetch` issues any work-scoped token |
| `CancelAuthorized` | `requireRepositoryWrite` in `hosted_cli.go` before cancel |
| `QueueMutationsAudited` | `AuditedCLICommandQueue` enqueue/retry/cancel commits its mutation and hash-chain append in one PostgreSQL/SQLite transaction; failed or timed-out transactions stutter, and an uncertain commit never triggers compensating cancellation |
| `TokenSingleUse`; `ReleasedNeverReused` | `hostedcli/broker.go` `Acquire`/`Release`: a token leaves the pool once and is revoked on release |
| `TokenScopedToRepository` | Repository-scoped installation token minting in `githubapp/scoped_token.go` |
| `HeldOnlyByActiveCommand`; `PoolBoundedAndUnused` | Broker pool bounds (`MaxPooled`, `MaxPooledPerScope`) and prefetch |
| `TerminalTokenInvalidated` (liveness) | `Release` revocation, `Maintain` retry until expiry, `Close` |

Time is abstract: step-up freshness and token expiry use bounded ticks rather
than wall-clock TTLs. The model abstracts away GitHub's 2FA/SSO disclosure,
OAuth token refresh, subprocess isolation, output redaction, queue leases,
and network failures other than failed revocation and aborted audit
transactions. Admission/cancel failures leave the state unchanged; a lost
commit response is observationally identical to a committed mutation with its
audit event. Real-store tests cover idempotency, lock/statement deadlines, and
rollback; the model does not represent SQL timing or hash bytes. Mutations that skip the
execution recheck, return a released token to the pool, or skip admission
step-up each violate `Safety`.

```bash
java -Xmx3g -cp "$TLA2TOOLS_JAR" tlc2.TLC -workers 2 \
  -metadir test-results/tla/hosted-cli-enabled \
  -config specs/tla/HostedCLIActions.enabled.cfg \
  specs/tla/HostedCLIActions.tla
java -Xmx512m -cp "$TLA2TOOLS_JAR" tlc2.TLC -workers 1 \
  -metadir test-results/tla/hosted-cli-disabled \
  -config specs/tla/HostedCLIActions.disabled.cfg \
  specs/tla/HostedCLIActions.tla
```

The enabled configuration explores about 600,000 distinct states.

## Statistical sampling publication

[`StatisticalSampling.tla`](StatisticalSampling.tla) is the bounded lifecycle
companion to [`dashboard-data.md` §5.1.1](../dashboard-data.md#511-independent-statistical-run-projection).
Three eligible identities, one sample slot and two canonical revisions exercise
staged/committed/aborted publication, budget and missing-clock rejection,
incomplete quality, consumer filtering, incremental replacement/retention, and
one application restart. Each Run owns two abstract children and one structural
parent. Identity-ordered priorities in a single stratum abstract the deterministic
sampler; hash bytes, UTC eligibility, failure-reserve allocation, actual SQL,
namespace isolation, concurrency and physical memory are covered by executable
tests, not proved by this model. No fairness or eventual-publication claim is made.

| Checked property | Implementation/test boundary |
| --- | --- |
| `WholeBundles`, `Bounded`, `UnavailableIsEmpty` | PostgreSQL all-or-nothing typed bundle admission and budget rejection |
| `NoPartialPublication`, `AbortPreservesPublication` | Transactional publication, failed validation and cancellation rollback |
| `ExactPublicationIndependent` | Sampling-only rejection preserves canonical exact publication |
| `HonestWeights`, `FrozenSelection`, `SelectionBeforeFilter` | Complete recorded frames, deterministic membership and no consumer refill |
| `FrozenAcrossIncremental`, `RestartPreservesEvidence` | Frozen PostgreSQL evidence survives incremental revisions, partition retention and application restart |

```bash
java -XX:+UseParallelGC -Xmx1g -cp "$TLA2TOOLS_JAR" tlc2.TLC -workers 2 \
  -metadir /tmp/cao-statistical-tla \
  -config specs/tla/StatisticalSampling.cfg specs/tla/StatisticalSampling.tla
```

The statistical conformance command also runs TLC and requires a complete
state-space check without violations, alongside the declared real-backend tests:

```bash
POSTGRES_URL=... TLA2TOOLS_JAR=/path/to/tla2tools.jar \
  npm run test:conformance:statistical-sampling
```

## Hosted container execution

[`HostedCLIExecution.tla`](HostedCLIExecution.tla) separately checks durable
container identity, at-most-once dispatch, lease fencing, and concurrency
retention across failed runtime clients and executor restart. `Start`,
`CollectOutput`, and `ClientExit` distinguish launching work from observing its
output through `ContainerLifecycle`. The model abstracts the private
in-container Git bootstrap and GitHub effects. Failed inspection/stop is a
stuttering step, not exit evidence; recovery requires confirmation on the
original runtime plus an operator's attestation that the old executor and its
runtime requests stopped.

`RunningClaimed` and `UnconfirmedClaimed` guard `Executor.execute` settlement;
`StoppedEvidence` distinguishes runtime client exit from workload termination.
`CapacityHeld` and `AtMostOnce` guard the durable queue's claim and recovery
transitions. The bounded configuration uses two commands, one execution slot,
and two lease generations. `AtMostOnce` applies per command ID, not across
separately admitted idempotent updates or to external effects.

```bash
java -Xmx512m -cp "$TLA2TOOLS_JAR" tlc2.TLC -workers 1 \
  -metadir test-results/tla/hosted-cli-execution \
  -config specs/tla/HostedCLIExecution.cfg specs/tla/HostedCLIExecution.tla
```
