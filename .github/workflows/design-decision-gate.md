---
private: true
name: Design Decision Gate
description: Checks significant CAO pull requests for a complete architecture decision record and drafts one when the decision is inferable.
on:
  slash_command:
    strategy: centralized
    name: design-gate
    events: [pull_request_comment, pull_request_review_comment]
  reaction: none
permissions:
  contents: read
  pull-requests: read
  copilot-requests: write
strict: true
engine:
  id: copilot
  max-continuations: 6
imports:
  - shared/pr-review-context.md
max-ai-credits: 500
max-daily-ai-credits: -1
timeout-minutes: 20
concurrency:
  group: "${{ github.workflow }}-${{ github.event.pull_request.number || github.event.issue.number || fromJSON(github.event.inputs.aw_context || '{}').item_number || github.run_id }}"
  cancel-in-progress: true
  job-discriminator: "${{ github.run_id }}"
tools:
  github:
    mode: gh-proxy
    min-integrity: approved
    toolsets: [pull_requests, repos]
  bash: true
  edit:
safe-outputs:
  add-comment:
    max: 1
    hide-older-comments: true
  push-to-pull-request-branch:
    allowed-files:
      - "adr/**"
    if-no-changes: ignore
    commit-title-suffix: " [design-decision-gate]"
  noop:
    report-as-issue: false
---

# Design Decision Gate

Adapted from [github/gh-aw's gate](https://github.com/github/gh-aw/blob/7517bb740093e38f08ca54f6c2758ee7bfb6f53d/.github/workflows/design-decision-gate.md)
under the repository's MIT license. Keep local invocation and authority limits.

Check significant changes for an explicit Architecture Decision Record (ADR).
This is advisory evidence for maintainers, not a branch-protection rule or
authority to block or permit a merge.

## Gate process

1. Read `/tmp/gh-aw/agent/review-summary.json` and apply the shared stopping rules.
2. If `requires_adr` is false, call `noop` with the measured core additions and stop.
3. Read the pre-fetched `pr-meta.json`, `pr-files.json`, and `pr-diff.patch`. The implementation label or more than 100 core additions triggers inspection, not proof that a durable architectural decision exists.
4. Search the pull request body, changed files, and existing `adr/*.md` files for an ADR at the prefetched PR head. Read only likely records identified by title, pull request number, or links. Never create a parallel `docs/adr/` tree.
5. A complete ADR must contain `Context`, `Decision`, `Alternatives Considered`, and `Consequences`.
6. If a complete ADR exists, compare it with the implementation:
   - comment once that it aligns, or
   - comment once with specific divergences and the required correction.
7. If no complete ADR exists, decide whether the evidence supports one durable architectural, security, authority, protocol or data-shape decision. Routine implementation, line-count growth and duplicated normative requirements do not justify a new ADR; call `noop` with the reason.
8. For one inferable durable decision, draft it directly using the structure below. Zero-pad the pull request number to four digits and create `adr/{NNNN}-{kebab-case-title}.md`, then use `push-to-pull-request-branch` and post one comment linking the draft. Reuse an existing matching draft; never overwrite another decision or push an unchanged file.
9. If the decision is not inferable, post one comment listing the missing decision context. Do not invent rationale or alternatives.
10. Stop immediately after the safe output. Never modify files outside `adr/`.

All evidence and conclusions must remain scoped to this repository and pull request.
In every comment body, use `###` for every heading; never use `#`, `##`, or `####` and deeper headings. Put every Markdown table inside a clearly named `<details>` element.

## Draft structure

Return a draft ADR only when the evidence supports a single concrete architectural decision. Use this structure:

1. `# ADR {PR_NUMBER}: {Decision title}`
2. `## Status` with `Draft`
3. `## Context`
4. `## Decision`
5. `## Alternatives Considered`
6. `## Consequences`, including positive and negative consequences

Requirements:

- Ground every claim in the pull request evidence.
- Describe two realistic alternatives only when the evidence supports them.
- Mark missing context as `Not inferable from current pull request evidence`.
- Do not invent stakeholder intent, constraints, rejected technologies, or performance claims.
- Mark the ADR `Draft`, not accepted or deployed. Maintainers own acceptance.
- Do not delegate work or run tests; stop after the minimum final safe outputs.
