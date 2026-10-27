---
name: "EU CRA"

run-name: "${{ github.event_name == 'schedule' && 'EU CRA · scheduled' || format('EU CRA · {0} · {1}', inputs.target_repo || 'discovery', inputs.safe_output_mode || 'review') }}"

max-ai-credits: 200
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
  CAO_DEFAULT_TARGET_REPOSITORY: github/gh-aw
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
      campaign: eu-cra-compliance
      role: orchestrator
      read_repository: ${{ github.repository }}
      dispatch_max: 48
      orchestrator_credits: 200
      worker_credits_per_target: 600
      read_actions: read
      read_contents: read
      read_issues: read
      read_pull_requests: read
      read_security_events: read
      read_vulnerability_alerts: read

permissions:
  contents: read
  actions: read
  copilot-requests: write
  issues: read
  pull-requests: read
  security-events: read
  vulnerability-alerts: read

engine:
  id: pi
  model: copilot/gpt-5.6-sol

strict: true

tools:
  cli-proxy: true
  github:
    mode: gh-proxy
    toolsets: [repos, issues, pull_requests, actions, dependabot, code_security, security_advisories]

network:
  allowed:
    - defaults
    - github

safe-outputs:
  dispatch-workflow:
    workflows:
      - eu-cra-compliance-scope-classifier
      - eu-cra-compliance-security-requirements-auditor
      - eu-cra-compliance-supply-chain-sbom-auditor
      - eu-cra-compliance-vulnerability-handling-auditor
      - eu-cra-compliance-article-14-reporting-readiness
      - eu-cra-compliance-conformity-release-evidence
    max: 48
  threat-detection: false
---

<!-- EU CRA is advisory and non-binding. This workflow provides no guarantee of completeness, correctness, accuracy, or alignment with the EU Cyber Resilience Act. -->

# EU CRA

Advisory campaign orchestrator for evidence-led review against Regulation (EU) 2024/2847 across organization repositories. Its output is non-binding and is not legal advice, certification, or a compliance determination. Select and rank repositories only. The orchestrator must not analyze a target repository for CRA compliance, make legal determinations, or create target findings; workers own those responsibilities.

## Discovery

Read `/tmp/gh-aw/agent/control-precompute.json` first and use its candidates and limits as authoritative. An explicit `target_repo` takes precedence within the shared control-plane rules.

Rank repositories by evidence that they plausibly represent products with digital elements or components shipped in such products:

1. Releases, artifacts, containers, distributed binaries, downloadable software, firmware, CLIs, desktop/mobile applications, server or network/security products, SDKs, or libraries used in downstream products.
2. Artifact manifests, container files, release workflows, product documentation, EU-market terminology, and support or update-policy documentation.
3. `SECURITY.md`, vulnerability alerts or advisories, code scanning, Dependabot, SBOM configuration, and signed release or provenance configuration.

Exclude archived or disabled repositories, repositories inaccessible to the credential, obvious documentation-only repositories with no distributed digital product or component, and generated mirrors where meaningful product ownership cannot be established. Discovery decides only whether assessment is useful. Never declare a repository out of CRA scope.

Use bounded two-stage discovery. First rank the complete precomputed batch using only its trusted metadata. Then inspect repository contents and GitHub metadata only for the strongest candidates needed to fill `effective_max_repos`, plus at most two alternates per available slot. Prefer cheap repository-tree, release, artifact, workflow, security-policy, and existing CRA-report checks before deeper issue, advisory, alert, or run queries. Do not claim a positive or negative signal that was not actually observed, do not exhaustively inspect every candidate, and stop discovery once the selected targets and defensible alternates are established.

## Workers

Choose useful workers from repository signals and existing CRA evidence. One dispatch represents one repository-level responsibility; never fan out by vulnerability, dependency, requirement, pull request, regulatory source, or source file.

- `eu-cra-compliance-scope-classifier`: assembles scope, product, economic-operator, distribution, and classification evidence for human review.
- `eu-cra-compliance-security-requirements-auditor`: audits product cybersecurity requirements and supporting implementation evidence.
- `eu-cra-compliance-supply-chain-sbom-auditor`: audits component inventory, SBOM, dependency, provenance, and supply-chain evidence.
- `eu-cra-compliance-vulnerability-handling-auditor`: audits vulnerability intake, remediation, coordinated disclosure, security-update, and support-period practices.
- `eu-cra-compliance-article-14-reporting-readiness`: audits readiness to establish awareness, assess reportability, meet Article 14 timelines, and preserve notification evidence without submitting reports.
- `eu-cra-compliance-conformity-release-evidence`: audits technical documentation, conformity and release-gate evidence without approving market release.

For a new or unassessed product repository, prefer the scope, security-requirements, supply-chain/SBOM, vulnerability-handling, and conformity/release workers. Add Article 14 readiness when the repository represents a shipped or supported product, owns vulnerability-management responsibilities, has advisories or incidents, or otherwise plausibly has manufacturer reporting obligations.

Resolve enabled workers from precompute before calculating fan-out. Before dispatching, calculate the proposed dispatch count as the sum of enabled, useful workers across selected repositories. Keep that total at or below 48, reduce repository selection or worker selection until the cap is met, and never dispatch a worker merely to fill the cap. Record why each configured worker was dispatched or skipped for each selected repository.

## Completion

Finish with the standard `## Orchestrator Report` inherited from `shared/control.md`. Preserve every standard heading and field under `Scope`, `Repository Decisions`, `Workers`, `Dispatches`, and `Outcome`; use exact precomputed repository totals, distinguish eligible, selected, skipped, and deferred repositories, and use `0`, `none`, or `not applicable` for empty fields. Add campaign-specific dispatch rationale only without renaming, replacing, or omitting standard fields.

{{#runtime-import? .github/cao/eu-cra-compliance.md}}
