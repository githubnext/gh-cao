import { state } from './reactive.js';
import { createDebug } from './debug.js';

const debugBrowserFirstLoad = createDebug('browser-first-load');

/** @type {Array<{ timestamp: string, event: string, details: Record<string, string | number | boolean | null> }>} */
const preparationLog = [];
/** @typedef {{ persisted: boolean | null, usage: number | null, quota: number | null, errorName?: string }} PreparationStorage */
export const preparationStorage = state(/** @type {PreparationStorage | null} */ (null));
let recording = false;

export function resetPreparationDiagnostics() {
  preparationLog.length = 0;
  preparationStorage.set(null);
  recording = true;
  debugBrowserFirstLoad({ event: 'preparation-started' });
}

export function finishPreparationDiagnostics() {
  recording = false;
  debugBrowserFirstLoad({ event: 'preparation-finished', recordedEvents: preparationLog.length });
}

/** @param {string} event @param {Record<string, string | number | boolean | null>} [details] */
export function recordPreparationEvent(event, details = {}) {
  if (!recording) return;
  preparationLog.push({ timestamp: new Date().toISOString(), event, details });
}

/** @param {PreparationStorage} diagnostics */
export function recordPreparationStorage(diagnostics) {
  preparationStorage.set(diagnostics);
  debugBrowserFirstLoad({
    event: 'storage-recorded',
    persisted: diagnostics.persisted,
    errorName: diagnostics.errorName ?? null
  });
  recordPreparationEvent('browser-storage', diagnostics);
}

export function preparationDiagnostics() {
  const { ingestion } = browserFirstLoad.get();
  return {
    storage: preparationStorage.get(),
    log: [...preparationLog],
    ingestion: ingestion ? {
      message: ingestion.message,
      detailsSubtitle: ingestion.detailsSubtitle ?? null,
      details: ingestion.details ?? []
    } : null
  };
}

/** Browser import lifecycle, not campaign activity or canonical query data. */
export const browserFirstLoad = state(
  /** @type {{ status: 'inactive' | 'loading' | 'failed', dismissed: boolean, reason?: 'upgrade' | 'missing-snapshot', oldVersion?: number, newVersion?: number, completed?: number, total?: number, stage?: 'files' | 'maintenance' | 'inventory' | 'queries', ingestion?: { id: string, message: string, detailsSubtitle?: string, details?: string[], actions?: Array<{ label: string, run: () => void }> } }} */ ({
    status: 'inactive',
    dismissed: false
  })
);

export function showBrowserFirstLoad() {
  const previousStatus = browserFirstLoad.get().status;
  browserFirstLoad.set((current) => ({ ...current, dismissed: false }));
  debugBrowserFirstLoad({ event: 'reopen-requested', previousStatus });
}
