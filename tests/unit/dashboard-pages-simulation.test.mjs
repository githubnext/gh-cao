import assert from "node:assert/strict";
import { createHash, randomUUID } from "node:crypto";
import { mkdir, readFile, readdir, rm, symlink, writeFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { Readable } from "node:stream";
import "fake-indexeddb/auto";
import { IDBFactory } from "fake-indexeddb";
import {
  assertSafePagesOutput, parsePagesArguments, prepareDashboardPages,
} from "../../scripts/simulate-dashboard-pages.mjs";
import { ingestCachedGhAwJsonl, ingestDashboardSources, ingestNormalizedJsonl } from "../../dashboard/site/src/data/ingest/coordinator.js";
import { enterpriseSample } from "../../scripts/dashboard-demo-data.mjs";
import { readCollection } from "../../dashboard/site/src/data/storage/indexeddb.js";
import { relationshipErrors } from "../../dashboard/site/src/data/model/schema.js";
import { isExpiredCanonicalRecord } from "../../dashboard/site/src/data/storage/retention.js";

const root = path.resolve(".");

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
  const modelCounts = [...new Set(runs.map(run => run.model))].map(model => runs.filter(run => run.model === model).length);
  assert.equal(modelCounts.length, 3);
  assert.ok(Math.max(...modelCounts) > Math.min(...modelCounts) * 1.1, "model mix is not round robin");
  const variants = new Map();
  for (const run of runs) {
    const date = run.started_at.slice(0, 10);
    if (!variants.has(date)) variants.set(date, new Set());
    variants.get(date).add(run.experiments.assignments["review-strategy"]);
  }
  assert.ok([...variants.values()].every(values => values.size === 2), "variant is not determined by day");
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
  for (const clock of [now, now + 60_000]) {
    const sample = enterpriseSample(clock);
    await ingestCachedGhAwJsonl(indexedDB, sample.records.map(record => JSON.stringify(record)).join("\n"), { now: clock });
    assert.equal((await readCollection(indexedDB, "operationalValues")).length, 672);
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

test("sample-only build produces verified canonical enterprise data and no operational exports", { timeout: 120_000 }, async (t) => {
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
  assert.equal(dashboard.dashboard["github-url-base"], "https://example.invalid");
  const html = await readFile(path.join(output, "index.html"), "utf8");
  assert.match(html, /dashboard-deployment" content="synthetic-demo"/);
  assert.match(html, /connect-src 'self'/);
  const data = path.join(output, "demo-data");
  const inventory = JSON.parse(await readFile(path.join(data, "inventory-sources.json"), "utf8"));
  assert.equal(inventory.workflows.metadata["source-kind"], "synthetic");
  assert.equal(inventory.workflows.metadata["as-of"], new Date(now).toISOString());
  const manifest = JSON.parse(await readFile(path.join(data, "payload-hashes.json"), "utf8"));
  const indexedDB = new IDBFactory();
  await ingestDashboardSources(indexedDB, inventory, { now, deferMaintenance: true });
  const records = {};
  // Use the canonical production boundary, including run-before-detail ingestion.
  const shards = Object.entries(manifest).sort(([a], [b]) =>
    Number(b.startsWith("gh-aw-logs-runs/")) - Number(a.startsWith("gh-aw-logs-runs/")) || a.localeCompare(b));
  for (const [name, expected] of shards) {
    assert.match(name, /^gh-aw-logs-(?:runs|records)\/[^/]+\.jsonl$/);
    const bytes = await readFile(path.join(data, name));
    assert.equal(createHash("sha256").update(bytes).digest("hex"), expected);
    for (const entry of bytes.toString().trim().split("\n").map(line => JSON.parse(line))) {
      if (entry.kind === "record") (records[entry.collection] ??= []).push(entry.record);
    }
    await ingestNormalizedJsonl(indexedDB, Readable.from([bytes]), {
      payloadIdentity: expected, payloadScope: name, now, deferMaintenance: true,
      expectedPhase: name.startsWith("gh-aw-logs-runs/") ? "runs" : "records",
    });
  }
  assert.deepEqual(relationshipErrors(records), []);
  const runs = await readCollection(indexedDB, "runs");
  assert.equal(runs.length, marker.runs);
  assert.equal((await readCollection(indexedDB, "repositories")).length, 12);
  assert.equal((await readCollection(indexedDB, "workflows")).length, 48);
  for (const collection of ["campaigns", "marketplacePackages", "domains", "tools", "skills", "friction",
    "audits", "issues", "graders", "graderObservations", "evals", "evalObservations", "experiments",
    "experimentAssignments", "operationalValues", "operationalValueEvidence", "workflowValueCollections"]) {
    assert.ok((await readCollection(indexedDB, collection)).length > 0, `${collection} must have canonical sample evidence`);
  }
  for (const field of ["agentVersion", "modelId", "engine", "duration", "aicTotal", "inputTokens", "outputTokens"]) {
    assert.ok(runs.every(run => run[field] !== undefined && run[field] !== null && run[field] !== ""), `run.${field}`);
  }
  const memory = JSON.parse(await readFile(path.join(data, "memory/manifest.json"), "utf8"));
  assert.equal(memory.campaigns.length, 2);
  assert.ok(runs.some(run => run.conclusion === "failure"));
  assert.ok(runs.every(run => run.id.includes("sample-enterprise/")));
  const tools = await readCollection(indexedDB, "tools");
  assert.ok(tools.length > 0);
  assert.ok(tools.some(record => !isExpiredCanonicalRecord("tools", record, { now: now + 60_000 })));
  assert.match(await readFile(path.join(output, "llms.txt"), "utf8"), /not operational evidence/);
});
