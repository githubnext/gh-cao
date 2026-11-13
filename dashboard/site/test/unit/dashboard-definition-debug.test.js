import { afterEach, describe, expect, it, vi } from 'vitest';

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.unstubAllGlobals();
  vi.resetModules();
});

/** @param {string} search */
async function load(search) {
  const output = { debug: vi.fn() };
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (await vi.importActual('../../src/debug.js'));
    return {
      ...actual,
      createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => search, output })
    };
  });
  vi.resetModules();
  return { ...(await import('../../src/dashboard-definition.js')), output };
}

/** @param {number} status */
function stubFetch(status) {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(
    JSON.stringify({ secret: 'SECRET-BODY' }), { status }
  )));
}

describe('dashboard definition debug logging', () => {
  it('is silent by default and when another category is selected', async () => {
    for (const search of ['', '?debug=auth']) {
      stubFetch(200);
      const { loadDashboardSchema, loadDashboardPageChunk, output } = await load(search);
      await loadDashboardSchema();
      await loadDashboardPageChunk('dashboard-pages/secret-page.json');
      expect(output.debug).not.toHaveBeenCalled();
    }
  });

  it('logs response status under dashboard-definition without paths or bodies', async () => {
    stubFetch(200);
    const { loadDashboardSchema, loadDashboardPageChunk, output } = await load('?debug=dashboard-definition');
    await loadDashboardSchema();
    await loadDashboardPageChunk('dashboard-pages/secret-page.json');
    expect(output.debug.mock.calls).toEqual([
      ['[cao:dashboard-definition]', { event: 'schema-response', status: 200, ok: true }],
      ['[cao:dashboard-definition]', { event: 'page-chunk-response', status: 200, ok: true }]
    ]);
    expect(JSON.stringify(output.debug.mock.calls)).not.toMatch(/secret/i);
  });

  it('logs failed responses and the preload failure with only safe error names', async () => {
    stubFetch(503);
    const { preloadDashboardDefinition, output } = await load('?debug=dashboard-definition');
    preloadDashboardDefinition();
    await vi.waitFor(() => expect(output.debug).toHaveBeenCalledTimes(2));
    expect(output.debug.mock.calls).toEqual([
      ['[cao:dashboard-definition]', { event: 'schema-response', status: 503, ok: false }],
      ['[cao:dashboard-definition]', { event: 'preload-failed', errorName: 'Error' }]
    ]);
  });
});
