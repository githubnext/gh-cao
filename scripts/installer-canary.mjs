import { pathToFileURL } from "node:url";
import { compareVersions, parseVersion } from "../dashboard/lib/version.mjs";

const versionPattern = /^v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/;

export function installerCanaryMatrix(releases) {
  const include = [];
  for (const channel of ["stable", "prerelease"]) {
    const available = releases.filter((release) => !release.draft &&
      release.prerelease === (channel === "prerelease") && versionPattern.test(release.tag_name) && parseVersion(release.tag_name))
      .sort((left, right) => compareVersions(right.tag_name, left.tag_name));
    if (available.length < 2) throw new Error(`Need two published ${channel} gh-aw releases`);
    const [current, previous] = available.map((release) => release.tag_name);
    for (const os of ["ubuntu-latest", "macos-latest"]) {
      for (const scenario of ["missing", "current", "outdated", "newer"]) {
        include.push({
          os, channel, scenario,
          required: scenario === "newer" ? previous : current,
          preinstalled: scenario === "missing" ? "" : scenario === "outdated" ? previous : current,
        });
      }
    }
  }
  return { include };
}

export function checkCanaryWorkflowRuns(runs, { now = Date.now(), maxAgeMs = 24 * 60 * 60 * 1000 } = {}) {
  if (!Array.isArray(runs)) throw new Error("Expected workflow run evidence");
  const completed = runs.filter((run) => run.status === "completed" && run.conclusion !== "skipped")
    .sort((left, right) => Date.parse(right.created_at) - Date.parse(left.created_at));
  const latest = completed[0];
  if (!latest || latest.conclusion !== "success" ||
      !Number.isFinite(Date.parse(latest.created_at)) ||
      Date.parse(latest.created_at) > now || now - Date.parse(latest.created_at) > maxAgeMs) {
    throw new Error("Latest completed non-skipped workflow run must be successful and less than 24 hours old");
  }
  if (!/^https:\/\/github\.com\/githubnext\/gh-cao\/actions\/runs\/\d+$/.test(latest.html_url)) {
    throw new Error("Expected gh-cao workflow evidence URL");
  }
  return latest.html_url;
}

export function checkCanaryJobs(jobs, patterns) {
  for (const pattern of patterns) {
    const matching = jobs.filter((job) => pattern.test(job.name));
    if (matching.length === 0 || matching.some((job) => job.conclusion !== "success")) {
      throw new Error(`Missing or unsuccessful smoke jobs: ${pattern}`);
    }
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const { execFileSync } = await import("node:child_process");
  const api = (endpoint, query) => JSON.parse(execFileSync("gh", ["api", endpoint, "--jq", query], {
    encoding: "utf8", maxBuffer: 16 * 1024 * 1024,
  }));
  if (process.argv[2] === "matrix") {
    // Fetch enough published evidence for both release channels.
    const releases = [];
    for (let page = 1; page <= 10; page++) {
      const batch = api(`repos/github/gh-aw/releases?per_page=100&page=${page}`, "[.[] | {tag_name, draft, prerelease}]");
      releases.push(...batch);
      try {
        console.log(JSON.stringify(installerCanaryMatrix(releases)));
        break;
      } catch (error) {
        if (batch.length < 100 || page === 10) throw error;
      }
    }
  } else if (process.argv[2] === "smoke") {
    for (const [workflow, requiredJobs] of [
      ["dashboard-deployed-integration.yml", [/^Test deployed dashboard$/]],
    ]) {
      const runs = api(`repos/githubnext/gh-cao/actions/workflows/${workflow}/runs?branch=main&per_page=100`,
        "[.workflow_runs[] | {status, conclusion, created_at, html_url}]");
      const url = checkCanaryWorkflowRuns(runs);
      const id = url.split("/").at(-1);
      const evidence = api(`repos/githubnext/gh-cao/actions/runs/${id}/jobs?per_page=100`,
        "{total_count, jobs: [.jobs[] | {name, conclusion}]}");
      if (evidence.total_count !== evidence.jobs.length) throw new Error("Incomplete workflow job evidence");
      checkCanaryJobs(evidence.jobs, requiredJobs);
      console.log(`${workflow}: ${url}`);
    }
  } else {
    throw new Error("Usage: node scripts/installer-canary.mjs matrix|smoke");
  }
}
