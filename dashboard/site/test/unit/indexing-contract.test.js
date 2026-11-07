import 'fake-indexeddb/auto';
import { beforeEach, describe, expect, it } from 'vitest';
import { processDataRequest } from '../../src/data-worker.js';
import { DATABASE_NAME, recordTransaction } from '../../src/data/storage/indexeddb.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';

const dashboard = /** @type {Record<string, any>} */ (
  authoritativeDashboard.dashboard
);
const metadata = {
  'source-id': 'indexing-contract',
  'source-kind': 'fixture',
  'as-of': '2026-09-24T00:00:00Z',
  'retrieved-at': '2026-09-24T00:00:00Z',
  completeness: /** @type {'complete'} */ ('complete'),
  freshness: /** @type {'fresh'} */ ('fresh'),
  availability: /** @type {'available'} */ ('available')
};

describe('indexing dashboard', () => {
  it('projects individual ingestion run counts and actual canonical table names through the worker', () => {
    const results = /** @type {Record<string, any>} */ (processDataRequest({
      operation: 'execute-dashboard-queries',
      queries: dashboard.queries,
      sources: {
        transactions: {
          source: 'transactions',
          rows: [
            {
              id: 'ingest-normalized-jsonl:one',
              kind: 'ingest-normalized-jsonl',
              payloadScope: 'gh-aw-logs-runs/2026-09-20-0000-0123456789abcdef.jsonl',
              createdAt: '2026-09-23T00:00:00Z',
              records: 12,
              committedRecords: 10,
              rawRuns: 3
            },
            {
              id: 'ingest-normalized-jsonl:two',
              kind: 'ingest-normalized-jsonl',
              payloadScope: 'gh-aw-logs-runs/2026-09-21-0000-0123456789abcdef.jsonl',
              createdAt: '2026-09-24T01:00:00Z',
              records: 8,
              committedRecords: 7,
              rawRuns: 2
            },
            {
              id: 'ingest-dashboard-sources:three',
              kind: 'ingest-dashboard-sources',
              payloadScope: 'gh-aw-logs-records/2026-09-22-0000-0123456789abcdef.jsonl',
              createdAt: '2026-09-24T02:00:00Z',
              records: 99,
              committedRecords: 99,
              rawRuns: 99
            }
          ],
          metadata
        },
        campaigns: { source: 'campaigns', rows: [{ campaign: 'one' }], metadata },
        repositories: { source: 'repositories', rows: [{ repository: 'one' }], metadata },
        workflows: { source: 'workflows', rows: [{ workflow: 'one' }, { workflow: 'two' }], metadata },
        runs: { source: 'runs', rows: [{ run: 'one' }, { run: 'two' }, { run: 'three' }], metadata },
        domains: { source: 'domains', rows: [], metadata },
        tools: { source: 'tools', rows: [{ event: 'tool:one' }], metadata },
        audits: { source: 'audits', rows: [{ event: 'audit:one' }, { event: 'audit:two' }], metadata },
        issues: { source: 'issues', rows: [{ event: 'issue:one' }], metadata },
        'operational-values': { source: 'operational-values', rows: [], metadata }
      },
      sourceNames: [
        'indexing-ingestion-runs',
        'indexing-database-table-counts'
      ]
    }));

    expect(results['indexing-ingestion-runs'].rows).toHaveLength(3);
    expect(results['indexing-ingestion-runs'].rows.map((/** @type {any} */ row) => [row['shard-day'], row.rawRuns]))
      .toEqual([
        ['2026-09-20', 3],
        ['2026-09-21', 2],
        ['2026-09-22', 99]
      ]);
    expect(results['indexing-database-table-counts'].rows).toEqual([
      { table: 'runs', records: 3 },
      { table: 'transactions', records: 3 },
      { table: 'audits', records: 2 },
      { table: 'workflows', records: 2 },
      { table: 'campaigns', records: 1 },
      { table: 'issues', records: 1 },
      { table: 'repositories', records: 1 },
      { table: 'tools', records: 1 }
    ]);
    const tableLabels = results['indexing-database-table-counts'].rows.map((/** @type {any} */ row) => row.table);
    expect(tableLabels).not.toContain('domains');
    expect(tableLabels).not.toContain('operationalValues');
  });

  it('declares storage information without activity queries and removes the daily ingestion chart and its query', () => {
    const page = dashboard.pages.find(/** @param {any} candidate */ (candidate) => candidate.id === 'indexing');
    expect(page.views[0]).toMatchObject({
      id: 'ingestion-storage', mark: 'element', element: 'storage-info', data: { sources: [] }
    });
    expect(page.views.some(/** @param {any} view */ (view) => view.id === 'indexing-size')).toBe(false);
    expect(dashboard.queries.some(/** @param {any} query */ (query) => query.name === 'indexing-daily-records')).toBe(false);
  });

  describe('ingestion scatter worker contract', () => {
    const source = 'indexing-ingestion-runs';
    const page = dashboard.pages.find(/** @param {any} candidate */ (candidate) => candidate.id === 'indexing');
    const view = page.views.find(/** @param {any} candidate */ (candidate) => candidate.id === 'indexing-workflow-runs');
    const queryContext = {
      filters: { repository: ['unrelated-repository'], organization: ['unrelated-organization'] },
      timeWindow: { start: '2099-01-01T00:00:00Z', end: '2099-01-02T00:00:00Z' }
    };

    beforeEach(async () => {
      await new Promise((resolve, reject) => {
        const request = indexedDB.deleteDatabase(DATABASE_NAME);
        request.onsuccess = () => resolve(undefined);
        request.onerror = () => reject(request.error);
      });
    });

    async function query() {
      const compiled = compileDashboardViewPayloadQueries(page, 'indexing', {
        queries: dashboard.queries, viewId: view.id, sourceNames: [source], queryContext
      });
      const result = /** @type {Record<string, any>} */ (await processDataRequest({
        operation: 'query-canonical-dashboard',
        pageId: 'indexing', viewId: view.id, sourceNames: [source],
        context: dashboard, queryContext
      }));
      const payload = result[compiled.aliases[0]];
      expect(['available', 'empty']).toContain(payload?.metadata.availability);
      return { [source]: payload };
    }

    it('declares an unbucketed, unaggregated, unbounded scatter independent of page context', () => {
      const definition = dashboard.queries.find(/** @param {any} candidate */ (candidate) => candidate.name === source);
      expect(definition.from).toBe('indexing-shard-dates');
      expect(dashboard.queries.find(/** @param {any} candidate */ (candidate) => candidate.name === definition.from))
        .toMatchObject({ from: 'transactions', compute: [
          { as: 'shard-day', function: 'activity-shard-day', args: [{ field: 'payloadScope' }] },
          { as: 'missing-shard-day', function: 'equals-any', args: [{ field: 'shard-day' }, { value: null }] }
        ] });
      for (const property of ['aggregate', 'limit']) expect(definition[property]).toBeUndefined();
      expect(definition.filter).toEqual({
        predicates: [{ field: 'missing-shard-day', equals: false }]
      });
      expect(view).toMatchObject({
        chart: 'scatter',
        scatter: { cluster: false },
        data: { source, 'query-context': false },
        encoding: {
          x: { field: 'shard-day', type: 'temporal', title: 'Shard activity date' },
          y: { field: 'workflow-runs', type: 'quantitative' },
          value: { field: 'shard-entry-count', type: 'quantitative' },
          color: { field: 'run-count-status' }
        }
      });
      expect(view.encoding.x['time-unit']).toBeUndefined();
      expect(view.encoding.y.aggregate).toBeUndefined();
      expect(view.data.limit).toBeUndefined();
    });

    it('uses shard dates independently of import times, retaining every kind, coincident dates, zeroes, and unavailable counts', async () => {
      const entries = [
        { id: 'a', kind: 'ingest-jsonl', createdAt: '2020-01-01T01:00:00.123Z', rawRuns: 4 },
        { id: 'b', kind: 'ingest-normalized-jsonl', createdAt: '2020-01-01T02:00:00.456Z', rawRuns: 0 },
        { id: 'c', kind: 'ingest-dashboard-sources', createdAt: '2020-01-01T02:00:00.456Z' },
        { id: 'd', kind: 'ingest-jsonl-failed', createdAt: '2020-01-01T02:00:00.456Z' },
        { id: 'e', kind: 'retention', createdAt: '2020-01-01T03:00:00.789Z', rawRuns: null },
        { id: 'f', kind: 'future-ingestion-kind', createdAt: '2020-01-01T04:00:00.999Z', rawRuns: 7 }
      ];
      for (const entry of entries) await recordTransaction(indexedDB, {
        ...entry,
        payloadScope: 'gh-aw-logs-runs/2019-12-31-0000-0123456789abcdef.jsonl'
      });
      const results = await query();
      expect(results[source].rows).toEqual(entries.map((entry) => ({
        ...entry,
        'shard-day': '2019-12-31',
        'workflow-runs': entry.rawRuns ?? 0,
        'shard-entry-count': entries.length,
        'run-count-status': entry.rawRuns == null
          ? 'Run count unavailable (shown at 0)' : 'Reported run count'
      })));
      const aliases = compileDashboardViewPayloadQueries(page, 'indexing', {
        queries: dashboard.queries, viewId: view.id, sourceNames: [source], queryContext
      });
      expect(aliases.aliases).toHaveLength(1);
      expect(aliases.queries.at(-1)).toMatchObject({ from: `${aliases.aliases[0]}:input` });
      expect(aliases.queries.at(-1)?.limit).toBeUndefined();
    });

    it('does not fall back to import time for undated, structural, or invalid shard filenames', async () => {
      const scopes = [
        undefined, 'inventory-sources.json',
        'gh-aw-logs-runs/0000-00-00-0000-0123456789abcdef.jsonl',
        'gh-aw-logs-runs/2026-02-30-0000-0123456789abcdef.jsonl'
      ];
      for (const [index, payloadScope] of scopes.entries()) await recordTransaction(indexedDB, {
        id: `undated-${index}`, kind: 'ingest-normalized-jsonl', payloadScope,
        createdAt: '2026-09-24T01:00:00Z', rawRuns: 1
      });
      const result = await query();
      expect(result[source].rows).toEqual([]);
    });

    it('returns an honest empty result and picks up subsequent database entries without a row cap', async () => {
      expect((await query())[source].rows).toEqual([]);
      for (let index = 0; index < 125; index++) {
        await recordTransaction(indexedDB, {
          id: `entry-${index.toString().padStart(3, '0')}`, kind: 'ingest-normalized-jsonl',
          payloadScope: 'gh-aw-logs-runs/2026-09-23-0000-0123456789abcdef.jsonl',
          createdAt: '2026-09-24T00:00:00.123Z', rawRuns: index
        });
      }
      expect((await query())[source].rows).toHaveLength(125);
      await recordTransaction(indexedDB, {
        id: 'new-entry', kind: 'ingest-jsonl', createdAt: '2026-09-24T12:00:00.999Z', rawRuns: 8,
        payloadScope: 'gh-aw-logs-runs/2026-09-24-0000-0123456789abcdef.jsonl'
      });
      const updated = await query();
      expect(updated[source].rows).toHaveLength(126);
      expect(updated[source].rows.at(-1)).toMatchObject({ id: 'new-entry', 'workflow-runs': 8, 'shard-entry-count': 1 });
      expect(updated[source].rows[0]['shard-entry-count']).toBe(125);
    });

    it('fails closed when the transaction source cannot be produced', () => {
      const result = /** @type {Record<string, any>} */ (processDataRequest({
        operation: 'execute-dashboard-queries', queries: dashboard.queries,
        sourceNames: [source], sources: {}
      }));
      expect(result[source]).toMatchObject({ rows: [], metadata: { availability: 'unavailable' } });
    });
  });

  it('uses a sorted horizontal count chart and focused transaction cards without a table facet', () => {
    const page = dashboard.pages.find(/** @param {any} page */ (page) => page.id === 'indexing');
    const countChart = page.views.find(/** @param {any} view */ (view) => view.id === 'indexing-database-table-counts');
    const transactions = page.views.find(/** @param {any} view */ (view) => view.id === 'transaction-entries');
    const card = dashboard['card-templates'].find(/** @param {any} template */ (template) => template.id === 'ingestion-transaction');

    expect(countChart).toMatchObject({
      mark: 'chart',
      chart: 'horizontal-bar',
      data: { source: 'indexing-database-table-counts' }
    });
    expect(dashboard.queries.find(/** @param {any} query */ (query) => query.name === 'indexing-database-table-counts')['order-by'])
      .toEqual([{ field: 'records', direction: 'desc' }, { field: 'table', direction: 'asc' }]);
    expect(page.views[1].id).toBe('indexing-database-table-counts');
    expect(transactions).toMatchObject({
      mark: 'list',
      list: { style: 'entity-cards', card: 'ingestion-transaction' }
    });
    expect(transactions.controls).toBeUndefined();
    expect(card.details.map(/** @param {any} detail */ (detail) => detail.field))
      .toEqual(['created-at', 'committed-records', 'payload-hash']);
  });

  it('reads table counts directly instead of cloning all count dependencies for the view', () => {
    const page = dashboard.pages.find(/** @param {any} candidate */ (candidate) => candidate.id === 'indexing');
    const payload = compileDashboardViewPayloadQueries(page, 'indexing', {
      queries: dashboard.queries,
      views: dashboard.views,
      viewId: 'indexing-database-table-counts',
      sourceNames: ['indexing-database-table-counts'],
      queryContext: { filters: { repository: ['example'] } }
    });

    expect(page.views.find(/** @param {any} view */ (view) => view.id === 'indexing-database-table-counts').data['query-context']).toBe(false);
    expect(payload).toEqual({ aliases: [], queries: [], replacedSources: [] });
  });

  it('declares a live collection-health table on the Ingestion page', () => {
    const page = dashboard.pages.find(/** @param {any} page */ (page) => page.id === 'indexing');
    const query = dashboard.queries.find(/** @param {any} query */ (query) => query.name === 'ingestion-health');
    const view = page.views.find(/** @param {any} view */ (view) => view.id === 'ingestion-health-status');

    expect(page).toMatchObject({ title: 'Ingestion', 'navigation-label': 'Ingestion' });
    expect(query).toMatchObject({
      from: 'collection-health',
      select: expect.arrayContaining([
        { field: 'health' },
        { field: 'queue-depth' },
        { field: 'webhook-received' },
        { field: 'collection-retried' },
        { field: 'last-failure-code' }
      ])
    });
    expect(view).toMatchObject({
      mark: 'table',
      data: { source: 'ingestion-health' },
      'empty-message': expect.stringContaining('server-side collection profile')
    });
  });

  it('declares a 24-hour GitHub API usage line chart on the Ingestion page', () => {
    const page = dashboard.pages.find(/** @param {any} page */ (page) => page.id === 'indexing');
    const query = dashboard.queries.find(/** @param {any} query */ (query) => query.name === 'github-api-usage');
    const view = page.views.find(/** @param {any} view */ (view) => view.id === 'github-api-usage');

    expect(query).toMatchObject({
      from: 'github-quota-usage',
      select: expect.arrayContaining([
        { field: 'observed-at' },
        { field: 'bucket' },
        { field: 'usage-percent' }
      ])
    });
    expect(view).toMatchObject({
      mark: 'chart',
      chart: 'line',
      data: { source: 'github-api-usage' },
      encoding: {
        x: { field: 'observed-at', type: 'temporal', 'time-unit': 'hour' },
        y: { field: 'usage-percent', aggregate: 'max' },
        color: { field: 'bucket' }
      }
    });
  });
});
