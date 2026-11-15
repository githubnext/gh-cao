import { readTransaction, recordTransaction } from './storage/indexeddb.js';
import { createDebug } from '../debug.js';

const debug = createDebug('data:github');
const debugGitHubService = createDebug('github-service');

class GitHubStorageError extends Error {
  constructor() { super('storage-unavailable'); }
}

/**
 * @typedef {{ owner: string, repo: string, resource?: 'repository' | 'latest-release' }} GitHubResource
 * @typedef {{ baseUrl: string | null, credentials?: RequestCredentials, cacheIdentity?: string }} GitHubBackendConfiguration
 * @typedef {{ data: Record<string, unknown> | null, status: 'available' | 'stale' | 'unavailable', source: 'cache' | 'network' | 'none', error: string | null, retryAt: number | null }} GitHubResourceSnapshot
 * @typedef {{ id: string, kind: string, createdAt: string, data: Record<string, unknown>, etag: string | null, freshUntil: number, staleUntil: number }} CacheRecord
 * @typedef {{ listeners: Set<(snapshot: GitHubResourceSnapshot) => void>, controller: AbortController, cached: CacheRecord | null, snapshot: GitHubResourceSnapshot | null, timer?: ReturnType<typeof setTimeout>, expiryTimer?: ReturnType<typeof setTimeout>, attempts: number }} ResourceEntry
 */

const MAX_RESOURCES = 256;
const FRESH_MS = 300_000;
const STALE_MS = 86_400_000;
const MAX_BODY_BYTES = 65_536;

/** @param {GitHubResource} resource */
export function githubResourcePath(resource) {
  if (!resource || !/^[a-z\d](?:[a-z\d-]{0,38})$/i.test(resource.owner)
      || !/^[a-z\d_.-]{1,100}$/i.test(resource.repo)
      || resource.repo === '.' || resource.repo === '..'
      || (resource.resource !== undefined && !['repository', 'latest-release'].includes(resource.resource))) {
    throw new TypeError('Invalid GitHub resource.');
  }
  return `repos/${resource.owner.toLowerCase()}/${resource.repo.toLowerCase()}${resource.resource === 'latest-release' ? '/releases/latest' : ''}`;
}

/** @param {number} delay @param {AbortSignal} signal */
function wait(delay, signal) {
  return new Promise((resolve, reject) => {
    const abort = () => {
      clearTimeout(timer);
      reject(new DOMException('Cancelled', 'AbortError'));
    };
    const timer = setTimeout(() => {
      signal.removeEventListener('abort', abort);
      resolve(undefined);
    }, Math.min(delay, 2_147_483_647));
    if (signal.aborted) abort();
    else signal.addEventListener('abort', abort, { once: true });
  });
}

/** @param {unknown} input @param {boolean} release */
function normalizedResource(input, release) {
  if (!input || typeof input !== 'object' || Array.isArray(input)) throw new Error('invalid-response');
  const record = /** @type {Record<string, unknown>} */ (input);
  const fields = release
    ? ['tag_name', 'name', 'html_url', 'published_at', 'draft', 'prerelease']
    : ['full_name', 'description', 'html_url', 'default_branch', 'stargazers_count', 'forks_count', 'open_issues_count', 'archived', 'visibility'];
  if (typeof record[release ? 'tag_name' : 'full_name'] !== 'string') throw new Error('invalid-response');
  for (const field of fields) {
    if (!Object.hasOwn(record, field)) continue;
    const value = record[field];
    if (value === null && ['description', 'name', 'published_at'].includes(field)) continue;
    const type = ['draft', 'prerelease', 'archived'].includes(field) ? 'boolean'
      : ['stargazers_count', 'forks_count', 'open_issues_count'].includes(field) ? 'number' : 'string';
    if (typeof value !== type || (type === 'number' && (!Number.isSafeInteger(value) || Number(value) < 0))) throw new Error('invalid-response');
  }
  return Object.fromEntries(fields.filter((field) => Object.hasOwn(record, field)).map((field) => [field, record[field]]));
}

/**
 * Worker-owned ancillary cache. Receipts share the existing bounded transaction
 * store, not canonical entities. Only opaque authentication scopes enter keys;
 * callers must rotate their scope at every principal/session boundary.
 * @param {{ fetch?: typeof globalThis.fetch, indexedDB?: IDBFactory, now?: () => number, timeoutMs?: number, retryBaseMs?: number }} [dependencies]
 */
export function createGitHubDataService(dependencies = {}) {
  const fetchResource = dependencies.fetch ?? globalThis.fetch;
  const database = dependencies.indexedDB ?? globalThis.indexedDB;
  const now = dependencies.now ?? Date.now;
  /** @type {Map<string, ResourceEntry>} */
  const entries = new Map();
  /** @type {Map<string, { until: number, nextStart: number }>} */
  const limits = new Map();
  /** @type {Map<string, string>} */
  const ephemeralScopes = new Map();
  let authenticationGeneration = 0;
  let activeRequests = 0;

  /** @param {{ id: string, kind: string, createdAt: string, [field: string]: unknown }} record */
  async function persist(record) {
    try {
      await recordTransaction(database, record);
    } catch {
      debug({ event: 'storage-failed', operation: 'write' });
      throw new GitHubStorageError();
    }
  }

  /** @param {ResourceEntry} entry @param {GitHubResourceSnapshot} snapshot */
  function emit(entry, snapshot) {
    if (entry.controller.signal.aborted) return;
    entry.snapshot = snapshot;
    for (const listener of entry.listeners) {
      try { listener(snapshot); } catch { /* Consumers cannot interrupt other subscriptions. */ }
    }
  }

  /** @param {ResourceEntry} entry @param {'cache' | 'network'} source @param {string | null} [error] @param {number | null} [retryAt] */
  function snapshot(entry, source, error = null, retryAt = null) {
    clearTimeout(entry.expiryTimer);
    const usable = entry.cached && entry.cached.staleUntil > now() ? entry.cached : null;
    emit(entry, {
      data: usable ? usable.data : null,
      status: usable ? (usable.freshUntil > now() ? 'available' : 'stale') : 'unavailable',
      source: usable ? source : 'none', error, retryAt
    });
    if (usable && !entry.controller.signal.aborted) {
      entry.expiryTimer = setTimeout(() => snapshot(entry, 'cache', error, retryAt), Math.max(1, usable.staleUntil - now()));
    }
  }

  /**
   * Each subscription requires an abort-scoped lifetime. Identical resources and
   * auth contexts share reads, requests, retries and refresh timers.
   * @param {GitHubResource} resource
   * @param {GitHubBackendConfiguration} configuration
   * @param {(snapshot: GitHubResourceSnapshot) => void} listener
   * @param {{ signal: AbortSignal }} options
   */
  function subscribe(resource, configuration, listener, { signal }) {
    const path = githubResourcePath(resource);
    if (signal.aborted) return () => {};
    const base = configuration.baseUrl ? new URL(configuration.baseUrl) : null;
    if (base && (!['http:', 'https:'].includes(base.protocol) || base.username || base.password
        || base.search || base.hash || !/\/api\/v1\/github\/$/.test(base.pathname)
        || /(^|\.)github(?:usercontent)?\.com$/i.test(base.hostname.replace(/\.$/, '')))) {
      throw new TypeError('GitHub resources require a dashboard backend URL.');
    }
    const credentials = configuration.credentials ?? 'same-origin';
    let scope = configuration.cacheIdentity;
    if (!scope) {
      scope = ephemeralScopes.get(credentials);
      if (!scope) {
        if (ephemeralScopes.size >= MAX_RESOURCES) ephemeralScopes.delete(ephemeralScopes.keys().next().value ?? '');
        scope = crypto.randomUUID();
        ephemeralScopes.set(credentials, scope);
      }
    }
    const identity = JSON.stringify([
      base?.href ?? '', credentials,
      authenticationGeneration ? `${scope}:epoch:${authenticationGeneration}` : scope
    ]);
    const key = `${identity}:${path}`;
    let entry = entries.get(key);
    const initializing = !entry;
    if (!entry) {
      if (entries.size >= MAX_RESOURCES) throw new Error('Too many GitHub resource subscriptions.');
      entry = { listeners: new Set(), controller: new AbortController(), cached: null, snapshot: null, attempts: 0 };
      entries.set(key, entry);
    }
    const current = entry;
    /** @param {GitHubResourceSnapshot} value */
    const notify = (value) => listener(value);
    current.listeners.add(notify);
    let stopped = false;
    const stop = () => {
      if (stopped) return;
      stopped = true;
      signal.removeEventListener('abort', stop);
      current.listeners.delete(notify);
      if (current.listeners.size) return;
      current.controller.abort();
      clearTimeout(current.timer);
      clearTimeout(current.expiryTimer);
      if (entries.get(key) === current) entries.delete(key);
    };
    signal.addEventListener('abort', stop, { once: true });
    if (signal.aborted) stop();
    if (current.snapshot && !stopped) {
      try { listener(current.snapshot); } catch { /* Isolate consumer failures. */ }
    }
    if (initializing && !stopped) void initialize();
    return stop;

    async function initialize() {
      try {
        const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(identity));
        const partition = Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, '0')).join('');
        const id = `github-resource:${partition}:${path}`;
        let receipt;
        try {
          receipt = await readTransaction(database, id);
        } catch {
          debug({ event: 'storage-failed', operation: 'read' });
          throw new GitHubStorageError();
        }
        if (receipt?.kind === 'github-resource-cache') current.cached = /** @type {CacheRecord} */ (receipt);
        if (current.controller.signal.aborted) return;
        snapshot(current, 'cache', base ? null : 'backend-unconfigured');
        if (!base) return;
        if (!limits.has(identity)) {
          if (limits.size >= MAX_RESOURCES) limits.delete(limits.keys().next().value ?? '');
          limits.set(identity, { until: 0, nextStart: 0 });
        }
        const limit = /** @type {{ until: number, nextStart: number }} */ (limits.get(identity));

        /** @param {number} delay */
        function schedule(delay) {
          if (!current.controller.signal.aborted) current.timer = setTimeout(() => { void refresh(); }, Math.min(2_147_483_647, Math.max(1, delay)));
        }

        async function refresh() {
          const signal = current.controller.signal;
          if (signal.aborted) return;
          if (current.cached) snapshot(current, 'cache');
          let acquired = false;
          const requestController = new AbortController();
          const abort = () => requestController.abort();
          signal.addEventListener('abort', abort, { once: true });
          /** @type {ReturnType<typeof setTimeout> | undefined} */
          let timeout;
          try {
            while (activeRequests >= 4 || Math.max(limit.until, limit.nextStart) > now()) {
              await wait(Math.max(10, Math.max(limit.until, limit.nextStart) - now()), signal);
            }
            if (signal.aborted) return;
            activeRequests++;
            acquired = true;
            limit.nextStart = now() + 100;
            timeout = setTimeout(abort, dependencies.timeoutMs ?? 15_000);
            const response = await fetchResource(new URL(path, base ?? undefined), {
              method: 'GET', cache: 'no-store', redirect: 'error', credentials,
              signal: requestController.signal,
              headers: {
                Accept: 'application/json',
                ...(current.cached?.etag ? { 'If-None-Match': current.cached.etag } : {})
              }
            });
            const header = response.headers.get('Retry-After');
            if (header || response.status === 429 || response.status === 503) {
              const delay = header && /^\d+$/.test(header) ? Number(header) * 1000 : header ? Date.parse(header) - now() : 0;
              limit.until = Math.max(limit.until, now() + Math.max(1000, Number.isFinite(delay) ? delay : 0));
            }
            if (!response.ok && response.status !== 304) {
              if ([401, 403, 404].includes(response.status)) {
                current.cached = null;
                await persist({
                  id, kind: 'github-resource-invalidated', createdAt: new Date(now()).toISOString()
                });
              }
              throw new Error(`http-${response.status}`);
            }
            let data = current.cached?.data;
            if (response.status !== 304) {
              const text = await response.text();
              if (text.length > MAX_BODY_BYTES) throw new Error('invalid-response');
              data = normalizedResource(JSON.parse(text), resource.resource === 'latest-release');
            }
            if (!data) throw new Error('invalid-response');
            const cacheControl = response.headers.get('Cache-Control') ?? '';
            const lifetimeMs = Math.min(FRESH_MS, Number(cacheControl.match(/(?:^|,)\s*max-age=(\d+)/i)?.[1] ?? 300) * 1000);
            const staleMs = Math.min(STALE_MS, Number(cacheControl.match(/(?:^|,)\s*stale-while-revalidate=(\d+)/i)?.[1] ?? 86400) * 1000);
            const age = Number(response.headers.get('Age') ?? 0);
            const ageMs = Number.isFinite(age) ? Math.max(0, age * 1000) : 0;
            const staleResponse = /(?:^|,)\s*110\b/.test(response.headers.get('Warning') ?? '');
            const freshMs = staleResponse ? 0 : Math.max(0, lifetimeMs - ageMs);
            const staleUntil = now() + Math.max(0, lifetimeMs + staleMs - ageMs);
            if (signal.aborted) return;
            const updated = {
              id, kind: 'github-resource-cache', createdAt: new Date(now()).toISOString(), data,
              etag: response.headers.get('ETag') ?? (response.status === 304 ? current.cached?.etag ?? null : null),
              freshUntil: now() + freshMs,
              staleUntil: staleResponse ? Math.min(current.cached?.staleUntil ?? staleUntil, staleUntil) : staleUntil
            };
            await persist(updated);
            if (signal.aborted) return;
            current.cached = updated;
            current.attempts = 0;
            snapshot(current, 'network', staleResponse ? 'stale-response' : null, limit.until > now() ? limit.until : null);
            schedule(Math.max(staleResponse ? 30_000 : 1000, freshMs, limit.until - now()));
            debugGitHubService({
              event: 'refresh-succeeded', status: response.status, notModified: response.status === 304,
              staleResponse, rateLimited: limit.until > now()
            });
          } catch (error) {
            if (signal.aborted) return;
            current.attempts++;
            const message = error instanceof GitHubStorageError ? error.message
              : error instanceof Error && /^http-\d+$/.test(error.message) ? error.message : 'unavailable';
            const retryable = ['unavailable', 'storage-unavailable'].includes(message) || /^http-(429|5\d\d)$/.test(message);
            const backoff = current.attempts <= 3
              ? Math.min(60_000, (dependencies.retryBaseMs ?? 1000) * 2 ** (current.attempts - 1))
              : FRESH_MS;
            const delay = Math.max(limit.until - now(), backoff);
            snapshot(current, 'cache', message, retryable ? now() + delay : null);
            if (retryable) schedule(delay);
            debugGitHubService({ event: 'refresh-failed', reason: message, attempt: current.attempts, retryable, retryDelayMs: retryable ? delay : 0 });
          } finally {
            clearTimeout(timeout);
            signal.removeEventListener('abort', abort);
            if (acquired) activeRequests--;
          }
        }
        if (current.cached && current.cached.freshUntil > now()) schedule(current.cached.freshUntil - now());
        else void refresh();
      } catch (error) {
        snapshot(current, 'cache', error instanceof GitHubStorageError ? error.message : base ? 'unavailable' : 'backend-unconfigured');
      }
    }
  }
  function invalidateAuthentication() {
    authenticationGeneration++;
    debugGitHubService({ event: 'authentication-invalidated', generation: authenticationGeneration, subscriptionCount: entries.size });
    for (const entry of entries.values()) {
      if (entry.cached) void recordTransaction(database, {
        id: entry.cached.id, kind: 'github-resource-invalidated', createdAt: new Date(now()).toISOString()
      }).catch(() => { debug({ event: 'storage-failed', operation: 'invalidate' }); });
      entry.cached = null;
      snapshot(entry, 'cache', 'authentication-changed');
      entry.controller.abort();
      clearTimeout(entry.timer);
      clearTimeout(entry.expiryTimer);
      entry.listeners.clear();
    }
    entries.clear();
    limits.clear();
    ephemeralScopes.clear();
  }
  return { subscribe, invalidateAuthentication };
}
