# SelfCare

SelfCare runs repository-local maintenance for `githubnext/gh-cao`. Its orchestrator registers twenty-three live-only workers and dispatches only enabled workers, including:

- **Accessibility Checker** audits the rendered documentation site and publishes one prioritized accessibility issue.
- **Code Improvement** extracts one evidenced duplicated dashboard UI construct into a tested reusable component and opens one focused draft pull request.
- **Calesi Design Principles** checks domain-owned service interfaces, protocol/location-independent consumption, faithful local implementations, multi-adapter conformance and deliberate governed extension delivery. Enabled for the existing authorized target, it proposes at most one bounded, tested Go remediation in a draft PR.
- **Dashboard Performance** rotates through trace-backed CFO, CTO, and CSO Lighthouse bottlenecks and opens one focused draft pull request.
- **Dashboard Debug Logging** instruments one evidenced dashboard JavaScript subsystem with category-filtered, privacy-preserving diagnostics.
- **Dashboard Data Schema** verifies the deployed dashboard JSON contracts and opens one focused draft pull request when inferred pseudo schemas change.
- **Data Acquisition Audit** reviews dashboard acquisition paths and opens one focused draft pull request when the audit is stale.
- **Database Architecture** maintains the [cloud-architect data guide](../docs/dashboard-data-architecture.md) with accessible Mermaid architecture diagrams, evidence-backed deployment and data-ownership contracts, and explicit source qualifications. It runs at most once per rolling 24 hours through the existing orchestrator, skips while its guide PR is open, and proposes one documentation-only draft PR only for material drift.
- **Dashboard Language Refactor** replaces one over-specialized dashboard view with tested reusable subcomponents configured through Dashboard Language.
- **Dashboard Review** assesses dashboard correctness, decision support, efficiency, and usability through deterministic checks and executive browser journeys.
- **Experimental Views** exercises every editable experimental and Operations view in Chromium and WebKit across data-source shapes and DOM-size stress, then opens one focused draft pull request for the highest-ranked issue.
- **Docs Build-Time Investigator** analyzes Documentation Pages workflow timings and publishes one non-repeating, evidence-backed caching or dashboard build-speed suggestion.
- **Glossary** scans recent merged pull requests and default-branch code changes, then opens one focused draft pull request when current repository evidence supports an Astro-compatible glossary update. The orchestrator dispatches this worker at most once per rolling 24 hours.
- **Pages Health** runs at most once per rolling six hours, scrolls every deployed dashboard view under desktop, mobile, and low-bandwidth profiles, and opens a focused draft PR with the highest-confidence JavaScript quick wins it can validate.
- **Primer Brand Checker** audits the dashboard against current Primer brand guidance and opens one focused draft pull request when an evidenced fix is available.
- **Reactive UI Expert** maintains the reactive UI skill, migrates JavaScript-produced view sources to request-scoped dashboard queries, reviews recent dashboard JavaScript changes for reactive patterns, and extracts direct HTML mutations into reactive elements, data binding, and effects.
- **Server Go Logging** refactors one server Go subsystem with privacy-preserving internal logging and focused unit tests that exercise real code without mocks.
- **Server SOC2** reassesses bounded Go server readiness at most once per rolling 24 hours and proposes up to three compatible, verified code fixes in one focused draft PR. The existing every-20-minute orchestrator checks at most ten recent runs and defers on unavailable/ambiguous history, including failed and cancelled runs. The worker also checks recent history for manual dispatches; it has no independent schedule.

The checked-in control policy admits only `githubnext/gh-cao` as a live target, and the target-authority declaration grants this repository's control plane authority for the campaign. The orchestrator rejects every other repository and every non-live candidate; all workers repeat those checks before performing their mission.

The campaign uses `shared/control.md` for policy resolution, target authority, dispatch envelopes, safe-output routing, and correlation. Its dispatch ceiling is twenty-three workflows for its single target; disabled workers remain ineligible.

The Database Architecture worker can change only
`docs/dashboard-data-architecture.md`. It rechecks current authoritative
sources rather than relying on a daily cursor, so missed runs do not hide
drift. The guide uses cloud architecture vocabulary for topology, lineage,
consistency, cache freshness, tenancy, capacity, backpressure and recovery;
unresolved specification contradictions remain visible. Accurate content
produces a no-op, and incomplete evidence or validation blocks publication.
The worker is explicitly enabled within the existing sole live target; scope,
the review default, credentials and unrelated worker enablement are unchanged.
The parent accounts for its additional 400 AI Credit ceiling in the aggregate
worker estimate. Activation still requires the reviewed policy, sources and
generated locks on the active control branch, existing credentials and human
PR review. There is no separate worker cron, automatic merge or deployment.

## Calesi design-principle enforcement

The [Calesi worker](../.github/workflows/self-care-calesi-design-principles.md)
adapts [SAP's guiding principles](https://cap.cloud.sap/docs/guides/integration/calesi)
without migrating CAO to SAP CAP or adding dependencies. Canonical data,
operational state, policy/authorization and dispatch remain separate narrow
contracts. A uniform interface does not promise identical latency, durability,
transactions, backend capabilities or authority.

Each finding needs current SHA/path/line evidence, a consumer-to-contract-to-adapter
trace, a violated principle, counter-evidence and explicit provider limitations.
The worker checks for implementation leakage, adapter bypasses, misleading local
fixtures, missing shared conformance and extension side effects bypassing
authorization, safe outputs, outbox durability or idempotency. Intentional
limitations are not forced into parity; unsupported operations must fail
explicitly. Production mock/backend fallbacks and main-thread dashboard queries
or component-side joins are prohibited.

Enforcement uses the existing draft-PR/test mechanism: one reproduced violation,
at most four production Go files and six tests under `server/internal/`, ten files
and 50 KiB total. Required regression and relevant real-adapter conformance tests
must pass; skipped backends remain incomplete. Work outside that boundary is
deferred, not partially migrated. No new enforcement framework or dependency on
an unmerged architectural refactor is introduced.

The effective policy enables this worker with `enabled: true` for SelfCare's
existing sole authorized target, `githubnext/gh-cao`. This explicit enablement
does not change target scope, credentials, the campaign review default or
unrelated worker settings. The parent must still require successful precomputed
policy admission at the exact workflow revision. Existing live-only SelfCare
admission remains unchanged; standard review-mode output routing remains
fail-closed, not a new review-mode dispatch path. Activation requires the
reviewed sources, policy and compiled workflows on the active control branch.

There is no deterministic operational-value grader yet: a proposed abstraction
or successful test run is not evidence of accepted architectural value. Measure
accepted remediation and preserved semantics only after independent adoption
evidence supports a frozen value function.

## Server SOC2 readiness

The SOC2 worker follows the [readiness specification](../specs/server-soc2-compliance.md)
and [assessment skill](../.github/skills/soc2-server-assessment/SKILL.md).
Its daily JSON/Markdown evidence artifacts (14-day retention) and run summary
remain separate from remediation deduplication: an open worker PR blocks
another PR, not assessment. Material register/changelog updates are proposed
in the PR; while blocked, explicit pending deltas remain in the artifact for
human consolidation. No unchanged daily timestamp PR is created.

The worker ranks current actionable code gaps by severity, confidence and
stable ID/path. Missing operational evidence remains an evidence request.
One package, four production files, six tests, two SOC2 records and 50 KiB
(51,200 patch bytes) are the maximum proposal. Workflow, policy, credentials, dependencies,
generated files, deployment and feature enablement are outside its write scope.
Required checks must pass; skipped integrations are not verified controls.
The worker cannot certify compliance, accept risks or approve exclusions.

Activation requires the reviewed workflow, compiled lock, inventory and policy
registration at the same commit on the control repository's active branch,
existing read/safe-output credentials, Actions availability and human PR review.
Registration does not add targets or change the existing sole live target;
campaign default remains review. No manual run, merge or deployment is implied.

## Operational value

The Docs Build-Time Investigator registers an operational grader through gh-aw's `operational-value` protocol. It measures material `docs.yml` execution-time reduction while requiring completed-run reliability to be preserved. The campaign dashboard keeps recommendations distinct from matured attainment. Design graders for the other workers after adoption evidence establishes measurable accessibility, component-reuse, declarative-view-reuse, reactive-UI-maintenance, glossary-maintenance, data-acquisition, dashboard-review, public-failure-remediation, and brand-maintenance outcomes.
