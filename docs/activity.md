---
title: Evidence Collection
description: Native collection and explicitly prepared operational evidence for CAO.
agent:
  type: data-collection
---

Hosted CAO collects evidence through the native Go server using authenticated
GitHub App APIs, reviewed inventory, shared quota, and exact enrollment epochs.
See [server ingestion](../specs/server-ingestion.md) for the normative
acquisition, publication, erasure, and recovery contracts.

The Activity JavaScript runtime, scheduled publisher, cache, CLI, and Node MCP
image are removed. There is no compatibility command or automatic fallback.
Workflow execution is evidence of execution, not proof of accepted outcomes.

Local analysis uses a provisioned PostgreSQL namespace and verified canonical
JSONL. Configure `CAO_POSTGRES_URL` for `gh cao query` or `gh cao mcp`, or download
from an explicitly selected operational `payload-hashes.json` URL with
`gh cao download --url URL`. No default artifact discovery or Pages download
is provided.

The synthetic Pages demo uses the shared production normalization under
`dashboard/report/`, canonical worker ingestion, and Dashboard Language.
It is not an operational evidence source and publishes no SQLite database.

See [the canonical data model](../specs/dashboard-data.md),
[dashboard data ingestion](dashboard-data-ingestion.md), and
[agent analysis](agent-analysis.md). Missing, stale, or unavailable evidence
must remain explicit; it must not be reported as a complete empty result.
