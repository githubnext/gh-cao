import { createDebug } from './debug.js';

const debugBackendMode = createDebug('backend-mode');

/** @returns {boolean} */
export function cliQueueAvailable() {
  return Reflect.get(globalThis, '__CLI_QUEUE__') === true;
}

/** Local credentials can administer variable scopes; hosted installation tokens cannot. */
export function localCliQueueAvailable() {
  return cliQueueAvailable() && Reflect.get(globalThis, '__LOCAL_CLI_QUEUE__') === true;
}

/** @returns {string | null} */
export function localCliQueueCapability() {
  return localCliQueueAvailable() ? globalThis.localStorage?.getItem('cao-dashboard-cli-session') ?? null : null;
}

/** @param {Document} [document] */
export function usesRemoteDataBackend(document = globalThis.document) {
  const remote = document?.querySelector?.('meta[name="dashboard-data-backend"]')?.getAttribute("content") === "server-http";
  debugBackendMode({ operation: 'uses-remote-data-backend', remote });
  return remote;
}

/**
 * Removes static-dashboard PWA state before server-backed startup.
 * @param {{ serviceWorkers?: ServiceWorkerContainer, cacheStorage?: CacheStorage }} [dependencies]
 */
export async function disableRemoteDashboardPwa(dependencies = {}) {
  const serviceWorkers = dependencies.serviceWorkers ?? globalThis.navigator?.serviceWorker;
  const cacheStorage = dependencies.cacheStorage ?? globalThis.caches;
  const registrations = await serviceWorkers?.getRegistrations?.().catch(() => []) ?? [];
  const unregisterResults = await Promise.allSettled(registrations.map((registration) => registration.unregister()));
  debugBackendMode({
    operation: 'disable-remote-pwa',
    status: 'service-workers-unregistered',
    count: unregisterResults.length
  });
  const keys = await cacheStorage?.keys?.().catch(() => []) ?? [];
  const matchingKeys = keys.filter((key) => key.startsWith("central-agentic-ops-dashboard-"));
  await Promise.allSettled(matchingKeys.map((key) => cacheStorage.delete(key)));
  debugBackendMode({
    operation: 'disable-remote-pwa',
    status: 'caches-deleted',
    count: matchingKeys.length
  });
}
