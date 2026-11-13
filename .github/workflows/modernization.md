---
name: Modernization
description: Select dependency-ready tasks from one reviewed multi-repository modernization engagement.
intent: Coordinate approved modernization work without dispatching dependent tasks before independently reviewed prerequisites have merged.
max-ai-credits: 250
max-daily-ai-credits: -1
timeout-minutes: 15
on:
  schedule: hourly
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
        options: [review, live]
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
    steps:
      - name: Checkout reviewed modernization engagement
        if: steps.cao_precompute.outputs.authorized != 'false' && steps.cao_admission.outputs.authorized == 'true'
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          ref: ${{ github.workflow_sha }}
          persist-credentials: false
          path: .modernization-control
      - name: Resolve dependency-ready modernization tasks
        if: steps.cao_precompute.outputs.authorized != 'false' && steps.cao_admission.outputs.authorized == 'true'
        uses: actions/github-script@3a2844b7e9c422d3c10d287c895573f7108da1b3 # v9.0.0
        env:
          MODERNIZATION_SOURCE: ${{ github.workspace }}/.modernization-control
          GITHUB_WORKFLOW_SHA: ${{ github.workflow_sha }}
          CAO_AUTH_MODE: ${{ vars.GH_AW_GITHUB_AUTH_MODE }}
          CAO_CONTROL_READ_TOKEN: ${{ steps.cao_pre_activation_app_token.outputs.token }}
          CAO_READ_APP_ID: ${{ (vars.GH_AW_GITHUB_AUTH_MODE == 'app' || vars.GH_AW_GITHUB_AUTH_MODE == '') && vars.GH_AW_GITHUB_READ_APP_ID || '' }}
          CAO_READ_APP_PRIVATE_KEY: ${{ (vars.GH_AW_GITHUB_AUTH_MODE == 'app' || vars.GH_AW_GITHUB_AUTH_MODE == '') && secrets.GH_AW_GITHUB_READ_APP_PRIVATE_KEY || '' }}
          CAO_READ_PAT_REPOSITORIES: ${{ vars.GH_AW_GITHUB_READ_PAT_REPOSITORIES }}
          CAO_CONTROL_READ_PAT: ${{ vars.GH_AW_GITHUB_AUTH_MODE == 'pat' && secrets[fromJSON(vars.GH_AW_GITHUB_READ_PAT_REPOSITORIES || '{}')[github.repository]] || '' }}
          CAO_INVENTORY_PAT_REPOSITORY: ${{ inputs.target_repo || github.repository }}
          CAO_INVENTORY_READ_PAT: ${{ vars.GH_AW_GITHUB_AUTH_MODE == 'pat' && secrets[fromJSON(vars.GH_AW_GITHUB_READ_PAT_REPOSITORIES || '{}')[inputs.target_repo || github.repository]] || '' }}
          CAO_LEGACY_READ_TOKEN: ${{ vars.GH_AW_GITHUB_AUTH_MODE == '' && (secrets.GH_AW_GITHUB_READ_PAT || secrets.GH_AW_GITHUB_TOKEN) || '' }}
          CAO_WORKFLOW_TOKEN: ${{ github.token }}
        with:
          script: |
            const { pathToFileURL } = require('node:url');
            const runtime = await import(pathToFileURL(`${process.env.MODERNIZATION_SOURCE}/modernization/actions.mjs`).href);
            await runtime.prepare({ github, core, getOctokit });
      - name: Freeze modernization dispatch evidence
        if: steps.cao_precompute.outputs.authorized != 'false' && steps.cao_admission.outputs.authorized == 'true'
        uses: actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a # v7.0.1
        with:
          name: modernization-dispatch-${{ github.run_attempt }}
          path: /tmp/gh-aw/agent/modernization/dispatch.json
          if-no-files-found: error
          retention-days: 1
if: needs.pre_activation.outputs.cao_authorized == 'true'
imports:
  - uses: shared/modernization-guard.md
  - uses: shared/control.md
    with:
      campaign: modernization
      role: orchestrator
      read_repository: ${{ github.repository }}
      dispatch_max: 3
      orchestrator_credits: 250
      worker_credits_per_target: 600
      read_actions: read
      read_checks: read
      read_contents: read
      read_issues: read
      read_pull_requests: read
      read_statuses: read
permissions:
  contents: read
  actions: read
  checks: read
  issues: read
  pull-requests: read
  statuses: read
  copilot-requests: write
engine: copilot
strict: true
concurrency:
  group: "${{ github.workflow }}"
  cancel-in-progress: true
  job-discriminator: ${{ github.run_id }}
tools:
  repo-memory:
    branch-name: memory/modernization
  bash: true
  cli-proxy: true
network:
  allowed: [defaults, github]
steps:
  - name: Restore frozen modernization task selection
    uses: actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c # v8.0.1
    with:
      name: modernization-dispatch-${{ github.run_attempt }}
      path: /tmp/gh-aw/agent/modernization
safe-outputs:
  dispatch-workflow:
    workflows: [modernization-task]
    max: 3
  threat-detection: false
---

# Modernization coordination

Read `/tmp/gh-aw/agent/control-precompute.json`, the frozen
`/tmp/gh-aw/agent/modernization/dispatch.json`, and `$GH_AW_MEMORY_DIR`.
Memory is advisory; only the trusted frozen ready set selects work.
Do not inspect target code, create plans, file issues, or implement changes.

Dispatch each ready entry at most once to `modernization-task`, using its exact
repository, mode, destination and `batch_label`. Supply the standard control
envelope with this control repository and this run's URL. Never invent or alter
a ready entry. Do not select more than `effective_max_repos` repositories or
three tasks. If none are ready, call `noop` and distinguish awaiting approval,
dependencies, existing work, missing evidence, and completion in the report.

The reviewed `.github/cao/modernization.json` is intent, not rollout authority.
Each worker still passes current CAO admission. A new task or revised plan needs
a new reviewed commit. Preserve bounded advisory dispatch history in memory.
Finish with the inherited standard Orchestrator Report.

{{#runtime-import? .github/cao/modernization.md}}
