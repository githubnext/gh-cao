import { afterEach, expect, it, vi } from 'vitest';
import { webcrypto } from 'node:crypto';
import { IDBFactory, IDBKeyRange } from 'fake-indexeddb';
import { processDataRequest } from '../../src/data-worker.js';
import { upsertCanonicalBatch } from '../../src/data/storage/indexeddb.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { dashboardPageAllSourceNames } from '../../src/dashboard-chunks.js';
import { authoritativeDashboard as document } from '../authoritative-dashboard.js';
import { composeImmediateDashboard } from '../../../report/compose-immediate-dashboard.mjs';
import { resolveDashboardQuerySources } from '../../src/data/queries/declarative.js';
import { validateDashboardDocument } from '../../src/validator.js';
import contract from '../fixtures/local-dashboard-contract.json' with { type: 'json' };
import profile from '../../dashboard-fragments/immediate-profile.json' with { type: 'json' };

afterEach(() => vi.unstubAllGlobals());

it('serves the shared run table through the canonical worker without fleet sources', async () => {
  const page = document.dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === contract.page);
  const view = page.definition.views.find((/** @type {{ id: string }} */ view) => view.id === contract.view);
  expect(view.encoding.columns.map((/** @type {{ field: string }} */ column) => column.field)).toEqual(contract.fields);
  vi.stubGlobal('crypto', webcrypto);
  vi.stubGlobal('indexedDB', new IDBFactory());
  vi.stubGlobal('IDBKeyRange', IDBKeyRange);
  await upsertCanonicalBatch(indexedDB, contract.batch);
  const payload = compileDashboardViewPayloadQueries(page, contract.page, {
    queries: document.dashboard.queries, viewId: contract.view, sourceNames: [contract.source]
  });

  const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
    operation: 'query-canonical-dashboard',
    sourceNames: [...payload.aliases, ...contract.sources.filter((name) => name !== contract.source)],
    context: { queries: [...document.dashboard.queries, ...payload.queries], pages: document.dashboard.pages }
  }));
  expect(payload.aliases).toHaveLength(1);
  const source = result[payload.aliases[0]];
  expect(source.metadata.availability).toBe('available');
  expect(source.rows).toHaveLength(1);
  expect(source.rows[0]).toMatchObject({ run: '123', 'run-status': 'in_progress', 'repository-coordinate': 'octo/api' });
  for (const name of contract.sources.filter((name) => name !== contract.source)) {
    expect(result[name].metadata.availability).not.toBe('unavailable');
  }
});

it('preserves normal pages, navigation, templates and drill-downs with local acquisition queries', async () => {
  const immediate = composeImmediateDashboard(document, profile);
  const context = { languageVersion: immediate['language-version'], dashboard: immediate.dashboard };
  const validation = validateDashboardDocument(JSON.stringify(immediate));
  expect(validation, JSON.stringify(validation)).toMatchObject({ ok: true });
  expect(immediate.dashboard.pages).toEqual(document.dashboard.pages.filter(
    (/** @type {{ id: string }} */ page) => !profile['excluded-pages'].includes(page.id)));
  expect((immediate.dashboard.navigation ?? []).flatMap((section) => section.pages)).not.toContain('configuration');
  expect((immediate.dashboard.navigation ?? []).flatMap((section) => section.pages)).not.toContain('indexing');
  expect(document.dashboard.pages.some((/** @type {{ id: string }} */ page) => page.id === 'configuration')).toBe(true);
  expect(document.dashboard.pages.some((/** @type {{ id: string }} */ page) => page.id === 'indexing')).toBe(true);
  expect(immediate.dashboard['card-templates']).toEqual(document.dashboard['card-templates']);
  const overview = immediate.dashboard.pages.find((page) => page.id === 'overview');
  expect(overview?.title).toBe('Overview');
  expect(overview?.['class-name']).toBe('dashboard-overview-page');
  expect(immediate.dashboard['data-status']).toEqual(document.dashboard['data-status']);
  expect(immediate.dashboard.callouts ?? []).toEqual(document.dashboard.callouts.filter(
    (/** @type {{ ['navigation-page']: string }} */ callout) => !profile['excluded-pages'].includes(callout['navigation-page'])));
  const names = immediate.dashboard.pages.flatMap((page) =>
    dashboardPageAllSourceNames(context, page.id ?? '', 'all'));
  const dependencies = resolveDashboardQuerySources(immediate.dashboard.queries, names);
  expect(dependencies).toContain('daily-run-counts');
  expect(dependencies).toContain('campaigns');
  expect(dependencies).toContain('tools');
  expect(dependencies).toContain('audits');
  vi.stubGlobal('crypto', webcrypto);
  vi.stubGlobal('indexedDB', new IDBFactory());
  vi.stubGlobal('IDBKeyRange', IDBKeyRange);
  const batch = structuredClone(contract.batch);
  const now = new Date().toISOString();
  batch.runs[0].startedAt = now;
  batch.runs[0].createdAt = now;
  await upsertCanonicalBatch(indexedDB, batch);
  for (const page of immediate.dashboard.pages.filter((page) => page.id === 'runs')) {
    const sources = dashboardPageAllSourceNames(context, page.id ?? '', 'all');
    const payload = compileDashboardViewPayloadQueries(page, page.id ?? '', {
      queries: immediate.dashboard.queries, sourceNames: sources
    });
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
      operation: 'query-canonical-dashboard',
      sourceNames: payload.aliases,
      context: { queries: [...(immediate.dashboard.queries ?? []), ...payload.queries], pages: immediate.dashboard.pages }
    }));
    for (const name of payload.aliases) {
      expect(result[name].metadata.availability).toBe('available');
      expect(result[name].rows.length).toBeGreaterThan(0);
    }
  }
});
