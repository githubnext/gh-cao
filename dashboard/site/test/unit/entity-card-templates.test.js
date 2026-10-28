// @vitest-environment node
import { describe, expect, it } from 'vitest';

import { authoritativeDashboard as document } from '../authoritative-dashboard.js';
const dashboard = document.dashboard;
const templates = Object.fromEntries(dashboard['card-templates'].map(
  (/** @type {Record<string, any>} */ template) => [template.id, template]
));
const views = Object.fromEntries(dashboard.views.map(
  (/** @type {Record<string, any>} */ view) => [view.id, view]
));
const pages = Object.fromEntries(dashboard.pages.map(
  (/** @type {Record<string, any>} */ page) => [page.id, page]
));

describe('entity card templates', () => {
  it('declares cards for every navigable canonical entity', () => {
    expect(Object.keys(templates)).toEqual(expect.arrayContaining([
      'repository',
      'workflow',
      'run',
      'event',
      'campaign',
      'operation',
      'marketplace-package-summary',
      'marketplace-package'
    ]));
  });

  it('declares a Primer-branded marketplace package card', () => {
    expect(templates['marketplace-package-summary']).toMatchObject({
      icon: 'archive',
      'icon-field': 'package-icon',
      title: { field: 'package-name' },
      subtitle: { field: 'package-description' },
      'detail-labels': 'visible',
      details: [
        { field: 'publisher', title: 'By' }
      ],
      labels: [
        { field: 'registry-name', title: 'Registry', display: 'label' }
      ]
    });
    expect(templates['marketplace-package-summary'].actions).toBeUndefined();
    expect(templates['marketplace-package']).toMatchObject({
      icon: 'archive',
      'icon-field': 'package-icon',
      title: { field: 'package-name' },
      subtitle: { field: 'package-description' },
      actions: [{ action: 'add-marketplace-package', context: ['package-source'] }]
    });
  });

  it('declares marketplace list and detail routes', () => {
    expect(pages.marketplace.experimental).toBe(false);
    expect(pages.marketplace['class-name']).toBe('marketplace-page');
    expect(dashboard.navigation[0]).toEqual({ pages: ['overview', 'marketplace'] });
    expect(dashboard.navigation.find(
      (/** @type {Record<string, any>} */ section) => section.label === 'Maintenance'
    )?.pages).not.toContain('marketplace');
    expect(pages.marketplace.views).toHaveLength(1);
    expect(pages.marketplace.views[0]).toMatchObject({
      data: { source: 'marketplace-directory' },
      'lazy-list': true,
      list: {
        style: 'entity-cards',
        appearance: 'marketplace',
        layout: 'grid',
        icon: 'archive',
        card: 'marketplace-package-summary',
        drill: { page: 'marketplace-package', query: 'marketplace-package-detail' }
      }
    });
    expect(pages['marketplace-package']).toMatchObject({
      route: { 'hash-query-parameter': 'package-source', 'navigation-page': 'marketplace' }
    });
    expect(pages['marketplace-package'].views[0]).toMatchObject({
      list: { icon: 'archive', appearance: 'marketplace', card: 'marketplace-package' }
    });
    expect(pages['marketplace-package'].views[1]).toMatchObject({
      element: 'markdown',
      config: { 'content-field': 'package-readme', 'path-field': 'package-readme-path' }
    });
    expect(pages['marketplace-package'].views[2]).toMatchObject({
      element: 'link-button-list',
      config: { 'label-field': 'repository', 'link-field': 'repository-link' }
    });
    expect(pages['marketplace-package'].sections.map(
      (/** @type {Record<string, any>} */ section) => [section.id, section.layout]
    )).toEqual([['package', 'full'], ['readme', 'wide'], ['about', 'narrow']]);
  });

  it('marks the ingestion page as non-experimental', () => {
    expect(pages.indexing.experimental).toBe(false);
  });

  it('declares the marketplace action as copy-only', () => {
    expect(dashboard['cli-actions'].find(
      (/** @type {Record<string, any>} */ action) => action.id === 'add-marketplace-package'
    )).toMatchObject({
      label: 'Add',
      icon: 'plus',
      command: 'gh cao add {{package-source}}',
      placement: 'row',
      'copy-only': true
    });
  });

  it('declares the factory campaign cards in JSON', () => {
    expect(templates.campaign).toMatchObject({
      icon: 'goal',
      title: { field: 'campaign-name' },
      labels: [
        { field: 'modes', display: 'label' }
      ],
      details: [
        { field: 'dispatches', title: '# dispatches' },
        { field: 'aic', title: '# aic', unit: 'aic' }
      ]
    });
    expect(templates.campaign.details).not.toContainEqual(expect.objectContaining({ field: 'runs' }));
    expect(pages.overview.views.find(
      (/** @type {Record<string, any>} */ view) => view.id === 'overview-campaigns'
    )).toBeUndefined();
    expect(pages.campaigns.definition.views.find(
      (/** @type {Record<string, any>} */ view) => view.id === 'campaigns-inventory'
    )).toMatchObject({
      title: 'Campaigns',
      data: { source: 'campaign-inventory' },
      mark: 'table'
    });
    const dispatches = pages.campaigns.definition.views.find(
      (/** @type {Record<string, any>} */ view) => view.id === 'campaigns-dispatches'
    );
    expect(dispatches).toMatchObject({
      data: { source: 'campaign-inventory' },
      encoding: { y: { field: 'dispatches', type: 'quantitative' } }
    });
    expect(dispatches.encoding.y).not.toHaveProperty('aggregate');
    expect(pages.campaigns.definition.views.find(
      (/** @type {Record<string, any>} */ view) => view.id === 'campaigns-repository-coverage'
    )).toMatchObject({
      title: 'Repository coverage by campaign',
      data: {
        source: 'campaign-inventory',
        limit: 100
      },
      mark: 'chart',
      chart: 'horizontal-bar',
      encoding: {
        x: { field: 'campaign-name', type: 'nominal' },
        color: { field: 'campaign-name', type: 'nominal' },
        y: { field: 'covered-repositories', type: 'quantitative' }
      }
    });
  });

  it('presents runs like a GitHub Actions run row', () => {
    expect(templates.run).toMatchObject({
      status: { field: 'run-conclusion', 'fallback-field': 'run-status' },
      title: { field: 'workflow', format: 'workflow-relative-path' },
      subtitle: { field: 'target-repository' },
      labels: [
        { field: 'run-status', title: 'Status', display: 'status' },
        { field: 'run-conclusion', title: 'Outcome', display: 'status' },
        { field: 'branch', display: 'ref' }
      ],
      timing: [
        { field: 'started-at', icon: 'calendar' },
        { field: 'duration', icon: 'stopwatch' }
      ]
    });
    const runQuery = dashboard.queries.find((/** @type {Record<string, any>} */ query) => query.name === 'entity-runs');
    const selected = runQuery.select.map((/** @type {Record<string, any>} */ field) => field.field);
    expect(selected).toEqual(expect.arrayContaining(['workflow', 'target-repository', 'run-title', 'run-status', 'run-conclusion', 'branch', 'event', 'duration', 'started-at']));
    expect(views['entity-runs'].prompt).toBe('none');
  });

  it('presents the workflow list with latest execution status instead of aggregate counters', () => {
    expect(templates['workflow-latest']).toMatchObject({
      icon: 'workflow',
      status: { field: 'latest-run-status' },
      title: { field: 'workflow-name' },
      subtitle: { field: 'workflow', format: 'workflow-relative-path' },
      details: expect.arrayContaining([
        { field: 'latest-run-at', title: 'Latest run', format: 'human-friendly-timestamp' }
      ])
    });
    const inventoryQuery = dashboard.queries.find(
      (/** @type {Record<string, any>} */ query) => query.name === 'workflow-list'
    );
    const selected = inventoryQuery.select.map((/** @type {Record<string, any>} */ field) => field.as ?? field.field);
    expect(selected).toEqual(expect.arrayContaining(['workflow-name', 'workflow', 'latest-run-status', 'latest-run-at']));
    const inventoryView = pages.workflows.definition.views.find(
      (/** @type {Record<string, any>} */ view) => view.id === 'workflows-inventory'
    );
    const columns = inventoryView.encoding.columns.map((/** @type {Record<string, any>} */ field) => field.field);
    expect(columns).toEqual(expect.arrayContaining(['workflow-name', 'workflow', 'latest-run-status', 'latest-run-at']));
    expect(columns).not.toEqual(expect.arrayContaining(['successful-runs', 'failed-runs']));
    expect(inventoryView).toMatchObject({ prompt: 'none', 'card-drill': { type: 'external', field: 'workflow-link' } });
    const entityQuery = dashboard.queries.find(
      (/** @type {Record<string, any>} */ query) => query.name === 'entity-workflows'
    );
    const entityFields = entityQuery.select.map((/** @type {Record<string, any>} */ field) => field.as ?? field.field);
    expect(entityFields).toEqual(expect.arrayContaining(['runs', 'successful-runs', 'failed-runs', 'aic-per-run']));
  });

  it('links failure cards to their representative workflow run', () => {
    expect(templates.problem.details).toContainEqual({
      field: 'run',
      title: 'Run',
      display: 'run-link'
    });
    expect(templates.problem.timing).toContainEqual({
      field: 'started-at',
      title: 'Observed',
      type: 'temporal',
      format: 'human-friendly-timestamp',
      icon: 'clock'
    });
  });

  it('declares a firewall domain card with allowed and blocked metrics', () => {
    expect(templates['firewall-domain']).toEqual({
      id: 'firewall-domain',
      icon: 'globe',
      drill: {
        type: 'query',
        page: 'domain-insights',
        query: 'domain-entity-insights',
        'title-field': 'domain',
        arguments: [{ name: 'domain', field: 'domain' }]
      },
      title: { field: 'domain', title: 'Domain' },
      labels: [],
      details: [
        { field: 'accepted', title: 'Allowed' },
        { field: 'blocked', title: 'Blocked' },
        { field: 'run', title: 'Runs' }
      ]
    });
  });

  it('drills from firewall domains to their allowed and blocked workflows', () => {
    const firewall = pages.firewall;
    const domainList = firewall.views.find(
      (/** @type {Record<string, any>} */ view) => view.id === 'security-firewall-domains'
    );
    expect(domainList).toMatchObject({ mark: 'table' });
    expect(domainList['card-drill']).toBeUndefined();
    expect(views['firewall-domain-workflows']).toMatchObject({
      data: {
        source: 'firewall-domain-workflows',
        arguments: [{ name: 'domain', field: 'domain' }]
      },
      list: { card: 'firewall-workflow' }
    });
    expect(templates['firewall-workflow']).toMatchObject({
      title: { field: 'workflow', format: 'workflow-relative-path' },
      subtitle: { field: 'repository' },
      details: [
        { field: 'accepted', title: 'Allowed' },
        { field: 'blocked', title: 'Blocked' },
        { field: 'runs', title: 'Runs' }
      ]
    });
    expect(pages['firewall-domain-workflows'].views).toEqual(['firewall-domain-workflows']);
  });

  it('retains the reusable operation card template', () => {
    expect(templates.operation).toMatchObject({
      icon: 'workflow',
      'icon-field': 'operation-icon',
      title: { field: 'operation-name' },
      subtitle: { field: 'operation-description' }
    });
  });

  it('drills from repositories through workflows and runs to events', () => {
    expect(templates.repository).toMatchObject({
      title: { field: 'repository' },
      details: [{ field: 'runs', title: 'Runs' }]
    });
    expect(templates.repository.actions).toBeUndefined();
    expect(views['entity-repositories'].data.source).toBe('repository-activity');
    expect(views['entity-repositories'].encoding.columns).toEqual([
      { field: 'repository', type: 'nominal', title: 'Repository' },
      { field: 'runs', type: 'quantitative', title: 'Runs' }
    ]);
    expect(views['entity-repositories'].list).toMatchObject({
      card: 'repository',
      drill: {
        type: 'query',
        page: 'repository-workflows',
        query: 'entity-workflows',
        arguments: [
          { name: 'organization', field: 'organization' },
          { name: 'repository', field: 'repository-name' }
        ]
      }
    });
    expect(views['repository-workflows'].data.arguments).toEqual([
      { name: 'organization', field: 'organization' },
      { name: 'repository', field: 'repository' }
    ]);
    expect(views['repository-workflows'].list.drill).toMatchObject({
      page: 'workflow-run-cards',
      query: 'entity-runs'
    });
    expect(views['workflow-runs-cards'].list.drill).toMatchObject({
      page: 'run-events',
      query: 'entity-events'
    });
    expect(pages['run-events'].views).toEqual(['run-events']);
  });

  it('specializes GitHub entity events', () => {
    expect(views['run-events']).toMatchObject({
      data: {
        source: 'entity-events',
        arguments: expect.arrayContaining([{ name: 'run', field: 'run' }])
      },
      list: {
        card: 'event',
        drill: { type: 'external', field: 'event-url' }
      }
    });
  });
});
