import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, cp, readFile, readdir, rm } from 'node:fs/promises';
import path from 'node:path';
import test from 'node:test';
import { normalizeSnapshotPayloads } from '../../dashboard/report/normalized-payloads.mjs';
import { normalizedPhaseBatch, relationshipSafeEvidenceBatch } from '../../dashboard/report/normalized-phase.mjs';
import { NORMALIZED_COLLECTIONS } from '../../dashboard/report/collections.mjs';

test('normalized phase partition emits every relational experiment collection exactly once', () => {
  const batch = Object.fromEntries(NORMALIZED_COLLECTIONS.map(collection => [collection, [{ id: collection }]]));
  const runs = normalizedPhaseBatch(batch, 'runs');
  const records = normalizedPhaseBatch(batch, 'records');
  for (const collection of NORMALIZED_COLLECTIONS) {
    assert.equal(runs[collection].length + records[collection].length, 1, collection);
  }
  for (const collection of ['experiments', 'experimentAssignments']) assert.equal(runs[collection].length, 1);
  for (const collection of ['graders', 'graderObservations', 'evals', 'evalObservations']) {
    assert.equal(records[collection].length, 1);
  }
});

test('relationship-safe evidence drops observations tied to another workflow', () => {
  const batch = Object.fromEntries(NORMALIZED_COLLECTIONS.map(collection => [collection, []]));
  batch.runs = [{ id: 'run:1', workflowId: 'workflow:campaign' }];
  batch.graders = [
    { id: 'grader:campaign', workflowId: 'workflow:campaign' },
    { id: 'grader:package', workflowId: 'workflow:package' }
  ];
  batch.graderObservations = [
    { id: 'valid', runId: 'run:1', graderId: 'grader:campaign' },
    { id: 'mismatched', runId: 'run:1', graderId: 'grader:package' }
  ];
  assert.deepEqual(relationshipSafeEvidenceBatch(batch).graderObservations, [batch.graderObservations[0]]);
});

test('Activity publishes checksummed normalized JSONL without a database artifact', async t => {
  await mkdir('.test-artifacts', { recursive: true });
  const root = await mkdtemp(path.resolve('.test-artifacts/activity-normalized-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const shardDirectory = path.join(root, 'gh-aw-logs-shards');
  const runsDirectory = path.join(root, 'gh-aw-logs-runs');
  const recordsDirectory = path.join(root, 'gh-aw-logs-records');
  await mkdir(shardDirectory);
  const source = path.join(shardDirectory, 'logs-1000.jsonl');
  await cp('dashboard/site/test/fixtures/gh-aw-logs/cached-v2.jsonl', source);
  const options = { shardDirectory, runsDirectory, recordsDirectory, maxBytes: 2048 };
  const hashes = await normalizeSnapshotPayloads(options);
  assert.ok(Object.keys(hashes).some(name => name.startsWith('gh-aw-logs-runs/')));
  assert.ok(Object.keys(hashes).some(name => name.startsWith('gh-aw-logs-records/')));
  assert.ok(Object.keys(hashes).every(name => name.endsWith('.jsonl')));
  for (const [name, digest] of Object.entries(hashes)) {
    assert.equal(createHash('sha256').update(await readFile(path.join(root, name))).digest('hex'), digest);
  }
  const phaseHashes = entries => Object.fromEntries(Object.entries(entries)
    .filter(([name]) => !name.startsWith('gh-aw-logs-shards/')));
  await cp(source, path.join(shardDirectory, 'logs-2000.jsonl'));
  assert.deepEqual(phaseHashes(await normalizeSnapshotPayloads(options)), phaseHashes(hashes));
  assert.deepEqual((await readdir(root)).sort(), ['gh-aw-logs-records', 'gh-aw-logs-runs', 'gh-aw-logs-shards']);
});
