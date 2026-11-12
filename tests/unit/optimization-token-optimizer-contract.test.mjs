import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { root, workflow } from "./workflow-contract.helpers.mjs";

const workers = [
  "optimization-token-auditor",
  "optimization-token-optimizer",
];

function evaluate(worker, input) {
  return JSON.parse(execFileSync(
    join(root, "optimization", ".github", "graders", `${worker}-operational-value.sh`),
    { encoding: "utf8", input: JSON.stringify(input) },
  ));
}

test("Optimization workers no longer use run-scoped operational-value graders", () => {
  for (const worker of workers) {
    assert.doesNotMatch(workflow(`${worker}.md`), /^graders:/m);
  }
});

test("Optimization workers use deterministic one-shot operational-value contracts", { skip: "legacy run-scoped graders were removed" }, () => {
  for (const worker of workers) {
    const fixtures = JSON.parse(readFileSync(
      join(root, ".github", "workflows", "graders", `${worker}-operational-value.fixtures.json`),
      "utf8",
    ));
    for (const fixture of fixtures) {
      assert.deepEqual(evaluate(worker, fixture.request), fixture.expected, `${worker}: ${fixture.name}`);
      assert.deepEqual(evaluate(worker, fixture.request), fixture.expected, `${worker}: ${fixture.name}: deterministic rerun`);
    }
  }
});

test("Optimization campaign installs exactly two workers without per-workflow evaluators", () => {
  const manifest = readFileSync(join(root, "optimization", "aw.yml"), "utf8");
  const descriptor = JSON.parse(readFileSync(join(root, "optimization", "cao.json"), "utf8"));

  assert.deepEqual(descriptor.workers, {
    "token-auditor": "optimization-token-auditor",
    "token-optimizer": "optimization-token-optimizer",
  });
  assert.match(manifest, /\.github\/workflows\/optimization-token-auditor\.md/);
  assert.match(manifest, /\.github\/workflows\/optimization-token-optimizer\.md/);
  assert.doesNotMatch(manifest, /ai-credit|agents-md|skills-curator|token-efficiency|intervention-tracker/);
  assert.doesNotMatch(manifest, /\.github\/graders\//);
});

test("Optimization workers are review-capped, target-scoped, and idempotent", () => {
  const policy = JSON.parse(readFileSync(join(root, ".github", "workflows", "cao.json"), "utf8"));
  const campaign = policy["control-plane"].campaigns.optimization;

  assert.equal(campaign.mode, "review");
  assert.equal(campaign["max-repositories"], 1);
  for (const worker of workers) {
    const source = workflow(`${worker}.md`);
    const workerName = worker.replace("optimization-", "");
    assert.match(source, new RegExp(`worker: ${workerName}`));
    assert.doesNotMatch(source, /uses: shared\/activity-cache\.md/);
    assert.match(source, /deduplicate-by-title: true/);
    assert.match(source, /group: "\$\{\{ github\.workflow \}\}-\$\{\{ inputs\.target_repo \}\}"/);
    assert.doesNotMatch(source, /dispatch-workflow:/);
    assert.equal(campaign.workers[workerName]["max-mode"], "review");
  }
});

test("Optimization orchestrator dispatches exactly the two campaign workers", () => {
  const source = workflow("optimization.md");

  assert.match(source, /workflows: \[optimization-token-auditor, optimization-token-optimizer\]/);
  assert.match(source, /threat-detection: false/);
  assert.match(source, /group: "\$\{\{ github\.workflow \}\}"/);
  assert.match(source, /\{\{#runtime-import\? \.github\/cao\/optimization\.md\}\}/);
});

test("Optimization token optimizer follows canonical gh-aw optimization guidance", () => {
  const source = workflow("optimization-token-optimizer.md");

  assert.match(source, /github\/gh-aw\/blob\/main\/\.github\/aw\/optimize-agentic-workflow\.md/);
  assert.match(source, /github\/gh-aw\/blob\/main\/\.github\/aw\/token-optimization\.md/);
  assert.match(source, /experiments:[\s\S]*metric: "aic"/);
  assert.match(source, /every applicable upstream optimization has been exhausted/);
});
