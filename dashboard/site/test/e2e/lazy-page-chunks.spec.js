import { execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { normalizedActivityShards, normalizedRunShard } from './normalized-shard.js';
import { queryDashboardSourceObservations } from '../../src/data/queries/ingestion.js';
import { expect, test } from '@playwright/test';

const siteRoot = fileURLToPath(new URL('../..', import.meta.url));
const buildScript = fileURLToPath(new URL('../../scripts/build.mjs', import.meta.url));
const controlSettings = fileURLToPath(new URL('../performance/fixtures/control-settings.json', import.meta.url));
/** @type {string} */
let buildRoot;
const origin = 'http://lazy-page-chunks.dashboard.test';
const shardName = `gh-aw-logs-runs/logs-${'a'.repeat(64)}-${'b'.repeat(16)}.jsonl`;

test.beforeAll(() => {
  buildRoot = mkdtempSync(join(tmpdir(), 'lazy-page-chunks-'));
  execFileSync(process.execPath, [buildScript, buildRoot, controlSettings], {
    cwd: siteRoot,
    stdio: 'inherit',
  });
});

const inventory = {
  campaigns: {
    rows: [{
      campaign: 'dependabot',
      'campaign-name': 'Dependabot',
      'campaign-description': 'Dependabot automation',
      'campaign-icon': 'goal',
      'campaign-mode': 'review',
      'campaign-enabled': true,
      'campaign-worker-count': 1,
      'campaign-min-version': 'v1.0.0',
      'observed-at': '2026-09-15T10:00:00Z',
    }],
    metadata: { 'as-of': '2026-09-15T10:00:00Z', 'retrieved-at': '2026-09-15T10:00:00Z', completeness: 'complete', freshness: 'fresh', availability: 'available' },
  },
  repositories: {
    rows: [{ id: 'repository:githubnext/gh-cao', organization: 'githubnext', repository: 'gh-cao' }],
    metadata: { 'as-of': '2026-09-15T10:00:00Z', 'retrieved-at': '2026-09-15T10:00:00Z', completeness: 'complete', freshness: 'fresh', availability: 'available' },
  },
  workflows: {
    rows: [{
      organization: 'githubnext',
      repository: 'gh-cao',
      workflow: '.github/workflows/dashboard.md',
      'workflow-name': 'Dashboard',
      role: 'orchestrator',
      state: 'active',
      updatedAt: '2026-09-15T10:00:00Z',
      ghAwMetadata: { strict: false },
      ghAwManifest: { actions: [] },
    }],
    metadata: { 'as-of': '2026-09-15T10:00:00Z', 'retrieved-at': '2026-09-15T10:00:00Z', completeness: 'complete', freshness: 'fresh', availability: 'available' },
  },
  'marketplace-packages': {
    rows: [{
      id: `official:githubnext/gh-cao/dependabot@${'a'.repeat(40)}`,
      'registry-id': 'official',
      'registry-name': 'Official CAO catalog',
      'registry-precedence': 0,
      name: 'Dependabot',
      description: 'Dependabot automation',
      publisher: 'githubnext',
      repository: 'githubnext/gh-cao',
      'repository-link': {
        relation: 'repository',
        href: 'https://github.com/githubnext/gh-cao',
        label: 'Open githubnext/gh-cao on GitHub',
      },
      path: 'dependabot',
      ref: 'main',
      'resolved-commit': 'a'.repeat(40),
      version: 'main',
      icon: 'workflow',
      artwork: '',
      contents: ['aw.yml'],
      readme: '# Dependabot\n\nKeeps dependency updates moving.\n',
      'readme-path': 'dependabot/README.md',
      source: `githubnext/gh-cao/dependabot@${'a'.repeat(40)}`,
      'add-command': `gh cao add githubnext/gh-cao/dependabot@${'a'.repeat(40)}`,
    }, ...['aw-optimization', 'cao-evolution'].map((path, index) => ({
      id: `official:githubnext/gh-cao/${path}@${'b'.repeat(40)}`,
      'registry-id': 'official',
      'registry-name': 'Official CAO catalog',
      'registry-precedence': 0,
      name: index === 0 ? 'AW Optimization' : 'CAO Evolution',
      description: `${path} automation`,
      publisher: 'githubnext',
      repository: 'githubnext/gh-cao',
      path,
      ref: 'main',
      'resolved-commit': 'b'.repeat(40),
      version: 'main',
      icon: 'workflow',
      artwork: '',
      contents: ['aw.yml'],
      readme: `# ${path}\n`,
      'readme-path': `${path}/README.md`,
      source: `githubnext/gh-cao/${path}@${'b'.repeat(40)}`,
      'add-command': `gh cao add githubnext/gh-cao/${path}@${'b'.repeat(40)}`,
      'observed-at': '2026-09-15T10:00:00Z',
    })), {
      id: `official:githubnext/gh-cao/aw-optimization@${'c'.repeat(40)}`,
      'registry-id': 'official',
      'registry-name': 'Official CAO catalog',
      'registry-precedence': 0,
      name: 'AW Optimization',
      description: 'Earlier package revision',
      publisher: 'githubnext',
      repository: 'githubnext/gh-cao',
      path: 'aw-optimization',
      ref: 'main',
      'resolved-commit': 'c'.repeat(40),
      version: 'main',
      icon: 'workflow',
      artwork: '',
      contents: ['aw.yml'],
      readme: '# aw-optimization\n',
      'readme-path': 'aw-optimization/README.md',
      source: `githubnext/gh-cao/aw-optimization@${'c'.repeat(40)}`,
      'add-command': `gh cao add githubnext/gh-cao/aw-optimization@${'c'.repeat(40)}`,
      'observed-at': '2026-09-14T10:00:00Z',
    }],
    metadata: { 'as-of': '2026-09-15T10:00:00Z', 'retrieved-at': '2026-09-15T10:00:00Z', completeness: 'complete', freshness: 'fresh', availability: 'available' },
  },
  'configuration-policy': {
    rows: [{
      path: '.github/workflows/cao.json',
      document: { version: 1, 'control-plane': { campaigns: {} } },
      raw: '{"version":1,"control-plane":{"campaigns":{}}}',
      diagnostics: [{
        severity: 'valid',
        path: '.github/workflows/cao.json',
        title: 'Policy is valid',
        detail: 'The runtime policy resolver accepted this revision.',
      }],
    }],
    metadata: { 'as-of': '2026-09-15T10:00:00Z', 'retrieved-at': '2026-09-15T10:00:00Z', completeness: 'complete', freshness: 'fresh', availability: 'available' },
  },
};

const logs = `${JSON.stringify({
  schema_version: 2,
  kind: 'run',
  run: {
    run_id: 1001,
    run_attempt: 1,
    organization: 'githubnext',
    repository: 'gh-cao',
    workflow_name: 'Dashboard',
    workflow_path: '.github/workflows/dashboard.md',
    display_title: 'Dashboard run',
    status: 'completed',
    conclusion: 'success',
    created_at: '2026-09-15T10:00:00Z',
    updated_at: '2026-09-15T10:05:00Z',
  },
})}\n`;

test.afterAll(() => {
  rmSync(buildRoot, { recursive: true, force: true });
});

test.beforeEach(async ({ context }) => {
  await context.route(`${origin}/**`, async (route) => {
    const url = new URL(route.request().url());
    const pathname = url.pathname;
    if (pathname === '/payload-hashes.json') {
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({ [shardName]: 'a'.repeat(64) }),
      });
      return;
    }
    if (pathname === '/inventory-sources.json') {
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(inventory) });
      return;
    }
    if (pathname === `/${shardName}`) {
      await route.fulfill({ contentType: 'application/x-ndjson', body: normalizedRunShard(logs) });
      return;
    }
    let filePath = join(buildRoot, pathname);
    if (pathname === '/') filePath = join(buildRoot, 'index.html');
    else if (pathname.endsWith('/')) filePath = join(buildRoot, pathname, 'index.html');
    if (existsSync(filePath)) {
      const contentType = pathname.endsWith('.css')
        ? 'text/css'
        : pathname.endsWith('.json')
        ? 'application/json'
        : pathname.endsWith('.svg')
          ? 'image/svg+xml'
          : pathname.endsWith('.webmanifest')
            ? 'application/manifest+json'
            : pathname.endsWith('.map')
              ? 'application/json'
              : pathname.endsWith('.html') || pathname.endsWith('/')
                ? 'text/html'
                : 'application/javascript';
      await route.fulfill({ contentType, body: readFileSync(filePath) });
      return;
    }
    await route.fulfill({ status: 404, body: 'Not found' });
  });
});

/** @param {import('@playwright/test').Page} page */
function captureChunkRequests(page) {
  /** @type {string[]} */
  const requests = [];
  page.on('response', (/** @type {import('@playwright/test').Response} */ response) => {
    if (!response.url().startsWith(`${origin}/dashboard-pages/`)) return;
    requests.push(decodeURIComponent(response.url().split('/dashboard-pages/')[1].replace(/\.json(?:\?.*)?$/, '')));
  });
  return requests;
}

/**
 * @param {import('@playwright/test').Page} page
 * @param {string} pageId
 */
async function navigateToPage(page, pageId) {
  await page.evaluate((nextPageId) => {
    const link = document.querySelector(`[data-nav-page-id="${nextPageId}"]`);
    if (!(link instanceof HTMLAnchorElement)) throw new Error(`Missing navigation link for ${nextPageId}.`);
    link.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
  }, pageId);
}

/**
 * @param {import('@playwright/test').Page} page
 * @param {string} pageId
 */
async function pageText(page, pageId) {
  return (await page.locator(`[data-page-id="${pageId}"]`).textContent()) ?? '';
}

test('core dashboard stays small and page chunks load on demand with in-memory caching', async ({ page }) => {
  const chunkRequests = captureChunkRequests(page);
  await page.addInitScript(() => {
    const nativeFetch = window.fetch;
    window.fetch = (input, init) => {
      const url = input instanceof Request ? input.url : String(input);
      if (url.includes('dashboard-pages/')) {
        const requests = Reflect.get(window, '__dashboardChunkFetches') ?? [];
        requests.push({ url, cache: init?.cache });
        Reflect.set(window, '__dashboardChunkFetches', requests);
      }
      return nativeFetch(input, init);
    };
  });

  await page.goto(`${origin}/#page-repositories`);

  const core = await page.evaluate(async () => {
    const response = await fetch('./dashboard.json', { cache: 'no-store' });
    return {
      textLength: (await response.clone().text()).length,
      value: await response.json(),
    };
  });
  // Query definitions stay in page chunks; core keeps each page stub's source index so sources can
  // load before the chunk arrives. Page descriptions and analysis contracts stay in their chunks.
  expect(core.textLength).toBeLessThan(81000);
  expect(core.value.dashboard.pages.some((/** @type {Record<string, unknown>} */ entry) =>
    ['description', 'intent', 'subject', 'objective', 'acceptance'].some((field) => field in entry))).toBe(false);
  expect(core.value.dashboard.pages.some((/** @type {{ views?: unknown[] }} */ entry) => Array.isArray(entry.views))).toBe(false);
  expect(core.value.dashboard.pages.some((/** @type {{ definition?: { views?: unknown[] } }} */ entry) => Array.isArray(entry.definition?.views))).toBe(false);

  await expect(page.getByRole('heading', { name: 'Repositories', exact: true, level: 1 })).toBeVisible();
  await expect.poll(() => pageText(page, 'repositories')).toMatch(/Top repositories with issues/);
  const analysis = page.locator('[data-view-id="repositories-runs"] .table-intent-button');
  await expect(analysis).toBeVisible();
  await analysis.click();
  const preview = page.locator('[data-view-id="repositories-runs"] .table-intent-preview');
  await expect(preview).toContainText('Page intent:\nDetermine which repository most needs CAO operational investigation.');
  await expect(preview).toContainText('https://github.com/githubnext/gh-cao/blob/main/skills/analyze-cao/SKILL.md');
  await page.getByRole('button', { name: 'Close prompt preview' }).click();
  await page.getByRole('button', { name: 'Table' }).click();
  await expect.poll(() => pageText(page, 'repositories')).toMatch(
    /Ingestion %|No repositories discovered\.|gh-cao|Loading Repositories/
  );
  await expect.poll(() => chunkRequests.filter((id) => id === 'repositories').length).toBe(1);

  await navigateToPage(page, 'runs');
  await expect(page.getByRole('heading', { name: 'Runs', exact: true, level: 1 })).toBeVisible();
  await expect.poll(() => pageText(page, 'runs')).toMatch(/Runs in the last week/);
  await expect.poll(() => pageText(page, 'runs')).toMatch(/No runs observed\.|1001|Runs/);
  await expect.poll(() => chunkRequests.filter((id) => id === 'runs').length).toBe(1);

  await navigateToPage(page, 'repositories');
  await expect(page.getByRole('heading', { name: 'Repositories', exact: true, level: 1 })).toBeVisible();
  await expect.poll(() => chunkRequests.filter((id) => id === 'repositories').length).toBe(1);

  await navigateToPage(page, 'configuration');
  await expect(page.getByRole('heading', { name: 'Settings', exact: true, level: 1 })).toBeVisible();
  await expect(page.locator('[data-page-id="configuration"] .configuration-view')).toBeVisible();
  await expect(page.locator('[data-page-id="configuration"]')).not.toContainText('Affected source: configuration-policy');
  await expect.poll(() => chunkRequests.filter((id) => id === 'configuration').length).toBe(1);
  const configurationFetch = await page.evaluate(() =>
    Reflect.get(window, '__dashboardChunkFetches')?.find(
      (/** @type {{ url: string }} */ request) => request.url.endsWith('/configuration.json')
    )
  );
  expect(configurationFetch?.cache).toBe('reload');
});

test('built inventory tables resolve partial-source queries through the production worker', async ({ page }) => {
  await page.addInitScript(() => {
    Reflect.set(window, '__dashboardImportComplete', false);
    document.addEventListener('dashboard-data', (event) => {
      if (event instanceof CustomEvent && event.detail?.kind === 'refresh' && event.detail.status === 'completed') {
        Reflect.set(window, '__dashboardImportComplete', true);
      }
    });
  });
  await page.goto(`${origin}/?debug-eager-ingest=1#page-campaigns`);
  await page.waitForFunction(() => Reflect.get(window, '__dashboardImportComplete') === true);
  for (const [pageId, identity] of [['campaigns', 'Dependabot'], ['repositories', 'gh-cao']]) {
    await navigateToPage(page, pageId);
    await page.getByRole('button', { name: 'Table', exact: true }).click();
    const active = page.locator(`[data-page-id="${pageId}"]`);
    await expect(active.locator('table')).toContainText(identity);
    await expect(active.locator('[data-view-state="unavailable"]')).toHaveCount(0);
    await expect(active.locator('[aria-busy="true"]')).toHaveCount(0);
  }
});

for (const mode of ['table', 'card']) {
  test(`page-bound Campaigns preserves its ${mode} preview while event records are still loading`, async ({ context, page }) => {
    const recordsName = shardName.replace('gh-aw-logs-runs/', 'gh-aw-logs-records/');
    const shards = normalizedActivityShards(logs, queryDashboardSourceObservations(inventory).observations);
    let releaseRecords = () => {};
    const recordsReady = new Promise((resolve) => { releaseRecords = () => resolve(undefined); });
    await context.route(`${origin}/payload-hashes.json`, route => route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ [shardName]: 'a'.repeat(64), [recordsName]: 'c'.repeat(64) })
    }));
    await context.route(`${origin}/${shardName}`, route => route.fulfill({
      contentType: 'application/x-ndjson', body: shards.runs
    }));
    await context.route(`${origin}/${recordsName}`, async route => {
      if (route.request().method() !== 'HEAD') await recordsReady;
      await route.fulfill({ contentType: 'application/x-ndjson', body: shards.records });
    });
    await page.addInitScript(() => {
      Reflect.set(window, '__dashboardImportComplete', false);
      document.addEventListener('dashboard-data', event => {
        if (event instanceof CustomEvent && event.detail?.kind === 'refresh' && event.detail.status === 'completed') {
          Reflect.set(window, '__dashboardImportComplete', true);
        }
      });
    });
    try {
      await page.goto(`${origin}/#page-campaigns`);
      await page.getByRole('button', { name: 'Explore data', exact: true }).click();
      const active = page.locator('[data-page-id="campaigns"]');
      await active.locator(`[data-view-mode-value="${mode}"]`).click();
      const inventoryView = active.locator('[data-view-id="campaigns-inventory"]');
      await expect(inventoryView.locator('a[href="#page-campaign-insights?campaign=dependabot"]:visible').first()).toBeVisible();
      await expect(inventoryView).toContainText('Dependabot');
      expect(await inventoryView.locator('[aria-label$="is being calculated"]').count()).toBeGreaterThan(0);
      expect(await page.evaluate(() => Reflect.get(window, '__dashboardImportComplete'))).toBe(false);
    } finally {
      releaseRecords();
    }
    await page.waitForFunction(() => Reflect.get(window, '__dashboardImportComplete') === true);
    const active = page.locator('[data-page-id="campaigns"]');
    await expect(active.locator('[aria-label$="is being calculated"]')).toHaveCount(0);
    await expect(active.locator('[data-view-state="unavailable"]')).toHaveCount(0);
  });
}

test('built dashboard preserves supported campaign icons outside its inline core', async ({ context, page }) => {
  await context.route(`${origin}/inventory-sources.json`, (route) => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({
      ...inventory,
      'marketplace-packages': { ...inventory['marketplace-packages'], rows: inventory['marketplace-packages'].rows.map((row) => ({
        ...row, icon: 'feed-trophy'
      })) }
    })
  }));
  const spriteLoaded = page.waitForResponse((response) => response.url() === `${origin}/src/octicons.svg`);
  await page.goto(`${origin}/#page-marketplace-package?package-source=${encodeURIComponent(inventory['marketplace-packages'].rows[0].source)}`);
  await spriteLoaded;
  const icon = page.locator('.link-button-list-item .octicon-feed-trophy');
  await expect(icon).toBeVisible();
  await expect(icon.locator('path')).not.toHaveCount(0);
  await expect(icon.locator('use')).toHaveCount(0);
});

test('async Factory Overview elements receive their chunked query definitions', async ({ page }) => {
  const chunkRequests = captureChunkRequests(page);
  await page.goto(`${origin}/#page-overview`);

  const overview = page.locator('[data-page-id="overview"]');
  await expect(overview).not.toContainText('No custom views available.');
  await expect(overview.locator(':scope > .custom-view-grid > .custom-view')).toHaveCount(2);
  await expect(overview.getByRole('heading', { name: 'Your factory is humming.' })).toBeVisible();
  await expect(overview.locator('.factory-station').nth(0)).toContainText('1');
  await expect(overview.locator('.factory-station').nth(1)).toContainText('1');
  await expect(overview).not.toContainText('Unavailable');
  await expect.poll(() => chunkRequests.filter((id) => id === 'overview').length).toBe(1);
  await expect(overview.getByText('Weekly rhythm', { exact: true })).toBeVisible();
  await expect(overview.locator('.dashboard-ingestion-status')).toHaveCount(0);
  await expect(page.locator('.report-footer-status')).toHaveCount(0);
  const status = page.getByRole('button', { name: 'Dashboard data is stale', exact: true });
  await expect(status).toHaveCount(1);
  await expect(status.locator('.octicon-alert')).toBeVisible();
  await expect(overview.locator('.factory-station').first()).toContainText('Factory health');
  for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport);
    await expect(overview.locator('.factory-station').first()).toBeVisible();
    if (viewport.width < 768) await page.getByRole('button', { name: 'Select view', exact: true }).click();
    await expect(status).toBeVisible();
    await status.hover();
    await expect(page.locator('#dashboard-current-status-tooltip')).toContainText('The latest workflow evidence is from');
    const path = test.info().outputPath(`factory-overview-${viewport.width}.png`);
    await page.screenshot({ path, fullPage: true });
    await test.info().attach(`factory-overview-${viewport.width}`, {
      path,
      contentType: 'image/png'
    });
    if (viewport.width < 768) await page.getByRole('button', { name: 'Select view', exact: true }).click();
  }
});

test('fresh upstream evidence shows green on desktop and mobile', async ({ page }) => {
  await page.clock.setFixedTime(new Date('2026-09-15T12:00:00Z'));
  await page.goto(`${origin}/#page-overview`);
  const status = page.getByRole('button', { name: 'Dashboard data is current', exact: true });
  for (const viewport of [{ width: 1440, height: 900 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport);
    if (viewport.width < 768) await page.getByRole('button', { name: 'Select view', exact: true }).click();
    await expect(status).toBeVisible();
    await expect(status.locator('.octicon-check-circle-fill')).toBeVisible();
    await expect(page.locator('.dashboard-current-status')).toHaveAttribute('data-tone', 'success');
    await status.hover();
    await expect(page.locator('#dashboard-current-status-tooltip')).toContainText('Workflow evidence is current as of');
  }
});

test('dashboard status preserves refresh failure and recovery without the Overview duplicate', async ({ page }) => {
  await page.goto(`${origin}/#page-overview`);
  const status = page.getByRole('button', { name: 'Dashboard data is stale', exact: true });
  await expect(status).toBeVisible();
  await expect(page.locator('.dashboard-ingestion-status')).toHaveCount(0);
  await page.route(`${origin}/payload-hashes.json`, (route) => route.fulfill({
    status: 503,
    contentType: 'application/json',
    body: '{"error":"Temporarily unavailable"}'
  }));
  await page.evaluate(() => window.dispatchEvent(new Event('dashboard-refresh-request')));
  const refreshNotice = page.locator('[data-notification-id="dashboard:refresh-error"]');
  await expect(refreshNotice).toContainText('last complete snapshot');
  await expect(refreshNotice).toBeVisible();
  await expect(page.getByRole('button', { name: 'Dashboard refresh failed', exact: true }).locator('.octicon-stop')).toBeVisible();
  await expect(page.locator('.dashboard-current-status')).toHaveAttribute('data-tone', 'danger');
  await expect(page.locator('.dashboard-ingestion-status')).toHaveCount(0);
  await page.unroute(`${origin}/payload-hashes.json`);
  await page.evaluate(() => window.dispatchEvent(new Event('dashboard-refresh-request')));
  await expect(status).toBeVisible();
  await expect(status.locator('.octicon-alert')).toBeVisible();
  await expect(page).toHaveURL(`${origin}/#page-overview`);
});

test('marketplace page renders canonical package cards after ingestion', async ({ page }, testInfo) => {
  await page.goto(`${origin}/#page-marketplace`);

  const marketplace = page.locator('[data-page-id="marketplace"]');
  await expect(page.getByRole('heading', { name: 'Marketplace', exact: true, level: 1 })).toBeVisible();
  const packageCards = marketplace.locator('.entity-card-list-marketplace .entity-card-list-card');
  await expect(packageCards).toHaveCount(3);
  await expect(packageCards.locator('.issue-list-card-title')).toHaveText([
    'AW Optimization',
    'CAO Evolution',
    'Dependabot'
  ]);
  const packageCard = packageCards.filter({ hasText: 'Dependabot' });
  await expect(marketplace.locator('.entity-card-list-marketplace')).toHaveCSS('display', 'grid');
  await expect.poll(async () => {
    const cardBoxes = await packageCards.evaluateAll((cards) => cards.map((card) => card.getBoundingClientRect().toJSON()));
    return cardBoxes.length === 3
      && cardBoxes[1].y === cardBoxes[0].y
      && cardBoxes[1].x >= cardBoxes[0].x + cardBoxes[0].width + 12;
  }).toBe(true);
  await expect(marketplace).toContainText('Dependabot');
  await expect(packageCard).toContainText('By');
  await expect(packageCard).toContainText('githubnext');
  await expect(packageCards.filter({ hasText: 'Earlier package revision' })).toHaveCount(0);
  await expect(packageCard.getByRole('button', { name: 'Add' })).toHaveCount(0);
  await expect(marketplace).not.toContainText('Unable to load this page.');

  await expect(marketplace.locator('.marketplace-controls')).toHaveCount(0);
  await expect(marketplace.getByRole('searchbox')).toHaveCount(0);
  await expect(packageCards).toHaveCount(3);

  for (let navigation = 0; navigation < 3; navigation += 1) {
    await navigateToPage(page, 'repositories');
    await expect(page.getByRole('heading', { name: 'Repositories', exact: true, level: 1 })).toBeVisible();
    await navigateToPage(page, 'marketplace');
    await expect(page.getByRole('heading', { name: 'Marketplace', exact: true, level: 1 })).toBeVisible();
    await expect(packageCards).toHaveCount(3);
    await expect(packageCards.locator('.issue-list-card-title')).toHaveText([
      'AW Optimization',
      'CAO Evolution',
      'Dependabot'
    ]);
  }

  await packageCard.getByRole('link', { name: 'Dependabot' }).click();
  const detail = page.locator('[data-page-id="marketplace-package"]');
  await expect(detail).toBeVisible();
  await expect(page.locator('#page-title')).toHaveText('Dependabot');
  const add = detail.locator('.entity-card-list-actions .cli-action-trigger');
  await expect(add).toBeVisible();
  await expect(add).toHaveAccessibleName('Add');
  await expect(add.locator(':scope > .octicon')).toHaveCSS('color', 'rgb(255, 255, 255)');
  const dashboardRoot = page.locator('.dashboard-root');
  await dashboardRoot.evaluate((root) => root.setAttribute('data-theme', 'dark'));
  await expect(add).toHaveCSS('color', 'rgb(13, 17, 23)');
  await expect(add.locator(':scope > .octicon')).toHaveCSS('color', 'rgb(13, 17, 23)');
  await dashboardRoot.evaluate((root) => root.setAttribute('data-theme', 'light'));
  await add.click();
  await expect(page.locator('.cli-action-dialog .cli-action-command')).toHaveText(
    `gh cao add githubnext/gh-cao/dependabot@${'a'.repeat(40)}`
  );
  await page.screenshot({ path: testInfo.outputPath('marketplace-add-desktop.png') });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('.cli-action-dialog .cli-action-command')).toHaveText(
    `gh cao add githubnext/gh-cao/dependabot@${'a'.repeat(40)}`
  );
  await page.screenshot({ path: testInfo.outputPath('marketplace-add-mobile.png') });
  await page.getByRole('dialog').getByRole('button', { name: 'Cancel', exact: true }).click();
  await page.setViewportSize({ width: 1280, height: 720 });
  await expect(detail.locator('.dashboard-markdown')).toContainText('Keeps dependency updates moving.');
  const about = detail.locator('.link-button-list-view');
  await expect(about.getByRole('heading', { name: 'About' })).toBeVisible();
  const repositoryLink = about.getByRole('link', { name: 'Open githubnext/gh-cao on GitHub' });
  await expect(repositoryLink).toHaveAttribute('href', 'https://github.com/githubnext/gh-cao');
  await expect(about.locator('.link-button-list')).toHaveCSS('border-width', '0px');
  const readmeBox = await detail.locator('.dashboard-markdown').boundingBox();
  const aboutBox = await about.boundingBox();
  expect(aboutBox?.x ?? 0).toBeGreaterThan((readmeBox?.x ?? 0) + (readmeBox?.width ?? 0));
});

test('marketplace cards stay within their grid at mobile, tablet, and desktop widths', async ({ page }) => {
  await page.goto(`${origin}/#page-marketplace`);
  const cards = page.locator('[data-page-id="marketplace"] .entity-card-list-card');
  await expect(cards).toHaveCount(3);

  for (const width of [320, 375, 390, 420, 430, 700, 768, 1440]) {
    await page.setViewportSize({ width, height: 900 });
    await expect.poll(() => cards.evaluateAll((items) => items.length === 3 && items.every((card) => {
      const bounds = card.getBoundingClientRect();
      return bounds.width >= 160 && card.scrollWidth <= card.clientWidth + 1
        && bounds.left >= 0 && bounds.right <= window.innerWidth
        && [...card.children].every((child) => {
          const box = child.getBoundingClientRect();
          return box.width === 0 || (box.left >= bounds.left && box.right <= bounds.right);
        });
    })), { message: `Card content should fit at ${width}px` }).toBe(true);

    if (width <= 700) {
      await expect.poll(() => page.locator('[data-page-id="marketplace"] .document-list-header > p').evaluate((paragraph) => {
        const bounds = paragraph.getBoundingClientRect();
        return bounds.width > 0 && bounds.left >= 16 && bounds.right <= innerWidth - 16;
      }), { message: `Introductory text should have mobile gutters at ${width}px` }).toBe(true);
      await expect.poll(() => cards.evaluateAll((items) => {
        if (items.length !== 3) return false;
        const [first, second] = items.map((card) => card.getBoundingClientRect());
        return first.width >= 160 && second.x === first.x && second.y > first.bottom;
      }), { message: `Cards should stack at ${width}px` }).toBe(true);
    }
  }

  await page.setViewportSize({ width: 390, height: 844 });
  const link = cards.first().getByRole('link', { name: 'AW Optimization' });
  await link.focus();
  await expect(link).toBeFocused();
  await page.keyboard.press('Enter');
  const detail = page.locator('[data-page-id="marketplace-package"]');
  await expect(detail.locator('.entity-card-list-card')).toHaveCount(1);
  await expect(detail.locator('.entity-card-list-actions .cli-action-trigger')).toBeVisible();
  await expect.poll(() => detail.locator('.entity-card-list-card').evaluate((card) =>
    card.scrollWidth <= card.clientWidth + 1)).toBe(true);
});

for (const width of [390, 1440]) {
  test(`marketplace progressively loads canonical package pages at ${width}px`, async ({ context, page }) => {
    const packageRows = Array.from({ length: 65 }, (_, index) => ({
      ...inventory['marketplace-packages'].rows[0],
      id: `package-${index}`,
      name: `Package ${String.fromCharCode(65 + Math.floor(index / 26))}${String.fromCharCode(65 + index % 26)}`,
      path: `package-${index}`,
      source: `githubnext/gh-cao/package-${index}@${'a'.repeat(40)}`,
      description: 'A long package description that explains its purpose and operational scope. '.repeat(8)
    }));
    await context.route(`${origin}/inventory-sources.json`, route => route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        ...inventory,
        'marketplace-packages': { ...inventory['marketplace-packages'], rows: packageRows }
      })
    }));
    await page.setViewportSize({ width, height: 844 });
    await page.addInitScript(() => {
      document.addEventListener('dashboard-data', event => {
        if (event instanceof CustomEvent && event.detail.kind === 'refresh') {
          document.documentElement.dataset.testRefreshState = event.detail.status;
        }
      });
    });
    await page.goto(`${origin}/#page-marketplace`);
    await expect(page.locator('html')).toHaveAttribute('data-test-refresh-state', 'completed');
    const marketplace = page.locator('[data-page-id="marketplace"]');
    const cards = marketplace.locator('.entity-card-list-card');
    await expect(cards).toHaveCount(25);
    await expect(cards.locator('.issue-list-card-title')).toHaveText(packageRows.slice(0, 25).map(row => row.name));
    const first = cards.first().getByRole('link', { name: packageRows[0].name });
    const firstCard = await cards.first().elementHandle();
    const more = marketplace.getByRole('button', { name: 'Load more', exact: true });
    await more.scrollIntoViewIfNeeded();
    await expect(cards).toHaveCount(50);
    await expect(first).toHaveCount(1);
    expect(await firstCard?.evaluate((card) => card.isConnected)).toBe(true);
    await more.scrollIntoViewIfNeeded();
    await expect(cards).toHaveCount(65);
    await expect(more).toBeHidden();
    await expect(marketplace.getByRole('status')).toHaveText('Showing 65 of 65 items');
    expect(await cards.evaluateAll((items) => new Set(items.map((card) => card.textContent)).size)).toBe(65);
    const last = cards.last().getByRole('link', { name: packageRows[64].name });
    await last.focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('[data-page-id="marketplace-package"] .entity-card-list-card')).toContainText(packageRows[64].name);
    await page.goBack();
    await expect(cards).toHaveCount(65);
  });
}

test('outcome sections settle into empty states when no outcome is selected', async ({ page }) => {
  await page.goto(`${origin}/#page-outcome-detail`);
  const outcome = page.locator('[data-page-id="outcome-detail"]');
  await expect(outcome).toBeVisible();
  for (const id of ['outcome-discussion', 'outcome-metadata']) {
    const section = outcome.locator(`[data-view-id="${id}"]`);
    await expect(section.locator('[data-view-state="empty"]')).toBeVisible();
    await expect(section.locator('[aria-busy="true"]')).toHaveCount(0);
  }
});

test('package detail failure offers recovery without rendering unrelated sections', async ({ page }) => {
  await page.goto(`${origin}/#page-marketplace-package?package-source=missing-package`);
  const detail = page.locator('[data-page-id="marketplace-package"]');
  await expect(detail.getByRole('heading', { name: 'Package unavailable' })).toBeVisible();
  await expect(detail).toContainText('It may have been removed');
  await expect(detail).not.toContainText('marketplace-package-detail');
  await expect(detail.getByRole('heading', { name: 'README' })).toBeHidden();
  await expect(detail.getByRole('heading', { name: 'About' })).toBeHidden();
  await expect(detail.getByRole('button', { name: 'Retry' })).toBeVisible();
  await detail.getByRole('button', { name: 'Retry' }).click();
  await expect(detail.getByRole('heading', { name: 'Package unavailable' })).toBeVisible();
  await detail.getByRole('link', { name: 'Back to Marketplace' }).click();
  await expect(page.locator('[data-page-id="marketplace"] .entity-card-list-card')).toHaveCount(3);
});

test('a partially loaded package keeps its metadata and identifies incomplete data', async ({ context, page }) => {
  await context.route(`${origin}/inventory-sources.json`, (route) => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({
      ...inventory,
      'marketplace-packages': {
        ...inventory['marketplace-packages'],
        metadata: { ...inventory['marketplace-packages'].metadata, completeness: 'partial' }
      }
    })
  }));
  await page.goto(`${origin}/#page-marketplace`);
  await page.locator('[data-page-id="marketplace"] .entity-card-list-card')
    .filter({ hasText: 'Dependabot' }).getByRole('link', { name: 'Dependabot' }).click();
  const detail = page.locator('[data-page-id="marketplace-package"]');
  await expect(detail).toContainText('Some package information could not be retrieved');
  await expect(detail.locator('.entity-card-list-card')).toHaveCount(1);
  await expect(detail.getByRole('heading', { name: 'Package unavailable' })).toBeHidden();
});

test('Marketplace is restored immediately after viewing a package without reloading the document', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 640 });
  /** @type {string[]} */
  const documentLoads = [];
  page.on('request', (request) => {
    if (request.isNavigationRequest()) documentLoads.push(request.url());
  });
  await page.goto(`${origin}/#page-marketplace`);
  const marketplace = page.locator('[data-page-id="marketplace"]');
  await expect(marketplace.locator('.entity-card-list-card')).toHaveCount(3);
  const scrollBefore = await page.locator('main.dashboard-prototype').evaluate((main) => {
    main.scrollTop = 120;
    return main.scrollTop;
  });
  const card = marketplace.locator('.entity-card-list-card').filter({ hasText: 'AW Optimization' });
  await card.getByRole('link', { name: 'AW Optimization' }).click();
  await expect(page.locator('[data-page-id="marketplace-package"] .entity-card-list-card')).toHaveCount(1);
  await page.goBack();
  await expect(marketplace.locator('.entity-card-list-card')).toHaveCount(3);
  await expect.poll(() => page.locator('main.dashboard-prototype').evaluate((main) => main.scrollTop)).toBe(scrollBefore);
  expect(documentLoads).toHaveLength(1);
});

test('deep links and redirect routes fetch only the requested initial page chunk', async ({ page }) => {
  const hashChunkRequests = captureChunkRequests(page);
  await page.goto(`${origin}/#page-runs`);
  await expect(page.getByRole('heading', { name: 'Runs', exact: true, level: 1 })).toBeVisible();
  await expect.poll(() => pageText(page, 'runs')).toMatch(/Runs in the last week/);
  await expect.poll(() => [...new Set(hashChunkRequests)].sort()).toEqual(['runs']);

  const routeChunkRequests = captureChunkRequests(page);
  await page.goto(`${origin}/repositories/`);
  await expect(page).toHaveURL(`${origin}/#page-repositories`);
  await expect(page.getByRole('heading', { name: 'Repositories', exact: true, level: 1 })).toBeVisible();
  await expect.poll(() => pageText(page, 'repositories')).toMatch(/Top repositories with issues/);
  await expect.poll(() => [...new Set(routeChunkRequests)].sort()).toEqual(['repositories']);
});

test('Settings remains useful when its delayed policy source is unavailable', async ({ context, page }) => {
  const inventoryWithoutPolicy = Object.fromEntries(
    Object.entries(inventory).filter(([name]) => name !== 'configuration-policy')
  );
  await context.route(`${origin}/inventory-sources.json`, (route) => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify(inventoryWithoutPolicy),
  }));

  await page.goto(`${origin}/#page-configuration`);

  await expect(page.locator('[data-page-id="configuration"] .configuration-view')).toBeVisible();
  await expect(page.locator('[data-page-id="configuration"]')).toContainText(
    'The policy cannot be edited until it contains valid JSON.'
  );
  await expect(page.locator('[data-page-id="configuration"]')).not.toContainText(
    'Affected source: configuration-policy'
  );
});
