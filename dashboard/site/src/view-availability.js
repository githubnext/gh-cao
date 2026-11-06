import { createDebug } from './debug.js';
import { cliQueueAvailable } from './backend-mode.js';

/**
 * @typedef {'static'|'hosted'} DashboardDataBackend
 */

const debugViewAvailability = createDebug('view-availability');

/** @param {unknown} page */
export function pageCapabilityAvailable(page) {
  if (typeof page !== 'object' || page === null || Array.isArray(page)) return false;
  const requirement = /** @type {Record<string, unknown>} */ (page).requires;
  if (requirement === undefined) return true;
  if (typeof requirement !== 'object' || requirement === null || Array.isArray(requirement)) return false;
  const capability = /** @type {{ capability?: unknown }} */ (requirement).capability;
  if (capability === undefined) return pageRequiredSource(page) !== null;
  if (capability === 'admin') return Reflect.get(globalThis, '__DASHBOARD_ADMIN__') === true;
  return capability === 'cli-queue' && cliQueueAvailable();
}

/** @param {unknown} page @returns {string | null} */
export function pageRequiredSource(page) {
  if (typeof page !== 'object' || page === null || Array.isArray(page)) return null;
  const requirement = Reflect.get(page, 'requires');
  if (typeof requirement !== 'object' || requirement === null || Array.isArray(requirement)) return null;
  const source = Reflect.get(requirement, 'source');
  return typeof source === 'string' && source.length > 0 ? source : null;
}

/**
 * @param {unknown} page
 * @param {Record<string, { rows?: unknown[], metadata?: Record<string, unknown> } | undefined>} sources
 */
export function pageSourceAvailable(page, sources) {
  const name = pageRequiredSource(page);
  if (!name) return true;
  const source = sources[name];
  return Boolean(source?.rows?.length) && (source?.metadata?.availability ?? 'available') === 'available';
}

/** @param {Document} [document] @returns {DashboardDataBackend} */
export function dashboardDataBackend(document = globalThis.document) {
  return document?.querySelector?.('meta[name="dashboard-data-backend"]')?.getAttribute('content') === 'server-http'
    ? 'hosted'
    : 'static';
}

/** @param {unknown} view @param {DashboardDataBackend} backend */
export function viewBackendAvailable(view, backend) {
  if (typeof view !== 'object' || view === null || Array.isArray(view)) return true;
  const requirement = /** @type {Record<string, unknown>} */ (view).requires;
  if (requirement === undefined) return true;
  if (typeof requirement !== 'object' || requirement === null || Array.isArray(requirement)) {
    debugViewAvailability({ event: 'malformed-requirement', backend });
    return false;
  }
  return /** @type {Record<string, unknown>} */ (requirement).backend === backend;
}

/** @param {unknown} view @param {DashboardDataBackend} backend */
export function viewBackendHidden(view, backend) {
  return !viewBackendAvailable(view, backend)
    && /** @type {{ requires?: { ['on-unavailable']?: unknown } }} */ (view)?.requires?.['on-unavailable'] === 'hide';
}

/** @param {unknown} view @param {DashboardDataBackend} backend @returns {string | undefined} */
export function viewBackendUnavailableMessage(view, backend) {
  if (viewBackendAvailable(view, backend)) return undefined;
  const requirement = /** @type {{ requires?: { backend?: unknown, message?: unknown } }} */ (view)?.requires;
  const requiredBackend = typeof requirement?.backend === 'string' ? requirement.backend : 'unknown';
  const hasCustomMessage = typeof requirement?.message === 'string' && requirement.message.trim().length > 0;
  debugViewAvailability({ event: 'view-unavailable', requiredBackend, currentBackend: backend, hasCustomMessage });
  return hasCustomMessage
    ? /** @type {string} */ (requirement.message)
    : 'This view is unavailable on the current dashboard backend.';
}
