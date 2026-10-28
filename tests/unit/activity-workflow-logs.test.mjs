import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { chmod, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';
import test from 'node:test';
import { collectWorkflowLogs, workflowLogTargets } from '../../activity/collect-workflow-logs.mjs';

const execute = promisify(execFile);

test('workflow collection uses sorted repository-owned compiled paths across every discovery page', () => {
  const pages = [
    { workflows: [{ path: '.github/workflows/worker.lock.yml' }, { path: '.github/workflows/ci.yml' }] },
    { workflows: [{ path: '.github/workflows/experiment.lock.yml' }, { path: '.github/workflows/worker.lock.yml' }] }
  ];
  assert.deepEqual(workflowLogTargets(pages, 'octo/api'), [
    'octo/api/.github/workflows/experiment.lock.yml',
    'octo/api/.github/workflows/worker.lock.yml'
  ]);
  for (const value of [{}, [null], [{ workflows: [{}] }], [{ workflows: [{ path: '../other.lock.yml' }] }]]) {
    assert.throws(() => workflowLogTargets(value, 'octo/api'), TypeError);
  }
});

async function fixture(t, { response = '[{"workflows":[{"path":".github/workflows/remote-experiment.lock.yml"}]}]',
  discoveryStatus = 0, collectionStatus = 0, collectionDelay = 0, stderr = '',
  metadata = { full_name: 'octo/api', private: false, visibility: 'public' } } = {}) {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'cao-workflow-logs-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const binary = path.join(directory, 'gh');
  const calls = path.join(directory, 'calls.jsonl');
  await writeFile(binary, `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2);
fs.appendFileSync(${JSON.stringify(calls)}, JSON.stringify(args) + '\\n');
if (args[0] === 'api') {
  if (args[1] === 'repos/octo/api') {
    console.log(${JSON.stringify(JSON.stringify(metadata))});
    process.exit(0);
  }
  process.stdout.write(${JSON.stringify(response)});
  process.stderr.write(${JSON.stringify(stderr)});
  process.exit(${discoveryStatus});
}
setTimeout(() => process.exit(${collectionStatus}), ${collectionDelay});
`);
  await chmod(binary, 0o700);
  return {
    args: [path.resolve('activity/collect-workflow-logs.mjs'), binary,
      '--repo', 'octo/api', '--count', '10', '--timeout', '2',
      '--max-github-api-rate-limit', '-2000', '--artifacts', 'usage,experiment'],
    calls: async () => (await readFile(calls, 'utf8')).trim().split('\n').map(JSON.parse)
  };
}

test('collection passes remote-only experiment workflows without changing collection flags or repository scope', async (t) => {
  const f = await fixture(t);
  await execute(process.execPath, f.args);
  const calls = await f.calls();
  assert.deepEqual(calls, [
    ['api', 'repos/octo/api/actions/workflows?per_page=100', '--paginate', '--slurp'],
    ['aw', 'logs', ...f.args.slice(2), 'octo/api/.github/workflows/remote-experiment.lock.yml']
  ]);
});

test('an empty remote workflow catalog never falls back to checkout-local workflows', async (t) => {
  const f = await fixture(t, { response: '[{"workflows":[]}]' });
  const result = await execute(process.execPath, f.args);
  assert.match(result.stdout, /No agentic workflows found for octo\/api/);
  assert.equal((await f.calls()).length, 1);
});

test('public-only collection rechecks visibility and marks inaccessible discovery or artifacts as incomplete', async (t) => {
  for (const options of [{}, { discoveryStatus: 1 }, { collectionStatus: 1 }]) {
    const f = await fixture(t, options);
    const [, binary, ...args] = f.args;
    assert.equal(await collectWorkflowLogs(binary, args, { publicOnly: true }),
      options.discoveryStatus || options.collectionStatus ? 76 : 0);
    assert.deepEqual((await f.calls())[0], ['api', 'repos/octo/api']);
  }
  for (const metadata of [
    { full_name: 'octo/api', private: true, visibility: 'private' },
    { full_name: 'octo/api', private: false, visibility: 'internal' },
    { full_name: 'octo/api' },
    { full_name: 'octo/moved', private: false, visibility: 'public' },
  ]) {
    const f = await fixture(t, { metadata });
    const [, binary, ...args] = f.args;
    await assert.rejects(collectWorkflowLogs(binary, args, { publicOnly: true }), /verified public visibility/);
    assert.equal((await f.calls()).length, 1);
  }
});

test('discovery fails closed without exposing upstream errors or invoking local workflow discovery', async (t) => {
  for (const options of [
    { discoveryStatus: 1, stderr: 'PRIVATE_UPSTREAM_ERROR' },
    { response: 'PRIVATE_INVALID_JSON' },
    { response: '{}' }
  ]) {
    const f = await fixture(t, options);
    await assert.rejects(execute(process.execPath, f.args), (error) => {
      assert.equal(error.code, 1);
      assert.match(error.stderr, /Remote workflow discovery/);
      assert.doesNotMatch(error.stderr, /PRIVATE_/);
      return true;
    });
    assert.equal((await f.calls()).length, 1);
  }
});

test('public-only log collection permits a private control repository but not private remote targets', async (t) => {
  const f = await fixture(t, {
    metadata: { full_name: 'octo/api', private: true, visibility: 'private' },
  });
  await execute(process.execPath, f.args, {
    env: { ...process.env, REPORT_PUBLIC_ONLY: 'true', GITHUB_REPOSITORY: 'octo/api' },
  });
  assert.equal((await f.calls()).length, 3);
  const [, binary, ...args] = f.args;
  await assert.rejects(collectWorkflowLogs(binary, args, {
    publicOnly: true, controlRepository: 'octo/control',
  }), /verified public visibility/);
  const renamed = await fixture(t, {
    metadata: { full_name: 'octo/moved', private: true, visibility: 'private' },
  });
  await assert.rejects(collectWorkflowLogs(renamed.args[1], renamed.args.slice(2), {
    publicOnly: true, controlRepository: 'octo/api',
  }), /verified control repository visibility and unchanged identity/);
});

test('collection preserves quota-exhausted statuses from discovery and gh-aw', async (t) => {
  for (const options of [
    { discoveryStatus: 1, stderr: 'HTTP 403: API rate limit exceeded for user ID PRIVATE_ID' },
    { collectionStatus: 75 }
  ]) {
    const f = await fixture(t, options);
    await assert.rejects(execute(process.execPath, f.args), (error) => {
      assert.equal(error.code, 75);
      assert.doesNotMatch(error.stderr, /PRIVATE_/);
      return true;
    });
  }
});

test('collection allows gh-aw to audit and flush evidence after its download timeout', async (t) => {
  const timeout = AbortSignal.timeout.bind(AbortSignal);
  const deadlines = [];
  t.mock.method(AbortSignal, 'timeout', (milliseconds) => {
    deadlines.push(milliseconds);
    return timeout(milliseconds / 100);
  });
  const f = await fixture(t, { collectionDelay: 2_500 });
  const [, binary, ...args] = f.args;
  args[args.indexOf('--timeout') + 1] = '1';

  assert.equal(await collectWorkflowLogs(binary, args), 0);
  assert.deepEqual(deadlines, [360_000]);
  assert.equal((await f.calls()).length, 2);
});

test('collection still fails closed when gh-aw exceeds the bounded cleanup window', async (t) => {
  const timeout = AbortSignal.timeout.bind(AbortSignal);
  t.mock.method(AbortSignal, 'timeout', (milliseconds) => timeout(milliseconds / 100));
  const f = await fixture(t, { collectionDelay: 10_000 });
  const [, binary, ...args] = f.args;
  args[args.indexOf('--timeout') + 1] = '1';

  await assert.rejects(collectWorkflowLogs(binary, args), /Workflow log collection exceeded its timeout/);
});
