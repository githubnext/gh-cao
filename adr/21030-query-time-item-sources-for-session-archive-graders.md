# ADR 21030: Query-Time Item Sources for Session-Archive Trajectory Graders

## Status

Draft

## Context

Pull request #21030 tracks whether each of the 35 upstream `github/gh-aw` built-in graders can be recomputed as a reviewed `session-query` over the private session archive. Before this pull request, only the four usage metrics were implemented. The `archive-events` source is one row per event and cannot express per-element collections.

Four trajectory graders need element-level rows from the `data.objectives`, `data.actions`, and `data.states` arrays of the last agent-phase `trajectory.ir` event of each trace: `objective-coverage`, `premature-termination-gap`, `grounding-accuracy`, and `state-revisit-probability-rep`.

The pull request also records these related facts:

- `specs/session-archive-grader-queries.md` is a living tracker. Its invariants require computation at query time, with no reconciliation, state identity, guard classification, or score persisted at capture. The tracker approves no new evidence.
- ADX and native backends keep `AnalyticalCapabilities` and do not advertise the item sources. Their query plans are rejected before execution.
- The KQL lowering is deferred to Phase 1b. The pull request states that the Kusto emulator does not run on the ARM development host.
- The pull request has no evidence of an operator incident, a hosted latency requirement, or a review of earlier alternatives. Those are Not inferable from current pull request evidence.

## Decision

Add three item sources, `archive-objectives`, `archive-actions`, and `archive-states`, to the local DuckDB analytical backend only.

Each source binds the last agent-phase `trajectory.ir` event of every complete active trace and flattens the matching collection at query time. Nothing is persisted, and the archive schema is unchanged. Every trace yields one anchor row, so traces without the array stay visible. Rows carry the trace binding, the IR `Sequence`, `Items`, `Ordinal`, and only the reviewed typed element fields. Wrong JSON types are reported as `invalid` and are not coerced. `PayloadJSON` is never exposed.

Backends advertise item sources through an optional `analytical.sources` list in the TypeSpec capability model. `LocalAnalyticalCapabilities` adds them for DuckDB. `Plan.Require` rejects any plan whose source is not advertised, and ADX and native backends do not advertise them. Declared divergences fail toward `unavailable` rather than a fabricated value: an excluded or redacted collection, and the 5,000-item cap checked only on the queried collection.

## Alternatives Considered

- **Persist item rows or reconciled state at capture time.** This was rejected by the tracker invariant that computation happens at query time and that capture persists no state identity, guard classification, or score. The pull request does not record any further rationale. Rationale beyond the invariant: Not inferable from current pull request evidence.
- **Advertise item sources on ADX now.** This is deferred to Phase 1b. The pull request has no reviewed KQL lowering and cannot run the Kusto emulator on the ARM host. Until a lowering is accepted, a backend must reject the plan rather than approximate it.

## Consequences

**Positive:**

- Four trajectory graders reach parity with pinned upstream on local DuckDB, with no schema change and no new persisted rows.
- Capability advertisement is explicit. A backend without a reviewed lowering rejects the plan instead of approximating it.
- Wrong JSON types become `invalid` rather than coerced values.

**Negative:**

- Hosted ADX and native backends cannot execute item-source queries until Phase 1b. Hosted parity for these four graders is absent.
- Flattening happens on each query. Cost is paid per query, not amortized at capture.
- Excluded and redacted collections report `unavailable`, and the item cap is checked only on the queried collection. Both are declared divergences from upstream, which sees the original data.
- The public contract grows: the `from` enum is widened, `analytical.sources` is added, and the OpenAPI and JSON Schemas are regenerated. The MCP contract and `specs/session-archive.md` change to match.
- Item sources forbid `first` and real-valued `sum`, so they cannot be used for every existing reducer.
