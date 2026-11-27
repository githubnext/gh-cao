---
name: "Repo Ownership / Enforcement"
description: "Requests human review of verified ownership mapping maintenance for one repository."
intent: Help maintainers keep reviewed repository ownership current without closing or changing ownership based on missing evidence.
max-ai-credits: 150
max-daily-ai-credits: -1
timeout-minutes: 15

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
        type: string
  permissions:
    contents: read
    actions: read

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
    pre-steps:
      - name: Block ownership campaign activation
        run: |
          echo "::error::Repository ownership controls are not yet implemented and reviewed."
          exit 1

if: needs.pre_activation.outputs.cao_authorized == 'true'

imports:
  - uses: shared/control.md
    with:
      campaign: repository-ownership
      role: worker
      worker: enforcement
      read_repository: ${{ inputs.target_repo }}
      read_actions: read
      read_contents: read
      read_issues: read

permissions:
  contents: read
  actions: read
  issues: read
  copilot-requests: write

engine: copilot
strict: true
network:
  allowed: [defaults, github]

concurrency:
  group: "${{ github.workflow }}-${{ inputs.target_repo }}"
  job-discriminator: ${{ github.run_id }}
  cancel-in-progress: true

tracker-id: repository-ownership-enforcement

tools:
  github:
    mode: gh-proxy
    toolsets: [repos, issues]

safe-outputs:
  mentions: false
  create-issue:
    target-repo: ${{ (inputs.safe_output_mode || 'review') == 'review' && (inputs.safe_output_repo || github.repository) || inputs.target_repo }}
    title-prefix: "[repository-ownership:enforcement] "
    labels: [repository-ownership, "repository-ownership:enforcement"]
    deduplicate-by-title: true
    expires: 14d
    max: 1
---

# Repo Ownership / Enforcement

The failing pre-activation step is intentional. Do not remove it until
the prerequisites in the orchestrator and this worker are implemented and
independently reviewed. A compiled workflow is not an evidence producer and
policy enablement alone does not authorize or implement enforcement.

Read `/tmp/gh-aw/agent/control-precompute.json` first and assess only the
admitted target. Never discover repositories or dispatch work. Require a
provisioned verified parser, exact-revision mapping review provenance,
verified current repository activity, and verified identity and exemption
semantics. Never install dependencies at runtime or rely on inferred ownership.
If evidence is absent, inaccessible, malformed, stale, ambiguous, or lacks
trusted provenance, call `report_incomplete`; it is not proof of no owner.

Only a validated mapping at the exact control workflow revision can establish
the recorded mapping. A due date at or before the assessment time means review
is due; an exemption declaration alone is not authority to suppress a finding.
For a verified stale mapping request a human review, never automatic removal.
Inferred candidates require explicit human confirmation and a separately
reviewed mapping change. No issue closure is authorized by missing or stale
evidence. Do not archive repositories, change properties, modify mappings,
or call any direct write API. Those capabilities are proposal-only or absent.

Search open campaign/worker issues in `SAFE_OUTPUT_REPO` for the stable subject
`TARGET_REPO ownership review`. Reuse an existing issue or call `noop`;
otherwise create at most one issue with that exact unprefixed subject, only
when all prerequisites are satisfied and human review is due.
The configured `title-prefix` is added automatically; do not repeat it or
add a semantically equivalent category prefix.
Include a concise summary, `**Action:**` naming the reviewer and acceptance check,
and collapsed mapping provenance, time, evidence and limitations. A current
validated mapping with no outstanding review is a `noop`. The configured title
deduplication is not proof of safe resolution; no closure safe output exists.

{{#runtime-import? .github/cao/repository-ownership.md}}
