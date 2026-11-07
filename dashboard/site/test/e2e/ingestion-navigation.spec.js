import { expect, test } from '@playwright/test';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { normalizedRunShard } from './normalized-shard.js';
import { authoritativeDashboard as dashboard } from '../authoritative-dashboard.js';
import { DATABASE_VERSION } from '../../src/data/storage/indexeddb.js';

const siteRoot = fileURLToPath(new URL('../..', import.meta.url));
const origin = 'http://ingestion-navigation.dashboard.test';
const asOf = '2026-09-16T22:00:00Z';
const shardHash = 'a'.repeat(64);
const pageDefinitions = [
  ['runs', 'Runs', 'runs', 'run-title'],
  ['repositories', 'Repositories', 'repositories', 'repository'],
  ['workflows', 'Workflows', 'workflows', 'workflow-name']
];
const inventory = {
  repositories: {
    rows: [{ organization: 'githubnext', repository: 'gh-cao' }],
    metadata: { 'as-of': asOf }
  },
  workflows: {
    rows: [{
      organization: 'githubnext',
      repository: 'gh-cao',
      workflow: '.github/workflows/dashboard.md',
      'workflow-name': 'Dashboard'
    }],
    metadata: { 'as-of': asOf }
  }
};

/**
 * @param {import('@playwright/test').Page} page
 * @param {number} expectedTotal
 */
async function expectRunsLoadOnScroll(page, expectedTotal) {
  const view = page.locator('[data-view-id="runs-runs-source"]');
  const rows = view.locator('tbody > tr');
  const scroll = view.locator('.table-scroll');
  const loadBoundary = view.locator('[data-table-more]');
  await expect(rows).toHaveCount(25);
  await expect(view.locator('.table-filter-result')).toHaveText(`Showing 25 of ${expectedTotal} results`);
  const storageNotice = page.locator('[data-notification-id="dashboard:browser-storage"]');
  if (await storageNotice.isVisible()) {
    await storageNotice.getByRole('button', { name: /^Dismiss / }).click();
  }
  await scroll.hover();
  await page.mouse.wheel(0, 10_000);
  await scroll.evaluate((element) => {
    const scrollElement = /** @type {HTMLElement} */ (element);
    scrollElement.scrollTop = scrollElement.scrollHeight;
  });
  await scroll.dispatchEvent('scroll');
  await expect.poll(() => scroll.evaluate((element) => /** @type {HTMLElement} */ (element).scrollTop)).toBeGreaterThan(0);
  await expect(loadBoundary).toHaveText('Load more rows');
  await expect.poll(() => rows.count()).toBeGreaterThan(25);
}

/** @param {import('@playwright/test').Page} page */
async function storedRunCount(page) {
  return page.evaluate(async (storageUrl) => {
    const { readCollection } = await import(storageUrl);
    return (await readCollection(indexedDB, 'runs')).length;
  }, `${origin}/src/data/storage/indexeddb.js`);
}

/**
 * @param {import('@playwright/test').Page} page
 * @param {string} pageId
 */
async function navigateToPage(page, pageId) {
  await page.evaluate((nextPageId) => {
    const link = document.querySelector(`[data-nav-page-id="${nextPageId}"]`);
    if (!(link instanceof HTMLAnchorElement)) throw new Error(`Missing navigation link for ${nextPageId}.`);
    link.click();
  }, pageId);
}

/** @param {import('@playwright/test').Page} page @param {boolean} mobile */
async function selectTable(page, mobile) {
  if (mobile) {
    await page.getByRole('button', { name: 'Switch to Cards view' }).click();
    await page.getByRole('button', { name: 'Switch to Table view' }).click();
  } else {
    await page.getByRole('button', { name: 'Table', exact: true }).click();
  }
}

/**
 * @param {{ context: import('@playwright/test').BrowserContext, page: import('@playwright/test').Page }} fixture
 * @param {boolean} mobile
 * @param {boolean} [upgrade]
 * @param {'light'|'dark'} [colorScheme]
 */
async function exerciseFirstImport({ context, page }, mobile, upgrade = false, colorScheme = 'light') {
  await page.emulateMedia({ colorScheme });
  if (mobile) await page.setViewportSize({ width: 390, height: 844 });
  const shardCount = 12;
  const runsPerShard = 10;
  let requestedShards = 0;
  let completedShards = 0;
  let releaseFinalShard = () => {};
  const finalShardReady = new Promise((resolve) => {
    releaseFinalShard = () => resolve(undefined);
  });
  await context.route(`${origin}/**`, async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === '/seed') {
      await route.fulfill({ contentType: 'text/html', body: '<title>Database seed</title>' });
      return;
    }
    if (url.pathname === '/') {
      await route.fulfill({
        contentType: 'text/html',
        body: '<div id="root"></div><script type="module" src="./src/main.js"></script>'
      });
      return;
    }
    if (url.pathname === '/dashboard.json') {
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(dashboard) });
      return;
    }
    if (url.pathname === '/inventory-sources.json') {
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(inventory) });
      return;
    }
    if (url.pathname === '/payload-hashes.json') {
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(Object.fromEntries(
          Array.from({ length: shardCount }, (_, index) => [
           `gh-aw-logs-runs/logs-${String(index + 1).padStart(2, '0')}-${shardHash}-${String(index + 1).padStart(16, '0')}.jsonl`,
            String(index + 1).padStart(64, 'a')
          ])
        ))
      });
      return;
    }
    const shard = /^\/gh-aw-logs-runs\/logs-(\d+)-[a-f0-9]{64}-[a-f0-9]{16}\.jsonl$/.exec(url.pathname);
    if (shard) {
      if (route.request().method() === 'HEAD') {
        await route.fulfill({ headers: { 'content-length': '512' }, body: '' });
        return;
      }
      requestedShards += 1;
      await new Promise((resolve) => setTimeout(resolve, 35));
      const run = Number(shard[1]);
      if (run === shardCount) await finalShardReady;
      await route.fulfill({
        contentType: 'application/x-ndjson',
        body: normalizedRunShard(`${Array.from({ length: runsPerShard }, (_, index) => JSON.stringify({
          schema_version: 2,
          kind: 'run',
          run: {
            run_id: 1000 + (run - 1) * runsPerShard + index + 1,
            run_attempt: 1,
            organization: 'githubnext',
            repository: 'gh-cao',
            workflow_name: 'Dashboard',
            workflow_path: '.github/workflows/dashboard.md',
            display_title: `Ingested run ${run}-${index + 1}`,
            status: 'completed',
            conclusion: 'success',
            created_at: asOf,
            updated_at: asOf
          }
        })).join('\n')}\n`)
      });
      completedShards += 1;
      return;
    }
    const filePath = join(siteRoot, url.pathname);
    if (existsSync(filePath)) {
      await route.fulfill({
        contentType: url.pathname.endsWith('.json') ? 'application/json'
          : url.pathname.endsWith('.svg') ? 'image/svg+xml' : 'application/javascript',
        body: readFileSync(filePath)
      });
      return;
    }
    await route.fulfill({ status: 404, body: 'Not found' });
  });

  if (upgrade) {
    await page.goto(`${origin}/seed`);
    await page.evaluate((version) => new Promise((resolve, reject) => {
      const request = indexedDB.open('gh-aw-cao-dashboard-data', version - 1);
      request.onupgradeneeded = () => request.result.createObjectStore('legacy');
      request.onsuccess = () => {
        request.result.close();
        resolve(undefined);
      };
      request.onerror = () => reject(request.error);
    }), DATABASE_VERSION);
  }
  await page.goto(`${origin}/`);
  const importScreen = page.getByRole('dialog', { name: 'Preparing your dashboard' });
  await expect(importScreen).toBeVisible();
  const expectTheme = async (/** @type {'light'|'dark'} */ theme) => {
    await expect(importScreen).toHaveCSS('background-color', theme === 'dark' ? 'rgb(21, 27, 35)' : 'rgb(246, 248, 250)');
    await expect(importScreen.locator('.first-load-card')).toHaveCSS('background-color', theme === 'dark' ? 'rgb(13, 17, 23)' : 'rgb(255, 255, 255)');
  };
  await expectTheme(colorScheme);
  await expect(importScreen.locator('header')).toHaveText('Central Agentic Ops');
  const loader = importScreen.locator('.first-load-agentic-loader');
  await expect(loader).toBeVisible();
  await expect(loader.locator('.first-load-agentic-rider')).toHaveCount(3);
  await expect.poll(() => loader.locator('img').evaluateAll((images) =>
    images.every((image) => /** @type {HTMLImageElement} */ (image).naturalWidth > 0))).toBe(true);
  await expect(importScreen.locator('header')).toBeInViewport({ ratio: 1 });
  await expect(importScreen.getByRole('button', { name: 'Explore data' })).toHaveCount(1);
  await expect(importScreen.locator('.first-load-server-option')).not.toBeVisible();
  await expect(importScreen.locator('.first-load-storage-warning')).toHaveCount(0);
  await expect(page.locator('[data-notification-id="dashboard:browser-storage"]')).toContainText('private session ends');
  const importHistory = importScreen.locator('.first-load-ingestion-details');
  await expect(importHistory).toBeVisible();
  await importHistory.locator('summary').click();
  await expect(importHistory.locator('.dashboard-notification-details li').first()).toContainText('Preparing data');
  await expect(importHistory.getByRole('button', { name: 'Cancel' })).toBeVisible();
  await importHistory.locator('summary').click();
  if (upgrade) {
    await expect(importScreen).toContainText('refreshing your browser copy');
    await expect(importScreen.locator('.first-load-eyebrow')).toHaveText('Dashboard update');
  }
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  if (mobile) {
    for (const viewport of [{ width: 320, height: 568 }, { width: 390, height: 844 }, { width: 430, height: 932 }]) {
      await page.setViewportSize(viewport);
      await expectTheme(colorScheme);
      await expect(importScreen.locator('header')).toBeInViewport({ ratio: 1 });
      await expect(importScreen.getByRole('heading', { name: 'Preparing your dashboard.' })).toBeVisible();
      await expect(importScreen.locator('.first-load-steps')).not.toBeVisible();
      await expect(importScreen.getByRole('button', { name: 'Explore data' })).toBeInViewport({ ratio: 1 });
      await expect(importScreen.getByRole('button', { name: 'Dismiss import screen' })).toHaveCount(0);
      await expect.poll(() => importScreen.evaluate((element) => ({
        horizontal: element.scrollWidth > element.clientWidth,
        vertical: element.scrollHeight > element.clientHeight
      }))).toEqual({ horizontal: false, vertical: false });
    }
    const alternateTheme = colorScheme === 'dark' ? 'light' : 'dark';
    await page.emulateMedia({ colorScheme: alternateTheme });
    await expectTheme(alternateTheme);
    await page.emulateMedia({ colorScheme });
    await expectTheme(colorScheme);
    await page.setViewportSize({ width: 390, height: 844 });
  }
  await importScreen.locator('.first-load-about summary').click();
  await expect(importScreen.locator('.first-load-server-option')).toBeVisible();
  await expect(importScreen.locator('.first-load-reason')).toContainText(upgrade ? 'newer browser database format' : 'no completed local copy yet');
  await expect(importScreen.getByRole('link', { name: 'deployment options (opens in a new tab)' })).toBeVisible();
  await expect(importScreen.locator('.first-load-reason').getByRole('button', { name: 'Copy preparation details' })).toBeVisible();
  await importScreen.locator('.first-load-about summary').click();
  await expect(importScreen.locator('.first-load-server-option')).not.toBeVisible();
  const readBackground = (/** @type {Element} */ element) => {
    const css = getComputedStyle(element, '::before');
    return { image: css.backgroundImage, size: css.backgroundSize, border: css.borderRightColor };
  };
  const overviewGrid = await page.locator('.factory-floor').evaluate(readBackground);
  expect(await importScreen.locator('.first-load-background').evaluate(readBackground)).toEqual(overviewGrid);
  await expect.poll(() => importScreen.locator('.first-load-background').evaluate((element) => (
    getComputedStyle(element, '::before').animationName
  ))).toBe('first-load-grid-breathe');
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await expect(loader.locator('.first-load-agentic-rider').first()).toHaveCSS('animation-name', 'none');
  await expect(loader.locator('.first-load-agentic-rider').first()).toHaveCSS('opacity', '1');
  await expect.poll(() => importScreen.locator('.first-load-background').evaluate((element) => (
    getComputedStyle(element, '::before').animationName
  ))).toBe('none');
  await page.keyboard.press('Escape');
  await expect(importScreen).not.toBeVisible();
  await expect(page.locator('#agent-factory-heading')).toHaveText('Your dashboard is taking shape.');
  await expect(page.getByRole('button', { name: 'Show import progress' })).toHaveCount(0);
  await expect.poll(() => completedShards).toBe(shardCount - 1);
  await expect.poll(() => storedRunCount(page)).toBe((shardCount - 1) * runsPerShard);
  const preparation = await page.evaluate(async (url) => {
    const { preparationDiagnostics } = await import(url);
    return preparationDiagnostics();
  }, `${origin}/src/browser-first-load.js`);
  expect(preparation.storage.persisted).not.toBe(true);
  expect(preparation.log.some((/** @type {{ details: { message?: string } }} */ entry) =>
    entry.details.message?.startsWith('Shard 1/12 committed'))).toBe(true);
  expect(preparation.log.some((/** @type {{ details: { message?: string } }} */ entry) =>
    entry.details.message?.startsWith('Shard 11/12 committed'))).toBe(true);
  await expect(page.locator('#agent-factory-heading')).toHaveText('Your dashboard is taking shape.');
  await expect(page.locator('.factory-intro .factory-rhythm')).not.toBeVisible();
  await page.locator('.dashboard-notification-toggle').click();
  await expect(page.getByRole('button', { name: 'Cancel', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Sync queries', exact: true })).toHaveCount(0);
  await page.locator('.dashboard-notification-toggle').click();
  await navigateToPage(page, 'runs');
  await selectTable(page, mobile);
  await expect(page.locator('td[data-field="run"]', { hasText: '1001' })).toBeVisible();
  expect(completedShards).toBeLessThan(shardCount);
  await expect(page.locator('.loading-progress')).toBeVisible();
  await expect.poll(async () => Number(await page.locator('.loading-progress').getAttribute('aria-valuenow')))
    .toBeLessThanOrEqual(70);
  await expectRunsLoadOnScroll(page, completedShards * runsPerShard);

  for (let cycle = 0; cycle < 4; cycle += 1) {
    for (const [id, title] of pageDefinitions) {
      await navigateToPage(page, id);
      await expect(page.locator('#page-title')).toHaveText(title);
    }
  }

  await navigateToPage(page, 'overview');
  await expect(page.getByRole('button', { name: 'Show import progress' })).toHaveCount(0);
  releaseFinalShard();
  await expect.poll(() => storedRunCount(page)).toBe(shardCount * runsPerShard);
  await expect(importScreen).toHaveCount(0);
  await navigateToPage(page, 'overview');
  await expect(page.locator('#agent-factory-heading')).not.toHaveText('Your dashboard is taking shape.');
  await expect(page.getByRole('button', { name: 'Show import progress' })).toHaveCount(0);
  await expect(importScreen).toHaveCount(0);
  await navigateToPage(page, 'runs');
  await selectTable(page, mobile);
  expect(requestedShards).toBe(shardCount);
  await expectRunsLoadOnScroll(page, shardCount * runsPerShard);
}

for (const colorScheme of /** @type {const} */ (['light', 'dark'])) {
  for (const mobile of [false, true]) {
    test(`First import stays honest while browsing on ${mobile ? 'mobile' : 'desktop'} in ${colorScheme} mode`, async ({ context, page }) => {
      await exerciseFirstImport({ context, page }, mobile, false, colorScheme);
    });
  }
}

test('An existing database upgrade immediately reuses the import dialog', async ({ context, page }) => {
  await exerciseFirstImport({ context, page }, false, true);
});
