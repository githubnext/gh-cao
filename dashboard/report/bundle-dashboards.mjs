import { readFile, realpath, writeFile } from "node:fs/promises";
import { readFileSync, realpathSync } from "node:fs";
import path from "node:path";
import { composeDashboardDocuments } from "./compose-dashboard-documents.mjs";

/** @typedef {import("./compose-dashboard-documents.mjs").DashboardDocument} DashboardDocument */

/**
 * @param {unknown} value
 * @param {string} source
 * @returns {DashboardDocument}
 */
function dashboard(value, source) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${source} is not a dashboard document`);
  }
  const candidate = /** @type {Record<string, unknown>} */ (value);
  if (typeof candidate["language-version"] !== "string" || candidate["language-version"].length === 0
      || !candidate.dashboard || typeof candidate.dashboard !== "object" || Array.isArray(candidate.dashboard)) {
    throw new Error(`${source} is not a dashboard document`);
  }
  const dashboardCandidate = /** @type {Record<string, unknown>} */ (candidate.dashboard);
  if (!Array.isArray(dashboardCandidate.pages) || dashboardCandidate.pages.length === 0) {
    throw new Error(`${source} is not a dashboard document`);
  }
  return /** @type {DashboardDocument} */ (value);
}

/** @param {unknown} value @returns {value is Record<string, any>} */
function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

/** @param {string} root @param {string} candidate */
function isWithin(root, candidate) {
  const relativePath = path.relative(root, candidate);
  return relativePath === "" || (!relativePath.startsWith(`..${path.sep}`) && relativePath !== ".." && !path.isAbsolute(relativePath));
}

/**
 * @param {Record<string, any>} target
 * @param {unknown} fragment
 * @param {string} source
 */
function mergeDashboardFragment(target, fragment, source) {
  if (!isObject(fragment)) throw new Error(`${source} is not a dashboard fragment`);
  if ("fragments" in fragment) throw new Error(`${source} cannot declare nested dashboard fragments`);

  for (const [key, value] of Object.entries(fragment)) {
    if (Array.isArray(value)) {
      if (target[key] !== undefined && !Array.isArray(target[key])) {
        throw new Error(`${source} cannot append array dashboard.${key} to a non-array value`);
      }
      target[key] ??= [];
      if (key === "navigation") {
        const sections = new Map(target.navigation.map((/** @type {any} */ section) => [section?.label, section]));
        for (const incoming of value) {
          const section = sections.get(incoming?.label);
          if (section && Array.isArray(section.pages) && Array.isArray(incoming?.pages)) {
            section.pages.push(...structuredClone(incoming.pages));
          } else {
            const appended = structuredClone(incoming);
            target.navigation.push(appended);
            sections.set(appended?.label, appended);
          }
        }
        continue;
      }
      target[key].push(...structuredClone(value));
      continue;
    }
    if (target[key] !== undefined) {
      throw new Error(`${source} duplicates dashboard.${key}`);
    }
    target[key] = structuredClone(value);
  }
}

/** @param {string} source */
async function readJson(source) {
  try {
    return JSON.parse(await readFile(source, "utf8"));
  } catch (error) {
    throw new Error(`cannot read dashboard JSON ${source}: ${error instanceof Error ? error.message : String(error)}`, {
      cause: error,
    });
  }
}

/**
 * Loads a dashboard authoring source and composes its path-addressed fragments.
 * Fragment paths are relative to the root document and cannot escape its directory.
 * Each fragment is a partial `dashboard` object, so related queries, reusable views,
 * and pages can remain in one feature-oriented file.
 * @param {string} source
 * @returns {Promise<{ document: DashboardDocument, sourcePaths: string[] }>}
 */
export async function loadDashboardSource(source) {
  const sourcePath = await realpath(source);
  const sourceRoot = await realpath(path.dirname(sourcePath));
  const value = await readJson(sourcePath);
  if (!isObject(value)) throw new Error(`${source} is not a dashboard document`);
  const fragmentPaths = value.fragments;
  if (fragmentPaths !== undefined && (
    !Array.isArray(fragmentPaths)
    || fragmentPaths.some((fragmentPath) => typeof fragmentPath !== "string" || fragmentPath.length === 0)
  )) {
    throw new Error(`${source} dashboard fragments must be non-empty relative path strings`);
  }
  if (!isObject(value.dashboard)) throw new Error(`${source} is not a dashboard document`);

  const document = structuredClone(value);
  delete document.fragments;
  const resolvedPaths = [];
  const seen = new Set();
  for (const fragmentPath of fragmentPaths ?? []) {
    if (path.isAbsolute(fragmentPath)) {
      throw new Error(`${source} dashboard fragment path must be relative: ${fragmentPath}`);
    }
    const candidate = path.resolve(sourceRoot, fragmentPath);
    if (!isWithin(sourceRoot, candidate)) {
      throw new Error(`${source} dashboard fragment path escapes its source directory: ${fragmentPath}`);
    }
    const resolvedPath = await realpath(candidate);
    if (!isWithin(sourceRoot, resolvedPath)) {
      throw new Error(`${source} dashboard fragment path escapes its source directory: ${fragmentPath}`);
    }
    if (seen.has(resolvedPath)) {
      throw new Error(`${source} declares duplicate dashboard fragment: ${fragmentPath}`);
    }
    seen.add(resolvedPath);
    const fragment = await readJson(resolvedPath);
    mergeDashboardFragment(document.dashboard, fragment, resolvedPath);
    resolvedPaths.push(resolvedPath);
  }
  return {
    document: dashboard(document, sourcePath),
    sourcePaths: [sourcePath, ...resolvedPaths],
  };
}

/**
 * Synchronous counterpart for test fixtures and command-line consumers that
 * load the authoritative dashboard during module initialization.
 * @param {string | URL} source
 * @returns {{ document: DashboardDocument, sourcePaths: string[] }}
 */
export function loadDashboardSourceSync(source) {
  const sourcePath = realpathSync(source);
  const sourceRoot = realpathSync(path.dirname(sourcePath));
  const value = JSON.parse(readFileSync(sourcePath, "utf8"));
  if (!isObject(value)) throw new Error(`${source} is not a dashboard document`);
  const fragmentPaths = value.fragments;
  if (fragmentPaths !== undefined && (
    !Array.isArray(fragmentPaths)
    || fragmentPaths.some((fragmentPath) => typeof fragmentPath !== "string" || fragmentPath.length === 0)
  )) {
    throw new Error(`${source} dashboard fragments must be non-empty relative path strings`);
  }
  if (!isObject(value.dashboard)) throw new Error(`${source} is not a dashboard document`);

  const document = structuredClone(value);
  delete document.fragments;
  const resolvedPaths = [];
  const seen = new Set();
  for (const fragmentPath of fragmentPaths ?? []) {
    if (path.isAbsolute(fragmentPath)) {
      throw new Error(`${source} dashboard fragment path must be relative: ${fragmentPath}`);
    }
    const candidate = path.resolve(sourceRoot, fragmentPath);
    if (!isWithin(sourceRoot, candidate)) {
      throw new Error(`${source} dashboard fragment path escapes its source directory: ${fragmentPath}`);
    }
    const resolvedPath = realpathSync(candidate);
    if (!isWithin(sourceRoot, resolvedPath)) {
      throw new Error(`${source} dashboard fragment path escapes its source directory: ${fragmentPath}`);
    }
    if (seen.has(resolvedPath)) {
      throw new Error(`${source} declares duplicate dashboard fragment: ${fragmentPath}`);
    }
    seen.add(resolvedPath);
    mergeDashboardFragment(
      document.dashboard,
      JSON.parse(readFileSync(resolvedPath, "utf8")),
      resolvedPath,
    );
    resolvedPaths.push(resolvedPath);
  }
  return {
    document: dashboard(document, sourcePath),
    sourcePaths: [sourcePath, ...resolvedPaths],
  };
}

/**
 * @param {string} outputPath
 * @param {string[]} dashboardPaths
 * @param {string} [primaryPath]
 */
export async function bundleDashboardFiles(outputPath, dashboardPaths, primaryPath = outputPath) {
  const primary = (await loadDashboardSource(primaryPath)).document;
  const additions = await Promise.all(
    dashboardPaths.toSorted().map(async (source) => (
      (await loadDashboardSource(source)).document
    )),
  );
  await writeFile(outputPath, `${JSON.stringify(composeDashboardDocuments(primary, additions), null, 2)}\n`);
}
