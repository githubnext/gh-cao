      import { dashboardPagePaginatedSourceBindings, dashboardPageSourceNames, disposeDashboard, renderDashboard, updateWithViewTransition } from "./presenter.js";
      import { setLoadingProgressState } from "./loading-progress.js";
      import { isDataProcessingCancellation, offerCancelCommand, offerStartupRecovery } from "./cancel-command.js";
      import { resetBrowserDashboardData } from "./components/reset-dashboard-control.js";
      import { loadDashboardQuerySources, processDashboardQueries, subscribeWorkerLoadingProgress } from "./data-processor.js";
      import { startDashboardData } from "./data/startup.js";
      import { renderDashboardCurrentStatus, notifyRefreshError } from "./components/refresh-error.js";
      import { resolveNotification } from "./notification-service.js";
      import { startDashboardAppUpdates } from "./dashboard-data-updates.js";
      import { attachCliActions, setDeclaredCliActions } from "./components/cli-actions.js";
      import { cliActionTemplateContext } from "./cli-action-template.js";
      import { browserTableCapacityDecision, logTableCapacityDecision } from "./data/table-capacity.js";
      import { startConsoleLogCapture } from "./console-log-capture.js";
      import { DashboardServerError, usesRemoteDataBackend } from "./remote-data-backend.js";
      import {
        dashboardPageChunkPath,
        dashboardPageIsLoaded,
        mergeDashboardPage,
        normalizeDashboardPageChunk,
        splitDashboardDocument,
      } from "./dashboard-chunks.js";
      import { createDebug } from "./debug.js";
      import { loadDashboardPageChunk, loadDashboardSchema } from "./dashboard-definition.js";
      import { pageCapabilityAvailable } from "./view-availability.js";
      import { localCliQueueAvailable } from "./backend-mode.js";
      import { DEMO_MODE } from "./deployment-profile.js";

      /** @typedef {{ name?: string } & Record<string, unknown>} DashboardQueryDefinition */
      /** @typedef {{ 'language-version': string, dashboard: import('./presenter.js').PresentableDashboard }} DashboardSchema */

      export async function startDashboardApp() {
      const debugDashboardApp = createDebug("dashboard-app");
      const modelContext = /** @type {{ modelContext?: { registerTool?: unknown } }} */ (document).modelContext;
      const webMCP = typeof modelContext?.registerTool === "function"
        ? await import("./webmcp/runtime.js") : null;

      startConsoleLogCapture();

      /** @type {Window & { collectFullDiagnostics?: typeof import('./diagnostics.js').collectFullDiagnostics }} */ (window).collectFullDiagnostics =
        () => import("./diagnostics.js").then(({ collectFullDiagnostics }) => collectFullDiagnostics());

      const stopWorkerLoadingProgress = subscribeWorkerLoadingProgress((state) => {
        setLoadingProgressState(document, state);
      });
      const cancelCommand = offerCancelCommand(document);
      /** @type {ReturnType<import('./webmcp/runtime.js').startDashboardWebMCP>} */
      let dashboardWebMCP = null;
      const stopDashboardAppUpdates = usesRemoteDataBackend(document)
        ? () => {}
        : startDashboardAppUpdates();
      window.addEventListener("pagehide", (event) => {
        if (!event.persisted) {
          stopDashboardAppUpdates();
          stopWorkerLoadingProgress();
          dashboardWebMCP?.stop();
        }
      });
      const dashboardSchema = await loadDashboardSchema()
        .catch((error) => {
          cancelCommand.complete();
          throw error;
        });
      /**
       * @param {DashboardSchema} schema
       */
      const normalizeDashboardSchema = (schema) => splitDashboardDocument({
        languageVersion: schema["language-version"],
        dashboard: schema.dashboard,
      });
      /** @type {Map<string, ReturnType<typeof normalizeDashboardPageChunk>>} */
      let dashboardPageChunks = new Map();
      /** @type {Map<string, Promise<ReturnType<typeof normalizeDashboardPageChunk>>>} */
      let dashboardPageChunkLoads = new Map();
      let dashboardSchemaRevision = 0;
      const loadedDashboardPages = new Set();
      /** @type {import('./presenter.js').PresentationDocument} */
      let dashboardDocument;
      const tableCapacityDecision = browserTableCapacityDecision(window);
      const tableRowLimit = tableCapacityDecision.rowLimit;
      logTableCapacityDecision(tableCapacityDecision);
      /** @type {DashboardQueryDefinition[]} */
      const dashboardQueries = [];
      /** @type {{ githubUrlBase?: string, dashboardRepository: string | null, pages: import('./presenter.js').PresentationDocument['dashboard']['pages'], queries: DashboardQueryDefinition[], views: unknown[], callouts?: import('./components/site-callout.js').SiteCallout[] }} */
      const dashboardContext = {
        githubUrlBase: undefined,
        dashboardRepository: null,
        pages: /** @type {import('./presenter.js').PresentationDocument['dashboard']['pages']} */ ([]),
        queries: dashboardQueries,
        views: [],
      };
      const syncDashboardContext = () => {
        dashboardDocument.dashboard.queries = dashboardQueries;
        dashboardContext.githubUrlBase = dashboardDocument.dashboard["github-url-base"];
        dashboardContext.dashboardRepository = dashboardDocument.dashboard.repository ?? null;
        dashboardContext.pages = dashboardDocument.dashboard.pages;
        dashboardContext.queries = dashboardQueries;
        dashboardContext.views = dashboardDocument.dashboard.views ?? [];
        dashboardContext.callouts = dashboardDocument.dashboard.callouts;
      };
      /**
       * @param {DashboardQueryDefinition[]} queries
       */
      const mergeDashboardQueries = (queries) => {
        for (const query of queries) {
          if (typeof query?.name !== "string") continue;
          const existingIndex = dashboardQueries.findIndex((candidate) => candidate?.name === query.name);
          if (existingIndex >= 0) dashboardQueries.splice(existingIndex, 1, query);
          else dashboardQueries.push(query);
        }
        syncDashboardContext();
      };
      /**
       * @param {string} pageId
       * @param {ReturnType<typeof normalizeDashboardPageChunk>} chunk
       */
      const mergeDashboardPageChunk = (pageId, chunk) => {
        const pageIndex = dashboardDocument.dashboard.pages.findIndex((candidate) => candidate.id === pageId);
        if (pageIndex < 0) throw new Error(`Dashboard page "${pageId}" is not declared.`);
        const stub = dashboardDocument.dashboard.pages[pageIndex];
        const mergedPage = /** @type {import('./presenter.js').PresentableBuiltInPage | import('./presenter.js').PresentableCustomPage} */ (
          mergeDashboardPage(stub, chunk.page)
        );
        dashboardDocument.dashboard.pages.splice(pageIndex, 1, mergedPage);
        mergeDashboardQueries(chunk.queries);
        loadedDashboardPages.add(pageId);
        return mergedPage;
      };
      /**
       * @param {DashboardSchema} schema
       */
      const resetDashboardState = (schema) => {
        dashboardSchemaRevision += 1;
        const normalized = normalizeDashboardSchema(schema);
        dashboardDocument = {
          languageVersion: normalized.core["language-version"],
          dashboard: /** @type {import('./presenter.js').PresentableDashboard} */ (normalized.core.dashboard),
        };
        dashboardQueries.splice(0, dashboardQueries.length, ...(normalized.core.dashboard.queries ?? []));
        dashboardPageChunks = new Map(
          [...normalized.pageChunks.entries()].map(([pageId, chunk]) => [pageId, normalizeDashboardPageChunk(chunk)])
        );
        dashboardPageChunkLoads.clear();
        loadedDashboardPages.clear();
        syncDashboardContext();
      };
      /**
       * @param {string} pageId
       * @returns {Promise<void>}
       */
      const ensureDashboardPageLoaded = async (pageId) => {
        const page = dashboardDocument.dashboard.pages.find((candidate) => candidate.id === pageId);
        if (!page || !pageCapabilityAvailable(page)) return;
        if (dashboardPageIsLoaded(page)) {
          if (!loadedDashboardPages.has(pageId)) {
            mergeDashboardQueries([]);
            loadedDashboardPages.add(pageId);
          }
          return;
        }
        const preloadedChunk = dashboardPageChunks.get(pageId);
        if (preloadedChunk) {
          dashboardPageChunks.delete(pageId);
          mergeDashboardPageChunk(pageId, preloadedChunk);
          return;
        }
        const chunkPath = dashboardPageChunkPath(page);
        if (!chunkPath) return;
        let pendingChunk = dashboardPageChunkLoads.get(pageId);
        if (!pendingChunk) {
          const revision = dashboardSchemaRevision;
          debugDashboardApp({ event: "page-chunk-fetch-start", pageId });
          pendingChunk = loadDashboardPageChunk(chunkPath)
            .then((chunk) => normalizeDashboardPageChunk(chunk))
            .then((chunk) => {
              if (revision !== dashboardSchemaRevision) {
                throw new DOMException("Dashboard definition changed while loading the page.", "AbortError");
              }
              return chunk;
            })
            .then((chunk) => {
              debugDashboardApp({ event: "page-chunk-fetch-outcome", pageId, status: "success" });
              return chunk;
            })
            .catch((error) => {
              debugDashboardApp({
                event: "page-chunk-fetch-outcome",
                pageId,
                status: "error",
                errorName: error instanceof Error ? error.name : "unknown"
              });
              throw error;
            })
            .finally(() => {
              dashboardPageChunkLoads.delete(pageId);
            });
          dashboardPageChunkLoads.set(pageId, pendingChunk);
        }
        mergeDashboardPageChunk(pageId, await pendingChunk);
      };
      const ensureAllDashboardPagesLoaded = async () => {
        for (const page of dashboardDocument.dashboard.pages) {
          if (typeof page?.id === "string" && page.id) await ensureDashboardPageLoaded(page.id);
        }
      };
      const initialDashboardPageId = () => {
        const hash = window.location.hash;
        if (!hash.startsWith("#page-")) return "overview";
        try {
          const route = hash.slice("#page-".length);
          const queryIndex = route.indexOf("?");
          const pageId = decodeURIComponent(queryIndex === -1 ? route : route.slice(0, queryIndex));
          return dashboardDocument.dashboard.pages.some((page) => page.id === pageId && pageCapabilityAvailable(page)) ? pageId : "overview";
        } catch {
          return "overview";
        }
      };
      resetDashboardState(dashboardSchema);
      const root = document.querySelector("#root");
      if (!(root instanceof HTMLElement)) throw new Error("Dashboard root element is missing.");
      /** @type {Record<string, import('./presenter.js').LogicalSourceInput>} */
      let renderedSources = {};
      let renderedSourcesPrepared = false;
      /** @type {((pageId: string, options: { signal: AbortSignal, onUpdate: (sources: Record<string, import('./presenter.js').LogicalSourceInput>) => void }) => Promise<Record<string, import('./presenter.js').LogicalSourceInput>>) | undefined} */
      let renderedPageSourceLoader;
      const previewMode = new URLSearchParams(window.location.search).get("local-preview");
      /** @type {WebSocket | undefined} */
      let dashboardSocket;
      if (previewMode && previewMode !== "operator") {
        const dashboardSocketUrl = new URL("./__dashboard_socket", window.location.href);
        dashboardSocketUrl.protocol = dashboardSocketUrl.protocol.replace(/^http/, "ws");
        dashboardSocket = new WebSocket(dashboardSocketUrl);
        dashboardSocket.addEventListener("message", (event) => {
          const message = JSON.parse(event.data);
          if (message?.type === "dashboard-update" && message.dashboard) {
            window.dispatchEvent(new CustomEvent("dashboard-preview-update", {
              detail: { dashboard: message.dashboard, traceId: message.traceId },
            }));
          } else if (!message?.type) {
            window.dispatchEvent(new CustomEvent("dashboard-preview-update", {
              detail: { dashboard: message },
            }));
          }
        });
        console.log("Dashboard preview socket initialized.");
      }
      /**
       * Keeps the generated WebMCP tools aligned with the rendered dashboard
       * definition. WebMCP is progressive enhancement: browsers without
       * `document.modelContext` register nothing and render as before.
       */
      const syncDashboardWebMCP = () => {
        if (!webMCP) return;
        if (dashboardWebMCP) {
          dashboardWebMCP.refresh();
          return;
        }
        if (!renderedPageSourceLoader) return;
        dashboardWebMCP = webMCP.startDashboardWebMCP(document, {
          dashboardDocument: () => dashboardDocument,
          loadPageSources: (pageId, loadOptions) => renderedPageSourceLoader
            ? renderedPageSourceLoader(pageId, loadOptions)
            : Promise.reject(new Error("The dashboard data pipeline is not ready.")),
          navigate: (route) => {
            window.location.hash = route;
          },
        });
      };
      /**
      * @param {Record<string, import('./presenter.js').LogicalSourceInput>} sources
      * @param {'ready' | 'loading' | 'cached' | 'stale'} [state]
      * @param {boolean} [prepared]
      * @param {import('./presenter.js').PageSourceLoader} [loadPageSources]
      * @param {() => void} [retryRefresh]
      * @param {{ createdAt: string } | null} [snapshot]
      */
      const renderSources = (sources, state = "ready", prepared = false, loadPageSources, retryRefresh, snapshot = null) => {
        const canExecuteCliActions = previewMode === "canvas" || previewMode === "admin"
          || (previewMode === "operator" && localCliQueueAvailable());
        renderedSources = sources;
        renderedSourcesPrepared = prepared;
        renderedPageSourceLoader = loadPageSources;
        const cliContext = cliActionTemplateContext({
          repository: dashboardDocument.dashboard.repository,
          githubUrlBase: dashboardDocument.dashboard["github-url-base"],
          caoUrl: document.querySelector('meta[name="cao-url"]')?.getAttribute("content")
            ?? (!previewMode && window.location.protocol === "https:" ? new URL(".", window.location.href).href : undefined),
        });
        setDeclaredCliActions(dashboardDocument.dashboard["cli-actions"] ?? [], {
          canExecute: canExecuteCliActions,
          context: cliContext,
          templateValues: dashboardDocument.dashboard.repository
            ? { repository: dashboardDocument.dashboard.repository }
            : {}
        });
        const dashboard = renderDashboard({
          document: dashboardDocument,
          sources,
          commitSha: document.querySelector('meta[name="dashboard-version"]')?.getAttribute("content"),
          caoVersion: document.querySelector('meta[name="cao-version"]')?.getAttribute("content"),
          ghAwVersion: document.querySelector('meta[name="gh-aw-version"]')?.getAttribute("content"),
          prepared,
          loading: state === "loading" || (state === "stale" && snapshot === null),
          loadPageSources,
          tableRowLimit,
        });
        if (state === "cached") {
          dashboard.classList.add("dashboard-refreshing");
          dashboard.setAttribute("aria-busy", "true");
        } else if (state === "stale") {
          dashboard.classList.add("dashboard-stale");
        }
        if (snapshot || usesRemoteDataBackend(document) || dashboardDocument.dashboard["data-status"]) {
          dashboard.querySelector(".dashboard-horizon")?.after(renderDashboardCurrentStatus(snapshot, {
            refreshing: state === "cached",
            failed: state === "stale" || Boolean(retryRefresh && state === "loading"),
            configuration: dashboardDocument.dashboard["data-status"],
            sources,
            loadPageSources
          }));
        }
        if (retryRefresh && (state === "stale" || (state === "loading" && !snapshot))) {
          notifyRefreshError(retryRefresh, { snapshot });
        } else {
          resolveNotification("dashboard:refresh-error");
        }
        attachCliActions(dashboard, dashboardDocument.dashboard["cli-actions"] ?? [], {
          repository: dashboardDocument.dashboard.repository,
          context: cliContext,
          canExecute: canExecuteCliActions
        });
        const previousDashboard = root.firstElementChild;
        if (previousDashboard instanceof HTMLElement) disposeDashboard(previousDashboard);
        root.replaceChildren(dashboard);
        syncDashboardWebMCP();
        return dashboard;
      };
      window.addEventListener("dashboard-preview-update", (event) => {
        const previewEvent = /** @type {CustomEvent<{ dashboard: { 'language-version': string, dashboard: import('./presenter.js').PresentableDashboard }, traceId?: string }>} */ (event);
        const { dashboard: schema, traceId } = previewEvent.detail;
        const previousDashboardDocument = dashboardDocument;
        const previousDashboardQueries = [...dashboardQueries];
        const previousDashboardPageChunks = dashboardPageChunks;
        const previousLoadedDashboardPages = new Set(loadedDashboardPages);
        try {
          resetDashboardState(schema);
          updateWithViewTransition(document, () => renderSources(renderedSources, "ready", renderedSourcesPrepared, renderedPageSourceLoader));
          if (traceId && dashboardSocket?.readyState === WebSocket.OPEN) {
            dashboardSocket.send(JSON.stringify({
              type: "browser.trace",
              traceId,
              event: "preview.rendered",
              details: {
                pageCount: schema.dashboard.pages.length,
                activeHash: window.location.hash,
              },
            }));
          }
        } catch (error) {
          const message = error instanceof Error ? error.message : String(error);
          const errorLog = error instanceof Error && error.stack ? error.stack : message;
          let recovered = false;
          let recoveryErrorLog = "";
          dashboardDocument = previousDashboardDocument;
          dashboardPageChunks = previousDashboardPageChunks;
          dashboardQueries.splice(0, dashboardQueries.length, ...previousDashboardQueries);
          loadedDashboardPages.clear();
          for (const pageId of previousLoadedDashboardPages) loadedDashboardPages.add(pageId);
          syncDashboardContext();
          try {
            renderSources(renderedSources, "ready", renderedSourcesPrepared, renderedPageSourceLoader);
            recovered = true;
          } catch (recoveryError) {
            recoveryErrorLog = recoveryError instanceof Error && recoveryError.stack
              ? recoveryError.stack
              : String(recoveryError);
          }
          const combinedErrorLog = [
            errorLog,
            recoveryErrorLog ? `Preview recovery failed:\n${recoveryErrorLog}` : "",
          ].filter(Boolean).join("\n\n").slice(0, 6000);
          console.error("Dashboard preview hot reload failed.", {
            message,
            recovered,
          });
          if (traceId && dashboardSocket?.readyState === WebSocket.OPEN) {
            dashboardSocket.send(JSON.stringify({
              type: "browser.trace",
              traceId,
              event: "preview.render.failed",
              details: {
                message,
                errorLog: combinedErrorLog,
                recovered,
              },
            }));
          }
        }
      });

      /**
       * @param {Record<string, unknown>} sources
       * @param {boolean} [ingest]
       * @returns {Promise<Record<string, import('./presenter.js').LogicalSourceInput>>}
       */
      async function withDatabaseQuerySources(sources, ingest = false) {
        const sourceNames = [...new Set(dashboardDocument.dashboard.pages.flatMap((page) =>
          dashboardPageSourceNames(dashboardDocument, page.id)
        ))];
        return loadDashboardQuerySources(sources, {
          ingest,
          sourceNames,
          queries: dashboardQueries,
        });
      }

      if (new URLSearchParams(window.location.search).has("fixtures")) {
        const { fixtureSources } = await import("./dashboard-fixtures.js");
        await ensureAllDashboardPagesLoaded();
        const fixtureProjection = await withDatabaseQuerySources(fixtureSources, true);
        renderSources({
          ...fixtureProjection,
          ...await processDashboardQueries(dashboardQueries, fixtureProjection),
        });
        cancelCommand.complete();
      } else {
        await ensureDashboardPageLoaded(initialDashboardPageId());
        if (!document.querySelector('meta[name="dashboard-data-backend"][content="server-http"]')) {
          renderSources({}, "loading");
        }
        const sourceUrl = new URL(DEMO_MODE ? "./demo-data/payload-hashes.json" : "./payload-hashes.json", window.location.href).href;
        try {
          await startDashboardData({
            browserWindow: window,
            document,
            sourceUrl,
            dashboardContext,
            preparePage: ensureDashboardPageLoaded,
            pageSourceNames: (pageId, viewMode) => dashboardPageSourceNames(dashboardDocument, pageId, viewMode),
            pagePaginatedSourceBindings: (pageId) => dashboardPagePaginatedSourceBindings(dashboardDocument, pageId),
            render: (sources, state, loadPageSources, retryRefresh, snapshot) =>
              renderSources(sources, state, true, loadPageSources, retryRefresh, snapshot),
          });
        } catch (error) {
          if (isDataProcessingCancellation(error)) {
            // A user-requested cancel is not a fatal failure: keep the rendered
            // dashboard shell and offer recovery instead of replacing the page.
            debugDashboardApp({ event: "startup-cancelled" });
            offerStartupRecovery(document, {
              reload: () => window.location.reload(),
              reset: () => resetBrowserDashboardData(),
            });
          } else {
            const message = error instanceof Error ? error.message : String(error);
            const failure = new Error(`Unable to load live dashboard data: ${message}`, { cause: error });
            const requestId = error instanceof DashboardServerError ? error.traceId : "";
            root.textContent = requestId
              ? `${failure.message} Request ID: ${requestId}`
              : failure.message;
            throw failure;
          }
        } finally {
          cancelCommand.complete();
        }
      }
      }
