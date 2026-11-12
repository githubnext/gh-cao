import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { parse } from "yaml";
import {
  estimateRetainedBytes,
  measureDashboardQueryCost,
  QUERY_COST_PAGE_LIMIT,
  selectCostlyQueries,
} from "../helpers/dashboard-query-cost.mjs";
import { authoritativeDashboard as dashboardDocument } from "../helpers/authoritative-dashboard.mjs";
import { postgresQueryCostMarkdown } from "../helpers/dashboard-query-cost-report.mjs";

const CANDIDATE_LIMIT = 10;

test("static query cost evaluator picks the highest ranked queries to investigate", () => {
  const { candidates, analysis } = selectCostlyQueries(dashboardDocument, CANDIDATE_LIMIT);
  assert.equal(candidates.length, Math.min(CANDIDATE_LIMIT, analysis.ranking.length));
  assert.deepEqual(
    candidates.map(({ name }) => name),
    analysis.ranking.slice(0, CANDIDATE_LIMIT).map(({ name }) => name),
  );
  assert.deepEqual(
    candidates.map(({ rank }) => rank),
    candidates.map((_, index) => index + 1),
  );
  for (const candidate of candidates) {
    assert.ok(candidate["static-total-row-read-units"] >= candidate["static-direct-row-read-units"]);
  }
});

test("candidate limit must be a positive integer", () => {
  assert.throws(() => selectCostlyQueries(dashboardDocument, 0), TypeError);
  assert.throws(() => selectCostlyQueries(dashboardDocument, 1.5), TypeError);
});

test("retained byte estimate counts shared rows once", () => {
  const row = { name: "run", value: 12 };
  const single = estimateRetainedBytes([row]);
  assert.ok(single > 0);
  assert.equal(estimateRetainedBytes([row, row]), single + 8);
  assert.ok(estimateRetainedBytes([row, { ...row }]) > estimateRetainedBytes([row, row]));
});

test("query cost measures a bounded page without truncating input computation", () => {
  const rows = Array.from({ length: QUERY_COST_PAGE_LIMIT * 3 }, (_, id) => ({ id }));
  const definition = {
    name: "paged-events",
    from: "events",
    "order-by": [{ field: "id", direction: "desc" }],
  };
  const measurement = measureDashboardQueryCost(definition, {
    events: { rows, metadata: { availability: "available" } },
  });
  assert.equal(measurement.status, "available", measurement.failure);
  assert.equal(measurement.rows, QUERY_COST_PAGE_LIMIT);
  assert.equal(measurement["total-rows"], rows.length);
  assert.equal(measurement["input-row-total"], rows.length);
  assert.ok(measurement.operations >= rows.length);
  assert.equal(
    measurement["result-bytes"],
    estimateRetainedBytes(rows.toReversed().slice(0, QUERY_COST_PAGE_LIMIT)),
  );
  assert.equal(rows.length, QUERY_COST_PAGE_LIMIT * 3);
});

test("operational artifacts feed SQL benchmarks separately from synthetic UI checks", async () => {
  const workflow = await readFile(".github/workflows/dashboard-deployed-integration.yml", "utf8");
  const { jobs } = parse(workflow);
  assert.match(workflow, /tests\/performance\/dashboard-query-cost-postgres\.test\.mjs/);
  assert.match(workflow, /tests\/helpers\/dashboard-query-cost\.mjs/);
  assert.match(jobs.integration.name, /synthetic/i);
  assert.doesNotMatch(JSON.stringify(jobs.integration),
    /test:performance:dashboard-quer|dashboard:data:download|cao-activity-index|DASHBOARD_DATA_URL/);
  const benchmarkCommands = jobs["query-cost"].steps.map((step) => step.run ?? "").join("\n");
  assert.match(benchmarkCommands, /go run \.\/cmd\/gh-cao download/);
  assert.doesNotMatch(benchmarkCommands, /gh run download|github\.io/);
  assert.match(benchmarkCommands, /npm run test:performance:dashboard-query-cost-postgres\n/);
  assert.match(workflow, /image: postgres:16-alpine/);
  assert.equal(jobs["query-cost"].env.CAO_POSTGRES_RUN_RETENTION_DAYS, "90");
  assert.equal(jobs["query-cost"].env.CAO_POSTGRES_LINKED_RETENTION_DAYS, "7");
  assert.match(workflow, /go -C server build -o \.\.\/\.tmp\/cao-dashboard/);
  assert.match(workflow, /name: dashboard-query-cost\n/);
  const manifest = JSON.parse(await readFile("package.json", "utf8"));
  assert.equal(
    manifest.scripts["test:performance:dashboard-query-cost"],
    "node --test tests/performance/dashboard-query-cost-postgres.test.mjs",
  );
  assert.equal(
    manifest.scripts["test:performance:dashboard-query-cost-postgres"],
    "node --test tests/performance/dashboard-query-cost-postgres.test.mjs",
  );
});

test("Postgres query cost report includes Go measurements", () => {
  const report = {
    records: 42,
    "page-limit": 500,
    measurements: [{
      query: "test-query", rows: 2, "total-rows": 900, "duration-ms": 1.25,
      metrics: { operations: 12, peakWorkingRows: 7, retainedBytes: 128 },
    }],
  };
  const markdown = postgresQueryCostMarkdown(report, [{ name: "test-query", rank: 1 }]);
  assert.match(markdown, /Postgres \+ Go/);
  assert.match(markdown, /first \*\*500 result rows\*\*/);
  assert.match(markdown, /\| `test-query` \| 1 \| 2 \| 900 \| 12 \| 1\.25 \| 7 \| 128 \|/);
});

test("deployed integration reports the query cost report in a pull request comment", async () => {
  const workflow = parse(await readFile(".github/workflows/dashboard-deployed-integration.yml", "utf8"));
  const job = workflow.jobs["query-cost-comment"];
  assert.ok(job, "expected a query-cost-comment job");
  assert.deepEqual(job.needs, "query-cost");
  assert.match(job.if, /github\.event_name == 'pull_request'/);
  assert.match(job.if, /github\.event\.pull_request\.head\.repo\.full_name == github\.repository/);
  assert.deepEqual(job.permissions, { actions: "read", "pull-requests": "write" });
  const download = job.steps.find((step) => step.uses?.startsWith("actions/download-artifact@"));
  assert.equal(download.with.name, "dashboard-query-cost");
  const comment = job.steps.find((step) => step.uses?.startsWith("actions/github-script@"));
  assert.match(comment.with.script, /<!-- dashboard-query-cost-results -->/);
  assert.match(comment.with.script, /summary\.md/);
  assert.match(comment.with.script, /<details><summary><b>Postgres query cost report<\/b><\/summary>/);
  assert.match(comment.with.script, /postgres,[\s\S]*?<\/details>/);
  assert.doesNotMatch(comment.with.script, /sqlite/i);
  assert.match(comment.with.script, /postgres=success/);
  assert.match(comment.with.script, /issues\.createComment/);
  assert.match(comment.with.script, /issues\.updateComment/);
});
