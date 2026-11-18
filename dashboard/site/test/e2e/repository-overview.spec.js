import { authoritativeDashboard, expect, registerSmokeRoutes, selectViewMode, test } from './helpers/smoke-fixtures.js';

registerSmokeRoutes();

test('repository overview shares one worker subscription and refreshes metrics and health', async ({ page }) => {
  const dashboard = authoritativeDashboard.dashboard;
  const overview = dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === 'repository-detail');
  const repositories = dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === 'repositories');
  const model = { ...authoritativeDashboard, dashboard: { ...dashboard, pages: [repositories, overview] } };
  let updated = false;
  await page.route('http://dashboard.test/repository-sources.json', async route => {
    const now = new Date().toISOString();
    const identity = { organization: 'octo', repository: 'repo', workflow: '.github/workflows/worker.md' };
    const metadata = { 'as-of': now, 'artifact-generation': updated ? 'updated' : 'initial', completeness: 'complete', freshness: 'fresh' };
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify({
      repositories: { metadata, rows: [{
        organization: 'octo', repository: 'repo',
        'repository-link': { href: 'https://github.com/octo/repo', relation: 'repository' }
      }] },
      workflows: { metadata, rows: [{ ...identity, 'workflow-active': 'true' }] },
      runs: { metadata, rows: [
        { ...identity, run: '1', 'run-attempt': 1, 'started-at': now, 'run-conclusion': 'success', 'aic-total': 2 },
        ...(updated ? [{ ...identity, run: '2', 'run-attempt': 1, 'started-at': now, 'run-conclusion': 'failure', 'aic-total': 3 }] : [])
      ] },
      audits: { metadata, rows: [] }, tools: { metadata, rows: [] },
      domains: { metadata, rows: [] }, issues: { metadata, rows: [] }
    }) });
  });
  await page.evaluate(async documentModel => {
    const { renderDashboard, dashboardPageSourceNames } = await import(`${location.origin}/src/presenter.js`);
    const { loadCanonicalDashboardSources, subscribeCanonicalDashboardView } = await import(`${location.origin}/src/data-processor.js`);
    const context = { pages: documentModel.dashboard.pages, queries: documentModel.dashboard.queries, views: documentModel.dashboard.views };
    await loadCanonicalDashboardSources(`${location.origin}/repository-sources.json`, [], context);
    /** @type {import('../../src/presenter.js').PageSourceLoader} */
    const loader = (pageId, options) => new Promise((resolve, reject) => {
      let initial = true;
      subscribeCanonicalDashboardView('repository-overview', dashboardPageSourceNames(documentModel, pageId, options.queryContext?.viewMode), context,
        (/** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ sources) => {
          if (initial) { initial = false; resolve(sources); }
          else options.onUpdate(sources);
        }, undefined, { ...options, pageId, onError: reject });
    });
    loader.subscribeViewSources = () => { throw new Error('Unexpected independent view subscription.'); };
    location.hash = '#page-repository-detail?repository=octo%2Frepo';
    document.querySelector('#root')?.replaceChildren(renderDashboard({ document: documentModel, sources: {}, loadPageSources: loader }));
  }, model);
  const root = page.locator('[data-page-id="repository-detail"]');
  await expect(root.locator('[data-metric-value="workflows"]')).toHaveText('1');
  await expect(root.locator('[data-metric-value="runs"]')).toHaveText('1');
  await expect(root.locator('[data-metric-value="aic"]')).toHaveText('$0.02');
  updated = true;
  await page.evaluate(async documentModel => {
    const { refreshCanonicalDashboardSources } = await import(`${location.origin}/src/data-processor.js`);
    await refreshCanonicalDashboardSources(`${location.origin}/repository-sources.json`, [], {
      pages: documentModel.dashboard.pages, queries: documentModel.dashboard.queries, views: documentModel.dashboard.views
    });
  }, model);
  await expect(root.locator('[data-metric-value="runs"]')).toHaveText('2');
  await expect(root.locator('[data-metric-value="aic"]')).toHaveText('$0.06');
  await selectViewMode(page, 'table');
  await expect(root.locator('[data-view-id="repository-health-summary"]')).toContainText('Needs attention');
  await expect(root.locator('[data-view-id="repository-health-summary"]')).toContainText('50%');
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(root.locator('[data-view-id="repository-health-summary"]')).toContainText('octo/repo');
});
