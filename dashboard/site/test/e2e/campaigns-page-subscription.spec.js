import { authoritativeDashboard, expect, registerSmokeRoutes, test } from './helpers/smoke-fixtures.js';

registerSmokeRoutes();

test('page-bound Campaigns loads its second page through the production continuation loader', async ({ page }) => {
  const dashboard = authoritativeDashboard.dashboard;
  const campaigns = dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === 'campaigns');
  const model = { ...authoritativeDashboard, dashboard: { ...dashboard, pages: [campaigns] } };
  const metadata = { 'as-of': new Date().toISOString(), completeness: 'complete', freshness: 'fresh' };
  await page.route('http://dashboard.test/paged-campaign-sources.json', route => {
    return route.fulfill({ contentType: 'application/json', body: JSON.stringify({
      campaigns: { metadata, rows: Array.from({ length: 31 }, (_, index) => ({
        campaign: `campaign-${String(index).padStart(2, '0')}`,
        'campaign-name': `Campaign ${String(index).padStart(2, '0')}`,
        'campaign-mode': 'review', 'campaign-enabled': true
      })) },
      ...Object.fromEntries(['repositories', 'workflows', 'runs', 'audits', 'tools', 'domains', 'issues']
        .map(name => [name, { metadata, rows: [] }]))
    }) });
  });
  const result = await page.evaluate(async documentModel => {
    const { startDashboardData } = await import(`${location.origin}/src/data/startup.js`);
    const { loadCanonicalDashboardSources } = await import(`${location.origin}/src/data-processor.js`);
    const { dashboardDataStatus } = await import(`${location.origin}/src/dashboard-data-status.js`);
    const { effect } = await import(`${location.origin}/src/reactive.js`);
    const { dashboardPageSourceNames, dashboardPagePaginatedSourceBindings } = await import(`${location.origin}/src/presenter.js`);
    /** @type {import('../../src/presenter.js').PageSourceLoader | undefined} */
    let loader;
    const controller = new AbortController();
    const context = { pages: documentModel.dashboard.pages, queries: documentModel.dashboard.queries };
    await loadCanonicalDashboardSources(`${location.origin}/paged-campaign-sources.json`, [], context);
    /** @type {Parameters<typeof import('../../src/data/startup.js').startDashboardData>[0]} */
    const startupOptions = {
      browserWindow: window, document,
      sourceUrl: `${location.origin}/paged-campaign-sources.json`,
      dashboardContext: context,
      pageSourceNames: (id, mode) => dashboardPageSourceNames(documentModel, id, mode),
      pagePaginatedSourceBindings: id => dashboardPagePaginatedSourceBindings(documentModel, id),
      render: (_sources, _state, load) => { loader = load; }
    };
    const stop = await startDashboardData(startupOptions);
    try {
      await new Promise((resolve, reject) => {
        effect(() => {
          const phase = dashboardDataStatus.get().phase;
          if (phase === 'ready') resolve(undefined);
          else if (phase === 'stale') reject(new Error('Campaign fixture refresh failed.'));
        }, { signal: controller.signal });
      });
      if (!loader) throw new Error('Production page loader was not registered.');
      const sources = await loader('campaigns', {
        signal: controller.signal, onUpdate: () => {}, queryContext: { viewMode: 'table' }
      });
      const alias = 'view:campaigns:campaigns-inventory:campaign-inventory';
      const first = sources[alias];
      if (!first?.continuationToken || !first.loadContinuation) throw new Error('Missing Campaigns continuation.');
      const next = await first.loadContinuation(first.continuationToken);
      return {
        firstRows: first.rows.length, nextRows: next.rows.length,
        campaigns: [...first.rows, ...next.rows].map(row => row.campaign),
        queryName: next.metadata['query-name'], continuation: next.continuationToken
      };
    } finally {
      controller.abort();
      stop();
    }
  }, model);
  expect(result.firstRows).toBe(25);
  expect(result.nextRows).toBe(6);
  expect(result.campaigns).toEqual(Array.from({ length: 31 }, (_, index) => `campaign-${String(index).padStart(2, '0')}`));
  expect(result.queryName).toBe('view:campaigns:campaigns-inventory:campaign-inventory');
  expect(result.continuation).toBeUndefined();
});

test('Campaigns uses the shared page binding and refreshes its charts, table, and navigation', async ({ page }) => {
  const dashboard = authoritativeDashboard.dashboard;
  const campaigns = dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === 'campaigns');
  const model = { ...authoritativeDashboard, dashboard: { ...dashboard, pages: [campaigns] } };
  let updated = false;
  await page.route('http://dashboard.test/campaign-sources.json', async route => {
    const now = new Date().toISOString();
    const identity = { organization: 'octo', repository: 'control', workflow: 'worker' };
    const metadata = { 'as-of': now, 'artifact-generation': updated ? 'updated' : 'initial', completeness: 'complete', freshness: 'fresh' };
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify({
      campaigns: { metadata, rows: ['alpha', 'beta'].map(campaign => ({
        campaign, 'campaign-name': campaign, 'campaign-mode': 'review', 'campaign-enabled': true
      })) },
      repositories: { metadata, rows: [identity, { organization: 'octo', repository: 'target' }] },
      workflows: { metadata, rows: [{ ...identity, campaign: 'alpha', 'workflow-role': 'worker', 'workflow-active': 'true' }] },
      runs: { metadata, rows: [
        { ...identity, run: '1', 'run-attempt': 1, 'started-at': now, 'run-conclusion': 'success',
          event: 'workflow_dispatch', 'target-repository': 'octo/target', 'aic-total': 2 },
        ...(updated ? [{ ...identity, run: '2', 'run-attempt': 1, 'started-at': now, 'run-conclusion': 'success',
          event: 'workflow_dispatch', 'target-repository': 'octo/target', 'aic-total': 3 }] : [])
      ] },
      audits: { metadata, rows: [] }, tools: { metadata, rows: [] },
      domains: { metadata, rows: [] }, issues: { metadata, rows: [] }
    }) });
  });
  await page.evaluate(async documentModel => {
    const { renderDashboard, dashboardPageSourceNames } = await import(`${location.origin}/src/presenter.js`);
    const { loadCanonicalDashboardSources, subscribeCanonicalDashboardView } = await import(`${location.origin}/src/data-processor.js`);
    const context = { pages: documentModel.dashboard.pages, queries: documentModel.dashboard.queries, views: documentModel.dashboard.views };
    const sources = await loadCanonicalDashboardSources(`${location.origin}/campaign-sources.json`, ['campaign-navigation-availability'], context);
    Reflect.set(window, '__campaignPageBindings', 0);
    /** @type {import('../../src/presenter.js').PageSourceLoader} */
    const loader = (pageId, options) => new Promise((resolve, reject) => {
      Reflect.set(window, '__campaignPageBindings', Reflect.get(window, '__campaignPageBindings') + 1);
      let initial = true;
      subscribeCanonicalDashboardView('campaigns-page', dashboardPageSourceNames(documentModel, pageId, options.queryContext?.viewMode), context,
        (/** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ sources) => {
          if (initial) { initial = false; resolve(sources); }
          else options.onUpdate(sources);
        }, undefined, { ...options, pageId, onError: reject });
    });
    loader.subscribeViewSources = () => { throw new Error('Unexpected independent view subscription.'); };
    history.replaceState({}, '', '#page-campaigns');
    document.querySelector('#root')?.replaceChildren(renderDashboard({ document: documentModel, sources, loadPageSources: loader }));
  }, model);
  const root = page.locator('[data-page-id="campaigns"]');
  await expect(root.locator('[data-view-id="campaigns-dispatches"]')).toContainText('alpha');
  await expect(root.locator('[data-view-id="campaigns-repository-coverage"]')).toContainText('alpha');
  await expect(root.locator('[data-view-state="unavailable"]')).toHaveCount(0);
  expect(await page.evaluate(() => Reflect.get(window, '__campaignPageBindings'))).toBe(1);
  await root.locator('[data-view-mode-value="table"]').click();
  const row = root.locator('[data-view-id="campaigns-inventory"] tbody > tr').filter({ hasText: 'alpha' });
  await expect(row.getByRole('cell').nth(4)).toHaveText('1');
  await expect(row.getByRole('cell').nth(5)).toHaveText('1');
  await expect(row.getByRole('cell').nth(6)).toHaveText('$0.02');
  await expect(row.locator('a').first()).toHaveAttribute('href', '#page-campaign-insights?campaign=alpha');
  await expect(root.locator('[data-view-state="unavailable"]')).toHaveCount(0);
  updated = true;
  await page.evaluate(async documentModel => {
    const { refreshCanonicalDashboardSources } = await import(`${location.origin}/src/data-processor.js`);
    await refreshCanonicalDashboardSources(`${location.origin}/campaign-sources.json`, [], {
      pages: documentModel.dashboard.pages, queries: documentModel.dashboard.queries, views: documentModel.dashboard.views
    });
  }, model);
  await expect(row.getByRole('cell').nth(4)).toHaveText('2');
  await expect(row.getByRole('cell').nth(5)).toHaveText('2');
  await expect(row.getByRole('cell').nth(6)).toHaveText('$0.06');
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(row.locator('a').first()).toBeVisible();
  await page.getByRole('button', { name: 'Switch to Chart view', exact: true }).click();
  await expect(root.locator('[data-view-id="campaigns-dispatches"]')).toContainText('alpha');
  await expect(root.locator('[data-view-state="unavailable"]')).toHaveCount(0);
  expect(await page.evaluate(() => Reflect.get(window, '__campaignPageBindings'))).toBe(3);
});
