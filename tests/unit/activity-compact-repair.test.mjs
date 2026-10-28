import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';
import { test } from 'node:test';
import { compactJsonlShards } from '../../activity/cli/jsonl.mjs';

const execFileAsync = promisify(execFile);
const groups = ['octo-a/b=first-logs-', 'octo/a-b=second-logs-'];
const run = (repository, id, status = 'completed') => ({
  kind: 'run',
  run: { repository, run_id: id, status, workflow_path: '.github/workflows/test.lock.yml' }
});
const jsonl = (records) => `${records.map((record) => JSON.stringify(record)).join('\n')}\n`;

async function fixture(t, records) {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'activity-compact-repair-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const source = path.join(directory, 'legacy-1.jsonl');
  const content = jsonl(records);
  await writeFile(source, content);
  return { directory, source, content };
}

async function groupRecords(group) {
  const contents = await Promise.all(group.outputs.map((filename) => readFile(filename, 'utf8')));
  return contents.flatMap((content) => content.trim().split('\n').map(JSON.parse));
}

test('compact-jsonl automatically repairs mixed shards and preserves per-repository observation order', async (t) => {
  const safeOutput = { kind: 'safe_output_item', safe_output: { run_id: 1, type: 'create_issue' } };
  const observation = { kind: 'token_efficiency_observation', observation: { optimizerRunId: 2 } };
  const repairedSafeOutput = { ...safeOutput, request: { repository: 'octo-a/b' } };
  const repairedObservation = { ...observation, request: { repository: 'octo/a-b' } };
  const first = run('Octo-A/B', 1, 'in_progress');
  const second = run('octo/a-b', 2);
  const updated = run('octo-a/b', 1);
  const { directory, source } = await fixture(t, [safeOutput, first, observation, second, updated, first]);
  const later = run('octo-a/b', 1, 'updated');
  await writeFile(path.join(directory, 'legacy-2.jsonl'), jsonl([later]));
  const { stdout } = await execFileAsync(process.execPath, [
    path.resolve('activity/cao.mjs'), 'compact-jsonl', '--input-dir', directory,
    ...groups.flatMap((group) => ['--group', group]), '--max-bytes', '200'
  ]);
  const result = JSON.parse(stdout);
  assert.equal(result.repairedShards, 1);
  assert.deepEqual(await groupRecords(result.groups[0]), [repairedSafeOutput, first, updated, later]);
  assert.deepEqual(await groupRecords(result.groups[1]), [repairedObservation, second]);
  assert.equal(result.groups[0].deduplicatedRunRecords, 1);
  await assert.rejects(readFile(source), { code: 'ENOENT' });
  const repeated = await compactJsonlShards(directory, groups, 200);
  assert.equal(repeated.repairedShards, 0);
  assert.deepEqual(await groupRecords(repeated.groups[0]), [repairedSafeOutput, first, updated, later]);
  assert.deepEqual(await groupRecords(repeated.groups[1]), [repairedObservation, second]);
});

test('repair refuses ambiguous records before changing any shard', async (t) => {
  for (const extra of [
    { kind: 'unknown', value: 1 },
    { kind: 'safe_output_item', safe_output: { run_id: 99 } },
    { request: { repository: 'octo-a/b' }, run: { repository_full_name: 'octo/a-b' } },
    { run: { repository: '../invalid' } }
  ]) {
    const { directory, source, content } = await fixture(t, [run('octo-a/b', 1), run('octo/a-b', 2), extra]);
    await assert.rejects(compactJsonlShards(directory, groups), /repository identit/);
    assert.equal(await readFile(source, 'utf8'), content);
    assert.deepEqual(await readdir(directory), [path.basename(source)]);
  }
});

test('repair does not infer ownership from run IDs shared by different repositories', async (t) => {
  const { directory, source, content } = await fixture(t, [
    run('octo-a/b', 1), run('octo/a-b', 1),
    { kind: 'safe_output_item', safe_output: { run_id: 1 } }
  ]);
  await assert.rejects(compactJsonlShards(directory, groups), /unattributable records/);
  assert.equal(await readFile(source, 'utf8'), content);
});

test('repair does not move evidence outside the declared repository groups', async (t) => {
  const { directory, source, content } = await fixture(t, [run('octo-a/b', 1), run('other/repo', 2)]);
  await assert.rejects(compactJsonlShards(directory, groups), /outside the declared compaction groups/);
  assert.equal(await readFile(source, 'utf8'), content);
  assert.deepEqual(await readdir(directory), [path.basename(source)]);
});

test('scoped collection excludes foreign records and unscoped audits from mixed downloaded shards', async (t) => {
  const kept = run('octo-a/b', 1);
  const safeOutput = { kind: 'safe_output_item', safe_output: { run_id: 1 } };
  const foreign = run('other/repo', 2);
  const foreignOutput = { kind: 'safe_output_item', safe_output: { run_id: 2 } };
  const rateLimit = { kind: 'github_api_rate_limit', rate_limit: { host: 'github.com' } };
  const { directory, source } = await fixture(t, [kept, safeOutput, foreign, foreignOutput, rateLimit]);
  const foreignOnly = path.join(directory, 'first-logs-foreign.jsonl');
  await writeFile(foreignOnly, jsonl([run('other/repo', 3)]));
  const result = await compactJsonlShards(directory, [groups[0]], 1024, { excludeOutOfScope: true });

  assert.equal(result.repairedShards, 1);
  assert.equal(result.excludedRecords, 4);
  assert.deepEqual(await groupRecords(result.groups[0]), [
    kept, { ...safeOutput, request: { repository: 'octo-a/b' } }
  ]);
  await assert.rejects(readFile(source), { code: 'ENOENT' });
  await assert.rejects(readFile(foreignOnly), { code: 'ENOENT' });
});

test('scoped collection still refuses ambiguous dependent evidence before modifying shards', async (t) => {
  const { directory, source, content } = await fixture(t, [
    run('octo-a/b', 1), run('other/repo', 1),
    { kind: 'safe_output_item', safe_output: { run_id: 1 } }
  ]);
  await assert.rejects(compactJsonlShards(directory, [groups[0]], 1024, {
    excludeOutOfScope: true
  }), /unattributable records/);
  assert.equal(await readFile(source, 'utf8'), content);
});

test('scoped collection refuses foreign shards without the selected download prefix', async (t) => {
  const { directory, source, content } = await fixture(t, [run('octo-a/b', 1)]);
  const unrelated = path.join(directory, 'other-logs-foreign.jsonl');
  await writeFile(unrelated, jsonl([run('other/repo', 2)]));
  await assert.rejects(compactJsonlShards(directory, [groups[0]], 1024, {
    excludeOutOfScope: true
  }), /lacks a declared download prefix/);
  assert.equal(await readFile(source, 'utf8'), content);
  assert.deepEqual((await readdir(directory)).sort(), [path.basename(source), path.basename(unrelated)].sort());
});

test('scoped collection refuses to discard unattributable evidence from a foreign-only shard', async (t) => {
  const { directory, source, content } = await fixture(t, [run('octo-a/b', 1)]);
  const foreignOnly = path.join(directory, 'first-logs-foreign.jsonl');
  await writeFile(foreignOnly, jsonl([
    run('other/repo', 2),
    { kind: 'safe_output_item', safe_output: { run_id: 1 } }
  ]));
  await assert.rejects(compactJsonlShards(directory, [groups[0]], 1024, {
    excludeOutOfScope: true
  }), /Foreign shard contains unattributable records/);
  assert.equal(await readFile(source, 'utf8'), content);
  assert.equal((await readdir(directory)).length, 2);
});

test('failed repair writes retain original shards and roll back completed groups', async (t) => {
  const { directory, source, content } = await fixture(t, [run('octo-a/b', 1), run('octo/a-b', 2)]);
  // A valid prefix whose generated filename exceeds the filesystem component limit.
  const failingPrefix = `${'x'.repeat(255)}-`;
  await assert.rejects(compactJsonlShards(directory, [groups[0], `octo/a-b=${failingPrefix}`]), { code: 'ENAMETOOLONG' });
  assert.equal(await readFile(source, 'utf8'), content);
  assert.deepEqual(await readdir(directory), [path.basename(source)]);
});

test('malformed JSONL prevents repair without replacing originals', async (t) => {
  const { directory, source, content } = await fixture(t, [run('octo-a/b', 1), run('octo/a-b', 2)]);
  await writeFile(source, `${content}{invalid\n`);
  await assert.rejects(compactJsonlShards(directory, groups), SyntaxError);
  assert.equal(await readFile(source, 'utf8'), `${content}{invalid\n`);
  assert.deepEqual(await readdir(directory), [path.basename(source)]);
});
