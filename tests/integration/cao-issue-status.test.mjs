import assert from 'node:assert/strict';
import { chmod, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import test from 'node:test';

const execFileAsync = promisify(execFile);
const cao = path.resolve('activity/cao.mjs');

async function fixture() {
  const root = await mkdtemp(path.join(os.tmpdir(), 'cao-issue-status-'));
  const shardDirectory = path.join(root, 'shards');
  const databasePath = path.join(root, 'activity.sqlite');
  const bin = path.join(root, 'bin');
  const callsPath = path.join(root, 'graphql-calls.jsonl');
  await mkdir(shardDirectory);
  await mkdir(bin);
  const envelopes = [
    {
      schema_version: 2,
      kind: 'run',
      run: {
        run_id: 303,
        run_attempt: 1,
        organization: 'githubnext',
        repository: 'githubnext/gh-cao',
        workflow_name: 'Dashboard',
        workflow_path: '.github/workflows/dashboard.lock.yml',
        status: 'completed',
        conclusion: 'success',
        created_at: '2026-09-09T03:59:00Z',
        started_at: '2026-09-09T04:00:00Z',
        updated_at: '2026-09-09T04:01:00Z'
      }
    },
    {
      schema_version: 2,
      kind: 'safe_output_item',
      safe_output: {
        run_id: 303,
        timestamp: '2026-09-09T04:00:30Z',
        type: 'create_issue',
        provider: 'github',
        url: 'https://github.com/githubnext/gh-cao/issues/42'
      }
    },
    {
      schema_version: 2,
      kind: 'safe_output_item',
      safe_output: {
        run_id: 303,
        timestamp: '2026-09-09T04:00:31Z',
        type: 'create_issue',
        provider: 'github',
        url: 'https://github.com/githubnext/gh-cao/issues/43'
      }
    }
  ];
  const shardPath = path.join(shardDirectory, 'logs-fixture.jsonl');
  await writeFile(shardPath, `${envelopes.map((record) => JSON.stringify(record)).join('\n')}\n`);
  await execFileAsync(process.execPath, [
    cao,
    'ingest-jsonl',
    '--database',
    databasePath,
    '--input-dir',
    shardDirectory,
    '--retention-days', 'all',
    '--run-retention-days', 'all'
  ]);
  const ghPath = path.join(bin, 'gh');
  await writeFile(ghPath, `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2);
const queryArgument = args.find((argument) => argument.startsWith('query=')) || '';
fs.appendFileSync(process.env.GRAPHQL_CALLS_PATH, JSON.stringify(args) + '\\n');
if (queryArgument.includes('repository(')) {
  const number = queryArgument.includes('issueOrPullRequest(number: 43)') ? 43 : 42;
  process.stdout.write(JSON.stringify({ data: {
    repository: { i0: {
      number,
      state: 'CLOSED',
      stateReason: 'COMPLETED',
      closedAt: '2026-09-20T12:00:00Z',
      url: 'https://github.com/githubnext/gh-cao/issues/' + number
    } },
    rateLimit: { cost: 1, remaining: 998, resetAt: '2026-09-20T13:00:00Z' }
  } }));
} else {
  process.stdout.write(JSON.stringify({ data: {
    rateLimit: { cost: 1, remaining: 999, resetAt: '2026-09-20T13:00:00Z' }
  } }));
}
`);
  await chmod(ghPath, 0o755);
  return { root, shardDirectory, shardPath, databasePath, bin, callsPath };
}

test('issue-status enriches issues through one-item GraphQL batches within a small budget', async () => {
  const item = await fixture();
  try {
    const env = {
      ...process.env,
      PATH: `${item.bin}:${process.env.PATH}`,
      GRAPHQL_CALLS_PATH: item.callsPath
    };
    const { stdout } = await execFileAsync(process.execPath, [
      cao,
      'issue-status',
      '--database',
      item.databasePath,
      '--input-dir',
      item.shardDirectory,
      '--batch-size',
      '1',
      '--graphql-cost-budget',
      '3',
      '--graphql-min-remaining',
      '500'
    ], { env });
    const result = JSON.parse(stdout);
    assert.equal(result.issues, 2);
    assert.equal(result.queried, 2);
    assert.equal(result.statuses, 2);
    assert.equal(result.updatedRecords, 2);
    assert.equal(result.rateLimit.cost, 3);

    const records = (await readFile(item.shardPath, 'utf8')).trim().split('\n').map(JSON.parse);
    for (const record of records.slice(1)) {
      assert.deepEqual(record.safe_output.github_issue_status, {
        state: 'CLOSED',
        closed: true,
        state_reason: 'COMPLETED',
        closed_at: '2026-09-20T12:00:00Z',
        observed_at: record.safe_output.github_issue_status.observed_at
      });
      assert.ok(Number.isFinite(Date.parse(record.safe_output.github_issue_status.observed_at)));
    }

    await execFileAsync(process.execPath, [
      cao,
      'ingest-jsonl',
      '--database',
      item.databasePath,
      '--input-dir',
      item.shardDirectory,
      '--retention-days', 'all',
      '--run-retention-days', 'all'
    ]);
    const query = await execFileAsync(process.execPath, [
      cao,
      'query',
      '--database',
      item.databasePath,
      '--collection',
      'issues'
    ]);
    assert.deepEqual(JSON.parse(query.stdout).map((issue) => ({
      closed: issue.closed,
      state: issue.state,
      stateReason: issue.stateReason,
      closedAt: issue.closedAt
    })), [42, 43].map(() => ({
      closed: true,
      state: 'CLOSED',
      stateReason: 'COMPLETED',
      closedAt: '2026-09-20T12:00:00Z'
    })));

    const calls = (await readFile(item.callsPath, 'utf8')).trim().split('\n').map(JSON.parse);
    assert.equal(calls.length, 3);
    assert.ok(calls.every((arguments_) => arguments_.slice(0, 2).join(' ') === 'api graphql'));
    assert.match(calls[1].find((argument) => argument.startsWith('query=')), /issueOrPullRequest\(number: 42\)/);
    assert.match(calls[2].find((argument) => argument.startsWith('query=')), /issueOrPullRequest\(number: 43\)/);
  } finally {
    await rm(item.root, { recursive: true, force: true });
  }
});

test('issue-status skips missing issues without blocking the rest of a batch', async () => {
  const item = await fixture();
  try {
    const ghPath = path.join(item.bin, 'gh');
    await writeFile(ghPath, `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2);
const query = args.find((argument) => argument.startsWith('query=')) || '';
fs.appendFileSync(process.env.GRAPHQL_CALLS_PATH, JSON.stringify(args) + '\\n');
process.stdout.write(JSON.stringify(query.includes('repository(') ? {
  data: {
    repository: {
      i0: null,
      i1: {
        number: 43,
        state: 'OPEN',
        stateReason: null,
        closedAt: null,
        url: 'https://github.com/githubnext/gh-cao/issues/43'
      }
    },
    rateLimit: { cost: 1, remaining: 998, resetAt: '2026-09-20T13:00:00Z' }
  }
} : {
  data: {
    rateLimit: { cost: 1, remaining: 999, resetAt: '2026-09-20T13:00:00Z' }
  }
}));
`);
    await chmod(ghPath, 0o755);
    const env = {
      ...process.env,
      PATH: `${item.bin}:${process.env.PATH}`,
      GRAPHQL_CALLS_PATH: item.callsPath
    };
    const { stdout } = await execFileAsync(process.execPath, [
      cao,
      'issue-status',
      '--database',
      item.databasePath,
      '--input-dir',
      item.shardDirectory
    ], { env });

    const result = JSON.parse(stdout);
    assert.equal(result.queried, 2);
    assert.equal(result.statuses, 1);
    assert.equal(result.updatedRecords, 1);
    assert.equal(result.stopped, null);
    assert.deepEqual(result.errors, []);

    const records = (await readFile(item.shardPath, 'utf8')).trim().split('\n').map(JSON.parse);
    assert.equal(records[1].safe_output.github_issue_status, undefined);
    assert.deepEqual(records[2].safe_output.github_issue_status, {
      state: 'OPEN',
      closed: false,
      state_reason: null,
      closed_at: null,
      observed_at: records[2].safe_output.github_issue_status.observed_at
    });

    const calls = (await readFile(item.callsPath, 'utf8')).trim().split('\n').map(JSON.parse);
    const issueQuery = calls[1].find((argument) => argument.startsWith('query='));
    assert.match(issueQuery, /issueOrPullRequest\(number: 42\)/);
    assert.match(issueQuery, /issueOrPullRequest\(number: 43\)/);
    assert.match(issueQuery, /\.\.\. on Issue/);
  } finally {
    await rm(item.root, { recursive: true, force: true });
  }
});

test('issue-status does not query issues beyond its GraphQL point budget', async () => {
  const item = await fixture();
  try {
    const env = {
      ...process.env,
      PATH: `${item.bin}:${process.env.PATH}`,
      GRAPHQL_CALLS_PATH: item.callsPath
    };
    const { stdout } = await execFileAsync(process.execPath, [
      cao,
      'issue-status',
      '--database',
      item.databasePath,
      '--input-dir',
      item.shardDirectory,
      '--graphql-cost-budget',
      '1'
    ], { env });
    const result = JSON.parse(stdout);
    assert.equal(result.queried, 0);
    assert.equal(result.updatedRecords, 0);
    assert.equal(result.stopped, 'cost-budget');
    const calls = (await readFile(item.callsPath, 'utf8')).trim().split('\n').map(JSON.parse);
    assert.equal(calls.length, 1);
  } finally {
    await rm(item.root, { recursive: true, force: true });
  }
});
