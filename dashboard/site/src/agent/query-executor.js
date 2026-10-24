/**
 * Shared named-query executor for agent transports.
 *
 * The `cao` CLI and the read-only MCP server execute reviewed Dashboard
 * Language queries through this module so both transports resolve the same
 * dependencies, read the same canonical stores, and report the same freshness
 * and completeness metadata. It never accepts SQL and never accepts an
 * unreviewed query definition: only query identifiers declared by the dashboard
 * document can be executed.
 */

import { createDebug } from '../debug.js';
import { loadDatabaseQuerySources } from '../data/queries/database.js';
import { DASHBOARD_QUERY_LIMITS, resolveDashboardQuerySources } from '../data/queries/declarative.js';
import { resolveDashboardQueryParameters } from '../data/queries/view-payload-compiler.js';
import { describeQuery, queryExecutionRequirements, queryParameters } from './catalog.js';
import mcpContract from './mcp-contract.json' with { type: 'json' };

const debugQueryExecutor = createDebug('query-executor');

/** Maximum rows one named-query result returns when no smaller limit is given. */
export const DEFAULT_NAMED_QUERY_LIMIT = mcpContract.limits.defaultQueryRows;

/** Hard upper bound on rows returned by one named-query result. */
export const MAX_NAMED_QUERY_LIMIT = mcpContract.limits.maxQueryRows;

/** Maximum number of parameters accepted by one named-query invocation. */
export const MAX_NAMED_QUERY_PARAMETERS = mcpContract.limits.maxParameters;

/** Maximum length of one parameter value. */
export const MAX_NAMED_QUERY_PARAMETER_LENGTH = mcpContract.limits.maxParameterLength;

/** Raised for invalid agent input so transports can report it without a stack trace. */
export class NamedQueryError extends Error {}

/**
 * @param {unknown} value
 * @returns {value is Record<string, unknown>}
 */
function isPlainObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/**
 * Validates the requested parameters against the parameters the dashboard
 * declares for one query.
 *
 * @param {unknown} document
 * @param {string} queryId
 * @param {unknown} parameters
 * @returns {Array<{ name: string, field?: string, value: string|number|boolean, type?: string }>}
 */
export function resolveNamedQueryParameters(document, queryId, parameters) {
  if (parameters !== undefined && parameters !== null && !isPlainObject(parameters)) {
    throw new NamedQueryError('Query parameters must be an object of name/value pairs');
  }
  const entries = Object.entries(parameters ?? {});
  if (entries.length > MAX_NAMED_QUERY_PARAMETERS) {
    throw new NamedQueryError(`At most ${MAX_NAMED_QUERY_PARAMETERS} query parameters are accepted`);
  }
  const declared = new Map(queryParameters(document, queryId).map((parameter) => [parameter.name, parameter]));
  const supplied = new Set(entries.map(([name]) => name));
  for (const parameter of declared.values()) {
    if (parameter.required && !supplied.has(parameter.name)) {
      throw new NamedQueryError(`Query ${queryId} requires parameter "${parameter.name}"`);
    }
  }
  return entries.map(([name, value]) => {
    const parameter = declared.get(name);
    if (!parameter) {
      const known = [...declared.keys()].toSorted().join(', ') || 'none';
      throw new NamedQueryError(`Unknown parameter "${name}" for query ${queryId}; declared parameters: ${known}`);
    }
    if (typeof value !== 'string' && typeof value !== 'number' && typeof value !== 'boolean') {
      throw new NamedQueryError(`Parameter "${name}" must be a string, number, or boolean`);
    }
    const input = String(value);
    if (input.length > MAX_NAMED_QUERY_PARAMETER_LENGTH) {
      throw new NamedQueryError(`Parameter "${name}" exceeds ${MAX_NAMED_QUERY_PARAMETER_LENGTH} characters`);
    }
    if (!parameter.type) return { name: parameter.name, field: parameter.field, value: input };
    const typed = value;
    const schema = parameter.schema ?? {};
    if (typeof typed !== parameter.type
        || (parameter.type === 'number' && (typeof typed !== 'number' || !Number.isFinite(typed)
          || typeof schema.minimum === 'number' && typed < schema.minimum
          || typeof schema.maximum === 'number' && typed > schema.maximum
          || typeof schema.multipleOf === 'number' && Math.abs(typed / schema.multipleOf - Math.round(typed / schema.multipleOf)) > 1e-9))
        || (Array.isArray(schema.enum) && !schema.enum.includes(typed))) {
      throw new NamedQueryError(`Invalid value for query parameter "${name}"`);
    }
    return { name: parameter.name, type: parameter.type, value: typed };
  });
}

/**
 * @param {unknown} limit
 * @returns {number}
 */
function resolveLimit(limit) {
  if (limit === undefined || limit === null) return DEFAULT_NAMED_QUERY_LIMIT;
  const value = typeof limit === 'number' ? limit : Number(limit);
  if (!Number.isInteger(value) || value < 1) {
    throw new NamedQueryError('Query limit must be a positive integer');
  }
  return Math.min(value, MAX_NAMED_QUERY_LIMIT);
}

/**
 * Compiles the requested parameters and row bound into derived declarative
 * queries so narrowing and limiting are executed by the query engine rather
 * than by JavaScript over a materialized result.
 *
 * The requested query is cloned under a private name because a few dashboard
 * query names are also declared table sources, and a derived query reading such
 * a name would consume the empty table placeholder instead of the query result.
 *
 * @param {string} queryId
 * @param {Array<Record<string, unknown>>} queries
 * @param {Array<{ field: string, value: string }>} filters
 * @param {number} maxRows
 * @returns {{ alias: string, queries: Array<Record<string, unknown>> }}
 */
function compileBoundedQueries(queryId, queries, filters, maxRows) {
  const alias = `agent:${queryId}`;
  const sourceName = `${alias}:source`;
  const definition = queries.find((query) => isPlainObject(query) && query.name === queryId);
  const clone = { ...(definition ?? { from: queryId }), name: sourceName };
  return {
    alias,
    queries: [clone, {
      name: alias,
      from: sourceName,
      ...(filters.length > 0
        ? { filter: { predicates: filters.map((filter) => ({ field: filter.field, equals: filter.value })) } }
        : {}),
      limit: Math.min(maxRows + 1, DASHBOARD_QUERY_LIMITS['max-output-rows'])
    }]
  };
}

/**
 * Executes one reviewed named Dashboard Language query against the local
 * canonical projection.
 *
 * @param {{
 *   indexedDB: IDBFactory,
 *   document: unknown,
 *   queryId: string,
 *   parameters?: Record<string, unknown>,
 *   limit?: number,
 *   signal?: AbortSignal,
 *   statisticalCapacity?: number
 * }} request
 * @returns {Promise<{ query: string, rows: Record<string, unknown>[], metadata: Record<string, unknown> }>}
 */
export async function executeNamedQuery({ indexedDB, document, queryId, parameters, limit, signal, statisticalCapacity }) {
  const id = typeof queryId === 'string' ? queryId.trim() : '';
  if (!id) throw new NamedQueryError('A dashboard query identifier is required');
  const definition = describeQuery(document, id);
  if (!definition) {
    debugQueryExecutor({ query: id, outcome: 'unknown-query' });
    throw new NamedQueryError(`Unknown dashboard query: ${id}`);
  }
  const resolvedParameters = resolveNamedQueryParameters(document, id, parameters);
  const filters = resolvedParameters.filter((parameter) => parameter.field);
  const maxRows = resolveLimit(limit);
  const execution = queryExecutionRequirements(document, id);
  if (!execution.local) {
    debugQueryExecutor({
      query: id,
      outcome: 'unavailable',
      missingCount: Array.isArray(execution.missing) ? execution.missing.length : 0
    });
    return {
      query: id,
      rows: [],
      metadata: {
        availability: 'unavailable',
        completeness: 'unknown',
        freshness: 'unknown',
        'query-diagnostic': String(execution.reason),
        requirements: execution.requirements
      }
    };
  }
  signal?.throwIfAborted?.();
  const queries = isPlainObject(document) && isPlainObject(document.dashboard)
    && Array.isArray(document.dashboard.queries)
    ? document.dashboard.queries
    : [];
  const required = new Set(resolveDashboardQuerySources(queries, [id]));
  const scoped = queries.filter((query) => isPlainObject(query) && typeof query.name === 'string' && required.has(query.name));
  const operandValues = Object.fromEntries(resolvedParameters.filter((parameter) => parameter.type)
    .map((parameter) => [parameter.name, parameter.value]));
  const resolved = /** @type {Array<Record<string, unknown>>} */ (resolveDashboardQueryParameters(scoped, operandValues));
  const bounded = compileBoundedQueries(id, resolved, /** @type {Array<{field:string,value:string}>} */ (filters), maxRows);
  const sources = await loadDatabaseQuerySources(indexedDB, {}, {
    signal, statisticalCapacity,
    sourceNames: [bounded.alias],
    queries: [...resolved, ...bounded.queries]
  });
  const source = /** @type {{ rows?: unknown, metadata?: unknown } | undefined} */ (sources[bounded.alias]);
  signal?.throwIfAborted?.();
  const metadata = isPlainObject(source?.metadata) ? source.metadata : {};
  const selected = /** @type {Record<string, unknown>[]} */ (Array.isArray(source?.rows) ? source.rows : []);
  // The bounded query requests one row beyond the limit so truncation is
  // reported as partial completeness rather than silently returned as complete.
  const truncated = selected.length > maxRows;
  const rows = truncated ? selected.slice(0, maxRows) : selected;
  debugQueryExecutor({
    query: id,
    outcome: 'executed',
    'returned-rows': rows.length,
    truncated,
    completeness: truncated ? 'partial' : (metadata.completeness ?? 'unknown')
  });
  return {
    query: id,
    rows,
    metadata: {
      ...Object.fromEntries(Object.entries(metadata['aggregate-version'] === 1 ? metadata : {}).filter(([field]) =>
        field.startsWith('aggregate-') || ['excluded-runs', 'conflicted-runs', 'query-diagnostic'].includes(field))),
      availability: typeof metadata.availability === 'string'
        ? (metadata.availability === 'available' && rows.length === 0 ? 'empty' : metadata.availability)
        : 'unknown',
      completeness: truncated ? 'partial' : (metadata.completeness ?? 'unknown'),
      freshness: metadata.freshness ?? 'unknown',
      'as-of': metadata['as-of'] ?? '',
      'returned-rows': rows.length,
      limit: maxRows,
      ...(metadata['statistical-frame'] ? { 'statistical-frame': metadata['statistical-frame'] } : {}),
      ...(metadata['sample-frame'] ? { 'sample-frame': metadata['sample-frame'] } : {}),
      ...(resolvedParameters.length > 0
        ? { parameters: Object.fromEntries(resolvedParameters.map((parameter) => [parameter.name, parameter.value])) }
        : {})
    }
  };
}
