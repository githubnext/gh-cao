import 'fake-indexeddb/auto';
import { rmSync } from 'node:fs';
import { afterEach, expect, it, vi } from 'vitest';
import {
  deleteCanonicalDatabase, readCollections
} from '../../src/data/storage/indexeddb.js';
import {
  finalizeNormalizedJsonlIngestion, ingestNormalizedJsonl, NORMALIZED_JSONL_INGESTION_VERSION
} from '../../src/data/ingest/coordinator.js';
import { CANONICAL_SCHEMA_VERSION } from '../../src/data/model/schema.js';
import { queryDatabaseSources } from '../../src/data/queries/database.js';

const originalIndexedDB = globalThis.indexedDB;
const originalIDBKeyRange = globalThis.IDBKeyRange;
const directories = /** @type {string[]} */ ([]);
const day = 86_400_000;
const sunday = Date.parse('2026-10-25T23:59:59.999Z');
const monday = sunday + 1;
const stores = /** @type {const} */ ([
  'runs', 'tools', 'dailyRunCounts', 'runCountContributions', 'dailyRunCountState'
]);

afterEach(async () => {
  vi.restoreAllMocks();
  await deleteCanonicalDatabase(globalThis.indexedDB);
  globalThis.indexedDB = originalIndexedDB;
  globalThis.IDBKeyRange = originalIDBKeyRange;
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true });
});

/** @param {string} id @param {string | null} startedAt */
function run(id, startedAt) {
  return {
    id, repositoryId: 'repository:1', workflowId: 'workflow:1',
    owner: 'octo', repository: 'demo', workflowPath: '.github/workflows/worker.md',
    startedAt, updatedAt: startedAt, status: 'completed', conclusion: 'success', attempt: 2
  };
}

/** @param {Record<string, unknown>[]} runs @param {Record<string, unknown>[]} [tools] */
async function* payload(runs, tools = []) {
  yield `${JSON.stringify({
    kind: 'metadata', schemaVersion: CANONICAL_SCHEMA_VERSION,
    ingestionVersion: NORMALIZED_JSONL_INGESTION_VERSION, phase: 'all',
    records: runs.length + tools.length
  })}\n`;
  for (const [collection, records] of [['runs', runs], ['tools', tools]]) {
    for (const record of records) yield `${JSON.stringify({ kind: 'record', collection, record })}\n`;
  }
}

/** @param {Awaited<ReturnType<typeof readCollections>>} records */
function expectAccounting(records) {
  const bytes = (/** @type {Record<string, unknown>[]} */ rows) => rows.reduce((sum, row) =>
    sum + new TextEncoder().encode(JSON.stringify(row)).byteLength + 512, 0);
  expect(records.dailyRunCountState[0]).toMatchObject({
    receiptCount: records.runCountContributions.length,
    dailyBucketCount: records.dailyRunCounts.length,
    receiptBytes: bytes(records.runCountContributions),
    dailyBytes: bytes(records.dailyRunCounts)
  });
}

it.each(['indexeddb'])(
  'runs independent TTL and weekly eviction through deferred ingestion and unchanged replay in %s',
  async () => {
    await deleteCanonicalDatabase(indexedDB);
    vi.spyOn(Date, 'now').mockReturnValue(sunday);
    const expired = Array.from({ length: 251 }, (_, index) =>
      run(`expired:${index}`, '2026-10-05T00:00:00Z'));
    const boundary = run('boundary', '2026-10-12T00:00:00Z');
    const runs = [...expired, boundary];
    const tools = [{ id: 'tool:old', runId: expired[0].id, name: 'bash', observedAt: expired[0].startedAt }];
    const options = {
      now: sunday, payloadIdentity: 'retention-path', payloadScope: 'retention-path',
      retentionWindowMs: 30 * day, maxDatabaseBytes: Number.MAX_SAFE_INTEGER
    };
    await ingestNormalizedJsonl(indexedDB, payload(runs, tools), { ...options, deferMaintenance: true });
    const ingested = await readCollections(indexedDB, stores);
    expect(ingested.runs).toHaveLength(252);
    expect(ingested.tools).toHaveLength(1);

    const ttl = { ...options, retentionWindowMs: day };
    await finalizeNormalizedJsonlIngestion(indexedDB, ttl);
    const detailedExpired = await readCollections(indexedDB, stores);
    expect(detailedExpired.runs).toEqual([]);
    expect(detailedExpired.tools).toEqual([]);
    expect(detailedExpired.dailyRunCounts).toEqual([
      expect.objectContaining({ day: '2026-10-05', total: 251, successful: 251 }),
      expect.objectContaining({ day: '2026-10-12', total: 1, successful: 1 })
    ]);
    expect(detailedExpired.runCountContributions).toHaveLength(252);
    expect(detailedExpired.dailyRunCountState[0].retentionCutoff).toBe('2026-10-05');
    expectAccounting(detailedExpired);

    // Prime the same short-retention receipt, then roll the UTC week with an
    // unchanged shard: the skip path must still run independent maintenance.
    await ingestNormalizedJsonl(indexedDB, payload(runs, tools), ttl);
    async function* unread() { yield await Promise.reject(new Error('An unchanged payload must not be read')); }
    const skipped = await ingestNormalizedJsonl(indexedDB, unread(), { ...ttl, now: monday });
    expect(skipped).toMatchObject({ skipped: true });
    const rolled = await readCollections(indexedDB, stores);
    expect(rolled.dailyRunCounts).toEqual([expect.objectContaining({ day: '2026-10-12', total: 1 })]);
    expect(rolled.runCountContributions.map(({ id }) => id)).toEqual(['boundary']);
    expect(rolled.dailyRunCountState[0]).toMatchObject({
      retentionCutoff: '2026-10-12', retentionWatermark: monday
    });
    expectAccounting(rolled);

    await ingestNormalizedJsonl(indexedDB, payload(runs), {
      ...ttl, now: sunday, payloadIdentity: 'changed-stale-replay'
    });
    const replayed = await readCollections(indexedDB, stores);
    expect(replayed).toEqual(rolled);
    const source = (await queryDatabaseSources(indexedDB, {}, ['daily-run-counts']))['daily-run-counts'];
    expect(source.rows).toEqual([expect.objectContaining({ day: '2026-10-12', total: 1 })]);
    expect(source.metadata).toMatchObject({
      completeness: 'complete', 'aggregate-retention-start': '2026-10-12',
      'aggregate-retention-watermark': new Date(monday).toISOString()
    });
  }
);

it.each(['indexeddb'])(
  'maintains retired replay guards and incomplete undated evidence through ingestion in %s',
  async () => {
    await deleteCanonicalDatabase(indexedDB);
    vi.spyOn(Date, 'now').mockReturnValue(sunday);
    const original = run('corrected', '2026-10-12T00:00:00Z');
    const undated = { ...run('undated', null), updatedAt: '2026-10-25T12:00:00Z' };
    const future = run('future', '2026-10-26T00:00:00Z');
    const options = {
      now: sunday, payloadScope: 'corrections', retentionWindowMs: day,
      maxDatabaseBytes: Number.MAX_SAFE_INTEGER
    };
    await ingestNormalizedJsonl(indexedDB, payload([original, undated, future]), {
      ...options, payloadIdentity: 'initial'
    });
    const initial = await readCollections(indexedDB, stores);
    expect(initial.dailyRunCounts).toEqual([expect.objectContaining({ total: 1 })]);
    expect(initial.runCountContributions.map(({ id }) => id)).toEqual(['corrected', 'undated']);
    const partial = (await queryDatabaseSources(indexedDB, {}, ['daily-run-counts']))['daily-run-counts'];
    expect(partial.metadata).toMatchObject({ completeness: 'partial', 'excluded-runs': 1 });

    const correction = { ...original, startedAt: '2026-10-04T23:59:59Z', updatedAt: '2026-10-25T13:00:00Z' };
    await ingestNormalizedJsonl(indexedDB, payload([correction]), { ...options, payloadIdentity: 'correction' });
    await ingestNormalizedJsonl(indexedDB, payload([original]), {
      ...options, now: monday, payloadIdentity: 'stale-correction-replay'
    });
    const retired = await readCollections(indexedDB, stores);
    expect(retired.dailyRunCounts).toEqual([]);
    expect(retired.runCountContributions).toContainEqual(expect.objectContaining({
      id: 'corrected', retired: true, retentionDay: '2026-10-12', contribution: null
    }));
    expectAccounting(retired);

    await finalizeNormalizedJsonlIngestion(indexedDB, { ...options, now: Date.parse('2026-11-09T00:00:00Z') });
    await ingestNormalizedJsonl(indexedDB, payload([original, undated]), {
      ...options, payloadIdentity: 'expired-replay'
    });
    const expired = await readCollections(indexedDB, stores);
    expect(expired.dailyRunCounts).toEqual([]);
    expect(expired.runCountContributions).toEqual([]);
    expect(expired.dailyRunCountState[0]).toMatchObject({
      excludedRuns: 0, conflictedRuns: 0, retentionCutoff: '2026-10-26'
    });
    expectAccounting(expired);
  }
);
