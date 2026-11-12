import {
  loadDashboardSnapshotMetadata,
  loadCanonicalDashboardPage,
  refreshCanonicalDashboardSources,
  subscribeDatabaseUpgrade,
  subscribeCanonicalDashboardView,
  subscribeWorkerLoadingProgress,
} from "../data-processor.js";
import { bindSourceContinuations, continuationRequests } from "./continuation.js";
import { DASHBOARD_DATA_EVENT, emitDashboardDebugEvent } from "../debug-events.js";
import {
  DASHBOARD_REFRESH_REQUEST_EVENT,
  startAutomaticDashboardDataUpdates,
} from "../dashboard-data-updates.js";
import { configureSourceLoader, refreshSources as refreshBoundSources } from "../source-store.js";
import { dashboardViewAliasName } from "./queries/view-payload-compiler.js";
import { usesRemoteDataBackend } from "../remote-data-backend.js";
import { createDebug, diagnosticErrorName } from "../debug.js";
import { publishNotification, resolveNotification } from "../notification-service.js";
import { browserFirstLoad, recordPreparationEvent, recordPreparationStorage, resetPreparationDiagnostics, finishPreparationDiagnostics } from "../browser-first-load.js";
import { inspectBrowserStorage, TEMPORARY_STORAGE_WARNING } from "../browser-storage.js";
import { dashboardDataStatus } from "../dashboard-data-status.js";
import { mountFirstLoadOverlay } from "../components/first-load-overlay.js";
import { DEMO_MODE } from "../deployment-profile.js";

const debugStartup = createDebug("startup");

/** @typedef {{ pageId?: string, viewId?: string, sourceIndex?: number, routeParameters?: Record<string, string>, queryContext?: DashboardQueryContext }} BatchedSourceOptions */

/**
 * Coalesces source requests issued by one page in the same turn so the worker
 * reads and projects shared canonical dependencies only once. Requests with
 * distinct query contexts remain isolated.
 *
 * @param {{ githubUrlBase?: string, dashboardRepository?: string | null, pages: unknown[], queries?: unknown[], views?: unknown[] }} dashboardContext
 * @returns {(name: string, options?: BatchedSourceOptions) => Promise<import("../presenter.js").LogicalSourceInput | undefined>}
 */
export function createBatchedSourceLoader(dashboardContext) {
  /** @type {Array<{ name: string, options: BatchedSourceOptions, resolve: (source: import("../presenter.js").LogicalSourceInput | undefined) => void, reject: (error: unknown) => void }>} */
  let pending = [];
  let scheduled = false;

  const flush = async () => {
    scheduled = false;
    const requests = pending;
    pending = [];
    try {
      /** @type {Map<string, typeof requests>} */
      const groups = new Map();
      for (const request of requests) {
        const key = JSON.stringify([
          request.options?.pageId ?? null,
          request.options?.routeParameters ?? null,
          request.options?.queryContext ?? null,
        ]);
        const group = groups.get(key) ?? [];
        group.push(request);
        groups.set(key, group);
      }

      debugStartup({
        op: "batched-source-flush",
        requestCount: requests.length,
        groupCount: groups.size,
      });

      await Promise.all([...groups.values()].map(async (group) => {
        const [{ options }] = group;
        const names = [...new Set(group.map(({ name }) => name))];
        const viewIds = new Set(group.map(({ options: requestOptions }) => requestOptions?.viewId));
        try {
          const sources = await loadCanonicalDashboardPage(
            names,
            dashboardContext,
            undefined,
            {
              pageId: options?.pageId,
              viewId: viewIds.size === 1 ? options?.viewId : undefined,
              routeParameters: options?.routeParameters,
              queryContext: options?.queryContext,
            },
          );
          for (const request of group) {
            const alias = request.options?.pageId && request.options.viewId
              ? dashboardViewAliasName(
                  request.options.pageId,
                  { id: request.options.viewId },
                  0,
                  request.name,
                  request.options.sourceIndex ?? 0,
                )
              : request.name;
            request.resolve(sources[alias] ?? sources[request.name]);
          }
        } catch (error) {
          for (const request of group) request.reject(error);
        }
      }));
    } catch (error) {
      for (const request of requests) request.reject(error);
    }
  };

  return (name, options = {}) => new Promise((resolve, reject) => {
    pending.push({ name, options, resolve, reject });
    if (scheduled) return;
    scheduled = true;
    queueMicrotask(() => void flush());
  });
}

/** @typedef {Record<string, import('../presenter.js').LogicalSourceInput>} DashboardSources */
/** @typedef {import('./queries/view-payload-compiler.js').GlobalQueryContext} DashboardQueryContext */
/** @typedef {{ signal: AbortSignal, onUpdate: (sources: DashboardSources) => void, routeParameters?: Record<string, string>, queryContext?: DashboardQueryContext }} PageLoadOptions */
/** @typedef {((pageId: string, options: PageLoadOptions) => Promise<DashboardSources>) & { prepare?: (pageId: string) => Promise<void>, subscribeBackgroundSources?: (sourceNames: string[], options: PageLoadOptions) => Promise<DashboardSources>, subscribeViewSources?: (pageId: string, viewId: string, sourceNames: string[], options: PageLoadOptions) => Promise<DashboardSources> }} PageSourceLoader */

/**
 * Gives a cached render two animation frames to commit before network activity starts.
 * @param {Window} browserWindow
 * @returns {Promise<void>}
 */
export function waitForDashboardUi(browserWindow) {
  return new Promise((resolve) => {
    /** @type {(callback: FrameRequestCallback) => number} */
    const schedule = browserWindow.requestAnimationFrame
      ? (callback) => browserWindow.requestAnimationFrame(callback)
      : (callback) => browserWindow.setTimeout(() => callback(performance.now()), 0);
    schedule(() => schedule(() => resolve()));
  });
}

/**
 * Starts the live dashboard data pipeline without depending on the view engine.
 *
 * @param {{
 *   browserWindow: Window,
 *   document: Document,
 *   sourceUrl: string,
 *   dashboardContext: {
 *     githubUrlBase?: string,
 *     dashboardRepository?: string | null,
 *     pages: import('../presenter.js').PresentationDocument['dashboard']['pages'],
 *     queries: unknown[],
 *     callouts?: import('../components/site-callout.js').SiteCallout[],
 *   },
 *   preparePage?: (pageId: string) => Promise<void>,
 *   pageSourceNames: (pageId: string, viewMode?: 'chart'|'table'|'card') => string[],
 *   pagePaginatedSourceBindings: (pageId: string) => Record<string, { sourceName: string, viewId: string }>,
 *   render: (sources: DashboardSources, state: 'ready' | 'loading' | 'cached' | 'stale', loadPageSources: PageSourceLoader, retryRefresh?: () => void, snapshot?: { createdAt: string } | null) => void,
 *   settleUi?: () => Promise<void>,
 * }} options
 * @returns {Promise<() => void>}
 */
export async function startDashboardData(options) {
  resetPreparationDiagnostics();
  recordPreparationEvent("startup");
  dashboardDataStatus.set({ phase: "loading", snapshot: null });
  const {
    browserWindow,
    document,
    sourceUrl,
    dashboardContext,
    preparePage,
    pageSourceNames,
    pagePaginatedSourceBindings,
    render,
    settleUi = () => waitForDashboardUi(browserWindow),
  } = options;
  const cleanup = new AbortController();
  const failedSubscriptions = new Set();
  /** @type {ReturnType<typeof publishNotification> | undefined} */
  let updateNotice;
  const clearUpdateNotice = (resolved = true) => {
    if (failedSubscriptions.size) return;
    updateNotice?.dismiss();
    if (resolved) resolveNotification("dashboard:subscription-error", document);
    updateNotice = undefined;
  };
  cleanup.signal.addEventListener("abort", () => {
    finishPreparationDiagnostics();
    failedSubscriptions.clear();
    clearUpdateNotice(false);
  }, { once: true });
  let nextViewSubscriptionId = 0;
  let stopAutomaticDataUpdates = () => {};
  /** @type {() => void} */
  let startBackgroundWork = () => {};
  const backgroundWorkStarted = new Promise((resolve) => {
    startBackgroundWork = () => resolve(undefined);
  });
  browserWindow.addEventListener("pagehide", (event) => {
    if (!event.persisted) {
      startBackgroundWork();
      cleanup.abort();
      stopAutomaticDataUpdates();
    }
  });

  /**
   * @param {string} pageId
   * @param {DashboardSources} sources
   * @param {Record<string, { sourceName: string, viewId: string }>} bindings
   * @param {{ sourceNames: string[], viewId?: string }} scope
   * @param {Pick<PageLoadOptions, 'routeParameters' | 'queryContext'>} [pageOptions]
   */
  const bindContinuations = (pageId, sources, bindings, scope, pageOptions = {}) => bindSourceContinuations(
    sources,
    Object.keys(bindings),
    (_requested, pagination) => {
      return loadCanonicalDashboardPage(
        scope.sourceNames,
        dashboardContext,
        pagination,
        {
          pageId,
          viewId: scope.viewId,
          routeParameters: pageOptions.routeParameters,
          queryContext: pageOptions.queryContext,
        },
      );
    },
  );
  /**
   * Subscribes to a bounded source set. The returned promise resolves with the
   * first snapshot; later snapshots are delivered through `pageOptions.onUpdate`.
   * @param {{ subscriptionId: string, sourceNames: string[], pageOptions: PageLoadOptions & { pageId?: string, viewId?: string }, pagination?: Record<string, { limit: number, continuationToken?: string }>, transform?: (sources: DashboardSources) => DashboardSources }} options
   */
  const subscribeSources = (options) => {
    const pageOptions = options.pageOptions;
    const transform = options.transform ?? ((sources) => sources);
    if (pageOptions.signal.aborted) {
      throw new DOMException("Dashboard source load was cancelled.", "AbortError");
    }
    return new Promise((resolve, reject) => {
      let receivedInitialSnapshot = false;
      pageOptions.signal.addEventListener("abort", () => {
        if (failedSubscriptions.delete(options.subscriptionId)) clearUpdateNotice();
      }, { once: true });
      const cleanup = () => pageOptions.signal.removeEventListener("abort", abort);
      const abort = () => {
        cleanup();
        reject(new DOMException("Dashboard source load was cancelled.", "AbortError"));
      };
      pageOptions.signal.addEventListener("abort", abort, { once: true });
      subscribeCanonicalDashboardView(
        options.subscriptionId,
        options.sourceNames,
        dashboardContext,
        (sources) => {
          if (failedSubscriptions.delete(options.subscriptionId)) clearUpdateNotice();
          const transformedSources = transform(sources);
          if (!receivedInitialSnapshot) {
            receivedInitialSnapshot = true;
            cleanup();
            resolve(transformedSources);
            return;
          }
          pageOptions.onUpdate(transformedSources);
        },
        options.pagination ?? {},
        {
          signal: pageOptions.signal,
          pageId: pageOptions.pageId,
          viewId: pageOptions.viewId,
          routeParameters: pageOptions.routeParameters,
          queryContext: pageOptions.queryContext,
          onError: (error) => {
            cleanup();
            debugStartup({
              op: "subscribe-sources",
              subscriptionId: options.subscriptionId,
              status: receivedInitialSnapshot ? "update-error" : "initial-error",
              sourceCount: options.sourceNames.length,
              errorName: diagnosticErrorName(error),
            });
            if (!receivedInitialSnapshot) {
              reject(error);
            } else {
              failedSubscriptions.add(options.subscriptionId);
              updateNotice ??= publishNotification({
                id: "dashboard:subscription-error",
                message: "Some dashboard data could not be updated. Existing data remains visible.",
                tone: "warning",
                duration: 0,
                action: { label: "Reload dashboard", run: () => browserWindow.location.reload() },
              }, document);
            }
          },
        },
      );
    });
  };
  /** @type {PageSourceLoader} */
  const loadPageSources = async (pageId, pageOptions) => {
    await preparePage?.(pageId);
    const sourceNames = pageSourceNames(pageId, pageOptions.queryContext?.viewMode);
    const paginatedSources = pagePaginatedSourceBindings(pageId);
    const pagination = continuationRequests(Object.keys(paginatedSources));
    return subscribeSources({
      subscriptionId: `page:${pageId}`,
      sourceNames,
      pageOptions: { ...pageOptions, pageId },
      pagination,
      transform: (sources) => bindContinuations(pageId, sources, paginatedSources, { sourceNames }, pageOptions)
    });
  };
  loadPageSources.subscribeViewSources = (pageId, viewId, sourceNames, pageOptions) => {
    const bindings = Object.fromEntries(Object.entries(pagePaginatedSourceBindings(pageId))
      .filter(([, binding]) => binding.viewId === viewId));
    return subscribeSources({
      subscriptionId: `page:${pageId}:view:${viewId}:${++nextViewSubscriptionId}`,
      sourceNames: [...new Set(sourceNames)],
      pageOptions: { ...pageOptions, pageId, viewId },
      pagination: continuationRequests(Object.keys(bindings)),
      transform: (sources) => bindContinuations(pageId, sources, bindings, {
        sourceNames: [...new Set(sourceNames)], viewId
      }, pageOptions)
    });
  };
  loadPageSources.subscribeBackgroundSources = async (sourceNames, pageOptions) => {
    await backgroundWorkStarted;
    if (pageOptions.signal.aborted) {
      throw new DOMException("Dashboard source load was cancelled.", "AbortError");
    }
    const subscriptionSourceNames = [...new Set(sourceNames)].toSorted();
    return subscribeSources({
      subscriptionId: `sources:${subscriptionSourceNames.join(",")}:${++nextViewSubscriptionId}`,
      sourceNames: subscriptionSourceNames,
      pageOptions
    });
  };
  loadPageSources.prepare = async (pageId) => {
    await preparePage?.(pageId);
  };
  configureSourceLoader(createBatchedSourceLoader(dashboardContext));
  const startAutomaticUpdates = () => {
    if (usesRemoteDataBackend(document)) return;
    stopAutomaticDataUpdates = startAutomaticDashboardDataUpdates([
      new URL("./payload-hashes.json", sourceUrl).href,
      sourceUrl,
      new URL("./inventory-sources.json", sourceUrl).href,
    ]);
  };

  const bootstrapStartedAt = performance.now();
  /** @template T @param {string} step @param {() => Promise<T>} run @returns {Promise<T>} */
  const bootstrapStep = async (step, run) => {
    const stepStartedAt = performance.now();
    debugStartup({ op: "bootstrap", step, status: "started" });
    recordPreparationEvent("bootstrap", { step, status: "started" });
    try {
      const result = await run();
      recordPreparationEvent("bootstrap", { step, status: "completed", durationMs: Math.round(performance.now() - stepStartedAt) });
      debugStartup({ op: "bootstrap", step, status: "completed", durationMs: Math.round(performance.now() - stepStartedAt) });
      return result;
    } catch (error) {
      recordPreparationEvent("bootstrap", { step, status: "failed", errorName: diagnosticErrorName(error) });
      debugStartup({
        op: "bootstrap",
        step,
        status: "failed",
        durationMs: Math.round(performance.now() - stepStartedAt),
        errorName: error instanceof Error ? error.name : "Unknown",
      });
      throw error;
    }
  };
  const remoteDataBackend = usesRemoteDataBackend(document);
  const storageInspection = remoteDataBackend ? null : inspectBrowserStorage(browserWindow.navigator?.storage);
  void storageInspection?.then((diagnostics) => {
    if (cleanup.signal.aborted) return;
    recordPreparationStorage(diagnostics);
    if (DEMO_MODE || diagnostics.persisted === true) {
      resolveNotification("dashboard:browser-storage", document);
    } else {
      const notice = publishNotification({
        id: "dashboard:browser-storage",
        message: TEMPORARY_STORAGE_WARNING,
        tone: "warning",
        duration: 0
      }, document);
      cleanup.signal.addEventListener("abort", () => notice.dismiss(), { once: true });
    }
  });
  let upgradeStarted = false;
  let overlayMounted = false;
  const showFirstLoadOverlay = () => {
    if (overlayMounted || cleanup.signal.aborted) return;
    overlayMounted = true;
    mountFirstLoadOverlay({ document, signal: cleanup.signal, retry: () => refreshSources() });
  };
  const stopUpgrade = remoteDataBackend ? () => {} : subscribeDatabaseUpgrade((versions) => {
    if (upgradeStarted || cleanup.signal.aborted) return;
    upgradeStarted = true;
    browserFirstLoad.set({ status: "loading", dismissed: false, reason: "upgrade", ...versions });
    showFirstLoadOverlay();
  });
  /** @type {{ createdAt: string } | null} */
  let snapshot;
  /** @type {DashboardSources} */
  let shellSources = {};
  const shellSourceNames = [...new Set((dashboardContext.callouts ?? []).flatMap((callout) => (
    callout['visible-when'] ? [callout['visible-when'].source] : []
  )))];
  const readShellSources = async () => {
    if (shellSourceNames.length) {
      shellSources = await loadCanonicalDashboardPage(shellSourceNames, dashboardContext);
    }
  };
  try {
    await bootstrapStep("open-canonical-database", () => loadCanonicalDashboardPage([], dashboardContext));
    snapshot = await bootstrapStep("read-snapshot-metadata", () => loadDashboardSnapshotMetadata());
    if (shellSourceNames.length) await bootstrapStep("read-shell-sources", readShellSources);
  } catch (error) {
    if (upgradeStarted) {
      cleanup.abort();
      browserFirstLoad.set({ status: "inactive", dismissed: false });
    }
    throw error;
  } finally {
    stopUpgrade();
  }
  debugStartup({
    op: "bootstrap",
    status: "ready",
    snapshot: snapshot === null ? "absent" : "present",
    durationMs: Math.round(performance.now() - bootstrapStartedAt),
  });
  let hasCompleteSnapshot = remoteDataBackend || snapshot !== null;
  const firstBrowserLoad = !hasCompleteSnapshot;
  let stopFirstLoadProgress = () => {};
  const finishFirstLoad = () => {
    stopFirstLoadProgress();
    stopFirstLoadProgress = () => {};
    browserFirstLoad.set({ status: "inactive", dismissed: false });
  };
  if (firstBrowserLoad) {
    browserFirstLoad.set({
      status: "loading",
      dismissed: upgradeStarted && browserFirstLoad.get().dismissed,
      ...(upgradeStarted
        ? { reason: "upgrade", oldVersion: browserFirstLoad.get().oldVersion, newVersion: browserFirstLoad.get().newVersion }
        : { reason: "missing-snapshot" })
    });
    stopFirstLoadProgress = subscribeWorkerLoadingProgress((progress) => {
      if (browserFirstLoad.get().status !== "loading" || progress.phase !== "update") return;
      recordPreparationEvent("import-progress", {
        stage: progress.stage ?? null, completed: progress.completed ?? null, total: progress.total ?? null
      });
      browserFirstLoad.set((current) => ({
        ...current,
        completed: progress.completed,
        total: progress.total,
        stage: progress.stage,
      }));
    });
    cleanup.signal.addEventListener("abort", finishFirstLoad, { once: true });
  } else if (upgradeStarted) {
    browserFirstLoad.set({ status: "inactive", dismissed: false });
  }
  dashboardDataStatus.set({ phase: hasCompleteSnapshot ? "cached" : "loading", snapshot });
  render(shellSources, hasCompleteSnapshot ? "cached" : "loading", loadPageSources, undefined, snapshot);
  let refreshFailed = false;
  let refreshPending = false;
  /** @param {unknown} error */
  const showStaleSources = (error) => {
    if (refreshFailed || cleanup.signal.aborted) return;
    refreshFailed = true;
    refreshPending = false;
    dashboardDataStatus.set({ phase: "stale", snapshot });
    const message = error instanceof Error ? error.message : String(error);
    recordPreparationEvent("refresh", { status: "failed", errorName: diagnosticErrorName(error) });
    console.error(`Unable to refresh live dashboard data: ${message}`);
    debugStartup({
      op: "refresh-sources",
      status: "stale",
      errorName: error instanceof Error ? error.name : "Unknown",
    });
    emitDashboardDebugEvent(document, DASHBOARD_DATA_EVENT, {
      kind: "refresh",
      status: "failed",
      message,
    });
    if (firstBrowserLoad && !hasCompleteSnapshot) {
      browserFirstLoad.set((current) => ({ ...current, status: "failed" }));
    }
    render(shellSources, hasCompleteSnapshot ? "stale" : "loading", loadPageSources, refreshSources, snapshot);
  };
  const refreshSources = (showRefreshing = true) => {
    if (refreshPending || cleanup.signal.aborted) return;
    refreshFailed = false;
    refreshPending = true;
    recordPreparationEvent("refresh", { status: "started" });
    dashboardDataStatus.set({ phase: hasCompleteSnapshot ? "cached" : "loading", snapshot });
    if (firstBrowserLoad && !hasCompleteSnapshot) {
      browserFirstLoad.set((current) => ({ ...current, status: "loading", completed: undefined, total: undefined, stage: undefined, ingestion: undefined }));
    }
    emitDashboardDebugEvent(document, DASHBOARD_DATA_EVENT, {
      kind: "refresh",
      status: "started",
    });
    if (showRefreshing) {
      render(shellSources, hasCompleteSnapshot ? "cached" : "loading", loadPageSources, undefined, snapshot);
    }
    void refreshCanonicalDashboardSources(
      sourceUrl,
      [],
      dashboardContext,
    ).then(
      async ({ changed }) => {
        if (cleanup.signal.aborted) return;
        snapshot = await loadDashboardSnapshotMetadata() ?? snapshot;
        if (cleanup.signal.aborted) return;
        hasCompleteSnapshot = remoteDataBackend || snapshot !== null;
        recordPreparationEvent("snapshot", { present: hasCompleteSnapshot });
        if (!hasCompleteSnapshot) throw new Error("Dashboard import finished without a complete snapshot.");
        await readShellSources();
        if (cleanup.signal.aborted) return;
        refreshPending = false;
        dashboardDataStatus.set({ phase: "ready", snapshot });
        recordPreparationEvent("refresh", { status: "completed", changed });
        finishPreparationDiagnostics();
        if (firstBrowserLoad) finishFirstLoad();
        emitDashboardDebugEvent(document, DASHBOARD_DATA_EVENT, {
          kind: "refresh",
          status: "completed",
          changed,
        });
        refreshBoundSources();
        if (usesRemoteDataBackend(document)) {
          const dashboard = document.querySelector("#root > .dashboard-root");
          dashboard?.classList.remove("dashboard-refreshing");
          dashboard?.removeAttribute("aria-busy");
        } else {
          render(shellSources, "ready", loadPageSources, undefined, snapshot);
        }
      },
    ).catch(showStaleSources);
  };
  browserWindow.addEventListener(DASHBOARD_REFRESH_REQUEST_EVENT, () => refreshSources(), {
    signal: cleanup.signal,
  });

  await settleUi();
  if (firstBrowserLoad && !cleanup.signal.aborted) {
    showFirstLoadOverlay();
  }
  // The active page is subscribed and painted before lower-priority work begins.
  startBackgroundWork();
  if (!cleanup.signal.aborted) {
    startAutomaticUpdates();
    refreshSources(false);
  }
  return () => {
    cleanup.abort();
    stopAutomaticDataUpdates();
  };
}
