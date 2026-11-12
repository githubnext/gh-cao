import { writeCanvasArtifact } from './canvas-fixture.js';
import { mkdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
const { startCanvasServer } = await import(new URL('../../../../tests/helpers/canvas-snapshot.mjs', import.meta.url).href);

for (const width of [1440, 390]) {
  test(`Governance is hidden in canvas development at ${width}px`, async ({ page }) => {
    const preview = await startCanvasServer({
      workingDirectory: fileURLToPath(new URL('../../../../', import.meta.url)),
      repository: 'acme/control', port: 0,
      cliQueueAdmission: async () => ({ concurrency: 1 }),
      output: () => {}, requestOutput: () => {}, traceOutput: () => {},
      approveCliAction: async () => false,
      executeCliAction: async () => { throw new Error('Hidden Governance must not execute commands.'); },
      prepareSnapshot: async (/** @type {string} */ destination) => {
        await mkdir(destination, { recursive: true });
        await writeCanvasArtifact(destination, { sources: {
          repositories: { metadata: { 'as-of': new Date().toISOString() },
            rows: [{ organization: 'acme', repository: 'control' }] }
        } });
      }
    });
    try {
      await page.setViewportSize({ width, height: 900 });
      await page.goto(`${preview.url}/#page-governance`);
      await expect(page.locator('#page-title')).toHaveText('Overview');
      await expect(page.locator('a[href="#page-governance"]')).toHaveCount(0);
      await expect(page.getByRole('region', { name: 'Compiler defaults editor' })).toHaveCount(0);
      await page.goto(`${preview.url}/?local-preview=admin#page-governance`);
      await expect(page.locator('#page-title')).toHaveText('Overview');
      await expect(page.locator('a[href="#page-governance"]')).toHaveCount(0);
    } finally {
      await preview.close();
    }
  });
}
