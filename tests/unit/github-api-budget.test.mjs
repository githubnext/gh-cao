import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  GithubApiBudgetError, consumeGithubApiBudget, prepareGithubApiBudget,
} from '../../activity/github-api-budget.mjs';
import { githubReader } from '../../.github/skills/add-operational-value/scripts/github-evidence.mjs';

function fixture(t) {
  const directory = mkdtempSync(path.join(tmpdir(), 'cao-api-budget-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return path.join(directory, 'budget.json');
}

const rateLimit = remaining => JSON.stringify({ resources: { core: {
  limit: 5000, remaining, used: 5000 - remaining, reset: 1790000000,
} } });

test('reviewed API budget admits full capacity and persists consumption without credentials', t => {
  const filename = fixture(t);
  const state = prepareGithubApiBudget({
    filename, requested: 3, reserve: 1000, token: 'secret-token', credentialId: 'github',
    now: () => new Date('2026-10-27T00:00:00Z'),
    execute: (_command, _args, options) => {
      assert.equal(options.env.GH_TOKEN, 'secret-token');
      return { status: 0, stdout: rateLimit(4000), stderr: '' };
    },
  });
  assert.equal(state.granted, 3);
  assert.doesNotMatch(readFileSync(filename, 'utf8'), /secret-token/);
  assert.equal(consumeGithubApiBudget(filename).consumed, 1);
  assert.equal(consumeGithubApiBudget(filename, 2).consumed, 3);
  assert.throws(() => consumeGithubApiBudget(filename), error =>
    error instanceof GithubApiBudgetError && error.reasonCode === 'api-budget');
  assert.equal(JSON.parse(readFileSync(filename, 'utf8')).consumed, 3);
});

test('insufficient shared capacity fails before creating a grant', t => {
  const filename = fixture(t);
  assert.throws(() => prepareGithubApiBudget({
    filename, requested: 700, reserve: 2000,
    execute: () => ({ status: 0, stdout: rateLimit(2699), stderr: '' }),
  }), error => error instanceof GithubApiBudgetError && error.reasonCode === 'api-budget');
  assert.throws(() => readFileSync(filename, 'utf8'), /ENOENT/);
});

test('GitHub evidence cache consumes only uncached remote requests', t => {
  const filename = fixture(t);
  prepareGithubApiBudget({
    filename, requested: 2, reserve: 1000,
    execute: () => ({ status: 0, stdout: rateLimit(4000), stderr: '' }),
  });
  let calls = 0;
  const reader = githubReader(path.dirname(filename), (_command, args) => {
    calls++;
    assert.deepEqual(args, ['api', 'repos/org/repo']);
    return '{"id":1}';
  }, { consume: () => consumeGithubApiBudget(filename), maxRequests: 2 });
  assert.deepEqual(reader('repos/org/repo'), { id: 1 });
  assert.deepEqual(reader('repos/org/repo'), { id: 1 });
  assert.equal(calls, 1);
  assert.equal(JSON.parse(readFileSync(filename, 'utf8')).consumed, 1);
});
