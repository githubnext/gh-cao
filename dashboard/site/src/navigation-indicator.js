import { createDebug } from './debug.js';
import { pageRequiredSource } from './view-availability.js';

const debugNavigationIndicator = createDebug('navigation-indicator');

/**
 * Resolves the declarative navigation indicator for one page.
 * The returned source names refer to declarative query outputs that already
 * encode the indicator condition.
 *
 * @param {Record<string, unknown>} page
 * @returns {{ label: string, sources: string[] } | null}
 */
export function navigationIndicator(page) {
  if (!isPlainObject(page['navigation-indicator'])) return null;
  const indicator = /** @type {Record<string, unknown>} */ (page['navigation-indicator']);
  if (typeof indicator.label !== 'string' || indicator.label.length === 0) {
    debugNavigationIndicator({ event: 'rejected', reason: 'missing-label', pageId: String(page.id ?? '') });
    return null;
  }
  const sources = Array.isArray(indicator.any)
    ? indicator.any.filter((source) => typeof source === 'string' && source.length > 0)
    : [];
  if (sources.length === 0) {
    debugNavigationIndicator({ event: 'rejected', reason: 'no-sources', pageId: String(page.id ?? '') });
    return null;
  }
  return {
    label: indicator.label,
    sources
  };
}

/**
 * Returns the unique bounded sources needed to evaluate page navigation
 * indicators.
 * @param {Array<Record<string, unknown>>} pages
 */
export function navigationIndicatorSourceNames(pages) {
  const raw = pages.flatMap(navigationIndicatorSourceNamesForPage);
  const unique = [...new Set(raw)];
  debugNavigationIndicator({ event: 'source-names-resolved', pageCount: pages.length, rawCount: raw.length, uniqueCount: unique.length });
  return unique;
}

/**
 * Returns the bounded sources referenced by one page's navigation indicator.
 * @param {Record<string, unknown>} page
 */
export function navigationIndicatorSourceNamesForPage(page) {
  const indicator = navigationIndicator(page);
  const required = pageRequiredSource(page);
  return [...(indicator?.sources ?? []), ...(required ? [required] : [])];
}

/** @param {unknown} value @returns {value is Record<string, unknown>} */
function isPlainObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
