import {
  compileDashboardViewPayloadQueries,
  dashboardFormDefaultValues,
  resolveDashboardQueryParameters
} from "./data/queries/view-payload-compiler.js";
import { resolveDashboardQuerySources } from "./data/queries/declarative.js";
import { csrfHeaders, csrfToken, ensureCsrfToken, usesGitHubAuthentication, githubResourcePrincipal, invalidateGitHubResourceAuthentication } from "./auth.js";
import { usesRemoteDataBackend } from "./backend-mode.js";
import { createDebug, diagnosticErrorName } from "./debug.js";
import { updateRateLimitNotification } from "./rate-limit-notification.js";
import { publishNotification } from "./notification-service.js";
export { disableRemoteDashboardPwa, usesRemoteDataBackend } from "./backend-mode.js";

const debugRemoteBackend = createDebug("remote-data-backend");

const ACCESS_TOKEN_STORAGE_KEY = "cao-dashboard-access-token";
/** @type {string | undefined} */
let githubSessionCacheIdentity;
/** @type {string | undefined} */
let githubObservedAuthentication;
globalThis.addEventListener?.('cao-authentication-change', () => { githubSessionCacheIdentity = undefined; });

export class DashboardServerError extends Error {
  /**
   * @param {string} message
   * @param {string} code
   * @param {string} queryId
   * @param {string} [boundary]
   * @param {string} [traceId]
   * @param {string} [spanId]
   */
  constructor(message, code, queryId, boundary = "", traceId = "", spanId = "") {
    super(message);
    this.name = "DashboardServerError";
    this.code = code;
    this.queryId = queryId;
    this.boundary = boundary;
    this.traceId = traceId;
    this.spanId = spanId;
  }
}
/** @type {number | null} */
let observedRevision = null;
/** @type {number | null} */
let observedHealthRevision = null;
/** @type {string | undefined} */
let observedEvaluatedAt;
/** @type {Set<{ onRevision: (revision: number, healthRevision: number | null) => void, onError?: (error: Error) => void }>} */
const remoteRevisionSubscribers = new Set();
/** @type {(() => void) | undefined} */
let stopRemoteRevisionStream;

/**
 * @typedef {{
 *   filters?: Record<string, string[]>,
 *   viewFilters?: import('./view-filter-contract.js').ViewFilters,
 *   search?: { fields: string[], query: string },
 *   orderBy?: Array<{ field: string, direction?: 'asc'|'desc' }>,
 *   timeWindow?: { start?: string, end?: string },
 *   viewMode?: 'chart'|'table'|'card',
 *   formValues?: Record<string, string|number|boolean>
 * }} RemoteQueryContext
 */

/**
 * @typedef {{
 *   signal?: AbortSignal,
 *   pageId?: string,
 *   viewId?: string,
 *   routeParameters?: Record<string, string>,
 *   queryContext?: RemoteQueryContext
 * }} RemoteQueryOptions
 */

/** @param {AbortSignal} [signal] */
export function fetchServerLogs(signal) {
  return apiRequest("/api/admin/logs", {}, signal);
}


/** @param {string} path */
function apiUrl(path) {
  const origin = globalThis.location?.origin ?? "https://localhost";
  const base = globalThis.document?.querySelector?.('meta[name="dashboard-api-base"]')?.getAttribute("content");
  return base
    ? new URL(path.replace(/^\//, ""), new URL(base, origin)).href
    : new URL(path, origin).href;
}

function accessToken() {
  try {
    return globalThis.localStorage?.getItem(ACCESS_TOKEN_STORAGE_KEY) ?? "";
  } catch {
    return "";
  }

}

/** Serializable backend configuration for worker-owned GitHub resource requests. */
export function githubWorkerBackendConfiguration() {
  const previewBase = globalThis.document?.querySelector?.('meta[name="dashboard-github-api-base"]')?.getAttribute("content");
  if (previewBase) return {
    baseUrl: new URL("api/v1/github/", new URL(previewBase, globalThis.location?.origin ?? "https://localhost")).href,
    credentials: /** @type {RequestCredentials} */ ("omit"),
    cacheIdentity: `preview:${githubSessionCacheIdentity ??= crypto.randomUUID()}`
  };
  const configured = usesRemoteDataBackend()
    || Boolean(globalThis.document?.querySelector?.('meta[name="dashboard-api-base"]')?.getAttribute("content"));
  const oauth = usesGitHubAuthentication();
  const authentication = JSON.stringify([oauth, githubResourcePrincipal(), oauth ? csrfToken() : "local-session"]);
  const changed = githubObservedAuthentication !== undefined && githubObservedAuthentication !== authentication;
  githubObservedAuthentication = authentication;
  if (changed) invalidateGitHubResourceAuthentication(true);
  return {
    baseUrl: configured ? apiUrl("/api/v1/github/") : null,
    credentials: /** @type {RequestCredentials} */ ("same-origin"),
    cacheIdentity: `${oauth ? githubResourcePrincipal() || "unverified" : "capability"}:${githubSessionCacheIdentity ??= crypto.randomUUID()}`
  };
}

/**
 * @param {string} path
 * @param {RequestInit} [init]
 * @param {AbortSignal} [signal]
 */
async function apiRequest(path, init = {}, signal) {
  const oauth = usesGitHubAuthentication();
  const token = oauth ? "" : accessToken();
  const method = (init.method ?? "GET").toUpperCase();
  const operation = path === "/api/v1/query" ? "query"
    : path === "/api/v1/refresh" ? "refresh" : "other";
  const startedAt = performance.now();
  debugRemoteBackend({ event: "request-started", operation, method });
  if (oauth && !["GET", "HEAD", "OPTIONS"].includes(method)) await ensureCsrfToken(signal);
  const headers = {
    Accept: "application/json",
    ...(!["GET", "HEAD"].includes(method) ? { "Content-Type": "application/json" } : {}),
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...init.headers,
  };
  let response;
  try {
    response = await fetch(apiUrl(path), {
      ...init,
      signal,
      cache: "no-store",
      credentials: oauth ? "same-origin" : "omit",
      headers: oauth && !["GET", "HEAD", "OPTIONS"].includes(method)
        ? csrfHeaders(headers)
        : headers,
    });
  } catch (error) {
    debugRemoteBackend({
      event: "request-network-failed", operation,
      durationMs: Math.round(performance.now() - startedAt),
      errorName: diagnosticErrorName(error)
    });
    throw error;
  }
  updateRateLimitNotification(path, response.status);
  if (!response.ok) {
    const payload = await response.json().catch(() => null);
    const message = typeof payload?.error === "string" ? payload.error : `Dashboard data server request failed: ${response.status}`;
    const traceId = typeof payload?.traceId === "string"
      ? payload.traceId
      : response.headers.get("X-Trace-Id") ?? "";
    const spanId = typeof payload?.spanId === "string"
      ? payload.spanId
      : response.headers.get("X-Span-Id") ?? "";
    debugRemoteBackend({
      event: "request-failed",
      operation,
      status: response.status,
      durationMs: Math.round(performance.now() - startedAt),
      ...(/^[a-f0-9]{32}$/i.test(traceId) ? { traceId } : {})
    });
    throw new DashboardServerError(
      message,
      typeof payload?.code === "string" ? payload.code : "",
      typeof payload?.queryId === "string" ? payload.queryId : "",
      typeof payload?.boundary === "string" ? payload.boundary : "",
      traceId,
      spanId
    );
  }
  const result = await response.json();
  debugRemoteBackend({
    event: "request-completed", operation, status: response.status,
    durationMs: Math.round(performance.now() - startedAt)
  });
  return result;
}

/**
 * @param {string[]} sourceNames
 * @param {{ pages: unknown[], queries?: unknown[], views?: unknown[] }} context
 * @param {Record<string, { limit: number, continuationToken?: string }>} [pagination]
 * @param {RemoteQueryOptions} [options]
 */
function remoteQueryPayload(sourceNames, context, pagination, options = {}) {
  const page = options.pageId
    ? context.pages.find((candidate) => (
        candidate && typeof candidate === "object" && "id" in candidate && candidate.id === options.pageId
      ))
    : null;
  const viewPayload = page && options.pageId
    ? compileDashboardViewPayloadQueries(page, options.pageId, {
        backend: "hosted",
        routeParameters: options.routeParameters,
        queryContext: options.queryContext,
        evaluatedAt: options.queryContext?.timeWindow?.end ?? observedEvaluatedAt,
        queries: context.queries ?? [],
        views: context.views ?? [],
        viewId: options.viewId,
        sourceNames,
      })
    : { aliases: [], queries: [], replacedSources: [] };
  const pageForm = page && typeof page === "object" && !Array.isArray(page)
    ? /** @type {Record<string, unknown>} */ (page).form
    : undefined;
  const formValues = {
    ...dashboardFormDefaultValues(pageForm),
    ...(options.queryContext?.formValues ?? {})
  };
  const required = new Set(resolveDashboardQuerySources(context.queries ?? [], sourceNames));
  const scopedQueries = (context.queries ?? []).filter((definition) => (
    definition && typeof definition === "object"
      && typeof (/** @type {Record<string, unknown>} */ (definition)).name === "string"
      && required.has(/** @type {{ name: string }} */ (definition).name)
  ));
  return {
    sourceNames,
    queries: resolveDashboardQueryParameters(scopedQueries, formValues),
    compiledQueries: viewPayload.queries,
    aliases: viewPayload.aliases,
    replacedSources: viewPayload.replacedSources,
    pagination: pagination ?? {},
    pageId: options.pageId,
    viewId: options.viewId,
    routeParameters: options.routeParameters,
    queryContext: options.queryContext,
  };
}

/**
 * @param {string[]} sourceNames
 * @param {{ pages: unknown[], queries?: unknown[], views?: unknown[] }} context
 * @param {Record<string, { limit: number, continuationToken?: string }>} [pagination]
 * @param {RemoteQueryOptions} [options]
 */
export async function queryRemoteDashboard(sourceNames, context, pagination, options = {}) {
  debugRemoteBackend({
    event: "query-started",
    sourceCount: sourceNames.length,
    hasPage: Boolean(options.pageId),
    hasView: Boolean(options.viewId),
    needsEvaluationTime: !observedEvaluatedAt
  });
  if (!observedEvaluatedAt) {
    const status = await apiRequest("/api/v1/refresh", { method: "POST" }, options.signal);
    if (Number.isSafeInteger(status?.revision)) observedRevision = status.revision;
    if (typeof status?.evaluatedAt === "string") observedEvaluatedAt = status.evaluatedAt;
  }
  const payload = await apiRequest("/api/v1/query", {
    method: "POST",
    body: JSON.stringify(remoteQueryPayload(sourceNames, context, pagination, options)),
  }, options.signal);
  if (!payload?.sources || typeof payload.sources !== "object" || Array.isArray(payload.sources)) {
    throw new Error("Dashboard data server returned an invalid query response.");
  }
  if (Number.isSafeInteger(payload.revision)) observedRevision = payload.revision;
  if (Number.isSafeInteger(payload.healthRevision)) observedHealthRevision = payload.healthRevision;
  if (typeof payload.evaluatedAt === "string") observedEvaluatedAt = payload.evaluatedAt;
  debugRemoteBackend({
    event: "query-completed",
    sourceCount: Object.keys(payload.sources).length,
    revision: observedRevision,
    healthRevision: observedHealthRevision
  });
  return {
    revision: Number.isSafeInteger(payload.revision) ? payload.revision : null,
    healthRevision: Number.isSafeInteger(payload.healthRevision) ? payload.healthRevision : null,
    sources: payload.sources,
  };
}

/**
 * @param {string[]} sourceNames
 * @param {{ pages: unknown[], queries?: unknown[], views?: unknown[] }} context
 * @param {Record<string, { limit: number, continuationToken?: string }>} [pagination]
 * @param {RemoteQueryOptions} [options]
 */
export async function refreshRemoteDashboard(sourceNames, context, pagination, options = {}) {
  const status = await apiRequest("/api/v1/refresh", { method: "POST" }, options.signal);
  const revision = Number.isSafeInteger(status?.revision) ? status.revision : null;
  const changed = revision !== null && observedRevision !== null && revision !== observedRevision;
  debugRemoteBackend({ event: "refresh-checked", changed });
  if (typeof status?.evaluatedAt === "string") observedEvaluatedAt = status.evaluatedAt;
  const result = await queryRemoteDashboard(sourceNames, context, pagination, options);
  return { sources: result.sources, changed };
}

/**
 * Asks the owning local `gh cao dashboard` process to start a collection pass.
 * @param {AbortSignal} [signal]
 */
export function requestLocalIngestion(signal) {
  return apiRequest("/api/v1/ingestion/start", { method: "POST" }, signal);
}

/** @param {AbortSignal} [signal] */
export function queryRemoteDiagnostics(signal) {
  return apiRequest("/api/v1/diagnostics", {}, signal);
}

/**
 * @param {string} campaign
 * @param {string | undefined} path
 * @param {AbortSignal} [signal]
 */
export function queryRemoteRepositoryMemory(campaign, path, signal) {
  const campaignPath = encodeURIComponent(campaign);
  const suffix = path === undefined ? "" : `/content?path=${encodeURIComponent(path)}`;
  return apiRequest(`/api/v1/memory/${campaignPath}${suffix}`, {}, signal);
}

/**
 * @param {(revision: number, healthRevision: number | null) => void} onRevision
 * @param {(error: Error) => void} [onError]
 */
export function subscribeRemoteRevision(onRevision, onError) {
  if (typeof fetch !== "function" || typeof TextDecoder === "undefined") return () => {};
  const subscriber = { onRevision, onError };
  remoteRevisionSubscribers.add(subscriber);
  debugRemoteBackend({ event: "stream-subscriber-added", subscriberCount: remoteRevisionSubscribers.size });
  if (!stopRemoteRevisionStream) stopRemoteRevisionStream = startRemoteRevisionStream();
  return () => {
    remoteRevisionSubscribers.delete(subscriber);
    debugRemoteBackend({ event: "stream-subscriber-removed", subscriberCount: remoteRevisionSubscribers.size });
    if (remoteRevisionSubscribers.size > 0) return;
    stopRemoteRevisionStream?.();
    stopRemoteRevisionStream = undefined;
  };
}

function startRemoteRevisionStream() {
  const controller = new AbortController();
  let streamRevision = observedRevision;
  let streamHealthRevision = observedHealthRevision;
  /** @type {ReturnType<typeof setTimeout> | undefined} */
  let retry;
  let stopped = false;
  let attempt = 0;
  /** @type {ReturnType<typeof publishNotification> | undefined} */
  let connectionNotice;

  /**
   * @param {(subscriber: { onRevision: (revision: number, healthRevision: number | null) => void, onError?: (error: Error) => void }) => void} callback
   * @param {"revision" | "error"} callbackType
   */
  const notifySubscribers = (callback, callbackType) => {
    for (const subscriber of [...remoteRevisionSubscribers]) {
      if (!remoteRevisionSubscribers.has(subscriber)) continue;
      try {
        callback(subscriber);
      } catch (error) {
        debugRemoteBackend({
          event: "subscriber-callback-failed",
          callback: callbackType,
          errorName: diagnosticErrorName(error),
        });
      }
    }
  };

  /** @param {string} data */
  const emit = (data) => {
    try {
      const payload = JSON.parse(data);
      if (Number.isSafeInteger(payload?.revision)) {
        const healthRevision = Number.isSafeInteger(payload.healthRevision)
          ? payload.healthRevision
          : observedHealthRevision;
        const changed = payload.revision !== streamRevision
          || (healthRevision !== null && healthRevision !== streamHealthRevision);
        streamRevision = payload.revision;
        streamHealthRevision = healthRevision;
        if (payload.revision !== observedRevision) observedEvaluatedAt = undefined;
        observedRevision = payload.revision;
        if (healthRevision !== null) observedHealthRevision = healthRevision;
        if (changed) {
          debugRemoteBackend({ event: "revision-changed", hasHealthRevision: healthRevision !== null });
          notifySubscribers(
            (subscriber) => subscriber.onRevision(payload.revision, healthRevision),
            "revision",
          );
        }
      }
    } catch (error) {
      const failure = error instanceof Error ? error : new Error(String(error));
      notifySubscribers((subscriber) => subscriber.onError?.(failure), "error");
    }
  };

  const connect = async () => {
    attempt += 1;
    debugRemoteBackend({ event: "stream-connecting", attempt, subscriberCount: remoteRevisionSubscribers.size });
    try {
      const oauth = usesGitHubAuthentication();
      const token = oauth ? "" : accessToken();
      const response = await fetch(apiUrl("/api/v1/events"), {
        cache: "no-store",
        credentials: oauth ? "same-origin" : "omit",
        headers: {
          Accept: "text/event-stream",
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        signal: controller.signal,
      });
      updateRateLimitNotification("/api/v1/events", response.status);
      if (!response.ok || !response.body) {
        debugRemoteBackend({ event: "stream-response-failed", status: response.status, hasBody: Boolean(response.body) });
        throw new Error("Dashboard data event stream is unavailable.");
      }
      debugRemoteBackend({ event: "stream-connected", attempt, status: response.status });
      connectionNotice?.dismiss();
      connectionNotice = undefined;
      attempt = 0;
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      for (;;) {
        const { done, value } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true }).replaceAll("\r\n", "\n");
        let boundary;
        while ((boundary = buffer.indexOf("\n\n")) >= 0) {
          const event = buffer.slice(0, boundary);
          buffer = buffer.slice(boundary + 2);
          const data = event.split("\n")
            .filter((line) => line.startsWith("data:"))
            .map((line) => line.slice(5).trimStart())
            .join("\n");
          if (data) emit(data);
        }
      }
      throw new Error("Dashboard data server event stream disconnected.");
    } catch (error) {
      if (stopped || controller.signal.aborted) return;
      const errorName = diagnosticErrorName(error);
      debugRemoteBackend({ event: "stream-reconnecting", errorName, attempt, retryDelayMs: 1000 });
      // A lost event stream does not invalidate the last successful query.
      connectionNotice ??= publishNotification({
        message: "Live dashboard updates are paused. Reconnecting automatically; existing data remains visible. If this continues, check your connection or sign in again.",
        tone: "warning",
        duration: 0
      });
      retry = setTimeout(() => void connect(), 1000);
    }
  };
  void connect();
  return () => {
    stopped = true;
    if (retry !== undefined) clearTimeout(retry);
    controller.abort();
    connectionNotice?.dismiss();
  };
}
