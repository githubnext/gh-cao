import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWorkflowValueReport, renderWorkflowValueSnapshot } from '../../src/components/workflow-value-report.js';
import { configureSourceLoader, publishSource, resetSourceStore } from '../../src/source-store.js';
import { dashboardViewAliasName } from '../../src/data/queries/view-payload-compiler.js';
import { DataRequestWorker } from '../data-request-worker.js';
import fragment from '../../dashboard-fragments/workflow-value.json' with { type: 'json' };

const reportData = fragment.pages.find(page => page.id === 'value-report')?.views[0].data;
if (!reportData || !('sources' in reportData)) throw new Error('Native report sources must be declared');
const names = reportData.sources;
/** @type {import('../../src/presenter.js').SourceMetadata} */
const metadata = {
  'source-id': 'value-report-component', 'source-kind': 'fixture',
  'as-of': '2026-10-20T11:13:27Z', 'retrieved-at': '2026-10-20T11:13:27Z',
  completeness: /** @type {const} */ ('complete'), freshness: /** @type {const} */ ('unknown'),
  availability: /** @type {const} */ ('available')
};
/** @type {Record<string, unknown>} */
const summary = {
  'report-id': 'example', 'workflow-name': 'File diet', goal: '<script>Untrusted workflow goal</script>',
  'observed-at': metadata['as-of'], 'window-start': '2025-11-15T13:36:21Z', 'window-end': '2026-10-20T10:51:42Z',
  'workflow-value-href': '#page-workflow-value?workflow=org%2Frepo%3Afile-diet',
  'output-count': 2, 'output-unit': 'issues', 'accepted-implementations': 1, 'implementation-unit': 'merged PRs',
  'show-workflow-trail': true,
  'native-result': 1, 'metric-unit': 'files', 'complete-report': true,
  'snapshot-name': 'Oversized production Go files', 'current-value': 54, 'snapshot-unit': 'files',
  'current-snapshot-href': '#page-value-snapshot?snapshot=example%7Clast',
  'change-label': '↑ +38 files · Worse', movement: 'Worse', 'comparison-label': "Since 2025-11-15 · Everyone's work",
  'direction-label': '↓ Lower is better', 'recent-result': 0, 'previous-result': 1,
  'result-label': 'file reductions'
};
/** @param {Record<string, unknown>[][]} [rows] */
function context(rows = [[summary], [], [], [], [], [], [
  { at: '2025-11-15T13:36:21Z', value: 16, 'snapshot-href': '#page-value-snapshot?snapshot=example%7Cfirst' },
  { at: '2026-10-20T10:51:42Z', value: 54, 'snapshot-href': '#page-value-snapshot?snapshot=example%7Clast' }
], [], []]) {
  return {
    pageId: 'value-report', viewId: 'native-value-report', title: 'Value report', sourceNames: names,
    routeParameters: { report: 'example' }, contextDetails: [], headingTag: /** @type {const} */ ('h2'),
    sources: Object.fromEntries(names.map((name, index) => [name, { source: name, metadata, rows: rows[index] ?? [] }]))
  };
}
beforeEach(() => {
  vi.stubGlobal('Worker', DataRequestWorker);
});
afterEach(() => {
  document.body.replaceChildren();
  resetSourceStore();
  vi.unstubAllGlobals();
});

describe('native workflow value report', () => {
  it('renders the supplied shared result, no source queries or HTML execution, with exact snapshot links', () => {
    const loader = vi.fn();
    configureSourceLoader(loader);
    const root = renderWorkflowValueReport(context());
    document.body.append(root);
    expect(root.querySelector('script')).toBeNull();
    expect(root.textContent).toContain(summary.goal);
    expect(root.querySelector('.value-report-movement')?.textContent).toBe('↑ +38 files · Worse');
    expect(root.querySelector('.value-report-number')?.textContent).toBe('54 files');
    expect(root.querySelectorAll('.value-snapshot-guide')).toHaveLength(1);
    expect(root.querySelectorAll('svg a')[1].getAttribute('href')).toBe(summary['current-snapshot-href']);
    expect(root.querySelector('.value-snapshot-direction')?.textContent).toBe('↓ Lower is better');
    expect(root.querySelector('details')?.open).toBe(false);
    expect(loader).not.toHaveBeenCalled();
    root.querySelector('button')?.click();
    expect(root.querySelector('details')?.open).toBe(true);
    expect((/** @type {HTMLDetailsElement | null} */ (root.querySelector('[data-value-section="outputs"]')))?.open).toBe(true);
    expect(document.activeElement).toBe(root.querySelector('[data-value-section="outputs"] > summary'));
  });

  it('discloses one counted category at a time and progressively renders searchable sortable evidence', async () => {
    const input = context();
    input.sources[names[3]].rows = Array.from({ length: 60 }, (_, index) => {
      const number = String(index + 1).padStart(3, '0');
      return {
        label: `Output ${number}`,
        url: `https://github.com/org/repo/issues/${index + 1}`,
        at: `2026-09-${String(index % 28 + 1).padStart(2, '0')}T00:00:00Z`,
        state: index % 2 === 0 ? 'open' : 'closed',
        origin: 'receipt-verified'
      };
    });
    input.sources[names[4]].rows = [{
      'output-label': 'Output 001',
      'output-url': 'https://github.com/org/repo/issues/1',
      'implementation-label': 'Implementation 001',
      'implementation-url': 'https://github.com/org/repo/pull/1',
      'trail-accepted-at': metadata['as-of'],
      'implementation-value': 0
    }];
    input.sources[names[7]].rows = [{ label: 'Tests', status: 'verified', detail: 'Focused checks passed.' }];
    input.sources[names[8]].rows = [{ label: 'Attribution', detail: 'Contribution is not exclusive causal credit.' }];
    const root = renderWorkflowValueReport(input);
    document.body.append(root);

    const outputButton = root.querySelector('[aria-label^="Safe outputs:"]');
    expect(outputButton).toBeInstanceOf(HTMLButtonElement);
    /** @type {HTMLButtonElement} */ (outputButton).click();
    const outputCategory = /** @type {HTMLDetailsElement} */ (root.querySelector('[data-value-section="outputs"]'));
    const outputSummary = outputCategory.querySelector(':scope > summary');
    expect(outputCategory.open).toBe(true);
    expect(outputSummary?.textContent).toContain('60 outputs');
    expect(document.activeElement).toBe(outputSummary);
    expect([...root.querySelectorAll('[data-value-section]')].filter(category => (
      category instanceof HTMLDetailsElement && category.open
    ))).toEqual([outputCategory]);

    const table = /** @type {HTMLElement} */ (root.querySelector('[data-evidence-table="outputs"]'));
    const attachedRows = () => [...table.querySelectorAll('tbody > tr')]
      .filter(row => row.parentElement instanceof HTMLTableSectionElement);
    await vi.waitFor(() => expect(attachedRows()).toHaveLength(25));
    await vi.waitFor(() => expect(table.querySelector('.table-filter-result')?.textContent).toBe('Showing 25 of 60 outputs'));

    const outputSort = /** @type {HTMLButtonElement} */ (table.querySelector('[data-table-sort="0"]'));
    outputSort.click();
    outputSort.click();
    await vi.waitFor(() => expect(outputSort.closest('th')?.getAttribute('aria-sort')).toBe('descending'));
    await vi.waitFor(() => {
      const rows = attachedRows();
      const first = /** @type {HTMLTableRowElement} */ (rows[0]).cells[0]?.textContent ?? '';
      const last = /** @type {HTMLTableRowElement} */ (rows.at(-1)).cells[0]?.textContent ?? '';
      expect(first.localeCompare(last)).toBeGreaterThan(0);
    });
    const firstSortedLabel = /** @type {HTMLTableRowElement} */ (attachedRows()[0]).cells[0]?.textContent ?? '';

    const filter = /** @type {HTMLInputElement} */ (table.querySelector('[data-table-filter]'));
    filter.value = firstSortedLabel;
    filter.dispatchEvent(new Event('input'));
    await vi.waitFor(() => expect(attachedRows()).toHaveLength(1));
    expect(table.querySelector('.table-filter-result')?.textContent).toBe('Showing 1 of 1 output');

    filter.value = '';
    filter.dispatchEvent(new Event('input'));
    await vi.waitFor(() => expect(attachedRows()).toHaveLength(25));
    /** @type {HTMLButtonElement} */ (table.querySelector('[data-table-more]')).click();
    await vi.waitFor(() => expect(attachedRows()).toHaveLength(50));
    expect(table.querySelector('.table-filter-result')?.textContent).toBe('Showing 50 of 60 outputs');
    /** @type {HTMLButtonElement} */ (table.querySelector('[data-table-more]')).click();
    await vi.waitFor(() => expect(table.querySelector('.table-filter-result')?.textContent).toBe('Showing 60 of 60 outputs'));
    expect(attachedRows()).toHaveLength(50);
    expect(table.querySelector('.table-filter-result')?.textContent).toBe('Showing 60 of 60 outputs');

    const trailsCategory = /** @type {HTMLDetailsElement} */ (root.querySelector('[data-value-section="trails"]'));
    /** @type {HTMLElement} */ (trailsCategory.querySelector(':scope > summary')).click();
    await vi.waitFor(() => {
      expect(trailsCategory.open).toBe(true);
      expect(outputCategory.open).toBe(false);
    });

    /** @type {HTMLButtonElement} */ (root.querySelector('.value-report-quality')).click();
    const checksCategory = /** @type {HTMLDetailsElement} */ (root.querySelector('[data-value-section="checks"]'));
    await vi.waitFor(() => {
      expect(checksCategory.open).toBe(true);
      expect(trailsCategory.open).toBe(false);
    });
    expect(checksCategory.querySelector(':scope > summary')?.textContent).toContain('1 check · 1 limitation');
    expect(document.activeElement).toBe(checksCategory.querySelector(':scope > summary'));
  });

  it('does not turn unavailable source metadata into a successful report', () => {
    const input = context();
    input.sources[names[5]].metadata = { ...metadata, availability: 'unavailable' };
    const root = renderWorkflowValueReport(input);
    expect(root.textContent).toContain('Value evidence is unavailable');
    expect(root.querySelector('.value-report-number')).toBeNull();
  });

  it('updates only owned DOM from subscribed query results and stops after detachment', async () => {
    const input = context();
    const root = renderWorkflowValueReport(input);
    document.body.append(root);
    root.querySelector('button')?.click();
    const focused = document.activeElement;
    const key = dashboardViewAliasName(input.pageId, { id: input.viewId }, 0, names[0], 0);
    publishSource(names[0], { source: names[0], metadata, rows: [{ ...summary, 'current-value': 15, 'change-label': '↓ -1 files · Better', movement: 'Better' }] }, key);
    await vi.waitFor(() => expect(root.querySelector('.value-report-movement')?.textContent).toBe('↓ -1 files · Better'));
    expect(root.querySelector('.value-report-number')?.textContent).toBe('15 files');
    expect(root.querySelector('details')?.open).toBe(true);
    expect((/** @type {HTMLDetailsElement | null} */ (root.querySelector('[data-value-section="outputs"]')))?.open).toBe(true);
    expect(document.activeElement).toBe(focused);
    root.remove();
    await Promise.resolve();
    publishSource(names[0], { source: names[0], metadata, rows: [{ ...summary, 'current-value': 99 }] }, key);
    await Promise.resolve();
    expect(root.querySelector('.value-report-number')?.textContent).toBe('15 files');
  });

  it('preserves negative effects and renders exact item evidence as safe text', () => {
    const input = context();
    input.sources[names[4]].rows.push({ 'output-label': 'org/repo#1', 'output-url': 'https://github.com/org/repo/issues/1',
      'implementation-label': 'org/repo#2', 'implementation-url': 'https://github.com/org/repo/pull/2',
      'trail-accepted-at': metadata['as-of'], 'implementation-value': -1 });
    input.sources[names[5]].rows.push({ 'implementation-label': 'org/repo#2', 'implementation-url': 'https://github.com/org/repo/pull/2',
      label: '<img src=x onerror=alert(1)>', url: 'javascript:alert(1)' });
    const root = renderWorkflowValueReport(input);
    expect(root.querySelector('[data-value-section="trails"]')?.textContent).toContain('-1');
    expect(root.querySelector('a[href="https://github.com/org/repo/pull/2"]')).not.toBeNull();
    expect(root.querySelector('img, a[href^="javascript:"]')).toBeNull();
    expect(root.querySelector('[data-value-section="files"]')?.textContent).toContain('<img src=x');
  });

  it('distinguishes missing snapshot items from a retained empty population', () => {
    const input = context();
    input.sourceNames = ['snapshot', 'items'];
    input.sources = {
      snapshot: { source: 'snapshot', metadata, rows: [{ value: 0, at: metadata['as-of'], 'report-id': 'example', 'items-available': false }] },
      items: { source: 'items', metadata, rows: [] }
    };
    expect(renderWorkflowValueSnapshot(input).textContent).toContain('Item-level evidence was not retained');
    input.sources.snapshot.rows[0]['items-available'] = true;
    expect(renderWorkflowValueSnapshot(input).textContent).toContain('retained item list is empty');
  });
});
