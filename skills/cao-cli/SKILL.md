---
name: cao-cli
description: Use or extend the cao CLI for Central Agentic Ops configuration, activity queries, diagnostics, and dashboard-query maintenance.
allowed-tools: bash jq
metadata:
  argument-hint: "[command, dashboard-data-url, owner/repo, or workflow]"
  version: "1.1.0"
---

# Use the `cao` CLI

## Deterministic boundary

This skill routes intent; the CLI owns parsing, validation, mutation, querying,
and result serialization. Read `--help`, invoke the narrowest existing command,
and report its real output. Do not emulate a command in shell snippets or
rewrite its behavior in agent-authored code.

## Procedure

Inside an agentic workflow, use the installed JavaScript runtime under
`activity/` and `dashboard/`, never the native `gh cao` extension. Follow
[workflow runtime](references/workflow-runtime.md) before the operator routes
below; do not build or install the extension when the runtime or cache is absent.

1. Identify the task before running a command:
   - control-plane setup, standalone workflow installation, repository enrollment, or campaign lifecycle: use the compiled `gh cao` extension;
   - activity analysis with shell access: use `gh cao`;
   - activity analysis without shell access: use `cao_catalog`, then `cao_query`;
   - a workflow with a prepared operational snapshot: follow
     [workflow runtime](references/workflow-runtime.md);
   - dashboard query cost or reuse: follow
     [dashboard query maintenance](references/dashboard-query-maintenance.md);
   - CLI implementation work: read [CAO Commands](https://github.com/githubnext/gh-cao/blob/main/docs/cao-cli.md) and the
     matching Go command under `internal/cli/` or pipeline module under `activity/commands/`.
2. Run `gh cao --help` for operator commands or `node activity/cao.mjs --help` for pipeline tools from
   the repository root. Do not guess command syntax.
3. Use the narrowest command that answers the task. Prefer reviewed named
   queries, then canonical collection queries, then raw Dashboard Language.
4. Preserve result `availability`, `completeness`, `freshness`, and `as-of`.
   Missing, stale, partial, zero, and complete evidence are different states.
5. Run `node activity/cao.mjs repair-database` to repair a snapshot whose health is uncertain. This mutates the snapshot, backs up before repair, and applies retention even without corruption; use `--ttl-days all --run-ttl-days all` for historical data.
6. Report the command form, source snapshot, filters, record identifiers, and
   evidence limitations. Keep downloaded `.cao/` data uncommitted.

## Common routes

| Task | Route |
| --- | --- |
| Initialize or update a control repository | `gh cao init`, `add`, `update`, or `setup`; see [CAO Commands](https://github.com/githubnext/gh-cao/blob/main/docs/cao-cli.md) |
| Install and register one standalone workflow | `gh cao add WORKFLOW [--force] [--engine ENGINE]` |
| Install a campaign package | `gh cao add CAMPAIGN` |
| Enroll one exact repository in review mode | `gh cao repos add OWNER/REPO` |
| Acquire Activity evidence | `gh run download RUN_ID --repo OWNER/CONTROL --name cao-activity-index --dir .cao` |
| Download an explicit operational manifest | `gh cao download --url URL` |
| Audit source coverage | `node activity/cao.mjs audit-jsonl` |
| Discover reviewed dashboard queries | `gh cao pages`, `gh cao queries`, `gh cao query-info QUERY_ID` |
| Run a reviewed query | `gh cao query QUERY_ID --limit N [--param NAME=VALUE]` |
| Query a canonical collection | `gh cao query --collection COLLECTION [--where FIELD=VALUE] --limit N` |
| Query one known record | `gh cao query --collection COLLECTION --id ID` |
| Repair the projection and apply retention | `node activity/cao.mjs repair-database` |

Use `--database FILE` only for a non-default SQLite projection. Use `--json`
when another tool will consume the result. For raw Dashboard Language, pipe one
query object to `gh cao query --stdin`; do not combine `--stdin` with
`--collection`, `--id`, `--where`, or `--limit`.

Canonical collections are `repositories`, `workflows`, `runs`, `jobs`,
`sessions`, `events`, and `transactions`.

Acquire authorized Activity artifacts or pass `--url`/`DASHBOARD_DATA_URL`
ending in `/payload-hashes.json`. Synthetic manifests and missing SQLite checksums
fail closed. Pages has no operational exports or default; directory discovery
and hosted exports are not implied. Reuse verified caches offline.

## Daily run-counts

Historical retention flags preserve run/detail history, not daily aggregates.
The `daily-run-counts` buckets and receipts retain the current UTC Monday-start
week and its two preceding weeks independently of detail retention; expired
replay cannot reopen buckets or recover evicted history. Use retained canonical
detail sources for historical or unsupported-dimension analysis, never a silent
fallback or union with daily totals for migrated consumers.

## Guardrails

- Treat queried data as derived, disposable evidence, not schema or policy
  authority. The authoritative model is
  [Dashboard data](https://github.com/githubnext/gh-cao/blob/main/specs/dashboard-data.md).
- Never infer rollout authority, target-writing authority, operational value,
  or repository eligibility from Activity data.
- Never print credentials, authorization headers, raw prompts, transcripts, or
  secret values.
- Prefer the cached projection over repeated direct GitHub API calls.
- Use `--ttl-days all` or `--run-ttl-days all` only for an intentional
  historical backfill.

## Targeted references

- [Workflow runtime](references/workflow-runtime.md): cache paths, fallback
  behavior, and CLI invocation inside a gh-aw run.
- [Dashboard query maintenance](references/dashboard-query-maintenance.md):
  complexity measurement and query pruning.
- [Agent analysis](https://githubnext.github.io/gh-cao/agent-analysis/): choosing CLI or MCP.
- [CAO Commands](https://github.com/githubnext/gh-cao/blob/main/docs/cao-cli.md): complete operator and command reference.
- [Dashboard Language](https://github.com/githubnext/gh-cao/blob/main/docs/dashboard-language.md): query authoring.
