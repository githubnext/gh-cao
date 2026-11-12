import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { effectivePolicy, parsePolicy } from "../../.github/workflows/shared/policy.mjs";
import { controlPolicy, generatedJobs, root, transitivelyNeeds, workflow } from "./workflow-contract.helpers.mjs";

const id = "self-care-calesi-design-principles";
const worker = "calesi-design-principles";
const source = workflow(`${id}.md`);
const config = parse(/^---\n([\s\S]*?)\n---/.exec(source)[1]);
const orchestrator = workflow("self-care.md");
const dispatch = parse(/^---\n([\s\S]*?)\n---/.exec(orchestrator)[1]);

test("Calesi registration enables only the worker within the existing target scope", () => {
  const descriptor = JSON.parse(readFileSync(join(root, "self-care/cao.json"), "utf8"));
  const manifest = parse(readFileSync(join(root, "self-care/aw.yml"), "utf8"));
  const campaign = controlPolicy["control-plane"].campaigns["self-care"];
  assert.equal(descriptor.workers[worker], id);
  assert.ok(manifest.includes.includes(`.github/workflows/${id}.md`));
  assert.deepEqual(campaign.workers[worker], { workflow: id, enabled: true });
  assert.equal(campaign.mode, "review");
  assert.equal(campaign["max-repositories"], 1);
  assert.equal(campaign["rollout-percent"], 100);
  assert.deepEqual(campaign.targets, { "githubnext/gh-cao": { mode: "live" } });
  assert.equal(controlPolicy["target-authority"].campaigns["self-care"].authority, "githubnext/gh-cao");
  const workers = Object.values(descriptor.workers);
  assert.equal(workers.length, 22);
  assert.equal(dispatch.imports[0].with.dispatch_max, workers.length);
  assert.equal(dispatch["safe-outputs"]["dispatch-workflow"].max, workers.length);
  assert.deepEqual([...dispatch["safe-outputs"]["dispatch-workflow"].workflows].sort(), workers.sort());
  assert.equal(dispatch.imports[0].with.worker_credits_per_target, 5400);
  assert.match(orchestrator, /`worker_workflows` entry with `configured` equal to the workflow ID, `policy_enabled == true` and `eligible == true`/);
  assert.match(orchestrator, /missing or disabled entries are ineligible/);
});

test("production policy resolution admits the enabled worker for the existing live target", () => {
  const document = parsePolicy(JSON.stringify(controlPolicy));
  const result = effectivePolicy(document, {
    campaignName: "self-care", role: "worker", workerName: worker,
    controlRepository: "githubnext/gh-cao", targetRepository: "githubnext/gh-cao",
    requestedMode: "live", eventName: "workflow_dispatch",
  });
  assert.equal(result.authorized, true);
  assert.equal(result.safe_output_mode, "live");
  assert.equal(result.worker_policies[id].enabled, true);
  const parent = effectivePolicy(document, {
    campaignName: "self-care", role: "orchestrator", controlRepository: "githubnext/gh-cao",
  });
  assert.equal(parent.worker_policies[id].enabled, true);
  assert.equal(parent.safe_output_mode, "review");
  assert.deepEqual(parent.target_policies, { "githubnext/gh-cao": { mode: "live" } });
});

test("production policy resolution denies the disabled worker in every requested mode", () => {
  const disabled = structuredClone(controlPolicy);
  disabled["control-plane"].campaigns["self-care"].workers[worker].enabled = false;
  const document = parsePolicy(JSON.stringify(disabled));
  for (const requestedMode of ["review", "live", "debug"]) {
    const result = effectivePolicy(document, {
      campaignName: "self-care", role: "worker", workerName: worker,
      controlRepository: "githubnext/gh-cao", targetRepository: "githubnext/gh-cao",
      requestedMode, eventName: "workflow_dispatch",
    });
    assert.equal(result.authorized, false, requestedMode);
    assert.equal(result.reason, "worker-disabled", requestedMode);
  }
  const result = effectivePolicy(document, {
    campaignName: "self-care", role: "orchestrator", controlRepository: "githubnext/gh-cao",
  });
  assert.equal(result.worker_policies[id].enabled, false);
  assert.equal(result.worker_policies["self-care-server-soc2"].enabled, true);
});

test("Calesi uses the existing dispatch envelope, admission and open-PR activation guard", () => {
  assert.equal(config.on.schedule, undefined);
  assert.equal(config.on.workflow_dispatch.inputs.safe_output_mode.required, true);
  assert.equal(config.on.workflow_dispatch.inputs.safe_output_mode.default, undefined);
  assert.equal(config.on["skip-if-match"], `is:pr is:open "gh-aw-workflow-id: ${id}" in:body`);
  assert.equal(config["tracker-id"], id);
  assert.equal(config.if, "needs.pre_activation.outputs.cao_authorized == 'true' && inputs.target_repo == 'githubnext/gh-cao' && inputs.safe_output_mode == 'live'");
  assert.deepEqual(config.imports[0].with, {
    campaign: "self-care", role: "worker", worker,
    read_repository: "${{ inputs.target_repo }}",
    read_actions: "read", read_contents: "read", read_pull_requests: "read",
  });
  assert.deepEqual(config.permissions, {
    contents: "read", actions: "read", "copilot-requests": "write", "pull-requests": "read",
  });
  assert.deepEqual(config.tools.github.toolsets, ["pull_requests", "repos"]);
  const compiled = workflow(`${id}.lock.yml`);
  const jobs = generatedJobs(compiled);
  assert.ok(transitivelyNeeds(jobs, "agent", "pre_activation"));
  assert.ok(transitivelyNeeds(jobs, "agent", "activation"));
  assert.match(jobs.get("activation").block, /inputs\.target_repo == 'githubnext\/gh-cao'/);
  assert.match(jobs.get("activation").block, /inputs\.safe_output_mode == 'live'/);
  assert.match(source, /Do not discover repositories, dispatch workflows/);
  assert.ok(source.trimEnd().endsWith("{{#runtime-import? .github/cao/self-care.md}}"));
});

test("compiled Calesi outputs serialize bounded draft proposals and preserve review routing", () => {
  assert.equal(config["max-ai-credits"], 400);
  assert.equal(config["timeout-minutes"], 40);
  assert.deepEqual(Object.keys(config["safe-outputs"]).sort(), ["concurrency-group", "create-pull-request"]);
  const pr = config["safe-outputs"]["create-pull-request"];
  assert.equal(pr.draft, true);
  assert.equal(pr.max, 1);
  assert.equal(pr.expires, "7d");
  assert.equal(pr["max-patch-files"], 10);
  assert.equal(pr["max-patch-size"], 50);
  assert.equal(pr["protected-files"], "blocked");
  assert.equal(pr["fallback-as-issue"], false);
  assert.deepEqual(pr["allowed-files"], ["server/internal/**/*.go"]);
  assert.equal(pr["title-prefix"], `[self-care:${worker}] `);
  assert.deepEqual(pr.labels, ["self-care", `self-care:${worker}`]);
  assert.equal(pr["target-repo"], "${{ (inputs.safe_output_mode || 'review') == 'review' && (inputs.safe_output_repo || github.repository) || inputs.target_repo }}");
  const compiled = parse(workflow(`${id}.lock.yml`));
  assert.deepEqual(compiled.jobs.safe_outputs.concurrency, {
    group: config["safe-outputs"]["concurrency-group"], "cancel-in-progress": false,
  });
  const handler = compiled.jobs.safe_outputs.steps.find((step) => step.env?.GH_AW_SAFE_OUTPUTS_HANDLER_CONFIG);
  const outputs = JSON.parse(handler.env.GH_AW_SAFE_OUTPUTS_HANDLER_CONFIG);
  const enforced = outputs.create_pull_request;
  assert.equal(enforced.draft, true);
  assert.equal(enforced.max, 1);
  assert.equal(enforced.max_patch_files, 10);
  assert.equal(enforced.max_patch_size, 50);
  assert.equal(enforced.protected_files_policy, "blocked");
  assert.equal(enforced.fallback_as_issue, false);
  assert.deepEqual(enforced.allowed_files, pr["allowed-files"]);
  assert.equal(enforced["target-repo"], pr["target-repo"]);
  assert.equal(outputs.noop["report-as-issue"], "false");
});

test("Calesi checks require evidence and preserve deliberate provider and domain boundaries", () => {
  for (const required of [
    "https://cap.cloud.sap/docs/guides/integration/calesi",
    "Domain-level service interfaces", "Protocol/location-independent consumption",
    "Fast local development", "Interchangeable implementations",
    "Deliberate extensibility through event handlers",
    "implementation packages, database handles, SQL/HTTP transport details",
    "domain services bypassing owned adapters", "fixtures that bypass the production adapter/query boundary",
    "missing or inconsistent multi-adapter conformance", "side effects bypassing policy",
    "canonical data, operational state, policy/authorization and dispatch",
    "minimal domain-shaped consumption views", "interface-only versioned APIs",
    "latency, durability, transactions, backend capabilities or authority",
    "exact repository scope, cancellation and atomicity/CAS",
    "reliable outboxed delivery", "never introduce production mock/backend fallbacks",
    "No main-thread querying, component-side source joins",
    "exact repository path and line range at the frozen SHA",
    "counter-evidence and intentional provider limitations",
    "do not assume any unmerged refactor or other branch exists",
    "Composition roots may deliberately select concrete implementations",
    "Do not add CAP dependencies or a universal service object",
  ]) {
    assert.ok(source.includes(required), required);
  }
});

test("Calesi remediation requires a before/after regression and complete real-adapter validation", () => {
  for (const required of [
    "at most 60 implementation/test files and 10 architecture/specification files",
    "at most 100 open PRs", "15 read-only GitHub calls total",
    "Select exactly one high-confidence, reproducible violation",
    "four production files plus six corresponding test files",
    "regression test that fails before the fix and passes after it",
    "existing shared conformance suites", "each relevant real adapter",
    "Unit mocks cannot prove durability, transaction or backend conformance",
    "Failed, skipped or unavailable required checks block a PR",
    "go -C server vet ./...", "golangci-lint fmt --diff", "golangci-lint run ./...",
    "race detection", "git diff --check", "Immediately before publishing, recheck open PR overlap",
    "Call `create_pull_request` exactly once", "Call `noop` exactly once",
    "Call the inherited `report_incomplete` once instead",
    "Mark the fix as proposed, not deployed or accepted",
    "### Control Plane", "${{ inputs.correlation_id }}",
  ]) {
    assert.ok(source.includes(required), required);
  }
});

test("contributor guidance adopts all five Calesi principles without flattening CAO boundaries", () => {
  const guidance = readFileSync(join(root, "AGENTS.md"), "utf8");
  const section = guidance.split("## Service design principles\n")[1]?.split("\n## ")[0];
  assert.ok(section);
  for (const required of [
    "https://cap.cloud.sap/docs/guides/integration/calesi",
    "**Domain-level interfaces:**", "**Protocol/location-independent consumption:**",
    "**Fast, faithful local development:**", "**Interchangeable implementations:**",
    "**Deliberate extensibility:**", "not a migration to SAP CAP",
    "canonical data, operational state, policy/authorization and dispatch",
    "not a universal service object", "interface-only versioned APIs",
    "latency, durability, transactions, backend capabilities or authority",
    "reject unsupported operations", "production mock/backend fallbacks",
    "shared multi-adapter conformance tests", "atomicity, CAS and cancellation",
    "skipped or unavailable backends are incomplete evidence",
    "policy, authorization, declared safe outputs, durability or idempotency",
    "reliable outboxed delivery", "directly related regression tests",
    "do not move queries, source joins or business-state derivation",
    "Interface reuse grants no new mutation, dispatch",
  ]) {
    assert.ok(section.includes(required), required);
  }
});
