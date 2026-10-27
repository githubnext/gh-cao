# SelfCare

SelfCare runs repository-local maintenance for `githubnext/gh-cao`. Its orchestrator dispatches up to twenty live-only workers, including:

- **Accessibility Checker** audits the rendered documentation site and publishes one prioritized accessibility issue.
- **Code Improvement** extracts one evidenced duplicated dashboard UI construct into a tested reusable component and opens one focused draft pull request.
- **Dashboard Performance** rotates through trace-backed CFO, CTO, and CSO Lighthouse bottlenecks and opens one focused draft pull request.
- **Dashboard Debug Logging** instruments one evidenced dashboard JavaScript subsystem with category-filtered, privacy-preserving diagnostics.
- **Dashboard Data Schema** verifies the deployed dashboard JSON contracts and opens one focused draft pull request when inferred pseudo schemas change.
- **Data Acquisition Audit** reviews dashboard acquisition paths and opens one focused draft pull request when the audit is stale.
- **Dashboard Language Refactor** replaces one over-specialized dashboard view with tested reusable subcomponents configured through Dashboard Language.
- **Dashboard Review** assesses dashboard correctness, decision support, efficiency, and usability through deterministic checks and executive browser journeys.
- **Experimental Views** exercises every editable experimental and Operations view in Chromium and WebKit across data-source shapes and DOM-size stress, then opens one focused draft pull request for the highest-ranked issue.
- **Docs Build-Time Investigator** analyzes Documentation Pages workflow timings and publishes one non-repeating, evidence-backed caching or dashboard build-speed suggestion.
- **Glossary** scans recent merged pull requests and default-branch code changes, then opens one focused draft pull request when current repository evidence supports an Astro-compatible glossary update. The orchestrator dispatches this worker at most once per rolling 24 hours.
- **Open Source Failures** scans the bounded CAO dashboard activity snapshot, clusters failed runs across represented public projects, and files a digest plus focused remediation issues.
- **Pages Health** runs at most once per rolling six hours, scrolls every deployed dashboard view under desktop, mobile, and low-bandwidth profiles, and opens a focused draft PR with the highest-confidence JavaScript quick wins it can validate.
- **Primer Brand Checker** audits the dashboard against current Primer brand guidance and opens one focused draft pull request when an evidenced fix is available.
- **Reactive UI Expert** maintains the reactive UI skill, migrates JavaScript-produced view sources to request-scoped dashboard queries, reviews recent dashboard JavaScript changes for reactive patterns, and extracts direct HTML mutations into reactive elements, data binding, and effects.
- **Server Go Logging** refactors one server Go subsystem with privacy-preserving internal logging and focused unit tests that exercise real code without mocks.
- **Server SOC2** reassesses bounded Go server readiness at most once per rolling 24 hours and proposes up to three compatible, verified code fixes in one focused draft PR. The existing every-20-minute orchestrator checks at most ten recent runs and defers on unavailable/ambiguous history, including failed and cancelled runs. The worker also checks recent history for manual dispatches; it has no independent schedule.

The checked-in control policy admits only `githubnext/gh-cao` as a live target, and the target-authority declaration grants this repository's control plane authority for the campaign. The orchestrator rejects every other repository and every non-live candidate; all workers repeat those checks before performing their mission. Open Source Failures selects public project records only from the dashboard's validated activity snapshot; it does not discover or access repositories independently.

The campaign uses `shared/control.md` for policy resolution, target authority, dispatch envelopes, safe-output routing, and correlation. It dispatches at most twenty workflows for its single target.

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

The Docs Build-Time Investigator registers an operational grader through gh-aw's `operational-value` protocol. It measures material `docs.yml` execution-time reduction while requiring completed-run reliability to be preserved. The campaign dashboard keeps recommendations distinct from matured attainment. Design graders for the other workers after adoption evidence establishes measurable accessibility, component-reuse, declarative-view-reuse, reactive-UI-maintenance, glossary-maintenance, data-acquisition, dashboard-review, public-failure-remediation, and brand-maintenance outcomes.
