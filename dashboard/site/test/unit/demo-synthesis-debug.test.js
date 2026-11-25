import { afterEach, describe, expect, it, vi } from 'vitest';
import 'fake-indexeddb/auto';
import { IDBFactory } from 'fake-indexeddb';

vi.mock('../../src/deployment-profile.js', () => ({ DEMO_MODE: true }));

vi.mock('../../src/data/demo/sample.mjs', async (importOriginal) => {
  const actual = /** @type {typeof import('../../src/data/demo/sample.mjs')} */ (await importOriginal());
  return {
    ...actual,
    enterpriseDemo: { ...actual.enterpriseDemo, repositories: 2, installedRepositories: 1 },
    enterpriseActivityBatches: function* () {}
  };
});

/** @param {string} search */
async function loadSynthesisWithDebug(search) {
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
  const module = await import('../../src/data/demo/synthesis.js');
  return { ...module, output };
}

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('synthesis debug logging', () => {
  const now = Date.parse('2040-06-15T12:00:00Z');

  it('is disabled by default when the debug query is absent', async () => {
    const { synthesizeEnterpriseDemo, output } = await loadSynthesisWithDebug('');
    await synthesizeEnterpriseDemo(new IDBFactory(), { now });
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('does not log when the category is excluded or another category is selected', async () => {
    const { synthesizeEnterpriseDemo, output } = await loadSynthesisWithDebug('?debug=data,-synthesis');
    await synthesizeEnterpriseDemo(new IDBFactory(), { now });
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs start and completion counts under the synthesis category', async () => {
    const { synthesizeEnterpriseDemo, output } = await loadSynthesisWithDebug('?debug=synthesis');
    const indexedDB = new IDBFactory();
    await synthesizeEnterpriseDemo(indexedDB, { now });
    expect(output.debug.mock.calls).toEqual([
      ['[cao:synthesis]', {
        event: 'start', resumed: false, complete: false,
        repositories: 0, installedRepositories: 0, activityBatches: 0
      }],
      ['[cao:synthesis]', {
        event: 'complete', repositories: 2, installedRepositories: 1, activityBatches: 0
      }]
    ]);

    output.debug.mockClear();
    await synthesizeEnterpriseDemo(indexedDB, { now });
    expect(output.debug).toHaveBeenCalledTimes(1);
    expect(output.debug).toHaveBeenCalledWith('[cao:synthesis]', {
      event: 'start', resumed: true, complete: true,
      repositories: 2, installedRepositories: 1, activityBatches: 0
    });
  });

  it('logs a reset reason when an earlier day receipt is replaced', async () => {
    const { synthesizeEnterpriseDemo, output } = await loadSynthesisWithDebug('?debug=synthesis');
    const indexedDB = new IDBFactory();
    await synthesizeEnterpriseDemo(indexedDB, { now });
    output.debug.mockClear();

    await synthesizeEnterpriseDemo(indexedDB, { now: now + 86_400_000 });
    expect(output.debug.mock.calls.map(([, metadata]) => metadata.event)).toEqual(['reset', 'start', 'complete']);
    expect(output.debug.mock.calls[0][1]).toEqual({ event: 'reset', reason: 'incompatible-receipt' });
  });

  it('only logs scalar metadata', async () => {
    const { synthesizeEnterpriseDemo, output } = await loadSynthesisWithDebug('?debug=*');
    await synthesizeEnterpriseDemo(new IDBFactory(), { now });
    const calls = output.debug.mock.calls.filter(([prefix]) => prefix === '[cao:synthesis]');
    expect(calls.length).toBeGreaterThan(0);
    for (const [, metadata] of calls) {
      for (const value of Object.values(metadata)) {
        expect(['string', 'number', 'boolean']).toContain(typeof value);
      }
    }
  });
});
