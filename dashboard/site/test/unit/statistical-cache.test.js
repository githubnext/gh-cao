import { webcrypto } from 'node:crypto';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { IDBFactory, IDBKeyRange, IDBObjectStore } from 'fake-indexeddb';
import fixture from '../fixtures/statistical-sampling.json' with { type: 'json' };
import {
  configureStatisticalRefresh, createStatisticalCache, hostedStatisticalRefreshInterval,
  statisticalRefreshInterval, subscribeStatisticalRefresh
} from '../../src/data/queries/statistical-cache.js';
import { invalidateStatisticalCaches, loadDatabaseQuerySources, queryDatabaseSources } from '../../src/data/queries/database.js';
import { upsertCanonicalBatch } from '../../src/data/storage/indexeddb.js';
import * as sampling from '../../src/data/queries/statistical-sample.js';
import { DashboardQueryCancelledError } from '../../src/data/queries/declarative.js';
import { normalize } from '../../src/data/normalize/index.js';

afterEach(() => {
  configureStatisticalRefresh({});
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('lazy statistical cache', () => {
  it.each([undefined, 1_000, 30_000])('throttles attempts at the exact configured boundary (%s)', async (configured) => {
    let time = 0;
    const interval = statisticalRefreshInterval(configured);
    const cache = createStatisticalCache({ now: () => time, interval: () => interval });
    const build = vi.fn(async () => ({ value: build.mock.calls.length }));
    expect((await cache.get('scope', 'one', build)).value).toEqual({ value: 1 });
    time = interval - 1;
    expect(await cache.get('scope', 'two', build)).toMatchObject({ stale: true, sourceRevision: 'one' });
    expect(build).toHaveBeenCalledTimes(1);
    time++;
    expect(await cache.get('scope', 'two', build)).toMatchObject({ stale: false, value: { value: 2 } });
    time += interval * 10;
    await cache.get('scope', 'two', build);
    expect(build).toHaveBeenCalledTimes(2);
  });

  it('shares in-flight attempts and throttles failures, cancellation, and invalidation', async () => {
    let time = 0;
    const cache = createStatisticalCache({ now: () => time, interval: () => 1_000 });
    /** @type {(value: number) => void} */
    let release = () => {};
    const build = vi.fn(() => new Promise((resolve) => { release = resolve; }));
    const first = cache.get('scope', 'one', build);
    const second = cache.get('scope', 'one', build);
    await Promise.resolve();
    expect(build).toHaveBeenCalledTimes(1);
    release(1);
    expect((await first).value).toBe(1);
    expect((await second).value).toBe(1);
    const failed = vi.fn(async () => { throw new DashboardQueryCancelledError('cancelled', 'aborted'); });
    time = 1_000;
    expect(await cache.get('scope', 'two', failed)).toMatchObject({ value: 1, stale: true, error: expect.any(Error) });
    time = 1_999;
    await cache.get('scope', 'two', failed);
    expect(failed).toHaveBeenCalledTimes(1);
    cache.invalidate();
    expect((await cache.get('scope', 'two', failed)).value).toBeUndefined();
    expect(failed).toHaveBeenCalledTimes(1);
    time = 2_000;
    await cache.get('scope', 'two', failed);
    expect(failed).toHaveBeenCalledTimes(2);
  });

  it('discards an in-flight result after authority withdrawal without resetting its ledger', async () => {
    const cache = createStatisticalCache({ now: () => 0, interval: () => 1_000 });
    /** @type {(value: string) => void} */
    let release = () => {};
    const build = vi.fn(() => new Promise((resolve) => { release = resolve; }));
    const pending = cache.get('scope', 'one', build);
    await Promise.resolve();
    cache.invalidate();
    release('withdrawn');
    expect((await pending).value).toBeUndefined();
    expect((await cache.get('scope', 'two', build)).value).toBeUndefined();
    expect(build).toHaveBeenCalledTimes(1);
  });

  it('does not reset the attempt clock when the interval increases', async () => {
    let time = 0;
    let interval = 1_000;
    const cache = createStatisticalCache({ now: () => time, interval: () => interval });
    const build = vi.fn(async () => 1);
    await cache.get('scope', 'one', build);
    interval = 2_000;
    time = 1_000;
    await cache.get('scope', 'two', build);
    expect(build).toHaveBeenCalledTimes(1);
    time = 2_000;
    await cache.get('scope', 'two', build);
    expect(build).toHaveBeenCalledTimes(2);
  });

  it.each([0, -1, 0.5, '300000', NaN, Infinity, Number.MAX_SAFE_INTEGER + 1])('rejects invalid interval %s', (value) => {
    expect(() => configureStatisticalRefresh({ statisticalRefreshIntervalMs: value })).toThrow(/positive safe integer/);
  });

  it('uses valid hosted cadence metadata without truncating long intervals or accepting malformed values', () => {
    expect(hostedStatisticalRefreshInterval({
      valid: { metadata: { 'refresh-interval-ms': 60_000 } },
      malformed: { metadata: { 'refresh-interval-ms': -1 } }
    }, 300_000)).toBe(60_000);
    expect(hostedStatisticalRefreshInterval({
      malformed: { metadata: { 'refresh-interval-ms': '60000' } }
    }, 300_000)).toBe(300_000);
    expect(hostedStatisticalRefreshInterval({
      long: { metadata: { 'refresh-interval-ms': 4_000_000_000 } }
    }, 300_000)).toBe(4_000_000_000);
  });

  it('bounds timer delays without shortening long cache eligibility intervals', async () => {
    vi.useFakeTimers();
    configureStatisticalRefresh({ statisticalRefreshIntervalMs: 4_000_000_000 });
    let time = 0;
    const cache = createStatisticalCache({ now: () => time });
    const build = vi.fn(async () => 1);
    await cache.get('scope', 'one', build);
    const controller = new AbortController();
    const refresh = vi.fn(() => cache.get('scope', 'two', build));
    subscribeStatisticalRefresh(refresh, controller.signal);
    time = 2_147_483_646;
    await vi.advanceTimersByTimeAsync(time);
    expect(refresh).not.toHaveBeenCalled();
    time++;
    await vi.advanceTimersByTimeAsync(1);
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(build).toHaveBeenCalledTimes(1);
    time = 4_294_967_294;
    await vi.advanceTimersByTimeAsync(2_147_483_647);
    expect(refresh).toHaveBeenCalledTimes(2);
    expect(build).toHaveBeenCalledTimes(2);
    controller.abort();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('schedules active demand only and removes timers on stop or abort', async () => {
    vi.useFakeTimers();
    configureStatisticalRefresh({ statisticalRefreshIntervalMs: 1_000 });
    const refresh = vi.fn();
    const controller = new AbortController();
    const stop = subscribeStatisticalRefresh(refresh, controller.signal);
    await vi.advanceTimersByTimeAsync(999);
    expect(refresh).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(refresh).toHaveBeenCalledTimes(1);
    controller.abort();
    expect(vi.getTimerCount()).toBe(0);
    await vi.advanceTimersByTimeAsync(2_000);
    expect(refresh).toHaveBeenCalledTimes(1);
    stop();
    const stopped = subscribeStatisticalRefresh(refresh, new AbortController().signal);
    stopped();
    expect(vi.getTimerCount()).toBe(0);
  });

  it('does not resurrect a stopped timer when its running refresh finishes', async () => {
    vi.useFakeTimers();
    /** @type {() => void} */
    let release = () => {};
    const refresh = vi.fn(() => new Promise((resolve) => { release = () => resolve(undefined); }));
    const stop = subscribeStatisticalRefresh(refresh, new AbortController().signal, () => 1_000);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(refresh).toHaveBeenCalledTimes(1);
    stop();
    release();
    await vi.advanceTimersByTimeAsync(2_000);
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });
});

describe('canonical lazy sample snapshots', () => {
  async function setup() {
    vi.stubGlobal('crypto', webcrypto);
    vi.stubGlobal('IDBKeyRange', IDBKeyRange);
    const factory = new IDBFactory();
    await upsertCanonicalBatch(factory, { ...normalize([]), runs: fixture.rows }, { validateRelationships: false });
    return factory;
  }
  const sources = { runs: { rows: [], metadata: {
    availability: 'available', completeness: 'complete', freshness: 'fresh', 'as-of': fixture.watermark
  } } };

  it('does not consume a refresh attempt before cold ingestion supplies Run evidence or a frame clock', async () => {
    vi.stubGlobal('crypto', webcrypto);
    vi.stubGlobal('IDBKeyRange', IDBKeyRange);
    const factory = new IDBFactory();
    const sample = vi.spyOn(sampling, 'sampleStatisticalRuns');
    const cold = await queryDatabaseSources(factory, {}, ['statistical-runs']);
    expect(cold['statistical-runs'].metadata).toMatchObject({
      availability: 'unavailable', error: expect.stringContaining('awaiting canonical Run evidence')
    });
    expect(sample).not.toHaveBeenCalled();
    await upsertCanonicalBatch(factory, { ...normalize([]), runs: fixture.rows }, { validateRelationships: false });
    const ingested = await queryDatabaseSources(factory, {}, ['statistical-runs']);
    expect(ingested['statistical-runs'].rows.length).toBeGreaterThan(0);
    expect(ingested['statistical-runs'].metadata.availability).toBe('available');
    expect(sample).toHaveBeenCalledTimes(1);
  });

  it('keeps complete old summaries across revisions, scopes detail reads, then rebuilds on demand', async () => {
    const sample = vi.spyOn(sampling, 'sampleStatisticalRuns');
    const factory = await setup();
    expect(sample).not.toHaveBeenCalled();
    let time = 0;
    vi.spyOn(performance, 'now').mockImplementation(() => time);
    configureStatisticalRefresh({ statisticalRefreshIntervalMs: 1_000 });
    const reads = vi.spyOn(IDBObjectStore.prototype, 'getAll');
    const [initial, concurrent] = await Promise.all([
      queryDatabaseSources(factory, sources, ['statistical-runs']),
      queryDatabaseSources(factory, sources, ['statistical-runs'])
    ]);
    expect(sample).toHaveBeenCalledTimes(1);
    expect(concurrent).toEqual(initial);
    expect(reads.mock.instances.some((store) =>
      ['tools', 'domains', 'skills', 'audits'].includes(/** @type {IDBObjectStore} */ (store).name))).toBe(false);
    const old = initial['statistical-runs'];
    const id = old.rows[0].id;
    const run = fixture.rows.find((row) => row.id === id);
    if (!run) throw new Error('Fixture sample member missing');
    await upsertCanonicalBatch(factory, {
      ...normalize([]),
      runs: [{ ...run, title: 'changed summary', updatedAt: '2026-03-08T11:00:00Z' }],
      tools: [{ id: 'tool', runId: run.id, name: 'selected' }, { id: 'other', runId: fixture.rows[7].id, name: 'outside' }]
    }, { validateRelationships: false });
    const stale = await queryDatabaseSources(factory, sources, ['runs', 'statistical-runs', 'statistical-tools']);
    expect(stale['statistical-runs'].rows).toEqual(old.rows);
    expect(stale['statistical-runs'].metadata).toMatchObject({ freshness: 'stale', 'sample-stale': true });
    expect(stale['statistical-tools'].metadata.availability).toBe('unavailable');
    expect(stale.runs.rows.find((row) => row.id === id)?.['run-title']).toBe('changed summary');
    expect(sample).toHaveBeenCalledTimes(1);
    time = 1_000;
    const fresh = await queryDatabaseSources(factory, sources, ['statistical-runs', 'statistical-tools']);
    expect(sample).toHaveBeenCalledTimes(2);
    expect(fresh['statistical-runs'].rows.find((row) => row.id === id)?.['run-title']).toBe('changed summary');
    expect(fresh['statistical-tools'].rows.map((row) => row.id)).toEqual(['tool']);
    expect(fresh['statistical-tools'].metadata['statistical-frame']).toEqual(fresh['statistical-runs'].metadata['statistical-frame']);
    time = 10_000;
    await queryDatabaseSources(factory, sources, ['statistical-runs']);
    expect(sample).toHaveBeenCalledTimes(2);
    await upsertCanonicalBatch(factory, {
      ...normalize([]), tools: [{ id: 'later', runId: run.id, name: 'new revision' }]
    }, { validateRelationships: false });
    sample.mockRejectedValueOnce(new Error('refresh failed'));
    const failed = await queryDatabaseSources(factory, sources, ['statistical-runs']);
    expect(failed['statistical-runs'].rows).toEqual(fresh['statistical-runs'].rows);
    expect(failed['statistical-runs'].metadata).toMatchObject({
      'sample-stale': true, 'sample-refresh-error': expect.stringContaining('refresh failed'), 'refresh-interval-ms': 1_000
    });
    time = 10_999;
    await queryDatabaseSources(factory, sources, ['statistical-runs']);
    expect(sample).toHaveBeenCalledTimes(3);
    time++;
    await queryDatabaseSources(factory, sources, ['statistical-runs']);
    expect(sample).toHaveBeenCalledTimes(4);
  });

  it('fails closed after authority changes and throttles failures without hiding exact data', async () => {
    const factory = await setup();
    let time = 0;
    vi.spyOn(performance, 'now').mockImplementation(() => time);
    const sample = vi.spyOn(sampling, 'sampleStatisticalRuns');
    sample.mockRejectedValueOnce(new Error('bounded failure'));
    const failed = await queryDatabaseSources(factory, sources, ['statistical-runs', 'runs']);
    expect(failed['statistical-runs'].metadata.availability).toBe('unavailable');
    expect(failed.runs.rows.length).toBeGreaterThan(0);
    time = 299_999;
    await queryDatabaseSources(factory, sources, ['statistical-runs']);
    expect(sample).toHaveBeenCalledTimes(1);
    time++;
    const fresh = await queryDatabaseSources(factory, sources, ['statistical-runs']);
    expect(fresh['statistical-runs'].rows.length).toBeGreaterThan(0);
    invalidateStatisticalCaches();
    const invalidated = await queryDatabaseSources(factory, sources, ['statistical-runs']);
    expect(invalidated['statistical-runs'].metadata.availability).toBe('unavailable');
    expect(sample).toHaveBeenCalledTimes(2);
    time += 300_000;
    await queryDatabaseSources(factory, sources, ['statistical-runs']);
    const withdrawn = await queryDatabaseSources(factory, {
      runs: { metadata: { availability: 'unavailable' } }
    }, ['statistical-runs']);
    expect(withdrawn['statistical-runs'].metadata.availability).toBe('unavailable');
    expect(sample).toHaveBeenCalledTimes(3);
  });

  it('shares a frame before different declarative consumer filters', async () => {
    const factory = await setup();
    const sample = vi.spyOn(sampling, 'sampleStatisticalRuns');
    const queries = ['success', 'failure'].map((outcome) => ({
      name: outcome, from: 'statistical-runs',
      filter: { predicates: [{ field: 'run-conclusion', equals: outcome }] }
    }));
    const success = await loadDatabaseQuerySources(factory, sources, { queries, sourceNames: ['success'] });
    const failure = await loadDatabaseQuerySources(factory, sources, { queries, sourceNames: ['failure'] });
    expect(sample).toHaveBeenCalledTimes(1);
    expect(success.success.metadata['statistical-frame']).toEqual(failure.failure.metadata['statistical-frame']);
  });

  it('preserves cancellation and the failed-attempt throttle through the production loader', async () => {
    const factory = await setup();
    const sample = vi.spyOn(sampling, 'sampleStatisticalRuns');
    sample.mockRejectedValueOnce(new DashboardQueryCancelledError('cancelled sample', 'aborted'));
    await expect(queryDatabaseSources(factory, sources, ['statistical-runs'])).rejects.toThrow('cancelled sample');
    await expect(queryDatabaseSources(factory, sources, ['statistical-runs'])).rejects.toThrow('cancelled sample');
    expect(sample).toHaveBeenCalledTimes(1);
  });
});
