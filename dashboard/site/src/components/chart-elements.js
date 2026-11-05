/**
 * Reusable presentation-only chart legend and series helpers for dashboard views.
 */

import { h, svgAnchor } from '../dom.js';
import { effect, state } from '../reactive.js';
import { formatNumber, formatShortUtcDate, toNumber } from '../view-formatters.js';
import { formatCount, formatCoveragePercent, pluralSuffix } from './count-formatters.js';
import { createFactoryScope } from './factory-elements.js';
import { binHistogramValues } from './histogram.js';
import { externalAnchorAttrs, renderSafeLink } from './link-content.js';
import { renderEmptyMessage, renderLegendList } from './ui-primitives.js';
import { createDebug } from '../debug.js';
import { renderTreemapChart } from './treemap-chart.js';

const debugChartElements = createDebug('chart-elements');

const MAX_LINE_POINT_SIZE = 6;
const MIN_LINE_POINT_SIZE = 2;
const MAX_DOT_POINT_RADIUS = 2.5;
const MIN_DOT_POINT_RADIUS = 0.5;
const MIN_RADIUS_POINT_COUNT = 100;
// Some SVG engines collapse zero-length lines before painting their round caps.
const NON_SCALING_POINT_LENGTH = 0.001;
const MAX_INTERACTIVE_LINE_POINTS = 500;
const MAX_RENDERED_LINE_POINTS = 2_000;
const MAX_TIMELINE_TICKS = 5;
const LINE_CHART_RIGHT = 100;
const LINE_CHART_BOTTOM = 38;
const LINE_CHART_HEIGHT = 34;
const LINE_CHART_MIN_LEFT = 5;
const LINE_CHART_MIN_PLOT_WIDTH = 25;
const LINE_CHART_LABEL_GAP = 2;
// Approximate the 2.6px SVG axis font before attachment: digits 1.5,
// punctuation 0.75, wide glyphs 2.1, and remaining glyphs 1.6 viewBox units.
const LINE_CHART_LABEL_DIGIT_WIDTH = 1.5;
const LINE_CHART_LABEL_DEFAULT_WIDTH = 1.6;
const LINE_CHART_LABEL_NARROW_WIDTH = 0.75;
const LINE_CHART_LABEL_WIDE_WIDTH = 2.1;
const MAX_BAR_AXIS_TICKS = 5;
const MAX_HORIZONTAL_BARS = 100;
const BAR_CHART_LEFT = 12;
const BAR_CHART_RIGHT = 100;
const BAR_CHART_BOTTOM = 38;
const BAR_CHART_HEIGHT = 34;
const MAX_HISTOGRAM_X_TICKS = 5;
const MAX_HEATMAP_CELLS = 100;
const MAX_HEATMAP_AXIS_CATEGORIES = 12;
const HEATMAP_VIEWBOX_WIDTH = 120;
const HEATMAP_LEFT = 28;
const HEATMAP_TOP = 12;
const HEATMAP_RIGHT = 2;
const HEATMAP_CELL_HEIGHT = 11;
const HEATMAP_BOTTOM = 2;
const PIE_CHART_CENTER = 21;
const PIE_CHART_RADIUS = 15.9155;
const PIE_CHART_STROKE_WIDTH = 6;
const PIE_CHART_SEGMENT_GAP = 0.03;
const PIE_CHART_CENTER_TEXT_LENGTH = 21;
const MAX_SWIMLANE_SECTIONS_PER_LANE = 120;
// Avoid hiding short incidental overlaps such as "QA " or "run " prefixes.
/** @type {Array<[string, string]>} */
const SWIMLANE_DEFINITIONS = [
  ['action-required', 'Action required'],
  ['failure', 'Failure'],
  ['cancelled', 'Cancelled'],
  ['skipped', 'Skipped'],
  ['success', 'Success']
];
const SWIMLANE_VIEWBOX_WIDTH = 120;
const SWIMLANE_START_X = 19;
const SWIMLANE_END_X = 116;
const SWIMLANE_LABEL_X = SWIMLANE_START_X - 1.5;
const SWIMLANE_LABEL_BASELINE_OFFSET = 1;
const SWIMLANE_TOP_Y = 5;
const SWIMLANE_LANE_GAP = 7.25;
const SWIMLANE_AXIS_Y = SWIMLANE_TOP_Y + (SWIMLANE_LANE_GAP * SWIMLANE_DEFINITIONS.length);
const SWIMLANE_TICK_END_Y = SWIMLANE_AXIS_Y + 2;
const SWIMLANE_AXIS_LABEL_Y = SWIMLANE_AXIS_Y + 6;
const SWIMLANE_VIEWBOX_HEIGHT = SWIMLANE_AXIS_LABEL_Y + 1.5;
/**
 * Swimlane coordinates are expressed in SVG viewBox units. `viewBoxWidth` and
 * `viewBoxHeight` define the drawing plane; `startX` and `endX` bound the
 * timeline; `labelX` and `labelBaselineOffset` align lane labels beside their
 * separators; `topY` and `laneGap` place the categorical lanes; and `axisY`,
 * `tickEndY`, and `axisLabelY` anchor the time axis below the final lane.
 */
export const SWIMLANE_LAYOUT = Object.freeze({
  viewBoxWidth: SWIMLANE_VIEWBOX_WIDTH,
  startX: SWIMLANE_START_X,
  endX: SWIMLANE_END_X,
  labelX: SWIMLANE_LABEL_X,
  labelBaselineOffset: SWIMLANE_LABEL_BASELINE_OFFSET,
  topY: SWIMLANE_TOP_Y,
  laneGap: SWIMLANE_LANE_GAP,
  axisY: SWIMLANE_AXIS_Y,
  tickEndY: SWIMLANE_TICK_END_Y,
  axisLabelY: SWIMLANE_AXIS_LABEL_Y,
  viewBoxHeight: SWIMLANE_VIEWBOX_HEIGHT
});
const SWIMLANE_FAILURES = new Set(['failure', 'startup-failure', 'stale', 'timed-out']);
const CHART_SERIES_COLOR_COUNT = 12;
const DEFAULT_CHART_SERIES_NAME = 'value';
let pieChartTableId = 0;
const SEMANTIC_SERIES_TERMS = {
  failure: new Set(['0', 'denied', 'error', 'errored', 'fail', 'failed', 'failing', 'failure', 'false', 'invalid', 'no', 'rejected', 'stale', 'timeout', 'unhealthy', 'unsuccessful']),
  success: new Set(['approved', 'complete', 'completed', 'healthy', 'pass', 'passed', 'passing', 'resolved', 'succeed', 'succeeded', 'success', 'successful']),
  waiting: new Set(['awaiting', 'pending', 'queued', 'waiting']),
  attention: new Set(['cancelled', 'canceled', 'degraded', 'warning']),
  neutral: new Set(['disabled', 'dismissed', 'inactive', 'neutral', 'skipped', 'unknown'])
};
const SEMANTIC_SERIES_PHRASES = {
  failure: new Set(['timed out']),
  waiting: new Set(['action required', 'in progress'])
};

/**
 * @typedef {{ name: string, className: string }} ChartSeriesDescriptor
 */

/**
 * @typedef {{
 *   x: string,
 *   y: number | null,
 *   weight?: number,
 *   color: string | null,
 *   highlighted?: boolean | null,
 *   key?: string,
 *   category?: string,
 *   section?: string | null,
 *   link?: { href: string, label: string } | null,
 *   source?: Record<string, unknown>
 * }} ChartPointLike
 */

/**
 * @param {ChartPointLike[]} points
 * @returns {Array<[string, ChartPointLike[]]>}
 */
export function groupChartSeries(points) {
  const grouped = new Map();
  for (const point of points) {
    const name = point.color ?? DEFAULT_CHART_SERIES_NAME;
    const series = grouped.get(name) ?? [];
    series.push(point);
    grouped.set(name, series);
  }
  return [...grouped.entries()].sort(([left], [right]) => left.localeCompare(right));
}

/**
 * @param {ChartPointLike[]} points
 * @returns {ChartSeriesDescriptor[]}
 */
export function listChartSeries(points) {
  return groupChartSeries(points).map(([name], index) => ({
    name,
    className: chartSeriesClassName(name, index)
  }));
}

/**
 * Assigns named categories to a stable fallback palette while adding a
 * semantic color when the series name describes a commonly understood status.
 * @param {string} name
 * @param {number} index
 * @returns {string}
 */
export function chartSeriesClassName(name, index) {
  const identity = name.normalize('NFKC').trim().toLowerCase();
  const paletteIndex = identity
    ? stablePaletteIndex(identity)
    : index % CHART_SERIES_COLOR_COUNT;
  const paletteClass = `chart-series-${paletteIndex + 1}`;
  const normalized = name.toLowerCase().replace(/[^a-z0-9]+/g, ' ').trim();
  const terms = new Set(normalized.split(/\s+/).filter(Boolean));
  let meaning = null;

  if (SEMANTIC_SERIES_PHRASES.failure.has(normalized) || hasMatchingTerm(terms, SEMANTIC_SERIES_TERMS.failure)) {
    meaning = 'failure';
  } else if (
    SEMANTIC_SERIES_PHRASES.waiting.has(normalized)
    || ((terms.has('approval') || terms.has('review')) && (terms.has('required') || terms.has('awaiting') || terms.has('pending') || terms.has('waiting')))
    || hasMatchingTerm(terms, SEMANTIC_SERIES_TERMS.waiting)
  ) {
    meaning = 'waiting';
  } else if (hasMatchingTerm(terms, SEMANTIC_SERIES_TERMS.success)) {
    meaning = 'success';
  } else if (hasMatchingTerm(terms, SEMANTIC_SERIES_TERMS.attention)) {
    meaning = 'attention';
  } else if (hasMatchingTerm(terms, SEMANTIC_SERIES_TERMS.neutral)) {
    meaning = 'neutral';
  }

  return meaning ? `${paletteClass} chart-series-semantic-${meaning}` : paletteClass;
}

/** @param {string} identity */
function stablePaletteIndex(identity) {
  if (identity === DEFAULT_CHART_SERIES_NAME) return 0;
  let hash = 0x811c9dc5;
  for (const byte of new TextEncoder().encode(identity)) {
    hash ^= byte;
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0) % CHART_SERIES_COLOR_COUNT;
}

/** @param {Set<string>} terms @param {Set<string>} candidates */
function hasMatchingTerm(terms, candidates) {
  for (const term of terms) {
    if (candidates.has(term)) return true;
  }
  return false;
}

/**
 * @param {ChartSeriesDescriptor[]} series
 * @param {string} chartType
 * @returns {HTMLElement}
 */
export function renderChartLegend(series, chartType) {
  const legendItems = chartType === 'scatter'
    ? [...series, { name: 'Dashed lines show scale guides', className: 'chart-grid-key' }]
    : series;
  return renderLegendList(
    `chart-legend chart-legend-${chartType}`,
    legendItems,
    (item) => item.className,
    (item) => [h('span', null, item.name)],
    { 'data-chart-legend': 'visual' }
  );
}

/**
 * @param {Array<[string, ChartPointLike[]]>} groupedSeries
 * @param {string[]} xValues
 * @param {boolean} [gapMissing]
 * @returns {Map<string, Array<{ x: number, lower: number, upper: number, valid: boolean }>>}
 */
function chartAreaCoordinates(groupedSeries, xValues, gapMissing = false) {
  const result = new Map();
  const cumulativeByX = new Map(xValues.map((value) => [value, 0]));
  const negativeByX = new Map(xValues.map((value) => [value, 0]));
  for (const [seriesName, seriesPoints] of groupedSeries) {
    /** @type {Map<string, number | null>} */
    const valuesByX = new Map();
    for (const point of seriesPoints) {
      if (point.y == null) {
        if (!valuesByX.has(point.x)) valuesByX.set(point.x, null);
        continue;
      }
      const value = toNumber(point.y);
      valuesByX.set(point.x, (valuesByX.get(point.x) ?? 0) + (Number.isFinite(value) ? gapMissing ? value : Math.max(0, value) : 0));
    }
    result.set(seriesName, xValues.map((xValue, index) => {
      const value = valuesByX.get(xValue) ?? 0;
      const stack = gapMissing && value < 0 ? negativeByX : cumulativeByX;
      const lower = stack.get(xValue) ?? 0;
      const upper = lower + value;
      stack.set(xValue, upper);
      return {
        x: xValues.length < 2 ? 0.5 : index / (xValues.length - 1),
        lower, upper, valid: (!gapMissing || valuesByX.has(xValue)) && valuesByX.get(xValue) !== null
      };
    }));
  }
  return result;
}

/**
 * @typedef {{ xValues: string[], minimum: number, maximum: number, left: number, temporal: boolean }} ChartPlotDomain
 * @typedef {{ chart: string, points: ChartPointLike[], unit: { name: string, symbol: string, significant: number, format?: string } | null, label: string }} RenderedChartLayer
 */

/**
 * Composes existing chart marks on one drawing plane, without changing the
 * worker's row selection, order, or grain.
 * @param {RenderedChartLayer[]} layers
 * @param {boolean} temporal
 * @param {boolean} [independentY]
 * @returns {HTMLElement}
 */
export function renderLayeredChartWidget(layers, temporal, independentY = false) {
  const points = layers.flatMap((layer) => layer.points);
  if (!points.some((point) => point.y != null && Number.isFinite(point.y))) {
    return renderChartWidgetEmptyState('layer', 'No data is available for this visualization.');
  }
  if (layers.some((layer) => ['bar', 'rule'].includes(layer.chart) && layer.points.length > MAX_RENDERED_LINE_POINTS)) {
    debugChartElements({ event: 'layer-limit-exceeded', limit: MAX_RENDERED_LINE_POINTS });
    return renderChartWidgetEmptyState('layer', `Bar and rule layers support at most ${MAX_RENDERED_LINE_POINTS} observations. Limit the source query.`);
  }
  const xValues = [...new Set(points.filter((point) => point.x !== '' && (!temporal || Number.isFinite(Date.parse(point.x)))).map((point) => point.x))];
  if (temporal) xValues.sort((left, right) => Date.parse(left) - Date.parse(right));
  const ranges = layers.map((layer) => {
    let minimum = 0;
    let maximum = 1;
    for (const point of layer.points) {
      if (point.y == null || !Number.isFinite(point.y)) continue;
      minimum = Math.min(minimum, point.y);
      maximum = Math.max(maximum, point.y);
    }
    if (layer.chart === 'area') {
      for (const coordinates of chartAreaCoordinates(groupChartSeries(layer.points), xValues, true).values()) {
        for (const coordinate of coordinates) if (coordinate.valid) {
          maximum = Math.max(maximum, coordinate.lower, coordinate.upper);
          minimum = Math.min(minimum, coordinate.lower, coordinate.upper);
        }
      }
    }
    return { minimum, maximum };
  });
  const shared = {
    minimum: Math.min(...ranges.map((range) => range.minimum)),
    maximum: Math.max(...ranges.map((range) => range.maximum))
  };
  const left = Math.min(LINE_CHART_RIGHT - LINE_CHART_MIN_PLOT_WIDTH, Math.max(
    LINE_CHART_MIN_LEFT,
    ...layers.map((layer, index) => {
      const range = independentY ? ranges[index] : shared;
      return Math.ceil(Math.max(...[range.maximum, (range.maximum + range.minimum) / 2, range.minimum]
        .map((value) => estimateLineChartLabelWidth(formatChartAxisTick(value, layer.unit)))) + LINE_CHART_LABEL_GAP);
    })
  ));
  const widgets = layers.map((layer, index) => renderChartWidget(
    layer.chart, layer.points, listChartSeries(layer.points), null, layer.label, layer.unit,
    null, null, undefined, null, {}, { xValues, ...(independentY ? ranges[index] : shared), left, temporal }
  ));
  const first = widgets[0];
  const svg = first.querySelector('svg');
  if (!svg) throw new Error('Layered chart did not produce a drawing plane.');
  const guides = [...svg.children].filter((node) => node.matches('.line-chart-y-axis, .line-chart-axis'));
  const firstLayerMarks = [...svg.children].filter((node) => !guides.includes(node));
  svg.replaceChildren(...guides);
  widgets.forEach((widget, index) => {
    const layerSvg = index === 0 ? null : widget.querySelector('svg');
    const marks = index === 0
      ? firstLayerMarks
      : [...(layerSvg?.children ?? [])].filter((node) => !node.matches('.line-chart-y-axis, .line-chart-axis'));
    svg.append(h('g', {
      'data-chart-layer': String(index),
      'data-chart-layer-type': layers[index].chart,
      'aria-label': layers[index].label
    }, ...marks));
  });
  svg.setAttribute('aria-label', `Layered chart with ${layers.length} layers${independentY ? ' and independent y scales' : ''}`);
  first.classList.add('layer-chart-widget');
  first.dataset.chart = 'layer';
  if (independentY) {
    first.querySelector('.line-chart-y-labels')?.remove();
    first.append(h('div', { className: 'layer-chart-scale-key', 'aria-label': 'Independent y scales' },
      ...layers.map((layer, index) => h('span', null,
        `${layer.label}: ${formatNumber(ranges[index].minimum, layer.unit)} – ${formatNumber(ranges[index].maximum, layer.unit)}`,
        ` (midpoint ${formatNumber((ranges[index].minimum + ranges[index].maximum) / 2, layer.unit)})`))));
  }
  return first;
}

/**
 * @param {ChartPointLike[]} points
 * @returns {{ entries: Array<[string, number]>, total: number }}
 */
export function pieChartEntries(points) {
  const totals = new Map();
  for (const point of points) {
    const category = point.x;
    totals.set(category, (totals.get(category) ?? 0) + point.y);
  }
  const entries = [...totals.entries()].filter(([, value]) => value > 0);
  const total = entries.reduce((sum, [, value]) => sum + value, 0);
  return { entries, total };
}

/** @param {number} startFraction @param {number} endFraction @param {boolean} separated */
function pieChartSegmentPath(startFraction, endFraction, separated = false) {
  const start = Math.min(1, Math.max(0, startFraction));
  const end = Math.min(1, Math.max(start, endFraction));
  const sweep = end - start;
  const gap = separated && sweep < 1 - Number.EPSILON
    ? Math.min(PIE_CHART_SEGMENT_GAP, sweep * 0.2)
    : 0;
  const visibleStart = start + gap;
  const visibleEnd = end - gap;
  /** @param {number} fraction */
  const pointAt = (fraction) => {
    const angle = (fraction * Math.PI * 2) - (Math.PI / 2);
    return [
      Number((PIE_CHART_CENTER + (Math.cos(angle) * PIE_CHART_RADIUS)).toFixed(4)),
      Number((PIE_CHART_CENTER + (Math.sin(angle) * PIE_CHART_RADIUS)).toFixed(4))
    ];
  };
  const [startX, startY] = pointAt(visibleStart);

  if (sweep <= Number.EPSILON) return `M ${startX} ${startY}`;
  if (sweep >= 1 - Number.EPSILON) {
    const [middleX, middleY] = pointAt(start + 0.5);
    return `M ${startX} ${startY} A ${PIE_CHART_RADIUS} ${PIE_CHART_RADIUS} 0 1 1 ${middleX} ${middleY} A ${PIE_CHART_RADIUS} ${PIE_CHART_RADIUS} 0 1 1 ${startX} ${startY}`;
  }

  const [endX, endY] = pointAt(visibleEnd);
  return `M ${startX} ${startY} A ${PIE_CHART_RADIUS} ${PIE_CHART_RADIUS} 0 ${(visibleEnd - visibleStart) > 0.5 ? 1 : 0} 1 ${endX} ${endY}`;
}

/**
 * Ranks rows by value descending while retaining their input-order color.
 * @param {Array<[string, number]>} entries
 * @param {number} total
 * @param {Map<string, { href: string, label: string }>} [links]
 * @param {{ name: string, symbol: string, significant: number } | null} [unit]
 * @param {(label: string) => string} [formatLabel]
 * @returns {HTMLElement}
 */
export function renderPieLegend(entries, total, links = new Map(), unit = null, formatLabel = (label) => label) {
  const rankedEntries = entries
    .map((entry, index) => ({ entry, index }))
    .sort((left, right) => right.entry[1] - left.entry[1] || left.index - right.index);
  return renderLegendList(
    'chart-legend chart-legend-pie',
    rankedEntries,
    ({ entry: [label], index }) => chartSeriesClassName(label, index),
    ({ entry: [label, value] }) => {
      const link = links.get(label) ?? null;
      return [
        h('span', null, renderSafeLink(formatLabel(label), link)),
        h('strong', null, formatPieValue(value, unit)),
        h('small', null, total > 0 ? formatCoveragePercent(value / total) : '0%')
      ];
    },
    { 'data-chart-legend': 'visual' }
  );
}

/**
 * @param {number} value
 * @param {{ name: string, symbol: string, significant: number } | null} unit
 * @param {boolean} [includeUnit]
 */
function formatPieValue(value, unit, includeUnit = true) {
  return unit ? formatNumber(value, unit, includeUnit) : formatCount(value);
}

/**
 * @param {HTMLElement} chart
 * @param {HTMLElement} table
 * @returns {HTMLElement}
 */
export function renderPieChartLayout(chart, table) {
  const tableId = `pie-chart-table-${++pieChartTableId}`;
  table.id = tableId;
  const toggle = h('button', {
    type: 'button',
    className: 'pie-chart-table-toggle',
    'aria-controls': tableId
  });
  const layout = h('div', { className: 'pie-chart-layout' }, chart, toggle, table);
  const scope = createFactoryScope();
  const expanded = state(true);
  // `expanded` is the toggle's entire visible state; this effect is the only
  // place that writes the layout's hidden marker and the toggle's expanded
  // attribute/label, replacing the read-then-write bookkeeping the click
  // handler used to repeat against `layout`'s own attribute.
  effect(() => {
    const isExpanded = expanded.get();
    layout.toggleAttribute('data-chart-table-hidden', !isExpanded);
    toggle.setAttribute('aria-expanded', String(isExpanded));
    toggle.setAttribute('aria-label', `${isExpanded ? 'Hide' : 'Show'} chart table`);
  }, { signal: scope.signal });
  toggle.addEventListener('click', () => {
    expanded.set((current) => !current);
  }, { signal: scope.signal });
  scope.bind(layout);
  return layout;
}

/**
 * Wraps chart content in the shared `chart-widget` container, applying the
 * per-chart-type modifier class and `data-chart-widget` marker consistently.
 * @param {string} chartType
 * @param {Record<string, unknown> | null} extraAttrs
 * @param {...(HTMLElement | null)} children
 * @returns {HTMLElement}
 */
function renderChartWidgetShell(chartType, extraAttrs, ...children) {
  return h(
    'div',
    { className: `chart-widget ${chartType}-chart-widget`, 'data-chart-widget': chartType, ...(extraAttrs ?? {}) },
    ...children
  );
}

/**
 * Renders a chart widget shell containing the shared `role="status"` empty
 * placeholder message. Shared by the generic chart, heatmap, and swimlane
 * renderers, which otherwise duplicated the same
 * `renderChartWidgetShell(type, null, renderEmptyMessage(message, { role: 'status' }))`
 * wrapping.
 * @param {string} chartType
 * @param {string} message
 * @returns {HTMLElement}
 */
function renderChartWidgetEmptyState(chartType, message) {
  return renderChartWidgetShell(chartType, null, renderEmptyMessage(message, { role: 'status' }));
}

/**
 * Renders the small `<g><rect/><text/></g>` tooltip shell shown alongside a
 * chart point, segment, or bar on hover/focus. Shared by the pie, histogram,
 * and line/scatter/dot chart renderers, which otherwise duplicated the same
 * hidden-tooltip markup with only their sizing and positioning attributes
 * differing.
 * @param {{
 *   className?: string,
 *   transform: string,
 *   width?: number,
 *   height?: number,
 *   rx?: number,
 *   textX?: number,
 *   textY?: number,
 *   textLengthAttrs?: Record<string, unknown> | null,
 *   label: string
 * }} options
 */
function renderChartPointTooltip({
  className = 'point-tooltip',
  transform,
  width = 42,
  height = 9,
  rx = 2,
  textX = 3,
  textY = 6,
  textLengthAttrs = null,
  label
}) {
  return h(
    'g',
    { className, transform, 'aria-hidden': 'true' },
    h('rect', { width, height, rx }),
    h('text', { x: textX, y: textY, ...(textLengthAttrs ?? {}) }, label)
  );
}

/**
 * Re-appends a chart point/segment to its parent on hover or focus so its
 * tooltip paints above sibling marks instead of being clipped underneath them.
 * @param {Element} mark
 */
function raiseChartPointOnInteraction(mark) {
  const bringToFront = () => mark.parentNode?.append(mark);
  mark.addEventListener('pointerenter', bringToFront);
  mark.addEventListener('focus', bringToFront);
}

/**
 * Builds an interactive chart-point `<g>` mark: a `chart-point`-flavored
 * group with `role="img"`, `aria-label`, and a `<title>` for accessibility,
 * the caller's shape element(s), and a hidden tooltip, then wires it to
 * raise above sibling marks on hover/focus. Shared by the pie, histogram,
 * and line/scatter/dot chart renderers, which otherwise duplicated this
 * same `<g>` + `<title>` + tooltip + raise-on-interaction scaffolding
 * around their own shape markup.
 * @param {{
 *   className: string,
 *   entryIndex: number,
 *   label: string,
 *   shape: Element | Element[],
 *   tooltip: Parameters<typeof renderChartPointTooltip>[0]
 * }} options
 * @returns {Element}
 */
function renderInteractiveChartMark({ className, entryIndex, label, shape, tooltip }) {
  const mark = h(
    'g',
    {
      className,
      style: `--chart-entry-index: ${entryIndex}`,
      tabIndex: 0,
      role: 'img',
      'aria-label': label
    },
    h('title', null, label),
    ...(Array.isArray(shape) ? shape : [shape]),
    renderChartPointTooltip(tooltip)
  );
  raiseChartPointOnInteraction(mark);
  return mark;
}

/**
 * Renders a declaratively selected chart using normalized dashboard points.
 * @param {string} chartType
 * @param {ChartPointLike[]} points
 * @param {ChartSeriesDescriptor[]} series
 * @param {{ entries: Array<[string, number]>, total: number } | null} [pieSummary]
 * @param {string} [totalLabel]
 * @param {{ name: string, symbol: string, significant: number, format?: string } | null} [unit]
 * @param {Record<string, unknown> | null} [timeRange]
 * @param {string | null} [referenceField]
 * @param {(label: string) => string} [formatCategory]
 * @param {{ at: string, label: string } | null} [temporalMarker]
 * @param {import('./treemap-layout.js').TreemapOptions} [treemapOptions]
 * @param {ChartPlotDomain | null} [plotDomain]
 * @param {{ cluster?: boolean }} [scatterOptions]
 * @returns {HTMLElement}
 */
export function renderChartWidget(chartType, points, series, pieSummary = null, totalLabel = 'Total', unit = null, timeRange = null, referenceField = null, formatCategory = (label) => label, temporalMarker = null, treemapOptions = {}, plotDomain = null, scatterOptions = {}) {
  if (chartType === 'treemap') {
    return renderTreemapChart(points, totalLabel, unit, treemapOptions, chartSeriesClassName);
  }
  const pieData = chartType === 'pie' ? pieSummary ?? pieChartEntries(points) : null;
  const entryCount = pieData ? pieData.entries.length : points.length;
  const minimumEntries = ['bar', 'heatmap', 'horizontal-bar', 'pie', 'scatter'].includes(chartType) ? 1 : 2;
  debugChartElements({ event: 'render', chartType, entryCount, seriesCount: series.length });
  if (entryCount < minimumEntries && chartType !== 'swimlane' && !plotDomain) {
    debugChartElements({ event: 'empty-state', chartType, reason: entryCount === 0 ? 'no-data' : 'insufficient-data', entryCount });
    return renderChartWidgetEmptyState(
      chartType,
      entryCount === 0 ? 'No data is available for this visualization.' : 'Not enough data to show this visualization.'
    );
  }

  if (chartType === 'pie') {
    const { entries, total } = /** @type {{ entries: Array<[string, number]>, total: number }} */ (pieData);
    const safeTotal = Number.isFinite(total) && total > 0 ? total : 0;
    const formattedTotal = formatPieValue(total, unit, false);
    const totalValueLengthAttrs = formattedTotal.length > 7
      ? { textLength: PIE_CHART_CENTER_TEXT_LENGTH, lengthAdjust: 'spacingAndGlyphs' }
      : null;
    const totalLabelLengthAttrs = totalLabel.length > 12
      ? { textLength: PIE_CHART_CENTER_TEXT_LENGTH, lengthAdjust: 'spacingAndGlyphs' }
      : null;
    const separated = entries.filter(([, value]) => Number.isFinite(value) && value > 0).length > 1;
    let cumulativeValue = 0;
    return renderChartWidgetShell(
      'pie',
      null,
      h(
        'svg',
        { viewBox: '0 0 42 42', role: 'img', 'aria-label': `Pie chart: ${entries.map(([label, value]) => `${label} ${formatPieValue(value, unit)}`).join(', ') || 'no data'}` },
        h('circle', { className: 'pie-chart-track', cx: PIE_CHART_CENTER, cy: PIE_CHART_CENTER, r: PIE_CHART_RADIUS, fill: 'none', 'stroke-width': PIE_CHART_STROKE_WIDTH }),
        ...entries.map(([label, value], index) => {
          const segmentValue = Number.isFinite(value) && value > 0 ? value : 0;
          const startFraction = safeTotal > 0 ? cumulativeValue / safeTotal : 0;
          cumulativeValue = Math.min(safeTotal, cumulativeValue + segmentValue);
          const endFraction = safeTotal > 0 ? cumulativeValue / safeTotal : startFraction;
          const segmentLabel = `${label}: ${formatPieValue(value, unit)}`;
          const midpoint = ((startFraction + endFraction) / 2) * Math.PI * 2 - (Math.PI / 2);
          const tooltipWidth = Math.min(40, Math.max(18, (segmentLabel.length * 1.25) + 5));
          const tooltipX = Math.min(Math.max(21 + (Math.cos(midpoint) * 14) - (tooltipWidth / 2), 1), 41 - tooltipWidth);
          const tooltipY = Math.min(Math.max(21 + (Math.sin(midpoint) * 14) - 9, 1), 34);
          return renderInteractiveChartMark({
            className: 'chart-point pie-chart-mark',
            entryIndex: index,
            label: segmentLabel,
            shape: h('path', {
              className: `pie-chart-segment ${chartSeriesClassName(label, index)}`,
              d: pieChartSegmentPath(startFraction, endFraction, separated),
              fill: 'none',
              'stroke-width': PIE_CHART_STROKE_WIDTH,
              'stroke-linecap': 'round',
              'data-chart-category': label
            }),
            tooltip: {
              className: 'point-tooltip pie-chart-tooltip',
              transform: `translate(${tooltipX} ${tooltipY})`,
              width: tooltipWidth,
              height: 7,
              textX: 2.5,
              textY: 4.75,
              textLengthAttrs: tooltipWidth === 40 ? { textLength: 35, lengthAdjust: 'spacingAndGlyphs' } : null,
              label: segmentLabel
            }
          });
        }),
        h('text', { className: 'pie-chart-total-value', x: 21, y: 20, 'text-anchor': 'middle', 'aria-hidden': 'true', ...(totalValueLengthAttrs ?? {}) }, formattedTotal),
        h('text', { className: 'pie-chart-total-label', x: 21, y: 25.5, 'text-anchor': 'middle', 'aria-hidden': 'true', ...(totalLabelLengthAttrs ?? {}) }, totalLabel)
      )
    );
  }

  if (chartType === 'histogram') {
    const bins = binHistogramValues(points.map((point) => toNumber(point.y)));
    const maximum = Math.max(...bins.map((bin) => bin.count), 1);
    const plotStart = 9;
    const plotWidth = 100 - plotStart;
    const barWidth = bins.length > 0 ? plotWidth / bins.length : plotWidth;
    const xBoundaries = bins.length > 0
      ? [...bins.map((bin) => bin.lower), bins[bins.length - 1].upper]
      : [];
    const xTickCount = Math.min(xBoundaries.length, MAX_HISTOGRAM_X_TICKS);
    const xTicks = Array.from({ length: xTickCount }, (_, index) => (
      xBoundaries[Math.round((index * (xBoundaries.length - 1)) / Math.max(xTickCount - 1, 1))]
    )).map((value) => formatNumber(value, unit))
      .filter((value, index, values) => index === 0 || value !== values[index - 1]);
    /** @param {{ lower: number, upper: number }} bin */
    const binLabel = (bin) => {
      const lower = formatNumber(bin.lower, unit);
      const upper = formatNumber(bin.upper, unit);
      return bin.lower === bin.upper ? lower : `${lower}–${upper}`;
    };
    return renderChartWidgetShell(
      'histogram',
      null,
      h(
        'svg',
        { viewBox: '0 0 100 42', role: 'img', 'aria-label': `Histogram with ${bins.length} automatically calculated bins` },
        ...[
          [4, maximum],
          [21, maximum / 2],
          [38, 0]
        ].flatMap(([y, value]) => [
          h('text', {
            className: 'histogram-chart-y-label',
            x: plotStart - 1.5,
            y: y + 1,
            'text-anchor': 'end',
            'aria-hidden': 'true'
          }, formatNumber(value, null)),
          h('line', { className: 'histogram-chart-grid', x1: plotStart, y1: y, x2: 100, y2: y })
        ]),
        h('line', { className: 'bar-chart-axis', x1: plotStart, y1: 38, x2: 100, y2: 38 }),
        ...bins.map((bin, index) => {
          const height = Math.max(1, (bin.count / maximum) * 34);
          const label = `${binLabel(bin)}: ${bin.count} observation${pluralSuffix(bin.count)}`;
          const x = plotStart + (index * barWidth);
          const tooltipX = Math.min(Math.max(x + ((barWidth - 1) / 2) - 21, 1), 57);
          return renderInteractiveChartMark({
            className: 'chart-point histogram-chart-mark',
            entryIndex: index,
            label,
            shape: h('rect', {
              className: 'histogram-chart-bar chart-series-1',
              x,
              y: 38 - height,
              width: Math.max(0, barWidth - 1),
              height,
              rx: 0.75
            }),
            tooltip: {
              className: 'point-tooltip histogram-chart-tooltip',
              transform: `translate(${tooltipX} ${Math.max(38 - height - 12, 1)})`,
              textLengthAttrs: label.length > 22 ? { textLength: 36, lengthAdjust: 'spacingAndGlyphs' } : null,
              label
            }
          });
        })
      ),
      bins.length > 0
        ? h(
          'div',
          { className: 'chart-axis', 'data-chart-axis': 'histogram' },
          ...xTicks.map((value) => h('span', null, value))
        )
        : null
    );
  }

  if (chartType === 'heatmap') {
    return renderHeatmapChart(points, totalLabel, unit);
  }

  if (chartType === 'horizontal-bar') {
    if (points.length > MAX_HORIZONTAL_BARS) {
      debugChartElements({ event: 'bar-limit-exceeded', chartType, pointCount: points.length, limit: MAX_HORIZONTAL_BARS });
      return renderChartWidgetEmptyState(
        chartType,
        `Horizontal bar charts support at most ${MAX_HORIZONTAL_BARS} bars.`
      );
    }
    const maximum = Math.max(...points.map((point) => toNumber(point.y)).filter(Number.isFinite), 1);
    const seriesClassNames = new Map(series.map((item) => [item.name, item.className]));
    /** @param {ChartPointLike} point @param {number} index */
    const renderRow = (point, index) => {
      const numericValue = toNumber(point.y);
      const value = Number.isFinite(numericValue) ? numericValue : 0;
      const barSize = Math.max(0, value);
      const label = chartPointLabel(point, unit);
      const category = formatCategory(point.x);
      const fullCategory = point.section ? `${point.section}:${point.x}` : point.x;
      const categoryText = h('bdi', {
        className: 'horizontal-bar-chart-label-text',
        dir: 'ltr'
      }, category);
      const categoryContent = renderSafeLink(categoryText, point.link ?? null);
      let hasInteractiveLink = false;
      if (point.link && categoryContent instanceof HTMLElement) {
        categoryContent.title = fullCategory;
        categoryContent.setAttribute('aria-label', `${point.link.label}: ${fullCategory}`);
        hasInteractiveLink = true;
      }
      const bar = h('span', {
        className: `bar-chart-bar horizontal-bar-chart-bar ${seriesClassNames.get(point.color ?? 'value') ?? 'chart-series-1'}`,
        style: `--chart-entry-index: ${index}; --horizontal-bar-size: ${(barSize / maximum) * 100}%`,
        'aria-hidden': 'true'
      });
      const track = point.link
        ? renderSafeLink(bar, { ...point.link, label: `${label}: ${point.link.label}` })
        : h('span', { 'aria-hidden': 'true' }, bar);
      if (track instanceof HTMLElement) track.classList.add('horizontal-bar-chart-track');
      return h(
        'li',
        { className: 'horizontal-bar-chart-row' },
        h('span', {
          className: 'horizontal-bar-chart-label',
          title: fullCategory,
          tabIndex: hasInteractiveLink ? undefined : 0,
          'aria-label': hasInteractiveLink ? undefined : fullCategory
        },
        categoryContent
        ),
        track,
        h('span', {
          className: 'horizontal-bar-chart-value',
          tabIndex: 0,
          role: 'img',
          'aria-label': label
        }, point.y == null ? 'Unavailable' : formatNumber(value, unit))
      );
    };
    const hasSections = points.some((point) => point.section);
    /** @type {HTMLElement[]} */
    let entries;
    if (hasSections) {
      /** @type {Map<string, Array<{ point: ChartPointLike, index: number }>>} */
      const sections = new Map();
      points.forEach((point, index) => {
        const section = point.section || 'Other';
        const sectionPoints = sections.get(section) ?? [];
        sectionPoints.push({ point, index });
        sections.set(section, sectionPoints);
      });
      entries = [...sections.entries()].map(([section, sectionPoints]) => h(
        'li',
        { className: 'horizontal-bar-chart-section' },
        h('h4', { className: 'horizontal-bar-chart-section-title' }, section),
        h(
          'ul',
          { className: 'horizontal-bar-chart-section-list', 'aria-label': section },
          ...sectionPoints.map(({ point, index }) => renderRow(point, index))
        )
      ));
    } else {
      entries = points.map(renderRow);
    }
    return renderChartWidgetShell(
      chartType,
      null,
      h(
        'ul',
        { className: 'horizontal-bar-chart-list', 'aria-label': `Horizontal bar chart with ${points.length} bars` },
        ...entries
      )
    );
  }

  if (chartType === 'area' || chartType === 'line' || chartType === 'dot' || chartType === 'scatter' || plotDomain) {
    const isAreaChart = chartType === 'area';
    const isDotChart = chartType === 'dot';
    const isScatterChart = chartType === 'scatter';
    const isLayerBar = chartType === 'bar' && plotDomain !== null;
    const isRuleChart = chartType === 'rule';
    const isPointChart = isDotChart || isScatterChart;
    const groupedSeries = groupChartSeries(points);
    const renderedPointLimit = Math.max(2, Math.floor(MAX_RENDERED_LINE_POINTS / Math.max(groupedSeries.length, 1)));
    const hasWindowHighlight = points.some((point) => typeof point.highlighted === 'boolean');
    const showInteractivePoints = points.length <= MAX_INTERACTIVE_LINE_POINTS;
    const seriesClassNames = new Map(series.map((item) => [item.name, item.className]));
    const xValues = plotDomain?.xValues ?? [...new Set(points.map((point) => point.x))];
    const xIndexes = new Map(xValues.map((value, index) => [value, index]));
    const proportionalTime = isScatterChart || plotDomain?.temporal === true;
    const parsedTimes = proportionalTime ? xValues.map((value) => Date.parse(value)) : [];
    let minimumTime = Number.POSITIVE_INFINITY;
    let maximumTime = Number.NEGATIVE_INFINITY;
    for (const time of parsedTimes) {
      if (!Number.isFinite(time)) continue;
      minimumTime = Math.min(minimumTime, time);
      maximumTime = Math.max(maximumTime, time);
    }
    const timelineTicks = proportionalTime
      ? scatterChartTimeAxisTicks(parsedTimes, minimumTime, maximumTime)
      : lineChartTimelineTicks(xValues);
    /** @type {Map<string, Array<{ x: number, lower: number, upper: number, valid: boolean }>>} */
    const areaCoordinates = isAreaChart ? chartAreaCoordinates(groupedSeries, xValues, plotDomain !== null) : new Map();
    let maximum = 1;
    let minimum = 0;
    if (isAreaChart) {
      for (const coordinates of areaCoordinates.values()) {
        for (const coordinate of coordinates) if (coordinate.valid) maximum = Math.max(maximum, coordinate.upper);
      }
    } else {
      for (const point of points) {
        if (point.y == null) continue;
        const value = toNumber(point.y);
        if (Number.isFinite(value)) {
          maximum = Math.max(maximum, value);
          minimum = Math.min(minimum, value);
        }
      }
    }
    const referenceLines = isDotChart && referenceField
      ? groupedSeries.flatMap(([seriesName, seriesPoints]) => [...new Set(seriesPoints
        .map((point) => toNumber(point.source?.[referenceField]))
        .filter(Number.isFinite))]
        .map((value) => ({ seriesName, value })))
      : [];
    for (const { value } of referenceLines) maximum = Math.max(maximum, value);
    if (plotDomain) {
      maximum = plotDomain.maximum;
      minimum = plotDomain.minimum;
    }
    const pointSize = lineChartPointSize(points.length);
    const dotPointRadius = dotChartPointRadius(points.length);
    const yTicks = [maximum, (maximum + minimum) / 2, minimum];
    const yTickLabels = yTicks.map((value) => formatChartAxisTick(value, unit));
    const lineChartLeft = plotDomain?.left ?? Math.min(
      LINE_CHART_RIGHT - LINE_CHART_MIN_PLOT_WIDTH,
      Math.max(
        LINE_CHART_MIN_LEFT,
        Math.ceil(Math.max(...yTickLabels.map(estimateLineChartLabelWidth)) + LINE_CHART_LABEL_GAP)
      )
    );
    const plotWidth = LINE_CHART_RIGHT - lineChartLeft;
    /** @param {string} value */
    const scaledX = (value) => {
      const time = Date.parse(value);
      if (proportionalTime && maximumTime > minimumTime && Number.isFinite(time)) {
        return lineChartLeft + (((time - minimumTime) / (maximumTime - minimumTime)) * plotWidth);
      }
      return xValues.length < 2 ? lineChartLeft + (plotWidth / 2)
        : lineChartLeft + (((xIndexes.get(value) ?? 0) / (xValues.length - 1)) * plotWidth);
    };
    const markerTime = Date.parse(temporalMarker?.at ?? '');
    const pointTimes = xValues.map((value) => Date.parse(value)).filter(Number.isFinite);
    const markerX = temporalMarker && Number.isFinite(markerTime) && pointTimes.length > 0
      ? lineChartLeft + (
        Math.max(0, Math.min(1, (markerTime - Math.min(...pointTimes))
          / Math.max(1, Math.max(...pointTimes) - Math.min(...pointTimes))))
        * plotWidth
      )
      : null;
    for (const coordinates of areaCoordinates.values()) {
      for (const [index, coordinate] of coordinates.entries()) coordinate.x = scaledX(xValues[index]);
    }
    const gridLines = yTicks.map((value) => {
      const y = LINE_CHART_BOTTOM - (((value - minimum) / (maximum - minimum)) * LINE_CHART_HEIGHT);
      return h('line', {
        className: 'line-chart-grid',
        x1: lineChartLeft,
        y1: y,
        x2: LINE_CHART_RIGHT,
        y2: y
      });
    });
    const highlightedIndexes = [...new Set(points.flatMap((point) => {
      const index = point.highlighted ? xIndexes.get(point.x) : undefined;
      return index === undefined ? [] : [index];
    }))];
    const xStep = xValues.length > 1 ? plotWidth / (xValues.length - 1) : plotWidth;
    const windowBand = highlightedIndexes.length > 0
      ? {
        start: Math.max(lineChartLeft, lineChartLeft + ((Math.min(...highlightedIndexes) - 0.5) * xStep)),
        end: Math.min(LINE_CHART_RIGHT, lineChartLeft + ((Math.max(...highlightedIndexes) + 0.5) * xStep))
      }
      : null;
    /** @param {number} value */
    const scaledAreaY = (value) => Number((LINE_CHART_BOTTOM - ((value - minimum) / (maximum - minimum)) * LINE_CHART_HEIGHT).toFixed(4));
    return renderChartWidgetShell(
      chartType,
      {
        'data-line-rendering': showInteractivePoints ? 'rich' : 'compact',
        style: `--line-chart-left: ${lineChartLeft}%;`
      },
      h(
        'div',
        { className: 'line-chart-plot' },
        h(
        'svg',
        {
          viewBox: '0 0 100 42',
          role: 'img',
          'aria-label': isAreaChart
            ? `${series.length > 1 ? 'Stacked area' : 'Area'} chart with ${points.length} points`
            : isPointChart
            ? `${isScatterChart ? 'Scatter' : 'Dot'} chart with ${points.length} points${isDotChart ? ` and ${referenceLines.length} reference lines` : ''}`
            : `Line chart with ${points.length} points${markerX === null ? '' : ` and ${temporalMarker?.label ?? 'one temporal marker'}`}`
        },
        windowBand ? h('rect', {
          className: 'line-chart-window-band',
          x: windowBand.start,
          y: 0,
          width: Math.max(windowBand.end - windowBand.start, 1),
          height: LINE_CHART_BOTTOM,
          'aria-hidden': 'true'
        }) : null,
        h(
          'g',
          { className: 'line-chart-y-axis', 'data-chart-axis': 'y', 'aria-hidden': 'true' },
          ...gridLines,
          h('line', {
            className: 'line-chart-axis',
            x1: lineChartLeft,
            y1: LINE_CHART_BOTTOM - LINE_CHART_HEIGHT,
            x2: lineChartLeft,
            y2: LINE_CHART_BOTTOM
          })
        ),
        h('line', {
          className: 'line-chart-axis',
          x1: lineChartLeft,
          y1: LINE_CHART_BOTTOM,
          x2: LINE_CHART_RIGHT,
          y2: LINE_CHART_BOTTOM
        }),
        markerX === null ? null : h('g', {
          className: 'line-chart-temporal-marker',
          'data-chart-temporal-marker': temporalMarker?.at,
          'aria-hidden': 'true'
        },
        h('line', {
          x1: markerX,
          y1: LINE_CHART_BOTTOM - LINE_CHART_HEIGHT,
          x2: markerX,
          y2: LINE_CHART_BOTTOM
        }),
        h('text', {
          x: Math.min(markerX + 1.5, LINE_CHART_RIGHT - 1),
          y: LINE_CHART_BOTTOM - LINE_CHART_HEIGHT + 2.5,
          'text-anchor': markerX > LINE_CHART_RIGHT - 18 ? 'end' : 'start'
        }, temporalMarker?.label ?? 'Marker')),
        ...referenceLines.map(({ seriesName, value }) => h('line', {
          className: `dot-chart-reference ${seriesClassNames.get(seriesName) ?? 'chart-series-1'}`,
          x1: lineChartLeft,
          y1: LINE_CHART_BOTTOM - ((Math.max(minimum, value) - minimum) / (maximum - minimum)) * LINE_CHART_HEIGHT,
          x2: LINE_CHART_RIGHT,
          y2: LINE_CHART_BOTTOM - ((Math.max(minimum, value) - minimum) / (maximum - minimum)) * LINE_CHART_HEIGHT,
          'data-chart-reference': seriesName,
          'data-chart-reference-value': String(value),
          'aria-hidden': 'true'
        })),
        ...groupedSeries.flatMap(([seriesName, seriesPoints], seriesIndex) => {
          const seriesClassName = seriesClassNames.get(seriesName) ?? 'chart-series-1';
          const stackedCoordinates = areaCoordinates.get(seriesName) ?? [];
          const coordinates = seriesPoints.map((point, index) => {
            const x = scaledX(point.x);
            const valid = point.y != null && Number.isFinite(point.y)
              && (!plotDomain?.temporal || isRuleChart || Number.isFinite(Date.parse(point.x)));
            const y = valid
              ? LINE_CHART_BOTTOM - ((/** @type {number} */ (point.y) - minimum) / (maximum - minimum)) * LINE_CHART_HEIGHT
              : LINE_CHART_BOTTOM;
            return { point, x, y, valid, index };
          });
          const validCoordinates = coordinates.filter(({ valid }) => valid);
          const hasGaps = validCoordinates.length !== coordinates.length;
          const renderedCoordinates = isScatterChart && scatterOptions.cluster === false
            ? validCoordinates
            : sampleLineCoordinates(validCoordinates, renderedPointLimit);
          const gapPrefix = [];
          let gaps = 0;
          for (const coordinate of coordinates) {
            if (!coordinate.valid) gaps += 1;
            gapPrefix.push(gaps);
          }
          const highlightedCoordinates = hasWindowHighlight
            ? sampleLineCoordinates(validCoordinates.filter(({ point }) => point.highlighted), renderedPointLimit)
            : [];
          if (isRuleChart) {
            /** @type {Map<number, ChartPointLike[]>} */
            const rules = new Map();
            for (const { point } of validCoordinates) {
              const value = /** @type {number} */ (point.y);
              const observations = rules.get(value) ?? [];
              observations.push(point);
              rules.set(value, observations);
            }
            return [...rules].map(([value, observations]) => {
              const y = scaledAreaY(value);
              const label = `${seriesName}: ${formatNumber(value, unit)}`;
              const links = new Map(observations.flatMap((point) => point.link ? [[point.link.href, point.link]] : []));
              const point = { ...observations[0], x: seriesName, color: null, link: links.size === 1 ? links.values().next().value ?? null : null };
              return renderLayerObservationMark(h('line', {
                className: `dot-chart-reference ${seriesClassName}`,
                x1: lineChartLeft, x2: LINE_CHART_RIGHT, y1: y, y2: y,
                tabIndex: 0, role: 'img', 'aria-label': label,
                'data-chart-series': seriesName
              }, h('title', null, label)), point, unit, plotDomain !== null);
            });
          }
          if (isLayerBar) {
            const barWidth = Math.min(14, plotWidth / Math.max(xValues.length * groupedSeries.length, 1) * 0.7);
            return validCoordinates.map(({ point, x, y }) => {
              const zero = scaledAreaY(0);
              return renderLayerObservationMark(h('rect', {
                className: `bar-chart-bar ${seriesClassName}`,
                x: Math.max(lineChartLeft, Math.min(LINE_CHART_RIGHT - barWidth,
                  x + (seriesIndex - groupedSeries.length / 2) * barWidth)),
                y: Math.min(y, zero), width: barWidth, height: Math.abs(y - zero),
                tabIndex: 0, role: 'img', 'aria-label': chartPointLabel(point, unit),
                'data-chart-series': seriesName
              }, h('title', null, chartPointLabel(point, unit))), point, unit, true);
            });
          }
          return [
            ...(isAreaChart ? [h('path', {
              className: `area-chart-area ${seriesClassName}`,
              style: `--chart-entry-index: ${seriesIndex}`,
              d: stackedAreaPath(stackedCoordinates, scaledAreaY),
              'data-chart-series': seriesName,
              'aria-hidden': 'true'
            })] : !isPointChart && hasGaps ? [h('path', {
              className: `line-chart-series ${seriesClassName}${hasWindowHighlight ? ' line-chart-context' : ''}`,
              style: `--chart-entry-index: ${seriesIndex}`,
              pathLength: 1,
              d: lineSegmentPath(renderedCoordinates, gapPrefix),
              fill: 'none',
              'data-chart-series': seriesName
            })] : !isPointChart ? [h('polyline', {
              className: `line-chart-series ${seriesClassName}${hasWindowHighlight ? ' line-chart-context' : ''}`,
              style: `--chart-entry-index: ${seriesIndex}`,
              pathLength: 1,
              points: renderedCoordinates.map(({ x, y }) => `${x},${y}`).join(' '),
              fill: 'none',
              'data-chart-series': seriesName
            })] : []),
            ...(!isAreaChart && !isPointChart && highlightedCoordinates.length > 1
              ? [h(hasGaps ? 'path' : 'polyline', {
                className: `line-chart-series line-chart-current ${seriesClassName}`,
                style: `--chart-entry-index: ${seriesIndex}`,
                pathLength: 1,
                ...(hasGaps ? { d: lineSegmentPath(highlightedCoordinates, gapPrefix) }
                  : { points: highlightedCoordinates.map(({ x, y }) => `${x},${y}`).join(' ') }),
                fill: 'none',
                'data-chart-window': 'current'
              })]
              : []),
            ...(isPointChart && !showInteractivePoints
              ? renderedCoordinates.map(({ point, x, y }) => renderLayerObservationMark(h('circle', {
                className: `${isScatterChart ? 'scatter' : 'dot'}-chart-point ${seriesClassName}`,
                cx: x,
                cy: y,
                r: dotPointRadius,
                'aria-hidden': plotDomain ? undefined : 'true',
                ...(plotDomain ? { tabindex: 0, role: 'img', 'aria-label': chartPointLabel(point, unit) } : {})
              }), point, unit, plotDomain !== null))
              : []),
            ...(showInteractivePoints ? validCoordinates.map(({ point, x, y }, pointIndex) => {
              const stacked = isAreaChart
                ? stackedCoordinates.find((coordinate) => coordinate.x === x && coordinate.valid)
                : null;
              const markY = stacked ? scaledAreaY(stacked.upper) : y;
              return renderLayerObservationMark(h('g', {
                className: `chart-point${point.highlighted === false ? ' chart-point-context' : point.highlighted ? ' chart-point-current' : ''}`,
                style: `--chart-entry-index: ${pointIndex}`,
                tabIndex: 0,
                role: 'img',
                'aria-label': `${chartPointLabel(point, unit)}${point.highlighted === false ? ' (context)' : point.highlighted ? ' (selected window)' : ''}`,
                ...(typeof point.key === 'string' && point.key ? { 'data-chart-point-key': point.key } : {}),
                'data-chart-point-series': seriesName
              },
              h('title', null, chartPointLabel(point, unit)),
              isAreaChart
                ? h('line', {
                  className: `area-chart-point ${seriesClassName}`,
                  x1: x,
                  y1: markY,
                  x2: x + NON_SCALING_POINT_LENGTH,
                  y2: markY
                })
                : isPointChart
                  ? h('circle', {
                    className: `${isScatterChart ? 'scatter' : 'dot'}-chart-point ${seriesClassName}`,
                    cx: x,
                    cy: y,
                    r: dotPointRadius
                  })
                  : h('line', {
                    className: `line-chart-point ${seriesClassName}`,
                    style: `--chart-point-size: ${hasWindowHighlight ? 4 : pointSize}px`,
                    x1: x,
                    y1: y,
                    x2: x + NON_SCALING_POINT_LENGTH,
                    y2: y
                  }),
              renderChartPointTooltip({
                transform: `translate(${Math.min(Math.max(x - 21, 1), 57)} ${Math.max(markY - 12, 1)})`,
                label: chartPointLabel(point, unit)
              })), point, unit, plotDomain !== null);
            }) : [])
          ];
        })
        ),
        h('div', { className: 'line-chart-y-labels', 'aria-hidden': 'true' },
          ...yTickLabels.map((label) => h('span', null, label)))
      ),
      hasWindowHighlight
        ? h('div', { className: 'chart-window-key', 'aria-label': 'Chart window key' },
          h('span', { className: 'chart-window-context' }, 'Previous context'),
          h('strong', null, 'Selected window'))
        : null,
      xValues.length > 0
        ? h(
          'div',
          { className: 'chart-axis timeline-chart-axis', 'data-chart-axis': chartType },
          ...timelineTicks.map((value) => h('span', { title: value }, plotDomain && !plotDomain.temporal ? compactAxisLabel(value) : formatTimelineTick(value)))
        )
        : null
    );
  }

  /**
   * @param {Element} mark
   * @param {ChartPointLike} point
   * @param {{ name: string, symbol: string, significant: number, format?: string } | null} unit
   * @param {boolean} layered
   * @returns {Element}
   */
  function renderLayerObservationMark(mark, point, unit, layered) {
    if (!layered || !point.link) return mark;
    mark.removeAttribute('tabindex');
    mark.removeAttribute('role');
    return svgAnchor({
      ...externalAnchorAttrs(point.link.href, `${chartPointLabel(point, unit)}: ${point.link.label}`),
      ...(point.link.href.startsWith('#') ? { target: undefined, rel: undefined } : {}),
      tabindex: 0,
      className: 'chart-point-link'
    }, mark);
  }

  if (chartType === 'swimlane') {
    return renderSwimlaneChart(points, timeRange);
  }

  const finiteValues = points.map((point) => toNumber(point.y)).filter(Number.isFinite);
  const maximum = Math.max(...finiteValues, 1);
  const plotWidth = BAR_CHART_RIGHT - BAR_CHART_LEFT;
  const barWidth = points.length > 0 ? Math.min(14, (plotWidth * 0.8) / points.length) : 14;
  const seriesClassNames = new Map(series.map((item) => [item.name, item.className]));
  const yTicks = [maximum, maximum / 2, 0];
  const xTickIndexes = sampledIndexes(points.length, MAX_BAR_AXIS_TICKS);
  return renderChartWidgetShell(
    'bar',
    null,
    h(
      'svg',
      { viewBox: '0 0 100 42', role: 'img', 'aria-label': `Bar chart with ${points.length} bars` },
      h(
        'g',
        { className: 'bar-chart-y-axis', 'data-chart-axis': 'y', 'aria-hidden': 'true' },
        ...yTicks.flatMap((value) => {
          const y = BAR_CHART_BOTTOM - ((value / maximum) * BAR_CHART_HEIGHT);
          return [
            h('line', { className: 'bar-chart-grid', x1: BAR_CHART_LEFT, y1: y, x2: BAR_CHART_RIGHT, y2: y }),
            h('text', { x: BAR_CHART_LEFT - 1.5, y: y + 1, 'text-anchor': 'end' }, formatNumber(value, unit))
          ];
        }),
        h('line', {
          className: 'bar-chart-axis',
          x1: BAR_CHART_LEFT,
          y1: BAR_CHART_BOTTOM - BAR_CHART_HEIGHT,
          x2: BAR_CHART_LEFT,
          y2: BAR_CHART_BOTTOM
        })
      ),
      h('line', {
        className: 'bar-chart-axis',
        x1: BAR_CHART_LEFT,
        y1: BAR_CHART_BOTTOM,
        x2: BAR_CHART_RIGHT,
        y2: BAR_CHART_BOTTOM
      }),
      ...points.map((point, index) => {
        const x = BAR_CHART_LEFT + (((index + 0.5) / Math.max(points.length, 1)) * plotWidth) - (barWidth / 2);
        const height = Math.max(1, (Math.max(0, toNumber(point.y)) / maximum) * BAR_CHART_HEIGHT);
        return h('rect', {
          className: `bar-chart-bar ${seriesClassNames.get(point.color ?? 'value') ?? 'chart-series-1'}`,
          style: `--chart-entry-index: ${index}`,
          x,
          y: BAR_CHART_BOTTOM - height,
          width: barWidth,
          height,
          rx: 0.75,
          tabIndex: 0,
          role: 'img',
          'aria-label': chartPointLabel(point, unit)
        }, h('title', null, chartPointLabel(point, unit)));
      }),
      h(
        'g',
        { className: 'bar-chart-x-axis', 'data-chart-axis': 'x', 'aria-hidden': 'true' },
        ...xTickIndexes.map((index) => {
          const point = points[index];
          const x = BAR_CHART_LEFT + (((index + 0.5) / points.length) * plotWidth);
          return h(
            'text',
            { x, y: 41.5, 'text-anchor': 'middle', title: point.x },
            h('title', null, point.x),
            compactAxisLabel(point.x)
          );
        })
      )
    )
  );
}

/** @param {Array<{ x: number, y: number, index: number }>} coordinates @param {number[]} gapPrefix */
function lineSegmentPath(coordinates, gapPrefix) {
  return coordinates.map(({ x, y, index: rowIndex }, index) => {
    const previous = coordinates[index - 1];
    return `${!previous || gapPrefix[rowIndex] > gapPrefix[previous.index] ? 'M' : 'L'} ${x} ${y}`;
  }).join(' ');
}

/**
 * Builds separate closed areas around missing observations; one SVG path
 * retains a bounded DOM shape even when a long series has many gaps.
 * @param {Array<{ x: number, lower: number, upper: number, valid: boolean }>} coordinates
 * @param {(value: number) => number} scaleY
 */
function stackedAreaPath(coordinates, scaleY) {
  /** @type {string[]} */
  const paths = [];
  /** @type {typeof coordinates} */
  let segment = [];
  const flush = () => {
    if (!segment.length) return;
    paths.push([
      ...segment.map(({ x, upper }, index) => `${index === 0 ? 'M' : 'L'} ${x} ${scaleY(upper)}`),
      ...[...segment].reverse().map(({ x, lower }) => `L ${x} ${scaleY(lower)}`),
      'Z'
    ].join(' '));
    segment = [];
  };
  for (const coordinate of coordinates) {
    if (coordinate.valid) segment.push(coordinate);
    else flush();
  }
  flush();
  return paths.join(' ');
}

/**
 * Keeps line-series SVG output bounded while retaining the first, last, and
 * visual extrema (minimum and maximum SVG y-coordinates) in each bucket.
 * @template T
 * @param {Array<T & { y: number }>} coordinates
 * @param {number} limit
 * @returns {Array<T & { y: number }>}
 */
function sampleLineCoordinates(coordinates, limit) {
  if (coordinates.length <= limit) return coordinates;
  if (limit <= 2) return [coordinates[0], coordinates[coordinates.length - 1]];
  // Budget for interior points, excluding the mandatory first/last endpoints.
  const interiorBudget = limit - 2;
  const bucketCount = Math.max(1, Math.floor(interiorBudget / 2));
  const bucketSize = (coordinates.length - 2) / bucketCount;
  const sampled = [coordinates[0]];
  let interiorCount = 0;
  for (let bucket = 0; bucket < bucketCount; bucket += 1) {
    if (interiorCount >= interiorBudget) break;
    const start = 1 + Math.floor(bucket * bucketSize);
    const end = Math.min(coordinates.length - 1, 1 + Math.floor((bucket + 1) * bucketSize));
    let minimumIndex = start;
    let maximumIndex = start;
    for (let index = start + 1; index < end; index += 1) {
      if (coordinates[index].y < coordinates[minimumIndex].y) minimumIndex = index;
      if (coordinates[index].y > coordinates[maximumIndex].y) maximumIndex = index;
    }
    sampled.push(coordinates[Math.min(minimumIndex, maximumIndex)]);
    interiorCount += 1;
    // Only add the second extremum if the interior budget still allows it, so
    // very small limits (e.g. 3) can't push the total output above `limit`.
    if (minimumIndex !== maximumIndex && interiorCount < interiorBudget) {
      sampled.push(coordinates[Math.max(minimumIndex, maximumIndex)]);
      interiorCount += 1;
    }
  }
  sampled.push(coordinates[coordinates.length - 1]);
  return sampled;
}

/**
 * @param {ChartPointLike[]} points
 * @param {string} valueLabel
 * @param {{ name: string, symbol: string, significant: number } | null} unit
 * @returns {HTMLElement}
 */
function renderHeatmapChart(points, valueLabel, unit) {
  const columns = [...new Set(points.map((point) => point.x))].sort((left, right) => left.localeCompare(right));
  const rows = [...new Set(points.map((point) => point.color ?? 'unknown'))].sort((left, right) => left.localeCompare(right));
  const cellCount = columns.length * rows.length;
  if (
    points.length > MAX_HEATMAP_CELLS
    || cellCount > MAX_HEATMAP_CELLS
    || columns.length > MAX_HEATMAP_AXIS_CATEGORIES
    || rows.length > MAX_HEATMAP_AXIS_CATEGORIES
  ) {
    return renderChartWidgetEmptyState(
      'heatmap',
      `This heatmap is too large to display. Limit it to ${MAX_HEATMAP_CELLS} cells and ${MAX_HEATMAP_AXIS_CATEGORIES} categories per axis.`
    );
  }

  const cells = new Map(points.map((point) => [JSON.stringify([point.x, point.color ?? 'unknown']), point]));
  const values = points.map((point) => toNumber(point.y)).filter(Number.isFinite);
  const minimum = Math.min(...values);
  const maximum = Math.max(...values);
  const span = maximum - minimum;
  const plotWidth = HEATMAP_VIEWBOX_WIDTH - HEATMAP_LEFT - HEATMAP_RIGHT;
  const cellWidth = plotWidth / columns.length;
  const viewBoxHeight = HEATMAP_TOP + (rows.length * HEATMAP_CELL_HEIGHT) + HEATMAP_BOTTOM;

  return renderChartWidgetShell(
    'heatmap',
    null,
    h(
      'div',
      { className: 'heatmap-scroll-region', tabIndex: 0, role: 'region', 'aria-label': 'Heatmap chart' },
      h(
        'svg',
        {
          className: 'heatmap-chart',
          style: `--heatmap-columns: ${columns.length}`,
          viewBox: `0 0 ${HEATMAP_VIEWBOX_WIDTH} ${viewBoxHeight}`,
          role: 'img',
          'aria-label': `Heatmap of ${valueLabel}`
        },
        ...columns.map((column, columnIndex) => h(
          'text',
          {
            className: 'heatmap-axis-label heatmap-column-label',
            x: HEATMAP_LEFT + ((columnIndex + 0.5) * cellWidth),
            y: HEATMAP_TOP - 2,
            'text-anchor': 'middle',
            'aria-hidden': 'true'
          },
          h('title', null, column),
          compactAxisLabel(column)
        )),
        ...rows.flatMap((row, rowIndex) => [
          h(
            'text',
            {
              className: 'heatmap-axis-label heatmap-row-label',
              x: HEATMAP_LEFT - 1.5,
              y: HEATMAP_TOP + ((rowIndex + 0.5) * HEATMAP_CELL_HEIGHT) + 1,
              'text-anchor': 'end',
              'aria-hidden': 'true'
            },
            h('title', null, row),
            compactAxisLabel(row)
          ),
          ...columns.map((column, columnIndex) => {
              const point = cells.get(JSON.stringify([column, row]));
              const x = HEATMAP_LEFT + (columnIndex * cellWidth);
              const y = HEATMAP_TOP + (rowIndex * HEATMAP_CELL_HEIGHT);
              if (!point) {
                const label = `${column}, ${row}: no observation`;
                return h('g', {
                  className: 'heatmap-cell heatmap-cell-empty',
                  tabIndex: 0,
                  role: 'img',
                  'aria-label': label
                },
                h('title', null, label),
                h('rect', { x, y, width: cellWidth, height: HEATMAP_CELL_HEIGHT, rx: 0.75 }),
                h('text', {
                  x: x + (cellWidth / 2),
                  y: y + (HEATMAP_CELL_HEIGHT / 2) + 1,
                  'text-anchor': 'middle',
                  'aria-hidden': 'true'
                }, '—'));
              }
              const value = toNumber(point.y);
              const formatted = formatNumber(value, unit);
              const intensity = span > 0 ? 12 + (((value - minimum) / span) * 28) : 26;
              const label = `${column}, ${row}, ${valueLabel}: ${formatted}`;
              return h(
                'g',
                {
                  className: 'heatmap-cell',
                  style: `--heatmap-intensity: ${intensity.toFixed(1)}%`,
                  tabIndex: 0,
                  role: 'img',
                  'aria-label': label
                },
                h('title', null, label),
                h('rect', { x, y, width: cellWidth, height: HEATMAP_CELL_HEIGHT, rx: 0.75 }),
                h('text', {
                  x: x + (cellWidth / 2),
                  y: y + (HEATMAP_CELL_HEIGHT / 2) + 1,
                  'text-anchor': 'middle',
                  'aria-hidden': 'true'
                }, formatted)
              );
          })
        ])
      )
    )
  );
}

/**
 * @param {ChartPointLike[]} points
 * @param {Record<string, unknown> | null} timeRange
 * @returns {HTMLElement}
 */
function renderSwimlaneChart(points, timeRange) {
  const laneDefinitions = swimlaneLaneDefinitions(points);
  const layout = swimlaneLayout(laneDefinitions.length);
  const timestamps = new Float64Array(points.length);
  const laneIndexes = new Int16Array(points.length);
  laneIndexes.fill(-1);
  const counts = Object.fromEntries(SWIMLANE_DEFINITIONS.map(([lane]) => [lane, 0]));
  counts.unknown = 0;
  let plottedCount = 0;
  let firstObserved = Number.POSITIVE_INFINITY;
  let lastObserved = Number.NEGATIVE_INFINITY;
  for (let index = 0; index < points.length; index += 1) {
    const point = points[index];
    const timestamp = Date.parse(String(point.x));
    const lane = swimlaneLane(point, laneDefinitions);
    if (!Number.isFinite(timestamp) || lane === null) continue;
    const status = swimlaneConclusion(point.color ?? point.category) ?? 'unknown';
    timestamps[index] = timestamp;
    laneIndexes[index] = laneDefinitions.findIndex(([candidate]) => candidate === lane);
    const weight = Number.isFinite(point.weight) && Number(point.weight) > 0 ? Number(point.weight) : 1;
    counts[status] += weight;
    plottedCount += weight;
    firstObserved = Math.min(firstObserved, timestamp);
    lastObserved = Math.max(lastObserved, timestamp);
  }
  if (plottedCount === 0) {
    return renderChartWidgetEmptyState('swimlane', 'No workflow runs to show.');
  }
  let start = Date.parse(String(timeRange?.start ?? ''));
  let end = Date.parse(String(timeRange?.end ?? ''));
  if (!Number.isFinite(start)) start = firstObserved;
  if (!Number.isFinite(end)) end = lastObserved;
  if (start === end) {
    start -= 43_200_000;
    end += 43_200_000;
  }
  const span = Math.max(end - start, 1);
  const successes = counts.success;
  const summary = [
    `${formatCount(plottedCount)} runs`,
    `${formatCoveragePercent(successes / plottedCount, '0.0%')} success`,
    `${formatCount(counts.failure)} failed`,
    `${formatCount(counts.skipped)} skipped`,
    `${formatCount(counts['action-required'])} action required`
  ];
  const ticks = Array.from({ length: 4 }, (_, index) => start + ((span * index) / 3));
  /** @param {number} timestamp */
  const xCoordinate = (timestamp) => layout.startX
    + (Math.min(1, Math.max(0, (timestamp - start) / span)) * (layout.endX - layout.startX));
  const sectionsByLane = buildSwimlaneSections(points, timestamps, laneIndexes, laneDefinitions, xCoordinate);

  return renderChartWidgetShell(
    'swimlane',
    null,
    h(
      'ul',
      { className: 'swimlane-summary', 'aria-label': 'Run summary' },
      summary.map((value) => h('li', null, value))
    ),
    h(
      'svg',
      {
        viewBox: `0 0 ${layout.viewBoxWidth} ${layout.viewBoxHeight}`,
        preserveAspectRatio: 'xMinYMid meet',
        role: 'img',
        'aria-label': `Categorical swimlane timeline with ${plottedCount} workflow runs`
      },
      ...laneDefinitions.flatMap(([lane, label], laneIndex) => {
        const y = layout.topY + (laneIndex * layout.laneGap);
        return [
          h('text', { className: 'swimlane-label', x: layout.labelX, y: y + layout.labelBaselineOffset, 'text-anchor': 'end' }, label),
          h('line', { className: 'swimlane-separator', x1: layout.startX, y1: y, x2: layout.endX, y2: y }),
          ...(sectionsByLane.get(lane) ?? []).map((section) => renderSwimlaneSection(section, y))
        ];
      }),
      h('line', { className: 'swimlane-axis', x1: layout.startX, y1: layout.axisY, x2: layout.endX, y2: layout.axisY }),
      ...ticks.map((instant, index) => {
        const x = xCoordinate(instant);
        return [
          h('line', { className: 'swimlane-tick', x1: x, y1: layout.axisY, x2: x, y2: layout.tickEndY }),
          h('text', {
            className: 'swimlane-time-label',
            x,
            y: layout.axisLabelY,
            'text-anchor': index === 0 ? 'start' : index === ticks.length - 1 ? 'end' : 'middle'
          }, formatSwimlaneAxisTime(instant, span))
        ];
      })
    )
  );
}

/**
 * Coalesces observations into a fixed number of visual buckets per lane, then
 * combines neighboring occupied buckets into contiguous sections.
 * @param {ChartPointLike[]} points
 * @param {Float64Array} timestamps
 * @param {Int16Array} laneIndexes
 * @param {Array<[string, string]>} laneDefinitions
 * @param {(timestamp: number) => number} xCoordinate
 */
function buildSwimlaneSections(points, timestamps, laneIndexes, laneDefinitions, xCoordinate) {
  const binCount = MAX_SWIMLANE_SECTIONS_PER_LANE;
  const sectionWidth = (SWIMLANE_LAYOUT.endX - SWIMLANE_LAYOUT.startX) / binCount;
  /** @type {Map<string, Array<Map<string, { count: number, first: number, last: number, status: string, point: ChartPointLike & { lane: string, status: string, timestamp: number } }>>>} */
  const binsByLane = new Map(laneDefinitions.map(([lane]) => [
    lane,
    Array.from({ length: binCount }, () => new Map())
  ]));
  for (let pointIndex = 0; pointIndex < points.length; pointIndex += 1) {
    const laneIndex = laneIndexes[pointIndex];
    if (laneIndex < 0) continue;
    const timestamp = timestamps[pointIndex];
    const lane = laneDefinitions[laneIndex][0];
    const status = swimlaneConclusion(points[pointIndex].color ?? points[pointIndex].category) ?? 'unknown';
    const x = xCoordinate(timestamp);
    const index = Math.min(binCount - 1, Math.max(0, Math.floor((x - SWIMLANE_LAYOUT.startX) / sectionWidth)));
    const bins = /** @type {NonNullable<ReturnType<typeof binsByLane.get>>} */ (binsByLane.get(lane));
    const weight = Number.isFinite(points[pointIndex].weight) && Number(points[pointIndex].weight) > 0
      ? Number(points[pointIndex].weight)
      : 1;
    const bin = bins[index].get(status);
    if (bin) {
      bin.count += weight;
      bin.first = Math.min(bin.first, timestamp);
      bin.last = Math.max(bin.last, timestamp);
    } else {
      bins[index].set(status, { count: weight, first: timestamp, last: timestamp, status, point: { ...points[pointIndex], lane, status, timestamp } });
    }
  }

  /** @type {Map<string, Array<{ lane: string, x1: number, x2: number, count: number, first: number, last: number, status: string, point: ChartPointLike & { lane: string, status: string, timestamp: number } }>>} */
  const sectionsByLane = new Map();
  for (const [lane, bins] of binsByLane) {
    /** @type {Array<{ lane: string, x1: number, x2: number, count: number, first: number, last: number, status: string, point: ChartPointLike & { lane: string, status: string, timestamp: number } }>} */
    const sections = [];
    for (let index = 0; index < bins.length; index += 1) {
      const statusBins = [...bins[index].values()].sort((left, right) => left.status.localeCompare(right.status));
      const statusWidth = sectionWidth / Math.max(1, statusBins.length);
      for (let statusIndex = 0; statusIndex < statusBins.length; statusIndex += 1) {
        const bin = statusBins[statusIndex];
        const previous = sections.at(-1);
        const x1 = SWIMLANE_LAYOUT.startX + (index * sectionWidth) + (statusIndex * statusWidth);
        const x2 = Math.min(SWIMLANE_LAYOUT.endX, x1 + statusWidth);
        if (previous && previous.status === bin.status && Math.abs(previous.x2 - x1) < 1e-9) {
          previous.x2 = x2;
          previous.count += bin.count;
          previous.first = Math.min(previous.first, bin.first);
          previous.last = Math.max(previous.last, bin.last);
        } else {
          sections.push({ lane, x1, x2, ...bin });
        }
      }
    }
    sectionsByLane.set(lane, sections);
  }
  return sectionsByLane;
}

/** @param {{ lane: string, status: string, x1: number, x2: number, count: number, first: number, last: number, point: Record<string, any> }} section @param {number} y */
function renderSwimlaneSection(section, y) {
  const label = section.count === 1
    ? swimlaneTooltipLines(section.point).join(', ')
    : `${formatCount(section.count)} runs in ${section.lane}, ${formatSwimlaneTooltipTime(section.first)} to ${formatSwimlaneTooltipTime(section.last)}`;
  return h('line', {
    className: `chart-point swimlane-mark swimlane-run-mark swimlane-mark-${section.status}`,
    x1: section.x1,
    y1: y,
    x2: section.x2,
    y2: y,
    tabIndex: 0,
    role: 'img',
    'aria-label': label,
    'data-swimlane-lane': section.lane,
    'data-swimlane-count': section.count
  });
}

/** @param {Record<string, any>} point @returns {string[]} */
function swimlaneTooltipLines(point) {
  const source = point.source ?? {};
  const lines = [
    formatSwimlaneTooltipTime(point.timestamp),
    ...(point.lane === point.status ? [] : [`Lane: ${point.lane}`]),
    `Conclusion: ${point.status}`
  ];
  const run = String(source.run ?? '').trim();
  const branch = String(source.branch ?? source.ref ?? source['head-branch'] ?? '').trim();
  const started = Date.parse(String(source['started-at'] ?? ''));
  const ended = Date.parse(String(source['ended-at'] ?? ''));
  if (run) lines.push(`Run #${run}`);
  if (branch) lines.push(`Branch: ${branch}`);
  if (Number.isFinite(started) && Number.isFinite(ended) && ended >= started) {
    lines.push(`Duration: ${formatSwimlaneDuration(ended - started)}`);
  }
  return lines;
}

/** @param {unknown} value @returns {string | null} */
function swimlaneConclusion(value) {
  const conclusion = String(value ?? '').toLowerCase();
  if (SWIMLANE_FAILURES.has(conclusion)) return 'failure';
  return SWIMLANE_DEFINITIONS.some(([lane]) => lane === conclusion) ? conclusion : null;
}

/** @param {ChartPointLike[]} points @returns {Array<[string, string]>} */
function swimlaneLaneDefinitions(points) {
  if (points.every((point) => swimlaneConclusion(point.category ?? point.color) !== null)) {
    return SWIMLANE_DEFINITIONS;
  }
  /** @type {Map<string, string>} */
  const lanes = new Map();
  for (const point of points) {
    const lane = String(point.category ?? '').trim();
    if (lane && !lanes.has(lane)) lanes.set(lane, lane);
  }
  return [...lanes];
}

/** @param {ChartPointLike} point @param {Array<[string, string]>} laneDefinitions */
function swimlaneLane(point, laneDefinitions) {
  if (laneDefinitions === SWIMLANE_DEFINITIONS) return swimlaneConclusion(point.category ?? point.color);
  const lane = String(point.category ?? '').trim();
  return laneDefinitions.some(([candidate]) => candidate === lane) ? lane : null;
}

/** @param {number} laneCount */
function swimlaneLayout(laneCount) {
  const axisY = SWIMLANE_TOP_Y + (SWIMLANE_LANE_GAP * laneCount);
  const tickEndY = axisY + 2;
  const axisLabelY = axisY + 6;
  return {
    ...SWIMLANE_LAYOUT,
    axisY,
    tickEndY,
    axisLabelY,
    viewBoxHeight: axisLabelY + 1.5
  };
}

/** @param {number} instant @param {number} span */
function formatSwimlaneAxisTime(instant, span) {
  if (span <= 86_400_000) {
    return new Intl.DateTimeFormat('en', { hour: 'numeric', minute: '2-digit', timeZone: 'UTC' }).format(new Date(instant));
  }
  return formatShortUtcDate(new Date(instant));
}

/** @param {number} instant */
function formatSwimlaneTooltipTime(instant) {
  const parts = new Intl.DateTimeFormat('en', {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
    timeZone: 'UTC'
  }).formatToParts(new Date(instant));
  /** @param {string} type */
  const value = (type) => parts.find((part) => part.type === type)?.value ?? '';
  return `${value('month')} ${value('day')}, ${value('year')} · ${value('hour')}:${value('minute')}:${value('second')}`;
}

/** @param {number} milliseconds */
function formatSwimlaneDuration(milliseconds) {
  const seconds = Math.round(milliseconds / 1000);
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const remainder = seconds % 60;
  return [hours ? `${hours}h` : '', minutes ? `${minutes}m` : '', `${remainder}s`].filter(Boolean).join(' ');
}

/**
 * @param {number} pointCount
 * @returns {number}
 */
function lineChartPointSize(pointCount) {
  const progress = Math.min(1, Math.max(0, (pointCount - 2) / (MIN_RADIUS_POINT_COUNT - 2)));
  return MAX_LINE_POINT_SIZE - (progress * (MAX_LINE_POINT_SIZE - MIN_LINE_POINT_SIZE));
}

/**
 * @param {number} pointCount
 * @returns {number}
 */
function dotChartPointRadius(pointCount) {
  const progress = Math.min(1, Math.max(0, (pointCount - 2) / (MIN_RADIUS_POINT_COUNT - 2)));
  return MAX_DOT_POINT_RADIUS - (progress * (MAX_DOT_POINT_RADIUS - MIN_DOT_POINT_RADIUS));
}

/**
 * @param {string[]} values
 * @returns {string[]}
 */
function lineChartTimelineTicks(values) {
  const tickCount = Math.min(values.length, MAX_TIMELINE_TICKS);
  if (tickCount < 2) return values;

  return Array.from(
    { length: tickCount },
    (_, index) => values[Math.round((index / (tickCount - 1)) * (values.length - 1))]
  );
}

/**
 * @param {number[]} values
 * @param {number} minimum
 * @param {number} maximum
 * @returns {string[]}
 */
function scatterChartTimeAxisTicks(values, minimum, maximum) {
  const validValues = [...new Set(values.filter(Number.isFinite))];
  const tickCount = Math.min(validValues.length, MAX_TIMELINE_TICKS);
  if (tickCount === 0) return [];
  if (tickCount === 1 || maximum === minimum) return [new Date(minimum).toISOString()];

  // Equal time intervals align these flex-distributed labels with proportional scatter positions.
  return Array.from(
    { length: tickCount },
    (_, index) => new Date(minimum + ((index / (tickCount - 1)) * (maximum - minimum))).toISOString()
  );
}

/**
 * @param {number} valueCount
 * @param {number} maximumCount
 * @returns {number[]}
 */
function sampledIndexes(valueCount, maximumCount) {
  const tickCount = Math.min(valueCount, maximumCount);
  if (tickCount < 2) return Array.from({ length: tickCount }, (_, index) => index);

  return Array.from(
    { length: tickCount },
    (_, index) => Math.round((index / (tickCount - 1)) * (valueCount - 1))
  );
}

/** @param {string} value */
function compactAxisLabel(value) {
  const formatted = formatTimelineTick(value);
  return formatted.length > 12 ? `${formatted.slice(0, 11)}…` : formatted;
}

/**
 * @param {number} value
 * @param {{ name: string, symbol: string, significant: number, format?: string } | null} unit
 */
function formatChartAxisTick(value, unit) {
  if (unit?.format === 'bytes' || unit?.format === 'compact-number') return formatNumber(value, unit);
  if (Math.abs(value) < 10_000) return formatNumber(value, unit);
  if (unit?.format === 'aicc') {
    return new Intl.NumberFormat('en-US', {
      style: 'currency',
      currency: 'USD',
      notation: 'compact',
      maximumFractionDigits: 1
    }).format(value / 100);
  }
  const compact = new Intl.NumberFormat('en', {
    notation: 'compact',
    maximumFractionDigits: 1
  }).format(value);
  if (unit?.format === 'usd') return compact.startsWith('-') ? `-$${compact.slice(1)}` : `$${compact}`;
  return unit && unit.format !== 'number' ? `${compact} ${unit.symbol}` : compact;
}

/** @param {string} label */
function estimateLineChartLabelWidth(label) {
  return [...label].reduce((width, character) => width + (
    /\d/.test(character)
      ? LINE_CHART_LABEL_DIGIT_WIDTH
      : /[.,:\s]/.test(character)
        ? LINE_CHART_LABEL_NARROW_WIDTH
        : /[MW%@]/.test(character)
          ? LINE_CHART_LABEL_WIDE_WIDTH
          : LINE_CHART_LABEL_DEFAULT_WIDTH
  ), 0);
}

/**
 * @param {string} value
 * @returns {string}
 */
function formatTimelineTick(value) {
  if (!/^\d{4}-\d{2}-\d{2}(?:T|$)/.test(value)) return value;
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return value;

  if (!value.includes('T')) return formatShortUtcDate(date);

  const options = { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZone: 'UTC', timeZoneName: 'short' };
  return new Intl.DateTimeFormat('en', /** @type {Intl.DateTimeFormatOptions} */ (options)).format(date);
}

/**
 * @param {{ x: string, y: number | null, color: string | null, source?: Record<string, unknown> }} point
 * @param {{ name: string, symbol: string, significant: number } | null} [unit]
 * @returns {string}
 */
function chartPointLabel(point, unit = null) {
  const clusterCount = Number(point.source?.['cluster-count']);
  return `${point.x}: ${point.y == null ? 'Unavailable' : formatNumber(point.y, unit)}${point.color ? `, ${point.color}` : ''}${clusterCount > 1 ? `, cluster of ${formatNumber(clusterCount, null)} observations` : ''}`;
}
