import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { controlPolicy, root, workflow } from "./workflow-contract.helpers.mjs";

const slug = "continuous-architecture-review";
const worker = `${slug}-implementation`;
const manifest = parse(readFileSync(join(root, slug, "aw.yml"), "utf8"));
const descriptor = JSON.parse(readFileSync(join(root, slug, "cao.json"), "utf8"));
const policy = controlPolicy["control-plane"].campaigns[slug];
const orchestrator = workflow(`${slug}.md`);
const implementation = workflow(`${worker}.md`);
const frontmatter = (source) => parse(/^---\n([\s\S]*?)\n---/.exec(source)[1]);

test("architecture campaign installs one bounded worker and its report generator", () => {
  assert.equal(manifest["min-version"], controlPolicy["gh-aw-version"]);
  assert.deepEqual(descriptor, {
    campaign: slug,
    orchestrator: slug,
    workers: { implementation: worker },
  });
  assert.deepEqual(manifest.includes.toSorted(), [
    "README.md",
    "report.mjs",
    `.github/workflows/${slug}.md`,
    `.github/workflows/${worker}.md`,
  ].toSorted());
  for (const name of ["README.md", "report.mjs"]) {
    assert.ok(existsSync(join(root, slug, name)));
  }
  assert.equal(policy.enabled, false, "registration must not activate a new schedule");
  assert.equal(policy.mode, "review");
  assert.equal(policy["max-repositories"], 1);
  assert.equal(policy.workers.implementation.workflow, worker);
});

test("architecture worker respects the review boundary and never dispatches", () => {
  const dispatcher = frontmatter(orchestrator);
  const workerConfig = frontmatter(implementation);
  assert.equal(dispatcher.imports[0].with.campaign, slug);
  assert.equal(dispatcher.imports[0].with.role, "orchestrator");
  assert.deepEqual(dispatcher["safe-outputs"]["dispatch-workflow"].workflows, [worker]);
  assert.equal(dispatcher["safe-outputs"]["dispatch-workflow"].max, 1);
  assert.equal(dispatcher["safe-outputs"]["threat-detection"], false);
  assert.equal(workerConfig.imports[0].with.campaign, slug);
  assert.equal(workerConfig.imports[0].with.role, "worker");
  assert.equal(workerConfig.imports[0].with.worker, "implementation");
  assert.ok(workerConfig.imports.some((entry) => entry.uses === "shared/review-bundle.md"));
  assert.equal(workerConfig["safe-outputs"]["dispatch-workflow"], undefined);
  assert.equal(workerConfig["safe-outputs"]["create-pull-request"]["target-repo"], "${{ inputs.target_repo }}");
  assert.equal(workerConfig["safe-outputs"]["create-issue"]["deduplicate-by-title"], true);
  assert.match(implementation, /In `review` mode, edit only `target\/`, never the workspace root/);
  assert.match(implementation, /A critical security failure blocks live execution/);
  assert.match(implementation, /node continuous-architecture-review\/report\.mjs/);
  for (const source of [orchestrator, implementation]) {
    assert.match(source, /Read `\/tmp\/gh-aw\/agent\/control-precompute\.json` first/);
    assert.match(source, /githubnext\/gh-cao/);
    assert.match(source, /#runtime-import\? \.github\/cao\/continuous-architecture-review\.md/);
  }
});
