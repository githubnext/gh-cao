import { createHash } from "node:crypto";
import databaseQueries from "../site/src/data/queries/database.json" with { type: "json" };
import { queryInputNames, resolveDashboardQuerySources } from "../site/src/data/queries/declarative.js";
import { findDeadDashboardQueries } from "../site/src/query-usage.js";
import { dashboardViewSourceNames } from "../site/src/view-filter-contract.js";

/** @typedef {import("../site/src/dashboard-chunks.js").DashboardDocument['dashboard'] & { navigation?: Array<{ pages?: string[] } & Record<string, unknown>> }} DashboardConfig */
/** @typedef {{ pages: Array<Record<string, any>>, queries: Array<Record<string, any>>, views?: Array<Record<string, any>>, ['cli-actions']: Array<Record<string, any>> } & Record<string, any>} CompositionDashboard */

/**
 * Freeze both consumers and the production source contracts. Even a new view on
 * an excluded page needs an explicit audit before it can enter this profile.
 * @param {{ dashboard: Record<string, any> }} document
 */
export function immediateDashboardAuditFingerprint(document) {
  return createHash("sha256").update(JSON.stringify({
    dashboard: document.dashboard,
    databaseQueries
  })).digest("hex");
}

/**
 * @param {{ dashboard: DashboardConfig, languageVersion?: string, ['language-version']?: string }} document
 * @param {Record<string, any>} profile
 * @returns {{ ['language-version']: string, dashboard: DashboardConfig }}
 */
export function composeImmediateDashboard(document, profile) {
  if (profile["audited-dashboard-sha256"] !== immediateDashboardAuditFingerprint(document)) {
    throw new Error("Immediate dashboard inventory changed: audit every page, view, query and CLI action before updating audited-dashboard-sha256.");
  }
  const {
    ["audited-dashboard-sha256"]: _audit,
    ["include-experimental-pages"]: includeExperimental = false,
    ["query-source-aliases"]: aliases = {},
    ["excluded-pages"]: excludedPages = [],
    ["supported-stores"]: supportedStores = [],
    ["supported-external-sources"]: supportedExternalSources = [],
    ["included-cli-actions"]: includedActions = [],
    ["cli-action-overrides"]: actionOverrides = {},
    ["page-overrides"]: pageOverrides = {},
    ["view-overrides"]: viewOverrides = {},
    ["imported-queries"]: importedQueries = [],
    queries: overrides = [],
    ...presentation
  } = profile;
  const dashboard = /** @type {CompositionDashboard} */ (structuredClone(document.dashboard));
  Object.assign(dashboard, presentation);
  const excluded = new Set(excludedPages);
  const actions = new Set(includedActions);
  dashboard.pages = dashboard.pages.filter((page) => !excluded.has(page.id));
  for (const page of dashboard.pages) {
    if (includeExperimental && page.experimental === true) page.experimental = false;
    Object.assign(page, structuredClone(pageOverrides[page.id] ?? {}));
    const definition = /** @type {{ views: any[] }} */ (page.definition ?? page);
    definition.views = definition.views.map((view) => {
      if (typeof view === "string") return view;
      return { ...view, ...structuredClone(viewOverrides[`${page.id}/${view.id}`] ?? {}) };
    });
  }
  if (includeExperimental && Array.isArray(dashboard.navigation)) {
    for (const section of dashboard.navigation) {
      if (section.experimental === true) section.experimental = false;
    }
  }
  for (const view of [
    ...(dashboard.views ?? []),
    ...dashboard.pages.flatMap((page) => /** @type {any[]} */ ((page.definition ?? page).views))
  ]) {
    if (typeof view !== "object" || view === null) continue;
    if (view.data) {
      for (const key of ["source", "partial-source"]) {
        if (aliases[view.data[key]]) view.data[key] = aliases[view.data[key]];
      }
      if (Array.isArray(view.data.sources)) {
        view.data.sources = view.data.sources.map((/** @type {string} */ name) => aliases[name] ?? name);
      }
    }
    for (const filter of view["filter-bar"]?.filters ?? []) {
      for (const group of filter.groups ?? []) group.source = aliases[group.source] ?? group.source;
    }
  }
  dashboard["cli-actions"] = dashboard["cli-actions"]
    .filter((action) => actions.has(action.id))
    .map((action) => ({ ...action, ...structuredClone(actionOverrides[action.id] ?? {}) }));
  dashboard.queries = (document.dashboard.queries ?? []).map((query) => aliasQuerySources(query, aliases));
  for (const imported of importedQueries) {
    const source = databaseQueries.find((query) => query.name === imported.source);
    if (!source) throw new Error(`Unknown immediate query import: ${imported.source}`);
    const { stores: _stores, ...query } = source;
    const {
      source: _source,
      ["excluded-computed-fields"]: excludedComputed = [],
      ["prepend-computed-fields"]: prependComputed = [],
      ["computed-field-overrides"]: computedOverrides = {},
      ...metadata
    } = imported;
    const importedQuery = /** @type {Record<string, any>} */ ({
      ...query,
      ...metadata,
      name: imported.name,
      subject: imported.subject,
      objective: imported.objective,
      acceptance: imported.acceptance
    });
    if (Array.isArray(importedQuery.compute)) {
      importedQuery.compute = [
        ...prependComputed,
        .../** @type {Array<Record<string, any>>} */ (importedQuery.compute)
          .filter((computed) => !excludedComputed.includes(computed.as))
          .map((computed) => computedOverrides[computed.as] ?? computed)
      ];
    }
    dashboard.queries.push(aliasQuerySources(importedQuery, aliases));
  }
  delete dashboard.repository;
  for (const override of overrides) {
    const index = dashboard.queries.findIndex((query) => query.name === override.name);
    if (index < 0) dashboard.queries.push(structuredClone(override));
    else {
      const query = { ...dashboard.queries[index], ...structuredClone(override) };
      for (const key of Object.keys(override)) if (override[key] === null) delete query[key];
      dashboard.queries[index] = query;
    }
    dashboard.queries = orderQueries(dashboard.queries);
  }
  pruneReferences(dashboard, excluded, actions);
  const reusable = new Set(dashboard.pages.flatMap((page) =>
    /** @type {any[]} */ ((page.definition ?? page).views).filter((view) => typeof view === "string")));
  dashboard.views = (dashboard.views ?? []).filter((view) => reusable.has(view.id));
  const unused = new Set(findDeadDashboardQueries(dashboard).map((query) => query.name));
  dashboard.queries = dashboard.queries.filter((query) => !unused.has(query.name));
  assertSupportedSources(dashboard, new Set(supportedStores), new Set(supportedExternalSources));
  const languageVersion = document.languageVersion ?? document["language-version"] ?? "0.1.0";
  return { "language-version": languageVersion, dashboard: /** @type {DashboardConfig} */ (dashboard) };
}

/** @param {Array<Record<string, any>>} queries */
function orderQueries(queries) {
  const index = new Map(queries.map((query) => [query.name, query]));
  /** @type {Array<Record<string, any>>} */
  const ordered = [];
  const visited = new Set();
  const visiting = new Set();
  /** @param {string} name */
  const visit = (name) => {
    if (visited.has(name) || !index.has(name)) return;
    if (visiting.has(name)) throw new Error(`Cyclic immediate query: ${name}`);
    visiting.add(name);
    const query = index.get(name);
    if (!query) return;
    for (const input of queryInputNames(/** @type {import("../site/src/data/queries/declarative.js").DashboardQuery} */ (query))) visit(input);
    visiting.delete(name);
    visited.add(name);
    ordered.push(query);
  };
  for (const query of queries) visit(query.name);
  return ordered;
}

/**
 * Resolve through the same declarative dependency graph used by production.
 * Database query `stores` describe canonical reads, including internal sources.
 * @param {DashboardConfig | CompositionDashboard} dashboard
 * @param {string[]} requested
 */
export function immediateDashboardSourceDependencies(dashboard, requested) {
  const definitions = [...databaseQueries, ...(dashboard.queries ?? [])];
  const names = new Set(definitions.map((query) => query.name));
  const resolved = resolveDashboardQuerySources(definitions, requested);
  return {
    sources: resolved,
    stores: [...new Set(resolved.flatMap((name) =>
      databaseQueries.find((query) => query.name === name)?.stores ?? []))].sort(),
    external: resolved.filter((name) => !names.has(name) && !name.startsWith("$")).sort()
  };
}

/** @param {CompositionDashboard} dashboard @param {Set<string>} stores @param {Set<string>} external */
function assertSupportedSources(dashboard, stores, external) {
  const requested = [
    ...dashboard.queries.map((query) => query.name),
    ...dashboard.pages.flatMap((page) => /** @type {any[]} */ ((page.definition ?? page).views).flatMap((view) =>
      typeof view === "string" ? [] : dashboardViewSourceNames(view)))
  ];
  const dependencies = immediateDashboardSourceDependencies(dashboard, requested);
  const unsupported = [
    ...dependencies.stores.filter((name) => !stores.has(name)),
    ...dependencies.external.filter((name) => !external.has(name))
  ];
  if (unsupported.length > 0) {
    throw new Error(`Unsupported immediate dashboard sources: ${unsupported.join(", ")}`);
  }
}

/** @param {Record<string, any>} query @param {Record<string, string>} aliases */
function aliasQuerySources(query, aliases) {
  const result = structuredClone(query);
  if (typeof query.from === "string" && aliases[query.from]) result.from = aliases[query.from];
  if (Array.isArray(query.union)) result.union = query.union.map((name) => aliases[name] ?? name);
  if (Array.isArray(query.joins)) {
    result.joins = query.joins.map((join) => ({
      ...structuredClone(join),
      source: aliases[join.source] ?? join.source
    }));
  }
  return result;
}

/**
 * Prune declarative navigation and action references together with their targets.
 * A removed destination in a query expression becomes a missing link, not an
 * invented replacement route. Other evidence columns remain intact.
 * @param {Record<string, any>} value
 * @param {Set<string>} excluded
 * @param {Set<string>} actions
 */
function pruneReferences(value, excluded, actions) {
  const omittedHash = (/** @type {unknown} */ text) => typeof text === "string"
    && [...text.matchAll(/#page-([a-z0-9-]+)/g)].some((match) => excluded.has(match[1]));
  for (const [key, child] of Object.entries(value)) {
    if (["page", "navigation-page"].includes(key) && excluded.has(child)) {
      delete value[key];
    } else if (Array.isArray(child)) {
      value[key] = child.filter((entry) => {
        if (key === "pages" && typeof entry === "string") return !excluded.has(entry);
        if (key === "actions" && typeof entry === "string") return actions.has(entry);
        if (!entry || typeof entry !== "object") return true;
        if (excluded.has(entry.page) || excluded.has(entry["navigation-page"])) return false;
        if (typeof entry.action === "string" && !actions.has(entry.action)) return false;
        return !(key === "navigation" && Array.isArray(entry.pages)
          && entry.pages.every((/** @type {string} */ page) => excluded.has(page)));
      });
      for (const entry of value[key]) {
        if (!entry || typeof entry !== "object") continue;
        if (key === "compute" && Array.isArray(entry.args)
            && entry.args.some((/** @type {{ value?: unknown }} */ arg) => omittedHash(arg.value))) {
          entry.function = "literal";
          entry.args = [{ value: null }];
        } else pruneReferences(entry, excluded, actions);
      }
      if (["actions", "callouts"].includes(key) && value[key].length === 0) delete value[key];
    } else if (child && typeof child === "object") {
      if (excluded.has(child.page) || excluded.has(child["navigation-page"])
          || omittedHash(child.href)) delete value[key];
      else pruneReferences(child, excluded, actions);
    }
  }
}
