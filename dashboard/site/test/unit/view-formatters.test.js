import { describe, expect, it } from 'vitest';
import { formatAggregateValue, formatAicc, formatClockDuration, formatCompactElapsedTime, formatHumanFriendlyTimestamp, formatNumber, formatPercent, formatRelativeTime, formatString, formatUsd, renderTemplate, resolveThresholdStatus, stringOrFallback, toNumber } from '../../src/view-formatters.js';

/**
 * @param {unknown} value
 * @returns {string}
 */
function toText(value) {
  return value == null || value === '' ? 'unknown' : String(value);
}

describe('view formatter helpers', () => {
  it('formats declared byte and compact count units across metrics and tables', () => {
    const bytes = { name: 'Bytes', symbol: 'B', significant: 1, format: 'bytes' };
    const count = { name: 'Modeled count', symbol: 'count', significant: 0.01, format: 'compact-number' };
    expect(formatNumber(890_880_000, bytes)).toBe('891 MB');
    expect(formatNumber(8_601_600_000_000, bytes)).toBe('8.6 TB');
    expect(formatNumber(-1_500, bytes)).toBe('-1.5 KB');
    expect(formatNumber(100_000, count)).toBe('100K');
    expect(formatNumber(50_000_000, count)).toBe('50M');
    expect(formatNumber(1.235, count)).toBe('1.24');
    expect(formatNumber(-1.235, count)).toBe('-1.24');
    expect(formatAggregateValue([{ bytes: 890_880_000 }], 'bytes', 'sum', toText, bytes)).toBe('891 MB');
    expect(formatAggregateValue([{ count: 100_000 }, { count: 50_000 }], 'count', 'sum', toText, count)).toBe('150K');
  });

  it('formats workflow-relative paths without changing other strings', () => {
    expect(formatString('.github/workflows/daily.md', 'workflow-relative-path')).toBe('daily.md');
    expect(formatString('.github/workflows/nested/daily.md', 'workflow-relative-path')).toBe('nested/daily.md');
    expect(formatString('daily.md', 'workflow-relative-path')).toBe('daily.md');
    expect(formatString(null, 'workflow-relative-path')).toBe('unknown');
    expect(formatString('.github/workflows/', 'workflow-relative-path')).toBe('unknown');
  });

  it('formats workflow identities as compact workflow and repository labels', () => {
    expect(formatString(
      'github/gh-aw-mcpg:.github/workflows/repo-assist.md',
      'workflow-identity-label'
    )).toBe('repo-assist.md (github/gh-aw-mcpg)');
    expect(formatString(
      'github/gh-aw-mcpg:.github/workflows/nested/repo-assist.md',
      'workflow-identity-label'
    )).toBe('nested/repo-assist.md (github/gh-aw-mcpg)');
    expect(formatString('repo-assist.md', 'workflow-identity-label')).toBe('repo-assist.md');
  });

  it('DLS-VIEW-013 formats aggregate metric values for count, distinct-count, sum, mean, min, max, and default field access', () => {
    const rows = [
      { aic: 12, repository: 'repo-a', score: 1.5 },
      { aic: 18, repository: 'repo-b', score: 2.5 },
      { aic: null, repository: 'repo-a', score: 3 }
    ];

    expect(formatAggregateValue(rows, 'aic', 'count', toText)).toBe('2');
    expect(formatAggregateValue(rows, 'repository', 'distinct-count', toText)).toBe('2');
    expect(formatAggregateValue(rows, 'aic', 'sum', toText)).toBe('30');
    expect(formatAggregateValue(rows, 'score', 'mean', toText)).toBe('2.333');
    expect(formatAggregateValue(rows, 'score', 'min', toText)).toBe('1.50');
    expect(formatAggregateValue(rows, 'score', 'max', toText)).toBe('3');
    expect(formatAggregateValue(rows, 'repository', 'none', toText)).toBe('repo-a');
  });

  it('DLS-VIEW-013 formats aggregate metric edge cases for missing fields, empty rows, and non-numeric values', () => {
    expect(formatAggregateValue([], 'aic', 'sum', toText)).toBe('');
    expect(formatAggregateValue([], 'aic', 'mean', toText)).toBe('');
    expect(formatAggregateValue([], 'aic', 'none', toText)).toBe('');
    expect(formatAggregateValue([{ aic: 'bad' }], 'aic', 'mean', toText)).toBe('');
    expect(formatAggregateValue([{ aic: 0 }], 'aic', 'sum', toText)).toBe('0');
    expect(formatAggregateValue([{ repository: '' }], 'repository', 'distinct-count', toText)).toBe('');
    expect(formatAggregateValue([{ repository: 'repo-a' }], null, 'count', toText)).toBe('');
  });

  it('formats shared numeric helpers deterministically', () => {
    expect(toNumber(12)).toBe(12);
    expect(toNumber('12')).toBe(0);
    expect(formatNumber(2)).toBe('2');
    expect(formatNumber(2.5)).toBe('2.50');
    expect(formatNumber(11648.874562937062)).toBe('11648.875');
    expect(formatNumber(-1.23456)).toBe('-1.235');
    expect(formatNumber(2.5, { name: 'AI Credits', symbol: 'AIC', significant: 1 })).toBe('3 AIC');
    expect(formatNumber(-2.5, { name: 'AI Credits', symbol: 'AIC', significant: 1 })).toBe('-3 AIC');
    expect(formatNumber(2.5, { name: 'AI Credits', symbol: 'AIC', significant: 1, format: 'number' })).toBe('3');
    expect(formatNumber(0.0341, { name: 'US dollars', symbol: 'USD', significant: 0.001, format: 'number' })).toBe('0.034');
    expect(formatNumber(1.24, { name: 'Dollars', symbol: 'USD', significant: 0.01 })).toBe('1.24 USD');
    const usd = { name: 'US dollars', symbol: 'USD', significant: 0.001, format: 'usd' };
    expect(formatNumber(0.0341, usd)).toBe('$0.035');
    expect(formatNumber(1, usd)).toBe('$1.00');
    const aicc = { name: 'AICc($)', symbol: 'cAIC', significant: 2, format: 'aicc' };
    expect(formatNumber(341, aicc)).toBe('$3.42');
    expect(formatNumber(2.5, aicc)).toBe('$0.02');
    expect(formatNumber(-2.5, aicc)).toBe('-$0.02');
    const duration = { name: 'Human-friendly duration', symbol: 's', significant: 1, format: 'duration' };
    expect(formatNumber(45, duration)).toBe('45s');
    expect(formatNumber(5_000, duration)).toBe('1h 23m');
    expect(formatNumber(90, duration)).toBe('1m 30s');
    expect(formatNumber(97_200, duration)).toBe('1d 3h');
    expect(formatNumber(-5_000, duration)).toBe('-1h 23m');
    expect(formatAggregateValue(rowsWithUnit(), 'aic', 'sum', toText, {
      name: 'AI Credits',
      symbol: 'AIC',
      significant: 1
    })).toBe('3 AIC');
  });

  it('formats USD with at most three decimals and rounds upward', () => {
    expect(formatUsd(12)).toBe('$12.00');
    expect(formatUsd(12.3)).toBe('$12.30');
    expect(formatUsd(12.345)).toBe('$12.345');
    expect(formatUsd(12.3451)).toBe('$12.346');
    expect(formatUsd(0.0001)).toBe('$0.001');
    expect(formatUsd(0.00049)).toBe('$0.001');
    expect(formatUsd(-0.0004)).toBe('$0.00');
  });

  it('formats AIC cost as US dollars using the browser internationalization API', () => {
    expect(formatAicc(0)).toBe('$0.00');
    expect(formatAicc(1)).toBe('$0.01');
    expect(formatAicc(123_456)).toBe('$1,234.56');
    expect(formatAicc(-150)).toBe('-$1.50');
  });

  it('formats a 0-1 ratio as a locale percentage string', () => {
    expect(formatPercent(0.5)).toBe('50%');
    expect(formatPercent(0.256)).toBe('25.6%');
    expect(formatPercent(0)).toBe('0%');
    expect(formatPercent(1)).toBe('100%');
    expect(formatPercent('0.5')).toBe('50%');
    expect(formatPercent(null)).toBe('Not observed');
    expect(formatPercent('')).toBe('Not observed');
    expect(formatPercent('not a number')).toBe('Not observed');
  });

  it('formats timestamps relative to the dashboard evaluation time', () => {
    expect(formatRelativeTime('2026-09-03T11:48:00Z', '2026-09-03T12:38:00Z')).toBe('50 minutes ago');
    expect(formatRelativeTime('2026-09-03T13:08:00Z', '2026-09-03T12:38:00Z')).toBe('in 30 minutes');
    expect(formatRelativeTime('invalid', '2026-09-03T12:38:00Z')).toBe('');
  });

  it('formats human-friendly timestamps for quick scanning', () => {
    const now = new Date('2026-09-10T14:00:00Z');
    expect(formatHumanFriendlyTimestamp('2026-09-10T13:59:16Z', now)).toBe('just now');
    expect(formatHumanFriendlyTimestamp('2026-09-10T13:59:15Z', now)).toBe('1 minute ago');
    expect(formatHumanFriendlyTimestamp('2026-09-10T13:58:30Z', now)).toBe('2 minutes ago');
    expect(formatHumanFriendlyTimestamp('2026-09-10T13:59:40Z', now)).toBe('just now');
    expect(formatHumanFriendlyTimestamp('2026-09-10T13:55:00Z', now)).toBe('5 minutes ago');
    expect(formatHumanFriendlyTimestamp('2026-09-10T14:05:00Z', now)).toBe('in 5 minutes');
    expect(formatHumanFriendlyTimestamp('2026-09-10T13:15:00Z', now)).toBe('1 hour ago');
    expect(formatHumanFriendlyTimestamp('2026-09-10T12:30:00Z', now)).toBe('2 hours ago');
    expect(formatHumanFriendlyTimestamp('2026-09-09T14:00:00Z', now)).toBe('yesterday');
    expect(formatHumanFriendlyTimestamp('2026-09-04T14:00:01Z', now)).toBe('6 days ago');
    expect(formatHumanFriendlyTimestamp('2026-09-03T14:00:00Z', now)).toBe('Sep 3');
    expect(formatHumanFriendlyTimestamp('2025-09-03T14:00:00Z', now)).toBe('Sep 3, 2025');
    expect(formatHumanFriendlyTimestamp('invalid', now)).toBe('');
  });

  it('formats compact elapsed timestamps for dashboard chrome', () => {
    expect(formatCompactElapsedTime('2026-09-03T12:37:42Z', '2026-09-03T12:38:00Z')).toBe('18s ago');
    expect(formatCompactElapsedTime('2026-09-03T11:48:00Z', '2026-09-03T12:38:00Z')).toBe('50m ago');
    expect(formatCompactElapsedTime('2026-09-03T13:08:00Z', '2026-09-03T12:38:00Z')).toBe('0s ago');
    expect(formatCompactElapsedTime('invalid', '2026-09-03T12:38:00Z')).toBe('');
  });

  it('renders JSON-configured copy templates with plain, suffix, and word substitutions', () => {
    expect(renderTemplate('{{count}} failed run{{count:suffix::s}}', { count: 1 })).toBe('1 failed run');
    expect(renderTemplate('{{count}} failed run{{count:suffix::s}}', { count: 3 })).toBe('3 failed runs');
    expect(renderTemplate('Across {{repositories}} repositor{{repositories:suffix:y:ies}}', { repositories: 1 })).toBe('Across 1 repository');
    expect(renderTemplate('Across {{repositories}} repositor{{repositories:suffix:y:ies}}', { repositories: 3 })).toBe('Across 3 repositories');
    expect(renderTemplate('{{count}} run{{count:suffix::s}} {{status:word:is:are}} pending', { count: 2, status: 2 })).toBe('2 runs are pending');
    expect(renderTemplate('{{missing}} unavailable', {})).toBe(' unavailable');
  });

  function rowsWithUnit() {
    return [{ aic: 1.4 }, { aic: 1.2 }];
  }

  it('resolves an ordered, JSON-configured threshold list to a status label', () => {
    const thresholds = [
      { max: 0.5, status: 'low' },
      { max: 0.8, status: 'medium' },
      { status: 'high' }
    ];

    expect(resolveThresholdStatus(0.2, thresholds)).toBe('low');
    expect(resolveThresholdStatus(0.5, thresholds)).toBe('medium');
    expect(resolveThresholdStatus(0.79, thresholds)).toBe('medium');
    expect(resolveThresholdStatus(0.8, thresholds)).toBe('high');
    expect(resolveThresholdStatus(5, thresholds)).toBe('high');
  });

  it('substitutes a fallback for nullish or empty values, otherwise stringifies the value', () => {
    expect(stringOrFallback(null, 'unknown')).toBe('unknown');
    expect(stringOrFallback(undefined, 'unavailable')).toBe('unavailable');
    expect(stringOrFallback('', 'unknown')).toBe('unknown');
    expect(stringOrFallback('review', 'unknown')).toBe('review');
    expect(stringOrFallback(0, 'unknown')).toBe('0');
  });

  it('formats cascading clock durations across seconds, minutes, hours, and days tiers', () => {
    expect(formatClockDuration(45_000)).toBe('45s');
    expect(formatClockDuration(90_000)).toBe('1m 30s');
    expect(formatClockDuration(7_260_000)).toBe('2h 1m');
    expect(formatClockDuration(97_200_000)).toBe('1d 3h');
    expect(formatClockDuration(-1_000)).toBe('0s');
  });

  it('omits the days tier when includeDays is false, rolling straight into hours', () => {
    expect(formatClockDuration(97_200_000, { includeDays: false })).toBe('27h 0m');
    expect(formatClockDuration(45_000, { includeDays: false })).toBe('45s');
  });
});
