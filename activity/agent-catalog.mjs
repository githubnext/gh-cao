/**
 * Node adapter over the protocol-independent agent catalog.
 *
 * The CLI and the read-only MCP server both discover dashboard pages and named
 * queries through this module, so neither transport maintains its own catalog
 * or its own query semantics.
 */
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import {
  agentCatalog,
  describePage,
  describeQuery,
  listPages,
  listQueries
} from '../dashboard/site/src/agent/catalog.js';
import { executeNamedQuery, NamedQueryError } from '../dashboard/site/src/agent/query-executor.js';
import { STATISTICAL_CAPACITIES } from '../dashboard/site/src/data/queries/statistical-sample.js';
import { loadDashboardSource } from '../dashboard/report/bundle-dashboards.mjs';

export { NamedQueryError };

/** Dashboard definition installed alongside the CLI. */
export const DEFAULT_DASHBOARD_PATH = fileURLToPath(
  new URL('../dashboard/site/dashboard.json', import.meta.url)
);

/** @type {Map<string, Promise<unknown>>} */
const documents = new Map();

/**
 * Loads one dashboard document, reusing the parsed document within a process.
 * @param {string} [dashboardPath]
 */
export function loadAgentDashboardDocument(dashboardPath) {
  const resolved = path.resolve(dashboardPath || DEFAULT_DASHBOARD_PATH);
  const cached = documents.get(resolved);
  if (cached) return cached;
  const pending = loadDashboardSource(resolved).then((loaded) => loaded.document);
  documents.set(resolved, pending);
  return pending;
}

/** @param {string} value */
function indent(value) {
  return value ? `    ${value}` : '';
}

/**
 * @param {ReturnType<typeof listPages>} pages
 */
function formatPages(pages) {
  return [
    `Dashboard pages (${pages.length}):`,
    '',
    ...pages.flatMap((page) => [
      `  ${page.id}${page.experimental ? ' (experimental)' : ''}  ${page.title}`,
      indent(page.description),
      indent(page.parameters.length > 0
        ? `parameters: ${page.parameters.map((parameter) => (
            parameter.required ? `${parameter.name} (required)` : parameter.name
          )).join(', ')}`
        : ''),
      indent(page.queries.length > 0 ? `queries: ${page.queries.join(', ')}` : ''),
      ''
    ]).filter((line) => line !== '')
  ].join('\n');
}

/**
 * @param {ReturnType<typeof listQueries>[number]} query
 */
function formatQuery(query) {
  return [
    `  ${query.id}${query.execution.local ? '' : '  (not locally executable)'}`,
    indent(query.subject || query.description),
    indent(query.objective ? `objective: ${query.objective}` : ''),
    indent(query.acceptance ? `acceptance: ${query.acceptance}` : ''),
    indent(query.parameters.length > 0
      ? `parameters: ${query.parameters.map((parameter) => parameter.name).join(', ')}`
      : ''),
    indent(query['used-by-pages'].length > 0 ? `pages: ${query['used-by-pages'].join(', ')}` : ''),
    indent(query.execution.local
      ? `sqlite collections: ${(query.execution.requirements ?? []).join(', ')}`
      : String(query.execution.reason))
  ].filter((line) => line !== '').join('\n');
}

/**
 * Describes dashboard pages for the `cao pages` command.
 *
 * @param {{ dashboardPath?: string, pageId?: string, json?: boolean }} [request]
 */
export async function describeAgentPages({ dashboardPath, pageId, json } = {}) {
  const document = await loadAgentDashboardDocument(dashboardPath);
  if (pageId) {
    const page = describePage(document, pageId);
    if (!page) throw new NamedQueryError(`Unknown dashboard page: ${pageId}`);
    return json ? { command: 'pages', page } : formatPages([page]);
  }
  const pages = listPages(document);
  return json ? { command: 'pages', pages } : formatPages(pages);
}

/**
 * Describes named dashboard queries for the `cao queries` and `cao query-info`
 * commands.
 *
 * @param {{ dashboardPath?: string, queryId?: string, json?: boolean }} [request]
 */
export async function describeAgentQueries({ dashboardPath, queryId, json } = {}) {
  const document = await loadAgentDashboardDocument(dashboardPath);
  if (queryId) {
    const query = describeQuery(document, queryId);
    if (!query) throw new NamedQueryError(`Unknown dashboard query: ${queryId}`);
    return json ? { command: 'query-info', query } : formatQuery(query);
  }
  const queries = listQueries(document);
  return json
    ? { command: 'queries', queries }
    : [
        `Dashboard queries (${queries.length}):`,
        '',
        ...queries.map((query) => `${formatQuery(query)}\n`)
      ].join('\n');
}

/**
 * Returns the whole catalog for one dashboard document.
 * @param {{ dashboardPath?: string }} [request]
 */
export async function readAgentCatalog({ dashboardPath } = {}) {
  return agentCatalog(await loadAgentDashboardDocument(dashboardPath));
}

/**
 * Executes one reviewed named dashboard query against the local projection.
 *
 * @param {{
 *   indexedDB: IDBFactory,
 *   dashboardPath?: string,
 *   queryId: string,
 *   parameters?: Record<string, unknown>,
 *   limit?: number,
 *   signal?: AbortSignal,
 *   unknownQueryHint?: string
 * }} request
 */
export async function runNamedDashboardQuery({
  indexedDB,
  dashboardPath,
  queryId,
  parameters,
  limit,
  signal,
  unknownQueryHint
}) {
  const document = await loadAgentDashboardDocument(dashboardPath);
  try {
    return await executeNamedQuery({
      indexedDB, document, queryId, parameters, limit, signal, statisticalCapacity: STATISTICAL_CAPACITIES.sqlite
    });
  } catch (error) {
    if (unknownQueryHint && error instanceof NamedQueryError && error.message.startsWith('Unknown dashboard query')) {
      throw new NamedQueryError(`${error.message}; ${unknownQueryHint}`);
    }
    throw error;
  }
}

/**
 * Parses repeatable `--param NAME=VALUE` CLI arguments.
 * @param {string | string[] | undefined} values
 * @returns {Record<string, string>}
 */
export function parseNamedQueryParameters(values) {
  const list = values === undefined ? [] : Array.isArray(values) ? values : [values];
  /** @type {Record<string, string>} */
  const parameters = {};
  for (const entry of list) {
    const separator = entry.indexOf('=');
    if (separator < 1) throw new NamedQueryError(`Invalid --param value: ${entry}`);
    const name = entry.slice(0, separator);
    if (Object.hasOwn(parameters, name)) {
      throw new NamedQueryError(`Parameter --param ${name} may only be specified once`);
    }
    parameters[name] = entry.slice(separator + 1);
  }
  return parameters;
}
