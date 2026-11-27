import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderInventorySync } from '../../src/components/inventory-sync.js';
import { syncRemoteInventory } from '../../src/remote-data-backend.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import maintenanceContract from '../fixtures/maintenance-navigation-contract.json' with { type: 'json' };

vi.mock('../../src/notification-service.js', () => ({ publishNotification: vi.fn() }));
import { publishNotification } from '../../src/notification-service.js';

const context = /** @type {import('../../src/components/ui-elements.js').ElementRenderContext} */ ({
  title: 'Sync inventory', pageId: 'maintenance', sourceNames: [], sources: {}, contextDetails: [], headingTag: 'h2'
});

function serverMode() {
  document.head.innerHTML = '<meta name="dashboard-data-backend" content="server-http">';
}
function render() {
  const root = renderInventorySync(context);
  document.body.append(root);
  return { root, button: /** @type {HTMLButtonElement} */ (root.querySelector('button')) };
}

afterEach(async () => {
  document.body.replaceChildren();
  await Promise.resolve();
  document.head.replaceChildren();
  document.cookie = 'cao_csrf=; Max-Age=0; Path=/';
  localStorage.clear();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe('inventory sync', () => {
  it('declares a query-free control on each Updates page', () => {
    for (const [index, id] of maintenanceContract.navigation.pages.entries()) {
      const page = authoritativeDashboard.dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === id);
      expect(page.views.find((/** @type {{ element?: string }} */ view) => view.element === 'inventory-sync')).toMatchObject({
        id: maintenanceContract['sync-controls'][index],
        mark: 'element', data: { sources: [] }, 'show-title': false, prompt: 'none'
      });
    }
  });

  it('disables static demo sync with an accessible explanation and no requests', async () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const { root, button } = render();
    expect(button.textContent).toBe('Sync');
    expect(button.disabled).toBe(true);
    expect(button.getAttribute('aria-describedby')).toBe(root.querySelector('[role="status"]')?.id);
    expect(root.textContent).toContain('static demo');
    button.click();
    await expect(syncRemoteInventory()).rejects.toThrow('configured dashboard server');
    expect(fetch).not.toHaveBeenCalled();
  });

  it('uses the local bearer API with no body and reports queued, never completed', async () => {
    serverMode();
    localStorage.setItem('cao-dashboard-access-token', 'test-capability');
    /** @type {(response: Response) => void} */
    let resolve = () => {};
    const fetch = vi.fn((/** @type {string} */ _url, /** @type {RequestInit} */ _init) => new Promise(r => { resolve = r; }));
    vi.stubGlobal('fetch', fetch);
    const { root, button } = render();
    button.focus();
    button.click();
    button.click();
    expect(button.getAttribute('aria-disabled')).toBe('true');
    expect(button.getAttribute('aria-busy')).toBe('true');
    expect(fetch).toHaveBeenCalledOnce();
    const [url, init] = fetch.mock.calls[0];
    expect(new URL(url).pathname).toBe('/api/admin/collection/sync');
    expect(init).toMatchObject({ method: 'POST', credentials: 'omit' });
    expect(/** @type {Record<string, string>} */ (init.headers).Authorization).toBe(['Bearer', localStorage.getItem('cao-dashboard-access-token')].join(' '));
    expect(init.body).toBeUndefined();
    resolve(new Response(JSON.stringify({ queued: true }), { status: 202 }));
    await vi.waitFor(() => expect(button.getAttribute('aria-disabled')).toBe('false'));
    expect(root.textContent).toContain('Inventory sync queued');
    expect(root.textContent).not.toContain('completed');
    expect(document.activeElement).toBe(button);
    expect(publishNotification).toHaveBeenCalledWith(expect.objectContaining({ tone: 'info', message: expect.stringContaining('queued') }));
  });

  it('uses hosted cookie and CSRF authentication rather than the bearer token', async () => {
    serverMode();
    document.head.insertAdjacentHTML('beforeend', '<meta name="cao-auth-mode" content="github">');
    document.cookie = 'cao_csrf=test-csrf; Path=/';
    localStorage.setItem('cao-dashboard-access-token', 'unused-capability');
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ queued: true }), { status: 202 }));
    vi.stubGlobal('fetch', fetch);
    await syncRemoteInventory();
    expect(fetch).toHaveBeenCalledOnce();
    expect(fetch.mock.calls[0][1]).toMatchObject({
      method: 'POST', credentials: 'same-origin', headers: { 'X-CSRF-Token': 'test-csrf' }
    });
    expect(fetch.mock.calls[0][1].headers.Authorization).toBeUndefined();
  });

  it.each([401, 403, 503])('reports %s honestly and permits retry', async (status) => {
    serverMode();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: 'unavailable' }), { status })));
    const { root, button } = render();
    button.click();
    await vi.waitFor(() => expect(publishNotification).toHaveBeenCalled());
    expect(button.disabled).toBe(false);
    expect(root.textContent).toContain('not confirmed');
    expect(publishNotification).toHaveBeenCalledWith(expect.objectContaining({ tone: 'error' }));
  });

  it('rejects malformed admission responses', async () => {
    serverMode();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ queued: false }), { status: 202 })));
    await expect(syncRemoteInventory()).rejects.toThrow('did not confirm');
  });

  it('aborts pending work on detachment without stale feedback', async () => {
    serverMode();
    /** @type {AbortSignal} */
    let signal;
    /** @type {(response: Response) => void} */
    let resolve = () => {};
    vi.stubGlobal('fetch', vi.fn((url, init) => {
      signal = init.signal;
      return new Promise(r => { resolve = r; });
    }));
    const { root, button } = render();
    await Promise.resolve();
    button.click();
    root.remove();
    await vi.waitFor(() => expect(signal.aborted).toBe(true));
    resolve(new Response(JSON.stringify({ queued: true }), { status: 202 }));
    await Promise.resolve();
    await Promise.resolve();
    expect(publishNotification).not.toHaveBeenCalled();
    expect(root.textContent).toContain('Requesting');
  });
});
