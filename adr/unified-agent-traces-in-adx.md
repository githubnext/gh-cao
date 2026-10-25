# ADR: Unified agent traces in ADX

## Status

Proposed - 2026-10-25.

This record proposes extending the optional Go ADX add-on implemented at
`7ea765d4c` in [PR #19702](https://github.com/githubnext/gh-cao/pull/19702).
It does not implement or enable trace capture, broaden collection authority,
provision infrastructure, authorize training, or change existing normative
contracts. The current implementation remains described by
[ADX ingestion](../server/ADX-INGEST.md).

## Context

The existing add-on exports bounded retained canonical session snapshots.
Those summaries cannot reconstruct conversation and tool content already
discarded by dashboard normalization. Longer destination retention alone does
not solve this: later canonical snapshots can replace detailed evidence with
sparse evidence after dashboard detail expires.

Root-cause investigation, harness improvement, tool optimization and controlled
SLM dataset construction need approved unified traces captured before that loss.
The desired evidence does not vary by destination pricing tier. Volume,
sampling, retention and buffering should be capacity choices within one design.
Start at moderate scale rather than requiring hyperscale infrastructure.

## Proposed decision

### Capture once through the existing acquisition boundary

Reuse the existing collection visit to `usage/aw_session.jsonl` before
normalization and workspace cleanup discard content. Preserve approved messages,
tool inputs/results/errors, usage, routing, skills, inventories, diagnostics,
grader/eval evidence and observed outcomes.

Unified artifacts are essential-payload projections, not lossless native engine
logs. Missing producer fields remain missing; current producer capability does
not prove historical coverage. Do not add another collector or fetch undeclared
repositories. Keep trace bodies out of canonical dashboard PostgreSQL and
public/static shards.

### One versioned evidence model

Define `TraceEvents`, bounded content parts and `TraceManifest` in TypeSpec,
following the existing generated Go/KQL contract pattern. Preserve exact run
attempt, original ordering and supplied native correlations, producer/version,
artifact digest, coverage/redaction gaps and expected counts/digests.
Corrections create immutable versions. Publish readiness only after verification
of the complete declared trace.

Reuse supplied episode/hop execution lineage: `episodeId`, `hopId`,
`parentHopId` and root/parent references, preserving producer semantics. Do not
invent another execution-lineage scheme or infer relationships from timestamps.
Artifact/configuration versions and dataset provenance remain separate.

Retain observed harness/runtime versions, executing/caller workflow references,
invocation IDs, skill digests and available effective-configuration/tool-schema
references. Do not infer harness from engine ID or compiled configuration from
head SHA. A configuration digest identifies a version but does not explain its
contents.

### Simple, bounded delivery

Start with one Go worker reusing existing lifecycle, credentials and operational
state, plus a bounded private durable spool on suitable existing storage.
Use direct streaming, retry/backoff and the existing 1 MiB request limit;
one trace may span several batches. Keep trace retention independent of
dashboard detail expiry.

Queued ingestion, Blob staging, replicas and custom partitioning are deferred
until measured backlog, throughput or isolation requirements justify them.
Durable storage suitability must be verified; a transient host filesystem is
not a restart-safe spool.

### Capacity-configurable whole-trace sampling

Any destination may ingest all eligible acquired traces or a frozen probability
sample of whole run-attempt traces. Selected traces retain identical approved
schema and fidelity. Never thin events or substitute summaries.

Initially use closed-day frames, a recorded cutoff, fixed sample count and
versioned seeded uniform selection without replacement. Record
`TraceSampleFrames/Membership`, eligible count `N`, selected count `k`, inclusion
probability `k/N`, weight `N/k` for nonempty samples, source coverage and delivery
completeness. An empty eligible frame or zero selection must be explicit, not an
invented weight. Full capture has probability/weight 1 over its eligible acquired
frame, not proof of complete upstream collection.

Declare size/content exclusions before selection. Do not greedily skip large
selected traces or silently refill corrected/withdrawn members. Missing members
invalidate completeness claims. Keep manually selected incident sets separate.
Sampling may miss rare incidents and does not necessarily reduce acquisition
work. Existing episode relationships do not establish that a sample contains
every ancestor or descendant. This is not reuse of canonical `statistical-runs`
membership.

### Universal safeguards

Apply content approval, redaction, access protection and bounded storage before
durable capture. Native secret redaction is not a complete privacy or rights
guarantee; sampling and hashing are not anonymization. Unknown extensions require
explicit admission. Default training exports exclude source-exposed reasoning
unless specifically permitted.

Trace content is untrusted evidence, not instructions. Do not execute embedded
commands, replay live writes, automatically edit harnesses, relax policy or infer
collection authority. Withdrawals stop replay and invalidate affected samples,
findings and datasets; account for spool, backups and derived copies.
Exact erasure depends on verified destination capabilities.

Reconcile usage by source/request/phase. Never sum cumulative snapshots with
per-request costs or add friction attribution to total spend. Unknowns remain
unknown. Run success is not a gold training label or user acceptance.
Statistical uncertainty cannot repair missing-source bias.

## Intended analytical uses

| Use | Deliverable and evidence boundary |
| --- | --- |
| Root-cause analysis | Reconstruct observed failure/recovery chains with tool/error context; distinguish evidence from causal hypotheses. |
| Harness evolution | Compare runtime/harness/configuration cohorts on held-out tasks; turn regressions into evaluation cases before proposing rollout. |
| Misconfiguration detection | Explain tool/server incompatibilities, initialization failures and skill collisions against observed requirements. Expected denials and automatic model resolution are not inherently defects. |
| Tool efficiency | Rank avoidable retry/read loops, oversized responses and batching candidates. Distinguish required polling, changed state and verification; never automatically deduplicate writes. |
| SLM datasets | Reconstruct approved role/tool trajectories with schema binding, quality labels and immutable lineage. Group related tasks/episodes/retries across splits and freeze held-out evaluation. Actual training remains separately authorized. |

The corpus can also support skill/context design, delegation analysis,
dependency reliability, guardrail calibration and producer-conformance checks.
Do not claim exact effective context, complete critical paths or accepted business
outcomes without the required evidence.

Additional enterprise-admin uses remain:

| Idea | Prerequisite and limitation |
| --- | --- |
| Service cost showback | Reconciled episode-linked usage, effective-dated service ownership and allocation rules. Avoid parent/child double counting; sampled estimates are not exact invoices. |
| Trigger storms / duplicate automation | Episode fan-out and repeated target work identify candidates; exact deduplication requires trigger/idempotency evidence. |
| Change blast radius | Observed consumers of server, skill, plugin, endpoint or harness changes; complete decommissioning decisions also require declared inventory and ownership. |
| Incident evidence packets | Episode-linked timelines, configuration/source references and gaps. Identity, access/admission and audit facts retain their authoritative systems; traces are not compliance attestations. |
| Queue fairness / critical-workflow SLOs | Conditional backlog: requires scheduler/admission timestamps, priorities and unstarted/cancelled jobs. Completed traces alone are insufficient. |

## Delivery and acceptance gates

1. **Capture and sampling:** implement the shared TypeSpec contract,
   episode/context bindings, one-worker delivery and optional frozen sampling.
   Verify content fidelity, crash/retry recovery, bounded resources, exact
   readiness, correction/withdrawal and full-versus-sampled schema parity.
2. **Utility pilot:** demonstrate RCA, harness-version comparison, explainable
   configuration findings and tool-efficiency candidates with counterexamples.
   Review precision/coverage and evaluate quality, cost and latency on held-out
   tasks before claiming improvements. Start enterprise extensions with showback
   and observed change exposure.
3. **Datasets and measured expansion:** produce validated lineage-linked SLM
   exports; adjust volume, retention and transport from observed sizes/backlog.
   Approve missing audit/scheduler/configuration sources separately. No automatic
   training or operational remediation.

## Capacity and deployment prerequisites

Illustrations assume 1 MB per trace and 1.2x physical retry/correction overhead,
excluding indexes, buffers, recovery lag, spool and frame metadata:

| Selected traces/day | Retention | Estimated retained uncompressed |
| ---: | ---: | ---: |
| 100 | 7 days | 0.84 GB |
| 1,000 | 30 days | 36 GB |
| 10,000 | 30 days | 360 GB |

These are arithmetic assumptions, not production measurements or selected
settings. Additional scalar harness/configuration binding is estimated at
0.5-2 KB per trace, excluding large inventories. Measure actual median/p95 sizes,
coverage, backlog and CAO CPU/DB/network impact before choosing budgets.

Verify service suitability before activation. The Free offer's approximate
100 GB uncompressed capacity, personal/regulated-data restrictions and missing
purge/private-network capabilities remain deployment constraints, not reasons for
another evidence schema. Resizing does not automatically change terms or
capabilities. Retention/soft deletion is asynchronous. Obtain regional
compute/storage/network quotes; no verified total dollar cost or live throughput
is claimed here.

Operator decisions include approved corpus/use and destination, existing
collection scope, full versus sampled volume, retention/spool budgets,
effective-configuration and ownership sources, evaluation thresholds, withdrawal
process and any isolated replay permissions. Keep the feature default-off until
reviewed.

## Alternatives and consequences

- **Canonical summaries only:** retain the current simple analytics path, but
  cannot recover discarded content or meet trace-based investigation/dataset
  objectives.
- **Different content models by destination tier:** rejected; destination size
  should not change evidence semantics or selected-trace fidelity.
- **Event thinning or opportunistic size-based sampling:** rejected; breaks
  trajectory reconstruction and the declared sampling population.
- **Immediate hyperscale pipeline:** deferred; adds operational resources before
  moderate-scale behavior is measured.

The proposed design reuses acquisition and hosting while introducing a distinct
private trace lifecycle and its associated storage, content-governance and
withdrawal obligations. ADX remains derived analytical evidence, not enrollment,
rollout, billing, execution or training authority. Normative schemas and behavior
must be specified and validated separately if this proposal is accepted.

## Evidence

- Acquisition/loss boundary:
  `server/internal/collect/runner.go:205-285`,
  `activity/session-evidence.mjs:137-349`. Existing export:
  `server/spec/adx.tsp`, `server/internal/sessionexport/session.go`,
  `server/internal/postgresx/session_export.go`,
  `server/internal/adxingest/`.
- Canonical semantics: checked-out
  `specs/dashboard-data.md:23-76,644-767,2024-2058`; persisted source fields:
  `server/spec/storage.tsp:88-455`; metadata projection:
  `dashboard/site/src/data/adapters/unified-session-telemetry.js:53-175`.
- Pinned upstream gh-aw `0a87def68d78763d96f24e903363376ba3a0db96`:
  [unified artifact content/omissions](https://github.com/github/gh-aw/blob/0a87def68d78763d96f24e903363376ba3a0db96/docs/src/content/docs/specs/unified-agent-session-specification.md#L215-L260),
  [episode/harness/workflow/skill context](https://github.com/github/gh-aw/blob/0a87def68d78763d96f24e903363376ba3a0db96/actions/setup/js/types/unified_session.d.ts#L475-L611),
  [tool/message events](https://github.com/github/gh-aw/blob/0a87def68d78763d96f24e903363376ba3a0db96/actions/setup/js/types/unified_session.d.ts#L674-L731).
  Producer capability is not guaranteed historical field coverage.
- Microsoft references:
  [service specifications/terms](https://learn.microsoft.com/en-us/azure/data-explorer/start-for-free),
  [streaming limits](https://learn.microsoft.com/en-us/azure/data-explorer/ingest-data-streaming),
  [retention](https://learn.microsoft.com/en-us/kusto/management/retention-policy?view=azure-data-explorer),
  [purge](https://learn.microsoft.com/en-us/kusto/concepts/data-purge),
  [pricing](https://azure.microsoft.com/en-us/pricing/details/data-explorer/).
  Capabilities and prices may change.
