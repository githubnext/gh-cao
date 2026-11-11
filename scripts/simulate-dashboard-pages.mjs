#!/usr/bin/env node

import { randomUUID } from "node:crypto";
import { copyFile, lstat, mkdir, readFile, readdir, rename, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { buildInventoryDashboardSources } from "../activity/inventory-sources.mjs";
import { hashActivityPayloads } from "../activity/cli/payloads.mjs";
import { buildDashboardSite } from "../dashboard/site/scripts/build.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
const marker = ".cao-pages-demo.json";
const organization = "sample-enterprise";
const departments = ["commerce", "platform", "analytics", "workplace"];
const services = ["api", "web", "infrastructure"];
const workflows = [
  { id: "dependency-review", name: "Dependency review" },
  { id: "repository-assistant", name: "Repository assistant" },
  { id: "security-review", name: "Security review" },
  { id: "documentation", name: "Documentation maintenance" },
];
const usage = `Usage: node scripts/simulate-dashboard-pages.mjs [--output DIR]

Build the synthetic enterprise Pages demo, without network access or real inputs.
Default output: .tmp/dashboard-pages (must be below this checkout's .tmp/).
Uses production Activity normalization, IndexedDB ingestion and Dashboard Language.
Does not publish SQLite, operational agent evidence, credentials or live services.
`;

export function parsePagesArguments(args) {
  const result = {};
  for (let index = 0; index < args.length; index++) {
    const name = args[index];
    if (name === "--help") return { help: true };
    if (name !== "--output") throw new Error(`Unknown option: ${name}; Pages accepts synthetic data only`);
    if (result.output || !args[index + 1] || args[index + 1].startsWith("--")) {
      throw new Error("--output requires one directory");
    }
    result.output = args[++index];
  }
  return result;
}

async function exists(file) {
  try { await lstat(file); return true; }
  catch (error) { if (error.code === "ENOENT") return false; throw error; }
}

export async function assertSafePagesOutput(output) {
  const destination = path.resolve(output);
  if (!destination.startsWith(`${path.join(root, ".tmp")}${path.sep}`)) {
    throw new Error("Pages output must be a dedicated directory below this checkout's .tmp/");
  }
  let current = root;
  for (const part of path.relative(root, destination).split(path.sep)) {
    current = path.join(current, part);
    if (await exists(current) && !(await lstat(current)).isDirectory()) {
      throw new Error(`Pages output must not contain symlinks or non-directories: ${current}`);
    }
  }
  if (await exists(destination) && (await readdir(destination)).length) {
    const ownership = path.join(destination, marker);
    if (!await exists(ownership) || !(await lstat(ownership)).isFile()
        || JSON.parse(await readFile(ownership, "utf8")).tool !== "cao-pages-demo") {
      throw new Error("Refusing to replace an existing directory not owned by the Pages demo builder");
    }
  }
  return destination;
}

async function json(file, value) {
  await writeFile(file, `${JSON.stringify(value, null, 2)}\n`);
}

async function writeEnterpriseSample(directory, now) {
  const generatedAt = new Date(now).toISOString();
  const sources = {};
  const records = [];
  let runId = 0;
  for (const department of departments) {
    for (const service of services) {
      const repository = `${organization}/${department}-${service}`;
      const inventory = buildInventoryDashboardSources({
        inventory: { workflows: workflows.map(({ id, name }) => ({
          id, name, role: "standalone", sourcePath: `.github/workflows/${id}.md`, compiled: true,
        })) },
        controlSettings: { campaigns: {} }, repository, generatedAt,
      });
      for (const [name, source] of Object.entries(inventory)) {
        sources[name] ??= { ...source, rows: [], metadata: {
          ...source.metadata, "source-kind": "synthetic",
          "collection-operation": "synthetic-enterprise-demo",
          "collection-reason": "Generated sample enterprise; not observed GitHub activity.",
        } };
        sources[name].rows.push(...source.rows);
      }
      for (let day = 0; day < 14; day++) {
        for (const [index, workflow] of workflows.entries()) {
          runId++;
          const startedAt = new Date(now - day * 86_400_000 - (index + 1) * 3_600_000).toISOString();
          const completedAt = new Date(Date.parse(startedAt) + (45 + runId % 240) * 1_000).toISOString();
          const failed = runId % 11 === 0;
          records.push({ schema_version: 2, kind: "run", run: {
            run_id: runId, run_attempt: "1", organization, repository,
            workflow_name: workflow.name, workflow_path: `.github/workflows/${workflow.id}.md`,
            display_title: `Synthetic ${workflow.name} for ${department} ${service}`,
            event: index % 2 ? "issues" : "schedule", status: "completed",
            conclusion: failed ? "failure" : "success", classification: failed ? "failure" : "success",
            created_at: startedAt, started_at: startedAt, updated_at: completedAt,
            token_usage_summary: { total_aic: 0.5 + runId % 30 / 4 },
            mcp_tool_usage: { tool_calls: [{
              tool_call_id: `sample-read-${runId}`, server_name: "github", tool_name: "get_file_contents",
              timestamp: startedAt, status: "success", input_size: 100 + runId % 200, output_size: 900 + runId % 500,
            }] },
          } });
        }
      }
    }
  }
  await json(path.join(directory, "inventory-sources.json"), sources);
  await mkdir(path.join(directory, "gh-aw-logs-shards"));
  await writeFile(path.join(directory, "gh-aw-logs-shards/sample.jsonl"),
    records.map(record => JSON.stringify(record)).join("\n") + "\n");
  return generatedAt;
}

export async function prepareDashboardPages({
  output = path.join(root, ".tmp/dashboard-pages"), now = Date.now(), experimental = false, ...unsupported
} = {}) {
  if (Object.keys(unsupported).length) throw new Error("Pages accepts synthetic data only; unsupported preparation options");
  if (!Number.isFinite(now) || typeof experimental !== "boolean") throw new Error("Invalid demo build options");
  const destination = await assertSafePagesOutput(output);
  const staging = `${destination}.prepare-${randomUUID()}`;
  const activity = path.join(staging, "activity");
  const site = path.join(staging, "site");
  await mkdir(activity, { recursive: true });
  try {
    const generatedAt = await writeEnterpriseSample(activity, now);
    const hashes = await hashActivityPayloads({
      shardDirectory: path.join(activity, "gh-aw-logs-shards"),
      runsDirectory: path.join(activity, "gh-aw-logs-runs"),
      recordsDirectory: path.join(activity, "gh-aw-logs-records"),
      inventoryPath: path.join(activity, "inventory-sources.json"),
    });
    await buildDashboardSite({
      destination: site, controlSettings: { campaigns: {}, web: { experimental } },
      githubRepository: `${organization}/platform-api`, githubServerUrl: "https://example.invalid",
      profile: "demo",
    });
    const published = Object.fromEntries(Object.entries(hashes)
      .filter(([name]) => /^gh-aw-logs-(?:runs|records)\/[^/\\]+\.jsonl$/.test(name)));
    if (!Object.keys(published).some(name => name.startsWith("gh-aw-logs-runs/"))) {
      throw new Error("Synthetic normalization produced no run shards");
    }
    const data = path.join(site, "demo-data-v1");
    for (const name of ["inventory-sources.json", ...Object.keys(published)]) {
      await mkdir(path.dirname(path.join(data, name)), { recursive: true });
      await copyFile(path.join(activity, name), path.join(data, name));
    }
    await json(path.join(data, "payload-hashes.json"), published);
    await writeFile(path.join(site, "llms.txt"),
      "# CAO synthetic enterprise demo\n\nAll records are generated examples, not operational evidence.\n"
      + "Production runs in the cloud Go server; local development uses explicit operational snapshots.\n"
      + "This demo exposes no operational SQLite export, agent summary, or live services.\n");
    await json(path.join(site, marker), {
      tool: "cao-pages-demo", version: 1, synthetic: true, generatedAt,
      repositories: departments.length * services.length, runs: 14 * workflows.length * departments.length * services.length,
    });
    await assertSafePagesOutput(destination);
    await rm(destination, { recursive: true, force: true });
    await rename(site, destination);
    return { output: destination, synthetic: true, generatedAt, payloads: Object.keys(published).length };
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
    process.stderr.write(`Pages demo preparation failed: ${error.message}\n`);
    process.exitCode = 1;
  }
}
