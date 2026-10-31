#!/usr/bin/env node

import { execFile } from "node:child_process";
import { randomUUID } from "node:crypto";
import { copyFile, lstat, mkdir, readFile, readdir, rename, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";
import { writeAgentSummary } from "../activity/agent-summary.mjs";
import { buildInventoryDashboardSources } from "../activity/inventory-sources.mjs";
import { hashActivityPayloads } from "../activity/cli/payloads.mjs";
import { hashFileContents, jsonlRecords } from "../activity/cli/files.mjs";
import { NORMALIZED_COLLECTIONS } from "../activity/cli-usage.mjs";
import { buildDashboardSite, loadDashboardControlSettings } from "../dashboard/site/scripts/build.mjs";
import { generateDashboardLlms, validateDashboardAgentArtifacts } from "../dashboard/site/scripts/llms.mjs";

const execute = promisify(execFile);
const root = fileURLToPath(new URL("../", import.meta.url));
const marker = ".cao-pages-simulation.json";
const fixtureRepository = "example/pages-fixture";
export const PAGES_INGEST_TIMEOUT_MS = 15 * 60 * 1000;
const commandErrorTailChars = 8192;
const shardName = /^gh-aw-logs-(?:runs|records)\/[^/\\]+\.jsonl$/;
const usage = `Usage: node scripts/simulate-dashboard-pages.mjs (--activity-dir DIR | --fixture) [--output DIR]

Prepare a static Pages deployment; does not start a server or contact GitHub.
Default output: .tmp/dashboard-pages (output must be below this checkout's .tmp/).
Existing output is replaced only when it carries this tool's ownership marker.
--activity-dir accepts an Activity snapshot with control-settings.json,
inventory-sources.json and raw gh-aw-logs-shards/ or manifest-listed phased shards.
--fixture explicitly generates synthetic, labelled evidence for offline development.
Synthetic evidence is dated at invocation time to survive normal browser retention;
supplied snapshot timestamps are never refreshed or otherwise rewritten.

Uses production Activity normalization/SQLite ingestion and Dashboard site building.
Retains all supplied evidence; does not simulate collection, Actions permissions,
caches/artifact transport, GitHub Pages deployment, TLS, or custom domains.
The Actions runtime test simulator only accepts compiled dry-run agentic locks and
does not implement the cache/Pages actions required by these ordinary workflows.
`;

export function parsePagesArguments(args) {
  const result = {};
  for (let index = 0; index < args.length; index++) {
    const name = args[index];
    if (name === "--help") return { help: true };
    if (name === "--fixture") {
      if (result.fixture) throw new Error("Duplicate --fixture");
      result.fixture = true;
    } else if (name === "--activity-dir" || name === "--output") {
      const key = name === "--activity-dir" ? "activityDir" : "output";
      if (result[key] || !args[index + 1] || args[index + 1].startsWith("--")) {
        throw new Error(`${name} requires one directory`);
      }
      result[key] = args[++index];
    } else {
      throw new Error(`Unknown option: ${name}`);
    }
  }
  if (Boolean(result.activityDir) === Boolean(result.fixture)) {
    throw new Error("Choose exactly one of --activity-dir DIR or --fixture; no implicit collection or fallback");
  }
  return result;
}

async function exists(file) {
  try { await lstat(file); return true; }
  catch (error) { if (error.code === "ENOENT") return false; throw error; }
}

export async function assertSafePagesOutput(output) {
  const destination = path.resolve(output);
  const scratch = path.join(root, ".tmp");
  if (!destination.startsWith(`${scratch}${path.sep}`)) {
    throw new Error("Pages output must be a dedicated directory below this checkout's .tmp/");
  }
  let current = root;
  for (const part of path.relative(root, destination).split(path.sep)) {
    current = path.join(current, part);
    if (await exists(current)) {
      if (!(await lstat(current)).isDirectory()) {
        throw new Error(`Pages output must not contain symlinks or non-directories: ${current}`);
      }
    }
  }
  if (await exists(destination)) {
    const names = await readdir(destination);
    if (names.length > 0) {
      const ownership = path.join(destination, marker);
      if (!await exists(ownership) || !(await lstat(ownership)).isFile()
          || JSON.parse(await readFile(ownership, "utf8")).tool !== "simulate-dashboard-pages") {
        throw new Error("Refusing to replace an existing directory not owned by the Pages simulator");
      }
    }
  }
  return destination;
}

async function copyTree(source, destination) {
  const info = await lstat(source);
  if (info.isFile()) {
    await mkdir(path.dirname(destination), { recursive: true });
    await copyFile(source, destination);
  } else if (info.isDirectory()) {
    await mkdir(destination, { recursive: true });
    for (const name of await readdir(source)) {
      await copyTree(path.join(source, name), path.join(destination, name));
    }
  } else {
    throw new Error(`Snapshot must contain only regular files and directories: ${source}`);
  }
}

async function json(file, value) {
  await writeFile(file, `${JSON.stringify(value, null, 2)}\n`);
}

async function writeFixture(directory, fixtureTime, experimental) {
  const settings = { campaigns: {}, web: { experimental }, scope: { "allowed-repositories": [fixtureRepository] } };
  const sources = buildInventoryDashboardSources({
    inventory: { workflows: [{
      id: "pages-fixture", name: "Synthetic Pages fixture", role: "standalone",
      sourcePath: ".github/workflows/pages-fixture.md", compiled: true,
    }] },
    controlSettings: settings,
    repository: fixtureRepository,
    generatedAt: fixtureTime,
  });
  for (const source of Object.values(sources)) {
    source.metadata["collection-operation"] = "synthetic-pages-fixture";
    source.metadata["collection-reason"] = "Explicit synthetic fixture; not collected GitHub evidence.";
  }
  const run = {
    run_id: 1, run_attempt: "1", organization: "example", repository: fixtureRepository,
    workflow_name: "Synthetic Pages fixture", workflow_path: ".github/workflows/pages-fixture.md",
    display_title: "Synthetic Pages fixture (not real activity)", event: "workflow_dispatch",
    status: "completed", conclusion: "success", classification: "success",
    created_at: fixtureTime, started_at: fixtureTime, updated_at: fixtureTime,
    token_usage_summary: { total_aic: 2.5 },
    mcp_tool_usage: { tool_calls: [{
      tool_call_id: "synthetic-read", server_name: "synthetic-fixture", tool_name: "read",
      timestamp: fixtureTime, status: "success", input_size: 10, output_size: 20,
    }] },
  };
  await json(path.join(directory, "control-settings.json"), settings);
  await json(path.join(directory, "inventory-sources.json"), sources);
  await mkdir(path.join(directory, "gh-aw-logs-shards"));
  await writeFile(path.join(directory, "gh-aw-logs-shards/fixture.jsonl"),
    `${JSON.stringify({ schema_version: 2, kind: "run", run })}\n`);
  await mkdir(path.join(directory, "memory"));
  await json(path.join(directory, "memory/manifest.json"), {
    version: 1, generatedAt: fixtureTime, campaigns: [],
  });
}

async function digest(file) {
  return hashFileContents(file);
}

async function validatePhasedShard(file, phase) {
  let header;
  let records = 0;
  for (const { value } of jsonlRecords([file])) {
    if (!header) {
      if (value?.kind !== "metadata" || value.phase !== phase
          || !Number.isSafeInteger(value.records) || value.records < 0
          || !Number.isSafeInteger(value.schemaVersion) || !Number.isSafeInteger(value.ingestionVersion)) {
        throw new Error(`Invalid normalized ${phase} shard metadata: ${path.basename(file)}`);
      }
      header = value;
    } else {
      if (value?.kind !== "record" || !NORMALIZED_COLLECTIONS.includes(value.collection)
          || !value.record || typeof value.record !== "object" || Array.isArray(value.record)
          || typeof value.record.id !== "string" || !value.record.id) {
        throw new Error(`Invalid normalized ${phase} shard record: ${path.basename(file)}`);
      }
      records += 1;
    }
  }
  if (!header || records !== header.records) {
    throw new Error(`Invalid normalized ${phase} shard record count: ${path.basename(file)}`);
  }
}

async function copySnapshot(source, destination, { includeSqlite }) {
  if (!(await lstat(source)).isDirectory()) throw new Error("Activity source must be a directory, not a symlink");
  for (const name of ["control-settings.json", "inventory-sources.json"]) {
    await copyTree(path.join(source, name), path.join(destination, name));
  }
  const raw = path.join(source, "gh-aw-logs-shards");
  const hashes = {};
  if (await exists(raw)) {
    if (!includeSqlite) throw new Error("includeSqlite: false requires already normalized phased data, not raw Activity shards");
    if (!(await lstat(raw)).isDirectory()) throw new Error("Activity raw shards must be a directory, not a symlink");
    const names = (await readdir(raw)).filter(name => name.endsWith(".jsonl"));
    if (!names.length) throw new Error("Activity snapshot raw shard directory is empty");
    await mkdir(path.join(destination, "gh-aw-logs-shards"));
    for (const name of names) await copyTree(path.join(raw, name), path.join(destination, "gh-aw-logs-shards", name));
  } else {
    const manifest = JSON.parse(await readFile(path.join(source, "payload-hashes.json"), "utf8"));
    const names = Object.keys(manifest).filter(name => shardName.test(name));
    for (const phase of ["runs", "records"]) {
      if (!(await lstat(path.join(source, `gh-aw-logs-${phase}`))).isDirectory()) {
        throw new Error("Activity phased shards must be directories, not symlinks");
      }
      if (!names.some(name => name.startsWith(`gh-aw-logs-${phase}/`))) {
        throw new Error(`Activity snapshot contains no manifest-listed ${phase} shards`);
      }
    }
    for (const name of names) {
      const file = path.join(source, name);
      await copyTree(file, path.join(destination, name));
      if (await digest(path.join(destination, name)) !== manifest[name]) {
        throw new Error(`Activity snapshot checksum mismatch: ${name}`);
      }
      if (!includeSqlite) {
        await validatePhasedShard(path.join(destination, name), name.startsWith("gh-aw-logs-runs/") ? "runs" : "records");
      }
      hashes[name] = manifest[name];
    }
  }
  const memory = path.join(source, "memory");
  if (await exists(memory)) await copyTree(memory, path.join(destination, "memory"));
  return hashes;
}

// Mirrors the publish allowlist in cao-dashboard.yml; raw logs and .payloads
// normalization caches must never become public Pages assets.
async function publishPayloads(activity, destination, hashes, { includeSqlite }) {
  const published = Object.fromEntries(Object.entries(hashes).filter(([name]) =>
    name === "gh-aw-logs.sqlite" || shardName.test(name)));
  if (!Object.keys(published).some(name => name.startsWith("gh-aw-logs-runs/"))) {
    throw new Error("Dashboard payload contains no compacted run-information shards");
  }
  for (const name of ["inventory-sources.json", ...(includeSqlite ? ["agent-summary.json"] : []), ...Object.keys(published)]) {
    await copyTree(path.join(activity, name), path.join(destination, name));
  }
  for (const name of includeSqlite ? ["gh-aw-logs.sqlite", "agent-summary.json"] : []) {
    published[name] = await digest(path.join(destination, name));
  }
  if (await exists(path.join(activity, "memory"))) {
    await copyTree(path.join(activity, "memory"), path.join(destination, "memory"));
  }
  await json(path.join(destination, "payload-hashes.json"), published);
  await generateDashboardLlms({ outputPath: path.join(destination, "llms.txt"), activityDataPath: destination });
  // The complete agent-artifact contract requires SQLite and a summary; an
  // explicit browser-only preview intentionally does not provide those artifacts.
  if (includeSqlite) await validateDashboardAgentArtifacts({ sitePath: destination });
}

export async function runPagesPreparationCommand(stage, args, { timeoutMs = PAGES_INGEST_TIMEOUT_MS } = {}) {
  try {
    return await execute(process.execPath, args, {
      cwd: root, timeout: timeoutMs, maxBuffer: 16 * 1024 * 1024,
    });
  } catch (error) {
    const timedOut = error.killed === true && error.signal != null && error.code == null;
    const stderr = String(error.stderr ?? "").slice(-commandErrorTailChars).trim() || "(no stderr)";
    throw Object.assign(new Error(
      `Pages ${stage} failed (timeout=${timedOut}; timeoutMs=${timeoutMs}; `
      + `signal=${error.signal ?? "none"}; code=${error.code ?? "none"}; killed=${error.killed === true}). `
      + `stderr (last ${commandErrorTailChars} characters): ${stderr}`,
    ), { code: error.code, signal: error.signal, timedOut, timeoutMs });
  }
}

export async function prepareDashboardPages({
  activityDir, fixture = false, output = path.join(root, ".tmp/dashboard-pages"), fixtureNow = Date.now(), includeSqlite = true,
  fixtureExperimental = false,
} = {}) {
  if (Boolean(activityDir) === Boolean(fixture)) {
    throw new Error("Choose exactly one of --activity-dir DIR or --fixture; no implicit collection or fallback");
  }
  if (typeof includeSqlite !== "boolean") throw new Error("includeSqlite must be a boolean");
  if (typeof fixtureExperimental !== "boolean" || fixtureExperimental && !fixture) {
    throw new Error("fixtureExperimental must be a boolean and can only enable synthetic fixture views");
  }
  if (!includeSqlite && fixture) throw new Error("includeSqlite: false requires supplied normalized phased data, not a synthetic fixture");
  const destination = await assertSafePagesOutput(output);
  const source = activityDir && path.resolve(activityDir);
  if (source && (source === destination || source.startsWith(`${destination}${path.sep}`)
      || destination.startsWith(`${source}${path.sep}`))) {
    throw new Error("Activity source and Pages output must not overlap");
  }
  const staging = `${destination}.prepare-${randomUUID()}`;
  const activity = path.join(staging, "activity");
  const site = path.join(staging, "site");
  await mkdir(activity, { recursive: true });
  try {
    const fixtureTime = fixture ? new Date(fixtureNow).toISOString() : undefined;
    let hashes;
    if (fixture) await writeFixture(activity, fixtureTime, fixtureExperimental);
    else hashes = await copySnapshot(source, activity, { includeSqlite });
    const inventoryPath = path.join(activity, "inventory-sources.json");
    const settings = await loadDashboardControlSettings(path.join(activity, "control-settings.json"));
    const sources = JSON.parse(await readFile(inventoryPath, "utf8"));
    for (const name of ["campaigns", "workflows", "repositories"]) {
      if (!Array.isArray(sources[name]?.rows)) throw new Error(`Invalid Activity inventory: ${name}.rows`);
    }
    const runsDirectory = path.join(activity, "gh-aw-logs-runs");
    const recordsDirectory = path.join(activity, "gh-aw-logs-records");
    const raw = path.join(activity, "gh-aw-logs-shards");
    const normalization = { runsDirectory, recordsDirectory, inventoryPath };
    const hasRaw = await exists(raw);
    if (includeSqlite) {
      if (hasRaw) await hashActivityPayloads({ ...normalization, shardDirectory: raw });
      const databasePath = path.join(activity, "gh-aw-logs.sqlite");
      await runPagesPreparationCommand("canonical SQLite ingestion", [
        path.join(root, "activity/cao.mjs"), "ingest-jsonl", "--database", databasePath,
        "--runs-dir", runsDirectory, "--records-dir", recordsDirectory,
        "--retention-days", "all", "--run-retention-days", "all",
      ]);
      hashes = await hashActivityPayloads({
        ...normalization, databasePath, ...(hasRaw ? { shardDirectory: raw } : {}),
      });
      if (!hasRaw) {
        for (const directory of [runsDirectory, recordsDirectory]) {
          for (const name of await readdir(directory)) {
            if (name.endsWith(".jsonl")) hashes[`${path.basename(directory)}/${name}`] = await digest(path.join(directory, name));
          }
        }
      }
      await writeAgentSummary(inventoryPath, path.join(activity, "agent-summary.json"));
    }
    await json(path.join(activity, "payload-hashes.json"), hashes);
    await buildDashboardSite({
      destination: site, controlSettings: settings, activityDataPath: activity,
      githubRepository: fixture ? fixtureRepository : undefined,
    });
    await publishPayloads(activity, site, hashes, { includeSqlite });
    await json(path.join(site, marker), {
      tool: "simulate-dashboard-pages", version: 1, mode: fixture ? "fixture" : "snapshot",
      synthetic: fixture, retention: "all",
      includeSqlite,
      ...(fixture ? { fixtureGeneratedAt: fixtureTime } : {}),
      limits: "Offline production preparation only; no collection, Actions services, or remote Pages deployment.",
    });
    await assertSafePagesOutput(destination);
    await rm(destination, { recursive: true, force: true });
    await rename(site, destination);
    return { output: destination, mode: fixture ? "fixture" : "snapshot", includeSqlite, payloads: Object.keys(hashes).length };
  } finally {
    await rm(staging, { recursive: true, force: true });
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  try {
    const options = parsePagesArguments(process.argv.slice(2));
    if (options.help) process.stdout.write(usage);
    else process.stdout.write(`${JSON.stringify(await prepareDashboardPages(options), null, 2)}\n`);
  } catch (error) {
    process.stderr.write(`Pages preparation failed: ${error.message}\n`);
    process.exitCode = 1;
  }
}
