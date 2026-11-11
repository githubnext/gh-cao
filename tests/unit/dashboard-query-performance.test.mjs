import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { parse } from "yaml";
import config from "../playwright/configs/dashboard-query-performance.config.mjs";
import { DEPLOYED_REFRESH_TIMEOUT_MS } from "../e2e/dashboard-deployed-refresh-helpers.mjs";
import {
  DEFAULT_OVERVIEW_REQUEST_BUDGET_MS,
  QUERY_CHUNK_SIZE,
  snapshotDeployedActivityShards,
  deployedProxyTarget,
  mergeQueryPerformanceReports,
  overviewPhaseBreakdown,
  parseQueryPerformanceShard,
  partitionQueryDefinitions,
  queryPerformanceMarkdown,
  summarizeQueryTiming,
} from "../e2e/dashboard-query-performance-helpers.mjs";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const deployedIntegrationWorkflowPath = resolve(root, ".github/workflows/dashboard-deployed-integration.yml");

test("dashboard query benchmark runs serially with enough time for deployed data", () => {
  assert.equal(config.workers, 1);
  assert.equal(config.timeout, 1_800_000);
  assert.equal(QUERY_CHUNK_SIZE, 25);
});

test("query benchmarking waits for the full deployed refresh budget", () => {
  const source = readFileSync(resolve(root, "tests/e2e/dashboard-query-performance.spec.mjs"), "utf8");
  assert.match(source, /detail\?\.kind === "refresh"[\s\S]*?timeout: DEPLOYED_REFRESH_TIMEOUT_MS/);
  assert.equal(DEPLOYED_REFRESH_TIMEOUT_MS, 600_000);
  assert.ok(config.timeout > DEPLOYED_REFRESH_TIMEOUT_MS);
});

test("Overview request budget allows the measured deployed-data baseline", () => {
  assert.ok(1_128.9 < DEFAULT_OVERVIEW_REQUEST_BUDGET_MS);
  assert.equal(DEFAULT_OVERVIEW_REQUEST_BUDGET_MS, 1_250);
});

test("dashboard integration retains operational SQL benchmarks without real-data Pages benchmarking", () => {
  const workflow = readFileSync(deployedIntegrationWorkflowPath, "utf8");
  assert.match(workflow, /push:\n\s+branches: \[main\]\n\s+paths:/);
  assert.match(workflow, /group: dashboard-deployed-integration-\$\{\{ github\.event_name \}\}/);
  assert.match(workflow, /tests\/e2e\/dashboard-deployed-refresh-helpers\.mjs/);
  assert.match(workflow, /tests\/e2e\/dashboard-view-assessment\.mjs/);
  assert.doesNotMatch(workflow, /run: npm run test:performance:dashboard-queries|query-performance-comment:/);
  assert.match(workflow, /--name cao-activity-index --dir \.cao/);
  assert.match(workflow, /run: npm run test:performance:dashboard-query-cost/);
  assert.match(workflow, /run: npm run test:performance:dashboard-query-cost-postgres/);
});

test("dashboard integration main push trigger watches demo and operational benchmark inputs", () => {
  const workflow = parse(readFileSync(deployedIntegrationWorkflowPath, "utf8"));
  // The boolean-key lookup is a defensive fallback for YAML 1.1 core-schema behavior.
  const workflowTriggers = workflow.on ?? workflow[true];
  assert.ok(workflowTriggers, "expected workflow trigger block");
  assert.equal(workflowTriggers.pull_request, undefined);
  assert.deepEqual(workflowTriggers.push.branches, ["main"]);
  const paths = workflowTriggers.push.paths;
  assert.ok(Array.isArray(paths), "expected push.paths trigger list");
  for (const path of [
    "dashboard/site/dashboard.json",
    "dashboard/site/dashboard-fragments/**",
    "dashboard/site/dashboard-pages/**",
    "dashboard/site/src/data/**",
    "dashboard/site/src/data-*.js",
    "dashboard/site/src/dashboard-chunks.js",
    "dashboard/site/src/dashboard-app.js",
    "dashboard/site/src/debug.js",
    "dashboard/site/src/remote-data-backend.js",
    "dashboard/site/src/source-*.js",
    "package.json",
    "package-lock.json",
    "scripts/simulate-dashboard-pages.mjs",
    "tests/e2e/dashboard-deployed-refresh-helpers.mjs",
    "tests/e2e/dashboard-deployed-refresh.spec.mjs",
    "tests/integration/dashboard-deployed-data.test.mjs",
    "tests/e2e/dashboard-view-assessment.mjs",
    "tests/helpers/dashboard-query-cost.mjs",
    "tests/performance/dashboard-query-cost.test.mjs",
    "tests/playwright/configs/dashboard-deployed.config.mjs",
  ]) {
    assert.ok(paths.includes(path), `expected trigger path ${path}`);
  }
  for (const path of [
    "dashboard/site/**",
    "dashboard/site/src/notification-service.js",
    "dashboard/site/src/styles.js",
  ]) {
    assert.ok(!paths.includes(path), `unexpected broad trigger path ${path}`);
  }
  const allowedDashboardSiteWildcards = new Set([
    "dashboard/site/dashboard-fragments/**",
    "dashboard/site/dashboard-pages/**",
    "dashboard/site/src/data/**",
  ]);
  assert.deepEqual(
    paths.filter((path) =>
      path.startsWith("dashboard/site/")
      && path.endsWith("/**")
      && !allowedDashboardSiteWildcards.has(path)
    ),
    [],
    "unexpected broad dashboard site wildcard trigger paths",
  );
});

test("deployed proxy targets remain under the trusted dashboard URL", () => {
  const base = "https://githubnext.github.io/gh-cao/cao/";
  assert.equal(
    deployedProxyTarget("/gh-aw-logs-runs/shard.jsonl", base)?.href,
    `${base}gh-aw-logs-runs/shard.jsonl`,
  );
  assert.equal(
    deployedProxyTarget("/https://example.com/private", base)?.origin,
    "https://githubnext.github.io",
  );
  assert.equal(deployedProxyTarget("/../private", base), null);
});

test("deployed shard snapshots pin bytes and filter missing manifest entries", async () => {
  const base = "https://githubnext.github.io/gh-cao/cao/";
  const requests = [];
  const entries = [
    {
      name: "gh-aw-logs-runs/available.jsonl",
      sourceName: "gh-aw-logs-runs/available.jsonl",
      hash: "a".repeat(64),
    },
    {
      name: "gh-aw-logs-runs/missing.jsonl",
      sourceName: "gh-aw-logs-runs/missing.jsonl",
      hash: "b".repeat(64),
    },
    {
      name: "gh-aw-logs-records/available.jsonl",
      sourceName: "gh-aw-logs-records/available.jsonl",
      hash: "c".repeat(64),
    },
  ];

  const available = await snapshotDeployedActivityShards(entries, {
    baseUrl: base,
    async fetchImpl(url, options) {
      requests.push([url.href, options]);
      return new Response("deployed shard bytes", {
        status: url.pathname.endsWith("/available.jsonl") ? 200 : 404,
      });
    },
  });

  assert.deepEqual(available, [entries[0], entries[2]].map((entry) => ({
    ...entry, content: Buffer.from("deployed shard bytes"),
  })));
  assert.deepEqual(requests.map(([url]) => url), entries.map(({ sourceName }) => `${base}${sourceName}`));
  assert.ok(requests.every(([, options]) =>
    options.method === "GET" && options.redirect === "error" && options.signal instanceof AbortSignal));
});

test("deployed shard snapshots report upstream and network failures", async () => {
  const entry = { name: "gh-aw-logs-runs/shard.jsonl", sourceName: "gh-aw-logs-runs/shard.jsonl", hash: "a".repeat(64) };
  const options = { baseUrl: "https://githubnext.github.io/gh-cao/cao/" };
  await assert.rejects(snapshotDeployedActivityShards([entry], {
    ...options, fetchImpl: async () => new Response(null, { status: 503 }),
  }), /Unable to snapshot deployed activity shard .*: 503/);
  await assert.rejects(snapshotDeployedActivityShards([entry], {
    ...options, fetchImpl: async () => { throw new Error("network unavailable"); },
  }), /network unavailable/);
});

test("dashboard query timing summary distinguishes initial, continuation, and fill timings", () => {
  assert.deepEqual(summarizeQueryTiming({
    name: "recent-runs",
    firstChunkMs: 10.126,
    continuationChunkMs: 4.333,
    fillIterationMs: 20.555,
    rows: 75,
  }), {
    query: "recent-runs",
    rows: 75,
    chunks: 3,
    firstChunkMs: 10.13,
    continuationChunkMs: 4.33,
    fillIterationMs: 20.56,
  });
});

test("dashboard query Markdown keeps unpaginated results explicit", () => {
  const markdown = queryPerformanceMarkdown({
    populateMs: 123.45,
    chunkSize: 25,
    indexedDbCount: {
      durationMs: 3.21,
      stores: 8,
      records: 12345,
      counts: {
        campaigns: 3,
        repositories: 42,
        workflows: 100,
        runs: 12200,
      },
    },
    overview: {
      initialReadyMs: 350.25,
      requestMs: 100,
      worker: {
        databaseMs: 20,
        projectionMs: 30,
        queryMs: 70,
        totalMs: 90,
        recordsRead: 1234,
      },
      slowestSources: [{
        query: "overview-outcome-summary",
        rows: 1,
        firstChunkMs: 75,
      }],
    },
    queries: [{
      query: "repositories",
      rows: 4,
      chunks: 1,
      firstChunkMs: 2.5,
      continuationChunkMs: null,
      fillIterationMs: 2.75,
    }],
  });
  assert.match(markdown, /Population time: \*\*123\.45 ms\*\*/);
  assert.match(markdown, /### IndexedDB count\(\)/);
  assert.match(markdown, /Counted \*\*12,345 records\*\* across \*\*8 stores\*\* in \*\*3\.21 ms\*\*\./);
  assert.match(markdown, /Initial Overview ready: \*\*350\.25 ms\*\*/);
  assert.match(markdown, /Settled deployed-data query: \*\*100\.00 ms\*\* \(1,234 records read\)/);
  assert.match(markdown, /\| Declarative queries \| 40\.00 \| 40\.00% \|/);
  assert.match(markdown, /\| `overview-outcome-summary` \| 75\.00 \| 1 \|/);
  assert.match(markdown, /\n\| `repositories` \| 4 \| 1 \| 2\.50 \| — \| 2\.75 \|\n/);
});

test("overview phase breakdown attributes worker and messaging time", () => {
  assert.deepEqual(overviewPhaseBreakdown({
    requestMs: 100,
    worker: {
      databaseMs: 20,
      projectionMs: 30,
      queryMs: 70,
      totalMs: 90,
    },
  }), [
    { phase: "Declarative queries", durationMs: 40, percent: 40 },
    { phase: "Canonical projection", durationMs: 30, percent: 30 },
    { phase: "IndexedDB reads", durationMs: 20, percent: 20 },
    { phase: "Worker messaging", durationMs: 10, percent: 10 },
  ]);
});

test("query performance shard descriptor parses valid N/M pairs and falls back otherwise", () => {
  assert.deepEqual(parseQueryPerformanceShard("2/3"), { index: 2, total: 3 });
  assert.deepEqual(parseQueryPerformanceShard(undefined), { index: 1, total: 1 });
  assert.deepEqual(parseQueryPerformanceShard("bogus"), { index: 1, total: 1 });
  assert.deepEqual(parseQueryPerformanceShard("0/3"), { index: 1, total: 1 });
  assert.deepEqual(parseQueryPerformanceShard("4/3"), { index: 1, total: 1 });
});

test("query definitions partition into near-even, deterministic, gapless shards", () => {
  const queries = Array.from({ length: 8 }, (_, index) => ({ name: `q${index}` }));
  const shards = [1, 2, 3].map((index) => partitionQueryDefinitions(queries, index, 3));
  assert.deepEqual(shards.map((shard) => shard.length), [3, 3, 2]);
  assert.deepEqual(shards.flat(), queries);
  assert.deepEqual(partitionQueryDefinitions(queries, 1, 1), queries);
});

test("merging a single shard report returns it unchanged", () => {
  const report = { queries: [{ query: "a" }] };
  assert.equal(mergeQueryPerformanceReports([report]), report);
});

test("merging shard reports concatenates queries in shard order and recomputes the slowest overview sources", () => {
  const merged = mergeQueryPerformanceReports([
    {
      shard: { index: 1, total: 2 },
      overview: {
        returnedRows: { "overview-a": 1, "overview-b": 2 },
        slowestSources: [],
      },
      queries: [
        { query: "overview-a", firstChunkMs: 5 },
        { query: "other-1", firstChunkMs: 50 },
      ],
    },
    {
      shard: { index: 2, total: 2 },
      overview: null,
      queries: [
        { query: "overview-b", firstChunkMs: 40 },
        { query: "other-2", firstChunkMs: 1 },
      ],
    },
  ]);
  assert.deepEqual(merged.queries.map(({ query }) => query), [
    "overview-a",
    "other-1",
    "overview-b",
    "other-2",
  ]);
  assert.deepEqual(merged.overview.slowestSources.map(({ query }) => query), [
    "overview-b",
    "overview-a",
  ]);
});
