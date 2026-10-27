import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { chmod, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { promisify } from "node:util";
import { parse } from "yaml";
import { discoverWorkflows } from "../../activity/cli/dashboard.mjs";
import { mergeInventoryDashboardSources } from "../../activity/inventory-sources.mjs";

const execute = promisify(execFile);
const workflow = parse(await readFile(".github/workflows/cao-activity.yml", "utf8"));
const collectionStep = workflow.jobs.collect.steps.find(({ name }) => name === "Download owner-scoped agentic workflow logs");
const repositories = ["acme/one", "acme/two", "acme/three"];

async function fixture(t, mode) {
  const root = await mkdtemp(path.join(os.tmpdir(), "cao-partial-collection-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const bin = path.join(root, "bin");
  const seeds = path.join(root, "seed-shards");
  const shards = path.join(root, "partial", "gh-aw-logs-shards");
  const database = path.join(root, "cao-activity", "collection.sqlite");
  const inventory = path.join(root, "inventory-sources.json");
  const output = path.join(root, "github-output");
  const summary = path.join(root, "summary");
  const calls = path.join(root, "calls.jsonl");
  const exitCode = path.join(root, "exit-code");
  await mkdir(bin);
  await mkdir(seeds);
  await writeFile(inventory, JSON.stringify(Object.fromEntries(
    ["repositories", "workflows"].map((name) => [name, {
      source: name, rows: [], metadata: { completeness: "complete" },
    }]),
  )));
  for (const [index, repository] of repositories.entries()) {
    await writeFile(path.join(seeds, `${repository.replace("/", "-")}-logs-seed.jsonl`), `${JSON.stringify({
      schema_version: 2, kind: "run", run: {
        run_id: index + 1, repository, organization: "acme",
        workflow_name: "Example",
        workflow_path: ".github/workflows/example.lock.yml",
        created_at: new Date().toISOString(), status: "completed",
      },
    })}\n`);
  }
  const gh = path.join(bin, "gh");
  await writeFile(gh, `#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");
const args = process.argv.slice(2);
fs.appendFileSync(process.env.CALLS, JSON.stringify(args) + "\\n");
const calls = fs.readFileSync(process.env.CALLS, "utf8").trim().split("\\n").map(JSON.parse);
if (args[0] === "api") {
  if (args[1] !== "rate_limit") {
    if (/^repos\\/[^/]+\\/[^/]+$/.test(args[1])) {
      const repository = args[1].slice(6);
      const private = process.env.MODE === "visibility-private" && repository === "acme/two";
      console.log(JSON.stringify({ full_name: repository, private, visibility: private ? "private" : "public" }));
      process.exit(0);
    }
    console.log(JSON.stringify([{ workflows: [{ path: ".github/workflows/example.lock.yml" }] }]));
    process.exit(0);
  }
  const count = calls.filter((call) => call[0] === "api" && call[1] === "rate_limit").length;
  const limited = process.env.MODE === "reserve-first" || process.env.MODE === "reserve-second" && count > 1;
  console.log(JSON.stringify({ resources: { core: { limit: 5000, remaining: limited ? 2000 : 5000, reset: 1791406800 } } }));
  process.exit(0);
}
const repository = args[args.indexOf("--repo") + 1];
const shard = args[args.indexOf("--cached-jsonl") + 1].replace(/\\*$/, "") + "new.jsonl";
fs.mkdirSync(path.dirname(shard), { recursive: true });
fs.writeFileSync(shard, JSON.stringify({ schema_version: 2, kind: "run", run: {
  run_id: repository === "acme/one" ? 42 : 43, repository, organization: "acme",
  workflow_name: "Example",
  workflow_path: ".github/workflows/example.lock.yml", created_at: new Date().toISOString(), status: "completed",
} }) + "\\n");
if (repository === "acme/two") {
  if (process.env.MODE.startsWith("session-")) {
    const usage = path.join(args[args.indexOf("--output") + 1], "run-43", "usage");
    fs.mkdirSync(usage, { recursive: true });
    fs.writeFileSync(path.join(usage, "aw_session.jsonl"),
      process.env.MODE === "session-json" ? "SECRET_INVALID_JSON" : JSON.stringify({ type: "session.format", data: { version: 1 } }));
    if (process.env.MODE === "session-json") {
      fs.writeFileSync(path.join(usage, "aw_info.json"), JSON.stringify({ run_id: 43, run_attempt: 1, repository }));
    }
  }
  const messages = {
    primary: "HTTP 403: API rate limit exceeded for user ID 123",
    secondary: "HTTP 429: You have exceeded a secondary rate limit",
    permission: "HTTP 403: Resource not accessible by integration",
    transport: "HTTP 500: Internal Server Error",
    malformed: "HTTP 403: API rate limit exceeded for user ID 123",
    "session-identity": "HTTP 403: API rate limit exceeded for user ID 123",
    "session-json": "HTTP 403: API rate limit exceeded for user ID 123",
    "unknown-75": "Unexpected collector failure",
  };
  if (process.env.MODE === "malformed") fs.appendFileSync(shard, "invalid-json\\n");
  if (messages[process.env.MODE]) {
    console.error(messages[process.env.MODE]);
    process.exit(process.env.MODE === "unknown-75" ? 75 : 1);
  }
}
`);
  await chmod(gh, 0o755);
  return {
    root, database, inventory, output, summary, calls, exitCode,
    env: {
      ...process.env, PATH: `${bin}:${process.env.PATH}`, MODE: mode, CALLS: calls,
      RUNNER_TEMP: root, GITHUB_REPOSITORY: "acme/control",
      GITHUB_OUTPUT: output, GITHUB_STEP_SUMMARY: summary,
      REPORT_AIC_CACHE: path.join(root, "downloads"),
      REPORT_ACTIVITY_DATABASE: database,
      REPORT_GH_AW_LOGS_EXIT_CODE: exitCode,
      REPORT_GH_AW_LOGS_SEED_SHARDS: seeds,
      REPORT_GH_AW_LOGS_SHARDS: shards,
      REPORT_INVENTORY_SOURCES: inventory,
      REPORT_INCLUDE_CONTROL_REPOSITORY: "0",
      REPORT_TARGET_REPOSITORIES_JSON: JSON.stringify(repositories),
      REPORT_MAX_GITHUB_API_RATE_LIMIT: "-2000", REPORT_DEFER_ISSUE_STATUS: "1",
    },
  };
}

for (const [mode, expectedRuns, expectedDownloads] of [
  ["reserve-first", ["1", "2", "3"], 0],
  ["reserve-second", ["1", "2", "3", "42"], 1],
  ["primary", ["1", "2", "3", "42", "43"], 2],
  ["secondary", ["1", "2", "3", "42", "43"], 2],
  ["session-identity", ["1", "2", "3", "42", "43"], 2],
  ["session-json", ["1", "2", "3", "42", "43"], 2],
]) {
  test(`${mode} exhaustion publishes a marked partial fragment and indexes retained and downloaded runs`, async (t) => {
    const item = await fixture(t, mode);
    const result = await execute("bash", ["-e", "-c", collectionStep.run], { env: item.env });
    assert.match(result.stdout, /indexing retained and downloaded runs as partial evidence/);
    if (mode.startsWith("session-")) {
      assert.match(result.stderr, /Skipping invalid unified session evidence for run 43/);
      assert.doesNotMatch(result.stderr, /SECRET_/);
    }
    assert.equal(await readFile(item.exitCode, "utf8"), "75\n");
    assert.equal(await readFile(item.output, "utf8"), "rate-limited=true\n");
    assert.match(await readFile(item.summary, "utf8"), /uncollected repositories are not current/);
    const invocations = (await readFile(item.calls, "utf8")).trim().split("\n").map(JSON.parse);
    assert.equal(invocations.filter((args) => args[0] === "aw").length, expectedDownloads);
    const { stdout } = await execute(process.execPath, [
      "activity/cao.mjs", "query", "--database", item.database, "--collection", "runs",
    ]);
    assert.deepEqual(JSON.parse(stdout).map((run) => run.githubRunId).sort(), expectedRuns);
    const health = await execute(process.execPath, ["activity/cao.mjs", "repair-database", "--database", item.database]);
    assert.equal(JSON.parse(health.stdout).healthy, true);
    const sources = JSON.parse(await readFile(item.inventory, "utf8"));
    const merged = mergeInventoryDashboardSources([sources]);
    for (const name of ["repositories", "workflows"]) {
      assert.equal(sources[name].metadata.completeness, "partial");
      assert.equal(merged[name].metadata.completeness, "partial");
      assert.match(sources[name].metadata["collection-reason"], /retained runs may be stale/);
      const failed = sources[name].metadata["repository-failures"].map(({ repository }) => repository);
      assert.deepEqual(failed, repositories.slice(mode === "reserve-first" ? 0 : 1));
    }
  });
}

for (const mode of ["permission", "transport", "malformed", "unknown-75"]) {
  test(`${mode} errors remain hard failures rather than publishable partial snapshots`, async (t) => {
    const item = await fixture(t, mode);
    await assert.rejects(execute("bash", ["-e", "-c", collectionStep.run], { env: item.env }));
    await assert.rejects(readFile(item.output), { code: "ENOENT" });
    const sources = JSON.parse(await readFile(item.inventory, "utf8"));
    assert.equal(sources.workflows.metadata.completeness, "complete");
  });
}

test("offline workflow discovery makes no API requests and never claims live registry state", async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), "cao-offline-inventory-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  t.mock.method(globalThis, "fetch", () => { throw new Error("Offline discovery must not request GitHub"); });
  await mkdir(path.join(root, ".github", "workflows"), { recursive: true });
  await writeFile(path.join(root, ".github", "workflows", "example.md"), "---\nname: Example\non: workflow_dispatch\n---\nInspect the repository.\n");
  const settings = path.join(root, "settings.json");
  await writeFile(settings, JSON.stringify({ allowed_repositories: repositories }));
  const outputPath = path.join(root, "sources.json");
  await discoverWorkflows({
    root, controlSettingsPath: settings, inventoryPath: path.join(root, "inventory.json"),
    outputPath, repository: "acme/control", offline: true,
  });
  const sources = JSON.parse(await readFile(outputPath, "utf8"));
  assert.equal(sources.repositories.rows.length, 4);
  assert.ok(sources.workflows.rows.length > 0);
  assert.ok(sources.workflows.rows.every((row) => row["workflow-active"] === "unknown"));
  assert.ok(Object.values(sources).every((source) => source.metadata.completeness === "partial"));
  await execute(process.execPath, [
    "activity/cao.mjs", "discover-workflows", "--offline", "--root", root,
    "--control-settings", settings, "--inventory", path.join(root, "cli-inventory.json"),
    "--output", path.join(root, "cli-sources.json"), "--repo", "acme/control",
  ], { env: { ...process.env, GH_TOKEN: "", GITHUB_TOKEN: "" } });
});

test("partial fragments skip extra API work without bypassing authentication and visibility gates", () => {
  const steps = workflow.jobs.collect.steps;
  assert.equal(steps.find(({ name }) => name === "Enrich owner-scoped issue statuses").if,
    "steps.activity-logs.outputs.rate-limited != 'true' && matrix.publicOnly != true");
  const value = steps.find(({ name }) => name === "Retain Value evidence and publish collection health");
  assert.equal(value.env.REPORT_RATE_LIMITED, "${{ steps.activity-logs.outputs.rate-limited }}");
  assert.ok(value.run.indexOf('cp "$retained_values" "$value_output"') < value.run.indexOf('"$REPORT_RATE_LIMITED" == "true"'));
  assert.match(value.run, /reason=api-budget/);
  assert.match(value.run, /defer_args\+=\(--defer "\$reason"\)/);
  assert.equal(value.env.GH_TOKEN, undefined);
  assert.match(value.run, /--apiBudget/);
  assert.equal(value["timeout-minutes"], 20);
  assert.equal(value["continue-on-error"], true);
  assert.doesNotMatch(value.run, /cao\.mjs operational-value|gh api/);
  assert.equal(steps.find(({ name }) => name === "Select independent activity credential")["continue-on-error"], undefined);
  assert.equal(steps.find(({ name }) => name === "Validate repository visibility boundary")["continue-on-error"], undefined);
  assert.match(workflow.jobs.index.if, /needs\.collect\.result == 'success'/);
});

for (const mode of ["permission", "transport", "success"]) {
  test(`public-only ${mode} collection preserves evidence and explicitly reports restricted coverage`, async (t) => {
    const item = await fixture(t, mode);
    await execute("bash", ["-e", "-c", collectionStep.run], { env: { ...item.env, REPORT_PUBLIC_ONLY: "true" } });
    assert.equal(await readFile(item.exitCode, "utf8"), "0\n");
    assert.equal(await readFile(item.output, "utf8"), "evidence-partial=true\n");
    assert.match(await readFile(item.summary, "utf8"), /Missing evidence is not zero activity/);
    const sources = JSON.parse(await readFile(item.inventory, "utf8"));
    assert.equal(sources.workflows.metadata.completeness, "partial");
    assert.match(sources.workflows.metadata["collection-reason"], /artifacts.*unavailable/);
    if (mode !== "success") {
      const unavailable = sources.workflows.metadata["repository-failures"].find(({ repository }) => repository === "acme/two");
      assert.equal(unavailable.state, "unavailable");
      assert.match(unavailable.reason, /could not be collected/);
    }
    const calls = (await readFile(item.calls, "utf8")).trim().split("\n").map(JSON.parse);
    assert.equal(calls.filter((args) => args[0] === "aw").length, 3);
    const merged = mergeInventoryDashboardSources([sources]);
    assert.equal(merged.workflows.metadata.completeness, "partial");
  });
}

test("public-only visibility changes and malformed evidence remain publication-blocking failures", async (t) => {
  for (const mode of ["visibility-private", "malformed"]) {
    const item = await fixture(t, mode);
    await assert.rejects(execute("bash", ["-e", "-c", collectionStep.run], { env: { ...item.env, REPORT_PUBLIC_ONLY: "true" } }));
    await assert.rejects(readFile(item.output), { code: "ENOENT" });
  }
});
