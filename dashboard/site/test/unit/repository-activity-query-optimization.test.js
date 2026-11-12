import 'fake-indexeddb/auto';
import { execFileSync } from 'node:child_process';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { processDataRequest } from '../../src/data-worker.js';
import { queryIndexedDatabaseSources } from '../../src/data/queries/database.js';
import {
  createDashboardQueryBudget,
  executeDashboardQueries,
  resolveDashboardQuerySources
} from '../../src/data/queries/declarative.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { DATABASE_NAME } from '../../src/data/storage/indexeddb.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import runInventoryContract from '../fixtures/repository-run-inventory-contract.json' with { type: 'json' };
import overviewContract from '../fixtures/repository-overview-contract.json' with { type: 'json' };

/** @typedef {import('../../src/presenter.js').LogicalSourceInput} Source */
/** @typedef {import('../../src/data/queries/declarative.js').DashboardQuery} Query */

const dashboard = authoritativeDashboard.dashboard;
/** @type {Query[]} */
const queries = dashboard.queries;
const canonicalNames = ['repositories', 'workflows', 'runs', 'outcomes', 'audits', 'domains', 'tools', 'issues'];
const publicNames = [
  'repository-workflow-totals', 'repository-report-totals',
  'repository-run-totals', 'repository-activity-core', 'repository-activity'
];
/** @type {import('../../src/data/queries/view-payload-compiler.js').GlobalQueryContext[]} */
const contexts = [
  { timeWindow: { start: '2026-09-01T00:00:00Z', end: '2026-09-24T00:00:00Z' } },
  { timeWindow: { start: '2026-09-20T00:00:00Z', end: '2026-09-20T00:00:10Z' } },
  { timeWindow: { start: '2026-09-20T00:00:00Z', end: '2026-09-24T00:00:00Z' } },
  { filters: { organization: ['other'] } },
  { filters: { 'rollout-mode': ['review'], organization: ['org'] } },
  { filters: { repository: ['alpha'] } },
  { filters: { repository: ['org/alpha'] } },
  { filters: { workflow: ['.github/workflows/worker.md'] } },
  { search: { fields: ['repository'], query: 'alpha' } }
];

/** @type {Query} */
const originalRunTotals = {
  name: 'repository-run-totals',
  from: 'run-import-status',
  aggregate: {
    by: ['organization', 'repository'],
    values: [
      { field: 'run', as: 'runs', reducer: 'distinct-count' },
      {
        field: 'run-conclusion', as: 'failed', reducer: 'count',
        filter: { predicates: [{ field: 'run-conclusion', in: ['failure', 'startup-failure', 'timed-out'] }] }
      },
      {
        field: 'run-conclusion', as: 'action-required', reducer: 'count',
        filter: { predicates: [{ field: 'run-conclusion', equals: 'action-required' }] }
      },
      { field: 'imported', as: 'imported-runs', reducer: 'sum' }
    ]
  }
};
/** @type {Query[]} */
const baseline = queries.map((query) => {
  if (query.name === originalRunTotals.name) return originalRunTotals;
  if (query.name === 'repository-aic-totals') return {
    ...query, from: 'workflow-aic-totals', filter: undefined, select: undefined,
    aggregate: { by: ['organization', 'repository'], values: [{ field: 'aic', as: 'aic', reducer: 'sum' }] }
  };
  if (query.name !== 'repository-activity') return query;
  return {
    ...query,
    joins: query.joins?.map((join) => ({ ...join, source: 'repository-aic-totals' }))
  };
});
/** @type {Query[]} */
const chartBaseline = baseline.map((query) => {
  if (query.name === runInventoryContract.source) {
    return { ...query, from: 'repository-activity', joins: undefined, compute: undefined };
  }
  // Compare the former chart's work on the daily projection's admitted horizon.
  if (query.name === 'repository-run-totals') {
    return { ...query, filter: { predicates: [{ field: 'started-at', gte: '2026-09-07T00:00:00Z' }] } };
  }
  return query;
});

/** @param {string} name @param {Record<string, unknown>[]} rows */
function source(name, rows) {
  return {
    source: name, rows,
    metadata: {
      'source-id': name, 'source-kind': 'fixture',
      'as-of': '2026-09-24T00:00:00Z', 'retrieved-at': '2026-09-24T01:00:00Z',
      availability: rows.length ? 'available' : 'empty',
      completeness: 'complete', freshness: 'fresh'
    }
  };
}

function fixture() {
  const repositories = [
    ['org', 'alpha'], ['other', 'alpha'], ['org', 'approval'], ['org', 'running'],
    ['org', 'disabled'], ['org', 'reported'], ['org', 'empty']
  ].map(([organization, repository]) => ({
    organization, repository,
    'repository-link': { href: `https://github.com/${organization}/${repository}`, relation: 'repository' }
  }));
  const workflows = repositories.filter((row) => row.repository !== 'empty').map((row) => ({
    ...row, workflow: '.github/workflows/worker.md',
    'workflow-active': ['disabled', 'alpha', 'running'].includes(row.repository) ? 'false' : 'true',
    'rollout-mode': 'review'
  }));
  /** @param {string} repository @param {string} run @param {string} conclusion @param {number | null} aic */
  const run = (repository, run, conclusion, aic) => ({
    organization: 'org', repository, workflow: '.github/workflows/worker.md',
    run, 'run-attempt': 1, 'run-conclusion': conclusion, 'run-status': 'completed',
    'started-at': '2026-09-20T00:00:00Z', 'aic-total': aic, 'rollout-mode': 'review'
  });
  const runs = [
    run('alpha', '100', 'success', 3),
    run('alpha', '101', 'failure', 2),
    run('alpha', '102', 'startup-failure', 5),
    run('alpha', '103', 'timed-out', 7),
    run('alpha', '104', 'stale', 11),
    run('alpha', '105', 'action-required', 13),
    { ...run('alpha', '106', 'success', 29), organization: 'other' },
    run('approval', '200', 'action-required', null),
    run('running', '300', 'cancelled', 0),
    { ...run('reported', '400', 'success', 17), 'started-at': '2026-09-01T00:00:00Z' }
  ];
  /** @param {string} run @param {string} event @param {string} [type] */
  const record = (run, event, type = 'mcp.tool_call') => ({
    run, 'run-attempt': 1, event, 'event-source': 'fixture', 'event-type': type,
    'event-timestamp': '2026-09-20T00:00:10Z', 'event-summary': 'Retained evidence',
    'safe-output-type': 'create_issue',
    'safe-output-url': type === 'safe_output.result' ? `https://github.com/org/evidence/issues/${run}` : undefined
  });
  return {
    repositories: source('repositories', repositories),
    workflows: source('workflows', workflows),
    runs: source('runs', runs),
    audits: source('audits', [record('100', 'audit-one'), record('100', 'audit-two')]),
    domains: source('domains', [record('101', 'domain-one')]),
    tools: source('tools', [record('102', 'tool-one')]),
    issues: source('issues', [
      record('103', 'issue-one', 'safe_output.result'),
      record('300', 'issue-two', 'safe_output.result'),
      record('400', 'issue-three', 'safe_output.result')
    ])
  };
}

/** @param {Record<string, unknown>} [inputs] */
async function canonicalSources(inputs = fixture()) {
  return /** @type {Record<string, Source>} */ (await processDataRequest({
    operation: 'load-dashboard-query-sources',
    ingest: true, sources: inputs, queries: [], sourceNames: [...canonicalNames, 'daily-run-counts']
  }));
}

/** @param {Query[] | Record<string, unknown>[]} definitions @param {Record<string, Source>} sources @param {string[]} [names] @returns {Record<string, { rows: Record<string, unknown>[], metadata: Source['metadata'] }>} */
function workerQuery(definitions, sources, names = publicNames) {
  if (names.length === 1 && names[0] === runInventoryContract.source) {
    const compiled = chartPayload(/** @type {Query[]} */ (definitions));
    return { [names[0]]: workerQuery(compiled.queries, sources, compiled.aliases)[compiled.aliases[0]] };
  }
  const results = /** @type {Record<string, Source>} */ (processDataRequest({
    operation: 'execute-dashboard-queries', queries: definitions, sources, sourceNames: names
  }));
  return Object.fromEntries(names.map((name) => [name, {
    rows: results[name].rows, metadata: results[name].metadata
  }]));
}

/** @param {Query[]} definitions */
function chartPayload(definitions) {
  const page = dashboard.pages.find((/** @type {{ id: string }} */ value) => value.id === runInventoryContract.page);
  return compileDashboardViewPayloadQueries(page.definition, runInventoryContract.page, {
    queries: definitions, viewId: runInventoryContract.view,
    evaluatedAt: '2026-09-24T00:00:00Z'
  });
}

beforeEach(async () => {
  vi.spyOn(Date, 'now').mockReturnValue(Date.parse('2026-09-24T00:00:00Z'));
  await new Promise((resolve, reject) => {
    const request = indexedDB.deleteDatabase(DATABASE_NAME);
    request.onsuccess = () => resolve(undefined);
    request.onerror = () => reject(request.error);
  });
});

afterEach(() => vi.restoreAllMocks());

describe('repository activity query optimization', () => {
  it.each(contexts)('shares the overview dependency graph without changing scoped values or quality for %j', async (queryContext) => {
    const sources = await canonicalSources();
    sources.audits.metadata = {
      ...sources.audits.metadata, completeness: 'partial', freshness: 'stale',
      'as-of': '2026-09-01T00:00:00Z'
    };
    const page = dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === overviewContract.page);
    expect(page['query-binding']).toBe(overviewContract['query-binding']);
    expect(page.views.map((/** @type {{ id: string }} */ view) => view.id)).toEqual(overviewContract.views);
    const options = {
      queries, queryContext, routeParameters: overviewContract.route,
      sourceNames: [overviewContract.source], evaluatedAt: '2026-09-24T00:00:00Z'
    };
    const combined = compileDashboardViewPayloadQueries(page, overviewContract.page, options);
    const shared = workerQuery(combined.queries, sources, combined.aliases);
    const budget = createDashboardQueryBudget();
    Object.values(executeDashboardQueries(combined.queries, sources, combined.aliases, { budget })).map(result => result.rows);
    let independentOperations = 0;
    let dependencyCount = 0;
    for (const viewId of overviewContract.views) {
      const independent = compileDashboardViewPayloadQueries(page, overviewContract.page, { ...options, viewId });
      expect(shared[independent.aliases[0]]).toEqual(workerQuery(independent.queries, sources, independent.aliases)[independent.aliases[0]]);
      const independentBudget = createDashboardQueryBudget();
      Object.values(executeDashboardQueries(independent.queries, sources, independent.aliases, { budget: independentBudget })).map(result => result.rows);
      independentOperations += independentBudget.operations;
      dependencyCount ||= independent.queries.filter((query) => String(query.name).endsWith(`:dependency:${overviewContract['shared-dependency']}`)).length;
    }
    expect(dependencyCount).toBeGreaterThan(0);
    expect(combined.queries.filter((query) => String(query.name).endsWith(`:dependency:${overviewContract['shared-dependency']}`))).toHaveLength(dependencyCount);
    expect(budget.operations).toBeLessThan(independentOperations * 0.4);
  });

  it('keeps native aggregate inputs row-local and joins only narrow import identities or repository totals', async () => {
    const sources = await canonicalSources();
    const nativeNames = runInventoryContract['native-aggregates'];
    for (const name of nativeNames) {
      const query = queries.find((candidate) => candidate.name === name);
      expect(query?.aggregate).toBeDefined();
      expect(query?.joins?.length ?? 0).toBe(0);
      expect(query?.union?.length ?? 0).toBe(0);
      expect(query?.aggregate?.values.every((value) => ['count', 'distinct-count', 'sum'].includes(value.reducer))).toBe(true);
    }
    const native = await queryIndexedDatabaseSources(indexedDB, sources, queries, nativeNames);
    for (const name of nativeNames) {
      expect(native[name], `no storage-native plan for ${name}`).toBeDefined();
      const expected = workerQuery(queries, sources, [name])[name];
      expect(native[name].rows).toEqual(expected.rows);
      expect(native[name].metadata.availability).not.toBe('unavailable');
    }
    const identities = queries.find((query) => query.name === 'repository-run-identities');
    expect(identities?.select?.map((field) => field.field)).toEqual(runInventoryContract['import-identity-fields']);
    expect(queries.find((query) => query.name === 'repository-run-totals')).toMatchObject({
      from: 'repository-run-statistics',
      joins: [{
        source: 'repository-import-totals',
        on: [{ left: 'organization', right: 'organization' }, { left: 'repository', right: 'repository' }]
      }]
    });
  });

  it('keeps the run chart independent of workflow, outcome, cost, and imported event queries', async () => {
    const compiled = compileDashboardViewPayloadQueries(
      dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === runInventoryContract.page).definition,
      runInventoryContract.page,
      { queries, viewId: runInventoryContract.view }
    );
    for (const { definitions, names } of [
      { definitions: queries, names: [runInventoryContract.source] },
      { definitions: compiled.queries, names: compiled.aliases }
    ]) {
      expect(resolveDashboardQuerySources(definitions, names).filter((name) => [...canonicalNames, 'daily-run-counts'].includes(name)).toSorted())
        .toEqual(runInventoryContract.canonicalSources);
    }
    const inputs = fixture();
    const sources = await canonicalSources(inputs);
    const expected = workerQuery(queries, sources, [runInventoryContract.source])[runInventoryContract.source];
    for (const name of canonicalNames.filter((name) => !runInventoryContract.canonicalSources.includes(name))) {
      sources[name] = {
        ...sources[name], rows: [],
        metadata: { ...sources[name].metadata, availability: 'unavailable', completeness: 'unknown' }
      };
    }
    expect(workerQuery(queries, sources, [runInventoryContract.source])[runInventoryContract.source]).toEqual(expected);
  });

  it.each(contexts.filter((context) => !context.filters?.['rollout-mode']))('preserves run chart rows for supported scopes while eliminating unrelated evidence for %j', async (queryContext) => {
    const sources = await canonicalSources();
    const page = dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === runInventoryContract.page);
    /** @param {Query[]} definitions */
    const scoped = (definitions) => {
      const compiled = compileDashboardViewPayloadQueries(page.definition, runInventoryContract.page, {
        queries: definitions, viewId: runInventoryContract.view,
        queryContext, evaluatedAt: '2026-09-24T00:00:00Z'
      });
      return workerQuery(compiled.queries, sources, compiled.aliases)[compiled.aliases[0]].rows;
    };
    expect(scoped(queries)).toEqual(scoped(chartBaseline));
  });

  it('reduces chart row work by at least two thirds and only reads repositories and daily counts through the worker', async () => {
    const sources = await canonicalSources();
    /** @param {Query[]} definitions */
    const measure = (definitions) => {
      const budget = createDashboardQueryBudget();
      const compiled = chartPayload(definitions);
      const result = executeDashboardQueries(compiled.queries, sources, compiled.aliases, { budget })[compiled.aliases[0]];
      return { rows: result.rows, operations: budget.operations };
    };
    const original = measure(chartBaseline);
    const optimized = measure(queries);
    expect(optimized.rows).toEqual(original.rows);
    expect(optimized.operations).toBeLessThan(original.operations / 3);
    const result = /** @type {Record<string, Source>} */ (await processDataRequest({
      operation: 'load-dashboard-query-sources', sources: {},
      queries: chartPayload(queries).queries, sourceNames: chartPayload(queries).aliases
    }));
    expect(result[chartPayload(queries).aliases[0]].rows).toEqual(optimized.rows);
  });

  it('lists repository run counts and drill links through the production worker boundary', async () => {
    const sources = await canonicalSources();
    const query = queries.find((candidate) => candidate.name === runInventoryContract.source);
    const view = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === runInventoryContract.page)?.definition?.views
      ?.find((/** @type {{ id: string }} */ candidate) => candidate.id === runInventoryContract.view);
    expect(query).toBeDefined();
    expect(view).toMatchObject({
      mark: 'chart', chart: runInventoryContract.chart,
      data: { source: runInventoryContract.source, limit: runInventoryContract.limit },
      encoding: {
        x: { field: 'repository', type: 'nominal' },
        y: { field: 'runs', type: 'quantitative' },
        href: { field: 'repository-link', type: 'nominal' }
      }
    });
    for (const definition of [query, view]) {
      expect(definition?.subject).toBeTruthy();
      expect(definition?.objective).toBeTruthy();
      expect(definition?.acceptance).toBeTruthy();
      expect(Array.from(`${definition?.subject}${definition?.objective}${definition?.acceptance}`).length).toBeLessThanOrEqual(512);
    }
    const result = workerQuery(queries, sources, [runInventoryContract.source])[runInventoryContract.source];
    expect(result.rows.map(({ repository, runs }) => ({ repository, runs }))).toEqual(runInventoryContract.rows);
    for (const row of result.rows) {
      expect(row['repository-link']).toMatchObject({
        'dashboard-href': `#page-repository-detail?repository=${encodeURIComponent(String(row.repository))}`
      });
    }
  });

  it('counts distinct run IDs across attempts within the selected horizon and organization', async () => {
    const inputs = fixture();
    inputs.runs.rows.push({ ...inputs.runs.rows[0], 'run-attempt': 2 });
    const sources = await canonicalSources(inputs);
    const page = dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === runInventoryContract.page);
    const compiled = compileDashboardViewPayloadQueries(page.definition, runInventoryContract.page, {
      queries, viewId: runInventoryContract.view,
      queryContext: {
        timeWindow: { start: '2026-09-20T00:00:00Z', end: '2026-09-24T00:00:00Z' },
        filters: { organization: ['org'] }
      },
      evaluatedAt: '2026-09-24T00:00:00Z'
    });
    const result = workerQuery(compiled.queries, sources, compiled.aliases)[compiled.aliases[0]];
    expect(result.rows).toHaveLength(6);
    expect(result.rows[0]).toMatchObject({ repository: 'org/alpha', runs: 6 });
    expect(result.rows).toContainEqual(expect.objectContaining({ repository: 'org/reported', runs: 0 }));
    expect(result.rows.some((row) => row.repository === 'other/alpha')).toBe(false);
  });

  it('bounds the chart query to 100 repositories without truncating the full Table inventory', async () => {
    const inputs = fixture();
    inputs.repositories.rows = Array.from({ length: 101 }, (_, index) => {
      const repository = `extra-${String.fromCharCode(97 + Math.floor(index / 26), 97 + index % 26)}`;
      return {
        organization: 'org', repository,
        'repository-link': { href: `https://github.com/org/${repository}`, relation: 'repository' }
      };
    });
    const sources = await canonicalSources(inputs);
    expect(workerQuery(queries, sources, ['repository-activity'])['repository-activity'].rows).toHaveLength(101);
    expect(workerQuery(queries, sources, [runInventoryContract.source])[runInventoryContract.source].rows).toHaveLength(runInventoryContract.limit);
    const page = dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === runInventoryContract.page);
    const compiled = compileDashboardViewPayloadQueries(page.definition, runInventoryContract.page, {
      queries, viewId: runInventoryContract.view
    });
    const result = workerQuery(compiled.queries, sources, compiled.aliases)[compiled.aliases[0]];
    expect(result.rows).toHaveLength(runInventoryContract.limit);
    expect(result.rows[0]).toMatchObject({ repository: 'org/extra-aa', runs: 0 });
    expect(result.rows.at(-1)).toMatchObject({ repository: 'org/extra-dv', runs: 0 });
  });

  it('preserves empty inventory and rejects unavailable daily evidence without false zeros', async () => {
    const sources = await canonicalSources();
    sources['daily-run-counts'] = { ...sources['daily-run-counts'], rows: [], metadata: { ...sources['daily-run-counts'].metadata, availability: 'unavailable' } };
    const result = workerQuery(queries, sources, [runInventoryContract.source])[runInventoryContract.source];
    expect(result.rows).toEqual([]);
    expect(result.metadata.availability).toBe('unavailable');
    sources.repositories = { ...sources.repositories, rows: [], metadata: { ...sources.repositories.metadata, availability: 'empty' } };
    expect(workerQuery(queries, sources, [runInventoryContract.source])[runInventoryContract.source].rows).toEqual([]);
  });

  it('projects repository identities and drill links without loading activity tables before enrichment', async () => {
    const sources = await canonicalSources();
    const view = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === 'repositories')?.definition?.views
      ?.find((/** @type {{ id: string }} */ candidate) => candidate.id === 'repositories-activity');
    expect(view?.data).toMatchObject({
      source: 'repository-activity',
      'partial-source': 'repository-activity-preview'
    });
    const page = { views: [view] };
    const preview = compileDashboardViewPayloadQueries(page, 'repositories', {
      queries,
      viewId: 'repositories-activity',
      sourceNames: ['repository-activity'],
      partial: true
    });
    const alias = preview.aliases[0];
    expect(resolveDashboardQuerySources([...queries, ...preview.queries], [alias]).filter((name) => name !== alias))
      .toEqual(['repositories']);
    const rows = workerQuery([...queries, ...preview.queries], sources, [alias])[alias].rows;
    expect(rows).toHaveLength(sources.repositories.rows.length);
    expect(rows[0]).toMatchObject({
      repository: 'org/alpha',
      'repository-link': { 'dashboard-href': '#page-repository-detail?repository=org%2Falpha' }
    });
    expect(rows[0]).not.toHaveProperty('runs');
    expect(rows[0]).not.toHaveProperty('status');
  });

  it('preserves every existing repository output and metadata through canonical ingestion and the worker boundary', async () => {
    const sources = await canonicalSources();
    const optimized = workerQuery(queries, sources);
    expect(optimized).toEqual(workerQuery(baseline, sources));
    expect(optimized['repository-run-totals'].rows).toContainEqual({
      organization: 'org', repository: 'alpha', runs: 6, failed: 3, 'action-required': 1, 'imported-runs': 4
    });
    const rows = optimized['repository-activity'].rows;
    expect(rows.map((row) => row.repository)).toEqual([
      'org/alpha', 'org/approval', 'org/reported', 'org/running', 'other/alpha', 'org/disabled', 'org/empty'
    ]);
    expect(rows[0]).toMatchObject({
      repository: 'org/alpha', 'repository-name': 'alpha', workflows: 1, runs: 6,
      reports: 1, failed: 3, aic: 41, ingestion: '66.7%', 'failure-summary': '50% · 3 failed',
      status: 'Needs attention',
      'repository-link': {
        href: 'https://github.com/org/alpha',
        'dashboard-href': '#page-repository-detail?repository=org%2Falpha',
        'dashboard-label': 'View org/alpha repository dashboard'
      }
    });
    expect(rows.map((row) => row.status)).toEqual([
      'Needs attention', 'Approval required', 'No failures observed', 'No failures observed',
      'No failures observed', 'Disabled workflows', 'No recent activity'
    ]);
    expect(rows.at(-1)).toMatchObject({
      workflows: 0, runs: 0, reports: null, aic: 0, ingestion: null, 'failure-summary': '—'
    });
  });

  it('preserves weakest quality and oldest provenance for optional imported evidence', async () => {
    const sources = await canonicalSources();
    sources.audits.metadata = {
      ...sources.audits.metadata, completeness: 'partial', freshness: 'stale',
      'as-of': '2026-09-01T00:00:00Z', 'retrieved-at': '2026-09-02T00:00:00Z'
    };
    const result = workerQuery(queries, sources);
    expect(result).toEqual(workerQuery(baseline, sources));
    expect(result['repository-activity'].metadata).toMatchObject({
      completeness: 'partial', freshness: 'stale',
      'as-of': '2026-09-01T00:00:00Z', 'retrieved-at': '2026-09-02T00:00:00Z'
    });
  });

  it.each(canonicalNames)('preserves rows and quality when canonical %s is unavailable or missing', async (name) => {
    const sources = await canonicalSources();
    sources[name] = {
      ...sources[name], rows: [],
      metadata: { ...sources[name].metadata, availability: 'unavailable', completeness: 'unknown' }
    };
    for (const missing of [false, true]) {
      if (missing) delete sources[name];
      const optimized = workerQuery(queries, sources, ['repository-activity'])['repository-activity'];
      const original = workerQuery(baseline, sources, ['repository-activity'])['repository-activity'];
      expect(optimized.rows).toEqual(original.rows);
      expect(optimized.metadata).toEqual(original.metadata);
    }
  });

  it('retains AIC and explicit partial quality when every import branch is unavailable', async () => {
    const sources = await canonicalSources();
    for (const name of ['audits', 'domains', 'tools', 'issues']) {
      sources[name] = {
        ...sources[name], rows: [],
        metadata: { ...sources[name].metadata, availability: 'unavailable' }
      };
    }
    const result = workerQuery(queries, sources);
    expect(result).toEqual(workerQuery(baseline, sources));
    expect(result['repository-activity'].rows[0]).toMatchObject({ aic: 41, ingestion: '0%' });
    expect(result['repository-activity'].metadata).toMatchObject({ availability: 'available', completeness: 'partial' });
  });

  it('preserves an empty canonical inventory without claiming availability', async () => {
    const inputs = Object.fromEntries(Object.entries(fixture()).map(([name]) => [name, source(name, [])]));
    const sources = await canonicalSources(inputs);
    const result = workerQuery(queries, sources);
    expect(result).toEqual(workerQuery(baseline, sources));
    expect(result['repository-activity']).toMatchObject({ rows: [], metadata: { availability: 'empty' } });
  });

  it.each(contexts)('preserves scoped view payloads for %j', async (queryContext) => {
    const sources = await canonicalSources();
    const page = { views: [{ id: 'activity', mark: 'table', data: { source: 'repository-activity' } }] };
    /** @param {Query[]} definitions */
    const scoped = (definitions) => {
      const payload = compileDashboardViewPayloadQueries(page, 'repositories', {
        queries: definitions, queryContext, evaluatedAt: '2026-09-24T00:00:00Z'
      });
      return workerQuery(payload.queries, sources, payload.aliases);
    };
    const result = scoped(queries);
    expect(result).toEqual(scoped(baseline));
    for (const value of Object.values(result)) expect(value.metadata.availability).not.toBe('unavailable');
    if (queryContext.filters?.organization?.[0] === 'other') {
      expect(Object.values(result)[0].rows.map((row) => row.repository)).toEqual(['other/alpha']);
    }
    if (queryContext.timeWindow?.start === '2026-09-20T00:00:00Z'
        && queryContext.timeWindow.end === '2026-09-24T00:00:00Z') {
      expect(Object.values(result)[0].rows).toContainEqual(expect.objectContaining({
        repository: 'org/reported', runs: 0, reports: 1, status: 'Outcomes observed'
      }));
    }
  });

  it('reduces materialized fields without narrowing canonical evidence or hiding extra row work', async () => {
    const sources = await canonicalSources();
    const originalInputs = resolveDashboardQuerySources(baseline, ['repository-activity']).filter((name) => canonicalNames.includes(name));
    const optimizedInputs = resolveDashboardQuerySources(queries, ['repository-activity']).filter((name) => canonicalNames.includes(name));
    expect(optimizedInputs.toSorted()).toEqual(originalInputs.toSorted());
    /** @param {Query[]} definitions */
    const measure = (definitions) => {
      const budget = createDashboardQueryBudget();
      const result = executeDashboardQueries(definitions, sources, ['repository-activity'], { budget })['repository-activity'];
      return { rows: result.rows, operations: budget.operations };
    };
    const original = measure(baseline);
    const optimized = measure(queries);
    expect(optimized.rows).toEqual(original.rows);
    expect(optimized.operations - original.operations).toBe(19);

    const estimates = JSON.parse(execFileSync(process.execPath, ['--input-type=module', '-e', `
      import { readFileSync } from 'node:fs';
      import { analyzeDashboardComplexity } from '../../dashboard/report/query-complexity.mjs';
      const definitions = JSON.parse(readFileSync(0, 'utf8'));
      console.log(JSON.stringify(definitions.map((queries) =>
        analyzeDashboardComplexity({ dashboard: { queries } }).inventory.find((query) => query.name === 'repository-activity')
      )));
    `], { input: JSON.stringify([baseline, queries]), encoding: 'utf8' }));
    expect(estimates[0]).toMatchObject({ 'total-row-read-units': 63, 'total-materialized-field-units': 166 });
    expect(estimates[1]['total-materialized-field-units']).toBeLessThan(estimates[0]['total-materialized-field-units']);
  });
});
