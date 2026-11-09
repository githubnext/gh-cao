#!/usr/bin/env node

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { isDeepStrictEqual } from "node:util";
import {
  acquireFileDietHistory, collectReceipts, fileDietAcquisitionRequirement, fileDietDiscoveryTerms,
  preserveFileDietSnapshots, refreshFileDiet,
} from "../.github/skills/add-operational-value/examples/acquire-file-diet.mjs";
import { fileDietEvents, readFileDietProof } from "../.github/skills/add-operational-value/examples/daily-file-diet.mjs";
import { githubReader } from "../.github/skills/add-operational-value/scripts/github-evidence.mjs";
import { prepareGithubApiBudget, readGithubApiBudget, consumeGithubApiBudget } from "../activity/github-api-budget.mjs";
import { loadPolicyFile } from "../.github/workflows/shared/policy.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const repository = "github/gh-aw";
const workflow = "daily-file-diet";

function measuredRequests(directory, limit, observe) {
  const budget = path.join(directory, "api-budget.json");
  prepareGithubApiBudget({ filename: budget, requested: limit, reserve: 2000 });
  return {
    budget,
    reader: githubReader(path.join(directory, "requests"), execFileSync, {
      maxRequests: limit, requirementFor: fileDietAcquisitionRequirement,
      consume: () => consumeGithubApiBudget(budget), onRequest: observe,
    }),
  };
}

function comparable(ledger) {
  return {
    outputs: ledger.outputs.items,
    implementations: ledger.events.map(({ id, at, lineage, acceptance, value, attribution }) =>
      ({ id, at, lineage, acceptance, value, attribution })),
    snapshots: ledger.repositoryState.observations,
    coverage: ledger.coverage,
    checks: ledger.checks,
  };
}

function requestCounts() {
  const counts = new Map();
  return {
    observe(requirement) {
      assert.ok(requirement, "Unreviewed File Diet request");
      const key = `${requirement.pool}:${requirement.primitive}`;
      counts.set(key, (counts.get(key) ?? 0) + 1);
    },
    summary() {
      return Object.fromEntries([...counts].sort(([a], [b]) => a.localeCompare(b)));
    },
    total() {
      return [...counts.values()].reduce((sum, count) => sum + count, 0);
    },
  };
}

export function compareFileDietProof(full, shadow) {
  const expected = comparable(fileDietEvents(full));
  const actual = comparable(fileDietEvents(shadow));
  return Object.keys(expected).filter(key => !isDeepStrictEqual(expected[key], actual[key]));
}

export function offlineFileDietPilot(previous) {
  const retained = readFileDietProof(previous);
  const { proof } = retained;
  const ledger = fileDietEvents(retained);
  const issues = proof.issues.length;
  const mutablePulls = proof.pulls.filter(pull =>
    !pull.mergedAt || Date.parse(pull.mergedAt) >= Date.parse(proof.until)).length;
  const graphQLRequests = { issues: Math.ceil(issues / 10), mutablePulls: Math.ceil(mutablePulls / 10) };
  const common = 2 + 1 + fileDietDiscoveryTerms.length + 1 + 2 * mutablePulls + graphQLRequests.mutablePulls;
  const full = common + graphQLRequests.issues + issues;
  const observations = ledger.repositoryState.observations;
  return {
    repository, workflow, cutoff: proof.until, mode: "preserved-proof-no-change-bound",
    productionReady: false, apiRequestsMade: 0,
    population: { issues, pulls: proof.pulls.length, mutablePulls,
      inventories: retained.inventories.size, uniqueBlobs: proof.uniqueBlobs,
      snapshots: proof.snapshots.length, linkedImplementations: ledger.events.length },
    repositoryOutcome: { first: observations[0]?.value, latest: observations.at(-1)?.value,
      unit: "oversized production Go files" },
    graphQLRequests,
    requestFloor: { existing: full, searchOnlyShadow: common, potentiallyAvoidable: full - common },
    limits: [
      "Request counts are lower bounds for an unchanged later cutoff, not observed GitHub traffic.",
      "The Search-only shadow cannot prove unchanged issue relationships or complete coverage.",
      "Git transport, retries, extra pages, new outputs, and receipt refreshes are excluded.",
    ],
  };
}

export function pilotFileDiet({ endAt, previous, output }) {
  assert.ok(!existsSync(output), "Pilot output must be a new directory");
  const prior = readFileDietProof(previous);
  assert.ok(Date.parse(endAt) > Date.parse(prior.proof.until) && Date.parse(endAt) <= Date.now(),
    "Pilot cutoff must follow the preserved proof and not be in the future");
  const policy = loadPolicyFile(path.join(root, ".github/workflows/cao.json"));
  const registration = policy["control-plane"].campaigns.activity["workflow-values"]
    .find(entry => entry.repository === repository && entry.workflow === workflow);
  assert.ok(registration?.enabled !== false && registration["api-requests"] === 700,
    "The reviewed File Diet request ceiling changed");
  mkdirSync(output, { recursive: true });
  const scratch = mkdtempSync(path.join(output, ".scratch-"));
  const originalBudget = process.env.CAO_GITHUB_API_BUDGET_FILE;
  try {
    const baselineCounts = requestCounts();
    const baselineDir = path.join(scratch, "baseline");
    mkdirSync(baselineDir);
    const baselineBudget = path.join(baselineDir, "api-budget.json");
    prepareGithubApiBudget({ filename: baselineBudget, requested: registration["api-requests"], reserve: 2000 });
    process.env.CAO_GITHUB_API_BUDGET_FILE = baselineBudget;
    const started = performance.now();
    refreshFileDiet({ endAt, previous, output: path.join(output, "baseline"),
      scratch: baselineDir, onRequest: baselineCounts.observe });
    const baselineMs = Math.ceil(performance.now() - started);
    const full = readFileDietProof(path.join(output, "baseline", "proof"));
    const baselineRequests = readGithubApiBudget(baselineBudget).consumed;
    assert.equal(baselineCounts.total(), baselineRequests, "Baseline request accounting is incomplete");

    const shadowCounts = requestCounts();
    const shadowDir = path.join(scratch, "shadow");
    mkdirSync(shadowDir);
    const shadow = measuredRequests(shadowDir, registration["api-requests"], shadowCounts.observe);
    process.env.CAO_GITHUB_API_BUDGET_FILE = shadow.budget;
    const shadowStarted = performance.now();
    const proof = acquireFileDietHistory(shadow.reader, endAt, prior.proof, { shadowIncremental: true });
    preserveFileDietSnapshots(proof, prior.proof);
    collectReceipts(proof, prior, shadow.reader, shadowDir);
    const acceptedPulls = new Map(full.proof.pulls.map(pull => [pull.id, pull]));
    for (const pull of proof.pulls) {
      const accepted = acceptedPulls.get(pull.id);
      if (accepted?.mergeSha === pull.mergeSha) pull.preMergeSha = accepted.preMergeSha;
    }
    proof.observedAt = full.proof.observedAt;
    const shadowMs = Math.ceil(performance.now() - shadowStarted);
    const shadowRequests = readGithubApiBudget(shadow.budget).consumed;
    assert.equal(shadowCounts.total(), shadowRequests, "Shadow request accounting is incomplete");
    const mismatches = compareFileDietProof(full, { proof, inventories: full.inventories });
    const result = {
      repository, workflow, endAt, previousCutoff: prior.proof.until,
      mode: "search-updated-shadow", productionReady: false,
      reason: "Search matches do not establish complete relationship-change coverage; full reconciliation remains required",
      parity: { matches: mismatches.length === 0, mismatches },
      baseline: { requests: baselineRequests, byRequirement: baselineCounts.summary(), wallMs: baselineMs,
        newInventories: full.proof.acquisition.newInventories, newBlobs: full.proof.acquisition.newBlobs },
      shadow: { requests: shadowRequests, byRequirement: shadowCounts.summary(), wallMs: shadowMs },
    };
    writeFileSync(path.join(output, "pilot.json"), `${JSON.stringify(result, null, 2)}\n`);
    return result;
  } finally {
    if (originalBudget === undefined) delete process.env.CAO_GITHUB_API_BUDGET_FILE;
    else process.env.CAO_GITHUB_API_BUDGET_FILE = originalBudget;
    rmSync(scratch, { recursive: true, force: true });
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const [endAt, previous, output, ...extra] = process.argv.slice(2);
    if (endAt === "--offline") {
      assert.ok(previous && output && extra.length === 0,
        "Usage: node scripts/pilot-file-diet-incremental.mjs --offline PRESERVED_PROOF NEW_OUTPUT_DIRECTORY");
      assert.ok(!existsSync(output), "Pilot output must be a new directory");
      const result = offlineFileDietPilot(path.resolve(previous));
      mkdirSync(output, { recursive: true });
      writeFileSync(path.join(output, "pilot.json"), `${JSON.stringify(result, null, 2)}\n`);
      console.log(JSON.stringify(result));
    } else {
      assert.ok(endAt && previous && output && extra.length === 0,
        "Usage: node scripts/pilot-file-diet-incremental.mjs END_AT PRESERVED_PROOF NEW_OUTPUT_DIRECTORY");
      const result = pilotFileDiet({ endAt, previous: path.resolve(previous), output: path.resolve(output) });
      console.log(JSON.stringify(result));
      if (!result.parity.matches) process.exitCode = 1;
    }
  } catch (error) {
    const reason = /SAML (?:SSO|enforcement)/.test(String(error))
      ? "GitHub token lacks organization SSO authorization"
      : String(error.message ?? error).replace(/https?:\/\/\S+/g, "[URL]");
    console.error(`File Diet pilot failed: ${reason}`);
    process.exitCode = 1;
  }
}
