// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { SWIMLANE_LAYOUT, chartSeriesClassName, groupChartSeries, listChartSeries, pieChartEntries, renderChartLegend, renderChartWidget, renderPieChartLayout, renderPieLegend } from '../../src/components/chart-elements.js';
import { buildChartPoints } from '../../src/components/view-data.js';

describe('chart element helpers', () => {
  it('uses declared byte and compact count units consistently on chart axes', () => {
    const points = [
      { x: '2026-08-29', y: 1_000_000_000, color: null },
      { x: '2026-08-30', y: 500_000_000, color: null }
    ];
    const bytes = renderChartWidget('line', points, [], null, 'Size', {
      name: 'Bytes', symbol: 'B', significant: 1, format: 'bytes'
    });
    const count = renderChartWidget('line', points, [], null, 'Count', {
      name: 'Modeled count', symbol: 'count', significant: 0.01, format: 'compact-number'
    });
    expect([...bytes.querySelectorAll('.line-chart-y-labels span')].map((tick) => tick.textContent))
      .toEqual(['1.0 GB', '500 MB', '0 B']);
    expect([...count.querySelectorAll('.line-chart-y-labels span')].map((tick) => tick.textContent))
      .toEqual(['1B', '500M', '0']);
  });

  it('retains missing window results as gaps and plots negative changes below zero', () => {
    const rows = [
      { 'observed-at': '2026-08-01T00:00:00Z', delta: 1 },
      { 'observed-at': '2026-08-02T00:00:00Z', delta: null },
      { 'observed-at': '2026-08-03T00:00:00Z', delta: -2 },
      { 'observed-at': '2026-08-04T00:00:00Z', delta: 0 }
    ];
    const points = buildChartPoints('usage', 'Change', rows,
      { field: 'observed-at' }, { field: 'delta' }, null, null);
    expect(points.map((point) => point.y)).toEqual([1, null, -2, 0]);
    const chart = renderChartWidget('line', points, listChartSeries(points));
    const path = chart.querySelector('path.line-chart-series');
    expect(path?.getAttribute('d')?.match(/M /g)).toHaveLength(2);
    expect(chart.querySelectorAll('.line-chart-point')).toHaveLength(3);
    expect([...chart.querySelectorAll('.line-chart-y-labels span')].map((tick) => tick.textContent))
      .toEqual(['1', '-0.50', '-2']);
  });

  it('does not stack missing area observations as zero or bridge their gaps', () => {
    const points = [
      { x: '2026-08-01', y: 2, color: 'first' },
      { x: '2026-08-02', y: null, color: 'first' },
      { x: '2026-08-03', y: 0, color: 'first' },
      { x: '2026-08-01', y: 3, color: 'second' },
      { x: '2026-08-02', y: 4, color: 'second' },
      { x: '2026-08-03', y: 5, color: 'second' }
    ];
    const chart = renderChartWidget('area', points, listChartSeries(points));
    const first = chart.querySelector('path[data-chart-series="first"]');
    const second = chart.querySelector('path[data-chart-series="second"]');
    expect(first?.getAttribute('d')?.match(/M /g)).toHaveLength(2);
    expect(second?.getAttribute('d')?.match(/M /g)).toHaveLength(1);
    expect(chart.querySelectorAll('[data-chart-point-series="first"]')).toHaveLength(2);
    expect(chart.querySelectorAll('[data-chart-point-series="second"]')).toHaveLength(3);
  });

  it('DLS-SAFE-009 groups chart series deterministically and lists reusable class names', () => {
    const points = [
      { x: '2026-08-29', y: 3, color: 'fail' },
      { x: '2026-08-28', y: 2, color: 'pass' },
      { x: '2026-08-30', y: 1, color: 'fail' },
      { x: '2026-08-27', y: 4, color: null }
    ];

    expect(groupChartSeries(points)).toEqual([
      ['fail', [points[0], points[2]]],
      ['pass', [points[1]]],
      ['value', [points[3]]]
    ]);
    expect(listChartSeries(points)).toEqual([
      { name: 'fail', className: 'chart-series-10 chart-series-semantic-failure' },
      { name: 'pass', className: 'chart-series-1 chart-series-semantic-success' },
      { name: 'value', className: 'chart-series-1' }
    ]);

    const reordered = listChartSeries([points[1], points[0]]);
    const filtered = listChartSeries([points[1]]);
    expect(reordered.find(({ name }) => name === 'pass')?.className)
      .toBe(filtered[0].className);
    expect(reordered.find(({ name }) => name === 'fail')?.className)
      .toBe(listChartSeries([points[0]])[0].className);
  });

  it('assigns color-blind-safe semantic colors before falling back to the palette', () => {
    expect(chartSeriesClassName('startup-failure', 0)).toContain('chart-series-semantic-failure');
    expect(chartSeriesClassName('timed_out', 1)).toContain('chart-series-semantic-failure');
    expect(chartSeriesClassName('Success', 2)).toContain('chart-series-semantic-success');
    expect(chartSeriesClassName('waiting for approval', 3)).toContain('chart-series-semantic-waiting');
    expect(chartSeriesClassName('action_required', 4)).toContain('chart-series-semantic-waiting');
    expect(chartSeriesClassName('in-progress', 5)).toContain('chart-series-semantic-waiting');
    expect(chartSeriesClassName('cancelled', 6)).toContain('chart-series-semantic-attention');
    expect(chartSeriesClassName('skipped', 7)).toContain('chart-series-semantic-neutral');
    expect(chartSeriesClassName('0', 9)).toContain('chart-series-semantic-failure');
    expect(chartSeriesClassName('false', 10)).toContain('chart-series-semantic-failure');
    expect(chartSeriesClassName('no', 11)).toContain('chart-series-semantic-failure');
  });

  it('assigns stable palette colors to existing and new category identities', () => {
    expect(chartSeriesClassName('githubnext/gh-cao', 0))
      .toBe(chartSeriesClassName('githubnext/gh-cao', 9));
    expect(chartSeriesClassName('New Campaign', 1))
      .toBe(chartSeriesClassName(' new campaign ', 7));
    expect(chartSeriesClassName('Cafe\u0301', 0)).toBe('chart-series-2');
    expect(chartSeriesClassName('Café', 11)).toBe('chart-series-2');
    expect(chartSeriesClassName('value', 3)).toBe(chartSeriesClassName('value', 9));
    expect(chartSeriesClassName('value', 3)).toBe('chart-series-1');
  });

  it('DLS-SAFE-009 renders reusable visual chart legends', () => {
    const legend = renderChartLegend([
      { name: 'fail', className: 'chart-series-1' },
      { name: 'pass', className: 'chart-series-2' }
    ], 'line');

    expect(legend.className).toBe('chart-legend chart-legend-line');
    expect(legend.getAttribute('data-chart-legend')).toBe('visual');
    expect(legend.querySelectorAll('li')).toHaveLength(2);
    expect(legend.textContent).toContain('fail');
    expect(legend.textContent).toContain('pass');
  });

  it('explains scatter chart scale guides in the legend', () => {
    const legend = renderChartLegend([
      { name: 'core · app · max 5000', className: 'chart-series-1' }
    ], 'scatter');

    expect(legend.querySelectorAll('li')).toHaveLength(2);
    expect(legend.querySelector('li:last-child i')?.className).toBe('chart-grid-key');
    expect(legend.querySelector('li:last-child')?.textContent).toBe('Dashed lines show scale guides');
  });

  it('DLS-SAFE-009 summarizes pie-chart entries and omits non-positive totals', () => {
    const summary = pieChartEntries([
      { x: 'open', y: 3, color: null },
      { x: 'closed', y: 0, color: null },
      { x: 'open', y: 2, color: null },
      { x: 'missing', y: -1, color: null }
    ]);

    expect(summary).toEqual({
      entries: [['open', 5]],
      total: 5
    });
  });

  it('DLS-SAFE-009 renders reusable pie legends including zero-total fallback percentages', () => {
    const links = new Map([['open', {
      href: 'https://github.com/octo-org/open',
      label: 'View octo-org/open on GitHub'
    }]]);
    const populated = renderPieLegend([
      ['open', 3],
      ['closed', 2]
    ], 5, links);
    const empty = renderPieLegend([], 0);

    expect(populated.className).toBe('chart-legend chart-legend-pie');
    expect(populated.querySelectorAll('li')).toHaveLength(2);
    expect(populated.textContent).toContain('open');
    expect(populated.textContent).toContain('3');
    expect(populated.textContent).toContain('60.0%');
    expect(populated.querySelector('a')?.getAttribute('href')).toBe('https://github.com/octo-org/open');
    expect(populated.querySelector('a')?.getAttribute('aria-label')).toBe('View octo-org/open on GitHub');
    expect(empty.querySelectorAll('li')).toHaveLength(0);

    const semantic = renderPieLegend([
      ['failure', 1],
      ['success', 2],
      ['waiting for approval', 3]
    ], 6);
    expect([...semantic.querySelectorAll('li span')].map((item) => item.textContent))
      .toEqual(['waiting for approval', 'success', 'failure']);
    expect(semantic.querySelector('li:nth-child(1) i')?.classList.contains('chart-series-semantic-waiting')).toBe(true);
    expect(semantic.querySelector('li:nth-child(2) i')?.classList.contains('chart-series-semantic-success')).toBe(true);
    expect(semantic.querySelector('li:nth-child(3) i')?.classList.contains('chart-series-semantic-failure')).toBe(true);
  });

  it('uses the same identity color for pie marks, ranked legends, bars, and lines', () => {
    /** @type {Array<[string, number]>} */
    const entries = [['SelfCare', 2], ['Dependabot', 5]];
    const chart = renderChartWidget('pie', [], [], { entries, total: 7 });
    const legend = renderPieLegend(entries, 7);

    for (const [label] of entries) {
      const markClass = chart.querySelector(`[data-chart-category="${label}"]`)?.getAttribute('class');
      const swatchClass = [...legend.querySelectorAll('li')]
        .find((item) => item.querySelector('span')?.textContent === label)
        ?.querySelector('i')?.className;
      expect(swatchClass).toBe(markClass?.replace('pie-chart-segment ', ''));
    }

    const points = [
      { x: '2026-09-26', y: 2, color: 'SelfCare' },
      { x: '2026-09-27', y: 3, color: 'SelfCare' }
    ];
    const expectedClass = chartSeriesClassName('SelfCare', 0);
    const series = listChartSeries(points);
    const bar = renderChartWidget('bar', points, series);
    const line = renderChartWidget('line', points, series);
    expect(bar.querySelector('.bar-chart-bar')?.getAttribute('class')).toContain(expectedClass);
    expect(line.querySelector('.line-chart-series')?.getAttribute('class')).toContain(expectedClass);
  });

  it('formats unitless pie counters for quick scanning', () => {
    /** @type {Array<[string, number]>} */
    const entries = [['blocked.example', 3_177_281]];
    const legend = renderPieLegend(entries, 3_177_281);
    const chart = renderChartWidget('pie', [], [], { entries, total: 3_177_281 });

    expect(legend.querySelector('strong')?.textContent).toBe('3,177,281');
    expect(chart.querySelector('.pie-chart-total-value')?.textContent).toBe('3,177,281');
    expect(chart.querySelector('.pie-chart-total-value')?.getAttribute('textLength')).toBe('21');
    expect(chart.querySelector('.pie-chart-total-value')?.getAttribute('lengthAdjust')).toBe('spacingAndGlyphs');
    expect(chart.querySelector('svg')?.getAttribute('aria-label')).toContain('blocked.example 3,177,281');
  });

  it('constrains long pie labels to the center of the chart', () => {
    const chart = renderChartWidget('pie', [], [], {
      entries: [['blocked.example', 6_848_298]],
      total: 6_848_298
    }, 'Blocked requests');

    expect(chart.querySelector('.pie-chart-total-label')?.getAttribute('textLength')).toBe('21');
    expect(chart.querySelector('.pie-chart-total-label')?.getAttribute('lengthAdjust')).toBe('spacingAndGlyphs');
  });

  it('toggles the pie table when the chart is tapped', () => {
    const chart = renderChartWidget('pie', [], [], {
      entries: [['first', 2], ['largest', 5]],
      total: 7
    });
    const layout = renderPieChartLayout(chart, renderPieLegend([['first', 2], ['largest', 5]], 7));
    const toggle = layout.querySelector('button');

    expect(toggle?.getAttribute('aria-controls')).toBe(layout.querySelector('.chart-legend-pie')?.id);
    expect(toggle?.getAttribute('aria-expanded')).toBe('true');
    toggle?.click();
    expect(layout.hasAttribute('data-chart-table-hidden')).toBe(true);
    expect(toggle?.getAttribute('aria-expanded')).toBe('false');
    expect(toggle?.getAttribute('aria-label')).toBe('Show chart table');
    toggle?.click();
    expect(layout.hasAttribute('data-chart-table-hidden')).toBe(false);
  });

  it('stops reacting to toggle clicks once the pie layout detaches from the document', async () => {
    const chart = renderChartWidget('pie', [], [], {
      entries: [['first', 2], ['largest', 5]],
      total: 7
    });
    const layout = renderPieChartLayout(chart, renderPieLegend([['first', 2], ['largest', 5]], 7));
    document.body.append(layout);
    const toggle = /** @type {HTMLButtonElement} */ (layout.querySelector('button'));
    // Let the MutationObserver backing createFactoryScope observe the initial attach.
    await new Promise((resolve) => queueMicrotask(() => resolve(undefined)));

    layout.remove();
    // Let the MutationObserver microtask backing createFactoryScope run.
    await new Promise((resolve) => queueMicrotask(() => resolve(undefined)));

    toggle.click();

    // The effect stopped when the layout detached, so the click can no longer
    // toggle the now-detached layout's hidden marker or the button's label.
    expect(layout.hasAttribute('data-chart-table-hidden')).toBe(false);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
  });

  it('distinguishes missing chart data from an insufficient sample', () => {
    for (const chartType of ['bar', 'histogram', 'line', 'pie', 'scatter']) {
      const chart = renderChartWidget(chartType, [], []);

      expect(chart.getAttribute('data-chart-widget')).toBe(chartType);
      expect(chart.querySelector('svg')).toBeNull();
      expect(chart.querySelector('[role="status"]')?.textContent).toBe('No data is available for this visualization.');
    }

    const point = [{ x: 'only', y: 1, color: null }];
    const bar = renderChartWidget('bar', point, listChartSeries(point));
    expect(bar.querySelector('[role="status"]')).toBeNull();
    expect(bar.querySelector('rect[role="img"]')).not.toBeNull();

    for (const chartType of ['histogram', 'line']) {
      const chart = renderChartWidget(chartType, point, listChartSeries(point));
      expect(chart.querySelector('[role="status"]')?.textContent).toBe('Not enough data to show this visualization.');
    }
  });

  it('renders a single-category pie distribution', () => {
    const singleCategoryPie = renderChartWidget('pie', [
      { x: 'only', y: 1, color: null },
      { x: 'only', y: 2, color: null }
    ], []);
    expect(singleCategoryPie.querySelector('[role="status"]')).toBeNull();
    expect(singleCategoryPie.querySelector('svg')).not.toBeNull();
    expect(singleCategoryPie.querySelector('[data-chart-category="only"]')).not.toBeNull();
    expect(singleCategoryPie.querySelector('.pie-chart-total-value')?.textContent).toBe('3');
  });

  it('renders compact heatmaps as accessible labeled SVG rect marks without relying on color', () => {
    const chart = renderChartWidget('heatmap', [
      { x: 'build', y: 62, color: 'ubuntu', source: {} },
      { x: 'test', y: 125, color: 'ubuntu', source: {} },
      { x: 'build', y: 80, color: 'macos', source: {} }
    ], [], null, 'Mean job time', {
      name: 'Seconds',
      symbol: 's',
      significant: 1
    });

    expect(chart.getAttribute('data-chart-widget')).toBe('heatmap');
    expect(chart.querySelector('.heatmap-chart')?.tagName).toBe('svg');
    expect(chart.querySelector('.heatmap-chart')?.getAttribute('aria-label')).toBe('Heatmap of Mean job time');
    expect([...chart.querySelectorAll('.heatmap-column-label')].map((label) => label.lastChild?.textContent)).toEqual(['build', 'test']);
    expect([...chart.querySelectorAll('.heatmap-row-label')].map((label) => label.lastChild?.textContent)).toEqual(['macos', 'ubuntu']);
    expect(chart.querySelectorAll('.heatmap-cell')).toHaveLength(4);
    expect([...chart.querySelectorAll('.heatmap-cell[tabindex="0"]')].map((cell) => cell.getAttribute('aria-label'))).toContain('build, ubuntu, Mean job time: 62 s');
    expect(chart.querySelector('.heatmap-cell-empty')?.getAttribute('aria-label')).toBe('test, macos: no observation');
    expect(chart.querySelector('.heatmap-cell-empty')?.getAttribute('tabindex')).toBe('0');
    expect(chart.querySelectorAll('.heatmap-cell rect')).toHaveLength(4);
    expect([...chart.querySelectorAll('.heatmap-cell text')].map((cell) => cell.textContent)).toContain('62 s');
  });

  it('renders bounded horizontal bars with labels on the left and shared bar colors', () => {
    const points = [
      { x: 'Alpha repository', y: 40, color: 'success', link: { href: '#page-alpha', label: 'View Alpha repository' } },
      { x: 'Beta repository', y: 20, color: 'failure' }
    ];
    const chart = renderChartWidget('horizontal-bar', points, listChartSeries(points));

    expect(chart.getAttribute('data-chart-widget')).toBe('horizontal-bar');
    expect(chart.getAttribute('role')).toBeNull();
    expect(chart.querySelector('.horizontal-bar-chart-list')?.getAttribute('aria-label')).toBe('Horizontal bar chart with 2 bars');
    expect([...chart.querySelectorAll('.horizontal-bar-chart-label')].map((label) => label.textContent))
      .toEqual(['Alpha repository', 'Beta repository']);
    expect(chart.querySelector('.horizontal-bar-chart-label a')?.getAttribute('href')).toBe('#page-alpha');
    const tracks = chart.querySelectorAll('.horizontal-bar-chart-track');
    expect(tracks[0].tagName).toBe('A');
    expect(tracks[0].getAttribute('href')).toBe('#page-alpha');
    expect(tracks[0].getAttribute('aria-label')).toContain('Alpha repository: 40');
    expect(tracks[0].getAttribute('aria-hidden')).toBeNull();
    expect(tracks[0].getAttribute('target')).toBeNull();
    expect(tracks[1].tagName).toBe('SPAN');
    expect(tracks[1].getAttribute('aria-hidden')).toBe('true');
    expect(chart.querySelectorAll('.bar-chart-bar')).toHaveLength(2);
    expect(chart.querySelector('.horizontal-bar-chart-bar')?.getAttribute('style')).toContain('--horizontal-bar-size: 100%');
    expect([...chart.querySelectorAll('.horizontal-bar-chart-value')].map((value) => value.textContent))
      .toEqual(['40', '20']);
    expect(chart.querySelector('.horizontal-bar-chart-value')?.getAttribute('aria-label'))
      .toContain('Alpha repository: 40');
  });

  it('distinguishes unavailable horizontal-bar observations from recorded zero costs', () => {
    const points = [
      { x: 'Missing telemetry', y: null, color: null },
      { x: 'Recorded zero', y: 0, color: null },
      { x: 'Recorded cost', y: 125, color: null }
    ];
    const chart = renderChartWidget('horizontal-bar', points, listChartSeries(points), null, 'AIC', {
      name: 'aic', symbol: 'AIC', significant: 0.01, format: 'aicc'
    });
    expect([...chart.querySelectorAll('.horizontal-bar-chart-value')].map((value) => value.textContent))
      .toEqual(['Unavailable', '$0.00', '$1.25']);
    expect(chart.querySelector('.horizontal-bar-chart-value')?.getAttribute('aria-label'))
      .toBe('Missing telemetry: Unavailable');
  });

  it('groups horizontal bars under section headings in first-appearance order', () => {
    const points = [
      { x: 'deploy.md', y: 40, color: null, section: 'octo/app' },
      { x: 'release.md', y: 30, color: null, section: 'octo/tools' },
      { x: 'test.md', y: 20, color: null, section: 'octo/app' }
    ];
    const chart = renderChartWidget('horizontal-bar', points, listChartSeries(points));

    expect([...chart.querySelectorAll('.horizontal-bar-chart-section-title')].map((heading) => heading.textContent))
      .toEqual(['octo/app', 'octo/tools']);
    expect([...chart.querySelectorAll('.horizontal-bar-chart-section-list')].map((list) => list.getAttribute('aria-label')))
      .toEqual(['octo/app', 'octo/tools']);
    expect([...chart.querySelectorAll('.horizontal-bar-chart-section:first-child .horizontal-bar-chart-label')]
      .map((label) => label.textContent))
      .toEqual(['deploy.md', 'test.md']);
  });

  it('formats linked horizontal bar categories without changing their destinations', () => {
    const points = [{
      x: 'githubnext/gh-cao:.github/workflows/optimization-agents-and-curator.md',
      y: 40,
      color: null,
      link: { href: '#page-workflow-runtime?workflow=optimization', label: 'View workflow dashboard' }
    }];
    const chart = renderChartWidget(
      'horizontal-bar',
      points,
      listChartSeries(points),
      null,
      'Total',
      null,
      null,
      null,
      (label) => label.replace('.github/workflows/', '').replace(':', ' (') + ')'
    );

    expect(chart.querySelector('.horizontal-bar-chart-label a')?.getAttribute('href'))
      .toBe('#page-workflow-runtime?workflow=optimization');
    expect(chart.querySelector('.horizontal-bar-chart-label-text')?.textContent)
      .toBe('githubnext/gh-cao (optimization-agents-and-curator.md)');
  });

  it('preserves complete horizontal bar labels with shared prefixes', () => {
    const points = [
      {
        x: 'octo/app:.github/workflows/alpha-shared-health.md',
        y: 25,
        color: 'success',
        link: { href: '#page-alpha', label: 'View workflow dashboard' }
      },
      {
        x: '.github/workflows/beta-shared-health.md',
        y: 13,
        color: 'failure',
        section: 'octo/tools'
      }
    ];
    const chart = renderChartWidget('horizontal-bar', points, listChartSeries(points));
    const labels = [...chart.querySelectorAll('.horizontal-bar-chart-label')];
    const links = [...chart.querySelectorAll('.horizontal-bar-chart-label a')];

    expect(labels.map((label) => label.textContent)).toEqual(points.map((point) => point.x));
    expect(labels.map((label) => label.getAttribute('title'))).toEqual([
      'octo/app:.github/workflows/alpha-shared-health.md',
      'octo/tools:.github/workflows/beta-shared-health.md'
    ]);
    expect(links[0]?.getAttribute('title')).toBe('octo/app:.github/workflows/alpha-shared-health.md');
    expect(links[0]?.getAttribute('aria-label'))
      .toBe('View workflow dashboard: octo/app:.github/workflows/alpha-shared-health.md');
    expect(labels[0]?.hasAttribute('tabindex')).toBe(false);
    expect(labels[1]?.getAttribute('tabindex')).toBe('0');
    expect(labels[1]?.getAttribute('aria-label')).toBe('octo/tools:.github/workflows/beta-shared-health.md');
    expect(chart.querySelector('.horizontal-bar-chart-value')?.getAttribute('aria-label'))
      .toBe('octo/app:.github/workflows/alpha-shared-health.md: 25, success');
  });

  it('preserves negative horizontal bar values while keeping bars left aligned', () => {
    const points = [
      { x: 'Gain', y: 10, color: null },
      { x: 'Loss', y: -5, color: null }
    ];
    const chart = renderChartWidget('horizontal-bar', points, listChartSeries(points));

    expect([...chart.querySelectorAll('.horizontal-bar-chart-value')].map((value) => value.textContent))
      .toEqual(['10', '-5']);
    expect([...chart.querySelectorAll('.horizontal-bar-chart-bar')][1]?.getAttribute('style'))
      .toContain('--horizontal-bar-size: 0%');
  });

  it('rejects horizontal bar chart payloads larger than 100 rows', () => {
    const points = Array.from({ length: 101 }, (_, index) => ({
      x: `item-${index}`,
      y: index,
      color: null
    }));
    const chart = renderChartWidget('horizontal-bar', points, listChartSeries(points));

    expect(chart.querySelector('.horizontal-bar-chart-list')).toBeNull();
    expect(chart.querySelector('[role="status"]')?.textContent).toBe('Horizontal bar charts support at most 100 bars.');
  });

  it('rejects oversized heatmaps with a visible status instead of rendering a dense matrix', () => {
    const points = Array.from({ length: 13 }, (_, index) => ({
      x: `job-${index}`,
      y: index,
      color: 'ubuntu'
    }));
    const chart = renderChartWidget('heatmap', points, []);

    expect(chart.querySelector('.heatmap-chart')).toBeNull();
    expect(chart.querySelector('[role="status"]')?.textContent).toContain('12 categories per axis');
  });

  it('renders categorical workflow runs as accessible swimlanes without connecting marks', () => {
    const points = [
      {
        x: '2026-08-28T08:00:00Z',
        y: Number.NaN,
        category: 'action-required',
        color: 'action-required',
        source: { run: '1840', 'started-at': '2026-08-28T08:00:00Z', 'ended-at': '2026-08-28T08:03:18Z' }
      },
      {
        x: '2026-08-29T12:48:37Z',
        y: Number.NaN,
        category: 'failure',
        color: 'failure',
        source: { run: '1842', 'started-at': '2026-08-29T12:48:37Z', 'ended-at': '2026-08-29T12:51:55Z', branch: 'main' }
      },
      { x: '2026-08-30T08:00:00Z', y: Number.NaN, category: 'cancelled', color: 'cancelled', source: {} },
      { x: '2026-08-30T12:00:00Z', y: Number.NaN, category: 'skipped', color: 'skipped', source: {} },
      { x: '2026-08-31T08:00:00Z', y: Number.NaN, category: 'success', color: 'success', source: {} }
    ];
    const chart = renderChartWidget(
      'swimlane',
      points,
      listChartSeries(points),
      null,
      'Total',
      null,
      { start: '2026-08-28T00:00:00Z', end: '2026-09-01T00:00:00Z' }
    );

    expect(chart.getAttribute('data-chart-widget')).toBe('swimlane');
    expect(chart.querySelector('svg')?.getAttribute('viewBox')).toBe(`0 0 ${SWIMLANE_LAYOUT.viewBoxWidth} ${SWIMLANE_LAYOUT.viewBoxHeight}`);
    expect(chart.querySelectorAll('.swimlane-label')).toHaveLength(5);
    expect([...chart.querySelectorAll('.swimlane-label')].map((label) => label.textContent)).toEqual([
      'Action required',
      'Failure',
      'Cancelled',
      'Skipped',
      'Success'
    ]);
    expect([...chart.querySelectorAll('.swimlane-label')].map((label) => Number(label.getAttribute('x')))).toEqual(Array(5).fill(SWIMLANE_LAYOUT.labelX));
    expect([...chart.querySelectorAll('.swimlane-label')].map((label) => Number(label.getAttribute('y')))).toEqual(
      Array.from({ length: 5 }, (_, index) => SWIMLANE_LAYOUT.topY + (index * SWIMLANE_LAYOUT.laneGap) + SWIMLANE_LAYOUT.labelBaselineOffset)
    );
    const laneYs = [...chart.querySelectorAll('.swimlane-separator')].map((separator) => Number(separator.getAttribute('y1')));
    expect(laneYs[0]).toBe(SWIMLANE_LAYOUT.topY);
    for (let index = 1; index < laneYs.length; index += 1) {
      expect(laneYs[index] - laneYs[index - 1]).toBeCloseTo(SWIMLANE_LAYOUT.laneGap);
    }
    expect(Number(chart.querySelector('.swimlane-axis')?.getAttribute('y1'))).toBe(SWIMLANE_LAYOUT.axisY);
    expect([...chart.querySelectorAll('.swimlane-tick')].map((tick) => Number(tick.getAttribute('y2'))))
      .toEqual(Array(4).fill(SWIMLANE_LAYOUT.tickEndY));
    expect([...chart.querySelectorAll('.swimlane-time-label')].map((label) => Number(label.getAttribute('y'))))
      .toEqual(Array(4).fill(SWIMLANE_LAYOUT.axisLabelY));
    expect(chart.querySelectorAll('.swimlane-mark')).toHaveLength(5);
    expect(chart.querySelector('polyline')).toBeNull();
    expect(chart.querySelector('.swimlane-summary')?.textContent).toContain('5 runs');
    expect(chart.querySelector('.swimlane-summary')?.textContent).toContain('20.0% success');
    expect(chart.querySelector('.swimlane-mark-failure')?.getAttribute('aria-label')).toContain('Conclusion: failure');
    expect(chart.querySelector('.swimlane-mark-failure')?.getAttribute('aria-label')).toContain('Run #1842');
    expect(chart.querySelector('.swimlane-mark-failure')?.getAttribute('aria-label')).toContain('Branch: main');
    expect(chart.querySelector('.swimlane-mark-failure')?.getAttribute('aria-label')).toContain('Duration: 3m 18s');
    expect(chart.querySelectorAll('.swimlane-mark > *')).toHaveLength(0);
    expect(chart.querySelector('[data-chart-axis="swimlane"]')).toBeNull();
    expect(chart.textContent).toContain('Aug 28');
  });

  it('renders arbitrary workflow categories as lanes colored by run conclusion', () => {
    const points = [
      {
        x: '2026-08-28T08:00:00Z',
        y: Number.NaN,
        category: 'doctor.md (githubnext/gh-cao)',
        color: 'success',
        source: { run: '1840' }
      },
      {
        x: '2026-08-28T08:00:00Z',
        y: Number.NaN,
        category: 'doctor.md (githubnext/gh-cao)',
        color: 'failure',
        source: { run: '1841' }
      },
      {
        x: '2026-08-30T08:00:00Z',
        y: Number.NaN,
        category: 'audit.md (githubnext/control-plane)',
        color: 'cancelled',
        source: { run: '1842' }
      }
    ];

    const chart = renderChartWidget('swimlane', points, listChartSeries(points));

    expect([...chart.querySelectorAll('.swimlane-label')].map((label) => label.textContent)).toEqual([
      'doctor.md (githubnext/gh-cao)',
      'audit.md (githubnext/control-plane)'
    ]);
    expect(chart.querySelectorAll('.swimlane-run-mark')).toHaveLength(3);
    expect(chart.querySelectorAll('.swimlane-mark-success')).toHaveLength(1);
    expect(chart.querySelectorAll('.swimlane-mark-failure')).toHaveLength(1);
    expect(chart.querySelectorAll('.swimlane-mark-cancelled')).toHaveLength(1);
    expect([...chart.querySelectorAll('[data-swimlane-count]')].map((mark) => mark.getAttribute('data-swimlane-count')))
      .toEqual(['1', '1', '1']);
    expect(chart.querySelector('.swimlane-mark-failure')?.getAttribute('aria-label'))
      .toContain('Lane: doctor.md (githubnext/gh-cao)');
  });

  it('renders 100,000 swimlane observations as bounded line intervals', () => {
    const start = Date.parse('2026-08-01T00:00:00Z');
    const points = Array.from({ length: 100_000 }, (_, index) => {
      const interval = Math.floor((index * 120) / 100_000);
      const lane = ['success', 'failure', 'skipped', 'cancelled', 'action-required'][interval % 5];
      return {
        x: new Date(start + index).toISOString(),
        y: Number.NaN,
        category: lane,
        color: lane,
        source: { run: String(index) }
      };
    });
    const startedAt = performance.now();
    const chart = renderChartWidget('swimlane', points, [], null, 'Total', null, {
      start: new Date(start).toISOString(),
      end: new Date(start + points.length).toISOString()
    });
    const elapsedMilliseconds = performance.now() - startedAt;
    const marks = chart.querySelectorAll('.swimlane-mark');
    expect(elapsedMilliseconds).toBeLessThan(1_000);
    expect(elapsedMilliseconds).toBeLessThan(1_000);
    expect(marks.length).toBeLessThanOrEqual(600);
    expect([...marks].every((mark) => mark.tagName === 'line')).toBe(true);
    expect([...marks].every((mark) => mark.tagName === 'line')).toBe(true);
    expect([...marks].reduce((total, mark) => total + Number(mark.getAttribute('data-swimlane-count')), 0)).toBe(100_000);
  });

  it('renders one swimlane observation without applying the multi-point chart empty state', () => {
    const chart = renderChartWidget('swimlane', [{
      x: '2026-08-31T12:48:37Z',
      y: Number.NaN,
      category: 'success',
      color: 'success',
      source: { run: '1842' }
    }], []);

    expect(chart.querySelectorAll('.swimlane-mark')).toHaveLength(1);
    expect(chart.querySelector('[role="status"]')).toBeNull();
  });

  it('uses declarative weights for daily aggregate swimlane observations', () => {
    const chart = renderChartWidget('swimlane', [
      {
        x: '2026-08-30',
        y: Number.NaN,
        weight: 40,
        category: 'success',
        color: 'success',
        source: {}
      },
      {
        x: '2026-08-30',
        y: Number.NaN,
        weight: 10,
        category: 'failure',
        color: 'failure',
        source: {}
      }
    ], []);

    expect(chart.querySelector('.swimlane-summary')?.textContent).toContain('50 runs');
    expect(chart.querySelector('.swimlane-summary')?.textContent).toContain('80.0% success');
    expect([...chart.querySelectorAll('.swimlane-mark')]
      .reduce((total, mark) => total + Number(mark.getAttribute('data-swimlane-count')), 0)).toBe(50);
  });

  it('renders an empty swimlane without invalid timeline dates', () => {
    const chart = renderChartWidget('swimlane', [], []);

    expect(chart.getAttribute('data-chart-widget')).toBe('swimlane');
    expect(chart.querySelector('[role="status"]')?.textContent).toBe('No workflow runs to show.');
    expect(chart.querySelector('svg')).toBeNull();
  });

  it('DLS-VIEW-005 DLS-VIEW-006 DLS-VIEW-007 renders JSON-selected chart marks through one generic helper', () => {
    const points = [
      { x: '2026-08-29', y: 3, color: 'success' },
      { x: '2026-08-30', y: 1, color: 'failure' }
    ];
    const series = listChartSeries(points);

    const bar = renderChartWidget('bar', [...points, { x: 'invalid', y: Number.NaN, color: null }], series);
    const line = renderChartWidget('line', points, series);
    const pie = renderChartWidget('pie', points, series);
    const histogram = renderChartWidget('histogram', [
      { x: 'run-1', y: 3, color: null },
      { x: 'run-2', y: 6, color: null },
      { x: 'run-3', y: 9, color: null },
      { x: 'run-4', y: 9, color: null }
    ], series, null, 'AIC per run', {
      name: 'AI Credits',
      symbol: 'AIC',
      significant: 1
    });
    const unitPie = renderChartWidget('pie', points, series, null, 'Total', {
      name: 'AI Credits',
      symbol: 'AIC',
      significant: 1
    });
    const fullPie = renderChartWidget('pie', [], [], {
      entries: [['success', 4], ['failure', 0]],
      total: 4
    });
    const chartHeight = String(38 - 4);

    expect(bar.getAttribute('data-chart-widget')).toBe('bar');
    expect(bar.querySelectorAll('.bar-chart-bar')).toHaveLength(3);
    expect(bar.querySelector('.bar-chart-bar')?.getAttribute('height')).toBe(chartHeight);
    expect(bar.querySelector('.bar-chart-bar')?.getAttribute('rx')).toBe('0.75');
    expect(bar.querySelector('.bar-chart-bar')?.getAttribute('style')).toContain('--chart-entry-index: 0');
    expect([...bar.querySelectorAll('.bar-chart-bar')].at(-1)?.getAttribute('height')).toBe('1');
    expect([...bar.querySelectorAll('.bar-chart-y-axis text')].map((tick) => tick.textContent)).toEqual(['3', '1.50', '0']);
    expect([...bar.querySelectorAll('.bar-chart-x-axis text')].map((tick) => tick.lastChild?.textContent)).toEqual([
      'Aug 29',
      'Aug 30',
      'invalid'
    ]);
    expect(bar.querySelectorAll('.bar-chart-grid')).toHaveLength(3);
    expect(bar.querySelector('[data-chart-axis="x"]')).not.toBeNull();
    expect(bar.querySelector('[data-chart-axis="y"]')).not.toBeNull();
    expect(line.getAttribute('data-chart-widget')).toBe('line');
    expect(line.querySelectorAll('.line-chart-series')).toHaveLength(2);
    expect(line.querySelector('.line-chart-series')?.getAttribute('pathLength')).toBe('1');
    expect(line.querySelector('.line-chart-series')?.getAttribute('style')).toContain('--chart-entry-index: 0');
    expect(line.querySelector('.line-chart-point')?.getAttribute('style')).toContain('--chart-point-size: 6px');
    expect(line.querySelector('.chart-point')?.getAttribute('style')).toContain('--chart-entry-index: 0');
    expect([...line.querySelectorAll('.line-chart-y-labels span')].map((tick) => tick.textContent)).toEqual(['3', '1.50', '0']);
    expect(line.querySelector('[data-chart-axis="y"]')).not.toBeNull();
    expect([...line.querySelectorAll('.timeline-chart-axis span')].map((tick) => tick.textContent)).toEqual([
      'Aug 29',
      'Aug 30'
    ]);
    expect(pie.getAttribute('data-chart-widget')).toBe('pie');
    expect(pie.querySelectorAll('.pie-chart-segment')).toHaveLength(2);
    expect(pie.querySelector('.pie-chart-mark')?.getAttribute('style')).toContain('--chart-entry-index: 0');
    expect(pie.querySelector('.pie-chart-track')?.getAttribute('stroke-width')).toBe('6');
    expect(pie.querySelector('.pie-chart-segment')?.tagName).toBe('path');
    expect(pie.querySelector('.pie-chart-segment')?.getAttribute('stroke-width')).toBe('6');
    expect(pie.querySelector('.pie-chart-segment')?.getAttribute('stroke-linecap')).toBe('round');
    expect([...pie.querySelectorAll('.pie-chart-segment')].map((segment) => segment.getAttribute('d'))).toEqual([
      'M 23.9823 5.3664 A 15.9155 15.9155 0 1 1 5.3664 23.9823',
      'M 5.3664 18.0177 A 15.9155 15.9155 0 0 1 18.0177 5.3664'
    ]);
    expect(pie.querySelector('.pie-chart-segment')?.hasAttribute('stroke-dasharray')).toBe(false);
    expect(pie.querySelectorAll('.pie-chart-mark .point-tooltip')).toHaveLength(2);
    expect(pie.querySelector('.pie-chart-mark')?.getAttribute('aria-label')).toBe('2026-08-29: 3');
    expect(pie.querySelector('.pie-chart-tooltip rect')?.getAttribute('width')).toBe('21.25');
    const firstPieMark = pie.querySelector('.pie-chart-mark');
    firstPieMark?.dispatchEvent(new Event('pointerenter'));
    expect(pie.querySelector('.pie-chart-mark:last-child')).toBe(firstPieMark);
    expect(histogram.getAttribute('data-chart-widget')).toBe('histogram');
    expect(histogram.querySelectorAll('.histogram-chart-bar')).toHaveLength(3);
    expect(histogram.querySelector('.histogram-chart-mark')?.getAttribute('style')).toContain('--chart-entry-index: 0');
    expect(histogram.querySelectorAll('.histogram-chart-mark .point-tooltip')).toHaveLength(3);
    expect(histogram.querySelectorAll('.histogram-chart-grid')).toHaveLength(3);
    expect([...histogram.querySelectorAll('.histogram-chart-y-label')].map((tick) => tick.textContent)).toEqual([
      '2',
      '1',
      '0'
    ]);
    expect([...histogram.querySelectorAll('[data-chart-axis="histogram"] span')].map((tick) => tick.textContent)).toEqual([
      '3 AIC',
      '5 AIC',
      '7 AIC',
      '9 AIC'
    ]);
    expect(histogram.querySelector('.histogram-chart-bar')?.classList.contains('chart-series-1')).toBe(true);
    expect(histogram.querySelector('.histogram-chart-bar')?.getAttribute('rx')).toBe('0.75');
    expect(histogram.querySelector('.histogram-chart-tooltip text')?.getAttribute('lengthAdjust')).toBe('spacingAndGlyphs');
    expect(histogram.querySelector('svg')?.getAttribute('aria-label')).toContain('automatically calculated bins');
    expect(histogram.querySelector('.histogram-chart-mark')?.getAttribute('aria-label')).toContain('AIC');
    const firstHistogramMark = histogram.querySelector('.histogram-chart-mark');
    firstHistogramMark?.dispatchEvent(new Event('pointerenter'));
    expect(histogram.querySelector('.histogram-chart-mark:last-child')).toBe(firstHistogramMark);
    expect(unitPie.querySelector('.pie-chart-mark')?.getAttribute('aria-label')).toBe('2026-08-29: 3 AIC');
    expect(unitPie.querySelector('.pie-chart-total-value')?.textContent).toBe('4');
    const fullPieSegments = fullPie.querySelectorAll('.pie-chart-segment');
    expect(fullPieSegments[0]?.getAttribute('d')?.match(/ A /g)).toHaveLength(2);
    expect(fullPieSegments[1]?.getAttribute('d')).toBe('M 21 5.0845');
  });

  it('compacts large temporal chart y-axis labels within the reserved gutter', () => {
    const chart = renderChartWidget('line', [
      { x: '2026-08-29', y: 1_000_000, color: null },
      { x: '2026-08-30', y: 500_000, color: null }
    ], [{ name: 'value', className: 'chart-series-1' }]);

    expect([...chart.querySelectorAll('.line-chart-y-labels span')].map((tick) => tick.textContent))
      .toEqual(['1M', '500K', '0']);
  });

  it('packs the temporal chart y-axis gutter around its formatted labels', () => {
    const chart = renderChartWidget('line', [
      { x: '2026-09-10', y: 918, color: null },
      { x: '2026-09-11', y: 0, color: null }
    ], [{ name: 'value', className: 'chart-series-1' }]);

    expect(chart.getAttribute('style')).toBe('--line-chart-left: 7%;');
    expect(chart.querySelector('.line-chart-y-axis .line-chart-axis')?.getAttribute('x1')).toBe('7');
    expect(chart.querySelector('.line-chart-grid')?.getAttribute('x1')).toBe('7');
    expect(chart.querySelector('.line-chart-series')?.getAttribute('points')).toBe('7,4 100,38');
  });

  it('accounts for punctuation and wide glyphs when packing the y-axis gutter', () => {
    const points = [
      { x: '2026-09-10', y: 9_999, color: null },
      { x: '2026-09-11', y: 0, color: null }
    ];
    const currencyChart = renderChartWidget('line', points, [], null, 'Total', {
      name: 'US dollars',
      symbol: 'USD',
      significant: 2,
      format: 'usd'
    });
    const unitChart = renderChartWidget('line', points, [], null, 'Total', {
      name: 'Megawatts',
      symbol: 'MW',
      significant: 2
    });
    const longUnitChart = renderChartWidget('line', points, [], null, 'Total', {
      name: 'Long unit',
      symbol: 'W'.repeat(100),
      significant: 2
    });

    expect([...currencyChart.querySelectorAll('.line-chart-y-labels span')].map((tick) => tick.textContent))
      .toEqual(['$9,999.00', '$4,999.50', '$0.00']);
    expect(currencyChart.getAttribute('style')).toBe('--line-chart-left: 15%;');
    expect(unitChart.getAttribute('style')).toBe('--line-chart-left: 15%;');
    expect(longUnitChart.getAttribute('style')).toBe('--line-chart-left: 75%;');
  });

  it('renders area marks and stacks color series over the shared ordered axis', () => {
    const points = [
      { x: '2026-09-01', y: 2, color: 'review' },
      { x: '2026-09-01', y: 3, color: 'triage' },
      { x: '2026-09-02', y: 4, color: 'review' },
      { x: '2026-09-02', y: 1, color: 'triage' }
    ];
    const chart = renderChartWidget('area', points, listChartSeries(points), null, 'AI Credits', {
      name: 'AI Credits',
      symbol: 'AIC',
      significant: 1
    });

    expect(chart.getAttribute('data-chart-widget')).toBe('area');
    expect(chart.querySelector('svg')?.getAttribute('aria-label')).toBe('Stacked area chart with 4 points');
    expect(chart.querySelectorAll('path.area-chart-area')).toHaveLength(2);
    expect(chart.querySelectorAll('.area-chart-point')).toHaveLength(4);
    expect(chart.querySelectorAll('line.area-chart-point')).toHaveLength(4);
    expect(chart.querySelector('.line-chart-y-labels')?.getAttribute('aria-hidden')).toBe('true');
    expect(chart.querySelector('[data-chart-series="review"]')?.getAttribute('d'))
      .toBe('M 10 24.4 L 100 10.8 L 100 38 L 10 38 Z');
    expect(chart.querySelector('[data-chart-series="triage"]')?.getAttribute('d'))
      .toBe('M 10 4 L 100 4 L 100 10.8 L 10 24.4 Z');
    expect(chart.querySelector('.chart-point')?.getAttribute('aria-label')).toContain('2 AIC');
  });

  it('renders temporal dot observations with per-series reference lines', () => {
    const points = [
      { x: '2026-09-04T10:00:00Z', y: 4_900, color: 'core', source: { limit: 5_000 } },
      { x: '2026-09-04T11:00:00Z', y: 4_875, color: 'core', source: { limit: 5_000 } },
      { x: '2026-09-04T11:00:00Z', y: 28, color: 'search', source: { limit: 30 } }
    ];
    const dot = renderChartWidget('dot', points, listChartSeries(points), null, 'Remaining', null, null, 'limit');

    expect(dot.getAttribute('data-chart-widget')).toBe('dot');
    expect(dot.querySelectorAll('.dot-chart-point')).toHaveLength(3);
    expect(dot.querySelectorAll('.dot-chart-reference')).toHaveLength(2);
    expect(dot.querySelector('[data-chart-reference="core"]')?.getAttribute('data-chart-reference-value')).toBe('5000');
    expect(dot.querySelector('.line-chart-series')).toBeNull();
    expect(dot.querySelector('svg')?.getAttribute('aria-label')).toBe('Dot chart with 3 points and 2 reference lines');
  });

  it('renders temporal scatter observations at proportional timestamps without connecting them', () => {
    const points = [
      { x: '2026-09-04T10:00:00Z', y: 98, color: 'max 5000' },
      { x: '2026-09-04T11:00:00Z', y: 50, color: 'max 5000' },
      { x: '2026-09-04T14:00:00Z', y: 10, color: 'max 30' }
    ];
    const scatter = renderChartWidget('scatter', points, listChartSeries(points), null, 'Remaining', {
      name: 'Percent',
      symbol: '%',
      significant: 0.1
    });
    const xCoordinates = [...scatter.querySelectorAll('.scatter-chart-point')]
      .map((point) => Number(point.getAttribute('cx')))
      .sort((left, right) => left - right);

    expect(scatter.getAttribute('data-chart-widget')).toBe('scatter');
    expect(xCoordinates).toEqual([11, 33.25, 100]);
    expect([...scatter.querySelectorAll('.timeline-chart-axis span')].map((tick) => tick.getAttribute('title'))).toEqual([
      '2026-09-04T10:00:00.000Z',
      '2026-09-04T12:00:00.000Z',
      '2026-09-04T14:00:00.000Z'
    ]);
    expect(scatter.querySelector('.line-chart-series')).toBeNull();
    expect(scatter.querySelector('svg')?.getAttribute('aria-label')).toBe('Scatter chart with 3 points');
  });

  it('exposes scatter cluster sizes in accessible point labels', () => {
    const scatter = renderChartWidget('scatter', [{
      x: '2026-09-04T10:00:00Z',
      y: 72,
      color: 'max 5000',
      source: { 'cluster-count': 250 }
    }], [{ name: 'max 5000', className: 'chart-series-1' }], null, 'Remaining', {
      name: 'Percent',
      symbol: '%',
      significant: 0.1
    });

    expect(scatter.querySelector('.chart-point')?.getAttribute('aria-label')).toContain('cluster of 250 observations');
  });

  it('dims historical line context and emphasizes the selected window', () => {
    const points = [
      { x: '2026-09-03T10:00:00Z', y: 2, color: 'worker', highlighted: false },
      { x: '2026-09-04T10:00:00Z', y: 3, color: 'worker', highlighted: true },
      { x: '2026-09-04T11:00:00Z', y: 4, color: 'worker', highlighted: true }
    ];
    const line = renderChartWidget('line', points, listChartSeries(points));

    expect(line.querySelector('.line-chart-context')).not.toBeNull();
    expect(Number(line.querySelector('.line-chart-window-band')?.getAttribute('width'))).toBeGreaterThan(0);
    expect(line.querySelector('.line-chart-current')?.getAttribute('points')).not.toBe('');
    expect(line.querySelectorAll('.chart-point-context')).toHaveLength(1);
    expect(line.querySelector('.chart-point-context .line-chart-point')?.getAttribute('style')).toContain('--chart-point-size: 4px');
    expect(line.querySelector('.chart-point-current .line-chart-point')?.getAttribute('style')).toContain('--chart-point-size: 4px');
    expect(line.querySelector('.chart-window-key')?.textContent).toContain('Selected window');
  });

  it('reduces non-scaling line-chart point sizes as the number of points increases', () => {
    /** @param {number} count */
    const renderPoints = (count) => {
      const points = Array.from({ length: count }, (_, index) => ({
        x: String(index),
        y: index,
        color: null
      }));
      return renderChartWidget('line', points, listChartSeries(points));
    };

    expect(renderPoints(2).querySelector('.line-chart-point')?.getAttribute('style')).toContain('--chart-point-size: 6px');
    expect(renderPoints(51).querySelector('.line-chart-point')?.getAttribute('style')).toContain('--chart-point-size: 4px');
    expect(renderPoints(100).querySelector('.line-chart-point')?.getAttribute('style')).toContain('--chart-point-size: 2px');
    expect(renderPoints(150).querySelector('.line-chart-point')?.getAttribute('style')).toContain('--chart-point-size: 2px');
  });

  it('renders 100,000 line-chart points in bounded time and SVG size', () => {
    const points = Array.from({ length: 100_000 }, (_, index) => ({
      x: new Date(index * 60_000).toISOString(),
      y: index % 1_000,
      color: null
    }));
    const startedAt = performance.now();
    const chart = renderChartWidget('line', points, listChartSeries(points));
    const elapsedMilliseconds = performance.now() - startedAt;
    const renderedPoints = chart.querySelector('.line-chart-series')?.getAttribute('points')?.split(' ') ?? [];

    expect(elapsedMilliseconds).toBeLessThan(1_000);
    expect(chart.getAttribute('data-line-rendering')).toBe('compact');
    expect(renderedPoints.length).toBeLessThanOrEqual(2_000);
    expect(chart.querySelectorAll('.chart-point')).toHaveLength(0);
    expect(chart.querySelectorAll('svg *').length).toBeLessThan(25);
  });

  it('shares the rendered point budget across line series', () => {
    const points = Array.from({ length: 20_000 }, (_, index) => ({
      x: new Date(index * 60_000).toISOString(),
      y: index % 1_000,
      color: `series-${index % 10}`
    }));
    const chart = renderChartWidget('line', points, listChartSeries(points));
    const renderedPointCount = [...chart.querySelectorAll('.line-chart-series')]
      .reduce((total, line) => total + (line.getAttribute('points')?.split(' ').length ?? 0), 0);

    expect(renderedPointCount).toBeLessThanOrEqual(2_000);
    expect(chart.querySelectorAll('.chart-point')).toHaveLength(0);
  });

  it('caps each series to its two endpoints when the series count collapses the per-series budget', () => {
    const seriesCount = 3_000;
    const pointsPerSeries = 5;
    const points = Array.from({ length: seriesCount * pointsPerSeries }, (_, index) => ({
      x: new Date(Math.floor(index / seriesCount) * 60_000).toISOString(),
      y: index % 1_000,
      color: `series-${index % seriesCount}`
    }));
    const chart = renderChartWidget('line', points, listChartSeries(points));
    const seriesLines = [...chart.querySelectorAll('.line-chart-series')];

    // Each series' per-series budget collapses to 2 points; sampleLineCoordinates
    // must honor that instead of always emitting first/last plus bucket extrema.
    expect(seriesLines.every((line) => (line.getAttribute('points')?.split(' ').length ?? 0) <= 2)).toBe(true);
    expect(chart.querySelectorAll('.chart-point')).toHaveLength(0);
  });

  it('honors the aggregate 2,000-point budget when the per-series limit collapses to 3', () => {
    // 667 series gives Math.floor(2000 / 667) === 2, so use 666 series to land
    // exactly on renderedPointLimit === 3, the boundary flagged in review.
    const seriesCount = 666;
    const pointsPerSeries = 50;
    const points = Array.from({ length: seriesCount * pointsPerSeries }, (_, index) => ({
      x: new Date(Math.floor(index / seriesCount) * 60_000).toISOString(),
      y: index % 1_000,
      color: `series-${index % seriesCount}`
    }));
    const chart = renderChartWidget('line', points, listChartSeries(points));
    const renderedPointCount = [...chart.querySelectorAll('.line-chart-series')]
      .reduce((total, line) => total + (line.getAttribute('points')?.split(' ').length ?? 0), 0);

    expect(renderedPointCount).toBeLessThanOrEqual(2_000);
    expect(chart.querySelectorAll('.chart-point')).toHaveLength(0);
  });

  it('renders a concise, evenly sampled timeline axis while preserving exact values', () => {
    const points = Array.from({ length: 9 }, (_, index) => ({
      x: `2026-09-0${index + 1}T0${index}:15:00Z`,
      y: index,
      color: null
    }));
    const chart = renderChartWidget('line', points, listChartSeries(points));
    const ticks = [...chart.querySelectorAll('.timeline-chart-axis span')];

    expect(ticks.map((tick) => tick.textContent)).toEqual([
      'Sep 1, 00:15 UTC',
      'Sep 3, 02:15 UTC',
      'Sep 5, 04:15 UTC',
      'Sep 7, 06:15 UTC',
      'Sep 9, 08:15 UTC'
    ]);
    expect(ticks.map((tick) => tick.getAttribute('title'))).toEqual([
      points[0].x,
      points[2].x,
      points[4].x,
      points[6].x,
      points[8].x
    ]);
  });

  it('renders concise, evenly sampled bar axes while preserving exact category values', () => {
    const points = Array.from({ length: 9 }, (_, index) => ({
      x: `category-with-a-long-name-${index + 1}`,
      y: index * 25,
      color: null
    }));
    const chart = renderChartWidget('bar', points, listChartSeries(points), null, 'Total', {
      name: 'AI Credits',
      symbol: 'AIC',
      significant: 1
    });
    const xTicks = [...chart.querySelectorAll('.bar-chart-x-axis text')];

    expect(xTicks).toHaveLength(5);
    expect(xTicks.map((tick) => tick.lastChild?.textContent)).toEqual([
      'category-wi…',
      'category-wi…',
      'category-wi…',
      'category-wi…',
      'category-wi…'
    ]);
    expect(xTicks.map((tick) => tick.getAttribute('title'))).toEqual([
      points[0].x,
      points[2].x,
      points[4].x,
      points[6].x,
      points[8].x
    ]);
    expect([...chart.querySelectorAll('.bar-chart-x-axis text > title')].map((title) => title.textContent)).toEqual([
      points[0].x,
      points[2].x,
      points[4].x,
      points[6].x,
      points[8].x
    ]);
    expect([...chart.querySelectorAll('.bar-chart-y-axis text')].map((tick) => tick.textContent)).toEqual([
      '200 AIC',
      '100 AIC',
      '0 AIC'
    ]);
  });
});
