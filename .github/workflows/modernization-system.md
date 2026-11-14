---
name: Modernization / System
description: Assess or verify one bounded multi-repository modernization engagement with the actual Squad.
intent: Produce an evidence-backed coordinated modernization proposal, or independently verify the approved system-wide acceptance criteria.
max-ai-credits: 600
max-daily-ai-credits: -1
timeout-minutes: 60
on:
  workflow_dispatch:
    inputs:
      target_repos:
        description: Comma-separated explicitly enrolled repositories in this system
        required: true
        type: string
      safe_output_repo:
        type: string
      safe_output_mode:
        default: review
        type: choice
        options: [review]
      max_repos:
        default: 6
        type: number
  permissions:
    contents: read
    actions: read
env:
  GH_AW_SAFE_OUTPUT_MODE: review
  REVIEW_OUTPUT_REPO: ${{ inputs.safe_output_repo || github.repository }}
  SAFE_OUTPUT_REPO: ${{ inputs.safe_output_repo || github.repository }}
jobs:
  pre-activation:
    outputs:
      cao_authorized: ${{ steps.cao_admission.outputs.authorized == 'true' && steps.cao_precompute.outputs.authorized != 'false' }}
      cao_reason: ${{ steps.cao_precompute.outputs.reason || steps.cao_admission.outputs.reason }}
if: needs.pre_activation.outputs.cao_authorized == 'true'
imports:
  - uses: shared/control.md
    with:
      role: standalone
      workflow: modernization-system
      read_repository: ${{ github.repository }}
      read_actions: read
      read_checks: read
      read_contents: read
      read_issues: read
      read_pull_requests: read
      read_statuses: read
  - uses: shared/modernization-squad.md
permissions:
  contents: read
  actions: read
  checks: read
  issues: read
  pull-requests: read
  statuses: read
  copilot-requests: write
strict: true
tools:
  github:
    github-token: ${{ steps.modernization_system_read.outputs.token }}
concurrency:
  group: "${{ github.workflow }}"
  job-discriminator: ${{ github.run_id }}
  cancel-in-progress: false
pre-agent-steps:
  - name: Load admitted modernization system mission
    id: modernization_system_scope
    uses: actions/github-script@3a2844b7e9c422d3c10d287c895573f7108da1b3 # v9.0.0
    env:
      MODERNIZATION_SOURCE: ${{ github.workspace }}/.modernization-team
      CAO_AUTH_MODE: ${{ vars.GH_AW_GITHUB_AUTH_MODE }}
      CAO_READ_PAT_REPOSITORIES: ${{ vars.GH_AW_GITHUB_READ_PAT_REPOSITORIES }}
    with:
      script: |
        const { pathToFileURL } = require('node:url');
        const runtime = await import(pathToFileURL(`${process.env.MODERNIZATION_SOURCE}/modernization/actions.mjs`).href);
        const scope = runtime.systemContext();
        for (const [key, value] of Object.entries(scope)) core.setOutput(key, value);
  - name: Mint exact admitted system read credential
    if: steps.modernization_system_scope.outputs.mode == 'app'
    id: modernization_system_app
    uses: actions/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1 # v3.2.0
    with:
      client-id: ${{ vars.GH_AW_GITHUB_READ_APP_ID }}
      private-key: ${{ secrets.GH_AW_GITHUB_READ_APP_PRIVATE_KEY }}
      owner: ${{ steps.modernization_system_scope.outputs.owner }}
      repositories: ${{ steps.modernization_system_scope.outputs.repositories }}
      github-api-url: ${{ github.api_url }}
      permission-actions: read
      permission-checks: read
      permission-contents: read
      permission-issues: read
      permission-pull-requests: read
      permission-statuses: read
  - name: Select admitted system read credential
    id: modernization_system_read
    uses: actions/github-script@3a2844b7e9c422d3c10d287c895573f7108da1b3 # v9.0.0
    env:
      PROFILE: ${{ steps.modernization_system_scope.outputs.mode }}
      APP_TOKEN: ${{ steps.modernization_system_app.outputs.token }}
      PAT_TOKEN: ${{ vars.GH_AW_GITHUB_AUTH_MODE == 'pat' && secrets[steps.modernization_system_scope.outputs.secret] || '' }}
    with:
      script: |
        const token = process.env.PROFILE === 'app' ? process.env.APP_TOKEN : process.env.PAT_TOKEN;
        if (!token) throw new Error('The explicitly selected system read credential is unavailable');
        core.setSecret(token);
        core.setOutput('token', token);
safe-outputs:
  mentions: false
  allowed-github-references: []
  create-issue:
    target-repo: ${{ inputs.safe_output_repo || github.repository }}
    title-prefix: "[modernization-system] "
    labels: [modernization]
    deduplicate-by-title: true
    expires: 30d
    max: 1
---

# Modernization system mission

Read `/tmp/gh-aw/agent/control-precompute.json` and
`/tmp/gh-aw/agent/modernization/system.json`. Work only across its complete,
explicitly admitted repository set. No dispatch, target writes, PRs or deployment.
Invoke the actual Squad cast, including architecture, dependency, implementation,
testing and independent-review specialists appropriate to the system.

## Assessment

When phase is `assess`, inspect repositories together. Establish actual
cross-repository API, package, data, deployment and test dependencies. Do not
assume all changes can run independently. Propose a bounded modernization with
measurable acceptance per task and for the complete system, backwards-compatible
transitions, explicit target platform if any, risks, sequencing and independent
review. Start by filing one coordinated central proposal, not target issues.

Include a fenced JSON plan conforming exactly to
`modernization/engagement.mjs`'s `cao-modernization/v1` contract. Use an
engagement ID, objective, at most ten repositories (each with `repository`,
exact current default-branch `base_sha` and `base_branch`), at most fifty tasks
(each with `id`, `repository`, `title`, `objective`, nonempty `acceptance`,
`depends_on`, and exact roster `specialists` directory IDs), and nonempty
`integration_acceptance`. Dependencies must be an acyclic task graph. Every
repository must be admitted; don't quietly drop a system dependency.

The proposal must tell a human to review the plan and commit it as
`.github/cao/modernization.json` with policy and workflow changes in one reviewed
commit. That commit is approval of the concrete plan, not permission to widen
CAO scope or enable live mode. Do not write the plan or approve it yourself.

## Whole-system verification

When phase is `verify`, inspect the approved plan, linked task PRs, merged
revisions, current-head independent GitHub reviews, check results, and actual
cross-repository integration evidence. Have different specialists implement
inspection and independent verification. Verify every system acceptance
criterion against the named revisions. Issue closure, green isolated unit
tests, or a Squad summary alone are not integration acceptance.

Produce one central verification issue: pass, fail or incomplete per criterion,
exact revisions and run links, remaining compatibility risks and manual
deployment acceptance still needed. Mark the engagement complete only if every
task is merged with independent approving review and successful checks, and
every integration criterion has affirmative evidence. Never merge, deploy,
close target issues, or imply unavailable evidence passed.

{{#runtime-import? .github/cao/modernization-system.md}}
