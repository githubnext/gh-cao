import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";

import { controlPolicy, root, workflow } from "./workflow-contract.helpers.mjs";

const campaign = "repository-ownership";
const workers = ["discovery", "enforcement"];
const ids = [campaign, ...workers.map((worker) => `${campaign}-${worker}`)];
const reviewDestination = "${{ (inputs.safe_output_mode || 'review') == 'review' && (inputs.safe_output_repo || github.repository) || inputs.target_repo }}";

function source(id) {
  const text = workflow(`${id}.md`);
  const match = /^---\n([\s\S]*?)\n---\n/.exec(text);
  assert.ok(match, `${id} must have frontmatter`);
  return { text, frontmatter: parse(match[1]), body: text.slice(match[0].length) };
}

function lock(id) {
  return parse(workflow(`${id}.lock.yml`));
}

function needs(job) {
  return Array.isArray(job.needs) ? job.needs : [job.needs];
}

function handlerConfig(id) {
  const step = lock(id).jobs.safe_outputs.steps.find((item) => item.env?.GH_AW_SAFE_OUTPUTS_HANDLER_CONFIG);
  assert.ok(step, `${id} must have compiled safe-output handler configuration`);
  return JSON.parse(step.env.GH_AW_SAFE_OUTPUTS_HANDLER_CONFIG);
}

test("ownership package and policy declare disabled review-capped workers", () => {
  const manifest = parse(readFileSync(join(root, campaign, "aw.yml"), "utf8"));
  const declaration = JSON.parse(readFileSync(join(root, campaign, "cao.json"), "utf8"));
  const policy = controlPolicy["control-plane"].campaigns[campaign];

  assert.equal(manifest.name, "Repository Ownership");
  assert.equal(manifest.experimental, true);
  assert.equal(manifest["min-version"], controlPolicy["gh-aw-version"]);
  assert.deepEqual(manifest.includes.toSorted(), ids.map((id) => `.github/workflows/${id}.md`).sort());
  assert.equal(manifest.resources, undefined, "the parser is not packaged as an implicit runtime resource");
  assert.deepEqual(declaration, {
    campaign,
    orchestrator: campaign,
    workers: Object.fromEntries(workers.map((worker) => [worker, `${campaign}-${worker}`])),
  });
  assert.equal(policy.enabled, false);
  assert.equal(policy.mode, "review");
  assert.equal(policy["max-repositories"], 1);
  assert.deepEqual(Object.keys(policy.workers).sort(), workers.toSorted());
  for (const worker of workers) {
    assert.deepEqual(policy.workers[worker], {
      workflow: `${campaign}-${worker}`,
      enabled: false,
      "max-mode": "review",
    });
  }
  assert.equal(controlPolicy["target-authority"]?.campaigns?.[campaign], undefined);
  for (const target of Object.values(policy.targets ?? {})) {
    assert.notEqual(target.mode, "live");
  }
});

test("source and lock fail closed before activation independently of CAO admission", () => {
  for (const id of ids) {
    const { frontmatter: fm } = source(id);
    const compiled = lock(id);
    assert.equal(fm.if, "needs.pre_activation.outputs.cao_authorized == 'true'", id);
    assert.match(fm.jobs["pre-activation"].outputs.cao_authorized, /cao_admission\.outputs\.authorized == 'true'/);
    assert.match(fm.jobs["pre-activation"].outputs.cao_authorized, /cao_precompute\.outputs\.authorized != 'false'/);
    const sourceStops = fm.jobs["pre-activation"]["pre-steps"].filter((step) => step.name === "Block ownership campaign activation");
    const lockStops = compiled.jobs.pre_activation.steps.filter((step) => step.name === "Block ownership campaign activation");
    assert.equal(sourceStops.length, 1, id);
    assert.deepEqual(lockStops, sourceStops, id);
    const [stop] = lockStops;
    assert.equal(stop.if, undefined, id);
    assert.equal(stop["continue-on-error"], undefined, id);
    assert.match(stop.run, /^exit 1\s*$/m, id);
    assert.equal(compiled.jobs.pre_activation["continue-on-error"], undefined, id);
    assert.equal(
      compiled.jobs.activation.if,
      "needs.pre_activation.outputs.activated == 'true' && (needs.pre_activation.outputs.cao_authorized == 'true')",
      id,
    );
    assert.ok(needs(compiled.jobs.activation).includes("pre_activation"), id);
    assert.ok(needs(compiled.jobs.agent).includes("activation"), id);
    assert.ok(needs(compiled.jobs.safe_outputs).includes("agent"), id);
    assert.match(compiled.jobs.safe_outputs.if, /needs\.agent\.result != 'skipped'/, id);
    assert.equal(Object.keys(compiled.jobs).some((job) => /memory/i.test(job)), false, id);
    assert.equal(fm.tools["repo-memory"], undefined, id);
  }
});

test("orchestrator and workers import shared control with exact roles and bounded routing", () => {
  const orchestrator = source(campaign).frontmatter;
  assert.equal(orchestrator.on.workflow_dispatch.inputs.safe_output_mode.default, "review");
  assert.equal(orchestrator.on.workflow_dispatch.inputs.max_repos.default, 1);
  const dispatch = orchestrator["safe-outputs"]["dispatch-workflow"];
  assert.deepEqual(dispatch.workflows, workers.map((worker) => `${campaign}-${worker}`));
  assert.equal(dispatch.max, 2);
  assert.equal(orchestrator["safe-outputs"]["threat-detection"], false);
  const compiledDispatch = handlerConfig(campaign).dispatch_workflow;
  assert.deepEqual(compiledDispatch.workflows, dispatch.workflows);
  assert.equal(compiledDispatch.max, 2);
  assert.deepEqual(compiledDispatch.allowed_refs, ["refs/heads/${{ github.event.repository.default_branch }}"]);

  for (const id of ids) {
    const { frontmatter: fm, body } = source(id);
    const worker = workers.find((name) => id === `${campaign}-${name}`);
    const control = fm.imports.find((entry) => entry.uses === "shared/control.md");
    assert.ok(control, id);
    assert.equal(control.with.campaign, campaign);
    assert.equal(control.with.role, worker ? "worker" : "orchestrator");
    assert.equal(control.with.read_repository, worker ? "${{ inputs.target_repo }}" : "${{ github.repository }}");
    assert.equal(control.with.worker, worker);
    for (const [permission, value] of Object.entries(fm.permissions)) {
      if (value === "read") assert.equal(control.with[`read_${permission.replaceAll("-", "_")}`], "read", id);
    }
    assert.match(body, /\/tmp\/gh-aw\/agent\/control-precompute\.json/);
    assert.ok(body.trimEnd().endsWith("{{#runtime-import? .github/cao/repository-ownership.md}}"), id);
    if (!worker) {
      assert.equal(control.with.dispatch_max, 2);
      assert.match(body, /at most once per `\(worker, target, effective mode\)`/);
      assert.match(body, /never promote review to live/i);
      continue;
    }
    const inputs = fm.on.workflow_dispatch.inputs;
    for (const name of ["target_repo", "safe_output_repo", "safe_output_mode"]) {
      assert.equal(inputs[name].required, true, `${id}: ${name}`);
      assert.equal(inputs[name].type, "string", `${id}: ${name}`);
    }
    assert.equal(inputs.safe_output_mode.default, undefined);
    for (const name of ["correlation_id", "central_repo", "control_plane_run_url", "batch_label"]) {
      assert.equal(inputs[name].type, "string", `${id}: ${name}`);
      assert.notEqual(inputs[name].required, true, `${id}: ${name} must allow debug`);
    }
    assert.equal(fm.env.SAFE_OUTPUT_REPO, reviewDestination);
    assert.equal(fm["safe-outputs"]["create-issue"]["target-repo"], reviewDestination);
    assert.match(body, /Never discover (?:other )?repositories or dispatch work/i);
  }
});

test("only bounded safe outputs can mutate and agent permissions remain read-only", () => {
  for (const id of ids) {
    const { frontmatter: fm, body } = source(id);
    const compiled = lock(id);
    const expected = id === campaign ? ["dispatch-workflow", "threat-detection"] : ["create-issue", "mentions"];
    assert.deepEqual(Object.keys(fm["safe-outputs"]).sort(), expected.sort(), id);
    assert.equal(fm.strict, true);
    assert.equal(fm.tools.github.mode, "gh-proxy");
    assert.equal(fm.tools["repo-memory"], undefined);
    assert.deepEqual(Object.keys(fm.jobs), ["pre-activation"]);
    for (const key of ["steps", "post-steps", "pre-steps", "pre-agent-steps"]) {
      assert.equal(fm[key], undefined, `${id}: ${key}`);
    }
    for (const [permission, value] of Object.entries(compiled.jobs.agent.permissions)) {
      if (permission !== "copilot-requests") assert.equal(value, "read", `${id}: ${permission}`);
    }
    assert.doesNotMatch(body, /\bgh\s+(?:api|issue|pr|repo|workflow)\s+(?:create|edit|delete|close|archive|run)\b/i);
    assert.doesNotMatch(body, /\bgh\s+api\b[^\n]*(?:--method|-X)\s*(?:POST|PATCH|PUT|DELETE)\b/i);
  }
});

test("worker issues are bounded, deduplicated and require human confirmation", () => {
  for (const worker of workers) {
    const id = `${campaign}-${worker}`;
    const { frontmatter: fm, body } = source(id);
    const issue = fm["safe-outputs"]["create-issue"];
    assert.equal(issue["deduplicate-by-title"], true);
    assert.equal(issue.max, 1);
    assert.equal(issue.expires, "14d");
    assert.equal(issue["title-prefix"], `[${campaign}:${worker}] `);
    assert.deepEqual(issue.labels, [campaign, `${campaign}:${worker}`]);
    assert.equal(issue["close-older-issues"], undefined);
    assert.equal(fm["safe-outputs"].mentions, false);
    const compiledIssue = handlerConfig(id).create_issue;
    assert.equal(compiledIssue.deduplicate_by_title, true);
    assert.equal(compiledIssue.max, 1);
    assert.equal(compiledIssue["target-repo"], reviewDestination);
    assert.equal(compiledIssue.title_prefix, issue["title-prefix"]);
    assert.deepEqual(compiledIssue.labels, issue.labels);
    assert.match(body, /Search open campaign\/worker\s+issues in `SAFE_OUTPUT_REPO`/);
    assert.match(body, /explicit human confirmation/i);
    assert.match(body, /reviewed mapping change/i);
    assert.match(body, /report_incomplete/);
    assert.match(body, /noop/);
  }
  const discovery = source(`${campaign}-discovery`).body;
  const enforcement = source(`${campaign}-enforcement`).body;
  assert.match(discovery, /`TARGET_REPO ownership confirmation`/);
  assert.match(discovery, /absent mapping is unknown, not unowned/i);
  assert.match(discovery, /Do not present an inferred owner as confirmed/i);
  assert.match(enforcement, /`TARGET_REPO ownership review`/);
  assert.match(enforcement, /No issue closure is authorized by missing or stale\s+evidence/i);
  assert.match(enforcement, /exemption declaration alone is not authority/i);
  assert.match(enforcement, /Do not archive repositories, change properties, modify mappings/i);
});
