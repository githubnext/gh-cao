---
title: Orchestrators and Workers
description: Design and govern campaign orchestrators and their bounded worker workflows.
---

Use this page when reviewing a campaign or deciding where new behavior belongs. Orchestrators select and dispatch work; workers perform one bounded repository task and can only narrow the policy they receive.

```text
orchestrator                              worker
------------                              ------
discover candidates                       receive one target
rank and cap selection   --dispatch-->     validate the control envelope
resolve eligible workers                  analyze only that target
summarize outcomes       <--result----     emit declared safe outputs
```

:::note[The ownership test]
If behavior chooses *which repositories run*, it belongs in the orchestrator. If it decides *what to do in one selected repository*, it belongs in the worker.
:::

## Orchestrator Authority

The campaign orchestrator is the policy authority for a run. It:

- imports the campaign's configured mode and review repository;
- discovers and ranks candidate repositories;
- enforces `max_repos` and its declared dispatch maximum;
- resolves configured worker availability;
- computes the effective safe-output destination;
- dispatches workers with the standard control envelope;
- summarizes selections, skips, and dispatches.

An orchestrator does not mutate target repositories directly. Its only write-capable safe output is dispatching its declared workers.

## Worker Enforcement

A worker receives one target and performs one bounded mission. It must:

- treat control precomputation as authoritative;
- analyze only `target_repo`;
- honor `safe_output_mode` and `safe_output_repo`;
- use only declared permissions, network access, tools, and safe outputs;
- include correlation metadata in user-visible outputs when provided;
- avoid organization-wide discovery and downstream workflow dispatch;
- fail closed when routing or required evidence is incomplete.

The worker may apply stricter behavior than requested, such as returning no output when evidence is insufficient. It may never promote itself from review to live.

A worker receives control data shaped like:

```yaml
target_repo: acme/example-service
safe_output_mode: review
safe_output_repo: acme/central-agentic-ops-review
correlation_id: dependabot-2026-08-25-001
central_repo: acme/central-agentic-ops
control_plane_run_url: https://github.com/acme/central-agentic-ops/actions/runs/123456
```

It does not receive a token, discovery query, or permission to dispatch another workflow.

## Worker Value

Operational value is evaluated at repository scope through each campaign's campaign-defined `operational-value.mjs` program. It is not inferred from worker runs, dispatch counts, generated outputs, or model assessments.

The repository-scoped evaluator owns its frozen evidence contract, eligibility rules, maturity window, metric semantics, and validation. It emits campaign-defined numeric observations for each admitted repository; missing evidence remains unavailable rather than becoming zero.

:::tip[Interpret repository outcomes]
A successful dispatch or generated suggestion is activity, not proof of a repository outcome. Interpret campaign value only from its retained repository evidence and measurement contract.
:::

## Current Worker Eligibility

Shared precomputation reads each orchestrator's `safe-outputs.dispatch-workflow.workflows` list and matches it against workflows installed in the control-plane repository. A worker is eligible only when it exists and is not disabled. Missing and disabled workers are skipped with explicit reasons.

This provides an immediate worker kill switch: disable the generated worker workflow in GitHub Actions. Campaign mode and review routing remain campaign-level controls.

## Worker Ceilings

Declare each installed campaign worker and its exact workflow slug in campaign policy. Add optional worker-specific controls only when a worker has a materially different blast radius, permission set, maturity timeline, or operational owner. The controls are:

| Control | Purpose | Default |
| --- | --- | --- |
| `workflow` | Declares the exact workflow slug dispatched for this worker | Required |
| `enabled` | Explicitly excludes or re-enables a worker workflow for dispatch | `true` |
| `max-mode` | Optionally caps the most permissive mode a worker workflow can execute | Inherits the resolved campaign or exact-target mode |
| worker workflow limit | Caps worker workflow-specific volume or resource use | Existing Agentic Workflow limit |

Mode ordering is:

`review < live`

Without `max-mode`, the worker inherits the resolved campaign or exact-target mode. When an explicit ceiling is present, the effective worker mode is the less permissive of that resolved mode and the worker ceiling:

`effective_mode = worker_max_mode ? min(resolved_mode, worker_max_mode) : resolved_mode`

For example:

```text
campaign mode     = live
worker max_mode   = review
effective mode    = review
```

A manual dispatch may narrow the mode but must not exceed the worker ceiling. Review safe outputs use the manual `safe_output_repo` override when provided and otherwise use the current control-plane repository.

Example: Optimization can be live while `optimization-ai-credit-optimizer` remains capped at review. The auditor can run live under the same orchestrator if its own ceiling permits it.

```json
{
	"version": 1,
	"gh-aw-version": "v0.93.5",
	"control-plane": {
		"campaigns": {
			"optimization": {
				"workers": {
					"ai-credit-optimizer": {
						"workflow": "optimization-ai-credit-optimizer",
						"max-mode": "review"
					}
				}
			}
		}
	}
}
```

:::caution[Ceilings only narrow]
Omitting a worker ceiling does not promote the campaign; the worker follows the campaign or exact-target decision. Adding or lowering a ceiling takes effect as an additional guard beneath scheduled and manual mode requests.
:::

## When to Split Control

Keep control at the campaign level when workers share ownership, permissions, output destination, and promotion evidence. Add a worker ceiling when any of these differ significantly:

- the worker can modify source or workflow files while peers only create issues;
- the worker has broader network or repository permissions;
- the worker is newly introduced and lacks live evidence;
- the worker has a history of noisy or high-volume outputs;
- a separate team approves its production use.

Create a separate campaign, rather than many worker flags, when workers need different authentication, review repositories, schedules, target populations, or operational ownership.

Workers independently reject disabled runs, malformed control envelopes, and modes above their configured ceiling before agent execution. Promote a worker by changing its `MAX_MODE` variable only after its campaign has passed the corresponding rollout gate.
