import { h } from '../dom.js';
import { createDebug } from '../debug.js';
import { navigationIndicator } from '../navigation-indicator.js';
import { pageSourceAvailable } from '../view-availability.js';
import { agenticWorkflowMark, octicon } from '../octicons.js';
import { effect, state } from '../reactive.js';
import { createFactoryScope } from './factory-elements.js';
import { scopedStorageKey } from '../storage-scope.js';
import { titleCase } from './count-formatters.js';
import { enableDetailsMenuDismissal, syncToggleButtonState } from './ui-primitives.js';

const debugNavigation = createDebug('dashboard-navigation');

const SIDEBAR_COLLAPSED_STORAGE_KEY = scopedStorageKey('central-agentic-ops.dashboard.sidebar-collapsed');
const NAV_SECTIONS_OPEN_STORAGE_KEY = scopedStorageKey('central-agentic-ops.dashboard.nav-sections-open');
const VIEW_MODE_LABELS = { chart: 'Chart', table: 'Table', card: 'Cards' };
const VIEW_MODE_ICONS = { chart: 'graph', table: 'table', card: 'stack' };

/**
 * @param {Array<Record<string, unknown>>} pages
 * @param {string} title
 * @param {Array<{ label?: string, pages?: string[], experimental?: boolean, placement?: string }> | undefined} navigation
 */
export function renderDashboardNavigation(pages, title, navigation) {
  const scope = createFactoryScope();
  const offline = state(!navigator.onLine);
  const mobileStatus = h('span', { className: 'dashboard-offline-status', role: 'status', hidden: true, title: 'Offline', 'aria-label': 'Offline' }, octicon('cloud-offline'));
  const desktopStatus = h('span', { className: 'dashboard-offline-status', role: 'status', hidden: true, title: 'Offline', 'aria-label': 'Offline' }, octicon('cloud-offline'), h('span', null, 'Offline'));
  effect(() => {
    mobileStatus.hidden = !offline.get();
    desktopStatus.hidden = !offline.get();
  }, { signal: scope.signal });
  window.addEventListener('online', () => offline.set(false), { signal: scope.signal });
  window.addEventListener('offline', () => offline.set(true), { signal: scope.signal });
  const pagesById = new Map(pages.map((page) => [page.id, page]));
  const configuredSections = Array.isArray(navigation) && navigation.length > 0
    ? navigation
      .map((section) => ({
        label: section?.label,
        experimental: section?.experimental === true,
        placement: section?.placement === 'bottom' ? 'bottom' : 'top',
        pages: (Array.isArray(section?.pages) ? section.pages : [])
          .map((pageId) => pagesById.get(pageId))
          .filter((page) => page !== undefined)
      }))
      .filter((section) => section.pages.length > 0)
    : [{ label: undefined, experimental: false, placement: 'top', pages }];
  const experimentalPages = configuredSections
    .filter((section) => section.experimental)
    .flatMap((section) => section.pages);
  const standardSections = configuredSections.filter((section) => !section.experimental);
  const navigationSections = [
    ...standardSections.filter((section) => section.placement !== 'bottom'),
    ...(experimentalPages.length > 0
      ? [{ label: 'Experimental', experimental: true, placement: 'top', pages: experimentalPages }]
      : []),
    ...standardSections.filter((section) => section.placement === 'bottom')
  ];
  const firstPageId = navigationSections.find((section) => !section.experimental)?.pages[0]?.id ?? pages[0]?.id;
  const mainSectionIndex = Math.max(
    0,
    navigationSections.findIndex((section) => section.label?.toLowerCase() === 'main')
  );
  let navigationPageIndex = 0;

  const root = h(
    'aside',
    { className: 'org-sidebar', 'aria-label': 'Central Agentic Ops navigation', dataset: { defaultPageId: firstPageId ?? '' } },
    h(
      'div',
      { className: 'sidebar-header' },
      h(
        'button',
        {
          className: 'mobile-history-back',
          type: 'button',
          'aria-label': 'Go back',
          title: 'Go back',
          hidden: true
        },
        octicon('chevron-left', 'mobile-history-back-icon')
      ),
      h(
        'a',
        { className: 'sidebar-brand', href: firstPageId ? `#page-${firstPageId}` : '#main-content', title },
        agenticWorkflowMark(),
        h('span', null, title)
      ),
      h('div', { className: 'mobile-page-header' }, h('span', { className: 'mobile-brand-name' }, title)),
      h(
        'button',
        {
          className: 'mobile-view-mode-toggle',
          type: 'button',
          'aria-label': 'Change view',
          title: 'Change view',
          hidden: true
        },
        octicon('graph')
      ),
      h(
        'details',
        { className: 'mobile-nav-menu' },
        h(
          'summary',
          { role: 'button', 'aria-label': 'Select view', title: 'Select view' },
          octicon('three-bars'),
          mobileStatus,
          h('span', { className: 'nav-indicator mobile-nav-menu-indicator', hidden: true, 'aria-hidden': 'true', 'data-mobile-nav-menu-indicator': '' })
        ),
        h(
          'div',
          { className: 'mobile-nav-menu-list' },
          h('div', { className: 'mobile-nav-menu-actions', 'aria-label': 'Dashboard controls' }),
          ...navigationSections.flatMap((section) => [
            ...(typeof section.label === 'string' && section.label.length > 0
              ? [h('span', { className: 'mobile-nav-section-label' }, section.label)]
              : []),
            ...section.pages.map((page) => renderMobileNavItem(page, page.id === firstPageId))
          ])
        )
      ),
      h(
        'button',
        {
          className: 'sidebar-toggle',
          type: 'button',
          'aria-label': 'Collapse navigation',
          'aria-expanded': 'true',
          title: 'Collapse navigation'
        },
        octicon('sidebar-expand')
      )
    ),
    h(
      'nav',
      { className: 'primary-nav', 'aria-label': 'Primary' },
      ...navigationSections.flatMap((section, sectionIndex) => {
        const items = section.pages.map((page) => {
          const pageIndex = navigationPageIndex++;
          return renderNavItem(page, page.id === firstPageId, pageIndex >= 6, pageIndex >= 5);
        });
        return typeof section.label === 'string' && section.label.length > 0
          ? [h(
              'details',
              {
                className: `nav-section${section.placement === 'bottom' ? ' nav-section-bottom' : ''}`,
                'aria-label': section.label,
                dataset: { navSection: section.label },
                open: section.placement === 'bottom' || sectionIndex === mainSectionIndex || ['investigate', 'insights'].includes(section.label?.toLowerCase() ?? '')
              },
              h(
                'summary',
                { className: 'nav-section-toggle', title: `${section.label} menu section` },
                h('span', { className: 'nav-section-label' }, section.label),
                octicon('chevron-right')
              ),
              h('div', { className: 'nav-section-items' }, ...items)
            )]
          : items;
      })
    ),
    desktopStatus
  );
  scope.bind(root);
  return root;
}

/**
 * @param {HTMLElement} root
 * @param {Array<Record<string, unknown>>} pages
 * @param {Record<string, { rows?: Array<Record<string, unknown>> } | undefined>} sources
 */
export function syncDashboardNavigationIndicators(root, pages, sources) {
  const activeMobileIndicatorLabels = [];
  for (const page of pages) {
    const indicator = navigationIndicator(page);
    if (!indicator) continue;
    const active = indicatorMatches(indicator, sources);
    if (active) activeMobileIndicatorLabels.push(indicator.label);
    const label = pageNavigationAccessibleLabel(page, active ? indicator.label : '');
    const pageId = selectorIdentifier(String(page.id));
    const links = root.querySelectorAll(`[data-nav-page-id="${pageId}"], [data-mobile-nav-page-id="${pageId}"]`);
    for (const link of links) {
      if (!(link instanceof HTMLAnchorElement) || link.closest('[data-route-tabs]')) continue;
      link.setAttribute('aria-label', label);
      link.title = label;
      const dot = link.querySelector('[data-nav-indicator]');
      if (dot instanceof HTMLElement) dot.hidden = !active;
    }
  }
  syncMobileNavigationMenuIndicator(root, activeMobileIndicatorLabels);
}

/**
 * @param {HTMLElement} root
 * @param {Array<Record<string, unknown>>} pages
 * @param {Record<string, { rows?: unknown[], metadata?: Record<string, unknown> } | undefined>} sources
 */
export function syncDashboardNavigationAvailability(root, pages, sources) {
  for (const page of pages) {
    const available = pageSourceAvailable(page, sources);
    const id = selectorIdentifier(String(page.id));
    for (const link of root.querySelectorAll(`[data-nav-page-id="${id}"], [data-mobile-nav-page-id="${id}"]`)) {
      if (!(link instanceof HTMLAnchorElement)) continue;
      link.setAttribute('aria-disabled', String(!available));
      if (available) {
        link.removeAttribute('tabindex');
        link.title = link.getAttribute('aria-label') ?? pageTitle(page);
      } else {
        link.tabIndex = -1;
        link.title = typeof page.requires === 'object' && page.requires !== null && 'message' in page.requires
          ? String(page.requires.message) : 'Required data is unavailable.';
      }
    }
  }
}

/** @param {HTMLElement} root */
export function enableDashboardNavigation(root) {
  enableSidebarToggle(root);
  enableNavSectionStatePersistence(root);
  enableMobileViewModeToggle(root);
  enableSidebarKeyboardNavigation(root);
  const menu = root.querySelector('.mobile-nav-menu');
  if (menu instanceof HTMLDetailsElement) {
    enableDetailsMenuDismissal(root, menu, '[data-mobile-nav-page-id]');
    enableMobileNavigationKeyboard(menu);
  }
}

/** @param {HTMLElement} root */
function enableSidebarKeyboardNavigation(root) {
  const nav = root.querySelector('.primary-nav');
  if (!(nav instanceof HTMLElement)) return;
  /** @returns {HTMLElement[]} */
  const visibleItems = () => [...nav.querySelectorAll('.nav-section-toggle, [data-nav-page-id]')]
    .filter((item) => item instanceof HTMLElement)
    .map((item) => /** @type {HTMLElement} */ (item))
    .filter((item) => (!item.closest('.nav-section') || item.matches('.nav-section-toggle')
        || /** @type {HTMLDetailsElement | null} */ (item.closest('.nav-section'))?.open)
      && item.getAttribute('aria-disabled') !== 'true'
      && getComputedStyle(item).display !== 'none');
  nav.addEventListener('keydown', (event) => {
    if (event.altKey || event.ctrlKey || event.metaKey) return;
    const target = event.target;
    if (!(target instanceof HTMLElement) || !target.matches('.nav-section-toggle, [data-nav-page-id]')) return;
    const items = visibleItems();
    const index = items.indexOf(target);
    if (index < 0) return;
    const section = target.parentElement instanceof HTMLDetailsElement ? target.parentElement : null;
    /** @type {HTMLElement | undefined} */
    let next;
    switch (event.key) {
      case 'ArrowDown': next = items[index + 1]; break;
      case 'ArrowUp': next = items[index - 1]; break;
      case 'Home': next = items[0]; break;
      case 'End': next = items.at(-1); break;
      case 'ArrowRight':
        if (!section) return;
        if (!section.open) section.open = true;
        else if (section.contains(items[index + 1])) next = items[index + 1];
        break;
      case 'ArrowLeft':
        if (section?.open) section.open = false;
        else if (!section) {
          const summary = /** @type {HTMLElement | null} */ (target.closest('.nav-section')?.querySelector('.nav-section-toggle'));
          if (summary && items.includes(summary)) next = summary;
          else return;
        }
        else return;
        break;
      default: return;
    }
    event.preventDefault();
    next?.focus();
  });
  nav.addEventListener('toggle', (event) => {
    const section = event.target;
    if (section instanceof HTMLDetailsElement && !section.open && section.contains(section.ownerDocument.activeElement)) {
      section.querySelector('summary')?.focus();
    }
  }, true);
}

/** @param {HTMLDetailsElement} menu */
function enableMobileNavigationKeyboard(menu) {
  menu.addEventListener('keydown', (event) => {
    if (event.altKey || event.ctrlKey || event.metaKey || !menu.open) return;
    const target = event.target;
    if (!(target instanceof HTMLElement) || !target.matches('.mobile-nav-menu > summary, [data-mobile-nav-page-id]')) return;
    const links = /** @type {HTMLAnchorElement[]} */ ([...menu.querySelectorAll('[data-mobile-nav-page-id]')]
      .filter((link) => link.getAttribute('aria-disabled') !== 'true'));
    const index = links.indexOf(/** @type {HTMLAnchorElement} */ (target));
    /** @type {HTMLAnchorElement | undefined} */
    let next;
    switch (event.key) {
      case 'ArrowDown': next = links[index + 1]; break;
      case 'ArrowUp': next = links[index - 1]; break;
      case 'Home': next = links[0]; break;
      case 'End': next = links.at(-1); break;
      default: return;
    }
    event.preventDefault();
    next?.focus();
  });
}

/** @param {HTMLElement} root */
export function syncMobileViewModeToggle(root) {
  const toggle = root.querySelector('.mobile-view-mode-toggle');
  const activePage = root.querySelector('.dashboard-page:not([hidden])');
  const modeButtons = [...activePage?.querySelectorAll('[data-view-mode-value]') ?? []]
    .filter((button) => button instanceof HTMLButtonElement);
  if (!(toggle instanceof HTMLButtonElement) || modeButtons.length < 2) {
    if (toggle instanceof HTMLButtonElement) toggle.hidden = true;
    return;
  }

  const activeIndex = Math.max(0, modeButtons.findIndex((button) => button.getAttribute('aria-pressed') === 'true'));
  const activeMode = modeButtons[activeIndex]?.getAttribute('data-view-mode-value');
  if (activeMode !== 'chart' && activeMode !== 'table' && activeMode !== 'card') {
    toggle.hidden = true;
    return;
  }
  const nextMode = modeButtons[(activeIndex + 1) % modeButtons.length]?.getAttribute('data-view-mode-value');
  const nextLabel = nextMode === 'chart' || nextMode === 'table' || nextMode === 'card'
    ? VIEW_MODE_LABELS[nextMode]
    : 'next';
  toggle.hidden = false;
  toggle.dataset.viewMode = activeMode;
  toggle.setAttribute('aria-label', `Switch to ${nextLabel} view`);
  toggle.title = `Current view: ${VIEW_MODE_LABELS[activeMode]}. Switch to ${nextLabel}.`;
  toggle.replaceChildren(octicon(VIEW_MODE_ICONS[activeMode]));
}

/** @param {HTMLElement} root */
function enableMobileViewModeToggle(root) {
  const toggle = root.querySelector('.mobile-view-mode-toggle');
  if (!(toggle instanceof HTMLButtonElement)) return;
  toggle.addEventListener('click', () => {
    const activePage = root.querySelector('.dashboard-page:not([hidden])');
    const modeButtons = [...activePage?.querySelectorAll('[data-view-mode-value]') ?? []]
      .filter((button) => button instanceof HTMLButtonElement);
    if (modeButtons.length < 2) return;
    const activeIndex = Math.max(0, modeButtons.findIndex((button) => button.getAttribute('aria-pressed') === 'true'));
    modeButtons[(activeIndex + 1) % modeButtons.length]?.click();
    syncMobileViewModeToggle(root);
  });
}

/** @param {Record<string, unknown>} page */
function pageTitle(page) {
  return typeof page['navigation-label'] === 'string' && page['navigation-label'].length > 0
    ? page['navigation-label']
    : typeof page.title === 'string' && page.title.length > 0
      ? page.title
      : titleCase(String(page.id));
}

/** @param {Record<string, unknown>} page */
function pageIcon(page) {
  return typeof page.icon === 'string' ? page.icon : 'server';
}

/** @param {Record<string, unknown>} page @param {string} [indicatorLabel] */
function pageNavigationAccessibleLabel(page, indicatorLabel = '') {
  return [pageTitle(page), page.experimental === true ? 'Experimental' : '', indicatorLabel]
    .filter(Boolean)
    .join(', ');
}

/** @param {Record<string, unknown>} page */
function renderExperimentalPageLabel(page) {
  return page.experimental === true
    ? h('span', { className: 'experimental-page-label', 'aria-hidden': 'true', title: 'Experimental' }, octicon('beaker'))
    : null;
}

/** @param {Record<string, unknown>} page @param {boolean} isActive @param {boolean} mobileOverflow @param {boolean} narrowMobileOverflow */
function renderNavItem(page, isActive, mobileOverflow, narrowMobileOverflow) {
  const title = pageTitle(page);
  const hasIndicator = navigationIndicator(page) !== null;
  return h(
    'a',
    {
      href: `#page-${page.id}`,
      className: `nav-item${isActive ? ' active' : ''}${mobileOverflow ? ' mobile-nav-overflow' : ''}${narrowMobileOverflow ? ' narrow-mobile-nav-overflow' : ''}`,
      'aria-current': isActive ? 'page' : undefined,
      'aria-label': pageNavigationAccessibleLabel(page),
      title,
      'data-nav-page-id': page.id
    },
    octicon(pageIcon(page)),
    h('span', { className: 'nav-label' }, title),
    renderExperimentalPageLabel(page),
    hasIndicator ? h('span', { className: 'nav-indicator', hidden: true, 'aria-hidden': 'true', 'data-nav-indicator': '' }) : null
  );
}

/** @param {Record<string, unknown>} page @param {boolean} isActive */
function renderMobileNavItem(page, isActive) {
  const title = pageTitle(page);
  const hasIndicator = navigationIndicator(page) !== null;
  return h(
    'a',
    {
      href: `#page-${page.id}`,
      className: `mobile-nav-item${isActive ? ' active' : ''}`,
      'aria-current': isActive ? 'page' : undefined,
      'aria-label': pageNavigationAccessibleLabel(page),
      title,
      'data-mobile-nav-page-id': page.id
    },
    octicon(pageIcon(page)),
    h('span', { className: 'mobile-nav-label' }, title),
    renderExperimentalPageLabel(page),
    hasIndicator ? h('span', { className: 'nav-indicator', hidden: true, 'aria-hidden': 'true', 'data-nav-indicator': '' }) : null
  );
}

/** @param {{ sources: string[] }} indicator @param {Record<string, { rows?: Array<Record<string, unknown>> } | undefined>} sources */
function indicatorMatches(indicator, sources) {
  return indicator.sources.some((sourceName) => {
    const rows = sources[sourceName]?.rows;
    return Array.isArray(rows) && rows.length > 0;
  });
}

/**
 * @param {HTMLElement} root
 * @param {string[]} indicatorLabels
 */
function syncMobileNavigationMenuIndicator(root, indicatorLabels) {
  const summary = root.querySelector('.mobile-nav-menu > summary');
  const dot = summary?.querySelector('[data-mobile-nav-menu-indicator]');
  if (!(summary instanceof HTMLElement) || !(dot instanceof HTMLElement)) return;
  const uniqueLabels = [...new Set(indicatorLabels)];
  const active = uniqueLabels.length > 0;
  const label = uniqueLabels.length > 1
    ? `Select view, ${uniqueLabels[0]} and ${uniqueLabels.length - 1} more`
    : active ? `Select view, ${uniqueLabels[0]}` : 'Select view';
  summary.setAttribute('aria-label', label);
  summary.title = label;
  dot.hidden = !active;
}

/** @param {string} value */
function selectorIdentifier(value) {
  return globalThis.CSS?.escape ? globalThis.CSS.escape(value) : value.replace(/[^a-zA-Z0-9_-]/g, '\\$&');
}


/** @param {HTMLElement} root */
function enableSidebarToggle(root) {
  const appShell = root.querySelector('.app-shell');
  const toggle = root.querySelector('.sidebar-toggle');
  if (!(appShell instanceof HTMLElement) || !(toggle instanceof HTMLButtonElement)) return;

  let initiallyCollapsed = false;
  try {
    initiallyCollapsed = globalThis.window?.localStorage?.getItem(SIDEBAR_COLLAPSED_STORAGE_KEY) === 'true';
  } catch (error) {
    // Storage can be unavailable in embedded or privacy-restricted contexts.
    debugNavigation({ event: 'sidebar-state.read_failed', errorName: /** @type {Error} */ (error)?.name });
  }
  const collapsed = state(initiallyCollapsed);

  // The smallest DOM update needed from collapsed state: toggle the shell class and
  // resync the toggle button's accessible label, title, and icon.
  effect(() => {
    const isCollapsed = collapsed.get();
    appShell.classList.toggle('sidebar-collapsed', isCollapsed);
    syncToggleButtonState(toggle, !isCollapsed, {
      expandedLabel: 'Collapse navigation',
      collapsedLabel: 'Expand navigation',
      expandedIcon: 'sidebar-expand',
      collapsedIcon: 'sidebar-collapse'
    });
  });

  toggle.addEventListener('click', () => {
    const nextCollapsed = !collapsed.get();
    collapsed.set(nextCollapsed);
    try {
      globalThis.window?.localStorage?.setItem(SIDEBAR_COLLAPSED_STORAGE_KEY, String(nextCollapsed));
    } catch (error) {
      // The display mode still works for the current page when storage is unavailable.
      debugNavigation({ event: 'sidebar-state.write_failed', errorName: /** @type {Error} */ (error)?.name });
    }
  });
}

/**
 * Persist which sidebar sections, such as "Data", are expanded so the sidebar
 * keeps its shape across reloads alongside the other UI soft state.
 * @param {HTMLElement} root
 */
function enableNavSectionStatePersistence(root) {
  const sections = [...root.querySelectorAll('.nav-section[data-nav-section]')]
    .filter((section) => section instanceof HTMLDetailsElement);
  if (sections.length === 0) return;

  const storedState = readNavSectionState();
  for (const section of sections) {
    const label = section.dataset.navSection;
    if (label === undefined) continue;
    const stored = storedState[label];
    if (typeof stored === 'boolean') section.open = stored;
  }

  for (const section of sections) {
    section.addEventListener('toggle', () => {
      const label = section.dataset.navSection;
      if (label === undefined) return;
      const state = readNavSectionState();
      state[label] = section.open;
      try {
        globalThis.window?.localStorage?.setItem(NAV_SECTIONS_OPEN_STORAGE_KEY, JSON.stringify(state));
      } catch {
        // Section state still applies to the current page when storage is unavailable.
      }
    });
  }
}

/** @returns {Record<string, boolean>} */
function readNavSectionState() {
  try {
    const raw = globalThis.window?.localStorage?.getItem(NAV_SECTIONS_OPEN_STORAGE_KEY);
    const parsed = typeof raw === 'string' ? JSON.parse(raw) : null;
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
    return Object.fromEntries(
      Object.entries(parsed).filter(([, value]) => typeof value === 'boolean')
    );
  } catch (error) {
    debugNavigation({ event: 'nav-section-state.read_failed', errorName: /** @type {Error} */ (error)?.name });
    return {};
  }
}