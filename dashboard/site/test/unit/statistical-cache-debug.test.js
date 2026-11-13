// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

/** @param {string} search */
async function loadStatisticalCacheWithDebug(search) {
  const output = { debug: vi.fn() };
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (
      await vi.importActual('../../src/debug.js')
    );
    return {
      ...actual,
      createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => search, output })
    };
  });
  vi.resetModules();
  const module = await import('../../src/data/queries/statistical-cache.js');
  return { ...module, output };
}

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('statistical-cache debug logging', () => {
  it('is disabled by default and for unrelated categories', async () => {
    for (const search of ['', '?debug=data', '?debug=statistical-cache,-statistical-cache']) {
      const { createStatisticalCache, output } = await loadStatisticalCacheWithDebug(search);
      const cache = createStatisticalCache({ now: () => 0, interval: () => 1 });
      await cache.get('scope', 'one', async () => 1);
      cache.invalidate();
      expect(output.debug).not.toHaveBeenCalled();
    }
  });

  it('logs refresh, invalidation, and capacity transitions under its predictable category', async () => {
    const { createStatisticalCache, output } = await loadStatisticalCacheWithDebug('?debug=statistical-cache');
    let time = 100;
    const cache = createStatisticalCache({ now: () => time, interval: () => 1 });

    await cache.get('scope', 'one', async () => {
      time = 142.4;
      return 1;
    });
    expect(output.debug).toHaveBeenCalledWith(
      '[cao:statistical-cache]',
      { operation: 'refresh', status: 'built', durationMs: 42 }
    );

    output.debug.mockClear();
    cache.invalidate();
    expect(output.debug).toHaveBeenCalledWith(
      '[cao:statistical-cache]',
      { operation: 'invalidate', status: 'dropped', slotCount: 1 }
    );

    output.debug.mockClear();
    for (let index = 1; index < 8; index++) await cache.get(`scope-${index}`, 'one', async () => index, undefined, false);
    await expect(cache.get('overflow', 'one', async () => 1)).rejects.toThrow('capacity exceeded');
    expect(output.debug).toHaveBeenCalledWith(
      '[cao:statistical-cache]',
      { operation: 'slot', status: 'capacity-exceeded', slotCount: 8 }
    );
  });

  it('logs failed and superseded refreshes without error messages', async () => {
    const { createStatisticalCache, output } = await loadStatisticalCacheWithDebug('?debug=statistical-*');
    const cache = createStatisticalCache({ now: () => 0, interval: () => 1 });
    await cache.get('failing', 'one', async () => { throw new TypeError('token=secret-value https://example.test/?q=private'); });
    expect(output.debug).toHaveBeenCalledWith(
      '[cao:statistical-cache]',
      { operation: 'refresh', status: 'failed', durationMs: 0, errorName: 'TypeError' }
    );

    output.debug.mockClear();
    let release = () => {};
    const gate = new Promise((resolve) => { release = () => resolve(undefined); });
    const pending = cache.get('superseded', 'one', async () => { await gate; return 1; });
    cache.invalidate();
    release();
    await pending;
    expect(output.debug).toHaveBeenCalledWith(
      '[cao:statistical-cache]',
      { operation: 'refresh', status: 'superseded', durationMs: 0 }
    );

    const serialized = JSON.stringify(output.debug.mock.calls);
    expect(serialized).not.toContain('secret-value');
    expect(serialized).not.toContain('example.test');
    for (const [, payload] of output.debug.mock.calls) {
      for (const value of Object.values(payload)) {
        expect(['string', 'number', 'boolean']).toContain(typeof value);
      }
    }
  });
});
