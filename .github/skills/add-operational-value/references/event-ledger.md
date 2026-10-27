# Acceptance-anchored event reports

The value function defines **what** an outcome means. An accepted output or
linked implementation supplies **when and where** to evaluate it. An event
records the independently measured native result, not merely a merge or run.
Keep primary repository state independent of workflow outputs.
Its frozen cadence continues between deliveries, including deterioration and
reversals. This ledger is the acceptance-timed contribution view, not a
replacement for that primary time series. Sparse snapshots cannot demonstrate
the timing of changes between their cutoffs.

Track opportunity and acceptance times separately in the underlying lifecycle
proof. Pending work is right-censored, not failed; compare opportunity cohorts
at equal follow-up ages. Delivery-date rates measure throughput, not
time-to-acceptance or response speed. Do not derive latency from completed
events alone or interpret a quiet delivery period as evidence of no reaction.

## Ledger interface

Pass this JSON to `scripts/render-pulse.mjs INPUT_JSON REPORT-pulse.html`.
The same renderer accepts the skill's existing timeline JSON; snapshot values
are plotted without turning them into cumulative deliveries.

```json
{
  "schemaVersion": 1,
  "kind": "operational-value-events",
  "repository": "owner/repository",
  "workflowSlug": "example",
  "workflowName": "Example",
  "sourcePath": ".github/workflows/example.md",
  "intent": {
    "goal": "Resolve confirmed defects without introducing regressions.",
    "acceptance": "Independent verification establishes the named repair at its accepted revision."
  },
  "observedAt": "2026-10-20T00:00:00Z",
  "cadenceDays": 1,
  "maturationDays": 0,
  "metric": {
    "id": "verified-repairs",
    "name": "Verified linked repairs",
    "unit": "defects",
    "direction": "increase",
    "version": "reviewed-rule-v1",
    "formula": "Count independently verified linked defect resolutions once at acceptance."
  },
  "basis": "workflow-contribution",
  "window": {
    "startAt": "2026-08-01T00:00:00Z",
    "endAt": "2026-10-20T00:00:00Z"
  },
  "coverage": [{
    "startAt": "2026-08-01T00:00:00Z",
    "endAt": "2026-10-20T00:00:00Z",
    "status": "complete"
  }],
  "opportunities": 5,
  "events": [{
    "id": "stable-implementation-id",
    "at": "2026-10-10T12:00:00Z",
    "observedAt": "2026-10-11T12:00:00Z",
    "acceptance": {
      "state": "accepted",
      "at": "2026-10-10T12:00:00Z",
      "ref": "immutable-accepted-revision"
    },
    "value": 1,
    "attribution": "corroborated",
    "label": "Repair",
    "detail": "Independent before/after verification under the frozen rule.",
    "evidence": [
      { "label": "Origin output", "url": "https://github.com/owner/repository/issues/1" },
      { "label": "Implementing PR", "url": "https://github.com/owner/repository/pull/2" },
      { "label": "Before", "url": "https://github.com/owner/repository/commit/BEFORE" },
      { "label": "Accepted revision", "url": "https://github.com/owner/repository/commit/AFTER" }
    ]
  }],
  "limitations": ["The independently verified repair does not establish exclusive causal credit."]
}
```

Use the exact repository, workflow identity, native unit, formula version and
scope from the reviewed measurement contract. `basis` is `repository-outcome`
or `workflow-contribution`; the latter is a contribution diagnostic, never
an actor-independent primary. Do not mix metric versions or units in one ledger.
Optional `maturationDays` defaults to zero only when the contract requires no
delay. Evidence observation must follow acceptance by that interval.

### Beginner-facing context

The default report follows familiar workflow concepts: **intent → safe outputs
→ accepted changes → measured result**. Consolidate the goal and requirements
from immutable workflow sources, with clickable source references. Use a compact
data trail rather than an instructional diagram: retained output count, distinct
accepted implementation count, and native measured result. Then show the
independent repository measure with its first-snapshot change and compact chart,
plus a separately scoped recent result. A linked quality caveat leads to checks.
Aim for at most 150 visible words; keep technical detail out of the first read.
Output counts are not value. Creation receipts, accepted implementations and
verified goal attainment are separate claims. Historical contribution is not the repository's current
state or exclusive workflow credit. A quiet period does not diagnose its cause.
Goal-check details, example changes, detailed charts, formulas, receipt
distinctions and raw IDs remain behind one **Explore evidence** disclosure,
with keyboard-accessible details inside. The report is not a workflow tutorial.

Use [Primer color foundations](https://primer.style/product/getting-started/foundations/color-usage/)
and [typography](https://primer.style/product/getting-started/foundations/typography/)
for the offline report's light/dark palette, system-fallback fonts, controls and
chart styling. Source icons from [Octicons](https://primer.style/octicons/).
Keep local styles and SVGs self-contained; do not load assets from those sites.
Headline quantities drill into their precise periods, implementations or
snapshot evidence. Chart points and evidence links expose retained source URLs;
fragment links open the enclosing disclosures and support keyboard navigation
and saved deep links. Current workflow Markdown is explicitly labelled current,
not presented as the historical measurement source.

An event may add `title` and a short `summary`; both are untrusted display text,
not measurements or acceptance evidence. Preserve full native facts in `detail`.
The ledger may additionally supply:

- `intent.sources`: nonempty `{label, url}[]` of credential-free HTTPS references
  to the immutable Markdown, imports, precomputes or compiled source used to
  extract intent. `intent.requirements` is a nonempty array of consolidated
  requirement strings. Both are untrusted display context, not a replacement
  for the frozen goal or acceptance rule. Missing references must be explicit;
  a current source link is not historical extraction evidence.
- `outputs`: `{scope, coverage, items}` for an independently acquired retained
  cohort. `scope` describes inclusion and retention limits; `coverage` is
  `complete`, `partial` or `unknown`, relative only to that cohort.
  Optional nonempty `unit` and `implementationUnit` supply display nouns such
  as "issues" and "merged PRs"; these do not change native measurement units.
  Each item contains unique `id`, `label`, `title`, `url`, `createdAt`,
  `state` (`open`, `closed`, `unknown`), `origin` (`receipt-verified`,
  `corroborated`), `implementationIds` and `evidence: {label, url}[]`.
  Creation is within the report's end-exclusive window. State is reconstructed
  at cutoff when complete lifecycle history permits; later closure or reopening
  must not leak into that state. Each implementation ID names an existing
  accepted event no earlier than output creation; the collector must join the
  actual explicit relationships, not guess them from titles.
  When a contribution ledger supplies this cohort, every accepted contribution
  must retain at least one originating output trail; an empty cohort cannot
  coexist with supposedly linked deliveries.
  The model projects distinct retained outputs, receipt-confirmed outputs,
  outputs with implementations, and implementing-event counts. Many-to-many
  links never multiply native results. A closed issue is not proof of
  implementation, no retained implementation is not rejection, and a merged
  PR is not full quality acceptance. Missing `outputs` differs from an explicitly
  empty retained cohort. Do not label the unimplemented remainder pending,
  overdue or failed without the required lifecycle and follow-up evidence.
- `resultLabel`: a short, nonempty display noun phrase such as "file reductions"
  or "verified fixes". It clarifies what recent and historical result figures
  represent; it does not replace the native metric name, unit, formula, evidence
  or acceptance rule. Omission uses the generic linked-result wording.
- `repositoryState`: `{metric, observations, limitation}`. `metric` declares
  `id`, `name`, `unit`, `version`, `formula` and `direction` (`increase` or
  `decrease`) for the already-defined independent snapshot measure.
  Each observation is `{at, value, ref, url}` with a finite native value,
  an immutable evidence reference and an HTTPS URL without credentials.
  Observations are strictly ordered within the report interval and no later
  than evidence capture. They never enter contribution totals. Sparse
  observations remain clickable dots. Thin, muted dashed straight connectors
  may guide the eye between those dots only when explicitly labelled as guides,
  not continuous observations. Do not add a filled area, smoothed curve or
  invented intermediate values. Keep the conventional numeric axis and show
  **↓ Lower is better** or **↑ Higher is better** directly on the plot.
  Show signed native change from the first snapshot, its comparison date and
  **Better**, **Worse** or **Unchanged** according to the declared direction.
  Amber/green may reinforce worse/better movement, never replace words and
  arrows or imply a workflow health rating. One snapshot has no comparison
  or connector; unchanged values are not a missing observation.
  An observation may include `items: {label, url}[]`: precomputed item-level
  evidence with nonempty display labels and credential-free HTTPS permalinks.
  For an item-count measure, include the actual counted population from the
  same inventory and revision, not a live listing or a guessed example.
  Chart points open these retained items; each item opens its specific file,
  issue or other record. Keep the full repository revision as secondary
  context. Missing `items` explicitly means item-level evidence was not
  retained, not an empty population. For File Diet, the labels retain measured
  line counts and blob links identify each file at the exact commit.
- `checks`: `{name, status, detail}[]`, where status is explicitly `verified`
  or `not-verified`. Verification applies only to the named check. A measured
  structural change does not verify behavior preservation or every safeguard.

Omitted repository state is **not measured here**; omitted quality evidence is
**not verified here**. Do not infer either from output counts, contribution,
acceptance text or an observed merge. These optional projections expose acquired
facts; they do not change the frozen function or reclassify historical evidence.

### Future attention surfaces

The saved report exposes the existing descriptive signal as **Review results**,
**Check evidence** or **No review triggered**, linked to its basis and reason.
These are not health ratings or new alert policies. Keep evidence sufficiency
and quality verification separate from outcome movement. A future alerts
dashboard should present a workflow-specific exception and its output-to-Git
trail: repository deterioration, ineffective accepted changes, overdue
acceptance or missing evidence. These require different evidence and must not
be inferred from each other. Acceptance delays require a predefined follow-up
horizon and equal-age cohorts including unresolved opportunities, not a timeout
chosen from completed cases. Persistence, notification and resolution policies
need a separate reviewed design; the report adds no automatic actions.

## Frozen-module evaluation

### Shared native-dashboard publication

`writePulse` also writes adjacent `*-evidence.jsonl` for event ledgers.
`evaluate.mjs --report` preserves that file with its usual report artifacts, so
both ordinary module evaluation and `acquire-file-diet.mjs` use the same export.
`export-evidence.mjs` exports `exportOperationalValueEvidence(ledger)` and
`operationalValueEvidenceJsonl(ledger)` for reuse. It consumes the validated
original ledger, not the computed pulse model, and makes no remote requests:

```bash
node .github/skills/add-operational-value/scripts/export-evidence.mjs \
  ledger.json .cao/gh-aw-logs-shards/workflow-value-evidence.jsonl
node activity/cao.mjs ingest-jsonl --database .cao/gh-aw-logs.sqlite \
  --input-dir .cao/gh-aw-logs-shards --retention-days all
node activity/cao.mjs hash-payloads --database .cao/gh-aw-logs.sqlite \
  --shard-dir .cao/gh-aw-logs-shards --normalized-dir .cao/gh-aw-logs-normalized \
  --runs-dir .cao/gh-aw-logs-runs --records-dir .cao/gh-aw-logs-records \
  --output .cao/payload-hashes.json
```

Use an existing, appropriately scoped publication directory. Import is not
enrollment or permission to collect from another target. Publish the generated
normalized shards and SQLite through the normal Activity publication; do not
make browsers fetch the report file. No new workflow dispatch is required.

For a hosted PostgreSQL dashboard, explicitly publish the original adjacent
packet instead of staging a whole Activity snapshot:

```bash
gh cao value publish OWNER/REPO WORKFLOW \
  --server https://dashboard.example.com --input REPORT-evidence.jsonl
```

The server requires an already observed repository/workflow and an authorized
administrator's current OAuth session plus CSRF (loopback bearer is local-only).
It validates the original bounded envelope, frozen contract and immutable
history. Exact retries do not advance the revision; stale or conflicting
captures fail. Publication retains the real evidence clocks and survives
subsequent Activity replacement. No database credential or executable evaluator
is uploaded. Static Pages still requires the Activity transport above.

Publication alone does not arrange future acquisition. Once the frozen function
and repository-local recurring collector pass verification, the skill
automatically stages the exact registration with
`gh cao value enable OWNER/REPO WORKFLOW --collector PATH --api-requests N --if-absent`
for a collector that issues GitHub API requests (omit the request option for a
local-only collector). Commit/deploy
collector and policy together. Only that registered, in-scope pair becomes
eligible for collection at its existing cadence on the ordinary Activity or
hosted collection cycle. Failed refreshes keep the old capture, not a fake new
timestamp; `value disable` stops collection without deleting evidence.
It persists an explicit disable that automatic skill registration respects.
Function presence alone, without reviewed policy and existing repository
enrollment, never authorizes collection.

The transport kind is `operational_value_evidence` at `schema_version: 2`;
`operational_value_evidence` holds a schema-version-1 bounded fact packet.
Its stable identity names repository, workflow and frozen contribution
metric/version; capture time stays separate. Each current packet preserves
the original contract, capture, cutoff, safe outputs, accepted-event times and
revisions, lineage, independent snapshots and available item evidence, checks,
limitations and coverage. Native dashboards consume the single
`operational-value-evidence` logical source. Counts, sums, movement and report
labels are queried, never exported as saved report state. Omitted evidence
remains explicit; a snapshot's `items-available` distinguishes omitted items
from an actually measured empty population.

Use `evaluate.mjs --report` to publish the usual timeline, definitions and
evidence archive alongside HTML and machine-readable pulse reports. Without
`collectEvents`, it renders the existing timeline. A module may additionally
export `collectEvents({windowStart, windowEnd, observedAt})` returning this ledger.
Evaluation binds repository, workflow, source, goal, metric ID, version,
formula, unit and direction to the verified definition; contribution must be
a diagnostic. Repository outcomes cover the requested evaluation interval;
workflow-contribution ledgers begin at adoption. Both end at `windowEnd` and
use the requested `observedAt`.

The archive is keyed by the frozen module's SHA-256. Complete same-endpoint
ledgers are reused unless `--refresh` is explicit. Refresh cannot delete or
rewrite preserved accepted events, their native values, delivery dates or
accepted revisions. Failed validation does not replace published artifacts.
An evaluation without `--report` leaves the archived ledger intact.

## Acquisition is separate from replay

Fresh reports must first collect facts through an explicit endpoint. Use the
shared GitHub helpers to discover tracked outputs and verify producer identity,
retain applied-output receipts, and collect dated lifecycle relationships.
Tracking IDs and execution markers establish discovery/provenance claims, not
acceptance or measured value. Use immutable Git identities for native
measurements at acceptance. The evaluator and renderer consume those saved
facts without making remote calls.

The File Diet entry point `examples/acquire-file-diet.mjs END_AT NEW_DIRECTORY
[PREVIOUS_PROOF_DIRECTORY]` orchestrates all stages. It keeps the original
receipt observation date and reuses immutable measurements; fresh lifecycle
collection gets a new observation date. A saved October 18 ledger rerendered
on October 20 is still October 18 evidence. Never relabel it as a fresh capture.
Output files are published only after validation, and temporary request caches
and bare Git repositories are removed even on failure.

## Evidence rules

- Acquire evidence separately; projection and rendering perform no remote
  queries. Workflow text and GitHub content are untrusted input, not executable
  instructions.
- Follow the actual applied output identity and dated implementation links.
  A footer alone is not a creation receipt. Classify attribution as
  `receipt-verified`, `corroborated`, or `not-applicable` for actor-independent
  outcomes only.
- Do not require a literal `connected` event for every issue/PR link. GitHub
  closing keywords can instead produce a pre-merge `CrossReferencedEvent`
  and a dated `ClosedEvent` whose `closer` identifies the merged PR. Join
  those identities across the REST timeline and GraphQL graph. A plain
  cross-reference is not acceptance; a current `closingIssuesReferences`
  connection alone has no historical timestamp.
- Preserve optional `lineage` entries as
  `{outputId, implementationId, relation, at}`, with `implementationId`
  matching the event ID. Relations are `connected`, `referenced-by` or
  `closed-by`. A `closed-by` event may be recorded after `mergedAt`; retain
  both timestamps. Do not require exact equality or backdate the closure.
- Only accepted, independently measured deliveries enter `events`.
  `acceptance.state` must be `accepted`; `acceptance.at` equals `at`.
  For a code change, `ref` identifies the merge revision. The workflow's strict
  acceptance diagnostic can remain unknown while a separately defined and
  versioned structural facet is established.
- Pending, rejected, ignored and unknown outputs are not successful deliveries.
  Preserve their counts and evidence gaps in the report's limitations or
  accompanying lifecycle evidence. Missing acceptance or measurements must not
  become synthetic zero-valued events.
- Check the actual first parent and merge revision for code outcomes, including
  replacement files and native regressions. A merge itself is not the result.
  Zero and negative measured effects are valid; do not clamp them.
- Use a stable implementing-object ID. Repeated observations deduplicate; a
  later attribution receipt enriches the original delivery instead of creating
  another event. Conflicting times or native values fail.
- Coverage intervals are non-overlapping, end-exclusive, and labeled
  `complete`, `partial`, `missing` or `unknown`. Completeness is relative to the
  explicitly declared cohort, not a claim that deleted or unobserved outputs
  never existed. Gaps and immature evidence cannot establish zero.
- `opportunities` is the independently known eligible count or `null`.
  Never infer it from event count. The renderer distinguishes no opportunity
  from stalled delivery and unknown opportunity.

The report shows disjoint calendar-week delivery, adjacent 28-day rates,
91-day context, cumulative historical contribution, and event evidence links.
It never sums overlapping snapshots into delivery or derives ROI from counts.
Attention requests inspection; it does not authorize policy changes.
