import 'fake-indexeddb/auto';
import { IDBFactory } from 'fake-indexeddb';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { parseUnifiedSessionDocument } from '../../src/data/adapters/unified-session.js';
import { ingestCachedGhAwJsonl } from '../../src/data/ingest/coordinator.js';
import { processDataRequest } from '../../src/data-worker.js';
import { executeDashboardQueries, resolveDashboardQuerySources } from '../../src/data/queries/declarative.js';
import { queryDatabaseSources } from '../../src/data/queries/database.js';
import { compileDashboardViewPayloadQueries, dashboardFormDefaultValues, resolveDashboardQueryParameters } from '../../src/data/queries/view-payload-compiler.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import contract from '../fixtures/model-routing-contract.json' with { type: 'json' };

const dashboard = authoritativeDashboard.dashboard;
const page = dashboard.pages.find((/** @type {{ id: string }} */ value) => value.id === contract.detail);
const required = new Set(resolveDashboardQuerySources(dashboard.queries, page.views.map((/** @type {{ data: { source: string } }} */ view) => view.data.source)));
const now = Date.parse('2026-10-18T12:00:00Z');
const usage = { inputTokens: 10, outputTokens: 2, cacheReadInputTokens: 0, cacheCreationInputTokens: 0 };

/** @param {string} type @param {Record<string, unknown>} data @param {number} [clock] @param {string} [phase] */
function event(type, data, clock = now, phase = 'agent') {
  return { type, data, provenance: { phase, timestampMs: clock, component: 'awf' } };
}

/** @param {IDBFactory} indexedDB @param {number} run @param {Record<string, unknown>[]} events @param {number} [attempt] */
async function ingest(indexedDB, run, events, attempt = 1) {
  const document = parseUnifiedSessionDocument([
    { type: 'session.format', data: { version: 1 } },
    event('workflow.info', { repository: 'octo/api', runId: run, engineId: 'copilot' }),
    ...events
  ].map((row) => JSON.stringify(row)).join('\n'), 'usage/aw_session.jsonl', { runId: String(run), attempt });
  await ingestCachedGhAwJsonl(indexedDB, JSON.stringify({
    schema_version: 5, kind: 'run',
    run: { run_id: run, run_attempt: attempt, organization: 'octo', repository: 'octo/api',
      workflow_name: 'Worker', workflow_path: '.github/workflows/worker.lock.yml',
      created_at: new Date(now).toISOString(), updated_at: new Date(now).toISOString(),
      status: 'completed', conclusion: 'success',
      cao_unified_session: { version: 1, path: 'usage/aw_session.jsonl', ...document } }
  }), { now });
}

/** @param {Record<string, string|number|boolean>} [values] */
function definitions(values = {}) {
  return /** @type {Array<Record<string, unknown>>} */ (resolveDashboardQueryParameters(
    dashboard.queries.filter((/** @type {{ name: string }} */ query) => required.has(query.name)),
    { ...dashboardFormDefaultValues(page.form), ...values }));
}

/** @param {IDBFactory} indexedDB @param {string} run @param {string} viewId @param {Record<string, string|number|boolean>} [values] */
async function viewResult(indexedDB, run, viewId, values = {}) {
  vi.stubGlobal('indexedDB', indexedDB);
  const queries = definitions(values);
  const view = page.views.find((/** @type {{ id: string }} */ value) => value.id === viewId);
  const payload = compileDashboardViewPayloadQueries(page, page.id, {
    queries, viewId, sourceNames: [view.data.source], routeParameters: { 'routing-run': run }
  });
  const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
    operation: 'load-dashboard-query-sources', sources: {}, queries: [...queries, ...payload.queries], sourceNames: payload.aliases
  }));
  expect(Object.keys(result)).toEqual(payload.aliases);
  expect(result[payload.aliases[0]].metadata.availability).not.toBe('unavailable');
  return result[payload.aliases[0]];
}

describe('run-scoped model routing', () => {
  afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

  it('matches late-scoped canonical outputs for complete, partial, empty and missing runs', async () => {
    const indexedDB = new IDBFactory();
    const receipt = event('session.collection', {
      sources: [{ path: 'firewall/agent/token-usage.jsonl', phase: 'agent', component: 'firewall', events: 4 }]
    }, now, 'conclusion');
    await ingest(indexedDB, 1, [
      receipt,
      event('firewall.model_routing', { stage: 'selection', selectedModel: 'old', selectedEffort: 'low' }),
      event('firewall.model_routing', { stage: 'selection', selectedModel: 'gpt-5', selectedEffort: 'high' }, now + 1),
      event('subagent.request', { requestId: 'exact', agentName: 'exact-owner', model: 'gpt-5', usage }),
      event('subagent.request', { requestId: 'engine-a', agentName: 'first-owner', model: 'gpt-5', usage }),
      event('subagent.request', { requestId: 'engine-b', agentName: 'second-owner', model: 'gpt-5', usage }),
      event('firewall.model_routing', { stage: 'request', requestId: 'exact', routed: 'as_selected' }),
      event('firewall.token_usage', { requestId: 'exact', model: 'gpt-5', usage, aic: 0 }),
      event('firewall.token_usage', { requestId: 'proxy-a', model: 'gpt-5', usage, aic: 2 }),
      event('firewall.token_usage', { requestId: 'proxy-b', model: 'gpt-5', usage, aic: 3 }),
      event('firewall.token_usage', { requestId: 'classifier', purpose: 'routing_classification', aic: 1 }),
      event('usage.report', { requestId: 'classifier', purpose: 'routing_classification', aic: 900 }),
      event('usage.report', { requestId: 'detection', aic: 2 }, now, 'detection'),
      event('usage.report', { requestId: 'eval', aic: 3 }, now, 'evals'),
      ...['main', 'reviewer'].map(role => event('model.resolution', {
        engine: 'copilot', role, configured_model: 'small', resolved_model: 'gpt-5', status: 'resolved'
      }))
    ]);
    await ingest(indexedDB, 2, [receipt, event('firewall.token_usage', { requestId: 'unknown' })]);
    await ingest(indexedDB, 3, []);
    await ingest(indexedDB, 4, [
      event('firewall.model_routing', { stage: 'selection', selectedModel: 'unrelated' }),
      event('subagent.request', { requestId: 'engine-a', agentName: 'wrong-owner', model: 'gpt-5', usage }),
      event('firewall.token_usage', { requestId: 'proxy-a', model: 'gpt-5', usage, aic: 999 })
    ]);
    const canonical = await queryDatabaseSources(indexedDB, {}, ['audits', 'routing-aggregates']);
    for (const run of [1, 2, 3, 999]) {
      for (const view of page.views) {
        const expectedName = 'late-scoped-expected';
        const expected = executeDashboardQueries([...definitions(), {
          name: expectedName, from: view.data.source,
          filter: { predicates: [{ field: 'routing-run-key', equals: `octo/api:${run}` }] }
        }], canonical, [expectedName])[expectedName];
        const result = await viewResult(indexedDB, `octo/api:${run}`, view.id);
        expect(result.rows, `${view.id}: run ${run}`).toEqual(expected.rows);
      }
    }
    expect((await viewResult(indexedDB, 'octo/api:1', 'routing-run-attribution')).rows).toMatchObject([
      { bucket: 'subagent_exact', aic: 0, 'agent-name': 'exact-owner' },
      { bucket: 'subagent_correlated', aic: 2, 'agent-name': 'first-owner' },
      { bucket: 'subagent_correlated', aic: 3, 'agent-name': 'second-owner' },
      { bucket: 'classifier', aic: 1 }, { bucket: 'detection', aic: 2 }, { bucket: 'evals', aic: 3 }
    ]);
    expect((await viewResult(indexedDB, 'octo/api:2', 'routing-run-cost')).rows).toMatchObject([
      { 'cost-complete': false, 'complete-aic': null, 'missing-cost-records': 1 }
    ]);
    for (const run of ['', 'not/a/run', 'octo/api:999']) {
      for (const view of page.views) expect((await viewResult(indexedDB, run, view.id)).rows).toEqual([]);
    }
    for (const view of page.views) {
      expect((await viewResult(indexedDB, 'octo/api:1', view.id, { 'routing-filter-state': 'unrouted' })).rows).toEqual([]);
    }
    expect((await viewResult(indexedDB, 'octo/api:1', 'routing-run-attribution', {
      'routing-filter-start': new Date(now).toISOString(), 'routing-filter-end': new Date(now + 1).toISOString()
    })).rows).toHaveLength(6);
  });

  it('reads only the selected audit partition and stays fresh when the attempt changes', async () => {
    const indexedDB = new IDBFactory();
    await ingest(indexedDB, 1, [event('firewall.token_usage', { requestId: 'old', aic: 10 })]);
    for (let run = 2; run <= 30; run++) {
      await ingest(indexedDB, run, Array.from({ length: 12 }, (_, index) => event('firewall.token_usage', {
        requestId: `unrelated-${index}`, aic: 999
      })));
    }
    const reads = vi.spyOn(IDBObjectStore.prototype, 'getAll');
    const indexes = vi.spyOn(IDBIndex.prototype, 'getAll');
    for (const viewId of contract['detail-views']) {
      reads.mockClear();
      indexes.mockClear();
      await viewResult(indexedDB, 'octo/api:1', viewId);
      expect(reads.mock.contexts.map(store => /** @type {IDBObjectStore} */ (store).name)).not.toContain('audits');
      const auditReads = indexes.mock.contexts.flatMap((value, index) => {
        const source = /** @type {IDBIndex} */ (value);
        return source.objectStore.name === 'audits' ? [{ source, request: indexes.mock.results[index].value }] : [];
      });
      expect(auditReads).toHaveLength(contract['detail-audit-read-bound']);
      for (const read of auditReads) {
        expect(read.source.name).toBe('byRun');
        expect(read.request.result).toHaveLength(3);
      }
    }
    vi.restoreAllMocks();
    await ingest(indexedDB, 1, [event('firewall.token_usage', { requestId: 'new', aic: 2 })], 2);
    expect((await viewResult(indexedDB, 'octo/api:1', 'routing-run-attribution')).rows)
      .toMatchObject([{ 'correlation-id': 'new', aic: 2 }]);
    expect((await viewResult(indexedDB, 'octo/api:2', 'routing-run-attribution')).rows).toHaveLength(12);
  });

  it('keeps the Run baseline comparison visible after its Audit detail expires', async () => {
    const indexedDB = new IDBFactory();
    await ingest(indexedDB, 1, [
      event('firewall.model_routing', { stage: 'selection', selectedModel: 'gpt-5' }),
      event('model.baseline_estimate', { baselineModel: 'claude-sonnet-5.5', aic: 6, complete: true, mainRequests: 1 }),
      event('firewall.model_routing', { stage: 'request', requestId: 'main', routed: 'as_selected' }),
      event('firewall.token_usage', { requestId: 'main', aic: 1 })
    ]);
    const { maintainCanonicalDatabase } = await import('../../src/data/storage/indexeddb.js');
    await maintainCanonicalDatabase(indexedDB, { now: now + 8 * 86400000, maxDatabaseBytes: Number.MAX_SAFE_INTEGER });
    expect((await viewResult(indexedDB, 'octo/api:1', 'routing-run-cost')).rows).toMatchObject([
      { 'baseline-model': 'claude-sonnet-5.5', 'baseline-aic': 6, 'baseline-complete': true }
    ]);
    expect((await viewResult(indexedDB, 'octo/api:1', 'routing-run-attribution')).rows).toEqual([]);
  });
});
