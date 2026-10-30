import 'fake-indexeddb/auto';
import { describe, expect, it, vi } from 'vitest';
import large from '../../../../tests/fixtures/company-stress/large.json' with { type: 'json' };
import contract from '../fixtures/pull-request-simulator-contract.json' with { type: 'json' };
import { authoritativeDashboard, authoritativeDashboardSource } from '../authoritative-dashboard.js';
import { processDataRequest } from '../../src/data-worker.js';
import { compileDashboardViewPayloadQueries, dashboardFormDefaultValues } from '../../src/data/queries/view-payload-compiler.js';
import { validateDashboardDocument } from '../../src/validator.js';

const dashboard = authoritativeDashboard.dashboard;
const page = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === contract.page);

/** @param {Record<string, number>} values @param {string} [viewId] */
async function project(values, viewId = contract.view) {
  const compiled = compileDashboardViewPayloadQueries(page, contract.page, {
    viewId, queries: dashboard.queries, queryContext: { formValues: values }
  });
  const response = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
    await processDataRequest({
      operation: 'load-dashboard-query-sources', sources: {},
      queries: compiled.queries, sourceNames: compiled.aliases
    })
  );
  const source = response[compiled.aliases[0]];
  expect(source.metadata.availability).toBe('available');
  return source.rows;
}

describe('Pull request simulator', () => {
  it('declares a calculator tab, bounded controls and the view contract', () => {
    expect(validateDashboardDocument(authoritativeDashboardSource)).toMatchObject({ ok: true, errors: [] });
    expect(page.route).toMatchObject({
      'navigation-page': contract.navigation,
      tab: 'pull-request',
      tabs: [{ page: contract.navigation }, { label: 'Pull request', page: contract.page }]
    });
    expect(page.views[0]).toMatchObject({
      id: contract.view, mark: 'table', data: { source: contract.query, 'query-context': false }
    });
    expect(page.views[0].encoding.columns.map((/** @type {{ field: string }} */ column) => column.field))
      .toEqual(contract.fields);
    expect(dashboardFormDefaultValues(page.form)).toEqual(contract.defaults);
    expect(contract.defaults).toEqual({
      repositories: large.repositories, developers: large.developers,
      'prs-per-developer-per-day': large.prsPerDeveloperPerDay,
      'aw-runs-per-pr': large.awRunsPerPR, days: large.days, 'aic-per-run': large.aicPerRun
    });
  });

  it('executes large-company arithmetic through the real worker without database reads', async () => {
    const open = vi.spyOn(indexedDB, 'open');
    try {
      expect(await project({})).toEqual([
        { measure: 'Pull requests', daily: 100_000, total: 100_000 },
        { measure: 'AW runs', daily: 500_000, total: 500_000 },
        { measure: 'AI Credits (AIC)', daily: 50_000_000, total: 50_000_000 }
      ]);
      expect(open).not.toHaveBeenCalled();
    } finally {
      open.mockRestore();
    }
  });

  it('recomputes all inputs and does not multiply volume by repository count', async () => {
    const values = {
      repositories: 7, developers: 3, 'prs-per-developer-per-day': 2,
      'aw-runs-per-pr': 4, days: 31, 'aic-per-run': 0.25
    };
    const rows = await project(values);
    expect(rows).toEqual([
      { measure: 'Pull requests', daily: 6, total: 186 },
      { measure: 'AW runs', daily: 24, total: 744 },
      { measure: 'AI Credits (AIC)', daily: 6, total: 186 }
    ]);
    expect(await project({ ...values, repositories: 1 })).toEqual(rows);
    const [averages] = await project(values, 'simulator-pr-averages');
    expect(averages['prs-per-repo']).toBeCloseTo(6 / 7);
    expect(averages['runs-per-repo']).toBeCloseTo(24 / 7);
    expect(averages['runs-per-second']).toBeCloseTo(24 / 86400);
  });

  it('supports zero PR, AW and AIC baselines without non-finite output', async () => {
    for (const parameter of ['prs-per-developer-per-day', 'aw-runs-per-pr', 'aic-per-run']) {
      const rows = await project({ [parameter]: 0 });
      expect(rows[2]).toEqual({ measure: 'AI Credits (AIC)', daily: 0, total: 0 });
      expect(rows.every((row) => Number.isFinite(row.daily) && Number.isFinite(row.total))).toBe(true);
    }
  });
});
