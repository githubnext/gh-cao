import { state } from './reactive.js';

/** @typedef {{ 'freshness-source': string, 'health-source'?: string, 'ingestion-source'?: string, 'stale-after-hours': number }} DashboardStatusConfiguration */

/** Dashboard refresh/import lifecycle, not upstream collection health. */
export const dashboardDataStatus = state(
  /** @type {{ phase: 'unknown' | 'loading' | 'cached' | 'ready' | 'stale', snapshot: { createdAt: string } | null }} */ ({
    phase: 'unknown',
    snapshot: null
  })
);
