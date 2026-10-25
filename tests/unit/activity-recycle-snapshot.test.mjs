import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { verifyRecycleSnapshot, resetRecycleProjection } from '../../activity/recycle-snapshot.mjs';
import { recycleActivityCache } from '../../activity/cli/cache.mjs';
import { hashFileContents } from '../../activity/cli/files.mjs';

const root = path.resolve(import.meta.dirname, '../..');
const runCao = (args) => JSON.parse(execFileSync(process.execPath, ['activity/cao.mjs', ...args], {
  cwd: root, encoding: 'utf8', env: { ...process.env, GH_TOKEN: '', GITHUB_TOKEN: '' }
}));

async function fixture(t) {
  const directory = await mkdtemp(path.join(tmpdir(), 'activity-recycle-only-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const shards = path.join(directory, 'gh-aw-logs-shards');
  await mkdir(shards);
  const settings = { allowed_owners: ['octo'], allowed_repositories: ['octo/control'] };
  const current = path.join(directory, 'current-settings.json');
  await writeFile(current, JSON.stringify({ ...settings, policy_resolution: { status: 'available' } }));
  await writeFile(path.join(directory, 'control-settings.json'), JSON.stringify(settings));
  const inventory = Object.fromEntries(['repositories', 'workflows', 'campaigns'].map((name) => [
    name, { source: name, rows: [], metadata: { 'as-of': '2026-01-01T00:00:00Z' } }
  ]));
  await writeFile(path.join(directory, 'inventory-sources.json'), JSON.stringify(inventory));
  await writeFile(path.join(shards, 'empty.jsonl'), '{"schema_version":2,"kind":"workflow_runs","request":{"repository":"octo/control"},"payload":[]}\n');
  return { directory, shards, current };
}

test('recycle-only accepts the same case-insensitive scope without refreshing evidence metadata', async (t) => {
  const { directory, current } = await fixture(t);
  const inventory = await readFile(path.join(directory, 'inventory-sources.json'), 'utf8');
  const settings = JSON.parse(await readFile(current, 'utf8'));
  settings.allowed_owners = ['OCTO', 'octo'];
  settings.allowed_repositories = ['OCTO/CONTROL'];
  settings.activity_public_only = false;
  await writeFile(current, JSON.stringify(settings));
  assert.equal((await verifyRecycleSnapshot(directory, current)).shards, 1);
  await resetRecycleProjection(directory, { repository: 'octo/control' });
  assert.equal(await readFile(path.join(directory, 'inventory-sources.json'), 'utf8'), inventory);
});

test('recycle-only fails closed on missing snapshots, changed scope, or unavailable policy', async (t) => {
  const { directory, current, shards } = await fixture(t);
  const existing = JSON.parse(await readFile(current, 'utf8'));
  for (const settings of [
    { ...existing, allowed_repositories: ['octo/other'] },
    { ...existing, allowed_owners: ['another'] },
    { ...existing, policy_resolution: { status: 'unavailable' } }
  ]) {
    await writeFile(current, JSON.stringify(settings));
    await assert.rejects(verifyRecycleSnapshot(directory, current), /different repository scope|available current control policy/);
  }
  await writeFile(current, JSON.stringify(existing));
  await rm(path.join(shards, 'empty.jsonl'));
  await assert.rejects(verifyRecycleSnapshot(directory, current), /requires restored raw evidence/);
  await rm(path.join(directory, 'inventory-sources.json'));
  await assert.rejects(verifyRecycleSnapshot(directory, current), { code: 'ENOENT' });
});

test('recycle-only rejects changed Activity scope or public-only collection boundaries', async (t) => {
  const { directory, current } = await fixture(t);
  const existing = JSON.parse(await readFile(current, 'utf8'));
  const retained = {
    ...existing,
    activity_repositories: ['octo/control', 'octo/other'],
    activity_public_only: false
  };
  await writeFile(path.join(directory, 'control-settings.json'), JSON.stringify(retained));
  for (const settings of [
    { ...retained, activity_repositories: ['octo/control'] },
    { ...retained, activity_repositories: [] },
    { ...retained, activity_public_only: true },
    existing
  ]) {
    await writeFile(current, JSON.stringify(settings));
    await assert.rejects(verifyRecycleSnapshot(directory, current), /different Activity collection scope/);
  }
  await writeFile(current, JSON.stringify({
    ...retained, activity_repositories: ['OCTO/OTHER', 'OCTO/CONTROL', 'octo/control']
  }));
  assert.equal((await verifyRecycleSnapshot(directory, current)).shards, 1);
});

test('recycle-only rejects malformed Activity scope in either snapshot', async (t) => {
  const { directory, current } = await fixture(t);
  const existing = JSON.parse(await readFile(current, 'utf8'));
  for (const file of [current, path.join(directory, 'control-settings.json')]) {
    for (const activity_repositories of [null, 'octo/control', [1]]) {
      await writeFile(file, JSON.stringify({ ...existing, activity_repositories }));
      await assert.rejects(verifyRecycleSnapshot(directory, current), /valid activity_repositories snapshot/);
    }
    await writeFile(file, JSON.stringify(existing));
  }
});

test('recycle-only distinguishes explicit Activity scope from owner-wide legacy collection', async (t) => {
  const { directory, current } = await fixture(t);
  const existing = JSON.parse(await readFile(current, 'utf8'));
  const legacy = { ...existing, allowed_repositories: [] };
  await writeFile(path.join(directory, 'control-settings.json'), JSON.stringify(legacy));
  await writeFile(current, JSON.stringify({ ...legacy, activity_repositories: [] }));
  await assert.rejects(verifyRecycleSnapshot(directory, current), /different Activity collection scope/);
});

test('recycle-only resets stale projections, preserves repository memory, and rebuilds valid empty phases', async (t) => {
  const { directory, current, shards } = await fixture(t);
  await verifyRecycleSnapshot(directory, current);
  for (const suffix of ['', '-wal', '-shm', '-journal']) {
    await writeFile(path.join(directory, `gh-aw-logs.sqlite${suffix}`), 'stale');
  }
  for (const phase of ['runs', 'records', 'normalized']) {
    await mkdir(path.join(directory, `gh-aw-logs-${phase}`));
    await writeFile(path.join(directory, `gh-aw-logs-${phase}`, 'stale.jsonl'), 'stale');
  }
  await writeFile(path.join(directory, 'payload-hashes.json'), '{"stale":"stale"}');
  await mkdir(path.join(directory, 'memory'));
  const memory = '{"version":1,"generatedAt":"2026-01-01T00:00:00Z","campaigns":[]}';
  await writeFile(path.join(directory, 'memory/manifest.json'), memory);
  await rm(path.join(shards, 'empty.jsonl'));
  await resetRecycleProjection(directory, { repository: 'octo/control' });
  assert.equal(await readFile(path.join(directory, 'memory/manifest.json'), 'utf8'), memory);
  assert.ok(!(await readdir(directory)).includes('gh-aw-logs.sqlite'));
  await rebuild(directory);
});

async function rebuild(directory) {
  const phases = ['--runs-dir', path.join(directory, 'gh-aw-logs-runs'), '--records-dir', path.join(directory, 'gh-aw-logs-records')];
  const hashing = [
    'hash-payloads', '--shard-dir', path.join(directory, 'gh-aw-logs-shards'), ...phases,
    '--inventory', path.join(directory, 'inventory-sources.json'), '--max-cache-bytes', '1048576'
  ];
  runCao(hashing);
  const database = path.join(directory, 'gh-aw-logs.sqlite');
  runCao(['ingest-jsonl', '--database', database, ...phases, '--retention-days', '7', '--run-retention-days', '30']);
  const repair = runCao(['repair-database', '--database', database, '--ttl-days', '7', '--run-ttl-days', '30']);
  assert.equal(repair.command, 'repair-database');
  assert.equal(repair.healthy, true);
  const hashes = runCao([...hashing, '--database', database, '--output', path.join(directory, 'payload-hashes.json')]);
  assert.equal(hashes['gh-aw-logs.sqlite'], await hashFileContents(database));
  for (const [name, hash] of Object.entries(hashes)) assert.equal(await hashFileContents(path.join(directory, name)), hash);
  for (const phase of ['shards', 'runs', 'records']) {
    assert.ok((await readdir(path.join(directory, `gh-aw-logs-${phase}`))).some((name) => name.endsWith('.jsonl')));
  }
}

test('recycle-only removes expired evidence before rebuilding the snapshot without network collection', async (t) => {
  const { directory, shards, current } = await fixture(t);
  await writeFile(path.join(shards, 'expired.jsonl'), JSON.stringify({
    schema_version: 2, kind: 'run', run: { run_id: 123, created_at: '2020-01-01T00:00:00Z', status: 'completed' }
  }) + '\n');
  await verifyRecycleSnapshot(directory, current);
  assert.equal((await recycleActivityCache(shards)).expiredRuns, 1);
  await resetRecycleProjection(directory);
  await rebuild(directory);
  assert.ok(!(await readdir(shards)).includes('expired.jsonl'));
});
