import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { planDigest, readyTasks, systemReadScope, taskComplete, taskMarker, validateDispatch, validateOutputs, validatePlan } from "../../modernization/engagement.mjs";
import { boundedPages, collectEvidence } from "../../modernization/github.mjs";

const sha = "a".repeat(40);
const digest = "b".repeat(64);
const candidates = [
  { full_name: "octodemo/api", safe_output_mode: "live", safe_output_repo: "octodemo/api" },
  { full_name: "octodemo/web", safe_output_mode: "review", safe_output_repo: "octodemo/control" },
];
function plan() {
  const task = (id, repository, depends_on) => ({
    id, repository, title: `Modernize ${id}`, objective: "Preserve the versioned system contract",
    acceptance: ["Contract test passes"], depends_on, specialists: ["architect"],
  });
  return {
    schema: "cao-modernization/v1", id: "zava", objective: "Coordinate the API and frontend",
    repositories: candidates.map(entry => ({ repository: entry.full_name, base_sha: sha, base_branch: "main" })),
    tasks: [task("api", "octodemo/api", []), task("web", "octodemo/web", ["api"])],
    integration_acceptance: ["Frontend passes end-to-end tests against the merged API"],
  };
}

test("reviewed plans reject out-of-scope targets, cycles, unknown specialists and unsupported authority", () => {
  assert.equal(validatePlan(plan(), candidates.map(entry => entry.full_name), ["architect"]).id, "zava");
  for (const change of [
    value => { value.repositories[0].repository = "unauthorized/api"; },
    value => { value.tasks[0].depends_on = ["web"]; },
    value => { value.tasks[0].depends_on = ["missing"]; },
    value => { value.tasks[0].specialists = ["invented"]; },
    value => { value.tasks[0].acceptance = []; },
    value => { value.mode = "live"; },
    value => { value.repositories[0].base_sha = "main"; },
    value => { delete value.id; },
    value => { value.tasks[0].id = undefined; },
    value => { value.tasks[0].repository = null; },
    value => { value.tasks[0].specialists = ["architect", "architect"]; },
  ]) {
    const value = plan();
    change(value);
    assert.throws(() => validatePlan(value, candidates.map(entry => entry.full_name), ["architect"]), /Modernization/);
  }
  const reordered = Object.fromEntries(Object.entries(plan()).reverse());
  assert.equal(planDigest(reordered), planDigest(plan()));
  const changed = plan();
  changed.tasks[0].acceptance.push("Compatibility verified");
  assert.notEqual(planDigest(changed), planDigest(plan()));
});

test("task selection files issues first and gates implementation on completed dependencies", () => {
  const evidence = { api: { base_sha: sha }, web: { base_sha: sha } };
  let selected = readyTasks(plan(), evidence, candidates, 3);
  assert.deepEqual(selected.ready.map(entry => [entry.task, entry.phase]), [["api", "issue"], ["web", "issue"]]);
  evidence.web.issue = 5;
  evidence.api.issue = 4;
  assert.equal(readyTasks(plan(), evidence, candidates, 3).ready[0].phase, "implement");
  evidence.api.active_pull = true;
  assert.equal(readyTasks(plan(), evidence, candidates, 3).ready.length, 0);
  evidence.api.complete = true;
  selected = readyTasks(plan(), evidence, candidates, 3);
  assert.deepEqual(selected.ready.map(entry => [entry.task, entry.mode, entry.destination]), [["web", "review", "octodemo/control"]]);
  evidence.web.closed_issue = true;
  assert.equal(readyTasks(plan(), evidence, candidates, 3).ready.length, 0);
  evidence.web.complete = true;
  assert.equal(readyTasks(plan(), evidence, candidates, 3).complete, true);
  assert.equal(readyTasks(plan(), evidence, candidates, 0).ready.length, 0);
  const repeatedRepository = plan();
  repeatedRepository.tasks[1].repository = repeatedRepository.tasks[0].repository;
  assert.equal(readyTasks(repeatedRepository, { api: {}, web: {} }, candidates, 3).ready.length, 1);
});

test("native system credentials narrow to the complete requested admitted set and fail on missing targets or mixed secrets", () => {
  const admitted = candidates.map(candidate => ({ ...candidate, safe_output_mode: "review" }));
  const precompute = { authorized: true, control_role: "standalone", workflow: "modernization-system",
    candidate_repositories: admitted, effective_max_repos: 2 };
  const scope = systemReadScope(precompute, "Octodemo/api, octodemo/web", "app");
  assert.equal(scope.owner, "octodemo");
  assert.equal(scope.repositories, "api,web");
  assert.equal(systemReadScope(precompute, "octodemo/api", "app").admitted.length, 1);
  assert.throws(() => systemReadScope(precompute, "octodemo/api,octodemo/missing", "app"), /complete system/);
  assert.throws(() => systemReadScope({ ...precompute, effective_max_repos: 1 }, "octodemo/api,octodemo/web", "app"), /complete system/);
  assert.throws(() => systemReadScope(precompute, "octodemo/api,octodemo/api", "app"), /invalid explicit/);
  assert.throws(() => systemReadScope(precompute, "octodemo/api", "workflow-token"), /credentials/);
  assert.throws(() => systemReadScope(precompute, "octodemo/api,octodemo/web", "pat",
    { "octodemo/api": "API_READ", "octodemo/web": "WEB_READ" }), /one explicitly mapped/);
  assert.equal(systemReadScope(precompute, "octodemo/api,octodemo/web", "pat",
    { "octodemo/api": "FLEET_READ", "octodemo/web": "FLEET_READ" }).secret, "FLEET_READ");
});

test("merge completion requires current-head independent approval and successful complete check evidence", () => {
  const pull = { merged_at: "2026-01-01", head: { sha, repo: { full_name: "octodemo/api" } },
    base: { repo: { full_name: "octodemo/api" } }, user: { login: "author" } };
  const approval = { state: "APPROVED", commit_id: sha, user: { login: "reviewer" } };
  const checks = [{ status: "completed", conclusion: "success" }];
  assert.equal(taskComplete(pull, [approval], checks, []), true);
  assert.equal(taskComplete({ ...pull, merged_at: null }, [approval], checks, []), false);
  assert.equal(taskComplete({ ...pull, head: { sha }, base: {} }, [approval], checks, []), false);
  assert.equal(taskComplete(pull, [{ ...approval, commit_id: "c".repeat(40) }], checks, []), false);
  assert.equal(taskComplete(pull, [{ ...approval, user: { login: "author" } }], checks, []), false);
  assert.equal(taskComplete(pull, [approval], [], []), false);
  assert.equal(taskComplete(pull, [approval], [{ status: "completed", conclusion: "failure" }], []), false);
  assert.equal(taskComplete(pull, [approval], checks, [{ state: "pending" }]), false);
  assert.equal(taskComplete(pull, [approval, { ...approval, state: "DISMISSED" }], checks, []), false);
  assert.equal(taskComplete(pull, [approval, { ...approval, user: { login: "other" }, state: "CHANGES_REQUESTED" }], checks, []), false);
});

test("trusted dispatch rejects widened routing, unknown tasks, stale artifacts and different control revisions", () => {
  const entry = { task: "api", repository: "octodemo/api", phase: "issue", mode: "live",
    destination: "octodemo/api", batch_label: `${digest}:api:issue` };
  const now = Date.parse("2026-01-01T00:01:00Z");
  const snapshot = { schema: "cao-modernization-dispatch/v1", repository: "octodemo/control",
    workflow_sha: sha, created_at: "2026-01-01T00:00:00Z", ready: [entry] };
  const binding = { central_repo: "octodemo/control", workflow_sha: sha,
    target_repo: entry.repository, mode: entry.mode, destination: entry.destination, batch_label: entry.batch_label };
  assert.equal(validateDispatch(snapshot, binding, now), entry);
  for (const altered of [
    { ...binding, mode: "review" }, { ...binding, destination: "octodemo/other" },
    { ...binding, workflow_sha: "c".repeat(40) }, { ...binding, batch_label: `${digest}:web:issue` },
  ]) assert.throws(() => validateDispatch(snapshot, altered, now), /Modernization/);
  assert.throws(() => validateDispatch(snapshot, binding, now + 3_600_000), /expired/);
  assert.throws(() => validateDispatch(snapshot, binding, now - 120_000), /future/);
});

test("safe-output phase guards reject premature PRs, identity substitution and review-mode PRs", () => {
  const entry = { task: "api", phase: "issue", mode: "live", repository: "octodemo/api" };
  const body = `${taskMarker(digest, "api")}\n<!-- cao-modernization-phase:issue -->`;
  validateOutputs([{ type: "create_issue", body }], entry, digest);
  assert.throws(() => validateOutputs([{ type: "create_pull_request", body }], entry, digest), /phase/);
  assert.throws(() => validateOutputs([{ type: "create_issue", body: body.replace(digest, "c".repeat(64)) }], entry, digest), /identity/);
  const implementing = { ...entry, phase: "implement" };
  const implementation = { type: "create_pull_request", draft: true,
    body: `${taskMarker(digest, "api")}\n<!-- cao-modernization-phase:implement -->` };
  validateOutputs([implementation], implementing, digest);
  assert.throws(() => validateOutputs([{ ...implementation, draft: false }], implementing, digest), /draft/);
  assert.throws(() => validateOutputs([implementation], { ...implementing, mode: "review" }, digest), /phase/);
  assert.throws(() => validateOutputs([{ type: "publish_review_bundle", target_repo: "octodemo/other", requested_output: "create-pull-request" }],
    { ...implementing, mode: "review" }, digest), /target/);
});

test("bounded GitHub evidence fails closed on truncated lists", async () => {
  let pages = 0;
  const client = { async request() { pages++; return { data: [], headers: { link: '<next>; rel="next"' } }; } };
  await assert.rejects(boundedPages(client, "/repos/octodemo/api/issues"), /incomplete.*pagination/);
  assert.equal(pages, 10);
});

test("production evidence adapter requires approved baseline ancestry and unambiguous outputs", async () => {
  const value = plan();
  const hash = planDigest(value);
  const marker = taskMarker(hash, "api");
  const requests = [];
  let duplicate = false;
  let comparison = "ahead";
  const client = { async request(route) {
    requests.push(route);
    let data;
    if (route.endsWith("/compare/{basehead}")) data = { status: comparison };
    else if (route.endsWith("/commits/{ref}")) data = { sha };
    else if (route.endsWith("/issues")) data = duplicate ? [1, 2].map(number => ({
      number, user: { type: "Bot" }, state: "open", body: `${marker}\n<!-- cao-modernization-phase:issue -->`,
    })) : [];
    else if (route.endsWith("/pulls")) data = [];
    else data = { default_branch: "main" };
    return { data, headers: {} };
  } };
  const evidence = await collectEvidence(value, hash, candidates, client, client, "octodemo/control");
  assert.equal(evidence.api.issue, null);
  assert.ok(requests.every(route => /^GET \/repos\/octodemo\/(api|web|control)(\/|$)/.test(route)));
  comparison = "diverged";
  await assert.rejects(collectEvidence(value, hash, candidates, client, client, "octodemo/control"), /baseline/);
  comparison = "ahead";
  duplicate = true;
  await assert.rejects(collectEvidence(value, hash, candidates, client, client, "octodemo/control"), /duplicate/);
});

test("compiled workers use the native Squad agent and run trusted guards before publication", () => {
  const worker = readFileSync(new URL("../../.github/workflows/modernization-task.lock.yml", import.meta.url), "utf8");
  assert.match(worker, /--disable-builtin-mcps[\s\S]*--agent squad/);
  assert.ok(worker.indexOf("Restore native Squad agent") < worker.indexOf("Validate approved task and coordinator evidence"));
  assert.match(worker, /await runtime\.guardWorkerOutputs\(\{ github, core, getOctokit \}\)/);
  assert.match(worker, /publish_review_bundle:[\s\S]*?needs:[\s\S]*?- safe_outputs/);
  const jobs = parse(worker).jobs;
  assert.ok(jobs.safe_outputs.needs.includes("modernization_guard"));
  assert.ok(jobs.modernization_guard.needs.includes("agent"));
  assert.ok(jobs.modernization_guard.needs.includes("detection"));
  assert.deepEqual(jobs.modernization_guard.permissions, { actions: "read", contents: "read" });
  for (const [job, credential] of [["agent", "cao_target_read_credential"], ["safe_outputs", "safe-outputs-app-token"]]) {
    const checkout = jobs[job].steps.find(step => step.with?.repository === "${{ inputs.target_repo }}");
    assert.ok(checkout, `${job} must check out the admitted target`);
    assert.ok(checkout.with.token.includes(`github.job == '${job}' && steps.${credential}.outputs.token`));
  }
  const coordinator = readFileSync(new URL("../../.github/workflows/modernization.lock.yml", import.meta.url), "utf8");
  assert.match(coordinator, /await runtime\.guardDispatchOutputs\(github\)/);
  assert.ok(parse(coordinator).jobs.safe_outputs.needs.includes("modernization_guard"));
  assert.ok(coordinator.indexOf("Resolve dependency-ready modernization tasks") < coordinator.indexOf("Freeze modernization dispatch evidence"));
});

test("native team setup materializes the complete runtime including required local skills", () => {
  const workspace = mkdtempSync(join(tmpdir(), "cao-modernization-team-"));
  try {
    const source = new URL("../../modernization/squad/", import.meta.url);
    cpSync(source, join(workspace, ".modernization-team/modernization/squad"), { recursive: true });
    mkdirSync(join(workspace, ".github/agents"), { recursive: true });
    writeFileSync(join(workspace, ".github/agents/project.agent.md"), "Preserve the target's other agents");
    const module = readFileSync(new URL("../../.github/workflows/shared/modernization-squad.md", import.meta.url), "utf8");
    const config = parse(/^---\n([\s\S]*?)\n---/.exec(module)[1]);
    const restore = config.steps.find(step => step.name === "Restore native Squad agent and modernization cast");
    execFileSync("bash", ["-c", restore.run], { env: { PATH: process.env.PATH, GITHUB_WORKSPACE: workspace } });
    const coordinator = readFileSync(join(workspace, ".github/agents/squad.agent.md"), "utf8");
    assert.equal(coordinator, readFileSync(new URL(".github/agents/squad.agent.md", source), "utf8"));
    for (const [, path] of coordinator.matchAll(/`(\.squad\/skills\/[^`]+\/SKILL\.md)`/g)) {
      assert.ok(existsSync(join(workspace, path)), `Native coordinator requires ${path}`);
    }
    assert.ok(existsSync(join(workspace, ".squad/native-gh-aw.md")));
    assert.equal(readFileSync(join(workspace, ".github/agents/project.agent.md"), "utf8"), "Preserve the target's other agents");
  } finally {
    rmSync(workspace, { recursive: true, force: true });
  }
});
