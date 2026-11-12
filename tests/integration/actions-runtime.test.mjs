import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { compile, fixture, image, prepareActions, prepareTools, root, run, simulate } from "../helpers/actions-runtime/index.mjs";

test("signed gateway delivery runs compiled AWI and CAO samples in Docker", { timeout: 600_000 }, () => {
  run("docker", ["image", "inspect", image]);
  const parent = join(root, ".tmp/actions-runtime");
  mkdirSync(parent, { recursive: true });
  const directory = mkdtempSync(join(parent, "e2e-"));
  const workspace = join(directory, "workspace");
  fixture(workspace);
  compile(workspace, ["sample", "cao"]);
  const upstream = prepareActions(workspace, ".github/workflows/sample.lock.yml", parent);
  const tools = prepareTools(parent);
  const eventFile = join(directory, "event.json");
  run("go", ["-C", "server", "test", "./internal/webhookgateway", "-run", "^TestGitHubSimulatorDockerDispatch$",
    "-count=1", "-timeout=90s"], {
    cwd: root,
    env: { ...process.env, CAO_ACTIONS_SIMULATOR_POSTGRES: "1",
      CAO_DOCKER_ACTIONS_WORKSPACE: workspace, CAO_DOCKER_ACTIONS_EVENT: eventFile },
  });
  const event = JSON.parse(readFileSync(eventFile, "utf8"));
  assert.equal(event.action, "gh-aw-webhook-9f86d081884c7d659a2feaa0");
  assert.equal(event.client_payload.aw_context.webhook.event, "dependabot_alert");
  assert.equal(event.client_payload.aw_context.webhook.deliveries.length, 1);
  assert.ok(!JSON.stringify(event).includes("MUST-NOT-PERSIST"));
  for (const name of ["sample", "cao"]) {
    const output = join(directory, name);
    const report = simulate({ workspace, workflow: `.github/workflows/${name}.lock.yml`, event, output, upstream, tools,
      vars: { GH_AW_GITHUB_AUTH_MODE: "workflow-token" } });
    assert.deepEqual(report.identity, { uid: process.getuid(), gid: process.getgid() });
    for (const id of ["pre_activation", "activation", "agent", "safe_outputs", "conclusion"]) {
      assert.equal(report.jobs[id].result, "success", `${name}/${id}`);
    }
    assert.equal(report.jobs.agent.steps.agentic_execution.outcome, "success");
    assert.equal(report.jobs.safe_outputs.steps.process_safe_outputs.outcome, "success");
    assert.equal(report.jobs.safe_outputs.steps.process_safe_outputs.outputs.items_succeeded, "1");
    assert.deepEqual(report.unhandled, []);
    assert.ok(report.requests.every(request => request.method === "GET"));
    const info = JSON.parse(readFileSync(join(output, "artifacts/activation/aw_info.json"), "utf8"));
    assert.equal(info.dry_run === true, name === "sample");
    assert.equal(info.staged, true);
    assert.equal(info.context.items[0].url, event.client_payload.aw_context.webhook.deliveries[0].payload.url);
    const agent = JSON.parse(readFileSync(join(output, "artifacts/agent/agent_output.json"), "utf8"));
    assert.equal(agent.items.length, 1);
    assert.equal(agent.items[0].type, "create_issue");
    assert.equal(agent.items[0].title, name === "sample" ? "Simulator deterministic report" : "Simulator CAO review report");
    if (name === "cao") {
      assert.equal(report.jobs.pre_activation.steps.cao_admission.outputs.authorized, "true");
      assert.equal(report.jobs.pre_activation.steps.cao_precompute_validation.outcome, "success");
      const precompute = JSON.parse(readFileSync(join(output, "artifacts/cao-control-precompute/control-precompute.json"), "utf8"));
      assert.equal(precompute.authorized, true);
      assert.equal(precompute.control_role, "standalone");
      assert.equal(precompute.safe_output_mode, "review");
      assert.deepEqual(precompute.candidate_repositories.map(repository => repository.full_name), ["simulator/repo-00001"]);
      assert.ok(report.requests.some(request => request.path.endsWith("/contents/.github/workflows/cao.json") &&
        request.ref === precompute.policy_source.sha));
    }
  }
  const caoPolicy = join(workspace, ".github/workflows/cao.json");
  const policy = JSON.parse(readFileSync(caoPolicy, "utf8"));
  policy["control-plane"].workflows.cao.enabled = false;
  writeFileSync(caoPolicy, JSON.stringify(policy));
  run("git", ["add", ".github/workflows/cao.json"], { cwd: workspace });
  run("git", ["-c", "user.name=Actions simulator", "-c", "user.email=simulator@example.invalid",
    "commit", "--quiet", "--no-gpg-sign", "-m",
    "Disable the synthetic CAO workflow\n\nCo-authored-by: Copilot App <223556219+Copilot@users.noreply.github.com>"], { cwd: workspace });
  const deniedOutput = join(directory, "denied");
  const denied = simulate({ workspace, workflow: ".github/workflows/cao.lock.yml", event, output: deniedOutput, upstream, tools });
  assert.equal(denied.jobs.pre_activation.steps.cao_admission.outputs.authorized, "false");
  assert.equal(denied.jobs.agent.result, "skipped");
  assert.equal(denied.jobs.safe_outputs.result, "skipped");
  const admission = JSON.parse(readFileSync(join(deniedOutput, "artifacts/cao-admission/admission.json"), "utf8"));
  assert.equal(admission.authorized, false);
  assert.equal(admission.reason, "workflow-disabled");

  const sampleLock = join(workspace, ".github/workflows/sample.lock.yml"), original = readFileSync(sampleLock, "utf8");
  for (const [name, replacement, message] of [
    ["live", original.replace('"dry_run":true', '"dry_run":false'), /Only dry-run locks/],
    ["unstaged", original.replaceAll('GH_AW_SAFE_OUTPUTS_STAGED: "true"', 'GH_AW_SAFE_OUTPUTS_STAGED: "false"'), /unconditionally staged/],
    ["inference", original.replaceAll("GH_AW_SAMPLES:", "NO_SAMPLES:"), /Compiled sample replay is required/],
    ["unsupported-action", original.replaceAll("actions/checkout@", "unapproved/action@"), /Unsupported or unpinned action/],
  ]) {
    assert.notEqual(replacement, original, `${name} must alter the compiled fixture`);
    writeFileSync(sampleLock, replacement);
    assert.throws(() => simulate({ workspace, workflow: ".github/workflows/sample.lock.yml", event,
      output: join(directory, name), upstream, tools }), message);
  }
  writeFileSync(sampleLock, original);
  rmSync(directory, { recursive: true, force: true });
});
