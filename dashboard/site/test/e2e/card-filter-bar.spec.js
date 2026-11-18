import { selectViewMode } from './helpers/smoke-fixtures.js';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import contract from '../fixtures/card-filter-contract.json' with { type: 'json' };

const siteRoot = fileURLToPath(new URL('../..', import.meta.url));
const fixtureTimeOffset = Date.now() - Date.parse('2026-10-04T00:00:00Z');
/** @param {string} value */
const currentFixtureTime = (value) => new Date(Date.parse(value) + fixtureTimeOffset).toISOString();
const origin = 'http://card-filters.dashboard.test';
const dashboard = authoritativeDashboard.dashboard;
const issuesPage = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === contract.page);
const issuesView = issuesPage.definition.views.find((/** @type {{ id: string }} */ view) => view.id === contract.view);
const documentModel = {
  languageVersion: '0.1.0',
  dashboard: {
    ...dashboard,
    pages: [{
      id: 'issues', kind: 'custom', title: 'Issues', icon: 'issue-opened',
      views: [issuesView]
    }]
  }
};

/** @param {boolean} [updated] @param {boolean} [expanded] */
function sources(updated = false, expanded = false) {
  const metadata = { 'as-of': currentFixtureTime('2026-10-04T00:00:00Z'), 'artifact-generation': updated ? 'updated' : 'initial' };
  return {
    repositories: { rows: [{ organization: 'octo', repository: 'repo' }], metadata },
    workflows: { rows: [{ organization: 'octo', repository: 'repo', workflow: 'worker.md' }], metadata },
    runs: { rows: [{
      organization: 'octo', repository: 'repo', workflow: 'worker.md', run: '1', 'run-attempt': 1,
      'started-at': currentFixtureTime('2026-10-01T00:00:00Z'), 'run-status': 'completed', 'run-conclusion': 'success'
    }], metadata },
    issues: { rows: [
      ...contract.issues,
      ...(updated ? [{
        ...contract.issues[0], event: 'four', 'event-summary': 'New open issue',
        'correlation-id': 'https://github.com/octo/repo/issues/4'
      }] : []),
      ...(expanded ? Array.from({ length: 80 }, (_, index) => ({
        ...contract.issues[0], event: `extra-${index}`, 'event-summary': `Open issue ${index + 5}`,
        'correlation-id': `https://github.com/octo/repo/issues/${index + 5}`
      })) : [])
    ].map((row) => ({
      ...row, organization: 'octo', repository: 'repo', workflow: 'worker.md', run: '1',
      'run-attempt': 1, 'event-source': 'safe-output',
      'event-timestamp': currentFixtureTime(row['event-timestamp'])
    })), metadata }
  };
}

/** @param {import('@playwright/test').Page} page */
async function refreshSources(page) {
  await page.evaluate(async (model) => {
    const { refreshCanonicalDashboardSources } = await import(`${location.origin}/src/data-processor.js`);
    await refreshCanonicalDashboardSources(`${location.origin}/sources.json`, [], {
      pages: model.dashboard.pages, queries: model.dashboard.queries, githubUrlBase: 'https://github.com'
    });
  }, documentModel);
}

for (const mobile of [false, true]) {
  test(`declarative Status filter queries the worker and stays reactive on ${mobile ? 'mobile' : 'desktop'}`, async ({ context, page }) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    let updated = false;
    let expanded = false;
    await context.route(`${origin}/**`, async (route) => {
      const pathname = new URL(route.request().url()).pathname;
      if (pathname === '/') {
        await route.fulfill({ contentType: 'text/html', body: '<main id="root"></main>' });
      } else if (pathname === '/sources.json') {
        await route.fulfill({ contentType: 'application/json', body: JSON.stringify(sources(updated, expanded)) });
      } else {
        const path = join(siteRoot, pathname);
        await route.fulfill(existsSync(path)
          ? { contentType: pathname.endsWith('.json') ? 'application/json' : 'application/javascript', body: readFileSync(path) }
          : { status: 404 });
      }
    });
    await page.goto(`${origin}/#page-issues`);
    const scrollTestStyle = await page.addStyleTag({ content: '.card-filter-popover { max-height: 100px !important; }' });
    await page.evaluate(async (model) => {
      const { renderDashboard, dashboardPageSourceNames } = await import(`${location.origin}/src/presenter.js`);
      const { loadCanonicalDashboardSources, subscribeCanonicalDashboardView } = await import(`${location.origin}/src/data-processor.js`);
      const dashboardContext = {
        pages: model.dashboard.pages, queries: model.dashboard.queries, githubUrlBase: 'https://github.com'
      };
      await loadCanonicalDashboardSources(`${location.origin}/sources.json`, [], dashboardContext);
      /** @type {import('../../src/presenter.js').PageSourceLoader} */
      const loader = (pageId, options) => new Promise((resolve, reject) => {
        let initial = true;
        subscribeCanonicalDashboardView('issue-filter-page',
          dashboardPageSourceNames(model, pageId, options.queryContext?.viewMode),
          dashboardContext, (/** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ result) => {
            if (initial) { initial = false; resolve(result); }
            else options.onUpdate(result);
          }, undefined, { ...options, pageId, onError: reject });
      });
      const root = renderDashboard({ document: model, sources: {}, loadPageSources: loader });
      document.querySelector('#root')?.replaceChildren(root);
    }, documentModel);

    const view = page.locator(`[data-view-id="${contract.view}"]`);
    await expect(view.locator('tbody > tr[data-custom-row-key]')).toHaveCount(3);
    const statusMenu = view.locator('.card-filter-menu').filter({ has: page.locator('summary[aria-label^="Status"]') });
    const labelsMenu = view.locator('.card-filter-menu').filter({ has: page.locator('summary[aria-label^="Labels"]') });
    const summary = statusMenu.locator('summary');
    await summary.click();
    await statusMenu.getByRole('checkbox', { name: 'Closed: Completed', exact: true }).check();
    await statusMenu.getByRole('button', { name: 'Apply filters' }).click();
    await expect(view.locator('tbody > tr[data-custom-row-key]')).toHaveCount(1);
    await expect(view.locator('tbody')).toContainText('Closed issue');
    await view.getByRole('button', { name: 'Clear filters' }).click();
    await expect(view.locator('tbody > tr[data-custom-row-key]')).toHaveCount(3);
    if (mobile) {
      await page.getByRole('button', { name: 'Switch to Cards view' }).click();
    } else {
      await selectViewMode(page, 'card');
    }
    await expect(view.locator('.mobile-table-card-list')).toBeVisible();
    const cardList = view.locator('.mobile-table-card-list');
    await expect(cardList).toHaveCSS('border-radius', '14px');
    await expect(cardList.locator('.mobile-table-card-toolbar .card-filter-bar')).toBeVisible();
    await expect(page.locator('.dashboard-root')).toHaveClass(/dashboard-full-view/);
    await expect(view).toHaveCSS('padding-left', '0px');
    await expect(view).toHaveCSS('padding-right', '0px');
    await expect(view).toHaveCSS('padding-top', '16px');
    const viewBox = await view.boundingBox();
    const listBox = await cardList.boundingBox();
    expect(listBox?.x).toBe(viewBox?.x);
    expect(listBox?.width).toBe(viewBox?.width);
    expect((listBox?.y ?? 0) - (viewBox?.y ?? 0)).toBe(16);
    await expect(summary).toHaveCSS('border-radius', mobile ? '22px' : '6px');
    await expect(summary).toHaveCSS('border-top-width', '1px');
    if (mobile) expect((await summary.boundingBox())?.height).toBeGreaterThanOrEqual(44);
    await expect(cardList.locator('.mobile-table-card-list-items > li[data-custom-row-key]').first()).toHaveCSS('border-top-width', '0px');
    await summary.focus();
    await page.keyboard.press('Enter');
    await expect(view.getByRole('checkbox', { name: 'Open', exact: true })).toBeVisible();
    await view.getByRole('checkbox', { name: 'Open', exact: true }).check();
    await view.getByRole('button', { name: 'Apply filters' }).click();
    await expect(view.locator('.mobile-table-card-list-items > li[data-custom-row-key]')).toHaveCount(1);
    await expect(view.locator('.mobile-table-card-list-items')).toContainText('Open issue');
    await expect(summary).toBeFocused();
    await expect(summary).toHaveAttribute('aria-label', 'Status, 1 selected');
    if (!mobile) {
      await selectViewMode(page, 'table');
      await expect(view.locator('tbody > tr[data-custom-row-key]')).toHaveCount(1);
      await selectViewMode(page, 'card');
      await expect(view.locator('.mobile-table-card-list-items > li[data-custom-row-key]')).toHaveCount(1);
      await expect(summary).toHaveAttribute('aria-label', 'Status, 1 selected');
    }
    await summary.click();
    await expect(view.getByRole('checkbox', { name: 'Closed: Completed', exact: true })).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(summary).toBeFocused();
    await expect(statusMenu).not.toHaveAttribute('open');
    await labelsMenu.locator('summary').click();
    await view.getByRole('checkbox', { name: 'update_issue', exact: true }).check();
    const popover = labelsMenu.locator('.card-filter-popover');
    const scrollTop = await popover.evaluate((node) => {
      node.scrollTop = 60;
      return node.scrollTop;
    });
    expect(scrollTop).toBeGreaterThan(0);

    updated = true;
    await refreshSources(page);
    await expect(view.locator('.mobile-table-card-list-items > li[data-custom-row-key]')).toHaveCount(2);
    await expect(view.locator('.mobile-table-card-list-items')).toContainText('New open issue');
    await expect(view.getByRole('checkbox', { name: 'update_issue', exact: true })).toBeChecked();
    await expect(view.getByRole('checkbox', { name: 'update_issue', exact: true })).toBeFocused();
    await expect(labelsMenu).toHaveAttribute('open');
    await expect(popover).toHaveJSProperty('scrollTop', scrollTop);
    await view.getByRole('checkbox', { name: 'update_issue', exact: true }).uncheck();
    await page.keyboard.press('Escape');
    await expect(summary).toHaveAttribute('aria-label', 'Status, 1 selected');
    await labelsMenu.locator('summary').click();
    await view.getByRole('checkbox', { name: 'update_issue', exact: true }).check();
    await labelsMenu.getByRole('button', { name: 'Apply filters' }).click();
    await expect(view.locator('.mobile-table-card-list-items > li[data-custom-row-key]')).toHaveCount(0);
    await expect(view.getByRole('button', { name: 'Clear filters' })).toBeVisible();
    await summary.click();
    await expect(view.getByRole('checkbox', { name: 'Closed: Completed', exact: true })).toBeVisible();
    await page.keyboard.press('Escape');
    await view.getByRole('button', { name: 'Clear filters' }).click();
    await expect(view.locator('.mobile-table-card-list-items > li[data-custom-row-key]')).toHaveCount(4);
    await expect(summary).toHaveAttribute('aria-label', 'Status');
    await scrollTestStyle.evaluate((node) => node.parentNode?.removeChild(node));
    expanded = true;
    await refreshSources(page);
    const items = cardList.locator('.mobile-table-card-list-items');
    await expect(items.locator('li[data-custom-row-key]')).toHaveCount(25);
    const toolbar = cardList.locator('.mobile-table-card-toolbar');
    const beforeScroll = await toolbar.boundingBox();
    await items.evaluate((node) => { node.scrollTop = 500; });
    await expect(items).toHaveJSProperty('scrollTop', 500);
    await expect(toolbar).toBeInViewport();
    expect((await toolbar.boundingBox())?.y).toBe(beforeScroll?.y);
    await summary.click();
    await expect(statusMenu.getByRole('checkbox', { name: 'Open', exact: true })).toBeInViewport();
    const optionBox = await statusMenu.locator('.card-filter-popover').boundingBox();
    expect(optionBox?.x).toBeGreaterThanOrEqual(0);
    expect((optionBox?.x ?? 0) + (optionBox?.width ?? 0)).toBeLessThanOrEqual(mobile ? 390 : 1280);
    await expect(page.locator('body')).toHaveJSProperty('scrollWidth', await page.locator('body').evaluate((body) => body.clientWidth));
  });
}
