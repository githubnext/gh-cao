---
name: add-operational-value
description: "Define and measure workflow value from Markdown intent, accepted safe outputs and independent repository evidence. Design frozen value functions, evaluate outcomes and create deterministic HTML/JSON reports, including acceptance-timed Git contribution. Usage: /add-operational-value OWNER/REPO [WORKFLOW-NAME] [--campaign CAMPAIGN-SLUG]."
argument-hint: "OWNER/REPO [WORKFLOW-NAME] [--campaign CAMPAIGN-SLUG]"
allowed-tools: bash node gh
metadata:
  version: "0.9.0"
---

# Add Operational Value

## Operational Value

**Operational value is the degree to which the workflow's intended repository outcome is attained across eligible opportunities, demonstrated by accepted evidence under a fixed measurement contract.**

- An **eligible opportunity** is a repository event or state to which the intended outcome could apply.
- The **intended outcome** is the repository state the workflow exists to advance.
- **Accepted evidence** is a repository-observable fact that proves an eligible opportunity reached that state after any maturation period.
- Value is outcome attainment, not workflow execution, output volume, or the agent's assessment. Count evidence of attainment regardless of whether a workflow, person, or other system produced it.
- Baseline-comparable and attainment-only describe historical evidence capability, not permission to compare recent periods. Both may report observed change between complete comparable post-adoption periods. Only a valid pre-adoption reference supports an adoption-relative comparison, and neither establishes causation.

Design the evidence contract only from information available by adoption. Never use post-adoption results to choose evidence, formulas, targets, or classification.

Do not confuse workflow adoption with creation of the measured phenomenon. Before classifying any measure, ask: **Could this exact repository outcome and native measure exist before the workflow was adopted?** AIC per successful run, failure rate, file size, issue resolution, and other repository outcomes often predate the workflow even when the workflow-specific recommendation, trace, or output does not. Test the outcome measure itself, not the workflow mechanism.

Do not derive the primary measure from a gh-aw workflow's own output, recommendation, issue, trace, run, or engagement signal. Ask first: **What pre-existing repository problem caused this workflow to exist, and which repository-native facts would prove that problem improved?** Measure that problem independently of the workflow and count improvement regardless of whether it was produced by a maintainer, Dependabot, another automation, or the workflow. Adoption-timed movement is association, never causation.

## Intent, acceptance and measurement

**Intent defines what to measure; cadence tracks repository state;
acceptance anchors linked contribution.**
Recover the intended outcome and checks from workflow Markdown, imports,
precomputes and the effective executor. Translate them into a fixed value
function before inspecting results; do not invent a new goal or score.

For linked contribution, follow an applied safe output to its accepted
implementation. A proposal issue may lead to a PR implemented by a person or
another agent. Record the explicit relationship, final acceptance state and
delivery time. For merged code, evaluate the native condition at the merge's
actual first parent and merge revision, after any required maturation period.
Output creation and a green run are not acceptance; merge alone does not prove
every quality safeguard. Pending, rejected and unknown outcomes remain visible.
Do not confuse absence of a literal `connected` event with absent dated
lineage: closing keywords may produce a `CrossReferencedEvent` and a
`ClosedEvent.closer` pointing to the PR. Preserve the later closure timestamp
separately from merge time; a current closing declaration alone is undated.

Explicit lineage plus time-aligned native measurements demonstrate the named
contribution, not exclusive causation. Count each delivery once at its original
acceptance time, even if stronger receipts arrive later. Keep the independent
primary repository measure separate: improvements by anyone must count.
Preserve full-acceptance checks when reporting a separately named, versioned
partial improvement. Missing quality evidence and missing attribution receipts
qualify different claims; neither is an invented zero.

Do not sample only at accepted outputs: that would condition observation on
delivery and miss independent changes, deterioration, no-op periods and
reversals. Measure the actor-independent primary at the frozen cadence whether
or not the workflow runs or anything merges. Event-aligned before/after
measurements are additional contribution diagnostics and retain zero/negative
effects. Where the outcome is response to a repository change, retain that
opportunity's start time and measure response time or time in the adverse state
under the agreed contract; do not wait until a positive result to observe it.

Acceptance latency varies. Keep opportunity/start, delivery/acceptance,
observation and maturation clocks separate. Follow cohorts from opportunity
creation and compare the same elapsed follow-up, not only completed cases.
An unresolved opportunity is right-censored at the observation cutoff, not a
zero, rejection or failed outcome. Report pending counts and ages, coverage and
time-to-acceptance evidence; later checks and reversals remain visible. Do not
choose a maturation interval, causal lag or follow-up horizon after seeing
results, or claim a reaction was caused by the workflow from timing alone.

## Inputs

Use `/add-operational-value OWNER/REPO` to list repository workflows or `/add-operational-value OWNER/REPO WORKFLOW-NAME` to design one. Add `--campaign CAMPAIGN-SLUG` to restrict selection to workflows directly included by one campaign and place the resulting module under that campaign.

- `OWNER/REPO`: repository containing the workflow. Outcome evidence may come from other affected repositories.
- `WORKFLOW-NAME`: filename stem using lowercase letters, numbers, and single hyphens, such as `example-workflow`. Do not accept a path or `.md` suffix.
- `CAMPAIGN-SLUG`: optional top-level campaign directory containing `aw.yml`. It scopes workflow selection to directly included workers and owns the shared CAO adapter. Never infer it from a workflow name.
- After validation, set `WORKFLOW-PATH` to `.github/workflows/WORKFLOW-NAME.md` and use `WORKFLOW-NAME` unchanged as the slug.
- Never infer the repository, workflow, or campaign from the workspace, environment, or Git remotes.

## Workflow

1. **Resolve the workflow.**
  - Without `WORKFLOW-NAME`, run `.github/skills/add-operational-value/scripts/list-repository-workflows.mjs OWNER/REPO`, appending `--campaign CAMPAIGN-SLUG` when supplied. Campaign-scoped results contain only directly included worker workflows; never offer an orchestrator as a choice. Reproduce every returned name in the assistant response as a Markdown list, ask the user to select one, and stop. Tool output is not user-visible; never say the list is "shown above." Do not summarize or truncate it.
  - With `WORKFLOW-NAME`, run the same command with `WORKFLOW-NAME` before the optional campaign flag. Use its output as `WORKFLOW-PATH`. Campaign-scoped resolution must reject orchestrators and workflows not directly included by that campaign. If resolution fails, ask for a valid name.
  - Design exactly one selected workflow per invocation. A campaign scopes ownership and selection; it does not combine distinct workflow outcome contracts.

2. **Protect an existing design.** Run `.github/skills/add-operational-value/scripts/value-function-path.mjs OWNER/REPO WORKFLOW-NAME [CAMPAIGN-SLUG]`. Without a campaign, the default package is `WORKFLOW-NAME`. With a campaign, the canonical module is `CAMPAIGN-SLUG/operational-value/WORKFLOW-NAME.mjs` and the shared adapter is `CAMPAIGN-SLUG/operational-value.mjs`. If the module exists, verify it with `.github/skills/add-operational-value/scripts/verify-value-function.mjs --cao-adapter <adapter> <path>`, report that it was left unchanged, and continue at step 7. Do not inspect post-adoption evidence or redesign it unless the user explicitly identifies a contract flaw or requests reconsideration. In that case, preserve the ex-ante boundary, ignore post-adoption outcomes, and continue at step 3.

3. **Recover adoption-time intent and measurements.** Run `.github/skills/add-operational-value/scripts/extract-workflow-intent.mjs OWNER/REPO WORKFLOW-PATH`. Treat its adoption commit as adoption and its first parent as the baseline; if there is no parent, no historical baseline exists. Infer intent from adoption-time frontmatter, imports, instructions, and compiled workflow. Use triggers and skip rules to identify opportunities, and checkout, tools, permissions, and safe outputs to identify accessible or affected repositories.
  - State the pre-existing repository problem in repository-native terms before inspecting workflow-produced outputs. Identify the adverse state, backlog, risk, cost, latency, or reliability condition that motivated intervention.
  - Inventory direct measures of that problem first. Include outcomes that a person, native platform feature, or other automation could attain without this workflow.
  - Inventory every measure that adoption-time deterministic setup or precompute calculates, gates, ranks, or passes to the agent.
  - Inventory every measure the workflow instructions explicitly require the agent to calculate, compare, preserve, or use as an acceptance check.
  - Inventory which required facts already exist in canonical CAO collections and can be selected with a declarative query. Record the collection, fields, relationships, authoritative timestamps, and source-retention limit.
  - Inventory which required facts exist only in repository history or content, such as files, syntax, dependencies, ownership, commits, diffs, issue state, or pull-request state. Record the repository and immutable cutoff needed to reconstruct them.
  - Preserve each measure's exact target repository, population, grain, evidence window, filters, comparison rule, and missing-data behavior. A target checkout is strong evidence that the measured outcome belongs to that target, not to the repository hosting the workflow.
  - Do not treat workflow-native measures as the first operational-value candidates. Classify each as an acceptance gate, diagnostic, or mechanism/output proxy. A workflow-produced artifact or engagement signal must not be the primary measure.
  - Select primary candidates from repository-native problem state. For dependency operations, test security-alert backlog and severity, outdated-dependency backlog, dependency-update pull-request backlog, and remediation duration when adoption-time scope and evidence support them.
  - For every direct-outcome candidate, perform an **antecedent-existence test** before choosing an evaluation mode:
    1. Could the same eligible opportunity and repository outcome occur before adoption, including through human or other automation?
    2. Could the same native formula, unit, population, grain, filters, and window be applied before adoption without referring to a workflow-generated artifact?
    3. Which authoritative facts would reconstruct that pre-adoption measure, and were those facts retained, preserved, or immutable?
  - Record one result per candidate: `pre-existing-and-reconstructable`, `pre-existing-but-not-reconstructable`, or `created-at-adoption`. Never infer `created-at-adoption` merely because the current collector, event type, recommendation, or workflow trace begins at adoption.

4. **Design and test once.** From adoption-time intent and pre-adoption facts, form one provisional contract and test it with one batched pre-adoption probe. The probe is mandatory for every candidate classified `pre-existing-and-reconstructable`; do not skip it because the workflow was not yet installed or because current reports begin at adoption. Fetch only cutoffs at or before the baseline commit time; never fetch, inspect, or compare recent or post-adoption outcomes during design. One valid baseline observation is enough to establish comparability. Resolve discoverable facts without asking the user. The contract must include:
  - operational value stated as "For [eligible opportunities], attain [repository outcome], demonstrated by [accepted evidence]";
  - eligible opportunity population;
  - accepted evidence and evidence repositories;
  - deterministic opportunity/outcome matching rule and maturation period;
  - output-to-implementation lineage, final acceptance predicate, delivery-time
    anchor, immutable before/after references and evidence-observation time for
    each proposed contribution diagnostic;
  - observation window, cadence, evidence shape, and zero-versus-missing rule;
  - evidence source plan, including canonical queries, immutable repository facts, source retention, and historical backfill capability;
  - whether the outcome can occur without the workflow;
  - the antecedent-existence result and evidence for every proposed primary or diagnostic measure;
  - disposition of every workflow-native measure as primary, diagnostic, gate, or rejected proxy;
  - disposition of every repository-problem measure considered and why any reconstructable native outcome was not selected;
  - proposed primary and diagnostic measures;
  - classification as baseline-comparable, attainment-only, or not measurable.

  Test repository-native problem measures before workflow-native measures. Reuse workflow-established evidence grain, windows, filters, and safeguards only when they apply unchanged to the independent repository outcome. A proposed estimate, self-assessment, recommendation, produced report, plan issue, engagement event, or workflow run remains a mechanism proxy and cannot be primary operational value.

  Choose evidence sources in this order:
  1. Use a declarative query over canonical CAO data when it contains the authoritative facts and its retention covers the observation being collected.
  2. Use `gh api` at immutable commits or historical event cutoffs for GitHub and repository facts that canonical data does not contain.
  3. Use an archive or temporary checkout of the target repository at the immutable cutoff only when repository-wide file or code inspection is necessary.
  4. Combine queried activity facts with immutable repository facts when the outcome contract requires both. Join them inside the collector's evidence object; do not derive business state in dashboard or presentation code.

  Source convenience never changes the outcome contract. Do not replace missing code, Git, issue, pull-request, or acceptance evidence with an available run metric. Do not clone or inspect the control repository when the outcome occurs in a dispatched target repository.

  Apply this test: **if a person achieved the intended outcome without the workflow, would the primary measure record success?** If not, it measures mechanism rather than operational value. Use one workspace-local temporary directory for the entire probe, reuse downloaded evidence, and remove it afterward. Use `gh api` for GitHub data; do not use `/tmp`, `${TMPDIR}`, `curl`, or `wget`.

  Classify the tested contract:
  - **Baseline-comparable:** the measured phenomenon existed before adoption and valid comparable outcome evidence can be reconstructed. Apply one symmetric measure before and after adoption. This is the required classification whenever at least one valid baseline observation exists.
  - **Attainment-only:** the future outcome contract is valid, but either the measured phenomenon was genuinely created at adoption or it pre-existed and the same measure cannot be reconstructed after exhausting canonical, preserved, and immutable evidence. Begin at adoption and make no adoption-relative improvement claim. Complete comparable post-adoption periods may still show recent improvement or deterioration. State which of those two reasons applies; “the workflow did not exist yet” is not sufficient.
  - **Not measurable:** no deterministic opportunity population, repository outcome, or accepted-evidence rule can be defined. State what is missing and stop without creating a function.

  Eligible opportunities plus evidence that none attained the outcome may establish zero; absent evidence does not. Never treat missing workflow-generated traces as zero when the outcome could occur another way.

5. **Confirm once.** Present the tested operational value, complete contract, retained and rejected metrics, and classification in one concise response. Ask one confirmation question covering the whole design. Stop there; do not inspect implementation scripts or begin implementation until the user confirms. Do not ask separate outcome, contract, metric, or classification questions. Ask earlier only when one unresolved decision prevents a valid pre-adoption probe.

6. **Implement and freeze.** For a confirmed measurable design:
  - Freeze the confirmed evidence object, collection plan, metric roles, formulas, and classification. Do not reconsider or replace them during implementation.
  - Reuse deterministic adoption-time measurement logic when it is still the confirmed source of a retained measure. Extract a shared helper when practical so workflow precompute and repository-level evaluation cannot drift; otherwise port the exact grain, window, filters, and missing-data behavior and test their parity.
  - Select exactly one direct primary metric using outcome relevance plus pre-adoption observations and validation examples. Never use post-adoption results to compare metric sensitivity. Do not use a population-wide rate dominated by unaffected items when a direct severity or attainment measure better exposes the intended change. For thresholded populations, compare worst-violation severity, compliant opportunity share, and compliant mass share when the same snapshot supports them; retain only dimensions that can meaningfully disagree. Do not normalize raw counts with arbitrary transforms. Never invent normalization caps, targets, or weights absent from pre-adoption evidence.
  - Create the canonical shared module directly from the Module Interface below. Do not search report directories for examples or conventions.
  - The module owns the frozen contract, batch collector, and scoring functions. It must not read stdin, emit output, or depend on the CAO or report adapters.
  - Reuse the standard package-level `operational-value.mjs` adapter for every workflow in a campaign. It discovers all modules under the campaign's `operational-value/` directory, collects one current observation per supported repository and module, and emits namespaced CAO JSONL IDs in the form `WORKFLOW-SLUG.METRIC-ID`. Namespacing prevents collisions between workflows in the same campaign.
  - For a campaign, run `.github/skills/add-operational-value/scripts/wire-campaign.mjs CAMPAIGN-SLUG WORKFLOW-NAME`. It creates the standard adapter only when absent, refuses to overwrite a nonstandard adapter, and idempotently includes the adapter and selected module in `aw.yml`. Resolve a nonstandard existing adapter deliberately; never replace it automatically.
  - Without a campaign manifest, create the standard adapter beside the module but do not create or enroll a campaign solely to host an evaluation example.
  - Run `.github/skills/add-operational-value/scripts/verify-value-function.mjs --collector --cao-adapter <adapter> <path>` and do not report success unless it passes.
  - For ongoing native Value reports, implement and test the bounded
    `collectValueEvidence` interface described below using the same frozen
    definition and acquired originals. Once both function and collector pass
    verification, automatically stage registration for the selected pair with
    `gh cao value enable OWNER/REPO WORKFLOW --collector PATH --if-absent`.
    This is part of implementing the confirmed design, not a separate enable
    question. Use it only in a control repository with an existing reviewed
    policy and that repository already enrolled in Activity. Otherwise state
    that deployment/registration remains a prerequisite; never widen scope.
    Preserve existing registrations and explicit disables. Review, commit and
    deploy the collector and policy together before collection starts.
    When reusing an existing verified design at step 2, apply this registration
    only if its recurring collector already exists and passes verification;
    do not rewrite the design or invent a collector from an old report.
  - Report the outcome contract, evidence shape, metrics and roles, agreement or disagreement, rejected metrics, limitations, selected model, generated module and adapter, and successful verification command. Do not create evaluation artifacts yet.
  - For an acceptance-anchored contribution report, implement a deterministic
    projection of the acquired evidence into the
    [event ledger contract](references/event-ledger.md). This projection must
    use the frozen native rule and independent postcondition checks, not PR
    counts or the model's assessment. It is not a new primary metric or a
    substitute for the module's strict acceptance diagnostic.
  - Include a bounded acquisition entry point for facts absent from canonical
    data; a renderer plus a historical replay is not a current-evidence
    implementation. Reuse `scripts/github-evidence.mjs` for workflow/tracker
    identities, execution markers, run identity, dated issue/PR graph and
    timelines, and `scripts/git-evidence.mjs` for immutable native inventories.
    Acquisition may read GitHub; window scoring and rendering must remain
    offline. Preserve the acquired proof so another run can replay it.
  - Discover candidates using exact `gh-aw-workflow-id`, `gh-aw-tracker-id`,
    versioned `gh-aw-execution` markers, named legacy footers and declared
    workflow-specific search rules. Tracking IDs locate outputs, not accepted
    value. Verify the referenced producer repository, workflow path, run,
    attempt and revision when retained; compare applied safe-output receipt
    identities when available. A footer is not an applied receipt, a deleted
    run is explicit unavailable evidence, and a title match alone is not origin
    proof. Never follow a reference into an undeclared repository.
    If a retained artifact names a different created output, keep that
    contradiction explicit rather than crediting the copied tracking/footer
    text; auxiliary tracked outputs need not be eligible outcome opportunities.
  - Join actual output IDs through dated implementation relationships to the
    Git graph. Deduplicate implementations, use the merge's actual first
    parent rather than its PR base field, and measure all eligible replacement
    files. Keep producer, issue, connection, merge, closure, observation and
    required maturation timestamps distinct. Preserve incomplete pagination,
    source retention and unobserved/deleted-output limitations.

7. **Offer deterministic evaluation.** Ask whether to evaluate the verified function now. If declined, stop. If accepted, capture one UTC endpoint and run:

  ```bash
  END_AT=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
  .github/skills/add-operational-value/scripts/evaluate.mjs --report --end "$END_AT" [--campaign CAMPAIGN-SLUG] OWNER/REPO WORKFLOW-NAME
  ```

  Use the repository and slug from the verified definition. Never let evaluation evidence alter the frozen function, contract, formulas, targets, roles, or classification. Use `--refresh` only when explicitly requested. Report the endpoint and artifact paths. Describe baseline-comparable results as association, attainment-only results as attainment, and neither as causation. State that evaluation artifacts were generated deterministically from the frozen function. Offer AI interpretation only with explicit consent and label it as interpretation.

  `--report` writes the standard HTML/JSON pulse. If the module exports
  `collectEvents`, it also validates and archives the accepted-event ledger
  against the same frozen metric, intent, repository and evaluation endpoint.
  Otherwise it reports the existing timeline without inventing event deliveries.
  A complete matching archived ledger is reused; `--refresh` is explicit.
  When the user requests a report **as of today** or a fresh refresh, acquire
  evidence through the requested UTC endpoint first. Reuse verified immutable
  Git measurements and preserved receipts by identity/digest, but refresh
  mutable discovery and lifecycle state. Do not move the evidence-capture
  timestamp or extend complete coverage merely by rerendering an old ledger.
  Publish only after acquisition, validation and measurement succeed.
  For direct hosted publication, run
  `gh cao value publish OWNER/REPO WORKFLOW --server URL --input REPORT-evidence.jsonl`
  with the original generated envelope and the administrator's private session
  credentials. It publishes existing evidence; it does not acquire facts or
  register executable code. Local SQLite and static Pages have no such endpoint.
  Ongoing collection uses the reviewed local
  collector exporting the same frozen `definition` and
  `collectValueEvidence({repository, workflow, observedAt, database, previous, workspace})`,
  returning an original schema-v2 evidence envelope after bounded acquisition
  and validation. The automatic registration in step 6 records only the selected
  pair; function files alone are not runtime authority. Have the policy
  and collector reviewed, committed and deployed together. Do not widen Activity
  scope or enable the measured workflow. Recurring remote acquisition remains
  deferred until enforceable shared API budgets protect ordinary Activity.
  Registration is visible as enabled-but-deferred, not a promise of fresh
  measurements. Bounded local-only computations run independently on eligible
  published snapshots; they do not bypass fast, public-only or API-limit gates.
  An admitted collector must honor the frozen cadence; a failed refresh must retain
  the prior accepted evidence and timestamps. `value disable` revokes future
  collection without removing history and persists an explicit disable that
  automatic registration must respect. Re-enabling requires explicit
  `gh cao value enable ...` without `--if-absent`. Never opt every workflow in, infer
  opt-in from an uploaded packet, redesign the contract during collection or
  add a separate per-workflow schedule.
  The renderer can also replay either input directly:

  ```bash
  node .github/skills/add-operational-value/scripts/render-pulse.mjs INPUT_JSON REPORT-pulse.html
  ```

  It writes self-contained HTML, adjacent pulse JSON and, for event ledgers,
  `*-evidence.jsonl` containing validated originals for normal Activity ingestion
  and native dashboard reports. See [publication](references/event-ledger.md#shared-native-dashboard-publication).
  It makes no model calls,
  remote queries or a dashboard. Do not implement a custom chart or report for
  each workflow. The report preserves native units, separate outcome and
  contribution bases, explicit evidence gaps and inspectable event links.
  Adjacent 28-day event windows describe recent delivery; the overlapping 91-day
  curve is context, not a pre-adoption baseline. Without an agreed decision bar
  or matched costs, report **not rated** and **not measured**.

  Use the GitHub Agentic Workflows reader's vocabulary: consolidated workflow
  intent, safe outputs, accepted implementation and measured repository change.
  Link the extracted intent to immutable Markdown, imports and compiled sources,
  with the original requirements available on demand. Keep any separately named
  partial measurement distinct from the workflow's full acceptance criteria.
  Show a compact evidence trail of retained outputs, distinct linked accepted
  implementations and their native measured result, all with an explicit scope
  and clickable drilldowns. Do not reconstruct output creation from accepted
  events. Distinguish applied creation receipts from corroborated tracking;
  closed issues are not automatically accepted implementations. Output counts,
  implementation counts and native results are different populations, not an
  implicit conversion rate. Missing lifecycle data is unknown, not zero.
  Follow with a deterministic plain-language assessment and the independent
  repository state, its first-snapshot change and a compact, directly labelled
  snapshot chart. Keep recent results separate from the trail's historical scope.
  Make the snapshot goal direction explicit on the plot: **↓ Lower is better**
  or **↑ Higher is better**, without inverting the native axis. Show a signed
  native delta, comparison date and direction-aware Better/Worse/Unchanged
  wording; color alone is insufficient. Keep points clickable. Thin dashed
  straight connectors may guide the eye when labelled as guides, not continuous
  observations; do not add an area fill, smoothing or invented intermediate
  values. A single snapshot cannot establish change.
  Use a short human result label, such as "file reductions", when supplied by
  the collector; never make readers infer what identical units mean.
  Keep the default view to roughly 150 words, not an equal-weight audit table
  or a wall of cards. Show one linked unknown-quality caveat.
  Do not make the default
  report a tutorial: avoid card grids, a lengthy workflow walkthrough and repeated
  disclaimers. Keep missing coverage visible. Put goal-check details,
  investigation guidance, example implementations, elapsed times, rates,
  formulas, receipts and raw IDs behind one **Explore evidence** disclosure,
  with keyboard-accessible details inside. Do not invent a health score,
  causal verdict, pending count,
  reason for inactivity or keep/retire recommendation. Optional structured
  repository snapshots and verification checks follow the event-ledger
  contract; absence must say **not measured** or **not verified**.

  Treat attention as an inspectable exception, not a workflow health score:
  **Review results**, **Check evidence** or **No review triggered**. Show its
  measured basis and why it fired; no review triggered is not an all-clear.
  Keep evidence completeness and unverified quality separate from observed
  outcome movement. A future alerts dashboard may distinguish repository
  deterioration, ineffective accepted changes, overdue acceptance and missing
  evidence, but must not infer those reasons interchangeably. Overdue acceptance
  requires an agreed follow-up horizon and equal-age opportunity cohorts,
  including right-censored pending work. Do not invent alert thresholds,
  persistence windows, causal claims, notifications or automatic actions here.

  Follow GitHub Primer product foundations: functional light/dark color tokens,
  the system-fallback font stack, Octicons, restrained borders and clear link
  affordances. Keep charts consistent with that palette and typography; do not
  rely on color alone. Embed styles and icons locally, without remote fonts or
  runtime assets. Headline quantities must open the exact periods, retained
  implementations or independent snapshots behind them. Link chart marks to
  their retained evidence and Git revisions, with native keyboard-accessible
  links and working saved-report fragments. Unknown and zero periods need
  honest evidence drilldowns, not invented results. Label current workflow
  source separately from immutable measurement evidence; never construct a
  target-repository URL from an unrelated implementation-source revision.
  Do not stop at a repository tree when item evidence is available. A snapshot
  point must reveal the actual counted files or records, with immutable
  per-item links and native measurements. Link accepted implementations to
  their PR or diff and the measured before/after files, including replacements;
  do not invent links for deleted or unretained files. Keep whole-repository
  revisions as context and explicitly flag missing item-level evidence.
  Use restrained visual hierarchy, decorative icons and clear native units
  rather than more panels. Make evidence actions obviously interactive,
  keyboard-visible and touch-sized; respect reduced-motion preferences.

## Collect and report Daily File Diet

The skill includes a complete fresh acquisition-to-report entry point for
`github/gh-aw` / `daily-file-diet`. It uses `gh api`, Git and `unzip`; authenticate
with read access to the repository and retained Actions artifacts. It never
dispatches a workflow or mutates the target repository.

```bash
END_AT=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
node .github/skills/add-operational-value/examples/acquire-file-diet.mjs \
  "$END_AT" NEW_REPORT_DIRECTORY [PREVIOUS_PROOF_DIRECTORY]
```

Pass an unused report directory. Omit the optional previous-proof argument
for a clean acquisition. To refresh incrementally, supply a prior report's
`proof/` directory, or the pinned historical proof described below.
The command refreshes output discovery, tracking/run identity and lifecycle
graphs, then fetches missing Git objects and measures eligible files. Existing
verified blob measurements and applied-output receipts remain reusable even
after their upstream artifacts expire; unavailable new evidence is not invented.
Temporary Git and request caches are workspace-local and removed in `finally`.

The result directory contains `proof/`, `daily-file-diet-pulse-events.json`,
`daily-file-diet-pulse.json`, `daily-file-diet-pulse.html` and
`daily-file-diet-pulse-evidence.jsonl`. Acquisition fails
closed on identity mismatches, incomplete bounded search, changed adoption,
corrupt evidence or exhausted budgets. Do not claim full acceptance, historical
discovery completeness or causal impact from tracking and structural facts.

### Replay the preserved example

For native dashboard integration rather than standalone HTML, use
`examples/native-file-diet.mjs --proof PROOF_DIRECTORY --output .tmp/file-diet-native`.
It recomputes retained inventory measurements, writes original facts through
normal SQLite ingestion and executes the shipped Value query. It does not
remeasure Git blobs, collect GitHub data or enable a campaign. Its manifest
separates source/capture identity from processing costs. An optional already
approved `--archive-server ORIGIN` uses `scripts/session-archive.mjs` to bind
exact retained producer runs to generation-pinned session evidence; raw trace
content never becomes a public shard or acceptance proof. See
`docs/operational-value-how-to.md#native-reports-from-retained-evidence`.
For any other verified workflow, use the shared
`scripts/prepare-native-value.mjs --input REPORT-evidence.jsonl --sha256 DIGEST --output .tmp/workflow-native-value`.
This imports original evidence, not arbitrary intent or executable scoring.
It preserves reviewed registration, disables, collection health and source
clocks; hosted publication remains the authenticated `gh cao value publish` path.

Use the preserved `github/gh-aw` evidence from source revision
`0782148c0af21f4f1d10b7cb5e81b571ed58c463` on
`mnkiefer-workflow-operational-value`. Only the evidence directory is needed
from that checkout; use this skill's updated tools:

```bash
node .github/skills/add-operational-value/examples/daily-file-diet.mjs \
  SOURCE_CHECKOUT/docs/operational-value/proof/file-diet-lifetime \
  REPORT_DIRECTORY/daily-file-diet-pulse.html
```

The example validates preserved inventories and receipt identities, follows
explicit implementation links, and measures each merged PR at its actual
first parent and merge revision. It writes the input event ledger plus the
standard HTML/JSON pulse. Expected results: **48 net structural reductions**,
including a **13-reduction receipt-backed subset**, latest 28-day delivery
**0 files/week** versus **0.5** previously, and **worsening / review**.
Full behavior preservation remains unknown. This is replay of a saved,
outcome-informed retrospective instrument, not a fresh prospective design or
proof of causal impact. It does not require copying experimental executable
code or installing a campaign. Those expected numbers belong to the October 18
proof, not a target for fresh collections.

## Design Invariants

### Evidence

- Design ex ante: use only outcome facts known by the baseline commit. Use the adoption commit only for the workflow and contemporaneous repository context.
- Always separate **phenomenon existence** from **evidence availability**. First decide whether the exact outcome and native measure existed before adoption; then decide whether its facts can be reconstructed. A missing historical source can justify `pre-existing-but-not-reconstructable`, but it cannot turn a pre-existing measure into a post-adoption-only phenomenon.
- Apply the same-formula counterfactual: remove the workflow from the historical picture and ask whether the metric still has a coherent numerator, denominator, unit, population, and timestamp. If yes, probe it before adoption. Workflow-specific recommendations, traces, and outputs may disappear in this test; the repository outcome must not.
- Apply a problem-first counterfactual before considering any gh-aw signal: remove the workflow and all of its outputs, then ask which repository problem would still motivate intervention and which native facts would show that problem improving or worsening.
- Count outcome attainment regardless of actor. If a maintainer, Dependabot, another automation, or the measured workflow improves the repository-native problem, record the same outcome. Describe pre/post movement as association and never infer causation from adoption timing.
- Preserve an adoption comparison whenever historical evidence supports it, separately from default recent movement. Attainment-only is a fallback after an explicit failed reconstruction, not the default for a newly installed workflow.
- Evaluate evidence in the repository where the intended outcome occurs. When the workflow checks out or names a target repository, do not substitute the workflow-owning or control repository merely because execution happens there.
- Use files, history, pull requests, issues, and other relevant data already present in canonical CAO data, persisted precomputations, or an available target-repository checkout. For mutable records, reconstruct state at the baseline cutoff; do not use current fields or later events.
- Treat canonical CAO queries as the preferred access path for facts they authoritatively contain. Query retention is a source limitation, not the operational-value history window.
- Operational-value history begins at adoption for attainment-only designs and includes the fixed pre-adoption baseline for baseline-comparable designs. Routine CAO collection publishes only the current observation; explicit historical evaluation incrementally fills every reconstructable cadence-aligned observation and preserves accepted snapshots in the evidence archive.
- A short-lived source such as a rolling Activity database may support current observations without supporting historical backfill. If an older observation can be reconstructed from immutable Git or GitHub history, reconstruct it. If it depended on expired run, usage, artifact, queue, or tool-call facts, report that interval as missing unless a contemporaneous accepted snapshot was preserved. Never shorten the claimed history to the source-retention window or synthesize older values from the oldest available record.
- Record enough provenance to distinguish a live canonical query, an immutable repository reconstruction, and a preserved contemporaneous snapshot. Historical evaluation must reuse a preserved snapshot rather than reinterpret old evidence under a newer contract.
- Document the observation window before scoring. Every collected window must use the same duration, filters, opportunity denominator, maturation period, evidence shape, and formulas. Baseline-comparable designs apply this contract on both sides of adoption; attainment-only designs begin at adoption.
- Attainment-only collection may publish observations from adoption onward before a full window has matured. During that interval, collect the bounded partial post-adoption window from adoption through observation time rather than a pre-adoption window. Mark every such observation `maturityStatus: "interim"` and `dubious: true`, and describe its score as provisional rather than accepted attainment. Do not suppress an otherwise valid early observation solely because its window or outcomes are still maturing.
- Early observations must use the frozen formula and the evidence available at their observation time. Distinguish provisional zero from missing evidence, preserve unresolved counts, and never relabel an interim score as matured or use it to claim improvement.
- Use the least dense evidence sampling that directly measures the outcome. For repository-state outcomes, prefer one immutable cutoff snapshot per observation window; use daily or per-event assessments only when the accepted-outcome matching rule requires them.
- Do not call GitHub APIs to evaluate an interactive or historical window. API acquisition is too costly and rate-limited for arbitrary comparison. Precompute missing Git or repository facts during bounded Activity collection and persist them canonically; if a required fact was not preserved and is not available in a local checkout, mark that interval missing.
- Temporary checkouts and extracted evidence must remain under the workspace and be removed by the command that creates them.
- Every evaluator accepts explicit inclusive `windowStart` and exclusive `windowEnd` inputs. A dashboard may request any bounded windows covered by retained canonical data, preserved observations, persisted precomputations, or a local checkout; the evaluator must not hard-code a 30-day analytical limit.
- Separate measurement duration, sampling cadence, display horizon and reference period. Freeze the outcome formula, population rule and evidence window; version comparison policies separately without changing the evaluator to improve a reported result.
- For either evaluation mode, assess the latest native period without falling back to an older accepted result. Recent movement requires that period and the immediately preceding equal-duration period to be complete. Compare interval windows only when they abut without overlapping; compare snapshot state at the declared cutoffs, never sums of backlog snapshots. Do not substitute an older retained period across a gap. Daily seven-day rolling observations share six days of evidence and are not independent weekly comparisons.
- Preserve exact period boundaries, measurement kind, maturity, coverage, missing reasons, evidence lineage, eligible cohort identity and available numerator/denominator counts. Keep newer unavailable assessments visible. Missing, stale, partial, provisional or unknown evidence cannot establish a current all-clear. An immutable formula alone does not establish unchanged workload mix or comparable exposure.
- For `baseline-comparable` workflows, offer a separate adoption comparison between complete windows wholly before and wholly after adoption, using the same formula, eligibility rules and unit. Missing evidence makes that comparison unavailable, never an invented zero or the first available post-adoption value.
- For `attainment-only` workflows, never invent a pre-adoption baseline. Two complete comparable observations support observed post-adoption change, not a sustained trend, statistical anomaly or causal improvement. A single observation is attainment only.
- Retain an explicit stable reference or approved target separately from recent movement when available; stable-but-bad outcomes must not appear healthy merely because their latest delta is zero. EWMA, CUSUM, anomaly and change-point assessments need adequate suitable history and a separately calibrated, versioned policy. Do not apply independent-sample thresholds to overlapping windows or silently reset a reference after deterioration.
- Recompute bounded windows only from authoritative retained evidence or aligned non-overlapping sufficient statistics at the necessary grain before window-level eligibility. Preserve numerator and denominator separately; never average ratios or add overlapping rolling totals. A display-horizon change must not reinterpret the frozen evaluator.
- If pre-adoption evidence later becomes available for an attainment-only workflow, require a reviewed contract change before reclassifying it as baseline-comparable; do not silently change evaluation mode from a dashboard interaction. Treat discovery that the phenomenon predated adoption as a classification-review trigger even when evidence is still incomplete.

### Metrics

- Workflow-native measures from deterministic precompute and explicit workflow instructions are presumptive candidates. Retain them when they directly measure the intended repository outcome; otherwise preserve them only as gates or diagnostics, or reject them with a specific reason.
- Do not silently replace an established workflow-native grain, denominator, evidence window, safeguard, or missing-data rule with a convenient dashboard or collector proxy.
- Safe-output creation is activity, not acceptance. When relevant, classify its later repository state as `accepted`, `rejected`, `pending`, `ignored`, or normal lifecycle behavior after the fixed maturation period.
- Use the same evidence shape and formula for every window. The function scores one window; it never accepts `{before, after}` or calculates improvement.
- Preserve the native measure and its unit. Do not normalize operational value to `0..1`, complement decrease goals, or invent a dimensionless score solely to make unlike measures share an axis. Use `direction` to state whether higher, lower, maintenance, or proximity to a target is better.
- Derive metrics, targets, units, and weights only from the workflow goal, documented repository standards, and pre-adoption evidence. Never tune them using post-adoption results.
- Prefer direct outcomes and opportunity-normalized rates over raw counts and weak proxies.
- Treat metric disagreement as evidence; do not average it away.
- A repository-scoped metric may declare additive rollup numerator and denominator evidence fields only when summing those fields and dividing the totals is mathematically identical to measuring the combined eligible-opportunity population in the metric's native unit. Never average repository ratios to produce a campaign value.
- Use composites only for distinct dimensions; document weights, gates, overlap, and missing-data behavior.
- For baseline-comparable designs, require a non-null score from collected baseline evidence for every retained metric and reject metrics whose opportunity population is empty at baseline. If defensible baseline evidence is unavailable but the future contract is valid, classify the design as attainment-only and state the missing historical interface. Do not fabricate evidence or convert absence of workflow-generated traces into zero outcome attainment.

### Shared Module Interface

- The value function is a self-contained importable ECMAScript module and uses only Node.js built-in modules plus repository-local deterministic tools. Window evaluation does not call GitHub APIs.
- It exports `definition`, the current JSON contract containing `schemaVersion`, repository, workflow, adoption, `evaluation.mode`, frozen evidence contract, model, summary, metrics, and validation examples. Mode is `baseline-comparable` or `attainment-only`. `schemaVersion` is the only interface version; do not add separate collector version or command metadata.
- It exports `collectBatch(requests, context)`, which accepts every requested `{windowStart, windowEnd, observedAt}` object as one array and returns one collection result per window in the same order. A CAO adapter may additionally supply `repository` on each request and `database` in context.
- When `context.database` contains the authoritative facts, execute bounded canonical queries for all requested windows. Use persisted precomputations or an available immutable target-repository checkout for evidence absent from that database; otherwise return missing evidence.
- Collect repository state at the last immutable commit at or before `windowEnd`; `observedAt` records when matured evidence is available and is not the repository-state cutoff.
- Batch local evidence reads across all windows. Reuse each canonical projection, preserved snapshot, or checked-out immutable commit, then derive every observation locally.
- Each collection result contains `evidence`, non-empty immutable `provenance`, and an optional `commit`. Never make one remote request per selected window.
- `evidence` includes non-empty `key`, `repositories`, `opportunity`, `filters`, `collection`, `measurementKind` (`interval` or `snapshot`), and `window` fields. `window` defines positive `durationDays` and `cadenceDays` plus non-negative `maturationDays`. A snapshot measures state at `windowEnd`; its start identifies the preceding comparison cutoff, not a seven-day flow.
- Each metric records `id`, `name`, `role`, native `formula`, `direction`, `unit`, and presentation. Presentation uses `identity`; consumers format the native unit and interpret movement using `direction`. Exactly one metric is primary.
- A metric that supports a campaign rollup may additionally declare `rollup: { numeratorField, denominatorField }`. Both fields must identify non-negative additive evidence values and the denominator must be positive. Their quotient must have the metric's declared native unit; the numerator may exceed the denominator.
- It exports `scoreMetric(id, evidence)`, which returns a deterministic finite native-unit number, or `null` for missing or wholly malformed evidence. Round only at the output boundary; do not clamp to a normalized range.
- A metric may return a provisional number for valid interim evidence. The evidence must carry `maturityStatus: "interim"` and `dubious: true`; adapters and reports must preserve that status wherever the number is shown.
- Include `targetAttained`, `targetMissed`, `missing`, and `malformed` validation examples for every metric. `targetAttained` must score higher than `targetMissed`.
- The collector acceptance gate must validate evidence and provenance. Temporary evidence must be removed in `finally` cleanup.
- For accepted-event reporting, the module may additionally export
  `collectEvents({windowStart, windowEnd, observedAt})`. It returns the
  [event ledger](references/event-ledger.md), using the same acquired originals
  and frozen measurement rules without remote calls. Declare the selected
  metric's immutable `version` and `formula` in `definition.metrics` and its
  intended goal in `definition.intent.goal`. Contribution events must bind to
  a diagnostic, never replace the primary repository outcome. An accepted
  event is counted once at delivery after required maturation; pending and
  rejected outputs remain lifecycle evidence, not successful deliveries.

### CAO Adapter Interface

- The package-level adapter reads one `{schemaVersion: 1, timestamp, repositories, database}` request from stdin and discovers every slug-named `.mjs` module directly under its `operational-value/` directory.
- For each module, it derives one current observation window per supported repository from the frozen duration and maturation period. Routine CAO collection must never rebuild historical windows.
- It calls each module's `collectBatch` once for all supported repositories and calls `scoreMetric` for every metric using the returned evidence.
- It emits one JSONL record per metric assessment with required `timestamp`, `repository`, namespaced `valueId` (`WORKFLOW-SLUG.METRIC-ID`), native-unit numeric `value` or an explicitly unavailable null, and `metricUnit`, plus the metric and maturity metadata needed by consumers.
- New contracts also emit exact `windowStart`, `windowEnd`, `measurementKind`, `coverageStatus` (`complete`, `partial`, `missing`, or `unknown`), and an `evidenceDigest`. Complete current cutoffs are cadence-aligned after maturation; historical requests retain their explicit cutoffs. Carry collector-supplied `eligibleCount` and `populationDigest` without inventing them from a value. A successful query or non-null score alone is not proof that the source covered the entire period; preserve unknown coverage.
- A null score emits an explicit unavailable assessment with `value: null`, `maturityStatus: "unavailable"`, coverage and a non-empty `missingReason`; it is not zero and cannot satisfy routine successful-measurement reuse. Collector failures remain explicit errors, not fabricated missing-reason classifications.
- Before metric records, it emits an `operational_value_definition` JSONL record containing the workflow slug, adoption timestamp, evaluation mode, cadence, supported repositories, and namespaced metric IDs. CAO uses that authoritative definition query to retire cached IDs that no longer exist and, only when explicitly requested by the Activity compute path, query missing cadence observations within retained source evidence. It is metadata, not an observation.
- For a metric with a declared rollup, it also emits finite `rollupNumerator` and `rollupDenominator` values from the same accepted evidence. Consumers may derive a campaign value only as the sum of numerators divided by the sum of denominators.
- It may emit provisional pre-maturity values, but must include their interim maturity status and workflow adoption timestamp so consumers can mark them dubious and place adoption in temporal plots.
- It emits no record for unsupported repositories and fails closed on malformed requests, inaccessible evidence, or invalid collection results.
- Historical evaluation and CAO collection must import the same shared module so evidence and scoring cannot drift.
- A module may export `localCollection = true` only when `collectBatch` can use
  the supplied canonical database or local immutable evidence without remote
  calls or download fallbacks. Automatic local-only execution admits the reviewed
  standard adapter by source digest; keep its template and package copies equal
  and update that reviewed digest with adapter changes. Other adapters defer.
