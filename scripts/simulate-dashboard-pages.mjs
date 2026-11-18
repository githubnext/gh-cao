#!/usr/bin/env node

import { randomUUID } from "node:crypto";
import { lstat, mkdir, readFile, readdir, rename, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { buildDashboardSite } from "../dashboard/site/scripts/build.mjs";
import { enterpriseDemo } from "./dashboard-demo-data.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
const marker = ".cao-pages-demo.json";
const organization = "sample-enterprise";
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

export async function prepareDashboardPages({
  output = path.join(root, ".tmp/dashboard-pages"), now = Date.now(), experimental = false, ...unsupported
} = {}) {
  if (Object.keys(unsupported).length) throw new Error("Pages accepts synthetic data only; unsupported preparation options");
  if (!Number.isFinite(now) || typeof experimental !== "boolean") throw new Error("Invalid demo build options");
  const destination = await assertSafePagesOutput(output);
  const staging = `${destination}.prepare-${randomUUID()}`;
  const site = path.join(staging, "site");
  await mkdir(staging, { recursive: true });
  try {
    const generatedAt = new Date(now).toISOString();
    await buildDashboardSite({
      destination: site, controlSettings: { campaigns: {}, web: { experimental } },
      githubRepository: `${organization}/platform-api`, githubServerUrl: "https://example.invalid",
      profile: "demo",
    });
    await writeFile(path.join(site, "llms.txt"),
      "# CAO synthetic enterprise demo\n\nAll records are generated examples, not operational evidence.\n"
      + "Production runs in the cloud Go server; local development uses explicit operational snapshots.\n"
      + "This demo exposes no operational SQLite export, agent summary, or live services.\n");
    await json(path.join(site, marker), {
      tool: "cao-pages-demo", synthetic: true, generatedAt,
      ...enterpriseDemo,
    });
    await assertSafePagesOutput(destination);
    await rm(destination, { recursive: true, force: true });
    await rename(site, destination);
    return { output: destination, synthetic: true, generatedAt, payloads: 0 };
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
