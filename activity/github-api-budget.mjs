import { spawnSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import {
  closeSync, fsyncSync, lstatSync, mkdirSync, openSync, readFileSync, renameSync, unlinkSync, writeFileSync,
} from 'node:fs';
import path from 'node:path';
import { isGithubRateLimitError, normalizeRateLimit } from './github-telemetry.mjs';

const MAX_REQUESTS = 1000;

export class GithubApiBudgetError extends Error {
  constructor(message, reasonCode = 'api-budget') {
    super(message);
    this.name = 'GithubApiBudgetError';
    this.reasonCode = reasonCode;
  }
}

function validInteger(value, minimum, maximum = Number.MAX_SAFE_INTEGER) {
  return Number.isSafeInteger(value) && value >= minimum && value <= maximum;
}

function validateState(state) {
  if (!state || state.schemaVersion !== 1 || state.status !== 'open'
    || typeof state.preparedAt !== 'string' || !Number.isFinite(Date.parse(state.preparedAt))
    || typeof state.resetAt !== 'string' || !Number.isFinite(Date.parse(state.resetAt))
    || !validInteger(state.limit, 1) || !validInteger(state.remainingAtPrepare, 0, state.limit)
    || !validInteger(state.reserve, 0, state.limit)
    || !validInteger(state.granted, 1, MAX_REQUESTS)
    || !validInteger(state.consumed, 0, state.granted)
    || !(state.credentialId === null || typeof state.credentialId === 'string')) {
    throw new GithubApiBudgetError('GitHub API budget state is invalid', 'budget-unavailable');
  }
  return state;
}

function atomicWrite(filename, state) {
  mkdirSync(path.dirname(filename), { recursive: true });
  const pending = `${filename}.${process.pid}.${randomUUID()}.pending`;
  const descriptor = openSync(pending, 'wx', 0o600);
  try {
    writeFileSync(descriptor, `${JSON.stringify(state, null, 2)}\n`);
    fsyncSync(descriptor);
  } finally {
    closeSync(descriptor);
  }
  try {
    renameSync(pending, filename);
  } catch (error) {
    try { unlinkSync(pending); } catch {}
    throw error;
  }
}

export function prepareGithubApiBudget({
  filename,
  requested,
  reserve,
  token = process.env.GH_TOKEN || process.env.GITHUB_TOKEN || '',
  credentialId = process.env.CAO_GITHUB_CREDENTIAL_ID || null,
  execute = spawnSync,
  now = () => new Date(),
}) {
  if (typeof filename !== 'string' || !filename || !validInteger(requested, 1, MAX_REQUESTS)
    || !validInteger(reserve, 0)) {
    throw new GithubApiBudgetError('GitHub API budget configuration is invalid', 'budget-unavailable');
  }
  const result = execute('gh', ['api', 'rate_limit'], {
    encoding: 'utf8',
    env: token ? { ...process.env, GH_TOKEN: token } : process.env,
    maxBuffer: 4 * 1024 * 1024,
  });
  if (result.error) throw new GithubApiBudgetError(result.error.message, 'budget-unavailable');
  if (result.status !== 0) {
    const message = result.stderr?.trim() || `gh api rate_limit exited with status ${result.status}`;
    throw new GithubApiBudgetError(message,
      isGithubRateLimitError(message) ? 'api-budget' : 'budget-unavailable');
  }
  let rateLimit;
  try {
    rateLimit = normalizeRateLimit(JSON.parse(result.stdout));
  } catch {
    throw new GithubApiBudgetError('GitHub API returned invalid rate-limit state', 'budget-unavailable');
  }
  const core = rateLimit.core;
  if (!core || core.limit <= 0 || core.remaining < 0 || core.remaining > core.limit
    || reserve >= core.limit) {
    throw new GithubApiBudgetError('GitHub API returned no usable core capacity', 'budget-unavailable');
  }
  if (core.remaining - reserve < requested) {
    throw new GithubApiBudgetError(
      `GitHub API core capacity insufficient: ${core.remaining}/${core.limit} remaining; `
      + `${reserve} reserved for ordinary Activity and ${requested} requested for Value; reset at ${core.resetAt}`,
    );
  }
  const state = validateState({
    schemaVersion: 1,
    status: 'open',
    preparedAt: now().toISOString(),
    resetAt: core.resetAt,
    limit: core.limit,
    remainingAtPrepare: core.remaining,
    reserve,
    granted: requested,
    consumed: 0,
    credentialId,
  });
  atomicWrite(path.resolve(filename), state);
  return state;
}

export function consumeGithubApiBudget(filename, count = 1) {
  if (typeof filename !== 'string' || !filename || !validInteger(count, 1, MAX_REQUESTS)) {
    throw new GithubApiBudgetError('GitHub API budget consumption is invalid', 'budget-unavailable');
  }
  const resolved = path.resolve(filename);
  let info;
  try {
    info = lstatSync(resolved);
  } catch {
    throw new GithubApiBudgetError('GitHub API budget state is unavailable', 'budget-unavailable');
  }
  if (!info.isFile() || info.isSymbolicLink()) {
    throw new GithubApiBudgetError('GitHub API budget state must be a regular file', 'budget-unavailable');
  }
  let state;
  try {
    state = validateState(JSON.parse(readFileSync(resolved, 'utf8')));
  } catch (error) {
    if (error instanceof GithubApiBudgetError) throw error;
    throw new GithubApiBudgetError('GitHub API budget state is unreadable', 'budget-unavailable');
  }
  if (state.consumed + count > state.granted) {
    throw new GithubApiBudgetError(
      `GitHub API Value request budget exhausted: ${state.consumed}/${state.granted} requests consumed`,
    );
  }
  state.consumed += count;
  atomicWrite(resolved, state);
  return state;
}

export function githubApiBudgetConsumerFromEnvironment() {
  const filename = process.env.CAO_GITHUB_API_BUDGET_FILE;
  return filename ? () => consumeGithubApiBudget(filename) : null;
}
