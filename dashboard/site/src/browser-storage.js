import { createDebug, diagnosticErrorName } from './debug.js';

const debugBrowserStorage = createDebug('browser-storage');

export const TEMPORARY_STORAGE_WARNING = 'Browser storage is temporary or not guaranteed. In private browsing, downloaded dashboard data will be lost when the private session ends. Use a regular window to reuse data on future visits. Campaign data is not affected.';

/**
 * Browsers deliberately do not expose a reliable private-mode flag. Check the
 * storage guarantee instead; best-effort storage also covers private sessions.
 * @param {StorageManager | undefined} storage
 */
export async function inspectBrowserStorage(storage) {
  const [persistence, estimate] = await Promise.allSettled([
    Promise.resolve().then(async () => {
      const persisted = await storage?.persisted?.() ?? null;
      if (persisted !== false || !storage?.persist) return persisted;
      const granted = await storage.persist();
      debugBrowserStorage({ operation: 'persist-request', granted: granted === true });
      return granted;
    }),
    Promise.resolve().then(() => storage?.estimate?.() ?? null)
  ]);
  const inspection = {
    persisted: persistence.status === 'fulfilled' ? persistence.value : null,
    usage: estimate.status === 'fulfilled' ? estimate.value?.usage ?? null : null,
    quota: estimate.status === 'fulfilled' ? estimate.value?.quota ?? null : null,
    ...(persistence.status === 'rejected' || estimate.status === 'rejected'
      ? { errorName: diagnosticErrorName(persistence.status === 'rejected'
          ? persistence.reason : estimate.status === 'rejected' ? estimate.reason : undefined) }
      : {})
  };
  debugBrowserStorage({
    operation: 'inspect',
    persisted: inspection.persisted,
    'has-estimate': inspection.usage !== null && inspection.quota !== null,
    'persist-api': typeof storage?.persist === 'function',
    ...('errorName' in inspection ? { errorName: inspection.errorName } : {})
  });
  return inspection;
}
