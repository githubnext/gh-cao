import { dashboardPageAllSourceNames } from "../site/src/dashboard-chunks.js";
import { resolveDashboardQuerySources } from "../site/src/data/queries/declarative.js";

/**
 * @typedef {import("../site/src/dashboard-chunks.js").DashboardDocument['dashboard']} DashboardConfig
 * @param {{ dashboard: DashboardConfig, languageVersion?: string, ['language-version']?: string }} document
 * @param {DashboardConfig} profile
 */
export function composeImmediateDashboard(document, profile) {
  const dashboard = {
    ...profile,
    pages: profile.pages.map((page) => {
      const original = document.dashboard.pages.find((candidate) => candidate.id === page.id);
      return structuredClone(page.kind ? page : { ...original, ...page });
    }),
    queries: [...(document.dashboard.queries ?? [])],
  };
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
  const candidate = { languageVersion, dashboard };
  const requested = dashboard.pages.flatMap((page) => dashboardPageAllSourceNames(candidate, page.id ?? "", "all"));
  const names = new Set(resolveDashboardQuerySources(dashboard.queries, requested));
  dashboard.queries = dashboard.queries.filter((query) => typeof query.name === "string" && names.has(query.name));
  return { "language-version": languageVersion, dashboard };
}
