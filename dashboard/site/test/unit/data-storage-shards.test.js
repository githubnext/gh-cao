import 'fake-indexeddb/auto';
import { IDBFactory } from 'fake-indexeddb';
import { describe, expect, it, vi } from 'vitest';
import {
  maintainCanonicalDatabase, openCanonicalDatabase, prepareCanonicalRecord,
  readCanonicalBatch, readCollection, replaceCanonicalBatch, upsertCanonicalBatch
} from '../../src/data/storage/indexeddb.js';
import { ingestNormalizedJsonl } from '../../src/data/ingest/coordinator.js';
import { normalize } from '../../src/data/normalize/index.js';
import { CANONICAL_SCHEMA_VERSION } from '../../src/data/model/schema.js';

const now = Date.parse('2026-10-10T12:00:00Z');
const options = { now, maxDatabaseBytes: Number.MAX_SAFE_INTEGER };

/** @param {IDBFactory} indexedDB */
async function totals(indexedDB) {
  const database = await openCanonicalDatabase(indexedDB);
  try {
    return await new Promise((resolve, reject) => {
      const request = database.transaction('storageShards').objectStore('storageShards').get('totals');
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
  } finally {
    database.close();
  }
}

/** @param {IDBFactory} indexedDB */
async function expectAccurateTotals(indexedDB) {
  const batch = await readCanonicalBatch(indexedDB);
  let bytes = 0;
  let count = 0;
  for (const [store, records] of Object.entries(batch)) {
    for (const record of records) {
      bytes += prepareCanonicalRecord(store, record)._storage.bytes;
      count += 1;
    }
  }
  expect(await totals(indexedDB)).toMatchObject({ bytes, count });
}

describe('incremental canonical storage shards', () => {
  it('preserves usage through the production normalized-shard ingestion boundary', async () => {
    const indexedDB = new IDBFactory();
    const run = { id: 'run:303', attempt: 1, startedAt: '2026-10-09T00:00:00Z' };
    const ingest = (/** @type {Record<string, unknown>} */ record, /** @type {string} */ scope) => {
      const content = [
        { kind: 'metadata', schemaVersion: CANONICAL_SCHEMA_VERSION, ingestionVersion: 4, phase: 'runs', records: 1 },
        { kind: 'record', collection: 'runs', record }
      ].map((row) => JSON.stringify(row)).join('\n');
      async function* chunks() { yield content; }
      return ingestNormalizedJsonl(indexedDB, chunks(), {
        now, payloadIdentity: scope, payloadScope: scope, expectedPhase: 'runs', deferMaintenance: true
      });
    };
    await ingest({ ...run, aicTotal: 172.72252, inputTokens: 100,
      routingAggregateVersion: 1, routingState: 'selected', routingBaselineAic: 6.02079 }, 'measured');
    await ingest({ ...run, aicTotal: null, inputTokens: null }, 'sparse-refresh');
    expect((await readCollection(indexedDB, 'runs'))[0]).toMatchObject({
      aicTotal: 172.72252, inputTokens: 100,
      routingAggregateVersion: 1, routingState: 'selected', routingBaselineAic: 6.02079
    });
    await expect(ingest({ ...run, aicTotal: null, inputTokens: null }, 'sparse-refresh'))
      .resolves.toMatchObject({ skipped: true });
    await expectAccurateTotals(indexedDB);
    await ingest({ ...run, routingAggregateVersion: 1, routingState: 'unrouted', routingBaselineAic: null }, 'replacement');
    expect((await readCollection(indexedDB, 'runs'))[0]).toMatchObject({
      routingState: 'unrouted', routingBaselineAic: null
    });
    await ingest({ ...run, attempt: 2 }, 'rerun');
    expect((await readCollection(indexedDB, 'runs'))[0]).not.toHaveProperty('routingAggregateVersion');
    expect((await readCollection(indexedDB, 'runs'))[0]).not.toHaveProperty('routingBaselineAic');
    await expectAccurateTotals(indexedDB);
  });

  it('retains measured run usage across transport batches with accurate accounting and rerun isolation', async () => {
    const indexedDB = new IDBFactory();
    const run = { id: 'run:1', attempt: 1, startedAt: '2026-10-01T00:00:00Z' };
    const write = (/** @type {Record<string, unknown>[]} */ runs) =>
      upsertCanonicalBatch(indexedDB, { ...normalize([]), runs }, { validateRelationships: false });
    await write([{ ...run, aicTotal: 2.5, inputTokens: 100, outputTokens: 0 }]);
    await write([{ ...run, aicTotal: null, inputTokens: null, outputTokens: null, conclusion: 'success' }]);
    expect((await readCollection(indexedDB, 'runs'))[0]).toMatchObject({
      aicTotal: 2.5, inputTokens: 100, outputTokens: 0, conclusion: 'success'
    });
    await expectAccurateTotals(indexedDB);
    await write([{ ...run, aicTotal: 4 }, { ...run, aicTotal: null }]);
    expect((await readCollection(indexedDB, 'runs'))[0].aicTotal).toBe(4);
    await expectAccurateTotals(indexedDB);
    await write([{ ...run, attempt: 2, aicTotal: null }]);
    expect((await readCollection(indexedDB, 'runs'))[0]).toMatchObject({ attempt: 2, aicTotal: null });
    expect((await readCollection(indexedDB, 'runs'))[0]).not.toHaveProperty('inputTokens');
    await expectAccurateTotals(indexedDB);
    await write([{ ...run, attempt: 2, aicTotal: 0 }]);
    await write([{ ...run, attempt: 2, aicTotal: null }]);
    expect((await readCollection(indexedDB, 'runs'))[0].aicTotal).toBe(0);
    await expectAccurateTotals(indexedDB);
  });

  it('rechecks a retained audit when its last evidence reference expires', async () => {
    const indexedDB = new IDBFactory();
    const batch = normalize([]);
    batch.runs = [{ id: 'run:1', startedAt: '2026-10-01T00:00:00Z' }];
    batch.audits = [{
      id: 'audit:1', runId: 'run:1', timestamp: '2026-10-09T12:00:00Z',
      source: 'gh-aw-logs', type: 'workflow_run_working_set', status: 'observed',
      summary: 'Working set measured'
    }];
    batch.experimentAssignments = [{
      id: 'assignment:1', runId: 'run:1', experimentId: 'experiment:1',
      timestamp: '2026-10-01T12:00:00Z', auditId: 'audit:1'
    }];
    await upsertCanonicalBatch(indexedDB, batch, { validateRelationships: false });
    expect((await maintainCanonicalDatabase(indexedDB, {
      ...options, now: Date.parse('2026-10-02T12:00:00Z')
    })).prunedAudits).toBe(0);
    expect((await maintainCanonicalDatabase(indexedDB, options)).prunedAudits).toBe(1);
    expect(await readCollection(indexedDB, 'audits')).toEqual([]);
    await expectAccurateTotals(indexedDB);
  });

  it('fails closed when shard accounting has lost its totals', async () => {
    const indexedDB = new IDBFactory();
    await upsertCanonicalBatch(indexedDB, {
      ...normalize([]), repositories: [{ id: 'repo:1' }]
    });
    const database = await openCanonicalDatabase(indexedDB);
    try {
      await new Promise((resolve, reject) => {
        const transaction = database.transaction('storageShards', 'readwrite');
        transaction.objectStore('storageShards').delete('totals');
        transaction.oncomplete = resolve;
        transaction.onabort = () => reject(transaction.error);
      });
    } finally {
      database.close();
    }
    await expect(maintainCanonicalDatabase(indexedDB, options)).rejects.toThrow('shard totals are missing');
  });

  it('does not read or reserialize unaffected entity records during maintenance', async () => {
    const indexedDB = new IDBFactory();
    const batch = normalize([]);
    batch.runs = [{ id: 'run:old-summary', startedAt: '2026-01-01T00:00:00Z' }];
    batch.tools = Array.from({ length: 2000 }, (_, index) => ({
      id: `tool:${index}`, runId: 'run:old-summary', timestamp: '2026-10-09T12:00:00Z'
    }));
    await upsertCanonicalBatch(indexedDB, batch, { validateRelationships: false });
    const getAll = vi.spyOn(IDBObjectStore.prototype, 'getAll');
    const indexGetAll = vi.spyOn(IDBIndex.prototype, 'getAll');
    const cursor = vi.spyOn(IDBObjectStore.prototype, 'openCursor');
    const stringify = vi.spyOn(JSON, 'stringify');
    try {
      const result = await maintainCanonicalDatabase(indexedDB, options);
      expect(result).toMatchObject({ deletedRecords: 0, retainedRecords: 2001 });
      expect(indexGetAll).not.toHaveBeenCalled();
      expect(cursor).not.toHaveBeenCalled();
      expect(stringify.mock.calls).toEqual([
        [expect.objectContaining({ id: 'run:old-summary', contribution: null })],
        [expect.objectContaining({ id: 'daily-run-counts', status: 'ready' })]
      ]);
      for (const [index, store] of getAll.mock.contexts.entries()) {
        expect(store).toBeInstanceOf(IDBObjectStore);
        const objectStore = /** @type {IDBObjectStore} */ (store);
        expect(['storageShards', 'dailyRunCounts', 'runCountContributions']).toContain(objectStore.name);
        expect(getAll.mock.calls[index][1]).toBe(objectStore.name === 'storageShards' ? 1000 : 250);
      }
    } finally {
      vi.restoreAllMocks();
    }
    await expectAccurateTotals(indexedDB);
  });

  it('fails closed when totals disagree with physical shard estimates', async () => {
    const indexedDB = new IDBFactory();
    await upsertCanonicalBatch(indexedDB, {
      ...normalize([]), repositories: [{ id: 'repo:1' }]
    });
    const before = await totals(indexedDB);
    const database = await openCanonicalDatabase(indexedDB);
    try {
      await new Promise((resolve, reject) => {
        const transaction = database.transaction('storageShards', 'readwrite');
        transaction.objectStore('storageShards').put({ ...before, bytes: before.bytes + 1 });
        transaction.oncomplete = resolve;
        transaction.onabort = () => reject(transaction.error);
      });
    } finally {
      database.close();
    }
    await expect(maintainCanonicalDatabase(indexedDB, options)).rejects.toThrow('shard totals are inconsistent');
  });

  it('expires whole and boundary-day shards in bounded indexed batches', async () => {
    const indexedDB = new IDBFactory();
    const batch = normalize([]);
    batch.runs = [{ id: 'run:1', startedAt: '2026-01-01T00:00:00Z' }];
    batch.tools = [
      ...Array.from({ length: 2105 }, (_, index) => ({
        id: `expired:${index}`, runId: 'run:1', timestamp: '2026-10-01T00:00:00Z'
      })),
      { id: 'boundary:expired', runId: 'run:1', timestamp: '2026-10-03T11:59:59Z' },
      { id: 'boundary:retained', runId: 'run:1', timestamp: '2026-10-03T12:00:00Z' },
      { id: 'missing:expired', runId: 'run:1' }
    ];
    batch.operationalValues = [{ id: 'value:1', timestamp: '2026-10-01T00:00:00Z' }];
    await upsertCanonicalBatch(indexedDB, batch, { validateRelationships: false });
    const reads = vi.spyOn(IDBIndex.prototype, 'getAll');
    const deletes = vi.spyOn(IDBCursor.prototype, 'delete');
    try {
      const result = await maintainCanonicalDatabase(indexedDB, options);
      expect(result.deletedRecords).toBe(2107);
      expect(reads.mock.calls.every(([range, count]) => range instanceof IDBKeyRange && count === 1000)).toBe(true);
      expect(deletes).not.toHaveBeenCalled();
    } finally {
      vi.restoreAllMocks();
    }
    expect((await readCollection(indexedDB, 'tools')).map(({ id }) => id)).toEqual(['boundary:retained']);
    expect(await readCollection(indexedDB, 'runs')).toHaveLength(1);
    expect(await readCollection(indexedDB, 'operationalValues')).toHaveLength(1);
    await expectAccurateTotals(indexedDB);
  }, 30_000);

  it('accounts duplicate IDs, overwrites, shard moves, and replacements exactly once', async () => {
    const indexedDB = new IDBFactory();
    const batch = normalize([]);
    batch.tools = [
      { id: 'tool:1', runId: 'run:1', timestamp: '2026-10-08T12:00:00Z', summary: 'first' },
      { id: 'tool:1', runId: 'run:1', timestamp: '2026-10-09T12:00:00Z', summary: 'last' }
    ];
    await upsertCanonicalBatch(indexedDB, batch, { validateRelationships: false });
    await expectAccurateTotals(indexedDB);
    batch.tools = [{ ...batch.tools[1], timestamp: '2026-10-10T12:00:00Z', summary: 'larger replacement' }];
    await upsertCanonicalBatch(indexedDB, batch, { validateRelationships: false });
    await expectAccurateTotals(indexedDB);
    await replaceCanonicalBatch(indexedDB, normalize([]));
    await expectAccurateTotals(indexedDB);
    expect(await totals(indexedDB)).toMatchObject({ bytes: 0, count: 0 });
  });

  it('rolls back canonical writes together with failed shard accounting', async () => {
    const indexedDB = new IDBFactory();
    const batch = normalize([]);
    batch.repositories = [{ id: 'repo:1', name: 'original' }];
    await upsertCanonicalBatch(indexedDB, batch);
    const before = await totals(indexedDB);
    const originalPut = IDBObjectStore.prototype.put;
    const put = vi.spyOn(IDBObjectStore.prototype, 'put').mockImplementation(
      /** @this {IDBObjectStore} */ function (...args) {
        if (this.name === 'storageShards') throw new Error('simulated accounting failure');
        return originalPut.apply(this, args);
      }
    );
    try {
      await expect(upsertCanonicalBatch(indexedDB, {
        ...batch, repositories: [{ id: 'repo:1', name: 'replacement' }]
      })).rejects.toThrow('simulated accounting failure');
    } finally {
      put.mockRestore();
    }
    expect(await totals(indexedDB)).toEqual(before);
    expect(await readCollection(indexedDB, 'repositories')).toEqual(batch.repositories);
  });

  it('rechecks only audits belonging to runs whose curation facts changed', async () => {
    const indexedDB = new IDBFactory();
    const batch = normalize([]);
    batch.runs = [{ id: 'run:1', startedAt: '2026-10-09T11:00:00Z', status: 'completed' }];
    batch.audits = [{
      id: 'audit:1', runId: 'run:1', timestamp: '2026-10-09T12:00:00Z',
      source: 'gh-aw-logs', type: 'workflow_run_started', status: 'completed', summary: ''
    }];
    await upsertCanonicalBatch(indexedDB, batch, { validateRelationships: false });
    await maintainCanonicalDatabase(indexedDB, options);
    expect(await readCollection(indexedDB, 'audits')).toHaveLength(1);
    await upsertCanonicalBatch(indexedDB, {
      ...normalize([]), runs: [{ ...batch.runs[0], startedAt: '2026-10-09T12:00:00Z' }]
    }, { validateRelationships: false });
    expect((await maintainCanonicalDatabase(indexedDB, options)).prunedAudits).toBe(1);
    await expectAccurateTotals(indexedDB);
  });

  it('validates expired transport records and replays longer retention profiles', async () => {
    const indexedDB = new IDBFactory();
    const record = {
      id: 'tool:old', runId: 'run:1', timestamp: '2026-09-30T12:00:00Z'
    };
    const stream = (declared = 1) => async function* () {
      yield [
        { kind: 'metadata', schemaVersion: CANONICAL_SCHEMA_VERSION, ingestionVersion: 4, phase: 'records', records: declared },
        { kind: 'record', collection: 'tools', record }
      ].map((line) => JSON.stringify(line)).join('\n');
    };
    const ingestionOptions = {
      ...options, payloadIdentity: 'old-detail', payloadScope: 'fixture:old-detail', deferMaintenance: true
    };
    await expect(ingestNormalizedJsonl(indexedDB, stream(2)(), ingestionOptions))
      .rejects.toThrow('declared 2 records but contained 1');
    await expect(ingestNormalizedJsonl(indexedDB, stream()(), ingestionOptions))
      .resolves.toMatchObject({ committedRecords: 0, expiredRecords: 1 });
    expect(await readCollection(indexedDB, 'tools')).toEqual([]);
    await expect(ingestNormalizedJsonl(indexedDB, stream()(), ingestionOptions))
      .resolves.toMatchObject({ skipped: true });
    await expect(ingestNormalizedJsonl(indexedDB, stream()(), {
      ...ingestionOptions, retentionWindowMs: 30 * 86400000
    })).resolves.toMatchObject({ committedRecords: 1, expiredRecords: 0 });
    expect(await readCollection(indexedDB, 'tools')).toHaveLength(1);
    await expectAccurateTotals(indexedDB);
  });
});
