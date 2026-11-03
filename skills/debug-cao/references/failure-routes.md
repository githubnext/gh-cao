# CAO failure routes

Inspect sources at the failing workflow commit, not `main`.

| Symptom | Inspect first |
| --- | --- |
| Install/update or missing runtime file | package or campaign record, `resolvedCommit`, materialized files |
| Policy rejection, wrong target/mode, disabled campaign | `.github/workflows/cao.json`, control summary, dispatch inputs |
| Missing or under-scoped credentials | selected profile, secret/variable presence, App installation and permissions |
| Agent/model 403 or billing failure | organization billing, `copilot-requests: write`, token mapping, gh-aw version |
| Syntax, compile, lock, tool, network, safe output | editable workflow source, generated lock, compiler diagnostics |
| Wrong selection or dispatch | orchestrator source, resolved policy, inventory, rollout summary |
| Worker target or output error | envelope, worker source, effective mode, destination, write scope |
| `CAO_ACTIVITY_*` or stale data | Activity workflow logs, cache/artifact, `activity/` |
| `CAO_DASHBOARD_*`, Pages, or site build | dashboard workflow, Activity dependency, build/deploy job |
| Rate limit or inaccessible evidence | admission summary, credential identity, reset time, target visibility |
| Runner/action/Node/npm failure | failed step, runner image, pinned action, lockfile, runtime |

Source map:

- rollout authority: `.github/workflows/cao.json`;
- shared admission/authentication: `.github/workflows/shared/control.md`,
  `control.mjs`, and `policy.mjs`;
- workflow source: `.github/workflows/<workflow>.md`;
- generated workflow: `.github/workflows/<workflow>.lock.yml`;
- installed identity: `.github/aw/packages/*.json` and
  `.github/aw/campaigns/*.json`;
- Activity: `.github/workflows/cao-activity.yml` and `activity/`;
- dashboard: `.github/workflows/cao-dashboard.yml` and `dashboard/`;
- architecture: `ARCHITECTURE.md` and `specs/`.

For browser-only failures, use the narrowest `?debug=` category from
`dashboard/site/src/debug.js`. For Go/Redis server failures, use the narrowest
`DEBUG` namespace described in `server/README.md`. Logging stays off by default;
never log credentials, query payloads, prompts, or source records.

When the checkout matches the failing revision, useful read-only diagnostics are
`gh aw --version`, `gh aw doctor --repo OWNER/CONTROL-REPOSITORY --dir .`, and
`npm run compile`.
