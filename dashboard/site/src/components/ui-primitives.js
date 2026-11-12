/**
 * Presentation-only reusable UI primitives shared across dashboard components.
 */

import { h } from '../dom.js';
import { createDebug } from '../debug.js';
import { octicon } from '../octicons.js';
import { effect, state } from '../reactive.js';
import { createFactoryScope } from './factory-elements.js';

const debugUiPrimitives = createDebug('ui-primitives');

/**
 * @typedef {{
 *   kicker: string,
 *   id: string,
 *   title: string,
 *   description?: string,
 *   summary?: string,
 *   headingTag?: 'h2'|'h3'|'h4'
 * }} SectionHeadingOptions
 */

/**
 * @param {SectionHeadingOptions} options
 * @returns {HTMLElement}
 */
export function renderSectionHeading({
  kicker,
  id,
  title,
  description,
  summary,
  headingTag = 'h3'
}) {
  return h(
    'div',
    { className: 'section-heading' },
    h(
      'div',
      null,
      h('span', { className: 'scope-kicker' }, kicker),
      h(headingTag, { id }, title),
      description ? h('p', null, description) : null
    ),
    summary ? h('strong', null, summary) : null
  );
}

/**
 * Renders a `<tr>` of `<th scope="col">` header cells from plain label
 * strings. Shared by the campaign summary table and the workflow
 * operational-grader observation table, which both build a single header
 * row from a flat list of column labels.
 * @param {string[]} labels
 * @returns {HTMLElement}
 */
export function renderTableHeadRow(labels) {
  return h('tr', null, ...labels.map((label) => h('th', { scope: 'col' }, label)));
}

/**
 * Renders a single `<div><dt>{term}</dt><dd>{description}</dd></div>` row for
 * use inside a `<dl>`, optionally followed by a trailing detail paragraph.
 * Shared by vital-stat metrics, metadata summary rows, and definition-list
 * rows across the dashboard.
 * @param {Node | string | Array<Node | string | null>} term
 * @param {unknown} description
 * @param {string} [detail]
 * @returns {HTMLElement}
 */
export function renderDlRow(term, description, detail) {
  return h('div', null, h('dt', null, ...(Array.isArray(term) ? term : [term])), h('dd', null, description), detail ? h('p', null, detail) : null);
}

/**
 * @param {string} label
 * @param {unknown} value
 * @param {string} [detail]
 * @returns {HTMLElement}
 */
export function renderVitalStat(label, value, detail) {
  return renderDlRow(label, value, detail);
}

/**
 * Renders a `<span><strong>{label}</strong>{value}</span>` inline metadata
 * pair. Shared by the horizon tooltip window summary and the control-plane
 * readiness snapshot meta row, which both present a bolded label followed by
 * a plain-text value inside a single inline span.
 * @param {string} label
 * @param {unknown} value
 * @returns {HTMLElement}
 */
export function renderLabeledSpan(label, value) {
  return h('span', null, h('strong', null, label), value);
}

/**
 * @param {{
 *   id: string,
 *   label: string,
 *   description?: string,
 *   icon?: Node,
 *   content?: Node | Node[],
 *   trigger?: HTMLElement,
 *   className?: string,
 *   contentClassName?: string,
 *   viewportAnchored?: boolean,
 *   mobilePopup?: boolean,
 *   signal?: AbortSignal
 * }} options
 * @returns {HTMLElement}
 */
export function renderTooltip({ id, label, description, icon, content, trigger, className, contentClassName, viewportAnchored = false, mobilePopup = false, signal }) {
  const tooltipTrigger = trigger ?? h(
    'button',
    {
      type: 'button',
      className: 'tooltip-trigger'
    },
    icon
  );
  tooltipTrigger.setAttribute('aria-label', label);
  tooltipTrigger.setAttribute('aria-describedby', id);
  const tooltip = h(
    'span',
    { className: ['tooltip-help', className].filter(Boolean).join(' ') },
    tooltipTrigger,
    h(
      'span',
      { id, className: ['tooltip-content', contentClassName].filter(Boolean).join(' '), role: 'tooltip' },
      description ? h('span', { className: 'tooltip-description' }, description) : null,
      ...(Array.isArray(content) ? content : [content])
    )
  );
  const tooltipContent = /** @type {HTMLElement} */ (tooltip.lastElementChild);
  if (viewportAnchored) tooltipContent.classList.add('tooltip-content-viewport');
  const scope = createFactoryScope({ signal });
  let pointerInside = false;
  let focusInside = false;
  let trackingViewport = false;
  let popupOpen = false;
  const updatePosition = () => {
    if (!pointerInside && !focusInside && !popupOpen) return;
    const triggerRect = tooltipTrigger.getBoundingClientRect();
    const rootRect = tooltip.getBoundingClientRect();
    const contentRect = tooltipContent.getBoundingClientRect();
    if (contentRect.width <= 0 || contentRect.height <= 0) return;

    const margin = 14;
    const viewportWidth = window.innerWidth || document.documentElement.clientWidth;
    const viewportHeight = window.innerHeight || document.documentElement.clientHeight;
    const maxLeft = Math.max(margin, viewportWidth - margin - contentRect.width);
    const left = Math.min(Math.max(triggerRect.right - contentRect.width, margin), maxLeft);
    const below = triggerRect.bottom + 8;
    const above = triggerRect.top - contentRect.height - 8;
    const top = below + contentRect.height <= viewportHeight - margin
      ? below
      : Math.max(margin, above);
    const fixed = viewportAnchored || tooltip.classList.contains('tooltip-mobile-popup');
    tooltipContent.style.left = `${fixed ? left : left - rootRect.left}px`;
    tooltipContent.style.right = 'auto';
    tooltipContent.style.top = `${fixed ? top : top - rootRect.top}px`;
  };
  const startViewportTracking = () => {
    if (trackingViewport || scope.signal.aborted) return;
    trackingViewport = true;
    window.addEventListener('resize', updatePosition, { signal: scope.signal });
    window.addEventListener('scroll', updatePosition, { capture: true, signal: scope.signal });
  };
  const stopViewportTracking = () => {
    if (!trackingViewport || pointerInside || focusInside || popupOpen) return;
    trackingViewport = false;
    window.removeEventListener('resize', updatePosition);
    window.removeEventListener('scroll', updatePosition, true);
  };
  tooltip.addEventListener('pointerenter', () => {
    pointerInside = true;
    startViewportTracking();
    updatePosition();
  }, { signal: scope.signal });
  tooltip.addEventListener('pointerleave', () => {
    pointerInside = false;
    stopViewportTracking();
  }, { signal: scope.signal });
  tooltip.addEventListener('focusin', () => {
    focusInside = true;
    startViewportTracking();
    updatePosition();
  }, { signal: scope.signal });
  tooltip.addEventListener('focusout', (event) => {
    focusInside = event.relatedTarget instanceof Node && tooltip.contains(event.relatedTarget);
    stopViewportTracking();
  }, { signal: scope.signal });
  if (mobilePopup) {
    if (!(tooltipTrigger instanceof HTMLButtonElement)) throw new TypeError('Mobile popups require a button trigger.');
    const media = window.matchMedia?.('(max-width: 700px)');
    const syncMode = () => {
      if (popupOpen && tooltipContent.isConnected) tooltipContent.hidePopover();
      popupOpen = false;
      const mobile = media?.matches === true;
      tooltip.classList.toggle('tooltip-mobile-popup', mobile);
      if (mobile) {
        tooltipContent.setAttribute('popover', 'auto');
        tooltipContent.setAttribute('role', 'dialog');
        tooltipContent.setAttribute('aria-label', label);
        tooltipTrigger.removeAttribute('aria-describedby');
        tooltipTrigger.setAttribute('popovertarget', id);
        tooltipTrigger.setAttribute('aria-controls', id);
        tooltipTrigger.setAttribute('aria-haspopup', 'dialog');
        tooltipTrigger.setAttribute('aria-expanded', 'false');
      } else {
        tooltipContent.removeAttribute('popover');
        tooltipContent.removeAttribute('aria-label');
        tooltipContent.setAttribute('role', 'tooltip');
        tooltipTrigger.setAttribute('aria-describedby', id);
        for (const attribute of ['popovertarget', 'aria-controls', 'aria-haspopup', 'aria-expanded']) {
          tooltipTrigger.removeAttribute(attribute);
        }
      }
      stopViewportTracking();
    };
    tooltipContent.addEventListener('toggle', (event) => {
      if (!tooltip.classList.contains('tooltip-mobile-popup')) return;
      popupOpen = 'newState' in event && event.newState === 'open';
      tooltipTrigger.setAttribute('aria-expanded', String(popupOpen));
      if (popupOpen) {
        startViewportTracking();
        updatePosition();
      } else stopViewportTracking();
    }, { signal: scope.signal });
    tooltip.addEventListener('keydown', (event) => {
      if (event.key === 'Escape' && popupOpen) event.stopPropagation();
    }, { signal: scope.signal });
    media?.addEventListener('change', syncMode, { signal: scope.signal });
    syncMode();
  }
  scope.signal.addEventListener('abort', () => {
    if (popupOpen && tooltipContent.isConnected) tooltipContent.hidePopover();
    popupOpen = false;
    if (tooltip.classList.contains('tooltip-mobile-popup')) tooltipTrigger.setAttribute('aria-expanded', 'false');
    pointerInside = false;
    focusInside = false;
    stopViewportTracking();
  }, { once: true });
  scope.bind(tooltip);
  return tooltip;
}

/**
 * Computes the whole-hour span between a metadata object's `coverage-start`
 * and `coverage-end` fields, or `null` when either bound is missing, invalid,
 * or non-increasing.
 * @param {{ 'coverage-start'?: unknown, 'coverage-end'?: unknown } | undefined} metadata
 * @returns {number | null}
 */
export function coverageWindowHours(metadata) {
  const start = Date.parse(String(metadata?.['coverage-start'] ?? ''));
  const end = Date.parse(String(metadata?.['coverage-end'] ?? ''));
  return Number.isFinite(start) && Number.isFinite(end) && end > start
    ? Math.round((end - start) / 3_600_000)
    : null;
}

/**
 * Formats a `Date`, timestamp, or parseable date string as a short local
 * date (e.g. `Aug 30, 2026`), with no time-of-day or time-zone
 * normalization. Shared by the agent marketplace's last-observed label and
 * the experiments table's observation date column, which both derive a
 * compact date label from a raw timestamp or ISO string.
 * @param {Date | number | string} input
 * @param {string | undefined} [locale]
 * @returns {string}
 */
export function formatShortDate(input, locale = undefined) {
  const date = input instanceof Date ? input : new Date(input);
  return date.toLocaleDateString(locale, { month: 'short', day: 'numeric', year: 'numeric' });
}

/**
 * Formats a `Date` or millisecond timestamp as a medium-date, short-time,
 * UTC string (e.g. `Aug 30, 2026, 10:00 AM`). Callers are responsible for
 * validating their input; invalid input renders `Invalid Date`.
 * @param {Date | number} input
 * @returns {string}
 */
export function formatMediumUtcDateTime(input) {
  return new Intl.DateTimeFormat('en', {
    dateStyle: 'medium',
    timeStyle: 'short',
    timeZone: 'UTC'
  }).format(input instanceof Date ? input : new Date(input));
}

/**
 * Formats a `Date` or millisecond timestamp as a medium-date-only, UTC
 * string (e.g. `Aug 30, 2026`), with no time-of-day component. Callers are
 * responsible for validating their input; invalid input renders `Invalid
 * Date`.
 * @param {Date | number} input
 * @returns {string}
 */
export function formatMediumUtcDate(input) {
  return new Intl.DateTimeFormat('en', {
    dateStyle: 'medium',
    timeZone: 'UTC'
  }).format(input instanceof Date ? input : new Date(input));
}

/**
 * @param {unknown} value
 * @returns {string}
 */
export function formatUtcDateTime(value) {
  const parsed = Date.parse(value == null ? '' : String(value));
  if (!Number.isFinite(parsed)) return 'Time unavailable';
  return formatMediumUtcDateTime(parsed);
}

/**
 * Formats a `Date` or millisecond timestamp as {@link formatMediumUtcDateTime}
 * with a trailing ` UTC` suffix (e.g. `Aug 30, 2026, 10:00 AM UTC`), the
 * shape shared by tooltip titles, footer timestamps, and history readouts
 * that all annotate their formatted instant with an explicit UTC label.
 * Callers are responsible for validating their input; invalid input renders
 * `Invalid Date UTC`.
 * @param {Date | number} input
 * @returns {string}
 */
export function formatMediumUtcDateTimeWithSuffix(input) {
  return `${formatMediumUtcDateTime(input)} UTC`;
}

/**
 * Renders a digest string's first 12 characters wrapped in `<code>`, or
 * `null` when the digest is empty. Shared by the table cell display's
 * `digest` type and the workflow operational-grader stat, which
 * both truncate and code-format a digest but differ in their own fallback
 * content for the empty case.
 * @param {unknown} value
 * @returns {HTMLElement | null}
 */
export function renderDigest(value) {
  const text = value == null ? '' : String(value);
  return text ? h('code', null, text.slice(0, 12)) : null;
}

/**
 * Renders the shared placeholder used by table cells whose underlying record
 * exists but whose value was never observed, so the cell is explicitly marked
 * as missing rather than rendered blank.
 * @param {string} [message]
 * @returns {HTMLElement}
 */
export function renderMissingValue(message = 'missing data') {
  return h('span', { className: 'table-missing-value', 'data-missing-value': '' }, message);
}

/**
 * Renders the shared "no data" placeholder used by table-summary cells when
 * a column has no values eligible for summarization.
 * @param {string} message
 * @returns {HTMLElement}
 */
export function renderTableSummaryEmpty(message) {
  return h('span', { className: 'table-summary-empty' }, message);
}

/**
 * Renders the shared three-bar pulsing skeleton placeholder used while a
 * lazy view or table-summary cell awaits its content. Shared by
 * {@link ../components/lazy-view.js renderLazyView} and
 * {@link ../components/table-summary.js renderTableSummarySkeleton}, which
 * otherwise duplicated the same `<div>`-of-three-`<span>`s markup under
 * different class names.
 * @param {string} className
 * @returns {HTMLElement}
 */
export function renderSkeletonBars(className) {
  return h(
    'div',
    { className, 'aria-hidden': 'true' },
    h('span'),
    h('span'),
    h('span')
  );
}

/**
 * Column-span patterns (out of a 12-column grid) used to draw a fresh "UI
 * layout" pattern for {@link renderDashboardViewSkeleton} on every render, so
 * a full-page loading skeleton resembles a plausible arrangement of cards
 * and panels rather than a handful of uniform bars sitting in an otherwise
 * empty box.
 * @type {number[][]}
 */
const DASHBOARD_VIEW_SKELETON_LAYOUTS = [
  [12, 6, 6, 4, 4, 4],
  [8, 4, 4, 4, 4, 12],
  [6, 6, 12, 3, 3, 3, 3],
  [4, 4, 4, 6, 6, 12],
  [12, 3, 3, 3, 3, 6, 6],
  [7, 5, 4, 4, 4, 12]
];

/** Minimum height, in pixels, of a randomized page-skeleton block. */
const DASHBOARD_VIEW_SKELETON_MIN_BLOCK_HEIGHT_PX = 48;
/** Maximum extra height, in pixels, randomly added on top of the minimum. */
const DASHBOARD_VIEW_SKELETON_BLOCK_HEIGHT_VARIANCE_PX = 64;
/** Entrance-animation delay, in milliseconds, added per subsequent block. */
const DASHBOARD_VIEW_SKELETON_BLOCK_DELAY_STEP_MS = 60;

/**
 * Renders a randomized "UI layout" `aria-hidden` loading skeleton for a full
 * dashboard page view. Unlike {@link renderSkeletonBars}, which draws a
 * handful of uniform-width bars for compact inline placeholders, this picks
 * one of several 12-column layout patterns at random on every render and
 * gives each block a slightly different height, so the placeholder reads as
 * cards and panels rather than a few thin lines sitting in an otherwise
 * empty bordered box. Blocks fade and rise into place with a short
 * staggered entrance animation; both the entrance and the shimmer are pure
 * CSS and are disabled under `prefers-reduced-motion` (see `styles.js`).
 * @returns {HTMLElement}
 */
export function renderDashboardViewSkeleton() {
  const layout = DASHBOARD_VIEW_SKELETON_LAYOUTS[Math.floor(Math.random() * DASHBOARD_VIEW_SKELETON_LAYOUTS.length)];
  const blocks = layout.map((span, index) => {
    const height = DASHBOARD_VIEW_SKELETON_MIN_BLOCK_HEIGHT_PX
      + Math.round(Math.random() * DASHBOARD_VIEW_SKELETON_BLOCK_HEIGHT_VARIANCE_PX);
    const delay = index * DASHBOARD_VIEW_SKELETON_BLOCK_DELAY_STEP_MS;
    return h('span', {
      className: 'dashboard-view-skeleton-block',
      style: `--dashboard-view-skeleton-span: ${span}; --dashboard-view-skeleton-height: ${height}px; --dashboard-view-skeleton-delay: ${delay}ms`
    });
  });
  return h(
    'div',
    { className: 'dashboard-view-skeleton', 'aria-hidden': 'true' },
    ...blocks
  );
}

/**
 * Renders the shared "three cards and a panel" `aria-hidden` loading
 * placeholder blocks used while the dashboard shell or a page view awaits
 * its content. Shared by the top-level dashboard loading skeleton in
 * `main.js` and {@link ../presenter.js renderPageSkeleton}, which otherwise
 * duplicated the same `skeleton-card`/`skeleton-panel` `<div>` sequence
 * through two different DOM-construction styles.
 * @returns {HTMLElement[]}
 */
export function renderLoadingPlaceholderBlocks() {
  return [
    h('div', { className: 'skeleton-card', 'aria-hidden': 'true' }),
    h('div', { className: 'skeleton-card', 'aria-hidden': 'true' }),
    h('div', { className: 'skeleton-card', 'aria-hidden': 'true' }),
    h('div', { className: 'skeleton-panel', 'aria-hidden': 'true' })
  ];
}

/**
 * Renders the shared "empty" placeholder paragraph used across route views and
 * panels when there is no data to display.
 * @param {string} message
 * @param {Record<string, unknown>} [extraAttrs]
 * @returns {HTMLElement}
 */
export function renderEmptyMessage(message, extraAttrs) {
  return h('p', { className: 'empty', ...extraAttrs }, message);
}

/**
 * Renders the shared `<p class="empty-message">` placeholder used by
 * visualization components (chart/plot-style elements) when there is no
 * data to plot. Distinct from {@link renderEmptyMessage}'s `.empty` styling,
 * which several panels and route views rely on for their own layout.
 * @param {string} message
 * @returns {HTMLElement}
 */
export function renderVisualizationEmptyMessage(message) {
  return h('p', { className: 'empty-message' }, message);
}

/**
 * Renders the shared "loading" placeholder paragraph used by route/panel
 * views while data is still in flight: a {@link renderEmptyMessage} paragraph
 * announced as an in-progress live-region status.
 * @param {string} message
 * @returns {HTMLElement}
 */
export function renderLoadingMessage(message) {
  return renderEmptyMessage(message, { role: 'status', 'aria-busy': 'true' });
}

/**
 * Renders a `<ul>` of caller-supplied `<li>` elements when `items` is
 * non-empty, or a component-specific empty-state paragraph otherwise. Shared
 * by the configuration-actions list and the campaign-status repository list,
 * which both render a fully-formed `<li>` per item (unlike
 * {@link renderListWithFallback}, whose fallback message is itself wrapped
 * in a single `<li>`).
 * @template T
 * @param {string} listClassName
 * @param {T[]} items
 * @param {(item: T, index: number) => HTMLElement} renderItem returns a complete `<li>` element
 * @param {string} emptyClassName
 * @param {string} emptyMessage
 * @returns {HTMLElement}
 */
export function renderListOrEmptyMessage(listClassName, items, renderItem, emptyClassName, emptyMessage) {
  return items.length > 0
    ? h('ul', { className: listClassName }, ...items.map(renderItem))
    : h('p', { className: emptyClassName }, emptyMessage);
}

/**
 * Renders the shared "single `<td>` spanning the full table width" empty-body
 * row used by table regions and campaign summary tables when there is no data
 * to display. An optional `action` renders an inline button after `message`
 * (for example, a "Clear time filter" recovery action) without changing the
 * plain-message shape existing callers rely on.
 * @param {number} colSpan
 * @param {string} message
 * @param {{ label: string, onActivate: (event: MouseEvent) => void }} [action]
 * @returns {HTMLElement}
 */
export function renderEmptyTableRow(colSpan, message, action) {
  return h(
    'tr',
    null,
    h(
      'td',
      { colSpan, ...(action ? { 'aria-live': 'polite' } : {}) },
      message,
      action
        ? h('button', { type: 'button', className: 'table-empty-action', onclick: action.onActivate }, action.label)
        : null
    )
  );
}

/**
 * Renders the shared `<span class="count-badge">` numeric-count pill used by
 * the horizon filter bar (live filter count) and the work-project board
 * (per-column item count). Both callers pair a count value with an
 * accessible label describing what is being counted.
 * @param {number|string} count
 * @param {string} ariaLabel
 * @returns {HTMLElement}
 */
export function renderCountBadge(count, ariaLabel) {
  return h('span', { className: 'count-badge', 'aria-label': ariaLabel }, String(count));
}

/**
 * Renders the shared "empty `aria-live="polite"` status element, populated
 * later by the caller's render loop" pattern used by the table intent-action
 * dialog, the CLI action approval dialog, the reset-dashboard confirmation,
 * and the configuration diagnostics control. Callers differ only in element
 * tag (`span` vs `output`) and class name.
 * @param {'span'|'output'} tag
 * @param {string} className
 * @returns {HTMLElement}
 */
export function renderLiveRegion(tag, className) {
  return h(tag, { className, 'aria-live': 'polite' });
}

/**
 * Renders the shared "`<select>` filter with a placeholder option followed by
 * the sorted, de-duplicated set of values" pattern used by the operations
 * marketplace owner filter and the work-project facet filters.
 * @param {string} ariaLabel
 * @param {string} placeholderLabel
 * @param {string[]} values
 * @param {(left: string, right: string) => number} [compareFn]
 * @returns {HTMLSelectElement}
 */
export function renderFilterSelect(ariaLabel, placeholderLabel, values, compareFn) {
  return /** @type {HTMLSelectElement} */ (h('select', { 'aria-label': ariaLabel },
    h('option', { value: '' }, placeholderLabel),
    ...[...new Set(values)].sort(compareFn).map((value) => h('option', { value }, value))
  ));
}

/**
 * Renders a rounded filter control around a select while preserving the
 * select's native keyboard and form behavior.
 * @param {HTMLSelectElement} select
 * @returns {HTMLElement}
 */
export function renderFilterSelectControl(select) {
  select.classList.add('filter-select-control-input');
  return h('span', { className: 'filter-select-control' }, select);
}

/**
 * Renders the shared `type="search"` filter/search input pattern used by the
 * agent marketplace, work-project, and notifications-inbox toolbars, which
 * all pair an identical placeholder and `aria-label` with a disabled
 * spellcheck.
 * @param {string} label
 * @param {string} [value]
 * @returns {HTMLInputElement}
 */
export function renderSearchInput(label, value = '') {
  return /** @type {HTMLInputElement} */ (h('input', {
    type: 'search', value, placeholder: label, 'aria-label': label, spellcheck: 'false'
  }));
}

/**
 * Renders the shared `<summary>` label plus "Show details" hint span pair
 * used by disclosure summaries whose hint text swaps to "Hide details" via
 * the `[open] .*-hint::after` CSS rule in `styles.js`. Shared by the campaign
 * README resources panel and the supplemental custom-view disclosure toggle,
 * both of which pair a title/label with a differently-classed hint span.
 * @param {string | Node} label
 * @param {string} hintClassName
 * @returns {Node[]}
 */
export function renderDisclosureSummaryLabel(label, hintClassName) {
  return [
    typeof label === 'string' ? h('span', null, label) : label,
    h('span', { className: hintClassName }, 'Show details')
  ];
}

/**
 * Renders a `<details>` disclosure whose body is deferred until the panel is
 * first expanded, then cached for the life of the element. Shared by the
 * configuration policy tree (nested setting groups) and the notifications
 * repeated-cause clusters, both of which defer populating a body container
 * until a reader actually opens the panel.
 * @param {string} className
 * @param {Node | Node[]} summaryContent
 * @param {HTMLElement} bodyContainer container appended once, then populated on first expand
 * @param {(bodyContainer: HTMLElement) => void} populateBody called once, the first time the panel opens
 * @param {{ open?: boolean, eagerContent?: Node | Node[], summaryClassName?: string }} [options]
 *   `open: true` renders (and immediately populates) expanded; `eagerContent` renders immediately
 *   between the summary and the lazily-populated body container; `summaryClassName` sets a class
 *   on the `<summary>` element
 * @returns {HTMLDetailsElement}
 */
export function renderLazyDisclosure(className, summaryContent, bodyContainer, populateBody, options = {}) {
  const initiallyOpen = options.open === true;
  const eagerContent = options.eagerContent === undefined ? [] : [options.eagerContent].flat();
  let populated = false;
  const populateOnce = () => {
    if (populated) return;
    populateBody(bodyContainer);
    populated = true;
  };
  if (initiallyOpen) populateOnce();
  const details = /** @type {HTMLDetailsElement} */ (h(
    'details',
    { className, open: initiallyOpen },
    h('summary', options.summaryClassName ? { className: options.summaryClassName } : null, ...[summaryContent].flat()),
    ...eagerContent,
    bodyContainer
  ));
  details.addEventListener('toggle', () => {
    if (details.open) populateOnce();
  });
  return details;
}

/**
 * Renders the shared "`<ul>` of items, or a single fallback `<li>`" pattern
 * used by summary and provenance lists when there is no data to display.
 * @template T
 * @param {string} className
 * @param {T[]} items
 * @param {(item: T) => Node | string} renderItem
 * @param {string} fallbackMessage
 * @returns {HTMLElement}
 */
export function renderListWithFallback(className, items, renderItem, fallbackMessage) {
  return h(
    'ul',
    { className },
    items.length > 0 ? items.map((item) => h('li', null, renderItem(item))) : [h('li', null, fallbackMessage)]
  );
}

/**
 * Renders the shared "icon plus name" identity link used for campaign/entity
 * references (campaign summary rows, utilization cards, campaign status
 * cards), wrapping the label text in the caller-selected inline element.
 * @param {{ href: string, icon: string, label: string, className?: string, labelTag?: 'span'|'strong' }} options
 * @returns {HTMLElement}
 */
export function renderIdentityLink({ href, icon, label, className, labelTag = 'span' }) {
  return h('a', className ? { href, className } : { href }, octicon(icon), h(labelTag, null, label));
}

/**
 * Renders the shared decorative legend swatch (`<i>` with a series-specific
 * class and `aria-hidden`) used to color-key chart and trend legend entries.
 * @param {string} className
 * @returns {HTMLElement}
 */
export function renderLegendSwatch(className) {
  return h('i', { className, 'aria-hidden': 'true' });
}

/**
 * Renders the shared `<ul class="chart-legend ...">` of `<li>` entries, each
 * pairing a color-keyed {@link renderLegendSwatch} with caller-supplied
 * content. Shared by the line/bar series legend, the pie-chart legend, and
 * the outcome-diagnostic legend, which all build one list item per series
 * from a swatch class name and the series' own label markup.
 * @template T
 * @param {string} className full `className` for the `<ul>` element
 * @param {T[]} items
 * @param {(item: T, index: number) => string} swatchClassName
 * @param {(item: T, index: number) => Array<Node | string | null>} renderContent
 * @param {Record<string, unknown>} [extraAttrs]
 * @returns {HTMLElement}
 */
export function renderLegendList(className, items, swatchClassName, renderContent, extraAttrs) {
  return h(
    'ul',
    { className, ...extraAttrs },
    items.map((item, index) => h(
      'li',
      null,
      renderLegendSwatch(swatchClassName(item, index)),
      ...renderContent(item, index)
    ))
  );
}

/**
 * Renders a `<span>` (or another element tag) wrapping a single octicon,
 * used by the attention-domain cards, readiness-verdict hero, signal-list
 * rows, metric/document/issue cards, factory stations, the filter search
 * control, and discussion post avatars to present one decorative or
 * semantic icon inside a component-specific class name.
 * @param {string} className
 * @param {string} iconName
 * @param {{ ariaHidden?: boolean, tag?: string }} [options]
 * @returns {HTMLElement}
 */
export function renderIconSpan(className, iconName, options = {}) {
  return h(
    options.tag ?? 'span',
    options.ariaHidden ? { className, 'aria-hidden': 'true' } : { className },
    octicon(iconName)
  );
}

/**
 * Renders a visually-hidden `<span class="sr-only action-label">` used
 * alongside an icon-only trigger to give it an accessible name. Shared by
 * the account menu avatar, the dashboard repository link, the dashboard
 * horizon toggle, and the appearance control summary, which otherwise
 * duplicated the identical `h('span', { className: 'sr-only action-label' },
 * text)` call at each icon-only trigger.
 * @param {string} text
 * @returns {HTMLSpanElement}
 */
export function renderActionLabel(text) {
  return /** @type {HTMLSpanElement} */ (h('span', { className: 'sr-only action-label' }, text));
}

/**
 * Renders the shared dismiss/close icon button used by overlay-style
 * components (dialogs, callouts) that need a labelled "x" trigger with
 * matching `title` and `aria-label` text.
 * @param {{ className: string, label: string, onClick: (event: MouseEvent) => void }} options
 * @returns {HTMLButtonElement}
 */
export function renderCloseButton({ className, label, onClick }) {
  return /** @type {HTMLButtonElement} */ (h(
    'button',
    {
      type: 'button',
      className,
      title: label,
      'aria-label': label,
      onClick
    },
    octicon('x')
  ));
}

/**
 * Synchronizes a toggle button's `aria-expanded` attribute, accessible label,
 * title, and icon with the given expanded state. Shared by the sidebar
 * collapse toggle and the table column-summary toggle, which otherwise
 * duplicated the same attribute/label/icon swap on every click.
 * @param {HTMLElement} toggle
 * @param {boolean} expanded
 * @param {{ expandedLabel: string, collapsedLabel: string, expandedIcon: string, collapsedIcon: string }} options
 */
export function syncToggleButtonState(toggle, expanded, { expandedLabel, collapsedLabel, expandedIcon, collapsedIcon }) {
  const label = expanded ? expandedLabel : collapsedLabel;
  toggle.setAttribute('aria-expanded', String(expanded));
  toggle.setAttribute('aria-label', label);
  toggle.setAttribute('title', label);
  toggle.replaceChildren(octicon(expanded ? expandedIcon : collapsedIcon));
}

/**
 * Creates a `<dialog>` element with open/close helpers that fall back to the
 * `open` attribute on runtimes without `HTMLDialogElement.showModal`/`close`
 * support. Shared by the table intent-action prompt preview and the
 * work-item mobile detail sheet, which both otherwise duplicated the same
 * feature-detection branches. Contains clicks so dialogs nested in clickable
 * cards cannot activate their owning row.
 * @param {{ className: string, ariaLabel: string, onFallbackClose?: () => void }} options
 * @returns {{ dialog: HTMLDialogElement, open: () => void, close: () => void }}
 */
export function createModalDialog({ className, ariaLabel, onFallbackClose }) {
  const dialog = /** @type {HTMLDialogElement} */ (h('dialog', {
    className,
    'aria-label': ariaLabel,
    onClick: (/** @type {MouseEvent} */ event) => event.stopPropagation()
  }));
  const open = () => {
    if (typeof dialog.showModal === 'function') {
      dialog.showModal();
    } else {
      dialog.setAttribute('open', '');
    }
  };
  const close = () => {
    if (typeof dialog.close === 'function' && dialog.open) {
      dialog.close();
    } else {
      dialog.removeAttribute('open');
      onFallbackClose?.();
    }
  };
  return { dialog, open, close };
}

/**
 * Closes a native `<details>` disclosure menu when the pointer clicks a
 * designated dismiss-triggering descendant or anywhere outside the menu, and
 * when the menu has focus and the user presses Escape, returning focus to
 * its `<summary>` toggle. Shared by the account menu and the mobile
 * view-navigation menu in the dashboard shell, which otherwise duplicated
 * the same open-state teardown wired to different selectors.
 * @param {HTMLElement} root the ancestor that receives the outside-click listener
 * @param {HTMLDetailsElement} menu the `<details>` element to dismiss
 * @param {string} dismissSelector selector matched against the click target (in addition to
 *   clicks outside `menu`) that should also close the menu, e.g. a selected menu action
 */
export function enableDetailsMenuDismissal(root, menu, dismissSelector) {
  root.addEventListener('click', (event) => {
    if (!(event.target instanceof Element)) return;
    if (event.target.closest(dismissSelector) || !menu.contains(event.target)) {
      menu.removeAttribute('open');
    }
  });
  menu.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape') return;
    menu.removeAttribute('open');
    const summary = menu.querySelector('summary');
    if (summary instanceof HTMLElement) summary.focus();
  });
}

/**
 * Renders the shared `<label><span>{label}</span>{control}</label>` pattern
 * used to associate a visible text label with a form control (search
 * inputs, facet selects, time-window inputs) across the filter bar and
 * table region toolbars.
 * @param {string} label
 * @param {Node} control
 * @param {{ className?: string, prefix?: Node, visuallyHiddenLabel?: boolean }} [options]
 * @returns {HTMLLabelElement}
 */
export function renderLabeledControl(label, control, options = {}) {
  return /** @type {HTMLLabelElement} */ (h(
    'label',
    options.className ? { className: options.className } : null,
    options.prefix,
    h('span', options.visuallyHiddenLabel ? { className: 'sr-only' } : null, label),
    control
  ));
}

/**
 * Renders a checkbox with consistent event handling.
 * @param {{
 *   id?: string,
 *   className?: string,
 *   checked?: boolean,
 *   disabled?: boolean,
 *   ariaLabel?: string,
 *   onChange?: (event: Event) => void,
 *   stopClickPropagation?: boolean
 * }} options
 * @returns {HTMLInputElement}
 */
export function renderCheckbox({
  id,
  className,
  checked = false,
  disabled = false,
  ariaLabel,
  onChange,
  stopClickPropagation = false
}) {
  return /** @type {HTMLInputElement} */ (h('input', {
    id,
    className,
    type: 'checkbox',
    checked,
    disabled,
    'aria-label': ariaLabel,
    onChange,
    onClick: stopClickPropagation
      ? /** @param {MouseEvent} event */ (event) => event.stopPropagation()
      : undefined
  }));
}

/**
 * Writes text to the clipboard via the async Clipboard API, resolving to
 * whether the copy succeeded. Shared by the table intent-action dialog and
 * the raw-policy panel, which both need a best-effort clipboard write that
 * degrades to a boolean result instead of throwing.
 * @param {string} content
 * @returns {Promise<boolean>}
 */
export async function copyTextToClipboard(content) {
  if (typeof navigator?.clipboard?.writeText !== 'function') {
    debugUiPrimitives({ operation: 'copy-to-clipboard', outcome: 'unavailable' });
    return false;
  }
  try {
    await navigator.clipboard.writeText(content);
    debugUiPrimitives({ operation: 'copy-to-clipboard', outcome: 'succeeded', length: content.length });
    return true;
  } catch (error) {
    debugUiPrimitives({ operation: 'copy-to-clipboard', outcome: 'failed', errorName: error instanceof Error ? error.name : 'unknown' });
    return false;
  }
}

/**
 * Builds a `button` + `output` pair that copies text to the clipboard via
 * {@link copyTextToClipboard} and reports the result through the `output`'s
 * `aria-live` region. Shared by the raw-policy panel and the table
 * intent-action prompt dialog, which both need a copy button with a status
 * message but differ in label, styling, and whether the button should be
 * disabled and flagged with `data-copy-state` while the copy is pending.
 * @param {{
 *   getContent: () => string,
 *   label: string,
 *   buttonClassName: string,
 *   statusClassName: string,
 *   successText?: string,
 *   failureText?: string,
 *   trackState?: boolean
 * }} options
 * @returns {{ button: HTMLButtonElement, status: HTMLOutputElement, reset: () => void }}
 */
export function createCopyControl(options) {
  const {
    getContent,
    label,
    buttonClassName,
    statusClassName,
    successText = 'Copied.',
    failureText = 'Copy unavailable.',
    trackState = false
  } = options;
  const scope = createFactoryScope();

  // `busy`/`statusText`/`copyState` are this control's entire visible run
  // state; the effect below is the only place that writes them onto the
  // owned `button`/`status` nodes, matching the `reset-dashboard-control.js`
  // state/effect/createFactoryScope shape for owned interactive DOM.
  const busy = state(false);
  const statusText = state('');
  const copyState = state(/** @type {'success' | 'error' | null} */ (null));

  const status = /** @type {HTMLOutputElement} */ (h('output', { className: statusClassName, 'aria-live': 'polite' }));
  const button = /** @type {HTMLButtonElement} */ (h(
    'button',
    {
      type: 'button',
      className: buttonClassName,
      onClick: async () => {
        if (trackState) {
          busy.set(true);
          statusText.set('');
        }
        const copied = await copyTextToClipboard(getContent());
        if (scope.signal.aborted) return;
        if (trackState) {
          busy.set(false);
          copyState.set(copied ? 'success' : 'error');
        }
        statusText.set(copied ? successText : failureText);
      }
    },
    octicon('copy'),
    label
  ));
  effect(() => {
    status.textContent = statusText.get();
    if (trackState) button.disabled = busy.get();
    const nextCopyState = copyState.get();
    if (nextCopyState) button.setAttribute('data-copy-state', nextCopyState);
    else button.removeAttribute('data-copy-state');
  }, { signal: scope.signal });
  const reset = () => {
    statusText.set('');
    copyState.set(null);
  };
  scope.bind(button);
  return { button, status, reset };
}

/**
 * Observes a "load more" boundary element and invokes `onLoadMore` once it
 * intersects the viewport (or its configured root). Shared by the lazy
 * infinite list and table region components, which both otherwise duplicate
 * an `entries.some((entry) => entry.isIntersecting)` observer callback.
 * Returns `null` (observing nothing) when no `IntersectionObserver`
 * constructor is available, e.g. in a non-browser environment.
 * @param {typeof IntersectionObserver | undefined} observerCtor
 * @param {Element} boundaryElement
 * @param {() => void} onLoadMore
 * @param {IntersectionObserverInit} [observerOptions]
 * @returns {IntersectionObserver | null}
 */
export function observeLoadMoreBoundary(observerCtor, boundaryElement, onLoadMore, observerOptions) {
  if (typeof observerCtor !== 'function') return null;
  const observer = new observerCtor((entries) => {
    if (entries.some((entry) => entry.isIntersecting)) onLoadMore();
  }, observerOptions);
  observer.observe(boundaryElement);
  return observer;
}

/**
 * Checks whether a value is a URL string using the https protocol with no embedded
 * credentials, the safety bar every dashboard link and href renderer applies before
 * trusting externally-sourced link data.
 * @param {unknown} value
 * @returns {boolean}
 */
export function isSafeHttpsUrl(value) {
  if (typeof value !== 'string' || value.length === 0) return false;
  try {
    const url = new URL(value);
    return url.protocol === 'https:' && !url.username && !url.password;
  } catch {
    return false;
  }
}

/**
 * @param {unknown} value
 * @returns {value is Record<string, any>}
 */
export function isPlainObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
