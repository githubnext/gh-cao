// @vitest-environment node
import 'fake-indexeddb/auto';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { DATABASE_NAME } from '../../src/data/storage/indexeddb.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { createDashboardQueryBudget, executeDashboardQueries } from '../../src/data/queries/declarative.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import contract from '../fixtures/campaigns-page-query-contract.json' with { type: 'json' };

/** @typedef {import('../../src/presenter.js').LogicalSourceInput} Source */
const context = { queries: authoritativeDashboard.dashboard.queries, pages: authoritativeDashboard.dashboard.pages };
const page = context.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === contract.page);
const identity = { organization: 'example', repository: 'control', workflow: 'worker' };
const metadata = {
  'as-of': '2026-10-19T12:00:00Z', 'retrieved-at': '2026-10-19T12:00:00Z',
  completeness: 'complete', freshness: 'fresh', availability: 'available'
};
const input = {
  campaigns: { rows: ['alpha', 'beta', 'empty'].map((campaign) => ({
    campaign, 'campaign-name': campaign, 'campaign-mode': 'review', 'campaign-enabled': true
  })), metadata },
  repositories: { rows: [identity, { organization: 'example', repository: 'target' }], metadata },
  workflows: { rows: [{ ...identity, campaign: 'alpha', 'workflow-role': 'worker', 'workflow-active': 'true' }], metadata },
  runs: { rows: [{ ...identity, run: '42', 'run-attempt': 1, 'aic-total': 3,
    event: 'workflow_dispatch', 'target-repository': 'example/target',
    'run-conclusion': 'success', 'started-at': metadata['as-of'] }], metadata },
  ...Object.fromEntries(contract['quality-sources'].map((name) => [name, { rows: [], metadata }]))
};
const subscriptionId = 'campaigns-page';
/** @type {(event: { data: Record<string, unknown> }) => void} */
let listener;
/** @type {Array<{ subscriptionId?: string, data?: Record<string, Source>, error?: string }>} */
let posted;
/** @type {typeof import('../../src/data-worker.js').processDataRequest} */
let processDataRequest;
/** @type {typeof import('../../src/data/queries/database.js')} */
let databaseQueries;

beforeEach(async () => {
  vi.resetModules();
  await new Promise((resolve, reject) => {
    const request = indexedDB.deleteDatabase(DATABASE_NAME);
    request.onsuccess = () => resolve(undefined);
    request.onerror = () => reject(request.error);
  });
  posted = [];
  vi.stubGlobal('self', {
    addEventListener: (/** @type {string} */ _name, /** @type {typeof listener} */ callback) => { listener = callback; },
    postMessage: (/** @type {typeof posted[number]} */ message) => posted.push(message)
  });
  vi.stubGlobal('fetch', vi.fn(async (url) => String(url).endsWith('/sources/manifest.json')
    ? Response.json({}, { status: 404 }) : Response.json(input)));
  databaseQueries = await import('../../src/data/queries/database.js');
  ({ processDataRequest } = await import('../../src/data-worker.js'));
  await refresh();
});

afterEach(() => {
  listener({ data: { operation: 'unsubscribe-canonical-dashboard', subscriptionId } });
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function refresh() {
  return processDataRequest({
    operation: 'load-canonical-dashboard', sourceUrl: 'https://dashboard.example/sources.json',
    sourceNames: [], context
  });
}

/** @param {Record<string, unknown>} [options] */
function subscribe(options = {}) {
  listener({ data: {
    operation: 'subscribe-canonical-dashboard', subscriptionId,
    sourceNames: [contract.source], pageId: contract.page, context, ...options
  } });
}

async function settled() {
  await vi.waitFor(() => expect(posted.filter((message) => message.subscriptionId === subscriptionId)).toHaveLength(1));
  const message = posted.find((entry) => entry.subscriptionId === subscriptionId);
  expect(message?.error).toBeUndefined();
  expect(Object.keys(message?.data ?? {})).toHaveLength(contract.views.length);
  return /** @type {Record<string, Source>} */ (message?.data);
}

describe('Campaigns shared canonical page subscription', () => {
  it('uses the same declarative binding and one dependency graph as Repository overview', () => {
    const repository = context.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === 'repository-detail');
    expect(page['query-binding']).toBe(contract['query-binding']);
    expect(page.requires).toEqual(contract.requires);
    expect(repository['query-binding']).toBe(contract['query-binding']);
    expect(page.definition.views.map((/** @type {{ id: string }} */ view) => view.id)).toEqual(contract.views);
    const compiled = compileDashboardViewPayloadQueries(page, contract.page, { queries: context.queries });
    expect(compiled.queries.filter(query => String(query.name).endsWith(`:dependency:${contract['shared-dependency']}`))).toHaveLength(1);
    expect(compiled.aliases).toEqual(contract.views.map(viewId => `view:campaigns:${viewId}:campaign-inventory`));
    expect(page.definition.views[2].data['partial-source']).toBe('campaign-inventory-preview');
  });

  it('resolves the bounded navigation prerequisite through the production worker', async () => {
    const sources = /** @type {Record<string, Source>} */ (await processDataRequest({
      operation: 'query-canonical-dashboard', sourceNames: [contract.requires.source], context
    }));
    expect(sources[contract.requires.source].rows).toHaveLength(1);
    expect(sources[contract.requires.source].rows[0]).toHaveProperty('campaign');
    expect(sources[contract.requires.source].metadata.availability).toBe('available');
  });

  it('evaluates once and preserves all three independent view payloads and metadata', async () => {
    const expected = await Promise.all(contract.views.map((viewId) => processDataRequest({
      operation: 'query-canonical-dashboard', sourceNames: [contract.source],
      pageId: contract.page, viewId, context
    })));
    const native = vi.spyOn(databaseQueries, 'loadDatabaseQuerySources');
    subscribe();
    const result = await settled();
    expect(native).toHaveBeenCalledTimes(contract['maximum-identical-evaluations-per-refresh']);
    for (const [position, viewId] of contract.views.entries()) {
      const alias = `view:campaigns:${viewId}:campaign-inventory`;
      expect(result[alias]).toEqual(/** @type {Record<string, Source>} */ (expected[position])[alias]);
      expect(result[alias].rows).toHaveLength(contract.rows);
      expect(result[alias].rows[0]).toMatchObject({ campaign: 'alpha', runs: 1, aic: 3, dispatches: 1, 'covered-repositories': 1 });
    }
    const sources = /** @type {Record<string, Source>} */ (await processDataRequest({
      operation: 'query-canonical-dashboard', sourceNames: Object.keys(input), context
    }));
    const compiled = compileDashboardViewPayloadQueries(page, contract.page, { queries: context.queries });
    const budget = createDashboardQueryBudget();
    Object.values(executeDashboardQueries(compiled.queries, sources, compiled.aliases, { budget })).map(source => source.rows);
    let independentOperations = 0;
    for (const viewId of contract.views) {
      const independent = compileDashboardViewPayloadQueries(page, contract.page, { queries: context.queries, viewId });
      const separateBudget = createDashboardQueryBudget();
      Object.values(executeDashboardQueries(independent.queries, sources, independent.aliases, { budget: separateBudget })).map(source => source.rows);
      independentOperations += separateBudget.operations;
    }
    expect(budget.operations).toBeLessThanOrEqual(independentOperations * 0.5);
  });

  it.each(['chart', 'table', 'card'])('retains every view payload when the page presents %s mode', async (viewMode) => {
    subscribe({ queryContext: { viewMode } });
    const result = await settled();
    for (const source of Object.values(result)) expect(source.rows).toHaveLength(contract.rows);
  });

  it('recomputes all view aliases after canonical refresh and unsubscribes cleanly', async () => {
    subscribe();
    await settled();
    posted.length = 0;
    const changed = structuredClone(input);
    changed.runs.rows[0]['aic-total'] = 9;
    vi.mocked(fetch).mockImplementation(async (url) => String(url).endsWith('/sources/manifest.json')
      ? Response.json({}, { status: 404 }) : Response.json(changed));
    const native = vi.spyOn(databaseQueries, 'loadDatabaseQuerySources');
    await refresh();
    const result = await settled();
    expect(native.mock.calls.filter((call) =>
      call[2]?.sourceNames?.some((name) => String(name).startsWith('view:campaigns:'))
    )).toHaveLength(1);
    for (const source of Object.values(result)) expect(source.rows[0]).toMatchObject({ aic: 9 });
    listener({ data: { operation: 'unsubscribe-canonical-dashboard', subscriptionId } });
    posted.length = 0;
    await refresh();
    expect(posted.filter(message => message.subscriptionId === subscriptionId)).toHaveLength(0);
  });

  it('keeps distinct view filters isolated within the single request', async () => {
    subscribe({ context: { ...context, pages: [{ ...page, definition: { ...page.definition,
      views: page.definition.views.map((/** @type {Record<string, unknown>} */ view, /** @type {number} */ position) => ({
        ...view, data: { source: contract.source, filters: { campaign: position === 1 ? 'beta' : 'alpha' } }
      }))
    } }] } });
    const result = await settled();
    for (const [position, viewId] of contract.views.entries()) {
      expect(result[`view:campaigns:${viewId}:campaign-inventory`].rows.map(row => row.campaign))
        .toEqual([position === 1 ? 'beta' : 'alpha']);
    }
  });

  it.each([
    { filters: { campaign: ['beta'] } },
    { search: { fields: ['campaign-name'], query: 'alpha' } },
    { orderBy: [{ field: 'campaign-name', direction: 'desc' }] }
  ])('preserves independent view semantics for context %j', async (queryContext) => {
    const expected = await Promise.all(contract.views.map((viewId) => processDataRequest({
      operation: 'query-canonical-dashboard', sourceNames: [contract.source],
      pageId: contract.page, viewId, context, queryContext
    })));
    subscribe({ queryContext });
    const result = await settled();
    for (const [position, viewId] of contract.views.entries()) {
      const alias = `view:campaigns:${viewId}:campaign-inventory`;
      expect(result[alias]).toEqual(/** @type {Record<string, Source>} */ (expected[position])[alias]);
    }
  });

  it('retains both native and contextual derived payloads on a mixed-context page', async () => {
    const mixedContext = {
      queries: [{ name: 'selected-campaigns', from: 'campaigns' }],
      pages: [{
        id: 'mixed', kind: 'custom', 'query-binding': 'page',
        views: [
          { id: 'all', mark: 'metric', data: { source: 'selected-campaigns', 'query-context': false } },
          { id: 'filtered', mark: 'metric', data: { source: 'selected-campaigns' } }
        ]
      }]
    };
    const result = /** @type {Record<string, Source>} */ (await processDataRequest({
      operation: 'query-canonical-dashboard', sourceNames: ['selected-campaigns'],
      pageId: 'mixed', context: mixedContext, queryContext: { filters: { campaign: ['alpha'] } }
    }));
    expect(result['selected-campaigns']?.rows.map(row => row.campaign)).toEqual(['alpha', 'beta', 'empty']);
    expect(result['view:mixed:filtered:selected-campaigns']?.rows.map(row => row.campaign)).toEqual(['alpha']);
  });

  it('paginates every alias independently after the shared graph evaluation', async () => {
    subscribe({ pagination: Object.fromEntries(contract.views.map((viewId, position) => [
      `view:campaigns:${viewId}:campaign-inventory`, { limit: position + 1 }
    ])) });
    const result = await settled();
    for (const [position, viewId] of contract.views.entries()) {
      const alias = `view:campaigns:${viewId}:campaign-inventory`;
      expect(result[alias].rows).toHaveLength(position + 1);
      expect(result[alias].metadata).toMatchObject({ 'total-row-count': 3, 'query-name': alias });
      if (position < 2) expect(result[alias].continuationToken).toEqual(expect.any(String));
      else expect(result[alias].continuationToken).toBeUndefined();
    }
  });

  it('preserves the selected horizon across all shared view aliases', async () => {
    subscribe({ queryContext: { timeWindow: { start: '2026-10-19T13:00:00Z', end: '2026-10-20T00:00:00Z' } } });
    const result = await settled();
    for (const source of Object.values(result)) expect(source.rows[0])
      .toMatchObject({ campaign: 'alpha', runs: 0, aic: 0, dispatches: 0, 'covered-repositories': 0 });
  });

  it.each(contract['quality-sources'])('preserves partial/stale %s import evidence in every alias', async (name) => {
    const changed = structuredClone(input);
    const quality = /** @type {Record<string, { metadata: typeof metadata }>} */ (changed);
    quality[name].metadata = { ...metadata, completeness: 'partial', freshness: 'stale' };
    vi.mocked(fetch).mockImplementation(async (url) => String(url).endsWith('/sources/manifest.json')
      ? Response.json({}, { status: 404 }) : Response.json(changed));
    await refresh();
    subscribe();
    const result = await settled();
    for (const source of Object.values(result)) expect(source.metadata).toMatchObject({ freshness: 'stale', completeness: 'partial' });
  });
});
