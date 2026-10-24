/**
 * Declarative dashboard data queries.
 *
 * A query is a closed, structured projection over declared logical sources.
 * Queries contain no scripts, callbacks, SQL text, or executable expressions:
 * every clause compiles to the serializable row operators in
 * `data-operations.js` so the whole pipeline runs inside the data Web Worker.
 *
 * Clause execution order is fixed and deterministic:
 * `from` -> `union` -> `joins` -> `filter` -> `compute` -> `aggregate` -> `predict` -> `window` ->
 * `select` -> `order-by` -> `limit`.
 */

import { computeRows, PREDICTION_METHODS, tidy } from '../../data-operations.js';
import { createDebug } from '../../debug.js';
import { SIMULATION_DAYS, simulationDaysSource } from './simulation-days.js';
import { validFacetDefinition } from './facet.js';

const debugQuery = createDebug('data:query');

/**
 * @typedef {Record<string, unknown>} Row
 * @typedef {import('../../presenter.js').LogicalSourceInput} LogicalSourceInput
 * @typedef {import('../../presenter.js').SourceMetadata} SourceMetadata
 */

/**
 * @typedef {{
 *   name: string,
 *   description?: string,
 *   from: string,
 *   stores?: string[],
 *   union?: string[],
 *   time?: { range?: string, start?: string, end?: string },
 *   joins?: Array<{ source: string, type?: 'inner'|'left'|'inner-many', on: Array<{ left: string, right: string }>, fields: Array<{ field: string, as: string }> }>,
 *   filter?: { predicates?: Array<{ field: string, equals?: unknown, in?: unknown[], includes?: string, gte?: unknown, lt?: unknown, optional?: boolean }>, search?: { fields: string[], query: string } },
 *   compute?: import('../../data-operations.js').ComputedField[],
 *   ['temporal-series']?: import('../../data-operations.js').TemporalSeriesDefinition,
 *    aggregate?: { by?: string[], values: Array<{ field: string, as: string, reducer: 'count'|'distinct-count'|'distinct-list'|'distinct-values'|'sum'|'mean'|'min'|'max'|'unique'|'median'|'p75'|'p90'|'p95', filter?: { predicates: Array<{ field: string, equals?: string|number|boolean, in?: Array<string|number|boolean> }> } }> },
 *   predict?: import('../../data-operations.js').PredictedField[],
 *   window?: import('../../data-operations.js').WindowField[],
 *   select?: Array<{ field: string, as?: string }>,
 *   ['order-by']?: Array<{ field: string, direction?: 'asc'|'desc' }>,
 *   limit?: number,
 *   facet?: import('./facet.js').FacetDefinition
 * }} DashboardQuery
 */

/**
 * Documented resource limits. A query that would exceed any limit fails
 * closed and reports an explicit unavailable state instead of truncating.
 */
export const DASHBOARD_QUERY_LIMITS = {
  'max-input-rows': 200000,
  'max-join-rows': 200000,
  'max-output-rows': 100000,
  'max-joins': 4,
  'max-aggregate-values': 64,
  'max-aggregate-filter-predicates': 8,
  'max-predicate-alternatives': 32,
  'max-operations': 5000000,
  'max-duration-ms': 60000
};

const CONTINUATION_TOKEN_VERSION = 2;

/**
 * Applies request-scoped pagination after query execution so query definitions
 * remain independent of transport cursors. Tokens are stateless: continuing a
 * query recompiles it and locates the last emitted run in the new result.
 *
 * @param {Record<string, LogicalSourceInput>} sources
 * @param {Record<string, { limit: number, continuationToken?: string }>} [pagination]
 * @param {string} [revision]
 */
export function paginateDashboardSources(
  sources,
  pagination = {},
  revision = continuationRevision(Object.fromEntries(
    Object.entries(sources).map(([name, source]) => [name, source.metadata])
  ))
) {
  return Object.fromEntries(Object.entries(sources).map(([name, source]) => {
    const page = pagination[name];
    if (!page) return [name, source];
    if (!Number.isSafeInteger(page.limit) || page.limit <= 0
        || page.limit > DASHBOARD_QUERY_LIMITS['max-output-rows']) {
      throw new TypeError(`Pagination limit for "${name}" must be a positive integer no greater than ${DASHBOARD_QUERY_LIMITS['max-output-rows']}.`);
    }
    const rows = Array.isArray(source.rows) ? source.rows : [];
    const cursor = page.continuationToken
      ? decodeContinuationToken(page.continuationToken, name, revision)
      : null;
    let offset = cursor?.offset ?? 0;
    if (cursor?.run !== undefined) {
      const expected = rows[offset - 1];
      if (runCursor(expected) !== cursor.run) {
        const anchor = rows.findIndex((row) => runCursor(row) === cursor.run);
        if (anchor < 0) throw new TypeError(`Continuation token for "${name}" no longer identifies a result row.`);
        offset = anchor + 1;
      }
    }
    const end = Math.min(rows.length, offset + page.limit);
    const pageRows = rows.slice(offset, end);
    const continuationToken = end < rows.length
      ? encodeContinuationToken({
          source: name,
          offset: end,
          run: runCursor(pageRows.at(-1)),
          revision
        })
      : undefined;
    return [name, {
      ...source,
      rows: pageRows,
      ...(continuationToken ? { continuationToken } : {}),
      metadata: { ...source.metadata, 'total-row-count': rows.length }
    }];
  }));
}

/** @param {Row | undefined} row */
function runCursor(row) {
  const run = row?.run;
  return typeof run === 'string' && /^\d+$/.test(run) ? run : undefined;
}

/** @param {{ source: string, offset: number, run?: string, revision: string }} cursor */
function encodeContinuationToken(cursor) {
  const bytes = new TextEncoder().encode(JSON.stringify({
    version: CONTINUATION_TOKEN_VERSION,
    ...cursor
  }));
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '');
}

/** @param {string} token @param {string} source @param {string} revision */
function decodeContinuationToken(token, source, revision) {
  try {
    const encoded = token.replaceAll('-', '+').replaceAll('_', '/');
    const binary = atob(encoded.padEnd(Math.ceil(encoded.length / 4) * 4, '='));
    const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
    const cursor = JSON.parse(new TextDecoder().decode(bytes));
    if (cursor?.version !== CONTINUATION_TOKEN_VERSION
        || cursor?.source !== source
        || cursor?.revision !== revision
        || !Number.isSafeInteger(cursor?.offset)
        || cursor.offset <= 0
        || (cursor.run !== undefined && (typeof cursor.run !== 'string' || !/^\d+$/.test(cursor.run)))) {
      throw new Error('invalid cursor');
    }
    return /** @type {{ source: string, offset: number, run?: string, revision: string }} */ (cursor);
  } catch {
    throw new TypeError(`Invalid or stale continuation token for "${source}".`);
  }
}

/**
 * Produces a deterministic, non-secret revision for stale-token detection.
 * Query definitions and data revisions are execution context, never cursor
 * fields in the authored query language.
 *
 * @param {unknown} queryContext
 * @param {unknown} [dataContext]
 */
export function continuationRevision(queryContext, dataContext) {
  const text = stableJson([queryContext, dataContext]);
  let hash = 2166136261;
  for (let index = 0; index < text.length; index += 1) {
    hash ^= text.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return `v1-${(hash >>> 0).toString(16).padStart(8, '0')}`;
}

/** @param {unknown} value @returns {string} */
function stableJson(value) {
  if (Array.isArray(value)) return `[${value.map(stableJson).join(',')}]`;
  if (value && typeof value === 'object') {
    return `{${Object.entries(value)
      .filter(([, item]) => typeof item !== 'function')
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, item]) => `${JSON.stringify(key)}:${stableJson(item)}`)
      .join(',')}}`;
  }
  return JSON.stringify(value) ?? 'null';
}

/** Rows processed between cancellation checkpoints inside a join. */
const CANCELLATION_CHECK_INTERVAL = 4096;

/**
 * @typedef {ReturnType<typeof createDashboardQueryBudget>} QueryBudget
 */

/**
 * Raised when execution stops before it finished, either because the caller
 * aborted it, the deadline elapsed, or the operation budget was exhausted.
 * It is distinct from a query fault: no partial result is reported.
 */
export class DashboardQueryCancelledError extends Error {
  /** @param {string} message @param {'aborted'|'timeout'|'budget'} kind */
  constructor(message, kind) {
    super(message);
    this.name = 'DashboardQueryCancelledError';
    this.kind = kind;
  }
}

/**
 * Bounds one execution by wall-clock time, by the number of row operations it
 * performs, and by an external abort signal. Every bound is checked at the
 * same checkpoints, so a runaway computation stops on whichever bound it
 * reaches first.
 *
 * @param {{ signal?: { aborted?: boolean, reason?: unknown }, timeout?: number, maxOperations?: number, now?: () => number }} [options]
 */
export function createDashboardQueryBudget(options = {}) {
  const timeout = Number.isFinite(options.timeout) && Number(options.timeout) > 0
    ? Number(options.timeout)
    : DASHBOARD_QUERY_LIMITS['max-duration-ms'];
  const maxOperations = Number.isFinite(options.maxOperations) && Number(options.maxOperations) > 0
    ? Number(options.maxOperations)
    : DASHBOARD_QUERY_LIMITS['max-operations'];
  const now = options.now ?? (() => Date.now());
  const startedAt = now();
  let operations = 0;
  return {
    get operations() {
      return operations;
    },
    /**
     * Records row operations and stops execution when the budget is spent.
     * @param {number} count
     */
    spend(count) {
      operations += Math.max(0, count);
      if (operations > maxOperations) {
        throw new DashboardQueryCancelledError(
          `dashboard queries exceeded the max-operations budget of ${maxOperations}`,
          'budget'
        );
      }
      this.checkpoint();
    },
    /** Stops execution when the caller aborted it or the deadline elapsed. */
    checkpoint() {
      if (options.signal?.aborted) {
        throw new DashboardQueryCancelledError('dashboard queries were cancelled', 'aborted');
      }
      if (now() - startedAt > timeout) {
        throw new DashboardQueryCancelledError(
          `dashboard queries exceeded the max-duration-ms limit of ${timeout}`,
          'timeout'
        );
      }
    }
  };
}

/**
 * Indexes declared queries by name, preserving declaration order.
 * @param {unknown} definitions
 * @returns {Map<string, DashboardQuery>}
 */
export function dashboardQueryIndex(definitions) {
  /** @type {Map<string, DashboardQuery>} */
  const index = new Map();
  if (!Array.isArray(definitions)) return index;
  for (const definition of definitions) {
    if (!isPlainObject(definition) || typeof definition.name !== 'string') continue;
    if (index.has(definition.name)) continue;
    index.set(definition.name, /** @type {DashboardQuery} */ (definition));
  }
  return index;
}

/**
 * Detects the query patterns that cannot be executed safely, so they fail
 * closed before any rows are read instead of looping, expanding without
 * bound, or resolving to an arbitrary definition.
 *
 * Detected patterns are cyclic dependencies (including self-reference),
 * dependencies declared after the consuming query, duplicate query names,
 * unbounded keyless joins, excessive join chains, and out-of-range limits.
 *
 * @param {unknown} definitions
 * @returns {Map<string, string>} query name to the reason it is rejected
 */
export function dashboardQueryDefects(definitions) {
  /** @type {Map<string, string>} */
  const defects = new Map();
  const list = Array.isArray(definitions) ? definitions : [];
  const index = dashboardQueryIndex(definitions);

  /** @type {Set<string>} */
  const seen = new Set();
  for (const definition of list) {
    if (!isPlainObject(definition) || typeof definition.name !== 'string') continue;
    if (seen.has(definition.name)) {
      defects.set(definition.name, `query name "${definition.name}" is declared more than once`);
    }
    seen.add(definition.name);
  }

  /** @type {Set<string>} */
  const available = new Set();
  for (const [name, definition] of index) {
    for (const input of queryInputNames(definition)) {
      if (!index.has(input) || available.has(input)) continue;
      defects.set(name, input === name
        ? `query "${name}" reads itself`
        : reachesQuery(index, input, name)
          ? `query "${name}" and input source "${input}" form a dependency cycle`
          : `input source "${input}" is declared after "${name}"`);
      break;
    }
    available.add(name);
    const structural = queryStructuralDefect(definition);
    if (structural && !defects.has(name)) defects.set(name, structural);
  }

  let propagated = true;
  while (propagated) {
    propagated = false;
    for (const [name, definition] of index) {
      if (defects.has(name)) continue;
      const rejected = queryInputNames(definition).find((input) => defects.has(input));
      if (!rejected) continue;
      defects.set(name, `input source "${rejected}" is a rejected query`);
      propagated = true;
    }
  }
  return defects;
}

/**
 * Rejects the query shapes whose cost is unbounded or undefined regardless of
 * the rows they read.
 * @param {DashboardQuery} definition
 * @returns {string | undefined}
 */
function queryStructuralDefect(definition) {
  if (definition.facet !== undefined && !validFacetDefinition(definition.facet)) {
    return 'facet requires a field or row/column fields and an output as';
  }
  if (definition.union !== undefined
      && (!Array.isArray(definition.union) || definition.union.length === 0
        || definition.union.some((source) => typeof source !== 'string'))) {
    return 'union must contain one or more source names';
  }
  const joins = definition.joins ?? [];
  if (joins.length > DASHBOARD_QUERY_LIMITS['max-joins']) {
    return `joins exceed the max-joins limit of ${DASHBOARD_QUERY_LIMITS['max-joins']}`;
  }
  for (const join of joins) {
    if (!Array.isArray(join?.on) || join.on.length === 0) {
      return `join on "${String(join?.source)}" declares no equality keys`;
    }
  }
  if (definition['temporal-series'] !== undefined) {
    const series = definition['temporal-series'];
    const measures = series?.measures ?? [];
    const maps = series?.maps ?? [];
    if (!isPlainObject(series)
        || typeof series.time !== 'string'
        || typeof series.series !== 'string'
        || (series.shape !== undefined && !['tidy', 'groups', 'panels'].includes(series.shape))
        || (series.link !== undefined && (series.shape !== 'panels' || typeof series.link !== 'string'))
        || (series['point-carry'] !== undefined && (series.shape !== 'panels'
          || !Array.isArray(series['point-carry']) || series['point-carry'].length === 0
          || series['point-carry'].length > 16 || series['point-carry'].some((field) => typeof field !== 'string' || !field)))
        || (series.carry !== undefined && (!Array.isArray(series.carry) || series.carry.length > 16 || series.carry.some((field) => typeof field !== 'string')))
        || (series.trend !== undefined && (series.shape !== 'groups' || !isPlainObject(series.trend) || typeof series.trend.direction !== 'string'))
        || (!Array.isArray(measures) || measures.length > 64)
        || (!Array.isArray(maps) || maps.length > 64)
        || measures.length + maps.length === 0
        || measures.some((measure) => !isPlainObject(measure) || typeof measure.field !== 'string' || typeof measure.kind !== 'string')
        || maps.some((map) => !isPlainObject(map) || typeof map.field !== 'string' || typeof map.kind !== 'string')) {
      return 'temporal-series requires time, series, optional valid trend direction, and between 1 and 64 bounded measure or map definitions';
    }
  }
  if (definition.aggregate) {
    if (!Array.isArray(definition.aggregate.values)
        || definition.aggregate.values.length === 0
        || definition.aggregate.values.length > DASHBOARD_QUERY_LIMITS['max-aggregate-values']) {
      return `aggregate values must contain between 1 and ${DASHBOARD_QUERY_LIMITS['max-aggregate-values']} definitions`;
    }
    for (const value of definition.aggregate.values) {
      if (!isPlainObject(value)) return 'aggregate values must be mappings';
      if (value.filter === undefined) continue;
      if (!isPlainObject(value.filter)
          || Object.keys(value.filter).some((key) => key !== 'predicates')) {
        return 'aggregate filters must be mappings containing only predicates';
      }
      const predicates = value.filter?.predicates;
      if (!Array.isArray(predicates)
          || predicates.length === 0
          || predicates.length > DASHBOARD_QUERY_LIMITS['max-aggregate-filter-predicates']) {
        return `aggregate filter predicates must contain between 1 and ${DASHBOARD_QUERY_LIMITS['max-aggregate-filter-predicates']} definitions`;
      }
      for (const predicate of predicates) {
        const keys = Object.keys(predicate ?? {});
        const hasEquals = Object.hasOwn(predicate ?? {}, 'equals');
        const hasIn = Object.hasOwn(predicate ?? {}, 'in');
        const operators = Number(hasEquals) + Number(hasIn);
        if (typeof predicate?.field !== 'string' || operators !== 1
            || keys.some((key) => !['field', 'equals', 'in'].includes(key))
            || (hasEquals && !isAggregateFilterLiteral(predicate.equals))
            || (hasIn
              && (!Array.isArray(predicate.in)
                || predicate.in.length === 0
                || predicate.in.length > DASHBOARD_QUERY_LIMITS['max-predicate-alternatives']
                || predicate.in.some((candidate) => !isAggregateFilterLiteral(candidate))))) {
          return 'aggregate filter predicates must declare a field and exactly one bounded equals or in comparison';
        }
      }
    }
  }
  if (definition.predict !== undefined) {
    if (!Array.isArray(definition.predict) || definition.predict.length === 0 || definition.predict.length > 8) {
      return 'predict must contain between 1 and 8 prediction definitions';
    }
    for (const prediction of definition.predict) {
      const predictors = typeof prediction?.on === 'string'
        ? [prediction.on]
        : Array.isArray(prediction?.on) ? prediction.on : [];
      const groupby = prediction?.groupby ?? [];
      if (typeof prediction?.field !== 'string' || typeof prediction?.as !== 'string'
          || predictors.length === 0 || predictors.length > 8
          || predictors.some((field) => typeof field !== 'string')) {
        return 'prediction definitions require field, as, and between 1 and 8 predictor fields';
      }
      if (!Array.isArray(groupby) || groupby.some((field) => typeof field !== 'string')) {
        return 'prediction groupby must contain only field names';
      }
      if (prediction.method !== undefined
          && (typeof prediction.method !== 'string' || !PREDICTION_METHODS.includes(prediction.method))) {
        return `prediction method must be one of ${PREDICTION_METHODS.join(', ')}`;
      }
      if ((prediction.method ?? 'linear') !== 'linear' && predictors.length !== 1) {
        return `prediction method ${String(prediction.method)} requires exactly one predictor field`;
      }
      if (prediction.order !== undefined
          && ((prediction.method ?? 'linear') !== 'poly' || !Number.isSafeInteger(prediction.order)
            || prediction.order < 1 || prediction.order > 10)) {
        return 'prediction order is allowed only for poly and must be an integer from 1 to 10';
      }
    }
  }
  if (definition.window !== undefined) {
    if (!Array.isArray(definition.window) || definition.window.length === 0 || definition.window.length > 8) {
      return 'window must contain between 1 and 8 definitions';
    }
    const outputs = new Set();
    for (const entry of definition.window) {
      if (!isPlainObject(entry) || typeof entry.field !== 'string' || typeof entry.as !== 'string'
          || !['rolling', 'change', 'previous', 'row-number'].includes(String(entry.operation))
          || !Array.isArray(entry['order-by']) || entry['order-by'].length === 0 || entry['order-by'].length > 8
          || entry['order-by'].some((clause) => !isPlainObject(clause) || typeof clause.field !== 'string'
            || (clause.direction !== undefined && clause.direction !== 'asc' && clause.direction !== 'desc'))
          || (entry.groupby !== undefined && (!Array.isArray(entry.groupby) || entry.groupby.length === 0
            || entry.groupby.length > 8 || entry.groupby.some((field) => typeof field !== 'string')))
          || (entry.operation === 'rolling'
            ? !Number.isSafeInteger(entry.frame) || Number(entry.frame) < 1 || Number(entry.frame) > 1000
              || (entry.reducer !== undefined && !['sum', 'mean', 'min', 'max'].includes(String(entry.reducer)))
              || (entry.alignment !== undefined && !['trailing', 'centered'].includes(String(entry.alignment)))
              || (entry.alignment === 'centered' && Number(entry.frame) % 2 !== 1)
              || entry.mode !== undefined || entry['time-field'] !== undefined || entry.unit !== undefined
            : ['previous', 'row-number'].includes(String(entry.operation))
              ? entry.frame !== undefined || entry.reducer !== undefined || entry.alignment !== undefined
                || entry.mode !== undefined || entry['time-field'] !== undefined || entry.unit !== undefined
              : entry.frame !== undefined || entry.reducer !== undefined || entry.alignment !== undefined
              || (entry.mode !== undefined && !['absolute', 'percentage', 'rate'].includes(String(entry.mode)))
              || (entry.mode === 'rate'
                ? typeof entry['time-field'] !== 'string' || !['second', 'minute', 'hour', 'day'].includes(String(entry.unit))
                : entry['time-field'] !== undefined || entry.unit !== undefined))) {
        return 'window entries require ordered rolling, change, previous or row-number definitions';
      }
      if (outputs.has(entry.as)) return 'window output names must be unique';
      outputs.add(entry.as);
    }
  }
  if (definition.limit !== undefined
      && (!Number.isSafeInteger(definition.limit)
        || Number(definition.limit) <= 0
        || Number(definition.limit) > DASHBOARD_QUERY_LIMITS['max-output-rows'])) {
    return `limit must be a positive integer no greater than ${DASHBOARD_QUERY_LIMITS['max-output-rows']}`;
  }
  return undefined;
}

/**
 * @param {Map<string, DashboardQuery>} index
 * @param {string} from
 * @param {string} target
 * @returns {boolean} whether `target` is reachable from `from`
 */
function reachesQuery(index, from, target) {
  /** @type {Set<string>} */
  const visited = new Set();
  /** @type {string[]} */
  const pending = [from];
  while (pending.length > 0) {
    const name = /** @type {string} */ (pending.shift());
    if (name === target) return true;
    if (visited.has(name)) continue;
    visited.add(name);
    pending.push(...queryInputNames(index.get(name)));
  }
  return false;
}

/**
 * Resolves the query dependency graph so every input source required by the
 * requested sources is loaded and unrelated sources stay excluded.
 * @param {unknown} definitions
 * @param {Iterable<string>} requested
 * @param {Set<string>} [precomputed] query results already available without loading their inputs
 * @returns {string[]} requested names plus every transitively required source
 */
export function resolveDashboardQuerySources(definitions, requested, precomputed = new Set()) {
  const index = dashboardQueryIndex(definitions);
  /** @type {Set<string>} */
  const resolved = new Set();
  /** @type {string[]} */
  const pending = [...requested];
  while (pending.length > 0) {
    const name = /** @type {string} */ (pending.shift());
    if (resolved.has(name)) continue;
    resolved.add(name);
    if (precomputed.has(name)) continue;
    for (const input of queryInputNames(index.get(name))) {
      if (!resolved.has(input)) pending.push(input);
    }
  }
  return [...resolved];
}

/**
 * @param {DashboardQuery | undefined} definition
 * @returns {string[]}
 */
export function queryInputNames(definition) {
  if (!definition) return [];
  const unions = Array.isArray(definition.union) ? definition.union : [];
  const joins = Array.isArray(definition.joins) ? definition.joins : [];
  return [definition.from, ...unions, ...joins.map((join) => join.source)].filter((name) => typeof name === 'string');
}

/**
 * Derives the static output field schema of a query so encodings, filters,
 * and `order-by` references can be validated before execution.
 * @param {DashboardQuery} definition
 * @param {(source: string) => string[] | undefined} fieldsOf
 * @returns {string[] | undefined} undefined when an input schema is unknown
 */
export function dashboardQueryOutputFields(definition, fieldsOf) {
  const inputFields = fieldsOf(definition.from);
  if (!inputFields) return undefined;
  const unionFields = (definition.union ?? []).map((source) => fieldsOf(source));
  if (unionFields.some((fields) => !fields)) return undefined;
  /** @type {string[]} */
  let fields = [...new Set([...inputFields, ...unionFields.flatMap((candidate) => candidate ?? [])])];
  for (const join of definition.joins ?? []) {
    for (const field of join.fields ?? []) fields.push(field.as);
  }
  for (const computed of definition.compute ?? []) fields.push(computed.as);
  if (definition['temporal-series']) {
    fields = ['groups', 'panels'].includes(definition['temporal-series'].shape ?? '')
      ? [
          ...(definition['temporal-series'].carry ?? []),
          'metric',
          'metric-key',
          'metric-name',
          'metric-kind',
          'metric-group',
          definition['temporal-series'].shape === 'panels' ? 'series' : 'points',
          ...(definition['temporal-series'].trend
            ? ['trend-start-value', 'trend-end-value', 'trend-delta', 'trend-relative-percent', 'trend-observed-direction', 'trend-assessment', 'trend-observation-count']
            : [])
        ]
      : [...(definition['temporal-series'].carry ?? []), 'time', 'series', 'metric', 'metric-key', 'metric-name', 'metric-kind', 'metric-group', 'value'];
  }
  if (definition.aggregate) {
    fields = [...(definition.aggregate.by ?? []), ...definition.aggregate.values.map((value) => value.as)];
  }
  for (const predicted of definition.predict ?? []) fields.push(predicted.as);
  for (const entry of definition.window ?? []) fields.push(entry.as);
  if (definition.select) {
    fields = definition.select.map((field) => field.as ?? field.field);
  }
  if (definition.facet) {
    fields = ['facet-field', 'facet-row', 'facet-column', 'facet-row-index', 'facet-column-index', definition.facet.as];
  }
  return [...new Set(fields)];
}

/**
 * Creates lazy declared queries over already loaded logical sources.
 * Each query and its dependencies execute when that result is first consumed.
 *
 * @param {unknown} definitions
 * @param {Record<string, LogicalSourceInput>} sources
 * @param {Iterable<string>} [requested] only these queries are executed when provided
 * @param {{ signal?: { aborted?: boolean }, timeout?: number, maxOperations?: number, budget?: QueryBudget, pagination?: Record<string, { limit: number, continuationToken?: string }> }} [options]
 * @returns {Record<string, LogicalSourceInput>}
 */
export function executeDashboardQueries(definitions, sources, requested, options = {}) {
  const index = dashboardQueryIndex(definitions);
  if (index.size === 0) return {};
  const defects = dashboardQueryDefects(definitions);
  /** @type {QueryBudget | undefined} */
  let budget = options.budget;
  const queryBudget = () => (budget ??= createDashboardQueryBudget(options));
  /** @type {Record<string, LogicalSourceInput>} */
  const derived = {};
  /** @type {Record<string, LogicalSourceInput>} */
  const compiled = Object.create(null);
  const visiting = new Set();
  const names = new Set(requested ?? index.keys());
  const inputs = resolveDashboardQuerySources(definitions, names).includes(SIMULATION_DAYS)
    ? { ...sources, [SIMULATION_DAYS]: simulationDaysSource() }
    : sources;
  for (const name of names) {
    if (!index.has(name)) continue;
    defineLazyProperty(derived, name, () => {
      compileDashboardQuery(name, index, inputs, defects, queryBudget(), compiled, visiting);
      const page = options.pagination?.[name];
      if (!page) return compiled[name];
      const required = new Set(resolveDashboardQuerySources(definitions, [name]));
      return paginateDashboardSources(
        { [name]: compiled[name] },
        { [name]: page },
        continuationRevision(definitions, Object.fromEntries(
          Object.entries(sources)
            .filter(([sourceName]) => required.has(sourceName))
            .map(([sourceName, source]) => [sourceName, source.metadata])
        ))
      )[name];
    });
  }
  return derived;
}

/**
 * Compiles one consumed query and its dependencies into a shared working set
 * so dependencies are materialized at most once per batch.
 *
 * @param {string} name
 * @param {Map<string, DashboardQuery>} index
 * @param {Record<string, LogicalSourceInput>} sources
 * @param {Map<string, string>} defects
 * @param {QueryBudget} budget
 * @param {Record<string, LogicalSourceInput>} compiled
 * @param {Set<string>} visiting
 */
function compileDashboardQuery(name, index, sources, defects, budget, compiled, visiting) {
  /** @param {string} queryName */
  const compile = (queryName) => {
    if (Object.hasOwn(compiled, queryName) || visiting.has(queryName)) return;
    const definition = index.get(queryName);
    if (!definition) return;
    visiting.add(queryName);
    for (const input of queryInputNames(definition)) {
      const precomputed = sources[input];
      if (index.has(input)
          && (!precomputed || (precomputed.metadata?.availability === 'unavailable'
            && precomputed.metadata?.['aggregate-version'] !== 1))) {
        compile(input);
      }
    }
    visiting.delete(queryName);
    budget.checkpoint();
    compiled[queryName] = executeDashboardQuery(
      definition,
      { ...sources, ...compiled },
      defects.get(queryName),
      budget
    );
  };
  compile(name);
  return compiled;
}

/**
 * @param {DashboardQuery} definition
 * @param {Record<string, LogicalSourceInput>} sources
 * @param {string} [defect] a rejected query pattern detected before execution
 * @param {QueryBudget} [budget] shared cancellation, deadline, and operation budget
 * @returns {LogicalSourceInput}
 */
export function executeDashboardQuery(definition, sources, defect, budget) {
  let queryBudget = budget;
  const consume = lazyValue(() => materializeDashboardQuery(
    definition,
    sources,
    defect,
    queryBudget ??= createDashboardQueryBudget()
  ));
  return /** @type {LogicalSourceInput} */ ({
    source: definition.name,
    get rows() {
      return consume().rows;
    },
    get metadata() {
      return consume().metadata;
    }
  });
}

/**
 * @param {DashboardQuery} definition
 * @param {Record<string, LogicalSourceInput>} sources
 * @param {string | undefined} defect
 * @param {QueryBudget} budget
 * @returns {LogicalSourceInput}
 */
function materializeDashboardQuery(definition, sources, defect, budget) {
  const inputs = queryInputNames(definition).map((name) => ({ name, source: sources[name] }));
  const preserveJoinedAggregate = Boolean(definition.joins?.length) && !definition.union?.length;
  const startedAt = queryTimestamp();
  const startingOperations = budget.operations;
  let status = 'available';
  let outputRows = 0;
  let executing = false;
  /** @type {string | undefined} */
  let failure;
  try {
    const rejected = defect ?? queryStructuralDefect(definition);
    if (rejected) {
      status = 'unavailable';
      failure = rejected;
      return unavailableResult(definition, composedMetadata(definition.name, inputs, 0, new Set(), preserveJoinedAggregate), rejected);
    }
    // Union branches are additive: an unavailable branch must not hide the
    // rows produced by the remaining branches. This keeps aggregate views,
    // such as indexing table counts, usable while one optional collection is
    // unavailable.
    const requiredInputs = new Set([
      definition.from,
      ...(definition.joins ?? []).filter((join) => join.type !== 'left'
        // Daily projections cannot turn unavailable count evidence into left-join zeros.
        || sources[join.source]?.metadata?.['aggregate-version'] === 1).map((join) => join.source)
    ]);
    const optionalInputs = new Set((definition.joins ?? [])
      .filter((join) => join.type === 'left' && !requiredInputs.has(join.source))
      .map((join) => join.source));
    for (const source of definition.union ?? []) optionalInputs.add(source);
    const unavailable = inputs.find((input) => (
      requiredInputs.has(input.name)
      && (!input.source || !Array.isArray(input.source.rows) || input.source.metadata?.availability === 'unavailable')
    ));
    if (unavailable) {
      status = 'unavailable';
      failure = `input source "${unavailable.name}" is unavailable`;
      const diagnostic = unavailable.source?.metadata?.['query-diagnostic'];
      if (typeof diagnostic === 'string' && diagnostic.length > 0) {
        failure += `; upstream diagnostic: ${diagnostic.slice(0, 2048)}`;
      }
      const upstream = unavailable.source?.metadata?.['query-error'];
      const rootSource = isPlainObject(upstream)
        && upstream.code === 'input-unavailable'
        && typeof upstream.source === 'string'
        && upstream.source.length <= 256
        && /^[a-z][a-z0-9]*(?:-[a-z0-9]+)*(?::[a-z][a-z0-9]*(?:-[a-z0-9]+)*)*$/.test(upstream.source)
        ? upstream.source : unavailable.name;
      return unavailableResult(definition, composedMetadata(definition.name, inputs, 0, new Set(), preserveJoinedAggregate), failure, rootSource);
    }

    const effectiveSources = { ...sources };
    for (const name of optionalInputs) {
      const source = effectiveSources[name];
      if (!source || !Array.isArray(source.rows) || source.metadata?.availability === 'unavailable') {
        effectiveSources[name] = { ...source, source: source?.source ?? name, rows: [] };
      }
    }

    executing = true;
    const rows = runDashboardQuery(definition, effectiveSources, budget);
    outputRows = rows.length;
    return {
      source: definition.name,
      rows,
      metadata: composedMetadata(definition.name, inputs, rows.length, optionalInputs, preserveJoinedAggregate)
    };
  } catch (error) {
    failure = error instanceof Error ? error.message : String(error);
    if (error instanceof DashboardQueryCancelledError) {
      status = error.kind;
      throw error;
    }
    if (!executing) {
      status = 'failed';
      throw error;
    }
    status = 'unavailable';
    return unavailableResult(
      definition,
      composedMetadata(definition.name, inputs, 0, new Set(), preserveJoinedAggregate),
      failure
    );
  } finally {
    debugQuery('query', {
      query: definition.name,
      durationMs: elapsedQueryMilliseconds(startedAt),
      inputRows: Object.fromEntries(inputs.map(({ name, source }) => [
        name,
        knownSourceRowCount(source)
      ])),
      outputRows,
      operations: budget.operations - startingOperations,
      status,
      ...(failure ? { failure } : {})
    });
  }
}

/**
 * Defines an enumerable property whose value is computed at most once, when read.
 *
 * @template T
 * @param {Record<string, T>} target
 * @param {string} name
 * @param {() => T} consume
 */
function defineLazyProperty(target, name, consume) {
  const value = lazyValue(consume);
  Object.defineProperty(target, name, {
    enumerable: true,
    get: value
  });
}

/**
 * @template T
 * @param {() => T} consume
 * @returns {() => T}
 */
function lazyValue(consume) {
  let state = 'pending';
  /** @type {T | undefined} */
  let value;
  /** @type {unknown} */
  let failure;
  return () => {
    if (state === 'pending') {
      try {
        value = consume();
        state = 'resolved';
      } catch (error) {
        failure = error;
        state = 'rejected';
      }
    }
    if (state === 'rejected') throw failure;
    return /** @type {T} */ (value);
  };
}

/**
 * @param {DashboardQuery} definition
 * @param {Record<string, LogicalSourceInput>} sources
 * @param {QueryBudget} budget
 * @returns {Row[]}
 */
function runDashboardQuery(definition, sources, budget) {
  const sourceNames = [definition.from, ...(definition.union ?? [])];
  const operators = compileRowOperators(definition);
  const materializesRows = Boolean(definition.joins?.length)
    || operators.some((operator) => operator.op === 'compute');
  const input = sourceNames.length === 1
    ? /** @type {Row[]} */ (sources[definition.from].rows)
    : sourceNames.flatMap((sourceName) => /** @type {Row[]} */ (sources[sourceName].rows));
  enforceLimit(input.length, 'max-input-rows', sourceNames.join(', '));
  let rows = timeQueryStage(definition.name, 'from', input.length, budget, () => {
    // Joins and computed fields allocate their output rows; copying every
    // input first only adds a redundant full-row pass, including per scan batch.
    if (materializesRows) {
      if (sourceNames.length > 1) budget.spend(input.length);
      else budget.checkpoint();
      return input;
    }
    budget.spend(input.length);
    return input.map((row) => ({ ...row }));
  });
  for (const join of definition.joins ?? []) {
    const joinedRows = /** @type {Row[]} */ (sources[join.source].rows);
    rows = timeQueryStage(definition.name, `join:${join.source}`, rows.length, budget, () => (
      applyJoin(rows, join, joinedRows, join.source, budget)
    ), {
      joinedRows: joinedRows.length
    });
  }
  for (let index = 0; index < operators.length; index++) {
    const operator = operators[index];
    const next = operators[index + 1];
    if (operator.op === 'compute' && next?.op === 'select') {
      rows = timeQueryStage(definition.name, 'compute-select', rows.length, budget, () => {
        budget.spend(rows.length);
        return computeRows(rows, operator, next);
      });
      index++;
      continue;
    }
    rows = timeQueryStage(
      definition.name,
      queryOperatorStage(operator),
      rows.length,
      budget,
      () => {
        budget.spend(rows.length * queryOperatorCost(operator));
        return tidy(rows, [operator]);
      }
    );
  }
  enforceLimit(rows.length, 'max-output-rows', definition.name);
  return rows;
}

/** @param {import('../../data-operations.js').DataOperator} operator */
function queryOperatorCost(operator) {
  if (operator.op === 'summarize') {
    return 1 + operator.values.reduce(
      (cost, value) => value.filter
        ? cost + 1 + value.filter.predicates.reduce(
            (predicateCost, predicate) => predicateCost + 1 + (predicate.in?.length ?? 0),
            0
          )
        : cost,
      0
    );
  }
  if (operator.op === 'window') return operator.values.reduce((cost, entry) => cost + (entry.operation === 'rolling' ? entry.frame ?? 1 : 1) + 32, 0);
  if (operator.op !== 'predict') return 1;
  return operator.values.reduce((cost, prediction) => {
    const predictors = Array.isArray(prediction.on) ? prediction.on.length : 1;
    const terms = prediction.method === 'quad'
      ? 3
      : prediction.method === 'poly' ? (prediction.order ?? 3) + 1 : predictors + 1;
    return cost + terms * terms;
  }, 0);
}

/**
 * @template T
 * @param {string} queryName
 * @param {string} stage
 * @param {number} inputRows
 * @param {QueryBudget} budget
 * @param {() => T} operation
 * @param {Record<string, unknown>} [details]
 * @returns {T}
 */
function timeQueryStage(queryName, stage, inputRows, budget, operation, details = {}) {
  const startedAt = queryTimestamp();
  const startingOperations = budget.operations;
  let outputRows = null;
  let status = 'complete';
  try {
    const result = operation();
    outputRows = Array.isArray(result) ? result.length : null;
    return result;
  } catch (error) {
    status = error instanceof DashboardQueryCancelledError ? error.kind : 'failed';
    throw error;
  } finally {
    debugQuery('stage', {
      query: queryName,
      stage,
      durationMs: elapsedQueryMilliseconds(startedAt),
      inputRows,
      outputRows,
      operations: budget.operations - startingOperations,
      status,
      ...details
    });
  }
}

function queryTimestamp() {
  return globalThis.performance?.now() ?? Date.now();
}

/** @param {number} startedAt */
function elapsedQueryMilliseconds(startedAt) {
  return Math.round((queryTimestamp() - startedAt) * 100) / 100;
}

/** @param {LogicalSourceInput | undefined} source */
function knownSourceRowCount(source) {
  if (!source) return null;
  const rows = Object.getOwnPropertyDescriptor(source, 'rows');
  return rows && 'value' in rows && Array.isArray(rows.value) ? rows.value.length : null;
}

/** @param {import('../../data-operations.js').DataOperator} operator */
function queryOperatorStage(operator) {
  if (operator.op === 'summarize') return 'aggregate';
  if (operator.op === 'arrange') return 'order-by';
  if (operator.op === 'slice') return 'limit';
  return operator.op;
}

/**
 * Compiles the post-join clauses into serializable row operators.
 * @param {DashboardQuery} definition
 * @returns {import('../../data-operations.js').DataOperator[]}
 */
export function compileRowOperators(definition) {
  /** @type {import('../../data-operations.js').DataOperator[]} */
  const operators = [];
  if (definition.filter?.predicates?.length || definition.filter?.search?.query) {
    operators.push({ op: 'filter', ...definition.filter });
  }
  if (definition.compute?.length) operators.push({ op: 'compute', values: definition.compute });
  if (definition['temporal-series']) operators.push({ op: 'temporal-series', ...definition['temporal-series'] });
  if (definition.aggregate) {
    operators.push({ op: 'summarize', by: definition.aggregate.by ?? [], values: definition.aggregate.values });
  }
  if (definition.predict?.length) operators.push({ op: 'predict', values: definition.predict });
  if (definition.window?.length) operators.push({ op: 'window', values: definition.window });
  if (definition.select?.length) operators.push({ op: 'select', fields: definition.select });
  if (definition['order-by']?.length) operators.push({ op: 'arrange', by: definition['order-by'] });
  if (typeof definition.limit === 'number') operators.push({ op: 'slice', limit: definition.limit });
  if (definition.facet) operators.push({ op: 'facet', ...definition.facet });
  return operators;
}

/**
 * Applies one constrained equality join. The joined source must contain at
 * most one row per join key unless inner-many explicitly requests expansion.
 *
 * @param {Row[]} rows
 * @param {NonNullable<DashboardQuery['joins']>[number]} join
 * @param {Row[]} joinedRows
 * @param {string} sourceName
 * @param {QueryBudget} budget
 * @returns {Row[]}
 */
function applyJoin(rows, join, joinedRows, sourceName, budget) {
  enforceLimit(joinedRows.length, 'max-input-rows', sourceName);
  budget.spend(joinedRows.length);
  /** @type {Map<string, Row[]>} */
  const byKey = new Map();
  for (const row of joinedRows) {
    const key = joinKey(row, join.on.map((pair) => pair.right));
    if (key === null) continue;
    if (byKey.has(key) && join.type !== 'inner-many') {
      throw new Error(`joined source "${sourceName}" contains more than one row per join key`);
    }
    const matches = byKey.get(key) ?? [];
    matches.push(row);
    byKey.set(key, matches);
  }
  const type = join.type ?? 'inner';
  /** @type {Row[]} */
  const joined = [];
  for (const row of rows) {
    if (joined.length % CANCELLATION_CHECK_INTERVAL === 0) budget.checkpoint();
    const key = joinKey(row, join.on.map((pair) => pair.left));
    const matches = key === null ? undefined : byKey.get(key);
    if (!matches && type !== 'left') continue;
    for (const match of matches ?? [undefined]) {
      enforceLimit(joined.length + 1, 'max-join-rows', sourceName);
      budget.spend(1);
      joined.push({
        ...row,
        ...Object.fromEntries(join.fields.map((field) => [field.as, match?.[field.field] ?? null]))
      });
    }
  }
  enforceLimit(joined.length, 'max-join-rows', sourceName);
  return joined;
}

/**
 * Null, missing, and blank key parts never match, so a `left` join keeps the
 * row with null joined fields and an `inner` join drops it.
 * @param {Row} row
 * @param {string[]} fields
 * @returns {string | null}
 */
function joinKey(row, fields) {
  /** @type {string[]} */
  const parts = [];
  for (const field of fields) {
    const value = row[field];
    if (value === null || value === undefined || typeof value === 'object') return null;
    const text = String(value).trim();
    if (text === '') return null;
    parts.push(text);
  }
  return JSON.stringify(parts);
}

/**
 * @param {number} count
 * @param {keyof typeof DASHBOARD_QUERY_LIMITS} limit
 * @param {string} subject
 */
function enforceLimit(count, limit, subject) {
  if (count > DASHBOARD_QUERY_LIMITS[limit]) {
    throw new Error(`"${subject}" exceeds the ${limit} limit of ${DASHBOARD_QUERY_LIMITS[limit]} rows`);
  }
}

/**
 * Composes provenance, freshness, completeness, and availability across every
 * input source. Ordinary unavailable left joins degrade completeness; daily
 * count joins require their projection and retain its provenance on enrichment.
 * @param {string} name
 * @param {Array<{ name: string, source?: LogicalSourceInput }>} inputs
 * @param {number} rowCount
 * @param {Set<string>} [optionalInputs]
 * @param {boolean} [preserveJoinedAggregate]
 * @returns {SourceMetadata}
 */
function composedMetadata(name, inputs, rowCount, optionalInputs = new Set(), preserveJoinedAggregate = false) {
  const metadata = inputs.map((input) => input.source?.metadata).filter(Boolean);
  const statisticalFrame = metadata.find((value) => value?.['statistical-frame'])?.['statistical-frame'];
  /** @param {SourceMetadata | undefined} value */
  const aggregateFields = (value) => Object.fromEntries(Object.entries(value ?? {}).filter(([field]) =>
    field.startsWith('aggregate-') || ['excluded-runs', 'conflicted-runs', 'query-diagnostic'].includes(field)));
  const aggregateMetadata = preserveJoinedAggregate
    ? metadata.filter((value) => value?.['aggregate-version'] !== undefined) : metadata;
  const candidateProvenance = aggregateFields(aggregateMetadata[0]);
  const aggregateProvenance = metadata.length === inputs.length && candidateProvenance['aggregate-version'] === 1
    && aggregateMetadata.every((value) => {
      const fields = aggregateFields(value);
      return Object.keys(fields).length === Object.keys(candidateProvenance).length
        && Object.entries(candidateProvenance).every(([field, entry]) => fields[field] === entry);
    })
    ? candidateProvenance
    : {};
  /** @param {'as-of'|'retrieved-at'} field */
  const oldest = (field) => metadata
    .map((value) => value?.[field])
    .filter((value) => typeof value === 'string' && Number.isFinite(Date.parse(value)))
    .sort((left, right) => Date.parse(/** @type {string} */ (left)) - Date.parse(/** @type {string} */ (right)))[0];
  const complete = metadata.length === inputs.length && metadata.length > 0;
  const unavailableOptional = inputs.some((input) => optionalInputs.has(input.name) && (
    !input.source || !Array.isArray(input.source.rows) || input.source.metadata?.availability === 'unavailable'
  ));
  return {
    ...(statisticalFrame ? { 'statistical-frame': statisticalFrame, 'sample-frame': statisticalFrame } : {}),
    ...aggregateProvenance,
    'source-id': `${name}-query`,
    'source-kind': 'derived',
    'as-of': /** @type {string} */ (oldest('as-of')) ?? new Date(0).toISOString(),
    'retrieved-at': /** @type {string} */ (oldest('retrieved-at')) ?? new Date(0).toISOString(),
    completeness: unavailableOptional
      ? 'partial'
      : !complete || metadata.some((value) => value?.completeness !== 'complete')
      ? (metadata.some((value) => value?.completeness === 'partial') ? 'partial' : 'unknown')
      : 'complete',
    freshness: metadata.some((value) => value?.freshness === 'stale')
      ? 'stale'
      : complete && metadata.every((value) => value?.freshness === 'fresh')
        ? 'fresh'
        : 'unknown',
    availability: inputs.some((input) => !optionalInputs.has(input.name) && (
      !input.source || input.source.metadata?.availability === 'unavailable'
    ))
      ? 'unavailable'
      : rowCount > 0 ? 'available' : 'empty',
    'query-name': name
  };
}

/**
 * Query diagnostics identify the dashboard path and the failing clause without
 * exposing source payloads or secrets.
 * @param {DashboardQuery} definition
 * @param {SourceMetadata} metadata
 * @param {string} reason
 * @param {string} [unavailableInput]
 * @returns {LogicalSourceInput}
 */
export function unavailableResult(definition, metadata, reason, unavailableInput) {
  return {
    source: definition.name,
    rows: [],
    metadata: {
      ...metadata,
      completeness: 'unknown',
      freshness: 'unknown',
      availability: 'unavailable',
      'query-diagnostic': `$.dashboard.queries[${definition.name}]: ${reason}.`,
      ...(unavailableInput ? { 'query-error': { code: 'input-unavailable', source: unavailableInput } } : {})
    }
  };
}

/** @param {unknown} value @returns {value is Record<string, unknown>} */
function isPlainObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** @param {unknown} value */
function isAggregateFilterLiteral(value) {
  return ['string', 'number', 'boolean'].includes(typeof value)
    && (typeof value !== 'number' || Number.isFinite(value));
}
