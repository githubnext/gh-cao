import { afterEach, expect, it, vi } from 'vitest';
import { disposeDashboard, renderDashboard } from '../../src/presenter.js';
import { pageCapabilityAvailable, pageSourceAvailable } from '../../src/view-availability.js';
import { navigationIndicatorSourceNames } from '../../src/navigation-indicator.js';
import { splitDashboardDocument } from '../../src/dashboard-chunks.js';
import { validateDashboardDocument } from '../../src/validator.js';

const requirement = { source: 'campaign-navigation-availability', message: 'Campaign information is unavailable.' };
/** @type {import('../../src/presenter.js').PresentableCustomPage} */
const pageDefinition = {
  id: 'inventory', kind: 'custom', title: 'Inventory', requires: requirement,
  views: [{ id: 'inventory-callout', title: 'Inventory ready', description: 'Collected inventory is available.', mark: 'callout',
    callout: { label: 'Inventory ready', icon: 'info' } }]
};
/** @type {import('../../src/presenter.js').PresentationDocument} */
const input = {
  languageVersion: '0.1.0',
  dashboard: {
    id: 'source-prerequisite', title: 'Source prerequisite', pages: [
      { id: 'home', kind: 'custom', title: 'Home', views: [{
        id: 'home-callout', title: 'Home', description: 'Available everywhere.', mark: 'callout', callout: { label: 'Home', icon: 'info' }
      }] }, pageDefinition
    ],
    queries: [{ name: requirement.source, subject: 'Presence of canonical campaign inventory.',
      from: 'campaigns', select: [{ field: 'campaign' }], limit: 1 }]
  }
};
/** @type {HTMLElement[]} */
const roots = [];
/**
 * @param {'available'|'empty'|'unavailable'} availability
 * @param {boolean} [present]
 * @returns {Record<string, import('../../src/presenter.js').LogicalSourceInput>}
 */
function evidence(availability, present = availability === 'available') {
  return { [requirement.source]: {
    source: requirement.source, rows: present ? [{ campaign: 'alpha' }] : [],
    metadata: { availability, completeness: 'partial', freshness: 'fresh',
      'source-id': requirement.source, 'source-kind': 'derived',
      'as-of': '2026-11-06T12:00:00Z', 'retrieved-at': '2026-11-06T12:00:00Z' }
  } };
}

afterEach(() => {
  for (const root of roots.splice(0)) { disposeDashboard(root); root.remove(); }
  window.history.replaceState(null, '', '/');
  vi.unstubAllGlobals();
});

it.each([
  ['missing', {}, false], ['empty', evidence('empty'), false],
  ['unavailable', evidence('unavailable', true), false],
  ['available but empty', evidence('available', false), false],
  ['partial available evidence', evidence('available'), true]
])('gates desktop/mobile navigation and direct routes with %s evidence', async (_label, sources, available) => {
  window.history.replaceState(null, '', '/#page-inventory');
  const loader = Object.assign(vi.fn(async () => ({})), { prepare: vi.fn(async () => {}) });
  const root = renderDashboard({ document: input, sources, loadPageSources: loader });
  roots.push(root);
  document.body.append(root);
  await vi.waitFor(() => expect(root.querySelector('#page-inventory')?.textContent)
    .toContain(available ? 'Inventory ready' : requirement.message));
  const links = root.querySelectorAll('a[href="#page-inventory"]');
  expect(links).toHaveLength(2);
  for (const link of links) expect(link.getAttribute('aria-disabled')).toBe(String(!available));
  expect(loader.mock.calls.length > 0).toBe(available);
  expect(loader.prepare).not.toHaveBeenCalled();
});

it('reacts to prerequisite appearance/disappearance and stops updates on disposal', async () => {
  window.history.replaceState(null, '', '/#page-inventory');
  /** @type {import('../../src/presenter.js').PageSourceLoadOptions | undefined} */
  let subscription;
  const loader = Object.assign(vi.fn(async () => ({})), {
    subscribeBackgroundSources: vi.fn(async (_names, options) => {
      subscription = options;
      return evidence('unavailable');
    })
  });
  const root = renderDashboard({ document: input, sources: {}, loadPageSources: loader });
  roots.push(root);
  document.body.append(root);
  await vi.waitFor(() => expect(root.querySelector('#page-inventory')?.textContent).toContain(requirement.message));
  expect(loader.subscribeBackgroundSources.mock.calls[0][0]).toEqual([requirement.source]);
  if (!subscription) throw new Error('Expected the prerequisite source subscription.');
  subscription.onUpdate(evidence('available'));
  await vi.waitFor(() => expect(root.querySelector('#page-inventory')?.textContent).toContain('Inventory ready'));
  for (const link of root.querySelectorAll('a[href="#page-inventory"]')) {
    expect(link.getAttribute('aria-disabled')).toBe('false');
  }
  subscription.onUpdate(evidence('unavailable'));
  await vi.waitFor(() => expect(root.querySelector('#page-inventory')?.textContent).toContain(requirement.message));
  expect(loader).toHaveBeenCalledTimes(1);
  disposeDashboard(root);
  expect(subscription.signal.aborted).toBe(true);
  subscription.onUpdate(evidence('available'));
  expect(root.querySelector('a[href="#page-inventory"]')?.getAttribute('aria-disabled')).toBe('true');
});

it('blocks mouse/keyboard activation and retains the available page', async () => {
  const root = renderDashboard({ document: input, sources: {} });
  roots.push(root);
  document.body.append(root);
  for (const link of root.querySelectorAll('a[href="#page-inventory"]')) {
    expect(link.getAttribute('tabindex')).toBe('-1');
    link.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
  }
  await vi.waitFor(() => expect(root.querySelector('#page-title')?.textContent).toBe('Home'));
  expect(window.location.hash).not.toContain('inventory');
});

it('preserves the source requirement and query in lazy page/core documents', () => {
  const { core, pageChunks } = splitDashboardDocument(input);
  expect(core.dashboard.pages.find(page => page.id === 'inventory')?.requires).toEqual(requirement);
  expect(core.dashboard.queries?.find(query => query.name === requirement.source)).toBeDefined();
  expect(pageChunks.get('home')?.queries?.find(query => query.name === requirement.source)).toBeDefined();
  expect(navigationIndicatorSourceNames(input.dashboard.pages)).toEqual([requirement.source]);
  expect(pageCapabilityAvailable(pageDefinition)).toBe(true);
  vi.stubGlobal('__DASHBOARD_ADMIN__', false);
  expect(pageCapabilityAvailable({ requires: { ...requirement, capability: 'admin' } })).toBe(false);
  expect(pageSourceAvailable(pageDefinition, evidence('available'))).toBe(true);
});

it('validates declared sources and rejects unknown sources or empty requirements', () => {
  const validate = (/** @type {{ languageVersion: string, dashboard: unknown }} */ document) => validateDashboardDocument(JSON.stringify({
    'language-version': document.languageVersion, dashboard: document.dashboard
  }));
  expect(validate(input).errors).toEqual([]);
  expect(validate(input).ok).toBe(true);
  for (const requires of [{ source: 'unknown-source' }, {}, { capability: 'unknown' }]) {
    const invalid = { ...input, dashboard: {
      ...input.dashboard, pages: [input.dashboard.pages[0], { ...pageDefinition, requires }]
    } };
    expect(validate(invalid).ok).toBe(false);
  }
});
