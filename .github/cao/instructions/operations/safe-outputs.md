# Campaign safe outputs

A model instruction alone is not sufficient when a handler-level safeguard
exists. The orchestrator owns idempotent selection and dispatch. Workers own
idempotent repository outputs.

For each unique worker, target repository, and effective mode tuple, emit at
most one dispatch. Use singleton campaign concurrency with
`group: "${{ github.workflow }}"` and `cancel-in-progress: true`.

when `safe-outputs.create-issue` or `safe-outputs.create-pull-request` is
enabled, use `labels: [<campaign-slug>, <campaign-slug>:<worker-slug>]` and
`title-prefix: "[<campaign-slug>:<worker-slug>] "` so every created issue or
pull request identifies both its owning operation and worker.
GitHub rejects label names longer than 50 characters, so every label must be
50 characters or fewer. When `<campaign-slug>:<worker-slug>` is longer, keep
the campaign slug and use a shorter worker part in the label only; keep the
full worker slug in the title prefix.

when `safe-outputs.create-issue` is enabled, configure
`deduplicate-by-title: true`, explicit expiry, bounded `max`, a canonical
unprefixed subject that remains identical for the same unresolved repository
work across reruns, and existing-item reuse instructions; search all open
campaign-worker issues in the safe-output repository and reuse or comment on
matching work, or call `noop`.
every issue-creating worker configures `deduplicate-by-title: true`, explicit
expiry, bounded `max`, stable subject, and existing-item reuse instructions.

Use `3d` for high-frequency telemetry, `7d` for fast-changing operational
findings, `14d` for dependency and routine maintenance work, and `30d` only for
compliance. Dependabot worker issues expire after `14d`. Expiration is lifecycle
cleanup, not duplicate prevention.

Pull requests: preserve a stable branch or machine-readable body marker and
search open pull requests before creating another. Comments and reviews: inspect
prior campaign-worker output and do not post the same finding or status again.

control-plane workflows inherit `noop.report-as-issue: false` from
`shared/control.md` and must not redeclare an empty local `noop:` block because
it overrides imported handler settings. Standalone workflows that do not import
shared control must configure `safe-outputs.noop.report-as-issue: false`
explicitly. Do not use sub-issue grouping as backlog control.

## Worker report formatting

when `safe-outputs.create-issue` or `safe-outputs.create-pull-request` is
enabled, require every created issue or pull request body to follow the complete
Worker Report Formatting contract. The contract is mandatory for every worker
that creates issues, pull requests, comments, or reviews and applies to the
complete output body.

Make the report delightful to read, precise, terse, and easy to scan. Use plain
language, short sentences, compact bullets, and descriptive labels. Keep the
entire visible report to a single screen at normal GitHub desktop viewing. Show
only the decision essentials; move everything else into progressive disclosure.

Start directly with a concise executive-summary paragraph. Do not add a heading
before this opening paragraph because the first paragraph is always the
executive summary. It states what happened, the decision-relevant result,
critical findings, and key metrics. Immediately follow the summary with one
clear `**Action:**` sentence naming who should do what next and the acceptance
check.

After the opening paragraph, use `###` for every heading; never use `#`, `##`,
or `####` and deeper headings. Put non-essential background, verbose evidence,
logs, secondary metrics, per-item breakdowns, and every Markdown table in
clearly named `<details><summary><b>...</b></summary>` blocks. Use `> [!NOTE]`
for neutral status, `> [!WARNING]` for warnings, and `> [!CAUTION]` for
high-risk or blocking findings. Do not use emoji severity markers.

When a safe output includes a follow-up prompt, render
`<details><summary><b>Agent prompt</b></summary> ... </details>` so a human can
review the issue before using the prompt for an agentic run; evaluate the
potential follow-up actions and recommend the single most important action with
the highest expected return on investment.
