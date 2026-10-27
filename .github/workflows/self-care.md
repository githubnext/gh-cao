---
name: "SelfCare"

run-name: "${{ github.event_name == 'schedule' && 'SelfCare · scheduled' || format('SelfCare · {0} · {1}', inputs.target_repo || github.repository, inputs.safe_output_mode || 'review') }}"

max-ai-credits: 200
max-daily-ai-credits: -1
timeout-minutes: 15

concurrency:
  group: "${{ github.workflow }}"
  job-discriminator: ${{ github.run_id }}
  cancel-in-progress: true

on:
  schedule: every 20 minutes
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
  CAO_DEFAULT_TARGET_REPOSITORY: ${{ github.repository }}
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
      campaign: self-care
      role: orchestrator
      read_repository: ${{ github.repository }}
      dispatch_max: 20
      orchestrator_credits: 200
      worker_credits_per_target: 4600
      read_actions: read
      read_contents: read

permissions:
  contents: read
  actions: read
  copilot-requests: write

engine: copilot

strict: true

tools:
  github:
    mode: remote
    min-integrity: approved
    toolsets: [repos, actions]

network:
  allowed:
    - defaults
    - github

safe-outputs:
  dispatch-workflow:
    workflows: [self-care-accessibility-checker, self-care-agent-discoverability, self-care-code-improvement, self-care-dashboard-data-schema, self-care-dashboard-debug-logging, self-care-dashboard-performance, self-care-data-acquisition-audit, self-care-dashboard-language-refactor, self-care-dashboard-review, self-care-docs-build-time-investigator, self-care-docs-maintainer, self-care-documentation-discoverability, self-care-glossary, self-care-open-source-failures, self-care-pages-health, self-care-primer-brand-checker, self-care-reactive-ui-expert, self-care-server-go-logging, self-care-server-soc2, self-care-specs-maintainer]
    max: 20
  threat-detection: false

source: githubnext/gh-cao@a4b937e2ee4e540d3ccce1377f8943315670f33d
---

# SelfCare

## Discovery

This operation is exclusively for `githubnext/gh-cao`. Select that repository only when its precomputed candidate mode is `live`. Treat every other repository and every non-live candidate as ineligible, regardless of apparent need, and record the skip reason in the standard report. Never promote a `review` candidate to `live`.

The single eligible repository contains the documentation site and dashboard maintained by the SelfCare workers. Do not discover, rank, or dispatch work to any other repository.

## Workers

- `self-care-accessibility-checker`: audits the rendered documentation site with axe-core, keyboard traversal, and browser evidence, then publishes one prioritized accessibility issue.
- `self-care-agent-discoverability`: starts from the published `/cao/` route and verifies that procedural, bounded metric, targeted query, and bulk-analysis tasks choose skills, Activity artifacts, MCP, and SQLite without UI scraping.
- `self-care-code-improvement`: extracts one evidenced duplicated dashboard UI construct into a tested reusable component and opens one focused draft pull request.
- `self-care-dashboard-performance`: rotates through trace-backed CFO, CTO, and CSO Lighthouse bottlenecks and opens one focused draft pull request.
- `self-care-dashboard-debug-logging`: instruments one evidenced dashboard JavaScript subsystem with category-filtered, privacy-preserving debug logging and opens one focused draft pull request.
- `self-care-dashboard-data-schema`: downloads every JSON file advertised by the deployed Pages manifest and opens one focused draft pull request when the inferred pseudo schemas change.
- `self-care-dashboard-review`: uses deterministic checks and CFO, CSO, and CTO browser journeys to assess dashboard correctness, decision support, efficiency, and usability.
- `self-care-docs-build-time-investigator`: analyzes Documentation Pages workflow timing evidence and opens one issue with a non-repeating caching or dashboard build-speed improvement.
- `self-care-docs-maintainer`: scans recent merged pull request diffs and ADR changes, verifies them against current repository files, and opens one focused draft pull request for an evidenced documentation correction.
- `self-care-documentation-discoverability`: audits the deployed `llms.txt` entry point against ten bounded development tasks and maintains one issue for deterministic routing defects.
- `self-care-glossary`: scans recent merged pull requests and default-branch code changes, then opens one focused draft pull request when repository evidence supports a glossary update.
- `self-care-data-acquisition-audit`: reviews gh-aw logs, GitHub API access, predownloads, indexing, and caching, then opens one focused draft pull request when the acquisition audit is stale.
- `self-care-dashboard-language-refactor`: replaces one over-specialized dashboard view with tested reusable subcomponents configured through Dashboard Language and opens one focused draft pull request.
- `self-care-open-source-failures`: scans the dashboard activity snapshot for clustered failures across represented public projects and files one digest plus focused remediation issues.
- `self-care-pages-health`: audits every deployed dashboard view under desktop, mobile, and low-bandwidth profiles and opens a focused draft PR with the highest-confidence JavaScript quick wins it can validate.
- `self-care-primer-brand-checker`: audits the dashboard against retrieved Primer brand guidance and opens one focused draft pull request when an evidenced presentational fix is available.
- `self-care-reactive-ui-expert`: maintains the reactive UI skill, migrates JavaScript-produced view sources to request-scoped dashboard queries, reviews recent dashboard JavaScript changes, and replaces evidenced imperative DOM updates with owned reactive elements, data binding, and effects.
- `self-care-server-go-logging`: refactors one Go server subsystem with privacy-preserving internal logging and focused unit tests that use real code without mocks, then opens one focused draft pull request.
- `self-care-server-soc2`: reassesses bounded Go server SOC 2 readiness daily, publishes evidence even when a remediation PR remains open, and proposes at most three compatible verified code fixes in one focused draft PR.
- `self-care-specs-maintainer`: scans recently merged pull requests for behavior changes missing from the normative specifications and opens one focused draft pull request that fixes them in W3C specification style.

For a selected `live` target, dispatch every non-cadence-limited worker. Dispatch `self-care-agent-discoverability`, `self-care-dashboard-data-schema`, `self-care-data-acquisition-audit`, `self-care-docs-maintainer`, `self-care-glossary`, and `self-care-specs-maintainer` only when no run of the respective workflow is in progress or started during the preceding 24 hours. Inspect at most the ten most recent runs of each workflow to make this decision. Dispatch `self-care-documentation-discoverability` only when no run of that workflow is queued, in progress, or started during the preceding seven days. Inspect at most its ten most recent runs. Dispatch `self-care-pages-health` only when no run of that workflow is queued, in progress, or started during the preceding six hours. Inspect at most the 20 most recent Pages Health workflow runs to make this decision. If any run history is unavailable or ambiguous, fail closed by not dispatching the affected cadence-limited worker and record the incomplete cadence check. Never dispatch any worker in review mode or for another repository.

## Dispatch execution

Dispatch `self-care-server-soc2` only when no run is queued, in progress, or
started during the preceding 24 hours. Inspect at most its ten most recent
runs, including failed and cancelled runs. If history is unavailable,
truncated before the time boundary, or ambiguous, fail closed by deferring
this worker and recording the incomplete cadence check. Do not gate its
assessment dispatch on an open remediation PR. Include its daily eligibility
and confirmed dispatch/defer reason in `Outcome`.

Submit each dispatch through its configured safe-output tool and wait for the complete result. When using the supported CLI transport, pass the standard dispatch envelope as one inline JSON argument: `safeoutputs <tool_name> '<JSON>'`. Never pipe `safeoutputs` output to `head` or another truncating consumer, run dispatch calls in the background, or treat bridge startup logs as confirmation. Count a dispatch only after the tool confirms that its safe-output request was recorded; this confirms dispatch intent, not downstream worker completion.

If a dispatch fails or its result is unconfirmed, record that worker as deferred and call the configured `report_incomplete` safe-output tool without retrying the dispatch. If no workers are eligible, call the configured `noop` safe-output tool. A prose report alone is not a terminal safe output, and must not claim that an unconfirmed worker was dispatched.

## Completion

Finish with the standard orchestrator report inherited from `shared/control.md`. Preserve `Scope`, `Repository Decisions`, `Workers`, `Dispatches`, and `Outcome`, including every standard field. Use exact precomputed totals for repositories scanned and distinguish eligible, selected, skipped, and deferred repositories. Use `0`, `none`, or `not applicable` for every empty field.

In `Outcome`, additionally state whether the sole authorized target was selected and its resolved mode, whether every non-cadence-limited SelfCare worker was dispatched when the mode was `live`, whether the agent discoverability, dashboard data schema, data acquisition audit, docs maintainer, glossary, and specs maintainer workers were daily-eligible and dispatched when applicable, whether the documentation discoverability worker was weekly-eligible and dispatched when applicable, and whether the Pages Health worker was six-hour-eligible and dispatched when applicable.

{{#runtime-import? .github/cao/self-care.md}}
