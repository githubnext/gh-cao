import { createDebug, diagnosticErrorName } from '../../debug.js';
import { resolveDashboardQuerySources } from './declarative.js';

const debugStatisticalCache = createDebug('statistical-cache');

export const DEFAULT_STATISTICAL_REFRESH_INTERVAL_MS = 300_000;
const MAX_TIMEOUT_MS = 2_147_483_647;
let refreshIntervalMs = DEFAULT_STATISTICAL_REFRESH_INTERVAL_MS;

/** @param {unknown} value */
export function statisticalRefreshInterval(value = DEFAULT_STATISTICAL_REFRESH_INTERVAL_MS) {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value <= 0) {
    throw new TypeError('statisticalRefreshIntervalMs must be a positive safe integer.');
  }
  return value;
}

/** Worker/bootstrap configuration, never a query parameter. @param {{ statisticalRefreshIntervalMs?: unknown }} configuration */
export function configureStatisticalRefresh(configuration) {
  refreshIntervalMs = statisticalRefreshInterval(configuration.statisticalRefreshIntervalMs);
}

export function statisticalRefreshConfiguration() {
  return refreshIntervalMs;
}

/**
 * Hosted response metadata owns its refresh cadence; bootstrap is only a
 * fallback. Keep the full interval; only the timer delay is clamped.
 * @param {Record<string, { metadata?: Record<string, unknown> }>} sources
 * @param {number} fallback
 */
export function hostedStatisticalRefreshInterval(sources, fallback) {
  let interval = Infinity;
  for (const source of Object.values(sources)) {
    const value = source.metadata?.['refresh-interval-ms'];
    if (typeof value === 'number' && Number.isSafeInteger(value) && value > 0) {
      interval = Math.min(interval, value);
    }
  }
  return Number.isFinite(interval) ? interval : fallback;
}

/** @param {unknown[]} queries @param {Iterable<string>} names */
export function requestsStatisticalSources(queries, names) {
  return resolveDashboardQuerySources(queries, names).some((name) => name.startsWith('statistical-'));
}

/**
 * One worker-local slot per owned scope/capacity. Invalidating authority drops
 * evidence, not the attempt ledger; cancellation and errors also consume a slot.
 * @template T
 * @param {{ now?: () => number, interval?: () => number }} [clock]
 */
export function createStatisticalCache(clock = {}) {
  const now = clock.now ?? (() => performance.now());
  const interval = clock.interval ?? statisticalRefreshConfiguration;
  /** @type {Map<string, { attemptedAt: number, revision?: string, value?: T, error?: unknown, flight?: Promise<void>, generation: number }>} */
  const slots = new Map();
  return {
    invalidate() {
      debugStatisticalCache({ operation: 'invalidate', status: 'dropped', slotCount: slots.size });
      for (const slot of slots.values()) {
        delete slot.value;
        delete slot.revision;
        slot.generation++;
      }
    },
    /**
     * @param {string} key
     * @param {string} revision
     * @param {() => Promise<T>} build
     * @param {() => void} [checkpoint]
     * @param {boolean} [ready]
     */
    async get(key, revision, build, checkpoint, ready = true) {
      checkpoint?.();
      let slot = slots.get(key);
      if (!slot) {
        // Scope/capacity comes from owned configuration, not consumer filters.
        if (slots.size >= 8) {
          debugStatisticalCache({ operation: 'slot', status: 'capacity-exceeded', slotCount: slots.size });
          throw new Error('Statistical cache scope capacity exceeded');
        }
        slot = { attemptedAt: -Infinity, generation: 0 };
        slots.set(key, slot);
      }
      if (ready && !slot.flight && slot.revision !== revision && now() - slot.attemptedAt >= interval()) {
        slot.attemptedAt = now();
        const generation = slot.generation;
        const target = slot;
        const startedAt = target.attemptedAt;
        let outcome = 'superseded';
        /** @type {string | undefined} */
        let errorName;
        target.flight = Promise.resolve().then(build).then((value) => {
          if (target.generation !== generation) return;
          target.value = value;
          target.revision = revision;
          delete target.error;
          outcome = 'built';
        }, (error) => {
          if (target.generation !== generation) return;
          target.error = error;
          outcome = 'failed';
          errorName = diagnosticErrorName(error);
        }).finally(() => {
          delete target.flight;
          debugStatisticalCache({
            operation: 'refresh',
            status: outcome,
            durationMs: Math.round(now() - startedAt),
            ...(errorName ? { errorName } : {})
          });
        });
      }
      await slot.flight;
      checkpoint?.();
      return {
        value: slot.value, error: slot.error, stale: slot.revision !== revision || Boolean(slot.error),
        sourceRevision: slot.revision, attemptedAt: slot.attemptedAt,
        refreshIntervalMs: interval()
      };
    }
  };
}

/**
 * Only active subscribers own timers. Await refresh to avoid overlapping work,
 * and read the interval again each time so configuration never resets a ledger.
 * @param {() => unknown | Promise<unknown>} refresh
 * @param {AbortSignal} signal
 * @param {() => number} [interval]
 */
export function subscribeStatisticalRefresh(refresh, signal, interval = statisticalRefreshConfiguration) {
  let active = !signal.aborted;
  /** @type {ReturnType<typeof setTimeout> | undefined} */
  let timer;
  const schedule = () => {
    if (!active || signal.aborted) return;
    timer = setTimeout(async () => {
      try { await refresh(); } finally { schedule(); }
    }, Math.min(interval(), MAX_TIMEOUT_MS));
  };
  const stop = () => {
    active = false;
    clearTimeout(timer);
    signal.removeEventListener('abort', stop);
  };
  if (active) signal.addEventListener('abort', stop, { once: true });
  schedule();
  return stop;
}
