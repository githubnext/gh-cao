import { tidy } from './data-operations.js';
import { summarizeTableColumns } from './table-summary-data.js';
import { clusterScatterPoints } from './scatter-clustering.js';
import { queryDashboardSourceObservations } from './data/queries/ingestion.js';
import {
  finalizeNormalizedJsonlIngestion,
  ingestDashboardSources,
  ingestNormalizedJsonl,
  normalizedJsonlCurrentShards
} from './data/ingest/coordinator.js';
import { normalize } from './data/normalize/index.js';
import {
  queryDatabaseSources,
  queryIndexedDatabaseSources,
  loadDatabaseQuerySources
} from './data/queries/database.js';
import { relationshipErrors } from './data/model/schema.js';
import {
  DATABASE_VERSION,
  ENTITY_STORES,
  readCollections,
  readTransaction,
  readTransactions,
  recordTransaction,
  subscribeCanonicalDatabaseUpgrade
} from './data/storage/indexeddb.js';
import { compileDashboardViewPayloadQueries, dashboardPartialViewSources } from './data/queries/view-payload-compiler.js';
import { normalizeViewFilters } from './view-filter-contract.js';
import { BROWSER_RETENTION_WINDOWS_MS } from './data/storage/retention.js';
import { DashboardQueryCancelledError, continuationRevision, executeDashboardQueries, paginateDashboardSources, resolveDashboardQuerySources } from './data/queries/declarative.js';
import { formatDataSize, publishWorkerLoadingProgress, startIngestionProgress } from './ingestion-progress.js';
import { loadDashboardSources } from './source-loader.js';
import { createDebug, debugEagerIngest, debugShardLimit, diagnosticErrorName, isDebugEnabled } from './debug.js';
import { withRetries } from './retry.js';
import { prepareMemoryFile } from './data/repository-memory-jsonl.js';
import { cliQueueOperation, cliQueueEnabled, configureCliQueue, isCliQueueQuery, queryCliQueue } from './data/cli-queue.js';
import { createGitHubDataService } from './data/github-service.js';
import { isGitHubQuerySource, githubSnapshotSource } from './data/github-query-sources.js';
import { queryCalloutVisibility } from './data/queries/callouts.js';
import { evidencePacketSelection } from './data/queries/evidence-selection.js';

const debugIngestion = createDebug('data:ingestion');
const debugPerformance = createDebug('data:performance');
const DASHBOARD_SNAPSHOT_TRANSACTION_ID = 'dashboard-snapshot:complete';
let nextQueryProgressId = 0;

/** @template T @param {() => T} query @returns {T} */
function withQueryProgress(query) {
  if (typeof self === 'undefined' || typeof self.postMessage !== 'function'
      || typeof document !== 'undefined') return query();
  const id = `query-progress-${++nextQueryProgressId}`;
  publishWorkerLoadingProgress({ id, phase: 'start' });
  try {
    const result = query();
    if (result instanceof Promise) {
      return /** @type {T} */ (result.finally(() => publishWorkerLoadingProgress({ id, phase: 'complete' })));
    }
    publishWorkerLoadingProgress({ id, phase: 'complete' });
    return result;
  } catch (error) {
    publishWorkerLoadingProgress({ id, phase: 'complete' });
    throw error;
  }
}

async function readDashboardSnapshotMetadata() {
  const snapshot = await readTransaction(indexedDB, DASHBOARD_SNAPSHOT_TRANSACTION_ID);
  if (typeof snapshot?.createdAt === 'string') {
    debugIngestion({ event: 'snapshot-read', status: 'present', schemaVersion: DATABASE_VERSION });
    return { createdAt: snapshot.createdAt };
  }

  const legacySnapshot = (await readTransactions(indexedDB))
    .filter((transaction) => transaction.kind === 'ingest-dashboard-sources'
      && typeof transaction.createdAt === 'string'
      && !String(transaction.payloadScope ?? '').endsWith('#inventory-phase'))
    .toSorted((left, right) => String(left.createdAt).localeCompare(String(right.createdAt)))
    .at(-1);
  if (typeof legacySnapshot?.createdAt !== 'string') {
    debugIngestion({ event: 'snapshot-read', status: 'missing', schemaVersion: DATABASE_VERSION });
    return null;
  }
  const migratedSnapshot = {
    id: DASHBOARD_SNAPSHOT_TRANSACTION_ID,
    kind: 'dashboard-snapshot',
    createdAt: legacySnapshot.createdAt
  };
  await recordTransaction(indexedDB, migratedSnapshot).catch(() => undefined);
  debugIngestion({ event: 'snapshot-read', status: 'migrated', schemaVersion: DATABASE_VERSION });
  return { createdAt: migratedSnapshot.createdAt };
}

async function reportBrowserStorageDiagnostics() {
  if (!isDebugEnabled('data:ingestion')) return;
  const storage = globalThis.navigator?.storage;
  try {
    const [estimate, persisted] = await Promise.all([
      storage?.estimate?.() ?? null,
      storage?.persisted?.() ?? null
    ]);
    debugIngestion({
      event: 'browser-storage',
      estimateSupported: typeof storage?.estimate === 'function',
      persistenceSupported: typeof storage?.persist === 'function',
      persisted,
      usageBytes: estimate?.usage ?? null,
      quotaBytes: estimate?.quota ?? null,
      indexedDBBytes: /** @type {StorageEstimate & { usageDetails?: { indexedDB?: number } } | null} */ (estimate)?.usageDetails?.indexedDB ?? null
    });
  } catch (error) {
    debugIngestion({ event: 'browser-storage-failed', errorName: diagnosticErrorName(error) });
  }
}
/**
 * Forces every published activity shard to be ingested before results are
 * published, so diagnostics and measurement runs observe a fully ingested
 * canonical database instead of run-phase-only data.
 */
const eagerIngest = debugEagerIngest();
if (eagerIngest) debugIngestion('eager ingestion requested', { eagerIngest });
const monotonicNow = () => globalThis.performance?.now() ?? Date.now();
const workerScope = typeof self !== 'undefined' && 'postMessage' in self ? self : null;
if (typeof document === 'undefined' && workerScope) {
  subscribeCanonicalDatabaseUpgrade((versions) => workerScope.postMessage({ type: 'database-upgrade', versions }));
}

/** Returns one self-contained database diagnostics payload to the main thread. */
async function collectCanonicalDatabaseDiagnostics() {
  const records = await readCollections(indexedDB, ENTITY_STORES);
  return {
    schemaVersion: DATABASE_VERSION,
    counts: Object.fromEntries(
      ENTITY_STORES.map((store) => [store, records[store].length])
    ),
    relationshipErrors: relationshipErrors(
      /** @type {import('./data/model/schema.js').CanonicalBatch} */ (records)
    ),
    duplicateRecordIds: Object.fromEntries(ENTITY_STORES.map((store) => [store, []]))
  };
}

/** @param {ReadableStream<Uint8Array>} body */
async function* responseChunks(body) {
  const reader = body.getReader();
  let completed = false;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) {
        completed = true;
        return;
      }
      yield value;
    }
  } finally {
    if (!completed) await reader.cancel().catch(() => undefined);
    reader.releaseLock();
  }
}

/** @type {{ logicalSources: Record<string, import('./presenter.js').LogicalSourceInput>, revision: number, hosted?: boolean } | null} */
let liveDashboard = null;
let hasCompleteDashboardSnapshot = false;
/**
 * The narrowest phase whose canonical data is currently published. While a
 * partial phase is published, only subscriptions fully satisfied by that
 * phase's sources may be emitted.
 * @type {'inventory' | 'runs' | 'complete'}
 */
let publicationPhase = 'complete';
/**
 * @typedef {{ sourceNames: string[], context: ReturnType<typeof dashboardContext>, requestContext: { githubUrlBase?: string, dashboardRepository?: string | null }, pagination: Record<string, { limit: number, continuationToken?: string }>, revision: number | null, emitted: boolean, previewedViews?: Set<string>, controller?: AbortController, githubController?: AbortController, githubSources?: Record<string, import('./presenter.js').LogicalSourceInput>, hostedSources?: Record<string, import('./presenter.js').LogicalSourceInput>, pageId?: string, viewId?: string, routeParameters?: Record<string, string>, queryContext?: import('./data/queries/view-payload-compiler.js').GlobalQueryContext }} DashboardSubscription
 */
/** @type {Map<string, DashboardSubscription>} */
const dashboardSubscriptions = new Map();
/** @type {ReturnType<typeof createGitHubDataService> | undefined} */
let githubDataService;
let githubAuthenticationEpoch = 0;

/**
 * @param {string[]} names
 * @param {{ queries?: unknown[], dashboardRepository?: string | null }} context
 * @param {import('./data/github-service.js').GitHubBackendConfiguration | undefined} configuration
 * @param {AbortSignal} signal
 * @param {(name: string, source: import('./presenter.js').LogicalSourceInput) => void} notify
 */
function subscribeGitHubQuerySources(names, context, configuration, signal, notify) {
  signal.throwIfAborted();
  const resources = resolveDashboardQuerySources(context.queries ?? [], names).filter(isGitHubQuerySource);
  const [owner, repo, extra] = (context.dashboardRepository ?? '').split('/');
  githubDataService ??= createGitHubDataService();
  return Promise.all(resources.map((name) => new Promise((resolve, reject) => {
    const aborted = () => reject(new DashboardQueryCancelledError('GitHub source query was cancelled', 'aborted'));
    signal.addEventListener('abort', aborted, { once: true });
    const ready = () => { signal.removeEventListener('abort', aborted); resolve(undefined); };
    if (!owner || !repo || extra) {
      notify(name, githubSnapshotSource(name, { data: null, status: 'unavailable', source: 'none', error: 'repository-unconfigured', retryAt: null }));
      ready();
      return;
    }
    try {
      githubDataService?.subscribe({
        owner, repo, resource: name === 'github-latest-release' ? 'latest-release' : 'repository'
      }, configuration ?? { baseUrl: null }, (snapshot) => {
        notify(name, githubSnapshotSource(name, snapshot));
        if (snapshot.data || snapshot.error) ready();
      }, { signal });
    } catch (error) {
      signal.removeEventListener('abort', aborted);
      reject(error);
    }
  })));
}
/** @type {Map<number, Record<string, unknown>>} */
const inFlightDashboardSources = new Map();
/** @type {Set<string>} */
const dirtyDashboardSubscriptions = new Set();
const SUBSCRIPTION_FLUSH_DELAY_MS = 50;
const REPOSITORY_MEMORY_CAMPAIGN_PATTERN = /^[a-z0-9](?:[a-z0-9._-]{0,99})$/;
const REPOSITORY_MEMORY_EXTENSIONS = new Set(['.json', '.jsonl', '.md', '.txt', '.yaml', '.yml']);
const REPOSITORY_MEMORY_MAX_FILES = 400;
const REPOSITORY_MEMORY_MAX_FILE_SIZE = 1024 * 1024;
const REPOSITORY_MEMORY_MAX_NESTING = 10;
const REPOSITORY_MEMORY_MAX_TOTAL_SIZE = 64 * 1024 * 1024;
/** @type {ReturnType<typeof setTimeout> | null} */
let subscriptionFlushTimer = null;
let subscriptionFlushRunning = false;
let dashboardIngestionCount = 0;

const INGESTION_LOCK_WAIT_MESSAGE = 'Waiting for another dashboard ingestion to finish.';

function hasUnrenderedDashboardSubscription() {
  return [...dirtyDashboardSubscriptions].some((id) => dashboardSubscriptions.get(id)?.emitted === false);
}

async function loadActiveDashboard() {
  if (liveDashboard) return liveDashboard;
  liveDashboard = {
    logicalSources: {},
    revision: 0
  };
  return liveDashboard;
}

/**
 * @param {unknown} sourceNames
 * @returns {Set<string>}
 */
function requestedSourceNames(sourceNames) {
  if (!Array.isArray(sourceNames) || sourceNames.some((name) => typeof name !== 'string')) {
    throw new TypeError('Canonical dashboard source names must be an array of strings.');
  }
  return new Set(sourceNames);
}

/** @param {unknown} value */
function repositoryMemoryPath(value) {
  if (typeof value !== 'string' || !value || value.startsWith('/') || value.includes('\\')) return '';
  const segments = value.split('/');
  if (segments.some((segment) => !segment || segment === '.' || segment === '..')) return '';
  if (segments.length - 1 > REPOSITORY_MEMORY_MAX_NESTING) return '';
  const name = segments.at(-1) ?? '';
  const extensionIndex = name.lastIndexOf('.');
  const extension = extensionIndex >= 0 ? name.slice(extensionIndex).toLowerCase() : '';
  return REPOSITORY_MEMORY_EXTENSIONS.has(extension) ? value : '';
}

/** @param {unknown} value */
function repositoryMemoryOmissions(value) {
  const source = value && typeof value === 'object' ? /** @type {Record<string, unknown>} */ (value) : {};
  const omitted = {
    fileLimit: 0, fileSize: 0, totalSize: 0, extension: 0, nesting: 0, unsafePath: 0, invalidContent: 0, unsupportedType: 0
  };
  for (const key of Object.keys(omitted)) {
    const count = source[key] ?? 0;
    if (!Number.isSafeInteger(count) || Number(count) < 0) {
      throw new Error('Campaign repository-memory omission metadata is invalid.');
    }
    omitted[/** @type {keyof typeof omitted} */ (key)] = Number(count);
  }
  return omitted;
}

/** @param {unknown[]} campaigns */
function validateRepositoryMemoryTotalSize(campaigns) {
  let totalSize = 0;
  for (const value of campaigns) {
    if (!value || typeof value !== 'object' || Array.isArray(value)) {
      throw new Error('Campaign repository-memory entry is invalid.');
    }
    const entry = /** @type {Record<string, unknown>} */ (value);
    if (!Array.isArray(entry.files)) throw new Error('Campaign repository-memory entry is invalid.');
    for (const file of entry.files) {
      const size = Number(file?.size);
      if (!Number.isSafeInteger(size) || size < 0 || size > REPOSITORY_MEMORY_MAX_FILE_SIZE) {
        throw new Error('Campaign repository-memory file metadata is invalid.');
      }
      if (size > REPOSITORY_MEMORY_MAX_TOTAL_SIZE - totalSize) {
        throw new Error('Campaign repository-memory files exceed the total size limit.');
      }
      totalSize += size;
    }
  }
}

/** @param {unknown} value @param {string} campaign */
function repositoryMemoryCampaign(value, campaign) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('Campaign repository-memory entry is invalid.');
  }
  const entry = /** @type {Record<string, unknown>} */ (value);
  if (entry.campaign !== campaign
      || entry.branch !== `memory/${campaign}`
      || typeof entry.commit !== 'string'
      || !/^[0-9a-f]{40,64}$/i.test(entry.commit)
      || !Array.isArray(entry.files)
      || entry.files.length > REPOSITORY_MEMORY_MAX_FILES) {
    throw new Error('Campaign repository-memory entry is invalid.');
  }
  const files = entry.files.map((value) => {
    const file = /** @type {Record<string, unknown>} */ (value);
    return {
      path: repositoryMemoryPath(file.path),
      size: Number(file.size),
      oid: String(file.oid ?? ''),
      sha256: file.sha256 === undefined ? undefined : String(file.sha256),
    };
  });
  if (files.some((file) => !file.path
      || !Number.isSafeInteger(file.size)
      || file.size < 0
      || file.size > REPOSITORY_MEMORY_MAX_FILE_SIZE
      || !/^[0-9a-f]{40,64}$/i.test(file.oid)
      || (file.sha256 !== undefined && !/^[0-9a-f]{64}$/i.test(file.sha256)))) {
    throw new Error('Campaign repository-memory file metadata is invalid.');
  }
  const totalSize = files.reduce((sum, file) => sum + file.size, 0);
  if (!Number.isSafeInteger(totalSize) || totalSize > REPOSITORY_MEMORY_MAX_TOTAL_SIZE) {
    throw new Error('Campaign repository-memory files exceed the total size limit.');
  }
  return {
    branch: String(entry.branch),
    commit: String(entry.commit),
    files,
    omitted: repositoryMemoryOmissions(entry.omitted),
  };
}

/** @param {Response} response */
async function boundedRepositoryMemoryContent(response) {
  const declaredLength = Number(response.headers.get('content-length'));
  if (Number.isFinite(declaredLength) && declaredLength > REPOSITORY_MEMORY_MAX_FILE_SIZE) {
    throw new Error('Memory file exceeds the published size limit.');
  }
  const reader = response.body?.getReader();
  if (!reader) {
    const content = await response.arrayBuffer();
    if (content.byteLength > REPOSITORY_MEMORY_MAX_FILE_SIZE) {
      throw new Error('Memory file exceeds the published size limit.');
    }
    return new Uint8Array(content);
  }
  /** @type {Uint8Array[]} */
  const chunks = [];
  let bytes = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      bytes += value.byteLength;
      if (bytes > REPOSITORY_MEMORY_MAX_FILE_SIZE) {
        throw new Error('Memory file exceeds the published size limit.');
      }
      chunks.push(value);
    }
  } catch (error) {
    await reader.cancel().catch(() => undefined);
    throw error;
  } finally {
    reader.releaseLock();
  }
  const content = new Uint8Array(bytes);
  let offset = 0;
  for (const chunk of chunks) {
    content.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return content;
}

/** @param {Record<string, unknown>} request @param {AbortSignal} [signal] */
async function queryRepositoryMemory(request, signal) {
  const campaignIds = request.action === 'list-campaigns' ? request.campaigns : [request.campaign];
  if (!Array.isArray(campaignIds) || !campaignIds.every(
    (campaign) => typeof campaign === 'string' && REPOSITORY_MEMORY_CAMPAIGN_PATTERN.test(campaign)
  )) {
    throw new Error('Repository-memory campaign is invalid.');
  }
  if (typeof request.memoryRoot !== 'string') throw new Error('Repository-memory root is invalid.');
  const memoryRoot = new URL(request.memoryRoot);
  if (!['http:', 'https:'].includes(memoryRoot.protocol)
      || (globalThis.location?.origin
        && globalThis.location.origin !== 'null'
        && memoryRoot.origin !== globalThis.location.origin)) {
    throw new Error('Repository-memory root must be same-origin.');
  }
  const manifestResponse = await fetch(new URL('manifest.json', memoryRoot), {
    cache: 'no-store', credentials: 'same-origin', signal
  });
  if (!manifestResponse.ok) throw new Error(`Manifest request returned ${manifestResponse.status}.`);
  const manifest = await manifestResponse.json();
  if (manifest?.version !== 1 || !Array.isArray(manifest.campaigns)) {
    throw new Error('Repository-memory manifest is invalid.');
  }
  validateRepositoryMemoryTotalSize(manifest.campaigns);
  const findCampaign = (/** @type {string} */ campaignId) => {
    const campaignValue = manifest.campaigns.find(
      (/** @type {Record<string, unknown>} */ entry) => entry?.campaign === campaignId
    );
    return campaignValue ? repositoryMemoryCampaign(campaignValue, campaignId) : null;
  };
  if (request.action === 'list-campaigns') return campaignIds.map(findCampaign);
  const campaignId = String(request.campaign);
  const campaign = findCampaign(campaignId);
  if (!campaign) return null;
  if (request.action === 'list') return campaign;
  if (request.action !== 'content') throw new Error('Repository-memory action is invalid.');
  const filePath = repositoryMemoryPath(request.path);
  const file = campaign.files.find((entry) => entry.path === filePath);
  if (!file) throw new Error('Memory file path is invalid.');
  const campaignRoot = new URL(`${encodeURIComponent(campaignId)}/`, memoryRoot);
  const fileUrl = new URL(filePath.split('/').map(encodeURIComponent).join('/'), campaignRoot);
  if (fileUrl.origin !== memoryRoot.origin || !fileUrl.href.startsWith(campaignRoot.href)) {
    throw new Error('Memory file path is invalid.');
  }
  const response = await fetch(fileUrl, { cache: 'no-store', credentials: 'same-origin', signal });
  if (!response.ok) throw new Error(`File request returned ${response.status}.`);
  const content = await boundedRepositoryMemoryContent(response);
  if (content.byteLength !== file.size) throw new Error('Memory file size does not match its manifest.');
  if (file.sha256) {
    const digest = await crypto.subtle.digest('SHA-256', content);
    const actual = [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, '0')).join('');
    if (actual !== file.sha256.toLowerCase()) throw new Error('Memory file hash does not match its manifest.');
  }
  try {
    return { content: new TextDecoder('utf-8', { fatal: true }).decode(content) };
  } catch {
    throw new Error('Memory file is not valid UTF-8 text.');
  }
}

/**
 * Inventory sources whose canonical records no activity shard ever observes.
 * They can be committed before historical shard ingestion starts without
 * changing how the complete inventory commit resolves shared structural
 * records, so their subscriptions render immediately.
 */
const INVENTORY_PHASE_DATABASE_SOURCES = new Set(['marketplace-packages']);
const RUN_PHASE_DATABASE_SOURCES = new Set([
  ...INVENTORY_PHASE_DATABASE_SOURCES,
  'campaigns',
  'repositories',
  'workflows',
  'runs'
]);

/** @param {{ sourceNames: string[], context: ReturnType<typeof dashboardContext> }} subscription @param {string[]} [sourceNames] */
function subscriptionDatabaseSources(subscription, sourceNames = subscription.sourceNames) {
  const queryNames = new Set(subscription.context.queries
    .filter((definition) => definition && typeof definition === 'object' && !Array.isArray(definition))
    .map((definition) => /** @type {{ name?: unknown }} */ (definition).name)
    .filter((name) => typeof name === 'string'));
  return resolveDashboardQuerySources(subscription.context.queries, sourceNames)
    .filter((name) => !queryNames.has(name) && !isGitHubQuerySource(name));
}

/** @param {DashboardSubscription} subscription @param {'inventory' | 'runs' | 'complete'} [phase] */
function subscriptionPartialViews(subscription, phase = publicationPhase) {
  const interactive = subscription.queryContext;
  if (Object.keys(interactive?.filters ?? {}).length
      || Object.keys(interactive?.viewFilters ?? {}).length
      || interactive?.search?.query
      || interactive?.orderBy?.length
      || Object.keys(subscription.routeParameters ?? {}).length) return [];
  const page = subscription.context.pages.find((candidate) => candidate?.id === subscription.pageId);
  return dashboardPartialViewSources(page, {
    views: subscription.context.views, viewId: subscription.viewId, sourceNames: subscription.sourceNames,
    backend: subscription.hostedSources !== undefined || liveDashboard?.hosted ? 'hosted' : 'static'
  }).filter(({ partialSource }) => isPhaseSubscription(subscription, phase, [partialSource]));
}

/**
 * @param {DashboardSubscription} subscription
 * @param {'inventory' | 'runs' | 'complete'} phase
 * @param {string[]} [sourceNames]
 */
function isPhaseSubscription(subscription, phase, sourceNames) {
  if (phase === 'complete') return true;
  const available = phase === 'inventory'
    ? INVENTORY_PHASE_DATABASE_SOURCES
    : RUN_PHASE_DATABASE_SOURCES;
  return subscriptionDatabaseSources(subscription, sourceNames).every((name) => available.has(name));
}

/**
 * @param {Record<string, import('./presenter.js').LogicalSourceInput>} sources
 * @param {Set<string>} requested
 */
function pageScopedSources(sources, requested) {
  return Object.fromEntries(Object.entries(sources).filter(([name]) => requested.has(name)));
}

/**
 * @param {Set<string>} requested
 * @param {ReturnType<typeof dashboardContext>} context
 * @param {{ githubUrlBase?: string, dashboardRepository?: string | null }} requestContext
 * @param {{ aborted?: boolean }} [signal]
 * @param {Record<string, { limit: number, continuationToken?: string }>} [pagination]
 * @param {string} [pageId]
 * @param {Record<string, string>} [routeParameters]
 * @param {{ filters?: Record<string, string[]>, timeWindow?: { start?: string, end?: string }, viewMode?: 'chart'|'table'|'card' }} [queryContext]
 * @param {string} [viewId]
 * @param {typeof liveDashboard} [dashboard]
 * @param {boolean} [partial]
 * @param {string[]} [partialViewIds]
 * @returns {Promise<Record<string, import('./presenter.js').LogicalSourceInput>>}
 */
async function queryLiveDashboard(
  requested,
  context,
  requestContext,
  signal,
  pagination = {},
  pageId,
  routeParameters,
  queryContext,
  viewId,
  dashboard = liveDashboard,
  partial = false,
  partialViewIds
) {
  return withQueryProgress(() => executeLiveDashboardQuery(
    requested, context, requestContext, signal, pagination, pageId,
    routeParameters, queryContext, viewId, dashboard, partial, partialViewIds
  ));
}

/** @type {typeof queryLiveDashboard} */
async function executeLiveDashboardQuery(
  requested, context, requestContext, signal, pagination = {}, pageId,
  routeParameters, queryContext, viewId, dashboard = liveDashboard, partial = false, partialViewIds
) {
  dashboard ??= await loadActiveDashboard();
  if (signal?.aborted) throw new DashboardQueryCancelledError('dashboard queries were cancelled', 'aborted');
  const startedAt = monotonicNow();
    const page = pageId
      ? context.pages.find((candidate) => candidate?.id === pageId)
      : null;
    let evaluatedAt = queryContext?.timeWindow?.end;
    if (page && requested.size > 0 && !evaluatedAt) {
      const metadataSources = Object.fromEntries(Object.entries(dashboard.logicalSources).map(([name, source]) => [
        name, { ...source, rows: [] }
      ]));
      evaluatedAt = latestCanonicalInstant(metadataSources);
      if (!evaluatedAt && !partial) {
        evaluatedAt = latestCanonicalInstant(await queryDatabaseSources(indexedDB, dashboard.logicalSources, ['runs']));
      }
    }
    const viewPayload = page && pageId
      ? compileDashboardViewPayloadQueries(page, pageId, {
          routeParameters,
          queryContext: partial ? { ...queryContext, timeWindow: undefined } : queryContext,
          evaluatedAt,
          queries: context.queries,
          views: context.views,
          backend: dashboard.hosted ? 'hosted' : 'static',
          viewId,
          viewIds: partialViewIds,
          sourceNames: requested,
          partial
        })
      : { aliases: [], queries: [], replacedSources: [] };
    const replacedSources = new Set(viewPayload.replacedSources);
    const executionQueries = [...context.queries, ...viewPayload.queries];
    const executionRequested = new Set([
      ...[...requested].filter((name) => !replacedSources.has(name)),
      ...viewPayload.aliases
    ]);
    const requestedWithDependencies = resolveDashboardQuerySources(executionQueries, executionRequested);
    const nativeSources = dashboard.hosted ? {} : await queryIndexedDatabaseSources(
      indexedDB,
      dashboard.logicalSources,
      executionQueries,
      requestedWithDependencies,
      { signal }
    );
    const nativeSourceNames = new Set(Object.keys(nativeSources));
    const nonNativeRequested = [...executionRequested].filter((name) => !nativeSourceNames.has(name));
    const required = resolveDashboardQuerySources(
      executionQueries,
      nonNativeRequested,
      nativeSourceNames
    );
    const databaseRequired = required.filter((name) => !nativeSourceNames.has(name));
    /** @type {{ databaseMs: number, projectionMs: number, totalMs: number, recordsRead: number, stores: string[] } | undefined} */
    let databaseMetrics;
    const databasePayload = dashboard.hosted ? dashboard.logicalSources : await queryDatabaseSources(
      indexedDB,
      dashboard.logicalSources,
      databaseRequired,
      { onMetrics: (metrics) => { databaseMetrics = metrics; },
        checkpoint: () => { if (signal?.aborted) throw new DashboardQueryCancelledError('Value source read aborted', 'aborted'); },
        evidenceSelection: evidencePacketSelection(executionQueries, nonNativeRequested) }
    );
    const directRequests = new Set([...executionRequested].filter((name) => (
      !nativeSourceNames.has(name)
    )));
    const querySources = {
      ...databasePayload,
      ...nativeSources,
      ...executeDashboardQueries(
        executionQueries,
        { ...databasePayload, ...nativeSources },
        directRequests,
        { signal }
      )
    };
    const selected = pageScopedSources(
      querySources,
      new Set([...requested].filter((name) => !replacedSources.has(name)))
    );
    const responseSources = {
      ...selected,
      ...pageScopedSources(querySources, new Set(viewPayload.aliases))
    };
    if (partial) {
      for (const alias of viewPayload.aliases) {
        const source = responseSources[alias];
        if (source) responseSources[alias] = {
          ...source,
          metadata: { ...source.metadata, 'projection-state': 'pending' }
        };
      }
    }
    const response = paginateDashboardSources(
      responseSources,
      /** @type {Record<string, { limit: number, continuationToken?: string }>} */ (pagination ?? {}),
      continuationRevision(executionQueries, dashboard.revision)
    );
    const totalMs = monotonicNow() - startedAt;
    const databaseMs = databaseMetrics?.databaseMs ?? 0;
    debugPerformance('page query', {
      pageId: pageId ?? null,
      viewId: viewId ?? null,
      databaseMs,
      queryMs: Math.max(0, totalMs - databaseMs),
      projectionMs: databaseMetrics?.projectionMs ?? 0,
      totalMs,
      recordsRead: databaseMetrics?.recordsRead ?? 0,
      stores: databaseMetrics?.stores ?? [],
      requestedSources: [...requested],
      returnedRows: Object.fromEntries(Object.entries(response).map(([name, source]) => [name, source.rows.length]))
    });
  return response;
}

/** @param {Record<string, import('./presenter.js').LogicalSourceInput>} sources */
function latestCanonicalInstant(sources) {
  let latest = Number.NEGATIVE_INFINITY;
  for (const source of Object.values(sources)) {
    for (const value of [source.metadata?.['as-of'], source.metadata?.['retrieved-at']]) {
      const timestamp = Date.parse(String(value ?? ''));
      if (Number.isFinite(timestamp)) latest = Math.max(latest, timestamp);
    }
    for (const row of source.rows ?? []) {
      for (const field of ['observed-at', 'started-at', 'ended-at']) {
        const timestamp = Date.parse(String(row[field] ?? ''));
        if (Number.isFinite(timestamp)) latest = Math.max(latest, timestamp);
      }
    }
  }
  return Number.isFinite(latest) ? new Date(latest).toISOString() : undefined;
}

/** @param {Iterable<string>} [ids] */
function scheduleDashboardSubscriptions(ids = dashboardSubscriptions.keys()) {
  for (const id of ids) {
    if (dashboardSubscriptions.has(id)) dirtyDashboardSubscriptions.add(id);
  }
  const hasUnrenderedSubscription = hasUnrenderedDashboardSubscription();
  if (subscriptionFlushRunning
      || dirtyDashboardSubscriptions.size === 0
      || (dashboardIngestionCount > 0 && !hasUnrenderedSubscription)) return;
  if (subscriptionFlushTimer !== null) clearTimeout(subscriptionFlushTimer);
  const delay = hasUnrenderedSubscription && dashboardIngestionCount === 0 ? 0 : SUBSCRIPTION_FLUSH_DELAY_MS;
  subscriptionFlushTimer = setTimeout(() => {
    subscriptionFlushTimer = null;
    void flushDashboardSubscriptions();
  }, delay);
}

async function flushDashboardSubscriptions(allowDuringIngestion = false) {
  if (subscriptionFlushRunning
      || (!allowDuringIngestion && dashboardIngestionCount > 0 && !hasUnrenderedDashboardSubscription())) return;
  subscriptionFlushRunning = true;
  try {
    while (dirtyDashboardSubscriptions.size > 0) {
      const ids = [...dirtyDashboardSubscriptions];
      dirtyDashboardSubscriptions.clear();
      const dashboard = liveDashboard;
      if (!dashboard) {
        for (const id of ids) dirtyDashboardSubscriptions.add(id);
        break;
      }
      for (const id of ids) {
        const subscription = dashboardSubscriptions.get(id);
        if (!subscription) continue;
        if (subscription.githubSources
            && resolveDashboardQuerySources(subscription.context.queries, subscription.sourceNames)
              .some((name) => isGitHubQuerySource(name) && !Object.hasOwn(subscription.githubSources ?? {}, name))) {
          continue;
        }
        if (subscription.hostedSources !== undefined
            && subscriptionDatabaseSources(subscription).some((name) => !Object.hasOwn(subscription.hostedSources ?? {}, name))) {
          continue;
        }
        const queryDashboard = subscription.githubSources ? {
          ...dashboard, hosted: subscription.hostedSources !== undefined,
          logicalSources: { ...dashboard.logicalSources, ...subscription.hostedSources, ...subscription.githubSources }
        } : dashboard;
        const controller = new AbortController();
        subscription.controller = controller;
        try {
          const pagination = subscription.revision === dashboard.revision
            ? subscription.pagination
            : resetPagination(subscription.pagination);
          const partialViews = subscriptionPartialViews(subscription);
          if (publicationPhase !== 'complete'
              && partialViews.some(({ viewId }) => !subscription.previewedViews?.has(viewId))) {
            const partial = await queryLiveDashboard(
              new Set(partialViews.map(({ source }) => source)),
              subscription.context,
              subscription.requestContext,
              controller.signal,
              pagination,
              subscription.pageId,
              subscription.routeParameters,
              subscription.queryContext,
              subscription.viewId,
              queryDashboard,
              true,
              partialViews.map(({ viewId }) => viewId)
            );
            if (dashboardSubscriptions.get(id) !== subscription || liveDashboard !== dashboard) continue;
            subscription.previewedViews = new Set(partialViews.map(({ viewId }) => viewId));
            workerScope?.postMessage({
              subscriptionId: id, revision: dashboard.revision, data: partial, partial: true,
              ...(subscription.githubSources ? { githubEpoch: githubAuthenticationEpoch } : {})
            });
            if (!isPhaseSubscription(subscription, publicationPhase)) {
              subscription.emitted = true;
              subscription.revision = dashboard.revision;
              continue;
            }
          }
          if (!isPhaseSubscription(subscription, publicationPhase)) continue;
          const data = await queryLiveDashboard(
            new Set(subscription.sourceNames),
            subscription.context,
            subscription.requestContext,
            controller.signal,
            pagination,
            subscription.pageId,
            subscription.routeParameters,
            subscription.queryContext,
            subscription.viewId,
            queryDashboard
          );
          if (dashboardSubscriptions.get(id) === subscription && liveDashboard === dashboard) {
            subscription.emitted = true;
            subscription.revision = dashboard.revision;
            subscription.pagination = pagination;
            workerScope?.postMessage({
              subscriptionId: id, revision: dashboard.revision, data,
              ...(subscription.githubSources ? { githubEpoch: githubAuthenticationEpoch } : {})
            });
          }
        } catch (error) {
          if (dashboardSubscriptions.get(id) === subscription && liveDashboard === dashboard) {
            workerScope?.postMessage({
              subscriptionId: id,
              ...(subscription.githubSources ? { githubEpoch: githubAuthenticationEpoch } : {}),
              error: error instanceof Error ? error.message : String(error)
            });
          }
        } finally {
          if (subscription.controller === controller) delete subscription.controller;
        }

      }
    }

    /** @param {Record<string, { limit: number, continuationToken?: string }>} pagination */
    function resetPagination(pagination) {
      return Object.fromEntries(Object.entries(pagination).map(([source, request]) => [
        source,
        { limit: request.limit }
      ]));
    }
  } finally {
    subscriptionFlushRunning = false;
    if (liveDashboard) scheduleDashboardSubscriptions(dirtyDashboardSubscriptions);
  }
}

/**
 * Publishes the latest committed canonical projection without blocking the next
 * shard download. The subscription flusher coalesces commits that arrive while
 * an earlier refresh is still running.
 *
 * A partial phase publishes only the subscriptions whose sources that phase
 * already satisfies, so a page such as Marketplace renders from the committed
 * inventory while historical activity shards are still being ingested.
 *
 * @param {Record<string, import('./presenter.js').LogicalSourceInput>} logicalSources
 * @param {'inventory' | 'runs' | 'complete'} phase
 * @returns {Promise<void>}
 */
function refreshDashboardSubscriptions(logicalSources, phase) {
  if (phase !== 'complete' && hasCompleteDashboardSnapshot && !eagerIngest) {
    debugIngestion('preserving complete dashboard snapshot during refresh', { phase });
    return Promise.resolve();
  }
  // Phases are published in widening order within one ingestion, which always
  // ends at 'complete', so a narrower phase never supersedes a wider one.
  const publication = phase !== 'complete' && eagerIngest ? 'complete' : phase;
  // The complete logical sources stay available to every published
  // subscription: the phase gate below, not this payload, decides which
  // subscriptions may read the partially committed canonical database.
  liveDashboard = {
    logicalSources,
    revision: (liveDashboard?.revision ?? 0) + 1
  };
  publicationPhase = publication;
  const published = publication === 'complete'
    ? [...dashboardSubscriptions.keys()]
    : [...dashboardSubscriptions]
      .filter(([, subscription]) => {
        return isPhaseSubscription(subscription, publication)
          || subscriptionPartialViews(subscription, publication).length > 0;
      })
      .map(([id]) => id);
  debugIngestion('publishing canonical phase', {
    phase: publication,
    requestedPhase: phase,
    eagerIngest,
    subscriptions: dashboardSubscriptions.size,
    published: published.length
  });
  scheduleDashboardSubscriptions(published);
  return flushDashboardSubscriptions(true);
}

/** @param {unknown} value */
function dashboardContext(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new TypeError('Canonical dashboard queries require a dashboard context.');
  }

  const context = /** @type {{ githubUrlBase?: unknown, pages?: unknown, queries?: unknown, views?: unknown }} */ (value);
  if (!Array.isArray(context.pages)) {
    throw new TypeError('Canonical dashboard context requires pages.');
  }
  if (context.queries !== undefined && !Array.isArray(context.queries)) {
    throw new TypeError('Canonical dashboard queries must be an array.');
  }
  if (context.views !== undefined && !Array.isArray(context.views)) {
    throw new TypeError('Canonical dashboard views must be an array.');
  }
  return {
    githubUrlBase: typeof context.githubUrlBase === 'string' && context.githubUrlBase
      ? context.githubUrlBase : 'https://github.com',
    pages: /** @type {Array<{ id: string, kind: 'built-in' | 'custom', route?: { ['hash-query-parameter']?: string } }>} */ (context.pages),
    queries: /** @type {unknown[]} */ (context.queries ?? []),
    views: /** @type {unknown[]} */ (context.views ?? [])
  };
}

/** @param {unknown} hashes @param {'runs' | 'records'} phase */
function publishedPhaseShards(hashes, phase) {
  if (!hashes || typeof hashes !== 'object' || Array.isArray(hashes)) return [];
  const pattern = new RegExp(`^gh-aw-logs-${phase}/[^/]+\\.jsonl$`, 'i');
  return Object.entries(/** @type {Record<string, unknown>} */ (hashes))
    .filter(([name, hash]) => pattern.test(name)
      && typeof hash === 'string'
      && /^[a-f0-9]{64}$/i.test(hash))
    .map(([name, hash]) => ({ name, hash: /** @type {string} */ (hash).toLowerCase(), phase }))
    .sort((left, right) => left.name.localeCompare(right.name));
}

/** @param {unknown} hashes */
export function publishedRunInformationShards(hashes) {
  return publishedPhaseShards(hashes, 'runs');
}

/** @param {unknown} hashes */
export function publishedRunRecordShards(hashes) {
  return publishedPhaseShards(hashes, 'records');
}

/** @param {unknown} hashes */
export function publishedPhasedActivityShards(hashes) {
  const runs = publishedRunInformationShards(hashes);
  const records = publishedRunRecordShards(hashes);
  return runs.length > 0 ? [...runs, ...records] : [];
}

/**
 * @param {{ id?: unknown, operation?: unknown, action?: unknown, command?: unknown, commandId?: unknown, after?: unknown, idempotencyKey?: unknown, campaign?: unknown, campaigns?: unknown, callouts?: unknown, path?: unknown, memoryRoot?: unknown, content?: unknown, data?: unknown, operators?: unknown, columns?: unknown, limit?: unknown, sources?: unknown, queries?: unknown, context?: unknown, sourceUrl?: unknown, sourceNames?: unknown, pagination?: unknown, reportActivation?: unknown, emitCurrent?: unknown, ingest?: unknown, pageId?: unknown, viewId?: unknown, routeParameters?: unknown, queryContext?: unknown, githubConfiguration?: import('./data/github-service.js').GitHubBackendConfiguration, hostedSources?: Record<string, import('./presenter.js').LogicalSourceInput> }} request
 * @param {AbortSignal} [signal] cancels declarative query execution
 * @returns {unknown}
 */
export function processDataRequest(request, signal) {
  if (request?.operation === 'query-dashboard-callouts') {
    if (!Array.isArray(request.callouts)) throw new TypeError('Dashboard callouts must be an array.');
    return queryCalloutVisibility(
      request.callouts,
      /** @type {Record<string, import('./presenter.js').LogicalSourceInput>} */ (request.sources ?? {}),
      signal
    );
  }
  if (request?.operation === 'cli-queue') {
    return cliQueueOperation(/** @type {Record<string, unknown>} */ (request), signal, (event) => {
      workerScope?.postMessage({ type: 'cli-queue-output', id: request.id, event });
    });
  }
  if (request?.operation === 'validate-query-editor-document') {
    signal?.throwIfAborted();
    return import('./data/query-editor.js').then(({ validateQueryEditorDocument }) => {
      signal?.throwIfAborted();
      return validateQueryEditorDocument(request.content);
    });
  }
  if (request?.operation === 'prepare-repository-memory-file') {
    if (typeof request.content !== 'string') throw new TypeError('Memory file content must be text.');
    const path = repositoryMemoryPath(request.path);
    if (!path) throw new Error('Memory file path is invalid.');
    if (new TextEncoder().encode(request.content).byteLength > REPOSITORY_MEMORY_MAX_FILE_SIZE) {
      throw new Error('Memory file exceeds the published size limit.');
    }
    return prepareMemoryFile(path, request.content);
  }
  if (request?.operation === 'read-dashboard-snapshot') {
    void reportBrowserStorageDiagnostics();
    return readDashboardSnapshotMetadata().then((snapshot) => {
      hasCompleteDashboardSnapshot = snapshot !== null;
      return snapshot;
    });
  }
  if (request?.operation === 'query-repository-memory') {
    return queryRepositoryMemory(/** @type {Record<string, unknown>} */ (request), signal);
  }
  if (request?.operation === 'query-canonical-dashboard') {
    if (isCliQueueQuery(dashboardContext(request.context).queries, requestedSourceNames(request.sourceNames))) {
      return queryCliQueue(request, signal);
    }
    const requested = requestedSourceNames(request.sourceNames);
    const context = dashboardContext(request.context);
    /** @param {typeof liveDashboard} [dashboard] */
    const query = (dashboard) => queryLiveDashboard(
      requested,
      context,
      /** @type {{ githubUrlBase?: string, dashboardRepository?: string | null }} */ (request.context ?? {}),
      signal,
      /** @type {Record<string, { limit: number, continuationToken?: string }>} */ (request.pagination ?? {}),
      typeof request.pageId === 'string' ? request.pageId : undefined,
      routeParameters(request.routeParameters),
      queryContext(request.queryContext),
      typeof request.viewId === 'string' ? request.viewId : undefined,
      dashboard
    );
    if (!resolveDashboardQuerySources(context.queries, requested).some(isGitHubQuerySource)) return query();
    return (async () => {
      const controller = new AbortController();
      const abort = () => controller.abort();
      signal?.addEventListener('abort', abort, { once: true });
      if (signal?.aborted) abort();
      /** @type {Record<string, import('./presenter.js').LogicalSourceInput>} */
      const githubSources = {};
      try {
        await subscribeGitHubQuerySources([...requested], {
          ...context, .../** @type {{ dashboardRepository?: string | null }} */ (request.context ?? {})
        }, request.githubConfiguration, controller.signal, (name, source) => { githubSources[name] = source; });
        const dashboard = await loadActiveDashboard();
        return await query({
          ...dashboard, hosted: request.hostedSources !== undefined,
          logicalSources: { ...dashboard.logicalSources, ...request.hostedSources, ...githubSources }
        });
      } finally {
        controller.abort();
        signal?.removeEventListener('abort', abort);
      }
    })();
  }
  if (request?.operation === 'load-canonical-dashboard') {
    if (typeof request.sourceUrl !== 'string' || !request.sourceUrl.trim()) {
      throw new TypeError('Canonical dashboard loading requires a source URL.');
    }
    const sourceUrl = new URL(request.sourceUrl);
    if (!['http:', 'https:'].includes(sourceUrl.protocol)) {
      throw new TypeError('Canonical dashboard source URL must use HTTP or HTTPS.');
    }
    if (typeof globalThis.location?.origin === 'string'
        && globalThis.location.origin !== 'null'
        && sourceUrl.origin !== globalThis.location.origin) {
      throw new TypeError('Canonical dashboard source URL must be same-origin.');
    }
    const requested = requestedSourceNames(request.sourceNames);
    const context = dashboardContext(request.context);
    return (async () => {
      const previousSnapshot = await readDashboardSnapshotMetadata();
      debugIngestion({ event: 'ingestion-start', snapshotPresent: previousSnapshot !== null, revision: liveDashboard?.revision ?? 0 });
      hasCompleteDashboardSnapshot = previousSnapshot !== null;
      dashboardIngestionCount += 1;
      const progress = startIngestionProgress(
        undefined,
        typeof request.id === 'number' ? request.id : undefined
      );
      /** @type {typeof fetch} */
      const ingestionFetch = (input, init) => fetch(input, { ...init, signal });
      const activity = sourceUrl.pathname.endsWith('/payload-hashes.json');
      if (!activity) progress.start();
      let changed = false;
      /** @type {Record<string, unknown>} */
      let sources = {};
      if (typeof request.id === 'number') {
        inFlightDashboardSources.set(request.id, sources);
      }
      try {
        progress.log(activity ? 'Loading ingestion metadata.' : 'Downloading dashboard source data.');
        sources = activity ? {} : await loadDashboardSources(ingestionFetch, sourceUrl.href, {
          onShardLoaded: ({ name, sizeBytes, cacheStatus }) => {
            const size = sizeBytes === null ? 'size unavailable' : `${sizeBytes.toLocaleString()} bytes`;
            progress.log(`Loaded dashboard source shard ${name} (${size}; cache: ${cacheStatus ?? 'unavailable'}).`);
          }
        });
        if (typeof request.id === 'number') inFlightDashboardSources.set(request.id, sources);
        if (activity) {
          const payloadHashesUrl = sourceUrl;
          const inventoryUrl = new URL('./inventory-sources.json', payloadHashesUrl);
          progress.log('Refreshing workflow and repository inventory.');
          const { response: inventoryResponse, value: inventorySources } = await withRetries(async () => {
            const response = await ingestionFetch(inventoryUrl, { cache: 'no-store' });
            if (!response.ok) {
              if (response.status === 404) return { response, value: {} };
              throw new Error(`Unable to load dashboard inventory sources: ${response.status}`);
            }
            return { response, value: await response.json() };
          });
          if (inventoryResponse.ok) {
            sources = inventorySources;
            if (typeof request.id === 'number') inFlightDashboardSources.set(request.id, sources);
            progress.log('Inventory metadata refreshed.');
            // Commit and publish the inventory-only sources before any activity
            // shard is downloaded so pages such as Marketplace render without
            // waiting for historical ingestion. The complete inventory payload
            // is still committed after shard ingestion, under its own scope, so
            // inventory keeps resolving shared structural records last.
            const inventoryPhaseSources = Object.fromEntries([...INVENTORY_PHASE_DATABASE_SOURCES]
              .filter((name) => Object.hasOwn(sources, name))
              .map((name) => [name, /** @type {Record<string, unknown>} */ (sources)[name]]));
            debugIngestion('planned inventory-phase publication', {
              sources: Object.keys(inventoryPhaseSources),
              inventorySources: Object.keys(/** @type {Record<string, unknown>} */ (sources)).length
            });
            if (Object.keys(inventoryPhaseSources).length > 0) {
              progress.log('Normalizing inventory-only metadata.');
              const inventoryPhaseIngestion = await ingestDashboardSources(indexedDB, inventoryPhaseSources, {
                storage: globalThis.navigator?.storage,
                retentionWindowMsByStore: BROWSER_RETENTION_WINDOWS_MS,
                payloadScope: `${inventoryUrl.href}#inventory-phase`,
                deferMaintenance: true,
                onWriteProgress: (written) => progress.store(written),
                onLockWait: () => progress.log(INGESTION_LOCK_WAIT_MESSAGE),
                signal
              });
              changed ||= inventoryPhaseIngestion.updated;
              if (signal?.aborted) throw new DashboardQueryCancelledError('data ingestion was cancelled', 'aborted');
              await refreshDashboardSubscriptions(
                /** @type {Record<string, import('./presenter.js').LogicalSourceInput>} */ (sources),
                'inventory'
              );
            }
          } else {
            progress.log('No separate inventory metadata was published.');
          }
          progress.log('Checking the published payload identity.');
          const payloadHashesResponse = await ingestionFetch(payloadHashesUrl, { cache: 'no-store' }).catch(() => null);
          if (signal?.aborted) throw new DashboardQueryCancelledError('data ingestion was cancelled', 'aborted');
          const payloadHashes = payloadHashesResponse?.ok
            ? await payloadHashesResponse.json().catch(() => null)
            : null;
          const runInformationShards = publishedRunInformationShards(payloadHashes);
          const phasedShards = publishedPhasedActivityShards(payloadHashes);
          const shardLimit = eagerIngest ? undefined : debugShardLimit();
          const shards = shardLimit === undefined ? phasedShards : phasedShards.slice(0, shardLimit);
          const shardCount = shards.length;
          // Reserve at least 10% for each post-shard stage, even for large manifests.
          const preparationStepWeight = Math.max(1, Math.ceil(shardCount / 7));
          const importSteps = shardCount + 3 * preparationStepWeight;
          debugIngestion('loaded activity manifest', {
            source: sourceUrl.pathname,
            shardCount,
            shardLimit,
            eagerIngest,
            manifestAvailable: payloadHashesResponse?.ok === true
          });
          if (shardCount > 0) {
            progress.log(`Published activity data includes ${shardCount.toLocaleString('en-US')} `
              + `${shardCount === 1 ? 'shard' : 'shards'}.`);
          }
          if (shards.length === 0) {
            throw new Error('Activity shard manifest is missing compacted run-information shards.');
          }
          /** @type {Array<{ index: number, shard: { name: string, hash: string }, shardUrl: URL, current: boolean, sizeBytes: number | undefined }>} */
          if (signal?.aborted) throw new DashboardQueryCancelledError('data ingestion was cancelled', 'aborted');
          const currentShards = await normalizedJsonlCurrentShards(indexedDB, shards.map((shard) => ({
            payloadIdentity: shard.hash,
            expectedPhase: shard.phase,
            retentionWindowMsByStore: BROWSER_RETENTION_WINDOWS_MS
          })));
          /** @type {Array<{ index: number, shard: { name: string, hash: string }, shardUrl: URL, current: boolean, sizeBytes: number | undefined }>} */
          const shardStates = [];
          for (const [index, shard] of shards.entries()) {
            const shardUrl = new URL(`./${shard.name}`, payloadHashesUrl);
            const current = currentShards[index];
            shardStates.push({ index, shard, shardUrl, current, sizeBytes: undefined });
          }
          const pendingShards = shardStates.filter(({ current }) => !current);
          const runPhaseShardCount = phasedShards.length > 0 && !eagerIngest
            ? Math.min(runInformationShards.length, shardStates.length)
            : 0;
          const initialPendingShards = runPhaseShardCount > 0
            ? pendingShards.filter(({ index }) => index < runPhaseShardCount)
            : pendingShards;
          debugIngestion('planned shard ingestion phases', {
            shardCount,
            pendingShardCount: pendingShards.length,
            runPhaseShardCount,
            initialPendingShardCount: initialPendingShards.length,
            eagerIngest
          });
          debugIngestion({
            event: 'shard-cache-checked',
            cached: shardStates.length - pendingShards.length,
            missing: pendingShards.length,
            cachedRuns: shardStates.slice(0, runInformationShards.length).filter(({ current }) => current).length,
            cachedRecords: shardStates.slice(runInformationShards.length).filter(({ current }) => current).length
          });
          if (pendingShards.length > 0) {
            progress.start();
            progress.reportImportProgress(shardStates.length - pendingShards.length, importSteps, 'files');
          }
          const measureShards = (/** @type {typeof pendingShards} */ states) => Promise.all(states.map(async (state) => {
            const response = await ingestionFetch(state.shardUrl, { method: 'HEAD' }).catch(() => null);
            const contentLength = response?.ok ? Number(response.headers.get('content-length')) : Number.NaN;
            state.sizeBytes = Number.isFinite(contentLength) && contentLength >= 0 ? contentLength : undefined;
          }));
          await measureShards(initialPendingShards);
          let workloadBytes = initialPendingShards.every(({ sizeBytes }) => typeof sizeBytes === 'number')
            ? initialPendingShards.reduce((sum, { sizeBytes }) => sum + (sizeBytes ?? 0), 0)
            : undefined;
          progress.setWorkload(workloadBytes);
          let completedShardCount = shardStates.length - pendingShards.length;
          progress.reportImportProgress(completedShardCount, importSteps, 'files');
          if (completedShardCount > 0) {
            progress.log(`Reusing ${completedShardCount}/${shardCount} cached activity `
              + `${completedShardCount === 1 ? 'shard' : 'shards'}.`);
          }
          let processedBytes = 0;
          let processedRecords = 0;
          for (const { index, shard, shardUrl, current, sizeBytes } of shardStates) {
            if (signal?.aborted) throw new DashboardQueryCancelledError('data ingestion was cancelled', 'aborted');
            if (runPhaseShardCount > 0 && index === runPhaseShardCount) {
              await refreshDashboardSubscriptions(
                /** @type {Record<string, import('./presenter.js').LogicalSourceInput>} */ (sources),
                'runs'
              );
              const eventPendingShards = pendingShards.filter((state) => state.index >= runPhaseShardCount);
              await measureShards(eventPendingShards);
              workloadBytes = pendingShards.every(({ sizeBytes }) => typeof sizeBytes === 'number')
                ? pendingShards.reduce((sum, state) => sum + (state.sizeBytes ?? 0), 0)
                : undefined;
              progress.setWorkload(workloadBytes);
            }
            if (current) {
              debugIngestion('skipping current activity shard', {
                shard: shard.name,
                index: index + 1,
                shardCount
              });
              continue;
            }
            progress.log(`Downloading shard ${index + 1}/${shardCount}.`);
            debugIngestion('fetching activity shard', {
              shard: shard.name,
              index: index + 1,
              shardCount
            });
            const response = await ingestionFetch(shardUrl);
            if (!response.ok) throw new Error(`Unable to load activity shard ${shard.name}: ${response.status}`);
            if (!response.body) throw new Error(`Unable to stream activity shard ${shard.name}`);
            {
              const contentLengthHeader = response.headers.get('content-length');
              const contentLength = contentLengthHeader === null ? Number.NaN : Number(contentLengthHeader);
              const payloadBytes = sizeBytes ?? (Number.isFinite(contentLength) && contentLength >= 0
                ? contentLength
                : undefined);
              const compressed = response.headers.has('content-encoding');
              progress.log(payloadBytes === undefined
                ? `Shard ${index + 1} received; parsing.`
                : `Shard ${index + 1} received (${formatDataSize(payloadBytes)}`
                  + `${compressed ? ' compressed' : ''}); parsing.`);
              const expectedPhase = 'phase' in shard && (shard.phase === 'runs' || shard.phase === 'records')
                ? /** @type {'runs' | 'records'} */ (shard.phase)
                : undefined;
              const ingestionOptions = {
                storage: globalThis.navigator?.storage,
                retentionWindowMsByStore: BROWSER_RETENTION_WINDOWS_MS,
                onWriteProgress: (/** @type {{ storedRecords: number, totalRecords: number }} */ written) => progress.store(written),
                onLockWait: () => progress.log(INGESTION_LOCK_WAIT_MESSAGE),
                signal,
                payloadIdentity: shard.hash,
                payloadScope: shardUrl.href,
                expectedPhase,
                deferMaintenance: true
              };
              const ingestion = await ingestNormalizedJsonl(
                indexedDB,
                responseChunks(/** @type {ReadableStream<Uint8Array>} */ (response.body)),
                ingestionOptions
              );
              processedBytes += payloadBytes ?? 0;
              const sourceRecords = 'records' in ingestion ? ingestion.records : 0;
              processedRecords += sourceRecords;
              changed ||= ingestion.updated;
              debugIngestion('committed activity shard', {
                shard: shard.name,
                index: index + 1,
                shardCount,
                committedRecords: ingestion.committedRecords,
                sourceRecords
              });
              progress.log(`Shard ${index + 1}/${shardCount} committed `
                + `${ingestion.committedRecords.toLocaleString('en-US')} rec.`);
              progress.update({
                bytesProcessed: processedBytes,
                recordsIngested: processedRecords,
                totalBytes: workloadBytes
              });
              completedShardCount += 1;
              progress.reportImportProgress(completedShardCount, importSteps, 'files');
            }
          }
          progress.reportImportProgress(shardCount, importSteps, 'inventory');
          if (inventoryResponse.ok) {
            progress.log('Normalizing inventory metadata.');
            const inventoryIngestion = await ingestDashboardSources(indexedDB, sources, {
              storage: globalThis.navigator?.storage,
              retentionWindowMsByStore: BROWSER_RETENTION_WINDOWS_MS,
              payloadScope: inventoryUrl.href,
              deferMaintenance: true,
              onWriteProgress: (written) => progress.store(written),
              onLockWait: () => progress.log(INGESTION_LOCK_WAIT_MESSAGE),
              signal
            });
            changed ||= inventoryIngestion.updated;
            progress.log('skipped' in inventoryIngestion && inventoryIngestion.skipped
              ? 'Inventory metadata is already current.'
              : `Inventory ingestion committed ${inventoryIngestion.committedRecords} canonical records.`);
          }
          progress.log('Applying retention limits.');
          progress.reportImportProgress(shardCount + preparationStepWeight, importSteps, 'maintenance');
          const maintenance = await finalizeNormalizedJsonlIngestion(indexedDB, {
            storage: globalThis.navigator?.storage,
            retentionWindowMsByStore: BROWSER_RETENTION_WINDOWS_MS,
            onMaintenanceProgress: (completed, total) =>
              progress.reportImportProgress(shardCount + preparationStepWeight
                + preparationStepWeight * 0.9 * completed / total, importSteps, 'maintenance'),
            onLockWait: () => progress.log(INGESTION_LOCK_WAIT_MESSAGE),
            signal
          });
          changed ||= maintenance.deletedRecords > 0 || maintenance.prunedFailureLogs > 0;
          progress.reportImportProgress(shardCount + 2 * preparationStepWeight, importSteps, 'queries');
        } else {
          progress.log('Normalizing dashboard source data.');
          progress.reportImportProgress(0, 3, 'inventory');
          const ingestion = await ingestDashboardSources(indexedDB, sources, {
            storage: globalThis.navigator?.storage,
            retentionWindowMsByStore: BROWSER_RETENTION_WINDOWS_MS,
            payloadScope: sourceUrl.href,
            onWriteProgress: (written) => progress.store(written),
            onLockWait: () => progress.log(INGESTION_LOCK_WAIT_MESSAGE),
            signal
          });
          changed ||= ingestion.updated;
          progress.log('skipped' in ingestion && ingestion.skipped
            ? 'Dashboard source data is already current.'
            : `Dashboard ingestion committed ${ingestion.committedRecords} canonical records.`);
          progress.reportImportProgress(2, 3, 'queries');
        }
        if (signal?.aborted) throw new DashboardQueryCancelledError('data ingestion was cancelled', 'aborted');
        progress.log('Refreshing active dashboard queries.');
        await recordTransaction(indexedDB, {
          id: DASHBOARD_SNAPSHOT_TRANSACTION_ID,
          kind: 'dashboard-snapshot',
          createdAt: new Date().toISOString()
        });
        debugIngestion({ event: 'snapshot-saved', schemaVersion: DATABASE_VERSION, changed });
        hasCompleteDashboardSnapshot = true;
        // A different tab may have committed the current payload to IndexedDB,
        // leaving this worker's in-memory query results stale even when this
        // ingestion reports no local writes.
        const nextRevision = (liveDashboard?.revision ?? 0) + 1;
        debugIngestion({ event: 'dashboard-revision', previous: liveDashboard?.revision ?? 0, next: nextRevision, phase: 'complete' });
        liveDashboard = {
          logicalSources: /** @type {Record<string, import('./presenter.js').LogicalSourceInput>} */ (sources),
          revision: nextRevision
        };
        publicationPhase = 'complete';
        scheduleDashboardSubscriptions();
        const projected = await queryLiveDashboard(
          requested,
          context,
          /** @type {{ githubUrlBase?: string, dashboardRepository?: string | null }} */ (request.context ?? {}),
          signal,
          /** @type {Record<string, { limit: number, continuationToken?: string }>} */ (request.pagination ?? {}),
          typeof request.pageId === 'string' ? request.pageId : undefined,
          routeParameters(request.routeParameters),
          queryContext(request.queryContext),
          typeof request.viewId === 'string' ? request.viewId : undefined
        );
        progress.complete();
        return request.reportActivation
          ? { sources: projected, changed }
          : projected;
      } finally {
        if (typeof request.id === 'number') inFlightDashboardSources.delete(request.id);
        progress.complete();
        dashboardIngestionCount = Math.max(0, dashboardIngestionCount - 1);
        if (dashboardIngestionCount === 0) scheduleDashboardSubscriptions(dirtyDashboardSubscriptions);
      }
    })();
  }
  if (request?.operation === 'execute-dashboard-queries') {
    if (!request.sources || typeof request.sources !== 'object' || Array.isArray(request.sources)) {
      throw new TypeError('Dashboard query requests require a sources object.');
    }
    if (!Array.isArray(request.queries)) {
      throw new TypeError('Dashboard query requests require a queries array.');
    }
    const requested = request.sourceNames === undefined
      ? undefined
      : requestedSourceNames(request.sourceNames);
    const querySources = withQueryProgress(() => ({ ...executeDashboardQueries(
      request.queries,
      /** @type {Record<string, import('./presenter.js').LogicalSourceInput>} */ (request.sources),
      requested,
      {
        signal,
        pagination: /** @type {Record<string, { limit: number, continuationToken?: string }>} */ (request.pagination ?? {})
      }
    ) }));
    return querySources;
  }
  if (request?.operation === 'load-dashboard-query-sources') {
    if (!request.sources || typeof request.sources !== 'object' || Array.isArray(request.sources)) {
      throw new TypeError('Dashboard database query requests require a sources object.');
    }
    return withQueryProgress(async () => {
      const sources = /** @type {Record<string, unknown>} */ (request.sources);
      const sourceNames = request.sourceNames === undefined
        ? Object.keys(sources)
        : [...requestedSourceNames(request.sourceNames)];
      const queries = Array.isArray(request.queries) ? request.queries : [];
      const required = new Set(resolveDashboardQuerySources(queries, sourceNames));
      const database = /** @type {Record<string, import('./presenter.js').LogicalSourceInput>} */ (
        await loadDatabaseQuerySources(indexedDB, sources, {
          ingest: request.ingest === true, sourceNames, queries, signal
        })
      );
      return {
        ...database,
        ...paginateDashboardSources(
          pageScopedSources(database, new Set(sourceNames)),
          /** @type {Record<string, { limit: number, continuationToken?: string }>} */ (request.pagination ?? {}),
          continuationRevision(queries, {
            inputs: Object.fromEntries(Object.entries(sources)
              .filter(([name]) => required.has(name))
              .map(([name, source]) => [
                name, source && typeof source === 'object' && 'metadata' in source ? source.metadata : null
              ])),
            outputs: Object.fromEntries(Object.entries(database).map(([name, source]) => [name, source.metadata]))
          })
        )
      };
    });
  }
  if (request?.operation === 'query-canonical-database-diagnostics') {
    return collectCanonicalDatabaseDiagnostics();
  }
  if (request?.operation === 'summarize-table-columns') {
    if (!Array.isArray(request.columns)) {
      throw new TypeError('Table summary requests require a columns array.');
    }
    return summarizeTableColumns(request.columns);
  }
  if (request?.operation === 'cluster-scatter-points') {
    if (!Array.isArray(request.data)) {
      throw new TypeError('Scatter clustering requests require a data array.');
    }
    const limit = Number(request.limit);
    if (!Number.isSafeInteger(limit) || limit <= 0) {
      throw new TypeError('Scatter clustering requests require a positive integer limit.');
    }
    return clusterScatterPoints(request.data, limit);
  }
  if (request?.operation === 'canonicalize-dashboard-sources') {
    if (!request.sources || typeof request.sources !== 'object' || Array.isArray(request.sources)) {
      throw new TypeError('Dashboard ingestion requests require a sources object.');
    }
    const adapted = queryDashboardSourceObservations(/** @type {Record<string, unknown>} */ (request.sources));
    return normalize(adapted.observations);
  }
  if (!Array.isArray(request?.data) || !Array.isArray(request?.operators)) {
    throw new TypeError('Data worker requests require data and operators arrays.');
  }
  return tidy(request.data, request.operators);
}

/** @type {Map<number, AbortController>} */
const inFlight = new Map();
/** @type {Map<number, string>} */
const inFlightOperations = new Map();
/** @type {Map<string, AbortController>} */
const queueSubscriptions = new Map();

/** @param {Record<string, any>} request */
async function subscribeCliQueue(request) {
  const id = request.subscriptionId;
  queueSubscriptions.get(id)?.abort();
  const controller = new AbortController();
  queueSubscriptions.set(id, controller);
  try {
    while (!controller.signal.aborted) {
      try {
        const data = await queryCliQueue(request, controller.signal);
        if (!controller.signal.aborted) workerScope?.postMessage({ subscriptionId: id, data });
        if (!cliQueueEnabled()) break;
      } catch (error) {
        if (!controller.signal.aborted) workerScope?.postMessage({
          subscriptionId: id, error: error instanceof Error ? error.message : String(error)
        });
      }
      await new Promise((resolve) => {
        const finish = () => {
          clearTimeout(timer);
          controller.signal.removeEventListener('abort', finish);
          resolve(undefined);
        };
        const timer = setTimeout(finish, 1500);
        controller.signal.addEventListener('abort', finish, { once: true });
        if (controller.signal.aborted) finish();
      });
    }
  } finally {
    if (queueSubscriptions.get(id) === controller) queueSubscriptions.delete(id);
  }
}
const debugRequests = createDebug('data:worker');

/**
 * Cancels the identified in-flight requests, or every in-flight request when
 * no identifier is supplied.
 * @param {unknown} ids
 */
function cancelInFlight(ids) {
  const targets = Array.isArray(ids) && ids.length
    ? ids.filter((id) => inFlight.has(/** @type {number} */ (id)))
    : [...inFlight.keys()];
  for (const id of targets) inFlight.get(/** @type {number} */ (id))?.abort();
  return targets.length;
}

if (typeof document === 'undefined' && workerScope) {
  const githubResources = githubDataService ??= createGitHubDataService();
  /** @type {Map<string, AbortController>} */
  const githubSubscriptions = new Map();
  workerScope.addEventListener('message', (event) => {
    const id = event.data?.id;
    if (event.data?.operation === 'configure-cli-queue') {
      configureCliQueue(event.data.enabled === true, event.data.endpoint);
      return;
    }
    if (event.data?.operation === 'invalidate-github-authentication') {
      githubAuthenticationEpoch = Number.isSafeInteger(event.data.epoch) ? event.data.epoch : githubAuthenticationEpoch + 1;
      githubResources.invalidateAuthentication();
      return;
    }
    if (event.data?.operation === 'subscribe-github-resource') {
      const subscriptionId = event.data.subscriptionId;
      if (typeof subscriptionId !== 'string' || !subscriptionId) return;
      githubSubscriptions.get(subscriptionId)?.abort();
      const controller = new AbortController();
      githubSubscriptions.set(subscriptionId, controller);
      try {
        githubResources.subscribe(event.data.resource, event.data.configuration, (snapshot) => {
          workerScope.postMessage({ type: 'github-resource', subscriptionId, snapshot, githubEpoch: githubAuthenticationEpoch });
        }, { signal: controller.signal });
      } catch {
        controller.abort();
        githubSubscriptions.delete(subscriptionId);
        workerScope.postMessage({ type: 'github-resource', subscriptionId, error: 'Unable to subscribe to GitHub resource.', githubEpoch: githubAuthenticationEpoch });
      }
      return;
    }
    if (event.data?.operation === 'unsubscribe-github-resource') {
      githubSubscriptions.get(event.data.subscriptionId)?.abort();
      githubSubscriptions.delete(event.data.subscriptionId);
      return;
    }
    if (event.data?.operation === 'update-github-dashboard-sources') {
      const subscription = dashboardSubscriptions.get(event.data.subscriptionId);
      if (subscription?.githubSources && subscription.hostedSources !== undefined) {
        subscription.hostedSources = event.data.sources;
        if (liveDashboard) liveDashboard = { ...liveDashboard, revision: liveDashboard.revision + 1 };
        scheduleDashboardSubscriptions([event.data.subscriptionId]);
      }
      return;
    }
    if (event.data?.operation === 'subscribe-canonical-dashboard') {
      if (isCliQueueQuery(event.data.context?.queries ?? [], event.data.sourceNames ?? [])) {
        void subscribeCliQueue(event.data);
        return;
      }
      const subscriptionId = event.data.subscriptionId;
      if (typeof subscriptionId !== 'string' || !subscriptionId.trim()) return;
      try {
        const context = dashboardContext(event.data.context);
        const subscription = /** @type {DashboardSubscription} */ ({
          sourceNames: [...requestedSourceNames(event.data.sourceNames)],
          context,
          requestContext: /** @type {{ githubUrlBase?: string, dashboardRepository?: string | null }} */ (event.data.context ?? {}),
          pagination: /** @type {Record<string, { limit: number, continuationToken?: string }>} */ (event.data.pagination ?? {}),
          pageId: typeof event.data.pageId === 'string' ? event.data.pageId : undefined,
          viewId: typeof event.data.viewId === 'string' ? event.data.viewId : undefined,
          routeParameters: routeParameters(event.data.routeParameters),
          queryContext: queryContext(event.data.queryContext),
          revision: liveDashboard?.revision ?? null,
          emitted: false
        });
        dashboardSubscriptions.get(subscriptionId)?.controller?.abort();
        dashboardSubscriptions.get(subscriptionId)?.githubController?.abort();
        dashboardSubscriptions.set(subscriptionId, subscription);
        if (resolveDashboardQuerySources(context.queries, subscription.sourceNames).some(isGitHubQuerySource)) {
          subscription.githubController = new AbortController();
          subscription.githubSources = {};
          subscription.hostedSources = event.data.hostedSources;
          liveDashboard ??= { logicalSources: {}, revision: 0 };
          void subscribeGitHubQuerySources(subscription.sourceNames, {
            ...context, dashboardRepository: subscription.requestContext.dashboardRepository
          }, event.data.githubConfiguration, subscription.githubController.signal, (name, source) => {
            if (dashboardSubscriptions.get(subscriptionId) !== subscription) return;
            subscription.githubSources ??= {};
            subscription.githubSources[name] = source;
            if (liveDashboard) liveDashboard = { ...liveDashboard, revision: liveDashboard.revision + 1 };
            scheduleDashboardSubscriptions([subscriptionId]);
          }).catch(() => {
            if (dashboardSubscriptions.get(subscriptionId) === subscription) workerScope.postMessage({
              subscriptionId, githubEpoch: githubAuthenticationEpoch, error: 'GitHub dashboard source unavailable.'
            });
          });
        }
        if (liveDashboard && event.data.emitCurrent !== false) {
          if (isPhaseSubscription(subscription, publicationPhase)
              || subscriptionPartialViews(subscription).length > 0) {
            scheduleDashboardSubscriptions([subscriptionId]);
          }
        }
      } catch (error) {
        // Remove any partially-registered subscription so it cannot linger
        // as a stale, unusable entry, then report the failure so the main
        // thread's pending page load rejects instead of waiting forever for
        // a snapshot that will never arrive.
        dashboardSubscriptions.delete(subscriptionId);
        workerScope.postMessage({
          subscriptionId,
          error: error instanceof Error ? error.message : String(error)
        });
      }
      return;
    }
    if (event.data?.operation === 'unsubscribe-canonical-dashboard') {
      queueSubscriptions.get(event.data.subscriptionId)?.abort();
      dashboardSubscriptions.get(event.data.subscriptionId)?.controller?.abort();
      dashboardSubscriptions.get(event.data.subscriptionId)?.githubController?.abort();
      dashboardSubscriptions.delete(event.data.subscriptionId);
      dirtyDashboardSubscriptions.delete(event.data.subscriptionId);
      if (dirtyDashboardSubscriptions.size === 0 && subscriptionFlushTimer !== null) {
        clearTimeout(subscriptionFlushTimer);
        subscriptionFlushTimer = null;
      }
      return;
    }
    if (event.data?.operation === 'sync-dashboard-queries') {
      const requestId = event.data.requestId;
      const sources = Number.isSafeInteger(requestId)
        ? inFlightDashboardSources.get(requestId)
        : undefined;
      if (sources) {
        void refreshDashboardSubscriptions(
          /** @type {Record<string, import('./presenter.js').LogicalSourceInput>} */ (sources),
          'complete'
        );
      }
      return;
    }
    if (event.data?.operation === 'cancel-data-processing') {
      const cancelled = cancelInFlight(event.data.ids);
      debugRequests({
        event: 'cancel-received',
        requestedCount: Array.isArray(event.data.ids) ? event.data.ids.length : 0,
        cancelled,
        inFlightOperations: [...inFlightOperations.values()]
      });
      workerScope.postMessage({ id, data: { cancelled } });
      return;
    }
    const controller = new AbortController();
    const operation = typeof event.data?.operation === 'string' ? event.data.operation : 'unknown';
    const startedAt = monotonicNow();
    inFlight.set(id, controller);
    inFlightOperations.set(id, operation);
    debugRequests({ event: 'request-started', id, operation, inFlightCount: inFlight.size });
    /** @param {unknown} error */
    const failure = (error) => ({
      error: error instanceof Error ? error.message : String(error),
      cancelled: controller.signal.aborted || error instanceof DashboardQueryCancelledError
    });
    const settle = (/** @type {Record<string, unknown>} */ message) => {
      inFlight.delete(id);
      inFlightOperations.delete(id);
      debugRequests({
        event: 'request-settled',
        id,
        operation,
        status: 'error' in message ? (message.cancelled ? 'cancelled' : 'failed') : 'succeeded',
        durationMs: Math.round(monotonicNow() - startedAt),
        inFlightCount: inFlight.size
      });
      try {
        workerScope.postMessage({ id, ...message });
      } catch (error) {
        workerScope.postMessage({ id, ...failure(error) });
      }
    };
    try {
      Promise.resolve(processDataRequest(event.data, controller.signal)).then(
        (data) => settle({ data }),
        (error) => settle(failure(error))
      );
    } catch (error) {
      settle(failure(error));
    }
  });
}

/** @param {unknown} value */
function routeParameters(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
  return Object.fromEntries(Object.entries(value)
    .filter(([, item]) => typeof item === 'string')
    .map(([key, item]) => [key, String(item)]));
}

/** @param {unknown} value @returns {DashboardSubscription['queryContext']} */
function queryContext(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
  const context = /** @type {{ filters?: unknown, viewFilters?: unknown, search?: unknown, orderBy?: unknown, timeWindow?: unknown, viewMode?: unknown, formValues?: unknown }} */ (value);
  const filters = context.filters && typeof context.filters === 'object' && !Array.isArray(context.filters)
    ? Object.fromEntries(Object.entries(context.filters)
      .map(([field, candidates]) => [field, Array.isArray(candidates)
        ? candidates.filter((item) => typeof item === 'string').map(String)
        : []])
      .filter(([, candidates]) => candidates.length > 0))
    : undefined;
  const rawSearch = context.search && typeof context.search === 'object' && !Array.isArray(context.search)
    ? /** @type {Record<string, unknown>} */ (context.search)
    : null;
  const searchFields = rawSearch && Array.isArray(rawSearch.fields)
    ? rawSearch.fields.filter((field) => typeof field === 'string' && field.trim()).map(String)
    : [];
  const searchQuery = rawSearch && typeof rawSearch.query === 'string' ? rawSearch.query.trim() : '';
  const search = searchQuery && searchFields.length > 0 ? { fields: searchFields, query: searchQuery } : undefined;
  const orderBy = Array.isArray(context.orderBy)
    ? context.orderBy.flatMap((ordering) => {
        if (!ordering || typeof ordering !== 'object' || Array.isArray(ordering)) return [];
        const candidate = /** @type {Record<string, unknown>} */ (ordering);
        if (typeof candidate.field !== 'string' || !candidate.field.trim()) return [];
        const direction = candidate.direction === 'asc' || candidate.direction === 'desc'
          ? /** @type {'asc'|'desc'} */ (candidate.direction)
          : undefined;
        return [{ field: candidate.field, ...(direction ? { direction } : {}) }];
      })
    : [];
  const rawTimeWindow = context.timeWindow && typeof context.timeWindow === 'object' && !Array.isArray(context.timeWindow)
    ? /** @type {Record<string, unknown>} */ (context.timeWindow)
    : null;
  const timeWindow = rawTimeWindow
    ? {
        start: typeof rawTimeWindow.start === 'string' ? rawTimeWindow.start : undefined,
        end: typeof rawTimeWindow.end === 'string' ? rawTimeWindow.end : undefined
      }
    : undefined;
  const viewMode = context.viewMode === 'chart' || context.viewMode === 'table' || context.viewMode === 'card'
    ? context.viewMode
    : undefined;
  const formValues = context.formValues && typeof context.formValues === 'object' && !Array.isArray(context.formValues)
    ? Object.fromEntries(Object.entries(context.formValues)
      .filter(([, entry]) => typeof entry === 'string' || typeof entry === 'boolean'
        || (typeof entry === 'number' && Number.isFinite(entry))))
    : undefined;
  const viewFilters = normalizeViewFilters(context.viewFilters);
  return {
    ...(filters ? { filters } : {}),
    ...(search ? { search } : {}),
    ...(orderBy.length > 0 ? { orderBy } : {}),
    ...(timeWindow?.start || timeWindow?.end ? { timeWindow } : {}),
    ...(viewMode ? { viewMode } : {}),
    ...(viewFilters ? { viewFilters } : {}),
    ...(formValues ? { formValues } : {})
  };
}
