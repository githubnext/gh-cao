import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { effectivePolicy, parsePolicy } from "../../.github/workflows/shared/policy.mjs";
import { controlPolicy, generatedJobs, root, transitivelyNeeds, workflow } from "./workflow-contract.helpers.mjs";

const id = "self-care-database-architecture";
const source = workflow(`${id}.md`);
const config = parse(/^---\n([\s\S]*?)\n---/.exec(source)[1]);
const orchestrator = workflow("self-care.md");
const dispatch = parse(/^---\n([\s\S]*?)\n---/.exec(orchestrator)[1]);
const guidePath = "docs/dashboard-data-architecture.md";
const guide = readFileSync(join(root, guidePath), "utf8");

test("database architecture worker is registered without widening SelfCare authority", () => {
  const descriptor = JSON.parse(readFileSync(join(root, "self-care/cao.json"), "utf8"));
  const manifest = parse(readFileSync(join(root, "self-care/aw.yml"), "utf8"));
  const campaign = controlPolicy["control-plane"].campaigns["self-care"];
  assert.equal(descriptor.workers["database-architecture"], id);
  assert.ok(manifest.includes.includes(`.github/workflows/${id}.md`));
  assert.deepEqual(campaign.workers["database-architecture"], { workflow: id, enabled: true });
  assert.equal(campaign.mode, "review");
  assert.equal(campaign["max-repositories"], 1);
  assert.deepEqual(campaign.targets, { "githubnext/gh-cao": { mode: "live" } });
  const workers = Object.values(descriptor.workers);
  assert.deepEqual([...dispatch["safe-outputs"]["dispatch-workflow"].workflows].sort(), workers.sort());
  assert.equal(dispatch["safe-outputs"]["dispatch-workflow"].max, workers.length);
  assert.equal(dispatch.imports[0].with.dispatch_max, workers.length);
  assert.equal(dispatch.imports[0].with.worker_credits_per_target, 5500 + config["max-ai-credits"]);

  for (const enabled of [true, false]) {
    const policy = structuredClone(controlPolicy);
    policy["control-plane"].campaigns["self-care"].workers["database-architecture"].enabled = enabled;
    const result = effectivePolicy(parsePolicy(JSON.stringify(policy)), {
      campaignName: "self-care", role: "worker", workerName: "database-architecture",
      controlRepository: "githubnext/gh-cao", targetRepository: "githubnext/gh-cao",
      requestedMode: "live", eventName: "workflow_dispatch",
    });
    assert.equal(result.authorized, enabled);
  }
});

test("database architecture cadence is daily through the existing orchestrator only", () => {
  assert.equal(dispatch.on.schedule, "every 20 minutes");
  assert.equal(config.on.schedule, undefined);
  assert.equal(config["tracker-id"], id);
  assert.equal(config.on["skip-if-match"], `is:pr is:open "gh-aw-workflow-id: ${id}" in:body`);
  assert.match(orchestrator, /Dispatch `self-care-database-architecture` only when no run is queued, in\nprogress, or started during the preceding 24 hours/);
  assert.match(orchestrator, /failed and cancelled runs/);
  assert.match(source, /excluding this\nrun/);
  assert.match(source, /truncated before\nthe time boundary, or ambiguous/);
  assert.match(source, /do not restrict detection to\n\s+changes merged in the preceding day/);
  assert.match(source, /existing overlapping guide PR blocks another proposal/);
});

test("database architecture publication is admission-gated and documentation-only", () => {
  assert.equal(config.imports[0].with.campaign, "self-care");
  assert.equal(config.imports[0].with.role, "worker");
  assert.equal(config.imports[0].with.worker, "database-architecture");
  assert.equal(config.if, "needs.pre_activation.outputs.cao_authorized == 'true' && inputs.target_repo == 'githubnext/gh-cao' && inputs.safe_output_mode == 'live'");
  for (const permission of ["actions", "contents", "pull-requests"]) {
    assert.equal(config.permissions[permission], "read");
  }
  const output = config["safe-outputs"]["create-pull-request"];
  assert.deepEqual(output["allowed-files"], [guidePath]);
  assert.equal(output["max-patch-files"], 1);
  assert.equal(output.max, 1);
  assert.equal(output.draft, true);
  assert.equal(output["fallback-as-issue"], false);
  assert.equal(output["protected-files"], "blocked");
  assert.match(source, /npm run docs:build/);
  assert.match(source, /npm run compile/);
  assert.match(source, /--grep "data architecture"/);
  assert.match(source, /Failed, missing, skipped, or incomplete validation blocks a PR/);
  assert.match(source, /date\/version-only churn/);
  const jobs = generatedJobs(workflow(`${id}.lock.yml`));
  assert.ok(transitivelyNeeds(jobs, "agent", "pre_activation"));
  assert.match(jobs.get("activation").block, /inputs\.target_repo == 'githubnext\/gh-cao'/);
  assert.match(jobs.get("activation").block, /inputs\.safe_output_mode == 'live'/);
});

test("cloud-architect guide is discoverable, diagram-rich, and preserves evidence qualifications", () => {
  const diagrams = [...guide.matchAll(/```mermaid\n([\s\S]*?)```/g)];
  assert.ok(diagrams.length >= 10);
  for (const [, diagram] of diagrams) {
    assert.match(diagram, /^\s*accTitle: .+/m);
    assert.match(diagram, /^\s*accDescr: .+/m);
  }
  for (const kind of ["flowchart", "sequenceDiagram", "erDiagram"]) {
    assert.ok(diagrams.some(([, diagram]) => diagram.trimStart().startsWith(kind)));
  }
  assert.match(guide, /not generation-atomic/);
  assert.match(guide, /five minutes after admission/);
  assert.match(guide, /conflicting summary-retention/);
  assert.match(guide, /does not promise a numerical availability SLO, RPO, or RTO/);
  assert.match(guide, /does not silently choose a new retention policy/);
  assert.match(guide, /unweighted\s+sample counts/i);
  assert.match(guide, /do not fall back to Pages/);
  assert.match(readFileSync(join(root, "astro.config.mjs"), "utf8"), /link: "\/dashboard-data-architecture\/"/);
  assert.match(readFileSync(join(root, "docs/dashboard-data-model.md"), "utf8"), /dashboard-data-architecture\.md/);
});
