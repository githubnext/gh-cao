// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

/** @param {string} search @param {Record<string, unknown> | undefined} state @param {boolean} [fail] */
async function load(search, state, fail = false) {
  const output = { debug: vi.fn() };
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (await vi.importActual('../../src/debug.js'));
    return { ...actual, createDebug: (/** @type {string} */ c) => actual.createDebug(c, { search: () => search, output }) };
  });
  vi.resetModules();
  const { evictDailyRunCounts } = await import('../../src/data/model/daily-run-counts.js');
  const stores = { dailyRunCountState: {
    get: () => request(state), put: () => undefined
  }, dailyRunCounts: { getAll: () => request([]) }, runCountContributions: { getAll: () => request([]) } };
  const transaction = /** @type {any} */ ({
    objectStore: (/** @type {keyof typeof stores} */ name) => stores[name],
    commit() { queueMicrotask(() => this.oncomplete()); },
    abort() { queueMicrotask(() => this.onabort()); }
  });
  /** @param {unknown} value */
  function request(value) {
    const r = /** @type {any} */ ({ result: value });
    queueMicrotask(() => (fail ? r.onerror?.() : r.onsuccess?.()));
    return r;
  }
  const database = /** @type {any} */ ({
    transaction: () => {
      if (!state) queueMicrotask(() => transaction.oncomplete());
      return transaction;
    }
  });
  const keyRange = /** @type {any} */ ({ lowerBound: () => 0 });
  return { evictDailyRunCounts, database, keyRange, output };
}

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('daily-run-counts debug logging', () => {
  it('is silent by default', async () => {
    const { evictDailyRunCounts, database, keyRange, output } = await load('', undefined);
    await evictDailyRunCounts(database, Date.now(), undefined, keyRange);
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs a skip under the predictable category', async () => {
    const { evictDailyRunCounts, database, keyRange, output } = await load('?debug=daily-run-counts', undefined);
    await evictDailyRunCounts(database, Date.now(), undefined, keyRange);
    expect(output.debug).toHaveBeenCalledWith(
      '[cao:daily-run-counts]', { operation: 'evict', status: 'skipped', reason: 'state-not-ready' }
    );
  });

  it('logs eviction counts only, without record contents', async () => {
    const state = { status: 'ready', dailyBucketCount: 2, receiptCount: 3, secret: 'token-value' };
    const { evictDailyRunCounts, database, keyRange, output } = await load('?debug=daily-run-counts', state);
    await evictDailyRunCounts(database, Date.parse('2026-10-24T00:00:00Z'), undefined, keyRange);
    expect(output.debug).toHaveBeenCalledWith('[cao:daily-run-counts]', {
      operation: 'evict', status: 'evicted', cutoff: '2026-10-05', dailyBuckets: 2, receipts: 3
    });
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain('token-value');
  });

  it('logs a sanitized error name on failure and rethrows', async () => {
    const state = { status: 'ready' };
    const { evictDailyRunCounts, database, keyRange, output } = await load('?debug=daily-run-counts', state);
    const failing = Object.assign(new TypeError('secret message'), { name: 'TypeError' });
    const transaction = database.transaction();
    transaction.objectStore = (/** @type {string} */ name) => {
      if (name === 'dailyRunCounts') throw failing;
      return { get: () => { const r = /** @type {any} */ ({ result: state }); queueMicrotask(() => r.onsuccess()); return r; }, put() {} };
    };
    await expect(evictDailyRunCounts(/** @type {any} */ ({ transaction: () => transaction }), Date.now(), undefined, keyRange)).rejects.toBe(failing);
    expect(output.debug).toHaveBeenCalledWith('[cao:daily-run-counts]', { operation: 'evict', status: 'failed', error: 'TypeError' });
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain('secret message');
  });
});
