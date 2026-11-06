import { createServer } from 'node:http';
import { access, mkdir, mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises';
import { extname, join, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { gzipSync } from 'node:zlib';
import { chromium } from '@playwright/test';
import lighthouse from 'lighthouse';
import desktopConfig from 'lighthouse/core/config/desktop-config.js';
import puppeteer from 'puppeteer-core';
import {
  scrollRenderedViewsIntoView
} from '../../../../tests/e2e/dashboard-deployed-refresh-helpers.mjs';
import {
  visibleBusyViewSelector
} from '../../../../tests/e2e/dashboard-view-assessment.mjs';
import {
  canonicalCounts, instrumentPerformancePage, preparePerformanceData
} from './lighthouse-support.mjs';
import { assertCanonicalCounts, median, performanceFailures } from './lighthouse-contract.js';

const sourceRoot = resolve(fileURLToPath(new URL('../..', import.meta.url)));
const siteRoot = resolve(process.env.DASHBOARD_PERFORMANCE_SITE_ROOT || join(sourceRoot, 'dist'));
const outputRoot = resolve(process.env.DASHBOARD_PERFORMANCE_OUTPUT_DIR || join(sourceRoot, 'test-results/lighthouse'));
const temporaryRoot = join(sourceRoot, '.tmp');
const fullRoot = resolve(process.env.DASHBOARD_PERFORMANCE_DATA_ROOT || join(temporaryRoot, 'lighthouse-data/full'));
const emptyRoot = join(temporaryRoot, 'lighthouse-data/empty');
const dataUrl = process.env.DASHBOARD_DATA_URL || 'https://githubnext.github.io/gh-cao/cao/payload-hashes.json';
const repeats = Number(process.env.DASHBOARD_PERFORMANCE_REPEATS || 3);
const networkModes = (process.env.DASHBOARD_PERFORMANCE_NETWORK_MODES || 'cold,pwa').split(',');
const threshold = 0.88;
const defaultBudgets = {
  'cumulative-layout-shift': 0.1,
  'first-contentful-paint': 1800,
  'largest-contentful-paint': 2500,
  'speed-index': 3400,
  'total-blocking-time': 200
};
const scenarios = [
  { id: 'cfo', persona: 'Chief Financial Officer', question: 'Where is AI Credit usage concentrated?', routes: ['cost', 'campaigns', 'repositories'], budgets: { 'cumulative-layout-shift': 0.2 } },
  { id: 'cto', persona: 'Chief Technology Officer', question: 'Which automation bottleneck threatens reliability?', routes: ['overview', 'runs', 'workflows'] },
  { id: 'cso', persona: 'Chief Security Officer', question: 'Which assurance gap needs attention?', routes: ['firewall', 'overview-security-findings', 'mcps'] }
];
const mime = {
  '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8',
  '.json': 'application/json', '.jsonl': 'application/x-ndjson',
  '.svg': 'image/svg+xml', '.png': 'image/png', '.webmanifest': 'application/manifest+json',
  '.txt': 'text/plain; charset=utf-8'
};

async function startServer(retentionAsOf) {
  const compressed = new Map();
  const server = createServer(async (request, response) => {
    try {
      const url = new URL(request.url || '/', 'http://127.0.0.1');
      const [, state, ...segments] = decodeURIComponent(url.pathname).split('/');
      if (!['empty', 'full'].includes(state)) { response.writeHead(404).end(); return; }
      const relative = segments.join('/') || 'index.html';
      const isData = relative === 'payload-hashes.json' || relative === 'inventory-sources.json'
        || relative.startsWith('gh-aw-logs-')
        || relative.startsWith('memory/') && relative !== 'memory/index.html';
      const root = isData ? state === 'full' ? fullRoot : emptyRoot : siteRoot;
      const path = resolve(root, relative);
      if (!path.startsWith(`${root}${sep}`)) { response.writeHead(403).end(); return; }
      const details = await stat(path);
      if (!details.isFile()) { response.writeHead(404).end(); return; }
      // Measure the published snapshot at its evidence time, not its download time.
      // Keep retention cutoffs stable across the long repeated-navigation audit.
      const prefix = relative === 'src/data-worker.js'
        ? Buffer.from(`Date.now = () => ${retentionAsOf};\n`) : null;
      const readBody = async () => {
        const body = await readFile(path);
        return prefix ? Buffer.concat([prefix, body]) : body;
      };
      const headers = {
        'content-type': mime[extname(path)] || 'application/octet-stream',
        'cache-control': 'no-store', 'content-length': details.size + (prefix?.length ?? 0), vary: 'Accept-Encoding'
      };
      if (request.method === 'HEAD') { response.writeHead(200, headers).end(); return; }
      let body;
      if (/\bgzip\b/.test(request.headers['accept-encoding'] || '')
          && ['.js', '.css', '.json', '.jsonl', '.svg', '.html'].includes(extname(path))) {
        body = compressed.get(path);
        if (!body) { body = gzipSync(await readBody()); compressed.set(path, body); }
        headers['content-encoding'] = 'gzip';
      } else {
        body = await readBody();
      }
      headers['content-length'] = body.length;
      response.writeHead(200, headers).end(body);
    } catch (error) {
      if (error.code === 'ENOENT') response.writeHead(404).end();
      else { console.error(error); response.writeHead(500).end(); }
    }
  });
  await new Promise((resolvePromise, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolvePromise);
  });
  const { port } = server.address();
  return { server, origin: `http://127.0.0.1:${port}`, retentionAsOf };
}

async function unusedPort() {
  const server = createServer();
  await new Promise((resolvePromise, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolvePromise);
  });
  const { port } = server.address();
  await new Promise((resolvePromise) => server.close(resolvePromise));
  return port;
}

const routeUrl = (origin, state, route, eager = false) => (
  `${origin}/${state}/?online=1${eager ? '&debug-eager-ingest=1' : ''}#page-${route}`
);

async function ready(page, route, state) {
  const active = page.locator(`[data-page-id="${route}"]`);
  await active.waitFor({ state: 'visible', timeout: 180_000 });
  await page.waitForFunction((selector) => {
    const active = document.querySelector(selector);
    return active && (active.querySelector('[data-view-id]')
      || active.getAttribute('data-page-prerequisite-unavailable') === 'true');
  }, `[data-page-id="${route}"]`, { timeout: 180_000 });
  if (state === 'empty' && await active.getAttribute('data-page-prerequisite-unavailable') === 'true') {
    await active.locator('[data-view-state="unavailable"]').waitFor({ state: 'visible' });
    return;
  }
  await active.locator('[data-view-id]').first().waitFor({ state: 'attached' });
  await scrollRenderedViewsIntoView(active);
  await page.waitForFunction((selector) => {
    const active = document.querySelector(selector);
    return active && active.getAttribute('aria-busy') !== 'true';
  }, `[data-page-id="${route}"]`, { timeout: 180_000 });
  await active.locator(visibleBusyViewSelector).first().waitFor({ state: 'hidden', timeout: 180_000 });
  const unavailable = await active.locator('[data-view-state="unavailable"]:visible').allTextContents();
  if (unavailable.length) throw new Error(`${route} rendered unavailable data: ${unavailable.join('; ')}`);
  await page.evaluate(() => {
    const main = document.querySelector('main.dashboard-prototype');
    if (main) main.scrollTop = 0;
    window.scrollTo(0, 0);
  });
}

async function audit(context, browser, origin, state, profile, network, scenario, directory, port) {
  const budgets = { ...defaultBudgets, ...scenario.budgets };
  const samples = [];
  for (let repeat = 1; repeat <= repeats; repeat++) {
    // Preserve canonical storage and the service worker, not prior audits' renderer state.
    const [page, auditPage] = await Promise.all([
      context.waitForEvent('page'),
      browser.newPage()
    ]);
    try {
      const errors = [];
      page.on('pageerror', (error) => errors.push(error.message));
      const session = await page.context().newCDPSession(page);
      try { await session.send('Network.clearBrowserCache'); } finally { await session.detach(); }
      await auditPage.setBypassServiceWorker(network === 'cold');
      const result = await lighthouse(routeUrl(origin, state, scenario.routes[0]), {
        port, logLevel: 'error', output: ['json', 'html'], disableStorageReset: true,
        onlyCategories: ['performance', 'accessibility', 'best-practices', 'seo'],
        maxWaitForLoad: 180_000,
        ...(profile === 'mobile' ? {
          formFactor: 'mobile',
          screenEmulation: { mobile: true, width: 390, height: 844, deviceScaleFactor: 3, disabled: false }
        } : {})
      }, profile === 'desktop' ? desktopConfig : undefined, auditPage);
      if (!result || result.lhr.runtimeError) throw new Error(`Lighthouse failed: ${JSON.stringify(result?.lhr.runtimeError)}`);
      if (result.lhr.configSettings.formFactor !== profile) throw new Error('Lighthouse used the wrong device profile.');
      if (network === 'cold' && !result.lhr.audits['network-requests'].details.items.some((item) => (
        item.url.includes('/src/main.js') && item.transferSize > 0
      ))) throw new Error('The cold audit did not measure an uncached application transfer.');
      await ready(page, scenario.routes[0], state);
      if (errors.length) throw new Error(`${state} audit page errors: ${errors.join('; ')}`);
      const repeatRoot = join(directory, String(repeat));
      await mkdir(repeatRoot, { recursive: true });
      await Promise.all([
        writeFile(join(repeatRoot, 'lighthouse.report.json'), JSON.stringify(result.lhr, null, 2)),
        writeFile(join(repeatRoot, 'lighthouse.report.html'), result.report[1]),
        writeFile(join(repeatRoot, 'trace.json'), JSON.stringify(result.artifacts.Trace)),
        writeFile(join(repeatRoot, 'devtoolslog.json'), JSON.stringify(result.artifacts.DevtoolsLog))
      ]);
      samples.push({
        scores: Object.fromEntries(Object.entries(result.lhr.categories).map(([name, category]) => [name, category.score])),
        metrics: Object.fromEntries(Object.keys(budgets).map((name) => [name, result.lhr.audits[name].numericValue]))
      });
    } finally {
      await page.close();
    }
  }
  const metrics = Object.fromEntries(Object.keys(budgets).map((name) => [name, median(samples.map((sample) => sample.metrics[name]))]));
  const score = median(samples.map((sample) => sample.scores.performance));
  return {
    ...scenario, id: `${scenario.id}-${state}-${profile}-${network}`, state, profile, network,
    score, passingScore: threshold, aspirationalScore: 1, samples, metrics, budgets,
    failures: performanceFailures(score, metrics, threshold, budgets)
  };
}

async function main() {
  if (!Number.isSafeInteger(repeats) || repeats < 1) throw new Error('DASHBOARD_PERFORMANCE_REPEATS must be a positive integer.');
  if (!networkModes.length || new Set(networkModes).size !== networkModes.length
      || networkModes.some((mode) => !['cold', 'pwa'].includes(mode))) throw new Error('Invalid performance network modes.');
  await access(join(siteRoot, 'index.html'));
  await mkdir(outputRoot, { recursive: true });
  await mkdir(temporaryRoot, { recursive: true });
  const dataset = await preparePerformanceData(fullRoot, emptyRoot, dataUrl, Boolean(process.env.DASHBOARD_PERFORMANCE_DATA_ROOT));
  await writeFile(join(outputRoot, 'dataset.json'), JSON.stringify(dataset, null, 2));
  const { server, origin, retentionAsOf } = await startServer(dataset.retentionAsOf);
  const results = [];
  const states = {};
  const writeSummary = (status, error) => writeFile(join(outputRoot, 'summary.json'), JSON.stringify({
    generatedAt: new Date().toISOString(), status, ...(error ? { error } : {}),
    methodology: 'Gzip production site; verified empty and fully ingested canonical databases; repeated desktop/mobile medians; cold HTTP with service-worker bypass and separate PWA navigation.',
    retentionAsOf: new Date(retentionAsOf).toISOString(),
    dataset, states, results
  }, null, 2));
  try {
    for (const state of ['empty', 'full']) {
      const profileRoot = await mkdtemp(join(temporaryRoot, 'lighthouse-profile-'));
      const port = await unusedPort();
      let context;
      let browser;
      try {
        context = await chromium.launchPersistentContext(profileRoot, {
          headless: true, viewport: { width: 1440, height: 900 },
          executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || chromium.executablePath(),
          args: [`--remote-debugging-port=${port}`, '--no-sandbox', '--disable-dev-shm-usage']
        });
        browser = await puppeteer.connect({ browserURL: `http://127.0.0.1:${port}` });
        const page = context.pages()[0] || await context.newPage();
        await page.addInitScript(instrumentPerformancePage);
        const errors = [];
        page.on('pageerror', (error) => errors.push(error.message));
        const started = performance.now();
        await page.goto(routeUrl(origin, state, 'overview', true), { waitUntil: 'domcontentloaded' });
        await page.waitForFunction(() => ['completed', 'failed'].includes(
          Reflect.get(window, '__dashboardPerformanceRefresh')?.status
        ), null, { timeout: 900_000 });
        const refresh = await page.evaluate(() => Reflect.get(window, '__dashboardPerformanceRefresh'));
        if (refresh.status !== 'completed') throw new Error(`${state} ingestion failed: ${JSON.stringify(refresh)}`);
        const counts = await canonicalCounts(page);
        states[state] = { ingestionMs: performance.now() - started, counts, refresh, journeys: [] };
        assertCanonicalCounts(state, counts, state === 'full' ? dataset.shards : 2);
        await page.evaluate(() => navigator.serviceWorker.ready);
        for (const profile of ['desktop', 'mobile']) {
          await page.setViewportSize(profile === 'desktop' ? { width: 1440, height: 900 } : { width: 390, height: 844 });
          for (const scenario of scenarios) {
            const directory = join(outputRoot, scenario.id, state, profile);
            await mkdir(directory, { recursive: true });
            await context.tracing.start({ screenshots: true, snapshots: true });
            try {
              await page.goto(routeUrl(origin, state, scenario.routes[0]), { waitUntil: 'domcontentloaded' });
              for (const route of scenario.routes) {
                const started = performance.now();
                await page.evaluate((route) => { location.hash = `page-${route}`; }, route);
                await ready(page, route, state);
                states[state].journeys.push({ profile, route, durationMs: performance.now() - started });
                const table = page.locator(`[data-page-id="${route}"] [data-view-mode-value="table"]`);
                if (await table.isVisible()) { await table.click(); await ready(page, route, state); }
              }
            } finally {
              await context.tracing.stop({ path: join(directory, 'playwright-trace.zip') });
            }
            await page.goto(routeUrl(origin, state, scenario.routes[0]), { waitUntil: 'domcontentloaded' });
            await ready(page, scenario.routes[0], state);
            await page.screenshot({ path: join(directory, 'dashboard.png'), fullPage: true });
            for (const network of networkModes) {
              const result = await audit(context, browser, origin, state, profile, network, scenario, join(directory, network), port);
              results.push(result);
              await writeSummary('running');
              console.log(`${result.id}: performance ${Math.round(result.score * 100)}, CLS ${result.metrics['cumulative-layout-shift'].toFixed(3)}`);
            }
          }
        }
        const after = await canonicalCounts(page);
        if (JSON.stringify(after) !== JSON.stringify(counts)) {
          const changed = Object.keys(counts).filter((store) => counts[store] !== after[store]);
          throw new Error(`${state} canonical counts changed during the audits: ${
            changed.map((store) => `${store} ${counts[store]} -> ${after[store]}`).join(', ')
          }.`);
        }
        if (errors.length) throw new Error(`${state} page errors: ${errors.join('; ')}`);
      } catch (error) {
        await writeSummary('failed', error instanceof Error ? error.message : String(error));
        throw error;
      } finally {
        await browser?.disconnect();
        await context?.close();
        await rm(profileRoot, { recursive: true, force: true });
      }
    }
  } finally {
    await new Promise((resolvePromise, reject) => server.close((error) => error ? reject(error) : resolvePromise()));
  }
  await writeSummary('complete');
  const failures = results.flatMap((result) => result.failures.map((failure) => `${result.id}: ${failure}`));
  for (const failure of failures) console.error(`::error title=Lighthouse budget exceeded::${failure}`);
  console.log(`Performance evidence: ${outputRoot}`);
  return failures.length ? 42 : 0;
}

try {
  process.exitCode = await main();
} catch (error) {
  console.error(error instanceof Error ? error.stack : error);
  process.exitCode = 1;
}
