import { expect, test } from '@playwright/test';
import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { normalizedRunShard } from './normalized-shard.js';
import { authoritativeDashboard as dashboard } from '../authoritative-dashboard.js';

const siteRoot = fileURLToPath(new URL('../..', import.meta.url));
const origin = 'http://ios-cache.dashboard.test';
const firstIdentity = 'a'.repeat(64);
const secondIdentity = 'b'.repeat(64);

/** @param {number} runId */
function shard(runId) {
  return normalizedRunShard(`${JSON.stringify({
    schema_version: 2,
    kind: 'run',
    run: {
      run_id: runId,
      run_attempt: 1,
      organization: 'githubnext',
      repository: 'gh-cao',
      workflow_name: 'Dashboard',
      workflow_path: '.github/workflows/dashboard.md',
      status: 'completed',
      conclusion: 'success',
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString()
    }
  })}\n`);
}

test('iPhone WebKit retains canonical data through ingest, reload, and another ingest', async ({ context, page }) => {
  await context.route(`${origin}/**`, async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname === '/') {
      await route.fulfill({ contentType: 'text/html', body: '<main>IndexedDB cache test</main>' });
      return;
    }
    const filePath = join(siteRoot, pathname);
    if (existsSync(filePath)) {
      await route.fulfill({
        contentType: pathname.endsWith('.json') ? 'application/json' : 'application/javascript',
        body: readFileSync(filePath)
      });
      return;
    }
    await route.fulfill({ status: 404, body: 'Not found' });
  });

  await page.goto(`${origin}/`);
  const firstShard = shard(1001);
  const secondShard = shard(1002);
  const moduleUrl = `${origin}/src/data/ingest/coordinator.js`;
  const storageUrl = `${origin}/src/data/storage/indexeddb.js`;
  /** @param {string} content @param {string} payloadIdentity */
  const ingest = async (content, payloadIdentity) => page.evaluate(async ({ content, payloadIdentity, moduleUrl }) => {
    const { ingestNormalizedJsonl } = await import(moduleUrl);
    async function* chunks() { yield content; }
    return ingestNormalizedJsonl(indexedDB, chunks(), {
      payloadIdentity,
      payloadScope: `test:${payloadIdentity}`,
      expectedPhase: 'runs',
      storage: navigator.storage
    });
  }, { content, payloadIdentity, moduleUrl });
  const stored = async () => page.evaluate(async (url) => {
    const { readCollection, readTransactions } = await import(url);
    const [runs, receipts] = await Promise.all([
      readCollection(indexedDB, 'runs'),
      readTransactions(indexedDB)
    ]);
    return {
      runIds: runs.map((/** @type {{ id: string }} */ run) => run.id).sort(),
      receiptHashes: receipts.filter((/** @type {{ kind: string }} */ receipt) => receipt.kind === 'ingest-normalized-jsonl')
        .map((/** @type {{ payloadHash: string }} */ receipt) => receipt.payloadHash).sort()
    };
  }, storageUrl);
  const cachedShards = async () => page.evaluate(async ({ url, identities }) => {
    const { normalizedJsonlCurrentShards } = await import(url);
    return normalizedJsonlCurrentShards(indexedDB,
      identities.map((payloadIdentity) => ({ payloadIdentity, expectedPhase: 'runs' })));
  }, { url: moduleUrl, identities: [firstIdentity, secondIdentity] });
  const readSnapshot = async () => page.evaluate(async (url) => {
    const { processDataRequest } = await import(url);
    return processDataRequest({ operation: 'read-dashboard-snapshot' });
  }, `${origin}/src/data-worker.js`);

  expect(await readSnapshot()).toBeNull();
  expect(await ingest(firstShard, firstIdentity)).toMatchObject({ updated: true });
  const createdAt = new Date().toISOString();
  await page.evaluate(async ({ url, createdAt }) => {
    const { recordTransaction } = await import(url);
    await recordTransaction(indexedDB, {
      id: 'dashboard-snapshot:complete', kind: 'dashboard-snapshot', createdAt
    });
  }, { url: storageUrl, createdAt });
  expect(await readSnapshot()).toEqual({ createdAt });
  const firstState = await stored();
  expect(firstState.runIds).toEqual(['github:run:githubnext/gh-cao:1001']);
  expect(firstState.receiptHashes).toEqual([firstIdentity]);

  await page.reload();
  expect(await readSnapshot()).toEqual({ createdAt });
  expect(await stored()).toEqual(firstState);
  expect(await cachedShards()).toEqual([true, false]);
  expect(await ingest(firstShard, firstIdentity)).toMatchObject({ updated: false, skipped: true });
  expect(await ingest(secondShard, secondIdentity)).toMatchObject({ updated: true });
  expect(await cachedShards()).toEqual([true, true]);
  expect(await stored()).toEqual({
    runIds: [
      'github:run:githubnext/gh-cao:1001',
      'github:run:githubnext/gh-cao:1002'
    ],
    receiptHashes: [firstIdentity, secondIdentity]
  });

  await page.reload();
  expect(await readSnapshot()).toEqual({ createdAt });
  expect((await stored()).runIds).toHaveLength(2);
  expect(await cachedShards()).toEqual([true, true]);
});

test('iPhone WebKit completes dashboard import when shard size probing stalls', async ({ context, page }) => {
  test.setTimeout(25_000);
  const shardNames = Array.from({ length: 8 }, (_, index) => {
    const hash = `${'c'.repeat(63)}${index.toString(16)}`;
    return `gh-aw-logs-runs/logs-${hash}-${'d'.repeat(16)}.jsonl`;
  });
  let headRequests = 0;
  let downloadedShards = 0;
  await context.route(`${origin}/**`, async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    const shardIndex = shardNames.indexOf(pathname.slice(1));
    if (pathname === '/') {
      await route.fulfill({
        contentType: 'text/html',
        body: '<meta name="viewport" content="width=device-width, initial-scale=1"><div id="root"></div><script type="module" src="./src/main.js"></script>'
      });
    } else if (pathname === '/dashboard.json') {
      await route.fulfill({ contentType: 'application/json', body: JSON.stringify(dashboard) });
    } else if (pathname === '/inventory-sources.json') {
      await route.fulfill({ contentType: 'application/json', body: '{}' });
    } else if (pathname === '/payload-hashes.json') {
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify(Object.fromEntries(shardNames.map((name, index) => [
          name, `${'c'.repeat(63)}${index.toString(16)}`
        ])))
      });
    } else if (shardIndex !== -1 && route.request().method() === 'HEAD') {
      headRequests += 1;
      await new Promise((resolve) => setTimeout(resolve, 20_000));
      await route.fulfill({ headers: { 'content-length': '512' }, body: '' });
    } else if (shardIndex !== -1) {
      downloadedShards += 1;
      await route.fulfill({ contentType: 'application/x-ndjson', body: shard(1003 + shardIndex) });
    } else {
      const filePath = join(siteRoot, pathname);
      if (existsSync(filePath)) {
        await route.fulfill({
          contentType: pathname.endsWith('.json') ? 'application/json' : 'application/javascript',
          body: readFileSync(filePath)
        });
      } else {
        await route.fulfill({ status: 404, body: 'Not found' });
      }
    }
  });
  await page.goto(`${origin}/`);
  await expect.poll(() => page.evaluate(async (url) => {
    const { processDataRequest } = await import(url);
    return processDataRequest({ operation: 'read-dashboard-snapshot' });
  }, `${origin}/src/data-worker.js`), { timeout: 20_000 }).not.toBeNull();
  expect(headRequests).toBeLessThanOrEqual(4);
  expect(downloadedShards).toBe(shardNames.length);
  expect(await page.evaluate(async (url) => {
    const { readCollection } = await import(url);
    return (await readCollection(indexedDB, 'runs')).map((/** @type {{ id: string }} */ run) => run.id);
  }, `${origin}/src/data/storage/indexeddb.js`)).toEqual(
    Array.from({ length: shardNames.length }, (_, index) => `github:run:githubnext/gh-cao:${1003 + index}`)
  );
});
