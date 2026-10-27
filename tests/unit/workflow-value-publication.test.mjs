import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { gunzipSync, gzipSync } from 'node:zlib';
import {
  acceptValueEvidenceHistory,
  readReviewedValueEvidence,
} from '../../activity/workflow-value-evidence.mjs';
import { runWorkflowValues } from '../../activity/workflow-values.mjs';

const root = path.resolve(import.meta.dirname, '../..');
const source = path.join(root, '.github/value-evidence/daily-file-diet.jsonl.gz');
const decoded = gunzipSync(await readFile(source));
const envelope = JSON.parse(decoded);
const packet = envelope.operational_value_evidence;
const sha256 = bytes => createHash('sha256').update(bytes).digest('hex');
const reference = {
  path: '.github/value-evidence/daily-file-diet.jsonl.gz',
  sha256: '9afaf3b49a02c1eda0c634016bae98bf0be0a5c1b63f1ae6c6353b151301d19e',
};
const registration = {
  repository: 'github/gh-aw',
  workflow: 'daily-file-diet',
  collector: '.github/value-collectors/daily-file-diet.mjs',
  evidence: reference,
};

async function fixture(t, input = envelope) {
  const directory = await mkdtemp(path.join(tmpdir(), 'cao-value-publication-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const evidence = path.join(directory, reference.path);
  await mkdir(path.dirname(evidence), { recursive: true });
  const content = Buffer.from(`${JSON.stringify(input)}\n`);
  await writeFile(evidence, gzipSync(content, { level: 9 }));
  const entry = { ...registration, evidence: { ...reference, sha256: sha256(content) } };
  const policy = path.join(directory, '.github/workflows/cao.json');
  await mkdir(path.dirname(policy), { recursive: true });
  await writeFile(policy, JSON.stringify({
    version: 1,
    'gh-aw-version': 'v0.93.5',
    'control-plane': {
      scope: { 'allowed-owners': ['github'], 'allowed-repositories': ['github/gh-aw'] },
      activity: { repositories: ['github/gh-aw'] },
      campaigns: { activity: { 'workflow-values': [entry] } },
    },
  }));
  return {
    directory, entry, policy,
    output: path.join(directory, 'workflow-values.jsonl'),
    healthOutput: path.join(directory, 'workflow-value-collections.jsonl'),
    workspace: path.join(directory, 'workspaces'),
  };
}

test('verified retained File Diet evidence is digest-pinned and exact retries are idempotent', async () => {
  assert.equal(sha256(decoded), reference.sha256);
  const first = await readReviewedValueEvidence({ root, entry: registration, retained: [] });
  assert.equal(first.updated, true);
  assert.equal(first.envelope.operational_value_evidence.observedAt, '2026-10-18T12:05:52.580Z');
  assert.equal(first.envelope.operational_value_evidence.windowEnd, '2026-10-18T11:54:09Z');
  const repeated = await readReviewedValueEvidence({
    root, entry: registration, retained: [first.envelope.operational_value_evidence],
  });
  assert.equal(repeated.updated, false);
  assert.deepEqual(repeated.envelope, first.envelope);
});

test('reviewed evidence rejects digest mismatch, missing input and source identity mismatch', async (t) => {
  await assert.rejects(readReviewedValueEvidence({
    root, entry: { ...registration, evidence: { ...reference, sha256: '0'.repeat(64) } },
  }), /digest mismatch/);
  const missing = await fixture(t);
  missing.entry.evidence.path = '.github/value-evidence/missing.jsonl.gz';
  await assert.rejects(readReviewedValueEvidence({
    root: missing.directory, entry: missing.entry,
  }), /input is missing/);
  const mismatch = await fixture(t);
  mismatch.entry.repository = 'github/other';
  await assert.rejects(readReviewedValueEvidence({
    root: mismatch.directory, entry: mismatch.entry,
  }), /source identity mismatch/);
});

test('accepted history rejects changed contracts, same-capture conflicts and older replacement', () => {
  const changedContract = structuredClone(packet);
  changedContract.observedAt = '2026-10-19T12:05:52.580Z';
  changedContract.contractDigest = 'b'.repeat(64);
  assert.throws(() => acceptValueEvidenceHistory(packet, changedContract), /Frozen contract/);

  const conflict = structuredClone(packet);
  const output = conflict.facts.find((fact) => fact['fact-kind'] === 'output');
  output.state = output.state === 'closed' ? 'open' : 'closed';
  assert.throws(() => acceptValueEvidenceHistory(packet, conflict), /same capture/);

  const older = structuredClone(packet);
  older.observedAt = new Date(Date.parse(packet.observedAt) - 1).toISOString();
  assert.throws(() => acceptValueEvidenceHistory(packet, older), /Older evidence/);
});

test('Activity imports historical evidence while independently publishing current deferral health', async (t) => {
  const options = await fixture(t);
  let checkedAt = '2026-10-27T18:00:00.000Z';
  const result = await runWorkflowValues({
    root: options.directory, policy: options.policy, output: options.output,
    healthOutput: options.healthOutput, workspace: options.workspace,
    repositories: ['github/gh-aw'], defer: 'shared-budget-unavailable',
    now: () => checkedAt,
  });
  assert.deepEqual({ imported: result.imported, deferred: result.deferred, failed: result.failed },
    { imported: 1, deferred: 1, failed: 0 });
  const accepted = JSON.parse(await readFile(options.output, 'utf8')).operational_value_evidence;
  assert.equal(accepted.observedAt, packet.observedAt);
  assert.equal(accepted.windowStart, packet.windowStart);
  assert.equal(accepted.windowEnd, packet.windowEnd);
  const health = JSON.parse(await readFile(options.healthOutput, 'utf8')).workflow_value_collection;
  assert.equal(health.observedAt, checkedAt);
  assert.equal(health.state, 'deferred');
  assert.equal(health.reasonCode, 'shared-budget-unavailable');
  assert.equal(health.lastAttemptAt, undefined);

  checkedAt = '2026-10-27T19:00:00.000Z';
  const repeated = await runWorkflowValues({
    root: options.directory, policy: options.policy, output: options.output,
    healthOutput: options.healthOutput, workspace: options.workspace,
    repositories: ['github/gh-aw'], defer: 'shared-budget-unavailable',
    now: () => checkedAt,
  });
  assert.equal(repeated.imported, 0);
  assert.equal(JSON.parse(await readFile(options.output, 'utf8')).operational_value_evidence.observedAt,
    packet.observedAt);
});

test('failed reviewed import leaves accepted evidence and health unchanged', async (t) => {
  const options = await fixture(t);
  await runWorkflowValues({
    root: options.directory, policy: options.policy, output: options.output,
    healthOutput: options.healthOutput, workspace: options.workspace,
    repositories: ['github/gh-aw'], defer: 'shared-budget-unavailable',
  });
  const accepted = await readFile(options.output, 'utf8');
  const health = await readFile(options.healthOutput, 'utf8');
  const policy = JSON.parse(await readFile(options.policy, 'utf8'));
  policy['control-plane'].campaigns.activity['workflow-values'][0].evidence.sha256 = '0'.repeat(64);
  await writeFile(options.policy, JSON.stringify(policy));
  await assert.rejects(runWorkflowValues({
    root: options.directory, policy: options.policy, output: options.output,
    healthOutput: options.healthOutput, workspace: options.workspace,
    repositories: ['github/gh-aw'], defer: 'shared-budget-unavailable',
  }), /digest mismatch/);
  assert.equal(await readFile(options.output, 'utf8'), accepted);
  assert.equal(await readFile(options.healthOutput, 'utf8'), health);
});
