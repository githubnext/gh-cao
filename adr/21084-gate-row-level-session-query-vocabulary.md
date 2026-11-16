# ADR 21084: Gate Row-Level Session-Query Vocabulary on Real-Backend Acceptance

## Status

Draft

## Context

Pull request #21084 is Phase 4 of `specs/session-archive-grader-queries.md`. It adds three analytical vocabulary extensions to the session-archive query contract:

- **L2:** row-level `compute` values, evaluated after `filter` and before `window` or `aggregate`.
- **L5:** `previous` and `row-number` windows, ported from Dashboard Language §5.5.4. The `groupby` must include the full trace binding, and `groupby` plus `order-by` must cover the input row key so that the order is total.
- **L6:** `state` predicates over the structured `Error`, `Usage` and `Provenance` families, without exposing family values.

The DuckDB lowering uses one common table expression for computations and one for windows. The KQL lowering serializes by partition and order keys and uses `row_number` and `prev`. Total window orders let both engines agree without tie-breaking.

The pull request records these facts:

- `AnalyticalCapabilities` advertises `compute`, `windows` and `stateFamilies` for every backend. This happened only after the real Kusto acceptance job (`session-archive-kusto`, [run 43917614253](https://github.com/githubnext/gh-cao/actions/runs/43917614253) and [run 43920740101](https://github.com/githubnext/gh-cao/actions/runs/43920740101)) accepted the lowering through `TestKustoAnalyticalCorpus`.
- The spec states that a backend whose lowering has not passed the real Kusto acceptance job MUST omit these capabilities and reject such plans. A local DuckDB run is not recorded as sufficient evidence.
- The shared corpus has no `Error`, `Usage` or `Provenance` values. The `Usage` and `Provenance` present-paths are not exercised on Kusto. The `Error`-present path is covered on DuckDB by `TestDuckDBStructuredFamilyStates` and on Kusto by a dedicated subtest.
- Computed `is-null` and `coalesce` over string fields are rejected because absent and empty strings are indistinguishable across engines.
- The pull request unlocks no grader. The tool-reconciliation graders (`tool-failure-count`, `tool-success-rate`, `trajectory-efficiency`) are reclassified as `vocabulary-gated` (G3), because the FIFO pointer's skips need sequential state.
- The pull request has no evidence of an operator incident, a hosted latency requirement, or a review of other designs. Those are Not inferable from current pull request evidence.

## Decision

1. Row-level `compute`, `previous` and `row-number` windows, and structured-family `state` predicates belong to the session-query vocabulary. A backend advertises `analytical.compute`, `analytical.windows` and `analytical.stateFamilies` only after its lowering passes the real Kusto acceptance job. A backend without an accepted lowering omits them and rejects plans that use them. It must not approximate them.
2. Window orders must be total. The `groupby` includes the complete trace binding, and `groupby` plus `order-by` covers the input row key, which is the trace binding plus `Sequence` or `Ordinal`, or an earlier stage's key.
3. Computed `is-null` and `coalesce` over string fields are rejected.
4. Tool-reconciliation graders stay `vocabulary-gated` (G3). Phase 4 does not approximate their sequential state with row-level vocabulary.

## Alternatives Considered

- **Advertise the vocabulary on a backend once its lowering passes locally, without real-backend acceptance.** The spec rejects this: a backend whose lowering has not passed the real Kusto acceptance job MUST omit the capabilities and reject such plans. Rationale beyond the spec: Not inferable from current pull request evidence.
- **Permit computed string `is-null` and `coalesce`.** The spec rejects this because empty and absent strings are indistinguishable across the two engines, so a result could differ by backend. Rationale beyond the spec: Not inferable from current pull request evidence.

## Consequences

**Positive:**

- A backend advertises only vocabulary that a real backend has accepted. An unsupported plan is rejected rather than approximated.
- Row-level compute, windows and gaps-and-islands work on DuckDB and Kusto without a new operator. Gaps-and-islands is three stages over `row-number`.
- Total window orders make results independent of tie-breaking across engines.

**Negative:**

- The public contract grows. `analytical.compute`, `analytical.windows` and `analytical.stateFamilies` are new capability fields, and the TypeSpec, OpenAPI, JSON Schemas, MCP contract and specs change to match.
- The `Usage` and `Provenance` present-paths are not exercised on Kusto, because the corpus has no such values. The tracker records this as a declared limit.
- Tool-reconciliation graders remain `vocabulary-gated` (G3). Phase 4 unlocks no grader.
- Computed string `is-null` and `coalesce` are rejected, so graders that need string defaults must use other forms.
- Each definition is limited to 32 compute values and eight windows, as the spec declares.
