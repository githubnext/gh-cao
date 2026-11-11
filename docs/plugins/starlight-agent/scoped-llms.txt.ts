import type { APIRoute } from "astro";
import { getCollection } from "astro:content";
import { starlightAgentConfig } from "virtual:starlight-agent/config";
import { normalizedBase } from "./resource";

export const prerender = true;

export const GET: APIRoute = async ({ site }) => {
  if (!site) return new Response("Astro site URL is required", { status: 500 });
  const base = new URL(normalizedBase(import.meta.env.BASE_URL), site);
  const entries = (await getCollection(
    "docs",
    (entry) => !entry.data.draft && entry.data.agent?.prominent === true,
  )).sort((left, right) => left.data.title.localeCompare(right.data.title));
  const resources = entries.map((entry) => {
    const route = entry.id === "index" ? "" : `${entry.id}/`;
    const jsonRoute = entry.id === "index" ? "index.json" : `${entry.id}/index.json`;
    return [
      `- [${entry.data.title}](${new URL(route, base)})`,
      `  - [Machine-readable metadata](${new URL(jsonRoute, base)})`,
      `  - ${entry.data.description ?? "Authoritative project documentation."}`,
    ].join("\n");
  });
  const body = [
    `# ${starlightAgentConfig.projectName} agent resources`,
    "",
    `> ${starlightAgentConfig.description}`,
    "",
    "Use these routes to navigate the documentation and dashboard architecture without executing client-side JavaScript. Each HTML documentation page advertises a compact JSON representation containing stable identity, relationships, provenance, and freshness.",
    "",
    `- [Complete machine-readable resource index](${new URL(`${starlightAgentConfig.scopePath}/resources.json`, base)})`,
    "",
    "## Important resources",
    "",
    ...resources,
    "",
    "## Deeper access",
    "",
    `- [Agent analysis](${new URL("agent-analysis/", base)}): use the CAO CLI or read-only MCP server for large operational datasets and named Dashboard Language queries.`,
    `- [Complete documentation](${new URL("llms-full.txt", base)}): use only when the compact routes do not answer the task.`,
    "",
  ].join("\n");
  return new Response(body, {
    headers: { "Content-Type": "text/plain; charset=utf-8" },
  });
};
