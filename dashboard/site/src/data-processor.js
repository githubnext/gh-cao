import { summarizeTableColumns } from './table-summary-data.js';
import { clusterScatterPoints } from './scatter-clustering.js';
import { resolveDashboardQuerySources } from './data/queries/declarative.js';
import { batch } from './reactive.js';
import { publishNotification, resolveNotification } from './notification-service.js';
import { browserFirstLoad, recordPreparationEvent } from './browser-first-load.js';
import { createDebug, diagnosticErrorName, withDebugParameter } from './debug.js';
import {
  queryRemoteDashboard,
  queryRemoteDiagnostics,
  queryRemoteRepositoryMemory,
  refreshRemoteDashboard,
  subscribeRemoteRevision,
  githubWorkerBackendConfiguration,
  usesRemoteDataBackend
} from './remote-data-backend.js';
import { githubResourcePath } from './data/github-service.js';
import { isGitHubQuerySource } from './data/github-query-sources.js';
import { invalidateGitHubResourceAuthentication } from './auth.js';
import { cliQueueAvailable } from './backend-mode.js';
import { DEMO_MODE } from './deployment-profile.js';

export { cliQueueAvailable } from './backend-mode.js';

/** Milliseconds a cooperative cancellation is given before the worker is terminated. */
const CANCELLATION_GRACE_MS = 250;

const debugDataProcessor = createDebug('data-processor');

/** @type {Worker | null} */
let worker = null;
let nextRequestId = 0;
/** @type {Map<number, { resolve: (value: unknown) => void, reject: (reason: Error) => void, cleanup: () => void, processor: Worker, githubEpoch?: number }>} */
const pending = new Map();
/**
 * @typedef {{ notify: (sources: Record<string, import('./presenter.js').LogicalSourceInput>) => void, onError?: (error: Error) => void, cleanup: () => void, frame: number | null }} SubscriptionListener
 */
/**
 * @typedef {{
 *   id: string,
 *   sourceNames: string[],
 *   context: { githubUrlBase?: string, dashboardRepository?: string | null, pages: unknown[], queries?: unknown[] },
 *   pageId?: string,
 *   viewId?: string,
 *   routeParameters?: Record<string, string>,
 *   queryContext?: import('./data/queries/view-payload-compiler.js').GlobalQueryContext,
 *   pagination?: Record<string, { limit: number, continuationToken?: string }>,
 *   listeners: Set<SubscriptionListener>,
 *   registeredWorker: Worker | null,
 *   latest: { sources: Record<string, import('./presenter.js').LogicalSourceInput>, revision: number | null, partial: boolean } | null,
 *   snapshot: Record<string, import('./presenter.js').LogicalSourceInput> | null,
 *   snapshotRevision: number | null,
 *   snapshotPartial: boolean,
 *   frame: number | null,
 *   emitCurrent: boolean,
 *   remoteStop?: () => void,
 *   remoteQueryRunning?: boolean,
 *   remoteRevision?: number | null,
 *   remoteHealthRevision?: number | null
 * }} ViewSubscription
 */
/** @type {Map<string, ViewSubscription>} */
const subscriptions = new Map();
/** @type {Map<string, { processor: Worker, resource: import('./data/github-service.js').GitHubResource, notify: (snapshot: import('./data/github-service.js').GitHubResourceSnapshot) => void, onError?: (error: Error) => void, stop: () => void }>} */
const githubSubscriptions = new Map();
let githubAuthenticationEpoch = 0;
globalThis.addEventListener?.('cao-authentication-change', (event) => {
  githubAuthenticationEpoch++;
  worker?.postMessage({ operation: 'invalidate-github-authentication', epoch: githubAuthenticationEpoch });
  for (const subscription of subscriptions.values()) {
    if (githubDashboardDependencies(subscription.sourceNames, subscription.context)) {
      subscription.latest = null;
      subscription.snapshot = null;
      subscription.snapshotRevision = null;
      for (const listener of subscription.listeners) cancelSubscriberFrame(listener);
      subscription.remoteStop?.();
      subscription.remoteStop = undefined;
    }
  }
  if (/** @type {CustomEvent<{ resume?: boolean }>} */ (event).detail?.resume !== true) return;
  queueMicrotask(() => {
    if (!worker) return;
    const configuration = githubWorkerBackendConfiguration();
    for (const [subscriptionId, subscription] of githubSubscriptions) {
      subscription.processor.postMessage({
        operation: 'subscribe-github-resource', subscriptionId, resource: subscription.resource, configuration
      });
    }
    for (const subscription of subscriptions.values()) {
      if (!githubDashboardDependencies(subscription.sourceNames, subscription.context)) continue;
      subscription.registeredWorker = null;
      registerSubscription(worker, subscription);
      if (usesRemoteDataBackend()) registerRemoteGitHubSubscription(worker, subscription);
    }
  });
});
globalThis.addEventListener?.('storage', (event) => {
  if ('key' in event && (event.key === 'cao-dashboard-access-token' || event.key === null)) {
    invalidateGitHubResourceAuthentication(true);
  }
});
globalThis.addEventListener?.('focus', () => {
  if (worker) githubWorkerBackendConfiguration();
});
/** @type {Map<string, ReturnType<typeof publishNotification>>} */
const workerNotificationHandles = new Map();
/** @type {Set<string>} */
const cancelledWorkerNotificationIds = new Set();
/** @type {Set<(state: { id: string, phase: 'start' | 'update' | 'complete', completed?: number, total?: number, stage?: 'files' | 'maintenance' | 'inventory' | 'queries' }) => void>} */
const workerLoadingProgressListeners = new Set();
/** @type {Set<string>} */
const workerLoadingProgressOperations = new Set();
/** @type {Set<(versions: { oldVersion: number, newVersion: number }) => void>} */
const databaseUpgradeListeners = new Set();
/** @type {Map<number, (event: Record<string, any>) => void>} */
const cliQueueOutputListeners = new Map();

/** @param {string[]} names @param {{ pages: unknown[], queries?: unknown[] }} context */
function usesCliQueueSources(names, context) {
  return resolveDashboardQuerySources(context.queries ?? [], names)
    .some((name) => name === 'cli-commands' || name === 'cli-command-summary' || name === 'cli-command-statuses');
}

/**
 * All command lifecycle and output traffic stays in the data worker.
 * @param {Record<string, unknown>} payload
 * @param {{ signal?: AbortSignal, onOutput?: (event: Record<string, any>) => void }} [options]
 * @returns {Promise<any>}
 */
export function requestCliQueue(payload, options = {}) {
  if (!cliQueueAvailable()) return Promise.reject(new Error('Command queue is unavailable in this dashboard.'));
  getWorker()?.postMessage({
    operation: 'configure-cli-queue', enabled: true,
    endpoint: new URL('./__cli_commands', globalThis.location.href).href
  });
  return Promise.resolve(processRequest({
    operation: 'cli-queue', ...payload, cliQueueOutput: options.onOutput
  }, () => { throw new Error('Command queue requires the data worker.'); }, false, options.signal));
}

/** @param {Record<string, unknown>} command @param {AbortSignal} [signal] */
export function enqueueCliCommand(command, signal) {
  return requestCliQueue({ action: 'enqueue', command }, { signal });
}

/** @param {string} commandId @param {(event: Record<string, any>) => void} onOutput @param {AbortSignal} [signal] @param {number} [after] */
export function attachCliCommand(commandId, onOutput, signal, after = 0) {
  return requestCliQueue({ action: 'attach', commandId, after }, { signal, onOutput });
}

/** @param {string} commandId @param {AbortSignal} [signal] */
export function cancelCliCommand(commandId, signal) {
  return requestCliQueue({ action: 'cancel', commandId }, { signal });
}

/** @param {string} commandId @param {AbortSignal} [signal] */
export function retryCliCommand(commandId, signal) {
  return requestCliQueue({ action: 'retry', commandId, idempotencyKey: crypto.randomUUID() }, { signal });
}

/**
 * Adds supported main-thread behavior to a serializable worker notification.
 * @param {Omit<Exclude<Parameters<typeof publishNotification>[0], string>, 'action' | 'actions'> & { actions?: Array<{ label?: unknown, operation?: unknown, placement?: unknown, requestId?: unknown }> }} notification
 * @param {string} id
 * @param {() => ReturnType<typeof publishNotification>} getHandle
 */
function attachWorkerNotificationActions(notification, id, getHandle) {
  const { actions, ...base } = notification;
  return {
    ...base,
    actions: (Array.isArray(actions) ? actions : []).flatMap((action) => {
      if (!action || typeof action !== 'object' || Array.isArray(action)) return [];
      const placement = action.placement === 'details'
        ? { placement: /** @type {'details'} */ ('details') }
        : {};
      if (action.operation === 'cancel-data-ingestion') {
        return [{
          label: typeof action.label === 'string' && action.label.trim() ? action.label : 'Cancel',
          ...placement,
          run: () => {
            if (typeof action.requestId !== 'number'
                || !cancelDataProcessingRequest(action.requestId)) return;
            getHandle().update({
              ...base,
              message: 'Data ingestion cancelled.',
              tone: 'warning',
              actions: [],
              dismissOnCollapse: true,
              duration: 0
            });
            if (browserFirstLoad.get().ingestion?.id === id) {
              browserFirstLoad.set((current) => ({
                ...current,
                ingestion: current.ingestion && { ...current.ingestion, message: 'Data ingestion cancelled.', actions: [] }
              }));
            }
            cancelledWorkerNotificationIds.add(id);
          }
        }];
      }
      if (action.operation === 'sync-dashboard-queries') {
        return [{
          label: typeof action.label === 'string' && action.label.trim() ? action.label : 'Sync queries',
          ...placement,
          run: () => {
            if (typeof action.requestId === 'number') {
              syncDashboardQueries(action.requestId);
            }
          }
        }];
      }
      return [];
    })
  };
}

/** @param {number} id */
function cancelDataProcessingRequest(id) {
  const request = pending.get(id);
  if (!request) return 0;
  request.processor.postMessage({ id: ++nextRequestId, operation: 'cancel-data-processing', ids: [id] });
  return 1;
}

/** @param {number} id */
function syncDashboardQueries(id) {
  const request = pending.get(id);
  if (!request) return;
  request.processor.postMessage({ operation: 'sync-dashboard-queries', requestId: id });
}

/** @param {{ id: string, phase: 'start' | 'update' | 'complete', completed?: number, total?: number, stage?: 'files' | 'maintenance' | 'inventory' | 'queries' }} state */
function emitWorkerLoadingProgress(state) {
  for (const listener of workerLoadingProgressListeners) {
    try {
      listener(state);
    } catch {
      // Ignore listener failures so a broken consumer cannot interrupt other subscribers.
    }
  }
}

/**
 * Subscribes to worker-owned loading progress for the active top progress bar.
 * @param {(state: { id: string, phase: 'start' | 'update' | 'complete', completed?: number, total?: number, stage?: 'files' | 'maintenance' | 'inventory' | 'queries' }) => void} listener
 * @returns {() => void}
 */
export function subscribeWorkerLoadingProgress(listener) {
  if (typeof listener !== 'function') {
    throw new TypeError('Worker loading progress subscribers require a listener.');
  }
  workerLoadingProgressListeners.add(listener);
  return () => workerLoadingProgressListeners.delete(listener);
}

/** @param {(versions: { oldVersion: number, newVersion: number }) => void} listener */
export function subscribeDatabaseUpgrade(listener) {
  databaseUpgradeListeners.add(listener);
  return () => databaseUpgradeListeners.delete(listener);
}

/**
 * Subscribes through the data worker in both hosted and static modes. Backend
 * credentials never enter resource results or persistent storage.
 * @param {import('./data/github-service.js').GitHubResource} resource
 * @param {(snapshot: import('./data/github-service.js').GitHubResourceSnapshot) => void} notify
 * @param {{ signal: AbortSignal, onError?: (error: Error) => void }} options
 */
export function subscribeGitHubResource(resource, notify, { signal, onError }) {
  githubResourcePath(resource);
  if (typeof notify !== 'function') throw new TypeError('A GitHub resource listener is required.');
  if (signal.aborted) return () => {};
  const configuration = githubWorkerBackendConfiguration();
  const processor = getWorker();
  if (!processor) throw new Error('GitHub resources require a data worker.');
  const subscriptionId = `github-resource-${++nextRequestId}`;
  const stop = () => {
    signal.removeEventListener('abort', stop);
    if (!githubSubscriptions.delete(subscriptionId)) return;
    processor.postMessage({ operation: 'unsubscribe-github-resource', subscriptionId });
  };
  githubSubscriptions.set(subscriptionId, { processor, resource, notify, onError, stop });
  signal.addEventListener('abort', stop, { once: true });
  processor.postMessage({
    operation: 'subscribe-github-resource', subscriptionId, resource,
    configuration
  });
  return stop;
}

/**
 * Cancels every in-flight data-worker request. The worker is first asked to
 * stop cooperatively; if it does not acknowledge within the grace period it is
 * blocked in a runaway computation and is terminated instead. A terminated
 * worker is recreated on the next request.
 *
 * @param {string} [reason]
 * @returns {number} the number of requests that were cancelled
 */
export function cancelDataProcessing(reason = 'Data processing was cancelled.') {
  const ids = [...pending.keys()];
  if (!ids.length) return 0;
  const processor = worker;
  if (processor) processor.postMessage({ id: ++nextRequestId, operation: 'cancel-data-processing', ids });
  const cancellation = new Error(reason);
  cancellation.name = 'DataProcessingCancelledError';
  if (processor) scheduleForcedCancellation(processor, ids, cancellation);
  debugDataProcessor({ event: 'cancel-requested', cancelledCount: ids.length });
  return ids.length;
}

/** @param {Worker} processor @param {number[]} ids @param {Error} cancellation */
function scheduleForcedCancellation(processor, ids, cancellation) {
  setTimeout(() => {
    if (!ids.some((id) => pending.has(id))) return;
    rejectWorkerRequests(processor, cancellation);
    resetWorker(processor);
  }, CANCELLATION_GRACE_MS);
}

/** @param {Worker} processor @param {Error} error */
function rejectWorkerRequests(processor, error) {
  for (const [id, request] of pending) {
    if (request.processor !== processor) continue;
    request.cleanup();
    request.reject(error);
    pending.delete(id);
  }
}

/**
 * Runs a serializable row pipeline exclusively in the data worker.
 * There is no main-thread fallback: row operations either run in the worker or fail.
 * @param {Array<Record<string, unknown>>} data
 * @param {import('./data-operations.js').DataOperator[]} operators
 * @returns {Promise<Array<Record<string, unknown>>>}
 */
export function processRows(data, operators) {
  return /** @type {Promise<Array<Record<string, unknown>>>} */ (processRequest(
    { data, operators },
    () => Promise.reject(new Error('Row operations require a data worker.')),
    false
  ));
}

/**
 * Computes serializable table summaries in a Web Worker when supported.
 * @param {import('./table-summary-data.js').TableSummaryColumn[]} columns
 * @returns {import('./table-summary-data.js').TableColumnSummary[]|Promise<import('./table-summary-data.js').TableColumnSummary[]>}
 */
export function processTableSummaries(columns) {
  return processRequest(
    { operation: 'summarize-table-columns', columns },
    () => summarizeTableColumns(columns)
  );
}

/**
 * @param {import('./scatter-clustering.js').ScatterPoint[]} points
 * @param {number} limit
 * @returns {import('./scatter-clustering.js').ScatterPoint[]|Promise<import('./scatter-clustering.js').ScatterPoint[]>}
 */
export function processScatterPoints(points, limit) {
  return processRequest(
    { operation: 'cluster-scatter-points', data: points, limit },
    () => clusterScatterPoints(points, limit)
  );
}

/**
 * Executes declarative dashboard queries exclusively in the data worker.
 * There is no main-thread fallback: queries either run in the worker or fail.
 * @param {unknown[]} queries
 * @param {Record<string, import('./presenter.js').LogicalSourceInput>} sources
 * @param {{ signal?: AbortSignal, pagination?: Record<string, { limit: number, continuationToken?: string }>, sourceNames?: string[] }} [options] cancels the request from outside
 * @returns {Promise<Record<string, import('./presenter.js').LogicalSourceInput>>}
 */
export function processDashboardQueries(queries, sources, options = {}) {
  return /** @type {Promise<Record<string, import('./presenter.js').LogicalSourceInput>>} */ (processRequest(
    { operation: 'execute-dashboard-queries', queries, sources, sourceNames: options.sourceNames, pagination: options.pagination },
    () => Promise.reject(new Error('Declarative dashboard queries require a data worker.')),
    false,
    options.signal
  ));
}

/**
 * @param {import('./components/site-callout.js').SiteCallout[]} callouts
 * @param {Record<string, import('./presenter.js').LogicalSourceInput>} sources
 * @param {AbortSignal} signal
 * @returns {Promise<import('./data/queries/callouts.js').CalloutVisibility[]>}
 */
export function processDashboardCallouts(callouts, sources, signal) {
  return /** @type {Promise<import('./data/queries/callouts.js').CalloutVisibility[]>} */ (processRequest(
    { operation: 'query-dashboard-callouts', callouts, sources },
    () => Promise.reject(new Error('Dashboard callout queries require a data worker.')),
    false,
    signal
  ));
}

/**
 * Ingests optional published rows and executes their declarative database
 * projections entirely in the data worker.
 * @param {Record<string, unknown>} sources
 * @param {{ ingest?: boolean, sourceNames?: string[], queries?: unknown[] }} [options]
 */
export function loadDashboardQuerySources(sources, options = {}) {
  return /** @type {Promise<Record<string, import('./presenter.js').LogicalSourceInput>>} */ (processRequest(
    { operation: 'load-dashboard-query-sources', sources, ...options },
    () => Promise.reject(new Error('Dashboard database queries require a data worker.')),
    false
  ));
}

/**
 * Queries a bounded canonical-database consistency summary exclusively in the
 * data worker.
 */
export function queryCanonicalDatabaseDiagnostics() {
  if (usesRemoteDataBackend()) return queryRemoteDiagnostics();
  return /** @type {Promise<import('./diagnostics.js').DatabaseDiagnostics>} */ (processRequest(
    { operation: 'query-canonical-database-diagnostics' },
    () => Promise.reject(new Error('Database diagnostics require a data worker.')),
    false
  ));
}

/**
 * Lists one campaign's published repository-memory files.
 * @param {string} campaign
 * @param {AbortSignal} [signal]
 */
export function listRepositoryMemory(campaign, signal) {
  if (usesRemoteDataBackend()) return queryRemoteRepositoryMemory(campaign, undefined, signal);
  return processRequest(
    {
      operation: 'query-repository-memory',
      action: 'list',
      campaign,
      memoryRoot: new URL(DEMO_MODE ? './demo-data/memory/' : './memory/', document.baseURI).href,
    },
    () => Promise.reject(new Error('Repository memory requires a data worker.')),
    false,
    signal
  );
}

/**
 * Lists registered campaigns' repository-memory files with one static manifest request.
 * @param {string[]} campaigns
 * @param {AbortSignal} [signal]
 * @returns {Promise<Array<{ branch: string, commit: string, files: Array<{path: string, oid: string, size: number}>, omitted: {fileLimit: number, fileSize: number, totalSize: number, extension: number, nesting: number, unsafePath: number, invalidContent: number, unsupportedType: number} } | null | {error: unknown}>>}
 */
export function listRepositoryMemoryCampaigns(campaigns, signal) {
  if (usesRemoteDataBackend()) {
    return Promise.all(campaigns.map((campaign) => queryRemoteRepositoryMemory(campaign, undefined, signal)
      .catch((error) => ({ error }))));
  }
  return /** @type {Promise<Array<{ branch: string, commit: string, files: Array<{path: string, oid: string, size: number}>, omitted: {fileLimit: number, fileSize: number, totalSize: number, extension: number, nesting: number, unsafePath: number, invalidContent: number, unsupportedType: number} } | null>>} */ (processRequest(
    {
      operation: 'query-repository-memory',
      action: 'list-campaigns',
      campaigns,
      memoryRoot: new URL(DEMO_MODE ? './demo-data/memory/' : './memory/', document.baseURI).href,
    },
    () => Promise.reject(new Error('Repository memory requires a data worker.')),
    false,
    signal
  ));
}

/**
 * Reads one published repository-memory file.
 * @param {string} campaign
 * @param {string} path
 * @param {AbortSignal} [signal]
 */
export function readRepositoryMemoryFile(campaign, path, signal) {
  if (usesRemoteDataBackend()) return queryRemoteRepositoryMemory(campaign, path, signal);
  return processRequest(
    {
      operation: 'query-repository-memory',
      action: 'content',
      campaign,
      path,
      memoryRoot: new URL(DEMO_MODE ? './demo-data/memory/' : './memory/', document.baseURI).href,
    },
    () => Promise.reject(new Error('Repository memory requires a data worker.')),
    false,
    signal
  );
}

/**
 * Prepares Raw and Table memory-file payloads exclusively in the data worker.
 * @param {string} path
 * @param {string} content
 * @param {AbortSignal} [signal]
 * @returns {Promise<import('./data/repository-memory-jsonl.js').MemoryFileContent>}
 */
export function processRepositoryMemoryFile(path, content, signal) {
  return /** @type {Promise<import('./data/repository-memory-jsonl.js').MemoryFileContent>} */ (processRequest(
    { operation: 'prepare-repository-memory-file', path, content },
    () => Promise.reject(new Error('Memory file formatting requires a data worker.')),
    false,
    signal
  ));
}

/**
 * Loads and hydrates the live canonical dashboard entirely in the data worker.
 * The main thread sends only a URL and receives the query projection needed by
 * the renderer.
 * @param {string} sourceUrl
 * @param {string[]} sourceNames
 * @param {{ githubUrlBase?: string, dashboardRepository?: string | null, pages: unknown[], queries?: unknown[] }} context
 * @param {Record<string, { limit: number, continuationToken?: string }>} [pagination]
 * @param {{ pageId?: string, viewId?: string, routeParameters?: Record<string, string>, queryContext?: ViewSubscription['queryContext'] }} [options]
 * @returns {Promise<Record<string, import('./presenter.js').LogicalSourceInput>>}
 */
export function loadCanonicalDashboardSources(sourceUrl, sourceNames, context, pagination, options = {}) {
  if (usesCliQueueSources(sourceNames, context)) return loadCanonicalDashboardPage(sourceNames, context, pagination, options);
  if (githubDashboardDependencies(sourceNames, context)) {
    if (usesRemoteDataBackend()) return loadCanonicalDashboardPage(sourceNames, context, pagination, options);
    return loadCanonicalDashboardSources(sourceUrl, [], context, undefined, {})
      .then(() => loadCanonicalDashboardPage(sourceNames, context, pagination, options));
  }
  if (usesRemoteDataBackend()) {
    return queryRemoteDashboard(sourceNames, context, pagination, options).then(({ sources }) => sources);
  }
  return /** @type {Promise<Record<string, import('./presenter.js').LogicalSourceInput>>} */ (processRequest(
    { operation: 'load-canonical-dashboard', sourceUrl, sourceNames, context, pagination, ...options },
    () => Promise.reject(new Error('Live canonical dashboard loading requires a data worker.')),
    false
  ));
}

/**
 * Refreshes the canonical dashboard and reports whether fresh data was written.
 * @param {string} sourceUrl
 * @param {string[]} sourceNames
 * @param {{ githubUrlBase?: string, dashboardRepository?: string | null, pages: unknown[], queries?: unknown[] }} context
 * @param {Record<string, { limit: number, continuationToken?: string }>} [pagination]
 * @param {{ pageId?: string, viewId?: string, routeParameters?: Record<string, string>, queryContext?: ViewSubscription['queryContext'] }} [options]
 * @returns {Promise<{ sources: Record<string, import('./presenter.js').LogicalSourceInput>, changed: boolean }>}
 */
export function refreshCanonicalDashboardSources(sourceUrl, sourceNames, context, pagination, options = {}) {
  const githubDependencies = githubDashboardDependencies(sourceNames, context);
  if (githubDependencies) {
    return (async () => {
      const refreshed = usesRemoteDataBackend()
        ? githubDependencies.length
          ? await refreshRemoteDashboard(githubDependencies, { ...context, pages: [], queries: [] })
          : { changed: false }
        : await refreshCanonicalDashboardSources(sourceUrl, [], context);
      return { sources: await loadCanonicalDashboardPage(sourceNames, context, pagination, options), changed: refreshed.changed };
    })();
  }
  if (usesRemoteDataBackend()) {
    return refreshRemoteDashboard(sourceNames, context, pagination, options);
  }
  return /** @type {Promise<{ sources: Record<string, import('./presenter.js').LogicalSourceInput>, changed: boolean }>} */ (processRequest(
    { operation: 'load-canonical-dashboard', sourceUrl, sourceNames, context, pagination, reportActivation: true, ...options },
    () => Promise.reject(new Error('Live canonical dashboard refresh requires a data worker.')),
    false
  ));
}

/**
 * Queries one page from the live canonical dashboard retained by the worker.
 * @param {string[]} sourceNames
 * @param {{ githubUrlBase?: string, dashboardRepository?: string | null, pages: unknown[], queries?: unknown[] }} context
 * @param {Record<string, { limit: number, continuationToken?: string }>} [pagination]
 * @param {{ pageId?: string, viewId?: string, routeParameters?: Record<string, string>, queryContext?: ViewSubscription['queryContext'], signal?: AbortSignal }} [options]
 * @returns {Promise<Record<string, import('./presenter.js').LogicalSourceInput>>}
 */
export function loadCanonicalDashboardPage(sourceNames, context, pagination, options = {}) {
  const { signal, ...queryOptions } = options;
  const githubDependencies = githubDashboardDependencies(sourceNames, context);
  if (githubDependencies) {
    return (async () => {
      const hostedSources = usesRemoteDataBackend()
        ? githubDependencies.length
          ? (await queryRemoteDashboard(githubDependencies, { ...context, pages: [], queries: [] }, undefined, { signal })).sources
          : {}
        : undefined;
      return /** @type {Promise<Record<string, import('./presenter.js').LogicalSourceInput>>} */ (processRequest(
        {
          operation: 'query-canonical-dashboard', sourceNames, context, pagination, ...queryOptions,
          githubConfiguration: githubWorkerBackendConfiguration(), hostedSources
        },
        () => Promise.reject(new Error('GitHub dashboard queries require a data worker.')), false, signal
      ));
    })();
  }
  if (usesRemoteDataBackend() && !usesCliQueueSources(sourceNames, context)) {
    return queryRemoteDashboard(sourceNames, context, pagination, options).then(({ sources }) => sources);
  }
  return /** @type {Promise<Record<string, import('./presenter.js').LogicalSourceInput>>} */ (processRequest(
    { operation: 'query-canonical-dashboard', sourceNames, context, pagination, ...queryOptions },
    () => Promise.reject(new Error('Live canonical dashboard queries require a data worker.')),
    false,
    signal
  ));
}

/**
 * Validates editor YAML/JSON in the worker with no main-thread fallback.
 * @param {string} source
 * @param {AbortSignal} [signal]
 * @returns {Promise<import('./data/query-editor.js').QueryEditorValidation>}
 */
export function validateQueryEditorDocument(source, signal) {
  return /** @type {Promise<import('./data/query-editor.js').QueryEditorValidation>} */ (processRequest(
    { operation: 'validate-query-editor-document', content: source },
    () => Promise.reject(new Error('Query editor validation requires a data worker.')),
    false,
    signal
  ));
}

/**
 * Reads the timestamp of the last fully committed browser snapshot.
 * @returns {Promise<{ createdAt: string } | null>}
 */
export function loadDashboardSnapshotMetadata() {
  if (usesRemoteDataBackend()) return Promise.resolve(null);
  return /** @type {Promise<{ createdAt: string } | null>} */ (processRequest(
    { operation: 'read-dashboard-snapshot' },
    () => null,
    false
  ));
}

/**
 * Subscribes UI code to the latest rows for one dashboard view. Multiple
 * listeners for the same view share one worker subscription, and rapid worker
 * updates collapse into one notification containing the newest tables.
 *
 * @param {string} viewId
 * @param {string[]} sourceNames
 * @param {{ githubUrlBase?: string, dashboardRepository?: string | null, pages: unknown[], queries?: unknown[] }} context
 * @param {(sources: Record<string, import('./presenter.js').LogicalSourceInput>) => void} listener
 * @param {Record<string, { limit: number, continuationToken?: string }>} [pagination]
 * @param {{ signal?: AbortSignal, onError?: (error: Error) => void, emitCurrent?: boolean, pageId?: string, viewId?: string, routeParameters?: Record<string, string>, queryContext?: ViewSubscription['queryContext'] }} [options]
 * @returns {() => void}
 */
export function subscribeCanonicalDashboardView(viewId, sourceNames, context, listener, pagination, options = {}) {
  if (typeof viewId !== 'string' || !viewId.trim()) {
    throw new TypeError('Canonical dashboard subscriptions require a view identifier.');
  }
  if (!Array.isArray(sourceNames) || sourceNames.some((name) => typeof name !== 'string')) {
    throw new TypeError('Canonical dashboard subscription source names must be an array of strings.');
  }
  if (typeof listener !== 'function') {
    throw new TypeError('Canonical dashboard subscriptions require a listener.');
  }
  if (options.signal?.aborted) return () => {};
  let subscription = subscriptions.get(viewId);
  if (subscription) {
    if (!sameSubscription(subscription, sourceNames, context, pagination, options)) {
      throw new Error(`Canonical dashboard view ${viewId} is already subscribed with different query parameters.`);
    }
  } else {
    subscription = {
      id: viewId,
      sourceNames: [...sourceNames],
      context,
      pageId: options.pageId,
      viewId: options.viewId,
      routeParameters: options.routeParameters,
      queryContext: options.queryContext,
      pagination,
      listeners: new Set(),
      registeredWorker: null,
      latest: null,
      snapshot: null,
      snapshotRevision: null,
      snapshotPartial: false,
      frame: null,
      emitCurrent: options.emitCurrent !== false
    };
    subscriptions.set(viewId, subscription);
  }
  const listenerEntry = {
    notify: listener,
    onError: options.onError,
    cleanup: () => options.signal?.removeEventListener('abort', unsubscribeFromSignal),
    frame: null
  };
  subscription.listeners.add(listenerEntry);
  if (subscription.snapshot && subscription.frame === null) {
    const snapshot = subscription.snapshot;
    invokeSubscriber(() => listener(snapshot));
  }
  const unsubscribeFromSignal = () => unsubscribe();
  options.signal?.addEventListener('abort', unsubscribeFromSignal, { once: true });
  if (githubDashboardDependencies(sourceNames, context)) {
    const processor = getWorker();
    if (processor) {
      registerSubscription(processor, subscription);
      if (usesRemoteDataBackend()) registerRemoteGitHubSubscription(processor, subscription);
    }
  } else if (usesRemoteDataBackend() && !usesCliQueueSources(sourceNames, context)) {
    registerRemoteSubscription(subscription);
  } else {
    const processor = getWorker();
    if (processor) registerSubscription(processor, subscription);
  }

  let active = true;
  const unsubscribe = () => {
    if (!active) return;
    active = false;
    listenerEntry.cleanup();
    cancelSubscriberFrame(listenerEntry);
    const current = subscriptions.get(viewId);
    if (!current) return;
    current.listeners.delete(listenerEntry);
    if (current.listeners.size > 0) return;
    subscriptions.delete(viewId);
    current.latest = null;
    current.snapshot = null;
    current.snapshotRevision = null;
    current.snapshotPartial = false;
    if (current.frame !== null && typeof globalThis.cancelAnimationFrame === 'function') {
      globalThis.cancelAnimationFrame(current.frame);
    }
    current.frame = null;
    current.remoteStop?.();
    current.remoteStop = undefined;
    current.registeredWorker?.postMessage({
      operation: 'unsubscribe-canonical-dashboard',
      subscriptionId: viewId
    });
    current.registeredWorker = null;
  };
  return unsubscribe;
}

/**
 * Returns only unresolved non-GitHub inputs, never computes source rows.
 * @param {string[]} names
 * @param {{ queries?: unknown[] }} context
 */
function githubDashboardDependencies(names, context) {
  const queries = context.queries ?? [];
  const resolved = resolveDashboardQuerySources(queries, names);
  if (!resolved.some(isGitHubQuerySource)) return null;
  const queryNames = new Set(queries.flatMap((query) => (
    query && typeof query === 'object' && 'name' in query ? [query.name] : []
  )));
  return resolved.filter((name) => !queryNames.has(name) && !isGitHubQuerySource(name));
}

/** @param {Worker} processor @param {ViewSubscription} subscription */
function registerRemoteGitHubSubscription(processor, subscription) {
  if (subscription.remoteStop) return;
  const names = githubDashboardDependencies(subscription.sourceNames, subscription.context) ?? [];
  if (!names.length) return;
  const controller = new AbortController();
  let running = false;
  let queued = false;
  const refresh = async () => {
    if (controller.signal.aborted) return;
    if (running) { queued = true; return; }
    running = true;
    try {
      const result = await queryRemoteDashboard(names, { ...subscription.context, pages: [], queries: [] }, undefined, { signal: controller.signal });
      if (!controller.signal.aborted) processor.postMessage({
        operation: 'update-github-dashboard-sources', subscriptionId: subscription.id, sources: result.sources
      });
    } catch (error) {
      if (!controller.signal.aborted) for (const listener of subscription.listeners) {
        invokeSubscriber(() => listener.onError?.(error instanceof Error ? error : new Error('Remote dashboard sources unavailable.')));
      }
    } finally {
      running = false;
      if (queued) { queued = false; void refresh(); }
    }
  };
  const stopRevision = subscribeRemoteRevision(() => { void refresh(); });
  subscription.remoteStop = () => { controller.abort(); stopRevision(); };
  void refresh();
}

/** @param {ViewSubscription} subscription */
function registerRemoteSubscription(subscription) {
  if (subscription.remoteStop) return;
  let active = true;
  const controller = new AbortController();
  /** @type {{ revision: number, healthRevision: number | null } | null} */
  let queuedUpdate = null;
  const query = async (refreshDecayingLoad = false) => {
    if (!active || subscription.remoteQueryRunning) return;
    subscription.remoteQueryRunning = true;
    const startedAt = performance.now();
    debugDataProcessor({
      event: 'remote-subscription-query-started',
      sourceCount: subscription.sourceNames.length,
      refreshDecayingLoad
    });
    try {
      const result = await queryRemoteDashboard(
        subscription.sourceNames,
        subscription.context,
        subscription.pagination,
        {
          signal: controller.signal,
          pageId: subscription.pageId,
          viewId: subscription.viewId,
          routeParameters: subscription.routeParameters,
          queryContext: subscription.queryContext
        }
      );
      if (!active || subscriptions.get(subscription.id) !== subscription) return;
      subscription.remoteRevision = result.revision;
      subscription.remoteHealthRevision = result.healthRevision;
      debugDataProcessor({
        event: 'remote-subscription-query-completed',
        durationMs: Math.round(performance.now() - startedAt),
        revision: result.revision,
        healthRevision: result.healthRevision
      });
      enqueueSubscriptionUpdate(subscription, result.sources, refreshDecayingLoad ? null : result.revision);
    } catch (error) {
      if (!active) return;
      debugDataProcessor({
        event: 'remote-subscription-query-failed',
        durationMs: Math.round(performance.now() - startedAt),
        errorName: diagnosticErrorName(error)
      });
      const failure = error instanceof Error ? error : new Error(String(error));
      for (const listener of [...subscription.listeners]) {
        if (subscription.listeners.has(listener) && listener.onError) {
          invokeSubscriber(() => listener.onError?.(failure));
        }
      }
    } finally {
      subscription.remoteQueryRunning = false;
      if (queuedUpdate && (
        queuedUpdate.revision !== subscription.remoteRevision
        || queuedUpdate.healthRevision !== subscription.remoteHealthRevision
      )) {
        queuedUpdate = null;
        void query();
      }
    }
  };
  const stopEvents = subscribeRemoteRevision((revision, healthRevision) => {
    if (revision === subscription.remoteRevision
        && healthRevision === subscription.remoteHealthRevision) return;
    queuedUpdate = { revision, healthRevision };
    void query();
  }, (error) => {
    if (!active) return;
    for (const listener of [...subscription.listeners]) {
      if (subscription.listeners.has(listener) && listener.onError) {
        invokeSubscriber(() => listener.onError?.(error));
      }
    }
  });
  const refreshesRuntime = resolveDashboardQuerySources(
    subscription.context.queries ?? [], subscription.sourceNames
  ).some((source) => source === 'collection-health' || source === 'webhook-status');
  const loadTimer = refreshesRuntime ? setInterval(() => { void query(true); }, 30_000) : null;
  subscription.remoteStop = () => {
    active = false;
    if (loadTimer !== null) clearInterval(loadTimer);
    stopEvents();
    controller.abort();
  };
  if (subscription.emitCurrent) void query();
}

/** @param {ViewSubscription} subscription @param {string[]} sourceNames @param {ViewSubscription['context']} context @param {ViewSubscription['pagination']} pagination @param {{ pageId?: string, viewId?: string, routeParameters?: Record<string, string>, queryContext?: ViewSubscription['queryContext'] }} [options] */
function sameSubscription(subscription, sourceNames, context, pagination, options = {}) {
  return sameContext(subscription.context, context)
    && samePagination(subscription.pagination, pagination)
    && sameStringMap(subscription.routeParameters, options.routeParameters)
    && sameQueryContext(subscription.queryContext, options.queryContext)
    && subscription.pageId === options.pageId
    && subscription.viewId === options.viewId
    && subscription.sourceNames.length === sourceNames.length
    && subscription.sourceNames.every((name, index) => name === sourceNames[index]);
}

/** @param {Record<string, string> | undefined} left @param {Record<string, string> | undefined} right */
function sameStringMap(left, right) {
  if (left === right) return true;
  const leftEntries = Object.entries(left ?? {});
  const rightEntries = Object.entries(right ?? {});
  return leftEntries.length === rightEntries.length
    && leftEntries.every(([key, value]) => right?.[key] === value);
}

/** @param {ViewSubscription['queryContext']} left @param {ViewSubscription['queryContext']} right */
function sameQueryContext(left, right) {
  if (left === right) return true;
  const leftFilters = left?.filters ?? {};
  const rightFilters = right?.filters ?? {};
  const sameFilters = Object.keys(leftFilters).length === Object.keys(rightFilters).length
    && Object.entries(leftFilters).every(([field, values]) => {
      const candidate = rightFilters[field] ?? [];
      return candidate.length === values.length
        && candidate.every((value, index) => value === values[index]);
    });
  if (!sameFilters) return false;
  const sameSearch = left?.search?.query === right?.search?.query
    && (left?.search?.fields ?? []).length === (right?.search?.fields ?? []).length
    && (left?.search?.fields ?? []).every((field, index) => field === right?.search?.fields[index]);
  const sameOrder = (left?.orderBy ?? []).length === (right?.orderBy ?? []).length
    && (left?.orderBy ?? []).every((ordering, index) => ordering.field === right?.orderBy?.[index]?.field
      && ordering.direction === right?.orderBy?.[index]?.direction);
  return sameSearch
    && sameOrder
    && left?.timeWindow?.start === right?.timeWindow?.start
    && left?.timeWindow?.end === right?.timeWindow?.end
    && left?.viewMode === right?.viewMode
    && JSON.stringify(left?.viewFilters ?? {}) === JSON.stringify(right?.viewFilters ?? {})
    && sameScalarMap(left?.formValues, right?.formValues);
}

/** @param {Record<string, string|number|boolean> | undefined} left @param {Record<string, string|number|boolean> | undefined} right */
function sameScalarMap(left, right) {
  if (left === right) return true;
  const leftEntries = Object.entries(left ?? {});
  const rightEntries = Object.entries(right ?? {});
  return leftEntries.length === rightEntries.length
    && leftEntries.every(([key, value]) => right?.[key] === value);
}

/** @param {ViewSubscription['context']} left @param {ViewSubscription['context']} right */
function sameContext(left, right) {
  return left === right || (
    left.githubUrlBase === right.githubUrlBase
    && left.dashboardRepository === right.dashboardRepository
    && left.pages === right.pages
    && left.queries === right.queries
  );
}

/** @param {ViewSubscription['pagination']} left @param {ViewSubscription['pagination']} right */
function samePagination(left, right) {
  if (left === right) return true;
  const leftEntries = Object.entries(left ?? {});
  const rightEntries = Object.entries(right ?? {});
  return leftEntries.length === rightEntries.length && leftEntries.every(([source, request]) => {
    const candidate = right?.[source];
    return candidate?.limit === request.limit
      && candidate?.continuationToken === request.continuationToken;
  });
}

/** @param {Worker} processor @param {ViewSubscription} subscription */
function registerSubscription(processor, subscription) {
  if (subscription.registeredWorker === processor) return;
  subscription.registeredWorker = processor;
  processor.postMessage({
    operation: 'subscribe-canonical-dashboard',
    subscriptionId: subscription.id,
    sourceNames: subscription.sourceNames,
    context: subscription.context,
    pageId: subscription.pageId,
    viewId: subscription.viewId,
    routeParameters: subscription.routeParameters,
    queryContext: subscription.queryContext,
    pagination: subscription.pagination,
    emitCurrent: subscription.emitCurrent,
    ...(githubDashboardDependencies(subscription.sourceNames, subscription.context) ? {
      githubConfiguration: githubWorkerBackendConfiguration(),
      hostedSources: usesRemoteDataBackend() ? {} : undefined
    } : {})
  });
}

/**
 * Retains only the newest worker payload until the current microtask completes.
 * @param {ViewSubscription} subscription
 * @param {Record<string, import('./presenter.js').LogicalSourceInput>} sources
 * @param {unknown} revision
 * @param {boolean} [partial]
 */
function enqueueSubscriptionUpdate(subscription, sources, revision, partial = false) {
  if (Number.isSafeInteger(revision)
      && subscription.snapshot
      && subscription.snapshotRevision === revision
      && (partial || !subscription.snapshotPartial)) return;
  subscription.latest = { sources, revision: Number.isSafeInteger(revision) ? Number(revision) : null, partial };
  for (const listener of subscription.listeners) cancelSubscriberFrame(listener);
  if (subscription.frame !== null) return;
  const flush = () => {
    subscription.frame = null;
    const latest = subscription.latest;
    subscription.latest = null;
    if (!latest || subscriptions.get(subscription.id) !== subscription) return;
    subscription.snapshot = latest.sources;
    subscription.snapshotRevision = latest.revision;
    subscription.snapshotPartial = latest.partial;
    batch(() => {
      for (const listener of [...subscription.listeners]) {
        if (subscription.listeners.has(listener)) invokeSubscriber(() => listener.notify(latest.sources));
      }
    });
  };
  if (typeof globalThis.requestAnimationFrame === 'function') {
    subscription.frame = globalThis.requestAnimationFrame(flush);
  } else {
    subscription.frame = 0;
    queueMicrotask(flush);
  }
}

/** @param {SubscriptionListener} listener */
function cancelSubscriberFrame(listener) {
  if (listener.frame !== null && typeof globalThis.cancelAnimationFrame === 'function') {
    globalThis.cancelAnimationFrame(listener.frame);
  }
  listener.frame = null;
}

/**
 * @template T
 * @param {Record<string, unknown>} request
 * @param {() => T} fallback
 * @param {boolean} [recoverWorkerError]
 * @param {AbortSignal} [signal]
 * @returns {T|Promise<T>}
 */
function processRequest(request, fallback, recoverWorkerError = true, signal) {
  if (signal?.aborted) {
    const cancellation = new Error('Data processing was cancelled.');
    cancellation.name = 'DataProcessingCancelledError';
    return Promise.reject(cancellation);
  }
  const processor = getWorker();
  if (!processor) return fallback();
  const id = ++nextRequestId;
  const result = new Promise((resolve, reject) => {
    const onAbort = () => {
      if (!pending.has(id)) return;
      processor.postMessage({ id: ++nextRequestId, operation: 'cancel-data-processing', ids: [id] });
    };
    pending.set(id, {
      resolve: (value) => resolve(/** @type {T} */ (value)),
      reject,
      cleanup: () => {
        signal?.removeEventListener('abort', onAbort);
        cliQueueOutputListeners.delete(id);
      },
      processor,
      githubEpoch: 'githubConfiguration' in request ? githubAuthenticationEpoch : undefined
    });
    const { cliQueueOutput, ...serializable } = request;
    if (typeof cliQueueOutput === 'function') cliQueueOutputListeners.set(id, /** @type {(event: Record<string, any>) => void} */ (cliQueueOutput));
    processor.postMessage({ id, ...serializable });
    if (signal?.aborted) onAbort();
    else signal?.addEventListener('abort', onAbort, { once: true });
  });
  return recoverWorkerError ? result.catch(fallback) : result;
}

/** @returns {Worker | null} */
function getWorker() {
  if (worker) return worker;
  if (typeof Worker === 'undefined' || import.meta.url.startsWith('data:')) return null;
  worker = new Worker(withDebugParameter(new URL('./data-worker.js', import.meta.url)), { type: 'module' });
  const processor = worker;
  processor.postMessage({
    operation: 'configure-cli-queue', enabled: cliQueueAvailable(),
    endpoint: new URL('./__cli_commands', globalThis.location.href).href
  });
  debugDataProcessor({ event: 'worker-created' });
  worker.addEventListener('message', (event) => {
    if (event.data?.type === 'cli-queue-output') {
      cliQueueOutputListeners.get(event.data.id)?.(event.data.event);
      return;
    }
    if (event.data?.type === 'github-resource') {
      if ((event.data.githubEpoch ?? 0) !== githubAuthenticationEpoch) return;
      const subscription = githubSubscriptions.get(event.data.subscriptionId);
      if (subscription?.processor !== processor) return;
      if (event.data.snapshot) invokeSubscriber(() => subscription.notify(event.data.snapshot));
      else if (typeof event.data.error === 'string') {
        subscription.stop();
        invokeSubscriber(() => subscription.onError?.(new Error(event.data.error)));
      }
      return;
    }
    if (event.data?.type === 'database-upgrade') {
      for (const listener of databaseUpgradeListeners) listener(event.data.versions);
      return;
    }
    if (event.data?.type === 'preparation-log') {
      if (typeof event.data.event?.message === 'string') {
        recordPreparationEvent('ingestion', { message: event.data.event.message });
      }
      return;
    }
    if (event.data?.type === 'loading-progress') {
      const state = event.data.state;
      if (typeof state?.id === 'string' && ['start', 'update', 'complete'].includes(state.phase)) {
        if (state.phase === 'start') workerLoadingProgressOperations.add(state.id);
        if (state.phase === 'complete') workerLoadingProgressOperations.delete(state.id);
        emitWorkerLoadingProgress(state);
      }
      return;
    }
    if (event.data?.type === 'notification') {
      try {
        const notification = event.data.notification;
        const id = typeof notification?.id === 'string' ? notification.id : undefined;
        if (notification?.dismiss === true) {
          if (notification.kind === 'ingestion' && browserFirstLoad.get().ingestion?.id === id) {
            browserFirstLoad.set((current) => ({ ...current, ingestion: undefined }));
          }
          if (id) {
            if (!cancelledWorkerNotificationIds.has(id)) {
              resolveNotification(`worker:${id}`);
            }
            workerNotificationHandles.delete(id);
            cancelledWorkerNotificationIds.delete(id);
          }
        } else if (id) {
          if (cancelledWorkerNotificationIds.has(id)) return;
          if (typeof notification.message !== 'string') return;
          const interactiveNotification = /** @type {Omit<Exclude<Parameters<typeof publishNotification>[0], string>, 'action' | 'actions'> & { actions?: Array<{ label?: unknown, operation?: unknown, placement?: unknown, requestId?: unknown }> }} */ (notification);
          const current = workerNotificationHandles.get(id);
          /** @type {ReturnType<typeof publishNotification>} */
          let handle;
          const interactive = {
            ...attachWorkerNotificationActions(interactiveNotification, id, () => handle),
            id: `worker:${id}`
          };
          if (current) {
            handle = current;
            current.update(interactive);
          } else {
            handle = publishNotification(interactive);
            workerNotificationHandles.set(id, handle);
          }
          if (notification.kind === 'ingestion' && browserFirstLoad.get().status === 'loading') {
            browserFirstLoad.set((current) => ({ ...current, ingestion: {
              id, message: interactive.message, detailsSubtitle: interactive.detailsSubtitle,
              details: interactive.details, actions: interactive.actions
            } }));
          }
        } else {
          publishNotification(notification);
        }
      } catch {
        // Ignore malformed worker notifications without disrupting data processing.
      }
      return;
    }
    if (typeof event.data?.subscriptionId === 'string') {
      const subscription = subscriptions.get(event.data.subscriptionId);
      if (subscription && githubDashboardDependencies(subscription.sourceNames, subscription.context)
          && (event.data.githubEpoch ?? 0) !== githubAuthenticationEpoch) return;
      if (subscription?.registeredWorker === processor
          && event.data.data && typeof event.data.data === 'object') {
        enqueueSubscriptionUpdate(subscription, event.data.data, event.data.revision, event.data.partial === true);
      } else if (subscription?.registeredWorker === processor
          && typeof event.data.error === 'string') {
        const error = new Error(event.data.error);
        for (const listener of [...subscription.listeners]) {
          if (subscription.listeners.has(listener) && listener.onError) {
            invokeSubscriber(() => listener.onError?.(error));
          }
        }
      }
      return;
    }
    const request = pending.get(event.data?.id);
    if (!request) return;
    pending.delete(event.data.id);
    request.cleanup();
    if (request.githubEpoch !== undefined && request.githubEpoch !== githubAuthenticationEpoch) {
      request.reject(new Error('GitHub authentication changed.'));
      return;
    }
    if (typeof event.data.error === 'string') {
      const error = new Error(event.data.error);
      if (event.data.cancelled) error.name = 'DataProcessingCancelledError';
      request.reject(error);
    } else {
      request.resolve(event.data.data);
    }
  });
  worker.addEventListener('error', (event) => {
    const failedWorker = processor;
    const error = new Error(event.message || 'Data worker failed.');
    debugDataProcessor({ event: 'worker-failed', pendingCount: pending.size });
    rejectWorkerRequests(failedWorker, error);
    terminateWorkerSubscriptions(failedWorker, error);
    resetWorker(failedWorker);
  });
  if (githubAuthenticationEpoch > 0) worker.postMessage({
    operation: 'invalidate-github-authentication', epoch: githubAuthenticationEpoch
  });
  for (const subscription of subscriptions.values()) {
    if (usesRemoteDataBackend() && !githubDashboardDependencies(subscription.sourceNames, subscription.context)) continue;
    registerSubscription(worker, subscription);
    if (usesRemoteDataBackend()) registerRemoteGitHubSubscription(worker, subscription);
  }
  return worker;
}

/** @param {Worker | null} processor */
function resetWorker(processor) {
  for (const subscription of [...githubSubscriptions.values()]) {
    if (subscription.processor !== processor) continue;
    subscription.stop();
    invokeSubscriber(() => subscription.onError?.(new Error('Data worker stopped.')));
  }
  processor?.terminate();
  if (worker !== processor) return;
  worker = null;
  for (const id of workerLoadingProgressOperations) {
    emitWorkerLoadingProgress({ id, phase: 'complete' });
  }
  workerLoadingProgressOperations.clear();
  for (const [id, notification] of workerNotificationHandles) {
    if (!cancelledWorkerNotificationIds.has(id)) notification.dismiss();
  }
  workerNotificationHandles.clear();
  cancelledWorkerNotificationIds.clear();
  if (browserFirstLoad.get().ingestion) {
    browserFirstLoad.set((current) => ({ ...current, ingestion: undefined }));
  }
  for (const subscription of subscriptions.values()) {
    if (subscription.registeredWorker === processor) {
      subscription.remoteStop?.();
      subscription.remoteStop = undefined;
    }
    subscription.registeredWorker = null;
  }
}

/** @param {Worker} processor @param {Error} error */
function terminateWorkerSubscriptions(processor, error) {
  for (const [id, subscription] of subscriptions) {
    if (subscription.registeredWorker !== processor) continue;
    subscriptions.delete(id);
    subscription.remoteStop?.();
    subscription.remoteStop = undefined;
    subscription.latest = null;
    subscription.snapshot = null;
    if (subscription.frame !== null && typeof globalThis.cancelAnimationFrame === 'function') {
      globalThis.cancelAnimationFrame(subscription.frame);
    }
    subscription.frame = null;
    for (const listener of [...subscription.listeners]) {
      cancelSubscriberFrame(listener);
      listener.cleanup();
      if (listener.onError) invokeSubscriber(() => listener.onError?.(error));
    }
    subscription.listeners.clear();
  }
}

/** @param {() => void} notify */
function invokeSubscriber(notify) {
  try {
    notify();
  } catch (error) {
    queueMicrotask(() => {
      throw error;
    });
  }
}
