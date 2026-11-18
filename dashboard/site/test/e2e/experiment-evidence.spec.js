import { selectViewMode } from './helpers/smoke-fixtures.js';
import { createHash } from 'node:crypto';
import { authoritativeDashboard, expect, registerSmokeRoutes, test, hydrateView } from './helpers/smoke-fixtures.js';
import { normalizedActivityShards } from './normalized-shard.js';

registerSmokeRoutes();

test('Experiments renders persisted assignments and updates its active worker queries after ingestion', async ({ page }) => {
  let count = 1;
  const now = new Date().toISOString();
  await page.route('http://dashboard.test/evidence/**', async (route) => {
    const shards = normalizedActivityShards(Array.from({ length: count }, (_, index) => JSON.stringify({
      schema_version: 5, kind: 'run',
      run: {
        run_id: 303 + index, run_attempt: 1, organization: 'octo', repository: 'octo/api',
        workflow_name: 'Worker', workflow_path: '.github/workflows/worker.lock.yml',
        created_at: now, updated_at: now, status: 'completed', conclusion: 'success',
        url: `https://github.com/octo/api/actions/runs/${303 + index}`,
        experiments: { assignments: { prompt: index === 0 ? 'candidate' : 'control' } }
      }
    })).join('\n'));
    const pathname = new URL(route.request().url()).pathname;
    if (pathname.endsWith('/payload-hashes.json')) {
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify({
        'gh-aw-logs-runs/experiments.jsonl': createHash('sha256').update(shards.runs).digest('hex'),
        'gh-aw-logs-records/experiments.jsonl': createHash('sha256').update(shards.records).digest('hex')
      }) });
    } else if (pathname.endsWith('/experiments.jsonl')) {
      await route.fulfill({ contentType: 'application/x-ndjson',
        body: pathname.includes('gh-aw-logs-runs/') ? shards.runs : shards.records });
    } else {
      await route.fulfill({ status: 404 });
    }
  });
  await page.evaluate(async (model) => {
    const { renderDashboard, dashboardPageSourceNames } = await import(`${location.origin}/src/presenter.js`);
    const { loadCanonicalDashboardSources, subscribeCanonicalDashboardView } = await import(`${location.origin}/src/data-processor.js`);
    const dashboard = model.dashboard;
    await loadCanonicalDashboardSources(`${location.origin}/evidence/payload-hashes.json`, [], dashboard);
    /** @type {import('../../src/presenter.js').PageSourceLoader} */
    const loadPageSources = (pageId, options) => new Promise((resolve, reject) => {
      let initial = true;
      subscribeCanonicalDashboardView(dashboard.id,
        dashboardPageSourceNames(model, pageId, options.queryContext?.viewMode),
        dashboard, (/** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ result) => {
          if (initial) { initial = false; resolve(result); }
          else options.onUpdate(result);
        }, undefined, { ...options, pageId, onError: reject });
    });
    location.hash = '#page-experiments';
    document.querySelector('#root')?.replaceChildren(renderDashboard({ document: model, sources: {}, loadPageSources }));
  }, authoritativeDashboard);
  const coverage = page.locator('[data-view-id="experiments-assignment-chart"]');
  await expect(coverage.getByRole('img', { name: 'prompt: 1', exact: true })).toBeVisible();
  await selectViewMode(page, 'table');
  await hydrateView(page, 'Run assignments');
  const assignments = page.locator('[data-view-id="experiments-assignments"]');
  await expect(assignments.locator('tbody tr')).toHaveCount(1);
  await expect(assignments.locator('tbody')).toContainText('candidate');
  await expect(assignments.getByRole('link', { name: '303', exact: true }))
    .toHaveAttribute('href', 'https://github.com/octo/api/actions/runs/303');
  count = 2;
  await page.evaluate(async (model) => {
    const { refreshCanonicalDashboardSources } = await import(`${location.origin}/src/data-processor.js`);
    await refreshCanonicalDashboardSources(`${location.origin}/evidence/payload-hashes.json`, [], model.dashboard);
  }, authoritativeDashboard);
  await expect(assignments.locator('tbody tr')).toHaveCount(2);
  await expect(assignments.locator('tbody')).toContainText('control');
  await selectViewMode(page, 'chart');
  await expect(coverage.getByRole('img', { name: 'prompt: 2', exact: true })).toBeVisible();
  await selectViewMode(page, 'table');
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(assignments.locator('tbody')).toContainText('candidate');
  await expect(assignments.locator('tbody')).toContainText('control');
});
