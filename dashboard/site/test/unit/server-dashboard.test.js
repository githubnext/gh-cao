import { afterEach, expect, it, vi } from 'vitest';
import { webcrypto } from 'node:crypto';
import { IDBFactory, IDBKeyRange } from 'fake-indexeddb';
import { processDataRequest } from '../../src/data-worker.js';
import { upsertCanonicalBatch } from '../../src/data/storage/indexeddb.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { authoritativeDashboard as document } from '../authoritative-dashboard.js';
import contract from '../fixtures/canonical-dashboard-contract.json' with { type: 'json' };

afterEach(() => vi.unstubAllGlobals());

it('serves ingested runs through the standard dashboard queries without local aliases', async () => {
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
    sourceNames: payload.aliases,
    context: { queries: [...document.dashboard.queries, ...payload.queries], pages: document.dashboard.pages }
  }));
  const source = result[payload.aliases[0]];
  expect(source.metadata.availability).toBe('available');
  expect(source.rows).toHaveLength(1);
  expect(source.rows[0]).toMatchObject({
    run: '123', 'run-status': 'in_progress', 'repository-coordinate': 'octo/api'
  });
});
