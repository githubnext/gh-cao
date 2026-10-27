import { expect, test } from "@playwright/test";
import { createWriteStream } from "node:fs";
import { mkdir, readFile, readdir, stat, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { pipeline } from "node:stream/promises";
import { startDashboardPagesPreview } from "../helpers/dashboard-pages-preview.mjs";
import { captureMobileDashboardScreenshot } from "./dashboard-screenshot.mjs";
import {
  deployedActivityShardEntries,
  legacyPhaseJsonToJsonl,
} from "./dashboard-deployed-refresh-helpers.mjs";
import {
  summarizeAccessibilityTree,
  summarizeDomTree,
  summarizeMobileAccessibility,
} from "./dashboard-tree-analysis.mjs";

const maximumDomNodes = 6_000;
const maximumWebKitShardCount = 5;
const transientDownloadStatuses = new Set([408, 429, 500, 502, 503, 504]);
let preview;
let sourcePayload;
let expectedActivityShardPaths;

function optionalPositiveInteger(name) {
  const value = process.env[name];
  if (value === undefined) return undefined;
  if (!/^[1-9]\d*$/.test(value)) throw new Error(`${name} must be a positive integer.`);
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed)) throw new Error(`${name} must be a safe integer.`);
  return parsed;
}

function mobileDebugShardLimit() {
  const requested = optionalPositiveInteger("MOBILE_DEBUG_SHARD_LIMIT");
  return process.env.MOBILE_BROWSER === "webkit"
    ? Math.min(requested ?? maximumWebKitShardCount, maximumWebKitShardCount)
    : requested;
}

async function fetchDeployedData(url) {
  const maximumAttempts = 4;
  for (let attempt = 1; ; attempt += 1) {
    try {
      const response = await fetch(url);
      if (response.ok || !transientDownloadStatuses.has(response.status) || attempt === maximumAttempts) {
        return response;
      }
      await response.body?.cancel();
    } catch (error) {
      if (attempt === maximumAttempts) throw error;
    }
    await new Promise((resolve) => setTimeout(resolve, 1_000 * 2 ** (attempt - 1)));
  }
}

function metricValues(metrics) {
  return Object.fromEntries(metrics.map(({ name, value }) => [name, value]));
}

async function processTreeMemory(rootPid = process.pid) {
  if (process.platform !== "linux") return null;
  const entries = await readdir("/proc", { withFileTypes: true });
  const processes = (await Promise.all(entries
    .filter((entry) => entry.isDirectory() && /^\d+$/.test(entry.name))
    .map(async (entry) => {
      try {
        const status = await readFile(`/proc/${entry.name}/status`, "utf8");
        return {
          pid: Number(entry.name),
          parentPid: Number(/^PPid:\s+(\d+)/m.exec(status)?.[1]),
          rssBytes: Number(/^VmRSS:\s+(\d+)\s+kB/m.exec(status)?.[1]) * 1024,
        };
      } catch {
        return null;
      }
    }))).filter(Boolean);
  const descendants = new Set([rootPid]);
  let previousSize = 0;
  while (descendants.size !== previousSize) {
    previousSize = descendants.size;
    for (const candidate of processes) {
      if (descendants.has(candidate.parentPid)) descendants.add(candidate.pid);
    }
  }
  const children = processes.filter(({ pid }) => pid !== rootPid && descendants.has(pid));
  const proportionalBytes = await Promise.all(children.map(async ({ pid }) => {
    try {
      const rollup = await readFile(`/proc/${pid}/smaps_rollup`, "utf8");
      return Number(/^Pss:\s+(\d+)\s+kB/m.exec(rollup)?.[1]) * 1024;
    } catch {
      return 0;
    }
  }));
  return {
    processCount: children.length,
    rssBytes: children.reduce((total, child) => total + child.rssBytes, 0),
    pssBytes: proportionalBytes.reduce((total, value) => total + value, 0),
  };
}

async function startMemoryInvestigation(page, browserIsChromium) {
  const session = browserIsChromium ? await page.context().newCDPSession(page) : null;
  await session?.send("Performance.enable");
  const startedAt = performance.now();
  const samples = [];
  let active = true;
  let interval;
  let pending = Promise.resolve();
  let samplingError = null;

  const capture = async (phase) => {
    const [performanceMetrics, dom, processes] = await Promise.all([
      session?.send("Performance.getMetrics") ?? null,
      session?.send("Memory.getDOMCounters") ?? null,
      processTreeMemory(),
    ]);
    const values = metricValues(performanceMetrics?.metrics ?? []);
    samples.push({
      elapsedMs: Number((performance.now() - startedAt).toFixed(2)),
      phase,
      jsHeapUsedSize: values.JSHeapUsedSize ?? null,
      jsHeapTotalSize: values.JSHeapTotalSize ?? null,
      documents: dom?.documents ?? null,
      nodes: dom?.nodes ?? null,
      jsEventListeners: dom?.jsEventListeners ?? null,
      processTreeRssBytes: processes?.rssBytes ?? null,
      processTreePssBytes: processes?.pssBytes ?? null,
      processCount: processes?.processCount ?? null,
    });
  };
  const scheduleCapture = (phase) => {
    pending = pending.then(() => active ? capture(phase) : undefined).catch((error) => {
      samplingError ??= error instanceof Error ? error.message : String(error);
    });
    return pending;
  };
  await scheduleCapture("before-navigation");
  interval = setInterval(() => void scheduleCapture("loading"), 250);
  page.once("close", () => {
    active = false;
    clearInterval(interval);
  });

  return {
    session,
    mark: scheduleCapture,
    stop: async () => {
      active = false;
      clearInterval(interval);
      await pending;
      await capture("settled");
      if (session) {
        await session.send("HeapProfiler.collectGarbage");
        await capture("after-garbage-collection");
        await session.send("Performance.disable");
      }
      const peakJsHeap = samples.reduce((maximum, sample) =>
        (sample.jsHeapUsedSize ?? 0) > (maximum.jsHeapUsedSize ?? 0) ? sample : maximum
      , samples[0]);
      const peakProcessTreeRss = samples.reduce((maximum, sample) =>
        (sample.processTreeRssBytes ?? 0) > (maximum.processTreeRssBytes ?? 0) ? sample : maximum
      , samples[0]);
      const peakProcessTreePss = samples.reduce((maximum, sample) =>
        (sample.processTreePssBytes ?? 0) > (maximum.processTreePssBytes ?? 0) ? sample : maximum
      , samples[0]);
      return {
        supported: process.platform === "linux",
        jsHeapSupported: Boolean(session),
        samplingIntervalMs: 250,
        peakJsHeap,
        peakProcessTreeRss,
        peakProcessTreePss,
        settled: samples.findLast(({ phase }) => phase === "settled"),
        afterGarbageCollection: samples.findLast(({ phase }) => phase === "after-garbage-collection") ?? null,
        samplingError,
        samples,
      };
    },
  };
}

function optionalNumber(name) {
  const value = process.env[name];
  if (value === undefined) return null;
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) throw new Error(`${name} must be a positive number.`);
  return parsed;
}

function formatDomAnalysis(dom) {
  const formatDistribution = ({ mean, median, p95, maximum }) => `mean ${mean}, median ${median}, p95 ${p95}, max ${maximum}`;
  const formatFrequencies = (values) => values.slice(0, 5).map(({ name, count }) => `${name} (${count})`).join(", ") || "none";
  const formatStructures = dom.topStructures
    .slice(0, 5)
    .map(({ tag, id, jsonPath, jsView, descendantElements }) => {
      const provenance = jsonPath ? ` [${jsonPath}${jsView ? ` -> ${jsView}` : ""}]` : "";
      return `${tag}${id ? `#${id}` : ""}${provenance} (${descendantElements} descendants)`;
    })
    .join(", ") || "none";

  return [
    "Mobile dashboard DOM analysis:",
    `  Elements: ${dom.totalElements}`,
    `  Depth: ${formatDistribution(dom.depth)}`,
    `  Child elements: ${formatDistribution(dom.childElements)}`,
    `  Tags: ${formatFrequencies(dom.byTag)}`,
    `  Classes: ${formatFrequencies(dom.byClass)}`,
    `  JSON sources: ${formatFrequencies(dom.byJsonPath)}`,
    `  Largest structures: ${formatStructures}`,
  ].join("\n");
}

test.beforeAll(async () => {
  const dataUrl = process.env.DASHBOARD_DATA_URL;
  if (!dataUrl) throw new Error("DASHBOARD_DATA_URL is required.");
  preview = await startDashboardPagesPreview({
    downloadData: async (destination) => {
      const inventoryUrl = new URL("inventory-sources.json", dataUrl);
      const payloadHashesUrl = new URL("payload-hashes.json", dataUrl);
      const [payloadHashesResponse, inventoryResponse] = await Promise.all([
        fetchDeployedData(payloadHashesUrl),
        fetchDeployedData(inventoryUrl),
      ]);
      if (!payloadHashesResponse.ok) {
        throw new Error(`Unable to download deployed dashboard manifest: HTTP ${payloadHashesResponse.status}.`);
      }
      if (!inventoryResponse.ok) throw new Error(`Unable to download deployed dashboard inventory: HTTP ${inventoryResponse.status}.`);
      if (!inventoryResponse.body) throw new Error("Deployed dashboard inventory response has no body.");
      const payloadHashes = await payloadHashesResponse.json();
      const selectedShards = deployedActivityShardEntries(payloadHashes, mobileDebugShardLimit());
      expectedActivityShardPaths = selectedShards.map(({ name }) => `/${name}`);
      await mkdir(destination, { recursive: true });
      const inventoryPath = join(destination, "inventory-sources.json");
      await pipeline(inventoryResponse.body, createWriteStream(inventoryPath));
      let activityBytes = 0;
      for (const { name, sourceName } of selectedShards) {
        const response = await fetchDeployedData(new URL(sourceName, dataUrl));
        if (!response.ok) throw new Error(`Unable to download deployed dashboard shard ${sourceName}: HTTP ${response.status}.`);
        if (!response.body) throw new Error(`Deployed dashboard shard ${sourceName} has no body.`);
        const shardPath = join(destination, name);
        await mkdir(dirname(shardPath), { recursive: true });
        if (sourceName.endsWith(".json")) {
          await writeFile(shardPath, legacyPhaseJsonToJsonl(Buffer.from(await response.arrayBuffer())));
        } else {
          await pipeline(response.body, createWriteStream(shardPath));
        }
        activityBytes += (await stat(shardPath)).size;
      }
      const inventory = await stat(inventoryPath);
      sourcePayload = {
        activityBytes,
        inventoryBytes: inventory.size,
        totalBytes: activityBytes + inventory.size,
      };
    },
  });
});

test.afterAll(async () => {
  await preview?.close();
});

test("latest dashboard data loads within the mobile DOM budget", async ({ page }, testInfo) => {
  const pageErrors = [];
  let crashed = false;
  const shardResponses = new Map();
  let inventoryResponse;
  const memoryMb = optionalNumber("MOBILE_MEMORY_MB");
  const network = {
    downloadKbps: optionalNumber("MOBILE_NETWORK_DOWNLOAD_KBPS"),
    uploadKbps: optionalNumber("MOBILE_NETWORK_UPLOAD_KBPS"),
    latencyMs: optionalNumber("MOBILE_NETWORK_LATENCY_MS"),
  };
  const networkIsConstrained = Object.values(network).some((value) => value !== null);
  const browserIsChromium = process.env.MOBILE_BROWSER === "chromium";
  test.skip(
    (memoryMb !== null || networkIsConstrained) && !browserIsChromium,
    "Restricted memory and network throttling constraints require Chromium; running this profile on another browser would silently skip the constraint.",
  );
  const memoryInvestigation = await startMemoryInvestigation(page, browserIsChromium);
  if (networkIsConstrained) {
    expect(Object.values(network), "All network constraint values are required").not.toContain(null);
    const session = memoryInvestigation.session;
    await session.send("Network.enable");
    await session.send("Network.emulateNetworkConditions", {
      offline: false,
      latency: network.latencyMs,
      downloadThroughput: network.downloadKbps * 1024 / 8,
      uploadThroughput: network.uploadKbps * 1024 / 8,
    });
  }

  page.on("crash", () => {
    crashed = true;
  });
  page.on("pageerror", (error) => {
    pageErrors.push(error.message);
  });
  page.on("response", (response) => {
    const pathname = new URL(response.url()).pathname;
    const shardPath = /\/gh-aw-logs-(?:runs|records)\/[^/]+\.jsonl$/.exec(pathname)?.[0];
    if (shardPath) {
      shardResponses.set(shardPath, response);
    }
    if (pathname.endsWith("/inventory-sources.json")) inventoryResponse = response;
  });

  await page.addInitScript(() => {
    window.__dashboardRefreshStatus = null;
    document.addEventListener("dashboard-data", (event) => {
      if (event.detail?.kind === "refresh") {
        window.__dashboardRefreshStatus = event.detail.status;
      }
    });
  });

  const parameters = new URLSearchParams({ debug: "1" });
  const shardLimit = mobileDebugShardLimit();
  if (shardLimit !== undefined) parameters.set("debug-shard-limit", String(shardLimit));
  await page.goto(`${preview.url}/?${parameters}`, { waitUntil: "domcontentloaded" });
  const dashboard = page.locator(".dashboard-root");
  await expect(dashboard).toBeVisible({ timeout: networkIsConstrained ? 60_000 : 5_000 });
  await memoryInvestigation.mark("dashboard-visible");
  // The shell can be visible and not busy before canonical ingestion starts.
  await page.waitForFunction(() =>
    ["completed", "failed"].includes(window.__dashboardRefreshStatus),
  null, { timeout: 540_000 });
  expect(
    await page.evaluate(() => window.__dashboardRefreshStatus),
    "The dashboard must complete its canonical data refresh",
  ).toBe("completed");
  await expect(dashboard).not.toHaveAttribute("aria-busy", "true", { timeout: 540_000 });
  await expect(page.locator(".loading-progress")).toHaveCount(0, { timeout: 30_000 });
  await memoryInvestigation.mark("dashboard-idle");
  // DOM provenance annotation (`data-json-path`/`data-js-view`) is lazily
  // loaded and applied asynchronously; wait for it so the DOM analysis below
  // can attribute node counts to their owning JSON view.
  await expect(dashboard).toHaveAttribute("data-json-path", "$.dashboard", { timeout: 30_000 });

  expect(
    [...shardResponses.keys()].sort(),
    "The dashboard must request every canonical activity shard",
  ).toEqual([...expectedActivityShardPaths].sort());
  expect(
    [...shardResponses.values()].every((response) => response.ok()),
    "Dashboard activity shard requests must succeed",
  ).toBe(true);
  expect(inventoryResponse, "The dashboard must request inventory sources").toBeDefined();
  expect(inventoryResponse?.ok(), `Dashboard inventory returned ${inventoryResponse?.status()}`).toBe(true);
  expect(crashed, "The mobile browser page crashed while rendering the dashboard").toBe(false);
  expect(pageErrors, "The dashboard emitted browser errors").toEqual([]);

  const menu = page.locator(".mobile-nav-menu");
  await expect(menu).toBeVisible();
  await menu.locator(":scope > summary").click();
  await expect(menu).toHaveAttribute("open", "");

  const domTree = await page.evaluate(() => {
    const elements = [...document.getElementsByTagName("*")];
    const nodes = elements.map((element) => {
      let depth = 0;
      for (let parent = element.parentElement; parent; parent = parent.parentElement) depth += 1;
      return {
        tag: element.tagName.toLowerCase(),
        classes: [...element.classList],
        depth,
        childElementCount: element.childElementCount,
        jsonPath: element.getAttribute("data-json-path"),
        jsView: element.getAttribute("data-js-view"),
      };
    });
    const structures = [...document.querySelectorAll(
      "[data-page-id], [data-section-id], [data-view-id], header, nav, main, footer, table, svg, details",
    )].map((element) => ({
      tag: element.tagName.toLowerCase(),
      id: element.id || null,
      classes: [...element.classList],
      pageId: element.getAttribute("data-page-id"),
      sectionId: element.getAttribute("data-section-id"),
      viewId: element.getAttribute("data-view-id"),
      jsonPath: element.getAttribute("data-json-path"),
      jsView: element.getAttribute("data-js-view"),
      descendantElements: element.getElementsByTagName("*").length,
    }));
    return { nodes, structures };
  });
  const accessibilitySnapshot = await page.locator("body").ariaSnapshot();
  const mobileAccessibility = await page.evaluate(() => {
    const selector = [
      "a[href]",
      "button:not([disabled])",
      "input:not([type=hidden]):not([disabled])",
      "select:not([disabled])",
      "textarea:not([disabled])",
      "summary",
      "[role=button]",
      "[role=link]",
      "[role=menuitem]",
      "[role=tab]",
    ].join(",");
    const targets = [...document.querySelectorAll(selector)].flatMap((element) => {
      const wrappingLabel = element.closest("label");
      const explicitLabel = (!wrappingLabel && element.id && element.matches("input, select, textarea"))
        ? document.querySelector(`label[for="${CSS.escape(element.id)}"]`)
        : null;
      const hitArea = (wrappingLabel?.contains(element) ? wrappingLabel : explicitLabel) ?? element;
      const style = getComputedStyle(hitArea);
      const rectangle = hitArea.getBoundingClientRect();
      if (
        style.visibility === "hidden"
        || style.display === "none"
        || style.pointerEvents === "none"
        || rectangle.width === 0
        || rectangle.height === 0
      ) return [];
      const labelText = hitArea instanceof HTMLLabelElement ? hitArea.textContent : null;
      return [{
        tag: element.tagName.toLowerCase(),
        name: element.getAttribute("aria-label")
          || labelText?.replace(/\s+/g, " ").trim()
          || element.textContent?.replace(/\s+/g, " ").trim()
          || element.getAttribute("title")
          || element.getAttribute("name")
          || "",
        width: rectangle.width,
        height: rectangle.height,
      }];
    });
    return {
      targets,
      scrollWidth: document.documentElement.scrollWidth,
      viewportWidth: document.documentElement.clientWidth,
      viewportMeta: document.querySelector('meta[name="viewport"]')?.getAttribute("content") ?? "",
    };
  });
  const accessibility = summarizeAccessibilityTree(accessibilitySnapshot);
  const runtime = await page.evaluate(() => {
    const navigation = performance.getEntriesByType("navigation")[0];
    const memory = performance.memory;
    const resources = performance.getEntriesByType("resource")
      .filter(({ name }) => /\/(?:gh-aw-logs-(?:runs|records)\/[^/]+\.jsonl|inventory-sources\.json|dashboard\.json)$/.test(new URL(name).pathname))
      .map(({ name, duration, transferSize, encodedBodySize, decodedBodySize }) => ({
        name: new URL(name).pathname.split("/").at(-1),
        durationMs: Number(duration.toFixed(2)),
        transferSize,
        encodedBodySize,
        decodedBodySize,
      }));
    return {
      navigation: navigation ? {
        domContentLoadedMs: Number(navigation.domContentLoadedEventEnd.toFixed(2)),
        loadMs: Number(navigation.loadEventEnd.toFixed(2)),
        transferSize: navigation.transferSize,
        decodedBodySize: navigation.decodedBodySize,
      } : null,
      memory: memory ? {
        jsHeapSizeLimit: memory.jsHeapSizeLimit,
        totalJSHeapSize: memory.totalJSHeapSize,
        usedJSHeapSize: memory.usedJSHeapSize,
      } : null,
      resources,
    };
  });
  const memory = await memoryInvestigation.stop();
  const analysis = {
    profile: process.env.MOBILE_PROFILE ?? "baseline",
    device: process.env.MOBILE_DEVICE,
    browser: process.env.MOBILE_BROWSER,
    constraints: {
      memoryMb,
      network: networkIsConstrained ? network : null,
    },
    runtime,
    sourcePayload,
    memory,
    dom: summarizeDomTree(domTree.nodes, domTree.structures),
    accessibility,
    mobileAccessibility: summarizeMobileAccessibility({
      ...mobileAccessibility,
      unnamedInteractive: accessibility.issues.unnamedInteractive,
    }),
  };
  console.log(formatDomAnalysis(analysis.dom));
  await mkdir(testInfo.outputDir, { recursive: true });
  const analysisPath = testInfo.outputPath("mobile-dashboard-analysis.json");
  const accessibilityPath = testInfo.outputPath("mobile-dashboard-accessibility-tree.yml");
  const screenshotPath = testInfo.outputPath("mobile-dashboard.png");
  await writeFile(analysisPath, `${JSON.stringify(analysis, null, 2)}\n`);
  await writeFile(accessibilityPath, accessibilitySnapshot);
  await captureMobileDashboardScreenshot(page, testInfo, screenshotPath);
  await testInfo.attach("mobile-dashboard-analysis", {
    path: analysisPath,
    contentType: "application/json",
  });
  await testInfo.attach("mobile-dashboard-accessibility-tree", {
    path: accessibilityPath,
    contentType: "application/yaml",
  });
  expect(analysis.dom.totalElements, `Dashboard rendered ${analysis.dom.totalElements} DOM elements`).toBeLessThanOrEqual(maximumDomNodes);
  expect(
    analysis.mobileAccessibility.targets.undersized,
    `Visible interactive targets must be at least ${analysis.mobileAccessibility.minimumTargetSize} by ${analysis.mobileAccessibility.minimumTargetSize} CSS pixels`,
  ).toEqual([]);
  expect(analysis.mobileAccessibility.reflow, "The dashboard must reflow without horizontal page scrolling").toMatchObject({ passes: true });
  expect(analysis.mobileAccessibility.zoom, "The viewport metadata must allow at least 200% zoom").toMatchObject({ passes: true });
  expect(analysis.mobileAccessibility.accessibleNames.unnamedInteractive, "Interactive controls must have accessible names").toEqual([]);
});
