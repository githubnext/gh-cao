import { state } from './reactive.js';

/** @typedef {{ 'freshness-source': string, 'health-source'?: string, 'ingestion-source'?: string, 'stale-after-hours': number }} DashboardStatusConfiguration */

/** Dashboard refresh/import lifecycle, not upstream collection health. */
export const dashboardDataStatus = state(
  /** @type {{ phase: 'unknown' | 'loading' | 'cached' | 'ready' | 'stale', snapshot: { createdAt: string } | null }} */ ({
    phase: 'unknown',
    snapshot: null
  })
);

/**
 * Transient local acquisition telemetry, not canonical query results.
 * @typedef {{ passId: string, repository: string, phase: 'discovering' | 'receiving' | 'ingesting' | 'waiting' | 'paused' | 'failed', receivedRuns: number, startedAt: string, updatedAt: string }} LocalIngestionProgress
 */
export const localIngestionProgress = state(/** @type {LocalIngestionProgress | null} */ (null));
