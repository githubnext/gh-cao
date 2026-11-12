import assert from 'node:assert/strict';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  analyzeDashboardComplexity,
  formatDashboardComplexityMarkdown,
  readDashboardTableCounts
} from '../../dashboard/report/query-complexity.mjs';
import { DatabaseSync } from 'node:sqlite';
import { SQLITE_INDEXEDDB_METADATA_SCHEMA } from '../../dashboard/site/src/data/storage/sqlite-indexeddb.js';

function dashboard() {
  return {
    'language-version': '0.1.0',
    dashboard: {
      id: 'test-dashboard',
      title: 'Test dashboard',
      description: 'Test dashboard.',
      queries: [
        {
          name: 'base',
          from: 'runs',
          filter: { predicates: [{ field: 'conclusion', equals: 'failure' }] },
          select: [{ field: 'run' }, { field: 'repository' }]
        },
        {
          name: 'summary',
          from: 'base',
          aggregate: { values: [{ field: 'run', as: 'runs', reducer: 'count' }] }
        },
        {
          name: 'joined',
          from: 'base',
          joins: [{
            source: 'repositories',
            type: 'left',
            on: [{ left: 'repository', right: 'repository' }],
            fields: [{ field: 'organization', as: 'organization' }]
          }],
          select: [{ field: 'run' }, { field: 'organization' }]
        }
      ],
      views: [
        { id: 'joined-view', data: { source: 'joined' } }
      ],
      pages: [{
        id: 'overview',
        kind: 'custom',
        views: [
          { id: 'summary-card', data: { source: 'summary' } },
          'joined-view'
        ]
      }],
      navigation: []
    }
  };
}

test('estimates row reads and ranks queries by dependency-amortized pressure', () => {
  const analysis = analyzeDashboardComplexity(dashboard());
  const byName = Object.fromEntries(analysis.inventory.map((query) => [query.name, query]));

  assert.deepEqual(byName.base, {
    name: 'base',
    rank: 3,
    'used-by': ['query:joined', 'query:summary'],
    model: 'normalized-upper-bound',
    assumptions: 'Each database table has weight 1; selectivity is 1; query dependencies materialize once per batch.',
    class: 'linear-row-reads',
    'output-field-count': 2,
    'direct-row-read-units': 3,
    'dependency-row-read-units': 0,
    'total-row-read-units': 3,
    'output-row-units': 1,
    'direct-materialized-field-units': 2,
    'dependency-materialized-field-units': 0,
    'total-materialized-field-units': 2,
    warnings: [],
    'source-coefficients': { runs: 3 },
    'direct-source-coefficients': { runs: 3 },
    'stage-row-reads': {
      from: { runs: 1 },
      filter: { runs: 1 },
      select: { runs: 1 }
    }
  });
  assert.equal(byName.summary['total-row-read-units'], 5);
  assert.deepEqual(byName.summary['used-by'], ['page:overview/view:summary-card']);
  assert.equal(byName.joined['total-row-read-units'], 7);
  assert.deepEqual(byName.joined['used-by'], ['view:joined-view']);
  assert.deepEqual(analysis.ranking.map(({ rank, name, score }) => ({ rank, name, score })), [
    { rank: 1, name: 'joined', score: 7 },
    { rank: 2, name: 'summary', score: 5 },
    { rank: 3, name: 'base', score: 3 }
  ]);
  assert.equal(analysis.summary['materialize-all-row-read-units'], 9);
  assert.deepEqual(Object.keys(analysis.summary['source-coefficients']), [
    'repositories',
    'runs'
  ]);
});

test('formats a bounded markdown complexity ranking', () => {
  const markdown = formatDashboardComplexityMarkdown(analyzeDashboardComplexity(dashboard()), {
    limit: 2
  });

  assert.match(markdown, /^### Dashboard query complexity/m);
  assert.match(markdown, /\| Rank \| Query \| Used by \| Total \|/);
  assert.match(markdown, /Database table coefficients: `runs` 8, `repositories` 1/);
  assert.match(markdown, /\| 1 \| `joined` \| `view:joined-view` \| 7 \| 4 \| 3 \| 2 \| 4 \| — \| linear \|/);
  assert.match(markdown, /\| 2 \| `summary` \| `page:overview\/view:summary-card` \| 5 \| 2 \| 3 \| 1 \| 3 \| — \| linear \|/);
  assert.doesNotMatch(markdown, /\| 3 \| `base`/);
  assert.match(markdown, /Showing 2 of 3 queries/);
});

test('warns when a wide raw union materializes more field-units than aggregated sources', () => {
  const runKeys = ['organization', 'repository', 'workflow', 'run', 'run-attempt'];
  const recordSources = ['audits', 'domains', 'tools', 'issues'];
  const queries = [
    { name: 'event-base', from: 'audits', union: recordSources.slice(1) },
    ...recordSources.map((source) => ({
      name: `${source}-event-runs`,
      from: source,
      aggregate: {
        by: runKeys,
        values: [{ field: 'event', as: 'events', reducer: 'count' }]
      }
    })),
    {
      name: 'event-runs',
      from: 'audits-event-runs',
      union: recordSources.slice(1).map((source) => `${source}-event-runs`),
      aggregate: {
        by: runKeys,
        values: [{ field: 'events', as: 'events', reducer: 'sum' }]
      }
    }
  ];
  const analysis = analyzeDashboardComplexity({
    dashboard: { queries }
  });
  const byName = Object.fromEntries(analysis.inventory.map((query) => [query.name, query]));

  assert.ok(byName['event-base']['output-field-count'] > byName['event-runs']['output-field-count']);
  assert.ok(byName['event-base']['total-materialized-field-units'] >= 256);
  assert.match(byName['event-base'].warnings[0], /Potentially large materialized output/);
  assert.ok(
    byName['event-base']['total-materialized-field-units']
      > byName['event-runs']['total-materialized-field-units']
  );
  assert.deepEqual(byName['event-runs'].warnings, []);
  assert.equal(analysis.ranking[0].name, 'event-base');
});


test('weights database tables by normalized deployed row counts', async (t) => {
  const directory = await mkdtemp(path.join(tmpdir(), 'cao-dashboard-complexity-database-'));
  const databasePath = path.join(directory, 'dashboard.sqlite');
  t.after(() => rm(directory, { recursive: true, force: true }));
  const connection = new DatabaseSync(databasePath);
  connection.exec(SQLITE_INDEXEDDB_METADATA_SCHEMA);
  connection.prepare('INSERT INTO __idb_databases (name, version) VALUES (?, 1)')
    .run('gh-aw-cao-dashboard-data');
  for (const [table, count] of [['repositories', 4], ['runs', 8], ['workflows', 2]]) {
    connection.prepare('INSERT INTO __idb_stores (database_name, name, key_path) VALUES (?, ?, ?)')
      .run('gh-aw-cao-dashboard-data', table, '"id"');
    for (let index = 0; index < count; index += 1) {
      connection.prepare(`
        INSERT INTO __idb_records (database_name, store_name, record_key, value)
        VALUES (?, ?, ?, ?)
      `).run('gh-aw-cao-dashboard-data', table, String(index), '{}');
    }
  }
  connection.close();

  const tableCounts = readDashboardTableCounts(databasePath);
  assert.deepEqual(tableCounts, { repositories: 4, runs: 8, workflows: 2 });
  const analysis = analyzeDashboardComplexity(dashboard(), { tableCounts });
  const byName = Object.fromEntries(analysis.inventory.map((query) => [query.name, query]));

  assert.equal(analysis.summary.model, 'deployment-weighted-upper-bound');
  assert.equal(analysis.summary['materialize-all-row-read-units'], 8.5);
  assert.deepEqual(analysis.summary['source-coefficients'], {
    repositories: 0.5,
    runs: 8
  });
  assert.equal(byName.joined['total-row-read-units'], 6.5);
  assert.match(
    formatDashboardComplexityMarkdown(analysis),
    /Deployed table rows: `repositories` 4, `runs` 8, `workflows` 2/
  );

  const emptyTable = analyzeDashboardComplexity({
    dashboard: {
      queries: [{ name: 'empty-campaigns', from: 'campaigns' }]
    }
  }, {
    tableCounts: { campaigns: 0, runs: 8 }
  });
  assert.equal(emptyTable.summary['materialize-all-row-read-units'], 0);
  assert.deepEqual(emptyTable.summary['source-coefficients'], { campaigns: 0 });
});
