// @vitest-environment jsdom
import { beforeEach, describe, expect, it } from 'vitest';
import { renderDashboardHeader } from '../../src/components/dashboard-header.js';
import { enableDashboardNavigation, renderDashboardNavigation, syncDashboardNavigationIndicators, syncMobileViewModeToggle } from '../../src/components/dashboard-navigation.js';
import { buildChartPoints, prepareChartPoints, prepareTableRows } from '../../src/components/view-data.js';
import { renderPageTabs } from '../../src/components/route-tabs.js';

describe('dashboard sidebar', () => {
  beforeEach(() => {
    document.body.replaceChildren();
    localStorage.clear();
  });

  it('keeps route tab names independent from the parent navigation indicator', () => {
    const pages = [{
      id: 'maintenance', title: 'Updates',
      'navigation-indicator': { label: 'updates available', any: ['updates'] }
    }];
    const root = document.createElement('div');
    const sidebar = renderDashboardNavigation(pages, 'Example', undefined);
    const tabs = renderPageTabs({
      currentTab: 'repositories',
      tabs: [{ id: 'repositories', label: 'Repositories', icon: 'repo', page: 'maintenance' }]
    });
    root.append(sidebar, tabs);
    for (const rows of [[], [{ available: true }]]) {
      syncDashboardNavigationIndicators(root, pages, { updates: { rows } });
      const tab = tabs.querySelector('a');
      expect(tab?.textContent).toBe('Repositories');
      expect(tab?.getAttribute('aria-label')).toBeNull();
      expect(tab?.getAttribute('title')).toBeNull();
      expect(sidebar.querySelector('[data-nav-page-id="maintenance"]')?.getAttribute('aria-label'))
        .toBe(rows.length ? 'Updates, updates available' : 'Updates');
    }
  });

  it('owns its navigation markup and collapse interaction', () => {
    const sidebar = renderDashboardNavigation([
      { id: 'overview', title: 'Overview', icon: 'home' },
      { id: 'runs', title: 'Runs', icon: 'play' }
    ], 'Example', [{ label: 'Main', pages: ['overview', 'runs'] }]);
    const shell = document.createElement('div');
    shell.className = 'app-shell';
    shell.append(sidebar);
    const root = document.createElement('div');
    root.append(shell);
    document.body.append(root);

    enableDashboardNavigation(root);

    expect(sidebar.dataset.defaultPageId).toBe('overview');
    expect(sidebar.querySelectorAll('[data-nav-page-id]')).toHaveLength(2);
    const backIcon = sidebar.querySelector('.mobile-history-back .octicon');
    expect(backIcon).toBeInstanceOf(SVGElement);
    if (!(backIcon instanceof SVGElement)) return;
    expect(backIcon.classList.contains('octicon-chevron-left')).toBe(true);
    expect(backIcon.classList.contains('mobile-history-back-icon')).toBe(true);
    const toggle = sidebar.querySelector('.sidebar-toggle');
    expect(toggle).toBeInstanceOf(HTMLButtonElement);
    if (!(toggle instanceof HTMLButtonElement)) return;
    toggle.click();
    expect(shell.classList.contains('sidebar-collapsed')).toBe(true);
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
  });

  it('remembers which navigation sections are expanded', () => {
    const renderSidebar = () => {
      document.body.replaceChildren();
      const sidebar = renderDashboardNavigation([
        { id: 'overview', title: 'Overview' },
        { id: 'runs', title: 'Runs' }
      ], 'Example', [
        { label: 'Main', pages: ['overview'] },
        { label: 'Data', pages: ['runs'] }
      ]);
      const shell = document.createElement('div');
      shell.className = 'app-shell';
      shell.append(sidebar);
      const root = document.createElement('div');
      root.append(shell);
      document.body.append(root);
      enableDashboardNavigation(root);
      return sidebar;
    };

    const sidebar = renderSidebar();
    const dataSection = sidebar.querySelector('[data-nav-section="Data"]');
    expect(dataSection).toBeInstanceOf(HTMLDetailsElement);
    if (!(dataSection instanceof HTMLDetailsElement)) return;
    expect(dataSection.open).toBe(false);

    dataSection.open = true;
    dataSection.dispatchEvent(new Event('toggle'));

    const restored = renderSidebar().querySelector('[data-nav-section="Data"]');
    expect(restored instanceof HTMLDetailsElement && restored.open).toBe(true);
    const mainSection = document.querySelector('[data-nav-section="Main"]');
    expect(mainSection instanceof HTMLDetailsElement && mainSection.open).toBe(true);
  });

  it('ignores malformed stored navigation section state', () => {
    localStorage.setItem('central-agentic-ops.dashboard.nav-sections-open', '{ not json');
    const sidebar = renderDashboardNavigation([
      { id: 'overview', title: 'Overview' },
      { id: 'runs', title: 'Runs' }
    ], 'Example', [
      { label: 'Main', pages: ['overview'] },
      { label: 'Data', pages: ['runs'] }
    ]);
    const shell = document.createElement('div');
    shell.className = 'app-shell';
    shell.append(sidebar);
    const root = document.createElement('div');
    root.append(shell);
    document.body.append(root);

    enableDashboardNavigation(root);

    const dataSection = sidebar.querySelector('[data-nav-section="Data"]');
    expect(dataSection instanceof HTMLDetailsElement && dataSection.open).toBe(false);
  });

  it('places the hosted account control last in the top-right header actions', () => {
    const accountMenu = document.createElement('details');
    accountMenu.className = 'account-menu';
    const header = renderDashboardHeader({
      title: 'Overview',
      overviewPageHref: '#page-overview',
      dashboardHorizon: document.createElement('div'),
      accountMenu,
      githubUrlBase: 'https://github.com',
      dashboardRepository: 'octo/example'
    });

    const actions = header.querySelector('.report-actions');
    expect(actions?.lastElementChild).toBe(accountMenu);
    expect(header.querySelector('.overview-header + .report-actions > .account-menu')).toBe(accountMenu);
  });

  it('groups experimental pages in one explicit section', () => {
    const sidebar = renderDashboardNavigation([
      { id: 'overview', title: 'Overview' },
      { id: 'preview-a', title: 'Preview A' },
      { id: 'preview-b', title: 'Preview B' }
    ], 'Example', [
      { label: 'Main', pages: ['overview'] },
      { label: 'Alpha', pages: ['preview-a'], experimental: true },
      { label: 'Beta', pages: ['preview-b'], experimental: true }
    ]);

    const labels = [...sidebar.querySelectorAll('.nav-section-label')].map((element) => element.textContent);
    expect(labels).toEqual(['Main', 'Experimental']);
    const experimentalSection = sidebar.querySelector('[data-nav-section="Experimental"]');
    expect(experimentalSection).toBeInstanceOf(HTMLDetailsElement);
    expect(/** @type {HTMLDetailsElement} */ (experimentalSection).open).toBe(false);
  });

  it('labels experimental pages in desktop and mobile navigation', () => {
    const sidebar = renderDashboardNavigation([
      { id: 'overview', title: 'Overview' },
      { id: 'preview', title: 'Preview', experimental: true }
    ], 'Example', [{ label: 'Main', pages: ['overview', 'preview'] }]);

    const desktopLink = sidebar.querySelector('[data-nav-page-id="preview"]');
    const mobileLink = sidebar.querySelector('[data-mobile-nav-page-id="preview"]');
    expect(desktopLink?.querySelector('.experimental-page-label .octicon-beaker')).not.toBeNull();
    expect(mobileLink?.querySelector('.experimental-page-label .octicon-beaker')).not.toBeNull();
    expect(desktopLink?.querySelector('.experimental-page-label')?.getAttribute('title')).toBe('Experimental');
    expect(mobileLink?.querySelector('.experimental-page-label')?.getAttribute('title')).toBe('Experimental');
    expect(desktopLink?.getAttribute('aria-label')).toBe('Preview, Experimental');
    expect(mobileLink?.getAttribute('aria-label')).toBe('Preview, Experimental');
  });

  it('anchors bottom navigation sections after the standard sections', () => {
    const sidebar = renderDashboardNavigation([
      { id: 'overview', title: 'Overview' },
      { id: 'runs', title: 'Runs' },
      { id: 'maintenance', title: 'Updates' },
      { id: 'configuration', title: 'Settings' }
    ], 'Example', [
      { label: 'Main', pages: ['overview'] },
      { label: 'Data', pages: ['runs'] },
      { label: 'Manage', placement: 'bottom', pages: ['maintenance', 'configuration'] }
    ]);

    const sectionLabels = [...sidebar.querySelectorAll('.nav-section-label')].map((element) => element.textContent);
    expect(sectionLabels).toEqual(['Main', 'Data', 'Manage']);
    const bottomSection = sidebar.querySelector('.nav-section-bottom');
    expect(bottomSection?.querySelector('.nav-section-label')?.textContent).toBe('Manage');
    expect([...bottomSection?.querySelectorAll('[data-nav-page-id]') ?? []].map((element) => element.textContent))
      .toEqual(['Updates', 'Settings']);
    expect(bottomSection?.hasAttribute('open')).toBe(true);
    expect(bottomSection?.classList.contains('nav-section-bottom')).toBe(true);
  });

  it('marks navigation items when their declared indicator source matches', () => {
    const pages = [
      { id: 'overview', title: 'Overview', icon: 'home' },
      {
        id: 'maintenance',
        title: 'Updates',
        icon: 'tools',
        'navigation-indicator': {
          label: 'updates available',
          any: [
            'campaigns',
            'maintenance-repositories'
          ]
        }
      },
      {
        id: 'reports',
        title: 'Reports',
        icon: 'graph',
        'navigation-indicator': {
          label: 'reports need review',
          any: ['reports-needing-review']
        }
      }
    ];
    const sidebar = renderDashboardNavigation(pages, 'Example', [
      { label: 'Main', pages: ['overview'] },
      { label: 'Updates', placement: 'bottom', pages: ['maintenance', 'reports'] }
    ]);

    const updatesLink = sidebar.querySelector('[data-nav-page-id="maintenance"]');
    const mobileUpdatesLink = sidebar.querySelector('[data-mobile-nav-page-id="maintenance"]');
    const mobileMenuSummary = sidebar.querySelector('.mobile-nav-menu > summary');
    expect(updatesLink?.querySelector('[data-nav-indicator]')?.hasAttribute('hidden')).toBe(true);
    expect(mobileMenuSummary?.querySelector('[data-mobile-nav-menu-indicator]')?.hasAttribute('hidden')).toBe(true);

    syncDashboardNavigationIndicators(sidebar, pages, {
      campaigns: {
        rows: [{ 'campaign-update-state': 'update-available' }]
      }
    });

    expect(updatesLink?.querySelector('[data-nav-indicator]')?.hasAttribute('hidden')).toBe(false);
    expect(updatesLink?.getAttribute('aria-label')).toBe('Updates, updates available');
    expect(mobileUpdatesLink?.querySelector('[data-nav-indicator]')?.hasAttribute('hidden')).toBe(false);
    expect(mobileUpdatesLink?.getAttribute('aria-label')).toBe('Updates, updates available');
    expect(mobileMenuSummary?.querySelector('[data-mobile-nav-menu-indicator]')?.hasAttribute('hidden')).toBe(false);
    expect(mobileMenuSummary?.getAttribute('aria-label')).toBe('Select view, updates available');

    syncDashboardNavigationIndicators(sidebar, pages, {
      campaigns: {
        rows: []
      }
    });

    expect(updatesLink?.querySelector('[data-nav-indicator]')?.hasAttribute('hidden')).toBe(true);
    expect(updatesLink?.getAttribute('aria-label')).toBe('Updates');
    expect(mobileMenuSummary?.querySelector('[data-mobile-nav-menu-indicator]')?.hasAttribute('hidden')).toBe(true);
    expect(mobileMenuSummary?.getAttribute('aria-label')).toBe('Select view');

    syncDashboardNavigationIndicators(sidebar, pages, {
      'maintenance-repositories': {
        rows: [{}]
      }
    });

    expect(updatesLink?.querySelector('[data-nav-indicator]')?.hasAttribute('hidden')).toBe(false);
    expect(updatesLink?.getAttribute('aria-label')).toBe('Updates, updates available');
    expect(mobileMenuSummary?.querySelector('[data-mobile-nav-menu-indicator]')?.hasAttribute('hidden')).toBe(false);

    syncDashboardNavigationIndicators(sidebar, pages, {
      campaigns: {
        rows: [{ 'campaign-update-state': 'update-available' }]
      },
      'reports-needing-review': {
        rows: [{}]
      }
    });

    expect(mobileMenuSummary?.getAttribute('aria-label')).toBe('Select view, updates available and 1 more');
  });

  it('cycles the active page view from chrome without page-level buttons', () => {
    const sidebar = renderDashboardNavigation([{ id: 'runs', title: 'Runs' }], 'Example', undefined);
    const page = document.createElement('section');
    page.className = 'dashboard-page';
    page.dataset.viewModes = 'chart card table';
    page.dataset.viewMode = 'chart';
    const shell = document.createElement('div');
    shell.className = 'app-shell';
    shell.append(sidebar, page);
    const root = document.createElement('div');
    root.append(shell);
    document.body.append(root);
    page.addEventListener('dashboard-view-mode-change', (event) => {
      if (!(event instanceof CustomEvent)) return;
      page.dataset.viewMode = event.detail;
      syncMobileViewModeToggle(root);
    });

    enableDashboardNavigation(root);
    syncMobileViewModeToggle(root);

    const toggle = /** @type {HTMLButtonElement} */ (sidebar.querySelector('.mobile-view-mode-toggle'));
    expect(toggle.hidden).toBe(false);
    expect(toggle.dataset.viewMode).toBe('chart');
    expect(toggle.getAttribute('aria-label')).toBe('Switch to Cards view');
    expect(toggle.querySelector('.octicon-graph')).not.toBeNull();

    toggle.click();
    expect(toggle.dataset.viewMode).toBe('card');
    expect(toggle.getAttribute('aria-label')).toBe('Switch to Table view');
    expect(toggle.querySelector('.octicon-stack')).not.toBeNull();
    toggle.click();
    expect(toggle.dataset.viewMode).toBe('table');
    toggle.click();
    expect(toggle.dataset.viewMode).toBe('chart');

    page.removeAttribute('data-view-modes');
    syncMobileViewModeToggle(root);
    expect(toggle.hidden).toBe(true);
    page.dataset.viewModes = 'card';
    syncMobileViewModeToggle(root);
    expect(toggle.hidden).toBe(true);
  });
});

describe('declarative view data', () => {
  it('aggregates, orders, and limits table rows', () => {
    expect(prepareTableRows([
      { campaign: 'alpha', runs: 2 },
      { campaign: 'alpha', runs: 3 },
      { campaign: 'beta', runs: 7 }
    ], [
      { field: 'campaign' },
      { field: 'runs', aggregate: 'sum', as: 'total' }
    ], {
      'order-by': [{ field: 'total', direction: 'desc' }],
      limit: 1
    })).toEqual([{ campaign: 'beta', total: 7 }]);
  });

  it('builds and orders aggregated chart points', () => {
    const points = buildChartPoints('runs', 'Runs', [
      { day: '2026-09-15', status: 'success', count: 2 },
      { day: '2026-09-15', status: 'success', count: 3 },
      { day: '2026-09-16', status: 'failure', count: 1 }
    ], { field: 'day' }, { field: 'count', aggregate: 'sum', as: 'total' }, { field: 'status' }, null);

    expect(prepareChartPoints(points, { field: 'day' }, { field: 'count', aggregate: 'sum', as: 'total' }, { field: 'status' }, {
      'order-by': [{ field: 'total', direction: 'desc' }]
    }).map(({ x, y, color }) => ({ x, y, color }))).toEqual([
      { x: '2026-09-15', y: 5, color: 'success' },
      { x: '2026-09-16', y: 1, color: 'failure' }
    ]);
  });
});
