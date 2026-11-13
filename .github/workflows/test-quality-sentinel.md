---
private: true
name: Test Quality Sentinel
description: Reviews changed Go, Node, Vitest and Playwright tests for CAO behavioral contracts rather than test-count growth.
on:
  reaction: none
  slash_command:
    strategy: centralized
    name: test-quality
    events: [pull_request_comment, pull_request_review_comment]
permissions:
  contents: read
  pull-requests: read
  copilot-requests: write
engine:
  id: copilot
  max-continuations: 6
max-ai-credits: 500
max-daily-ai-credits: -1
timeout-minutes: 20
strict: true
imports:
  - shared/pr-review-context.md
tools:
  bash: true
  github:
    min-integrity: approved
    mode: gh-proxy
    toolsets: [pull_requests, repos]
safe-outputs:
  add-comment:
    max: 1
    hide-older-comments: true
  submit-pull-request-review:
    max: 1
  noop:
    report-as-issue: false
---

# Test Quality Sentinel

Adapted from [github/gh-aw's sentinel](https://github.com/github/gh-aw/blob/7517bb740093e38f08ca54f6c2758ee7bfb6f53d/.github/workflows/test-quality-sentinel.md)
under the repository's MIT license. Keep local invocation and authority limits.

Analyze new and modified tests for genuine behavioral coverage. High test counts
and coverage percentages alone do not establish correctness.

## Evidence and scope

Read the shared review summary first, then `pr-meta.json`, `pr-files.json`,
`test-diff.patch`, `pr-diff.patch` and `pr-review-comments.json` under
`/tmp/gh-aw/agent/`. Identify added, modified, renamed and deleted tests from file
metadata, not just added function signatures. Body-only edits matter.

Supported tests include Go `*_test.go`, Node `*.test.mjs`/`*.test.cjs`, Vitest
`*.test.js`/`*.test.ts`, Playwright `*.spec.js`/`*.spec.ts`, and test scenarios
under `tests/e2e/`, `tests/load/` and `tests/performance/`. Other languages are
not scored. If no supported behavioral test or test-contract defect changed,
call `noop` with the specific scope reason. `TestMain` and runner setup are
infrastructure, not behavioral cases; report a concrete infrastructure defect,
but do not invent a numeric score for setup-only changes.

Analyze at most 50 added or modified behavioral scenarios. Count table rows as
scenarios where evidence permits. Report sampling and truncation explicitly;
never extrapolate a clean sample to approval of unreviewed tests.

## Quality rubric

For each reviewed test, identify its path, changed line, observable contract,
regression that would escape if deleted, and any evidenced defect:

- distinguish behavioral contracts, implementation details and unknown evidence;
- check meaningful success, failure, edge, cancellation and authorization cases;
- flag tests that merely assert mocked internal calls without observable output;
- detect weakened/deleted assertions, false-green skips and duplicated scenarios;
- distinguish explicit conformance enforcement from brittle source-text checks:
  workflow source/compiled-lock assertions can be valid capability contracts;
- require production query/worker boundaries for dashboard source fixtures;
  JavaScript-only synthesized sources do not demonstrate conformance;
- real-adapter tests must retain missing/null semantics, exact scope, atomicity,
  CAS and cancellation where promised. Skipped PostgreSQL/native-browser
  backends are missing evidence, not proof of parity.

Do not import gh-aw-only rules: this repository does not mandate build tags on
every Go test, forbid all Go mock libraries, or use Vitest for root Node tests.
Mocks for explicit I/O seams are valid when they preserve the production
contract; do not mock away the behavior under review. Existing standard-library
Go assertions and Node `assert` are valid. More test than production lines is
an investigation signal, never an automatic failure or evidence of inflation.

## Score and verdict

For a complete reviewed set of behavioral cases, compute the upstream diagnostic:
`40 * design_tests / total + 30 * edge_tests / total +
max(0, 20 - 5 * duplicate_clusters) + (inflation ? 0 : 10)`, clamped to 0–100.
Count inflation only when unnecessary duplication is evidenced, not from a
test/production line ratio alone. Unknown classifications or incomplete evidence
make the score `N/A`; never divide by zero. Keep per-test classifications and
metrics in a named `<details>` section.

The score and upstream 30% implementation-test heuristic are diagnostics, not
CAO merge gates. Request changes only for concrete, high-confidence defects
that weaken the tested contract; cite the scenario, regression and smallest fix.
Use `COMMENT` for non-blocking observations or incomplete evidence, `APPROVE`
only for complete, clean behavioral review, and `noop` for no reviewable signal.
Never manufacture a finding to justify a report.

For a reviewable result, post at most one concise summary comment and exactly
one overall review through the declared safe outputs. Include the reviewed head,
scope, limitations, score (or `N/A`) and one next action. Do not duplicate existing
findings or claim test execution. Do not modify tests or production files.

In every comment and overall review body, use `###` for every heading; never use `#`, `##`, or `####` and deeper headings. Put every Markdown table inside a clearly named `<details>` element.
