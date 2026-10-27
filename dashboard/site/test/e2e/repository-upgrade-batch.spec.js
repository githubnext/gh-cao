import { writeCanvasArtifact } from './canvas-fixture.js';
import { mkdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';

const { startCanvasServer } = await import(new URL('../../../canvas-server.mjs', import.meta.url).href);

for (const width of [1440, 390]) {
  test(`Upgrade copy dialog contains clicks inside the repository card at ${width}px`, async ({ page }, testInfo) => {
    const preview = await startCanvasServer({
      workingDirectory: fileURLToPath(new URL('../../../../', import.meta.url)),
      repository: 'acme/control', caoUrl: 'https://ops.example.com/central/', port: 0,
      output: () => {}, requestOutput: () => {}, traceOutput: () => {},
      downloadData: async (/** @type {string} */ destination) => {
        await mkdir(destination, { recursive: true });
        const metadata = { 'as-of': new Date().toISOString(), availability: 'available', completeness: 'complete', freshness: 'fresh' };
        await writeCanvasArtifact(destination, { sources: {
          repositories: { metadata, rows: [{ organization: 'octo', repository: 'example' }] },
          workflows: { metadata, rows: [{
            organization: 'octo', repository: 'example', workflow: 'demo.md', 'workflow-name': 'Demo',
            'gh-aw-version': 'v0.91.5', 'gh-aw-current-version': 'v0.92.0'
          }] }
        } });
      }
    });
    try {
      await page.setViewportSize({ width, height: 900 });
      await page.goto(`${preview.url}/#page-maintenance`);
      const repositoryLink = page.locator('[data-view-id="maintenance-repositories"] [data-card-drill="external"]');
      await expect(repositoryLink).toHaveCount(1);
      await repositoryLink.evaluate((link) => {
        link.addEventListener('click', () => link.setAttribute('data-test-activated', 'true'));
      });
      await page.evaluate(() => {
        Object.defineProperty(navigator, 'clipboard', {
          configurable: true,
          value: { writeText: async (/** @type {string} */ text) => { document.body.dataset.copiedCommand = text; } }
        });
      });
      const trigger = page.getByRole('button', { name: 'Upgrade', exact: true });
      await trigger.click();
      const dialog = page.getByRole('dialog', { name: 'Approve Upgrade', exact: true });
      await expect(dialog).toBeVisible();
      await dialog.getByText('Upgrade Agentic Workflows in the selected repository and create a pull request.', { exact: true }).click();
      await expect(repositoryLink).not.toHaveAttribute('data-test-activated');
      await dialog.getByText('Include pre-releases', { exact: true }).click();
      await expect(dialog.getByRole('checkbox', { name: 'Include pre-releases' })).toBeChecked();
      await dialog.getByText('Auto merge', { exact: true }).click();
      await expect(dialog.getByRole('checkbox', { name: 'Auto merge' })).toBeChecked();
      await dialog.getByRole('button', { name: 'Copy command', exact: true }).click();
      await expect(dialog.locator('.cli-action-status')).toHaveText('Command copied.');
      await expect(page.locator('body')).toHaveAttribute(
        'data-copied-command',
        /gh aw upgrade --repo octo\/example.*--pre-releases.*--auto-merge/
      );
      await expect(repositoryLink).not.toHaveAttribute('data-test-activated');
      await expect(page).toHaveURL(/#page-maintenance$/);
      const screenshot = testInfo.outputPath(`upgrade-copy-dialog-${width}.png`);
      await page.screenshot({ path: screenshot, fullPage: true });
      await testInfo.attach(`upgrade-copy-dialog-${width}`, { path: screenshot, contentType: 'image/png' });
      await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect(dialog).not.toBeVisible();
      await expect(trigger).toBeFocused();
    } finally {
      await page.goto('about:blank').catch(() => {});
      await preview.close();
    }
  });

  test(`Maintenance actions use exact retained coordinates at ${width}px`, async ({ page }, testInfo) => {
    const preview = await startCanvasServer({
      workingDirectory: fileURLToPath(new URL('../../../../', import.meta.url)),
      repository: 'acme/control', caoUrl: 'https://ops.example.com/central/', port: 0,
      output: () => {}, requestOutput: () => {}, traceOutput: () => {},
      downloadData: async (/** @type {string} */ destination) => {
        await mkdir(destination, { recursive: true });
        const metadata = { 'as-of': new Date().toISOString(), availability: 'available', completeness: 'complete', freshness: 'fresh' };
        await writeCanvasArtifact(destination, { sources: {
          campaigns: { metadata, rows: [
            {
              campaign: 'activity', 'campaign-name': 'Activity', 'campaign-source': 'acme/catalog/activity',
              'campaign-version': 'v1', 'campaign-current-version': 'v2',
              'campaign-update-state': 'update-available', 'campaign-registration': 'true'
            },
            {
              campaign: 'local', 'campaign-name': 'Local package', 'campaign-source': '',
              'campaign-version': 'unknown', 'campaign-current-version': 'unknown',
              'campaign-update-state': 'unknown', 'campaign-registration': 'true'
            }
          ] },
          repositories: { metadata, rows: [
            { organization: 'octo', repository: 'outdated' },
            { organization: 'octo', repository: 'current' },
            { organization: 'octo', repository: 'unknown' }
          ] },
          workflows: { metadata, rows: [
            {
              organization: 'octo', repository: 'outdated', workflow: 'outdated.md',
              'workflow-name': 'Outdated', 'gh-aw-version': 'v0.91.5',
              'gh-aw-current-version': 'v0.92.0'
            },
            {
              organization: 'octo', repository: 'current', workflow: 'current.md',
              'workflow-name': 'Current', 'gh-aw-version': 'v0.92.0',
              'gh-aw-current-version': 'v0.92.0'
            },
            {
              organization: 'octo', repository: 'unknown', workflow: 'unknown.md',
              'workflow-name': 'Unknown', 'gh-aw-version': 'unknown',
              'gh-aw-current-version': 'v0.92.0'
            },
            {
              organization: 'acme', repository: 'control', workflow: 'review.md',
              'workflow-name': 'Review', 'workflow-role': 'standalone',
              'workflow-source-kind': 'source-managed', 'workflow-source': 'acme/workflows/review',
              'workflow-source-version': 'v1', 'workflow-source-current-version': 'v2',
              'workflow-source-update-state': 'update-available',
              'gh-aw-version': 'v0.92.0', 'gh-aw-current-version': 'v0.92.0'
            },
            {
              organization: 'acme', repository: 'control', workflow: 'local.md',
              'workflow-name': 'Local workflow', 'workflow-role': 'standalone',
              'workflow-source-kind': 'local', 'workflow-source': '',
              'workflow-source-version': 'unknown', 'workflow-source-current-version': 'unknown',
              'workflow-source-update-state': 'unknown',
              'gh-aw-version': 'v0.92.0', 'gh-aw-current-version': 'v0.92.0'
            }
          ] }
        } });
      }
    });
    try {
      await page.setViewportSize({ width, height: 900 });
      await page.goto(`${preview.url}/#page-maintenance`);
      const compilerPage = page.locator('#page-maintenance');
      await expect(compilerPage.getByRole('button', { name: 'Upgrade', exact: true })).toHaveCount(1);
      await expect(compilerPage.getByRole('button', { name: 'Upgrade selected repositories', exact: true })).toHaveCount(0);
      await expect(compilerPage.getByText('Installed gh-aw compiler version unavailable for one or more workflows.', { exact: true })).toBeVisible();
      await compilerPage.getByRole('button', { name: 'Upgrade', exact: true }).click();
      const upgradeDialog = page.getByRole('dialog', { name: 'Approve Upgrade', exact: true });
      await expect(upgradeDialog.locator('.cli-action-command')).toContainText('gh aw upgrade --repo octo/outdated');
      await upgradeDialog.getByRole('button', { name: 'Cancel', exact: true }).click();

      await compilerPage.getByRole('link', { name: 'Workflow updates', exact: true }).click();
      const updatesPage = page.locator('#page-maintenance-campaigns');
      await expect(updatesPage.getByRole('button', { name: 'Update selected sources', exact: true })).toHaveCount(0);

      const campaignCard = updatesPage.locator('.entity-card-list-card').filter({ hasText: 'Activity' });
      await campaignCard.getByRole('button', { name: 'Update', exact: true }).click();
      const campaignDialog = page.getByRole('dialog', { name: 'Approve Update', exact: true });
      await expect(campaignDialog.locator('.cli-action-command'))
        .toContainText('gh aw update https://github.com/acme/catalog/activity --repo acme/control');
      await campaignDialog.getByRole('button', { name: 'Cancel', exact: true }).click();

      const workflowCard = updatesPage.locator('.entity-card-list-card').filter({ hasText: 'Review' });
      await workflowCard.getByRole('button', { name: 'Update', exact: true }).click();
      const workflowDialog = page.getByRole('dialog', { name: 'Approve Update', exact: true });
      await expect(workflowDialog.locator('.cli-action-command'))
        .toContainText('gh aw update https://github.com/acme/workflows/review --repo acme/control');
      await workflowDialog.getByRole('button', { name: 'Cancel', exact: true }).click();

      const localCards = updatesPage.locator('.entity-card-list-card').filter({ hasText: 'Local' });
      await expect(localCards).toHaveCount(2);
      await expect(localCards.getByRole('button', { name: 'Update', exact: true })).toHaveCount(0);
      await expect(updatesPage.getByText('No upstream source coordinate was retained; update manually.', { exact: true })).toBeVisible();

      const screenshot = testInfo.outputPath(`maintenance-exact-coordinates-${width}.png`);
      await page.screenshot({ path: screenshot, fullPage: true });
      await testInfo.attach(`maintenance-exact-coordinates-${width}`, { path: screenshot, contentType: 'image/png' });
    } finally {
      await page.goto('about:blank').catch(() => {});
      await preview.close();
    }
  });
}
