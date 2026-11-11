import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

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
    const { prepareDashboardPages } = await import(process.argv[1]);
    console.log(JSON.stringify(await prepareDashboardPages(JSON.parse(process.argv[2]))));
  `, builderUrl, JSON.stringify({ output: siteRoot, now, experimental: true })], {
    cwd: repositoryRoot, timeout: 180_000, maxBuffer: 1024 * 1024
  });
  const demo = JSON.parse(stdout);
  const fullRoot = join(siteRoot, 'demo-data-v1');
  const emptyDataRoot = join(emptyRoot, 'demo-data-v1');
  const manifest = JSON.parse(await readFile(join(fullRoot, 'payload-hashes.json'), 'utf8'));
  const expected = JSON.parse(await readFile(join(siteRoot, '.cao-pages-demo.json'), 'utf8'));
  const shards = Object.entries(manifest).map(([name, hash]) => {
    if (!/^gh-aw-logs-(?:runs|records)\/[A-Za-z0-9._-]+\.jsonl$/.test(name)
        || typeof hash !== 'string' || !/^[a-f0-9]{64}$/i.test(hash)) {
      throw new Error('Synthetic performance manifest contains an invalid normalized shard.');
    }
    return { name, hash };
  });
  if (!shards.some(({ name }) => name.startsWith('gh-aw-logs-runs/'))
      || !shards.some(({ name }) => name.startsWith('gh-aw-logs-records/'))) {
    throw new Error('Synthetic performance manifest must contain both normalized phases.');
  }
  let bytes = 0;
  for (const shard of shards) {
    const body = await readFile(join(fullRoot, shard.name));
    if (digest(body) !== shard.hash) throw new Error(`Dashboard shard hash mismatch: ${shard.name}`);
    bytes += body.length;
  }
  const first = JSON.parse((await readFile(join(fullRoot, shards[0].name), 'utf8')).split('\n')[0]);
  if (first.kind !== 'metadata') throw new Error('Dashboard shard metadata is missing.');
  /** @type {Record<string, string>} */
  const emptyManifest = {};
  for (const phase of ['runs', 'records']) {
    const body = JSON.stringify({ ...first, sourceRecords: 0, records: 0, phase }) + '\n';
    const hash = digest(body);
    const name = `gh-aw-logs-${phase}/empty-${hash.slice(0, 16)}.jsonl`;
    await mkdir(dirname(join(emptyDataRoot, name)), { recursive: true });
    await writeFile(join(emptyDataRoot, name), body);
    emptyManifest[name] = hash;
  }
  const inventory = JSON.parse(await readFile(join(fullRoot, 'inventory-sources.json'), 'utf8'));
  for (const source of Object.values(inventory)) {
    if (!Array.isArray(source.rows)) throw new Error('Dashboard inventory source rows are invalid.');
    source.rows = [];
  }
  await writeFile(join(emptyDataRoot, 'inventory-sources.json'), JSON.stringify(inventory));
  await writeFile(join(emptyDataRoot, 'payload-hashes.json'), JSON.stringify(emptyManifest));
  return {
    evidenceKind: 'synthetic-enterprise-demo',
    evidenceLimits: 'Browser UX and performance only; not operational freshness, completeness, or production performance evidence.',
    synthetic: true,
    generatedAt: demo.generatedAt,
    expected: { runs: expected.runs, repositories: expected.repositories },
    manifestSha256: digest(JSON.stringify(manifest)), shards: shards.length, bytes, retentionAsOf: now
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

/** @param {import('@playwright/test').Page} page */
export async function canonicalCounts(page) {
  return page.evaluate(async () => {
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
  });
}
