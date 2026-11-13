import { createServer } from 'node:http';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
import { writeServerArtifact } from './server-fixture.js';

const { startDashboardServer } = await import(new URL('../../../../tests/helpers/dashboard-server.mjs', import.meta.url).href);

for (const width of [1440, 390]) {
  test(`server-owned maintenance survives browser detachment at ${width}px`, async ({ page }, testInfo) => {
    test.setTimeout(90_000);
    const directory = await mkdtemp(fileURLToPath(new URL('../../../../.tmp/operator-browser-', import.meta.url)));
    const prefix = '/' + 'b'.repeat(48);
    /** @type {string[][]} */
    const calls = [];
    /** @type {(() => void) | undefined} */
    let finish;
    const adapter = createServer(async (request, response) => {
      if (request.url !== prefix + '/execute') {
        response.writeHead(404).end();
        return;
      }
      let body = '';
      for await (const chunk of request) body += chunk;
      calls.push(JSON.parse(body));
      response.writeHead(200, { 'Content-Type': 'application/x-ndjson' });
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
      operatorActions: { repositories: ['octo/example'] }
    }));
    let preview;
    try {
      preview = await startDashboardServer({
        operatorConfig,
        cliActions: [{
          id: 'upgrade-repository', command: 'gh aw upgrade --repo {{repository}} --create-pull-request'
        }],
        prepareSnapshot: (/** @type {string} */ destination) => writeServerArtifact(destination, { sources: {
          repositories: { metadata: { 'as-of': new Date().toISOString() },
            rows: [{ organization: 'octo', repository: 'example' }] }
        } })
      });
      await page.setExtraHTTPHeaders(preview.headers);
      await page.setViewportSize({ width, height: 900 });
      await page.goto(`${preview.url}/#page-overview`);
      await expect(page.locator('#page-title')).toHaveText('Overview');
      const admitted = await page.evaluate(async () => {
        const response = await fetch('/__cli_commands', {
          method: 'POST', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ id: 'upgrade-repository', values: { repository: 'octo/example' } })
        });
        if (response.status !== 202) throw new Error(`Admission failed: ${response.status} ${await response.text()}`);
        return response.json();
      });
      await expect.poll(() => calls.length).toBe(1);
      expect(calls[0]).toEqual(['upgrade', '--repo', 'octo/example', '--create-pull-request']);
      await page.goto(`${preview.url}/#page-command?command=${admitted.id}`);
      const terminal = page.getByRole('region', { name: 'Read-only command terminal' });
      await expect(terminal).toBeVisible();
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
      finish?.();
      await page.goto('about:blank');
      await preview?.close();
      adapter.closeAllConnections();
      await new Promise((resolve) => adapter.close(resolve));
      await rm(directory, { recursive: true, force: true });
    }
  });
}
