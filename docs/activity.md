---
title: CAO Activity
description: Canonical operational evidence inputs and explicit snapshot analysis.
agent:
  type: data-collection
  prominent: true
---

CAO Activity names the shared evidence and normalization contracts, not an
installed scheduled workflow. The legacy Activity publisher, dashboard artifact
builder, local Value workflow, component manifests, and shared Actions cache
have been removed. There is no compatibility discovery or bootstrap pipeline.

Production collection runs through the authenticated Go server and its reviewed
repository enrollment. Local development and analysis use an explicitly prepared
operational SQLite snapshot. The public Pages dashboard is a **Synthetic
enterprise demo**, generated independently of operational collection.

## Prepare a snapshot

Use an explicitly selected operational `payload-hashes.json` URL:

```bash
node activity/cao.mjs download --url https://SNAPSHOT_HOST/payload-hashes.json
node activity/cao.mjs query --database .cao/gh-aw-logs.sqlite --collection runs --limit 20
```

Authenticated acquisition of a specifically selected Actions artifact remains
an input mechanism when such an artifact exists; no workflow or latest artifact
is discovered automatically. This does not introduce a hosted export endpoint.
Never use synthetic Pages data as operational evidence.

## Evidence contract

SQLite and normalized JSONL are derived projections of bounded, scoped evidence.
Keep their scope, collection time, completeness, and checksum metadata together.
Missing or partial evidence is not proof of no runs, no cost, or successful work.
Snapshot analysis grants no rollout, collection, credential, or write authority.

The Copilot canvas reads a prepared `.cao/gh-aw-logs.sqlite` and
`.cao/payload-hashes.json` in its workspace. It rejects missing, corrupt, and
synthetic snapshots rather than discovering a legacy dashboard bundle.

Read the [Activity specification](https://github.com/githubnext/gh-cao/blob/main/specs/activity.md),
the [dashboard data specification](https://github.com/githubnext/gh-cao/blob/main/specs/dashboard-data.md),
and [Agent analysis](agent-analysis.md) for supported queries and evidence limits.
