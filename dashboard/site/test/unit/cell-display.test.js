// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { renderCellDisplay } from '../../src/components/cell-display.js';

describe('table cell display helper', () => {
  it.each([
    ['bytes', 0, '0 B'],
    ['bytes', 512, '512 B'],
    ['bytes', 1_000, '1.0 KB'],
    ['bytes', 890_880_000, '891 MB'],
    ['bytes', 1_228_800_000, '1.2 GB'],
    ['bytes', 8_601_600_000_000, '8.6 TB'],
    ['compact-number', 0, '0'],
    ['compact-number', 100_000, '100K'],
    ['compact-number', 50_000_000, '50M'],
    ['compact-number', 31_000_000_000_000, '31T'],
    ['compact-number', -1_234, '-1.23K'],
    ['compact-number', 0.000278, '0.000278']
  ])('renders %s value %s as %s while retaining exact accessible text', (format, value, expected) => {
    const unit = { name: format === 'bytes' ? 'Bytes' : 'Modeled count', symbol: format === 'bytes' ? 'B' : 'count',
      significant: format === 'bytes' ? 1 : 0.000001, format };
    const rendered = /** @type {HTMLElement} */ (
      renderCellDisplay(undefined, value, String, unit, 'quantitative')
    );
    expect(rendered.textContent).toBe(expected);
    expect(rendered.title).toBe(`${value.toLocaleString('en', { maximumSignificantDigits: 21 })} ${unit.name}`);
    expect(rendered.getAttribute('aria-label')).toBe(`${expected} (${rendered.title})`);
  });

  it.each(['bytes', 'compact-number'])('keeps absent and invalid %s values distinct from zero', (format) => {
    const unit = { name: 'Test unit', symbol: 'test', significant: 1, format };
    for (const value of [null, undefined, '', NaN, Infinity, 'invalid']) {
      expect(renderCellDisplay(undefined, value, String, unit, 'quantitative')).toBe('');
    }
  });

  it('DLS-VIEW-004 renders JSON-selected display types through one generic helper', () => {
    /** @param {unknown} value */
    const toText = (value) => value == null || value === '' ? 'unknown' : String(value);

    const mode = renderCellDisplay('mode', 'live', toText);
    const activeState = renderCellDisplay('active-state', 'true', toText);
    const status = renderCellDisplay('status', 'failure', toText);
    const repositoryStatus = renderCellDisplay('status', 'Needs attention', toText);
    const graderPass = renderCellDisplay('grader-status', 'pass', toText);
    const graderUnavailable = renderCellDisplay('grader-status', 'unavailable', toText);

    expect(mode).toBeInstanceOf(HTMLElement);
    expect(/** @type {HTMLElement} */ (mode).className).toBe('mode-badge mode-live');
    expect(activeState).toBeInstanceOf(HTMLElement);
    expect(/** @type {HTMLElement} */ (activeState).className).toBe('status status-success');
    expect(status).toBeInstanceOf(HTMLElement);
    expect(/** @type {HTMLElement} */ (status).className).toBe('status status-danger');
    expect(/** @type {HTMLElement} */ (repositoryStatus).className).toBe('status status-danger');
    expect(/** @type {HTMLElement} */ (graderPass).className).toBe('status status-success');
    expect(/** @type {HTMLElement} */ (graderUnavailable).className).toBe('status status-attention');
    expect(renderCellDisplay('label', 'matured', toText)).toBe('Mature');
    expect(renderCellDisplay(undefined, null, toText, null, 'quantitative')).toBe('');
    expect(renderCellDisplay(undefined, 0, toText, null, 'quantitative')).toBe('0');
    expect(renderCellDisplay(undefined, 11648.874562937062, toText, null, 'quantitative')).toBe('11648.875');
    expect(renderCellDisplay(undefined, 11648.874562937062, toText, null, 'nominal')).toBe('11648.875');
    expect(renderCellDisplay(undefined, '11648.874562937062', toText, null, 'nominal')).toBe('11648.874562937062');
    expect(/** @type {HTMLElement} */ (renderCellDisplay('digest', '1234567890abcdef', toText)).textContent).toBe('1234567890ab');
    expect(renderCellDisplay(undefined, 'plain', toText)).toBe('plain');
    expect(renderCellDisplay(undefined, '.github/workflows/daily.md', toText, null, 'nominal', 'workflow-relative-path')).toBe('daily.md');
    expect(renderCellDisplay(undefined, 'nested/daily.md', toText, null, 'nominal', 'workflow-relative-path')).toBe('nested/daily.md');
    const workflowRun = /** @type {HTMLAnchorElement} */ (renderCellDisplay(
      undefined,
      'https://github.com/githubnext/gh-cao/actions/runs/12345',
      toText,
      null,
      'nominal',
      'workflow-run-url'
    ));
    expect(workflowRun.tagName).toBe('A');
    expect(workflowRun.href).toBe('https://github.com/githubnext/gh-cao/actions/runs/12345');
    expect(workflowRun.textContent).toContain('12345');
    expect(workflowRun.target).toBe('_blank');
    expect(workflowRun.rel).toBe('noopener noreferrer');
    expect(renderCellDisplay(undefined, 'https://example.com/actions/runs/12345', toText, null, 'nominal', 'workflow-run-url'))
      .toBe('https://example.com/actions/runs/12345');
    const shortenedUrl = /** @type {HTMLAnchorElement} */ (renderCellDisplay(
      undefined,
      'https://dashboard.example/gh-aw-logs-shards/logs-1.jsonl',
      toText,
      null,
      'nominal',
      'shortened-url'
    ));
    expect(shortenedUrl.href).toBe('https://dashboard.example/gh-aw-logs-shards/logs-1.jsonl');
    expect(shortenedUrl.textContent).toContain('https://dashboard.example/.../logs-1.jsonl');
    expect(shortenedUrl.getAttribute('aria-label')).toBe('https://dashboard.example/.../logs-1.jsonl');
    expect(shortenedUrl.target).toBe('_blank');
    expect(shortenedUrl.rel).toBe('noopener noreferrer');
    expect(renderCellDisplay(undefined, 'gh-aw-jsonl', toText, null, 'nominal', 'shortened-url')).toBe('gh-aw-jsonl');
    expect(renderCellDisplay('unsupported', null, toText)).toBe('');
    expect(renderCellDisplay(undefined, null, toText, null, 'temporal', 'human-friendly-timestamp')).toBe('');
    const temporal = /** @type {HTMLElement} */ (renderCellDisplay(undefined, '2026-08-30T07:00:00Z', toText, null, 'temporal'));
    expect(temporal.tagName).toBe('TIME');
    expect(temporal.getAttribute('datetime')).toBe('2026-08-30T07:00:00Z');
    expect(temporal.textContent).toBe('Aug 30, 2026, 7:00 AM');
    const recent = /** @type {HTMLElement} */ (renderCellDisplay(
      undefined,
      new Date(Date.now() - 5 * 60_000).toISOString(),
      toText,
      null,
      'temporal',
      'human-friendly-timestamp'
    ));
    expect(recent.tagName).toBe('TIME');
    expect(recent.textContent).toBe('5 minutes ago');
    expect(recent.title).toMatch(/ UTC$/);
    expect(recent.getAttribute('aria-label')).toBe(`5 minutes ago (${recent.title})`);
    expect(renderCellDisplay(undefined, 2.5, toText, {
      name: 'AI Credits',
      symbol: 'AIC',
      significant: 1
    })).toBe('3 AIC');
  });

  it('DLS-TEST-003 marks absent workflow paths as missing data while leaving other absent values blank', () => {
    /** @param {unknown} value */
    const toText = (value) => value == null || value === '' ? 'unknown' : String(value);

    for (const absent of [null, undefined, '']) {
      const missing = /** @type {HTMLElement} */ (renderCellDisplay(undefined, absent, toText, null, 'nominal', 'workflow-relative-path'));
      expect(missing).toBeInstanceOf(HTMLElement);
      expect(missing.className).toBe('table-missing-value');
      expect(missing.textContent).toBe('missing data');
      expect(missing.hasAttribute('data-missing-value')).toBe(true);
    }

    expect(renderCellDisplay(undefined, '', toText, null, 'nominal')).toBe('');
    expect(renderCellDisplay(undefined, 0, toText, null, 'nominal', 'workflow-relative-path')).toBe('0');
  });
});
