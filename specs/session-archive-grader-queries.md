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
| L4 | Equality joins (`inner`, `left`, `inner-many`), at most four per query | DLS-QUERY-005..008 | Applies only within one trace and projection. Expansion fails closed at the row budget. No cross joins. Lowering constraints are in §3.1. |
| L5 | `previous` and `row-number` windows with `groupby`, usable before aggregation through L3 | DL §5.5.4 | Ordering must be on `Sequence` or a preserved element ordinal, never on ingestion order. Lowering constraints are in §3.1. |
| L6 | `state` predicate over approved structured families, for example `Error` non-null or `RetryHint` present | Archive-specific | Reads the existing presence metadata only. Never exposes or stringifies values. |
| S1 | Child-row sources that flatten approved arrays: `archive-objectives`, `archive-actions`, `archive-states`, `archive-ir-events`, `archive-attempt-results`, and nested `archive-objective-dependencies` | Archive-specific | One row per original element, with parent trace, projection, `Sequence` and element ordinal. Element scalars come from generated `@evidence` paths with the same state metadata. Preserves values without deriving state identity or scores. Changes the generated archive schema and contract version. |
| S2 | `trajectory-ir` binding of S1 sources to upstream's `trajectoryIR` (the last agent-phase `trajectory.ir` per trace) | Archive-specific | The trusted lowerer selects the binding at query time, not at capture. Traces whose upstream value would come from a synthesized fallback collection report `unavailable`, which is a declared and tested divergence. |
| G1 | Regex or keyword matching over text | — | General and caller-supplied regex stays rejected. A closed, reviewed ASCII keyword-word-match predicate is feasible on both backends (§3.1), but it is a language change and also needs approval of each text field it reads. Blocks message-based retries, guard-objective detection and skill constraints. |
| G2 | `log` and `exp` functions | — | Not in Dashboard Language. Adding them is a language change, not a profile port. Rejected while replay requires exact equality, because DuckDB and V8 results differ in the last bit (§3.1). |
| G3 | Recursion, graph reachability, or iterative or dynamic-programming algorithms | — | Rejected. Blocks provenance lineage, LZ76 and DTW. The RQA graders do not need it (§4.2). |
| G4 | Caller-supplied grader configuration or reference data (constraints, reference trajectories or patches, process models) | — | Rejected for the authenticated archive profile. Would need separately reviewed reference evidence. |
| E1 | Approval of IR `observations`, `toolCalls`, `provenanceEdges`, `resources` or `reference` | — | Privacy review. Observations can carry tool output, which stays excluded. |

### 3.1 Backend lowering constraints

Every extension must run efficiently and give identical results on local
DuckDB and hosted Kusto (ADX). Replay compares values with exact equality, so
"close" floating-point results are failures. The DuckDB findings below were
measured with the CLI at one thread and 256 MiB. The Kusto findings rely on
the Microsoft Learn KQL reference only, because the Kusto emulator does not
run on the ARM development host. They must be confirmed against the emulator
or a real cluster before hosted support is advertised.

- **L5 windows.**
  - **DuckDB lowering:** `row_number()` and `lag()`
    `OVER (PARTITION BY <trace key>, <groupby> ORDER BY …)`. Both windows over
    1,000,000 rows (200 traces × 5,000 items) took 56 ms.
  - **KQL lowering:** `partition` by the trace key, with `hint.strategy=native`
    or `shuffle` by key cardinality, containing `sort`, `row_number()` and
    `prev()`. A global `serialize` funnels every row to one node and MUST NOT
    be used.
  - **Unique order key:** Kusto `sort` is not stable. The order key must end
    with a unique tiebreak (`Sequence`, then the element ordinal), and hosted
    plans whose order key is not unique are rejected rather than relying on
    "ties keep input order".
  - **Explicit null placement:** the lowerers emit null placement explicitly
    because the engines' defaults differ.
  - **First-row nulls:** Kusto strings cannot be null, so the KQL lowerer
    derives the null first-row `previous` from a `row_number() == 1` flag, not
    from `prev()`'s default value.
- **L4 joins.**
  - **DuckDB lowering:** hash joins.
  - **KQL lowering:** an explicit `kind` (`inner` or `leftouter`) with
    `hint.strategy=shuffle` on the trace key. KQL's default `innerunique`
    silently deduplicates the left side and MUST NOT be emitted.
  - **Trace and projection key:** every join key includes the trace and
    projection key.
  - **Missing keys:** missing keys are excluded explicitly, because DuckDB
    NULL never matches but a KQL `""` key matches `""`.
- **L2 and L6.** Lower to `CASE` and `extend` expressions and reuse the
  existing presence metadata. The cost is negligible. Integer arithmetic stays
  in `BIGINT` and `long`. An integer-over-integer quotient is one correctly
  rounded IEEE division, so it is identical across engines and V8.
- **G1 keyword matching.**
  - **Regex dialects:** DuckDB `regexp_matches` is RE2. KQL `matches regex`
    uses Rust `regex` semantics, where `\b` and `\w` are Unicode-aware, while
    RE2 and JavaScript non-`u` `\b` are ASCII. RE2 `(?i)` also applies Unicode
    simple case folding (Kelvin sign `K` to `k`, `ſ` to `s`), which JavaScript
    non-`u` `/i` does not. On DuckDB, `(?i)\bcheck\b` matches `checKx` where
    upstream does not. `(?i)\bretry(ing)?\b` is exact on DuckDB only because
    no letter of `retry(ing)` has a non-ASCII fold, and it still differs on
    KQL through Unicode `\b`.
  - **Lowering:** a closed keyword list must therefore never be lowered with
    `\b`, `\w` or `(?i)`. It is lowered to explicit ASCII classes, for example
    `(^|[^A-Za-z0-9_])[rR][eE][tT][rR][yY]([iI][nN][gG])?([^A-Za-z0-9_]|$)`.
  - **Measured parity:** on 20,000 fuzzed strings with non-ASCII letters,
    Kelvin sign, long s and dotted or dotless `i`, that DuckDB pattern matched
    upstream `/\bretry(?:ing)?\b/i` exactly. The executable evidence in §4.2
    checks both upstream patterns on DuckDB.
  - **Cost and exclusions:** both engines match in linear time. KQL `has` uses
    different term tokenization and is not an equivalent. Caller-supplied
    JavaScript regex cannot be translated faithfully to either dialect and
    stays rejected with G4.
- **G2 logarithms.** Over 80,199 rationals `t ÷ c` with `t ≤ 400`, DuckDB
  differed from V8 in the last bit for 1,110 `log2` results (1.4%), 5,790 `ln`
  results (7.2%) and 7,007 `exp` results (8.7%). ADX uses a third math library
  (untested). Upstream entropy also sums terms in `Map` first-occurrence
  order. G2 can be revisited only if replay adopts an explicit tolerance
  contract.
- **G3 algorithms.**
  - **LZ76:** Kaspar–Schuster parsing is sequential, with a prefix substring
    search. A DuckDB recursive CTE would take about n iterations of O(n) work,
    and KQL `scan` has no prefix lookback.
  - **Provenance lineage:** needs unbounded reachability, which a DuckDB
    recursive CTE and bounded-hop KQL `graph-match` do not express
    identically.
  - **DTW:** an O(n·m) dynamic program.
  - **Conclusion:** none has an efficient, identical lowering on both engines.

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
| `retries` | MCP retry events (event, `retryHint` or a message regex) plus deduplicated harness attempts | yes | `vocabulary-gated` | G1 (closed keywords) with approval of the message field. The non-regex part needs L3, L6 and S1 (`attemptResults`) | — |
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

Upstream reconciliation is stateful: completions that are already paired or
have a mismatched ID are skipped, gateway calls are deduplicated against
native IDs, and resumed attempts map through `previousScopes`. Before relying
on L5 FIFO keys alone, a parity fixture MUST cover out-of-order completions,
duplicate and mismatched IDs, gateway duplicates and resume chains on both
backends.

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
| `recurrence-laminarity` | Vertical-line recurrent points ÷ recurrent points (vmin 2, n ≥ 4) | no | `extension-ready` | L2, L3, L5; visit order needs S1 (`firstEventIndex`, IR `events`) and L4 | M, linear |
| `recurrence-determinism` | Diagonal-line recurrent points ÷ recurrent points (lmin 2, n ≥ 4) | no | `extension-ready` | L2, L3, L5; visit order needs S1 (`firstEventIndex`, IR `events`) and L4 | M, linear |
| `recurrence-trapping-time` | Mean vertical line length for lines of length ≥ 2 | no | `extension-ready` | L2, L3, L5; visit order needs S1 (`firstEventIndex`, IR `events`) and L4 | M, linear |
| `dependency-order-violation-rate` | Completed dependent objectives whose prerequisite is unsatisfied or later ÷ completed dependent objectives | no | `extension-ready` | S1 (nested `dependsOn`), L2, L3, L4 | M |
| `evidence-saturation-stopping-lag` | IR events after the maximum satisfaction index; every index must exist in the events | no | `extension-ready` | S1 (IR `events`), L2, L3, L4 | M |
| `exploration-error` | clamp(1 − observations ÷ distinct visited states) when an objective is unmet | no | `evidence-gated` | E1 (`observations`) | — |
| `exploitation-error` | Unused observations ÷ observations when an objective is unmet | no | `evidence-gated` | E1 (`observations`) | — |
| `tool-output-consumption-rate` | Tool-originated observations that were consumed ÷ tool-originated observations | no | `evidence-gated` | E1 (`observations`, `toolCalls`), L4 | — |
| `policy-near-miss` | Unmet guard objectives ÷ guard objectives, where guard objectives are matched by a keyword regex on the description | yes | `vocabulary-gated` | G1 (closed keywords) with approval of the objective `description` field. Also needs S1 (IR `events`) for the safe-output check, L2 and L3 | — |
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

Upstream orders visits by the position of `firstEventIndex` in the IR
`events`, falling back to `firstEventIndex` and then to the array position,
with a stable sort. The declarative order key is therefore that derived order
followed by the element ordinal, which also satisfies the unique-order rule in
§3.1. Write `C2(x) = x(x − 1) ÷ 2`. Every RQA row is a sum of per-visit or
per-group integer terms followed by one final division, so no recurrence
matrix, self-join or quadratic expansion is needed:

- **LAM.** A visit of a state with `c` visits contributes:
  - `c − 1` points when its previous and next visits share its state.
  - `c − 2` points when only one neighbor does.
  - Zero points otherwise.

  LAM is the sum of those contributions ÷ Σ c(c−1). Previous and next state
  come from L5 windows in both orders.
- **DET.** The recurrent points on diagonal lines of length ≥ 2 equal
  `2 · Σ C2(bigram count) − Σ C2(trigram count)`. Bigrams and trigrams come
  from `previous` at lags 1 and 2, so DET is that value ÷ Σ C2(state count).
- **TT.** Gaps-and-islands (`row-number` minus `row-number` per state) yields
  maximal runs. A run of length `L` of a state with `c` visits contributes:
  - **Lines:** `(c − L) · [L ≥ 2] + 2 · max(0, L − 2)`.
  - **Points:** `(c − L) · L · [L ≥ 2] + 2 · (C2(L) − 1) · [L ≥ 3]`.

  TT is total points ÷ total lines, or 0 when there are no lines.

Upstream's `ratio` helper returns 0 on a zero denominator, and fewer than four
visits (two for RR) is not applicable. These closed forms matched the upstream
loops on 3,000 random sequences with no mismatch. On DuckDB, DET over 1,000,000
visits took 113 ms. Counts stay integral until the last division, so results
are identical across engines (§3.1).

**Executable evidence.** `TestDuckDBLoweringEvidenceIntegration` runs
reference DuckDB lowerings from
[`testdata/lowering/`](../server/internal/sessionarchive/testdata/lowering/)
on every required DuckDB CI run:
- **Recurrence:** `recurrence.sql` computes RR, DET, LAM and TT with only L2
  expressions, L3 stages, L4 joins on the trace key and L5
  `previous`/`row-number` windows.
- **Keywords:** `keywords.sql` holds the ASCII G1 lowering of the `retries`
  and `policy-near-miss` keyword patterns.

[`scripts/generate-session-query-lowering-cases.mjs`](../scripts/generate-session-query-lowering-cases.mjs)
regenerates `cases.json` from the pinned, hash-verified upstream graders. Every
status and the exact bits of every float64 value must match. The fixture
covers:
- Event-order ties and repeated event indexes (last position wins).
- Dropped elements and the 5,000 and 5,001 item cap.
- 40 random sequences.
- About 430 keyword texts with the Kelvin sign, long s and dotted or dotless
  `i` next to word characters.

Seeded mutations (a changed DET, LAM or TT term, first-wins event order, an
unstable tiebreak, an off-by-one cap, or `(?i)\b` matching) each fail it.

The reference lowering is stricter than upstream in one declared way.
Upstream coerces `firstEventIndex` and event `index` with `Number()`, so `null`
and numeric strings still order visits. Strict S1 typing reports such a trace
unavailable instead, and the fixture records both outcomes. This SQL is
evidence for the closed forms, not the production lowerer. The KQL side is
not executed (§3.1).

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
- [ ] **Phase 4:** L2, L5 (with gaps-and-islands over `row-number`) and L6, lowered under the §3.1 constraints on DuckDB and KQL. Unlocks `tool-failure-count`, `tool-success-rate` and `trajectory-efficiency` after the reconciliation parity fixture (§4.1).
- [ ] **Phase 5:** L4 and nested S1 (`dependsOn`, IR `events`, state `firstEventIndex`). Unlocks `dependency-order-violation-rate`, `evidence-saturation-stopping-lag` and, through the linear closed forms in §4.2, `recurrence-laminarity`, `recurrence-determinism` and `recurrence-trapping-time`.
- [ ] Decide on E1 through privacy review; until then the `evidence-gated` graders stay unsupported.
- [ ] Decide whether to approve the retry message and objective `description` fields. Only then consider a closed ASCII keyword-match predicate (G1, §3.1) for `retries` and `policy-near-miss`; typed upstream retry or guard flags would remove the need.

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
