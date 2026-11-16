import { expect, test } from '@playwright/test';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import { splitDashboardDocument } from '../../src/dashboard-chunks.js';
import { normalizedRunShard } from './normalized-shard.js';

const siteRoot = fileURLToPath(new URL('../..', import.meta.url));
const origin = 'http://ingestion-backend.dashboard.test';
const activityDay = new Date(Date.now() - 2 * 86400000).toISOString().slice(0, 10);
const asOf = `${activityDay}T00:00:00Z`;
const hash = 'a'.repeat(64);
const shardName = `gh-aw-logs-runs/${activityDay}-0000-${'b'.repeat(16)}.jsonl`;
const contract = JSON.parse(readFileSync(join(siteRoot, 'test/fixtures/ingestion-backend-contract.json'), 'utf8'));
const pageDefinition = authoritativeDashboard.dashboard.pages.find(
  (/** @type {{ id: string }} */ page) => page.id === contract.page
);
const { core, pageChunks } = splitDashboardDocument({
  ...authoritativeDashboard,
  dashboard: {
    ...authoritativeDashboard.dashboard,
    pages: [authoritativeDashboard.dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === 'runs'), pageDefinition],
    navigation: [{ pages: ['runs', contract.page] }]
  }
});

for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
test(`stale data links to ingested shards without hosted telemetry at ${viewport.width}px`, async ({ context, page }, testInfo) => {
  await page.setViewportSize(viewport);
  await page.addInitScript(() => {
    const prototype = Worker.prototype;
    const post = prototype.postMessage;
    /** @type {Array<{ operation: string, sourceNames?: string[] }>} */
    const requests = [];
    Object.assign(window, { dashboardWorkerRequests: requests });
    prototype.postMessage = function (message, ...options) {
      if (message && typeof message.operation === 'string') {
        requests.push({ operation: message.operation, sourceNames: message.sourceNames });
      }
      return Reflect.apply(post, this, [message, ...options]);
    };
  });
  await context.route(`${origin}/**`, async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === '/') {
      await route.fulfill({
        contentType: 'text/html',
        body: '<div id="root"></div><script type="module" src="./src/main.js"></script>'
      });
      return;
    }
    if (url.pathname === '/dashboard.json') {
      await route.fulfill({ json: core });
      return;
    }
    const chunk = /^\/dashboard-pages\/([\w-]+)\.json$/.exec(url.pathname);
    if (chunk) {
      await route.fulfill({ json: pageChunks.get(chunk[1]) });
      return;
    }
    if (url.pathname === '/payload-hashes.json') {
      await route.fulfill({ json: { [shardName]: hash } });
      return;
    }
    if (url.pathname === '/inventory-sources.json') {
      await route.fulfill({
        json: {
          repositories: { rows: [{ organization: 'githubnext', repository: 'gh-cao' }], metadata: { 'as-of': asOf } },
          workflows: {
            rows: [{ organization: 'githubnext', repository: 'gh-cao', workflow: 'dashboard.md', 'workflow-name': 'Dashboard' }],
            metadata: { 'as-of': asOf }
          }
        }
      });
      return;
    }
    if (url.pathname === `/${shardName}`) {
      await route.fulfill({
        contentType: 'application/x-ndjson',
        body: route.request().method() === 'HEAD' ? '' : normalizedRunShard(`${JSON.stringify({
          schema_version: 2, kind: 'run',
          run: {
            run_id: 3001, run_attempt: 1, organization: 'githubnext', repository: 'gh-cao',
            workflow_path: 'dashboard.md', workflow_name: 'Dashboard', status: 'completed',
            conclusion: 'success', started_at: asOf, created_at: asOf, updated_at: asOf
          }
        })}\n`)
      });
      return;
    }
    const path = join(siteRoot, url.pathname);
    if (existsSync(path)) {
      await route.fulfill({
        contentType: path.endsWith('.json') ? 'application/json' : 'application/javascript',
        body: readFileSync(path)
      });
      return;
    }
    await route.fulfill({ status: 404, body: 'Not found' });
  });
  await page.goto(`${origin}/#page-runs`);
  const banner = page.locator('[data-notification-id="site-callout:stale-data"]:not(.dashboard-notification-exit)');
  await expect(banner).toBeVisible();
  const bannerBounds = await banner.boundingBox();
  const noticesBounds = await page.getByRole('region', { name: 'Notifications' }).boundingBox();
  if (!bannerBounds || !noticesBounds) {
    throw new Error('The notice and notification region must have visible bounds.');
  }
  expect(bannerBounds.x).toBeGreaterThanOrEqual(0);
  expect(bannerBounds.x + bannerBounds.width).toBeLessThanOrEqual(viewport.width);
  expect(noticesBounds.y + noticesBounds.height).toBeLessThanOrEqual(viewport.height);
  await expect(banner).toHaveCSS('border-radius', '6px');
  await expect(banner).toContainText('24 hours');
  await banner.screenshot({ path: testInfo.outputPath('stale-data-banner.png') });
  await testInfo.attach(`Managed stale-data notice (${viewport.width}px)`, {
    path: testInfo.outputPath('stale-data-banner.png'), contentType: 'image/png'
  });
  await expect(banner.getByRole('link', { name: 'View Ingestion' })).toHaveAttribute('href', '#page-indexing');
  await banner.getByRole('link', { name: 'View Ingestion' }).click();
  await expect(page.locator('#page-title')).toHaveText('Ingestion');
  await expect(page.locator('[data-view-id="ingestion-storage"]')).toContainText('Browser-local storage (IndexedDB)');
  await expect(page.getByRole('heading', { name: 'Indexed data over time' })).toHaveCount(0);
  await expect(page.locator('[data-view-id="indexing-database-table-counts"] [data-chart-widget="horizontal-bar"]')).toBeVisible();
  const daily = page.locator(`[data-view-id="${contract.dailyRuns.view}"]`);
  await expect(daily.getByRole('heading', { name: 'Observed daily runs' })).toBeVisible();
  await expect(daily.locator('[data-chart-widget="line"]')).toBeVisible();
  await expect(daily).toContainText('Undated runs are excluded');
  await expect(daily).toContainText('do not prove complete upstream collection');
  const dailyRows = await page.evaluate(async (source) => {
    const { processDataRequest } = await import(new URL('/src/data-worker.js', window.location.href).href);
    const chunk = await (await fetch('/dashboard-pages/indexing.json')).json();
    const result = await processDataRequest({
      operation: 'query-canonical-dashboard', sourceNames: [source],
      context: { queries: chunk.queries, pages: [chunk.page] }
    });
    return result[source];
  }, contract.dailyRuns.source);
  expect(dailyRows.rows).toEqual([{ date: activityDay, runs: 1 }]);
  expect(dailyRows.metadata).toMatchObject({ 'aggregate-version': 1, 'aggregate-coverage': 'admitted' });
  const shards = page.locator('[data-view-id="ingested-shards"]');
  await expect(shards).toContainText(shardName);
  await expect(shards).toContainText('Committed records');
  await expect(shards).toContainText(hash);
  const scatter = page.locator('[data-view-id="indexing-workflow-runs"]');
  await scatter.scrollIntoViewIfNeeded();
  await expect(scatter.getByRole('heading', { name: 'Runs per ingestion' })).toBeVisible();
  await expect(scatter.locator('[data-chart-widget="scatter"]')).toBeVisible();
  const ingestionRows = await page.evaluate(async () => {
    const { processDataRequest } = await import(new URL('/src/data-worker.js', window.location.href).href);
    const response = await fetch('/dashboard-pages/indexing.json');
    const chunk = await response.json();
    const result = await processDataRequest({
      operation: 'query-canonical-dashboard',
      sourceNames: ['indexing-ingestion-runs'],
      context: { queries: chunk.queries, pages: [chunk.page] }
    });
    return result['indexing-ingestion-runs'];
  });
  expect(ingestionRows.metadata.availability).toBe('available');
  expect(ingestionRows.rows.length).toBeGreaterThan(0);
  expect(ingestionRows.rows.find((/** @type {any} */ row) => row['shard-day'])?.['shard-day']).toBe(activityDay);
  await expect(scatter.locator('.scatter-chart-point')).toHaveCount(
    ingestionRows.rows.filter((/** @type {any} */ row) => row['shard-day']).length
  );
  await expect(scatter).toContainText('UTC activity date in the source shard filename');
  await expect(scatter).toContainText('Missing run counts are labeled');
  await expect(page.locator('.first-load-overlay')).toHaveCount(0);
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath('stale-data-ingestion.png'), fullPage: true });
  await testInfo.attach(`Fixture-backed Ingestion page (${viewport.width}px)`, {
    path: testInfo.outputPath('stale-data-ingestion.png'), contentType: 'image/png'
  });
  for (const id of contract.hostedViews) {
    await expect(page.locator(`[data-view-id="${id}"]`)).toHaveCount(0);
  }
  const requests = await page.evaluate(() => /** @type {Window & { dashboardWorkerRequests?: Array<{ operation: string, sourceNames?: string[] }> }} */ (window).dashboardWorkerRequests ?? []);
  expect(requests.some((request) => request.operation === 'query-dashboard-callouts')).toBe(true);
  const sources = requests.flatMap((request) => request.sourceNames ?? []);
  for (const source of contract.hostedSources) expect(sources).not.toContain(source);
  expect(sources).not.toContain('collection-health');
  expect(sources).not.toContain('github-quota-usage');
  expect(sources).toContain('indexing-database-table-counts');
  expect(sources).toContain(contract.dailyRuns.source);
  expect(sources).toContain('dashboard-data-freshness');
  await banner.getByRole('button', { name: 'Dismiss Dashboard data may be stale' }).click();
  await expect(banner).toHaveCount(0);
});
}
