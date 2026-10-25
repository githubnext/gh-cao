# Optional unified agent traces in ADX

The existing OAuth Go host can capture **approved unified-session evidence**
before dashboard normalization, independently of canonical detail retention.
This is a separate, default-off option, not an expansion of the scalar snapshot
exporter's allowlist. No collection enrollment, policy, resources, training or
live writes are enabled by this implementation.

## Model and lifecycle

`spec/trace.tsp` owns six additional tables, generated Go column contracts and
operator-applied KQL: `TraceEvents`, `TraceContentParts`, `TraceManifest`,
`TraceSampleFrames`, `TraceSampleMembership` and `TraceDatasets`.
Run-attempt trace IDs are hashes of exact repository/run/attempt coordinates;
immutable projection IDs bind the source artifact, content policy and approved
events. They are not anonymization. Native IDs, ordering, parent references,
observed episode/hop bindings and harness/configuration evidence remain supplied
facts, not inferred timestamp edges or guesses from engine IDs/head SHAs.

The existing `usage` artifact acquisition is reused. The runner stages gh-aw's
metadata cache and publishes new cache files only after durable trace admission.
Uncaptured attempts are omitted from the staged read cache so a failed capture
can reacquire them on the next already-authorized collection visit. Existing
canonical cache records are not replaced by those filtered staging copies.
This can increase artifact downloads, especially at initial activation, without
expanding repository scope or introducing another collector.

Content passes a closed operator-reviewed policy and redaction **before**
entering the private spool. Unknown event/data/envelope/provenance extensions
produce explicit gap codes, not unrestricted copy-through. An admitted data
field admits its complete JSON subtree; review that boundary carefully.
Built-in credential-field, GitHub-token, bearer-token and PEM-key redaction is a
backstop, **not a complete privacy detector**. Add reviewed content-specific
patterns and use synthetic traces before approving real content. Native
`provenance.native` is not automatically admitted.

Each approved event is JSON, split into contiguous zero-based UTF-8 text parts
at rune boundaries, with exact content hashes. Explicit nulls and JSON numbers
survive reconstruction. The worker streams batches no larger than 1 MiB,
verifies every deduplicated event/part digest through bounded count/hash queries,
then publishes and verifies the manifest. Physical retry duplicates are expected.
Read through the generated `cao_` functions, never sum raw append-only tables.
Producer omission, unsupported format, absent artifacts, identity conflicts,
redaction and lack of source coverage attestation remain explicit.

One private, existing durable filesystem directory holds immutable approved
payloads, dataset files and an atomic fsync-backed ledger. An OS file lock
serializes acquisition, delivery and offline administration across restart
overlap. Atomic replacement accounts for peak space; uncommitted temporary and
orphan files are recovered under the lock. This is not a distributed outbox.
Use an owner-only dedicated encrypted volume with suitable durable rename/fsync
semantics on a POSIX host; Windows trace spooling is not supported. An ephemeral container directory or network mount with unreliable
locking is unsuitable. The operator confirmation is not a storage durability
probe. Directory permissions are `0700`, file permissions `0600`.

## Activation

First meet [snapshot export prerequisites](ADX-INGEST.md), including OAuth,
PostgreSQL, restart-durable operational services, explicit Entra credentials,
exact canonical repository scope, and one confirmed exporter replica.
Traces additionally require **co-resident collection workers**. Static/Pages,
snapshot-only servers, admission-only, standalone collect/backfill and
per-request Azure Functions cannot capture traces. A narrower trace scope is
allowed; it cannot exceed `CAO_ADX_REPOSITORY_IDS`.

Print `cao-dashboard adx schema` and review/apply the generated tables, mappings,
streaming policies and functions manually. The command now prints snapshot and
trace schemas. Configure destination retention for **all related tables** to
cover the intended evidence lifetime and delivery lag; generated seven-day
retention is only a starting default, not a runtime-applied setting.

Set these in the existing host's private deployment environment:

| Variable | Contract |
| --- | --- |
| `CAO_ADX_TRACES_ENABLED` | Explicit `true`; unset/`false` performs no trace policy/file/client/worker activity |
| `CAO_ADX_TRACE_DIRECTORY` | Existing dedicated private durable spool directory |
| `CAO_ADX_TRACE_DURABLE_STORAGE` | `true` only after verifying storage suitability |
| `CAO_ADX_TRACE_REPOSITORIES` | Exact lowercase `owner/repository` names, comma separated |
| `CAO_ADX_TRACE_POLICY` | Private versioned policy JSON file |
| `CAO_ADX_TRACE_MODE` | Explicit `full` or `sample`; identical approved trace fidelity |
| `CAO_ADX_TRACE_SAMPLE_COUNT` | Nonnegative fixed count per repository/completion-day frame in sample mode; zero is explicit zero selection |
| `CAO_ADX_TRACE_SEED` | Predeclared seed, at least 16 bytes in sample mode; record before observing outcomes |
| `CAO_ADX_TRACE_FRAME_DELAY` | Grace after UTC day end, default `24h`, allowed `1h` to `168h` |
| `CAO_ADX_TRACE_MAX_BYTES` | Source and approved-event-content eligibility ceiling, default 8 MiB, range 1 KiB to 32 MiB |
| `CAO_ADX_TRACE_SPOOL_BYTES` | Peak private directory budget, default 256 MiB, at least twice the trace ceiling, at most 1 TiB |
| `CAO_ADX_TRACE_MAX_ENTRIES` | Total retained record/frame/dataset entries, default 10,000, maximum 100,000; includes no-replay tombstones |
| `CAO_ADX_TRACE_RETENTION` | Local evidence lifetime, default `168h`, range `24h` to `8760h`, greater than frame delay |

Ledger JSON is capped at 64 MiB, one projection at four times the
trace ceiling, each trace at 32,768 events, staged cache files at 64 MiB and
cache lines at 32 MiB. Entry exhaustion fails closed; tombstones are not silently
evicted. Size storage/entry budgets and the operational rotation process for the
measured corpus. Increasing destination capacity does not increase these budgets
automatically. No production throughput or dollar-price claim is made.

Minimal **synthetic-only** policy example:

```json
{
  "version": "synthetic-v1",
  "contentApproved": true,
  "rightsApproved": true,
  "events": {
    "session.format": ["version"],
    "workflow.info": ["engineId", "engineHarness", "agentVersion", "workflowRef", "callerWorkflowRef", "workflowCallId", "episode"],
    "user.message": ["content", "sessionId"],
    "assistant.message": ["content", "channel", "sessionId"],
    "tool.execution_start": ["toolCallId", "toolName", "input"],
    "tool.execution_complete": ["toolCallId", "toolName", "output", "result", "error", "success"]
  },
  "redactPatterns": []
}
```

Approve additional observed usage/routing/skill/configuration/grader/eval fields
explicitly for their intended use. Do not blindly admit every raw producer
extension. Changing policy, sampling seed/mode/count, scope or eligibility ceiling
in an existing ledger is rejected: withdraw/reconcile the old corpus and use a
reviewed fresh destination identity/spool rather than rewriting frozen evidence.
Programmatic `Config.ADXTraces` can explicitly disable ambient trace settings.

## Sampling and readiness

Full capture delivers eligible acquired traces immediately. Sample mode waits
for frozen repository/completion-day frames. Both modes record identical
whole-trace evidence. Frames describe **observed acquired completed run
attempts**, not all GitHub runs, all episode relatives or a complete upstream
population. Collection limits, omitted/expired artifacts and prior source gaps
cannot be repaired by weights.

At the first worker pass after UTC day end plus the grace, HMAC-SHA256 ranking
with a predeclared seed freezes uniform pseudo-random selection without
replacement. Frames retain `N`, selected `k`, actual cutoff, seed digest,
algorithm version, source gaps, preselection exclusions and membership digest.
Members carry `k/N` and `N/k`; full-mode members have probability/weight one.
There are no invented weights for empty or zero-selection frames. Frames with
known source gaps are `incomplete`, not ready statistical populations.

The entire selected approved trace is delivered. Missing large selected traces
are not greedily replaced. Corrections or late arrivals invalidate frozen
frames without refilling membership. Separate incident picks are not mixed into
the probability sample. Sampling reduces retained selected evidence, **not**
necessarily acquisition or preselection spool needs. Unselected eligible
payloads remain bounded by the same local retention/budget.

## Investigation and datasets

Generated parameterized functions reconstruct hash-verified events and provide
observed timelines, harness context, configuration/error candidates, oversized
tool responses and repeated starts. These are investigation inputs, not causal
diagnoses or savings estimates. Use a trace's hashed `RunId` and destination
`Namespace` from its manifest:

```kusto
cao_TraceTimeline("NAMESPACE", "TRACE_ID", 1024)
cao_TraceContext("NAMESPACE", "TRACE_ID", 1024)
cao_TraceConfigurationCandidates("NAMESPACE", "TRACE_ID", 1024)
cao_TraceToolResponseCandidates("NAMESPACE", "TRACE_ID", 65536, 1024)
cao_TraceRepeatedToolCandidates("NAMESPACE", "TRACE_ID", 2, 1024)
```

The event limit bounds a sequence prefix, not a claim of whole-trace coverage.
Expected polling, verification reads, intentional denials and automatically
resolved model changes are counterexamples to naive recommendations.
Held-out task evaluation, precision/coverage review and effective configuration
are required before claiming improvements. Reconciled usage is not billing:
never sum cumulative snapshots and per-request contributions. Showback,
trigger-storm analysis, change exposure and incident packets can consume the same
evidence; exact ownership, trigger idempotency, audit and scheduler/SLO evidence
remain separate prerequisites. This feature adds no such collectors.

Offline commands use the same private deployment configuration without obtaining
Azure tokens or executing tools:

```sh
cao-dashboard adx traces status
cao-dashboard adx traces dataset --spec /private/reviewed-dataset.json
cao-dashboard adx traces withdraw owner/repository
```

A dataset spec is a private JSON object with `ID`, `Transform`, `Evaluator`,
`TrainingApproved: true`, `ToolSchemaEvidence` identifying the reviewed historical
configuration/schema evidence, bound `ToolSchemas` (`name` and object
`parameters` schemas), and
`Entries`. Each entry supplies `TraceID`, exact `TraceVersion`, `TaskGroup`,
`Split` (`train`, `validation`, `test`), `QualityLabel` and `LabelEvidence`.
The exporter requires current ready retained source evidence and rejects
duplicate traces, unbound tools and related episode/run-attempt split leakage.
It writes a bounded immutable native-trajectory JSONL file **inside the spool**,
including source/policy/transform/evaluator/schema/split/label lineage, and queues
ADX `TraceDatasets` metadata. This is not an OpenAI-format claim, an inferred
quality label or an actual fine-tuning/training operation.
Default training records include approved user/system/assistant-final text and
tool-call/result events. They exclude known reasoning events/blocks,
analysis-channel updates and unneeded producer fields. Unsupported message
content blocks are rejected, rather than silently converted; this initial
dataset transform is text-only. Native tool correlations must be unique and
complete, and the trajectory must end in an assistant response.
Operational runtime success is not acceptance.

## Withdrawal, retention and operations

Collection unenrollment durably stops replay, removes local content and
invalidates related frames/datasets before ordinary repository erasure completes.
Explicit offline withdrawal persists the same intent. The running worker then
publishes a newer manifest/tombstones; generated queries resolve latest revision
**before** readiness filtering. Destination outages delay remote invalidation;
stop consumer access when immediate withdrawal is required.

Local evidence expiry also invalidates readiness and derived local datasets.
Downloaded/exported copies, backups and physical ADX erasure require the
operator's verified destination-specific process. Disabling the feature is not
erasure. Keep the worker active until required tombstones are verified, then
disable and drain it. Do not reuse a withdrawn ledger to reenroll the same
corpus. No claim of synchronous physical purge is made.

`App.ADXTraceStatus()` and the offline status command expose bounded counts and
degradation independently of snapshot export/dashboard readiness. Acquisition
backpressure may retry collection rather than silently losing the source; ADX
delivery failures retry independently with bounded exponential delay. Warnings
contain no trace bodies, credentials or raw errors.

Tests cover full/sample fidelity, UTF-8 boundaries, redaction/missingness, seed
determinism, zero samples, source gaps, crash/retry recovery, immutable
corrections, resource limits, local withdrawal, datasets and actual loopback
streaming HTTP requests. The simulator is not a Kusto engine. Before production
acceptance, apply KQL and exercise synthetic reconstruction, helper queries,
retention, visibility and erasure using the actual destination/principal.
