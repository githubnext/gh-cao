import { assert, authoritativeDashboard, buildPresenterModuleUrl, builtInPage, expect, hydrateView, registerSmokeRoutes, test } from './helpers/smoke-fixtures.js';

registerSmokeRoutes();

test('persisted display horizons settle without restarting page queries as the clock advances', async ({ page }) => {
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(buildPresenterModuleUrl())};
      import { HORIZON_FILTER_STORAGE_KEY } from 'http://dashboard.test/src/components/filter-bar.js';
      let clock = Date.parse('2026-10-15T08:00:00Z');
      Date.now = () => clock += 1000;
      localStorage.setItem(HORIZON_FILTER_STORAGE_KEY, JSON.stringify({ range: '30d' }));
      let loads = 0;
      document.querySelector('#root').append(renderDashboard({
        document: {
          languageVersion: '0.1.0',
          dashboard: {
            id: 'stable-horizon', title: 'Stable horizon',
            defaults: { time: { range: '1w' } },
            pages: [{
              id: 'runs', kind: 'custom', title: 'Runs', 'filter-bar': true,
              views: [{ id: 'runs-table', data: { source: 'runs' }, mark: 'table',
                encoding: { columns: [{ field: 'run' }] } }]
            }]
          }
        },
        sources: {},
        loadPageSources: async () => {
          document.body.dataset.pageQueryLoads = String(++loads);
          if (loads > 4) localStorage.removeItem(HORIZON_FILTER_STORAGE_KEY);
          return {};
        }
      }));
    </script>
  `);
  await expect(page.locator('.horizon-toggle')).toHaveAttribute('aria-label', /Horizon 30 days/);
  await expect(page.locator('body')).toHaveAttribute('data-page-query-loads', '2');
  await page.locator('.horizon-toggle').click();
  await expect(page.getByRole('combobox', { name: 'Time window' })).toHaveValue('30d');
  await expect(page.locator('body')).toHaveAttribute('data-page-query-loads', '2');
});

test('DLS-PAGE-014 DLS-PAGE-015 built-in campaigns page renders dispatches, inventory, and campaign activity in browser', async ({ page }) => {
  const presenterModuleUrl = buildPresenterModuleUrl();
  const queryDefinitions = authoritativeDashboard.dashboard.queries;
  const campaignInsightsPage = authoritativeDashboard.dashboard.pages.find(
    (/** @type {{ id?: string }} */ candidate) => candidate.id === 'campaign-insights'
  );
  const campaignProblemsPage = authoritativeDashboard.dashboard.pages.find(
    (/** @type {{ id?: string }} */ candidate) => candidate.id === 'campaign-problems'
  );
  const campaignIssuesPage = authoritativeDashboard.dashboard.pages.find(
    (/** @type {{ id?: string }} */ candidate) => candidate.id === 'campaign-issues'
  );
  const campaignMemoryPage = authoritativeDashboard.dashboard.pages.find(
    (/** @type {{ id?: string }} */ candidate) => candidate.id === 'campaign-memory'
  );
  assert(campaignInsightsPage, 'Missing campaign insights page');
  assert(campaignProblemsPage, 'Missing campaign problems page');
  assert(campaignIssuesPage, 'Missing campaign issues page');
  assert(campaignMemoryPage, 'Missing campaign memory page');

  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(presenterModuleUrl)};
      import { setDeclaredCliActions } from 'http://dashboard.test/src/components/cli-actions.js';
      import { prepareDashboardViewSources } from 'http://dashboard.test/test/e2e/helpers/dashboard-view-sources.js';

      window.localStorage.setItem(
        'central-agentic-ops.dashboard.horizon-filter-settings',
        JSON.stringify({ range: 'all' })
      );
      setDeclaredCliActions([{
        id: 'update-campaign',
        label: 'Update campaign',
        description: "Update this campaign's agentic workflow.",
        icon: 'sync',
        command: 'gh aw update {{campaign}}',
        placement: 'row'
      }], { canExecute: false });

      const metadata = {
        'source-id': 'campaigns-fixture',
        'source-kind': 'fixture',
        'as-of': '2026-09-14T16:00:00Z',
        'retrieved-at': '2026-09-14T16:01:00Z',
        completeness: 'complete',
        freshness: 'fresh',
        availability: 'available'
      };
      const documentModel = {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'campaigns-render',
          title: 'Central Agentic Ops',
          queries: ${JSON.stringify(queryDefinitions)},
          pages: [
            ${JSON.stringify(builtInPage('campaigns', {
              id: 'campaigns',
              title: 'Campaigns',
              description: 'Activity from centrally managed campaigns.',
            }))},
            ${JSON.stringify(campaignInsightsPage)},
            ${JSON.stringify(campaignProblemsPage)},
            ${JSON.stringify(campaignMemoryPage)},
            {
              id: 'campaign-detail',
              kind: 'custom',
              title: 'Campaign',
              route: { 'hash-query-parameter': 'campaign' },
              views: [
                {
                  id: 'campaign-workflow-navigation',
                  title: 'Campaign workflows',
                  data: { sources: ['workflows', 'campaign-insight-tab-counts', 'campaign-problem-tab-counts', 'campaign-issue-tab-counts'] },
                  mark: 'element',
                  element: 'campaign-route',
                  config: { body: 'overview' }
                }
              ]
            },
            {
              id: 'campaign-workflows',
              kind: 'custom',
              title: 'Campaign',
              route: { 'hash-query-parameter': 'campaign' },
              views: [
                {
                  id: 'campaign-workflow-navigation',
                  title: 'Campaign workflows',
                  data: { sources: ['workflows', 'campaign-insight-tab-counts', 'campaign-problem-tab-counts', 'campaign-issue-tab-counts'] },
                  mark: 'element',
                  element: 'campaign-route',
                  config: { body: 'workflows' }
                },
                {
                  id: 'campaign-workflow-table',
                  title: 'Orchestrator and workers',
                  data: { source: 'campaign-workflows', 'route-field': 'campaign' },
                  mark: 'table',
                  controls: 'interactive',
                  encoding: {
                    columns: [
                      { field: 'workflow-role', type: 'ordinal', title: 'Role', display: 'label' },
                      { field: 'workflow-name', type: 'nominal', title: 'Workflow' },
                      { field: 'workflow', type: 'nominal', title: 'Definition' },
                      { field: 'rollout-mode', type: 'nominal', title: 'Mode', display: 'mode' },
                      { field: 'workflow-active', type: 'nominal', title: 'Registration', display: 'active-state' },
                      { field: 'runs', type: 'quantitative', title: 'Runs' },
                      { field: 'aic', type: 'quantitative', title: 'Total AIC', unit: 'aic' }
                    ]
                  }
                }
              ]
            },
            {
              id: 'campaign-runs',
              kind: 'custom',
              title: 'Campaign',
              route: { 'hash-query-parameter': 'campaign' },
              views: [
                {
                  id: 'campaign-run-navigation',
                  title: 'Campaign workflow runs',
                  data: { sources: ['workflows', 'campaign-insight-tab-counts', 'campaign-problem-tab-counts', 'campaign-issue-tab-counts'] },
                  mark: 'element',
                  element: 'campaign-route',
                  config: { body: 'runs' }
                },
                {
                  id: 'campaign-run-status',
                  title: 'Workflow run status',
                  data: { source: 'campaign-runs', 'route-field': 'campaign' },
                  mark: 'chart',
                  chart: 'pie',
                  'empty-message': 'No workflow runs were observed for this campaign in the current run window.',
                  encoding: {
                    x: { field: 'status', type: 'nominal', title: 'Status' },
                    y: { field: 'started-at', type: 'quantitative', aggregate: 'count', title: 'Runs' }
                  }
                },
                {
                  id: 'campaign-failure-reason-distribution',
                  title: 'Why these dispatches failed',
                  data: {
                    source: 'campaign-failed-dispatch-reason-counts',
                    'route-field': 'campaign'
                  },
                  mark: 'chart',
                  chart: 'pie',
                  'empty-message': 'No failed workflow dispatch runs were observed for this campaign in the current run window.',
                  encoding: {
                    x: { field: 'status-detail', type: 'nominal', title: 'Failure reason' },
                    y: { field: 'failed-dispatches', type: 'quantitative', title: 'Failed dispatches' }
                  }
                },
                {
                  id: 'campaign-failed-dispatch-table',
                  title: 'Failed dispatches',
                  data: { source: 'dispatches', 'route-field': 'campaign', filters: { status: ['failure', 'startup-failure', 'timed-out', 'stale'] } },
                  mark: 'table',
                  controls: 'interactive',
                  encoding: {
                    href: { field: 'run-link', type: 'nominal' },
                    columns: [
                      { field: 'status-detail', type: 'nominal', title: 'Why' },
                      { field: 'started-at', type: 'temporal', title: 'Started' },
                      { field: 'workflow-name', type: 'nominal', title: 'Workflow' },
                      { field: 'run-title', type: 'nominal', title: 'Run title' },
                      { field: 'runtime-repository', type: 'nominal', title: 'Runtime repository' }
                    ],
                    actions: [{
                      intent: 'Debug this failed workflow dispatch.',
                      presentation: 'copy-prompt',
                      icon: 'search',
                      label: 'Review debug prompt',
                      context: ['campaign', 'status', 'status-detail', 'started-at', 'workflow-name', 'run-title', 'runtime-repository', 'run-link']
                    }]
                  }
                },
                {
                  id: 'campaign-run-table',
                  title: 'All workflow runs',
                  data: { source: 'campaign-runs', 'route-field': 'campaign' },
                  mark: 'table',
                  controls: 'interactive',
                  encoding: {
                    href: { field: 'run-link', type: 'nominal' },
                    columns: [
                      { field: 'started-at', type: 'temporal', title: 'Started' },
                      { field: 'dispatch-type', type: 'nominal', title: 'Type' },
                      { field: 'workflow-name', type: 'nominal', title: 'Workflow' },
                      { field: 'run-title', type: 'nominal', title: 'Run title' },
                      { field: 'runtime-repository', type: 'nominal', title: 'Runtime repository' },
                      { field: 'status', type: 'nominal', title: 'Status', display: 'status' },
                      { field: 'status-detail', type: 'nominal', title: 'Why' }
                    ]
                  }
                }
              ]
            },
            ${JSON.stringify(campaignIssuesPage)},
            {
              id: 'campaign-reports',
              kind: 'custom',
              title: 'Campaign',
              route: { 'hash-query-parameter': 'campaign' },
              views: [
                {
                  id: 'campaign-report-navigation',
                  title: 'Campaign reports',
                  data: { sources: ['workflows', 'campaign-insight-tab-counts', 'campaign-problem-tab-counts', 'campaign-issue-tab-counts'] },
                  mark: 'element',
                  element: 'campaign-route',
                  config: { body: 'reports' }
                },
                {
                  id: 'campaign-report-table',
                  title: 'Reports',
                  data: { source: 'campaign-reports', 'route-field': 'campaign' },
                  mark: 'table',
                  controls: 'interactive',
                  encoding: {
                    columns: [
                      { field: 'outcome-title', type: 'nominal', title: 'Report', display: 'outcome-link' },
                      { field: 'outcome-status', type: 'nominal', title: 'Status', display: 'status' },
                      { field: 'rollout-mode', type: 'nominal', title: 'Mode', display: 'mode' },
                      { field: 'outcome-category', type: 'nominal', title: 'Type' },
                      { field: 'observed-at', type: 'temporal', title: 'Updated' }
                    ]
                  }
                }
              ]
            }
          ]
        }
      };
      const sources = {
        campaigns: {
          source: 'campaigns',
          rows: [
            { campaign: 'ambient-context', 'campaign-name': 'Ambient Context', 'campaign-icon': 'workflow', 'campaign-link': { 'dashboard-href': '#page-campaign-insights?campaign=ambient-context', 'dashboard-label': 'View Ambient Context campaign dashboard' } },
            { campaign: 'aw-doctor', 'campaign-name': 'AW Doctor', 'campaign-icon': 'gear', 'campaign-link': { 'dashboard-href': '#page-campaign-insights?campaign=aw-doctor', 'dashboard-label': 'View AW Doctor campaign dashboard' } }
          ],
          metadata
        },
        workflows: {
          source: 'workflows',
          rows: [
            { organization: 'githubnext', repository: 'gh-cao', campaign: 'ambient-context', 'campaign-name': 'Ambient Context', 'campaign-icon': 'workflow', workflow: '.github/workflows/ambient-context.md', 'workflow-name': 'Ambient Context', 'workflow-role': 'orchestrator', 'rollout-mode': 'review', 'workflow-active': true, 'max-ai-credits': 250, 'campaign-aic-allowance': 1050, 'campaign-inventory-warnings': 0 },
            { organization: 'githubnext', repository: 'gh-cao', campaign: 'ambient-context', 'campaign-name': 'Ambient Context', 'campaign-icon': 'workflow', workflow: '.github/workflows/ambient-context-worker.md', 'workflow-name': 'Ambient Context Worker', 'workflow-role': 'worker', 'rollout-mode': 'review', 'workflow-active': true, 'max-ai-credits': 800, 'campaign-aic-allowance': 1050, 'campaign-inventory-warnings': 0 },
            { organization: 'githubnext', repository: 'gh-cao', campaign: 'aw-doctor', 'campaign-name': 'AW Doctor', 'campaign-icon': 'gear', workflow: '.github/workflows/aw-doctor.md', 'workflow-role': 'orchestrator', 'rollout-mode': 'review', 'workflow-active': true, 'max-ai-credits': 250, 'campaign-aic-allowance': 1250, 'campaign-inventory-warnings': 1 }
          ],
          metadata
        },
        runs: {
          source: 'runs',
          rows: [
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/ambient-context-worker.md', run: '3', event: 'workflow_dispatch', 'run-title': 'Refresh ambient context', 'started-at': '2026-09-14T14:00:00Z', 'run-conclusion': 'failure', 'admission-reason': 'github-api-capacity-insufficient', 'resource-reset-at': '2026-09-14T15:00:00Z', 'rollout-mode': 'review', 'run-link': { relation: 'run', href: 'https://github.com/githubnext/gh-cao/actions/runs/3', label: 'Run 3' } },
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/ambient-context-worker.md', run: '5', event: 'workflow_dispatch', 'run-title': 'Refresh ambient context', 'started-at': '2026-09-14T13:00:00Z', 'run-conclusion': 'failure', 'admission-reason': 'github-api-capacity-insufficient', 'resource-reset-at': '2026-09-14T15:00:00Z', 'rollout-mode': 'review', 'run-link': { relation: 'run', href: 'https://github.com/githubnext/gh-cao/actions/runs/5', label: 'Run 5' } },
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/ambient-context-worker.md', run: '6', event: 'workflow_dispatch', 'run-title': 'Refresh ambient context', 'started-at': '2026-09-14T12:00:00Z', 'run-conclusion': 'failure', 'admission-reason': 'github-api-capacity-insufficient', 'resource-reset-at': '2026-09-14T15:00:00Z', 'rollout-mode': 'review', 'run-link': { relation: 'run', href: 'https://github.com/githubnext/gh-cao/actions/runs/6', label: 'Run 6' } },
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/ambient-context-worker.md', run: '7', event: 'workflow_dispatch', 'run-title': 'Refresh ambient context', 'started-at': '2026-09-14T11:00:00Z', 'run-conclusion': 'failure', 'admission-reason': 'github-api-capacity-insufficient', 'resource-reset-at': '2026-09-14T15:00:00Z', 'rollout-mode': 'review', 'run-link': { relation: 'run', href: 'https://github.com/githubnext/gh-cao/actions/runs/7', label: 'Run 7' } },
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/ambient-context-worker.md', run: '8', event: 'workflow_dispatch', 'run-title': 'Refresh ambient context', 'started-at': '2026-09-14T10:00:00Z', 'run-conclusion': 'failure', 'failure-job': 'pre_activation', 'failure-message': 'Target authority missing: add .github/workflows/cao.json to the target default branch for live mode', 'failure-step': 'Run CAO control precompute', 'rollout-mode': 'review', 'run-link': { relation: 'run', href: 'https://github.com/githubnext/gh-cao/actions/runs/8', label: 'Run 8' } },
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/aw-doctor.md', run: '1', 'started-at': '2026-08-28T10:00:00Z', 'run-conclusion': 'success', 'rollout-mode': 'review', 'aic-total': 23.9 },
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/aw-doctor.md', run: '2', 'started-at': '2026-08-29T10:00:00Z', 'run-conclusion': 'failure', 'rollout-mode': 'live' }
          ],
          metadata
        },
        usage: {
          source: 'usage',
          rows: [
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/aw-doctor.md', run: '1', invocation: 'a', aic: 23.9, 'rollout-mode': 'review' }
          ],
          metadata: { ...metadata, completeness: 'partial' }
        },
        findings: {
          source: 'findings',
          rows: [
            { workflow: '.github/workflows/aw-doctor.md', run: '2', finding: 'warning-1', 'finding-kind': 'authored-warning', 'observed-at': '2026-08-29T10:05:00Z' }
          ],
          metadata
        },
        audits: {
          source: 'audits',
          rows: [],
          metadata
        },
        tools: {
          source: 'tools',
          rows: [],
          metadata
        },
        outcomes: {
          source: 'outcomes',
          rows: [
            { organization: 'githubnext', repository: 'gh-cao', campaign: 'ambient-context', workflow: '.github/workflows/ambient-context.md', 'workflow-name': 'Ambient Context', run: '3', 'run-conclusion': 'success', 'safe-output': 'ambient-review', 'outcome-title': 'Review ambient context proposal', 'outcome-summary': 'A review proposal is ready.', 'outcome-category': 'issue', 'outcome-status': 'open', 'outcome-state': 'pending', 'rollout-mode': 'review', 'published-at': '2026-08-29T18:00:00Z', 'observed-at': '2026-08-29T18:05:00Z' },
            { organization: 'githubnext', repository: 'gh-cao', campaign: 'ambient-context', workflow: '.github/workflows/ambient-context-worker.md', 'workflow-name': 'Ambient Context Worker', run: '4', 'run-conclusion': 'success', 'safe-output': 'ambient-worker-issue', 'outcome-title': 'Review worker finding', 'outcome-summary': 'A worker finding is ready.', 'outcome-category': 'issue', 'outcome-status': 'open', 'outcome-state': 'pending', 'rollout-mode': 'review', 'published-at': '2026-08-28T19:00:00Z', 'observed-at': '2026-08-28T19:05:00Z' },
            { organization: 'githubnext', repository: 'gh-cao', campaign: 'ambient-context', workflow: '.github/workflows/ambient-context-worker.md', 'workflow-name': 'Ambient Context Worker', run: '4', 'run-conclusion': 'success', 'safe-output': 'ambient-live', 'outcome-title': 'Reconcile ambient context', 'outcome-summary': 'Updated durable guidance.', 'outcome-category': 'pull-request', 'outcome-status': 'closed', 'outcome-state': 'lifecycle-close', 'rollout-mode': 'live', 'published-at': '2026-08-28T18:00:00Z', 'observed-at': '2026-08-28T18:05:00Z' },
            { campaign: 'aw-doctor', workflow: '.github/workflows/aw-doctor.md', run: '1', 'run-conclusion': 'success', 'safe-output': 'maintenance-review', 'rollout-mode': 'review', 'published-at': '2026-08-28T10:00:00Z', 'observed-at': '2026-08-28T10:00:00Z' },
            { campaign: 'aw-doctor', workflow: '.github/workflows/aw-doctor.md', run: '2', 'run-conclusion': 'failure', 'safe-output': 'maintenance-live', 'rollout-mode': 'live', 'published-at': '2026-08-29T10:00:00Z', 'observed-at': '2026-08-29T10:00:00Z' }
          ],
          metadata
        },
        'operational-values': {
          source: 'operational-values',
          rows: [
            {
              campaign: 'ambient-context', organization: 'githubnext', repository: 'gh-cao', 'operational-value': 0.5,
              'operational-value-definition': 'ambient-context.repository-value',
              'operational-value-role': 'primary',
              'operational-value-name': 'Repository value',
              'operational-value-unit': 'value',
              'operational-value-direction': 'increase',
              'rollup-numerator': 1,
              'rollup-denominator': 2,
              'maturity-status': 'matured',
              'observed-at': '2026-09-14T14:00:00Z'
            },
            {
              campaign: 'ambient-context', organization: 'githubnext', repository: 'gh-cao', 'operational-value': 1,
              'operational-value-definition': 'ambient-context.repository-value',
              'operational-value-role': 'primary',
              'operational-value-name': 'Repository value',
              'operational-value-unit': 'value',
              'operational-value-direction': 'increase',
              'maturity-status': 'matured',
              'observed-at': '2026-09-15T14:00:00Z'
            }
          ],
          metadata
        }
      };

      const loadPageSources = (pageId, options) => prepareDashboardViewSources(
        documentModel,
        pageId,
        sources,
        { queryContext: options.queryContext, routeParameters: options.routeParameters }
      );
      const { configureSourceLoader } = await import('http://dashboard.test/src/source-store.js');
      const { dashboardViewAliasName } = await import('http://dashboard.test/src/data/queries/view-payload-compiler.js');
      configureSourceLoader(async (name, options) => {
        const loaded = await loadPageSources(options.pageId, options);
        const alias = dashboardViewAliasName(options.pageId, { id: options.viewId }, 0, name, options.sourceIndex);
        return loaded[alias] ?? loaded[name];
      });
      const viewSources = await loadPageSources('campaigns', {});
      document.querySelector('#root').append(renderDashboard({
        document: documentModel,
        sources: viewSources,
        loadPageSources
      }));
    </script>
  `);

  await expect(page.getByRole('heading', { name: 'Campaigns', level: 1 })).toBeVisible();
  const dispatchChart = page.locator('[data-view-id="campaigns-dispatches"]');
  await expect(dispatchChart.locator('[data-chart-widget="pie"]')).toBeVisible();
  await expect(dispatchChart.locator('.chart-legend-pie')).toContainText('Ambient Context');
  await page.getByRole('button', { name: 'Table' }).click();
  await expect(page.locator('[data-page-id="campaigns"] [data-view-layout="full-view"]')).toBeVisible();
  await expect(page.locator('[data-page-id="campaigns"] [data-lazy-list]')).toBeVisible();
  await expect(page.locator('[data-page-id="campaigns"] [data-table-filter]')).toBeVisible();
  await expect(page.locator('[data-page-id="campaigns"] .table-summary-row')).toBeVisible();
  const campaignRows = page.locator('[data-page-id="campaigns"] .custom-table tbody tr');
  await expect(campaignRows).toHaveCount(2);
  await expect(page.locator('[data-page-id="campaigns"] .custom-table thead tr').first().locator('th')).toHaveText([
    'Campaign',
    'Workflows',
    'Roles',
    'Modes',
    'Runs',
    'Dispatches',
    'AIC',
    'Registration'
  ]);
  const awDoctorSummary = campaignRows.filter({ hasText: 'AW Doctor' });
  await expect(awDoctorSummary).toContainText('AW Doctor');
  await expect(awDoctorSummary).toContainText('23.9');
  await expect(awDoctorSummary.getByRole('button', { name: 'Update campaign' })).toHaveCount(0);
  await expect(awDoctorSummary.getByRole('link', { name: 'View AW Doctor campaign dashboard' })).toHaveAttribute('href', '#page-campaign-insights?campaign=aw-doctor');
  await expect(awDoctorSummary.locator('[data-field="modes"] .mode-badge')).toHaveText('review');
  await expect(awDoctorSummary.locator('[data-field="registration"] .status')).toHaveText('Active');
  await page.getByRole('button', { name: 'Cards' }).click();
  const awDoctorCard = page.locator('[data-page-id="campaigns"] [data-mobile-card-list] .entity-card-list-card').filter({ hasText: 'AW Doctor' });
  await expect(awDoctorCard.locator('[data-card-drill]')).toHaveAttribute('href', '#page-campaign-insights?campaign=aw-doctor');
  await awDoctorCard.click({ position: { x: 6, y: 6 } });
  await expect(page).toHaveURL(/#page-campaign-insights\?campaign=aw-doctor$/);
  await expect(page.locator('[data-page-id="campaign-insights"] .campaign-tabs')).toBeVisible();
  await page.evaluate(() => {
    window.location.hash = '#page-campaign-detail?campaign=ambient-context';
  });
  await expect(page.locator('[data-breadcrumb-page]')).toHaveText('Ambient Context');
  await expect(page.locator('[data-page-mode]')).toBeHidden();
  await expect(page.locator('[data-nav-page-id="campaigns"]')).toHaveAttribute('aria-current', 'page');
  const campaignNavigation = page.getByRole('navigation', { name: 'Ambient Context views' });
  await expect(campaignNavigation.getByRole('link')).toHaveCount(4);
  await expect(campaignNavigation).toHaveCSS('display', 'flex');
  await expect(campaignNavigation).toHaveCSS('border-bottom-style', 'solid');
  await expect(campaignNavigation.locator('[aria-current="page"]')).toHaveCount(0);
  await expect(campaignNavigation.locator('.count-badge')).toHaveText(['1']);
  const campaignTabBadges = await campaignNavigation.locator('.count-badge').allTextContents();
  await expect(page.getByRole('heading', { name: 'Orchestrator and workers', level: 3 })).toHaveCount(0);
  await campaignNavigation.getByRole('link', { name: 'Failures' }).click();
  await expect(page).toHaveURL(/#page-campaign-problems\?campaign=ambient-context$/);
  await expect(campaignNavigation.getByRole('link', { name: 'Failures' })).toHaveAttribute('aria-current', 'page');
  await expect(campaignNavigation.locator('.count-badge')).toHaveText(campaignTabBadges);
  await expect(page.locator('[data-page-id="campaign-problems"] [data-view-id="campaign-current-runtime-problems"]')).toBeVisible();
  const currentCampaignUrl = page.url();
  await campaignNavigation.getByRole('link', { name: 'Failures' }).click();
  expect(page.url()).toBe(currentCampaignUrl);
  await expect(page.locator('[data-page-id="campaign-problems"] [data-view-id="campaign-current-runtime-problems"]')).toBeVisible();
  await page.evaluate(() => {
    window.location.hash = '#page-campaign-memory?campaign=ambient-context';
  });
  await expect(page).toHaveURL(/#page-campaign-memory\?campaign=ambient-context$/);
  await expect(campaignNavigation.getByRole('link', { name: 'Memory' })).toHaveAttribute('aria-current', 'page');
  await expect(page.locator('.campaign-memory-browser')).toContainText('Repository memory is unavailable.');
  await page.evaluate(() => {
    window.location.hash = '#page-campaign-workflows?campaign=ambient-context';
  });
  await expect(campaignNavigation.locator('[aria-current="page"]')).toHaveCount(0);
  await page.getByRole('button', { name: 'Table' }).click();
  await expect(page.getByRole('heading', { name: 'Orchestrator and workers', level: 2 })).toBeVisible();
  const campaignWorkflowRows = page.locator('[data-page-id="campaign-workflows"] .custom-table tbody tr');
  await expect(campaignWorkflowRows).toHaveCount(2);
  await expect(page.locator('[data-page-id="campaign-workflows"] .custom-table thead tr').first().locator('th')).toHaveText([
    'Role',
    'Workflow',
    'Definition',
    'Mode',
    'Registration',
    'Runs',
    'Total AIC'
  ]);
  await expect(campaignWorkflowRows.first()).toContainText('OrchestratorAmbient Context');
  await expect(campaignWorkflowRows.first().locator('td').nth(5)).toHaveText('0');
  await expect(campaignWorkflowRows.first().locator('td').nth(6)).toHaveText('0');
  await expect(campaignWorkflowRows.nth(1)).toContainText('WorkerAmbient Context Worker');
  await page.evaluate(() => {
    window.location.hash = '#page-campaign-runs?campaign=ambient-context';
  });
  await expect(page).toHaveURL(/#page-campaign-runs\?campaign=ambient-context$/);
  const campaignRunsPage = page.locator('[data-page-id="campaign-runs"]');
  await expect(campaignRunsPage.locator('.custom-view-grid > .custom-view').first()).toHaveAttribute('data-view-id', 'campaign-run-navigation');
  await expect(campaignNavigation.locator('[aria-current="page"]')).toHaveCount(0);
  await expect(campaignRunsPage.locator('[data-view-id="campaign-run-status"] [data-chart-widget="pie"]')).toBeVisible();
  await expect(campaignRunsPage.locator('[data-view-id="campaign-failure-reason-distribution"] [data-chart-widget="pie"]')).toBeVisible();
  await page.getByRole('button', { name: 'Table' }).click();
  await expect(campaignRunsPage.locator('[data-view-id="campaign-run-table"] tbody tr')).toHaveCount(5);
  await page.getByRole('button', { name: 'Chart' }).click();
  await campaignNavigation.getByRole('link', { name: 'Reports' }).click();
  await expect(campaignNavigation.getByRole('link', { name: 'Reports' })).toHaveAttribute('aria-current', 'page');
  await page.getByRole('button', { name: 'Cards' }).click();
  const campaignIssueView = page.locator('[data-page-id="campaign-issues"] [data-view-id="campaign-issue-table"]');
  await expect(campaignIssueView).toBeVisible();
  await expect(campaignIssueView).toContainText('Review worker finding');
  await expect(campaignIssueView.getByRole('listitem')).toHaveCount(1);
  await expect(campaignIssueView).not.toContainText('Review ambient context proposal');
  await expect(campaignNavigation.locator('.count-badge')).toHaveText(campaignTabBadges);
  await page.getByRole('button', { name: 'Chart' }).click();
  await page.evaluate(() => {
    window.location.hash = '#page-campaign-detail?campaign=ambient-context';
  });
  await expect(page.locator('[data-page-id="campaign-detail"]')).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(campaignNavigation).toHaveCSS('display', 'grid');
  await expect(campaignNavigation).toHaveCSS('gap', '0px');
  await expect(campaignNavigation).toHaveCSS('overflow', 'hidden');
  const mobileCampaignLinks = campaignNavigation.locator('a');
  await expect(mobileCampaignLinks).toHaveCount(4);
  await expect(mobileCampaignLinks.first().locator('.tab-trailing-icon')).toBeVisible();
  expect(await mobileCampaignLinks.first().evaluate((link) => {
    return link.lastElementChild?.classList.contains('tab-trailing-icon') === true;
  })).toBe(true);
  await expect.poll(() => mobileCampaignLinks.first().evaluate((link) => {
    link.focus();
    return getComputedStyle(link).outlineOffset;
  })).toBe('-3px');
  const mobileLinkBoxes = await mobileCampaignLinks.evaluateAll((links) => links.map((link) => {
    const box = link.getBoundingClientRect();
    return { height: box.height, top: box.top };
  }));
  expect(mobileLinkBoxes.every((box) => box.height >= 44)).toBe(true);
  expect(mobileLinkBoxes.every((box, index) => index === 0 || box.top > mobileLinkBoxes[index - 1].top)).toBe(true);

  await campaignNavigation.getByRole('link', { name: 'Insights' }).click();
  const campaignInsights = page.locator('[data-page-id="campaign-insights"]');
  await expect(campaignInsights).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  const prompt = campaignInsights.locator('.semantic-prompt-action').first();
  if (await prompt.count() > 0) {
    await expect(prompt).toHaveCSS('position', 'static');
    await expect(prompt.locator('.table-intent-button span')).toBeHidden();
    await expect(prompt.locator('.table-intent-button')).toHaveAccessibleName(/^Propose fix/);
  }
  await expect(campaignInsights.locator('.view-description-tooltip')).toHaveCount(0);
  await expect(campaignInsights.locator('[data-view-id="campaign-audit-event-summary-buckets"]')).toHaveCount(0);
  for (let step = 0; step < 3 && await campaignInsights.getAttribute('data-view-mode') !== 'table'; step++) {
    const before = await campaignInsights.getAttribute('data-view-mode');
    await page.locator('.mobile-view-mode-toggle').click();
    await expect(campaignInsights).not.toHaveAttribute('data-view-mode', before ?? '');
  }
  await expect(campaignInsights).toHaveAttribute('data-view-mode', 'table');
  await expect(campaignInsights.locator('.view-mode-control')).toHaveCount(1);
  await expect(campaignInsights.getByRole('navigation', { name: 'Ambient Context views' })).toBeVisible();
  await expect(campaignInsights.getByRole('navigation', { name: 'Ambient Context views' })).toHaveCSS('display', 'grid');
  const mobileBack = page.getByRole('button', { name: 'Go back' });
  await expect(mobileBack).toBeVisible();
  await expect(page.locator('.overview-header')).toContainText('Operational activity for the Ambient Context campaign.');
  const performanceBaseline = campaignInsights.locator('[data-view-id="campaign-performance-baseline"]');
  await expect(performanceBaseline).toBeVisible();
  await expect(performanceBaseline).toContainText('Execution reliability');
  await expect(performanceBaseline).toContainText('Success rate');
  await expect(performanceBaseline).toContainText('Successful attempts');
  await expect(performanceBaseline).toContainText('Reported output items');
  await expect(performanceBaseline).toContainText('Average AIC / costed successful attempt');
  await expect(performanceBaseline).toHaveAccessibleDescription(/gh aw logs/);
  await expect(performanceBaseline).toHaveAccessibleDescription(/reruns count separately/);
  const outcomeDistribution = campaignInsights.locator('[data-view-id="campaign-baseline-outcomes"]');
  await expect(outcomeDistribution).not.toBeVisible();
  await campaignInsights.locator('details')
    .filter({ has: page.locator('[data-view-id="campaign-baseline-outcomes"]') })
    .locator(':scope > summary').click();
  await expect(outcomeDistribution).toBeVisible();
  await expect(performanceBaseline).toBeVisible();
  await expect(campaignInsights.locator('.measure-history-operational-value')).toHaveCount(0);
  await expect(campaignInsights.getByText('All native repository timelines', { exact: true })).toHaveCount(0);
  await mobileBack.click();
  await expect(page).toHaveURL(/#page-campaign-detail\?campaign=ambient-context$/);
  await expect(page.locator('[data-page-id="campaign-detail"]')).toBeVisible();
  await expect(campaignNavigation).toBeVisible();
});


test('DLS-SAFE-004 DLS-SAFE-007 DLS-SAFE-008 DLS-SAFE-010 custom findings table exposes accessible names, labeled columns, textual data states, and only safe labeled external links in browser', async ({ page }) => {
  const presenterModuleUrl = buildPresenterModuleUrl();

  await page.setContent(`
    <a id="plain-external-link" href="https://example.com/docs">External documentation</a>
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(presenterModuleUrl)};

      const dashboardDocument = {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'security-dashboard',
          title: 'Security Dashboard',
          repository: 'githubnext/gh-cao',
          pages: [
            {
              id: 'finding-review',
              kind: 'custom',
              title: 'Findings',
              views: [{
                id: 'finding-review-table',
                data: { source: 'findings' },
                mark: 'table',
                encoding: {
                  columns: [
                    { field: 'finding-summary', type: 'nominal' },
                    { field: 'issue-link', type: 'nominal' }
                  ],
                  href: { field: 'issue-link', type: 'nominal' }
                }
              }]
            }
          ]
        }
      };

      const sources = {
        findings: {
          source: 'findings',
          rows: [
            {
              finding: 'unsafe-html',
              'finding-summary': '<img src=x onerror=alert(1)>',
              'finding-severity': 'critical',
              'finding-status': 'open',
              organization: 'github',
              repository: 'gh-cao',
              workflow: '.github/workflows/daily.yml',
              'observed-at': '2026-08-29T12:00:00Z',
              'issue-link': {
                relation: 'issue',
                href: 'https://example.com/issues/1',
                label: 'Issue 1 label'
              }
            }
          ],
          metadata: {
            'source-id': 'findings-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      };

      document.querySelector('#root').append(renderDashboard({ document: dashboardDocument, sources }));
    </script>
  `);

  await expect(page.getByRole('link', { name: 'Skip to main content' })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Primary' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Findings', exact: true, level: 1 })).toBeVisible();
  await page.getByRole('button', { name: 'Table' }).click();
  await expect(page.locator('[data-view-id="finding-review-table"] .custom-table')).toBeVisible();
  await expect(page.locator('.data-state-summary')).toBeHidden();
  await expect(page.getByRole('columnheader', { name: 'Issue Link' })).toBeVisible();
  await expect(page.locator('[data-page-id="finding-review"] .custom-table tbody td').first()).toContainText('<img src=x onerror=alert(1)>');
  await expect(page.locator('[data-page-id="finding-review"] .custom-table tbody img')).toHaveCount(0);

  const issueLink = page.getByRole('link', { name: 'Issue 1 label' });
  await expect(issueLink).toBeVisible();
  await expect(issueLink).toHaveAttribute('href', 'https://example.com/issues/1');
  await expect(issueLink).toHaveAttribute('target', '_blank');
  await expect(issueLink).toHaveAttribute('rel', 'noopener noreferrer');

  const externalLinkMask = await page.locator('#plain-external-link').evaluate((link) => getComputedStyle(link, '::after').maskImage);
  const repositoryLinkMask = await page.locator('.repository-link').evaluate((link) => getComputedStyle(link, '::after').maskImage);
  expect(externalLinkMask).toBe('none');
  await expect(page.locator('.refresh-button')).toHaveCount(0);
  expect(repositoryLinkMask).toBe('none');
});

test('DLS-VIEW-013 DLS-VIEW-014 DLS-VIEW-015 DLS-SAFE-006 custom views render available, empty, and unavailable states with only context-permitted observations in browser', async ({ page }) => {
  const presenterModuleUrl = buildPresenterModuleUrl();

  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(presenterModuleUrl)};
      import { prepareDashboardViewSources } from 'http://dashboard.test/test/e2e/helpers/dashboard-view-sources.js';

      const dashboardDocument = {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'custom-dashboard',
          title: 'Custom Dashboard',
          pages: [
            {
              id: 'custom-views',
              kind: 'custom',
              title: 'Custom Views',
              views: [
                {
                  id: 'total-aic',
                  title: 'Total AI Credits',
                  data: {
                    source: 'usage',
                    filters: {
                      'rollout-mode': ['review', 'live']
                    }
                  },
                  mark: 'metric',
                  encoding: {
                    value: {
                      field: 'aic',
                      type: 'quantitative',
                      aggregate: 'sum'
                    }
                  }
                },
                {
                  id: 'findings-table',
                  title: 'Findings Table',
                  data: {
                    source: 'findings',
                    scope: {
                      repositories: ['gh-cao']
                    },
                    time: {
                      start: '2026-08-29T00:00:00Z',
                      end: '2026-08-30T00:00:00Z'
                    }
                  },
                  mark: 'table',
                  encoding: {
                    columns: [
                      { field: 'finding-summary' },
                      { field: 'finding-severity' },
                      { field: 'finding-status' }
                    ],
                    href: {
                      field: 'pull-request-link'
                    }
                  }
                },
                {
                  id: 'daily-runs',
                  title: 'Daily Runs',
                  data: {
                    source: 'runs'
                  },
                  mark: 'chart',
                  encoding: {
                    x: {
                      field: 'started-at',
                      type: 'temporal',
                      'time-unit': 'day'
                    },
                    y: {
                      field: 'run',
                      type: 'quantitative',
                      aggregate: 'count'
                    },
                    color: {
                      field: 'run-conclusion',
                      type: 'nominal'
                    },
                    href: {
                      field: 'run-link'
                    }
                  }
                },
                {
                  id: 'empty-usage',
                  title: 'Empty Usage',
                  data: {
                    source: 'empty-usage'
                  },
                  mark: 'metric',
                  encoding: {
                    value: {
                      field: 'aic',
                      type: 'quantitative',
                      aggregate: 'sum'
                    }
                  }
                },
                {
                  id: 'missing-source',
                  title: 'Missing Source',
                  data: {
                    source: 'missing-source'
                  },
                  mark: 'table',
                  encoding: {
                    columns: [
                      { field: 'finding-summary' }
                    ]
                  }
                }
              ]
            }
          ]
        }
      };

      const sources = {
        usage: {
          source: 'usage',
          rows: [
            { organization: 'github', repository: 'gh-cao', workflow: '.github/workflows/daily.yml', run: '1001', engine: 'actions', 'requested-model': 'gpt-4o', 'resolved-model': 'gpt-4.1', 'rollout-mode': 'live', aic: 2, 'observed-at': '2026-08-29T10:00:00Z' },
            { organization: 'github', repository: 'gh-cao', workflow: '.github/workflows/daily.yml', run: '1002', engine: 'actions', 'requested-model': 'gpt-4o', 'resolved-model': 'gpt-4.1', 'rollout-mode': 'review', aic: 3, 'observed-at': '2026-08-29T11:00:00Z' }
          ],
          metadata: {
            'source-id': 'usage-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        },
        findings: {
          source: 'findings',
          rows: [
            {
              code: 'finding-1',
              organization: 'github',
              repository: 'gh-cao',
              'observed-at': '2026-08-29T12:00:00Z',
              'finding-summary': 'Unsafe dependency',
              'finding-severity': 'high',
              'finding-status': 'open',
              'pull-request-link': {
                relation: 'pull-request',
                href: 'https://example.com/pull/1',
                label: 'PR 1'
              }
            },
            {
              code: 'finding-2',
              organization: 'github',
              repository: 'other-repo',
              'observed-at': '2026-08-29T13:00:00Z',
              'finding-summary': 'Out of scope finding',
              'finding-severity': 'medium',
              'finding-status': 'resolved',
              'pull-request-link': {
                relation: 'pull-request',
                href: 'https://example.com/pull/2',
                label: 'PR 2'
              }
            },
            {
              code: 'finding-3',
              organization: 'github',
              repository: 'gh-cao',
              'observed-at': '2026-08-30T01:00:00Z',
              'finding-summary': 'Out of range finding',
              'finding-severity': 'low',
              'finding-status': 'open'
            }
          ],
          metadata: {
            'source-id': 'findings-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'partial',
            freshness: 'stale',
            availability: 'available'
          }
        },
        runs: {
          source: 'runs',
          rows: [
            {
              run: '1001',
              'started-at': '2026-08-29T10:00:00Z',
              'run-conclusion': 'success',
              'run-link': { relation: 'run', href: 'https://github.com/github/central-agentic-ops/actions/runs/1001', label: 'Run 1001' }
            },
            {
              run: '1002',
              'started-at': '2026-08-29T11:00:00Z',
              'run-conclusion': 'failure',
              'run-link': { relation: 'run', href: 'https://github.com/github/central-agentic-ops/actions/runs/1002', label: 'Run 1002' }
            }
          ],
          metadata: {
            'source-id': 'runs-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        },
        'empty-usage': {
          source: 'empty-usage',
          rows: [],
          metadata: {
            'source-id': 'empty-usage-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'unknown',
            freshness: 'unknown',
            availability: 'empty'
          }
        }
      };

      const viewSources = await prepareDashboardViewSources(dashboardDocument, 'custom-views', sources);
      document.querySelector('#root').append(renderDashboard({ document: dashboardDocument, sources: viewSources }));
    </script>
  `);

  await expect(page.getByRole('heading', { name: 'Custom Views', exact: true, level: 1 })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Total AI Credits' })).toBeVisible();
  await expect(page.locator('[data-metric-value="aic"]')).toHaveText('5');
  const metricSection = page.locator('.page-section').filter({ has: page.getByRole('heading', { name: 'Total AI Credits' }) });
  await expect(metricSection).not.toContainText('Source: usage');
  await expect(metricSection).not.toContainText('Filters:');

  await page.getByRole('button', { name: 'Table' }).click();
  await expect(page.getByRole('heading', { name: 'Findings Table' })).toBeVisible();
  await expect(page.locator('.custom-table tbody tr')).toHaveCount(1);
  await expect(page.getByRole('link', { name: 'PR 1' })).toHaveAttribute('href', 'https://example.com/pull/1');
  const tableSection = page.locator('.page-section').filter({ has: page.getByRole('heading', { name: 'Findings Table' }) });
  await expect(tableSection).not.toContainText('Scope:');
  await expect(tableSection).not.toContainText('Time:');
  await expect(tableSection).not.toContainText('Out of scope finding');
  await expect(tableSection).not.toContainText('Out of range finding');

  await page.getByRole('button', { name: 'Chart' }).click();
  await expect(page.getByRole('heading', { name: 'Daily Runs' })).toBeVisible();
  await expect(page.locator('.chart-default')).toHaveCount(0);
  await expect(page.locator('[data-chart-legend="text"]')).toHaveCount(0);
  await expect(page.locator('[data-chart-legend="visual"] li')).toHaveCount(2);
  await expect(page.locator('[data-chart-legend="visual"] li span')).toHaveText(['failure', 'success']);
  await expect(page.locator('.chart-view .table-region')).toHaveCount(0);
  await expect(page.locator('.page-section').filter({ has: page.getByRole('heading', { name: 'Daily Runs' }) }).locator('.view-source')).toHaveCount(0);

  await hydrateView(page, 'Empty Usage');
  await expect(page.getByRole('heading', { name: 'Empty Usage' })).toBeVisible();
  await expect(page.locator('[data-view-availability="empty"]')).toHaveText('No observations matched the effective context.');
  const emptySection = page.locator('.page-section').filter({ has: page.getByRole('heading', { name: 'Empty Usage' }) });
  await expect(emptySection).toContainText('Affected source: empty-usage');

  await page.getByRole('button', { name: 'Table' }).click();
  await hydrateView(page, 'Missing Source');
  await expect(page.getByRole('heading', { name: 'Missing Source' })).toBeVisible();
  await expect(page.locator('[data-view-availability="unavailable"]')).toHaveText('This view cannot be shown because its data query failed.');
  const unavailableSection = page.locator('.page-section').filter({ has: page.getByRole('heading', { name: 'Missing Source' }) });
  await expect(unavailableSection).toContainText('Affected source: missing-source');
});

test('DLS-SAFE-007 DLS-SAFE-008 keyboard navigation moves across labeled page sections in browser', async ({ page }) => {
  const presenterModuleUrl = buildPresenterModuleUrl();

  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard, enableDashboardKeyboardNavigation } from ${JSON.stringify(presenterModuleUrl)};

      const dashboardDocument = {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'runs-dashboard',
          title: 'Runs Dashboard',
          pages: [
            {
              id: 'keyboard-navigation',
              kind: 'custom',
              title: 'Keyboard Navigation',
              views: [
                { id: 'runs-source', data: { source: 'runs' } },
                { id: 'outcomes-source', data: { source: 'outcomes' } }
              ]
            }
          ]
        }
      };

      const sources = {
        runs: {
          source: 'runs',
          rows: [
            {
              organization: 'github',
              repository: 'gh-cao',
              workflow: '.github/workflows/daily.yml',
              run: '1001',
              'run-status': 'completed',
              'run-conclusion': 'success',
              'rollout-mode': 'live',
              engine: 'actions',
              'requested-model': 'gpt-4o',
              'resolved-model': 'gpt-4.1',
              'started-at': '2026-08-29T10:00:00Z',
              'run-link': {
                relation: 'run',
                href: 'https://example.com/runs/1001',
                label: 'Run 1001'
              }
            }
          ],
          metadata: {
            'source-id': 'runs-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        },
        outcomes: {
          source: 'outcomes',
          rows: [
            { run: '1001', 'outcome-state': 'accepted' }
          ],
          metadata: {
            'source-id': 'outcomes-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-08-29T20:00:00Z',
            'retrieved-at': '2026-08-29T20:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      };

      const root = document.querySelector('#root');
      const dashboard = renderDashboard({ document: dashboardDocument, sources });
      root.append(dashboard);
      enableDashboardKeyboardNavigation(dashboard);
    </script>
  `);

  const sections = page.locator('[data-page-id="keyboard-navigation"] .page-section');
  await expect(sections).toHaveCount(2);
  await expect(page.locator('#keyboard-navigation-runs-source-heading')).toHaveText('Runs Source');
  await expect(page.locator('#keyboard-navigation-outcomes-source-heading')).toHaveText('Outcomes Source');

  await sections.nth(0).focus();
  await page.keyboard.press('ArrowDown');
  await expect(sections.nth(1)).toBeFocused();
  await page.keyboard.press('ArrowUp');
  await expect(sections.nth(0)).toBeFocused();
});

test('repository page template follows its JSON-declared hash query route in browser', async ({ page }) => {
  const presenterModuleUrl = buildPresenterModuleUrl();
  const dashboardDocument = authoritativeDashboard;
  await page.goto('http://dashboard.test/#page-repository-workflow-inventory?repository=octo-org%2Focto-repo');
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(presenterModuleUrl)};
      import { prepareDashboardViewSources } from 'http://dashboard.test/test/e2e/helpers/dashboard-view-sources.js';
      const dashboardDocument = ${JSON.stringify(dashboardDocument)};
      const metadata = {
        'source-id': 'workflows-fixture',
        'source-kind': 'fixture',
        'as-of': '2026-08-30T08:00:00Z',
        'retrieved-at': '2026-08-30T08:01:00Z',
        completeness: 'complete',
        freshness: 'fresh',
        availability: 'available'
      };
      const sources = {
        workflows: {
          source: 'workflows',
          metadata,
          rows: [
            { organization: 'octo-org', repository: 'octo-repo', workflow: 'review.md', 'workflow-name': 'Review', 'workflow-active': 'true', runs: 2, aic: 3 },
            { organization: 'octo-org', repository: 'octo-repo', workflow: 'triage.md', 'workflow-name': 'Triage', 'workflow-active': 'true', runs: 4, aic: 5 },
            { organization: 'other-org', repository: 'other-repo', workflow: 'other.md', 'workflow-name': 'Other', 'workflow-active': 'true', runs: 1, aic: 1 }
          ]
        },
        repositories: {
          source: 'repositories',
          metadata,
          rows: [
            { organization: 'octo-org', repository: 'octo-repo', 'rollout-mode': 'review', 'observed-at': '2026-08-30T08:00:00Z' },
            { organization: 'other-org', repository: 'other-repo', 'rollout-mode': 'review', 'observed-at': '2026-08-30T08:00:00Z' }
          ]
        },
        runs: { source: 'runs', metadata, rows: [] },
        outcomes: { source: 'outcomes', metadata, rows: [] },
        audits: { source: 'audits', metadata, rows: [] }
      };
      const loadPageSources = (pageId, options) => prepareDashboardViewSources(
        dashboardDocument,
        pageId,
        sources,
        { queryContext: options.queryContext, routeParameters: options.routeParameters }
      );
      const viewSources = await loadPageSources('repository-workflow-inventory', {
        routeParameters: { repository: 'octo-org/octo-repo' }
      });
      document.querySelector('#root').append(renderDashboard({
        document: dashboardDocument,
        sources: viewSources,
        loadPageSources
      }));
    </script>
  `);

  await expect(page.getByRole('heading', { level: 1 })).toHaveText('octo-org/octo-repo');
  await expect(page.getByRole('navigation', { name: 'octo-org/octo-repo views' })
    .getByRole('link', { name: 'Workflows' })).toHaveAttribute('aria-current', 'page');
  const repositoryTable = page.locator('[data-page-id="repository-workflow-inventory"] .custom-table');
  await expect(repositoryTable).toContainText('Review');
  await expect(repositoryTable).toContainText('Triage');
  await expect(repositoryTable).not.toContainText('Other');

  await page.evaluate(() => {
    window.location.hash = '#page-repository-workflow-inventory?repository=other-org%2Fother-repo';
  });

  await expect(page.getByRole('heading', { level: 1 })).toHaveText('other-org/other-repo');
  await expect(repositoryTable).toContainText('Other');
  await expect(repositoryTable).not.toContainText('Review');
});

test('workflow page template follows its JSON-declared route and renders attributed reports', async ({ page }) => {
  const presenterModuleUrl = buildPresenterModuleUrl();
  const workflowRoute = 'githubnext%2Fgh-cao%3A.github%2Fworkflows%2Fambient-context.md';
  await page.goto(`http://dashboard.test/#page-workflow-detail?workflow=${workflowRoute}`);
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(presenterModuleUrl)};
      import { prepareDashboardViewSources } from 'http://dashboard.test/test/e2e/helpers/dashboard-view-sources.js';
      const metadata = {
        'source-id': 'workflow-fixture',
        'source-kind': 'fixture',
        'as-of': '2026-08-31T20:00:00Z',
        'retrieved-at': '2026-08-31T20:01:00Z',
        completeness: 'complete',
        freshness: 'fresh',
        availability: 'available'
      };
      const dashboardDocument = {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'workflow-route',
          title: 'Central Agentic Ops',
          repository: 'githubnext/gh-cao',
          pages: [
            {
              id: 'repositories',
              kind: 'custom',
              title: 'Repositories',
              views: []
            },
            {
              id: 'repository-detail',
              kind: 'custom',
              title: 'Repository',
              route: { 'hash-query-parameter': 'repository' },
              views: []
            },
            {
              id: 'workflow-runtime',
              kind: 'custom',
              title: 'Workflow runtime',
              route: { 'hash-query-parameter': 'workflow' },
              views: []
            },
            {
              id: 'workflow-runs',
              kind: 'custom',
              title: 'Workflow runs',
              route: { 'hash-query-parameter': 'workflow' },
              views: [
                {
                  id: 'workflow-runs-route',
                  title: 'Workflow runs',
                  data: { sources: ['workflows'] },
                  mark: 'element',
                  element: 'workflow-route-page',
                  config: { body: 'runs' }
                },
                {
                  id: 'workflow-runs-table',
                  title: 'Runs',
                  data: { source: 'workflow-runs', 'route-field': 'workflow-route' },
                  mark: 'table',
                  controls: 'interactive',
                  encoding: {
                    columns: [
                      { field: 'run', type: 'nominal', title: 'Run' },
                      { field: 'run-title', type: 'nominal', title: 'Title' },
                      { field: 'run-status', type: 'nominal', title: 'Status', display: 'status' },
                      { field: 'run-conclusion', type: 'nominal', title: 'Conclusion', display: 'status' },
                      { field: 'event', type: 'nominal', title: 'Trigger' },
                      { field: 'started-at', type: 'temporal', title: 'Started' }
                    ],
                    href: { field: 'run-link', type: 'nominal' }
                  }
                }
              ]
            },
            {
              id: 'workflow-detail',
              kind: 'custom',
              title: 'Workflow',
              description: 'Workflow reports.',
              route: { 'hash-query-parameter': 'workflow' },
              views: [
                {
                  id: 'workflow-reports-route',
                  title: 'Workflow reports',
                  data: { sources: ['workflows'] },
                  mark: 'element',
                  element: 'workflow-route-page',
                  config: { body: 'reports' }
                },
                {
                  id: 'workflow-report-table',
                  title: 'Reports',
                  data: { source: 'workflow-reports', 'route-field': 'workflow-route' },
                  mark: 'table',
                  encoding: {
                    columns: [
                      { field: 'outcome-title', type: 'nominal', title: 'Report', display: 'outcome-link' },
                      { field: 'outcome-status', type: 'nominal', title: 'Status', display: 'status' },
                      { field: 'rollout-mode', type: 'nominal', title: 'Mode', display: 'mode' },
                      { field: 'outcome-category', type: 'nominal', title: 'Type' },
                      { field: 'observed-at', type: 'temporal', title: 'Updated' }
                    ]
                  }
                }
              ]
            },
            {
              id: 'outcome-detail',
              kind: 'custom',
              title: 'Outcome',
              route: { 'hash-query-parameter': 'outcome' },
              views: [{
                id: 'outcome-record',
                title: 'Outcome',
                data: { sources: ['outcomes'] },
                mark: 'element',
                element: 'outcome-detail'
              }]
            }
          ]
        }
      };
      const sources = {
        workflows: {
          source: 'workflows',
          metadata,
          rows: [{
            organization: 'githubnext',
            repository: 'gh-cao',
            campaign: 'ambient-context',
            'campaign-name': 'Ambient Context',
            workflow: '.github/workflows/ambient-context.md',
            'workflow-name': 'Ambient Context',
            'workflow-role': 'orchestrator',
            'rollout-mode': 'review'
          }]
        },
        'workflow-reports': {
          source: 'workflow-reports',
          metadata,
          rows: [{
            organization: 'customer',
            repository: 'target',
            'runtime-repository': 'githubnext/gh-cao',
            'workflow-route': 'githubnext/gh-cao:.github/workflows/ambient-context.md',
            workflow: '.github/workflows/ambient-context.md',
            'workflow-name': 'Ambient Context',
            'safe-output': 'report-1',
            'outcome-title': 'Debug ambient context workflow failure',
            'outcome-summary': 'Investigated the reported workflow failure.',
            'outcome-category': 'pull-request',
            'outcome-status': 'closed',
            'rollout-mode': 'review',
            'observed-at': '2026-08-31T19:00:00Z'
          }]
        },
        'workflow-runs': {
          source: 'workflow-runs',
          metadata,
          rows: [
            {
              organization: 'githubnext',
              repository: 'gh-cao',
              workflow: '.github/workflows/ambient-context.md',
              'workflow-route': 'githubnext/gh-cao:.github/workflows/ambient-context.md',
              run: '102',
              'run-title': 'Scheduled review',
              event: 'schedule',
              'run-status': 'completed',
              'run-conclusion': 'success',
              'started-at': '2026-08-31T20:00:00Z',
              'run-link': {
                relation: 'run',
                href: 'https://github.com/githubnext/gh-cao/actions/runs/102',
                label: 'View run 102'
              }
            },
            {
              organization: 'githubnext',
              repository: 'gh-cao',
              workflow: '.github/workflows/ambient-context.md',
              'workflow-route': 'githubnext/gh-cao:.github/workflows/ambient-context.md',
              run: '101',
              'run-title': 'Manual review',
              event: 'workflow_dispatch',
              'run-status': 'completed',
              'run-conclusion': 'failure',
              'started-at': '2026-08-31T19:00:00Z'
            }
          ]
        }
      };
      const loadPageSources = (pageId, options) => prepareDashboardViewSources(
        dashboardDocument,
        pageId,
        sources,
        { queryContext: options.queryContext, routeParameters: options.routeParameters }
      );
      const routeParameters = {
        workflow: 'githubnext/gh-cao:.github/workflows/ambient-context.md'
      };
      const viewSources = await loadPageSources('workflow-detail', { routeParameters });
      document.querySelector('#root').append(renderDashboard({
        document: dashboardDocument,
        sources: viewSources,
        loadPageSources
      }));
    </script>
  `);

  await expect(page.getByRole('heading', { name: 'Ambient Context', level: 1 })).toBeVisible();
  await expect(page.locator('#page-workflow-detail .custom-table')).toContainText('Debug ambient context workflow failure');
  await expect(page.locator('#page-workflow-detail .custom-table .status-success')).toHaveText('closed');
  await expect(page.locator('#page-workflow-detail .custom-table .mode-review')).toHaveText('review');
  await page.goto(`http://dashboard.test/#page-workflow-detail?workflow=${workflowRoute}`);
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(presenterModuleUrl)};
      const metadata = {
        'source-id': 'workflow-fixture',
        'source-kind': 'fixture',
        'as-of': '2026-08-31T20:00:00Z',
        'retrieved-at': '2026-08-31T20:01:00Z',
        completeness: 'complete',
        freshness: 'fresh',
        availability: 'available'
      };
      const dashboardDocument = {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'workflow-route',
          title: 'Central Agentic Ops',
          repository: 'githubnext/gh-cao',
          pages: [
            {
              id: 'repositories',
              kind: 'custom',
              title: 'Repositories',
              views: []
            },
            {
              id: 'repository-detail',
              kind: 'custom',
              title: 'Repository',
              route: { 'hash-query-parameter': 'repository' },
              views: []
            },
            {
              id: 'workflow-runtime',
              kind: 'custom',
              title: 'Workflow runtime',
              route: { 'hash-query-parameter': 'workflow' },
              views: []
            },
            {
              id: 'workflow-runs',
              kind: 'custom',
              title: 'Workflow runs',
              route: { 'hash-query-parameter': 'workflow' },
              views: [
                {
                  id: 'workflow-runs-route',
                  title: 'Workflow runs',
                  data: { sources: ['workflows'] },
                  mark: 'element',
                  element: 'workflow-route-page',
                  config: { body: 'runs' }
                },
                {
                  id: 'workflow-runs-table',
                  title: 'Runs',
                  data: { source: 'workflow-runs', 'route-field': 'workflow-route' },
                  mark: 'table',
                  controls: 'interactive',
                  encoding: {
                    columns: [
                      { field: 'run', type: 'nominal', title: 'Run' },
                      { field: 'run-title', type: 'nominal', title: 'Title' },
                      { field: 'run-status', type: 'nominal', title: 'Status', display: 'status' },
                      { field: 'run-conclusion', type: 'nominal', title: 'Conclusion', display: 'status' },
                      { field: 'event', type: 'nominal', title: 'Trigger' },
                      { field: 'started-at', type: 'temporal', title: 'Started' }
                    ],
                    href: { field: 'run-link', type: 'nominal' }
                  }
                }
              ]
            },
            {
              id: 'workflow-detail',
              kind: 'custom',
              title: 'Workflow',
              description: 'Workflow reports.',
              route: { 'hash-query-parameter': 'workflow' },
              views: [
                {
                  id: 'workflow-reports-route',
                  title: 'Workflow reports',
                  data: { sources: ['workflows'] },
                  mark: 'element',
                  element: 'workflow-route-page',
                  config: { body: 'reports' }
                },
                {
                  id: 'workflow-report-table',
                  title: 'Reports',
                  data: { source: 'workflow-reports', 'route-field': 'workflow-route' },
                  mark: 'table',
                  encoding: {
                    columns: [
                      { field: 'outcome-title', type: 'nominal', title: 'Report', display: 'outcome-link' },
                      { field: 'outcome-status', type: 'nominal', title: 'Status', display: 'status' },
                      { field: 'rollout-mode', type: 'nominal', title: 'Mode', display: 'mode' },
                      { field: 'outcome-category', type: 'nominal', title: 'Type' },
                      { field: 'observed-at', type: 'temporal', title: 'Updated' }
                    ]
                  }
                }
              ]
            },
            {
              id: 'outcome-detail',
              kind: 'custom',
              title: 'Outcome',
              route: { 'hash-query-parameter': 'outcome' },
              views: [{
                id: 'outcome-record',
                title: 'Outcome',
                data: { sources: ['outcomes'] },
                mark: 'element',
                element: 'outcome-detail'
              }]
            }
          ]
        }
      };
      const sources = {
        workflows: {
          source: 'workflows',
          metadata,
          rows: [{
            organization: 'githubnext',
            repository: 'gh-cao',
            campaign: 'ambient-context',
            'campaign-name': 'Ambient Context',
            workflow: '.github/workflows/ambient-context.md',
            'workflow-name': 'Ambient Context',
            'workflow-role': 'orchestrator',
            'rollout-mode': 'review'
          }]
        },
        outcomes: {
          source: 'outcomes',
          metadata,
          rows: [{
            organization: 'customer',
            repository: 'target',
            'runtime-repository': 'githubnext/gh-cao',
            workflow: '.github/workflows/ambient-context.md',
            'workflow-name': 'Ambient Context',
            'safe-output': 'report-1',
            'outcome-title': 'Debug ambient context workflow failure',
            'outcome-summary': 'Investigated the reported workflow failure.',
            'outcome-category': 'pull-request',
            'outcome-status': 'closed',
            'rollout-mode': 'review',
            'observed-at': '2026-08-31T19:00:00Z'
          }]
        },
        runs: {
          source: 'runs',
          metadata,
          rows: [
            {
              organization: 'githubnext',
              repository: 'gh-cao',
              workflow: '.github/workflows/ambient-context.md',
              run: '102',
              'run-title': 'Scheduled review',
              event: 'schedule',
              'run-status': 'completed',
              'run-conclusion': 'success',
              'started-at': '2026-08-31T20:00:00Z',
              'run-link': {
                relation: 'run',
                href: 'https://github.com/githubnext/gh-cao/actions/runs/102',
                label: 'View run 102'
              }
            },
            {
              organization: 'githubnext',
              repository: 'gh-cao',
              workflow: '.github/workflows/ambient-context.md',
              run: '101',
              'run-title': 'Manual review',
              event: 'workflow_dispatch',
              'run-status': 'completed',
              'run-conclusion': 'failure',
              'started-at': '2026-08-31T19:00:00Z'
            }
          ]
        }
      };
      document.querySelector('#root').append(renderDashboard({ document: dashboardDocument, sources }));
    </script>
  `);
});

test('workflow runtime route renders JSON-declared workflow insights', async ({ page }) => {
  const presenterModuleUrl = buildPresenterModuleUrl();
  const runtimePage = authoritativeDashboard.dashboard.pages.find(
    (/** @type {{ id?: string }} */ candidate) => candidate.id === 'workflow-runtime'
  );
  assert(runtimePage, 'Missing workflow runtime page');
  await page.goto('http://dashboard.test/#page-workflow-runtime?workflow=githubnext%2Fgh-cao%3A.github%2Fworkflows%2Fmulti-device-docs-tester.md');
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(presenterModuleUrl)};
      import { prepareDashboardViewSources } from 'http://dashboard.test/test/e2e/helpers/dashboard-view-sources.js';
      const metadata = {
        'source-id': 'workflow-runtime-fixture',
        'source-kind': 'fixture',
        'as-of': '2026-08-31T19:00:00Z',
        'retrieved-at': '2026-08-31T19:01:00Z',
        'coverage-start': '2026-08-30T19:00:00Z',
        'coverage-end': '2026-08-31T19:00:00Z',
        completeness: 'complete',
        freshness: 'fresh',
        availability: 'available'
      };
      const workflow = '.github/workflows/multi-device-docs-tester.md';
      const dashboardDocument = {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'workflow-runtime-route',
          title: 'Workflow runtime route',
          repository: 'githubnext/gh-cao',
          queries: ${JSON.stringify(authoritativeDashboard.dashboard.queries)},
          pages: [${JSON.stringify(runtimePage)}]
        }
      };
      const sources = {
        workflows: {
          source: 'workflows',
          metadata,
          rows: [{
            organization: 'githubnext',
            repository: 'gh-cao',
            workflow,
            'workflow-name': 'Multi-Device Docs Tester',
            'workflow-role': 'standalone',
            campaign: 'testing',
            'campaign-name': 'Testing',
            'workflow-active': 'true',
            'rollout-mode': 'review',
            'workflow-link': {
              relation: 'workflow',
              href: 'https://github.com/githubnext/gh-cao/blob/HEAD/.github/workflows/multi-device-docs-tester.md',
              label: 'View authored workflow'
            }
          }]
        },
        runs: {
          source: 'runs',
          metadata,
          rows: [{
            organization: 'githubnext',
            repository: 'gh-cao',
            workflow,
            run: '45',
            'run-status': 'completed',
            'run-conclusion': 'success'
          }]
        },
        usage: {
          source: 'usage',
          metadata: { ...metadata, completeness: 'partial' },
          rows: [{
            organization: 'githubnext',
            repository: 'gh-cao',
            workflow,
            run: '45',
            aic: 962.7
          }]
        },
      };
      const routeParameters = {
        workflow: 'githubnext/gh-cao:.github/workflows/multi-device-docs-tester.md'
      };
      const viewSources = await prepareDashboardViewSources(
        dashboardDocument,
        'workflow-runtime',
        sources,
        { routeParameters }
      );
      document.querySelector('#root').append(renderDashboard({
        document: dashboardDocument,
        sources: viewSources
      }));
    </script>
  `);

  await expect(page.getByRole('heading', { name: 'Multi-Device Docs Tester', level: 1 })).toBeVisible();
  await expect(page.getByRole('navigation', { name: 'Multi-Device Docs Tester views' })).toContainText('InsightsReportsRuns');
  await expect(page.getByRole('navigation', { name: 'Multi-Device Docs Tester views' })
    .getByRole('link', { name: 'Value', exact: true })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Reports' })).toHaveAttribute('href', /#page-workflow-detail\?workflow=/);
  await expect(page.locator('.workflow-badges .workflow-badge')).toHaveText([
    'Standalone',
    'Campaign · Testing'
  ]);
  await expect(page.getByRole('link', { name: 'View authored workflow' })).toHaveAttribute(
    'href',
    'https://github.com/githubnext/gh-cao/blob/HEAD/.github/workflows/multi-device-docs-tester.md'
  );
  await expect(page.locator('.workflow-runtime-metrics')).toContainText('1');
  await expect(page.locator('.workflow-runtime-metrics')).toContainText('962.7');
});

test('outcome page template follows its JSON-declared hash query route in browser', async ({ page }) => {
  const presenterModuleUrl = buildPresenterModuleUrl();
  await page.goto('about:blank#page-outcome-detail?outcome=outcome-1');
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(presenterModuleUrl)};
      const metadata = {
        'source-id': 'outcomes-fixture',
        'source-kind': 'fixture',
        'as-of': '2026-08-31T08:00:00Z',
        'retrieved-at': '2026-08-31T08:01:00Z',
        completeness: 'complete',
        freshness: 'fresh',
        availability: 'available'
      };
      const dashboardDocument = {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'outcome-route',
          title: 'Outcome route',
          pages: [{
            id: 'outcome-detail',
            kind: 'custom',
            title: 'Outcome',
            description: 'Outcome details.',
            'filter-bar': true,
            route: { 'hash-query-parameter': 'outcome' },
            views: [{
              id: 'outcome-record',
              title: 'Outcome',
              data: { sources: ['outcomes'] },
              'title-link': { 'href-field': 'external-link', 'identifier-field': 'outcome-number' },
              mark: 'element',
              element: 'outcome-detail'
            }]
          }]
        }
      };
      const sources = {
        outcomes: {
          source: 'outcomes',
          metadata,
          rows: [{
            workflow: '.github/workflows/daily.md',
            'workflow-name': 'Daily review',
            'safe-output': 'outcome-1',
            'outcome-number': 403,
            'outcome-title': 'Parity verification sweep',
            'outcome-body-html': '<h2>Summary</h2><p>All checks passed.</p>',
            'outcome-category': 'pull-request',
            'outcome-status': 'closed',
            'outcome-state': 'lifecycle-close',
            'rollout-mode': 'live',
            'published-at': '2026-08-31T01:26:00Z',
            'observed-at': '2026-08-31T01:49:00Z',
            'external-link': {
              relation: 'external',
              href: 'https://github.com/githubnext/gh-cao/issues/403',
              label: 'View output'
            }
          }]
        }
      };
      document.querySelector('#root').append(renderDashboard({ document: dashboardDocument, sources }));
    </script>
  `);

  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Parity verification sweep');
  await expect(page.locator('[data-page-title-link]')).toHaveText('Open #403 on GitHub');
  await expect(page.locator('[data-page-title-link]')).toHaveAttribute('href', 'https://github.com/githubnext/gh-cao/issues/403');
  await expect(page.locator('.overview-header [data-page-description]')).toHaveText('Daily review · Pull Request · Closed');
  await page.locator('.horizon-toggle').click();
  await expect(page.getByRole('searchbox', { name: 'Current filters' })).toHaveValue('');
  await expect(page.locator('.outcome-detail')).toHaveAttribute('data-outcome', 'outcome-1');
  await expect(page.locator('.discussion-post')).toContainText('All checks passed.');
  await expect(page.locator('.outcome-meta')).toContainText('Live');
  await expect(page.locator('.discussion-post')).toHaveCount(1);
  await expect(page.locator('.outcome-meta')).toHaveCount(1);
});

test('declarative tables expose report-style facets and progressive catalog disclosure', async ({ page }) => {
  const presenterModuleUrl = buildPresenterModuleUrl();

  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(presenterModuleUrl)};

      const rows = Array.from({ length: 30 }, (_, index) => ({
        workflow: \`workflow-\${index + 1}\`,
        'rollout-mode': index % 2 === 0 ? 'review' : 'live'
      }));
      document.querySelector('#root').append(renderDashboard({
        document: {
          languageVersion: '0.1.0',
          dashboard: {
            id: 'catalog-dashboard',
            title: 'Catalog Dashboard',
            pages: [{
              id: 'catalog',
              kind: 'custom',
              title: 'Catalog',
              views: [{
                id: 'workflow-catalog',
                title: 'Workflow catalog',
                data: { source: 'workflows' },
                mark: 'table',
                encoding: {
                  columns: [
                    { field: 'workflow', type: 'nominal' },
                    { field: 'rollout-mode', type: 'nominal', title: 'Mode' }
                  ]
                }
              }]
            }]
          }
        },
        sources: {
          workflows: {
            source: 'workflows',
            rows,
            metadata: {
              'source-id': 'workflow-catalog-fixture',
              'source-kind': 'fixture',
              'as-of': '2026-08-30T20:00:00Z',
              'retrieved-at': '2026-08-30T20:01:00Z',
              completeness: 'complete',
              freshness: 'fresh',
              availability: 'available'
            }
          }
        }
      }));
    </script>
  `);

  const tableRows = page.locator('.custom-table tbody tr');
  const visibleRows = page.locator('.custom-table tbody tr:visible');
  await expect(tableRows).toHaveCount(30);
  await expect(visibleRows).toHaveCount(25);
  await expect(page.locator('.table-filter-result')).toHaveText('Showing 25 of 30 results');
  const tableLayout = await page.locator('.custom-table').evaluate((table) => {
    const scroll = table.closest('.table-scroll');
    const cells = table.querySelectorAll('thead tr:first-child th');
    return {
      tableWidth: table.getBoundingClientRect().width,
      scrollWidth: scroll?.getBoundingClientRect().width ?? 0,
      firstColumnWidth: cells[0]?.getBoundingClientRect().width,
      lastColumnWidth: cells[cells.length - 1]?.getBoundingClientRect().width,
      lastColumnAlignment: getComputedStyle(cells[cells.length - 1]).textAlign
    };
  });
  expect(tableLayout.tableWidth).toBeCloseTo(tableLayout.scrollWidth, 0);
  expect(tableLayout.lastColumnWidth).toBeGreaterThan(tableLayout.firstColumnWidth);
  expect(tableLayout.lastColumnAlignment).toBe('left');
  await expect(page.locator('thead th').filter({ has: page.locator('[data-table-facet="rollout-mode"]') })).toHaveCount(1);
  const modeFilter = page.getByRole('combobox', { name: 'Filter by Mode' });
  await expect(modeFilter).toHaveValue('');
  await expect(modeFilter).toHaveCSS('appearance', 'none');
  await expect(modeFilter.locator('..')).toHaveCSS('border-radius', '6px');

  await page.getByRole('button', { name: 'Show all rows' }).click();
  await expect(visibleRows).toHaveCount(30);
  await expect(page.locator('.table-region')).toHaveClass(/table-region-expanded/);
  await expect(page.locator('.table-scroll')).toHaveCSS('max-height', 'none');
  await expect(page.locator('.table-scroll')).toHaveCSS('overflow', 'visible');

  await page.locator('[data-table-facet="rollout-mode"]').selectOption('review');
  await expect(visibleRows).toHaveCount(15);
  await expect(page.locator('.table-filter-result')).toHaveText('Showing 15 of 15 results');
  await expect.poll(() => page.evaluate(() => location.hash)).toContain('workflow-catalog.rollout-mode=review');

  await page.getByRole('searchbox', { name: 'Filter Workflow catalog' }).fill('workflow-29');
  await expect(visibleRows).toHaveCount(1);
  await expect(visibleRows).toContainText('workflow-29');
  await expect(page.locator('.table-filter-result')).toHaveText('Showing 1 of 1 result');
  await expect.poll(() => page.evaluate(() => location.hash)).toContain('workflow-catalog.q=workflow-29');
});
