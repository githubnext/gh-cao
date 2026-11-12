import { expect, test } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { startDashboardPagesPreview } from "../helpers/dashboard-pages-preview.mjs";
import { scrollRenderedViewsIntoView } from "./dashboard-deployed-refresh-helpers.mjs";

let preview;
let expectedRuns;

test.beforeAll(async () => {
  test.setTimeout(240_000);
  preview = await startDashboardPagesPreview({ experimental: true });
  expectedRuns = JSON.parse(await readFile(join(preview.site, ".cao-pages-demo.json"), "utf8")).runs;
});

test.afterAll(async () => { await preview?.close(); });
test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    document.addEventListener("dashboard-data", (event) => {
      if (event.detail?.kind === "refresh") window.__demoRefreshStatus = event.detail.status;
    });
  });
});

test("enterprise sample populates analytical pages and supports real drill-downs", async ({ page }) => {
  test.setTimeout(180_000);
  const errors = [];
  const externalRequests = [];
  page.on("pageerror", error => errors.push(error.message));
  page.on("request", request => {
    if (new URL(request.url()).origin !== new URL(preview.url).origin || /\/api\//.test(request.url())) {
      externalRequests.push(request.url());
    }
  });
  const drillTargets = new Map([
    ["repositories", "repository-"],
    ["workflows", "workflow-"],
    ["campaigns", "campaign-"],
    ["value", "value-report"],
    ["firewall", "domain-"],
    ["mcps", "tool-"],
    ["marketplace", "marketplace-package"],
  ]);
  for (const id of ["campaigns", "repositories", "workflows", "runs", "issues", "firewall", "mcps",
    "skills", "friction", "engines-models", "value", "value-campaigns", "value-standalone", "value-review",
    "cost", "marketplace", "maintenance", "maintenance-campaigns"]) {
    await page.goto(`${preview.url}/?debug=1&debug-eager-ingest=1#page-${id}`);
    await page.waitForFunction(() => window.__demoRefreshStatus === "completed", null, { timeout: 90_000 });
    const active = page.locator(`[data-page-id="${id}"]`);
    await expect(active).toBeVisible();
    await expect(active).not.toHaveAttribute("data-page-prerequisite-unavailable", "true");
    const views = active.locator("[data-view-id]:visible");
    await expect(views.first()).toBeVisible();
    await scrollRenderedViewsIntoView(active);
    await expect(active.locator(".dashboard-view-skeleton:visible, .dashboard-lazy-view-skeleton:visible")).toHaveCount(0);
    await expect(active.locator(".empty:visible, .empty-message:visible")).toHaveCount(0);
    const target = drillTargets.get(id);
    if (target) {
      const link = active.locator(`a[href^="#page-${target}"]`).first();
      await expect(link).toBeAttached();
      const href = await link.getAttribute("href");
      await page.goto(`${preview.url}/?debug=1${href}`);
      const detail = page.locator(`[data-page-id="${href.split("?")[0].slice("#page-".length)}"]`);
      await expect(detail).toBeVisible();
      await expect(detail.locator("[data-view-id]").first()).toBeAttached();
      await expect(detail).not.toContainText("Unable to load");
    }
  }
  await page.goto(`${preview.url}/?debug=1#page-memory`);
  await page.waitForFunction(() => window.__demoRefreshStatus === "completed", null, { timeout: 90_000 });
  const memory = page.locator('[data-page-id="memory"]');
  await memory.getByRole("treeitem", { name: "Dependency health", exact: true }).click();
  await expect(memory.getByRole("treeitem", { name: /observations\.jsonl/ }).first()).toBeVisible();
  await memory.getByRole("treeitem", { name: /observations\.jsonl/ }).first().click();
  await expect(memory).toContainText("Synthetic dependency upgrade accepted");
  expect(errors).toEqual([]);
  expect(externalRequests).toEqual([]);
});

for (const width of [1440, 390]) {
  test(`Pull request simulator recomputes company estimates at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    await page.goto(`${preview.url}/?debug=1&debug-eager-ingest=1#page-simulators`, { waitUntil: "domcontentloaded" });
    await expect(page.locator("#page-title")).toHaveText("Simulators");
    await page.waitForFunction(() => window.__demoRefreshStatus === "completed", null, { timeout: 90_000 });
    await expect(page.locator(".first-load-overlay")).toHaveCount(0, { timeout: 90_000 });
    await page.locator('[data-page-id="simulators"]').getByRole("link", { name: "Pull request", exact: true }).click();
    const simulator = page.locator('[data-page-id="simulator-pull-request"]');
    await expect(simulator.getByRole("link", { name: "Pull request", exact: true })).toHaveAttribute("aria-current", "page");
    const table = simulator.locator('[data-view-id="simulator-pr-totals"]').getByRole("table");
    const awRuns = table.getByRole("row").filter({ has: page.getByRole("cell", { name: "AW runs", exact: true }) });
    const credits = table.getByRole("row").filter({ has: page.getByRole("cell", { name: "AI Credits (AIC)", exact: true }) });
    await expect(awRuns.getByRole("cell").nth(1)).toHaveAccessibleName(/500,000 Modeled count/);
    await expect(credits.getByRole("cell").nth(1)).toHaveAccessibleName(/50,000,000 Modeled count/);
    const screenshot = testInfo.outputPath(`pull-request-simulator-${width}.png`);
    await page.screenshot({ path: screenshot, fullPage: true });
    await testInfo.attach(`Pull request simulator ${width}px`, { path: screenshot, contentType: "image/png" });
    await simulator.getByRole("slider", { name: "Duration (days)", exact: true }).fill("2");
    await expect(awRuns.getByRole("cell").nth(2)).toHaveAccessibleName(/1,000,000 Modeled count/);
    const developerInput = simulator.getByRole("slider", { name: "Developers", exact: true });
    await developerInput.focus();
    await developerInput.fill("3");
    await expect(awRuns.getByRole("cell").nth(2)).toHaveAccessibleName(/300 Modeled count/);
    await expect(developerInput).toBeFocused();
    await simulator.getByRole("slider", { name: "Repositories", exact: true }).fill("7");
    await expect(awRuns.getByRole("cell").nth(2)).toHaveAccessibleName(/300 Modeled count/);
    await simulator.getByRole("slider", { name: "AIC per AW run", exact: true }).fill("0");
    await expect(credits.getByRole("cell").nth(2)).toHaveAccessibleName(/0 Modeled count/);
    await simulator.getByRole("link", { name: "Database size", exact: true }).click();
    await expect(page.locator('[data-page-id="simulators"]').getByRole("slider")).toHaveCount(5);
  });

  test(`Go Pages deployment uses populated IndexedDB at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const hostedRequests = [];
    const errors = [];
    page.on("request", (request) => {
      if (new URL(request.url()).origin !== new URL(preview.url).origin
          || /\/api\//.test(request.url())) {
        hostedRequests.push(request.url());
      }
    });
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto(`${preview.url}/favicon.svg`);
    await page.evaluate(async (base) => {
      const request = indexedDB.open(`gh-aw-cao-dashboard-data:${encodeURIComponent(new URL(base).pathname + "/")}`, 1);
      request.onupgradeneeded = () => {
        request.result.createObjectStore("runs", { keyPath: "id" }).put({ id: "real-operational-record" });
      };
      await new Promise((resolve, reject) => {
        request.onsuccess = () => { request.result.close(); resolve(); };
        request.onerror = () => reject(request.error);
      });
      const cache = await caches.open("central-agentic-ops-dashboard-data-old");
      await cache.put(`${base}/payload-hashes.json`, Response.json({ "real-data.jsonl": "must-not-be-read" }));
    }, preview.url);
    await page.goto(`${preview.url}/?debug=1&debug-eager-ingest=1#page-overview`, { waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => window.__demoRefreshStatus === "completed", null, { timeout: 90_000 });
    await expect(page.locator(".first-load-overlay")).toHaveCount(0, { timeout: 90_000 });
    await expect(page.locator("#page-title")).toHaveText("Overview");
    await expect(page.locator('meta[name="dashboard-data-backend"]')).toHaveCount(0);
    await expect.poll(() => page.evaluate(async () => {
      const databases = await indexedDB.databases();
      const descriptor = databases.find(({ name }) => name?.includes("synthetic-demo"));
      if (!descriptor?.name) return 0;
      const database = await new Promise((resolve, reject) => {
        const request = indexedDB.open(descriptor.name);
        request.onsuccess = () => resolve(request.result);
        request.onerror = () => reject(request.error);
      });
      try {
        return await new Promise((resolve, reject) => {
          const transaction = database.transaction("runs", "readonly");
          const request = transaction.objectStore("runs").count();
          request.onsuccess = () => resolve(request.result);
          request.onerror = () => reject(request.error);
        });
      } finally { database.close(); }
    }), { timeout: 90_000 }).toBe(expectedRuns);
    const demoRunIds = await page.evaluate(async () => {
      const descriptor = (await indexedDB.databases()).find(({ name }) => name?.includes("synthetic-demo"));
      const db = await new Promise((resolve, reject) => {
        const request = indexedDB.open(descriptor.name);
        request.onsuccess = () => resolve(request.result);
        request.onerror = () => reject(request.error);
      });
      try {
        return await new Promise((resolve, reject) => {
          const request = db.transaction("runs").objectStore("runs").getAllKeys();
          request.onsuccess = () => resolve(request.result);
          request.onerror = () => reject(request.error);
        });
      } finally { db.close(); }
    });
    expect(demoRunIds).toHaveLength(expectedRuns);
    expect(demoRunIds.every(id => String(id).includes("sample-enterprise/"))).toBe(true);
    await expect(page.locator("body")).toContainText("Synthetic enterprise demo");
    await expect(page.locator('meta[name="dashboard-deployment"]')).toHaveAttribute("content", "synthetic-demo");
    const screenshot = testInfo.outputPath(`pages-indexeddb-${width}.png`);
    await page.screenshot({ path: screenshot, fullPage: true });
    await testInfo.attach(`Pages IndexedDB ${width}px`, { path: screenshot, contentType: "image/png" });
    await page.reload({ waitUntil: "domcontentloaded" });
    await expect(page.locator("#page-title")).toHaveText("Overview");
    await expect(page.locator(".first-load-overlay")).toHaveCount(0);
    expect(hostedRequests).toEqual([]);
    expect(errors).toEqual([]);
    for (const name of ["payload-hashes.json", "inventory-sources.json"]) {
      const response = await page.request.get(`${preview.url}/demo-data/${name}`);
      expect(response.ok(), name).toBe(true);
    }
    for (const name of ["payload-hashes.json", "gh-aw-logs.sqlite", "agent-summary.json"]) {
      expect((await page.request.get(`${preview.url}/${name}`)).status()).toBe(404);
    }
    expect((await page.request.post(`${preview.url}/api/v1/query`, { data: {} })).status()).toBe(405);
    expect((await page.request.get(`${preview.url}/api/v1/query`)).status()).toBe(404);
  });
}
