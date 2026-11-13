// @vitest-environment node
import 'fake-indexeddb/auto';
import { afterEach, expect, it, vi } from 'vitest';
import * as databaseQueries from '../../src/data/queries/database.js';
import { configureStatisticalRefresh } from '../../src/data/queries/statistical-cache.js';
import { DATABASE_NAME } from '../../src/data/storage/indexeddb.js';

afterEach(() => {
  configureStatisticalRefresh({});
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it('refreshes only mounted statistical worker subscriptions and cleans up on unsubscribe', async () => {
  await new Promise((resolve, reject) => {
    const request = indexedDB.deleteDatabase(DATABASE_NAME);
    request.onsuccess = () => resolve(undefined);
    request.onerror = () => reject(request.error);
  });
  /** @type {((event: { data: Record<string, unknown> }) => void) | undefined} */
  let dispatch;
  const posted = vi.fn();
  vi.stubGlobal('self', {
    addEventListener: (/** @type {string} */ _type, /** @type {(event: { data: Record<string, unknown> }) => void} */ callback) => { dispatch = callback; },
    postMessage: posted
  });
  vi.stubGlobal('fetch', vi.fn(async (input) => (
    String(input).endsWith('/sources/manifest.json')
      ? Response.json({}, { status: 404 })
      : Response.json({ runs: { rows: [], metadata: { 'as-of': '2026-10-02T20:00:00Z', completeness: 'complete' } } })
  )));
  const { processDataRequest } = await import('../../src/data-worker.js');
  await processDataRequest({
    operation: 'load-canonical-dashboard', sourceUrl: 'https://dashboard.example/sources.json',
    sourceNames: [], context: { pages: [], queries: [] }
  });
  vi.useFakeTimers();
  dispatch?.({ data: {
    operation: 'configure-dashboard-data', configuration: { statisticalRefreshIntervalMs: 1_000 }
  } });
  const queries = vi.spyOn(databaseQueries, 'loadDatabaseQuerySources').mockResolvedValue({});
  dispatch?.({ data: {
    operation: 'subscribe-canonical-dashboard', subscriptionId: 'statistical',
    sourceNames: ['composition'], context: {
      pages: [], queries: [{ name: 'composition', from: 'statistical-runs' }]
    }
  } });
  dispatch?.({ data: {
    operation: 'subscribe-canonical-dashboard', subscriptionId: 'exact',
    sourceNames: ['runs'], context: { pages: [], queries: [] }
  } });
  await vi.advanceTimersByTimeAsync(1);
  expect(queries).toHaveBeenCalledTimes(2);
  await vi.advanceTimersByTimeAsync(999);
  expect(queries).toHaveBeenCalledTimes(2);
  await vi.advanceTimersByTimeAsync(50);
  expect(queries).toHaveBeenCalledTimes(3);
  expect(posted.mock.calls.filter(([message]) => message.subscriptionId === 'statistical')).toHaveLength(2);
  dispatch?.({ data: { operation: 'unsubscribe-canonical-dashboard', subscriptionId: 'statistical' } });
  await vi.advanceTimersByTimeAsync(5_000);
  expect(queries).toHaveBeenCalledTimes(3);
  expect(vi.getTimerCount()).toBe(0);
  dispatch?.({ data: { operation: 'unsubscribe-canonical-dashboard', subscriptionId: 'exact' } });
});
