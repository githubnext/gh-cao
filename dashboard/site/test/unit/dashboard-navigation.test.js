// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

/** Builds the minimal DOM structure enableDashboardNavigation expects. */
function buildNavigationRoot() {
  const root = document.createElement('div');
  root.innerHTML = `
    <div class="app-shell">
      <button class="sidebar-toggle" type="button"></button>
    </div>
    <details class="nav-section" data-nav-section="Data"></details>
  `;
  document.body.append(root);
  return root;
}

afterEach(() => {
  document.body.innerHTML = '';
  window.history.replaceState({}, '', '/');
  vi.restoreAllMocks();
  localStorage.clear();
  vi.resetModules();
});

describe('dashboard sidebar collapse reactive state', () => {
  it('shows the offline Octicon in the navigation menu and updates on connection changes', async () => {
    const { renderDashboardNavigation } = await import('../../src/components/dashboard-navigation.js');
    const root = renderDashboardNavigation([{ id: 'overview', title: 'Overview' }], 'Dashboard', undefined);
    document.body.append(root);
    const statuses = root.querySelectorAll('.dashboard-offline-status');
    expect([...statuses].every((status) => status.hasAttribute('hidden'))).toBe(true);
    window.dispatchEvent(new Event('offline'));
    expect([...statuses].every((status) => !status.hasAttribute('hidden'))).toBe(true);
    expect(root.querySelector('.mobile-nav-menu .octicon-cloud-offline')).not.toBeNull();
    window.dispatchEvent(new Event('online'));
    expect([...statuses].every((status) => status.hasAttribute('hidden'))).toBe(true);
  });
  it('applies the initial collapsed state from storage and syncs the toggle button', async () => {
    localStorage.setItem('central-agentic-ops.dashboard.sidebar-collapsed', 'true');
    const { enableDashboardNavigation } = await import('../../src/components/dashboard-navigation.js');
    const root = buildNavigationRoot();
    enableDashboardNavigation(root);

    const appShell = root.querySelector('.app-shell');
    const toggle = root.querySelector('.sidebar-toggle');
    expect(appShell?.classList.contains('sidebar-collapsed')).toBe(true);
    expect(toggle?.getAttribute('aria-expanded')).toBe('false');
    expect(toggle?.getAttribute('aria-label')).toBe('Expand navigation');
  });

  it('toggles collapsed state, syncs the button, and persists the new value on click', async () => {
    const { enableDashboardNavigation } = await import('../../src/components/dashboard-navigation.js');
    const root = buildNavigationRoot();
    enableDashboardNavigation(root);

    const appShell = root.querySelector('.app-shell');
    const toggle = /** @type {HTMLButtonElement} */ (root.querySelector('.sidebar-toggle'));
    expect(appShell?.classList.contains('sidebar-collapsed')).toBe(false);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');

    toggle.click();

    expect(appShell?.classList.contains('sidebar-collapsed')).toBe(true);
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(toggle.getAttribute('aria-label')).toBe('Expand navigation');
    expect(localStorage.getItem('central-agentic-ops.dashboard.sidebar-collapsed')).toBe('true');

    toggle.click();

    expect(appShell?.classList.contains('sidebar-collapsed')).toBe(false);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    expect(toggle.getAttribute('aria-label')).toBe('Collapse navigation');
    expect(localStorage.getItem('central-agentic-ops.dashboard.sidebar-collapsed')).toBe('false');
  });
});

describe('dashboard navigation debug logging', () => {
  it('stays silent without a matching debug category', async () => {
    const debug = vi.spyOn(console, 'debug').mockImplementation(() => {});
    const storageError = new Error('denied');
    storageError.name = 'SecurityError';
    vi.spyOn(window, 'localStorage', 'get').mockImplementation(() => {
      throw storageError;
    });

    const { enableDashboardNavigation } = await import('../../src/components/dashboard-navigation.js');
    const root = buildNavigationRoot();
    enableDashboardNavigation(root);

    expect(debug).not.toHaveBeenCalled();
  });

  it('logs a sanitized error name when reading sidebar state fails', async () => {
    window.history.replaceState({}, '', '?debug=dashboard-navigation');
    const debug = vi.spyOn(console, 'debug').mockImplementation(() => {});
    const storageError = new Error('storage disabled in this context');
    storageError.name = 'SecurityError';
    vi.spyOn(window, 'localStorage', 'get').mockImplementation(() => {
      throw storageError;
    });

    const { enableDashboardNavigation } = await import('../../src/components/dashboard-navigation.js');
    const root = buildNavigationRoot();
    enableDashboardNavigation(root);

    expect(debug).toHaveBeenCalledWith(
      '[cao:dashboard-navigation]',
      { event: 'sidebar-state.read_failed', errorName: 'SecurityError' }
    );
    expect(debug).not.toHaveBeenCalledWith(
      '[cao:dashboard-navigation]',
      expect.objectContaining({ message: expect.anything() })
    );
  });

  it('logs a sanitized error name when persisting sidebar state fails', async () => {
    window.history.replaceState({}, '', '?debug=dashboard-navigation');
    const debug = vi.spyOn(console, 'debug').mockImplementation(() => {});
    const writeError = new Error('quota exceeded');
    writeError.name = 'QuotaExceededError';
    const fakeStorage = {
      getItem: () => null,
      setItem: () => {
        throw writeError;
      }
    };
    vi.spyOn(window, 'localStorage', 'get').mockReturnValue(/** @type {Storage} */ (/** @type {unknown} */ (fakeStorage)));

    const { enableDashboardNavigation } = await import('../../src/components/dashboard-navigation.js');
    const root = buildNavigationRoot();
    enableDashboardNavigation(root);
    const toggle = root.querySelector('.sidebar-toggle');
    expect(toggle).toBeInstanceOf(HTMLButtonElement);
    if (!(toggle instanceof HTMLButtonElement)) throw new Error('sidebar toggle was not rendered');
    toggle.click();

    expect(debug).toHaveBeenCalledWith(
      '[cao:dashboard-navigation]',
      { event: 'sidebar-state.write_failed', errorName: 'QuotaExceededError' }
    );
  });

  it('logs a sanitized error name when reading persisted nav-section state fails', async () => {
    window.history.replaceState({}, '', '?debug=dashboard-navigation');
    const debug = vi.spyOn(console, 'debug').mockImplementation(() => {});
    const readError = new Error('storage disabled');
    readError.name = 'SecurityError';
    const fakeStorage = {
      getItem: () => {
        throw readError;
      },
      setItem: () => {}
    };
    vi.spyOn(window, 'localStorage', 'get').mockReturnValue(/** @type {Storage} */ (/** @type {unknown} */ (fakeStorage)));

    const { enableDashboardNavigation } = await import('../../src/components/dashboard-navigation.js');
    const root = buildNavigationRoot();
    enableDashboardNavigation(root);

    expect(debug).toHaveBeenCalledWith(
      '[cao:dashboard-navigation]',
      { event: 'nav-section-state.read_failed', errorName: 'SecurityError' }
    );
  });

  it('logs only a sanitized error name when persisting nav-section state fails', async () => {
    window.history.replaceState({}, '', '?debug=dashboard-navigation');
    const debug = vi.spyOn(console, 'debug').mockImplementation(() => {});
    const writeError = new Error('quota exceeded for do-not-log-this-value');
    writeError.name = 'QuotaExceededError';
    const fakeStorage = {
      getItem: () => null,
      setItem: (/** @type {string} */ key) => {
        if (key.includes('nav-sections-open')) throw writeError;
      }
    };
    vi.spyOn(window, 'localStorage', 'get').mockReturnValue(/** @type {Storage} */ (/** @type {unknown} */ (fakeStorage)));

    const { enableDashboardNavigation } = await import('../../src/components/dashboard-navigation.js');
    const root = buildNavigationRoot();
    enableDashboardNavigation(root);
    root.querySelector('.nav-section')?.dispatchEvent(new Event('toggle'));

    expect(debug).toHaveBeenCalledWith(
      '[cao:dashboard-navigation]',
      { event: 'nav-section-state.write_failed', errorName: 'QuotaExceededError' }
    );
    expect(JSON.stringify(debug.mock.calls)).not.toContain('do-not-log-this-value');
  });

  it('stays silent when persisting nav-section state fails without a matching debug category', async () => {
    const debug = vi.spyOn(console, 'debug').mockImplementation(() => {});
    const fakeStorage = {
      getItem: () => null,
      setItem: () => {
        throw new Error('quota exceeded');
      }
    };
    vi.spyOn(window, 'localStorage', 'get').mockReturnValue(/** @type {Storage} */ (/** @type {unknown} */ (fakeStorage)));

    const { enableDashboardNavigation } = await import('../../src/components/dashboard-navigation.js');
    const root = buildNavigationRoot();
    enableDashboardNavigation(root);
    root.querySelector('.nav-section')?.dispatchEvent(new Event('toggle'));

    expect(debug).not.toHaveBeenCalled();
  });
});
