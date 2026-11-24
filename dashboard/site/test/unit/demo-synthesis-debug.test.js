import { afterEach, describe, expect, it, vi } from 'vitest';
import 'fake-indexeddb/auto';
import { IDBFactory } from 'fake-indexeddb';

vi.mock('../../src/deployment-profile.js', () => ({ DEMO_MODE: true }));

vi.mock('../../src/data/demo/sample.mjs', async (importOriginal) => {
  const actual = /** @type {typeof import('../../src/data/demo/sample.mjs')} */ (await importOriginal());
  return {
    ...actual,
    enterpriseDemo: { ...actual.enterpriseDemo, repositories: 3, installedRepositories: 1 },
    enterpriseActivityBatches: function* () {
      yield { runs: 0, records: [] };
    }
  };
});

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

/** @param {string} search debug query string used to enable logging */
async function importSynthesisWithDebug(search) {
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
  const { synthesizeEnterpriseDemo } = await import('../../src/data/demo/synthesis.js');
  return { synthesizeEnterpriseDemo, output };
}

describe('demo synthesis debug logging', () => {
  it('is disabled by default', async () => {
    const { synthesizeEnterpriseDemo, output } = await importSynthesisWithDebug('');
    await synthesizeEnterpriseDemo(new IDBFactory(), { now: Date.parse('2040-06-15T12:00:00Z') });
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('is not enabled by an unrelated category', async () => {
    const { synthesizeEnterpriseDemo, output } = await importSynthesisWithDebug('?debug=data,render:*');
    await synthesizeEnterpriseDemo(new IDBFactory(), { now: Date.parse('2040-06-15T12:00:00Z') });
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs start, completion and a same-day resume under the predictable category', async () => {
    const { synthesizeEnterpriseDemo, output } = await importSynthesisWithDebug('?debug=synthesis');
    const indexedDB = new IDBFactory();
    const now = Date.parse('2040-06-15T12:00:00Z');

    await synthesizeEnterpriseDemo(indexedDB, { now });
    await synthesizeEnterpriseDemo(indexedDB, { now });

    /** @type {Record<string, any>[]} */
    const events = output.debug.mock.calls.map(([prefix, payload]) => {
      expect(prefix).toBe('[cao:synthesis]');
      return payload;
    });
    expect(events.map(entry => entry.event)).toEqual(['population-started', 'population-completed', 'population-started']);
    expect(events[0]).toMatchObject({ resumed: false, complete: false, repositories: 0 });
    expect(events[1]).toMatchObject({ repositories: 3, installedRepositories: 1 });
    expect(typeof events[1].durationMs).toBe('number');
    expect(events[2]).toMatchObject({ resumed: true, complete: true, repositories: 3 });
  });

  it('logs only scalar metadata and no raw rows, names or storage identifiers', async () => {
    const { synthesizeEnterpriseDemo, output } = await importSynthesisWithDebug('?debug=*');
    await synthesizeEnterpriseDemo(new IDBFactory(), { now: Date.parse('2040-06-15T12:00:00Z') });

    const own = output.debug.mock.calls.filter(([prefix]) => prefix === '[cao:synthesis]');
    expect(own.length).toBeGreaterThan(0);
    for (const [, payload] of own) {
      for (const value of Object.values(payload)) {
        expect(['string', 'number', 'boolean']).toContain(typeof value);
      }
    }
    const serialized = JSON.stringify(own);
    expect(serialized).not.toContain('synthetic-demo');
    expect(serialized).not.toContain('gh-aw-cao-dashboard-data');
  });

  it('logs the reset reason when a stale receipt is replaced', async () => {
    const { synthesizeEnterpriseDemo, output } = await importSynthesisWithDebug('?debug=synthesis');
    const indexedDB = new IDBFactory();

    await synthesizeEnterpriseDemo(indexedDB, { now: Date.parse('2040-06-15T12:00:00Z') });
    await synthesizeEnterpriseDemo(indexedDB, { now: Date.parse('2040-06-16T12:00:00Z') });

    const reset = output.debug.mock.calls.find(([, payload]) => payload.event === 'population-reset');
    expect(reset?.[1]).toEqual({ event: 'population-reset', reason: 'incompatible-receipt' });
  });
});
