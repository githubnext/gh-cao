import { writeServerArtifact } from './server-fixture.js';
import { mkdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { expect, test } from "@playwright/test";
const { startDashboardServer } = await import(new URL("../../../../tests/helpers/dashboard-server.mjs", import.meta.url).href);

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  test(`local server queries PostgreSQL without opening browser IndexedDB at ${viewport.width}px`, async ({ page }, testInfo) => {
    const preview = await startDashboardServer({
      workingDirectory: fileURLToPath(new URL("../../../../", import.meta.url)),
      repository: "acme/control",
      prepareSnapshot: async (/** @type {string} */ destination) => {
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
