import { writeServerArtifact } from './server-fixture.js';
import { mkdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';

const { startDashboardServer } = await import(new URL('../../../../tests/helpers/dashboard-server.mjs', import.meta.url).href);

for (const width of [1440, 390]) {
  test(`Upgrade copy dialog contains clicks inside the repository card at ${width}px`, async ({ page }, testInfo) => {
    const preview = await startDashboardServer({
      workingDirectory: fileURLToPath(new URL('../../../../', import.meta.url)),
      repository: 'acme/control', caoUrl: 'https://ops.example.com/central/',
      prepareSnapshot: async (/** @type {string} */ destination) => {
        await mkdir(destination, { recursive: true });
        const metadata = { 'as-of': new Date().toISOString(), availability: 'available', completeness: 'complete', freshness: 'fresh' };
        await writeServerArtifact(destination, { sources: {
          repositories: { metadata, rows: [{ organization: 'octo', repository: 'example' }] },
          workflows: { metadata, rows: [{
            organization: 'octo', repository: 'example', workflow: 'demo.md', 'workflow-name': 'Demo',
            'gh-aw-version': 'v0.91.5', 'gh-aw-current-version': 'v0.92.0'
          }] }
        } });
      }
    });
    try {
      await page.setExtraHTTPHeaders(preview.headers);
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
    const preview = await startDashboardServer({
      workingDirectory: fileURLToPath(new URL('../../../../', import.meta.url)),
      repository: 'acme/control', caoUrl: 'https://ops.example.com/central/',
      prepareSnapshot: async (/** @type {string} */ destination) => {
        await mkdir(destination, { recursive: true });
        const metadata = { 'as-of': new Date().toISOString(), availability: 'available', completeness: 'complete', freshness: 'fresh' };
        await writeServerArtifact(destination, { sources: {
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
            { organization: 'acme', repository: 'control' },
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
              organization: 'octo', repository: 'outdated', workflow: 'review.md',
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
            },
            {
              organization: 'acme', repository: 'control', workflow: 'activity-worker.md',
              campaign: 'activity', 'workflow-name': 'Activity worker', 'workflow-role': 'worker',
              'workflow-source-kind': 'local', 'workflow-source': '',
              'gh-aw-version': 'v0.92.0', 'gh-aw-current-version': 'v0.92.0'
            },
            {
              organization: 'octo', repository: 'outdated', workflow: 'review-extended.md',
              'workflow-name': 'Review extended', 'workflow-role': 'standalone',
              'workflow-source-kind': 'local', 'workflow-source': '',
              'gh-aw-version': 'v0.92.0', 'gh-aw-current-version': 'v0.92.0'
            }
          ] }
        } });
      }
    });
    try {
      await page.setExtraHTTPHeaders(preview.headers);
      await page.setViewportSize({ width, height: 900 });
      await page.goto(`${preview.url}/#page-maintenance`);
      await page.evaluate(() => {
        Object.defineProperty(navigator, 'clipboard', {
          configurable: true,
          value: { writeText: async (/** @type {string} */ text) => { document.body.dataset.copiedCommand = text; } }
        });
      });
      const compilerPage = page.locator('#page-maintenance');
      await expect(compilerPage.getByRole('button', { name: 'Upgrade', exact: true })).toHaveCount(1);
      await expect(compilerPage.getByRole('button', { name: 'Upgrade selected repositories', exact: true })).toHaveCount(0);
      await expect(compilerPage.getByText('Installed gh-aw compiler version unavailable for one or more workflows.', { exact: true })).toBeVisible();
      await compilerPage.getByRole('button', { name: 'Upgrade', exact: true }).click();
      const upgradeDialog = page.getByRole('dialog', { name: 'Approve Upgrade', exact: true });
      await expect(upgradeDialog.locator('.cli-action-command')).toContainText('gh aw upgrade --repo octo/outdated');
      await upgradeDialog.getByRole('button', { name: 'Cancel', exact: true }).click();

      const unknownRepository = compilerPage.locator('.entity-card-list-card').filter({ has: page.getByText('octo/unknown', { exact: true }) });
      await unknownRepository.getByRole('button', { name: 'Disable', exact: true }).click();
      const disableRepositoryDialog = page.getByRole('dialog', { name: 'Approve Disable', exact: true });
      await expect(disableRepositoryDialog).toContainText('Cancel in-progress');
      await disableRepositoryDialog.getByRole('button', { name: 'Copy command', exact: true }).click();
      await expect(page.locator('body')).toHaveAttribute('data-copied-command', 'gh aw disable --repo octo/unknown');
      await disableRepositoryDialog.getByRole('button', { name: 'Cancel', exact: true }).click();
      await unknownRepository.getByRole('button', { name: 'Update', exact: true }).click();
      const repositoryUpdateDialog = page.getByRole('dialog', { name: 'Approve Update', exact: true });
      await expect(repositoryUpdateDialog.locator('.cli-action-command')).toHaveText('gh aw update --repo octo/unknown --create-pull-request');
      await repositoryUpdateDialog.getByRole('button', { name: 'Cancel', exact: true }).click();
      const repositoryScreenshot = testInfo.outputPath(`maintenance-repositories-${width}.png`);
      await page.screenshot({ path: repositoryScreenshot, fullPage: true });
      await testInfo.attach(`maintenance-repositories-${width}`, { path: repositoryScreenshot, contentType: 'image/png' });

      await compilerPage.getByRole('link', { name: 'Campaigns', exact: true }).click();
      const updatesPage = page.locator('#page-maintenance-campaigns');
      await expect(updatesPage.getByRole('button', { name: 'Update selected sources', exact: true })).toHaveCount(0);

      const campaignCard = updatesPage.locator('.entity-card-list-card').filter({ hasText: 'Activity' });
      await campaignCard.getByRole('button', { name: 'Update', exact: true }).click();
      const campaignDialog = page.getByRole('dialog', { name: 'Approve Update', exact: true });
      await expect(campaignDialog.locator('.cli-action-command'))
        .toContainText('gh aw update https://github.com/acme/catalog/activity --repo acme/control');
      await campaignDialog.getByRole('button', { name: 'Cancel', exact: true }).click();
      await campaignCard.getByRole('button', { name: 'Deactivate', exact: true }).click();
      const deactivateDialog = page.getByRole('dialog', { name: 'Approve Deactivate', exact: true });
      await expect(deactivateDialog).toContainText('Active runs continue');
      await deactivateDialog.getByRole('button', { name: 'Copy command', exact: true }).click();
      await expect(page.locator('body')).toHaveAttribute('data-copied-command', 'gh cao campaigns disable activity');
      await deactivateDialog.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect(updatesPage.getByText('Review', { exact: true })).toHaveCount(0);
      const campaignScreenshot = testInfo.outputPath(`maintenance-campaigns-${width}.png`);
      await page.screenshot({ path: campaignScreenshot, fullPage: true });
      await testInfo.attach(`maintenance-campaigns-${width}`, { path: campaignScreenshot, contentType: 'image/png' });
      await updatesPage.getByRole('link', { name: 'Workflows', exact: true }).click();
      const workflowsPage = page.locator('#page-maintenance-workflows');

      const workflowCard = workflowsPage.locator('.entity-card-list-card').filter({ has: page.getByText('Review', { exact: true }) });
      await workflowCard.getByRole('button', { name: 'Update', exact: true }).click();
      const workflowDialog = page.getByRole('dialog', { name: 'Approve Update', exact: true });
      await expect(workflowDialog.locator('.cli-action-command'))
        .toContainText('gh aw update review.md --repo octo/outdated --create-pull-request');
      await workflowDialog.getByRole('button', { name: 'Cancel', exact: true }).click();
      await workflowCard.getByRole('button', { name: 'Disable', exact: true }).click();
      const disableWorkflowDialog = page.getByRole('dialog', { name: 'Approve Disable', exact: true });
      await disableWorkflowDialog.getByRole('button', { name: 'Copy command', exact: true }).click();
      await expect(page.locator('body')).toHaveAttribute('data-copied-command', 'gh aw disable review.md --repo octo/outdated');
      await disableWorkflowDialog.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect(workflowCard.getByRole('button', { name: 'Disable', exact: true })).toBeFocused();
      const campaignWorkflowCard = workflowsPage.locator('.entity-card-list-card').filter({ has: page.getByText('Activity worker', { exact: true }) });
      await campaignWorkflowCard.getByRole('button', { name: 'Disable', exact: true }).click();
      const campaignWorkflowDialog = page.getByRole('dialog', { name: 'Approve Disable', exact: true });
      await expect(campaignWorkflowDialog.locator('.cli-action-command')).toHaveText('gh aw disable activity-worker.md --repo acme/control');
      await campaignWorkflowDialog.getByRole('button', { name: 'Cancel', exact: true }).click();

      const localCards = workflowsPage.locator('.entity-card-list-card').filter({ hasText: 'Local workflow' });
      await expect(localCards).toHaveCount(1);
      await expect(localCards.getByRole('button', { name: 'Update', exact: true })).toHaveCount(0);
      await expect(localCards.getByRole('button', { name: 'Disable', exact: true })).toHaveCount(1);
      await expect(workflowsPage.getByText('Source update unavailable; compiler upgrades apply to the whole repository.', { exact: true }).first()).toBeVisible();

      const screenshot = testInfo.outputPath(`maintenance-exact-coordinates-${width}.png`);
      await page.screenshot({ path: screenshot, fullPage: true });
      await testInfo.attach(`maintenance-exact-coordinates-${width}`, { path: screenshot, contentType: 'image/png' });
    } finally {
      await page.goto('about:blank').catch(() => {});
      await preview.close();
    }
  });
}
