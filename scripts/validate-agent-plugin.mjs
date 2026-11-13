import { readFile, readdir, realpath, stat } from "node:fs/promises";
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import YAML from "yaml";
import { bundledResources } from "../com.github.copilot/extensions/cao-dashboard/bundled-resources.mjs";

const manifestFields = new Set([
  "$schema", "name", "version", "description", "author", "homepage",
  "repository", "license", "keywords", "extensions",
]);
const skillFields = new Set([
  "name", "description", "license", "compatibility", "metadata", "allowed-tools",
]);
const object = (value) => value !== null && typeof value === "object" && !Array.isArray(value);

export async function validateAgentPlugin(pluginRoot) {
  const root = await realpath(pluginRoot);
  const errors = [];
  const checked = new Set();
  const checkPath = async (path) => {
    let resolvedPath;
    try {
      resolvedPath = await realpath(path);
    } catch (error) {
      if (error?.code !== "ENOENT" && error?.code !== "ENOTDIR") throw error;
      errors.push(`${relative(root, path)}: missing package file`);
      return null;
    }
    const relativePath = relative(root, resolvedPath);
    if (isAbsolute(relativePath) || relativePath === ".." || relativePath.startsWith(`..${sep}`)) {
      errors.push(`${relative(root, path)}: package path resolves outside the plugin root`);
      return null;
    }
    return resolvedPath;
  };
  const manifestPath = await checkPath(join(root, "plugin.json"));
  if (manifestPath) {
    let manifest;
    try {
      manifest = JSON.parse(await readFile(manifestPath, "utf8"));
    } catch {
      errors.push("plugin.json: invalid JSON manifest");
    }
    if (!object(manifest)) {
      errors.push("plugin.json: manifest must be an object");
    } else {
      for (const field of Object.keys(manifest)) {
        if (!manifestFields.has(field)) errors.push(`plugin.json: unsupported field ${field}`);
      }
      if (manifest.$schema !== "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json") {
        errors.push("plugin.json: unsupported Agent Plugins schema");
      }
      if (typeof manifest.name !== "string" || manifest.name.length > 64
          || !/^(?!.*(?:--|\.\.))[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$/.test(manifest.name)) {
        errors.push("plugin.json: invalid plugin name");
      }
      for (const field of ["version", "description", "homepage", "repository", "license"]) {
        if (field in manifest && typeof manifest[field] !== "string") {
          errors.push(`plugin.json: ${field} must be a string`);
        }
      }
      if ("author" in manifest && (!object(manifest.author)
          || Object.entries(manifest.author).some(([key, value]) =>
            !["name", "email", "url"].includes(key) || typeof value !== "string"))) {
        errors.push("plugin.json: invalid author");
      }
      if ("keywords" in manifest && (!Array.isArray(manifest.keywords)
          || manifest.keywords.some((value) => typeof value !== "string"))) {
        errors.push("plugin.json: keywords must be strings");
      }
      if ("extensions" in manifest && (!object(manifest.extensions)
          || Object.values(manifest.extensions).some((value) => !object(value)))) {
        errors.push("plugin.json: extensions must map namespaces to objects");
      }
    }
  }

  const walk = async (directory) => {
    const resolvedDirectory = await checkPath(directory);
    if (!resolvedDirectory || checked.has(resolvedDirectory)) return;
    checked.add(resolvedDirectory);
    for (const entry of await readdir(resolvedDirectory)) {
      const path = join(resolvedDirectory, entry);
      const resolvedPath = await checkPath(path);
      if (!resolvedPath) continue;
      if ((await stat(resolvedPath)).isDirectory()) {
        await walk(path);
        continue;
      }
      if (!entry.endsWith(".md")) continue;
      const source = await readFile(resolvedPath, "utf8");
      const label = relative(root, path);
      if (entry === "SKILL.md") {
        const frontmatter = source.match(/^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/);
        let metadata;
        try {
          metadata = frontmatter ? YAML.parse(frontmatter[1]) : undefined;
        } catch {
          errors.push(`${label}: invalid YAML frontmatter`);
        }
        if (!object(metadata)) {
          errors.push(`${label}: skill frontmatter must be an object`);
        } else {
          for (const key of Object.keys(metadata)) {
            if (!skillFields.has(key)) errors.push(`${label}: unsupported skill field ${key}`);
          }
          const name = typeof metadata.name === "string" ? metadata.name.normalize("NFKC").trim() : "";
          if (name !== basename(dirname(path)).normalize("NFKC") || !name || [...name].length > 64
              || name !== name.toLowerCase() || name.startsWith("-") || name.endsWith("-")
              || name.includes("--") || !/^[\p{L}\p{N}-]+$/u.test(name)) {
            errors.push(`${label}: skill name must match its directory and the Agent Skills naming contract`);
          }
          if (typeof metadata.description !== "string" || !metadata.description.trim()
              || metadata.description.length > 1024) {
            errors.push(`${label}: description must contain 1-1024 characters`);
          }
          for (const field of ["license", "compatibility", "allowed-tools"]) {
            if (field in metadata && typeof metadata[field] !== "string") {
              errors.push(`${label}: ${field} must be a string`);
            }
          }
          if (typeof metadata.compatibility === "string"
              && (!metadata.compatibility.length || metadata.compatibility.length > 500)) {
            errors.push(`${label}: compatibility must contain 1-500 characters`);
          }
          if ("metadata" in metadata && (!object(metadata.metadata)
              || Object.values(metadata.metadata).some((value) => typeof value !== "string"))) {
            errors.push(`${label}: metadata must map string keys to string values`);
          }
        }
      }
      for (const match of source.matchAll(/\[[^\]]*\]\(([^)]+)\)/g)) {
        const target = match[1].split("#")[0];
        if (!target || /^[a-z][a-z0-9+.-]*:/i.test(target)) continue;
        await checkPath(resolve(dirname(path), decodeURIComponent(target)));
      }
    }
  };
  await walk(join(root, "skills"));
  for (const resource of Object.values(bundledResources)) {
    await checkPath(join(root, resource));
  }
  const extensionRoot = join(root, "com.github.copilot", "extensions", "cao-dashboard");
  for (const filename of [
    "extension.mjs", "dashboard-extension.mjs", "dashboard-agent-tools.mjs",
    "bundled-resources.mjs", "copilot-extension.json",
  ]) {
    await checkPath(join(extensionRoot, filename));
  }
  return errors;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const errors = await validateAgentPlugin(fileURLToPath(new URL("../", import.meta.url)));
  if (errors.length) {
    for (const error of errors) console.error(error);
    process.exitCode = 1;
  } else {
    console.error("Agent plugin manifest, skills, links, and bundled resources are valid.");
  }
}
