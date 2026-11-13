import { writeServerArtifact } from './server-fixture.js';
import { mkdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
const { startDashboardServer } = await import(new URL('../../../../tests/helpers/dashboard-server.mjs', import.meta.url).href);

for (const width of [1440, 390]) {
  test(`Governance is hidden in local server development at ${width}px`, async ({ page }) => {
    const preview = await startDashboardServer({
      workingDirectory: fileURLToPath(new URL('../../../../', import.meta.url)),
      repository: 'acme/control',
      prepareSnapshot: async (/** @type {string} */ destination) => {
        await mkdir(destination, { recursive: true });
        await writeServerArtifact(destination, { sources: {
          repositories: { metadata: { 'as-of': new Date().toISOString() },
            rows: [{ organization: 'acme', repository: 'control' }] }
        } });
      }
    });
    try {
      await page.setExtraHTTPHeaders(preview.headers);
      await page.setViewportSize({ width, height: 900 });
      await page.goto(`${preview.url}/#page-governance`);
      await expect(page.locator('#page-title')).toHaveText('Overview', { timeout: 20_000 });
      await expect(page.locator('a[href="#page-governance"]')).toHaveCount(0);
      await expect(page.getByRole('region', { name: 'Compiler defaults editor' })).toHaveCount(0);
      await page.goto(`${preview.url}/?local-preview=admin#page-governance`);
      await expect(page.locator('#page-title')).toHaveText('Overview', { timeout: 20_000 });
      await expect(page.locator('a[href="#page-governance"]')).toHaveCount(0);
    } finally {
      await preview.close();
    }
  });
}
