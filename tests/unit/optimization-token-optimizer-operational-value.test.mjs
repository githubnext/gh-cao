import assert from "node:assert/strict";
import test from "node:test";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { createServer } from "node:net";
import { once } from "node:events";
import { tmpdir } from "node:os";
import { createDatabase } from "../../activity/cli/ingestion.mjs";
import { upsertCanonicalBatch } from "../../dashboard/site/src/data/storage/indexeddb.js";
import {
  buildEvidence,
  collectBatch,
  definition,
  efficiencyQuery,
  scoreMetric,
} from "../../optimization/operational-value/optimization-token-optimizer.mjs";

test("Optimization backfill materializes real canonical SQLite history and is idempotent", async () => {
  const root = path.resolve(import.meta.dirname, "../..");
  mkdirSync(path.join(root, ".tmp"), { recursive: true });
  const temporary = mkdtempSync(path.join(root, ".tmp", "optimization-sqlite-history-"));
  const database = path.join(temporary, "archive.sqlite");
  const output = path.join(temporary, "values.jsonl");
  try {
    const indexedDB = await createDatabase(database);
    await upsertCanonicalBatch(indexedDB, {
      repositories: [{id:"repo", owner:"github", name:"gh-aw"}],
      workflows: [{id:"workflow", repositoryId:"repo", owner:"github",
        repository:"gh-aw", path:".github/workflows/example.md"}],
      runs: Array.from({length:3}, (_, index) => ({
        id:`run-${index}`, repositoryId:"repo", workflowId:"workflow",
        owner:"github", repository:"gh-aw", githubRunId:index + 1,
        startedAt:`2026-09-12T0${index}:00:00Z`, conclusion:"success", aicTotal:10,
      }))
    });
    const args = [
      path.join(root, "activity/cao.mjs"), "operational-value", "--root", root,
      "--database", database, "--output", output, "--campaign", "optimization",
      "--history-campaign", "optimization", "--repository", "github/gh-aw",
      "--retention-days", "365", "--timestamp", "2026-09-18T12:00:00Z"
    ];
    const first = JSON.parse(execFileSync(process.execPath, args, {encoding:"utf8"}));
    assert.deepEqual(first.warnings, []);
    assert.equal(first.historyValues, 18);
    const records = readFileSync(output, "utf8").trim().split("\n").map(JSON.parse);
    assert.equal(records.length, 18);
    const primary = records.map((record) => record.operational_value)
      .filter((value) => value.metric_role === "primary");
    assert.equal(primary.length, 6);
    assert.equal(new Set(primary.map((value) => value.window_end)).size, primary.length);
    assert.ok(primary.some((value) => value.timestamp === "2026-09-18T12:00:00.000Z"
      && value.window_end === "2026-09-17T23:30:36.000Z"));
    assert.ok(primary.every((value) => value.value === 10 && value.metric_unit === "aic-per-run"));
    assert.equal(primary[1].timestamp, "2026-09-12T23:30:36.000Z");
    const repeated = JSON.parse(execFileSync(process.execPath, args, {encoding:"utf8"}));
    assert.deepEqual(repeated.warnings, []);
    assert.equal(repeated.historyValues, 0);
    assert.deepEqual(readFileSync(output, "utf8").trim().split("\n").map(JSON.parse), records);
  } finally {
    rmSync(temporary, {recursive:true, force:true});
  }
});

test("Optimization history uses one local canonical projection for all windows and repositories", async () => {
  const root = path.resolve(import.meta.dirname, "../..");
  mkdirSync(path.join(root, ".tmp"), { recursive: true });
  const temporary = mkdtempSync(path.join(root, ".tmp", "optimization-batch-"));
  const cli = path.join(temporary, "query.mjs");
  const calls = path.join(temporary, "calls.jsonl");
  const database = path.join(temporary, "archive.sqlite");
  writeFileSync(database, "preserved fixture");
  const runs = Array.from({ length: 3 }, (_, index) => ({
    owner: "github", repository: "gh-aw",
    declaredWorkflowPath: ".github/workflows/example.md",
    githubRunId: index + 1, conclusion: "success", aicTotal: 10,
    startedAt: `2026-09-10T0${index}:00:00Z`
  }));
  writeFileSync(cli, `
import { appendFileSync, readFileSync } from "node:fs";
appendFileSync(${JSON.stringify(calls)}, readFileSync(0,"utf8"));
console.log(${JSON.stringify(JSON.stringify(runs))});
`);
  const previous = process.env.CAO_ACTIVITY_CLI;
  process.env.CAO_ACTIVITY_CLI = cli;
  try {
    const collections = await collectBatch([
      request,
      {...request, repository: "github/gh-aw-actions"},
      {...request, windowStart: "2026-09-09T00:00:00Z", windowEnd: "2026-09-16T00:00:00Z",
        observedAt: "2026-09-16T00:00:00Z"},
      {...request, windowStart: "2026-08-01T00:00:00Z", windowEnd: "2026-08-08T00:00:00Z"}
    ], { database, history: true });
    assert.equal(collections.length, 4);
    assert.equal(scoreMetric("aic-per-successful-run", collections[0].evidence), 10);
    assert.equal(scoreMetric("aic-per-successful-run", collections[1].evidence), null);
    assert.equal(scoreMetric("aic-per-successful-run", collections[2].evidence), 10);
    assert.equal(scoreMetric("aic-per-successful-run", collections[3].evidence), null);
    const queries = readFileSync(calls, "utf8").trim().split("\n").map(JSON.parse);
    assert.equal(queries.length, 1);
    assert.equal(queries[0].aggregate, undefined);
    assert.deepEqual(queries[0].filter.predicates.slice(0, 2), [
      {field:"startedAt", gte:"2026-08-01T00:00:00.000Z"},
      {field:"startedAt", lt:"2026-09-16T00:00:00.000Z"}
    ]);
    await assert.rejects(collectBatch([request], {history: true}), /remote acquisition is disabled/);
  } finally {
    if (previous === undefined) delete process.env.CAO_ACTIVITY_CLI;
    else process.env.CAO_ACTIVITY_CLI = previous;
    rmSync(temporary, {recursive:true, force:true});
  }
});

test("Optimization metrics are identical through the hosted canonical query socket", async () => {
  const temporary = mkdtempSync(path.join(tmpdir(), "cao-hosted-value-"));
  const socketPath = path.join(temporary, "query.sock");
  const runs = Array.from({ length: 3 }, (_, index) => ({
    owner: "github", repository: "gh-aw",
    declaredWorkflowPath: ".github/workflows/example.md",
    githubRunId: index + 1, conclusion: index === 2 ? "failure" : "success",
    aicTotal: index < 2 ? 10 : null,
    startedAt: `2026-09-10T0${index}:00:00Z`
  }));
  let received;
  const server = createServer(connection => {
    let requestBody = "";
    connection.setEncoding("utf8");
    connection.on("data", chunk => requestBody += chunk);
    connection.on("end", () => {
      received = JSON.parse(requestBody);
      const name = received.requested[0];
      connection.end(JSON.stringify({schemaVersion: 1, sources: {
        [name]: {source: name, rows: runs, metadata: {
          availability: "available", "canonical-revision": 42
        }}
      }}));
    });
  });
  server.listen(socketPath);
  await once(server, "listening");
  const previous = process.env.CAO_DASHBOARD_QUERY_SOCKET;
  process.env.CAO_DASHBOARD_QUERY_SOCKET = socketPath;
  try {
    const [collection] = await collectBatch([request], {history: true});
    assert.equal(scoreMetric("aic-per-successful-run", collection.evidence), 10);
    assert.equal(scoreMetric("failure-rate-percent", collection.evidence), 33.333333333);
    assert.equal(scoreMetric("cancellation-rate-percent", collection.evidence), 0);
    assert.equal(received.schemaVersion, 1);
    assert.equal(received.queries.length, 1);
    assert.deepEqual(received.requested, ["optimization-repository-efficiency"]);
  } finally {
    if (previous === undefined) delete process.env.CAO_DASHBOARD_QUERY_SOCKET;
    else process.env.CAO_DASHBOARD_QUERY_SOCKET = previous;
    server.close();
    await once(server, "close");
    rmSync(temporary, {recursive:true, force:true});
  }
});

const request = {
  repository: "github/gh-aw",
  windowStart: "2026-09-08T00:00:00Z",
  windowEnd: "2026-09-15T00:00:00Z",
  observedAt: "2026-09-15T00:00:00Z",
};

function row(overrides = {}) {
  return {
    owner: "github",
    repository: "gh-aw",
    declaredWorkflowPath: ".github/workflows/example-workflow.md",
    concludedRuns: 10,
    successfulRuns: 8,
    successfulRunsWithAic: 8,
    aicPerSuccessfulRun: 10,
    failedRuns: 1,
    cancelledRuns: 1,
    ...overrides,
  };
}

test("token optimizer measures native repository efficiency and reliability", () => {
  const evidence = buildEvidence([
    row({ aicPerSuccessfulRun: 8 }),
    row({
      declaredWorkflowPath: ".github/workflows/ci-doctor.md",
      concludedRuns: 5,
      successfulRuns: 2,
      successfulRunsWithAic: 2,
      aicPerSuccessfulRun: 20,
      failedRuns: 2,
      cancelledRuns: 1,
    }),
  ], request);

  assert.deepEqual(evidence, {
    maturityStatus: "matured",
    dubious: false,
    coverageStatus: "unknown",
    missingReason: "source-window-coverage-unverified",
    eligibleCount: 2,
    populationDigest: "42eef0d1c60c7bd18a2dfd7d30c1537fa7e344b7c5307b77f44985062fe118cf",
    eligibleWorkflowCount: 2,
    successfulRunCount: 10,
    successfulRunAicTotal: 104,
    concludedRunCount: 15,
    failedRunCount: 3,
    cancelledRunCount: 2,
    failedRunPercentagePointTotal: 300,
    cancelledRunPercentagePointTotal: 200,
  });
  assert.equal(scoreMetric("aic-per-successful-run", evidence), 10.4);
  assert.equal(scoreMetric("failure-rate-percent", evidence), 20);
  assert.equal(scoreMetric("cancellation-rate-percent", evidence), 13.333333333);
});

test("token optimizer declares pre-existing native measures as baseline comparable", () => {
  assert.equal(definition.evaluation.mode, "baseline-comparable");
  assert.equal(definition.adoption.baselineCommit, "ad7be08097b5b69cc15a8bf17dbc5e0ad12865ab");
  assert.equal(definition.adoption.baselineAt, "2026-09-15T23:02:17Z");

  const baselineEvidence = buildEvidence([row({ aicPerSuccessfulRun: 43.530901429 })], request);
  assert.equal(scoreMetric("aic-per-successful-run", baselineEvidence), 43.530901429);
  assert.equal(scoreMetric("failure-rate-percent", baselineEvidence), 10);
  assert.equal(scoreMetric("cancellation-rate-percent", baselineEvidence), 10);
});

test("token optimizer keeps insufficient or incomplete target evidence missing", () => {
  const evidence = buildEvidence([
    row({ concludedRuns: 2 }),
    row({
      declaredWorkflowPath: ".github/workflows/incomplete-aic.md",
      successfulRunsWithAic: 7,
    }),
  ], request);

  assert.equal(evidence.eligibleWorkflowCount, 0);
  for (const metric of definition.metrics) assert.equal(scoreMetric(metric.id, evidence), null);
});

test("token optimizer excludes its own campaign and other repositories", () => {
  const evidence = buildEvidence([
    row({ declaredWorkflowPath: ".github/workflows/optimization-token-auditor.md" }),
    row({ repository: "gh-aw-mcpg" }),
  ], request);

  assert.equal(evidence.eligibleWorkflowCount, 0);
});

test("token optimizer marks a bounded partial window interim", () => {
  const evidence = buildEvidence([row({ aicPerSuccessfulRun: 8 })], {
    ...request,
    windowStart: "2026-09-04T00:00:00Z",
    windowEnd: "2026-09-08T00:00:00Z",
    observedAt: "2026-09-08T00:00:00Z",
  });

  assert.equal(evidence.maturityStatus, "interim");
  assert.equal(evidence.dubious, true);
  assert.equal(scoreMetric("aic-per-successful-run", evidence), 8);
});

test("token optimizer declares additive native rollup evidence", () => {
  assert.deepEqual(
    definition.metrics.map(({ id, unit, direction, rollup }) => ({
      id, unit, direction, rollup,
    })),
    [{
      id: "aic-per-successful-run",
      unit: "aic-per-run",
      direction: "decrease",
      rollup: {
        numeratorField: "successfulRunAicTotal",
        denominatorField: "successfulRunCount",
      },
    }, {
      id: "failure-rate-percent",
      unit: "percent",
      direction: "decrease",
      rollup: {
        numeratorField: "failedRunPercentagePointTotal",
        denominatorField: "concludedRunCount",
      },
    }, {
      id: "cancellation-rate-percent",
      unit: "percent",
      direction: "decrease",
      rollup: {
        numeratorField: "cancelledRunPercentagePointTotal",
        denominatorField: "concludedRunCount",
      },
    }],
  );
});

test("token optimizer bounds canonical queries to each rolling observation window", () => {
  const query = efficiencyQuery(request.windowStart, request.windowEnd);
  assert.deepEqual(query.filter.predicates.slice(0, 2), [
    { field: "startedAt", gte: request.windowStart },
    { field: "startedAt", lt: request.windowEnd },
  ]);
  assert.equal(Object.hasOwn(query, "time"), false);
});

test("token optimizer validation examples preserve decrease-goal ordering", () => {
  for (const metric of definition.metrics) {
    assert.ok(
      scoreMetric(metric.id, definition.validationExamples.targetAttained)
      < scoreMetric(metric.id, definition.validationExamples.targetMissed),
    );
    assert.equal(scoreMetric(metric.id, definition.validationExamples.missing), null);
    assert.equal(scoreMetric(metric.id, definition.validationExamples.malformed), null);
  }
});
