import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { executeCanonicalQuery } from "../../activity/canonical-query.mjs";
import { executeDashboardQuery } from "../../dashboard/site/src/data/queries/declarative.js";

const DAY_MS = 86_400_000;
const ACTIVITY_CLI = fileURLToPath(new URL("../../activity/cao.mjs", import.meta.url));
const MINIMUM_COMPLETED_RUNS = 3;
const TERMINAL_CONCLUSIONS = [
  "success",
  "failure",
  "cancelled",
  "timed_out",
  "startup_failure",
  "action_required",
  "neutral",
  "skipped",
  "stale",
];
const FAILURE_CONCLUSIONS = ["failure", "timed_out", "startup_failure"];

export const historyBatch = true;
export const localCollection = true;

export const definition = {
  schemaVersion: 3,
  slug: "optimization-token-optimizer",
  sourcePath: ".github/workflows/optimization-token-optimizer.md",
  repository: "githubnext/gh-cao",
  workflowName: "Optimization / Token Optimizer",
  adoption: {
    commit: "dc1e91886790cdac1bcfba3e8984186ad7f4a5e7",
    adoptedAt: "2026-09-15T23:30:36Z",
    baselineCommit: "ad7be08097b5b69cc15a8bf17dbc5e0ad12865ab",
    baselineAt: "2026-09-15T23:02:17Z",
  },
  evaluation: { mode: "baseline-comparable" },
  evidence: {
    key: "target-repository-agentic-workflow-efficiency",
    measurementKind: "interval",
    repositories: [
      "github/gh-aw",
      "github/gh-aw-actions",
      "github/gh-aw-firewall",
      "github/gh-aw-mcpg",
      "github/gh-aw-threat-detection",
      "githubnext/gh-cao",
    ],
    opportunity: "A declared non-Optimization agentic workflow in the authorized target repository with at least three completed runs, at least one successful run, and complete successful-run AI Credit coverage in the observation window.",
    filters: [
      "Measure runs in the authorized target repository, never the control repository as a proxy.",
      "Include declared agentic workflow sources and exclude the Optimization campaign itself.",
      "Require at least three completed runs and at least one successful run in the observation window.",
      "Require authoritative AI Credit for every successful run in the observation window.",
      "Measure native AI Credit per successful run, failed-run percentage, and cancelled-run percentage without normalizing them to a synthetic score.",
    ],
    collection: "Execute one bounded canonical Activity query over target-repository runs in the observation window, grouped by declared workflow. Apply the identical native formulas before and after adoption because AI Credit and run conclusions predate the Optimization workflow. Aggregate only eligible workflow rows into native repository measures. No eligible workflow or incomplete AI Credit evidence is missing.",
    window: { durationDays: 7, cadenceDays: 1, maturationDays: 0 },
  },
  model: {
    architecture: "Direct target-repository efficiency and reliability measurements in their native units.",
    recommendation: "Track AI Credit per successful run as the primary efficiency measure and retain failure and cancellation percentages as separate reliability diagnostics. Lower is better for every measure.",
    presentation: {
      label: "Target-repository efficiency and reliability",
      betterLabel: "Lower AI Credit per successful run, failure percentage, and cancellation percentage are better.",
    },
  },
  summary: {
    nativeLabel: "Native AI Credit and reliability measures for eligible target workflows",
  },
  metrics: [
    {
      id: "aic-per-successful-run",
      name: "AI Credit per successful run",
      role: "primary",
      formula: "authoritative AI Credit consumed by successful runs / successful runs with complete AI Credit evidence",
      direction: "decrease",
      unit: "aic-per-run",
      rollup: {
        numeratorField: "successfulRunAicTotal",
        denominatorField: "successfulRunCount",
      },
      presentation: {
        name: "AI Credit per successful run",
        legendLabel: "AIC / successful run",
        transform: "identity",
      },
    },
    {
      id: "failure-rate-percent",
      name: "Failure rate",
      role: "diagnostic",
      formula: "failed completed runs * 100 / completed runs",
      direction: "decrease",
      unit: "percent",
      rollup: {
        numeratorField: "failedRunPercentagePointTotal",
        denominatorField: "concludedRunCount",
      },
      presentation: {
        name: "Failure rate",
        legendLabel: "Failure rate",
        transform: "identity",
      },
    },
    {
      id: "cancellation-rate-percent",
      name: "Cancellation rate",
      role: "diagnostic",
      formula: "cancelled completed runs * 100 / completed runs",
      direction: "decrease",
      unit: "percent",
      rollup: {
        numeratorField: "cancelledRunPercentagePointTotal",
        denominatorField: "concludedRunCount",
      },
      presentation: {
        name: "Cancellation rate",
        legendLabel: "Cancellation rate",
        transform: "identity",
      },
    },
  ],
  validationExamples: {
    targetAttained: {
      eligibleWorkflowCount: 2,
      successfulRunCount: 10,
      successfulRunAicTotal: 40,
      concludedRunCount: 12,
      failedRunCount: 0,
      cancelledRunCount: 0,
      failedRunPercentagePointTotal: 0,
      cancelledRunPercentagePointTotal: 0,
    },
    targetMissed: {
      eligibleWorkflowCount: 2,
      successfulRunCount: 10,
      successfulRunAicTotal: 100,
      concludedRunCount: 12,
      failedRunCount: 2,
      cancelledRunCount: 1,
      failedRunPercentagePointTotal: 200,
      cancelledRunPercentagePointTotal: 100,
    },
    missing: {
      eligibleWorkflowCount: 0,
      successfulRunCount: 0,
      successfulRunAicTotal: 0,
      concludedRunCount: 0,
      failedRunCount: 0,
      cancelledRunCount: 0,
      failedRunPercentagePointTotal: 0,
      cancelledRunPercentagePointTotal: 0,
    },
    malformed: {
      eligibleWorkflowCount: "two",
      successfulRunCount: -1,
      successfulRunAicTotal: "large",
      concludedRunCount: null,
      failedRunCount: -1,
      cancelledRunCount: "one",
      failedRunPercentagePointTotal: null,
      cancelledRunPercentagePointTotal: null,
    },
  },
};

function fail(message) {
  throw new Error(message);
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    encoding: "utf8",
    input: options.input,
    maxBuffer: 1024 * 1024 * 1024,
  });
  if (result.status !== 0) fail(String(result.stderr || `${command} failed`).trim());
  return result.stdout;
}

function round(value) {
  return Math.round(value * 1_000_000_000) / 1_000_000_000;
}

function validCount(value) {
  return Number.isInteger(value) && value >= 0;
}

function ratio(numerator, denominator) {
  if (typeof numerator !== "number" || !Number.isFinite(numerator) || numerator < 0
      || !validCount(denominator) || denominator === 0) return null;
  return round(numerator / denominator);
}

export function scoreMetric(metricId, evidence) {
  const eligible = evidence?.eligibleWorkflowCount;
  if (!validCount(eligible) || eligible === 0) return null;

  if (metricId === "aic-per-successful-run") {
    return ratio(evidence.successfulRunAicTotal, evidence.successfulRunCount);
  }
  if (metricId === "failure-rate-percent") {
    return ratio(evidence.failedRunPercentagePointTotal, evidence.concludedRunCount);
  }
  if (metricId === "cancellation-rate-percent") {
    return ratio(evidence.cancelledRunPercentagePointTotal, evidence.concludedRunCount);
  }
  fail(`unknown metric: ${metricId}`);
}

function repositoryCoordinate(row) {
  const owner = String(row?.owner ?? "").toLowerCase();
  const repository = String(row?.repository ?? "").toLowerCase();
  return owner && repository ? `${owner}/${repository}` : "";
}

function isOptimizationWorkflow(workflowPath) {
  return /\/optimization(?:-|\.md$)/.test(String(workflowPath ?? ""));
}

function validWindowRow(row) {
  return typeof row?.declaredWorkflowPath === "string"
    && validCount(row.concludedRuns)
    && validCount(row.successfulRuns)
    && validCount(row.successfulRunsWithAic)
    && validCount(row.failedRuns)
    && validCount(row.cancelledRuns)
    && typeof row.aicPerSuccessfulRun === "number"
    && Number.isFinite(row.aicPerSuccessfulRun)
    && row.aicPerSuccessfulRun >= 0;
}

function eligibleRow(row) {
  return validWindowRow(row)
    && row.concludedRuns >= MINIMUM_COMPLETED_RUNS
    && row.successfulRuns > 0
    && row.successfulRunsWithAic === row.successfulRuns;
}

export function buildEvidence(rows, request) {
  const repository = String(request.repository ?? "").toLowerCase();
  const supported = new Set(
    definition.evidence.repositories.map((value) => value.toLowerCase()),
  );
  const inScope = (row) => {
    const coordinate = repositoryCoordinate(row);
    return repository ? coordinate === repository : supported.has(coordinate);
  };
  let eligibleWorkflowCount = 0;
  let successfulRunCount = 0;
  let successfulRunAicTotal = 0;
  let concludedRunCount = 0;
  let failedRunCount = 0;
  let cancelledRunCount = 0;
  const eligibleWorkflows = [];

  for (const row of rows.filter(inScope).filter((candidate) => !isOptimizationWorkflow(candidate.declaredWorkflowPath))) {
    if (!eligibleRow(row)) continue;
    eligibleWorkflowCount += 1;
    eligibleWorkflows.push(`${repositoryCoordinate(row)}/${row.declaredWorkflowPath}`);
    successfulRunCount += row.successfulRuns;
    successfulRunAicTotal += row.aicPerSuccessfulRun * row.successfulRuns;
    concludedRunCount += row.concludedRuns;
    failedRunCount += row.failedRuns;
    cancelledRunCount += row.cancelledRuns;
  }

  const duration = Date.parse(request.windowEnd) - Date.parse(request.windowStart);
  return {
    maturityStatus: duration < definition.evidence.window.durationDays * DAY_MS
      ? "interim"
      : "matured",
    dubious: duration < definition.evidence.window.durationDays * DAY_MS,
    coverageStatus: "unknown",
    missingReason: eligibleWorkflowCount === 0 ? "no-eligible-opportunities" : "source-window-coverage-unverified",
    eligibleCount: eligibleWorkflowCount,
    populationDigest: createHash("sha256").update(JSON.stringify(eligibleWorkflows.sort())).digest("hex"),
    eligibleWorkflowCount,
    successfulRunCount,
    successfulRunAicTotal: round(successfulRunAicTotal),
    concludedRunCount,
    failedRunCount,
    cancelledRunCount,
    failedRunPercentagePointTotal: failedRunCount * 100,
    cancelledRunPercentagePointTotal: cancelledRunCount * 100,
  };
}

export function efficiencyQuery(start, end) {
  return {
    name: "optimization-repository-efficiency",
    from: "runs",
    joins: [{
      source: "workflows",
      type: "inner",
      on: [{ left: "workflowId", right: "id" }],
      fields: [{ field: "path", as: "declaredWorkflowPath" }],
    }],
    filter: {
      predicates: [
        { field: "startedAt", gte: start },
        { field: "startedAt", lt: end },
        { field: "declaredWorkflowPath", includes: ".md" },
      ],
    },
    aggregate: {
      by: ["owner", "repository", "declaredWorkflowPath"],
      values: [
        {
          field: "githubRunId",
          as: "concludedRuns",
          reducer: "count",
          filter: {
            predicates: [{ field: "conclusion", in: TERMINAL_CONCLUSIONS }],
          },
        },
        {
          field: "githubRunId",
          as: "successfulRuns",
          reducer: "count",
          filter: {
            predicates: [{ field: "conclusion", equals: "success" }],
          },
        },
        {
          field: "aicTotal",
          as: "successfulRunsWithAic",
          reducer: "count",
          filter: {
            predicates: [{ field: "conclusion", equals: "success" }],
          },
        },
        {
          field: "aicTotal",
          as: "aicPerSuccessfulRun",
          reducer: "mean",
          filter: {
            predicates: [{ field: "conclusion", equals: "success" }],
          },
        },
        {
          field: "githubRunId",
          as: "failedRuns",
          reducer: "count",
          filter: {
            predicates: [{ field: "conclusion", in: FAILURE_CONCLUSIONS }],
          },
        },
        {
          field: "githubRunId",
          as: "cancelledRuns",
          reducer: "count",
          filter: {
            predicates: [{ field: "conclusion", equals: "cancelled" }],
          },
        },
      ],
    },
  };
}

async function queryEfficiencyWindows(database, requests) {
  const start = new Date(Math.min(...requests.map((request) => Date.parse(request.windowStart)))).toISOString();
  const end = new Date(Math.max(...requests.map((request) => Date.parse(request.windowEnd)))).toISOString();
  const projection = efficiencyQuery(start, end);
  delete projection.aggregate;
  const repositories = requests.map((request) => request.repository?.split("/")[1]);
  if (repositories.every(Boolean)) {
    projection.filter.predicates.push({ field: "repository", in: [...new Set(repositories)] });
  }
  projection.select = [
    "owner", "repository", "declaredWorkflowPath", "githubRunId", "startedAt",
    "conclusion", "aicTotal",
  ].map((field) => ({ field }));
  return executeCanonicalQuery(projection, { database });
}

export async function collectBatch(requests, context = {}) {
  const hosted = Boolean(process.env.CAO_DASHBOARD_QUERY_SOCKET);
  if (context.localOnly && !context.database && !hosted) {
    fail("Local-only Optimization collection requires an existing Activity database");
  }
  if (context.history && !context.database && !hosted) {
    fail("Historical Optimization collection requires a preserved Activity database; remote acquisition is disabled");
  }
  const supported = new Set(
    definition.evidence.repositories.map((repository) => repository.toLowerCase()),
  );
  const valid = Array.isArray(requests) && requests.length > 0
    && requests.every((request) => {
      const timestampsValid = ["windowStart", "windowEnd", "observedAt"]
        .every((key) => typeof request[key] === "string" && !Number.isNaN(Date.parse(request[key])));
      if (!timestampsValid) return false;
      const duration = Date.parse(request.windowEnd) - Date.parse(request.windowStart);
      return (request.repository === undefined
          || (typeof request.repository === "string"
            && supported.has(request.repository.toLowerCase())))
        && duration > 0
        && duration <= definition.evidence.window.durationDays * DAY_MS
        && Date.parse(request.observedAt) >= Date.parse(request.windowEnd);
    });
  if (!valid) fail("invalid batch collection request");
  if (!process.env.CAO_DASHBOARD_QUERY_SOCKET && !existsSync(ACTIVITY_CLI)) {
    fail("CAO Activity CLI is unavailable");
  }

  let temporary;
  let database = context.database;
  try {
    if (!database && !hosted) {
      const localDatabase = path.join(process.cwd(), ".cao", "gh-aw-logs.sqlite");
      if (existsSync(localDatabase)) {
        database = localDatabase;
      } else {
        temporary = mkdtempSync(path.join(process.cwd(), ".aw-value-optimization-token-optimizer."));
        run(process.execPath, [ACTIVITY_CLI, "download", "--output", temporary]);
        database = path.join(temporary, "collector.sqlite");
        const runsDirectory = path.join(temporary, "gh-aw-logs-runs");
        const recordsDirectory = path.join(temporary, "gh-aw-logs-records");
        const shardsDirectory = path.join(temporary, "gh-aw-logs-shards");
        if (existsSync(runsDirectory) && existsSync(recordsDirectory)) {
          run(process.execPath, [
            ACTIVITY_CLI,
            "ingest-jsonl",
            "--database",
            database,
            "--runs-dir",
            runsDirectory,
            "--records-dir",
            recordsDirectory,
            "--retention-days",
            "all",
            "--run-retention-days",
            "all",
          ]);
        } else if (existsSync(shardsDirectory)) {
          run(process.execPath, [
            ACTIVITY_CLI,
            "ingest-jsonl",
            "--database",
            database,
            "--input-dir",
            shardsDirectory,
            "--retention-days",
            "all",
            "--run-retention-days",
            "all",
          ]);
        } else {
          fail("Downloaded CAO Activity data contains no canonical JSONL shards");
        }
      }
    }
    if (!process.env.CAO_DASHBOARD_QUERY_SOCKET && !existsSync(database)) {
      fail("CAO Activity database is unavailable");
    }

    const canonicalSource = await queryEfficiencyWindows(database, requests);
    const canonicalRows = canonicalSource.rows;
    const rowsByWindow = new Map();
    const readWindow = (start, end) => {
      const key = `${start}\0${end}`;
      if (!rowsByWindow.has(key)) {
        const query = efficiencyQuery(start, end);
        delete query.joins;
        const result = executeDashboardQuery(query, {
          runs: { rows: canonicalRows, metadata: { availability: "available" } },
        });
        if (result.metadata?.availability === "unavailable") {
          fail(String(result.metadata["query-diagnostic"] ?? "Efficiency query is unavailable"));
        }
        rowsByWindow.set(key, result.rows);
      }
      return rowsByWindow.get(key);
    };
    const digest = hosted
      ? createHash("sha256").update(String(
          canonicalSource.metadata?.["canonical-revision"]
            ?? fail("Canonical PostgreSQL query did not identify its revision"),
        )).digest("hex")
      : createHash("sha256").update(readFileSync(database)).digest("hex");

    return requests.map((request) => {
      const rows = readWindow(request.windowStart, request.windowEnd);
      return {
        evidence: {
          key: definition.evidence.key,
          repositories: [request.repository],
          opportunity: definition.evidence.opportunity,
          filters: definition.evidence.filters,
          collection: definition.evidence.collection,
          window: {
            start: request.windowStart,
            end: request.windowEnd,
            observedAt: request.observedAt,
            ...definition.evidence.window,
          },
          ...buildEvidence(rows, request),
        },
        provenance: [{
          repository: request.repository ?? definition.repository,
          kind: "cao-canonical-query-sqlite-sha256",
          ref: digest,
        }],
      };
    });
  } finally {
    if (temporary) rmSync(temporary, { recursive: true, force: true });
  }
}
