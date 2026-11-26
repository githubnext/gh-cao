// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import operations from '../../dashboard-fragments/operations.json' with { type: 'json' };
import contract from '../fixtures/maintenance-navigation-contract.json' with { type: 'json' };

const { requestInventoryRefresh, usesRemoteDataBackend, usesGitHubAuthentication, publishNotification } = vi.hoisted(() => ({
  requestInventoryRefresh: vi.fn(),
  usesRemoteDataBackend: vi.fn(() => true),
  usesGitHubAuthentication: vi.fn(() => true),
  publishNotification: vi.fn()
}));
vi.mock('../../src/remote-data-backend.js', () => ({ requestInventoryRefresh, usesRemoteDataBackend }));
vi.mock('../../src/auth.js', () => ({ usesGitHubAuthentication }));
vi.mock('../../src/notification-service.js', () => ({ publishNotification }));

import { renderInventorySync } from '../../src/components/inventory-sync.js';

function mount() {
  const root = renderInventorySync(/** @type {import('../../src/components/ui-elements.js').ElementRenderContext} */ ({}));
  document.body.append(root);
  const button = root.querySelector('button');
  if (!button) throw new Error('Sync button missing');
  return { root, button };
}

afterEach(() => {
  document.body.replaceChildren();
  vi.clearAllMocks();
  vi.unstubAllGlobals();
  usesRemoteDataBackend.mockReturnValue(true);
  usesGitHubAuthentication.mockReturnValue(true);
});

describe('declarative inventory sync', () => {
  it('places the same interaction-only element on all Updates pages without data queries', () => {
    for (const [index, id] of contract.navigation.pages.entries()) {
      const page = operations.pages.find((page) => page.id === id);
      expect(page?.views[0]).toMatchObject({
        id: contract['inventory-sync']['view-ids'][index],
        element: contract['inventory-sync'].element,
        data: { sources: contract['inventory-sync'].sources }
      });
    }
  });

  it.each(['static', 'unconfigured', 'unauthorized'])('is honestly unavailable for %s without sending a request', (reason) => {
    vi.stubGlobal('__INVENTORY_REFRESH__', reason !== 'unconfigured');
    vi.stubGlobal('__DASHBOARD_ADMIN__', reason !== 'unauthorized');
    usesRemoteDataBackend.mockReturnValue(reason !== 'static');
    const { root, button } = mount();
    expect(button.disabled).toBe(true);
    expect(root.textContent).toContain('Sync unavailable');
    button.click();
    expect(requestInventoryRefresh).not.toHaveBeenCalled();
  });

  it('allows the explicitly configured local owner without hosted admin privilege', async () => {
    vi.stubGlobal('__INVENTORY_REFRESH__', true);
    usesGitHubAuthentication.mockReturnValue(false);
    requestInventoryRefresh.mockResolvedValue({ queued: true });
    const { button } = mount();
    expect(button.disabled).toBe(false);
    expect(button.closest('section')?.textContent).toContain('Unsupported version evidence stays unavailable');
    button.click();
    await vi.waitFor(() => expect(requestInventoryRefresh).toHaveBeenCalledOnce());
  });

  it('disables duplicate clicks while busy and announces queued, not completed, inventory', async () => {
    vi.stubGlobal('__INVENTORY_REFRESH__', true);
    vi.stubGlobal('__DASHBOARD_ADMIN__', true);
    let finish = /** @type {(() => void) | undefined} */ (undefined);
    requestInventoryRefresh.mockImplementation(() => new Promise((resolve) => { finish = () => resolve({ queued: true }); }));
    const { root, button } = mount();
    button.click();
    await vi.waitFor(() => expect(button.disabled).toBe(true));
    expect(button.getAttribute('aria-label')).toBe(contract['inventory-sync'].label);
    expect(button.getAttribute('aria-busy')).toBe('true');
    button.click();
    expect(requestInventoryRefresh).toHaveBeenCalledOnce();
    finish?.();
    await vi.waitFor(() => expect(root.textContent).toContain('Inventory sync queued'));
    expect(root.textContent).toContain('when server collection completes');
    expect(button.disabled).toBe(false);
    expect(publishNotification).toHaveBeenCalledWith(expect.objectContaining({ message: 'Inventory sync queued' }));
  });

  it('shows accessible errors and permits an explicit retry', async () => {
    vi.stubGlobal('__INVENTORY_REFRESH__', true);
    vi.stubGlobal('__DASHBOARD_ADMIN__', true);
    requestInventoryRefresh.mockRejectedValueOnce(new Error('403')).mockResolvedValueOnce({ queued: true });
    const { root, button } = mount();
    button.click();
    await vi.waitFor(() => expect(root.querySelector('[role="alert"]')?.textContent).toContain('Unable to request inventory sync'));
    expect(button.disabled).toBe(false);
    button.click();
    await vi.waitFor(() => expect(root.textContent).toContain('Inventory sync queued'));
    expect(requestInventoryRefresh).toHaveBeenCalledTimes(2);
  });

  it('aborts on detachment and ignores late responses without notifications', async () => {
    vi.stubGlobal('__INVENTORY_REFRESH__', true);
    vi.stubGlobal('__DASHBOARD_ADMIN__', true);
    let finish = /** @type {(() => void) | undefined} */ (undefined);
    requestInventoryRefresh.mockImplementation(() => new Promise((resolve) => { finish = () => resolve({ queued: true }); }));
    const { root, button } = mount();
    button.click();
    const signal = requestInventoryRefresh.mock.lastCall?.[0];
    root.remove();
    await vi.waitFor(() => expect(signal?.aborted).toBe(true));
    finish?.();
    await Promise.resolve();
    expect(publishNotification).not.toHaveBeenCalled();
  });
});
