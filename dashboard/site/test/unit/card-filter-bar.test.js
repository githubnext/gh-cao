import { afterEach, describe, expect, it, vi } from 'vitest';
import { captureCardFilterState, renderCardFilterBar, restoreCardFilterState } from '../../src/components/card-filter-bar.js';
import { compileDashboardViewPayloadQueries, dashboardViewAliasName } from '../../src/data/queries/view-payload-compiler.js';
import { processDataRequest } from '../../src/data-worker.js';
import { dashboardPageLazySourceNames, dashboardPageSourceNames } from '../../src/dashboard-chunks.js';
import { dashboardPagePaginatedSourceBindings, renderDashboard } from '../../src/presenter.js';
import { dashboardViewSourceNames, normalizeViewFilters } from '../../src/view-filter-contract.js';
import { validateDashboardDocument } from '../../src/validator.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import contract from '../fixtures/card-filter-contract.json' with { type: 'json' };

const dashboard = authoritativeDashboard.dashboard;
const page = dashboard.pages.find((/** @type {{ id: string }} */ entry) => entry.id === contract.page);
const view = page.definition.views.find((/** @type {{ id: string }} */ entry) => entry.id === contract.view);
/** @type {import('../../src/presenter.js').SourceMetadata} */
const metadata = {
  'source-id': 'card-filter-fixture', 'source-kind': 'fixture',
  'as-of': '2026-10-04T00:00:00Z', 'retrieved-at': '2026-10-04T00:00:00Z',
  availability: 'available', completeness: 'complete', freshness: 'fresh'
};
/** @param {string} name @param {Record<string, unknown>[]} rows */
const source = (name, rows) => ({ source: name, rows, metadata });
const canonical = {
  issues: source('issues', contract.issues.map((row) => ({
    ...row, organization: 'octo', repository: 'repo', workflow: 'worker.md', run: '1', 'run-attempt': '1'
  }))),
  runs: source('runs', [])
};

/** @param {import('../../src/data/queries/view-payload-compiler.js').GlobalQueryContext} [queryContext] */
function payload(queryContext) {
  const compiled = compileDashboardViewPayloadQueries(page, page.id, {
    queries: dashboard.queries, viewId: view.id, queryContext
  });
  const results = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
    operation: 'execute-dashboard-queries', queries: compiled.queries, sourceNames: compiled.aliases, sources: canonical
  }));
  return Object.fromEntries(dashboardViewSourceNames(view).map((name, index) => [
    name, results[dashboardViewAliasName(page.id, view, 2, name, index)]
  ]));
}

afterEach(() => document.body.replaceChildren());

describe('declarative card filters', () => {
  it('keeps the Issues view and reusable card list aligned with the contract', () => {
    expect(view['filter-bar']).toEqual(contract['filter-bar']);
    expect(dashboard.views.find((/** @type {{ id: string }} */ entry) => entry.id === 'issues')['filter-bar'])
      .toEqual(contract['filter-bar']);
    const documentModel = { languageVersion: authoritativeDashboard['language-version'], dashboard };
    expect(dashboardPageSourceNames(documentModel, page.id, 'card'))
      .toEqual(['issue-safe-outputs', 'issue-safe-output-label-options', 'issue-status-label-options', 'dashboard-data-freshness']);
    expect(dashboardPageLazySourceNames(documentModel, page.id)).toEqual(['issue-safe-outputs']);
    expect(Object.keys(dashboardPagePaginatedSourceBindings(documentModel, page.id)))
      .toEqual([dashboardViewAliasName(page.id, view, 2, 'issue-safe-outputs')]);
  });

  it('produces sorted distinct label options through the worker query boundary', () => {
    const results = payload();
    for (const name of dashboardViewSourceNames(view)) expect(results[name].metadata.availability).toBe('available');
    expect(results['issue-safe-output-label-options'].rows.map((row) => row['safe-output-type']))
      .toEqual(['create_issue', 'update_issue']);
    expect(results['issue-status-label-options'].rows.map((row) => row['issue-status-detail']))
      .toEqual(['Closed: Completed', 'Open', 'Unknown']);
  });

  it('applies exact OR-within-field and AND-between-field filters only to the owning view', () => {
    const results = payload({ viewFilters: { [view.id]: {
      'safe-output-type': ['create_issue'], 'issue-status-detail': ['Open', 'Unknown']
    } } });
    expect(results['issue-safe-outputs'].rows.map((row) => row['event-summary'])).toEqual(['Open issue']);
    expect(results['issue-status-label-options'].rows).toEqual(payload()['issue-status-label-options'].rows);
    const ignored = payload({ viewFilters: { unrelated: { 'issue-status-detail': ['Open'] } } });
    expect(ignored['issue-safe-outputs'].rows).toHaveLength(3);
    const empty = payload({ viewFilters: { [view.id]: { 'issue-status-detail': ['Closed'] } } });
    expect(empty['issue-safe-outputs'].metadata.availability).toBe('empty');
    expect(empty['issue-safe-outputs'].rows).toEqual([]);
    expect(empty['issue-status-label-options'].rows).toHaveLength(3);
  });

  it('normalizes serializable selections without accepting malformed entries', () => {
    expect(normalizeViewFilters({ view: { field: ['Open', 1, 'Open'] }, invalid: 3 }))
      .toEqual({ view: { field: ['Open'] } });
    expect(normalizeViewFilters({ view: { field: [] } })).toBeUndefined();
  });

  it('scopes options to global filters and time without result search or ordering', () => {
    const results = payload({
      filters: { 'safe-output-type': ['create_issue'] },
      search: { fields: ['event-summary'], query: 'Open issue' },
      orderBy: [{ field: 'issue-status-detail', direction: 'desc' }]
    });
    expect(results['issue-safe-outputs'].rows.map((row) => row['event-summary'])).toEqual(['Open issue']);
    expect(results['issue-safe-output-label-options'].rows.map((row) => row['safe-output-type']))
      .toEqual(['create_issue']);
    expect(results['issue-status-label-options'].rows.map((row) => row['issue-status-detail']))
      .toEqual(['Closed: Completed', 'Open']);
    const timed = payload({
      filters: { 'safe-output-type': ['create_issue'] },
      timeWindow: { start: '2026-10-02T00:00:00Z', end: '2026-10-04T00:00:00Z' }
    });
    expect(timed['issue-status-label-options'].rows.map((row) => row['issue-status-detail']))
      .toEqual(['Closed: Completed']);
  });

  it('filters before query limits and fails closed when a selected row field is missing', () => {
    const scopedView = {
      id: 'cards', mark: 'list', data: { source: 'bounded-issues' },
      'filter-bar': { filters: [{
        id: 'labels', label: 'Labels', groups: [{
          label: 'Status', field: 'issue-state', source: 'status-options', 'value-field': 'issue-state'
        }]
      }] }
    };
    const queries = [
      { name: 'bounded-issues', from: 'issues', limit: 1 },
      { name: 'status-options', from: 'issues', aggregate: {
        by: ['issue-state'], values: [{ field: 'event', as: 'items', reducer: 'count' }]
      } }
    ];
    const compiled = compileDashboardViewPayloadQueries({ views: [scopedView] }, 'test', {
      queries, queryContext: { viewFilters: { cards: { 'issue-state': ['OPEN'] } } }
    });
    const results = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries', queries: compiled.queries, sourceNames: compiled.aliases,
      sources: { issues: source('issues', [
        { event: 'first', 'issue-state': 'CLOSED' }, { event: 'missing' }, { event: 'match', 'issue-state': 'OPEN' }
      ]) }
    }));
    expect(results[compiled.aliases[0]].rows).toEqual([{ event: 'match', 'issue-state': 'OPEN' }]);
  });

  it('does not synthesize filter options when a canonical dependency is missing', () => {
    const compiled = compileDashboardViewPayloadQueries(page, page.id, {
      queries: dashboard.queries, viewId: view.id
    });
    const results = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries', queries: compiled.queries, sourceNames: compiled.aliases, sources: {}
    }));
    for (const alias of compiled.aliases) {
      expect(results[alias].metadata.availability).toBe('unavailable');
      expect(results[alias].rows).toEqual([]);
    }
  });

  const bar = contract['filter-bar'];
  const invalidFilterBars = [
    { ...bar, unknown: true }, { ...bar, filters: [] },
    ...[
      { source: 'missing-source' }, { field: 'missing-field' },
      { 'value-field': 'missing-field' }, { field: 'issue-status-detail' }
    ].map((invalid) => ({
      filters: [
        { ...bar.filters[0], groups: [{ ...bar.filters[0].groups[0], ...invalid }] },
        bar.filters[1]
      ]
    }))
  ];

  it.each(invalidFilterBars)('rejects invalid filter bar %#', (invalid) => {
    const model = structuredClone(authoritativeDashboard);
    const issues = model.dashboard.pages.find((/** @type {{ id: string }} */ entry) => entry.id === page.id);
    issues.definition.views[2]['filter-bar'] = invalid;
    expect(validateDashboardDocument(JSON.stringify(model)).ok).toBe(false);
  });

  it('requires a stable owning view id for filter selections', () => {
    const model = structuredClone(authoritativeDashboard);
    const issues = model.dashboard.pages.find((/** @type {{ id: string }} */ entry) => entry.id === page.id);
    delete issues.definition.views[2].id;
    expect(validateDashboardDocument(JSON.stringify(model)).ok).toBe(false);
  });
});

describe('card filter controls', () => {
  /** @param {Partial<Parameters<typeof renderCardFilterBar>[0]>} [overrides] */
  function render(overrides = {}) {
    const onChange = vi.fn();
    const root = renderCardFilterBar({
      pageId: page.id, viewId: view.id, controls: contract['filter-bar'].filters,
      sources: payload(), onChange, ...overrides
    });
    document.body.append(root);
    return { root, onChange };
  }

  it('renders grouped accessible options, applies selections, and clears only local filters', () => {
    const { root, onChange } = render();
    expect([...root.querySelectorAll('legend')].map((node) => node.textContent)).toEqual(['Safe output', 'Status']);
    const statusMenu = /** @type {HTMLDetailsElement} */ (root.querySelectorAll('details')[1]);
    const summary = statusMenu.querySelector('summary');
    expect(summary?.textContent).toContain('Status');
    const label = [...root.querySelectorAll('label')].find((node) => node.textContent === 'Open');
    label?.querySelector('input')?.click();
    expect(onChange).not.toHaveBeenCalled();
    expect(summary?.getAttribute('aria-label')).toBe('Status, 1 selected');
    expect(root.querySelector('summary')?.getAttribute('aria-label')).toBe('Labels');
    statusMenu.querySelector('button')?.dispatchEvent(new MouseEvent('click'));
    expect(onChange).toHaveBeenCalledWith({ 'issue-status-detail': ['Open'] });
    expect(document.activeElement).toBe(summary);
    root.querySelector('.card-filter-clear')?.dispatchEvent(new MouseEvent('click'));
    expect(onChange).toHaveBeenLastCalledWith({});
    expect([...root.querySelectorAll('input')].every((input) => !input.checked)).toBe(true);
  });

  it('shows honest loading, empty, partial, invalid and unavailable option states', () => {
    const name = 'issue-safe-output-label-options';
    for (const [sources, pending, message] of [
      [{}, true, 'Loading filter options...'],
      [{}, false, 'Filter options unavailable.'],
      [{ [name]: source(name, []) }, false, 'No options available.'],
      [{ [name]: { ...source(name, []), metadata: { ...metadata, availability: 'unavailable' } } }, false, 'Filter options unavailable.'],
      [{ [name]: source(name, [{ 'safe-output-type': 12 }]) }, false, 'Invalid filter options.'],
      [{ [name]: { ...source(name, [{ 'safe-output-type': 'create_issue' }]), metadata: { ...metadata, completeness: 'partial' } } }, false, 'evidence is incomplete']
    ]) {
      const { root } = render({
        sources: /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (sources),
        pending: Boolean(pending)
      });
      expect(root.textContent).toContain(message);
    }
  });

  it('dismisses with Escape and releases document listeners after detachment', async () => {
    const { root } = render();
    const menu = /** @type {HTMLDetailsElement} */ (root.querySelector('details'));
    menu.open = true;
    root.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(menu.open).toBe(false);
    expect(document.activeElement).toBe(root.querySelector('summary'));
    await Promise.resolve();
    root.remove();
    await Promise.resolve();
    menu.open = true;
    document.body.click();
    expect(menu.open).toBe(true);
  });

  it('preserves unapplied selections, expanded menus and scroll without applying a query', () => {
    const { root, onChange } = render();
    const menu = /** @type {HTMLDetailsElement} */ (root.querySelector('details'));
    const popover = /** @type {HTMLElement} */ (root.querySelector('.card-filter-popover'));
    root.querySelector('input')?.click();
    menu.open = true;
    popover.scrollTop = 120;
    const snapshots = captureCardFilterState(document.body);
    root.remove();
    const replacement = render();
    restoreCardFilterState(document.body, snapshots);
    expect(replacement.root.querySelector('input')?.checked).toBe(true);
    expect(replacement.root.querySelector('details')?.open).toBe(true);
    expect(replacement.root.querySelector('summary')?.getAttribute('aria-expanded')).toBe('true');
    expect(replacement.root.querySelector('.card-filter-popover')?.scrollTop).toBe(120);
    expect(onChange).not.toHaveBeenCalled();
    expect(replacement.onChange).not.toHaveBeenCalled();
  });

  it('stays silent by default and logs only scalar metadata under its predictable category', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => '?debug=card-filter-bar', output })
      };
    });
    vi.resetModules();
    const { renderCardFilterBar: renderWithDebug } = await import('../../src/components/card-filter-bar.js');

    const onChange = vi.fn();
    const root = renderWithDebug({
      pageId: page.id, viewId: view.id, controls: contract['filter-bar'].filters, sources: payload(), onChange
    });
    document.body.append(root);

    const label = [...root.querySelectorAll('label')].find((node) => node.textContent === 'Open');
    label?.querySelector('input')?.click();
    const details = /** @type {HTMLDetailsElement} */ (root.querySelectorAll('details')[1]);
    details.open = true;
    details.dispatchEvent(new Event('toggle'));
    details.querySelector('button')?.dispatchEvent(new MouseEvent('click'));
    root.querySelector('.card-filter-clear')?.dispatchEvent(new MouseEvent('click'));

    expect(output.debug).toHaveBeenCalledWith('[cao:card-filter-bar]', { event: 'menu-opened', viewId: view.id, controlId: 'status' });
    expect(output.debug).toHaveBeenCalledWith('[cao:card-filter-bar]',
      { event: 'filters-applied', viewId: view.id, controlId: 'status', count: 1 });
    expect(output.debug).toHaveBeenCalledWith('[cao:card-filter-bar]', { event: 'filters-cleared', viewId: view.id });

    for (const call of output.debug.mock.calls) {
      const metadata = call[1];
      expect(Object.values(metadata).every((value) => typeof value !== 'object')).toBe(true);
    }

    vi.doUnmock('../../src/debug.js');
    vi.resetModules();
  });

  it('is disabled by default (no debug output) when the debug query is absent', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => '', output })
      };
    });
    vi.resetModules();
    const { renderCardFilterBar: renderWithoutDebug } = await import('../../src/components/card-filter-bar.js');

    const onChange = vi.fn();
    const root = renderWithoutDebug({
      pageId: page.id, viewId: view.id, controls: contract['filter-bar'].filters, sources: payload(), onChange
    });
    document.body.append(root);

    const label = [...root.querySelectorAll('label')].find((node) => node.textContent === 'Open');
    label?.querySelector('input')?.click();
    const details = /** @type {HTMLDetailsElement} */ (root.querySelector('details'));
    details.open = true;
    details.dispatchEvent(new Event('toggle'));
    root.querySelector('.card-filter-popover button')?.dispatchEvent(new MouseEvent('click'));
    root.querySelector('.card-filter-clear')?.dispatchEvent(new MouseEvent('click'));

    expect(output.debug).not.toHaveBeenCalled();

    vi.doUnmock('../../src/debug.js');
    vi.resetModules();
  });

  it('keeps the filter bar and clear action available for an empty card list', () => {
    const results = payload({ viewFilters: { [view.id]: { 'issue-status-detail': ['absent'] } } });
    const listView = dashboard.views.find((/** @type {{ id: string }} */ entry) => entry.id === 'issues');
    const root = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'fixture', title: 'Issues', 'card-templates': dashboard['card-templates'],
          pages: [{ id: 'issues', title: 'Issues', kind: 'custom', views: [listView] }]
        }
      },
      sources: results
    });
    document.body.append(root);
    expect(root.querySelector('.card-filter-bar')).not.toBeNull();
    expect(root.textContent).toContain('No issue safe outputs were retained');
  });

  it('places card filters and the prompt inside the card list toolbar', async () => {
    const root = renderDashboard({
      document: {
        languageVersion: '0.1.0',
        dashboard: {
          id: 'fixture', title: 'Issues', 'card-templates': dashboard['card-templates'],
          pages: [{ id: 'issues', title: 'Issues', kind: 'custom', views: [{ ...view, prompt: 'always' }] }]
        }
      },
      sources: payload()
    });
    document.body.append(root);
    root.dispatchEvent(new CustomEvent('dashboard-query-context-change', {
      detail: { pageId: 'issues', queryContext: { viewMode: 'card' } }
    }));
    await vi.waitFor(() => expect(root.querySelector('[data-page-id="issues"]')?.getAttribute('data-view-mode')).toBe('card'));
    const cardView = root.querySelector(`[data-view-id="${view.id}"]`);
    const toolbar = cardView?.querySelector('.mobile-table-card-toolbar');
    expect(toolbar?.querySelector('.card-filter-bar')).not.toBeNull();
    expect(toolbar?.querySelector('.semantic-prompt-action .table-intent-button')).not.toBeNull();
    expect(cardView?.querySelector(':scope > .semantic-prompt-action')).toBeNull();
    expect(toolbar?.nextElementSibling?.classList.contains('mobile-table-card-list-items')).toBe(true);
  });
});
