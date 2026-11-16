---
name: central-agentic-ops
description: "Route Central Agentic Ops (CAO) work: set up a control plane, add or create campaigns and operations, use gh cao, gh aw, or gh, analyze dashboard queries and activity evidence, or diagnose a deployment failure. Loads shared CAO instructions plus only the matching task guide."
metadata:
  argument-hint: "Describe the CAO task, for example: set up, add a campaign, analyze a dashboard query, or debug a run"
---

# Central Agentic Ops

This skill only routes. Task guides live under `.github/cao/` at the
repository root (control repository, catalog checkout, or plugin root). If a
path is absent, read it from
`https://github.com/githubnext/gh-cao/blob/main/<path>`.

## Progressive disclosure

1. Read the shared instructions: `.github/cao/instructions.md`.
2. Pick exactly one task below and read only its guide. Open a guide's linked
   references only when its procedure asks for them.
3. Load another guide only when the task hands off to it.

| Task | Guide |
| --- | --- |
| Create, bootstrap, or install a bare control plane, scope, and authentication | `.github/cao/tasks/setup.md` |
| Discover, install, author, or change campaigns and operations | `.github/cao/tasks/operations.md` |
| Use or extend `gh cao` commands, the runtime, or dashboard-query maintenance | `.github/cao/tasks/gh-cao.md` |
| Author, compile, or inspect gh-aw workflows | `.github/cao/tasks/gh-aw.md` |
| Inspect GitHub state with `gh` | `.github/cao/tasks/gh.md` |
| Answer questions from dashboard queries and activity evidence | `.github/cao/tasks/dashboard-analysis.md` |
| Diagnose a failed run, setup, or policy decision | `.github/cao/tasks/diagnostics.md` |

## Former skill names

These skills were consolidated here; route their requests as follows.

| Former skill | Guide |
| --- | --- |
| `setup-cao` | `.github/cao/tasks/setup.md` |
| `add-cao-campaign` | `.github/cao/tasks/operations/add-campaign.md` |
| `create-cao-campaign` | `.github/cao/tasks/operations/create-campaign.md` |
| `cao-cli` | `.github/cao/tasks/gh-cao.md` |
| `analyze-cao` | `.github/cao/tasks/dashboard-analysis.md` |
| `debug-cao` | `.github/cao/tasks/diagnostics.md` |

## Evidence first

Ground conclusions in named dashboard queries (`gh cao query-info`,
`gh cao query`, or MCP `cao_catalog`/`cao_query`) and their run, workflow, and
safe-output provenance, not the rendered UI. Preserve availability,
completeness, freshness, and as-of metadata; missing or stale evidence is
incomplete, never zero.

## Boundaries

- CAO policy decides whether and where an operation runs; gh-aw decides how.
  Neither grants the other's authority.
- `review` is the default. Never broaden scope, enablement, or `live` mode
  without an explicit reviewed change to `.github/workflows/cao.json`.
- Agent GitHub tools stay read-only; writes use declared safe outputs only.
- Fail closed when policy, credentials, access, or evidence is missing.
- Never print or persist tokens, private keys, or other secrets.
