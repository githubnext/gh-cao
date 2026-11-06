import { afterEach, expect, it, vi } from 'vitest';
import { webcrypto } from 'node:crypto';
import { IDBFactory, IDBKeyRange } from 'fake-indexeddb';
import { processDataRequest } from '../../src/data-worker.js';
import { upsertCanonicalBatch } from '../../src/data/storage/indexeddb.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { dashboardPageAllSourceNames } from '../../src/dashboard-chunks.js';
import { authoritativeDashboard as document } from '../authoritative-dashboard.js';
import contract from '../fixtures/immediate-operations-contract.json' with { type: 'json' };

afterEach(() => vi.unstubAllGlobals());

it('serves the immediate landing view through the canonical worker without fleet or daily sources', async () => {
  const page = document.dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === contract.page);
  const view = page.views.find((/** @type {{ id: string }} */ view) => view.id === contract.view);
  expect(view.encoding.columns.map((/** @type {{ field: string }} */ column) => column.field)).toEqual(contract.fields);
  expect(dashboardPageAllSourceNames(document, contract.page)).toEqual(contract.sources);
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
