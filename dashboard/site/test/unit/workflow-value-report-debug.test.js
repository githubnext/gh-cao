// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import fragment from '../../dashboard-fragments/workflow-value.json' with { type: 'json' };

const reportData = fragment.pages.find(page => page.id === 'value-report')?.views[0].data;
if (!reportData || !('sources' in reportData)) throw new Error('Native report sources must be declared');
const names = reportData.sources;

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
  document.body.replaceChildren();
});

/**
 * @param {ReturnType<typeof vi.fn>} debugFn
 * @param {string} search
 */
function mockDebugModule(debugFn, search) {
  const output = /** @type {Pick<Console, 'debug'>} */ ({ debug: debugFn });
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (await vi.importActual('../../src/debug.js'));
    return {
      ...actual,
      createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => search, output })
    };
  });
  vi.resetModules();
}

/** @param {Record<string, unknown>[]} rows @param {string} availability */
function context(rows, availability = 'available') {
  const metadata = {
    'source-id': 'fixture', 'source-kind': 'fixture', 'as-of': '2026-10-20T11:13:27Z',
    'retrieved-at': '2026-10-20T11:13:27Z', completeness: 'complete', freshness: 'unknown', availability
  };
  return {
    pageId: 'value-report', viewId: 'debug-report', viewIndex: 0, title: 'Value report', sourceNames: names,
    routeParameters: {}, contextDetails: [], headingTag: 'h2',
    sources: Object.fromEntries(names.map((name, index) => [name, { source: name, metadata, rows: index === 0 ? rows : [] }]))
  };
}

const summary = { 'workflow-name': 'secret-workflow-name', goal: 'secret goal text' };

describe('workflow-value-report debug logging', () => {
  it('is silent by default', async () => {
    const debugFn = vi.fn();
    mockDebugModule(debugFn, '');
    const { renderWorkflowValueReport } = await import('../../src/components/workflow-value-report.js');
    renderWorkflowValueReport(/** @type {any} */ (context([], 'empty')));
    renderWorkflowValueReport(/** @type {any} */ (context([summary], 'unavailable')));
    expect(debugFn).not.toHaveBeenCalled();
  });

  it('logs empty and unavailable evidence under its predictable category without content', async () => {
    const debugFn = vi.fn();
    mockDebugModule(debugFn, '?debug=workflow-value-report');
    const { renderWorkflowValueReport } = await import('../../src/components/workflow-value-report.js');
    renderWorkflowValueReport(/** @type {any} */ (context([], 'empty')));
    renderWorkflowValueReport(/** @type {any} */ (context([summary], 'unavailable')));
    expect(debugFn).toHaveBeenCalledWith('[cao:workflow-value-report]', { event: 'evidence-empty', sources: names.length });
    expect(debugFn).toHaveBeenCalledWith('[cao:workflow-value-report]', { event: 'evidence-unavailable', sources: names.length });
    expect(JSON.stringify(debugFn.mock.calls)).not.toContain('secret');
  });

  it('is excluded by a negated category filter', async () => {
    const debugFn = vi.fn();
    mockDebugModule(debugFn, '?debug=*,-workflow-value-report');
    const { renderWorkflowValueReport } = await import('../../src/components/workflow-value-report.js');
    renderWorkflowValueReport(/** @type {any} */ (context([], 'empty')));
    expect(debugFn.mock.calls.some(([category]) => category === '[cao:workflow-value-report]')).toBe(false);
  });
});
