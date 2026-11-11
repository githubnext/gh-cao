import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readdir, readFile, stat } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { loadAgentDashboardDocument } from "../activity/agent-catalog.mjs";
import { commandHandlers } from "../activity/commands/index.mjs";
import { MCP_TOOLS } from "../activity/mcp-server.mjs";
import { webMCPManifestForDashboard } from "../dashboard/site/src/webmcp/manifest.js";

const artifactNames = ["llms.txt", "llms-small.txt", "llms-full.txt"];
const scopedIndexPath = "agent/llms.txt";
const resourceIndexPath = "agent/resources.json";
const dashboardAgentGuide = "https://githubnext.github.io/gh-cao/agent-analysis/";
const artifactSizeLimits = {
  "llms.txt": 4 * 1024,
  "llms-small.txt": 64 * 1024,
};
export const MAX_SKILL_ENTRY_BYTES = 6 * 1024;
const commonTaskRoutes = [
  ["Set up a CAO control plane", "setup-cao"],
  ["Debug a CAO failure", "debug-cao"],
  ["Add an existing campaign", "add-cao-campaign"],
  ["Create a new campaign", "create-cao-campaign"],
  ["Analyze CAO activity", "analyze-cao"],
  ["Use or extend the CAO CLI", "cao-cli"],
];
const requiredSkills = commonTaskRoutes.map(([, skill]) => skill);
const demotedHeadings = [
  "Activity cache compression analysis",
  "Weekly rhythm",
  "Dashboard view catalog",
];
const requiredResourceTypes = [
  "overview",
  "architecture",
  "campaign-guide",
  "cli-reference",
  "data-collection",
  "dashboard",
  "data-model",
  "query-language",
  "operations",
];
const credentialPatterns = [
  /-----BEGIN [A-Z ]*PRIVATE KEY-----/,
  /\bgh[pousr]_[A-Za-z0-9_]{20,}\b/,
  /\bgithub_pat_[A-Za-z0-9_]{20,}\b/,
  /\bBearer\s+[A-Za-z0-9._~+/=-]{20,}\b/i,
];
const developmentPathPatterns = [
  /\bfile:\/\//i,
  /\/home\/runner\/work\//,
  /\/Users\/[^/\s]+/,
  /\b[A-Za-z]:\\(?:Users|Windows|Temp)\\/,
  /https?:\/\/(?:localhost|127\.0\.0\.1)(?::\d+)?\//i,
  /\/tmp\/(?:astro|npm|runner|vite)[^)\s]*/i,
];

function markdownLinks(text) {
  return [...text.matchAll(/\[[^\]]+\]\(([^)\s]+)(?:\s+"[^"]*")?\)/g)]
    .map((match) => match[1]);
}

function builtPathForUrl(distDirectory, url) {
  const relativePath = url.pathname.slice("/gh-cao/".length);
  if (!relativePath || relativePath.endsWith("/")) {
    return path.join(distDirectory, relativePath, "index.html");
  }
  return path.join(distDirectory, relativePath);
}

async function isRegularNonemptyFile(filePath) {
  try {
    const metadata = await stat(filePath);
    if (!metadata.isFile() || metadata.size === 0) return false;
    return Boolean((await readFile(filePath, "utf8")).trim());
  } catch {
    return false;
  }
}

async function findResourceFiles(directory) {
  const files = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const filePath = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...await findResourceFiles(filePath));
    } else if (entry.name === "index.json") {
      files.push(filePath);
    }
  }
  return files;
}

async function findHtmlFiles(directory) {
  const files = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const filePath = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...await findHtmlFiles(filePath));
    } else if (entry.name === "index.html") {
      files.push(filePath);
    }

  }
  return files;
}

async function findMarkdownFiles(directory) {
  const files = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const filePath = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...await findMarkdownFiles(filePath));
    } else if (entry.name.endsWith(".md")) {
      files.push(filePath);
    }
  }
  return files;
}

function validateMcpArguments(tool, argumentsValue) {
  if (!tool || !argumentsValue || typeof argumentsValue !== "object" || Array.isArray(argumentsValue)) {
    return false;
  }
  const schema = tool.inputSchema ?? {};
  const properties = schema.properties ?? {};
  const required = Array.isArray(schema.required) ? schema.required : [];
  if (required.some((name) => !Object.hasOwn(argumentsValue, name))) return false;
  if (schema.additionalProperties === false
    && Object.keys(argumentsValue).some((name) => !Object.hasOwn(properties, name))) {
    return false;
  }
  return Object.entries(argumentsValue).every(([name, value]) => {
    const property = properties[name];
    if (!property) return schema.additionalProperties !== false;
    if (Array.isArray(property.enum) && !property.enum.includes(value)) return false;
    if (property.type === "string" && typeof value !== "string") return false;
    if (property.type === "integer" && !Number.isInteger(value)) return false;
    if (property.type === "object"
      && (typeof value !== "object" || value === null || Array.isArray(value))) return false;
    return true;
  });
}

export async function validateAgentDocs({
  root,
  trackedFiles = [],
  injectedSecretValues = [],
  expectedCommit,
}) {
  const errors = [];
  const distDirectory = path.join(root, "dist");
  const artifacts = {};

  for (const artifactName of artifactNames) {
    const artifactPath = path.join(distDirectory, artifactName);
    if (!await isRegularNonemptyFile(artifactPath)) {
      errors.push(`${artifactName} must exist as a non-empty regular file`);
      continue;
    }
    artifacts[artifactName] = await readFile(artifactPath, "utf8");
  }

  const index = artifacts["llms.txt"];
  const small = artifacts["llms-small.txt"];
  const full = artifacts["llms-full.txt"];
  const scopedPath = path.join(distDirectory, scopedIndexPath);
  const resourceIndexFile = path.join(distDirectory, resourceIndexPath);
  let scoped;
  if (!await isRegularNonemptyFile(scopedPath)) {
    errors.push(`${scopedIndexPath} must exist as a non-empty regular file`);
  } else {
    scoped = await readFile(scopedPath, "utf8");
  }
  let resourceIndex;
  if (!await isRegularNonemptyFile(resourceIndexFile)) {
    errors.push(`${resourceIndexPath} must exist as a non-empty regular file`);
  } else {
    try {
      resourceIndex = JSON.parse(await readFile(resourceIndexFile, "utf8"));
    } catch {
      errors.push(`${resourceIndexPath} is not valid JSON`);
    }
  }

  if (index) {
    const links = markdownLinks(index);
    if (!links.includes("https://githubnext.github.io/gh-cao/agent/llms.txt")) {
      errors.push("llms.txt must route directly to the scoped agent resource index");
    }
    if (!links.includes("https://githubnext.github.io/gh-cao/agent/resources.json")) {
      errors.push("llms.txt must route directly to the machine-readable resource index");
    }
    if (!links.includes(dashboardAgentGuide)) {
      errors.push("llms.txt must route directly to the dashboard agent access guide");
    }
    for (const [task, skill] of commonTaskRoutes) {
      const expected = `https://github.com/githubnext/gh-cao/blob/main/skills/${skill}/SKILL.md`;
      const taskLine = index.split("\n").find((line) => line.includes(`[${task}]`));
      const skillLinks = taskLine
        ? markdownLinks(taskLine).filter((link) => /\/skills\/[^/]+\/SKILL\.md$/.test(link))
        : [];
      if (skillLinks.length !== 1 || skillLinks[0] !== expected) {
        errors.push(`llms.txt task "${task}" must route to only the ${skill} skill`);
      }
    }
    if (links.some((link) => /\/(?:specs|operational-value\/reports)\//.test(link))) {
      errors.push("llms.txt must not promote specifications or reports");
    }

    for (const link of links) {
      let url;
      try {
        url = new URL(link);
      } catch {
        errors.push(`llms.txt contains a non-absolute link: ${link}`);
        continue;
      }
      if (url.hostname !== "githubnext.github.io") continue;
      if (url.origin !== "https://githubnext.github.io" || !url.pathname.startsWith("/gh-cao/")) {
        errors.push(`llms.txt contains a non-canonical Pages URL: ${link}`);
        continue;
      }
      if (link !== dashboardAgentGuide
        && !await isRegularNonemptyFile(builtPathForUrl(distDirectory, url))) {
        errors.push(`llms.txt contains an unresolved internal link: ${link}`);
      }
    }
  }

  for (const [artifactName, limit] of Object.entries(artifactSizeLimits)) {
    const content = artifacts[artifactName];
    if (content && Buffer.byteLength(content) > limit) {
      errors.push(`${artifactName} exceeds the ${limit} byte routing/context limit`);
    }
  }
  if (small && full && Buffer.byteLength(small) >= Buffer.byteLength(full)) {
    errors.push("llms-small.txt must be smaller than llms-full.txt");
  }
  if (full) {
    for (const heading of demotedHeadings) {
      if (!full.includes(`# ${heading}`)) {
        errors.push(`llms-full.txt must retain demoted content: ${heading}`);
      }
    }
    try {
      for (const sourceFile of await findMarkdownFiles(path.join(root, "docs"))) {
        const source = await readFile(sourceFile, "utf8");
        if (/^draft:\s*true\s*$/m.test(source)) continue;
        const heading = source.match(/^#\s+(.+)$/m)?.[1]?.trim();
        if (heading && !full.includes(`# ${heading}`)) {
          errors.push(
            `llms-full.txt must retain authoritative document: ${
              path.relative(root, sourceFile).replaceAll("\\", "/")
            }`,
          );
        }
      }
    } catch (error) {
      errors.push(`unable to verify authoritative documentation coverage: ${error.message}`);
    }
  }

  for (const skill of requiredSkills) {
    const skillPath = path.join(root, "skills", skill, "SKILL.md");
    if (!await isRegularNonemptyFile(skillPath)) {
      errors.push(`missing routed skill: ${skill}`);
      continue;
    }
    const skillBytes = (await stat(skillPath)).size;
    if (skillBytes > MAX_SKILL_ENTRY_BYTES) {
      errors.push(`${skill}/SKILL.md exceeds the ${MAX_SKILL_ENTRY_BYTES} byte entry-point limit`);
    }
    if (skill !== "setup-cao") continue;
    const skillSource = await readFile(skillPath, "utf8");
    for (const link of markdownLinks(skillSource)) {
      if (/^(?:[a-z]+:|#)/i.test(link)) continue;
      const localPath = link.split("#", 1)[0];
      if (!localPath) continue;
      const resolvedPath = path.resolve(path.dirname(skillPath), localPath);
      if (!await isRegularNonemptyFile(resolvedPath)) {
        errors.push(`${skill}/SKILL.md references missing local file: ${link}`);
      }
    }
  }

  const resourceTypes = new Set();
  const generationTimes = new Set();
  const interfaceKinds = new Set();
  const resourceIds = new Set();
  const resourceInterfaces = new Map();
  const cliCommands = new Set(commandHandlers.keys());
  const cliMcpCapabilities = new Map(MCP_TOOLS.map((tool) => [tool.name, tool]));
  const dashboardDocument = await loadAgentDashboardDocument();
  const webMcpTools = new Map(
    webMCPManifestForDashboard(dashboardDocument).map((tool) => [tool.name, tool]),
  );
  let resourceFiles = [];
  try {
    resourceFiles = await findResourceFiles(distDirectory);
  } catch {
    errors.push("unable to enumerate generated resource metadata");
  }
  for (const resourceFile of resourceFiles) {
    const relative = path.relative(distDirectory, resourceFile).replaceAll("\\", "/");
    const content = await readFile(resourceFile, "utf8");
    if (Buffer.byteLength(content) > 64 * 1024) {
      errors.push(`${relative} exceeds the 64 KiB resource metadata limit`);
      continue;
    }
    let resource;
    try {
      resource = JSON.parse(content);
    } catch {
      errors.push(`${relative} is not valid JSON`);
      continue;
    }
    for (const field of ["id", "type", "title", "url"]) {
      if (typeof resource[field] !== "string" || !resource[field]) {
        errors.push(`${relative} is missing string field ${field}`);
      }
    }
    resourceIds.add(resource.id);
    resourceTypes.add(resource.type);
    if (!resource.provenance || resource.provenance.repository !== "githubnext/gh-cao") {
      errors.push(`${relative} is missing repository provenance`);
    }
    if (expectedCommit && resource.provenance?.commit !== expectedCommit) {
      errors.push(`${relative} does not identify the expected build commit`);
    }
    for (const field of ["generatedAt", "sourceCommittedAt", "dataUpdatedAt"]) {
      const value = resource.freshness?.[field];
      if (value !== undefined && (!Number.isFinite(Date.parse(value)) || !/Z$/.test(value))) {
        errors.push(`${relative} has an invalid ${field} timestamp`);
      }
    }
    if (!Number.isFinite(Date.parse(resource.freshness?.generatedAt))) {
      errors.push(`${relative} is missing a valid generatedAt timestamp`);
    } else {
      generationTimes.add(resource.freshness.generatedAt);
    }
    if (!resource.source
      || resource.source.repository !== "githubnext/gh-cao"
      || typeof resource.source.path !== "string"
      || typeof resource.source.url !== "string") {
      errors.push(`${relative} is missing canonical source metadata`);
    }
    if (resource.integrity?.algorithm !== "sha256"
      || !/^[0-9a-f]{64}$/.test(resource.integrity?.sourceDigest ?? "")) {
      errors.push(`${relative} is missing SHA-256 source integrity`);
    } else if (resource.source?.path) {
      try {
        const sourceBytes = await readFile(path.join(root, resource.source.path));
        const digest = createHash("sha256").update(sourceBytes).digest("hex");
        if (digest !== resource.integrity.sourceDigest) {
          errors.push(`${relative} source integrity does not match ${resource.source.path}`);
        }
      } catch {
        errors.push(`${relative} source path cannot be read: ${resource.source.path}`);
      }
    }
    for (const binding of resource.interfaces?.cli ?? []) {
      interfaceKinds.add("cli");
      if (binding.command !== "cao" || !cliCommands.has(binding.subcommand)) {
        errors.push(`${relative} references an unregistered CLI command`);
      }
      if (!Array.isArray(binding.arguments?.positional)
        || !binding.arguments?.options
        || typeof binding.arguments.options !== "object"
        || Array.isArray(binding.arguments.options)) {
        errors.push(`${relative} CLI binding must separate positional arguments and options`);
      }
      if (binding.readOnly !== true) {
        errors.push(`${relative} CLI binding must declare readOnly`);
      }
    }
    for (const binding of resource.interfaces?.mcp ?? []) {
      const kind = binding.transport === "web" ? "web-mcp" : "cli-mcp";
      interfaceKinds.add(kind);
      if (binding.transport === "cli" && !cliMcpCapabilities.has(binding.capability)) {
        errors.push(`${relative} references an unregistered CLI MCP capability`);
      } else if (binding.transport === "cli"
        && !validateMcpArguments(cliMcpCapabilities.get(binding.capability), binding.arguments)) {
        errors.push(`${relative} supplies invalid CLI MCP arguments`);
      }
      if (binding.transport === "web") {
        const tool = webMcpTools.get(binding.capability);
        if (!tool || tool.pageId !== binding.resourceId) {
          errors.push(`${relative} references an unregistered WebMCP capability`);
        }
      }
      if (binding.readOnly !== true) {
        errors.push(`${relative} MCP binding must declare readOnly`);
      }
    }
    if (resource.recommendedInterface) {
      const available = new Set([
        ...((resource.interfaces?.cli?.length ?? 0) > 0 ? ["cli"] : []),
        ...(resource.interfaces?.mcp ?? []).map((binding) =>
          binding.transport === "web" ? "web-mcp" : "cli-mcp"),
      ]);
      if (!available.has(resource.recommendedInterface.default)
        || !(resource.recommendedInterface.alternatives ?? []).every((kind) => available.has(kind))) {
        errors.push(`${relative} recommends an unavailable interface`);
      }
    }
    resourceInterfaces.set(resource.id, [
      ...((resource.interfaces?.cli?.length ?? 0) > 0 ? ["cli"] : []),
      ...((resource.interfaces?.mcp ?? []).some((binding) => binding.transport === "cli") ? ["cli-mcp"] : []),
      ...((resource.interfaces?.mcp ?? []).some((binding) => binding.transport === "web") ? ["web-mcp"] : []),
    ]);
    for (const link of resource.links ?? []) {
      let url;
      try {
        url = new URL(link.href);
      } catch {
        errors.push(`${relative} contains a non-absolute resource link`);
        continue;
      }
      if (url.hostname === "githubnext.github.io"
        && !await isRegularNonemptyFile(builtPathForUrl(distDirectory, url))) {
        errors.push(`${relative} contains an unresolved resource link: ${link.href}`);
      }
    }

    const htmlPath = relative === "index.json"
      ? path.join(distDirectory, "index.html")
      : path.join(distDirectory, path.dirname(relative), "index.html");
    if (await isRegularNonemptyFile(htmlPath)) {
      const html = await readFile(htmlPath, "utf8");
      const expectedAlternate = relative === "index.json"
        ? "/gh-cao/index.json"
        : `/gh-cao/${relative}`;
      if (!html.includes(`rel="describedby" href="/gh-cao/agent/llms.txt"`)) {
        errors.push(`${path.relative(distDirectory, htmlPath)} does not advertise the scoped llms.txt`);
      }
      if (!html.includes(`rel="index" type="application/json" href="/gh-cao/agent/resources.json"`)) {
        errors.push(`${path.relative(distDirectory, htmlPath)} does not advertise the resource index`);
      }
      if (!html.includes(`rel="alternate" type="application/json" href="${expectedAlternate}"`)) {
        errors.push(`${path.relative(distDirectory, htmlPath)} does not advertise ${expectedAlternate}`);
      }
    }
  }
  if (generationTimes.size > 1) {
    errors.push("structured resources must share one build-wide generatedAt timestamp");
  }
  for (const type of requiredResourceTypes) {
    if (!resourceTypes.has(type)) errors.push(`structured resources must include type ${type}`);
  }
  for (const kind of ["cli", "cli-mcp", "web-mcp"]) {
    if (!interfaceKinds.has(kind)) errors.push(`structured resources must expose ${kind} bindings`);
  }
  const advertisedResources = new Set();
  for (const htmlFile of await findHtmlFiles(distDirectory)) {
    const html = await readFile(htmlFile, "utf8");
    const alternate = html.match(/<link\b(?=[^>]*\brel="alternate")(?=[^>]*\btype="application\/json")[^>]*\bhref="([^"]+)"/)?.[1];
    if (!alternate) continue;
    let builtPath;
    try {
      builtPath = builtPathForUrl(distDirectory, new URL(alternate, "https://githubnext.github.io"));
    } catch {
      errors.push(`${path.relative(distDirectory, htmlFile)} advertises an invalid JSON resource`);
      continue;
    }
    advertisedResources.add(path.relative(distDirectory, builtPath).replaceAll("\\", "/"));
    if (!await isRegularNonemptyFile(builtPath)) {
      errors.push(`${path.relative(distDirectory, htmlFile)} advertises a missing JSON resource`);
    }
  }
  const generatedResources = new Set(
    resourceFiles.map((file) => path.relative(distDirectory, file).replaceAll("\\", "/")),
  );
  for (const relative of generatedResources) {
    if (!advertisedResources.has(relative)) {
      errors.push(`${relative} has no HTML route advertising it`);
    }
  }
  for (const relative of advertisedResources) {
    if (!generatedResources.has(relative)) {
      errors.push(`${relative} is advertised by HTML but missing from generated resources`);
    }
  }
  if (resourceIndex) {
    if (resourceIndex.schemaVersion !== "1" || !Array.isArray(resourceIndex.resources)) {
      errors.push(`${resourceIndexPath} must contain a versioned resource list`);
    } else {
      const ids = resourceIndex.resources.map((resource) => resource.id);
      if (new Set(ids).size !== ids.length) {
        errors.push(`${resourceIndexPath} contains duplicate resource identifiers`);
      }
      if (ids.join("\0") !== [...ids].sort((left, right) => left.localeCompare(right)).join("\0")) {
        errors.push(`${resourceIndexPath} resources must be sorted by identifier`);
      }
      if (ids.length !== resourceIds.size || ids.some((id) => !resourceIds.has(id))) {
        errors.push(`${resourceIndexPath} does not match generated resource routes`);
      }
      for (const summary of resourceIndex.resources) {
        if (typeof summary.agent !== "string"
          || !await isRegularNonemptyFile(builtPathForUrl(distDirectory, new URL(summary.agent)))) {
          errors.push(`${resourceIndexPath} contains an unresolved agent resource`);
          break;
        }
        if (JSON.stringify(summary.interfaces ?? []) !== JSON.stringify(resourceInterfaces.get(summary.id) ?? [])) {
          errors.push(`${resourceIndexPath} interface summary does not match ${summary.id}`);
        }
      }
    }
  }
  if (scoped) {
    for (const link of markdownLinks(scoped)) {
      const url = new URL(link);
      if (url.hostname === "githubnext.github.io"
        && link !== dashboardAgentGuide
        && !await isRegularNonemptyFile(builtPathForUrl(distDirectory, url))) {
        errors.push(`${scopedIndexPath} contains an unresolved link: ${link}`);
      }
    }
  }

  for (const [artifactName, content] of Object.entries({
    ...artifacts,
    ...(scoped ? { [scopedIndexPath]: scoped } : {}),
    ...(resourceIndex ? { [resourceIndexPath]: JSON.stringify(resourceIndex) } : {}),
  })) {
    for (const pattern of credentialPatterns) {
      if (pattern.test(content)) {
        errors.push(`${artifactName} contains credential-like content`);
        break;
      }
    }
    for (const secretValue of injectedSecretValues) {
      if (secretValue.length >= 12 && content.includes(secretValue)) {
        errors.push(`${artifactName} contains an injected secret value`);
        break;
      }
    }
    for (const link of markdownLinks(content)) {
      if (developmentPathPatterns.some((pattern) => pattern.test(link))) {
        errors.push(`${artifactName} contains a development-machine URL or path`);
        break;
      }
    }
  }

  for (const file of trackedFiles) {
    const normalized = file.replaceAll("\\", "/");
    if (
      /(^|\/)agent-index\.json$/.test(normalized)
      || /(^|\/)agent\/resources\.json$/.test(normalized)
      || /(^|\/)llms(?:-small|-full)?\.txt$/.test(normalized)
    ) {
      errors.push(`generated agent documentation must not be committed: ${normalized}`);
    }
  }

  return errors;
}

async function main() {
  const root = path.resolve(import.meta.dirname, "..");
  const trackedFiles = execFileSync("git", ["ls-files", "-z"], {
    cwd: root,
    encoding: "utf8",
  }).split("\0").filter(Boolean);
  const injectedSecretValues = Object.entries(process.env)
    .filter(([name]) => /(?:TOKEN|SECRET|PASSWORD|PRIVATE_KEY)$/i.test(name))
    .map(([, value]) => value)
    .filter(Boolean);
  const expectedCommit = process.env.PUBLIC_PAGES_BUILD_COMMIT?.trim()
    || execFileSync("git", ["rev-parse", "HEAD"], { cwd: root, encoding: "utf8" }).trim();
  const errors = await validateAgentDocs({
    root,
    trackedFiles,
    injectedSecretValues,
    expectedCommit,
  });
  if (errors.length > 0) {
    console.error(`Agent documentation validation failed:\n${errors.map((error) => `- ${error}`).join("\n")}`);
    process.exitCode = 1;
    return;
  }
  console.log("Agent documentation validation passed.");
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await main();
}
