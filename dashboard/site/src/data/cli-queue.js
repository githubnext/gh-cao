import { createDebug } from '../debug.js';
import { executeDashboardQueries, resolveDashboardQuerySources } from './queries/declarative.js';
import { compileDashboardViewPayloadQueries } from './queries/view-payload-compiler.js';

export const CLI_QUEUE_SOURCES = new Set(['cli-commands', 'cli-command-summary', 'cli-command-statuses']);
const debugCliQueue = createDebug('cli-queue');
const COMMAND_STATUSES = ['queued', 'running', 'completed', 'failed', 'canceled', 'timed-out'];
let enabled = false;
let endpoint = '';

/** @param {boolean} value @param {string} [url] */
export function configureCliQueue(value, url) {
  enabled = value === true;
  if (url) {
    const parsed = new URL(url);
    if (globalThis.location?.origin && parsed.origin !== globalThis.location.origin) {
      throw new Error('Command queue endpoint must be same-origin.');
    }
    endpoint = parsed.href;
  }
}

export function cliQueueEnabled() { return enabled; }

/** @param {unknown[]} queries @param {Iterable<string>} names */
export function isCliQueueQuery(queries, names) {
  const dependencies = resolveDashboardQuerySources(queries, [...names]);
  return dependencies.some((name) => CLI_QUEUE_SOURCES.has(name));
}

/**
 * Runtime operational data is deliberately never ingested into IndexedDB.
 * The server owns filtering, pagination, admission, and command lifecycle.
 * @param {string} path
 * @param {RequestInit} [options]
 */
async function request(path, options = {}) {
  if (!enabled) throw new Error('Command queue is unavailable in this dashboard.');
  const response = await fetch(`${endpoint || './__cli_commands'}${path}`, {
    cache: 'no-store', credentials: 'same-origin', ...options
  });
  if (!response.ok) {
    debugCliQueue({ event: 'request-failed', method: options.method ?? 'GET', status: response.status });
    const result = await response.json().catch(() => null);
    throw new Error(result?.error || `Command queue returned HTTP ${response.status}.`);
  }
  return response;
}

/**
 * @param {Record<string, unknown>} payload
 * @param {AbortSignal} [signal]
 * @param {(event: Record<string, unknown>) => void} [notify]
 */
export async function cliQueueOperation(payload, signal, notify = () => {}) {
  const action = payload.action;
  const id = (typeof payload.commandId === 'string' && payload.commandId)
    || (Number.isSafeInteger(payload.commandId) && Number(payload.commandId) > 0)
    ? encodeURIComponent(String(payload.commandId)) : null;
  if (action === 'enqueue') {
    const response = await request('', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload.command), signal
    });
    return response.json();
  }
  if (action === 'list') {
    const params = new URLSearchParams();
    for (const key of ['status', 'limit', 'offset']) {
      if (payload[key] !== undefined) params.set(key, String(payload[key]));
    }
    return (await request(`?${params}`, { signal })).json();
  }
  if (!id) throw new Error('A persistent command ID is required.');
  if (action === 'get') return (await request(`/${id}`, { signal })).json();
  if (action === 'cancel' || action === 'retry') {
    return (await request(`/${id}/${action}`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload.idempotencyKey ? { idempotencyKey: payload.idempotencyKey } : {}),
      signal
    })).json();
  }
  if (action !== 'attach') throw new Error('Unknown command queue operation.');
  const after = Number.isSafeInteger(payload.after) && Number(payload.after) >= 0 ? Number(payload.after) : 0;
  const response = await request(`/${id}/output?after=${after}`, { signal });
  if (!response.body) throw new Error('Command output stream is unavailable.');
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let complete;
  let sequence = after;
  const consume = (/** @type {string} */ line) => {
    if (!line.trim()) return;
    const event = JSON.parse(line);
    if (event.type === 'output' && Number.isSafeInteger(event.sequence) && event.sequence > sequence) {
      sequence = event.sequence;
      notify(event);
    } else if (event.type === 'status') notify(event);
    else if (event.type === 'complete') complete = event;
    else if (event.type === 'error') throw new Error(event.error || 'Command output failed.');
  };
  try {
    while (true) {
      signal?.throwIfAborted();
      const { value, done } = await reader.read();
      buffer += decoder.decode(value, { stream: !done });
      const lines = buffer.split('\n');
      buffer = lines.pop() ?? '';
      for (const line of lines) consume(line);
      if (done) break;
    }
    consume(buffer);
    debugCliQueue({ event: 'attach', after, lastSequence: sequence, completed: Boolean(complete) });
    if (!complete) throw new Error('Command output disconnected. Reopen the command to resume.');
    return complete;
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}

/** @param {string} name @param {Record<string, unknown>[]} rows @param {boolean} available @returns {import('../presenter.js').LogicalSourceInput} */
function source(name, rows, available) {
  return {
    source: name,
    rows,
    metadata: {
      'source-id': name,
      'source-kind': 'runtime-command-queue',
      'as-of': new Date().toISOString(),
      'retrieved-at': new Date().toISOString(),
      availability: available ? (rows.length ? 'available' : 'empty') : 'unavailable',
      completeness: available ? 'complete' : 'unknown',
      freshness: available ? 'fresh' : 'unknown'
    }
  };
}

/**
 * Executes the actual document query and view compiler over bounded server rows.
 * @param {Record<string, any>} payload
 * @param {AbortSignal} [signal]
 */
export async function queryCliQueue(payload, signal) {
  const context = payload.context;
  const requested = new Set(/** @type {string[]} */ (payload.sourceNames ?? []));
  const page = context.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === payload.pageId);
  const compiled = page ? compileDashboardViewPayloadQueries(page, payload.pageId, {
    queries: context.queries ?? [], views: context.views ?? [],
    routeParameters: payload.routeParameters,
    queryContext: payload.queryContext, viewId: payload.viewId, sourceNames: requested,
    backend: 'static'
  }) : { queries: [], aliases: [], replacedSources: [] };
  const queries = [...(context.queries ?? []), ...compiled.queries];
  const names = new Set([...requested].filter((name) => !compiled.replacedSources.includes(name)));
  for (const alias of compiled.aliases) names.add(alias);
  const paging = Object.values(payload.pagination ?? {})[0];
  const limit = Math.min(200, Math.max(1, Number(paging?.limit ?? 50)));
  let offset = 0;
  if (paging?.continuationToken) {
    const cursor = JSON.parse(atob(paging.continuationToken));
    if (!Number.isSafeInteger(cursor.offset) || cursor.offset < 0) throw new Error('Invalid command page cursor.');
    offset = cursor.offset;
  }
  const selectedStatus = payload.queryContext?.viewFilters?.[payload.viewId]?.status
    ?? payload.queryContext?.filters?.status;
  const status = selectedStatus?.join(',') ?? '';
  const result = enabled ? await cliQueueOperation({ action: 'list', status, limit, offset }, signal) : null;
  /** @type {Record<string, import('../presenter.js').LogicalSourceInput>} */
  const inputs = {
    'cli-commands': source('cli-commands', (result?.commands ?? []).map((/** @type {Record<string, unknown>} */ command) => ({
      ...command,
      ...Object.fromEntries(['createdAt', 'startedAt', 'completedAt'].map((field) => [
        field, typeof command[field] === 'number' ? new Date(command[field]).toISOString() : command[field]
      ]))
    })), enabled),
    'cli-command-summary': source('cli-command-summary', result ? [{
      running: result.running, total: result.total, 'paused-reason': result.pausedReason ?? '', revision: result.revision
    }] : [], enabled),
    'cli-command-statuses': source('cli-command-statuses', COMMAND_STATUSES.map((status) => ({ status })), enabled)
  };
  debugCliQueue({
    event: 'query', enabled, limit, offset, rows: result?.commands?.length ?? 0, total: result?.total ?? 0,
    sources: names.size
  });
  const output = new Map(Object.entries(executeDashboardQueries(queries, inputs, names, { signal })));
  /** @type {Array<[string, import('../presenter.js').LogicalSourceInput]>} */
  const entries = [];
  for (const name of names) {
    const value = output.get(name) ?? (Object.hasOwn(inputs, name) ? inputs[name] : undefined);
    if (!value) throw new Error('Declared command queue source is unavailable.');
    if (resolveDashboardQuerySources(queries, [name]).includes('cli-commands') && result) {
      value.metadata['total-row-count'] = result.total;
      if (offset + limit < result.total) value.continuationToken = btoa(JSON.stringify({ offset: offset + limit }));
    }
    entries.push([name, value]);
  }
  return Object.fromEntries(entries);
}
