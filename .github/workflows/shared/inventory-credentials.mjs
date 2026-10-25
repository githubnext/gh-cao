import { createSign } from "node:crypto";
import { validateTargetScope } from "./authority.mjs";

const REPOSITORY = /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/;
const READ_PERMISSIONS = Object.freeze({ actions: "read", contents: "read" });

export class InventoryError extends Error {
  constructor(code, message, { status = undefined, phase = "inventory" } = {}) {
    super(message);
    this.code = code;
    this.phase = phase;
    this.status = Number.isInteger(status) ? status : null;
    this.cleanupFailure = "";
  }
}

export function credentialFailure(error, phase = "inventory") {
  if (error instanceof InventoryError) return error;
  const status = error?.status;
  const headers = error?.response?.headers ?? {};
  const limited = status === 429 || status === 403 && (
    headers["x-ratelimit-remaining"] === "0" || headers["retry-after"] !== undefined
  );
  const code = limited ? "api-capacity"
    : status === 401 ? "credential-rejected"
      : status === 403 ? "permission-denied"
        : status === 404 ? "repository-inaccessible"
          : status === 422 ? "credential-scope-rejected"
            : status >= 500 ? "service-unavailable" : "request-failed";
  const messages = {
    "api-capacity": "GitHub API capacity is unavailable; respect Retry-After or the rate-limit reset before retrying.",
    "credential-rejected": "The selected credential was rejected; verify its validity and expiration.",
    "permission-denied": "GitHub denied this request; verify the selected profile's permissions and organization restrictions.",
    "repository-inaccessible": "GitHub returned 404; verify repository identity, selected token scope, installation access and organization restrictions. A 404 alone does not establish missing installation membership.",
    "credential-scope-rejected": "GitHub rejected the exact repository scope or requested permissions; verify installation access and App permissions.",
    "service-unavailable": "GitHub is unavailable; check service status before retrying.",
    "request-failed": "The credential-bound request could not be completed; check connectivity and GitHub service status.",
  };
  return new InventoryError(code, messages[code], { status, phase });
}

export function inventoryScope(policy, {
  target = "",
  excludeControl = false,
  controlRepository = "",
} = {}) {
  const repositories = target ? [target] : policy.allowed_repositories;
  if (!Array.isArray(repositories) || repositories.length === 0) {
    throw new InventoryError("exact-enrollment-required",
      "Inventory requires an explicit target or exact allowed-repositories enrollment. Owner-wide discovery is not supported by this credential path.");
  }
  const scope = { owners: policy.allowed_owners, repositories: policy.allowed_repositories };
  const unique = new Map();
  for (const repository of repositories) {
    if (!REPOSITORY.test(repository) || [".", ".."].includes(repository.split("/")[1])) {
      throw new InventoryError("malformed-enrollment", "Inventory enrollment must contain exact OWNER/REPOSITORY coordinates.");
    }
    try {
      validateTargetScope(repository, scope);
    } catch {
      throw new InventoryError("target-outside-scope", "An inventory target is outside the admitted owner/repository scope.");
    }
    if (excludeControl && repository.toLowerCase() === controlRepository.toLowerCase()) {
      if (target) throw new InventoryError("target-excluded", "The importing workflow excludes the control repository from inventory.");
      continue;
    }
    unique.set(repository.toLowerCase(), repository);
  }
  const result = [...unique.values()];
  if (result.length > policy.inventory?.["max-scan-repositories"]) {
    throw new InventoryError("inventory-limit-exceeded", "Exact enrollment exceeds max-scan-repositories; inventory cannot execute partially.");
  }
  return result;
}

export function readProfile(env) {
  const mode = env.CAO_AUTH_MODE || (
    env.CAO_READ_APP_ID || env.CAO_READ_APP_PRIVATE_KEY ? "app"
      : env.CAO_LEGACY_READ_TOKEN ? "legacy-token" : "workflow-token"
  );
  if (!["app", "pat", "workflow-token", "legacy-token"].includes(mode)
    || mode === "legacy-token" && env.CAO_AUTH_MODE) {
    throw new InventoryError("authentication-profile-invalid", "GH_AW_GITHUB_AUTH_MODE must select app, pat, or workflow-token.");
  }
  return mode;
}

function patMap(env) {
  try {
    const mapping = JSON.parse(env.CAO_READ_PAT_REPOSITORIES || "{}");
    if (!mapping || typeof mapping !== "object" || Array.isArray(mapping)) throw new Error();
    return mapping;
  } catch {
    throw new InventoryError("pat-enrollment-invalid", "GH_AW_GITHUB_READ_PAT_REPOSITORIES must be a repository-to-secret-name object.");
  }
}

function patSecret(mapping, repository) {
  const entries = Object.entries(mapping).filter(([name]) => name.toLowerCase() === repository.toLowerCase());
  if (entries.length !== 1 || !/^[A-Z_][A-Z0-9_]*$/.test(String(entries[0][1]))) {
    throw new InventoryError("pat-mapping-missing", "The selected PAT profile needs one unambiguous read-secret mapping for every required repository.");
  }
  return entries[0][1];
}

function appJwt(id, key) {
  try {
    if (!id || !key) throw new Error();
    const now = Math.floor(Date.now() / 1000);
    const header = Buffer.from(JSON.stringify({ alg: "RS256", typ: "JWT" })).toString("base64url");
    const payload = Buffer.from(JSON.stringify({ iat: now - 60, exp: now + 540, iss: id })).toString("base64url");
    const unsigned = `${header}.${payload}`;
    const signature = createSign("RSA-SHA256").update(unsigned).sign(key, "base64url");
    return `${unsigned}.${signature}`;
  } catch {
    throw new InventoryError("app-credential-unavailable", "The selected App requires a valid App ID and private key in Actions configuration.");
  }
}

export async function verifyCredentials({ settings, controlRepository, workflowSha, getOctokit, core, env = process.env }) {
  /** @type {{check: string, status: string, code?: string, http_status?: number | null, message?: string}[]} */
  const readChecks = [];
  /** @type {typeof readChecks} */
  const outputChecks = [];
  const report = {
    schema_version: 1,
    workflow_sha: workflowSha,
    read_checks: readChecks,
    output_checks: outputChecks,
    issue_publication: "unverified",
    ok: false,
  };
  const record = async (checks, check, operation) => {
    try {
      await operation();
      checks.push({ check, status: "passed" });
    } catch (error) {
      const failure = credentialFailure(error, check);
      checks.push({
        check, status: "incomplete", code: failure.code, http_status: failure.status,
        message: [failure.message, failure.cleanupFailure].filter(Boolean).join(" "),
      });
    }
  };
  await record(report.read_checks, "runtime-read-profile", async () => {
    const repositories = inventoryScope(settings, { controlRepository });
    await withInventoryCredentials({ repositories, controlRepository, getOctokit, core, env }, async ({ control, inventory, profile }) => {
      await record(report.read_checks, "control-source", () => control.request(
        `GET /repos/${controlRepository}/contents/.github/workflows/cao.json`, { ref: workflowSha },
      ));
      await record(report.read_checks, "control-workflows", () => control.request(
        `GET /repos/${controlRepository}/actions/workflows`, { per_page: 1 },
      ));
      for (const repository of repositories) {
        await record(report.read_checks, `target:${repository}`, async () => {
          const response = await inventory.request(`GET /repos/${repository}`);
          if (response.data?.full_name?.toLowerCase() !== repository.toLowerCase()) {
            throw new InventoryError("inventory-response-invalid", "The returned repository does not match exact enrollment.");
          }
          await inventory.request(`GET /repos/${repository}/actions/workflows`, { per_page: 1 });
          await inventory.request(`GET /repos/${repository}/branches`, { per_page: 1 });
        });
      }
      const permission = env.CAO_VERIFY_OUTPUT_PERMISSION || "issues";
      if (!["issues", "actions", "contents", "pull_requests"].includes(permission)
        || !["review", "live"].includes(env.CAO_VERIFY_OUTPUT_MODE || "review")) {
        throw new InventoryError("verification-input-invalid", "Select a supported output permission and review or live destination mode.");
      }
      const destinations = env.CAO_VERIFY_OUTPUT_MODE === "live" ? repositories : [controlRepository];
      for (const destination of destinations) {
        await record(report.output_checks, `output:${destination}:${permission}`, async () => {
          if (profile !== "app") {
            throw new InventoryError("output-permission-unverified",
              "Read-only checks cannot establish this token's output-write permission. Verify the declared safe-output credential separately.");
          }
          const jwt = appJwt(env.CAO_WRITE_APP_ID, env.CAO_WRITE_APP_PRIVATE_KEY);
          core?.setSecret(jwt);
          const app = getOctokit(jwt, { request: { redirect: "error" } });
          const response = await app.request(`GET /repos/${destination}/installation`);
          if (response.data?.permissions?.[permission] !== "write") {
            throw new InventoryError("output-permission-missing",
              "The selected write App installation does not grant the requested safe-output permission. No write was attempted.");
          }
        });
      }
    });
  });
  report.ok = [...report.read_checks, ...report.output_checks].every(check => check.status === "passed");
  return report;
}

// Tokens never leave this closure or enter the policy/precompute handoff.
export async function withInventoryCredentials({
  repositories,
  controlRepository,
  getOctokit,
  core,
  env = process.env,
}, operation) {
  const profile = readProfile(env);
  const clients = new Map();
  const minted = [];
  let failure;
  const octokit = token => {
    if (!token) throw new InventoryError("credential-unavailable", `The selected ${profile} read credential is unavailable.`);
    core?.setSecret(token);
    return getOctokit(token, { request: { redirect: "error" } });
  };
  try {
    if (profile === "app") {
      // The independently minted control token remains control-repository-only.
      if (!env.CAO_CONTROL_READ_TOKEN) {
        throw new InventoryError("control-credential-unavailable", "The selected read App could not produce a control-repository token.", { phase: "control" });
      }

      const app = octokit(appJwt(env.CAO_READ_APP_ID, env.CAO_READ_APP_PRIVATE_KEY));
      const byOwner = new Map();
      for (const repository of repositories) {
        const owner = repository.split("/")[0].toLowerCase();
        if (!byOwner.has(owner)) byOwner.set(owner, []);
        byOwner.get(owner).push(repository);
      }
      for (const group of byOwner.values()) {
        const installation = await app.request(`GET /repos/${group[0]}/installation`);
        if (!Number.isSafeInteger(installation.data?.id) || installation.data.id <= 0) {
          throw new InventoryError("installation-response-invalid", "GitHub returned an invalid App installation identity.");
        }
        for (let index = 0; index < group.length; index += 500) {
          const batch = group.slice(index, index + 500);
          const response = await app.request("POST /app/installations/{installation_id}/access_tokens", {
            installation_id: installation.data.id,
            repositories: batch.map(repository => repository.split("/")[1]),
            permissions: READ_PERMISSIONS,
          });
          const client = octokit(response.data?.token);
          minted.push(client);
          for (const repository of batch) clients.set(repository.toLowerCase(), client);
        }
      }

    } else {
      let token;
      if (profile === "pat") {
        const mapping = patMap(env);
        const selectedRepository = env.CAO_INVENTORY_PAT_REPOSITORY || controlRepository;
        const selected = patSecret(mapping, selectedRepository);
        const conflictingRepository = repositories.find(repository => patSecret(mapping, repository) !== selected);
        if (conflictingRepository) {
          throw new InventoryError("pat-fleet-unsupported",
            `PAT inventory spans multiple PAT secrets: the read-secret mappings for ${selectedRepository} (selected credential) and ${conflictingRepository} differ in GH_AW_GITHUB_READ_PAT_REPOSITORIES. `
            + "Precompute receives only the selected inventory PAT and cannot read the entire enrolled fleet with it. "
            + "Configure the App profile (GH_AW_GITHUB_AUTH_MODE=app) with access to every enrolled repository, or dispatch with an explicit in-scope target_repo. "
            + "max_repos limits dispatch, not required inventory. No credential fallback or partial inventory is allowed.");
        }
        token = env.CAO_INVENTORY_READ_PAT;
      } else {
        token = profile === "legacy-token" ? env.CAO_LEGACY_READ_TOKEN : env.CAO_WORKFLOW_TOKEN;
      }
      const client = octokit(token);
      for (const repository of repositories) clients.set(repository.toLowerCase(), client);
    }
    let controlToken = env.CAO_CONTROL_READ_TOKEN;
    if (profile === "pat") {
      patSecret(patMap(env), controlRepository);
      controlToken = env.CAO_CONTROL_READ_PAT;
    } else if (profile !== "app") {
      controlToken = profile === "legacy-token" ? env.CAO_LEGACY_READ_TOKEN : env.CAO_WORKFLOW_TOKEN;
    }
    const control = octokit(controlToken);
    const inventory = {
      async request(route, parameters) {
        const match = /^GET \/repos\/([^/]+\/[^/?]+)(?:[/?]|$)/.exec(route);
        const client = match && clients.get(match[1].toLowerCase());
        if (!client) throw new InventoryError("inventory-client-scope", "Inventory attempted a request outside its exact admitted repository scope.");
        try {
          return await client.request(route, parameters);
        } catch (error) {
          throw credentialFailure(error);
        }
      },
    };
    return await operation({ control, inventory, profile });
  } catch (error) {
    failure = credentialFailure(error);
    throw failure;
  } finally {
    const results = await Promise.allSettled(minted.map(client => client.request("DELETE /installation/token")));
    if (results.some(result => result.status === "rejected")) {
      const cleanup = new InventoryError("credential-cleanup-failed",
        "An inventory token could not be revoked. It expires automatically; investigate GitHub availability before retrying.");
      if (!failure) throw cleanup;
      failure.cleanupFailure = cleanup.message;
    }
  }
}
