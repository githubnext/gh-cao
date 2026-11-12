---
title: Roll Out a Campaign Safely
description: Promote one campaign from review through limited and scheduled live operation.
---

Roll out each campaign independently. Begin with one explicit target in `review`, inspect the proposal in the private review repository, and allow target writes only after that bounded scenario succeeds.

Review is real execution, not a no-write simulation. Verify
[authority and admission](architecture.md#authority-and-admission) before
following the campaign-specific policy fields and procedures below.

## Promotion at a Glance

1. Run the installed campaign in `review` against one target.
2. Verify the private review destination changed and the target did not.
3. Run one low-risk target in `live` and verify the resulting output and downstream checks.
4. Enable the scheduled live campaign with `max_repos` kept small.
5. Increase limits only from observed evidence.

Set the campaign's checked-in `enabled` field to `false` whenever authentication, routing, output quality, cost, or provenance is uncertain. Resume in `review` after correcting the issue.

```text
review --approve--> limited live --observe--> scheduled live
  ^                       |                         |
  +-----------------------+-------------------------+
                  uncertainty: disable, then review
```

## Campaign-Level Control

Each campaign under `control-plane.campaigns` has its own mode and limits. Review safe outputs route to the current control-plane repository unless a manual run supplies an allowed `safe_output_repo`. This is the primary unit of gradual rollout.

| Control | Campaign JSON field | Default |
| --- | --- | --- |
| Kill switch | `enabled` | `true` for a declared campaign |
| Output mode | `mode` | `review` |
| Scheduled absolute cap | `max-repositories` | `1` |
| Rollout percentage | `rollout-percent` | `100` |
| Exact target mode | `targets.<owner/repository>.mode` | Campaign mode |
| Worker workflow identity | `workers.<worker>.workflow` | Required workflow slug |
| Worker kill switch | `workers.<worker>.enabled` | `true` |
| Optional worker mode ceiling | `workers.<worker>.max-mode` | Inherit campaign or exact-target mode |

Changing one campaign does not change another. For example, Dependabot may be live while Optimization remains in review.

An exact campaign target can advance independently while the campaign remains in review elsewhere:

```json
{
	"dependabot": {
		"mode": "review",
		"targets": {
			"acme/example-service": { "mode": "live" }
		}
	}
}
```

Unmatched repositories retain the campaign mode. Exact targets must remain inside `control-plane.scope`, comparisons are case-insensitive, and duplicate spellings fail validation. The worker re-resolves its own target policy before execution, so a dispatched envelope cannot promote a review target. A manual mode may narrow all selected targets to review but cannot widen any target to live.

Absolute caps default to `1`, so missing configuration cannot create broad fan-out. Rollout percentages accept integers from `1` through `100` and default to `100`. The control plane rounds the percentage-derived repository count up for a non-empty candidate set, then applies the smallest of that count, `max_repos`, and the target count supported by the declared dispatch budget and eligible worker count. For example, a `10` percent rollout over 25 discovered repositories permits at most 3 selections before stricter caps are applied. Invalid values fail closed.

:::note[The smallest cap always wins]
For 25 discovered repositories at 10 percent, the percentage cap is 3. If `max_repos` is `1`, only one repository can be selected.
:::

Automatic discovery scans at most `control-plane.inventory.max-scan-repositories`, defaulting to `1000` with a hard maximum of `100000`. The checked-in cell and batch fields deterministically select one bounded inventory slice before ranking. They do not auto-advance or retry batches. Manual target and review repositories must satisfy `control-plane.scope`, whose allowed owners default to the control repository owner.

### Live Authority Check

The control repository's `.github/workflows/cao.json` is the sole live-activation decision marker. Before promoting a campaign to `live`, operators must declare the campaign mode, worker ceiling, allowed owner or exact repository, and any target-specific mode in that policy. Workers resolve it at the exact workflow SHA, so a target repository cannot widen, narrow, or veto the decision by adding, changing, or removing its own files.

If an enterprise and organization runtime both select the same target and campaign, keep both in `review` until operators choose one control repository and remove live scope from the other. Do not rely on run timing or workflow concurrency to resolve the conflict. Separate control repositories have independent queues and kill switches.

## Modes

| Mode | Target behavior | Intended use |
| --- | --- | --- |
| `review` | safe outputs route to the current control-plane repository, with an optional manual `safe_output_repo` override | Human review of proposed effects before target mutation |
| `live` | Declared worker workflow safe outputs may write to the selected target | Production campaign after promotion gates pass |

Review mode is the installation default. It resolves its destination from the manual `safe_output_repo` workflow input, then `github.repository`.

In review mode, the review repository is not treated as a clone of the target. When a target-bound mutation cannot be represented natively against the review repository, the worker should publish an artifact-backed review bundle describing the target, intended output primitive, base branch, and supporting evidence.

<span id="pages-report-routing"></span>

## Dashboard hosting is not safe-output routing

Campaign mode selects destinations for declared safe outputs, not dashboard
hosting. `safe_output_repo` remains the review-output destination; it does not
need a Pages site. Production dashboards serve operational evidence through the
same-origin Go/PostgreSQL host, and local development uses the same Go server
over an authorized canonical PostgreSQL data.

Pages publishes only the synthetic enterprise demo, independently of campaign
mode and operational freshness. No operational artifacts or review outputs
belong in that demo. Agents receive neither Pages deployment permission nor
mode-promotion authority. See [Actions artifacts and the Pages demo](deployment-actions.md)
for publication and legacy-data migration boundaries.

## `workflow_dispatch` Runs

A `workflow_dispatch` run can set the `target_repo`, `max_repos`, `rollout_percent`, `safe_output_mode`, and `safe_output_repo` workflow inputs. These DispatchOps runs are useful for a controlled canary or incident diagnosis. They do not update checked-in policy. Mode and numeric requests may narrow the resolved campaign policy but cannot widen it.

`workflow_dispatch` runs should narrow scope during validation:

- specify one `target_repo`;
- keep `max_repos` at `1`;
- use `review` first;
- use the control-plane repository for scheduled review runs, and use `safe_output_repo` only when a manual run needs a private override;
- do not use a manual live run to bypass failed promotion gates.

Example canary inputs:

```yaml
target_repo: acme/example-service
max_repos: 1
rollout_percent: 100
safe_output_mode: review
safe_output_repo: ""
```

## Promotion Plan

Promote each campaign independently:

1. **Installed in review**: credentials and repository access are configured; proposals route to the private review destination without target writes.
2. **Review verified**: run against one representative repository; inspect selection, prompts, permissions, correlation data, and the actionable proposal. For a Pages report, also verify that the access-controlled review site updates and production Pages does not.
3. **Enrolled**: record target-owner approval separately and commit the assigned live authority and explicit target scope in the control repository's reviewed `.github/workflows/cao.json`.
4. **Limited live**: confirm no other control repository has live authority for the same campaign, then manually target one low-risk repository and verify the resulting safe output and downstream CI. For a Pages report, verify the production site update independently of the review site.
5. **Scheduled live**: enable the scheduled campaign with `max_repos` kept small, then increase limits only from observed evidence.

Promotion evidence should cover successful authentication, correct target selection, safe output routing, no unexpected writes, worker workflow completion, useful safe output quality, and acceptable AI Credit consumption.

:::tip[Promote evidence, not elapsed time]
A campaign does not become safer because it remained in a mode for several days. Promote only after a representative run satisfies that mode's checks.
:::

## Rollback

The first rollback action is to set the affected campaign's `enabled` field to `false` in `.github/workflows/cao.json` and deploy that reviewed revision. For a narrower incident, set the worker's `enabled` field to `false`. Then:

1. stop new dispatches;
2. inspect the orchestrator run and correlated worker runs;
3. close, revert, or supersede unintended safe outputs using normal repository procedures;
4. if a workflow or campaign release caused the incident, restore its last known-good Git revision, compile every affected workflow, and deploy that revision through the normal reviewed change process;
5. otherwise, correct the affected policy or worker behavior and compile every affected workflow;
6. re-enable the campaign in review mode and repeat promotion gates.

Do not reduce another campaign's mode unless the incident involves shared authentication or shared control behavior.

If two runtimes were found mutating the same `(target repository, campaign)` pair, disable that campaign in every conflicting control repository, cancel active runs, and assign one live authority before resuming in review. Stopping only one runtime is insufficient until its queued and in-progress runs are also canceled.
