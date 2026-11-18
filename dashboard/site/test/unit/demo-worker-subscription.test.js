// @vitest-environment node
import 'fake-indexeddb/auto';
import { expect, it, vi } from 'vitest';
import { ingestDashboardSources } from '../../src/data/ingest/coordinator.js';
import { synthesizeEnterpriseDemo } from '../../src/data/demo/synthesis.js';

vi.mock('../../src/deployment-profile.js', () => ({ DEMO_MODE: true }));
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
    finish();
    await population;
    await vi.advanceTimersByTimeAsync(5_000);
    await vi.waitFor(() => expect(updates()).toHaveLength(3));
    expect(updates().at(-1).partial).toBeUndefined();
    expect(updates().at(-1).data.repositories.metadata.completeness).not.toBe('partial');
    await commit('cancelled');
    message({ data: { operation: 'unsubscribe-canonical-dashboard', subscriptionId: 'demo-repositories' } });
    message({ data: { operation: 'unsubscribe-canonical-dashboard', subscriptionId: 'demo-second-view' } });
    await vi.advanceTimersByTimeAsync(5_000);
    expect(updates()).toHaveLength(3);
  } finally {
    finish();
    await population;
    message({ data: { operation: 'unsubscribe-canonical-dashboard', subscriptionId: 'demo-repositories' } });
    message({ data: { operation: 'unsubscribe-canonical-dashboard', subscriptionId: 'demo-second-view' } });
    vi.useRealTimers();
    vi.unstubAllGlobals();
  }
});
