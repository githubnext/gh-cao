import { writeCanvasArtifact } from './canvas-fixture.js';
import { mkdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
const { startCanvasServer } = await import(new URL('../../../canvas-server.mjs', import.meta.url).href);

for (const { width, height = 900, empty = false, crowded = false } of [
  { width: 1440 }, { width: 390 }, { width: 489 }, { width: 320 }, { width: 1440, empty: true },
  { width: 1440, height: 600, crowded: true }, { width: 390, height: 667, crowded: true },
  { width: 844, height: 390, crowded: true }
]) {
  test(`maintenance repository selector confirms exact enrollment at ${width}x${height}${empty ? ' with empty inventory' : ''}${crowded ? ' with delayed broad search' : ''}`, async ({ page }, testInfo) => {
    /** @type {Array<{ id: string, text: string }>} */
    const calls = [];
    /** @type {string[]} */
    const approvals = [];
    /** @type {string[]} */
    const executions = [];
    /** @type {(() => void) | undefined} */
    let finishSearch;
    const preview = await startCanvasServer({
      workingDirectory: fileURLToPath(new URL('../../../../', import.meta.url)),
      repository: 'acme/control', port: 0,
      cliQueuePath: testInfo.outputPath('.cao', 'cli-actions.sqlite'),
      cliQueueAdmission: async () => ({ concurrency: 1 }),
      output: () => {}, requestOutput: () => {}, traceOutput: () => {},
      lookupSelector: async (/** @type {{ id: string, selector: { query: { limit: number } } }} */ action, /** @type {string} */ text) => {
        calls.push({ id: action.id, text });
        const selected = { value: 'octo/example', label: 'octo/example', description: 'Selected repository' };
        if (!crowded) return [selected];
        await new Promise((resolve) => { finishSearch = () => resolve(undefined); });
        return Array.from({ length: 500 }, (_, index) => index === action.selector.query.limit - 1 ? selected : {
          value: `octo/repository-${index}`, label: `octo/repository-${index}`,
          description: 'Repository description that wraps across multiple lines on a narrow display.'
        }).slice(0, action.selector.query.limit);
      },
      approveCliAction: async (/** @type {{ command: string }} */ { command }) => { approvals.push(command); return true; },
      executeCliAction: async (/** @type {{ command: string, onOutput: (event: { stream: string, data: string }) => void }} */ { command, onOutput }) => {
        executions.push(command);
        onOutput({ stream: 'stdout', data: 'Repository staged in review mode.\n' });
        return { ok: true };
      },
      downloadData: async (/** @type {string} */ destination) => {
        await mkdir(destination, { recursive: true });
        await writeCanvasArtifact(destination, { sources: {
          campaigns: {
            metadata: { 'as-of': new Date().toISOString(), availability: 'available', completeness: 'complete', freshness: 'fresh' },
            rows: empty ? [] : [{
              campaign: 'demo', 'campaign-name': 'Demo campaign', 'campaign-version': 'v1',
              'campaign-current-version': 'v2', 'campaign-update-state': 'update-available',
              'campaign-registration': 'true', 'campaign-source': 'acme/catalog/demo'
            }]
          },
          repositories: {
            metadata: { 'as-of': new Date().toISOString(), availability: 'available', completeness: 'complete', freshness: 'fresh' },
            rows: [{ organization: 'acme', repository: 'control', 'gh-aw-version': 'v0.91.5', 'gh-aw-current-version': 'v0.91.5' }]
          },
          workflows: {
            metadata: { 'as-of': new Date().toISOString(), availability: 'available', completeness: 'complete', freshness: 'fresh' },
            rows: empty ? [] : [
              { organization: 'acme', repository: 'control', workflow: 'demo.md', 'workflow-name': 'Demo',
                'workflow-role': 'standalone', 'workflow-source-kind': 'source-managed',
                'workflow-source': 'acme/workflows/demo', 'workflow-source-version': 'v1',
                'workflow-source-current-version': 'v2', 'workflow-source-update-state': 'update-available',
                'gh-aw-version': 'v0.91.5', 'gh-aw-current-version': 'v0.91.5' },
              { organization: 'acme', repository: 'control', workflow: 'local.md', 'workflow-name': 'Local',
                'workflow-role': 'standalone', 'workflow-source-kind': 'local',
                'workflow-source': '', 'workflow-source-version': 'unknown',
                'workflow-source-current-version': 'unknown', 'workflow-source-update-state': 'unknown',
                'gh-aw-version': 'v0.91.5', 'gh-aw-current-version': 'v0.91.5' }
            ]
          }
        } });
      }
    });
    try {
      await page.addInitScript(() => {
        const start = document.startViewTransition?.bind(document);
        if (!start) return;
        document.startViewTransition = (update) => {
          const root = document.documentElement;
          root.dataset.lastTransitionKind = root.dataset.navigationTransition;
          root.dataset.lastTransitionDirection = root.dataset.navigationDirection ?? '';
          root.dataset.lastTransitionRootName = getComputedStyle(root).viewTransitionName;
          const body = document.querySelector('.dashboard-page:not([hidden]) > :is(.custom-view-grid, .page-layout-grid)');
          root.dataset.lastTransitionBodyName = body ? getComputedStyle(body).viewTransitionName : '';
          return start(update);
        };
      });
      await page.setViewportSize({ width, height });
      await page.goto(`${preview.url}/#page-maintenance`);
      const add = page.getByRole('button', { name: 'Add repository', exact: true });
      await expect(add).toBeVisible();
      const headers = page.locator('#page-maintenance .document-list-header');
      await expect(headers).toHaveCount(1);
      const tabs = page.locator('#page-maintenance [data-route-tabs]');
      await expect(tabs.getByRole('link', { name: 'Compiler upgrades', exact: true })).toHaveAttribute('aria-current', 'page');
      await expect(tabs.getByRole('link', { name: 'Workflow updates', exact: true })).toHaveAttribute('href', '#page-maintenance-campaigns');
      await expect(page.locator('#page-maintenance').getByRole('region', { name: 'Campaign packages', exact: true })).toHaveCount(0);
      await expect(page.locator('#page-maintenance .view-description-tooltip')).toHaveCount(0);
      for (const header of await headers.all()) {
        const geometry = await header.evaluate((element) => {
          const bounds = element.getBoundingClientRect();
          const description = element.querySelector(':scope > p')?.getBoundingClientRect();
          return {
            left: bounds.left, right: bounds.right,
            width: element.clientWidth, contentWidth: element.scrollWidth,
            description: description ? { left: description.left, right: description.right } : null,
            actions: [...element.querySelectorAll('.cli-action-trigger')].map((button) => {
              const box = button.getBoundingClientRect();
              const copy = button.querySelector('.cli-action-trigger-copy');
              return {
                left: box.left, right: box.right, width: box.width, height: box.height,
                name: button.getAttribute('aria-label'), title: button.getAttribute('title'),
                disabled: button.matches(':disabled'),
                labelDisplay: copy ? getComputedStyle(copy).display : null
              };
            })
          };
        });
        expect(geometry.contentWidth).toBeLessThanOrEqual(geometry.width);
        if (geometry.description) {
          expect(geometry.description.left).toBeGreaterThanOrEqual(geometry.left);
          expect(geometry.description.right).toBeLessThanOrEqual(geometry.right);
          if (width <= 700) expect(geometry.description.left).toBeGreaterThanOrEqual(geometry.left + 15);
        }
        for (const button of geometry.actions) {
          expect(button.left).toBeGreaterThanOrEqual(geometry.left);
          expect(button.right).toBeLessThanOrEqual(geometry.right);
          expect(button.name).toBeTruthy();
          if (button.disabled) expect(button.title).toBeTruthy();
          else expect(button.title).toBe(button.name);
          if (geometry.description) expect(geometry.description.right).toBeLessThanOrEqual(button.left);
          if (width <= 700) {
            expect(button.width).toBe(44);
            expect(button.height).toBeGreaterThanOrEqual(44);
            expect(button.labelDisplay).toBe('none');
          } else expect(button.labelDisplay).not.toBe('none');
        }
      }
      const repositories = page.getByRole('region', { name: 'Repository compiler status', exact: true });
      if (empty) await expect(repositories.getByText('No Agentic Workflow compiler versions were observed.')).toBeVisible();
      else await expect(repositories.getByText('acme/control', { exact: true }).first()).toBeVisible();
      if (!empty && !crowded && [1440, 390].includes(width)) {
        await page.screenshot({ path: testInfo.outputPath(`updates-repositories-${width}.png`), fullPage: true });
        await testInfo.attach(`updates-repositories-${width}`, { path: testInfo.outputPath(`updates-repositories-${width}.png`), contentType: 'image/png' });
      }
      await tabs.getByRole('link', { name: 'Compiler upgrades', exact: true }).focus();
      await page.keyboard.press('ArrowRight');
      const campaignsTab = tabs.getByRole('link', { name: 'Workflow updates', exact: true });
      await expect(campaignsTab).toBeFocused();
      await campaignsTab.press('Enter');
      const campaignPage = page.locator('#page-maintenance-campaigns');
      await expect(campaignPage).toBeVisible();
      const documentRoot = page.locator('html');
      await expect(documentRoot).toHaveAttribute('data-last-transition-kind', 'tab');
      await expect(documentRoot).toHaveAttribute('data-last-transition-direction', '');
      await expect(documentRoot).toHaveAttribute('data-last-transition-root-name', 'none');
      await expect(documentRoot).toHaveAttribute('data-last-transition-body-name', 'dashboard-tab-content');
      await expect(campaignPage.locator('[data-route-tabs]').getByRole('link', { name: 'Workflow updates', exact: true }))
        .toHaveAttribute('aria-current', 'page');
      const campaignPackages = campaignPage.getByRole('region', { name: 'Campaign packages', exact: true });
      const standaloneWorkflows = campaignPage.getByRole('region', { name: 'Standalone workflows', exact: true });
      await expect(campaignPackages).toBeVisible();
      await expect(standaloneWorkflows).toBeVisible();
      if (empty) {
        await expect(campaignPage.getByText('No campaign packages were observed.')).toBeVisible();
        await expect(campaignPage.getByText('No standalone workflow sources were observed.')).toBeVisible();
      }
      else {
        await expect(campaignPackages.getByText('Demo campaign', { exact: true }).first()).toBeVisible();
        await expect(campaignPackages.getByRole('button', { name: 'Update', exact: true })).toBeVisible();
        await expect(standaloneWorkflows.getByText('Demo', { exact: true }).first()).toBeVisible();
        await expect(standaloneWorkflows.getByRole('button', { name: 'Update', exact: true })).toBeVisible();
        await expect(standaloneWorkflows.getByText('Local workflow', { exact: true })).toBeVisible();
        await expect(standaloneWorkflows.getByText('No upstream source coordinate was retained; update manually.', { exact: true })).toBeVisible();
      }
      await expect(campaignPage.getByRole('button', { name: 'Add repository', exact: true })).toHaveCount(0);
      if (!empty && !crowded && [1440, 390].includes(width)) {
        await page.screenshot({ path: testInfo.outputPath(`updates-campaigns-${width}.png`), fullPage: true });
        await testInfo.attach(`updates-campaigns-${width}`, { path: testInfo.outputPath(`updates-campaigns-${width}.png`), contentType: 'image/png' });
      }
      await page.reload();
      await expect(campaignPage.locator('[data-route-tabs]')).toHaveAttribute('data-route-tabs-current', 'campaigns');
      await campaignPage.getByRole('link', { name: 'Compiler upgrades', exact: true }).click();
      await expect(add).toBeVisible();
      await expect(documentRoot).toHaveAttribute('data-last-transition-kind', 'tab');
      await page.goBack();
      await expect(campaignPage).toBeVisible();
      await expect(documentRoot).toHaveAttribute('data-last-transition-kind', 'tab');
      await expect(documentRoot).toHaveAttribute('data-last-transition-direction', '');
      await page.goForward();
      await expect(add).toBeVisible();
      await expect(documentRoot).toHaveAttribute('data-last-transition-kind', 'tab');
      await add.click();
      const dialog = page.getByRole('dialog', { name: 'Approve Add repository' });
      await expect(dialog.getByText(/Confirm the exact effect of/)).toHaveCount(0);
      const input = dialog.getByRole('searchbox', { name: 'Repository', exact: true });
      await expect(input).toBeFocused();
      const confirm = dialog.getByRole('button', { name: 'Add repository', exact: true });
      await expect(confirm).toBeDisabled();
      await input.fill('o');
      await page.waitForTimeout(350);
      expect(calls).toHaveLength(0);
      await input.fill('octo');
      const results = dialog.getByRole('group', { name: 'Search results' });
      if (crowded) {
        await expect(results).toHaveAttribute('aria-busy', 'true');
        await expect(dialog.locator('.dialog-selector-status')).toHaveText('Searching repositories...');
        await expect(input).toBeEnabled();
        await expect(confirm).toBeDisabled();
        await expect(dialog.getByRole('button', { name: 'Cancel', exact: true })).toBeEnabled();
        await expect(dialog.locator('.dialog-selector-status')).toContainText('taking longer than usual');
        finishSearch?.();
      }
      const option = dialog.getByRole('button', { name: 'octo/example Selected repository' });
      await expect(option).toBeVisible();
      expect(calls).toEqual([{ id: 'add-repository', text: 'octo' }]);
      if (crowded) {
        await expect(results.getByRole('button')).toHaveCount(20);
        await expect(dialog.locator('.dialog-selector-status')).toContainText('Showing the first 20 repositories. Refine your search');
        const dimensions = await results.evaluate((element) => ({
          visible: element.clientHeight, total: element.scrollHeight, viewport: window.innerHeight,
          overflow: getComputedStyle(element).overflowY
        }));
        expect(dimensions.visible).toBeGreaterThan(0);
        expect(dimensions.visible).toBeLessThanOrEqual(Math.min(280, Math.ceil(height * 0.35)));
        expect(dimensions.total).toBeGreaterThan(dimensions.visible);
        expect(dimensions.overflow).toBe('auto');
      }
      await option.focus();
      await page.keyboard.press('Enter');
      await expect(option).toHaveAttribute('aria-pressed', 'true');
      await expect(dialog.locator('.cli-action-command')).toHaveText('gh cao repos add octo/example');
      if (crowded) {
        expect(await results.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);
        const bounds = await dialog.boundingBox();
        if (!bounds) throw new Error('Repository dialog must remain visible.');
        expect(bounds.y).toBeGreaterThanOrEqual(0);
        expect(bounds.y + bounds.height).toBeLessThanOrEqual(height);
        await expect(confirm).toBeInViewport();
        await expect(dialog.getByRole('button', { name: 'Cancel', exact: true })).toBeInViewport();
      }
      expect(executions).toHaveLength(0);
      await page.screenshot({ path: testInfo.outputPath(`repository-selector-${width}.png`), fullPage: true });
      await testInfo.attach(`repository-selector-${width}`, { path: testInfo.outputPath(`repository-selector-${width}.png`), contentType: 'image/png' });
      await confirm.click();
      const consoleView = page.getByRole('region', { name: 'Command console log', exact: true });
      await expect(consoleView).toBeVisible();
      await expect(consoleView.getByRole('status')).toContainText('completed');
      expect(approvals).toEqual(['gh cao repos add octo/example']);
      expect(executions).toEqual(approvals);
      await page.getByRole('button', { name: 'Back to Commands', exact: true }).click();
      await page.goto(`${preview.url}/#page-maintenance`);
      await add.click();
      await expect(confirm).toBeDisabled();
      await expect(input).toHaveValue('');
      await page.keyboard.press('Escape');
      await expect(dialog).not.toBeVisible();
      await expect(add).toBeFocused();
      if (width <= 700) {
        await page.locator('.mobile-nav-menu > summary').click();
        await page.locator('[data-mobile-nav-page-id="configuration"]').click();
      } else await page.locator('[data-nav-page-id="configuration"]').first().click();
      await expect(page.locator('#page-configuration')).toBeVisible();
      await expect(documentRoot).toHaveAttribute('data-last-transition-kind', 'page');
      await expect(documentRoot).toHaveAttribute('data-last-transition-direction', 'forward');
    } finally {
      finishSearch?.();
      await page.goto('about:blank');
      await preview.close();
    }
  });
}
