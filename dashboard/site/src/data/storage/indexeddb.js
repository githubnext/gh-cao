import { relationshipErrors } from '../model/schema.js';
import { DAILY_RUN_COUNT_STORES, DAILY_RUN_COUNT_STATE_ID, evictDailyRunCounts, reconcileDailyRunCountsBatch } from '../model/daily-run-counts.js';
import { pruneCanonicalRecord } from '../model/fields.js';
import { BROWSER_RETENTION_WINDOWS_MS, recordTimestamp, RETENTION_TIMESTAMPS as RETENTION_TIMESTAMP_FIELDS } from './retention.js';
import { scopedStorageKey } from '../../storage-scope.js';
import { createDebug } from '../../debug.js';
import { tidy } from '../../data-operations.js';
import {
  STORAGE_SHARD_STORE, maintainStorageShards, rebuildStorageShards,
  withStorageMetadata, writeAccountedRecords
} from './shards.js';
import {
  AUDIT_CURATION_TRANSACTION_ID,
  AUDIT_CURATION_VERSION,
  auditCurationRunFacts,
  discardAudit
} from '../model/audit-curation.js';

const debug = createDebug('data:indexeddb');

export const DATABASE_NAME = 'gh-aw-cao-dashboard-data';
export const DATABASE_VERSION = 46;
/** @type {Set<(versions: { oldVersion: number, newVersion: number }) => void>} */
const upgradeListeners = new Set();

/** @param {(versions: { oldVersion: number, newVersion: number }) => void} listener */
export function subscribeCanonicalDatabaseUpgrade(listener) {
  upgradeListeners.add(listener);
  return () => upgradeListeners.delete(listener);
}

export const CANONICAL_QUERY_INDEX_FIELDS = /** @type {Record<string, string[]>} */ ({
  byQuerySummary: ['summary'],
  byQueryDomain: ['domain'],
  byQueryMcpIdentity: ['source', 'type', 'mcpServer', 'mcpTool']
});

/** @param {string} [pathname] */
export function canonicalDatabaseName(pathname) {
  return scopedStorageKey(DATABASE_NAME, pathname);
}

export const ENTITY_STORES = /** @type {const} */ ([
  'campaigns',
  'repositories',
  'workflows',
  'runs',
  'domains',
  'tools',
  'skills',
  'friction',
  'audits',
  'issues',
  'operationalValues',
  'operationalValueEvidence',
  'workflowValueCollections',
  'marketplacePackages',
  'experiments', 'experimentAssignments', 'graders', 'graderObservations', 'evals', 'evalObservations'
]);
export const TRANSACTION_STORE = 'transactions';
export const DATABASE_STORES = /** @type {const} */ ([...ENTITY_STORES, TRANSACTION_STORE, STORAGE_SHARD_STORE, ...DAILY_RUN_COUNT_STORES]);
export const CANONICAL_DATABASE_SCHEMA = /** @type {Record<
 * string, { keyPath: string, indexes: Record<string, string | string[]> }
 * >} */ ({
 campaigns: {
   keyPath: 'id',
   indexes: { bySlug: 'slug' }
 },
 repositories: {
   keyPath: 'id',
   indexes: {}
 },
 workflows: {
   keyPath: 'id',
   indexes: { byRepository: 'repositoryId', byCampaign: 'campaignId' }
 },
 runs: {
   keyPath: 'id',
   indexes: {
     byRepository: 'repositoryId',
     byWorkflow: 'workflowId',
     byConclusion: 'conclusion',
     byEvent: 'event',
     byEventConclusion: ['event', 'conclusion']
   }
 },
 domains: {
   keyPath: 'id',
   indexes: {
     byRun: 'runId',
     byQuerySummary: '_queryKeys.byQuerySummary',
     byQueryDomain: '_queryKeys.byQueryDomain'
   }
 },
 tools: {
   keyPath: 'id',
   indexes: {
     byRun: 'runId',
     byQuerySummary: '_queryKeys.byQuerySummary',
     byQueryMcpIdentity: '_queryKeys.byQueryMcpIdentity',
     byTypeStatusRun: ['type', 'status', 'runId'],
     byTypeStatusRunSummary: ['type', 'status', 'runId', 'summary']
   }
 },
 skills: {
   keyPath: 'id',
   indexes: { byRun: 'runId' }
 },
 friction: {
   keyPath: 'id',
   indexes: { byRun: 'runId' }
 },
 audits: {
   keyPath: 'id',
   indexes: {
     byRun: 'runId',
     byQuerySummary: '_queryKeys.byQuerySummary',
     byTypeStatusRun: ['type', 'status', 'runId'],
     byTypeStatusRunSummary: ['type', 'status', 'runId', 'summary']
   }
  },
  issues: {
    keyPath: 'id',
    indexes: {
      byRun: 'runId',
      byQuerySummary: '_queryKeys.byQuerySummary'
    }
  },
  operationalValues: {
    keyPath: 'id',
    indexes: {
      byRepository: 'repositoryId',
      byValue: 'valueId'
    }
  },
  operationalValueEvidence: {
    keyPath: 'id',
    indexes: { byRepository: 'repositoryId', byWorkflow: 'workflowId' }
  },
  workflowValueCollections: {
    keyPath: 'id',
    indexes: { byRepository: 'repositoryId', byWorkflow: 'workflowId' }
  },
  marketplacePackages: {
    keyPath: 'id',
    indexes: {
      byRegistry: 'registryId',
      byRepository: 'repository'
    }
  },
  experiments: { keyPath: 'id', indexes: { byWorkflow: 'workflowId' } },
  experimentAssignments: { keyPath: 'id', indexes: { byRun: 'runId', byExperiment: 'experimentId' } },
  graders: { keyPath: 'id', indexes: { byWorkflow: 'workflowId' } },
  graderObservations: { keyPath: 'id', indexes: { byRun: 'runId', byGrader: 'graderId' } },
  evals: { keyPath: 'id', indexes: { byWorkflow: 'workflowId' } },
  evalObservations: { keyPath: 'id', indexes: { byRun: 'runId', byEval: 'evalId' } },
  transactions: {
    keyPath: 'id',
    indexes: { byCreatedAt: 'createdAt' }
  },
  storageShards: {
    keyPath: 'id',
    indexes: {}
  },
  dailyRunCounts: { keyPath: 'id', indexes: {} },
  runCountContributions: { keyPath: 'id', indexes: {} },
  dailyRunCountState: { keyPath: 'id', indexes: {} }
});
for (const storeName of ENTITY_STORES) {
  if (Object.hasOwn(RETENTION_TIMESTAMP_FIELDS, storeName)) {
    CANONICAL_DATABASE_SCHEMA[storeName].indexes.byStorageTimestamp = ['_storage.timestamp', 'id'];
  }
}
for (const storeName of [
  'domains', 'tools', 'skills', 'friction', 'audits', 'issues',
  'experimentAssignments', 'graderObservations', 'evalObservations'
]) {
  CANONICAL_DATABASE_SCHEMA[storeName].indexes.byStorageRun = ['runId', 'id'];
}
for (const storeName of ['experimentAssignments', 'graderObservations', 'evalObservations']) {
  CANONICAL_DATABASE_SCHEMA[storeName].indexes.byAudit = 'auditId';
}
const DEFAULT_WRITE_BATCH_SIZE = 1000;
const MAX_TRANSACTION_RECORDS = 1000;
const INGESTION_LOCK_ID = 'lock:canonical-ingestion';
const INGESTION_LOCK_LEASE_MS = 5 * 60 * 1000;
const INGESTION_LOCK_ACQUIRE_TIMEOUT_MS = INGESTION_LOCK_LEASE_MS + 30_000;
const INGESTION_LOCK_RETRY_DELAY_MS = 25;
const INGESTION_LOCK_WAITING_NOTICE_DELAY_MS = 500;
const MAX_QUERY_INDEX_LOOKUPS = 32;
const RETENTION_TIMESTAMPS = new Set([
  'runs',
  'domains',
  'tools',
  'skills',
  'friction',
  'audits',
  'issues',
  'operationalValues',
  'experimentAssignments', 'graderObservations', 'evalObservations'
]);
const RUN_LINKED_STORES = /** @type {const} */ ([
  'domains',
  'tools',
  'skills',
  'friction',
  'audits',
  'issues', 'experimentAssignments', 'graderObservations', 'evalObservations'
]);
const QUERYABLE_STRING_KEY_PATHS = new Set([
  'id',
  'slug',
  'repositoryId',
  'campaignId',
  'workflowId',
  'runId',
  'valueId',
  'conclusion',
  'event'
]);
const monotonicNow = () => globalThis.performance?.now() ?? Date.now();

/**
 * Physical index keys include absent/null dimensions so native selection never
 * silently drops observations. Query semantics remain in Dashboard Language;
 * these tuples encode raw canonical fields, not computed labels or page state.
 * @param {string} storeName
 * @param {Record<string, unknown>} record
 */
export function prepareCanonicalRecord(storeName, record) {
  const indexes = Object.keys(CANONICAL_DATABASE_SCHEMA[storeName]?.indexes ?? {})
    .filter((name) => Object.hasOwn(CANONICAL_QUERY_INDEX_FIELDS, name));
  if (indexes.length === 0) return withStorageMetadata(storeName, record);
  return withStorageMetadata(storeName, {
    ...record,
    _queryKeys: Object.fromEntries(indexes.flatMap((name) => {
      const values = CANONICAL_QUERY_INDEX_FIELDS[name].map((field) => record[field] ?? null);
      return values.every((value) => losslessQueryKeyValue(value))
        ? [[name, JSON.stringify(values)]] : [];
    }))
  });
}

/**
 * @param {unknown} value
 * @param {Set<object>} [seen]
 * @param {number} [depth]
 * @returns {boolean}
 */
function losslessQueryKeyValue(value, seen = new Set(), depth = 0) {
  if (value == null || ['string', 'boolean'].includes(typeof value)) return true;
  if (typeof value === 'number') return Number.isFinite(value);
  if (typeof value !== 'object' || depth > 64 || seen.has(value)
      || (!Array.isArray(value) && ![Object.prototype, null].includes(Object.getPrototypeOf(value)))) return false;
  seen.add(value);
  const safe = Object.values(value).every((child) => losslessQueryKeyValue(child, seen, depth + 1));
  seen.delete(value);
  return safe;
}

/**
 * @template {Record<string, unknown> | undefined | null} T
 * @param {T} record
 * @returns {T}
 */
function canonicalRecord(record) {
  if (record && Object.hasOwn(record, '_queryKeys')) delete record._queryKeys;
  if (record && Object.hasOwn(record, '_storage')) delete record._storage;
  return record;
}

/**
 * @template T
 * @param {IDBRequest<T>} request
 * @returns {Promise<T>}
 */
function requestResult(request) {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => {
      const error = request.error ?? new Error('IndexedDB request failed');
      debug({ event: 'request-failed', ...errorSummary(error) });
      reject(error);
    };
  });
}

/** @param {IDBTransaction} transaction @param {'error' | 'abort'} outcome */
function reportTransactionFailure(transaction, outcome) {
  debug({
    event: outcome === 'abort' ? 'transaction-aborted' : 'transaction-failed',
    mode: transaction.mode,
    stores: [...(transaction.objectStoreNames ?? [])].join('|'),
    ...errorSummary(transaction.error)
  });
}

/** @param {IDBTransaction} transaction */
function transactionDone(transaction) {
  return new Promise((resolve, reject) => {
    transaction.oncomplete = () => resolve(undefined);
    transaction.onerror = () => {
      reportTransactionFailure(transaction, 'error');
      reject(transaction.error ?? new Error('IndexedDB transaction failed'));
    };
    transaction.onabort = () => {
      reportTransactionFailure(transaction, 'abort');
      reject(transaction.error ?? new Error('IndexedDB transaction aborted'));
    };
  });
}

/**
 * Uses relaxed durability for the reconstructable cache where supported.
 * Safari versions that reject the options argument retain the default path.
 * @param {IDBDatabase} database
 * @param {string | string[]} storeNames
 */
function readwriteTransaction(database, storeNames) {
  storeNames = typeof storeNames === 'string' ? [storeNames] : storeNames;
  if (storeNames.includes('runs')) storeNames = [...new Set([...storeNames, ...DAILY_RUN_COUNT_STORES])];
  try {
    return database.transaction(storeNames, 'readwrite', { durability: 'relaxed' });
  } catch (error) {
    if (!(error instanceof TypeError)) throw error;
    debug('relaxed transaction durability unsupported; using default durability', {
      storeCount: Array.isArray(storeNames) ? storeNames.length : 1
    });
    return database.transaction(storeNames, 'readwrite');
  }
}

/** @param {IDBTransaction} transaction */
function commitTransaction(transaction) {
  if (typeof transaction.commit === 'function') {
    debug('explicitly committing queued IndexedDB requests');
    transaction.commit();
  } else {
    debug('explicit IndexedDB commit unsupported; using automatic commit');
  }
}

/**
 * @param {unknown} existing
 * @param {number} now
 */
function ingestionLockLease(existing, now) {
  if (!existing || typeof existing !== 'object') return { active: false, expiresAt: null };
  const expiresAt = Number(/** @type {{ expiresAt?: unknown }} */ (existing).expiresAt);
  return {
    active: Number.isFinite(expiresAt)
      && expiresAt > now
      && expiresAt <= now + INGESTION_LOCK_LEASE_MS,
    expiresAt: Number.isFinite(expiresAt) ? expiresAt : null
  };
}

/**
 * @param {IDBObjectStore} store
 * @param {string} name
 * @param {string | string[]} keyPath
 */
function createIndex(store, name, keyPath) {
  store.createIndex(name, keyPath);
}

/** @param {IDBDatabase} database @param {boolean} [backfill] @param {string} [coverage] */
function createSchema(database, backfill = false, coverage = 'retained-v44') {
  for (const [storeName, definition] of Object.entries(CANONICAL_DATABASE_SCHEMA)) {
    if (database.objectStoreNames.contains(storeName)) continue;
    const store = database.createObjectStore(storeName, { keyPath: definition.keyPath });
    for (const [indexName, keyPath] of Object.entries(definition.indexes)) {
      createIndex(store, indexName, keyPath);
    }
    if (storeName === 'dailyRunCountState') store.put({
      id: DAILY_RUN_COUNT_STATE_ID, status: backfill ? 'backfilling' : 'ready',
      coverage: backfill ? coverage : 'admitted', excludedRuns: 0, conflictedRuns: 0
    });
  }
}

const DELETE_BLOCKED_TIMEOUT_MS = 3_000;

/**
 * @param {IDBFactory} indexedDB
 * @param {{ blockedTimeoutMs?: number, onBlocked?: () => void }} [options]
 * @returns {Promise<void>}
 */
export function deleteCanonicalDatabase(indexedDB, options = {}) {
  const name = canonicalDatabaseName();
  const blockedTimeoutMs = options.blockedTimeoutMs ?? DELETE_BLOCKED_TIMEOUT_MS;
  debug('deleting database', name);
  const request = indexedDB.deleteDatabase(name);
  return new Promise((resolve, reject) => {
    /** @type {ReturnType<typeof setTimeout> | undefined} */
    let blockedTimeout;
    const settle = (/** @type {() => void} */ action) => {
      if (blockedTimeout !== undefined) clearTimeout(blockedTimeout);
      action();
    };
    request.onsuccess = () => settle(() => {
      debug('deleted database', name);
      resolve();
    });
    request.onerror = () => settle(() => {
      const error = request.error ?? new Error('Unable to delete canonical dashboard data');
      debug('failed to delete database', name, error);
      reject(error);
    });
    request.onblocked = () => {
      // Another open connection (a stale tab, worker, or leaked handle) is
      // still holding the database open. The delete request stays pending
      // and resolves once that connection closes, so give it a bounded grace
      // period instead of hanging indefinitely or failing immediately.
      debug('delete database blocked by an open connection', name);
      options.onBlocked?.();
      if (blockedTimeout === undefined) {
        blockedTimeout = setTimeout(() => {
          debug('delete database still blocked after timeout', name);
          const error = new Error('Deleting canonical dashboard data was blocked by another open tab or connection');
          error.name = 'IndexedDBDeleteBlockedError';
          reject(error);
        }, blockedTimeoutMs);
      }
    };
  });
}

/**
 * Milliseconds between diagnostics reports while an open request has not
 * settled. A healthy open settles in a few milliseconds; a request that stays
 * pending is waiting on a lock held by another connection (a stale tab, a
 * worker that is mid-upgrade) or on a damaged backing store.
 */
const OPEN_PENDING_REPORT_MS = 2_000;
let nextOpenRequestId = 0;
let pendingOpenRequests = 0;

/** @param {unknown} error */
function errorSummary(error) {
  return error instanceof Error || (error && typeof error === 'object' && 'name' in error)
    ? {
        errorName: String(/** @type {{ name?: unknown }} */ (error).name ?? 'Error'),
        errorMessage: String(/** @type {{ message?: unknown }} */ (error).message ?? '')
      }
    : { errorName: 'Unknown', errorMessage: String(error) };
}

/**
 * Reports browser storage state that explains an open request that does not
 * settle: whether other versions of the canonical database exist, and whether
 * the origin is close to its storage quota. Reports only names, versions, and
 * byte counts.
 * @param {IDBFactory} indexedDB
 * @param {string} name
 * @param {number} openId
 */
async function reportPendingOpenDiagnostics(indexedDB, name, openId) {
  try {
    const databases = typeof indexedDB.databases === 'function' ? await indexedDB.databases() : null;
    debug({
      event: 'open-pending-databases',
      openId,
      databasesSupported: databases !== null,
      matching: databases?.filter((database) => database.name === name)
        .map((database) => ({ version: database.version ?? null })) ?? [],
      totalDatabaseCount: databases?.length ?? null
    });
  } catch (error) {
    debug({ event: 'open-pending-databases-failed', openId, ...errorSummary(error) });
  }
  try {
    const storage = globalThis.navigator?.storage;
    const estimate = typeof storage?.estimate === 'function' ? await storage.estimate() : null;
    const persisted = typeof storage?.persisted === 'function' ? await storage.persisted() : null;
    debug({
      event: 'open-pending-storage',
      openId,
      usageBytes: estimate?.usage ?? null,
      quotaBytes: estimate?.quota ?? null,
      persisted
    });
  } catch (error) {
    debug({ event: 'open-pending-storage-failed', openId, ...errorSummary(error) });
  }
}

/**
 * @param {IDBFactory} indexedDB
 * @param {typeof IDBKeyRange} [keyRange]
 * @returns {Promise<IDBDatabase>}
 */
export function openCanonicalDatabase(indexedDB, keyRange = globalThis.IDBKeyRange) {
  const name = canonicalDatabaseName();
  const openId = ++nextOpenRequestId;
  const startedAt = monotonicNow();
  const elapsed = () => Math.round(monotonicNow() - startedAt);
  pendingOpenRequests += 1;
  debug({ event: 'open-requested', openId, version: DATABASE_VERSION, pendingOpenRequests });
  let reports = 0;
  // Reports only while the request is unsettled; debug output is a no-op
  // unless `?debug=` enables the `data:indexeddb` category.
  const pendingReport = setInterval(() => {
    reports += 1;
    debug({ event: 'open-pending', openId, elapsedMs: elapsed(), pendingOpenRequests });
    if (reports === 1) void reportPendingOpenDiagnostics(indexedDB, name, openId);
  }, OPEN_PENDING_REPORT_MS);
  let settled = false;
  const settle = () => {
    if (settled) return;
    settled = true;
    pendingOpenRequests -= 1;
    clearInterval(pendingReport);
  };
  /** @type {IDBOpenDBRequest} */
  let request;
  try {
    request = indexedDB.open(name, DATABASE_VERSION);
  } catch (error) {
    settle();
    debug({ event: 'open-threw', openId, elapsedMs: elapsed(), ...errorSummary(error) });
    return Promise.reject(error);
  }
  return new Promise((resolve, reject) => {
    request.onupgradeneeded = (event) => {
      const database = request.result;
      const upgrade = request.transaction;
      debug({
        event: 'open-upgrade-needed',
        openId,
        oldVersion: event.oldVersion,
        newVersion: event.newVersion,
        existingStoreCount: database.objectStoreNames.length,
        elapsedMs: elapsed()
      });
      if (upgrade) {
        upgrade.addEventListener('abort', () => debug({
          event: 'open-upgrade-aborted', openId, elapsedMs: elapsed(), ...errorSummary(upgrade.error)
        }));
        upgrade.addEventListener('complete', () => debug({
          event: 'open-upgrade-completed', openId, elapsedMs: elapsed()
        }));
      }
      if ((event.oldVersion === 44 || event.oldVersion === 45) && database.objectStoreNames.contains('runs')) {
        createSchema(database, true, `retained-v${event.oldVersion}`);
      } else if (event.oldVersion < DATABASE_VERSION) {
        // Canonical data is a derived cache. Rebuild incompatible identities and
        // schemas from authoritative dashboard inputs instead of migrating them.
        debug('upgrading database schema', name, { from: event.oldVersion, to: DATABASE_VERSION });
        if (event.oldVersion > 0) {
          for (const listener of upgradeListeners) listener({ oldVersion: event.oldVersion, newVersion: DATABASE_VERSION });
        }
        for (const storeName of [...database.objectStoreNames]) database.deleteObjectStore(storeName);
        createSchema(database);
      }
    };
    let blocked = false;
    request.onsuccess = () => {
      settle();
      const database = request.result;
      if (blocked) {
        debug('closing database opened after blocked request settled', name);
        database.close();
        return;
      }
      const missingStores = Object.keys(CANONICAL_DATABASE_SCHEMA)
        .filter((storeName) => !database.objectStoreNames.contains(storeName));
      debug({
        event: missingStores.length > 0 ? 'open-schema-mismatch' : 'open-succeeded',
        openId,
        version: database.version,
        storeCount: database.objectStoreNames.length,
        missingStores,
        elapsedMs: elapsed()
      });
      database.onversionchange = (event) => {
        debug({
          event: 'connection-version-change',
          openId,
          oldVersion: event.oldVersion,
          newVersion: event.newVersion
        });
        debug('closing database for version change', name);
        database.close();
      };
      // `close` fires only when the browser closes the connection abnormally,
      // for example after backing-store corruption or when site data is cleared.
      database.onclose = () => debug({ event: 'connection-closed-abnormally', openId });
      backfillDailyRunCounts(database, keyRange).then(() => resolve(database), (error) => {
        database.close();
        reject(error);
      });
    };
    request.onerror = () => {
      settle();
      const error = request.error ?? new Error('Unable to open canonical dashboard data');
      debug({ event: 'open-failed', openId, elapsedMs: elapsed(), ...errorSummary(error) });
      debug('failed to open database', name, error);
      reject(error);
    };
    request.onblocked = (event) => {
      settle();
      debug({
        event: 'open-blocked',
        openId,
        oldVersion: event.oldVersion,
        newVersion: event.newVersion,
        elapsedMs: elapsed()
      });
      debug('open database blocked by an older connection', name);
      blocked = true;
      reject(new Error('Opening canonical dashboard data was blocked'));
    };
  });
}

/**
 * Upgrade work is cursor-paged and never rewrites a detail record. A v44
 * cache cannot recover already-evicted runs, so its coverage remains partial.
 * @param {IDBDatabase} database
 * @param {typeof IDBKeyRange} [keyRange]
 */
export async function backfillDailyRunCounts(database, keyRange = globalThis.IDBKeyRange) {
  if (!database.objectStoreNames.contains('dailyRunCountState')) return;
  const initial = await requestResult(database.transaction('dailyRunCountState')
    .objectStore('dailyRunCountState').get(DAILY_RUN_COUNT_STATE_ID));
  if (initial?.status !== 'backfilling') return;
  let more = true;
  while (more) {
    const transaction = readwriteTransaction(database, ['runs', ...DAILY_RUN_COUNT_STORES]);
    const done = transactionDone(transaction);
    try {
      const states = transaction.objectStore('dailyRunCountState');
      const state = await requestResult(states.get(DAILY_RUN_COUNT_STATE_ID));
      if (state?.status !== 'backfilling') {
        await done;
        return;
      }
      /** @type {Record<string, unknown>[]} */
      const records = [];
      const runs = transaction.objectStore('runs');
      const range = state.after === undefined ? undefined : keyRange.lowerBound(state.after, true);
      if (typeof runs.openCursor !== 'function') {
        records.push(...await requestResult(runs.getAll(range, 250)));
        more = records.length === 250;
      } else await new Promise((resolve, reject) => {
        const request = runs.openCursor(range);
        request.onerror = () => reject(request.error);
        request.onsuccess = () => {
          const cursor = request.result;
          if (!cursor || records.length === 250) {
            more = Boolean(cursor);
            resolve(undefined);
          } else {
            records.push(cursor.value);
            cursor.continue();
          }
        };
      });
      await reconcileDailyRunCountsBatch(transaction, records);
      const latest = await requestResult(states.get(DAILY_RUN_COUNT_STATE_ID));
      states.put({
        ...latest, after: records.at(-1)?.id ?? state.after,
        status: more ? 'backfilling' : 'ready'
      });
      commitTransaction(transaction);
      await done;
    } catch (error) {
      try { transaction.abort(); } catch { /* Already completed. */ }
      await done.catch(() => undefined);
      throw error;
    }
  }
}

/**
 * Upserts a canonical batch onto an already-open database connection using
 * bounded transactions. Callers that write many small batches in sequence
 * (such as streamed JSONL ingestion) should open the connection once with
 * {@link openCanonicalDatabase} and reuse it across calls: repeatedly
 * opening and closing a connection for every batch adds IPC/versioning
 * overhead per call that dominates ingestion time once batch counts grow
 * into the hundreds.
 *
 * @param {IDBDatabase} database
 * @param {import('../model/schema.js').CanonicalBatch} batch
 * @param {{ batchSize?: number, validateRelationships?: boolean, signal?: AbortSignal, onBatchCommitted?: (progress: { committedBatches: number, committedRecords: number }) => void | Promise<void> }} [options]
 */
export async function upsertCanonicalBatchWithConnection(database, batch, options = {}) {
  options.signal?.throwIfAborted();
  if (options.validateRelationships !== false) {
    const errors = relationshipErrors(batch);
    if (errors.length > 0) {
      throw new Error(`Canonical relationship validation failed: ${errors.join('; ')}`);
    }

  }

  const batchSize = options.batchSize ?? DEFAULT_WRITE_BATCH_SIZE;
  if (!Number.isInteger(batchSize) || batchSize < 1) {
    throw new TypeError('Write batch size must be a positive integer');
  }

  let committedRecords = 0;
  let committedBatches = 0;
  for (const storeName of ENTITY_STORES) {
    options.signal?.throwIfAborted();
    const records = batch[storeName] ?? [];
    for (let offset = 0; offset < records.length; offset += batchSize) {
      options.signal?.throwIfAborted();
      const boundedRecords = records.slice(offset, offset + batchSize)
        .map((record) => pruneCanonicalRecord(storeName, record));
      const transaction = readwriteTransaction(database, [storeName, STORAGE_SHARD_STORE]);
      const done = transactionDone(transaction);
      try {
        await writeAccountedRecords(transaction, storeName,
          boundedRecords.map((record) => prepareCanonicalRecord(storeName, record)));
        commitTransaction(transaction);
        await done;
      } catch (error) {
        try { transaction.abort(); } catch { /* Already aborted or completed. */ }
        await done.catch(() => undefined);
        throw error;
      }
      committedRecords += boundedRecords.length;
      committedBatches += 1;
      await options.onBatchCommitted?.({ committedBatches, committedRecords });
    }
  }
  return { committedBatches, committedRecords };
}

/**
 * Directly upserts a canonical batch using bounded transactions, opening and
 * closing a database connection for this call only. Prefer
 * {@link upsertCanonicalBatchWithConnection} when writing many batches in a
 * loop so the connection can be reused.
 *
 * @param {IDBFactory} indexedDB
 * @param {import('../model/schema.js').CanonicalBatch} batch
 * @param {{ batchSize?: number, validateRelationships?: boolean, signal?: AbortSignal, onBatchCommitted?: (progress: { committedBatches: number, committedRecords: number }) => void | Promise<void> }} [options]
 */
export async function upsertCanonicalBatch(indexedDB, batch, options = {}) {
  const database = await openCanonicalDatabase(indexedDB);
  try {
    return await upsertCanonicalBatchWithConnection(database, batch, options);
  } finally {
    database.close();
  }
}

/**
 * Admits count facts whose detail is intentionally outside its retention window.
 * This never resurrects expired detail or modifies its retention timestamps.
 * @param {IDBDatabase} database
 * @param {Record<string, unknown>[]} records
 */
export async function admitExpiredRunCountsWithConnection(database, records) {
  for (let offset = 0; offset < records.length; offset += 250) {
    const transaction = readwriteTransaction(database, [...DAILY_RUN_COUNT_STORES]);
    const done = transactionDone(transaction);
    try {
      await reconcileDailyRunCountsBatch(transaction, records.slice(offset, offset + 250));
      commitTransaction(transaction);
      await done;
    } catch (error) {
      try { transaction.abort(); } catch { /* Already completed. */ }
      await done.catch(() => undefined);
      throw error;
    }
  }
}

/** @param {IDBFactory} indexedDB @param {Record<string, unknown>[]} records */
export async function admitExpiredRunCounts(indexedDB, records) {
  if (records.length === 0) return;
  const database = await openCanonicalDatabase(indexedDB);
  try {
    await admitExpiredRunCountsWithConnection(database, records);
  } finally {
    database.close();
  }
}

/**
 * Applies retention and size limits using indexed shard cleanup and bounded Run pages.
 * Full evidence scans are reserved for explicit relationship or legacy audit repair.
 *
 * @param {IDBFactory} indexedDB
 * @param {{ now?: number, retentionWindowMs?: number, retentionWindowMsByStore?: Record<string, number>, maxDatabaseBytes: number, usageBytes?: number | null, reconcileRelationships?: boolean, repairAuditCuration?: boolean, preserveEntityIds?: { repositories?: string[], workflows?: string[] }, onMaintenanceProgress?: (completed: number, total: number) => void, signal?: AbortSignal }} options
 */
export async function maintainCanonicalDatabase(indexedDB, options) {
  if (options.reconcileRelationships || options.repairAuditCuration) return reconcileCanonicalDatabase(indexedDB, options);
  options.signal?.throwIfAborted();
  const database = await openCanonicalDatabase(indexedDB);
  try {
    return await maintainStorageShards(database, {
      ...options, entityStores: ENTITY_STORES, runLinkedStores: RUN_LINKED_STORES
    });
  } finally {
    database.close();
  }
}

/**
 * Explicit relationship or legacy audit repair; never used for routine ingestion maintenance.
 * @param {IDBFactory} indexedDB
 * @param {Parameters<typeof maintainCanonicalDatabase>[1]} options
 */
async function reconcileCanonicalDatabase(indexedDB, options) {
    options.signal?.throwIfAborted();
    const now = options.now ?? Date.now();
    const defaultWindow = Number.isFinite(options.retentionWindowMs)
      ? Math.max(0, Number(options.retentionWindowMs))
      : 30 * 24 * 60 * 60 * 1000;
    let deletedRecords = 0;
    let prunedAudits = 0;
    /** @type {Map<string, Record<string, unknown>>} */
    const auditRunFacts = new Map();
    /** @type {string[]} */
    const expiredRunIds = [];
    const campaignIds = new Set();
    const repositoryIds = new Set();
    /** @type {Map<string, string>} */
    const workflowRepositories = new Map();
    const validRunIds = new Set();
    const referencedWorkflowIds = new Set();
    const referencedRepositoryIds = new Set();
    const database = await openCanonicalDatabase(indexedDB);
    try {
      await evictDailyRunCounts(database, now, options.signal);
      const referencedAudits = new Set();
      for (const storeName of ['graderObservations', 'evalObservations', 'experimentAssignments']) {
        options.signal?.throwIfAborted();
        const transaction = database.transaction(storeName);
        const done = transactionDone(transaction);
        const store = transaction.objectStore(storeName);
        if (typeof store.openCursor !== 'function') {
          for (const record of await requestResult(store.getAll())) {
            if (typeof record.auditId === 'string') referencedAudits.add(record.auditId);
          }
        } else {
          const request = store.openCursor();
          request.onsuccess = () => {
            if (options.signal?.aborted) {
              transaction.abort();
              return;
            }
            const cursor = request.result;
            if (!cursor) return;
            if (typeof cursor.value.auditId === 'string') referencedAudits.add(cursor.value.auditId);
            cursor.continue();
          };
        }
        await done;
      }
      for (const storeName of ENTITY_STORES) {
        options.signal?.throwIfAborted();
        const transaction = readwriteTransaction(database, storeName);
        const done = transactionDone(transaction);
        const store = transaction.objectStore(storeName);
        /** @param {Record<string, unknown>} record @param {() => void} remove */
        const visit = (record, remove) => {
          const id = String(record.id);
          if (storeName === 'runs') auditRunFacts.set(id, auditCurationRunFacts(record));
          if (storeName === 'audits' && !referencedAudits.has(id)
              && discardAudit(canonicalRecord({ ...record }), auditRunFacts.get(String(record.runId)))) {
            remove();
            deletedRecords += 1;
            prunedAudits += 1;
            return;
          }
          if (options.reconcileRelationships) {
            if (storeName === 'campaigns') {
              campaignIds.add(id);
            } else if (storeName === 'repositories') {
              repositoryIds.add(id);
            } else if (storeName === 'workflows') {
              const repositoryId = String(record.repositoryId);
              const campaignId = record.campaignId === undefined || record.campaignId === null
                ? null
                : String(record.campaignId);
              if (!repositoryIds.has(repositoryId) || (campaignId !== null && !campaignIds.has(campaignId))) {
                remove();
                deletedRecords += 1;
                return;
              }
              workflowRepositories.set(id, repositoryId);
            } else if (storeName === 'runs') {
              const repositoryId = String(record.repositoryId);
              const workflowId = String(record.workflowId);
              if (!repositoryIds.has(repositoryId) || workflowRepositories.get(workflowId) !== repositoryId) {
                expiredRunIds.push(id);
                return;
              }
              validRunIds.add(id);
              referencedWorkflowIds.add(workflowId);
              referencedRepositoryIds.add(repositoryId);
            } else if (storeName === 'operationalValueEvidence' || storeName === 'workflowValueCollections') {
              const repositoryId = String(record.repositoryId);
              const workflowId = String(record.workflowId);
              if (!repositoryIds.has(repositoryId) || workflowRepositories.get(workflowId) !== repositoryId) {
                remove();
                deletedRecords += 1;
                return;
              }
              referencedWorkflowIds.add(workflowId);
              referencedRepositoryIds.add(repositoryId);
            } else if (['experiments', 'graders', 'evals'].includes(storeName)
              && !workflowRepositories.has(String(record.workflowId))) {
              remove();
              deletedRecords += 1;
              return;
            } else if (RUN_LINKED_STORES.includes(
              /** @type {typeof RUN_LINKED_STORES[number]} */ (storeName)
            ) && !validRunIds.has(String(record.runId))) {
              remove();
              deletedRecords += 1;
              return;
            }
          }
          const configuredWindow = options.retentionWindowMsByStore?.[storeName]
            ?? (Number.isFinite(options.retentionWindowMs)
              ? undefined : BROWSER_RETENTION_WINDOWS_MS[/** @type {keyof typeof BROWSER_RETENTION_WINDOWS_MS} */ (storeName)]);
          const windowMs = Number.isFinite(configuredWindow)
            ? Math.max(0, Number(configuredWindow))
            : defaultWindow;
          const timestamp = recordTimestamp(storeName, record);
          if (RETENTION_TIMESTAMPS.has(storeName)
              && (timestamp === null || timestamp < now - windowMs)) {
            if (storeName === 'runs') {
              expiredRunIds.push(String(record.id));
            } else {
              remove();
              deletedRecords += 1;
            }
            return;
          }
        };
        if (typeof store.openCursor !== 'function') {
          for (const record of await requestResult(store.getAll())) {
            visit(record, () => store.delete(record.id));
          }
        } else {
          await new Promise((resolve, reject) => {
            /** @type {IDBValidKey[]} */
            const pendingAuditDeletes = [];
            const flushAuditDeletes = () => {
              for (const key of pendingAuditDeletes) store.delete(key);
              pendingAuditDeletes.length = 0;
            };
            const cursorRequest = store.openCursor();
            cursorRequest.onerror = () => reject(cursorRequest.error ?? new Error('IndexedDB cursor failed'));
            cursorRequest.onsuccess = () => {
              if (options.signal?.aborted) {
                transaction.abort();
                return;
              }
              const cursor = cursorRequest.result;
              if (!cursor) {
                flushAuditDeletes();
                resolve(undefined);
                return;
              }
              visit(cursor.value, () => {
                if (storeName === 'audits') pendingAuditDeletes.push(cursor.primaryKey);
                else cursor.delete();
              });
              cursor.continue();
              // Per-row deletes invalidate Chromium's cursor prefetch cache.
              if (pendingAuditDeletes.length >= DEFAULT_WRITE_BATCH_SIZE) flushAuditDeletes();
            };
          });
        }
        await done;
        options.onMaintenanceProgress?.(ENTITY_STORES.indexOf(storeName) + 1, ENTITY_STORES.length);
      }

      if (options.reconcileRelationships) {
        const preservedWorkflows = new Set(options.preserveEntityIds?.workflows ?? []);
        const survivingWorkflows = new Set([...referencedWorkflowIds, ...preservedWorkflows]);
        for (const workflowId of survivingWorkflows) {
          const repositoryId = workflowRepositories.get(workflowId);
          if (repositoryId) referencedRepositoryIds.add(repositoryId);
        }
        const parentStores = [
          ['workflows', survivingWorkflows],
          ['repositories', new Set([
            ...referencedRepositoryIds,
            ...(options.preserveEntityIds?.repositories ?? [])
          ])]
        ];
        for (const [storeName, retainedIds] of parentStores) {
          const transaction = readwriteTransaction(database, /** @type {string} */ (storeName));
          const done = transactionDone(transaction);
          const store = transaction.objectStore(/** @type {string} */ (storeName));
          /** @param {IDBValidKey} id @param {() => void} remove */
          const removeUnreferenced = (id, remove) => {
            if (/** @type {Set<string>} */ (retainedIds).has(String(id))) return;
            remove();
            deletedRecords += 1;
          };
          if (typeof store.openCursor !== 'function') {
            for (const record of await requestResult(store.getAll())) {
              removeUnreferenced(record.id, () => store.delete(record.id));
            }
          } else {
            await new Promise((resolve, reject) => {
              const request = store.openCursor();
              request.onerror = () => reject(request.error ?? new Error('IndexedDB cursor failed'));
              request.onsuccess = () => {
                const cursor = request.result;
                if (!cursor) {
                  resolve(undefined);
                  return;
                }
                removeUnreferenced(cursor.primaryKey, () => cursor.delete());
                cursor.continue();
              };
            });
          }
          await done;
        }
      }

      for (let offset = 0; offset < expiredRunIds.length; offset += DEFAULT_WRITE_BATCH_SIZE) {
        const ids = expiredRunIds.slice(offset, offset + DEFAULT_WRITE_BATCH_SIZE);
        const transaction = readwriteTransaction(database, ['runs', ...RUN_LINKED_STORES]);
        const done = transactionDone(transaction);
        const runStore = transaction.objectStore('runs');
        for (const id of ids) {
          runStore.delete(id);
          deletedRecords += 1;
        }
        const idSet = new Set(ids);
        for (const storeName of RUN_LINKED_STORES) {
          const store = transaction.objectStore(storeName);
          const index = store.index('byRun');
          if (typeof index.openCursor !== 'function') {
            const records = await requestResult(store.getAll());
            for (const record of records) {
              if (!idSet.has(String(record.runId))) continue;
              store.delete(record.id);
              deletedRecords += 1;
            }
            continue;
          }
          for (const id of ids) {
            const request = index.openCursor(id);
            request.onsuccess = () => {
              const cursor = request.result;
              if (!cursor) return;
              cursor.delete();
              deletedRecords += 1;
              cursor.continue();
            };
          }
        }
        await done;
      }

      options.signal?.throwIfAborted();
      const receipt = readwriteTransaction(database, TRANSACTION_STORE);
      const receiptDone = transactionDone(receipt);
      receipt.objectStore(TRANSACTION_STORE).put({
        id: AUDIT_CURATION_TRANSACTION_ID,
        kind: 'audit-curation',
        version: AUDIT_CURATION_VERSION,
        createdAt: new Date(now).toISOString()
      });
      await receiptDone;
      await rebuildStorageShards(database, ENTITY_STORES);
      const maintenance = await maintainStorageShards(database, {
        ...options, entityStores: ENTITY_STORES, runLinkedStores: RUN_LINKED_STORES
      });
      debug('curated canonical audits', { prunedAudits, version: AUDIT_CURATION_VERSION });
      return {
        ...maintenance,
        deletedRecords: deletedRecords + maintenance.deletedRecords,
        prunedAudits: prunedAudits + maintenance.prunedAudits
      };
    } finally {
      database.close();
    }
}

/**
 * @param {IDBFactory} indexedDB
 * @returns {Promise<import('../model/schema.js').CanonicalBatch>}
 */
export async function readCanonicalBatch(indexedDB) {
  return /** @type {import('../model/schema.js').CanonicalBatch} */ (
    await readCollections(indexedDB, ENTITY_STORES)
  );
}

/**
 * Reads multiple stores through one connection and one readonly transaction.
 * @param {IDBFactory} indexedDB
 * @param {readonly typeof DATABASE_STORES[number][]} storeNames
 * @param {{ bounds?: { stores: ReadonlySet<string>, rows: number, bytes: number, onLimit: () => void }, checkpoint?: () => void }} [options]
 */
export async function readCollections(indexedDB, storeNames, options = {}) {
  const startedAt = monotonicNow();
  const database = await openCanonicalDatabase(indexedDB);
  try {
    const records = storeNames.length === 0
      ? []
      : await (async () => {
        const transaction = database.transaction([...storeNames]);
        const done = transactionDone(transaction);
        let bytes = 0;
        const encoder = new TextEncoder();
        const values = storeNames.map((storeName) => {
          const store = transaction.objectStore(storeName);
          const bounds = options.bounds;
          if (!bounds?.stores.has(storeName)) return requestResult(store.getAll());
          /** @type {Promise<Record<string, unknown>[]>} */
          const result = new Promise((resolve, reject) => {
            /** @type {Record<string, unknown>[]} */
            const rows = [];
            /** @param {IDBKeyRange | undefined} range */
            const read = (range) => {
              const batchSize = Math.min(1000, bounds.rows - rows.length + 1);
              const request = store.getAll(range, batchSize);
              request.onerror = () => reject(request.error);
              request.onsuccess = () => {
                try {
                  options.checkpoint?.();
                  for (const record of request.result) {
                    const row = canonicalRecord(record);
                    bytes += encoder.encode(JSON.stringify(row)).byteLength;
                    if (rows.length >= bounds.rows || bytes > bounds.bytes) {
                      bounds.onLimit();
                      resolve([]);
                      return;
                    }
                    rows.push(row);
                  }
                  if (request.result.length === batchSize) {
                    const last = request.result[request.result.length - 1];
                    read(IDBKeyRange.lowerBound(last[String(store.keyPath)], true));
                  } else resolve(rows);
                } catch (error) {
                  reject(error);
                  try {
                    transaction.abort();
                  } catch (abortError) {
                    debug({ event: 'bounded-read-abort-failed', ...errorSummary(abortError) });
                  }
                }
              };
            };
            read(undefined);
          });
          return result;
        });
        const [records] = await Promise.all([Promise.all(values), done]);
        return records;
      })();
    debug('completed multi-store collection read', {
      storeCount: storeNames.length,
      requestCount: storeNames.length,
      stores: [...storeNames].join('|'),
      durationMs: monotonicNow() - startedAt
    });
    return Object.fromEntries(storeNames.map((storeName, index) => [
      storeName, records[index].map((record) => canonicalRecord(record))
    ]));
  } finally {
    database.close();
  }
}

/**
 * Counts multiple stores through one connection and one readonly transaction.
 * @param {IDBFactory} indexedDB
 * @param {readonly typeof DATABASE_STORES[number][]} storeNames
 */
export async function countCollections(indexedDB, storeNames) {
  const startedAt = monotonicNow();
  const database = await openCanonicalDatabase(indexedDB);
  try {
    const counts = storeNames.length === 0
      ? []
      : await (async () => {
          const transaction = database.transaction([...storeNames]);
          const done = transactionDone(transaction);
          const values = await Promise.all(storeNames.map((storeName) =>
            requestResult(transaction.objectStore(storeName).count())
          ));
          await done;
          return values;
        })();
    debug('completed multi-store collection count', {
      storeCount: storeNames.length,
      requestCount: storeNames.length,
      stores: [...storeNames].join('|'),
      durationMs: monotonicNow() - startedAt
    });
    return Object.fromEntries(storeNames.map((storeName, index) => [storeName, counts[index]]));
  } finally {
    database.close();
  }
}

/**
 * @param {IDBFactory} indexedDB
 * @param {typeof ENTITY_STORES[number]} storeName
 */
export async function readCollection(indexedDB, storeName) {
  const database = await openCanonicalDatabase(indexedDB);
  try {
    return (await requestResult(database.transaction(storeName).objectStore(storeName).getAll()))
      .map((record) => canonicalRecord(record));
  } finally {
    database.close();
  }
}

/**
 * Reads a canonical collection through an opportunistically compiled
 * IndexedDB access plan, then applies the complete operator pipeline in
 * JavaScript to preserve the declarative query semantics.
 *
 * @param {IDBFactory} indexedDB
 * @param {typeof ENTITY_STORES[number]} storeName
 * @param {import('../../data-operations.js').DataOperator[]} operators
 * @param {{ maxRows?: number, maxBytes?: number, keyFilter?: (key: IDBValidKey) => boolean, checkpoint?: () => void, onMetrics?: (metrics: { durationMs: number, requestCount: number, recordsScanned: number, recordsReturned: number, index: string | null }) => void }} [options]
 */
export async function queryCollection(indexedDB, storeName, operators, options = {}) {
  const startedAt = monotonicNow();
  const database = await openCanonicalDatabase(indexedDB);
  try {
    const transaction = database.transaction(storeName);
    const store = transaction.objectStore(storeName);
    const plan = indexedQueryPlan(store, operators);
    if (options.maxRows !== undefined) {
      const counts = await Promise.all(plan
        ? plan.keys.map((key) => requestResult(plan.index.count(key)))
        : [requestResult(store.count())]);
      if (counts.reduce((sum, count) => sum + count, 0) > options.maxRows) {
        throw new Error(`IndexedDB selection exceeded max-input-rows of ${options.maxRows}`);
      }
    }
    /** @type {Record<string, any>[]} */
    let records;
    if (options.maxBytes !== undefined) {
      const selected = new Map();
      let bytes = 0;
      for (const range of plan?.keys ?? [undefined]) {
        options.checkpoint?.();
        const keys = await requestResult((plan?.index ?? store).getAllKeys(range));
        for (const key of keys) {
          options.checkpoint?.();
          if (options.keyFilter && !options.keyFilter(key)) continue;
          if (selected.has(key)) continue;
          const row = await requestResult(store.get(key));
          bytes += new TextEncoder().encode(JSON.stringify(row)).length;
          if (bytes > options.maxBytes || (options.maxRows !== undefined && selected.size >= options.maxRows)) {
            throw new Error('Canonical selection exceeded bounded input budget');
          }
          selected.set(key, row);
        }
      }
      records = [...selected.values()];
      records.sort((left, right) => indexedDB.cmp(left[String(store.keyPath)], right[String(store.keyPath)]));
    } else if (!plan) {
      records = await requestResult(store.getAll());
    } else {
      const matches = await Promise.all(plan.keys.map((key) => requestResult(plan.index.getAll(key))));
      const keyPath = String(store.keyPath);
      records = [...new Map(matches.flat().map((record) => [record[keyPath], record])).values()]
        .sort((left, right) => indexedDB.cmp(left[keyPath], right[keyPath]));
      debug('compiled collection query', {
        store: storeName,
        index: plan.index.name,
        lookups: plan.keys.length,
        candidateRecords: records.length
      });
    }
    const result = tidy(records.map((record) => canonicalRecord(record)), operators);
    const metrics = {
      durationMs: monotonicNow() - startedAt,
      requestCount: plan?.keys.length ?? 1,
      recordsScanned: records.length,
      recordsReturned: result.length,
      index: plan?.index.name ?? null
    };
    debug('completed collection query', { store: storeName, ...metrics });
    options.onMetrics?.(metrics);
    return result;
  } finally {
    database.close();
  }
}

/**
 * Visits bounded batches in primary-key order within one readonly snapshot.
 * The synchronous visitor keeps projection and reduction inside the transaction.
 * @param {IDBFactory} indexedDB
 * @param {typeof ENTITY_STORES[number]} storeName
 * @param {(rows: Record<string, unknown>[]) => void} visit
 * @param {{ batchSize?: number, checkpoint?: () => void }} [options]
 */
export async function scanCanonicalCollection(indexedDB, storeName, visit, options = {}) {
  const batchSize = options.batchSize ?? 1000;
  if (!Number.isSafeInteger(batchSize) || batchSize < 1 || batchSize > 1000) {
    throw new TypeError('Canonical scan batchSize must be between 1 and 1000');
  }
  const database = await openCanonicalDatabase(indexedDB);
  try {
    await new Promise((resolve, reject) => {
      const transaction = database.transaction(storeName);
      const store = transaction.objectStore(storeName);
      transaction.onerror = () => reject(transaction.error);
      transaction.onabort = () => reject(transaction.error ?? new Error('Canonical scan aborted'));
      transaction.oncomplete = () => resolve(undefined);
      /** @param {IDBKeyRange | undefined} range */
      const read = (range) => {
        const request = store.getAll(range, batchSize);
        request.onerror = () => reject(request.error);
        request.onsuccess = () => {
          try {
            options.checkpoint?.();
            const records = request.result;
            visit(records.map((record) => canonicalRecord(record)));
            if (records.length === batchSize) {
              const lastKey = records[records.length - 1][String(store.keyPath)];
              read(IDBKeyRange.lowerBound(lastKey, true));
            }
          } catch (error) {
            reject(error);
            try {
              transaction.abort();
            } catch (abortError) {
              debug({ event: 'scan-abort-failed', store: storeName, ...errorSummary(abortError) });
            }
          }
        };
      };
      read(undefined);
    });
  } finally {
    database.close();
  }
}

/**
 * Counts distinct index keys without reading their event records. A nullable
 * trailing dimension is recovered by subtracting its indexed counts from the
 * prefix counts: IndexedDB omits null and absent keys, whereas query grouping
 * places both in the same null bucket.
 *
 * @param {IDBFactory} indexedDB
 * @param {typeof ENTITY_STORES[number]} storeName
 * @param {{ index: string, nullableIndex?: string, predicates?: import('../../data-operations.js').Predicate[], maxGroups: number, checkpoint?: () => void }} plan
 * @returns {Promise<Record<string, unknown>[] | null>}
 */
export async function queryCollectionCountGroups(indexedDB, storeName, plan) {
  const startedAt = monotonicNow();
  const database = await openCanonicalDatabase(indexedDB);
  try {
    const transaction = database.transaction(storeName);
    const done = transactionDone(transaction);
    const store = transaction.objectStore(storeName);
    const base = store.index(plan.index);
    if (typeof base.openKeyCursor !== 'function') { await done; return null; }
    // A missing run relationship cannot be silently dropped by an index.
    const [total, indexed, emptyId] = await Promise.all([
      requestResult(store.count()), requestResult(store.index('byRun').count()), requestResult(store.getKey(''))
    ]);
    if (total !== indexed || emptyId !== undefined) {
      await done;
      return null;
    }

    /** @type {Record<string, unknown>[]} */
    const groups = [];
    let requestCount = 3;
    let recordsScanned = 0;
    /**
     * Finish the unique-key scan before counting so other requests do not
     * invalidate cursor prefetch. Keep count requests bounded and the same
     * readonly transaction alive throughout.
     * @param {IDBIndex} index
     * @param {(key: IDBValidKey[], count: number, firstKey: IDBValidKey) => void} receive
     */
    const countKeys = (index, receive) => new Promise((resolve, reject) => {
      const fields = Array.isArray(index.keyPath) ? index.keyPath : [index.keyPath];
      /** @type {{ key: IDBValidKey, values: IDBValidKey[], firstKey: IDBValidKey }[]} */
      const keys = [];
      let nextKey = 0;
      let remaining = 0;
      const queueCount = () => {
        const group = keys[nextKey++];
        const count = index.count(group.key);
        requestCount += 1;
        remaining += 1;
        count.onerror = () => reject(count.error ?? new Error('IndexedDB group count failed'));
        count.onsuccess = () => {
          try {
            plan.checkpoint?.();
            receive(group.values, count.result, group.firstKey);
            remaining -= 1;
            if (nextKey < keys.length) queueCount();
            else if (remaining === 0) resolve(undefined);
          } catch (error) {
            reject(error);
          }
        };
      };
      const cursorRequest = index.openKeyCursor(null, 'nextunique');
      requestCount += 1;
      cursorRequest.onerror = () => reject(cursorRequest.error ?? new Error('IndexedDB count cursor failed'));
      cursorRequest.onsuccess = () => {
        try { plan.checkpoint?.(); } catch (error) { reject(error); return; }
        const cursor = cursorRequest.result;
        if (!cursor) {
          if (keys.length === 0) resolve(undefined);
          else {
            try {
              for (let i = 0; i < Math.min(keys.length, 32); i += 1) queueCount();
            } catch (error) {
              reject(error);
            }
          }
          return;
        }
        const key = Array.isArray(cursor.key) ? cursor.key : [cursor.key];
        const row = Object.fromEntries(fields.map((field, i) => [field, key[i]]));
        if (plan.predicates?.length && tidy([row], [{ op: 'filter', predicates: plan.predicates }]).length === 0) {
          cursor.continue();
          return;
        }
        if (keys.length >= plan.maxGroups) {
          reject(new Error(`IndexedDB aggregate exceeded max-input-rows of ${plan.maxGroups}`));
          return;
        }
        keys.push({ key: cursor.key, values: key, firstKey: cursor.primaryKey });
        cursor.continue();
      };
    });
    const fields = Array.isArray(base.keyPath) ? base.keyPath : [base.keyPath];
    /** @type {Map<string, { key: IDBValidKey[], count: number }>} */
    const prefixes = new Map();
    const append = (/** @type {Record<string, unknown>} */ row) => {
      if (groups.length + recordsScanned >= plan.maxGroups) throw new Error(`IndexedDB aggregate exceeded max-input-rows of ${plan.maxGroups}`);
      groups.push(row);
    };
    try {
      await countKeys(base, (key, count, firstKey) => {
        if (plan.nullableIndex) {
          if (prefixes.size >= plan.maxGroups) throw new Error(`IndexedDB aggregate exceeded max-input-rows of ${plan.maxGroups}`);
          prefixes.set(JSON.stringify(key), { key, count });
        } else {
          append({ ...Object.fromEntries(fields.map((field, i) => [field, key[i]])), id: firstKey, count });
        }
      });
      if (plan.nullableIndex) {
        const nullable = store.index(plan.nullableIndex);
        const nullableFields = Array.isArray(nullable.keyPath) ? nullable.keyPath : [nullable.keyPath];
        await countKeys(nullable, (key, count, firstKey) => {
          const prefix = prefixes.get(JSON.stringify(key.slice(0, fields.length)));
          if (!prefix) throw new Error('IndexedDB aggregate has an inconsistent prefix count');
          prefix.count -= count;
          append({ ...Object.fromEntries(nullableFields.map((field, i) => [field, key[i]])), id: firstKey, count });
        });
        for (const { key, count } of prefixes.values()) {
          if (count < 0) throw new Error('IndexedDB aggregate has a negative missing-key count');
          if (count === 0) continue;
          const missingField = nullableFields[nullableFields.length - 1];
          // Read only the earliest missing-key representative, preserving
          // explicit null versus absent values and canonical input ordering.
          const representative = await new Promise((resolve, reject) => {
            const request = base.openCursor(Array.isArray(base.keyPath) ? key : key[0]);
            requestCount += 1;
            request.onerror = () => reject(request.error ?? new Error('IndexedDB missing-key lookup failed'));
            request.onsuccess = () => {
              try { plan.checkpoint?.(); } catch (error) { reject(error); return; }
              const cursor = request.result;
              if (!cursor) {
                reject(new Error('IndexedDB aggregate has no missing-key representative'));
                return;
              }
              recordsScanned += 1;
              if (recordsScanned + groups.length > plan.maxGroups) {
                reject(new Error(`IndexedDB aggregate exceeded max-input-rows of ${plan.maxGroups}`));
                return;
              }
              if (cursor.value[missingField] == null) {
                resolve({ id: cursor.primaryKey, value: cursor.value[missingField] });
              } else {
                cursor.continue();
              }
            };
          });
          append({
            ...Object.fromEntries(fields.map((field, i) => [field, key[i]])),
            [missingField]: representative.value,
            id: representative.id,
            count
          });
        }
      }
      await done;
    } catch (error) {
      try { transaction.abort(); } catch { /* Already completed or aborted. */ }
      await done.catch(() => undefined);
      throw error;
    }
    debug('completed indexed collection aggregate', {
      store: storeName, index: base.name, executionPath: 'indexed-count',
      durationMs: monotonicNow() - startedAt, requestCount,
      recordsScanned, recordsReturned: groups.length
    });
    return groups.sort((left, right) => indexedDB.cmp(
      /** @type {IDBValidKey} */ (left.id), /** @type {IDBValidKey} */ (right.id)
    ));
  } finally {
    database.close();
  }
}

/**
 * Reads a bounded dictionary of nullable raw-field tuples, not event rows.
 * @param {IDBFactory} indexedDB
 * @param {typeof ENTITY_STORES[number]} storeName
 * @param {string} indexName
 * @param {{ maxKeys: number, checkpoint?: () => void, exactKey?: string }} options
 * @returns {Promise<Array<{ key: string, row: Record<string, unknown> }> | null>}
 */
export async function readCollectionQueryKeys(indexedDB, storeName, indexName, options) {
  const database = await openCanonicalDatabase(indexedDB);
  try {
    const transaction = database.transaction(storeName);
    const done = transactionDone(transaction);
    const store = transaction.objectStore(storeName);
    const index = store.index(indexName);
    if (typeof index.openKeyCursor !== 'function') { await done; return null; }
    const [total, indexed] = await Promise.all([requestResult(store.count()), requestResult(index.count())]);
    if (total !== indexed) {
      await done;
      return null;
    }
    const fields = CANONICAL_QUERY_INDEX_FIELDS[indexName];
    if (options.exactKey !== undefined && fields.length === 1) {
      // JSON string tuples sort before every non-string tuple. Check the tail
      // without scanning the string dictionary, retaining coercion semantics.
      const nonString = await requestResult(index.openKeyCursor(IDBKeyRange.lowerBound('[#')));
      options.checkpoint?.();
      if (!nonString) {
        await done;
        if (options.maxKeys < 1) throw new Error(`IndexedDB query keys exceeded max-input-rows of ${options.maxKeys}`);
        const [value] = JSON.parse(options.exactKey);
        return [{ key: options.exactKey, row: { [fields[0]]: value } }];
      }
    }
    /** @type {Array<{ key: string, row: Record<string, unknown> }>} */
    const rows = [];
    try {
      await new Promise((resolve, reject) => {
        const request = index.openKeyCursor(null, 'nextunique');
        request.onerror = () => reject(request.error ?? new Error('IndexedDB query-key read failed'));
        request.onsuccess = () => {
          try {
            options.checkpoint?.();
            const cursor = request.result;
            if (!cursor) { resolve(undefined); return; }
            if (rows.length >= options.maxKeys) throw new Error(`IndexedDB query keys exceeded max-input-rows of ${options.maxKeys}`);
            const key = String(cursor.key);
            const values = JSON.parse(key);
            if (!Array.isArray(values) || values.length !== fields.length) throw new Error('Invalid canonical query index key');
            rows.push({ key, row: Object.fromEntries(fields.map((field, i) => [field, values[i]])) });
            cursor.continue();
          } catch (error) { reject(error); }
        };
      });
      await done;
    } catch (error) {
      try { transaction.abort(); } catch { /* Already completed or aborted. */ }
      await done.catch(() => undefined);
      throw error;
    }
    return rows;
  } finally {
    database.close();
  }
}

/**
 * Materializes only selected tuple keys, after checking their native counts.
 * @param {IDBFactory} indexedDB
 * @param {typeof ENTITY_STORES[number]} storeName
 * @param {string} indexName
 * @param {string[]} keys
 * @param {{ maxRows: number, checkpoint?: () => void }} options
 * @returns {Promise<Record<string, unknown>[]>}
 */
export async function readCollectionQueryKeyRecords(indexedDB, storeName, indexName, keys, options) {
  const database = await openCanonicalDatabase(indexedDB);
  try {
    const index = database.transaction(storeName).objectStore(storeName).index(indexName);
    const unique = [...new Set(keys)];
    const counts = await Promise.all(unique.map((key) => requestResult(index.count(key))));
    options.checkpoint?.();
    if (counts.reduce((sum, count) => sum + count, 0) > options.maxRows) {
      throw new Error(`IndexedDB selection exceeded max-input-rows of ${options.maxRows}`);
    }
    const records = await Promise.all(unique.map((key) => requestResult(index.getAll(key))));
    options.checkpoint?.();
    return records.flat().map((record) => canonicalRecord(record)).sort((left, right) => indexedDB.cmp(
      /** @type {IDBValidKey} */ (left.id), /** @type {IDBValidKey} */ (right.id)
    ));
  } finally {
    database.close();
  }
}

/**
 * @param {IDBObjectStore} store
 * @param {import('../../data-operations.js').DataOperator[]} operators
 */
function indexedQueryPlan(store, operators) {
  const filter = operators[0]?.op === 'filter' ? operators[0] : null;
  if (!filter || filter.search || !filter.predicates?.length) return null;
  const predicates = new Map(/** @type {[string, string[]][]} */ (filter.predicates.flatMap((predicate) => {
    if (predicate.optional || !QUERYABLE_STRING_KEY_PATHS.has(predicate.field)) return [];
    const values = Array.isArray(predicate.in) ? predicate.in : [predicate.equals];
    return values.length > 0
      && values.every((value) => typeof value === 'string' && value !== 'unknown')
      ? [[predicate.field, /** @type {string[]} */ (values)]]
      : [];
  })));
  /** @type {{ index: IDBIndex | IDBObjectStore, keys: IDBValidKey[] } | null} */
  let best = null;
  for (const index of [store, ...Array.from(store.indexNames, name => store.index(name))]) {
    const keyPath = Array.isArray(index.keyPath) ? index.keyPath : [index.keyPath];
    if (!keyPath.every((field) => typeof field === 'string' && predicates.has(field))) continue;
    const keys = keyPath.reduce(
      (combinations, field) => combinations.flatMap((combination) =>
        (predicates.get(String(field)) ?? []).map((value) => [...combination, value])
      ),
      /** @type {string[][]} */ ([[]])
    ).map((key) => keyPath.length === 1 ? key[0] : key);
    if (keys.length > MAX_QUERY_INDEX_LOOKUPS) continue;
    if (!best || keyPath.length > (Array.isArray(best.index.keyPath) ? best.index.keyPath.length : 1)) {
      best = { index, keys };
    }
  }
  return best;
}

/**
 * Reads a canonical record from an already-open database connection. Prefer
 * this over {@link readRecord} when reading many records in a loop (such as
 * per-record structural-metadata merges) so the connection can be reused
 * instead of reopened for each read.
 *
 * @param {IDBDatabase} database
 * @param {typeof ENTITY_STORES[number]} storeName
 * @param {string} id
 */
export async function readRecordWithConnection(database, storeName, id) {
  const result = await requestResult(database.transaction(storeName).objectStore(storeName).get(id));
  return result && typeof result === 'object' ? canonicalRecord(result) : null;
}

/**
 * @param {IDBFactory} indexedDB
 * @param {typeof ENTITY_STORES[number]} storeName
 * @param {string} id
 */
export async function readRecord(indexedDB, storeName, id) {
  const database = await openCanonicalDatabase(indexedDB);
  try {
    return await readRecordWithConnection(database, storeName, id);
  } finally {
    database.close();
  }
}

/**
 * @param {IDBFactory} indexedDB
 * @param {typeof ENTITY_STORES[number]} storeName
 * @param {string} indexName
 * @param {(string | number)[]} key
 */
export async function readIndex(indexedDB, storeName, indexName, key) {
  const database = await openCanonicalDatabase(indexedDB);
  try {
    const index = database.transaction(storeName).objectStore(storeName).index(indexName);
    if (key.length === 1 && !Array.isArray(index.keyPath)) {
      return (await requestResult(index.getAll(key[0]))).map((record) => canonicalRecord(record));
    }
    return (await requestResult(index.getAll(
      IDBKeyRange.bound(key, [...key, []], false, true)
    ))).map((record) => canonicalRecord(record));
  } finally {
    database.close();
  }
}

/**
 * Replaces retained canonical records with the supplied batch.
 *
 * Records are written in bounded transactions so a constrained browser reports
 * quota pressure after one chunk instead of after a whole store, and so callers
 * can report progress while very large batches are stored.
 *
 * @param {IDBFactory} indexedDB
 * @param {import('../model/schema.js').CanonicalBatch} batch
 * @param {{ batchSize?: number, onProgress?: (progress: { storedRecords: number, totalRecords: number }) => void, onMetrics?: (metrics: { durationMs: number, requestCount: number, storedRecords: number, deletedRecords: number, scannedKeys: number, committedBatches: number, abortedTransactions: number }) => void, previousBatch?: import('../model/schema.js').CanonicalBatch, signal?: AbortSignal }} [options]
 */
export async function replaceCanonicalBatch(indexedDB, batch, options = {}) {
  options.signal?.throwIfAborted();
  const errors = relationshipErrors(batch);
  if (errors.length > 0) throw new Error(`Canonical relationship validation failed: ${errors.join('; ')}`);
  const batchSize = options.batchSize ?? DEFAULT_WRITE_BATCH_SIZE;
  if (!Number.isInteger(batchSize) || batchSize < 1) {
    throw new TypeError('Write batch size must be a positive integer');
  }
  const recordsToWrite = Object.fromEntries(ENTITY_STORES.map((storeName) => {
    const previous = new Map((options.previousBatch?.[storeName] ?? [])
      .map((record) => [String(record.id), record]));
    return [storeName, (batch[storeName] ?? []).map((record) => pruneCanonicalRecord(storeName, record)).filter((record) => {
      const retained = previous.get(String(record.id));
      return retained !== record
        && (!retained || JSON.stringify(retained) !== JSON.stringify(record));
    })];
  }));
  const previousBatch = options.previousBatch;
  const recordsToDelete = previousBatch
    ? Object.fromEntries(ENTITY_STORES.map((storeName) => {
        const retained = new Set((batch[storeName] ?? []).map((record) => String(record.id)));
        return [storeName, (previousBatch[storeName] ?? [])
          .map((record) => record.id)
          .filter((id) => !retained.has(String(id)))];
      }))
    : null;
  const totalRecords = ENTITY_STORES.reduce(
    (total, storeName) => total + recordsToWrite[storeName].length,
    0
  );
  let storedRecords = 0;
  let deletedRecords = 0;
  let scannedKeys = 0;
  let requestCount = 0;
  let committedBatches = 0;
  let abortedTransactions = 0;
  const startedAt = monotonicNow();
  const currentMetrics = () => ({
    durationMs: monotonicNow() - startedAt,
    requestCount,
    storedRecords,
    deletedRecords,
    scannedKeys,
    committedBatches,
    abortedTransactions
  });
  debug('starting canonical batch replacement', { totalRecords, batchSize });
  const database = await openCanonicalDatabase(indexedDB);
  try {
    for (const storeName of ENTITY_STORES) {
      const records = batch[storeName] ?? [];
      const retained = new Set(records.map((record) => String(record.id)));
      // Evict first so reclaimed space is available to the writes that follow.
      const removal = readwriteTransaction(database, [storeName, STORAGE_SHARD_STORE]);
      const removalDone = transactionDone(removal);
      const removalStore = removal.objectStore(storeName);
      const knownDeleted = recordsToDelete?.[storeName];
      /** @type {IDBValidKey[]} */
      const removedIds = [];
      const reconciliationStrategy = knownDeleted
        ? 'retained-snapshot'
        : typeof removalStore.openKeyCursor === 'function'
          ? 'key-cursor'
          : 'all-keys-fallback';
      try {
        if (knownDeleted) {
          removedIds.push(.../** @type {IDBValidKey[]} */ (knownDeleted));
          deletedRecords += knownDeleted.length;
          requestCount += knownDeleted.length;
        } else if (typeof removalStore.openKeyCursor !== 'function') {
          requestCount += 1;
          const existing = await requestResult(removalStore.getAllKeys());
          scannedKeys += existing.length;
          for (const id of existing) {
            if (retained.has(String(id))) continue;
            removedIds.push(id);
            deletedRecords += 1;
            requestCount += 1;
          }
        } else {
          await new Promise((resolve, reject) => {
            requestCount += 1;
            const cursorRequest = removalStore.openKeyCursor();
            cursorRequest.onerror = () => reject(cursorRequest.error ?? new Error('IndexedDB key cursor failed'));
            cursorRequest.onsuccess = () => {
              const cursor = cursorRequest.result;
              if (!cursor) {
                resolve(undefined);
                return;
              }
              scannedKeys += 1;
              if (!retained.has(String(cursor.primaryKey))) {
                removedIds.push(cursor.primaryKey);
                deletedRecords += 1;
                requestCount += 1;
              }
              cursor.continue();
            };
          });
        }
        const removed = (await Promise.all(removedIds.map((id) => requestResult(removalStore.get(id)))))
          .filter((record) => record !== undefined);
        await writeAccountedRecords(removal, storeName, [], removed);
        commitTransaction(removal);
        await removalDone;
        committedBatches += 1;
      } catch (error) {
        abortedTransactions += 1;
        try {
          removal.abort();
        } catch {
          // The transaction already aborted or completed.
        }
        await removalDone.catch(() => undefined);
        throw error;
      }
      debug('completed canonical store eviction', {
        store: storeName,
        retainedRecords: retained.size,
        deletedRecords: knownDeleted?.length ?? deletedRecords,
        scannedKeys: knownDeleted ? 0 : scannedKeys,
        reconciliationStrategy
      });
      const changedRecords = recordsToWrite[storeName];
      for (let offset = 0; offset < changedRecords.length; offset += batchSize) {
        const boundedRecords = changedRecords.slice(offset, offset + batchSize);
        const transaction = readwriteTransaction(database, [storeName, STORAGE_SHARD_STORE]);
        const done = transactionDone(transaction);
        try {
          await writeAccountedRecords(transaction, storeName,
            boundedRecords.map((record) => prepareCanonicalRecord(storeName, record)));
          requestCount += boundedRecords.length;
          commitTransaction(transaction);
          await done;
          committedBatches += 1;
        } catch (error) {
          abortedTransactions += 1;
          try {
            transaction.abort();
          } catch {
            // The transaction already aborted or completed.
          }
          await done.catch(() => undefined);
          throw error;
        }
        storedRecords += boundedRecords.length;
        debug('committed canonical write chunk', {
          store: storeName,
          chunkRecords: boundedRecords.length,
          storedRecords,
          totalRecords
        });
        options.onProgress?.({ storedRecords, totalRecords });
      }
    }
    // Empty batches still report a terminal progress update.
    if (totalRecords === 0) options.onProgress?.({ storedRecords, totalRecords });
  } catch (error) {
    const metrics = currentMetrics();
    debug('canonical batch replacement failed', metrics);
    options.onMetrics?.(metrics);
    throw error;
  } finally {
    database.close();
  }
  const metrics = currentMetrics();
  debug('completed canonical batch replacement', metrics);
  options.onMetrics?.(metrics);
}

/**
 * @param {IDBFactory} indexedDB
 * @param {{ id: string, kind: string, createdAt: string, [field: string]: unknown }} transaction
 */
export async function recordTransaction(indexedDB, transaction) {
  const database = await openCanonicalDatabase(indexedDB);
  try {
    const write = readwriteTransaction(database, TRANSACTION_STORE);
    const done = transactionDone(write);
    const store = write.objectStore(TRANSACTION_STORE);
    store.put(transaction);
    /** @param {Record<string, unknown> | undefined} candidate */
    const persistentReceipt = (candidate) => (
      candidate?.kind === 'dashboard-snapshot'
      || (candidate?.kind === 'ingest-normalized-jsonl' && typeof candidate.payloadHash === 'string')
    );
    if (typeof store.count !== 'function' || typeof store.index('byCreatedAt').openCursor !== 'function') {
      const records = await requestResult(store.index('byCreatedAt').getAll());
      let remaining = Math.max(0, records.length - MAX_TRANSACTION_RECORDS);
      for (const expired of records) {
        if (remaining === 0) break;
        if (persistentReceipt(expired)) continue;
        store.delete(expired.id);
        remaining -= 1;
      }
    } else {
      const count = await requestResult(store.count());
      let remaining = Math.max(0, count - MAX_TRANSACTION_RECORDS);
      await new Promise((resolve, reject) => {
        const cursorRequest = store.index('byCreatedAt').openCursor();
        cursorRequest.onerror = () => reject(cursorRequest.error ?? new Error('IndexedDB transaction cursor failed'));
        cursorRequest.onsuccess = () => {
          const cursor = cursorRequest.result;
          if (!cursor || remaining === 0) {
            resolve(undefined);
            return;
          }
          if (!persistentReceipt(cursor.value)) {
            cursor.delete();
            remaining -= 1;
          }
          cursor.continue();
        };
      });
    }
    await done;
    debug('recorded ingestion transaction', {
      kind: transaction.kind,
      committedRecords: transaction.committedRecords ?? null
    });
  } finally {
    database.close();
  }
}

/** @param {IDBFactory} indexedDB @param {string} id */
export async function readTransaction(indexedDB, id) {
  const database = await openCanonicalDatabase(indexedDB);
  try {
    const result = await requestResult(
      database.transaction(TRANSACTION_STORE).objectStore(TRANSACTION_STORE).get(id)
    );
    return result && typeof result === 'object' ? result : null;
  } finally {
    database.close();
  }
}

/** @param {IDBFactory} indexedDB @param {string[]} ids */
export async function readTransactionBatch(indexedDB, ids) {
  const database = await openCanonicalDatabase(indexedDB);
  try {
    const transaction = database.transaction(TRANSACTION_STORE);
    const done = transactionDone(transaction);
    const store = transaction.objectStore(TRANSACTION_STORE);
    try {
      const records = await Promise.all(ids.map((id) => requestResult(store.get(id))));
      await done;
      return records;
    } catch (error) {
      await done.catch(() => undefined);
      throw error;
    }
  } finally {
    database.close();
  }
}

/** @returns {Error} */
function ingestionLockTimeoutError() {
  const error = new Error('Timed out waiting for canonical ingestion lock');
  error.name = 'CanonicalIngestionLockTimeoutError';
  return error;
}

/**
 * Serializes canonical ingestion with the Web Locks API, which the browser
 * releases as soon as the holding tab or worker goes away. A leased record
 * cannot do that: a tab terminated mid-ingestion leaves its lease behind, and
 * every later ingestion then stalls until that lease expires.
 *
 * @template T
 * @param {LockManager} locks
 * @param {() => Promise<T>} task
 * @param {{ acquireTimeoutMs?: number, waitingNoticeDelayMs?: number, onWaiting?: () => void }} options
 */
async function withWebIngestionLock(locks, task, options) {
  const name = `canonical-ingestion:${canonicalDatabaseName()}`;
  const startedAt = Date.now();
  const acquireTimeoutMs = options.acquireTimeoutMs ?? INGESTION_LOCK_ACQUIRE_TIMEOUT_MS;
  const controller = new AbortController();
  let acquired = false;
  // Aborting only cancels a request that is still pending, so a granted lock
  // keeps the ingestion running for as long as it needs.
  const timeout = setTimeout(() => controller.abort(), acquireTimeoutMs);
  const notice = setTimeout(() => {
    if (!acquired) options.onWaiting?.();
  }, options.waitingNoticeDelayMs ?? INGESTION_LOCK_WAITING_NOTICE_DELAY_MS);
  try {
    return await locks.request(name, { mode: 'exclusive', signal: controller.signal }, async () => {
      acquired = true;
      clearTimeout(timeout);
      clearTimeout(notice);
      debug('acquired canonical ingestion lock', { name, waitedMs: Date.now() - startedAt });
      try {
        return await task();
      } finally {
        debug('released canonical ingestion lock', { name });
      }
    });
  } catch (error) {
    if (!acquired
        && controller.signal.aborted
        && /** @type {{ name?: unknown }} */ (error)?.name === 'AbortError') {
      debug('timed out waiting for canonical ingestion lock', { name, waitedMs: Date.now() - startedAt });
      throw ingestionLockTimeoutError();
    }
    throw error;
  } finally {
    clearTimeout(timeout);
    clearTimeout(notice);
  }
}

/**
 * Serializes canonical ingestion across tabs and workers.
 *
 * `locks` selects how ingestion is serialized: omitting it uses the Web Locks
 * API when the environment provides it, and passing `null` forces the leased
 * lock record kept in the transactions store.
 *
 * @template T
 * @param {IDBFactory} indexedDB
 * @param {() => Promise<T>} task
 * @param {{ acquireTimeoutMs?: number, retryDelayMs?: number, waitingNoticeDelayMs?: number, onWaiting?: () => void, locks?: LockManager | null }} [options]
 */
export async function withCanonicalIngestionLock(indexedDB, task, options = {}) {
  const locks = options.locks === undefined ? globalThis.navigator?.locks : options.locks;
  if (locks && typeof locks.request === 'function') {
    return await withWebIngestionLock(locks, task, options);
  }
  const owner = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}:${Math.random()}`;
  const startedAt = Date.now();
  const acquireTimeoutMs = options.acquireTimeoutMs ?? INGESTION_LOCK_ACQUIRE_TIMEOUT_MS;
  const retryDelayMs = options.retryDelayMs ?? INGESTION_LOCK_RETRY_DELAY_MS;
  const waitingNoticeDelayMs = options.waitingNoticeDelayMs ?? INGESTION_LOCK_WAITING_NOTICE_DELAY_MS;
  let notified = false;
  for (;;) {
    const waitedMs = Date.now() - startedAt;
    if (waitedMs > acquireTimeoutMs) throw ingestionLockTimeoutError();
    const database = await openCanonicalDatabase(indexedDB);
    let acquired;
    try {
      const transaction = database.transaction(TRANSACTION_STORE, 'readwrite');
      const done = transactionDone(transaction);
      const store = transaction.objectStore(TRANSACTION_STORE);
      const existing = await requestResult(store.get(INGESTION_LOCK_ID));
      const now = Date.now();
      const lease = ingestionLockLease(existing, now);
      acquired = !lease.active;
      if (acquired) {
        if (existing) {
          debug('replacing stale canonical ingestion lock', {
            heldBy: existing?.owner ?? null,
            expiresAt: lease.expiresAt,
            waitedMs: now - startedAt
          });
        }
        store.put({
          id: INGESTION_LOCK_ID,
          kind: 'canonical-ingestion-lock',
          createdAt: new Date(now).toISOString(),
          owner,
          expiresAt: now + INGESTION_LOCK_LEASE_MS
        });
      } else {
        debug('waiting for canonical ingestion lock', {
          heldBy: existing?.owner ?? null,
          expiresInMs: Number(existing?.expiresAt ?? 0) - now,
          waitedMs: now - startedAt
        });
        if (!notified && now - startedAt >= waitingNoticeDelayMs) {
          notified = true;
          options.onWaiting?.();
        }
      }
      await done;
    } finally {
      // Always close the connection, even if the lock check fails, so a
      // failed acquisition attempt never leaves an open handle that blocks
      // subsequent opens, writes, or a local-data reset.
      database.close();
    }
    if (acquired) break;
    await new Promise((resolve) => { setTimeout(resolve, retryDelayMs); });
  }
  debug('acquired canonical ingestion lock', { owner, waitedMs: Date.now() - startedAt });

  try {
    return await task();
  } finally {
    const database = await openCanonicalDatabase(indexedDB);
    try {
      const transaction = database.transaction(TRANSACTION_STORE, 'readwrite');
      const done = transactionDone(transaction);
      const store = transaction.objectStore(TRANSACTION_STORE);
      const existing = await requestResult(store.get(INGESTION_LOCK_ID));
      if (existing?.owner === owner) store.delete(INGESTION_LOCK_ID);
      await done;
    } finally {
      database.close();
    }
    debug('released canonical ingestion lock', { owner });
  }
}

/** @param {IDBFactory} indexedDB */
export async function readTransactions(indexedDB) {
  const database = await openCanonicalDatabase(indexedDB);
  try {
    return await requestResult(database.transaction(TRANSACTION_STORE).objectStore(TRANSACTION_STORE).getAll());
  } finally {
    database.close();
  }
}
