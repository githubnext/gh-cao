import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { controlPolicy, generatedJobs, root, transitivelyNeeds, workflow } from "./workflow-contract.helpers.mjs";

const id = "self-care-server-soc2";
const source = workflow(`${id}.md`);
const config = parse(/^---\n([\s\S]*?)\n---/.exec(source)[1]);
const orchestrator = workflow("self-care.md");
const dispatch = parse(/^---\n([\s\S]*?)\n---/.exec(orchestrator)[1]);

test("SOC2 worker is registered consistently without expanding target authority", () => {
  const descriptor = JSON.parse(readFileSync(join(root, "self-care/cao.json"), "utf8"));
  const manifest = parse(readFileSync(join(root, "self-care/aw.yml"), "utf8"));
  const campaign = controlPolicy["control-plane"].campaigns["self-care"];
  assert.equal(descriptor.workers["server-soc2"], id);
  assert.deepEqual(campaign.workers["server-soc2"], { workflow: id });
  assert.ok(manifest.includes.includes(`.github/workflows/${id}.md`));
  assert.equal(campaign.mode, "review");
  assert.deepEqual(campaign.targets, { "githubnext/gh-cao": { mode: "live" } });
  assert.equal(controlPolicy["target-authority"].campaigns["self-care"].authority, "githubnext/gh-cao");
  assert.equal(campaign["max-repositories"], 1);
  const workers = Object.values(descriptor.workers);
  assert.equal(workers.length, 20);
  assert.equal(dispatch.imports[0].with.dispatch_max, workers.length);
  assert.equal(dispatch["safe-outputs"]["dispatch-workflow"].max, workers.length);
  assert.deepEqual([...dispatch["safe-outputs"]["dispatch-workflow"].workflows].sort(), workers.sort());
  assert.equal(dispatch.imports[0].with.worker_credits_per_target, 4600);
  assert.equal(dispatch["max-ai-credits"], 200);
});

test("SOC2 cadence uses daily fail-closed dispatch history, never PR activation skips", () => {
  assert.equal(dispatch.on.schedule, "every 20 minutes");
  assert.equal(config.on.schedule, undefined);
  assert.equal(config.on["skip-if-match"], undefined);
  assert.ok(config.on.workflow_dispatch);
  assert.match(orchestrator, /Dispatch `self-care-server-soc2` only when no run is queued, in progress, or\nstarted during the preceding 24 hours/);
  assert.match(orchestrator, /at most its ten most recent\nruns, including failed and cancelled runs/);
  assert.match(orchestrator, /truncated before the time boundary, or ambiguous, fail closed/);
  assert.match(orchestrator, /Do not gate its\nassessment dispatch on an open remediation PR/);
  assert.match(source, /Manual dispatch does not authorize extra daily fixes/);
  assert.match(source, /excluding this run/);
  assert.match(source, /unavailable, truncated before the time boundary, or ambiguous/);
  assert.match(source, /`report_incomplete` and stop/);
});

test("SOC2 admission and compiled jobs enforce exact target and live mode", () => {
  assert.equal(config.if, "needs.pre_activation.outputs.cao_authorized == 'true' && inputs.target_repo == 'githubnext/gh-cao' && inputs.safe_output_mode == 'live'");
  assert.deepEqual(config.imports[0].with, {
    campaign: "self-care",
    role: "worker",
    worker: "server-soc2",
    read_repository: "${{ inputs.target_repo }}",
    read_actions: "read",
    read_contents: "read",
    read_pull_requests: "read",
  });
  assert.match(source, /precomputed `target_repo` is exactly `githubnext\/gh-cao`/);
  assert.match(source, /precomputed `safe_output_mode` is `live`/);
  assert.match(source, /exact workflow SHA/);
  assert.match(source, /stop without\ninspecting or changing repository files/);
  const compiled = workflow(`${id}.lock.yml`);
  const jobs = generatedJobs(compiled);
  assert.ok(transitivelyNeeds(jobs, "agent", "pre_activation"));
  assert.ok(transitivelyNeeds(jobs, "agent", "activation"));
  assert.match(jobs.get("activation").block, /inputs\.target_repo == 'githubnext\/gh-cao'/);
  assert.match(jobs.get("activation").block, /inputs\.safe_output_mode == 'live'/);
  assert.match(compiled, /github\.workflow_sha/);
});

test("SOC2 proposals have a compiled draft and path/resource boundary", () => {
  const outputs = config["safe-outputs"];
  const pr = outputs["create-pull-request"];
  assert.equal(pr.draft, true);
  assert.equal(pr.max, 1);
  assert.equal(pr["max-patch-files"], 12);
  assert.equal(pr["max-patch-size"], 50);
  assert.equal(pr["protected-files"], "blocked");
  assert.equal(pr["fallback-as-issue"], false);
  assert.deepEqual(pr["allowed-files"], [
    "server/internal/**/*.go", "server/cmd/**/*.go",
    "server/SOC2.md", "server/SOC2-CHANGELOG.md",
  ]);
  assert.deepEqual(Object.keys(outputs).sort(), ["create-pull-request", "upload-artifact"]);
  assert.deepEqual(config.tools.github.toolsets, ["pull_requests", "repos"]);
  assert.equal(config.tools.github.mode, "gh-proxy");
  for (const permission of ["contents", "actions", "pull-requests"]) {
    assert.equal(config.permissions[permission], "read");
  }
  assert.equal(config["max-ai-credits"], 400);
  assert.equal(config["timeout-minutes"], 45);
  assert.match(source, /at most four production Go files in\none package, six corresponding test files/);
  assert.match(source, /Limit\nvalidation to 12 commands, each with a five-minute timeout/);
  assert.match(source, /workflow\/policy\/credential modifications/);
  const compiled = parse(workflow(`${id}.lock.yml`));
  const handler = compiled.jobs.safe_outputs.steps.find(
    (step) => step.env?.GH_AW_SAFE_OUTPUTS_HANDLER_CONFIG,
  );
  const enforced = JSON.parse(handler.env.GH_AW_SAFE_OUTPUTS_HANDLER_CONFIG).create_pull_request;
  assert.equal(enforced.draft, true);
  assert.equal(enforced.max, 1);
  assert.equal(enforced.max_patch_files, 12);
  assert.equal(enforced.max_patch_size, 50);
  assert.equal(enforced.protected_files_policy, "blocked");
  assert.equal(enforced.fallback_as_issue, false);
  assert.deepEqual(enforced.allowed_files, pr["allowed-files"]);
});

test("SOC2 selection requires verified ranked code gaps, never invented evidence", () => {
  for (const record of ["CODEBASE.yml", "specs/server-soc2-compliance.md", "server/SOC2.md", "server/SOC2-CHANGELOG.md", ".github/skills/soc2-server-assessment/SKILL.md"]) {
    assert.ok(source.includes(record), record);
  }
  assert.match(source, /Rank eligible findings by severity \(`high`, `medium`, `low`, `informational`\),\nthen confidence \(`high`, `medium`, `low`\), then stable finding ID ascending/);
  assert.match(source, /ASCII lexical order/);
  assert.match(source, /at most the top THREE compatible/);
  assert.match(source, /never invent issues to fill a quota/);
  assert.match(source, /Missing deployment, organizational or historical evidence is an explicit\nevidence request, never a forced code fix/);
  assert.match(source, /revalidate current implementation/);
  assert.match(source, /Preserve original source\/SHA observations/);
  assert.match(source, /counter-evidence/);
  assert.match(source, /Defer incompatible or unsafe/);
  assert.match(source, /No validated issue means no code PR/);
  assert.match(source, /never merge, deploy, approve risks,\napprove exclusions or close evidence gaps/);
});

test("SOC2 duplicate guards block remediation, not daily bounded reporting", () => {
  assert.match(source, /100 open PRs, 30 recently merged PRs/);
  assert.match(source, /at most 20 read-only\n\s+GitHub calls total/);
  assert.match(source, /Search all remediation authors/);
  assert.match(source, /including concurrent individual-finding remediation/);
  assert.match(source, /fail closed for remediation and report the incomplete check/);
  assert.match(source, /Always finish the assessment\/report even if this worker already has an open\n\s+PR/);
  assert.match(source, /do not create another PR\n\s+or modify its branch/);
  assert.match(source, /Recheck current open PR overlap immediately before proposing/);
  const artifact = config["safe-outputs"]["upload-artifact"];
  assert.equal(artifact["max-uploads"], 1);
  assert.equal(artifact["retention-days"], 14);
  assert.equal(artifact.defaults["if-no-files"], "error");
  assert.deepEqual(artifact["allowed-paths"], [
    "self-care-server-soc2-evidence/report.json",
    "self-care-server-soc2-evidence/report.md",
  ]);
  assert.match(source, /Each file must be at most 64 KiB/);
  assert.match(source, /even when an earlier\nPR remains open/);
  assert.match(source, /Never claim persistence from a local file alone/);
  assert.match(source, /durable consolidation pending and report incomplete/);
});

test("SOC2 validation and records distinguish proposal from operating effectiveness", () => {
  assert.match(source, /Run each\nreproduction on the unchanged baseline and after the fix/);
  for (const command of ["go -C server vet ./...", "golangci-lint fmt --diff", "golangci-lint run ./...", "npm run compile"]) {
    assert.ok(source.includes(command), command);
  }
  assert.match(source, /affected-package tests including race detection/);
  assert.match(source, /Failed, skipped or not-run required\nregression\/lint\/type checks block/);
  assert.match(source, /baseline failures\nremain explicit incomplete validation/);
  assert.match(source, /operating_effectiveness/);
  assert.match(source, /is `not-assessed`/);
  assert.match(source, /Record code fixes as PROPOSED in a draft PR, not deployed or\nclosed/);
  assert.match(source, /operator decisions pending/);
  assert.match(source, /Record-only PRs are allowed/);
  assert.match(source, /not\ndaily timestamp churn/);
  assert.match(source, /`noop`, `partial`, `incomplete`, or `proposed`/);
  assert.match(source, /`report_incomplete` once for missing evidence\/validation/);
});
