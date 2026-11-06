/**
 * @typedef {import("../site/src/dashboard-chunks.js").DashboardDocument['dashboard']} DashboardConfig
 * @param {{ dashboard: DashboardConfig, languageVersion?: string, ['language-version']?: string }} document
 * @param {Partial<DashboardConfig> & { ['query-source-aliases']?: Record<string, string> }} profile
 */
export function composeImmediateDashboard(document, profile) {
  const { ["query-source-aliases"]: aliases = {}, ...presentation } = profile;
  const dashboard = structuredClone(document.dashboard);
  Object.assign(dashboard, presentation);
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
