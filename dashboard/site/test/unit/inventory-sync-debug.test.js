// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('../../src/notification-service.js', () => ({ publishNotification: vi.fn() }));

/** @param {string} search @param {{ debug: import('vitest').Mock }} output */
async function loadWithDebug(search, output) {
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (await vi.importActual('../../src/debug.js'));
    return {
      ...actual,
      createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => search, output })
    };
  });
  vi.resetModules();
  return import('../../src/components/inventory-sync.js');
}

/** @param {typeof import('../../src/components/inventory-sync.js').renderInventorySync} renderInventorySync @param {Response | Error} result */
async function click(renderInventorySync, result) {
  document.head.innerHTML = '<meta name="dashboard-data-backend" content="server-http">';
  localStorage.setItem('cao-dashboard-access-token', 'secret-capability');
  vi.stubGlobal('fetch', result instanceof Error ? vi.fn().mockRejectedValue(result) : vi.fn().mockResolvedValue(result));
  const root = renderInventorySync({ title: 'Sync', pageId: 'maintenance', sourceNames: [], sources: {}, contextDetails: [], headingTag: 'h2' });
  document.body.append(root);
  const button = /** @type {HTMLButtonElement} */ (root.querySelector('button'));
  button.click();
  await vi.waitFor(() => expect(button.getAttribute('aria-busy')).toBe('false'));
}

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  document.body.replaceChildren();
  document.head.replaceChildren();
  localStorage.clear();
});

describe('inventory-sync debug logging', () => {
  it('is silent by default', async () => {
    const output = { debug: vi.fn() };
    const { renderInventorySync } = await loadWithDebug('', output);
    await click(renderInventorySync, new Response(JSON.stringify({ queued: true }), { status: 202 }));
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('is silent when another category is selected', async () => {
    const output = { debug: vi.fn() };
    const { renderInventorySync } = await loadWithDebug('?debug=data', output);
    await click(renderInventorySync, new Response(JSON.stringify({ queued: true }), { status: 202 }));
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs requested and queued under the filename category', async () => {
    const output = { debug: vi.fn() };
    const { renderInventorySync } = await loadWithDebug('?debug=inventory-sync', output);
    await click(renderInventorySync, new Response(JSON.stringify({ queued: true }), { status: 202 }));
    expect(output.debug.mock.calls).toEqual([
      ['[cao:inventory-sync]', { event: 'requested' }],
      ['[cao:inventory-sync]', { event: 'queued' }]
    ]);
  });

  it('logs only a sanitized error name on failure, never messages or credentials', async () => {
    const output = { debug: vi.fn() };
    const { renderInventorySync } = await loadWithDebug('?debug=*', output);
    await click(renderInventorySync, new TypeError('https://example.test/api?token=secret-capability'));
    const own = output.debug.mock.calls.filter(([prefix]) => prefix === '[cao:inventory-sync]');
    expect(own).toEqual([
      ['[cao:inventory-sync]', { event: 'requested' }],
      ['[cao:inventory-sync]', { event: 'failed', errorName: 'TypeError' }]
    ]);
    expect(JSON.stringify(own)).not.toMatch(/secret|example\.test/);
  });
});
