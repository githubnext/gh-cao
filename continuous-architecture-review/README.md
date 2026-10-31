# Continuous Architecture Review

This campaign makes one evidence-backed, reviewable architecture improvement to
`githubnext/gh-cao` at a time. Unlike CAO Evolution's control-plane integrity
findings or Repo Assist's general maintenance, its worker implements a cohesive
change, consolidates callers, removes superseded paths when safe, verifies the
result, and publishes a readiness report. It does not merge or deploy changes.

| Workflow | Responsibility |
| --- | --- |
| `continuous-architecture-review` | Select one explicitly authorized `githubnext/gh-cao` target and dispatch once. |
| `continuous-architecture-review-implementation` | Inspect, implement, test, reassess, and publish one reviewable patch and readiness evidence. |

Install with `gh aw add githubnext/gh-cao/continuous-architecture-review`.
Enable the campaign explicitly in the **control repository's** reviewed
`.github/workflows/cao.json` policy. This catalog registers it disabled in
review mode for one repository; creation does not activate or run it. The dispatcher
admits only `githubnext/gh-cao`. Review mode places patches and reports in a
review bundle and a deduplicated control-repository issue; it never edits the
target repository remotely. Live draft pull requests require a separate reviewed
policy change granting live mode. A report's READY gate is not live authority.
No new GitHub tokens, services, databases, or dashboards are required.

## Readiness report

The canonical input is `continuous-architecture-review/evidence.json` in the
target checkout. After a substantive change, update that input only with
observed check results, paths, command results, and source references, then run:

```sh
node continuous-architecture-review/report.mjs \
  continuous-architecture-review/evidence.json .
```

The script writes `campaign-readiness.md` and timestamped Markdown and evidence
snapshots under `continuous-architecture-review/reports/`. The first run is the
baseline. Subsequent runs compare the same metrics to the baseline and latest
previous snapshot; a rerun with identical evidence does not make up a new
historical observation. Unknown counts and missing security evidence stay
unknown, not zero. Critical security failures block live execution even if
other checks pass. The worker must also obey the independent CAO policy gate.
Review bundles preserve proposed snapshots, but the target's comparison
history advances only after a maintainer accepts and applies those files.
The initial execution check is `FAIL` because the campaign is intentionally
disabled by policy, not because a workflow run failed; the gate is `BLOCKED`
until an authorized review-mode run and its security evidence are available.

Evidence uses `campaign`, the exact 40-character `commit`, an ISO UTC `date`,
`checks` for `execution`, `security`, `reliability`, `scalability`, `tests`,
`legacy`, `duplicates`, `dependencies`, and `architecture`, nullable observed
`counts` for `securityBlockers`, `duplicatePaths`, `legacyComponents`, and
short `findings` and `nextActions`. Check statuses are `PASS`, `WARN`, `FAIL`,
or `UNKNOWN`; each check has an `evidence` reference. Security also declares
`critical: true`, `false`, or `null` (`null` means not established). Run the
focused report tests with `node --test tests/unit/campaign-readiness.test.mjs`.
The report documents its own exact gate and comparison rules in collapsed
technical evidence. Do not equate passing tests with a comprehensive security
assessment or with campaign activation. The commit identifies the inspected
target base revision, not an assertion that a proposed patch is already merged.

An optional improvement loop is a maintainer's review of accepted patches and
later regressions against the evidence snapshots. It does not auto-promote
policy, rewrite workflow instructions, or infer success from a created PR.
