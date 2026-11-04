import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { authoritativeDashboard, expect, registerSmokeRoutes, test } from './helpers/smoke-fixtures.js';
import { adaptCachedGhAwJsonl } from '../../src/data/adapters/gh-aw-logs.js';
import { normalize } from '../../src/data/normalize/index.js';
import { CANONICAL_SCHEMA_VERSION } from '../../src/data/model/schema.js';
import { NORMALIZED_JSONL_INGESTION_VERSION } from '../../src/data/ingest/coordinator.js';
import ledgerFixture from '../fixtures/operational-value-ledger.json' with { type: 'json' };

registerSmokeRoutes();

/** @param {typeof ledgerFixture} ledger @param {{includeEvidence?: boolean, defer?: string | null, previousHealth?: string, measurements?: string}} [options] */
function publishedShards(ledger, { includeEvidence = true, defer = 'shared-budget-unavailable', previousHealth = '', measurements = '' } = {}) {
  const root = resolve('../..');
  mkdirSync(join(root, '.tmp'), { recursive: true });
  const directory = mkdtempSync(join(root, '.tmp', 'workflow-value-browser-'));
  try {
    const input = join(directory, 'ledger.json');
    const output = join(directory, 'pulse.html');
    writeFileSync(input, JSON.stringify(ledger));
    execFileSync(process.execPath, [
      '.github/skills/add-operational-value/scripts/render-pulse.mjs', input, output
    ], { cwd: root, stdio: 'pipe' });
    const policy = join(directory, 'cao.json');
    writeFileSync(policy, JSON.stringify({ version: 1, 'gh-aw-version': 'v0.93.1', 'control-plane': {
      scope: { 'allowed-repositories': ['octo/example'] }, activity: { repositories: ['octo/example'] },
      campaigns: { activity: { 'workflow-values': [
        { repository: 'octo/example', workflow: 'verify-bugs', collector: 'collectors/not-imported.mjs' },
        { repository: 'octo/example', workflow: 'disabled-worker', enabled: false }
      ] } },
    } }));
    const health = join(directory, 'collection.jsonl');
    if (previousHealth) writeFileSync(health, previousHealth);
    execFileSync(process.execPath, [
      'activity/workflow-values.mjs', '--root', directory, '--policy', policy,
      '--repository', 'octo/example', '--output', join(directory, 'values.jsonl'),
      '--healthOutput', health, '--workspace', join(directory, 'proofs'),
      ...(defer ? ['--defer', defer] : [])
    ], { cwd: root, stdio: 'pipe' });
    const original = readFileSync(health, 'utf8') + (includeEvidence ? readFileSync(join(directory, 'pulse-evidence.jsonl'), 'utf8') : '') + measurements;
    const batch = normalize(adaptCachedGhAwJsonl(original).observations);
    const records = Object.entries(batch).flatMap(([collection, rows]) =>
      Array.isArray(rows) ? rows.map(record => ({ kind: 'record', collection, record })) : []);
    const runs = new Set(['campaigns', 'repositories', 'workflows', 'runs']);
    /** @type {Record<string, string>} */
    const manifest = {};
    /** @type {Record<string, string>} */
    const contents = {};
    for (const phase of ['runs', 'records']) {
      const selected = records.filter(({ collection }) => runs.has(collection) === (phase === 'runs'));
      const content = [
        { kind: 'metadata', schemaVersion: CANONICAL_SCHEMA_VERSION, ingestionVersion: NORMALIZED_JSONL_INGESTION_VERSION,
          sourceRecords: original.trim().split('\n').length, phase, records: selected.length },
        ...selected
      ].map(record => JSON.stringify(record)).join('\n') + '\n';
      const hash = createHash('sha256').update(content).digest('hex');
      const name = `gh-aw-logs-${phase}/logs-${hash}-${'0'.repeat(16)}.jsonl`;
      manifest[name] = hash;
      contents[`/${name}`] = content;
    }

    return { manifest, contents, health: readFileSync(health, 'utf8') };
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

function nativeMeasurements() {
  const root = resolve('../..');
  const directory = mkdtempSync(join(root, '.tmp', 'batch-value-browser-'));
  try {
    execFileSync(process.execPath, [
      '.github/skills/add-operational-value/scripts/evaluate.mjs', '--native',
      '--function', 'tests/fixtures/operational-value/native-workflow.mjs',
      '--output-dir', directory, '--end', '2026-10-15T00:00:00Z', 'example/service', 'incident-response'
    ], { cwd: root, stdio: 'pipe' });
    return readFileSync(join(directory, 'example-service/incident-response-measurements.jsonl'), 'utf8');
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

for (const width of [1440, 390]) {
  test(`workflow Value card opens native evidence and exact items at ${width}px`, async ({ page, context }) => {
    await page.setViewportSize({ width, height: 900 });
    let publication = publishedShards(structuredClone(ledgerFixture), { includeEvidence: false, defer: 'api-budget' });
    const inventory = {
      campaigns: { rows: [{ campaign: 'quality', 'campaign-name': 'Quality' }], metadata: { 'as-of': ledgerFixture.observedAt } },
      repositories: { rows: [{ organization: 'octo', repository: 'example' }], metadata: { 'as-of': ledgerFixture.observedAt } },
      workflows: { rows: [
        { organization: 'octo', repository: 'example', workflow: ledgerFixture.sourcePath,
          'workflow-name': ledgerFixture.workflowName, 'workflow-role': 'worker',
          campaign: 'quality', 'campaign-name': 'Quality' },
        { organization: 'octo', repository: 'example', workflow: '.github/workflows/disabled-worker.md',
          'workflow-name': 'Disabled worker', 'workflow-role': 'worker',
          campaign: 'quality', 'campaign-name': 'Quality' },
        { organization: 'octo', repository: 'example', workflow: '.github/workflows/standalone-check.md',
          'workflow-name': 'Standalone check', 'workflow-role': 'standalone' }
      ], metadata: { 'as-of': ledgerFixture.observedAt } }
    };
    await context.route('http://dashboard.test/inventory-sources.json', route => route.fulfill({
      contentType: 'application/json', body: JSON.stringify(inventory)
    }));
    await context.route('http://dashboard.test/payload-hashes.json', route => route.fulfill({
      contentType: 'application/json', body: JSON.stringify(publication.manifest)
    }));
    await context.route('http://dashboard.test/gh-aw-logs-*/**', route => route.fulfill({
      contentType: 'application/x-ndjson', body: publication.contents[new URL(route.request().url()).pathname]
    }));
    await page.evaluate(async ({ dashboard }) => {
      const { renderDashboard, dashboardPageSourceNames } = await import(`${location.origin}/src/presenter.js`);
      const { loadCanonicalDashboardSources, subscribeCanonicalDashboardView } = await import(`${location.origin}/src/data-processor.js`);
      location.hash = '#page-workflow-runtime?workflow=octo%2Fexample%3A.github%2Fworkflows%2Fverify-bugs.md';
      const model = { languageVersion: dashboard['language-version'], dashboard: dashboard.dashboard };
      const queryContext = { pages: model.dashboard.pages, queries: model.dashboard.queries, views: model.dashboard.views };
      await loadCanonicalDashboardSources(`${location.origin}/payload-hashes.json`, [], queryContext);
      let subscription = 0;
      const subscribe = (
        /** @type {string} */ pageId,
        /** @type {string[]} */ names,
        /** @type {import('../../src/presenter.js').PageSourceLoadOptions} */ options,
        /** @type {string | undefined} */ viewId = undefined
      ) => new Promise((resolve, reject) => {
        let initial = true;
        subscribeCanonicalDashboardView(`value-browser:${++subscription}`, names, queryContext,
          (/** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ sources) => {
            if (initial) { initial = false; resolve(sources); } else options.onUpdate(sources);
          }, {}, { signal: options.signal, pageId, viewId, routeParameters: options.routeParameters,
            queryContext: options.queryContext, onError: reject });
      });
      const loadPageSources = (
        /** @type {string} */ pageId,
        /** @type {import('../../src/presenter.js').PageSourceLoadOptions} */ options
      ) => subscribe(pageId, dashboardPageSourceNames(model, pageId), options);
      loadPageSources.subscribeViewSources = (
        /** @type {string} */ pageId, /** @type {string} */ viewId, /** @type {string[]} */ names,
        /** @type {import('../../src/presenter.js').PageSourceLoadOptions} */ options
      ) => subscribe(pageId, names, options, viewId);
      document.querySelector('#root')?.append(renderDashboard({ document: model, sources: {}, loadPageSources }));
    }, { dashboard: authoritativeDashboard });
    const topology = await page.evaluate(async ({ dashboard }) => {
      const { processDataRequest } = await import(`${location.origin}/src/data-worker.js`);
      const result = await processDataRequest({
        operation: 'query-canonical-dashboard', sourceNames: ['workflows'],
        context: { pages: dashboard.dashboard.pages, queries: dashboard.dashboard.queries }
      });
      return result.workflows.rows;
    }, { dashboard: authoritativeDashboard });
    expect(topology).toEqual(expect.arrayContaining([
      expect.objectContaining({ campaign: 'quality', 'workflow-role': 'worker' }),
      expect.objectContaining({ workflow: '.github/workflows/standalone-check.md', 'workflow-role': 'standalone' })
    ]));

    await expect(page.locator('.workflow-tabs').getByRole('link', { name: 'Value', exact: true }))
      .toHaveCount(0);
    if (width < 600) {
      await page.locator('.mobile-nav-menu > summary').click();
      await page.locator('.mobile-nav-menu a[href="#page-value"]').click();
    } else {
      await page.getByRole('navigation', { name: 'Primary', exact: true }).getByRole('link', { name: 'Value', exact: true }).click();
    }
    await expect(page).toHaveURL(/#page-value$/);
    await expect(page.getByRole('heading', { level: 1, name: 'Value', exact: true })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Repository measurements', exact: true })).toHaveCount(0);
    await expect(page.locator('a[href^="#page-operational-value"]')).toHaveCount(0);
    await expect(page.getByText('GitHub API capacity is exhausted or reserved for ordinary Activity collection.', { exact: true }).first()).toBeVisible();
    await expect(page.getByText('No retained capture', { exact: true }).first()).toBeVisible();
    await expect(page.getByText('disabled', { exact: true }).first()).toBeVisible();
    await expect(page.getByRole('link', { name: '↓ -1 defects', exact: true })).toHaveCount(0);
    const valueTabs = page.locator('[data-page-id="value"] .route-tab-navigation');
    await expect(valueTabs.getByRole('link', { name: 'All', exact: true })).toBeVisible();
    await expect(valueTabs.getByRole('link', { name: 'Campaigns', exact: true })).toBeVisible();
    await expect(valueTabs.getByRole('link', { name: 'Standalone', exact: true })).toBeVisible();
    await expect(valueTabs.getByRole('link', { name: 'Needs review', exact: true })).toBeVisible();
    await valueTabs.getByRole('link', { name: 'Campaigns', exact: true }).click();
    await expect(page).toHaveURL(/#page-value-campaigns$/);
    await expect(page.getByRole('heading', { level: 1, name: 'Campaign Value', exact: true })).toBeVisible();
    await page.getByRole('link', { name: 'Quality', exact: true }).click();
    await expect(page).toHaveURL(/#page-campaign-value\?campaign=quality$/);
    await expect(page.getByRole('heading', { level: 1, name: 'Campaign Value', exact: true })).toBeVisible();
    await expect(page.getByRole('link', { name: 'octo/example · Verify bugs', exact: true })).toBeVisible();
    await page.goBack();
    await expect(page).toHaveURL(/#page-value-campaigns$/);
    await page.locator('[data-page-id="value-campaigns"] .route-tab-navigation')
      .getByRole('link', { name: 'Standalone', exact: true }).click();
    await expect(page).toHaveURL(/#page-value-standalone$/);
    await expect(page.getByRole('link', { name: 'octo/example · Standalone check', exact: true })).toBeVisible();
    await page.locator('[data-page-id="value-standalone"] .route-tab-navigation')
      .getByRole('link', { name: 'Needs review', exact: true }).click();
    await expect(page).toHaveURL(/#page-value-review$/);
    await expect(page.getByRole('link', { name: 'octo/example · Standalone check', exact: true })).toBeVisible();
    await page.locator('[data-page-id="value-review"] .route-tab-navigation')
      .getByRole('link', { name: 'All', exact: true }).click();
    await expect(page).toHaveURL(/#page-value$/);
    await page.getByRole('link', { name: 'octo/example · Verify bugs', exact: true }).click();
    await expect(page).toHaveURL(/#page-workflow-value\?workflow=/);
    await page.goBack();
    await expect(page).toHaveURL(/#page-value$/);
    publication = publishedShards(structuredClone(ledgerFixture), { defer: null, previousHealth: publication.health });
    await page.evaluate(async ({ dashboard }) => {
      const { loadCanonicalDashboardSources } = await import(`${location.origin}/src/data-processor.js`);
      await loadCanonicalDashboardSources(`${location.origin}/payload-hashes.json`, [], {
        pages: dashboard.dashboard.pages, queries: dashboard.dashboard.queries, views: dashboard.dashboard.views
      });
    }, { dashboard: authoritativeDashboard });
    await expect(page.getByText('Rich evidence retained').first()).toBeVisible();
    await expect(page.getByText('The collector failed; previous accepted evidence is retained.', { exact: true }).first()).toBeVisible();
    await expect(page.getByText('failed', { exact: true }).first()).toBeVisible();
    await expect(page.getByText('↓ -1 defects')).toHaveCount(1);
    await page.locator('a[href^="#page-value-report?report="]').click();
    await expect(page).toHaveURL(/#page-value-report\?report=/);
    await expect(page.locator('.value-report-heading')).toContainText('Resolve confirmed defects.');
    await expect(page.locator('.value-report-movement')).toHaveText('↓ -1 defects · Better');
    const outputs = page.getByRole('button', { name: 'Safe outputs: 1 issues. Explore evidence.' });
    await outputs.focus();
    await outputs.press('Enter');
    const outputCategory = page.locator('[data-value-section="outputs"]');
    const outputSummary = outputCategory.locator(':scope > summary');
    await expect(outputCategory).toHaveJSProperty('open', true);
    await expect(outputSummary).toBeFocused();
    await expect(outputSummary).toContainText('1 output');
    await expect(page.locator('[data-value-section][open]')).toHaveCount(1);
    await expect(page.locator('[data-value-section="trails"] a[href="https://github.com/octo/example/pull/2"]')).toHaveCount(1);
    await page.getByRole('button', { name: 'Quality checks and evidence limits' }).click();
    await expect(page.locator('[data-value-section="checks"]')).toHaveJSProperty('open', true);
    await expect(page.locator('[data-value-section="checks"] > summary')).toBeFocused();
    await expect(outputCategory).not.toHaveAttribute('open', '');
    await page.locator('details.value-report-evidence > summary').click();
    await page.locator('.value-report-plot svg a').first().locator('circle').click();
    await expect(page).toHaveURL(/#page-value-snapshot\?snapshot=/);
    expect(new URLSearchParams(page.url().split('?')[1]).has('report')).toBe(false);
    await expect(page.locator('.value-report-items a')).toHaveAttribute('href', 'https://github.com/octo/example/issues/1');
    await page.getByRole('link', { name: '← Value report' }).click();
    await expect(page.locator('.value-report-movement')).toHaveText('↓ -1 defects · Better');

    const newer = structuredClone(ledgerFixture);
    newer.observedAt = '2026-10-20T12:01:00Z';
    newer.window.endAt = '2026-10-20T00:01:00Z';
    newer.repositoryState.observations.push({
      at: newer.window.endAt, value: 2, ref: 'new123', url: 'https://github.com/octo/example/tree/new123',
      items: [{ label: 'Defect 3', url: 'https://github.com/octo/example/issues/3' },
        { label: 'Defect 4', url: 'https://github.com/octo/example/issues/4' }]
    });
    publication = publishedShards(newer, { previousHealth: publication.health });
    await page.evaluate(async ({ dashboard }) => {
      const { loadCanonicalDashboardSources } = await import(`${location.origin}/src/data-processor.js`);
      await loadCanonicalDashboardSources(`${location.origin}/payload-hashes.json`, [], {
        pages: dashboard.dashboard.pages, queries: dashboard.dashboard.queries, views: dashboard.dashboard.views
      });
    }, { dashboard: authoritativeDashboard });
    await expect(page.locator('.value-report-movement')).toHaveText('↑ +1 defects · Worse');
    await expect(page.locator('.value-report-number')).toHaveText('2 defects');
    expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(1);
    await page.emulateMedia({ colorScheme: 'dark' });
    await expect(page.locator('.value-report-movement')).toBeVisible();

    publication = publishedShards(newer, { previousHealth: publication.health, measurements: nativeMeasurements() });
    await page.evaluate(async ({ dashboard }) => {
      const { loadCanonicalDashboardSources } = await import(`${location.origin}/src/data-processor.js`);
      await loadCanonicalDashboardSources(`${location.origin}/payload-hashes.json`, [], {
        pages: dashboard.dashboard.pages, queries: dashboard.dashboard.queries, views: dashboard.dashboard.views
      });
      location.hash = '#page-workflow-value?workflow=example%2Fservice%3A.github%2Fworkflows%2Fincident-response.md';
    }, { dashboard: authoritativeDashboard });
    await expect(page.locator('[data-page-id="workflow-value"] .route-tab-navigation')).toHaveCount(0);
    await expect(page.getByRole('link', { name: 'Not measured', exact: true })).toBeVisible();
    await page.getByRole('link', { name: 'Not measured', exact: true }).click();
    await expect(page).toHaveURL(/#page-value-measurement\?/);
    const measurementPage = page.locator('[data-page-id="value-measurement"]');
    await expect(measurementPage).toHaveAttribute('data-view-mode', /^(card|table)$/);
    if (width < 768) {
      if (await measurementPage.getAttribute('data-view-mode') !== 'table') {
        await page.getByRole('button', { name: 'Switch to Table view', exact: true }).click();
      }
    } else {
      await page.getByRole('button', { name: 'Table', exact: true }).click();
    }
    await expect(page.getByRole('cell', { name: 'Incomplete retained deliveries', exact: true })).toBeVisible();
    await page.getByRole('button', { name: width < 768 ? 'Switch to Cards view' : 'Cards', exact: true }).click();
    await expect(measurementPage).toHaveAttribute('data-view-mode', 'card');
    await expect(page.getByLabel('Not measured metadata').getByText('Incomplete retained deliveries', { exact: true })).toBeVisible();
    await expect(page.getByText('interval', { exact: true }).first()).toBeVisible();
    await expect(page.getByText('Period and evidence identity', { exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)).toBeLessThanOrEqual(1);
  });
}
