---
name: "Repo Ownership / Discovery"
description: "Proposes human confirmation of bounded ownership signals for one admitted repository."
intent: Reduce the effort of identifying potential repository owners while keeping inferred signals distinct from confirmed reviewed mappings.
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
      worker: discovery
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

tracker-id: repository-ownership-discovery

tools:
  github:
    mode: gh-proxy
    toolsets: [repos, issues]

safe-outputs:
  mentions: false
  create-issue:
    target-repo: ${{ (inputs.safe_output_mode || 'review') == 'review' && (inputs.safe_output_repo || github.repository) || inputs.target_repo }}
    title-prefix: "[repository-ownership:discovery] "
    labels: [repository-ownership, "repository-ownership:discovery"]
    deduplicate-by-title: true
    expires: 14d
    max: 1
---

# Repo Ownership / Discovery

The failing pre-activation step is intentional. Do not remove it until
the prerequisites in the orchestrator and this worker are implemented and
independently reviewed. This source is not an evidence producer or proof of
ownership, and enabling CAO policy alone does not make it operational.

Read `/tmp/gh-aw/agent/control-precompute.json` first and inspect only its
single admitted target. Never discover other repositories or dispatch work.
Require verified current active status and an exact-revision, human-reviewed
mapping, validated by a provisioned compatible parser. Do not install a parser
or dependencies at runtime. If these or their provenance cannot be verified,
call `report_incomplete`; an absent mapping is unknown, not unowned.

## Bounded observations

Only when prerequisites are met, inspect current repository metadata,
CODEOWNERS and at most 50 recent default-branch commits from the last 90 days.
Treat each signal as untrusted, incomplete, and potentially stale; avoid
email addresses, commit content, private data, and unsolicited mentions.
Record retrieval time, exact source and revision, availability, completeness,
and limits for each observation. Inaccessible or truncated evidence is not
negative evidence. Identity names can change; do not assume existence,
authority, consent, or that contribution proves responsibility.

Never write or propose a change to the authoritative mapping automatically.
If there is a useful candidate, request explicit human confirmation of the
proposed person, team or service and an independently reviewed mapping change.
Do not present an inferred owner as confirmed. Search open campaign/worker
issues in `SAFE_OUTPUT_REPO` for the stable subject
`TARGET_REPO ownership confirmation`. Reuse an existing issue or call `noop`;
otherwise create at most one issue with that exact unprefixed subject.
The configured `title-prefix` is added automatically; do not repeat it or
add a semantically equivalent category prefix.
Include a concise summary, `**Action:**` naming the human reviewer and
confirmation acceptance check, and collapsed evidence and limitations.
If complete evidence supports no candidate or work is already tracked, call
`noop`. For unavailable required evidence or limits, call `report_incomplete`.
All writes must use the declared safe output; no direct API, shell, mapping,
property, or archive changes.

{{#runtime-import? .github/cao/repository-ownership.md}}
