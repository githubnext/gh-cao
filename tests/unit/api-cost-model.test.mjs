import assert from "node:assert/strict";
import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import {
  dailyFileDietApiCostScenario,
  formatApiCostMarkdown,
  simulateApiCost,
} from "../../activity/api-cost-model.mjs";
import { runCli } from "../../activity/cli/run.mjs";

test("Daily File Diet retained scenario exposes modeled and guaranteed capacity", async () => {
  const scenario = await dailyFileDietApiCostScenario({ repositories: 100 });
  const report = simulateApiCost(scenario);

  assert.equal(scenario.detail.trackedIssues, 95);
  assert.equal(scenario.detail.corePerWorkflow, 99);
  assert.equal(scenario.detail.searchRequestsPerWorkflow, 6);
  assert.equal(scenario.detail.graphqlRequestsPerWorkflow, 10);
  assert.equal(scenario.detail.totalRequestsPerWorkflow, 115);
  assert.equal(report.requested.supported, false);
  assert.deepEqual(report.requested.violations, [
    "core:peak",
    "reviewed-collector-grant:modeled-admission",
    "runner:peak",
  ]);
  assert.equal(report.capacity.modeledMaximumRepositories, 6);
  assert.equal(report.capacity.guaranteedMaximumRepositories, 4);
  assert.deepEqual(report.capacity.bottlenecks, ["runner:peak"]);
});

test("Daily File Diet hard bound cannot fit one reviewed collector grant", async () => {
  const scenario = await dailyFileDietApiCostScenario({ scenario: "hard-bound" });
  const report = simulateApiCost(scenario);

  assert.equal(scenario.detail.totalRequestsPerWorkflow, 4084);
  assert.equal(report.requested.pools.search.peakDemandPerWindow, 6);
  assert.equal(report.requested.admissions[0].grantExceeded, true);
  assert.equal(report.capacity.modeledMaximumRepositories, 0);
  assert.equal(report.capacity.guaranteedMaximumRepositories, 0);
  assert.ok(report.requested.violations.includes("reviewed-collector-grant:collector-grant"));
});

test("custom scenarios identify peak quota bottlenecks and collection spreading", () => {
  const base = {
    schemaVersion: 1,
    name: "Repository metadata",
    repositories: 100,
    workflowsPerRepository: 1,
    horizonHours: 24,
    collectionWindowHours: 1,
    quotas: {
      core: { limit: 100, reserve: 20, windowHours: 1 },
    },
    operations: [{
      id: "repository-read",
      pool: "core",
      cadenceHours: 24,
      scale: { perRepository: 10 },
    }],
  };
  const oneHour = simulateApiCost(base);
  const twoHours = simulateApiCost({ ...base, collectionWindowHours: 2 });

  assert.equal(oneHour.capacity.modeledMaximumRepositories, 8);
  assert.deepEqual(oneHour.capacity.bottlenecks, ["core:peak"]);
  assert.equal(twoHours.capacity.modeledMaximumRepositories, 16);
});

test("cache hit rates reduce only the modeled remote demand", () => {
  const report = simulateApiCost({
    schemaVersion: 1,
    name: "Cached collection",
    repositories: 16,
    collectionWindowHours: 1,
    quotas: { core: { limit: 100, reserve: 20, windowHours: 1 } },
    operations: [{
      id: "conditional-read",
      pool: "core",
      cadenceHours: 24,
      cacheHitRate: 0.5,
      scale: { perRepository: 10 },
    }],
  });

  assert.equal(report.requested.supported, true);
  assert.equal(report.requested.pools.core.peakDemandPerWindow, 80);
  assert.equal(report.capacity.modeledMaximumRepositories, 16);
});

test("cao api-cost accepts reusable JSON scenarios and markdown output", async () => {
  const directory = await mkdtemp(path.join(tmpdir(), "cao-api-cost-"));
  const filename = path.join(directory, "scenario.json");
  await writeFile(filename, JSON.stringify({
    schemaVersion: 1,
    name: "Workflow registry",
    repositories: 5,
    quotas: { core: { limit: 100, reserve: 20, windowHours: 1 } },
    operations: [{
      id: "workflow-pages",
      pool: "core",
      cadenceHours: 24,
      scale: { perRepository: 2 },
    }],
  }));

  const report = await runCli(["api-cost", "--input", filename, "--repositories", "40"]);
  assert.equal(report.requested.pools.core.peakDemandPerWindow, 80);
  assert.equal(report.capacity.modeledMaximumRepositories, 40);

  const markdown = await runCli(["api-cost", "--input", filename, "--format", "markdown"]);
  assert.equal(markdown, formatApiCostMarkdown(simulateApiCost(JSON.parse(await readFile(filename)))));
  assert.match(markdown, /Modeled maximum: \*\*40 repositories\*\*/);
});
