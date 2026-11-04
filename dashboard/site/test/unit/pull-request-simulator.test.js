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
    for (const [field, unit] of Object.entries(contract.units)) {
      expect(page.views[0].encoding.columns.find((/** @type {{ field: string }} */ column) => column.field === field))
        .toMatchObject({ type: 'quantitative', unit });
    }
    expect(page.views[1].encoding.columns.map(
      (/** @type {{ unit?: string }} */ column) => column.unit
    )).toEqual(['pull-requests-per-repository-day', 'runs-per-repository-day', 'runs-per-second']);
    expect(dashboardFormDefaultValues(page.form)).toEqual(contract.defaults);
    expect(contract.defaults).toEqual({
      'company-repositories': large.repositories, developers: large.developers,
      'prs-per-developer-per-day': large.prsPerDeveloperPerDay,
      'aw-runs-per-pr': large.awRunsPerPR, days: large.days, 'aic-per-run': large.aicPerRun
    });
  });

  it('validates simulator unit definitions and rejects undeclared units', () => {
    const document = structuredClone(authoritativeDashboard);
    expect(document.dashboard.units.bytes).toEqual({
      name: 'Bytes', symbol: 'B', significant: 1, format: 'bytes'
    });
    expect(document.dashboard.units.count).toEqual({
      name: 'Modeled count', symbol: 'count', significant: 0.01, format: 'compact-number'
    });
    const simulator = document.dashboard.pages.find(
      (/** @type {{ id: string }} */ candidate) => candidate.id === contract.page
    );
    simulator.views[0].encoding.columns[1].unit = 'missing-unit';
    expect(validateDashboardDocument(JSON.stringify(document)).errors).toContainEqual(expect.objectContaining({
      code: 'DLS-E010', message: 'unit must reference a unit declared by dashboard.units.'
    }));
    document.dashboard.units.bytes.symbol = 'KB';
    expect(validateDashboardDocument(JSON.stringify(document)).errors).toContainEqual(expect.objectContaining({
      message: 'byte units must use symbol "B" and significant 1.'
    }));
    document.dashboard.units.bytes.symbol = 'B';
    document.dashboard.units.bytes.significant = 0.1;
    expect(validateDashboardDocument(JSON.stringify(document)).errors).toContainEqual(expect.objectContaining({
      message: 'byte units must use symbol "B" and significant 1.'
    }));
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
      'company-repositories': 7, developers: 3, 'prs-per-developer-per-day': 2,
      'aw-runs-per-pr': 4, days: 31, 'aic-per-run': 0.25
    };
    const rows = await project(values);
    expect(rows).toEqual([
      { measure: 'Pull requests', daily: 6, total: 186 },
      { measure: 'AW runs', daily: 24, total: 744 },
      { measure: 'AI Credits (AIC)', daily: 6, total: 186 }
    ]);
    expect(await project({ ...values, 'company-repositories': 1 })).toEqual(rows);
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
