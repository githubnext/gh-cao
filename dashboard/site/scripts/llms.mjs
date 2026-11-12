import { access, readFile, stat, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const SKILLS = [
  ["Set up CAO", "setup-cao", "Bootstrap a control plane, repository scope, and authentication."],
  ["Diagnose CAO", "debug-cao", "Investigate policy, credential, workflow, Activity, dashboard, or safe-output failures."],
  ["Analyze CAO", "analyze-cao", "Choose an access path and analyze deployed Activity evidence."],
  ["Use the CAO CLI", "cao-cli", "Acquire canonical JSONL and query PostgreSQL."],
  ["Create a campaign", "create-cao-campaign", "Author a new governed orchestrator and worker campaign."],
  ["Add a campaign", "add-cao-campaign", "Discover, compare, and install an existing catalog campaign."],
];

const REQUIRED_SECTIONS = [
  "# Central Agentic Ops Dashboard",
  "## Choose the cheapest access path",
  "## Skills",
  "## Published Activity artifacts",
  "## Targeted queries with MCP",
  "## Bulk analysis with CAO CLI and PostgreSQL",
  "## Schema and freshness",
];

async function artifactStats(activityDataPath) {
  if (!activityDataPath) return {};
  const root = activityDataPath instanceof URL ? fileURLToPath(activityDataPath) : resolve(activityDataPath);
  const result = {};
  for (const name of ["inventory-sources.json", "payload-hashes.json"]) {
    try {
      result[name] = (await stat(join(root, name))).size;
    } catch (error) {
      if (error?.code !== "ENOENT") throw error;
    }
  }
  try {
    const manifest = JSON.parse(await readFile(join(root, "payload-hashes.json"), "utf8"));
    for (const phase of ["runs", "records"]) {
      const names = Object.keys(manifest).filter((name) => name.startsWith(`gh-aw-logs-${phase}/`));
      let bytes = 0;
      for (const name of names) {
        try {
          bytes += (await stat(join(root, name))).size;
        } catch (error) {
          if (error?.code !== "ENOENT") throw error;
        }
      }
      result[`gh-aw-logs-${phase}`] = { count: names.length, bytes };
    }
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
  }
  return result;
}

function size(value) {
  if (!Number.isFinite(value)) return "not available in this build";
  return `${value} bytes`;
}

export async function generateDashboardLlms({ outputPath, activityDataPath } = {}) {
  const stats = await artifactStats(activityDataPath);
  const runShards = stats["gh-aw-logs-runs"] ?? {};
  const recordShards = stats["gh-aw-logs-records"] ?? {};
  const raw = (name) => `https://raw.githubusercontent.com/githubnext/gh-cao/main/skills/${name}/SKILL.md`;
  const lines = [
    "# Central Agentic Ops Dashboard",
    "",
    "> Agent entry point for CAO dashboards. Local and hosted operational deployments use PostgreSQL. Pages is a synthetic-only demo, never an operational data source. This guide is not a data source or specification.",
    "",
    "- Human dashboard: [open /cao/](./)",
    "- Broader CAO documentation: [Central Agentic Ops documentation](../)",
    "- Agent analysis guide: [choose CLI or MCP](../agent-analysis/)",
    "",
    "## Choose the cheapest access path",
    "",
    "1. Read this file.",
    "2. For a procedure, read the matching raw skill.",
    "3. Discover a reviewed named query for the required metric.",
    "4. For one campaign, workflow, run, repository, or session, use the named-query MCP tools.",
    "5. For bulk analysis, acquire verified canonical JSONL and explicitly ingest it into provisioned PostgreSQL.",
    "6. Use the dashboard UI only for interactive human visualization; do not scrape it.",
    "",
    "## Skills",
    "",
    ...SKILLS.map(([title, name, description]) => `- [${title}](${raw(name)}): ${description}`),
    "",
    "## Published Activity artifacts",
    "",
    "- Operational artifacts are access-controlled inputs acquired from an explicitly selected authenticated Actions artifact or trusted direct `payload-hashes.json` URL. This guide does not declare a hosted export endpoint.",
    `- Inventory sources: canonical discovery envelopes; ${size(stats["inventory-sources.json"])}; freshness is described by source metadata, not file age.`,
    `- Payload hash manifest: name-to-SHA-256 mapping; ${size(stats["payload-hashes.json"])}; verifies inventory and canonical shard bytes, not source freshness.`,
    `- Normalized run shards: ${runShards.count ?? 0} files, ${size(runShards.bytes)}. Preserve observation clocks and unavailable coverage during explicit ingestion.`,
    `- Normalized record shards: ${recordShards.count ?? 0} files, ${size(recordShards.bytes)}. Use only for bounded canonical ingestion.`,
    "- Pages publishes generated demo data only, with no operational exports, agent summary or live services.",
    "",
    "## Targeted queries with MCP",
    "",
    "- Prefer MCP for one campaign, one run, recent failures for one workflow, or a specific repository, workflow, or session.",
    "- Call `cao_catalog` with `kind: pages` or `kind: queries` to discover reviewed dashboard queries.",
    "- Call `cao_query` with a discovered query `id`, declared `parameters`, and a bounded `limit`.",
    "- MCP exposes no raw SQL or writes. Preserve result `availability`, `completeness`, `freshness`, and `as-of` metadata.",
    "- Setup and transport details: [Agent analysis](../agent-analysis/).",
    "",
    "## Bulk analysis with CAO CLI and PostgreSQL",
    "",
    "1. Install or use the repository-local CAO CLI.",
    "2. Download a selected operational Actions snapshot: `gh run download RUN_ID --repo OWNER/CONTROL --name cao-activity-index --dir .cao`. Alternatively use `cao download --url https://SNAPSHOT-HOST/payload-hashes.json` with an explicitly configured operational endpoint.",
    "3. Configure `CAO_POSTGRES_URL` and an exact namespace, provision storage explicitly, and ingest the verified canonical JSONL with the Go server.",
    "4. Use native `gh cao query` or MCP reviewed named queries against that PostgreSQL namespace. Runtime reads do not bootstrap or repair missing storage.",
    "5. Use this route for bounded historical analysis, aggregates and multi-repository queries within the authorized scope.",
    "",
    "There is no database-file download, conversion, embedded backend or legacy fallback.",
    "",
    "## Schema and freshness",
    "",
    "- [Authoritative dashboard data model](https://github.com/githubnext/gh-cao/blob/main/specs/dashboard-data.md) defines canonical entities, storage, and freshness semantics.",
    "- [Dashboard data model guide](../dashboard-data-model/) summarizes repositories, workflows, runs, jobs, sessions, events, transactions, safe outputs, graders/evals, and campaign projections.",
    "- Named CLI/MCP query results carry `availability`, `completeness`, `freshness`, and `as-of`; do not treat missing, stale, partial, or unavailable evidence as zero.",
    "- The payload manifest binds the published snapshot by SHA-256. File age alone does not prove source freshness.",
    "",
  ];
  const output = `${lines.join("\n")}\n`;
  if (outputPath) await writeFile(outputPath, output);
  return output;
}

export async function validateDashboardAgentArtifacts({ sitePath } = {}) {
  const site = resolve(sitePath);
  const llms = await readFile(join(site, "llms.txt"), "utf8");
  if (!llms.trim()) throw new Error("dashboard llms.txt is empty");
  for (const section of REQUIRED_SECTIONS) {
    if (!llms.includes(section)) throw new Error(`dashboard llms.txt is missing section: ${section}`);
  }
  for (const [, name] of SKILLS) {
    if (!llms.includes(`https://raw.githubusercontent.com/githubnext/gh-cao/main/skills/${name}/SKILL.md`)) {
      throw new Error(`dashboard llms.txt is missing raw skill URL: ${name}`);
    }
  }

  for (const name of ["inventory-sources.json", "payload-hashes.json"]) {
    await access(join(site, name));
  }
  if (/(?:token|secret|password|private[-_ ]key)\s*[:=]\s*\S+/i.test(llms)) {
    throw new Error("dashboard llms.txt contains secret-like data");
  }
  if (!llms.includes("[Central Agentic Ops documentation](../)")) {
    throw new Error("dashboard llms.txt contains a base-path-unsafe local link");
  }
}

async function runCli() {
  const [command, firstPath, secondPath] = process.argv.slice(2);
  if (command === "generate" && firstPath) {
    await generateDashboardLlms({
      outputPath: firstPath,
      activityDataPath: secondPath,
    });
    return;
  }
  if (command === "validate" && firstPath) {
    await validateDashboardAgentArtifacts({
      sitePath: firstPath,
    });
    return;
  }
  throw new Error(
    "usage: node dashboard/site/scripts/llms.mjs "
    + "generate OUTPUT_PATH [ACTIVITY_DATA_PATH] | validate SITE_PATH",
  );
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  await runCli();
}
