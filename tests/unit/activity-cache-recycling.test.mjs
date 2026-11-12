import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdir, mkdtemp, readFile, readdir, rm, stat, symlink, utimes, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';
import test from 'node:test';
import { pruneNormalizationCaches, recycleActivityCache } from '../../activity/cli/cache.mjs';
import { jsonlLines, totalFileBytes } from '../../activity/cli/files.mjs';
import { hashActivityPayloads } from '../../activity/cli/payloads.mjs';

const execute = promisify(execFile);
const now = Date.parse('2026-10-15T00:00:00Z');
const day = 86400000;
const jsonl = (records) => records.map((record) => JSON.stringify(record)).join('\n') + '\n';
const run = (id, timestamp, extra = {}) => ({
  schema_version: 2, kind: 'run', run: {
    run_id: id, repository: 'octo/api', workflow_path: '.github/workflows/agent.lock.yml',
    status: 'completed', created_at: timestamp, ...extra
  }
});
const issue = (id) => ({ schema_version: 2, kind: 'safe_output_item', safe_output: { run_id: id, title: 'Evidence' } });

async function fixture(t) {
  const root = await mkdtemp(path.join(tmpdir(), 'activity-cache-recycling-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const shards = path.join(root, 'gh-aw-logs-shards');
  await mkdir(shards);
  return { root, shards };
}

async function contents(directory) {
  const names = (await readdir(directory)).filter((name) => name.endsWith('.jsonl')).sort();
  const records = [];
  for await (const line of jsonlLines(names.map((name) => path.join(directory, name)))) records.push(JSON.parse(line));
  return records;
}

test('recycling expires runs across mixed shards without orphaning dependent evidence', async (t) => {
  const { shards } = await fixture(t);
  const boundary = new Date(now - 30 * day).toISOString();
  await writeFile(path.join(shards, 'a.jsonl'), jsonl([
    run(1, '2026-08-01T00:00:00Z'),
    run(2, '2026-10-14T00:00:00Z'),
    run(3, boundary),
    run(4, undefined),
    run(5, '2026-08-01T00:00:00Z', { status: 'in_progress' }),
    { kind: 'workflow_runs', payload: [
      { databaseId: 1, createdAt: '2026-08-01T00:00:00Z' },
      { databaseId: 2, createdAt: '2026-10-14T00:00:00Z' }
    ] }
  ]));
  await writeFile(path.join(shards, 'b.jsonl'), jsonl([
    issue(1), issue(2), issue(999),
    { kind: 'token_efficiency_run_context', run: run(1, '2026-08-01T00:00:00Z').run },
    { kind: 'token_efficiency_observation', observation: { optimizerRunId: 1 } },
    { kind: 'token_efficiency_lifecycle_observation', observation: { optimizerRunId: 1 } },
    { kind: 'token_efficiency_observation', observation: { optimizerRunId: 2 } },
    { kind: 'operational_value', operational_value: { timestamp: '2026-01-01T00:00:00Z' } },
    { kind: 'operational_value', operational_value: { timestamp: '2025-01-01T00:00:00Z' } },
    { kind: 'future_metadata', note: 'Preserve unrecognized evidence' }
  ]));
  await writeFile(path.join(shards, 'expired.jsonl'), jsonl([issue(1)]));
  const result = await recycleActivityCache(shards, { now });
  assert.equal(result.expiredRuns, 1);
  assert.equal(result.evictedRuns, 0);
  assert.equal(result.removedFiles, 1);
  const records = await contents(shards);
  assert.deepEqual(records.filter((record) => record.kind === 'run').map((record) => record.run.run_id), [2, 3, 4, 5]);
  assert.deepEqual(records.find((record) => record.kind === 'workflow_runs').payload.map((record) => record.databaseId), [2]);
  assert.deepEqual(records.filter((record) => record.kind === 'safe_output_item').map((record) => record.safe_output.run_id), [2, 999]);
  assert.equal(records.filter((record) => record.kind.startsWith('token_efficiency')).length, 1);
  assert.equal(records.filter((record) => record.kind === 'operational_value').length, 1);
  assert.ok(records.some((record) => record.kind === 'future_metadata'));
  const inode = (await stat(path.join(shards, 'a.jsonl'))).ino;
  const repeat = await recycleActivityCache(shards, { now });
  assert.equal(repeat.rewrittenFiles, 0);
  assert.equal(repeat.retainedBytes, result.retainedBytes);
  assert.equal((await stat(path.join(shards, 'a.jsonl'))).ino, inode);
});

test('byte pressure evicts oldest complete run groups across files and repositories', async (t) => {
  const { shards } = await fixture(t);
  const recent = run(2, '2026-10-14T00:00:00Z', { repository: 'octo/another' });
  const retainedContent = jsonl([recent, issue(2)]);
  await writeFile(path.join(shards, 'old.jsonl'), jsonl([run(1, '2026-10-01T00:00:00Z')]));
  await writeFile(path.join(shards, 'new.jsonl'), retainedContent);
  await writeFile(path.join(shards, 'child.jsonl'), jsonl([issue(1)]));
  const maxBytes = Buffer.byteLength(retainedContent);
  const result = await recycleActivityCache(shards, { now, maxBytes });
  assert.equal(result.evictedRuns, 1);
  assert.equal(result.retainedRuns, 1);
  assert.equal(result.retainedBytes, maxBytes);
  assert.deepEqual(await readdir(shards), ['new.jsonl']);
  assert.equal(await readFile(path.join(shards, 'new.jsonl'), 'utf8'), retainedContent);
});

test('recycling preserves an explicit empty collection marker for first deployment', async (t) => {
  const { shards } = await fixture(t);
  const marker = { schema_version: 2, kind: 'workflow_runs', request: { repository: 'octo/api' }, payload: [] };
  await writeFile(path.join(shards, 'bootstrap.jsonl'), jsonl([marker]));
  const result = await recycleActivityCache(shards, { now });
  assert.equal(result.retainedRuns, 0);
  assert.equal(result.removedFiles, 0);
  assert.deepEqual(await contents(shards), [marker]);
});

test('the newest observation determines expiry and missing dates are retained', async (t) => {
  const { shards } = await fixture(t);
  const records = [
    run(1, '2026-08-01T00:00:00Z'),
    run(1, '2026-08-01T00:00:00Z', {
      completed_at: '2026-08-01T01:00:00Z', updated_at: '2026-10-14T00:00:00Z'
    }),
    run(2, 'invalid', { started_at: '2026-10-14T00:00:00Z' }),
    run(3, undefined)
  ];
  await writeFile(path.join(shards, 'observations.jsonl'), jsonl(records));
  const result = await recycleActivityCache(shards, { now });
  assert.equal(result.expiredRuns, 0);
  assert.deepEqual(await contents(shards), JSON.parse(JSON.stringify(records)));
});

test('context records without status cannot erase an active run state during age recycling', async (t) => {
  const { shards } = await fixture(t);
  const active = run(1, '2026-08-01T00:00:00Z', { status: 'in_progress' });
  const context = { kind: 'token_efficiency_run_context', run: { ...active.run, status: undefined } };
  const metadata = { kind: 'workflow_runs', payload: [{ databaseId: 1, createdAt: '2026-08-01T00:00:00Z' }] };
  await writeFile(path.join(shards, 'active.jsonl'), jsonl([active, context, metadata]));
  const result = await recycleActivityCache(shards, { now });
  assert.equal(result.expiredRuns, 0);
  assert.equal((await contents(shards)).length, 3);
});

test('protected history over budget and malformed shards fail before changing source files', async (t) => {
  const { shards } = await fixture(t);
  const file = path.join(shards, 'a.jsonl');
  const original = jsonl([
    run(1, '2026-08-01T00:00:00Z'),
    { kind: 'operational_value', operational_value: { timestamp: '2026-01-01T00:00:00Z' } }
  ]);
  await writeFile(file, original);
  await assert.rejects(recycleActivityCache(shards, { now, maxBytes: 1 }), /operational-value history.*no shards changed/);
  assert.equal(await readFile(file, 'utf8'), original);
  await writeFile(path.join(shards, 'z.jsonl'), '{invalid}\n');
  await assert.rejects(recycleActivityCache(shards, { now }), SyntaxError);
  assert.equal(await readFile(file, 'utf8'), original);
  assert.deepEqual(await readdir(shards), ['a.jsonl', 'z.jsonl']);
});

test('recycling refuses symlink shards and handles blank lines within the real byte budget', async (t) => {
  const { root, shards } = await fixture(t);
  const outside = path.join(root, 'other.jsonl');
  await writeFile(outside, jsonl([run(1, '2026-08-01T00:00:00Z')]));
  await symlink(outside, path.join(shards, 'link.jsonl'));
  await assert.rejects(recycleActivityCache(shards, { now }), /symlink shard/);
  assert.equal((await contents(root)).length, 1);
  await rm(path.join(shards, 'link.jsonl'));
  const content = jsonl([run(2, '2026-10-14T00:00:00Z')]);
  await writeFile(path.join(shards, 'padded.jsonl'), `\n\n${content}\n\n`);
  const result = await recycleActivityCache(shards, { now, maxBytes: Buffer.byteLength(content) });
  assert.equal(result.retainedBytes, Buffer.byteLength(content));
});

test('recycle-cache exposes diagnostics, supports all retention, and rejects invalid limits', async (t) => {
  const { shards } = await fixture(t);
  await writeFile(path.join(shards, 'old.jsonl'), jsonl([run(1, '2020-01-01T00:00:00Z')]));
  const args = [path.resolve('activity/cao.mjs'), 'recycle-cache', '--input-dir', shards];
  const { stdout } = await execute(process.execPath, [...args, '--retention-days', 'all']);
  assert.equal(JSON.parse(stdout).retainedRuns, 1);
  for (const limit of ['0', '-1', 'invalid', '1.5']) {
    await assert.rejects(execute(process.execPath, [...args, '--max-bytes', limit]), (error) => {
      assert.match(error.stderr, /--max-bytes must be a positive integer/);
      assert.doesNotMatch(error.stderr, /\n\s+at /);
      return true;
    });
  }
});

test('disposable normalization caches prune stale payloads and enforce a shared byte limit', async (t) => {
  const { root } = await fixture(t);
  const directories = [path.join(root, 'runs', '.payloads'), path.join(root, 'records', '.payloads')];
  for (const directory of directories) await mkdir(directory, { recursive: true });
  await writeFile(path.join(directories[0], 'obsolete.jsonl'), 'obsolete');
  await writeFile(path.join(directories[0], 'interrupted.tmp'), 'partial');
  await writeFile(path.join(directories[0], 'old.jsonl'), '1234');
  await writeFile(path.join(directories[1], 'new.jsonl'), '567890');
  await utimes(path.join(directories[0], 'old.jsonl'), 1, 1);
  const result = await pruneNormalizationCaches(directories, {
    retainedNames: new Set(['old.jsonl', 'new.jsonl']), maxBytes: 6
  });
  assert.equal(result.retainedBytes, 6);
  assert.deepEqual(await readdir(directories[0]), []);
  assert.deepEqual(await readdir(directories[1]), ['new.jsonl']);
  await pruneNormalizationCaches(directories, { maxBytes: 0 });
  assert.deepEqual(await readdir(directories[1]), []);
});

test('hash-payloads bounds reusable caches without changing published hashes or evidence', async (t) => {
  const { root, shards } = await fixture(t);
  await writeFile(path.join(shards, 'raw.jsonl'),
    await readFile(path.resolve('dashboard/site/test/fixtures/gh-aw-logs/cached-v2.jsonl'), 'utf8') + jsonl([{
      kind: 'safe_output_item', schema_version: 2,
      safe_output: { run_id: 303, type: 'create_issue', url: 'https://github.com/githubnext/gh-cao/issues/7' }
    }]));
  const options = {
    shardDirectory: shards, runsDirectory: path.join(root, 'runs'), recordsDirectory: path.join(root, 'records')
  };
  const first = await hashActivityPayloads(options);
  const published = await contents(options.recordsDirectory);
  assert.ok(published.some((record) => record.collection === 'issues'));
  const cache = path.join(options.recordsDirectory, '.payloads');
  await writeFile(path.join(cache, 'obsolete.jsonl'), '{broken obsolete cache}\n');
  const bounded = await hashActivityPayloads({ ...options, maxCacheBytes: 0 });
  assert.deepEqual(bounded, first);
  assert.deepEqual(await contents(options.recordsDirectory), published);
  for (const directory of [options.runsDirectory, options.recordsDirectory]) {
    assert.deepEqual(await readdir(path.join(directory, '.payloads')), []);
  }
  assert.deepEqual(await hashActivityPayloads({ ...options, maxCacheBytes: 0 }), first);
});

test('obsolete caches are removed before replacement normalization, even when a new source fails', async (t) => {
  const { root, shards } = await fixture(t);
  const recordsDirectory = path.join(root, 'records');
  const cache = path.join(recordsDirectory, '.payloads');
  await mkdir(cache, { recursive: true });
  await writeFile(path.join(cache, 'superseded.jsonl'), 'obsolete evidence\n');
  const published = path.join(recordsDirectory, 'published.jsonl');
  await writeFile(published, 'previous published evidence\n');
  await writeFile(path.join(shards, 'broken.jsonl'), '{invalid}\n');
  await assert.rejects(hashActivityPayloads({ shardDirectory: shards, recordsDirectory }), /JSON|Unexpected/);
  assert.deepEqual(await readdir(cache), []);
  assert.equal(await readFile(published, 'utf8'), 'previous published evidence\n');
});
