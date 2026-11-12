import 'fake-indexeddb/auto';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { processDataRequest } from '../../src/data-worker.js';
import {
  createDashboardQueryBudget,
  dashboardQueryOutputFields,
  executeDashboardQueries,
  resolveDashboardQuerySources
} from '../../src/data/queries/declarative.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { DATABASE_NAME } from '../../src/data/storage/indexeddb.js';
import { TABLE_FIELDS } from '../../src/specification.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import previewContract from '../fixtures/campaign-inventory-preview-contract.json' with { type: 'json' };

/** @typedef {import('../../src/data/queries/declarative.js').DashboardQuery} Query */
/** @typedef {import('../../src/presenter.js').LogicalSourceInput} Source */
/** @typedef {import('../../src/presenter.js').SourceMetadata} Metadata */
/** @typedef {Omit<Source, 'metadata'> & { metadata?: Metadata }} InputSource */

// The CLI estimator is outside the dashboard's strict TypeScript project.
const complexityModule = resolve('../../dashboard/report/query-complexity.mjs');
/** @type {{ analyzeDashboardComplexity: (document: unknown) => { inventory: Array<{ name: string, 'total-row-read-units': number, 'total-materialized-field-units': number }> } }} */
const { analyzeDashboardComplexity } = createRequire(resolve('package.json'))(complexityModule);
/** @type {Record<string, string[]>} */
const tableFields = TABLE_FIELDS;

const names = ['campaign-inventory', 'campaign-workflows', 'campaign-workflow-totals'];
/** @type {Query[]} */
const queries = authoritativeDashboard.dashboard.queries;

// The original consumers retain the public workflow-inventory dependency.
/** @type {Record<string, Query>} */
const originalConsumers = {
  'campaign-dispatch-totals': {
    name: 'campaign-dispatch-totals',
    from: 'dispatches',
    aggregate: { by: ['campaign'], values: [{ field: 'run', as: 'dispatches', reducer: 'distinct-count' }] }
  },
  'campaign-workflow-totals': {
    name: 'campaign-workflow-totals',
    from: 'workflow-inventory',
    filter: { predicates: [{ field: 'workflow-role', in: ['orchestrator', 'worker'] }] },
    aggregate: {
      by: ['campaign'],
      values: [
        { field: 'workflow', as: 'workflows', reducer: 'count' },
        { field: 'workflow-role', as: 'roles', reducer: 'distinct-list' },
        { field: 'rollout-mode', as: 'modes', reducer: 'distinct-list' },
        { field: 'workflow-active', as: 'registration', reducer: 'distinct-list' },
        { field: 'runs', as: 'runs', reducer: 'sum' },
        { field: 'aic', as: 'aic', reducer: 'sum' }
      ]
    }
  },
  'campaign-workflows': {
    name: 'campaign-workflows',
    from: 'workflow-inventory',
    filter: {
      predicates: [
        { field: 'workflow-role', in: ['orchestrator', 'worker'] },
        { field: 'workflow-active', equals: 'true' }
      ]
    },
    select: [
      'campaign', 'campaign-name', 'organization', 'repository', 'workflow',
      'workflow-name', 'workflow-role', 'rollout-mode', 'workflow-active',
      'runs', 'aic', 'repository-link', 'workflow-link'
    ].map((field) => ({ field })),
    'order-by': [
      { field: 'campaign-name', direction: 'asc' },
      { field: 'workflow-role', direction: 'asc' },
      { field: 'workflow-name', direction: 'asc' }
    ]
  }
};
const baseline = queries.map((query) => originalConsumers[query.name] ?? query);

/** @param {string} name @param {Partial<Metadata>} [overrides] @returns {Metadata} */
function metadata(name, overrides = {}) {
  return {
    'source-id': name,
    'source-kind': 'fixture',
    'as-of': '2026-09-22T12:00:00Z',
    'retrieved-at': '2026-09-22T12:05:00Z',
    availability: 'available',
    completeness: 'complete',
    freshness: 'fresh',
    ...overrides
  };
}

/** @param {string} name @param {Record<string, unknown>[]} rows @returns {Source} */
function source(name, rows) {
  return { source: name, rows, metadata: metadata(name) };
}

/** @param {number} [scale] */
function evidence(scale = 1) {
  const declared = [
    { repository: 'control', workflow: 'alpha-plan', campaign: 'alpha', 'workflow-role': 'orchestrator' },
    { repository: 'control', workflow: 'alpha-worker', campaign: 'alpha', 'workflow-role': 'worker', 'workflow-name': 'Same worker' },
    { repository: 'other', workflow: 'alpha-worker', campaign: 'alpha', 'workflow-role': 'worker', 'workflow-name': 'Same worker' },
    { repository: 'control', workflow: 'alpha-inactive', campaign: 'alpha', 'workflow-role': 'worker', 'workflow-active': 'false', 'rollout-mode': 'live' },
    { repository: 'control', workflow: 'alpha-unknown', campaign: 'alpha', 'workflow-role': 'worker', 'workflow-active': 'unknown', 'rollout-mode': '' },
    { repository: 'control', workflow: 'standalone', campaign: 'alpha', 'workflow-role': 'standalone' },
    { repository: 'control', workflow: 'roleless', campaign: 'alpha', 'workflow-role': '' },
    { repository: 'control', workflow: 'beta-worker', campaign: 'beta', 'workflow-role': 'worker', 'workflow-name': '' },
    { repository: 'control', workflow: 'unattributed', campaign: '', 'workflow-role': 'worker', 'workflow-name': '' }
  ];
  const workflowRows = declared.map((row, index) => ({
    organization: 'example',
    'workflow-active': 'true',
    'rollout-mode': 'review',
    'workflow-name': row.workflow,
    'campaign-name': row.campaign === 'alpha' ? 'Alpha' : '',
    'workflow-link': { relation: 'workflow', href: `https://github.com/example/${row.repository}/actions/workflows/${index + 1}` },
    ...row
  }));
  const runRows = workflowRows.filter((row) => row.workflow !== 'alpha-plan').flatMap((row, index) => (
    Array.from({ length: scale * 3 }, (_, attempt) => ({
      organization: row.organization,
      repository: row.repository,
      workflow: row.workflow,
      run: String(index * 100000 + Math.floor(attempt / 2) + 1),
      'run-attempt': attempt % 2 + 1,
      'run-status': 'completed',
      'run-conclusion': attempt % 2 ? 'failure' : 'success',
      'failure-kind': attempt % 2 ? 'driver_exit' : '',
      'terminal-outcome': attempt % 2 ? 'report_incomplete' : undefined,
      'terminal-outcome-detail': attempt % 2 ? 'Retained diagnostic evidence' : undefined,
      'aic-total': (index + 1) * 0.25,
      event: attempt % 2 ? 'schedule' : 'workflow_dispatch',
      'target-repository': attempt % 4 ? 'example/target' : 'example/unregistered',
      'started-at': attempt % 3 === 0 ? '2026-09-18T12:00:00Z'
        : attempt % 3 === 1 ? '2026-09-20T12:00:00Z' : '2026-09-23T12:00:00Z'
    }))
  ));
  const recordSources = Object.fromEntries(['audits', 'domains', 'tools', 'issues'].map((name) => [
    name,
    source(name, runRows.flatMap((run, index) => (
      Array.from({ length: scale === 1 ? 1 : 4 }, (_, record) => ({
        organization: run.organization,
        repository: run.repository,
        workflow: run.workflow,
        run: run.run,
        'run-attempt': run['run-attempt'],
        event: `${name}-${index}-${record}`,
        'event-source': 'fixture',
        'safe-output-url': name === 'issues'
          ? `https://github.com/example/${run.repository}/issues/${index * 4 + record + 1}` : '',
        'event-type': name === 'audits' && index % 2 ? 'safe_output.result' : 'mcp.tool_call',
        'safe-output-type': name === 'audits' && index % 2 ? 'report_incomplete' : '',
        'event-summary': 'Retained diagnostic evidence',
        'event-timestamp': run['started-at']
      }))
    )))
  ]));
  return {
    campaigns: source('campaigns', [
      { campaign: 'alpha', 'campaign-name': 'Alpha', 'campaign-mode': 'review', 'campaign-enabled': true },
      { campaign: 'beta', 'campaign-name': 'Beta', 'campaign-mode': 'review', 'campaign-enabled': true },
      { campaign: 'empty', 'campaign-name': 'Empty declared campaign', 'campaign-mode': 'review', 'campaign-enabled': false }
    ]),
    repositories: source('repositories', [
      { organization: 'example', repository: 'control' },
      { organization: 'example', repository: 'other' },
      { organization: 'example', repository: 'target' }
    ]),
    workflows: source('workflows', workflowRows),
    runs: source('runs', runRows),
    ...recordSources
  };
}

/** @param {ReturnType<typeof evidence>} [input] @returns {Promise<Record<string, Source>>} */
async function canonicalSources(input = evidence()) {
  return /** @type {Record<string, Source>} */ (await processDataRequest({
    operation: 'load-dashboard-query-sources',
    sources: input,
    ingest: true,
    queries: [],
    sourceNames: resolveDashboardQuerySources(baseline, names).filter((name) => Object.hasOwn(TABLE_FIELDS, name))
  }));
}

/** @param {Query[]} definitions @param {Record<string, InputSource>} sources @param {string[]} [requested] */
function workerResults(definitions, sources, requested = names) {
  const lazy = /** @type {Record<string, Source>} */ (processDataRequest({
    operation: 'execute-dashboard-queries', queries: definitions, sources, sourceNames: requested
  }));
  return Object.fromEntries(requested.map((name) => [
    name, { source: lazy[name].source, rows: lazy[name].rows, metadata: lazy[name].metadata }
  ]));
}

/** @param {Record<string, InputSource>} sources */
function parity(sources) {
  const before = workerResults(baseline, sources);
  const after = workerResults(queries, sources);
  expect(after).toEqual(before);
  return after;
}

beforeEach(async () => {
  vi.spyOn(Date, 'now').mockReturnValue(Date.parse('2026-09-23T12:00:00Z'));
  await new Promise((resolve, reject) => {
    const request = indexedDB.deleteDatabase(DATABASE_NAME);
    request.onsuccess = () => resolve(undefined);
    request.onerror = () => reject(request.error);
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('campaign inventory query optimization', () => {
  it('projects campaign identities and navigation without execution inputs while totals are pending', async () => {
    const page = authoritativeDashboard.dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === previewContract.page);
    const view = page?.definition?.views?.find((/** @type {{ id: string }} */ candidate) => candidate.id === previewContract.view);
    expect(view?.data).toMatchObject({
      source: previewContract.source, 'partial-source': previewContract.partialSource
    });
    const compiled = compileDashboardViewPayloadQueries(page, previewContract.page, {
      queries, viewId: previewContract.view, partial: true
    });
    expect(resolveDashboardQuerySources([...queries, ...compiled.queries], compiled.aliases)
      .filter((name) => !compiled.aliases.includes(name))).toEqual(['campaigns']);
    const sources = await canonicalSources();
    const preview = workerResults(/** @type {Query[]} */ ([...queries, ...compiled.queries]), sources, compiled.aliases)[compiled.aliases[0]];
    const complete = workerResults(queries, sources, [previewContract.source])[previewContract.source];
    expect(preview.rows.map((row) => row.campaign)).toEqual(complete.rows.map((row) => row.campaign));
    for (const row of preview.rows) {
      const final = complete.rows.find((candidate) => candidate.campaign === row.campaign);
      expect(row['campaign-dashboard-link']).toEqual(final?.['campaign-dashboard-link']);
      for (const field of previewContract.pendingFields) expect(row).not.toHaveProperty(field);
    }
  });

  it('preserves canonical declared inventory, attempt/AIC totals, inactive state, links, and stable ordering', async () => {
    const sources = await canonicalSources();
    const results = parity(sources);
    expect(results['campaign-inventory'].rows.map((row) => row.campaign)).toEqual(['alpha', 'beta', 'empty']);
    expect(results['campaign-inventory'].rows).toEqual(expect.arrayContaining([
      expect.objectContaining({
        campaign: 'alpha', workflows: 5, roles: 'orchestrator, worker',
        modes: 'live, review, unknown', registration: 'false, true, unknown',
        runs: 8, aic: 5, dispatches: 6, 'covered-repositories': 1
      }),
      expect.objectContaining({ campaign: 'empty', workflows: 0, runs: 0, aic: 0, dispatches: 0, 'covered-repositories': 0 })
    ]));
    expect(results['campaign-workflows'].rows.map((row) => row.workflow)).toEqual([
      'alpha-plan', 'alpha-worker', 'alpha-worker', 'beta-worker', 'unattributed'
    ]);
    expect(results['campaign-workflows'].rows[0]).toMatchObject({ runs: 0, aic: 0 });
    expect(results['campaign-workflows'].rows[1]).toMatchObject({
      repository: 'example/control',
      'repository-link': { 'dashboard-href': '#page-repository-detail?repository=example%2Fcontrol' },
      'workflow-link': { 'dashboard-href': '#page-workflow-runtime?workflow=example%2Fcontrol%3Aalpha-worker' }
    });
    for (const name of names) expect(results[name].metadata.availability).toBe('available');
  });

  it('preserves selected-horizon filtering through the view compiler and worker boundary', async () => {
    const sources = await canonicalSources();
    for (const name of names) {
      const page = { views: [{ id: 'inventory', mark: 'table', data: { source: name } }] };
      const options = { queryContext: { timeWindow: { start: '2026-09-19T00:00:00Z', end: '2026-09-23T12:00:00Z' } } };
      const before = compileDashboardViewPayloadQueries(page, 'campaigns', { ...options, queries: baseline });
      const after = compileDashboardViewPayloadQueries(page, 'campaigns', { ...options, queries });
      expect(after.aliases).toEqual(before.aliases);
      const original = workerResults(/** @type {Query[]} */ (before.queries), sources, before.aliases);
      const optimized = workerResults(/** @type {Query[]} */ (after.queries), sources, after.aliases);
      expect(optimized).toEqual(original);
      expect(optimized[after.aliases[0]].rows.length).toBeGreaterThan(0);
      if (name !== 'campaign-workflows') {
        expect(optimized[after.aliases[0]].rows.find((row) => row.campaign === 'alpha')).toMatchObject({ runs: 4, aic: 2.5 });
      }
    }
  });

  it('retains empty declarations and missing run evidence instead of implying a complete inventory', async () => {
    const sources = await canonicalSources();
    const withoutRuns = {
      ...sources,
      runs: { ...sources.runs, rows: [], metadata: metadata('runs', { availability: 'unavailable', completeness: 'unknown' }) }
    };
    const results = parity(withoutRuns);
    expect(results['campaign-workflows'].rows.length).toBeGreaterThan(0);
    for (const name of names) expect(results[name].metadata.completeness).toBe('partial');
    parity({
      ...withoutRuns,
      domains: { ...sources.domains, metadata: metadata('domains', { freshness: 'stale' }) }
    });
    const empty = Object.fromEntries(Object.entries(sources).map(([name, value]) => [
      name, { ...value, rows: [], metadata: metadata(name, { availability: 'empty' }) }
    ]));
    for (const value of Object.values(parity(empty))) {
      expect(value.rows).toEqual([]);
      expect(value.metadata.availability).toBe('empty');
    }
  });

  it.each(['audits', 'domains', 'tools', 'issues'])('preserves unavailable, absent, and stale %s evidence quality', async (name) => {
    const sources = await canonicalSources();
    const absent = { ...sources };
    delete absent[name];
    const missing = parity(absent);
    for (const selected of names) {
      expect(missing[selected].rows.length).toBeGreaterThan(0);
      expect(missing[selected].metadata.completeness).toBe('partial');
    }
    const unavailable = parity({
      ...sources,
      [name]: { ...sources[name], metadata: metadata(name, { availability: 'unavailable' }) }
    });
    for (const selected of names) expect(unavailable[selected].metadata.completeness).toBe('partial');
    const stale = parity({
      ...sources,
      [name]: {
        ...sources[name],
        metadata: metadata(name, {
          freshness: 'stale', completeness: 'partial',
          'as-of': '2026-08-01T00:00:00Z', 'retrieved-at': '2026-08-01T01:00:00Z'
        })
      }
    });
    for (const selected of names) {
      expect(stale[selected].metadata).toMatchObject({
        freshness: 'stale', completeness: 'partial', 'as-of': '2026-08-01T00:00:00Z'
      });
    }
    const withoutMetadata = parity({ ...sources, [name]: { ...sources[name], metadata: undefined } });
    for (const selected of names) expect(withoutMetadata[selected].metadata.completeness).toBe('unknown');
  });

  it('fails closed when the declared workflow source is unavailable', async () => {
    const sources = await canonicalSources();
    const results = workerResults(queries, {
      ...sources, workflows: { ...sources.workflows, metadata: metadata('workflows', { availability: 'unavailable' }) }
    });
    const original = workerResults(baseline, {
      ...sources, workflows: { ...sources.workflows, metadata: metadata('workflows', { availability: 'unavailable' }) }
    });
    for (const name of ['campaign-workflows', 'campaign-workflow-totals']) {
      expect(results[name].rows).toEqual([]);
      expect(results[name].metadata).toMatchObject({
        availability: 'unavailable', 'query-error': { code: 'input-unavailable', source: 'workflows' }
      });
      expect(results[name].metadata['query-diagnostic']).toContain(`$.dashboard.queries[${name}]`);
      expect(results[name].metadata).toEqual(expect.objectContaining({
        ...original[name].metadata, 'query-diagnostic': expect.any(String)
      }));
    }
    expect(results['campaign-inventory']).toEqual(original['campaign-inventory']);
  });

  it('preserves duplicate incomplete-outcome join failures instead of counting unvalidated dispatches', async () => {
    const sources = await canonicalSources();
    const diagnosis = sources.runs.rows.find((row) => row['terminal-outcome'] === 'report_incomplete');
    if (!diagnosis) throw new Error('The canonical fixture must contain an incomplete outcome.');
    const results = parity({
      ...sources,
      runs: {
        ...sources.runs,
        rows: [...sources.runs.rows, { ...diagnosis, id: 'duplicate-incomplete-outcome', 'aic-total': 0 }]
      }
    });
    for (const row of results['campaign-inventory'].rows) expect(row.dispatches).toBe(0);
    expect(results['campaign-inventory'].metadata.completeness).toBe('partial');
    expect(results['campaign-workflow-totals'].rows.find((row) => row.campaign === 'alpha'))
      .toMatchObject({ runs: 8, aic: 5 });
  });

  it('reduces measured production query operations and estimator pressure for every selected query', async () => {
    const sources = await canonicalSources(evidence(12));
    const beforeAnalysis = analyzeDashboardComplexity({ dashboard: { queries: baseline } });
    const afterAnalysis = analyzeDashboardComplexity({ dashboard: { queries } });
    const expected = {
      'campaign-inventory': [88, 257, 74, 88],
      'campaign-workflows': [56, 161, 47, 69],
      'campaign-workflow-totals': [55, 155, 44, 63]
    };
    for (const name of names) {
      const before = beforeAnalysis.inventory.find((query) => query.name === name);
      const after = afterAnalysis.inventory.find((query) => query.name === name);
      expect([
        before?.['total-row-read-units'], before?.['total-materialized-field-units'],
        after?.['total-row-read-units'], after?.['total-materialized-field-units']
      ]).toEqual(expected[/** @type {keyof typeof expected} */ (name)]);
      expect(after?.['total-row-read-units']).toBeLessThan(Number(before?.['total-row-read-units']) * 0.85);
      expect(after?.['total-materialized-field-units']).toBeLessThan(Number(before?.['total-materialized-field-units']) * 0.7);
      const beforeBudget = createDashboardQueryBudget();
      const afterBudget = createDashboardQueryBudget();
      const original = executeDashboardQueries(baseline, sources, [name], { budget: beforeBudget })[name];
      const optimized = executeDashboardQueries(queries, sources, [name], { budget: afterBudget })[name];
      expect(optimized.rows).toEqual(original.rows);
      expect(optimized.rows.length).toBeGreaterThan(0);
      expect(optimized.metadata).toEqual(original.metadata);
      console.info('Campaign query operation reduction', { query: name, before: beforeBudget.operations, after: afterBudget.operations });
      expect(afterBudget.operations).toBeLessThan(beforeBudget.operations * 0.9);
      expect(workerResults(queries, sources, [name])[name].rows).toEqual(optimized.rows);
    }
  }, 30000);

  it('keeps the public workflow inventory schema while isolating lean campaign dependencies', () => {
    const publicQuery = queries.find((query) => query.name === 'workflow-inventory');
    expect(publicQuery?.from).toBe('workflows');
    expect(publicQuery?.joins?.map((join) => join.source)).toEqual(['workflow-run-totals', 'workflow-aic-totals']);
    expect(publicQuery && dashboardQueryOutputFields(publicQuery, (name) => tableFields[name])).toEqual([
      'campaign', 'campaign-name', 'organization', 'repository', 'workflow', 'workflow-name',
      'workflow-label', 'workflow-role', 'rollout-mode', 'workflow-active', 'aic', 'runs',
      'successful-runs', 'failed-runs', 'aic-per-run', 'has-observed-runs', 'ingestion',
      'campaign-link', 'repository-link', 'workflow-link'
    ]);
    for (const name of names) {
      const dependencies = resolveDashboardQuerySources(queries, [name]);
      expect(dependencies).not.toContain('workflow-inventory');
      expect(dependencies).not.toContain('run-import-status');
      for (const source of ['workflows', 'runs', 'audits', 'domains', 'tools', 'issues']) {
        expect(dependencies).toContain(source);
      }
    }
  });
});
