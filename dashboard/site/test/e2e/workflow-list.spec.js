import { authoritativeDashboard, expect, registerSmokeRoutes, test } from './helpers/smoke-fixtures.js';

registerSmokeRoutes();

for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
  test(`workflow list shows latest status and opens its dedicated page at ${viewport.width}px`, async ({ page, context }) => {
    await page.setViewportSize(viewport);
    const now = Date.now();
    const at = (/** @type {number} */ hoursAgo) => new Date(now - hoursAgo * 3_600_000).toISOString();
    const metadata = { 'as-of': at(0), 'retrieved-at': at(0) };
    const sources = {
      repositories: { metadata, rows: [{ organization: 'org', repository: 'control' }] },
      workflows: {
        metadata,
        rows: ['idle', 'worker', 'recent'].map((name) => ({
          organization: 'org', repository: 'control',
          workflow: `.github/workflows/${name}.md`, 'workflow-name': name,
          'workflow-active': 'true', 'workflow-role': 'standalone', 'rollout-mode': 'review'
        }))
      },
      runs: {
        metadata,
        rows: [
          { workflow: '.github/workflows/worker.md', run: '100', 'started-at': at(25), 'run-conclusion': 'success' },
          { workflow: '.github/workflows/worker.md', run: '101', 'started-at': at(2), 'run-conclusion': 'failure' },
          { workflow: '.github/workflows/recent.md', run: '200', 'started-at': at(1), 'run-conclusion': 'success' }
        ].map((row) => ({ organization: 'org', repository: 'control', 'run-attempt': 1, 'run-status': 'completed', ...row }))
      }
    };
    await context.route('http://dashboard.test/sources/manifest.json', (route) => route.fulfill({ status: 404 }));
    await context.route('http://dashboard.test/workflow-evidence.json', (route) => route.fulfill({
      contentType: 'application/json', body: JSON.stringify(sources)
    }));
    await page.evaluate(async ({ dashboard }) => {
      const { renderDashboard, dashboardPageSourceNames } = /** @type {typeof import('../../src/presenter.js')} */ (await import(`${location.origin}/src/presenter.js`));
      const { loadCanonicalDashboardSources, loadCanonicalDashboardPage, subscribeWorkerLoadingProgress } = /** @type {typeof import('../../src/data-processor.js')} */ (await import(`${location.origin}/src/data-processor.js`));
      const { setLoadingProgressState } = /** @type {typeof import('../../src/loading-progress.js')} */ (await import(`${location.origin}/src/loading-progress.js`));
      window.localStorage.setItem('central-agentic-ops.dashboard.horizon-filter-settings', JSON.stringify({ range: 'all' }));
      window.location.hash = '#page-workflows';
      subscribeWorkerLoadingProgress((state) => setLoadingProgressState(document, state));
      const model = { languageVersion: dashboard['language-version'], dashboard: dashboard.dashboard };
      const queryContext = { pages: model.dashboard.pages, queries: model.dashboard.queries, views: model.dashboard.views };
      await loadCanonicalDashboardSources(`${location.origin}/workflow-evidence.json`, [], queryContext);
      const loadPageSources = (
        /** @type {string} */ pageId,
        /** @type {import('../../src/presenter.js').PageSourceLoadOptions} */ options
      ) => loadCanonicalDashboardPage(
        dashboardPageSourceNames(model, pageId), queryContext, undefined,
        { pageId, routeParameters: options.routeParameters, queryContext: options.queryContext }
      );
      document.querySelector('#root')?.append(renderDashboard({ document: model, sources: {}, loadPageSources }));
    }, { dashboard: authoritativeDashboard });

    const view = page.locator('[data-view-id="workflows-inventory"]');
    if (viewport.width < 600) {
      await page.locator('.mobile-view-mode-toggle').click();
      const cards = view.locator('[data-mobile-card-list] .entity-card-list-card');
      await expect(cards).toHaveCount(3);
      await expect(cards.nth(0)).toContainText('recent');
      await expect(cards.nth(1)).toContainText('failure');
      await expect(cards.nth(2)).toContainText('No observed run');
      await expect(cards.nth(0).locator('.entity-card-list-status-success')).toHaveCount(1);
      await expect(view.locator('.semantic-prompt-action')).toHaveCount(0);
      await cards.nth(1).locator('[data-card-drill]').click();
    } else {
      await page.locator('[data-view-mode-value="table"]').click();
      const rows = view.locator('tbody > tr');
      await expect(rows).toHaveCount(3);
      await expect(rows.nth(0)).toContainText('recent');
      await expect(rows.nth(1)).toContainText('failure');
      await expect(rows.nth(2)).toContainText('No observed run');
      await expect(view.locator('.semantic-prompt-action')).toHaveCount(0);
      await rows.nth(1).locator('td').first().getByRole('link').click();
    }
    await expect(page).toHaveURL(/#page-workflow-runtime\?workflow=org%2Fcontrol%3A.github%2Fworkflows%2Fworker.md/);
    await expect(page.locator('[data-page-id="workflow-runtime"]')).toBeVisible();
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('worker');
  });
}

test('canonical continuation queries publish loading progress until their response settles', async ({ page, context }) => {
  const metadata = { 'as-of': '2026-10-07T13:00:00Z' };
  await context.route('http://dashboard.test/sources/manifest.json', (route) => route.fulfill({ status: 404 }));
  await context.route('http://dashboard.test/paged-evidence.json', (route) => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({
      repositories: { metadata, rows: [{ organization: 'org', repository: 'control' }] },
      workflows: {
        metadata, rows: ['first', 'second'].map((workflow) => ({
          organization: 'org', repository: 'control', workflow
        }))
      }
    })
  }));
  const result = await page.evaluate(async () => {
    const { loadCanonicalDashboardSources, loadCanonicalDashboardPage, subscribeWorkerLoadingProgress } = /** @type {typeof import('../../src/data-processor.js')} */ (await import(`${location.origin}/src/data-processor.js`));
    const { setLoadingProgressState } = /** @type {typeof import('../../src/loading-progress.js')} */ (await import(`${location.origin}/src/loading-progress.js`));
    /** @type {Array<{ id: string, phase: string, active: boolean }>} */
    const states = [];
    const stop = subscribeWorkerLoadingProgress((state) => {
      setLoadingProgressState(document, state);
      states.push({ ...state, active: Boolean(document.querySelector('.loading-progress:not(.loading-progress-complete)')) });
    });
    const queryContext = { pages: [], queries: [{ name: 'list', from: 'workflows', 'order-by': [{ field: 'workflow', direction: 'asc' }] }] };
    const first = await loadCanonicalDashboardSources(
      `${location.origin}/paged-evidence.json`, ['list'], queryContext, { list: { limit: 1 } }
    );
    const next = await loadCanonicalDashboardPage(['list'], queryContext, {
      list: { limit: 1, continuationToken: first.list.continuationToken }
    });
    stop();
    return {
      states: states.filter((state) => state.id.startsWith('query-progress-')),
      rows: [first.list.rows[0].workflow, next.list.rows[0].workflow]
    };
  });
  expect(result.rows).toEqual(['first', 'second']);
  expect(result.states.map((state) => state.phase)).toEqual(['start', 'complete', 'start', 'complete']);
  expect(result.states[0].id).toBe(result.states[1].id);
  expect(result.states[2].id).toBe(result.states[3].id);
  expect(result.states.filter((state) => state.phase === 'start').every((state) => state.active)).toBe(true);
  await expect(page.getByRole('progressbar')).toHaveCount(0);
});
