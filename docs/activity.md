---
title: CAO Activity
description: Learn how CAO Activity collects gh-aw logs for Central Agentic Ops.
agent:
  type: data-collection
  prominent: true
---

CAO Activity is the shared, bounded `gh aw logs` collector for Central Agentic
Ops. It prevents consumers from independently acquiring the same compiled
workflow history. It also materializes the canonical log projection in SQLite
so local tools and agents can query the snapshot without re-ingesting it.
Every [deployment option](deployment.md) serves dashboard data derived from
this collector.

Read this page when changing Activity collection, cache publication, or workflow
registry enrichment. Continue to the
[Activity specification](https://github.com/githubnext/gh-cao/blob/main/specs/activity.md),
then the [dashboard data specification](https://github.com/githubnext/gh-cao/blob/main/specs/dashboard-data.md)
and the implementation under `activity/`.

## How Activity works

```mermaid
sequenceDiagram
  accTitle: Activity cache publication
  accDescr: Activity restores the previous snapshot, collects logs once, ingests SQLite and saves refreshed evidence. Consumers restore that shared snapshot instead of collecting independently.
  participant Activity as CAO Activity
  participant Cache as Actions cache
  participant Consumer as Consumer

  Activity->>Cache: Restore latest cao-activity-v4-* snapshot
  Activity->>Activity: Run gh aw logs once
  Activity->>Activity: Ingest JSONL into SQLite
  Activity->>Cache: Save refreshed JSONL, SQLite, and Drain3 weights
  Consumer->>Cache: Restore compatible snapshot
```

The scheduled and manually dispatchable `.github/workflows/cao-activity.yml`
checks out the trusted control-repository source, restores its cache, collects
compiled workflow evidence with `gh aw logs --audit --artifacts usage`, ingests
the resulting JSONL through the dashboard's Node.js canonical data pipeline,
and stores both data files plus the generated Drain3 weights. Restored weights
are passed to the next `gh aw logs` invocation so log clustering can continue
learning across runs. Downloaded artifacts are job-local inputs and are not
cached. The dependent publication job verifies that every snapshot file
extracted from the artifact exists and is non-empty before saving the cache, so
a path or packaging regression fails immediately instead of leaving consumers
with a cache miss. A collection that observes no agentic workflow runs, such as
a newly bootstrapped control repository, still publishes one header-only run
shard and one header-only record shard; because no logs were clustered, the
Drain3 weights are required only when the run shards contain records.

Activity uses the `central-agentic-ops-activity` concurrency group with
`cancel-in-progress: false`, so a running refresh is never cancelled mid-flight
by the next scheduled trigger; GitHub Actions queues at most one pending
refresh behind it.

Runs collect a rolling 30-day window and recent artifact detail. The Activity
SQLite projection retains Run and Operational Value records for 30 days by
default while expiring run-linked detail after seven days. Historical archives
can override these windows explicitly.

## Cache contract

The cache holds:

```text
$RUNNER_TEMP/cao-activity/gh-aw-logs-shards/
$RUNNER_TEMP/cao-activity/gh-aw-logs.sqlite
$RUNNER_TEMP/cao-activity/gh-aw-logs-runs/
$RUNNER_TEMP/cao-activity/gh-aw-logs-records/
$RUNNER_TEMP/cao-activity/payload-hashes.json
$RUNNER_TEMP/cao-activity/control-settings.json
$RUNNER_TEMP/cao-activity/inventory-sources.json
$RUNNER_TEMP/cao-activity/drain3_weights.json
```

`payload-hashes.json` maps the current JSONL source and SQLite projection
filenames, plus each retained source, run-information, and record shard, to
their SHA-256 checksums. Run and record shards use matching filename stems; an
unpaired phase set is incomplete and consumers fall back to a complete
compatible transport. The dashboard publishes this small file beside the
payloads so clients can detect unchanged data before downloading them.
Dashboard ingestion checks the sidecar first, then falls back to ETag validation
and finally a downloaded-content hash when neither server-side identity is
usable.

Shard compaction automatically repairs mixed-repository shards on a best-effort
basis. It splits records with unambiguous repository identities into the declared
repository groups, associates repository-less dependent records only by an
unambiguous run ID within the original shard, preserves record order, and adds the
verified repository to those records' request metadata. `repairedShards` reports
how many mixed shards were repaired. Contradictory identities, unattributable
records, and undeclared repositories still fail closed without replacing the
original shards, which are removed only after every group's output is written.

Run-information shards are intentionally small and become queryable before
event ingestion completes. This improves time to first useful render, but does
not reduce the bytes needed for a complete refresh. Until the record phase
finishes, record-dependent views remain partial; background refresh is skipped
on metered or data-saver connections.

Its immutable key is
`cao-activity-v4-${github.run_id}-${github.run_attempt}`; its restore prefix is
`cao-activity-v4-`. Consumers dispatched by Activity must restore the exact
completed run's cache key. Every producer and consumer uses the complete path
list because GitHub includes paths in the cache version. The cache is evictable
and is not historical authority: consumers must enforce their own freshness,
completeness, and scope requirements. Agent jobs install the SQLite CLI before
restoring this snapshot, so they can query the normalized database directly.

## Installation

`activity/aw.yml` installs the Activity and maintenance workflows plus the
shared JSONL parser. The installer-managed CAO root package installs
Activity and the dashboard ingestion runtime automatically.
