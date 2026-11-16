import { createServer } from 'node:http';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
import { writeServerArtifact } from './server-fixture.js';
const { startDashboardServer } = await import(new URL('../../../../tests/helpers/dashboard-server.mjs', import.meta.url).href);

for (const { width, empty = false } of [{ width: 1440 }, { width: 390 }, { width: 1440, empty: true }]) {
  test(`Repositories resolves GitHub suggestions and previews wildcard enrollment at ${width}px${empty ? ' with empty inventory' : ''}`, async ({ page }, testInfo) => {
    test.setTimeout(120_000);
    const temporaryRoot = fileURLToPath(new URL('../../../../.tmp/', import.meta.url));
    await mkdir(temporaryRoot, { recursive: true });
    const directory = await mkdtemp(join(temporaryRoot, 'repository-configure-'));
    let executions = 0;
    /** @type {Array<{ text: string, limit: number }>} */
    const lookups = [];
    /** @type {string[]} */
    const canceledLookups = [];
    const adapter = createServer(async (request, response) => {
      if (request.url?.endsWith('/repositories/search')) {
        let body = '';
        for await (const chunk of request) body += chunk;
        const input = JSON.parse(body);
        lookups.push(input);
        if (input.text.startsWith('slow-')) {
          response.on('close', () => canceledLookups.push(input.text));
          return;
        }
        response.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify(
          input.text === 'octo/ex' ? [{ fullName: 'Octo/Example', description: 'Repository returned by GitHub' }] : []));
      } else if (request.url?.endsWith('/execute')) {
        executions += 1;
        response.writeHead(200, { 'Content-Type': 'application/x-ndjson' })
          .end('{"stream":"stderr","data":"Terminal approval declined.\\n"}\n{"complete":true,"exitCode":1}\n');
      } else {
        response.writeHead(404).end();
      }
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
      await page.goto(`${preview.url}/?access_token=${preview.headers.Authorization.slice('Bearer '.length)}#page-repositories`);
      await page.setExtraHTTPHeaders({});
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
      await expect(input).toHaveAttribute('spellcheck', 'false');
      await expect(input).toHaveAttribute('autocapitalize', 'none');
      await expect(input).toHaveAttribute('autocorrect', 'off');
      await expect(confirm).toBeDisabled();
      await input.pressSequentially('octo/ex', { delay: 30 });
      await expect(confirm).toBeDisabled();
      await dialog.getByRole('button', { name: /Octo\/Example Repository returned by GitHub/ }).click();
      expect(lookups).toEqual([{ text: 'octo/ex', limit: 20 }]);
      await expect(dialog.locator('.cli-action-command')).toHaveText('gh cao repos add Octo/Example');
      await expect(confirm).toBeEnabled();
      await input.fill('octo/missing');
      await expect(dialog.locator('.dialog-selector-status')).toHaveText('No repositories found.');
      await expect(dialog.locator('.dialog-selector-option')).toHaveCount(0);
      await expect(confirm).toBeDisabled();
      await input.fill('slow-switch');
      await expect.poll(() => lookups.at(-1)?.text).toBe('slow-switch');
      await input.fill('octo/service-*');
      await expect(confirm).toBeDisabled();
      await dialog.getByRole('button', { name: /octo\/service-\* Use wildcard/ }).click();
      await expect(dialog.locator('.cli-action-command')).toHaveText("gh cao repos add 'octo/service-*'");
      expect(executions).toBe(0);
      await expect.poll(() => canceledLookups).toContain('slow-switch');
      expect(lookups).toHaveLength(3);
      await page.screenshot({ path: testInfo.outputPath(`repository-pattern-${width}.png`), fullPage: true });
      await testInfo.attach(`repository-pattern-${width}`, { path: testInfo.outputPath(`repository-pattern-${width}.png`), contentType: 'image/png' });
      await expect(confirm).toBeInViewport();
      await input.fill('slow-close');
      await expect.poll(() => lookups.at(-1)?.text).toBe('slow-close');
      await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect.poll(() => canceledLookups).toContain('slow-close');
      await add.click();
      await input.fill('octo/service-*');
      await dialog.getByRole('button', { name: /octo\/service-\* Use wildcard/ }).click();
      const queued = page.waitForResponse((response) =>
        new URL(response.url()).pathname === '/__cli_commands' && response.request().method() === 'POST');
      await confirm.click();
      expect((await queued).status()).toBe(202);
      await expect.poll(() => executions).toBe(1);
    } finally {
      await page.goto('about:blank');
      await preview?.close();
      adapter.closeAllConnections();
      await new Promise((resolve) => adapter.close(resolve));
      await rm(directory, { recursive: true, force: true });
    }
  });
}
