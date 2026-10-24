// @vitest-environment jsdom
import 'fake-indexeddb/auto';
import { afterEach, describe, expect, it, vi } from 'vitest';

/** @param {string} search */
async function load(search) {
  const output = { debug: vi.fn() };
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (await vi.importActual('../../src/debug.js'));
    return { ...actual, createDebug: (/** @type {string} */ c) => actual.createDebug(c, { search: () => search, output }) };
  });
  vi.resetModules();
  const module = await import('../../src/data/model/daily-run-counts.js');
  return { ...module, output };
}

/** @param {Record<string, unknown> | undefined} state */
async function database(state) {
  const name = `debug-${Math.random()}`;
  const db = await new Promise((resolve, reject) => {
    const request = indexedDB.open(name, 1);
    request.onupgradeneeded = () => {
      for (const store of ['dailyRunCounts', 'runCountContributions', 'dailyRunCountState']) {
        request.result.createObjectStore(store, { keyPath: 'id' });
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
  if (state) {
    await new Promise((resolve) => {
      const tx = /** @type {IDBDatabase} */ (db).transaction('dailyRunCountState', 'readwrite');
      tx.objectStore('dailyRunCountState').put({ id: 'daily-run-counts', ...state });
      tx.oncomplete = () => resolve(undefined);
    });
  }
  return /** @type {IDBDatabase} */ (db);
}

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('daily-run-counts debug logging', () => {
  it('is silent by default', async () => {
    const { evictDailyRunCounts, output } = await load('');
    await evictDailyRunCounts(await database(undefined), Date.now());
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs skipped eviction under its predictable category', async () => {
    const { evictDailyRunCounts, output } = await load('?debug=daily-run-counts');
    await evictDailyRunCounts(await database(undefined), Date.now());
    expect(output.debug).toHaveBeenCalledWith('[cao:daily-run-counts]',
      { operation: 'evict', status: 'skipped', reason: 'no-state' });
  });

  it('logs advanced cutoff with scalar counts only', async () => {
    const { evictDailyRunCounts, output } = await load('?debug=daily-run-counts');
    await evictDailyRunCounts(await database({ status: 'ready', dailyBucketCount: 2, receiptCount: 3, secret: 'token' }), Date.parse('2026-10-26T00:00:00Z'));
    expect(output.debug).toHaveBeenCalledWith('[cao:daily-run-counts]',
      { operation: 'evict', status: 'advanced', cutoff: '2026-10-12', dailyBucketCount: 2, receiptCount: 3 });
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain('token');
  });

  it('logs only a sanitized error name on failure', async () => {
    const { evictDailyRunCounts, output } = await load('?debug=daily-run-counts');
    await expect(evictDailyRunCounts(await database({ status: 'ready' }), Number.NaN)).rejects.toThrow();
    expect(output.debug).toHaveBeenCalledWith('[cao:daily-run-counts]',
      { operation: 'evict', status: 'failed', error: 'TypeError' });
  });
});
