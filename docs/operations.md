---
title: Monitor, Recover, and Maintain
description: Monitor control-plane runs, stop unsafe activity, recover from incidents, and maintain installed campaigns.
agent:
  type: operations
  prominent: true
---

Use this page after installation to answer the urgent operator questions: Is the control plane healthy? How do I stop it? What evidence should I collect? How do I recover safely?

Choosing what to run is a separate task. Start with
[From Outcome to Execution](operation-patterns.md) for the meaning of central
operations and the tradeoffs between direct execution, standalone fleet
workflows, campaigns and ordinary Actions. Campaign-specific procedures below
apply only when you use that pattern.

For a failed deployment or runtime, follow the
[`debug-cao` skill](https://github.com/githubnext/gh-cao/blob/main/skills/debug-cao/SKILL.md)
before changing or rerunning anything, then use the deployment-specific guide
linked from [deployment options](deployment.md).

| Need | Start here |
| --- | --- |
| Check scheduled runs | [Routine monitoring](#routine-monitoring) |
| Publish a finished review issue to its target | [Publishing reviewed campaign issues](#publishing-reviewed-campaign-issues) |
| Investigate cancelled or incomplete work | [Queuing and resource exhaustion](#queuing-and-resource-exhaustion) |
| Stop one worker, one campaign, or everything | [Emergency stop](#emergency-stop) |
| Respond to an unsafe output or exposed credential | [Incident response](#incident-response) |
| Update an installed control plane | [Update CAO](#update-cao) |
| Add or update catalog workflows | [Maintain the catalog](#adding-a-campaign) |

For installation, begin with [Set Up CAO](setup-quickstarts.md). Add and run a campaign only after the bare control plane is committed.

```text
Is unsafe activity active or broadly possible?
	|
	+-- yes --> disable Actions, cancel runs, revoke credentials if needed
	|
	+-- no ---> disable one campaign or worker, collect evidence, resume in review
```

:::danger[Stop first when scope is unclear]
If shared control, authentication, or multiple campaigns may be affected, use the control-plane-wide emergency stop before investigating.
:::

## Validate Before Scheduled Live Runs

Before scheduled live operation, run one target through two manual checks:

1. `review`: set the worker `MAX_MODE` to `review`; verify the private review destination and no target writes.
2. `live`: set the worker `MAX_MODE` to `live`; use one low-risk target and verify the declared output and downstream CI.

Record both run URLs and restore the intended worker ceiling after the canary. A failed check disables the affected campaign and cancels its active runs until it can resume in `review`.

Use the same bounded profile in every gate:

```yaml
target_repo: acme/disposable-canary
max_repos: 1
rollout_percent: 100
expected_target_writes:
	review: 0
	live: declared outputs only
```

:::tip[Change one dimension at a time]
Keep the target and repository limits fixed while changing the mode. That makes routing differences attributable to the promotion gate rather than a different repository sample.
:::

The catalog source repository's `Review smoke` Actions workflow automates the first check for catalog maintainers. It is repository-only test tooling and is not installed by `aw.yml`. Run it manually, select one campaign, and provide one explicit `OWNER/REPO` target plus a private review repository. It dispatches that orchestrator with `max_repos: 1`, `rollout_percent: 100`, and `safe_output_mode: review`, waits for the orchestrator and correlated workers, and verifies that target issue and branch snapshots remain unchanged. It has no schedule and cannot request live processing.

The repository-only `Enterprise canary` Actions workflow automates both modes for catalog maintainers while keeping review and live deliberate:

1. Create repository environments named `central-agentic-ops-review` and `central-agentic-ops-live`. Require reviewers for both; restricting deployment branches to the default branch is recommended.
2. Add `GH_AW_E2E_TOKEN` to the environments when the built-in token cannot read the target/review repository or inspect cross-repository refs and issues. Scope it only to the dedicated canary repositories and required metadata, issues, pull requests, contents, and Actions access.
3. Use dedicated disposable target and private review repositories under an allowed owner. Never point review or live canaries at production repositories.
4. For review, enter `REVIEW OWNER/REPO` in `confirmation`; for live, enter `LIVE OWNER/REPO`.
5. Leave `require_output` false when a legitimate no-op is acceptable. Set it true only after preparing repository evidence that should deterministically produce a durable output. Review then requires a review-repository change; live requires a target-repository change.

The canary snapshots issues, pull requests (through the issues API), and branch refs before dispatch. Review must leave the target snapshot unchanged and may change only its private review destination; live may change only the dedicated target. Repository snapshots are a routing guard, not semantic approval of generated content, so operators must still inspect the output and correlation metadata.

The repository-only `Enterprise review stress` workflow sends only `2`, `3`, or `5` same-scope review runs and requires `STRESS OWNER/REPO RUNS` confirmation plus approval through the `central-agentic-ops-stress` environment. It routes outputs to an explicit private review repository, verifies that concurrency supersedes all but the newest run, and confirms that the target snapshot remains unchanged. Real stress remains manual because every run consumes AI Credits; `npm run test:load` supplies the CI-scale test with 100,000 synthetic repositories and no model calls.

## Routine Monitoring

Review the following for scheduled runs:

| Signal | Expected condition |
| --- | --- |
| Authentication | App token or PAT resolves without exposing credential data |
| Candidate selection | Targets match campaign discovery rules and configured limits |
| worker workflow eligibility | Installed worker workflows match and disabled worker workflows are skipped |
| safe output routing | review routes privately without target writes, and live targets the selected repository |
| Correlation | worker workflow safe outputs identify the orchestrator workflow run |
| safe outputs | Type, count, branch, files, and destination stay within declarations |
| Quality | safe outputs are actionable, non-duplicative, and supported by evidence |
| Cost | AI Credits and run volume remain within workflow limits and expectations |

List recent runs from the command line when correlating orchestrators and workers:

```bash
CONTROL_REPO="acme/central-agentic-ops"

gh run list \
	--repo "$CONTROL_REPO" \
	--limit 20 \
	--json databaseId,displayTitle,event,status,conclusion,url
```

With default one-repository caps, one Advisory orchestration is bounded by 850 AI Credits (250 for the orchestrator plus one 600-credit worker), one Dependabot orchestration is bounded by 850 AI Credits (250 plus one 600-credit worker), one Optimization orchestration is bounded by 1,150 AI Credits (250 plus one 400-credit auditor and one 500-credit optimizer), one Dreaming orchestration is bounded by 650 AI Credits (250 plus one 400-credit `AGENTS.md` curator), one EU CRA orchestration is bounded by 1,100 AI Credits (200 plus six 150-credit workers), one CAO Evolution orchestration is bounded by 2,950 AI Credits (250 plus four control-plane workers totaling 1,700 credits, one 500-credit failure investigator, and one 500-credit compiler-security worker), one Dev Practices orchestration is bounded by 1,050 AI Credits (250 plus two 400-credit workers), and one ESLint Factory orchestration is bounded by 2,000 AI Credits (250 plus a 200-credit inventory worker, a 450-credit miner, a 450-credit refiner, a 350-credit applier, and a 300-credit librarian). The independent weekly Advisory campaign maintainer and daily CRA campaign maintainer are each bounded by 200 AI Credits. Declared dispatch ceilings keep deliberately expanded runs finite. These are hard worst-case envelopes, not expected consumption. Every workflow also has a timeout and same-scope concurrency cancellation.

### Run an AW Fixing Loop

The CAO Evolution compiler-security worker reports the exact compiler, validation, lint, image, and security-scanner findings that need remediation. To fix the same findings locally with a coding agent:

1. Install or update the extension with `gh extension install github/gh-aw` or `gh extension upgrade gh-aw`.
2. Configure the coding agent's MCP client to launch `gh aw mcp-server` over stdio with the target repository as its working directory.
3. Ask the agent to use the server's `fix` and `compile` tools, change workflow Markdown sources rather than generated lock files, and repeat the full validation command until it passes.

Use this command as the loop's acceptance check:

```bash
gh aw compile \
	--no-check-update \
	--strict \
	--validate \
	--validate-images \
	--models \
	--actionlint \
	--shellcheck \
	--yamllint \
	--zizmor \
	--poutine \
	--runner-guard \
	--grant \
	--grype \
	--syft
```

The container and image checks require a running Docker daemon. If a tool, image, or registry is unavailable, treat the result as incomplete rather than clean. Review the generated `.lock.yml` diffs after each successful compile, but make source changes only in `.github/workflows/*.md` and directly related files.

### Queuing and Resource Exhaustion

The control plane does not implement a durable work queue. GitHub Actions accepts workflow dispatches, while each orchestrator and each target-scoped worker uses `cancel-in-progress: true`: a newer same-scope run supersedes an older running or pending run instead of building an unbounded backlog.

API and budget failures are fail-closed:

- a discovery API failure, including rate limiting, produces no candidates and no worker dispatches, then an incomplete orchestrator report;
- a required control-source or workflow-resolution API failure stops precomputation before dispatch;
- a dispatch failure is recorded as deferred and is not retried within the same run;
- a worker that reaches an API limit, workflow AI Credit cap, or broader budget limit after startup stops additional work and reports incomplete without self-dispatch or a wait loop; if budget enforcement rejects startup, the failed Actions run is the audit record;
- work resumes only through a later scheduled run or an authorized manual run, which is a new bounded attempt. Each attempt checks live API capacity before rediscovering current candidates.

This favors bounded failure over eventual delivery. Scheduled, level-triggered operations reconcile current work rather than resume a prior process. One-off manual requests are not replayed, and guaranteed eventual processing is not provided by the current workflows.

Optional observability imports for Sentry, Grafana, and Datadog configure exporter destinations; they do not emit the dispatcher span or replace GitHub Actions run history and correlation metadata as the primary execution audit trail.

Every orchestrator emits a `central-agentic-ops.dispatcher.run` span in the safe-output phase after normalized agent output and the trusted control-precompute artifact are restored. The hook runs only in the consolidated safe-output job, not worker or custom safe-output jobs; a skipped safe-output phase does not emit it. Its attributes contain only the campaign, policy state, limits, and aggregate candidate, requested dispatch, target, workflow, and incomplete counts; target names, workflow inputs, run URLs, and error payloads are excluded. A `requested` status records dispatch intent before safe-output handlers call the GitHub API. Missing or malformed telemetry evidence is reported as a step failure without blocking dispatch. Use gh-aw outcome spans and GitHub Actions run history to determine dispatch success or failure.

## Publishing Reviewed Campaign Issues

The optional Ops Publish add-on turns an explicit human label into a deterministic issue publication without rerunning AI. It remains outside the Agentic Workflow campaign catalog: copy `ops-publish/ops-publish.yml` and `ops-publish/ops-publish.mjs` from a pinned catalog revision into the private repository that receives review issues.

Enable `control-plane.publishing` in `.github/workflows/cao.json`, declare its `reviewers` and optional `control-repositories`, and create the `ops:publish-to-target` label. Applying the label to an eligible bot-authored review issue validates the originating worker run, derives its target and campaign from trusted run metadata, enforces checked-in scope and target-owned campaign authority, creates the target issue with provenance, and closes the review issue.

Once review is finished, apply **`ops:publish-to-target`** to the **review issue**:

```bash
gh issue edit ISSUE_NUMBER \
  --repo OWNER/REVIEW_REPO \
  --add-label "ops:publish-to-target"
```

Replace `ISSUE_NUMBER` with the review issue number and `OWNER/REVIEW_REPO` with the repository that holds it, not the target repository. A successful publication recreates the reviewed issue in its validated target repository, posts the target URL on the review issue, and closes the review issue. **It does not rerun the agent or change campaign mode.**

This path supports issue outputs only. It does not transfer issues, publish pull requests or comments, or apply artifact-backed review bundles. GitHub issue transfer is not used because it is limited to repositories under one owner and cannot transfer a private issue to a public repository. See the [Ops Publish add-on guide](https://github.com/githubnext/gh-cao/blob/main/ops-publish/README.md) for installation, credentials, and failure behavior.

## Publishing Pages Reports

This section covers the GitHub Actions only dashboard. For a step-by-step procedure, credential profiles, and server-backed alternatives, see [Deployment options](deployment.md).

### Install the dashboard campaign

The root Central Agentic Ops campaign installs the deterministic activity index and dashboard by default. To install the dashboard without the operational workflows, install both focused deterministic campaigns. Unpinned package coordinates resolve the latest release automatically:

```bash
gh aw add githubnext/gh-cao/activity
gh aw add githubnext/gh-cao/dashboard
```

Both installation paths add an independently dispatchable dashboard builder, a manual standalone Pages publisher, and their deterministic report modules. There is no additional dashboard enable variable, and installation does not deploy or enable Pages.

:::note[Do not create `REPORT_PAGES_TOKEN`]
The dashboard does not use a `REPORT_PAGES_TOKEN` secret. Its build job reads report data with the automatic `github.token` and explicit job-scoped permissions. Its standalone deploy job uses GitHub Pages OIDC with `pages: write` and `id-token: write`. If an installed workflow requests `REPORT_PAGES_TOKEN`, it did not come from the current campaign and should be reviewed or updated rather than supplied with a PAT.
:::

:::caution[The report can contain private repository data]
The generated site includes data from its private control-plane repository, including repository identity, issue and pull request content, comments, artifact-derived summaries, workflow names and states, and run links. A private source repository does not by itself make its Pages site private. Configure Pages access control for the intended audience before the first deployment, and do not install this campaign when that boundary is unavailable.

Organization discovery excludes unrelated private repositories by default. `REPORT_INCLUDE_PRIVATE` is a boolean flag, not a credential, and there is no `REPORT_INCLUDE_TOKEN`. The current catalog workflow does not set the flag or accept a cross-repository credential, so it cannot discover unrelated private repositories out of the box.

A deliberate custom extension should mint a short-lived GitHub App token installed only on the selected repositories and grant `Metadata: read`, `Contents: read`, and `Actions: read`. The optional organization audit-log health query requires a compatible user token or fine-grained PAT with organization `Administration: read`; discovery continues without that health data when access is unavailable. Do not use a broad classic PAT.
:::

The campaign installs the following components in the control-plane repository:

- `.github/workflows/cao-dashboard.yml`, the dashboard builder, artifact publisher, and optional standalone Pages publisher;
- `.github/workflows/cao-activity.yml`, the scheduled and manually dispatchable data collector and cache publisher;
- `activity/logs.mjs`, the single bounded `gh aw logs` acquisition entrypoint;
- `activity/index.mjs`, the local-only deployed-workflow and run-health indexer;
- `dashboard/report/aic-usage.mjs`, the bounded AI Credit usage collector;
- `activity/inventory.mjs`, the dependency-free control-plane inventory extractor;
- `dashboard/report/operational-values.mjs`, the compatibility-named fleet collector that preserves ordered operational-grader metrics from retained gh-aw run records;
- `dashboard/report/records.mjs`, the durable issue, pull request, comment, and review-artifact normalizer with logs-derived run attribution;
- `dashboard/site/src/data/queries/database.json`, the declarative projections over canonical inventory, run, and record data (no report-source build);
- `dashboard/site`, the bundled Dashboard Language configuration, validator, presenter, and browser runtime.

For a standalone Pages site:

1. In **Settings > Pages**, select **GitHub Actions** as the source and apply the required access controls.
2. Protect the `github-pages` environment as required by your organization.
3. Run **Central Agentic Ops Dashboard** from the repository's **Actions** page.
4. Verify the deployment URL and confirm that the report shows data only from the intended control-plane repository.

Run `gh cao setup` to configure the control repository's Pages source as GitHub Actions with repository-restricted access for private repositories. The standalone workflow passes `enablement: false` to `actions/configure-pages`, checks private-site access before publishing, and has no schedule. Set `control-plane.campaigns.dashboard.deploy` to `false` when an existing Pages workflow owns deployment. The dashboard workflow continues to publish `central-agentic-ops-dashboard`; the owning workflow can list successful `cao-dashboard.yml` runs on the default branch, download the latest artifact into its site output, and deploy the combined artifact.

In the CAO catalog repository, **Documentation Pages** (`docs.yml`) owns the
combined documentation and dashboard deployment. Scheduled runs, successful
default-branch dashboard completions, and normal manual runs skip rebuilding when
both deployed components are current; matching pushes always rebuild. To redeploy
after a repository rename or Pages configuration change, select **Run workflow**
on the default branch and enable **force_deploy**, or run:

```sh
gh workflow run docs.yml --repo githubnext/gh-cao --ref main -f force_deploy=true
```

This bypasses freshness checks, rebuilds the documentation, and deploys the
combined site only after the build succeeds. Pages environment protections and
permissions still apply.

The Activity workflow restores its source-shard cache, collects bounded read-only
`gh aw logs --audit --artifacts usage` evidence for the resolved repository scope,
and refreshes native operational-value measurements. Its index job merges the
owner-scoped fragments and publishes normalized run/detail shards and a canonical
SQLite projection. Consumers query those snapshots without collecting more data.

Target repository Git history, Actions run metadata, and accepted evidence are the reconstructable authority for operational value. gh-aw's local weekly shards and the installed control repository's Actions observation cache are accelerators, not archives; either may be deleted or evicted. The current Pages artifact is a presentation snapshot. No private organization-specific observation ledger belongs in the public catalog. Organizations that require an independently durable derived archive must persist the versioned observation records in an access-controlled control-plane data store and retain their source identity and evidence lineage.

### Tracking campaign outcomes

The regular campaign **Insights** tab shows **Workflow run results** and
**Execution reliability**, not verified campaign outcomes. Their source is
retained GitHub Actions workflow-attempt metadata collected through `gh aw logs`
for the campaign's control-repository workflows in the selected time window.
Only the retained attempt for each run is available; earlier retry history is not
reconstructed. Success rate divides successful retained attempts by concluded
attempts; queued and running attempts are excluded. The chart's other concluded
attempts include cancellations and skipped runs, not just failures.
Reported output items are safe-output counts from successful attempts, not
confirmed merges or resolved problems. Average AIC includes only successful
attempts with recorded positive cost. These facts do not measure improvements in
target repositories, and missing collection history is not reconstructed.
Sparse refreshes retain earlier cost and token totals only within the same run
attempt. A newer explicit credit measurement, including zero, takes precedence
over a preserved summary.

Open **Value** in the sidebar for published workflow results, then select a card
to inspect its native evidence report. Value is not a permanent workflow tab;
the top-level destination is the single discovery surface. Collection-health
cards may open a workflow-scoped detail page, while reports keep intent, safe
outputs, accepted implementations and independent repository snapshots separate.
Snapshot drill-downs expose the exact retained items counted. See
[Operational Value](operational-value.md) for the measurement and publication
contract.

The legacy **Operational Value** page, its **Repository measurements** tab and
repository drill-downs are no longer shipped. Existing canonical observations
and their collection, retention and evaluation contracts remain available.
Missing evidence is not zero value, and successful execution does not establish
an accepted outcome or causal impact.

Dashboard Language performs all classification, ordering, aggregation and
drill-down shaping in the data worker. Shared cards, charts and temporal plots
render the supplied payloads; there is no new compute-layer analysis, acquisition,
API traffic or LLM call on navigation.

#### Native observation evaluation

Retained native observations keep their unit, direction, and maturity. Their
evaluation contracts remain distinct from the published Value report packets.
Recent movement compares
the current complete period with the immediately preceding equal-duration period
under the same repository, measure, evaluator, native unit, direction,
measurement kind and cadence. Daily seven-day rolling samples compare seven days
apart, not against yesterday's overlapping window. Snapshot backlog changes compare
cutoff states, not counts of remediations. Evaluation evidence retains native
delta, exact compared periods, exposure and cohort caveats.

Both evaluation modes allow recent post-adoption movement. Adoption association
is separate and requires genuine pre-adoption evidence; the first post-adoption
point is never a substitute baseline. Two points do not establish a sustained
trend, statistical anomaly, target attainment or causation. Stable-but-bad is not
healthy merely because recent change is zero. Calibrated anomaly/drift policies
are not supplied by these comparisons.

Missing periods, unknown or partial source coverage, interim values and stale
assessments withhold movement without hiding native values. Freshness uses the
original assessment time and cadence, not the evidence cutoff before maturation.
A newer unavailable assessment cannot resurrect an older accepted value.
Changing the display horizon does not change the frozen measurement duration or
exclude its required preceding reference. Cohort and denominator changes remain
visible, never silently normalized away.

New observations retain exact periods, measurement kind, explicit coverage,
evidence lineage and available eligible-cohort identity/counts. Legacy evidence
is not assigned proof retrospectively. Optimization currently reports
**unknown source-window coverage**: a successful canonical query alone does not
prove complete run history, so its native values remain visible but cannot
establish trusted recent movement until genuine coverage evidence is retained.
Deterministic evaluation reports use schema version 3 with separately named
`recentComparison` and `adoptionComparison`, under `adjacent-period-v1`.
Version 2 report artifacts remain readable without reinterpreting their fields.

Routine Actions and hosted collection no longer reconstruct historical windows.
The standard adapter queries existing canonical observations first and reuses a
complete set of repository/metric measurements within the contract's cadence
and the same exact measurement period. A new aligned cutoff triggers collection
even when the previous assessment is less than one cadence old.
Reused measurements retain their original timestamps; unchanged scheduled runs
do not fabricate additional history. A fingerprint of the adapter and outcome
module source invalidates reuse when either changes. Outcome modules must version changes to imported evaluator dependencies as part
of their measurement contract; source fingerprints alone do not cover transitive
dependencies.
Partial or missing results are not cached as zero. Collection still performs
credential and quota checks, and a cache miss may require bounded evidence
acquisition. Opening the dashboard performs queries, not collection or API calls.

The Actions and hosted collectors carry retained compact observations forward
for **365 days**, independently of the run collection window. Browser storage,
the SQLite projection and `repair-database` also keep a year of outcomes without retaining
extra run details; an explicitly longer SQLite run archive remains longer.
At weekly cadence this is about 52 observation instants per repository and
measure, rather than a copy of every run. Select a wider dashboard time window
to inspect older retained observations.
Single-observation timelines say **trend unavailable**; they require at least
two comparable observations. Baseline-comparable panels explicitly report when
no pre-adoption observations are retained. A longer retention window preserves
future evidence; it does not recreate already-lost observations or change
the frozen measurement cadence. Neither browser storage
nor an evictable Actions cache guarantees an all-time archive. Explicit
`cao operational-value --history-campaign CAMPAIGN --retention-days DAYS`
remains available for deliberate reconstruction where the frozen contract and
retained evidence support it; unavailable intervals remain missing. Do not run
that command routinely or during dashboard navigation. Historical reconstruction
must use retained snapshots or locally preserved authoritative facts; an adapter
that reacquires remote lifecycle evidence for each cutoff is not a low-cost
backfill path. No remote backfill is enabled by the longer retention default.

Native timeline dots remain selectable. Lines connect observations only within
the same campaign, repository, metric, native unit, direction, adoption context,
evaluator version, maturity, and declared cadence. Each outcome module supplies
its own frozen cadence with its measurements; the shared query and renderer do
not infer cadence from campaign names or metric identifiers. Lines break when the
elapsed interval exceeds that cadence. Legacy observations without cadence use
a conservative 24-hour display gap limit, while their cadence metadata remains
unknown. This connects nearby retained observations without claiming a recovered
measurement schedule or verified contract comparability. The distinction is
explained under Evidence details. Lines show observed movement, not continuous measurement or
causation. This uses existing worker query results and makes no additional API
calls.

The renderer is shared, not the outcome definition. Each campaign may contain
different workflow outcome contracts, with their own primary measure, diagnostics,
native units, direction, cadence, adoption and accepted evidence. Their panels
remain separate; a new contract needs no campaign-specific renderer. Campaigns
without an outcome contract remain unmeasured rather than inheriting another
campaign's metric or a synthetic common score.

Each native timeline keeps the value, chart, latest observation date and concise
comparison status visible. A collapsed **Evidence details** disclosure contains
the first/latest retained dates, adoption and baseline context, observation
count, and coverage/comparison limitations.
Those dates describe evidence, not live repository state. A pre-adoption dot
does not certify complete coverage of its measurement window: the current
observation records do not carry that coverage proof or a reason for missing
periods. Panels say so explicitly rather than inferring collection failure,
insufficient runs, or zero outcomes. Missing evaluator metadata is labeled as
an unavailable comparison; versioned panels without a supplied trend assessment
are labeled observed history, not "No trend yet".

Optimization supports **batched local backfill**. It sends all missing cadence
instants (at most 366) to its adapter in one call, reads the bounded canonical
run/workflow evidence once, and applies the unchanged declarative efficiency
query to each distinct window. Repositories sharing a window reuse its result.
This performs no GitHub API acquisition and requires a prepared local Activity
SQLite database. Use an explicit repository scope and retain the output shard:

```sh
node activity/cao.mjs operational-value \
  --database .cao/gh-aw-logs.sqlite --root . --campaign optimization \
  --history-campaign optimization --retention-days 365 \
  --repository github/gh-aw --output .cao/optimization-values.jsonl
```

Accepted cadence-bucket observations keep their real timestamps, and rerunning fills only missing
points. Expired run or AI Credit evidence cannot be recovered by this command;
windows with no eligible complete evidence emit no value, not zero. Retained
evidence is not a guarantee that an earlier acquisition covered every run.
This does not enable Dependabot remote lifecycle backfill or schedule backfill
as part of routine collection.

Canonical ingestion preserves known AI Credit and token measurements when a
sparse refresh omits them for the same explicit run attempt, including measured
zero. A newer supplied measurement takes precedence. Usage is never carried
across reruns or records without an explicit matching attempt. This applies to
duplicate observations and streamed storage batches without additional GitHub
API calls. It cannot recover evidence absent from every retained source.

Repository pages are outcome projections, not campaign projections. Reports and operational-value insights are grouped by their subject repository whether they were produced by a repository-local workflow or by a centrally executed worker. The report retains the producer identity `(runtime_repository, workflow_path)`, the durable output repository, and optional campaign membership as separate provenance. Local Actions health and AI Credit usage remain labeled as local execution data; a central worker run is not counted as a target repository run.

Collection is bounded by the configured repository scope and available credentials. Inaccessible downstream repositories are reported as incomplete coverage rather than inferred from another source. Cross-repository private collection therefore requires the deliberately scoped GitHub App extension described above.

Report implementation changes are released through this catalog. Use `gh aw update` to refresh the installed workflows and report modules, then review, commit, and push the resulting changes.

Pages report destinations are selected by the control-plane mode, while conventional GitHub Actions workflows perform the builds and deployments:

| Mode | Published result |
| --- | --- |
| `review` | Access-controlled review Pages in the private `safe_output_repo`. |
| `live` | Production Pages. |

To operate a report publisher:

1. Confirm the required source records are durable, approved for publication to the selected review or production audience, and free of data that audience must not receive.
2. Confirm the effective mode and that review routes only to `safe_output_repo` while live routes only to the production destination.
3. Confirm the build used fixed trusted source locations and the expected source revisions. Trigger inputs must not select arbitrary repositories, paths, commands, or generated site bundles.
4. Review the build and deploy jobs, including accessibility and link checks, the protected environment approval when configured, and the resulting deployment URL.
5. Verify report freshness, provenance, project-path assets, representative desktop and mobile views, and a visible review or production identity.

Review Pages must be private and access-controlled for the intended reviewers. If the repository plan or policy cannot provide that boundary, review publication fails closed. Never publish review content to a public fallback site. Agents must not receive `pages: write`, `id-token: write`, or authority to promote review content to production.

Setting a campaign's checked-in `enabled` field to `false` prevents new campaign work after policy resolution but does not remove an already deployed site. Changing its policy mode from `live` to `review` redirects future publication to review Pages but does not unpublish production. To stop or roll back either site, disable its conventional Pages workflow, use its protected environment to block deployment, or redeploy a known-good source revision through normal repository procedures. Handle sensitive-data exposure as a Pages incident in addition to stopping the affected agentic campaign.

## Emergency Stop

Disabling GitHub Actions for the private control repository is the control-plane-wide stop. It prevents new orchestrator and worker runs from starting, including manual dispatches. A repository administrator, or an organization or enterprise administrator with authority over Actions policy, should:

:::caution[Campaign switches are not an all-stop]
A campaign kill switch is evaluated only after a workflow starts. It does not cancel active runs or block unrelated campaigns and workflows. Disable Actions and cancel active runs when a complete stop is required.
:::

1. Open the control repository's **Settings > Actions > General** and disable Actions for the repository. An organization or enterprise administrator may instead apply an Actions policy that disables the repository.
2. Cancel every queued or running orchestrator and worker run from the repository's **Actions** page. Disabling future execution does not replace canceling work that has already started.
3. Revoke the GitHub App installation or PAT when credentials may be exposed or when repository access must be removed independently of Actions execution.
4. Record the stop time, initiating administrator, reason, active correlation IDs, affected targets, and any safe outputs already created.
5. Verify that the control repository has no queued or in-progress runs and that no new run can be manually dispatched.

This is intentionally a GitHub-native administrative control rather than checked-in workflow policy. Policy is evaluated only after a workflow starts and therefore cannot be the authoritative stop for all execution.

The stop applies to one central control repository. In a deployment with an enterprise control repository and additional organization control repositories, an enterprise incident commander must identify and stop every participating control repository that falls within the incident scope.

There is no global workflow-level kill switch across independent control repositories. Keep the approved control-repository inventory available outside any one runtime so incident commanders can enumerate affected installations even when a repository is unavailable. For each affected runtime, disable Actions, cancel active runs, and revoke its credential independently.

Use narrower controls when a full stop is unnecessary:

| Scope | Control | Limitation |
| --- | --- | --- |
| One campaign | Set `control-plane.campaigns.<campaign>.enabled` to `false`, deploy the reviewed revision, and cancel active runs | Stops campaign work after policy resolution; does not cancel work already in progress. |
| One Orchestrator or worker workflow | Disable that workflow in GitHub Actions | Other enabled workflows can continue. |
| Repository credentials | Revoke the App installation or PAT | Does not itself prevent runs that can use another available credential. |
| Entire control plane | Disable Actions for the control repository and cancel active runs | Also stops unrelated Actions workflows in that repository. |

To resume after an all-stop:

1. Resolve the incident and rotate or narrow credentials when needed.
2. Set every installed campaign's checked-in mode to `review` and `enabled` to `false`.
3. Re-enable Actions for the control repository.
4. Re-enable one campaign, run one `workflow_dispatch` target with `max_repos: 1`, and verify routing, permissions, and safe outputs.
5. Promote each campaign independently through the normal review gates.

## Incident Response

For unexpected writes, unsafe routing, excessive dispatch, or credential concerns:

1. Use the [emergency stop](#emergency-stop) when the incident affects shared control, authentication, or multiple campaigns.
2. Otherwise, set the affected campaign or worker's checked-in `enabled` field to `false` and disable a specific worker workflow when the incident is worker-local.
3. Cancel active orchestrator and worker runs; mode changes do not alter runs already in progress.
4. Revoke or rotate credentials when exposure is possible.
5. Trace `correlation_id`, `central_repo`, and `control_plane_run_url` across safe outputs.
6. Record affected targets and safe outputs.
7. Revert or close safe outputs through normal repository procedures.
8. Fix and compile the affected workflows.
9. Resume with a one-repository review run before returning to live.

Capture enough evidence to reconstruct the boundary and the outcome:

```yaml
stopped_at: 2026-08-25T14:30:00Z
central_repo: acme/central-agentic-ops
bundle: optimization
correlation_ids:
	- optimization-2026-08-25-001
affected_targets:
	- acme/example-service
safe_outputs:
	- https://github.com/acme/example-service/issues/123
credential_action: app-installation-revoked
```

Do not include tokens, private keys, or secret values in the incident record.

If shared authentication or shared control caused the incident, perform the control-plane-wide emergency stop. Otherwise, preserve unaffected campaigns.

## Publish a CAO Release

Dispatch the private **Release** workflow from `main` with a patch, minor, or
major bump. The workflow creates the version tag and a draft release, waits for
the successful exact-SHA `cao-runtime.yml` push build, verifies its checksum and
GitHub-hosted signed provenance, attaches the runtime archive and checksum plus
six version-stamped `gh-cao-OS-ARCH` extension binaries (`.exe` on Windows) and
their `checksums.txt`, and
publishes that exact release as an immutable prerelease with an explicit
`vX.Y.Z` tag. It verifies the release ID, tag, source revision, assets, and
immutable state before adding human-friendly release highlights.

Do not publish the draft manually. After the complete Release workflow succeeds,
review the generated notes, confirm the tag targets the intended `main` commit,
and confirm both runtime assets, all six extension binaries, and the binary
checksums are present. Then dispatch
**Promote prerelease to latest** with the existing version tag and obtain
independent approval through its protected environment after exact-source
validation. See [Release promotion controls](release-controls.md) for the
administrator setup and evidence requirements. The workflow's prerelease
publication makes the tag and assets immutable; no workflow can add, replace, or
remove release assets afterward. A failure before publication leaves the draft
unpublished; a later failure leaves a correctly tagged prerelease for diagnosis,
never a stable release that installation or update can select.

Promotion to latest makes those compiled binaries available to the root
installer's default selection. The installer downloads the platform binary and
checksums, verifies the embedded version, and registers it directly as `gh cao`.
An explicit `githubnext/gh-cao@vX.Y.Z` catalog source selects that same
extension tag. See [the CLI reference](cao-cli.md#github-cli-extension).

## Update CAO

Update campaign-owned workflows and runtime resources through a reviewable update proposal. Keep `.github/workflows/cao.json` unchanged unless the release requires an explicit, separately reviewed policy migration.

From the control repository:

```bash
gh cao update --major --cool-down 0
```

The command installs or upgrades `gh-aw` to the minimum version declared by `.github/workflows/cao.json`, resolves published GitHub releases, updates each installed CAO campaign to its latest compatible release, and refreshes CAO campaign worker declarations in policy without widening operator-owned rollout settings. Commit the resulting campaign-owned workflows, generated locks, shared runtime modules, ownership records, and policy declaration refresh as one atomic runtime revision. Do not fetch control files separately or copy them with a script. Parse `.github/workflows/cao.json`, reject unresolved placeholders, and run one bounded review target before restoring scheduled or live operation. Never edit generated `.lock.yml` files or `.github/aw/campaigns/*.json` ownership records by hand.

Stable releases are used by default. Pass `--pre-releases` to include published prereleases when selecting the latest compatible release.

To explicitly select the catalog's default-branch HEAD or a particular commit instead of a release:

```bash
gh cao update main
gh cao update master
gh cao update 0123456789abcdef0123456789abcdef01234567
```

The positional ref accepts `main`, `master`, `latest`, or a 7-40 character hexadecimal commit SHA. Both `main` and `master` select the CAO catalog repository's default branch as reported by GitHub, regardless of its actual name; they do not use the control repository's branch. Options can appear before or after the ref, for example `gh cao update --force main`. CAO resolves the ref once through the catalog repository to a full SHA and reapplies every installed CAO package at that same immutable revision, then verifies the recorded revision before materializing its runtime. This is a replacement, not a three-way merge: campaign-owned files and local edits to them are overwritten. Operator-owned rollout policy is preserved, apart from refreshing campaign worker declarations. Review unreleased code before enabling it.

After installing an untagged default-branch or SHA revision, return to published releases explicitly:

```bash
gh cao update latest
```

`latest` selects the highest published stable `vMAJOR.MINOR.PATCH` version across all release pages, excluding drafts, prereleases, and non-version tags, and pins its full commit SHA for every installed CAO package. It intentionally bypasses major-version compatibility and cooldown selection, so review the selected release before committing. Once installed, subsequent `gh cao update` commands follow release-based selection again. Without a release tag on the installed revision, a bare update stops with guidance to use `latest` rather than guessing a compatible release.

The selected release must contain the canonical runtime resources required by the materializer. Incomplete or incompatible historical releases fail materialization; `latest` does not bypass that check. As with other update failures, inspect the working-tree changes before committing or retrying.

Ref updates accept gh-aw add options such as `--no-security-scanner`, `--engine`, and `--dir`; `--force` is implicit. Release-update options such as `--major`, `--cool-down`, and `--pre-releases` are rejected with an explicit ref. Omitting the ref retains the existing release-selection behavior.

As in the root installer, ref updates skip gh-aw's Markdown security scanner for the root package because its GitHub App setup resource embeds an intentional HTML form. Other campaigns retain normal scanning unless `--no-security-scanner` is explicitly passed.

Existing control repositories whose campaign records predate the campaign-owned `.github/workflows/shared/` runtime must update before running CAO so `control.mjs` and `policy.mjs` are materialized beside `control.md`. Admission intentionally fails closed when those canonical source-path resources are missing.

### Catalog Release Revocation

A catalog maintainer cannot remotely disable workflows already installed in independent control repositories. When a campaign release is unsafe:

1. identify the affected published release and publish a known-good replacement release;
2. identify installations through campaign manifests and the approved control-repository inventory;
3. commit `enabled: false` for affected campaigns and cancel active runs in every installation;
4. revoke credentials when repository access must stop immediately;
5. pin or restore the known-good campaign revision, compile affected workflows, and validate one review target;
6. update projected catalog versions and lifecycle status after validation;
7. resume each runtime through review and limited-live promotion.

Removing or retagging the catalog source does not revoke resources already materialized in control repositories at their canonical source paths. Revocation is complete only after every affected runtime is stopped, repaired, or has its repository access removed.

## Adding a Campaign

A new campaign should:

1. Define an orchestrator with a schedule and manual inputs.
2. Add the campaign and its workers to the closed JSON schema and declare them in `.github/workflows/cao.json`; review remains the default mode.
3. Import `shared/control.md` as `role: orchestrator` with a static campaign identity and request-only narrowing inputs.
4. Pass the stable lowercase slug through shared control's `campaign` input and document the matching target-authority entry.
5. Keep GitHub tools read-only.
6. Declare only worker workflow dispatches as orchestrator workflow safe outputs.
7. Document discovery, ranking, dispatch, completion, and no-op behavior.
8. Start in review mode and complete all promotion gates independently.

## Adding a Worker

A new worker should:

1. Require the standard control envelope inputs.
2. Import `shared/control.md` as `role: worker` with the same stable campaign slug as its orchestrator.
3. Use a target checkout separate from the safe-output repository when needed.
4. Request minimum permissions, tools, network access, and AI credits.
5. Declare narrow safe outputs with explicit count, file, branch, and destination limits.
6. Avoid repository discovery and downstream dispatch.
7. Support review mode before live operation.
8. Be added to exactly the orchestrators that are allowed to dispatch it.
9. Receive a checked-in `max-mode` ceiling when its risk or maturity differs from its campaign peers.

## Change Validation

Control changes should be validated with the pinned minimum `gh-aw` version. Compile every executable workflow affected by shared imports, not only the directly edited file. Then check:

```bash
npm test
npm run test:load
npm run compile
npm run docs:build
git diff --check
```

- zero compile errors and warnings;
- no duplicated workflow-local authentication blocks;
- campaign manifests and docs agree on variables and modes;
- review and live routing remain fail closed;
- worker safe-output limits remain intact;
- `git diff --check` passes;
- compile-generated metadata is handled according to repository policy.

Do not promote a control change and a new high-risk worker to live in the same step. Validate shared policy first, then promote worker behavior separately.
