import { writeCanvasArtifact } from './canvas-fixture.js';
import { mkdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
const { startCanvasServer } = await import(new URL('../../../../tests/helpers/canvas-snapshot.mjs', import.meta.url).href);

for (const width of [1440, 390]) {
  test(`Commands is absent without queue support at ${width}px`, async ({ page }, testInfo) => {
    const preview = await startCanvasServer({
      workingDirectory: fileURLToPath(new URL('../../../../', import.meta.url)),
      repository: 'acme/control', port: 0,
      output: () => {}, requestOutput: () => {}, traceOutput: () => {},
      prepareSnapshot: async (/** @type {string} */ destination) => {
        await mkdir(destination, { recursive: true });
        await writeCanvasArtifact(destination, { sources: {
          repositories: {
            metadata: { 'as-of': new Date().toISOString(), availability: 'available', completeness: 'complete', freshness: 'fresh' },
            rows: [{ organization: 'acme', repository: 'control' }]
          },
          workflows: {
            metadata: { 'as-of': new Date().toISOString(), availability: 'available', completeness: 'complete', freshness: 'fresh' },
            rows: [{ organization: 'acme', repository: 'control', workflow: 'demo.md', 'workflow-name': 'Demo' }]
          }
        } });
      }
    });
    /** @type {string[]} */
    const queueRequests = [];
    page.on('request', (request) => {
      if (request.url().includes('__cli_commands') || /dashboard-pages\/commands?\.json/.test(request.url())) {
        queueRequests.push(request.url());
      }
    });
    try {
      await page.setViewportSize({ width, height: 900 });
      await page.goto(`${preview.url}/#page-commands`);
      await expect(page.locator('#page-title')).toHaveText('Overview');
      await expect(page.locator('.first-load-overlay')).toHaveCount(0);
      await expect(page.locator('a[href="#page-commands"]')).toHaveCount(0);
      await expect(page.locator('[data-page-id="commands"], [data-page-id="command"]')).toHaveCount(0);
      if (width <= 700) await page.getByRole('button', { name: 'Select view', exact: true }).click();
      await expect(page.getByRole('link', { name: 'Commands', exact: true })).toHaveCount(0);
      await page.goto(`${preview.url}/#page-command?command=123`);
      await expect(page.locator('#page-title')).toHaveText('Overview');
      await expect(page.getByRole('dialog', { name: 'Command console log', exact: true })).toHaveCount(0);
      expect(queueRequests).toEqual([]);
      const screenshot = testInfo.outputPath(`commands-unavailable-${width}.png`);
      await page.screenshot({ path: screenshot, fullPage: true });
      await testInfo.attach(`Dashboard without command support (${width}px)`, { path: screenshot, contentType: 'image/png' });
    } finally {
      await page.goto('about:blank');
      await preview.close();
    }
  });

  test(`Commands retains detached work and reacts to completion at ${width}px`, async ({ page }, testInfo) => {
    test.setTimeout(90_000);
    /** @type {string[]} */
    const browserErrors = [];
    page.on('pageerror', (error) => browserErrors.push(error.message));
    page.on('requestfailed', (request) => browserErrors.push(`${new URL(request.url()).pathname.split('/').slice(2).join('/')}: ${request.failure()?.errorText}`));
    page.on('response', (response) => {
      if (response.status() >= 400) browserErrors.push(`${response.status()} ${new URL(response.url()).pathname.split('/').slice(2).join('/')}`);
    });
    let runs = 0;
    let cancellations = 0;
    /** @type {(() => void) | undefined} */
    let finish;
    const preview = await startCanvasServer({
      workingDirectory: fileURLToPath(new URL('../../../../', import.meta.url)),
      repository: 'acme/control', port: 0,
      approveCliAction: async () => true,
      cliQueueAdmission: async () => ({ concurrency: 1 }),
      output: () => {}, requestOutput: () => {}, traceOutput: () => {},
      lookupSelector: async () => [{ value: 'octo/example', label: 'octo/example' }],
      executeCliAction: async (/** @type {{ onOutput: (event: {stream: string, data: string}) => void, signal?: AbortSignal }} */ { onOutput, signal }) => {
        runs += 1;
        onOutput({ stream: 'stdout', data: 'Persistent command output\r\n' });
        onOutput({ stream: 'stdout', data: 'https://github.com/octo/example/pull/1\r\n' });
        onOutput({ stream: 'stdout', data: 'https://ghe.example.test:8443/github/octo/example/pull/42\r\n' });
        await new Promise((resolve) => {
          finish = () => resolve(undefined);
          signal?.addEventListener('abort', () => { cancellations += 1; resolve(undefined); }, { once: true });
        });
        return { ok: true, exitCode: 0 };
      },
      prepareSnapshot: async (/** @type {string} */ destination) => {
        await mkdir(destination, { recursive: true });
        await writeCanvasArtifact(destination, { sources: {
          repositories: {
            metadata: { 'as-of': new Date().toISOString(), availability: 'available', completeness: 'complete', freshness: 'fresh' },
            rows: [{ organization: 'acme', repository: 'control', 'gh-aw-version': 'v0.91.5', 'gh-aw-current-version': 'v0.91.5' }]
          },
          workflows: {
            metadata: { 'as-of': new Date().toISOString(), availability: 'available', completeness: 'complete', freshness: 'fresh' },
            rows: [{ organization: 'acme', repository: 'control', workflow: 'demo.md', 'workflow-name': 'Demo',
              'gh-aw-version': 'v0.91.5', 'gh-aw-current-version': 'v0.91.5' }]
          }
        } });
      }
    });
    try {
      await page.setViewportSize({ width, height: 900 });
      await page.goto(`${preview.url}/#page-maintenance`);
      await expect(page.locator('main')).toBeVisible({ timeout: 10_000 });
      await expect(page.locator('.cli-commands-navigation')).toBeHidden();
      await page.getByRole('button', { name: 'Add repository', exact: true }).click();
      const approval = page.getByRole('dialog', { name: 'Approve Add repository', exact: true });
      await approval.getByRole('searchbox', { name: 'Repository', exact: true }).fill('octo');
      await approval.getByRole('button', { name: 'octo/example', exact: true }).click();
      await approval.getByRole('button', { name: 'Add repository', exact: true }).click();
      await expect.poll(() => runs).toBe(1);
      const back = page.getByRole('button', { name: 'Back to Commands', exact: true });
      await expect(page.getByRole('region', { name: 'Command console log', exact: true })).toBeVisible();
      const activity = page.locator('.cli-commands-navigation');
      await expect(activity).not.toHaveAttribute('hidden', '');
      await expect(activity).toHaveText('');
      await expect(activity.locator('.octicon-terminal')).toHaveCount(1);
      const copy = page.getByRole('button', { name: 'Copy command output', exact: true });
      await expect(copy).toBeVisible();
      await expect(page.getByRole('button', { name: 'Cancel command', exact: true })).toHaveCount(0);
      expect(await copy.evaluate((button) => {
        const actions = button.parentElement;
        if (!actions) return false;
        const style = getComputedStyle(actions);
        const buttonStyle = getComputedStyle(button);
        return style.right === '24px' && style.bottom === '24px'
          && buttonStyle.backgroundColor !== 'rgba(0, 0, 0, 0)';
      })).toBe(true);
      await back.click();
      await expect(approval).not.toBeVisible();
      expect(cancellations).toBe(0);

      const history = page.locator('[data-view-id="commands-history"]');
      await expect(history.getByRole('heading', { name: 'Command queue', exact: true })).not.toBeVisible();
      await expect(page.locator('[data-view-id="commands-admission"]')).toHaveCount(0);
      await expect(history).toHaveAttribute('data-view-layout', 'full');
      await expect(history).toHaveAttribute('data-view-lazy-list', '');
      await expect(history.locator('.table-card-list')).toBeVisible();
      await expect(history.locator('.entity-card-list-badge')).toHaveCount(0);
      await expect(history.locator('.table-intent-button')).toHaveCount(0);
      await expect(history.getByRole('table')).toHaveCount(0);
      await expect(history).toContainText('running');
      await expect(history).toContainText('octo/example');
      await page.getByRole('button', { name: 'Back to Overview', exact: true }).click();
      await expect(page).toHaveURL(/#page-overview$/);
      await expect(page.locator('.mobile-history-back')).toBeHidden();
      if (width <= 700) await page.getByRole('button', { name: 'Select view', exact: true }).click();
      await activity.click();
      await expect(page).toHaveURL(/#page-commands$/);
      const command = history.getByRole('link', { name: 'gh cao repos add octo/example', exact: true }).first();
      await expect(command).toBeVisible();
      const queuedTime = history.locator('time').first();
      await expect(queuedTime).toBeVisible();
      await expect(queuedTime).toHaveAttribute('datetime', /^\d{4}-\d{2}-\d{2}T/);
      expect(await queuedTime.innerText()).not.toMatch(/^\d{4}-\d{2}-\d{2}T/);
      const commandHref = await command.getAttribute('href');
      expect(commandHref).toContain('#page-command?command=');
      finish?.();
      await expect(history).toContainText('completed');
      await expect(activity).toBeHidden();
      expect(runs).toBe(1);
      const screenshot = testInfo.outputPath(`commands-${width}.png`);
      await page.screenshot({ path: screenshot, fullPage: true });
      await testInfo.attach(`commands-${width}`, { path: screenshot, contentType: 'image/png' });

      await command.click();
      const consoleView = page.getByRole('region', { name: 'Command console log', exact: true });
      await expect(consoleView).toBeVisible();
      await expect(page.getByRole('dialog')).toHaveCount(0);
      await expect(page.locator('[data-view-id="command-terminal"]')).toHaveAttribute('data-view-layout', 'full-view');
      await expect(back).toBeVisible();
      const chromeFits = await back.evaluate((button) => {
        const bounds = button.getBoundingClientRect();
        return bounds.width >= 28 && bounds.left >= 0 && bounds.right <= window.innerWidth;
      });
      expect(chromeFits).toBe(true);
      const terminal = page.getByRole('region', { name: 'Read-only command terminal' });
      await expect(terminal).toBeVisible();
      await expect(terminal.locator('.xterm-accessibility-tree')).toContainText('Persistent command output');
      await expect(terminal.locator('.xterm-accessibility-tree [role="listitem"]').first())
        .toHaveText('$ gh cao repos add octo/example');
      const resultLink = consoleView.getByRole('link', { name: 'Open pull request', exact: true });
      await expect(resultLink).toHaveAttribute('href', 'https://ghe.example.test:8443/github/octo/example/pull/42');
      await page.context().grantPermissions(['clipboard-read', 'clipboard-write'], { origin: new URL(preview.url).origin });
      await consoleView.getByRole('button', { name: 'Copy pull request URL', exact: true }).click();
      expect(await page.evaluate(() => navigator.clipboard.readText()))
        .toBe('https://ghe.example.test:8443/github/octo/example/pull/42');
      await page.reload();
      await expect(consoleView).toBeVisible();
      await expect(page.getByRole('region', { name: 'Read-only command terminal' })).toBeVisible();
      await expect(consoleView.getByRole('button', { name: 'Reconnect output', exact: true })).toHaveCount(0);
      await expect(consoleView.getByRole('button', { name: 'Back to Commands', exact: true })).toHaveCount(0);
      await expect(terminal.locator('.xterm-accessibility-tree')).toContainText('Persistent command output');
      await expect(resultLink).toHaveAttribute('href', 'https://ghe.example.test:8443/github/octo/example/pull/42');
      const terminalScreenshot = testInfo.outputPath(`commands-console-${width}.png`);
      await page.screenshot({ path: terminalScreenshot, fullPage: true });
      await testInfo.attach(`commands-console-${width}`, { path: terminalScreenshot, contentType: 'image/png' });
      await back.click();
      await expect(page).toHaveURL(/#page-commands$/);
      await expect(consoleView).not.toBeVisible();
      await expect(history).toContainText('completed');
      await history.getByRole('link', { name: 'gh cao repos add octo/example', exact: true }).first().click();
      await expect(consoleView).toBeVisible();
      await back.click();
      await expect(page).toHaveURL(/#page-commands$/);
      expect(runs).toBe(1);
      expect(cancellations).toBe(0);

      await page.goto(`${preview.url}/#page-maintenance`);
      await page.getByRole('button', { name: 'Add repository', exact: true }).click();
      await approval.getByRole('searchbox', { name: 'Repository', exact: true }).fill('octo');
      await approval.getByRole('button', { name: 'octo/example', exact: true }).click();
      await approval.getByRole('button', { name: 'Add repository', exact: true }).click();
      await expect.poll(() => runs).toBe(2);
      finish?.();
      await expect(consoleView).toBeVisible();
      await expect(consoleView.getByRole('status')).toContainText('completed');
      await expect(page).toHaveURL(/#page-command\?command=\d+$/);
      await expect(back).toBeVisible();
      await expect(consoleView.getByRole('button', { name: 'Cancel command', exact: true })).not.toBeVisible();
      const completedScreenshot = testInfo.outputPath(`commands-completed-${width}.png`);
      await page.screenshot({ path: completedScreenshot, fullPage: true });
      await testInfo.attach(`commands-completed-${width}`, { path: completedScreenshot, contentType: 'image/png' });
      await back.click();
      await expect(consoleView).not.toBeVisible();
      expect(cancellations).toBe(0);
    } catch (error) {
      await testInfo.attach('commands-failure-content', {
        body: `${browserErrors.join('\n')}\n${await page.locator('main').innerText().catch(() => 'Page unavailable.')}`,
        contentType: 'text/plain'
      });
      throw error;
    } finally {
      finish?.();
      await page.goto('about:blank').catch(() => {});
      await preview.close();
    }
  });
}
