import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  dashboardPageAllSourceNames, dashboardPageSourceNames, dashboardPageLazySourceNames,
  dashboardTableSourceNames, resolveDashboardDocument, splitDashboardDocument
} from '../../src/dashboard-chunks.js';
import { dashboardPagePaginatedSourceBindings, disposeDashboard, renderDashboard } from '../../src/presenter.js';
import { pageCapabilityAvailable } from '../../src/view-availability.js';
import { validateDashboardDocument } from '../../src/validator.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import contract from '../fixtures/commands-view-contract.json' with { type: 'json' };
import governanceContract from '../fixtures/governance-contract.json' with { type: 'json' };

/** @type {HTMLElement[]} */
const roots = [];
const fallback = {
  id: 'home', kind: 'custom', title: 'Home', views: [{
    id: 'welcome', mark: 'callout', title: 'Welcome', description: 'Available everywhere.',
    callout: { label: 'Welcome', icon: 'info' }
  }]
};
const documentInput = {
  languageVersion: '0.1.0',
  dashboard: {
    ...authoritativeDashboard.dashboard,
    pages: [fallback, ...authoritativeDashboard.dashboard.pages.filter(
      (/** @type {{ id: string }} */ page) => ['commands', 'command', governanceContract.page].includes(page.id)
    )],
    navigation: [{ pages: ['home'] }, { label: 'Local', pages: ['commands'] },
      { label: 'Maintenance', pages: [governanceContract.page] }],
    callouts: undefined
  }
};

afterEach(() => {
  for (const root of roots.splice(0)) {
    disposeDashboard(root);
    root.remove();
  }
  document.head.querySelector('meta[name="dashboard-data-backend"]')?.remove();
  window.history.replaceState(null, '', '/');
  vi.unstubAllGlobals();
});

describe('page capability prerequisites', () => {
  it.each([
    ['published', undefined, false],
    ['local preview', undefined, false],
    ['native maintenance', undefined, true],
    ['hosted reader', undefined, true],
    ['local admin', true, true],
    ['hosted admin', true, false]
  ])('gates Governance navigation, routes and lazy loading for %s', async (_mode, admin, queue) => {
    vi.stubGlobal('__DASHBOARD_ADMIN__', admin);
    vi.stubGlobal('__CLI_QUEUE__', queue);
    vi.stubGlobal('__LOCAL_CLI_QUEUE__', queue);
    const { core } = splitDashboardDocument(documentInput);
    expect(core.dashboard.pages.find((page) => page.id === governanceContract.page)?.requires)
      .toEqual(governanceContract.requires);
    for (const input of [documentInput, resolveDashboardDocument(core, [])]) {
      const loader = Object.assign(vi.fn(async (_pageId) => ({})), {
        prepare: vi.fn(async (_pageId) => {}),
        subscribeViewSources: vi.fn(async () => ({}))
      });
      window.history.replaceState(null, '', '/?local-preview=admin#page-governance');
      const root = renderDashboard({ document: input, sources: {}, loadPageSources: loader });
      roots.push(root);
      document.body.append(root);
      await vi.waitFor(() => expect(root.querySelector('#page-title')?.textContent)
        .toBe(admin ? 'Governance' : 'Home'));
      expect(root.querySelectorAll('a[href="#page-governance"]')).toHaveLength(admin ? 2 : 0);
      expect(root.querySelector('[data-page-id="governance"]') !== null).toBe(admin === true);
      if (!admin) {
        expect(root.querySelector('.org-sidebar')?.textContent).not.toContain('Maintenance');
        expect(loader.mock.calls.every(([id]) => id === 'home')).toBe(true);
        expect(loader.prepare.mock.calls.every(([id]) => id === 'home')).toBe(true);
      }
      disposeDashboard(root);
      root.remove();
    }
  });

  it.each([false, 'true', 1, undefined])('requires an explicit boolean admin capability, not %j', (value) => {
    vi.stubGlobal('__DASHBOARD_ADMIN__', value);
    vi.stubGlobal('__CLI_QUEUE__', true);
    expect(pageCapabilityAvailable({ requires: governanceContract.requires })).toBe(false);
  });

  it('preserves requirements and all query indexes when splitting without a local capability', () => {
    const { core, pageChunks } = splitDashboardDocument(documentInput);
    const stub = core.dashboard.pages.find((page) => page.id === 'commands');
    expect(stub?.requires).toEqual(contract.requires);
    expect(stub?.['source-names']).toContain(contract.query);
    for (const input of [documentInput, resolveDashboardDocument(core, []),
      resolveDashboardDocument(core, pageChunks.values())]) {
      expect(dashboardPageSourceNames(input, 'commands')).toEqual([]);
      expect(dashboardPageAllSourceNames(input, 'commands')).toEqual([]);
      expect(dashboardPageLazySourceNames(input, 'commands')).toEqual([]);
      expect(dashboardTableSourceNames(input, 'commands')).toEqual([]);
      expect(dashboardPagePaginatedSourceBindings(input, 'commands')).toEqual({});
      vi.stubGlobal('__CLI_QUEUE__', true);
      expect(dashboardPageSourceNames(input, 'commands')).toContain(contract.query);
      expect(dashboardPageLazySourceNames(input, 'commands')).toContain(contract.query);
      expect(dashboardTableSourceNames(input, 'commands')).toContain(contract.query);
      vi.stubGlobal('__CLI_QUEUE__', false);
    }
  });

  it.each(['static', 'hosted'])('hides local pages and rejects direct routes on %s backends', async (backend) => {
    if (backend === 'hosted') {
      const marker = document.createElement('meta');
      marker.name = 'dashboard-data-backend';
      marker.content = 'server-http';
      document.head.append(marker);
    }
    const { core } = splitDashboardDocument(documentInput);
    for (const input of [documentInput, resolveDashboardDocument(core, [])]) {
      window.history.replaceState(null, '', '/#page-commands');
      const loader = Object.assign(vi.fn(async (_pageId) => ({})), {
        prepare: vi.fn(async (_pageId) => {}),
        subscribeViewSources: vi.fn(async () => ({}))
      });
      const root = renderDashboard({ document: input, sources: {}, loadPageSources: loader });
      roots.push(root);
      document.body.append(root);
      await vi.waitFor(() => expect(root.querySelector('#page-title')?.textContent).toBe('Home'));
      expect(root.querySelector('a[href="#page-commands"]')).toBeNull();
      expect(root.querySelector('[data-page-id="commands"]')).toBeNull();
      expect(root.querySelector('[data-page-id="command"]')).toBeNull();
      expect(root.querySelector('.org-sidebar')?.textContent).not.toContain('Local');
      window.location.hash = 'page-command?command=123';
      await new Promise((resolve) => setTimeout(resolve, 0));
      expect(root.querySelector('#page-title')?.textContent).toBe('Home');
      expect(loader.mock.calls.every(([id]) => id === 'home')).toBe(true);
      expect(loader.prepare.mock.calls.every(([id]) => id === 'home')).toBe(true);
      expect(loader.subscribeViewSources).not.toHaveBeenCalled();
      disposeDashboard(root);
      root.remove();
    }
  });

  it('shows desktop and mobile navigation when the queue transport is enabled', () => {
    vi.stubGlobal('__CLI_QUEUE__', true);
    window.history.replaceState(null, '', '/#page-home');
    const root = renderDashboard({ document: documentInput, sources: {} });
    roots.push(root);
    document.body.append(root);
    expect(root.querySelectorAll('a[href="#page-commands"]')).toHaveLength(2);
    expect(root.querySelector('[data-page-id="commands"]')).not.toBeNull();
    expect(root.querySelector('[data-page-id="command"]')).not.toBeNull();
  });

  it.each([false, 'true', 1, undefined])('requires an explicit boolean transport capability, not %j', (value) => {
    vi.stubGlobal('__CLI_QUEUE__', value);
    expect(pageCapabilityAvailable({ requires: contract.requires })).toBe(false);
    expect(pageCapabilityAvailable({})).toBe(true);
  });

  it.each([null, {}, { capability: 'unknown' }, { capability: 'cli-queue', authority: 'admin' }])(
    'rejects invalid or authority-widening page prerequisites %j', (requires) => {
      const source = {
        'language-version': '0.1.0',
        dashboard: {
          ...authoritativeDashboard.dashboard,
          pages: authoritativeDashboard.dashboard.pages.map((/** @type {{ id: string }} */ page) =>
            page.id === 'commands' ? { ...page, requires } : page)
        }
      };
      expect(validateDashboardDocument(JSON.stringify(source)).ok).toBe(false);
    }
  );

  it('validates the declared page capability', () => {
    const result = validateDashboardDocument(JSON.stringify({
      'language-version': '0.1.0',
      dashboard: authoritativeDashboard.dashboard
    }));
    expect(result.errors).toEqual([]);
    expect(result.ok).toBe(true);
  });
});
