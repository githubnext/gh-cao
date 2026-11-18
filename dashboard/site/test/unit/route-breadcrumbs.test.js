// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderRouteBreadcrumbs } from '../../src/components/dashboard-header.js';
import { disposeDashboard, renderDashboard } from '../../src/presenter.js';
import { validateDashboardDocument } from '../../src/validator.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';

const breadcrumbs = [
  { page: 'value' },
  { page: 'campaign', parameters: ['campaign'] },
  { page: 'repository', parameters: ['organization', 'repository'] }
];
const pages = [
  { id: 'value', title: 'Operational Value' },
  { id: 'campaign', title: 'Operational Value', titleFormat: 'title-case' },
  { id: 'repository', title: 'Operational Value' }
];

/** @type {HTMLElement | undefined} */
let dashboard;
afterEach(() => {
  if (dashboard) {
    disposeDashboard(dashboard);
    dashboard.remove();
    dashboard = undefined;
  }
  window.history.replaceState(null, '', window.location.pathname);
});

describe('declared route breadcrumbs', () => {
  it('keeps the navigation hierarchy and accumulates only declared route parameters', () => {
    const nodes = renderRouteBreadcrumbs({
      breadcrumbs, pages, activePage: 'repository',
      parameters: new URLSearchParams({ campaign: 'security-updates', organization: 'octo', repository: 'api', unrelated: 'ignored' })
    });
    expect(nodes.map(node => node.textContent)).toEqual(['Operational Value', 'Security Updates', 'octo/api']);
    expect(nodes.map(node => node.getAttribute('href'))).toEqual([
      '#page-value', '#page-campaign?campaign=security-updates', null
    ]);
    expect(nodes[2].getAttribute('aria-current')).toBe('page');
  });

  it('renders direct-entry labels as text and encodes parent links without accepting markup or URLs', () => {
    const label = '<script>bad</script> & /';
    const nodes = renderRouteBreadcrumbs({
      breadcrumbs, pages, activePage: 'repository',
      parameters: new URLSearchParams({ campaign: label, organization: 'one', repository: 'a?b#c' })
    });
    expect(nodes[1].querySelector('script')).toBeNull();
    const href = nodes[1].getAttribute('href') ?? '';
    expect(new URLSearchParams(href.slice(href.indexOf('?') + 1)).get('campaign')).toBe(label);
    expect(nodes[2].textContent).toBe('one/a?b#c');
  });

  it('omits a chain that resolves to the active page alone', () => {
    expect(renderRouteBreadcrumbs({
      breadcrumbs: [{ page: 'repository' }], pages, activePage: 'repository', parameters: new URLSearchParams()
    })).toEqual([]);
  });

  it('stops at the first missing route binding and fails explicitly for an unknown page', () => {
    expect(renderRouteBreadcrumbs({
      breadcrumbs, pages, activePage: 'repository',
      parameters: new URLSearchParams({ organization: 'octo', repository: 'api' })
    }).map(node => node.textContent)).toEqual(['Operational Value']);
    expect(() => renderRouteBreadcrumbs({
      breadcrumbs: [{ page: 'unknown' }], pages, activePage: 'repository', parameters: new URLSearchParams()
    })).toThrow('Unknown breadcrumb page');
  });

  it('retains the view title through direct entry, route allocation, parent navigation and legacy navigation', async () => {
    window.history.replaceState(null, '', '#page-repository?campaign=security-updates&organization=octo&repository=api');
    dashboard = renderDashboard({
      document: { languageVersion: '0.1.0', dashboard: {
        id: 'breadcrumb-navigation', title: 'Dashboard',
        pages: [
          { id: 'value', kind: 'custom', title: 'Operational Value', route: { breadcrumbs: [{ page: 'value' }] }, views: [] },
          { id: 'campaign', kind: 'custom', title: 'Operational Value', route: {
            'navigation-page': 'value', 'hash-query-parameter': 'campaign', 'title-format': 'title-case',
            breadcrumbs: breadcrumbs.slice(0, 2)
          }, views: [] },
          { id: 'repository', kind: 'custom', title: 'Operational Value', route: {
            'navigation-page': 'value', 'hash-query-parameter': 'repository', breadcrumbs
          }, views: [] },
          { id: 'other', kind: 'custom', title: 'Other', views: [] }
        ]
      } },
      sources: {}
    });
    document.body.append(dashboard);
    const root = dashboard;
    expect(root.querySelector('#page-title')?.textContent).toBe('Operational Value');
    expect(root.querySelectorAll('[data-route-breadcrumb]')).toHaveLength(3);
    root.querySelector('#page-repository')?.dispatchEvent(new CustomEvent('dashboard-route-allocation', {
      bubbles: true, detail: { title: 'octo/api', description: 'Repository evidence' }
    }));
    expect(root.querySelector('#page-title')?.textContent).toBe('Operational Value');
    expect(document.title).toBe('Operational Value · Dashboard');
    const campaignLink = /** @type {HTMLAnchorElement} */ (root.querySelector('[data-route-breadcrumb][href^="#page-campaign"]'));
    campaignLink.click();
    await vi.waitFor(() => expect(root.querySelectorAll('[data-route-breadcrumb]')).toHaveLength(2));
    expect(root.querySelector('#page-title')?.textContent).toBe('Operational Value');
    expect(root.querySelector('[data-route-breadcrumb][aria-current]')?.textContent).toBe('Security Updates');
    const other = /** @type {HTMLAnchorElement} */ (root.querySelector('[data-nav-page-id="other"]'));
    other.click();
    await vi.waitFor(() => expect(root.querySelector('#page-title')?.textContent).toBe('Other'));
    expect(root.querySelectorAll('[data-route-breadcrumb]')).toHaveLength(0);
  });

  it.each([
    [],
    [{ page: 'missing-page' }],
    [{ page: 'value-report' }, { page: 'value-report' }],
    [{ page: 'value-report', parameters: [] }],
    [{ page: 'value-report', parameters: ['campaign', 'campaign'] }],
    [{ page: 'value-report', parameters: ['invalid parameter'] }],
    [{ page: 'value-report', template: '<script>' }]
  ].map(invalid => [invalid]))('rejects invalid breadcrumb contracts %j', (invalid) => {
    const documentModel = structuredClone(authoritativeDashboard);
    const page = documentModel.dashboard.pages.find((/** @type {{ id: string }} */ entry) => entry.id === 'value-report');
    page.route.breadcrumbs = invalid;
    const result = validateDashboardDocument(JSON.stringify(documentModel));
    expect(result.ok).toBe(false);
    expect(result.errors.some(error => error.path.includes('.breadcrumbs'))).toBe(true);
  });
});
