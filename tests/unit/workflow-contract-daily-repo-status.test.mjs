import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { runInNewContext } from "node:vm";
import { parse } from "yaml";
import { effectivePolicy } from "../../.github/workflows/shared/policy.mjs";
import { controlPolicy, root, targetReadCredential, workflow } from "./workflow-contract.helpers.mjs";

const source = workflow("daily-repo-status.md");
const config = (source) => parse(/^---\n([\s\S]*?)\n---/.exec(source)[1]);
const report = config(source);

function executeStep(name, { github, files = new Map(), environment = {} }) {
  const step = report.steps.find(entry => entry.name === name);
  assert.ok(step, name);
  const fs = {
    mkdirSync() {},
    readFileSync(path) {
      assert.ok(files.has(path), `unexpected file read: ${path}`);
      return files.get(path);
    },
    writeFileSync(path, body) { files.set(path, body); },
  };
  return runInNewContext(`(async () => { ${step.with.script}\n })()`, {
    require(name) { assert.equal(name, "node:fs"); return fs; },
    github,
    core: { setOutput() {} },
    context: { repo: { owner: "acme", repo: "control" }, runId: 123 },
    process: { env: environment },
  }).then(() => files);
}

test("Daily Repo Status is standalone and registered without campaign packaging or activation", () => {
  assert.equal(existsSync(join(root, "repo-status/aw.yml")), false);
  assert.equal(existsSync(join(root, "repo-status/cao.json")), false);
  assert.equal(existsSync(join(root, ".github/workflows/repo-status-report.md")), false);
  const rootManifest = parse(readFileSync(join(root, "aw.yml"), "utf8"));
  assert.equal(rootManifest.includes.includes("repo-status/aw.yml"), false);
  assert.equal(controlPolicy["control-plane"].campaigns["repo-status"], undefined);
  assert.deepEqual(controlPolicy["control-plane"].workflows["daily-repo-status"], {
    enabled: false, mode: "review", "max-mode": "review", "max-repositories": 1,
  });
});

test("Daily Repo Status preserves single-target standalone execution and shared credential/admission boundaries", () => {
  assert.match(source, /^if: needs\.pre_activation\.outputs\.cao_authorized == 'true'$/m);
  assert.match(source, /cao_precompute\.outputs\.authorized != 'false'/);
  assert.equal(report.strict, true);
  assert.equal(report.engine, "copilot");
  assert.equal(report.permissions["copilot-requests"], "write");
  const control = report.imports.find(entry => entry.uses === "shared/control.md");
  assert.equal(control.with.role, "standalone");
  assert.equal(control.with.workflow, "daily-repo-status");
  assert.equal(control.with.campaign, undefined);
  assert.equal(control.with.worker, undefined);
  for (const [permission, access] of Object.entries(report.permissions)) {
    if (access !== "read") continue;
    assert.equal(control.with[`read_${permission.replaceAll("-", "_")}`], "read", permission);
  }
  assert.match(source, /\{\{#runtime-import\? \.github\/cao\/daily-repo-status\.md\}\}/);
  assert.equal(report.on.schedule, undefined);
  assert.equal(report.on.workflow_dispatch.inputs.target_repo.required, true);
  assert.deepEqual(report.on.workflow_dispatch.inputs.safe_output_mode, {
    default: "review", type: "choice", options: ["review", "live"],
  });
  assert.equal(report.tools["repo-memory"], undefined);
  assert.equal(control.with.read_repository, "${{ inputs.target_repo }}");
  assert.equal(report.checkout, false);
  assert.equal(report.tools.github["min-integrity"], "none");
  assert.equal(report.tools.github.lockdown, false);
  assert.deepEqual(report.tools.bash, ["cat"]);
  assert.equal(report.evals, undefined);
  assert.equal(report["safe-outputs"]["dispatch-workflow"], undefined);
  for (const input of ["correlation_id", "central_repo", "control_plane_run_url", "batch_label"]) {
    assert.equal(report.on.workflow_dispatch.inputs[input], undefined);
  }
});

test("standalone target guard accepts only the admitted target, mode and output route", async () => {
  const path = "/tmp/gh-aw/agent/control-precompute.json";
  const targetPath = "/tmp/gh-aw/agent/daily-repo-status/target.json";
  const environment = {
    TARGET_REPO: "acme/target", REQUESTED_MODE: "review",
    REVIEW_OUTPUT_REPO: "acme/control", OUTPUT_REPOSITORY: "acme/control",
  };
  const target = { full_name: "acme/target", safe_output_mode: "review", safe_output_repo: "acme/control" };
  const admitted = {
    authorized: true, control_role: "standalone", workflow: "daily-repo-status",
    effective_max_repos: 1, candidate_repositories: [target],
  };
  const execute = (precompute, env = environment) => executeStep("Validate the standalone report target", {
    github: {}, files: new Map([[path, JSON.stringify(precompute)]]), environment: env,
  });
  const accepted = await execute(admitted);
  assert.deepEqual(JSON.parse(accepted.get(targetPath)), { target });
  await execute(admitted, { ...environment, TARGET_REPO: "ACME/Target", OUTPUT_REPOSITORY: "ACME/Control" });
  const live = { ...target, safe_output_mode: "live", safe_output_repo: target.full_name };
  await execute({ ...admitted, candidate_repositories: [live] }, {
    ...environment, REQUESTED_MODE: "live",
    REVIEW_OUTPUT_REPO: target.full_name, OUTPUT_REPOSITORY: target.full_name,
  });
  await assert.rejects(execute({ ...admitted, candidate_repositories: [live] }, {
    ...environment, REQUESTED_MODE: "live", OUTPUT_REPOSITORY: target.full_name,
  }), /admitted routing/, "the write credential must be configured for the actual live destination");
  const empty = await execute({ ...admitted, effective_max_repos: 0, candidate_repositories: [] });
  assert.deepEqual(JSON.parse(empty.get(targetPath)), { target: null });
  for (const invalid of [
    { ...admitted, authorized: false },
    { ...admitted, control_role: "worker" },
    { ...admitted, workflow: "another-workflow" },
    { ...admitted, candidate_repositories: null },
    { ...admitted, candidate_repositories: [target, target] },
    { ...admitted, effective_max_repos: 0 },
    { ...admitted, effective_max_repos: null },
    { ...admitted, candidate_repositories: [{ ...target, full_name: "acme/other" }] },
    { ...admitted, candidate_repositories: [{ ...target, safe_output_mode: "live" }] },
    { ...admitted, candidate_repositories: [{ ...target, safe_output_repo: "acme/other" }] },
  ]) {
    await assert.rejects(execute(invalid), /admission|admitted target|admitted routing/);
  }
});

test("compiled status outputs enforce destination routing, deduplication and guarded body-only refresh", () => {
  const compiled = parse(workflow("daily-repo-status.lock.yml"));
  const handler = compiled.jobs.safe_outputs.steps.find(step => step.env?.GH_AW_SAFE_OUTPUTS_HANDLER_CONFIG);
  assert.ok(handler);
  const outputs = JSON.parse(handler.env.GH_AW_SAFE_OUTPUTS_HANDLER_CONFIG);
  const create = report["safe-outputs"]["create-issue"];
  const update = report["safe-outputs"]["update-issue"];
  assert.equal(create["deduplicate-by-title"], true);
  assert.equal(create.expires, "3d");
  assert.equal(create.max, 1);
  assert.equal(create["close-older-issues"], undefined);
  assert.equal(update.max, 1);
  assert.equal(update.body, true);
  assert.equal(update.title, undefined);
  assert.equal(update.status, undefined);
  assert.deepEqual(update["required-labels"], ["repo-status"]);
  assert.equal(create["target-repo"], update["target-repo"]);
  assert.match(create["target-repo"], /== 'review' && \(inputs\.safe_output_repo \|\| github\.repository\) \|\| inputs\.target_repo/);
  assert.equal(outputs.create_issue.deduplicate_by_title, true);
  assert.deepEqual(outputs.update_issue.required_labels, update["required-labels"]);
  assert.equal(outputs.update_issue.allow_body, true);
  assert.equal(outputs.update_issue.allow_title, undefined);
  assert.equal(outputs.update_issue.allow_status, undefined);
  assert.equal(outputs.noop["report-as-issue"], "false");
  assert.equal(report["safe-outputs"]["close-issue"], undefined);
  assert.equal(create["target-repo"], report.steps[0].env.OUTPUT_REPOSITORY);
  const guard = compiled.jobs.agent.steps.findIndex(step => step.id === "repo_status_target");
  const window = compiled.jobs.agent.steps.findIndex(step => step.name === "Fix the daily reporting window");
  assert.ok(guard >= 0 && window > guard, "routing guard runs before report evidence collection");
  assert.match(source, /Emit exactly one of those operations, never both/);
  assert.match(source, /render target item references as inline code, not links/);
  assert.match(source, /fully read quiet day|every category was completely read/);
  assert.match(source, /Missing required API access or failed[\s\S]*`report_incomplete`/);
});

test("report windows use the original run time across midnight, leap days and year boundaries", async () => {
  for (const [createdAt, start, end] of [
    ["2026-10-21T00:00:00Z", "2026-10-20T00:00:00.000Z", "2026-10-21T00:00:00.000Z"],
    ["2026-01-01T23:59:59Z", "2025-12-31T00:00:00.000Z", "2026-01-01T00:00:00.000Z"],
    ["2024-03-01T15:15:15Z", "2024-02-29T00:00:00.000Z", "2024-03-01T00:00:00.000Z"],
  ]) {
    const github = { rest: { actions: { async getWorkflowRun(parameters) {
      assert.deepEqual(JSON.parse(JSON.stringify(parameters)), { owner: "acme", repo: "control", run_id: 123 });
      return { data: { created_at: createdAt } };
    } } } };
    const files = await executeStep("Fix the daily reporting window", { github });
    const window = JSON.parse(files.get("/tmp/gh-aw/agent/daily-repo-status/window.json"));
    assert.deepEqual(window, { date: start.slice(0, 10), start, end, observed_at: createdAt });
  }
  await assert.rejects(executeStep("Fix the daily reporting window", {
    github: { rest: { actions: { async getWorkflowRun() { return { data: {} }; } } } },
  }), /Missing workflow creation time/);
});

test("report lookups are destination-bound, include closed duplicates and fail closed on missing access or truncation", async () => {
  const windowPath = "/tmp/gh-aw/agent/daily-repo-status/window.json";
  const reportPath = "/tmp/gh-aw/agent/daily-repo-status/reports.json";
  const files = () => new Map([[windowPath, JSON.stringify({ start: "2026-10-20T00:00:00.000Z" })]]);
  const calls = [];
  const result = await executeStep("Read existing owned reports", {
    files: files(), environment: { OUTPUT_REPOSITORY: "acme/control" },
    github: { rest: { issues: { async listForRepo(parameters) {
      calls.push(JSON.parse(JSON.stringify(parameters)));
      return { data: [
        { number: 1, title: "acme/a repository status", body: "owned a", state: parameters.state, labels: ["repo-status"] },
        { number: 2, title: "acme/b repository status", body: "owned b", state: parameters.state, labels: [{ name: "repo-status" }] },
        { number: 3, pull_request: {}, labels: [] },
      ] };
    } } } },
  });
  assert.equal(calls.length, 2);
  assert.equal(calls[0].owner, "acme");
  assert.equal(calls[0].repo, "control");
  assert.equal(calls[0].labels, "repo-status");
  assert.equal(calls[0].state, "open");
  assert.equal(calls[1].state, "closed");
  assert.equal(calls[1].since, "2026-10-20T00:00:00.000Z");
  const reports = JSON.parse(result.get(reportPath));
  assert.equal(reports.complete, true);
  assert.equal(reports.repository, "acme/control");
  assert.equal(reports.reports.length, 4);
  assert.equal(reports.reports.some(report => report.number === 3), false);

  for (const failure of ["access", "truncation"]) {
    const output = files();
    let requests = 0;
    await assert.rejects(executeStep("Read existing owned reports", {
      files: output, environment: { OUTPUT_REPOSITORY: "acme/control" },
      github: { rest: { issues: { async listForRepo() {
        requests += 1;
        if (failure === "access") throw new Error("403 forbidden");
        return { data: Array.from({ length: 100 }, (_, number) => ({ number, labels: [] })) };
      } } } },
    }), failure === "access" ? /403 forbidden/ : /exceeded its open issue bound/);
    assert.equal(requests, failure === "access" ? 1 : 2);
    assert.equal(output.has(reportPath), false, "failure must not publish success-shaped evidence");
  }
  const lookup = report.steps.find(step => step.name === "Read existing owned reports");
  assert.equal(lookup.if, "${{ steps.repo_status_target.outputs.eligible == 'true' }}");
  assert.equal(lookup.with["github-token"], `\${{ (inputs.safe_output_mode || 'review') == 'review' && (inputs.safe_output_repo || github.repository) == github.repository && github.token || ${targetReadCredential.slice(4)}`);
});

test("Daily Repo Status quickstart is discoverable, review-first and honest about operational value", () => {
  const guide = readFileSync(join(root, "docs/setup/quickstart.md"), "utf8");
  const astroConfig = readFileSync(join(root, "astro.config.mjs"), "utf8");
  assert.match(astroConfig, /label: "Setup"[\s\S]*?label: "Quick Start", link: "\/setup-quickstarts\/"/);
  assert.match(astroConfig, /label: "Gallery"[\s\S]*?label: "Daily Repo Status", link: "\/setup\/quickstart\/"/);
  assert.doesNotMatch(guide, /CAO recording not yet available|Video TODO/);
  assert.match(guide, /\*\*Estimated time: 15 minutes after setup\*\*/);
  assert.match(guide, /## Prerequisites/);
  assert.match(guide, /Complete \[Set Up CAO\]\(\.\.\/setup-quickstarts\.md\)/);
  assert.doesNotMatch(guide, /install-gh-cao\.sh|gh cao setup|gh cao validate|Review and apply/);
  assert.match(guide, /Reuse your setup/);
  assert.deepEqual([...guide.matchAll(/^## Step \d - (.+)$/gm)].map(match => match[1]), [
    "Add and review the sample workflow",
    "Request one bounded review",
    "Wait for the run and inspect its result",
    "Customize your report (optional)",
  ]);
  for (const anchor of [
    "step-1---set-up-cao-and-monitor-one-repository",
    "step-2---add-and-review-the-sample-workflow",
    "step-3---request-one-bounded-review",
    "step-4---wait-for-the-run-and-inspect-its-result",
    "step-5---customize-your-report-optional",
  ]) {
    assert.ok(guide.includes(`<span id="${anchor}"></span>`), anchor);
  }
  assert.match(guide, /## Continue learning/);
  assert.doesNotMatch(guide, /<video|<iframe|youtube\.com|youtu\.be|\.mp4/);
  assert.match(guide, /gh cao add daily-repo-status/);
  assert.match(guide, /No manual registration is needed/);
  assert.match(guide, /control-plane\.workflows/);
  assert.doesNotMatch(guide, /gh cao add \S+\/\S+|gh cao campaigns mode|gh workflow enable/);
  assert.match(guide, /manual-only/);
  const registration = JSON.parse(/```json\n([\s\S]*?)\n```/.exec(guide)[1]);
  assert.deepEqual(registration, {
    "daily-repo-status": { mode: "review", "max-mode": "review", "max-repositories": 1 },
  });
  const resolved = effectivePolicy({
    "control-plane": {
      scope: { "allowed-owners": ["acme"], "allowed-repositories": ["acme/control", "acme/target"] },
      workflows: registration,
    },
  }, {
    role: "standalone", workflowName: "daily-repo-status",
    controlRepository: "acme/control", targetRepository: "acme/target",
  });
  assert.equal(resolved.authorized, true);
  assert.equal(resolved.enabled, true);
  assert.equal(resolved.safe_output_mode, "review");
  assert.equal(resolved.max_repositories, 1);
  assert.equal(resolved.rollout_percent, 100);
  assert.match(guide, /even\nwhen global defaults are live/);
  assert.match(guide, /Registered workflows are enabled/);
  assert.doesNotMatch(guide, /Once reviewed, change only|policy capped at review/);
  assert.match(guide, /--raw-field safe_output_mode="review"/);
  assert.match(guide, /--raw-field max_repos="1"/);
  assert.match(guide, /gh issue list --repo CONTROL_OWNER\/CONTROL_REPOSITORY/);
  assert.match(guide, /Operational value is not measurable yet/);
  assert.equal(report.model, "gpt-5.3-codex");
  assert.equal(report.name, "Daily Repo Status");
  assert.equal(report["tracker-id"], "daily-repo-status");
  assert.match(report["run-name"], /^Daily Repo Status/);
  assert.match(source, /ae8d551f07c7ed7619f8c58c7bb4c3ac89395d38\/workflows\/repo-status\.md/);
  assert.match(source, /Copyright \(c\) 2025 GitHub/);
  assert.match(source, /The above copyright notice and this permission notice shall be included in all/);
});
