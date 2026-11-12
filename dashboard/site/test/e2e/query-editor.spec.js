import { test, expect } from '@playwright/test';
import { cp, mkdtemp, readFile, rm } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { join, relative, sep } from 'node:path';
import previewDocument from '../fixtures/query-editor.json' with { type: 'json' };
import sources from '../fixtures/query-editor-sources.json' with { type: 'json' };
import { normalizedRunShard } from './normalized-shard.js';
import { writeCanvasArtifact } from './canvas-fixture.js';
import { createSqliteIndexedDB } from '../../src/data/storage/sqlite-indexeddb.js';
import { ingestDashboardSources } from '../../src/data/ingest/coordinator.js';

test.describe('Go canvas query editor', () => {
/** @type {{ url: string, close: () => Promise<void> }} */
let server;
let workspace = '';
let artifactDirectory = '';

test.beforeAll(async () => {
  const { startCanvasServer } = await import(new URL('../../../../tests/helpers/canvas-snapshot.mjs', import.meta.url).href);
  workspace = await mkdtemp(fileURLToPath(new URL('../../../../.cao-dashboard-e2e-', import.meta.url)));
  const siteRoot = fileURLToPath(new URL('../../', import.meta.url));
  await cp(siteRoot, join(workspace, 'site'), {
    recursive: true,
    filter: (path) => !['node_modules', 'dist', '.tmp', 'test', 'test-results', 'scripts'].includes(relative(siteRoot, path).split(sep)[0])
  });
  server = await startCanvasServer({
    workingDirectory: workspace,
    siteRoot: join(workspace, 'site'),
    catalogRoot: null, port: 0,
    output: () => {}, requestOutput: () => {}, traceOutput: () => {},
    executeCliAction: async () => ({}), approveCliAction: async () => false,
    generateQuery: async () => ({ document: JSON.stringify(previewDocument, null, 2) }),
    enhanceQueryIntent: async (/** @type {Record<string, string>} */ request) => Object.fromEntries(
      ['intent', 'subject', 'objective', 'acceptance'].map((name) => [name, `Enhanced ${name}: ${request[name] || 'measurable criteria'}`])
    ),
    prepareSnapshot: async (/** @type {string} */ destination) => {
      artifactDirectory = destination;
      await writeCanvasArtifact(destination, {
        sources, shards: [normalizedRunShard(await readFile(new URL('../fixtures/query-editor-runs.jsonl', import.meta.url), 'utf8'))]
      });
    },
  });
});
test.afterAll(async () => {
  await server?.close();
  if (workspace) await rm(workspace, { recursive: true, force: true });
});

/** @param {import('@playwright/test').Page} page */
async function openEditor(page) {
  await page.addInitScript(() => {
    IDBFactory.prototype.open = () => { throw new Error('Go canvas must not open browser IndexedDB.'); };
  });
  await page.goto(`${server.url}/?local-preview=canvas#page-query-editor`);
  await expect(page.locator('meta[name="dashboard-data-backend"]')).toHaveAttribute('content', 'server-http');
  await expect(page.getByRole('textbox', { name: 'Intent', exact: true })).toBeVisible();
  const experimental = page.locator('.org-sidebar [data-nav-section="Experimental"]');
  await expect(experimental.getByRole('link', { name: 'Query editor', exact: true, includeHidden: true }))
    .toHaveAttribute('href', '#page-query-editor');
  await expect(page.locator('.org-sidebar [data-nav-section="Canvas"]')).toHaveCount(0);
  await expect(page.getByRole('dialog', { name: 'Preparing your dashboard' })).toBeHidden();
}

for (const width of [1280, 390]) {
  test(`canvas editor generates, validates, subscribes and enhances all fields at ${width}px`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    await openEditor(page);
    await page.getByRole('textbox', { name: 'Intent', exact: true }).fill('Compare run conclusions');
    await page.getByRole('textbox', { name: 'Subject', exact: true }).fill('Workflow runs');
    await page.getByRole('textbox', { name: 'Acceptance criteria', exact: true }).fill('Show native conclusion counts');
    const improve = page.getByRole('button', { name: 'Improve all fields with Copilot', exact: true });
    await expect(improve).toBeVisible();
    await expect(improve).toHaveText('');
    await expect(improve).toHaveAttribute('title', 'Improve all fields with Copilot');
    await expect(improve.locator('svg')).toHaveCount(1);
    const box = await improve.boundingBox();
    expect(box?.width).toBeGreaterThanOrEqual(44);
    expect(box?.height).toBeGreaterThanOrEqual(44);
    await improve.click();
    for (const label of ['Intent', 'Subject', 'Objective (optional)', 'Acceptance criteria']) {
      await expect(page.getByRole('textbox', { name: label, exact: true })).toHaveValue(/^Enhanced /);
    }
    await page.getByRole('button', { name: 'Generate query and view', exact: true }).click();
    await expect(page.locator('.query-editor > [role=status]')).toContainText('Preview updated');
    const preview = page.locator('.query-editor-preview');
    await expect(preview.getByRole('heading', { name: 'Run conclusions', exact: true })).toBeVisible();
    await expect(preview.locator('svg').first()).toBeVisible();
    await expect(page.getByRole('textbox', { name: 'Dashboard Language document', exact: true })).toHaveCount(0);
    await expect(page.getByText('Dashboard Language source (advanced)', { exact: true })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Validate and render', exact: true })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Copy Dashboard Language', exact: true })).toHaveCount(0);
    let invalidAttempts = 0;
    await page.route('**/__query_designer', (route) => {
      invalidAttempts += 1;
      return route.fulfill({ json: { document: '[broken' } });
    });
    await page.getByRole('button', { name: 'Generate query and view', exact: true }).click();
    await expect(page.locator('.query-editor > [role=status]')).toContainText('after 10 attempts');
    expect(invalidAttempts).toBe(10);
    await expect(page.locator('.query-editor-errors')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Save as custom view', exact: true })).toBeDisabled();
    await expect(preview.locator('svg').first()).toBeVisible();
    await page.unroute('**/__query_designer');
    const correction = page.waitForRequest((request) => request.url().endsWith('/__query_designer'));
    await page.getByRole('button', { name: 'Generate query and view', exact: true }).click();
    expect((await correction).postDataJSON()).toMatchObject({ document: '[broken', feedback: expect.any(String) });
    await expect(page.locator('.query-editor > [role=status]')).toContainText('Preview updated');
    await expect(page.locator('.query-editor-errors')).toBeHidden();
    await expect(page.getByRole('button', { name: 'Save as custom view', exact: true })).toBeEnabled();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    const screenshot = testInfo.outputPath(`query-editor-${width}.png`);
    await page.screenshot({ path: screenshot, fullPage: true });
    await testInfo.attach(`query-editor-${width}`, { path: screenshot, contentType: 'image/png' });
  });
}

test('saves a validated rendered view locally and restores it after reload', async ({ page }) => {
  await openEditor(page);
  const save = page.getByRole('button', { name: 'Save as custom view', exact: true });
  await expect(save).toBeDisabled();
  await page.getByRole('textbox', { name: 'Intent', exact: true }).fill('Compare run conclusions');
  await page.getByRole('textbox', { name: 'Subject', exact: true }).fill('Workflow runs');
  await page.getByRole('textbox', { name: 'Acceptance criteria', exact: true }).fill('Show native conclusion counts');
  await page.getByRole('button', { name: 'Generate query and view', exact: true }).click();
  await expect(save).toBeEnabled();
  await save.click();
  await expect(page).toHaveURL(/#page-local-[a-f0-9]{24}$/);
  const rendered = page.locator('.dashboard-page:not([hidden])');
  await expect(rendered.getByRole('heading', { name: 'Run conclusions', exact: true })).toBeVisible();
  await expect(rendered.locator('svg').first()).toBeVisible();
  await expect(page.locator('.query-editor')).toHaveCount(0);
  const id = new URL(page.url()).hash.slice('#page-'.length);
  const saved = JSON.parse(await readFile(join(workspace, '.cao/dashboard/custom-views', `${id}.json`), 'utf8'));
  expect(saved).toEqual(previewDocument);
  await page.reload();
  await expect(rendered.getByRole('heading', { name: 'Run conclusions', exact: true })).toBeVisible();
  await expect(rendered.locator('svg').first()).toBeVisible();
});

for (const acceptedAttempt of [2, 10]) {
test(`generation repairs validator diagnostics and accepts valid output on attempt ${acceptedAttempt}`, async ({ page }) => {
  await openEditor(page);
  await page.getByRole('textbox', { name: 'Intent', exact: true }).fill('Compare run conclusions');
  await page.getByRole('textbox', { name: 'Subject', exact: true }).fill('Workflow runs');
  await page.getByRole('textbox', { name: 'Acceptance criteria', exact: true }).fill('Show native conclusion counts');
  const invalid = structuredClone(previewDocument);
  invalid.dashboard.queries[0].subject = 'x'.repeat(513);
  const invalidDocument = JSON.stringify(invalid);
  let attempts = 0;
  await page.route('**/__query_designer', (route) => {
    attempts += 1;
    if (attempts > 1) {
      expect(route.request().postDataJSON()).toMatchObject({
        intent: 'Compare run conclusions', document: invalidDocument,
        feedback: expect.stringContaining('at most 512 characters'),
      });
    }
    return route.fulfill({ json: { document: attempts < acceptedAttempt ? invalidDocument : JSON.stringify(previewDocument) } });
  });
  await page.getByRole('button', { name: 'Generate query and view', exact: true }).click();
  await expect(page.locator('.query-editor > [role=status]')).toContainText('Preview updated');
  expect(attempts).toBe(acceptedAttempt);
  await expect(page.locator('.query-editor-preview svg').first()).toBeVisible();
  await expect(page.locator('.query-editor-errors')).toBeHidden();
  await expect(page.getByRole('button', { name: 'Save as custom view', exact: true })).toBeEnabled();
});
}

test('cancelling a validator repair stops further attempts and preserves authoring text', async ({ page }) => {
  await openEditor(page);
  const intent = page.getByRole('textbox', { name: 'Intent', exact: true });
  await intent.fill('Compare run conclusions');
  await page.getByRole('textbox', { name: 'Subject', exact: true }).fill('Workflow runs');
  await page.getByRole('textbox', { name: 'Acceptance criteria', exact: true }).fill('Show native conclusion counts');
  let attempts = 0;
  /** @type {{ route?: import('@playwright/test').Route }} */
  const pending = {};
  await page.route('**/__query_designer', (route) => {
    attempts += 1;
    if (attempts === 1) return route.fulfill({ json: { document: '[broken' } });
    pending.route = route;
  });
  await page.getByRole('button', { name: 'Generate query and view', exact: true }).click();
  await expect.poll(() => Boolean(pending.route)).toBe(true);
  await expect(page.locator('.query-editor > [role=status]')).toContainText('Correcting');
  await page.locator('.query-editor').getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.locator('.query-editor > [role=status]')).toContainText('cancelled');
  await expect(intent).toHaveValue('Compare run conclusions');
  await expect(page.getByRole('button', { name: 'Generate query and view', exact: true })).toBeEnabled();
  expect(attempts).toBe(2);
  await pending.route?.abort();
});

test('repairs a pre-bucketed temporal chart using the worker time-unit hint', async ({ page }) => {
  await openEditor(page);
  await page.getByRole('textbox', { name: 'Intent', exact: true }).fill('Show hourly workflow run counts');
  await page.getByRole('textbox', { name: 'Subject', exact: true }).fill('Hourly workflow runs');
  await page.getByRole('textbox', { name: 'Acceptance criteria', exact: true }).fill('Render an hourly temporal line chart');
  const temporal = {
    id: 'hourly-trend', title: 'Hourly workflow runs', mark: 'chart', chart: 'line',
    data: { source: 'hourly-runs', limit: 200 },
    encoding: { x: { field: 'date', type: 'temporal' }, y: { field: 'runs', type: 'quantitative' } }
  };
  const document = {
    ...previewDocument,
    dashboard: {
      ...previewDocument.dashboard,
      queries: [
        ...previewDocument.dashboard.queries,
        {
          name: 'hourly-runs', subject: 'Native run counts by UTC hour.', from: 'runs', limit: 200,
          compute: [{ as: 'date', function: 'date-bucket', args: [{ field: 'started-at' }, { value: 'hour' }] }],
          aggregate: { by: ['date'], values: [{ field: 'run', as: 'runs', reducer: 'count' }] }
        }
      ],
      pages: [{
        ...previewDocument.dashboard.pages[0],
        views: [...previewDocument.dashboard.pages[0].views, temporal]
      }]
    }
  };
  const invalidDocument = JSON.stringify(document);
  Object.assign(temporal.encoding.x, { 'time-unit': 'hour' });
  const repairedDocument = JSON.stringify(document);
  let attempts = 0;
  await page.route('**/__query_designer', (route) => {
    attempts += 1;
    if (attempts > 1) {
      expect(route.request().postDataJSON()).toMatchObject({
        document: invalidDocument,
        feedback: expect.stringContaining('add "time-unit": "day" inside encoding.x')
      });
    }
    return route.fulfill({ json: { document: attempts === 1 ? invalidDocument : repairedDocument } });
  });
  await page.getByRole('button', { name: 'Generate query and view', exact: true }).click();
  await expect(page.locator('.query-editor > [role=status]')).toContainText('Preview updated');
  expect(attempts).toBe(2);
  const preview = page.locator('.query-editor-preview');
  await expect(preview.getByRole('heading', { name: 'Hourly workflow runs', exact: true })).toBeVisible();
  await expect(preview.getByRole('region', { name: 'Hourly workflow runs', exact: true }).locator('svg').first()).toBeVisible();
  await expect(page.locator('.query-editor-errors')).toBeHidden();
});

test('checks the combined 512-character Unicode budget before generation and field improvement', async ({ page }) => {
  await openEditor(page);
  /** @type {Record<string, string>} */
  const authoring = {
    intent: 'Compare native run conclusions.',
    subject: '\u{1f600}'.repeat(170), objective: 'b'.repeat(170), acceptance: 'c'.repeat(172),
  };
  /** @type {Record<string, import('@playwright/test').Locator>} */
  const fields = {
    intent: page.getByRole('textbox', { name: 'Intent', exact: true }),
    subject: page.getByRole('textbox', { name: 'Subject', exact: true }),
    objective: page.getByRole('textbox', { name: 'Objective (optional)', exact: true }),
    acceptance: page.getByRole('textbox', { name: 'Acceptance criteria', exact: true }),
  };
  for (const [key, field] of Object.entries(fields)) await field.fill(authoring[key]);
  const limit = page.locator('.query-editor-semantic-limit');
  const generate = page.getByRole('button', { name: 'Generate query and view', exact: true });
  const improve = page.getByRole('button', { name: 'Improve all fields with Copilot', exact: true });
  await expect(limit).toContainText('512/512 characters');
  await expect(generate).toBeEnabled();
  await page.route('**/__query_designer', (route) => {
    const { subject, objective, acceptance } = route.request().postDataJSON();
    const document = structuredClone(previewDocument);
    Object.assign(document.dashboard.queries[0], { subject, objective, acceptance });
    Object.assign(document.dashboard.pages[0].views[0], { subject, objective, acceptance });
    return route.fulfill({ json: { document: JSON.stringify(document) } });
  });
  await generate.click();
  await expect(page.locator('.query-editor > [role=status]')).toContainText('Preview updated');
  await fields.acceptance.fill(`${authoring.acceptance}d`);
  await expect(limit).toContainText('513/512 characters');
  await expect(generate).toBeDisabled();
  await expect(improve).toBeEnabled();
  for (const key of ['subject', 'objective', 'acceptance']) {
    await expect(fields[key]).toHaveAttribute('aria-invalid', 'true');
    await expect(fields[key]).toHaveAttribute('aria-describedby', await limit.getAttribute('id') ?? '');
  }
  /** @type {Record<string, string>} */
  const improved = {
    intent: authoring.intent, subject: 'Retained runs', objective: 'Compare native conclusions', acceptance: 'Show conclusion counts',
  };
  await page.route('**/__query_designer/enhance', (route) => route.fulfill({ json: improved }));
  await improve.click();
  await expect(page.locator('.query-editor > [role=status]')).toContainText('All four fields improved');
  await expect(generate).toBeEnabled();
  await expect(fields.acceptance).toHaveAttribute('aria-invalid', 'false');
  await page.unroute('**/__query_designer/enhance');
  await page.route('**/__query_designer/enhance', (route) => route.fulfill({
    json: { ...authoring, acceptance: `${authoring.acceptance}d` },
  }));
  await improve.click();
  await expect(page.locator('.query-editor > [role=status]')).toContainText('exceeding 512 characters combined');
  for (const [key, field] of Object.entries(fields)) await expect(field).toHaveValue(improved[key]);
  await expect(generate).toBeEnabled();
});

test('preview subscriptions refresh from Go canonical revisions and query output stays bounded', async ({ page }) => {
  await openEditor(page);
  await page.getByRole('textbox', { name: 'Intent', exact: true }).fill('Compare run conclusions');
  await page.getByRole('textbox', { name: 'Subject', exact: true }).fill('Workflow runs');
  await page.getByRole('textbox', { name: 'Acceptance criteria', exact: true }).fill('Show native conclusion counts');
  await page.getByRole('button', { name: 'Generate query and view', exact: true }).click();
  await expect(page.locator('.query-editor > [role=status]')).toContainText('Preview updated');
  await ingestDashboardSources(createSqliteIndexedDB(join(artifactDirectory, 'gh-aw-logs.sqlite')), {
      ...sources,
      runs: {
        metadata: { 'as-of': '2026-10-10T11:00:00Z', 'artifact-generation': 'query-editor-refresh' },
        rows: Array.from({ length: 251 }, (_, index) => ({
          organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/example.md',
          run: String(1000 + index), 'run-attempt': 1, 'run-status': 'completed', 'run-conclusion': 'failure',
          'started-at': '2026-10-10T10:00:00Z'
        }))
      }
  });
  const outputCounts = await page.evaluate(async () => {
    const { loadCanonicalDashboardPage, validateQueryEditorDocument } =
      await import(new URL('./src/data-processor.js', location.href).href);
    const result = await validateQueryEditorDocument(JSON.stringify({
      'language-version': '0.1.0',
      dashboard: {
        id: 'bounded-runs', title: 'Bounded runs',
        queries: [{ name: 'bounded-runs', subject: 'Retained workflow run summaries', from: 'runs', limit: 200 }],
        pages: [{
          id: 'query-preview', kind: 'custom', title: 'Bounded runs',
          views: [{
            id: 'bounded', mark: 'table', data: { source: 'bounded-runs', limit: 200 },
            encoding: { columns: [{ field: 'run', type: 'nominal' }] }
          }]
        }]
      }
    }));
    if (!result.ok) throw new Error(JSON.stringify(result.errors));
    const tables = await loadCanonicalDashboardPage(result.sourceNames, {
      pages: result.document.dashboard.pages, queries: result.document.dashboard.queries
    }, undefined, { pageId: result.pageId, signal: new AbortController().signal });
    return Object.values(tables).map((source) => source.rows.length);
  });
  expect(outputCounts.length).toBeGreaterThan(0);
  expect(outputCounts.every((count) => count === 200)).toBe(true);
  await expect(page.locator('.query-editor-preview .chart-legend-pie')).toContainText('252');
});

test('intent improvement preserves edits during an in-flight request and cancels on navigation', async ({ page }) => {
  await openEditor(page);
  const intent = page.getByRole('textbox', { name: 'Intent', exact: true });
  await intent.fill('Original intent');
  /** @type {{ route?: import('@playwright/test').Route }} */
  const pending = {};
  const pendingRoute = () => pending.route;
  await page.route('**/__query_designer/enhance', (route) => { pending.route = route; });
  await page.getByRole('button', { name: 'Improve all fields with Copilot', exact: true }).click();
  await expect.poll(() => Boolean(pending.route)).toBe(true);
  await intent.fill('New intent');
  await pending.route?.fulfill({ json: { intent: 'Obsolete enhancement', subject: 'Runs', objective: 'Investigate', acceptance: 'Native conclusions' } });
  await expect(page.locator('.query-editor > [role=status]')).toContainText('not applied');
  await expect(intent).toHaveValue('New intent');
  delete pending.route;
  await page.getByRole('button', { name: 'Improve all fields with Copilot', exact: true }).click();
  await expect.poll(() => Boolean(pending.route)).toBe(true);
  await page.locator('.query-editor').getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.locator('.query-editor > [role=status]')).toContainText('cancelled');
  await expect(intent).toHaveValue('New intent');
  await pendingRoute()?.abort();
  const response = await page.request.get(`${server.url}/dashboard.json`);
  const dashboard = await response.json();
  const other = dashboard.dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id !== 'query-editor');
  await page.evaluate((id) => { location.hash = `#page-${id}`; }, other.id);
  await expect(page.locator('.query-editor')).toHaveCount(0);
});
});
