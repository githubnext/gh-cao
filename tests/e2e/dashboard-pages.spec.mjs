import { expect, test } from "@playwright/test";
import { startDashboardPagesPreview } from "../helpers/dashboard-pages-preview.mjs";

let preview;

test.beforeAll(async () => {
  test.setTimeout(240_000);
  preview = await startDashboardPagesPreview({ fixture: true, fixtureExperimental: true });
});

test.afterAll(async () => { await preview?.close(); });

for (const width of [1440, 390]) {
  test(`Pull request simulator recomputes company estimates at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    await page.goto(`${preview.url}/?debug-eager-ingest=1#page-simulators`, { waitUntil: "domcontentloaded" });
    await expect(page.locator("#page-title")).toHaveText("Simulators");
    await expect(page.locator(".first-load-overlay")).toHaveCount(0, { timeout: 90_000 });
    await page.locator('[data-page-id="simulators"]').getByRole("link", { name: "Pull request", exact: true }).click();
    const simulator = page.locator('[data-page-id="simulator-pull-request"]');
    await expect(simulator.getByRole("link", { name: "Pull request", exact: true })).toHaveAttribute("aria-current", "page");
    const table = simulator.locator('[data-view-id="simulator-pr-totals"]').getByRole("table");
    await expect(table.getByRole("row", { name: "AW runs 500000 500000", exact: true })).toBeVisible();
    await expect(table.getByRole("row", { name: "AI Credits (AIC) 50000000 50000000", exact: true })).toBeVisible();
    const screenshot = testInfo.outputPath(`pull-request-simulator-${width}.png`);
    await page.screenshot({ path: screenshot, fullPage: true });
    await testInfo.attach(`Pull request simulator ${width}px`, { path: screenshot, contentType: "image/png" });
    await simulator.getByRole("slider", { name: "Duration (days)", exact: true }).fill("2");
    await expect(table.getByRole("row", { name: "AW runs 500000 1000000", exact: true })).toBeVisible();
    const developerInput = simulator.getByRole("slider", { name: "Developers", exact: true });
    await developerInput.focus();
    await developerInput.fill("3");
    await expect(table.getByRole("row", { name: "AW runs 150 300", exact: true })).toBeVisible();
    await expect(developerInput).toBeFocused();
    await simulator.getByRole("slider", { name: "Repositories", exact: true }).fill("7");
    await expect(table.getByRole("row", { name: "AW runs 150 300", exact: true })).toBeVisible();
    await simulator.getByRole("slider", { name: "AIC per AW run", exact: true }).fill("0");
    await expect(table.getByRole("row", { name: "AI Credits (AIC) 0 0", exact: true })).toBeVisible();
    await simulator.getByRole("link", { name: "Database size", exact: true }).click();
    await expect(page.locator('[data-page-id="simulators"]').getByRole("slider")).toHaveCount(5);
  });

  test(`Go Pages deployment uses populated IndexedDB at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const hostedRequests = [];
    const errors = [];
    page.on("request", (request) => {
      if (/\/api\/v1\/(?:query|refresh|diagnostics|events)(?:[/?]|$)/.test(request.url())) {
        hostedRequests.push(request.url());
      }
    });
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto(`${preview.url}/?debug-eager-ingest=1#page-overview`, { waitUntil: "domcontentloaded" });
    await expect(page.locator(".first-load-overlay")).toHaveCount(0, { timeout: 90_000 });
    await expect(page.locator("#page-title")).toHaveText("Overview");
    await expect(page.locator('meta[name="dashboard-data-backend"]')).toHaveCount(0);
    await expect.poll(() => page.evaluate(async () => {
      const databases = await indexedDB.databases();
      const descriptor = databases.find(({ name }) => name?.startsWith("gh-aw-cao-dashboard-data"));
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
    }), { timeout: 90_000 }).toBeGreaterThan(0);
    await expect(page.locator("body")).toContainText("example/pages-fixture");
    const screenshot = testInfo.outputPath(`pages-indexeddb-${width}.png`);
    await page.screenshot({ path: screenshot, fullPage: true });
    await testInfo.attach(`Pages IndexedDB ${width}px`, { path: screenshot, contentType: "image/png" });
    await page.reload({ waitUntil: "domcontentloaded" });
    await expect(page.locator("#page-title")).toHaveText("Overview");
    await expect(page.locator(".first-load-overlay")).toHaveCount(0);
    expect(hostedRequests).toEqual([]);
    expect(errors).toEqual([]);
    for (const name of ["payload-hashes.json", "inventory-sources.json", "gh-aw-logs.sqlite"]) {
      const response = await page.request.get(`${preview.url}/${name}`);
      expect(response.ok(), name).toBe(true);
    }
    expect((await page.request.post(`${preview.url}/api/v1/query`, { data: {} })).status()).toBe(405);
    expect((await page.request.get(`${preview.url}/api/v1/query`)).status()).toBe(404);
  });
}
