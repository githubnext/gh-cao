import { isPlainObject } from './validator-common.js';
import { createDebug } from './debug.js';

const debugValidatorState = createDebug('validator-state');

// A document's declarations are accumulated in validation order and reset after validation.
export const state = {
  /** @type {Map<string, string[] | undefined>} */
  declaredQueries: new Map(),
  /** @type {Map<string, Map<string, import('./query-type-checker.js').FieldType> | undefined>} */
  declaredQueryFieldTypes: new Map(),
  /** @type {Map<string, Map<string, string>>} */
  declaredQueryParameters: new Map(),
  /** @type {Set<string>} */
  declaredCardTemplates: new Set(),
  /** @type {Map<string, Record<string, unknown>>} */
  declaredCardDefinitions: new Map(),
  /** @type {Map<string, Record<string, unknown>>} */
  declaredViews: new Map(),
  /** @type {Map<string, Set<string>>} */
  declaredQueryTables: new Map(),
  /** @type {Map<string, Record<string, unknown>>} */
  declaredCliActions: new Map()
};

/** @param {unknown} page */
export function resolveReusablePageViews(page) {
  if (!isPlainObject(page)) return page;
  const definition = page.kind === 'built-in' && isPlainObject(page.definition)
    ? page.definition
    : null;
  const views = definition?.views ?? page.views;
  if (!Array.isArray(views)) return page;
  let unresolvedCount = 0;
  const resolvedViews = views.map((view) => {
    if (typeof view !== 'string') return view;
    const resolved = state.declaredViews.get(view);
    if (resolved === undefined) unresolvedCount += 1;
    return resolved ?? view;
  });
  debugValidatorState({
    operation: 'resolve-reusable-page-views',
    pageKind: definition ? 'built-in' : 'custom',
    viewCount: views.length,
    unresolvedCount
  });
  return definition
    ? { ...page, definition: { ...definition, views: resolvedViews } }
    : { ...page, views: resolvedViews };
}
