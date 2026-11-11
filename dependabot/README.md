# Dependabot Campaign

> [!NOTE]
> **Research prototype:** Features and interfaces may change as the project evolves.

The Dependabot campaign runs manifest-aware dependency maintenance from a private Central Agentic Ops control repository. It prioritizes security and repair work, selects target repositories, and dispatches one bounded update planner per repository.

The Agentic Workflow definitions remain in the control repository. Target repositories receive only declared safe outputs; they do not receive installed copies of these workflows.

## What It Does

- Prioritizes repositories with dependency alerts, Dependabot repository-access gaps, stale or conflicted update pull requests, lockfile drift, and actionable Dependabot configuration failures.
- Understands relationships among manifests, lockfiles, workspaces, solutions, source code, tests, and CI instead of grouping updates only by package name.
- Maintains one concise repository-scoped parent containing every update, blocker, and access gap, plus bounded PR-sized child tasks.
- Keeps merge order, grouping, security-sensitive surfaces, and Dependabot access boundaries visible near the top of the issue instead of burying them in collapsed details.
- Refreshes the durable existing issue, posts a confirmation comment on later runs, and replaces obsolete tasks with a completed description when no work remains.
- Remembers the issue number in repository memory and reads that issue directly on later runs instead of repeatedly using GitHub search.
- Reads optional target-maintainer guidance from `.github/dependabot.md` and tells issue readers how to use that feedback channel.
- Reads comments on the durable plan issue and incorporates safe, evidence-backed feedback about priority, grouping, validation, blockers, and risk in the next refresh.
- Keeps the durable parent unassigned and gives each delegable child a complete coding-agent prompt scoped to exactly one pull request.
- Verifies peer compatibility, generated consumers, golden fixtures, and old-version remnants before a child is assignment-ready.
- Measures whether a child task is consumed through assignment, outside participation, a linked pull request, or completed closure within 14 days.
- Never creates or changes a pull request.

## Campaign Contents

| Workflow | Role |
| --- | --- |
| [`dependabot`](../.github/workflows/dependabot.md) | Hourly orchestrator workflow that discovers, ranks, and selects repositories. |
| [Dependabot / Update Planner](../.github/workflows/dependabot-update-planner.md) | Repository-scoped worker that maintains one durable plan and bounded, PR-sized child tasks. |

The orchestrator workflow can dispatch no more than 50 worker workflows in one run. Each worker workflow handles one target repository and uses only its declared issue, refresh-comment, or `noop` safe outputs.

## Intelligence declaration

[`intelligence.json`](intelligence.json) declares the Campaign's
repository-native problem, eligible opportunity, outcome evidence, schedule,
overlap identity, output policy, maturation, deduplication, stop conditions,
and operational-value definition. After gh-aw installs the Campaign, the
trusted CAO materializer places the Campaign-owned copy at
`.github/cao/intelligence/dependabot.json`, where Activity inventory can
preserve it on the canonical Campaign record.

The declaration is descriptive evidence. It does not enable the Campaign,
enroll repositories, grant credentials, promote review work to live, or
authorize outputs. `backoff` remains undeclared until the Campaign has an
explicit reviewed backoff contract.

## Install

Install the campaign into a new private control repository owned by an organization:

```bash
gh aw add githubnext/gh-cao/dependabot
```

The campaign is runnable after credentials, when needed, and checked-in policy are configured.

## Configure

Configure a GitHub App, a fine-grained PAT, or both in the control repository for private targets, alternate review repositories, or live operation. App authentication is preferred. A bounded review run against a public target can use the automatically provided `GITHUB_TOKEN` when outputs stay in the private control repository.

Store `GH_AW_GITHUB_READ_APP_ID` and `GH_AW_GITHUB_WRITE_APP_ID` as repository variables and their corresponding `GH_AW_GITHUB_READ_APP_PRIVATE_KEY` and `GH_AW_GITHUB_WRITE_APP_PRIVATE_KEY` values as repository secrets, or store separate fine-grained PATs in `GH_AW_GITHUB_READ_PAT` and `GH_AW_GITHUB_WRITE_PAT`.

Declare Dependabot in `.github/workflows/cao.json`:

```json
{
	"version": 1,
	"control-plane": {
		"campaigns": {
			"dependabot": {
				"workers": {
					"update-planner": {
						"workflow": "dependabot-update-planner"
					}
				}
			}
		}
	}
}
```

The omitted fields default to an enabled campaign and worker, `review` mode, one repository, and 100 percent rollout. Set shared owner, repository, and inventory boundaries under `control-plane.scope` and `control-plane.inventory`.

The App installation or PAT must cover every private or internal target, alternate review repository, and live target the campaign needs to read or update. Public review runs may use `GITHUB_TOKEN`, but unavailable target Actions, security, or Dependabot data makes the run incomplete rather than broadening access or guessing. The update planner reads Dependabot alerts for private or internal targets only when the control repository is private; with a public control repository, alert evidence for non-public targets is reported as unavailable so private alert data never reaches public outputs. See the [authentication guide](../docs/authentication.md) for the permission model and credential precedence.

The update-planner rename preserves previously created plan issues. Existing issues with the former `dependabot:release-train-updater` label are recovered by the `dependabot` campaign label and canonical repository marker. Newly created issues use the portable `dependabot` label and retain worker identity through the `[dependabot:update-planner]` title prefix because live-target credentials may not be allowed to create missing labels.

Grant the read App or fine-grained PAT **Dependabot alerts: read** (`vulnerability_alerts`) on the exact target repository, with the App installation or approved PAT covering that repository. A PAT's user must also have access to its alerts. The workflows already declare `vulnerability-alerts: read` and `security-events: read`; security-events access alone does not replace the Dependabot alerts permission. They do not request security write access and never change Dependabot repository-access settings.

The planner fetches alerts with cursor pagination from the API's `Link` header, retaining only the `after` cursor and keeping requests bound to the authorized target. HTTP 400 for a request containing `page=1` is an unsupported-pagination failure, not evidence that broader permissions are needed. For HTTP 403 or 404, verify the selected read credential's target coverage and Dependabot alerts permission rather than granting write access or substituting pull requests for unavailable alert evidence.

Optional organization-level [Dependabot repository-access inspection](https://docs.github.com/en/rest/dependabot/repository-access#lists-the-repositories-dependabot-can-access-in-an-organization) separately requires **Organization administration: read** (`organization_administration`), which the default read profile does not request. This is not the repository's administration permission and is not required to list its alerts. If unavailable, the planner records that evidence gap; it must not broaden credentials or change Dependabot settings.

Target maintainers can add `.github/dependabot.md` to declare dependency priorities, grouping preferences, validation commands, and sensitive risk areas. The update planner treats this file as guidance only: it cannot grant permissions, widen repository scope, or override campaign safety rules. Every generated plan issue reports whether the file was present and which guidance was used.

## Validate in review mode

Start with one representative repository:

1. Open the generated **Dependabot** workflow in the control repository's **Actions** tab.
2. Select **Run workflow**.
3. Leave `target_repo` blank to review the control repository itself, or set it to one fully qualified `owner/repository` name to review another repository.
4. Keep `max_repos` at `1` and `safe_output_mode` at `review`.
5. Trigger a `workflow_dispatch` run and inspect repository selection, the dispatched worker workflow, review outputs in the control repository, and control-plane correlation data.

To keep scheduled runs in review, leave `mode` omitted or set it to `review` in the campaign policy. Policy changes affect future runs; cancel active runs separately during incident response.

## Promote the Campaign

| Mode | Behavior |
| --- | --- |
| `review` | Routes safe outputs to the control-plane repository; manual runs may override it with `safe_output_repo`. |
| `live` | Allows the worker to create or refresh the durable plan and its PR-sized child tasks in the selected target repository. |

Promote in order: one-repository review, limited live, then scheduled live. Change only this campaign's checked-in `mode`; other campaigns keep their own rollout state.

## Targeting

A manual `target_repo` can address a fully qualified repository only when its owner is allowlisted and the configured credential can access it. A manual `review` run with no explicit target uses the control repository itself. Scheduled runs, and manual `live` runs without a target, use bounded automatic discovery across repositories in the organization that owns the control repository. Enterprise-wide automatic discovery across multiple organizations is not provided.

The orchestrator favors:

1. Critical or high security alerts with a known patched version.
2. Dependabot repository-access blockers that prevent security updates.
3. Broken, stale, conflicted, or duplicated dependency update work.
4. Lower-severity security updates with a clear path to a safer state.
5. Dependabot, registry, toolchain, grouping, or permissions repairs.
6. Routine compatible patch and minor maintenance.

Repositories without a recognized dependency ecosystem, readable manifests, or enough evidence for a safe change are skipped.

## Safety Boundaries

- GitHub tools are read-only; mutations occur only through declared safe outputs.
- The orchestrator workflow selects repositories but does not mutate them directly.
- A worker workflow receives one target and cannot discover more repositories, dispatch another workflow, or promote its mode.
- The worker workflow cannot create or mutate pull requests or repository files.
- The worker can create one deduplicated durable parent and at most 12 deduplicated PR-sized child tasks, refresh existing issues with one parent confirmation comment, close obsolete children, replace resolved parent work with a completed description, or emit `noop`.
- The worker can report Dependabot repository-access gaps, but it cannot grant, revoke, or broaden Dependabot access.
- Comments on the plan issue can refine the next refresh only within existing control-plane policy, credential reach, Dependabot evidence, and safe-output boundaries.
- Credentials remain in the private control repository and are never included in dispatch inputs.

## Operational Questions

The campaign dashboard answers three questions from retained run, safe-output, and operational-value evidence:

1. **Does Dependabot work?** Run conclusions show whether the orchestrator and update planner activate and complete successfully.
2. **What did it produce?** The outcomes table links each durable repository plan issue and shows its disposition.
3. **Are people consuming my issues?** The consumption evaluator observes the exact remembered issue number. Assignment, outside participation, checklist progress, a linked pull request, or closure counts as consumption. Issues without a signal mature after 14 days; unavailable evidence remains unknown rather than becoming a false zero.

## Pause or Stop

Set `control-plane.campaigns.dependabot.enabled` to `false`, deploy that reviewed policy revision, and cancel active runs. For a narrower stop, set `workers.update-planner.enabled` to `false`. Re-enable in `review` mode after resolving the incident. For a control-plane-wide stop, follow the [emergency-stop procedure](../docs/operations.md#emergency-stop).

## More Information

- [Configuration reference](../docs/configuration.md)
- [Rollout and safe output routing](../docs/rollout-and-routing.md)
- [Control architecture](../docs/architecture.md)
- [Operations and incident response](../docs/operations.md)
