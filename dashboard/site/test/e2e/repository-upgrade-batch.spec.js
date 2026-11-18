import { writeServerArtifact } from './server-fixture.js';
import { mkdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';

const { startDashboardServer } = await import(new URL('../../../../tests/helpers/dashboard-server.mjs', import.meta.url).href);

for (const width of [1440, 390]) {
  test(`Updates checkbox selection follows the filtered query at ${width}px`, async ({ page }, testInfo) => {
    test.setTimeout(120_000);
    const preview = await startDashboardServer({
      repository: 'acme/control',
      prepareSnapshot: async (/** @type {string} */ destination) => {
        const metadata = { 'as-of': new Date().toISOString(), availability: 'available', completeness: 'complete', freshness: 'fresh' };
        await writeServerArtifact(destination, { sources: {
          repositories: { metadata, rows: Array.from({ length: 63 }, (_, index) => ({
            organization: 'octo', repository: `repo-${String(index).padStart(3, '0')}`
          })) },
          workflows: { metadata, rows: [61, 62].map((index) => ({
            organization: 'octo', repository: `repo-${String(index).padStart(3, '0')}`,
            workflow: 'review.md', 'workflow-active': 'true'
          })) }
        } });
      }
    });
    try {
      await page.setExtraHTTPHeaders(preview.headers);
      await page.setViewportSize({ width, height: 900 });
      await page.goto(`${preview.url}/#page-maintenance`);
      const view = page.locator('[data-view-id="maintenance-repositories"]');
      const stateFilter = view.locator('.card-filter-menu').filter({ has: page.locator('summary[id$="-state"]') });
      await stateFilter.locator('summary').click();
      await stateFilter.getByRole('checkbox', { name: 'Unknown', exact: true }).check();
      await stateFilter.getByRole('button', { name: 'Apply filters', exact: true }).click();
      const cards = view.locator('.entity-card-list-card');
      await expect(cards).toHaveCount(25);
      await view.getByRole('button', { name: 'Select items', exact: true }).click();
      const first = cards.first().getByRole('checkbox');
      await first.focus();
      await page.keyboard.press('Space');
      await expect(first).toBeChecked();
      await expect(first).toBeFocused();
      await expect(page).toHaveURL(/#page-maintenance$/);
      await view.getByRole('checkbox', { name: 'Select all loaded items', exact: true }).check();
      const controls = view.locator('.entity-card-selection-controls');
      await expect(controls.getByRole('status')).toHaveText('25 selected');
      await controls.getByRole('button', { name: 'Select all 61', exact: true }).click();
      await expect(controls.getByRole('status')).toHaveText('61 selected');
      await expect(cards).toHaveCount(25);
      await view.getByRole('button', { name: 'Load more', exact: true }).click();
      await expect(cards).toHaveCount(50);
      await expect(cards.last().getByRole('checkbox')).toBeChecked();
      await first.uncheck();
      await expect(controls.getByRole('status')).toHaveText('60 selected');
      const screenshot = testInfo.outputPath(`updates-selection-${width}.png`);
      await page.screenshot({ path: screenshot, fullPage: true });
      await testInfo.attach(`updates-selection-${width}`, { path: screenshot, contentType: 'image/png' });
      await controls.getByRole('button', { name: 'Disable', exact: true }).click();
      const dialog = page.getByRole('dialog', { name: 'Approve Disable', exact: true });
      await expect(dialog).toBeVisible();
      const command = dialog.locator('.cli-action-command');
      await expect(command).toContainText('gh aw disable --repo octo/repo-060');
      await expect(command).not.toContainText('octo/repo-000');
      await expect(command).not.toContainText('octo/repo-061');
      await expect(command).not.toContainText('octo/repo-062');
      expect((await command.textContent())?.split('\n')).toHaveLength(60);
      await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
      await expect(controls.getByRole('button', { name: 'Disable', exact: true })).toBeFocused();
      await view.getByRole('button', { name: 'Cancel selection', exact: true }).click();
      await expect(first).not.toBeVisible();
    } finally {
      await page.goto('about:blank').catch(() => {});
      await preview.close();
    }
  });

  test(`Updates lazily render full-view cards for a 10,000-repository fleet at ${width}px`, async ({ page }, testInfo) => {
    test.setTimeout(120_000);
    const preview = await startDashboardServer({
      repository: 'acme/control',
      prepareSnapshot: async (/** @type {string} */ destination) => {
        const metadata = { 'as-of': new Date().toISOString(), availability: 'available', completeness: 'complete', freshness: 'fresh' };
        const names = Array.from({ length: 10_000 }, (_, i) => String(i).padStart(5, '0'));
        await writeServerArtifact(destination, { sources: {
          repositories: { metadata, rows: names.map((i) => ({ organization: 'octo', repository: `repo-${i}` })) },
          workflows: { metadata, rows: names.map((i) => ({
            organization: 'octo', repository: `repo-${i}`, workflow: `workflow-${i}.md`,
            'workflow-name': `Workflow ${i}`, 'workflow-source-kind': 'local'
          })) },
          campaigns: { metadata, rows: names.map((i) => ({
            campaign: `campaign-${i}`, 'campaign-name': `Campaign ${i}`, 'campaign-registration': 'true'
          })) }
        } });
      }
    });
    try {
      await page.setExtraHTTPHeaders(preview.headers);
      await page.setViewportSize({ width, height: 900 });
      await page.goto(`${preview.url}/#page-maintenance`);
      for (const [pageId, viewId, label, helpLabel] of [
        ['maintenance', 'maintenance-repositories', 'octo/repo-', 'About repository maintenance'],
        ['maintenance-workflows', 'maintenance-standalone-workflows', 'Workflow ', 'About workflow maintenance'],
        ['maintenance-campaigns', 'maintenance-campaigns', 'Campaign ', 'About campaign maintenance']
      ]) {
        if (pageId !== 'maintenance') {
          if (width < 768) await page.getByRole('button', { name: /^Select view/ }).click();
          await page.locator(width < 768 ? `[data-mobile-nav-page-id="${pageId}"]` : `[data-nav-section="Updates"] [data-nav-page-id="${pageId}"]`).click();
        }
        const view = page.locator(`[data-view-id="${viewId}"]`);
        await expect(view).toHaveAttribute('data-view-layout', 'full-view');
        await expect(page.locator('.dashboard-root')).toHaveClass(/dashboard-full-view/);
        await expect(view.locator('table')).toHaveCount(0);
        await expect(page.getByRole('button', { name: /Switch to .* view/ })).toHaveCount(0);
        await expect(view.locator('.entity-card-list')).toBeVisible();
        await expect(view.locator('.view-description')).toHaveCount(0);
        const help = page.locator('.title-area').getByRole('button', { name: helpLabel, exact: true });
        await expect(help).toBeVisible();
        if (width <= 700) {
          await help.click();
          await expect(page.getByRole('dialog', { name: helpLabel, exact: true })).toBeVisible();
          await page.keyboard.press('Escape');
        } else {
          await help.focus();
          await expect(page.locator('#page-help-tooltip')).toBeVisible();
          await help.blur();
        }
        const bounds = await view.boundingBox();
        if (!bounds) throw new Error('Missing full-view card list bounds');
        const mainBounds = await page.locator('.app-main').boundingBox();
        if (!mainBounds) throw new Error('Missing main content bounds');
        expect(bounds.x).toBe(mainBounds.x);
        expect(bounds.width).toBe(mainBounds.width);
        expect(bounds.y + bounds.height).toBeGreaterThanOrEqual(880);
        expect(bounds.y + bounds.height).toBeLessThanOrEqual(900);
        const rows = view.locator('.entity-card-list-card');
        await expect(rows).toHaveCount(25);
        await expect(rows.last()).toContainText(`${label}00024`);
        await expect(view.getByRole('button', { name: /^(Update|Upgrade) all/ })).toHaveCount(0);
        const more = view.getByRole('button', { name: 'Load more', exact: true });
        await more.click();
        await expect(rows.last()).toContainText(`${label}00049`);
        await expect(rows).toHaveCount(50);
        await view.locator('.entity-card-list').evaluate((element) => { element.scrollTop = element.scrollHeight; });
        await expect(rows.last()).toContainText(`${label}00074`);
        await expect(rows).toHaveCount(75);
        await expect(view.getByText(`${label}09999`, { exact: true })).toHaveCount(0);
        await view.locator('.entity-card-list').evaluate((element) => { element.scrollTop = 0; });
        await expect(page.locator('.dashboard-root')).not.toHaveClass(/dashboard-full-view-scrolled/);
        const screenshot = testInfo.outputPath(`updates-${pageId}-${width}.png`);
        await page.screenshot({ path: screenshot, fullPage: true });
        await testInfo.attach(`updates-${pageId}-${width}`, { path: screenshot, contentType: 'image/png' });
      }
    } finally {
      await page.goto('about:blank').catch(() => {});
      await preview.close();
    }
  });

  test(`Upgrade copy dialog preserves repository cards at ${width}px`, async ({ page }, testInfo) => {
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
      const repositoryLink = page.locator('[data-view-id="maintenance-repositories"] .entity-card-list-card a[href="https://github.com/octo/example"]').first();
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
      await expect(compilerPage.locator('[data-route-tabs]')).toHaveCount(0);
      await expect(compilerPage.getByRole('button', { name: 'Upgrade all repositories', exact: true })).toHaveCount(0);
      await expect(compilerPage.getByRole('button', { name: 'Upgrade', exact: true })).toHaveCount(1);
      await expect(compilerPage.getByRole('button', { name: 'Upgrade selected repositories', exact: true })).toHaveCount(0);
      await expect(compilerPage.getByText('Evidence', { exact: true })).toHaveCount(0);
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
      await expect(repositoryUpdateDialog.locator('.cli-action-command')).toHaveText('gh aw upgrade --repo octo/unknown --create-pull-request');
      await repositoryUpdateDialog.getByRole('button', { name: 'Cancel', exact: true }).click();
      const repositoryScreenshot = testInfo.outputPath(`maintenance-repositories-${width}.png`);
      await page.screenshot({ path: repositoryScreenshot, fullPage: true });
      await testInfo.attach(`maintenance-repositories-${width}`, { path: repositoryScreenshot, contentType: 'image/png' });

      if (width < 768) await page.getByRole('button', { name: /^Select view/ }).click();
      const campaignNavigation = page.locator(width < 768
        ? '[data-mobile-nav-page-id="maintenance-campaigns"]'
        : '[data-nav-section="Updates"] [data-nav-page-id="maintenance-campaigns"]');
      await campaignNavigation.click();
      const updatesPage = page.locator('#page-maintenance-campaigns');
      await expect(campaignNavigation).toHaveAttribute('aria-current', 'page');
      await expect(updatesPage.locator('[data-route-tabs]')).toHaveCount(0);
      await expect(updatesPage.getByRole('button', { name: 'Update selected sources', exact: true })).toHaveCount(0);
      await expect(updatesPage.getByRole('button', { name: 'Update all campaigns', exact: true })).toHaveCount(0);

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
      if (width < 768) await page.getByRole('button', { name: /^Select view/ }).click();
      const workflowNavigation = page.locator(width < 768
        ? '[data-mobile-nav-page-id="maintenance-workflows"]'
        : '[data-nav-section="Updates"] [data-nav-page-id="maintenance-workflows"]');
      await workflowNavigation.click();
      const workflowsPage = page.locator('#page-maintenance-workflows');
      await expect(workflowNavigation).toHaveAttribute('aria-current', 'page');
      await expect(workflowsPage.locator('[data-route-tabs]')).toHaveCount(0);
      await expect(workflowsPage.getByRole('button', { name: 'Update all workflows', exact: true })).toHaveCount(0);

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
      await expect(workflowsPage.getByText('Evidence', { exact: true })).toHaveCount(0);

      const screenshot = testInfo.outputPath(`maintenance-exact-coordinates-${width}.png`);
      await page.screenshot({ path: screenshot, fullPage: true });
      await testInfo.attach(`maintenance-exact-coordinates-${width}`, { path: screenshot, contentType: 'image/png' });
    } finally {
      await page.goto('about:blank').catch(() => {});
      await preview.close();
    }
  });
}
