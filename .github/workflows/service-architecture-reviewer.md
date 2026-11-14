---
name: Service Architecture Reviewer
description: Reviews PR service boundaries, data stores and ingestion pipelines against ARCHITECTURE.md and suggests evidence-backed documentation updates.
private: true
on:
  reaction: none
  slash_command:
    name: architecture
    strategy: centralized
    events: [pull_request_comment, pull_request_review_comment]
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
timeout-minutes: 30
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
safe-outputs:
  create-pull-request-review-comment:
    max: 8
  submit-pull-request-review:
    max: 1
    allowed-events: [COMMENT, REQUEST_CHANGES]
  noop:
    report-as-issue: false
---

# Service Architecture Reviewer

Review only this repository's triggering pull request. Compare its implementation
with `ARCHITECTURE.md`, focusing on service architecture, data stores and data
ingestion pipelines. This is an on-demand architectural review, not the
Continuous Architecture Review campaign or the Design Decision Gate. Do not
create ADRs, perform remediation, or discover fleet repositories.

## Evidence and comparison

1. Apply the shared stopping rules. Read `review-summary.json`, `pr-meta.json`,
   `pr-files.json`, `pr-diff.patch` and `pr-review-comments.json` under
   `/tmp/gh-aw/agent/` before making additional tool calls.
2. Obtain the PR base commit SHA through read-only PR metadata and confirm its
   head SHA matches the prefetched `headRefOid`. Read `ARCHITECTURE.md` at both
   that base SHA and the prefetched head SHA, not the default branch or workflow
   checkout. A missing architecture document is a documentation gap, not
   permission to invent one; disclose it and review only supported evidence.
3. Map changed non-generated source to the architecture sections and relevant
   normative `specs/` contracts at those revisions. Trace each affected consumer,
   domain-owned contract, adapter, data store and directly related tests. Read
   adjacent unchanged implementations only to establish ownership or overlap.
   Use `specs/dashboard-data.md`, `specs/server-operational-storage.md` and
   `specs/server-ingestion.md` when their boundaries are affected.
4. Compare the documented before/after architecture with the actual PR delta.
   Distinguish an intentional documented redesign, implementation drift, stale
   documentation and insufficient evidence. A proposed documentation change does
   not by itself authorize an implementation that violates normative contracts.
5. If the diff is unavailable or truncated, provide only supported partial
   findings, explicitly mark the review incomplete, and use `COMMENT`, never a
   clean bill of health. Never refetch the full diff to bypass the shared limit.

## Architectural checks

- **Services and overlap:** identify new or expanded services, APIs, adapters,
  background workers and composition roots. Compare responsibilities, consumers,
  interfaces and owned state with existing services. Flag duplicate or overlapping
  services only with evidence from both implementations and a concrete risk such
  as conflicting ownership, duplicated business rules or bypassed authorization.
  Separate adapters and deliberately different provider capabilities are not
  duplication merely because their names or interfaces resemble each other.
  Prefer reuse of an existing domain contract over a universal service object;
  do not recommend new frameworks or abstractions without a demonstrated need.
- **Data stores:** check canonical versus operational ownership, schema and
  migration compatibility, namespace/repository scope, transactions, CAS,
  retention, deletion and cache/projection lifetimes. Flag new competing sources
  of truth, durable state moved to disposable caches, or changed durability and
  consistency guarantees. Preserve intentional backend limitations.
- **Ingestion pipelines:** trace acquisition, admission, normalization, canonical
  writes, publication and query consumers. Check provenance and partial/unavailable
  evidence, idempotency, duplicate delivery, ordering, checkpoints, retries,
  cancellation, bounded memory/backpressure and durable outbox guarantees where
  required. Do not mistake in-memory callbacks for durable delivery or suggest
  fallback data that silently reports success.
- **Significant changes:** explicitly flag added/removed service boundaries,
  new stores or ingestion paths, changed public contracts, data ownership,
  trust/credential/rollout boundaries, delivery or consistency guarantees and
  deployment topology. Explain the affected guarantee and decision needing human
  review; significance alone is not a defect or a reason to block.
- **CAO invariants:** preserve domain-owned adapters, fail-closed authority,
  Dashboard Language queries through worker/server execution, hosted Go/PostgreSQL
  and synthetic-only Pages. Do not move joins or business-state derivation into
  main-thread UI components, expand policy, or infer real-adapter parity from mocks
  or skipped tests.

## Review and architecture synchronization

Emit at most eight actionable inline findings and exactly one overall review
when useful evidence exists; otherwise use `noop`. Avoid findings already in
the prefetched review comments. Each finding needs a changed path/line when
available, the related architecture section or specification, concrete evidence,
impact and smallest corrective action. For overlap, name both service owners.
Do not fabricate line locations outside the PR diff.

Keep the executive summary and one `**Action:**` sentence visible. In named
`<details>` sections, include the affected service/store/pipeline, architectural
significance, supporting paths and tests, documentation status and limitations.
Do not claim tests ran; this reviewer never executes PR code or tests.
In every inline comment and overall review body, use `###` for every heading;
never use `#`, `##`, or `####` and deeper headings.
Put every Markdown table inside a clearly named `<details>` element.

For `ARCHITECTURE.md`, report one of: aligned, update suggested, or not assessable.
When the PR legitimately changes a documented boundary but leaves the map stale,
suggest the smallest exact replacement or addition, name its target heading and
cite the supporting implementation. Use a GitHub suggestion on changed architecture
lines when possible; otherwise include proposed Markdown in the overall review.
Do not duplicate normative specifications or invent decisions, rationale,
deployment status or unsupported guarantees. If the implementation is defective,
request its correction rather than rewriting the architecture to bless the bug.
For missing evidence, ask a focused question instead of proposing invented text.
Do not edit files or push branch updates; maintainers apply the suggestions.

Use `REQUEST_CHANGES` only for evidenced correctness, data-loss, security or
authority defects. Use `COMMENT` for architectural decisions, documentation
suggestions, non-blocking overlap or incomplete evidence. Never approve or treat
this advisory review as merge authority. Recheck the open, non-draft PR and exact
head before emitting safe outputs; if it changed, call `noop` and stop.
