// @vitest-environment node
import 'fake-indexeddb/auto';
import { afterEach, expect, it, vi } from 'vitest';
import * as databaseQueries from '../../src/data/queries/database.js';
import { DATABASE_NAME } from '../../src/data/storage/indexeddb.js';

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it('aborts an in-flight subscription query when its view unsubscribes', async () => {
  await new Promise((resolve, reject) => {
    const request = indexedDB.deleteDatabase(DATABASE_NAME);
    request.onsuccess = () => resolve(undefined);
    request.onerror = () => reject(request.error);
  });
  /** @type {((event: { data: Record<string, unknown> }) => void) | undefined} */
  let listener;
  const posted = vi.fn();
  vi.stubGlobal('self', {
    addEventListener: (/** @type {string} */ _type, /** @type {(event: { data: Record<string, unknown> }) => void} */ callback) => { listener = callback; },
    postMessage: posted
  });
  const metadata = { 'as-of': '2026-10-02T20:00:00Z', completeness: 'complete' };
  vi.stubGlobal('fetch', vi.fn(async (input) => (
    String(input).endsWith('/sources/manifest.json')
      ? Response.json({}, { status: 404 })
      : Response.json({ runs: { rows: [], metadata } })
  )));
  const { processDataRequest } = await import('../../src/data-worker.js');
  await processDataRequest({
    operation: 'load-canonical-dashboard', sourceUrl: 'https://dashboard.example/sources.json',
    sourceNames: [], context: { pages: [], queries: [] }
  });
  /** @type {() => void} */
  let release = () => {};
  const waiting = new Promise((resolve) => { release = () => resolve(undefined); });
  /** @type {{ aborted?: boolean } | undefined} */
  let signal;
  vi.spyOn(databaseQueries, 'loadDatabaseQuerySources').mockImplementation(async (_database, _sources, options) => {
    signal = options?.signal;
    await waiting;
    return {};
  });
  listener?.({ data: {
    operation: 'subscribe-canonical-dashboard', subscriptionId: 'removed-view',
    sourceNames: ['runs'], context: { pages: [], queries: [] }
  } });
  await vi.waitFor(() => expect(signal).toBeDefined());
  listener?.({ data: { operation: 'unsubscribe-canonical-dashboard', subscriptionId: 'removed-view' } });
  expect(signal?.aborted).toBe(true);
  release();
  await vi.waitFor(() => expect(posted.mock.calls.some(([message]) => message.subscriptionId === 'removed-view')).toBe(false));
});
