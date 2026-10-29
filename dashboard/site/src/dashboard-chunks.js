import { resolveDashboardQuerySources } from './data/queries/declarative.js';
import { elementLoadsSourcesAsync } from './components/ui-elements.js';
import { navigationIndicatorSourceNames } from './navigation-indicator.js';
import { createDebug } from './debug.js';
import { dashboardDataBackend, pageCapabilityAvailable, viewBackendAvailable } from './view-availability.js';
import { dashboardViewSourceNames as getViewSources, viewDataSourceNames } from './view-filter-contract.js';

const debugDashboardChunks = createDebug('dashboard-chunks');

/** @typedef {import('./view-availability.js').DashboardDataBackend | 'all'} BackendSelection */
/** @typedef {{ sourceNames: string[], lazySourceNames: string[], tableSourceNames: string[] }} PageSourceIndex */
/**
 * @typedef {{ id?: string, kind?: string, title?: string, description?: string, icon?: string, ['navigation-label']?: string, ['class-name']?: string, route?: { ['hash-query-parameter']?: string, ['navigation-page']?: string }, views?: unknown[], sections?: unknown[], definition?: { views?: unknown[], sections?: unknown[] }, chunk?: string, ['source-names']?: string[], ['lazy-source-names']?: string[], ['table-source-names']?: string[], ['independent-source-bindings']?: boolean } & Record<string, unknown>} DashboardPage
 */

/**
 * @typedef {{ languageVersion: string, dashboard: { pages: DashboardPage[], queries?: Array<Record<string, unknown>>, callouts?: Array<Record<string, unknown>> } & Record<string, unknown> }} DashboardDocument
 */

/**
 * @typedef {{ name?: string } & Record<string, unknown>} DashboardQuery
 */

/**
 * @param {unknown} value
 * @returns {value is Record<string, unknown>}
 */
function isPlainObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/**
 * @param {unknown} value
 * @returns {string[]}
 */
function stringList(value) {
  return Array.isArray(value)
    ? value.filter((entry) => typeof entry === 'string' && entry.length > 0)
    : [];
}

/**
 * @param {DashboardPage | undefined} page
 * @returns {boolean}
 */
export function dashboardPageIsLoaded(page) {
  if (!page) return false;
  if (page.kind === 'built-in') {
    return Array.isArray(page.definition?.views) || Array.isArray(page.definition?.sections);
  }
  return Array.isArray(page.views) || Array.isArray(page.sections);
}

/**
 * @param {DashboardPage} page
 */
export function dashboardPageChunkPath(page) {
  const chunkPath = typeof page.chunk === 'string' && page.chunk.length > 0 ? page.chunk : null;
  if (!chunkPath) debugDashboardChunks({ event: 'chunk-path-missing', pageId: page.id ?? 'unknown' });
  return chunkPath;
}

/**
 * @param {DashboardPage} page
 * @param {unknown} [reusableViews]
 */
export function dashboardPagePayload(page, reusableViews = []) {
  const definition = isPlainObject(page.definition) ? page.definition : null;
  const configuredViews = page.kind === 'built-in'
    ? Array.isArray(definition?.views) ? definition.views : []
    : Array.isArray(page.views) ? page.views : [];
  const viewsById = new Map((Array.isArray(reusableViews) ? reusableViews : [])
    .filter(isPlainObject)
    .map((view) => [view.id, view]));
  const views = configuredViews.map((view) => (
    typeof view === 'string' ? viewsById.get(view) ?? view : view
  ));
  const sections = page.kind === 'built-in'
    ? Array.isArray(definition?.sections) ? definition.sections : undefined
    : Array.isArray(page.sections) ? page.sections : undefined;
  return {
    ...page,
    kind: 'custom',
    views,
    ...(sections ? { sections } : {}),
  };
}

/**
 * @param {DashboardDocument} document
 * @param {string} pageId
 */
function dashboardPage(document, pageId) {
  return Array.isArray(document.dashboard?.pages)
    ? document.dashboard.pages.find((candidate) => candidate?.id === pageId)
    : undefined;
}

/**
 * @param {unknown} view
 * @returns {boolean}
 */
function isAsyncElementView(view) {
  return isPlainObject(view)
    && typeof view.element === 'string'
    && elementLoadsSourcesAsync(view.element);
}

/**
 * Reports whether every source-backed view on a page owns its query lifecycle.
 * Section counts remain page-bound because their chrome is rendered once.
 * @param {DashboardPage | undefined} page
 * @param {unknown} reusableViews
 * @param {BackendSelection} [backend]
 */
export function dashboardPageSourcesAreIndependentlyBound(page, reusableViews = [], backend = dashboardDataBackend()) {
  if (!page) return false;
  if (!dashboardPageIsLoaded(page) && page['independent-source-bindings'] === true) return true;
  const payload = dashboardPagePayload(page, reusableViews);
  if ((payload.sections ?? []).some((section) => (
    isPlainObject(section) && (
      typeof section['count-source'] === 'string'
      || Array.isArray(section['count-sources']) && section['count-sources'].length > 0
    )
  ))) return false;
  const sourceViews = (payload.views ?? []).filter((view) => (
    (backend === 'all' || viewBackendAvailable(view, backend)) && getViewSources(view).length > 0
  ));
  return sourceViews.length > 0 && sourceViews.every(isAsyncElementView);
}

/**
 * @param {DashboardDocument} document
 * @param {string} pageId
 * @param {'chart'|'table'|'card'} [viewMode]
 * @param {BackendSelection} [backend]
 * @returns {string[]}
 */
export function dashboardPageSourceNames(document, pageId, viewMode, backend = dashboardDataBackend()) {
  const page = dashboardPage(document, pageId);
  if (!page || backend !== 'all' && !pageCapabilityAvailable(page)) return [];
  if (page['query-binding'] === 'page') viewMode = undefined;
  const indexed = backendSourceIndex(page, backend)?.sourceNames ?? stringList(page['source-names']);
  if (!dashboardPageIsLoaded(page) && !viewMode && (indexed.length > 0 || backendSourceIndex(page, backend))) return indexed;
  const payload = dashboardPagePayload(page, document.dashboard.views);
  const names = new Set();
  for (const view of payload.views ?? []) {
    if (backend !== 'all' && !viewBackendAvailable(view, backend)) continue;
    if (isAsyncElementView(view)) continue;
    if (viewMode && !viewMatchesMode(view, viewMode)) continue;
    for (const sourceName of getViewSources(view)) names.add(sourceName);
  }
  for (const section of payload.sections ?? []) {
    const configuredSection = isPlainObject(section) ? section : {};
    if (typeof configuredSection['count-source'] === 'string') names.add(configuredSection['count-source']);
    for (const sourceName of /** @type {unknown[]} */ (configuredSection['count-sources'] ?? [])) {
      if (typeof sourceName === 'string') names.add(sourceName);
    }
  }
  for (const callout of document.dashboard.callouts ?? []) {
    const visibility = isPlainObject(callout?.['visible-when']) ? callout['visible-when'] : null;
    if (typeof visibility?.source === 'string') {
      names.add(visibility.source);
    }
  }
  return [...names];
}

/**
 * Returns every source rendered by a page, including sources owned by
 * independently loading UI elements.
 * @param {DashboardDocument} document
 * @param {string} pageId
 * @param {BackendSelection} [backend]
 * @returns {string[]}
 */
export function dashboardPageAllSourceNames(document, pageId, backend = dashboardDataBackend()) {
  const page = dashboardPage(document, pageId);
  if (!page || backend !== 'all' && !pageCapabilityAvailable(page)) return [];
  const names = new Set(dashboardPageSourceNames(document, pageId, undefined, backend));
  const payload = dashboardPagePayload(page, document.dashboard.views);
  for (const view of payload.views ?? []) {
    if (backend !== 'all' && !viewBackendAvailable(view, backend)) continue;
    for (const sourceName of getViewSources(view)) names.add(sourceName);
  }
  return [...names];
}

/** @param {unknown} view @param {'chart'|'table'|'card'} mode */
function viewMatchesMode(view, mode) {
  if (!isPlainObject(view)) return mode === 'chart';
  if (view.mark === 'element') return true;
  if (mode === 'table') return view.mark === 'table';
  if (mode === 'card') return view.mark === 'table' || view.mark === 'list';
  return view.mark !== 'table' && view.mark !== 'list';
}

/**
 * @param {DashboardDocument} document
 * @param {string} pageId
 * @param {BackendSelection} [backend]
 * @returns {string[]}
 */
export function dashboardPageLazySourceNames(document, pageId, backend = dashboardDataBackend()) {
  const page = dashboardPage(document, pageId);
  if (!page || backend !== 'all' && !pageCapabilityAvailable(page)) return [];
  const indexed = backendSourceIndex(page, backend)?.lazySourceNames ?? stringList(page['lazy-source-names']);
  if (!dashboardPageIsLoaded(page) && (indexed.length > 0 || backendSourceIndex(page, backend))) return indexed;
  const payload = dashboardPagePayload(page, document.dashboard.views);
  return [...new Set((payload.views ?? []).flatMap((view) =>
    isPlainObject(view) && view['lazy-list'] === true
      && (backend === 'all' || viewBackendAvailable(view, backend)) ? viewDataSourceNames(view) : []
  ))];
}

/**
 * @param {DashboardDocument} document
 * @param {string} [pageId]
 * @param {BackendSelection} [backend]
 * @returns {string[]}
 */
export function dashboardTableSourceNames(document, pageId, backend = dashboardDataBackend()) {
  if (typeof pageId === 'string' && pageId.length > 0) {
    const page = dashboardPage(document, pageId);
    if (!page || backend !== 'all' && !pageCapabilityAvailable(page)) return [];
    const indexed = backendSourceIndex(page, backend)?.tableSourceNames ?? stringList(page['table-source-names']);
    if (!dashboardPageIsLoaded(page) && (indexed.length > 0 || backendSourceIndex(page, backend))) return indexed;
    const payload = dashboardPagePayload(page, document.dashboard.views);
    return [...new Set((payload.views ?? []).flatMap((view) =>
      isPlainObject(view) && view.mark === 'table'
        && (backend === 'all' || viewBackendAvailable(view, backend)) ? viewDataSourceNames(view) : []
    ))];
  }
  return [...new Set((document.dashboard.pages ?? []).flatMap((page) => dashboardTableSourceNames(document, page.id, backend)))];
}

/** @param {DashboardPage} page @param {BackendSelection} backend @returns {PageSourceIndex | undefined} */
function backendSourceIndex(page, backend) {
  if (backend === 'all' || !isPlainObject(page['backend-source-index'])) return undefined;
  return /** @type {PageSourceIndex | undefined} */ (page['backend-source-index'][backend]);
}

/**
 * Resolves `kind: 'built-in'` pages in `document` against the full `definition`
 * of the matching built-in page template found in `templateDocument` (looked
 * up by `page.page`). Used by tests and compliance fixtures that need a fully
 * resolved document without going through the runtime chunk-loading path.
 * @template {{ dashboard: { pages: DashboardPage[] } & Record<string, unknown> } & Record<string, unknown>} Document
 * @param {Document} document
 * @param {{ dashboard: { pages: DashboardPage[] } }} templateDocument
 * @returns {Document}
 */
export function resolveBuiltInPages(document, templateDocument) {
  return {
    ...document,
    dashboard: {
      ...document.dashboard,
      pages: document.dashboard.pages.map((page) => {
        if (page.kind !== 'built-in') return page;
        const template = templateDocument.dashboard.pages.find((candidate) => (
          candidate.kind === 'built-in' && candidate.page === page.page
        ));
        return template
          ? { ...template, ...page, definition: template.definition }
          : page;
      }),
    },
  };
}

/**
 * @param {DashboardPage} page
 * @param {string} chunkPath
 * @param {PageSourceIndex & { independentSourceBindings: boolean, backendSourceIndex?: Record<string, PageSourceIndex> }} index
 * @returns {DashboardPage}
 */
function stubDashboardPage(page, chunkPath, index) {
  /** @type {DashboardPage} */
  const base = {
    ...page,
    chunk: chunkPath,
    'source-names': index.sourceNames,
    'lazy-source-names': index.lazySourceNames,
    'table-source-names': index.tableSourceNames,
    'independent-source-bindings': index.independentSourceBindings,
    ...(index.backendSourceIndex ? { 'backend-source-index': index.backendSourceIndex } : {}),
  };
  for (const field of ['description', 'intent', 'subject', 'objective', 'acceptance']) delete base[field];
  if (page.kind === 'built-in') {
    delete base.definition;
  } else {
    delete base.views;
    delete base.sections;
  }
  return base;
}

/**
 * @param {DashboardPage | undefined} stub
 * @param {DashboardPage} page
 * @returns {DashboardPage}
 */
export function mergeDashboardPage(stub, page) {
  return {
    ...(stub ?? {}),
    ...page,
    ...(Array.isArray(stub?.['source-names']) ? {
      chunk: stub?.chunk,
      'source-names': stub?.['source-names'],
      'lazy-source-names': stub?.['lazy-source-names'],
      'table-source-names': stub?.['table-source-names'],
      'independent-source-bindings': stub?.['independent-source-bindings'],
      ...(stub?.['backend-source-index'] ? { 'backend-source-index': stub['backend-source-index'] } : {}),
    } : {}),
  };
}

/**
 * @param {{ page: DashboardPage, queries?: Array<Record<string, unknown>> }} chunk
 * @returns {{ page: DashboardPage, queries: Array<Record<string, unknown>> }}
 */
export function normalizeDashboardPageChunk(chunk) {
  return {
    page: /** @type {DashboardPage} */ (chunk.page),
    queries: Array.isArray(chunk.queries) ? chunk.queries : [],
  };
}

/**
 * Builds the relative chunk path a page's JSON is written to / fetched from,
 * given its id and the (optional) chunk directory name. Shared by
 * `splitDashboardDocument`, `scripts/build.mjs`, and `canvas-server.mjs` so
 * the naming scheme only needs to change in one place.
 * @param {string} pageId
 * @param {string} [chunkDirectory]
 * @returns {string}
 */
export function buildDashboardPageChunkPath(pageId, chunkDirectory = 'dashboard-pages') {
  return `${chunkDirectory}/${encodeURIComponent(pageId)}.json`;
}

/**
 * @param {{ 'language-version'?: string, dashboard?: { pages?: DashboardPage[], queries?: DashboardQuery[], callouts?: Array<Record<string, unknown>> } & Record<string, unknown> } | DashboardDocument} source
 * @param {{ chunkDirectory?: string }} [options]
 */
export function splitDashboardDocument(source, options = {}) {
  /** @type {DashboardDocument} */
  const document = 'languageVersion' in source
    ? /** @type {DashboardDocument} */ (source)
    : {
        languageVersion: source['language-version'] ?? '',
        dashboard: {
          ...(source.dashboard ?? {}),
          pages: source.dashboard?.pages ?? [],
        },
      };
  const chunkDirectory = options.chunkDirectory ?? 'dashboard-pages';
  const queryDefinitions = /** @type {DashboardQuery[]} */ (Array.isArray(document.dashboard.queries) ? document.dashboard.queries : []);
  /** @type {Map<string, { page: DashboardPage, queries: DashboardQuery[] }>} */
  const pageChunks = new Map();
  const corePages = (document.dashboard.pages ?? []).map((page) => {
    // Pages that already carry a `chunk` pointer are assumed to already be
    // split (e.g. a previously-built dashboard.json, or a mixed schema where
    // only some pages have been split so far); leave them exactly as-is
    // instead of re-splitting or discarding their existing chunk reference.
    if (typeof page.chunk === 'string' && page.chunk.length > 0) return page;
    const sourceNames = dashboardPageSourceNames(document, page.id ?? '', undefined, 'all');
    const lazySourceNames = dashboardPageLazySourceNames(document, page.id ?? '', 'all');
    const tableSourceNames = dashboardTableSourceNames(document, page.id ?? '', 'all');
    const independentSourceBindings = dashboardPageSourcesAreIndependentlyBound(page, document.dashboard.views, 'all');
    const payload = dashboardPagePayload(page, document.dashboard.views);
    const backendSourceIndexes = (payload.views ?? []).some((view) => isPlainObject(view) && view.requires !== undefined)
      ? Object.fromEntries(['static', 'hosted'].map((backend) => [
          backend,
          {
            sourceNames: dashboardPageSourceNames(document, page.id ?? '', undefined, /** @type {BackendSelection} */ (backend)),
            lazySourceNames: dashboardPageLazySourceNames(document, page.id ?? '', /** @type {BackendSelection} */ (backend)),
            tableSourceNames: dashboardTableSourceNames(document, page.id ?? '', /** @type {BackendSelection} */ (backend)),
          }
        ]))
      : undefined;
    const querySourceNames = new Set([
      ...sourceNames,
      // Indicator queries are available from the first loaded chunk, but are
      // requested separately so they never delay the active page.
      ...navigationIndicatorSourceNames(document.dashboard.pages ?? []),
      ...(payload.views ?? []).flatMap(getViewSources),
      ...(payload.views ?? []).flatMap((view) => (
        isPlainObject(view) && isPlainObject(view.data) && typeof view.data['partial-source'] === 'string'
          ? [view.data['partial-source']]
          : []
      )),
    ]);
    const requiredQueryNames = new Set(resolveDashboardQuerySources(queryDefinitions, querySourceNames));
    const queries = queryDefinitions.filter((query) => (
      typeof query?.name === 'string' && requiredQueryNames.has(query.name)
    ));
    const chunkPath = buildDashboardPageChunkPath(page.id ?? '', chunkDirectory);
    pageChunks.set(page.id ?? '', { page, queries });
    return stubDashboardPage(page, chunkPath, {
      sourceNames,
      lazySourceNames,
      tableSourceNames,
      independentSourceBindings,
      backendSourceIndex: backendSourceIndexes
    });
  });
  const dashboard = { ...document.dashboard, pages: corePages };
  const calloutSources = (document.dashboard.callouts ?? []).flatMap((callout) => {
    const visibility = isPlainObject(callout?.['visible-when']) ? callout['visible-when'] : null;
    return typeof visibility?.source === 'string' ? [visibility.source] : [];
  });
  const status = isPlainObject(document.dashboard['data-status']) ? document.dashboard['data-status'] : {};
  const statusSources = [status['freshness-source'], status['health-source']].filter((name) => typeof name === 'string');
  const shellQueries = new Set(resolveDashboardQuerySources(queryDefinitions, [...calloutSources, ...statusSources]));
  const shellDefinitions = queryDefinitions.filter((query) => shellQueries.has(query.name ?? ''));
  if (shellDefinitions.length) dashboard.queries = shellDefinitions;
  else delete dashboard.queries;
  debugDashboardChunks({
    event: 'split-complete',
    pageCount: corePages.length,
    chunkCount: pageChunks.size
  });
  return {
    core: {
      'language-version': document.languageVersion,
      dashboard,
    },
    pageChunks,
  };
}

/**
 * @param {{ 'language-version': string, dashboard: Record<string, unknown> & { pages: DashboardPage[] } }} core
 * @param {Iterable<{ page: DashboardPage, queries?: DashboardQuery[] }>} chunks
 */
export function resolveDashboardDocument(core, chunks) {
  /** @type {DashboardDocument} */
  const document = {
    languageVersion: core['language-version'],
    dashboard: {
      ...core.dashboard,
      pages: [...core.dashboard.pages],
      /** @type {DashboardQuery[]} */
      queries: Array.isArray(core.dashboard.queries) ? [...core.dashboard.queries] : [],
    },
  };
  const queries = /** @type {DashboardQuery[]} */ (document.dashboard.queries);
  const queryNames = queries.map((query) => query.name ?? '');
  for (const chunk of chunks) {
    const normalized = normalizeDashboardPageChunk(chunk);
    const pageIndex = document.dashboard.pages.findIndex((page) => page.id === normalized.page.id);
    if (pageIndex >= 0) {
      document.dashboard.pages.splice(
        pageIndex,
        1,
        mergeDashboardPage(document.dashboard.pages[pageIndex], normalized.page),
      );
    }
    for (const query of normalized.queries) {
      if (typeof query?.name !== 'string') continue;
      const existing = queryNames.indexOf(query.name);
      if (existing >= 0) {
        queries[existing] = query;
      } else {
        queryNames.push(query.name);
        queries.push(query);
      }
    }
  }
  debugDashboardChunks({
    event: 'resolve-complete',
    pageCount: document.dashboard.pages.length,
    queryCount: queries.length
  });
  return document;
}
