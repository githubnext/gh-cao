import { test, expect } from '@playwright/test';
import { cp, mkdtemp, rm } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { join, relative, sep } from 'node:path';
import { parseUnifiedSessionDocument } from '../../src/data/adapters/unified-session.js';
import { normalizedActivityShards } from './normalized-shard.js';
import { originalSessions, replay, runCacheLine } from '../model-resolution-fixture.js';
import { writeCanvasArtifact } from './canvas-fixture.js';
import routingContract from '../fixtures/model-routing-contract.json' with { type: 'json' };

/**
 * Serves a copy of the site with the supplied normalized activity shards.
 * @param {{ runs: string, records: string }} shards @param {string} timestamp
 * @param {string} [repository]
 */
async function startRoutingServer(shards, timestamp, repository = 'octo/api') {
  const [organization, name] = repository.split('/');
  const { startCanvasServer } = await import(new URL('../../../canvas-server.mjs', import.meta.url).href);
  const workspace = await mkdtemp(fileURLToPath(new URL('../../../../.cao-routing-e2e-', import.meta.url)));
  const siteRoot = fileURLToPath(new URL('../../', import.meta.url));
  await cp(siteRoot, join(workspace, 'site'), {
    recursive: true,
    filter: (path) => !['node_modules', 'dist', '.tmp', 'test', 'test-results', 'scripts'].includes(relative(siteRoot, path).split(sep)[0])
  });
  /** @type {{ url: string, close: () => Promise<void> }} */
  let server;
  try {
    server = await startCanvasServer({
      workingDirectory: workspace, siteRoot: join(workspace, 'site'), catalogRoot: null,
      port: 0,
      output: () => {}, requestOutput: () => {}, traceOutput: () => {},
      executeCliAction: async () => { throw new Error('CLI actions are disabled in routing browser tests.'); },
      approveCliAction: async () => false,
      downloadData: async (/** @type {string} */ destination) => {
        await writeCanvasArtifact(destination, { sources: {
          repositories: { rows: [{ organization, repository: name, 'observed-at': timestamp }],
            metadata: { 'as-of': timestamp, 'artifact-generation': 'routing-e2e' } }
        }, shards: [shards.runs, shards.records] });
      }
    });
  } catch (error) {
    await rm(workspace, { recursive: true, force: true });
    throw error;
  }
  return { server, workspace };
}

const ROUTING_VIEWS = ['routing-health', 'routing-models', 'routing-cost-trend', 'routing-problems', 'routing-runs'];
const ROUTING_TITLES = ['Routing health by workflow', 'Models picked over time', 'Cost per run over time', 'Problems', 'Routing runs'];

/** @param {import('@playwright/test').Page} page */
async function expectFiveViews(page) {
  const views = page.locator('.dashboard-page:not([hidden]) [data-view-id^="routing-"]');
  await expect.poll(() => views.evaluateAll((elements) => elements.map((element) => element.getAttribute('data-view-id')))).toEqual(ROUTING_VIEWS);
  for (const [index, title] of ROUTING_TITLES.entries()) {
    await expect(views.nth(index)).toContainText(title);
  }
  await expect(page.locator('.dashboard-page:not([hidden]) [data-disclosure="supplemental"]')).toHaveCount(0);
}

/** @param {import('@playwright/test').Page} page @param {number} width */
async function showTables(page, width) {
  const activePage = page.locator('.dashboard-page:not([hidden])');
  await expect(async () => {
    if (width < 768) {
      for (let step = 0; step < 2; step += 1) {
        if (await activePage.getAttribute('data-view-mode') === 'table') break;
        await page.locator('.mobile-view-mode-toggle').click();
      }
    } else await page.getByRole('button', { name: 'Table', exact: true }).click();
    await expect(activePage).toHaveAttribute('data-view-mode', 'table', { timeout: 1000 });
  }).toPass();
}

/**
 * Runs table-mode assertions, reselecting table mode if a data publication re-renders the page.
 * @param {import('@playwright/test').Page} page @param {number} width @param {() => Promise<void>} check
 */
async function inTables(page, width, check) {
  await expect(async () => {
    await showTables(page, width);
    await check();
  }).toPass({ timeout: 20000 });
}

  test.describe('Go SQLite model routing', () => {
    /** @type {{ url: string, close: () => Promise<void> }} */
    let server;
    let workspace = '';

    test.beforeAll(async () => {
      const now = Date.now();
      const timestamp = new Date(now).toISOString();
      /** @param {string} type @param {Record<string, unknown>} data @param {string} [phase] */
      const event = (type, data, phase = 'agent') => ({ type, data, provenance: { phase, timestampMs: now, component: 'awf' } });
      const document = parseUnifiedSessionDocument([
        { type: 'session.format', data: { version: 1 } },
        event('workflow.info', { repository: 'octo/api', runId: 101, engineId: 'copilot' }),
        event('model.config', { routing: { enabled: true } }),
        event('firewall.model_routing', { stage: 'selection', selectedModel: 'gpt-5', selectedEffort: 'high',
          objective: { goal: 'cost', mode: 'auto' }, mode: 'balanced',
          labels: { task_type: 'coding', scope: 'repository', task_complexity: 'high' } }),
        event('firewall.model_routing', { stage: 'request', requestId: 'request-1', requestedModel: 'gpt-5',
          selectedModel: 'gpt-5', routed: 'as_selected', outcome: 'success' }),
        event('firewall.token_usage', { requestId: 'request-1', model: 'gpt-5', aic: 4 }),
        event('workflow.job_result', { jobId: 'agent', result: 'success' }),
        event('session.collection', { sources: [{ path: 'firewall/agent/token-usage.jsonl', phase: 'agent', component: 'firewall', events: 1 }] }, 'conclusion')
      ].map((row) => JSON.stringify(row)).join('\n'), 'usage/aw_session.jsonl', { runId: '101', attempt: 1 });
      const shards = normalizedActivityShards(JSON.stringify({
        schema_version: 5, kind: 'run',
        run: { run_id: 101, run_attempt: 1, organization: 'octo', repository: 'octo/api',
          workflow_name: 'Routing worker', workflow_path: '.github/workflows/worker.lock.yml',
          created_at: timestamp, updated_at: timestamp, status: 'completed', conclusion: 'success',
          cao_unified_session: { version: 1, path: 'usage/aw_session.jsonl', ...document } }
      }));
      ({ server, workspace } = await startRoutingServer(shards, timestamp));
    });
    test.afterAll(async () => {
      await server?.close();
      if (workspace) await rm(workspace, { recursive: true, force: true });
    });

    for (const width of [1280, 390]) {
      test(`renders the real paired SDK baseline at ${width}px`, async ({ page }, testInfo) => {
        const fixture = routingContract['observed-sessions'][0];
        const sessionDocument = parseUnifiedSessionDocument(fixture.records.map((record) => JSON.stringify(record)).join('\n'),
          'usage/aw_session.jsonl', { runId: fixture.run, attempt: 1 });
        const timestamp = new Date().toISOString();
        const shards = normalizedActivityShards(JSON.stringify({
          schema_version: 5, kind: 'run', run: {
            run_id: Number(fixture.run), run_attempt: 1, organization: 'github', repository: 'github/gh-aw',
            workflow_name: 'Smoke Copilot SDK Routed', workflow_path: '.github/workflows/smoke-copilot-sdk-routed.lock.yml',
            created_at: timestamp, updated_at: timestamp, status: 'completed', conclusion: 'success',
            cao_unified_session: { version: 1, path: 'usage/aw_session.jsonl', ...sessionDocument }
          }
        }));
        const real = await startRoutingServer(shards, timestamp, 'github/gh-aw');
        try {
          await page.setViewportSize({ width, height: 900 });
          await page.goto(`${real.server.url}/?local-preview=canvas#page-model-routing`);
          await expect(page.getByRole('dialog', { name: 'Preparing your dashboard' })).toBeHidden();
          await expectFiveViews(page);
          const health = page.locator('[data-view-id="routing-health"]');
          await inTables(page, width, async () => {
            await expect(health).toContainText('claude-sonnet-5.5');
            await expect(health).toContainText('11.2%');
          });
          await page.getByRole('slider', { name: 'Minimum complete runs per group' }).press('Home');
          await inTables(page, width, async () => {
            await expect(page.locator('[data-view-id="routing-cost-trend"]'))
              .toContainText('Baseline estimate (estimated, main requests): claude-sonnet-5.5');
          });
          const screenshot = testInfo.outputPath(`routing-baseline-go-${width}.png`);
          await page.screenshot({ path: screenshot, fullPage: true });
          await testInfo.attach(`routing-baseline-go-${width}`, { path: screenshot, contentType: 'image/png' });
          await inTables(page, width, () => page.locator('[data-view-id="routing-runs"] a[href*="routing-run="]:visible').first().click());
          await expect(page.locator('[data-view-id="routing-run-cost"]')).toContainText('claude-sonnet-5.5');
          await expect(page.locator('[data-view-id="routing-run-cost"]')).toContainText('Baseline estimate (estimated)');
          expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
          const detailScreenshot = testInfo.outputPath(`routing-baseline-detail-go-${width}.png`);
          await page.screenshot({ path: detailScreenshot, fullPage: true });
          await testInfo.attach(`routing-baseline-detail-go-${width}`, { path: detailScreenshot, contentType: 'image/png' });
        } finally {
          await real.server.close();
          await rm(real.workspace, { recursive: true, force: true });
        }
      });

      test(`renders populated routing and drill-down at ${width}px`, async ({ page }, testInfo) => {
        await page.setViewportSize({ width, height: 900 });
        await page.goto(`${server.url}/?local-preview=canvas#page-model-routing`);
        await expect(page.getByRole('dialog', { name: 'Preparing your dashboard' })).toBeHidden();
        await expectFiveViews(page);
        await expect(page.getByRole('heading', { name: 'Models picked over time', exact: true })).toBeVisible();
        await expect(page.getByRole('heading', { name: 'Cost per run over time', exact: true })).toBeVisible();
        const runs = page.locator('[data-view-id="routing-runs"]');
        await expect(runs).toContainText('selected');
        await expect(runs).toContainText('gpt-5');
        const screenshot = testInfo.outputPath(`model-routing-go-${width}.png`);
        await page.screenshot({ path: screenshot, fullPage: true });
        await testInfo.attach(`model-routing-go-${width}`, { path: screenshot, contentType: 'image/png' });
        const state = page.getByRole('textbox', { name: 'Routing state', exact: true });
        await state.fill('unrouted');
        await expect(runs).toContainText('No identified routing sessions');
        await state.fill('');
        await expect(runs).toContainText('selected');
        const health = page.locator('[data-view-id="routing-health"]');
        await inTables(page, width, async () => {
          await expect(health).toBeVisible({ timeout: 1000 });
          await expect(health).toContainText('octo/api', { timeout: 1000 });
          await expect(health).toContainText('.github/workflows/worker.md', { timeout: 1000 });
          await expect(health).toContainText('100%', { timeout: 1000 });
          await expect(health).toContainText('0%', { timeout: 1000 });
          await expect(page.locator('[data-view-id="routing-problems"]'))
            .toContainText('No routing failures, deviations or resolution failures were recorded', { timeout: 1000 });
        });
        const tableScreenshot = testInfo.outputPath(`model-routing-go-${width}-table.png`);
        await page.screenshot({ path: tableScreenshot, fullPage: true });
        await testInfo.attach(`model-routing-go-${width}-table`, { path: tableScreenshot, contentType: 'image/png' });
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
        await inTables(page, width, () => runs.locator('a[href*="routing-run="]:visible').first().click({ timeout: 1000 }));
        await expect(page.getByRole('heading', { name: 'Agent-phase accounting coverage', exact: true })).toBeVisible();
        await expect(page.locator('[data-view-id="routing-run-attribution"]')).toContainText('main_agent');
        await expect(page.locator('[data-view-id="routing-run-requests"]')).toContainText('as_selected');
        await expect(page.locator('[data-view-id="routing-run-timeline"]')).toContainText('firewall.model_routing');
        await page.getByRole('textbox', { name: 'Routing state', exact: true }).fill('unrouted');
        await expect(page.locator('[data-view-id="routing-run-attribution"]')).toContainText('No per-response accounting evidence');
        await page.getByRole('textbox', { name: 'Routing state', exact: true }).fill('');
        await expect(page.locator('[data-view-id="routing-run-attribution"]')).toContainText('main_agent');
        await page.evaluate(() => { location.hash = '#page-routing-run?routing-run=octo%2Fapi%3A999'; });
        await expect(page.locator('[data-view-id="routing-run-cost"]')).toContainText('no retained verified workflow identity');
        await expect(page.locator('[data-view-id="routing-run-attribution"]')).toContainText('No per-response accounting evidence');
        await page.evaluate(() => { location.hash = '#page-routing-run?routing-run=octo%2Fapi%3A101'; });
        await expect(page.locator('[data-view-id="routing-run-attribution"]')).toContainText('main_agent');
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
      });
    }
  });

  test.describe('Go SQLite model resolution', () => {
    /** @type {{ url: string, close: () => Promise<void> }} */
    let server;
    let workspace = '';

    test.beforeAll(async () => {
      const now = Date.now();
      // Minimized real smoke sessions plus documented controlled replays (alias move and failures).
      const sessions = [...originalSessions, replay('opus-before'), replay('opus-after'),
        replay('pi-main-model-unavailable'), replay('pi-main-catalog-unavailable')];
      const shards = normalizedActivityShards(sessions.map((source) => runCacheLine(source, now)).join('\n'));
      ({ server, workspace } = await startRoutingServer(shards, new Date(now).toISOString(), 'github/gh-aw'));
    });
    test.afterAll(async () => {
      await server?.close();
      if (workspace) await rm(workspace, { recursive: true, force: true });
    });

    for (const width of [1280, 390]) {
      test(`renders resolution Problems and run resolution drill-down at ${width}px`, async ({ page }, testInfo) => {
        await page.setViewportSize({ width, height: 900 });
        await page.goto(`${server.url}/?local-preview=canvas#page-model-routing`);
        await expect(page.getByRole('dialog', { name: 'Preparing your dashboard' })).toBeHidden();
        await expectFiveViews(page);
        const problems = page.locator('[data-view-id="routing-problems"]');
        await inTables(page, width, async () => {
          for (const [run, cause] of [['990000003', 'model_unavailable'], ['990000004', 'catalog_unavailable']]) {
            await expect(problems.locator(`a[href*="routing-run=github%2Fgh-aw%3A${run}"]:visible`).first()).toBeVisible({ timeout: 1000 });
            await expect(problems).toContainText(run, { timeout: 1000 });
            await expect(problems).toContainText(cause, { timeout: 1000 });
          }
          await expect(problems).toContainText('resolution failure', { timeout: 1000 });
          await expect(problems).toContainText('.github/workflows/smoke-pi-sub-agents.md', { timeout: 1000 });
        });
        const screenshot = testInfo.outputPath(`model-resolution-go-${width}.png`);
        await page.screenshot({ path: screenshot, fullPage: true });
        await testInfo.attach(`model-resolution-go-${width}`, { path: screenshot, contentType: 'image/png' });
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);

        const engine = page.getByRole('textbox', { name: 'Engine', exact: true });
        await engine.fill('codex');
        await expect(problems).toContainText('No routing failures, deviations or resolution failures were recorded');
        await engine.fill('');
        await expect(problems).toContainText('model_unavailable');

        await inTables(page, width, () => problems.locator('a[href*="routing-run=github%2Fgh-aw%3A990000003"]:visible').first().click({ timeout: 1000 }));
        await expect(page).toHaveURL(/#page-routing-run\?routing-run=github%2Fgh-aw%3A990000003$/);
        const disclosure = page.locator('details', { has: page.locator('[data-view-id="routing-run-resolutions"]') });
        if (await disclosure.getAttribute('open') === null) await disclosure.locator('summary').first().press('Enter');
        const drill = page.locator('[data-view-id="routing-run-resolutions"]');
        await expect(drill).toContainText('copilot/gpt-5.3 → unknown');
        await expect(drill).toContainText('model_unavailable');
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
      });
    }
  });
