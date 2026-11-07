import 'fake-indexeddb/auto';
import { describe, expect, it } from 'vitest';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import { processDataRequest } from '../../src/data-worker.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { renderDataView } from '../../src/components/data-view.js';

describe('simulator unit presentation', () => {
  it.each([
    ['simulators', 'simulator-database-growth', {}, ['154 MB', '614 MB', '123 MB', '891 MB']],
    ['simulators', 'simulator-database-growth', { repositories: 2000, 'skip-rate': 50 },
      ['307 MB', '768 MB', '154 MB', '1.2 GB']],
    ['simulator-pull-request', 'simulator-pr-totals', {}, ['100K', '100K', '500K', '500K', '50M', '50M']],
    ['simulator-pull-request', 'simulator-pr-totals', { days: 31 },
      ['100K', '3.1M', '500K', '15.5M', '50M', '1.55B']],
    ['simulator-pull-request', 'simulator-pr-averages', {}, ['10', '50', '5.79']]
  ])('renders %s/%s worker results using declared units', async (pageId, viewId, formValues, expected) => {
    const dashboard = authoritativeDashboard.dashboard;
    const page = dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === pageId);
    const view = page.views.find((/** @type {{ id: string }} */ candidate) => candidate.id === viewId);
    const compiled = compileDashboardViewPayloadQueries(page, pageId, {
      viewId, queries: dashboard.queries, queryContext: { formValues }
    });
    const response = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
      await processDataRequest({
        operation: 'load-dashboard-query-sources', sources: {},
        queries: compiled.queries, sourceNames: compiled.aliases
      })
    );
    const source = response[compiled.aliases[0]];
    expect(source.metadata.availability).toBe('available');
    const originalRows = structuredClone(source.rows);
    const rendered = renderDataView('table', {
      pageId, title: view.title ?? viewId, view, sourceName: compiled.aliases[0],
      rows: source.rows, metadata: source.metadata, units: dashboard.units,
      contextDetails: [], headingTag: 'h3', prepareTableRows: (rows) => rows,
      buildChartPoints: () => [], prepareChartPoints: () => [], toText: String
    });
    const numericCells = [...(rendered?.querySelectorAll('tbody td[data-sort-value]') ?? [])]
      .filter((cell) => Number.isFinite(Number(cell.getAttribute('data-sort-value'))));
    expect(numericCells.map((cell) => cell.textContent)).toEqual(expected);
    expect(numericCells.every((cell) => cell.querySelector('span[title][aria-label]'))).toBe(true);
    expect(numericCells.map((cell) => Number(cell.getAttribute('data-sort-value'))))
      .toEqual(source.rows.flatMap((row) => view.encoding.columns
        .filter((/** @type {{ unit?: string }} */ column) => column.unit)
        .map((/** @type {{ field: string }} */ column) => row[column.field])));
    expect(source.rows).toEqual(originalRows);
  });
});
