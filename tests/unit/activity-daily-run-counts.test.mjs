import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { DatabaseSync } from 'node:sqlite';
import test from 'node:test';
import { resetRecycleProjection } from '../../activity/recycle-snapshot.mjs';
import { CANONICAL_SCHEMA_VERSION } from '../../dashboard/site/src/data/model/schema.js';
import { DAILY_RUN_COUNT_STORES, dailyRunCountCutoff } from '../../dashboard/site/src/data/model/daily-run-counts.js';
import { DATABASE_NAME, DATABASE_VERSION } from '../../dashboard/site/src/data/storage/indexeddb.js';
import { doctorSqliteDatabase } from '../../dashboard/site/src/data/storage/sqlite-doctor.js';
import { hashFileContents } from '../../activity/cli/files.mjs';

const root = path.resolve(import.meta.dirname, '../..');
const day = 86_400_000;
const runCao = (args) => JSON.parse(execFileSync(process.execPath, [
  path.join(root, 'activity/cao.mjs'), ...args
], { encoding: 'utf8', timeout: 30_000 }));

async function fixture(t) {
  const directory = await mkdtemp(path.join(tmpdir(), 'activity-daily-counts-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const shards = path.join(directory, 'gh-aw-logs-shards');
  await mkdir(shards);
  return {
    directory, shards, database: path.join(directory, 'gh-aw-logs.sqlite'),
    phases: ['--runs-dir', path.join(directory, 'gh-aw-logs-runs'),
      '--records-dir', path.join(directory, 'gh-aw-logs-records')]
  };
}

function runEnvelope(id, overrides = {}) {
  const timestamp = new Date(Date.now() - 3 * day).toISOString();
  return {
    schema_version: 2, kind: 'run', run: {
      run_id: id, organization: 'octo', repository: 'octo/demo', workflow_name: 'Worker',
      workflow_path: '.github/workflows/worker.lock.yml',
      created_at: timestamp, started_at: timestamp, updated_at: timestamp,
      status: 'completed', conclusion: 'success', ...overrides
    }
  };
}

function records(database, store) {
  const connection = new DatabaseSync(database, { readOnly: true });
  try {
    return connection.prepare(`
      SELECT record_key, value FROM __idb_records
      WHERE database_name = ? AND store_name = ? ORDER BY record_key
    `).all(DATABASE_NAME, store);
  } finally {
    connection.close();
  }
}

const values = (database, store) => records(database, store).map((row) => JSON.parse(row.value));
const protectedRecords = (database) => Object.fromEntries(
  DAILY_RUN_COUNT_STORES.filter((store) => store !== 'dailyRunCountState')
    .map((store) => [store, records(database, store)]));

function restoreV44(database) {
  const connection = new DatabaseSync(database);
  try {
    for (const store of DAILY_RUN_COUNT_STORES) {
      for (const table of ['__idb_records', '__idb_indexes']) {
        connection.prepare(`DELETE FROM ${table} WHERE database_name = ? AND store_name = ?`)
          .run(DATABASE_NAME, store);
      }
      connection.prepare('DELETE FROM __idb_stores WHERE database_name = ? AND name = ?').run(DATABASE_NAME, store);
    }
    connection.prepare('UPDATE __idb_databases SET version = 44 WHERE name = ?').run(DATABASE_NAME);
  } finally {
    connection.close();
  }
}

async function publish(fixture) {
  const { database, shards, phases } = fixture;
  const hashes = runCao(['hash-payloads', '--database', database, '--shard-dir', shards, ...phases]);
  assert.equal(hashes['gh-aw-logs.sqlite'], await hashFileContents(database));
  return hashes;
}

test('Activity phased CLI ingestion, repair, replay and publication preserve daily counts after detail expiry', async (t) => {
  const fixture_ = await fixture(t);
  const { database, shards, phases } = fixture_;
  const raw = path.join(shards, 'runs.jsonl');
  await writeFile(raw, [runEnvelope(1), runEnvelope(2, { conclusion: 'failure' })]
    .map(JSON.stringify).join('\n') + '\n');
  runCao(['hash-payloads', '--shard-dir', shards, ...phases]);
  const ingested = runCao(['ingest-jsonl', '--database', database, ...phases,
    '--retention-days', '7', '--run-retention-days', '30']);
  assert.equal(ingested.counts.runs, 2);
  const [count] = values(database, 'dailyRunCounts');
  assert.equal(count.total, 2);
  assert.equal(count.successful, 1);
  assert.equal(count.failed, 1);
  assert.equal(values(database, 'runCountContributions').length, 2);
  const protectedBefore = protectedRecords(database);
  const [stateBefore] = values(database, 'dailyRunCountState');
  const repair = runCao(['repair-database', '--database', database,
    '--ttl-days', '1', '--run-ttl-days', '1']);
  assert.equal(repair.healthy, true);
  assert.equal(repair.after.counts.runs, 0);
  assert.deepEqual(protectedRecords(database), protectedBefore);
  const [stateAfter] = values(database, 'dailyRunCountState');
  assert.ok(stateAfter.retentionWatermark >= stateBefore.retentionWatermark);
  assert.deepEqual({ ...stateAfter, retentionWatermark: 0 }, { ...stateBefore, retentionWatermark: 0 });
  await publish(fixture_);

  const replay = runCao(['ingest-jsonl', '--database', database, ...phases,
    '--retention-days', '7', '--run-retention-days', '30']);
  assert.equal(replay.result.updated, false);
  assert.deepEqual(values(database, 'dailyRunCounts'), [count]);

  // A changed source forces normalization/ingestion rather than just testing
  // the unchanged-shard receipt shortcut.
  await writeFile(raw, [runEnvelope(1), runEnvelope(2, { conclusion: 'failure' }),
    { schema_version: 2, kind: 'workflow_runs', request: { repository: 'octo/demo' }, payload: [] }]
    .map(JSON.stringify).join('\n') + '\n');
  runCao(['ingest-jsonl', '--database', database, '--input', raw,
    '--retention-days', '7', '--run-retention-days', '30']);
  assert.deepEqual(values(database, 'dailyRunCounts'), [count]);
  await publish(fixture_);
});

test('Activity SQLite v44 upgrade backfills multiple cursor pages without rewriting retained detail', async (t) => {
  const { database, shards } = await fixture(t);
  const input = path.join(shards, 'backfill.jsonl');
  await writeFile(input, Array.from({ length: 251 }, (_, index) => JSON.stringify(runEnvelope(index + 1))).join('\n') + '\n');
  runCao(['ingest-jsonl', '--database', database, '--input', input]);
  const before = records(database, 'runs');
  assert.equal(before.length, 251);
  restoreV44(database);
  assert.equal(runCao(['query', '--database', database, '--collection', 'runs']).length, 251);
  assert.deepEqual(records(database, 'runs'), before);
  const [state] = values(database, 'dailyRunCountState');
  assert.equal(state.status, 'ready');
  assert.equal(state.coverage, 'retained-v44');
  assert.equal(values(database, 'dailyRunCounts')[0].total, 251);
  assert.equal(values(database, 'runCountContributions').length, 251);
  runCao(['query', '--database', database, '--collection', 'runs']);
  assert.equal(values(database, 'dailyRunCounts')[0].total, 251);
  assert.equal(DATABASE_VERSION, 47);
  assert.equal(CANONICAL_SCHEMA_VERSION, 36);
});

test('Activity doctor publishes UTC rollover eviction independently of detail TTL and fences changed-shard replay', async (t) => {
  const fixture_ = await fixture(t);
  const { database, shards, phases } = fixture_;
  const cutoff = Date.parse(`${dailyRunCountCutoff(Date.now())}T00:00:00Z`);
  const monday = cutoff + 21 * day;
  const timestamp = new Date(cutoff).toISOString();
  const boundary = new Date(cutoff + 7 * day).toISOString();
  const input = path.join(shards, 'rollover.jsonl');
  const expired = Array.from({ length: 251 }, (_, index) => runEnvelope(index + 1, {
    created_at: timestamp, started_at: timestamp, updated_at: timestamp
  }));
  const retained = runEnvelope(252, {
    created_at: boundary, started_at: boundary, updated_at: boundary, conclusion: 'failure'
  });
  await writeFile(input, [...expired, retained].map(JSON.stringify).join('\n') + '\n');
  runCao(['hash-payloads', '--shard-dir', shards, ...phases]);
  runCao(['ingest-jsonl', '--database', database, ...phases, '--run-retention-days', '30']);
  assert.equal(values(database, 'runs').length, 252);
  assert.equal(values(database, 'runCountContributions').length, 252);
  const detail = records(database, 'runs');

  const rollover = await doctorSqliteDatabase(database, {
    now: monday, ttlDays: 'all', runTtlDays: 'all'
  });
  assert.equal(rollover.healthy, true);
  assert.deepEqual(rollover.repairs.actions, ['maintain-daily-count-retention']);
  assert.deepEqual(records(database, 'runs'), detail);
  assert.equal(values(rollover.repairs.backup, 'runCountContributions').length, 252);
  assert.equal(values(database, 'runCountContributions').length, 1);
  const [count] = values(database, 'dailyRunCounts');
  assert.equal(count.day, boundary.slice(0, 10));
  assert.equal(count.total, 1);
  assert.equal(count.failed, 1);
  assert.equal(values(database, 'dailyRunCountState')[0].retentionCutoff, boundary.slice(0, 10));
  const published = await publish(fixture_);

  const detailExpiry = await doctorSqliteDatabase(database, {
    now: monday, ttlDays: 1, runTtlDays: 1
  });
  assert.equal(detailExpiry.healthy, true);
  assert.equal(detailExpiry.after.counts.runs, 0);
  assert.deepEqual(values(database, 'dailyRunCounts'), [count]);
  const protectedBefore = protectedRecords(database);
  // Force a changed payload through normalization, not the unchanged shortcut.
  await writeFile(input, [...expired, retained,
    { schema_version: 2, kind: 'workflow_runs', request: { repository: 'octo/demo' }, payload: [] }]
    .map(JSON.stringify).join('\n') + '\n');
  runCao(['ingest-jsonl', '--database', database, '--input', input,
    '--retention-days', '1', '--run-retention-days', '1']);
  assert.deepEqual(protectedRecords(database), protectedBefore);
  const [state] = values(database, 'dailyRunCountState');
  assert.equal(state.retentionWatermark, monday);
  assert.equal(state.retentionCutoff, boundary.slice(0, 10));
  assert.equal(state.receiptCount, 1);
  assert.equal(state.dailyBucketCount, 1);
  const republished = await publish(fixture_);
  assert.notEqual(republished['gh-aw-logs.sqlite'], published['gh-aw-logs.sqlite']);
});

for (const { age, ttl, retained } of [
  { age: 3, ttl: 'all', retained: 1 },
  { age: 12, ttl: '7', retained: 0 }
]) {
  test(`repair-database backfills v44 before applying ${ttl}-day run retention to a ${age}-day-old run`, async (t) => {
    const { database, shards } = await fixture(t);
    const input = path.join(shards, 'legacy.jsonl');
    const timestamp = new Date(Date.now() - age * day).toISOString();
    await writeFile(input, `${JSON.stringify(runEnvelope(1, {
      created_at: timestamp, started_at: timestamp, updated_at: timestamp
    }))}\n`);
    runCao(['ingest-jsonl', '--database', database, '--input', input]);
    restoreV44(database);
    const repair = runCao(['repair-database', '--database', database,
      '--ttl-days', ttl, '--run-ttl-days', ttl]);
    assert.equal(repair.healthy, true);
    assert.equal(repair.after.counts.runs, retained);
    assert.equal(values(database, 'dailyRunCountState')[0]?.status, 'ready');
    assert.equal(values(database, 'dailyRunCountState')[0]?.coverage, 'retained-v44');
    assert.equal(values(database, 'dailyRunCounts')[0]?.total, 1);
    assert.equal(values(repair.repairs.backup, 'runs').length, 1);
    assert.deepEqual(values(repair.repairs.backup, 'dailyRunCountState'), []);
  });
}

test('recycle-only reconstruction regenerates daily counts from retained raw evidence and publishes them', async (t) => {
  const fixture_ = await fixture(t);
  const { directory, database, shards, phases } = fixture_;
  await writeFile(path.join(shards, 'run.jsonl'), `${JSON.stringify(runEnvelope(7))}\n`);
  runCao(['ingest-jsonl', '--database', database, '--input-dir', shards]);
  const before = values(database, 'dailyRunCounts');
  await resetRecycleProjection(directory, { repository: 'octo/demo' });
  runCao(['hash-payloads', '--shard-dir', shards, ...phases]);
  runCao(['ingest-jsonl', '--database', database, ...phases,
    '--retention-days', '7', '--run-retention-days', '30']);
  const repair = runCao(['repair-database', '--database', database,
    '--ttl-days', '7', '--run-ttl-days', '30']);
  assert.equal(repair.healthy, true);
  assert.deepEqual(values(database, 'dailyRunCounts'), before);
  assert.equal(values(database, 'dailyRunCountState')[0].status, 'ready');
  await publish(fixture_);
  assert.equal(JSON.parse(await readFile(path.join(directory, 'memory/manifest.json'), 'utf8')).version, 1);
});
