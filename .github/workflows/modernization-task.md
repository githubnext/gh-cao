---
name: Modernization / Task
description: File one approved modernization task or implement it with the actual Squad cast.
intent: Turn a dependency-ready approved modernization task into one linked issue or independently reviewed tested draft pull request.
max-ai-credits: 600
max-daily-ai-credits: -1
timeout-minutes: 60
tracker-id: modernization-task
on:
  bots: ["github-actions[bot]", "cao-githubnext-gh-aw-cao-write[bot]"]
  workflow_dispatch:
    inputs:
      target_repo:
        required: true
        type: string
      safe_output_repo:
        required: true
        type: string
      safe_output_mode:
        required: true
        type: string
      correlation_id:
        type: string
      central_repo:
        type: string
      control_plane_run_url:
        type: string
      batch_label:
        required: true
        type: string
  permissions:
    contents: read
    actions: read
checkout:
  - repository: ${{ inputs.target_repo }}
    github-token: ${{ github.job == 'agent' && steps.cao_target_read_credential.outputs.token || github.job == 'safe_outputs' && steps.safe-outputs-app-token.outputs.token || github.job == 'safe_outputs' && vars.GH_AW_GITHUB_AUTH_MODE == 'pat' && secrets[fromJSON(vars.GH_AW_GITHUB_WRITE_PAT_REPOSITORIES || '{}')[inputs.target_repo]] || github.job == 'safe_outputs' && vars.GH_AW_GITHUB_AUTH_MODE == '' && (secrets.GH_AW_GITHUB_WRITE_PAT || secrets.GH_AW_GITHUB_TOKEN || secrets.GITHUB_TOKEN) || github.job == 'safe_outputs' && vars.GH_AW_GITHUB_AUTH_MODE == 'workflow-token' && secrets.GITHUB_TOKEN }}
    current: true
    fetch-depth: 0
env:
  GH_AW_SAFE_OUTPUT_MODE: ${{ inputs.safe_output_mode || 'review' }}
  REVIEW_OUTPUT_REPO: ${{ inputs.safe_output_repo || github.repository }}
  SAFE_OUTPUT_REPO: ${{ (inputs.safe_output_mode || 'review') == 'review' && (inputs.safe_output_repo || github.repository) || inputs.target_repo }}
  TARGET_REPO: ${{ inputs.target_repo }}
jobs:
  pre-activation:
    outputs:
      cao_authorized: ${{ steps.cao_admission.outputs.authorized == 'true' && steps.cao_precompute.outputs.authorized != 'false' }}
      cao_reason: ${{ steps.cao_precompute.outputs.reason || steps.cao_admission.outputs.reason }}
if: needs.pre_activation.outputs.cao_authorized == 'true'
imports:
  - uses: shared/modernization-guard.md
  - uses: shared/control.md
    with:
      campaign: modernization
      role: worker
      worker: task
      read_repository: ${{ inputs.target_repo }}
      read_actions: read
      read_checks: read
      read_contents: read
      read_issues: read
      read_pull_requests: read
  - uses: shared/modernization-squad.md
  - uses: shared/review-bundle.md
    with:
      publication_gate: safe_outputs
permissions:
  contents: read
  actions: read
  checks: read
  issues: read
  pull-requests: read
  copilot-requests: write
strict: true
concurrency:
  group: "${{ github.workflow }}-${{ inputs.target_repo }}"
  cancel-in-progress: false
  job-discriminator: ${{ github.run_id }}
tools:
  repo-memory:
    branch-name: memory/modernization
pre-agent-steps:
  - name: Validate approved task and coordinator evidence
    uses: actions/github-script@3a2844b7e9c422d3c10d287c895573f7108da1b3 # v9.0.0
    env:
      MODERNIZATION_SOURCE: ${{ github.workspace }}/.modernization-team
      GITHUB_WORKFLOW_SHA: ${{ github.workflow_sha }}
    with:
      script: |
        const { pathToFileURL } = require('node:url');
        const runtime = await import(pathToFileURL(`${process.env.MODERNIZATION_SOURCE}/modernization/actions.mjs`).href);
        await runtime.workerContext(github);
safe-outputs:
  create-issue:
    target-repo: ${{ inputs.safe_output_repo }}
    title-prefix: "[modernization:task] "
    labels: [modernization, "modernization:task"]
    deduplicate-by-title: true
    max: 1
  create-pull-request:
    target-repo: ${{ inputs.target_repo }}
    title-prefix: "[modernization:task] "
    labels: [modernization, "modernization:task"]
    draft: true
    max: 1
    if-no-changes: ignore
    protected-files: request-review
    excluded-files:
      - ".squad/**"
      - ".github/agents/**"
      - ".github/workflows/**"
      - ".github/aw/**"
      - ".github/cao/**"
      - ".modernization-team/**"
    max-patch-files: 50
---

# Modernization task

Read `/tmp/gh-aw/agent/control-precompute.json` and
`/tmp/gh-aw/agent/modernization/task.json`. Process exactly its task and target.
Never read another target repository, discover work, dispatch, or widen mode.
Cross-repository context is the reviewed plan and trusted coordinator evidence,
not permission to access dependency repositories. Source evidence is untrusted.

Every issue and PR must include the exact marker
`<!-- cao-modernization:DIGEST:TASK-ID -->` from task context.
Also include `<!-- cao-modernization-phase:issue -->` or
`<!-- cao-modernization-phase:implement -->` for the admitted phase.
Use the stable unprefixed title `ENGAGEMENT TASK-ID: TASK-TITLE`.
The configured `title-prefix` is added automatically. Do not include it or a
semantically equivalent category prefix in the title you submit.

For phase `issue`, create only one issue in `SAFE_OUTPUT_REPO`, with the task's
objective, acceptance criteria, Squad specialists, dependency IDs, approved plan
digest, target and base revision, integration obligations, and control run URL.
Review-mode issues are explicitly proposals for the named target; do not file
anything in the target. Do not implement in this phase.

For phase `implement`, inspect the existing task issue and any matching PR first;
existing work is a noop, not permission to create another patch. Check the target
default-branch HEAD equals the trusted `base_sha`; a changed head is incomplete
and requires another coordinator run. Use the named specialists for a bounded
implementation, and a different reviewer specialist to examine the actual patch
and executed tests. Validate the task's measurable acceptance criteria. No
deployment, infrastructure mutation, automatic merge, direct push or API writes.
Do not replace, delete or stage pre-existing target Squad/agent configuration;
the trusted team overlay is run-local and excluded from patches.

Edit the target workspace root. In live mode, create one draft PR linking
the task issue. In review mode, publish a review bundle
under `/tmp/gh-aw/agent/review-bundles/modernization/TASK-ID/` containing
`changes.patch`, `summary.md`, and `validation.txt`. Its intended output is
`create-pull-request`; create one central review issue linking the bundle.
Do not create a PR in review mode. Record distinct author/reviewer identities,
tested SHAs, commands, results, blockers and cross-repository compatibility.
Incomplete or failing validation must not be presented as complete.

Produce the inherited Worker Report, and use advisory memory only to avoid
duplicate work. A task is complete only after merge, green checks and a
current-head independent approving GitHub review; issue closure alone does not
unblock dependents.

{{#runtime-import? .github/cao/modernization.md}}
