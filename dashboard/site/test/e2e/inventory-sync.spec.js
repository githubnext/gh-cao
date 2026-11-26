import { createServer } from 'node:http';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
import { writeServerArtifact } from './server-fixture.js';

const { startDashboardServer } = await import(new URL('../../../../tests/helpers/dashboard-server.mjs', import.meta.url).href);

for (const width of [1440, 390]) {
  test(`Updates Sync queues local collection and remains visible at ${width}px`, async ({ page }) => {
    test.setTimeout(120_000);
    const scratchRoot = fileURLToPath(new URL('../../../../.tmp/', import.meta.url));
    await mkdir(scratchRoot, { recursive: true });
    const directory = await mkdtemp(join(scratchRoot, 'inventory-sync-browser-'));
    const prefix = '/' + 'b'.repeat(48);
    let requests = 0;
    const adapter = createServer((request, response) => {
      if (request.method === 'POST' && request.url === prefix + '/ingest') {
        requests += 1;
        response.writeHead(202).end();
      } else {
        response.writeHead(404).end();
      }
    });
    await new Promise((resolve) => adapter.listen(0, '127.0.0.1', () => resolve(undefined)));
    const address = adapter.address();
    if (!address || typeof address === 'string') throw new Error('Adapter did not bind');
    const operatorConfig = join(directory, 'operator.json');
    await writeFile(operatorConfig, JSON.stringify({
      adapterUrl: `http://127.0.0.1:${address.port}${prefix}`,
      operatorActions: { repositories: ['octo/control'] },
      managedUpdates: true
    }));
    let preview;
    try {
      preview = await startDashboardServer({
        operatorConfig,
        prepareSnapshot: (/** @type {string} */ destination) => writeServerArtifact(destination, { sources: {
          repositories: { metadata: { 'as-of': new Date().toISOString() },
            rows: [{ organization: 'octo', repository: 'control' }] }
        } })
      });
      await page.setExtraHTTPHeaders(preview.headers);
      await page.setViewportSize({ width, height: 900 });
      for (const pageId of ['maintenance', 'maintenance-campaigns', 'maintenance-workflows']) {
        await page.goto(`${preview.url}/?access_token=${preview.headers.Authorization.slice('Bearer '.length)}#page-${pageId}`);
        const sync = page.getByRole('button', { name: 'Sync', exact: true });
        await expect(sync).toBeVisible();
        await expect(sync).toBeEnabled();
        await sync.click();
        await expect(page.locator('[aria-label^="Sync "] > [role="status"]').filter({ hasText: 'local collection pass completes' })).toBeVisible();
        expect(requests).toBe(['maintenance', 'maintenance-campaigns', 'maintenance-workflows'].indexOf(pageId) + 1);
        if (pageId === 'maintenance') {
          await expect(page.locator('[data-view-id="maintenance-repositories"]')).toContainText('octo/control');
          if (process.env.COPILOT_ARTIFACTS_DIR) {
            await page.screenshot({
              path: join(process.env.COPILOT_ARTIFACTS_DIR, `updates-sync-${width === 1440 ? 'desktop' : 'mobile'}.png`),
              fullPage: true
            });
          }
        }
      }
    } finally {
      await preview?.close();
      await new Promise((resolve) => adapter.close(() => resolve(undefined)));
      await rm(directory, { recursive: true, force: true });
    }
  });
}
