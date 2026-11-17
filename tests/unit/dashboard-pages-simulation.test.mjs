import assert from "node:assert/strict";
import { createHash, randomUUID } from "node:crypto";
import { mkdir, readFile, readdir, rm, symlink, writeFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import "fake-indexeddb/auto";
import { IDBFactory } from "fake-indexeddb";
import {
  assertSafePagesOutput, parsePagesArguments, prepareDashboardPages,
} from "../../scripts/simulate-dashboard-pages.mjs";
import { ingestCachedGhAwJsonl, ingestDashboardSources, ingestDashboardSourceBatches } from "../../dashboard/site/src/data/ingest/coordinator.js";
import { enterpriseDemo, enterpriseRepositorySources, enterpriseSample } from "../../scripts/dashboard-demo-data.mjs";
import { demoSha256 } from "../../dashboard/site/src/data/demo/hash.mjs";
import { loadDashboardSourceSync } from "../../dashboard/report/bundle-dashboards.mjs";
import { queryDatabaseSources } from "../../dashboard/site/src/data/queries/database.js";
import { compileDashboardViewPayloadQueries } from "../../dashboard/site/src/data/queries/view-payload-compiler.js";
import { processDataRequest } from "../../dashboard/site/src/data-worker.js";
import { readCollection } from "../../dashboard/site/src/data/storage/indexeddb.js";
import { isExpiredCanonicalRecord } from "../../dashboard/site/src/data/storage/retention.js";

const root = path.resolve(".");

test("browser sample SHA-256 matches standard UTF-8 hashing across block boundaries", () => {
  for (const input of ["", "abc", "👩🏽‍💻 synthetic enterprise", "a".repeat(55), "b".repeat(64), "c".repeat(1_000)]) {
    assert.equal(demoSha256(input), createHash("sha256").update(input).digest("hex"));
  }
});

test("bounded raw repository inventory uses production normalization and preserves earlier batches", async () => {
  const indexedDB = new IDBFactory();
  const now = Date.parse("2040-06-15T12:00:00Z");
  async function* batches() {
    yield enterpriseRepositorySources(0, 100, now);
    yield enterpriseRepositorySources(100, 100, now);
  }
  const result = await ingestDashboardSourceBatches(indexedDB, batches());
  assert.equal(result.committedRecords, 200);
  assert.equal((await readCollection(indexedDB, "repositories")).length, 200);
  const controller = new AbortController();
  async function* aborted() {
    yield enterpriseRepositorySources(200, 100, now);
    controller.abort();
    yield enterpriseRepositorySources(300, 100, now);
  }
  await assert.rejects(ingestDashboardSourceBatches(indexedDB, aborted(), { signal: controller.signal }), /abort/i);
  assert.equal((await readCollection(indexedDB, "repositories")).length, 300);
  await ingestDashboardSourceBatches(indexedDB, batches());
  assert.equal((await readCollection(indexedDB, "repositories")).length, 300, "replay upserts rather than duplicating");
  assert.deepEqual([enterpriseDemo.repositories, enterpriseDemo.campaigns, enterpriseDemo.installedWorkflows], [1_000_000, 3, 200]);
});

test("enterprise distributions are reproducible, seasonal and uneven rather than one run per workflow per day", () => {
  for (const date of ["2040-06-15T12:00:00Z", "2040-06-17T23:59:00Z", "2040-06-20T00:00:00Z"]) {
    const now = Date.parse(date);
    const sample = enterpriseSample(now);
    assert.deepEqual(enterpriseSample(now), sample);
    const runs = sample.records.filter(record => record.kind === "run").map(record => record.run);
    assert.equal(runs.length, sample.runs);
    assert.ok(runs.length > 300 && runs.length < 1500, "bounded but nontrivial population");
    const repositories = new Map();
    const days = new Map();
    for (const run of runs) {
      repositories.set(run.repository, (repositories.get(run.repository) ?? 0) + 1);
      const date = run.started_at.slice(0, 10);
      days.set(date, (days.get(date) ?? 0) + 1);
    }
    assert.ok(days.size >= 20, "sample spans three weeks, including both chart weeks");
    const today = Math.floor(now / 86_400_000) * 86_400_000;
    for (let offset = 1; offset <= 14; offset++) {
      assert.ok(days.has(new Date(today - offset * 86_400_000).toISOString().slice(0, 10)),
        "both preceding calendar weeks contain runs on every day");
    }
    assert.equal(repositories.size, 12);
    assert.ok(Math.max(...repositories.values()) > Math.min(...repositories.values()) * 3);
    const dailyMean = weekend => {
      const counts = [...days].filter(([date]) => date !== new Date(now).toISOString().slice(0, 10)
        && [0, 6].includes(new Date(date).getUTCDay()) === weekend).map(([, count]) => count);
      return counts.reduce((sum, count) => sum + count, 0) / counts.length;
    };
    assert.ok(dailyMean(true) < dailyMean(false) * 0.6, "weekends are quieter");
    assert.equal(new Set(runs.map(run => run.run_id)).size, runs.length);
  }
});

test("enterprise workloads have long tails, correlated costs and independently assigned experiment variants", () => {
  const sample = enterpriseSample(Date.parse("2040-06-15T12:00:00Z"));
  const runs = sample.records.filter(record => record.kind === "run").map(record => record.run);
  const percentile = (values, fraction) => values.toSorted((a, b) => a - b)[Math.floor(values.length * fraction)];
  const duration = run => parseFloat(run.duration);
  const cost = run => run.token_usage_summary.total_aic;
  for (const measure of [duration, cost, run => run.token_usage_summary.total_input_tokens]) {
    const values = runs.map(measure);
    assert.ok(percentile(values, 0.95) > percentile(values, 0.5) * 3, "heavy right tail");
  }
  const cheap = runs.filter(run => cost(run) <= percentile(runs.map(cost), 0.25));
  const expensive = runs.filter(run => cost(run) >= percentile(runs.map(cost), 0.75));
  assert.ok(percentile(expensive.map(duration), 0.5) > percentile(cheap.map(duration), 0.5) * 2);
  assert.equal(new Set(runs.map(run => run.model)).size, 3);
  const modelTokens = model => runs.filter(run => run.model === model).map(run => run.token_usage_summary.total_input_tokens);
  assert.ok(percentile(modelTokens("gpt-5.3-codex"), 0.5) > percentile(modelTokens("claude-haiku-5.5"), 0.5) * 1.5,
    "complex workloads favor the larger model rather than round-robin selection");
  const variants = new Map();
  for (const run of runs) {
    const date = run.started_at.slice(0, 10);
    if (!variants.has(date)) variants.set(date, new Set());
    variants.get(date).add(run.experiments.assignments["review-strategy"]);
  }
  assert.ok([...variants.values()].every(values => values.size === 2), "variant is not determined by day");
});

test("workflow output rates, batch sizes, issue/PR mix and tool activity are heterogeneous", () => {
  for (const date of ["2040-06-15T12:00:00Z", "2040-06-17T23:59:00Z", "2040-06-20T00:00:00Z"]) {
    const runs = enterpriseSample(Date.parse(date)).records.filter(record => record.kind === "run").map(record => record.run);
    const deployments = Object.values(Object.groupBy(runs, run => `${run.repository}:${run.workflow_path}`));
    const outputCounts = deployments.map(rows => rows.flatMap(run => run.safe_outputs).length).toSorted((a, b) => a - b);
    assert.ok(outputCounts.filter(count => count === 0).length >= 3, "some workflows are quiet");
    assert.ok(outputCounts.at(-1) > outputCounts[Math.floor(outputCounts.length / 2)] * 5, "a few workflows produce most outputs");
    const outputRates = deployments.filter(rows => rows.filter(run => run.conclusion === "success").length >= 10)
      .map(rows => rows.filter(run => run.safe_outputs.length > 0).length / rows.filter(run => run.conclusion === "success").length);
    assert.ok(Math.max(...outputRates) - Math.min(...outputRates) > 0.6, "output probability is not shared by all workflows");
    assert.ok(runs.some(run => run.safe_outputs.length >= 4), "orchestrators can create batches");
    assert.ok(runs.some(run => run.conclusion === "success" && run.safe_outputs.length === 0), "successful no-ops are normal");
    const mix = name => {
      const outputs = runs.filter(run => run.workflow_name === name).flatMap(run => run.safe_outputs);
      return outputs.filter(output => output.type === "create_pull_request").length / outputs.length;
    };
    assert.ok(mix("Dependency review") < 0.25 && mix("Security review") < 0.25, "review workflows favor findings");
    assert.ok(mix("Repository assistant") > 0.75 && mix("Documentation maintenance") > 0.75, "repair workflows favor PRs");
    assert.ok(new Set(runs.map(run => run.mcp_tool_usage.tool_calls.length)).size > 10, "tool call counts vary");
    assert.deepEqual(new Set(runs.map(run => run.skill_activations.length)), new Set([0, 1, 2]));
    const urls = runs.flatMap(run => run.safe_outputs).map(output => output.url);
    assert.equal(new Set(urls).size, urls.length, "batched outputs have distinct identities");
  }
});

test("probabilistic outcomes preserve timestamps, output eligibility and delayed Value acceptance", () => {
  const now = Date.parse("2040-06-15T12:00:00Z");
  const sample = enterpriseSample(now);
  const runs = sample.records.filter(record => record.kind === "run").map(record => record.run);
  assert.deepEqual(new Set(runs.map(run => run.classification)),
    new Set(["success", "failure", "timed_out", "cancelled", "action_required", "pending"]));
  for (const run of runs) {
    assert.ok(Date.parse(run.started_at) <= Date.parse(run.updated_at));
    assert.ok(Date.parse(run.updated_at) <= now);
    assert.equal(run.safe_items_count, run.safe_outputs.length);
    if (run.classification !== "success") assert.equal(run.safe_outputs.length, 0);
    assert.ok(run.friction.cost.aic <= run.token_usage_summary.total_aic);
    const grader = run.graders.results[0];
    if (grader) assert.equal(grader.passed, grader.value >= grader.threshold);
    for (const output of run.safe_outputs) {
      const closedAt = output.github_issue_status.closed_at;
      if (closedAt) {
        assert.ok(Date.parse(closedAt) > Date.parse(output.timestamp));
        assert.ok(Date.parse(closedAt) <= now);
      }
    }
  }
  const packets = sample.records.filter(record => record.kind === "operational_value_evidence")
    .map(record => record.operational_value_evidence);
  const delays = new Set();
  const outputStates = new Set();
  const changes = [];
  for (const packet of packets) {
    const facts = packet.facts;
    const outputs = new Map(facts.filter(fact => fact["fact-kind"] === "output").map(fact => [fact["fact-id"], fact]));
    for (const output of outputs.values()) outputStates.add(output.state);
    for (const fact of facts.filter(fact => fact["fact-kind"] === "implementation")) {
      const link = facts.find(link => link["fact-kind"] === "output-implementation" && link["related-id"] === fact["fact-id"]);
      const delay = Date.parse(fact["accepted-at"]) - Date.parse(outputs.get(link["parent-id"]).at);
      assert.ok(delay > 0);
      assert.ok(Date.parse(fact["accepted-at"]) <= now);
      delays.add(Math.round(delay / 3600_000));
    }
    const snapshots = facts.filter(fact => fact["fact-kind"] === "snapshot");
    for (let index = 1; index < snapshots.length; index++) changes.push(snapshots[index].value - snapshots[index - 1].value);
  }
  assert.deepEqual(outputStates, new Set(["open", "closed"]));
  assert.ok(delays.size > 20);
  assert.ok(changes.some(change => change > 0) && changes.some(change => change < 0));
});

test("rebuilding the enterprise sample does not duplicate daily Value observations", async () => {
  const indexedDB = new IDBFactory();
  const now = Date.parse("2040-06-15T12:00:00.000Z");
  let identities;
  for (const clock of [now, now + 60_000]) {
    const sample = enterpriseSample(clock);
    // The full run/detail corpus is covered by the canonical round-trip test below.
    const records = sample.records.filter(record =>
      ["operational_value", "operational_value_evidence", "workflow_value_collection"].includes(record.kind));
    const result = await ingestCachedGhAwJsonl(indexedDB, records.map(record => JSON.stringify(record)).join("\n"), { now: clock });
    assert.equal(result.updated, true, "rebuild must ingest changed evidence, not skip a cached payload");
    const values = await readCollection(indexedDB, "operationalValues");
    assert.equal(values.length, 672);
    const current = values.map(value => value.id).sort();
    if (identities) assert.deepEqual(current, identities);
    identities = current;
  }
});

async function workspace(t) {
  const directory = path.join(root, ".tmp", `pages-demo-test-${randomUUID()}`);
  await mkdir(directory, { recursive: true });
  t.after(() => rm(directory, { recursive: true, force: true }));
  return directory;
}

test("Pages accepts only generated sample data, with no fixture or real-data switch", async () => {
  for (const args of [["--fixture"], ["--activity-dir", "data"], ["--other"], ["--output"]]) {
    assert.throws(() => parsePagesArguments(args));
  }
  assert.deepEqual(parsePagesArguments([]), {});
  assert.deepEqual(parsePagesArguments(["--output", ".tmp/demo"]), { output: ".tmp/demo" });
  assert.deepEqual(parsePagesArguments(["--help"]), { help: true });
  await assert.rejects(prepareDashboardPages({ activityDir: "data" }), /synthetic data only/);
  await assert.rejects(prepareDashboardPages({ includeSqlite: true }), /synthetic data only/);
});

test("Pages output refuses repository paths, unowned data, and symlink ancestors", async (t) => {
  const directory = await workspace(t);
  for (const output of [path.parse(root).root, root, path.dirname(root), path.join(root, ".tmp"),
    path.join(root, "dashboard"), path.join(root, "scripts")]) {
    await assert.rejects(assertSafePagesOutput(output), /dedicated directory/);
  }
  await writeFile(path.join(directory, "keep.txt"), "important");
  await assert.rejects(assertSafePagesOutput(directory), /not owned/);
  const link = `${directory}-link`;
  await symlink(directory, link);
  t.after(() => rm(link, { force: true }));
  await assert.rejects(assertSafePagesOutput(path.join(link, "output")), /symlinks/);
  assert.equal(await readFile(path.join(directory, "keep.txt"), "utf8"), "important");
});

test("data-free build leaves browser samples to production canonical ingestion, without operational exports", { timeout: 240_000 }, async (t) => {
  const directory = await workspace(t);
  const output = path.join(directory, "site");
  const now = Date.parse("2040-06-15T12:00:00.000Z");
  const result = await prepareDashboardPages({ output, now });
  assert.equal(result.synthetic, true);
  const marker = JSON.parse(await readFile(path.join(output, ".cao-pages-demo.json"), "utf8"));
  assert.equal(marker.synthetic, true);
  const names = await readdir(output);
  for (const name of ["gh-aw-logs.sqlite", "agent-summary.json", "payload-hashes.json",
    "gh-aw-logs-shards", "control-settings.json", "activity"]) assert.ok(!names.includes(name), name);
  const dashboard = JSON.parse(await readFile(path.join(output, "dashboard.json"), "utf8"));
  assert.equal(dashboard.dashboard.title, "CAO synthetic enterprise demo");
  assert.ok(dashboard.dashboard.callouts.some(callout => callout.id === "synthetic-demo" && !callout["visible-when"]));
  const overviewPage = dashboard.dashboard.pages.find(page => page.id === "overview");
  const overviewChunk = JSON.parse(await readFile(path.join(output, overviewPage.chunk), "utf8"));
  assert.ok(overviewChunk.page.views.some(view => view.id === "synthetic-demo" && view.mark === "callout" && !view["visible-when"]));
  assert.equal(dashboard.dashboard["github-url-base"], "https://example.invalid");
  const html = await readFile(path.join(output, "index.html"), "utf8");
  assert.match(html, /dashboard-deployment" content="synthetic-demo"/);
  assert.match(html, /connect-src 'self'/);
  assert.ok(!names.includes("demo-data"), "build must not pregenerate demo data or manifests");
  const sample = enterpriseSample(now, { count: enterpriseDemo.installedRepositories });
  const inventory = sample.sources;
  assert.equal(inventory.workflows.metadata["source-kind"], "synthetic");
  assert.equal(inventory.workflows.metadata["as-of"], new Date(now).toISOString());
  const indexedDB = new IDBFactory();
  await ingestDashboardSources(indexedDB, inventory, { now, deferMaintenance: true });
  await ingestCachedGhAwJsonl(indexedDB, sample.records.map(record => JSON.stringify(record)).join("\n"), { now });
  const runs = await readCollection(indexedDB, "runs");
  assert.equal(runs.length, sample.runs);
  assert.equal(marker.repositories, 1_000_000);
  const dashboardDocument = loadDashboardSourceSync(path.join(root, "dashboard/site/dashboard.json")).document.dashboard;
  const overview = dashboardDocument.pages.find(page => page.id === "overview");
  const rhythmQuery = compileDashboardViewPayloadQueries(overview, "overview", {
    viewId: "overview-header", sourceNames: ["overview-rhythm"],
    queries: dashboardDocument.queries,
    evaluatedAt: new Date(now).toISOString(),
    queryContext: { timeWindow: { end: new Date(now).toISOString() } },
  });
  const database = await queryDatabaseSources(indexedDB, inventory, ["overview-runs"]);
  const rhythm = processDataRequest({
    operation: "execute-dashboard-queries", queries: rhythmQuery.queries,
    sourceNames: rhythmQuery.aliases, sources: database,
  })[rhythmQuery.aliases[0]];
  assert.equal(rhythm.metadata.availability, "available");
  assert.equal(rhythm.rows[0].rhythm.days.length, 7);
  assert.ok(rhythm.rows[0].rhythm.days.some(day => day.current > 0), "current week has visible bars");
  assert.ok(rhythm.rows[0].rhythm.days.every(day => day.previous > 0), "last week has visible bars");
  assert.equal((await readCollection(indexedDB, "repositories")).length, 50);
  assert.equal((await readCollection(indexedDB, "workflows")).length, 200);
  assert.equal((await readCollection(indexedDB, "campaigns")).length, 3);
  const expectedOutputs = new Map();
  const lifecycleByUrl = new Map();
  for (const { run } of enterpriseSample(now).records.filter(record => record.kind === "run")) {
    const key = `${run.repository}:${run.workflow_path}`;
    const retainedOutputs = run.safe_outputs.filter(output => !isExpiredCanonicalRecord("issues",
      { timestamp: output.timestamp }, { now }));
    expectedOutputs.set(key, (expectedOutputs.get(key) ?? 0) + retainedOutputs.length);
    for (const output of retainedOutputs) lifecycleByUrl.set(output.url, output.github_issue_status);
  }
  const workflowByRun = new Map(runs.map(run => [run.id, `${run.owner}/${run.repository}:${run.workflowPath}`]));
  const actualOutputs = new Map(runs.map(run => [workflowByRun.get(run.id), 0]));
  for (const issue of await readCollection(indexedDB, "issues")) {
    const key = workflowByRun.get(issue.runId);
    assert.ok(actualOutputs.has(key), "canonical outputs retain their producing workflow");
    actualOutputs.set(key, actualOutputs.get(key) + 1);
    const lifecycle = lifecycleByUrl.get(issue.correlationId);
    assert.ok(lifecycle, "canonical output retains its exact URL");
    assert.equal(issue.state, lifecycle.state);
    assert.equal(issue.closed, lifecycle.closed);
    assert.equal(issue.closedAt, lifecycle.closed_at);
  }
  assert.deepEqual(actualOutputs, expectedOutputs, "normalization preserves per-workflow output counts and batches within retention");
  for (const collection of ["campaigns", "marketplacePackages", "domains", "tools", "skills", "friction",
    "audits", "issues", "graders", "graderObservations", "evals", "evalObservations", "experiments",
    "experimentAssignments", "operationalValues", "operationalValueEvidence", "workflowValueCollections"]) {
    assert.ok((await readCollection(indexedDB, collection)).length > 0, `${collection} must have canonical sample evidence`);
  }
  for (const field of ["agentVersion", "modelId", "engine", "duration", "aicTotal", "inputTokens", "outputTokens"]) {
    assert.ok(runs.every(run => run[field] !== undefined && run[field] !== null && run[field] !== ""), `run.${field}`);
  }
  assert.equal(sample.memory.length, 3);
  assert.ok(runs.some(run => run.conclusion === "failure"));
  assert.ok(runs.every(run => run.id.includes("sample-enterprise/")));
  const tools = await readCollection(indexedDB, "tools");
  assert.ok(tools.length > 0);
  assert.ok(tools.some(record => !isExpiredCanonicalRecord("tools", record, { now: now + 60_000 })));
  assert.match(await readFile(path.join(output, "llms.txt"), "utf8"), /not operational evidence/);
});
