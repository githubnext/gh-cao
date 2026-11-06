import { findDeadDashboardQueries } from "../site/src/query-usage.js";

/**
 * @typedef {import("../site/src/dashboard-chunks.js").DashboardDocument['dashboard'] & { navigation?: Array<{ pages?: string[] } & Record<string, unknown>> }} DashboardConfig
 * @param {{ dashboard: DashboardConfig, languageVersion?: string, ['language-version']?: string }} document
 * @param {Partial<DashboardConfig> & { ['query-source-aliases']?: Record<string, string>, ['excluded-pages']?: string[] }} profile
 */
export function composeImmediateDashboard(document, profile) {
  const { ["query-source-aliases"]: aliases = {}, ["excluded-pages"]: excludedPages = [], ...presentation } = profile;
  const dashboard = structuredClone(document.dashboard);
  Object.assign(dashboard, presentation);
  const excluded = new Set(excludedPages);
  dashboard.pages = dashboard.pages.filter((page) => !excluded.has(page.id ?? ""));
  if (Array.isArray(dashboard.navigation)) {
    dashboard.navigation = dashboard.navigation.map((section) => ({
      ...section,
      pages: (section.pages ?? []).filter((id) => !excluded.has(id))
    })).filter((section) => section.pages.length > 0);
  }
  if (Array.isArray(dashboard.callouts)) {
    dashboard.callouts = dashboard.callouts.filter((callout) =>
      typeof callout["navigation-page"] !== "string" || !excluded.has(callout["navigation-page"]));
    if (dashboard.callouts.length === 0) delete dashboard.callouts;
  }
  dashboard.queries = (document.dashboard.queries ?? []).map((query) => aliasQuerySources(query, aliases));
  delete dashboard.repository;
  for (const override of profile.queries ?? []) {
    const index = dashboard.queries.findIndex((query) => query.name === override.name);
    if (index < 0) dashboard.queries.push(structuredClone(override));
    else {
      const query = { ...dashboard.queries[index], ...structuredClone(override) };
      for (const key of Object.keys(override)) if (override[key] === null) delete query[key];
      dashboard.queries[index] = query;
    }
  }
  const unused = new Set(findDeadDashboardQueries(dashboard).map((query) => query.name));
  dashboard.queries = dashboard.queries.filter((query) => !unused.has(String(query.name)));
  const languageVersion = document.languageVersion ?? document["language-version"] ?? "0.1.0";
  return { "language-version": languageVersion, dashboard };
}

/**
 * @param {Record<string, unknown>} query
 * @param {Record<string, string>} aliases
 */
function aliasQuerySources(query, aliases) {
  const result = structuredClone(query);
  if (typeof query.from === "string" && aliases[query.from]) result.from = aliases[query.from];
  if (Array.isArray(query.joins)) {
    result.joins = query.joins.map((join) => {
      if (!join || typeof join !== "object" || Array.isArray(join)) return join;
      const copy = structuredClone(join);
      if (typeof copy.source === "string" && aliases[copy.source]) copy.source = aliases[copy.source];
      return copy;
    });
  }
  return result;
}
