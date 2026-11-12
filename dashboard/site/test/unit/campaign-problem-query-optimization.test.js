import 'fake-indexeddb/auto';
import { execFileSync } from 'node:child_process';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import { authoritativeDashboard } from '../authoritative-dashboard.js';

const dashboard = authoritativeDashboard.dashboard;
const queries = dashboard.queries;
const selected = [
  'campaign-problem-tab-counts',
  'campaign-problem-items',
  'campaign-current-problem-error-groups',
  'campaign-problem-error-groups',
  'campaign-problem-latest-target-runs',
  'campaign-problem-run-evidence'
];
const privateQueries = new Set([
  'campaign-problem-runtime-failures',
  'campaign-problem-runtime-partitions'
]);

// Retain the retired runtime queries only in the pre-optimization baseline.
// Shared dependencies still execute from the production document in both graphs.
const retiredRuntimeQueries = [
  {
    name: 'campaign-runtime-problem-candidates',
    from: 'campaign-runtime-health-groups',
    compute: [
      { as: 'has-current-failure', function: 'greater-than', args: [{ field: 'failure-count' }, { value: 0 }] },
      { as: 'problem-kind', function: 'coalesce', args: [{ value: 'failure' }, { value: 'failure' }] }
    ]
  },
  {
    name: 'campaign-current-runtime-failures',
    from: 'campaign-runtime-problem-candidates',
    filter: { predicates: [{ field: 'has-current-failure', equals: true }] },
    'order-by': [
      { field: 'failure-count', direction: 'desc' },
      { field: 'workflow-name', direction: 'asc' },
      { field: 'runtime-repository', direction: 'asc' }
    ]
  },
  {
    name: 'campaign-runtime-problems',
    from: 'campaign-current-runtime-failures',
    union: ['campaign-unobserved-orchestrators'],
    'order-by': [
      { field: 'failure-count', direction: 'desc' },
      { field: 'workflow-name', direction: 'asc' },
      { field: 'runtime-repository', direction: 'asc' }
    ]
  }
];
const baselineQueries = queries
  .flatMap((/** @type {{ name: string }} */ query) => {
    if (query.name === 'workflow-run-observation-counts') return [...retiredRuntimeQueries.slice(0, 2), query];
    if (query.name === 'dispatches') return [retiredRuntimeQueries[2], query];
    return [query];
  })
  .filter((/** @type {{ name: string }} */ query) => !privateQueries.has(query.name))
  .map((/** @type {import('../../src/data/queries/declarative.js').DashboardQuery & { subject: string, objective?: string, acceptance?: string }} */ query) => {
    if (query.name === 'campaign-problem-runs') {
      return {
        ...query,
        'order-by': [
          { field: 'started-at', direction: 'desc' },
          { field: 'run', direction: 'desc' }
        ]
      };
    }
    if (query.name === 'campaign-problem-run-evidence') {
      return {
        ...query,
        from: 'campaign-problem-runs',
        'order-by': undefined,
        joins: [{
          source: 'campaign-runtime-problems',
          type: 'inner',
          on: [
            { left: 'campaign', right: 'campaign' },
            { left: 'workflow', right: 'workflow' },
            { left: 'runtime-repository', right: 'runtime-repository' }
          ],
          fields: [
            { field: 'failure-count', as: 'failure-count' },
            { field: 'problem-kind', as: 'problem-kind' }
          ]
        }]
      };
    }
    if (query.name === 'campaign-problem-tab-counts') {
      return {
        name: query.name,
        subject: query.subject,
        objective: query.objective,
        acceptance: query.acceptance,
        description: query.description,
        from: 'campaign-problem-items',
        aggregate: {
          by: ['campaign'],
          values: [{ field: 'campaign', as: 'items', reducer: 'count' }]
        }
      };
    }
    return query;
  });

/** @type {typeof import('../../src/data-worker.js').processDataRequest} */
let processDataRequest;
/** @type {typeof import('../../src/data/queries/view-payload-compiler.js').compileDashboardViewPayloadQueries} */
let compileDashboardViewPayloadQueries;
/** @type {string} */
let databaseName;
/** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */
let canonical;

beforeAll(async () => {
  window.history.replaceState(null, '', '/?debug=data:query');
  vi.resetModules();
  ({ processDataRequest } = await import('../../src/data-worker.js'));
  ({ compileDashboardViewPayloadQueries } = await import('../../src/data/queries/view-payload-compiler.js'));
  ({ DATABASE_NAME: databaseName } = await import('../../src/data/storage/indexeddb.js'));
});

function evidence() {
  const metadata = {
    'as-of': '2026-09-30T12:00:00Z',
    'retrieved-at': '2026-09-30T12:00:00Z',
    'artifact-generation': 'campaign-problem-optimization'
  };
  const workflows = ['alpha', 'beta'].flatMap((campaign, campaignIndex) => (
    ['worker', 'healthy', 'unobserved'].map((role) => ({
      organization: 'octo',
      repository: `control-${campaignIndex}`,
      workflow: `.github/workflows/${role}.md`,
      campaign,
      'campaign-name': campaign,
      'workflow-name': `${campaign} ${role}`,
      'workflow-role': role === 'unobserved' ? 'orchestrator' : 'worker',
      'workflow-active': 'true',
      'rollout-mode': campaignIndex === 0 ? 'review' : 'live'
    }))
  ));
  const runs = workflows.filter((workflow) => workflow['workflow-role'] === 'worker').flatMap((workflow, index) => (
    Array.from({ length: 32 }, (_, attempt) => {
      const run = String(1000 + index * 100 + attempt);
      return {
        ...workflow,
        run,
        'run-attempt': 1,
        'run-status': attempt === 31 && index % 2 === 0 ? 'in-progress' : 'completed',
        'run-conclusion': index % 2 === 1 && attempt === 31 ? 'success'
          : ['failure', 'startup-failure', 'stale', 'timed-out'][attempt % 4],
        'started-at': `2026-09-${attempt < 16 ? '24' : '29'}T${String(attempt % 16).padStart(2, '0')}:00:00Z`,
        'target-repository': `octo/target-${attempt % 3}`,
        'failure-kind': attempt % 3 === 0 ? 'driver_exit' : 'agent_logic',
        'failure-message': `Retained failure ${run}`,
        'failure-step': 'agent',
        'failure-log': `Diagnostic ${run}`,
        'terminal-outcome': index === 0 && attempt === 30 ? 'report_incomplete' : undefined,
        'terminal-outcome-detail': index === 0 && attempt === 30 ? 'Required target evidence is unavailable.' : undefined,
        engine: 'copilot',
        'engine-version': '1.2.3',
        'gh-aw-version': 'v0.89.4',
        'requested-model': 'requested',
        'resolved-model': 'resolved',
        'run-link': { relation: 'run', href: `https://github.com/octo/${workflow.repository}/actions/runs/${run}` }
      };
    })
  ));
  const audits = [{
    ...runs[30],
    event: 'incomplete-report',
    'event-timestamp': runs[30]['started-at'],
    'event-source': 'mcp',
    'event-type': 'safe_output.result',
    'safe-output-type': 'report_incomplete',
    'event-summary': 'Required target evidence is unavailable.',
    'observed-at': runs[30]['started-at']
  }];
  return {
    campaigns: { metadata, rows: ['alpha', 'beta'].map((campaign) => ({ campaign, 'campaign-name': campaign })) },
    repositories: { metadata, rows: [0, 1].map((index) => ({ organization: 'octo', repository: `control-${index}` })) },
    workflows: { metadata, rows: workflows },
    runs: { metadata, rows: runs },
    audits: { metadata, rows: audits },
    tools: { metadata, rows: [] }
  };
}

beforeEach(async () => {
  vi.spyOn(Date, 'now').mockReturnValue(Date.parse('2026-09-30T12:00:00Z'));
  vi.spyOn(console, 'debug').mockImplementation(() => {});
  await new Promise((resolve, reject) => {
    const request = indexedDB.deleteDatabase(databaseName);
    request.onsuccess = () => resolve(undefined);
    request.onerror = () => reject(request.error);
  });
  const loaded = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
    await processDataRequest({
      operation: 'load-dashboard-query-sources',
      sources: evidence(),
      queries,
      sourceNames: ['workflows', 'overview-runs', 'runs', 'audits', 'tools'],
      ingest: true
    })
  );
  canonical = loaded;
});

afterEach(() => {
  vi.restoreAllMocks();
});

/**
 * @param {unknown[]} definitions
 * @param {string[]} [sourceNames]
 * @param {typeof canonical} [sources]
 */
function execute(definitions, sourceNames = selected, sources = canonical) {
  const debug = vi.mocked(console.debug);
  debug.mockClear();
  const result = /** @type {typeof canonical} */ (processDataRequest({
    operation: 'execute-dashboard-queries', queries: definitions, sourceNames, sources
  }));
  const outputs = Object.fromEntries(sourceNames.map((name) => [name, {
    source: result[name].source,
    rows: result[name].rows,
    metadata: result[name].metadata
  }]));
  const stages = debug.mock.calls.flatMap(([category, event, record]) => (
    category === '[cao:data:query]' && event === 'stage' ? [record] : []
  ));
  const operations = stages.reduce((total, record) => total + record.operations, 0);
  return { outputs, operations, stages };
}

describe('campaign problem query optimization', () => {
  it('preserves all six complete outputs and metadata through canonical ingestion and the worker', () => {
    const before = execute(baselineQueries);
    const after = execute(queries);
    expect(after.outputs).toEqual(before.outputs);
    expect(after.outputs['campaign-problem-items'].rows.length).toBeGreaterThan(0);
    expect(after.outputs['campaign-problem-run-evidence'].rows.length).toBeGreaterThan(32);
    expect(after.outputs['campaign-problem-items'].rows).toEqual(expect.arrayContaining([
      expect.objectContaining({
        'error-signature': 'incomplete_evidence',
        'status-detail': 'Required target evidence is unavailable.'
      })
    ]));
    expect(after.operations).toBeLessThan(before.operations);
    const originalSort = before.stages.find((stage) => stage.query === 'campaign-problem-runs' && stage.stage === 'order-by');
    const optimizedSort = after.stages.find((stage) => stage.query === 'campaign-problem-run-evidence' && stage.stage === 'order-by');
    expect(originalSort).toBeDefined();
    expect(optimizedSort).toBeDefined();
    expect(optimizedSort.inputRows).toBeLessThan(originalSort.inputRows);
    expect(after.stages.some((stage) => [
      'campaign-current-runtime-failures', 'campaign-runtime-problems'
    ].includes(stage.query))).toBe(false);
    console.info('campaign problems combined worker operations', { before: before.operations, after: after.operations });
  });

  it.each(selected)('reduces measured production worker operations for %s', (name) => {
    const before = execute(baselineQueries, [name]);
    const after = execute(queries, [name]);
    expect(after.outputs).toEqual(before.outputs);
    expect(after.operations).toBeGreaterThan(0);
    expect(after.operations).toBeLessThan(before.operations);
    console.info(name, { before: before.operations, after: after.operations });
  });

  it('preserves selected-horizon and route/filter semantics before dependent aggregates', () => {
    const page = {
      id: 'problem-contract',
      kind: 'custom',
      views: selected.map((name) => ({
        id: name,
        mark: 'table',
        data: { source: name, arguments: [{ name: 'campaign', field: 'campaign' }] }
      }))
    };
    const options = {
      routeParameters: { campaign: 'alpha' },
      queryContext: {
        filters: { campaign: ['alpha'] },
        timeWindow: { start: '2026-09-29T00:00:00Z', end: '2026-09-30T00:00:00Z' }
      }
    };
    const before = compileDashboardViewPayloadQueries(page, page.id, { ...options, queries: baselineQueries });
    const after = compileDashboardViewPayloadQueries(page, page.id, { ...options, queries });
    const original = execute([...baselineQueries, ...before.queries], before.aliases);
    const optimized = execute([...queries, ...after.queries], after.aliases);
    expect(optimized.outputs).toEqual(original.outputs);
    expect(optimized.operations).toBeLessThan(original.operations);
    for (const output of Object.values(optimized.outputs)) {
      expect(output.rows.length).toBeGreaterThan(0);
      expect(output.rows.every((row) => row.campaign === 'alpha')).toBe(true);
    }
    const missing = compileDashboardViewPayloadQueries(page, page.id, { queries });
    for (const output of Object.values(execute([...queries, ...missing.queries], missing.aliases).outputs)) {
      expect(output.rows).toEqual([]);
    }
    const emptyWindow = {
      ...options,
      queryContext: {
        timeWindow: { start: '2026-09-01T00:00:00Z', end: '2026-09-02T00:00:00Z' }
      }
    };
    const emptyBefore = compileDashboardViewPayloadQueries(page, page.id, { ...emptyWindow, queries: baselineQueries });
    const emptyAfter = compileDashboardViewPayloadQueries(page, page.id, { ...emptyWindow, queries });
    const originalEmpty = execute([...baselineQueries, ...emptyBefore.queries], emptyBefore.aliases);
    const optimizedEmpty = execute([...queries, ...emptyAfter.queries], emptyAfter.aliases);
    expect(optimizedEmpty.outputs).toEqual(originalEmpty.outputs);
    for (const output of Object.values(optimizedEmpty.outputs)) {
      expect(output.rows).toEqual([]);
      expect(output.metadata?.availability).toBe('empty');
    }
  });

  it('preserves partial/stale canonical evidence and missing enrichment semantics', () => {
    /** @type {typeof canonical} */
    const sources = {
      ...canonical,
      runs: {
        ...canonical.runs,
        metadata: { ...canonical.runs.metadata, completeness: 'partial', freshness: 'stale' }
      }
    };
    const before = execute(baselineQueries, selected, sources);
    const after = execute(queries, selected, sources);
    expect(after.outputs).toEqual(before.outputs);
    for (const output of Object.values(after.outputs)) {
      expect(output.metadata).toMatchObject({ completeness: 'partial', freshness: 'stale' });
    }
  });

  it.each(['workflows', 'overview-runs', 'runs', 'audits'])('fails or degrades identically when canonical %s is unavailable', (name) => {
    /** @type {typeof canonical} */
    const sources = {
      ...canonical,
      [name]: {
        ...canonical[name],
        rows: [],
        metadata: { ...canonical[name].metadata, availability: 'unavailable' }
      }
    };
    const before = execute(baselineQueries, selected, sources);
    const after = execute(queries, selected, sources);
    for (const query of selected) {
      expect(after.outputs[query].rows).toEqual(before.outputs[query].rows);
      expect(after.outputs[query].metadata).toMatchObject({
        availability: before.outputs[query].metadata?.availability,
        completeness: before.outputs[query].metadata?.completeness,
        freshness: before.outputs[query].metadata?.freshness,
        'as-of': before.outputs[query].metadata?.['as-of'],
        'retrieved-at': before.outputs[query].metadata?.['retrieved-at']
      });
      if (before.outputs[query].metadata?.availability === 'unavailable') {
        expect(after.outputs[query].metadata?.['query-error']).toBeDefined();
      }
    }
  });

  it('retains duplicate run join-key failures rather than counting ambiguous representatives', async () => {
    const database = await new Promise((resolve, reject) => {
      const request = indexedDB.open(databaseName);
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
    await new Promise((resolve, reject) => {
      const transaction = database.transaction('runs', 'readwrite');
      const store = transaction.objectStore('runs');
      const request = store.getAll();
      request.onsuccess = () => {
        const run = request.result.find((/** @type {{ githubRunId: unknown }} */ row) => String(row.githubRunId) === '1000');
        expect(run).toBeDefined();
        store.put({ ...run, id: `${run.id}:duplicate`, attempt: 2 });
      };
      transaction.oncomplete = () => resolve(undefined);
      transaction.onerror = () => reject(transaction.error);
    });
    database.close();
    const loaded = /** @type {typeof canonical} */ (await processDataRequest({
      operation: 'load-dashboard-query-sources',
      sources: evidence(),
      queries,
      sourceNames: selected
    }));
    const queryNames = new Set(queries.map((/** @type {{ name: string }} */ query) => query.name));
    const sources = Object.fromEntries(Object.keys(loaded)
      .filter((name) => !queryNames.has(name)).map((name) => [name, loaded[name]]));
    const before = execute(baselineQueries, ['campaign-problem-items', 'campaign-problem-tab-counts'], sources);
    const after = execute(queries, ['campaign-problem-items', 'campaign-problem-tab-counts'], sources);
    for (const name of ['campaign-problem-items', 'campaign-problem-tab-counts']) {
      expect(before.outputs[name].metadata?.availability).toBe('unavailable');
      expect(after.outputs[name].rows).toEqual([]);
      expect(after.outputs[name].metadata?.availability).toBe('unavailable');
      expect(after.outputs[name].metadata).toEqual(before.outputs[name].metadata);
    }
  });

  it('reduces estimated reads and materialized fields for every selected query without changing output schemas', () => {
    /** @param {unknown[]} definitions */
    const estimates = (definitions) => JSON.parse(execFileSync(process.execPath, ['--input-type=module', '-e', `
      import { readFileSync } from 'node:fs';
      import { analyzeDashboardComplexity } from '../../dashboard/report/query-complexity.mjs';
      console.log(JSON.stringify(analyzeDashboardComplexity(JSON.parse(readFileSync(0, 'utf8'))).inventory));
    `], {
      input: JSON.stringify({ ...authoritativeDashboard, dashboard: { ...dashboard, queries: definitions } }),
      encoding: 'utf8'
    }));
    const before = estimates(baselineQueries);
    const after = estimates(queries);
    for (const name of selected) {
      const original = before.find((/** @type {{ name: string }} */ query) => query.name === name);
      const optimized = after.find((/** @type {{ name: string }} */ query) => query.name === name);
      expect(optimized['output-field-count']).toBe(original['output-field-count']);
      expect(optimized['total-row-read-units']).toBeLessThan(original['total-row-read-units']);
      expect(optimized['total-materialized-field-units']).toBeLessThan(original['total-materialized-field-units']);
    }
  });
});
