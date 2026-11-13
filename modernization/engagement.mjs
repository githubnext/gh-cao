import { createHash } from "node:crypto";

const SHA = /^[a-f0-9]{40}$/;
const REPOSITORY = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;
const ID = /^[a-z][a-z0-9-]{0,47}$/;

function requireThat(condition, message) {
  if (!condition) throw new Error(`Modernization: ${message}`);
}

function object(value, keys, label) {
  requireThat(value && typeof value === "object" && !Array.isArray(value), `${label} must be an object`);
  requireThat(Object.keys(value).every(key => keys.includes(key)), `${label} has unsupported fields`);
  requireThat(keys.every(key => Object.hasOwn(value, key)), `${label} has missing fields`);
}

function text(value, label, maximum = 2000) {
  requireThat(typeof value === "string" && value.trim().length > 0 && value.length <= maximum,
    `${label} must be nonempty bounded text`);
}

function list(value, label, maximum) {
  requireThat(Array.isArray(value) && value.length > 0 && value.length <= maximum, `${label} must be a bounded nonempty array`);
}

export function validatePlan(plan, allowedRepositories, specialistIds) {
  object(plan, ["schema", "id", "objective", "repositories", "tasks", "integration_acceptance"], "plan");
  requireThat(Buffer.byteLength(JSON.stringify(plan)) <= 524_288, "reviewed plan exceeds handoff size bound");
  requireThat(plan.schema === "cao-modernization/v1" && typeof plan.id === "string" && ID.test(plan.id), "invalid plan identity");
  text(plan.objective, "objective");
  list(plan.repositories, "repositories", 10);
  list(plan.tasks, "tasks", 50);
  list(plan.integration_acceptance, "integration_acceptance", 20);
  plan.integration_acceptance.forEach(value => text(value, "integration acceptance"));
  const allowed = new Set(allowedRepositories.map(value => value.toLowerCase()));
  const repositories = new Set();
  for (const entry of plan.repositories) {
    object(entry, ["repository", "base_sha", "base_branch"], "repository");
    requireThat(REPOSITORY.test(entry.repository) && allowed.has(entry.repository.toLowerCase()), "repository outside admitted scope");
    requireThat(!repositories.has(entry.repository.toLowerCase()), "duplicate repository");
    requireThat(SHA.test(entry.base_sha), "repository requires an exact base SHA");
    text(entry.base_branch, "base branch", 200);
    repositories.add(entry.repository.toLowerCase());
  }
  const tasks = new Map();
  for (const task of plan.tasks) {
    object(task, ["id", "repository", "title", "objective", "acceptance", "depends_on", "specialists"], "task");
    requireThat(typeof task.id === "string" && ID.test(task.id) && !tasks.has(task.id), "invalid or duplicate task identity");
    requireThat(typeof task.repository === "string" && repositories.has(task.repository.toLowerCase()), "task repository is not in the engagement");
    text(task.title, "task title", 160);
    text(task.objective, "task objective");
    list(task.acceptance, "task acceptance", 20);
    task.acceptance.forEach(value => text(value, "task acceptance"));
    list(task.specialists, "specialists", 8);
    requireThat(task.specialists.every(id => typeof id === "string" && specialistIds.includes(id))
      && new Set(task.specialists).size === task.specialists.length, "unknown or duplicate Squad specialist");
    requireThat(Array.isArray(task.depends_on) && task.depends_on.length <= 49
      && new Set(task.depends_on).size === task.depends_on.length, "invalid dependencies");
    tasks.set(task.id, task);
  }
  const visited = new Set();
  const active = new Set();
  function visit(id) {
    requireThat(tasks.has(id), "missing dependency");
    requireThat(!active.has(id), "cyclic dependencies");
    if (visited.has(id)) return;
    active.add(id);
    tasks.get(id).depends_on.forEach(visit);
    active.delete(id);
    visited.add(id);
  }
  tasks.forEach(task => visit(task.id));
  return plan;
}

export function planDigest(plan) {
  function canonical(value) {
    if (Array.isArray(value)) return value.map(canonical);
    if (value && typeof value === "object") {
      return Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])]));
    }
    return value;
  }
  return createHash("sha256").update(JSON.stringify(canonical(plan))).digest("hex");
}

export function taskMarker(digest, taskId) {
  requireThat(/^[a-f0-9]{64}$/.test(digest) && typeof taskId === "string" && ID.test(taskId), "invalid task marker");
  return `<!-- cao-modernization:${digest}:${taskId} -->`;
}

export function taskComplete(pull, reviews, checks, statuses) {
  if (!pull?.merged_at || !SHA.test(pull.head?.sha ?? "") || !REPOSITORY.test(pull.head?.repo?.full_name ?? "")
    || pull.head.repo.full_name !== pull.base?.repo?.full_name) return false;
  const latest = new Map();
  for (const review of reviews) {
    if (["APPROVED", "CHANGES_REQUESTED", "DISMISSED"].includes(review.state)) latest.set(review.user?.login, review);
  }
  const decisions = [...latest.values()];
  if (decisions.some(review => review.state === "CHANGES_REQUESTED")) return false;
  if (!decisions.some(review => review.state === "APPROVED" && review.commit_id === pull.head.sha
    && review.user?.login && review.user.login !== pull.user?.login)) return false;
  return checks.length + statuses.length > 0
    && checks.every(check => check.status === "completed" && check.conclusion === "success")
    && statuses.every(status => status.state === "success");
}

export function readyTasks(plan, evidence, candidates, maximum) {
  const digest = planDigest(plan);
  const admitted = new Map(candidates.map(entry => [entry.full_name.toLowerCase(), entry]));
  const completed = new Set(plan.tasks.filter(task => evidence[task.id]?.complete === true).map(task => task.id));
  const ready = [];
  const selectedRepositories = new Set();
  requireThat(Number.isSafeInteger(maximum) && maximum >= 0 && maximum <= 3, "invalid dispatch bound");
  if (maximum === 0) return { schema: "cao-modernization-ready/v1", digest, ready, completed: [...completed],
    complete: completed.size === plan.tasks.length };
  for (const task of plan.tasks) {
    const candidate = admitted.get(task.repository.toLowerCase());
    const state = evidence[task.id];
    if (!candidate || !state || state.complete || state.active_pull || state.closed_issue
      || selectedRepositories.has(task.repository.toLowerCase())) continue;
    const phase = state.issue ? "implement" : "issue";
    if (phase === "implement" && !task.depends_on.every(id => completed.has(id))) continue;
    ready.push({
      task: task.id, repository: task.repository, phase,
      mode: candidate.safe_output_mode, destination: candidate.safe_output_repo,
      issue: state.issue ?? null, base_sha: state.base_sha,
      batch_label: `${digest}:${task.id}:${phase}`,
    });
    selectedRepositories.add(task.repository.toLowerCase());
    if (ready.length >= maximum) break;
  }
  return { schema: "cao-modernization-ready/v1", digest, ready, completed: [...completed],
    complete: completed.size === plan.tasks.length };
}

export function systemReadScope(precompute, requested, mode, mapping = {}) {
  requireThat(precompute.authorized === true && precompute.control_role === "standalone"
    && precompute.workflow === "modernization-system", "system mission requires standalone admission");
  requireThat(typeof requested === "string", "system requires an explicit repository list");
  const repositories = requested.split(",").map(value => value.trim().toLowerCase());
  requireThat(repositories.length > 0 && repositories.length <= 10
    && repositories.every(value => REPOSITORY.test(value))
    && new Set(repositories).size === repositories.length, "invalid explicit system repository list");
  const admitted = precompute.candidate_repositories.filter(candidate => repositories.includes(candidate.full_name.toLowerCase()));
  requireThat(admitted.length === repositories.length && admitted.length <= precompute.effective_max_repos
    && admitted.every(candidate => candidate.safe_output_mode === "review"), "complete system is not admitted in review mode");
  const owners = new Set(repositories.map(repository => repository.split("/")[0]));
  requireThat(owners.size === 1, "native system assessment currently requires one repository owner");
  requireThat(["app", "pat"].includes(mode), "system assessment requires explicit App or PAT read credentials");
  let secret = "";
  if (mode === "pat") {
    const secrets = new Set(repositories.map(repository => mapping[repository]));
    requireThat(secrets.size === 1 && typeof [...secrets][0] === "string"
      && /^[A-Z_][A-Z0-9_]*$/i.test([...secrets][0]), "system assessment requires one explicitly mapped fleet read PAT");
    secret = [...secrets][0];
  }
  return { owner: [...owners][0], repositories: repositories.map(repository => repository.split("/")[1]).join(","),
    secret, mode, admitted };
}

export function validateDispatch(snapshot, binding, now = Date.now()) {
  requireThat(snapshot.schema === "cao-modernization-dispatch/v1"
    && snapshot.repository === binding.central_repo && snapshot.workflow_sha === binding.workflow_sha,
  "dispatch source does not match the executing control revision");
  requireThat(Number.isFinite(Date.parse(snapshot.created_at))
    && now >= Date.parse(snapshot.created_at) && now - Date.parse(snapshot.created_at) <= 3_600_000, "dispatch expired or future");
  const entries = snapshot.ready.filter(entry => entry.batch_label === binding.batch_label
    && entry.repository.toLowerCase() === binding.target_repo.toLowerCase()
    && entry.mode === binding.mode && entry.destination.toLowerCase() === binding.destination.toLowerCase());
  requireThat(entries.length === 1, "dispatch is not in the trusted ready set");
  return entries[0];
}

export function validateOutputs(items, entry, digest) {
  requireThat(Array.isArray(items) && items.length <= 5, "invalid safe-output batch");
  const marker = taskMarker(digest, entry.task);
  for (const item of items) {
    if (["noop", "report_incomplete", "report_missing_data", "report_missing_tool"].includes(item.type)) continue;
    const allowed = entry.phase === "issue" ? ["create_issue"]
      : entry.mode === "live" ? ["create_pull_request"] : ["create_issue", "publish_review_bundle"];
    requireThat(allowed.includes(item.type), "safe output does not match the admitted task phase");
    if (item.type === "publish_review_bundle") {
      requireThat(item.target_repo === entry.repository && item.requested_output === "create-pull-request",
        "review bundle target does not match the admitted task");
    } else {
      requireThat(typeof item.body === "string" && item.body.includes(marker), "safe output lacks exact task identity");
      requireThat(item.body.includes(`<!-- cao-modernization-phase:${entry.phase} -->`), "safe output lacks phase identity");
      if (item.type === "create_pull_request") requireThat(item.draft !== false, "implementation must remain draft");
    }
  }
}
