import { createHash } from 'node:crypto';
import { lstat, readFile } from 'node:fs/promises';
import path from 'node:path';
import { gunzipSync } from 'node:zlib';
import { validateOperationalValueEvidence } from '../dashboard/site/src/data/model/operational-value-evidence.js';
import { assertValueHistory } from './workflow-value-history.mjs';

const MAX_BYTES = 8 * 1024 * 1024;
const EVIDENCE_PATH = /^\.github\/value-evidence\/[A-Za-z0-9._/-]+\.jsonl(?:\.gz)?$/;
const same = (left, right) => typeof left === 'string' && typeof right === 'string'
  && left.toLowerCase() === right.toLowerCase();

async function confinedEvidenceFile(root, reference) {
  if (!reference || typeof reference !== 'object' || Array.isArray(reference)
    || Object.keys(reference).sort().join(',') !== 'path,sha256'
    || !EVIDENCE_PATH.test(reference.path)
    || reference.path.split('/').some((part) => !part || part === '.' || part === '..')
    || !/^[a-f0-9]{64}$/.test(reference.sha256)) {
    throw new Error('Reviewed workflow Value evidence reference is invalid');
  }
  let current = path.resolve(root);
  try {
    for (const part of reference.path.split('/')) {
      current = path.join(current, part);
      if ((await lstat(current)).isSymbolicLink()) {
        throw new Error('Reviewed workflow Value evidence symlinks are forbidden');
      }
    }
    const info = await lstat(current);
    if (!info.isFile() || info.size > MAX_BYTES) {
      throw new Error('Reviewed workflow Value evidence must be a bounded regular file');
    }
  } catch (error) {
    if (error.code === 'ENOENT') throw new Error('Reviewed workflow Value evidence input is missing', { cause: error });
    throw error;
  }
  return current;
}

export function acceptValueEvidenceHistory(previous, incoming) {
  validateOperationalValueEvidence(incoming);
  if (!previous) return { packet: incoming, updated: true };
  validateOperationalValueEvidence(previous);
  if (previous.id !== incoming.id) throw new Error('Operational Value evidence identity conflict');
  if (JSON.stringify(previous) === JSON.stringify(incoming)) return { packet: previous, updated: false };
  const previousCapture = Date.parse(previous.observedAt);
  const incomingCapture = Date.parse(incoming.observedAt);
  if (incomingCapture === previousCapture) throw new Error('Conflicting evidence packet at the same capture time');
  if (incomingCapture < previousCapture) throw new Error('Older evidence cannot replace newer accepted evidence');
  assertValueHistory(previous, incoming);
  return { packet: incoming, updated: true };
}

export async function readReviewedValueEvidence({ root, entry, retained = [], now = Date.now() }) {
  const filename = await confinedEvidenceFile(root, entry.evidence);
  const source = await readFile(filename);
  const decoded = filename.endsWith('.gz')
    ? gunzipSync(source, { maxOutputLength: MAX_BYTES })
    : source;
  if (decoded.length > MAX_BYTES
    || createHash('sha256').update(decoded).digest('hex') !== entry.evidence.sha256) {
    throw new Error('Reviewed workflow Value evidence digest mismatch');
  }
  const envelope = JSON.parse(decoded.toString('utf8'));
  if (envelope?.schema_version !== 2 || envelope?.kind !== 'operational_value_evidence'
    || Object.keys(envelope).sort().join(',') !== 'kind,operational_value_evidence,schema_version') {
    throw new Error('Reviewed workflow Value evidence must be one original schema-v2 envelope');
  }
  const packet = validateOperationalValueEvidence(envelope.operational_value_evidence);
  if (!same(packet.repository, entry.repository) || packet.workflow !== entry.workflow
    || packet.sourcePath !== `.github/workflows/${entry.workflow}.md`
    || Date.parse(packet.observedAt) > now) {
    throw new Error('Reviewed workflow Value evidence source identity mismatch');
  }
  const previous = retained.find((candidate) => candidate.id === packet.id);
  const accepted = acceptValueEvidenceHistory(previous, packet);
  return {
    envelope: {
      schema_version: 2,
      kind: 'operational_value_evidence',
      operational_value_evidence: accepted.packet,
    },
    updated: accepted.updated,
  };
}
