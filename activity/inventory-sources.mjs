import { readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { actionsLog as log } from "./actions-log.mjs";
import { resolveMarketplace } from "./marketplace.mjs";
import {
  compilerVersionFromLock,
  normalizeVersion,
  parseVersion,
  revisionUpdateState,
  updateState,
} from "./version.mjs";

const INTERNAL_CAMPAIGNS = new Set(["activity", "dashboard"]);
const POLICY_PATH = ".github/workflows/cao.json";
const WORKFLOW_PAGE_SIZE = 100;
const MAX_WORKFLOWS_PER_REPOSITORY = 10_000;
const RELEASE_PAGE_SIZE = 100;

function objectRecord(value) {
  return value && typeof value === "object" && !Array.isArray(value) ? value : {};
}

function rolloutMode(value) {
  return ["review", "live"].includes(value) ? value : "unknown";
}

function metadata(name, generatedAt, health = {}) {
  const result = {
    "source-id": `central-agentic-ops-${name}`,
    "source-kind": "github",
    "as-of": generatedAt,
    "retrieved-at": generatedAt,
    completeness: health.complete === false ? "partial" : "complete",
    freshness: "fresh",
    availability: health.available === false ? "unavailable" : "available",
  };
  if (Number.isFinite(health.expected)) result["coverage-expected"] = health.expected;
  if (Number.isFinite(health.observed)) result["coverage-observed"] = health.observed;
  if (health.operation) result["collection-operation"] = health.operation;
  if (health.state) result["collection-state"] = health.state;
  if (health.failureClass) result["failure-class"] = health.failureClass;
  if (health.reason) result["collection-reason"] = health.reason;
  if (Array.isArray(health.failures) && health.failures.length > 0) {
    result["repository-failures"] = health.failures;
  }
  return result;
}

function source(name, rows, generatedAt, health) {
  return { source: name, rows, metadata: metadata(name, generatedAt, health) };
}

function repositoryRows(discoveredRepositories, repository, generatedAt) {
  const repositories = new Map();
  for (const candidate of discoveredRepositories) {
    const fullName = typeof candidate === "string" ? candidate : candidate?.full_name;
    const [organization, name, ...extra] = String(fullName || "").trim().split("/");
    if (!organization || !name || extra.length > 0) continue;
    repositories.set(`${organization}/${name}`.toLowerCase(), {
      organization,
      repository: name,
      "repository-name": name,
      ...(typeof candidate === "object" && candidate
        ? { visibility: candidate.visibility || (candidate.private === true ? "private" : "public") }
        : {}),
      "observed-at": generatedAt,
    });
  }
  const [organization, name, ...extra] = repository.trim().split("/");
  if (organization && name && extra.length === 0 && !repositories.has(repository.toLowerCase())) {
    repositories.set(repository.toLowerCase(), {
      organization,
      repository: name,
      "repository-name": name,
      "observed-at": generatedAt,
    });
  }
  return [...repositories.values()];
}

async function githubResponse(fetchImplementation, apiUrl, token, path) {
  const response = await fetchImplementation(`${apiUrl.replace(/\/$/, "")}/${path}`, {
    headers: {
      accept: "application/vnd.github+json",
      authorization: ["Bearer", token].join(" "),
      "x-github-api-version": "2022-11-28",
    },
  });
  return response;
}

function repositoryFullName(candidate) {
  return typeof candidate === "string" ? candidate : candidate?.full_name;
}

function campaignRepository(candidate) {
  return campaignSource(candidate).repository;
}

function sourceProvenance(value) {
  const source = String(value || "").trim();
  const match = source.match(/^([A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+(?:\/[A-Za-z0-9._/-]+)?)(?:@(\S+))?$/i);
  if (!match) return { coordinate: "", repository: "", revision: "" };
  const [owner, repository] = match[1].split("/");
  return {
    coordinate: match[1],
    repository: `${owner}/${repository}`,
    revision: /^[0-9a-f]{40}$/i.test(match[2] || "") ? match[2] : "",
  };
}

function campaignSource(candidate) {
  const campaign = sourceProvenance(candidate?.campaign);
  return campaign.coordinate ? campaign : sourceProvenance(candidate?.source);
}

function shortRevision(value) {
  const revision = String(value || "").trim();
  return /^[0-9a-f]{40}$/i.test(revision) ? revision.slice(0, 12) : revision;
}

function contentPath(value) {
  return String(value || "").split("/").map(encodeURIComponent).join("/");
}

async function responseJson(response, description) {
  if (!response.ok) throw Object.assign(new Error(`${description}: ${response.status}`), { status: response.status });
  return response.json();
}

function repositoryFailure(repository, status, reason) {
  return {
    repository,
    state: "unavailable",
    "failure-class": status === 403 ? "permission" : "request",
    status: Number.isInteger(status) ? status : null,
    reason,
  };
}

function versionFailure(repository, operation, status, reason, workflow = "") {
  return {
    repository,
    operation,
    state: "unavailable",
    "failure-class": status === 403 ? "permission" : "request",
    status: Number.isInteger(status) ? status : null,
    reason,
    ...(workflow ? { workflow } : {}),
  };
}

function repositoryHealth(discoveredRepositories, rows) {
  const failures = discoveredRepositories
    .map((repository) => repository?.inventoryDiscovery)
    .filter(Boolean);
  const expected = discoveredRepositories.length;
  const observed = expected - failures.length;
  return {
    available: rows.length > 0 || expected === 0,
    complete: failures.length === 0,
    expected,
    observed,
    operation: "repository-discovery",
    state: failures.length === 0 ? "complete" : rows.length > 0 ? "partial" : "failed",
    failureClass: failures.some((failure) => failure["failure-class"] === "permission") ? "permission"
      : failures.length > 0 ? "request" : "",
    reason: failures.length > 0 ? `${failures.length} repositories could not be inspected` : "",
    failures,
  };
}

export async function discoverRepositories(controlSettings, {
  fetchImplementation = fetch,
  token = process.env.GH_TOKEN || process.env.GITHUB_TOKEN || "",
  apiUrl = process.env.GITHUB_API_URL || "https://api.github.com",
  controlRepository = process.env.GITHUB_REPOSITORY || "",
  credentialOwner = process.env.ACTIVITY_CREDENTIAL_OWNER || controlRepository.split("/", 1)[0],
} = {}) {
  if (!token) throw new Error("GH_TOKEN or GITHUB_TOKEN is required to discover repositories");
  const maximum = Number(controlSettings.policy_document?.["control-plane"]?.inventory?.["max-scan-repositories"] ?? 1000);
  const allowedRepositories = Array.isArray(controlSettings.allowed_repositories)
    ? controlSettings.allowed_repositories
    : [];
  if (allowedRepositories.length > 0) {
    if (allowedRepositories.length > maximum) {
      throw new Error("Allowed repositories exceed control-plane.inventory.max-scan-repositories");
    }
    const repositories = [];
    for (const repository of allowedRepositories) {
      try {
        const response = await githubResponse(fetchImplementation, apiUrl, token, `repos/${repository}`);
        if (!response.ok) {
          repositories.push({
            full_name: repository,
            visibility: "unknown",
            inventoryDiscovery: repositoryFailure(
              repository,
              response.status,
              `Unable to discover allowed repository ${repository}: ${response.status}`,
            ),
          });
          continue;
        }
        repositories.push(await response.json());
      } catch (error) {
        repositories.push({
          full_name: repository,
          visibility: "unknown",
          inventoryDiscovery: repositoryFailure(
            repository,
            null,
            `Unable to discover allowed repository ${repository}: ${error?.message || error}`,
          ),
        });
      }
    }
    return repositories;
  }

  const repositories = [];
  for (const owner of controlSettings.allowed_owners ?? []) {
    if (String(owner).toLowerCase() !== String(credentialOwner).toLowerCase()) {
      throw new Error(`Cannot completely discover repositories for ${owner} with the ${credentialOwner} credential`);
    }
    let endpoint = `orgs/${owner}/repos`;
    let installation = false;
    for (let page = 1; repositories.length < maximum; page += 1) {
      let response = await githubResponse(
        fetchImplementation,
        apiUrl,
        token,
        `${endpoint}?per_page=100&type=all&page=${page}`,
      );
      if (page === 1 && response.status === 404) {
        endpoint = `installation/repositories`;
        installation = true;
        response = await githubResponse(
          fetchImplementation,
          apiUrl,
          token,
          `${endpoint}?per_page=100&page=${page}`,
        );
        if (!response.ok) {
          endpoint = `users/${owner}/repos`;
          installation = false;
          response = await githubResponse(
            fetchImplementation,
            apiUrl,
            token,
            `${endpoint}?per_page=100&type=owner&page=${page}`,
          );
        }
      }
      if (!response.ok) throw new Error(`Unable to discover repositories for ${owner}: ${response.status}`);
      let payload = await response.json();
      let pageRepositories = installation ? payload?.repositories : payload;
      if (!Array.isArray(pageRepositories)) throw new Error(`Repository discovery returned invalid data for ${owner}`);
      let batch = installation
        ? pageRepositories.filter((repository) => (
          repository.full_name?.split("/", 1)[0]?.toLowerCase() === String(owner).toLowerCase()
        ))
        : pageRepositories;
      if (installation && page === 1 && batch.length === 0) {
        endpoint = `users/${owner}/repos`;
        installation = false;
        response = await githubResponse(
          fetchImplementation,
          apiUrl,
          token,
          `${endpoint}?per_page=100&type=owner&page=${page}`,
        );
        if (!response.ok) throw new Error(`Unable to discover repositories for ${owner}: ${response.status}`);
        payload = await response.json();
        pageRepositories = payload;
        if (!Array.isArray(pageRepositories)) throw new Error(`Repository discovery returned invalid data for ${owner}`);
        batch = pageRepositories;
      }
      repositories.push(...batch.slice(0, maximum - repositories.length));
      if (pageRepositories.length < 100) break;
    }
    if (repositories.length >= maximum) break;
  }
  return repositories;
}

function mergedSource(documents, name, key) {
  const candidates = documents.map((document) => document?.[name]).filter(Boolean);
  if (candidates.length === 0) return source(name, [], new Date().toISOString(), {
    available: false,
    complete: false,
    state: "failed",
    reason: `No ${name} source fragments were produced`,
  });
  const rows = new Map();
  for (const candidate of candidates) {
    for (const row of candidate.rows ?? []) rows.set(key(row), row);
  }
  const failures = candidates.flatMap((candidate) => candidate.metadata?.["repository-failures"] ?? []);
  const generatedAt = candidates
    .map((candidate) => candidate.metadata?.["as-of"])
    .filter(Boolean)
    .sort()
    .at(-1) ?? new Date().toISOString();
  return source(name, [...rows.values()], generatedAt, {
    available: candidates.some((candidate) => candidate.metadata?.availability !== "unavailable"),
    complete: candidates.every((candidate) => candidate.metadata?.completeness === "complete"),
    expected: candidates.reduce((total, candidate) => total + Number(candidate.metadata?.["coverage-expected"] ?? 0), 0),
    observed: candidates.reduce((total, candidate) => total + Number(candidate.metadata?.["coverage-observed"] ?? 0), 0),
    operation: candidates[0].metadata?.["collection-operation"],
    state: failures.length === 0 ? "complete" : rows.size > 0 ? "partial" : "failed",
    failureClass: failures.some((failure) => failure["failure-class"] === "permission") ? "permission"
      : failures.length > 0 ? "request" : "",
    reason: failures.length > 0 ? `${failures.length} repositories could not be inspected` : "",
    failures,
  });
}

export function mergeInventoryDashboardSources(documents) {
  if (!Array.isArray(documents) || documents.length === 0) {
    throw new Error("At least one inventory source fragment is required");
  }
  const result = {
    repositories: mergedSource(
      documents,
      "repositories",
      (row) => `${row.organization}/${row.repository}`.toLowerCase(),
    ),
    workflows: mergedSource(
      documents,
      "workflows",
      (row) => `${row.organization}/${row.repository}/${row.workflow}`.toLowerCase(),
    ),
  };
  for (const name of [
    "campaigns",
    "marketplace-packages",
    "marketplace-registries",
    "configuration-policy",
  ]) {
    result[name] = documents
      .map((document) => document?.[name])
      .filter(Boolean)
      .sort((left, right) => (
        Number(right.metadata?.completeness === "complete") - Number(left.metadata?.completeness === "complete")
        || (right.rows?.length ?? 0) - (left.rows?.length ?? 0)
      ))[0] ?? source(name, [], new Date().toISOString(), {
      available: false,
      complete: false,
      state: "failed",
      reason: `No ${name} source fragments were produced`,
    });
  }
  return result;
}

export function markInventoryCollectionPartial(sources, repositories, reason, names = ["repositories", "workflows"]) {
  for (const name of names) {
    const metadata = sources[name].metadata;
    metadata.completeness = "partial";
    metadata["collection-state"] = "partial";
    metadata["collection-reason"] = reason;
    metadata["failure-class"] = "request";
    metadata["repository-failures"] = [
      ...(metadata["repository-failures"] ?? []),
      ...repositories.map((repository) => ({
        repository,
        "failure-class": "request",
        reason,
        operation: "activity-collection",
      })),
    ];
  }
  return sources;
}

export async function discoverLatestSourceCommits(inventory = {}, {
  fetchImplementation = fetch,
  token = process.env.GH_TOKEN || process.env.GITHUB_TOKEN || "",
  apiUrl = process.env.GITHUB_API_URL || "https://api.github.com",
} = {}) {
  if (!token) throw new Error("GH_TOKEN or GITHUB_TOKEN is required to discover source versions");
  const repositories = [...new Set([
    ...(inventory.campaigns || []).map(campaignRepository),
    ...(inventory.bundles || []).map(campaignRepository),
    ...(inventory.workflows || []).map((workflow) => sourceProvenance(workflow.source).repository),
  ].filter(Boolean))];
  const commits = {};
  const failures = [];
  for (const repository of repositories) {
    try {
      const repositoryResponse = await githubResponse(fetchImplementation, apiUrl, token, `repos/${repository}`);
      const repositoryRecord = await responseJson(repositoryResponse, `Unable to inspect source repository ${repository}`);
      const defaultBranch = String(repositoryRecord?.default_branch || "").trim();
      if (!defaultBranch) throw new Error(`Source repository ${repository} did not report a default branch`);
      const commitResponse = await githubResponse(
        fetchImplementation,
        apiUrl,
        token,
        `repos/${repository}/commits/${contentPath(defaultBranch)}`,
      );
      const commit = await responseJson(commitResponse, `Unable to resolve source repository ${repository}`);
      const sha = String(commit?.sha || "").trim();
      if (!/^[0-9a-f]{40}$/i.test(sha)) throw new Error(`Source repository ${repository} did not report a commit SHA`);
      commits[repository] = sha;
    } catch (error) {
      failures.push(versionFailure(
        repository,
        "source-version-discovery",
        error?.status,
        error?.message || String(error),
      ));
    }
  }
  return {
    commits,
    failures,
    expected: repositories.length,
    observed: Object.keys(commits).length,
  };
}

export async function discoverLatestCampaignCommits(inventory = {}, options = {}) {
  return discoverLatestSourceCommits(inventory, options);
}

export async function discoverLatestGhAwVersion({
  fetchImplementation = fetch,
  token = process.env.GH_TOKEN || process.env.GITHUB_TOKEN || "",
  apiUrl = process.env.GITHUB_API_URL || "https://api.github.com",
} = {}) {
  if (!token) throw new Error("GH_TOKEN or GITHUB_TOKEN is required to discover the latest gh-aw version");
  for (let page = 1; ; page += 1) {
    const response = await githubResponse(
      fetchImplementation,
      apiUrl,
      token,
      `repos/github/gh-aw/releases?per_page=${RELEASE_PAGE_SIZE}&page=${page}`,
    );
    const releases = await responseJson(response, "Unable to discover the latest gh-aw release");
    if (!Array.isArray(releases)) throw new Error("gh-aw release discovery returned invalid data");
    const stable = releases.find((release) => {
      const parsed = parseVersion(release?.tag_name);
      return release?.draft !== true
        && release?.prerelease !== true
        && parsed
        && parsed.prerelease.length === 0;
    });
    if (stable) return normalizeVersion(stable.tag_name);
    if (releases.length < RELEASE_PAGE_SIZE) break;
  }
  throw new Error("No stable gh-aw release was found");
}

function workflowPath(value) {
  const path = String(value || "").trim();
  return path.startsWith(".github/workflows/") ? path : "";
}

function canonicalWorkflowPath(value) {
  const path = workflowPath(value).toLowerCase();
  return path.endsWith(".lock.yml")
    ? `${path.slice(0, -".lock.yml".length)}.md`
    : path;
}

function workflowRegistryRecord(repository, candidate) {
  const path = workflowPath(candidate?.path);
  const name = String(candidate?.name || "").trim();
  if (candidate?.state === "deleted") return null;
  if (!path || !name) return null;
  return {
    repository,
    id: candidate.id ?? null,
    name,
    path,
    state: String(candidate.state || "unknown"),
    htmlUrl: String(candidate.html_url || ""),
    createdAt: candidate.created_at || null,
    updatedAt: candidate.updated_at || null,
  };
}

export async function discoverWorkflowRegistries(discoveredRepositories, {
  fetchImplementation = fetch,
  token = process.env.GH_TOKEN || process.env.GITHUB_TOKEN || "",
  apiUrl = process.env.GITHUB_API_URL || "https://api.github.com",
  controlRepository = "",
} = {}) {
  if (!token) throw new Error("GH_TOKEN or GITHUB_TOKEN is required to discover workflows");
  const repositoryCandidates = new Map();
  for (const candidate of discoveredRepositories) {
    const repository = String(repositoryFullName(candidate) || "").trim();
    if (repository) repositoryCandidates.set(repository.toLowerCase(), candidate);
  }
  const controlRepositoryName = String(controlRepository || "").trim();
  if (controlRepositoryName && !repositoryCandidates.has(controlRepositoryName.toLowerCase())) {
    repositoryCandidates.set(controlRepositoryName.toLowerCase(), { full_name: controlRepositoryName });
  }
  const registries = [];
  for (const candidate of repositoryCandidates.values()) {
    const repository = String(repositoryFullName(candidate) || "").trim();
    if (!repository) continue;
    log.info`Discovering workflows for ${repository}`;
    const workflows = [];
    let expected = null;
    let observed = 0;
    let pages = 0;
    let failure = null;
    for (let page = 1; observed < MAX_WORKFLOWS_PER_REPOSITORY; page += 1) {
      try {
        const response = await githubResponse(
          fetchImplementation,
          apiUrl,
          token,
          `repos/${repository}/actions/workflows?per_page=${WORKFLOW_PAGE_SIZE}&page=${page}`,
        );
        if (!response.ok) {
          failure = repositoryFailure(
            repository,
            response.status,
            `Unable to discover workflows for ${repository}: ${response.status}`,
          );
          break;
        }
        const payload = await response.json();
        if (!Array.isArray(payload?.workflows)) {
          failure = repositoryFailure(repository, response.status, `Workflow discovery returned invalid data for ${repository}`);
          break;
        }
        pages += 1;
        if (Number.isFinite(Number(payload.total_count))) expected = Number(payload.total_count);
        const pageRecords = payload.workflows.slice(0, MAX_WORKFLOWS_PER_REPOSITORY - observed);
        observed += pageRecords.length;
        const pageWorkflows = pageRecords
          .map((workflow) => workflowRegistryRecord(repository, workflow))
          .filter(Boolean);
        workflows.push(...pageWorkflows);
        if (payload.workflows.length < WORKFLOW_PAGE_SIZE || (expected !== null && observed >= expected)) break;
      } catch (error) {
        failure = repositoryFailure(
          repository,
          null,
          `Unable to discover workflows for ${repository}: ${error?.message || error}`,
        );
        break;
      }
    }
    if (!failure && expected !== null && observed < expected) {
      failure = repositoryFailure(
        repository,
        200,
        `Workflow discovery for ${repository} stopped after ${observed} of ${expected} workflows`,
      );
    }
    registries.push({
      repository,
      workflows,
      expected,
      observed,
      pages,
      state: failure ? workflows.length > 0 ? "partial" : "unavailable" : "complete",
      failure,
    });
    log.info`Workflow discovery for ${repository}: ${failure ? workflows.length > 0 ? "partial" : "unavailable" : "complete"}; ${workflows.length} usable of ${observed} observed${expected === null ? "" : `, ${expected} expected`}; ${pages} pages`;
  }
  return registries;
}

function workflowContent(payload) {
  if (typeof payload === "string") return payload;
  if (typeof payload?.content !== "string") return "";
  return payload.encoding === "base64"
    ? Buffer.from(payload.content.replace(/\s/g, ""), "base64").toString("utf8")
    : payload.content;
}

export async function discoverWorkflowVersions(workflowRegistries, {
  fetchImplementation = fetch,
  token = process.env.GH_TOKEN || process.env.GITHUB_TOKEN || "",
  apiUrl = process.env.GITHUB_API_URL || "https://api.github.com",
} = {}) {
  if (!token) throw new Error("GH_TOKEN or GITHUB_TOKEN is required to discover workflow versions");
  const enriched = [];
  for (const registry of workflowRegistries) {
    log.info`Discovering compiler versions for ${registry.workflows?.length || 0} workflows in ${registry.repository}`;
    const versionFailures = [];
    const workflows = [];
    for (const workflow of registry.workflows || []) {
      if (!workflow.path.toLowerCase().endsWith(".lock.yml")) {
        workflows.push(workflow);
        continue;
      }
      try {
        const response = await githubResponse(
          fetchImplementation,
          apiUrl,
          token,
          `repos/${registry.repository}/contents/${contentPath(workflow.path)}`,
        );
        const payload = await responseJson(response, `Unable to read ${workflow.path} from ${registry.repository}`);
        const source = workflowContent(payload);
        if (!source) throw new Error(`Workflow ${workflow.path} in ${registry.repository} returned no content`);
        const ghAwVersion = compilerVersionFromLock(source);
        if (!ghAwVersion) throw new Error(`Workflow ${workflow.path} in ${registry.repository} has no valid compiler metadata`);
        workflows.push({
          ...workflow,
          ghAwVersion,
        });
      } catch (error) {
        versionFailures.push(versionFailure(
          registry.repository,
          "workflow-version-discovery",
          error?.status,
          error?.message || String(error),
          workflow.path,
        ));
        workflows.push(workflow);
      }
    }
    enriched.push({
      ...registry,
      workflows,
      versionFailures,
      versionState: versionFailures.length === 0 ? "complete" : workflows.length > 0 ? "partial" : "unavailable",
    });
    log.info`Workflow compiler version discovery for ${registry.repository}: ${workflows.length - versionFailures.length} resolved, ${versionFailures.length} unresolved`;
  }
  return enriched;
}

function workflowRegistryHealth(registries, rows) {
  const failures = registries.flatMap((registry) => [
    ...(registry.failure ? [{
      ...registry.failure,
      state: registry.state,
      expected: registry.expected,
      observed: registry.observed,
      pages: registry.pages,
    }] : []),
    ...(registry.versionFailures || []),
  ]);
  const expected = registries.length;
  const observed = registries.filter((registry) => (
    registry.state === "complete" && (registry.versionFailures || []).length === 0
  )).length;
  return {
    available: rows.length > 0 || observed > 0 || expected === 0,
    complete: failures.length === 0,
    expected,
    observed,
    operation: "workflow-registry-discovery",
    state: failures.length === 0 ? "complete" : observed > 0 || rows.length > 0 ? "partial" : "failed",
    failureClass: failures.some((failure) => failure["failure-class"] === "permission") ? "permission"
      : failures.length > 0 ? "request" : "",
    reason: failures.length > 0 ? `${failures.length} repository workflow registries were unavailable or incomplete` : "",
    failures,
  };
}

function campaignVersionHealth(resolution, rows) {
  const repositories = new Set(rows.map((row) => sourceProvenance(row["campaign-source"]).repository).filter(Boolean));
  const failures = (resolution?.failures || []).filter((failure) => repositories.has(failure.repository));
  const expected = repositories.size;
  const observed = [...repositories].filter((repository) => resolution?.commits?.[repository]).length;
  return {
    available: rows.length > 0 || expected === 0 || expected === undefined,
    complete: failures.length === 0,
    expected,
    observed,
    operation: "source-version-discovery",
    state: failures.length === 0 ? "complete" : observed > 0 ? "partial" : "failed",
    failureClass: failures.some((failure) => failure["failure-class"] === "permission") ? "permission"
      : failures.length > 0 ? "request" : "",
    reason: failures.length > 0 ? `${failures.length} campaign source repositories could not be resolved` : "",
    failures,
  };
}

function campaignRows(inventory, controlSettings, generatedAt, latestSourceCommits = {}) {
  const bundles = new Map((inventory.bundles || []).map((bundle) => [
    String(bundle.controlCampaign || bundle.id || "").trim(),
    bundle,
  ]).filter(([id]) => id));
  const registered = new Map((inventory.campaigns || []).map((entry) => [
    String(entry.id || "").trim(),
    entry,
  ]).filter(([id]) => id));
  const ids = new Set(
    [...bundles.keys(), ...registered.keys(), ...Object.keys(controlSettings.campaigns || {})]
      .filter((id) => !INTERNAL_CAMPAIGNS.has(id)),
  );
  return [...ids].sort().map((id) => {
    const bundle = bundles.get(id)
      || [...bundles.values()].find((candidate) => candidate.id === id)
      || {};
    const installed = registered.get(id) || {};
    const intelligenceDeclaration = bundle.intelligenceDeclaration
      || installed.intelligenceDeclaration
      || null;
    const source = campaignSource(installed).coordinate
      ? campaignSource(installed)
      : campaignSource(bundle);
    const repository = source.repository;
    const installedRevision = String(installed.resolvedCommit || bundle.version || "").trim();
    const latestRevision = String(latestSourceCommits[repository] || "").trim();
    const policy = controlSettings.campaigns?.[id] || {};
    const workers = Object.entries(policy.worker_policies || {}).map(([workflow, worker]) => ({
      id: worker.worker || workflow,
      workflow,
      enabled: worker.enabled !== false,
      "max-mode": worker.max_mode || null,
    }));
    const targets = Object.entries(policy.target_policies || {}).map(([repository, target]) => ({
      repository,
      mode: rolloutMode(target?.mode),
    }));
    const inventoryWorkers = bundle.workers || [];
    const inventoryWarnings = (bundle.compiled === true ? 0 : 1) + (bundle.missingWorkers || []).length;
    const aiCreditAllowance = [bundle.maxAiCredits, ...inventoryWorkers.map((worker) => worker.maxAiCredits)]
      .filter((value) => Number.isFinite(value) && value > 0)
      .reduce((total, value) => total + value, 0);
    return {
      campaign: id,
      "campaign-name": bundle.name || installed.name || id,
      "campaign-description": bundle.description || "",
      "campaign-icon": policy.icon || "goal",
      "campaign-mode": rolloutMode(policy.mode),
      "campaign-enabled": policy.enabled !== false,
      "campaign-max-repositories": policy["max-repositories"] ?? null,
      "campaign-rollout-percent": policy["rollout-percent"] ?? null,
      "campaign-monthly-ai-credit-budget": policy["monthly-ai-credit-budget"] ?? null,
      "campaign-aic-allowance": aiCreditAllowance || null,
      "campaign-worker-count": workers.length || inventoryWorkers.length,
      "campaign-inventory-warnings": inventoryWarnings,
      "campaign-workers": workers,
      "campaign-targets": targets,
      "campaign-intelligence-declaration": intelligenceDeclaration,
      "campaign-min-version": bundle.minVersion || "",
      "campaign-source": source.coordinate,
      "campaign-version": shortRevision(installedRevision) || "unknown",
      "campaign-current-version": shortRevision(latestRevision) || "unknown",
      "campaign-update-state": revisionUpdateState(installedRevision, latestRevision),
      "campaign-experimental": bundle.experimental === true,
      "campaign-readme-path": bundle.readmePath || "",
      "campaign-readme": bundle.readme || "",
      "observed-at": generatedAt,
    };
  });
}

function workflowAdmission(controlSettings, campaignName, role, workflowId) {
  if (!Object.hasOwn(controlSettings, "campaigns")) return null;
  if (controlSettings.policy_resolution?.status === "unavailable") {
    return { status: "unavailable", reason: controlSettings.policy_resolution.reason || "policy-resolution-unavailable" };
  }
  const campaignPolicy = controlSettings.campaigns?.[campaignName];
  if (!campaignPolicy) return { status: "blocked", reason: "campaign-undeclared" };
  if (campaignPolicy.enabled === false) return { status: "blocked", reason: "campaign-disabled" };
  if (role === "worker") {
    const workerPolicy = campaignPolicy.worker_policies?.[workflowId];
    if (!workerPolicy) return { status: "blocked", reason: "worker-undeclared" };
    if (workerPolicy.enabled === false) return { status: "blocked", reason: "worker-disabled" };
  }
  return { status: "authorized", reason: "authorized" };
}

function workflowCampaignDetails(inventory, controlSettings) {
  const details = new Map();
  for (const workflow of inventory.workflows || []) {
    const operation = controlSettings.operations?.[workflow.id];
    const operationStatus = operation
      ? controlSettings.policy_resolution?.status === "unavailable"
        ? { admissionStatus: "unavailable", admissionReason: "policy-resolution-unavailable" }
        : { admissionStatus: operation.enabled === false ? "blocked" : "authorized",
          admissionReason: operation.enabled === false ? "operation-disabled" : "authorized",
          configuredMode: rolloutMode(operation.mode ?? "review") }
      : {};
    details.set(workflow.sourcePath, {
      maxAiCredits: workflow.maxAiCredits,
      inventoryReady: workflow.compiled,
      ghAwVersion: normalizeVersion(workflow.ghAwVersion),
      ...operationStatus,
    });
  }
  for (const bundle of inventory.bundles || []) {
    const campaignId = String(bundle.controlCampaign || bundle.id || "").trim();
    const policy = controlSettings.campaigns?.[campaignId] || {};
    const configuredMode = rolloutMode(policy.mode);
    const rolloutPercent = Number(policy["rollout-percent"] ?? policy.rollout_percent);
    const targetPolicies = new Map(Object.entries(policy.targets ?? policy.target_policies ?? {})
      .map(([repository, targetPolicy]) => [repository.toLowerCase(), { repository, targetPolicy }]));
    const targetRepositories = new Map();
    for (const repository of controlSettings.allowed_repositories ?? []) {
      const name = String(repository).trim();
      if (name) targetRepositories.set(name.toLowerCase(), name);
    }
    for (const [repository, { repository: name }] of targetPolicies) targetRepositories.set(repository, name);
    const campaignTargets = [...targetRepositories.entries()]
      .map(([key, repository]) => ({
        repository,
        mode: rolloutMode(targetPolicies.get(key)?.targetPolicy?.mode ?? configuredMode),
        explicit: targetPolicies.has(key),
      }))
      .filter((target) => target.mode !== "unknown" && target.repository);
    const workers = bundle.workers || [];
    const allowance = [bundle.maxAiCredits, ...workers.map((worker) => worker.maxAiCredits)]
      .filter((value) => Number.isFinite(value) && value > 0)
      .reduce((total, value) => total + value, 0);
    const inventoryWarnings = (bundle.compiled === true ? 0 : 1) + (bundle.missingWorkers || []).length;
    const ready = bundle.compiled === true
      && (bundle.missingWorkers || []).length === 0
      && workers.every((worker) => worker.compiled !== false);
    for (const workflow of [
      { id: bundle.id, sourcePath: bundle.workflow, role: "orchestrator" },
      ...workers.map((worker) => ({ ...worker, role: "worker" })),
    ]) {
      if (!workflow.sourcePath) continue;
      const admission = workflowAdmission(controlSettings, campaignId, workflow.role, workflow.id);
      details.set(workflow.sourcePath, {
        ...details.get(workflow.sourcePath),
        campaign: campaignId,
        campaignName: bundle.name || campaignId,
        campaignDescription: bundle.description,
        campaignIcon: policy.icon || "goal",
        campaignReadmePath: bundle.readmePath,
        campaignReadme: bundle.readme,
        role: workflow.role,
        configuredMode,
        campaignTargets,
        allowance: allowance || null,
        workerCount: workers.length,
        inventoryWarnings,
        inventoryReady: ready,
        ...(Number.isFinite(rolloutPercent) ? { rolloutPercent } : {}),
        ...(admission ? { admissionStatus: admission.status, admissionReason: admission.reason } : {}),
      });
    }
  }
  return details;
}

function workflowLink(workflow) {
  return workflow.htmlUrl.startsWith("https://github.com/")
    ? { relation: "workflow", href: workflow.htmlUrl, label: `View ${workflow.name}` }
    : undefined;
}

function remoteWorkflowRow(workflow, generatedAt, latestGhAwVersion) {
  const [organization, repository] = workflow.repository.split("/");
  const active = workflow.state === "active"
    ? "true"
    : workflow.state.startsWith("disabled_") ? "false" : "unknown";
  const link = workflowLink(workflow);
  return {
    organization,
    repository,
    workflow: canonicalWorkflowPath(workflow.path),
    "workflow-name": workflow.name,
    "workflow-role": "standalone",
    "workflow-source-kind": "registry",
    "workflow-active": active,
    "workflow-registry-state": workflow.state,
    "gh-aw-version": workflow.ghAwVersion || "unknown",
    "gh-aw-current-version": latestGhAwVersion || "unknown",
    "gh-aw-update-state": updateState(workflow.ghAwVersion, latestGhAwVersion),
    ...(workflow.id !== null ? { "workflow-id": String(workflow.id) } : {}),
    ...(link ? { "workflow-link": link } : {}),
    ...(workflow.createdAt ? { "created-at": workflow.createdAt } : {}),
    ...(workflow.updatedAt ? { "updated-at": workflow.updatedAt } : {}),
    "rollout-mode": "unknown",
    "observed-at": generatedAt,
  };
}

function workflowRows(
  inventory,
  controlSettings,
  repository,
  generatedAt,
  workflowRegistries = [],
  latestGhAwVersion = null,
  latestSourceCommits = {},
) {
  const [organization, repositoryName] = repository.split("/");
  const campaignDetails = workflowCampaignDetails(inventory, controlSettings);
  const rows = new Map();
  for (const registry of workflowRegistries) {
    for (const workflow of registry.workflows) {
      const row = remoteWorkflowRow(workflow, generatedAt, latestGhAwVersion);
      const key = `${workflow.repository.toLowerCase()}:${canonicalWorkflowPath(workflow.path)}`;
      rows.set(key, row);
    }
  }
  for (const workflow of inventory.workflows || []) {
    const details = campaignDetails.get(workflow.sourcePath);
    const source = sourceProvenance(workflow.source);
    const installedSourceRevision = String(workflow.version || source.revision || "").trim();
    const latestSourceRevision = String(latestSourceCommits[source.repository] || "").trim();
    const repositoryKey = repository.toLowerCase();
    const targets = (details?.campaignTargets || [])
      .filter((target) => target.explicit || target.repository.toLowerCase() !== repositoryKey)
      .map(({ repository: targetRepository, mode }) => ({ repository: targetRepository, mode }));
    const localRow = {
      organization,
      repository: repositoryName,
      ...(details ? {
        campaign: details.campaign,
        "campaign-name": details.campaignName,
        "campaign-icon": details.campaignIcon,
        "campaign-aic-allowance": details.allowance,
        "campaign-worker-count": details.workerCount,
        "campaign-inventory-warnings": details.inventoryWarnings,
      } : {}),
      ...(Number.isFinite(details?.maxAiCredits) ? { "max-ai-credits": details.maxAiCredits } : {}),
      ...(details?.campaignDescription ? { "campaign-description": details.campaignDescription } : {}),
      ...(details?.campaignReadmePath ? { "campaign-readme-path": details.campaignReadmePath } : {}),
      ...(details?.campaignReadme ? { "campaign-readme": details.campaignReadme } : {}),
      ...(Number.isFinite(details?.rolloutPercent) ? { "campaign-rollout-percent": details.rolloutPercent } : {}),
      ...(targets.length > 0 ? { "campaign-targets": targets } : {}),
      ...(typeof details?.inventoryReady === "boolean" ? { "inventory-ready": details.inventoryReady } : {}),
      ...(details?.admissionStatus ? { "admission-status": details.admissionStatus } : {}),
      ...(details?.admissionReason ? { "admission-reason": details.admissionReason } : {}),
      workflow: workflow.sourcePath,
      "workflow-name": workflow.name,
      "workflow-role": details?.role || workflow.role || "standalone",
      "workflow-source-kind": source.coordinate ? "source-managed" : "local",
      "workflow-source": source.coordinate,
      "workflow-source-version": shortRevision(installedSourceRevision) || "unknown",
      "workflow-source-current-version": shortRevision(latestSourceRevision) || "unknown",
      "workflow-source-update-state": revisionUpdateState(installedSourceRevision, latestSourceRevision),
      "workflow-active": workflow.compiled === true ? "true" : "unknown",
      "gh-aw-version": details?.ghAwVersion || normalizeVersion(workflow.ghAwVersion) || "unknown",
      "gh-aw-current-version": latestGhAwVersion || "unknown",
      "gh-aw-update-state": updateState(details?.ghAwVersion || workflow.ghAwVersion, latestGhAwVersion),
      "rollout-mode": details?.campaignTargets?.find(
        (target) => target.repository.toLowerCase() === repositoryKey,
      )?.mode || details?.configuredMode || "unknown",
      "observed-at": generatedAt,
    };
    const key = `${repositoryKey}:${canonicalWorkflowPath(workflow.sourcePath)}`;
    const remoteRow = rows.get(key);
    const installedGhAwVersion = details?.ghAwVersion
      || normalizeVersion(workflow.ghAwVersion)
      || (remoteRow?.["gh-aw-version"] !== "unknown" ? remoteRow?.["gh-aw-version"] : null);
    rows.set(key, {
      ...remoteRow,
      ...localRow,
      "workflow-name": remoteRow?.["workflow-name"] || localRow["workflow-name"],
      "workflow-active": remoteRow
        ? remoteRow["workflow-active"]
        : workflowRegistries.length > 0 ? "unknown" : localRow["workflow-active"],
      ...(remoteRow?.["workflow-registry-state"]
        ? { "workflow-registry-state": remoteRow["workflow-registry-state"] }
        : {}),
      ...(remoteRow?.["workflow-id"] ? { "workflow-id": remoteRow["workflow-id"] } : {}),
      ...(remoteRow?.["workflow-link"] ? { "workflow-link": remoteRow["workflow-link"] } : {}),
      ...(remoteRow?.["created-at"] ? { "created-at": remoteRow["created-at"] } : {}),
      ...(remoteRow?.["updated-at"] ? { "updated-at": remoteRow["updated-at"] } : {}),
      "gh-aw-version": installedGhAwVersion || "unknown",
      "gh-aw-current-version": latestGhAwVersion || "unknown",
      "gh-aw-update-state": updateState(installedGhAwVersion, latestGhAwVersion),
    });
  }
  return [...rows.values()].sort((left, right) => (
    `${left.organization}/${left.repository}:${left.workflow}`
      .localeCompare(`${right.organization}/${right.repository}:${right.workflow}`)
  ));
}

function configurationPolicyRows(controlSettings) {
  const settings = objectRecord(controlSettings);
  const resolution = objectRecord(settings.policy_resolution);
  // No collected policy fields produces an empty Settings source. When any
  // policy field is present, the resolver status distinguishes "available" and
  // "unavailable"; any other status means collected but not validated.
  const hasDocument = Object.hasOwn(settings, "policy_document");
  const hasSource = Object.hasOwn(settings, "policy_source");
  const hasResolution = Object.hasOwn(settings, "policy_resolution");
  if (!hasDocument && !hasSource && !hasResolution) {
    return [];
  }
  const status = resolution.status;
  let diagnostic;
  if (status === "available") {
    diagnostic = {
      severity: "valid",
      title: "Policy is valid",
      detail: "The runtime policy resolver accepted this revision.",
    };
  } else if (status === "unavailable") {
    diagnostic = {
      severity: "error",
      title: "Policy validation failed",
      detail: resolution.reason || "The control policy could not be resolved.",
    };
  } else {
    diagnostic = {
      severity: "warning",
      title: "Policy validation status unavailable",
      detail: resolution.reason || "The control policy was collected but not validated.",
    };
  }
  return [{
    path: POLICY_PATH,
    document: settings.policy_document ?? null,
    raw: settings.policy_source || "",
    diagnostics: [{
      severity: diagnostic.severity,
      path: POLICY_PATH,
      title: diagnostic.title,
      detail: diagnostic.detail,
    }],
  }];
}

export function buildInventoryDashboardSources({
  inventory = {},
  controlSettings,
  discoveredRepositories = [],
  workflowRegistries = [],
  latestCampaignResolution = {},
  latestGhAwVersion = null,
  latestGhAwFailure = null,
  marketplace = { packages: [], diagnostics: [] },
  repository = "",
  generatedAt = inventory.generatedAt || new Date().toISOString(),
}) {
  const settings = objectRecord(controlSettings);
  const repositories = repositoryRows(discoveredRepositories, repository, generatedAt);
  const workflows = workflowRows(
    inventory,
    settings,
    repository,
    generatedAt,
    workflowRegistries,
    latestGhAwVersion,
    latestCampaignResolution.commits,
  );
  const campaignInventory = campaignRows(inventory, settings, generatedAt, latestCampaignResolution.commits);
  const workflowHealth = workflowRegistryHealth(workflowRegistries, workflows);
  if (latestGhAwFailure) {
    workflowHealth.complete = false;
    workflowHealth.state = workflowHealth.available ? "partial" : "failed";
    workflowHealth.failureClass = latestGhAwFailure["failure-class"];
    workflowHealth.reason = "The latest stable gh-aw release could not be resolved";
    workflowHealth.failures = [...(workflowHealth.failures || []), latestGhAwFailure];
  }
  const workflowSourceRepositories = new Set((inventory.workflows || [])
    .map((workflow) => sourceProvenance(workflow.source).repository)
    .filter(Boolean));
  const workflowSourceFailures = (latestCampaignResolution.failures || [])
    .filter((failure) => workflowSourceRepositories.has(failure.repository));
  if (workflowSourceFailures.length > 0) {
    workflowHealth.complete = false;
    workflowHealth.state = workflowHealth.available ? "partial" : "failed";
    workflowHealth.failureClass = workflowSourceFailures.some(
      (failure) => failure["failure-class"] === "permission",
    ) ? "permission" : "request";
    workflowHealth.reason = `${workflowSourceFailures.length} workflow source repositories could not be resolved`;
    workflowHealth.failures = [...(workflowHealth.failures || []), ...workflowSourceFailures];
  }
  return {
    campaigns: source(
      "campaigns",
      campaignInventory,
      generatedAt,
      campaignVersionHealth(latestCampaignResolution, campaignInventory),
    ),
    repositories: source(
      "repositories",
      repositories,
      generatedAt,
      repositoryHealth(discoveredRepositories, repositories),
    ),
    workflows: source(
      "workflows",
      workflows,
      generatedAt,
      workflowHealth,
    ),
    "marketplace-packages": source(
      "marketplace-packages",
      marketplace.packages,
      generatedAt,
      {
        available: marketplace.diagnostics.some((diagnostic) => diagnostic.status === "available")
          || marketplace.diagnostics.length === 0,
        complete: marketplace.diagnostics.every((diagnostic) => diagnostic.status === "available"),
        failures: marketplace.diagnostics.filter((diagnostic) => diagnostic.status !== "available"),
      },
    ),
    "marketplace-registries": source(
      "marketplace-registries",
      marketplace.diagnostics,
      generatedAt,
      {
        available: true,
        complete: marketplace.diagnostics.every((diagnostic) => diagnostic.status === "available"),
      },
    ),
    "configuration-policy": source(
      "configuration-policy",
      configurationPolicyRows(settings),
      generatedAt,
    ),
  };
}

export async function discoverInventoryDashboardSources({
  inventory,
  controlSettings,
  repository,
} = {}) {
  const discoveredRepositories = await discoverRepositories(controlSettings, { controlRepository: repository });
  log.info`Repository discovery selected ${discoveredRepositories.length} repositories`;
  log.info`Starting workflow registry, source version, and gh-aw release discovery`;
  const [rawWorkflowRegistries, latestCampaignResolution, latestGhAwResolution, marketplace] = await Promise.all([
    discoverWorkflowRegistries(discoveredRepositories, { controlRepository: repository }),
    discoverLatestSourceCommits(inventory),
    discoverLatestGhAwVersion().then((version) => ({ version, failure: null })).catch((error) => ({
      version: null,
      failure: versionFailure(
        "github/gh-aw",
        "gh-aw-version-discovery",
        error?.status,
        error?.message || String(error),
      ),
    })),
    resolveMarketplace(controlSettings?.marketplace ?? { registries: [] }),
  ]);
  const workflowRegistries = await discoverWorkflowVersions(rawWorkflowRegistries);
  log.info`Workflow discovery completed for ${workflowRegistries.length} repository registries`;
  return buildInventoryDashboardSources({
    inventory,
    controlSettings,
    discoveredRepositories,
    workflowRegistries,
    latestCampaignResolution,
    latestGhAwVersion: latestGhAwResolution.version,
    latestGhAwFailure: latestGhAwResolution.failure,
    marketplace,
    repository,
  });
}

export async function main() {
  const inventoryPath = process.env.REPORT_INVENTORY;
  const controlSettingsPath = process.env.REPORT_CONTROL_SETTINGS;
  const outputPath = process.env.REPORT_INVENTORY_SOURCES;
  const repository = process.env.GITHUB_REPOSITORY;
  if (!inventoryPath || !controlSettingsPath || !outputPath || !repository) {
    throw new Error("REPORT_INVENTORY, REPORT_CONTROL_SETTINGS, REPORT_INVENTORY_SOURCES, and GITHUB_REPOSITORY are required");
  }

  log.group`Build dashboard inventory sources`;
  try {
    const [inventory, controlSettings] = await Promise.all([
      readFile(inventoryPath, "utf8").then(JSON.parse),
      readFile(controlSettingsPath, "utf8").then(JSON.parse),
    ]);
    const sources = await discoverInventoryDashboardSources({
      inventory,
      controlSettings,
      repository,
    });
    await writeFile(path.resolve(outputPath), `${JSON.stringify(sources, null, 2)}\n`);
    const repositoryFailures = sources.workflows.metadata["repository-failures"] ?? [];
    if (repositoryFailures.length > 0) {
      log.warning`Workflow registry discovery was incomplete for ${repositoryFailures.length} repositories`;
    }
    const unavailableRegistries = sources["marketplace-registries"].rows
      .filter((registry) => registry.status !== "available");
    if (unavailableRegistries.length > 0) {
      log.warning`Marketplace discovery was incomplete for ${unavailableRegistries.length} registries`;
    }
    log.info`Wrote ${sources.repositories.rows.length} repositories, ${sources.campaigns.rows.length} campaigns, and ${sources.workflows.rows.length} workflows`;
  } finally {
    log.endGroup();
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  main().catch((error) => {
    log.error`${error.stack || error.message || error}`;
    process.exitCode = 1;
  });
}
