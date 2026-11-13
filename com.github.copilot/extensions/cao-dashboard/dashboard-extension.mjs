// @ts-check

import {
  executeDashboardQueryRequest,
  readDashboardDataSpecification,
} from "./dashboard-agent-tools.mjs";

export function createDashboardExtension() {
  /** @type {import("@github/copilot-sdk/extension").JoinSessionConfig} */
  const config = {
    tools: [
      {
        name: "cao_dashboard_execute_query",
        description:
          "Execute declarative Dashboard Language queries with the canonical CAO dashboard query engine against supplied logical source rows.",
        parameters: {
          type: "object",
          properties: {
            queries: {
              type: "array", minItems: 1,
              description: "Dashboard Language query definitions in dependency order.",
              items: { type: "object" },
            },
            sources: {
              type: "object", minProperties: 1,
              description: "Logical sources keyed by source name. Each value may be a row array or an object containing rows and optional metadata.",
              additionalProperties: {
                oneOf: [
                  { type: "array", items: { type: "object" } },
                  {
                    type: "object",
                    properties: {
                      source: { type: "string" },
                      rows: { type: "array", items: { type: "object" } },
                      metadata: { type: "object" },
                    },
                    required: ["rows"], additionalProperties: true,
                  },
                ],
              },
            },
            requested: {
              type: "array", uniqueItems: true, items: { type: "string" },
              description: "Optional query names to execute. Dependencies are resolved automatically.",
            },
          },
          required: ["queries", "sources"], additionalProperties: false,
        },
        handler: async (input) => executeDashboardQueryRequest({ ...input }),
      },
      {
        name: "cao_dashboard_read_data_specification",
        description: "Read a numbered line range from the canonical CAO dashboard data architecture specification.",
        parameters: {
          type: "object",
          properties: {
            startLine: {
              type: "integer", minimum: 1,
              description: "First line to read. Defaults to 1.",
            },
            endLine: {
              type: "integer", minimum: 1,
              description: "Last line to read, inclusive. Defaults to 200 lines after startLine and is limited to 400 lines per call.",
            },
          },
          additionalProperties: false,
        },
        handler: async (input) => readDashboardDataSpecification({ ...input }),
      },
    ],
    hooks: {
      onSessionStart: async () => ({
        additionalContext:
          "For CAO dashboard data-model or query work, read the relevant sections of the canonical dashboard data specification with cao_dashboard_read_data_specification before making architectural assumptions. Use cao_dashboard_execute_query to evaluate Dashboard Language queries instead of reimplementing query semantics.",
      }),
    },
  };
  return { config };
}
