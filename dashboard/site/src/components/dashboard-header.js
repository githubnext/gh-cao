import { createDebug } from '../debug.js';
import { h } from '../dom.js';
import { octicon } from '../octicons.js';
import { renderActionLabel, renderTooltip } from './ui-primitives.js';
import { titleCase } from './count-formatters.js';

const debugDashboardHeader = createDebug('dashboard-header');

/** @typedef {{ label: string, description: string, icon?: string }} PageTooltip */

/**
 * @param {HTMLElement} root
 * @param {PageTooltip | null} tooltip
 */
export function renderDashboardPageHelp(root, tooltip) {
  const key = JSON.stringify(tooltip);
  if (root.dataset.tooltip === key) return;
  root.dataset.tooltip = key;
  root.hidden = !tooltip;
  root.replaceChildren(...(tooltip ? [renderTooltip({
    id: 'page-help-tooltip', label: tooltip.label, description: tooltip.description,
    icon: octicon(tooltip.icon ?? 'question'), viewportAnchored: true, mobilePopup: true
  })] : []));
}

/**
 * @typedef {{ page: string, parameters?: string[] }} RouteBreadcrumb
 * @param {{
 *   breadcrumbs: RouteBreadcrumb[],
 *   pages: { id: string, title: string, titleFormat?: string }[],
 *   parameters: URLSearchParams,
 *   activePage: string
 * }} options
 */
export function renderRouteBreadcrumbs(options) {
  const boundParameters = new URLSearchParams();
  /** @type {HTMLElement[]} */
  const nodes = [];
  for (const breadcrumb of options.breadcrumbs) {
    const page = options.pages.find(candidate => candidate.id === breadcrumb.page);
    if (!page) throw new Error(`Unknown breadcrumb page: ${breadcrumb.page}`);
    const names = breadcrumb.parameters ?? [];
    const values = names.map(name => options.parameters.get(name)?.trim() ?? '');
    if (values.some(value => !value)) break;
    for (const [index, name] of names.entries()) boundParameters.set(name, values[index]);
    const label = values.length > 0 ? values.join('/') : page.title;
    const formattedLabel = page.titleFormat === 'title-case' && values.length > 0 ? titleCase(label) : label;
    const query = boundParameters.toString();
    nodes.push(breadcrumb.page === options.activePage
      ? h('span', { 'data-route-breadcrumb': '', 'aria-current': 'page' }, formattedLabel)
      : h('a', {
        'data-route-breadcrumb': '',
        'data-nav-page-id': breadcrumb.page,
        'aria-label': formattedLabel,
        href: `#page-${encodeURIComponent(breadcrumb.page)}${query ? `?${query}` : ''}`
      }, formattedLabel));
  }
  // An active-only chain would just repeat the page heading.
  return nodes.some(node => node instanceof HTMLAnchorElement) ? nodes : [];
}

/**
 * @param {{ title: string, description?: string, experimental?: boolean, overviewPageHref: string, dashboardHorizon: HTMLElement, accountMenu: HTMLElement | null, githubUrlBase: string, dashboardRepository: string | null }} options
 */
export function renderDashboardHeader(options) {
  debugDashboardHeader({
    event: 'initial-render',
    hasTitle: Boolean(options.title),
    experimental: options.experimental === true,
    hasDescription: Boolean(options.description),
    hasRepositoryLink: options.dashboardRepository !== null,
    hasAccountMenu: options.accountMenu !== null
  });
  return h(
    'header',
    { className: 'top-nav' },
    h(
      'div',
      { className: 'shell' },
      h(
        'div',
        { className: 'overview-header', 'aria-labelledby': 'page-title' },
        h(
          'nav',
          { className: 'breadcrumb-context', 'aria-label': 'Breadcrumb' },
          h('a', { hidden: true, 'data-breadcrumb-root': '' }),
          h('a', { href: options.overviewPageHref, hidden: true, 'data-breadcrumb-dashboard': '' }, 'Overview')
        ),
        h(
          'div',
          { className: 'title-area' },
          h('h1', { id: 'page-title', tabIndex: -1, 'data-breadcrumb-page': '' }, options.title),
          h('span', { 'data-page-help': '', hidden: true }),
          h(
            'span',
            {
              className: 'experimental-page-label',
              'data-page-experimental': '',
              hidden: options.experimental !== true,
              role: 'img',
              'aria-label': 'Experimental'
            },
            octicon('beaker')
          ),
          h('a', { className: 'title-link', 'data-page-title-link': '', hidden: true }),
          h('span', { className: 'mode-indicator', 'data-page-mode': '', hidden: true })
        ),
        h('p', { className: 'lede', 'data-page-description': '', hidden: !options.description }, options.description ?? '')
      ),
      h(
        'div',
        { className: 'report-actions' },
        options.dashboardHorizon,
        options.dashboardRepository
          ? h(
              'a',
              {
                className: 'repository-link',
                href: `${options.githubUrlBase}/${options.dashboardRepository}`,
                'aria-label': `View ${options.dashboardRepository} on GitHub`,
                title: `View ${options.dashboardRepository} on GitHub`
              },
              octicon('mark-github'),
              renderActionLabel(options.dashboardRepository)
            )
          : null,
        options.accountMenu
      )
    )
  );
}