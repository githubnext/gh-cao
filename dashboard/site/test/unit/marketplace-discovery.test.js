import { describe, expect, it } from 'vitest';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { executeDashboardQueries } from '../../src/data/queries/declarative.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import { validateDashboardDocument } from '../../src/validator.js';
import { dashboardPageLazySourceNames, dashboardPagePaginatedSourceBindings } from '../../src/presenter.js';
import contract from '../fixtures/marketplace-view-contract.json' with { type: 'json' };

const dashboard = authoritativeDashboard.dashboard;
const page = dashboard.pages.find((/** @type {{id: string}} */ candidate) => candidate.id === 'marketplace');
/** @type {import('../../src/presenter.js').SourceMetadata} */
const metadata = {
  'source-id': 'marketplace-fixture', 'source-kind': 'fixture',
  'as-of': '2026-09-30T00:00:00Z', 'retrieved-at': '2026-09-30T00:00:00Z',
  completeness: 'complete', freshness: 'fresh', availability: 'available'
};
const packages = contract.rows;
const sources = { 'marketplace-packages': { source: 'marketplace-packages', rows: packages, metadata } };

function resolve() {
  const compiled = compileDashboardViewPayloadQueries(page, 'marketplace', {
    queries: dashboard.queries
  });
  const baseResults = executeDashboardQueries(dashboard.queries, sources);
  const result = {
    ...baseResults,
    ...(compiled.queries.length > 0 ? executeDashboardQueries(compiled.queries, { ...sources, ...baseResults }) : {})
  };
  /** @param {string} source */
  const rows = (source) => (
    result[compiled.aliases.find((name) => name.includes(`:${source}`)) ?? '']
    ?? result[source]
    ?? (source === 'marketplace-packages' ? sources['marketplace-packages'] : undefined)
  )?.rows;
  return { rows, compiled };
}

describe('marketplace discovery', () => {
  it('omits the directory introduction and retains the no-AI add contract in package details', () => {
    const detail = dashboard.pages.find((/** @type {{id: string}} */ candidate) => candidate.id === 'marketplace-package');
    expect(page.views[0].description).toBeUndefined();
    expect(page.acceptance).toContain('use no AI');
    expect(detail.acceptance).toContain('use no AI');
  });

  it('requires a recovery route for the declared package availability view', () => {
    const document = structuredClone(authoritativeDashboard);
    const detail = document.dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === 'marketplace-package');
    expect(validateDashboardDocument(JSON.stringify(document)).ok).toBe(true);
    delete detail.route['navigation-page'];
    expect(validateDashboardDocument(JSON.stringify(document)).errors).toEqual(expect.arrayContaining([
      expect.objectContaining({
        message: 'route availability-view requires navigation-page for recovery.'
      })
    ]));
  });

  it('binds the declarative card list to an alphabetic package query without facets or ranking', () => {
    expect(page.views).toHaveLength(1);
    expect(page.views[0]).toMatchObject({
      id: contract.view,
      mark: 'list', data: { source: contract.source, 'query-context': false },
      'lazy-list': contract['lazy-list'],
      layout: contract.layout,
      list: { style: 'entity-cards', card: contract.card }
    });
    expect(dashboard.queries.find((/** @type {{name: string}} */ query) => query.name === 'marketplace-directory')).toMatchObject({
      from: 'marketplace-packages',
      'order-by': contract['order-by']
    });
    const { rows } = resolve();
    expect(rows('marketplace-directory')?.map((row) => row.id)).toEqual(['one', 'two', 'three']);
    expect(rows('marketplace-directory')?.[1].stars).toBeNull();
    const template = dashboard['card-templates'].find((/** @type {{ id: string }} */ template) => template.id === contract.card);
    expect([template.title, template.subtitle, ...template.labels, ...template.details].map(field => field.field))
      .toEqual(contract['summary-fields']);
  });

  it('requests canonical continuation pages for the declared lazy card list', () => {
    expect(dashboardPageLazySourceNames(authoritativeDashboard, 'marketplace')).toEqual(['marketplace-directory']);
    expect(Object.values(dashboardPagePaginatedSourceBindings(authoritativeDashboard, 'marketplace')))
      .toEqual([{ sourceName: 'marketplace-directory', viewId: 'marketplace-packages' }]);
    expect(resolve().compiled.aliases).toHaveLength(1);
  });

  it('enforces a declared lazy-list row limit in the query rather than per rendered batch', () => {
    const limited = structuredClone(page);
    limited.views[0].data.limit = 7;
    const compiled = compileDashboardViewPayloadQueries(limited, 'marketplace', { queries: dashboard.queries });
    expect(compiled.queries.find(query => query.name === compiled.aliases[0])?.limit).toBe(7);
  });

  it('rejects lazy loading on list presentations without entity cards', () => {
    const document = structuredClone(authoritativeDashboard);
    const list = document.dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === 'marketplace').views[0];
    list.list.style = 'cards';
    expect(validateDashboardDocument(JSON.stringify(document)).errors).toEqual(expect.arrayContaining([
      expect.objectContaining({ path: expect.stringMatching(/\.lazy-list$/) })
    ]));
  });
});
