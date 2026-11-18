import { authoritativeDashboard, expect, registerSmokeRoutes, selectViewMode, test } from './helpers/smoke-fixtures.js';

registerSmokeRoutes();

test('campaign repository topology stays subscribed without run/import inputs and preserves chart/table navigation', async ({ page }) => {
  const model = authoritativeDashboard;
  let updated = false;
  await page.route('http://dashboard.test/campaign-sources.json', async (route) => {
    const now = new Date().toISOString();
    const metadata = {
      'as-of': now, 'artifact-generation': updated ? 'updated' : 'initial',
      completeness: 'complete', freshness: 'fresh'
    };
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        campaigns: { rows: [{ campaign: 'alpha', 'campaign-name': 'Alpha' }, { campaign: 'empty' }], metadata },
        repositories: { rows: [{ organization: 'octo', repository: 'control' }], metadata },
        workflows: {
          rows: ['plan', 'worker', ...(updated ? ['second-worker'] : [])].map((workflow) => ({
            organization: 'octo', repository: 'control', workflow, campaign: 'alpha',
            'workflow-name': workflow, 'workflow-role': workflow === 'plan' ? 'orchestrator' : 'worker',
            'workflow-active': 'true', 'rollout-mode': 'review',
            'repository-link': { href: 'https://github.com/octo/control', relation: 'repository' }
          })),
          metadata
        }
      })
    });
  });
  await page.evaluate(async (documentModel) => {
    const { renderDashboard, dashboardPageSourceNames } = await import(`${location.origin}/src/presenter.js`);
    const { loadCanonicalDashboardSources, subscribeCanonicalDashboardView } = await import(`${location.origin}/src/data-processor.js`);
    const context = {
      pages: documentModel.dashboard.pages, queries: documentModel.dashboard.queries,
      views: documentModel.dashboard.views, githubUrlBase: 'https://github.com'
    };
    await loadCanonicalDashboardSources(`${location.origin}/campaign-sources.json`, [], context);
    /** @type {import('../../src/presenter.js').PageSourceLoader} */
    const loader = (pageId, options) => new Promise((resolve, reject) => {
      let initial = true;
      subscribeCanonicalDashboardView(
        'campaign-repository-inventory',
        dashboardPageSourceNames(documentModel, pageId, options.queryContext?.viewMode),
        context,
        (/** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ sources) => {
          if (initial) { initial = false; resolve(sources); }
          else options.onUpdate(sources);
        },
        undefined,
        { ...options, pageId, onError: reject }
      );
    });
    location.hash = '#page-campaign-repositories?campaign=alpha';
    document.querySelector('#root')?.replaceChildren(renderDashboard({
      document: documentModel, sources: {}, loadPageSources: loader
    }));
  }, model);

  const chart = page.locator('[data-view-id="campaign-repository-workflows"]');
  await expect(chart.getByRole('img', { name: 'octo/control: 2', exact: true })).toBeVisible();
  await expect(chart.locator('a[href="#page-repository-detail?repository=octo%2Fcontrol"]').first()).toBeVisible();
  await expect(page.locator('.campaign-tabs a')).toHaveCount(4);
  await expect(page.locator('.campaign-tabs a').first()).toHaveAttribute('href', '#page-campaign-insights?campaign=alpha');

  updated = true;
  await page.evaluate(async (documentModel) => {
    const { refreshCanonicalDashboardSources } = await import(`${location.origin}/src/data-processor.js`);
    await refreshCanonicalDashboardSources(`${location.origin}/campaign-sources.json`, [], {
      pages: documentModel.dashboard.pages, queries: documentModel.dashboard.queries,
      views: documentModel.dashboard.views, githubUrlBase: 'https://github.com'
    });
  }, model);
  await expect(chart.getByRole('img', { name: 'octo/control: 3', exact: true })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(chart.getByRole('img', { name: 'octo/control: 3', exact: true })).toBeVisible();
  await page.setViewportSize({ width: 1440, height: 1000 });
  await selectViewMode(page, 'table');
  const table = page.locator('[data-view-id="campaign-repository-table"]');
  await expect(table.locator('tbody > tr')).toHaveCount(3);
  await expect(table).toContainText('second-worker');
  await expect(table.locator('a[href="#page-repository-detail?repository=octo%2Fcontrol"]').first()).toBeVisible();

  await page.evaluate(() => { location.hash = '#page-campaign-repositories?campaign=empty'; });
  await expect(page.getByText('No repositories are associated with this campaign.', { exact: true })).toBeVisible();
  await expect(table.locator('tbody')).toHaveText('No repositories are associated with this campaign.');
  await expect(table.locator('tbody a')).toHaveCount(0);
  await expect(page.locator('.campaign-tabs a').first()).toHaveAttribute('href', '#page-campaign-insights?campaign=empty');
});
