import 'fake-indexeddb/auto';
import { mkdtempSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  CANONICAL_DATABASE_SCHEMA, DATABASE_NAME, DATABASE_VERSION, ENTITY_STORES,
  deleteCanonicalDatabase, maintainCanonicalDatabase, openCanonicalDatabase,
  readCollection, readCollections, recordTransaction, upsertCanonicalBatch
} from '../../src/data/storage/indexeddb.js';
import { dailyRunCountContribution, evictDailyRunCounts, protectedRunCountBytes } from '../../src/data/model/daily-run-counts.js';
import { normalize } from '../../src/data/normalize/index.js';
import { CANONICAL_SCHEMA_VERSION } from '../../src/data/model/schema.js';
import { queryDatabaseSources, loadDatabaseQuerySources } from '../../src/data/queries/database.js';
import { processDataRequest } from '../../src/data-worker.js';
import { ingestNormalizedJsonl, NORMALIZED_JSONL_INGESTION_VERSION } from '../../src/data/ingest/coordinator.js';
import { createSqliteIndexedDB, installSqliteIndexedDB } from '../../src/data/storage/sqlite-indexeddb.js';
import { doctorSqliteDatabase } from '../../src/data/storage/sqlite-doctor.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { resolveDashboardQuerySources } from '../../src/data/queries/declarative.js';
import detailContracts from '../fixtures/detail-query-contracts.json' with { type: 'json' };
import goldenCounts from '../fixtures/daily-run-counts.json' with { type: 'json' };
import repositoryContract from '../fixtures/repository-run-inventory-contract.json' with { type: 'json' };

const originalIndexedDB = globalThis.indexedDB;
const originalIDBKeyRange = globalThis.IDBKeyRange;
const directories = /** @type {string[]} */ ([]);
const now = Date.parse('2026-10-15T12:00:00Z');
const fixture = {
  id: 'run:1', repositoryId: 'repository:1', workflowId: 'workflow:1',
  owner: 'octo', repository: 'demo', workflowPath: '.github/workflows/worker.md',
  startedAt: '2026-10-01T23:30:00-02:00', status: 'in_progress',
  conclusion: null, attempt: 1, updatedAt: '2026-10-02T02:00:00Z'
};
const healthQueries = [{
  name: 'health', from: 'daily-run-counts',
  aggregate: {
    by: ['organization', 'repository', 'workflow'],
    values: ['total', 'successful', 'failed', 'approval', 'pending', 'other']
      .map((field) => ({ field, as: field, reducer: 'sum' }))
  }
}];

/** @param {Record<string, unknown>[]} runs @param {IDBFactory} [factory] */
async function writeRuns(runs, factory = indexedDB) {
  return upsertCanonicalBatch(factory, { ...normalize([]), runs }, { validateRelationships: false });
}

async function counts() {
  return (await readCollections(indexedDB, ['dailyRunCounts'])).dailyRunCounts;
}

beforeEach(async () => {
  await deleteCanonicalDatabase(indexedDB);
});
afterEach(() => {
  vi.restoreAllMocks();
  globalThis.indexedDB = originalIndexedDB;
  globalThis.IDBKeyRange = originalIDBKeyRange;
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true });
});

describe('additive daily run outcome counts', () => {
  it.each(goldenCounts.cases)('matches shared cross-backend golden case: $name', async (scenario) => {
    for (const records of scenario.writes) {
      await writeRuns(records.map((record) => ({ ...goldenCounts.runDefaults, ...record })));
    }
    if (scenario.stateMode) {
      const database = await openCanonicalDatabase(indexedDB);
      await new Promise((resolve, reject) => {
        const transaction = database.transaction('dailyRunCountState', 'readwrite');
        const store = transaction.objectStore('dailyRunCountState');
        if (scenario.stateMode === 'missing') store.clear();
        else store.put({ id: 'daily-run-counts', status: null });
        transaction.oncomplete = () => resolve(undefined);
        transaction.onerror = () => reject(transaction.error);
      });
      database.close();
    }
    const source = (await queryDatabaseSources(indexedDB, {}, ['daily-run-counts']))['daily-run-counts'];
    expect(source.rows).toEqual(scenario.expectedRows);
    if (scenario.expectedAvailability) expect(source.metadata.availability).toBe(scenario.expectedAvailability);
    else expect(source.metadata).toMatchObject({
      'aggregate-version': 1, 'aggregate-coverage': 'admitted',
      completeness: scenario.excludedRuns ? 'partial' : 'complete',
      'excluded-runs': scenario.excludedRuns ?? 0
    });
  });

  it('matches every mutually exclusive runtime health class and uses UTC start dates only', () => {
    for (const [conclusion, status, category] of [
      ['success', 'queued', 'successful'],
      ...['failure', 'startup-failure', 'startup_failure', 'stale', 'timed-out', 'timed_out']
        .map((value) => [value, 'completed', 'failed']),
      ['action-required', 'queued', 'approval'], ['action_required', 'queued', 'approval'], ['cancelled', 'queued', 'pending'],
      [null, 'in_progress', 'pending'], ['unknown', '', 'other'], [null, 'completed', 'other']
    ]) {
      const row = dailyRunCountContribution({ ...fixture, conclusion, status });
      expect(row).toMatchObject({ day: '2026-10-02', total: 1, [String(category)]: 1 });
      expect(['successful', 'failed', 'approval', 'pending', 'other']
        .reduce((sum, field) => sum + Number(/** @type {Record<string, unknown>} */ (row)[field]), 0)).toBe(1);
    }
    for (const startedAt of [undefined, null, '', 'invalid', '2026-10-02', '2026-02-30T00:00:00Z']) {
      expect(dailyRunCountContribution({ ...fixture, startedAt })).toBeNull();
    }
  });

  it('replays at zero delta and replaces pending with completed from the same canonical merged run', async () => {
    await writeRuns([{ ...fixture, inputTokens: 123 }]);
    await writeRuns([fixture]);
    expect(await counts()).toEqual([expect.objectContaining({ total: 1, pending: 1, failed: 0 })]);
    await writeRuns([{
      ...fixture, conclusion: 'failure', status: 'completed', updatedAt: '2026-10-02T03:00:00Z'
    }]);
    expect(await counts()).toEqual([expect.objectContaining({ total: 1, pending: 0, failed: 1 })]);
    expect((await readCollection(indexedDB, 'runs'))[0].inputTokens).toBe(123);
    const receipt = (await readCollections(indexedDB, ['runCountContributions'])).runCountContributions[0];
    expect(receipt.run.inputTokens).toBeUndefined();
    expect(receipt.run.status).toBe('completed');
    expect(CANONICAL_SCHEMA_VERSION).toBe(36);
    expect(DATABASE_VERSION).toBe(47);
    expect(ENTITY_STORES).not.toContain('dailyRunCounts');
    expect(ENTITY_STORES).not.toContain('runCountContributions');
  });

  it('accounts protected receipt and daily bytes incrementally without making them detail-evictable', async () => {
    await writeRuns([fixture, { ...fixture, id: 'run:2' }]);
    const usage = async () => {
      const records = await readCollections(indexedDB, ['runCountContributions', 'dailyRunCounts', 'dailyRunCountState']);
      const state = records.dailyRunCountState[0];
      const bytes = (/** @type {Record<string, unknown>[]} */ rows) => rows.reduce((sum, row) =>
        sum + new TextEncoder().encode(JSON.stringify(row)).byteLength + 512, 0);
      expect(state.receiptBytes).toBe(bytes(records.runCountContributions));
      expect(state.dailyBytes).toBe(bytes(records.dailyRunCounts));
      expect(state.receiptCount).toBe(records.runCountContributions.length);
      expect(state.dailyBucketCount).toBe(records.dailyRunCounts.length);
      return protectedRunCountBytes(state);
    };
    const initial = await usage();
    await writeRuns([fixture]);
    expect(await usage()).toBe(initial);
    await writeRuns([{
      ...fixture, startedAt: '2026-10-05T00:00:00Z', updatedAt: '2026-10-06T00:00:00Z'
    }]);
    const corrected = await usage();
    expect(corrected).toBeGreaterThan(initial);
    const maintenance = await maintainCanonicalDatabase(indexedDB, { now, maxDatabaseBytes: 0 });
    expect(maintenance.estimatedBytes).toBeGreaterThanOrEqual(corrected);
    expect(await usage()).toBeGreaterThanOrEqual(corrected);
    expect(await readCollection(indexedDB, 'runs')).toEqual([]);
  });

  it('reads and publishes shared count accounting once per bounded write batch', async () => {
    const get = vi.spyOn(IDBObjectStore.prototype, 'get');
    const put = vi.spyOn(IDBObjectStore.prototype, 'put');
    const runs = Array.from({ length: 300 }, (_, index) => ({
      ...fixture, id: `run:${index}`
    }));
    await upsertCanonicalBatch(indexedDB, { ...normalize([]), runs }, {
      batchSize: 150, validateRelationships: false
    });
    const stateCalls = (/** @type {typeof get | typeof put} */ spy) =>
      spy.mock.contexts.filter((store) =>
        /** @type {IDBObjectStore} */ (store).name === 'dailyRunCountState').length;
    // Opening the database reads state once; each of the two write batches does once more.
    expect(stateCalls(get)).toBe(3);
    // Initial schema state plus one publication per batch.
    expect(stateCalls(put)).toBe(3);
    expect(await counts()).toEqual([expect.objectContaining({ total: 300, pending: 300 })]);
    expect((await readCollections(indexedDB, ['dailyRunCountState'])).dailyRunCountState[0])
      .toMatchObject({ receiptCount: 300, dailyBucketCount: 1, excludedRuns: 0 });
  });

  it('moves corrected dates, repositories and workflows without retaining the obsolete bucket', async () => {
    await writeRuns([fixture]);
    await writeRuns([{
      ...fixture, repositoryId: 'repository:2', workflowId: 'workflow:2', repository: 'other',
      workflowPath: '.github/workflows/other.md', startedAt: '2026-10-03T00:00:00Z',
      updatedAt: '2026-10-04T00:00:00Z', conclusion: 'success', status: 'completed'
    }]);
    expect(await counts()).toEqual([expect.objectContaining({
      id: JSON.stringify(['2026-10-03', 'repository:2', 'workflow:2']),
      repository: 'other', total: 1, successful: 1, pending: 0
    })]);
  });

  it('preserves receipt facts through detail eviction and fences stale attempts and timestamps', async () => {
    await writeRuns([{
      ...fixture, attempt: 2, conclusion: 'success', status: 'completed',
      updatedAt: '2026-10-03T00:00:00Z'
    }]);
    await maintainCanonicalDatabase(indexedDB, { now, maxDatabaseBytes: 0 });
    expect(await readCollection(indexedDB, 'runs')).toEqual([]);
    expect(await counts()).toEqual([expect.objectContaining({ total: 1, successful: 1 })]);
    await writeRuns([fixture]);
    await writeRuns([{
      id: fixture.id, attempt: 2, status: 'queued', updatedAt: '2026-10-02T00:00:00Z'
    }]);
    await writeRuns([{ id: fixture.id, attempt: 2, status: 'queued' }]);
    expect(await counts()).toEqual([expect.objectContaining({ total: 1, successful: 1, pending: 0 })]);
    expect((await readCollection(indexedDB, 'runs'))[0]).toEqual({
      id: fixture.id, attempt: 2, status: 'queued'
    });
  });

  it('diagnoses undated runs without fabricating dates and admits a later dated correction', async () => {
    await writeRuns([{ ...fixture, startedAt: undefined }]);
    expect(await counts()).toEqual([]);
    const missing = (await queryDatabaseSources(indexedDB, {}, ['daily-run-counts']))['daily-run-counts'];
    expect(missing.metadata).toMatchObject({ completeness: 'partial', 'excluded-runs': 1 });
    await writeRuns([{ ...fixture, updatedAt: '2026-10-03T00:00:00Z' }]);
    const present = (await queryDatabaseSources(indexedDB, {}, ['daily-run-counts']))['daily-run-counts'];
    expect(present.metadata).toMatchObject({ completeness: 'complete', 'excluded-runs': 0 });
    expect(present.rows).toEqual([expect.objectContaining({ total: 1 })]);
  });

  it('does not carry a previous attempt conclusion into a higher-attempt partial run', async () => {
    await writeRuns([{ ...fixture, status: 'completed', conclusion: 'success' }]);
    await writeRuns([{
      id: fixture.id, attempt: 2, status: 'queued', startedAt: '2026-10-04T00:00:00Z'
    }]);
    expect(await counts()).toEqual([expect.objectContaining({
      day: '2026-10-04', total: 1, successful: 0, pending: 1
    })]);
  });

  it('selects equal-explicit-revision conflicts deterministically and marks incomplete', async () => {
    const failed = { ...fixture, status: 'completed', conclusion: 'failure' };
    const successful = { ...fixture, status: 'completed', conclusion: 'success' };
    await writeRuns([failed]);
    await writeRuns([successful]);
    const first = await counts();
    await deleteCanonicalDatabase(indexedDB);
    await writeRuns([successful]);
    await writeRuns([failed]);
    expect(await counts()).toEqual(first);
    expect((await queryDatabaseSources(indexedDB, {}, ['daily-run-counts']))['daily-run-counts'].metadata)
      .toMatchObject({ completeness: 'partial', 'conflicted-runs': 1 });
    await deleteCanonicalDatabase(indexedDB);
    await writeRuns([failed, successful]);
    expect(await counts()).toEqual(first);
    expect((await queryDatabaseSources(indexedDB, {}, ['daily-run-counts']))['daily-run-counts'].metadata)
      .toMatchObject({ completeness: 'partial', 'conflicted-runs': 1 });
    await writeRuns([{ ...failed, updatedAt: '2026-10-05T00:00:00Z' }]);
    expect((await queryDatabaseSources(indexedDB, {}, ['daily-run-counts']))['daily-run-counts'].metadata)
      .toMatchObject({ completeness: 'complete', 'conflicted-runs': 0 });
  });

  it('rolls back canonical run, contribution and bucket changes together on a count write failure', async () => {
    await writeRuns([fixture]);
    const before = await readCollections(indexedDB, ['runs', 'dailyRunCounts', 'runCountContributions', 'dailyRunCountState']);
    const put = IDBObjectStore.prototype.put;
    vi.spyOn(IDBObjectStore.prototype, 'put').mockImplementation(/** @this {IDBObjectStore} */ function (record, key) {
      if (this.name === 'dailyRunCounts') throw new DOMException('injected count failure', 'QuotaExceededError');
      return put.call(this, record, key);
    });
    await expect(writeRuns([{
      ...fixture, conclusion: 'failure', status: 'completed', updatedAt: '2026-10-03T00:00:00Z'
    }])).rejects.toThrow('injected count failure');
    expect(await readCollections(indexedDB, ['runs', 'dailyRunCounts', 'runCountContributions', 'dailyRunCountState']))
      .toEqual(before);
  });

  it('uses normalized UTC revision keys for equal-revision conflicts with different timestamp offsets', async () => {
    const successful = {
      ...fixture, status: 'completed', conclusion: 'success',
      startedAt: '2026-10-01T23:30:00-02:00', updatedAt: '2026-10-02T01:45:00-02:00'
    };
    const failed = {
      ...fixture, status: 'completed', conclusion: 'failure',
      startedAt: '2026-10-02T01:30:00Z', updatedAt: '2026-10-02T03:45:00Z'
    };
    await writeRuns([successful]);
    await writeRuns([failed]);
    const first = await counts();
    expect(first).toEqual([expect.objectContaining({ successful: 1, failed: 0 })]);
    await deleteCanonicalDatabase(indexedDB);
    await writeRuns([failed]);
    await writeRuns([successful]);
    expect(await counts()).toEqual(first);
    const receipt = (await readCollections(indexedDB, ['runCountContributions'])).runCountContributions[0];
    expect(receipt.run.startedAt).toBe('2026-10-02T01:30:00.000Z');
    expect(receipt.run.updatedAt).toBe('2026-10-02T03:45:00.000Z');
  });

  it.each(['indexeddb', 'sqlite'])('bounds timeless and future-revision undated receipts in %s', async (backend) => {
    if (backend === 'sqlite') {
      const directory = mkdtempSync(join(tmpdir(), 'cao-undated-retention-'));
      directories.push(directory);
      installSqliteIndexedDB(join(directory, 'canonical.sqlite'));
    }
    const clock = Date.parse('2026-10-23T12:00:00Z');
    vi.spyOn(Date, 'now').mockReturnValue(clock);
    await writeRuns([
      { ...fixture, id: 'timeless', startedAt: null, updatedAt: null },
      { ...fixture, id: 'future-revision', startedAt: null, updatedAt: '9999-01-01T00:00:00Z' }
    ]);
    const database = await openCanonicalDatabase(indexedDB);
    await evictDailyRunCounts(database, clock);
    expect(await counts()).toEqual([]);
    expect((await readCollections(indexedDB, ['runCountContributions'])).runCountContributions)
      .toEqual([expect.objectContaining({ observedAt: clock }), expect.objectContaining({ observedAt: clock })]);
    await evictDailyRunCounts(database, Date.parse('2026-11-09T00:00:00Z'));
    database.close();
    const records = await readCollections(indexedDB, ['runCountContributions', 'dailyRunCountState']);
    expect(records.runCountContributions).toEqual([]);
    expect(records.dailyRunCountState[0]).toMatchObject({ receiptCount: 0, receiptBytes: 0, excludedRuns: 0 });
  });

  it.each(['indexeddb', 'sqlite'])('preserves the retained-day replay guard when a correction removes the start date in %s', async (backend) => {
    if (backend === 'sqlite') {
      const directory = mkdtempSync(join(tmpdir(), 'cao-undated-correction-'));
      directories.push(directory);
      installSqliteIndexedDB(join(directory, 'canonical.sqlite'));
    }
    const original = { ...fixture, startedAt: '2026-10-12T00:00:00Z' };
    await writeRuns([original]);
    const database = await openCanonicalDatabase(indexedDB);
    await evictDailyRunCounts(database, Date.parse('2026-10-23T00:00:00Z'));
    await writeRuns([{ ...original, startedAt: null, updatedAt: '2026-10-24T00:00:00Z' }]);
    await evictDailyRunCounts(database, Date.parse('2026-10-26T00:00:00Z'));
    await writeRuns([original]);
    expect(await counts()).toEqual([]);
    const receipts = (await readCollections(indexedDB, ['runCountContributions'])).runCountContributions;
    expect(receipts).toEqual([expect.objectContaining({ retentionDay: '2026-10-12', contribution: null })]);
    await evictDailyRunCounts(database, Date.parse('2026-11-02T00:00:00Z'));
    database.close();
    await writeRuns([original]);
    expect((await readCollections(indexedDB, ['runCountContributions'])).runCountContributions).toEqual([]);
    expect(await counts()).toEqual([]);
  });

  it.each(['indexeddb', 'sqlite'])('evicts weekly buckets and receipts atomically with replay fencing in %s', async (backend) => {
      if (backend === 'sqlite') {
        const directory = mkdtempSync(join(tmpdir(), 'cao-daily-retention-'));
        directories.push(directory);
        installSqliteIndexedDB(join(directory, 'canonical.sqlite'));
      }
      const expired = Array.from({ length: 251 }, (_, index) => ({
        ...fixture, id: `expired:${index}`, startedAt: '2026-10-04T23:59:59Z'
      }));
      const boundary = { ...fixture, id: 'boundary', startedAt: '2026-10-05T00:00:00Z' };
      const lastDay = { ...fixture, id: 'last-day', startedAt: '2026-10-25T23:59:59Z' };
      const future = { ...fixture, id: 'future', startedAt: '2026-10-26T00:00:00Z' };
      const undated = { ...fixture, id: 'undated', startedAt: null };
      await writeRuns([...expired, boundary, lastDay, future, undated]);
      const database = await openCanonicalDatabase(indexedDB);
      const clock = Date.parse('2026-10-23T23:00:00Z');
      await evictDailyRunCounts(database, clock);
      database.close();
      expect((await counts()).map((row) => row.day).sort()).toEqual(['2026-10-05', '2026-10-25']);
      expect((await readCollections(indexedDB, ['runCountContributions'])).runCountContributions).toHaveLength(2);
      expect(await readCollection(indexedDB, 'runs')).toHaveLength(255);
      await writeRuns([...expired, future, undated]);
      expect((await counts()).map((row) => row.day).sort()).toEqual(['2026-10-05', '2026-10-25']);
      expect((await readCollections(indexedDB, ['runCountContributions'])).runCountContributions).toHaveLength(2);
      // A correction outside the retained window leaves a compact tombstone until
      // the original bucket expires, so replay cannot resurrect its old count.
      await writeRuns([{ ...boundary, startedAt: '2026-10-04T00:00:00Z', updatedAt: '2026-10-24T00:00:00Z' }]);
      await writeRuns([boundary]);
      expect((await counts()).map((row) => row.day)).toEqual(['2026-10-25']);
      await writeRuns([{ ...boundary, startedAt: '2026-10-06T00:00:00Z', updatedAt: '2026-10-25T00:00:00Z' }]);
      expect((await counts()).map((row) => row.day).sort()).toEqual(['2026-10-06', '2026-10-25']);
      const reopened = await openCanonicalDatabase(indexedDB);
      await evictDailyRunCounts(reopened, Date.parse('2026-10-26T00:00:00Z'));
      await evictDailyRunCounts(reopened, clock);
      reopened.close();
      await writeRuns([boundary]);
      expect((await counts()).map((row) => row.day)).toEqual(['2026-10-25']);
      const records = await readCollections(indexedDB, ['runCountContributions', 'dailyRunCounts', 'dailyRunCountState']);
      const state = records.dailyRunCountState[0];
      const bytes = (/** @type {Record<string, unknown>[]} */ rows) => rows.reduce((sum, row) =>
        sum + new TextEncoder().encode(JSON.stringify(row)).byteLength + 512, 0);
      expect(state).toMatchObject({
        retentionCutoff: '2026-10-12', retentionWatermark: Date.parse('2026-10-26T00:00:00Z'),
        receiptCount: 1, dailyBucketCount: 1, excludedRuns: 0, conflictedRuns: 0,
        receiptBytes: bytes(records.runCountContributions), dailyBytes: bytes(records.dailyRunCounts)
      });
    });

    it('rolls back bucket deletion, receipts, counters and watermark together when eviction fails', async () => {
      await writeRuns([fixture]);
      const before = await readCollections(indexedDB, ['dailyRunCounts', 'runCountContributions', 'dailyRunCountState']);
      const remove = IDBObjectStore.prototype.delete;
      vi.spyOn(IDBObjectStore.prototype, 'delete').mockImplementation(/** @this {IDBObjectStore} */ function (key) {
        if (this.name === 'runCountContributions') throw new Error('injected eviction failure');
        return remove.call(this, key);
      });
      const database = await openCanonicalDatabase(indexedDB);
      await expect(evictDailyRunCounts(database, Date.parse('2026-10-26T00:00:00Z'))).rejects.toThrow('injected eviction failure');
      database.close();
      expect(await readCollections(indexedDB, ['dailyRunCounts', 'runCountContributions', 'dailyRunCountState'])).toEqual(before);
    });

  it('preserves v46 aggregate receipts while adding optional source-maintenance fields', async () => {
    await writeRuns([fixture]);
    await maintainCanonicalDatabase(indexedDB, { now, maxDatabaseBytes: 0 });
    const before = await readCollections(indexedDB, ['dailyRunCounts', 'runCountContributions', 'dailyRunCountState']);
    await deleteCanonicalDatabase(indexedDB);
    await new Promise((resolve, reject) => {
      const request = indexedDB.open(DATABASE_NAME, 46);
      request.onupgradeneeded = () => {
        for (const [name, definition] of Object.entries(CANONICAL_DATABASE_SCHEMA)) {
          const store = request.result.createObjectStore(name, { keyPath: definition.keyPath });
          for (const [index, keyPath] of Object.entries(definition.indexes)) store.createIndex(index, keyPath);
          for (const record of before[name] ?? []) store.put(record);
        }
      };
      request.onsuccess = () => { request.result.close(); resolve(undefined); };
      request.onerror = () => reject(request.error);
    });
    const database = await openCanonicalDatabase(indexedDB);
    expect(database.version).toBe(DATABASE_VERSION);
    expect(database.objectStoreNames.contains('workflowValueCollections')).toBe(true);
    database.close();
    expect(await readCollection(indexedDB, 'runs')).toEqual([]);
    expect(await readCollections(indexedDB, ['dailyRunCounts', 'runCountContributions', 'dailyRunCountState'])).toEqual(before);
    await writeRuns([fixture]);
    expect(await counts()).toEqual(before.dailyRunCounts);
  });

  it.each([44, 45, 46])('resumes an interrupted v%s backfill with bounded cursors and byte-identical retained detail', async (version) => {
    const runs = Array.from({ length: 502 }, (_, index) => ({ ...fixture, id: `run:${index}` }));
    await new Promise((resolve, reject) => {
      const request = indexedDB.open(DATABASE_NAME, version);
      request.onupgradeneeded = () => {
        for (const [name, definition] of Object.entries(CANONICAL_DATABASE_SCHEMA)) {
          if (['dailyRunCounts', 'runCountContributions', 'dailyRunCountState'].includes(name)) continue;
          const store = request.result.createObjectStore(name, { keyPath: definition.keyPath });
          for (const [index, keyPath] of Object.entries(definition.indexes)) store.createIndex(index, keyPath);
          if (name === 'runs') for (const run of runs) store.put(run);
          if (name === 'transactions') store.put({ id: 'old-receipt', kind: 'test', createdAt: fixture.updatedAt });
        }
      };
      request.onsuccess = () => { request.result.close(); resolve(undefined); };
      request.onerror = () => reject(request.error);
    });
    const getAll = vi.spyOn(IDBObjectStore.prototype, 'getAll');
    const originalPut = IDBObjectStore.prototype.put;
    let receipts = 0;
    const put = vi.spyOn(IDBObjectStore.prototype, 'put').mockImplementation(/** @this {IDBObjectStore} */ function (record, key) {
      if (this.name === 'runCountContributions' && ++receipts === 251) throw new Error('interrupted backfill');
      return originalPut.call(this, record, key);
    });
    await expect(openCanonicalDatabase(indexedDB)).rejects.toThrow('interrupted backfill');
    const checkpoint = await new Promise((resolve, reject) => {
      const request = indexedDB.open(DATABASE_NAME, DATABASE_VERSION);
      request.onsuccess = () => {
        const database = request.result;
        const state = database.transaction('dailyRunCountState').objectStore('dailyRunCountState').get('daily-run-counts');
        state.onsuccess = () => { database.close(); resolve(state.result); };
        state.onerror = () => reject(state.error);
      };
      request.onerror = () => reject(request.error);
    });
    expect(checkpoint).toMatchObject({
      status: 'backfilling', receiptCount: 250, coverage: `retained-v${version}`,
      after: [...runs].sort((a, b) => a.id.localeCompare(b.id))[249].id
    });
    put.mockImplementation(/** @this {IDBObjectStore} */ function (record, key) {
      return originalPut.call(this, record, key);
    });
    const database = await openCanonicalDatabase(indexedDB);
    database.close();
    expect(getAll).not.toHaveBeenCalled();
    expect(put.mock.instances.filter((store) => /** @type {IDBObjectStore} */ (store).name === 'runs')).toHaveLength(0);
    expect(await readCollection(indexedDB, 'runs')).toEqual([...runs].sort((a, b) => a.id.localeCompare(b.id)));
    expect((await readCollections(indexedDB, ['transactions'])).transactions).toHaveLength(1);
    expect(await counts()).toEqual([expect.objectContaining({ total: 502, pending: 502 })]);
    const source = (await queryDatabaseSources(indexedDB, {}, ['daily-run-counts']))['daily-run-counts'];
    expect(source.metadata).toMatchObject({ completeness: 'partial', 'aggregate-coverage': `retained-v${version}` });
  });

  it('serves the worker production source and native summed query after detail expiry', async () => {
    await writeRuns([fixture, {
      ...fixture, id: 'run:2', status: 'completed', conclusion: 'failure'
    }]);
    await maintainCanonicalDatabase(indexedDB, { now, maxDatabaseBytes: 0 });
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
      operation: 'load-dashboard-query-sources', sources: {},
      queries: healthQueries, sourceNames: ['health']
    }));
    expect(result.health.rows).toEqual([{
      organization: 'octo', repository: 'demo', workflow: fixture.workflowPath,
      total: 2, successful: 0, failed: 1, approval: 0, pending: 1, other: 0
    }]);
  });

  it('composes the shipped workflow health query through the worker after corrections and detail deletion', async () => {
    const queries = authoritativeDashboard.dashboard.queries;
    expect(queries.find((/** @type {{ name: string }} */ query) => query.name === 'workflow-runtime-health-summary').from).toBe('daily-run-counts');
    const query = async () => /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
      await processDataRequest({
        operation: 'load-dashboard-query-sources',
        sources: { runs: { rows: [], metadata: {
          'coverage-start': '2026-10-22T00:00:00Z', 'coverage-end': '2026-10-23T00:00:00Z',
          completeness: 'complete'
        } } },
        queries, sourceNames: ['workflow-runtime-health-summary']
      })
    )['workflow-runtime-health-summary'];
    await writeRuns([fixture, { ...fixture, id: 'run:undated', startedAt: undefined }]);
    const initial = await query();
    expect(initial.rows).toEqual([expect.objectContaining({ total: 1, pending: 1, failed: 0 })]);
    expect(initial.metadata).toMatchObject({
      'aggregate-version': 1, 'aggregate-coverage': 'admitted', completeness: 'partial', 'excluded-runs': 1
    });
    expect(initial.metadata['coverage-start']).toBeUndefined();
    expect(initial.metadata['coverage-end']).toBeUndefined();
    expect(initial.metadata['query-diagnostic']).toContain('missing start/identity facts');
    await writeRuns([{
      ...fixture, startedAt: '2026-10-03T00:00:00Z', status: 'completed', conclusion: 'failure',
      updatedAt: '2026-10-04T00:00:00Z'
    }, {
      ...fixture, id: 'run:undated', startedAt: '2026-10-04T00:00:00Z',
      status: 'completed', conclusion: 'success', updatedAt: '2026-10-05T00:00:00Z'
    }]);
    expect((await counts()).map((row) => row.day).sort()).toEqual(['2026-10-03', '2026-10-04']);
    const corrected = await query();
    expect(corrected.rows).toEqual([{
      organization: 'octo', repository: 'demo', workflow: fixture.workflowPath,
      total: 2, successful: 1, failed: 1, approval: 0, pending: 0, other: 0
    }]);
    expect(corrected.metadata).toMatchObject({
      'aggregate-version': 1, completeness: 'complete', 'excluded-runs': 0
    });
    await maintainCanonicalDatabase(indexedDB, { now, maxDatabaseBytes: 0 });
    expect(await readCollection(indexedDB, 'runs')).toEqual([]);
    const evicted = await query();
    expect(evicted.rows).toEqual(corrected.rows);
    expect(evicted.metadata).toMatchObject({
      'aggregate-version': 1, 'aggregate-coverage': 'admitted', completeness: 'complete'
    });
  });

  it('fails closed for absent state in direct and native production queries', async () => {
    await writeRuns([fixture]);
    const database = await openCanonicalDatabase(indexedDB);
    await new Promise((resolve, reject) => {
      const transaction = database.transaction('dailyRunCountState', 'readwrite');
      transaction.objectStore('dailyRunCountState').clear();
      transaction.oncomplete = () => resolve(undefined);
      transaction.onerror = () => reject(transaction.error);
    });
    database.close();
    const direct = (await queryDatabaseSources(indexedDB, {}, ['daily-run-counts']))['daily-run-counts'];
    expect(direct.metadata.availability).toBe('unavailable');
    expect(direct.rows).toEqual([]);
    const native = await loadDatabaseQuerySources(indexedDB, {}, { queries: healthQueries, sourceNames: ['health'] });
    expect(native.health.metadata.availability).toBe('unavailable');
    expect(native.health.rows).toEqual([]);
  });

  it.each(['indexeddb', 'sqlite'])('serves repository daily inventory with scoped UTC buckets and fail-closed coverage in %s', async (backend) => {
    if (backend === 'sqlite') {
      const directory = mkdtempSync(join(process.cwd(), '.repository-count-test-'));
      directories.push(directory);
      installSqliteIndexedDB(join(directory, 'canonical.sqlite'));
    }
    const queries = authoritativeDashboard.dashboard.queries;
    const page = authoritativeDashboard.dashboard.pages.find(
      (/** @type {{ id: string }} */ value) => value.id === repositoryContract.page).definition;
    for (const field of ['mode', 'campaign', 'engine', 'rollout-mode']) {
      expect(() => compileDashboardViewPayloadQueries(page, repositoryContract.page, {
        queries, viewId: repositoryContract.view,
        queryContext: { filters: { [field]: ['selected'] } }
      })).toThrow(/Daily run counts do not support the selected/);
    }
    const compile = (repository = 'octo/demo') => compileDashboardViewPayloadQueries(page, repositoryContract.page, {
      queries, viewId: repositoryContract.view, evaluatedAt: '2026-10-06T12:00:00Z',
      queryContext: {
        filters: { organization: ['octo'], repository: [repository], workflow: [fixture.workflowPath] },
        timeWindow: { start: '2026-10-02T12:00:00Z', end: '2026-10-05T00:00:00Z' }
      }
    });
    const query = async (repository = 'octo/demo') => {
      const payload = compile(repository);
      expect(resolveDashboardQuerySources(payload.queries, payload.aliases).filter(
        (name) => ['runs', 'repositories', 'daily-run-counts'].includes(name)).sort())
        .toEqual(repositoryContract.canonicalSources);
      const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
        await processDataRequest({
          operation: 'load-dashboard-query-sources', sources: {},
          queries: payload.queries, sourceNames: payload.aliases
        }));
      expect(Object.keys(result)).toEqual(payload.aliases);
      return result[payload.aliases[0]];
    };
    await upsertCanonicalBatch(indexedDB, {
      ...normalize([]), repositories: [
        { id: fixture.repositoryId, owner: 'octo', name: 'demo' },
        { id: 'repository:2', owner: 'octo', name: 'elsewhere' },
        { id: 'repository:3', owner: 'elsewhere', name: 'demo' }
      ]
    });
    const dated = { ...fixture, startedAt: '2026-10-03T23:30:00-02:00' };
    await writeRuns([
      dated,
      { ...dated, id: 'run:other-workflow', workflowId: 'workflow:2', workflowPath: 'second.md' },
      { ...dated, id: 'run:other-repo', repositoryId: 'repository:2', repository: 'elsewhere' },
      { ...dated, id: 'run:other-org', repositoryId: 'repository:3', owner: 'elsewhere' },
      { ...dated, id: 'run:lower-day', startedAt: '2026-10-02T23:00:00Z' },
      { ...dated, id: 'run:upper-day', startedAt: '2026-10-05T00:00:00Z' },
      { ...dated, id: 'run:undated', startedAt: undefined }
    ]);
    const initial = await query();
    expect(initial).toMatchObject({ rows: [expect.objectContaining({ repository: 'octo/demo', runs: 1 })] });
    expect(initial.metadata).toMatchObject({
      'aggregate-version': 1, 'aggregate-coverage': 'admitted', completeness: 'partial', 'excluded-runs': 1
    });
    expect((await query('octo/elsewhere')).rows).toEqual([
      expect.objectContaining({ repository: 'octo/elsewhere', runs: 1 })
    ]);
    await writeRuns([{ ...dated, attempt: 2, updatedAt: '2026-10-05T01:00:00Z' }]);
    await maintainCanonicalDatabase(indexedDB, { now, maxDatabaseBytes: 0 });
    expect(await readCollection(indexedDB, 'runs')).toEqual([]);
    expect((await query()).rows).toEqual(initial.rows);
    const database = await openCanonicalDatabase(indexedDB);
    await new Promise((resolve, reject) => {
      const transaction = database.transaction('dailyRunCountState', 'readwrite');
      transaction.objectStore('dailyRunCountState').clear();
      transaction.oncomplete = () => resolve(undefined);
      transaction.onerror = () => reject(transaction.error);
    });
    database.close();
    const missing = await query();
    expect(missing.metadata).toMatchObject({ availability: 'unavailable', 'aggregate-coverage': 'absent' });
    expect(missing.rows).toEqual([]);
  });

  it('serves the shipped daily chart with scoped UTC buckets, corrections, and no detail dependency', async () => {
    const contract = detailContracts.dailyRunChart;
    const queries = authoritativeDashboard.dashboard.queries;
    const page = authoritativeDashboard.dashboard.pages.find(
      (/** @type {{ id: string }} */ value) => value.id === contract.page);
    for (const field of ['mode', 'campaign', 'engine']) {
      expect(() => compileDashboardViewPayloadQueries(page, contract.page, {
        queries, viewId: contract.view,
        queryContext: { filters: { [field]: ['selected'] } }
      })).toThrow(/Daily run counts do not support the selected/);
    }
    const payload = compileDashboardViewPayloadQueries(page, contract.page, {
      queries, viewId: contract.view, evaluatedAt: '2026-10-06T12:00:00Z',
      queryContext: {
        filters: { organization: ['octo'], repository: ['demo'] },
        timeWindow: { start: '2026-10-02T12:00:00Z', end: '2026-10-05T00:00:00Z' }
      }
    });
    const dependencies = resolveDashboardQuerySources(payload.queries, payload.aliases);
    expect(dependencies).toContain(contract.source);
    expect(dependencies).not.toContain('runs');
    const query = async () => {
      const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
        await processDataRequest({
          operation: 'load-dashboard-query-sources', sources: {},
          queries: payload.queries, sourceNames: payload.aliases
        }));
      expect(Object.keys(result)).toEqual(payload.aliases);
      return result[payload.aliases[0]];
    };
    const dated = { ...fixture, startedAt: '2026-10-03T23:00:00Z' };
    await writeRuns([
      dated,
      { ...dated, id: 'run:success', status: 'completed', conclusion: 'success' },
      { ...dated, id: 'run:approval', conclusion: 'action-required' },
      { ...dated, id: 'run:other', status: 'completed', conclusion: 'cancelled' },
      { ...dated, id: 'run:failed', status: 'completed', conclusion: 'timed-out' },
      { ...dated, id: 'run:second-workflow', workflowId: 'workflow:2',
        workflowPath: '.github/workflows/second.md', status: 'completed', conclusion: 'failure' },
      { ...dated, id: 'run:other-repo', repositoryId: 'repository:2', repository: 'elsewhere' },
      { ...dated, id: 'run:other-org', repositoryId: 'repository:3', owner: 'elsewhere' },
      { ...dated, id: 'run:lower-bound-day', startedAt: '2026-10-02T23:00:00Z' },
      { ...dated, id: 'run:upper-bound-day', startedAt: '2026-10-05T00:00:00Z' },
      { ...dated, id: 'run:undated', startedAt: undefined }
    ]);
    const initial = await query();
    expect(initial.rows).toEqual(contract.classes.map((category) => ({
      day: '2026-10-03', 'runtime-health': category, runs: category === 'Failed' ? 2 : 1
    })));
    expect(initial.rows.every((row) => Object.keys(row).sort().join() === [...contract.fields].sort().join())).toBe(true);
    expect(initial.metadata).toMatchObject({
      'aggregate-version': 1, 'aggregate-coverage': 'admitted', completeness: 'partial', 'excluded-runs': 1
    });
    await writeRuns([{
      ...dated, status: 'completed', conclusion: 'failure', updatedAt: '2026-10-04T00:00:00Z'
    }]);
    await maintainCanonicalDatabase(indexedDB, { now, maxDatabaseBytes: 0 });
    expect(await readCollection(indexedDB, 'runs')).toEqual([]);
    const corrected = await query();
    expect(corrected.rows).toEqual(contract.classes.map((category) => ({
      day: '2026-10-03', 'runtime-health': category,
      runs: category === 'Failed' ? 3 : category === 'Pending' ? 0 : 1
    })));
    const database = await openCanonicalDatabase(indexedDB);
    await new Promise((resolve, reject) => {
      const transaction = database.transaction('dailyRunCountState', 'readwrite');
      transaction.objectStore('dailyRunCountState').clear();
      transaction.oncomplete = () => resolve(undefined);
      transaction.onerror = () => reject(transaction.error);
    });
    database.close();
    const missing = await query();
    expect(missing.rows).toEqual([]);
    expect(missing.metadata.availability).toBe('unavailable');
  });

  it('admits expired normalized run counts without changing detail retention', async () => {
    const header = {
      kind: 'metadata', schemaVersion: CANONICAL_SCHEMA_VERSION,
      ingestionVersion: NORMALIZED_JSONL_INGESTION_VERSION, phase: 'runs', records: 1
    };
    const payload = `${JSON.stringify(header)}\n${JSON.stringify({
      kind: 'record', collection: 'runs', record: {
        ...fixture, startedAt: '2026-10-01T00:00:00Z', updatedAt: '2026-10-01T01:00:00Z'
      }
    })}\n`;
    async function* chunks() { yield payload; }
    await ingestNormalizedJsonl(indexedDB, chunks(), {
      payloadIdentity: 'old-run', payloadScope: 'old', now,
      retentionWindowMsByStore: { runs: 86400000 }
    });
    expect(await readCollection(indexedDB, 'runs')).toEqual([]);
    expect(await counts()).toEqual([expect.objectContaining({ day: '2026-10-01', total: 1 })]);
  });

  it('persists the same receipt layer in SQLite and protects it from doctor detail repair', async () => {
    const directory = mkdtempSync(join(process.cwd(), '.daily-count-test-'));
    directories.push(directory);
    const filename = join(directory, 'dashboard.sqlite');
    const factory = installSqliteIndexedDB(filename);
    await writeRuns([fixture], factory);
    await maintainCanonicalDatabase(factory, { now, maxDatabaseBytes: 0 });
    const before = await readCollections(factory, ['dailyRunCounts', 'runCountContributions', 'dailyRunCountState']);
    await recordTransaction(factory, { id: 'expire', kind: 'test', createdAt: '2000-01-01T00:00:00Z' });
    globalThis.indexedDB = originalIndexedDB;
    globalThis.IDBKeyRange = originalIDBKeyRange;
    const repaired = await doctorSqliteDatabase(filename, { now });
    expect(repaired.healthy).toBe(true);
    expect(globalThis.indexedDB).toBe(originalIndexedDB);
    expect(globalThis.IDBKeyRange).toBe(originalIDBKeyRange);
    expect(await readCollections(factory, ['dailyRunCounts', 'runCountContributions', 'dailyRunCountState'])).toEqual(before);
    await writeRuns([fixture], factory);
    expect((await readCollections(factory, ['dailyRunCounts'])).dailyRunCounts)
      .toEqual([expect.objectContaining({ total: 1, pending: 1 })]);
    installSqliteIndexedDB(filename);
    await maintainCanonicalDatabase(factory, { now, maxDatabaseBytes: 0 });
    globalThis.indexedDB = originalIndexedDB;
    globalThis.IDBKeyRange = originalIDBKeyRange;
    const rolled = await doctorSqliteDatabase(filename, { now: Date.parse('2026-10-26T00:00:00Z'), ttlDays: 'all' });
    expect(rolled.healthy).toBe(true);
    expect(rolled.repairs.actions).toEqual(['maintain-daily-count-retention']);
    if (!rolled.repairs.backup) throw new Error('Aggregate-only cleanup must create a backup');
    expect((await readCollections(createSqliteIndexedDB(rolled.repairs.backup), ['dailyRunCounts'])).dailyRunCounts)
      .toEqual([expect.objectContaining({ total: 1, pending: 1 })]);
    await writeRuns([fixture], factory);
    expect((await readCollections(factory, ['dailyRunCounts', 'runCountContributions'])))
      .toEqual({ dailyRunCounts: [], runCountContributions: [] });
  });

  it('backfills a v44 Activity SQLite snapshot through bounded reads without losing its detail', async () => {
    const directory = mkdtempSync(join(process.cwd(), '.daily-count-test-'));
    directories.push(directory);
    const factory = installSqliteIndexedDB(join(directory, 'dashboard.sqlite'));
    await new Promise((resolve, reject) => {
      const request = factory.open(DATABASE_NAME, 44);
      request.onupgradeneeded = () => {
        for (const [name, definition] of Object.entries(CANONICAL_DATABASE_SCHEMA)) {
          if (['dailyRunCounts', 'runCountContributions', 'dailyRunCountState'].includes(name)) continue;
          const store = request.result.createObjectStore(name, { keyPath: definition.keyPath });
          for (const [index, keyPath] of Object.entries(definition.indexes)) store.createIndex(index, keyPath);
          if (name === 'runs') for (let index = 0; index < 251; index += 1) {
            store.put({ ...fixture, id: `run:${index}` });
          }
        }
      };
      request.onsuccess = () => { request.result.close(); resolve(undefined); };
      request.onerror = () => reject(request.error);
    });
    const database = await openCanonicalDatabase(factory);
    database.close();
    expect(await readCollection(factory, 'runs')).toHaveLength(251);
    const rows = (await readCollections(factory, ['dailyRunCounts'])).dailyRunCounts;
    expect(rows).toEqual([expect.objectContaining({ total: 251, pending: 251 })]);
    const source = (await queryDatabaseSources(factory, {}, ['daily-run-counts']))['daily-run-counts'];
    expect(source.metadata).toMatchObject({
      'aggregate-version': 1, 'aggregate-coverage': 'retained-v44', completeness: 'partial'
    });
  });
});
