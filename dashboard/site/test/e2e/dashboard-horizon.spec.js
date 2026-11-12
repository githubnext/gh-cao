import { assert, buildPresenterModuleUrl, expect, registerSmokeRoutes, test } from './helpers/smoke-fixtures.js';

registerSmokeRoutes();

for (const width of [1200, 390, 320]) {
  test(`global horizon picker selects and retains windows without a page filter bar at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.setContent(`
      <div id="root"></div>
      <script type="module">
        import { renderDashboard } from ${JSON.stringify(buildPresenterModuleUrl())};
        import { prepareDashboardViewSources } from 'http://dashboard.test/test/e2e/helpers/dashboard-view-sources.js';
        localStorage.clear();
        const dashboardDocument = {
            languageVersion: '0.1.0',
            dashboard: {
              id: 'horizon-picker', title: 'Horizon picker',
              defaults: { time: { range: '1w' } },
              pages: ['runs', 'history'].map(id => ({
                id, kind: 'custom', title: id, 'filter-bar': id === 'history', views: [{
                  id: id + '-table', mark: 'table', data: { source: 'runs' },
                  encoding: { columns: [{ field: 'run' }] }
                }]
              }))
            }
          };
        const metadata = {
          'source-id': 'horizon-fixture', 'source-kind': 'fixture',
          'as-of': '2026-10-16T12:00:00Z', 'retrieved-at': '2026-10-16T12:00:00Z',
          completeness: 'complete', freshness: 'fresh', availability: 'available'
        };
        const sources = { runs: { source: 'runs', metadata, rows: [
          { run: 'recent', 'observed-at': '2026-10-16T06:00:00Z', 'rollout-mode': 'review' },
          { run: 'month', 'observed-at': '2026-10-01T12:00:00Z', 'rollout-mode': 'review' },
          { run: 'quarter', 'observed-at': '2026-07-01T12:00:00Z', 'rollout-mode': 'review' },
          { run: 'older', 'observed-at': '2025-09-01T12:00:00Z', 'rollout-mode': 'review' }
        ] } };
        document.querySelector('#root').append(renderDashboard({
          document: dashboardDocument,
          sources,
          loadPageSources: async (pageId, options) => {
            document.body.dataset.queryContext = JSON.stringify(options.queryContext ?? {});
            document.body.dataset.loadedPage = pageId;
            return prepareDashboardViewSources(dashboardDocument, pageId, sources, {
              ...options, evaluatedAt: metadata['as-of']
            });
          }
        }));
      </script>
    `);
    await expect(page.locator('body')).toHaveAttribute('data-loaded-page', 'runs');
    if (width < 700) await page.locator('.mobile-nav-menu > summary').click();
    const horizon = page.locator('.dashboard-horizon');
    const toggle = horizon.locator('.horizon-toggle');
    await toggle.click();
    const picker = horizon.getByRole('combobox', { name: 'Time window' });
    await expect(picker).toBeVisible();
    await expect(picker).toHaveValue('1w');
    await expect(horizon.getByLabel('Window start time')).toBeHidden();
    await expect(horizon.locator('.horizon-details-description')).toBeHidden();
    await expect(horizon.locator('.filter-tuning-controls')).toHaveCSS('transform', 'none');
    const bounds = await horizon.locator('.filter-tuning-controls').boundingBox();
    assert(bounds);
    if (width < 700) {
      const drawerBounds = await page.locator('.mobile-nav-menu-list').boundingBox();
      assert(drawerBounds);
      expect(bounds.x).toBeGreaterThanOrEqual(drawerBounds.x);
      expect(bounds.x + bounds.width).toBeLessThanOrEqual(drawerBounds.x + drawerBounds.width);
    } else {
      expect(bounds.width).toBeLessThanOrEqual(280);
    }
    expect(bounds.x).toBeGreaterThanOrEqual(0);
    expect(bounds.x + bounds.width).toBeLessThanOrEqual(width);
    const assertNoOverflow = async () => {
      expect(await horizon.locator('.filter-tuning-controls').evaluate((panel) => (
        panel.scrollWidth <= panel.clientWidth
      ))).toBe(true);
      for (const control of await horizon.locator('.time-window-control :is(select, input):visible').all()) {
        const controlBounds = await control.boundingBox();
        assert(controlBounds);
        expect(controlBounds.width).toBeGreaterThanOrEqual(12);
        expect(controlBounds.x).toBeGreaterThanOrEqual(bounds.x);
        expect(controlBounds.x + controlBounds.width).toBeLessThanOrEqual(bounds.x + bounds.width);
      }
    };
    await assertNoOverflow();
    if (width > 700) {
      const triggerBounds = await toggle.boundingBox();
      assert(triggerBounds);
      expect(bounds.y).toBeGreaterThanOrEqual(triggerBounds.y + triggerBounds.height);
      expect(bounds.x + bounds.width - triggerBounds.x).toBeLessThan(45);
    }
    await horizon.getByText('Coverage details', { exact: true }).click();
    await assertNoOverflow();
    await horizon.getByText('Coverage details', { exact: true }).click();

    await expect(picker.locator('option[value="all"], option[value="90d"], option[value="180d"], option[value="365d"]')).toHaveCount(0);
    for (const [range, days] of Object.entries({ '24h': 1, '1w': 7, '30d': 30 })) {
      await picker.selectOption(range);
      await expect.poll(async () => page.locator('body').evaluate((body) => {
        const window = JSON.parse(body.dataset.queryContext ?? '{}').timeWindow;
        return window ? (Date.parse(window.end) - Date.parse(window.start)) / 86_400_000 : 0;
      })).toBe(days);
      await expect(picker).toHaveValue(range);
      await expect(page.locator('.dashboard-page:not([hidden]) .custom-table tbody tr'))
        .toHaveCount(days < 30 ? 1 : days < 180 ? 2 : 3);
    }

    await picker.selectOption('30d');
    await horizon.locator('.mode-filter-control input[value="unknown"]').uncheck();
    await horizon.locator('.mode-filter-control input[value="live"]').uncheck();
    await page.locator('[data-nav-page-id="history"]:visible, [data-mobile-nav-page-id="history"]:visible').first().click();
    await expect(page.locator('body')).toHaveAttribute('data-loaded-page', 'history');
    await expect.poll(() => page.locator('body').evaluate((body) => {
      const window = JSON.parse(body.dataset.queryContext ?? '{}').timeWindow;
      return (Date.parse(window.end) - Date.parse(window.start)) / 86_400_000;
    })).toBe(30);
    await expect.poll(() => page.locator('body').evaluate(
      (body) => JSON.parse(body.dataset.queryContext ?? '{}').filters?.mode
    )).toEqual(['review']);
    if (width < 700) await page.locator('.mobile-nav-menu > summary').click();
    if (await toggle.getAttribute('aria-expanded') !== 'true') await toggle.click();
    await picker.selectOption('custom');
    await expect(horizon.getByLabel('Window start time')).toBeVisible();
    await horizon.getByLabel('Window start time').fill('2026-08-01T00:00');
    await horizon.getByLabel('Window stop time').fill('2026-10-03T00:00');
    await horizon.getByRole('button', { name: 'Apply', exact: true }).click();
    await expect(horizon.getByRole('alert')).toContainText('no more than 30 days');
    await assertNoOverflow();
    await horizon.getByLabel('Window start time').fill('2026-10-01T00:00');
    await horizon.getByLabel('Window stop time').fill('2026-10-03T00:00');
    await horizon.getByRole('button', { name: 'Apply', exact: true }).click();
    await expect.poll(() => page.locator('body').evaluate((body) => {
      const window = JSON.parse(body.dataset.queryContext ?? '{}').timeWindow;
      return (Date.parse(window.end) - Date.parse(window.start)) / 86_400_000;
    })).toBe(2);
    await expect(page.locator('.dashboard-page:not([hidden]) .custom-table tbody')).toHaveText('month');
    await picker.press('Escape');
    await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  });
}
