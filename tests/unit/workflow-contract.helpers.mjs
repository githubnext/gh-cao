import assert from "node:assert/strict";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

// Shared fixtures and helpers for the workflow-contract-*.test.mjs suites.

export const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
export const workflowsDirectory = join(root, ".github", "workflows");
export const modes = ["review", "live"];
export const controlPolicy = JSON.parse(readFileSync(join(workflowsDirectory, "cao.json"), "utf8"));
export const ghAwVersion = controlPolicy["gh-aw-version"];
export const escapedGhAwVersion = ghAwVersion.replaceAll(".", "\\.");
export const targetReadCredential = "${{ steps.cao_target_read_app_token.outputs.token || vars.GH_AW_GITHUB_AUTH_MODE == 'pat' && secrets[fromJSON(vars.GH_AW_GITHUB_READ_PAT_REPOSITORIES || '{}')[steps.cao_target_read_scope.outputs.full_name]] || vars.GH_AW_GITHUB_AUTH_MODE == '' && secrets.GH_AW_GITHUB_READ_PAT || vars.GH_AW_GITHUB_AUTH_MODE == '' && secrets.GH_AW_GITHUB_TOKEN || (vars.GH_AW_GITHUB_AUTH_MODE == '' || vars.GH_AW_GITHUB_AUTH_MODE == 'workflow-token') && secrets.GITHUB_TOKEN }}";
export const escapedTargetReadCredential = targetReadCredential.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

export function workflow(name, directory = workflowsDirectory) {
  return readFileSync(join(directory, name), "utf8").replaceAll("\r\n", "\n");
}

export function script(name, directory) {
  return readFileSync(join(directory, name), "utf8").replaceAll("\r\n", "\n").replace(/\n$/, "");
}

export function portableSkill(name) {
  const directory = join(root, "skills", name);
  const references = join(directory, "references");
  const guidance = [
    readFileSync(join(directory, "SKILL.md"), "utf8"),
    ...(existsSync(references)
      ? readdirSync(references, { withFileTypes: true })
          .filter((entry) => entry.isFile() && entry.name.endsWith(".md"))
          .sort((left, right) => left.name.localeCompare(right.name))
          .map((entry) => readFileSync(join(references, entry.name), "utf8"))
      : []),
  ].join("\n").replaceAll("\r\n", "\n");
  return `${guidance}\n${guidance.replace(/\s+/g, " ")}`;
}

export function controlPrecompute() {
  return [
    workflow("shared/control.md"),
    readFileSync(join(root, ".github", "workflows", "shared", "control.mjs"), "utf8"),
    readFileSync(join(root, ".github", "workflows", "shared", "policy.mjs"), "utf8"),
  ].join("\n");
}

export function generatedJobs(source) {
  const jobsStart = source.indexOf("\njobs:\n");
  assert.notEqual(jobsStart, -1, "generated workflow has no jobs section");
  const jobsSource = source.slice(jobsStart + 7);
  const matches = [...jobsSource.matchAll(/^  ([A-Za-z0-9_-]+):\n/gm)];

  return new Map(matches.map((match, index) => {
    const block = jobsSource.slice(match.index, matches[index + 1]?.index ?? jobsSource.length);
    const inlineNeeds = /^    needs: ([A-Za-z0-9_-]+)$/m.exec(block);
    const listNeeds = /^    needs:\n((?:      - [A-Za-z0-9_-]+\n)+)/m.exec(block);
    const needs = inlineNeeds
      ? [inlineNeeds[1]]
      : [...(listNeeds?.[1].matchAll(/^      - ([A-Za-z0-9_-]+)$/gm) ?? [])].map((item) => item[1]);

    return [match[1], { block, needs }];
  }));
}

export function transitivelyNeeds(jobs, jobName, dependency, visited = new Set()) {
  if (visited.has(jobName)) return false;
  visited.add(jobName);
  const needs = jobs.get(jobName)?.needs ?? [];
  return needs.includes(dependency)
    || needs.some((name) => transitivelyNeeds(jobs, name, dependency, visited));
}

export function stepBlock(source, name) {
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = new RegExp(`\\n\\s+- name: ${escaped}[\\s\\S]*?(?=\\n\\s+- name: |$)`).exec(source);
  assert.ok(match, `missing step: ${name}`);
  return match[0];
}

export function resolvePolicy({
  eventName,
  configuredMode,
  manualMode,
  manualReviewRepo,
  controlRepository = "acme/control-plane",
  maxRepos,
  rolloutPercent,
  totalRepositories,
  dispatchMax = 1000,
  eligibleWorkers = 1,
  orchestratorCredits = 0,
  workerCreditsPerTarget = 0,
  aggregateCreditLimit = 1100,
  campaignEnabled = true,
}) {
  if (campaignEnabled === false) {
    return {
      enabled: false,
      safeOutputMode: null,
      safeOutputRepo: "",
      effectiveMaxRepos: 0,
      dispatchAllowed: false,
    };
  }
  if (campaignEnabled !== true) {
    throw new TypeError("campaignEnabled must be true or false");
  }
  if (!Number.isInteger(maxRepos) || maxRepos < 1 || maxRepos > 1000) {
    throw new RangeError("maxRepos must be an integer from 1 through 1000");
  }
  if (!Number.isInteger(rolloutPercent) || rolloutPercent < 1 || rolloutPercent > 100) {
    throw new RangeError("rolloutPercent must be an integer from 1 through 100");
  }
  if (!Number.isInteger(orchestratorCredits) || orchestratorCredits < 0
    || !Number.isInteger(workerCreditsPerTarget) || workerCreditsPerTarget < 0
    || !Number.isInteger(aggregateCreditLimit) || aggregateCreditLimit < 1) {
    throw new RangeError("AI Credit admission values must be bounded integers");
  }

  const requestedMode = eventName === "workflow_dispatch"
    ? manualMode || "review"
    : configuredMode || "review";
  if (!modes.includes(requestedMode)) {
    throw new RangeError("safeOutputMode must be review or live");
  }
  const safeOutputMode = requestedMode;
  const reviewOutputRepo = manualReviewRepo || controlRepository;
  const percentCap = totalRepositories === 0
    ? 0
    : Math.max(1, Math.ceil(totalRepositories * rolloutPercent / 100));
  const dispatchCap = eligibleWorkers === 0 ? 0 : Math.floor(dispatchMax / eligibleWorkers);
  const creditCap = workerCreditsPerTarget === 0
    ? maxRepos
    : Math.max(0, Math.floor((aggregateCreditLimit - orchestratorCredits) / workerCreditsPerTarget));
  const effectiveMaxRepos = Math.min(maxRepos, percentCap, dispatchCap, creditCap);

  return {
    enabled: campaignEnabled,
    safeOutputMode,
    safeOutputRepo: safeOutputMode === "review" ? reviewOutputRepo : "",
    effectiveMaxRepos,
    dispatchAllowed: true,
  };
}
