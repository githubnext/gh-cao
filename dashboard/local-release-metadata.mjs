import { parseVersion, updateState } from "./lib/version.mjs";
import { repositoryCoordinateId, workflowSourcePath } from "./site/src/data/model/ids.js";

function compilerVersionKey(repositoryId, path) {
  return `${repositoryId}:${workflowSourcePath(path).replace(/^\.github\/workflows\//, "")}`;
}

export function refreshCanonicalCompilerRelease(value, latestVersion, compilerVersions = new Map()) {
  if (value?.kind !== "record" || value.collection !== "workflows") return value;
  const override = value.record.path
    ? compilerVersions.get(compilerVersionKey(value.record.repositoryId, value.record.path))
    : undefined;
  return {
    ...value,
    record: {
      ...value.record,
      ghAwCurrentVersion: latestVersion,
      ...(value.record.ghAwVersion
        ? { ghAwUpdateState: updateState(value.record.ghAwVersion, latestVersion) }
        : {}),
      ...override,
    },
  };
}

export function compilerVersionOverrides(sources) {
  return new Map((sources.workflows?.rows ?? []).flatMap((row) => {
    if (typeof row.organization !== "string" || typeof row.repository !== "string"
        || typeof row.workflow !== "string" || !row.workflow.trim()) return [];
    const key = compilerVersionKey(repositoryCoordinateId(row.organization, row.repository), row.workflow);
    return [[key, {
      ghAwVersion: row["gh-aw-version"],
      ghAwCurrentVersion: row["gh-aw-current-version"],
      ghAwUpdateState: row["gh-aw-update-state"],
    }]];
  }));
}

/**
 * Enrich snapshot inputs before canonical ingestion without changing installed
 * version evidence or its observation time.
 * @param {Record<string, { rows: Record<string, unknown>[], [key: string]: unknown }>} sources
 * @param {string} latestVersion
 */
export function refreshDashboardCompilerRelease(sources, latestVersion) {
  const parsed = parseVersion(latestVersion);
  if (!parsed || parsed.prerelease.length > 0) {
    throw new Error("GitHub returned an invalid latest stable gh-aw release.");
  }
  if (!sources.workflows) return sources;
  if (!Array.isArray(sources.workflows.rows)) {
    throw new Error("Dashboard workflow inventory does not contain source rows.");
  }
  return {
    ...sources,
    workflows: {
      ...sources.workflows,
      rows: sources.workflows.rows.map((row) => ({
        ...row,
        "gh-aw-current-version": parsed.normalized,
        "gh-aw-update-state": updateState(row["gh-aw-version"], parsed.normalized),
      })),
    },
  };
}

/**
 * Refresh only workflow coordinates already present in the downloaded inventory.
 * @param {Record<string, { rows: Record<string, unknown>[], [key: string]: unknown }>} sources
 * @param {{ request: (path: string) => Promise<{ status: number, headers?: Record<string, string>, body?: Record<string, unknown> }>, output: (...values: unknown[]) => void }} dependencies
 */
export async function refreshDashboardCompilerVersions(sources, { request, output }) {
  if (!sources.workflows) return sources;
  if (!Array.isArray(sources.workflows.rows)) throw new Error("Dashboard workflow inventory does not contain source rows.");
  const requests = new Map();
  const paths = sources.workflows.rows.map((row) => {
    const coordinate = `${row.organization}/${row.repository}`;
    const workflow = String(row.workflow ?? "").replace(/^\.github\/workflows\//, "");
    if (!/^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})\/[A-Za-z0-9_.-]{1,100}$/.test(coordinate)
        || [".", ".."].includes(String(row.repository))
        || !/^[A-Za-z0-9_-][A-Za-z0-9_.-]{0,199}(?:\.md|\.lock\.yml)$/.test(workflow)) return null;
    const path = `/api/v1/github/repos/${coordinate}/contents/.github/workflows/${workflow.replace(/\.md$/, ".lock.yml")}`;
    requests.set(path, undefined);
    return path;
  });
  const pending = [...requests.keys()];
  for (let index = 0; index < Math.min(pending.length, 128); index += 8) {
    await Promise.all(pending.slice(index, Math.min(index + 8, 128)).map(async (path) => {
      const result = await request(path);
      const version = !result.headers?.Warning && result.status === 200
        ? parseVersion(result.body?.gh_aw_version)?.normalized : null;
      requests.set(path, version);
      if (!version) output("Live workflow compiler metadata is unavailable.", { workflow: path, status: result.status });
    }));
  }
  if (pending.length > 128) output("Live workflow compiler refresh reached its 128-file limit.", { workflows: pending.length });
  return {
    ...sources,
    workflows: {
      ...sources.workflows,
      rows: sources.workflows.rows.map((row, index) => {
        if (!paths[index]) return row;
        const version = requests.get(paths[index]);
        return {
          ...row,
          ...(version ? { "gh-aw-version": version } : {}),
          "gh-aw-update-state": version ? updateState(version, row["gh-aw-current-version"]) : "unknown",
        };
      }),
    },
  };
}
