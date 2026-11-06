/**
 * Shared presentation-only formatting and aggregation helpers for custom dashboard views.
 */

import { createDebug } from './debug.js';

const debugViewFormatters = createDebug('view-formatters');

const KNOWN_AGGREGATES = new Set(['count', 'distinct-count', 'sum', 'mean', 'min', 'max']);

/** @type {Intl.RelativeTimeFormat | undefined} */
let relativeTimeFormat;
/** @type {Intl.DateTimeFormat | undefined} */
let dateFormat;
/** @type {Intl.DateTimeFormat | undefined} */
let dateWithYearFormat;
const humanRelativeTimeFormat = () => relativeTimeFormat ??= new Intl.RelativeTimeFormat('en', { numeric: 'auto' });
const humanDateFormat = () => dateFormat ??= new Intl.DateTimeFormat('en', {
  month: 'short',
  day: 'numeric',
  timeZone: 'UTC'
});
const humanDateWithYearFormat = () => dateWithYearFormat ??= new Intl.DateTimeFormat('en', {
  month: 'short',
  day: 'numeric',
  year: 'numeric',
  timeZone: 'UTC'
});

/**
 * Formats a Date/timestamp as a short, UTC month-and-day string (e.g. "Aug 9"),
 * matching the date portion used by relative-timestamp rendering elsewhere in the
 * dashboard. Shared so chart axis/tooltip labels stay visually consistent with it.
 * @param {number | Date} value
 * @returns {string}
 */
export function formatShortUtcDate(value) {
  return humanDateFormat().format(value);
}

/**
 * @param {Array<Record<string, unknown>>} rows
 * @param {string | null} fieldName
 * @param {string} aggregate
 * @param {(value: unknown) => string} toText
 * @param {{ name: string, symbol: string, significant: number, format?: string } | null} [unit]
 * @returns {string}
 */
export function formatAggregateValue(rows, fieldName, aggregate, toText, unit = null) {
  if (!fieldName) {
    return '';
  }
  if (rows.length === 0) {
    debugViewFormatters({ event: 'aggregate-empty-rows', aggregate });
  }

  const presentValues = rows.map((row) => row[fieldName]).filter((value) => value != null && value !== '');
  const numericValues = presentValues.filter((value) => isFiniteNumber(value));
  if (presentValues.length === 0 && rows.length > 0) {
    debugViewFormatters({ event: 'aggregate-no-present-values', aggregate, rowCount: rows.length });
  }
  if (aggregate !== 'none' && !KNOWN_AGGREGATES.has(aggregate)) {
    debugViewFormatters({ event: 'aggregate-unmatched', aggregate });
  }
  if (aggregate === 'count') {
    return presentValues.length > 0 ? formatNumber(presentValues.length, unit) : '';
  }
  if (aggregate === 'distinct-count') {
    return presentValues.length > 0 ? formatNumber(new Set(presentValues.map(toText)).size, unit) : '';
  }
  if (aggregate === 'sum') {
    return numericValues.length > 0
      ? formatNumber(numericValues.reduce((total, value) => total + value, 0), unit)
      : '';
  }
  if (aggregate === 'mean') {
    return numericValues.length > 0
      ? formatNumber(numericValues.reduce((total, value) => total + value, 0) / numericValues.length, unit)
      : '';
  }
  if (aggregate === 'min') {
    return numericValues.length > 0 ? formatNumber(Math.min(...numericValues), unit) : '';
  }
  if (aggregate === 'max') {
    return numericValues.length > 0 ? formatNumber(Math.max(...numericValues), unit) : '';
  }
  const value = rows[0]?.[fieldName];
  return rows.length > 0 && unit && typeof value === 'number' && Number.isFinite(value)
    ? formatNumber(value, unit)
    : value != null && value !== '' ? toText(value) : '';
}

/** @param {unknown} value @returns {value is number} */
function isFiniteNumber(value) {
  return typeof value === 'number' && Number.isFinite(value);
}

/**
 * Coerces a value to its string form, substituting a fallback when the value
 * is `null`, `undefined`, or an empty string. Shared by badge and cell
 * display renderers that fall back to a placeholder label such as
 * `'unknown'` or `'unavailable'`.
 * @param {unknown} value
 * @param {string} fallback
 * @returns {string}
 */
export function stringOrFallback(value, fallback) {
  return value == null || value === '' ? fallback : String(value);
}

/**
 * Applies a declarative presentation-only format to a string value.
 * @param {unknown} value
 * @param {unknown} format
 * @param {string} fallback
 * @returns {string}
 */
export function formatString(value, format, fallback = 'unknown') {
  const text = stringOrFallback(value, fallback);
  if (format === 'workflow-relative-path') {
    if (!text.startsWith('.github/workflows/')) return text;
    const formatted = text.slice('.github/workflows/'.length);
    return formatted || fallback;
  }
  if (format === 'workflow-identity-label') {
    const separator = text.indexOf(':');
    const repository = text.slice(0, separator);
    const workflow = text.slice(separator + 1);
    if (!repository || !workflow.startsWith('.github/workflows/')) return text;
    const relativeWorkflow = workflow.slice('.github/workflows/'.length);
    return relativeWorkflow ? `${relativeWorkflow} (${repository})` : text;
  }
  return text;
}

/**
 * Formats a millisecond duration using cascading clock units (seconds, then
 * minutes, then hours, optionally rolling over into days). Shared by
 * run-duration renderers that differ only in whether a days tier applies.
 * @param {number} duration
 * @param {{ includeDays?: boolean }} [options]
 * @returns {string}
 */
export function formatClockDuration(duration, { includeDays = true } = {}) {
  const seconds = Math.max(0, Math.round(duration / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`;
  const hours = Math.floor(minutes / 60);
  if (!includeDays || hours < 24) return `${hours}h ${minutes % 60}m`;
  const days = Math.floor(hours / 24);
  return `${days}d ${hours % 24}h`;
}

/**
 * @param {unknown} value
 * @returns {number}
 */
export function toNumber(value) {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

/**
 * @param {number} value
 * @param {{ name: string, symbol: string, significant: number, format?: string } | null} [unit]
 * @param {boolean} [includeUnit]
 * @returns {string}
 */
export function formatNumber(value, unit = null, includeUnit = true) {
  if (unit && Number.isFinite(unit.significant) && unit.significant > 0) {
    const quotient = value / unit.significant;
    const rounded = Math.sign(quotient) * Math.round(Math.abs(quotient)) * unit.significant;
    if (unit.format === 'bytes') {
      return `${rounded < 0 ? '-' : ''}${formatDataSize(Math.abs(rounded))}`;
    }
    if (unit.format === 'compact-number') {
      return formatCompactNumber(rounded);
    }
    if (unit.format === 'duration') {
      return formatDurationSeconds(rounded);
    }
    if (unit.format === 'usd') {
      return formatUsd(value);
    }
    if (unit.format === 'aicc') {
      return formatAicc(rounded);
    }
    return `${rounded.toFixed(fractionDigits(unit.significant))}${includeUnit && unit.format !== 'number' ? ` ${unit.symbol}` : ''}`;
  }
  return Number.isInteger(value) ? String(value) : value.toFixed(3).replace(/0$/, '');
}

/** @param {number} value */
export function formatCompactNumber(value) {
  return new Intl.NumberFormat('en', {
    notation: 'compact',
    maximumSignificantDigits: 3
  }).format(value);
}

/** @param {number} bytes */
export function formatDataSize(bytes) {
  const value = Math.max(0, Number(bytes) || 0);
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB'];
  let scaled = value;
  let unit = units[0];
  for (let index = 1; index < units.length && scaled >= 1_000; index += 1) {
    scaled /= 1_000;
    unit = units[index];
  }
  const digits = scaled >= 10 || unit === 'B' ? 0 : 1;
  return `${scaled.toFixed(digits)} ${unit}`;
}

/**
 * Formats US dollars with no more than three fractional digits, rounding
 * upward at the third fractional digit.
 * @param {number} value
 * @returns {string}
 */
export function formatUsd(value) {
  const scaled = value * 1_000;
  const tolerance = Number.EPSILON * Math.max(1, Math.abs(scaled));
  const rounded = Math.ceil(scaled - tolerance) / 1_000;
  const formatted = new Intl.NumberFormat('en', {
    style: 'decimal',
    minimumFractionDigits: 2,
    maximumFractionDigits: 3
  }).format(Math.abs(rounded));
  return rounded < 0 ? `-$${formatted}` : `$${formatted}`;
}

/**
 * Formats AI Credits as their US dollar cost, where 100 AIC equals one dollar.
 * @param {number} value
 * @returns {string}
 */
export function formatAicc(value) {
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD'
  }).format(value / 100);
}

/**
 * @param {number} seconds
 * @returns {string}
 */
function formatDurationSeconds(seconds) {
  const sign = seconds < 0 ? '-' : '';
  const absoluteSeconds = Math.abs(seconds);
  if (absoluteSeconds < 60) return `${sign}${absoluteSeconds}s`;
  const minutes = Math.floor(absoluteSeconds / 60);
  if (minutes < 60) return `${sign}${minutes}m ${absoluteSeconds % 60}s`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${sign}${hours}h ${minutes % 60}m`;
  const days = Math.floor(hours / 24);
  return `${sign}${days}d ${hours % 24}h`;
}

/**
 * Formats a 0-1 ratio as a locale percentage string with one fractional digit.
 * @param {unknown} value
 * @returns {string}
 */
export function formatPercent(value) {
  const numeric = value == null || value === '' ? Number.NaN : Number(value);
  return Number.isFinite(numeric)
    ? new Intl.NumberFormat('en', { style: 'percent', maximumFractionDigits: 1 }).format(numeric)
    : 'Not observed';
}

/**
 * Formats a timestamp relative to the dashboard's deterministic evaluation time.
 * @param {unknown} value
 * @param {unknown} relativeTo
 * @returns {string}
 */
export function formatRelativeTime(value, relativeTo) {
  const valueMs = Date.parse(String(value ?? ''));
  const relativeToMs = Date.parse(String(relativeTo ?? ''));
  if (!Number.isFinite(valueMs) || !Number.isFinite(relativeToMs)) return '';

  const differenceSeconds = (valueMs - relativeToMs) / 1000;
  const absoluteSeconds = Math.abs(differenceSeconds);
  const [divisor, unit] = absoluteSeconds < 60
    ? [1, 'second']
    : absoluteSeconds < 3_600
      ? [60, 'minute']
      : absoluteSeconds < 86_400
        ? [3_600, 'hour']
        : absoluteSeconds < 604_800
          ? [86_400, 'day']
          : [604_800, 'week'];
  const amount = Math.round(differenceSeconds / divisor);
  return new Intl.RelativeTimeFormat('en', { numeric: 'always' }).format(amount, /** @type {Intl.RelativeTimeFormatUnit} */ (unit));
}

/**
 * Formats an elapsed timestamp as compact dashboard chrome.
 * @param {unknown} value
 * @param {unknown} relativeTo
 * @returns {string}
 */
export function formatCompactElapsedTime(value, relativeTo) {
  const valueMs = Date.parse(String(value ?? ''));
  const relativeToMs = relativeTo instanceof Date
    ? relativeTo.getTime()
    : typeof relativeTo === 'number'
      ? relativeTo
      : Date.parse(String(relativeTo ?? ''));
  if (!Number.isFinite(valueMs) || !Number.isFinite(relativeToMs)) return '';

  const elapsedSeconds = Math.max(0, Math.floor((relativeToMs - valueMs) / 1_000));
  const [divisor, unit] = elapsedSeconds < 60
    ? [1, 's']
    : elapsedSeconds < 3_600
      ? [60, 'm']
      : elapsedSeconds < 86_400
        ? [3_600, 'h']
        : elapsedSeconds < 604_800
          ? [86_400, 'd']
          : [604_800, 'w'];
  return `${Math.floor(elapsedSeconds / divisor)}${unit} ago`;
}

/**
 * Formats a timestamp for quick scanning while retaining stable absolute dates
 * for older observations.
 * @param {unknown} value
 * @param {number | Date} [relativeTo]
 * @returns {string}
 */
export function formatHumanFriendlyTimestamp(value, relativeTo = Date.now()) {
  const valueMs = Date.parse(String(value ?? ''));
  const relativeToMs = relativeTo instanceof Date ? relativeTo.getTime() : relativeTo;
  if (!Number.isFinite(valueMs) || !Number.isFinite(relativeToMs)) return '';

  const differenceSeconds = (valueMs - relativeToMs) / 1_000;
  const absoluteSeconds = Math.abs(differenceSeconds);
  if (absoluteSeconds < 45) return 'just now';

  const roundedDifference = (/** @type {number} */ divisor) => (
    Math.sign(differenceSeconds) * Math.round(absoluteSeconds / divisor)
  );
  if (absoluteSeconds < 90) return humanRelativeTimeFormat().format(Math.sign(differenceSeconds), 'minute');
  if (absoluteSeconds < 45 * 60) return humanRelativeTimeFormat().format(roundedDifference(60), 'minute');
  if (absoluteSeconds < 90 * 60) return humanRelativeTimeFormat().format(Math.sign(differenceSeconds), 'hour');
  if (absoluteSeconds < 22 * 3_600) return humanRelativeTimeFormat().format(roundedDifference(3_600), 'hour');
  if (absoluteSeconds < 36 * 3_600) return humanRelativeTimeFormat().format(Math.sign(differenceSeconds), 'day');
  if (absoluteSeconds < 7 * 86_400) return humanRelativeTimeFormat().format(roundedDifference(86_400), 'day');

  const valueDate = new Date(valueMs);
  const relativeDate = new Date(relativeToMs);
  const dateFormatter = valueDate.getUTCFullYear() === relativeDate.getUTCFullYear()
    ? humanDateFormat()
    : humanDateWithYearFormat();
  return dateFormatter.format(valueDate);
}

/**
 * @param {number} value
 * @returns {number}
 */
function fractionDigits(value) {
  const [mantissa, exponentText = '0'] = value.toString().toLowerCase().split('e');
  const fractionLength = mantissa.split('.')[1]?.length ?? 0;
  return Math.min(100, Math.max(0, fractionLength - Number(exponentText)));
}

const TEMPLATE_TOKEN_PATTERN = /\{\{([a-zA-Z0-9_-]+)(?::(suffix|word):([^:}]*):([^:}]*))?\}\}/g;

/**
 * Renders a JSON-configurable copy template against a set of named values, so that
 * pluralization and count-driven UI copy can be expressed as data instead of code.
 * Supported tokens: `{{name}}` (raw value), `{{name:suffix:singular:plural}}` (appends a
 * pluralization suffix based on whether `name` equals 1), and `{{name:word:singular:plural}}`
 * (substitutes a whole word based on the same rule).
 * @param {string} template
 * @param {Record<string, unknown>} values
 * @returns {string}
 */
export function renderTemplate(template, values) {
  return template.replace(TEMPLATE_TOKEN_PATTERN, (match, name, mode, singular, plural) => {
    const value = values[name];
    if (mode === undefined) {
      return value === undefined ? '' : String(value);
    }
    const isSingular = Number(value) === 1;
    return isSingular ? singular : plural;
  });
}

/**
 * Resolves a status label for a ratio against an ordered list of JSON-configured
 * thresholds. Each threshold declares an optional `max` (exclusive upper bound) and a
 * `status`; the last entry without a `max` acts as the fallback for higher ratios.
 * @param {number} ratio
 * @param {Array<{ max?: number, status: string }>} thresholds
 * @returns {string}
 */
export function resolveThresholdStatus(ratio, thresholds) {
  for (const threshold of thresholds) {
    if (typeof threshold.max !== 'number') {
      return threshold.status;
    }
    if (ratio < threshold.max) {
      return threshold.status;
    }
  }
  return 'unknown';
}
