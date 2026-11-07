import { expect, registerSmokeRoutes, test } from './helpers/smoke-fixtures.js';

registerSmokeRoutes();

for (const scenario of [
  { pageId: 'indexing', viewId: 'indexing-workflow-runs', legend: 'Ingestion entries on shard day', values: ['3', '1'] },
  { pageId: 'workflow-runs', viewId: 'workflow-run-duration-cost', legend: 'AI Credits', values: ['2', '1'] }
]) {
  test(`${scenario.pageId} scatter uses production queries for size evidence and refreshes on mobile`, async ({ page }) => {
    await page.evaluate(async ({ pageId, viewId }) => {
      const { compileDashboardViewPayloadQueries } = await import(`${location.origin}/src/data/queries/view-payload-compiler.js`);
      const { renderDataView } = await import(`${location.origin}/src/components/data-view.js`);
      const { buildChartPoints, toViewText } = await import(`${location.origin}/src/components/view-data.js`);
      const { chartStyles } = await import(`${location.origin}/src/styles-charts.js`);
      const fragment = await fetch(`${location.origin}/dashboard-fragments/${pageId === 'indexing' ? 'operations' : 'workflow-runs'}.json`).then((response) => response.json());
      const pageModel = fragment.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === pageId);
      const view = pageModel.views.find((/** @type {{ id: string }} */ candidate) => candidate.id === viewId);
      const compiled = compileDashboardViewPayloadQueries(pageModel, pageId, {
        queries: fragment.queries, viewId, routeParameters: { workflow: 'octo/api:worker' }
      });
      const worker = new Worker(`${location.origin}/src/data-worker.js`, { type: 'module' });
      const style = document.createElement('style');
      style.textContent = chartStyles;
      document.head.append(style);
      const root = document.querySelector('#root');
      worker.onmessage = (event) => {
        const source = event.data.data?.[compiled.aliases[0]];
        if (!source || !root) return;
        const query = () => { throw new Error('Adopted bubble views must not transform source rows'); };
        const chart = renderDataView('chart', {
          pageId, title: view.title, view, sourceName: source.source, rows: source.rows,
          metadata: source.metadata, contextDetails: [], headingTag: 'h2', buildChartPoints,
          prepareChartPoints: query, prepareTableRows: query, toText: toViewText
        });
        if (chart) root.replaceChildren(chart);
      };
      const metadata = {
        'source-id': 'adopted-bubbles', 'source-kind': 'fixture',
        'as-of': '2026-11-01T00:00:00Z', 'retrieved-at': '2026-11-01T00:00:00Z',
        availability: 'available', completeness: 'complete', freshness: 'fresh'
      };
      const transactions = [4, 0, null, 1].map((runs, index) => ({
        id: `transaction-${index}`, kind: 'ingest-normalized-jsonl', rawRuns: runs,
        payloadScope: `gh-aw-logs-runs/2026-11-0${index < 3 ? 1 : 2}-0000-0123456789abcdef.jsonl`
      }));
      const runs = [2, 1, 4].map((value, index) => ({
        'aic-total': value, duration: '1h', 'action-minutes': 10 + index, workflow: 'worker',
        'started-at': `2026-11-01T0${index}:00:00Z`, organization: 'octo', repository: 'api',
        run: String(index), 'run-conclusion': 'success',
        'run-link': `https://github.com/octo/api/actions/runs/${index}`
      }));
      const send = () => worker.postMessage({
        id: 1, operation: 'execute-dashboard-queries', queries: compiled.queries, sourceNames: compiled.aliases,
        sources: {
          transactions: { source: 'transactions', rows: transactions, metadata },
          runs: { source: 'runs', rows: runs, metadata }
        }
      });
      send();
      const refresh = document.createElement('button');
      refresh.textContent = 'Refresh size evidence';
      refresh.onclick = () => {
        if (pageId === 'indexing') transactions.push({ ...transactions[0], id: 'transaction-new' });
        else runs.push({ ...runs[0], run: 'new', 'aic-total': 3 });
        send();
      };
      document.body.append(refresh);
    }, scenario);
    const legend = page.getByRole('list', { name: `${scenario.legend} size legend` });
    await expect(legend).toBeVisible();
    await expect(page.locator('.scatter-chart-point')).toHaveCount(scenario.pageId === 'indexing' ? 4 : 3);
    await expect(page.getByRole('img', { name: new RegExp(`${scenario.legend}: ${scenario.values[0]}$`) }).first()).toBeVisible();
    await expect(page.getByRole('img', { name: new RegExp(`${scenario.legend}: ${scenario.values[1]}$`) }).first()).toBeVisible();
    if (scenario.pageId === 'indexing') {
      await expect(page.getByRole('img', { name: /Run count unavailable \(shown at 0\)/ })).toBeVisible();
    }
    await page.setViewportSize({ width: 390, height: 844 });
    await expect(legend).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
    await page.getByRole('button', { name: 'Refresh size evidence' }).click();
    await expect(page.locator('.scatter-chart-point')).toHaveCount(scenario.pageId === 'indexing' ? 5 : 4);
    await expect(page.getByRole('img', { name: new RegExp(`${scenario.legend}: ${Number(scenario.values[0]) + 1}$`) }).first()).toBeVisible();
  });
}

test('bubble charts preserve value ratios, accessible legends, and worker refreshes on desktop and mobile', async ({ page }) => {
  await page.evaluate(async () => {
    const { compileDashboardViewPayloadQueries } = await import(`${location.origin}/src/data/queries/view-payload-compiler.js`);
    const { renderDataView } = await import(`${location.origin}/src/components/data-view.js`);
    const { buildChartPoints, toViewText } = await import(`${location.origin}/src/components/view-data.js`);
    const { chartStyles } = await import(`${location.origin}/src/styles-charts.js`);
    const fixture = await fetch(`${location.origin}/test/fixtures/scatter-value-contract.json`).then((response) => response.json());
    const pageModel = fixture.dashboard.pages[0];
    const view = pageModel.views[0];
    const compiled = compileDashboardViewPayloadQueries(pageModel, pageModel.id, { queries: fixture.dashboard.queries });
    const worker = new Worker(`${location.origin}/src/data-worker.js`, { type: 'module' });
    const style = document.createElement('style');
    style.textContent = chartStyles;
    document.head.append(style);
    const root = document.querySelector('#root');
    worker.onmessage = (event) => {
      const source = event.data.data?.[compiled.aliases[0]];
      if (!source || !root) return;
      const query = () => { throw new Error('Bubble UI must not query source rows'); };
      const chart = renderDataView('chart', {
        pageId: pageModel.id, title: view.title, view, sourceName: source.source,
        rows: source.rows, metadata: source.metadata, contextDetails: [], headingTag: 'h2',
        buildChartPoints, prepareChartPoints: query, prepareTableRows: query, toText: toViewText
      });
      if (chart) root.replaceChildren(chart);
    };
    /** @param {number[]} values */
    const send = (values) => worker.postMessage({
      id: 1, operation: 'execute-dashboard-queries', queries: compiled.queries, sourceNames: compiled.aliases,
      sources: { runs: { source: 'runs', metadata: {
        'source-id': 'bubble-evidence', 'source-kind': 'fixture',
        'as-of': '2026-11-01T00:00:00Z', 'retrieved-at': '2026-11-01T00:00:00Z',
        availability: 'available', completeness: 'complete', freshness: 'fresh'
      }, rows: values.map((value, index) => ({
        run: String(index), 'started-at': `2026-11-01T0${index}:00:00Z`,
        duration: 1000 + index, 'aic-total': value, 'run-conclusion': index % 2 ? 'failure' : 'success'
      })) } }
    });
    send([1, 4, 16]);
    const refresh = document.createElement('button');
    refresh.textContent = 'Refresh evidence';
    refresh.onclick = () => send([4, 16, 64]);
    const dispose = document.createElement('button');
    dispose.textContent = 'Dispose bubbles';
    dispose.onclick = () => { worker.terminate(); root?.replaceChildren(); };
    document.body.append(refresh, dispose);
  });
  const points = page.locator('.scatter-chart-point');
  const legend = page.getByRole('list', { name: 'AI credits size legend' });
  await expect(points).toHaveCount(3);
  expect(await points.evaluateAll((circles) => circles.map((circle) => {
    const style = getComputedStyle(circle);
    return { fill: style.fillOpacity, stroke: style.strokeOpacity };
  }))).toEqual(Array(3).fill({ fill: '0.4', stroke: '1' }));
  await expect(legend).toBeVisible();
  await expect(legend).toContainText('16');
  expect(await points.evaluateAll((circles) => circles.map((circle) => Number(circle.getAttribute('r'))).sort((a, b) => a - b))).toEqual([1, 2, 4]);
  await page.getByRole('img', { name: /AI credits: 4$/ }).focus();
  await expect(page.getByRole('img', { name: /AI credits: 4$/ })).toBeFocused();
  await expect(page.locator('.chart-point:focus .point-tooltip')).toContainText('AI credits: 4');
  for (const width of [1280, 390, 320]) {
    await page.setViewportSize({ width, height: 844 });
    await expect(legend).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
    const boxes = await points.evaluateAll((circles) => circles.map((circle) => {
      const box = circle.getBoundingClientRect();
      return { width: box.width, height: box.height };
    }));
    for (const box of boxes) expect(box.width).toBeCloseTo(box.height, 3);
    const plot = await page.locator('.line-chart-plot svg').boundingBox();
    if (!plot) throw new Error('Bubble plot has no layout');
    expect(await points.evaluateAll((circles, right) => circles.every((circle) => circle.getBoundingClientRect().right <= right), plot.x + plot.width)).toBe(true);
  }
  await page.getByRole('button', { name: 'Refresh evidence' }).click();
  await expect(legend).toContainText('64');
  await expect(page.getByRole('img', { name: /AI credits: 64$/ })).toBeVisible();
  await page.getByRole('button', { name: 'Dispose bubbles' }).click();
  await expect(points).toHaveCount(0);
});
