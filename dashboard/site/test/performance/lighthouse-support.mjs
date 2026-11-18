import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { enterpriseDemo, enterpriseSample } from '../../src/data/demo/sample.mjs';
import { CANONICAL_SCHEMA_VERSION } from '../../src/data/model/schema.js';
import { NORMALIZED_JSONL_INGESTION_VERSION } from '../../src/data/ingest/coordinator.js';

const execute = promisify(execFile);
const repositoryRoot = fileURLToPath(new URL('../../../../', import.meta.url));
const builderUrl = new URL('../../../../scripts/simulate-dashboard-pages.mjs', import.meta.url).href;
/** @param {string | Buffer} body */
const digest = (body) => createHash('sha256').update(body).digest('hex');

/** @param {{ siteRoot?: string, emptyRoot?: string, now?: number } & Record<string, unknown>} [options] */
export async function preparePerformanceData({ siteRoot, emptyRoot, now = Date.now(), ...unsupported } = {}) {
  if (Object.keys(unsupported).length) throw new Error('Lighthouse accepts synthetic demo preparation options only.');
  if (typeof siteRoot !== 'string' || typeof emptyRoot !== 'string') throw new Error('Lighthouse output directories are required.');
  const { stdout } = await execute(process.execPath, ['--input-type=module', '--eval', `
    const { prepareDashboardPages, assertSafePagesOutput } = await import(process.argv[1]);
    const { output, emptyRoot, now } = JSON.parse(process.argv[2]);
    await assertSafePagesOutput(emptyRoot);
    const demo = await prepareDashboardPages({ output, now, experimental: true });
    const { buildDashboardSite } = await import(${JSON.stringify(new URL('../../scripts/build.mjs', import.meta.url).href)});
    delete process.env.CAO_DASHBOARD_PROFILE;
    await buildDashboardSite({
      destination: emptyRoot, controlSettings: { campaigns: {}, web: { experimental: true } }
    });
    console.log(JSON.stringify(demo));
  `, builderUrl, JSON.stringify({ output: siteRoot, emptyRoot, now })], {
    cwd: repositoryRoot, timeout: 180_000, maxBuffer: 1024 * 1024
  });
  const demo = JSON.parse(stdout);
  await writeFile(join(emptyRoot, '.cao-pages-demo.json'), JSON.stringify({
    tool: 'cao-pages-demo', synthetic: true, fixture: 'empty'
  }));
  const totalRuns = Reflect.get(enterpriseDemo, 'runs');
  const expected = { runs: totalRuns ?? 0, repositories: enterpriseDemo.repositories };
  if (totalRuns === undefined) {
    for (let start = 0; start < enterpriseDemo.activeRepositories; start++) {
      expected.runs += enterpriseSample(now, { start, count: 1 }).runs;
    }
  }
  // Only the private empty-state fixture uses normalized files. The full demo
  // must populate itself through its production browser worker, without shards.
  /** @type {Record<string, string>} */
  const emptyManifest = {};
  for (const phase of ['runs', 'records']) {
    const body = JSON.stringify({
      kind: 'metadata', schemaVersion: CANONICAL_SCHEMA_VERSION,
      ingestionVersion: NORMALIZED_JSONL_INGESTION_VERSION,
      generatedAt: demo.generatedAt, sourceRecords: 0, records: 0, phase
    }) + '\n';
    const hash = digest(body);
    const name = `gh-aw-logs-${phase}/empty-${hash.slice(0, 16)}.jsonl`;
    await mkdir(dirname(join(emptyRoot, name)), { recursive: true });
    await writeFile(join(emptyRoot, name), body);
    emptyManifest[name] = hash;
  }
  await writeFile(join(emptyRoot, 'inventory-sources.json'), '{}');
  await writeFile(join(emptyRoot, 'payload-hashes.json'), JSON.stringify(emptyManifest));
  return {
    evidenceKind: 'synthetic-enterprise-demo',
    evidenceLimits: 'Browser UX and performance only; not operational freshness, completeness, or production performance evidence.',
    synthetic: true,
    generatedAt: demo.generatedAt,
    expected, sampleVersion: enterpriseDemo.version, retentionAsOf: now
  };
}

export function instrumentPerformancePage() {
  Reflect.set(window, '__dashboardPerformanceRefresh', null);
  document.addEventListener('dashboard-data', (event) => {
    if (event instanceof CustomEvent && event.detail?.kind === 'refresh') {
      Reflect.set(window, '__dashboardPerformanceRefresh', event.detail);
    }
  });
}

/** @param {import('@playwright/test').Page} page
 * @param {{ repositories: number, sampleVersion: number, retentionAsOf: number }} [population]
 */
export async function canonicalCounts(page, population) {
  return page.evaluate(async (population) => {
    const descriptor = (await indexedDB.databases()).find(({ name }) => name?.startsWith('gh-aw-cao-dashboard-data'));
    if (!descriptor?.name) throw new Error('Canonical dashboard database was not created.');
    const databaseName = descriptor.name;
    /** @type {IDBDatabase} */
    const database = await new Promise((resolve, reject) => {
      const request = indexedDB.open(databaseName, descriptor.version);
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
    try {
      if (population) {
        const receipt = await new Promise((resolve, reject) => {
          const request = database.transaction('transactions').objectStore('transactions').get('synthetic-demo:population');
          request.onsuccess = () => resolve(request.result);
          request.onerror = () => reject(request.error);
        });
        if (receipt?.complete !== true || receipt.repositories !== population.repositories
            || receipt.sampleVersion !== population.sampleVersion
            || receipt.sampleDay !== Math.floor(population.retentionAsOf / 86_400_000)) {
          throw new Error('Synthetic performance population is incomplete or incompatible.');
        }
      }
      return Object.fromEntries(await Promise.all([...database.objectStoreNames].map((store) => (
        new Promise((resolve, reject) => {
          const request = database.transaction(store).objectStore(store).count();
          request.onsuccess = () => resolve([store, request.result]);
          request.onerror = () => reject(request.error);
        })
      ))));
    } finally {
      database.close();
    }
  }, population);
}
