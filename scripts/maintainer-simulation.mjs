#!/usr/bin/env node

import { execFile, spawn } from "node:child_process";
import { once } from "node:events";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { buildInventoryDashboardSources } from "../activity/inventory-sources.mjs";
import { hashActivityPayloads } from "../activity/cli/payloads.mjs";
import { buildDashboardSite } from "../dashboard/site/scripts/build.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
const directory = path.join(root, ".tmp/maintainer-simulation");
const binary = path.join(directory, "cao-dashboard");
const site = path.join(directory, "site");
const apiOrigin = "http://127.0.0.1:18081";
const dashboardOrigin = "http://127.0.0.1:18443";
// This public, test-only value has no authority outside the loopback fixture.
const accessToken = "maintainer-simulation-local-only-0001";

async function json(file, value) {
  await writeFile(file, `${JSON.stringify(value, null, 2)}\n`);
}

function start(args) {
  const child = spawn(binary, args, {
    cwd: path.join(root, "server"),
    stdio: ["ignore", "inherit", "inherit"],
    env: {
      ...process.env,
      GH_TOKEN: "", GITHUB_TOKEN: "", GH_AW_GITHUB_TOKEN: "", GH_AW_GITHUB_READ_PAT: "",
      CAO_POSTGRES_URL: "", REDIS_URL: "", CAO_MARKETPLACE_POLICY_PATH: "",
    },
  });
  const exited = once(child, "exit");
  // Attach immediately: a missing binary must not become an unhandled rejection.
  exited.catch(() => {});
  return { child, exited };
}

async function stop({ child, exited }) {
  if (child.exitCode !== null || child.signalCode !== null || !child.pid) return;
  child.kill("SIGTERM");
  const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
  try { await exited; }
  finally { clearTimeout(timer); }
}

async function ready(url, process) {
  const deadline = Date.now() + 60_000;
  while (Date.now() < deadline) {
    if (!process.child.pid || process.child.exitCode !== null || process.child.signalCode !== null) {
      throw new Error(`Simulation process exited before readiness: ${url}`);
    }
    try {
      const response = await fetch(url, { signal: AbortSignal.timeout(1000), redirect: "error" });
      if (response.ok) return;
    } catch (error) {
      if (!(error instanceof TypeError) && error.name !== "TimeoutError") throw error;
    }
    await new Promise(resolve => setTimeout(resolve, 200));
  }
  throw new Error(`Simulation readiness timed out: ${url}`);
}

async function apiJSON(route) {
  const response = await fetch(`${apiOrigin}${route}`, {
    signal: AbortSignal.timeout(5000), redirect: "error",
  });
  if (!response.ok) throw new Error(`Simulated GitHub request failed: ${route} (${response.status})`);
  return response.json();
}

export function simulationRun(repository, run, index) {
  const workflow = index % 2 === 0 ? "dependency-review" : "repository-status";
  return {
    run_id: run.id, run_attempt: String(run.run_attempt), organization: "simulator",
    repository, workflow_name: `Synthetic ${workflow}`,
    workflow_path: `.github/workflows/${workflow}.md`,
    display_title: `SIMULATION: ${workflow} #${run.id}`,
    event: "workflow_dispatch", status: run.status, conclusion: run.conclusion,
    classification: run.conclusion,
    created_at: run.created_at, started_at: run.created_at, updated_at: run.created_at,
    token_usage_summary: { total_aic: (index + 1) / 2 },
    mcp_tool_usage: { tool_calls: [{
      tool_call_id: `synthetic-read-${run.id}`, server_name: "github",
      tool_name: "get_file_contents", timestamp: run.created_at,
      status: "success", input_size: 32, output_size: 128,
    }] },
  };
}

async function prepare() {
  await mkdir(directory, { recursive: true });
  const timestamp = new Date(Date.now() - 3_600_000).toISOString();
  await json(path.join(directory, "github-scenario.json"), {
    name: "Synthetic maintainer journeys (not live GitHub evidence)",
    repositories: 3, seed: 42, events_per_repository: 1,
    history: { days: 3, runs_per_day: 4, as_of: timestamp },
  });
  const api = start(["simulate-api", "--scenario", path.join(directory, "github-scenario.json"),
    "--listen", "127.0.0.1:18081"]);
  try {
    await ready(`${apiOrigin}/healthz`, api);
    const { repositories } = await apiJSON("/installation/repositories?per_page=100");
    if (!Array.isArray(repositories) || repositories.length !== 3) {
      throw new Error("Simulated GitHub must provide exactly three repositories");
    }
    const records = [];
    for (const repository of repositories) {
      const payload = await apiJSON(`/repos/${repository.full_name}/actions/runs?per_page=100`);
      if (payload.workflow_runs?.length !== 12) throw new Error("Incomplete simulated GitHub run history");
      records.push(...payload.workflow_runs.map((run, index) => ({
        schema_version: 2, kind: "run", run: simulationRun(repository.full_name, run, index),
      })));
    }
    const settings = {
      campaigns: {}, web: {},
      scope: { "allowed-repositories": repositories.map(repository => repository.full_name) },
    };
    const inventory = buildInventoryDashboardSources({
      inventory: { workflows: ["dependency-review", "repository-status"].map(id => ({
        id, name: `Synthetic ${id}`, role: "standalone",
        sourcePath: `.github/workflows/${id}.md`, compiled: true,
      })) },
      controlSettings: settings, repository: repositories[0].full_name, generatedAt: timestamp,
    });
    for (const source of Object.values(inventory)) {
      source.metadata["collection-operation"] = "synthetic-maintainer-simulation";
      source.metadata["collection-reason"] = "Synthetic simulator history; costs and tool calls are test fixtures.";
    }
    const activity = path.join(directory, "activity");
    await mkdir(path.join(activity, "gh-aw-logs-shards"), { recursive: true });
    await json(path.join(activity, "control-settings.json"), settings);
    await json(path.join(activity, "inventory-sources.json"), inventory);
    await writeFile(path.join(activity, "gh-aw-logs-shards/simulation.jsonl"),
      `${records.map(record => JSON.stringify(record)).join("\n")}\n`);
    await mkdir(path.join(activity, "memory"), { recursive: true });
    await json(path.join(activity, "memory/manifest.json"), { version: 1, generatedAt: timestamp, campaigns: [] });
    const runsDirectory = path.join(activity, "gh-aw-logs-runs");
    const recordsDirectory = path.join(activity, "gh-aw-logs-records");
    await hashActivityPayloads({
      shardDirectory: path.join(activity, "gh-aw-logs-shards"), runsDirectory, recordsDirectory,
      inventoryPath: path.join(activity, "inventory-sources.json"),
    });
    await buildDashboardSite({ destination: site, controlSettings: settings, githubRepository: repositories[0].full_name });
    await promisify(execFile)(process.execPath, [
      path.join(root, "activity/cao.mjs"), "ingest-jsonl", "--database", path.join(site, "gh-aw-logs.sqlite"),
      "--runs-dir", runsDirectory, "--records-dir", recordsDirectory,
      "--retention-days", "all", "--run-retention-days", "all",
    ], { cwd: root, timeout: 120_000 });
    await json(path.join(directory, "environment.json"), {
      simulation: true, backend: "local-sqlite", dashboardOrigin, apiOrigin,
      browserURL: `${dashboardOrigin}/?access_token=${accessToken}`,
      repositories: repositories.map(repository => repository.full_name),
      runs: records.length, generatedAt: timestamp,
      limitations: [
        "All GitHub runs are successful synthetic history; costs and tool calls are fixture decorations.",
        "No live GitHub writes, OAuth, hosted PostgreSQL, collection, dispatch or Actions execution is exercised.",
        "The local dashboard reads a production-normalized SQLite snapshot, not a live simulator subscription.",
      ],
    });
  } finally {
    await stop(api);
  }
}

async function serve() {
  await readFile(path.join(directory, "environment.json"), "utf8");
  const api = start(["simulate-api", "--scenario", path.join(directory, "github-scenario.json"),
    "--listen", "127.0.0.1:18081"]);
  const dashboard = start(["serve", "--sqlite", path.join(site, "gh-aw-logs.sqlite"),
    "--site", site, "--listen", "127.0.0.1:18443", "--access-token", accessToken]);
  const shutdown = new AbortController();
  const onSignal = () => shutdown.abort();
  process.once("SIGINT", onSignal);
  process.once("SIGTERM", onSignal);
  try {
    await Promise.all([ready(`${apiOrigin}/healthz`, api), ready(`${dashboardOrigin}/api/v1/health`, dashboard)]);
    if (shutdown.signal.aborted) return;
    console.log("SIMULATION READY: local dashboard and synthetic GitHub API; see environment.json");
    await Promise.race([
      once(shutdown.signal, "abort").then(() => undefined),
      api.exited.then(() => { throw new Error("Simulated GitHub stopped unexpectedly"); }),
      dashboard.exited.then(() => { throw new Error("Local dashboard stopped unexpectedly"); }),
    ]);
  } finally {
    await Promise.all([stop(api), stop(dashboard)]);
    process.removeListener("SIGINT", onSignal);
    process.removeListener("SIGTERM", onSignal);
  }
}

export function validateResults(report, plan) {
  if (report?.simulation !== true || !Array.isArray(report.scenarios) || report.scenarios.length !== 10) {
    throw new Error("A simulation report must contain exactly 10 scenarios");
  }
  const nonempty = value => typeof value === "string" && value.trim().length > 0;
  const ids = new Set();
  for (const scenario of report.scenarios) {
    if (!/^S(?:0[1-9]|10)$/.test(scenario.id) || ids.has(scenario.id)) {
      throw new Error("Scenario IDs must be unique S01 through S10");
    }
    ids.add(scenario.id);
    for (const field of ["persona", "goal", "preconditions", "acceptance", "actual"]) {
      if (!nonempty(scenario[field])) throw new Error(`${scenario.id}: missing ${field}`);
    }
    if (!["passed", "failed", "blocked"].includes(scenario.status)) throw new Error(`${scenario.id}: invalid status`);
    for (const field of ["steps", "evidence"]) {
      if (!Array.isArray(scenario[field]) || !scenario[field].length || !scenario[field].every(nonempty)) {
        throw new Error(`${scenario.id}: ${field} must contain observations or artifact paths`);
      }
    }
    if (scenario.status === "failed" && (!nonempty(scenario.reproduction) || !nonempty(scenario.potentialFix))) {
      throw new Error(`${scenario.id}: a failure requires reproduction and a potential fix`);
    }
    if (scenario.status === "blocked" && !nonempty(scenario.blocker)) throw new Error(`${scenario.id}: missing blocker`);
  }
  if (plan !== undefined) {
    if (!Array.isArray(plan?.scenarios) || plan.scenarios.length !== 10
      || new Set(plan.scenarios.map(scenario => scenario.id)).size !== 10) {
      throw new Error("The frozen plan must contain ten distinct scenarios");
    }
    for (const scenario of report.scenarios) {
      const planned = plan.scenarios.find(candidate => candidate.id === scenario.id);
      if (!planned || ["persona", "goal", "preconditions", "acceptance"].some(field =>
        planned[field] !== scenario[field])) {
        throw new Error(`${scenario.id}: outcome differs from the frozen plan`);
      }
    }
  }
  return {
    simulation: true, scenarios: 10,
    ...Object.fromEntries(["passed", "failed", "blocked"].map(status =>
      [status, report.scenarios.filter(scenario => scenario.status === status).length])),
  };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const command = process.argv[2];
    if (command === "prepare") await prepare();
    else if (command === "serve") await serve();
    else if (command === "validate" && process.argv[3] && process.argv[4]) {
      console.log(JSON.stringify(validateResults(
        JSON.parse(await readFile(process.argv[3], "utf8")),
        JSON.parse(await readFile(process.argv[4], "utf8")),
      )));
    } else throw new Error("Usage: node scripts/maintainer-simulation.mjs prepare|serve|validate REPORT.json PLAN.json");
  } catch (error) {
    console.error(`Maintainer simulation failed: ${error.message}`);
    process.exitCode = 1;
  }
}
