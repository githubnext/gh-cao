import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
import { writeCanvasArtifact } from './canvas-fixture.js';
const { startCanvasServer } = await import(new URL('../../../../tests/helpers/canvas-snapshot.mjs', import.meta.url).href);

for (const { width, empty = false } of [{ width: 1440 }, { width: 390 }, { width: 1440, empty: true }]) {
  test(`Repositories configures exact repositories and wildcards at ${width}px${empty ? ' with empty inventory' : ''}`, async ({ page }, testInfo) => {
    test.setTimeout(120_000);
    /** @type {string[]} */
    const executions = [];
    const preview = await startCanvasServer({
      workingDirectory: fileURLToPath(new URL('../../../../', import.meta.url)),
      repository: 'acme/control', port: 0,
      cliQueueAdmission: async () => ({ concurrency: 1 }),
      output: () => {}, requestOutput: () => {}, traceOutput: () => {},
      approveCliAction: async () => true,
      executeCliAction: async (/** @type {{ command: string }} */ { command }) => {
        executions.push(command);
        return { ok: true, exitCode: 0, stdout: 'Repositories staged in review mode.' };
      },
      prepareSnapshot: async (/** @type {string} */ destination) => writeCanvasArtifact(destination, { sources: {
        repositories: {
          metadata: { 'as-of': new Date().toISOString(), availability: 'available', completeness: 'complete', freshness: 'fresh' },
          rows: empty ? [] : [{ organization: 'acme', repository: 'control' }]
        }
      } })
    });
    try {
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
      expect(executions).toEqual([]);
      await page.screenshot({ path: testInfo.outputPath(`repository-pattern-${width}.png`), fullPage: true });
      await testInfo.attach(`repository-pattern-${width}`, { path: testInfo.outputPath(`repository-pattern-${width}.png`), contentType: 'image/png' });
      await expect(confirm).toBeInViewport();
      await confirm.click();
      await expect(page.getByRole('region', { name: 'Command console log', exact: true }).getByRole('status')).toContainText('completed');
      expect(executions).toEqual(["gh cao repos add 'octo/service-*'"]);
    } finally {
      await page.goto('about:blank');
      await preview.close();
    }
  });
}
