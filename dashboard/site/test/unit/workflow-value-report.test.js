import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWorkflowValueReport, renderWorkflowValueSnapshot } from '../../src/components/workflow-value-report.js';
import { configureSourceLoader, publishSource, resetSourceStore } from '../../src/source-store.js';
import { dashboardViewAliasName } from '../../src/data/queries/view-payload-compiler.js';
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
afterEach(() => {
  document.body.replaceChildren();
  resetSourceStore();
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
    expect(document.activeElement).toBe(root.querySelector('[data-value-section="outputs"]'));
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
