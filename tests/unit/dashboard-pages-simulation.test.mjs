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
import { finalizeNormalizedJsonlIngestion, ingestCachedGhAwJsonl, ingestDashboardSources, ingestDashboardSourceBatches } from "../../dashboard/site/src/data/ingest/coordinator.js";
import {
  enterpriseActivityBatches, enterpriseBlockedDomains, enterpriseDemo, enterpriseRepository,
  enterpriseRepositorySources, enterpriseRepositoryVocabulary, enterpriseSample as generateEnterpriseSample, enterpriseWorkflowNames,
} from "../../scripts/dashboard-demo-data.mjs";
import { demoSha256 } from "../../dashboard/site/src/data/demo/hash.mjs";
import { loadDashboardSourceSync } from "../../dashboard/report/bundle-dashboards.mjs";
import { queryDatabaseSources } from "../../dashboard/site/src/data/queries/database.js";
import { compileDashboardViewPayloadQueries } from "../../dashboard/site/src/data/queries/view-payload-compiler.js";
import { processDataRequest } from "../../dashboard/site/src/data-worker.js";
import { readCollection } from "../../dashboard/site/src/data/storage/indexeddb.js";
import { isExpiredCanonicalRecord } from "../../dashboard/site/src/data/storage/retention.js";

const root = path.resolve(".");
const enterpriseSample = (now, options = {}) => generateEnterpriseSample(now, { runs: 1_200, repositories: 12, count: 12, ...options });

test("the browser enterprise sample streams exactly 5,000 distinct runs in bounded batches", () => {
  const now = Date.parse("2040-06-15T12:00:00Z");
  const ids = new Set();
  const repositories = new Map();
  const earlyCoverage = new Set();
  const blockedDomains = new Set();
  let firstBatchRepositories;
  let runs = 0;
  for (const batch of enterpriseActivityBatches(now)) {
    assert.ok(batch.runs <= 100, "normalization batches stay within the existing query budgets");
    const records = batch.records.filter(record => record.kind === "run");
    assert.equal(records.length, batch.runs);
    if (runs === 0) firstBatchRepositories = new Set(records.map(({ run }) => run.repository)).size;
    for (const { run } of records) {
      ids.add(run.run_id);
      repositories.set(run.repository, (repositories.get(run.repository) ?? 0) + 1);
      if (ids.size <= enterpriseDemo.repositories * 3) earlyCoverage.add(run.repository);
      if (Date.parse(run.started_at) >= now - 7 * 86_400_000) {
        for (const [domain, requests] of Object.entries(run.firewall_analysis.requests_by_domain)) {
          if (requests.blocked > 0) blockedDomains.add(domain);
        }
      }
    }
    runs += batch.runs;
  }
  assert.equal(runs, 5_000);
  assert.equal(ids.size, 5_000);
  assert.equal(earlyCoverage.size, 1_000, "cover the fleet within the first 3,000 randomly interleaved runs");
  assert.ok(firstBatchRepositories >= 50 && firstBatchRepositories < 100,
    "early batches are diverse but allow realistic repeated activity, unlike strict round-robin");
  assert.equal(repositories.size, 1_000);
  assert.equal(blockedDomains.size, 128, "the retained firewall window includes all blocked-domain samples");
  assert.deepEqual(blockedDomains, new Set(enterpriseBlockedDomains));
  assert.ok([...blockedDomains].every(domain => domain.endsWith(".invalid")));
  const counts = [...repositories.values()].toSorted((left, right) => right - left);
  const topDecile = counts.slice(0, 100).reduce((sum, count) => sum + count, 0) / runs;
  assert.ok(topDecile > 0.3 && topDecile < 0.75, "a busy minority, not a uniform fleet or a single-repository monopoly");
  assert.ok(counts[0] > counts[500] * 10, "log-normal repository activity has a substantial right tail");
  const combined = enterpriseSample(now, { count: 2 }).records.filter(record => record.kind === "run");
  const partitioned = [0, 1].flatMap(start => enterpriseSample(now, { start, count: 1 }).records)
    .filter(record => record.kind === "run");
  const byId = (left, right) => left.run.run_id - right.run.run_id;
  assert.deepEqual(combined.toSorted(byId), partitioned.toSorted(byId), "worker repository partitions preserve run and session identities");
});

test("repository names use four safe words with a collision-free 32-bit naming space", () => {
  assert.equal(enterpriseRepositoryVocabulary.length, 256);
  const vocabulary = new Set(enterpriseRepositoryVocabulary);
  assert.equal(vocabulary.size, 256);
  assert.ok([...vocabulary].every(word => /^[a-z]+$/.test(word)));
  const names = new Set();
  const positions = Array.from({ length: 4 }, () => new Set());
  for (let index = 0; index < 65_536; index++) {
    const repository = enterpriseRepository(index);
    names.add(repository);
    const words = repository.split("/")[1].split("-");
    assert.equal(words.length, 4);
    for (const [position, word] of words.entries()) {
      assert.ok(vocabulary.has(word));
      positions[position].add(word);
    }
    if (index < enterpriseDemo.repositories) assert.equal(enterpriseRepository(index), repository);
  }
  assert.equal(names.size, 65_536);
  for (const words of positions) assert.equal(words.size, 256, "all eight bits are used at each word position");
  assert.notEqual(enterpriseRepository(0), enterpriseRepository(0xffff_ffff));
  for (const index of [-1, 0x1_0000_0000, 0.5, NaN]) assert.throws(() => enterpriseRepository(index), RangeError);
});

test("Value throughput follows the allocated run population rather than an independent fleet multiplier", () => {
  const sample = enterpriseSample(Date.parse("2040-06-15T12:00:00Z"), { runs: 0 });
  const evidence = sample.records.filter(record => record.kind === "operational_value_evidence");
  assert.equal(evidence.length, 48);
  for (const { operational_value_evidence: packet } of evidence) {
    assert.ok(packet.facts.some(fact => fact["fact-kind"] === "contract"));
    assert.equal(packet.facts.some(fact => ["output", "implementation"].includes(fact["fact-kind"])), false);
    assert.equal(new Set(packet.facts.filter(fact => fact["fact-kind"] === "snapshot").map(fact => fact.value)).size, 1);
  }
});

test("workflow names vary safely across repositories and agree with run and Value evidence", () => {
  const now = Date.parse("2040-06-15T12:00:00Z");
  const names = new Set(enterpriseWorkflowNames);
  assert.equal(names.size, 64);
  assert.ok([...names].every(name => /^[A-Za-z]+(?: [A-Za-z]+)*$/.test(name)));
  const inventory = generateEnterpriseSample(now, { count: enterpriseDemo.installedRepositories, includeHistory: false });
  const installed = inventory.sources.workflows.rows;
  assert.deepEqual(new Set(installed.map(workflow => workflow["workflow-name"])), names);
  const sample = enterpriseSample(now);
  for (const { run } of sample.records.filter(record => record.kind === "run")) {
    assert.ok(names.has(run.workflow_name));
    const source = installed.find(workflow => `${workflow.organization}/${workflow.repository}` === run.repository
      && workflow.workflow === run.workflow_path);
    assert.equal(source?.["workflow-name"], run.workflow_name);
  }
  for (const record of sample.records.filter(record => record.kind === "operational_value_evidence")) {
    const packet = record.operational_value_evidence;
    const contract = packet.facts.find(fact => fact["fact-kind"] === "contract");
    const source = installed.find(workflow => `${workflow.organization}/${workflow.repository}` === packet.repository
      && workflow.workflow === packet.sourcePath);
    assert.equal(contract["workflow-name"], source?.["workflow-name"]);
  }
});

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
  assert.deepEqual([enterpriseDemo.repositories, enterpriseDemo.campaigns, enterpriseDemo.installedWorkflows], [1_000, 3, 4_000]);
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
    const mix = slug => {
      const outputs = runs.filter(run => run.workflow_path === `.github/workflows/${slug}.md`).flatMap(run => run.safe_outputs);
      return outputs.filter(output => output.type === "create_pull_request").length / outputs.length;
    };
    assert.ok(mix("dependency-review") < 0.25 && mix("security-review") < 0.25, "review workflows favor findings");
    assert.ok(mix("repository-assistant") > 0.75 && mix("documentation") > 0.75, "repair workflows favor PRs");
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

test("synthetic unified sessions conserve usage across turns and phases without inventing terminal evidence", () => {
  const now = Date.parse("2040-06-15T12:00:00Z");
  const runs = enterpriseSample(now).records.filter(record => record.kind === "run").map(record => record.run);
  const turns = new Set();
  for (const run of runs) {
    const session = run.cao_unified_session;
    assert.ok(session, "every sample uses the production unified-session parser");
    const events = session.events;
    const pending = run.status !== "completed";
    const approval = run.conclusion === "action_required";
    const interrupted = pending || ["cancelled", "timed_out"].includes(run.conclusion);
    const requests = events.filter(event => event.type === "firewall.token_usage" && event.fields.phase === "agent");
    if (approval) {
      assert.equal(events.some(event => event.type === "session.start"), false);
      assert.equal(events.some(event => event.type === "firewall.token_usage"), false);
      assert.equal(run.token_usage_summary.total_aic, 0);
    } else {
      assert.ok(events.some(event => event.type === "session.start"));
      assert.ok(run.github_api_calls >= run.mcp_tool_usage.tool_calls.filter(call => call.server_name === "github").length,
        "network activity covers generated GitHub tool calls without an unrelated request multiplier");
      assert.ok(requests.length >= 2 && requests.length <= 20);
      turns.add(requests.length);
      assert.ok(requests[0].fields.inputTokens > requests[1].fields.inputTokens, "initial prompt dominates uncached input");
      assert.equal(requests[0].fields.cacheReadTokens, 0);
      assert.ok(requests.at(-1).fields.cacheReadTokens > 0, "later turns reuse context");
      for (const [field, value] of Object.entries(run.token_usage_summary)) {
        assert.ok(Math.abs(value - session.run.token_usage_summary[field]) < 1e-8,
          `${field} conserves totals despite proxy/report/snapshot copies`);
      }
    }
    assert.equal(events.some(event => event.type === "session.result"), !approval && !interrupted);
    assert.equal(events.some(event => event.type === "detection.result"), !approval && !interrupted);
    assert.equal(events.some(event => event.type === "session.collection"), !pending);
    if (pending) {
      assert.equal(events.some(event => event.type === "session.shutdown"), false);
      assert.equal(events.some(event => event.type === "workflow.job_result" && event.fields.name === "agent"), false);
    }
    for (const event of events) {
      if (event.timestamp) {
        assert.ok(Date.parse(event.timestamp) >= Date.parse(run.started_at));
        assert.ok(Date.parse(event.timestamp) <= Date.parse(run.updated_at));
      }
    }
    const tools = events.filter(event => ["tool.execution_start", "mcp.tool_call"].includes(event.type));
    assert.equal(tools.reduce((sum, event) => sum + event.fields.invocationCount, 0), run.mcp_tool_usage.tool_calls.length);
    for (const call of tools.filter(event => event.type === "tool.execution_start")) {
      assert.ok(events.some(event => event.type === "tool.execution_complete"
        && event.fields.correlationId === call.fields.correlationId));
    }
    const outputs = events.filter(event => event.type === "safe_output.result");
    assert.equal(outputs.length, run.safe_outputs.length);
    if (outputs.length) assert.equal(run.conclusion, "success");
    assert.ok(events.length < 500, "bounded per-run event population");
  }
  assert.ok(turns.size >= 10, "session length follows heterogeneous work rather than a fixed template");
});

test("unified demo telemetry survives canonical ingestion with exact run accounting", async () => {
  const now = Date.parse("2040-06-15T12:00:00Z");
  const population = enterpriseSample(now).records.filter(record => record.kind === "run");
  const records = ["success", "failure", "pending", "action_required", "timed_out", "cancelled"]
    .map(outcome => population.find(record => record.run.classification === outcome
      && Date.parse(record.run.started_at) >= now - 6 * 86_400_000));
  const resolution = population.find(record => record.run.cao_unified_session.events
    .some(event => event.type === "model.resolution") && Date.parse(record.run.started_at) >= now - 6 * 86_400_000);
  if (!records.includes(resolution)) records.push(resolution);
  assert.ok(records.every(Boolean));
  const indexedDB = new IDBFactory();
  await ingestCachedGhAwJsonl(indexedDB, records.map(record => JSON.stringify(record)).join("\n"), { now });
  const canonical = await readCollection(indexedDB, "runs");
  assert.equal(canonical.length, records.length);
  for (const { run } of records) {
    const actual = canonical.find(row => row.githubRunId === String(run.run_id));
    assert.ok(actual);
    for (const [field, source] of Object.entries({ inputTokens: "total_input_tokens", outputTokens: "total_output_tokens",
      cacheReadTokens: "total_cache_read_tokens", cacheWriteTokens: "total_cache_write_tokens",
      reasoningTokens: "total_reasoning_tokens", aicTotal: "total_aic" })) {
      assert.ok(Math.abs(actual[field] - run.token_usage_summary[source]) < 1e-8, `${field} round trips`);
    }
  }
  const audits = await readCollection(indexedDB, "audits");
  for (const type of ["session.start", "assistant.message", "session.result", "model.resolution", "detection.result"]) {
    assert.ok(audits.some(row => row.type === type), `${type} passes through production ingestion`);
  }
  const domains = await readCollection(indexedDB, "domains");
  assert.ok(domains.some(row => row.type === "firewall.http_access" && row.responseBytes > 0 && row.latencyMs > 0));
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
  const sample = enterpriseSample(now, { count: 50, runs: 600 });
  const inventory = sample.sources;
  assert.equal(inventory.workflows.metadata["source-kind"], "synthetic");
  assert.equal(inventory.workflows.metadata["as-of"], new Date(now).toISOString());
  const indexedDB = new IDBFactory();
  await ingestDashboardSources(indexedDB, inventory, { now, deferMaintenance: true });
  let batchIndex = 0;
  for (const batch of enterpriseActivityBatches(now, { repositories: 12, runs: 600 })) {
    await ingestCachedGhAwJsonl(indexedDB, batch.records.map(record => JSON.stringify(record)).join("\n"), {
      now, deferMaintenance: true, payloadScope: `synthetic-demo-test/activity/${batchIndex}`,
    });
    if (batchIndex++ === 0) {
      const partial = await readCollection(indexedDB, "runs");
      assert.equal(partial.length, 100);
      assert.equal(new Set(partial.map(run => run.repository)).size, 12,
        "early canonical results include the entire fixture fleet, not one completed repository");
    }
  }
  await finalizeNormalizedJsonlIngestion(indexedDB, { now });
  const runs = await readCollection(indexedDB, "runs");
  assert.equal(runs.length, sample.runs);
  assert.equal(marker.repositories, 1_000);
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
  for (const { run } of sample.records.filter(record => record.kind === "run")) {
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
  const blockedDomains = new Set((await readCollection(indexedDB, "domains"))
    .filter(record => record.decision === "denied").map(record => record.domain));
  for (const domain of enterpriseBlockedDomains.slice(0, 12)) {
    assert.ok(blockedDomains.has(domain), "guaranteed blocked domains survive production normalization");
  }
  assert.match(await readFile(path.join(output, "llms.txt"), "utf8"), /not operational evidence/);
});
