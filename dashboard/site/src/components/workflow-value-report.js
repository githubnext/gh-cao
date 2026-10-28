/**
 * Presentation of query-prepared workflow value evidence.
 * Counts, relationships, comparison and coverage belong to Dashboard Language.
 */
import { h, svgAnchor } from '../dom.js';
import { createDebug } from '../debug.js';
import { render } from '../reactive.js';
import { formatNumber } from '../view-formatters.js';
import { bindFactorySources, createFactoryScope } from './factory-elements.js';
import { externalAnchorAttrs } from './link-content.js';
import { renderTableRegion } from './table-region.js';
import { isSafeHttpsUrl, renderIconSpan, renderLoadingMessage, renderVisualizationEmptyMessage } from './ui-primitives.js';

const debugWorkflowValueReport = createDebug('workflow-value-report');
const EVIDENCE_PAGE_SIZE = 25;

/** @typedef {Record<string, unknown>} Row */
/** @typedef {import('./ui-elements.js').ElementRenderContext} Context */
/** @typedef {string | HTMLElement | { content: string | HTMLElement, sortValue?: string | number }} EvidenceCell */

/** @param {unknown} value */
const text = value => typeof value === 'string' ? value : '';
/** @param {unknown} value */
const quantity = value => typeof value === 'number' && Number.isFinite(value) ? formatNumber(value) : 'Unknown';
/** @param {unknown} value */
const date = value => text(value) ? new Date(text(value)).toLocaleDateString('en-US', { timeZone: 'UTC', year: 'numeric', month: 'short', day: 'numeric' }) : 'Not retained';
/** @param {number} value @param {string} singular @param {string} [plural] */
const countLabel = (value, singular, plural = `${singular}s`) => `${formatNumber(value)} ${value === 1 ? singular : plural}`;
/** @param {string | HTMLElement} content @param {string | number} sortValue */
const sortableCell = (content, sortValue) => ({ content, sortValue });

/** @param {unknown} label @param {unknown} url */
function evidenceLink(label, url) {
  const name = text(label) || 'Evidence';
  return typeof url === 'string' && isSafeHttpsUrl(url) ? h('a', externalAnchorAttrs(url, name), name) : h('span', null, name);
}

/**
 * @param {Context} context
 * @param {(rows: Row[][]) => HTMLElement} draw
 */
function boundEvidence(context, draw) {
  const scope = createFactoryScope();
  const bindings = bindFactorySources(context.sources, context.sourceNames, {
    pageId: context.pageId, viewId: context.viewId, viewIndex: context.viewIndex,
    sourceNames: context.sourceNames, routeParameters: context.routeParameters, queryContext: context.queryContext
  }, { requestMissingSources: !context.sourcesSubscribed });
  const root = h('section', { className: 'workflow-value-report-bound', 'aria-label': context.title });
  render(root, () => {
    const header = bindings[context.sourceNames[0]];
    if (context.sourceNames.some(name => bindings[name].pending() && !bindings[name].source())) {
      return renderLoadingMessage('Loading published value evidence...');
    }
    if (context.sourceNames.some(name => bindings[name].unavailable())) {
      debugWorkflowValueReport({ event: 'evidence-unavailable', sources: context.sourceNames.length });
      return renderVisualizationEmptyMessage('Value evidence is unavailable. Check collection and ingestion; missing evidence is not zero value.');
    }
    if (!header?.source() || header.empty()) {
      debugWorkflowValueReport({ event: 'evidence-empty', sources: context.sourceNames.length });
      return renderVisualizationEmptyMessage('No value evidence has been published for this selection.');
    }
    return draw(context.sourceNames.map(name => bindings[name].rows()));
  }, { signal: scope.signal });
  scope.bind(root);
  return root;
}

/** @param {Context} context */
export function renderWorkflowValueReport(context) {
  let evidenceExpanded = false;
  /** @type {string | null} */
  let activeEvidenceSection = null;
  return boundEvidence(context, ([headers, sources, requirements, outputs, trails, files, snapshots, checks, limitations, origins]) => {
    const summary = headers[0];
    const root = h('article', { className: 'workflow-value-report' });
    const evidence = h('details', { className: 'value-report-evidence', open: evidenceExpanded,
      onToggle: (/** @type {Event} */ event) => {
        if (event.currentTarget instanceof HTMLDetailsElement) evidenceExpanded = event.currentTarget.open;
      } },
      h('summary', null,
        h('span', null, 'Explore evidence'),
        h('span', { className: 'value-report-evidence-count' }, '5 categories')));
    /** @param {string} id @param {Event} event */
    const reveal = (id, event) => {
      debugWorkflowValueReport({ event: 'evidence-revealed', section: id });
      const owner = event.currentTarget instanceof Element ? event.currentTarget.closest('.workflow-value-report') : null;
      const disclosure = owner?.querySelector('.value-report-evidence');
      if (disclosure instanceof HTMLDetailsElement) disclosure.open = true;
      evidenceExpanded = true;
      const destination = owner?.querySelector(`[data-value-section="${id}"]`);
      if (destination instanceof HTMLDetailsElement) {
        activeEvidenceSection = id;
        for (const category of owner?.querySelectorAll('[data-value-section]') ?? []) {
          if (category instanceof HTMLDetailsElement) category.open = category === destination;
        }
        destination.scrollIntoView?.({ block: 'start' });
        const categorySummary = destination.querySelector(':scope > summary');
        if (categorySummary instanceof HTMLElement) categorySummary.focus({ preventScroll: true });
      }
    };
    /** @param {string} label @param {unknown} value @param {string} unit @param {string} destination */
    const trail = (label, value, unit, destination) => h('div', null,
      h('span', null, label),
      h('button', { type: 'button', onClick: reveal.bind(null, destination), 'aria-label': `${label}: ${quantity(value)} ${unit}. Explore evidence.` },
        h('strong', null, quantity(value)), ` ${unit}`));
    const back = text(summary['workflow-value-href']);
    root.append(h('header', { className: 'value-report-heading' },
      h('a', { href: '#page-value' }, '← Value'),
      h('h2', null, back ? h('a', { href: back }, text(summary['workflow-name']) || text(summary.workflow))
        : text(summary['workflow-name']) || text(summary.workflow)),
      h('p', { className: 'muted', title: `Captured ${text(summary['observed-at'])}; evidence cutoff ${text(summary['window-end'])}` },
        `Evidence captured ${date(summary['observed-at'])} · Cutoff ${date(summary['window-end'])}`),
      h('h3', null, 'Workflow intent'), h('p', null, text(summary.goal))));
    const outputTrail = trail('Safe outputs', summary['output-count'], text(summary['output-unit']) || 'outputs', 'outputs');
    outputTrail.append(h('p', { className: 'muted' }, `${quantity(summary['receipt-outputs'])} receipt-confirmed`));
    const implementationTrail = trail('Accepted changes', summary['accepted-implementations'], text(summary['implementation-unit']) || 'changes', 'trails');
    implementationTrail.append(h('p', { className: 'muted' }, 'Linked to outputs'));
    const resultTrail = trail(summary['complete-report'] === true ? 'Measured result' : 'Known subtotal', summary['native-result'], text(summary['metric-unit']), 'files');
    resultTrail.append(h('p', { className: 'muted' }, text(summary['result-label'])));
    if (summary['show-workflow-trail'] === true) {
      root.append(h('div', { className: 'value-report-trail' }, outputTrail, implementationTrail, resultTrail),
        h('p', { className: 'muted value-report-scope' },
          `Retained cohort since ${date(summary['window-start'])}. Output creation is not acceptance; these are different populations, not a conversion rate.`));
    }
    const state = h('section', { className: 'value-report-outcome', 'aria-label': 'Independent repository outcome' },
      h('h3', null, renderIconSpan('value-report-icon', 'graph'), text(summary['snapshot-name']) || 'Repository outcome'),
      h('div', { className: 'value-report-outcome-body' },
        h('div', null,
          h('a', { href: text(summary['current-snapshot-href']) || text(summary['report-href']), className: 'value-report-number' },
            quantity(summary['current-value']), h('span', null, ` ${text(summary['snapshot-unit'])}`)),
          h('p', { className: 'value-report-movement', 'data-movement': text(summary.movement) }, text(summary['change-label'])),
          h('p', { className: 'muted' }, text(summary['comparison-label']))),
        renderSnapshotPlot(snapshots, text(summary['direction-label']))));
    root.append(state,
      h('section', { className: 'value-report-recent', 'aria-label': 'Recent accepted contribution' },
        h('h3', null, `Recent ${text(summary['result-label']) || 'linked results'}`),
        h('p', null, h('strong', null, quantity(summary['recent-result'])), ` ${text(summary['metric-unit'])} in 28 days · ${quantity(summary['previous-result'])} in the previous 28`)),
      h('button', { type: 'button', className: 'value-report-quality', onClick: reveal.bind(null, 'checks') }, 'Quality checks and evidence limits'));
    /** @param {string} id @param {string} title @param {string} count @param {HTMLElement} content */
    const section = (id, title, count, content) => h('details', {
      className: 'value-report-evidence-category',
      'data-value-section': id,
      open: activeEvidenceSection === id,
      onToggle: (/** @type {Event} */ event) => {
        if (!(event.currentTarget instanceof HTMLDetailsElement)) return;
        const category = event.currentTarget;
        if (category.open) {
          activeEvidenceSection = id;
          const group = category.closest('.value-report-evidence');
          for (const other of group?.querySelectorAll('[data-value-section]') ?? []) {
            if (other instanceof HTMLDetailsElement && other !== category) other.open = false;
          }
        } else if (activeEvidenceSection === id) {
          activeEvidenceSection = null;
        }
      }
    },
    h('summary', null,
      h('span', null, title),
      h('span', { className: 'value-report-evidence-count' }, count)),
    h('div', { className: 'value-report-evidence-content' }, content));
    evidence.append(
      h('p', { className: 'muted value-report-evidence-intro' }, 'Linked measurements demonstrate a contribution, not exclusive causal credit or proof that every safeguard passed. Independent repository state includes everyone’s work.'),
      section('intent', 'Intent sources and acceptance requirements',
        `${countLabel(sources.length, 'source')} · ${countLabel(requirements.length, 'requirement')}`, h('div', null,
        h('ul', null, ...sources.map(row => h('li', null, evidenceLink(row.label, row.url)))),
        h('ul', null, ...requirements.map(row => h('li', null, text(row.label)))))),
      section('outputs', 'Retained safe outputs',
        `${countLabel(outputs.length, 'output')} · ${countLabel(origins.length, 'origin link')}`, h('div', null, evidenceTable(
        ['Output', 'Created', 'State at cutoff', 'Origin'],
        outputs.map(row => [evidenceLink(row.label, row.url), sortableCell(date(row.at), text(row.at)), text(row.state), text(row.origin)]),
        'No retained output facts. Missing context is not an empty cohort.',
        { id: 'outputs', label: 'Search retained safe outputs', noun: 'output' }),
        evidenceTable(['Output', 'Origin evidence'], origins.map(row => [
          evidenceLink(row['output-label'], row['output-url']), evidenceLink(row.label, row.url)
        ]), 'No producer evidence links were retained.',
        { id: 'output-origins', label: 'Search output origin evidence', noun: 'link' }))),
      section('trails', 'Outputs → accepted implementations',
        countLabel(trails.length, 'trail'), evidenceTable(
        ['Safe output', 'Accepted implementation', 'Accepted', 'Native effect'],
        trails.map(row => [evidenceLink(row['output-label'], row['output-url']),
          evidenceLink(row['implementation-label'], row['implementation-url']),
          sortableCell(date(row['trail-accepted-at']), text(row['trail-accepted-at'])),
          sortableCell(quantity(row['implementation-value']), typeof row['implementation-value'] === 'number' ? row['implementation-value'] : '')]),
        'No accepted implementation trails were retained. Closed issues alone are not acceptance.',
        { id: 'trails', label: 'Search accepted implementation trails', noun: 'trail' })),
      section('files', 'Measured changes and Git evidence',
        countLabel(files.length, 'evidence item'), evidenceTable(
        ['Implementation', 'Evidence', 'Observed relationship'],
        files.map(row => [evidenceLink(row['implementation-label'], row['implementation-url']), evidenceLink(row.label, row.url),
          row.at ? sortableCell(date(row.at), text(row.at)) : 'Not applicable']),
        'Item-level implementation evidence was not retained.',
        { id: 'files', label: 'Search measured changes and Git evidence', noun: 'item' })),
      section('checks', 'Quality checks and limitations',
        `${countLabel(checks.length, 'check')} · ${countLabel(limitations.length, 'limitation')}`, h('div', null,
        checks.length ? h('ul', null, ...checks.map(row => h('li', null,
          h('strong', null, `${text(row.label)}: ${text(row.status)}`), ` — ${text(row.detail)}`)))
          : h('p', null, 'Quality checks were not retained; full acceptance is not verified.'),
        h('ul', null, ...limitations.map(row => h('li', null, text(row.detail) || text(row.label)))),
        h('p', null, text(summary['metric-formula'])),
        h('p', { className: 'muted' }, `Frozen metric ${text(summary['metric-id'])} · Version ${text(summary['metric-version'])} · ${text(summary['contract-digest'])}`))));
    root.append(evidence);
    return root;
  });
}

/** @param {Context} context */
export function renderWorkflowValueSnapshot(context) {
  return boundEvidence(context, ([snapshots, items]) => {
    const snapshot = snapshots[0];
    return h('article', { className: 'workflow-value-report' },
      h('a', { href: `#page-value-report?report=${encodeURIComponent(text(snapshot['report-id']))}` }, '← Value report'),
      h('h2', null, `Measured snapshot · ${date(snapshot.at)}`),
      h('p', null, h('strong', null, quantity(snapshot.value)), ' counted items at the retained revision.'),
      snapshot['items-available'] !== true
        ? h('p', { className: 'muted' }, 'Item-level evidence was not retained. The repository revision alone does not identify the measured population.')
        : items.length ? h('ul', { className: 'value-report-items' }, ...items.map(row =>
          h('li', null, evidenceLink(row.label, row.url))))
          : h('p', null, 'The retained item list is empty.'),
      h('p', null, evidenceLink('Full repository revision (context)', snapshot.url)));
  });
}

/**
 * @param {string[]} headings
 * @param {EvidenceCell[][]} rows
 * @param {string} empty
 * @param {{ id: string, label: string, noun: string, plural?: string }} options
 */
function evidenceTable(headings, rows, empty, options) {
  if (!rows.length) return h('p', { className: 'muted' }, empty);
  const bodyRows = rows.map(row => h('tr', null, ...row.map(value => {
    const cell = typeof value === 'object' && value !== null && !(value instanceof HTMLElement)
      ? value
      : { content: value };
    return h('td', cell.sortValue === undefined ? null : { 'data-sort-value': String(cell.sortValue) }, cell.content);
  })));
  const initialRows = bodyRows.slice(0, EVIDENCE_PAGE_SIZE);
  const continuation = bodyRows.length > initialRows.length
    ? {
        token: String(initialRows.length),
        totalRows: bodyRows.length,
        load: async (/** @type {string} */ token) => {
          const offset = Number(token);
          if (!Number.isInteger(offset) || offset < 0 || offset > bodyRows.length) {
            throw new TypeError('Invalid workflow value evidence continuation.');
          }
          const nextOffset = Math.min(offset + EVIDENCE_PAGE_SIZE, bodyRows.length);
          return {
            rows: bodyRows.slice(offset, nextOffset).filter(row => row instanceof HTMLTableRowElement),
            continuationToken: nextOffset < bodyRows.length ? String(nextOffset) : undefined
          };
        }
      }
    : undefined;
  const region = renderTableRegion({
    tableClassName: 'custom-table',
    regionClassName: 'value-report-table-region',
    emptyMessage: empty,
    colSpan: headings.length,
    headCells: headings,
    bodyRows: initialRows,
    filterLabel: options.label,
    filterPlaceholder: 'Search this evidence category',
    lazyList: true,
    pageSize: EVIDENCE_PAGE_SIZE,
    continuation,
    resultNoun: options.noun,
    resultNounPlural: options.plural,
    sortable: true
  });
  region.dataset.evidenceTable = options.id;
  return region;
}

/** @param {Row[]} snapshots @param {string} direction */
function renderSnapshotPlot(snapshots, direction) {
  if (!snapshots.length) return renderVisualizationEmptyMessage('Independent repository state has not been measured.');
  const values = snapshots.map(row => Number(row.value));
  const low = Math.min(0, ...values), high = Math.max(1, ...values);
  const first = Date.parse(text(snapshots[0].at)), last = Date.parse(text(snapshots.at(-1)?.at));
  /** @param {Row} row */
  const x = row => first === last ? 250 : 32 + (Date.parse(text(row.at)) - first) / (last - first) * 436;
  /** @param {Row} row */
  const y = row => 44 + (high - Number(row.value)) / (high - low) * 88;
  const svg = h('svg', { viewBox: '0 0 500 180', role: 'group', 'aria-label': 'Independent measured snapshots' },
    h('title', null, 'Dashed lines guide the eye between measured observations, not continuous monitoring. Select a dot for its counted items.'),
    h('text', { x: first === last ? 32 : 250, y: 28, 'text-anchor': first === last ? 'start' : 'middle', class: 'value-snapshot-direction' }, direction));
  for (const [index, snapshot] of snapshots.entries()) {
    if (index) svg.append(h('line', { class: 'value-snapshot-guide', 'aria-hidden': 'true',
      x1: x(snapshots[index - 1]), y1: y(snapshots[index - 1]), x2: x(snapshot), y2: y(snapshot) }));
    const label = `${date(snapshot.at)}: ${quantity(snapshot.value)}. Inspect counted items.`;
    const anchor = svgAnchor({ href: text(snapshot['snapshot-href']), 'aria-label': label },
      h('title', null, label), h('circle', { cx: x(snapshot), cy: y(snapshot), r: 5 }));
    if (index === 0 || index === snapshots.length - 1) {
      anchor.append(h('text', { x: x(snapshot), y: y(snapshot) - 12, 'text-anchor': 'middle', class: 'value-snapshot-label' }, quantity(snapshot.value)),
        h('text', { x: x(snapshot), y: 157, 'text-anchor': first === last ? 'middle' : index === 0 ? 'start' : 'end', class: 'value-snapshot-date' },
          new Date(text(snapshot.at)).toLocaleDateString('en-US', { timeZone: 'UTC', year: 'numeric', month: 'short' })));
    }
    svg.append(anchor);
  }
  return h('div', { className: 'value-report-plot' }, svg, h('p', { className: 'muted' }, 'Dashed guide · Select measured dots'));
}
