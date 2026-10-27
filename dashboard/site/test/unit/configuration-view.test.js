// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import maintenanceNavigationContract from '../fixtures/maintenance-navigation-contract.json' with { type: 'json' };
import { processDataRequest } from '../../src/data-worker.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';

const queryCanonicalDatabaseDiagnostics = vi.hoisted(() => vi.fn().mockResolvedValue({
  schemaVersion: 13,
  counts: {
    campaigns: 0,
    repositories: 0,
    workflows: 0,
    runs: 0,
    domains: 0,
    tools: 0,
    audits: 0,
    issues: 0
  },
  relationshipErrors: [],
  duplicateRecordIds: {
    campaigns: [],
    repositories: [],
    workflows: [],
    runs: [],
    domains: [],
    tools: [],
    audits: [],
    issues: []
  }
}));
vi.mock('../../src/data-processor.js', async () => ({
  ...await vi.importActual('../../src/data-processor.js'),
  queryCanonicalDatabaseDiagnostics
}));

import { renderConfigurationView } from '../../src/components/configuration-view.js';
import { renderDataView } from '../../src/components/data-view.js';
import { setDeclaredCliActions } from '../../src/components/cli-actions.js';
import { renderUiElementAsync } from '../../src/components/ui-elements.js';
import { setAutomaticDashboardDataUpdatesEnabled } from '../../src/dashboard-data-updates.js';
import { fullDebugUrl, isDebugEnabled } from '../../src/debug.js';

const metadata = /** @type {import('../../src/presenter.js').SourceMetadata} */ ({
  'source-id': 'configuration-fixture',
  'source-kind': 'fixture',
  'as-of': '2026-09-05T09:00:00Z',
  'retrieved-at': '2026-09-05T09:01:00Z',
  completeness: 'complete',
  freshness: 'fresh',
  availability: 'available'
});

/** @param {Record<string, unknown>} row */
function context(row) {
  return /** @type {import('../../src/components/ui-elements.js').ElementRenderContext} */ ({
    pageId: 'configuration',
    title: 'Control policy',
    description: 'Explained configuration.',
    sourceNames: ['configuration-policy'],
    sources: {
      'configuration-policy': {
        source: 'configuration-policy',
        rows: [row],
        metadata
      }
    },
    contextDetails: [],
    headingTag: 'h3'
  });
}

describe('Configuration dashboard view', () => {
  it('resolves repository home drills through the worker view-payload boundary and fails closed without evidence', () => {
    const dashboard = authoritativeDashboard.dashboard;
    const page = dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === 'maintenance');
    const payload = compileDashboardViewPayloadQueries(page, page.id, {
      viewId: 'maintenance-repositories', sourceNames: ['maintenance-repositories'], queries: dashboard.queries
    });
    expect(payload.aliases).toHaveLength(1);
    for (const available of [true, false]) {
      const results = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
        operation: 'execute-dashboard-queries',
        queries: payload.queries,
        sourceNames: payload.aliases,
        sources: available ? {
          workflows: { source: 'workflows', rows: maintenanceNavigationContract['repository-workflows'], metadata }
        } : {}
      }));
      const result = results[payload.aliases[0]];
      expect(result.metadata.availability).toBe(available ? 'available' : 'unavailable');
      if (available) {
        expect(result.rows).toHaveLength(maintenanceNavigationContract['repository-homes'].length);
        expect(result.rows).toMatchObject(maintenanceNavigationContract['repository-homes']);
        vi.stubGlobal('__CLI_QUEUE__', true);
        setDeclaredCliActions(dashboard['cli-actions']);
        try {
          const view = page.views[0];
          const rendered = renderDataView('list', {
            pageId: page.id, title: view.title, view,
            sourceName: view.data.source, rows: result.rows,
            cardTemplates: Object.fromEntries(dashboard['card-templates'].map(
              (/** @type {{ id: string }} */ template) => [template.id, template]
            )),
            metadata: result.metadata, contextDetails: [], headingTag: 'h3',
            prepareTableRows: (rows) => rows, buildChartPoints: () => [],
            prepareChartPoints: () => [], toText: String
          });
          const cards = [...(rendered?.querySelectorAll('.entity-card-list-card') ?? [])];
          expect(cards).toHaveLength(maintenanceNavigationContract['repository-homes'].length);
          expect(cards.map((card) => card.querySelectorAll('.entity-card-list-actions .cli-action-trigger').length))
            .toEqual([0, 0, 0, 1]);
        } finally {
          setDeclaredCliActions([]);
          vi.unstubAllGlobals();
        }
      } else {
        expect(result.rows).toEqual([]);
      }
    }
  });

  it('renders campaign and standalone source updates only when exact provenance is actionable', () => {
    const dashboard = authoritativeDashboard.dashboard;
    const page = dashboard.pages.find((/** @type {{ id: string }} */ candidate) =>
      candidate.id === 'maintenance-campaigns');
    const templates = Object.fromEntries(dashboard['card-templates'].map(
      (/** @type {{ id: string }} */ template) => [template.id, template]
    ));
    vi.stubGlobal('__CLI_QUEUE__', true);
    setDeclaredCliActions(dashboard['cli-actions']);
    try {
      /** @type {Record<string, number[]>} */
      const expectedActions = {
        'maintenance-campaigns': [1, 2],
        'maintenance-standalone-workflows': [1, 0, 0]
      };
      for (const view of page.views) {
        const payload = compileDashboardViewPayloadQueries(page, page.id, {
          viewId: view.id, sourceNames: [view.data.source], queries: dashboard.queries
        });
        const results = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
          processDataRequest({
            operation: 'execute-dashboard-queries',
            queries: payload.queries,
            sourceNames: payload.aliases,
            sources: {
              campaigns: { source: 'campaigns', rows: maintenanceNavigationContract.campaigns, metadata },
              workflows: { source: 'workflows', rows: maintenanceNavigationContract['standalone-workflows'], metadata }
            }
          })
        );
        const result = results[payload.aliases[0]];
        const rendered = renderDataView('list', {
          pageId: page.id, title: view.title, view,
          sourceName: view.data.source, rows: result.rows,
          cardTemplates: templates,
          metadata: result.metadata, contextDetails: [], headingTag: 'h3',
          prepareTableRows: (rows) => rows, buildChartPoints: () => [],
          prepareChartPoints: () => [], toText: String
        });
        expect([...(rendered?.querySelectorAll('.entity-card-list-card') ?? [])]
          .map((card) => card.querySelectorAll('.entity-card-list-actions .cli-action-trigger').length))
          .toEqual(expectedActions[view.id]);
      }

      const standalonePayload = compileDashboardViewPayloadQueries(page, page.id, {
        viewId: 'maintenance-standalone-workflows',
        sourceNames: ['maintenance-standalone-workflows'],
        queries: dashboard.queries
      });
      const standalone = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
        processDataRequest({
          operation: 'execute-dashboard-queries',
          queries: standalonePayload.queries,
          sourceNames: standalonePayload.aliases,
          sources: {
            workflows: { source: 'workflows', rows: maintenanceNavigationContract['standalone-workflows'], metadata }
          }
        })
      )[standalonePayload.aliases[0]].rows;
      expect(standalone.map((row) => ({
        workflow: row['workflow-name'],
        state: row['workflow-update-state'],
        reason: row['workflow-update-status-reason'],
        action: row['workflow-update-action-visible']
      }))).toEqual([
        {
          workflow: 'Review',
          state: 'Update available',
          reason: 'Installed and upstream source revisions are available.',
          action: true
        },
        {
          workflow: 'Incomplete',
          state: 'Update status unavailable',
          reason: 'Latest upstream source revision unavailable.',
          action: false
        },
        {
          workflow: 'Local',
          state: 'Local workflow',
          reason: 'No upstream source coordinate was retained; update manually.',
          action: false
        }
      ]);
    } finally {
      setDeclaredCliActions([]);
      vi.unstubAllGlobals();
    }
  });

  it('disables hourly data downloads when Periodic Background Sync is unsupported', () => {
    localStorage.clear();
    const rendered = renderConfigurationView(context({
      document: { version: 1 },
      raw: '',
      diagnostics: []
    }));
    if (!rendered) throw new Error('configuration view did not render');

    const checkbox = rendered.querySelector('#configuration-automatic-dashboard-data-updates');
    if (!(checkbox instanceof HTMLInputElement)) throw new Error('automatic update checkbox did not render');
    document.body.append(rendered);
    expect(checkbox.disabled).toBe(true);
    expect(rendered.querySelector('.configuration-browser-setting-status')?.textContent)
      .toContain('Periodic Background Sync is not supported');
  });

  it('renders browser settings without local database queries', () => {
    localStorage.clear();
    const rendered = renderConfigurationView(context({ document: { version: 1 }, raw: '', diagnostics: [] }));

    if (!rendered) throw new Error('configuration view did not render');

    const theme = rendered.querySelector('.theme-control-options');
    expect(rendered.children[1]?.querySelector('#configuration-appearance-heading')?.textContent).toBe('Appearance');
    expect(theme?.getAttribute('role')).toBe('group');
    expect(theme?.getAttribute('aria-label')).toBe('Theme');
    expect([...rendered.querySelectorAll('[data-theme-value]')].map((button) => button.textContent))
      .toEqual(['System', 'Light', 'Dark']);
    expect(rendered.querySelector('.configuration-database-counts')).toBeNull();
    expect(rendered.querySelector('.reset-dashboard-trigger')).not.toBeNull();
    expect(rendered.querySelector('a[href="#page-indexing"]')).toBeNull();
    const debugSettings = rendered.querySelector('.configuration-debug-settings');
    expect(debugSettings).toBe(rendered.lastElementChild);
    const debugLink = debugSettings?.querySelector('a');
    expect(debugLink?.textContent).toBe('Relaunch with debugging');
    expect(debugLink?.getAttribute('href')).toBe(fullDebugUrl());
  });

  it('copies captured console logs', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText }
    });
    const rendered = renderConfigurationView(context({
      document: { version: 1 },
      raw: '',
      diagnostics: []
    }));
    const button = rendered?.querySelector('.configuration-debug-settings button');
    if (!(button instanceof HTMLButtonElement)) throw new Error('copy console logs button did not render');

    button.click();

    await vi.waitFor(() => expect(writeText).toHaveBeenCalledOnce());
    expect(writeText.mock.calls[0][0]).toBe('No console entries captured.');
    await vi.waitFor(() => expect(rendered?.querySelector('.configuration-debug-settings output')?.textContent)
      .toBe('Console logs copied.'));
  });

  it('hides debugging controls in installed app mode', () => {
    Object.defineProperty(navigator, 'standalone', { configurable: true, value: true });
    const rendered = renderConfigurationView(context({
      document: { version: 1 },
      raw: '',
      diagnostics: []
    }));

    expect(rendered?.querySelector('.configuration-debug-settings')).toBeNull();
    delete /** @type {Navigator & { standalone?: boolean }} */ (navigator).standalone;
  });

  it('renders repository actions when the settings view is activated lazily', () => {
    setDeclaredCliActions([{
      id: 'update-repository',
      label: 'Update',
      icon: 'sync',
      command: 'gh aw update --repo {{repository}}',
      placement: 'settings'
    }], {
      canExecute: false,
      templateValues: { repository: 'octo/example' }
    });

    const rendered = renderConfigurationView(context({
      document: { version: 1 },
      raw: '',
      diagnostics: []
    }));

    expect(rendered?.querySelector('.cli-actions-settings')).not.toBeNull();
    expect(rendered?.querySelector('.cli-action-trigger')?.textContent).toContain('Update');
    rendered?.querySelector('.cli-action-trigger')?.dispatchEvent(new MouseEvent('click'));
    expect(rendered?.querySelector('.cli-action-command')?.textContent)
      .toBe('gh aw update --repo octo/example');
    setDeclaredCliActions([]);
  });

  it('keeps repository actions available when policy data is unavailable', () => {
    setDeclaredCliActions([{
      id: 'upgrade-repository',
      label: 'Upgrade',
      icon: 'download',
      command: 'gh aw upgrade --repo {{repository}}',
      placement: 'settings'
    }], {
      templateValues: { repository: 'octo/example' }
    });
    const rendered = renderConfigurationView({
      ...context({}),
      sources: {}
    });

    expect(rendered?.querySelector('.cli-action-trigger')?.textContent).toContain('Upgrade');
    expect(rendered?.textContent).toContain('The policy cannot be edited');
    setDeclaredCliActions([]);
  });

  it('reflects background registration failures while the setting is mounted', () => {
    localStorage.setItem('central-agentic-ops.dashboard.automatic-data-updates', 'true');
    localStorage.setItem('central-agentic-ops.dashboard.background-data-updates-active', 'true');
    const rendered = renderConfigurationView(context({
      document: { version: 1 },
      raw: '',
      diagnostics: []
    }));
    if (!rendered) throw new Error('configuration view did not render');
    document.body.append(rendered);
    const checkbox = /** @type {HTMLInputElement | null} */ (
      rendered.querySelector('#configuration-automatic-dashboard-data-updates')
    );

    setAutomaticDashboardDataUpdatesEnabled(false);

    expect(checkbox?.checked).toBe(false);
    expect(rendered.querySelector('.configuration-browser-setting-status')?.textContent)
      .toContain('Periodic Background Sync is not supported');
    rendered.remove();
  });

  it('stops reacting to background update status once the setting detaches from the document', async () => {
    localStorage.setItem('central-agentic-ops.dashboard.automatic-data-updates', 'true');
    localStorage.setItem('central-agentic-ops.dashboard.background-data-updates-active', 'true');
    const rendered = renderConfigurationView(context({
      document: { version: 1 },
      raw: '',
      diagnostics: []
    }));
    if (!rendered) throw new Error('configuration view did not render');
    document.body.append(rendered);
    const checkbox = /** @type {HTMLInputElement | null} */ (
      rendered.querySelector('#configuration-automatic-dashboard-data-updates')
    );

    rendered.remove();
    // Let the MutationObserver microtask backing createFactoryScope run.
    await new Promise((resolve) => queueMicrotask(() => resolve(undefined)));

    setAutomaticDashboardDataUpdatesEnabled(false);

    // The effect stopped when the setting detached, so a status change no
    // longer updates the detached checkbox.
    expect(checkbox?.checked).toBe(true);
  });

  it('exposes Settings in the bottom management navigation without a chart', () => {
    const dashboard = authoritativeDashboard.dashboard;
    const page = dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === 'configuration');
    const manageNavigation = dashboard.navigation.find((/** @type {{ label?: string }} */ candidate) => candidate.label === 'Maintenance');

    expect(page.title).toBe('Settings');
    expect(page.icon).toBe('gear');
    expect(manageNavigation.placement).toBe('bottom');
    expect(manageNavigation.pages.at(-1)).toBe('configuration');
    expect(page.views.every((/** @type {{ mark: string }} */ view) => view.mark !== 'chart')).toBe(true);
    expect(page.views).toHaveLength(1);
    expect(page.views[0].id).toBe('configuration-policy');
    expect(page.views[0].data.sources).toEqual(['configuration-policy']);
    expect(dashboard['cli-actions']
      .filter((/** @type {{ id: string }} */ action) => ['update-repository', 'upgrade-repository'].includes(action.id))
      .every((/** @type {{ placement: string }} */ action) => action.placement === 'view')).toBe(true);
  });

  it('separates CAO package and compiler maintenance inventory', () => {
    const dashboard = authoritativeDashboard.dashboard;
    expect(dashboard['cli-actions'].find((/** @type {{ id: string }} */ action) =>
      action.id === 'upgrade-target-repository')?.label).toBe('Upgrade');
    const page = dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === 'maintenance');
    const campaigns = dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === 'maintenance-campaigns');
    const views = [...campaigns.views, ...page.views];
    const manageNavigation = dashboard.navigation.find((/** @type {{ label?: string }} */ candidate) => candidate.label === 'Maintenance');

    expect(page.title).toBe('Compiler upgrades');
    expect(campaigns.title).toBe('Workflow updates');
    expect(page.route).toEqual({ tab: 'repositories', tabs: maintenanceNavigationContract.tabs });
    expect(campaigns.route).toEqual({
      'navigation-page': 'maintenance', tab: 'campaigns', tabs: maintenanceNavigationContract.tabs
    });
    expect(page.views).toHaveLength(1);
    expect(campaigns.views).toHaveLength(2);
    expect([page.views[0].data.source, ...campaigns.views.map((/** @type {{ data: { source: string } }} */ view) => view.data.source)])
      .toEqual(maintenanceNavigationContract.sources);
    expect(manageNavigation.pages).toContain('maintenance');
    expect(manageNavigation.pages).not.toContain('maintenance-campaigns');
    expect(page.description).toBe('Review retained gh-aw compiler evidence by repository before applying upgrades.');
    expect(page.acceptance).toContain('diagnostic reason');
    expect(page.acceptance).toContain('known outdated evidence');
    expect(page['filter-bar']).toBeUndefined();
    expect(views.map((/** @type {{ data: { source: string } }} */ view) => view.data.source))
      .toEqual(['maintenance-campaign-packages', 'maintenance-standalone-workflows', 'maintenance-repositories']);
    expect(views.map((/** @type {{ mark: string }} */ view) => view.mark)).toEqual(['list', 'list', 'list']);
    expect(views.map((/** @type {{ list: { style: string, card: string } }} */ view) => view.list))
      .toEqual([
        { style: 'entity-cards', card: 'maintenance-campaign', icon: 'workflow' },
        { style: 'entity-cards', card: 'maintenance-standalone-workflow', icon: 'workflow' },
        { style: 'entity-cards', card: 'maintenance-repository', icon: 'repo', actions: ['add-repository'] }
      ]);
    expect(views[0].encoding.columns.map((/** @type {{ field: string }} */ column) => column.field))
      .toEqual([
        'campaign-name',
        'campaign-version',
        'campaign-current-version',
        'campaign-update-state',
        'campaign-registration'
      ]);
    expect(views[1].encoding.columns.map((/** @type {{ field: string }} */ column) => column.field))
      .toEqual([
        'workflow-name',
        'workflow-source',
        'workflow-source-version',
        'workflow-source-current-version',
        'workflow-update-state',
        'workflow-update-status-reason'
      ]);
    expect(views[2].encoding.columns.map((/** @type {{ field: string }} */ column) => column.field))
      .toEqual([
        'repository',
        'gh-aw-version',
        'gh-aw-current-version',
        'upgrade-state',
        'upgrade-status-reason'
      ]);
    expect(views.map((/** @type {{ title?: string }} */ view) => view.title))
      .toEqual(['Campaign packages', 'Standalone workflows', 'Repository compiler status']);
    expect(views.some((/** @type {{ disclosure?: string }} */ view) => view.disclosure !== undefined)).toBe(false);
    const templates = new Map(dashboard['card-templates'].map((/** @type {{ id: string }} */ template) => [template.id, template]));
    expect(templates.get('maintenance-campaign')['detail-labels']).toBe('visible');
    expect(templates.get('maintenance-campaign').details).toContainEqual({
      field: 'campaign-update-state',
      title: 'Update confidence',
      display: 'status'
    });
    expect(templates.get('maintenance-repository')['detail-labels']).toBe('visible');
    expect(templates.get('maintenance-repository').drill).toEqual(maintenanceNavigationContract['repository-drill']);
    expect(templates.get('maintenance-campaign').actions).toEqual([
      {
        action: 'disable-campaign',
        context: ['campaign'],
        when: { field: 'campaign-registration', equals: 'true' }
      },
      {
        action: 'update-campaign',
        context: ['campaign-source', 'campaign-name'],
        when: { field: 'campaign-update-action-visible', equals: true }
      },
      {
        action: 'enable-campaign',
        context: ['campaign'],
        when: { field: 'campaign-registration', equals: 'false' }
      }
    ]);
    expect(templates.get('campaign').actions).toEqual([
      {
        action: 'disable-campaign',
        context: ['campaign'],
        when: { field: 'registration', equals: 'true' }
      },
      {
        action: 'enable-campaign',
        context: ['campaign'],
        when: { field: 'registration', equals: 'false' }
      }
    ]);
    expect(templates.get('maintenance-repository').actions).toEqual([
      maintenanceNavigationContract['repository-upgrade-action']
    ]);
    expect(templates.get('maintenance-standalone-workflow').actions).toEqual([{
      action: 'update-workflow-source',
      context: ['workflow-source', 'workflow-name'],
      when: { field: 'workflow-update-action-visible', equals: true }
    }]);
    const compilerVersions = dashboard.pages.find((/** @type {{ id: string }} */ candidate) =>
      candidate.id === 'repository-settings').views[0];
    expect(compilerVersions.encoding.actions[0].when)
      .toEqual(maintenanceNavigationContract['repository-upgrade-action'].when);
    expect(dashboard['cli-actions']
      .find((/** @type {{ id: string }} */ action) => action.id === 'update-campaign')
      .command).toMatch(/^gh aw update https:\/\/github\.com\/\{\{campaign-source\}\} --repo \{\{cao-repository\}\} --pr-message '/);
    expect(dashboard['cli-actions']
      .find((/** @type {{ id: string }} */ action) => action.id === 'update-repository')
      .command).toMatch(/^gh aw update https:\/\/github\.com\/\{\{campaign-source\}\} --repo \{\{cao-repository\}\} --pr-message '/);
    expect(dashboard['cli-actions']
      .find((/** @type {{ id: string }} */ action) => action.id === 'update-workflow-source')
      .command).toMatch(/^gh aw update https:\/\/github\.com\/\{\{workflow-source\}\} --repo \{\{cao-repository\}\} --pr-message '/);
    expect(dashboard['cli-actions']
      .find((/** @type {{ id: string }} */ action) => action.id === 'update-campaign')
      .label).toBe('Update');
    expect(dashboard['cli-actions']
      .find((/** @type {{ id: string }} */ action) => action.id === 'disable-campaign')
      .label).toBe('Deactivate');
    expect(manageNavigation.placement).toBe('bottom');
    expect(manageNavigation.pages).toContain('maintenance');
  });

  it('wires the Settings view to a supported UI element', async () => {
    const dashboard = authoritativeDashboard.dashboard;
    const page = dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === 'configuration');
    const [view] = page.views;
    const rendered = await renderUiElementAsync(view.element, {
      ...context({ document: { version: 1 }, raw: '', diagnostics: [] }),
      title: view.title,
      description: view.description,
      sourceNames: view.data.sources,
      sources: context({ document: { version: 1 }, raw: '', diagnostics: [] }).sources
    });

    expect(rendered?.classList.contains('configuration-view')).toBe(true);
    expect(rendered?.textContent).toContain('Policy');
    expect(rendered?.textContent).not.toContain('Unsupported UI element');
  });

  it('renders cao.json entries as editable settings', () => {
    const rendered = renderConfigurationView(context({
      document: { version: 1, 'control-plane': { defaults: { mode: 'review' } } },
      raw: '',
      diagnostics: []
    }));
    if (!rendered) throw new Error('configuration view did not render');

    expect(rendered.querySelector('.configuration-editor')).not.toBeNull();
    expect(/** @type {HTMLInputElement | null} */ (rendered.querySelector('input[type="number"]'))?.value).toBe('1');
    expect(rendered.querySelector('select')?.value).toBe('review');
    expect(rendered.textContent).toContain('Sets the inherited execution mode.');
    expect(rendered.textContent).not.toContain('Suggested changes');
    expect(rendered.textContent).not.toContain('Raw JSON');
    expect(rendered.querySelector('.configuration-diagnostics-button')).not.toBeNull();
  });

  it('regenerates editable settings from valid raw JSON when the structured policy is missing', () => {
    const rendered = renderConfigurationView(context({
      document: null,
      raw: '{"version":1,"control-plane":{"defaults":{"mode":"review"}}}',
      diagnostics: []
    }));
    if (!rendered) throw new Error('configuration view did not render');

    expect(rendered.querySelector('.configuration-editor')).not.toBeNull();
    expect(rendered.querySelector('select')?.value).toBe('review');
    expect(rendered.textContent).not.toContain('The policy cannot be edited');
  });

  it('collects and copies full diagnostics', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText }
    });
    const rendered = renderConfigurationView(context({
      document: { version: 1 },
      raw: '',
      diagnostics: []
    }));
    if (!rendered) throw new Error('configuration view did not render');
    document.body.append(rendered);

    const button = rendered.querySelector('.configuration-diagnostics-button');
    if (!(button instanceof HTMLButtonElement)) throw new Error('diagnostics button did not render');
    button.click();

    await vi.waitFor(() => expect(writeText).toHaveBeenCalledOnce());
    expect(JSON.parse(writeText.mock.calls[0][0])).toEqual(expect.objectContaining({
      passed: false,
      database: expect.any(Object),
      ui: expect.any(Object)
    }));
    await vi.waitFor(() => expect(button.nextElementSibling?.textContent).toBe('Diagnostics copied.'));
  });

  it('defers settings inside collapsed groups until they are expanded', () => {
    const targets = Object.fromEntries(Array.from({ length: 100 }, (_, index) => [
      `githubnext/repository-${index}`,
      { mode: 'review' }
    ]));
    const rendered = renderConfigurationView(context({
      document: { 'control-plane': { campaigns: { maintenance: { targets } } } },
      raw: '',
      diagnostics: []
    }));
    if (!rendered) throw new Error('configuration view did not render');

    const maintenanceGroup = [...rendered.querySelectorAll('details')]
      .find((group) => group.querySelector(':scope > summary span')?.textContent === 'Maintenance');
    if (!(maintenanceGroup instanceof HTMLDetailsElement)) throw new Error('maintenance group did not render');
    expect(maintenanceGroup.open).toBe(false);
    expect(rendered.textContent).not.toContain('Targets');
    expect(rendered.querySelectorAll('.configuration-editor .configuration-setting-row')).toHaveLength(0);

    maintenanceGroup.open = true;
    maintenanceGroup.dispatchEvent(new Event('toggle'));
    const targetsGroup = [...maintenanceGroup.querySelectorAll('details')]
      .find((group) => group.querySelector(':scope > summary span')?.textContent === 'Targets');
    if (!(targetsGroup instanceof HTMLDetailsElement)) throw new Error('targets group did not render');
    expect(targetsGroup.open).toBe(false);
    expect(rendered.querySelectorAll('.configuration-editor .configuration-setting-row')).toHaveLength(0);

    targetsGroup.open = true;
    targetsGroup.dispatchEvent(new Event('toggle'));
    expect(targetsGroup.querySelectorAll(':scope > .configuration-setting-children > details')).toHaveLength(100);
    expect(rendered.querySelectorAll('.configuration-editor .configuration-setting-row')).toHaveLength(0);
  });

  it('edits lists without losing the nested policy path', () => {
    const rendered = renderConfigurationView(context({
      document: {
        'control-plane': {
          scope: { 'allowed-owners': ['githubnext', 'octodemo'] }
        }
      },
      raw: '',
      diagnostics: []
    }));
    if (!rendered) throw new Error('configuration view did not render');

    const owners = rendered.querySelector('textarea');
    if (!(owners instanceof HTMLTextAreaElement)) throw new Error('owner list did not render');
    expect(owners.value).toBe('githubnext\noctodemo');
    expect(owners.id).toContain('control-plane-scope-allowed-owners');
    owners.value = 'githubnext\ngithub';
    owners.dispatchEvent(new Event('input'));
    expect(rendered.querySelector('.configuration-edit-status')?.textContent).toBe('Modified locally');
  });

  it('preserves typed values when editing non-string arrays', () => {
    const rendered = renderConfigurationView(context({
      document: { values: [1, true, { mode: 'review' }] },
      raw: '',
      diagnostics: []
    }));
    if (!rendered) throw new Error('configuration view did not render');

    const values = rendered.querySelector('.configuration-setting-json');
    if (!(values instanceof HTMLTextAreaElement)) throw new Error('typed array editor did not render');
    values.value = '[2, false, {"mode":"live"}]';
    values.dispatchEvent(new Event('input'));
    expect(rendered.querySelector('.configuration-edit-status')?.textContent).toBe('Modified locally');
    expect(values.value).toBe('[2, false, {"mode":"live"}]');
  });

  it('copies edited policy JSON', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText }
    });
    const rendered = renderConfigurationView(context({
      document: { version: 1, 'control-plane': { defaults: { 'max-repositories': 7 } } },
      raw: '',
      diagnostics: []
    }));
    if (!rendered) throw new Error('configuration view did not render');

    const input = rendered.querySelector('#configuration-control-plane-defaults-max-repositories');
    if (!(input instanceof HTMLInputElement)) throw new Error('number setting did not render');
    input.value = '12';
    input.dispatchEvent(new Event('input'));
    const copyButton = rendered.querySelector('.configuration-copy-button');
    if (!(copyButton instanceof HTMLButtonElement)) throw new Error('copy button did not render');
    copyButton.click();

    await vi.waitFor(() => expect(writeText).toHaveBeenCalledOnce());
    expect(JSON.parse(writeText.mock.calls[0][0])).toEqual({
      version: 1,
      'control-plane': { defaults: { 'max-repositories': 12 } }
    });
    await vi.waitFor(() => expect(copyButton.nextElementSibling?.textContent).toBe('Updated JSON copied.'));
  });

  it('can discard local changes', () => {
    const rendered = renderConfigurationView(context({
      document: { version: 1, 'control-plane': { defaults: { 'max-repositories': 7 } } },
      raw: '',
      diagnostics: []
    }));
    if (!rendered) throw new Error('configuration view did not render');

    const input = rendered.querySelector('#configuration-control-plane-defaults-max-repositories');
    if (!(input instanceof HTMLInputElement)) throw new Error('number setting did not render');
    input.value = '12';
    input.dispatchEvent(new Event('input'));
    expect(rendered.querySelector('.configuration-copy-button')).not.toBeNull();

    const resetButton = rendered.querySelector('.configuration-reset-button');
    if (!(resetButton instanceof HTMLButtonElement)) throw new Error('reset button did not render');
    resetButton.click();
    expect(rendered.querySelector('.configuration-edit-status')?.textContent).toBe('No changes');
    expect(/** @type {HTMLInputElement | null} */ (rendered.querySelector('#configuration-control-plane-defaults-max-repositories'))?.value).toBe('7');
  });

  it('keeps the edited control node and focus while the draft changes', () => {
    const rendered = renderConfigurationView(context({
      document: { version: 1, 'control-plane': { defaults: { 'max-repositories': 7 } } },
      raw: '',
      diagnostics: []
    }));
    if (!rendered) throw new Error('configuration view did not render');
    document.body.append(rendered);

    const selector = '#configuration-control-plane-defaults-max-repositories';
    const input = /** @type {HTMLInputElement | null} */ (rendered.querySelector(selector));
    if (!(input instanceof HTMLInputElement)) throw new Error('number setting did not render');
    input.focus();
    input.value = '12';
    input.dispatchEvent(new Event('input'));

    expect(rendered.querySelector('.configuration-edit-status')?.textContent).toBe('Modified locally');
    expect(rendered.querySelector(selector)).toBe(input);
    expect(document.activeElement).toBe(input);
    expect(input.value).toBe('12');
    rendered.remove();
  });

  it('stops reacting to draft edits once the editor is detached from the document', () => {
    const rendered = renderConfigurationView(context({
      document: { version: 1, 'control-plane': { defaults: { 'max-repositories': 7 } } },
      raw: '',
      diagnostics: []
    }));
    if (!rendered) throw new Error('configuration view did not render');
    document.body.append(rendered);

    const input = /** @type {HTMLInputElement | null} */ (
      rendered.querySelector('#configuration-control-plane-defaults-max-repositories')
    );
    if (!(input instanceof HTMLInputElement)) throw new Error('number setting did not render');
    input.value = '12';
    input.dispatchEvent(new Event('input'));
    expect(rendered.querySelector('.configuration-edit-status')?.textContent).toBe('Modified locally');

    rendered.remove();
    input.value = '99';
    input.dispatchEvent(new Event('input'));
    expect(rendered.querySelector('.configuration-edit-status')?.textContent).toBe('Modified locally');
  });

  it('does not offer editing controls for invalid structured content', () => {
    const rendered = renderConfigurationView(context({ document: null, raw: '{bad json', diagnostics: [] }));
    if (!rendered) throw new Error('configuration view did not render');

    expect(rendered.textContent).toContain('The policy cannot be edited until it contains valid JSON.');
    expect(rendered.querySelector('.configuration-copy-button')).toBeNull();
  });

  describe('debug logging', () => {
    afterEach(() => {
      window.history.replaceState(null, '', '/');
      vi.restoreAllMocks();
    });

    it('derives the configuration-view debug category predictably from the filename', () => {
      expect(isDebugEnabled('configuration-view', '?debug=configuration-view')).toBe(true);
      expect(isDebugEnabled('configuration-view', '?debug=1')).toBe(true);
      expect(isDebugEnabled('configuration-view', '?debug=render')).toBe(false);
    });

    it('stays silent by default while editing settings and collecting diagnostics', async () => {
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText: vi.fn().mockResolvedValue(undefined) }
      });
      const debug = vi.spyOn(console, 'debug').mockImplementation(() => {});
      const rendered = renderConfigurationView(context({
        document: { 'control-plane': { defaults: { 'max-repositories': 7 } } },
        raw: '',
        diagnostics: []
      }));
      if (!rendered) throw new Error('configuration view did not render');
      document.body.append(rendered);

      const input = rendered.querySelector('#configuration-control-plane-defaults-max-repositories');
      if (!(input instanceof HTMLInputElement)) throw new Error('number setting did not render');
      input.value = '12';
      input.dispatchEvent(new Event('input'));

      const diagnosticsButton = rendered.querySelector('.configuration-diagnostics-button');
      if (!(diagnosticsButton instanceof HTMLButtonElement)) throw new Error('diagnostics button did not render');
      diagnosticsButton.click();
      await vi.waitFor(() => expect(diagnosticsButton.nextElementSibling?.textContent).not.toBe('Collecting diagnostics…'));

      expect(debug).not.toHaveBeenCalled();
    });

    it('logs sanitized draft and diagnostics metadata when the category is enabled', async () => {
      window.history.replaceState(null, '', '/?debug=configuration-view');
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText: vi.fn().mockResolvedValue(undefined) }
      });
      const debug = vi.spyOn(console, 'debug').mockImplementation(() => {});
      vi.resetModules();
      const { renderConfigurationView: renderConfigurationViewWithDebug } = await import('../../src/components/configuration-view.js');
      const rendered = renderConfigurationViewWithDebug(context({
        document: { 'control-plane': { defaults: { 'max-repositories': 7 } } },
        raw: '',
        diagnostics: []
      }));
      if (!rendered) throw new Error('configuration view did not render');
      document.body.append(rendered);

      const input = rendered.querySelector('#configuration-control-plane-defaults-max-repositories');
      if (!(input instanceof HTMLInputElement)) throw new Error('number setting did not render');
      input.value = '12';
      input.dispatchEvent(new Event('input'));
      expect(debug).toHaveBeenCalledWith('[cao:configuration-view]', 'settings draft state changed', { modified: true });

      const diagnosticsButton = rendered.querySelector('.configuration-diagnostics-button');
      if (!(diagnosticsButton instanceof HTMLButtonElement)) throw new Error('diagnostics button did not render');
      diagnosticsButton.click();
      expect(debug).toHaveBeenCalledWith('[cao:configuration-view]', 'diagnostics collection started');

      await vi.waitFor(() => expect(debug).toHaveBeenCalledWith(
        '[cao:configuration-view]',
        'diagnostics collection finished',
        expect.objectContaining({ status: 'success', copied: true, durationMs: expect.any(Number) })
      ));

      for (const call of debug.mock.calls) {
        for (const value of call.slice(1)) {
          if (value && typeof value === 'object') {
            expect(Object.keys(value)).not.toEqual(expect.arrayContaining(['message', 'token', 'secret', 'report']));
          }
        }
      }
    });
  });
});
