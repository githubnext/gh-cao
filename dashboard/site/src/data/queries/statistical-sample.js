/**
 * A frozen projection of the current canonical snapshot, not a historical
 * reservoir. Selection precedes every consumer's predicates and never evicts
 * exact evidence. The caller supplies a recorded logical clock.
 */
import { timestampMilliseconds } from '../model/schema.js';
import { createDashboardQueryBudget, DashboardQueryCancelledError, DASHBOARD_QUERY_LIMITS } from './declarative.js';
import { createDebug } from '../../debug.js';

export const STATISTICAL_CAPACITIES = Object.freeze({ postgres: 64000, sqlite: 8000, indexeddb: 16000 });
export const STATISTICAL_VERSION = 'canonical-snapshot-bottom-k-v1';
export const STATISTICAL_FRAME_VERSION = 'canonical-snapshot-frame-v2';
export const STATISTICAL_INPUT_LIMITS = Object.freeze({
  rows: DASHBOARD_QUERY_LIMITS['max-input-rows'], bytes: 256 * 1024 * 1024
});
const SEED = 'cao-statistical-v1';
const WEEK = 7 * 86400000;
const FAILURES = new Set(['failure', 'timed_out', 'action_required', 'startup_failure']);
const OUTCOMES = new Set(['success', ...FAILURES, 'cancelled', 'skipped', 'neutral', 'stale']);
const HASH_BATCH_SIZE = 64;
const debugSampling = createDebug('data:sampling');

/** @param {unknown} value */
function instant(value) {
  return timestampMilliseconds(value) ?? NaN;
}

/** @param {Record<string, unknown>[]} rows */
export function canonicalStatisticalWatermark(rows) {
  let latest = -Infinity;
  for (const row of rows) {
    for (const field of ['startedAt', 'createdAt', 'completedAt', 'updatedAt']) {
      const at = instant(row[field]);
      if (Number.isFinite(at)) latest = Math.max(latest, at);
    }
  }
  return Number.isFinite(latest) ? new Date(latest).toISOString() : '';
}

/** @param {string[]} parts @param {() => void} [checkpoint] */
function framed(parts, checkpoint) {
  const encoder = new TextEncoder();
  const lengths = parts.map((part, index) => {
    if (index % 256 === 0) checkpoint?.();
    return encoder.encode(part).length;
  });
  const bytes = new Uint8Array(lengths.reduce((size, length) => size + 4 + length, 0));
  const view = new DataView(bytes.buffer);
  let offset = 0;
  for (let index = 0; index < parts.length; index++) {
    if (index % 256 === 0) checkpoint?.();
    view.setUint32(offset, lengths[index]);
    offset += 4;
    encoder.encodeInto(parts[index], bytes.subarray(offset, offset + lengths[index]));
    offset += lengths[index];
  }
  return bytes;
}

/** @param {string} identity @param {string} [seed] */
export async function statisticalPriority(identity, seed = SEED) {
  seed ||= SEED;
  return digest([STATISTICAL_VERSION, seed, identity]);
}

/** @param {string[]} parts @param {() => void} [checkpoint] */
async function digest(parts, checkpoint) {
  const digest = await globalThis.crypto.subtle.digest('SHA-256', framed(parts, checkpoint));
  return [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, '0')).join('');
}

/** @param {unknown} value @returns {string} */
function stableJSON(value) {
  if (Array.isArray(value)) return `[${value.map(stableJSON).join(',')}]`;
  if (value && typeof value === 'object') {
    const record = /** @type {Record<string, unknown>} */ (value);
    return `{${Object.keys(record).sort().map((key) => `${JSON.stringify(key)}:${stableJSON(record[key])}`).join(',')}}`;
  }
  return JSON.stringify(value) ?? 'null';
}

/** @param {Map<string, number>} counts @param {number} capacity */
function allocate(counts, capacity) {
  const total = [...counts.values()].reduce((sum, count) => sum + count, 0);
  const budget = Math.min(capacity, total);
  const quotas = [...counts].map(([key, count]) => ({
    key, count: total ? Math.floor(budget * count / total) : 0,
    remainder: total ? (budget * count) % total : 0
  }));
  let remainder = budget - quotas.reduce((sum, quota) => sum + quota.count, 0);
  quotas.sort((a, b) => b.remainder - a.remainder || compare(a.key, b.key));
  for (const quota of quotas) if (remainder-- > 0) quota.count++;
  return new Map(quotas.map(({ key, count }) => [key, count]));
}

/** @param {string} a @param {string} b */
function compare(a, b) { return a < b ? -1 : a > b ? 1 : 0; }

/** @typedef {{ row: Record<string, unknown>, priority: string }} Candidate */

/** @param {Candidate} a @param {Candidate} b */
function compareCandidates(a, b) {
  return compare(a.priority, b.priority) || compare(String(a.row.id), String(b.row.id));
}

/** @param {Candidate[]} heap @param {Candidate} candidate @param {number} capacity @param {() => void} checkpoint */
function retainCandidate(heap, candidate, capacity, checkpoint) {
  if (heap.length < capacity) {
    let index = heap.length;
    heap.push(candidate);
    while (index > 0) {
      checkpoint();
      const parent = Math.floor((index - 1) / 2);
      if (compareCandidates(heap[parent], candidate) >= 0) break;
      heap[index] = heap[parent];
      index = parent;
    }
    heap[index] = candidate;
    return;
  }
  if (compareCandidates(candidate, heap[0]) >= 0) return;
  let index = 0;
  while (index * 2 + 1 < heap.length) {
    checkpoint();
    let child = index * 2 + 1;
    if (child + 1 < heap.length && compareCandidates(heap[child + 1], heap[child]) > 0) child++;
    if (compareCandidates(candidate, heap[child]) >= 0) break;
    heap[index] = heap[child];
    index = child;
  }
  heap[index] = candidate;
}

/**
 * @param {Record<string, unknown>[]} rows
 * @param {{ watermark: unknown, capacity?: number, complete?: boolean, seed?: string, hotIds?: Iterable<string>, checkpoint?: () => void }} options
 */
export async function sampleStatisticalRuns(rows, options) {
  const startedAt = performance.now();
  const diagnostics = { stage: 'validation', inputBytes: 0, hashBatches: 0, maxPendingHashes: 0 };
  let status = 'failed';
  let reason = '';
  /** @type {Record<string, unknown>} */
  let frame = {};
  try {
    const result = await selectStatisticalRuns(rows, options, diagnostics);
    frame = result.frame;
    status = 'completed';
    return result;
  } catch (error) {
    if (error instanceof DashboardQueryCancelledError) {
      status = 'cancelled';
      reason = error.kind;
    }
    throw error;
  } finally {
    debugSampling({
      event: 'statistical-sample', status, reason, ...diagnostics,
      durationMs: Math.round(performance.now() - startedAt), inputCount: rows.length,
      populationCount: frame['population-count'] ?? 0, selectedCount: frame['selected-count'] ?? 0,
      stratumCount: Array.isArray(frame.strata) ? frame.strata.length : 0,
      invalidCount: frame['invalid-count'] ?? 0, duplicateConflicts: frame['duplicate-conflicts'] ?? 0,
      weightValid: frame['weight-valid'] === true
    });
  }
}

/**
 * @param {Record<string, unknown>[]} rows
 * @param {Parameters<typeof sampleStatisticalRuns>[1]} options
 * @param {{ stage: string, inputBytes: number, hashBatches: number, maxPendingHashes: number }} diagnostics
 */
async function selectStatisticalRuns(rows, options, diagnostics) {
  const budget = createDashboardQueryBudget();
  const checkpoint = () => { budget.spend(1); options.checkpoint?.(); };
  if (rows.length > STATISTICAL_INPUT_LIMITS.rows) throw new Error('Statistical candidate frame exceeds input row budget');
  const watermark = instant(options.watermark);
  const capacity = options.capacity ?? STATISTICAL_CAPACITIES.indexeddb;
  if (!Number.isSafeInteger(capacity) || capacity < 0 || capacity > 100000) throw new Error('Invalid statistical capacity');
  if (!Number.isFinite(watermark)) throw new Error('Statistical sampling requires an explicit canonical UTC watermark');
  const hot = new Set(options.hotIds ?? []);
  const seed = options.seed || SEED;
  /** @type {Map<string, { row: Record<string, unknown>, variants?: Set<string> }>} */
  const identities = new Map();
  let invalid = 0;
  let conflicts = 0;
  let inputBytes = 0;
  const encoder = new TextEncoder();
  diagnostics.stage = 'candidates';
  for (const row of rows) {
    checkpoint();
    const json = stableJSON(row);
    inputBytes += encoder.encode(json).byteLength;
    diagnostics.inputBytes = inputBytes;
    if (inputBytes > STATISTICAL_INPUT_LIMITS.bytes) throw new Error('Statistical candidate frame exceeds working byte budget');
    const id = row.id;
    if (typeof id !== 'string' || !/^github:run:[^/:]+\/[^:]+:[^:]+$/.test(id)) { invalid++; continue; }
    const prior = identities.get(id);
    if (prior) {
      prior.variants ??= new Set([stableJSON(prior.row)]);
      prior.variants.add(json);
    } else identities.set(id, { row });
  }
  conflicts = [...identities.values()].reduce((sum, entry) => sum + (entry.variants?.size ?? 1) - 1, 0);
  /** @type {Map<string, Array<{ row: Record<string, unknown>, priority: string }>>} */
  const groups = new Map();
  let incomplete = 0;
  let hotExcluded = 0;
  let quarantined = 0;
  for (const [id, { row, variants }] of identities) {
    checkpoint();
    if ((variants?.size ?? 1) > 1) { quarantined++; continue; }
    if (row.status !== 'completed') { incomplete++; continue; }
    const started = instant(row.startedAt);
    const at = Number.isFinite(started) ? started : instant(row.createdAt);
    if (!Number.isFinite(at) || typeof row.conclusion !== 'string' || !row.conclusion) { invalid++; continue; }
    if (at < watermark - WEEK || at >= watermark) continue;
    if (hot.has(id)) { hotExcluded++; continue; }
    const outcome = OUTCOMES.has(row.conclusion) ? row.conclusion : 'other';
    const key = `${new Date(at).toISOString().slice(0, 10)}/${outcome}`;
    let group = groups.get(key);
    if (!group) { group = []; groups.set(key, group); }
    group.push({ row, priority: '' });
  }
  const counts = new Map([...groups].map(([key, candidates]) => [key, candidates.length]));
  const failures = new Map([...counts].filter(([key]) => FAILURES.has(key.split('/')[1])));
  const reserve = allocate(failures, capacity > 0 ? Math.ceil(capacity / 10) : 0);
  const reserved = [...reserve.values()].reduce((sum, count) => sum + count, 0);
  const residual = new Map([...counts].map(([key, count]) => [key, count - (reserve.get(key) ?? 0)]));
  const remainder = allocate(residual, capacity - reserved);
  const valid = options.complete === true && invalid === 0 && conflicts === 0;
  /** @type {Record<string, unknown>[]} */
  const selected = [];
  /** @type {Array<{ stratum: string, population: number, allocation: number, cutoff: string | null }>} */
  const strata = [];
  const populationParts = [];
  diagnostics.stage = 'selection';
  for (const [key, candidates] of [...groups].sort(([a], [b]) => compare(a, b))) {
    const population = candidates.length;
    const count = (reserve.get(key) ?? 0) + (remainder.get(key) ?? 0);
    candidates.sort((a, b) => {
      checkpoint();
      return compare(String(a.row.id), String(b.row.id));
    });
    populationParts.push(key, String(population));
    for (const candidate of candidates) {
      checkpoint();
      populationParts.push(String(candidate.row.id));
    }
    /** @type {Candidate[]} */
    const retained = [];
    if (count > 0) {
      for (let offset = 0; offset < candidates.length; offset += HASH_BATCH_SIZE) {
        checkpoint();
        const batch = candidates.slice(offset, offset + HASH_BATCH_SIZE);
        diagnostics.hashBatches++;
        diagnostics.maxPendingHashes = Math.max(diagnostics.maxPendingHashes, batch.length);
        const priorities = await Promise.all(batch.map((candidate) => statisticalPriority(String(candidate.row.id), seed)));
        for (let index = 0; index < batch.length; index++) {
          checkpoint();
          retainCandidate(retained, { row: batch[index].row, priority: priorities[index] }, count, checkpoint);
        }
      }
      retained.sort((a, b) => {
        checkpoint();
        return compareCandidates(a, b);
      });
    }
    for (const candidate of retained) selected.push({
      ...Object.fromEntries(Object.entries(candidate.row).filter(([field]) => !field.startsWith('statistical-'))),
      'statistical-stratum': key, 'statistical-priority': candidate.priority,
      'statistical-population': population, 'statistical-allocation': count,
      'statistical-weight-valid': valid,
      ...(valid ? { 'statistical-probability': count / population, 'statistical-weight': population / count } : {})
    });
    strata.push({ stratum: key, population, allocation: count, cutoff: count > 0 ? retained[count - 1].priority : null });
  }
  const normalizedWatermark = new Date(watermark).toISOString();
  diagnostics.stage = 'frame';
  const frameId = await digest([
    STATISTICAL_FRAME_VERSION, STATISTICAL_VERSION, seed, 'frame', normalizedWatermark, String(capacity), String(valid),
    ...populationParts,
    ...strata.flatMap((stratum) => [
      stratum.stratum, String(stratum.population), String(stratum.allocation), stratum.cutoff ?? ''
    ]),
    ...selected.flatMap((row) => [String(row.id), String(row['statistical-priority']), String(row['statistical-stratum'])])
  ], checkpoint);
  checkpoint();
  for (const row of selected) row['statistical-frame-id'] = `sha256:${frameId}`;
  diagnostics.stage = 'complete';
  return {
    rows: selected,
    frame: {
      version: STATISTICAL_VERSION, 'frame-version': STATISTICAL_FRAME_VERSION,
      seed, watermark: normalizedWatermark, 'frame-id': `sha256:${frameId}`,
      'watermark-policy': 'explicit',
      'window-start': new Date(watermark - WEEK).toISOString(),
      population: 'canonical-snapshot-completed-runs', capacity,
      'timestamp-field': 'startedAt-or-createdAt', 'identity-field': 'id',
      'allocation-policy': 'largest-remainder-failure-reserve-v1',
      'failure-reserve-fraction': 0.1, 'failure-reserve-allocation': reserved,
      'selection-stage': 'before-consumer-filter',
      'hot-exclusion-policy': 'explicit-identities-only', 'hot-exclusion-default': 0,
      'input-count': rows.length, 'population-count': [...counts.values()].reduce((sum, count) => sum + count, 0),
      'selected-count': selected.length, 'invalid-count': invalid, 'incomplete-count': incomplete,
      'duplicate-conflicts': conflicts, 'quarantined-count': quarantined, 'hot-excluded-count': hotExcluded,
      'input-row-limit': STATISTICAL_INPUT_LIMITS.rows, 'input-byte-limit': STATISTICAL_INPUT_LIMITS.bytes,
      'weight-valid': valid, 'full-population-estimable': valid && strata.every((stratum) => stratum.allocation > 0),
      strata
    }
  };
}
