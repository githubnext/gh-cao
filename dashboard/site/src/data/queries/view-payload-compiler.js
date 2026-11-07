/**
 * Compiles one page's authored view payload scope into deterministic,
 * worker-executed declarative queries.
 */

import { createDebug } from '../../debug.js';
import { viewBackendAvailable } from '../../view-availability.js';
import { resolveDashboardQuerySources } from './declarative.js';
import { dashboardViewSourceNames as getViewSources, viewFilterControls, viewFilterSourceNames } from '../../view-filter-contract.js';
import { chartFacet } from '../../chart-facet.js';
import { compileChartFacetQuery } from './chart-facet-compiler.js';

const debugViewPayloadCompiler = createDebug('view-payload-compiler');

/**
 * @typedef {{
 *   filters?: Record<string, string[]>,
 *   viewFilters?: import('../../view-filter-contract.js').ViewFilters,
 *   search?: { fields: string[], query: string },
 *   orderBy?: Array<{ field: string, direction?: 'asc'|'desc' }>,
 *   timeWindow?: { start?: string, end?: string },
 *   viewMode?: 'chart'|'table'|'card',
 *   formValues?: Record<string, string|number|boolean>
 * }} GlobalQueryContext
 */

/**
 * @param {string} pageId
 * @param {unknown} view
 * @param {number} viewIndex
 * @param {string} sourceName
 * @param {number} sourceIndex
 * @returns {string}
 */
export function dashboardViewAliasName(pageId, view, viewIndex, sourceName, sourceIndex = 0) {
  const candidate = isPlainObject(view) ? /** @type {Record<string, unknown>} */ (view) : {};
  const viewId = typeof candidate.id === 'string' && candidate.id.trim().length > 0
    ? candidate.id
    : `view-${viewIndex + 1}`;
  const qualifier = sourceIndex > 0 ? `-${sourceIndex + 1}` : '';
  return ['view', pageId, viewId, `${sourceName}${qualifier}`]
    .map(slug)
    .join(':');
}

/**
 * @param {unknown} page
 * @param {string} pageId
 * @param {{ routeParameters?: Record<string, string>, queryContext?: GlobalQueryContext, evaluatedAt?: string, queries?: unknown, views?: unknown, viewId?: string, viewIds?: Iterable<string>, sourceNames?: Iterable<string>, backend?: import('../../view-availability.js').DashboardDataBackend, partial?: boolean }} [options]
 * @returns {{ aliases: string[], queries: Array<Record<string, unknown>>, replacedSources: string[] }}
 */
export function compileDashboardViewPayloadQueries(page, pageId, options = {}) {
  const payload = pagePayload(page, options.views);
  const formValues = {
    ...dashboardFormDefaultValues(payload.form),
    ...(options.queryContext?.formValues ?? {})
  };
  const views = Array.isArray(payload.views) ? payload.views : [];
  const viewMode = payload['query-binding'] === 'page' ? undefined : options.queryContext?.viewMode;
  const selectedViewIds = options.viewIds ? new Set(options.viewIds) : null;
  const activeViews = views.filter((view, index) => {
    if (options.backend && !viewBackendAvailable(view, options.backend)) return false;
    if (selectedViewIds && (!isPlainObject(view) || !selectedViewIds.has(String(view.id ?? `view-${index + 1}`)))) return false;
    if (options.viewId) return isPlainObject(view) && (view.id ?? `view-${index + 1}`) === options.viewId;
    return !viewMode || viewMatchesMode(view, viewMode);
  });
  const requestedSources = options.sourceNames ? new Set(options.sourceNames) : null;
  const relevantSources = activeViews.flatMap((view) => getViewSources(view)
    .filter((source) => !requestedSources || requestedSources.has(source))
    .map((source) => options.partial && isPlainObject(view) && isPlainObject(view.data) && view.data.source === source
      ? typeof view.data['partial-source'] === 'string' ? view.data['partial-source'] : source
      : source));
  const required = new Set(resolveDashboardQuerySources(options.queries, relevantSources));
  const scopedDefinitions = Array.isArray(options.queries)
    ? options.queries.filter((definition) => (
        isPlainObject(definition) && typeof definition.name === 'string' && required.has(definition.name)
      ))
    : options.queries;
  const resolvedQueries = resolveDashboardQueryParameters(scopedDefinitions, formValues);
  const routeParameterName = typeof payload.route?.['hash-query-parameter'] === 'string'
    ? payload.route['hash-query-parameter']
    : '';
  const routeValue = routeParameterName
    ? typeof options.routeParameters?.[routeParameterName] === 'string'
      ? options.routeParameters?.[routeParameterName]
      : ''
    : '';
  /** @type {string[]} */
  const aliases = [];
  /** @type {Array<Record<string, unknown>>} */
  const queries = [];
  const replacedSources = new Set();
  const unaliasedSources = new Set();
  /** @type {Map<string, { replacesSource: boolean, dependencies: Array<Record<string, unknown>>, query: Record<string, unknown> }>} */
  const scopedGraphs = new Map();

  views.forEach((view, viewIndex) => {
    if (options.backend && !viewBackendAvailable(view, options.backend)) return;
    if (selectedViewIds && (!isPlainObject(view) || !selectedViewIds.has(String(view.id ?? `view-${viewIndex + 1}`)))) return;
    if (options.viewId && (!isPlainObject(view)
      || (view.id ?? `view-${viewIndex + 1}`) !== options.viewId)) return;
    if (!options.viewId && viewMode && !viewMatchesMode(view, viewMode)) return;
    const sources = getViewSources(view);
    if (sources.length === 0) return;
    const viewData = isPlainObject(view) && isPlainObject(view.data)
      ? /** @type {Record<string, unknown>} */ (view.data)
      : null;
    const useQueryContext = viewData?.['query-context'] !== false;
    const routeField = viewData && typeof viewData['route-field'] === 'string'
      ? viewData['route-field']
      : '';
    const optionSources = new Set(viewFilterSourceNames(view));

    sources.forEach((sourceName, sourceIndex) => {
      if (requestedSources && !requestedSources.has(sourceName)) return;
      const querySource = options.partial && viewData?.source === sourceName
        ? typeof viewData['partial-source'] === 'string' ? viewData['partial-source'] : sourceName
        : sourceName;
      const isOptions = optionSources.has(sourceName);
      const viewId = isPlainObject(view) && typeof view.id === 'string' ? view.id : `view-${viewIndex + 1}`;
      const selected = options.queryContext?.viewFilters?.[viewId];
      const localPredicates = isOptions ? [] : viewFilterControls(view).flatMap((control) => (
        control.groups.flatMap((group) => {
          const values = selected?.[group.field];
          return values?.length ? [{ field: group.field, in: values }] : [];
        })
      ));
      const predicates = [
        ...compileScopePredicates(viewData?.scope),
        ...compileViewFilterPredicates(viewData?.filters),
        ...compileArgumentPredicates(viewData?.arguments, options.routeParameters),
        ...compileTimePredicates(viewData?.time),
        ...compileGlobalFilterPredicates(useQueryContext ? options.queryContext?.filters : undefined),
        ...compileTimePredicates(useQueryContext ? options.queryContext?.timeWindow : undefined),
        ...compileRoutePredicates(routeField, routeValue),
        ...localPredicates
      ];
      if (usesNativeSource(view, sourceName, predicates, useQueryContext ? options.queryContext : undefined, resolvedQueries)) {
        unaliasedSources.add(sourceName);
        return;
      }
      const alias = dashboardViewAliasName(pageId, view, viewIndex, sourceName, sourceIndex);
      aliases.push(alias);
      const search = useQueryContext && !isOptions ? options.queryContext?.search : undefined;
      const orderBy = useQueryContext && !isOptions ? options.queryContext?.orderBy : undefined;
      const graphKey = JSON.stringify([querySource, predicates, search, orderBy, isOptions]);
      const shared = scopedGraphs.get(graphKey);
      const compiled = shared
        ? { ...shared, dependencies: [], query: { ...shared.query, name: alias } }
        : compileAliasedQuery(querySource, alias, predicates, search, orderBy, options.evaluatedAt, resolvedQueries, isOptions);
      if (!shared) scopedGraphs.set(graphKey, { ...compiled, query: { ...compiled.query } });
      /** @type {Record<string, unknown>} */
      const query = compiled.query;
      if (isPlainObject(view) && (view.mark === 'element' || view.mark === 'list' && view['lazy-list'] === true)
          && Number.isSafeInteger(viewData?.limit)) {
        query.limit = Math.min(
          Number(viewData?.limit),
          typeof query.limit === 'number' ? query.limit : Infinity
        );
      }
      if (isPlainObject(view) && view.mark === 'chart' && chartFacet(view) && !isOptions) {
        const facetView = (view.chart === 'treemap' || hasScatterValue(view)) && useQueryContext && options.queryContext?.orderBy?.length
          ? { ...view, data: { ...viewData, 'order-by': options.queryContext.orderBy } }
          : view;
        queries.push(...compiled.dependencies, ...compileChartFacetQuery(facetView, query));
      } else if (isPlainObject(view) && view.mark === 'chart' && (view.chart === 'treemap' || hasScatterValue(view)) && !isOptions) {
        // These charts consume ready-to-render rows, never presenter-side ordering or limiting.
        const inputName = `${alias}:input`;
        const limit = Number(viewData?.limit);
        const order = useQueryContext && options.queryContext?.orderBy?.length
          ? options.queryContext.orderBy
          : viewData?.['order-by'];
        const x = isPlainObject(view.encoding) && isPlainObject(view.encoding.x) ? view.encoding.x : null;
        queries.push(...compiled.dependencies, { ...query, name: inputName }, {
          name: alias,
          from: inputName,
          ...(hasScatterValue(view) && x && typeof x['time-unit'] === 'string'
            ? { compute: [{ as: x.field, function: 'date-bucket', args: [
              { field: x.field }, { value: x['time-unit'] }
            ] }] } : {}),
          ...(Array.isArray(order) ? { 'order-by': order } : {}),
          ...(Number.isSafeInteger(limit) && limit > 0 ? { limit } : {})
        });
      } else {
        queries.push(...compiled.dependencies, query);
      }
      if (compiled.replacesSource || querySource !== sourceName) replacedSources.add(sourceName);
    });
  });

  for (const source of unaliasedSources) replacedSources.delete(source);
  debugViewPayloadCompiler({
    event: 'compiled',
    pageId,
    viewId: options.viewId ?? null,
    viewCount: views.length,
    aliasCount: aliases.length,
    queryCount: queries.length,
    replacedSourceCount: replacedSources.size
  });

  return { aliases, queries, replacedSources: [...replacedSources] };
}

/**
 * @param {unknown} page
 * @param {{ views?: unknown, viewId?: string, sourceNames: string[], backend?: import('../../view-availability.js').DashboardDataBackend }} options
 */
export function dashboardPartialViewSources(page, options) {
  const payload = pagePayload(page, options.views);
  if (!options.viewId && payload['query-binding'] !== 'page') return [];
  if (options.viewId && options.sourceNames.length !== 1) return [];
  return (payload.views ?? []).flatMap((view, index) => {
    if (options.backend && !viewBackendAvailable(view, options.backend)) return [];
    if (!isPlainObject(view) || !isPlainObject(view.data)) return [];
    const viewId = typeof view.id === 'string' ? view.id : `view-${index + 1}`;
    if (options.viewId && options.viewId !== viewId) return [];
    const source = view.data.source;
    const partialSource = view.data['partial-source'];
    return typeof source === 'string' && options.sourceNames.includes(source)
      && getViewSources(view).length === 1 && typeof partialSource === 'string'
      ? [{ viewId, source, partialSource }] : [];
  });
}

/** @param {unknown} form */
export function dashboardFormDefaultValues(form) {
  if (!isPlainObject(form) || !Array.isArray(form.fields)) return {};
  /** @type {Array<[string, string|number|boolean]>} */
  const entries = form.fields.flatMap((field) => {
    if (!isPlainObject(field) || typeof field.id !== 'string') return [];
    const value = field.default;
    return typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean'
      ? [[field.id, value]]
      : [];
  });
  return Object.fromEntries(entries);
}

/**
 * Resolves inert parameter references before query graph compilation. Query
 * structure remains authored and static; only scalar operands are substituted.
 * @param {unknown} definitions
 * @param {Record<string, string|number|boolean>} values
 */
export function resolveDashboardQueryParameters(definitions, values) {
  if (!Array.isArray(definitions)) return definitions;
  return definitions.map((definition) => {
    if (!isPlainObject(definition)) return definition;
    const declared = new Map(Array.isArray(definition.parameters)
      ? definition.parameters.flatMap((parameter) => (
          isPlainObject(parameter) && typeof parameter.name === 'string' && typeof parameter.type === 'string'
            ? [[parameter.name, parameter.type]]
            : []
        ))
      : []);
    /** @param {unknown} value @param {string} [containerKey] @returns {unknown} */
    const resolve = (value, containerKey) => {
      if (Array.isArray(value)) return value.map((item) => resolve(item, containerKey));
      if (!isPlainObject(value)) return value;
      if (Object.keys(value).length === 1 && typeof value.parameter === 'string') {
        const type = declared.get(value.parameter);
        if (!type) {
          debugViewPayloadCompiler({
            event: 'parameter-resolution-failed',
            query: String(definition.name),
            reason: 'undeclared-parameter'
          });
          throw new TypeError(`Query "${String(definition.name)}" references undeclared parameter "${value.parameter}".`);
        }
        const resolved = values[value.parameter];
        if (typeof resolved !== type || (type === 'number' && !Number.isFinite(resolved))) {
          debugViewPayloadCompiler({
            event: 'parameter-resolution-failed',
            query: String(definition.name),
            reason: 'missing-form-parameter'
          });
          throw new TypeError(`Query "${String(definition.name)}" requires form parameter "${value.parameter}".`);
        }
        return containerKey === 'args' ? { value: resolved } : resolved;
      }
      return Object.fromEntries(Object.entries(value).map(([key, nested]) => [key, resolve(nested, key)]));
    };
    return resolve(definition);
  });
}

/**
 * Leaves an unmodified database table on its native IndexedDB read path.
 * The presenter already resolves the source directly when no view alias exists.
 * @param {unknown} view
 * @param {string} sourceName
 * @param {Array<Record<string, unknown>>} predicates
 * @param {GlobalQueryContext | undefined} queryContext
 * @param {unknown} definitions
 */
function usesNativeSource(view, sourceName, predicates, queryContext, definitions) {
  if (isPlainObject(view) && (chartFacet(view) || hasScatterValue(view))) return false;
  const declared = Array.isArray(definitions)
    && definitions.some((definition) => isPlainObject(definition) && definition.name === sourceName);
  const data = isPlainObject(view) && isPlainObject(view.data) ? view.data : null;
  if (declared && isPlainObject(view) && view['lazy-list'] !== true && view.mark !== 'element' && view.controls !== 'filter-only' && view.chart !== 'treemap' && data?.['query-context'] === false && predicates.length === 0
      && !(queryContext?.search?.query.trim()) && !(queryContext?.orderBy?.length)
      && !requiresContextualQuery(sourceName, definitions)) return true;
  return isPlainObject(view)
    && view.mark === 'list'
    && view['lazy-list'] !== true
    && !declared
    && predicates.length === 0
    && !(queryContext?.search?.query.trim())
    && !(queryContext?.orderBy?.length);
}

/** @param {Record<string, unknown>} view */
function hasScatterValue(view) {
  return view.chart === 'scatter' && isPlainObject(view.encoding) && isPlainObject(view.encoding.value);
}

/** @param {string} name @param {unknown} definitions */
function requiresContextualQuery(name, definitions) {
  const byName = new Map(Array.isArray(definitions)
    ? definitions.filter(isPlainObject).map((definition) => [definition.name, definition])
    : []);
  const pending = [name];
  const visited = new Set();
  while (pending.length > 0) {
    const current = pending.pop();
    if (visited.has(current)) continue;
    visited.add(current);
    const definition = byName.get(current);
    if (!definition) continue;
    if (isPlainObject(definition.time) && typeof definition.time.range === 'string') return true;
    if (Array.isArray(definition.parameters) && definition.parameters.length > 0) return true;
    pending.push(...[
      definition.from,
      ...(Array.isArray(definition.union) ? definition.union : []),
      ...(Array.isArray(definition.joins) ? definition.joins.filter(isPlainObject).map((join) => join.source) : [])
    ].filter((dependency) => typeof dependency === 'string'));
  }
  return false;
}

/** @param {unknown} view @param {'chart'|'table'|'card'} mode */
function viewMatchesMode(view, mode) {
  if (!isPlainObject(view)) return mode === 'chart';
  if (view.mark === 'element') return true;
  if (view.disclosure === 'supplemental') return true;
  if (mode === 'table') return view.mark === 'table';
  if (mode === 'card') return view.mark === 'table' || view.mark === 'list';
  return view.mark !== 'table' && view.mark !== 'list';
}

/**
 * @param {string} sourceName
 * @param {string} alias
 * @param {Array<Record<string, unknown>>} predicates
 * @param {GlobalQueryContext['search']} search
 * @param {GlobalQueryContext['orderBy']} orderBy
 * @param {string | undefined} evaluatedAt
 * @param {unknown} definitions
 * @param {boolean} [scopeInput]
 */
function compileAliasedQuery(sourceName, alias, predicates, search, orderBy, evaluatedAt, definitions, scopeInput = false) {
  const declaredQueries = Array.isArray(definitions) ? definitions.filter(isPlainObject) : [];
  const declared = declaredQueries
    .find((definition) => definition.name === sourceName);
  const declaredNames = new Set(declaredQueries
    .map((definition) => definition.name)
    .filter((name) => typeof name === 'string'));
  const standalone = isPlainObject(declared)
    && typeof declared.from === 'string'
    && !declaredNames.has(declared.from)
    && (!Array.isArray(declared.union) || declared.union.every((source) => (
      typeof source === 'string' && !declaredNames.has(source)
    )))
    && (!Array.isArray(declared.joins) || declared.joins.every((join) => (
      isPlainObject(join) && typeof join.source === 'string' && !declaredNames.has(join.source)
    )));
  const postQueryFields = isPlainObject(declared) ? derivedOutputFields(declared) : new Set();
  const requiresOutputFilter = Boolean(isPlainObject(declared) && Array.isArray(declared.window) && declared.window.length && predicates.length)
    || predicates.some((predicate) => (
    typeof predicate.field === 'string' && postQueryFields.has(predicate.field)
  ));
  if (isPlainObject(declared) && standalone && requiresOutputFilter) {
    return compileStandaloneOutputScopedQuery(
      declared,
      alias,
      predicates,
      postQueryFields,
      search,
      orderBy,
      evaluatedAt
    );
  }
  if (isPlainObject(declared) && !standalone) {
    return compileScopedQueryGraph(
      sourceName,
      alias,
      predicates,
      search,
      orderBy,
      evaluatedAt,
      declaredQueries,
      scopeInput
    );
  }

  /**
   * @param {Record<string, unknown>} definition
   * @param {string} alias
   * @param {Array<Record<string, unknown>>} predicates
   * @param {Set<unknown>} postQueryFields
   * @param {GlobalQueryContext['search']} search
   * @param {GlobalQueryContext['orderBy']} orderBy
   * @param {string | undefined} evaluatedAt
   */
  function compileStandaloneOutputScopedQuery(definition, alias, predicates, postQueryFields, search, orderBy, evaluatedAt) {
    const rootName = `${alias}:root`;
    const declaredFilter = isPlainObject(definition.filter) ? definition.filter : null;
    const declaredPredicates = declaredFilter && Array.isArray(declaredFilter.predicates)
      ? declaredFilter.predicates.filter(isPlainObject)
      : [];
    const hasWindow = Array.isArray(definition.window) && definition.window.length > 0;
    const preQueryPredicates = predicates.filter((predicate) => (
      predicate.field === '@time' || (!hasWindow && !postQueryFields.has(predicate.field))
    ));
    const rootPredicates = applyQueryTime(
      [...declaredPredicates, ...preQueryPredicates],
      definition.time,
      evaluatedAt
    );
    const root = resolveQueryContext({
      ...definition,
      name: rootName,
      'order-by': undefined,
      limit: undefined,
      ...(rootPredicates.length > 0 ? { filter: { predicates: rootPredicates } } : { filter: undefined })
    }, queryTimeEnd(rootPredicates) ?? evaluatedAt, queryTimeStart(rootPredicates));
    const outputPredicates = predicates.filter((predicate) => (
      predicate.field !== '@time' && (hasWindow || postQueryFields.has(predicate.field))
    ));
    const runtimeSearch = search && search.query.trim() && search.fields.length > 0
      ? { fields: search.fields, query: search.query.trim() }
      : undefined;
    const filter = {
      ...(outputPredicates.length > 0 ? { predicates: outputPredicates } : {}),
      ...(runtimeSearch ? { search: runtimeSearch } : {})
    };
    const effectiveOrder = Array.isArray(orderBy) && orderBy.length > 0
      ? orderBy
      : Array.isArray(definition['order-by']) ? definition['order-by'] : undefined;
    return {
      replacesSource: true,
      dependencies: [root],
      query: {
        name: alias,
        from: rootName,
        ...(Object.keys(filter).length > 0 ? { filter } : {}),
        ...(effectiveOrder ? { 'order-by': effectiveOrder } : {}),
        ...(Number.isInteger(definition.limit) ? { limit: definition.limit } : {})
      }
    };
  }

  /** @param {Record<string, unknown>} query */
  function derivedOutputFields(query) {
    const fields = new Set();
    for (const clause of ['compute', 'predict', 'window']) {
      if (!Array.isArray(query[clause])) continue;
      for (const item of query[clause]) {
        if (isPlainObject(item) && typeof item.as === 'string') fields.add(item.as);
      }
    }
    if (isPlainObject(query.aggregate) && Array.isArray(query.aggregate.values)) {
      for (const item of query.aggregate.values) {
        if (isPlainObject(item) && typeof item.as === 'string') fields.add(item.as);
      }
    }
    if (Array.isArray(query.select)) {
      for (const item of query.select) {
        if (isPlainObject(item) && typeof item.as === 'string' && item.as !== item.field) fields.add(item.as);
      }
    }
    return fields;
  }
  const sourceQuery = standalone
    ? /** @type {Record<string, unknown>} */ (declared)
    : undefined;
  const declaredFilter = sourceQuery && isPlainObject(sourceQuery.filter)
    ? /** @type {Record<string, unknown>} */ (sourceQuery.filter)
    : null;
  const declaredPredicates = declaredFilter && Array.isArray(declaredFilter.predicates)
    ? declaredFilter.predicates.filter(isPlainObject)
    : [];
  const combinedPredicates = applyQueryTime(
    [...declaredPredicates, ...predicates],
    sourceQuery?.time,
    evaluatedAt
  );
  const executableSourceQuery = sourceQuery
    ? resolveQueryContext(sourceQuery, queryTimeEnd(combinedPredicates) ?? evaluatedAt, queryTimeStart(combinedPredicates))
    : undefined;
  const runtimeSearch = search && search.query.trim() && search.fields.length > 0
    ? { fields: search.fields, query: search.query.trim() }
    : undefined;
  const runtimeOrder = Array.isArray(orderBy) && orderBy.length > 0 ? orderBy : undefined;
  const filter = {
    ...(combinedPredicates.length > 0 ? { predicates: combinedPredicates } : {}),
    ...(runtimeSearch ? { search: runtimeSearch } : {})
  };
  return {
    replacesSource: Boolean(sourceQuery),
    dependencies: [],
    query: executableSourceQuery
      ? {
        ...executableSourceQuery,
        name: alias,
        ...(Object.keys(filter).length > 0 ? { filter } : { filter: undefined }),
        ...(runtimeOrder ? { 'order-by': runtimeOrder } : {})
      }
      : {
        name: alias,
        from: sourceName,
        ...(Object.keys(filter).length > 0 ? { filter } : {}),
        ...(runtimeOrder ? { 'order-by': runtimeOrder } : {})
      }
  };
}

/**
 * Clones a derived graph, moving row predicates through projections and group
 * keys. Computed predicates stay after compute, where native row-local plans can
 * evaluate them without materializing an unscoped intermediate.
 * @param {string} sourceName
 * @param {string} alias
 * @param {Array<Record<string, unknown>>} predicates
 * @param {GlobalQueryContext['search']} search
 * @param {GlobalQueryContext['orderBy']} orderBy
 * @param {string | undefined} evaluatedAt
 * @param {Array<Record<string, unknown>>} definitions
 * @param {boolean} [scopeInput]
 */
function compileScopedQueryGraph(sourceName, alias, predicates, search, orderBy, evaluatedAt, definitions, scopeInput = false) {
  const byName = new Map(definitions
    .filter((definition) => typeof definition.name === 'string')
    .map((definition) => [/** @type {string} */ (definition.name), definition]));
  const structuralSources = new Set(['campaigns', 'repositories', 'workflows']);
  const rootComputedName = `${alias}:root`;
  const temporalPredicates = predicates.filter((predicate) => predicate.field === '@time');
  const analysisEnd = queryTimeEnd(temporalPredicates) ?? evaluatedAt;
  /** @type {Array<Record<string, unknown>>} */
  const dependencies = [];
  /** @type {Map<string, string>} */
  const compiled = new Map();
  const usedNames = new Set([rootComputedName]);

  /** @param {string} name */
  const allocateName = (name) => {
    const base = name === sourceName ? rootComputedName : `${alias}:dependency:${slug(name)}`;
    if (name === sourceName && !compiled.size) return base;
    let candidate = base;
    let suffix = 1;
    while (usedNames.has(candidate)) candidate = `${base}:${++suffix}`;
    usedNames.add(candidate);
    return candidate;
  };

  /**
   * @param {string} name
   * @param {Array<Record<string, unknown>>} outputPredicates
   * @param {Array<Record<string, unknown>>} inheritedTime
   * @param {boolean} retainedHistory
   * @returns {string}
   */
  const compile = (name, outputPredicates, inheritedTime = temporalPredicates, retainedHistory = false) => {
    const key = JSON.stringify([name, outputPredicates, inheritedTime, retainedHistory]);
    const existing = compiled.get(key);
    if (existing) return existing;
    const definition = byName.get(name);
    if (!definition) return name;
    if (typeof definition.from !== 'string') {
      throw new TypeError(`Declared dashboard query "${name}" requires a source.`);
    }
    const scoped = allocateName(name);
    compiled.set(key, scoped);
    const from = definition.from;
    const inputTime = isPlainObject(definition.time) && definition.time.range === 'all'
      ? applyQueryTime(inheritedTime, definition.time, analysisEnd)
      : inheritedTime;
    const includeHistory = retainedHistory || (isPlainObject(definition.time) && definition.time.range === 'all');
    // Option queries scope their input rows before projecting distinct values.
    const inputScoped = scopeInput && name === sourceName;
    // Daily scope must reach the count branch before inventory's top-N limit.
    const dailyCounts = resolveDashboardQuerySources(definitions, [name]).includes('daily-run-counts');
    const inputPredicates = (inputScoped ? outputPredicates : predicatesBeforeQuery(
      dailyCounts ? { ...definition, limit: undefined } : definition, outputPredicates))
      .filter((predicate) => !includeHistory || predicate.field !== '@time');
    const retainedPredicates = inputScoped ? [] : outputPredicates;
    /** @param {string} source @param {number} inputIndex */
    const compileInput = (source, inputIndex) => {
      if (byName.has(source)) return compile(source, inputPredicates, inputTime, includeHistory);
      if (source === 'daily-run-counts') {
        const unsupported = inputPredicates.find((predicate) => predicate.optional
          && !['organization', 'repository', 'workflow'].includes(String(predicate.field)));
        if (unsupported) {
          throw new TypeError(`Daily run counts do not support the selected "${String(unsupported.field)}" filter.`);
        }
      }
      if (inputPredicates.length === 0) return source;
      const inputName = `${scoped}:input-${inputIndex + 1}`;
      dependencies.push({
        name: inputName,
        from: source,
        filter: { predicates: inputPredicates }
      });
      return inputName;
    };
    const compiledFrom = compileInput(from, 0);
    const compiledUnion = Array.isArray(definition.union)
      ? definition.union.map((source, index) => typeof source === 'string' ? compileInput(source, index + 1) : source)
      : undefined;
    const compiledJoins = Array.isArray(definition.joins)
      ? definition.joins.map((join) => (
          isPlainObject(join) && typeof join.source === 'string'
            ? { ...join, source: compile(join.source, [
                ...joinKeyPredicates(join, inputPredicates),
                ...(resolveDashboardQuerySources(definitions, [join.source]).includes('daily-run-counts')
                  ? inputPredicates.filter((predicate) => predicate.optional).map((/** @type {Record<string, unknown>} */ predicate) => (
                      predicate.field === 'repository' && Array.isArray(predicate.in)
                        ? { ...predicate, in: predicate.in.map((value) => typeof value === 'string'
                          ? value.slice(value.indexOf('/') + 1) : value) }
                        : predicate
                    )) : [])
              ], inputTime, includeHistory) }
            : join
        ))
      : undefined;

    const declaredFilter = isPlainObject(definition.filter) ? definition.filter : null;
    const declaredPredicates = declaredFilter && Array.isArray(declaredFilter.predicates)
      ? declaredFilter.predicates.filter(isPlainObject)
      : [];
    const requestPredicates = !byName.has(from) && !structuralSources.has(from)
      ? inputTime
      : [];
    const combinedPredicates = applyQueryTime(
      [...declaredPredicates, ...requestPredicates],
      definition.time,
      queryTimeEnd(inputTime) ?? analysisEnd
    );
    const filter = {
      ...declaredFilter,
      ...(combinedPredicates.length > 0 ? { predicates: combinedPredicates } : {})
    };
    const query = resolveQueryContext({
      ...definition,
      name: retainedPredicates.length > 0 ? `${scoped}:unscoped` : scoped,
      from: compiledFrom,
      ...(compiledUnion ? { union: compiledUnion } : {}),
      ...(compiledJoins ? { joins: compiledJoins } : {}),
      ...(Object.keys(filter).length > 0 ? { filter } : { filter: undefined })
    }, queryTimeEnd(combinedPredicates) ?? queryTimeEnd(inputTime) ?? analysisEnd,
    queryTimeStart(combinedPredicates) ?? queryTimeStart(inputTime));
    dependencies.push(query);
    if (retainedPredicates.length > 0) {
      dependencies.push({
        name: scoped,
        from: query.name,
        filter: { predicates: retainedPredicates }
      });
    }
    return scoped;
  };

  const requestPredicates = predicates.filter((predicate) => predicate.field !== '@time');
  const compiledRoot = compile(sourceName, scopeInput ? predicates : requestPredicates);
  const runtimeSearch = search && search.query.trim() && search.fields.length > 0
    ? { fields: search.fields, query: search.query.trim() }
    : undefined;
  const filter = {
    ...(!scopeInput && requestPredicates.length > 0 ? { predicates: requestPredicates } : {}),
    ...(runtimeSearch ? { search: runtimeSearch } : {})
  };
  const query = {
    name: alias,
    from: compiledRoot,
    ...(Object.keys(filter).length > 0 ? { filter } : {}),
    ...(Array.isArray(orderBy) && orderBy.length > 0 ? { 'order-by': orderBy } : {})
  };
  return { replacesSource: true, dependencies, query };
}

/**
 * Only scalar fields unchanged by a clause commute with its output filter.
 * A computed label is deliberately never inverted into its component fields:
 * concat is not injective, and its null/structured-input semantics matter.
 * @param {Record<string, unknown>} definition
 * @param {Array<Record<string, unknown>>} predicates
 */
function predicatesBeforeQuery(definition, predicates) {
  if (definition.limit !== undefined
      || definition['temporal-series']
      || definition.facet
      || (Array.isArray(definition.predict) && definition.predict.length > 0)) return [];
  const computed = new Set(['compute', 'window'].flatMap((clause) => (
    Array.isArray(definition[clause]) ? definition[clause].filter(isPlainObject).map((value) => value.as) : []
  )));
  const joined = new Set(Array.isArray(definition.joins)
    ? definition.joins.filter(isPlainObject).flatMap((join) => (
        Array.isArray(join.fields) ? join.fields.filter(isPlainObject).map((value) => value.as ?? value.field) : []
      ))
    : []);
  const windows = Array.isArray(definition.window) ? definition.window.filter(isPlainObject) : [];
  const groups = isPlainObject(definition.aggregate)
    ? new Set(Array.isArray(definition.aggregate.by) ? definition.aggregate.by : [])
    : null;
  const selected = Array.isArray(definition.select)
    ? new Map(definition.select.filter(isPlainObject).map((value) => [value.as ?? value.field, value.field]))
    : null;
  return predicates.flatMap((predicate) => {
    const field = selected ? selected.get(predicate.field) ?? (predicate.optional ? predicate.field : undefined) : predicate.field;
    if (typeof field !== 'string' || computed.has(field) || joined.has(field)
        || (groups && !groups.has(field) && !predicate.optional)
        || windows.some((window) => !Array.isArray(window.groupby) || !window.groupby.includes(field))) return [];
    return [{ ...predicate, field }];
  });
}

/**
 * Only mandatory equality on a preserved left key can narrow a join's right
 * input. Other right-side evidence (including cross-run baselines) stays intact.
 * @param {Record<string, unknown>} join
 * @param {Array<Record<string, unknown>>} predicates
 */
function joinKeyPredicates(join, predicates) {
  if (!Array.isArray(join.on)) return [];
  const keys = join.on;
  return predicates.flatMap((predicate) => {
    if (predicate.optional || !Object.hasOwn(predicate, 'equals')) return [];
    const key = keys.find((pair) => isPlainObject(pair) && pair.left === predicate.field);
    return isPlainObject(key) && typeof key.right === 'string'
      ? [{ ...predicate, field: key.right }]
      : [];
  });
}

/** @param {Array<Record<string, unknown>>} predicates @param {unknown} time @param {string | undefined} evaluatedAt */
function applyQueryTime(predicates, time, evaluatedAt) {
  if (!isPlainObject(time) || typeof time.range !== 'string') return predicates;
  const end = queryTimeEnd(predicates) ?? evaluatedAt;
  if (time.range === 'all') {
    return predicates.filter((predicate) => predicate.field !== '@time');
  }
  const match = /^([1-9][0-9]*)(h|d|w)$/.exec(time.range);
  if (!end || !match) return predicates;
  const unitHours = { h: 1, d: 24, w: 168 }[match[2]];
  const endMs = Date.parse(end);
  if (!unitHours || !Number.isFinite(endMs)) return predicates;
  const start = new Date(endMs - Number(match[1]) * unitHours * 3_600_000).toISOString();
  return [
    ...predicates.filter((predicate) => predicate.field !== '@time'),
    { field: '@time', gte: start },
    { field: '@time', lt: end }
  ];
}

/** @param {Array<Record<string, unknown>>} predicates */
function queryTimeEnd(predicates) {
  const end = predicates.findLast((predicate) => predicate.field === '@time' && typeof predicate.lt === 'string')?.lt;
  return typeof end === 'string' ? end : undefined;
}

/** @param {Array<Record<string, unknown>>} predicates */
function queryTimeStart(predicates) {
  const start = predicates.findLast((predicate) => predicate.field === '@time' && typeof predicate.gte === 'string')?.gte;
  return typeof start === 'string' ? start : undefined;
}

/** @param {Record<string, unknown>} query @param {string | undefined} timeEnd @param {string | undefined} [timeStart] */
function resolveQueryContext(query, timeEnd, timeStart) {
  if (!Array.isArray(query.compute)) return query;
  return {
    ...query,
    compute: query.compute.map((computed) => !isPlainObject(computed) || !Array.isArray(computed.args)
      ? computed
      : {
          ...computed,
          args: computed.args.map((argument) => (
            isPlainObject(argument) && argument.context === 'time-end'
              ? { value: timeEnd ?? '' }
              : isPlainObject(argument) && argument.context === 'time-start'
                ? { value: timeStart ?? '' }
              : argument
          ))
        })
  };
}

/** @param {unknown} scope */
function compileScopePredicates(scope) {
  if (!isPlainObject(scope)) return [];
  const configured = /** @type {Record<string, unknown>} */ (scope);
  const map = {
    organizations: 'organization',
    repositories: 'repository',
    workflows: 'workflow'
  };
  return Object.entries(map).flatMap(([scopeKey, field]) => {
    const values = asStringList(configured[scopeKey]);
    return values.length > 0 ? [{ field, in: values }] : [];
  });
}

/** @param {unknown} filters */
function compileViewFilterPredicates(filters) {
  if (!isPlainObject(filters)) return [];
  const configured = /** @type {Record<string, unknown>} */ (filters);
  /** @type {Array<Record<string, unknown>>} */
  const predicates = [];
  for (const [field, expected] of Object.entries(configured)) {
    const values = Array.isArray(expected) ? expected : undefined;
    if (values) {
      const list = values.filter((candidate) => candidate !== undefined);
      if (list.length > 0) predicates.push({ field, in: list });
    } else {
      predicates.push({ field, equals: expected });
    }
  }
  return predicates;
}

/**
 * @param {unknown} args
 * @param {Record<string, string> | undefined} routeParameters
 */
function compileArgumentPredicates(args, routeParameters) {
  if (!Array.isArray(args)) return [];
  return args.flatMap((argument) => {
    if (!isPlainObject(argument) || typeof argument.name !== 'string' || typeof argument.field !== 'string') return [];
    return [{
      field: argument.field,
      equals: typeof routeParameters?.[argument.name] === 'string'
        ? routeParameters[argument.name]
        : ''
    }];
  });
}

/** @param {unknown} filters */
function compileGlobalFilterPredicates(filters) {
  if (!isPlainObject(filters)) return [];
  return Object.entries(/** @type {Record<string, unknown>} */ (filters)).flatMap(([configuredField, values]) => {
    const list = asStringList(values);
    if (list.length === 0) return [];
    return [{
      field: configuredField === 'mode' ? 'rollout-mode' : configuredField,
      in: list,
      optional: true
    }];
  });
}

/** @param {unknown} time */
function compileTimePredicates(time) {
  if (!isPlainObject(time)) return [];
  const configured = /** @type {Record<string, unknown>} */ (time);
  const start = typeof configured.start === 'string' ? configured.start : '';
  const end = typeof configured.end === 'string' ? configured.end : '';
  /** @type {Array<Record<string, unknown>>} */
  const predicates = [];
  if (start) predicates.push({ field: '@time', gte: start });
  if (end) predicates.push({ field: '@time', lt: end });
  return predicates;
}

/** @param {string} routeField @param {string} routeValue */
function compileRoutePredicates(routeField, routeValue) {
  const value = decodeRouteValue(routeValue.trim());
  if (!routeField) return [];
  if (!value) return [
    { field: routeField, equals: '' },
    { field: routeField, equals: '\0' }
  ];
  if (routeField === 'workflow') {
    const separator = value.indexOf(':');
    const repository = separator > 0 ? value.slice(0, separator) : '';
    const workflow = separator > 0 ? value.slice(separator + 1) : value;
    const slash = repository.indexOf('/');
    return [
      ...(slash > 0 ? [
        { field: 'organization', equals: repository.slice(0, slash) },
        { field: 'repository', equals: repository.slice(slash + 1) }
      ] : []),
      { field: 'workflow', equals: workflow }
    ];
  }
  return [{ field: routeField, equals: value }];
}

/** @param {string} value */
function decodeRouteValue(value) {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

/** @param {unknown} value */
function asStringList(value) {
  if (!Array.isArray(value)) return [];
  return [...new Set(value.filter((item) => typeof item === 'string' && item.trim().length > 0).map((item) => item.trim()))];
}

/** @param {unknown} page @param {unknown} reusableViews */
function pagePayload(page, reusableViews) {
  if (!isPlainObject(page)) return {};
  const configured = /** @type {Record<string, unknown>} */ (page);
  const builtInDefinition = configured.kind === 'built-in' && isPlainObject(configured.definition)
    ? /** @type {Record<string, unknown>} */ (configured.definition)
    : null;
  const viewsById = new Map((Array.isArray(reusableViews) ? reusableViews : [])
    .filter(isPlainObject)
    .map((view) => [view.id, view]));
  const views = /** @type {unknown[]} */ (
    builtInDefinition?.views ?? (Array.isArray(configured.views) ? configured.views : [])
  );
  return {
    'query-binding': configured['query-binding'],
    views: views.map((view) => typeof view === 'string' ? viewsById.get(view) ?? view : view),
    route: isPlainObject(configured.route) ? configured.route : null,
    form: isPlainObject(configured.form) ? configured.form : null
  };
}

/** @param {unknown} value */
function slug(value) {
  const text = String(value ?? '').trim().toLowerCase();
  const compact = text.replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
  return compact.length > 0 ? compact : 'view';
}

/** @param {unknown} value @returns {value is Record<string, unknown>} */
function isPlainObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
