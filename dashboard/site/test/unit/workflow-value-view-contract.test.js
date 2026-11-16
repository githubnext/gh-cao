// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import dashboard from '../../dashboard.json' with { type: 'json' };
import fragment from '../../dashboard-fragments/workflow-value.json' with { type: 'json' };
import contract from '../fixtures/workflow-value-view-contract.json' with { type: 'json' };
import { renderDataView } from '../../src/components/data-view.js';
import { buildChartPoints, prepareChartPoints } from '../../src/components/view-data.js';
import { executeDashboardQueries } from '../../src/data/queries/declarative.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { processDataRequest } from '../../src/data-worker.js';

/** @typedef {{ id: string, mark?: string, chart?: string, data?: { source?: string }, list?: { style?: string, card?: string, drill?: Record<string, unknown> }, "card-drill"?: Record<string, unknown>, "lazy-list"?: boolean, disclosure?: string, layout?: string, encoding?: { card?: string, x?: { field?: string }, y?: { field?: string, aggregate?: string }, href?: { field?: string }, columns?: Array<{ field?: string, title?: string }> } }} View */
/** @typedef {{ id: string, route?: unknown, sections?: unknown, "view-mode-control"?: boolean, views?: View[] }} Page */
/** @typedef {{ id: string, "icon-field"?: string, title?: { field?: string }, subtitle?: { field?: string }, status?: { field?: string }, labels?: Array<{ field?: string }>, details?: Array<{ field?: string }>, timing?: Array<{ field?: string }>, drill?: Record<string, unknown> }} CardTemplate */

/** @type {import('../../src/presenter.js').SourceMetadata} */
const metadata = {
  'source-id': 'workflow-value-view-contract',
  'source-kind': 'fixture',
  'as-of': '2026-11-15T00:00:00Z',
  'retrieved-at': '2026-11-15T00:00:00Z',
  completeness: 'complete',
  freshness: 'unknown',
  availability: 'available'
};

/**
 * @param {string} name
 * @param {Record<string, unknown>[]} rows
 * @returns {import('../../src/presenter.js').LogicalSourceInput}
 */
const source = (name, rows) => ({ source: name, rows, metadata: { ...metadata, 'source-id': name } });

describe('unified Value view contract', () => {
  it('declares type and status plots alongside Cards and Table views for top-level Value', () => {
    const pages = new Map(fragment.pages.map(raw => {
      const page = /** @type {Page} */ (raw);
      return [page.id, page];
    }));
    const page = pages.get(contract.overviewPage);
    expect(page?.route).toBeUndefined();
    expect(page?.sections).toBeUndefined();
    expect(page?.['view-mode-control']).toBe(true);
    expect(page?.views).toHaveLength(4);
    const [chart, statusChart, table, cardView] = page?.views ?? [];
    expect(chart).toMatchObject({
      id: contract.overviewChartView,
      mark: 'chart',
      chart: 'bar',
      data: { source: contract.overviewChartQuery },
      encoding: {
        x: { field: 'value-kind' },
        y: { field: 'value-entries' },
        href: { field: 'value-dashboard-link' }
      }
    });
    expect(statusChart).toMatchObject({
      id: contract.overviewStatusChartView,
      mark: 'chart',
      chart: 'bar',
      data: { source: contract.overviewStatusChartQuery },
      encoding: {
        x: { field: 'value-plot-status' },
        y: { field: 'value-entries' },
        color: { field: 'value-kind' },
        href: { field: 'value-dashboard-link' }
      }
    });
    expect(table).toMatchObject({
      id: contract.overviewTableView,
      mark: 'table',
      data: { source: contract.overviewQuery },
      'lazy-list': true
    });
    expect(table?.encoding?.card).toBeUndefined();
    expect(table?.encoding?.columns?.map(column => column.title)).toEqual([
      'Value entry', 'Type', 'Value status', 'Scope', 'Collection', 'Evidence',
      'Observed value', 'Review basis', 'Evidence captured', 'Evidence through'
    ]);
    expect(cardView).toMatchObject({
      id: contract.overviewCardsView,
      mark: 'list',
      data: { source: contract.overviewQuery },
      list: { style: 'entity-cards', card: contract.overviewCard }
    });
    for (const removed of contract.removedPages) expect(pages.has(removed)).toBe(false);

    const cards = /** @type {CardTemplate[]} */ (dashboard.dashboard['card-templates']);
    const card = cards.find(template => template.id === contract.overviewCard);
    expect(card).toMatchObject({
      'icon-field': 'value-icon',
      title: { field: 'value-title' },
      status: { field: 'value-status' },
      drill: { type: 'external', field: 'value-href' }
    });
  });

  it('renders the Value table with its declared columns', () => {
    const pages = new Map(fragment.pages.map(raw => {
      const page = /** @type {Page} */ (raw);
      return [page.id, page];
    }));
    const table = pages.get(contract.overviewPage)?.views?.find(view => view.id === contract.overviewTableView);
    const columns = table?.encoding?.columns ?? [];
    const rendered = renderDataView('table', {
      pageId: contract.overviewPage,
      title: 'Value entries',
      sourceName: contract.overviewQuery,
      view: table ?? {},
      rows: [{
        'value-title': 'Alpha campaign',
        'value-kind': 'Campaign',
        'value-status': 'Available',
        'value-context': 'octo/alpha',
        'value-collection': 'Campaign',
        'value-evidence': 'Retained result',
        'value-measure': '12 files',
        'value-attention': 'Current evidence',
        'value-captured': '2026-11-15T00:00:00Z',
        'value-cutoff': '2026-11-14T00:00:00Z'
      }],
      metadata,
      contextDetails: [],
      headingTag: 'h2',
      prepareTableRows: rows => rows,
      buildChartPoints: () => [],
      prepareChartPoints: () => [],
      toText: String
    });

    expect(rendered?.querySelector('[data-custom-view-mark="table"]')).not.toBeNull();
    expect([...rendered?.querySelectorAll('thead tr:first-child th') ?? []].map(header => header.textContent?.trim()))
      .toEqual(columns.map(column => column.title));
  });

  it('rolls campaign Value up only from workers and keeps standalone workflows separate', () => {
    const names = new Set([
      'value-campaign-complete-topology',
      'value-campaign-complete-deployments',
      'value-campaign-coverage-deployments',
      'value-campaign-totals',
      'value-campaign-overview',
      'value-standalone-entries',
      'value-overview',
      'value-overview-kind-counts',
      contract.overviewChartQuery
    ]);
    const queries = fragment.queries.filter(query => names.has(query.name));
    const sources = {
      workflows: source('workflows', [
        {
          organization: 'octo', repository: 'control', workflow: '.github/workflows/alpha-orchestrator.md',
          'workflow-role': 'orchestrator', campaign: 'alpha', 'campaign-name': 'Alpha', 'campaign-icon': 'goal'
        },
        {
          organization: 'octo', repository: 'one', workflow: '.github/workflows/alpha-worker.md',
          'workflow-role': 'worker', campaign: 'alpha', 'campaign-name': 'Alpha', 'campaign-icon': 'goal'
        },
        {
          organization: 'octo', repository: 'two', workflow: '.github/workflows/alpha-worker.md',
          'workflow-role': 'worker', campaign: 'alpha', 'campaign-name': 'Alpha', 'campaign-icon': 'goal'
        }
      ]),
      'value-collection-health': source('value-collection-health', [{
        'workflow-route': 'octo/one:.github/workflows/alpha-worker.md',
        registration: 'enabled',
        'collection-state': 'available'
      }]),
      'value-headlines': source('value-headlines', [{
        'workflow-route': 'octo/one:.github/workflows/alpha-worker.md',
        'headline-id': 'worker-one',
        'headline-trend-movement': 'Better',
        'headline-unavailable': false
      }, {
        'workflow-route': 'octo/two:.github/workflows/alpha-worker.md',
        'headline-id': 'worker-two',
        'headline-trend-movement': 'Measurement unavailable',
        'headline-unavailable': true
      }]),
      'value-workflow-overview': source('value-workflow-overview', [
        {
          'workflow-route': 'octo/one:.github/workflows/alpha-worker.md',
          'workflow-role': 'worker',
          'overview-title': 'octo/one · Alpha worker'
        },
        {
          'workflow-route': 'octo/solo:.github/workflows/value.md',
          'workflow-role': 'standalone',
          'repository-coordinate': 'octo/solo',
          'overview-title': 'octo/solo · Value',
          'trend-movement': 'Measurement unavailable',
          'overview-state': 'unknown',
          'overview-evidence': 'Latest assessment unavailable',
          'trend-label': 'Not measured',
          'attention-reason': 'No retained Value result',
          'overview-href': '#page-workflow-value?workflow=octo%2Fsolo',
          'overview-captured': null,
          'overview-cutoff': null
        }
      ])
    };

    const result = executeDashboardQueries(queries, sources, ['value-overview', contract.overviewChartQuery]);
    const rows = result['value-overview'].rows;
    expect(rows.map(row => row['value-kind'])).toEqual(contract.overviewKinds);
    expect(result[contract.overviewChartQuery].rows).toMatchObject([
      { 'value-kind': 'Campaign', 'value-entries': 1, 'value-dashboard-link': { 'dashboard-href': '#page-value-type?kind=Campaign' } },
      { 'value-kind': 'Standalone workflow', 'value-entries': 1, 'value-dashboard-link': { 'dashboard-href': '#page-value-type?kind=Standalone%20workflow' } }
    ]);

    const campaign = rows[0];
    expect(campaign).toMatchObject({
      campaign: 'alpha',
      'value-deployments': 2,
      'value-repositories': 2,
      'current-evidence-deployments': 1,
      'missing-evidence-deployments': 1,
      'unknown-deployments': 1,
      'value-context': '2 workers · 2 repositories',
      'value-evidence': '1 current results · 1 unavailable',
      'value-href': '#page-campaign-value?campaign=alpha'
    });

    const standalone = rows[1];
    expect(standalone).toMatchObject({
      'workflow-role': 'standalone',
      'value-title': 'octo/solo · Value',
      'value-evidence': 'Latest assessment unavailable',
      'value-href': '#page-workflow-value?workflow=octo%2Fsolo'
    });
    expect(rows.some(row => row['workflow-role'] === 'worker' || row['workflow-role'] === 'orchestrator')).toBe(false);
  });

  it('plots entry statuses without splitting campaigns by their worker review count', () => {
    const page = fragment.pages.find(candidate => candidate.id === contract.overviewPage);
    const names = new Set([
      'value-campaign-overview', 'value-standalone-entries', 'value-overview',
      'value-overview-status-counts', contract.overviewStatusChartQuery
    ]);
    const queries = fragment.queries.filter(query => names.has(query.name));
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries',
      queries,
      sourceNames: [contract.overviewStatusChartQuery],
      sources: {
        'value-campaign-totals': source('value-campaign-totals', [
          { campaign: 'alpha', 'campaign-name': 'Alpha', 'review-deployments': 1 },
          { campaign: 'beta', 'campaign-name': 'Beta', 'review-deployments': 4 },
          { campaign: 'gamma', 'campaign-name': 'Gamma', 'review-deployments': 0 }
        ]),
        'value-workflow-overview': source('value-workflow-overview', [
          { 'workflow-role': 'standalone', 'overview-title': 'First', 'trend-movement': 'Better' },
          { 'workflow-role': 'standalone', 'overview-title': 'Second', 'trend-movement': 'No result to compare' },
          { 'workflow-role': 'standalone', 'overview-title': 'Third', 'trend-movement': null },
          { 'workflow-role': 'worker', 'overview-title': 'Not a top-level entry', 'trend-movement': 'Worse' }
        ])
      }
    }));
    const rows = result[contract.overviewStatusChartQuery].rows;
    expect(result[contract.overviewStatusChartQuery].metadata.availability).toBe('available');
    expect(rows).toMatchObject([
      { 'value-kind': 'Campaign', 'value-plot-status': 'Needs review', 'value-entries': 2 },
      { 'value-kind': 'Campaign', 'value-plot-status': 'No review triggered', 'value-entries': 1 },
      { 'value-kind': 'Standalone workflow', 'value-plot-status': 'Better', 'value-entries': 1 },
      { 'value-kind': 'Standalone workflow', 'value-plot-status': 'No result to compare', 'value-entries': 1 },
      { 'value-kind': 'Standalone workflow', 'value-plot-status': 'Status unavailable', 'value-entries': 1 }
    ]);
    expect(rows[0]['value-dashboard-link']).toMatchObject({
      'dashboard-href': '#page-value-status?kind=Campaign&status=Needs%20review'
    });

    const view = page?.views.find(candidate => candidate.id === contract.overviewStatusChartView);
    const rendered = renderDataView('chart', {
      pageId: contract.overviewPage,
      title: 'Value entries by status',
      sourceName: contract.overviewStatusChartQuery,
      view: view ?? {},
      rows,
      metadata: result[contract.overviewStatusChartQuery].metadata,
      contextDetails: [],
      headingTag: 'h2',
      prepareTableRows: () => { throw new Error('Chart must not query rows'); },
      buildChartPoints,
      prepareChartPoints,
      toText: String
    });
    expect(rendered?.querySelectorAll('.bar-chart-bar')).toHaveLength(5);
    const drill = rendered?.querySelector('.chart-point-link[href="#page-value-status?kind=Campaign&status=Needs%20review"]');
    expect(drill?.getAttribute('href')).toBe('#page-value-status?kind=Campaign&status=Needs%20review');
    expect(drill?.getAttribute('aria-label')).toContain('Inspect Value entries by status');
    expect(rendered?.textContent).toContain('Status unavailable');

    const unavailable = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries', queries,
      sourceNames: [contract.overviewStatusChartQuery], sources: {}
    }));
    expect(unavailable[contract.overviewStatusChartQuery].metadata.availability).toBe('unavailable');
    expect(unavailable[contract.overviewStatusChartQuery].rows).toEqual([]);
  });

  it('declares chart, inferred cards and a useful table for workflow Value results', () => {
    const page = /** @type {Page | undefined} */ (
      fragment.pages.find(candidate => candidate.id === contract.page)
    );
    const chart = page?.views?.find(view => view.id === contract.chartView);
    const table = page?.views?.find(view => view.id === contract.view);
    expect(page?.views?.find(view => view.id === contract.collectionView)?.disclosure).toBe('supplemental');
    expect(page?.views?.find(view => view.id === 'workflow-native-measurements')?.disclosure).toBe('supplemental');
    expect(chart).toMatchObject({
      mark: 'chart',
      chart: 'bar',
      data: { source: contract.movementQuery },
      encoding: {
        x: { field: 'movement' },
        y: { field: 'value-entries' },
        href: { field: 'value-dashboard-link' }
      }
    });
    expect(table).toMatchObject({
      mark: 'table',
      data: { source: contract.query },
      'lazy-list': true,
      layout: 'full-view',
      'card-drill': {
        type: 'query',
        page: contract.reportPage,
        query: 'value-report-summary',
        'title-field': 'snapshot-name',
        arguments: [{ name: 'report', field: 'report-id' }]
      }
    });
    expect(table?.encoding?.card).toBeUndefined();

    const card = /** @type {CardTemplate | undefined} */ (
      dashboard.dashboard['card-templates'].find(template => template.id === contract.card)
    );
    const cardFields = [
      card?.title, card?.subtitle, card?.status,
      ...(card?.labels ?? []), ...(card?.details ?? []), ...(card?.timing ?? [])
    ].flatMap(field => field?.field ? [field.field] : []);
    const columns = new Set(table?.encoding?.columns?.flatMap(column => column.field ? [column.field] : []) ?? []);
    expect(cardFields).toEqual([
      'delta-label', 'workflow-context', 'movement', 'snapshot-name',
      'direction-label', 'comparison-label', 'observed-at'
    ]);
    expect(cardFields.every(field => columns.has(field))).toBe(true);
  });

  it('scopes plot drill-downs in the worker and fails closed on missing route arguments', () => {
    const kind = fragment.pages.find(page => page.id === contract.overviewTypePage);
    const status = fragment.pages.find(page => page.id === contract.overviewStatusPage);
    const movement = fragment.pages.find(page => page.id === contract.movementPage);
    if (!kind || !status || !movement) throw new Error('Value drill-down pages must be declared');
    const rows = [
      { 'value-kind': 'Campaign', 'value-plot-status': 'Needs review', 'value-title': 'Alpha' },
      { 'value-kind': 'Campaign', 'value-plot-status': 'No review triggered', 'value-title': 'Beta' },
      { 'value-kind': 'Standalone workflow', 'value-plot-status': 'Needs review', 'value-title': 'Other' }
    ];
    const counts = fragment.queries.filter(query => [
      'value-overview-status-counts', contract.overviewStatusChartQuery
    ].includes(query.name));
    const inputs = { 'value-overview': source('value-overview', rows) };
    /**
     * @param {(typeof fragment.pages)[number]} page
     * @param {string} viewId
     * @param {Record<string, string>} routeParameters
     * @param {typeof fragment.queries} queries
     * @param {Record<string, import('../../src/presenter.js').LogicalSourceInput>} sources
     */
    const run = (page, viewId, routeParameters, queries, sources) => {
      const compiled = compileDashboardViewPayloadQueries(page, page.id, {
        queries, viewId, routeParameters
      });
      const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
        operation: 'execute-dashboard-queries', queries: compiled.queries, sourceNames: compiled.aliases, sources
      }));
      return result[compiled.aliases[0]].rows;
    };
    expect(run(kind, 'value-type-status-chart', { kind: 'Campaign' }, counts, inputs))
      .toMatchObject([
        { 'value-kind': 'Campaign', 'value-plot-status': 'Needs review', 'value-entries': 1 },
        { 'value-kind': 'Campaign', 'value-plot-status': 'No review triggered', 'value-entries': 1 }
      ]);
    expect(run(kind, 'value-type-entries', { kind: 'Campaign' }, [], inputs).map(row => row['value-title']))
      .toEqual(['Alpha', 'Beta']);
    expect(run(status, 'value-status-entries', { kind: 'Campaign', status: 'Needs review' }, [], inputs)
      .map(row => row['value-title'])).toEqual(['Alpha']);
    expect(run(status, 'value-status-entries', { kind: 'Campaign' }, [], inputs)).toEqual([]);
    expect(run(status, 'value-status-evidence', { kind: 'Campaign', status: 'Unknown' }, [], inputs)).toEqual([]);

    const resultRows = [
      { 'workflow-route': 'octo/one:value', movement: 'Better', 'report-id': 'first' },
      { 'workflow-route': 'octo/one:value', movement: 'Worse', 'report-id': 'second' },
      { 'workflow-route': 'octo/two:value', movement: 'Better', 'report-id': 'third' }
    ];
    const resultSource = { 'value-workflow-results': source('value-workflow-results', resultRows) };
    const movementQueries = fragment.queries.filter(query => [
      'value-workflow-movement-counts', contract.movementQuery
    ].includes(query.name));
    const plot = executeDashboardQueries(movementQueries, resultSource, [contract.movementQuery]);
    expect(plot[contract.movementQuery].rows[0]['value-dashboard-link']).toMatchObject({
      'dashboard-href': '#page-value-movement?workflow=octo%2Fone%3Avalue&movement=Better'
    });
    expect(run(movement, 'value-movement-results', { workflow: 'octo/one:value', movement: 'Better' }, [], resultSource)
      .map(row => row['report-id'])).toEqual(['first']);
    expect(run(movement, 'value-movement-results', { workflow: 'octo/one:value' }, [], resultSource)).toEqual([]);
  });
});
