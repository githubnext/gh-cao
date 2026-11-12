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
native acquisition and publication, cold start, gap recovery, rate-limit
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

App-backed collection requires `CAO_COLLECT_NATIVE=true`, durable
deployment-scoped PostgreSQL operational ownership, and canonical PostgreSQL.
An App identifier without native selection MUST fail at startup; the former
filesystem collector and volatile memory collector are not fallback profiles.
Without an App identifier the existing Actions snapshot profile remains the
default.

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

- The native Go acquisition adapter MUST preserve the source coverage,
  canonical equivalence and activation gates of the Actions profile without
  shelling out to the Activity or gh-aw runtime.
- The collector profile MUST reconstruct retained operational-value observations
  with a native Go implementation conforming to
  `specs/operational-value-history.md`, using the same campaign adapter contract,
  retention window, historical campaign selection, and installation-scoped
  GitHub credentials as the Actions profile.
- Native adapters MUST preserve the authoritative canonical mapping rather than
  define alternate semantics. They MUST compare native records with the Activity
  adapter/normalizer using independent fixture oracles. Routing aggregation MUST
  execute the authoritative Dashboard Language queries through the production
  Go query engine. The shard format and payload-hash manifest remain shared
  contracts, not separately invented native formats.
- A conforming implementation MUST provide a profile equivalence test that
  asserts identical canonical records from Actions-published evidence and
  native exact-attempt acquisition covering the same window.

### 2.3 HTTP host independence

The acquisition profile is independent of which process owns the HTTP
listener. When an external host serves CAO's handler, it MUST start the
selected ingestion profile before admitting requests and MUST allow CAO's
background work to stop only after the HTTP host has drained. The external
host MUST NOT bypass CAO's webhook verification or independently infer
collection scope from requests. The mutual-exclusion, enrollment, durable
admission, and recovery requirements in this document remain unchanged.

### 2.4 Native acquisition and publication boundary

The native Go service under `server/internal/collectnative/` replaces workflow
artifact acquisition, metadata-only session extraction and raw JSONL compaction
without running Node, `cao-activity`, `gh-aw` or another subprocess. Its provider
uses installation-scoped GitHub APIs and the existing shared quota service.
Archive acquisition and projection MUST enforce explicit byte, entry,
pagination, attempt and cancellation bounds. Archive paths are logical names,
not filesystem locations.

Historical run tasks use the native exact-attempt REST lookup. Acquisition MUST
obtain an enrollment-fenced exact-attempt claim before the provider lookup.
A native task MUST carry its admission-time enrollment epoch. Exact-attempt
claims MUST compare that epoch atomically before provider lookup, rather than
retargeting a previously admitted task to a newer enrollment. Native task
deduplication and historical cursor identities MUST include the epoch.
A provider MUST verify the returned repository, run, attempt and agentic workflow
path before acquiring artifacts. A missing exact-attempt capability MUST fail;
it MUST NOT substitute the latest attempt or expand the task into repository
discovery.

Historical run processing MUST use a fresh, opaque durable queue receipt for
every delivery and reclaim, including reclaim by the same consumer name.
Completion, delayed retry and dead-letter replacement MUST atomically reject a
superseded receipt. Backends without that owned capability MUST reject native
run processing; a separate ownership read followed by an unfenced mutation is
not equivalent. The PostgreSQL operational adapter supplies this contract.
Processing failure counts MUST remain separate from the GitHub run attempt;
claim contention and shutdown MUST NOT spend the failure budget. Workers MUST
persist the safe publication intent before ACK, so another process can recover
and activate it after either a pre-ACK or post-ACK crash. A running native role
MUST drain pending publication intents even when no collection task remains.

Native historical discovery is an explicit task kind, never an exact-attempt
fallback. Admission MUST persist immutable inclusive UTC bounds before querying
GitHub; an unset lower retention window admits all available history through
that frozen upper bound. Workflow enumeration and per-workflow run cursors MUST
advance only through receipt-fenced replacement after every produced exact
attempt is durable. A retry or replica takeover MUST preserve the original
window. The cursor holds at most one workflow page, and separate enumeration and
exact-attempt queue capacities MUST allow acquired work to drain under discovery
backpressure. A running role MUST give both streams scheduling turns, including
fresh discovery when exact attempts are reclaimable; a continuous exact-attempt
backlog MUST NOT starve history. Scheduling preference is process-local liveness
only; shared queue receipts and source claims remain the ownership authority.
Capped or incomplete GitHub searches MUST fail explicitly rather
than record completed discovery. Quota exhaustion and admission backpressure
MUST preserve the failure budget and exact run attempt.
Discovery MUST recheck its enrollment epoch after provider acquisition and before
publishing child tasks or a cursor. Definitively withdrawn, transferred or
superseded enumeration epochs MUST be receipt-fenced acknowledged without
advancing discovery; transient scope-read errors and claim contention MUST remain
recoverable rather than being mistaken for obsolete authority.

The PostgreSQL acquisition adapter owns exact repository/run/attempt claims,
database expiry, generation/token fencing and enrollment epochs. Only
authenticated enrollment reconciliation may change scope; a queued collection
task MUST NOT enroll its own repository. Transfer, withdrawal and reenrollment
MUST invalidate prior claims and pending publications. Safe evidence is a
durable publication intent, not a local cache or proof of canonical activation.
Unconsumed intents MUST NOT expire through ordinary consumed-evidence cleanup.
Capacity exhaustion MUST reject work instead of evicting pending intents.

The native enrollment authority uses a bounded PostgreSQL ownership transaction
for authenticated reconciliation. Cancellation, transaction expiry or loss of
the owning process MUST prevent a resumed callback from publishing membership.
Operational membership sets and installation mappings are indexes and
checkpoints, not authorization sources for native ingestion. Missing indexes
MUST NOT hide source-owned installations; displaced indexes after rollback MUST
NOT grant repository scope. Predicted, uncommitted epochs MUST NOT escape into
task admission.

Ownership changes MUST atomically invalidate claims, pending safe evidence,
the withdrawn repository's canonical graph and its daily contributions. Frozen
statistical bundles MUST become explicitly unavailable, not retain withdrawn
evidence or silently substitute live detail. Unrelated canonical repositories
MUST remain intact.

Configured content destinations MUST complete approved durable admission before
safe evidence is published. Raw sessions MUST NOT enter ordinary acquisition
rows, canonical rows or operational records. Native trace admission reuses the
existing policy, redaction, exact-attempt identity, source-gap, sampling and
immutable delivery contracts. Shared trace storage uses PostgreSQL transactions,
not process mutexes or a shared filesystem.
When shared trace admission participates in native enrollment, its withdrawal
tombstone and approved-object deletion MUST commit in the same PostgreSQL
transaction as source ownership and canonical erasure. A later source failure
MUST roll all destinations back. Separately committed or filesystem trace
destinations cannot participate in this boundary. Reenrollment MUST NOT
implicitly reapprove a previously withdrawn trace destination.

Canonical publication MUST atomically verify the current enrollment epoch and
exact evidence digest, insert the owning canonical graph, advance its revision
and consume the intent. Failure or cancellation MUST roll back all four.
Separate replicas may enumerate bounded pending batches and replay them without
GitHub credentials, Activity caches or local files. Older attempts MUST NOT
replace a newer canonical attempt. Canonical run partitions MUST use recorded
run time, not restart/replay time; linked-detail retention remains enforced.
Acquisition gaps MUST remain visible as untimed `collection.coverage` audits and
partial source metadata, not success-shaped defaults or inferred zeroes.

**Explicit native activation:** `CollectorConfig.NativeAcquisition` selects the
distributed hosted profile, including authenticated enrollment, webhook admission,
receipt-fenced workers, immutable historical discovery, inventory preparation,
publication replay and repository/backfill lifecycle integration. Its
PostgreSQL canonical writer verifies the exact inventory snapshot's readiness
inside the activation transaction. Native session archive and shared trace
destinations use their scoped durable admission and enrollment authority.
Environment deployments MUST explicitly select `CAO_COLLECT_NATIVE=true` and,
for workers, the exact reviewed inventory digest already provisioned through
`Acquisition.PinInventoryConfiguration`. Programmatic callers MAY provide the
immutable reviewed bytes directly. Replacing the shared configuration MUST use
the explicit expected-digest CAS; replicas MUST NOT choose a newer configuration
implicitly. Native activation MUST NOT require an Activity directory, Node or a
local inventory file. Optional Go Value execution is separately
selected and retains its explicit reviewed catalog and policy dependencies. Native
selection MUST reject unsupported legacy limits and malformed settings rather
than silently ignore them or fall back to the retired filesystem producer.

Native Actions job acquisition uses the exact-attempt jobs endpoint
and scoped job-log downloads with shared quota observations, credential-free
signed redirects, cancellation and hard resource limits. Runner output is
ephemeral: only job metadata and allowlisted annotation classes/locations survive,
and runner stdout is never counted again as agent usage. The collector also
projects exact-attempt `usage-activity-summary/v1`, reported token-usage counters
and cumulative AIC, safe-output manifests and execution-state audit findings.
Structured `agent_output.json` reports retain missing-tool/data identifiers,
timestamps and noop occurrences, but not free-text reasons, context or messages;
noop text loss is explicit as `agent-report-text-unavailable`. Immutable
`github_api_calls` counts only `github_rate_limits.jsonl` response-header entries,
not rate-limit probes or the collector's own calls. Invalid, ambiguous,
wrong-attempt or over-budget sources cannot publish partial report/accounting
results.
Unified sessions replace legacy event projections, not immutable aggregates.
Unpriced legacy usage does not infer a model price or a zero AIC total.

Native startup-model analysis follows `github/gh-aw@v1.0.1`
`audit_model_resolution.go` and `audit_model_availability.go`. The full pinned
run `39499464895` session, stdio and same-run reflection fixtures now produce the
upstream high/configuration `model_unavailable` finding and its high-priority
model recommendation. Native acquisition downloads the `agent` artifact as well
as `usage`, and admits its `agent-stdio.log` and `awf-reflect.json` members only
with same-archive exact-attempt identity. The pinned Copilot/routed upload's
`runtime/aw_info.json` is an explicit sibling-layout identity source; metadata in
another artifact never binds an otherwise unidentified archive.

Attribution requires a failed run, a nonzero single agent execution, no native
agent activity, role-specific harness refusal or model-resolution error, and
complete unambiguous provider inventory excluding the exact requested model.
Router traffic and harness wrapper state do not establish native continuation.
Continued, successful, optional-agent or unproven-stop resolution failures produce
the low `model_resolution_failed` advisory, not a causal model-unavailability
finding. Missing, incomplete or contradictory reflection produces
`model_availability_unknown`; a recorded `unsupported_endpoint` cause produces
`model_endpoint_unsupported` without reclassifying the model as absent. The
legacy Pi startup-line fallback applies only when no failed resolution was
recorded. Provider aliases, exact model selectors and per-role stop evidence
remain distinct. Raw log/reflection text is never persisted; descriptions retain
validated identifiers only. Ambiguous or invalid sources produce stable coverage
classifications; byte, line, entry and role/log comparison limits reject excess
work instead of successful truncation. Invalid sessions cannot publish partial
causal findings or be reinterpreted as missing evidence.

Native default audit analysis additionally consumes transient Copilot
`events.jsonl` and admitted agent logs. Primary Copilot events own token/turn
metrics over debug logs; explicit shutdown model metrics, including zero,
override per-event fallback usage and exclude cache tokens. Without events,
Copilot session results and timestamped debug response blocks provide tokens,
turns and reported costs. Output-only Responses API wire histories establish
tool stubs without recounting repeated history or a later matching input.
Claude top-level result snapshots replace earlier snapshots in the
same session, exclude child results and include cache tokens. Without result
entries, Claude mixed text uses maximum per-line generic usage and the first
positive reported cost, including numeric-string and relaxed-JSON forms. Codex native
JSON/JSONL counters deduplicate exact turns within session scope, merge partial
snapshots with explicit zero/overflow semantics and distinguish cache-inclusive
input. Codex legacy thinking transitions, incremental inline token counts,
immediate-next-line pretty totals and trace totals retain structured snapshot
precedence, including explicit zero. Embedded tool-result JSON is excluded from
both structured and legacy agent metrics. Pi message, turn and agent-end snapshots replace each message's usage
instead of counting it repeatedly; compaction and explicit usage entries retain
their own identities. Gemini sums per-model stats across responses. Across raw files,
tokens sum and turns take the maximum; Actions runner logs, detection logs and
baseline directories cannot contribute agent usage. A raw total never
fabricates structured input/output tokens or AIC. Structured proxy usage may
backfill a zero raw total. Audit-only fallback tries the five known engine
parsers in pinned registry order, using tokens + 100,000×turns +
1,000×distinct-tools; ties retain the first parser. Recovered audit metrics never
rewrite immutable Run accounting. Truly unavailable inferred turns remain
explicit as `legacy-engine-fallback-unavailable`.

The independent pinned Copilot fixtures establish 805,343 tokens/two turns
from primary events and 3,325 tokens/two turns from debug responses. Supported
default rules now produce `high_token_usage`, `many_iterations`,
`mcp_server_failures`, `tools_not_available` and
`blocked_network_requests`, with failure, iteration, tooling and network
recommendations in upstream order. High token usage is a performance finding,
not the cost-category trigger for a cost recommendation. Exact-attempt job
metadata supplies immutable `error_count` as the number of failed, timed-out or
cancelled jobs, with upstream zero omission; annotation volume and arbitrary
agent error text are not that counter or a source of validation issues.
Exact-attempt workflow log ZIPs provide step boundaries: qualifying Actions
errors produce one validation issue per step file, not per annotation. Agent
tool-result annotations are excluded. Failure-only fallback uses admitted agent
stdio, then the last nonempty workflow step. More than five issues produces
`multiple_errors`. Only stable validation classes and hashed step locations
survive; arbitrary excerpts do not.

Raw Squid access logs take precedence over usage-summary firewall counts.
Codex `--allow-domains` warnings contribute one blocked request per distinct
domain, additively to raw access counts. Internal Squid placeholder entries are
excluded; private destinations are not silently filtered. Domain-only counts
and findings survive, never URLs, user agents or raw lines. Admitted
`redacted-urls.log` inputs yield unique-domain counts and the info-level
“Sensitive destinations were redacted” insight; the redacted names never
survive. Ambiguous source selections remain gaps.

Versioned unified subagent lifecycle events supply grouped `subagent_failed`
findings and failure-recovery recommendations. A complete, deduplicated
`subagent.model_unavailable` descriptor supplies a medium tooling warning,
not a failed invocation or evidence of model traffic. A successful parent
with observed child failures gets the parent-success narrative without
discarding the child failures. Declared-model reconciliation uses the complete
pinned builtin alias map, bounded custom aliases, provider and dated-model
normalization, patterns and parent-model inheritance. Latest source/session
scope, SDK accounting snapshot replacement, grouped effective models and Pi
dispatch/event/result or legacy tool completion establish invocation evidence.
Zero-usage failed Pi responses do not establish served-model traffic.
`subagent_model_not_observed` distinguishes successful/incomplete invocations
without matching served models from failed invocations and missing global usage.
Native trace identity can bridge an unidentified unified invocation to SDK
metrics only through unique matching tool-call starts, compatible names,
timestamps, native indices and persisted paths; collisions never establish a link.

Raw `gateway.jsonl` owns MCP summaries and server counters over usage-summary
fallback, preferring the artifact root over `mcp-logs/`. Only absent gateway logs
permit `rpc-messages.jsonl` fallback, preferring `mcp-logs/` there. RPC legacy
`type` and production `rpc-message/v2` events share server-scoped request IDs.
Sequential aggregate timing/error pairing remains distinct from the pinned
two-pass individual-call pairing. Filter and guard-policy events are not tool
calls. Safe byte/timing counters survive; payloads and error messages do not.
Matching usage tools may backfill missing numeric sizes/timings, never add stale
tools or replace raw call/error counts. Summary calls backfill only an empty raw
call list. Tool cardinality, assessments and Drain3 consume the same merged map.

The six pinned `agentic_*` assessments and their recommendations consume
admitted immutable turns, tool cardinality, safe outputs, workflow domain,
friction and delegated context. Execution, actuation, capability, network and
privacy observability insights consume their corresponding admitted inputs.
Unknown tool metrics remain an explicit gap, not fabricated zero tools.
Default per-run Drain3 template, anomaly-count and stage-sequence insights use
the pinned embedded plan/finish baselines, masks, insertion order and thresholds.
An independent upstream Go oracle verifies the one/two/three-anomaly fixtures.
Training, custom weights and cross-run baseline comparisons remain outside
this default contract. Tool/command names and report text used for clustering
are transient, nonserializable inputs and are removed before evidence admission.

Detection findings consume normalized job conclusions, same-attempt execution
markers, collector-versioned unified detection results and legacy verdict/log
fallback. Unified results own verdicts, while absent outcome fields may inherit
the usage result. Required verdict flags must all be booleans; missing verdicts
never establish safety. Failed, cancelled, warning and not-started detection
outcomes produce `threat_detection_job_failed`; categorized verdicts or the
allowlisted uncategorized threat reason produce `threat_detected`.
Contradictory or invalid streams produce coverage gaps instead of partial
verdicts. Acquisition admits these members only with same-archive identity.
Raw events, debug blocks, prompts, MCP error messages and detection reasons
remain ephemeral; PostgreSQL publication retains safe findings and honest
partial coverage, not raw logs or completeness inferred from a subset of findings.

The following default audit coverage inventory is pinned to `github/gh-aw@v1.0.1`;
it excludes optional cross-run/baseline comparisons and custom graders:

| Upstream owner and required inputs | Covered semantics and exact remaining limitation |
|---|---|
| `logs_metrics.go:extractLogMetrics`, `audit_report.go:buildAuditMetrics`: primary `events.jsonl`, engine-specific agent logs, inferred fallback metrics and their precedence | Copilot events/session/debug and wire stubs, Claude snapshots/mixed-text fallback, Codex native/legacy metrics and embedded-result exclusion, Pi snapshots and Gemini stats supply accounting and audit-only inference. Reported costs retain engine-specific snapshot/first-positive/additive semantics; no price is invented. An independent executable built from unmodified pinned Copilot, Claude and Codex parsers verifies tokens, turns, costs and exact tool counts. An encountered nonempty engine declaration outside these five builtin IDs fails with `ErrUnsupported`, even without raw logs or with otherwise parseable primary events; it is not guessed or downgraded to a coverage gap. Missing engine identity remains a source-specific gap when raw log metrics require it. |
| `audit_report.go:extractAuditErrors`, `extractPreAgentStepErrors`, and `audit_report_analysis.go` | Exact-attempt job counters, workflow step ZIP acquisition/grouping, failure-only fallback, tool-result annotation exclusion and `multiple_errors` now exist. Success and delegated-failure recovery narratives are supported. Free-text descriptions/excerpts are deliberately discarded because they are not canonical audit fields; their absence is not an excuse to omit canonical code/title/severity. |
| `audit_threat_detection.go` and `audit_threat_detection_session.go`: detection job conclusion, execution-start evidence, unified `detection.result`, legacy verdict/result fallback | Native now produces `threat_detection_job_failed` and `threat_detected` from these inputs; the obsolete blanket detection gap is removed. Unidentified legacy archives are rejected, not bound using metadata from another archive. Invalid/ambiguous streams remain explicit gaps; native rejects malformed tails instead of upstream's best-effort line skipping. Detection-model usage remains separate from agent usage. |
| `audit_report_analysis.go:generateToolingFindings`, `generateFirewallFindings`; `logs_metrics.go`, raw MCP/gateway, access and firewall adapters | Startup MCP failures, structured missing-tool reports, raw access precedence, additive agent firewall warnings and summary fallback produce the pinned tooling/network findings and recommendations. Gateway and legacy/v2 RPC adapters now preserve safe tool/server counts, sizes and timing with the raw/summary precedence above. Error-only zero-call tools still count as distinct types. Missing/invalid raw acquisition remains an explicit gap, not evidence that summary counters evaluate every source. |
| `token_usage_declared_subagents.go`, `token_usage_subagent*.go`, `audit_subagent_model.go`: declared models, session invocation lineage and per-model requests | Lifecycle failures, model-unavailable warnings, recovery, alias/inheritance/pattern-aware served-model reconciliation, `subagent_model_not_observed`, Pi attribution, latest-session selection, SDK snapshots and strict native identity bridging are supported. All 61 builtin aliases are independently compared with the pinned map. Invocation proof never follows a name-only or ambiguous identity guess. Raw model attribution does not invent structured token/credit accounting. |
| `audit_analysis_fanout.go`, `audit_expanded.go`: usage-summary friction/ledger/integrity/steering, raw work-queue evidence, prompt/session and safe-output chain reports | Working-set and selected structured counters plus count-only redaction analysis are supported. `legacy-friction-unavailable`, `legacy-ledger-unavailable`, `legacy-integrity-unavailable` and `legacy-steering-unavailable` identify observed unsupported sections. Expanded queue, firewall-policy, prompt/session and temporary-target chain report bodies are not reconstructed and are not the smaller default canonical finding/insight projection. Unsupported intelligence declarations remain rejected; these sources must never be treated as evaluated, empty or complete. |
| `audit_agentic_analysis.go`, `observability_insights.go`, `drain3_integration.go`, `audit_report.go:buildAuditNarrative` | All six assessment rules, execution/actuation/capability/network/privacy insights, default per-run Drain3 insights and corresponding recommendations consume the admitted engine, merged-tool, network and model-attribution inputs above. Focused fixtures cover the formerly discrepant gateway tools, legacy counters and declared models; unknown inputs remain gaps rather than zero-filled substitutions. |
| `audit_report_analysis.go:generateRecommendations` and the Activity audit normalizer | Startup-model, failure/critical, iteration, missing-tool/MCP, raw/summary network, subagent recovery, success and assessment recommendations are supported in pinned order. High token usage is not the cost-category trigger. Immutable high/medium counts include findings, recommendations and insights together. Any acquisition gap conservatively keeps both totals null, even when supported high findings exist. A gap-free supported audit can expose its complete totals, including measured zero. |

**Default audit capability contract:** successful supported native audit execution
supersedes the unconditional `legacy-audit-unavailable` marker; new evidence MUST
NOT carry that retired blanket gap. Existing saved evidence carrying it remains
conservatively projected with null high/medium totals. Missing, invalid,
ambiguous or unavailable sources retain their narrower coverage rows. Removing
the blanket marker MUST NOT remove those rows or make their totals complete.

The zero-valued `Collector.Audit` request selects only this pinned default per-run
canonical finding/recommendation/insight projection. `AuditRequest.Validate`
rejects an explicit expanded-report or training request, a nonempty external
baseline selector, or any non-nil custom-weight declaration (including an empty
map) with `ErrUnsupported` before acquisition. These options are capability
checks, not a new public configuration surface or authority to acquire another
run. Builtin embedded Drain3 baselines and startup model baseline estimates are
part of default analysis, not external-baseline requests.

An admitted exact-attempt `aw_info.json` declaring an engine other than
`copilot`, `claude`, `codex`, `pi` or `gemini` explicitly rejects acquisition.
Malformed non-string engine declarations also reject. Ordinary missing-tool,
missing-data and noop reports remain supported. An encountered otherwise
unrecognized `agent_output.json` item type in the reserved `report_*`,
`*_report` or exact `intelligence` namespace rejects with `ErrUnsupported`,
including after a valid report prefix; this error MUST propagate rather than
become `invalid-agent-reports` or publish that prefix. Ordinary non-report safe
outputs are not additional audit requests. Campaign intelligence declarations
remain rejected by native inventory storage.

Merely observing optional friction/ledger/integrity/steering source sections
does not request an expanded report: default analysis still executes, retaining
the corresponding explicit section gaps. Historical comparison directories
are excluded from the default audited-run input selection, not interpreted as
an implicit baseline request. Expanded report bodies, baseline comparisons,
training and custom weights MUST NOT be silently accepted or advertised as
evaluated by default acquisition.

Public native activation selects only this supported default capability
contract. It MUST NOT be represented as expanded-profile equivalence, nor waive
unsupported intelligence rejection, source ownership, inventory readiness,
approved trace admission or selected-feature dependency checks.

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
- In the optional collector profile, eligible issue, pull-request and completed
  run deliveries and default-branch pushes MAY also carry a durable remote
  Operational Value invalidation for the exact enrolled repository. A push
  without a workflow-lock change MUST NOT download run logs or request an
  inventory projection solely for Value. Value-only push debounce MUST remain
  independent of ordinary run and inventory work. An invalidation is not
  evidence of an accepted outcome and MUST NOT bypass reviewed Value
  registration, frozen cadence or installation-quota admission.
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
- A worker MUST bound each acquisition with a timeout and byte caps, use bounded
  in-memory processing and the durable handoff in section 2.4, and MUST NOT
  stage a filesystem evidence directory.
- Native provider requests MUST reserve and reconcile the existing shared
  installation quota directly, failing closed rather than exhausting it.
- On failure a worker MUST retry with bounded attempts and jittered backoff, and
  MUST dead-letter a task that exhausts them. A failed task MUST NOT corrupt or
  partially replace a repository's existing evidence.
- A task whose retry time has not arrived MUST NOT be immediately reinserted and
  reclaimed in a hot loop. Workers MUST honor its not-before time or use an
  equivalent delayed-work mechanism; repository-lock contention MUST also be
  retried without consuming the collection attempt budget.

## 6. Durable evidence

Collected safe evidence MUST be durably replayable. Native acquisition persists
exact-attempt publication intents in PostgreSQL as specified in section 2.4,
not in an Activity directory, local overlay, or approved-content trace store.
Native activation MUST enforce cold start, retention and scope erasure through
that shared durable representation, and reject backends unable to satisfy
those contracts. The Actions snapshot layout remains specified separately in
`specs/activity.md`; it is not an App-backed collector fallback.

## 7. Projection

Projection converts retained evidence into the current canonical PostgreSQL
dataset. Native publication remains subject to section 2.4's capability and
ownership contract.

- Projection MUST preserve the Actions profile's canonical mapping. The native
  Go adapter MUST satisfy the independent-oracle and production-query
  equivalence requirements in sections 2.2 and 2.4; it MUST NOT introduce
  alternate canonical semantics.
- Native publication MUST process bounded pending batches under its exact
  digest and epoch fences. Snapshot replacement and native publication MUST
  NOT be interleaved as compatibility paths.
- The snapshot Projector MUST only replay already-compacted, manifest-validated
  evidence through snapshot ingestion. It MUST NOT launch Node.js, discover
  inventory, or regenerate Activity payloads. An invalidated or uncompacted
  snapshot MUST fail closed until its producer repairs it; configuring the
  separately selected native Value catalog MUST NOT enable legacy collection.
- A replacement MUST commit all current sources, source documents, diagnostics,
  revision state, and evaluation time in one PostgreSQL transaction. A failed
  projection MUST leave the previously committed dataset serving.
- Relationship errors and duplicate record identities MUST fail the projection
  rather than commit a dataset known to be inconsistent.
- On successful activation the implementation MUST increment the revision and
  notify connected clients through the existing revision channel.
- Native publication MUST NOT reactivate a consumed digest in the same
  enrollment epoch. An explicit operator rebuild MUST NOT bypass current
  scope, retention, identity or publication fences.
- PostgreSQL MUST be the only dashboard entity and query store. Operational
  adapters MAY hold projection coordination and health state, but MUST NOT hold entity rows,
  canonical source documents, query indexes, persistent query projections, or
  execute dashboard data queries.
- Collector-enabled hosted Operational Value reconstruction MUST query canonical
  PostgreSQL through the production Dashboard Language SQL executor in one
  repeatable-read transaction. Every physical source MUST be restricted to the
  exact enrolled repository set before filtering, joining, or aggregation;
  sources that cannot enforce that scope MUST fail closed. The hosted path MUST
  NOT create, copy, open, rebuild, or ingest SQLite as an intermediate store.
- Hosted Operational Value publication MUST compare the starting canonical
  revision under the namespace writer lock and reject a stale revision. It MUST
  also reject a changed completed-projection generation, reviewed policy,
  local-eligibility request, or exact enrollment set. A successful publication
  atomically replaces canonical `operationalValues`, advances the ordinary
  canonical revision without changing the Activity data revision, and notifies
  existing revision consumers. Actions/static reconstruction remains an
  independent SQLite path.
- Inventory discovery MUST NOT silently truncate the enrolled repository set. An
  implementation MAY enforce a configured bound, but exceeding it MUST fail the
  projection rather than publish a partial inventory.
- Native activation MUST select reviewed inventory bytes or an exact
  previously provisioned digest. Runtime inventory discovery MUST reject
  unavailable policy resolution, enforce the exact operational enrollment
  set, and validate control-repository visibility with App credentials.
- The first canonical activation MUST fail when it cannot produce valid inventory evidence
  with at least one campaign. After one valid inventory has been retained, a
  transient policy, GitHub, or enrichment failure MAY reuse that previous
  inventory while projecting newer run evidence; an empty or malformed
  inventory seed MUST NOT qualify as a previous valid inventory.
- Reusing previous inventory after a refresh failure MUST retain a pending
  refresh request for the next reconciliation interval.

## 8. Cold start

Cold start MUST be resumable and MUST report progress through the existing
administrative rebuild surface.

An implementation MUST support cold start by replaying durable PostgreSQL
publication intents before acquiring missing evidence, reconciling App
installations and their repositories under enrollment ownership, and seeding
receipt-fenced historical discovery and exact-attempt tasks within a frozen
window. Interrupted discovery MUST resume from its durable cursor without
losing accepted attempts or changing its window.

During cold start the implementation MUST continue to accept webhooks and MUST
apply them without loss or double application. Window completeness and
backfilled repository count MUST be published as source metadata.

First activation MUST use reviewed inventory evidence; replicas MAY restore
the exact previously provisioned digest without local inventory files.

An explicitly configured historical backfill window MUST be sent as an
inclusive UTC `created=START..END` range to GitHub's run-list API; a provider
that cannot apply that range MUST fail closed. An unset window preserves
unbounded discovery. Windowed admission MUST NOT truncate retained publication
replay.
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
  replaying publication intents or enumerating historical workflow runs:
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
  shortly before expiry, and never logged or persisted to canonical evidence.
- Live inventory enrichment MUST use a short-lived token for the control
  repository installation and MUST use the same shared quota reserve as
  other native provider requests.
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
- Prompts, tokens, and secrets MUST NOT be written to canonical evidence,
  operational publication intents, or telemetry.
- Missing credentials, missing installation access, an exhausted rate limit, or a
  failed acquisition MUST fail closed. An implementation MUST NOT infer broader
  authority or present partial evidence as complete.
- A misconfigured collector MUST fail startup. It MUST NOT degrade to the
  Actions profile or a partial collector.
- Before collection begins, the collector MUST enumerate the complete App
  installation scope and validate repository visibility: when the control
  repository is public, it MUST fail startup if the credential's scope
  includes any non-public repository, rather than collect from it. A
  visibility failure MUST report only the count of rejected repositories and
  MUST NOT include non-public repository names or other identifying details.

## 11b. Retention and erasure

Native publication intents and accepted canonical evidence are retained
evidence rather than a disposable local cache. Retention and erasure are
governed by source ownership and enrollment epochs in section 2.4.

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
  evicted from enrollment and canonical evidence, including retained repository
  and workflow inventory. Canonical publication MUST remove their evidence;
  lifecycle metadata MUST NOT recreate absent canonical repository rows.
  Unarchive and rename discovery MUST enroll and collect the current verified
  coordinate. Repository deletion or archiving changes collection eligibility,
  not rollout authority. Consent
  withdrawal (`installation.deleted`, `installation.suspend`, or
  `installation_repositories.removed`) still takes precedence and MUST erase
  evidence immediately under the rules below.
- Leaving ingestion scope MUST erase retained evidence. When an installation is
  deleted or suspended, or repositories are removed from it, the enrollment
  ownership transaction MUST invalidate their pending publications, claims and
  canonical graph under the same source epoch. It MUST preserve unrelated
  repositories; a later delivery for the displaced installation MUST NOT erase
  a repository already transferred to another installation.
- Collector-owned operational-value observations MUST be filtered by exact
  repository identity and current enrollment before publication. Credential or
  adapter failures MUST NOT bypass that filtering or restore withdrawn
  observations through retained-value reconciliation.
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
- Erasure MUST be driven by the same authenticated enrollment reconciliation as
  scope admission, without waiting for a filesystem worker or operator action.
  Stale tasks MUST fail their source-epoch and ownership checks rather than
  publish withdrawn evidence. Failure to commit an atomic withdrawal MUST fail
  the reconciliation; an accepted delivery alone is not proof of erasure.
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
  rate-limit, durable publication, and tooling state.
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
production webhook handler with durable deployment-scoped PostgreSQL
operational storage, then verify the health API reports persisted webhook
counters and queued recovery work. A Redis-only admission test cannot establish
native publication or source-epoch ownership.

## 14. Conformance checklist

A conforming implementation:

1. selects exactly one profile and fails startup when both are configured;
2. leaves the Actions profile's behavior unchanged when the collector is absent;
3. derives scope from App installations, never from `cao.json`;
4. admits and enqueues webhooks without projecting in the request path;
5. acquires bounded, exact-attempt GitHub evidence with the native Go adapter,
   without spawning Activity or gh-aw processes;
6. persists receipt-fenced safe publication intents sufficient for recovery;
7. activates canonical evidence atomically under inventory and enrollment
   ownership fences;
8. recovers activity gaps by webhook delivery replay and repairs repository lists
   through periodic and operator-triggered authenticated discovery;
9. governs GitHub budget per installation and fails closed;
10. passes a profile equivalence test against an Actions-published directory;
11. erases retained evidence for repositories that leave ingestion scope, and
    bounds its queues;
12. admits deliveries without collection credentials, and fails closed when an
    admission-only process is asked to collect or project;
13. rejects duplicate activation in the same epoch, transactionally updates
    canonical PostgreSQL, and refuses to publish a truncated inventory.
