/**
 * Reusable presentation-only section and metadata chrome helpers for dashboard pages.
 */

import { h } from '../dom.js';
import { renderDisclosureSummaryLabel, renderDlRow, renderListWithFallback, renderSectionHeading } from './ui-primitives.js';
import { formatCount, slugify, titleCase } from './count-formatters.js';
import { createDebug } from '../debug.js';

const debugViewChrome = createDebug('view-chrome');

/**
 * @param {string} pageId
 * @param {string} title
 * @param {HTMLElement[]} content
 * @param {'h2'|'h3'|'h4'} [headingTag]
 * @param {string} [description]
 * @returns {HTMLElement}
 */
export function renderPageSection(pageId, title, content, headingTag = 'h3', description) {
  const headingId = `${pageId}-${slugify(title, 'section')}-heading`;
  return h(
    'section',
    {
      className: 'page-section',
      tabIndex: 0,
      'aria-labelledby': headingId,
      'aria-description': description
    },
    h(headingTag, { id: headingId }, title),
    ...content
  );
}

/**
 * @param {string} pageId
 * @param {string} title
 * @param {HTMLElement} content
 * @returns {HTMLElement}
 */
export function renderTitledRegion(pageId, title, content) {
  return renderPageSection(pageId, title, [content]);
}

/**
 * @param {string} pageId
 * @param {string} title
 * @param {string} listClassName
 * @param {Map<string, number>} counts
 * @returns {HTMLElement}
 */
export function renderSummaryRegion(pageId, title, listClassName, counts) {
  return renderTitledRegion(pageId, title, renderSummaryList(listClassName, counts));
}

/**
 * @param {string} listClassName
 * @param {Map<string, number>} counts
 * @returns {HTMLElement}
 */
export function renderSummaryList(listClassName, counts) {
  const entries = [...counts.entries()];
  return renderListWithFallback(listClassName, entries, ([name, count]) => `${name}: ${count}`, 'No data available.');
}

/**
 * @param {string[]} details
 * @returns {HTMLElement}
 */
export function renderContextList(details) {
  return h('ul', { className: 'view-context' }, details.map((detail) => h('li', null, detail)));
}

/**
 * @param {string} className
 * @param {Array<Record<string, unknown>>} rows
 * @returns {HTMLElement}
 */
export function renderDefinitionList(className, rows) {
  return h('dl', { className }, ...renderDefinitionListRows(rows));
}

/**
 * @param {Array<Record<string, unknown>>} rows
 * @returns {HTMLElement[]}
 */
export function renderDefinitionListRows(rows) {
  return rows.map((row) => renderDlRow(String(row.label ?? ''), String(row.value ?? '')));
}

/**
 * @param {string[]} contextDetails
 * @returns {HTMLElement[]}
 */
export function renderContextChrome(contextDetails) {
  return contextDetails.length > 0 ? [renderContextList(contextDetails)] : [];
}

/**
 * @param {string[]} lines
 * @returns {HTMLElement[]}
 */
export function renderViewChrome(lines) {
  return lines.map((line) => h('p', { className: 'view-metadata' }, line));
}

/**
 * @param {{ 'as-of': string, completeness: string, freshness: string }} metadata
 * @param {string[]} contextDetails
 * @returns {HTMLElement[]}
 */
export function renderViewSectionChrome(metadata, contextDetails) {
  const context = renderContextChrome(contextDetails);
  if (metadata.completeness === 'complete' && metadata.freshness === 'fresh') return context;
  return [
    h('p', { className: 'view-metadata', role: 'status' },
      `Evidence completeness: ${metadata.completeness}; freshness: ${metadata.freshness}.`),
    ...context
  ];
}

/**
 * Wraps a rendered supplemental view in the shared `<details class="view-disclosure">`
 * summary toggle, used by both the synchronous and async custom-page renderers to hide
 * supplemental views behind a "Show details" summary.
 * @param {HTMLElement} rendered the already-rendered view element, with `custom-view` and
 *   `data-view-layout` already removed by the caller
 * @param {string} layout
 * @param {string} disclosure
 * @param {string} title
 * @returns {HTMLElement}
 */
export function renderViewDisclosure(rendered, layout, disclosure, title) {
  return h(
    'details',
    {
      className: 'custom-view view-disclosure',
      'data-view-layout': layout,
      'data-disclosure': disclosure
    },
    h(
      'summary',
      { className: 'view-disclosure-summary' },
      ...renderDisclosureSummaryLabel(title, 'view-disclosure-hint')
    ),
    rendered
  );
}

/**
 * @param {'available'|'empty'|'unavailable'} availability
 * @returns {string}
 */
export function customViewAvailabilityMessage(availability) {
  return availability === 'available'
    ? 'Data available.'
    : availability === 'empty'
      ? 'No observations matched the effective context.'
      : 'This view cannot be shown because its data source is unavailable.';
}

/**
 * @param {string | null} sourceName
 * @param {string[]} contextDetails
 * @param {unknown} [queryError]
 * @param {unknown} [queryDiagnostic]
 * @returns {HTMLElement[]}
 */
export function renderCustomViewStateDetails(sourceName, contextDetails, queryError, queryDiagnostic) {
  const details = [];
  if (sourceName) {
    details.push(h('p', { className: 'view-source' }, `Affected source: ${sourceName}`));
  }
  const hasControlCharacter = typeof queryDiagnostic === 'string'
    && [...queryDiagnostic].some((character) => character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127);
  const diagnostic = typeof queryDiagnostic === 'string'
    && queryDiagnostic.length <= 1024
    && queryDiagnostic.startsWith('$.dashboard.queries[')
    && !hasControlCharacter
    ? queryDiagnostic
    : null;
  if (diagnostic) {
    details.push(h('p', { className: 'view-diagnostic' }, `Query diagnostic: ${diagnostic}`));
  }
  const error = queryError && typeof queryError === 'object' && !Array.isArray(queryError)
    ? /** @type {{ code?: unknown, source?: unknown }} */ (queryError)
    : null;
  const dependency = error?.code === 'input-unavailable' && typeof error.source === 'string'
    && error.source.length <= 256
    && /^[a-z][a-z0-9]*(?:-[a-z0-9]+)*(?::[a-z][a-z0-9]*(?:-[a-z0-9]+)*)*$/.test(error.source)
    ? error.source
    : null;
  if (dependency && dependency !== sourceName) {
    details.push(h('p', { className: 'view-source' }, `Unavailable query dependency: ${dependency}`));
  }
  details.push(...renderContextChrome(contextDetails));
  debugViewChrome({
    event: 'custom-view-state-details',
    hasSourceName: Boolean(sourceName),
    hasQueryError: Boolean(error),
    hasQueryDiagnostic: Boolean(diagnostic),
    dependencyResolved: Boolean(dependency),
    detailCount: details.length
  });
  return details;
}

/**
 * @param {Array<{ sourceName: string, sourceId: string, sourceKind: string, asOf: string }>} items
 * @returns {HTMLElement}
 */
export function renderProvenanceList(items) {
  return renderListWithFallback(
    'provenance-list',
    items,
    (item) => `${item.sourceName}: ${item.sourceId} (${item.sourceKind}) — as of ${item.asOf}`,
    'No source provenance available for this page.'
  );
}

/**
 * @param {string} pageId
 * @param {Array<{ sourceName: string, sourceId: string, sourceKind: string, asOf: string }>} items
 * @returns {HTMLElement}
 */
export function renderProvenanceSection(pageId, items) {
  return renderTitledRegion(pageId, 'Provenance', renderProvenanceList(items));
}

/**
 * @param {string} title
 * @param {Node} content
 * @param {'h2'|'h3'|'h4'} [headingTag]
 * @returns {HTMLElement}
 */
export function renderMetadataSection(title, content, headingTag = 'h2') {
  return h('section', null, h(headingTag, null, title), content);
}

/**
 * @param {string} headingId
 * @param {string} heading
 * @param {Node[]} body
 * @param {{
 *   sectionClassName?: string,
 *   headingTag?: 'h2'|'h3'|'h4',
 *   bodyClassName?: string,
 *   bodyAttributes?: Record<string, unknown>
 * }} [options]
 * @returns {HTMLElement}
 */
export function renderTitledBodySection(headingId, heading, body, options = {}) {
  const {
    sectionClassName,
    headingTag = 'h3',
    bodyClassName,
    bodyAttributes = {}
  } = options;
  const bodyProps = bodyClassName ? { ...bodyAttributes, className: bodyClassName } : bodyAttributes;
  const headingProps = headingId ? { id: headingId } : null;
  return h(
    'section',
    sectionClassName ? { className: sectionClassName } : null,
    h(headingTag, headingProps, heading),
    h('div', bodyProps, ...body)
  );
}

/**
 * @param {string} pageId
 * @param {{ id: string, title?: string, description?: string, layout: 'full'|'wide'|'narrow'|'horizontal', views: string[], ['count-source']?: string, ['count-sources']?: string[], ['count-field']?: string, ['count-label']?: string }} section
 * @param {number | null} count
 * @returns {HTMLElement}
 */
export function renderLayoutSectionChrome(pageId, section, count) {
  const title = section.title ?? titleCase(section.id);
  const headingId = `${pageId}-${section.id}-layout-heading`;
  const countTitle = count !== null && section['count-label']
    ? `${formatCount(count)} ${section['count-label']}`
    : null;
  if (section.layout === 'horizontal' && countTitle) {
    return h(
      'header',
      { className: 'layout-section-header layout-section-header-summary' },
      h('h2', { id: headingId }, countTitle)
    );
  }
  const sectionHeading = renderSectionHeading({
    kicker: titleCase(section.id),
    id: headingId,
    title,
    headingTag: 'h2',
    description: section.description
  });
  return h(
    'header',
    { className: 'layout-section-header' },
    sectionHeading,
    countTitle
      ? h('strong', null, countTitle)
      : null
  );
}
