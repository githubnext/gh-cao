import { writeServerArtifact } from './server-fixture.js';
import { mkdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { expect, test } from "@playwright/test";
const { startDashboardServer } = await import(new URL("../../../../tests/helpers/dashboard-server.mjs", import.meta.url).href);

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  test(`server data unavailability preserves navigation and retry at ${viewport.width}px`, async ({ page }) => {
    const preview = await startDashboardServer({
      prepareSnapshot: (/** @type {string} */ destination) => writeServerArtifact(destination),
    });
    try {
      await page.setExtraHTTPHeaders(preview.headers);
      await page.setViewportSize(viewport);
      await page.addInitScript(() => {
        IDBFactory.prototype.open = () => {
          throw new Error("Unavailable server data must not fall back to browser IndexedDB.");
        };
      });
      let unavailable = true;
      await page.route("**/api/v1/query", (route) => unavailable
        ? route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({
            error: "dashboard data is unavailable",
            traceId: "4bf92f3577b34da6a3ce929d0e0e4736",
          }),
        })
        : route.continue());
      await page.goto(`${preview.url}/`);
      await expect(page.locator(".dashboard-root")).toBeVisible();
      await expect(page.getByText("Your factory status is unavailable.", { exact: true })).toBeVisible();
      await expect(page.getByRole("region", { name: "Notifications" })
        .getByText("A complete dashboard snapshot is not available yet.", { exact: true })).toBeVisible();
      await expect(page.getByRole("dialog", { name: "Preparing your dashboard" })).toHaveCount(0);
      unavailable = false;
      await page.getByRole("button", { name: "Retry", exact: true }).click();
      await expect(page.getByText("Your factory status is unavailable.", { exact: true })).toHaveCount(0);
      await expect(page.locator("#root .custom-view").first()).toBeVisible();
    } finally {
      await page.goto("about:blank");
      await preview.close();
    }
  });

  test(`local server queries PostgreSQL without opening browser IndexedDB at ${viewport.width}px`, async ({ page }, testInfo) => {
    let sourceDirectory = '';
    const preview = await startDashboardServer({
      workingDirectory: fileURLToPath(new URL("../../../../", import.meta.url)),
      repository: "acme/control",
      prepareSnapshot: async (/** @type {string} */ destination) => {
        sourceDirectory = destination;
        await mkdir(destination, { recursive: true });
        await writeServerArtifact(destination, { sources: {
          repositories: {
            metadata: {
              "as-of": new Date().toISOString(), availability: "available",
              completeness: "complete", freshness: "fresh",
            },
            rows: [{ organization: "acme", repository: "control" }],
          },
        } });
      },
    });
    try {
      await page.setExtraHTTPHeaders(preview.headers);
      await page.setViewportSize(viewport);
      await page.addInitScript(() => {
        let opens = 0;
        let importScreens = 0;
        Object.defineProperty(globalThis, "__indexedDbOpens", { get: () => opens });
        Object.defineProperty(globalThis, "__browserImportScreens", { get: () => importScreens });
        new MutationObserver((changes) => {
          for (const change of changes) {
            for (const node of change.addedNodes) {
              if (node instanceof Element && (node.matches('.first-load-overlay') || node.querySelector('.first-load-overlay'))) {
                importScreens += 1;
              }
            }
          }
        }).observe(document, { childList: true, subtree: true });
        IDBFactory.prototype.open = () => {
          opens += 1;
          throw new Error("The local server must not open browser IndexedDB.");
        };
      });
      const response = page.waitForResponse((response) => response.url().endsWith("/api/v1/query")
        && response.ok() && response.request().postDataJSON()?.sourceNames?.length > 0);
      await page.goto(`${preview.url}/`);
      const result = await (await response).json();
      expect(Object.keys(result.sources).length).toBeGreaterThan(0);
      await expect(page.locator('meta[name="dashboard-data-backend"]')).toHaveAttribute("content", "server-http");
      await expect(page.getByRole("dialog", { name: "Preparing your dashboard" })).toHaveCount(0);
      await expect(page.locator("#root .custom-view").first()).toBeVisible();
      expect(await page.evaluate(() => Object.getOwnPropertyDescriptor(globalThis, "__indexedDbOpens")?.get?.())).toBe(0);
      expect(await page.evaluate(() => Object.getOwnPropertyDescriptor(globalThis, "__browserImportScreens")?.get?.())).toBe(0);
      const repositories = await page.evaluate(async () => {
        const response = await fetch("/api/v1/query", {
          method: "POST", headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ sourceNames: ["repositories"] }),
        });
        if (!response.ok) throw new Error(`Canonical query failed: ${response.status}`);
        return response.json();
      });
      expect(repositories.sources.repositories.rows).toHaveLength(1);
      expect(repositories.sources.repositories.rows[0].repository).toBe("control");
      await page.goto(`${preview.url}/#page-repositories`);
      await expect(page.locator('a[href^="#page-repository-"]').first()).toBeVisible();
      await writeServerArtifact(sourceDirectory, { sources: {
        repositories: {
          metadata: {
            "as-of": new Date().toISOString(), availability: "available",
            completeness: "complete", freshness: "fresh",
          },
          rows: [
            { organization: "acme", repository: "control" },
            { organization: "acme", repository: "pushed-repository" },
          ],
        },
      } });
      await preview.ingestSnapshot();
      await expect(page.locator('a[href^="#page-repository-"]').filter({ hasText: "pushed-repository" }).first())
        .toBeVisible({ timeout: 20_000 });
      await page.screenshot({ path: testInfo.outputPath(`server-postgres-${viewport.width}.png`), fullPage: true });
      await testInfo.attach(`server-postgres-${viewport.width}`, {
        path: testInfo.outputPath(`server-postgres-${viewport.width}.png`), contentType: "image/png",
      });
    } finally {
      await page.goto("about:blank");
      await preview.close();
    }
  });
}
