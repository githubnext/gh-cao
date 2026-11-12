import { describe, expect, it } from "vitest";

import { authoritativeDashboard } from "../authoritative-dashboard.js";
import {
  agentCatalog,
  agentFacingPages,
  isAgentFacingPage,
  navigationPageIds,
  pageParameters,
  describePage,
  describeQuery,
  listPages,
  listQueries,
  queriesForPage,
  queryExecutionRequirements,
  queryParameters,
} from "../../src/agent/catalog.js";
import {
  NamedQueryError,
  resolveNamedQueryParameters,
} from "../../src/agent/query-executor.js";

const document = {
  dashboard: {
    navigation: [{ pages: ["insights", "configuration"] }],
    queries: [
      {
        name: "usage-base",
        subject: "Reuse shared usage stages.",
        from: "runs",
      },
      {
        name: "usage-by-workflow",
        subject: "Show observed AI Credit usage by workflow.",
        objective: "Investigate unusual usage.",
        acceptance: "Usage is explained.",
        description: "Usage grouped by workflow.",
        from: "usage-base",
      },
      {
        name: "configuration-policy",
        subject: "Show control-repository configuration.",
        from: "work-items",
      },
    ],
    views: [
      {
        id: "shared-usage",
        data: {
          source: "usage-by-workflow",
          arguments: [{ name: "repository", field: "repository" }],
        },
      },
    ],
    pages: [
      {
        id: "insights",
        title: "Insights",
        description: "Inspect usage.",
        views: ["shared-usage"],
      },
      {
        id: "configuration",
        title: "Configuration",
        views: [{ data: { source: "configuration-policy" } }],
      },
      {
        id: "hidden",
        title: "Hidden",
        views: [{ data: { source: "usage-by-workflow" } }],
      },
    ],
  },
};

describe("agent catalog", () => {
  it("discovers agent-facing pages deterministically", () => {
    const pages = listPages(document);
    expect(pages.map((page) => page.id)).toEqual(["insights", "configuration"]);
  });

  it("omits pages that are not reachable from navigation", () => {
    expect(listPages(document).some((page) => page.id === "hidden")).toBe(false);
  });

  it("relates pages to the queries they render", () => {
    expect(queriesForPage(document, "insights")).toEqual(["usage-by-workflow"]);
    expect(describePage(document, "insights")?.queries).toEqual([
      "usage-by-workflow",
    ]);
  });

  it("preserves query semantics and description for query-info", () => {
    const query = describeQuery(document, "usage-by-workflow");
    expect(query?.subject).toBe("Show observed AI Credit usage by workflow.");
    expect(query?.objective).toBe("Investigate unusual usage.");
    expect(query?.acceptance).toBe("Usage is explained.");
    expect(query?.description).toBe("Usage grouped by workflow.");
    expect(query?.["used-by-pages"]).toEqual(["insights"]);
  });

  it("exposes authored page contracts through the shared CLI and MCP catalog", () => {
    for (const page of listPages(authoritativeDashboard)) {
      const source = authoritativeDashboard.dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === page.id);
      expect(page).toMatchObject({
        intent: source.intent, subject: source.subject, objective: source.objective, acceptance: source.acceptance
      });
    }
  });

  it("derives query parameters from the views that bind them", () => {
    expect(queryParameters(document, "usage-by-workflow")).toEqual([
      { name: "repository", field: "repository" },
    ]);
    expect(queryParameters(document, "configuration-policy")).toEqual([]);
  });

  it("publishes the simulator form bounds for transitive named-query operands", () => {
    const growth = describeQuery(authoritativeDashboard, "simulator-database-size");
    expect(growth?.parameters).toHaveLength(5);
    expect(growth?.parameters.find((parameter) => parameter.name === "repositories")).toMatchObject({
      name: "repositories",
      type: "number",
      required: true,
      schema: { type: "number", minimum: 1, maximum: 100000, multipleOf: 1 }
    });
    expect(queryExecutionRequirements(authoritativeDashboard, "simulator-database-size")).toMatchObject({
      local: true, requirements: ["simulation-days"]
    });
    expect(listPages(authoritativeDashboard).some((page) => page.id === "simulators")).toBe(true);
  });

  it("resolves transitive source requirements", () => {
    const execution = queryExecutionRequirements(document, "usage-by-workflow");
    expect(execution.local).toBe(true);
    expect(execution.backend).toBe("postgres");
    expect(execution.requirements).toEqual(["runs"]);
  });

  it("classifies queries that cannot run against the local projection", () => {
    const execution = queryExecutionRequirements(
      document,
      "configuration-policy",
    );
    expect(execution.local).toBe(false);
    expect(execution.reason).toMatch(/work-items/);
  });

  it("reports unknown queries instead of guessing", () => {
    const execution = queryExecutionRequirements(document, "missing");
    expect(execution.local).toBe(false);
    expect(describeQuery(document, "missing")).toBe(null);
  });

  it("lists queries deterministically", () => {
    expect(listQueries(document).map((query) => query.id)).toEqual([
      "usage-base",
      "usage-by-workflow",
      "configuration-policy",
    ]);
  });

  it("exposes one catalog for every transport", () => {
    const catalog = agentCatalog(document);
    expect(Object.keys(catalog).sort()).toEqual([
      "dashboard",
      "pages",
      "queries",
    ]);
    expect(catalog.pages).toEqual(listPages(document));
    expect(catalog.queries).toEqual(listQueries(document));
  });
});

describe("named query parameters", () => {
  it("maps declared parameters onto their query fields", () => {
    expect(
      resolveNamedQueryParameters(document, "usage-by-workflow", {
        repository: "githubnext/gh-cao",
      }),
    ).toEqual([
      {
        name: "repository",
        field: "repository",
        value: "githubnext/gh-cao",
      },
    ]);
  });

  it("rejects undeclared parameters", () => {
    expect(() =>
      resolveNamedQueryParameters(document, "usage-by-workflow", {
        nope: "value",
      }),
    ).toThrow(NamedQueryError);
  });

  it("rejects oversized parameter values", () => {
    expect(() =>
      resolveNamedQueryParameters(document, "usage-by-workflow", {
        repository: "x".repeat(1024),
      }),
    ).toThrow(NamedQueryError);
  });

  it("rejects unknown query identifiers", () => {
    expect(() =>
      resolveNamedQueryParameters(document, "missing", { any: "value" }),
    ).toThrow(NamedQueryError);
  });
});

const structuralDocument = {
  dashboard: {
    id: "cao",
    title: "CAO",
    navigation: [
      { label: "Operate", pages: ["home"] },
      { label: "Analyze", pages: ["built-in", "", "unknown-page"] },
    ],
    queries: [
      { name: "runs-base", subject: "Read runs.", from: "runs" },
      { name: "drill-runs", subject: "Drill into runs.", from: "runs-base" },
      { name: "counted", subject: "Count runs.", from: "runs-base" },
      {
        name: "multi-source",
        subject: "Join two sources.",
        from: "runs-base",
        joins: [{ source: "counted", on: [] }],
      },
      { name: "detail", subject: "Report one repository.", from: "runs-base" },
      { name: "unused", subject: "Never rendered.", from: "audits" },
      { subject: "Anonymous queries are not catalogued.", from: "runs" },
    ],
    pages: [
      {
        id: "home",
        title: "Home",
        description: "Operate CAO.",
        views: [
          {
            id: "runs-list",
            data: { source: "multi-source" },
            list: { drill: { query: "drill-runs" } },
          },
          { data: { sources: ["runs-base"] } },
          { data: { source: "not-a-query" } },
        ],
      },
      {
        id: "home",
        title: "Duplicate home",
        views: [{ data: { source: "unused" } }],
      },
      {
        id: "built-in",
        kind: "built-in",
        title: "Built in",
        experimental: true,
        definition: {
          sections: [
            { "count-source": "counted" },
            { "count-sources": ["runs-base", "missing-source"] },
            "not-a-section",
          ],
        },
        form: {
          fields: [
            { id: "include-drafts", control: "checkbox", label: "Include drafts", default: true },
            { id: "window", control: "slider", label: "Window", min: 2, max: 30, step: 2, default: 7 },
            { id: "offset-window", control: "slider", label: "Offset", min: 1, max: 9, step: 2 },
            { id: "mode", control: "select", label: "Mode", options: [{ value: "review" }, { value: "live" }], default: "live" },
            { id: "mixed", control: "radio", label: "Mixed", options: [{ value: "a" }, { value: 2 }] },
            { id: "empty", control: "select", label: "Empty", options: [] },
            { id: "free", control: "text", label: "Free", default: "any" },
            { id: "unsupported", control: "color", label: "Unsupported" },
            { control: "text", label: "Unnamed" },
          ],
        },
      },
      {
        id: "repository",
        title: "Repository",
        route: { "hash-query-parameter": "repository" },
        views: [{ data: { source: "detail", "route-field": "repository" } }],
      },
      { id: "untitled", views: [{ data: { source: "unused" } }] },
    ],
  },
};

describe("agent catalog structure", () => {
  it("reads navigation page identifiers and ignores blank entries", () => {
    expect([...navigationPageIds(structuralDocument.dashboard.navigation)]).toEqual([
      "home",
      "built-in",
      "unknown-page",
    ]);
    expect([...navigationPageIds(undefined)]).toEqual([]);
    expect([...navigationPageIds([{ pages: "home" }])]).toEqual([]);
  });

  it("includes route-addressed detail pages that navigation does not list", () => {
    expect(listPages(structuralDocument).map((page) => page.id)).toEqual([
      "home",
      "built-in",
      "repository",
    ]);
  });

  it("excludes pages without an identifier or a title", () => {
    expect(isAgentFacingPage({ id: "x" })).toBe(false);
    expect(isAgentFacingPage({ title: "X" })).toBe(false);
    expect(isAgentFacingPage(/** @type {any} */ ("home"))).toBe(false);
  });

  it("keeps the first definition of a duplicated page identifier", () => {
    const pages = agentFacingPages(structuralDocument);
    expect(pages.filter((page) => page.id === "home")).toHaveLength(1);
    expect(describePage(structuralDocument, "home")?.title).toBe("Home");
  });

  it("relates a page to inline, shared-array, and drill-down queries", () => {
    expect(queriesForPage(structuralDocument, "home")).toEqual([
      "drill-runs",
      "multi-source",
      "runs-base",
    ]);
  });

  it("ignores view sources that are not declared queries", () => {
    expect(queriesForPage(structuralDocument, "home")).not.toContain("not-a-query");
  });

  it("relates a built-in page to its section count sources", () => {
    expect(queriesForPage(structuralDocument, "built-in")).toEqual([
      "counted",
      "runs-base",
    ]);
  });

  it("reports no queries for an unknown page", () => {
    expect(queriesForPage(structuralDocument, "unknown-page")).toEqual([]);
    expect(describePage(structuralDocument, "unknown-page")).toBe(null);
  });

  it("marks experimental pages without hiding them", () => {
    expect(describePage(structuralDocument, "built-in")?.experimental).toBe(true);
    expect(describePage(structuralDocument, "home")?.experimental).toBe(false);
  });

  it("derives a required string parameter from a page route", () => {
    expect(describePage(structuralDocument, "repository")?.parameters).toEqual([
      {
        name: "repository",
        required: true,
        description: "The repository this page reports on.",
        schema: { type: "string", description: "The repository this page reports on." },
      },
    ]);
  });

  it("derives query parameters from route fields", () => {
    expect(queryParameters(structuralDocument, "detail")).toEqual([
      { name: "repository", field: "repository" },
    ]);
  });

  it("maps declared form controls onto JSON Schema parameters", () => {
    const parameters = pageParameters(structuralDocument.dashboard.pages[2]);
    const schemas = Object.fromEntries(
      parameters.map((parameter) => [parameter.name, parameter.schema]),
    );
    expect(parameters.every((parameter) => parameter.required === false)).toBe(true);
    expect(schemas["include-drafts"]).toEqual({
      type: "boolean",
      description: "Include drafts",
      default: true,
    });
    expect(schemas.window).toEqual({
      type: "number",
      description: "Window",
      minimum: 2,
      maximum: 30,
      multipleOf: 2,
      default: 7,
    });
    expect(schemas.mode).toEqual({
      type: "string",
      description: "Mode",
      enum: ["review", "live"],
      default: "live",
    });
    expect(schemas.free).toEqual({ type: "string", description: "Free", default: "any" });
  });

  it("omits a slider step that a non-aligned minimum would misreport", () => {
    const schemas = Object.fromEntries(
      pageParameters(structuralDocument.dashboard.pages[2])
        .map((parameter) => [parameter.name, parameter.schema]),
    );
    expect(schemas["offset-window"]).toEqual({
      type: "number",
      description: "Offset",
      minimum: 1,
      maximum: 9,
    });
  });

  it("omits option lists that are empty or mixed, and unsupported controls", () => {
    const names = pageParameters(structuralDocument.dashboard.pages[2])
      .map((parameter) => parameter.name);
    expect(names).not.toContain("mixed");
    expect(names).not.toContain("empty");
    expect(names).not.toContain("unsupported");
    expect(names).toHaveLength(5);
  });

  it("reports the declared sources of a query", () => {
    expect(describeQuery(structuralDocument, "multi-source")?.sources).toEqual([
      "counted",
      "runs-base",
    ]);
  });

  it("resolves requirements through every declared source", () => {
    const execution = queryExecutionRequirements(structuralDocument, "multi-source");
    expect(execution.local).toBe(true);
    expect(execution.requirements).toEqual(["runs"]);
    expect(execution.missing).toEqual([]);
  });

  it("reports the pages that read each query", () => {
    expect(describeQuery(structuralDocument, "runs-base")?.["used-by-pages"]).toEqual([
      "built-in",
      "home",
    ]);
    expect(describeQuery(structuralDocument, "unused")?.["used-by-pages"]).toEqual([]);
  });

  it("skips query definitions without a name", () => {
    expect(listQueries(structuralDocument).some((query) => query.id === "")).toBe(false);
  });

  it("reports the dashboard identity once for every transport", () => {
    expect(agentCatalog(structuralDocument).dashboard).toEqual({
      id: "cao",
      title: "CAO",
    });
  });

  it("tolerates documents without a dashboard", () => {
    expect(listPages({})).toEqual([]);
    expect(listQueries(null)).toEqual([]);
    expect(agentCatalog("nope").dashboard).toEqual({ id: "", title: "" });
  });

  it("recomputes the catalog when a catalog collection is replaced", () => {
    const mutable = {
      dashboard: {
        navigation: [{ pages: ["one"] }],
        queries: [{ name: "q", subject: "Read runs.", from: "runs" }],
        pages: [{ id: "one", title: "One", views: [{ data: { source: "q" } }] }],
      },
    };
    expect(listPages(mutable).map((page) => page.id)).toEqual(["one"]);
    mutable.dashboard.pages = [
      { id: "one", title: "Renamed", views: [{ data: { source: "q" } }] },
    ];
    expect(describePage(mutable, "one")?.title).toBe("Renamed");
  });
});

describe("agent catalog over the authoritative dashboard", () => {
  it("catalogs the reviewed dashboard without throwing", () => {
    const catalog = agentCatalog(authoritativeDashboard);
    expect(catalog.pages.length).toBeGreaterThan(0);
    expect(catalog.queries.length).toBeGreaterThan(0);
    expect(catalog.dashboard.id).toBeTruthy();
  });

  it("gives every catalogued query a stable identifier and execution verdict", () => {
    const queries = listQueries(authoritativeDashboard);
    const ids = queries.map((query) => query.id);
    expect(new Set(ids).size).toBe(ids.length);
    expect(ids.every((id) => id.length > 0)).toBe(true);
    for (const query of queries) {
      expect(typeof query.execution.local).toBe("boolean");
      if (query.execution.local) expect(query.execution.backend).toBe("postgres");
      else expect(query.execution.reason).toBeTruthy();
    }
  });

  it("reports why a non-local query cannot run instead of failing at execution", () => {
    const blocked = listQueries(authoritativeDashboard)
      .filter((query) => !query.execution.local);
    expect(blocked.length).toBeGreaterThan(0);
    expect(blocked.every((query) => /does not provide/.test(String(query.execution.reason)))).toBe(true);
  });

  it("only relates pages to queries the document declares", () => {
    const declared = new Set(listQueries(authoritativeDashboard).map((query) => query.id));
    for (const page of listPages(authoritativeDashboard)) {
      for (const queryId of page.queries) expect(declared.has(queryId)).toBe(true);
    }
  });

  it("returns an identical catalog for repeated reads", () => {
    expect(agentCatalog(authoritativeDashboard)).toEqual(agentCatalog(authoritativeDashboard));
  });
});
