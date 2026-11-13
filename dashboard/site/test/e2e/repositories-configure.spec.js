import { createServer } from 'node:http';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
import { writeServerArtifact } from './server-fixture.js';
const { startDashboardServer } = await import(new URL('../../../../tests/helpers/dashboard-server.mjs', import.meta.url).href);

for (const { width, empty = false } of [{ width: 1440 }, { width: 390 }, { width: 1440, empty: true }]) {
  test(`Repositories previews exact and wildcard enrollment without granting execution at ${width}px${empty ? ' with empty inventory' : ''}`, async ({ page }, testInfo) => {
    test.setTimeout(120_000);
    const directory = await mkdtemp(fileURLToPath(new URL('../../../../.tmp/repository-configure-', import.meta.url)));
    let executions = 0;
    const adapter = createServer((_request, response) => {
      executions += 1;
      response.writeHead(404).end();
    });
    await new Promise((resolve) => adapter.listen(0, '127.0.0.1', () => resolve(undefined)));
    const address = adapter.address();
    if (!address || typeof address === 'string') throw new Error('Native adapter did not bind.');
    const operatorConfig = join(directory, 'operator.json');
    await writeFile(operatorConfig, JSON.stringify({
      adapterUrl: `http://127.0.0.1:${address.port}/${'b'.repeat(48)}`,
      operatorActions: { repositories: ['acme/control'] }
    }));
    let preview;
    try {
      preview = await startDashboardServer({
        repository: 'acme/control', operatorConfig,
        prepareSnapshot: async (/** @type {string} */ destination) => writeServerArtifact(destination, { sources: {
          repositories: {
            metadata: { 'as-of': new Date().toISOString(), availability: 'available', completeness: 'complete', freshness: 'fresh' },
            rows: empty ? [] : [{ organization: 'acme', repository: 'control' }]
          }
        } })
      });
      await page.setExtraHTTPHeaders(preview.headers);
      await page.setViewportSize({ width, height: 900 });
      await page.goto(`${preview.url}/#page-repositories`);
      const add = page.getByRole('button', { name: 'Add repository', exact: true });
      await expect(add).toBeVisible({ timeout: 30_000 });
      await page.screenshot({ path: testInfo.outputPath(`repositories-${width}.png`), fullPage: true });
      await testInfo.attach(`repositories-${width}`, { path: testInfo.outputPath(`repositories-${width}.png`), contentType: 'image/png' });
      if (width <= 700) {
        await page.getByRole('button', { name: 'Switch to Cards view', exact: true }).click();
        await page.getByRole('button', { name: 'Switch to Table view', exact: true }).click();
      } else {
        await page.getByRole('button', { name: 'Table', exact: true }).click();
      }
      await expect(add).toBeVisible({ timeout: 30_000 });
      await add.click();
      const dialog = page.getByRole('dialog', { name: 'Approve Add repository' });
      const input = dialog.getByRole('searchbox', { name: 'Repository', exact: true });
      const confirm = dialog.getByRole('button', { name: 'Add repository', exact: true });
      await expect(input).toBeFocused();
      await expect(confirm).toBeDisabled();
      await input.fill('octo/example');
      await dialog.getByRole('button', { name: /octo\/example Use this exact repository/ }).click();
      await expect(dialog.locator('.cli-action-command')).toHaveText('gh cao repos add octo/example');
      await expect(confirm).toBeEnabled();
      await input.fill('octo/service-*');
      await expect(confirm).toBeDisabled();
      await dialog.getByRole('button', { name: /octo\/service-\* Use wildcard/ }).click();
      await expect(dialog.locator('.cli-action-command')).toHaveText("gh cao repos add 'octo/service-*'");
      expect(executions).toBe(0);
      await page.screenshot({ path: testInfo.outputPath(`repository-pattern-${width}.png`), fullPage: true });
      await testInfo.attach(`repository-pattern-${width}`, { path: testInfo.outputPath(`repository-pattern-${width}.png`), contentType: 'image/png' });
      await expect(confirm).toBeInViewport();
      const denied = page.waitForResponse((response) =>
        new URL(response.url()).pathname === '/__cli_commands' && response.request().method() === 'POST');
      await confirm.click();
      expect((await denied).status()).toBe(403);
      await expect(dialog.locator('.cli-action-status')).toContainText('Only declared repository maintenance actions are available.');
      await expect(page.getByRole('region', { name: 'Command console log', exact: true })).toHaveCount(0);
      expect(executions).toBe(0);
    } finally {
      await page.goto('about:blank');
      await preview?.close();
      adapter.closeAllConnections();
      await new Promise((resolve) => adapter.close(resolve));
      await rm(directory, { recursive: true, force: true });
    }
  });
}
