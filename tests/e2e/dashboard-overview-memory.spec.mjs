import { expect, test } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import { startDashboardPagesPreview } from "../helpers/dashboard-pages-preview.mjs";
import { WorkerHeapProbe, remoteDebuggingPort } from "./dashboard-worker-memory.mjs";

const sampleIntervalMs = numberSetting("DASHBOARD_OVERVIEW_MEMORY_SAMPLE_INTERVAL_MS", 50);
// Browser-worker budgets apply to the synthetic enterprise demo, not operational
// cloud or SQLite workloads. Keep ingestion and query peaks separately visible.
const maximumWorkerHeapMb = numberSetting("DASHBOARD_OVERVIEW_MAX_WORKER_HEAP_MB", 192);
const maximumRetainedWorkerHeapMb = numberSetting("DASHBOARD_OVERVIEW_MAX_RETAINED_WORKER_HEAP_MB", 128);
const maximumIngestionWorkerHeapMb = numberSetting("DASHBOARD_OVERVIEW_MAX_INGESTION_WORKER_HEAP_MB", 448);
const megabyte = 1024 * 1024;
// Forces the worker to ingest every published shard before publishing results,
// so both phases measure a fully ingested canonical database.
const overviewUrl = (preview) => `${preview.url}/?debug-eager-ingest=1#page-overview`;

let preview;

function numberSetting(name, fallback) {
  const value = Number(process.env[name] ?? fallback);
  if (!Number.isSafeInteger(value) || value < 1) throw new Error(`${name} must be a positive integer.`);
  return value;
}

test.beforeAll(async () => {
  test.setTimeout(900_000);
  preview = await startDashboardPagesPreview();
});

test.afterAll(async () => {
  await preview?.close();
});

const instrumentation = () => {
  window.__dashboardRefreshStatus = null;
  window.__dashboardOverviewRenders = 0;
  document.addEventListener("dashboard-data", (event) => {
    if (event.detail?.kind === "refresh") window.__dashboardRefreshStatus = event.detail.status;
  });
  document.addEventListener("dashboard-render", (event) => {
    if (event.detail?.kind === "page" && event.detail?.pageId === "overview"
        && event.detail?.status === "completed") {
      window.__dashboardOverviewRenders += 1;
    }
  });
};

async function canonicalDatabaseState(page) {
  return page.evaluate(async () => {
    // The canonical database name is scoped to the deployment path, so it has
    // to be discovered rather than guessed; opening a guessed name would
    // silently create an empty database instead.
    const databases = await indexedDB.databases();
    const descriptor = databases.find(({ name }) => name?.startsWith("gh-aw-cao-dashboard-data"));
    if (!descriptor?.name) return { name: null, version: 0, storeNames: [], counts: {} };
    const database = await new Promise((resolve, reject) => {
      const request = indexedDB.open(descriptor.name, descriptor.version);
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
    const storeNames = [...database.objectStoreNames];
    const counts = Object.fromEntries(await Promise.all(storeNames.map((name) => new Promise((resolve, reject) => {
      const request = database.transaction(name, "readonly").objectStore(name).count();
      request.onsuccess = () => resolve([name, request.result]);
      request.onerror = () => reject(request.error);
    }))));
    const version = database.version;
    database.close();
    return { name: descriptor.name, version, storeNames, counts };
  });
}

/** Samples the data worker's heap until `settled` resolves. */
async function sampleWhile(probe, phase, settled) {
  const startedAt = performance.now();
  let sampling = probe.sample(`${phase}-start`, 0);
  const interval = setInterval(() => {
    sampling = sampling.then(() => probe.sample(phase, Math.round(performance.now() - startedAt)));
  }, sampleIntervalMs);
  try {
    return await settled();
  } finally {
    clearInterval(interval);
    await sampling;
  }
}

test("the synthetic enterprise Overview renders within a bounded data-worker heap", async ({ page }, testInfo) => {
  test.setTimeout(900_000);
  await page.addInitScript(instrumentation);
  await page.setViewportSize({ width: 1440, height: 900 });

  const ingestionProbe = new WorkerHeapProbe({ port: remoteDebuggingPort() });
  await page.goto(overviewUrl(preview), { waitUntil: "domcontentloaded" });
  expect(await ingestionProbe.attach()).toBe(true);
  await sampleWhile(ingestionProbe, "ingestion", () =>
    page.waitForFunction(() => window.__dashboardRefreshStatus === "completed", null, { timeout: 600_000 }));
  const ingested = await canonicalDatabaseState(page);
  ingestionProbe.close();

  expect(ingested.counts.runs).toBeGreaterThan(0);
  expect(ingested.counts.repositories).toBeGreaterThan(0);
  // Proves the Overview heap budget is measured against a fully ingested
  // database rather than a run-phase-only or shard-limited subset.
  expect(ingested.counts.tools).toBeGreaterThan(0);
  expect(ingested.storeNames).not.toContain("dailyOverviewAggregates");
  expect(ingested.storeNames).not.toContain("overviewAggregateMetadata");

  // Reloading against the already-ingested database isolates the Overview page
  // load from ingestion, so the heap peak belongs to its declarative queries.
  // The probe is attached after the reload so it measures the replacement
  // worker rather than the one the reload is about to discard.
  await page.reload({ waitUntil: "domcontentloaded" });
  const overviewProbe = new WorkerHeapProbe({ port: remoteDebuggingPort() });
  expect(await overviewProbe.attach()).toBe(true);
  const overviewStartedAt = performance.now();
  await sampleWhile(overviewProbe, "overview", async () => {
    await page.waitForFunction(() => window.__dashboardOverviewRenders > 0, null, { timeout: 180_000 });
    await expect(page.locator(".dashboard-overview-page").first()).toBeVisible();
    await expect(page.locator(".dashboard-overview-page .custom-view").first()).toBeVisible();
    // The Overview load is fast enough that a bare render wait can finish
    // before the sampler runs, which would leave the budget unmeasured.
    await page.waitForTimeout(sampleIntervalMs * 8);
  });
  const overviewDurationMs = Math.round(performance.now() - overviewStartedAt);
  await overviewProbe.collectGarbage();
  await overviewProbe.sample("after-garbage-collection", overviewDurationMs);

  const renderedViews = await page.locator(".dashboard-overview-page .custom-view").count();
  const peakIngestionBytes = ingestionProbe.peakUsedBytes();
  const peakOverviewBytes = overviewProbe.peakUsedBytes(["overview-start", "overview"]);
  const retainedOverviewBytes = overviewProbe.lastUsedBytes();
  const metrics = {
    evidenceKind: "synthetic-enterprise-demo",
    evidenceLimits: "Browser-worker heap only; not operational freshness or completeness evidence.",
    database: ingested,
    overview: { durationMs: overviewDurationMs, renderedViews },
    workerHeap: {
      target: overviewProbe.targetUrl,
      peakIngestionBytes,
      peakOverviewBytes,
      retainedOverviewBytes,
      budgets: {
        ingestionBytes: maximumIngestionWorkerHeapMb * megabyte,
        overviewBytes: maximumWorkerHeapMb * megabyte,
        retainedBytes: maximumRetainedWorkerHeapMb * megabyte,
      },
      ingestionSamples: ingestionProbe.samples,
      overviewSamples: overviewProbe.samples,
    },
  };
  overviewProbe.close();
  await mkdir(testInfo.outputDir, { recursive: true });
  const metricsPath = testInfo.outputPath("dashboard-overview-memory-metrics.json");
  await writeFile(metricsPath, `${JSON.stringify(metrics, null, 2)}\n`);
  await testInfo.attach("dashboard-overview-memory-metrics", {
    path: metricsPath,
    contentType: "application/json",
  });

  expect(renderedViews).toBeGreaterThan(0);
  expect(peakIngestionBytes).toBeGreaterThan(0);
  // Fails an unmeasured run rather than letting an empty sample set satisfy
  // the Overview budget.
  expect(peakOverviewBytes).toBeGreaterThan(0);
  expect(peakOverviewBytes).toBeLessThanOrEqual(maximumWorkerHeapMb * megabyte);
  expect(retainedOverviewBytes).toBeLessThanOrEqual(maximumRetainedWorkerHeapMb * megabyte);
  expect(peakIngestionBytes).toBeLessThanOrEqual(maximumIngestionWorkerHeapMb * megabyte);
});
