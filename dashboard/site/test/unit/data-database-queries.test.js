import 'fake-indexeddb/auto';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ingestCachedGhAwJsonl, ingestDashboardSources, ingestNormalizedJsonl } from '../../src/data/ingest/coordinator.js';
import { CANONICAL_SCHEMA_VERSION } from '../../src/data/model/schema.js';
import {
  CANONICAL_DATABASE_SCHEMA,
  DATABASE_NAME,
  DATABASE_STORES,
  ENTITY_STORES,
  recordTransaction,
  upsertCanonicalBatch
} from '../../src/data/storage/indexeddb.js';
import {
  loadDatabaseQuerySources,
  queryDatabaseSources,
  queryIndexedDatabaseSources
} from '../../src/data/queries/database.js';
import { processDataRequest } from '../../src/data-worker.js';
import { createDashboardQueryBudget, executeDashboardQueries, resolveDashboardQuerySources } from '../../src/data/queries/declarative.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';

const loadCanonicalViewSources = loadDatabaseQuerySources;
const queryCanonicalViewSources = queryDatabaseSources;
const queryNativeCountSources = queryIndexedDatabaseSources;

const metadata = { 'as-of': '2026-09-09T05:00:00Z', 'artifact-generation': 'generation-a' };
const dashboardQueries = authoritativeDashboard.dashboard.queries;

/**
 * @param {Partial<import('../../src/data/model/schema.js').CanonicalBatch>} [overrides]
 * @returns {import('../../src/data/model/schema.js').CanonicalBatch}
 */
function canonicalBatch(overrides = {}) {
  return {
    campaigns: [],
    repositories: [],
    workflows: [],
    runs: [],
    domains: [],
    tools: [],
    skills: [],
    friction: [],
    audits: [],
    issues: [],
    operationalValues: [],
    marketplacePackages: [],
    ...overrides
  };
}

/** @param {string} storeName @param {Record<string, unknown>} record */
async function putCanonicalRecord(storeName, record) {
  const database = await new Promise((resolve, reject) => {
    const request = indexedDB.open(DATABASE_NAME);
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
  await new Promise((resolve, reject) => {
    const transaction = database.transaction(storeName, 'readwrite');
    transaction.objectStore(storeName).put(record);
    transaction.oncomplete = () => resolve(undefined);
    transaction.onerror = () => reject(transaction.error);
  });
  database.close();
}
const sources = {  campaigns: {
    rows: [{
      campaign: 'dashboard', 'campaign-name': 'CAO Dashboard', 'campaign-description': 'Deploy the dashboard.',
      'campaign-icon': 'graph', 'campaign-mode': 'review', 'campaign-enabled': true,
      'campaign-worker-count': 1, 'campaign-min-version': 'v0.89.3', 'campaign-experimental': true
    }],
    metadata
  },
  repositories: {
    rows: [{ organization: 'githubnext', repository: 'gh-cao', 'rollout-mode': 'review' }],
    metadata
  },
  workflows: {
    rows: [{
      organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.lock.yml',
      campaign: 'dashboard', 'campaign-name': 'CAO Dashboard', 'workflow-id': '501',
      'workflow-name': 'Published registry name', 'workflow-role': 'worker',
      'workflow-registry-state': 'active', 'workflow-active': 'true',
      'admission-status': 'admitted', 'inventory-ready': true,
      'workflow-link': {
        relation: 'workflow',
        href: 'https://github.com/githubnext/gh-cao/actions/workflows/501'
      },
      'created-at': '2026-09-01T00:00:00Z', 'updated-at': '2026-09-08T00:00:00Z',
      'rollout-mode': 'review'
    }],
    metadata
  },
  runs: {
    rows: [{
      organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md',
      run: '42', 'run-attempt': 2, 'run-status': 'completed', 'run-conclusion': 'failure',
      'started-at': '2026-09-09T04:00:00Z', 'failure-message': 'Build failed',
      'failure-detail': 'Build failed', 'failure-log': '##[error]Build failed',
      'failure-kind': 'driver_exit', 'target-repository': 'github/gh-aw',
      'admission-status': 'admitted', resource: 'actions', 'resource-wait-hours': 2,
      'rollout-mode': 'review', engine: 'copilot', 'engine-version': '1.2.3',
      'gh-aw-version': 'v0.89.4',
      'requested-model': 'model-a', 'resolved-model': 'model-b',
      'run-link': { relation: 'run', href: 'https://github.com/githubnext/gh-cao/actions/runs/42', label: 'Run 42' }
    }],
    metadata
  },
  'job-performance': {
    rows: [{
      organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md',
      run: '42', 'run-attempt': 2, 'job-id': '99', job: 'build',
      'job-status': 'completed', 'job-conclusion': 'failure', 'job-duration-seconds': 120,
      'started-at': '2026-09-09T04:01:00Z', runner: 'ubuntu-latest', engine: 'copilot', model: 'model-b'
    }],
    metadata
  },
  'work-items': {
    rows: [{
      'work-item-id': 'githubnext/gh-cao:.github/workflows/dashboard.md',
      name: 'Dashboard · Publish report', objective: 'Dashboard',
      organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md',
      run: '42', 'workflow-name': 'Dashboard', 'workflow-icon': 'workflow', campaign: 'dashboard',
      scope: 'githubnext/gh-cao', domain: 'dashboard', 'work-type': 'worker',
      'lifecycle-state': 'blocked', phase: 'completed', reason: 'Build failed',
      'reason-evidence-class': 'observed', 'next-action': 'Resolve the run failure',
      'next-actor': 'maintainer', 'safe-output-kind': 'workflow-output', 'waiting-on': 'scheduled run',
      'waiting-since': '2026-09-09T04:00:00Z', owner: 'dashboard', 'consequence-tier': 'medium',
      'verification-state': 'pending', 'outcome-state': 'pending',
      'started-at': '2026-09-09T04:00:00Z', 'ended-at': '2026-09-09T04:02:00Z',
      'observed-at': '2026-09-09T04:00:00Z',
      'run-link': { relation: 'run', href: 'https://github.com/githubnext/gh-cao/actions/runs/42' }
    }],
    metadata
  },
  'security-findings': {
    rows: [{
      organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md', run: '42',
      'smell-observation-id': 'threat-detection:observation-1', 'smell-id': 'threat-detection-secret-leak',
      'smell-name': 'Secret leak detected', 'smell-category': 'trust-and-security', 'smell-severity': 'high',
      'smell-summary': 'Threat detection reported untrusted or unsafe agent behavior.',
      'smell-evidence': 'credentials', 'observed-at': '2026-09-09T04:01:00Z',
      'run-link': { relation: 'run', href: 'https://github.com/githubnext/gh-cao/actions/runs/42' }
    }],
    metadata
  },
  tools: {
    rows: [
      {
        organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md',
        run: '42', 'run-attempt': 2, event: 'event:tool-call',
        'event-timestamp': '2026-09-09T04:00:10Z', 'event-source': 'mcp', 'event-type': 'mcp.tool_call',
        'event-summary': 'github.list_issues', 'event-status': 'requested',
        'request-count': 7,
        'correlation-id': 'call-1', 'safe-output-type': 'create_issue',
        'mcp-server-version': '1.0.0', 'mcp-protocol-version': '2025-06-18',
        'request-bytes': 128, 'response-bytes': 256,
        'github-entity-type': 'issue', 'source-sequence': 0, 'observed-at': '2026-09-09T04:00:10Z'
      }
    ],
    metadata
  },
  audits: {
    rows: [
      {
        organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md',
        run: '42', 'run-attempt': 2, event: 'event:agent-turn',
        'event-timestamp': '2026-09-09T04:00:20Z', 'event-source': 'agent', 'event-type': 'user.message',
        'event-summary': 'Planned the change', 'source-sequence': 1, 'observed-at': '2026-09-09T04:00:20Z'
      }
    ],
    metadata
  },
  domains: { rows: /** @type {Record<string, unknown>[]} */ ([]), metadata },
  issues: { rows: /** @type {Record<string, unknown>[]} */ ([]), metadata },
  operationalValues: { rows: /** @type {Record<string, unknown>[]} */ ([]), metadata },
  marketplacePackages: { rows: /** @type {Record<string, unknown>[]} */ ([]), metadata },
  usage: {
    rows: [{ organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/dashboard.md', run: '42', aic: 17 }],
    metadata
  }
};

/**
 * @param {string} generation
 * @param {Record<string, unknown>[]} auditRows
 */
function collection(generation, auditRows) {
  const collected = { 'as-of': metadata['as-of'], 'artifact-generation': generation };
  return Object.fromEntries(Object.entries(sources).map(([name, source]) => [
    name,
    { rows: name === 'audits' ? auditRows : source.rows, metadata: collected }
  ]));
}

beforeEach(async () => {
  vi.spyOn(Date, 'now').mockReturnValue(Date.parse('2026-09-10T00:00:00Z'));
  await new Promise((resolve, reject) => {
    const request = indexedDB.deleteDatabase(DATABASE_NAME);
    request.onsuccess = () => resolve(undefined);
    request.onerror = () => reject(request.error);
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('canonical view sources', () => {
  it('pushes workflow Value pages into IndexedDB and invalidates only on topology changes', async () => {
    const canonical = {
      campaigns: [],
      repositories: [
        { id: 'repository:1', owner: 'acme', name: 'one' },
        { id: 'repository:2', owner: 'acme', name: 'two' },
        { id: 'repository:3', owner: 'acme', name: 'three' }
      ],
      workflows: [
        { id: 'workflow:2', repositoryId: 'repository:2', path: '.github/workflows/b.md', name: 'B', role: 'standalone' },
        { id: 'workflow:1', repositoryId: 'repository:1', path: '.github/workflows/a.md', name: 'A', role: 'standalone' },
        { id: 'workflow:3', repositoryId: 'repository:3', path: '.github/workflows/c.md', name: 'C', role: 'standalone' }
      ]
    };
    await upsertCanonicalBatch(indexedDB, canonicalBatch(canonical), { validateRelationships: false });

    const first = await loadCanonicalViewSources(indexedDB, {}, {
      sourceNames: ['value-workflow-overview'],
      queries: dashboardQueries,
      pagination: { 'value-workflow-overview': { limit: 1 } }
    });
    expect(first['value-workflow-overview'].rows).toMatchObject([{
      id: 'workflow:1', repositoryId: 'repository:1', path: '.github/workflows/a.md'
    }]);
    expect(first['value-workflow-overview'].metadata).toMatchObject({
      'page-size': 1, 'page-row-count': 1, 'has-more': true
    });
    expect(first['value-workflow-overview'].metadata).not.toHaveProperty('total-row-count');
    const token = first['value-workflow-overview'].continuationToken;
    expect(token).toEqual(expect.any(String));

    await upsertCanonicalBatch(indexedDB, canonicalBatch({
      workflows: [{ ...canonical.workflows[0], name: 'Renamed B' }]
    }), { validateRelationships: false });
    const second = await loadCanonicalViewSources(indexedDB, {}, {
      sourceNames: ['value-workflow-overview'],
      queries: dashboardQueries,
      pagination: { 'value-workflow-overview': { limit: 1, continuationToken: token } }
    });
    expect(second['value-workflow-overview'].rows).toMatchObject([{
      id: 'workflow:2', repositoryId: 'repository:2'
    }]);

    await upsertCanonicalBatch(indexedDB, canonicalBatch({
      workflows: [{ id: 'workflow:4', repositoryId: 'repository:3', path: '.github/workflows/d.md' }]
    }), { validateRelationships: false });
    await expect(loadCanonicalViewSources(indexedDB, {}, {
      sourceNames: ['value-workflow-overview'],
      queries: dashboardQueries,
      pagination: { 'value-workflow-overview': { limit: 1, continuationToken: token } }
    })).rejects.toThrow('Invalid or stale continuation token');
  });

  it('executes temporal history through canonical IndexedDB projections and the worker query boundary', async () => {
    await processDataRequest({ operation: 'query-canonical-database-diagnostics' });
    for (const [run, attempt, startedAt] of [
      ['42', 2, '2026-09-09T04:00:00Z'],
      ['43', 1, '2026-09-08T04:00:00Z']
    ]) {
      await putCanonicalRecord('runs', {
        id: `github:run:${run}`, owner: 'githubnext', repository: 'gh-cao',
        githubRunId: run, attempt, startedAt
      });
    }
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
      operation: 'load-dashboard-query-sources',
      sources,
      queries: [{
        name: 'temporal-history', subject: 'Run attempt history from canonical records.', from: 'runs',
        compute: [{ as: 'direction', function: 'concat', args: [{ value: 'increase' }, { value: '' }] }],
        'temporal-series': {
          time: 'started-at', series: 'repository', shape: 'groups', carry: ['direction'],
          measures: [{ field: 'run-attempt', kind: 'primary' }], trend: { direction: 'direction' }
        }
      }],
      sourceNames: ['temporal-history']
    }));
    expect(result['temporal-history'].metadata.availability).toBe('available');
    expect(result['temporal-history'].rows).toEqual([expect.objectContaining({
      'trend-start-value': 1, 'trend-end-value': 2, 'trend-delta': 1,
      'trend-assessment': 'improving', 'trend-observation-count': 2,
      points: [
        expect.objectContaining({ x: '2026-09-08T04:00:00Z', y: 1 }),
        expect.objectContaining({ x: '2026-09-09T04:00:00Z', y: 2 })
      ]
    })]);
  });

  it.each(['acme/control', 'github/gh-aw'])('attributes a retained worker issue in %s after a normalized Activity import', async (issueRepository) => {
    const inventory = {
      campaigns: { rows: [{ campaign: 'repo-assist', 'campaign-name': 'Repo Assist' }], metadata },
      repositories: { rows: [{ organization: 'acme', repository: 'control' }], metadata },
      workflows: {
        rows: [{
          organization: 'acme', repository: 'control',
          workflow: '.github/workflows/repo-assist-maintenance.md',
          campaign: 'repo-assist', 'workflow-role': 'worker',
          'workflow-registry-state': 'active', 'workflow-active': 'true'
        }],
        metadata
      }
    };
    await ingestDashboardSources(indexedDB, inventory, { deferMaintenance: true });
    const workflowId = 'workflow:acme%2Fcontrol%3A.github%2Fworkflows%2Frepo-assist-maintenance.md';
    const runId = 'github:run:acme/control:38530688831';
    const records = [
      {
        collection: 'workflows',
        record: {
          id: workflowId, repositoryId: 'repository:acme%2Fcontrol',
          name: 'Repo Assist maintenance · github/gh-aw · review',
          path: '.github/workflows/repo-assist-maintenance.md',
          state: 'unknown', observedAt: metadata['as-of']
        }
      },
      {
        collection: 'runs',
        record: {
          id: runId, repositoryId: 'repository:acme%2Fcontrol', workflowId,
          owner: 'acme', repository: 'control', githubRunId: '38530688831',
          workflowPath: '.github/workflows/repo-assist-maintenance.md',
          attempt: 1, status: 'completed', conclusion: 'success',
          observedAt: metadata['as-of'], ghAwVersion: 'v0.91.5'
        }
      },
      {
        collection: 'issues',
        record: {
          id: `github:issue:${issueRepository}:2`, runId, type: 'safe_output.result',
          timestamp: metadata['as-of'], observedAt: metadata['as-of'],
          source: 'safe_outputs', summary: 'Maintenance review proposal',
          correlationId: `https://github.com/${issueRepository}/issues/2`,
          repositoryFullName: issueRepository, number: 2,
          githubEntityType: 'issue', isPullRequest: false, state: 'open', closed: false,
          sequence: 0
        }
      }
    ];
    const payload = [
      { kind: 'metadata', schemaVersion: CANONICAL_SCHEMA_VERSION, ingestionVersion: 4, phase: 'all', records: records.length },
      ...records.map((record) => ({ kind: 'record', ...record }))
    ].map((record) => JSON.stringify(record)).join('\n') + '\n';
    await ingestNormalizedJsonl(indexedDB, (async function* () { yield payload; })(), {
      payloadScope: 'activity-reports', payloadIdentity: 'a'.repeat(64), deferMaintenance: true
    });
    await expect(ingestDashboardSources(indexedDB, inventory, { deferMaintenance: true })).resolves.toMatchObject({ skipped: true });
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
      operation: 'load-dashboard-query-sources', sources: inventory,
      queries: dashboardQueries, sourceNames: ['campaign-worker-issues', 'campaign-issue-tab-counts']
    }));
    expect(result['campaign-worker-issues'].metadata.availability).toBe('available');
    expect(result['campaign-worker-issues'].rows).toEqual([
      expect.objectContaining({
        campaign: 'repo-assist', organization: 'acme', repository: 'control',
        workflow: '.github/workflows/repo-assist-maintenance.md',
        run: '38530688831', 'issue-state': 'open',
        'issue-link': expect.objectContaining({ href: `https://github.com/${issueRepository}/issues/2` })
      })
    ]);
    expect(result['campaign-issue-tab-counts'].rows).toEqual([{ campaign: 'repo-assist', items: 1 }]);
  });

  it('summarizes canonical database diagnostics inside the query layer', async () => {
    const diagnostics = /** @type {import('../../src/diagnostics.js').DatabaseDiagnostics} */ (await processDataRequest({
      operation: 'query-canonical-database-diagnostics'
    }));

    expect(diagnostics.schemaVersion).toBeGreaterThan(0);
    expect(diagnostics.counts).toEqual(expect.objectContaining({
      campaigns: 0,
      repositories: 0,
      workflows: 0,
      runs: 0
    }));
    expect(diagnostics.relationshipErrors).toEqual([]);
    expect(diagnostics.duplicateRecordIds).toEqual(expect.objectContaining({
      campaigns: [],
      repositories: [],
      workflows: [],
      runs: []
    }));
  });

  it('finds canonical relationship failures through declarative database queries', async () => {
    await processDataRequest({ operation: 'query-canonical-database-diagnostics' });
    await putCanonicalRecord('workflows', {
      id: 'workflow:orphan',
      repositoryId: 'repository:missing'
    });

    const diagnostics = /** @type {import('../../src/diagnostics.js').DatabaseDiagnostics} */ (await processDataRequest({
      operation: 'query-canonical-database-diagnostics'
    }));

    expect(diagnostics.counts.workflows).toBe(1);
    expect(diagnostics.relationshipErrors).toEqual([
      'workflow:orphan.repositoryId does not reference an existing repository'
    ]);
  });

  it('matches declarative counts for every canonical database table', async () => {
    await loadCanonicalViewSources(indexedDB, sources, { ingest: true });
    const tableNames = DATABASE_STORES.filter((store) => [...ENTITY_STORES, 'transactions'].includes(store));
    const canonical = await queryCanonicalViewSources(indexedDB, sources, tableNames);
    const definitions = tableNames.map((table) => ({
      name: `${table}-count`,
      from: table,
      aggregate: {
        values: [{ field: String(CANONICAL_DATABASE_SCHEMA[table].keyPath), as: 'count', reducer: 'count' }]
      }
    }));
    const collectionReads = vi.spyOn(IDBObjectStore.prototype, 'getAll');
    const nativeCounts = vi.spyOn(IDBObjectStore.prototype, 'count');
    const requested = definitions.map(({ name }) => name);

    const native = await queryNativeCountSources(indexedDB, sources, definitions, requested);
    const declarative = executeDashboardQueries(definitions, canonical, requested);

    expect(Object.keys(native)).toEqual(requested);
    for (const name of requested) {
      expect(native[name].rows).toEqual(declarative[name].rows.length
        ? declarative[name].rows : [{ count: 0 }]);
    }
    expect(nativeCounts).toHaveBeenCalledTimes(tableNames.length);
    expect(collectionReads).not.toHaveBeenCalled();
  });

  it('does not expose physical shard accounting through native source counts', async () => {
    await loadCanonicalViewSources(indexedDB, sources, { ingest: true });
    const definition = {
      name: 'internal-count', from: 'storageShards',
      aggregate: { values: [{ field: 'id', as: 'count', reducer: 'count' }] }
    };
    const counts = vi.spyOn(IDBObjectStore.prototype, 'count');
    expect(await queryNativeCountSources(indexedDB, sources, [definition], ['internal-count'])).toEqual({});
    expect(counts).not.toHaveBeenCalled();
  });

  it('projects canonical operational values with campaign fields', async () => {
    await processDataRequest({ operation: 'query-canonical-database-diagnostics' });
    await putCanonicalRecord('repositories', {
      id: 'repository:fixture',
      owner: 'githubnext',
      name: 'gh-cao',
      fullName: 'githubnext/gh-cao',
      repositoryLink: { href: 'https://github.com/githubnext/gh-cao' }
    });
    await putCanonicalRecord('campaigns', {
      id: 'campaign:dependabot',
      slug: 'dependabot',
      name: 'Dependabot',
      icon: 'dependabot',
      campaignLink: { href: '#page-campaign?campaign=dependabot' }
    });
    await putCanonicalRecord('operationalValues', {
      id: 'operational-value:dependabot',
      repositoryId: 'repository:fixture',
      repository: 'githubnext/gh-cao',
      campaign: 'dependabot',
      valueId: 'dependabot-vulnerability-alerts',
      'cadence-days': 7,
      value: 4,
      timestamp: '2026-09-09T04:00:00Z',
      observedAt: '2026-09-09T04:00:00Z'
    });

    const result = await queryCanonicalViewSources(indexedDB, sources, ['operational-values']);

    expect(result['operational-values'].rows).toEqual([expect.objectContaining({
      organization: 'githubnext',
      repository: 'gh-cao',
      campaign: 'dependabot',
      'campaign-name': 'Dependabot',
      'operational-value-definition': 'dependabot-vulnerability-alerts',
      'cadence-days': 7,
      'operational-value': 4,
      'observed-at': '2026-09-09T04:00:00Z'
    })]);
  });

  it('retains campaignless repository observations through the canonical worker boundary', async () => {
    await processDataRequest({ operation: 'query-canonical-database-diagnostics' });
    const records = [];
    for (const [index, value] of [4, 0].entries()) {
      const day = index + 8;
      const timestamp = `2026-09-${String(day).padStart(2, '0')}T04:00:00Z`;
      records.push({
        schema_version: 2, kind: 'operational_value',
        operational_value: {
          repository: 'octo/standalone', value_id: 'remaining-alerts', value, timestamp,
          workflow_slug: 'standalone', metric_role: 'primary',
          metric_name: 'Remaining alerts', metric_unit: 'alerts',
          metric_direction: 'decrease', maturity_status: 'matured',
          evaluator_digest: 'a'.repeat(64), cadence_days: 1,
          measurement_kind: 'snapshot', coverage_status: 'complete',
          window_start: `2026-09-${String(day - 1).padStart(2, '0')}T04:00:00Z`,
          window_end: timestamp
        }
      });
    }
    await ingestCachedGhAwJsonl(indexedDB, `${records.map(row => JSON.stringify(row)).join('\n')}\n`, {
      now: Date.parse(metadata['as-of'])
    });
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
      operation: 'query-canonical-dashboard', sourceNames: ['operational-values'],
      context: { queries: dashboardQueries, pages: authoritativeDashboard.dashboard.pages }
    }));
    expect(result['operational-values'].metadata?.availability).toBe('available');
    expect(result['operational-values'].rows).toHaveLength(2);
    expect(result['operational-values'].rows).toEqual(expect.arrayContaining(
      [4, 0].map(value => expect.objectContaining({
        organization: 'octo', repository: 'standalone', 'workflow-slug': 'standalone',
        'operational-value-definition': 'remaining-alerts', 'operational-value': value,
        'operational-value-unit': 'alerts', 'operational-value-direction': 'decrease',
        'evaluator-digest': 'a'.repeat(64), 'coverage-status': 'complete'
      }))
    ));
  });

  it('projects safe-output links declaratively for issues and pull requests', async () => {
    await loadCanonicalViewSources(indexedDB, sources, { ingest: true });
    await putCanonicalRecord('issues', {
      id: 'event:safe-output:issue',
      runId: 'github:run:42:attempt:2',
      timestamp: '2026-09-09T04:00:10Z',
      source: 'safe-output',
      type: 'safe_output.result',
      summary: 'Created issue',
      correlationId: 'https://github.com/githubnext/gh-cao/issues/123',
      safeOutputType: 'create_issue',
      isPullRequest: false,
      number: 123
    });
    await putCanonicalRecord('issues', {
      id: 'event:safe-output:pull-request',
      runId: 'github:run:42:attempt:2',
      timestamp: '2026-09-09T04:00:20Z',
      source: 'safe-output',
      type: 'safe_output.result',
      summary: 'Created pull request',
      correlationId: 'https://github.com/githubnext/gh-cao/pull/124',
      safeOutputType: 'create_pull_request',
      isPullRequest: true,
      number: 124
    });

    const projected = await queryCanonicalViewSources(indexedDB, sources, ['outcomes']);

    expect(projected.outcomes.rows).toEqual(expect.arrayContaining([
      expect.objectContaining({
        'outcome-title': 'Created issue',
        'issue-link': { href: 'https://github.com/githubnext/gh-cao/issues/123', label: 'View issue' },
        'pull-request-link': null,
        'external-link': { href: 'https://github.com/githubnext/gh-cao/issues/123', label: 'View issue' }
      }),
      expect.objectContaining({
        'outcome-title': 'Created pull request',
        'issue-link': null,
        'pull-request-link': { href: 'https://github.com/githubnext/gh-cao/pull/124', label: 'View pull request' },
        'external-link': { href: 'https://github.com/githubnext/gh-cao/pull/124', label: 'View pull request' }
      })
    ]));
  });

  it('returns the same zero counts as declarative execution for empty tables', async () => {
    const tableNames = DATABASE_STORES.filter((store) => [...ENTITY_STORES, 'transactions'].includes(store));
    const definitions = tableNames.map((table) => ({
      name: `${table}-count`,
      from: table,
      aggregate: {
        values: [{ field: String(CANONICAL_DATABASE_SCHEMA[table].keyPath), as: 'count', reducer: 'count' }]
      }
    }));
    const requested = definitions.map(({ name }) => name);
    const emptyMetadata = /** @type {import('../../src/presenter.js').SourceMetadata} */ ({
      'source-id': 'empty',
      'source-kind': 'database-query',
      'as-of': metadata['as-of'],
      'retrieved-at': metadata['as-of'],
      completeness: 'complete',
      freshness: 'fresh',
      availability: 'empty'
    });
    const emptySources = Object.fromEntries(tableNames.map((table) => [
      table,
      { source: table, rows: [], metadata: emptyMetadata }
    ]));

    const native = await queryNativeCountSources(indexedDB, emptySources, definitions, requested);
    const declarative = executeDashboardQueries(definitions, emptySources, requested);

    for (const name of requested) {
      expect(native[name].rows).toEqual([{ count: 0 }]);
      expect(native[name].rows).toEqual(declarative[name].rows);
    }
  });

  it('resolves the Overview repository metric with native IndexedDB count', async () => {
    await loadCanonicalViewSources(indexedDB, sources, { ingest: true });
    const collectionReads = vi.spyOn(IDBObjectStore.prototype, 'getAll');
    const nativeCounts = vi.spyOn(IDBObjectStore.prototype, 'count');

    const result = await queryNativeCountSources(
      indexedDB,
      sources,
      dashboardQueries,
      ['overview-registered-repository-summary']
    );

    expect(result['overview-registered-repository-summary'].rows).toEqual([
      { 'registered-repositories': 1 }
    ]);
    expect(nativeCounts).toHaveBeenCalledOnce();
    expect(collectionReads).not.toHaveBeenCalled();
  });

  it('uses native counts only for declared non-null logical identity fields', async () => {
    await loadCanonicalViewSources(indexedDB, sources, { ingest: true });
    const nativeCounts = vi.spyOn(IDBObjectStore.prototype, 'count');
    const definitions = [
      {
        name: 'repository-logical-count',
        from: 'repositories',
        aggregate: {
          values: [{ field: 'repository', as: 'repositories', reducer: 'count' }]
        }
      },
      {
        name: 'repository-optional-count',
        from: 'repositories',
        aggregate: {
          values: [{ field: 'rollout-mode', as: 'repositories', reducer: 'count' }]
        }
      }
    ];

    const result = await queryNativeCountSources(
      indexedDB,
      sources,
      definitions,
      ['repository-logical-count', 'repository-optional-count']
    );

    expect(result['repository-logical-count'].rows).toEqual([{ repositories: 1 }]);
    expect(result['repository-optional-count'].rows).toEqual([{ repositories: 1 }]);
    expect(nativeCounts).toHaveBeenCalledOnce();
  });

  it('resolves literal-labelled indexing table counts with native IndexedDB counts', async () => {
    await loadCanonicalViewSources(indexedDB, sources, { ingest: true });
    const collectionReads = vi.spyOn(IDBObjectStore.prototype, 'getAll');
    const nativeCounts = vi.spyOn(IDBObjectStore.prototype, 'count');
    const required = [
      'indexing-campaigns-table-count',
      'indexing-repositories-table-count',
      'indexing-workflows-table-count',
      'indexing-runs-table-count',
      'indexing-domains-table-count',
      'indexing-tools-table-count',
      'indexing-audits-table-count',
      'indexing-issues-table-count',
      'indexing-operational-values-table-count',
      'indexing-transactions-table-count'
    ];

    const result = await queryNativeCountSources(
      indexedDB,
      sources,
      dashboardQueries,
      required
    );

    expect(result['indexing-tools-table-count'].rows).toEqual([
      { table: 'tools', records: 1 }
    ]);
    expect(result['indexing-domains-table-count'].rows).toEqual([]);
    expect(Object.keys(result)).toEqual(required.filter((name) => (
      name !== 'indexing-operational-values-table-count'
    )));
    expect(nativeCounts).toHaveBeenCalledTimes(required.length - 1);
    expect(collectionReads).not.toHaveBeenCalled();
  });

  it('does not reload native count dependencies through the canonical query boundary', async () => {
    await loadCanonicalViewSources(indexedDB, sources, { ingest: true });
    const requested = ['indexing-database-table-counts'];
    const dependencies = resolveDashboardQuerySources(dashboardQueries, requested);
    const native = await queryNativeCountSources(indexedDB, sources, dashboardQueries, dependencies);
    const nativeNames = new Set(Object.keys(native));
    const required = resolveDashboardQuerySources(dashboardQueries, requested, nativeNames)
      .filter((name) => !nativeNames.has(name));
    const collectionReads = vi.spyOn(IDBObjectStore.prototype, 'getAll');
    const database = await queryCanonicalViewSources(indexedDB, sources, required);
    const results = executeDashboardQueries(dashboardQueries, { ...database, ...native }, requested);

    expect(collectionReads.mock.contexts.map((store) => /** @type {IDBObjectStore} */ (store).name))
      .toEqual(['repositories', 'campaigns', 'operationalValues']);
    expect(results['indexing-database-table-counts'].rows).toEqual(expect.arrayContaining([
      { table: 'runs', records: 1 },
      { table: 'tools', records: 1 }
    ]));
  });

  it('pushes declarative failed-run predicates into the canonical run index', async () => {
    await loadCanonicalViewSources(indexedDB, sources, { ingest: true });
    const collectionReads = vi.spyOn(IDBObjectStore.prototype, 'getAll');
    const indexedReads = vi.spyOn(IDBIndex.prototype, 'getAll');

    const result = await queryNativeCountSources(
      indexedDB,
      sources,
      dashboardQueries,
      ['failed-runs']
    );

    expect(result['failed-runs']).toMatchObject({
      source: 'failed-runs',
      rows: [{ repository: 'gh-cao', run: '42', 'run-conclusion': 'failure' }],
      metadata: { 'source-kind': 'derived', 'query-name': 'failed-runs' }
    });
    expect(indexedReads).toHaveBeenCalledTimes(4);
    expect(collectionReads).not.toHaveBeenCalled();
  });

  it('retains orphan evidence when a computed run-key constraint can match missing run identity', async () => {
    await loadCanonicalViewSources(indexedDB, sources, { ingest: true });
    await putCanonicalRecord('audits', { id: 'orphan-audit', runId: 'absent-run', type: 'workflow.info' });
    const definitions = [{
      name: 'orphan-run-key', from: 'audits',
      compute: [{ as: 'run-key', function: 'concat', args: [
        { field: 'organization' }, { value: '/' }, { field: 'repository' }, { value: ':' }, { field: 'run' }
      ] }],
      select: [{ field: 'event' }, { field: 'run-key' }]
    }];
    const canonical = await queryCanonicalViewSources(indexedDB, sources, ['audits']);
    const projected = executeDashboardQueries(definitions, canonical, ['orphan-run-key']);
    const key = projected['orphan-run-key'].rows.find(row => row.event === 'orphan-audit')?.['run-key'];
    expect(key).toBe('/:');
    const scoped = [...definitions, {
      name: 'orphan-selected', from: 'orphan-run-key',
      filter: { predicates: [{ field: 'run-key', equals: key }] },
      select: [{ field: 'event' }]
    }];
    const native = await queryNativeCountSources(indexedDB, sources, scoped, ['orphan-selected']);
    expect(native).not.toHaveProperty('orphan-selected');
    const result = await loadCanonicalViewSources(indexedDB, sources, {
      queries: scoped, sourceNames: ['orphan-selected']
    });
    expect(result['orphan-selected'].rows).toEqual([{ event: 'orphan-audit' }]);
  });

  it('streams row-local computed daily run aggregates without changing declarative results', async () => {
    await loadCanonicalViewSources(indexedDB, sources, { ingest: true });
    const queries = [{
      name: 'retained-daily-conclusions', from: 'runs',
      compute: [{ as: 'day', function: 'date-day', args: [{ field: 'started-at' }] }],
      aggregate: {
        by: ['day', 'run-conclusion'],
        values: [{ field: 'run', as: 'runs', reducer: 'count' }]
      }
    }];

    const native = await queryNativeCountSources(
      indexedDB,
      sources,
      queries,
      ['retained-daily-conclusions']
    );

    const canonical = await queryCanonicalViewSources(indexedDB, sources, ['runs']);
    const expected = executeDashboardQueries(queries, canonical, ['retained-daily-conclusions']);
    expect(native['retained-daily-conclusions'].rows).toEqual(expected['retained-daily-conclusions'].rows);
  });

  it('streams eligible transformed counts and declines joins, invalid queries, and non-table sources', async () => {
    await loadCanonicalViewSources(indexedDB, sources, { ingest: true });
    const definitions = [
      {
        name: 'repository-count',
        from: 'repositories',
        aggregate: { values: [{ field: 'id', as: 'repositories', reducer: 'count' }] }
      },
      {
        name: 'filtered-run-count',
        from: 'runs',
        filter: { predicates: [{ field: 'run-status', equals: 'completed' }] },
        aggregate: { values: [{ field: 'id', as: 'runs', reducer: 'count' }] }
      },
      {
        name: 'joined-run-count',
        from: 'runs',
        joins: [{
          source: 'workflows',
          type: 'left',
          on: [{ left: 'workflowId', right: 'id' }],
          fields: [{ field: 'id', as: 'workflow-id' }]
        }],
        aggregate: { values: [{ field: 'id', as: 'runs', reducer: 'count' }] }
      },
      {
        name: 'grouped-run-count',
        from: 'runs',
        aggregate: {
          by: ['conclusion'],
          values: [{ field: 'id', as: 'runs', reducer: 'count' }]
        }
      },
      {
        name: 'non-key-run-count',
        from: 'runs',
        aggregate: { values: [{ field: 'githubRunId', as: 'runs', reducer: 'count' }] }
      },
      {
        name: 'distinct-run-count',
        from: 'runs',
        aggregate: { values: [{ field: 'id', as: 'runs', reducer: 'distinct-count' }] }
      },
      {
        name: 'limited-run-count',
        from: 'runs',
        aggregate: { values: [{ field: 'id', as: 'runs', reducer: 'count' }] },
        limit: 1
      },
      {
        name: 'selected-run-count',
        from: 'runs',
        aggregate: { values: [{ field: 'id', as: 'runs', reducer: 'count' }] },
        select: [{ field: 'runs' }]
      },
      {
        name: 'events-count',
        from: 'events',
        aggregate: { values: [{ field: 'id', as: 'events', reducer: 'count' }] }
      },
      {
        name: 'missing-aggregate',
        from: 'runs'
      }
    ];
    const requested = definitions.map(({ name }) => name);
    const nativeCounts = vi.spyOn(IDBObjectStore.prototype, 'count');

    const projected = await queryNativeCountSources(
      indexedDB,
      sources,
      definitions,
      requested
    );

    expect(Object.keys(projected)).toEqual([
      'repository-count', 'filtered-run-count', 'grouped-run-count', 'non-key-run-count',
      'distinct-run-count', 'limited-run-count', 'selected-run-count'
    ]);
    const canonical = await queryCanonicalViewSources(indexedDB, sources, ['repositories', 'runs']);
    const expected = executeDashboardQueries(definitions, canonical, requested);
    for (const name of Object.keys(projected)) expect(projected[name].rows).toEqual(expected[name].rows);
    expect(projected['repository-count']).toMatchObject({
      source: 'repository-count',
      rows: [{ repositories: 1 }],
      metadata: {
        'source-kind': 'derived',
        availability: 'available',
        'query-name': 'repository-count'
      }
    });
    expect(nativeCounts.mock.instances.map((store) => (
      /** @type {IDBObjectStore} */ (store).name
    ))).toEqual(['repositories']);
  });

  it('projects retained ingestion transactions for dashboard inspection', async () => {
    await recordTransaction(indexedDB, {
      id: 'ingest-jsonl:current:test',
      kind: 'ingest-jsonl',
      createdAt: '2026-09-09T05:00:00Z',
      payloadScope: 'gh-aw-jsonl',
      payloadHash: 'abc123',
      records: 12,
      committedRecords: 10,
      unenrichedRuns: 2
    });
    await recordTransaction(indexedDB, {
      id: 'ingest-jsonl:current:newer',
      kind: 'ingest-jsonl',
      createdAt: '2026-09-09T06:00:00Z',
      payloadScope: 'gh-aw-jsonl',
      committedRecords: 4
    });

    const projected = await queryCanonicalViewSources(indexedDB, sources, ['transactions']);

    expect(projected.transactions).toMatchObject({
      source: 'transactions',
      metadata: { 'source-kind': 'database-query', availability: 'available' },
      rows: [
        {
          id: 'ingest-jsonl:current:newer',
          kind: 'ingest-jsonl',
          createdAt: '2026-09-09T06:00:00Z',
          payloadScope: 'gh-aw-jsonl',
          committedRecords: 4
        },
        {
          id: 'ingest-jsonl:current:test',
          kind: 'ingest-jsonl',
          createdAt: '2026-09-09T05:00:00Z',
          payloadScope: 'gh-aw-jsonl',
          payloadHash: 'abc123',
          records: 12,
          committedRecords: 10,
          unenrichedRuns: 2
        }
      ]
    });
  });

  it('queries only the canonical payload requested by a view', async () => {
    await loadCanonicalViewSources(indexedDB, sources, { ingest: true });
    /** @type {{ databaseMs: number, projectionMs: number, totalMs: number, recordsRead: number, stores: string[] } | undefined} */
    let metrics;

    const canonical = await queryCanonicalViewSources(
      indexedDB,
      sources,
      ['runs'],
      { onMetrics: (value) => { metrics = value; } }
    );
    const projected = executeDashboardQueries(
      dashboardQueries,
      canonical,
      ['failed-runs']
    );

    expect(Object.keys(projected)).toEqual(['failed-runs']);
    expect(projected['failed-runs']).toMatchObject({
      source: 'failed-runs',
      rows: [{ repository: 'gh-cao', run: '42', 'run-conclusion': 'failure' }],
      metadata: { 'source-kind': 'derived' }
    });
    expect(metrics).toMatchObject({
      databaseMs: expect.any(Number),
      projectionMs: expect.any(Number),
      totalMs: expect.any(Number),
      recordsRead: expect.any(Number),
      stores: ['workflows', 'runs']
    });
    expect(metrics?.totalMs).toBeGreaterThanOrEqual(metrics?.databaseMs ?? 0);
  });

  it('projects the narrow run fields required by Overview', async () => {
    await loadCanonicalViewSources(indexedDB, sources, { ingest: true });

    const projected = await queryCanonicalViewSources(indexedDB, sources, ['overview-runs']);

    expect(projected['overview-runs']).toMatchObject({
      source: 'overview-runs',
      rows: [{
        id: expect.any(String),
        organization: 'githubnext',
        repository: 'gh-cao',
        workflow: '.github/workflows/dashboard.md',
        'workflow-name': 'Published registry name',
        'workflow-role': 'worker',
        campaign: 'dashboard',
        'campaign-name': 'CAO Dashboard',
        run: '42',
        'target-repository': 'github/gh-aw',
        'run-status': 'completed',
        'run-conclusion': 'failure',
        'rollout-mode': 'review'
      }],
      metadata: { 'source-kind': 'database-query' }
    });
  });

  it('projects campaign rows and workflow membership from canonical records', async () => {
    await loadCanonicalViewSources(indexedDB, sources, { ingest: true });

    const projected = await queryCanonicalViewSources(indexedDB, sources, ['campaigns', 'workflows']);

    expect(projected.campaigns).toMatchObject({
      source: 'campaigns',
      rows: [{
        campaign: 'dashboard',
        'campaign-name': 'CAO Dashboard',
        'campaign-description': 'Deploy the dashboard.',
        'campaign-mode': 'review',
        'campaign-worker-count': 1
      }],
      metadata: { 'source-kind': 'database-query' }
    });
    expect(projected.workflows.rows).toEqual([
      expect.objectContaining({
        campaign: 'dashboard',
        'campaign-name': 'CAO Dashboard',
        workflow: '.github/workflows/dashboard.md',
        'workflow-id': '501',
        'workflow-name': 'Published registry name',
        'workflow-role': 'worker',
        'workflow-registry-state': 'active',
        'admission-status': 'admitted',
        'inventory-ready': true,
        'created-at': '2026-09-01T00:00:00Z',
        'updated-at': '2026-09-08T00:00:00Z',
        'workflow-link': {
          relation: 'workflow',
          href: 'https://github.com/githubnext/gh-cao/actions/workflows/501'
        }
      })
    ]);
  });

  it('projects run records and MCP calls through declarative canonical queries', async () => {
    await loadCanonicalViewSources(indexedDB, sources, { ingest: true });

    const projected = await queryCanonicalViewSources(indexedDB, {
      ...sources,
      'mcp-calls': { rows: [], metadata }
    }, ['tools', 'mcp-calls']);

    expect(projected.tools).toMatchObject({
      source: 'tools',
      rows: [{
        organization: 'githubnext',
        repository: 'gh-cao',
        run: '42',
        event: 'event:tool-call',
        'event-type': 'mcp.tool_call'
      }],
      metadata: { 'source-kind': 'database-query' }
    });
    expect(projected['mcp-calls']).toMatchObject({
      source: 'mcp-calls',
      rows: [{
        organization: 'githubnext',
        repository: 'gh-cao',
        run: '42',
        'mcp-observation': 'event:tool-call',
        'mcp-status': 'requested',
        'mcp-server-version': '1.0.0',
        'mcp-protocol-version': '2025-06-18',
        'request-bytes': 128,
        'response-bytes': 256
      }],
      metadata: { 'source-kind': 'database-query' }
    });
  });

  it('ignores populated report projections and reads canonical evidence instead', async () => {
    await loadCanonicalViewSources(indexedDB, sources, { ingest: true });
    const published = { rows: [{ 'mcp-observation': 'published-call' }], metadata };

    const projected = await queryDatabaseSources(
      indexedDB,
      { 'mcp-calls': published },
      ['mcp-calls']
    );

    expect(projected['mcp-calls'].rows).toHaveLength(1);
    expect(projected['mcp-calls'].rows[0]['mcp-observation']).toBe('event:tool-call');
    expect(projected['mcp-calls'].metadata['source-kind']).toBe('database-query');
  });

  it('reprojects populated unavailable MCP sources before deriving top tools', async () => {
    await loadCanonicalViewSources(indexedDB, sources, {
      ingest: true,
      sourceNames: ['mcp-calls']
    });
    const unavailable = {
      source: 'mcp-calls',
      rows: [{ 'mcp-observation': 'stale-call', 'mcp-tool': 'stale_tool' }],
      metadata: { ...metadata, availability: 'unavailable' }
    };

    const projected = await loadCanonicalViewSources(indexedDB, {
      ...sources,
      'mcp-calls': unavailable
    }, {
      sourceNames: ['mcp-tool-totals', 'mcp-top-tools'],
      queries: dashboardQueries
    });
    const topTools = /** @type {import('../../src/presenter.js').LogicalSourceInput} */ (projected['mcp-top-tools']);

    expect(topTools.metadata.availability).toBe('available');
    expect(topTools.rows).toHaveLength(1);
    expect(topTools.rows[0]['mcp-tool']).not.toBe('stale_tool');
  });

  it('does not read database stores for an undeclared logical source', async () => {
    const collectionReads = vi.spyOn(IDBObjectStore.prototype, 'getAll');

    const projected = await queryDatabaseSources(indexedDB, {}, ['missing-logical-source']);

    expect(projected['missing-logical-source']).toMatchObject({
      rows: [],
      metadata: { availability: 'unavailable' }
    });
    expect(collectionReads).not.toHaveBeenCalled();
  });

  it('queries work items canonically and rejects undeclared report sources', async () => {
    const loaded = await loadCanonicalViewSources(indexedDB, sources, {
      ingest: true,
      sourceNames: ['work-items', 'security-findings']
    });

    const projected = await queryCanonicalViewSources(
      indexedDB,
      sources,
      ['work-items', 'security-findings']
    );

    expect(projected['work-items']).toMatchObject({
      source: 'work-items',
      rows: [{ 'work-item-id': 'githubnext/gh-cao:.github/workflows/dashboard.md', 'lifecycle-state': 'blocked' }]
    });
    expect(projected['security-findings']).toMatchObject({
      source: 'security-findings',
      rows: [],
      metadata: { availability: 'unavailable', error: expect.stringContaining('No canonical database query') }
    });
    expect(projected).not.toHaveProperty('usage');
    expect(loaded['work-items']).toMatchObject({
      source: 'work-items',
      rows: [{ 'work-item-id': 'githubnext/gh-cao:.github/workflows/dashboard.md', 'lifecycle-state': 'blocked' }]
    });
    expect(loaded['security-findings']).toMatchObject({
      source: 'security-findings',
      rows: [],
      metadata: { availability: 'unavailable' }
    });
    expect(Object.keys(loaded)).toEqual(['work-items', 'security-findings']);
  });

  it('projects failed-run evidence from the active canonical generation', async () => {
    const projected = await loadCanonicalViewSources(indexedDB, sources, {
      ingest: true,
      sourceNames: ['campaign-problem-items', 'failed-runs', 'runs', 'repositories', 'workflows'],
      queries: dashboardQueries
    });

    expect(projected['campaign-problem-items']).toMatchObject({
      source: 'campaign-problem-items',
      rows: [{
        'problem-title': 'Build failed',
        'error-signature-label': 'Agent process exited unexpectedly',
        'failure-message': 'Build failed',
        'failure-log': '##[error]Build failed',
        'run-link': { relation: 'run', href: 'https://github.com/githubnext/gh-cao/actions/runs/42', label: 'Run 42' },
        'workflow-source-link': { relation: 'workflow', href: 'https://github.com/githubnext/gh-cao/actions/workflows/501' },
        'status-detail': 'Build failed'
      }]
    });
    expect(projected['failed-runs']).toMatchObject({
      source: 'failed-runs',
      rows: [{ repository: 'gh-cao', run: '42', 'run-attempt': 2, 'run-conclusion': 'failure', 'failure-detail': 'Build failed' }],
      metadata: { 'source-kind': 'derived', availability: 'available' }
    });
    expect(projected.runs).toMatchObject({
      source: 'runs',
      rows: [{
        repository: 'gh-cao', run: '42', 'run-attempt': 2,
        'rollout-mode': 'review', engine: 'copilot', 'engine-version': '1.2.3',
        'gh-aw-version': 'v0.89.4',
        'requested-model': 'model-a', 'resolved-model': 'model-b',
        'admission-status': 'admitted', resource: 'actions', 'resource-wait-hours': 2
      }],
      metadata: { 'source-kind': 'database-query', availability: 'available' }
    });
    expect(projected.repositories).toMatchObject({
      source: 'repositories',
      rows: [{ organization: 'githubnext', repository: 'gh-cao' }],
      metadata: { 'source-kind': 'database-query', availability: 'available' }
    });
    expect(projected.workflows).toMatchObject({
      source: 'workflows',
      rows: [{
        organization: 'githubnext', repository: 'gh-cao',
        workflow: '.github/workflows/dashboard.md'
      }],
      metadata: { 'source-kind': 'database-query', availability: 'available' }
    });
    expect(projected).not.toHaveProperty('job-performance');
    expect(projected).not.toHaveProperty('usage');
  });

  it('projects workflow configuration onto runs when run metadata is absent', async () => {
    const input = structuredClone(sources);
    Reflect.deleteProperty(input.runs.rows[0], 'rollout-mode');
    await loadCanonicalViewSources(indexedDB, input, { ingest: true });

    const projected = await queryCanonicalViewSources(indexedDB, input, ['runs']);

    expect(projected.runs.rows).toEqual([
      expect.objectContaining({
        workflow: '.github/workflows/dashboard.md',
        'rollout-mode': 'review'
      })
    ]);
  });

  it('projects run-linked view sources from their canonical tables', async () => {
    await loadCanonicalViewSources(indexedDB, sources, { ingest: true });

    const projected = await queryCanonicalViewSources(
      indexedDB,
      sources,
      ['tools', 'audits']
    );

    expect(Object.keys(projected)).toEqual(['tools', 'audits']);
    expect(projected.tools).toMatchObject({
      source: 'tools',
      metadata: { 'source-kind': 'database-query', availability: 'available' }
    });
    expect(projected.tools.rows).toEqual([
      expect.objectContaining({
        organization: 'githubnext',
        repository: 'gh-cao',
        workflow: '.github/workflows/dashboard.md',
        run: '42',
        'run-attempt': 2,
        event: 'event:tool-call',
        'event-type': 'mcp.tool_call',
        'event-source': 'mcp',
        'event-summary': 'github.list_issues',
        'correlation-id': 'call-1',
        'request-bytes': 128,
        'response-bytes': 256
      })
    ]);
    expect(projected.audits.rows).toEqual([
      expect.objectContaining({ event: 'event:agent-turn', 'event-source': 'agent', 'event-type': 'user.message' })
    ]);
  });

  it('projects firewall event arity with canonical event types', async () => {
    const firewallSources = structuredClone(sources);
    const firewallEvent = structuredClone(firewallSources.tools.rows[0]);
    firewallEvent.event = 'event:firewall-blocked';
    firewallEvent['event-source'] = 'firewall';
    firewallEvent['event-type'] = 'firewall.http_access';
    firewallSources.tools.rows = [];
    firewallSources.domains.rows = [firewallEvent];
    await loadCanonicalViewSources(indexedDB, firewallSources, { ingest: true });

    const projected = await queryCanonicalViewSources(indexedDB, firewallSources, ['domains']);

    expect(projected.domains.rows).toEqual([
      expect.objectContaining({
        event: 'event:firewall-blocked',
        'event-source': 'firewall',
        'event-type': 'firewall.http_access',
        'request-count': 7
      })
    ]);
  });

  it('resolves retained experiment and grader evidence directly from canonical tables', async () => {
    const content = JSON.stringify({
      schema_version: 2,
      kind: 'run',
      run: {
        run_id: 84,
        run_attempt: 1,
        organization: 'githubnext',
        repository: 'githubnext/gh-cao',
        workflow_name: 'Value worker',
        workflow_path: '.github/workflows/value-worker.md',
        status: 'completed',
        conclusion: 'success',
        created_at: '2026-09-09T04:00:00Z',
        started_at: '2026-09-09T04:00:00Z',
        updated_at: '2026-09-09T04:02:00Z',
        experiments: { assignments: { prompt: 'candidate' }, counts: { ignored: 3 } },
        graders: {
          results: [{
            id: 'operational-value',
            name: 'Operational Value',
            status: 'pass',
            unit: 'count',
            direction: 'higher_is_better',
            value: 0,
            metrics: [
              { id: 'accepted-maintenance-outcomes', value: 0 },
              { id: 'eligible-maintenance-items', value: 3 },
              { id: 'unavailable-maintenance-items', value: null }
            ]
          }]
        }
      }
    });
    await ingestCachedGhAwJsonl(indexedDB, `${content}\n`, {
      now: Date.parse('2026-09-09T05:00:00Z')
    });

    const projected = await queryCanonicalViewSources(
      indexedDB,
      { evals: { source: 'evals', rows: [{ eval: 'unsupported' }], metadata: { completeness: 'complete' } } },
      ['experiments', 'experiment-assignments', 'graders', 'grader-observations',
        'evals', 'eval-observations', 'operational-values']
    );

    expect(projected.experiments.rows).toEqual([
      expect.objectContaining({ experiment: 'prompt', workflow: '.github/workflows/value-worker.md' })
    ]);
    expect(projected['experiment-assignments'].rows).toEqual([
      expect.objectContaining({ experiment: 'prompt', variant: 'candidate', run: '84' })
    ]);
    expect(projected.graders.rows).toEqual([
      expect.objectContaining({ grader: 'operational-value', unit: 'count' })
    ]);
    expect(projected['grader-observations'].rows).toEqual([
      expect.objectContaining({ grader: 'operational-value', value: 0, status: 'pass' })
    ]);
    expect(projected.evals.rows).toEqual([]);
    expect(projected.evals.metadata.completeness).toBe('unknown');
    expect(projected['eval-observations'].rows).toEqual([]);
    expect(projected['operational-values'].rows).toEqual([]);
  });

  it('projects only explicit eval answers from canonical run-linked records', async () => {
    const content = JSON.stringify({
      schema_version: 2,
      kind: 'run',
      run: {
        run_id: 85, run_attempt: 1,
        organization: 'githubnext', repository: 'githubnext/gh-cao',
        workflow_name: 'Eval worker', workflow_path: '.github/workflows/eval-worker.md',
        created_at: '2026-09-09T04:00:00Z',
        evals: [
          { id: 'correctness', answer: 'NO', runId: '85' },
          { id: 'unverified', answer: 'MAYBE' }
        ]
      }
    });
    await ingestCachedGhAwJsonl(indexedDB, `${content}\n`, {
      now: Date.parse('2026-09-09T05:00:00Z')
    });
    const projected = await queryCanonicalViewSources(indexedDB, {}, ['evals', 'eval-observations', 'audits']);
    expect(projected.evals.rows).toEqual([expect.objectContaining({ eval: 'correctness' })]);
    expect(projected['eval-observations'].rows).toEqual([
      expect.objectContaining({ eval: 'correctness', 'eval-result': 'NO', status: 'fail', run: '85' })
    ]);
    expect(projected.audits.rows.map((row) => row['event-type'])).toContain('eval.result');
    expect(projected.audits.rows.map((row) => row['event-type'])).toContain('workflow_run_eval_unclassified');
  });

  it.skip('projects token optimizer interventions without parsing issue display text', async () => {
    const records = [
      {
        schema_version: 2,
        kind: 'run',
        run: {
          run_id: 1186001,
          run_attempt: 1,
          organization: 'githubnext',
          repository: 'githubnext/gh-cao',
          workflow_name: 'Optimization / Token Optimizer',
          workflow_path: '.github/workflows/optimization-token-optimizer.md',
          status: 'completed',
          conclusion: 'success',
          created_at: '2026-09-15T04:00:00Z',
          updated_at: '2026-09-15T04:02:00Z'
        }
      },
      {
        schema_version: 2,
        kind: 'safe_output_item',
        safe_output: {
          run_id: 1186001,
          type: 'create_issue',
          url: 'https://github.com/githubnext/gh-cao/issues/11861',
          number: 11861,
          repo: 'githubnext/gh-cao',
          timestamp: '2026-09-15T04:02:00Z'
        }
      },
      {
        schema_version: 2,
        kind: 'token_efficiency_observation',
        observation: {
          schemaVersion: 1,
          observedAt: '2026-09-15T04:02:00Z',
          controlRepository: 'githubnext/gh-cao',
          optimizerRunId: '1186001',
          runAttempt: 1,
          targetRepo: 'octo/example',
          workflowPath: '.github/workflows/review.md',
          evidenceWindowStart: '2026-09-01T00:00:00Z',
          evidenceWindowEnd: '2026-09-08T00:00:00Z',
          assignmentRunId: '1185999',
          experimentId: 'review-context-v1',
          opportunityKind: 'unbounded-context-growth',
          opportunityId: 'token-opportunity:octo/example:.github/workflows/review.md:2026-09-01T00:00:00Z:2026-09-08T00:00:00Z:1185999:review-context-v1',
          evidenceState: 'complete',
          evidenceConfidence: 0.9,
          costGrain: 'invocation',
          evidenceProvenance: [{ source: 'activity', runId: '42', costGrain: 'invocation' }],
          interventionId: 'token-intervention:review-context-v1:1186001',
          interventionState: 'proposed',
          recommendationDisposition: 'unapplied',
          controlVariant: 'control',
          optimizedVariant: 'optimized',
          proposedSavingsAic: 12.5,
          attributableRunIds: ['1185999', '1186001']
        }
      },
      {
        schema_version: 2,
        kind: 'token_efficiency_lifecycle_observation',
        observation: {
          schemaVersion: 1,
          lifecycleObservationId: 'token-lifecycle:accepted-1',
          observedAt: '2026-09-16T04:02:00Z',
          controlRepository: 'githubnext/gh-cao',
          claimRunId: '1189001',
          claimRunAttempt: 1,
          actor: 'maintainer',
          optimizerRunId: '1186001',
          optimizerRunAttempt: 1,
          optimizerWorkflowPath: '.github/workflows/optimization-token-optimizer.md',
          optimizerWorkflowName: 'Optimization / Token Optimizer',
          targetRepo: 'octo/example',
          workflowPath: '.github/workflows/review.md',
          opportunityId: 'token-opportunity:octo/example:.github/workflows/review.md:2026-09-01T00:00:00Z:2026-09-08T00:00:00Z:1185999:review-context-v1',
          interventionId: 'token-intervention:review-context-v1:1186001',
          experimentId: 'review-context-v1',
          controlVariant: 'control',
          optimizedVariant: 'optimized',
          proposedSavingsAic: 12.5,
          previousInterventionState: 'proposed',
          interventionState: 'running',
          previousRecommendationDisposition: 'unapplied',
          recommendationDisposition: 'applied',
          evidenceState: 'complete',
          safeOutputId: 'github:issue:githubnext/gh-cao:11861',
          safeOutputUrl: 'https://github.com/githubnext/gh-cao/issues/11861',
          implementationChangeId: 'github:pull-request:octo/example:42',
          implementationPullRequestUrl: 'https://github.com/octo/example/pull/42',
          implementationRunIds: ['1187001'],
          acceptedAt: '2026-09-15T06:00:00Z',
          implementationStartedAt: '2026-09-15T07:00:00Z',
          implementationCompletedAt: '2026-09-16T04:00:00Z',
          sourceProvenance: {
            kind: 'workflow-dispatch-claim',
            sourceId: 'github-actions-run:githubnext/gh-cao:1189001:attempt:1',
            sourceSchemaRevision: 1,
            generation: 'github-actions-run:githubnext/gh-cao:1189001:attempt:1',
            completeness: 'complete',
            freshness: 'fresh',
            evidenceLinks: [
              'https://github.com/githubnext/gh-cao/issues/11861',
              'https://github.com/octo/example/pull/42'
            ]
          }
        }
      }
    ];
    await ingestCachedGhAwJsonl(indexedDB, `${records.map((record) => JSON.stringify(record)).join('\n')}\n`, {
      now: Date.parse('2026-09-15T05:00:00Z')
    });

    const canonical = await queryCanonicalViewSources(
      indexedDB,
      {},
      ['audits']
    );
    const projected = executeDashboardQueries(
      [],
      canonical,
      ['token-efficiency-interventions']
    );

    expect(projected['token-efficiency-interventions'].rows).toEqual([
      expect.objectContaining({
        organization: 'octo',
        repository: 'example',
        'intervention-state': 'proposed',
        'recommendation-disposition': 'unapplied',
        'proposed-savings-aic': 12.5
      }),
      expect.objectContaining({
        organization: 'octo',
        repository: 'example',
        'lifecycle-observation-id': 'token-lifecycle:accepted-1',
        'previous-intervention-state': 'proposed',
        'intervention-state': 'running',
        'recommendation-disposition': 'applied',
        experiment: 'review-context-v1',
        'proposed-savings-aic': 12.5,
        'safe-output-id': 'github:issue:githubnext/gh-cao:11861',
        'implementation-change-id': 'github:pull-request:octo/example:42',
        'implementation-run-ids': ['1187001'],
        'implementation-completed-at': '2026-09-16T04:00:00.000Z',
        'issue-link': 'https://github.com/githubnext/gh-cao/issues/11861',
        'pull-request-link': 'https://github.com/octo/example/pull/42'
      })
    ]);
    expect(projected['token-efficiency-interventions'].rows[0]).not.toHaveProperty('issue-link');
    const latest = executeDashboardQueries([{
      name: 'latest-token-intervention',
      from: 'token-efficiency-interventions',
      select: [
        { field: 'intervention-id' },
        { field: 'intervention-state' },
        { field: 'recommendation-disposition' },
        { field: 'observed-at' }
      ],
      'order-by': [{ field: 'observed-at', direction: 'desc' }],
      limit: 1
    }], canonical, ['latest-token-intervention']);
    expect(latest['latest-token-intervention'].rows).toEqual([{
      'intervention-id': 'token-intervention:review-context-v1:1186001',
      'intervention-state': 'running',
      'recommendation-disposition': 'applied',
      'observed-at': '2026-09-16T04:02:00.000Z'
    }]);
  });

  it('keeps retained events available to event-backed views after a partial collection', async () => {
    await loadCanonicalViewSources(indexedDB, collection('generation-a', sources.audits.rows), { ingest: true });
    const partial = collection('generation-b', []);

    const projected = await loadCanonicalViewSources(indexedDB, partial, { ingest: true });
    const rows = /** @type {{ rows: Record<string, unknown>[] }} */ (projected.audits).rows;

    expect(rows.map((row) => row.event)).toEqual(['event:agent-turn']);
    expect(rows.map((row) => row['event-type'])).toEqual(['user.message']);
  });

  it('queries an empty database before fresh data is ingested', async () => {
    const projected = await loadCanonicalViewSources(indexedDB, sources);
    expect(/** @type {{ rows: unknown[] }} */ (projected.runs).rows).toEqual([]);
  });

  it('keeps lazy continuation snapshots safe across background IndexedDB updates', async () => {
    /** @param {string} generation @param {string[]} runIds */
    const snapshot = (generation, runIds) => {
      const input = structuredClone(sources);
      const collected = { ...metadata, 'artifact-generation': generation };
      for (const source of Object.values(input)) source.metadata = collected;
      input.runs.rows = runIds.map((run) => ({
        ...sources.runs.rows[0],
        run,
        'run-link': { relation: 'run', href: `https://github.com/githubnext/gh-cao/actions/runs/${run}`, label: `Run ${run}` }
      }));
      input['job-performance'].rows = [];
      input.audits.rows = [];
      return input;
    };
    const definitions = [{
      name: 'recent-runs',
      from: 'runs',
      select: [{ field: 'run' }],
      'order-by': [{ field: 'run', direction: 'desc' }]
    }];
    const initial = snapshot('generation-a', ['42', '43', '44']);
    await loadCanonicalViewSources(indexedDB, initial, { ingest: true });
    const initialSources = await queryCanonicalViewSources(indexedDB, initial, ['runs']);
    const initialBudget = createDashboardQueryBudget();
    const firstPage = executeDashboardQueries(definitions, initialSources, ['recent-runs'], {
      budget: initialBudget,
      pagination: { 'recent-runs': { limit: 2 } }
    });

    expect(initialBudget.operations).toBe(0);

    const updated = snapshot('generation-b', ['42', '43', '44', '45']);
    await loadCanonicalViewSources(indexedDB, updated, { ingest: true });

    expect(initialBudget.operations).toBe(0);
    expect(firstPage['recent-runs'].rows).toEqual([{ run: '44' }, { run: '43' }]);
    expect(initialBudget.operations).toBeGreaterThan(0);

    const updatedSources = await queryCanonicalViewSources(indexedDB, updated, ['runs']);
    expect(() => executeDashboardQueries(definitions, updatedSources, ['recent-runs'], {
      pagination: {
        'recent-runs': {
          limit: 2,
          continuationToken: firstPage['recent-runs'].continuationToken
        }
      }
    })['recent-runs']).toThrow('Invalid or stale continuation token');

    const refreshedBudget = createDashboardQueryBudget();
    const refreshed = executeDashboardQueries(definitions, updatedSources, ['recent-runs'], {
      budget: refreshedBudget,
      pagination: { 'recent-runs': { limit: 2 } }
    });
    expect(refreshedBudget.operations).toBe(0);
    expect(refreshed['recent-runs'].rows).toEqual([{ run: '45' }, { run: '44' }]);
    expect(refreshedBudget.operations).toBeGreaterThan(0);
  });
});