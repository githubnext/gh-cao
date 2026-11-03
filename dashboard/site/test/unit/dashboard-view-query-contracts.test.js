import { describe, expect, it, vi } from 'vitest';
import { webcrypto } from 'node:crypto';
import { IDBFactory } from 'fake-indexeddb';
import { configureCliQueue, isCliQueueQuery } from '../../src/data/cli-queue.js';
import commandQueueFixture from '../fixtures/cli-command-queue.json' with { type: 'json' };
import commandsContract from '../fixtures/commands-view-contract.json' with { type: 'json' };
import { processDataRequest } from '../../src/data-worker.js';
import { dashboardQueryDefects, executeDashboardQueries } from '../../src/data/queries/declarative.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { CAMPAIGN_ROUTE_BODY_VALUES } from '../../src/components/route-body-specification.js';
import { SERVER_SOURCE_VALUES, TABLE_FIELDS } from '../../src/specification.js';
import campaignNavigationContract from '../fixtures/campaign-navigation-contract.json' with { type: 'json' };
import evaluationContract from '../fixtures/evaluation-evidence-contract.json' with { type: 'json' };

import { authoritativeDashboard as document } from '../authoritative-dashboard.js';
const dashboard = document.dashboard;
const queries = dashboard.queries;
const queryNames = new Set(queries.map((/** @type {{ name: string }} */ query) => query.name));
const tableNames = new Set(Object.keys(TABLE_FIELDS));
const dataSourceNames = new Set([...tableNames, ...SERVER_SOURCE_VALUES]);
const queriesByName = new Map(queries.map((/** @type {{ name: string }} */ query) => [query.name, query]));
const serverSourceNames = new Set(SERVER_SOURCE_VALUES);

describe('Commands view contract', () => {
  it('resolves durable card rows through the production worker and declares the console route', async () => {
    const page = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === commandsContract.page);
    expect(page.requires).toEqual(commandsContract.requires);
    const view = page.views.find((/** @type {{ id: string }} */ view) => view.id === commandsContract.view);
    expect(view).toMatchObject({
      mark: 'table', layout: 'full', 'lazy-list': true,
      data: { source: commandsContract.query, 'query-context': false },
      encoding: { card: commandsContract.card }
    });
    expect(view.encoding.columns.map((/** @type {{ field: string }} */ column) => column.field)).toEqual(commandsContract.fields);
    const template = dashboard['card-templates'].find((/** @type {{ id: string }} */ template) => template.id === commandsContract.card);
    expect(template).toMatchObject({
      icon: 'terminal', title: { field: 'command-title' }, subtitle: { field: 'command-target' },
      status: { field: 'status' },
      labels: [],
      timing: [{ field: 'command-created-at', type: 'temporal', format: 'human-friendly-timestamp' }]
    });
    const outputPage = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === 'command');
    expect(outputPage.requires).toEqual(commandsContract.requires);
    expect(outputPage.route).toMatchObject({ 'hash-query-parameter': 'command', 'navigation-page': commandsContract.page });
    expect(outputPage.views[0]).toMatchObject({
      mark: 'element', element: commandsContract.element, data: { sources: [] },
      layout: commandsContract['terminal-layout'], 'show-title': commandsContract['terminal-show-title']
    });
    configureCliQueue(true);
    vi.stubGlobal('fetch', vi.fn().mockImplementation(async () => Response.json(commandQueueFixture)));
    try {
      const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
        operation: 'query-canonical-dashboard',
        sourceNames: [commandsContract.query],
        context: { queries, pages: dashboard.pages }
      }));
      const source = result[commandsContract.query];
      expect(source.metadata.availability).toBe('available');
      expect(source.rows.map((row) => row.status)).toEqual(commandsContract.statuses);
      for (const row of source.rows) {
        expect(row['command-dashboard-link']).toMatchObject({ 'dashboard-href': `${commandsContract.route}${row.id}` });
      }
    } finally {
      configureCliQueue(false);
      vi.unstubAllGlobals();
    }
  });
});

/** @type {import('../../src/presenter.js').SourceMetadata} */
const metadata = {
  'source-id': 'contract-fixture',
  'source-kind': 'fixture',
  'as-of': '2026-09-14T00:00:00Z',
  'retrieved-at': '2026-09-14T00:00:00Z',
  completeness: 'complete',
  freshness: 'fresh',
  availability: 'empty'
};

const databaseTables = Object.fromEntries([...tableNames].map((name) => [name, {
  source: name,
  rows: [],
  metadata
}]));

/**
 * @param {string} name
 * @param {Set<string>} [visited]
 * @returns {boolean}
 */
function requiresServerSource(name, visited = /** @type {Set<string>} */ (new Set())) {
  if (serverSourceNames.has(name)) return true;
  if (visited.has(name)) return false;
  visited.add(name);
  const query = queriesByName.get(name);
  if (!query) return false;
  const inputs = [
    query.from,
    ...(Array.isArray(query.union) ? query.union : []),
    ...(Array.isArray(query.joins) ? query.joins.map((/** @type {{ source: string }} */ join) => join.source) : [])
  ];
  return inputs.some((input) => typeof input === 'string' && requiresServerSource(input, visited));
}

/**
 * @param {unknown} page
 * @returns {Array<Record<string, unknown>>}
 */
function viewsOf(page) {
  if (!page || typeof page !== 'object' || Array.isArray(page)) return [];
  const configured = /** @type {Record<string, unknown>} */ (page);
  const body = configured.kind === 'built-in' && configured.definition && typeof configured.definition === 'object'
    ? /** @type {Record<string, unknown>} */ (configured.definition)
    : configured;
  return [
    ...(Array.isArray(body.views) ? body.views.filter(
      (view) => view && typeof view === 'object' && !Array.isArray(view)
    ) : []),
    ...(Array.isArray(body.sections) ? body.sections.flatMap((/** @type {unknown} */ section) => viewsOf(section)) : [])
  ];
}

/**
 * @param {unknown} view
 * @returns {string[]}
 */
function sourceNamesOf(view) {
  if (!view || typeof view !== 'object' || Array.isArray(view)) return [];
  const data = /** @type {Record<string, unknown>} */ (view).data;
  if (!data || typeof data !== 'object' || Array.isArray(data)) return [];
  const configured = /** @type {Record<string, unknown>} */ (data);
  if (Array.isArray(configured.sources)) return configured.sources.filter((/** @type {unknown} */ name) => typeof name === 'string');
  return [
    ...(typeof configured.source === 'string' ? [configured.source] : []),
    ...(typeof configured['partial-source'] === 'string' ? [configured['partial-source']] : [])
  ];
}

/**
 * @param {unknown} value
 * @returns {string[]}
 */
function declaredQueryReferences(value) {
  if (!value || typeof value !== 'object') return [];
  if (Array.isArray(value)) return value.flatMap(declaredQueryReferences);
  const configured = /** @type {Record<string, unknown>} */ (value);
  return Object.entries(configured).flatMap(([key, nested]) => {
    if (['from', 'source', 'partial-source', 'query', 'freshness-source', 'health-source'].includes(key) && typeof nested === 'string' && queryNames.has(nested)) {
      return [nested];
    }
    if ((key === 'sources' || key === 'union') && Array.isArray(nested)) {
      return nested.filter((name) => typeof name === 'string' && queryNames.has(name));
    }
    if (key === 'any' && typeof configured.label === 'string' && Array.isArray(nested)) {
      return nested.filter((name) => typeof name === 'string' && queryNames.has(name));
    }
    return declaredQueryReferences(nested);
  });
}

describe('dashboard view query contracts', () => {
  it('keeps Overview focused on the header and stations without a campaign list', () => {
    const overview = dashboard.pages.find((/** @type {{ id?: string }} */ page) => page.id === 'overview');
    expect(viewsOf(overview).map((view) => view.id)).toEqual(['overview-header', 'overview-floor']);
    expect(viewsOf(overview).flatMap(sourceNamesOf)).not.toContain('overview-campaign-links');
    expect(dashboard.pages.some((/** @type {{ id?: string }} */ page) => page.id === 'campaign-insights')).toBe(true);
  });

  it('declares experimental evidence pages without presenting observation counts as campaign completion', () => {
    const evolutionSection = dashboard.navigation.find(
      (/** @type {{ label?: string }} */ section) => section.label === 'Evolution'
    );
    expect(evolutionSection?.pages).toEqual(['experiments', 'evals', 'graders']);
    for (const id of ['experiments', 'evals', 'graders']) {
      expect(dashboard.pages.find((/** @type {{ id?: string }} */ page) => page.id === id))
        .toMatchObject({ kind: 'custom', experimental: true });
    }
    const experiments = dashboard.pages.find((/** @type {{ id?: string }} */ page) => page.id === 'experiments');
    expect(experiments.acceptance).toContain('counts are not completion percentages');
    expect(viewsOf(experiments)).toMatchObject([
      { mark: 'chart', chart: 'horizontal-bar', data: { source: 'experiment-assignment-coverage' } },
      { mark: 'table', data: { source: 'experiment-assignments' }, 'lazy-list': true, layout: 'full-view' },
      { mark: 'table', data: { source: 'experiments' }, 'lazy-list': true, layout: 'full-view' }
    ]);
    const assignments = viewsOf(experiments).find((view) => view.id === evaluationContract.experiments.view);
    const assignmentEncoding = /** @type {{ columns: { field: string }[] }} */ (assignments?.encoding);
    expect(assignmentEncoding.columns.map((column) => column.field))
      .toEqual(evaluationContract.experiments.fields);
    expect(viewsOf(dashboard.pages.find((/** @type {{ id?: string }} */ page) => page.id === 'evals')).find((view) => view.id === evaluationContract.evals.view))
      .toMatchObject({
        data: { source: evaluationContract.evals.query },
        encoding: { columns: expect.arrayContaining([{ field: 'run', type: 'nominal', title: 'Run', display: evaluationContract.evals.runDisplay }]) }
      });
    for (const id of /** @type {const} */ (['evals', 'graders'])) {
      const page = dashboard.pages.find((/** @type {{ id?: string }} */ candidate) => candidate.id === id);
      const contract = evaluationContract[id];
      const ledger = viewsOf(page).find((view) => view.id === contract.view);
      expect(ledger).toMatchObject({
        mark: 'table', 'lazy-list': true, layout: 'full-view',
        data: { 'order-by': [{ field: 'workflow', direction: 'asc' }, { field: 'observed-at', direction: 'desc' }] }
      });
      expect(page.description).not.toContain('TODO:');
      expect(page.acceptance).toContain('not current verdicts');
      expect(ledger).toMatchObject({ data: { source: contract.query } });
      const encoding = /** @type {{ columns: { field: string }[] }} */ (ledger?.encoding);
      expect(encoding.columns.map((column) => column.field)).toEqual(contract.fields);
    }
    const scatter = evaluationContract.graders.scatter;
    expect(viewsOf(dashboard.pages.find((/** @type {{ id?: string }} */ page) => page.id === 'graders'))[0])
      .toMatchObject({
        id: scatter.view, mark: 'chart', chart: 'scatter', scatter: { cluster: false },
        data: { source: scatter.query },
        encoding: {
          x: { field: scatter.x, type: 'temporal' }, y: { field: scatter.y, type: 'quantitative' },
          color: { field: scatter.color }, href: { field: scatter.href }
        }
      });
  });

  it('declares three percentage bars per eval above the evidence ledger', () => {
    const distribution = evaluationContract.evals.distribution;
    const page = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === 'evals');
    expect(viewsOf(page)[0]).toMatchObject({
      id: distribution.view, mark: 'chart', chart: distribution.chart,
      data: { source: distribution.query, limit: 99 },
      encoding: {
        x: { field: distribution.x },
        y: { field: distribution.y, unit: 'percent' },
        section: { field: distribution.section },
        color: { field: distribution.x }
      }
    });
  });

  it('keeps all eval answers and run links distinct from auxiliary grader verdicts', () => {
    const results = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries',
      queries,
      sourceNames: ['experiment-assignment-coverage', 'grader-status-ledger', evaluationContract.evals.query],
      sources: {
        ...databaseTables,
        'experiment-assignments': {
          source: 'experiment-assignments', metadata,
          rows: [
            { organization: 'githubnext', repository: 'one', workflow: 'worker.md', experiment: 'prompt', run: '1' },
            { organization: 'githubnext', repository: 'one', workflow: 'worker.md', experiment: 'prompt', run: '2' },
            { organization: 'githubnext', repository: 'two', workflow: 'worker.md', experiment: 'prompt', run: '3' }
          ]
        },
        'eval-observations': {
          source: 'eval-observations', metadata,
          rows: [
            { workflow: 'worker.md', eval: 'answer', 'eval-result': 'YES', run: '1',
              'evidence-link': 'https://github.com/octo/api/actions/runs/1' },
            { workflow: 'worker.md', eval: 'answer', 'eval-result': 'UNKNOWN', run: '2',
              'evidence-link': { href: 'https://github.com/octo/api/actions/runs/2', label: 'Run 2' } },
            { workflow: 'worker.md', eval: 'answer', 'eval-result': 'NO' }
          ]
        },
        'grader-observations': {
          source: 'grader-observations', metadata,
          rows: [
            { workflow: 'worker.md', grader: 'quality', status: 'pass' },
            { workflow: 'worker.md', grader: 'quality', status: 'failed' },
            { workflow: 'worker.md', grader: 'quality', status: 'unavailable' }
          ]
        }
      }
    }));
    expect(results['experiment-assignment-coverage'].rows).toEqual(expect.arrayContaining([
      expect.objectContaining({ 'workflow-coordinate': 'githubnext/one:worker.md', experiment: 'prompt', assignments: 2 }),
      expect.objectContaining({ 'workflow-coordinate': 'githubnext/two:worker.md', experiment: 'prompt', assignments: 1 })
    ]));
    expect(results['grader-status-ledger'].rows.map((row) => row.verdict)).toEqual(['YES', 'NO', 'unknown']);
    expect(results[evaluationContract.evals.query].rows.map((row) => row['eval-result'])).toEqual(['YES', 'UNKNOWN', 'NO']);
    expect(results[evaluationContract.evals.query].rows.map((row) => row['run-link'])).toEqual([
      { href: 'https://github.com/octo/api/actions/runs/1', label: '1' },
      { href: 'https://github.com/octo/api/actions/runs/2', label: '2' },
      null
    ]);
  });

  it('groups navigable experimental pages in one experimental section', () => {
    expect(dashboard.navigation.filter(
      (/** @type {{ experimental?: boolean }} */ section) => section.experimental === true
    )).toEqual([{
      label: 'Experimental',
      experimental: true,
      pages: [
        'wizard',
        'model-routing',
        'skills',
        'steering',
        'simulators'
      ]
    }]);
  });

  it('places the Cost page in the Data section', () => {
    const page = dashboard.pages.find((/** @type {Record<string, unknown>} */ candidate) => candidate.id === 'cost');
    const dataSection = dashboard.navigation.find(
      (/** @type {Record<string, unknown>} */ section) => section.label === 'Data'
    );

    expect(/** @type {Record<string, unknown> | undefined} */ (dataSection)?.pages).toContain('cost');
    expect(page).toMatchObject({
      icon: 'credit-card'
    });
  });

  it('does not retain the removed campaign cost queries', () => {
    expect(queriesByName.has('cost-by-campaign')).toBe(false);
    expect(queriesByName.has('campaign-cost-totals')).toBe(false);
  });

  it('places Campaigns immediately after Workflows in the Data navigation', () => {
    const dataSection = dashboard.navigation.find(
      (/** @type {{ label?: string }} */ section) => section.label === 'Data'
    );
    expect(dataSection.pages.slice(0, 3)).toEqual(['repositories', 'workflows', 'campaigns']);
  });

  it('renders the Cost page without a campaign chart and with a workflow bar chart', () => {
    const page = dashboard.pages.find((/** @type {Record<string, unknown>} */ candidate) => candidate.id === 'cost');
    expect(viewsOf(page).map((view) => view.id)).not.toContain('cost-by-campaign');
    expect(viewsOf(page)).toMatchObject([
      {
        id: 'cost-by-repository',
        title: 'Cost per repository',
        data: { source: 'cost-by-repository' },
        mark: 'chart',
        chart: 'pie',
        encoding: {
          x: { field: 'repository-coordinate' },
          y: { field: 'aic', unit: 'aic' }
        }
      },
      {
        id: 'cost-by-workflow',
        title: 'Cost per workflow',
        data: { source: 'cost-by-workflow' },
        mark: 'chart',
        chart: 'horizontal-bar',
        encoding: {
          x: { field: 'workflow' },
          section: { field: 'repository-coordinate' },
          y: { field: 'aic', unit: 'aic' }
        }
      },
      {
        id: 'cost-per-workflow-run',
        title: 'Cost per workflow run',
        disclosure: 'essential',
        data: { source: 'cost-per-workflow-run' },
        mark: 'chart',
        chart: 'horizontal-bar',
        encoding: {
          x: { field: 'workflow' },
          section: { field: 'repository-coordinate' },
          y: { field: 'aic-per-run', unit: 'aic-per-run' }
        }
      },
      {
        id: 'engines-models-aic-insights',
        title: 'AIC per observed run',
        data: { source: 'engines-models-usage' },
        mark: 'table',
        layout: 'full-view',
        'lazy-list': true,
        encoding: {
          columns: [
            { field: 'summary' },
            { field: 'runs' },
            { field: 'minimum-aic-per-run' },
            { field: 'average-aic-per-run' },
            { field: 'maximum-aic-per-run' }
          ]
        }
      }
    ]);
  });

  it('renders the failed-runs ledger as a bounded lazy table', () => {
    const page = dashboard.pages.find((/** @type {Record<string, unknown>} */ candidate) =>
      candidate.id === 'overview-failed-runs'
    );

    expect(viewsOf(page)).toMatchObject([{
      id: 'overview-failed-runs-ledger',
      data: {
        source: 'failed-runs',
        'order-by': [{ field: 'started-at', direction: 'desc' }]
      },
      mark: 'table',
      controls: 'interactive',
      'lazy-list': true,
      layout: 'full-view'
    }]);
  });

  it('keeps Overview runtime health independent from audit-heavy campaign run presentation', () => {
    const runtimeHealth = dashboard.queries.find(
      (/** @type {Record<string, unknown>} */ query) => query.name === 'campaign-runtime-health-groups'
    );
    const runtimeHealthInput = dashboard.queries.find(
      (/** @type {Record<string, unknown>} */ query) => query.name === 'campaign-runtime-health-runs'
    );

    expect(runtimeHealth).toMatchObject({ from: 'campaign-runtime-health-runs' });
    expect(runtimeHealthInput).toMatchObject({ from: 'overview-runs' });
    expect(declaredQueryReferences(runtimeHealthInput)).not.toContain('run-incomplete-outcomes');
    expect(declaredQueryReferences(runtimeHealthInput)).not.toContain('audits');
  });

  it('keeps campaign run navigation first and failure views scoped to dispatches', () => {
    const page = dashboard.pages.find((/** @type {Record<string, unknown>} */ candidate) => candidate.id === 'campaign-runs');
    const views = viewsOf(page);

    expect(views[0]?.id).toBe('campaign-run-navigation');
    const reasons = /** @type {Record<string, any>} */ (views.find((candidate) => candidate.id === 'campaign-failure-reason-distribution'));
    expect(reasons.data).toEqual({ source: 'campaign-dispatch-failure-reasons', 'route-field': 'campaign' });
    expect(reasons.encoding.y).toEqual({ field: 'failed-dispatches', type: 'quantitative', title: 'Failed dispatches' });
    const table = views.find((candidate) => candidate.id === 'campaign-failed-dispatch-table');
    expect(/** @type {Record<string, unknown> | undefined} */ (table?.data)?.source).toBe('dispatches');
  });

  it('uses one shared route shell first across every primary campaign tab', () => {
    const primaryPages = {
      'campaign-insights': 'insights',
      'campaign-problems': 'problems',
      'campaign-runs': 'runs',
      'campaign-issues': 'issues',
      'campaign-memory': 'memory',
      'campaign-detail': 'overview'
    };

    for (const [pageId, body] of Object.entries(primaryPages)) {
      const page = dashboard.pages.find((/** @type {Record<string, unknown>} */ candidate) => candidate.id === pageId);
      const firstView = viewsOf(page)[0];
      const sources = [
        'workflows',
        'campaign-insight-tab-counts',
        'campaign-problem-tab-counts',
        'campaign-issue-tab-counts'
      ];
      expect(firstView).toMatchObject({
        data: {
          sources,
          arguments: [{ name: 'campaign', field: 'campaign' }]
        },
        mark: 'element',
        element: 'campaign-route',
        config: { body }
      });
    }

    expect(dashboard.pages.some((/** @type {Record<string, unknown>} */ page) => page.id === 'campaign-dispatches')).toBe(false);
  });

  it.each(campaignNavigationContract.pages)('binds $id tab counts to the selected campaign through the worker', ({ id, view: viewId }) => {
    const page = dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === id);
    const view = viewsOf(page).find((candidate) => candidate.id === viewId);
    expect(view).toMatchObject({
      data: {
        sources: campaignNavigationContract.sources,
        arguments: [{ name: 'campaign', field: 'campaign' }]
      }
    });
    const options = { queries, viewId, sourceNames: campaignNavigationContract.sources };
    const selected = compileDashboardViewPayloadQueries(page, id, {
      ...options, routeParameters: { campaign: 'ambient-context' }
    });
    expect(selected.aliases).toHaveLength(campaignNavigationContract.sources.length);
    const sources = {
      ...databaseTables,
      workflows: {
        source: 'workflows',
        rows: [
          { campaign: 'other', 'workflow-role': 'orchestrator' },
          { campaign: 'ambient-context', 'workflow-role': 'orchestrator' }
        ],
        metadata: { ...metadata, availability: 'available' }
      }
    };
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries',
      queries: [...queries, ...selected.queries],
      sourceNames: selected.aliases,
      sources
    }));
    expect(Object.keys(result)).toEqual(selected.aliases);
    expect(result[selected.aliases[0]].rows.map((row) => row.campaign)).toEqual(['ambient-context']);
    const absent = compileDashboardViewPayloadQueries(page, id, options);
    const missing = /** @type {typeof result} */ (processDataRequest({
      operation: 'execute-dashboard-queries',
      queries: [...queries, ...absent.queries],
      sourceNames: absent.aliases,
      sources
    }));
    for (const alias of absent.aliases) expect(missing[alias].rows).toEqual([]);
  });

  it('keeps stable campaign route IDs with an Insights label across every entry path', () => {
    const campaignPageIds = ['campaign-insights', 'campaign-problems', 'campaign-issues', 'campaign-memory'];
    const campaignPages = dashboard.pages.filter((/** @type {Record<string, unknown>} */ page) => (
      campaignPageIds.includes(/** @type {string} */ (page.id))
    ));
    const expectedTabs = [
      { id: 'insights', label: 'Insights', icon: 'graph', page: 'campaign-insights' },
      { id: 'problems', label: 'Failures', icon: 'alert', page: 'campaign-problems' },
      { id: 'issues', label: 'Reports', icon: 'issue-opened', page: 'campaign-issues' },
      { id: 'memory', label: 'Memory', icon: 'archive', page: 'campaign-memory' }
    ];

    expect(campaignPages.map((/** @type {Record<string, unknown>} */ page) => page.id))
      .toEqual(expect.arrayContaining(campaignPageIds));
    for (const page of campaignPages) {
      expect(page.route).toMatchObject({
        'title-format': 'title-case',
        'tabs-class-name': 'campaign-tabs',
        tabs: expectedTabs
      });
    }
    expect(JSON.stringify(dashboard.queries)).not.toContain('#page-campaign-detail?campaign=');
    expect(JSON.stringify(dashboard.queries)).toContain('#page-campaign-insights?campaign=');
    expect(dashboard.pages.some((/** @type {Record<string, unknown>} */ page) => page.id === 'campaign-pull-requests')).toBe(false);
    expect(CAMPAIGN_ROUTE_BODY_VALUES).not.toContain('pull-requests');
  });

  it('renders campaign issues with the reusable issue card template', () => {
    const page = dashboard.pages.find((/** @type {Record<string, unknown>} */ candidate) => candidate.id === 'campaign-issues');
    const issueView = viewsOf(page).find((view) => view.id === 'campaign-issue-table');

    expect(issueView).toMatchObject({
      data: { source: 'campaign-worker-issues', 'route-field': 'campaign' },
      mark: 'list',
      list: {
        style: 'entity-cards',
        card: 'issue',
        drill: { type: 'external', field: 'issue-link' }
      }
    });
  });

  it('keeps campaign content data-driven through reusable views and templates', () => {
    const insights = dashboard.pages.find((/** @type {Record<string, unknown>} */ candidate) => candidate.id === 'campaign-insights');
    const problems = dashboard.pages.find((/** @type {Record<string, unknown>} */ candidate) => candidate.id === 'campaign-problems');
    const runs = dashboard.pages.find((/** @type {Record<string, unknown>} */ candidate) => candidate.id === 'campaign-runs');

    expect(viewsOf(insights)[0]).toMatchObject({
      data: {
        sources: [
          'workflows',
          'campaign-insight-tab-counts',
          'campaign-problem-tab-counts',
          'campaign-issue-tab-counts'
        ],
        arguments: [{ name: 'campaign', field: 'campaign' }]
      },
      mark: 'element',
      element: 'campaign-route',
      config: { body: 'insights' }
    });

    const outcomes = dashboard.pages.find((/** @type {Record<string, unknown>} */ candidate) => candidate.id === 'operational-value-campaign');
    expect(outcomes).toBeUndefined();
    expect(viewsOf(insights).map((view) => view.id)).not.toEqual(expect.arrayContaining([
      'campaign-outcome-directions', 'campaign-outcome-repositories', 'campaign-operational-value-history'
    ]));

    expect(viewsOf(insights)[1]).toMatchObject({
      id: 'campaign-baseline-outcomes',
      title: 'Workflow run results',
      disclosure: 'supplemental',
      data: { source: 'campaign-baseline-outcome-distribution', 'route-field': 'campaign' },
      mark: 'chart',
      chart: 'pie',
      encoding: {
        x: { field: 'outcome', type: 'nominal' },
        y: { field: 'runs', type: 'quantitative' }
      }
    });

    expect(viewsOf(insights)[2]).toMatchObject({
      id: 'campaign-performance-baseline',
      title: 'Execution reliability',
      data: {
        source: 'campaign-performance-baseline',
        'route-field': 'campaign'
      },
      mark: 'table',
      controls: 'static',
      encoding: {
        columns: [
          { field: 'concluded-runs', title: 'Concluded attempts' },
          { field: 'successful-runs', title: 'Successful attempts' },
          { field: 'success-rate-display', title: 'Success rate' },
          { field: 'reliability-signal', title: 'Reliability signal' },
          { field: 'produced-outputs', title: 'Reported output items' },
          { field: 'production-signal', title: 'Production signal' },
          { field: 'aic-per-successful-run', title: 'Average AIC / costed successful attempt', unit: 'aic-per-run' }
        ]
      }
    });
    expect(viewsOf(insights)[3]).toMatchObject({
      id: 'campaign-audit-event-table',
      disclosure: 'supplemental'
    });

    expect(viewsOf(problems).find((view) => view.id === 'campaign-current-runtime-problems')).toMatchObject({
      data: { source: 'campaign-problem-items' },
      mark: 'list',
      list: { style: 'entity-cards', card: 'problem' }
    });
    const runStatus = /** @type {Record<string, any>} */ (viewsOf(runs).find((view) => view.id === 'campaign-run-status'));
    expect(runStatus).toMatchObject({
      data: { source: 'campaign-run-status-counts', 'route-field': 'campaign' },
      mark: 'chart',
      encoding: { y: { field: 'runs', type: 'quantitative' } }
    });
    expect(runStatus.encoding.y).not.toHaveProperty('aggregate');
  });

  it('builds a route-scoped selected-horizon campaign baseline with deterministic signals', () => {
    const page = dashboard.pages.find(
      (/** @type {Record<string, unknown>} */ candidate) => candidate.id === 'campaign-insights'
    );
    const payload = compileDashboardViewPayloadQueries(page, 'campaign-insights', {
      viewId: 'campaign-performance-baseline',
      routeParameters: { campaign: 'optimization' },
      queryContext: {
        timeWindow: {
          start: '2026-08-31T00:00:00Z',
          end: '2026-09-30T00:00:00Z'
        }
      },
      queries
    });
    const result = executeDashboardQueries(payload.queries, {
      runs: {
        source: 'runs',
        metadata,
        rows: [
          {
            organization: 'githubnext',
            repository: 'gh-cao',
            workflow: 'optimization-worker.md',
            run: '100',
            'run-attempt': 1,
            'run-status': 'completed',
            'run-conclusion': 'success',
            'started-at': '2026-09-10T00:00:00Z',
            'safe-items-count': 2,
            'aic-total': 4
          },
          {
            organization: 'githubnext',
            repository: 'gh-cao',
            workflow: 'optimization-worker.md',
            run: '101',
            'run-attempt': 1,
            'run-status': 'completed',
            'run-conclusion': 'success',
            'started-at': '2026-09-20T00:00:00Z',
            'safe-items-count': 0,
            'aic-total': 6
          },
          {
            organization: 'githubnext',
            repository: 'gh-cao',
            workflow: 'optimization-worker.md',
            run: '102',
            'run-attempt': 1,
            'run-status': 'completed',
            'run-conclusion': 'failure',
            'started-at': '2026-09-25T00:00:00Z',
            'aic-total': 9
          },
          {
            organization: 'githubnext',
            repository: 'gh-cao',
            workflow: 'optimization-worker.md',
            run: 'old',
            'run-attempt': 1,
            'run-status': 'completed',
            'run-conclusion': 'failure',
            'started-at': '2026-08-20T00:00:00Z',
            'aic-total': 20
          },
          {
            organization: 'githubnext',
            repository: 'gh-cao',
            workflow: 'other-worker.md',
            run: 'other',
            'run-attempt': 1,
            'run-status': 'completed',
            'run-conclusion': 'failure',
            'started-at': '2026-09-25T00:00:00Z',
            'aic-total': 8
          }
        ]
      },
      workflows: {
        source: 'workflows',
        metadata,
        rows: [
          {
            organization: 'githubnext',
            repository: 'gh-cao',
            workflow: 'optimization-worker.md',
            campaign: 'optimization',
            'campaign-name': 'Optimization',
            'workflow-name': 'Optimization worker',
            'workflow-role': 'worker'
          },
          {
            organization: 'githubnext',
            repository: 'gh-cao',
            workflow: 'other-worker.md',
            campaign: 'other',
            'campaign-name': 'Other',
            'workflow-name': 'Other worker',
            'workflow-role': 'worker'
          }
        ]
      },
      audits: {
        source: 'audits',
        metadata,
        rows: []
      }
    }, payload.aliases);
    const baselineAlias = payload.aliases.find((alias) => alias.includes('campaign-performance-baseline'));

    expect(baselineAlias).toBeDefined();
    expect(result[baselineAlias ?? ''].rows).toEqual([{
      campaign: 'optimization',
      'concluded-runs': 3,
      'successful-runs': 2,
      'success-rate': 2 / 3,
      'success-rate-display': '66.7%',
      'success-rate-percent': (2 / 3) * 100,
      'reliability-signal': 'Unsuccessful runs observed',
      'produced-outputs': 2,
      'production-signal': 'Outputs produced; acceptance unverified',
      'aic-per-successful-run': 5
    }]);
  });

  it('projects the baseline into honest successful and other concluded run slices', () => {
    const outcomeQueries = dashboard.queries.filter(
      (/** @type {{ name?: string }} */ candidate) => [
        'campaign-baseline-successful-runs',
        'campaign-baseline-other-runs',
        'campaign-baseline-outcome-distribution'
      ].includes(candidate.name ?? '')
    );
    const results = executeDashboardQueries(outcomeQueries, {
      'campaign-performance-baseline-totals': {
        source: 'campaign-performance-baseline-totals',
        metadata,
        rows: [
          { campaign: 'optimization', 'concluded-runs': 3, 'successful-runs': 2 },
          { campaign: 'all-success', 'concluded-runs': 1, 'successful-runs': 1 }
        ]
      }
    });
    expect(results['campaign-baseline-outcome-distribution'].rows).toEqual([
      { campaign: 'optimization', outcome: 'Successful attempts', runs: 2 },
      { campaign: 'all-success', outcome: 'Successful attempts', runs: 1 },
      { campaign: 'optimization', outcome: 'Other concluded attempts', runs: 1 },
      { campaign: 'all-success', outcome: 'Other concluded attempts', runs: 0 }
    ]);
  });

  it('renders one campaign problem as a dedicated full detail view', () => {
    const detailPage = dashboard.pages.find(
      (/** @type {Record<string, unknown>} */ candidate) => candidate.id === 'campaign-problem-detail'
    );
    const detailView = dashboard.views.find(
      (/** @type {Record<string, unknown>} */ candidate) => candidate.id === 'campaign-problem-detail-view'
    );

    expect(detailPage).toMatchObject({
      route: { 'hash-query-parameter': 'target-repository' },
      views: ['campaign-problem-detail-view']
    });
    expect(detailView).toMatchObject({
      data: { sources: ['campaign-problem-items'] },
      mark: 'element',
      element: 'problem-detail',
      layout: 'full'
    });
  });

  it('renders runs and issues per repository with one activity inventory', () => {
    const page = dashboard.pages.find((/** @type {Record<string, unknown>} */ candidate) => candidate.id === 'repositories');
    const views = viewsOf(page);

    expect(views[0]).toMatchObject({
      id: 'repositories-runs',
      data: { source: 'repository-run-inventory', limit: 100 },
      mark: 'chart',
      chart: 'horizontal-bar'
    });
    expect(views[1]).toMatchObject(
      {
        id: 'repositories-issues',
        data: { source: 'issue-repository-totals' },
        mark: 'chart',
        chart: 'pie'
      }
    );
    expect(queries.find((/** @type {{ name: string }} */ query) => query.name === 'issue-repository-totals'))
      .toMatchObject({
        from: 'issue-safe-outputs',
        limit: 10,
        aggregate: {
          by: ['repository-coordinate'],
          values: [{ field: 'entity-url', as: 'issues', reducer: 'count' }]
        }
      });
    expect(views.map((view) => view.id)).toEqual([
      'repositories-runs',
      'repositories-issues',
      'repositories-activity'
    ]);
    expect(views[2]).toMatchObject({
      data: { source: 'repository-activity' },
      mark: 'table'
    });
  });

  it('renders issues as a top-repository chart with a full-view table and cards', () => {
    const page = dashboard.pages.find((/** @type {Record<string, unknown>} */ candidate) => candidate.id === 'issues');
    const views = viewsOf(page);

    expect(page).toMatchObject({
      kind: 'built-in',
      page: 'issues'
    });
    expect(views).toMatchObject([
      {
        id: 'issues-by-repository',
        data: { source: 'issue-repository-totals' },
        mark: 'chart',
        chart: 'pie'
      },
      {
        id: 'issues-by-status',
        data: { source: 'issue-safe-outputs' },
        mark: 'chart',
        chart: 'pie',
        encoding: {
          x: { field: 'issue-status-detail' },
          y: { field: 'entity-url', aggregate: 'count' }
        }
      },
      {
        id: 'issues-source',
        data: {
          source: 'issue-safe-outputs',
          limit: 100
        },
        mark: 'table',
        controls: 'interactive',
        'lazy-list': true,
        layout: 'full-view'
      }
    ]);
    expect(queries.find((/** @type {{ name: string }} */ query) => query.name === 'issue-repository-totals'))
      .toMatchObject({
        from: 'issue-safe-outputs',
        limit: 10,
        aggregate: {
          by: ['repository-coordinate'],
          values: [{ field: 'entity-url', as: 'issues', reducer: 'count' }]
        }
      });
    expect(dashboard.navigation.flatMap(
      (/** @type {{ pages?: string[] }} */ section) => section.pages ?? []
    )).toContain('issues');
  });

  it('keeps issue repository totals available when audit records are unavailable', () => {
    const availableMetadata = { ...metadata, availability: 'available' };
    const unavailableMetadata = { ...metadata, availability: 'unavailable', completeness: 'partial' };
    const issueRows = [
      {
        organization: 'githubnext',
        repository: 'gh-cao',
        workflow: '.github/workflows/worker.md',
        run: '101',
        'run-attempt': 1,
        'event-type': 'safe_output.result',
        'event-timestamp': '2026-09-20T12:00:00Z',
        'github-entity-type': 'issue',
        'is-pull-request': false,
        'issue-state': 'CLOSED',
        'issue-closed': true,
        'issue-state-reason': 'COMPLETED',
        'issue-closed-at': '2026-09-20T12:30:00Z',
        'issue-status-observed-at': '2026-09-20T12:31:00Z',
        'safe-output-type': 'create_issue',
        'event-summary': 'Fix issue view',
        'correlation-id': 'https://github.com/githubnext/gh-cao/issues/13439'
      },
      {
        organization: 'githubnext',
        repository: 'gh-cao',
        workflow: '.github/workflows/worker.md',
        run: '102',
        'run-attempt': 1,
        'event-type': 'safe_output.result',
        'event-timestamp': '2026-09-20T13:00:00Z',
        'github-entity-type': 'pull_request',
        'is-pull-request': true,
        'safe-output-type': 'create_pull_request',
        'event-summary': 'Ignore pull request',
        'correlation-id': 'https://github.com/githubnext/gh-cao/pull/13440'
      }
    ];
    const results = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries',
      queries,
      sourceNames: ['issue-safe-outputs', 'issue-repository-totals'],
      sources: {
        audits: { source: 'audits', rows: [], metadata: unavailableMetadata },
        issues: { source: 'issues', rows: issueRows, metadata: availableMetadata },
        runs: {
          source: 'runs',
          rows: [{
            organization: 'githubnext',
            repository: 'gh-cao',
            workflow: '.github/workflows/worker.md',
            run: '101',
            'run-attempt': 1,
            'run-link': {
              relation: 'run',
              href: 'https://github.com/githubnext/gh-cao/actions/runs/101',
              label: 'Run 101'
            }
          }],
          metadata: availableMetadata
        }
      }
    }));

    expect(results['issue-safe-outputs'].metadata.availability).toBe('available');
    expect(results['issue-safe-outputs'].rows).toEqual([
      expect.objectContaining({
        'event-summary': 'Fix issue view',
        'entity-url': 'https://github.com/githubnext/gh-cao/issues/13439',
        'issue-status': 'Closed',
        'issue-closing-status': 'Completed',
        'issue-status-detail': 'Closed: Completed',
        repository: 'gh-cao'
      })
    ]);
    expect(results['issue-repository-totals'].metadata.availability).toBe('available');
    expect(results['issue-repository-totals'].rows).toEqual([
      { 'repository-coordinate': 'githubnext/gh-cao', issues: 1 }
    ]);
  });

  it('resolves every authored view source through canonical data or Dashboard Language', () => {
    const unresolved = dashboard.pages.flatMap((/** @type {Record<string, unknown>} */ page) => viewsOf(page).flatMap((view) => (
      sourceNamesOf(view)
        .filter((name) => !dataSourceNames.has(name) && !queryNames.has(name))
        .map((name) => `${page.id}/${view.id}: ${name}`)
    )));

    expect(unresolved).toEqual([]);
  });

  it('registers collection health as a server-owned query source', () => {
    expect(SERVER_SOURCE_VALUES).toContain('collection-health');
    expect(queries.find((/** @type {{ name: string }} */ query) => query.name === 'ingestion-health')?.from)
      .toBe('collection-health');
    const sourceNames = ['ingestion-queue-sizes', 'dashboard-ingestion-health'];
    const available = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries',
      queries,
      sourceNames,
      sources: {
        'collection-health': {
          source: 'collection-health',
          metadata: { ...metadata, availability: 'available' },
          rows: [{ configured: true, health: 'degraded', 'queue-depth': 8, 'pending-tasks': 2, 'backfill-queued-run-tasks': 13 }]
        }
      }
    }));
    expect(available['ingestion-queue-sizes'].rows).toEqual([
      { queue: 'Ready and scheduled', tasks: 8 },
      { queue: 'In flight', tasks: 2 },
      { queue: 'Backfill admitted (total)', tasks: 13 }
    ]);
    expect(available['dashboard-ingestion-health'].rows).toEqual([{ configured: true, health: 'degraded' }]);
    const unconfigured = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries', queries, sourceNames,
      sources: {
        'collection-health': {
          source: 'collection-health', metadata: { ...metadata, availability: 'available' },
          rows: [{ configured: false, 'queue-depth': 0, 'pending-tasks': 0, 'backfill-queued-run-tasks': 0 }]
        }
      }
    }));
    expect(unconfigured['ingestion-queue-sizes'].rows).toEqual([]);
    const denied = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries', queries, sourceNames, sources: {}
    }));
    expect(denied['ingestion-queue-sizes'].metadata.availability).toBe('unavailable');
    expect(denied['dashboard-ingestion-health'].metadata.availability).toBe('unavailable');
  });

  it('registers GitHub quota usage as a server-owned query source', () => {
    expect(SERVER_SOURCE_VALUES).toContain('github-quota-usage');
    expect(queries.find((/** @type {{ name: string }} */ query) => query.name === 'github-api-usage')?.from)
      .toBe('github-quota-usage');
  });

  it('does not retain queries unused by dashboard content or another retained query', () => {
    const retained = new Set(declaredQueryReferences({
      ...dashboard,
      queries: undefined
    }));
    const queryByName = new Map(queries.map((/** @type {{ name: string }} */ query) => [query.name, query]));
    const pending = [...retained];

    while (pending.length > 0) {
      const query = queryByName.get(pending.pop());
      for (const dependency of declaredQueryReferences(query)) {
        if (retained.has(dependency)) continue;
        retained.add(dependency);
        pending.push(dependency);
      }
    }

    expect([...queryNames].filter((name) => !retained.has(name))).toEqual([]);
  });

  it('materializes worker-resolvable view queries through the production worker handler', async () => {
    expect([...dashboardQueryDefects(queries).entries()]).toEqual([]);
    const requested = [...new Set(dashboard.pages.flatMap((/** @type {Record<string, unknown>} */ page) => viewsOf(page).flatMap(sourceNamesOf)))];
    const runtimeRequested = requested.filter((name) => isCliQueueQuery(queries, [name]));
    const workerRequested = requested.filter((name) => !requiresServerSource(name) && !runtimeRequested.includes(name));
    expect(requested.filter((name) => requiresServerSource(name)).sort()).toEqual(['github-api-usage', 'ingestion-health', 'ingestion-queue-sizes', 'ingestion-webhook-status']);
    vi.stubGlobal('crypto', webcrypto);
    vi.stubGlobal('indexedDB', new IDBFactory());
    let results;
    try {
      results = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
        operation: 'load-dashboard-query-sources',
        queries,
        sources: {
          runs: {
            source: 'runs', rows: [],
            metadata: { availability: 'empty', completeness: 'complete', 'as-of': '2026-10-23T12:00:00.000Z' }
          },
          'configuration-policy': {
            source: 'configuration-policy',
            rows: [],
            metadata: { availability: 'empty', completeness: 'complete', freshness: 'fresh' }
          }
        },
        sourceNames: workerRequested
      }));
    } finally {
      vi.unstubAllGlobals();
    }

    expect(Object.keys(results).sort()).toEqual([...workerRequested].sort());
    for (const name of workerRequested) {
      expect(results[name]?.source).toBe(name);
      expect(results[name]?.rows).toEqual(expect.any(Array));
      expect(results[name]?.metadata.availability, name).not.toBe('unavailable');
    }
    configureCliQueue(true);
    vi.stubGlobal('fetch', vi.fn().mockImplementation(async () => Response.json(commandQueueFixture)));
    try {
      const runtime = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
        operation: 'query-canonical-dashboard',
        sourceNames: runtimeRequested,
        context: { queries, pages: dashboard.pages }
      }));
      expect(Object.keys(runtime).sort()).toEqual(runtimeRequested.sort());
      for (const name of runtimeRequested) {
        expect(runtime[name]?.source).toBe(name);
        expect(runtime[name]?.rows.length).toBeGreaterThan(0);
        expect(runtime[name]?.metadata.availability).toBe('available');
      }
    } finally {
      configureCliQueue(false);
      vi.unstubAllGlobals();
    }
  });
});