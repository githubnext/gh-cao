/**
 * Generic renderer for JSON-selected table cell displays.
 */

import { h } from '../dom.js';
import { createDebug } from '../debug.js';
import { renderActiveStateBadge, renderGraderStatusBadge, renderModeBadge, renderStatusBadge } from './badge.js';
import { renderShortenedUrl, renderWorkflowRunUrl } from './link-content.js';
import { formatHumanFriendlyTimestamp, formatNumber, formatString, stringOrFallback } from '../view-formatters.js';
import { formatUtcDateTime, formatMediumUtcDateTimeWithSuffix, renderDigest, renderMissingValue } from './ui-primitives.js';

const debugCellDisplay = createDebug('cell-display');

const KNOWN_DISPLAYS = new Set(['mode', 'active-state', 'status', 'grader-status', 'label', 'ref', 'digest']);

/**
 * @param {unknown} display
 * @param {unknown} value
 * @param {(value: unknown) => string} toText
 * @param {{ name: string, symbol: string, significant: number, format?: string } | null} [unit]
 * @param {unknown} [type]
 * @param {unknown} [format]
 * @returns {string | HTMLElement}
 */
export function renderCellDisplay(display, value, toText, unit = null, type, format) {
  if (value == null || value === '') {
    if (format === 'workflow-relative-path') {
      debugCellDisplay({ event: 'missing-value-rendered', format });
      return renderMissingValue();
    }
    return '';
  }
  if (typeof display === 'string' && display && !KNOWN_DISPLAYS.has(display)) {
    debugCellDisplay({ event: 'display-unmatched', display, type });
  }
  if (display === 'mode') return renderModeBadge(value);
  if (display === 'active-state') return renderActiveStateBadge(value);
  if (display === 'status') return renderStatusBadge(value);
  if (display === 'grader-status') return renderGraderStatusBadge(value);
  if (display === 'label') return formatLabel(value);
  if (display === 'ref') return h('span', { className: 'ref-label' }, toText(value));
  if (display === 'digest') return renderDigest(value) ?? 'unavailable';
  if (type === 'quantitative' && !Number.isFinite(Number(value))) {
    debugCellDisplay({ event: 'quantitative-unparseable', type });
    return '';
  }
  if (unit?.format === 'bytes' || unit?.format === 'compact-number') {
    const numeric = Number(value);
    if (!Number.isFinite(numeric)) {
      debugCellDisplay({ event: 'quantitative-unparseable', type });
      return '';
    }
    const text = formatNumber(numeric, unit);
    const title = `${numeric.toLocaleString('en', { maximumSignificantDigits: 21 })} ${unit.name}`;
    return h('span', { title, ariaLabel: `${text} (${title})` }, text);
  }
  if (type === 'temporal' && typeof value === 'string' && Number.isFinite(Date.parse(value))) {
    const text = format === 'human-friendly-timestamp'
      ? formatHumanFriendlyTimestamp(value)
      : formatUtcDateTime(value);
    const title = format === 'human-friendly-timestamp' ? formatMediumUtcDateTimeWithSuffix(Date.parse(value)) : undefined;
    const ariaLabel = title ? `${text} (${title})` : undefined;
    return h('time', { dateTime: value, title, ariaLabel }, text);
  }
  if (unit && typeof value === 'number' && Number.isFinite(value)) return formatNumber(value, unit);
  if (format === 'workflow-run-url') return renderWorkflowRunUrl(value) ?? toText(value);
  if (format === 'shortened-url') return renderShortenedUrl(value) ?? toText(value);
  if (format !== undefined) return formatString(value, format);
  if (typeof value === 'number' && Number.isFinite(value)) return formatNumber(value);
  return toText(value);
}

/** @param {unknown} value */
function formatLabel(value) {
  const text = stringOrFallback(value, 'unavailable');
  const normalized = text.toLowerCase();
  if (normalized === 'matured') return 'Mature';
  return `${normalized.charAt(0).toUpperCase()}${normalized.slice(1)}`;
}
