import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { existsSync, statSync } from 'node:fs';
import { mkdtemp, readFile, rename, rm } from 'node:fs/promises';
import path from 'node:path';
import { promisify } from 'node:util';
import {
  fileDietAcquisitionRequirements,
  refreshFileDiet,
} from '../skills/add-operational-value/examples/acquire-file-diet.mjs';
import { readFileDietProof } from '../skills/add-operational-value/examples/daily-file-diet.mjs';
import { assertValueHistory } from '../../activity/workflow-value-history.mjs';
import { validateOperationalValueEvidence } from '../../dashboard/site/src/data/model/operational-value-evidence.js';

// Runtime identity of the existing retrospective instrument, not a new evaluator.
export const definition = {
  schemaVersion: 3,
  repository: 'github/gh-aw',
  slug: 'daily-file-diet',
  sourcePath: '.github/workflows/daily-file-diet.md',
  adoption: { commit: '1186030a620f4113f655c156bedf70cf2c164f79', adoptedAt: '2025-11-15T13:36:21Z' },
  evidence: { repositories: ['github/gh-aw'], window: { cadenceDays: 1, maturationDays: 0 } },
  acquisition: {
    requirements: fileDietAcquisitionRequirements,
  },
};

const executeFile = promisify(execFile);
const historicalRevision = '0782148c0af21f4f1d10b7cb5e81b571ed58c463';
const historicalPath = 'docs/operational-value/proof/file-diet-lifetime';

export async function seedFileDietProof(directory, git = (...args) =>
  executeFile('git', args, { maxBuffer: 1024 * 1024 })) {
  await git('init', '--quiet', directory);
  await git('-C', directory, 'remote', 'add', 'origin', 'https://github.com/githubnext/gh-cao.git');
  await git('-C', directory, 'fetch', '--quiet', '--depth=1', '--filter=blob:none', 'origin', historicalRevision);
  const { stdout } = await git('-C', directory, 'ls-tree', '-r', historicalRevision, '--', historicalPath);
  const entries = stdout.trim().split('\n');
  assert.ok(entries.length <= 1024, 'Historical proof exceeds its file budget');
  const files = entries.map(entry => {
    const match = /^100644 blob [a-f0-9]{40}\t(docs\/operational-value\/proof\/file-diet-lifetime\/(?:proof|inventory-[a-f0-9]{40})\.json\.gz)$/.exec(entry);
    assert.ok(match, 'Historical proof must contain only regular, confined evidence archives');
    return match[1];
  });
  await git('-C', directory, 'sparse-checkout', 'set', '--no-cone', `/${historicalPath}/`);
  await git('-C', directory, '-c', 'core.hooksPath=/dev/null', 'checkout', '--quiet', '--detach', historicalRevision);
  const bytes = files.reduce((total, file) => total + statSync(path.join(directory, file)).size, 0);
  assert.ok(bytes <= 16 * 1024 * 1024, 'Historical proof exceeds its byte budget');
  const proof = path.join(directory, historicalPath);
  readFileDietProof(proof);
  return proof;
}

export async function collectValueEvidence(request, acquire = refreshFileDiet, seed = seedFileDietProof) {
  assert.equal(request.repository.toLowerCase(), definition.repository);
  assert.equal(request.workflow, definition.slug);
  assert.ok(Number.isFinite(Date.parse(request.observedAt)), 'A valid collection cutoff is required');
  const endAt = new Date(request.observedAt).toISOString().replace(/\.\d{3}Z$/, 'Z');
  const retained = path.join(request.workspace.directory, 'latest');
  const staging = await mkdtemp(path.join(request.workspace.directory, '.capture-'));
  const output = path.join(staging, 'report');
  try {
    const previous = existsSync(retained) ? path.join(retained, 'proof') : await seed(path.join(staging, 'seed'));
    await acquire({
      endAt, output, scratch: request.workspace.scratch, previous,
    });
    const { proof } = readFileDietProof(path.join(output, 'proof'));
    assert.equal(proof.adoption.commit, definition.adoption.commit, 'Frozen adoption changed');
    assert.equal(proof.adoption.at, definition.adoption.adoptedAt, 'Frozen adoption time changed');
    const envelope = JSON.parse(await readFile(path.join(output, 'daily-file-diet-pulse-evidence.jsonl'), 'utf8'));
    assert.equal(envelope.schema_version, 2);
    assert.equal(envelope.kind, 'operational_value_evidence');
    const packet = validateOperationalValueEvidence(envelope.operational_value_evidence);
    assert.equal(packet.repository, definition.repository);
    assert.equal(packet.workflow, definition.slug);
    assert.equal(Date.parse(packet.windowEnd), Date.parse(endAt));
    assert.ok(Date.parse(packet.observedAt) <= Date.now(), 'Capture cannot be in the future');
    assert.equal(packet.facts.find(fact => fact['fact-kind'] === 'contract')['cadence-days'], 1);
    assert.ok(packet.facts.some(fact => fact['fact-kind'] === 'coverage'));
    assert.ok(packet.facts.filter(fact => fact['fact-kind'] === 'coverage')
      .every(fact => fact['coverage-status'] === 'complete'), 'Incomplete acquisition; retaining accepted proof');
    assertValueHistory(request.previous, packet);
    const retired = path.join(staging, 'retired');
    if (existsSync(retained)) await rename(retained, retired);
    try {
      await rename(output, retained);
    } catch (error) {
      if (existsSync(retired)) await rename(retired, retained);
      throw error;
    }
    return envelope;
  } finally {
    await rm(staging, { recursive: true, force: true });
  }
}
