// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

/** @param {string} search */
async function loadWithDebug(search) {
  const output = { debug: vi.fn() };
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (
      await vi.importActual('../../src/debug.js')
    );
    return {
      ...actual,
      createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => search, output })
    };
  });
  vi.resetModules();
  const module = await import('../../src/deferred-styles.js');
  return { ...module, output };
}

function createBuiltDocument() {
  const document = window.document.implementation.createHTMLDocument('dashboard');
  const styles = document.createElement('style');
  styles.dataset.dashboardStyles = '';
  styles.dataset.deferredHref = './private/styles-deferred.css?token=secret';
  document.head.append(styles);
  return document;
}

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('deferred-styles debug logging', () => {
  it('is disabled by default', async () => {
    const { requestDeferredStyles, output } = await loadWithDebug('');
    const document = createBuiltDocument();
    requestDeferredStyles(document, 'rendered');
    document.querySelector('link[data-dashboard-deferred-styles]')?.dispatchEvent(new Event('load'));
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs request and load outcome under the deferred-styles category', async () => {
    const { requestDeferredStyles, output } = await loadWithDebug('?debug=deferred-styles');
    const document = createBuiltDocument();
    requestDeferredStyles(document, 'rendered');
    document.querySelector('link[data-dashboard-deferred-styles]')?.dispatchEvent(new Event('load'));
    expect(output.debug.mock.calls).toEqual([
      ['[cao:deferred-styles]', { event: 'deferred-styles-requested', reason: 'rendered' }],
      ['[cao:deferred-styles]', { event: 'deferred-styles-settled', status: 'loaded' }]
    ]);
    expect(document.documentElement.dataset.deferredStyles).toBeUndefined();
  });

  it('logs a failed sheet and never includes its URL', async () => {
    const { requestDeferredStyles, output } = await loadWithDebug('?debug=deferred-*');
    const document = createBuiltDocument();
    requestDeferredStyles(document, 'idle');
    document.querySelector('link[data-dashboard-deferred-styles]')?.dispatchEvent(new Event('error'));
    expect(output.debug.mock.calls.map(([, payload]) => payload.status ?? payload.reason)).toEqual(['idle', 'error']);
    expect(JSON.stringify(output.debug.mock.calls)).not.toMatch(/secret|styles-deferred/);
  });

  it('does not log when the category is excluded or the page has no deferred sheet', async () => {
    const excluded = await loadWithDebug('?debug=*,-deferred-styles');
    excluded.requestDeferredStyles(createBuiltDocument(), 'rendered');
    expect(excluded.output.debug).not.toHaveBeenCalled();

    const unbuilt = await loadWithDebug('?debug=1');
    unbuilt.requestDeferredStyles(window.document.implementation.createHTMLDocument('dashboard'));
    expect(unbuilt.output.debug).not.toHaveBeenCalled();
  });
});
