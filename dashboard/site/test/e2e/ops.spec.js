import { expect, test } from '@playwright/test';
import { opsEvidence } from '../fixtures/ops-evidence.js';
const { startDashboardServer } = await import(new URL('../../../../tests/helpers/dashboard-server.mjs', import.meta.url).href);
const { writeDashboardSnapshot } = await import(new URL('../../../../tests/helpers/dashboard-snapshot.mjs', import.meta.url).href);

for (const width of [1440, 390]) {
  test(`Ops navigates from empty evidence and reacts to PostgreSQL revisions at ${width}px`, async ({ page }, testInfo) => {
    test.setTimeout(120_000);
    let snapshot = '';
    const preview = await startDashboardServer({
      prepareSnapshot: async (/** @type {string} */ destination) => {
        snapshot = destination;
        await writeDashboardSnapshot(destination);
      }
    });
    try {
      await page.setExtraHTTPHeaders(preview.headers);
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript(() => {
        IDBFactory.prototype.open = () => { throw new Error('Ops on server-http must not open browser IndexedDB.'); };
      });
      const mutations = /** @type {string[]} */ ([]);
      page.on('request', request => {
        if (request.method() !== 'GET' && request.method() !== 'POST') mutations.push(request.url());
        if (request.method() === 'POST' && !['/api/v1/query', '/api/v1/refresh'].some(path => request.url().endsWith(path))) mutations.push(request.url());
      });
      await page.goto(`${preview.url}/`);
      await expect(page.locator('.dashboard-root')).toBeVisible();
      if (width < 700) {
        await page.getByRole('button', { name: 'Select view', exact: true }).click();
        await page.locator('[data-mobile-nav-page-id="ops"]').click();
      } else {
        const section = page.locator('.nav-section').filter({ has: page.locator('[data-nav-page-id="ops"]') });
        await section.locator('summary').click();
        await page.locator('[data-nav-page-id="ops"]').click();
      }
      await expect(page).toHaveURL(/#page-ops$/);
      const ops = page.locator('[data-page-id="ops"]');
      await expect(ops.getByText('Attention needed', { exact: true }).first()).toBeVisible();
      await expect(ops).toContainText('Missing evidence is not an idle farm.');
      await expect(ops).toContainText('No repository evidence is retained');
      await expect(ops).toContainText('No nonterminal run attempts are retained');
      const evidence = opsEvidence(new Date().toISOString());
      await writeDashboardSnapshot(snapshot, evidence);
      await preview.ingestSnapshot();
      const attention = ops.locator('[data-view-id="ops-attention"]');
      await expect(attention).toContainText('Blocked synthetic workflow', { timeout: 20_000 });
      await expect(attention).toContainText('Review synthetic workflow');
      await expect(attention).toContainText('Unknown synthetic workflow');
      const flight = ops.locator('[data-view-id="ops-in-flight"]');
      await expect(flight).toContainText('synthetic-2.md');
      await expect(flight).toContainText('synthetic-3.md');
      const outputs = ops.locator('[data-view-id="ops-recent-outputs"]');
      await expect(outputs).toContainText('Synthetic proposed repair');
      await expect(outputs).toContainText('human_decision_pending');
      await expect(outputs).toContainText('delivery_fallback');
      const outputLink = outputs.locator('a[href="https://github.com/octodemo/target/pull/42"]').first();
      await expect(outputLink).toHaveAttribute('href', 'https://github.com/octodemo/target/pull/42');
      const repositories = ops.locator('[data-view-id="ops-repositories"]');
      await expect(repositories).toContainText('octodemo/no-evidence');
      await expect(repositories).toContainText('No retained run evidence');
      await expect(ops).not.toContainText('[object Object]');
      await expect(ops.locator('[data-view-state="unavailable"]')).toHaveCount(0);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
      const screenshot = testInfo.outputPath(`ops-populated-${width}.png`);
      await page.screenshot({ path: screenshot, fullPage: true });
      await testInfo.attach(`Ops synthetic evidence ${width}px`, { path: screenshot, contentType: 'image/png' });
      evidence.runs[0].conclusion = 'success';
      evidence.runs[0].updatedAt = new Date(Date.now() + 1000).toISOString();
      await writeDashboardSnapshot(snapshot, evidence);
      await preview.ingestSnapshot();
      await expect(attention).not.toContainText('Blocked synthetic workflow', { timeout: 20_000 });
      await expect(attention).toContainText('Review synthetic workflow');
      await repositories.locator('a[href="#page-repository-detail?repository=octodemo%2Fops-synthetic"]').first().click();
      await expect(page).toHaveURL(/#page-repository-detail\?repository=octodemo%2Fops-synthetic$/);
      expect(mutations).toEqual([]);
    } finally {
      await page.goto('about:blank');
      await preview.close();
    }
  });
}

test('Ops keeps unavailable queries distinct from an empty farm and recovers without browser storage', async ({ page }) => {
  test.setTimeout(120_000);
  const preview = await startDashboardServer({
    prepareSnapshot: (/** @type {string} */ destination) => writeDashboardSnapshot(destination, opsEvidence(new Date().toISOString()))
  });
  try {
    await page.setExtraHTTPHeaders(preview.headers);
    await page.addInitScript(() => {
      IDBFactory.prototype.open = () => { throw new Error('Unavailable server evidence must not fall back to IndexedDB.'); };
    });
    let unavailable = true;
    await page.route('**/api/v1/query', route => unavailable
      ? route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: 'Synthetic outage' }) })
      : route.continue());
    await page.goto(`${preview.url}/#page-ops`);
    const ops = page.locator('[data-page-id="ops"]');
    await expect(ops.locator('[data-view-state="unavailable"]').first()).toBeVisible();
    await expect(ops).not.toContainText('No repository evidence is retained');
    unavailable = false;
    await page.getByRole('button', { name: 'Retry', exact: true }).first().click();
    await expect(ops).toContainText('Blocked synthetic workflow', { timeout: 20_000 });
  } finally {
    await page.goto('about:blank');
    await preview.close();
  }
});

test('Ops labels loading, partial and stale server evidence without changing its rows', async ({ page }) => {
  test.setTimeout(120_000);
  let release = () => {};
  const pending = new Promise(/** @param {(value?: unknown) => void} resolve */ resolve => { release = resolve; });
  const preview = await startDashboardServer({
    prepareSnapshot: (/** @type {string} */ destination) => writeDashboardSnapshot(destination, opsEvidence(new Date().toISOString()))
  });
  try {
    await page.setExtraHTTPHeaders(preview.headers);
    await page.route('**/api/v1/query', async route => {
      if (route.request().postDataJSON().pageId !== 'ops') {
        await route.continue();
        return;
      }
      const response = await route.fetch();
      const payload = await response.json();
      for (const source of Object.values(payload.sources)) {
        Object.assign(source.metadata, { completeness: 'partial', freshness: 'stale' });
      }
      await pending;
      await route.fulfill({ response, json: payload });
    });
    await page.goto(`${preview.url}/#page-ops`);
    const ops = page.locator('[data-page-id="ops"]');
    await expect(page.locator('[data-page-id="ops"][aria-busy="true"], [data-page-id="ops"] [aria-busy="true"]').first()).toBeVisible();
    release();
    await expect(ops).toContainText('Blocked synthetic workflow');
    await expect(ops.locator('.view-metadata[role="status"]')).toHaveCount(4);
    await expect(ops.locator('.view-metadata[role="status"]').first())
      .toHaveText('Evidence completeness: partial; freshness: stale.');
  } finally {
    release();
    await page.goto('about:blank');
    await preview.close();
  }
});
