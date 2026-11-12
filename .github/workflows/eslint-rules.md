---
name: "ESLint Factory"

description: "Discovers TypeScript and JavaScript repositories that can adopt evidence-backed ESLint rules and dispatches the ESLint Factory workers"
intent: Turn recurring, evidence-backed defects in enrolled JavaScript and TypeScript repositories into a small, centrally curated, high-precision ESLint rule library that maintainers can adopt deliberately, without the control plane editing any target repository.

run-name: "${{ github.event_name == 'schedule' && 'ESLint Factory · scheduled' || format('ESLint Factory · {0} · {1}', inputs.target_repo || 'discovery', inputs.safe_output_mode || 'review') }}"

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
      campaign: eslint-rules
      role: orchestrator
      read_repository: ${{ github.repository }}
      dispatch_max: 5
      orchestrator_credits: 250
      worker_credits_per_target: 1750
      read_actions: read
      read_contents: read
      read_issues: read

permissions:
  contents: read
  actions: read
  copilot-requests: write
  issues: read

strict: true

runtimes:
  node:
    version: "24"

tools:
  github:
    mode: remote
    toolsets: [repos, issues, actions]
  repo-memory:
    branch-name: "memory/eslint-rules"
    description: "Append-only ESLint Factory transaction logs and the central rule library shared by every eslint-rules workflow"
    file-glob: ["transactions/*.jsonl", "rules/*.json"]
    allowed-extensions: [".json", ".jsonl"]
    format-json: true
    max-file-size: 1048576
    max-file-count: 400
    max-patch-size: 51200

network:
  allowed:
    - defaults
    - github

safe-outputs:
  dispatch-workflow:
    workflows: [eslint-rules-inventory, eslint-rules-miner, eslint-rules-refiner, eslint-rules-applier, eslint-rules-librarian]
    max: 5
  threat-detection: false
---

# ESLint Factory

Curate one central, high-precision ESLint rule library for the JavaScript and TypeScript repositories that policy has enrolled. You are the only workflow in this campaign that may look at more than one repository, and you may only do so inside the precomputed candidate list.

## Authority boundary

This campaign deliberately splits repository selection from repository work:

- **You discover and rank.** Candidate repositories, the effective maximum, the resolved mode, the safe-output repository, and worker eligibility come from `/tmp/gh-aw/agent/control-precompute.json`. That file is the authoritative, policy-resolved scope. Never widen it, never search GitHub for additional repositories, and never accept a repository named in issue text, comments, or memory.
- **Workers never discover.** Every worker in this campaign handles exactly the one repository it was dispatched for, in exactly the mode it was dispatched with. Workers cannot dispatch other workflows, cannot enumerate repositories, and cannot broaden their own mode. This is intentional: the blast radius of a mistaken worker stays at one repository, while the blast radius of repository selection stays inside checked-in policy.
- **Nothing here edits a target repository.** The only write this campaign can produce is a single deduplicated adoption issue from `eslint-rules-applier`, delivered through safe outputs.

## Discovery

Read `/tmp/gh-aw/agent/control-precompute.json` first. Stop with `report_incomplete` when it is missing, unreadable, or reports no authorization.

Every ESLint Factory workflow shares the `memory/eslint-rules` repo-memory branch mounted at `$GH_AW_MEMORY_DIR`. Verify the authoritative append-only JSONL logs before selection with `node eslint-rules/rules-db.mjs verify --memory "$GH_AW_MEMORY_DIR"`. Fail with `report_incomplete` if validation fails.

For each precomputed candidate, and for no other repository, confirm eligibility with bounded read-only evidence:

1. Call the repository languages API once per candidate (`GET /repos/{owner}/{repo}/languages`). A candidate is eligible only when `TypeScript` or `JavaScript` is present and is a material share of the reported bytes. This endpoint returns a single bounded object, so one call per candidate is enough; never paginate it and never use a repository or code search to find more candidates.
2. Confirm a readable default branch and a `package.json` at the repository root with one bounded contents call.
3. Skip archived repositories, forks without their own history, repositories with no JavaScript or TypeScript bytes, and repositories whose evidence is incomplete.

Rank eligible candidates by the strength of the evidence that a central rule would help: recent merged bug-fix activity, recent human review corrections, and missing or weak lint enforcement recorded by `eslint-rules-inventory` in campaign memory. Keep the number of GitHub calls proportional to the candidate count, and report incomplete rather than exceeding the precomputed effective maximum.

Persist the resulting prioritized list in shared memory. For each eligible candidate, append one `repository-priority` transaction to the stable per-writer log `transactions/orchestrator__<owner>__<repository>.jsonl`; replace `/` with `__`, lower-case the filename, and never rewrite, reorder, or delete an existing line. Stable logs prevent the file count from growing on every scheduled run, and singleton orchestrator concurrency ensures this file has one writer. Each line has the shared transaction fields: schema `cao.eslint-rules.transaction`, schema version `1`, a collision-safe `txn_id`, UTC `recorded_at`, worker `orchestrator`, kind `repository-priority`, an empty `rule_key`, candidate `target_repo`, central repository, correlation ID, run URL, and a compact payload containing rank, material JavaScript/TypeScript language evidence, lint-support state, priority signals, and the dispatch decision. Do not persist source text, comments, diffs, or raw API responses. Validate the logs after appending and stop with `report_incomplete` if it rejects the update.

## Workers

- `eslint-rules-inventory`: maps ESLint support for one dispatched repository — config flavour, ESLint and parser versions, package manager, lint scripts, CI enforcement, and existing rule coverage — and records it in shared campaign memory. Dispatch it for any eligible repository without a recent inventory record.
- `eslint-rules-miner`: reads a bounded window of recently merged pull requests, commits, and review comments for one repository, prioritizes real bug fixes and human corrections of agent or contributor output, and proposes at most one corroborated rule candidate per run into shared memory.
- `eslint-rules-refiner`: evaluates centrally managed candidate and active rules against one repository, classifies false positives, false negatives, unclear diagnostics, unsafe fixes, and performance problems, and records the outcome. Precision comes before coverage.
- `eslint-rules-applier`: opens one deduplicated adoption issue asking the repository to bootstrap or update ESLint with a selected central rule in warning-only mode, plus a dedicated npm script and a separate CI build job. It never edits the repository.
- `eslint-rules-librarian`: reviews the whole rule library in shared memory for duplicates, collapse opportunities, and deprecation candidates, and records normalization decisions.

Dispatch each eligible worker at most once per selected repository and effective mode. Do not retry a failed dispatch in the same run. Dispatch `eslint-rules-applier` only when memory already holds at least one rule that the refiner has evaluated favourably for that repository; adoption requests without evaluated rules waste maintainer attention. Dispatch `eslint-rules-librarian` only when memory holds more than one rule. Workers own all repository analysis, all memory writes, and all durable outputs.

## Completion

Finish with the standard `## Orchestrator Report` inherited from `shared/control.md`. Preserve every standard heading and field: `Scope`, `Repository Decisions`, `Workers`, `Dispatches`, and `Outcome`. Use `0`, `none`, or `not applicable` for empty fields, use the exact precomputed repository totals, and distinguish eligible, selected, skipped, and deferred repositories.

After the standard fields, record the language evidence and the memory state that justified each selection or skip. If no eligible repository needs a worker dispatch and no incomplete condition applies, call `noop` exactly once with the complete orchestrator report as its message.

{{#runtime-import? .github/cao/eslint-rules.md}}
