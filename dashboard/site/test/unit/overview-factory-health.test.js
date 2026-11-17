import 'fake-indexeddb/auto';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { processDataRequest } from '../../src/data-worker.js';
import { ingestDashboardSources } from '../../src/data/ingest/coordinator.js';
import { DATABASE_NAME } from '../../src/data/storage/indexeddb.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import contract from '../fixtures/overview-health-contract.json' with { type: 'json' };

const dashboard = authoritativeDashboard.dashboard;
const page = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === 'overview');
const sourceNames = ['overview-workflow-health-summary', 'overview-factory-station', 'overview-header-presentation'];
const metadata = {
  'as-of': '2026-10-15T12:00:00Z',
  'retrieved-at': '2026-10-15T12:00:00Z',
  completeness: 'complete', freshness: 'fresh', availability: 'available'
};

beforeEach(async () => {
  vi.spyOn(Date, 'now').mockReturnValue(Date.parse(metadata['as-of']));
  await new Promise((resolve, reject) => {
    const request = indexedDB.deleteDatabase(DATABASE_NAME);
    request.onsuccess = () => resolve(undefined);
    request.onerror = () => reject(request.error);
  });
});

afterEach(() => vi.restoreAllMocks());

/** @param {Record<string, unknown>[]} [runs] */
async function load(runs = contract.runs) {
  const sources = {
    repositories: {
      rows: [
        { organization: 'acme', repository: 'control' },
        { organization: 'acme', repository: 'target' },
        { organization: 'another', repository: 'target' }
      ], metadata
    },
    workflows: { rows: contract.workflows, metadata },
    campaigns: { rows: [{ campaign: 'one' }], metadata },
    runs: { rows: runs, metadata }
  };
  await ingestDashboardSources(indexedDB, sources, { deferMaintenance: true });
  return /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
    operation: 'load-dashboard-query-sources',
    queries: dashboard.queries,
    sourceNames,
    sources
  }));
}

it('resolves all-workflow Factory health through canonical storage and the production worker', async () => {
  const result = await load();
  expect(result['overview-workflow-health-summary'].rows).toEqual([contract.summary]);
  expect(result['overview-factory-station'].metadata.availability).toBe('available');
  expect(result['overview-factory-station'].rows).toEqual([contract.station]);
  expect(result['overview-header-presentation'].rows).toEqual([{ heading: contract.heading }]);
});

it.each(['failure', 'startup-failure', 'stale', 'timed-out'])('counts standalone %s conclusions as current failures', async (conclusion) => {
  const result = await load([{
    ...contract.runs[1], 'run-conclusion': conclusion
  }]);
  expect(result['overview-workflow-health-summary'].rows).toEqual([{ workflows: 5, 'healthy-workflows': 4 }]);
});

it('clears a current workflow failure only after a later completed non-failing run', async () => {
  const result = await load([
    ...contract.runs,
    { ...contract.runs[1], run: '7', 'run-conclusion': 'success', 'started-at': '2026-10-15T11:30:00Z' }
  ]);
  expect(result['overview-workflow-health-summary'].rows).toEqual([{ workflows: 5, 'healthy-workflows': 4 }]);
});

it('wires the floor view to the all-workflow contract without a campaign-health dependency', () => {
  const payload = compileDashboardViewPayloadQueries(page, 'overview', {
    viewId: 'overview-floor', queries: dashboard.queries, views: dashboard.views
  });
  expect(page.views.find((/** @type {{ id: string }} */ view) => view.id === 'overview-floor').data.sources)
    .toEqual([
      'overview-onboarding-state',
      'overview-factory-station',
      'overview-repository-station',
      'overview-issue-station',
      'overview-value-station'
    ]);
  expect(payload.queries.some((query) => String(query.name).endsWith(':dependency:overview-workflow-health-summary'))).toBe(true);
  expect(payload.queries.some((query) => String(query.name).endsWith(':dependency:campaign-runtime-problems'))).toBe(false);
});

it.each(['workflows', 'overview-runs'])('preserves unavailable %s evidence metadata', (source) => {
  const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
    operation: 'execute-dashboard-queries',
    queries: dashboard.queries,
    sourceNames: ['overview-factory-health-presentation'],
    sources: {
      workflows: { source: 'workflows', rows: contract.workflows, metadata },
      'overview-runs': { source: 'overview-runs', rows: contract.runs, metadata },
      [source]: { source, rows: [], metadata: { ...metadata, availability: 'unavailable' } }
    }
  }));
  if (source === 'workflows') {
    expect(result['overview-factory-health-presentation'].metadata.availability).toBe('unavailable');
  } else {
    expect(result['overview-factory-health-presentation'].metadata.completeness).toBe('partial');
  }
});
