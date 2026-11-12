import assert from "node:assert/strict";
import { readdirSync } from "node:fs";
import test from "node:test";
import { resolvePolicy, workflow, workflowsDirectory } from "./workflow-contract.helpers.mjs";

// AI Credit evidence, budgets, and admission contracts.

test("Optimization workers use bounded canonical activity evidence", () => {
  for (const name of ["optimization-token-auditor.md", "optimization-token-optimizer.md"]) {
    const source = workflow(name);
    assert.doesNotMatch(source, /uses: shared\/activity-cache\.md/, name);
    assert.match(source, /activity\/cao\.mjs/, name);
    assert.match(source, /Validate schema|validate schema/i, name);
    assert.match(source, /Fetch only missing evidence|bounded read-only fallback/i, name);
    assert.doesNotMatch(source, /gh aw logs \\\n|repo-memory:|rolling-summary\.json/, name);
  }
});

test("workers disable costly daily AIC burn checks", () => {
  const workers = readdirSync(workflowsDirectory)
    .filter((name) => name.endsWith(".md"))
    .map((name) => [name, workflow(name)])
    .filter(([, source]) => /^\s+role: worker$/m.test(source));

  assert.ok(workers.length > 0, "expected at least one worker workflow");

  for (const [name, source] of workers) {
    assert.match(source, /^max-daily-ai-credits: -1$/m, name);
  }
});

test("Optimization preserves authoritative AI Credit and token grains", () => {
  const auditor = workflow("optimization-token-auditor.md");
  const optimizer = workflow("optimization-token-optimizer.md");

  for (const source of [auditor, optimizer]) {
    assert.match(source, /AI Credit/);
    assert.match(source, /input, output, cache-read, cache-write, and reasoning/);
    assert.match(source, /Never synthesize total tokens|Do not synthesize total tokens/);
    assert.match(source, /accepted outcome/i);
  }
  assert.match(auditor, /Missing evidence is not zero usage|preserving unknown/);
  assert.match(optimizer, /proposed, never realized/);
});

test("aggregate AI Credit admission reduces target fan-out", () => {
  const base = {
    eventName: "schedule",
    configuredMode: "review",
    maxRepos: 6,
    rolloutPercent: 100,
    totalRepositories: 100,
    dispatchMax: 12,
  };

  assert.equal(resolvePolicy({
    ...base,
    orchestratorCredits: 250,
    workerCreditsPerTarget: 500,
    aggregateCreditLimit: 1250,
  }).effectiveMaxRepos, 2);
  assert.equal(resolvePolicy({
    ...base,
    orchestratorCredits: 250,
    workerCreditsPerTarget: 500,
    aggregateCreditLimit: 250,
  }).effectiveMaxRepos, 0);
});
