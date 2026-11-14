import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { withInventoryCredentials } from "../.github/workflows/shared/inventory-credentials.mjs";
import { planDigest, readyTasks, systemReadScope, taskMarker, validateDispatch, validateOutputs, validatePlan } from "./engagement.mjs";
import { boundedPages, collectEvidence } from "./github.mjs";

const handoff = "/tmp/gh-aw/agent/modernization";
const root = () => process.env.MODERNIZATION_SOURCE;
const read = path => JSON.parse(readFileSync(path, "utf8"));
const planPath = () => join(root(), ".github/cao/modernization.json");
const specialists = () => readdirSync(join(root(), "modernization/squad/.squad/agents"));
const approvedPlan = candidates => validatePlan(read(planPath()), candidates.map(entry => entry.full_name), specialists());

function save(name, value) {
  mkdirSync(handoff, { recursive: true });
  writeFileSync(join(handoff, name), `${JSON.stringify(value, null, 2)}\n`);
}

export async function prepare({ github, core, getOctokit }) {
  const precompute = read("/tmp/gh-aw/agent/control-precompute.json");
  if (precompute.authorized !== true) throw new Error("Modernization: CAO admission is required");
  if (!existsSync(planPath())) {
    core.notice("No reviewed modernization plan is installed; run modernization-system assessment first.");
    save("dispatch.json", {
      schema: "cao-modernization-dispatch/v1", repository: process.env.GITHUB_REPOSITORY,
      workflow_sha: process.env.GITHUB_WORKFLOW_SHA, run_id: process.env.GITHUB_RUN_ID,
      created_at: new Date().toISOString(), ready: [], completed: [], complete: false,
    });
    return;
  }
  const candidates = precompute.candidate_repositories.map(candidate => ({
    ...candidate,
    safe_output_repo: candidate.safe_output_mode === "live" ? candidate.full_name : precompute.safe_output_repo,
  }));
  const plan = approvedPlan(candidates);
  const digest = planDigest(plan);
  const repositories = [...new Set([...plan.repositories.map(entry => entry.repository),
    ...candidates.map(entry => entry.safe_output_repo)])];
  const evidence = await withInventoryCredentials({
    repositories, controlRepository: process.env.GITHUB_REPOSITORY, getOctokit, core,
    readPermissions: ["issues", "pull_requests", "checks", "statuses"],
  }, async ({ inventory, control }) => collectEvidence(plan, digest, candidates, inventory, control, process.env.GITHUB_REPOSITORY));
  const worker = precompute.worker_workflows.find(entry => entry.configured === "modernization-task" && entry.eligible);
  const maximum = Math.min(3, precompute.effective_max_repos, precompute.dispatch_max);
  const result = worker && maximum > 0 ? readyTasks(plan, evidence, candidates, maximum)
    : { digest, ready: [], completed: [], complete: false };
  save("dispatch.json", {
    ...result, schema: "cao-modernization-dispatch/v1", repository: process.env.GITHUB_REPOSITORY,
    workflow_sha: process.env.GITHUB_WORKFLOW_SHA, run_id: process.env.GITHUB_RUN_ID,
    created_at: new Date().toISOString(), plan, evidence,
  });
  core.info(`Modernization: ${result.ready.length} ready task(s); ${result.completed.length}/${plan.tasks.length} complete.`);
}

export async function dispatchSnapshot(github, runId) {
  if (!/^[1-9][0-9]*$/.test(String(runId))) throw new Error("Modernization: invalid origin run");
  const repository = process.env.GITHUB_REPOSITORY;
  const run = (await github.request(`GET /repos/${repository}/actions/runs/${runId}`)).data;
  if (run.path !== ".github/workflows/modernization.lock.yml"
    || run.head_sha !== process.env.GITHUB_WORKFLOW_SHA
    || !["schedule", "workflow_dispatch"].includes(run.event)) {
    throw new Error("Modernization: dispatch did not originate from the trusted coordinator revision");
  }
  const response = await github.request(`GET /repos/${repository}/actions/runs/${runId}/artifacts`, { per_page: 100 });
  if (response.data.total_count !== response.data.artifacts.length) throw new Error("Modernization: incomplete artifact inventory");
  const artifacts = response.data.artifacts.filter(artifact => artifact.name === `modernization-dispatch-${run.run_attempt}` && !artifact.expired);
  if (artifacts.length !== 1 || artifacts[0].size_in_bytes > 1_048_576) throw new Error("Modernization: missing, ambiguous or oversized dispatch evidence");
  const archive = await github.request(`GET /repos/${repository}/actions/artifacts/${artifacts[0].id}/zip`);
  mkdirSync(handoff, { recursive: true });
  const zip = join(handoff, "origin.zip");
  writeFileSync(zip, Buffer.from(archive.data));
  const snapshot = JSON.parse(execFileSync("unzip", ["-p", zip, "dispatch.json"], { encoding: "utf8", maxBuffer: 1_048_576 }));
  if (snapshot.run_id !== String(runId)) throw new Error("Modernization: origin run binding mismatch");
  return snapshot;
}

export async function workerContext(github) {
  const event = read(process.env.GITHUB_EVENT_PATH).inputs;
  const origin = new URL(event.control_plane_run_url);
  const expected = new URL(process.env.GITHUB_SERVER_URL);
  const prefix = `/${process.env.GITHUB_REPOSITORY}/actions/runs/`;
  if (origin.origin !== expected.origin || !origin.pathname.startsWith(prefix) || origin.search || origin.hash
    || event.central_repo !== process.env.GITHUB_REPOSITORY) throw new Error("Modernization: invalid coordinator origin");
  const snapshot = await dispatchSnapshot(github, origin.pathname.slice(prefix.length));
  const entry = validateDispatch(snapshot, {
    central_repo: event.central_repo, workflow_sha: process.env.GITHUB_WORKFLOW_SHA,
    target_repo: event.target_repo, mode: event.safe_output_mode, destination: event.safe_output_repo,
    batch_label: event.batch_label,
  });
  const plan = validatePlan(read(planPath()), snapshot.plan.repositories.map(value => value.repository), specialists());
  if (planDigest(plan) !== snapshot.digest) throw new Error("Modernization: reviewed plan changed");
  const task = plan.tasks.find(task => task.id === entry.task && task.repository === entry.repository);
  if (!task) throw new Error("Modernization: task does not match the reviewed plan");
  const result = { ...entry, digest: snapshot.digest, task, integration_acceptance: plan.integration_acceptance };
  save("task.json", result);
  return result;
}

export async function guardWorkerOutputs({ github, core, getOctokit }) {
  const context = await workerContext(github);
  const output = read(process.env.GH_AW_AGENT_OUTPUT);
  validateOutputs(output.items, { ...context, task: context.task.id }, context.digest);
  if (!output.items.some(item => ["create_issue", "create_pull_request", "publish_review_bundle"].includes(item.type))) return;
  await withInventoryCredentials({
    repositories: [...new Set([context.repository, context.destination])],
    controlRepository: process.env.GITHUB_REPOSITORY, getOctokit, core,
    readPermissions: ["issues", "pull_requests"],
  }, async ({ inventory, control }) => {
    const repository = context.repository;
    const baseline = read(planPath()).repositories.find(entry => entry.repository.toLowerCase() === repository.toLowerCase());
    const head = (await inventory.request(`GET /repos/${repository}/commits/{ref}`, { ref: baseline.base_branch })).data.sha;
    if (head !== context.base_sha) throw new Error("Modernization: target changed; obtain a fresh coordinator dispatch");
    const marker = taskMarker(context.digest, context.task.id);
    const client = context.destination === process.env.GITHUB_REPOSITORY ? control : inventory;
    const issues = await boundedPages(client, `/repos/${context.destination}/issues`, { state: "all", labels: "modernization" });
    const duplicates = issues.filter(issue => !issue.pull_request && issue.user?.type === "Bot"
      && issue.body?.includes(marker) && issue.body.includes(`<!-- cao-modernization-phase:${context.phase} -->`));
    const pulls = await boundedPages(inventory, `/repos/${repository}/pulls`, { state: "all", sort: "updated", direction: "desc" });
    if (duplicates.length || pulls.some(pull => pull.user?.type === "Bot" && pull.body?.includes(marker))) {
      throw new Error("Modernization: task output already exists; refusing a duplicate");
    }
  });
}

export async function guardDispatchOutputs(github) {
  const snapshot = await dispatchSnapshot(github, process.env.GITHUB_RUN_ID);
  const output = read(process.env.GH_AW_AGENT_OUTPUT);
  const seen = new Set();
  const targets = new Set();
  for (const item of output.items) {
    if (["noop", "report_incomplete", "report_missing_data", "report_missing_tool"].includes(item.type)) continue;
    if (item.type !== "dispatch_workflow" || item.workflow_name !== "modernization-task") {
      throw new Error("Modernization: coordinator can only dispatch its declared task worker");
    }
    const entry = validateDispatch(snapshot, {
      central_repo: item.inputs.central_repo, workflow_sha: process.env.GITHUB_WORKFLOW_SHA,
      target_repo: item.inputs.target_repo, mode: item.inputs.safe_output_mode,
      destination: item.inputs.safe_output_repo, batch_label: item.inputs.batch_label,
    });
    const expectedUrl = `${process.env.GITHUB_SERVER_URL}/${process.env.GITHUB_REPOSITORY}/actions/runs/${process.env.GITHUB_RUN_ID}`;
    if (item.inputs.control_plane_run_url !== expectedUrl || seen.has(entry.batch_label)
      || targets.has(entry.repository.toLowerCase())
      || (item.ref && item.ref !== process.env.GITHUB_REF)) throw new Error("Modernization: duplicate or altered dispatch");
    seen.add(entry.batch_label);
    targets.add(entry.repository.toLowerCase());
  }
}

export function systemContext() {
  const precompute = read("/tmp/gh-aw/agent/control-precompute.json");
  const requested = read(process.env.GITHUB_EVENT_PATH).inputs.target_repos;
  const { admitted, ...scope } = systemReadScope(precompute, requested, process.env.CAO_AUTH_MODE || "app",
    JSON.parse(process.env.CAO_READ_PAT_REPOSITORIES || "{}"));
  save("system.json", {
    phase: existsSync(planPath()) ? "verify" : "assess",
    plan: existsSync(planPath()) ? approvedPlan(admitted) : null,
    repositories: admitted,
  });
  return scope;
}
