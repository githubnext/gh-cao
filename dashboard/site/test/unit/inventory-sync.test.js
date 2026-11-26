import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderInventorySync } from '../../src/components/inventory-sync.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import { renderUiElement } from '../../src/components/ui-elements.js';
import { validateDashboardDocument } from '../../src/validator.js';

const request = vi.hoisted(() => vi.fn());
const notify = vi.hoisted(() => vi.fn());
vi.mock('../../src/remote-data-backend.js', () => ({ requestInventoryRefresh: request }));
vi.mock('../../src/notification-service.js', () => ({ publishNotification: notify }));
const context = {
  pageId: 'maintenance', title: 'Sync information', sourceNames: [], sources: {},
  contextDetails: [], headingTag: /** @type {const} */ ('h2')
};

function mount(remote = true) {
  if (remote) {
    const meta = document.createElement('meta');
    meta.name = 'dashboard-data-backend';
    meta.content = 'server-http';
    document.head.append(meta);
  }
  const root = renderInventorySync(context);
  document.body.append(root);
  return root;
}

afterEach(() => {
  document.body.replaceChildren();
  document.head.querySelectorAll('meta[name="dashboard-data-backend"]').forEach((element) => element.remove());
  vi.clearAllMocks();
});

describe('inventory Sync', () => {
  it('validates and renders the named source-free interaction without permitting empty data elements', () => {
    const document = structuredClone(authoritativeDashboard);
    expect(validateDashboardDocument(JSON.stringify(document)).ok).toBe(true);
    expect(renderUiElement('inventory-sync', context)?.querySelector('button')?.textContent).toBe('Sync');
    const page = document.dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === 'maintenance');
    const view = page.views.find((/** @type {{ element?: string }} */ view) => view.element === 'inventory-sync');
    view.element = 'markdown';
    expect(validateDashboardDocument(JSON.stringify(document)).ok).toBe(false);
  });

  it('declares a source-free task interaction on every Updates page', () => {
    for (const id of ['maintenance', 'maintenance-workflows', 'maintenance-campaigns']) {
      const page = authoritativeDashboard.dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === id);
      const sync = page?.views.find((/** @type {{ element?: string }} */ view) => view.element === 'inventory-sync');
      expect(sync?.data).toEqual({ sources: [] });
      expect(sync?.objective).toContain('cannot submit server tasks');
    }
  });

  it('disables the synthetic demo without making a server request', () => {
    const root = mount(false);
    const button = root.querySelector('button');
    expect(button?.disabled).toBe(true);
    button?.click();
    expect(request).not.toHaveBeenCalled();
    expect(root.textContent).toContain('unavailable in the synthetic demo');
  });

  it.each(['inventory', 'local-collection'])('reports accepted %s work without claiming completion', async (mode) => {
    request.mockResolvedValue({ requested: true, mode });
    const root = mount();
    root.querySelector('button')?.click();
    await vi.waitFor(() => expect(root.textContent).toContain('Information will update'));
    expect(request).toHaveBeenCalledOnce();
    expect(root.querySelector('button')?.disabled).toBe(false);
    expect(notify).toHaveBeenCalledWith(expect.objectContaining({ tone: 'info' }));
  });

  it('prevents duplicate clicks and permits retry after failure', async () => {
    /** @type {(error: Error) => void} */
    let reject = () => { throw new Error('Request not started'); };
    request.mockImplementation(() => new Promise((_, fail) => { reject = fail; }));
    const root = mount();
    const button = root.querySelector('button');
    button?.click();
    button?.click();
    expect(request).toHaveBeenCalledOnce();
    expect(button?.getAttribute('aria-busy')).toBe('true');
    reject(new Error('unavailable'));
    await vi.waitFor(() => expect(root.textContent).toContain('could not be confirmed'));
    expect(button?.disabled).toBe(false);
    expect(notify).toHaveBeenCalledWith(expect.objectContaining({ tone: 'error' }));
  });

  it('aborts requests and suppresses feedback when detached', async () => {
    /** @type {(value: { requested: boolean, mode: string }) => void} */
    let finish = () => { throw new Error('Request not started'); };
    request.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    const root = mount();
    await Promise.resolve();
    root.querySelector('button')?.click();
    const signal = request.mock.calls[0][0];
    root.remove();
    await vi.waitFor(() => expect(signal.aborted).toBe(true));
    finish({ requested: true, mode: 'inventory' });
    await Promise.resolve();
    expect(notify).not.toHaveBeenCalled();
  });
});
