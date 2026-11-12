# Independent audit evidence delivery

`cao-dashboard audit deliver` is an **opt-in, bounded job**, not a daemon or a
second application outbox. It reads committed rows through the same `ListAudit`
and `VerifyAudit` interfaces as `audit export --after` and `audit verify --anchor`.
It never appends application events, deletes rows, or performs retention.
Producer crash durability is the primary audit store's responsibility.

No destination, schedule, alert rule, or external service is activated by this
repository. The HTTP adapter below is an **operator dependency**, not a bundled
service or a production-validated cloud integration. Do not point the job at a
generic logging/SIEM ingestion endpoint.

## Delivery and trust contract

Each run loads its checkpoint from the independent destination, reads at most
`--max-events` committed rows after the acknowledged cursor, reconstructs their
hashes, and verifies the retained chain against both the previous independent
head and the reconstructed page end. It exports only those verified rows.
The independently verified head can be ahead of the delivered cursor; neither
concurrent appends nor a page limit advances delivery past acknowledged rows.
Retention racing with the read or verification fails closed if continuity or
required anchors can no longer be established.

Each event is conditionally created at a stable stream/sequence key and then
read back byte-for-byte. Existing identical content is an acknowledged replay;
different content is a failure, never an overwrite. After all events are
acknowledged, the job creates and reads back a content-addressed verification
document, then conditionally replaces and reads back the checkpoint.
There is no unbounded retry loop. A failed run exits nonzero and the next
scheduled invocation resumes from the destination's checkpoint.

This is **at-least-once delivery attempts with duplicate-safe object storage**
under the adapter contract, not exactly-once network delivery. A crash after
delivery but before checkpoint persistence replays identical objects. Lost
checkpoint acknowledgements are resolved by reading the committed checkpoint on
restart. CAS conflicts never overwrite another writer's progress: the run fails;
the next invocation re-reads and validates the winning checkpoint and its anchors.
Consumers should identify an event by `(stream, sequence, hash)`, rejecting a
different hash for an already-seen stream/sequence.

Use a unique stable `--stream` for each **deployment and operational namespace**.
Never reuse it for another namespace, restored/replaced database, or independent
chain. Objects include the stream and event sequence/previous hash/hash; the
checkpoint includes stream, delivered sequence/hash, full verification result,
and verification time. The stream label is an operator binding, not a claim
that CAO discovers the identity of an external account.

The receiver must be administered independently of the application/database
roles. TLS/read-back checks cannot establish its physical durability, retention,
or immutability. Require documented durable writes, strongly consistent reads
and conditional writes, retained versions of mutable checkpoints, create-only
event/verification objects, deletion protection, and independently enforced
write-once retention appropriate for the audit period. Protect the latest
checkpoint from rollback. A local file, mounted volume, HTTP success, or a SIEM
label is not proof of these properties. An attacker controlling both the database
and the independent anchors can defeat the chain.

## HTTPS object adapter

Configure `CAO_AUDIT_DELIVERY_URL` with the adapter's HTTPS base path and
`CAO_AUDIT_DELIVERY_TOKEN` with its bearer credential **through the deployment's
secret environment**, never flags, committed files, shell history, or logs.
URL userinfo, query strings (including signed URLs), fragments, and redirects are
rejected. Requests do not forward credentials to redirect destinations.
Use the platform CA trust store. Each HTTP request has a 30-second deadline;
responses and individual objects are capped at 128 KiB.

The adapter must implement these paths relative to the base URL:

| Object | Path | Write condition |
| --- | --- | --- |
| Event envelope | `<stream>/events/<20-digit-sequence>.json` | `PUT` with `If-None-Match: *` |
| Verification/checkpoint evidence | `<stream>/verifications/<sha256-of-body>.json` | `PUT` with `If-None-Match: *` |
| Current checkpoint | `<stream>/checkpoint.json` | First `PUT`: `If-None-Match: *`; subsequent `PUT`: `If-Match: "<ETag>"` |

`GET` returns exact stored JSON bytes and `200`; checkpoint reads must also return
a strong quoted `ETag`. Only absent objects return `404`. Authentication failures
must not masquerade as absence. A synchronously durable `PUT` returns `200`, `201`,
or `204`; condition failure returns `412`. `202` or merely queued ingestion is
not delivery. `GET` read-back is required even after successful `PUT`. Checkpoint
replacement must atomically compare its ETag and store the complete new object.
Disable intermediary caching and transformations. Preserve all successful
verification objects, including idle runs and CAS losers.

The adapter may map this protocol to an approved provider, but that provider's
credentials, retention policies, account isolation, adapter implementation, and
deployment information are not specified here. CAO does not provision them.
The exporter needs GET/create on its event and verification prefixes and
GET/conditional-replace on its checkpoint, **not deletion or unconditional
overwrite authority**. A conditional client alone does not enforce server-side
permissions.

Run the executable protocol conformance test against an explicitly approved
test prefix; it writes a random `conformance-*` stream and intentionally does
not delete its evidence. Inject the two environment variables from a secret
manager, then run:

```sh
go -C server test ./internal/auditdelivery -run '^TestDestinationConformance$' -count=1 -v
```

The variables are `CAO_AUDIT_CONFORMANCE_URL` and
`CAO_AUDIT_CONFORMANCE_TOKEN`. Without the URL the provider test skips; the
in-process TLS contract/fault fixtures still run in ordinary unit tests.
The conformance test checks creation, exact replay, content conflicts, read-back,
namespace separation, checkpoint CAS and stale-writer rejection. It **does not**
prove WORM configuration, permission isolation, power-loss durability, retention,
alert delivery, or control operation over an audit period.

## Enrollment and scheduled operation

Provision the database schema/namespace administratively first. Use the audit
reader database role with SELECT on the existing audit table and namespace,
not owner/runtime credentials. The audit-only `OpenAuditReader` path must be
deployed with the database-role hardening change (no schema initialization,
maintenance, or write locks during open). Keep database credentials in the
existing `CAO_POSTGRES_URL` secret environment and select the same reviewed
policy and operational namespace as the primary store. No ordinary runtime
audit-delete right is needed.

Local testing also uses PostgreSQL. Provision an isolated operational namespace,
then select it with `CAO_POSTGRES_URL` and `CAO_OPERATIONAL_NAMESPACE`.
Delivery uses the SELECT-only evidence-reader role; it never creates storage
or retains old events. Verification needs runtime append authority to record
its result. The HTTPS destination, checkpoint protocol, and enrollment
anchor requirements below are unchanged. See
[local PostgreSQL testing](../POSTGRES.md#local-postgresql-development) for self-contained
tests with an in-process TLS fixture; these do not establish production role
isolation or independent storage durability.

Before first use, independently preserve and approve a fresh `audit verify`
result. Create an anchor JSON file outside the database, readable only by the
enrollment operator, with the exact stream, verification head and observation
time:

```json
{
  "stream": "production-cao-namespace",
  "head": { "sequence": 123, "hash": "<64-character verified SHA-256>" },
  "observed_at": "<RFC3339 UTC time of independent observation>"
}
```

These placeholders are documentation, not deployable credentials or evidence.
The first observation is an explicit trust-establishment ceremony; it cannot
prove the database was not rewritten beforehand. Preserve its approval and
provenance separately. `audit verify` currently records its own outcome and
therefore needs append permission; `audit deliver` directly invokes the read-only
verifier and requires no append permission.

Run enrollment once with secrets already injected:

```sh
cao-dashboard audit deliver --stream production-cao-namespace \
  --bootstrap-anchor /secure/enrollment-anchor.json --max-events 1000
```

Enrollment requires retained history starting at sequence 1. If retention has
already removed the prefix, the command refuses to invent coverage. Reconcile
from independent archived evidence and establish a separately reviewed migration
plan; this command has no skip-gap or trust-current-database switch.

Normal invocations **must omit `--bootstrap-anchor`**. Missing checkpoints then
fail closed rather than re-enrolling. Existing checkpoints always take precedence
over bootstrap input. Their anchors must be retained, not future-dated, and no
older than `--max-anchor-age` (default 24h; allowed 1m-168h). Every successful idle
run renews independently stored verification evidence without claiming new events.
Expired/missing anchors, rollback, gaps and mismatches require investigation;
do not delete a checkpoint or generate a new anchor automatically to clear them.

For a systemd deployment, install the adjacent service/timer templates and the
reviewed `cao-dashboard` binary. Create the root-owned mode-0600 environment file
`/etc/cao/audit-delivery/<stream>.env` using the secret manager. It contains the
destination URL/token, reader database connection, namespace and policy path.
Make only non-secret policy files readable by the service's dynamic user.
After enrollment and change approval, explicitly enable the chosen timer:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now cao-audit-delivery@production-cao-namespace.timer
```

The timer uses a five-minute interval with up to one minute of jitter; it does
not overlap the same service instance. Each run is capped at 1000 events and
five minutes, with an independent six-minute service kill bound. No local
checkpoint volume is needed. Other container/job schedulers can invoke the same
bounded command with isolated reader/destination credentials and no public
listener. Do not add exporter credentials to the web/executor environment.

## Monitoring and operator evidence still required

The command emits one JSON result: success to stdout, failure to stderr, with
`status`, fixed `stage`, acknowledged count, last committed sequence, verified
head and remaining backlog. Exit status is nonzero on failure. On failed runs,
the count can include acknowledged objects whose checkpoint was not committed;
do not interpret it as committed delivery progress. Raw database/network errors,
destination URLs, response bodies and credentials are excluded. Journal/collector
shipping is a monitoring aid, **not** an independent delivery receipt.

Connect the scheduler's failed/timed-out service signal to the approved on-call
route (for example an operator-provided systemd `OnFailure` drop-in). Also monitor
the independent checkpoint from a separately administered checker. For the sample
cadence, alert on any failed run, no new `verified_at` for 15 minutes, and a
nondecreasing positive `remaining` backlog over three runs. Escalate anchor,
continuity and verification failures immediately. Set throughput and backlog-age
alarms to catch up comfortably before the 400-day primary retention horizon;
the full-chain verification must fit the configured deadline. Measure that
against the actual deployment before enabling the job. A timeout does not
produce a successful verification or advance the cursor.

No universal notification provider is configured here. Missing-job/heartbeat
monitoring must be independent: a dead scheduler cannot report its own failure.
Preserve alert delivery and acknowledgement, investigation disposition, and
incident/recovery follow-up for CC7.2/CC7.3. Operators still need approved
destination/retention/role configuration, conformance results against the real
adapter, initial anchor provenance, scheduled success and forced-failure samples,
restart/replay and retention-gap exercises, restore testing, and evidence that
alerts were received and acted on. Repository tests are software verification,
not fabricated organizational control-operation evidence or a SOC 2 attestation.
