import { createServer } from 'node:http';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
import { writeServerArtifact } from './server-fixture.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';

const { startDashboardServer } = await import(new URL('../../../../tests/helpers/dashboard-server.mjs', import.meta.url).href);

for (const width of [1440, 390]) {
  test(`managed repository Update runs upgrade outside collection scope and survives detachment at ${width}px`, async ({ page }, testInfo) => {
    test.setTimeout(90_000);
    const temporaryRoot = fileURLToPath(new URL('../../../../.tmp/', import.meta.url));
    await mkdir(temporaryRoot, { recursive: true });
    const directory = await mkdtemp(join(temporaryRoot, 'operator-browser-'));
    const prefix = '/' + 'b'.repeat(48);
    /** @type {string[][]} */
    const calls = [];
    let accessChecks = 0;
    /** @type {(() => void) | undefined} */
    let finish;
    /** @type {(() => void) | undefined} */
    let writeOutput;
    const adapter = createServer(async (request, response) => {
      if (request.url === prefix + '/repositories/octo/managed/update-access') {
        accessChecks += 1;
        response.writeHead(200, { 'Content-Type': 'application/json' }).end('{"allowed":true}');
        return;
      }
      if (request.url !== prefix + '/execute') {
        response.writeHead(404).end();
        return;
      }
      let body = '';
      for await (const chunk of request) body += chunk;
      calls.push(JSON.parse(body));
      response.writeHead(200, { 'Content-Type': 'application/x-ndjson' });
      await new Promise((resolve) => { writeOutput = () => resolve(undefined); });
      response.write(JSON.stringify({ stream: 'stdout', data: 'Native maintenance output\n' }) + '\n');
      await new Promise((resolve) => { finish = () => resolve(undefined); });
      response.end(JSON.stringify({ complete: true, exitCode: 0 }) + '\n');
    });
    await new Promise((resolve) => adapter.listen(0, '127.0.0.1', () => resolve(undefined)));
    const address = adapter.address();
    if (!address || typeof address === 'string') throw new Error('Native adapter did not bind.');
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
        cliActions: [authoritativeDashboard.dashboard['cli-actions'].find(
          (/** @type {{ id: string }} */ action) => action.id === 'update-repository-sources')],
        prepareSnapshot: (/** @type {string} */ destination) => writeServerArtifact(destination, { sources: {
          repositories: { metadata: { 'as-of': new Date().toISOString() },
            rows: [{ organization: 'octo', repository: 'managed' }, { organization: 'GitHub', repository: 'GH-AW' }] }
        } })
      });
      await page.setExtraHTTPHeaders(preview.headers);
      await page.setViewportSize({ width, height: 900 });
      await page.goto(`${preview.url}/#page-maintenance`);
      await expect(page.locator('[data-view-id="maintenance-repositories"]')).not.toContainText(/github\/gh-aw\b/i);
      expect(await page.evaluate(async () => {
        const response = await fetch('/api/v1/query', {
          method: 'POST', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ sourceNames: ['repositories'] })
        });
        const result = /** @type {{ sources: Record<string, { rows: Record<string, unknown>[] }> }} */ (await response.json());
        return result.sources.repositories.rows.some((row) =>
          String(row['repository-coordinate']).toLowerCase() === 'github/gh-aw');
      })).toBe(true);
      await page.getByRole('button', { name: 'Update', exact: true }).click();
      const dialog = page.getByRole('dialog', { name: 'Approve Update', exact: true });
      await expect(dialog.locator('.cli-action-command')).toHaveText('gh aw upgrade --repo octo/managed --create-pull-request');
      const queued = page.waitForResponse((response) =>
        new URL(response.url()).pathname === '/__cli_commands' && response.request().method() === 'POST');
      await dialog.getByRole('button', { name: 'Update', exact: true }).click();
      const admitted = await (await queued).json();
      const terminal = page.getByRole('region', { name: 'Read-only command terminal' });
      await expect(terminal).toBeVisible();
      await expect.poll(() => calls.length).toBe(1);
      expect(calls[0]).toEqual(['upgrade', '--repo', 'octo/managed', '--create-pull-request']);
      expect(accessChecks).toBeGreaterThanOrEqual(2);
      await expect(page.locator('.command-terminal [role="status"]')).toHaveText(`${admitted.id} · running`);
      writeOutput?.();
      await expect(terminal.locator('.xterm-accessibility-tree')).toContainText('Native maintenance output');
      await page.goto(`${preview.url}/#page-overview`);
      expect(calls).toHaveLength(1);
      finish?.();
      await page.goto(`${preview.url}/#page-commands`);
      await expect(page.locator('[data-view-id="commands-history"]')).toContainText('completed');
      await page.goto(`${preview.url}/#page-command?command=${admitted.id}`);
      await page.reload();
      await expect(terminal.locator('.xterm-accessibility-tree')).toContainText('Native maintenance output');
      expect(calls).toHaveLength(1);
      const screenshot = testInfo.outputPath(`native-command-${width}.png`);
      await page.screenshot({ path: screenshot, fullPage: true });
      await testInfo.attach(`native-command-${width}`, { path: screenshot, contentType: 'image/png' });
    } finally {
      writeOutput?.();
      finish?.();
      await page.goto('about:blank');
      await preview?.close();
      adapter.closeAllConnections();
      await new Promise((resolve) => adapter.close(resolve));
      await rm(directory, { recursive: true, force: true });
    }
  });
}
