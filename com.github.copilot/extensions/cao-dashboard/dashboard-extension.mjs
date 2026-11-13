// @ts-check

import {
  CanvasError,
  createCanvas,
} from "@github/copilot-sdk/extension";

import {
  executeDashboardQueryRequest,
  readDashboardDataSpecification,
} from "./dashboard-agent-tools.mjs";
import { startLocalDashboardPreview } from "./local-preview.mjs";

/**
 * @param {{
 *   workingDirectory?: string,
 *   startPreview?: typeof startLocalDashboardPreview,
 * }} [options]
 */
export function createDashboardExtension({
  workingDirectory = process.cwd(),
  startPreview = startLocalDashboardPreview,
} = {}) {
  /** @type {Map<string, ReturnType<typeof startLocalDashboardPreview>>} */
  const previews = new Map();
  /** @type {Set<Promise<void>>} */
  const closingInstances = new Set();
  let dashboardWorkingDirectory = workingDirectory;
  /** @type {Promise<void> | undefined} */
  let closingPreviews;
  let stopping = false;

  /** @param {ReturnType<typeof startLocalDashboardPreview>} pending */
  const closePreview = (pending) => {
    const closing = pending.then((preview) => preview.close());
    closingInstances.add(closing);
    const remove = () => { closingInstances.delete(closing); };
    void closing.then(remove, remove);
    return closing;
  };

  const closePreviews = () => {
    stopping = true;
    if (closingPreviews) return closingPreviews;
    const pendingPreviews = [...previews.values()];
    previews.clear();
    const cleanups = [...closingInstances, ...pendingPreviews.map(closePreview)];
    closingPreviews = (async () => {
      const results = await Promise.allSettled(cleanups);
      const errors = results
        .filter((result) => result.status === "rejected")
        .map((result) => result.reason);
      if (errors.length) {
        throw new AggregateError(errors, "Could not close CAO dashboard previews.");
      }
    })().finally(() => { closingPreviews = undefined; });
    return closingPreviews;
  };

  /** @type {import("@github/copilot-sdk/extension").JoinSessionConfig} */
  const config = {
    requestedEnvironmentVariables: ["GH_TOKEN", "GITHUB_TOKEN"],
    tools: [
      {
        name: "cao_dashboard_execute_query",
        description:
          "Execute declarative Dashboard Language queries with the canonical CAO dashboard query engine against supplied logical source rows.",
        parameters: {
          type: "object",
          properties: {
            queries: {
              type: "array",
              minItems: 1,
              description:
                "Dashboard Language query definitions in dependency order.",
              items: { type: "object" },
            },
            sources: {
              type: "object",
              minProperties: 1,
              description:
                "Logical sources keyed by source name. Each value may be a row array or an object containing rows and optional metadata.",
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
                    required: ["rows"],
                    additionalProperties: true,
                  },
                ],
              },
            },
            requested: {
              type: "array",
              uniqueItems: true,
              items: { type: "string" },
              description:
                "Optional query names to execute. Dependencies are resolved automatically.",
            },
          },
          required: ["queries", "sources"],
          additionalProperties: false,
        },
        handler: async (input) =>
          executeDashboardQueryRequest({
            ...input,
          }),
      },
      {
        name: "cao_dashboard_read_data_specification",
        description:
          "Read a numbered line range from the canonical CAO dashboard data architecture specification.",
        parameters: {
          type: "object",
          properties: {
            startLine: {
              type: "integer",
              minimum: 1,
              description: "First line to read. Defaults to 1.",
            },
            endLine: {
              type: "integer",
              minimum: 1,
              description:
                "Last line to read, inclusive. Defaults to 200 lines after startLine and is limited to 400 lines per call.",
            },
          },
          additionalProperties: false,
        },
        handler: async (input) =>
          readDashboardDataSpecification({
            ...input,
          }),
      },
    ],
    hooks: {
      onSessionStart: async (input) => {
        dashboardWorkingDirectory = input.workingDirectory;
        return {
          additionalContext:
            "For CAO dashboard data-model or query work, read the relevant sections of the canonical dashboard data specification with cao_dashboard_read_data_specification before making architectural assumptions. Use cao_dashboard_execute_query to evaluate Dashboard Language queries instead of reimplementing query semantics.",
        };
      },
      onUserPromptSubmitted: async (input) => {
        dashboardWorkingDirectory = input.workingDirectory;
      },
      onSessionEnd: async ({ reason }) => {
        if (reason === "user_exit") await closePreviews();
      },
    },
    canvases: [
      createCanvas({
        id: "cao-dashboard",
        displayName: "Central Agentic Ops",
        description:
          "Open the local gh cao dashboard for the current or a specified repository.",
        inputSchema: {
          type: "object",
          properties: {
            repository: {
              type: "string",
              pattern: "^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$",
              maxLength: 200,
              description:
                "Optional GitHub repository in OWNER/REPOSITORY format. Defaults to the current repository.",
            },
          },
          additionalProperties: false,
        },
        open: async (context) => {
          let pending;
          try {
            if (stopping) {
              throw new Error("The CAO dashboard extension is shutting down.");
            }
            dashboardWorkingDirectory =
              context.session?.workingDirectory ?? dashboardWorkingDirectory;
            pending = previews.get(context.instanceId);
            if (!pending) {
              const previewWorkingDirectory = dashboardWorkingDirectory;
              const input = context.input;
              pending = startPreview({
                workingDirectory: previewWorkingDirectory,
                repository: input && typeof input === "object" && !Array.isArray(input)
                  && typeof input.repository === "string" ? input.repository : undefined,
              });
              previews.set(context.instanceId, pending);
            }
            const preview = await pending;
            return {
              title: "Central Agentic Ops",
              status: "gh cao dashboard",
              url: preview.url,
            };
          } catch (error) {
            if (previews.get(context.instanceId) === pending) {
              previews.delete(context.instanceId);
            }
            throw new CanvasError(
              "cao_dashboard_preview_unavailable",
              error instanceof Error
                ? error.message
                : "Could not start the local CAO dashboard preview.",
            );
          }
        },
        onClose: async (context) => {
          const pending = previews.get(context.instanceId);
          if (!pending) return;

          previews.delete(context.instanceId);
          await closePreview(pending);
        },
      }),
    ],
  };
  return { config, closePreviews };
}
