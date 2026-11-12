import { expect, test } from "@playwright/test";
import { spawn } from "node:child_process";
import { createReadStream, existsSync, statSync } from "node:fs";
import { mkdir, mkdtemp, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { extname, join, resolve, sep } from "node:path";
import { once } from "node:events";
import { normalizeSnapshotPayloads } from "../../dashboard/report/normalized-payloads.mjs";

const siteRoot = resolve("dashboard/site");
const repositories = numberSetting("DASHBOARD_STRESS_REPOSITORIES", 10_000);
const runs = numberSetting("DASHBOARD_STRESS_RUNS", 20_000);
const derivedEvents = numberSetting("DASHBOARD_STRESS_DERIVED_EVENTS", 6);
const shards = numberSetting("DASHBOARD_STRESS_SHARDS", 20);
const workflows = numberSetting("DASHBOARD_STRESS_WORKFLOWS", 24);
const sampleIntervalMs = numberSetting("DASHBOARD_STRESS_SAMPLE_INTERVAL_MS", 250);
const maximumPageHeapMb = numberSetting("DASHBOARD_STRESS_MAX_BROWSER_HEAP_MB", 220);
const maximumRetainedPageHeapMb = numberSetting("DASHBOARD_STRESS_MAX_RETAINED_HEAP_MB", 128);
const maximumWorkingSetMb = numberSetting("DASHBOARD_STRESS_MAX_WORKING_SET_MB", 2_304);
const maximumGeneratorRssMb = numberSetting("DASHBOARD_STRESS_MAX_GENERATOR_RSS_MB", 256);
const megabyte = 1024 * 1024;

let root;
let rawShardDirectory;
let manifest;
let payloadHashes;
let generationMemory;
let server;
let origin;

function numberSetting(name, fallback) {
  const value = Number(process.env[name] ?? fallback);
  if (!Number.isSafeInteger(value) || value < 1) throw new Error(`${name} must be a positive integer.`);
  return value;
}

async function linuxMemory(pid) {
  if (process.platform !== "linux") return null;
  try {
    const status = await readFile(`/proc/${pid}/status`, "utf8");
    return {
      rssBytes: Number(/^VmRSS:\s+(\d+)\s+kB/m.exec(status)?.[1] ?? 0) * 1024,
      peakRssBytes: Number(/^VmHWM:\s+(\d+)\s+kB/m.exec(status)?.[1] ?? 0) * 1024,
    };
  } catch {
    return null;
  }
}

async function processTreeWorkingSet(rootPid = process.pid) {
  if (process.platform !== "linux") return null;
  const entries = await readdir("/proc", { withFileTypes: true });
  const processes = (await Promise.all(entries
    .filter((entry) => entry.isDirectory() && /^\d+$/.test(entry.name))
    .map(async (entry) => {
      const memory = await linuxMemory(Number(entry.name));
      if (!memory) return null;
      const status = await readFile(`/proc/${entry.name}/status`, "utf8").catch(() => "");
      return {
        pid: Number(entry.name),
        parentPid: Number(/^PPid:\s+(\d+)/m.exec(status)?.[1] ?? 0),
        rssBytes: memory.rssBytes,
      };
    }))).filter(Boolean);
  const descendants = new Set([rootPid]);
  let previousSize = 0;
  while (descendants.size !== previousSize) {
    previousSize = descendants.size;
    for (const process of processes) {
      if (descendants.has(process.parentPid)) descendants.add(process.pid);
    }
  }
  return {
    processCount: descendants.size,
    rssBytes: processes
      .filter(({ pid }) => descendants.has(pid))
      .reduce((total, process) => total + process.rssBytes, 0),
  };
}

async function runMeasuredCommand(command, args) {
  const startedAt = performance.now();
  const child = spawn(command, args, { stdio: ["ignore", "pipe", "pipe"] });
  let stdout = "";
  let stderr = "";
  child.stdout.setEncoding("utf8");
  child.stderr.setEncoding("utf8");
  child.stdout.on("data", (chunk) => { stdout += chunk; });
  child.stderr.on("data", (chunk) => { stderr += chunk; });
  const samples = [];
  const capture = async () => {
    const memory = await linuxMemory(child.pid);
    if (memory) samples.push({ elapsedMs: Math.round(performance.now() - startedAt), ...memory });
  };
  await capture();
  const interval = setInterval(() => void capture(), sampleIntervalMs);
  const [code, signal] = await once(child, "exit");
  clearInterval(interval);
  await capture();
  if (code !== 0) {
    throw new Error(`${command} exited with ${code ?? signal}: ${stderr || stdout}`);
  }
  return {
    stdout,
    stderr,
    durationMs: Math.round(performance.now() - startedAt),
    peakRssBytes: Math.max(0, ...samples.map(({ rssBytes }) => rssBytes)),
    peakHighWaterBytes: Math.max(0, ...samples.map(({ peakRssBytes }) => peakRssBytes)),
    samples,
  };
}

test.beforeAll(async () => {
  root = await mkdtemp(join(tmpdir(), "cao-dashboard-massive-scale-"));
  rawShardDirectory = join(root, "gh-aw-logs-shards");
  const runsDirectory = join(root, "gh-aw-logs-runs");
  const recordsDirectory = join(root, "gh-aw-logs-records");
  generationMemory = await runMeasuredCommand(process.execPath, [
    resolve("tests/helpers/dashboard-stress-data.mjs"),
    "--output", rawShardDirectory,
    "--repositories", String(repositories),
    "--runs", String(runs),
    "--derived-events", String(derivedEvents),
    "--shards", String(shards),
    "--workflows", String(workflows),
  ]);
  manifest = JSON.parse(generationMemory.stdout);
  payloadHashes = await normalizeSnapshotPayloads({
    shardDirectory: rawShardDirectory,
    runsDirectory,
    recordsDirectory,
  });

  server = createServer(async (request, response) => {
    try {
      const pathname = new URL(request.url ?? "/", "http://localhost").pathname;
      if (pathname === "/" || pathname === "/index.html") {
        response.writeHead(200, { "content-type": "text/html" });
        response.end("<main>dashboard massive-scale stress</main>");
        return;
      }
      if (pathname === "/payload-hashes.json") {
        response.writeHead(200, { "content-type": "application/json" });
        response.end(JSON.stringify(Object.fromEntries(
          Object.entries(payloadHashes).filter(([name]) =>
            name.startsWith("gh-aw-logs-runs/") || name.startsWith("gh-aw-logs-records/")
          ),
        )));
        return;
      }
      const shardName = pathname.slice(1);
      if (Object.hasOwn(payloadHashes, shardName)
          && (shardName.startsWith("gh-aw-logs-runs/") || shardName.startsWith("gh-aw-logs-records/"))) {
        const shardPath = join(root, shardName);
        response.writeHead(200, {
          "content-type": "application/x-ndjson",
          "content-length": String((await stat(shardPath)).size),
        });
        createReadStream(shardPath).pipe(response);
        return;
      }
      const filePath = resolve(join(siteRoot, pathname));
      if (filePath.startsWith(`${siteRoot}${sep}`) && existsSync(filePath) && statSync(filePath).isFile()) {
        response.writeHead(200, {
          "content-type": extname(filePath) === ".json" ? "application/json" : "text/javascript",
        });
        createReadStream(filePath).pipe(response);
        return;
      }
      response.writeHead(404, { "content-type": "text/plain" });
      response.end("Not found");
    } catch {
      response.writeHead(500, { "content-type": "text/plain" });
      response.end("Unable to serve stress-test data.");
    }
  });
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const address = server.address();
  origin = `http://127.0.0.1:${typeof address === "object" && address ? address.port : 0}`;
});

test.afterAll(async () => {
  if (server) {
    server.close();
    await once(server, "close");
  }
  await rm(root, { recursive: true, force: true });
});

test("massive shards populate canonical storage within restricted memory", async ({ page }, testInfo) => {
  const session = await page.context().newCDPSession(page);
  await session.send("Performance.enable");
  const browserSamples = [];
  const browserStartedAt = performance.now();
  const capture = async (phase) => {
    const [metrics, workingSet] = await Promise.all([
      session.send("Performance.getMetrics"),
      processTreeWorkingSet(),
    ]);
    const values = Object.fromEntries(metrics.metrics.map(({ name, value }) => [name, value]));
    browserSamples.push({
      elapsedMs: Math.round(performance.now() - browserStartedAt),
      phase,
      jsHeapUsedBytes: values.JSHeapUsedSize ?? null,
      jsHeapTotalBytes: values.JSHeapTotalSize ?? null,
      workingSetBytes: workingSet?.rssBytes ?? null,
      processCount: workingSet?.processCount ?? null,
    });
  };
  await capture("before-navigation");
  let capturePending = Promise.resolve();
  const interval = setInterval(() => {
    capturePending = capturePending.then(() => capture("loading"));
  }, sampleIntervalMs);
  let result;
  try {
    await page.goto(`${origin}/?debug-eager-ingest=1`, { waitUntil: "domcontentloaded" });
    result = await page.evaluate(async ({ sourceUrl }) => {
      const { loadCanonicalDashboardSources, queryCanonicalDatabaseDiagnostics } = await import(`${location.origin}/src/data-processor.js`);
      const startedAt = performance.now();
      const sources = await loadCanonicalDashboardSources(
        sourceUrl,
        ["runs"],
        { githubUrlBase: "https://github.com", pages: [] },
        { runs: { limit: 100 } },
      );
      return {
        durationMs: Math.round(performance.now() - startedAt),
        returnedRuns: sources.runs?.rows?.length ?? 0,
        counts: (await queryCanonicalDatabaseDiagnostics()).counts,
      };
    }, { sourceUrl: `${origin}/payload-hashes.json` });
  } finally {
    clearInterval(interval);
    await capturePending;
  }
  await capture("settled");
  await session.send("HeapProfiler.collectGarbage");
  await capture("after-garbage-collection");
  await session.send("Performance.disable");

  const peakPageHeapBytes = Math.max(0, ...browserSamples.map(({ jsHeapUsedBytes }) => jsHeapUsedBytes ?? 0));
  const peakWorkingSetBytes = Math.max(0, ...browserSamples.map(({ workingSetBytes }) => workingSetBytes ?? 0));
  const retainedPageHeapBytes = browserSamples.at(-1)?.jsHeapUsedBytes ?? 0;
  const metrics = {
    parameters: { repositories, runs, derivedEvents, shards, workflows },
    manifest,
    database: {
      counts: result.counts,
      generation: generationMemory,
    },
    browser: {
      device: "Pixel 7",
      memoryLimitMb: Number(process.env.DASHBOARD_STRESS_BROWSER_MEMORY_MB ?? 256),
      result,
      peakPageHeapBytes,
      retainedPageHeapBytes,
      peakWorkingSetBytes,
      samples: browserSamples,
    },
  };
  await mkdir(testInfo.outputDir, { recursive: true });
  const metricsPath = testInfo.outputPath("dashboard-massive-scale-metrics.json");
  await writeFile(metricsPath, `${JSON.stringify(metrics, null, 2)}\n`);
  await testInfo.attach("dashboard-massive-scale-metrics", {
    path: metricsPath,
    contentType: "application/json",
  });

  expect(result.returnedRuns).toBe(Math.min(100, runs));
  expect(result.counts.repositories).toBe(manifest.expected.repositories);
  expect(result.counts.runs).toBe(manifest.expected.runs);
  expect(result.counts.audits).toBe(manifest.expected.audits);
  expect(generationMemory.peakHighWaterBytes).toBeLessThanOrEqual(maximumGeneratorRssMb * megabyte);
  expect(peakPageHeapBytes).toBeLessThanOrEqual(maximumPageHeapMb * megabyte);
  expect(retainedPageHeapBytes).toBeLessThanOrEqual(maximumRetainedPageHeapMb * megabyte);
  expect(peakWorkingSetBytes).toBeLessThanOrEqual(maximumWorkingSetMb * megabyte);
});
