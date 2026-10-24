import { createDebug, diagnosticErrorName } from '../../debug.js';
import { timestampMilliseconds } from './schema.js';

const debugDailyRunCounts = createDebug('daily-run-counts');

export const DAILY_RUN_COUNT_STORES = /** @type {const} */ ([
  'dailyRunCounts', 'runCountContributions', 'dailyRunCountState'
]);
export const DAILY_RUN_COUNT_STATE_ID = 'daily-run-counts';
export const RUN_COUNT_FIELDS = /** @type {const} */ ([
  'total', 'successful', 'failed', 'approval', 'pending', 'other'
]);
const COMPACT_FIELDS = [
  'id', 'repositoryId', 'workflowId', 'owner', 'repository', 'workflowPath',
  'startedAt', 'status', 'conclusion', 'attempt', 'updatedAt', 'completedAt'
];

/** @param {Record<string, unknown>} run */
export function compactRunCountRecord(run) {
  return Object.fromEntries(COMPACT_FIELDS.filter((field) => run[field] !== undefined)
    .map((field) => {
      const time = ['startedAt', 'updatedAt', 'completedAt'].includes(field)
        ? timestampMilliseconds(run[field]) : null;
      return [field, time === null ? run[field] : new Date(time).toISOString()];
    }));
}

/** @param {unknown} value */
function timestamp(value) {
  return timestampMilliseconds(value) ?? 0;
}

/**
 * Receipts retain only count-relevant run facts, not run detail or telemetry.
 * Explicit attempt and update/end timestamps fence stale replay after eviction.
 * @param {Record<string, unknown> | undefined} previous
 * @param {Record<string, unknown>} incoming
 */
export function reconcileRunCountRecord(previous, incoming) {
  const next = compactRunCountRecord(incoming);
  if (!previous) return { run: next, conflict: false };
  const before = compactRunCountRecord(previous);
  let merged = { ...before, ...next };
  const attemptDelta = Number(merged.attempt ?? 1) - Number(before.attempt ?? 1);
  const oldTime = Math.max(timestamp(before.updatedAt), timestamp(before.completedAt));
  const newTime = Math.max(timestamp(next.updatedAt), timestamp(next.completedAt));
  if (attemptDelta < 0 || (attemptDelta === 0 && newTime > 0 && oldTime > newTime)) {
    return { run: before, conflict: false };
  }
  if (attemptDelta > 0) {
    for (const field of ['startedAt', 'status', 'conclusion', 'updatedAt', 'completedAt']) {
      if (!Object.hasOwn(next, field)) delete merged[field];
    }
  }
  if (attemptDelta === 0 && oldTime > 0 && newTime === 0) {
    merged = { ...next, ...before };
  }
  const different = JSON.stringify(COMPACT_FIELDS.map((field) => before[field] ?? null))
    !== JSON.stringify(COMPACT_FIELDS.map((field) => merged[field] ?? null));
  const conflict = attemptDelta === 0 && oldTime > 0 && oldTime === newTime && different;
  if (conflict) {
    const key = (/** @type {Record<string, unknown>} */ value) =>
      JSON.stringify(COMPACT_FIELDS.map((field) => value[field] ?? null));
    const rank = (/** @type {Record<string, unknown>} */ value) =>
      Number(value.status === 'completed' || ['success', 'failure', 'startup-failure', 'stale',
        'timed-out', 'action-required'].includes(String(value.conclusion).replaceAll('_', '-')));
    return { run: rank(before) !== rank(merged)
      ? rank(before) > rank(merged) ? before : merged
      : key(before) > key(merged) ? before : merged, conflict: true };
  }
  return { run: merged, conflict: false };
}

/** @param {Record<string, unknown>} run */
export function dailyRunCountContribution(run) {
  const startedAt = typeof run.startedAt === 'string' ? run.startedAt : '';
  if (timestampMilliseconds(startedAt) === null) return null;
  if (![run.repositoryId, run.workflowId, run.owner, run.repository, run.workflowPath]
    .every((value) => typeof value === 'string' && value.length > 0)) return null;
  const day = new Date(startedAt).toISOString().slice(0, 10);
  const conclusion = String(run.conclusion ?? '').replaceAll('_', '-');
  const status = String(run.status ?? '');
  const category = conclusion === 'success' ? 'successful'
    : ['failure', 'startup-failure', 'stale', 'timed-out'].includes(String(conclusion)) ? 'failed'
    : conclusion === 'action-required' ? 'approval'
    : status !== '' && status !== 'completed' ? 'pending' : 'other';
  return {
    id: JSON.stringify([day, run.repositoryId, run.workflowId]),
    day, repositoryId: run.repositoryId, workflowId: run.workflowId,
    owner: run.owner, repository: run.repository, workflowPath: run.workflowPath,
    ...Object.fromEntries(RUN_COUNT_FIELDS.map((field) => [field, Number(field === 'total' || field === category)]))
  };
}

/** @template T @param {IDBRequest<T>} request @returns {Promise<T>} */
function result(request) {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error ?? new Error('Daily run count storage failed'));
  });
}

/** @param {Record<string, unknown> | undefined | null} record */
function storedBytes(record) {
  return record ? new TextEncoder().encode(JSON.stringify(record)).byteLength + 512 : 0;
}

/** @param {Record<string, unknown> | undefined} state */
export function protectedRunCountBytes(state) {
  return Number(state?.receiptBytes ?? 0) + Number(state?.dailyBytes ?? 0) + storedBytes(state);
}

/** @param {IDBDatabase} database */
export async function readProtectedRunCountBytes(database) {
  return protectedRunCountBytes(await result(database.transaction('dailyRunCountState')
    .objectStore('dailyRunCountState').get(DAILY_RUN_COUNT_STATE_ID)));
}

/** Current UTC Monday and its two preceding weeks. @param {number} now */
export function dailyRunCountCutoff(now) {
  if (!Number.isFinite(now)) throw new TypeError('Daily count watermark must be finite');
  const date = new Date(now);
  date.setUTCHours(0, 0, 0, 0);
  date.setUTCDate(date.getUTCDate() - (date.getUTCDay() + 6) % 7 - 14);
  return date.toISOString().slice(0, 10);
}

/** @param {Record<string, unknown>} run */
function runStartDay(run) {
  const time = timestampMilliseconds(run.startedAt);
  return time === null ? null : new Date(time).toISOString().slice(0, 10);
}

/**
 * Bounded reads in one transaction keep bucket deletion, receipt deletion, and
 * the monotonic replay fence atomic. No canonical detail is scanned or changed.
 * @param {IDBDatabase} database
 * @param {number} now
 * @param {AbortSignal} [signal]
 * @param {typeof IDBKeyRange} [keyRange]
 */
export async function evictDailyRunCounts(database, now, signal, keyRange = globalThis.IDBKeyRange) {
  signal?.throwIfAborted();
  const transaction = database.transaction([...DAILY_RUN_COUNT_STORES], 'readwrite');
  const done = new Promise((resolve, reject) => {
    transaction.oncomplete = () => resolve(undefined);
    transaction.onerror = transaction.onabort = () =>
      reject(transaction.error ?? new Error('Daily count eviction aborted'));
  });
  try {
    const states = transaction.objectStore('dailyRunCountState');
    const state = await result(states.get(DAILY_RUN_COUNT_STATE_ID));
    if (!state || state.status !== 'ready') {
      debugDailyRunCounts({ operation: 'evict', status: 'skipped', reason: state ? 'not-ready' : 'no-state' });
      await done;
      return;
    }
    const watermark = Math.max(Number(state.retentionWatermark ?? -Infinity), now);
    const cutoff = dailyRunCountCutoff(watermark);
    const end = new Date(Date.parse(cutoff) + 21 * 86400000).toISOString().slice(0, 10);
    if (state.retentionCutoff === cutoff) {
      states.put({ ...state, retentionWatermark: watermark });
      await done;
      return;
    }
    for (const storeName of ['dailyRunCounts', 'runCountContributions']) {
      const store = transaction.objectStore(storeName);
      /** @type {IDBValidKey | undefined} */
      let after;
      for (;;) {
        signal?.throwIfAborted();
        const records = await result(store.getAll(after === undefined ? undefined : keyRange.lowerBound(after, true), 250));
        for (const record of records) {
          const day = storeName === 'dailyRunCounts' ? record.day : record.retentionDay ?? runStartDay(record.run);
          let observedAt = Number(record.observedAt
            ?? Math.max(timestamp(record.run?.updatedAt), timestamp(record.run?.completedAt)));
          if (storeName === 'runCountContributions' && !day && observedAt > watermark) {
            const beforeBytes = storedBytes(record);
            record.observedAt = observedAt = watermark;
            store.put(record);
            state.receiptBytes = Number(state.receiptBytes ?? 0) + storedBytes(record) - beforeBytes;
          }
          if (day ? day >= cutoff && day < end : observedAt >= Date.parse(`${cutoff}T00:00:00Z`)) continue;
          store.delete(record.id);
          if (storeName === 'dailyRunCounts') {
            state.dailyBytes = Math.max(0, Number(state.dailyBytes ?? 0) - storedBytes(record));
            state.dailyBucketCount = Math.max(0, Number(state.dailyBucketCount ?? 0) - 1);
          } else {
            state.receiptBytes = Math.max(0, Number(state.receiptBytes ?? 0) - storedBytes(record));
            state.receiptCount = Math.max(0, Number(state.receiptCount ?? 0) - 1);
            state.excludedRuns = Math.max(0, Number(state.excludedRuns ?? 0) - Number(!record.contribution && !record.retired));
            state.conflictedRuns = Math.max(0, Number(state.conflictedRuns ?? 0) - Number(record.conflict ?? false));
          }
        }
        if (records.length < 250) break;
        after = records.at(-1).id;
      }
    }
    states.put({ ...state, retentionWatermark: watermark, retentionCutoff: cutoff });
    transaction.commit?.();
    await done;
    debugDailyRunCounts({
      operation: 'evict', status: 'advanced', cutoff,
      dailyBucketCount: Number(state.dailyBucketCount ?? 0), receiptCount: Number(state.receiptCount ?? 0)
    });
  } catch (error) {
    debugDailyRunCounts({ operation: 'evict', status: 'failed', error: diagnosticErrorName(error) });
    try { transaction.abort(); } catch { /* Already completed. */ }
    await done.catch(() => undefined);
    throw error;
  }
}

/**
 * Reconcile a bounded page inside its canonical write transaction, reading and
 * publishing shared accounting state once rather than for every receipt.
 * @param {IDBTransaction} transaction
 * @param {Record<string, unknown>[]} records
 * @param {Set<string>} [conflicts]
 */
export async function reconcileDailyRunCountsBatch(transaction, records, conflicts = new Set()) {
  if (!records.length) return;
  const states = transaction.objectStore('dailyRunCountState');
  const state = await result(states.get(DAILY_RUN_COUNT_STATE_ID));
  for (const record of records) {
    await reconcileDailyRunCounts(transaction, record, conflicts.has(String(record.id)), state);
  }
  if (state) states.put(state);
}

/**
 * Detail deletion deliberately does not reconcile count receipts.
 * @param {IDBTransaction} transaction
 * @param {Record<string, unknown>} record
 * @param {boolean} batchConflict
 * @param {Record<string, any> | undefined} state
 */
async function reconcileDailyRunCounts(transaction, record, batchConflict, state) {
  const receipts = transaction.objectStore('runCountContributions');
  const daily = transaction.objectStore('dailyRunCounts');
  const previous = await result(receipts.get(/** @type {IDBValidKey} */ (record.id)));
  const reconciled = reconcileRunCountRecord(previous?.run, record);
  const run = reconciled.run;
  const day = runStartDay(run);
  const cutoff = state?.retentionCutoff;
  const revisionTime = Math.max(timestamp(run.updatedAt), timestamp(run.completedAt));
  const end = cutoff ? new Date(Date.parse(cutoff) + 21 * 86400000).toISOString().slice(0, 10) : '';
  const expired = Boolean(cutoff && day && (day < cutoff || day >= end));
  const contribution = expired ? null : dailyRunCountContribution(run);
  if (expired && !previous) return;
  if (!previous && cutoff && !day && revisionTime > 0 && revisionTime < Date.parse(cutoff)) return;
  if (expired && !previous.retired
    && Number(run.attempt ?? 1) <= Number(previous.run.attempt ?? 1)
    && revisionTime
      <= Math.max(timestamp(previous.run.updatedAt), timestamp(previous.run.completedAt))) return;
  const conflict = Boolean(batchConflict || reconciled.conflict || (previous?.conflict
    && run.attempt === previous.run.attempt
    && run.updatedAt === previous.run.updatedAt && run.completedAt === previous.run.completedAt));
  const changes = JSON.stringify(previous?.contribution ?? null) === JSON.stringify(contribution)
    ? [] : [[previous?.contribution, -1], [contribution, 1]];
  let dailyBytesDelta = 0;
  let dailyCountDelta = 0;
  for (const [value, sign] of changes) {
    if (!value) continue;
    const current = await result(daily.get(value.id));
    const next = { ...value };
    for (const field of RUN_COUNT_FIELDS) {
      next[field] = Number(current?.[field] ?? 0) + Number(sign) * Number(value[field]);
      if (!Number.isSafeInteger(next[field]) || next[field] < 0) throw new Error('Invalid daily run count delta');
    }
    if (next.total === 0) daily.delete(next.id);
    else daily.put(next);
    dailyBytesDelta += storedBytes(next.total === 0 ? null : next) - storedBytes(current);
    dailyCountDelta += Number(next.total !== 0) - Number(Boolean(current));
  }
  const observedAt = previous?.observedAt
    ?? Math.min(Number(state?.retentionWatermark) || Date.now(), revisionTime || Infinity);
  const retentionDay = expired || !day
    ? previous?.retentionDay ?? (previous ? runStartDay(previous.run) : null) : null;
  const receipt = { id: record.id, run, contribution, conflict, observedAt,
    ...(expired ? { retired: true } : {}), ...(retentionDay ? { retentionDay } : {}) };
  receipts.put(receipt);
  if (state) {
    state.excludedRuns = Number(state.excludedRuns ?? 0) + Number(!contribution && !expired) - Number(Boolean(previous && !previous.contribution && !previous.retired));
    state.conflictedRuns = Number(state.conflictedRuns ?? 0) + Number(conflict) - Number(previous?.conflict ?? false);
    state.receiptBytes = Number(state.receiptBytes ?? 0) + storedBytes(receipt) - storedBytes(previous);
    state.dailyBytes = Number(state.dailyBytes ?? 0) + dailyBytesDelta;
    state.receiptCount = Number(state.receiptCount ?? 0) + Number(!previous);
    state.dailyBucketCount = Number(state.dailyBucketCount ?? 0) + dailyCountDelta;
  }
}
