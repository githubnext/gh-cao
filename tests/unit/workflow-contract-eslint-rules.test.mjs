import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";

import { controlPolicy, root, workflow } from "./workflow-contract.helpers.mjs";

const orchestratorName = "eslint-rules.md";
const workers = [
  ["eslint-rules-inventory.md", "inventory"],
  ["eslint-rules-miner.md", "miner"],
  ["eslint-rules-refiner.md", "refiner"],
  ["eslint-rules-applier.md", "applier"],
  ["eslint-rules-librarian.md", "librarian"],
];
const allWorkflows = [orchestratorName, ...workers.map(([name]) => name)];
const memoryBranch = 'branch-name: "memory/eslint-rules"';

// Joins backslash-continued shell lines so each `gh api` invocation is checked whole.
function shellCommands(block) {
  const commands = [];
  let current = null;

  for (const line of block.split("\n")) {
    const trimmed = line.trim();
    if (current === null) {
      if (!trimmed.startsWith("gh api")) continue;
      current = trimmed;
    } else {
      current += ` ${trimmed}`;
    }
    if (!trimmed.endsWith("\\")) {
      commands.push(current);
      current = null;
    }
  }

  return commands;
}

test("ESLint Factory orchestrator owns discovery and dispatches only its declared workers", () => {
  const source = workflow(orchestratorName);

  assert.match(source, /name: "ESLint Factory"/);
  assert.match(source, /^\s+schedule: "?hourly"?$/m);
  assert.match(source, /campaign: eslint-rules\n\s+role: orchestrator/);
  assert.match(source, /dispatch_max: 5/);
  assert.match(source, /orchestrator_credits: 250/);
  assert.match(source, /worker_credits_per_target: 1750/);
  assert.match(
    source,
    /workflows: \[eslint-rules-inventory, eslint-rules-miner, eslint-rules-refiner, eslint-rules-applier, eslint-rules-librarian\]/,
  );
  assert.match(source, /dispatch-workflow:[\s\S]*?max: 5/);
  assert.match(source, /threat-detection: false/);
  assert.match(source, /## Discovery/);
  assert.match(source, /## Workers/);
  assert.match(source, /## Completion/);
  // Discovery stays inside precomputed policy and bounded language evidence.
  assert.match(source, /control-precompute\.json/);
  assert.match(source, /GET \/repos\/\{owner\}\/\{repo\}\/languages/);
  assert.match(source, /never paginate it and never use a repository or code search to find more candidates/);
  assert.match(source, new RegExp(memoryBranch));
  assert.match(source, /kind `repository-priority`/);
  assert.match(source, /transactions\/orchestrator__<owner>__<repository>\.jsonl/);
  assert.match(
    source,
    /node eslint-rules\/rules-db\.mjs build --memory "\$GH_AW_MEMORY_DIR" --database \/tmp\/gh-aw\/eslint-rules\/rules\.sqlite/,
  );
});

test("ESLint Factory workers are single-target and cannot discover or dispatch", () => {
  for (const [name, worker] of workers) {
    const source = workflow(name);

    assert.match(source, new RegExp(`campaign: eslint-rules\\n\\s+role: worker\\n\\s+worker: ${worker}`), name);
    assert.match(source, new RegExp(`tracker-id: ${name.slice(0, -3)}`), name);
    assert.match(source, /target_repo:\n\s+required: true\n\s+type: string/, name);
    assert.match(source, /safe_output_repo:\n\s+required: true\n\s+type: string/, name);
    assert.match(source, /bots: \["github-actions\[bot\]", "cao-githubnext-gh-aw-cao-write\[bot\]"\]/, name);
    assert.match(source, /concurrency:\n\s+group: "\$\{\{ github\.workflow \}\}-\$\{\{ inputs\.target_repo \}\}"/, name);
    assert.match(source, /max-daily-ai-credits: -1/, name);
    assert.match(
      source,
      /Never discover, analyse, or write to another repository, never dispatch another workflow, and never widen the dispatched mode\./,
      name,
    );
    assert.match(source, /you only ever handle the single repository you were dispatched for/, name);
    assert.doesNotMatch(source, /dispatch-workflow:/, name);
    assert.doesNotMatch(source, /^\s+(contents|actions|issues|pull-requests): write$/m, name);
    assert.doesNotMatch(source, /^\s*noop:/m, name);
    assert.doesNotMatch(source, /^evals:/m, name);
  }
});

test("ESLint Factory workers share one append-only memory branch with collision-safe names", () => {
  for (const [name] of workers) {
    const source = workflow(name);

    assert.match(source, new RegExp(memoryBranch), name);
    assert.equal(source.match(/branch-name: "[^"]+"/g).length, 1, name);
    assert.match(source, /file-glob: \["transactions\/\*\.jsonl", "rules\/\*\.json"\]/, name);
    assert.match(source, /allowed-extensions: \[\.json, \.jsonl\]|allowed-extensions: \[".json", ".jsonl"\]/, name);
    assert.match(source, /transactions\/[a-z]+__<owner>__<repository>\.jsonl/, name);
    assert.match(source, /prevents per-run file-count growth/, name);
    assert.match(source, /Never rewrite, reorder, or delete an existing line/, name);
    assert.match(source, /"cao\.eslint-rules\.transaction"/, name);
    assert.match(source, /schema_version/, name);
    assert.match(source, /rules\/<rule-key>\.json` is a flat directory/, name);
    assert.match(
      source,
      /node eslint-rules\/rules-db\.mjs build --memory "\$GH_AW_MEMORY_DIR" --database \/tmp\/gh-aw\/eslint-rules\/rules\.sqlite/,
      name,
    );
    assert.match(source, /fails closed on malformed lines/, name);
    // Compact evidence only: raw transcripts and source dumps stay out of memory.
    assert.match(source, /Never copy [^.]*into memory\./, name);
  }
});

test("ESLint Factory memory-only workers end in a non-mutating terminal output", () => {
  for (const name of ["eslint-rules-inventory.md", "eslint-rules-miner.md", "eslint-rules-refiner.md", "eslint-rules-librarian.md"]) {
    const source = workflow(name);

    assert.doesNotMatch(source, /create-issue:/, name);
    assert.doesNotMatch(source, /create-pull-request:|push-to-pull-request-branch:|add-comment:/, name);
    assert.match(source, /never files issues and never writes to the target repository/, name);
    assert.match(source, /call `noop` exactly once/, name);
    assert.match(source, /`report_incomplete`/, name);
  }
});

test("ESLint Factory applier requests one deduplicated warning-only adoption issue", () => {
  const source = workflow("eslint-rules-applier.md");

  assert.match(source, /title-prefix: "\[eslint-rules:applier\] "/);
  assert.match(source, /labels: \[eslint-rules, eslint-rules:applier\]/);
  assert.match(source, /deduplicate-by-title: true/);
  assert.match(source, /expires: 30d/);
  assert.match(source, /create-issue:[\s\S]*?max: 1/);
  assert.match(
    source,
    /target-repo: \$\{\{ \(inputs\.safe_output_mode \|\| 'review'\) == 'review' && \(inputs\.safe_output_repo \|\| github\.repository\) \|\| inputs\.target_repo \}\}/,
  );
  assert.doesNotMatch(source, /create-pull-request:|push-to-pull-request-branch:/);
  assert.match(source, /You never change the target repository\./);
  assert.match(source, /configured at `warn` severity only/);
  assert.match(source, /dedicated npm script/i);
  assert.match(source, /separate CI build job/i);
  assert.match(source, /<details><summary><b>Agent prompt<\/b><\/summary>/);
  assert.match(source, /must not gate merges initially/);
  assert.match(
    source,
    /Provide only the unprefixed subject to `create_issue`; the configured `title-prefix` is added automatically, so do not repeat it or add a semantically equivalent category prefix\./,
  );
});

test("ESLint Factory workflows keep GitHub evidence acquisition bounded", () => {
  for (const name of allWorkflows) {
    const source = workflow(name);

    for (const line of source.split("\n").filter((entry) => entry.includes("--paginate"))) {
      assert.match(line, /never/i, `${name} only ever prohibits unbounded pagination`);
    }
    assert.match(source, /\{\{#runtime-import\? \.github\/cao\/eslint-rules\.md\}\}\s*$/, name);
    assert.match(source, /GH_AW_SAFE_OUTPUT_MODE: \$\{\{ inputs\.safe_output_mode \|\| 'review' \}\}/, name);
    assert.match(source, /if: needs\.pre_activation\.outputs\.cao_authorized == 'true'/, name);
  }

  const miner = workflow("eslint-rules-miner.md");
  const minerFrontmatter = miner.split("\n---\n")[0];
  assert.match(minerFrontmatter, /tools:\n\s+github: false/);
  assert.match(
    miner,
    /All GitHub evidence must come only from these pre-fetched snapshot files:/,
  );
  for (const file of [
    "merged-pull-requests.json",
    "commits.json",
    "pull-request-files.jsonl",
    "review-comments.jsonl",
  ]) {
    assert.match(miner, new RegExp(`/tmp/gh-aw/eslint-rules/evidence/${file.replace(".", "\\.")}`));
  }
  assert.doesNotMatch(miner, /uses: shared\/activity-cache\.md/);
  assert.match(miner, /WINDOW_DAYS: "14"/);
  assert.match(miner, /MAX_PULL_REQUESTS: "25"/);
  const minerSteps = miner.split("\n---\n")[0];
  for (const call of shellCommands(minerSteps)) {
    assert.match(call, /-F per_page=/, "every miner API call declares an explicit page size");
    assert.match(call, /-F page=1/, "every miner API call reads exactly one page");
  }
  assert.ok(shellCommands(minerSteps).length >= 4, "the miner pre-fetches its bounded evidence deterministically");
  assert.doesNotMatch(miner, /cao-activity\/gh-aw-logs-shards|ACTIVITY_LOG_DIR/);
  assert.match(miner, /Historical run evidence is not collected by this worker/);
  assert.match(miner, /at most one candidate per run|Select \*\*at most one\*\* candidate per run/);
});

test("ESLint Factory campaign manifest, policy, and dashboard describe the same operation", () => {
  const manifest = readFileSync(join(root, "eslint-rules", "aw.yml"), "utf8");
  for (const name of allWorkflows) {
    assert.match(manifest, new RegExp(`\\.github/workflows/${name.replace(".", "\\.")}`), name);
  }
  assert.doesNotMatch(manifest, /- \.\.\/aw\.yml/);
  assert.match(manifest, /experimental: true/);
  assert.doesNotMatch(manifest, /^resources:/m);
  assert.ok(readFileSync(join(root, "eslint-rules", "rules-db.mjs"), "utf8").length > 0);

  const policy = controlPolicy["control-plane"].campaigns["eslint-rules"];
  assert.equal(policy.mode, "review");
  assert.equal(policy["max-repositories"], 1);
  assert.deepEqual(
    Object.entries(policy.workers).map(([worker, entry]) => [`${worker}`, entry.workflow]),
    workers.map(([name, worker]) => [worker, name.replace(".md", "")]),
  );

});
