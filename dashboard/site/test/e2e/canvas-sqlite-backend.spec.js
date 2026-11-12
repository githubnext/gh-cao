import { writeCanvasArtifact } from './canvas-fixture.js';
import { mkdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { expect, test } from "@playwright/test";
const { startCanvasServer } = await import(new URL("../../../../tests/helpers/canvas-snapshot.mjs", import.meta.url).href);

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  test(`canvas queries SQLite without opening browser IndexedDB at ${viewport.width}px`, async ({ page }, testInfo) => {
    const preview = await startCanvasServer({
      workingDirectory: fileURLToPath(new URL("../../../../", import.meta.url)),
      repository: "acme/control",
      port: 0,
      cliQueuePath: testInfo.outputPath(".cao", "cli-actions.sqlite"),
      approveCliAction: async () => false,
      executeCliAction: async () => { throw new Error("CLI actions are not expected."); },
      output: () => {},
      requestOutput: () => {},
      traceOutput: () => {},
      prepareSnapshot: async (/** @type {string} */ destination) => {
        await mkdir(destination, { recursive: true });
        await writeCanvasArtifact(destination, { sources: {
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
          throw new Error("Canvas must not open browser IndexedDB.");
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
        const { queryRemoteDashboard } = await import(new URL("./src/remote-data-backend.js", location.href).href);
        return queryRemoteDashboard(["repositories"], { pages: [] });
      });
      expect(repositories.sources.repositories.rows).toHaveLength(1);
      expect(repositories.sources.repositories.rows[0].repository).toBe("control");
      await page.screenshot({ path: testInfo.outputPath(`canvas-sqlite-${viewport.width}.png`), fullPage: true });
      await testInfo.attach(`canvas-sqlite-${viewport.width}`, {
        path: testInfo.outputPath(`canvas-sqlite-${viewport.width}.png`), contentType: "image/png",
      });
    } finally {
      await page.goto("about:blank");
      await preview.close();
    }
  });
}
