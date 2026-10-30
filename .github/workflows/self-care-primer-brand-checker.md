---
emoji: "🎨"
name: "SelfCare / Primer"
description: Audit of the Central Agentic Ops dashboard against Primer brand guidance, opening a draft PR with focused fixes
intent: Keep the Central Agentic Ops dashboard aligned with current Primer brand guidance through small, evidenced presentational fixes.
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
  skip-if-match: 'is:pr is:open "gh-aw-workflow-id: self-care-primer-brand-checker" in:body'
  permissions:
    contents: read
    actions: read

checkout:
  repository: ${{ inputs.target_repo }}
  github-token: ${{ vars.GH_AW_GITHUB_AUTH_MODE == 'pat' && secrets[fromJSON(vars.GH_AW_GITHUB_READ_PAT_REPOSITORIES || '{}')[inputs.target_repo]] || vars.GH_AW_GITHUB_AUTH_MODE != 'pat' && secrets.GH_AW_GITHUB_READ_PAT || vars.GH_AW_GITHUB_AUTH_MODE != 'pat' && secrets.GH_AW_GITHUB_TOKEN || secrets.GITHUB_TOKEN }}
  fetch-depth: 0
  fetch: ["*"]
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
      worker: primer-brand-checker
      read_repository: ${{ inputs.target_repo }}
      read_actions: read
      read_contents: read
      read_pull_requests: read

permissions:
  contents: read
  actions: read
  copilot-requests: write
  pull-requests: read
network:
  allowed:
    - defaults
    - github
    - node
    - primer.style
tools:
  github:
    mode: remote
    min-integrity: approved
    toolsets: [repos, actions]
  cli-proxy: true
safe-outputs:
  create-pull-request:
    target-repo: ${{ (inputs.safe_output_mode || 'review') == 'review' && (inputs.safe_output_repo || github.repository) || inputs.target_repo }}
    title-prefix: "Primer branding: "
    draft: true
    max: 1
    if-no-changes: ignore
    allowed-files:
      - "dashboard/site/index.html"
      - "dashboard/site/src/*.js"
      - "dashboard/site/src/**/*.js"
      - "dashboard/site/test/**/*.js"
mcp-servers:
  primer-brand:
    command: npx
    args: ["-y", "@primer/brand-mcp@0.74.0"]
    allowed: ["*"]
strict: true
max-ai-credits: 400
max-daily-ai-credits: -1
timeout-minutes: 25
tracker-id: self-care-primer-brand-checker
run-name: "SelfCare Primer brand · ${{ inputs.target_repo }} · ${{ inputs.safe_output_mode || 'review' }}"
concurrency:
  group: "${{ github.workflow }}-${{ inputs.target_repo }}"
  job-discriminator: ${{ github.run_id }}
  cancel-in-progress: true
runtimes:
  node:
    version: "24"
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
---

# SelfCare Primer Brand Checker

Read `/tmp/gh-aw/agent/control-precompute.json` first. This worker is authorized only when its precomputed `target_repo` is exactly `githubnext/gh-cao` and its precomputed `safe_output_mode` is `live`. If either condition is false, call `noop` once with the denied scope and stop without auditing or changing files.

You are a **front-end designer** responsible for keeping the Central Agentic Ops dashboard aligned with GitHub's Primer brand guidance.

## Context

This repository contains a static dashboard renderer under `dashboard/site/`.

- `dashboard/site/index.html` — page shell and initial markup
- `dashboard/site/src/styles.js` — dashboard theme tokens and component styles
- `dashboard/site/src/presenter.js` and `dashboard/site/src/components/` — generated UI markup
- `dashboard/site/test/` — unit and browser coverage

Use the complete dashboard validation suite listed in Step 3 after making changes.

## Instructions

### Step 1: Gather guidance

1. Run `primer-brand --help` to list the tools mounted from the `primer-brand` MCP server, then use the relevant tools to fetch current brand guidance for color, typography, spacing, and voice/tone.
2. Record the specific guidance you retrieved. Cite it in the pull request body.

### Step 2: Audit the dashboard

Review `dashboard/site/index.html`, `dashboard/site/src/styles.js`, and the markup produced by `dashboard/site/src/presenter.js` and `dashboard/site/src/components/` for deviations from the retrieved guidance, focusing on:

- **Color**: hard-coded values that should use Primer variables or brand tokens; off-brand gradients or accents; light/dark mode parity.
- **Typography**: font families, weights, sizes, and line heights that diverge from the brand type scale.
- **Spacing and layout**: ad-hoc values where Primer spacing tokens exist.
- **Voice and tone**: headings, button labels, and helper text that do not match brand voice guidance.
- **Accessibility**: contrast ratios required by the brand guidance.

Prioritize the highest-impact, lowest-risk deviations. A focused change set is better than a sweeping one.

### Step 3: Apply fixes

1. Modify only existing files under the allowed dashboard paths. Prefer Primer variables and tokens over new hard-coded values. Do not flatten every gradient or highlight by default: tasteful shine is allowed when every color comes from Primer tokens, Primer variables, or existing site accent variables in one aligned color family, and it passes contrast.
2. Run all dashboard validation commands:

   ```bash
   npm --prefix dashboard/site run typecheck
   npm --prefix dashboard/site run lint
   npm --prefix dashboard/site test
   npm --prefix dashboard/site run test:e2e
   npm --prefix dashboard/site run build
   ```

3. If a fix breaks validation and cannot be resolved cleanly, revert that fix and describe it in the pull request body as a follow-up.

### Step 4: Open a pull request

Call `create_pull_request` exactly once with:

- A short, unprefixed summary as the title; the configured `title-prefix` is added automatically as `Primer branding: `. Do not repeat it or add a semantically equivalent category prefix.
- A body containing:
  - What changed, grouped by category (color, typography, spacing, voice, accessibility)
  - The specific brand guidance from the MCP server that motivated each change
  - Any deviations found but deliberately not fixed, and why
  - Confirmation that every validation command passed
  - A `### Control Plane` section with correlation ID `${{ inputs.correlation_id }}`, central repository `${{ inputs.central_repo }}`, and control plane run `${{ inputs.control_plane_run_url }}`

### Step 5: Skip when the dashboard is already on-brand

Always finish by calling exactly one safe-output tool. If the audit finds no meaningful deviations or no improvement is needed for any other reason, call `noop` once with a concise plain-text reason and do not open a pull request. Never finish with only a textual response.

## Rules

- Do NOT invent brand guidance. Every change must trace back to guidance returned by the `primer-brand` MCP server.
- Do NOT restructure the dashboard, rename files, create files, or change application logic. This workflow is presentational only.
- Do NOT remove existing functionality, tests, or accessibility affordances such as ARIA attributes and keyboard handling.
- Do NOT add or update dependencies.
- Do NOT change `dashboard/aw.yml`, report producers, documentation, agentic workflows, or files outside the configured safe-output allowlist.
- Do NOT open a pull request when any validation command fails.
- Keep the change set small enough for a human to review in one sitting.

{{#runtime-import? .github/cao/self-care.md}}
