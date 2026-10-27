import { readFileSync, realpathSync } from "node:fs";
import { dirname, isAbsolute, relative, resolve } from "node:path";
import { AuthorityError as PolicyError, narrowLimit, resolveAuthority } from "./authority.mjs";
export { AuthorityError as PolicyError } from "./authority.mjs";

const SCHEMA_URI = "https://raw.githubusercontent.com/githubnext/gh-cao/main/.github/workflows/shared/cao.schema.json";
const ROOT_KEYS = ["$schema", "version", "gh-aw-version", "control-plane", "target-authority"];
const CONTROL_KEYS = ["scope", "activity", "inventory", "web", "defaults", "campaigns", "workflows", "operations", "publishing", "marketplace", "webhook-gateway"];
const SCOPE_KEYS = ["allowed-owners", "allowed-repositories"];
const ACTIVITY_KEYS = ["repositories", "public-only"];
const INVENTORY_KEYS = ["max-scan-repositories", "cell-count", "cell-index", "batch-size", "batch-index"];
const WEB_KEYS = ["experimental", "favicon", "host"];
const HOST_KEYS = ["target", "redis", "operational-store", "cors"];
const OPERATIONAL_STORE_KEYS = ["backend", "single-process", "allow-volatile", "namespace-env", "redis", "postgres"];
const CORS_KEYS = ["allowed-origins", "max-age"];
const CORS_ORIGIN_PATTERN =
  /^(?:https:\/\/(?:[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*|\[[0-9A-Fa-f:.]+\])|http:\/\/(?:localhost|127(?:\.[0-9]{1,3}){3}|\[::1\]))(?::[0-9]{1,5})?\/?$/;
const TARGET_KEYS = [
  "module", "name", "authentication", "listener", "require-https",
  "trust-platform-proxy", "supports-single-replica", "replicas",
];
const TARGET_MODULES = ["generic", "container", "azure-functions"];
const REDIS_KEYS = [
  "module", "url-env", "host-env", "port-env", "username-env", "password-env",
  "namespace-env", "session", "isolate-process-namespace",
  "single-replica", "supports-collection", "allow-private-plaintext", "tls",
];
const REDIS_TLS_KEYS = ["mode", "server-name-env", "ca-certificate-env"];
const REDIS_MODULES = [
  "generic", "local", "upstash", "aws-elasticache", "redis-cloud", "gcp-memorystore",
  "railway", "render", "digitalocean",
];
const DEFAULT_KEYS = ["mode", "max-repositories", "rollout-percent", "monthly-ai-credit-budget"];
const OCTICONS = [
  "mark-github", "code", "repo", "server", "issue", "pull-request", "play", "eye",
  "shield", "meter", "graph", "codescan", "dependabot", "key", "beaker", "rocket",
  "workflow", "gear", "check-circle", "campaign", "external-link",
];
const CAMPAIGN_KEYS = ["enabled", ...DEFAULT_KEYS, "icon", "deploy", "targets", "workers", "workflow-values"];
const WORKFLOW_KEYS = ["enabled", ...DEFAULT_KEYS, "max-mode", "targets"];
const OPERATION_KEYS = ["enabled", "mode", "max-mode", "max-repositories", "targets"];
const TARGET_POLICY_KEYS = ["mode"];
const WORKER_KEYS = ["workflow", "enabled", "max-mode"];
const PUBLISHING_KEYS = ["enabled", "control-repositories", "reviewers"];
const MARKETPLACE_KEYS = ["cache-ttl-seconds", "registries"];
const REGISTRY_KEYS = ["id", "name", "repository", "path", "ref", "format", "api-url", "auth"];
const REGISTRY_AUTH_KEYS = {
  none: ["type"],
  pat: ["type", "secret"],
  "github-app": ["type", "app-id-secret", "private-key-secret", "installation-id-secret"],
};
const SECRET_REFERENCE_PATTERN = /^[A-Z][A-Z0-9_]{0,127}$/;
const SAFE_REGISTRY_PATH_PATTERN = /^(?!\/)(?!.*(?:^|\/)\.\.(?:\/|$))[A-Za-z0-9._/-]*$/;
const REGISTRY_NAME_PATTERN = /^.{1,100}$/s;
const REGISTRY_REF_PATTERN = /^.{1,255}$/s;
const HTTPS_URL_PATTERN = /^https:\/\/\S+$/;
const TARGET_AUTHORITY_KEYS = ["campaigns"];
const TARGET_CAMPAIGN_KEYS = ["authority"];
const REPOSITORY_PATTERN = /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/;
const OWNER_PATTERN = /^[A-Za-z0-9][A-Za-z0-9-]*$/;
const LOGIN_PATTERN = /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$/;
const SLUG_PATTERN = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const GH_AW_VERSION_PATTERN = /^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$/;
const MODES = ["review", "live"];
const MAX_PROFILE_DEPTH = 8;

function log(message) {
  console.error(`[CAO policy] ${message}`);
}

function isRecord(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

export function parsePolicy(source) {
  log("Parsing control policy.");
  const document = parseDocument(source);
  validateDocument(document);
  log("Validated control policy.");
  return document;
}

function parseDocument(source) {
  let document;
  try {
    document = JSON.parse(source);
    assertNoDuplicateKeys(source);
  } catch (error) {
    if (error instanceof PolicyError) throw error;
    throw new PolicyError(`invalid policy JSON: ${error.message}`);
  }
  if (!isRecord(document)) throw new PolicyError("policy root must be a mapping");

  rejectExpressions(document);
  return document;
}

export function loadPolicyFile(path) {
  const entryPath = realpathSync(resolve(path));
  const rootDirectory = realpathSync(dirname(entryPath));
  const loading = new Set();

  function load(currentPath, depth) {
    if (depth > MAX_PROFILE_DEPTH) {
      throw new PolicyError(`deployment profile import depth exceeds ${MAX_PROFILE_DEPTH}`);
    }
    const canonicalPath = realpathSync(currentPath);
    if (loading.has(canonicalPath)) throw new PolicyError("deployment profile import cycle");
    const pathFromRoot = relative(rootDirectory, canonicalPath);
    if (pathFromRoot === ".." || pathFromRoot.startsWith(`..${process.platform === "win32" ? "\\" : "/"}`) || isAbsolute(pathFromRoot)) {
      throw new PolicyError("deployment profile imports must remain within the policy directory");
    }

    loading.add(canonicalPath);
    try {
      const document = parseDocument(readFileSync(canonicalPath, "utf8"));
      const basePath = document.extends;
      if (basePath === undefined) return document;
      validateDeploymentProfile(document);
      if (typeof basePath !== "string" || !basePath || isAbsolute(basePath)) {
        throw new PolicyError("deployment profile extends must be a non-empty relative path");
      }
      const importedPath = resolve(dirname(canonicalPath), basePath);
      const overlay = { ...document };
      delete overlay.extends;
      return mergePolicyObjects(load(importedPath, depth + 1), overlay);
    } finally {
      loading.delete(canonicalPath);
    }
  }

  log(`Loading control policy profile ${entryPath}.`);
  const document = load(entryPath, 0);
  validateDocument(document);
  log("Validated composed control policy.");
  return document;
}

function validateDeploymentProfile(document) {
  assertKeys(document, ["extends", "control-plane"], "deployment profile");
  if (!("extends" in document) || !("control-plane" in document)) {
    throw new PolicyError("deployment profile requires extends and control-plane.web.host");
  }
  assertMapping(document["control-plane"], "deployment profile.control-plane");
  assertKeys(document["control-plane"], ["web"], "deployment profile.control-plane");
  if (!("web" in document["control-plane"])) {
    throw new PolicyError("deployment profile requires control-plane.web.host");
  }
  assertMapping(document["control-plane"].web, "deployment profile.control-plane.web");
  assertKeys(document["control-plane"].web, ["host"], "deployment profile.control-plane.web");
  if (!("host" in document["control-plane"].web)) {
    throw new PolicyError("deployment profile requires control-plane.web.host");
  }
}

function mergePolicyObjects(base, overlay) {
  if (!isRecord(base) || !isRecord(overlay)) return overlay;
  const merged = { ...base };
  for (const [key, value] of Object.entries(overlay)) {
    merged[key] = key in base && isRecord(base[key]) && isRecord(value)
      ? mergePolicyObjects(base[key], value)
      : value;
  }
  return merged;
}

function assertNoDuplicateKeys(source) {
  let offset = 0;

  function skipWhitespace() {
    while (/\s/.test(source[offset] ?? "")) offset += 1;
  }

  function scanString() {
    const start = offset;
    offset += 1;
    while (source[offset] !== '"') {
      offset += source[offset] === "\\" ? 2 : 1;
    }
    offset += 1;
    return JSON.parse(source.slice(start, offset));
  }

  function scanValue() {
    skipWhitespace();
    if (source[offset] === "{") {
      scanObject();
    } else if (source[offset] === "[") {
      scanArray();
    } else if (source[offset] === '"') {
      scanString();
    } else {
      while (offset < source.length && !/[\s,\]}]/.test(source[offset])) offset += 1;
    }
  }

  function scanObject() {
    const keys = new Set();
    offset += 1;
    skipWhitespace();
    if (source[offset] === "}") {
      offset += 1;
      return;
    }

    while (offset < source.length) {
      const key = scanString();
      if (keys.has(key)) throw new PolicyError(`duplicate mapping key: ${key}`);
      keys.add(key);
      skipWhitespace();
      offset += 1;
      scanValue();
      skipWhitespace();
      if (source[offset] === "}") {
        offset += 1;
        return;
      }
      offset += 1;
      skipWhitespace();
    }
  }

  function scanArray() {
    offset += 1;
    skipWhitespace();
    if (source[offset] === "]") {
      offset += 1;
      return;
    }

    while (offset < source.length) {
      scanValue();
      skipWhitespace();
      if (source[offset] === "]") {
        offset += 1;
        return;
      }
      offset += 1;
    }
  }

  scanValue();
}

function validateDocument(document) {
  assertKeys(document, ROOT_KEYS, "policy");
  if ("$schema" in document && document.$schema !== SCHEMA_URI) {
    throw new PolicyError(`$schema must be ${SCHEMA_URI}`);
  }
  assertInteger(document.version, "version", 1, 1);
  if (!("control-plane" in document) && !("target-authority" in document)) {
    throw new PolicyError("policy requires control-plane or target-authority");
  }
  if ("control-plane" in document && !("gh-aw-version" in document)) {
    throw new PolicyError("gh-aw-version is required for a control plane");
  }
  if ("gh-aw-version" in document) {
    assertString(document["gh-aw-version"], "gh-aw-version", GH_AW_VERSION_PATTERN);
  }

  if ("control-plane" in document) validateControlPlane(document["control-plane"]);
  if ("target-authority" in document) validateTargetAuthority(document["target-authority"]);
}

function validateControlPlane(control) {
  assertMapping(control, "control-plane");
  assertKeys(control, CONTROL_KEYS, "control-plane");
  if ("scope" in control) validateScope(control.scope);
  if ("activity" in control) validateActivity(control.activity, control.scope);
  if ("inventory" in control) validateInventory(control.inventory);
  if ("web" in control) validateWeb(control.web);
  if ("defaults" in control) validateDefaults(control.defaults, "control-plane.defaults");
  if ("campaigns" in control) {
    validateOperationPolicies(control.campaigns);
    validateCampaignRepositoryScopes(control);
  }
  if ("workflows" in control) {
    validateOperationPolicies(control.workflows, "workflows");
    validateCampaignRepositoryScopes(control, "workflows");
  }
  if ("operations" in control) {
    validateOperationPolicies(control.operations, "operations");
    validateCampaignRepositoryScopes(control, "operations");
    const bound = new Set(Object.keys(control.workflows ?? {}));
    for (const campaign of Object.values(control.campaigns ?? {})) {
      for (const worker of Object.values(campaign.workers ?? {})) bound.add(worker.workflow);
    }
    for (const name of Object.keys(control.operations)) {
      if (bound.has(name) || Object.hasOwn(control.campaigns ?? {}, name)) {
        throw new PolicyError(`operation workflow identity ${name} conflicts with an existing declaration`);
      }
    }
  }
  if ("publishing" in control) validatePublishing(control.publishing);
  if ("marketplace" in control) validateMarketplace(control.marketplace);
  if ("webhook-gateway" in control) validateWebhookGateway(control["webhook-gateway"], control.scope);
}

function validateWebhookGateway(gateway, scope) {
  const path = "control-plane.webhook-gateway";
  assertMapping(gateway, path);
  assertKeys(gateway, ["version", "enabled", "revision", "receivingAppId", "dispatchAppId", "namespace", "artifacts", "grants", "queue", "workers", "discovery", "projectAssignments", "externalSources"], path);
  assertInteger(gateway.version, `${path}.version`, 1, 1);
  assertBoolean(gateway.enabled, `${path}.enabled`);
  for (const key of ["revision", "receivingAppId", "dispatchAppId"]) assertInteger(gateway[key], `${path}.${key}`, 1);
  if (gateway.receivingAppId === gateway.dispatchAppId) throw new PolicyError(`${path} requires distinct App identities`);
  assertInteger(gateway.workers, `${path}.workers`, 1, 32);
  assertString(gateway.namespace, `${path}.namespace`, /^[A-Za-z0-9._-]{1,128}$/);
  for (const key of ["artifacts", "grants"]) {
    if (!Array.isArray(gateway[key]) || gateway[key].length > 1024) throw new PolicyError(`${path}.${key} must contain at most 1024 entries`);
  }
  for (const artifact of gateway.artifacts) {
    assertMapping(artifact, `${path}.artifacts`);
    assertKeys(artifact, ["path", "sha256", "repositoryId", "commitSha", "compilerId"], `${path}.artifacts`);
    assertString(artifact.path, `${path}.artifacts.path`, SAFE_REGISTRY_PATH_PATTERN);
    if (!artifact.path) throw new PolicyError(`${path}.artifacts.path is required`);
    assertString(artifact.sha256, `${path}.artifacts.sha256`, /^[a-f0-9]{64}$/);
    assertString(artifact.commitSha, `${path}.artifacts.commitSha`, /^[a-f0-9]{40}$/);
    assertString(artifact.compilerId, `${path}.artifacts.compilerId`, /^.{1,128}$/);
    assertInteger(artifact.repositoryId, `${path}.artifacts.repositoryId`, 1);
  }
  const grants = new Set();
  const discoveryRepositories = new Set();
  if ("discovery" in gateway) {
    const d = gateway.discovery;
    assertMapping(d, `${path}.discovery`);
    assertKeys(d, ["repositories", "intervalSeconds", "activateSecurity", "activateCommands", "activateNotifications", "serverUrl"], `${path}.discovery`);
    assertBoolean(d.activateSecurity, `${path}.discovery.activateSecurity`);
    if ("activateCommands" in d) assertBoolean(d.activateCommands, `${path}.discovery.activateCommands`);
    if ("activateNotifications" in d) assertBoolean(d.activateNotifications, `${path}.discovery.activateNotifications`);
    if ("serverUrl" in d) {
      assertString(d.serverUrl, `${path}.discovery.serverUrl`, /^https:\/\/[a-z0-9.-]+(?::[1-9][0-9]*)?(?:\/[a-zA-Z0-9._~%+-]+)*$/);
      const port = /^https:\/\/[^/]+:(\d+)(?:\/|$)/.exec(d.serverUrl)?.[1];
      if (port && (!Number.isSafeInteger(Number(port)) || Number(port) > 65535 || port === "443")) {
        throw new PolicyError(`${path}.discovery.serverUrl requires a valid non-default port`);
      }
      const origin = new URL(d.serverUrl);
      let canonicalPath;
      try {
        canonicalPath = encodeURI(decodeURI(origin.pathname));
      } catch {
        throw new PolicyError(`${path}.discovery.serverUrl has invalid path encoding`);
      }
      if (origin.origin + (origin.pathname === "/" ? "" : origin.pathname) !== d.serverUrl || origin.port === "443") {
        throw new PolicyError(`${path}.discovery.serverUrl requires a canonical HTTPS server URL`);
      }
      if (canonicalPath !== origin.pathname) throw new PolicyError(`${path}.discovery.serverUrl has a noncanonical path`);
    }
    if ("intervalSeconds" in d) assertInteger(d.intervalSeconds, `${path}.discovery.intervalSeconds`, 30, 3600);
    if (!Array.isArray(d.repositories) || d.repositories.length < 1 || d.repositories.length > 64) {
      throw new PolicyError(`${path}.discovery.repositories requires 1 to 64 explicit repositories`);
    }
    const names = new Set();
    const allowed = (scope?.["allowed-repositories"] ?? []).map(value => value.toLowerCase());
    const owners = (scope?.["allowed-owners"] ?? []).map(value => value.toLowerCase());
    for (const repository of d.repositories) {
      assertMapping(repository, `${path}.discovery.repositories`);
      assertKeys(repository, ["repository", "repositoryId", "receivingInstallationId", "dispatchInstallationId", "allowLabelRemoval"], `${path}.discovery.repositories`);
      if ("allowLabelRemoval" in repository) {
        assertBoolean(repository.allowLabelRemoval, `${path}.discovery.repositories.allowLabelRemoval`);
        if (repository.allowLabelRemoval && !d.activateCommands) throw new PolicyError(`${path}.discovery label removal requires explicit command activation`);
      }
      assertString(repository.repository, `${path}.discovery.repositories.repository`, REPOSITORY_PATTERN);
      if ([".", ".."].includes(repository.repository.split("/")[1])) {
        throw new PolicyError(`${path}.discovery repository name is invalid`);
      }
      for (const key of ["repositoryId", "receivingInstallationId", "dispatchInstallationId"]) {
        assertInteger(repository[key], `${path}.discovery.repositories.${key}`, 1);
      }
      const name = repository.repository.toLowerCase();
      if (names.has(name) || discoveryRepositories.has(repository.repositoryId)) {
        throw new PolicyError(`${path}.discovery.repositories contains duplicate identity`);
      }
      if ((!allowed.length && !owners.length) || (allowed.length && !allowed.includes(name)) ||
          (owners.length && !owners.includes(name.split("/")[0]))) {
        throw new PolicyError(`${path}.discovery repository is outside control-plane.scope`);
      }
      names.add(name);
      discoveryRepositories.add(repository.repositoryId);
    }
  }
  if ("externalSources" in gateway) validateGatewayExternalSources(gateway.externalSources, discoveryRepositories, `${path}.externalSources`);
  if ("projectAssignments" in gateway) {
    if (!Array.isArray(gateway.projectAssignments) || gateway.projectAssignments.length > 128) {
      throw new PolicyError(`${path}.projectAssignments must contain at most 128 entries`);
    }
    const projects = new Set();
    for (const assignment of gateway.projectAssignments) {
      assertMapping(assignment, `${path}.projectAssignments`);
      assertKeys(assignment, ["projectId", "organizationId", "repositoryId"], `${path}.projectAssignments`);
      assertString(assignment.projectId, `${path}.projectAssignments.projectId`, /^[A-Za-z0-9_+/=-]{1,256}$/);
      for (const key of ["organizationId", "repositoryId"]) assertInteger(assignment[key], `${path}.projectAssignments.${key}`, 1);
      if (projects.has(assignment.projectId) || !discoveryRepositories.has(assignment.repositoryId)) {
        throw new PolicyError(`${path}.projectAssignments requires a unique project and an enrolled repository`);
      }
      projects.add(assignment.projectId);
    }
  }
  for (const grant of gateway.grants) {
    assertMapping(grant, `${path}.grants`);
    assertKeys(grant, ["subscriptionId", "digest", "sourceInstallationId", "sourceRepositoryId", "sourceOrganizationId", "projectIds",
      "receivingInstallationId", "destinationInstallationId", "destinationRepositoryId", "dispatchKind", "allowVisibilityTransfer", "expiresAt", "revoked"], `${path}.grants`);
    assertString(grant.subscriptionId, `${path}.grants.subscriptionId`, /^[A-Za-z0-9._-]{1,200}$/);
    if (grants.has(grant.subscriptionId)) throw new PolicyError(`${path}.grants contains duplicate approval`);
    grants.add(grant.subscriptionId);
    assertString(grant.digest, `${path}.grants.digest`, /^[a-f0-9]{64}$/);
    for (const key of ["sourceInstallationId", "receivingInstallationId", "destinationInstallationId", "destinationRepositoryId"]) {
      assertInteger(grant[key], `${path}.grants.${key}`, 1);
    }
    for (const key of ["sourceRepositoryId", "sourceOrganizationId"]) assertInteger(grant[key], `${path}.grants.${key}`, 0);
    if (!grant.sourceRepositoryId && !grant.sourceOrganizationId) throw new PolicyError(`${path}.grants requires an explicit source`);
    if (grant.sourceInstallationId !== grant.receivingInstallationId) throw new PolicyError(`${path}.grants receiving installation mismatch`);
    if ("projectIds" in grant) assertUniqueStrings(grant.projectIds, `${path}.grants.projectIds`, /^[A-Za-z0-9_+/=-]{1,256}$/);
    assertOneOf(grant.dispatchKind, `${path}.grants.dispatchKind`, ["workflow_dispatch", "repository_dispatch"]);
    for (const key of ["allowVisibilityTransfer", "revoked"]) if (key in grant) assertBoolean(grant[key], `${path}.grants.${key}`);
    assertString(grant.expiresAt, `${path}.grants.expiresAt`, /^\d{4}-\d{2}-\d{2}T/);
    if (!Number.isFinite(Date.parse(grant.expiresAt))) throw new PolicyError(`${path}.grants.expiresAt must be a timestamp`);
  }
  if ("queue" in gateway) {
    assertMapping(gateway.queue, `${path}.queue`);
    assertKeys(gateway.queue, ["maxRows", "maxBytes", "maxTenantRows", "maxTenantBytes", "maxSourceRows", "maxSourceBytes",
      "maxDestinationRows", "maxDestinationBytes", "maxPayloadBytes", "maxBatch", "maxConcurrency", "maxAge",
      "dedupWindow", "terminalRetention", "leaseDuration", "rates"], `${path}.queue`);
    for (const [key, value] of Object.entries(gateway.queue)) {
      if (key !== "rates") assertInteger(value, `${path}.queue.${key}`, 1);
    }
    if ("rates" in gateway.queue) {
      assertMapping(gateway.queue.rates, `${path}.queue.rates`);
      assertKeys(gateway.queue.rates, ["global", "app", "tenant", "destination", "workflow"], `${path}.queue.rates`);
      for (const rate of Object.values(gateway.queue.rates)) {
        assertMapping(rate, `${path}.queue.rates`);
        assertKeys(rate, ["capacity", "period"], `${path}.queue.rates`);
        assertInteger(rate.capacity, `${path}.queue.rates.capacity`, 1);
        assertInteger(rate.period, `${path}.queue.rates.period`, 1);
      }
    }
  }
}

function validateGatewayExternalSources(sources, repositories, path) {
  if (!Array.isArray(sources) || sources.length > 64) throw new PolicyError(`${path} must contain at most 64 sources`);
  const ids = new Set(), secrets = new Set();
  for (const source of sources) {
    assertMapping(source, path);
    assertKeys(source, ["id", "event", "resourceUrlPrefix", "scope", "collectionId", "secretEnv", "destinationRepositoryIds", "expiresAt"], path);
    assertString(source.id, `${path}.id`, /^[A-Za-z0-9._-]{1,64}$/);
    assertOneOf(source.event, `${path}.event`, ["jira_issue", "linear_issue", "azure_devops_issue"]);
    assertString(source.secretEnv, `${path}.secretEnv`, /^CAO_GATEWAY_SOURCE_[A-Z0-9_]+$/);
    if (["compiler", ".", ".."].includes(source.id) || ids.has(source.id) || secrets.has(source.secretEnv) ||
        source.secretEnv.length > 128 || source.secretEnv.endsWith("_PREVIOUS")) {
      throw new PolicyError(`${path} requires distinct source and receive-only secret identities`);
    }
    ids.add(source.id);
    secrets.add(source.secretEnv);
    assertString(source.expiresAt, `${path}.expiresAt`, /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/);
    if (!Number.isFinite(Date.parse(source.expiresAt))) throw new PolicyError(`${path}.expiresAt must be a timestamp`);
    if (!Array.isArray(source.destinationRepositoryIds) || source.destinationRepositoryIds.length < 1 ||
        source.destinationRepositoryIds.length > 64 || new Set(source.destinationRepositoryIds).size !== source.destinationRepositoryIds.length ||
        source.destinationRepositoryIds.some(id => !Number.isSafeInteger(id) || !repositories.has(id))) {
      throw new PolicyError(`${path} requires explicitly enrolled destination repositories`);
    }
    assertString(source.resourceUrlPrefix, `${path}.resourceUrlPrefix`, /^https:\/\/.{1,2040}\/$/);
    let prefix;
    try { prefix = new URL(source.resourceUrlPrefix); } catch { throw new PolicyError(`${path} requires a canonical HTTPS resource prefix`); }
    if (source.resourceUrlPrefix.length > 2048 || prefix.href !== source.resourceUrlPrefix || prefix.username || prefix.password || prefix.search || prefix.hash ||
        /[?#\\\s]/.test(source.resourceUrlPrefix)) throw new PolicyError(`${path} requires a canonical HTTPS resource prefix`);
    assertString(source.scope, `${path}.scope`, /^.{1,256}$/);
    if (!source.scope.trim()) throw new PolicyError(`${path} requires explicit provider scope`);
    if (source.event === "azure_devops_issue") {
      assertString(source.collectionId, `${path}.collectionId`, /^.{1,256}$/);
      if (!source.collectionId.trim() || !(prefix.hostname === "dev.azure.com" || /^[A-Za-z0-9-]+\.visualstudio\.com$/.test(prefix.hostname)) ||
          !/^\/[A-Za-z0-9_-]+(?:\/(?:[A-Za-z0-9_.-]|%[0-9A-Fa-f]{2})+)?\/_apis\/wit\/work[Ii]tems\/$/.test(prefix.pathname)) {
        throw new PolicyError(`${path} requires an enrolled Azure collection work item prefix`);
      }
    } else if ("collectionId" in source) {
      throw new PolicyError(`${path}.collectionId is Azure-only`);
    }
    if (source.event === "jira_issue" && !/^\/rest\/api\/[23]\/issue\/$/.test(prefix.pathname)) throw new PolicyError(`${path} requires a Jira REST issue prefix`);
    if (source.event === "linear_issue" && (prefix.origin !== "https://linear.app" || !/^\/[A-Za-z0-9_-]+\/issue\/$/.test(prefix.pathname) || prefix.pathname.startsWith("/issue/"))) {
      throw new PolicyError(`${path} requires a workspace-qualified Linear issue prefix`);
    }
  }
}

function validateMarketplace(marketplace) {
  const path = "control-plane.marketplace";
  assertMapping(marketplace, path);
  assertKeys(marketplace, MARKETPLACE_KEYS, path);
  if ("cache-ttl-seconds" in marketplace) {
    assertInteger(marketplace["cache-ttl-seconds"], `${path}.cache-ttl-seconds`, 30, 86_400);
  }
  if (!Array.isArray(marketplace.registries)) throw new PolicyError(`${path}.registries must be a sequence`);
  if (marketplace.registries.length > 32) throw new PolicyError(`${path}.registries must contain at most 32 entries`);
  const ids = new Set();
  marketplace.registries.forEach((registry, index) => {
    const registryPath = `${path}.registries[${index}]`;
    assertMapping(registry, registryPath);
    assertKeys(registry, REGISTRY_KEYS, registryPath);
    assertString(registry.id, `${registryPath}.id`, SLUG_PATTERN);
    if (ids.has(registry.id)) throw new PolicyError(`${path}.registries contains duplicate id: ${registry.id}`);
    ids.add(registry.id);
    if ("name" in registry) assertString(registry.name, `${registryPath}.name`, REGISTRY_NAME_PATTERN);
    assertString(registry.repository, `${registryPath}.repository`, REPOSITORY_PATTERN);
    assertString(registry.ref, `${registryPath}.ref`, REGISTRY_REF_PATTERN);
    if ("format" in registry && !["packages", "workflows"].includes(registry.format)) {
      throw new PolicyError(`${registryPath}.format must be packages or workflows`);
    }
    if ("path" in registry) assertString(registry.path, `${registryPath}.path`, SAFE_REGISTRY_PATH_PATTERN);
    if ("api-url" in registry) {
      assertString(registry["api-url"], `${registryPath}.api-url`, HTTPS_URL_PATTERN);
      let apiURL;
      try {
        apiURL = new URL(registry["api-url"]);
      } catch {
        throw new PolicyError(`${registryPath}.api-url must be a valid HTTPS URL`);
      }
      if (apiURL.protocol !== "https:" || apiURL.username || apiURL.password) {
        throw new PolicyError(`${registryPath}.api-url must be a credential-free HTTPS URL`);
      }
    }
    const auth = registry.auth ?? { type: "none" };
    assertMapping(auth, `${registryPath}.auth`);
    const keys = REGISTRY_AUTH_KEYS[auth.type];
    if (!keys) throw new PolicyError(`${registryPath}.auth.type must be none, pat, or github-app`);
    assertKeys(auth, keys, `${registryPath}.auth`);
    for (const key of keys.filter((key) => key !== "type")) {
      assertString(auth[key], `${registryPath}.auth.${key}`, SECRET_REFERENCE_PATTERN);
    }
  });
}

function validateScope(scope) {
  assertMapping(scope, "control-plane.scope");
  assertKeys(scope, SCOPE_KEYS, "control-plane.scope");
  if ("allowed-owners" in scope) {
    assertUniqueStrings(scope["allowed-owners"], "control-plane.scope.allowed-owners", OWNER_PATTERN);
  }
  if ("allowed-repositories" in scope) {
    assertUniqueStrings(scope["allowed-repositories"], "control-plane.scope.allowed-repositories", REPOSITORY_PATTERN);
  }
}

function validateActivity(activity, scope = {}) {
  const path = "control-plane.activity";
  assertMapping(activity, path);
  assertKeys(activity, ACTIVITY_KEYS, path);
  if ("public-only" in activity) assertBoolean(activity["public-only"], `${path}.public-only`);
  if (!("repositories" in activity)) throw new PolicyError(`${path}.repositories is required`);
  assertUniqueStrings(activity.repositories, `${path}.repositories`, REPOSITORY_PATTERN);
  if (activity.repositories.length === 0) {
    throw new PolicyError(`${path}.repositories must contain at least one repository`);
  }
  const allowedRepositories = new Set(
    (scope?.["allowed-repositories"] ?? []).map((repository) => repository.toLowerCase()),
  );
  if (allowedRepositories.size > 0) {
    const outsideScope = activity.repositories.find(
      (repository) => !allowedRepositories.has(repository.toLowerCase()),
    );
    if (outsideScope) {
      throw new PolicyError(`${path} repository ${outsideScope} is outside control-plane.scope.allowed-repositories`);
    }
  }
  const allowedOwners = new Set(
    (scope?.["allowed-owners"] ?? []).map((owner) => owner.toLowerCase()),
  );
  if (allowedOwners.size > 0) {
    const outsideOwner = activity.repositories.find(
      (repository) => !allowedOwners.has(repository.toLowerCase().split("/", 1)[0]),
    );
    if (outsideOwner) {
      throw new PolicyError(`${path} repository ${outsideOwner} is outside control-plane.scope.allowed-owners`);
    }
  }
}

function validateInventory(inventory) {
  assertMapping(inventory, "control-plane.inventory");
  assertKeys(inventory, INVENTORY_KEYS, "control-plane.inventory");
  if ("max-scan-repositories" in inventory) {
    assertInteger(inventory["max-scan-repositories"], "control-plane.inventory.max-scan-repositories", 1, 100_000);
  }
  if ("cell-count" in inventory) assertInteger(inventory["cell-count"], "control-plane.inventory.cell-count", 1, 1000);
  if ("cell-index" in inventory) assertInteger(inventory["cell-index"], "control-plane.inventory.cell-index", 0);
  if ("batch-size" in inventory) assertInteger(inventory["batch-size"], "control-plane.inventory.batch-size", 1, 100_000);
  if ("batch-index" in inventory) assertInteger(inventory["batch-index"], "control-plane.inventory.batch-index", 0);

  const cellCount = inventory["cell-count"] ?? 1;
  const cellIndex = inventory["cell-index"] ?? 0;
  if (cellIndex >= cellCount) {
    throw new PolicyError("control-plane.inventory.cell-index must be smaller than cell-count");
  }
}

function validateWeb(web) {
  const path = "control-plane.web";
  assertMapping(web, path);
  assertKeys(web, WEB_KEYS, path);
  if ("experimental" in web) assertBoolean(web.experimental, `${path}.experimental`);
  if ("favicon" in web) assertFavicon(web.favicon, `${path}.favicon`);
  if ("host" in web) validateHost(web.host, `${path}.host`);
}

function validateHost(host, path) {
  assertMapping(host, path);
  assertKeys(host, HOST_KEYS, path);
  if (!("target" in host)) throw new PolicyError(`${path}.target is required`);
  validateTarget(host.target, `${path}.target`);
  if ("cors" in host) validateCORS(host.cors, `${path}.cors`);
  const targetSupportsSingleReplica =
    host.target.module === "container" ||
    (host.target.module === "generic" && host.target["supports-single-replica"] === true);
  let redis = host.redis;
  if ("operational-store" in host) {
    const operational = host["operational-store"];
    const operationalPath = `${path}.operational-store`;
    assertMapping(operational, operationalPath);
    assertKeys(operational, OPERATIONAL_STORE_KEYS, operationalPath);
    assertOneOf(operational.backend, `${operationalPath}.backend`, ["redis", "memory", "postgres"]);
    if ("redis" in host) {
      throw new PolicyError(`${path}.redis and operational-store cannot both select an operational backend`);
    }
    for (const key of ["single-process", "allow-volatile"]) {
      if (key in operational) assertBoolean(operational[key], `${operationalPath}.${key}`);
    }
    if ("namespace-env" in operational) {
      assertString(operational["namespace-env"], `${operationalPath}.namespace-env`, SECRET_REFERENCE_PATTERN);
    }
    if ("postgres" in operational) {
      if (operational.backend !== "postgres") {
        throw new PolicyError(`${operationalPath}.postgres requires the postgres backend`);
      }
      assertMapping(operational.postgres, `${operationalPath}.postgres`);
      assertKeys(operational.postgres, ["url-env"], `${operationalPath}.postgres`);
      if ("url-env" in operational.postgres) {
        assertString(operational.postgres["url-env"], `${operationalPath}.postgres.url-env`, /^[A-Z][A-Z0-9_]{0,127}$/);
      }
    }
    if (operational.backend === "memory") {
      if ("redis" in operational) throw new PolicyError(`${operationalPath} memory cannot configure Redis`);
      if (operational["single-process"] !== true || operational["allow-volatile"] !== true) {
        throw new PolicyError(`${operationalPath} memory requires single-process and allow-volatile acknowledgements`);
      }
      if (!targetSupportsSingleReplica || host.target.replicas !== 1 ||
          host.target.module === "azure-functions" ||
          (host.target.module === "generic" && host.target.listener !== "process")) {
        throw new PolicyError(`${operationalPath} memory requires a single-process, single-replica process-listener target`);
      }
      return;
    }
    if (operational["single-process"] === true || operational["allow-volatile"] === true) {
      throw new PolicyError(`${operationalPath} ${operational.backend} does not accept memory topology or volatile-state options`);
    }
    if (operational.backend === "postgres") {
      if ("redis" in operational) throw new PolicyError(`${operationalPath} postgres cannot configure Redis`);
      return;
    }
    redis = "redis" in operational ? operational.redis : { module: "generic" };
  }
  if (!redis && !("redis" in host) && !("operational-store" in host)) return;
  validateRedis(redis, `${path}.redis`);
  if (redis.module === "generic" &&
      redis["single-replica"] === true && redis.session !== "serialized") {
    throw new PolicyError(`${path}.redis.single-replica requires a serialized Redis session`);
  }
  if (redis.module === "generic" &&
      redis["isolate-process-namespace"] === true && redis.session !== "serialized") {
    throw new PolicyError(`${path}.redis.isolate-process-namespace requires a serialized session`);
  }
  const redisRequiresSingleReplica =
    redis.module === "upstash" ||
    (redis.module === "generic" && redis["single-replica"] === true);
  if (redisRequiresSingleReplica && !targetSupportsSingleReplica) {
    throw new PolicyError(`${path} target cannot guarantee the Redis module's single-replica requirement`);
  }
  if (redisRequiresSingleReplica && host.target.replicas !== 1) {
    throw new PolicyError(`${path}.target.replicas must be 1 for the Redis module`);
  }
}

function validateCORS(cors, path) {
  assertMapping(cors, path);
  assertKeys(cors, CORS_KEYS, path);
  if ("allowed-origins" in cors) {
    assertUniqueStrings(cors["allowed-origins"], `${path}.allowed-origins`, CORS_ORIGIN_PATTERN);
    for (const origin of cors["allowed-origins"]) {
      const port = /:([0-9]+)\/?$/.exec(origin.replace(/^[a-z]+:\/\/\[[^\]]*\]/, ""))?.[1];
      if (port !== undefined && (Number(port) < 1 || Number(port) > 65535)) {
        throw new PolicyError(`${path}.allowed-origins has an invalid port`);
      }
    }
    if (cors["allowed-origins"].length > 32) {
      throw new PolicyError(`${path}.allowed-origins accepts at most 32 origins`);
    }
  }
  if ("max-age" in cors) assertInteger(cors["max-age"], `${path}.max-age`, 1, 86400);
}

function validateTarget(target, path) {
  assertMapping(target, path);
  assertKeys(target, TARGET_KEYS, path);
  if (!("module" in target)) throw new PolicyError(`${path}.module is required`);
  assertOneOf(target.module, `${path}.module`, TARGET_MODULES);
  if ("name" in target) assertString(target.name, `${path}.name`, SLUG_PATTERN);
  if ("replicas" in target) assertInteger(target.replicas, `${path}.replicas`, 1);
  if (target.module === "generic") {
    for (const key of ["authentication", "listener"]) {
      if (!(key in target)) throw new PolicyError(`${path}.${key} is required for the generic module`);
    }
    assertOneOf(target.authentication, `${path}.authentication`, ["github-oauth"]);
    assertOneOf(target.listener, `${path}.listener`, ["process", "platform", "external"]);
    if (target.authentication !== "github-oauth") {
      throw new PolicyError(`${path}.authentication must be github-oauth for a configured host target`);
    }
    for (const key of ["trust-platform-proxy", "supports-single-replica"]) {
      if (key in target) assertBoolean(target[key], `${path}.${key}`);
    }
    if ("require-https" in target && target["require-https"] !== true) {
      throw new PolicyError(`${path}.require-https must be true for a configured host target`);
    }
    if (target.listener === "platform" && target.authentication !== "github-oauth") {
      throw new PolicyError(`${path} platform listeners require github-oauth authentication`);
    }
    if (target["trust-platform-proxy"] === true && target.listener !== "platform") {
      throw new PolicyError(`${path}.trust-platform-proxy requires a platform listener`);
    }
  } else {
    for (const key of [
      "authentication", "listener", "require-https", "trust-platform-proxy",
      "supports-single-replica",
    ]) {
      if (key in target) throw new PolicyError(`${path}.${key} is fixed by module ${target.module}`);
    }
  }
}

function validateRedis(redis, path) {
  assertMapping(redis, path);
  assertKeys(redis, REDIS_KEYS, path);
  if (!("module" in redis)) throw new PolicyError(`${path}.module is required`);
  assertOneOf(redis.module, `${path}.module`, REDIS_MODULES);
  for (const key of ["url-env", "host-env", "port-env", "username-env", "password-env", "namespace-env"]) {
    if (key in redis) assertString(redis[key], `${path}.${key}`, SECRET_REFERENCE_PATTERN);
  }
  if ("session" in redis) assertOneOf(redis.session, `${path}.session`, ["pooled", "serialized"]);
  for (const key of ["isolate-process-namespace", "single-replica", "supports-collection", "allow-private-plaintext"]) {
    if (key in redis) assertBoolean(redis[key], `${path}.${key}`);
  }
  if (redis.module !== "generic") {
    for (const key of ["session", "isolate-process-namespace", "single-replica", "supports-collection"]) {
      if (key in redis) throw new PolicyError(`${path}.${key} is fixed by module ${redis.module}`);
    }
  }
  if ("tls" in redis) {
    assertMapping(redis.tls, `${path}.tls`);
    assertKeys(redis.tls, REDIS_TLS_KEYS, `${path}.tls`);
    if ("mode" in redis.tls) assertOneOf(redis.tls.mode, `${path}.tls.mode`, ["auto", "required", "disabled"]);
    for (const key of ["server-name-env", "ca-certificate-env"]) {
      if (key in redis.tls) assertString(redis.tls[key], `${path}.tls.${key}`, SECRET_REFERENCE_PATTERN);
    }
  }
}

function validateDefaults(defaults, path) {
  assertMapping(defaults, path);
  assertKeys(defaults, DEFAULT_KEYS, path);
  if ("mode" in defaults) assertMode(defaults.mode, `${path}.mode`);
  if ("max-repositories" in defaults) assertInteger(defaults["max-repositories"], `${path}.max-repositories`, 1, 1000);
  if ("rollout-percent" in defaults) assertInteger(defaults["rollout-percent"], `${path}.rollout-percent`, 1, 100);
  if ("monthly-ai-credit-budget" in defaults) {
    assertInteger(defaults["monthly-ai-credit-budget"], `${path}.monthly-ai-credit-budget`, 0);
  }
}

function validateOperationPolicies(campaigns, collection = "campaigns") {
  assertMapping(campaigns, `control-plane.${collection}`);
  for (const [campaignName, campaignPolicy] of Object.entries(campaigns)) {
    const path = `control-plane.${collection}.${campaignName}`;
    assertString(campaignName, path, SLUG_PATTERN);
    assertMapping(campaignPolicy, path);
    assertKeys(campaignPolicy, collection === "operations" ? OPERATION_KEYS : collection === "workflows" ? WORKFLOW_KEYS : CAMPAIGN_KEYS, path);
    if ("workflow-values" in campaignPolicy) {
      if (collection !== "campaigns" || campaignName !== "activity" || !Array.isArray(campaignPolicy["workflow-values"])
        || campaignPolicy["workflow-values"].length > 1000) {
        throw new PolicyError(`${path}.workflow-values is only supported for Activity`);
      }
      const identities = new Set();
      for (const entry of campaignPolicy["workflow-values"]) {
        assertMapping(entry, `${path}.workflow-values`);
        assertKeys(entry, ["repository", "workflow", "collector", "evidence", "api-requests", "enabled"], `${path}.workflow-values`);
        assertString(entry.repository, `${path}.repository`, REPOSITORY_PATTERN);
        assertString(entry.workflow, `${path}.workflow`, SLUG_PATTERN);
        if ("enabled" in entry) assertBoolean(entry.enabled, `${path}.enabled`);
        if ("api-requests" in entry) {
          assertInteger(entry["api-requests"], `${path}.api-requests`, 1, 1000);
          if (!("collector" in entry)) throw new PolicyError(`${path}.api-requests requires a collector`);
        }
        if ("evidence" in entry) {
          assertMapping(entry.evidence, `${path}.evidence`);
          assertKeys(entry.evidence, ["path", "sha256"], `${path}.evidence`);
          assertString(entry.evidence.path, `${path}.evidence.path`,
            /^\.github\/value-evidence\/(?!.*(?:^|\/)\.\.?\/)[A-Za-z0-9._/-]+\.jsonl(?:\.gz)?$/);
          if (entry.evidence.path.split("/").some((part) => !part || part === "." || part === "..")) {
            throw new PolicyError(`${path}.evidence.path must be a confined reviewed evidence path`);
          }
          assertString(entry.evidence.sha256, `${path}.evidence.sha256`, /^[a-f0-9]{64}$/);
        }
        if (entry.enabled !== false || "collector" in entry) {
          assertString(entry.collector, `${path}.collector`, /^(?!\/)(?!.*(?:^|\/)\.\.?\/)[A-Za-z0-9._/-]+\.mjs$/);
          if (entry.collector.split("/").some((part) => !part || part === "." || part === "..")) {
            throw new PolicyError(`${path}.collector must be a confined repository-relative .mjs path`);
          }
        }
        const identity = `${entry.repository.toLowerCase()}:${entry.workflow}`;
        if (identities.has(identity)) throw new PolicyError(`${path}.workflow-values has duplicate identities`);
        identities.add(identity);
      }
    }
    if ("enabled" in campaignPolicy) assertBoolean(campaignPolicy.enabled, `${path}.enabled`);
    if ("icon" in campaignPolicy) assertOcticon(campaignPolicy.icon, `${path}.icon`);
    if ("deploy" in campaignPolicy) assertBoolean(campaignPolicy.deploy, `${path}.deploy`);
    if ("max-mode" in campaignPolicy) assertMode(campaignPolicy["max-mode"], `${path}.max-mode`);
    validateDefaults(pick(campaignPolicy, DEFAULT_KEYS), path);
    if ("targets" in campaignPolicy) {
      const targets = campaignPolicy.targets;
      assertMapping(targets, `${path}.targets`);
      assertUniqueRepositoryKeys(targets, `${path}.targets`);
      for (const [repository, targetPolicy] of Object.entries(targets)) {
        const targetPath = `${path}.targets.${repository}`;
        assertString(repository, targetPath, REPOSITORY_PATTERN);
        assertMapping(targetPolicy, targetPath);
        assertKeys(targetPolicy, TARGET_POLICY_KEYS, targetPath);
        if (!("mode" in targetPolicy)) throw new PolicyError(`${targetPath}.mode is required`);
        assertMode(targetPolicy.mode, `${targetPath}.mode`);
      }
    }
    if (!("workers" in campaignPolicy)) continue;

    const workers = campaignPolicy.workers;
    assertMapping(workers, `${path}.workers`);
    const workflows = new Set();
    for (const [workerName, worker] of Object.entries(workers)) {
      const workerPath = `${path}.workers.${workerName}`;
      assertString(workerName, workerPath, SLUG_PATTERN);
      assertMapping(worker, workerPath);
      assertKeys(worker, WORKER_KEYS, workerPath);
      assertString(worker.workflow, `${workerPath}.workflow`, SLUG_PATTERN);
      if (workflows.has(worker.workflow)) {
        throw new PolicyError(`${path}.workers must declare unique workflow identities`);
      }
      workflows.add(worker.workflow);
      if ("enabled" in worker) assertBoolean(worker.enabled, `${workerPath}.enabled`);
      if ("max-mode" in worker) assertMode(worker["max-mode"], `${workerPath}.max-mode`);
    }
  }
}

function validateCampaignRepositoryScopes(control, collection = "campaigns") {
  const allowedRepositories = control.scope?.["allowed-repositories"];
  const allowedOwners = control.scope?.["allowed-owners"];
  const repositorySet = allowedRepositories && new Set(allowedRepositories.map((repository) => repository.toLowerCase()));
  const ownerSet = allowedOwners && new Set(allowedOwners.map((owner) => owner.toLowerCase()));

  for (const campaignPolicy of Object.values(control[collection] ?? {})) {
    for (const repository of Object.keys(campaignPolicy.targets ?? {})) {
      const normalized = repository.toLowerCase();
      if (repositorySet && !repositorySet.has(normalized)) {
        throw new PolicyError(`${collection === "campaigns" ? "campaign" : "workflow"} target ${repository} is outside control-plane.scope.allowed-repositories`);
      }
      if (ownerSet && !ownerSet.has(normalized.split("/", 1)[0])) {
        throw new PolicyError(`${collection === "campaigns" ? "campaign" : "workflow"} target ${repository} is outside control-plane.scope.allowed-owners`);
      }
    }
  }
}

function validatePublishing(publishing) {
  const path = "control-plane.publishing";
  assertMapping(publishing, path);
  assertKeys(publishing, PUBLISHING_KEYS, path);
  if ("enabled" in publishing) assertBoolean(publishing.enabled, `${path}.enabled`);
  if ("control-repositories" in publishing) {
    assertUniqueStrings(publishing["control-repositories"], `${path}.control-repositories`, REPOSITORY_PATTERN);
  }
  if ("reviewers" in publishing) assertUniqueStrings(publishing.reviewers, `${path}.reviewers`, LOGIN_PATTERN);
  if ((publishing.enabled ?? false) && (publishing.reviewers ?? []).length === 0) {
    throw new PolicyError("control-plane.publishing.reviewers is required when publishing is enabled");
  }
}

function validateTargetAuthority(target) {
  const path = "target-authority";
  assertMapping(target, path);
  assertKeys(target, TARGET_AUTHORITY_KEYS, path);
  const campaigns = target.campaigns;
  assertMapping(campaigns, `${path}.campaigns`);
  for (const [campaignName, campaignPolicy] of Object.entries(campaigns)) {
    const campaignPath = `${path}.campaigns.${campaignName}`;
    assertString(campaignName, campaignPath, SLUG_PATTERN);
    assertMapping(campaignPolicy, campaignPath);
    assertKeys(campaignPolicy, TARGET_CAMPAIGN_KEYS, campaignPath);
    assertString(campaignPolicy.authority, `${campaignPath}.authority`, REPOSITORY_PATTERN);
  }
}

function rejectExpressions(value, path = "policy") {
  if (Array.isArray(value)) {
    value.forEach((child, index) => rejectExpressions(child, `${path}[${index}]`));
  } else if (isRecord(value)) {
    Object.entries(value).forEach(([key, child]) => rejectExpressions(child, `${path}.${key}`));
  } else if (typeof value === "string" && value.includes("${{")) {
    throw new PolicyError(`${path} must not contain a GitHub Actions expression`);
  }
}

export function effectivePolicy(
  document,
  {
    campaignName = "",
    workflowName = "",
    role,
    workerName = "",
    controlRepository,
    requestedMode = "",
    requestedMaxRepositories = "",
    requestedRolloutPercent = "",
    targetRepository = "",
    eventName = "",
  },
) {
  log("Resolving effective policy.");
  if (!["orchestrator", "worker", "standalone"].includes(role)) throw new PolicyError("role must be orchestrator, worker, or standalone");
  const standalone = role === "standalone";
  if (standalone) {
    assertString(workflowName, "workflow identity", SLUG_PATTERN);
    if (campaignName || workerName) throw new PolicyError("standalone workflows must not specify a campaign or worker identity");
  } else if (workflowName) {
    throw new PolicyError("standalone workflow identity is forbidden for campaign roles");
  }
  if (role === "worker" && !workerName) throw new PolicyError("worker identity is required");
  if (role === "orchestrator" && workerName) throw new PolicyError("worker identity is forbidden for orchestrators");

  const deny = (reason) => ({
    ...denied(reason, campaignName, role, workerName),
    ...(standalone ? { workflow: workflowName } : {}),
  });
  const control = document["control-plane"];
  if (!control) return deny("control-plane-absent");

  const campaignPolicy = standalone
    ? (Object.hasOwn(control.workflows ?? {}, workflowName) ? control.workflows[workflowName] : undefined)
    : (control.campaigns ?? {})[campaignName];
  const kind = standalone ? "workflow" : "campaign";
  if (!campaignPolicy) return deny(`${kind}-undeclared`);
  if (!(campaignPolicy.enabled ?? true)) return deny(`${kind}-disabled`);
  const workers = campaignPolicy.workers ?? {};
  if (role === "worker" && !(workerName in workers)) {
    throw new PolicyError(`unknown worker: ${campaignName}/${workerName}`);
  }

  const defaults = {
    mode: "review",
    "max-repositories": 1,
    "rollout-percent": 100,
    "monthly-ai-credit-budget": 0,
    ...(control.defaults ?? {}),
  };
  const effective = { ...defaults, ...pick(campaignPolicy, DEFAULT_KEYS) };
  const workerPolicies = Object.fromEntries(
    Object.entries(workers).map(([worker, policy]) => {
      return [policy.workflow, {
        worker,
        enabled: policy.enabled ?? true,
        max_mode: policy["max-mode"] ?? null,
      }];
    }),
  );
  if (targetRepository) {
    if (typeof targetRepository !== "string" || !REPOSITORY_PATTERN.test(targetRepository)) {
      throw new PolicyError("target_repo must use owner/repository form");
    }
  }

  if (role === "worker") {
    const worker = workers[workerName];
    if (!(worker.enabled ?? true)) return denied("worker-disabled", campaignName, role, workerName);
  }
  const authority = resolveAuthority({
    mode: effective.mode,
    maxMode: role === "worker" ? workers[workerName]["max-mode"] : standalone ? campaignPolicy["max-mode"] : undefined,
    maxTargets: effective["max-repositories"],
    targets: campaignPolicy.targets,
  }, {
    target: targetRepository,
    mode: requestedMode === "debug" ? "" : requestedMode,
    maxTargets: requestedMaxRepositories,
  });
  effective.mode = authority.mode;
  effective["max-repositories"] = authority.maxTargets;
  let targetPolicies = authority.targets;

  if (requestedMode === "debug") {
    if (role !== "worker" || eventName !== "workflow_dispatch") {
      throw new PolicyError("debug requires a manual workflow_dispatch worker run");
    }
    if (!targetRepository) throw new PolicyError("debug worker target_repo is required");
    effective.mode = "debug";
    targetPolicies = {};
  }
  effective["rollout-percent"] = narrowLimit(effective["rollout-percent"], requestedRolloutPercent, "rollout_percent", 100);

  const scope = control.scope ?? {};
  const allowedOwners = scope["allowed-owners"] ?? [controlRepository.split("/", 1)[0]];
  const inventory = {
    "max-scan-repositories": 1000,
    "cell-count": 1,
    "cell-index": 0,
    "batch-size": 100_000,
    "batch-index": 0,
    ...(control.inventory ?? {}),
  };

  return {
    authorized: true,
    reason: "authorized",
    control_role: role,
    campaign: campaignName,
    ...(standalone ? { workflow: workflowName } : {}),
    worker: workerName,
    enabled: true,
    safe_output_mode: effective.mode,
    max_repositories: effective["max-repositories"],
    rollout_percent: effective["rollout-percent"],
    monthly_ai_credit_budget: 0,
    target_policies: targetPolicies,
    worker_policies: workerPolicies,
    allowed_owners: allowedOwners,
    allowed_repositories: scope["allowed-repositories"] ?? [],
    inventory,
  };
}

export function controlSettings(document, controlRepository) {
  assertString(controlRepository, "GITHUB_REPOSITORY", REPOSITORY_PATTERN);
  const control = document["control-plane"];
  if (!control) throw new PolicyError("control-plane is required");

  const scope = control.scope ?? {};
  const web = control.web ?? {};
  const publishing = control.publishing ?? {};
  const defaults = {
    mode: "review",
    "max-repositories": 1,
    "rollout-percent": 100,
    "monthly-ai-credit-budget": 0,
    ...(control.defaults ?? {}),
  };
  const campaigns = Object.fromEntries(Object.entries(control.campaigns ?? {}).map(([name, policy]) => [name, {
    enabled: policy.enabled ?? true,
    ...defaults,
    ...pick(policy, DEFAULT_KEYS),
    icon: policy.icon ?? null,
    deploy: policy.deploy ?? true,
    worker_policies: Object.fromEntries(
      Object.entries(policy.workers ?? {}).map(([worker, workerPolicy]) => [workerPolicy.workflow, {
        worker,
        enabled: workerPolicy.enabled ?? true,
        max_mode: workerPolicy["max-mode"] ?? null,
      }]),
    ),
    ...(policy.targets ? {
      target_policies: Object.fromEntries(
        Object.entries(policy.targets).map(([repository, targetPolicy]) => [repository.toLowerCase(), targetPolicy]),
      ),
    } : {}),
  }]));
  return {
    allowed_owners: scope["allowed-owners"] ?? [controlRepository.split("/", 1)[0]],
    allowed_repositories: scope["allowed-repositories"] ?? [],
    ...(control.activity ? {
      activity_repositories: control.activity.repositories,
      ...(control.activity["public-only"] !== undefined ? { activity_public_only: control.activity["public-only"] } : {}),
    } : {}),
    web: {
      experimental: web.experimental ?? false,
      favicon: web.favicon ?? "./favicon.svg",
      ...(web.host ? { host: web.host } : {}),
    },
    marketplace: control.marketplace ?? { registries: [] },
    campaigns,
    ...(control.operations ? { operations: Object.fromEntries(Object.entries(control.operations).map(([name, policy]) => {
      const authority = resolveAuthority({
        mode: policy.mode ?? defaults.mode, maxMode: policy["max-mode"],
        maxTargets: policy["max-repositories"] ?? defaults["max-repositories"],
      });
      return [name, { ...policy, enabled: policy.enabled ?? true, mode: authority.mode, "max-repositories": authority.maxTargets }];
    })) } : {}),
    publishing_enabled: publishing.enabled ?? false,
    publishing_control_repositories: publishing["control-repositories"] ?? [controlRepository],
    publishing_reviewers: publishing.reviewers ?? [],
  };
}

function denied(reason, campaignName, role, workerName) {
  return {
    authorized: false,
    reason,
    control_role: role,
    campaign: campaignName,
    worker: workerName,
    enabled: false,
    effective_max_repos: 0,
    candidate_repositories: [],
    worker_workflows: [],
  };
}

function pick(object, keys) {
  return Object.fromEntries(keys.filter((key) => key in object).map((key) => [key, object[key]]));
}

function assertKeys(mapping, allowed, path) {
  const unknown = Object.keys(mapping).find((key) => !allowed.includes(key));
  if (unknown) throw new PolicyError(`unknown key ${path}.${unknown}`);
}

function assertMapping(value, path) {
  if (!isRecord(value)) throw new PolicyError(`${path} must be a mapping`);
}

function assertBoolean(value, path) {
  if (value !== true && value !== false) throw new PolicyError(`${path} must be a Boolean`);
}

function assertOcticon(value, path) {
  if (typeof value !== "string" || !OCTICONS.includes(value)) {
    throw new PolicyError(`${path} must be one of: ${OCTICONS.join(", ")}`);
  }
}

function assertFavicon(value, path) {
  if (typeof value !== "string" || value.length > 2048 || /\s/.test(value)) {
    throw new PolicyError(`${path} must be an absolute HTTPS URL or ./ relative path`);
  }
  if (value.startsWith("./")) {
    if (!/^\.\/[A-Za-z0-9][A-Za-z0-9._/-]*$/.test(value) || value.split("/").includes("..")) {
      throw new PolicyError(`${path} must be an absolute HTTPS URL or ./ relative path`);
    }
    return;
  }
  let url;
  try {
    url = new URL(value);
  } catch {
    throw new PolicyError(`${path} must be an absolute HTTPS URL or ./ relative path`);
  }
  if (url.protocol !== "https:" || url.username || url.password || url.search || url.hash) {
    throw new PolicyError(`${path} must be an absolute HTTPS URL or ./ relative path`);
  }
}

/**
 * @param {unknown} value
 * @param {string} path
 * @param {number} minimum
 * @param {number} [maximum]
 */
function assertInteger(value, path, minimum, maximum = undefined) {
  const valid = typeof value === "number" && Number.isSafeInteger(value) && value >= minimum && (maximum === undefined || value <= maximum);
  const range = maximum === undefined ? `>= ${minimum}` : `${minimum}..${maximum}`;
  if (!valid) throw new PolicyError(`${path} must be an integer in ${range}`);
}

function assertMode(value, path) {
  if (!MODES.includes(value)) throw new PolicyError(`${path} must be review or live`);
}

function assertOneOf(value, path, allowed) {
  if (!allowed.includes(value)) throw new PolicyError(`${path} has an invalid value`);
}

function assertString(value, path, pattern) {
  if (typeof value !== "string" || !pattern.test(value)) throw new PolicyError(`${path} has an invalid value`);
}

function assertUniqueStrings(value, path, pattern) {
  if (!Array.isArray(value)) throw new PolicyError(`${path} must be an array`);
  value.forEach((item) => assertString(item, path, pattern));
  const normalized = value.map((item) => item.toLowerCase());
  if (new Set(normalized).size !== normalized.length) throw new PolicyError(`${path} must contain unique values`);
}

function assertUniqueRepositoryKeys(value, path) {
  const normalized = Object.keys(value).map((repository) => repository.toLowerCase());
  if (new Set(normalized).size !== normalized.length) throw new PolicyError(`${path} must contain unique repository names`);
}
