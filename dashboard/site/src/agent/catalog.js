/**
 * Protocol-independent agent catalog over dashboard pages and queries.
 *
 * Dashboard definitions are the semantic contract for every agent transport.
 * The browser WebMCP adapter, the `cao` CLI, and the read-only MCP server all
 * read this catalog so none of them maintains a second catalog of pages or
 * queries. This module is pure: it reads a dashboard document and returns
 * descriptors without touching the DOM, the network, or the database.
 */

import databaseQueries from '../data/queries/database.json' with { type: 'json' };
import { queryInputNames } from '../data/queries/declarative.js';
import { createDebug } from '../debug.js';

const debugCatalog = createDebug('catalog');

/**
 * Canonical record collections that the database layer projects from run
 * records rather than from a named database query.
 */
const RUN_RECORD_SOURCES = ['audits', 'domains', 'issues', 'tools'];

/**
 * Source names that the local PostgreSQL projection can materialize. A database
 * query is locally executable when it reads canonical stores; database queries
 * that declare no stores project a browser-supplied static source instead.
 */
const LOCAL_SOURCES = new Set([
  ...RUN_RECORD_SOURCES,
  'simulation-days',
  ...(Array.isArray(databaseQueries) ? databaseQueries : [])
    .filter((query) => isPlainObject(query)
      && typeof query.name === 'string'
      && Array.isArray(query.stores)
      && query.stores.length > 0)
    .map((query) => String(query.name))
]);

/**
 * @typedef {{ id: string, title: string, description: string, intent?: string, subject?: string, objective?: string, acceptance?: string, experimental: boolean,
 *   parameters: Array<{ name: string, required: boolean, description: string, schema: Record<string, unknown> }>,
 *   queries: string[] }} AgentPageEntry
 */

/**
 * @typedef {{ id: string, subject: string, objective?: string, acceptance?: string, description: string,
 *   parameters: Array<{ name: string, field?: string, type?: string, required?: boolean, schema?: Record<string, unknown> }>,
 *   sources: string[], ['used-by-pages']: string[],
 *   execution: { local: boolean, backend?: string, requirements?: string[], reason?: string } }} AgentQueryEntry
 */

/**
 * @param {unknown} value
 * @returns {value is Record<string, any>}
 */
function isPlainObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/**
 * Dashboard documents are loaded once and read many times, so derived catalog
 * views are memoized per document instead of being recomputed for every agent
 * request. The memo is discarded whenever the document replaces one of the
 * collections the catalog reads, so a refreshed definition is never served from
 * a stale catalog.
 * @type {WeakMap<object, { collections: unknown[], entries: Map<string, any> }>}
 */
const caches = new WeakMap();

/** @param {unknown} document */
function catalogCollections(document) {
  const dashboard = dashboardOf(document);
  return [dashboard.pages, dashboard.queries, dashboard.views, dashboard.navigation];
}

/**
 * @template T
 * @param {unknown} document
 * @param {string} key
 * @param {() => T} compute
 * @returns {T}
 */
function memoize(document, key, compute) {
  if (!isPlainObject(document)) return compute();
  const collections = catalogCollections(document);
  let cache = caches.get(document);
  if (!cache || cache.collections.some((value, index) => value !== collections[index])) {
    const stale = Boolean(cache);
    cache = { collections, entries: new Map() };
    caches.set(document, cache);
    debugCatalog({ event: 'cache-rebuild', reason: stale ? 'stale' : 'new' });
  }
  if (!cache.entries.has(key)) cache.entries.set(key, compute());
  return cache.entries.get(key);
}

/**
 * @param {unknown} value
 * @returns {string}
 */
function text(value) {
  return typeof value === 'string' ? value.trim() : '';
}

/** @param {unknown} document */
function dashboardOf(document) {
  return isPlainObject(document) && isPlainObject(document.dashboard) ? document.dashboard : {};
}

/** @param {unknown} document */
function queryDefinitions(document) {
  const queries = dashboardOf(document).queries;
  return (Array.isArray(queries) ? queries : [])
    .filter((query) => isPlainObject(query) && text(query.name));
}

/** @param {unknown} document */
function queryIndex(document) {
  return new Map(queryDefinitions(document).map((query) => [text(query.name), query]));
}

/**
 * Returns the page identifiers reachable from the declared navigation.
 * @param {unknown} navigation
 * @returns {Set<string>}
 */
export function navigationPageIds(navigation) {
  /** @type {Set<string>} */
  const ids = new Set();
  if (!Array.isArray(navigation)) return ids;
  for (const group of navigation) {
    if (!isPlainObject(group) || !Array.isArray(group.pages)) continue;
    for (const pageId of group.pages) {
      if (text(pageId)) ids.add(text(pageId));
    }
  }
  return ids;
}

/**
 * Reports whether a page is exposed to agents.
 *
 * A page is agent facing when an operator can reach it deliberately: either it
 * appears in the declared navigation, or it is a detail page addressed by one
 * route parameter. Pages that only exist as navigation targets of another page
 * are excluded so the catalog stays a bounded capability catalog. Experimental
 * pages remain included because `experimental` is informational page metadata
 * that changes presentation only.
 *
 * @param {Record<string, any>} page
 * @param {Set<string>} [navigationPages]
 */
export function isAgentFacingPage(page, navigationPages = new Set()) {
  if (!isPlainObject(page)) return false;
  const pageId = text(page.id);
  if (!pageId || !text(page.title)) return false;
  if (navigationPages.has(pageId)) return true;
  return Boolean(text(page.route?.['hash-query-parameter']));
}

/**
 * Returns the agent-facing page definitions of one dashboard document in
 * declaration order.
 * @param {unknown} document
 * @returns {Record<string, any>[]}
 */
export function agentFacingPages(document) {
  return memoize(document, 'agent-facing-pages', () => computeAgentFacingPages(document));
}

/**
 * @param {unknown} document
 * @returns {Record<string, any>[]}
 */
function computeAgentFacingPages(document) {
  const dashboard = dashboardOf(document);
  const pages = Array.isArray(dashboard.pages) ? dashboard.pages : [];
  const navigationPages = navigationPageIds(dashboard.navigation);
  /** @type {Map<string, Record<string, any>>} */
  const unique = new Map();
  for (const page of pages) {
    if (!isAgentFacingPage(page, navigationPages)) continue;
    const pageId = text(page.id);
    if (unique.has(pageId)) continue;
    unique.set(pageId, page);
  }
  return [...unique.values()];
}

/**
 * Reports whether a value is an exact multiple of a positive step.
 * @param {number} value
 * @param {number} step
 */
function isMultipleOf(value, step) {
  const quotient = value / step;
  return Math.abs(quotient - Math.round(quotient)) < 1e-9;
}

/**
 * Maps one declared form field to a JSON Schema property.
 * @param {Record<string, any>} field
 * @returns {Record<string, unknown> | null}
 */
function formFieldSchema(field) {
  const description = text(field.label) || text(field.id);
  switch (text(field.control)) {
    case 'checkbox':
      return {
        type: 'boolean',
        description,
        ...(typeof field.default === 'boolean' ? { default: field.default } : {})
      };
    case 'slider': {
      /** @type {Record<string, unknown>} */
      const schema = { type: 'number', description };
      if (typeof field.min === 'number' && Number.isFinite(field.min)) schema.minimum = field.min;
      if (typeof field.max === 'number' && Number.isFinite(field.max)) schema.maximum = field.max;
      // Slider positions are offsets from `min`, so `multipleOf` (an offset from
      // zero) is only equivalent when `min` is itself a multiple of the step.
      if (typeof field.step === 'number' && Number.isFinite(field.step) && field.step > 0
        && (typeof schema.minimum !== 'number' || isMultipleOf(schema.minimum, field.step))) {
        schema.multipleOf = field.step;
      }
      if (typeof field.default === 'number' && Number.isFinite(field.default)) schema.default = field.default;
      return schema;
    }
    case 'radio':
    case 'select': {
      const options = Array.isArray(field.options) ? field.options : [];
      const values = options
        .map((option) => (isPlainObject(option) ? option.value : option))
        .filter((value) => typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean');
      if (values.length === 0) return null;
      if (values.some((value) => typeof value !== typeof values[0])) return null;
      const type = typeof values[0] === 'number' ? 'number' : typeof values[0] === 'boolean' ? 'boolean' : 'string';
      return {
        type,
        description,
        enum: values,
        ...(values.includes(field.default) ? { default: field.default } : {})
      };
    }
    case 'text':
      return {
        type: 'string',
        description,
        ...(typeof field.default === 'string' ? { default: field.default } : {})
      };
    default:
      return null;
  }
}

/**
 * Returns the declared parameters of one page: its route parameter first, then
 * the form fields that change what the page reports.
 * @param {Record<string, any>} page
 * @returns {Array<{ name: string, required: boolean, description: string, schema: Record<string, unknown> }>}
 */
export function pageParameters(page) {
  if (!isPlainObject(page)) return [];
  /** @type {Array<{ name: string, required: boolean, description: string, schema: Record<string, unknown> }>} */
  const parameters = [];
  /** @type {Set<string>} */
  const seen = new Set();
  const routeParameter = text(page.route?.['hash-query-parameter']);
  if (routeParameter) {
    const description = `The ${routeParameter} this page reports on.`;
    parameters.push({
      name: routeParameter,
      required: true,
      description,
      schema: { type: 'string', description }
    });
    seen.add(routeParameter);
  }
  const fields = Array.isArray(page.form?.fields) ? page.form.fields : [];
  for (const field of fields) {
    if (!isPlainObject(field)) continue;
    const fieldId = text(field.id);
    if (!fieldId || seen.has(fieldId)) continue;
    const schema = formFieldSchema(field);
    if (!schema) continue;
    parameters.push({
      name: fieldId,
      required: false,
      description: text(schema.description) || fieldId,
      schema
    });
    seen.add(fieldId);
  }
  return parameters;
}

/**
 * Returns the query names one view binds, including drill-down queries.
 * @param {Record<string, any>} view
 * @returns {string[]}
 */
function viewQueryNames(view) {
  /** @type {string[]} */
  const names = [];
  if (isPlainObject(view.data)) {
    if (text(view.data.source)) names.push(text(view.data.source));
    if (Array.isArray(view.data.sources)) {
      for (const name of view.data.sources) if (text(name)) names.push(text(name));
    }
  }
  if (isPlainObject(view.list) && isPlainObject(view.list.drill) && text(view.list.drill.query)) {
    names.push(text(view.list.drill.query));
  }
  return names;
}

/** @param {Record<string, any>} page */
function pageDefinition(page) {
  return page.kind === 'built-in' && isPlainObject(page.definition) ? page.definition : page;
}

/**
 * Returns the view definitions of one page. Pages may declare views inline or
 * reference a shared view by identifier, so both forms are resolved here.
 * @param {unknown} document
 * @param {Record<string, any>} page
 */
function pageViews(document, page) {
  const shared = memoize(document, 'shared-views', () => sharedViewIndex(document));
  const definition = pageDefinition(page);
  return (Array.isArray(definition.views) ? definition.views : [])
    .map((view) => (text(view) ? shared.get(text(view)) : view))
    .filter(isPlainObject);
}

/**
 * @param {unknown} document
 * @returns {Map<string, Record<string, any>>}
 */
function sharedViewIndex(document) {
  const declared = /** @type {Record<string, any>[]} */ (
    Array.isArray(dashboardOf(document).views) ? dashboardOf(document).views : []
  );
  return new Map(declared
    .filter((view) => isPlainObject(view) && text(view.id))
    .map((view) => [text(view.id), view]));
}

/**
 * Returns the named queries one page reads, in deterministic order.
 * @param {unknown} document
 * @param {string} pageId
 * @returns {string[]}
 */
export function queriesForPage(document, pageId) {
  return memoize(document, `page-queries:${text(pageId)}`, () => computeQueriesForPage(document, pageId));
}

/**
 * @param {unknown} document
 * @param {string} pageId
 * @returns {string[]}
 */
function computeQueriesForPage(document, pageId) {
  const page = agentFacingPages(document).find((candidate) => text(candidate.id) === text(pageId));
  if (!page) return [];
  const known = queryIndex(document);
  /** @type {Set<string>} */
  const names = new Set();
  for (const view of pageViews(document, page)) {
    for (const name of viewQueryNames(view)) {
      if (known.has(name)) names.add(name);
    }
  }
  const definition = pageDefinition(page);
  for (const section of Array.isArray(definition.sections) ? definition.sections : []) {
    if (!isPlainObject(section)) continue;
    for (const name of [
      section['count-source'],
      ...(Array.isArray(section['count-sources']) ? section['count-sources'] : [])
    ]) {
      if (text(name) && known.has(text(name))) names.add(text(name));
    }
  }
  return [...names].toSorted();
}

/**
 * Returns query operands and page-bound row filters available to agents.
 * @param {unknown} document
 * @param {string} queryId
 * @returns {Array<{ name: string, field?: string, type?: string, required?: boolean, schema?: Record<string, unknown> }>}
 */
export function queryParameters(document, queryId) {
  return memoize(document, `query-parameters:${text(queryId)}`, () => computeQueryParameters(document, queryId));
}

/**
 * @param {unknown} document
 * @param {string} queryId
 * @returns {Array<{ name: string, field?: string, type?: string, required?: boolean, schema?: Record<string, unknown> }>}
 */
function computeQueryParameters(document, queryId) {
  const id = text(queryId);
  /** @type {Map<string, { name: string, field?: string, type?: string, required?: boolean, schema?: Record<string, unknown> }>} */
  const parameters = new Map();
  const index = queryIndex(document);
  const dependencies = new Set();
  const pending = [id];
  while (pending.length) {
    const name = pending.pop();
    if (!name || dependencies.has(name)) continue;
    dependencies.add(name);
    const definition = index.get(name);
    if (definition) pending.push(...queryInputNames(definition));
  }
  for (const page of agentFacingPages(document)) {
    const routeParameter = text(page.route?.['hash-query-parameter']);
    for (const view of pageViews(document, page)) {
      if (!viewQueryNames(view).includes(id)) continue;
      const routeField = isPlainObject(view.data) ? text(view.data['route-field']) : '';
      if (routeField && routeParameter) {
        parameters.set(routeParameter, { name: routeParameter, field: routeField });
      }
      const args = isPlainObject(view.data) && Array.isArray(view.data.arguments) ? view.data.arguments : [];
      for (const argument of args) {
        if (!isPlainObject(argument) || !text(argument.name) || !text(argument.field)) continue;
        parameters.set(text(argument.name), { name: text(argument.name), field: text(argument.field) });
      }
    }
  }
  for (const name of dependencies) {
    const definition = index.get(name);
    for (const parameter of Array.isArray(definition?.parameters) ? definition.parameters : []) {
      if (!isPlainObject(parameter) || !text(parameter.name) || !['number', 'string', 'boolean'].includes(parameter.type)) continue;
      const formField = agentFacingPages(document).flatMap((page) => (
        pageViews(document, page).some((view) => viewQueryNames(view).some((source) => (
          source === id || queryDependsOn(index, source, id)
        )))
          ? pageParameters(page).filter((entry) => entry.name === parameter.name)
          : []
      ))[0];
      parameters.set(parameter.name, {
        name: parameter.name,
        type: parameter.type,
        required: true,
        ...(formField ? { schema: formField.schema } : {})
      });
    }
  }
  return [...parameters.values()].toSorted((left, right) => left.name.localeCompare(right.name));
}

/** @param {Map<string, Record<string, any>>} index @param {string} source @param {string} target */
function queryDependsOn(index, source, target) {
  const pending = [source];
  const seen = new Set();
  while (pending.length) {
    const name = pending.pop();
    if (name === target) return true;
    if (!name || seen.has(name)) continue;
    seen.add(name);
    const query = index.get(name);
    if (query) pending.push(...queryInputNames(/** @type {any} */ (query)));
  }
  return false;
}
/**
 * Resolves the transitive source dependencies of one named query and reports
 * whether the local PostgreSQL projection can materialize them.
 * @param {unknown} document
 * @param {string} queryId
 * @returns {{ local: boolean, backend?: string, requirements: string[], missing: string[], reason?: string }}
 */
export function queryExecutionRequirements(document, queryId) {
  return memoize(document, `query-execution:${text(queryId)}`, () => computeQueryExecutionRequirements(document, queryId));
}

/**
 * @param {unknown} document
 * @param {string} queryId
 * @returns {{ local: boolean, backend?: string, requirements: string[], missing: string[], reason?: string }}
 */
function computeQueryExecutionRequirements(document, queryId) {
  const index = queryIndex(document);
  const id = text(queryId);
  if (!index.has(id)) {
    return {
      local: false,
      requirements: [],
      missing: [],
      reason: `Unknown dashboard query: ${id}`
    };
  }
  /** @type {Set<string>} */
  const visited = new Set();
  /** @type {Set<string>} */
  const requirements = new Set();
  /** @type {Set<string>} */
  const missing = new Set();
  const pending = [id];
  while (pending.length > 0) {
    const name = /** @type {string} */ (pending.shift());
    if (visited.has(name)) continue;
    visited.add(name);
    const definition = index.get(name);
    if (!definition) {
      if (LOCAL_SOURCES.has(name)) requirements.add(name);
      else missing.add(name);
      continue;
    }
    for (const input of queryInputNames(definition)) {
      if (text(input)) pending.push(text(input));
    }
  }
  const sorted = [...requirements].toSorted();
  const unavailable = [...missing].toSorted();
  if (unavailable.length > 0) {
    debugCatalog({ event: 'query-execution', queryId: id, local: false, missingCount: unavailable.length });
    return {
      local: false,
      requirements: sorted,
      missing: unavailable,
      reason: `Requires ${unavailable.join(', ')}, which the local PostgreSQL projection does not provide.`
    };
  }
  return { local: true, backend: 'postgres', requirements: sorted, missing: [] };
}

/**
 * Describes every agent-facing page of one dashboard document.
 * @param {unknown} document
 * @returns {AgentPageEntry[]}
 */
export function listPages(document) {
  return memoize(document, 'pages', () => agentFacingPages(document).map((page) => ({
    id: text(page.id),
    title: text(page.title) || text(page['navigation-label']) || text(page.id),
    description: text(page.description) || text(page.intent),
    ...Object.fromEntries(['intent', 'subject', 'objective', 'acceptance']
      .filter((field) => text(page[field]))
      .map((field) => [field, text(page[field])])),
    experimental: page.experimental === true,
    parameters: pageParameters(page),
    queries: queriesForPage(document, text(page.id))
  })));
}

/**
 * Describes one agent-facing page.
 * @param {unknown} document
 * @param {string} pageId
 * @returns {AgentPageEntry | null}
 */
export function describePage(document, pageId) {
  const index = memoize(document, 'page-index', () => new Map(
    listPages(document).map((page) => [page.id, page])
  ));
  return index.get(text(pageId)) ?? null;
}

/**
 * Describes every named dashboard query.
 * @param {unknown} document
 * @returns {AgentQueryEntry[]}
 */
export function listQueries(document) {
  return memoize(document, 'queries', () => computeListQueries(document));
}

/**
 * @param {unknown} document
 * @returns {AgentQueryEntry[]}
 */
function computeListQueries(document) {
  /** @type {Map<string, string[]>} */
  const pagesByQuery = new Map();
  for (const page of agentFacingPages(document)) {
    for (const name of queriesForPage(document, text(page.id))) {
      pagesByQuery.set(name, [...(pagesByQuery.get(name) ?? []), text(page.id)]);
    }
  }
  return queryDefinitions(document).map((query) => {
    const id = text(query.name);
    const execution = queryExecutionRequirements(document, id);
    return {
      id,
      subject: text(query.subject),
      ...(query.objective ? { objective: text(query.objective) } : {}),
      ...(query.acceptance ? { acceptance: text(query.acceptance) } : {}),
      description: text(query.description),
      parameters: queryParameters(document, id),
      sources: queryInputNames(query).filter((name) => text(name)).toSorted(),
      'used-by-pages': (pagesByQuery.get(id) ?? []).toSorted(),
      execution: execution.local
        ? { local: true, backend: 'postgres', requirements: execution.requirements }
        : { local: false, requirements: execution.requirements, reason: String(execution.reason) }
    };
  });
}

/**
 * Describes one named dashboard query.
 * @param {unknown} document
 * @param {string} queryId
 * @returns {AgentQueryEntry | null}
 */
export function describeQuery(document, queryId) {
  const index = memoize(document, 'query-index', () => new Map(
    listQueries(document).map((query) => [query.id, query])
  ));
  const id = text(queryId);
  const found = index.get(id) ?? null;
  if (!found) debugCatalog({ event: 'describe-query', queryId: id, status: 'not-found' });
  return found;
}

/**
 * Returns the whole agent catalog of one dashboard document.
 * @param {unknown} document
 * @returns {{ dashboard: { id: string, title: string }, pages: AgentPageEntry[], queries: AgentQueryEntry[] }}
 */
export function agentCatalog(document) {
  const dashboard = dashboardOf(document);
  return {
    dashboard: { id: text(dashboard.id), title: text(dashboard.title) },
    pages: listPages(document),
    queries: listQueries(document)
  };
}
