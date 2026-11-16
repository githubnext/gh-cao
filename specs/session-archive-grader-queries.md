# Session-archive grader query feasibility

## Status and scope

This is a living progress tracker, not an implementation contract. It records
whether each upstream `github/gh-aw` grader can be recomputed as a reviewed
declarative `session-query` over the private session archive, what blocks the
graders that cannot, and which language or projection extensions would unlock
them. Normative archive, evidence, and query behavior remains in
[`session-archive.md`](session-archive.md); Dashboard Language vocabulary remains
in [`docs/dashboard-language-specification.md`](../docs/dashboard-language-specification.md).
Nothing here approves new evidence, broadens the query profile, or changes a
grader's semantics.

| Item | Value |
| --- | --- |
| Upstream pin | `github/gh-aw` `5ac122295e5e839aa15a727ecf4762f3e680cc24` ([`grader-source-pins.json`](../server/internal/sessionarchive/testdata/grader-source-pins.json)) |
| Last upstream review | `main` `a25e38ee6b9740b10008559bd2c9290ef1f925aa` (2026-11-16); grader code identical, `graders-specification.md` changed one link only |
| Upstream sources | `actions/setup/js/builtin_graders.json`, `grader_session.cjs`, `trace_graders.cjs`, `trajectory_graders.cjs`, `working_set_metrics.cjs`, `operational_value_grader.cjs`, `custom_graders.cjs` |
| Query profile | `session-query`, `language-version: "0.2.0"`, source `archive-events` (`AnalyticalCapabilities` in `server/internal/sessionarchive/analytical.go`); local DuckDB also advertises the Phase 1 item sources (`LocalAnalyticalCapabilities`) |
| Graders in scope | 35 built-ins; operational-value and custom graders are listed as out of scope |

Re-review when the pin moves: compare the pin with upstream `main` through
`gh api repos/github/gh-aw/compare/<pin>...<main>`. Reclassify any grader whose
source or `builtin_graders.json` entry changed, and update both this tracker and
[`grader-query-attempts.json`](../server/internal/sessionarchive/testdata/grader-query-attempts.json).

## 1. Current profile baseline

`session-query` 0.2.0 is a strict subset of Dashboard Language. It provides:

- Typed scalar `archive-events` columns. `PayloadJSON` and structured families
  are not analytical inputs.
- Conjunctive `equals`, `in`, `gte` and `lt` predicates.
- The `state` predicate, which applies to promoted scalars only. Its values are
  present, absent, null, invalid, excluded, redacted and valid.
- One `aggregate` stage with reducers `count`, `distinct-count`, `sum`, `max`,
  and ordered `first`. Filtered measures are supported.
- `post-aggregate` with the functions `if`, `equals`, `gt`, `lte`, `and`,
  `sum`, `difference`, `product`, `quotient`, `coalesce` and `is-null`.
- On local DuckDB only, the item sources `archive-objectives`,
  `archive-actions` and `archive-states` (Phase 1).
- Ordering and limits.

It has no joins, query chaining, windows, pre-aggregate computation, regex,
logarithms, or hosted structured-element rows.

The full Dashboard Language already standardizes several of the missing
capabilities:

- `difference` and `product` (§5.5.1), now ported as L1.
- Row-level `compute`.
- Equality joins, including `inner-many`, with at most four per query
  (DLS-QUERY-005..008).
- Queries that read earlier queries (DLS-QUERY-017).
- `previous` and `row-number` observation windows (§5.5.4).

Porting those constructs is preferred over inventing archive-specific
vocabulary. No Dashboard Language construct provides regex, logarithm or
exponential functions, recursion, iteration, or caller-supplied reference
data.

## 2. Status legend

| Status | Meaning |
| --- | --- |
| `implemented` | A reviewed definition under `server/internal/sessionarchive/testdata/graders/` matches the pinned upstream values and unavailable states in real DuckDB (`TestGraderReplayEvidence`) and, when configured, Kusto (`TestKustoUsageGraderReplay`). |
| `extension-ready` | Required evidence is already in the approved archive contract. Only the listed `L*` and `S*` extensions are needed. Feasibility is a reviewed hypothesis until a definition replays. |
| `evidence-gated` | Requires IR collections outside the approved families (`objectives`, `actions`, `states`, `events`, `attemptResults`, `startupAttemptResults`). A separate privacy review is needed before any query work. |
| `vocabulary-gated` | Requires a capability that Dashboard Language does not define or that this profile rejects (`G*`). |
| `out-of-scope` | Excluded by privacy policy, or executes arbitrary code. |

## 3. Extension catalog

Extensions are proposals. Each one requires its own reviewed change to
`session-archive.md`, `AnalyticalCapabilities`, the DuckDB and KQL lowerers,
and shared conformance tests. A backend that cannot implement an extension
MUST reject the query rather than approximate it.

| ID | Extension | Origin | Notes |
| --- | --- | --- | --- |
| L1 | `difference` and `product` post-aggregate and compute functions | DL §5.5.1 | Same null and finite-number rules as `quotient`. |
| L2 | Row-level `compute` before `aggregate`, with `if`, `equals-any`, `greater-than` and `and` | DL `compute` | Classifies rows before counting, for example a disjunctive tool failure. Avoids adding OR predicates. |
| L3 | Query chaining: a definition reads one earlier definition in the same request | DLS-QUERY-017 | Supports two-stage aggregation, such as per call key then per trace, or per state id then per trace. Every stage keeps exact trace and projection identity. |
| L4 | Equality joins (`inner`, `left`, `inner-many`), at most four per query | DLS-QUERY-005..008 | Applies only within one trace and projection. Expansion fails closed at the row budget. No cross joins. |
| L5 | `previous` and `row-number` windows with `groupby`, usable before aggregation through L3 | DL §5.5.4 | Ordering must be on `Sequence` or a preserved element ordinal, never on ingestion order. |
| L6 | `state` predicate over approved structured families, for example `Error` non-null or `RetryHint` present | Archive-specific | Reads the existing presence metadata only. Never exposes or stringifies values. |
| S1 | Child-row sources that flatten approved arrays: `archive-objectives`, `archive-actions`, `archive-states`, `archive-ir-events`, `archive-attempt-results`, and nested `archive-objective-dependencies` | Archive-specific | One row per original element, with parent trace, projection, `Sequence` and element ordinal. Element scalars come from generated `@evidence` paths with the same state metadata. Preserves values without deriving state identity or scores. Changes the generated archive schema and contract version. |
| S2 | `trajectory-ir` binding of S1 sources to upstream's `trajectoryIR` (the last agent-phase `trajectory.ir` per trace) | Archive-specific | The trusted lowerer selects the binding at query time, not at capture. Traces whose upstream value would come from a synthesized fallback collection report `unavailable`, which is a declared and tested divergence. |
| G1 | Regex or keyword matching over text | — | Rejected. Blocks message-based retries, guard-objective detection and skill constraints. |
| G2 | `log` and `exp` functions | — | Not in Dashboard Language. Adding them is a language change, not a profile port. |
| G3 | Recursion, graph reachability, or iterative or dynamic-programming algorithms | — | Rejected. Blocks provenance lineage, LZ76 and DTW. |
| G4 | Caller-supplied grader configuration or reference data (constraints, reference trajectories or patches, process models) | — | Rejected for the authenticated archive profile. Would need separately reviewed reference evidence. |
| E1 | Approval of IR `observations`, `toolCalls`, `provenanceEdges`, `resources` or `reference` | — | Privacy review. Observations can carry tool output, which stays excluded. |

## 4. Per-grader feasibility

Effort is S, M or L relative to the existing four definitions. The CAO column
marks the 15 candidates in the
[fresh evidence contract](session-archive.md#21-fresh-original-evidence-contract).

### 4.1 Default graders

| Grader | Upstream formula | CAO | Status | Needs | Effort |
| --- | --- | --- | --- | --- | --- |
| `execution-step-count` | Proxy usage count, or aggregate usage count | yes | `implemented` | — | — |
| `execution-duration` | Sum of complete selected durations, with execution-result fallback | yes | `implemented` | — | — |
| `working-set-rebuild-factor` | Total input ÷ peak input | yes | `implemented` | — | — |
| `context-growth` | Total tokens ÷ first-record tokens | yes | `implemented` | — | — |
| `tool-failure-count` | Reconciled calls whose completion is false, success is false, `isError` is set, status is error or failure, or error is non-null | yes | `extension-ready` | L2, L3, L5, L6 | L |
| `tool-success-rate` | (calls − failed) ÷ calls, or 1 when there are no calls | yes | `extension-ready` | L1, L2, L3, L5, L6 | L |
| `trajectory-efficiency` | Distinct tool names ÷ reconciled calls, or 1 | yes | `extension-ready` | L2, L3, L5, L6 | L |
| `artifact-production` | Applied `safe_output.result` with success or absent status, minus absent-status types with a safe-outputs-phase outcome | yes | `extension-ready` | L3 | M |
| `retries` | MCP retry events (event, `retryHint` or a message regex) plus deduplicated harness attempts | yes | `vocabulary-gated` | G1. The non-regex part needs L3, L6 and S1 (`attemptResults`) | — |
| `loops` | Consecutive identical session, tool name and arguments | no | `out-of-scope` | Tool arguments are not approved | — |

Tool-call reconciliation is the shared prerequisite for the first three
extension-ready rows. Upstream `createSessionLookups` reconciles calls in five
steps:

1. Pair native starts and completions by `ToolCallId`.
2. Pair calls without an ID FIFO by tool name, within session, agent and harness
   attempt scopes.
3. Collapse resume-strategy attempts to their root scope.
4. Add gateway `mcp.tool_call` and `tools/call` RPCs.
5. Drop gateway calls whose IDs match a native call.

The declarative plan follows the same steps:

1. An L5 `row-number` over starts and completions per scope and name yields
   FIFO keys.
2. An L3 stage groups by scope and call key.
3. L2 and L6 classify each completion as a failure.
4. The final stage counts per trace.

No reconciliation result is persisted at capture.

### 4.2 Trajectory graders

All rows below read the S2-selected `trajectory.ir` collections. Upstream
reports unavailable above 5,000 items, and S1 sources must preserve that
guard. S1 (`objectives`, `actions`, `states`), S2 and L1 shipped in Phase 1
and are no longer listed as needs.

| Grader | Upstream formula | CAO | Status | Needs | Effort |
| --- | --- | --- | --- | --- | --- |
| `objective-coverage` | Satisfied objectives ÷ objectives; unavailable on an invalid index | yes | `implemented` (local DuckDB) | — | — |
| `premature-termination-gap` | Count of objectives with a null or absent `satisfiedAtEventIndex`; unavailable on an invalid index | yes | `implemented` (local DuckDB) | — | — |
| `grounding-accuracy` | Actions with `validAtIssueTime` true ÷ actions; unavailable unless every flag is boolean | yes | `implemented` (local DuckDB) | — | — |
| `state-revisit-probability-rep` | (visits − distinct states) ÷ visits, for at least 2 visits. Order only affects diagnostics | yes | `implemented` (local DuckDB) | — | — |
| `recurrence-rate` | Σ c(c−1) over per-state counts ÷ n(n−1), for at least 2 visits | yes | `extension-ready` | L3 | M |
| `recurrence-laminarity` | Vertical-line recurrent points ÷ recurrent points (vmin 2, n ≥ 4) | no | `extension-ready` | L2, L3, L4, L5 | M |
| `recurrence-determinism` | Diagonal-line recurrent points ÷ recurrent points (lmin 2, n ≥ 4) | no | `extension-ready` | L2, L3, L4, L5 | L, quadratic |
| `recurrence-trapping-time` | Mean vertical line length for lines of length ≥ 2 | no | `extension-ready` | L3, L4, L5 | L, quadratic |
| `dependency-order-violation-rate` | Completed dependent objectives whose prerequisite is unsatisfied or later ÷ completed dependent objectives | no | `extension-ready` | S1 (nested `dependsOn`), L2, L3, L4 | M |
| `evidence-saturation-stopping-lag` | IR events after the maximum satisfaction index; every index must exist in the events | no | `extension-ready` | S1 (IR `events`), L2, L3, L4 | M |
| `exploration-error` | clamp(1 − observations ÷ distinct visited states) when an objective is unmet | no | `evidence-gated` | E1 (`observations`) | — |
| `exploitation-error` | Unused observations ÷ observations when an objective is unmet | no | `evidence-gated` | E1 (`observations`) | — |
| `tool-output-consumption-rate` | Tool-originated observations that were consumed ÷ tool-originated observations | no | `evidence-gated` | E1 (`observations`, `toolCalls`), L4 | — |
| `policy-near-miss` | Unmet guard objectives ÷ guard objectives, where guard objectives are matched by a keyword regex on the description | yes | `vocabulary-gated` | G1 | — |
| `skill-constraint-coverage` | Configured regex constraints that were exercised and passed | no | `vocabulary-gated` | G1, G4 | — |
| `event-entropy-rate` | Bigram conditional entropy ÷ log2 of the alphabet size | no | `vocabulary-gated` | G2. Otherwise needs L3, L5 and `concat` | — |
| `lempel-ziv-trajectory-complexity` | Normalized LZ76 complexity | no | `vocabulary-gated` | G2, G3 | — |
| `end-to-end-lineage-completeness` | Safe outputs with a provenance path to tool or observation roots | no | `vocabulary-gated` | G3, E1 | — |
| `action-provenance-coverage` | Consequential actions with a provenance path to evidence roots | no | `vocabulary-gated` | G3, E1 | — |
| `tool-wise-score` | Longest correct prefix against a reference, with argument credit | no | `vocabulary-gated` | G4, E1, arguments | — |
| `trajectory-ndtw` | exp(−DTW ÷ (reference length × threshold)) | no | `vocabulary-gated` | G2, G3, G4 | — |
| `code-search-recall` | Reference patch files that were located ÷ reference patch files | no | `vocabulary-gated` | G4, E1 (`resources`) | — |
| `code-read-precision` | Read files in the reference patch ÷ distinct read files | no | `vocabulary-gated` | G4, E1 | — |
| `code-edit-precision` | Edited files in the reference patch ÷ distinct edited files | no | `vocabulary-gated` | G4, E1 | — |
| `process-alignment-fitness` | Satisfied checks against a configured process model | no | `vocabulary-gated` | G4 | — |

The RQA rows decompose as follows over ordered visits:

- **LAM** is linear. A visit `i` of a state with `c` visits contributes:
  - `c − 1` points when its previous and next visits share its state.
  - `c − 2` points when only one neighbor does.
  - Zero points otherwise.

  LAM is the sum of those contributions ÷ Σ c(c−1). Previous and next state
  come from L5 windows in both orders.
- **DET and TT** need recurrent-pair or block expansion. DET uses an
  `inner-many` self-join plus diagonal-neighbor joins. TT uses gaps-and-islands
  blocks from `row-number` differences, joined per column.

At the upstream 5,000-visit cap, DET and TT can exceed row budgets. Those
queries MUST fail closed rather than return partial lines.

### 4.3 Other graders

| Grader | Status | Reason |
| --- | --- | --- |
| Operational-value evaluator (`operational_value_grader.cjs`) | `out-of-scope` | Runs a repository-owned shell script over `aw_session.jsonl` |
| Custom graders (`custom_graders.cjs`) | `out-of-scope` | Run arbitrary script code; retired in CAO |

### 4.4 Summary

| Status | Count | Graders |
| --- | ---: | --- |
| `implemented` | 8 | step count, duration, rebuild factor, context growth; objective coverage, premature termination, grounding accuracy, state revisit (local DuckDB) |
| `extension-ready` | 10 | Tool reconciliation (3), artifact production, state recurrence (4), objective ordering (2) |
| `evidence-gated` | 3 | exploration error, exploitation error, tool-output consumption |
| `vocabulary-gated` | 13 | retries, policy near-miss, skill constraints, entropy, LZ, lineage (2), reference and config graders (6) |
| `out-of-scope` | 1 | loops |

## 5. Invariants for every new definition

- No new arguments, tool output bodies, models, cache counters, or safe-output
  titles or URLs are approved to support a grader. Excluded or redacted
  evidence keeps the result unavailable and is never normalized to make replay
  pass.
- Computation happens at query time. No reconciliation, state identity, guard
  classification or score is persisted at capture.
- Results are recomputed scalar observations with an explicit availability and
  reason, distinct from recorded `grader.result`. A backend failure is an
  error, not a null grade.
- Each definition is one reviewed file under
  `server/internal/sessionarchive/testdata/graders/`. It is added to the
  `supported` set in `scripts/replay-session-archive-graders.mjs` and replayed
  against the pinned upstream code on DuckDB and, when configured, Kusto.
  [`grader-query-attempts.json`](../server/internal/sessionarchive/testdata/grader-query-attempts.json)
  is updated to remove a resolved blocker or record a new one.

## 6. Progress

Phases are ordered by how many graders each unlocks. Mark an item done only
when its definitions replay with parity.

- [x] Inventory the 35 upstream built-ins and classify feasibility at the pin and at `main` `a25e38ee`.
- [x] Implement the usage family: `execution-step-count`, `execution-duration`, `working-set-rebuild-factor`, `context-growth` (#21020).
- [x] **Phase 1:** S1 (`objectives`, `actions`, `states`), S2 and L1. Unlocks `objective-coverage`, `premature-termination-gap`, `grounding-accuracy` and `state-revisit-probability-rep` on local DuckDB (§7).
- [ ] **Phase 1b:** KQL lowering of the Phase 1 item sources, accepted against the Kusto emulator or a real ADX cluster. Until then hosted backends reject them.
- [ ] **Phase 2:** L3. Unlocks `recurrence-rate` and `artifact-production` (§8).
- [ ] **Phase 4:** L5 (with gaps-and-islands over `row-number`) and L6. Unlocks `tool-failure-count`, `tool-success-rate`, `trajectory-efficiency` and `recurrence-laminarity`.
- [ ] **Phase 5:** L4 and nested S1 (`dependsOn`, IR events). Unlocks `dependency-order-violation-rate` and `evidence-saturation-stopping-lag`.
- [ ] **Phase 6:** Prototype `recurrence-determinism` and `recurrence-trapping-time` under row budgets, or reclassify them as `vocabulary-gated`.
- [ ] Decide on E1 through privacy review; until then the `evidence-gated` graders stay unsupported.
- [ ] Revisit `retries` and `policy-near-miss` if upstream emits typed retry or guard flags that replace regex matching.

## 7. Phase 1 implementation notes

S1 is query-time flattening, not a persisted schema change. The trusted DuckDB
lowerer groups each complete active trace, binds the `PayloadJSON` of its
highest-`Sequence` `trajectory.ir` event with `Phase` `agent` (S2), and expands
the `data.objectives`, `data.actions` or `data.states` array into one row per
element plus one anchor row per trace. `PayloadJSON` is never exposed. Item
fields are typed without coercion: `SatisfiedAtEventIndex` must be a safe
non-negative JSON integer, `ValidAtIssueTime` a JSON boolean, and `Id` a string
element or an object's string `id`. State predicates on `Items` and element
fields are qualified by the collection path, so omissions surface as `excluded`
or `redacted`.

Declared divergences, all failing towards `unavailable`:

- An excluded collection, or a redacted `states` collection, reports
  `unavailable` with reason `excluded` or `redacted`. Live upstream sees the
  original data.
- The 5,000-item cap is checked only on the queried collection, not on
  upstream's synthesized event list.
- ADX and native backends do not advertise item sources and reject such plans.
  The KQL lowering is Phase 1b because the Kusto emulator does not run on the
  ARM development host used for Phase 1.

## 8. Smallest next delta

L3 (query chaining) is the smallest single extension that unlocks more
graders, without L2:

- `artifact-production`: a first stage aggregates `safe_output.result` rows
  per trace and `DataType` with filtered measures (applied with success or
  absent status, and absent-status types with a `safe_outputs`-phase outcome
  whose status is defined). A second stage sums per trace the counts of types
  not excluded. Filtered measures and `state` predicates already provide the
  classification, so L2 is unnecessary.
- `recurrence-rate`: a first stage counts visits per trace and state `Id` over
  `archive-states`. A second stage sums c(c−1) with L1 `product` and divides by
  n(n−1).

Tool reconciliation (`tool-failure-count`, `tool-success-rate`,
`trajectory-efficiency`) needs more than L5 `previous`: upstream collapses
resume-strategy attempts to the last non-resume attempt in each invocation,
which is a gaps-and-islands construction (`row-number` differences plus L3).
Those three graders stay in Phase 4.
