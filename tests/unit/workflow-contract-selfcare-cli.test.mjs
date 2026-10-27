import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { controlPolicy, generatedJobs, root, transitivelyNeeds, workflow } from "./workflow-contract.helpers.mjs";

const id = "self-care-cli-consistency";
const source = workflow(`${id}.md`);
const config = parse(/^---\n([\s\S]*?)\n---/.exec(source)[1]);
const orchestrator = workflow("self-care.md");
const dispatch = parse(/^---\n([\s\S]*?)\n---/.exec(orchestrator)[1]);

test("CLI consistency is continuously fed within the existing SelfCare authority", () => {
  const descriptor = JSON.parse(readFileSync(join(root, "self-care/cao.json"), "utf8"));
  const manifest = parse(readFileSync(join(root, "self-care/aw.yml"), "utf8"));
  const campaign = controlPolicy["control-plane"].campaigns["self-care"];
  assert.equal(descriptor.workers["cli-consistency"], id);
  assert.deepEqual(campaign.workers["cli-consistency"], { workflow: id });
  assert.ok(manifest.includes.includes(`.github/workflows/${id}.md`));
  assert.equal(campaign.mode, "review");
  assert.equal(campaign["max-repositories"], 1);
  assert.deepEqual(campaign.targets, { "githubnext/gh-cao": { mode: "live" } });
  assert.equal(dispatch.on.schedule, "every 20 minutes");
  assert.equal(config.on.schedule, undefined);
  assert.equal(config.on["skip-if-match"], `is:pr is:open "gh-aw-workflow-id: ${id}" in:body`);
  const workers = Object.values(descriptor.workers);
  assert.equal(dispatch.imports[0].with.dispatch_max, workers.length);
  assert.equal(dispatch["safe-outputs"]["dispatch-workflow"].max, workers.length);
  assert.deepEqual([...dispatch["safe-outputs"]["dispatch-workflow"].workflows].sort(), workers.sort());
  assert.match(source, /at most one unconsumed pull request/);
  assert.match(source, /Fresh source and PR history override cache state/);
  assert.match(source, /Advance after a complete evaluation, even a no-op/);
});

test("CLI consistency inherits fail-closed admission and compiled safe-output limits", () => {
  assert.equal(config.if, "needs.pre_activation.outputs.cao_authorized == 'true'");
  assert.equal(config.imports[0].with.worker, "cli-consistency");
  assert.match(source, /precomputed `target_repo` is exactly `githubnext\/gh-cao`/);
  assert.match(source, /precomputed `safe_output_mode` is `live`/);
  assert.match(source, /stop without inspecting or changing repository files/);
  assert.deepEqual(config.permissions, {
    contents: "read",
    actions: "read",
    "copilot-requests": "write",
    "pull-requests": "read",
  });
  const outputs = config["safe-outputs"];
  assert.deepEqual(Object.keys(outputs), ["create-pull-request"]);
  const pr = outputs["create-pull-request"];
  assert.equal(pr.draft, true);
  assert.equal(pr.max, 1);
  assert.equal(pr["max-patch-files"], 24);
  assert.equal(pr["protected-files"], "fallback-to-issue");
  assert.deepEqual(pr["allowed-files"], [
    "activity/**/*.mjs", "cmd/**/*.go", "internal/cli/**/*.go", "internal/ghcli/**/*.go",
    "server/cmd/**/*.go", "scripts/**/*.mjs", "scripts/**/*.sh", "tests/**/*.mjs",
    "docs/**/*.md", "specs/**/*.md", "README.md", "package.json", "Makefile",
  ]);
  for (const step of config["pre-agent-steps"]) {
    assert.equal(step.if, "${{ inputs.target_repo == 'githubnext/gh-cao' && (inputs.safe_output_mode || 'review') == 'live' }}");
    assert.doesNotMatch(step.run ?? "", /\bgh\s+cao\b|\bgo\s+run\b/);
  }
  assert.match(source, /Use `node activity\/cao\.mjs` for workflow runtime operations/);
  assert.match(source, /execute it only for isolated, offline validation/);
  const compiled = workflow(`${id}.lock.yml`);
  const jobs = generatedJobs(compiled);
  assert.ok(transitivelyNeeds(jobs, "agent", "pre_activation"));
  assert.ok(transitivelyNeeds(jobs, "agent", "activation"));
  assert.match(compiled, /copilot-requests: write/);
  assert.doesNotMatch(compiled, /secrets\.COPILOT_GITHUB_TOKEN/);
  assert.match(compiled, /self-care:cli-consistency/);
});

test("CLI consistency inventories every surface and enforces the four ownership boundaries", () => {
  for (const required of [
    "node activity/cao.mjs", "activity/commands/", "cmd/gh-cao/", "internal/cli/",
    "server/cmd/cao-dashboard/", "Single-purpose microservices",
    "Recursively enumerate Cobra subcommands", "including hidden commands and aliases",
    "package `bin` declarations", "container entry points", "workflow invocations",
    "one row per command", "flags/defaults/environment precedence",
    "stdout/stderr", "exit status", "signal/cancellation handling",
    "Similar names alone do not prove duplicate semantics",
    "Mark unknown or incomplete discovery explicitly",
    "Never execute a service, deployment, credential, destructive, or network-mutating command",
  ]) {
    assert.ok(source.includes(required), `missing inventory/ownership contract: ${required}`);
  }
  assert.match(source, /Select exactly one cohesive, high-confidence cleanup/);
  assert.match(source, /Cite both implementations/);
  assert.match(source, /Do not add deprecation periods, compatibility shims, forwarding aliases, dual registrations, silent fallbacks, or legacy support/);
  assert.match(source, /Migrate every in-repository caller/);
  assert.match(source, /Assert that removed paths and aliases fail explicitly/);
  assert.match(source, /never leave a partial migration/);
  assert.match(source, /Do not edit dependencies, lockfiles, generated files, policy, workflow sources\/configuration/);
});

test("CLI consistency requires focused baseline, complete validation, and actionable PR evidence", () => {
  for (const required of [
    "Before editing, run the focused existing tests", "npm run lint", "npm run typecheck:cao",
    "make check", "golangci-lint run ./...", "go vet ./...", "go test ./...",
    "without Node.js on `PATH`", "shellcheck", "npm run docs:build", "npm run compile",
    "Missing tooling", "before/after command map", "removed-reference search results",
    "Call `create_pull_request` exactly once", "Call `noop` exactly once",
    "### Control Plane", "${{ inputs.correlation_id }}",
  ]) {
    assert.ok(source.includes(required), `missing validation/output contract: ${required}`);
  }
});
