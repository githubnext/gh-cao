import { expect, test } from '@playwright/test';
import { opsEvidence } from '../fixtures/ops-evidence.js';
import { startModernizationOperator } from '../fixtures/modernization-operator.mjs';
const { startDashboardServer } = await import(new URL('../../../../tests/helpers/dashboard-server.mjs', import.meta.url).href);
const { writeDashboardSnapshot } = await import(new URL('../../../../tests/helpers/dashboard-snapshot.mjs', import.meta.url).href);

for (const width of [1440, 390]) {
  test(`Modernization uses confirmed native queue actions without browser GitHub writes at ${width}px`, async ({ page }) => {
    test.setTimeout(120_000);
    const repository = 'octodemo/ops-synthetic';
    const operator = await startModernizationOperator(repository);
    const preview = await startDashboardServer({
      repository, operatorConfig: operator.operatorConfig,
      prepareSnapshot: async (/** @type {string} */ destination) => {
        const evidence = opsEvidence(new Date().toISOString());
        evidence.workflows.push({ ...evidence.workflows[0], id: 'workflow:modernization',
          role: 'standalone', path: '.github/workflows/modernization.md' });
        await writeDashboardSnapshot(destination, evidence);
      }
    }).catch(async (/** @type {unknown} */ error) => { await operator.close(); throw error; });
    try {
      await page.setExtraHTTPHeaders(preview.headers);
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript(() => {
        IDBFactory.prototype.open = () => { throw new Error('Native actions must not use a browser-storage fallback.'); };
      });
      const githubWrites = /** @type {string[]} */ ([]);
      page.on('request', request => {
        if (request.url().includes('api.github.com') && request.method() !== 'GET') githubWrites.push(request.url());
      });
      await page.goto(`${preview.url}/#page-ops`);
      const controls = page.getByRole('region', { name: 'Modernization actions', exact: true });
      await expect(controls).toBeVisible();
      await expect(controls.locator('.modernization-farm')).toContainText(repository);
      await expect(controls.locator('input[type="text"]')).toHaveCount(0);
      for (const colorScheme of /** @type {const} */ (['dark', 'light'])) {
        await page.emulateMedia({ colorScheme });
        await expect(controls.getByRole('combobox', { name: 'Next-batch mode', exact: true })).toHaveCSS('border-top-style', 'solid');
        const colors = await controls.evaluate(element => ({
          text: getComputedStyle(element).color,
          description: getComputedStyle(/** @type {Element} */ (element.querySelector('.modernization-description'))).color,
          input: getComputedStyle(/** @type {Element} */ (element.querySelector('select'))).backgroundColor,
          ready: getComputedStyle(/** @type {Element} */ (element.querySelector('.modernization-inputs button'))).backgroundColor,
          disabled: getComputedStyle(/** @type {Element} */ (element.querySelector('button:disabled'))).backgroundColor
        }));
        expect(colors.description).not.toBe(colors.text);
        expect(colors.input).not.toBe('rgba(0, 0, 0, 0)');
        expect(colors.ready).not.toBe(colors.disabled);
      }
      const help = controls.locator('.modernization-help');
      await expect(help).not.toHaveAttribute('open');
      await help.locator('summary').click();
      await expect(help.getByText('Every write needs', { exact: false })).toBeVisible();
      await help.locator('summary').click();
      expect(operator.requests).toHaveLength(0);
      await controls.getByRole('button', { name: 'Check farm readiness', exact: true }).click();
      await expect(controls).toContainText('Synthetic operator task');
      const readiness = controls.getByRole('region', { name: 'Current Modernization readiness', exact: true });
      const taskRow = readiness.getByRole('list', { name: 'Server-checked task scope and phase' }).locator('li').first();
      await expect(taskRow).toContainText('Issue phase');
      await expect(taskRow).toContainText('No linked evidence');
      const titleBox = await taskRow.locator('.modernization-task-title > span').boundingBox();
      expect(titleBox?.width).toBeGreaterThan(width === 1440 ? 500 : 200);
      expect((await taskRow.boundingBox())?.height).toBeLessThan(width === 1440 ? 150 : 230);
      const revision = readiness.locator('.modernization-revision');
      await expect(revision).not.toHaveAttribute('open');
      await revision.locator('summary').click();
      await expect(revision.getByText('a'.repeat(40), { exact: true })).toBeVisible();
      await expect(revision.getByText('b'.repeat(64), { exact: true })).toBeVisible();
      await revision.locator('summary').click();
      await expect(controls.getByRole('button', { name: 'Check farm readiness', exact: true })).toHaveClass('cli-action-cancel');
      await controls.getByRole('button', { name: 'Review next run', exact: true }).click();
      const review = controls.getByRole('region', { name: 'Review Modernization action', exact: true });
      await expect(review).toBeVisible();
      await expect(readiness.getByRole('list', { name: 'Server-checked task scope and phase' })).toHaveCount(0);
      await expect(controls.getByRole('list', { name: 'Server-checked task scope and phase' })).toHaveCount(1);
      expect(operator.requests.some(request => request.args[1] === 'execute')).toBe(false);
      await controls.getByLabel('Next-batch mode').selectOption('live');
      await expect(review).toHaveCount(0);
      await controls.getByRole('button', { name: 'Review next run', exact: true }).click();
      await expect(review).toContainText('mode live');
      await review.getByRole('button', { name: 'Run next batch', exact: true }).click();
      await expect(controls).toContainText('Dispatch accepted—not proof');
      expect(operator.requests.filter(request => request.args[1] === 'execute')).toHaveLength(1);
      await controls.getByRole('button', { name: 'Check farm readiness', exact: true }).click();
      await expect(controls).toContainText('Current check: available');
      await controls.getByRole('button', { name: 'Review / approve plan', exact: true }).click();
      const tabs = controls.getByRole('tablist', { name: 'Engagement plan view' });
      await expect(tabs.getByRole('tab', { name: 'Overview', exact: true })).toHaveAttribute('aria-selected', 'true');
      const overview = controls.getByRole('tabpanel', { name: 'Plan overview', exact: true });
      await expect(readiness.getByRole('list', { name: 'Server-checked task scope and phase' })).toHaveCount(0);
      await expect(overview).toContainText('Synthetic API behavior is documented.');
      await expect(overview).toContainText('assessor');
      await expect(overview).toContainText('Currently approved plan');
      await tabs.getByRole('tab', { name: 'Overview', exact: true }).focus();
      await page.keyboard.press('Tab');
      await expect(overview).toBeFocused();
      await page.keyboard.press('PageDown');
      await expect.poll(() => overview.evaluate(element => element.scrollTop)).toBeGreaterThan(0);
      const plan = `${JSON.stringify({ ...operator.plan, id: 'synthetic-browser-approval' }, null, 2)}\n`;
      await controls.getByLabel('Import plan JSON').setInputFiles({
        name: 'synthetic-plan.json', mimeType: 'application/json', buffer: Buffer.from(plan)
      });
      await expect(overview).toContainText('synthetic-browser-approval');
      await expect(controls.getByLabel('Engagement JSON')).toBeHidden();
      await tabs.getByRole('tab', { name: 'Overview', exact: true }).focus();
      await page.keyboard.press('ArrowRight');
      await expect(tabs.getByRole('tab', { name: 'JSON', exact: true })).toBeFocused();
      await expect(controls.getByLabel('Engagement JSON')).toHaveValue(plan);
      await controls.getByLabel('Engagement JSON').fill('{"id":');
      await tabs.getByRole('tab', { name: 'Overview', exact: true }).click();
      await expect(overview.getByRole('alert')).toContainText('Open the JSON tab to correct it');
      await tabs.getByRole('tab', { name: 'JSON', exact: true }).click();
      await expect(controls.getByLabel('Engagement JSON')).toHaveValue('{"id":');
      await controls.getByLabel('Engagement JSON').fill(plan);
      await page.keyboard.press('Shift+Tab');
      await page.keyboard.press('ArrowLeft');
      await expect(tabs.getByRole('tab', { name: 'Overview', exact: true })).toBeFocused();
      await expect(overview).toContainText('Synthetic services remain compatible.');
      await controls.getByRole('button', { name: 'Review approval', exact: true }).click();
      await expect(review).toBeVisible();
      await expect(overview).toBeHidden();
      expect(operator.requests.at(-1)?.input).toBe(plan);
      await review.getByRole('button', { name: 'Publish approved plan', exact: true }).click();
      await expect(controls).toContainText('The approved plan was published');
      await controls.getByRole('button', { name: 'Check farm readiness', exact: true }).click();
      await expect(controls).toContainText('Current check: available');
      for (const [label, operation] of [['Assess system', 'assess'], ['Verify results', 'verify']]) {
        await controls.getByRole('button', { name: label, exact: true }).click();
        await expect(review).toBeVisible();
        expect(operator.requests.at(-1)?.args).toEqual([
          'modernization', 'preview', '--repo', repository, '--action', operation, '--mode', 'review'
        ]);
        await review.getByRole('button', { name: 'Dismiss', exact: true }).click();
      }
      expect(githubWrites).toEqual([]);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    } finally {
      await page.goto('about:blank');
      await preview.close();
      await operator.close();
    }
  });
}

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
      evidence.runs[2].status = 'in_progress';
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
