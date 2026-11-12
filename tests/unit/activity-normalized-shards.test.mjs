import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtemp, readFile, rm, stat } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  DEFAULT_NORMALIZED_JSONL_SHARD_BYTES,
  writeNormalizedShardBucket
} from '../../dashboard/report/normalized-shards.mjs';

async function directory(t) {
  const root = await mkdtemp(path.join(os.tmpdir(), 'activity-normalized-shards-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  return root;
}

function recordLine(id, summary = '') {
  return `${JSON.stringify({ kind: 'record', collection: 'audits', record: { id, summary } })}\n`;
}

async function readShards(root, names) {
  return Promise.all(names.map(async (name) => {
    const content = await readFile(path.join(root, name), 'utf8');
    const [header, ...records] = content.trimEnd().split('\n');
    const metadata = JSON.parse(header);
    assert.equal(metadata.records, records.length);
    assert.equal(metadata.sourceRecords, records.length);
    const digest = createHash('sha256').update(content).digest('hex');
    assert.ok(name.endsWith(`-${digest.slice(0, 16)}.jsonl`));
    return { content, records, metadata };
  }));
}

test('normalized shards default to at most 1 MiB including metadata without dropping records', async (t) => {
  const root = await directory(t);
  const lines = Array.from({ length: 300 }, (_, index) => recordLine(String(index), 'x'.repeat(8192)));
  const names = await writeNormalizedShardBucket('records', '2026-10-02', lines, root);

  assert.equal(DEFAULT_NORMALIZED_JSONL_SHARD_BYTES, 1024 * 1024);
  assert.equal(names.length, 3);
  const shards = await readShards(root, names);
  for (const shard of shards) {
    assert.ok(Buffer.byteLength(shard.content) <= DEFAULT_NORMALIZED_JSONL_SHARD_BYTES);
  }
  assert.deepEqual(shards.flatMap((shard) => shard.records), lines.map((line) => line.trimEnd()));
});

test('normalized shard bounds include UTF-8 bytes and metadata record-count growth', async (t) => {
  const root = await directory(t);
  const lines = Array.from({ length: 10 }, () => recordLine('same', '\u00e9'.repeat(20)));
  const [singleName] = await writeNormalizedShardBucket('records', '2026-10-02', [lines[0]], root);
  const single = (await readShards(root, [singleName]))[0];
  const headerBytes = Buffer.byteLength(single.content) - Buffer.byteLength(lines[0]);
  const maxBytes = headerBytes + lines.reduce((total, line) => total + Buffer.byteLength(line), 0);
  const names = await writeNormalizedShardBucket('records', '2026-10-02', lines, root, maxBytes);

  assert.equal(names.length, 2);
  const shards = await readShards(root, names);
  assert.deepEqual(shards.map((shard) => shard.metadata.records), [9, 1]);
  for (const shard of shards) assert.ok(Buffer.byteLength(shard.content) <= maxBytes);
});

test('a record that exactly fits the byte budget stays in one shard', async (t) => {
  const root = await directory(t);
  const lines = [recordLine('one'), recordLine('two')];
  const [name] = await writeNormalizedShardBucket('records', '2026-10-02', lines, root);
  const maxBytes = (await stat(path.join(root, name))).size;

  assert.deepEqual(
    await writeNormalizedShardBucket('records', '2026-10-02', lines, root, maxBytes),
    [name]
  );
});

test('oversized canonical records are isolated intact rather than split or truncated', async (t) => {
  const root = await directory(t);
  const lines = [recordLine('before'), recordLine('large', 'x'.repeat(4096)), recordLine('after')];
  const names = await writeNormalizedShardBucket('records', '2026-10-02', lines, root, 1024);
  const shards = await readShards(root, names);

  assert.deepEqual(shards.map((shard) => shard.metadata.records), [1, 1, 1]);
  assert.deepEqual(shards.flatMap((shard) => shard.records), lines.map((line) => line.trimEnd()));
  assert.ok(Buffer.byteLength(shards[0].content) <= 1024);
  assert.ok(Buffer.byteLength(shards[1].content) > 1024);
  assert.ok(Buffer.byteLength(shards[2].content) <= 1024);
});

test('empty phases retain one header-only shard and unchanged shards are not rewritten', async (t) => {
  const root = await directory(t);
  const names = await writeNormalizedShardBucket('runs', '0000-00-00', [], root);
  const shards = await readShards(root, names);
  assert.equal(names.length, 1);
  assert.equal(shards[0].metadata.phase, 'runs');
  assert.equal(shards[0].metadata.records, 0);
  const before = await stat(path.join(root, names[0]));

  assert.deepEqual(await writeNormalizedShardBucket('runs', '0000-00-00', [], root), names);
  const after = await stat(path.join(root, names[0]));
  assert.equal(after.mtimeMs, before.mtimeMs);
  assert.equal(after.ino, before.ino);
});

test('normalized shard limits reject invalid values and budgets smaller than the header', async (t) => {
  const root = await directory(t);
  for (const maxBytes of [0, -1, 0.5, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1, 1]) {
    await assert.rejects(
      writeNormalizedShardBucket('runs', '0000-00-00', [], root, maxBytes),
      /byte limit must fit its metadata header/
    );
  }
});
