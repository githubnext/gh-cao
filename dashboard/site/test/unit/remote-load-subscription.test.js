import { afterEach, expect, it, vi } from 'vitest';
import { subscribeCanonicalDashboardView } from '../../src/data-processor.js';
import { queryRemoteDashboard, subscribeRemoteRevision } from '../../src/remote-data-backend.js';

vi.mock('../../src/remote-data-backend.js', () => ({
  usesRemoteDataBackend: () => true,
  queryRemoteDashboard: vi.fn(),
  subscribeRemoteRevision: vi.fn(() => () => {})
}));

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.clearAllMocks();
  vi.unstubAllGlobals();
});

it('refreshes statistical subscribers at the configured interval without a new server revision', async () => {
  vi.useFakeTimers();
  vi.stubGlobal('__CAO_DASHBOARD_CONFIG__', { statisticalRefreshIntervalMs: 1_000 });
  vi.mocked(queryRemoteDashboard).mockResolvedValue({
    revision: 1, healthRevision: 1,
    sources: { composition: { source: 'composition', rows: [{ sample: 1 }] } }
  });
  const listener = vi.fn();
  const controller = new AbortController();
  const stop = subscribeCanonicalDashboardView('statistical-composition', ['composition'], {
    pages: [], queries: [{ name: 'composition', from: 'statistical-runs' }]
  }, listener, undefined, { signal: controller.signal });
  await vi.advanceTimersByTimeAsync(20);
  expect(queryRemoteDashboard).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(979);
  expect(queryRemoteDashboard).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(21);
  expect(queryRemoteDashboard).toHaveBeenCalledTimes(2);
  expect(listener).toHaveBeenCalledTimes(2);
  const signal = vi.mocked(queryRemoteDashboard).mock.calls[0][3]?.signal;
  controller.abort();
  stop();
  expect(signal?.aborted).toBe(true);
  await vi.advanceTimersByTimeAsync(5_000);
  expect(queryRemoteDashboard).toHaveBeenCalledTimes(2);
});

it('uses the server refresh interval instead of the browser default and adopts a changed interval', async () => {
  vi.useFakeTimers();
  const response = (/** @type {number} */ interval) => ({
    revision: 1, healthRevision: 1,
    sources: { composition: {
      source: 'composition', rows: [{ sample: 1 }],
      metadata: {
        'refresh-interval-ms': interval, 'sample-built-at': '2026-11-13T14:00:00Z', freshness: 'stale'
      }
    } }
  });
  vi.mocked(queryRemoteDashboard).mockResolvedValueOnce(response(60_000)).mockResolvedValue(response(15_000));
  const listener = vi.fn();
  const stop = subscribeCanonicalDashboardView('server-statistical-composition', ['composition'], {
    pages: [], queries: [{ name: 'composition', from: 'statistical-runs' }]
  }, listener);
  await vi.advanceTimersByTimeAsync(59_999);
  expect(queryRemoteDashboard).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(21);
  expect(queryRemoteDashboard).toHaveBeenCalledTimes(2);
  expect(listener).toHaveBeenCalledTimes(2);
  await vi.advanceTimersByTimeAsync(14_979);
  expect(queryRemoteDashboard).toHaveBeenCalledTimes(2);
  await vi.advanceTimersByTimeAsync(21);
  expect(queryRemoteDashboard).toHaveBeenCalledTimes(3);
  expect(listener).toHaveBeenCalledTimes(3);
  stop();
  await vi.advanceTimersByTimeAsync(300_000);
  expect(queryRemoteDashboard).toHaveBeenCalledTimes(3);
});

it('chunks long positive server intervals instead of overflowing into immediate polling', async () => {
  vi.useFakeTimers();
  vi.mocked(queryRemoteDashboard).mockResolvedValue({
    revision: 1, healthRevision: 1,
    sources: { composition: {
      source: 'composition', rows: [], metadata: { 'refresh-interval-ms': 4_000_000_000 }
    } }
  });
  const stop = subscribeCanonicalDashboardView('long-server-statistical', ['composition'], {
    pages: [], queries: [{ name: 'composition', from: 'statistical-runs' }]
  }, vi.fn());
  await vi.advanceTimersByTimeAsync(2_147_483_646);
  expect(queryRemoteDashboard).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(1);
  expect(queryRemoteDashboard).toHaveBeenCalledTimes(2);
  stop();
  expect(vi.getTimerCount()).toBe(0);
});

it('refreshes webhook counters without a revision and aborts polling when the view closes', async () => {
  vi.useFakeTimers();
  const counters = (/** @type {number} */ received) => ({
    revision: 1, healthRevision: 1,
    sources: { 'ingestion-webhook-status': { source: 'ingestion-webhook-status', rows: [{ received }] } }
  });
  vi.mocked(queryRemoteDashboard).mockResolvedValueOnce(counters(1)).mockResolvedValue(counters(2));
  const listener = vi.fn();
  const controller = new AbortController();
  const stop = subscribeCanonicalDashboardView('webhook-live', ['ingestion-webhook-status'], {
    pages: [], queries: [{ name: 'ingestion-webhook-status', from: 'webhook-status' }]
  }, listener, undefined, { signal: controller.signal });
  await vi.advanceTimersByTimeAsync(20);
  expect(listener.mock.calls.at(-1)?.[0]['ingestion-webhook-status'].rows).toEqual([{ received: 1 }]);
  const requestSignal = vi.mocked(queryRemoteDashboard).mock.calls[0][3]?.signal;
  expect(requestSignal).toBeInstanceOf(AbortSignal);
  await vi.advanceTimersByTimeAsync(30_000);
  await vi.advanceTimersByTimeAsync(20);
  expect(listener.mock.calls.at(-1)?.[0]['ingestion-webhook-status'].rows).toEqual([{ received: 2 }]);
  expect(queryRemoteDashboard).toHaveBeenCalledTimes(2);
  controller.abort();
  stop();
  expect(requestSignal?.aborted).toBe(true);
  await vi.advanceTimersByTimeAsync(60_000);
  expect(queryRemoteDashboard).toHaveBeenCalledTimes(2);
});

it('refreshes only subscribed collection load when time passes without a revision', async () => {
  vi.useFakeTimers();
  vi.mocked(queryRemoteDashboard).mockResolvedValue({
    revision: 1,
    healthRevision: 1,
    sources: { 'ingestion-health': { source: 'ingestion-health', rows: [{ 'webhook-load': 1 }] } }
  });
  const listener = vi.fn();
  const context = {
    pages: [],
    queries: [{ name: 'ingestion-health', from: 'collection-health' }]
  };
  const stop = subscribeCanonicalDashboardView('load', ['ingestion-health'], context, listener);
  const stopUnrelated = subscribeCanonicalDashboardView('runs', ['runs'], context, vi.fn());
  await vi.advanceTimersByTimeAsync(0);
  expect(queryRemoteDashboard).toHaveBeenCalledTimes(2);
  await vi.advanceTimersByTimeAsync(30_000);
  expect(queryRemoteDashboard).toHaveBeenCalledTimes(3);
  await vi.advanceTimersByTimeAsync(20);
  expect(listener).toHaveBeenCalledTimes(2);
  stop();
  stopUnrelated();
  await vi.advanceTimersByTimeAsync(30_000);
  expect(queryRemoteDashboard).toHaveBeenCalledTimes(3);
  expect(subscribeRemoteRevision).toHaveBeenCalledTimes(2);
});
