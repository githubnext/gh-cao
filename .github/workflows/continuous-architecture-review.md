---
name: "Continuous Architecture Review"
description: "Selects one authorized repository for a bounded architecture implementation"
intent: Improve the scoped CAO repository through small, verified architecture changes rather than assessment-only findings.

run-name: "${{ github.event_name == 'schedule' && 'Architecture review · scheduled' || format('Architecture review · {0} · {1}', inputs.target_repo || 'discovery', inputs.safe_output_mode || 'review') }}"

max-ai-credits: 200
max-daily-ai-credits: -1
timeout-minutes: 15

concurrency:
  group: "${{ github.workflow }}"
  job-discriminator: ${{ github.run_id }}
  cancel-in-progress: true

on:
  schedule: "every 6 hours"
  workflow_dispatch:
    inputs:
      target_repo:
        type: string
      safe_output_repo:
        type: string
      max_repos:
        default: 1
        type: number
      rollout_percent:
        default: 100
        type: number
      safe_output_mode:
        default: "review"
        type: choice
        options:
          - review
          - live
      correlation_id:
        type: string
      central_repo:
        type: string
      control_plane_run_url:
        type: string
  permissions:
    contents: read
    actions: read

env:
  GH_AW_SAFE_OUTPUT_MODE: ${{ inputs.safe_output_mode || 'review' }}
  REVIEW_OUTPUT_REPO: ${{ inputs.safe_output_repo || github.repository }}
  SAFE_OUTPUT_REPO: ${{ (inputs.safe_output_mode || 'review') == 'review' && (inputs.safe_output_repo || github.repository) || '' }}
  TARGET_REPO: ${{ inputs.target_repo || '' }}

jobs:
  pre-activation:
    outputs:
      cao_authorized: ${{ steps.cao_admission.outputs.authorized == 'true' && steps.cao_precompute.outputs.authorized != 'false' }}
      cao_reason: ${{ steps.cao_precompute.outputs.reason || steps.cao_admission.outputs.reason }}

if: needs.pre_activation.outputs.cao_authorized == 'true'

imports:
  - uses: shared/control.md
    with:
      campaign: continuous-architecture-review
      role: orchestrator
      read_repository: ${{ github.repository }}
      dispatch_max: 1
      orchestrator_credits: 200
      worker_credits_per_target: 600
      read_actions: read
      read_contents: read
      read_pull_requests: read

permissions:
  contents: read
  actions: read
  copilot-requests: write
  pull-requests: read

engine: copilot
strict: true

tools:
  github:
    mode: remote
    toolsets: [repos, pull_requests, actions]

network:
  allowed:
    - defaults
    - github

safe-outputs:
  dispatch-workflow:
    workflows: [continuous-architecture-review-implementation]
    max: 1
  threat-detection: false
---

# Continuous Architecture Review

Select one authorized `githubnext/gh-cao` repository revision for incremental architecture improvement. Do not review or modify source code here; the worker owns that work.

Read `/tmp/gh-aw/agent/control-precompute.json` first. Only the exact `githubnext/gh-cao` candidate may be selected, and only when the effective policy enables `implementation`. Never infer eligibility from the repository name or files in a target checkout; precompute must explicitly admit it. If the candidate is absent, inaccessible, or not admitted, report the reason without dispatching.

Use recent code changes, failures and open architecture pull requests only to prioritize one dispatch. Avoid duplicating an already open `[continuous-architecture-review:implementation]` pull request or review issue for the same work. Dispatch the worker at most once for `(implementation, githubnext/gh-cao, effective mode)`. Do not dispatch other campaigns, discover further repositories, or promote review to live.

Finish with the standard `## Orchestrator Report` inherited from `shared/control.md`, retaining its `Scope`, `Repository Decisions`, `Workers`, `Dispatches`, and `Outcome` fields. Distinguish skipped and deferred work, and call `noop` once with that report when no dispatch is warranted.

{{#runtime-import? .github/cao/continuous-architecture-review.md}}
