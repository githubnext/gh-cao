import {
  AuthorityError, narrowLimit, resolveEnvelope, validateEnvelope, validateNarrowing, validateTargetScope,
} from "./authority.mjs";

const REPOSITORY = /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/;
const MAX_TARGET_INPUT_LENGTH = 64 * 1024;
export const MULTI_TARGET_ENVELOPE_VERSION = "1";

export function operationBinding(environment) {
  return {
    repository: environment.GITHUB_REPOSITORY ?? "",
    policySha: environment.GITHUB_WORKFLOW_SHA ?? "",
    workflowRef: environment.GITHUB_WORKFLOW_REF ?? "",
    runId: environment.GITHUB_RUN_ID ?? "",
    runAttempt: environment.GITHUB_RUN_ATTEMPT ?? "",
  };
}

export function operationRequest(environment) {
  const target = environment.CAO_TARGET_REPOSITORY || "";
  const targetsInput = environment.CAO_TARGET_REPOSITORIES || "";
  if (target && targetsInput) throw new AuthorityError("specify target_repo or target_repos, not both");
  if (targetsInput.length > MAX_TARGET_INPUT_LENGTH) throw new AuthorityError("target_repos exceeds 64 KiB");
  let targets = target ? [target] : [];
  if (targetsInput) {
    try {
      targets = JSON.parse(targetsInput);
    } catch {
      throw new AuthorityError("target_repos must be a JSON array of repository names");
    }
  }
  if (environment.CAO_CORRELATION_ID || environment.CAO_CENTRAL_REPOSITORY || environment.CAO_CONTROL_PLANE_RUN_URL) {
    throw new AuthorityError("direct operations must not carry a dispatcher envelope");
  }
  if (environment.CAO_REQUESTED_ROLLOUT_PERCENT) throw new AuthorityError("direct operations do not use rollout_percent");
  return {
    version: 1,
    operation: environment.CAO_WORKFLOW,
    targets,
    mode: environment.CAO_REQUESTED_MODE || "review",
    destination: environment.CAO_REQUESTED_SAFE_OUTPUT_REPOSITORY || "",
    maxTargets: environment.CAO_REQUESTED_MAX_REPOSITORIES || "",
  };
}

export function resolveOperation(document, request, binding) {
  const workflow = request.operation;
  if (typeof workflow !== "string" || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(workflow)) {
    throw new AuthorityError("operation requires an exact workflow identity");
  }
  const prefix = `${binding.repository}/.github/workflows/${workflow}.lock.yml@`;
  if (!binding.workflowRef.startsWith(prefix) || binding.workflowRef.length === prefix.length) {
    throw new AuthorityError("operation identity must match GITHUB_WORKFLOW_REF");
  }
  const control = document["control-plane"];
  const declaration = Object.hasOwn(control?.operations ?? {}, workflow) ? control.operations[workflow] : null;
  const deny = reason => ({ authorized: false, reason, control_role: "direct", campaign: "", workflow, worker: "", enabled: false });
  if (!control) return deny("control-plane-absent");
  if (!declaration) return deny("operation-undeclared");
  const decision = resolveEnvelope({
    operation: workflow,
    enabled: declaration.enabled ?? true,
    mode: declaration.mode ?? control.defaults?.mode ?? "review",
    maxMode: declaration["max-mode"],
    maxTargets: declaration["max-repositories"] ?? control.defaults?.["max-repositories"] ?? 1,
    targets: declaration.targets,
    scope: {
      owners: control.scope?.["allowed-owners"] ?? [binding.repository.split("/")[0]],
      repositories: control.scope?.["allowed-repositories"] ?? [],
    },
  }, request, binding);
  if (decision.decision !== "allow") return deny(decision.reason);
  const envelope = decision.envelope;
  if (!envelope) throw new AuthorityError("operation decision has no authority envelope");
  const first = envelope.grants[0];
  if (first.mode !== request.mode) throw new AuthorityError("requested mode is not permitted for every operation target");
  if (envelope.grants.some(grant => grant.mode !== first.mode || grant.destination.toLowerCase() !== first.destination.toLowerCase())) {
    throw new AuthorityError("gh-aw direct operations require a common mode and output destination");
  }
  if (first.mode === "live" && (envelope.grants.length !== 1 || request.targets.length !== 1)) {
    throw new AuthorityError("live direct operations require one target_repo");
  }
  return {
    authorized: true,
    reason: decision.reason,
    control_role: "direct",
    campaign: "",
    workflow,
    worker: "",
    enabled: true,
    safe_output_mode: first.mode,
    max_repositories: envelope.maxTargets,
    allowed_owners: control.scope?.["allowed-owners"] ?? [binding.repository.split("/")[0]],
    allowed_repositories: control.scope?.["allowed-repositories"] ?? [],
    authority_envelope: envelope,
  };
}

export function validateOperationHandoff(admitted, resolved, binding) {
  if (resolved.authorized !== true || admitted.authorized !== true) throw new AuthorityError("operation handoff is not authorized");
  validateEnvelope(admitted.authority_envelope, binding);
  validateNarrowing(resolved.authority_envelope, admitted.authority_envelope);
  if (JSON.stringify(admitted.authority_envelope) !== JSON.stringify(resolved.authority_envelope)) {
    throw new AuthorityError("operation handoff does not match current request and exact-revision policy");
  }
}

export async function prepareOperation(envelope, { loadRepository, validateDestination }) {
  validateEnvelope(envelope, envelope.binding);
  const candidates = [];
  const resolvedNames = new Set();
  for (const grant of envelope.grants) {
    const repository = await loadRepository(grant.target);
    if (typeof repository.full_name !== "string" || repository.full_name.toLowerCase() !== grant.target.toLowerCase()
      || !Number.isSafeInteger(repository.id) || repository.id < 1
      || repository.archived !== false || repository.disabled !== false) {
      throw new AuthorityError("operation target is redirected, archived, disabled or lacks required evidence");
    }
    if (resolvedNames.has(repository.id)) throw new AuthorityError("operation targets resolve to duplicate repositories");
    resolvedNames.add(repository.id);
    await validateDestination(grant);
    candidates.push({ ...repository, safe_output_mode: grant.mode, safe_output_repo: grant.destination });
  }
  return candidates;
}

// Opt-in campaign worker adapter: one dispatched run reviews an explicit, bounded target set.
export function multiTargetInput(environment) {
  return {
    version: environment.CAO_ENVELOPE_VERSION ?? "",
    target: environment.CAO_TARGET_REPOSITORY ?? "",
    targets: environment.CAO_TARGET_REPOSITORIES ?? "",
    mode: environment.CAO_REQUESTED_MODE ?? "",
    destination: environment.CAO_REQUESTED_SAFE_OUTPUT_REPOSITORY ?? "",
    maxTargets: environment.CAO_REQUESTED_MAX_REPOSITORIES ?? "",
    rolloutPercent: environment.CAO_REQUESTED_ROLLOUT_PERCENT ?? "",
    correlationId: environment.CAO_CORRELATION_ID ?? "",
    centralRepository: environment.CAO_CENTRAL_REPOSITORY ?? "",
    controlPlaneRunUrl: environment.CAO_CONTROL_PLANE_RUN_URL ?? "",
    serverUrl: environment.GITHUB_SERVER_URL ?? "",
  };
}

function parseTargetSet(input) {
  if (input.version !== MULTI_TARGET_ENVELOPE_VERSION) throw new AuthorityError("target_repos requires envelope_version 1");
  if (input.target) throw new AuthorityError("target_repos must not be combined with target_repo");
  if (typeof input.targets !== "string" || !input.targets) {
    throw new AuthorityError("target_repos is required for multi-target workers");
  }
  if (input.targets.length > MAX_TARGET_INPUT_LENGTH) throw new AuthorityError("target_repos exceeds 64 KiB");
  let targets;
  try {
    targets = JSON.parse(input.targets);
  } catch {
    targets = null;
  }
  if (!Array.isArray(targets) || !targets.length) {
    throw new AuthorityError("target_repos must be a non-empty JSON array of repository names");
  }
  const names = new Set();
  targets.forEach((target, index) => {
    if (typeof target !== "string" || !REPOSITORY.test(target)) {
      throw new AuthorityError(`target_repos[${index}] must use owner/repository form`);
    }
    if (names.has(target.toLowerCase())) throw new AuthorityError(`target_repos[${index}] duplicates an earlier target`);
    names.add(target.toLowerCase());
  });
  const owner = targets[0].split("/")[0].toLowerCase();
  if (targets.some(target => target.split("/")[0].toLowerCase() !== owner)) {
    throw new AuthorityError("target_repos must share one owner because one read credential covers the run");
  }
  return targets;
}

function validateDispatchCorrelation(input, controlRepository) {
  if (input.centralRepository !== controlRepository) {
    throw new AuthorityError("central_repo must identify the current control repository");
  }
  if (!/^[1-9][0-9]*-[1-9][0-9]*$/.test(input.correlationId)) {
    throw new AuthorityError("correlation_id must identify an orchestrator run and attempt");
  }
  const expected = `${input.serverUrl}/${controlRepository}/actions/runs/${input.correlationId.split("-", 1)[0]}`;
  if (input.controlPlaneRunUrl !== expected) {
    throw new AuthorityError("control_plane_run_url must match correlation_id and central_repo");
  }
}

export function resolveMultiTargetWorker(document, input, binding, { campaign: campaignName, worker: workerName }) {
  const deny = reason => ({
    authorized: false, reason, control_role: "multi-target-worker", campaign: campaignName, worker: workerName, enabled: false,
  });
  const control = document["control-plane"];
  if (!control) return deny("control-plane-absent");
  const campaign = Object.hasOwn(control.campaigns ?? {}, campaignName) ? control.campaigns[campaignName] : null;
  if (!campaign) return deny("campaign-undeclared");
  if (!(campaign.enabled ?? true)) return deny("campaign-disabled");
  const worker = Object.hasOwn(campaign.workers ?? {}, workerName) ? campaign.workers[workerName] : null;
  if (!worker) throw new AuthorityError(`unknown worker: ${campaignName}/${workerName}`);
  if (!(worker.enabled ?? true)) return deny("worker-disabled");
  if (!Object.hasOwn(worker, "max-targets-per-run")) return deny("worker-multi-target-undeclared");
  const prefix = `${binding.repository}/.github/workflows/${worker.workflow}.lock.yml@`;
  if (!binding.workflowRef.startsWith(prefix) || binding.workflowRef.length === prefix.length) {
    throw new AuthorityError("multi-target worker identity must match GITHUB_WORKFLOW_REF");
  }

  const scope = {
    owners: control.scope?.["allowed-owners"] ?? [binding.repository.split("/")[0]],
    repositories: control.scope?.["allowed-repositories"] ?? [],
  };
  const targets = parseTargetSet(input);
  targets.forEach((target, index) => {
    try {
      validateTargetScope(target, scope);
    } catch (error) {
      throw new AuthorityError(error.message.replace("target_repo", `target_repos[${index}]`));
    }
  });
  validateDispatchCorrelation(input, binding.repository);

  if (input.mode && input.mode !== "review") {
    throw new AuthorityError("safe_output_mode must be review for multi-target workers");
  }
  if (input.destination) {
    if (!REPOSITORY.test(input.destination)) throw new AuthorityError("safe_output_repo must use owner/repository form");
    const owner = input.destination.split("/")[0].toLowerCase();
    if (scope.owners.length && !scope.owners.some(candidate => candidate.trim().toLowerCase() === owner)) {
      throw new AuthorityError("safe_output_repo owner is outside control-plane.scope.allowed-owners");
    }
  }

  if (input.rolloutPercent) throw new AuthorityError("rollout_percent is not supported for multi-target workers");
  const bound = Math.min(
    worker["max-targets-per-run"],
    campaign["max-repositories"] ?? control.defaults?.["max-repositories"] ?? 1,
  );
  if (targets.length > narrowLimit(bound, input.maxTargets, "max_repositories")) {
    throw new AuthorityError("target_repos count exceeds the admitted repository limit");
  }

  const operation = `${campaignName}/${workerName}`;
  const decision = resolveEnvelope({
    operation,
    enabled: true,
    mode: campaign.mode ?? control.defaults?.mode ?? "review",
    maxMode: worker["max-mode"],
    maxTargets: bound,
    targets: campaign.targets,
    scope,
  }, {
    version: 1, operation, targets, mode: "review", destination: input.destination, maxTargets: input.maxTargets,
  }, binding);
  const envelope = decision.envelope;
  if (decision.decision !== "allow" || !envelope) {
    throw new AuthorityError("multi-target worker decision has no authority envelope");
  }
  const destination = envelope.grants[0].destination;
  if (envelope.grants.some(grant => grant.mode !== "review" || grant.destination.toLowerCase() !== destination.toLowerCase())) {
    throw new AuthorityError("multi-target workers require review grants with one common output destination");
  }
  return {
    authorized: true,
    reason: decision.reason,
    control_role: "multi-target-worker",
    campaign: campaignName,
    worker: workerName,
    enabled: true,
    safe_output_mode: "review",
    max_repositories: envelope.maxTargets,
    allowed_owners: scope.owners,
    allowed_repositories: scope.repositories,
    authority_envelope: envelope,
  };
}
