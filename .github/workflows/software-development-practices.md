---
emoji: ":compass:"
description: "Selects repositories for evidence-led GitHub Well-Architected and NIST SSDF guidance."
intent: Reduce the effort required to identify evidence-backed improvements to software development practices without creating duplicate or unsupported work.
name: "Dev Practices"

run-name: "${{ github.event_name == 'schedule' && 'Dev Practices · scheduled' || format('Dev Practices · {0} · {1}', inputs.target_repo || 'discovery', inputs.safe_output_mode || 'review') }}"

max-ai-credits: 250
max-daily-ai-credits: -1
timeout-minutes: 15

concurrency:
  group: "${{ github.workflow }}"
  job-discriminator: ${{ github.run_id }}
  cancel-in-progress: true

on:
  schedule: "hourly"
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
      campaign: software-development-practices
      role: orchestrator
      read_repository: ${{ github.repository }}
      dispatch_max: 20
      orchestrator_credits: 250
      worker_credits_per_target: 800
      read_actions: read
      read_contents: read
      read_issues: read
      read_pull_requests: read

permissions:
  contents: read
  actions: read
  copilot-requests: write
  issues: read
  pull-requests: read

engine:
  id: pi
  model-routing:
    goal: cost
    mode: auto

strict: true

tools:
  cli-proxy: true
  github:
    mode: gh-proxy
    toolsets: [repos, issues, pull_requests, actions]

network:
  allowed:
    - defaults
    - github

safe-outputs:
  dispatch-workflow:
    workflows:
      - software-development-practices-github-well-architected
      - software-development-practices-nist-ssdf
    max: 20
  threat-detection: false
---

<!-- Dev Practices outputs are advisory and non-binding. They provide no guarantee of completeness, correctness, security, compliance, or alignment with current GitHub or NIST guidance. -->

# Dev Practices

Select and rank repositories for evidence-led improvement guidance based on GitHub Well-Architected and the NIST Secure Software Development Framework. All findings require human review. The orchestrator must not assess a target, create findings, claim framework alignment, or perform worker responsibilities.

## Discovery

Read `/tmp/gh-aw/agent/control-precompute.json` first and use its candidates, modes, workers, and limits as authoritative. An explicit `target_repo` takes precedence within the shared control-plane rules.

Rank active software repositories using:

1. Recent commits, releases, artifacts, deployments, or supported software artifacts.
2. Build, test, release, deployment, dependency, and security workflows.
3. Campaign manifests, source code, infrastructure as code, architecture records, `README`, `CONTRIBUTING`, `CODEOWNERS`, `SECURITY.md`, and support documentation.
4. Open maintenance or security work that indicates actionable development-practice improvements.

Exclude archived or disabled repositories, inaccessible repositories, generated mirrors without meaningful ownership, and repositories with no software-development or operational surface. Do not interpret missing metadata as a framework gap.

Use bounded two-stage discovery. Rank the complete precomputed batch using trusted metadata, then inspect only the strongest candidates needed to fill `effective_max_repos` plus at most two alternates per available slot. Prefer cheap repository-tree, activity, release, workflow, policy, and existing guidance-issue checks. Stop when the selected targets and defensible alternates are established.

## Workers

Resolve enabled workers from precompute. Dispatch one repository-level responsibility per useful worker and never fan out by pillar, practice, task, finding, file, or issue.

- `software-development-practices-github-well-architected` reviews observable evidence across the current GitHub Well-Architected pillars and produces one consolidated improvement issue.
- `software-development-practices-nist-ssdf` reviews observable evidence against the current final NIST SSDF practices and produces one consolidated improvement issue.

Dispatch `github-well-architected` when a repository has meaningful collaboration, workflow, GitHub configuration, or architecture evidence. Dispatch `nist-ssdf` when it ships or supports software with a security-relevant surface such as releases, artifacts, dependencies, builds, or vulnerability handling. Dispatch both only when both conditions hold.

Calculate the proposed dispatch count across selected repositories and enabled workers. Keep the total at or below 20, reduce repository or worker selection if needed, and record every dispatch or skip rationale. Workers own source-aware duplicate detection because the orchestrator must not assess framework currency.

## Completion

Finish with the standard `## Orchestrator Report` inherited from `shared/control.md`. Preserve every standard heading and field under `Scope`, `Repository Decisions`, `Workers`, `Dispatches`, and `Outcome`; use exact precomputed repository totals, distinguish eligible, selected, skipped, and deferred repositories, and use `0`, `none`, or `not applicable` for empty fields. Add framework-specific selection rationale only without renaming, replacing, or omitting standard fields.

{{#runtime-import? .github/cao/software-development-practices.md}}
