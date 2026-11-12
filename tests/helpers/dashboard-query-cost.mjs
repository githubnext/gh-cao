/**
 * Static browser-query test helper: headless computational and space cost
 * measurement for Dashboard Language queries against a SQLite-backed IndexedDB
 * snapshot. This is not the hosted Postgres + Go deployed benchmark.
 *
 * The static query cost evaluator (`analyzeDashboardComplexity`) ranks every
 * query by normalized row-read units; the most expensive candidates are then
 * executed through the production query boundary so the measured time,
 * operation count, result size, and retained heap footprint are observable
 * without a browser in focused tests.
 */
import { DatabaseSync } from "node:sqlite";
import { copyFile, mkdtemp, readFile, rm } from "node:fs/promises";
import { existsSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { loadDashboardSource } from "../../dashboard/report/bundle-dashboards.mjs";
import { analyzeDashboardComplexity } from "../../dashboard/report/query-complexity.mjs";
import {
  executeDashboardQueries,
  executeDashboardQuery,
  createDashboardQueryBudget,
  dashboardQueryDefects,
  queryInputNames,
  resolveDashboardQuerySources,
  paginateDashboardSources,
} from "../../dashboard/site/src/data/queries/declarative.js";
import {
  queryDatabaseSources,
  queryIndexedDatabaseSources,
} from "../../dashboard/site/src/data/queries/database.js";
import {
  canonicalDatabaseName,
  DATABASE_VERSION,
} from "../../dashboard/site/src/data/storage/indexeddb.js";
import { installSqliteIndexedDB } from "../../dashboard/site/src/data/storage/sqlite-indexeddb.js";
import { dashboardBenchmarkQueries } from "./dashboard-benchmark-queries.mjs";

export const DEFAULT_QUERY_COST_CANDIDATES = 10;
export const QUERY_COST_PAGE_LIMIT = 500;
// Generous bounds: the benchmark measures cost instead of enforcing the
// production budget, and the deployed snapshot is larger than a browser page.
const BENCHMARK_TIMEOUT_MS = 600_000;
const BENCHMARK_MAX_OPERATIONS = 2_000_000_000;

/**
 * Reads the canonical schema version recorded in a SQLite snapshot without
 * opening it through the IndexedDB shim, which would trigger a destructive
 * upgrade when the versions differ.
 *
 * @param {string} databasePath
 * @returns {number | null}
 */
export function readSnapshotDatabaseVersion(databasePath) {
  const filename = path.resolve(databasePath);
  if (!existsSync(filename)) return null;
  const connection = new DatabaseSync(filename, { readOnly: true });
  try {
    const table = connection
      .prepare("SELECT name FROM sqlite_master WHERE type = 'table' AND name = '__idb_databases'")
      .get();
    if (!table) return null;
    const row = connection
      .prepare("SELECT version FROM __idb_databases WHERE name = ?")
      .get(canonicalDatabaseName());
    const version = Number(/** @type {any} */ (row)?.version);
    return Number.isInteger(version) ? version : null;
  } finally {
    connection.close();
  }
}

/**
 * Locates the deployed JSONL payload directories published alongside the
 * SQLite snapshot. Canonical data is a derived cache, so these shards are the
 * authoritative input the snapshot is built from.
 *
 * @param {string} databasePath
 */
export function deployedPayloadSources(databasePath) {
  const directory = path.dirname(path.resolve(databasePath));
  const runs = path.join(directory, "gh-aw-logs-runs");
  const records = path.join(directory, "gh-aw-logs-records");
  if (existsSync(runs) && existsSync(records)) {
    return { kind: "phased", arguments: ["--runs-dir", runs, "--records-dir", records] };
  }
  const shards = path.join(directory, "gh-aw-logs-shards");
  if (existsSync(shards)) {
    return { kind: "shards", arguments: ["--input-dir", shards] };
  }
  return null;
}

/**
 * Prepares a measurable canonical database and installs the SQLite-backed
 * IndexedDB shim over it, returning a disposer that removes the working copy
 * and restores the previously installed globals.
 *
 * The snapshot is never opened in place: opening the canonical database
 * deletes every object store when the stored schema version is older than
 * `DATABASE_VERSION`, so measuring the downloaded file directly would destroy
 * it and silently report an empty projection. An incompatible snapshot must
 * be explicitly replaced by its producer; the benchmark does not repair it.
 *
 * @param {string} databasePath
 */
export async function openDeployedDatabase(databasePath) {
  const previousIndexedDB = globalThis.indexedDB;
  const previousKeyRange = globalThis.IDBKeyRange;
  const snapshot = path.resolve(databasePath);
  const version = readSnapshotDatabaseVersion(snapshot);
  if (version !== DATABASE_VERSION) {
    throw new Error(`A verified canonical SQLite snapshot at schema version ${DATABASE_VERSION} is required; Activity repair is retired.`);
  }
  const directory = await mkdtemp(path.join(tmpdir(), "cao-query-cost-snapshot-"));
  const working = path.join(directory, "snapshot.sqlite");
  await copyFile(snapshot, working);
  const indexedDB = installSqliteIndexedDB(working);
  return {
    indexedDB,
    path: working,
    rebuilt: false,
    async close() {
      globalThis.indexedDB = previousIndexedDB;
      globalThis.IDBKeyRange = previousKeyRange;
      await rm(directory, { recursive: true, force: true });
    },
  };
}

/** @param {string} documentPath */
export async function readDashboardDocument(documentPath) {
  const document = (await loadDashboardSource(path.resolve(documentPath))).document;
  return {
    ...document,
    dashboard: {
      ...document.dashboard,
      queries: dashboardBenchmarkQueries(document.dashboard),
    },
  };
}

/**
 * Ranks every declared query with the static cost evaluator and returns the
 * most expensive candidates to investigate.
 *
 * @param {unknown} document
 * @param {number} [limit]
 */
export function selectCostlyQueries(document, limit = DEFAULT_QUERY_COST_CANDIDATES) {
  if (!Number.isInteger(limit) || limit < 1) {
    throw new TypeError("Query cost candidate limit must be a positive integer.");
  }
  const analysis = analyzeDashboardComplexity(document);
  return {
    analysis,
    candidates: analysis.ranking.slice(0, limit).map((query) => ({
      name: query.name,
      rank: query.rank,
      class: query.class,
      "used-by": query["used-by"],
      "static-total-row-read-units": query["total-row-read-units"],
      "static-direct-row-read-units": query["direct-row-read-units"],
      "static-dependency-row-read-units": query["dependency-row-read-units"],
    })),
  };
}

/**
 * Approximates the retained bytes of a materialized query result. Object
 * identity is tracked so shared rows are only counted once, matching what the
 * dashboard retains in the worker heap.
 *
 * @param {unknown} value
 */
export function estimateRetainedBytes(value) {
  const seen = new Set();
  /** @param {unknown} current */
  const walk = (current) => {
    if (current === null || current === undefined) return 0;
    if (typeof current === "boolean") return 4;
    if (typeof current === "number") return 8;
    if (typeof current === "bigint") return 16;
    if (typeof current === "string") return 16 + current.length * 2;
    if (typeof current !== "object") return 8;
    if (seen.has(current)) return 0;
    seen.add(current);
    if (Array.isArray(current)) {
      return 32 + current.reduce((total, entry) => total + 8 + walk(entry), 0);
    }
    return Object.entries(current).reduce(
      (total, [key, entry]) => total + 16 + key.length * 2 + walk(entry),
      32,
    );
  };
  return walk(value);
}

function collectGarbage() {
  if (typeof globalThis.gc === "function") {
    globalThis.gc();
    globalThis.gc();
    return true;
  }
  return false;
}

/**
 * Measures one query executed over already materialized input sources.
 *
 * @param {Record<string, any>} definition
 * @param {Record<string, any>} sources
 * @param {string | undefined} defect
 */
export function measureDashboardQueryCost(definition, sources, defect) {
  const inputRows = Object.fromEntries(queryInputNames(definition).map((name) => [
    name,
    Array.isArray(sources[name]?.rows) ? sources[name].rows.length : 0,
  ]));
  const budget = createDashboardQueryBudget({
    timeout: BENCHMARK_TIMEOUT_MS,
    maxOperations: BENCHMARK_MAX_OPERATIONS,
  });
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
      { [definition.name]: result },
      { [definition.name]: { limit: QUERY_COST_PAGE_LIMIT } },
    )[definition.name];
    rows = page.rows;
    totalRows = Number(page.metadata?.["total-row-count"] ?? 0);
    status = String(result.metadata?.availability ?? "available");
    if (status === "unavailable") {
      failure = String(result.metadata?.["query-diagnostic"] ?? "query is unavailable");
    }
  } catch (error) {
    status = "failed";
    failure = error instanceof Error ? error.message : String(error);
  }
  const durationMs = performance.now() - startedAt;
  // Sampled once after execution: allocations already collected by the engine
  // are invisible, so this is an approximation of the execution high-water
  // mark rather than an exact peak.
  const executedHeapBytes = Math.max(0, process.memoryUsage().heapUsed - baselineHeapBytes);
  const resultBytes = estimateRetainedBytes(rows);
  collectGarbage();
  const retainedHeapBytes = Math.max(0, process.memoryUsage().heapUsed - baselineHeapBytes);
  const executionHeapBytes = Math.max(executedHeapBytes, retainedHeapBytes);
  return {
    query: definition.name,
    status,
    ...(failure ? { failure } : {}),
    "duration-ms": Number(durationMs.toFixed(3)),
    operations: budget.operations,
    "input-rows": inputRows,
    "input-row-total": Object.values(inputRows).reduce((total, count) => total + count, 0),
    rows: rows.length,
    "total-rows": totalRows,
    "result-bytes": resultBytes,
    "bytes-per-row": rows.length === 0 ? 0 : Math.round(resultBytes / rows.length),
    "retained-heap-bytes": retainedHeapBytes,
    "execution-heap-bytes": executionHeapBytes,
    "heap-measured": collected,
  };
}

/**
 * Executes each statically selected query against the deployed SQLite
 * snapshot and reports its computational and space cost.
 *
 * @param {{ databasePath: string, document: unknown, limit?: number }} options
 */
export async function benchmarkDashboardQueryCost({ databasePath, document, limit }) {
  const dashboard = /** @type {any} */ (document)?.dashboard;
  const definitions = dashboardBenchmarkQueries(dashboard ?? { queries: [] });
  const { analysis, candidates } = selectCostlyQueries(document, limit ?? DEFAULT_QUERY_COST_CANDIDATES);
  const index = new Map(definitions
    .filter((query) => query && typeof query.name === "string")
    .map((query) => [query.name, query]));
  const defects = dashboardQueryDefects(definitions);
  // Read before opening: the shim's upgrade path rewrites the stored version.
  const snapshotVersion = readSnapshotDatabaseVersion(databasePath);
  const database = await openDeployedDatabase(databasePath);
  const indexedDB = database.indexedDB;
  try {
    const required = resolveDashboardQuerySources(definitions, candidates.map(({ name }) => name));
    const databaseStartedAt = performance.now();
    // Mirrors the production worker boundary: the canonical projection resolves
    // every required name and declared queries execute over its result.
    const [databaseSources, nativeSources] = await Promise.all([
      queryDatabaseSources(indexedDB, {}, required),
      queryIndexedDatabaseSources(indexedDB, {}, definitions, required),
    ]);
    const databaseMs = performance.now() - databaseStartedAt;
    const dependencySources = executeDashboardQueries(
      definitions,
      { ...databaseSources, ...nativeSources },
      required.filter((name) => index.has(name)),
      { timeout: BENCHMARK_TIMEOUT_MS, maxOperations: BENCHMARK_MAX_OPERATIONS },
    );
    /** @type {Record<string, any>} */
    const sources = { ...databaseSources, ...nativeSources };
    for (const [name, source] of Object.entries(dependencySources)) {
      // Materialize every dependency before measurement so each candidate
      // reports only its own execution cost.
      sources[name] = { source: name, rows: source.rows, metadata: source.metadata };
    }
    const measurements = candidates
      .filter((candidate) => index.has(candidate.name))
      .map((candidate) => ({
        ...candidate,
        ...measureDashboardQueryCost(index.get(candidate.name), sources, defects.get(candidate.name)),
      }));
    const projected = Object.entries(databaseSources).filter(([name]) => !index.has(name));
    const sourceRecords = Object.fromEntries(projected.map(([name, source]) => [
      name,
      Array.isArray(source?.rows) ? source.rows.length : 0,
    ]));
    const databaseRecords = Object.values(sourceRecords)
      .reduce((total, count) => total + count, 0);
    return {
      dashboard: typeof dashboard?.id === "string" ? dashboard.id : "dashboard",
      queries: definitions.length,
      candidates: measurements.length,
      "page-limit": QUERY_COST_PAGE_LIMIT,
      database: {
        path: path.resolve(databasePath),
        "duration-ms": Number(databaseMs.toFixed(3)),
        "records-read": databaseRecords,
        sources: projected.length,
        "source-records": sourceRecords,
        "empty-sources": Object.entries(sourceRecords)
          .filter(([, count]) => count === 0)
          .map(([name]) => name),
        "snapshot-version": snapshotVersion,
        "expected-version": DATABASE_VERSION,
        "version-compatible": snapshotVersion === DATABASE_VERSION,
        "rebuilt-from-payloads": database.rebuilt,
        ...(database.rebuildFailure ? { "rebuild-error": database.rebuildFailure } : {}),
      },
      "static-model": analysis.summary.model,
      "static-materialize-all-row-read-units": analysis.summary["materialize-all-row-read-units"],
      measurements,
      "most-costly-by-time": rankMeasurements(measurements, "duration-ms"),
      "most-costly-by-memory": rankMeasurements(measurements, "result-bytes", "retained-heap-bytes"),
    };
  } finally {
    await database.close();
  }
}

/**
 * @param {any[]} measurements
 * @param {string} field
 * @param {string} [tiebreak]
 */
function rankMeasurements(measurements, field, tiebreak = "duration-ms") {
  return measurements
    .toSorted((left, right) => right[field] - left[field] || right[tiebreak] - left[tiebreak])
    .map(({ query }) => query);
}

/** @param {number} value */
function formatBytes(value) {
  const units = ["B", "KiB", "MiB", "GiB"];
  let size = Math.max(0, Number(value) || 0);
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${unit === 0 ? Math.round(size) : size.toFixed(2)} ${units[unit]}`;
}

/** @param {number} value */
function formatCount(value) {
  return Number(value ?? 0).toLocaleString("en-US");
}

/** @param {string} value */
function markdownCode(value) {
  const safe = String(value).replaceAll("`", "'").replaceAll("|", "\\|").replaceAll("\n", " ");
  return `\`${safe}\``;
}

/**
 * Renders the top most costly queries for a CI job summary or pull request.
 *
 * @param {Awaited<ReturnType<typeof benchmarkDashboardQueryCost>>} report
 */
export function dashboardQueryCostMarkdown(report) {
  const index = new Map(report.measurements.map((measurement) => [measurement.query, measurement]));
  const byTime = report["most-costly-by-time"].map((query) => index.get(query));
  const byMemory = report["most-costly-by-memory"].map((query) => index.get(query));
  const failures = report.measurements.filter((measurement) =>
    !["available", "empty"].includes(measurement.status));
  const sourceRecords = Object.entries(report.database["source-records"] ?? {})
    .toSorted(([left, leftCount], [right, rightCount]) => rightCount - leftCount || left.localeCompare(right));
  const emptySources = report.database["empty-sources"] ?? [];
  const snapshotVersion = report.database["snapshot-version"];
  const expectedVersion = report.database["expected-version"];
  const versionDiagnostic = report.database["version-compatible"]
    ? []
    : report.database["rebuilt-from-payloads"]
      ? [
        "",
        `> The published snapshot is at canonical schema version **${snapshotVersion ?? "unknown"}** while the reader expects **${expectedVersion}**, so the canonical database was rebuilt from the deployed JSONL payloads before measuring. Republish the deployed snapshot from the current schema to measure it directly.`,
      ]
      : [
        "",
        snapshotVersion === null
          ? `> The snapshot records no canonical schema version, so it is not a canonical dashboard database. The reader expects version ${expectedVersion}.`
          : `> The snapshot was written at canonical schema version **${snapshotVersion}** but the reader expects **${expectedVersion}**. Opening an older snapshot rebuilds the canonical stores from scratch, which discards every record; republish the deployed data from the current schema.`,
        ...(report.database["rebuild-error"]
          ? [`> Rebuilding from the deployed JSONL payloads failed: ${report.database["rebuild-error"]}`]
          : ["> The deployed JSONL payloads were not available to rebuild the canonical database."]),
      ];
  return [
    "### Dashboard query cost (deployed SQLite snapshot)",
    "",
    `Measured the **${report.candidates} most expensive of ${report.queries} queries** chosen by the static query cost evaluator (${report["static-model"]}, ${formatCount(report["static-materialize-all-row-read-units"])} normalized row-read units to materialize all queries).`,
    `Each measurement retains the first **${report["page-limit"]} result rows**; computational cost includes execution over the complete inputs.`,
    "",
    `Canonical projection read **${formatCount(report.database["records-read"])} records** across **${report.database.sources} sources** in **${report.database["duration-ms"].toFixed(2)} ms**.`,
    ...(report.database["records-read"] > 0
      ? []
      : [
        "",
        "> The canonical projection returned no records, so every measurement below is meaningless.",
      ]),
    ...versionDiagnostic,
    ...(sourceRecords.length === 0
      ? []
      : [
        "",
        "<details><summary><b>Canonical projection by source</b></summary>",
        "",
        "| Source | Records |",
        "| --- | ---: |",
        ...sourceRecords.map(([name, count]) => `| ${markdownCode(name)} | ${formatCount(count)} |`),
        "",
        "</details>",
      ]),
    ...(emptySources.length === 0 || report.database["records-read"] === 0
      ? []
      : [
        "",
        `Empty database tables: ${emptySources.map((name) => markdownCode(name)).join(", ")}.`,
      ]),
    "",
    "### Computational cost",
    "",
    "| Rank | Query | Static rank | Input rows | Output rows | Operations | Time (ms) |",
    "| ---: | --- | ---: | ---: | ---: | ---: | ---: |",
    ...byTime.map((measurement, index) => `| ${index + 1} | ${markdownCode(measurement.query)} | ${measurement.rank} | ${formatCount(measurement["input-row-total"])} | ${formatCount(measurement.rows)} | ${formatCount(measurement.operations)} | ${measurement["duration-ms"].toFixed(2)} |`),
    "",
    "### Space cost",
    "",
    "| Rank | Query | Output rows | Result size | Bytes/row | Retained heap | Heap after run |",
    "| ---: | --- | ---: | ---: | ---: | ---: | ---: |",
    ...byMemory.map((measurement, index) => `| ${index + 1} | ${markdownCode(measurement.query)} | ${formatCount(measurement.rows)} | ${formatBytes(measurement["result-bytes"])} | ${formatBytes(measurement["bytes-per-row"])} | ${formatBytes(measurement["retained-heap-bytes"])} | ${formatBytes(measurement["execution-heap-bytes"])} |`),
    "",
    report.measurements.every((measurement) => measurement["heap-measured"])
      ? "_Retained heap is measured after collection while the result is held; heap after run is a single post-execution sample, not an exact peak._"
      : "_Heap measurements are approximate: run Node.js with `--expose-gc` for settled retained-heap readings._",
    ...(failures.length === 0
      ? []
      : [
        "",
        "### Unavailable queries",
        "",
        ...failures.map((measurement) => `- ${markdownCode(measurement.query)}: ${measurement.failure ?? measurement.status}`),
      ]),
    "",
  ].join("\n");
}
