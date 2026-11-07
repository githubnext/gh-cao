import { describe, expect, it, vi } from 'vitest';
import contract from '../fixtures/scatter-value-contract.json' with { type: 'json' };
import workflowContract from '../fixtures/workflow-run-cost-contract.json' with { type: 'json' };
import workflowFragment from '../../dashboard-fragments/workflow-runs.json' with { type: 'json' };
import operationsFragment from '../../dashboard-fragments/operations.json' with { type: 'json' };
import { validateDashboardDocument } from '../../src/validator.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { processDataRequest } from '../../src/data-worker.js';
import { renderDataView } from '../../src/components/data-view.js';
import { buildChartPoints, toViewText } from '../../src/components/view-data.js';
import { renderChartWidget } from '../../src/components/chart-elements.js';

/** @type {import('../../src/presenter.js').SourceMetadata} */
const metadata = {
  'source-id': 'bubble-evidence', 'source-kind': 'fixture',
  'as-of': '2026-11-01T00:00:00Z', 'retrieved-at': '2026-11-01T00:00:00Z',
  availability: 'available', completeness: 'complete', freshness: 'fresh'
};
const basePage = contract.dashboard.pages[0];
const baseView = basePage.views[0];
const rows = [1, 4, 16].map((value, index) => ({
  run: String(index), 'started-at': `2026-11-01T0${index}:00:00Z`,
  duration: 1000 + index, 'aic-total': value, 'run-conclusion': 'success'
}));

/** @param {Record<string, unknown>} [view] @param {Array<Record<string, unknown>>} [input] */
function execute(view = baseView, input = rows) {
  const compiled = compileDashboardViewPayloadQueries({ ...basePage, views: [view] }, basePage.id, {
    queries: contract.dashboard.queries
  });
  const sources = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
    operation: 'execute-dashboard-queries', queries: compiled.queries, sourceNames: compiled.aliases,
    sources: { runs: { source: 'runs', rows: input, metadata } }
  }));
  return sources[compiled.aliases[0]];
}

/** @param {Record<string, unknown>} view @param {import('../../src/presenter.js').LogicalSourceInput} source */
function render(view, source) {
  const query = vi.fn(() => { throw new Error('Bubble UI must not query or transform source rows'); });
  const element = renderDataView('chart', {
    pageId: basePage.id, title: baseView.title, view, sourceName: source.source,
    rows: source.rows, metadata: source.metadata, contextDetails: [], headingTag: 'h2',
    buildChartPoints, prepareChartPoints: query, prepareTableRows: query, toText: toViewText
  });
  expect(query).not.toHaveBeenCalled();
  return /** @type {HTMLElement} */ (element);
}

describe('scatter value encoding', () => {
  it('uses recorded Actions minutes and AI Credits in the production workflow view, not grader sizes', () => {
    const page = workflowFragment.pages.find((page) => page.id === workflowContract.page);
    const view = page?.views.find((view) => typeof view !== 'string' && view.id === workflowContract.view);
    if (!page || !view || typeof view === 'string' || !('encoding' in view) || !view.encoding) throw new Error('Workflow bubble view is missing');
    expect(view.data).toEqual({ source: workflowContract.source, 'route-field': workflowContract['route-field'] });
    for (const channel of ['x', 'y', 'value', 'color', 'href']) {
      expect(Reflect.get(view.encoding, channel)?.field).toBe(Reflect.get(workflowContract, channel));
    }
    const compiled = compileDashboardViewPayloadQueries(page, page.id, {
      queries: workflowFragment.queries, viewId: view.id,
      routeParameters: { workflow: 'octo/api:worker' }
    });
    const sources = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries', queries: compiled.queries, sourceNames: compiled.aliases,
      sources: { runs: { source: 'runs', rows: [...rows.map((row) => ({
        ...row, duration: '1h', 'action-minutes': 10 + Number(row.run),
        organization: 'octo', repository: 'api', workflow: 'worker'
      })), { ...rows[0], organization: 'octo', repository: 'api', workflow: 'other' }], metadata } }
    }));
    const source = sources[compiled.aliases[0]];
    expect(source.rows.map((row) => row['aic-total'])).toEqual([16, 4, 1]);
    expect(source.rows.map((row) => row['action-minutes'])).toEqual([12, 11, 10]);
    expect(render(view, source).querySelectorAll('.scatter-chart-point')).toHaveLength(3);
    const graders = operationsFragment.pages.find((page) => page.id === 'graders');
    const graderView = graders?.views.find((view) => view.id === 'graders-value-scatter');
    if (!graderView || !('encoding' in graderView)) throw new Error('Grader scatter view is missing');
    expect(graderView.encoding).not.toHaveProperty('value');
  });

  it('validates the declarative contract', () => {
    expect(validateDashboardDocument(JSON.stringify(contract))).toMatchObject({ ok: true });
  });

  it.each([
    { field: 'aic-total', type: 'nominal' },
    { field: 'aic-total' },
    { field: 'missing', type: 'quantitative' },
    { field: 'aic-total', type: 'quantitative', aggregate: 'sum' },
    { field: 'aic-total', type: 'quantitative', display: 'text' },
    { field: 'aic-total', type: 'quantitative', filter: true },
    null
  ])('rejects incompatible value definitions %j', (definition) => {
    const fixture = structuredClone(contract);
    Object.assign(fixture.dashboard.pages[0].views[0].encoding, { value: definition });
    expect(validateDashboardDocument(JSON.stringify(fixture)).ok).toBe(false);
  });

  it.each(['line', 'dot', 'bar', 'pie'])('rejects value on %s charts', (chart) => {
    const fixture = structuredClone(contract);
    fixture.dashboard.pages[0].views[0].chart = chart;
    expect(validateDashboardDocument(JSON.stringify(fixture)).ok).toBe(false);
  });

  it('requires aggregation in the query rather than selecting an arbitrary size from a group', () => {
    const fixture = structuredClone(contract);
    Object.assign(fixture.dashboard.pages[0].views[0].encoding.y, { aggregate: 'mean' });
    expect(validateDashboardDocument(JSON.stringify(fixture)).ok).toBe(false);
  });

  it('uses worker ordering and limiting, preserving each row value and refreshed evidence', () => {
    const view = { ...baseView, data: { ...baseView.data, limit: 2,
      'order-by': [{ field: 'aic-total', direction: 'desc' }] } };
    const source = execute(view);
    expect(source.rows.map((row) => row['aic-total'])).toEqual([16, 4]);
    const element = render(view, source);
    const radii = [...element.querySelectorAll('.scatter-chart-point')].map((point) => Number(point.getAttribute('r')));
    expect(radii).toEqual([4, 2]);
    expect(element.querySelector('.chart-point')?.getAttribute('aria-label')).toContain('AI credits: 16');
    expect(element.querySelector('.chart-legend-size')?.textContent).toContain('16');
    const refreshed = render(view, execute(view, [{ ...rows[0], 'aic-total': 64 }, ...rows.slice(1)]));
    expect(refreshed.querySelector('.chart-legend-size')?.textContent).toContain('64');
  });

  it('renders exact proportional areas and a size legend without a color encoding', () => {
    const view = { ...baseView, encoding: { ...baseView.encoding, color: undefined } };
    const element = render(view, execute(view));
    const radii = [...element.querySelectorAll('.scatter-chart-point')].map((point) => Number(point.getAttribute('r')));
    expect(radii).toEqual([1, 2, 4]);
    expect(element.querySelector('.chart-legend-size')?.getAttribute('aria-label')).toBe('AI credits size legend');
    expect([...element.querySelectorAll('.chart-legend-size li span')].map((item) => item.textContent)).toEqual(['4', '8', '16']);
  });

  it('preserves coincident observations and value encodings in worker-produced facet panels', () => {
    const view = { ...baseView, facet: { field: 'run-conclusion', type: 'nominal' } };
    const source = execute(view, rows.map((row) => ({ ...row, 'started-at': rows[0]['started-at'] })));
    expect(source.rows[0]['facet-rows']).toHaveLength(3);
    const element = render(view, source);
    expect(element.querySelectorAll('.scatter-chart-point')).toHaveLength(3);
    expect(element.querySelectorAll('.chart-legend-size')).toHaveLength(1);
  });

  it('keeps safe point links accessible with their size values', () => {
    const element = render(baseView, execute(baseView, [{ ...rows[0],
      'run-link': { href: 'https://github.com/githubnext/gh-cao/actions/runs/1', label: 'Run 1', relation: 'run' }
    }]));
    const link = element.querySelector('a.chart-point-link');
    expect(link?.getAttribute('href')).toBe('https://github.com/githubnext/gh-cao/actions/runs/1');
    expect(link?.getAttribute('aria-label')).toContain('AI credits: 1');
    expect(link?.getAttribute('tabindex')).toBe('0');
  });

  it('buckets timestamps in the worker without merging separate size observations', () => {
    const view = { ...baseView, encoding: { ...baseView.encoding,
      x: { ...baseView.encoding.x, 'time-unit': 'day' }
    } };
    const source = execute(view);
    expect(new Set(source.rows.map((row) => row['started-at'])).size).toBe(1);
    expect(source.rows.map((row) => row['aic-total'])).toEqual([1, 4, 16]);
    expect(render(view, source).querySelectorAll('.scatter-chart-point')).toHaveLength(3);
  });

  it('does not cluster or sample value-encoded observations even beyond both rendering thresholds', () => {
    const input = Array.from({ length: 2100 }, (_, index) => ({ ...rows[index % rows.length], run: String(index) }));
    const view = { ...baseView, data: { source: 'run-bubbles' } };
    const element = render(view, execute(view, input));
    expect(element.querySelector('.chart-clustering-progress')).toBeNull();
    expect(element.querySelectorAll('.scatter-chart-point')).toHaveLength(input.length);
    expect(element.querySelector('.scatter-chart-point')?.getAttribute('aria-label')).toContain('AI credits:');
  });

  it('reports invalid size observations instead of treating them as zero or fixed-size circles', () => {
    const invalid = [null, undefined, 0, -1, NaN, Infinity, '16'];
    const element = render(baseView, execute(baseView, [
      rows[0], ...invalid.map((value) => ({ ...rows[1], 'aic-total': value }))
    ]));
    expect(element.querySelectorAll('.scatter-chart-point')).toHaveLength(1);
    expect(element.querySelector('.scatter-size-key [role="status"]')?.textContent).toContain('7 observations not plotted');
    const empty = render(baseView, execute(baseView, [{ ...rows[0], 'aic-total': null }]));
    expect(empty.textContent).toContain('No positive finite values available for AI credits');
    expect(empty.querySelector('.scatter-chart-point')).toBeNull();
  });

  it('formats size units independently of y units and avoids overflow for very large values', () => {
    const unit = { name: 'Credits', symbol: 'cr', significant: 0.1 };
    const chart = renderChartWidget('scatter', [
      { x: rows[0]['started-at'], y: 1, color: null, source: { cost: 1e308 } },
      { x: rows[1]['started-at'], y: 2, color: null, source: { cost: 2.5e307 } }
    ], [], null, 'Duration', null, null, null, undefined, null, {}, null, {
      value: { field: 'cost', title: 'Cost', unit }
    });
    expect([...chart.querySelectorAll('.scatter-chart-point')].map((point) => Number(point.getAttribute('r')))).toEqual([4, 2]);
    expect(chart.querySelector('.chart-point')?.getAttribute('aria-label')).toContain('cr');
  });

  it('preserves a positive radius across the finite numeric range', () => {
    const chart = renderChartWidget('scatter', [
      { x: rows[0]['started-at'], y: 1, color: null, source: { cost: Number.MIN_VALUE } },
      { x: rows[1]['started-at'], y: 2, color: null, source: { cost: Number.MAX_VALUE } }
    ], [], null, 'Duration', null, null, null, undefined, null, {}, null, {
      value: { field: 'cost', title: 'Cost', unit: null }
    });
    const radii = [...chart.querySelectorAll('.scatter-chart-point')].map((point) => Number(point.getAttribute('r')));
    expect(radii.every((radius) => Number.isFinite(radius) && radius > 0)).toBe(true);
    expect(radii[1]).toBe(4);
  });
});
