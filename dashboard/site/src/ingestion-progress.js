import { createElapsedStepTracker } from './elapsed-step-tracker.js';
import { createDebug } from './debug.js';
import { formatDataSize } from './view-formatters.js';

export { formatDataSize } from './view-formatters.js';

const INGESTION_PROGRESS_INTERVAL_MS = 1_000;
const INGESTION_PROGRESS_HISTORY_LIMIT = 100;
let nextIngestionProgressId = 0;

const debugIngestionProgress = createDebug('ingestion-progress');

/**
 * Publishes a user-facing notification from the data worker.
 * @param {{ id?: string, kind?: 'ingestion', message?: string, icon?: 'download', detailsSubtitle?: string, tone?: 'info' | 'success' | 'warning' | 'error', duration?: number, details?: string[], actions?: Array<{ label: string, operation: 'cancel-data-ingestion' | 'sync-dashboard-queries', placement: 'details', requestId?: number }>, dismiss?: boolean }} notification
 * @param {{ postMessage: (message: unknown) => void }} [target]
 */
export function publishWorkerNotification(notification, target = self) {
  target.postMessage({ type: 'notification', notification });
}

/**
 * Publishes the worker-owned state for the top loading bar.
 * @param {{ id: string, phase: 'start' | 'update' | 'complete', completed?: number, total?: number, stage?: 'files' | 'maintenance' | 'inventory' | 'queries' }} state
 * @param {{ postMessage: (message: unknown) => void }} [target]
 */
export function publishWorkerLoadingProgress(state, target = self) {
  target.postMessage({ type: 'loading-progress', state });
}

/**
 * Reports long-running ingestion status through the main-thread notification manager.
 * @param {{ postMessage: (message: unknown) => void }} [target]
 * @param {number} [requestId]
 */
export function startIngestionProgress(target = self, requestId) {
  const id = `ingestion-progress-${++nextIngestionProgressId}`;
  const clock = createElapsedStepTracker('Preparing data...', {
    historyLimit: INGESTION_PROGRESS_HISTORY_LIMIT
  });
  let status = 'Preparing data...';
  let workloadStartedAt = 0;
  let processedBytes = 0;
  /** @type {number | undefined} */
  let totalBytes;
  let started = false;
  let completed = false;
  /** @param {string} message */
  const publishLog = (message) => target.postMessage({ type: 'preparation-log', event: { message } });
  const updateStatus = () => {
    if (workloadStartedAt === 0) {
      status = 'Preparing data...';
      return;
    }
    const byteProgress = typeof totalBytes === 'number'
      ? `${formatDataSize(processedBytes)}/${formatDataSize(totalBytes)}`
      : formatDataSize(processedBytes);
    const elapsedMs = Date.now() - workloadStartedAt;
    const remainingMs = typeof totalBytes === 'number'
      ? estimateRemainingTime(processedBytes, totalBytes, elapsedMs)
      : null;
    const remaining = typeof totalBytes === 'number' && processedBytes >= totalBytes
      ? '0s remaining'
      : remainingMs === null ? 'Estimating time remaining' : `${formatRemainingTime(remainingMs)} remaining`;
    status = `${byteProgress} · ${remaining}`;
  };
  const report = () => {
    if (!completed) {
      updateStatus();
      const snapshot = clock.snapshot();
      publishWorkerNotification({
        id,
        kind: 'ingestion',
        message: status,
        icon: 'download',
        detailsSubtitle: 'Downloading and processing a local copy in this browser can take several minutes. Cached shards are reused.',
        details: snapshot.history,
        actions: [
          { label: 'Cancel', operation: 'cancel-data-ingestion', placement: 'details', requestId }
        ],
        tone: 'info',
        duration: 0
      }, target);
    }
  };
  /** @type {ReturnType<typeof setInterval> | undefined} */
  let interval;
  return {
    start() {
      if (started || completed) return;
      started = true;
      publishWorkerLoadingProgress({ id, phase: 'start' }, target);
      report();
      interval = setInterval(report, INGESTION_PROGRESS_INTERVAL_MS);
      debugIngestionProgress({ event: 'started', id });
    },
    /** @param {number | undefined} bytes */
    setWorkload(bytes) {
      totalBytes = typeof bytes === 'number' && Number.isFinite(bytes) && bytes >= 0 ? bytes : undefined;
      processedBytes = 0;
      workloadStartedAt = Date.now();
      updateStatus();
      debugIngestionProgress({ event: 'workload-set', id, totalBytes });
    },
    /**
     * @param {{ bytesProcessed: number, recordsIngested: number, totalBytes?: number }} progress
     */
    update({ bytesProcessed: nextProcessedBytes, recordsIngested, totalBytes: nextTotalBytes }) {
      if (completed) return;
      processedBytes = Math.max(0, Number(nextProcessedBytes) || 0);
      if (typeof nextTotalBytes === 'number' && Number.isFinite(nextTotalBytes) && nextTotalBytes >= 0) {
        totalBytes = nextTotalBytes;
      }
      const byteProgress = typeof totalBytes === 'number' && totalBytes > 0
        ? `${formatDataSize(processedBytes)}/${formatDataSize(totalBytes)}`
        : formatDataSize(processedBytes);
      updateStatus();
      clock.update(
        `Parsing overall: ${recordsIngested.toLocaleString('en-US')} rec, ${byteProgress}.`,
        'parsing'
      );
      publishLog(clock.snapshot().message);
    },
    /**
     * Reports the storage phase, which dominates large ingestions and would
     * otherwise leave the notification frozen on the last parsed record count.
     * @param {{ storedRecords: number, totalRecords: number }} progress
     */
    store({ storedRecords, totalRecords }) {
      if (completed) return;
      updateStatus();
      clock.update(
        `Storing ${storedRecords.toLocaleString('en-US')}/${totalRecords.toLocaleString('en-US')} rec.`,
        'storing'
      );
      publishLog(clock.snapshot().message);
    },
    /** @param {string} nextMessage */
    log(nextMessage) {
      if (completed) return;
      clock.advance(nextMessage);
      publishLog(nextMessage);
    },
    /** @param {number} completed @param {number} total @param {'files' | 'maintenance' | 'inventory' | 'queries'} stage */
    reportImportProgress(completed, total, stage) {
      if (!started) return;
      publishWorkerLoadingProgress({ id, phase: 'update', completed, total, stage }, target);
    },
    complete() {
      if (completed) return;
      completed = true;
      if (interval) clearInterval(interval);
      debugIngestionProgress({ event: 'completed', id, started });
      if (!started) return;
      publishWorkerLoadingProgress({ id, phase: 'complete' }, target);
      publishWorkerNotification({ id, kind: 'ingestion', dismiss: true }, target);
    }
  };
}

/**
 * Predicts remaining processing time with a linear bytes-to-time model.
 * @param {number} processedBytes
 * @param {number} totalBytes
 * @param {number} elapsedMs
 */
export function estimateRemainingTime(processedBytes, totalBytes, elapsedMs) {
  if (!Number.isFinite(processedBytes) || !Number.isFinite(totalBytes) || !Number.isFinite(elapsedMs)
      || processedBytes <= 0 || totalBytes <= processedBytes || elapsedMs <= 0) {
    return null;
  }
  return Math.max(0, Math.round((totalBytes - processedBytes) * elapsedMs / processedBytes));
}

/** @param {number} milliseconds */
function formatRemainingTime(milliseconds) {
  const seconds = Math.max(1, Math.ceil(milliseconds / 1_000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.ceil(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}
