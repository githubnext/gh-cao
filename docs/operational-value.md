---
title: Operational Value
description: Measure whether an agentic workflow's intended repository outcome is attained using a frozen evidence contract.
---

Operational value is the degree to which a workflow's intended repository outcome is attained across eligible opportunities, demonstrated by accepted evidence under a fixed measurement contract.

It measures repository outcomes rather than workflow runs, generated output, or an agent's assessment. A person or another system achieving the same outcome must count as success under the same contract.

For the short intent → output → Git evidence flow, see
[Define and measure workflow value](operational-value-how-to.md).

Open **Value** in the sidebar (the trophy icon) to see reviewed registrations,
collection health, native measurements and evidence-backed workflow result cards.
This top-level destination is the single discovery surface for workflow Value;
it is not repeated as a permanent workflow tab. Collection-health cards may open
a workflow-scoped detail page, while result and measurement cards open their
retained evidence directly.
The legacy **Operational Value** page and its **Repository measurements** tab
have been removed. Existing native observations remain in the canonical data
model; missing measurements are not zero value.
A result card appears when a validated report packet has been published.
For example, **↑ +38 files · Worse** means the
independently measured repository population increased by 38 since its displayed
first snapshot, against a **lower is better** goal. It is not an alert, a
pre-adoption baseline or a causal rating of the workflow. Select the card to
follow consolidated intent, retained safe outputs, distinct accepted changes and
their Git evidence; select a measured dot to inspect the exact counted items.
The card and native report share one canonical evidence packet, not a saved HTML
page. Opening either does not collect new evidence or silently fill missing
measurements. Each workflow still needs a frozen measurement contract and
published evidence. No packet does not mean unconfigured: an enabled collector
can be deferred before its first capture. Only an authoritative policy check
establishes an unconfigured or disabled state.
Health cards keep the decision time, last actual attempt, successful capture and
evidence cutoff separate. A newer failure or deferral does not make retained
evidence fresh. "No retained capture" describes available history, not every
historical archive.
These evidence routes keep their declared cohort and cutoff when the dashboard's
global display horizon changes; they are not new evaluations of that horizon.

## Publish a report once

After the skill has collected and validated fresh evidence, use
`gh cao value publish OWNER/REPO WORKFLOW --server URL --input REPORT-evidence.jsonl`.
This writes original facts through an administrator-authenticated API into the
hosted PostgreSQL canonical database. It does not upload HTML, execute evaluator
code or enable recurring collection. Exact retries are idempotent; conflicts
fail closed. Activity replacement preserves these publications and their
structural parents. The dashboard's active subscriptions see the new revision;
value-dependent query caches are revision-keyed so an old cached card cannot
hide the new evidence.

See [authentication and command details](cao-cli.md#publish-and-refresh-workflow-value).
Static dashboards receive the same envelope through Activity publication,
not a direct database connection.

## Recurring collection after opt-in

The skill designs the measurement once. **Once the frozen function and bounded
collector pass verification, the skill stages registration automatically for
that exact repository and workflow.** There is no separate enable step in the
normal skill flow. A report upload alone is not registration.
Unconfigured workflows are never discovered or evaluated automatically.

The skill uses `gh cao value enable ... --collector PATH --if-absent` to record
the selection under
`control-plane.campaigns.activity.workflow-values` in the control repository's
reviewed policy. The collector and policy must be committed and deployed
together; the repository must also be in the effective Activity collection scope.
For a remote collector, the skill includes its verified maximum as
`--api-requests N`; local-only collectors omit that option.
`gh cao value disable ...` persists `enabled: false` without deleting
measurements. Automatic registration preserves existing selections and explicit
disables; use `value enable` without `--if-absent` to deliberately re-enable.
Without an enrolled control repository or a verified collector, a function file
alone does not start collection.

The repository-local ECMAScript collector exports its frozen `definition` and
`collectValueEvidence({repository, workflow, observedAt, database, previous, workspace})`.
It returns the original schema-v2 `operational_value_evidence` envelope.
It may acquire bounded, read-only evidence missing from the canonical database;
measurement uses the same fixed rules, never a new model-generated assessment.
Keep immutable measurements and receipts in the supplied per-workflow workspace
for reuse. Uploaded report packets cannot register or execute collector code.

Remote acquisition requires a reviewed `api-requests` ceiling on the exact
registration. Activity completes ordinary collection first, reads the selected
credential's live capacity, preserves its existing reserve and admits a
collector only when the entire reviewed ceiling fits above that reserve. The
collector receives a secret-free durable grant; every uncached REST, search,
artifact or GraphQL request consumes one unit atomically before execution.
Cached reads consume no new unit. Exhaustion can overcount a failed request but
cannot undercount or issue another request. Missing enforcement or insufficient
capacity defers before invocation and leaves accepted packets, immutable proof
and freshness clocks unchanged. Fast mode, public-only mode and observed API
exhaustion keep their more precise deferral reasons. A separate worker alone
still does not isolate shared quota.

Collectors without remote requests need no ceiling. Git transport and immutable
blob measurement remain under their separate existing bounds rather than being
misrepresented as GitHub REST requests.

Bounded cache-only/local computation remains available separately.
`CAO Local Value` runs only after successful default-branch Activity publication,
checks the exact snapshot and policy digest, and uses the reviewed local-only
adapter without target credentials. Local-capable modules (currently Optimization)
may query that snapshot; remote modules may only reuse exact versioned observations.
Older/custom adapters defer rather than ignoring local-only mode. The job has a
five-minute ceiling, with two minutes per computation batch and thirty seconds
per adapter; it does not backfill history. Existing fast, public-only and
API-limited gates remain. A later Activity snapshot imports completed results.
The hosted collector uses the same local runtime under a separate lease and
private snapshot, never the core projection lease. Publication rechecks enrollment
and the starting input, policy and retained evidence; a concurrent change defers
the result rather than overwriting newer evidence. Completed-cache imports reject
conflicting measurements at an identical observation identity.

Collection decisions and actual attempts retain independent clocks. Reordered
or repeated health observations cannot erase a known attempt or restore an older
registration. A missing attempt remains unknown; accepted capture and cutoff
dates still come only from measurement evidence.

Opening a report does not trigger collection. A failed acquisition, timeout or
invalid result leaves previous evidence and its capture/cutoff timestamps intact;
it is not a fresh success or zero. Explicit operator evaluation and one-time
publication remain separate, subject to their existing authority and budgets.

### Daily File Diet collector

`.github/value-collectors/daily-file-diet.mjs` reuses the existing read-only
File Diet acquisition and report instrument for `github/gh-aw` only. This
source-managed control-repository collector is not installed into catalog
consumers or registered there automatically. It keeps
the named retrospective 800-line structural measure unchanged; full
behavior-preserving acceptance remains unverified. The collector retains
immutable inventories, applied receipts and all previously accepted snapshot
cutoffs under its supplied workspace's `latest/proof/` directory. Failed or
incomplete refreshes do not replace that accepted proof.
On a cold start it retrieves only the skill's historical evidence archives at
immutable source revision `0782148c0af21f4f1d10b7cb5e81b571ed58c463`, validates
their digests, then acquires fresh discovery and lifecycle evidence. This
preserves verified receipts after upstream artifact expiry without treating the
historical capture as fresh. Subsequent collections continue from their retained
proof and cutoff: discovery and workflow-source history cover only the new
interval, immutable merged pulls, receipts, snapshots, Git inventories and blob
measurements are reused, and only mutable lifecycle evidence is refreshed.
Rich evidence acquisition has a ten-minute per-collector runtime ceiling
(the hosted wrapper allows ten additional seconds for shutdown), independent
of the shorter compact-measurement budget.

The same reviewed registration pins
`.github/value-evidence/daily-file-diet.jsonl.gz` to decoded envelope SHA-256
`9afaf3b49a02c1eda0c634016bae98bf0be0a5c1b63f1ae6c6353b151301d19e`.
That compact retained original reproduces the October 18 schema-v2 packet
without another checkout or network request. Activity validates and merges it
through the shared acceptance boundary while publishing the current recurring
collector deferral independently. Its `2026-10-18T12:05:52.580Z` capture and
`2026-10-18T11:54:09Z` cutoff remain historical; a later Activity run does not
retime either clock.

The control policy explicitly lists the same six repositories previously
collected from `scope.allowed-repositories`; this pins Activity enrollment
without adding targets or activating Daily File Diet itself. Only the exact
File Diet pair is registered for recurring Value acquisition.
Registration is visible after the collector and policy are reviewed and
deployed together. Its reviewed `api-requests: 700` ceiling allows Activity to
refresh it automatically only when that full grant fits above ordinary
collection's protected reserve.
It does not override `campaigns.dashboard.deploy: false`:
this repository intentionally disables standalone Pages deployment because
Documentation Pages owns the combined site. The existing publication chain is
CAO Activity, then the CAO Dashboard artifact/cache, then Documentation Pages
mounting that artifact at `/cao/`. No hosted OAuth session is needed for this
Pages path. Verify the collected packet in the deployed snapshot after that
chain completes; acquisition or merge alone does not prove publication.
A separately selected hosted publication still needs its actual server URL and
administrator OAuth session.

## Existing compact repository measurements

CAO uses one shared campaign module for both collection paths:

- Explicit `cao operational-value` collection evaluates the current repository observation and publishes compact numeric records to the canonical data model. Automatic local-only computation uses the same contract without remote acquisition. Retention keeps one observation per declared cadence bucket: closed cadence-aligned observations remain fixed, while repeated collections replace the latest observation in the still-open bucket.
- Explicit historical evaluation incrementally fills missing immutable observations and produces the evidence archive, timeline, chart, and definitions page.

Both paths import the same frozen evidence contract, collector, and scoring functions. Default routine collection does not rebuild repository history, and a metric cannot drift between the dashboard record and its historical report.

Accepted operational-value observations are queryable from any local or downloaded canonical SQLite snapshot with `cao query --collection operationalValues`. Querying alone does not publish or reconstruct history. An explicit historical evaluation may query missing cadence observations for a named campaign while their authoritative source evidence remains in its bounded canonical database. The automatic local lane does not request that backfill. No history archive is installed with the campaign. Completed numeric records use the ordinary Activity snapshot pipeline; Dashboard artifact production and the configured Pages deployment owner remain unchanged.

The rolling Activity database is a query source for current observations, not the definition of operational-value history. Explicit evaluation schedules observations from adoption and incrementally preserves accepted snapshots. Git and other immutable repository facts can be reconstructed at historical cutoffs. Ephemeral run, usage, artifact, queue, and tool-call facts can be backfilled only while their source remains available; an expired interval without a contemporaneous snapshot is missing, not zero. Compact operational-value history retains its separate 365-day window, compacted to each metric's declared cadence plus its latest open-bucket observation, while the explicit evidence archive is the durable since-adoption record.

## Standalone design reference

The [operational-value reference prototype](https://github.com/githubnext/gh-cao/tree/main/examples/operational-value)
contains a seven-scene storyboard, a dependency-free synthetic model, tests,
and a reproducible operation-count study with retained offline-replay inputs.
It explores independent observation cadence, optional linked-integration
questions, selective computation, and evidence timing. It is **not a production
collector or dashboard feature**: the storyboard uses fictional Go-file counts,
while the executable study uses a separate synthetic JavaScript-file metric.
See its README for local viewing, exact-runtime replay, and limitations.
