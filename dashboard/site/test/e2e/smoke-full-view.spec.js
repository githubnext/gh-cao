import { selectViewMode } from './helpers/smoke-fixtures.js';
import { assert, buildPresenterModuleUrl, expect, expectTableFilterIsContained, pageUpAndExpectCompactWindow, registerSmokeRoutes, test } from './helpers/smoke-fixtures.js';

registerSmokeRoutes();

test('JSON full-view mode fills the viewport and supports repeated lazy-list scrolling', async ({ page }) => {
  const presenterModuleUrl = buildPresenterModuleUrl();
  await page.setViewportSize({ width: 1000, height: 900 });

  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(presenterModuleUrl)};
      const metadata = {
        'source-id': 'repositories-layout-fixture',
        'source-kind': 'fixture',
        'as-of': '2026-09-01T03:00:00Z',
        'retrieved-at': '2026-09-01T03:01:00Z',
        'coverage-start': '2026-08-31T03:00:00Z',
        'coverage-end': '2026-09-01T03:00:00Z',
        completeness: 'complete',
        freshness: 'fresh',
        availability: 'available'
      };
      const emptySource = (source) => ({ source, rows: [], metadata });
      const campaigns = ['EU CRA', 'Repository Ops', 'Optimization'];
      const roles = ['orchestrator', 'worker'];
      const modes = ['review', 'live', 'staged'];
      const sources = {
        inventory: {
          source: 'inventory',
          rows: Array.from({ length: 100 }, (_, index) => ({
            organization: 'githubnext',
            repository: \`repository-\${index + 1}\`,
            campaign: campaigns[index % campaigns.length],
            role: roles[index % roles.length],
            mode: modes[index % modes.length]
          })),
          metadata
        },
        runs: emptySource('runs')
      };
      const dashboardDocument = {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'full-view-layout',
          title: 'Full View Layout',
          callouts: [{
            id: 'inventory-warning',
            title: 'Inventory warning',
            description: 'Inventory data may be incomplete.'
          }],
          pages: [{
            id: 'inventory',
            kind: 'custom',
            title: 'Inventory',
            description: 'Inventory subtitle',
            views: [{
              id: 'inventory-callout',
              title: 'Review inventory warning',
              description: 'Some inventory rows require review.',
              mark: 'callout',
              callout: {
                label: 'Warning',
                icon: 'alert'
              }
            }, {
               id: 'inventory-summary',
               title: 'Inventory summary',
               data: { source: 'inventory' },
               mark: 'metric',
               encoding: {
                 value: { field: 'repository', type: 'nominal', aggregate: 'distinct-count' }
               }
             }, {
               id: 'inventory-list',
               title: 'Inventory list',
               description: 'Inventory view subtitle',
              data: { source: 'inventory' },
              mark: 'table',
              controls: 'interactive',
              'lazy-list': true,
              layout: 'full-view',
              encoding: {
                columns: [
                  { field: 'organization', type: 'nominal' },
                  { field: 'repository', type: 'nominal' },
                  { field: 'campaign', type: 'nominal' },
                  { field: 'role', type: 'nominal' },
                  { field: 'mode', type: 'nominal' }
                ]
              }
            }]
          }],
          navigation: [{ label: 'Explore', pages: ['inventory'] }]
        }
      };

      document.querySelector('#root').append(renderDashboard({ document: dashboardDocument, sources }));
    </script>
  `);

  const view = page.locator('[data-view-layout="full-view"]');
  const siteCallout = page.locator('[data-notification-id="site-callout:inventory-warning"]');
  const warningCallout = page.getByRole('note', { name: 'Review inventory warning' });
  const summary = page.locator('[data-view-id="inventory-summary"]');
  const pageTitle = page.locator('#page-title');
  const tableFilter = view.locator('.table-filter');
  const lazyList = view.locator('[data-lazy-list]');
  const dashboardRoot = page.locator('.dashboard-root');
  const fieldName = view.locator('thead > tr:first-child > th').first();
  const summaryCell = view.locator('.table-summary-row > th').first();
  const table = view.locator('.table-region');
  await expect(view).toHaveCount(1);
  await expect(siteCallout).toBeVisible();
  await expect(warningCallout).toBeVisible();
  await expect(summary).toBeVisible();
  await selectViewMode(page, 'table');
  await expect(warningCallout).toBeHidden();
  await expect(summary).toBeHidden();
  await expect(pageTitle).toBeVisible();
  await expect(tableFilter).toBeVisible();
  await expect(lazyList).toHaveCount(1);
  await expect(view.getByRole('searchbox', { name: 'Filter Inventory list' })).toBeVisible();
  await expect(page.locator('.overview-header .lede')).toBeHidden();
  await expect(view.getByRole('heading', { name: 'Inventory list' })).toBeHidden();
  expect(await lazyList.evaluate((element) => getComputedStyle(element).borderWidth)).toBe('0px');
  expect((await lazyList.boundingBox())?.height).toBeGreaterThanOrEqual(550);
  await expect(view.locator('tbody tr:visible')).toHaveCount(25);

  const scroll = view.locator('.table-scroll');
  await expect(scroll).toHaveCSS('overscroll-behavior-x', 'none');
  const more = view.locator('[data-table-more]');
  const sidebarBox = await page.locator('.org-sidebar').boundingBox();
  const initialScrollBox = await scroll.boundingBox();
  assert(sidebarBox);
  assert(initialScrollBox);
  expect(initialScrollBox.x).toBeGreaterThanOrEqual(sidebarBox.x + sidebarBox.width);
  await more.evaluate((button) => /** @type {HTMLButtonElement} */ (button).click());
  await expect(view.locator('tbody > tr')).toHaveCount(50);
  await more.evaluate((button) => /** @type {HTMLButtonElement} */ (button).click());
  await more.evaluate((button) => /** @type {HTMLButtonElement} */ (button).click());
  await expect(view.locator('tbody > tr')).toHaveCount(50);
  await expect(view.locator('tbody > tr').first()).toContainText('repository-51');

  for (let cycle = 0; cycle < 3; cycle += 1) {
    await scroll.evaluate((element) => {
      element.scrollTop = 100;
      element.dispatchEvent(new Event('scroll'));
    });
    await expect(dashboardRoot).toHaveClass(/dashboard-full-view-scrolled/);
    await expect(siteCallout).toBeVisible();
    await expect(warningCallout).toBeHidden();
    await expect(summary).toBeHidden();
    await expect(pageTitle).toBeHidden();
    await expect(fieldName).toHaveCSS('opacity', '0.8');
    await expect(summaryCell).toHaveCSS('opacity', '0');
    await expect(summaryCell).toHaveCSS('transition-property', 'opacity');
    const expandedScrollBox = await scroll.boundingBox();
    assert(expandedScrollBox);
    expect(expandedScrollBox.x).toBeGreaterThanOrEqual(sidebarBox.x + sidebarBox.width);
    expect(expandedScrollBox.width).toBeCloseTo(initialScrollBox.width, 0);
    expect((await lazyList.boundingBox())?.height).toBeGreaterThanOrEqual(830);
    await scroll.evaluate((element) => {
      element.scrollTop = element.scrollHeight;
      element.dispatchEvent(new Event('scroll'));
    });
    await expect(view.locator('tbody > tr').first()).toContainText('repository-51');
    await pageUpAndExpectCompactWindow(scroll, view, dashboardRoot, 'repository-26');
    await pageUpAndExpectCompactWindow(scroll, view, dashboardRoot, 'repository-1');
    await scroll.evaluate((element) => {
      element.scrollTop = 0;
      element.dispatchEvent(new Event('scroll'));
    });
    await expect(dashboardRoot).not.toHaveClass(/dashboard-full-view-scrolled/);
    await expect(pageTitle).toBeVisible();
    await expect(tableFilter).toBeVisible();
    await expect(summaryCell).toHaveCSS('opacity', '1');
    for (const firstRepository of [26, 51]) {
      await scroll.evaluate((element) => { element.scrollTop = element.scrollHeight; });
      await scroll.dispatchEvent('scroll');
      await expect(view.locator('tbody > tr').first()).toContainText(`repository-${firstRepository}`);
      await expect(view.locator('tbody > tr')).toHaveCount(50);
    }
  }

  await selectViewMode(page, 'card');
  await selectViewMode(page, 'chart');
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(dashboardRoot).not.toHaveClass(/dashboard-full-view-scrolled/);
  await expect(page.locator('.org-sidebar')).toBeVisible();
  await expect(view).toBeHidden();
  const mobileViewModeToggle = page.locator('.mobile-view-mode-toggle');
  await expect(mobileViewModeToggle).toBeVisible();
  await expect(mobileViewModeToggle).toHaveAttribute('aria-label', 'Switch to Cards view');
  await mobileViewModeToggle.click();
  await expect(page.locator('.dashboard-page:not([hidden])')).toHaveAttribute('data-view-mode', 'card');
  await expect(view.locator('[data-mobile-card-list]')).toBeVisible();
  await expect(table).toBeHidden();
  await expect(mobileViewModeToggle).toHaveAttribute('aria-label', 'Switch to Table view');
  await mobileViewModeToggle.click();
  await expect(page.locator('.dashboard-page:not([hidden])')).toHaveAttribute('data-view-mode', 'table');
  await expect(table).toBeVisible();
  await expect(view.locator('[data-mobile-card-list]')).toBeHidden();
  await expect(dashboardRoot).toHaveClass(/dashboard-full-view/);
  const mobileViewportHeight = await page.evaluate(() => innerHeight);
  expect((await view.boundingBox())?.height).toBeGreaterThanOrEqual(mobileViewportHeight / 2);
  await expectTableFilterIsContained(view.locator('.table-scroll > .table-filter'));
  expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBe(844);

  await page.setViewportSize({ width: 1000, height: 900 });

  await view.locator('.table-scroll').evaluate((element) => {
    element.scrollTop = 100;
    element.dispatchEvent(new Event('scroll'));
  });
  await expect(dashboardRoot).toHaveClass(/dashboard-full-view-scrolled/);
  await expect(page.locator('.top-nav')).toBeHidden();
  await expect(page.locator('.org-sidebar')).toBeVisible();
  expect((await lazyList.boundingBox())?.height).toBeGreaterThanOrEqual(830);
});

test('full-view scrolling with a small overscroll range does not jitter the app chrome', async ({ page }) => {
  test.slow();
  const presenterModuleUrl = buildPresenterModuleUrl();
  await page.setViewportSize({ width: 1000, height: 900 });

  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(presenterModuleUrl)};

      const metadata = {
        'source-id': 'small-overscroll-fixture',
        'source-kind': 'fixture',
        'as-of': '2026-09-01T03:00:00Z',
        'retrieved-at': '2026-09-01T03:01:00Z',
        'coverage-start': '2026-08-31T03:00:00Z',
        'coverage-end': '2026-09-01T03:00:00Z',
        completeness: 'complete',
        freshness: 'fresh',
        availability: 'available'
      };
      const sources = {
        inventory: {
          source: 'inventory',
          rows: Array.from({ length: 20 }, (_, index) => ({
            organization: 'githubnext',
            repository: \`repository-\${index + 1}\`
          })),
          metadata
        }
      };
      const dashboardDocument = {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'small-overscroll-layout',
          title: 'Small Overscroll Layout',
          pages: [{
            id: 'inventory',
            kind: 'custom',
            title: 'Inventory',
            views: [{
              id: 'inventory-list',
              title: 'Inventory list',
              data: { source: 'inventory' },
              mark: 'table',
              controls: 'interactive',
              layout: 'full-view',
              encoding: {
                columns: [
                  { field: 'organization', type: 'nominal' },
                  { field: 'repository', type: 'nominal' }
                ]
              }
            }]
          }],
          navigation: [{ label: 'Explore', pages: ['inventory'] }]
        }
      };

      document.querySelector('#root').append(renderDashboard({ document: dashboardDocument, sources }));
    </script>
  `);

  const view = page.locator('[data-view-layout="full-view"]');
  const dashboardRoot = page.locator('.dashboard-root');
  const scroll = view.locator('.table-scroll');
  await expect(view).toHaveCount(1);

  // Force a small scrollable range (below the minimum jitter-guard threshold) regardless of
  // the exact table height rendered by the browser, so the test is deterministic.
  await scroll.evaluate((element) => {
    Object.defineProperty(element, 'scrollHeight', { value: element.clientHeight + 20, configurable: true });
  });

  for (const scrollTop of [10, 30, 10, 30, 0]) {
    await scroll.evaluate((element, top) => {
      element.scrollTop = top;
      element.dispatchEvent(new Event('scroll'));
    }, scrollTop);
    await expect(dashboardRoot).not.toHaveClass(/dashboard-full-view-scrolled/);
    await expect(page.locator('.top-nav')).toBeVisible();
    await expect(page.locator('.org-sidebar')).toBeVisible();
  }
});

test('full-view mobile chrome stays stable while a repositories table scrolls', async ({ page }) => {
  const presenterModuleUrl = buildPresenterModuleUrl();
  await page.setViewportSize({ width: 390, height: 844 });

  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(presenterModuleUrl)};

      const metadata = {
        'source-id': 'mobile-full-view-fixture',
        'source-kind': 'fixture',
        'as-of': '2026-09-01T03:00:00Z',
        'retrieved-at': '2026-09-01T03:01:00Z',
        'coverage-start': '2026-08-31T03:00:00Z',
        'coverage-end': '2026-09-01T03:00:00Z',
        completeness: 'complete',
        freshness: 'fresh',
        availability: 'available'
      };
      const sources = {
        inventory: {
          source: 'inventory',
          rows: Array.from({ length: 60 }, (_, index) => ({
            organization: 'githubnext',
            repository: \`repository-\${index + 1}\`
          })),
          metadata
        }
      };
      const dashboardDocument = {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'mobile-full-view-layout',
          title: 'Mobile Full View Layout',
          pages: [{
            id: 'inventory',
            kind: 'custom',
            title: 'Inventory',
            views: [{
              id: 'inventory-list',
              title: 'Inventory list',
              data: { source: 'inventory' },
              mark: 'table',
              controls: 'interactive',
              'lazy-list': true,
              layout: 'full-view',
              encoding: {
                columns: [
                  { field: 'organization', type: 'nominal' },
                  { field: 'repository', type: 'nominal' }
                ]
              }
            }]
          }],
          navigation: [{ label: 'Explore', pages: ['inventory'] }]
        }
      };

      document.querySelector('#root').append(renderDashboard({ document: dashboardDocument, sources }));
    </script>
  `);

  const view = page.locator('[data-view-layout="full-view"]');
  const dashboardRoot = page.locator('.dashboard-root');
  const scroll = view.locator('.table-scroll');
  const sidebar = page.locator('.org-sidebar');
  await expect(view).toHaveCount(1);
  await expect(sidebar).toBeVisible();
  const restingBox = await sidebar.boundingBox();
  assert(restingBox);

  for (const scrollTop of [100, 30, 120, 0]) {
    await scroll.evaluate((element, top) => {
      element.scrollTop = top;
      element.dispatchEvent(new Event('scroll'));
    }, scrollTop);
    await expect(dashboardRoot).not.toHaveClass(/dashboard-full-view-scrolled/);
    await expect(sidebar).toBeVisible();
    expect(await sidebar.boundingBox()).toEqual(restingBox);
  }
});

test('pie charts match the report layout at medium viewport widths', async ({ page }) => {
  const presenterModuleUrl = buildPresenterModuleUrl();
  await page.setViewportSize({ width: 800, height: 900 });

  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(presenterModuleUrl)};

      const dashboardDocument = {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'pie-layout',
          title: 'Pie Layout',
          pages: [{
            id: 'cost',
            kind: 'custom',
            title: 'Cost',
            views: [{
              id: 'repository-allocation',
              title: 'AI Credit usage by AW repository',
              description: 'Read-only usage reported by AW runs.',
              data: { source: 'usage' },
              mark: 'chart',
              chart: 'pie',
              encoding: {
                x: { field: 'repository', type: 'nominal', title: 'Repository' },
                y: { field: 'aic', type: 'quantitative', aggregate: 'sum', title: 'Blocked requests' }
              }
            }]
          }],
          navigation: [{ label: 'Investigate', pages: ['cost'] }]
        }
      };
      const sources = {
        usage: {
          source: 'usage',
          rows: [
            { repository: 'a-very-long-repository-name-that-must-wrap-within-the-legend', aic: 4_280_186 },
            { repository: 'service', aic: 2_568_112 }
          ],
          metadata: {
            'source-id': 'pie-layout-fixture',
            'source-kind': 'fixture',
            'as-of': '2026-09-01T03:00:00Z',
            'retrieved-at': '2026-09-01T03:01:00Z',
            completeness: 'complete',
            freshness: 'fresh',
            availability: 'available'
          }
        }
      };

      document.querySelector('#root').append(renderDashboard({ document: dashboardDocument, sources }));
    </script>
  `);

  const heading = page.getByRole('heading', { name: 'AI Credit usage by AW repository' });
  const card = page.locator('.pie-chart-card');
  const description = card.locator('.view-description');
  const layout = page.locator('.pie-chart-layout');
  const chart = layout.locator('.pie-chart-widget');
  const legend = layout.locator('.chart-legend-pie');
  const table = page.locator('.chart-view-pie > .table-region');
  const [headingBox, descriptionBox, layoutBox, chartBox, legendBox, cardBox] = await Promise.all(
    [heading, description, layout, chart, legend, card].map((locator) => locator.boundingBox())
  );

  expect(headingBox).not.toBeNull();
  expect(descriptionBox).not.toBeNull();
  expect(layoutBox).not.toBeNull();
  expect(chartBox).not.toBeNull();
  expect(legendBox).not.toBeNull();
  expect(cardBox).not.toBeNull();
  await expect(table).toHaveCount(0);
  expect(layoutBox?.x).toBeCloseTo(headingBox?.x ?? 0, 0);
  expect(layoutBox?.y).toBeGreaterThan((descriptionBox?.y ?? 0) + (descriptionBox?.height ?? 0));
  expect(legendBox?.x).toBeGreaterThan((chartBox?.x ?? 0) + (chartBox?.width ?? 0));
  expect((legendBox?.x ?? 0) + (legendBox?.width ?? 0))
    .toBeLessThanOrEqual((cardBox?.x ?? 0) + (cardBox?.width ?? 0));
  expect((legendBox?.y ?? 0) + (legendBox?.height ?? 0) / 2)
    .toBeCloseTo((chartBox?.y ?? 0) + (chartBox?.height ?? 0) / 2, 0);
  const segmentGeometry = await chart.locator('.pie-chart-segment').evaluateAll((segments) => ({
    lengths: segments.map((segment) => /** @type {SVGGeometryElement} */ (segment).getTotalLength()),
    dashArrays: segments.map((segment) => getComputedStyle(segment).strokeDasharray),
    lineCaps: segments.map((segment) => getComputedStyle(segment).strokeLinecap),
    transforms: segments.map((segment) => getComputedStyle(segment).transform),
    vectorEffects: segments.map((segment) => getComputedStyle(segment).vectorEffect)
  }));
  expect(segmentGeometry.lengths[0]).toBeCloseTo(56.5, 1);
  expect(segmentGeometry.lengths[1]).toBeCloseTo(31.5, 1);
  expect(segmentGeometry.lengths.reduce((sum, length) => sum + length, 0)).toBeCloseTo(88, 1);
  expect(segmentGeometry.dashArrays).toEqual(['none', 'none']);
  expect(segmentGeometry.lineCaps).toEqual(['round', 'round']);
  expect(segmentGeometry.transforms).toEqual(['none', 'none']);
  expect(segmentGeometry.vectorEffects).toEqual(['none', 'none']);
  const centerTextGeometry = await chart.locator('svg').evaluate((svg) => {
    const total = /** @type {SVGGraphicsElement} */ (svg.querySelector('.pie-chart-total-value'));
    const label = /** @type {SVGGraphicsElement} */ (svg.querySelector('.pie-chart-total-label'));
    return [total.getBBox(), label.getBBox()].map(({ x, width }) => ({ x, width }));
  });
  for (const { x, width } of centerTextGeometry) {
    expect(x).toBeGreaterThanOrEqual(9);
    expect(x + width).toBeLessThanOrEqual(33);
  }

  const firstMark = chart.locator('.pie-chart-mark').first();
  expect(await firstMark.evaluate((mark) => {
    mark.focus();
    return mark === mark.parentElement?.lastElementChild;
  })).toBe(true);

  await page.setViewportSize({ width: 390, height: 844 });
  const tableToggle = layout.getByRole('button', { name: 'Hide chart table' });
  await expect(legend).toBeVisible();
  await expect(tableToggle).toBeVisible();
  await tableToggle.click();
  await expect(legend).toBeHidden();
  await layout.getByRole('button', { name: 'Show chart table' }).click();
  await expect(legend).toBeVisible();
});
