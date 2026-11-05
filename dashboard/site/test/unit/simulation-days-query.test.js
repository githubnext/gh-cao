import 'fake-indexeddb/auto';
import { describe, expect, it, vi } from 'vitest';
import { compileDashboardQueryTypes } from '../../src/query-type-checker.js';
import { processDataRequest } from '../../src/data-worker.js';
import { tidy } from '../../src/data-operations.js';
import { loadDatabaseQuerySources, queryDatabaseSources } from '../../src/data/queries/database.js';
import { executeDashboardQueries, resolveDashboardQuerySources } from '../../src/data/queries/declarative.js';
import {
  compileDashboardViewPayloadQueries,
  dashboardFormDefaultValues,
  resolveDashboardQueryParameters
} from '../../src/data/queries/view-payload-compiler.js';
import { validateDashboardDocument } from '../../src/validator.js';
import { authoritativeDashboard, authoritativeDashboardSource } from '../authoritative-dashboard.js';

const query = {
  name: 'simulated-series',
  subject: 'A synthetic day series for an interactive simulation.',
  from: 'simulation-days',
  compute: [{ as: 'score', function: 'product', args: [{ field: 'day' }, { value: 2 }] }],
  select: [{ field: 'day' }, { field: 'date' }, { field: 'score' }],
  'order-by': [{ field: 'day' }]
};

describe('simulation-days intrinsic query source', () => {
  it('resolves as a typed leaf and rejects invalid fields and shadowing', () => {
    expect(resolveDashboardQuerySources([query], ['simulated-series'])).toEqual([
      'simulated-series', 'simulation-days'
    ]);
    expect(compileDashboardQueryTypes([query])).toMatchObject({
      errors: [],
      queryFields: new Map([['simulated-series', ['day', 'date', 'score']]])
    });
    expect(compileDashboardQueryTypes([{
      name: 'bad-days', from: 'simulation-days',
      compute: [{ as: 'bad', function: 'number', args: [{ field: 'missing' }] }]
    }]).errors).toContainEqual(expect.objectContaining({
      code: 'DLS-E010', path: '$.dashboard.queries[0].compute[0].args[0].field'
    }));
    expect(compileDashboardQueryTypes([{ name: 'simulation-days', from: 'simulation-days' }]).errors)
      .toContainEqual(expect.objectContaining({ code: 'DLS-E005', path: '$.dashboard.queries[0].name' }));
  });

  it('projects thirty ordered numeric rows without opening canonical IndexedDB', async () => {
    const indexedDB = { open: () => { throw new Error('unexpected IndexedDB access'); } };
    const source = (await queryDatabaseSources(
      /** @type {IDBFactory} */ (/** @type {unknown} */ (indexedDB)),
      { 'simulation-days': { rows: [{ day: 999 }], metadata: { availability: 'available' } } },
      ['simulation-days']
    ))['simulation-days'];
    expect(source.rows).toEqual(Array.from({ length: 30 }, (_, index) => ({
      day: index + 1, date: new Date(Date.UTC(2025, 0, index + 1)).toISOString()
    })));
    expect(source.metadata).toMatchObject({ 'source-kind': 'synthetic', availability: 'available' });
    const projected = executeDashboardQueries([query], { 'simulation-days': source }, ['simulated-series']);
    expect(projected['simulated-series'].rows).toEqual(
      Array.from({ length: 30 }, (_, index) => ({
        day: index + 1,
        date: new Date(Date.UTC(2025, 0, index + 1)).toISOString(),
        score: (index + 1) * 2
      }))
    );
    const loaded = await loadDatabaseQuerySources(
      /** @type {IDBFactory} */ (/** @type {unknown} */ (indexedDB)),
      {}, { queries: [query], sourceNames: ['simulated-series'] }
    );
    expect(/** @type {import('../../src/presenter.js').LogicalSourceInput} */ (loaded['simulated-series']).rows)
      .toEqual(projected['simulated-series'].rows);
  });

  it('resolves a worker query graph with no canonical database access', async () => {
    const open = vi.spyOn(indexedDB, 'open');
    try {
      const response = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
        await processDataRequest({
          operation: 'load-dashboard-query-sources',
          sources: {},
          queries: [query],
          sourceNames: ['simulated-series']
        })
      );
      expect(response['simulated-series'].rows).toHaveLength(30);
      expect(response['simulated-series'].rows[0])
        .toEqual({ day: 1, date: '2025-01-01T00:00:00.000Z', score: 2 });
      expect(open).not.toHaveBeenCalled();
    } finally {
      open.mockRestore();
    }
  });

  it('resolves the intrinsic when executing a declared graph without supplied sources', () => {
    const response = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
      processDataRequest({
        operation: 'execute-dashboard-queries',
        sources: {},
        queries: [query],
        sourceNames: ['simulated-series']
      })
    );
    expect(response['simulated-series'].metadata.availability).toBe('available');
    expect(response['simulated-series'].rows).toHaveLength(30);
    expect(response['simulated-series'].rows[29])
      .toEqual({ day: 30, date: '2025-01-30T00:00:00.000Z', score: 60 });
  });

  it('keeps other missing worker inputs unavailable', () => {
    const response = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
      processDataRequest({
        operation: 'execute-dashboard-queries',
        sources: {},
        queries: [{ name: 'missing-runs', from: 'runs' }, query],
        sourceNames: ['missing-runs', 'simulated-series']
      })
    );
    expect(response['missing-runs'].metadata.availability).toBe('unavailable');
    expect(response['simulated-series'].metadata.availability).toBe('available');
  });

  it('accepts a view querying the intrinsic source through the document validator', () => {
    const document = {
      'language-version': '0.1.0',
      dashboard: {
        id: 'synthetic-test', title: 'Synthetic test',
        defaults: { scope: {}, time: {}, filters: {} },
        queries: [query],
        pages: [{
          id: 'simulator', kind: 'custom', title: 'Simulator',
          views: [{
            id: 'daily-projection',
            data: { source: 'simulated-series' },
            mark: 'chart',
            encoding: {
              x: { field: 'date', type: 'temporal', 'time-unit': 'day' },
              y: { field: 'score', aggregate: 'max', type: 'quantitative' }
            }
          }]
        }]
      }
    };
    const result = validateDashboardDocument(JSON.stringify(document));
    expect(result.errors).toEqual([]);
    expect(result.ok).toBe(true);
  });

  it('projects the 30-day simulator results into a table of sizes and a final total', () => {
    const definitions = authoritativeDashboard.dashboard.queries;
    const compiled = compileDashboardQueryTypes(definitions);
    expect(compiled.errors).toEqual([]);
    for (const name of [
      'simulator-database-inputs',
      'simulator-run-size',
      'simulator-tool-size',
      'simulator-issue-size'
    ]) {
      expect(compiled.queryFields.get(name), name).toContain('day');
    }
    for (const name of ['simulator-database-size', 'simulator-database-total', 'simulator-database-summary']) {
      expect(compiled.queryFields.get(name), name).toEqual(['table', 'bytes']);
    }
    const page = authoritativeDashboard.dashboard.pages.find(
      (/** @type {{ id?: string }} */ candidate) => candidate.id === 'simulators'
    );
    expect(page.views[0]).toMatchObject({
      mark: 'table',
      data: { source: 'simulator-database-summary' },
      encoding: { columns: [{ field: 'table' }, { field: 'bytes', unit: 'bytes', title: 'Estimated size after 30 days' }] }
    });
    expect(validateDashboardDocument(authoritativeDashboardSource)).toMatchObject({ ok: true });
  });

  it('projects the authored simulator form through the worker view payload with reactive parameter changes', () => {
    const dashboard = authoritativeDashboard.dashboard;
    const page = dashboard.pages.find((/** @type {{ id?: string }} */ candidate) => candidate.id === 'simulators');
    expect(page).toBeDefined();
    /** @param {Record<string, number>} formValues */
    const render = (formValues) => {
      const compiled = compileDashboardViewPayloadQueries(page, 'simulators', {
        viewId: 'simulator-database-growth',
        queries: dashboard.queries,
        queryContext: { formValues }
      });
      expect(compiled.aliases).toHaveLength(1);
      const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
        processDataRequest({
          operation: 'execute-dashboard-queries',
          queries: compiled.queries,
          sourceNames: compiled.aliases,
          sources: {}
        })
      )[compiled.aliases[0]];
      expect(result.metadata.availability).toBe('available');
      return result.rows;
    };
    const defaults = render({});
    /** @param {Record<string, unknown>[]} rows @param {string} table */
    const byTable = (rows, table) => rows.find((row) => row.table === table);
    expect(defaults).toHaveLength(4);
    expect(defaults.every((row) => Object.keys(row).sort().join(',') === 'bytes,table')).toBe(true);
    expect(byTable(defaults, 'Run summaries')?.bytes).toBe(153_600_000);
    expect(byTable(defaults, 'Tools (30-day TTL)')?.bytes).toBe(614_400_000);
    expect(byTable(defaults, 'Issues (30-day TTL)')?.bytes).toBe(122_880_000);
    expect(byTable(defaults, 'Total')?.bytes).toBe(890_880_000);

    const changed = render({ repositories: 2000, 'skip-rate': 50 });
    expect(byTable(changed, 'Run summaries')?.bytes).toBe(307_200_000);
    expect(byTable(changed, 'Tools (30-day TTL)')?.bytes).toBe(768_000_000);
    expect(byTable(changed, 'Issues (30-day TTL)')?.bytes).toBe(153_600_000);
    expect(byTable(changed, 'Total')?.bytes).toBe(1_228_800_000);
  });

  it('caps the authored detail-days calculation after the 30-day retention horizon', () => {
    const dashboard = authoritativeDashboard.dashboard;
    const page = dashboard.pages.find((/** @type {{ id?: string }} */ candidate) => candidate.id === 'simulators');
    const definition = dashboard.queries.find(
      (/** @type {{ name?: string }} */ candidate) => candidate.name === 'simulator-database-inputs'
    );
    expect(page).toBeDefined();
    expect(definition).toBeDefined();
    const [resolved] = /** @type {Array<{ compute: import('../../src/data-operations.js').ComputedField[] }>} */ (
      resolveDashboardQueryParameters([definition], dashboardFormDefaultValues(page.form))
    );
    const rows = tidy(
      [{ day: 0 }, { day: 30 }, { day: 31 }, { day: 60 }],
      [{ op: 'compute', values: resolved.compute }]
    );
    expect(rows.map((row) => [row['summary-runs'], row['detail-runs']])).toEqual([
      [0, 0], [300_000, 240_000], [310_000, 240_000], [600_000, 240_000]
    ]);
    /** @param {string} queryName */
    const tableBytes = (queryName) => {
      const table = dashboard.queries.find(
        (/** @type {{ name?: string }} */ candidate) => candidate.name === queryName
      );
      expect(table).toBeDefined();
      return tidy(rows, [{
        op: 'compute',
        values: /** @type {import('../../src/data-operations.js').ComputedField[]} */ (table.compute)
      }]).map((row) => row.bytes);
    };
    expect(tableBytes('simulator-run-size')).toEqual([0, 153_600_000, 158_720_000, 307_200_000]);
    expect(tableBytes('simulator-tool-size')).toEqual([0, 614_400_000, 614_400_000, 614_400_000]);
    expect(tableBytes('simulator-issue-size')).toEqual([0, 122_880_000, 122_880_000, 122_880_000]);
  });
});
