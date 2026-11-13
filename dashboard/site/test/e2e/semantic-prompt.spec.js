import { authoritativeDashboard, buildPresenterModuleUrl, expect, registerSmokeRoutes, test } from './helpers/smoke-fixtures.js';
import { getPrimerStyles } from '../../src/styles.js';

registerSmokeRoutes();

test('a loading chart has no prompt action until its own data arrives', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(buildPresenterModuleUrl())};
      const loadPageSources = (_pageId, options) => new Promise((resolve) => {
        window.publishChart = (rows = [{ workflow: 'daily', count: 3 }]) => options.onUpdate({
          runs: {
            source: 'runs', rows,
            metadata: {
              'source-id': 'runs', 'source-kind': 'fixture',
              'as-of': '', 'retrieved-at': '',
              availability: 'available', completeness: 'complete', freshness: 'fresh'
            }
          }
        });
        window.finishLoading = () => resolve({});
      });
      document.querySelector('#root').append(renderDashboard({
        document: {
          languageVersion: '0.1.0',
          dashboard: {
            id: 'loading-prompts', title: 'Loading prompts',
            pages: [{
              id: 'overview', kind: 'custom', title: 'Overview',
              views: [{
                id: 'runs-chart', title: 'Runs chart', prompt: 'always',
                mark: 'chart', chart: 'bar', data: { source: 'runs' },
                encoding: {
                  x: { field: 'workflow', type: 'nominal' },
                  y: { field: 'count', type: 'quantitative' }
                }
              }]
            }]
          }
        },
        sources: {},
        loadPageSources
      }));
    </script>
  `);
  await page.addStyleTag({ content: getPrimerStyles() });
  const view = page.locator('[data-view-id="runs-chart"]');
  const action = view.getByRole('button', { name: 'Propose fix: Runs chart' });
  await expect(view).toHaveAttribute('aria-busy', 'true');
  await expect(view.locator('.dashboard-view-skeleton')).toBeVisible();
  await expect(view.locator('.table-intent-button')).toHaveCount(0);
  const loadingHeight = await view.evaluate((element) => element.getBoundingClientRect().height);
  expect(loadingHeight).toBe(280);
  await page.evaluate(() => Reflect.get(window, 'publishChart')([]));
  await expect(view.locator('.dashboard-view-skeleton')).toHaveCount(0);
  expect(await view.evaluate((element) => element.getBoundingClientRect().height)).toBe(loadingHeight);
  await page.evaluate(() => Reflect.get(window, 'publishChart')());
  await expect(action).toBeVisible();
  await expect(view.locator('.dashboard-view-skeleton')).toHaveCount(0);
  await action.click();
  await expect(view.locator('.table-intent-preview')).toContainText('"workflow": "daily"');
  await page.evaluate(() => Reflect.get(window, 'finishLoading')());
});

for (const { width, categories } of [390, 1280].flatMap((width) => (
  [0, 1, 9, 18].map((categories) => ({ width, categories }))
))) {
  test(`pie chart loading reserves responsive space for ${categories} categories at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.setContent(`
      <div id="root"></div>
      <script type="module">
        import { renderDashboard } from ${JSON.stringify(buildPresenterModuleUrl())};
        const metadata = {
          'source-id': 'usage', 'source-kind': 'fixture', 'as-of': '', 'retrieved-at': '',
          availability: 'available', completeness: 'complete', freshness: 'fresh'
        };
        const loadPageSources = (_pageId, options) => new Promise((resolve) => {
          window.finishLoading = () => resolve({});
          window.publishCost = () => {
            const sources = { usage: { source: 'usage', rows: Array.from({ length: ${categories} }, (_, index) => ({
              campaign: 'Campaign ' + index, credits: index + 1
            })), metadata } };
            options.onUpdate(sources);
          };
        });
        document.querySelector('#root').append(renderDashboard({
          document: {
            languageVersion: '0.1.0',
            dashboard: {
              id: 'loading-cost', title: 'Cost',
              pages: [{
                id: 'cost', kind: 'custom', title: 'Cost',
                views: [{
                  id: 'cost-chart', title: 'Cost by campaign',
                  description: 'Compare AI Credits consumed by each campaign.',
                  mark: 'chart', chart: 'pie', layout: 'full', prompt: 'always',
                  data: { source: 'usage' },
                  encoding: {
                    x: { field: 'campaign', type: 'nominal' },
                    y: { field: 'credits', type: 'quantitative' }
                  }
                }, {
                  id: 'remaining-chart', title: 'Remaining evidence',
                  mark: 'chart', chart: 'bar', data: { source: 'remaining' },
                  encoding: {
                    x: { field: 'campaign', type: 'nominal' },
                    y: { field: 'credits', type: 'quantitative' }
                  }
                }]
              }]
            }
          },
          sources: {},
          loadPageSources
        }));
      </script>
    `);
    await page.addStyleTag({ content: getPrimerStyles() });
    const view = page.locator('[data-view-id="cost-chart"]');
    const heading = view.getByRole('heading', { name: 'Cost by campaign' });
    const description = view.locator(':scope > .view-description');
    await expect(view.locator('.dashboard-view-skeleton')).toBeVisible();
    await expect(heading).toBeVisible();
    await expect(description).toHaveText('Compare AI Credits consumed by each campaign.');
    const headingBounds = await heading.boundingBox();
    if (!headingBounds) throw new Error('The pending chart heading must reserve visible space.');
    expect(headingBounds.width).toBeGreaterThan(100);
    expect(headingBounds.height).toBeGreaterThan(16);
    await expect(view.locator(':scope > .chart-prompt-heading')).toHaveCSS('min-height', '32px');
    const loadingBounds = await view.boundingBox();
    const skeletonBounds = await view.locator('.dashboard-view-skeleton').boundingBox();
    await page.evaluate(async () => {
      await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
      const shifts = /** @type {number[]} */ ([]);
      Reflect.set(window, 'observedLayoutShifts', shifts);
      new PerformanceObserver((list) => {
        for (const entry of list.getEntries()) {
          if (!Reflect.get(entry, 'hadRecentInput')) shifts.push(Reflect.get(entry, 'value'));
        }
      }).observe({ type: 'layout-shift' });
    });

    await page.evaluate(() => Reflect.get(window, 'publishCost')());

    await expect(view.locator('.dashboard-view-skeleton')).toHaveCount(0);
    await expect(heading).toBeVisible();
    await expect(description).toHaveText('Compare AI Credits consumed by each campaign.');
    await expect(view.locator('.pie-chart-layout')).toBeVisible();
    const readyBounds = await view.boundingBox();
    const legend = view.locator('.chart-legend-pie');
    if (!loadingBounds || !readyBounds) throw new Error('The chart must retain its complete layout.');
    if (categories === 0) {
      await expect(view.locator('[data-view-state="empty"]')).toBeVisible();
      await expect(legend).toHaveCount(0);
    } else {
      await expect(legend.locator('li')).toHaveCount(categories);
      const legendBounds = await legend.boundingBox();
      if (!legendBounds) throw new Error('Every legend row must be included in the chart layout.');
      expect(legendBounds.y + legendBounds.height).toBeLessThanOrEqual(readyBounds.y + readyBounds.height + 1);
    }
    if (width === 390) {
      const skeletonWidth = loadingBounds.width;
      expect(loadingBounds.height).toBeGreaterThan(skeletonWidth);
      const layout = await view.locator('.pie-chart-layout').boundingBox();
      expect(layout?.height).toBeGreaterThanOrEqual(skeletonWidth);
      await page.waitForTimeout(100);
      const cls = await page.evaluate(() => {
        const shifts = /** @type {number[]} */ (Reflect.get(window, 'observedLayoutShifts'));
        return shifts.reduce((sum, value) => sum + value, 0);
      });
      expect(cls).toBeLessThanOrEqual(0.1);
      if (categories === 18) {
        if (!layout || !skeletonBounds) throw new Error('The chart must preserve its loading footprint.');
        expect(Math.abs(layout.height - skeletonBounds.height)).toBeLessThanOrEqual(1);
        const lastCategory = legend.locator('li').last();
        await lastCategory.scrollIntoViewIfNeeded();
        await expect(lastCategory).toBeInViewport();
      }
    }
    if (width === 390 && categories > 0) {
      const toggle = view.locator('.pie-chart-table-toggle');
      await toggle.click();
      await expect(legend).toBeHidden();
      await expect(toggle).toHaveAccessibleName('Show chart table');
      await toggle.click();
      await expect(legend).toBeVisible();
      await expect(legend.locator('li')).toHaveCount(categories);
    }
    await page.evaluate(() => Reflect.get(window, 'finishLoading')());
  });
}

test('an independently loading element hides its prompt until all busy widgets settle', async ({ page }) => {
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(buildPresenterModuleUrl())};
      import { configureSourceLoader } from 'http://dashboard.test/src/source-store.js';
      const resolutions = new Map();
      configureSourceLoader((name) => new Promise((resolve) => resolutions.set(name, resolve)));
      window.resolveHeaderSource = (name) => resolutions.get(name)({
        source: name,
        rows: name === 'overview-header-presentation' ? [{ heading: 'Factory health', summary: 'Ready' }] : [],
        metadata: {
          'source-id': name, 'source-kind': 'fixture', 'as-of': '', 'retrieved-at': '',
          availability: name === 'overview-header-presentation' ? 'available' : 'empty',
          completeness: 'complete', freshness: 'fresh'
        }
      });
      document.querySelector('#root').append(renderDashboard({
        document: {
          languageVersion: '0.1.0',
          dashboard: {
            id: 'loading-element', title: 'Loading element',
            pages: [{
              id: 'overview', kind: 'custom', title: 'Overview',
              views: [{
                id: 'header', title: 'Campaign header', prompt: 'always',
                mark: 'element', element: 'factory-header',
                data: { sources: ['overview-header-presentation', 'overview-rhythm'] }
              }]
            }]
          }
        },
        sources: {}
      }));
    </script>
  `);
  await page.addStyleTag({ content: getPrimerStyles() });
  const view = page.locator('[data-view-id="header"]');
  const action = view.locator('.table-intent-button');
  await expect(view.locator('#agent-factory-heading')).toHaveAttribute('aria-busy', 'true');
  await expect(action).toBeHidden();
  await expect(view.getByRole('button', { name: 'Create prompt for Campaign header' })).toHaveCount(0);
  await page.evaluate(() => Reflect.get(window, 'resolveHeaderSource')('overview-header-presentation'));
  await expect(view.locator('#agent-factory-heading')).toHaveText('Factory health');
  await expect(action).toBeHidden();
  await page.evaluate(() => Reflect.get(window, 'resolveHeaderSource')('overview-rhythm'));
  await expect(action).toBeVisible();
});

test('a chart with composed semantics offers a prompt preview and returns focus', async ({ page }) => {
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderDashboard } from ${JSON.stringify(buildPresenterModuleUrl())};
      const metadata = {
        'source-id': 'cost-by-repository', 'source-kind': 'fixture',
        'as-of': '2026-09-01T00:00:00Z', 'retrieved-at': '2026-09-01T00:00:00Z',
        availability: 'available', completeness: 'complete', freshness: 'fresh'
      };
      document.querySelector('#root').append(renderDashboard({
        document: ${JSON.stringify(authoritativeDashboard)},
        sources: {
          'cost-by-repository': {
            source: 'cost-by-repository',
            rows: [{ 'repository-coordinate': 'example/repo', aic: 12 }],
            metadata
          },
          'cost-per-workflow-run': {
            source: 'cost-per-workflow-run',
            rows: [{ 'repository-coordinate': 'example/repo', workflow: 'daily', 'aic-per-run': 6 }],
            metadata: { ...metadata, 'source-id': 'cost-per-workflow-run' }
          }
        }
      }));
    </script>
  `);
  await page.addStyleTag({ content: getPrimerStyles() });
  await page.evaluate(() => { window.location.hash = '#page-cost'; });
  await expect(page.locator('[data-view-id="cost-by-campaign"]')).toHaveCount(0);
  const perRun = page.locator('[data-view-id="cost-per-workflow-run"]');
  await expect(perRun).toHaveAttribute('data-disclosure', 'essential');
  await expect(perRun.getByRole('heading', { name: 'Cost per workflow run', exact: true })).toBeVisible();
  await expect(perRun.locator('summary')).toHaveCount(0);
  const action = page.locator('[data-view-id="cost-by-repository"] .table-intent-button');
  await expect(action).toBeVisible();
  const titleRow = page.locator('[data-view-id="cost-by-repository"] .chart-prompt-heading');
  const title = titleRow.getByRole('heading', { name: 'Cost per repository', level: 2, exact: true });
  await expect(title).toBeVisible();
  await expect(action.locator('.octicon')).toBeVisible();
  await expect(action).toHaveAttribute('aria-label', /Propose fix: /);
  await expect(action.locator('span')).toBeHidden();
  const titleBox = await title.boundingBox();
  const buttonBox = await action.boundingBox();
  if (!titleBox || !buttonBox) throw new Error('Chart title and prompt button must be visible.');
  expect(buttonBox.x).toBeGreaterThan(titleBox.x + titleBox.width);
  expect(buttonBox.y).toBeLessThan(titleBox.y + titleBox.height);
  await action.click();
  const dialog = page.locator('[data-view-id="cost-by-repository"] .table-intent-dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog.locator('.table-intent-preview')).toContainText('Subject:');
  await expect(dialog.locator('.table-intent-preview')).toContainText('Page intent:');
  await expect(dialog.locator('.table-intent-preview')).toContainText('Determine where observed AI Credit usage is most concentrated.');
  await expect(dialog.locator('.table-intent-preview')).toContainText('https://github.com/githubnext/gh-cao/blob/main/skills/analyze-cao/SKILL.md');
  await expect(dialog.locator('.table-intent-preview')).toContainText('https://github.com/githubnext/gh-cao/blob/HEAD/.github/workflows/cao.json');
  await expect(dialog.locator('.table-intent-preview')).toContainText('Named CAO query IDs:');
  await expect(dialog.locator('.table-intent-preview')).toContainText('Any unusual repository cost is checked');
  await expect(dialog.locator('.table-intent-preview')).toContainText('/analyze-cao');
  await expect(dialog.locator('.table-intent-preview')).toContainText('This is a preview of the data. Requery for full data.');
  await expect(dialog.locator('.table-intent-preview')).toContainText('Create a PR with the changes.');
  await dialog.getByRole('button', { name: 'Close prompt preview' }).click();
  await expect(action).toBeFocused();
});

test('an open shared prompt preview tracks reactive evidence until it closes', async ({ page }) => {
  await page.setContent(`
    <div id="root"></div>
    <script type="module">
      import { renderPromptPreviewAction } from ${JSON.stringify('http://dashboard.test/src/components/data-view.js')};
      import { state } from ${JSON.stringify('http://dashboard.test/src/reactive.js')};
      const evidence = state('first observation');
      const action = renderPromptPreviewAction('Inspect evidence', evidence.get, undefined, 'comment', 'semantic-prompt');
      window.refreshEvidence = () => evidence.set('updated observation');
      document.querySelector('#root').append(action);
    </script>
  `);
  await page.getByRole('button', { name: 'Inspect evidence' }).click();
  const preview = page.locator('.table-intent-preview');
  await expect(preview).toHaveText('first observation');
  await page.evaluate(() => Reflect.get(window, 'refreshEvidence')());
  await expect(preview).toHaveText('updated observation');
  await page.getByRole('button', { name: 'Close prompt preview' }).click();
  await expect(page.getByRole('button', { name: 'Inspect evidence' })).toBeFocused();
});
