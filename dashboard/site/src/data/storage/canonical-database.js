// Startup-safe canonical database identity and deletion. The main thread uses
// these without loading the worker-owned storage, retention, and normalization.
import { scopedStorageKey } from '../../storage-scope.js';
import { createDebug } from '../../debug.js';

const debug = createDebug('data:indexeddb');

export const DATABASE_NAME = 'gh-aw-cao-dashboard-data';

/** @param {string} [pathname] */
export function canonicalDatabaseName(pathname) {
  return scopedStorageKey(DATABASE_NAME, pathname);
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
