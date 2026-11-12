---
title: Data ingestion
description: Understand how Central Agentic Ops collects, normalizes, retains, and projects dashboard data.
---

Data ingestion moves operational evidence from GitHub Actions into dashboard
backends and local tools. Read this page to understand collection boundaries,
retention, failure behavior, and the available `cao` commands. For entity
identities and relationships, use the [Data model](dashboard-data-model.md).
For where the dashboard is served, see [Deployment options](deployment.md).

## Data flow

Agentic workflows produce Actions logs. The Activity workflow collects a bounded
snapshot into read-only operational artifacts. Consumers apply the shared model rules to build
separate projections: local and hosted Go deployments ingest into PostgreSQL;
native command-line tools use its canonical read boundary. Pages uses only
generated synthetic enterprise samples, passed through the same normalization,
canonical ingestion, queries, and views with browser IndexedDB. These are
independent profiles, not a fallback chain.

<div class="docs-theme-diagram">
  <img class="docs-theme-diagram-light" alt="Real Activity artifacts feed local and hosted Go/PostgreSQL. Separately generated synthetic samples use the same canonical pipeline and views through Pages and IndexedDB." src="/gh-cao/assets/dashboard-data-flow-light.svg">
  <img class="docs-theme-diagram-dark" alt="Real Activity artifacts feed local and hosted Go/PostgreSQL. Separately generated synthetic samples use the same canonical pipeline and views through Pages and IndexedDB." src="/gh-cao/assets/dashboard-data-flow-dark.svg">
</div>

PostgreSQL and IndexedDB are independently rebuildable projections, not sources
for one another. PostgreSQL uses the production typed relational schema; it does
not emulate IndexedDB stores or retain duplicate serialized row mirrors. Default
canonical retention keeps Runs for 30 days, linked detail for seven days and
Operational Value for at least 365 days. Historical analysis requires a separately
configured PostgreSQL namespace and preserved authoritative evidence.

See [Data model](/gh-cao/dashboard-data-model/) for the canonical entities, identities, and relationships produced by ingestion.

## Before the first Activity snapshot

Provisioning creates database structure, not operational evidence. Runtime opens
existing PostgreSQL storage without DDL, automatic file conversion or synthetic
bootstrap data. Readiness remains incomplete until an explicitly verified artifact
or authorized collector publishes canonical evidence.

Use a selected authenticated Actions artifact, a trusted direct manifest URL, or
the explicitly scoped native collection profile. Structural-only observations may
be available while Run and artifact coverage remain unknown or partial; absence
must not become healthy zero activity. The synthetic Pages demo is never an
operational source, repair path or hosted fallback.

<span id="canvas-administrative-actions"></span>

Canvas command execution is separate from ingestion. Ordinary Go local serving
and synthetic Pages previews cannot execute local commands. See
[Run approved commands](agent-plugin.md#run-approved-commands) and
[Compiler-version evidence](agent-plugin.md#compiler-version-evidence) for the
approval and evidence boundaries.

## Hosted server data pipeline

Server-backed deployments keep the browser isolated from both databases. The
Go server verifies the operational artifact payload, normalizes inventory metadata
onto canonical records, and transactionally replaces the current dashboard
sources in PostgreSQL. Dashboard Language queries execute as bounded,
parameterized SQL in a repeatable-read transaction. Unsupported query shapes
fail explicitly; the hosted path has no Go row-evaluator fallback.

Operational storage is a separate contract for sessions, queues, rate limits,
deduplication, coordination, revocation retries, audit records, and caches.
PostgreSQL is the default; Redis is an explicitly selected alternative for
supported capabilities, not a dashboard entity database. Protected operational
state is not disposable evidence-cache data. If canonical PostgreSQL storage is
unavailable, dashboard data remains unavailable even when Redis is healthy.
See the [server operational-storage contract](https://github.com/githubnext/gh-cao/blob/main/specs/server-operational-storage.md).

## Completeness and duplicates

The scheduled Activity workflow is a rolling operational snapshot, not a full historical archive. Data can be incomplete at these boundaries:

| Boundary | What can be missing |
| --- | --- |
| Collection | Runs outside the configured 30-day window. |
| Enrichment | The scheduled command downloads at most five matching usage artifacts across all workflow targets per Activity invocation. |
| GitHub retention | Expired or unavailable artifacts cannot provide agent, usage, job, or audit detail. The run summary may still exist. |
| Mapping | GitHub API rate-limit records without collection context are intentionally not attached to a run. |
| Browser storage | IndexedDB keeps all published run summaries and expires run-linked detail after seven days. |

Cached JSONL can repeat the same run in later snapshots. These are repeated observations, not duplicate database records. Raw runs are deduplicated by GitHub run ID and attempt. Enriched runs are deduplicated by run ID and attempt, with the newest observation winning.

### Collection and ingestion validation

The retired Activity Actions job graph and its simulator fixture are removed.
`npm run test:integration:actions-runtime` validates the remaining staged workflow
samples. Canonical ingestion and daily-count behavior retain their independent
worker, native browser, native CLI and PostgreSQL conformance suites.

## Create a historical archive

Preserve authoritative JSONL evidence separately from disposable dashboard state.
For historical local analysis, provision a dedicated PostgreSQL namespace with
explicit retention and ingest verified canonical artifacts into that namespace.
A rolling refresh must not silently overwrite another namespace or shorten its
history. Browser IndexedDB remains a bounded demo projection, not an archive.

## Estimate empirical workflow behavior

The offline estimator fits workflow-conditional descriptive weights from a
privately acquired operational cached JSONL file:

```bash
node scripts/fit-aw-behavior.mjs \
  --input /private/operational/gh-aw-history.jsonl \
  --repo OWNER/REPO \
  --since 2026-10-12T00:00:00Z \
  --until 2026-11-12T00:00:00Z \
  --output /private/operational/behavior-model.json
```

Acquire evidence through authenticated Actions artifacts or bounded `gh aw logs`
collection, never the synthetic Pages demo. The estimator uses the production
gh-aw adapter, canonical normalizer and Dashboard Language query engine. It
selects the exact execution repository and half-open UTC creation-time window,
converges repeated run observations, and writes a new owner-readable aggregate
with the input digest. Keep raw evidence and operational models outside committed
or public demo assets.

The versioned JSON contains outcome weights, numeric quantiles and histograms,
exact safe-item count weights, tool/skill observation lower bounds and coarse
joint cells for resampling within a workflow. Every fitted metric carries its
observed denominator; fewer than ten observations are marked small-sample.
Zero-creation estimates require count-reconciled manifests, not absent detail.
Safe items include dispatches and comments, not just created issues and PRs.
Quiet-day probabilities, complete-cost claims and acceptance/closure delays
remain unavailable without the required collection and lifecycle evidence.
Newest-run selection, retention and missing artifacts limit generalization;
these weights describe the selected observations, not enterprise population
rates or causal effects. No workflow policy or synthetic generator is changed.

## Browser data pipeline

This is the synthetic Pages demo and adapter-conformance path, not operational
hosting. It uses the same canonical normalization and query contracts as local
and hosted PostgreSQL, with no live services or operational-data input.

The unified-session adapter retains only metadata needed for model-routing
analytics: safe identifiers, explicit classifier labels, request ownership,
per-response token/AIC observations and source receipts. It accepts headerless
legacy sessions and records their format coverage. Unsupported session versions
retain the header only. Source-receipt phases take precedence over the collection
event's conclusion-phase provenance. Prompts, bodies, free-form reasons/errors
and content-derived hashes are not projected into routing facts.

Runner-written `model.resolution` events become one Audit record per recorded
role with the event engine, role, configured model, resolved model, provider,
status (`resolved`, `exact` or `failed`) and cause (`model_unavailable` or
`catalog_unavailable`). Configured and resolved models keep their recorded value,
including provider prefixes; unsafe or missing values stay unknown, and a failed
resolution keeps its resolved model unknown. The events have no timestamp, so
failed resolutions appear in the Model routing Problems view once per run, with
an unknown time unless the session clock is recorded, and per-run resolution roles
stay on the run page. Routed sessions are expected
to lack these events, and legacy sessions report no resolution evidence rather
than failure. Canonical schema 36 retains Campaign and Workflow
source-maintenance provenance. IndexedDB version 49 adds the disposable
workflow-subject cursor index and rebuilds older browser
projections so every retained Run receives the disposable statistical range
keys and indexes. Hosted PostgreSQL
needs a fresh generated schema with the new
Audit columns and compact Run routing aggregates. Ingestion derives the aggregate
with shared Dashboard Language accounting, retains request evidence needed by
run-page views, and drops unused inventory and generic proxy events.

The activity shard manifest describes operational artifact input. The Pages
demo generates equivalent synthetic input through the production normalizer;
it never downloads that operational manifest. The canonical ingestion expression
maps raw `workflow_runs` rows to Repository, Workflow, and Run observations.
Enriched `run` envelopes update the same Run identities and create deterministic
Domain, Tool, Audit, and Issue records owned by those Runs.
`github_api_rate_limit` envelopes create Audits only when explicit collection
context identifies their owning run; browser ingestion does not fabricate
ownership. Unknown kinds and unsupported non-empty schema versions fail explicitly.

The complete normative [cached gh-aw JSONL mapping](https://github.com/githubnext/gh-cao/blob/main/specs/dashboard-gh-aw-jsonl-mapping.md) describes source fields, canonical entities, identity, ownership, and accounting.

The canonical model is version 36. The browser database is
logically `gh-aw-cao-dashboard-data`, IndexedDB version 49. Pages storage keys
carry the `synthetic-demo` suffix and deployment scope; service-worker caches
use `demo`. Demo payloads live under `demo-data/`, not the old root
manifest URL, preventing old same-origin cached real data from being reused.
Its canonical stores are
`campaigns`, `repositories`, `workflows`, `runs`, `domains`, `tools`, `skills`,
`friction`, `audits`, `issues`, `operationalValues`, `operationalValueEvidence`, `workflowValueCollections`, `marketplacePackages`,
`experiments`, `experimentAssignments`, `graders`, `graderObservations`,
`evals`, and `evalObservations`;
all use `id` as the key.
The `transactions` store records ingestion outcomes and is indexed by
`createdAt`; `storageShards` holds disposable storage accounting. The separate
`dailyRunCounts`, `runCountContributions`, and `dailyRunCountState` projection
stores use `id` keys without secondary indexes. They hold UTC
day/Repository/Workflow counters, per-logical-Run reconciliation receipts, and
coverage/readiness, backfill checkpoints, and retention state respectively.
[Daily counts](https://github.com/githubnext/gh-cao/blob/main/specs/dashboard-data.md#722-additive-daily-run-counts)
are the only persisted daily projection; other Overview metrics remain
request-scoped queries.

Upgrading version 44 or 45 with a `runs` store creates only missing stores and
preserves existing canonical records and projection state. Newly created daily
state starts `backfilling` with `retained-v44` or `retained-v45` coverage.
Backfill reconciles at most 250 retained Runs per transaction and commits its
checkpoint with the contributions, so interruption can resume without double
counting. Coverage remains partial because already-evicted history cannot be
reconstructed. Other older databases rebuild all stores from authoritative inputs.
New databases initialize daily state as `ready` with `admitted` coverage.

Daily counts and compact receipts retain the current UTC Monday-start week and
its two preceding weeks, independently of detail cleanup. Reconciliation
atomically replaces a Run's contribution; detail eviction does not subtract
counts. A monotonic retention watermark prevents expired weeks from reopening.
Unavailable projection state must not be treated as zero counts or fall back
to retained detail.

Experiment assignments and eval observations come from the unified session when it contains them. Otherwise, ingestion accepts gh-aw's explicit current-run `assignments.json` artifact (including the compact `usage/experiment/` layout and prefixed artifact directories) and a supplied `evals.jsonl`. Collection requests the `usage,experiment,graders` artifact sets without widening repository scope or collection budgets. Before retaining these artifacts, collection verifies repository, run ID, and attempt against `aw_info.json`; a mismatch skips that evidence. Unified assignments, including an explicitly empty map, take precedence, and cumulative experiment state counts never infer a run's variant. The Experiments page shows retained assignment variants with run evidence links, and the Evals ledger shows `YES`, `NO`, and `UNKNOWN` answers distinctly; `UNKNOWN` is not a failed eval.

gh-aw v0.92.6 publishes built-in graders in the unified session and `usage/graders/`, with custom grader results in a separate `graders` artifact. Ingestion accepts version-1 and version-2 result documents, uses the adjacent `grader_manifest.json` for definition metadata, and retains session results in preference to duplicate built-ins. The separate artifact may add custom results not already present in the session. Native metric order, finite values, explicit `null` values, and evaluator digests survive projection; scripts, messages, error bodies, and raw session payloads do not. Historical repository-owned shell evaluators remain retired and are not executed during collection.

Normalized shards are written in bounded transactions, with a separate receipt
recorded after each complete shard. A stopped import can reuse those receipts on
the next visit, even before the full snapshot marker has been written. Run-phase
views can load after the run shards finish while record shards continue. Retention
and orphan cleanup run after shard ingestion. During maintenance, the worker
expires run-linked detail outside the seven-day window and Operational Values
outside the 30-day window, pruning orphaned descendants and unreferenced
structural parents. The effective retention horizon is the later of the browser
clock and the newest incoming observation, so a slow clock cannot prune
current producer data.

Browser IndexedDB and PostgreSQL ingestion also discard a failed Run's raw
`failureLog` excerpt once a later completed success is known in the same
repository/workflow, target, event, branch, and rollout mode. Cancellation and
incomplete outcomes do not prove recovery. Only the excerpt is removed: Run
summaries, failure signatures/messages, cost and reliability facts, and all
run-linked metric and comparison evidence keep their existing retention.
Re-importing an older shard cannot restore a pruned excerpt for the same attempt.
Unresolved excerpts follow the existing detail window and database size cap.
A separate archive with `--retention-days all` preserves full detail. This does
not prune hosted PostgreSQL, published shards, or GitHub artifacts.
The saving is retained diagnostic bytes and reduced storage pressure, not fewer
GitHub API calls, downloaded artifacts, or workflow executions. Cleanup itself
adds a bounded maintenance scan when excerpts are present; no execution-time
reduction is assumed.

Every merged batch must satisfy these relationships:

- Workflow to Repository
- Run to Repository and Workflow
- Domain, Tool, Audit, and Issue to Run

Work items and findings are projected from Issue and Audit records rather than stored in separate canonical tables. Independent logical sources, such as usage, outcomes, admissions, security, and MCP evidence, retain their published schemas in worker memory and are selected only when a page requests them.

Source download, adaptation, normalization, IndexedDB writes, and page queries
run in a dedicated Web Worker. Successful inputs write content-addressed
receipts containing the ingestion version, payload identity, and retained or
committed record counts. A failure writes a diagnostic receipt when possible.
Bounded writes that committed before a later failure may remain in the
disposable database, but no successful receipt is written and the shard remains
retryable.

Actions publishes normalized run and record shards with a 1 MiB budget,
including metadata and UTF-8 bytes. A single larger record is kept intact in its
own shard rather than truncated. Records are deduplicated before being ordered
by collection and canonical ID within stable day buckets, so changes in source
arrival order alone do not force clients to download and ingest unchanged data.
The 4 MiB raw-source compaction budget is independent of this published budget.

Worker errors abort the update instead of rerunning ingestion through an older path. There is no shadow, dual-read, alias, or fallback route. Views render only after the worker returns that page's query projection.

Before ingestion, the browser inspects its storage estimate and requests persistent storage when the API is available. Either request may be denied or fail without affecting correctness. Diagnostics use stable categories such as `NORMALIZATION_FAILED`, `TRANSACTION_ABORTED`, and `QUOTA_EXCEEDED`.

IndexedDB is disposable derived state. Clearing demo storage reconstructs it
from synthetic inputs; it does not delete authoritative information. The demo
does not adopt or automatically delete a former real-data browser database or
caches outside its namespace. Rebuilding Pages replaces current site artifacts,
not historical deployments, old browser storage, or downloaded copies.

To investigate repeated imports on a mobile browser, open the dashboard with
`?debug=data:ingestion,data:indexeddb,quota`. The worker reports whether the
complete snapshot was found, schema upgrades, storage quota and persistence
availability, cached versus missing run and record shards, shard commits, and
dashboard revisions. These diagnostics report counts and storage metadata, not
record contents. A `missing-snapshot` message alone does not mean previously
committed shards were lost: compare the `shard-cache-checked` counts on the next
visit. Browsers may decline persistent storage or evict this rebuildable cache.

## Validate browser performance

Run `npm --prefix dashboard/site run test:performance` to audit the production
build in two states: an empty canonical database and a completely ingested
canonical snapshot. The runner downloads every selected normalized JSONL shard,
verifies its manifest hash, waits for successful ingestion, and records canonical
store counts before and after the audits. Its empty snapshot uses valid
zero-record shards and empty inventory, not fixture-mode presenter data.

The CFO, CTO, and CSO journeys check that visible views finish loading without
unavailable-data cards, including the inventory table modes. Lighthouse measures
desktop and mobile separately and reports three-run medians. Cold-network audits
clear HTTP cache and bypass the audit page's service worker while retaining
IndexedDB; separate PWA audits use normal service-worker responses. PWA transfer
sizes do not represent uncached network bytes.

Production builds compile the shared styles into a native linked stylesheet,
preload the entry and application modules, and keep route modules deferred.
The worker is bundled independently so its startup does not repeat discovery
of the application's shared chunks. Source-mode previews retain the same
shared styles and rendering contracts.
Production keeps source-declared glyphs in a small inline icon core and loads
the complete sprite only for other supported campaign icons. The complete
sprite is precached for offline use; unknown names retain the question glyph.

To compare revisions against the same downloaded evidence, set
`DASHBOARD_PERFORMANCE_DATA_ROOT` to a directory containing
`payload-hashes.json`, `inventory-sources.json`, `memory/`, and all declared
normalized shards. An operational benchmark requires an explicitly acquired
private snapshot or a direct operational `payload-hashes.json` URL in
`DASHBOARD_DATA_URL`, never an implicit Pages source. Private benchmark data
must not be published with the demo. `DASHBOARD_PERFORMANCE_REPEATS` changes the repetition count,
and `DASHBOARD_PERFORMANCE_NETWORK_MODES=cold` runs only the cold cohort.

Reports, browser traces, desktop/mobile screenshots, ingestion timings, and
persona-journey timings are written under
`dashboard/site/test-results/lighthouse/`. A missing shard, failed query, invalid
state, or breached performance budget fails the command and the CI job.
Complete eager-ingestion time is recorded separately from page readiness: normal
first visits can still explore partial data while import continues.

## SQL interchange

SQL uses the versioned `gh-aw-cao.dashboard-sql-export` interchange contract.
Database owners map their schema to the contract and export JSON for authorized
canonical ingestion, not real-data Pages publication. All profiles use the
same contract, validator, adapter, and canonical queries; the demo never opens
a database connection.

## Use local PostgreSQL

Local development uses the same Go server, canonical PostgreSQL schema, SQL query
planner and revision subscriptions as hosted deployments. There is no file-database
backend, import conversion, writable overlay, or browser fallback.

1. Start an explicitly selected local PostgreSQL instance and configure CAO_POSTGRES_URL.
2. Provision canonical and operational storage separately with schema-owner credentials.
3. Acquire an operational cao-activity-index Actions artifact or a trusted direct
   payload-hashes.json manifest. Acquisition does not select a public Pages source.
4. Ingest its verified normalized JSONL with cao-dashboard ingest --source DIRECTORY.
5. Run npm run dashboard:local against that namespace. Set --redis-namespace to the
   exact provisioned namespace when using a non-default dataset.

See [PostgreSQL provisioning](../server/POSTGRES.md#local-postgresql-development).
Runtime credentials never bootstrap or repair storage. An explicit --source may
publish verified evidence before serving; failures preserve the previous revision.
External publications refresh active views without a stale local result cache.
Native gh cao query, computation and mcp commands use PostgreSQL through the bundled
server reader. Node cao retains artifact preparation and catalog discovery, not a
second local database backend. Removed ingestion and repair commands have no
compatibility path.

The Copilot canvas uses the same Go/PostgreSQL host, with private loopback authoring
callbacks. Neither local storage nor canvas configuration grants collection,
rollout, credential or hosted execution authority.

### Query the private session archive

> [!WARNING]
> **Experimental:** The session archive is a proposed, default-off analytics store. Passing its interface does not mean either deployment scenario has met its acceptance gates.

The Go server can optionally archive unified agent sessions as private, immutable Parquet evidence. It is separate from canonical PostgreSQL data, has no frontend, and never joins automatically with dashboard queries. Archive enablement does not change canonical ingestion, dashboard queries, or IndexedDB behavior.

| Scenario | Storage | Query engine |
| --- | --- | --- |
| Local persistent server | Private Parquet files on the filesystem | Pinned DuckDB CLI |
| Hosted persistent server | Private Blob objects | Approved Azure Data Explorer (ADX) external-table queries, only after destination conformance evidence exists |

Enable the local archive with `CAO_SESSION_ARCHIVE_ENABLED=true`. Also set `CAO_SESSION_ARCHIVE_DURABLE_STORAGE`, `CAO_SESSION_ARCHIVE_DIRECTORY`, `CAO_SESSION_ARCHIVE_NAMESPACE`, `CAO_SESSION_ARCHIVE_DUCKDB`, an exact comma-separated `CAO_SESSION_ARCHIVE_REPOSITORIES` list, and a private (no group or world permissions) `CAO_SESSION_ARCHIVE_POLICY` file. Only full capture is supported. Enabling the archive doesn't grant collection authority; it narrows the repositories that existing collection already permits. Unenrolling a repository withdraws its archived sessions.

Authenticated callers use two endpoints. Reading full traces requires an explicit grant beyond normal dashboard membership.

| Endpoint | Use |
| --- | --- |
| `GET /api/v1/session-archive/status` | Reports whether the archive is available, with bounded counters |
| `POST /api/v1/session-archive/query` | Runs a trusted `timeline` or `summary` template, or a local-only `session-query`, for one approved repository |

`timeline` needs a positive GitHub run ID and attempt; `summary` rejects them. Requests are limited to 4096 bytes, and responses to 8 MiB and 10,000 rows. A `session-query` takes a Dashboard Language `query` over `archive-events` and doesn't accept SQL, joins, or `PayloadJSON`. Exceeding a budget returns an error rather than partial results, and archive queries never fall back to dashboard queries. The hosted ADX adapter doesn't yet run `session-query`.

For the full contract, see the [Private unified-session archive specification](https://github.com/githubnext/gh-cao/blob/main/specs/session-archive.md).

For normative requirements and failure behavior, see the [Dashboard Data Architecture Specification](https://github.com/githubnext/gh-cao/blob/main/specs/dashboard-data.md).
