import { afterEach, describe, expect, it, vi } from 'vitest';

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

/** @param {string} search */
async function loadStoreWithDebug(search) {
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
  const module = await import('../../src/source-store.js');
  return { ...module, output };
}

/** @type {import('../../src/presenter.js').SourceMetadata} */
const metadata = {
  'source-id': 'fixture',
  'source-kind': 'fixture',
  'as-of': '2026-09-11T12:00:00Z',
  'retrieved-at': '2026-09-11T12:00:00Z',
  availability: 'available',
  completeness: 'complete',
  freshness: 'unknown'
};

const settle = async () => {
  for (let turn = 0; turn < 5; turn += 1) await Promise.resolve();
};

describe('source-store debug logging', () => {
  it('is disabled by default', async () => {
    const { configureSourceLoader, requestSource, output } = await loadStoreWithDebug('');
    configureSourceLoader(() => Promise.resolve({ source: 'runs', rows: [{ id: 1 }], metadata }));

    requestSource('runs');
    await settle();

    expect(output.debug).not.toHaveBeenCalled();
  });

  it('is enabled only by the predictable source-store category', async () => {
    const other = await loadStoreWithDebug('?debug=data:query');
    other.configureSourceLoader(() => Promise.resolve({ source: 'runs', rows: [], metadata }));
    other.requestSource('runs');
    await settle();
    expect(other.output.debug).not.toHaveBeenCalled();

    const selected = await loadStoreWithDebug('?debug=source-store');
    selected.configureSourceLoader(() => Promise.resolve({ source: 'runs', rows: [], metadata }));
    selected.requestSource('runs');
    await settle();
    expect(selected.output.debug).toHaveBeenCalledTimes(1);
    expect(selected.output.debug.mock.calls[0][0]).toBe('[cao:source-store]');
  });

  it('logs the resolved source name, status and row count only', async () => {
    const { configureSourceLoader, requestSource, resetSourceStore, output } = await loadStoreWithDebug('?debug=source-store');
    configureSourceLoader((name) => Promise.resolve(name === 'missing'
      ? undefined
      : { source: name, rows: [{ secret: 'row-secret-value' }, { secret: 'other' }], metadata }));

    requestSource('runs');
    requestSource('missing');
    await settle();

    expect(output.debug.mock.calls.map(([, details]) => details)).toEqual([
      { event: 'resolved', source: 'runs', status: 'ready', rows: 2, stale: false },
      { event: 'resolved', source: 'missing', status: 'missing', rows: 0, stale: false }
    ]);
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain('row-secret-value');
    resetSourceStore();
  });

  it('logs failures with a sanitized error name and never the error message', async () => {
    const { configureSourceLoader, requestSource, sourceState, output } = await loadStoreWithDebug('?debug=source-store');
    configureSourceLoader(() => Promise.reject(new TypeError('token=ghp_secret failed at https://example.test/?q=private')));

    requestSource('runs');
    await settle();

    expect(sourceState('runs').get().status).toBe('failed');
    expect(output.debug.mock.calls.map(([, details]) => details)).toEqual([
      { event: 'failed', source: 'runs', errorName: 'TypeError', stale: false }
    ]);
    const serialized = JSON.stringify(output.debug.mock.calls);
    expect(serialized).not.toContain('ghp_secret');
    expect(serialized).not.toContain('example.test');
  });

  it('reports stale results without publishing them', async () => {
    const { configureSourceLoader, requestSource, refreshSources, sourceState, output } = await loadStoreWithDebug('?debug=source-store');
    /** @type {Array<(value: Record<string, unknown>[]) => void>} */
    const resolvers = [];
    configureSourceLoader((name) => new Promise((resolve) => {
      resolvers.push((rows) => resolve({ source: name, rows, metadata }));
    }));

    requestSource('runs');
    refreshSources();
    resolvers[0]([{ id: 1 }]);
    await settle();
    expect(sourceState('runs').get().status).toBe('loading');
    resolvers[1]([{ id: 1 }, { id: 2 }]);
    await settle();

    expect(sourceState('runs').get().status).toBe('ready');
    expect(output.debug.mock.calls.map(([, details]) => details.stale)).toEqual([true, false]);
  });
});
