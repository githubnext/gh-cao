---
name: "SelfCare / Code Quality"
description: Grow the dashboard component library one reviewed improvement at a time
intent: Grow the reusable dashboard component library by replacing evidenced UI duplication with tested components while preserving behavior.
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
      max_repos:
        type: number
      rollout_percent:
        type: number
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
        type: string
  skip-if-match: 'is:pr is:open "gh-aw-workflow-id: self-care-code-improvement" in:body'
  permissions:
    contents: read
    actions: read

checkout:
  repository: ${{ inputs.target_repo }}
  github-token: ${{ vars.GH_AW_GITHUB_AUTH_MODE == 'pat' && secrets[fromJSON(vars.GH_AW_GITHUB_READ_PAT_REPOSITORIES || '{}')[inputs.target_repo]] || vars.GH_AW_GITHUB_AUTH_MODE != 'pat' && secrets.GH_AW_GITHUB_READ_PAT || vars.GH_AW_GITHUB_AUTH_MODE != 'pat' && secrets.GH_AW_GITHUB_TOKEN || secrets.GITHUB_TOKEN }}
  fetch-depth: 0
  current: true

env:
  GH_AW_SAFE_OUTPUT_MODE: ${{ inputs.safe_output_mode || 'review' }}
  REVIEW_OUTPUT_REPO: ${{ inputs.safe_output_repo || github.repository }}
  SAFE_OUTPUT_REPO: ${{ (inputs.safe_output_mode || 'review') == 'review' && (inputs.safe_output_repo || github.repository) || inputs.target_repo }}
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
      campaign: self-care
      role: worker
      worker: code-improvement
      read_repository: ${{ inputs.target_repo }}
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
max-ai-credits: 400
max-daily-ai-credits: -1
timeout-minutes: 30
concurrency:
  group: "${{ github.workflow }}-${{ inputs.target_repo }}"
  job-discriminator: ${{ github.run_id }}
  cancel-in-progress: true
tracker-id: self-care-code-improvement
run-name: "SelfCare code improvement · ${{ inputs.target_repo }} · ${{ inputs.safe_output_mode || 'review' }}"
runtimes:
  node:
    version: "24"
network:
  allowed:
    - defaults
    - github
    - node
    - chrome
    - playwright
tools:
  github:
    mode: gh-proxy
    min-integrity: approved
    toolsets: [pull_requests, repos, actions]
  bash:
    - "*"
safe-outputs:
  create-pull-request:
    target-repo: ${{ (inputs.safe_output_mode || 'review') == 'review' && (inputs.safe_output_repo || github.repository) || inputs.target_repo }}
    title-prefix: "[dashboard-components] "
    labels: [dashboard-component-refactor, ai-generated]
    draft: true
    max: 1
    expires: 7d
    if-no-changes: ignore
    protected-files: fallback-to-issue
    max-patch-files: 20
    allowed-files:
      - "dashboard/site/src/*.js"
      - "dashboard/site/src/**/*.js"
      - "dashboard/site/test/**/*.js"
pre-agent-steps:
  - name: Install dashboard dependencies
    if: ${{ inputs.target_repo == 'githubnext/gh-cao' && (inputs.safe_output_mode || 'review') == 'live' }}
    run: npm ci --prefix dashboard/site --ignore-scripts
  - name: Cache Chromium
    if: ${{ inputs.target_repo == 'githubnext/gh-cao' && (inputs.safe_output_mode || 'review') == 'live' }}
    uses: actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9 # v6.1.0
    with:
      path: ~/.cache/ms-playwright
      key: ${{ runner.os }}-${{ runner.arch }}-playwright-${{ hashFiles('dashboard/site/package-lock.json') }}-chromium
  - name: Install Chromium
    if: ${{ inputs.target_repo == 'githubnext/gh-cao' && (inputs.safe_output_mode || 'review') == 'live' }}
    run: npm exec --prefix dashboard/site -- playwright install --with-deps chromium
  - name: Validate dashboard baseline
    if: ${{ inputs.target_repo == 'githubnext/gh-cao' && (inputs.safe_output_mode || 'review') == 'live' }}
    run: |
      npm --prefix dashboard/site run typecheck
      npm --prefix dashboard/site run lint
      npm --prefix dashboard/site test
      npm --prefix dashboard/site run test:e2e
---

# SelfCare Code Improvement

Read `/tmp/gh-aw/agent/control-precompute.json` first. This worker is authorized only when its precomputed `target_repo` is exactly `githubnext/gh-cao` and its precomputed `safe_output_mode` is `live`. If either condition is false, call `noop` once with the denied scope and stop without inspecting or changing dashboard files.

Grow the JavaScript dashboard component library by extracting one common UI construct per run.

## Scope and evidence

1. Read `AGENTS.md`, `.github/aw/instructions.md`, `runtime/package.json`, `dashboard/site/package.json`, and the relevant source and tests before editing.
2. Inspect `dashboard/site/src/` and select exactly one repeated UI construction with at least two concrete call sites. Prefer a small, high-confidence refactor that measurably reduces duplication.
3. Read the three most recently closed pull requests from this workflow, newest first. Treat merged PRs as positive signals for similar component boundaries. Treat `not planned`, rejecting comments, and requested changes as negative signals; do not repeat those proposals.
4. Preserve rendered behavior, accessibility semantics, public module APIs, and Dashboard Language behavior. Add or update focused unit and end-to-end coverage when the import graph or rendered output changes.
5. Prefer extracting into an existing module under `dashboard/site/src/components/`. If a correct extraction requires a new runtime module and therefore a `runtime/package.json` manifest change, call `noop` because that manifest is outside the allowed change boundary.

## Boundaries

- DO NOT modify files outside `dashboard/site/src/**/*.js` and `dashboard/site/test/**/*.js`.
- DO NOT modify dependency manifests, lockfiles, CI configuration, generated workflow lock files, agent instructions, dashboard specifications, or report producers.
- DO NOT add dependencies, change product behavior, redesign the interface, broaden the selected refactor, or combine unrelated cleanup.
- DO NOT weaken, remove, or skip tests to make the change pass.
- DO NOT create a pull request unless the repeated construct is evidenced at two or more call sites and the extraction produces clearer reuse without speculative abstraction.
- DO NOT create more than one pull request, merge it, or modify an existing contributor pull request.

## Validation and output

The baseline completed before the agent started. After editing, run all of these from `dashboard/site`:

1. `npm run typecheck`
2. `npm run lint`
3. `npm test`
4. `npm run test:e2e`

Review the final diff and scan changed files for secrets. If every command passes, call `create_pull_request` exactly once with a focused draft PR that explains the duplicated call sites, the extracted component boundary, the preserved behavior, and validation results. Include a `### Control Plane` section with correlation ID `${{ inputs.correlation_id }}`, central repository `${{ inputs.central_repo }}`, and control plane run `${{ inputs.control_plane_run_url }}`.

Provide only the unprefixed subject as the safe-output title. The configured `title-prefix` is added automatically; do not repeat it or add a semantically equivalent category prefix.

Call `noop` with a short reason and make no visible write when no non-duplicate candidate meets the evidence threshold, the baseline or post-change validation fails, evidence is insufficient, or the necessary change exceeds the allowed or protected-file boundary.

{{#runtime-import? .github/cao/self-care.md}}
