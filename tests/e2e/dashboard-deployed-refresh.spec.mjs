import { expect, test as baseTest } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import {
  activateTableViewMode,
  configureDeployedStorageQuota,
  DEPLOYED_INITIAL_IMPORT_TIMEOUT_MS,
  DEPLOYED_REFRESH_TIMEOUT_MS,
  deployedDashboardUrl as dashboardUrl,
  deployedStorageContext,
  populatedDashboardPages,
  populatedOverviewStations,
  scrollRenderedViewsIntoView,
  shouldIgnoreRequestFailure,
} from "./dashboard-deployed-refresh-helpers.mjs";

const test = baseTest.extend({ context: deployedStorageContext });
const outputDirectory = resolve("test-results/dashboard-deployed");

test("deployed dashboard refreshes and renders populated views", async ({ page }, testInfo) => {
  await mkdir(outputDirectory, { recursive: true });
  const browserErrors = [];
  const failedRequests = [];
  const succeededRequests = new WeakSet();
  let reloading = false;
  page.on("console", (message) => {
    if (message.type() === "error") browserErrors.push(message.text());
  });
  page.on("pageerror", (error) => browserErrors.push(error.message));
  page.on("requestfailed", (request) => {
    const errorText = request.failure()?.errorText || "failed";
    if (shouldIgnoreRequestFailure({
      method: request.method(),
      url: request.url(),
      errorText,
      reloading,
      hadSuccessResponse: succeededRequests.has(request),
    })) return;
    failedRequests.push(`${request.method()} ${request.url()}: ${errorText}`);
  });
  page.on("response", (response) => {
    if (response.status() >= 400) {
      failedRequests.push(`${response.status()} ${response.url()}`);
      return;
    }
    succeededRequests.add(response.request());
  });
  await page.addInitScript(() => {
    window.__dashboardTestEvents = [];
    for (const type of ["dashboard-data", "dashboard-render"]) {
      document.addEventListener(type, (event) => {
        window.__dashboardTestEvents.push({ type, detail: event.detail });
      });
    }
  });

  let diagnostics;
  let storageSession;
  try {
    storageSession = await configureDeployedStorageQuota(page, dashboardUrl);
    await page.goto(dashboardUrl, { waitUntil: "domcontentloaded" });
    await expect(page.locator(".dashboard-root")).toBeVisible({ timeout: 120_000 });
    await page.waitForFunction(() =>
      window.__dashboardTestEvents?.some(({ type, detail }) =>
        type === "dashboard-data" && detail?.status === "completed"
      ), null, { timeout: DEPLOYED_INITIAL_IMPORT_TIMEOUT_MS });
    reloading = true;
    try {
      await page.reload({ waitUntil: "domcontentloaded" });
    } finally {
      reloading = false;
    }
    await expect(page.locator(".dashboard-root")).toBeVisible({ timeout: 120_000 });
    await page.waitForFunction(() =>
      window.__dashboardTestEvents?.some(({ type, detail }) =>
        type === "dashboard-data"
        && detail?.kind === "refresh"
        && detail?.status === "completed"
      ), null, { timeout: DEPLOYED_REFRESH_TIMEOUT_MS });
    await expect(page.locator(".dashboard-root")).not.toHaveAttribute("aria-busy", "true");
    await expect(page.locator(".dashboard-stale, .source-refresh-error")).toHaveCount(0);

    // Overview has no single canonical store. Check populated inventory
    // denominators, not positive health or reach: either
    // percentage can legitimately be zero after successful ingestion.
    const overviewPage = page.locator('[data-page-id="overview"]');
    await expect(overviewPage).toBeVisible({ timeout: 120_000 });
    await page.waitForFunction(() =>
      window.__dashboardTestEvents?.some(({ type, detail }) =>
        type === "dashboard-render"
        && detail?.kind === "page"
        && detail?.pageId === "overview"
        && detail?.status === "completed"
      ), null, { timeout: 120_000 });
    await expect(overviewPage.locator('[aria-busy="true"]')).toHaveCount(0);
    for (const { href, detail } of populatedOverviewStations) {
      const station = overviewPage.locator(".factory-station").filter({
        has: page.locator(`a[href="${href}"]`),
      });
      await expect(station.locator("small"), "Overview should show a populated inventory denominator")
        .toHaveText(detail);
    }

    for (const { pageId } of populatedDashboardPages) {
      await page.evaluate((nextPageId) => {
        window.location.hash = `#page-${nextPageId}`;
      }, pageId);
      const activePage = page.locator(`[data-page-id="${pageId}"]`);
      await expect(activePage).toBeVisible({ timeout: 120_000 });
      await expect(activePage).not.toHaveAttribute("aria-busy", "true", { timeout: 120_000 });
      await page.waitForFunction((nextPageId) =>
        window.__dashboardTestEvents?.some(({ type, detail }) =>
          type === "dashboard-render"
          && detail?.kind === "page"
          && detail?.pageId === nextPageId
          && detail?.status === "completed"
        ), pageId, { timeout: 120_000 });
      if (await activateTableViewMode(activePage)) {
        await expect(activePage).toHaveAttribute("data-view-mode", "table");
      }
      await activePage.locator("details.view-disclosure").evaluateAll((details) => {
        for (const detail of details) {
          detail.open = true;
          detail.dispatchEvent(new Event("toggle"));
        }
      });
      await expect(async () => {
        await scrollRenderedViewsIntoView(activePage);
        expect(await activePage.locator("[data-lazy-view]").count()).toBe(0);
      }).toPass({ timeout: 60_000 });
      await expect(activePage.locator('[aria-busy="true"]')).toHaveCount(0, { timeout: 120_000 });
      await expect(activePage.locator('[aria-label^="Unable to load "]')).toHaveCount(0);
      const populatedRows = activePage.locator("tbody > tr").filter({ has: page.locator("td") });
      await expect(populatedRows.first(), `${pageId} should render populated rows`).toBeVisible();
    }

    diagnostics = await page.evaluate(() => window.collectFullDiagnostics());
    expect(diagnostics.passed, JSON.stringify(diagnostics.checks, null, 2)).toBe(true);
    for (const { storeName } of populatedDashboardPages) {
      expect(diagnostics.database.counts[storeName]).toBeGreaterThan(0);
    }
    expect(browserErrors).toEqual([]);
    expect(failedRequests).toEqual([]);
  } finally {
    await storageSession?.detach();
    const summary = {
      generatedAt: new Date().toISOString(),
      dashboardUrl,
      diagnostics: diagnostics ?? null,
      browserErrors,
      failedRequests,
      events: await page.evaluate(() => window.__dashboardTestEvents ?? []).catch(() => []),
    };
    const summaryPath = resolve(outputDirectory, "summary.json");
    await writeFile(summaryPath, `${JSON.stringify(summary, null, 2)}\n`);
    await testInfo.attach("dashboard-deployed-summary", {
      path: summaryPath,
      contentType: "application/json",
    });
  }
});
