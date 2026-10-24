import 'fake-indexeddb/auto';
import { webcrypto } from 'node:crypto';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import contract from '../fixtures/statistical-model-distribution-contract.json' with { type: 'json' };
import { processDataRequest } from '../../src/data-worker.js';
import { DATABASE_NAME } from '../../src/data/storage/indexeddb.js';

const dashboard = authoritativeDashboard.dashboard;

beforeEach(async () => {
  vi.stubGlobal('crypto', webcrypto);
  vi.spyOn(Date, 'now').mockReturnValue(Date.parse(contract.watermark));
  await new Promise((resolve, reject) => {
    const request = indexedDB.deleteDatabase(DATABASE_NAME);
    request.onsuccess = () => resolve(undefined);
    request.onerror = () => reject(request.error);
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it('routes only sample composition to the statistical layer and labels its scope', () => {
  const page = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === contract.page);
  const view = page.views.find((/** @type {{ id: string }} */ view) => view.id === contract.view);
  const query = dashboard.queries.find((/** @type {{ name: string }} */ query) => query.name === contract.query);
  expect(query.from).toBe(contract.source);
  expect(query.aggregate.values).toEqual([
    { field: 'run', as: contract.measure, reducer: 'count' }
  ]);
  expect(view.data.source).toBe(contract.query);
  expect(view.title).toContain('sample');
  expect(view.description).toContain('not population totals');
  expect(view.description).toContain('oversampled');
  expect(view.encoding.y).toMatchObject({ field: contract.measure, title: 'Sampled runs' });
  expect(dashboard.queries.find(
    (/** @type {{ name: string }} */ query) => query.name === contract.exactUsageQuery
  ).from).toBe('runs');
  for (const id of ['engines-models-cost', 'engines-models-token-insights']) {
    expect(page.views.find((/** @type {{ id: string }} */ view) => view.id === id).data.source)
      .toBe(contract.exactUsageQuery);
  }
});

it('resolves sample composition through canonical ingestion and the production worker', async () => {
  const metadata = {
    'source-id': 'statistical-model-contract', 'source-kind': 'fixture',
    'as-of': contract.watermark, 'retrieved-at': contract.watermark,
    availability: 'available', completeness: 'complete', freshness: 'fresh'
  };
  const scope = { organization: 'example', repository: 'control', workflow: '.github/workflows/worker.md' };
  /** @param {string} run @param {string} startedAt @param {string} [status] */
  const run = (run, startedAt, status = 'completed') => ({
    ...scope, run, 'run-attempt': 1, 'started-at': startedAt,
    'run-status': status, 'run-conclusion': status === 'completed' ? 'success' : '',
    'agent-id': 'copilot', 'model-id': 'model', 'aic-total': 10
  });
  await processDataRequest({
    operation: 'load-dashboard-query-sources', ingest: true,
    sourceNames: ['runs'], queries: [],
    sources: {
      repositories: { rows: [{ organization: scope.organization, repository: scope.repository }], metadata },
      workflows: { rows: [scope], metadata },
      runs: {
        rows: [
          run('1', '2026-10-22T12:00:00.000Z'),
          run('2', contract.coverageStart),
          run('3', '2026-10-16T11:59:59.999Z'),
          run('4', '2026-10-22T13:00:00.000Z', 'in_progress'),
          run('5', contract.watermark)
        ],
        metadata
      }
    }
  });
  const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
    await processDataRequest({
      operation: 'load-dashboard-query-sources',
      sourceNames: [contract.query, contract.exactUsageQuery],
      queries: dashboard.queries,
      sources: { runs: { rows: [], metadata } }
    })
  );
  expect(result[contract.query].metadata.availability).toBe('available');
  expect(result[contract.query].metadata['statistical-frame']).toMatchObject({
    watermark: contract.watermark, 'window-start': contract.coverageStart,
    'selected-count': 2
  });
  expect(result[contract.query].rows).toEqual([{ summary: 'copilot / model', 'sampled-runs': 2 }]);
  expect(result[contract.exactUsageQuery].rows).toEqual([
    expect.objectContaining({ summary: 'copilot / model', runs: 5, 'average-aic-per-run': 10 })
  ]);
});
