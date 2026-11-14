/**
 * Reactive store that binds one dashboard query result to one reactive state,
 * so views can render before their queries resolve and update each bound UI
 * element independently as results arrive.
 */

import { batch, state, untracked } from './reactive.js';
import { createDebug, diagnosticErrorName } from './debug.js';

const debug = createDebug('source-store');

/** @typedef {import('./presenter.js').LogicalSourceInput} LogicalSourceInput */

/**
 * `origin` records whether rows were handed over by an already rendered view
 * (already filtered by the presenter) or loaded directly from a query, which
 * still needs the view's own row filter applied.
 * `missing` records a query that resolved without rows because the dashboard
 * data does not carry that source, which is not a load failure.
 * @typedef {{ status: 'idle'|'loading'|'ready'|'missing'|'failed', origin: 'view'|'query', source: LogicalSourceInput | null }} SourceEntry
 */

/** @type {SourceEntry} */
const IDLE_ENTRY = { status: 'idle', origin: 'query', source: null };

/** @type {Map<string, import('./reactive.js').State<SourceEntry>>} */
const entries = new Map();
/** @typedef {{ pageId?: string, viewId?: string, sourceIndex?: number, bindingKey?: string, refreshViewSource?: boolean, routeParameters?: Record<string, string>, queryContext?: { filters?: Record<string, string[]>, search?: { fields: string[], query: string }, orderBy?: Array<{ field: string, direction?: 'asc'|'desc' }>, timeWindow?: { start?: string, end?: string }, viewMode?: 'chart'|'table'|'card' } }} SourceRequestOptions */

/** @type {Set<string>} */
const requested = new Set();
/** @type {Map<string, SourceRequestOptions>} */
const requestOptions = new Map();
/** @type {Map<string, string>} */
const requestKeys = new Map();
/** @type {Map<string, string>} */
const requestNames = new Map();
/**
 * Generation of the newest load started per source, so stale results are
 * dropped when queries resolve out of order.
 * @type {Map<string, number>}
 */
const generations = new Map();
/** @type {((name: string, options?: SourceRequestOptions) => Promise<LogicalSourceInput | undefined>) | null} */
let loadSource = null;

/**
 * @param {string} name
 * @returns {import('./reactive.js').State<SourceEntry>}
 */
export function sourceState(name) {
  let entry = entries.get(name);
  if (!entry) {
    entry = state(IDLE_ENTRY);
    entries.set(name, entry);
  }
  return entry;
}

/**
 * Publishes rows a rendered view already holds, so bound elements render them
 * without waiting for a query round trip.
 * @param {string} name
 * @param {LogicalSourceInput} source
 * @param {string} [bindingKey]
 */
export function publishSource(name, source, bindingKey = name) {
  sourceState(bindingKey).set({ status: 'ready', origin: 'view', source });
}

/**
 * Registers the loader used to resolve one named query at a time. Each source
 * is requested on its own so a slow query never delays a fast one.
 * @param {((name: string, options?: SourceRequestOptions) => Promise<LogicalSourceInput | undefined>) | null} loader
 */
export function configureSourceLoader(loader) {
  loadSource = loader;
}

/**
 * Requests one source asynchronously when a loader is configured. Repeated
 * requests for the same source reuse the first in-flight query.
 * @param {string} name
 * @param {SourceRequestOptions} [options]
 */
export function requestSource(name, options = {}) {
  if (!loadSource) return;
  const bindingKey = options.bindingKey ?? name;
  const key = JSON.stringify(options);
  if (requested.has(bindingKey) && requestKeys.get(bindingKey) === key) return;
  requested.add(bindingKey);
  requestNames.set(bindingKey, name);
  requestOptions.set(bindingKey, options);
  requestKeys.set(bindingKey, key);
  void loadRequestedSource(bindingKey);
}

/** Re-runs every previously requested query, for example after live data changes. */
export function refreshSources() {
  // Grouped so bound elements run once for the whole refresh rather than once
  // per source that flips to loading.
  batch(() => {
    for (const name of [...requested]) {
      void loadRequestedSource(name);
    }
  });
}

/** @param {string} bindingKey */
async function loadRequestedSource(bindingKey) {
  const loader = loadSource;
  if (!loader) return;
  const name = requestNames.get(bindingKey) ?? bindingKey;
  const entry = sourceState(bindingKey);
  // Rows handed over by a rendered view are refreshed by the next render, so a
  // query result must not silently replace them with unfiltered rows.
  const current = untracked(() => entry.get());
  if (current.origin === 'view'
      && current.status === 'ready'
      && requestOptions.get(bindingKey)?.refreshViewSource !== true) return;
  const generation = (generations.get(bindingKey) ?? 0) + 1;
  generations.set(bindingKey, generation);
  // Ready rows stay on screen while they reload, and a source already loading
  // is left alone so bound elements are not woken for an unchanged state.
  if (current.status !== 'ready' && current.status !== 'loading') {
    entry.set({ status: 'loading', origin: 'query', source: null });
  }
  try {
    const source = await loader(name, requestOptions.get(bindingKey));
    const stale = generations.get(bindingKey) !== generation;
    debug({ event: 'resolved', source: name, status: source ? 'ready' : 'missing', rows: source?.rows?.length ?? 0, stale });
    // A later load already started, so this result is stale.
    if (stale) return;
    entry.set(source
      ? { status: 'ready', origin: 'query', source }
      : { status: 'missing', origin: 'query', source: null });
  } catch (error) {
    const stale = generations.get(bindingKey) !== generation;
    debug({ event: 'failed', source: name, errorName: diagnosticErrorName(error), stale });
    if (stale) return;
    entry.set({ status: 'failed', origin: 'query', source: null });
  }
}

/** Releases every bound source and the configured loader. */
export function resetSourceStore() {
  entries.clear();
  requested.clear();
  requestOptions.clear();
  requestKeys.clear();
  requestNames.clear();
  generations.clear();
  loadSource = null;
}
