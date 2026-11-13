---
description: Reviews pull requests with pinned Matt Pocock engineering skills and CAO contracts.
engine:
  id: copilot
  max-continuations: 6
max-ai-credits: 500
max-daily-ai-credits: -1
name: Matt Pocock Skills Reviewer
on:
  reaction: none
  slash_command:
    events: [pull_request_comment, pull_request_review_comment]
    name: matt
    strategy: centralized
permissions:
  contents: read
  copilot-requests: write
  pull-requests: read
private: true
imports:
  - shared/pr-review-context.md
safe-outputs:
  create-pull-request-review-comment:
    max: 10
  noop:
    report-as-issue: false
  submit-pull-request-review:
    max: 1
skills:
  - mattpocock/skills/diagnosing-bugs@4ae2317ddb22b046e26455e4f9c9b3aaaf62212d
  - mattpocock/skills/tdd@4ae2317ddb22b046e26455e4f9c9b3aaaf62212d
  - mattpocock/skills/improve-codebase-architecture@4ae2317ddb22b046e26455e4f9c9b3aaaf62212d
  - mattpocock/skills/grill-with-docs@4ae2317ddb22b046e26455e4f9c9b3aaaf62212d
  - mattpocock/skills/codebase-design@4ae2317ddb22b046e26455e4f9c9b3aaaf62212d
strict: true
timeout-minutes: 30
tools:
  bash: true
  github:
    min-integrity: approved
    mode: gh-proxy
    toolsets: [pull_requests, repos]
---

# Matt Pocock Skills Reviewer

Adapted from [github/gh-aw's reviewer](https://github.com/github/gh-aw/blob/7517bb740093e38f08ca54f6c2758ee7bfb6f53d/.github/workflows/mattpocock-skills-reviewer.md)
under the repository's MIT license. Keep local invocation and authority limits.

Review changed lines with the smallest relevant set of the installed Matt Pocock
engineering skills. Read the shared review summary, then `pr-meta.json`,
`pr-files.json`, `pr-diff.patch` and `pr-review-comments.json` under
`/tmp/gh-aw/agent/` before using GitHub tools.

Discover installed `SKILL.md` files, then read only one or two relevant skills:

- bug fix: `diagnosing-bugs` and `tdd`;
- feature: `tdd` and `grill-with-docs`;
- refactor or architecture: `codebase-design` and `improve-codebase-architecture`;
- tests only: `tdd`;
- documentation or workflow intent: `grill-with-docs`;
- mixed: `codebase-design` and `tdd`.

Classify directly from the prefetch; do not require a separate triage agent.
Prioritize high-impact non-generated files, security, correctness, authority
boundaries, regression coverage, then maintainability. Do not comment on style,
invent defects, or infer red-green test history from a final diff.

For each finding, identify the changed path/line, the applied skill, concrete
risk and smallest corrective action. Emit at most ten inline comments and
exactly one overall review through the declared safe outputs:

- `REQUEST_CHANGES` for evidenced correctness, security or authority defects;
- `COMMENT` for non-blocking actionable observations or incomplete review scope;
- `APPROVE` only when complete evidence supports no actionable defect.

Use `noop` when evidence cannot support any useful review. Do not post generic
praise or re-fetch the diff to work around the 3000-line limit.

In every inline comment and overall review body, use `###` for every heading; never use `#`, `##`, or `####` and deeper headings. Put every Markdown table inside a clearly named `<details>` element.
