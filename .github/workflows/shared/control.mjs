#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  mkdirSync,
  readFileSync,
  writeFileSync,
} from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import {
  PolicyError,
  controlSettings,
  effectivePolicy,
  loadPolicyFile,
  parsePolicy,
} from "./policy.mjs";
import { AuthorityError, validateTargetScope } from "./authority.mjs";
import { operationBinding, operationRequest, prepareOperation, resolveOperation, validateOperationHandoff } from "./operations.mjs";
import { credentialFailure, inventoryScope, InventoryError, verifyCredentials, withInventoryCredentials } from "./inventory-credentials.mjs";

const AGENT_DIRECTORY = "/tmp/gh-aw/agent";
const OUTPUT_PATH = join(AGENT_DIRECTORY, "control-precompute.json");
const POLICY_PATH = ".github/workflows/cao.json";
const REPOSITORY_PATTERN = /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/;
const SHA_PATTERN = /^[0-9a-fA-F]{40,64}$/;
const MINIMUM_GITHUB_API_REQUESTS = 100;
const GITHUB_API_CACHE_DURATION = "60s";
const GITHUB_RATE_LIMIT_DOCS = "https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api";
const GITHUB_REST_BEST_PRACTICES = "https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api";
const GITHUB_APP_ACTIONS_DOCS = "https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/making-authenticated-api-requests-with-a-github-app-in-a-github-actions-workflow";
const GITHUB_PAT_DOCS = "https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens";
const GITHUB_ACTIONS_SECRETS_DOCS = "https://docs.github.com/en/actions/security-for-github-actions/security-guides/using-secrets-in-github-actions";
const ACTION_GLOBALS = ["core", "github", "context", "exec", "io", "getOctokit", "inventoryGithub"];
const ADMISSION_CHECKS = [
  ["Runtime revision", "The control and policy modules are read from the exact `github.workflow_sha` commit."],
  ["Policy document", "The checked-in policy is parsed and validated for supported keys, types, ranges, unique names, and expressions."],
  ["Control plane", "The `control-plane` declaration is present."],
  ["Workflow identity", "The operation/standalone workflow or campaign role and exact worker identity are authorized."],
  ["Campaign", "The requested operation, campaign or standalone workflow is declared and enabled."],
  ["Worker", "For worker runs, the requested worker is declared and enabled."],
  ["Target input", "Any supplied `target_repo` uses the exact `owner/repository` form."],
  ["Mode input", "Any supplied `safe_output_mode` does not exceed the checked-in mode ceiling; debug requires a manual, human-authorized worker with staged outputs."],
  ["Run limits", "Any supplied `max_repos` and `rollout_percent` do not exceed checked-in limits."],
  ["GitHub API capacity", "The admission credential has enough primary REST API capacity; precompute also handles subsequent capacity failures."],
];

class ControlError extends Error {}

export function setActionsGlobals(actions = {}) {
  for (const name of ACTION_GLOBALS) {
    if (actions[name] !== undefined) globalThis[name] = actions[name];
  }
}

function environment(name, fallback = "") {
  return process.env[name] ?? fallback;
}

function importIdentity(name) {
  const value = environment(name);
  return value === "__none__" ? "" : value;
}

function parseInteger(value, fallback) {
  const candidate = value === "" ? fallback : value;
  if (typeof candidate !== "string") return candidate;
  const parsed = Number(candidate);
  return Number.isSafeInteger(parsed) ? parsed : candidate;
}

function log(message) {
  console.log(`[CAO] ${message}`);
}

function logDecision(decision, outcome, details = {}) {
  console.log(`[cao] ${JSON.stringify({ decision, outcome, ...details })}`);
}

function actionsCommand(command, message = "") {
  const escaped = message
    .replaceAll("%", "%25")
    .replaceAll("\r", "%0D")
    .replaceAll("\n", "%0A");
  console.log(`::${command}::${escaped}`);
}

function withLogGroup(title, operation) {
  const actions = environment("GITHUB_ACTIONS") === "true";
  if (actions) actionsCommand("group", title);
  else log(title);
  const endGroup = () => {
    if (actions) actionsCommand("endgroup");
  };
  try {
    const result = operation();
    if (result && typeof result.then === "function") return result.finally(endGroup);
    endGroup();
    return result;
  } catch (error) {
    endGroup();
    throw error;
  }
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
    ...options,
  });
  if (result.error) throw result.error;
  if (result.status !== 0) {
    const message = result.stderr.trim() || result.stdout.trim() || `${command} exited with status ${result.status}`;
    throw new ControlError(message);
  }
  return result.stdout;
}

async function githubRequest(endpoint, fields = {}, boundary = "control") {
  const client = boundary === "inventory"
    ? globalThis.inventoryGithub
    : boundary === "workflow"
      ? globalThis.workflowGithub
      : globalThis.github;
  if (boundary === "inventory" && globalThis.github && !client) {
    throw new InventoryError("inventory-credential-unavailable", "The inventory client has not been resolved for the admitted scope.");
  }
  if (boundary === "workflow" && globalThis.github && !client) {
    throw new InventoryError("workflow-credential-unavailable", "The job-scoped workflow credential is unavailable.");
  }
  if (!client?.request) return null;
  const [path, query = ""] = endpoint.split("?", 2);
  const params = { ...fields };
  for (const [key, value] of new URLSearchParams(query).entries()) params[key] = value;
  return (await client.request(`GET /${path}`, params)).data;
}

function filterGithubResponse(data, jq) {
  if (!jq) return JSON.stringify(data);
  if (jq === ".content") return data.content ?? "";
  if (jq === ".workflows[] | {id, name, path, state}") {
    return (Array.isArray(data.workflows) ? data.workflows : [])
      .map(({ id, name, path, state }) => JSON.stringify({ id, name, path, state }))
      .join("\n");
  }
  if (jq === "{id, full_name, archived, disabled, private, pushed_at, default_branch}") {
    const { id, full_name, archived, disabled, private: isPrivate, pushed_at, default_branch } = data;
    return JSON.stringify({ id, full_name, archived, disabled, private: isPrivate, pushed_at, default_branch });
  }
  if (jq === ".[] | {id, full_name, archived, disabled, private, pushed_at, default_branch}") {
    return (Array.isArray(data) ? data : [])
      .map(({ id, full_name, archived, disabled, private: isPrivate, pushed_at, default_branch }) => (
        JSON.stringify({ id, full_name, archived, disabled, private: isPrivate, pushed_at, default_branch })
      ))
      .join("\n");
  }
  throw new ControlError(`unsupported GitHub API response filter: ${jq}`);
}

async function ghApi(endpoint, { fields = {}, jq = "", boundary = "control" } = {}) {
  const data = await githubRequest(endpoint, fields, boundary);
  if (data !== null) return filterGithubResponse(data, jq);
  const args = ["api", "--cache", GITHUB_API_CACHE_DURATION];
  if (Object.keys(fields).length > 0) args.push("--method", "GET");
  args.push(endpoint);
  for (const [key, value] of Object.entries(fields)) args.push("-f", `${key}=${value}`);
  if (jq) args.push("--jq", jq);
  return run("gh", args);
}

async function decodeRepositoryFile(repository, path, sha) {
  const encoded = await ghApi(`repos/${repository}/contents/${path}`, {
    fields: { ref: sha },
    jq: ".content",
  });
  return Buffer.from(encoded.replace(/\s/g, ""), "base64").toString("utf8");
}

function parseJsonOutput(source) {
  const trimmed = source.trim();
  if (!trimmed) return [];
  try {
    return JSON.parse(trimmed);
  } catch {
    return trimmed.split("\n").filter(Boolean).map((line) => JSON.parse(line));
  }
}

function writeActionsOutputs(values) {
  const outputPath = environment("GITHUB_OUTPUT");
  if (!outputPath) return;
  const lines = Object.entries(values).map(([key, value]) => `${key}=${String(value).replace(/[\r\n]/g, " ")}`);
  writeFileSync(outputPath, `${lines.join("\n")}\n`, { flag: "a" });
}

function retryWait(resetEpochSeconds) {
  const seconds = Math.max(0, resetEpochSeconds - Math.floor(Date.now() / 1000));
  return {
    seconds,
    minutes: Math.ceil(seconds / 60),
    hours: Math.ceil(seconds / 36) / 100,
  };
}

function capacityGuidance(capacity) {
  if (!capacity || capacity.status === "available") return "";
  if (capacity.status === "unavailable") {
    return `
> [!CAUTION]
> GitHub REST API capacity could not be verified. Activation stopped before repository discovery.

### What to do now

1. Check the credential and the [REST API rate-limit guidance](${GITHUB_RATE_LIMIT_DOCS}); do not repeatedly retry a 403 or 429 response.
2. For durable cross-repository automation, configure a least-privilege GitHub App using [GitHub's Actions authentication guide](${GITHUB_APP_ACTIONS_DOCS}).
3. If an App cannot be installed and the exact scope is eligible, use separate fine-grained read and write PATs with minimal repository access, permissions, and expiration. Follow [GitHub's PAT guidance](${GITHUB_PAT_DOCS}) and store them as [Actions secrets](${GITHUB_ACTIONS_SECRETS_DOCS}) named \`GH_AW_GITHUB_READ_PAT\` and \`GH_AW_GITHUB_WRITE_PAT\`.

See also [GitHub REST API best practices](${GITHUB_REST_BEST_PRACTICES}).
`;
  }
  const wait = retryWait(capacity.reset);
  return `
> [!CAUTION]
> GitHub REST API capacity is too low for this run: ${capacity.remaining} of ${capacity.limit} core requests remain; at least ${capacity.required} are required.

### What to do now

1. Do not rerun before **${capacity.resetAt}**. That is approximately **${wait.minutes} minutes (${wait.hours.toFixed(2)} hours)** from this admission check. The next scheduled run after that time is a new attempt.
2. For durable cross-repository automation, configure a least-privilege GitHub App using [GitHub's Actions authentication guide](${GITHUB_APP_ACTIONS_DOCS}). GitHub documents higher, installation-scoped limits for Apps in the [REST API rate-limit guide](${GITHUB_RATE_LIMIT_DOCS}).
3. If an App cannot be installed and the exact scope is eligible, use separate fine-grained read and write PATs with minimal repository access, permissions, and expiration. Follow [GitHub's PAT guidance](${GITHUB_PAT_DOCS}) and store them as [Actions secrets](${GITHUB_ACTIONS_SECRETS_DOCS}) named \`GH_AW_GITHUB_READ_PAT\` and \`GH_AW_GITHUB_WRITE_PAT\`.

GitHub says not to retry primary-limit failures until \`x-ratelimit-reset\`; continuing while limited can result in integration blocking. See [GitHub REST API best practices](${GITHUB_REST_BEST_PRACTICES}).
`;
}

function failedAdmissionCheckIndex(reason) {
  if (!reason) return -1;
  if (reason.startsWith("cannot read")) return 0; // Runtime revision
  if (reason === "control policy validation failed") return 1; // Policy document
  if (reason === "control-plane-absent") return 2; // Control plane
  if (
    reason === "role must be orchestrator, worker, or standalone"
    || reason === "worker identity is required"
    || reason === "worker identity is forbidden for orchestrators"
  ) return 3; // Workflow identity
  if (reason.startsWith("standalone ") || reason.startsWith("workflow identity")) return 3;
  if (reason === "campaign-undeclared" || reason === "campaign-disabled") return 4; // Campaign
  if (reason === "workflow-undeclared" || reason === "workflow-disabled") return 4;
  if (reason === "operation-undeclared" || reason === "operation-disabled") return 4;
  if (reason === "worker-disabled" || reason.startsWith("unknown worker:")) return 5; // Worker
  if (reason.startsWith("target_repo ")) return 6; // Target input
  if (reason === "safe_output_mode exceeds checked-in policy" || reason === "safe_output_mode must be review or live") return 7; // Mode input
  if (reason.startsWith("debug ")) return 7; // Debug admission
  if (reason.startsWith("max_repositories") || reason.startsWith("rollout_percent")) return 8; // Run limits
  if (reason === "github-api-capacity-insufficient" || reason === "github-api-capacity-unavailable") return 9; // GitHub API capacity
  return -1;
}

function admissionCheckHeading(title, index, authorized, failedIndex) {
  if (authorized) return `✅ ${title}`;
  if (failedIndex < 0) return title;
  if (index < failedIndex) return `✅ ${title}`;
  if (index === failedIndex) return `❌ ${title}`;
  return title;
}

function writeAdmissionSummary({ authorized, campaignName, role, reason, apiCapacity }) {
  const summaryPath = environment("GITHUB_STEP_SUMMARY");
  if (!summaryPath) return;
  const identity = role === "standalone" ? `workflow \`${environment("CAO_WORKFLOW")}\`` : `campaign \`${campaignName}\``;
  const status = apiCapacity?.status === "limited"
    ? `Blocked ${identity} as \`${role}\` before activation: insufficient GitHub REST API capacity.`
    : apiCapacity?.status === "unavailable"
      ? `Blocked ${identity} as \`${role}\` before activation: GitHub REST API capacity is unavailable.`
      : authorized
    ? `Authorized ${identity} as \`${role}\`.`
    : `Skipped ${identity} as \`${role}\`: ${reason}`;
  const failedIndex = authorized ? -1 : failedAdmissionCheckIndex(reason);
  const checks = ADMISSION_CHECKS.map(([title, description], index) => (
    `- ${admissionCheckHeading(title, index, authorized, failedIndex)} — ${description}`
  )).join("\n");
  writeFileSync(
    summaryPath,
    `<details>\n<summary><h3>Central Agentic Ops admission</h3></summary>\n\n${status}\n${capacityGuidance(apiCapacity)}\n${checks}\n\n</details>\n`,
    { flag: "a" },
  );
}

function githubApiRequestRequirement(policy, options) {
  let estimated = options.role === "orchestrator" ? 2 : 0;
  if (options.role === "direct") {
    estimated += 1 + 2 * policy.authority_envelope.grants.length;
  } else if (options.role === "orchestrator" || options.role === "standalone") {
    estimated += options.targetRepository
      ? 1
      : policy.allowed_repositories.length > 0
        ? policy.allowed_repositories.length
        : Math.ceil(policy.inventory["max-scan-repositories"] / 100);
  } else if (policy.safe_output_mode === "live") {
    estimated += 3;
  }
  return Math.max(MINIMUM_GITHUB_API_REQUESTS, estimated);
}

async function githubApiCapacity(required) {
  const token = environment("CAO_API_TOKEN");
  try {
    const data = await githubRequest("rate_limit")
      ?? JSON.parse(run("gh", ["api", "rate_limit"], {
        env: token ? { ...process.env, GH_TOKEN: token } : process.env,
      }));
    const core = data?.resources?.core;
    if (![core?.limit, core?.remaining, core?.reset].every(Number.isSafeInteger)) {
      throw new ControlError("GitHub rate-limit response did not contain integer core limits");
    }
    const resetAt = new Date(core.reset * 1000).toISOString();
    return {
      status: core.remaining >= required ? "available" : "limited",
      limit: core.limit,
      remaining: core.remaining,
      required,
      reset: core.reset,
      resetAt,
    };
  } catch {
    return { status: "unavailable", limit: 0, remaining: 0, required, reset: 0, resetAt: "unknown" };
  }
}

function isRateLimitError(error) {
  return typeof error?.message === "string" && /rate limit/i.test(error.message);
}

function writeCapacityBlockedPrecompute(campaignName, role, capacity) {
  const reason = capacity.status === "limited" ? "github-api-capacity-insufficient" : "github-api-capacity-unavailable";
  writeActionsOutputs({
    authorized: false,
    reason,
    github_api_status: capacity.status,
    github_api_limit: capacity.limit,
    github_api_remaining: capacity.remaining,
    github_api_required: capacity.required,
    github_api_reset_at: capacity.resetAt,
  });
  writeAdmissionSummary({ authorized: false, campaignName, role, reason, apiCapacity: capacity });
}

async function applyGithubApiAdmission(result, options) {
  if (!result.authorized) {
    logDecision("github-api-capacity", "skipped", { reason: "policy-denied" });
    return result;
  }
  const required = githubApiRequestRequirement(result, options);
  const capacity = await githubApiCapacity(required);
  logDecision("github-api-capacity", capacity.status, {
    required,
    remaining: capacity.remaining,
    limit: capacity.limit,
  });
  if (capacity.status === "available") return { ...result, github_api_capacity: capacity };
  return {
    ...result,
    authorized: false,
    reason: capacity.status === "limited" ? "github-api-capacity-insufficient" : "github-api-capacity-unavailable",
    github_api_capacity: capacity,
  };
}

function policyOptions({ normalizeOrchestrator = false } = {}) {
  const role = environment("CAO_ROLE");
  return {
    campaignName: importIdentity("CAO_CAMPAIGN"),
    workflowName: importIdentity("CAO_WORKFLOW"),
    role,
    workerName: (normalizeOrchestrator && role === "orchestrator")
      || (["standalone", "direct"].includes(role) && environment("CAO_WORKER") === "__none__") ? "" : environment("CAO_WORKER"),
    controlRepository: environment("GITHUB_REPOSITORY"),
    requestedMode: environment("CAO_REQUESTED_MODE"),
    requestedMaxRepositories: environment("CAO_REQUESTED_MAX_REPOSITORIES"),
    requestedRolloutPercent: environment("CAO_REQUESTED_ROLLOUT_PERCENT"),
    targetRepository: environment("CAO_TARGET_REPOSITORY") || environment("CAO_DEFAULT_TARGET_REPOSITORY"),
    eventName: environment("GITHUB_EVENT_NAME"),
  };
}

async function validateDebugLaunch(policy, options) {
  if (options.requestedMode !== "debug") return;
  if (environment("GITHUB_ACTIONS") !== "true" || options.role !== "worker"
    || options.eventName !== "workflow_dispatch") {
    throw new ControlError("debug requires a manual workflow_dispatch worker run");
  }
  if (environment("CAO_CORRELATION_ID") || environment("CAO_CENTRAL_REPOSITORY")
    || environment("CAO_CONTROL_PLANE_RUN_URL")) {
    throw new ControlError("debug must not carry a dispatcher envelope");
  }
  const destination = environment("CAO_REQUESTED_SAFE_OUTPUT_REPOSITORY");
  if (!destination || !repositoryEqual(destination, options.targetRepository)) {
    throw new ControlError("debug safe_output_repo must equal target_repo");
  }
  validateTargetRepository(options.targetRepository, policy, "debug target_repo is not allowed");
  const actor = environment("GITHUB_ACTOR");
  let payload;
  try {
    payload = globalThis.context?.payload ?? readJson(environment("GITHUB_EVENT_PATH"));
  } catch {
    throw new ControlError("debug requires an authoritative dispatch event");
  }
  if (payload?.inputs?.safe_output_mode !== "debug"
    || payload.inputs.target_repo !== options.targetRepository
    || payload.inputs.safe_output_repo !== destination) {
    throw new ControlError("debug inputs must match the authoritative dispatch event");
  }
  if (!/^[A-Za-z0-9][A-Za-z0-9-]*$/.test(actor)
    || payload.sender?.type !== "User" || payload.sender?.login !== actor
    || environment("GITHUB_TRIGGERING_ACTOR") !== actor) {
    throw new ControlError("debug requires the original human dispatch actor");
  }
  let permission;
  try {
    const endpoint = `repos/${options.controlRepository}/collaborators/${actor}/permission`;
    permission = await githubRequest(endpoint) ?? JSON.parse(run("gh", ["api", endpoint]));
  } catch {
    throw new ControlError("debug actor write permission could not be verified");
  }
  if (permission?.user?.type !== "User" || permission.user?.login !== actor
    || !["write", "maintain", "admin"].includes(permission.permission)) {
    throw new ControlError("debug actor must have write access to the control repository");
  }
  logDecision("debug-launch", "accepted", { actor, safe_outputs_staged: true });
}

function admissionDirectory() {
  return join(environment("RUNNER_TEMP", "/tmp"), "cao");
}

function writeAdmissionRecord(result, options, workflowSha) {
  const directory = admissionDirectory();
  mkdirSync(directory, { recursive: true });
  const failedIndex = result.authorized ? -1 : failedAdmissionCheckIndex(result.reason);
  const checks = ADMISSION_CHECKS.map(([check], index) => ({
    check,
    status: result.authorized || index < failedIndex
      ? "passed"
      : index === failedIndex ? "failed" : "not-evaluated",
  }));
  writeJson(join(directory, "admission.json"), {
    schema_version: 1,
    observed_at: new Date().toISOString(),
    repository: options.controlRepository,
    workflow: environment("GITHUB_WORKFLOW"),
    workflow_sha: workflowSha,
    run_id: environment("GITHUB_RUN_ID"),
    run_attempt: parseInteger(environment("GITHUB_RUN_ATTEMPT"), 1),
    campaign: options.campaignName,
    ...(["standalone", "direct"].includes(options.role) ? { workflow_id: options.workflowName } : {}),
    role: options.role,
    worker: options.workerName,
    target_repository: options.targetRepository,
    authorized: Boolean(result.authorized),
    reason: result.reason || "unknown",
    failed_check: failedIndex >= 0 ? ADMISSION_CHECKS[failedIndex][0] : null,
    checks,
    ...(options.requestedMode === "debug" ? {
      requested_mode: "debug",
      safe_outputs_staged: true,
      debug_actor: environment("GITHUB_ACTOR"),
    } : {}),
    ...(result.github_api_capacity ? { github_api_capacity: result.github_api_capacity } : {}),
  });
}

async function admit() {
  const options = policyOptions({ normalizeOrchestrator: true });
  const workflowSha = environment("GITHUB_WORKFLOW_SHA");
  let result = { authorized: false, reason: "control policy admission did not complete" };

  try {
    if ((!options.campaignName && !options.workflowName) || !options.role) {
      throw new ControlError("admission requires a workflow or campaign identity and control role");
    }
    if (options.role === "standalone") validateStandaloneIdentity(options.workflowName);
    if (options.role === "direct" && (options.campaignName || options.workerName)) {
      throw new ControlError("direct operations must not specify campaign or worker identities");
    }
    if (!SHA_PATTERN.test(workflowSha)) throw new ControlError("github.workflow_sha must be an exact commit SHA");
    logDecision("admission-inputs", "accepted", {
      campaign: options.campaignName,
      ...(options.role === "standalone" ? { workflow: options.workflowName } : {}),
      role: options.role,
      worker: options.workerName || "none",
      target_requested: Boolean(options.targetRepository),
      mode_narrowing_requested: Boolean(options.requestedMode),
      repository_limit_requested: Boolean(options.requestedMaxRepositories),
      rollout_narrowing_requested: Boolean(options.requestedRolloutPercent),
    });

    const directory = admissionDirectory();
    mkdirSync(directory, { recursive: true });
    let source;
    try {
      source = await decodeRepositoryFile(options.controlRepository, POLICY_PATH, workflowSha);
      logDecision("policy-source", "loaded", { source: "github.workflow_sha" });
    } catch {
      throw new ControlError(`cannot read ${POLICY_PATH} at github.workflow_sha`);
    }
    let document;
    try {
      document = parsePolicy(source);
      logDecision("policy-document", "validated");
    } catch (error) {
      throw new ControlError(error instanceof PolicyError ? "control policy validation failed" : error.message);
    }
    const effective = options.role === "direct"
      ? resolveOperation(document, operationRequest(process.env), operationBinding(process.env))
      : effectivePolicy(document, options);
    if (effective.authorized) await validateDebugLaunch(effective, options);
    logDecision("effective-policy", effective.authorized ? "authorized" : "denied", {
      reason: effective.reason,
    });
    result = await applyGithubApiAdmission(effective, options);
    writeFileSync(join(directory, "effective-policy.json"), `${JSON.stringify(result, null, 2)}\n`);
  } catch (error) {
    result = { authorized: false, reason: error.message };
    logDecision("admission-evaluation", "failed", { reason: result.reason });
  }

  writeAdmissionRecord(result, options, workflowSha);
  const outputs = {
    authorized: result.authorized,
    reason: result.reason,
    monthly_credit_budget: 0,
  };
  if (result.github_api_capacity?.status !== "available" && result.github_api_capacity) {
    Object.assign(outputs, {
      github_api_status: result.github_api_capacity.status,
      github_api_limit: result.github_api_capacity.limit,
      github_api_remaining: result.github_api_capacity.remaining,
      github_api_required: result.github_api_capacity.required,
      github_api_reset_at: result.github_api_capacity.resetAt,
    });
  }
  writeActionsOutputs(outputs);
  writeAdmissionSummary({
    authorized: result.authorized,
    campaignName: options.campaignName,
    role: options.role,
    reason: result.reason,
    apiCapacity: result.github_api_capacity,
  });
  log(`Admission ${result.authorized ? "authorized" : "denied"}.`);
}

function readJson(path) {
  return JSON.parse(readFileSync(path, "utf8"));
}

function writeJson(path, value) {
  writeFileSync(path, `${JSON.stringify(value, null, 2)}\n`);
}

function requirePositiveInteger(value, maximum, message) {
  if (!Number.isSafeInteger(value) || value < 1 || value > maximum) throw new ControlError(message);
}

function requireNonNegativeInteger(value, message) {
  if (!Number.isSafeInteger(value) || value < 0) throw new ControlError(message);
}

function requireMode(value, label, { allowDebug = false } = {}) {
  if (allowDebug && value === "debug") return;
  if (!["review", "live"].includes(value)) throw new ControlError(`${label} must be review or live`);
}

function repositoryEqual(left, right) {
  return left.toLowerCase() === right.toLowerCase();
}

function validateRepositoryOwner(label, repository, allowedOwners) {
  if (!repository) return;
  if (!REPOSITORY_PATTERN.test(repository)) throw new ControlError(`${label} must use owner/repository form`);
  if (allowedOwners.length === 0) return;
  const owner = repository.split("/", 1)[0].toLowerCase();
  if (!allowedOwners.some((allowed) => owner === allowed.trim().toLowerCase())) {
    throw new ControlError(`${label} owner is outside control-plane.scope.allowed-owners`);
  }
}

function validateTargetRepository(repository, policy, denialReason = "target_repo is not allowed") {
  if (repository) validateTargetScope(repository, {
    owners: policy.allowed_owners, repositories: policy.allowed_repositories,
  }, denialReason);
}

function parseFrontmatterWorkers(source) {
  const lines = source.split("\n");
  let inFrontmatter = false;
  let inSafeOutputs = false;
  let inDispatch = false;
  let inWorkflows = false;
  const workers = [];

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    if (index === 0 && /^---\s*$/.test(line)) {
      inFrontmatter = true;
      continue;
    }
    if (inFrontmatter && /^---\s*$/.test(line)) break;
    if (!inFrontmatter) continue;
    if (/^safe-outputs:/.test(line)) {
      inSafeOutputs = true;
      continue;
    }
    if (inSafeOutputs && /^\S/.test(line)) inSafeOutputs = false;
    if (inSafeOutputs && /^  dispatch-workflow:/.test(line)) {
      inDispatch = true;
      continue;
    }
    if (inDispatch && /^  \S/.test(line)) {
      inDispatch = false;
      inWorkflows = false;
    }
    const inline = inDispatch && line.match(/^    workflows:\s*\[(.*)]\s*$/);
    if (inline) {
      workers.push(...inline[1].split(",").map((value) => value.trim().replace(/^['"]|['"]$/g, "")).filter(Boolean));
      continue;
    }
    if (inDispatch && /^    workflows:\s*$/.test(line)) {
      inWorkflows = true;
      continue;
    }
    const item = inWorkflows && line.match(/^\s{4,6}-\s+(.+)$/);
    if (item) {
      workers.push(item[1].trim().replace(/^['"]|['"]$/g, ""));
      continue;
    }
    if (inWorkflows) inWorkflows = false;
  }
  return [...new Set(workers)];
}

function controlSourcePath() {
  const repository = environment("GITHUB_REPOSITORY");
  const workflowReference = environment("GITHUB_WORKFLOW_REF");
  const withoutRepository = workflowReference.startsWith(`${repository}/`)
    ? workflowReference.slice(repository.length + 1)
    : workflowReference;
  const separator = withoutRepository.lastIndexOf("@");
  const workflowPath = separator >= 0 ? withoutRepository.slice(0, separator) : withoutRepository;
  return workflowPath.endsWith(".lock.yml")
    ? workflowPath.replace(/\.lock\.yml$/, ".md")
    : workflowPath.replace(/\.yml$/, ".md");
}

function validateStandaloneIdentity(workflow) {
  const reference = `${environment("GITHUB_REPOSITORY")}/.github/workflows/${workflow}.lock.yml@`;
  if (!environment("GITHUB_WORKFLOW_REF").startsWith(reference)
    || environment("GITHUB_WORKFLOW_REF").length === reference.length) {
    throw new ControlError("standalone workflow identity must match GITHUB_WORKFLOW_REF");
  }
}

async function loadWorkflowInventory(repository) {
  const workflows = [];
  for (let page = 1; page <= 101; page += 1) {
    const parsed = parseJsonOutput(await ghApi(`repos/${repository}/actions/workflows?per_page=100&page=${page}`, {
      jq: ".workflows[] | {id, name, path, state}",
      boundary: "workflow",
    }));
    const batch = Array.isArray(parsed) ? parsed : parsed.workflows ?? [parsed];
    if (page > 100) {
      if (batch.length > 0) throw new ControlError("workflow inventory exceeds 10000 workflows");
      return workflows;
    }
    workflows.push(...batch.map(({ id, name, path, state }) => ({ id, name, path, state })));
    if (batch.length < 100) return workflows;
  }
  return workflows;
}

async function loadRepository(endpoint) {
  const repository = parseJsonOutput(await ghApi(endpoint, {
    jq: "{id, full_name, archived, disabled, private, pushed_at, default_branch}",
    boundary: "inventory",
  }));
  const { id, full_name, archived, disabled, private: isPrivate, pushed_at, default_branch } = repository;
  if (!Number.isSafeInteger(id) || id <= 0 || !REPOSITORY_PATTERN.test(full_name)
    || !repositoryEqual(endpoint.slice("repos/".length), full_name)
    || typeof archived !== "boolean" || typeof disabled !== "boolean" || typeof isPrivate !== "boolean") {
    throw new InventoryError("inventory-response-invalid", "Repository metadata is malformed or does not match the enrolled repository identity.");
  }
  return { id, full_name, archived, disabled, private: isPrivate, pushed_at, default_branch };
}

async function loadBoundedInventory(organization, maximum) {
  const endpoints = [
    [`orgs/${organization}/repos`, "all"],
    [`users/${organization}/repos`, "owner"],
  ];
  let lastError = "";
  for (const [endpoint, type] of endpoints) {
    try {
      const repositories = [];
      for (let page = 1; page <= Math.ceil(maximum / 100); page += 1) {
        const batch = parseJsonOutput(await ghApi(`${endpoint}?per_page=100&type=${type}&page=${page}`, {
          jq: ".[] | {id, full_name, archived, disabled, private, pushed_at, default_branch}",
        }));
        repositories.push(...batch.map(({ id, full_name, archived, disabled, private: isPrivate, pushed_at, default_branch }) => ({
          id, full_name, archived, disabled, private: isPrivate, pushed_at, default_branch,
        })));
        if (batch.length < 100) break;
      }
      return { repositories: repositories.slice(0, maximum), error: "" };
    } catch (error) {
      lastError = error.message;
    }
  }
  return { repositories: [], error: lastError };
}

function inventoryDigest(repositories) {
  const sorted = [...repositories].sort((left, right) => left.id - right.id || left.full_name.localeCompare(right.full_name));
  const input = sorted.map((repository) => JSON.stringify(repository)).join("\n") + (sorted.length ? "\n" : "");
  return `sha256:${createHash("sha256").update(input).digest("hex")}`;
}

async function validateOutputDestination({ mode, role, safeOutputRepository, targetRepository, controlRepository }) {
  if (mode === "debug") {
    if (role !== "worker" || !repositoryEqual(safeOutputRepository, targetRepository)) {
      throw new ControlError("debug safe_output_repo must equal target_repo");
    }
    logDecision("safe-output-destination", "accepted", { mode, validation: "staged-target-preview" });
    return;
  }
  if (mode === "live") {
    const targetBound = role === "worker" || role === "standalone";
    if (targetBound && !repositoryEqual(safeOutputRepository, targetRepository)) {
      throw new ControlError(`live ${role} safe_output_repo must equal target_repo`);
    }
    logDecision("safe-output-destination", "accepted", {
      mode,
      validation: targetBound ? "target-bound" : "live-orchestrator",
    });
    return;
  }
  if (repositoryEqual(safeOutputRepository, targetRepository) && !repositoryEqual(safeOutputRepository, controlRepository)) {
    throw new ControlError("review safe_output_repo must differ from target_repo");
  }
  if (repositoryEqual(safeOutputRepository, controlRepository)) {
    logDecision("safe-output-destination", "accepted", { mode, validation: "central-review-shortcut" });
    return;
  }
  let repository;
  try {
    repository = JSON.parse(await ghApi(`repos/${safeOutputRepository}`));
  } catch (error) {
    if (isRateLimitError(error)) throw error;
    throw new ControlError("review safe_output_repo must be accessible");
  }
  if (repository.private !== true) throw new ControlError("non-central review safe_output_repo must be private");
  logDecision("safe-output-destination", "accepted", { mode, validation: "private-review-repository" });
}

function validateWorkerDispatch(context) {
  if (!context.targetRepository) throw new ControlError("worker target_repo is required");
  validateTargetRepository(context.targetRepository, context.policy, "worker target_repo is not allowed");
  if (context.workerPolicy.enabled !== true) throw new ControlError("worker is disabled by its control-plane policy");
  requireMode(context.workerPolicy.maxMode, "worker_max_mode");
  if (context.mode === "live" && context.workerPolicy.maxMode !== "live") {
    throw new ControlError("safe_output_mode exceeds the worker_max_mode ceiling");
  }
  if (context.mode === "debug") {
    logDecision("worker-dispatch-envelope", "not-required", { mode: "debug", safe_outputs_staged: true });
    return;
  }
  if (context.centralRepository !== context.controlRepository) {
    throw new ControlError("central_repo must identify the current control repository");
  }
  if (!/^[1-9][0-9]*-[1-9][0-9]*$/.test(context.correlationId)) {
    throw new ControlError("correlation_id must identify an orchestrator run and attempt");
  }
  const runId = context.correlationId.split("-", 1)[0];
  const expected = `${environment("GITHUB_SERVER_URL")}/${context.controlRepository}/actions/runs/${runId}`;
  if (context.controlPlaneRunUrl !== expected) {
    throw new ControlError("control_plane_run_url must match correlation_id and central_repo");
  }
  logDecision("worker-dispatch-envelope", "accepted", {
    mode: context.mode,
    worker_max_mode: context.workerPolicy.maxMode,
  });
}

function createContext(policy) {
  const campaignName = importIdentity("CAO_CAMPAIGN");
  const workflowName = importIdentity("CAO_WORKFLOW");
  const role = environment("CAO_ROLE");
  const worker = role === "orchestrator" || (role === "standalone" && environment("CAO_WORKER") === "__none__")
    ? "" : environment("CAO_WORKER");
  if ((!campaignName && !workflowName) || !role) throw new ControlError("precompute requires workflow or campaign and role inputs");
  const workerPolicy = Object.values(policy.worker_policies ?? {}).find((entry) => entry.worker === worker);
  const debug = policy.safe_output_mode === "debug";
  return {
    policy,
    campaignName,
    workflowName,
    role,
    worker,
    targetRepository: environment("CAO_TARGET_REPOSITORY") || environment("CAO_DEFAULT_TARGET_REPOSITORY"),
    dispatchMaximum: parseInteger(environment("CAO_DISPATCH_MAX"), 1),
    safeOutputRepository: environment("CAO_SAFE_OUTPUT_REPOSITORY"),
    correlationId: environment("CAO_CORRELATION_ID"),
    centralRepository: role === "orchestrator" || debug ? environment("GITHUB_REPOSITORY") : environment("CAO_CENTRAL_REPOSITORY"),
    controlPlaneRunUrl: environment("CAO_CONTROL_PLANE_RUN_URL"),
    orchestratorCredits: parseInteger(environment("CAO_ORCHESTRATOR_CREDITS"), 0),
    workerCreditsPerTarget: parseInteger(environment("CAO_WORKER_CREDITS_PER_TARGET"), 0),
    workflowSha: environment("GITHUB_WORKFLOW_SHA"),
    controlRepository: environment("GITHUB_REPOSITORY"),
    mode: policy.safe_output_mode,
    workerPolicy: {
      enabled: workerPolicy?.enabled ?? true,
      maxMode: workerPolicy?.max_mode ?? (debug ? "review" : policy.safe_output_mode),
    },
  };
}

function writeDeniedPrecompute(policy) {
  const role = environment("CAO_ROLE");
  const worker = role === "orchestrator" || role === "standalone" ? "" : environment("CAO_WORKER");
  writeJson(OUTPUT_PATH, {
    authorized: false,
    reason: policy.reason ?? "control policy denied this run",
    control_role: role,
    campaign: importIdentity("CAO_CAMPAIGN"),
    ...(role === "standalone" ? { workflow: importIdentity("CAO_WORKFLOW") } : {}),
    worker,
    enabled: false,
    effective_max_repos: 0,
    repo_error: "",
    candidate_repositories: [],
    worker_workflows: [],
    policy_source: {
      repository: environment("GITHUB_REPOSITORY"),
      path: POLICY_PATH,
      sha: environment("GITHUB_WORKFLOW_SHA"),
    },
  });
  const safeOutputsPath = environment("GH_AW_SAFE_OUTPUTS");
  if (safeOutputsPath) {
    writeFileSync(safeOutputsPath, `${JSON.stringify({
      type: "noop",
      message: `Central Agentic Ops policy denied this run: ${policy.reason ?? "control policy denied this run"}`,
    })}\n`, { flag: "a" });
  }
}

function writeWorkerPrecompute(context) {
  writeJson(OUTPUT_PATH, {
    authorized: true,
    reason: "authorized",
    control_role: "worker",
    campaign: context.campaignName,
    bundle: context.campaignName,
    worker: context.worker,
    enabled: context.policy.enabled,
    worker_enabled: context.workerPolicy.enabled,
    worker_max_mode: context.workerPolicy.maxMode,
    target_repo: context.targetRepository,
    safe_output_mode: context.mode,
    safe_output_repo: context.safeOutputRepository,
    ...(context.mode === "debug" ? {
      launch_kind: "manual-debug",
      debug_actor: environment("GITHUB_ACTOR"),
      safe_outputs_staged: true,
    } : {}),
    correlation_id: context.correlationId,
    central_repo: context.centralRepository,
    control_plane_run_url: context.controlPlaneRunUrl,
    candidate_repositories: [],
    worker_workflows: [],
    policy_source: { repository: context.controlRepository, path: POLICY_PATH, sha: context.workflowSha },
  });
}

async function selectInventory(context, maximum) {
  if (context.targetRepository) {
    try {
      const result = { repositories: [await loadRepository(`repos/${context.targetRepository}`)], source: "target_repo", error: "" };
      logDecision("repository-inventory", "selected", { source: result.source, repository_count: 1 });
      return result;
    } catch (error) {
      if (globalThis.github || error instanceof InventoryError) throw credentialFailure(error);
      logDecision("repository-inventory", "unavailable", { source: "target_repo", repository_count: 0 });
      return { repositories: [], source: "target_repo", error: error.message };
    }
  }
  const allowedRepositories = context.inventoryRepositories ?? context.policy.allowed_repositories;
  if (context.inventoryRepositories?.length === 0) return { repositories: [], source: "allowed_repos", error: "" };
  if (allowedRepositories.length > 0) {
    const repositories = [];
    for (const repository of allowedRepositories) {
      try {
        repositories.push(await loadRepository(`repos/${repository}`));
      } catch (error) {
        if (globalThis.github || error instanceof InventoryError) throw credentialFailure(error);
        logDecision("repository-inventory", "unavailable", { source: "allowed_repos", repository_count: 0 });
        return { repositories: [], source: "allowed_repos", error: `cannot read allowed repository ${repository}` };
      }
    }
    logDecision("repository-inventory", "selected", { source: "allowed_repos", repository_count: repositories.length });
    return { repositories, source: "allowed_repos", error: "" };
  }
  const organization = context.controlRepository.split("/", 1)[0];
  const { repositories, error } = await loadBoundedInventory(organization, maximum);
  logDecision("repository-inventory", error ? "unavailable" : "selected", {
    source: "organization",
    repository_count: repositories.length,
  });
  return { repositories, source: "organization", error };
}

function validateInventoryLimits(context) {
  const inventory = context.policy.inventory;
  const maximumRepositories = context.policy.max_repositories;
  const maximumScanRepositories = inventory["max-scan-repositories"];
  const cellCount = inventory["cell-count"];
  const cellIndex = inventory["cell-index"];
  const batchSize = inventory["batch-size"];
  const batchIndex = inventory["batch-index"];

  requirePositiveInteger(maximumRepositories, 1000, "max_repos must be an integer from 1 through 1000");
  requirePositiveInteger(maximumScanRepositories, 100_000, "max_scan_repos must be an integer from 1 through 100000");
  requirePositiveInteger(cellCount, 1000, "cell_count must be an integer from 1 through 1000");
  if (!Number.isSafeInteger(cellIndex) || cellIndex < 0 || cellIndex >= cellCount) {
    throw new ControlError("cell_index must be an integer from 0 through cell_count minus 1");
  }
  requirePositiveInteger(batchSize, 100_000, "batch_size must be an integer from 1 through 100000");
  requireNonNegativeInteger(batchIndex, "batch_index must be a non-negative integer");
  if (context.role === "orchestrator") {
    requirePositiveInteger(context.dispatchMaximum, 1000, "dispatch_max must be an integer from 1 through 1000");
  }
  requirePositiveInteger(context.policy.rollout_percent, 100, "rollout_percent must be an integer from 1 through 100");
}

function createInventory(context, repositories) {
  const inventory = context.policy.inventory;
  const cellCount = inventory["cell-count"];
  const cellIndex = inventory["cell-index"];
  const batchSize = inventory["batch-size"];
  const batchIndex = inventory["batch-index"];
  const uniqueRepositories = new Map();
  for (const repository of repositories) {
    const identity = repository.id ?? repository.full_name.toLowerCase();
    uniqueRepositories.set(identity, repository);
  }
  const sorted = [...uniqueRepositories.values()]
    .sort((left, right) => left.id - right.id || left.full_name.localeCompare(right.full_name));
  const version = inventoryDigest(sorted);
  const cell = context.targetRepository ? sorted : sorted.filter(({ id }) => id % cellCount === cellIndex);
  const batchCount = Math.ceil(cell.length / batchSize);
  if (batchCount > 0 && batchIndex >= batchCount) {
    throw new ControlError(`batch_index must be smaller than the selected cell batch count (${batchCount})`);
  }
  const candidates = cell.slice(batchIndex * batchSize, (batchIndex + 1) * batchSize);
  logDecision("inventory-partition", "selected", {
    input_count: repositories.length,
    unique_count: sorted.length,
    configured_cell_count: cellCount,
    cell_repository_count: cell.length,
    configured_batch_size: batchSize,
    batch_count: batchCount,
    batch_index: batchIndex,
    candidate_count: candidates.length,
    target_shortcut: Boolean(context.targetRepository),
  });
  return {
    candidates,
    metadata: {
      inventory_version: version,
      inventory_repository_count: sorted.length,
      cell_count: cellCount,
      cell_index: cellIndex,
      cell_repository_count: cell.length,
      batch_size: batchSize,
      batch_index: batchIndex,
      batch_count: batchCount,
      batch_id: `${version}:cell-${cellIndex}-of-${cellCount}:batch-${batchIndex}-of-${batchCount}`,
    },
  };
}

async function resolveTargetInventory(context) {
  const maximumScanRepositories = context.policy.inventory["max-scan-repositories"];

  if ((context.inventoryRepositories ?? context.policy.allowed_repositories).length > maximumScanRepositories) {
    throw new ControlError("allowed repos exceed max_scan_repos");
  }
  validateTargetRepository(context.targetRepository, context.policy);

  const selected = await selectInventory(context, maximumScanRepositories);
  if (selected.error) throw new InventoryError("inventory-unavailable",
    "Required repository inventory could not be read; verify credentials, exact enrollment, API capacity and GitHub availability.");
  const { candidates, metadata } = createInventory(context, selected.repositories);
  const resolvedCandidates = candidates.map((repository) => ({
    ...repository,
    safe_output_mode: context.policy.target_policies?.[repository.full_name.toLowerCase()]?.mode ?? context.mode,
  }));
  return { maximumScanRepositories, selected, metadata, resolvedCandidates };
}

async function writeStandalonePrecompute(context) {
  validateStandaloneIdentity(context.workflowName);
  if (context.policy.control_role !== "standalone" || context.policy.workflow !== context.workflowName
    || context.campaignName || context.worker) {
    throw new ControlError("standalone precompute identity must match admitted policy");
  }
  if (context.correlationId || context.centralRepository || context.controlPlaneRunUrl) {
    throw new ControlError("standalone workflows must not carry a dispatcher envelope");
  }
  if (!SHA_PATTERN.test(context.workflowSha)) throw new ControlError("github.workflow_sha must be an exact commit SHA");
  const { selected, metadata, resolvedCandidates } = await resolveTargetInventory(context);
  if (selected.error) throw new ControlError(`standalone inventory unavailable: ${selected.error}`);
  const candidates = resolvedCandidates.filter(({ archived, disabled }) => !archived && !disabled);
  const percentCap = candidates.length === 0
    ? 0 : Math.max(1, Math.ceil(candidates.length * context.policy.rollout_percent / 100));
  const maximum = Math.min(context.policy.max_repositories, percentCap);
  const admitted = candidates.slice(0, maximum).map((repository) => ({
    ...repository,
    safe_output_repo: repository.safe_output_mode === "live" ? repository.full_name : context.safeOutputRepository,
  }));
  for (const repository of admitted) {
    validateTargetRepository(repository.full_name, context.policy, "standalone target_repo is not allowed");
    await validateOutputDestination({
      ...context,
      mode: repository.safe_output_mode,
      safeOutputRepository: repository.safe_output_repo,
      targetRepository: repository.full_name,
    });
  }
  writeJson(OUTPUT_PATH, {
    authorized: true,
    reason: "authorized",
    control_role: "standalone",
    workflow: context.workflowName,
    campaign: "",
    bundle: "",
    worker: "",
    enabled: true,
    target_repo: context.targetRepository,
    safe_output_mode: context.mode,
    safe_output_repo: context.safeOutputRepository,
    max_repos: context.policy.max_repositories,
    rollout_percent: context.policy.rollout_percent,
    effective_max_repos: maximum,
    repo_source: selected.source,
    repo_error: "",
    ...metadata,
    total_repositories_scanned: metadata.inventory_repository_count,
    candidate_repositories: admitted,
    worker_workflows: [],
    policy_source: { repository: context.controlRepository, path: POLICY_PATH, sha: context.workflowSha },
  });
}

async function writeOrchestratorPrecompute(context) {
  if (!SHA_PATTERN.test(context.workflowSha)) throw new ControlError("github.workflow_sha must be an exact commit SHA");
  const sourcePath = controlSourcePath();
  const source = await decodeRepositoryFile(context.controlRepository, sourcePath, context.workflowSha);
  const configuredWorkers = parseFrontmatterWorkers(source);
  if (configuredWorkers.length === 0) {
    throw new ControlError("shared/control.md role orchestrator requires safe-outputs.dispatch-workflow.workflows");
  }
  const workflows = await loadWorkflowInventory(context.controlRepository);
  const { maximumScanRepositories, selected, metadata, resolvedCandidates } = await resolveTargetInventory(context);
  const workerWorkflows = configuredWorkers.map((configured) => {
    const match = workflows.find(({ path }) => path === `.github/workflows/${configured}.lock.yml`);
    const policy = context.policy.worker_policies?.[configured];
    const enabled = policy?.enabled ?? false;
    const active = match && !String(match.state ?? "").startsWith("disabled");
    const resolved = {
      configured,
      matched: Boolean(match),
      worker: policy?.worker ?? null,
      policy_enabled: enabled,
      max_mode: policy?.max_mode ?? null,
      id: match?.id ?? null,
      name: match?.name ?? null,
      path: match?.path ?? null,
      state: match?.state ?? "",
      eligible: Boolean(enabled && match && active),
      skip_reason: !policy
        ? "worker is not part of installed campaign"
        : !enabled
          ? "worker disabled by control-plane policy"
          : !match
            ? "worker workflow unavailable"
            : !active
              ? "worker workflow disabled"
              : null,
    };
    logDecision("worker-workflow", resolved.eligible ? "eligible" : "skipped", {
      workflow: configured,
      reason: resolved.skip_reason ?? "available",
      max_mode: resolved.max_mode ?? "none",
    });
    return resolved;
  });
  const eligibleWorkers = workerWorkflows.filter(({ eligible }) => eligible).length;
  const percentCap = resolvedCandidates.length === 0
    ? 0
    : Math.max(1, Math.ceil(resolvedCandidates.length * context.policy.rollout_percent / 100));
  const workersPerTarget = Math.min(eligibleWorkers, context.dispatchMaximum);
  const dispatchCap = workersPerTarget === 0 ? 0 : Math.floor(context.dispatchMaximum / workersPerTarget);
  const effectiveMaximum = Math.min(context.policy.max_repositories, percentCap, dispatchCap);
  logDecision("repository-cap", "resolved", {
    candidates: resolvedCandidates.length,
    eligible_workers: eligibleWorkers,
    workers_per_target: workersPerTarget,
    policy_cap: context.policy.max_repositories,
    rollout_cap: percentCap,
    dispatch_cap: dispatchCap,
    effective_cap: effectiveMaximum,
  });

  const result = {
    authorized: true,
    reason: "authorized",
    control_role: "orchestrator",
    campaign: context.campaignName,
    bundle: context.campaignName,
    worker: context.worker,
    enabled: context.policy.enabled,
    target_repo: context.targetRepository,
    organization: context.controlRepository.split("/", 1)[0],
    max_repos: context.policy.max_repositories,
    max_scan_repos: maximumScanRepositories,
    dispatch_max: context.dispatchMaximum,
    rollout_percent: context.policy.rollout_percent,
    effective_max_repos: effectiveMaximum,
    orchestrator_credits: context.orchestratorCredits,
    worker_credits_per_target: context.workerCreditsPerTarget,
    monthly_credit_budget: 0,
    monthly_ai_credits_spent: 0,
    monthly_ai_credits_remaining: 0,
    monthly_budget_error: "",
    monthly_budget_target_cap: effectiveMaximum,
    safe_output_mode: context.mode,
    safe_output_repo: context.safeOutputRepository,
    repo_source: selected.source,
    repo_error: selected.error,
    policy_source: { repository: context.controlRepository, path: POLICY_PATH, sha: context.workflowSha },
    ...metadata,
    total_repositories_scanned: metadata.inventory_repository_count,
    candidate_repositories: resolvedCandidates,
    worker_workflows: workerWorkflows,
  };
  writeJson(OUTPUT_PATH, result);
}

async function writeOperationPrecompute(admitted) {
  const binding = operationBinding(process.env);
  const request = operationRequest(process.env);
  const document = parsePolicy(await decodeRepositoryFile(binding.repository, POLICY_PATH, binding.policySha));
  const resolved = resolveOperation(document, request, binding);
  validateOperationHandoff(admitted, resolved, binding);
  const envelope = resolved.authority_envelope;
  if (!envelope) throw new ControlError("operation handoff has no authority envelope");
  if (envelope.grants[0].mode === "live" && !environment("CAO_TARGET_REPOSITORY")) {
    throw new ControlError("live direct operations require one target_repo");
  }
  const candidates = await prepareOperation(envelope, {
    loadRepository: repository => loadRepository(`repos/${repository}`),
    validateDestination: grant => validateOutputDestination({
      mode: grant.mode, role: "standalone", targetRepository: grant.target,
      safeOutputRepository: grant.destination, controlRepository: binding.repository,
    }),
  });
  writeJson(OUTPUT_PATH, {
    authorized: true, reason: "authorized", control_role: "direct",
    campaign: "", bundle: "", workflow: request.operation, worker: "", enabled: true,
    safe_output_mode: envelope.grants[0].mode,
    safe_output_repo: envelope.grants[0].destination,
    effective_max_repos: envelope.maxTargets,
    authority_envelope: envelope,
    candidate_repositories: candidates,
    worker_workflows: [],
    repo_error: "",
    policy_source: { repository: binding.repository, path: POLICY_PATH, sha: binding.policySha },
  });
  writeActionsOutputs({ authorized: true, reason: "authorized" });
  logDecision("precompute", "prepared", { role: "direct", candidate_count: candidates.length });
}

function precomputeFailure(error) {
  return error instanceof ControlError || error instanceof PolicyError || error instanceof AuthorityError
    ? new InventoryError("precompute-contract-invalid", error.message, { phase: "control" })
    : credentialFailure(error);
}

function writeIncompletePrecompute(policy, error) {
  const failure = precomputeFailure(error);
  writeJson(OUTPUT_PATH, {
    authorized: policy.authorized,
    activation_ready: false,
    reason: failure.code,
    control_role: environment("CAO_ROLE"),
    campaign: importIdentity("CAO_CAMPAIGN"),
    bundle: importIdentity("CAO_CAMPAIGN"),
    worker: environment("CAO_ROLE") === "worker" ? environment("CAO_WORKER") : "",
    workflow: importIdentity("CAO_WORKFLOW"),
    inventory_status: "incomplete",
    repo_error: failure.message,
    diagnostics: [
      { code: failure.code, phase: failure.phase, status: failure.status, message: failure.message },
      ...(failure.cleanupFailure ? [{ code: "credential-cleanup-failed", phase: "inventory", status: null, message: failure.cleanupFailure }] : []),
    ],
    effective_max_repos: 0,
    candidate_repositories: [],
    worker_workflows: [],
    policy_source: { repository: environment("GITHUB_REPOSITORY"), path: POLICY_PATH, sha: environment("GITHUB_WORKFLOW_SHA") },
  });
  writeActionsOutputs({ authorized: false, policy_authorized: policy.authorized, activation_ready: false, reason: failure.code });
  actionsCommand("error", `CAO precompute incomplete (${failure.code}): ${failure.message}`);
  if (failure.cleanupFailure) actionsCommand("error", failure.cleanupFailure);
  const summary = environment("GITHUB_STEP_SUMMARY");
  if (summary) writeFileSync(summary,
    `\n### CAO inventory incomplete\n\nPolicy admission: **authorized**. Activation and dispatch: **blocked**.\n\n${[failure.message, failure.cleanupFailure].filter(Boolean).join(" ")}\n\nDiagnostic: \`${failure.code}\` (${failure.phase}${failure.status ? `, HTTP ${failure.status}` : ""}). No partial fleet execution.\n`,
    { flag: "a" });
  process.exitCode = 1;
}

async function precompute() {
  mkdirSync(AGENT_DIRECTORY, { recursive: true });
  const effectivePath = join(admissionDirectory(), "effective-policy.json");
  let policy;
  try {
    policy = readJson(effectivePath);
  } catch {
    throw new ControlError(`missing admission effective policy: ${effectivePath}`);
  }
  if (typeof policy.authorized !== "boolean") {
    throw new ControlError("control policy resolver returned an invalid admission result");
  }
  if (!policy.authorized) {
    writeDeniedPrecompute(policy);
    logDecision("precompute", "skipped", { reason: policy.reason ?? "policy-denied" });
    return;
  }
  logDecision("precompute-policy", "accepted", {
    campaign: policy.campaign,
    role: environment("CAO_ROLE"),
  });
  if (globalThis.github) {
    const original = globalThis.github;
    try {
      const options = policyOptions({ normalizeOrchestrator: true });
      const document = parsePolicy(await decodeRepositoryFile(options.controlRepository, POLICY_PATH, environment("GITHUB_WORKFLOW_SHA")));
      const operation = options.role === "direct"
        ? resolveOperation(document, operationRequest(process.env), operationBinding(process.env)) : null;
      const current = operation ?? effectivePolicy(document, options);
      if (!current.authorized) throw new InventoryError("policy-handoff-denied", "Policy no longer admits this exact workflow request.");
      if (operation) validateOperationHandoff(policy, operation, operationBinding(process.env));
      policy = current;
      const repositories = options.role === "worker" ? []
        : operation ? operation.authority_envelope.grants.map(grant => grant.target)
          : inventoryScope(current, {
            controlRepository: options.controlRepository,
            target: options.targetRepository,
            excludeControl: environment("CAO_INVENTORY_EXCLUDE_CONTROL") === "true",
          });
      if (operation && environment("CAO_INVENTORY_EXCLUDE_CONTROL") === "true"
        && repositories.some(repository => repositoryEqual(repository, options.controlRepository))) {
        throw new InventoryError("target-excluded", "The importing workflow excludes the control repository from inventory.");
      }
      await withInventoryCredentials({
        repositories, controlRepository: options.controlRepository,
        getOctokit: globalThis.getOctokit, core: globalThis.core,
      }, async ({ control, inventory, workflow, profile }) => {
        globalThis.github = control;
        globalThis.inventoryGithub = inventory;
        globalThis.workflowGithub = workflow;
        logDecision("inventory-credential", "resolved", { profile, repository_count: repositories.length });
        try {
          await preparePrecompute(policy, repositories);
        } catch (error) {
          throw precomputeFailure(error);
        }
      });
    } catch (error) {
      writeIncompletePrecompute(policy, error);
    } finally {
      globalThis.github = original;
      delete globalThis.inventoryGithub;
      delete globalThis.workflowGithub;
    }
    return;
  }
  try {
    await preparePrecompute(policy);
  } catch (error) {
    if (!(error instanceof InventoryError)) throw error;
    writeIncompletePrecompute(policy, error);
    process.stderr.write(`[CAO failure] ${error.message}\n`);
  }
}

async function preparePrecompute(policy, repositories = undefined) {
  if (environment("CAO_ROLE") === "direct") {
    await writeOperationPrecompute(policy);
    markInventoryComplete();
    return;
  }
  const context = createContext(policy);
  context.inventoryRepositories = repositories;
  try {
    requireMode(context.mode, "safe_output_mode", { allowDebug: context.role === "worker" });
    if (context.mode === "debug") {
      await validateDebugLaunch(policy, { ...policyOptions(), requestedMode: "debug" });
    }

    validateRepositoryOwner("target_repo", context.targetRepository, policy.allowed_owners);
    validateRepositoryOwner("safe_output_repo", context.safeOutputRepository, policy.allowed_owners);
    if (context.role === "orchestrator" || context.role === "standalone") validateInventoryLimits(context);
    if (context.role === "standalone") {
      await writeStandalonePrecompute(context);
      markInventoryComplete();
      writeActionsOutputs({ authorized: true, reason: "authorized" });
      logDecision("precompute", "prepared", { role: "standalone" });
      return;
    }
    await validateOutputDestination(context);

    if (context.role === "worker") {
      validateWorkerDispatch(context);
      writeWorkerPrecompute(context);
      writeActionsOutputs({ authorized: true, reason: "authorized" });
      logDecision("precompute", "prepared", { role: "worker", candidate_count: 0 });
      return;
    }
    await writeOrchestratorPrecompute(context);
    markInventoryComplete();
    writeActionsOutputs({ authorized: true, reason: "authorized" });
    const result = readJson(OUTPUT_PATH);
    logDecision("precompute", "prepared", {
      role: "orchestrator",
      candidate_count: result.candidate_repositories.length,
      eligible_worker_count: result.worker_workflows.filter(({ eligible }) => eligible).length,
      effective_cap: result.effective_max_repos,
    });
  } catch (error) {
    if (globalThis.github || error instanceof InventoryError) throw error;
    if (!isRateLimitError(error)) throw error;
    const required = githubApiRequestRequirement(policy, { role: context.role, targetRepository: context.targetRepository });
    const capacity = await githubApiCapacity(required);
    logDecision("precompute-rate-limit-recovery", capacity.status, {
      required,
      remaining: capacity.remaining,
      limit: capacity.limit,
    });
    writeCapacityBlockedPrecompute(
      context.campaignName,
      context.role,
      capacity.status === "unavailable" ? { ...capacity, status: "limited" } : capacity,
    );
  }
}

function markInventoryComplete() {
  const result = readJson(OUTPUT_PATH);
  writeJson(OUTPUT_PATH, { ...result, activation_ready: true, inventory_status: "complete" });
}

async function verifyRuntimeCredentials() {
  const workflowSha = environment("GITHUB_WORKFLOW_SHA");
  if (!SHA_PATTERN.test(workflowSha)) throw new ControlError("Credential verification requires an exact workflow SHA.");
  const controlRepository = environment("GITHUB_REPOSITORY");
  let report;
  try {
    const document = parsePolicy(await decodeRepositoryFile(controlRepository, POLICY_PATH, workflowSha));
    report = await verifyCredentials({
      settings: controlSettings(document, controlRepository),
      controlRepository, workflowSha, getOctokit: globalThis.getOctokit, core: globalThis.core,
    });
  } catch (error) {
    const failure = credentialFailure(error, "control-policy");
    report = {
      schema_version: 1, workflow_sha: workflowSha, ok: false, issue_publication: "unverified",
      read_checks: [{ check: "control-policy", status: "incomplete", code: failure.code, message: failure.message }],
      output_checks: [],
    };
  }
  mkdirSync(admissionDirectory(), { recursive: true });
  writeJson(join(admissionDirectory(), "credential-verification.json"), report);
  const summary = environment("GITHUB_STEP_SUMMARY");
  if (summary) writeFileSync(summary,
    `\n### CAO runtime credential verification\n\n${report.ok ? "Read reach and requested output prerequisites verified." : "Credential verification is incomplete; inspect the sanitized artifact."} No issue publication, dispatch or activation was attempted. Successful read checks do not prove issue publication.\n`,
    { flag: "a" });
  for (const check of [...report.read_checks, ...report.output_checks]) logDecision("credential-verification", check.status, check);
  if (!report.ok) process.exitCode = 1;
}

function readSource(path) {
  return path === "-" ? readFileSync(0, "utf8") : readFileSync(path, "utf8");
}

async function policyCommand(command, args) {
  if (args.length !== 1) throw new ControlError(`usage: control.mjs ${command} <file|->`);
  const document = args[0] === "-"
    ? parsePolicy(readSource(args[0]))
    : await loadPolicyFile(args[0]);
  if (command === "validate-policy") return document;
  if (command === "resolve-policy") return effectivePolicy(document, policyOptions());
  if (command === "control-settings") return controlSettings(document, environment("GITHUB_REPOSITORY"));
  throw new ControlError(`unknown command: ${command}`);
}

function authority(args) {
  if (args.length !== 2) throw new ControlError("usage: control.mjs authority <file|-> <campaign>");
  const document = parsePolicy(readSource(args[0]));
  const value = document["target-authority"]?.campaigns?.[args[1]]?.authority;
  if (!value) throw new PolicyError(`target authority does not declare campaign ${args[1]}`);
  process.stdout.write(`${value}\n`);
}

function compilerVersion(args) {
  if (args.length !== 1) throw new ControlError("usage: control.mjs compiler-version <file|->");
  const document = parsePolicy(readSource(args[0]));
  const value = document["gh-aw-version"];
  if (!value) throw new PolicyError("gh-aw-version is required for a control plane");
  process.stdout.write(`${value}\n`);
}

/**
 * @param {Record<string, unknown>|string[]} actionsOrArguments
 * @param {string[]=} maybeArguments
 */
export async function main(actionsOrArguments = {}, maybeArguments = undefined) {
  const actions = Array.isArray(actionsOrArguments) ? {} : actionsOrArguments;
  const arguments_ = Array.isArray(actionsOrArguments) ? actionsOrArguments : maybeArguments ?? [];
  setActionsGlobals(actions);
  const [command, ...args] = arguments_;
  try {
    if (command === "admit" && args.length === 0) {
      return await withLogGroup("Central Agentic Ops admission", admit);
    }
    if (command === "precompute" && args.length === 0) {
      return await withLogGroup("Central Agentic Ops precompute", precompute);
    }
    if (command === "verify-credentials" && args.length === 0) {
      return await withLogGroup("Central Agentic Ops runtime credential verification", verifyRuntimeCredentials);
    }
    if (command === "authority") return authority(args);
    if (command === "compiler-version") return compilerVersion(args);
    if (["validate-policy", "resolve-policy", "control-settings"].includes(command)) {
      process.stdout.write(`${JSON.stringify(await policyCommand(command, args), null, 2)}\n`);
      return;
    }
    throw new ControlError("usage: control.mjs admit | precompute | verify-credentials | validate-policy <file|-> | resolve-policy <file|-> | control-settings <file|-> | compiler-version <file|-> | authority <file|-> <campaign>");
  } catch (error) {
    if (error instanceof ControlError || error instanceof PolicyError || error instanceof InventoryError || error?.code === "ENOENT") {
      process.stderr.write(`[CAO failure] ${error.message}\n`);
      process.exitCode = 1;
      return;
    }
    throw error;
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  await main(process.argv.slice(2));
}