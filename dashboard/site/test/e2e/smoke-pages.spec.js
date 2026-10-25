import { assert, authoritativeDashboard, buildPresenterModuleUrl, expect, expectLayoutWithin, registerSmokeRoutes, test } from './helpers/smoke-fixtures.js';

registerSmokeRoutes();

test('Simulators exposes its database-size tab and bounded scenario inputs', async ({ page }) => {
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(buildPresenterModuleUrl())};
      document.querySelector('#root').append(renderDashboard({
        document: ${JSON.stringify(authoritativeDashboard)},
        sources: {}
      }));
      window.location.hash = '#page-simulators';
    </script>
  `);
  const simulator = page.locator('[data-page-id="simulators"]');
  await expect(simulator.getByRole('link', { name: 'Database size' })).toHaveAttribute('aria-current', 'page');
  await expect(simulator.getByRole('slider')).toHaveCount(5);
  await expect(simulator.getByRole('slider', { name: 'Repositories' })).toHaveAttribute('max', '100000');
  await expect(simulator.getByRole('slider', { name: 'Skipped runs (%)' })).toHaveAttribute('min', '1');
});

test('Simulators binds slider values to a recomputed 30-day table', async ({ page }) => {
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(buildPresenterModuleUrl())};
      import { loadCanonicalDashboardPage } from '/src/data-processor.js';
      const documentModel = ${JSON.stringify(authoritativeDashboard)};
      window.location.hash = '#page-simulators';
      document.querySelector('#root').append(renderDashboard({
        document: documentModel,
        sources: {},
        loadPageSources: (pageId, options) => pageId === 'simulators'
          ? loadCanonicalDashboardPage(
              ['simulator-database-summary'],
              documentModel.dashboard,
              undefined,
              { pageId, queryContext: options.queryContext }
            )
          : Promise.resolve({})
      }));
    </script>
  `);
  const simulator = page.locator('[data-page-id="simulators"]');
  const table = simulator.getByRole('table');
  await expect(table.getByRole('row', { name: 'Total 890880000' })).toBeVisible();
  await simulator.getByRole('slider', { name: 'Repositories' }).fill('2000');
  await expect(table.getByRole('row', { name: 'Total 1781760000' })).toBeVisible();
  await simulator.getByRole('slider', { name: 'Skipped runs (%)' }).fill('50');
  await expect(table.getByRole('row', { name: 'Total 1228800000' })).toBeVisible();
  await expect(table.getByRole('row', { name: 'Run summaries 307200000' })).toBeVisible();
});

test('mobile shell keeps Overview navigation in the hamburger menu', async ({ page }) => {
  const presenterModuleUrl = buildPresenterModuleUrl();
  const documentModel = authoritativeDashboard;
  await page.setViewportSize({ width: 390, height: 844 });
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(presenterModuleUrl)};
      const documentModel = ${JSON.stringify(documentModel)};
      document.querySelector('#root').append(renderDashboard({ document: documentModel, sources: {} }));
    </script>
  `);

  const primaryNav = page.locator('.primary-nav');
  const overviewAction = page.locator('[data-nav-page-id="overview"]');
  const dashboardMain = page.locator('main.dashboard-prototype');
  const factoryOverview = page.locator('[data-page-id="overview"] > .custom-view-grid');
  await expect(primaryNav).toBeHidden();
  await expect(overviewAction).toBeHidden();
  const viewportSize = page.viewportSize();
  expect(viewportSize).not.toBeNull();
  if (viewportSize === null) throw new Error('Expected Playwright to provide a viewport size');
  const viewportWidth = viewportSize.width;
  await expect(factoryOverview).toBeVisible();
  const layoutPixelTolerance = 1;
  const [mainBox, factoryBox] = await Promise.all([
    dashboardMain.boundingBox(),
    factoryOverview.boundingBox()
  ]);
  expect(mainBox).not.toBeNull();
  expect(factoryBox).not.toBeNull();
  if (mainBox === null || factoryBox === null) throw new Error('Expected overview layout boxes to be available');
  expectLayoutWithin(factoryBox.x, 0, layoutPixelTolerance);
  expectLayoutWithin(factoryBox.y, mainBox.y, layoutPixelTolerance);
  expectLayoutWithin(factoryBox.width, viewportWidth, layoutPixelTolerance);

  const headerCopy = factoryOverview.locator('.factory-intro-copy');
  const rhythm = factoryOverview.locator('.factory-rhythm');
  const stations = factoryOverview.locator('.factory-station');
  const [headerCopyBox, rhythmBox, firstStationBox, secondStationBox] = await Promise.all([
    headerCopy.boundingBox(),
    rhythm.boundingBox(),
    stations.nth(0).boundingBox(),
    stations.nth(1).boundingBox()
  ]);
  if (!headerCopyBox || !rhythmBox || !firstStationBox || !secondStationBox) {
    throw new Error('Expected responsive Overview component boxes to be available');
  }
  expect(rhythmBox.y).toBeGreaterThanOrEqual(headerCopyBox.y + headerCopyBox.height);
  expect(await factoryOverview.locator('.factory-stations').evaluate((element) =>
    getComputedStyle(element).gridTemplateColumns.split(' ').filter(Boolean).length
  )).toBe(2);
  expect(firstStationBox.x).toBeLessThan(secondStationBox.x);
  expectLayoutWithin(firstStationBox.y, secondStationBox.y, 8);
  await expect(factoryOverview.locator('.factory-rhythm-day')).toHaveCount(7);

  await page.locator('.mobile-nav-menu > summary').click();
  await page.locator('[data-mobile-nav-page-id="runs"]').click();

  await expect(primaryNav).toHaveCSS('display', 'none');
  await expect(page.locator('[data-mobile-nav-page-id="overview"]')).toHaveAttribute('href', '#page-overview');
});



test('Ingestion shows daily run counts, shard activity dates and retained transactions', async ({ page }) => {
  const documentModel = authoritativeDashboard;
  await page.setViewportSize({ width: 1200, height: 900 });
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(buildPresenterModuleUrl())};
      const rows = Array.from({ length: 100 }, (_, index) => ({
        id: \`transaction:\${index}\`,
        kind: index % 2 === 0 ? 'ingest-jsonl' : 'ingest-dashboard-sources',
        createdAt: new Date(Date.UTC(2026, 8, 12, 12, index)).toISOString(),
        payloadScope: \`https://dashboard.example/gh-aw-logs-shards/logs-\${index}.jsonl\`,
        payloadHash: \`sha256:\${index}\`,
        payloadEtag: \`etag-\${index}\`,
        records: 30 + index,
        committedRecords: 25 + index,
        rawPayloadRecords: 24 + index,
        rawRuns: 20 + index,
        agenticRunRecords: 10 + index,
        agenticRuns: 8 + index,
        duplicateRawRunObservations: index,
        duplicateAgenticRunObservations: index,
        unenrichedRuns: index,
        'activity-status': 'success',
        error: ''
      }));
      const metadata = {
        'source-id': 'transactions-fixture',
        'source-kind': 'fixture',
        'as-of': '2026-09-12T12:00:00Z',
        'retrieved-at': '2026-09-12T12:01:00Z',
        completeness: 'complete',
        freshness: 'fresh',
        availability: 'available'
      };
      const transactionCards = rows.map((row) => ({
        id: row.id,
        kind: row.kind,
        'created-at': row.createdAt,
        'committed-records': row.committedRecords,
        'payload-hash': row.payloadHash
      }));
      const sources = {
        transactions: { source: 'transactions', rows, metadata },
        'indexing-daily-run-counts': {
          source: 'indexing-daily-run-counts',
          rows: [
            { date: '2026-09-11', runs: 6050 },
            { date: '2026-09-12', runs: 6950 }
          ],
          metadata
        },
        'indexing-ingestion-runs': {
          source: 'indexing-ingestion-runs',
          rows: [
            { 'shard-day': '2026-09-11', 'workflow-runs': 6050, 'run-count-status': 'Reported run count' },
            { 'shard-day': '2026-09-12', 'workflow-runs': 6950, 'run-count-status': 'Reported run count' }
          ],
          metadata
        },
        'indexing-database-table-counts': {
          source: 'indexing-database-table-counts',
          rows: [
            { table: 'workflow runs', records: 100 },
            { table: 'ingestion transactions', records: 100 }
          ],
          metadata
        },
        'indexing-transactions': { source: 'indexing-transactions', rows: transactionCards, metadata },
        'configuration-policy': {
          source: 'configuration-policy',
          rows: [{ document: { version: 1 }, raw: '{"version":1}', diagnostics: [] }],
          metadata
        },
        'overview-repository-coverage': { source: 'overview-repository-coverage', rows: [{ 'repository-coverage': 0.5, 'reached-repositories': 3, 'registered-repositories-total': 6 }], metadata },
        'database-repository-count': { source: 'database-repository-count', rows: [{ repositories: 3 }], metadata },
        'database-workflow-count': { source: 'database-workflow-count', rows: [{ workflows: 5 }], metadata },
        'database-run-count': { source: 'database-run-count', rows: [{ runs: 8 }], metadata },
        'database-domain-count': { source: 'database-domain-count', rows: [{ domains: 7 }], metadata },
        'database-tool-count': { source: 'database-tool-count', rows: [{ tools: 11 }], metadata },
        'database-audit-count': { source: 'database-audit-count', rows: [{ audits: 13 }], metadata },
        'database-issue-count': { source: 'database-issue-count', rows: [{ issues: 17 }], metadata }
      };
      window.location.hash = '#page-overview';
      document.querySelector('#root').append(renderDashboard({ document: ${JSON.stringify(documentModel)}, sources }));
    </script>
  `);

  const maintenanceNavigation = page.locator('.nav-section').filter({
    has: page.locator('summary', { hasText: /^Maintenance$/ })
  });
  await maintenanceNavigation.getByRole('link', { name: 'Ingestion', exact: true }).click();

  const root = page.locator('.dashboard-root');
  const transactionsPage = page.locator('[data-page-id="indexing"]');
  const view = transactionsPage.locator('[data-view-id="transaction-entries"]');
  await expect(transactionsPage.locator('[data-view-id]')).toHaveCount(6);
  await expect(transactionsPage.getByRole('heading', { name: 'Local database' })).toHaveCount(0);
  const dailyRuns = transactionsPage.locator('[data-view-id="indexing-daily-run-counts"]');
  await dailyRuns.scrollIntoViewIfNeeded();
  await expect(transactionsPage.locator('[data-chart-widget="bar"]')).toHaveCount(0);
  await expect(dailyRuns.locator('[data-chart-widget="line"] .line-chart-point')).toHaveCount(2);
  await expect(transactionsPage.locator('[data-chart-widget="scatter"] .scatter-chart-point')).toHaveCount(2);
  await expect(transactionsPage.locator('[data-chart-widget="horizontal-bar"]')).toHaveCount(1);
  await expect(transactionsPage.locator('[data-view-id="ingestion-queue-sizes"]')).toHaveCount(0);
  await transactionsPage.getByRole('button', { name: 'Cards', exact: true }).click();
  await expect(view).toBeVisible();
  await expect(view.locator('.entity-card-list-card')).toHaveCount(100);
  await expect(view.locator('.entity-card-list-card').getByText('Committed records').first()).toBeVisible();
  await expect(view.locator('.entity-card-list-card').getByText('Payload hash').first()).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('.org-sidebar')).toBeVisible();
  await expect(transactionsPage.locator(':scope > .page-chrome > .filter-bar')).toBeHidden();
  await expect(view).toBeVisible();
  await expect(root).not.toHaveClass(/dashboard-full-view-scrolled/);
  await expect(page.locator('.org-sidebar')).toBeVisible();
  await expect(page.locator('.top-nav')).toBeHidden();
});

test('Runs renders a last-week stacked area graph above its responsive table and scrolls like Cost', async ({ page }) => {
  const documentModel = authoritativeDashboard;
  await page.setViewportSize({ width: 1200, height: 900 });
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(buildPresenterModuleUrl())};
      const documentModel = ${JSON.stringify(documentModel)};
      const metadata = {
        'source-id': 'runs-viewport-fixture',
        'source-kind': 'fixture',
        'as-of': '2026-09-10T12:00:00Z',
        'retrieved-at': '2026-09-10T12:01:00Z',
        completeness: 'complete',
        freshness: 'fresh',
        availability: 'available'
      };
      const sources = {
        'runs-daily-conclusions': {
          source: 'runs-daily-conclusions',
          metadata,
          rows: [
            { day: '2026-09-10', 'runtime-health': 'Failed', runs: 50 },
            { day: '2026-09-10', 'runtime-health': 'Successful', runs: 50 }
          ]
        },
        'runs-table': {
          source: 'runs-table',
          metadata,
          rows: [
            {
              run: '2',
              'run-status': 'completed',
              'run-conclusion': 'failure',
              organization: 'githubnext',
              repository: 'gh-cao',
              'repository-coordinate': 'githubnext/gh-cao',
              workflow: '.github/workflows/aw-doctor.md',
              'rollout-mode': 'review',
              engine: 'copilot',
              'engine-version': '1.2.3',
              'requested-model': 'gpt-5',
              'resolved-model': 'gpt-5',
              'started-at': '2026-09-10T12:00:00Z',
              'repository-link': { relation: 'repository', href: 'https://github.com/githubnext/gh-cao', label: 'Open githubnext/gh-cao' },
              'workflow-link': { relation: 'workflow', href: 'https://github.com/githubnext/gh-cao/blob/main/.github/workflows/aw-doctor.md', label: 'Open .github/workflows/aw-doctor.md' },
              'run-link': { relation: 'run', href: 'https://github.com/githubnext/gh-cao/actions/runs/2', label: 'Run 2' }
            },
            {
              run: '1',
              'run-status': 'completed',
              'run-conclusion': 'success',
              organization: 'githubnext',
              repository: 'gh-cao',
              'repository-coordinate': 'githubnext/gh-cao',
              workflow: '.github/workflows/aw-doctor.md',
              'rollout-mode': 'review',
              engine: 'copilot',
              'engine-version': '1.2.3',
              'requested-model': 'gpt-5',
              'resolved-model': 'gpt-5',
              'started-at': '2026-09-10T11:00:00Z',
              'repository-link': { relation: 'repository', href: 'https://github.com/githubnext/gh-cao', label: 'Open githubnext/gh-cao' },
              'workflow-link': { relation: 'workflow', href: 'https://github.com/githubnext/gh-cao/blob/main/.github/workflows/aw-doctor.md', label: 'Open .github/workflows/aw-doctor.md' },
              'run-link': { relation: 'run', href: 'https://github.com/githubnext/gh-cao/actions/runs/1', label: 'Run 1' }
            }
          ]
        }
      };
      for (let run = 3; run <= 100; run += 1) {
        const template = sources['runs-table'].rows[run % 2];
        sources['runs-table'].rows.push({
          ...template,
          run: String(run),
          'run-link': {
            relation: 'run',
            href: 'https://github.com/githubnext/gh-cao/actions/runs/' + run,
            label: 'Run ' + run
          }
        });
      }
      window.location.hash = '#page-runs';
      document.querySelector('#root').append(renderDashboard({ document: documentModel, sources }));
    </script>
  `);

  const dashboardRoot = page.locator('.dashboard-root');
  const runsPage = page.locator('[data-page-id="runs"]');
  const view = runsPage.locator('[data-view-layout="full-view"]');
  const table = view.locator('[data-lazy-list]');
  const scroll = view.locator('.table-scroll');
  const areaGraph = runsPage.locator('[data-view-id="runs-last-week"]');
  const columnHeaders = view.locator('thead > tr:first-child > th');
  const facetControl = columnHeaders.locator('.filter-select-control').first();
  const expectAlignedColumnHeaders = async () => {
    const headerTops = await columnHeaders.evaluateAll((headers) => headers.map((header) => header.getBoundingClientRect().top));
    expect(Math.max(...headerTops) - Math.min(...headerTops)).toBeLessThanOrEqual(1);
  };
  await expect(page.getByRole('heading', { name: 'Runs', level: 1 })).toBeVisible();
  await expect(page.locator('[data-nav-page-id="runs"]')).toHaveAttribute('aria-current', 'page');
  await expect(dashboardRoot).not.toHaveClass(/dashboard-full-view/);
  await expect(view).toHaveCount(1);
  await expect(areaGraph.locator('[data-chart-widget="area"]')).toBeVisible();
  await expect(areaGraph.locator('.area-chart-area')).toHaveCount(2);
  await expect(areaGraph.locator('.chart-legend-area')).toContainText('Failed');
  await expect(areaGraph.locator('.chart-legend-area')).toContainText('Successful');
  await expect(areaGraph.locator('.line-chart-y-labels span')).toHaveText(['100', '50', '0']);
  await expect(areaGraph.locator('[data-chart-widget="area"]')).toHaveAttribute('style', '--line-chart-left: 7%;');
  const areaGraphHeadingBox = await areaGraph.getByRole('heading', { name: 'Runs in the last week' }).boundingBox();
  const areaGraphChartBox = await areaGraph.locator('[data-chart-widget="area"] svg').boundingBox();
  const areaGraphTimelineBox = await areaGraph.locator('.timeline-chart-axis').boundingBox();
  if (areaGraphHeadingBox === null || areaGraphChartBox === null || areaGraphTimelineBox === null) {
    throw new Error('Expected area graph heading and chart boxes to be measurable.');
  }
  expect(areaGraphChartBox.width).toBeGreaterThan(0);
  expect(areaGraphChartBox.height).toBeGreaterThan(0);
  expect(areaGraphTimelineBox.x).toBeCloseTo(areaGraphChartBox.x + (areaGraphChartBox.width * 0.07), 0);
  for (const width of [390, 1600]) {
    await page.setViewportSize({ width, height: 900 });
    const rendering = await areaGraph.locator('.line-chart-plot').evaluate((plot) => {
      const svg = plot.querySelector('svg');
      const topLabel = plot.querySelector('.line-chart-y-labels span');
      const xLabel = plot.parentElement?.querySelector('.timeline-chart-axis span');
      const dot = plot.querySelector('.area-chart-point');
      const grid = plot.querySelector('.line-chart-grid');
      if (!svg || !topLabel || !xLabel || !dot || !grid) throw new Error('Expected stacked chart labels and marks');
      const svgBox = svg.getBoundingClientRect();
      const labelBox = topLabel.getBoundingClientRect();
      return {
        fontSize: getComputedStyle(topLabel).fontSize,
        xFontSize: getComputedStyle(xLabel).fontSize,
        dotWidth: getComputedStyle(dot).strokeWidth,
        dotVectorEffect: getComputedStyle(dot).vectorEffect,
        labelBottom: labelBox.top + labelBox.height / 2,
        gridY: svgBox.top + Number(grid.getAttribute('y1')) * svgBox.height / 42,
        labelRight: labelBox.right,
        axisX: svgBox.left + Number(grid.getAttribute('x1')) * svgBox.width / 100
      };
    });
    expect(rendering.fontSize).toBe(rendering.xFontSize);
    expect(rendering.dotWidth).toBe('5px');
    expect(rendering.dotVectorEffect).toBe('non-scaling-stroke');
    expect(rendering.labelBottom).toBeCloseTo(rendering.gridY, 0);
    expect(rendering.labelRight).toBeLessThan(rendering.axisX);
  }
  await page.getByRole('button', { name: 'Table', exact: true }).click();
  await expect(table).toBeVisible();
  await expect(table.locator('tbody > tr')).toHaveCount(25);
  const more = table.locator('[data-table-more]');
  for (let pageIndex = 0; pageIndex < 3; pageIndex += 1) {
    await more.evaluate((button) => /** @type {HTMLButtonElement} */ (button).click());
  }
  await expect(table.locator('tbody > tr')).toHaveCount(50);
  await expect(view.locator('[data-table-filter]')).toBeVisible();
  const summaryRow = view.locator('.table-summary-row');
  const summaryToggle = summaryRow.getByRole('button', { name: 'Collapse column summaries' });
  await expect(summaryRow).toBeVisible();
  const expandedHeight = await summaryRow.evaluate((element) => element.getBoundingClientRect().height);
  await summaryToggle.click();
  await expect(summaryRow).toHaveClass(/table-summary-collapsed/);
  await expect(summaryRow.locator('.table-summary-expanded').first()).toBeHidden();
  await expect(summaryRow.locator('.table-summary-compact:visible').first()).toBeVisible();
  expect(await summaryRow.evaluate((element) => element.getBoundingClientRect().height)).toBeLessThan(expandedHeight);
  await summaryRow.getByRole('button', { name: 'Expand column summaries' }).click();
  await expect(summaryRow).not.toHaveClass(/table-summary-collapsed/);
  await expect(view.locator('.custom-table tbody tr')).not.toHaveCount(0);
  await expect(view.locator('.custom-table tbody tr').first()).toContainText('2');
  await expectAlignedColumnHeaders();
  await scroll.evaluate((element) => {
    element.scrollTop = 100;
    element.dispatchEvent(new Event('scroll'));
  });
  await expect(dashboardRoot).toHaveClass(/dashboard-full-view-scrolled/);
  const facetControlBox = await facetControl.boundingBox();
  const scrolledSummaryBox = await summaryRow.boundingBox();
  assert(facetControlBox);
  assert(scrolledSummaryBox);
  expect(scrolledSummaryBox.y - (facetControlBox.y + facetControlBox.height)).toBeGreaterThanOrEqual(4);
  await page.getByRole('button', { name: 'Cards' }).click();
  await page.getByRole('button', { name: 'Chart' }).click();
  await expect(areaGraph).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  const mobileViewModeToggle = page.locator('.mobile-view-mode-toggle');
  await expect(runsPage.locator(':scope > .page-chrome > .filter-bar')).toHaveCount(0);
  await expect(areaGraph).toBeVisible();
  await expect(table).toBeHidden();
  await expect(mobileViewModeToggle).toHaveAttribute('aria-label', 'Switch to Cards view');
  await mobileViewModeToggle.click();
  await mobileViewModeToggle.click();
  await expect(areaGraph).toBeHidden();
  await expect(table).toBeVisible();
  await expect(dashboardRoot).toHaveClass(/dashboard-full-view/);
  await expectAlignedColumnHeaders();
  await expect.poll(async () => scroll.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(true);
  await expect.poll(async () => scroll.locator(':scope > .table-filter').evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
});

test('a page combining a chart with a full-view table fills and scrolls in table and card layouts', async ({ page }) => {
  await page.setViewportSize({ width: 1000, height: 900 });
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(buildPresenterModuleUrl())};
      const dashboardDocument = {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'models-scroll',
          title: 'Models & Agents',
          pages: [{
            id: 'engines-models',
            kind: 'custom',
            title: 'Models & Agents',
            views: [{
              id: 'engines-models-distribution',
              title: 'Agent and model distribution',
              data: { source: 'engines-models-chart' },
              mark: 'chart',
              chart: 'pie',
              layout: 'full',
              encoding: {
                x: { field: 'summary', type: 'nominal', title: 'Agent / model' },
                y: { field: 'events', type: 'quantitative', title: 'Events' }
              }
            }, {
              id: 'engines-models-usage',
              title: 'Engines and models',
              data: { source: 'engines-models-usage' },
              mark: 'table',
              controls: 'interactive',
              layout: 'full-view',
              encoding: {
                columns: [
                  { field: 'summary', type: 'nominal', title: 'Agent / model' },
                  { field: 'events', type: 'quantitative', title: 'Events' }
                ]
              },
              'lazy-list': true
            }]
          }],
          navigation: [{ label: 'Data', pages: ['engines-models'] }]
        }
      };
      const metadata = {
        'source-id': 'models-scroll-fixture',
        'source-kind': 'fixture',
        'as-of': '2026-09-15T12:00:00Z',
        'retrieved-at': '2026-09-15T12:01:00Z',
        completeness: 'complete',
        freshness: 'fresh',
        availability: 'available'
      };
      const chartRows = Array.from({ length: 6 }, (_, index) => ({
        summary: 'copilot / model-' + (index + 1),
        events: 6 - index
      }));
      const tableRows = Array.from({ length: 60 }, (_, index) => ({
        summary: 'copilot / model-' + (index + 1),
        events: 60 - index
      }));
      const sources = {
        'engines-models-chart': { source: 'engines-models-chart', rows: chartRows, metadata },
        'engines-models-usage': { source: 'engines-models-usage', rows: tableRows, metadata }
      };
      document.querySelector('#root').append(renderDashboard({
        document: dashboardDocument,
        sources
      }));
    </script>
  `);

  const dashboardRoot = page.locator('.dashboard-root');
  const chart = page.locator('[data-view-id="engines-models-distribution"]');
  const view = page.locator('[data-view-id="engines-models-usage"]');
  const scroll = view.locator('.table-scroll');
  const cards = view.locator('[data-mobile-card-list]');
  const cardScroll = cards.locator('.mobile-table-card-list-items');
  const chrome = page.locator('.dashboard-page:not([hidden]) > .page-chrome');
  const fullViewSpacing = () => chrome.evaluate((element) => {
    const selector = element.querySelector('.view-mode-control');
    const header = document.querySelector('.app-main > .top-nav');
    const content = element.parentElement?.querySelector('.custom-view-grid');
    if (!selector || !header || !content) throw new Error('Expected full-view toolbar and content.');
    return {
      selectorGap: selector.getBoundingClientRect().top - header.getBoundingClientRect().bottom,
      contentGap: content.getBoundingClientRect().top - element.getBoundingClientRect().bottom
    };
  });
  await expect(chart.locator('[data-chart-widget="pie"]')).toBeVisible();
  await expect(page.locator('.org-sidebar')).toBeVisible();
  await expect(scroll).toBeHidden();
  await expect(dashboardRoot).not.toHaveClass(/dashboard-full-view/);

  await page.getByRole('button', { name: 'Table' }).click();
  await expect(chart).toBeHidden();
  await expect(scroll).toBeVisible();
  await expect(dashboardRoot).toHaveClass(/dashboard-full-view/);
  await expect.poll(fullViewSpacing).toEqual({ selectorGap: 12, contentGap: 0 });
  await expect(page.locator('.org-sidebar')).toBeVisible();
  await expect.poll(async () => scroll.evaluate((element) => ({
    fillsView: Math.abs(innerHeight - element.getBoundingClientRect().bottom) <= 1,
    scrollable: element.scrollHeight > element.clientHeight
  }))).toEqual({ fillsView: true, scrollable: true });
  await scroll.evaluate((element) => { element.scrollTop = 100; });
  await expect.poll(async () => scroll.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);

  await page.getByRole('button', { name: 'Cards' }).click();
  await expect(chart).toBeHidden();
  await expect(scroll).toBeHidden();
  await expect(cards).toBeVisible();
  await expect.poll(fullViewSpacing).toEqual({ selectorGap: 12, contentGap: 0 });
  await expect(cards.locator('.entity-card-list-card').first()).toBeVisible();
  await expect(cards.locator('.entity-card-list-card').first()).toContainText('copilot / model-1');
  await expect(cards.getByRole('button', { name: 'Load more cards' })).toHaveCount(0);
  await expect(dashboardRoot).toHaveClass(/dashboard-full-view/);
  await expect(page.getByRole('heading', { name: 'Engines and models', level: 3 })).toBeHidden();
  await expect.poll(async () => cards.evaluate((element) =>
    Math.abs(innerHeight - element.getBoundingClientRect().bottom) <= 1
  )).toBe(true);
  await expect.poll(async () => cardScroll.evaluate((element) =>
    element.scrollHeight > element.clientHeight
  )).toBe(true);
  await cardScroll.evaluate((element) => { element.scrollTop = element.scrollHeight; });
  await expect.poll(async () => cardScroll.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);
  await expect(cards.locator('.entity-card-list-card')).toHaveCount(50);

  await page.getByRole('button', { name: 'Chart' }).click();
  await expect(chart).toBeVisible();
  await expect(scroll).toBeHidden();
  await expect(dashboardRoot).not.toHaveClass(/dashboard-full-view/);
});

test('Runs renders the worker-projected table', async ({ page }) => {
  const documentModel = authoritativeDashboard;
  await page.setViewportSize({ width: 1200, height: 900 });
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(buildPresenterModuleUrl())};
      import { prepareDashboardViewSources } from 'http://dashboard.test/test/e2e/helpers/dashboard-view-sources.js';
      const documentModel = ${JSON.stringify(documentModel)};
      const metadata = {
        'source-id': 'runs-empty-time-filter-fixture',
        'source-kind': 'fixture',
        'as-of': '2026-09-10T12:00:00Z',
        'retrieved-at': '2026-09-10T12:01:00Z',
        completeness: 'complete',
        freshness: 'fresh',
        availability: 'available'
      };
      const sources = {
        runs: {
          source: 'runs',
          metadata,
          rows: [
            {
              run: '2',
              'run-status': 'completed',
              'run-conclusion': 'failure',
              organization: 'githubnext',
              repository: 'gh-cao',
              workflow: '.github/workflows/aw-doctor.md',
              'rollout-mode': 'review',
              engine: 'copilot',
              'engine-version': '1.2.3',
              'requested-model': 'gpt-5',
              'resolved-model': 'gpt-5',
              'started-at': '2026-09-10T12:00:00Z',
              'run-link': { relation: 'run', href: 'https://github.com/githubnext/gh-cao/actions/runs/2', label: 'Run 2' }
            }
          ]
        }
      };
      window.location.hash = '#page-runs';
      const loadPageSources = (pageId, options) => prepareDashboardViewSources(
        documentModel,
        pageId,
        sources,
        { queryContext: options.queryContext, routeParameters: options.routeParameters }
      );
      const viewSources = await loadPageSources('runs', {});
      document.querySelector('#root').append(renderDashboard({
        document: documentModel,
        sources: viewSources,
        loadPageSources
      }));
    </script>
  `);

  const runsPage = page.locator('[data-page-id="runs"]');
  const view = runsPage.locator('[data-view-layout="full-view"]');
  const rows = view.locator('.custom-table tbody tr');

  await page.getByRole('button', { name: 'Table' }).click();
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText('2');
  await expect(rows.locator('a').first()).toBeVisible();
});

test('Issues switches between its top-repository chart, table, and cards', async ({ page }) => {
  const documentModel = authoritativeDashboard;
  await page.setViewportSize({ width: 1200, height: 900 });
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(buildPresenterModuleUrl())};
      import { prepareDashboardViewSources } from 'http://dashboard.test/test/e2e/helpers/dashboard-view-sources.js';
      const documentModel = ${JSON.stringify(documentModel)};
      const metadata = {
        'source-id': 'issues-page-fixture',
        'source-kind': 'fixture',
        'as-of': '2026-09-20T12:00:00Z',
        'retrieved-at': '2026-09-20T12:01:00Z',
        completeness: 'complete',
        freshness: 'fresh',
        availability: 'available'
      };
      const issue = (id, repository, summary, observedAt) => ({
        id,
        organization: 'githubnext',
        repository,
        workflow: '.github/workflows/worker.md',
        run: id,
        'run-attempt': 1,
        event: id,
        'event-type': 'safe_output.result',
        'event-summary': summary,
        'event-timestamp': observedAt,
        'safe-output-type': 'create_issue',
        'github-entity-type': 'issue',
        'is-pull-request': false,
        'correlation-id': 'https://github.com/githubnext/' + repository + '/issues/' + id
      });
      const issues = [
        issue('3', 'alpha', 'Alpha issue 2', '2026-09-20T12:00:00Z'),
        issue('2', 'beta', 'Beta issue', '2026-09-19T12:00:00Z'),
        issue('1', 'alpha', 'Alpha issue 1', '2026-09-18T12:00:00Z')
      ];
      const sources = {
        audits: { source: 'audits', metadata, rows: [] },
        issues: { source: 'issues', metadata, rows: issues },
        runs: {
          source: 'runs',
          metadata,
          rows: issues.map((row) => ({
            organization: row.organization,
            repository: row.repository,
            workflow: row.workflow,
            run: row.run,
            'run-attempt': row['run-attempt'],
            'run-link': {
              relation: 'run',
              href: 'https://github.com/githubnext/' + row.repository + '/actions/runs/' + row.run,
              label: 'Run ' + row.run
            }
          }))
        }
      };
      window.location.hash = '#page-issues';
      const loadPageSources = (pageId, options) => prepareDashboardViewSources(
        documentModel,
        pageId,
        sources,
        { queryContext: options.queryContext, routeParameters: options.routeParameters }
      );
      const viewSources = await loadPageSources('issues', {});
      document.querySelector('#root').append(renderDashboard({
        document: documentModel,
        sources: viewSources,
        loadPageSources
      }));
    </script>
  `);

  const issuesPage = page.locator('[data-page-id="issues"]');
  const chart = issuesPage.locator('[data-view-id="issues-by-repository"]');
  const table = issuesPage.locator('[data-view-id="issues-source"] .table-region');
  const cards = issuesPage.locator('[data-mobile-card-list]');

  await expect(chart.locator('[data-chart-widget="pie"]')).toBeVisible();
  await expect(chart).toContainText('githubnext/alpha');
  await expect(page.getByRole('button', { name: 'Chart' })).toHaveAttribute('aria-pressed', 'true');

  await page.getByRole('button', { name: 'Table' }).click();
  await expect(table.locator('tbody tr')).toHaveCount(3);
  await expect(table.locator('tbody tr').first()).toContainText('Alpha issue 2');

  await page.getByRole('button', { name: 'Cards' }).click();
  await expect(cards.locator('.entity-card-list-card')).toHaveCount(3);
  await expect(cards.locator('.entity-card-list-card').first()).toContainText('Alpha issue 2');
  await expect(cards.locator('.entity-card-list-card').first().locator('[data-card-drill="external"]')).toHaveAttribute(
    'href',
    'https://github.com/githubnext/alpha/issues/3'
  );
});





test('desktop navigation sections collapse and expand around the current view', async ({ page }) => {
  const presenterModuleUrl = buildPresenterModuleUrl();
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(presenterModuleUrl)};
      document.querySelector('#root').append(renderDashboard({
        document: {
          languageVersion: '0.1.0',
          dashboard: {
            id: 'section-navigation-dashboard',
            title: 'Section Navigation',
            pages: [
              { id: 'overview', kind: 'custom', title: 'Overview', icon: 'home', views: [] },
              { id: 'runs', kind: 'custom', title: 'Runs', icon: 'play', views: [] }
            ],
            navigation: [
              { label: 'Main', pages: ['overview'] },
              { label: 'Investigate', pages: ['runs'] }
            ]
          }
        },
        sources: {}
      }));
    </script>
  `);

  const mainSection = page.locator('.nav-section').filter({ hasText: 'Main' });
  const investigateSection = page.locator('.nav-section').filter({ hasText: 'Investigate' });
  await expect(mainSection).toHaveAttribute('open', '');
  await expect(investigateSection).toHaveAttribute('open', '');

  await expect(investigateSection.getByRole('link', { name: 'Runs' })).toBeVisible();
  await investigateSection.getByRole('link', { name: 'Runs' }).click();
  await expect(investigateSection).toHaveAttribute('open', '');
  await expect(page.getByRole('heading', { name: 'Runs', level: 1 })).toBeVisible();
});

test('clean navigation preserves the Overview decision hierarchy across desktop and mobile', async ({ page }) => {
  const presenterModuleUrl = buildPresenterModuleUrl();
  const documentModel = authoritativeDashboard;
  await page.route('http://dashboard.test/memory/manifest.json', (route) => route.fulfill({
    contentType: 'application/json',
    body: JSON.stringify({
      version: 1,
      campaigns: [{
        campaign: 'aw-doctor',
        branch: 'memory/aw-doctor',
        commit: 'a'.repeat(40),
        files: [{ path: 'notes.md', oid: 'b'.repeat(40), size: 5 }],
        omitted: {},
      }],
    }),
  }));
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(presenterModuleUrl)};
      import { prepareDashboardViewSources } from 'http://dashboard.test/test/e2e/helpers/dashboard-view-sources.js';
      import { dashboardViewAliasName } from 'http://dashboard.test/src/data/queries/view-payload-compiler.js';
      import { configureSourceLoader } from 'http://dashboard.test/src/source-store.js';
      const documentModel = ${JSON.stringify(documentModel)};
      const metadata = {
        'source-id': 'dashboard-next-fixture',
        'source-kind': 'fixture',
        'as-of': '2026-08-29T10:00:00Z',
        'retrieved-at': '2026-08-29T10:01:00Z',
        completeness: 'complete',
        freshness: 'fresh',
        availability: 'available'
      };
      const evidenceLink = {
        relation: 'evidence',
        href: 'https://example.com/evidence/release-train',
        label: 'Review dependency evidence'
      };
      const sources = {
        campaigns: {
          source: 'campaigns',
          rows: [
            { id: 'campaign-1', campaign: 'aw-doctor', 'campaign-name': 'AW Doctor', 'campaign-icon': 'tools' },
            { id: 'campaign-2', campaign: 'dependabot', 'campaign-name': 'Dependabot', 'campaign-icon': 'dependabot' }
          ],
          metadata
        },
        issues: {
          source: 'issues',
          rows: Array.from({ length: 17 }, (_, index) => ({ id: \`issue-\${index + 1}\` })),
          metadata
        },
        'configuration-policy': {
          source: 'configuration-policy',
          rows: [{ document: { version: 1 }, raw: '', diagnostics: [] }],
          metadata
        },
        'overview-repository-coverage': { source: 'overview-repository-coverage', rows: [{ 'repository-coverage': 0.5, 'reached-repositories': 3, 'registered-repositories-total': 6 }], metadata },
        'database-repository-count': { source: 'database-repository-count', rows: [{ repositories: 3 }], metadata },
        'database-workflow-count': { source: 'database-workflow-count', rows: [{ workflows: 5 }], metadata },
        'database-run-count': { source: 'database-run-count', rows: [{ runs: 8 }], metadata },
        'database-domain-count': { source: 'database-domain-count', rows: [{ domains: 7 }], metadata },
        'database-tool-count': { source: 'database-tool-count', rows: [{ tools: 11 }], metadata },
        'database-audit-count': { source: 'database-audit-count', rows: [{ audits: 13 }], metadata },
        'database-issue-count': { source: 'database-issue-count', rows: [{ issues: 17 }], metadata },
        'overview-failed-run-count': {
          source: 'overview-failed-run-count',
          rows: [{ count: 80 }],
          metadata
        },
        'overview-blocked-work-count': {
          source: 'overview-blocked-work-count',
          rows: [],
          metadata: { ...metadata, availability: 'unavailable' }
        },
        'overview-awaiting-review-count': {
          source: 'overview-awaiting-review-count',
          rows: [],
          metadata: { ...metadata, availability: 'unavailable' }
        },
        'overview-security-finding-count': {
          source: 'overview-security-finding-count',
          rows: [],
          metadata: { ...metadata, availability: 'unavailable' }
        },
        'work-items': {
          source: 'work-items',
          rows: [
            {
              'work-item-id': 'dependabot:github/gh-aw:release-train',
              objective: 'Update the Dependabot release train',
              scope: 'github/gh-aw',
              'lifecycle-state': 'review',
              phase: 'verifying',
              reason: 'Security review remains pending.',
              'next-action': 'Review the dependency update evidence',
              'waiting-on': 'security-reviewers',
              owner: 'dependency-automation',
              'started-at': '2026-08-29T08:00:00Z',
              'ended-at': '2026-08-29T12:00:00Z',
              'evidence-link': evidenceLink
            },
            {
              'work-item-id': 'aw-doctor:github/gh-aw:inventory',
              objective: 'Inspect workflow inventory',
              scope: 'github/gh-aw',
              'lifecycle-state': 'unknown',
              phase: 'unknown',
              reason: 'Run telemetry is incomplete.',
              'next-action': 'Collect run telemetry',
              owner: 'aw-doctor',
              'started-at': '2026-08-01T08:00:00Z'
            }
          ],
          metadata
        },
        'attention-signals': {
          source: 'attention-signals',
          rows: [
            {
              'attention-signal-id': 'verification:dependabot:74',
              'signal-type': 'verification-review',
              objective: 'Update the Dependabot release train',
              scope: 'github/gh-aw',
              reason: 'Security verification requires human review.',
              action: 'Review dependency evidence',
              'expected-actor': 'security-reviewers',
              'age-seconds': 4800,
              'consequence-tier': 'high',
              priority: 2,
              'evidence-link': evidenceLink
            },
            {
              'attention-signal-id': 'authority:mona-tools:upgrade',
              'signal-type': 'authority-gate',
              objective: 'Upgrade agentic workflow dependencies',
              scope: 'github/mona-tools',
              reason: 'Live target authority is unavailable.',
              action: 'Confirm authority or retain review mode',
              'expected-actor': 'repository-owner',
              'age-seconds': 9000,
              'consequence-tier': 'medium',
              priority: 1,
              'evidence-link': evidenceLink
            }
          ],
          metadata
        },
        'agent-assignments': {
          source: 'agent-assignments',
          rows: [{
            'assignment-id': 'assignment:update-planner:74',
            'agent-name': 'Dependabot update planner',
            'agent-state': 'waiting',
            objective: 'Update the Dependabot release train',
            'handoff-state': 'waiting-for-review',
            'dependency-state': 'clear',
            'conflict-state': 'none',
            'evidence-link': evidenceLink
          }],
          metadata
        },
        'evidence-records': {
          source: 'evidence-records',
          rows: [{
            'evidence-id': 'evidence:dependabot:74:checks',
            objective: 'Update the Dependabot release train',
            'evidence-kind': 'verification',
            'evidence-class': 'observed',
            claim: 'Three required validations passed.',
            'verification-state': 'pending',
            'provenance-state': 'complete',
            'observed-at': '2026-08-29T09:55:00Z',
            'evidence-link': evidenceLink
          }],
          metadata
        },
        outcomes: {
          source: 'outcomes',
          rows: [
            {
              'safe-output': 'dependabot-review',
              'safe-output-kind': 'create-issue',
              'outcome-category': 'issue',
              'outcome-title': 'Dependabot review retained',
              repository: 'gh-aw',
              'outcome-state': 'accepted',
              'observed-at': '2026-08-29T09:40:00Z',
              'external-link': evidenceLink
            },
            {
              'safe-output': 'dependabot-update',
              'safe-output-kind': 'create-pull-request',
              'outcome-category': 'pull-request',
              'outcome-title': 'Dependabot update retained',
              repository: 'gh-aw',
              'outcome-state': 'accepted',
              'observed-at': '2026-08-29T09:45:00Z',
              'external-link': evidenceLink
            }
          ],
          metadata
        },
        runs: {
          source: 'runs',
          rows: [
            ...Array.from({ length: 80 }, (_, index) => ({
              repository: 'gh-aw', workflow: 'review', run: String(index + 1),
              'run-status': 'completed', 'run-conclusion': 'failure', 'started-at': '2026-08-29T09:30:00Z'
            })),
            ...Array.from({ length: 20 }, (_, index) => ({
              repository: 'gh-aw', workflow: 'update', run: String(index + 81),
              event: index < 12 ? 'workflow_dispatch' : 'schedule',
              'run-status': 'completed', 'run-conclusion': 'success', 'started-at': '2026-08-29T09:35:00Z'
            })),
            {
              repository: 'gh-aw', workflow: 'dispatch', run: '101',
              'run-status': 'in-progress', 'run-conclusion': 'unknown', 'started-at': '2026-08-29T09:50:00Z'
            }
          ],
          metadata
        },
        repositories: {
          source: 'repositories',
          rows: [{ id: 'repository:github/gh-aw', repository: 'gh-aw', 'rollout-mode': 'review', 'observed-at': '2026-08-29T09:30:00Z' }],
          metadata
        },
        workflows: {
          source: 'workflows',
          rows: [
            { repository: 'gh-aw', workflow: 'dispatch', 'workflow-role': 'orchestrator', 'workflow-active': 'true', 'observed-at': '2026-08-29T09:30:00Z' },
            { repository: 'gh-aw', workflow: 'review', 'workflow-role': 'worker', 'workflow-active': 'true', 'observed-at': '2026-08-29T09:30:00Z' },
            { repository: 'gh-aw', workflow: 'update', 'workflow-role': 'worker', 'workflow-active': 'true', 'observed-at': '2026-08-29T09:30:00Z' }
          ],
          metadata
        },
        'safe-output-performance': {
          source: 'safe-output-performance',
          rows: [{ 'safe-output-count': 1, 'observed-at': '2026-08-29T09:40:00Z' }],
          metadata
        },
        usage: {
          source: 'usage',
          rows: [
            {
              repository: 'gh-aw',
              aic: 8,
              'observed-at': '2026-08-01T09:50:00Z'
            },
            {
              repository: 'gh-aw',
              aic: 12,
              'observed-at': '2026-08-29T09:50:00Z'
            }
          ],
          metadata
        },
        'github-api-rate-limits': {
          source: 'github-api-rate-limits',
          rows: [{
            credential: 'control-plane',
            resource: 'core',
            remaining: 4200,
            'remaining-percent': 84,
            'reset-at': '2026-08-29T11:00:00Z',
            'risk-status': 'healthy'
          }],
          metadata
        }
      };
      sources['overview-runs'] = {
        source: 'overview-runs',
        rows: sources.runs.rows.map((run) => ({
          ...run,
          ...sources.workflows.rows.find((workflow) => (
            workflow.repository === run.repository && workflow.workflow === run.workflow
          ))
        })),
        metadata
      };
      const loadPageSources = (pageId, options) => prepareDashboardViewSources(
        documentModel,
        pageId,
        sources,
        { queryContext: options.queryContext, routeParameters: options.routeParameters }
      );
      configureSourceLoader(async (name, options) => {
        const prepared = await prepareDashboardViewSources(
          documentModel,
          options?.pageId ?? 'overview',
          sources,
          { queryContext: options?.queryContext }
        );
        const alias = options?.pageId && options.viewId
          ? dashboardViewAliasName(
              options.pageId,
              { id: options.viewId },
              0,
              name,
              options.sourceIndex ?? 0
            )
          : name;
        return prepared[alias] ?? prepared[name];
      });
      document.querySelector('#root').append(renderDashboard({
        document: documentModel,
        sources,
        loadPageSources
      }));
    </script>
  `);

  const cleanNavigation = page.locator('.primary-nav > [data-nav-page-id]');
  const data = page.locator('.nav-section').filter({ hasText: 'Data' });
  const evolutionSection = page.locator('.nav-section').filter({
    has: page.locator('summary', { hasText: /^Evolution$/ })
  });
  const maintenanceSection = page.locator('.nav-section').filter({
    has: page.locator('summary', { hasText: /^Maintenance$/ })
  });
  await expect(cleanNavigation).toHaveText(['Overview', 'Marketplace']);
  await expect(data.locator('summary')).toHaveText('Data');
  await data.locator('summary').click();
  await expect(data.getByRole('link')).toHaveText(['Repositories', 'Workflows', 'Campaigns', 'Runs', 'Issues', 'Cost', 'Value', 'Friction', 'Models & Agents', 'Firewall', 'MCPs', 'Memory']);
  await expect(data.getByRole('link', { name: 'Value', exact: true })).toHaveCount(1);
  await expect(data.getByRole('link', { name: 'Friction', exact: true })).toHaveCount(1);
  await expect(evolutionSection.locator('summary')).toHaveText('Evolution');
  await evolutionSection.locator('summary').click();
  await expect(evolutionSection.getByRole('link')).toHaveText(['Experiments', 'Evals', 'Graders']);
  for (const label of ['Experiments', 'Evals', 'Graders']) {
    await expect(evolutionSection.getByRole('link', { name: `${label}, Experimental` })).toHaveCount(1);
  }
  await data.getByRole('link', { name: /Memory/ }).click();
  await expect(page).toHaveURL(/#page-memory$/);
  await expect(page.getByRole('heading', { name: 'Memory', exact: true, level: 1 })).toBeVisible();
  const memoryView = page.getByRole('region', { name: 'CAO repository memory' });
  const disabledCampaigns = memoryView.locator('.cao-memory-campaign-disabled');
  await expect(disabledCampaigns).toHaveText(['Dependabot']);
  await expect(disabledCampaigns.first()).toHaveAttribute('aria-disabled', 'true');
  const campaignBranches = memoryView.locator('.cao-memory-campaign-branch');
  await expect(campaignBranches.locator(':scope > summary')).toHaveText(['AW Doctor']);
  await expect(campaignBranches.first()).not.toHaveAttribute('open', '');
  await campaignBranches.first().locator(':scope > summary').click();
  await expect(campaignBranches.first()).toHaveAttribute('open', '');
  await expect(page).toHaveURL(/#page-memory$/);
  await expect(memoryView.getByRole('link')).toHaveCount(0);
  await expect(maintenanceSection.locator('summary')).toHaveText('Maintenance');
  await expect(maintenanceSection.getByRole('link')).toHaveText(['Updates', 'Ingestion', 'Settings']);
  await expect(maintenanceSection).toHaveClass(/nav-section-bottom/);
  await expect.poll(async () => {
    const [navBox, manageBox] = await Promise.all([
      page.locator('.primary-nav').boundingBox(),
      maintenanceSection.boundingBox()
    ]);
    return navBox !== null && manageBox !== null
      ? Math.round(navBox.y + navBox.height - (manageBox.y + manageBox.height))
      : null;
  }).toBeLessThanOrEqual(1);
  await page.getByRole('button', { name: 'Collapse navigation' }).click();
  await expect(page.locator('.app-shell')).toHaveClass(/sidebar-collapsed/);
  await expect(maintenanceSection).toBeVisible();
  await expect(maintenanceSection).toHaveAttribute('aria-label', 'Maintenance');
  expect(await maintenanceSection.evaluate((element) => getComputedStyle(element, '::before').content))
    .toBe('"Maintenance"');
  await maintenanceSection.getByRole('link', { name: 'Settings' }).click();
  await expect(page).toHaveURL(/#page-configuration$/);
  await expect(page.getByRole('heading', { name: 'Settings', exact: true, level: 1 })).toBeVisible();
  await page.getByRole('button', { name: 'Expand navigation' }).click();
  await expect(page.locator('.app-shell')).not.toHaveClass(/sidebar-collapsed/);
  const experimentalSection = page.locator('.nav-section').filter({
    has: page.locator('summary', { hasText: /^Experimental$/ })
  });
  await expect(experimentalSection).not.toHaveAttribute('open', '');
  await experimentalSection.locator('summary').click();
  await expect(experimentalSection.getByRole('link')).toHaveText([
    'Workflow wizard',
    'Model routing',
    'Skills',
    'Steering',
    'Simulators'
  ]);
  await expect(cleanNavigation.first().locator('.octicon-home')).toBeVisible();
  await expect(page.locator('.account-menu')).toHaveCount(0);
  await expect(page.locator('.refresh-button')).toHaveCount(0);
  await expect(page.locator('.top-nav [data-theme-value]')).toHaveCount(0);
  await page.getByRole('link', { name: 'Settings' }).click();
  await expect(page).toHaveURL(/#page-configuration$/);
  await expect(page.getByRole('heading', { name: 'Settings', exact: true, level: 1 })).toBeVisible();
  const themeControl = page.locator('[data-page-id="configuration"] .theme-control-options');
  await expect(themeControl).toBeVisible();
  await expect(themeControl).toHaveAttribute('aria-label', 'Theme');
  await expect(page.getByRole('heading', { name: 'Appearance' })).toBeVisible();
  await expect(themeControl.getByRole('button', { name: 'System' })).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('.dashboard-root')).not.toHaveAttribute('data-theme');
  await themeControl.getByRole('button', { name: 'Light' }).click();
  await expect(page.locator('.dashboard-root')).toHaveAttribute('data-theme', 'light');
  expect(await page.evaluate(() => localStorage.getItem('central-agentic-ops.dashboard.theme'))).toBe('light');
  await page.getByRole('link', { name: 'Overview', exact: true }).first().click();
  await expect(page).toHaveURL(/#page-overview$/);
  await page.getByRole('link', { name: 'Settings' }).click();
  await expect(page).toHaveURL(/#page-configuration$/);
  await expect(page.getByRole('heading', { name: 'Settings', exact: true, level: 1 })).toBeVisible();
  await expect(themeControl.getByRole('button', { name: 'Light' })).toHaveAttribute('aria-pressed', 'true');
  await themeControl.getByRole('button', { name: 'System' }).click();
  await expect(page.locator('.dashboard-root')).not.toHaveAttribute('data-theme');
  await page.getByRole('button', { name: 'Reset local data' }).click();
  const resetDialog = page.getByRole('dialog', { name: 'Reset dashboard confirmation' });
  await expect(resetDialog).toBeVisible();
  await expect(resetDialog).toContainText('This action cannot be undone.');
  await resetDialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(resetDialog).not.toBeVisible();
  const description = page.locator('.overview-header .lede');
  expect(await description.evaluate((element) => element.scrollHeight <= element.clientHeight)).toBe(true);
  await expect(page.getByText('Dashboard Next', { exact: true })).toHaveCount(0);
  await expect(page.getByLabel('Show experimental')).toHaveCount(0);

  await cleanNavigation.filter({ hasText: 'Overview' }).click();
  const overviewPage = page.locator('[data-page-id="overview"]');
  await expect(overviewPage.locator(':scope > .custom-view-grid')).toBeVisible();
  await expect(overviewPage.getByRole('heading', { name: 'Your factory is humming.' })).toBeVisible();
  await expect(overviewPage.locator('.factory-running')).toHaveCount(0);
  await expect(overviewPage.locator('.factory-station')).toHaveCount(4);
  await expect(overviewPage.locator('.factory-station strong')).toHaveText(['100%', '0%', '17', 'Unavailable']);
  await expect(overviewPage.locator('.factory-station small')).toHaveText(['3/3 healthy workflows', '0/1 repositories reached', 'Retained safe outputs', '']);
  await expect(overviewPage.locator('.factory-output')).toHaveCount(0);
  await expect(overviewPage.locator('.factory-status')).toHaveCount(0);
  await expect(overviewPage.locator('.notifications-inbox')).toHaveCount(0);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  const factoryLinks = overviewPage.locator('.factory-station a');
  expect(await factoryLinks.count()).toBeGreaterThan(0);
  for (const link of await factoryLinks.evaluateAll((links) => links.map((element) => {
    const { width, height } = element.getBoundingClientRect();
    return { text: element.textContent?.trim(), width, height };
  }))) {
    expect(link.width, `${link.text} link width`).toBeGreaterThanOrEqual(24);
    expect(link.height, `${link.text} link height`).toBeGreaterThanOrEqual(24);
  }
  await page.evaluate(() => { window.location.hash = '#page-overview-failed-runs'; });
  const failedRunsPage = page.locator('[data-page-id="overview-failed-runs"]');
  await expect(failedRunsPage).toBeVisible();
  await expect(page.locator('.dashboard-root')).toHaveClass(/dashboard-full-view/);
  await expect(failedRunsPage.locator('[data-view-layout="full-view"]')).toHaveCount(1);
  expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBe(await page.evaluate(() => innerHeight));
  const fullViewShellSize = await page.locator('.top-nav > .shell').evaluate((element) => {
    const { width, height } = element.getBoundingClientRect();
    return { width, height };
  });
  await page.getByRole('navigation', { name: 'Breadcrumb' }).getByRole('link', { name: 'Overview' }).click();
  await expect(overviewPage).toBeVisible();

  for (const pageId of ['overview-blocked-work', 'overview-security-findings']) {
    await page.evaluate((nextPageId) => { window.location.hash = `#page-${nextPageId}`; }, pageId);
    const attentionPage = page.locator(`[data-page-id="${pageId}"]`);
    await expect(attentionPage).toBeVisible();
    await expect(page.locator('.dashboard-root')).toHaveClass(/dashboard-full-view/);
    await expect(attentionPage.locator('[data-view-layout="full-view"]')).toHaveCount(1);
  }
  await page.evaluate(() => { window.location.hash = '#page-overview'; });
  await expect(overviewPage).toBeVisible();

  await page.evaluate(() => { window.location.hash = '#page-overview-awaiting-review'; });
  const reviewPage = page.locator('[data-page-id="overview-awaiting-review"]');
  await expect(reviewPage).toBeVisible();
  await expect(page.locator('.dashboard-root')).toHaveClass(/dashboard-full-view/);
  await expect(reviewPage.locator('[data-view-layout="full-view"]')).toHaveCount(1);
  expect(await page.locator('.top-nav > .shell').evaluate((element) => {
    const { width, height } = element.getBoundingClientRect();
    return { width, height };
  })).toEqual(fullViewShellSize);
  await expect(reviewPage.locator('.custom-view')).toHaveCount(1);
  await expect(reviewPage.locator('.table-summary-row .table-summary-cell')).toHaveCount(3);
  await expect(reviewPage.locator('tbody tr')).toHaveCount(1);
  await expect(reviewPage.locator('tbody a[href="https://example.com/evidence/release-train"]')).toBeVisible();
  await expect(page.locator('[data-breadcrumb-dashboard]')).toHaveText('Overview');
  await cleanNavigation.filter({ hasText: 'Overview' }).click();
  await expect(overviewPage).toBeVisible();

  // A 320px window can leave 305px of layout width when the browser reserves a scrollbar gutter.
  await page.setViewportSize({ width: 305, height: 844 });
  await page.evaluate(() => { window.location.hash = '#page-overview'; });
  await expect(overviewPage).toBeVisible();
  await expect(overviewPage.locator(':scope > .custom-view-grid')).toBeVisible();
  await expect(overviewPage.locator('.factory-station')).toHaveCount(4);
  await page.locator('.mobile-nav-menu > summary').click();
  await expect(page.locator('.mobile-nav-section-label')).toHaveText(['Data', 'Evolution', 'Experimental', 'Maintenance']);
  const valueLink = page.locator('[data-mobile-nav-page-id="value"]');
  await expect(valueLink).toHaveAttribute('aria-label', 'Value');
  await expect(valueLink.locator('xpath=preceding-sibling::span[contains(@class, "mobile-nav-section-label")][1]')).toHaveText('Data');
  await expect(page.locator('[data-mobile-nav-page-id="operations"]')).toHaveCount(0);
  await page.locator('.mobile-nav-menu > summary').click();
  await expect(overviewPage.locator('.factory-intro')).toBeInViewport();
  await expect(overviewPage.locator('.custom-view')).toHaveCount(2);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  await expect(overviewPage.locator('.table-scroll')).toHaveCount(0);

  for (const pageName of ['overview']) {
    await page.evaluate((nextPage) => { window.location.hash = `#page-${nextPage}`; }, pageName);
    const activePage = page.locator(`[data-page-id="${pageName}"]`);
    await expect(activePage).toBeVisible();
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
    for (const chart of await activePage.locator('[data-chart-widget]').all()) {
      const box = await chart.boundingBox();
      expect(box?.width).toBeGreaterThan(0);
      expect(box?.height).toBeGreaterThan(0);
    }
  }
});




test('histogram keeps a low constant DOM size for 100,000 observations', async ({ page }) => {
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderChartWidget } from ${JSON.stringify('http://dashboard.test/src/components/chart-elements.js')};
      const points = Array.from({ length: 100_000 }, (_, index) => ({
        x: String(index),
        y: index,
        color: null
      }));
      document.querySelector('#root').append(renderChartWidget('histogram', points, []));
    </script>
  `);

  const histogram = page.locator('[data-chart-widget="histogram"]');
  await expect(histogram).toBeVisible();
  await expect(histogram.locator('.histogram-chart-bar')).toHaveCount(18);
  expect(await histogram.locator('*').count()).toBeLessThan(150);
});

test('DLS-DOC-014 horizon details are available in the expanded window picker', async ({ page }) => {
  const presenterModuleUrl = buildPresenterModuleUrl();
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(presenterModuleUrl)};
      const documentModel = {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'horizon-dashboard',
          title: 'Horizon dashboard',
          horizon: {
            label: 'Horizon',
            tooltip: {
              label: 'Horizon details',
              description: 'Data is included from the start up to the exclusive end.',
              icon: 'question'
            }
          },
          defaults: { time: { range: '1w' } },
          pages: [{ id: 'runs', kind: 'built-in', page: 'runs', title: 'Runs' }]
        }
      };
      const sources = {
        runs: {
          source: 'runs',
          rows: [{ run: '1', 'observed-at': '2026-09-01T11:00:00Z' }],
          metadata: {
            'source-id': 'runs-fixture',
            'source-kind': 'fixture',
            'retrieved-at': '2026-09-01T12:00:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      };
      document.querySelector('#root').append(renderDashboard({ document: documentModel, sources }));
    </script>
  `);

  const trigger = page.getByRole('button', { name: /Horizon 1 week/ });
  const details = page.getByRole('group', { name: 'Horizon details' });
  await expect(page.getByRole('button', { name: 'Horizon details', exact: true })).toHaveCount(0);
  await expect(details).toBeHidden();
  await trigger.click();
  await expect(details).toBeVisible();
  await expect(details).toContainText('StartAug 25, 2026, 12:00 PM UTC');
  await expect(details).toContainText('EndSep 1, 2026, 12:00 PM UTC');
  await expect(details).toContainText('Duration1 week');

  await page.setViewportSize({ width: 393, height: 852 });
  await page.locator('.mobile-nav-menu > summary').click();
  await expect(details).toBeHidden();
  await trigger.click();
  await expect(details).toBeVisible();
  await expect(page.locator('.report-footer .refresh-button')).toHaveCount(0);
  const actionBoxes = await page.locator('.report-actions > *').evaluateAll((items) => items.map((item) => {
    const bounds = item.getBoundingClientRect();
    return { top: Math.round(bounds.top), left: bounds.left, right: bounds.right };
  }));
  const actionTops = new Set(actionBoxes.map((box) => box.top));
  expect(actionTops.size).toBe(actionBoxes.length);
  for (const box of actionBoxes) {
    expect(box.left).toBeGreaterThanOrEqual(0);
    expect(box.right).toBeLessThanOrEqual(393);
  }
  const detailsBox = await details.boundingBox();
  expect(detailsBox).not.toBeNull();
  expect(detailsBox?.x).toBeGreaterThanOrEqual(0);
  expect((detailsBox?.x ?? 0) + (detailsBox?.width ?? 0)).toBeLessThanOrEqual(393);
});
