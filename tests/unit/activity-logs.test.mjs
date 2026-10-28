import assert from "node:assert/strict";
import { chmod, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { promisify } from "node:util";
import test from "node:test";
import { parse } from "yaml";
import { readGhAwLogShards } from "../../activity/gh-aw-logs.mjs";

const execFileAsync = promisify(execFile);

async function fixture() {
  const root = await mkdtemp(path.join(os.tmpdir(), "activity-logs-"));
  const workflowDirectory = path.join(root, ".github", "workflows");
  const bin = path.join(root, "bin");
  await mkdir(workflowDirectory, { recursive: true });
  await mkdir(bin);
  await writeFile(path.join(workflowDirectory, "sample.lock.yml"), "name: Sample\n");
  return {
    root,
    bin,
    logsPath: path.join(root, "cache", "gh-aw-logs-shards"),
    statePath: path.join(root, "cache", "gh-aw-logs-state.json"),
    outputPath: path.join(root, "cache", "gh-aw-logs"),
    argumentsPath: path.join(root, "arguments.json"),
    precollectionPath: path.join(root, "precollection.jsonl"),
    controlSettingsPath: path.join(root, "control-settings.json"),
    githubOutput: path.join(root, "github-output"),
  };
}

test("activity logs collects and consolidates owned runs from every configured repository", async () => {
  const item = await fixture();
  const ghPath = path.join(item.bin, "gh");
  const seedPath = path.join(item.root, "restored-shards");
  await writeFile(ghPath, `#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");
const args = process.argv.slice(2);
fs.appendFileSync(process.env.GH_ARGS_PATH, JSON.stringify(args) + "\\n");
if (args[0] === "api") {
  if (args[1] !== "rate_limit") {
    process.stdout.write(JSON.stringify([{ workflows: [
      { path: ".github/workflows/remote-experiment.lock.yml" },
      { path: ".github/workflows/ci.yml" },
    ] }]));
    process.exit(0);
  }
  process.stdout.write(JSON.stringify({ resources: {
    core: { limit: 5000, remaining: 5000, reset: 1791406800 },
  } }));
  process.exit(0);
}
const repository = args[args.indexOf("--repo") + 1];
const shardPattern = args[args.indexOf("--cached-jsonl") + 1];
const shardPrefix = shardPattern.replace(/\\*$/, "");
const shardDirectory = path.dirname(shardPrefix);
const shardNamePrefix = path.basename(shardPrefix);
const cachedRecords = fs.readdirSync(shardDirectory)
  .filter((name) => name.startsWith(shardNamePrefix) && name.endsWith(".jsonl"))
  .flatMap((name) => fs.readFileSync(path.join(shardDirectory, name), "utf8").trim().split("\\n"))
  .filter(Boolean);
fs.appendFileSync(process.env.PRECOLLECTION_PATH, JSON.stringify({ repository, cachedRecords: cachedRecords.length }) + "\\n");
const shardPath = shardPattern.replace(/\\*$/, "") + "fixture.jsonl";
fs.mkdirSync(path.dirname(shardPath), { recursive: true });
fs.writeFileSync(shardPath, JSON.stringify({ schema_version: 2, kind: "run", run: {
   database_id: repository === "githubnext/gh-cao" ? 42 : 43,
   repository,
   workflow_path:".github/workflows/sample.lock.yml",
   status:"completed"
  } }) + "\\n");
process.stderr.write("Fetched 1 run\\n");
`);
  await chmod(ghPath, 0o755);
  await writeFile(item.controlSettingsPath, JSON.stringify({
    allowed_repositories: ["github/gh-aw", "githubnext/gh-cao"]
  }));
  try {
    const env = {
      ...process.env,
      PATH: `${item.bin}:${process.env.PATH}`,
      GITHUB_REPOSITORY: "githubnext/gh-cao",
      REPORT_ROOT: item.root,
      REPORT_GH_AW_LOGS_SHARDS: item.logsPath,
      REPORT_GH_AW_LOGS_SEED_SHARDS: seedPath,
      REPORT_GH_AW_LOGS_STATE: item.statePath,
      REPORT_GH_AW_LOGS_EXIT_CODE: path.join(item.root, "cache", "gh-aw-logs-exit-code"),
      REPORT_AIC_CACHE: item.outputPath,
      REPORT_CONTROL_SETTINGS: item.controlSettingsPath,
      GH_ARGS_PATH: item.argumentsPath,
      PRECOLLECTION_PATH: item.precollectionPath,
      GITHUB_OUTPUT: item.githubOutput,
    };
    await mkdir(item.logsPath, { recursive: true });
    await mkdir(seedPath);
    const duplicate = JSON.stringify({ schema_version: 2, kind: "run", run: {
      database_id: 43,
      repository: "github/gh-aw",
      workflow_path: ".github/workflows/sample.lock.yml",
      status: "completed"
    } });
    await writeFile(path.join(seedPath, "github-gh-aw-logs-1000-first.jsonl"), `${duplicate}\n`);
    await writeFile(path.join(seedPath, "github-gh-aw-logs-2000-second.jsonl"), `${duplicate}\n`);
    const collection = await execFileAsync("bash", [path.resolve("activity/collect-logs.sh")], { env });
    assert.match(collection.stdout, /Activity log collection: 2 repositories; window=30d/);
    assert.match(collection.stdout, /download timeout=10m/);
    assert.match(collection.stdout, /github\/gh-aw restored 2 matching cache shards/);
    assert.match(collection.stdout, /githubnext\/gh-cao download exited 0 after \d+s/);
    assert.match(collection.stdout, /Activity log collection: completed with exit code 0/);
    const { stdout } = await execFileAsync(process.execPath, [path.resolve("activity/logs.mjs")], { env });
    const invocations = (await readFile(item.argumentsPath, "utf8")).trim().split("\n").map(JSON.parse);
    assert.deepEqual(invocations.filter((args) => args[0] === "api" && args[1] === "rate_limit"), [
      ["api", "rate_limit"], ["api", "rate_limit"],
    ]);
    assert.deepEqual(invocations.filter((args) => args[0] === "api" && args[1] !== "rate_limit"), [
      ["api", "repos/github/gh-aw/actions/workflows?per_page=100", "--paginate", "--slurp"],
      ["api", "repos/githubnext/gh-cao/actions/workflows?per_page=100", "--paginate", "--slurp"],
    ]);
    const logInvocations = invocations.filter((args) => args[0] === "aw");
    assert.equal(logInvocations.length, 2);
    const args = logInvocations[0];
    assert.deepEqual(args.slice(0, 3), ["aw", "logs", "--audit"]);
    assert.equal(args.at(-1), "github/gh-aw/.github/workflows/remote-experiment.lock.yml");
    assert.equal(args.some((arg) => arg.endsWith("/ci.yml")), false);
    assert.equal(args.includes("--json"), false);
    assert.deepEqual(args.slice(args.indexOf("--artifacts"), args.indexOf("--artifacts") + 2), [
      "--artifacts",
      "usage,experiment,graders",
    ]);
    assert.equal(args.filter((value) => value === "--prune-older-runs").length, 1);
    const shardPrefix = path.join(item.root, "cache", "gh-aw-logs-shards", "github-gh-aw-logs-");
    assert.deepEqual(args.slice(args.indexOf("--cached-jsonl"), args.indexOf("--cached-jsonl") + 2), [
      "--cached-jsonl",
      `${shardPrefix}*`,
    ]);
    assert.equal(args.filter((value) => value === "logs").length, 1);
    assert.deepEqual(args.slice(args.indexOf("--count"), args.indexOf("--count") + 2), ["--count", "10"]);
    assert.deepEqual(args.slice(args.indexOf("--timeout"), args.indexOf("--timeout") + 2), ["--timeout", "10"]);
    assert.equal(args.filter((value) => value === "--timeout").length, 1);
    assert.deepEqual(logInvocations.map((invocation) => invocation[invocation.indexOf("--repo") + 1]), [
      "github/gh-aw",
      "githubnext/gh-cao",
    ]);
    assert.deepEqual(
      (await readFile(item.precollectionPath, "utf8")).trim().split("\n").map(JSON.parse),
      [
        { repository: "github/gh-aw", cachedRecords: 1 },
        { repository: "githubnext/gh-cao", cachedRecords: 0 },
      ],
    );
    const runs = await readGhAwLogShards(item.logsPath);
    assert.deepEqual(runs.map((run) => run.repository), [
      "github/gh-aw",
      "githubnext/gh-cao",
    ]);
    const state = JSON.parse(await readFile(item.statePath, "utf8"));
    assert.equal(state.available, true);
    assert.equal(state.complete, true);
    assert.equal(Object.hasOwn(state, "jobDetails"), false);
    assert.equal(await readFile(item.githubOutput, "utf8"), "collection-outcome=success\n");
    assert.equal(collection.stderr.match(/Fetched 1 run/g)?.length, 2);
    assert.match(stdout, /Collected snapshot with 2 runs across 1 workflow/);
  } finally {
    await rm(item.root, { recursive: true, force: true });
  }
});

test("collection scopes mixed fresh shards and reports exclusions as partial evidence", async (t) => {
  const item = await fixture();
  t.after(() => rm(item.root, { recursive: true, force: true }));
  const ghPath = path.join(item.bin, "gh");
  await writeFile(ghPath, `#!/usr/bin/env node
const fs = require("node:fs");
const args = process.argv.slice(2);
if (args[0] === "api") {
  process.stdout.write(JSON.stringify(args[1] === "rate_limit"
    ? { resources: { core: { limit: 5000, remaining: 5000, reset: 1791406800 } } }
    : [{ workflows: [{ path: ".github/workflows/sample.lock.yml" }] }]));
  process.exit(0);
}
const shard = args[args.indexOf("--cached-jsonl") + 1].replace(/\\*$/, "") + "fixture.jsonl";
const run = (repository, run_id) => ({ schema_version: 2, kind: "run", run: {
  repository, run_id, organization: repository.split("/")[0],
  workflow_name: "Sample", workflow_path: ".github/workflows/sample.lock.yml",
  status: "completed", created_at: new Date().toISOString()
} });
fs.writeFileSync(shard, [
  run("githubnext/gh-cao", 1),
  { kind: "github_api_rate_limit", rate_limit: { host: "github.com" } },
  run("other/repo", 2)
].map((record) => JSON.stringify(record)).join("\\n") + "\\n");
`);
  await chmod(ghPath, 0o755);
  const inventoryPath = path.join(item.root, "inventory-sources.json");
  await writeFile(inventoryPath, JSON.stringify({
    repositories: { rows: [], metadata: { completeness: "complete" } },
    workflows: { rows: [], metadata: { completeness: "complete" } },
  }));
  const collection = await execFileAsync("bash", [path.resolve("activity/collect-logs.sh")], {
    env: {
      ...process.env,
      PATH: `${item.bin}:${process.env.PATH}`,
      GITHUB_REPOSITORY: "githubnext/gh-cao",
      REPORT_TARGET_REPOSITORIES_JSON: JSON.stringify(["githubnext/gh-cao"]),
      REPORT_INCLUDE_CONTROL_REPOSITORY: "0",
      REPORT_GH_AW_LOGS_SHARDS: item.logsPath,
      REPORT_GH_AW_LOGS_EXIT_CODE: path.join(item.root, "cache", "gh-aw-logs-exit-code"),
      REPORT_AIC_CACHE: item.outputPath,
      REPORT_INVENTORY_SOURCES: inventoryPath,
      GITHUB_OUTPUT: item.githubOutput,
    },
  });
  assert.match(collection.stdout, /"excludedRecords": 2/);
  assert.match(collection.stdout, /Activity log collection: completed with exit code 0/);
  assert.match(collection.stdout, /::warning::Activity collection excluded 2/);
  assert.equal(await readFile(item.githubOutput, "utf8"), "evidence-partial=true\n");
  const inventory = JSON.parse(await readFile(inventoryPath, "utf8"));
  for (const name of ["repositories", "workflows"]) {
    assert.equal(inventory[name].metadata.completeness, "partial");
    assert.equal(inventory[name].metadata["repository-failures"][0].repository, "githubnext/gh-cao");
  }
  const runs = await readGhAwLogShards(item.logsPath);
  assert.deepEqual(runs.map((run) => run.repository), ["githubnext/gh-cao"]);
});

test("collect-logs.sh creates the drain3 weights destination directory before moving generated weights", async () => {
  const item = await fixture();
  const ghPath = path.join(item.bin, "gh");
  await writeFile(ghPath, `#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");
const args = process.argv.slice(2);
if (args[0] === "api" && args[1] === "rate_limit") {
  process.stdout.write(JSON.stringify({ resources: {
    core: { limit: 5000, remaining: 5000, reset: 1791406800 },
  } }));
  process.exit(0);
}
if (args[0] === "api") {
  process.stdout.write(JSON.stringify([{ workflows: [{ path: ".github/workflows/sample.lock.yml" }] }]));
  process.exit(0);
}
const output = args[args.indexOf("--output") + 1];
fs.mkdirSync(output, { recursive: true });
fs.writeFileSync(path.join(output, "drain3_weights.json"), "{}\\n");
process.exit(0);
`);
  await chmod(ghPath, 0o755);
  await writeFile(item.controlSettingsPath, JSON.stringify({
    allowed_repositories: ["githubnext/gh-cao"],
  }));
  const drain3WeightsPath = path.join(item.root, "runner-temp", "cao-activity", "drain3_weights.json");
  try {
    await execFileAsync("bash", [path.resolve("activity/collect-logs.sh")], {
      env: {
        ...process.env,
        PATH: `${item.bin}:${process.env.PATH}`,
        GITHUB_REPOSITORY: "githubnext/gh-cao",
        REPORT_ROOT: item.root,
        REPORT_GH_AW_LOGS_SHARDS: item.logsPath,
        REPORT_GH_AW_LOGS_STATE: item.statePath,
        REPORT_GH_AW_LOGS_EXIT_CODE: path.join(item.root, "cache", "gh-aw-logs-exit-code"),
        REPORT_AIC_CACHE: item.outputPath,
        REPORT_CONTROL_SETTINGS: item.controlSettingsPath,
        REPORT_DRAIN3_WEIGHTS: drain3WeightsPath,
        GITHUB_OUTPUT: item.githubOutput,
      },
    });
    assert.equal(await readFile(drain3WeightsPath, "utf8"), "{}\n");
  } finally {
    await rm(item.root, { recursive: true, force: true });
  }
});

test("collection stops before a repository download when the shared quota reaches its reserve", async (t) => {
  const item = await fixture();
  t.after(() => rm(item.root, { recursive: true, force: true }));
  const ghPath = path.join(item.bin, "gh");
  await writeFile(ghPath, `#!/usr/bin/env node
const fs = require("node:fs");
const args = process.argv.slice(2);
if (args[0] === "api" && args[1] === "rate_limit") {
  process.stdout.write(JSON.stringify({ resources: {
    core: { limit: 5000, remaining: 4000, reset: 1791406800 },
  } }));
  process.exit(0);
}
fs.writeFileSync(process.env.GH_ARGS_PATH, JSON.stringify(args));
process.exit(99);
`);
  await chmod(ghPath, 0o755);
  const exitCodePath = path.join(item.root, "cache", "gh-aw-logs-exit-code");
  const result = await execFileAsync("bash", [path.resolve("activity/collect-logs.sh")], {
    env: {
      ...process.env,
      PATH: `${item.bin}:${process.env.PATH}`,
      GITHUB_REPOSITORY: "githubnext/gh-cao",
      REPORT_GH_AW_LOGS_SHARDS: item.logsPath,
      REPORT_GH_AW_LOGS_EXIT_CODE: exitCodePath,
      REPORT_AIC_CACHE: item.outputPath,
      REPORT_MAX_GITHUB_API_RATE_LIMIT: "-4000",
      GH_ARGS_PATH: item.argumentsPath,
    },
  });
  assert.match(result.stderr, /capacity insufficient: 4000\/5000.*reset at/);
  assert.match(result.stderr, /API capacity check failed before githubnext\/gh-cao/);
  assert.match(result.stdout, /Activity log collection: completed with exit code 75/);
  assert.match(result.stdout, /Activity collection is partial/);
  assert.equal(await readFile(exitCodePath, "utf8"), "75\n");
  await assert.rejects(readFile(item.argumentsPath, "utf8"), { code: "ENOENT" });
});

test("warm collection rebuilds its scoped projection without inheriting the restored cross-owner database", async (t) => {
  const item = await fixture();
  t.after(() => rm(item.root, { recursive: true, force: true }));
  const cacheRoot = path.join(item.root, "cao-activity");
  const seedPath = path.join(cacheRoot, "gh-aw-logs-shards");
  const restoredDatabase = path.join(cacheRoot, "gh-aw-logs.sqlite");
  await mkdir(seedPath, { recursive: true });
  await writeFile(path.join(seedPath, "other-service-logs-fixture.jsonl"), `${JSON.stringify({
    schema_version: 2,
    kind: "run",
    run: {
      run_id: 999,
      repository: "other/service",
      organization: "other",
      workflow_name: "Sample",
      workflow_path: ".github/workflows/sample.lock.yml",
      created_at: new Date().toISOString(),
      status: "completed",
    },
  })}\n`);
  await execFileAsync(process.execPath, [
    "activity/cao.mjs", "ingest-jsonl", "--database", restoredDatabase, "--input-dir", seedPath,
  ]);
  const hash = (bytes) => createHash("sha256").update(bytes).digest("hex");
  const restoredHash = hash(await readFile(restoredDatabase));
  const ghPath = path.join(item.bin, "gh");
  await writeFile(ghPath, `#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");
const args = process.argv.slice(2);
if (args[0] === "api" && args[1] === "rate_limit") {
  process.stdout.write(JSON.stringify({ resources: {
    core: { limit: 5000, remaining: 5000, reset: 1791406800 },
  } }));
  process.exit(0);
}
if (args[0] === "api") {
  process.stdout.write(JSON.stringify([{ workflows: [{ path: ".github/workflows/sample.lock.yml" }] }]));
  process.exit(0);
}
const repository = args[args.indexOf("--repo") + 1];
const shard = args[args.indexOf("--cached-jsonl") + 1].replace(/\\*$/, "") + "fixture.jsonl";
fs.mkdirSync(path.dirname(shard), { recursive: true });
fs.writeFileSync(shard, JSON.stringify({ schema_version: 2, kind: "run", run: {
  run_id: 42, repository, organization: "githubnext",
  workflow_name: "Sample",
  workflow_path: ".github/workflows/sample.lock.yml",
  created_at: new Date().toISOString(), status: "completed",
} }) + "\\n");
`);
  await chmod(ghPath, 0o755);
  const workflow = parse(await readFile(".github/workflows/cao-activity.yml", "utf8"));
  const step = workflow.jobs.collect.steps.find((step) => step.name === "Download owner-scoped agentic workflow logs");
  const collectionDatabase = step.env.REPORT_ACTIVITY_DATABASE.replace("${{ runner.temp }}", item.root);
  await execFileAsync("bash", [path.resolve("activity/collect-logs.sh")], {
    env: {
      ...process.env,
      PATH: `${item.bin}:${process.env.PATH}`,
      GITHUB_REPOSITORY: "githubnext/gh-cao",
      REPORT_GH_AW_LOGS_SHARDS: item.logsPath,
      REPORT_GH_AW_LOGS_SEED_SHARDS: seedPath,
      REPORT_GH_AW_LOGS_EXIT_CODE: path.join(item.root, "collection-exit-code"),
      REPORT_AIC_CACHE: item.outputPath,
      REPORT_ACTIVITY_DATABASE: collectionDatabase,
      REPORT_DEFER_ISSUE_STATUS: "1",
    },
  });
  const query = await execFileAsync(process.execPath, [
    "activity/cao.mjs", "query", "--database", collectionDatabase, "--collection", "runs",
  ]);
  assert.deepEqual(JSON.parse(query.stdout).map((run) => run.githubRunId), ["42"]);
  const repositories = await execFileAsync(process.execPath, [
    "activity/cao.mjs", "query", "--database", collectionDatabase, "--collection", "repositories",
  ]);
  assert.deepEqual(JSON.parse(repositories.stdout).map(({ owner, name }) => `${owner}/${name}`), ["githubnext/gh-cao"]);
  assert.equal(hash(await readFile(restoredDatabase)), restoredHash);
});

test("activity logs preserves cached runs and records collection failure", async () => {
  const item = await fixture();
  const ghPath = path.join(item.bin, "gh");
  await writeFile(ghPath, "#!/usr/bin/env node\nprocess.stderr.write('download failed\\n');process.exit(1);\n");
  await chmod(ghPath, 0o755);
  await mkdir(item.logsPath, { recursive: true });
  await writeFile(path.join(item.logsPath, "fixture.jsonl"), '{"schema_version":2,"kind":"run","run":{"database_id":7}}\n');
  await writeFile(item.statePath, '{"observedAt":"2026-09-06T20:00:00Z","available":true}\n');
  await writeFile(path.join(item.root, "cache", "gh-aw-logs-exit-code"), "1\n");
  try {
    await execFileAsync(process.execPath, [path.resolve("activity/logs.mjs")], {
      env: {
        ...process.env,
        PATH: `${item.bin}:${process.env.PATH}`,
        GITHUB_REPOSITORY: "githubnext/gh-cao",
        REPORT_ROOT: item.root,
        REPORT_GH_AW_LOGS_SHARDS: item.logsPath,
        REPORT_GH_AW_LOGS_STATE: item.statePath,
        REPORT_GH_AW_LOGS_EXIT_CODE: path.join(item.root, "cache", "gh-aw-logs-exit-code"),
        REPORT_AIC_CACHE: item.outputPath,
        GITHUB_OUTPUT: item.githubOutput,
      },
    });

    test("activity logs reports rate-limited snapshots as available but incomplete", async (t) => {
      const item = await fixture();
      t.after(() => rm(item.root, { recursive: true, force: true }));
      await mkdir(item.logsPath, { recursive: true });
      await writeFile(path.join(item.logsPath, "fixture.jsonl"), '{"schema_version":2,"kind":"run","run":{"database_id":7}}\n');
      await writeFile(item.statePath, '{"observedAt":"2026-09-06T20:00:00Z","available":true}\n');
      const exitCodePath = path.join(item.root, "cache", "gh-aw-logs-exit-code");
      await writeFile(exitCodePath, "75\n");
      const result = await execFileAsync(process.execPath, [path.resolve("activity/logs.mjs")], {
        env: {
          ...process.env, GITHUB_REPOSITORY: "githubnext/gh-cao",
          REPORT_ROOT: item.root, REPORT_GH_AW_LOGS_SHARDS: item.logsPath,
          REPORT_GH_AW_LOGS_STATE: item.statePath, REPORT_GH_AW_LOGS_EXIT_CODE: exitCodePath,
          GITHUB_OUTPUT: item.githubOutput,
        },
      });
      const state = JSON.parse(await readFile(item.statePath, "utf8"));
      assert.equal(state.available, true);
      assert.equal(state.complete, false);
      assert.equal(state.runCount, 1);
      assert.equal(state.snapshotObservedAt, "2026-09-06T20:00:00Z");
      assert.match(state.error, /partial evidence/);
      assert.match(result.stdout, /partial collected snapshot/);
      assert.equal(await readFile(item.githubOutput, "utf8"), "collection-outcome=partial\n");
    });

    assert.equal((await readGhAwLogShards(item.logsPath))[0].database_id, 7);
    const state = JSON.parse(await readFile(item.statePath, "utf8"));
    assert.equal(state.available, false);
    assert.equal(state.fallback, true);
    assert.equal(state.snapshotObservedAt, "2026-09-06T20:00:00Z");
    assert.equal(await readFile(item.githubOutput, "utf8"), "collection-outcome=failure\n");
  } finally {
    await rm(item.root, { recursive: true, force: true });
  }
});

test("activity logs does not invoke the GitHub API when gh aw logs fails", async () => {
  const item = await fixture();
  const ghPath = path.join(item.bin, "gh");
  const invocationPath = path.join(item.root, "unexpected-gh-invocation");
  await writeFile(ghPath, `#!/usr/bin/env node
require("node:fs").writeFileSync(process.env.GH_INVOCATION_PATH, process.argv.slice(2).join(" "));
process.exit(99);
`);
  await chmod(ghPath, 0o755);
  await mkdir(item.logsPath, { recursive: true });
  await writeFile(path.join(item.logsPath, "fixture.jsonl"), '{"schema_version":2,"kind":"run","run":{"database_id":42,"failure_message":"cached detail"}}\n');
  await writeFile(item.statePath, '{"observedAt":"2026-09-06T20:00:00Z","available":true}\n');
  await writeFile(path.join(item.root, "cache", "gh-aw-logs-exit-code"), "1\n");
  try {
    await execFileAsync(process.execPath, [path.resolve("activity/logs.mjs")], {
      env: {
        ...process.env,
        PATH: `${item.bin}:${process.env.PATH}`,
        GITHUB_REPOSITORY: "githubnext/gh-cao",
        REPORT_ROOT: item.root,
        REPORT_GH_AW_LOGS_SHARDS: item.logsPath,
        REPORT_GH_AW_LOGS_STATE: item.statePath,
        REPORT_GH_AW_LOGS_EXIT_CODE: path.join(item.root, "cache", "gh-aw-logs-exit-code"),
        REPORT_AIC_CACHE: item.outputPath,
        GITHUB_OUTPUT: item.githubOutput,
        GH_INVOCATION_PATH: invocationPath,
      },
    });

    assert.equal((await readGhAwLogShards(item.logsPath))[0].failure_message, "cached detail");
    const state = JSON.parse(await readFile(item.statePath, "utf8"));
    assert.equal(state.available, false);
    assert.equal(state.complete, false);
    assert.equal(Object.hasOwn(state, "actionsEnrichment"), false);
    await assert.rejects(readFile(invocationPath), { code: "ENOENT" });
    assert.equal(await readFile(item.githubOutput, "utf8"), "collection-outcome=failure\n");
  } finally {
    await rm(item.root, { recursive: true, force: true });
  }
});

test("activity logs records a failure when workflow discovery is unavailable", async () => {
  const item = await fixture();
  await rm(path.join(item.root, ".github"), { recursive: true });
  await mkdir(item.logsPath, { recursive: true });
  await writeFile(path.join(item.logsPath, "fixture.jsonl"), '{"schema_version":2,"kind":"run","run":{"database_id":7}}\n');
  await writeFile(item.statePath, '{"observedAt":"2026-09-06T20:00:00Z","available":true}\n');
  await writeFile(path.join(item.root, "cache", "gh-aw-logs-exit-code"), "1\n");
  try {
    await execFileAsync(process.execPath, [path.resolve("activity/logs.mjs")], {
      env: {
        ...process.env,
        GITHUB_REPOSITORY: "githubnext/gh-cao",
        REPORT_ROOT: item.root,
        REPORT_GH_AW_LOGS_SHARDS: item.logsPath,
        REPORT_GH_AW_LOGS_STATE: item.statePath,
        REPORT_GH_AW_LOGS_EXIT_CODE: path.join(item.root, "cache", "gh-aw-logs-exit-code"),
        REPORT_AIC_CACHE: item.outputPath,
        GITHUB_OUTPUT: item.githubOutput,
      },
    });
    const state = JSON.parse(await readFile(item.statePath, "utf8"));
    assert.equal((await readGhAwLogShards(item.logsPath))[0].database_id, 7);
    assert.equal(state.available, false);
    assert.equal(state.targetCount, 0);
    assert.equal(state.fallback, true);
    assert.equal(await readFile(item.githubOutput, "utf8"), "collection-outcome=failure\n");
  } finally {
    await rm(item.root, { recursive: true, force: true });
  }
});
