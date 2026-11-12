# ESLint Factory Campaign

> [!NOTE]
> **Research prototype:** Features and interfaces may change as the project evolves.

The ESLint Factory campaign builds one small, centrally curated ESLint rule library from real defects found in enrolled JavaScript and TypeScript repositories, and asks maintainers to adopt each rule deliberately, in warning-only mode. It never edits a target repository.

The Agentic Workflow definitions remain in the control repository. Target repositories receive only declared safe outputs; they do not receive installed copies of these workflows.

## What It Does

- Discovers and ranks eligible JavaScript and TypeScript repositories from precomputed policy, using one bounded repository-languages call per candidate.
- Maps how each repository lints today — configuration flavour, versions, scripts, CI enforcement, and coverage.
- Mines a bounded window of merged pull requests, commits, and review comments for repeated, independently corroborated defects, and proposes at most one rule candidate per run.
- Evaluates candidate and active rules against real repository code and records precision, diagnostic, autofix, and performance outcomes honestly.
- Raises at most one deduplicated adoption issue per repository, asking for warning-only enforcement, a dedicated npm script, and a separate CI build job.
- Normalizes the library over time by collapsing duplicates and deprecating rules that no longer earn their place.

## Campaign Contents

| Workflow | Role |
| --- | --- |
| [`eslint-rules`](../.github/workflows/eslint-rules.md) | Hourly orchestrator workflow that discovers, ranks, and selects repositories and dispatches workers. |
| [`eslint-rules-inventory`](../.github/workflows/eslint-rules-inventory.md) | Maps ESLint support and adoption readiness for one repository into shared memory. |
| [`eslint-rules-miner`](../.github/workflows/eslint-rules-miner.md) | Mines bounded recent defect evidence for one repository and proposes at most one corroborated rule candidate. |
| [`eslint-rules-refiner`](../.github/workflows/eslint-rules-refiner.md) | Evaluates central rules against one repository and records precision-first outcomes. |
| [`eslint-rules-applier`](../.github/workflows/eslint-rules-applier.md) | Opens one deduplicated warning-only adoption request for one repository. |
| [`eslint-rules-librarian`](../.github/workflows/eslint-rules-librarian.md) | Collapses duplicates, deprecates stale rules, and repairs library drift. |

Every worker workflow is independently dispatchable, handles exactly one target repository, and ends with exactly one terminal safe output. Only the applier can produce a repository-visible outcome, and that outcome is a single issue.

## Shared Memory

All six workflows share one repo-memory branch, `memory/eslint-rules`.

- `transactions/<worker>__<owner>__<repository>.jsonl` are stable, per-writer append-only transaction logs and the authoritative record. File names are flat, lower-case, and collision-safe. Lines are never rewritten or deleted, while the stable names keep scheduled runs from consuming a new repo-memory file each day.
- `repository-priority` transactions retain the orchestrator's latest bounded ranking and dispatch decision for each enrolled repository.
- `rules/<rule-key>.json` is a flat directory holding the current normalized state of each rule.
- Only compact evidence references and outcomes are persisted — permalinks, numbers, paths, counts, and classifications. Review comment text, agent transcripts, diffs, logs, and source dumps are never stored.

[`rules-db.mjs`](./rules-db.mjs) validates the authoritative logs and emits deterministic summaries directly. It uses only the Node.js standard library and creates no database or shadow memory:

```bash
node eslint-rules/rules-db.mjs verify --memory "$GH_AW_MEMORY_DIR"
node eslint-rules/rules-db.mjs summary --memory "$GH_AW_MEMORY_DIR"
```

Both commands fail closed on malformed lines, unsupported versions and duplicate transaction IDs. Summaries retain original evidence and observation order without changing the source logs.

## Install

Install the campaign into a Central Agentic Ops control repository:

```bash
gh aw add githubnext/gh-cao/eslint-rules
```

The campaign is runnable after credentials, when needed, and checked-in policy are configured.

## Configure

Declare the campaign in `.github/workflows/cao.json`:

```json
{
	"version": 1,
	"control-plane": {
		"campaigns": {
			"eslint-rules": {
				"mode": "review",
				"max-repositories": 1,
				"workers": {
					"inventory": { "workflow": "eslint-rules-inventory" },
					"miner": { "workflow": "eslint-rules-miner" },
					"refiner": { "workflow": "eslint-rules-refiner" },
					"applier": { "workflow": "eslint-rules-applier" },
					"librarian": { "workflow": "eslint-rules-librarian" }
				}
			}
		}
	}
}
```

The omitted fields default to an enabled campaign and worker and 100 percent rollout. Set shared owner and repository boundaries under `control-plane.scope`.

## Validate in review mode

1. Open the generated **ESLint Factory** workflow in the control repository's **Actions** tab.
2. Select **Run workflow**.
3. Leave `target_repo` blank to review the control repository itself, or set it to one fully qualified `owner/repository` name.
4. Keep `max_repos` at `1` and `safe_output_mode` at `review`.
5. Inspect repository selection, the dispatched workers, the `memory/eslint-rules` branch, and the adoption request in the control repository.

## Safety Boundaries

- GitHub tools are read-only; mutations occur only through declared safe outputs.
- The orchestrator selects repositories but never mutates one, and never searches beyond the precomputed candidate list.
- A worker receives one target and cannot discover more repositories, dispatch another workflow, or promote its mode.
- No worker changes a target repository. The applier's single deduplicated issue is the only repository-visible output.
- API use is explicitly bounded: fixed windows, fixed page sizes, single pages, and no `gh api --paginate`.
- Credentials remain in the control repository and are never included in dispatch inputs or memory.

## Operational Value

This campaign intentionally ships no operational grader. Its value — a rule adopted and a class of defect stopped — is decided by a maintainer days or weeks after a run, so nothing observable at a single run's boundary would measure it honestly. Recording a self-reported score at that boundary would fabricate attainment, so the workers report evidence and outcomes instead, and the dashboard shows runs and adoption requests rather than a synthetic success rate.

## Pause or Stop

Set `control-plane.campaigns.eslint-rules.enabled` to `false`, deploy that reviewed policy revision, and cancel active runs. For a narrower stop, set an individual worker's `enabled` to `false`. Re-enable in `review` mode after resolving the incident. For a control-plane-wide stop, follow the [emergency-stop procedure](../docs/operations.md#emergency-stop).

## More Information

- [Configuration reference](../docs/configuration.md)
- [Rollout and safe output routing](../docs/rollout-and-routing.md)
- [Control architecture](../docs/architecture.md)
- [Operations and incident response](../docs/operations.md)
