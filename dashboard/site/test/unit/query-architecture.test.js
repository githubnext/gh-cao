import { existsSync, globSync, readFileSync } from 'node:fs';
import { resolve, sep } from 'node:path';
import { describe, expect, it } from 'vitest';
import { TABLE_FIELDS } from '../../src/specification.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';

/** @param {string} path */
const read = (path) => readFileSync(resolve(path), 'utf8');

describe('dashboard query architecture', () => {
  it('declares each event family as a native-table query rather than an implicit combined source', () => {
    const definitions = /** @type {Array<{
      name: string, from: string, stores: string[], union?: string[],
      joins?: Array<{ source: string }>
    }>} */ (JSON.parse(read('src/data/queries/database.json')));
    for (const family of ['domains', 'tools', 'skills', 'friction', 'audits', 'issues']) {
      const definition = definitions.find(({ name }) => name === family);
      expect(definition, family).toMatchObject({
        from: `$${family}`, stores: ['runs', family], joins: [{ source: '$runs' }]
      });
      expect(definition?.union).toBeUndefined();
    }
    expect(definitions.filter(({ from }) => from.startsWith('$')).every(({ from, stores }) => (
      stores.length === 0 || stores.includes(from.slice(1))
    ))).toBe(true);
    expect(read('src/data/queries/database.js')).not.toContain('stores-by-source');
    expect(definitions.find(({ name }) => name === 'mcp-calls')?.from).toBe('tools');
    expect(definitions.find(({ name }) => name === 'findings')?.from).toBe('audits');
    expect(definitions.find(({ name }) => name === 'outcomes')?.from).toBe('issues');
    expect(definitions.find(({ name }) => name === 'work-items')?.from).toBe('workflows');
    expect(definitions.find(({ name }) => name === 'detection-observations')?.from).toBe('findings');
    expect(definitions.find(({ name }) => name === 'safe-output-performance')?.from).toBe('outcomes');
    expect(JSON.stringify(definitions)).not.toMatch(/\$(?:security-findings|outcomes)/);
    expect(existsSync(resolve('../report/dashboard-language-sources.mjs'))).toBe(false);
  });

  it('gives agents an explicit database-authoritative query and effect contract', () => {
    const guidance = read('../../AGENTS.md');

    expect(guidance).toContain('Eliminate every JavaScript-based dashboard query.');
    expect(guidance).toContain('executed through the data Web Worker for static deployments');
    expect(guidance).toContain("the Go server's query engine for local SQLite and hosted PostgreSQL deployments");
    expect(guidance).toContain('Author consumer queries in dashboard documents, not JavaScript engine modules.');
    expect(guidance).toContain('Keep the active page or view subscribed to its worker query');
    expect(guidance).toContain('Effects may synchronize query results and local interaction state to owned DOM only.');
  });

  it('executes dashboard queries and subscriptions through the canonical data worker', () => {
    const worker = read('src/data-worker.js');
    const startup = read('src/data/startup.js');
    const presenter = read('src/presenter.js');
    const factoryElements = read('src/components/factory-elements.js');
    const presentationQueryFixture = read('test/workflow-inventory-query.js');
    const databaseAccess = read('src/data/queries/database.js');
    const databaseQueries = /** @type {Array<{
     * name?: string,
     * from?: string,
     * stores?: string[],
     * aggregate?: unknown,
     * joins?: unknown[]
     * }>} */ (JSON.parse(read('src/data/queries/database.json')));
    const dashboard = authoritativeDashboard.dashboard;

    expect(worker).toContain('queryIndexedDatabaseSources(');
    expect(worker).toMatch(/executeDashboardQueries\(\s*executionQueries,\s*\{ \.\.\.databasePayload, \.\.\.nativeSources \},\s*directRequests/);
    const nativeQuery = worker.indexOf('const nativeSources =');
    const databaseQuery = worker.indexOf('const databasePayload =');
    expect(nativeQuery).toBeGreaterThan(-1);
    expect(databaseQuery).toBeGreaterThan(-1);
    expect(worker.indexOf('compileDashboardViewPayloadQueries(page, pageId')).toBeLessThan(nativeQuery);
    expect(nativeQuery).toBeLessThan(databaseQuery);
    expect(worker).toContain('const replacedSources = new Set(viewPayload.replacedSources)');
    expect(worker).not.toContain('data-health.js');
    expect(worker).not.toMatch(/deriveOverviewSources|deriveRepositorySources|deriveRuntimeSources|deriveWorkflowSources/);
    expect(worker).toContain("operation === 'subscribe-canonical-dashboard'");
    expect(worker).not.toContain('createDashboardQueryMemoization');
    expect(existsSync(resolve('src/data/queries/memoization.js'))).toBe(false);
    expect(databaseQueries.every((/** @type {{ stores?: unknown }} */ query) => Array.isArray(query.stores))).toBe(true);
    expect(startup).toContain('subscribeCanonicalDashboardView(');
    expect(startup).toContain('signal: pageOptions.signal');
    expect(startup).toContain('bindContinuations(pageId, sources, paginatedSources, { sourceNames }, pageOptions)');
    expect(startup).toContain('scope.sourceNames,\n        dashboardContext,\n        pagination,');
    expect(startup).toContain('sourceNames: [...new Set(sourceNames)], viewId');
    expect(startup).not.toContain('const sourceName = bindings[alias]?.sourceName');
    expect(startup).toContain('dashboardContext,\n        pagination,');
    expect(startup).toContain('viewId: scope.viewId');
    expect(startup).toContain('routeParameters: pageOptions.routeParameters');
    expect(startup).toContain('queryContext: pageOptions.queryContext');
    expect(presenter).not.toMatch(/filterDashboardSources|filterRowsForView|deriveOverviewSources|deriveRepositorySources|deriveRuntimeSources|deriveWorkflowSources/);
    expect(factoryElements).not.toMatch(/connectedRepositoryCoverage|latestOutcomes|activityDays|exceedsThreshold|workerCount/);
    expect(factoryElements).toMatch(/requestSource|publishSource/);
    expect(factoryElements).not.toMatch(/indexedDB/);
    expect(read('src/components/ui-elements.js')).not.toContain('filterRows');
    expect(presentationQueryFixture).toContain("operation: 'execute-dashboard-queries'");
    expect(presentationQueryFixture).not.toMatch(/executeDashboardQueries|compileDashboardViewPayloadQueries|deriveDashboardLinkSources/);
    expect(databaseAccess).not.toContain('tokenEfficiencySources');
    expect(databaseAccess).not.toContain('projectCanonicalViewSources');
    expect(databaseAccess).not.toContain('failedRunsSource');
    expect(databaseAccess).not.toContain('HEALTH_DATABASE_SOURCES');
    for (const projection of [
      'campaignsSource',
      'repositoriesSource',
      'workflowsSource',
      'runsSource',
      'recordsSource',
      'mcpCallsSource',
      'findingsSource',
      'detectionObservationsSource',
      'safeOutputPerformanceSource'
    ]) {
      expect(databaseAccess).not.toContain(`function ${projection}`);
    }
    expect(databaseQueries.map((/** @type {{ name?: string }} */ query) => query.name)).toEqual(expect.arrayContaining([
      'campaigns',
      'repositories',
      'workflows',
      'runs',
      'domains', 'tools', 'skills', 'friction', 'audits', 'issues',
      'mcp-calls',
      'findings',
      'detection-observations',
      'safe-output-performance'
    ]));
    for (const query of /** @type {Array<{
      name: keyof typeof TABLE_FIELDS,
      from: keyof typeof TABLE_FIELDS,
      select?: Array<{ field: string, as?: string }>,
      joins?: Array<{ fields: Array<{ field: string, as?: string }> }>
    }>} */ (databaseQueries)) {
      const expectedFields = TABLE_FIELDS[query.name];
      if (!expectedFields) continue;
      const selectedFields = new Set(query.select
        ? query.select.map((field) => field.as ?? field.field)
        : [
            ...(TABLE_FIELDS[query.from] ?? []),
            ...(query.joins ?? []).flatMap((join) => join.fields.map((field) => field.as ?? field.field))
          ]);
      expect(expectedFields.filter((field) => !selectedFields.has(field)), query.name).toEqual([]);
    }
    const failedRuns = dashboard.queries.find((/** @type {{ name?: string }} */ query) => query.name === 'failed-runs');
    expect(failedRuns?.from).toBe('failure-run-base');
    expect(dashboard.queries.find((/** @type {{ name?: string }} */ query) => query.name === failedRuns?.from))
      .toMatchObject({
        from: 'runs',
        filter: { predicates: [{ field: 'run-conclusion' }] }
      });
    expect(dashboard.queries.find((/** @type {{ name?: string }} */ query) => query.name === 'entity-events')?.from)
      .toBe('event-base');
    expect(dashboard.queries.find((/** @type {{ name?: string }} */ query) => query.name === 'event-base')?.select)
      .toEqual([
        { field: 'organization' },
        { field: 'repository' },
        { field: 'workflow' },
        { field: 'run' },
        { field: 'run-attempt' },
        { field: 'event' },
        { field: 'event-source' },
        { field: 'event-type' },
        { field: 'event-summary' },
        { field: 'event-status' },
        { field: 'github-entity-type' },
        { field: 'safe-output-type' },
        { field: 'safe-output-url' },
        { field: 'correlation-id' },
        { field: 'implementation-pull-request-url' },
        { field: 'event-timestamp' },
        { field: 'run-link' }
      ]);
    const eventRuns = dashboard.queries.find((/** @type {{ name?: string }} */ query) => query.name === 'event-runs');
    expect(eventRuns?.from).toBe('audit-event-runs');
    expect(eventRuns?.union).toEqual(['domain-event-runs', 'tool-event-runs', 'issue-event-runs']);
    for (const name of [eventRuns?.from, ...eventRuns.union]) {
      expect(dashboard.queries.find((/** @type {{ name?: string }} */ query) => query.name === name)?.aggregate)
        .toMatchObject({ values: [{ field: 'event', as: 'events', reducer: 'count' }] });
    }
    expect(dashboard.queries.find((/** @type {{ name?: string }} */ query) => query.name === 'token-efficiency-opportunities'))
      .toBeUndefined();
    expect(dashboard.queries.find((/** @type {{ name?: string }} */ query) => query.name === 'token-efficiency-interventions'))
      .toBeUndefined();
    for (const legacyModule of [
      'inferred-sources.js',
      'notification-stories.js',
      'overview-data.js',
      'repository-data.js',
      'runtime-data.js',
      'workflow-data.js',
      'components/notifications-inbox.js'
    ]) {
      expect(existsSync(resolve('src', legacyModule))).toBe(false);
    }
  });

  it('runs tidy row pipelines only inside the data worker', async () => {
    const workerSideModules = new Set([
      'src/data-worker.js',
      'src/data/queries/declarative.js',
      'src/data/queries/incremental-aggregate.js',
      'src/data/storage/indexeddb.js'
    ]);
    const importers = globSync('src/**/*.js')
      .filter((file) => {
        const source = read(file);
        return /from\s*['"][^'"]*data-operations\.js['"]|import\(\s*['"][^'"]*data-operations\.js['"]/.test(source)
          && /\btidy\b/.test(source);
      })
      .map((file) => file.split(sep).join('/'));

    expect(new Set(importers)).toEqual(workerSideModules);
    const { processRows } = await import('../../src/data-processor.js');
    await expect(processRows([], [])).rejects.toThrow('Row operations require a data worker.');
  });

  it('keeps canonical database reads out of the UI JavaScript layer', () => {
    const diagnostics = read('src/diagnostics.js');
    const configurationView = read('src/components/configuration-view.js');
    const databaseQueries = /** @type {Array<{ name: string, from?: string, stores?: string[], aggregate?: unknown, joins?: unknown[] }>} */ (
      JSON.parse(read('src/data/queries/database.json'))
    );
    const databaseQueryLayer = read('src/data/queries/database.js');
    const processor = read('src/data-processor.js');
    const worker = read('src/data-worker.js');

    expect(diagnostics).not.toMatch(/data\/storage\/indexeddb|indexedDB|readCollection/);
    expect(configurationView).not.toMatch(/data\/storage\/indexeddb|indexedDB|readCollection/);
    expect(processor).toContain("operation: 'query-canonical-database-diagnostics'");
    expect(worker).toContain("operation === 'query-canonical-database-diagnostics'");
    expect(worker).toContain('return collectCanonicalDatabaseDiagnostics()');
    expect(databaseQueryLayer).not.toMatch(/collectCanonicalDatabaseDiagnostics|relationshipErrors/);
    expect(worker).toContain('relationshipErrors(');
    expect(worker).toContain('readCollections(indexedDB, ENTITY_STORES)');
    expect(databaseQueries.find(({ name }) => name === 'transactions')).toMatchObject({
      from: '$transactions',
      stores: ['transactions']
    });
    expect(databaseQueries.some(({ name }) => name.startsWith('database-diagnostics-'))).toBe(false);
  });
});