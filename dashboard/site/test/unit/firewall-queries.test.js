// @vitest-environment node
import 'fake-indexeddb/auto';
import { IDBFactory } from 'fake-indexeddb';
import { afterEach, describe, expect, it } from 'vitest';
import databaseQueries from '../../src/data/queries/database.json' with { type: 'json' };
import { executeDashboardQueries } from '../../src/data/queries/declarative.js';
import { loadDatabaseQuerySources, queryDatabaseSources } from '../../src/data/queries/database.js';
import { DATABASE_NAME, openCanonicalDatabase } from '../../src/data/storage/indexeddb.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';

const queries = authoritativeDashboard.dashboard.queries.filter(
  (/** @type {{ name: string }} */ query) => query.name.startsWith('firewall-')
);
const observation = databaseQueries.find((query) => query.name === 'firewall-observations');
const runs = [
  {
    id: 'run:1', githubRunId: '1', owner: 'org', repository: 'repo', workflowPath: 'test.md',
    rolloutMode: 'review', conclusion: 'success', engine: 'copilot',
    workflowLink: { href: 'https://github.example/org/repo/blob/HEAD/test.md' }
  },
  { id: 'run:2', githubRunId: '2', owner: 'org', repository: 'other', workflowPath: 'other.md' }
];
const records = [
  { id: 'allowed', runId: 'run:1', type: 'firewall.http_access', decision: 'allowed', requestCount: 0 },
  { id: 'blocked', runId: 'run:1', type: 'firewall.http_access', decision: 'denied', requestCount: 7 },
  { id: 'null-count', runId: 'run:1', type: 'firewall.http_access', decision: 'allowed', requestCount: null },
  { id: 'missing-count', runId: 'run:2', type: 'firewall.http_access', decision: 'denied' },
  { id: 'other-source', runId: 'run:1', source: 'other', type: 'firewall.http_access', decision: 'denied', requestCount: 100 },
  { id: 'other-type', runId: 'run:1', type: 'mcp.tool_call', decision: 'denied', requestCount: 100 },
  { id: 'orphan', runId: 'missing', type: 'firewall.http_access', decision: 'denied', requestCount: 2 }
].map((record) => ({
  source: 'firewall', domain: 'example.com', timestamp: '2026-10-15T00:00:00Z', ...record
}));

/** @param {IDBFactory} [factory] */
async function seed(factory = indexedDB) {
  const database = await openCanonicalDatabase(factory);
  try {
    await new Promise((resolve, reject) => {
      const transaction = database.transaction(['runs', 'domains', 'audits'], 'readwrite');
      for (const row of runs) transaction.objectStore('runs').put(row);
      for (const row of records) transaction.objectStore('domains').put(row);
      transaction.objectStore('audits').put({
        id: 'not-a-domain', runId: 'run:1', source: 'firewall', type: 'firewall.http_access',
        domain: 'unrelated.example', decision: 'denied', requestCount: 999
      });
      transaction.oncomplete = () => resolve(undefined);
      transaction.onerror = () => reject(transaction.error);
    });
  } finally {
    database.close();
  }
}

afterEach(async () => {
  await new Promise((resolve, reject) => {
    const request = indexedDB.deleteDatabase(DATABASE_NAME);
    request.onsuccess = () => resolve(undefined);
    request.onerror = () => reject(request.error);
  });
});

describe('Firewall query contract', () => {
  it('projects only domain evidence with one run join and preserves the former observation contract', async () => {
    expect(observation).toMatchObject({
      from: '$domains', stores: ['runs', 'domains'],
      joins: [{ source: '$runs', type: 'left', on: [{ left: 'runId', right: 'id' }] }]
    });
    expect(observation?.compute).toHaveLength(2);
    await seed();
    const canonical = await queryDatabaseSources(indexedDB, {}, ['domains', 'firewall-observations']);
    const legacy = {
      ...observation, name: 'domain-observations', from: 'domains', joins: undefined,
      filter: { predicates: [
        { field: 'event-source', equals: 'firewall' },
        { field: 'event-type', in: ['firewall.http_access', 'firewall.http_access'] }
      ] },
      compute: [{ as: 'request-count-value', function: 'coalesce', args: [{ field: 'request-count' }, { value: 1 }] }],
      select: observation?.select?.map((field) => ({
        ...field,
        field: field.field === 'id' ? 'event' : field.field === 'timestamp' ? 'event-timestamp' : field.field
      }))
    };
    const expected = executeDashboardQueries([legacy], {
      domains: canonical.domains
    }, ['domain-observations']);
    expect(canonical['firewall-observations'].rows).toEqual(expected['domain-observations'].rows);
    expect(canonical['firewall-observations'].rows).toHaveLength(5);
    expect(canonical['firewall-observations'].rows.map((row) => row['request-count'])).toEqual([0, 7, 1, 1, 2]);
  });

  it('preserves native totals, source links, domain drilldown, and scoped view payloads', async () => {
    await seed();
    const names = queries.map((/** @type {{ name: string }} */ query) => query.name);
    const actual = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
      await loadDatabaseQuerySources(indexedDB, {}, { queries, sourceNames: names })
    );
    expect(actual['firewall-domain-totals'].rows).toEqual([
      { domain: 'example.com', run: 2, accepted: 1, blocked: 10 }
    ]);
    expect(actual['firewall-most-blocked-domains'].rows).toEqual(actual['firewall-domain-totals'].rows);
    expect(actual['firewall-domain-workflows'].rows).toContainEqual({
      domain: 'example.com', organization: 'org', repository: 'repo', workflow: 'test.md',
      'workflow-source-url': 'https://github.example/org/repo/blob/HEAD/test.md',
      runs: 1, accepted: 1, blocked: 7
    });
    const page = authoritativeDashboard.dashboard.pages.find(
      (/** @type {{ id: string }} */ page) => page.id === 'firewall'
    );
    const compiled = compileDashboardViewPayloadQueries(page, 'firewall', {
      queries, sourceNames: names,
      queryContext: {
        filters: { domain: ['unobserved.example'] },
        timeWindow: { start: '2026-10-01T00:00:00Z', end: '2026-11-01T00:00:00Z' }
      }
    });
    const scoped = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
      await loadDatabaseQuerySources(indexedDB, {}, {
        queries: [...queries, ...compiled.queries], sourceNames: compiled.aliases
      })
    );
    expect(compiled.aliases).toHaveLength(2);
    for (const alias of compiled.aliases) {
      expect(scoped[alias].rows).toEqual([]);
      expect(scoped[alias].metadata.availability).toBe('empty');
    }
  });

  it('executes explicit queries without reading unrelated event families', async () => {
    const factory = new IDBFactory();
      await seed(factory);
      /** @type {string[]} */
      let stores = [];
      const observations = await queryDatabaseSources(factory, {}, ['firewall-observations'], {
        onMetrics: (metrics) => { stores = metrics.stores; }
      });
      expect(stores).toEqual(['runs', 'domains']);
      expect(observations['firewall-observations'].rows).toHaveLength(5);
      const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
        await loadDatabaseQuerySources(factory, {}, {
          queries, sourceNames: ['firewall-domain-totals', 'firewall-most-blocked-domains']
        })
      );
      for (const source of ['firewall-domain-totals', 'firewall-most-blocked-domains']) {
        expect(result[source].metadata.availability).toBe('available');
        expect(result[source].rows).toEqual([
          { domain: 'example.com', run: 2, accepted: 1, blocked: 10 }
        ]);
      }
  });
});
