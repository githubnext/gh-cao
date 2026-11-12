# CAO activity evidence runtime

The legacy scheduled Activity publisher, dashboard artifact builder, local Value
workflow, component manifests, and shared Actions cache have been removed.
There is no automatic artifact discovery, empty-snapshot bootstrap, or legacy
dashboard bundle fallback.

The JavaScript runtime still provides canonical normalization, explicit SQLite
snapshot analysis, Dashboard Language queries, and bounded evidence tools.
The hosted Go collector uses the shared gh-aw log and session-evidence adapters.
These are not a replacement Actions publication chain.

## Explicit operational inputs

Prepare a real operational SQLite snapshot and its checksum manifest, or download
an explicitly selected operational manifest:

```bash
node activity/cao.mjs download --url https://SNAPSHOT_HOST/payload-hashes.json
node activity/cao.mjs query --database .cao/gh-aw-logs.sqlite --collection runs --limit 20
```

The URL must name `payload-hashes.json` directly. No public Pages URL or hosted
export endpoint is inferred. Missing scope, freshness, completeness, credentials,
or evidence stays unavailable.

Local Go serving and the Copilot canvas read prepared snapshots. Canvas startup
uses the workspace's `.cao/` directory, verifies the SQLite checksum, and never
discovers Actions artifacts. Production uses the authenticated Go/PostgreSQL host.

## Synthetic demo

`npm run dashboard:pages:build` generates synthetic enterprise raw samples through
production normalization and the shared dashboard views. Pages publishes no
operational SQLite, agent summary, or live service.

See [Activity](../docs/activity.md), the [Activity specification](../specs/activity.md),
and [dashboard data](../specs/dashboard-data.md) for evidence and query contracts.
