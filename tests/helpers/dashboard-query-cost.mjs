import path from "node:path";
import { loadDashboardSource } from "../../dashboard/report/bundle-dashboards.mjs";
import { analyzeDashboardComplexity } from "../../dashboard/report/query-complexity.mjs";
import {
  executeDashboardQuery, createDashboardQueryBudget, queryInputNames, paginateDashboardSources,
} from "../../dashboard/site/src/data/queries/declarative.js";
import { dashboardBenchmarkQueries } from "./dashboard-benchmark-queries.mjs";

export const DEFAULT_QUERY_COST_CANDIDATES = 10;
export const QUERY_COST_PAGE_LIMIT = 500;

export async function readDashboardDocument(documentPath) {
  const document = (await loadDashboardSource(path.resolve(documentPath))).document;
  return {
    ...document,
    dashboard: { ...document.dashboard, queries: dashboardBenchmarkQueries(document.dashboard) },
  };
}

export function selectCostlyQueries(document, limit = DEFAULT_QUERY_COST_CANDIDATES) {
  if (!Number.isInteger(limit) || limit < 1) {
    throw new TypeError("Query cost candidate limit must be a positive integer.");
  }
  const analysis = analyzeDashboardComplexity(document);
  return {
    analysis,
    candidates: analysis.ranking.slice(0, limit).map((query) => ({
      name: query.name, rank: query.rank, class: query.class, "used-by": query["used-by"],
      "static-total-row-read-units": query["total-row-read-units"],
      "static-direct-row-read-units": query["direct-row-read-units"],
      "static-dependency-row-read-units": query["dependency-row-read-units"],
    })),
  };
}

export function estimateRetainedBytes(value) {
  const seen = new Set();
  const walk = (current) => {
    if (current === null || current === undefined) return 0;
    if (typeof current === "boolean") return 4;
    if (typeof current === "number") return 8;
    if (typeof current === "bigint") return 16;
    if (typeof current === "string") return 16 + current.length * 2;
    if (typeof current !== "object") return 8;
    if (seen.has(current)) return 0;
    seen.add(current);
    if (Array.isArray(current)) return 32 + current.reduce((total, entry) => total + 8 + walk(entry), 0);
    return Object.entries(current).reduce((total, [key, entry]) => total + 16 + key.length * 2 + walk(entry), 32);
  };
  return walk(value);
}

function collectGarbage() {
  if (typeof globalThis.gc !== "function") return false;
  globalThis.gc();
  globalThis.gc();
  return true;
}

// Pure query-engine unit measurement; operational benchmarks use Go/PostgreSQL.
export function measureDashboardQueryCost(definition, sources, defect) {
  const inputRows = Object.fromEntries(queryInputNames(definition).map((name) => [
    name, Array.isArray(sources[name]?.rows) ? sources[name].rows.length : 0,
  ]));
  const budget = createDashboardQueryBudget({ timeout: 600_000, maxOperations: 2_000_000_000 });
  const collected = collectGarbage();
  const baselineHeapBytes = process.memoryUsage().heapUsed;
  const startedAt = performance.now();
  let rows = [];
  let status = "available";
  let failure;
  let totalRows = 0;
  try {
    const result = executeDashboardQuery(definition, sources, defect, budget);
    const page = paginateDashboardSources(
      { [definition.name]: result }, { [definition.name]: { limit: QUERY_COST_PAGE_LIMIT } },
    )[definition.name];
    rows = page.rows;
    totalRows = Number(page.metadata?.["total-row-count"] ?? 0);
    status = String(result.metadata?.availability ?? "available");
    if (status === "unavailable") failure = String(result.metadata?.["query-diagnostic"] ?? "query is unavailable");
  } catch (error) {
    status = "failed";
    failure = error instanceof Error ? error.message : String(error);
  }
  const durationMs = performance.now() - startedAt;
  const executedHeapBytes = Math.max(0, process.memoryUsage().heapUsed - baselineHeapBytes);
  const resultBytes = estimateRetainedBytes(rows);
  collectGarbage();
  const retainedHeapBytes = Math.max(0, process.memoryUsage().heapUsed - baselineHeapBytes);
  return {
    query: definition.name, status, ...(failure ? { failure } : {}),
    "duration-ms": Number(durationMs.toFixed(3)), operations: budget.operations,
    "input-rows": inputRows,
    "input-row-total": Object.values(inputRows).reduce((total, count) => total + count, 0),
    rows: rows.length, "total-rows": totalRows, "result-bytes": resultBytes,
    "bytes-per-row": rows.length === 0 ? 0 : Math.round(resultBytes / rows.length),
    "retained-heap-bytes": retainedHeapBytes,
    "execution-heap-bytes": Math.max(executedHeapBytes, retainedHeapBytes),
    "heap-measured": collected,
  };
}
