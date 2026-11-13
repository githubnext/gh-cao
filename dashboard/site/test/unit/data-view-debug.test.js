// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

const metadata = {
  'source-id': 'fixture',
  'source-kind': 'fixture',
  'as-of': '2026-08-31T00:00:00Z',
  'retrieved-at': '2026-08-31T00:00:00Z',
  completeness: /** @type {'complete'} */ ('complete'),
  freshness: /** @type {'fresh'} */ ('fresh')
};

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.doUnmock('../../src/data-processor.js');
  vi.unstubAllGlobals();
  vi.resetModules();
  document.body.replaceChildren();
});

/**
 * @param {string} search
 * @param {{ clusteringError?: Error }} [options]
 */
async function loadDataView(search, options = {}) {
  const output = { debug: vi.fn() };
  const categories = /** @type {string[]} */ ([]);
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (
      await vi.importActual('../../src/debug.js')
    );
    return {
      ...actual,
      createDebug: (/** @type {string} */ category) => {
        categories.push(category);
        return actual.createDebug(category, { search: () => search, output });
      }
    };
  });
  if (options.clusteringError) {
    const failure = options.clusteringError;
    vi.doMock('../../src/data-processor.js', async () => ({
      ...(await vi.importActual('../../src/data-processor.js')),
      processScatterPoints: () => Promise.reject(failure)
    }));
  }
  vi.resetModules();
  const module = await import('../../src/components/data-view.js');
  return { ...module, output, categories };
}

/** @param {Record<string, unknown>} overrides */
function context(overrides) {
  return {
    pageId: 'events',
    title: 'Events',
    sourceName: 'events',
    rows: /** @type {Array<Record<string, unknown>>} */ ([]),
    metadata,
    contextDetails: [],
    headingTag: /** @type {'h3'} */ ('h3'),
    prepareTableRows: (/** @type {Array<Record<string, unknown>>} */ rows) => rows,
    buildChartPoints: () => [],
    prepareChartPoints: (/** @type {unknown[]} */ points) => points,
    toText: String,
    ...overrides
  };
}

function stubIntersectionObserver() {
  /** @type {Map<Element, IntersectionObserverCallback>} */
  const callbacks = new Map();
  vi.stubGlobal('IntersectionObserver', class {
    /** @param {IntersectionObserverCallback} callback */
    constructor(callback) { this.callback = callback; }

    /** @param {Element} element */
    observe(element) { callbacks.set(element, this.callback); }

    disconnect() {}
  });
  return (/** @type {Element} */ element) => callbacks.get(element)?.(
    /** @type {IntersectionObserverEntry[]} */ (/** @type {unknown} */ ([{ target: element, isIntersecting: true }])),
    /** @type {IntersectionObserver} */ (/** @type {unknown} */ ({}))
  );
}

/** @param {() => Promise<unknown>} load */
function cardListContext(load) {
  return context({
    view: {
      mark: 'table',
      controls: 'interactive',
      'lazy-list': true,
      layout: 'full-view',
      encoding: { columns: [{ field: 'event', type: 'nominal' }] }
    },
    rows: Array.from({ length: 25 }, (_, index) => ({ event: `event-${index + 1}` })),
    continuation: { token: 'secret-continuation-token', totalRows: 26, load }
  });
}

function swimlaneContext(/** @type {() => Promise<unknown>} */ load) {
  return context({
    pageId: 'runs',
    view: {
      mark: 'chart',
      chart: 'swimlane',
      layout: 'horizontal',
      encoding: {
        x: { field: 'started-at', type: 'temporal' },
        y: { field: 'run-conclusion', type: 'ordinal' }
      }
    },
    rows: [{ run: '1', 'started-at': '2026-08-31T12:48:37Z', 'run-conclusion': 'success' }],
    buildChartPoints: (/** @type {string} */ _pageId, /** @type {string} */ _title, /** @type {Array<Record<string, unknown>>} */ rows) => rows.map((row) => ({
      key: String(row.run),
      x: String(row['started-at']),
      y: Number.NaN,
      category: String(row['run-conclusion']),
      color: String(row['run-conclusion']),
      link: null,
      source: row
    })),
    continuation: { token: 'secret-continuation-token', totalRows: 3, load }
  });
}

describe('data view debug logging', () => {
  it('uses the category derived from the filename', async () => {
    const { categories } = await loadDataView('');
    expect(categories).toContain('data-view');
  });

  it('emits nothing for failures unless the category is enabled', async () => {
    const intersect = stubIntersectionObserver();
    const { renderDataView, output } = await loadDataView('?debug=auth');
    const rendered = renderDataView('table', /** @type {any} */ (cardListContext(vi.fn().mockRejectedValue(new Error('boom')))));
    const boundary = /** @type {HTMLElement} */ (rendered?.querySelector('[data-card-list-boundary]'));
    intersect(boundary);
    await vi.waitFor(() => expect(boundary.dataset.loadState).toBe('error'));
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs a failed card continuation load without tokens or error messages', async () => {
    const intersect = stubIntersectionObserver();
    const { renderDataView, output } = await loadDataView('?debug=data-view');
    const load = vi.fn().mockRejectedValue(new TypeError('secret worker failure for secret-continuation-token'));
    const rendered = renderDataView('table', /** @type {any} */ (cardListContext(load)));
    const boundary = /** @type {HTMLElement} */ (rendered?.querySelector('[data-card-list-boundary]'));
    intersect(boundary);
    await vi.waitFor(() => expect(boundary.dataset.loadState).toBe('error'));

    expect(output.debug.mock.calls).toEqual([
      ['[cao:data-view]', { event: 'card-load-more', status: 'failed', errorName: 'TypeError', renderedCount: 25 }]
    ]);
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain('secret');
  });

  it('logs a failed swimlane continuation with the rows already loaded', async () => {
    const { renderDataView, output } = await loadDataView('?debug=data-view');
    const load = vi.fn().mockRejectedValue(new RangeError('secret swimlane failure'));
    const rendered = renderDataView('chart', /** @type {any} */ (swimlaneContext(load)));
    document.body.append(/** @type {HTMLElement} */ (rendered));
    await vi.waitFor(() => expect(output.debug).toHaveBeenCalledTimes(1));

    expect(output.debug.mock.calls).toEqual([
      ['[cao:data-view]', { event: 'swimlane-continuation', status: 'failed', errorName: 'RangeError', loadedRows: 1 }]
    ]);
    expect(rendered?.textContent).toContain('Showing partial results');
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain('secret');
  });

  it('logs a failed scatter clustering with the point count only', async () => {
    const { renderDataView, output } = await loadDataView('?debug=data-view', {
      clusteringError: new Error('secret clustering failure')
    });
    const points = Array.from({ length: 50_000 }, (_, index) => ({
      key: `secret-point-${index}`,
      x: new Date(Date.parse('2026-09-01T00:00:00Z') + index * 1_000).toISOString(),
      y: index % 101,
      color: 'lane',
      link: null
    }));
    const rendered = renderDataView('chart', /** @type {any} */ (context({
      pageId: 'github-api',
      view: {
        mark: 'chart',
        chart: 'scatter',
        encoding: {
          x: { field: 'observed-at', type: 'temporal' },
          y: { field: 'remaining-percent', type: 'quantitative' },
          color: { field: 'maximum-lane', type: 'nominal' }
        }
      },
      buildChartPoints: () => points
    })));
    document.body.append(/** @type {HTMLElement} */ (rendered));
    await vi.waitFor(() => expect(output.debug).toHaveBeenCalledTimes(1));

    expect(output.debug.mock.calls).toEqual([
      ['[cao:data-view]', { event: 'scatter-clustering', status: 'failed', errorName: 'Error', pointCount: 50_000 }]
    ]);
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain('secret');
  });
});
