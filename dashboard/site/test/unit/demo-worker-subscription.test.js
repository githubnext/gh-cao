// @vitest-environment node
import 'fake-indexeddb/auto';
import { expect, it, vi } from 'vitest';
import { ingestDashboardSources } from '../../src/data/ingest/coordinator.js';
import { synthesizeEnterpriseDemo } from '../../src/data/demo/synthesis.js';
import { loadDatabaseQuerySources } from '../../src/data/queries/database.js';

vi.mock('../../src/deployment-profile.js', () => ({ DEMO_MODE: true }));
vi.mock('../../src/data/queries/database.js', async (importOriginal) => {
  /** @type {typeof import('../../src/data/queries/database.js')} */
  const original = await importOriginal();
  return { ...original, loadDatabaseQuerySources: vi.fn(original.loadDatabaseQuerySources) };
});
vi.mock('../../src/data/demo/synthesis.js', async (importOriginal) => ({
  ...await importOriginal(),
  synthesizeEnterpriseDemo: vi.fn(),
}));

it('requeries committed demo batches during population in bounded five-second windows', async () => {
  /** @type {(event: { data: Record<string, unknown> }) => void} */
  let message = () => {};
  const posted = vi.fn();
  vi.stubGlobal('self', {
    addEventListener: (/** @type {string} */ _type, /** @type {typeof message} */ listener) => { message = listener; },
    postMessage: posted,
  });
  const { processDataRequest } = await import('../../src/data-worker.js');
  const context = { pages: [], queries: [] };
  await processDataRequest({ operation: 'query-canonical-dashboard', sourceNames: [], context });
  await expect(processDataRequest({
    operation: 'query-canonical-dashboard', sourceNames: ['configuration-policy'], context,
  })).resolves.toMatchObject({ 'configuration-policy': { metadata: { completeness: 'partial' } } });
  message({ data: {
    operation: 'subscribe-canonical-dashboard', subscriptionId: 'demo-repositories',
    sourceNames: ['repositories'], context,
  } });
  const updates = () => posted.mock.calls.map(([value]) => value)
    .filter(value => value.subscriptionId === 'demo-repositories');
  message({ data: {
    operation: 'subscribe-canonical-dashboard', subscriptionId: 'demo-second-view',
    sourceNames: ['repositories'], context,
  } });
  await vi.waitFor(() => expect(updates()).toHaveLength(1));
  await vi.waitFor(() => expect(posted.mock.calls.filter(([value]) => value.subscriptionId === 'demo-second-view')).toHaveLength(1));
  /** @type {NonNullable<Parameters<typeof synthesizeEnterpriseDemo>[1]>['onCommitted']} */
  let committed;
  /** @type {() => void} */
  let finish = () => {};
  vi.mocked(synthesizeEnterpriseDemo).mockImplementation(async (_database, options) => {
    committed = options?.onCommitted;
    await new Promise((resolve) => { finish = () => resolve(undefined); });
    return { sources: {}, changed: true };
  });
  const population = processDataRequest({
    operation: 'load-canonical-dashboard', sourceUrl: 'https://dashboard.example/demo-data/',
    sourceNames: [], context,
  });
  await vi.waitFor(() => expect(committed).toBeTypeOf('function'));
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
  const commit = async (/** @type {string} */ repository) => {
    await ingestDashboardSources(indexedDB, {
      repositories: {
        rows: [{ organization: 'synthetic', repository }],
        metadata: { 'as-of': new Date().toISOString() },
      },
    }, { deferMaintenance: true, payloadScope: `demo-test:${repository}` });
    committed?.({});
  };
  let releaseQuery = () => {};
  try {
    await commit('first');
    await vi.advanceTimersByTimeAsync(2_000);
    await commit('second');
    await vi.advanceTimersByTimeAsync(2_999);
    expect(updates()).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    await vi.waitFor(() => expect(updates()).toHaveLength(2));
    expect(updates().at(-1)).toMatchObject({
      partial: true,
      data: { repositories: {
        rows: expect.arrayContaining([
          expect.objectContaining({ repository: 'first' }),
          expect.objectContaining({ repository: 'second' }),
        ]),
        metadata: { completeness: 'partial' },
      } },
    });
    expect(posted.mock.calls.filter(([value]) => value.subscriptionId === 'demo-second-view')).toHaveLength(2);
    const originalQuery = vi.mocked(loadDatabaseQuerySources).getMockImplementation();
    if (!originalQuery) throw new Error('Expected the production database query implementation');
    let queryReady = false;
    vi.mocked(loadDatabaseQuerySources).mockImplementationOnce(async (...args) => {
      const result = await originalQuery(...args);
      await new Promise((resolve) => {
        releaseQuery = () => resolve(undefined);
        queryReady = true;
      });
      return result;
    });
    await commit('third');
    await vi.advanceTimersByTimeAsync(5_000);
    await vi.waitFor(() => expect(queryReady).toBe(true));
    await commit('fourth');
    releaseQuery();
    await vi.waitFor(() => expect(updates()).toHaveLength(3));
    const superseded = updates().at(-1);
    expect(superseded.data.repositories.rows).toEqual(expect.arrayContaining([
      expect.objectContaining({ repository: 'third' }),
    ]));
    expect(superseded.data.repositories.rows).not.toEqual(expect.arrayContaining([
      expect.objectContaining({ repository: 'fourth' }),
    ]));
    await vi.advanceTimersByTimeAsync(4_999);
    expect(updates()).toHaveLength(3);
    await vi.advanceTimersByTimeAsync(1);
    await vi.waitFor(() => expect(updates()).toHaveLength(4));
    expect(updates().at(-1).revision).toBeGreaterThan(superseded.revision);
    expect(updates().at(-1).data.repositories.rows).toEqual(expect.arrayContaining([
      expect.objectContaining({ repository: 'fourth' }),
    ]));
    finish();
    await population;
    await vi.advanceTimersByTimeAsync(5_000);
    await vi.waitFor(() => expect(updates()).toHaveLength(5));
    expect(updates().at(-1).partial).toBeUndefined();
    expect(updates().at(-1).data.repositories.metadata.completeness).not.toBe('partial');
    await commit('cancelled');
    message({ data: { operation: 'unsubscribe-canonical-dashboard', subscriptionId: 'demo-repositories' } });
    message({ data: { operation: 'unsubscribe-canonical-dashboard', subscriptionId: 'demo-second-view' } });
    await vi.advanceTimersByTimeAsync(5_000);
    expect(updates()).toHaveLength(5);
  } finally {
    releaseQuery();
    finish();
    await population;
    message({ data: { operation: 'unsubscribe-canonical-dashboard', subscriptionId: 'demo-repositories' } });
    message({ data: { operation: 'unsubscribe-canonical-dashboard', subscriptionId: 'demo-second-view' } });
    vi.useRealTimers();
    vi.unstubAllGlobals();
  }
});
