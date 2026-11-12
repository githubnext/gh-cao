import { createDebug } from './debug.js';
import { DEMO_MODE } from './deployment-profile.js';

const SOURCE_PATH_SEGMENT = '/src/';

const debugStorageScope = createDebug('storage-scope');

/**
 * Resolve the deployment path shared by the page and its data worker.
 * @param {string} [moduleHref]
 * @param {string | undefined} [documentPath]
 * @returns {string}
 */
export function dashboardPagePath(
  moduleHref = import.meta.url,
  documentPath = globalThis.document?.location?.pathname
) {
  const moduleUrl = new URL(moduleHref);
  const sourceIndex = moduleUrl.protocol.startsWith('http')
    ? moduleUrl.pathname.lastIndexOf(SOURCE_PATH_SEGMENT)
    : -1;
  const source = sourceIndex >= 0 ? 'module-url' : (typeof documentPath === 'string' && documentPath ? 'document' : 'default-root');
  debugStorageScope({ event: 'page-path-resolved', source });
  if (sourceIndex >= 0) return moduleUrl.pathname.slice(0, sourceIndex + 1);
  if (typeof documentPath === 'string' && documentPath) return documentPath;
  return '/';
}

/**
 * @param {string} key
 * @param {string} [pathname]
 */
export function scopedStorageKey(key, pathname = dashboardPagePath()) {
  if (DEMO_MODE) key = `${key}:synthetic-demo`;
  return pathname === '/' ? key : `${key}:${encodeURIComponent(pathname)}`;
}

/**
 * Remove local settings belonging to the current dashboard deployment.
 * @param {Storage} storage
 * @param {string} [pathname]
 */
export function clearScopedStorage(storage, pathname = dashboardPagePath()) {
  const suffix = `${DEMO_MODE ? ':synthetic-demo' : ''}${pathname === '/' ? '' : `:${encodeURIComponent(pathname)}`}`;
  let removedCount = 0;
  for (let index = storage.length - 1; index >= 0; index -= 1) {
    const key = storage.key(index);
    const appKey = key?.startsWith('central-agentic-ops.dashboard.') === true;
    // Root deployments retain the legacy unsuffixed keys; current base keys contain no colon.
    const inScope = suffix === '' ? !key?.includes(':') : key?.endsWith(suffix);
    if (appKey && inScope) {
      storage.removeItem(key);
      removedCount += 1;
    }
  }
  debugStorageScope({ event: 'scoped-storage-cleared', removedCount });
}
