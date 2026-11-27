# ADR 21647: Track Workflow Compiler Versions From Runs and Inventory

## Status

Draft

## Context

Pull request #21647 changes how the dashboard records the gh-aw compiler version of a workflow. Its body states that run ingestion did not update the workflow's compiler version.

- Before this pull request, the Workflow record carried one `ghAwVersion` / `ghAwUpdateState` pair with no observation clock or source. The pull request adds source fields to `server/spec/storage.tsp`.
- The pull request retains two observations per workflow:
  - a run observation: `ghAwRunVersion`, `ghAwRunVersionObservedAt`, and `ghAwRunVersionRunId`;
  - an inventory observation: `ghAwInventoryVersion`, `ghAwInventoryVersionObservedAt`, and `ghAwInventoryUpdateState`.
- The effective `ghAwVersion` is derived from these observations, and `ghAwVersionSource` records `run` or `inventory`.
- The same reconciliation appears in three layers:
  - browser ingestion: `mergeWorkflowCompilerEvidence` in `dashboard/site/src/data/model/fields.js`;
  - local snapshot merge: `mergeDashboardWorkflowCompiler` in `internal/cli/dashboard_snapshot.go`;
  - hosted PostgreSQL projection: `recordWorkflowCompilerVersion` in `server/internal/postgresx/acquisition_projection.go`.
- The projection code explains the clock comparison: "Compare their recorded clocks, rather than publication order, so replay and out-of-order collection cannot make an older run look current."
- `specs/dashboard-data.md` moves the canonical model from 37 to 38 and IndexedDB from 50 to 51. Schema-37 native shards are rewritten once. The pull request body states that hosted PostgreSQL requires a fresh schema.
- Branch commits: "Track workflow compiler observation sources", "Preserve compiler evidence through all ingestion paths", and "Preserve workflow paging keys during evidence merges".

Not inferable from current pull request evidence:

- why an equal-clock tie favours the run rather than the inventory;
- why the schema change requires a fresh hosted PostgreSQL schema rather than an in-place migration;
- whether a single shared implementation of the rule was considered before three parallel implementations were written.

## Decision

1. Keep run and inventory compiler observations as separate, timestamped evidence on each Workflow.
2. Select the effective version by the most recent recorded observation clock, not by publication or ingestion order. A run wins an equal-clock tie. Set `ghAwVersionSource` to match the winner.
3. Missing or unknown versions never replace a known observation. An older or replayed observation never replaces a newer one.
4. Attribute the inventory update comparison to a run only when the run's version equals the inventory version. Otherwise, `ghAwUpdateState` is `unknown`.
5. These values are evidence only. They grant no policy, activation, or write authority.
6. Apply the same rule in browser ingestion, local snapshot merge, and hosted PostgreSQL projection, and version the schemas (canonical model 38, IndexedDB 51).

## Alternatives Considered

- **Overwrite the workflow version on each ingested run (last write wins by publication order).** Not chosen. The projection code compares recorded clocks instead of publication order so that replay and out-of-order collection cannot make an older run look current. The specification states that replay of an older observation MUST NOT replace a newer one.
- **Keep inventory as the only compiler source.** Not chosen. This leaves the gap the pull request body describes: run ingestion does not update the compiler version.
- **Store one merged version field without per-source provenance.** Not chosen. The pull request requires retaining each source's version and observation time, plus the run ID or inventory update state. Any further reason is not inferable from current pull request evidence.

## Consequences

**Positive:**

- Replay and out-of-order collection converge on the same effective version because selection depends on recorded clocks.
- Provenance stays visible (source, observation time, run ID, and inventory comparison), and mixed evidence is not collapsed into a fabricated single version.
- Sparse or unknown evidence cannot erase a known value.

**Negative and operational:**

- Three implementations must agree on the clock comparison and the equal-clock tie-break. The changed files include tests in each layer, but no shared conformance fixture appears among them.
- A run that reports a version different from the inventory sets `ghAwUpdateState` to `unknown` until a matching inventory scan. Update-available visibility can therefore lag.
- An older known observation remains in effect until a newer known observation replaces it. The specification defines no staleness threshold.
- Schema-37 shards need a one-time rewrite. Hosted PostgreSQL requires a fresh schema, so hosted deployments have no in-place upgrade path.
