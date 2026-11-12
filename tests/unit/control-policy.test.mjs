import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import {
  controlSettings,
  effectivePolicy,
  loadPolicyFile,
  parsePolicy,
} from "../../.github/workflows/shared/policy.mjs";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const control = join(root, ".github", "workflows", "shared", "control.mjs");
const schema = JSON.parse(readFileSync(join(root, ".github", "workflows", "shared", "cao.schema.json"), "utf8"));

function validate(policy) {
  return spawnSync(process.execPath, [control, "validate-policy", "-"], {
    cwd: root,
    encoding: "utf8",
    input: policy,
  });
}

test("control policy recognizes an isolated, opt-in webhook gateway without enabling campaigns", () => {
  const document = {
    version: 1, "gh-aw-version": "v0.92.4",
    "control-plane": {
      scope: { "allowed-repositories": ["acme/control"] },
      "webhook-gateway": {
        version: 1, enabled: false, revision: 1, receivingAppId: 1, dispatchAppId: 2,
        namespace: "webhook-gateway", workers: 1, artifacts: [], grants: [],
      },
    },
  };
  const result = validate(JSON.stringify(document));
  assert.equal(result.status, 0, result.stderr);
  assert.equal(parsePolicy(JSON.stringify(document))["control-plane"]["webhook-gateway"].enabled, false);
  assert.equal(schema.$defs.controlPlane.properties["webhook-gateway"].$ref, "#/$defs/webhookGateway");
  const projects = structuredClone(document);
  const projectIds = ["PVT_base64+/=", "a".repeat(256)];
  projects["control-plane"]["webhook-gateway"].grants.push({
    subscriptionId: "projects", digest: "a".repeat(64), projectIds,
    sourceInstallationId: 1, sourceRepositoryId: 0, sourceOrganizationId: 7,
    receivingInstallationId: 1, destinationInstallationId: 2, destinationRepositoryId: 42,
    dispatchKind: "repository_dispatch", expiresAt: "2027-01-01T00:00:00Z",
  });
  const approved = validate(JSON.stringify(projects));
  assert.equal(approved.status, 0, approved.stderr);
  for (const ids of [["a".repeat(257)], ["PVT_bad!"], ["PVT_duplicate", "PVT_duplicate"]]) {
    const invalid = structuredClone(projects);
    invalid["control-plane"]["webhook-gateway"].grants[0].projectIds = ids;
    assert.notEqual(validate(JSON.stringify(invalid)).status, 0);
  }
  for (const mutate of [
    (gateway) => { gateway.dispatchAppId = gateway.receivingAppId; },
    (gateway) => { gateway.enabled = "live"; },
    (gateway) => { gateway.privateKey = "not-allowed"; },
    (gateway) => { gateway.queue = { unknown: 1 }; },
    (gateway) => { gateway.queue = { rates: { global: { capacity: 1, period: -1 } } }; },
  ]) {
    const invalid = structuredClone(document);
    mutate(invalid["control-plane"]["webhook-gateway"]);
    assert.notEqual(validate(JSON.stringify(invalid)).status, 0);
  }
});

test("webhook discovery is explicitly scoped and projects bind one enrolled repository", () => {
  const document = {
    version: 1, "gh-aw-version": "v0.92.5",
    "control-plane": {
      scope: { "allowed-owners": ["acme"], "allowed-repositories": ["acme/private"] },
      "webhook-gateway": {
        version: 1, enabled: false, revision: 1, receivingAppId: 1, dispatchAppId: 2,
        namespace: "gateway", workers: 1, artifacts: [], grants: [],
        discovery: { activateSecurity: true, intervalSeconds: 300, repositories: [
          { repository: "acme/private", repositoryId: 42, receivingInstallationId: 7, dispatchInstallationId: 8 },
        ] },
        projectAssignments: [{ projectId: "PVT_project", organizationId: 99, repositoryId: 42 }],
      },
    },
  };
  const parsed = parsePolicy(JSON.stringify(document));
  assert.equal(parsed["control-plane"]["webhook-gateway"].enabled, false);
  assert.equal(parsed["control-plane"]["webhook-gateway"].discovery.activateSecurity, true);
  assert.equal(parsed["control-plane"]["webhook-gateway"].discovery.activateCommands, undefined);
  assert.equal(parsed["control-plane"]["webhook-gateway"].discovery.activateNotifications, undefined);
  const notifications = structuredClone(document);
  notifications["control-plane"]["webhook-gateway"].discovery.activateNotifications = true;
  assert.equal(parsePolicy(JSON.stringify(notifications))["control-plane"]["webhook-gateway"].discovery.activateNotifications, true);
  const commands = structuredClone(document);
  commands["control-plane"]["webhook-gateway"].discovery.activateCommands = true;
  commands["control-plane"]["webhook-gateway"].discovery.serverUrl = "https://github.com";
  commands["control-plane"]["webhook-gateway"].discovery.repositories[0].allowLabelRemoval = true;
  assert.equal(parsePolicy(JSON.stringify(commands))["control-plane"]["webhook-gateway"].discovery.repositories[0].allowLabelRemoval, true);
  commands["control-plane"]["webhook-gateway"].discovery.serverUrl = "https://github.example:8443/github/base";
  assert.equal(parsePolicy(JSON.stringify(commands))["control-plane"]["webhook-gateway"].discovery.serverUrl, "https://github.example:8443/github/base");
  for (const mutate of [
    g => { delete g.discovery.activateSecurity; },
    g => { g.discovery.activateSecurity = null; },
    g => { g.discovery.intervalSeconds = 0; },
    g => { g.discovery.repositories = []; },
    g => { g.discovery.repositories[0].repository = "unapproved/private"; },
    g => { g.discovery.repositories[0].repository = "acme/other"; },
    g => { g.discovery.repositories[0].receivingInstallationId = 0; },
    g => { g.discovery.repositories.push({ ...g.discovery.repositories[0] }); },
    g => { g.projectAssignments[0].repositoryId = 100; },
    g => { g.projectAssignments.push({ ...g.projectAssignments[0] }); },
    g => { g.projectAssignments[0].projectId = "PVT_bad!"; },
    g => { g.discovery.extraAuthority = true; },
    g => { g.discovery.activateCommands = null; },
    g => { g.discovery.activateCommands = "true"; },
    g => { g.discovery.activateNotifications = null; },
    g => { g.discovery.activateNotifications = "true"; },
    g => { g.discovery.repositories[0].allowLabelRemoval = true; },
    g => { g.discovery.repositories[0].allowLabelRemoval = null; },
    g => { g.discovery.serverUrl = "https://github.com/"; },
    g => { g.discovery.serverUrl = "https://github.com:443"; },
    g => { g.discovery.serverUrl = "https://github.com:99999"; },
    g => { g.discovery.serverUrl = "https://token@github.com"; },
    g => { g.discovery.serverUrl = "http://github.com"; },
    g => { g.discovery.serverUrl = "https://github.com?query"; },
    g => { g.discovery.serverUrl = "https://github.example/a/../b"; },
    g => { g.discovery.serverUrl = "https://github.example/a//b"; },
    g => { g.discovery.serverUrl = "https://github.example/%61"; },
    g => { g.discovery.serverUrl = "https://github.example/%"; },
  ]) {
    const invalid = structuredClone(document);
    mutate(invalid["control-plane"]["webhook-gateway"]);
    assert.throws(() => parsePolicy(JSON.stringify(invalid)), undefined, String(mutate));
  }
});

test("external webhook sources require reviewed destination, native scope, and receive-only secret bindings", () => {
  const sources = JSON.parse(readFileSync(join(root, "specs/fixtures/webhook-external-deliveries-v2.json"), "utf8")).sources;
  const document = {
    version: 1, "gh-aw-version": "v0.92.5",
    "control-plane": {
      scope: { "allowed-repositories": ["acme/private"] },
      "webhook-gateway": {
        version: 1, enabled: false, revision: 1, receivingAppId: 1, dispatchAppId: 2,
        namespace: "gateway", workers: 1, artifacts: [], grants: [], externalSources: sources,
        discovery: { activateSecurity: false, repositories: [
          { repository: "acme/private", repositoryId: 11, receivingInstallationId: 7, dispatchInstallationId: 8 },
        ] },
      },
    },
  };
  assert.equal(parsePolicy(JSON.stringify(document))["control-plane"]["webhook-gateway"].externalSources.length, 3);
  assert.equal(schema.$defs.webhookGateway.properties.externalSources.items.$ref, "#/$defs/webhookExternalSource");
  for (const mutate of [
    g => { delete g.discovery; },
    g => { g.externalSources = null; },
    g => { g.externalSources.push(structuredClone(g.externalSources[0])); },
    g => { g.externalSources[0].secret = "forbidden"; },
    g => { g.externalSources[0].id = "compiler"; },
    g => { g.externalSources[0].secretEnv = "GH_TOKEN"; },
    g => { g.externalSources[0].secretEnv += "_PREVIOUS"; },
    g => { g.externalSources[1].secretEnv = g.externalSources[0].secretEnv; },
    g => { g.externalSources[0].expiresAt = null; },
    g => { g.externalSources[0].destinationRepositoryIds = [99]; },
    g => { g.externalSources[0].destinationRepositoryIds = [11, 11]; },
    g => { g.externalSources[0].resourceUrlPrefix = "http://example.atlassian.net/rest/api/3/issue/"; },
    g => { g.externalSources[0].resourceUrlPrefix = "https://user@example.atlassian.net/rest/api/3/issue/"; },
    g => { g.externalSources[0].resourceUrlPrefix += "?token=forbidden"; },
    g => { g.externalSources[0].resourceUrlPrefix = "https://example.atlassian.net/issue/"; },
    g => { g.externalSources[0].scope = ""; },
    g => { g.externalSources[0].types = []; },
    g => { g.externalSources[1].resourceUrlPrefix = "https://linear.app/issue/"; },
    g => { delete g.externalSources[2].collectionId; },
    g => { g.externalSources[2].resourceUrlPrefix = "https://unapproved.example/_apis/wit/workItems/"; },
    g => { g.externalSources[0].event = "webhook"; },
    g => { g.externalSources[0].types = ["com.example.release"]; },
    g => { g.externalSources[0].urlPath = "/data/url"; },
  ]) {
    const invalid = structuredClone(document);
    mutate(invalid["control-plane"]["webhook-gateway"]);
    assert.throws(() => parsePolicy(JSON.stringify(invalid)), undefined, String(mutate));
  }
});

function effective(policy, {
  campaignName = "dependabot",
  role = "orchestrator",
  worker = "",
  requestedMode = "",
  targetRepository = "",
  eventName = "",
} = {}) {
  try {
    return {
      status: 0,
      stderr: "",
      output: effectivePolicy(parsePolicy(policy), {
        campaignName,
        role,
        workerName: worker,
        controlRepository: "acme/control",
        requestedMode,
        targetRepository,
        eventName,
      }),
    };
  } catch (error) {
    return { status: 1, stderr: `${error.message}\n`, output: undefined };
  }
}

function effectiveWithLimits(policy, requestedMaxRepositories, requestedRolloutPercent) {
  try {
    return {
      status: 0,
      stderr: "",
      output: effectivePolicy(parsePolicy(policy), {
        campaignName: "dependabot",
        role: "orchestrator",
        controlRepository: "acme/control",
        requestedMaxRepositories,
        requestedRolloutPercent,
      }),
    };
  } catch (error) {
    return { status: 1, stderr: `${error.message}\n`, output: undefined };
  }
}

const minimalPolicy = JSON.stringify({
  $schema: schema.$id,
  version: 1,
  "gh-aw-version": "v0.89.15",
  "control-plane": {
    scope: { "allowed-repositories": ["acme/payments-api", "acme/storefront"] },
    campaigns: {
      dependabot: {
        mode: "live",
        "max-repositories": 8,
        workers: {
          "update-planner": {
            workflow: "dependabot-update-planner",
            "max-mode": "live",
          },
        },
      },
    },
  },
});

test("debug is request-only and cannot become persistent rollout or worker authority", () => {
  for (const change of [
    (document) => { document["control-plane"].defaults = { mode: "debug" }; },
    (document) => { document["control-plane"].campaigns.dependabot.mode = "debug"; },
    (document) => { document["control-plane"].campaigns.dependabot.targets = { "acme/payments-api": { mode: "debug" } }; },
    (document) => { document["control-plane"].campaigns.dependabot.workers["update-planner"]["max-mode"] = "debug"; },
  ]) {
    const document = JSON.parse(minimalPolicy);
    change(document);
    assert.throws(() => parsePolicy(JSON.stringify(document)), /must be review or live/);
  }
  const { output, status, stderr } = effective(minimalPolicy, {
    role: "worker", worker: "update-planner", targetRepository: "acme/payments-api",
    requestedMode: "debug", eventName: "workflow_dispatch",
  });
  assert.equal(status, 0, stderr);
  assert.equal(output.safe_output_mode, "debug");
  assert.deepEqual(output.target_policies, {});
  assert.deepEqual(output.allowed_repositories, ["acme/payments-api", "acme/storefront"]);
});

test("control policy accepts the minimal version 1 control document", () => {
  const result = validate(minimalPolicy);

  assert.equal(result.status, 0, result.stderr);
});

test("control policy schema accepts config-defined campaign and worker catalogs", () => {
  const policy = JSON.parse(readFileSync(join(root, ".github", "workflows", "cao.json"), "utf8"));

  assert.equal(schema.$schema, "https://json-schema.org/draft/2020-12/schema");
  assert.equal(policy.$schema, schema.$id);
  assert.match(policy["gh-aw-version"], /^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$/);
  assert.equal(schema.properties["gh-aw-version"].type, "string");
  assert.equal(schema.$defs.controlPlane.properties.web.$ref, "#/$defs/web");
  assert.equal(policy["control-plane"].web.experimental, true);
  assert.equal(policy["control-plane"].web.favicon, "./favicon.svg");
  assert.deepEqual(policy["control-plane"].marketplace.registries[0].auth, {
    type: "pat",
    secret: "GH_TOKEN",
  });
  assert.equal(schema.$defs.controlCampaigns.properties.activity.$ref, "#/$defs/campaignPolicy");
  assert.deepEqual(schema.$defs.controlCampaigns.additionalProperties.allOf, [
    { $ref: "#/$defs/campaignPolicy" },
    { not: { required: ["workflow-values"] } },
  ]);
  assert.equal(schema.$defs.targetCampaigns.additionalProperties.$ref, "#/$defs/targetCampaign");
  for (const campaignPolicy of Object.values(policy["control-plane"].campaigns)) {
    for (const workerPolicy of Object.values(campaignPolicy.workers ?? {})) {
      assert.match(workerPolicy.workflow, /^[a-z0-9]+(?:-[a-z0-9]+)*$/);
    }
  }
  assert.equal(validate(JSON.stringify(policy)).status, 0);
});

test("deployment profile extends cao.json without replacing rollout policy", () => {
  const directory = mkdtempSync(join(tmpdir(), "cao-profile-"));
  const base = JSON.parse(minimalPolicy);
  base["control-plane"].web = { experimental: true };
  writeFileSync(join(directory, "cao.json"), JSON.stringify(base));
  writeFileSync(join(directory, "cao.test.json"), JSON.stringify({
    extends: "cao.json",
    "control-plane": { web: { host: {
      target: { module: "container", name: "test" },
      redis: { module: "generic" },
    } } },
  }));
  const profile = loadPolicyFile(join(directory, "cao.test.json"));

  assert.deepEqual(profile["control-plane"].campaigns, base["control-plane"].campaigns);
  assert.equal(profile["control-plane"].web.experimental, true);
  assert.equal(profile["control-plane"].web.host.target.name, "test");
  assert.equal(profile["control-plane"].web.host.redis.module, "generic");
});

test("deployment profiles reject rollout overrides, cycles, and directory traversal", () => {
  const directory = mkdtempSync(join(tmpdir(), "cao-profile-"));
  const base = join(directory, "cao.json");
  writeFileSync(base, minimalPolicy);

  const widening = join(directory, "cao.widening.json");
  writeFileSync(widening, JSON.stringify({
    extends: "cao.json",
    "control-plane": {
      campaigns: { dependabot: { mode: "live" } },
      web: { host: { target: { module: "container" }, redis: { module: "generic" } } },
    },
  }));
  assert.throws(() => loadPolicyFile(widening), /unknown key deployment profile\.control-plane\.campaigns/);

  writeFileSync(join(directory, "cao.a.json"), JSON.stringify({
    extends: "cao.b.json",
    "control-plane": { web: { host: {} } },
  }));
  writeFileSync(join(directory, "cao.b.json"), JSON.stringify({
    extends: "cao.a.json",
    "control-plane": { web: { host: {} } },
  }));
  assert.throws(() => loadPolicyFile(join(directory, "cao.a.json")), /cycle/);

  const parentPolicy = join(dirname(directory), "cao.outside.json");
  writeFileSync(parentPolicy, minimalPolicy);
  writeFileSync(join(directory, "cao.escape.json"), JSON.stringify({
    extends: "../cao.outside.json",
    "control-plane": { web: { host: {} } },
  }));
  assert.throws(() => loadPolicyFile(join(directory, "cao.escape.json")), /within the policy directory/);
});

test("control policy validates provider-neutral host and Redis configuration", () => {
  const policy = JSON.parse(minimalPolicy);
  policy["control-plane"].web = {
    host: {
      target: {
        module: "generic",
        name: "managed-redis",
        authentication: "github-oauth",
        listener: "process",
        "require-https": true,
      },
      redis: {
        module: "redis-cloud",
        "url-env": "REDIS_URL",
        tls: {
          mode: "required",
          "server-name-env": "REDIS_TLS_SERVER_NAME",
          "ca-certificate-env": "REDIS_TLS_CA_CERT",
        },
      },
    },
  };

  const source = JSON.stringify(policy);
  assert.equal(validate(source).status, 0);
  assert.deepEqual(
    controlSettings(parsePolicy(source), "acme/control").web.host,
    policy["control-plane"].web.host,
  );

  policy["control-plane"].web.host.target.listener = "external";
  assert.equal(validate(JSON.stringify(policy)).status, 0);

  policy["control-plane"].web.host.target["require-https"] = false;
  const insecure = validate(JSON.stringify(policy));
  assert.notEqual(insecure.status, 0);
  assert.match(insecure.stderr, /require-https/);
});

test("control policy selects a volatile OAuth single-process operational backend", () => {
  const policy = JSON.parse(minimalPolicy);
  const memory = {
    backend: "memory",
    "single-process": true,
    "allow-volatile": true,
    "namespace-env": "CAO_OPERATIONAL_NAMESPACE",
  };
  policy["control-plane"].web = {
    host: { target: { module: "container", replicas: 1 }, "operational-store": memory },
  };
  assert.equal(validate(JSON.stringify(policy)).status, 0);
  assert.deepEqual(controlSettings(parsePolicy(JSON.stringify(policy)), "acme/control").web.host["operational-store"], memory);
  for (const operational of [
    { ...memory, backend: "postgres" },
    { ...memory, "single-process": false },
    { ...memory, "allow-volatile": false },
    { ...memory, redis: { module: "generic" } },
    { ...memory, unknown: true },
  ]) {
    policy["control-plane"].web.host["operational-store"] = operational;
    assert.notEqual(validate(JSON.stringify(policy)).status, 0);
  }
  policy["control-plane"].web.host["operational-store"] = memory;
  for (const target of [
    { module: "container" },
    { module: "container", replicas: 2 },
    { module: "azure-functions", replicas: 1 },
    { module: "generic", authentication: "github-oauth", listener: "platform", "supports-single-replica": true, replicas: 1 },
  ]) {
    policy["control-plane"].web.host.target = target;
    assert.notEqual(validate(JSON.stringify(policy)).status, 0);
  }
  policy["control-plane"].web.host.target = {
    module: "generic", authentication: "github-oauth", listener: "external", "supports-single-replica": true, replicas: 1,
  };
  assert.notEqual(validate(JSON.stringify(policy)).status, 0);
  policy["control-plane"].web.host.target.listener = "process";
  assert.equal(validate(JSON.stringify(policy)).status, 0);
  policy["control-plane"].web.host.redis = { module: "generic" };
  assert.notEqual(validate(JSON.stringify(policy)).status, 0);
});

test("control policy preserves Redis selection and provider restrictions", () => {
  const policy = JSON.parse(minimalPolicy);
  policy["control-plane"].web = {
    host: { target: { module: "container" }, "operational-store": { backend: "redis" } },
  };
  assert.equal(validate(JSON.stringify(policy)).status, 0);
  policy["control-plane"].web.host["operational-store"].redis = null;
  assert.notEqual(validate(JSON.stringify(policy)).status, 0);
  policy["control-plane"].web.host["operational-store"].redis = { module: "upstash" };
  assert.notEqual(validate(JSON.stringify(policy)).status, 0);
  policy["control-plane"].web.host.target.replicas = 1;
  assert.equal(validate(JSON.stringify(policy)).status, 0);
  policy["control-plane"].web.host.redis = { module: "generic" };
  assert.notEqual(validate(JSON.stringify(policy)).status, 0);
});

test("control policy accepts explicit PostgreSQL operational storage without Redis or volatile topology", () => {
  const policy = JSON.parse(minimalPolicy);
  const operational = {
    backend: "postgres",
    "namespace-env": "CAO_OPERATIONAL_NAMESPACE",
    postgres: { "url-env": "CAO_OPERATIONAL_POSTGRES_URL" },
  };
  policy["control-plane"].web = {
    host: { target: { module: "container" }, "operational-store": operational },
  };
  assert.equal(validate(JSON.stringify(policy)).status, 0);
  assert.deepEqual(controlSettings(parsePolicy(JSON.stringify(policy)), "acme/control").web.host["operational-store"], operational);
  const defaultPolicy = structuredClone(policy);
  delete defaultPolicy["control-plane"].web.host["operational-store"];
  assert.equal(validate(JSON.stringify(defaultPolicy)).status, 0);
  for (const invalid of [
    { ...operational, redis: { module: "generic" } },
    { ...operational, "single-process": true },
    { ...operational, "allow-volatile": true },
    { ...operational, postgres: null },
    { ...operational, postgres: { url: "postgres://secret" } },
    { ...operational, postgres: { "url-env": "postgres://secret" } },
    { ...operational, postgres: { "url-env": "" } },
    { ...operational, postgres: { "url-env": null } },
    { ...operational, postgres: { "url-env": "A".repeat(129) } },
    { ...operational, backend: "redis" },
    { ...operational, backend: "memory", "single-process": true, "allow-volatile": true },
  ]) {
    policy["control-plane"].web.host["operational-store"] = invalid;
    assert.notEqual(validate(JSON.stringify(policy)).status, 0);
  }
});

test("control policy validates host CORS configuration", () => {
  const policy = JSON.parse(minimalPolicy);
  policy["control-plane"].web = {
    host: {
      target: { module: "container" },
      redis: { module: "render" },
      cors: {
        "allowed-origins": ["https://tools.example.com", "http://localhost:5173"],
        "max-age": 600,
      },
    },
  };
  const source = JSON.stringify(policy);
  assert.equal(validate(source).status, 0);
  assert.deepEqual(
    controlSettings(parsePolicy(source), "acme/control").web.host.cors,
    policy["control-plane"].web.host.cors,
  );

  for (const [cors, message] of [
    [{ "allowed-origins": ["*"] }, /allowed-origins has an invalid value/],
    [{ "allowed-origins": ["http://tools.example.com"] }, /allowed-origins has an invalid value/],
    [{ "allowed-origins": ["https://tools.example.com/path"] }, /allowed-origins has an invalid value/],
    [{ "allowed-origins": ["https://tools.example.com"], "allow-credentials": true }, /allow-credentials/],
    [{ "max-age": 0 }, /max-age must be an integer/],
    [{ "allowed-origins": ["https://tools.example.com:70000"] }, /invalid port/],
    [{ "allowed-origins": ["https://example.com."] }, /allowed-origins has an invalid value/],
    [{ "allowed-origins": ["https://a..example.com"] }, /allowed-origins has an invalid value/],
    [{ "allowed-origins": ["https://bücher.example"] }, /allowed-origins has an invalid value/],
    [{ origins: [] }, /cors/],
  ]) {
    policy["control-plane"].web.host.cors = cors;
    const result = validate(JSON.stringify(policy));
    assert.notEqual(result.status, 0, JSON.stringify(cors));
    assert.match(result.stderr, message);
  }
});

test("control policy rejects inconsistent host capabilities", () => {
  const policy = JSON.parse(minimalPolicy);
  policy["control-plane"].web = {
    host: {
      target: {
        module: "generic",
        authentication: "github-oauth",
        listener: "process",
      },
      redis: { module: "generic", session: "pooled", "single-replica": true },
    },
  };

  const result = validate(JSON.stringify(policy));
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /single-replica requires/);
});

test("control policy validates every deployment module and rejects incompatible composition", () => {
  const targetModules = ["container", "azure-functions"];
  const redisModules = [
    "generic", "local", "upstash", "aws-elasticache", "redis-cloud",
    "gcp-memorystore", "railway", "render", "digitalocean",
  ];
  assert.deepEqual(schema.$defs.hostTarget.properties.module.enum, ["generic", ...targetModules]);
  assert.deepEqual(schema.$defs.redisModule.properties.module.enum, redisModules);

  for (const module of targetModules) {
    const policy = JSON.parse(minimalPolicy);
    policy["control-plane"].web = {
      host: { target: { module }, redis: { module: "generic" } },
    };
    assert.equal(validate(JSON.stringify(policy)).status, 0, module);
  }
  for (const module of redisModules) {
    const policy = JSON.parse(minimalPolicy);
    policy["control-plane"].web = {
      host: {
        target: { module: "container", ...(module === "upstash" ? { replicas: 1 } : {}) },
        redis: { module },
      },
    };
    assert.equal(validate(JSON.stringify(policy)).status, 0, module);
  }

  const incompatible = JSON.parse(minimalPolicy);
  incompatible["control-plane"].web = {
    host: {
      target: { module: "azure-functions" },
      redis: { module: "upstash" },
    },
  };
  assert.match(validate(JSON.stringify(incompatible)).stderr, /single-replica requirement/);

  const missingReplicaCount = JSON.parse(minimalPolicy);
  missingReplicaCount["control-plane"].web = {
    host: {
      target: { module: "container" },
      redis: { module: "upstash" },
    },
  };
  assert.match(validate(JSON.stringify(missingReplicaCount)).stderr, /target\.replicas must be 1/);
});

test("control policy rejects the previous flat host shape", () => {
  const policy = JSON.parse(minimalPolicy);
  policy["control-plane"].web = {
    host: {
      name: "legacy-host",
      authentication: "github-oauth",
      listener: "process",
      "require-https": true,
      "supports-collection": true,
      redis: {
        preset: "redis-cloud",
        "url-env": "REDIS_URL",
        session: "pooled",
        tls: { mode: "required" },
      },
    },
  };
  const result = validate(JSON.stringify(policy));
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /unknown key control-plane\.web\.host\.name|target is required/);
});

test("control policy rejects malformed gh-aw compiler versions", () => {
  const policy = JSON.parse(minimalPolicy);
  policy["gh-aw-version"] = "latest";

  const result = validate(JSON.stringify(policy));

  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /gh-aw-version has an invalid value/);
});

test("control policy requires a gh-aw compiler version for control planes", () => {
  const policy = JSON.parse(minimalPolicy);
  delete policy["gh-aw-version"];

  const result = validate(JSON.stringify(policy));

  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /gh-aw-version is required for a control plane/);
});

test("checked-in control policy keeps Dependabot scoped with live gh-aw target and local SelfCare authority", () => {
  const policy = parsePolicy(readFileSync(join(root, ".github", "workflows", "cao.json"), "utf8"));
  const repositories = [
    "github/gh-aw",
    "github/gh-aw-firewall",
    "github/gh-aw-mcpg",
    "github/gh-aw-actions",
    "github/gh-aw-threat-detection",
    "githubnext/gh-cao",
  ];

  assert.deepEqual(policy["control-plane"].scope["allowed-repositories"], repositories);
  assert.equal(policy["control-plane"].defaults["max-repositories"], 6);

  const dependabotCampaign = policy["control-plane"].campaigns.dependabot;
  assert.equal(dependabotCampaign.mode, "review");
  assert.equal(dependabotCampaign["max-repositories"], 1);
  assert.equal(dependabotCampaign["rollout-percent"], 100);
  assert.deepEqual(dependabotCampaign.targets, {
    "github/gh-aw": {
      mode: "live",
    },
  });

  const dependabotGhAw = effectivePolicy(policy, {
    campaignName: "dependabot",
    role: "orchestrator",
    controlRepository: "githubnext/gh-cao",
    targetRepository: "github/gh-aw",
  });
  assert.equal(dependabotGhAw.safe_output_mode, "live");
  assert.equal(dependabotGhAw.max_repositories, 1);

  for (const targetRepository of repositories.filter((repository) => repository !== "github/gh-aw")) {
    const effective = effectivePolicy(policy, {
      campaignName: "dependabot",
      role: "orchestrator",
      controlRepository: "githubnext/gh-cao",
      targetRepository,
    });
    assert.equal(effective.safe_output_mode, "review");
    assert.equal(effective.max_repositories, 1);
  }

  const selfCare = effectivePolicy(policy, {
    campaignName: "self-care",
    role: "orchestrator",
    controlRepository: "githubnext/gh-cao",
    targetRepository: "githubnext/gh-cao",
  });
  assert.equal(selfCare.safe_output_mode, "live");
  assert.equal(selfCare.max_repositories, 1);
  assert.equal(policy["control-plane"].campaigns["self-care"].workers["open-source-failures"], undefined);
  assert.equal(
    policy["control-plane"].campaigns["self-care"].workers["dashboard-performance"].workflow,
    "self-care-dashboard-performance",
  );
  assert.equal(
    policy["control-plane"].campaigns["self-care"].workers["pages-health"].workflow,
    "self-care-pages-health",
  );
  assert.equal(
    policy["control-plane"].campaigns["self-care"].workers.glossary.workflow,
    "self-care-glossary",
  );
  assert.equal(policy["control-plane"].campaigns["self-care"].workers["hosted-health"], undefined);
  assert.equal(
    policy["control-plane"].campaigns["self-care"].workers["docs-maintainer"].workflow,
    "self-care-docs-maintainer",
  );
  assert.equal(
    policy["control-plane"].campaigns["self-care"].workers["specs-maintainer"].workflow,
    "self-care-specs-maintainer",
  );
  assert.equal(
    policy["control-plane"].campaigns["self-care"].workers["reactive-ui-expert"].workflow,
    "self-care-reactive-ui-expert",
  );
  assert.equal(
    policy["control-plane"].campaigns["self-care"].workers["server-go-logging"].workflow,
    "self-care-server-go-logging",
  );
  assert.equal(policy["target-authority"].campaigns["self-care"].authority, "githubnext/gh-cao");
});

test("control policy applies schema defaults and campaign values", () => {
  const result = effective(minimalPolicy);

  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.output.authorized, true);
  assert.equal(result.output.safe_output_mode, "live");
  assert.equal(result.output.max_repositories, 8);
  assert.equal(result.output.rollout_percent, 100);
  assert.equal(result.output.monthly_ai_credit_budget, 0);
  assert.deepEqual(result.output.worker_policies, {
    "dependabot-update-planner": {
      worker: "update-planner",
      enabled: true,
      max_mode: "live",
    },
  });
  assert.deepEqual(result.output.allowed_owners, ["acme"]);
});

test("control policy exposes scope and publishing defaults to deterministic add-ons", () => {
  assert.deepEqual(controlSettings(parsePolicy(minimalPolicy), "acme/control"), {
    allowed_owners: ["acme"],
    allowed_repositories: ["acme/payments-api", "acme/storefront"],
    web: {
      experimental: false,
      favicon: "./favicon.svg",
    },
    marketplace: { registries: [] },
    campaigns: {
      dependabot: {
        enabled: true,
        mode: "live",
        "max-repositories": 8,
        "rollout-percent": 100,
        "monthly-ai-credit-budget": 0,
        icon: null,
        worker_policies: {
          "dependabot-update-planner": {
            worker: "update-planner",
            enabled: true,
            max_mode: "live",
          },
        },
      },
    },
    publishing_enabled: false,
    publishing_control_repositories: ["acme/control"],
    publishing_reviewers: [],
  });
});

test("control policy validates and exposes an explicit Activity collection subset", () => {
  const policy = JSON.parse(minimalPolicy);
  policy["control-plane"].scope["allowed-repositories"].push("acme/control");
  policy["control-plane"].activity = { repositories: ["acme/control"] };

  assert.equal(validate(JSON.stringify(policy)).status, 0);
  assert.deepEqual(
    controlSettings(parsePolicy(JSON.stringify(policy)), "acme/control").activity_repositories,
    ["acme/control"],
  );
  policy["control-plane"].activity["public-only"] = true;
  assert.equal(validate(JSON.stringify(policy)).status, 0);
  assert.equal(controlSettings(parsePolicy(JSON.stringify(policy)), "acme/control").activity_public_only, true);
  policy["control-plane"].activity["public-only"] = "true";
  assert.notEqual(validate(JSON.stringify(policy)).status, 0);
  delete policy["control-plane"].activity["public-only"];

  policy["control-plane"].activity.repositories = ["octo/outside-scope"];
  const outsideScope = validate(JSON.stringify(policy));
  assert.notEqual(outsideScope.status, 0);
  assert.match(outsideScope.stderr, /outside control-plane\.scope\.allowed-repositories/);

  delete policy["control-plane"].scope["allowed-repositories"];
  policy["control-plane"].scope["allowed-owners"] = ["acme"];
  policy["control-plane"].activity.repositories = ["octo/outside-owner"];
  const outsideOwner = validate(JSON.stringify(policy));
  assert.notEqual(outsideOwner.status, 0);
  assert.match(outsideOwner.stderr, /outside control-plane\.scope\.allowed-owners/);

  policy["control-plane"].activity.repositories = [];
  const empty = validate(JSON.stringify(policy));
  assert.notEqual(empty.status, 0);
  assert.match(empty.stderr, /must contain at least one repository/);
});

test("control policy validates and exposes web presentation settings", () => {
  const policy = JSON.parse(minimalPolicy);
  policy["control-plane"].web = {
    experimental: true,
    favicon: "https://example.com/operations.svg",
  };

  assert.equal(validate(JSON.stringify(policy)).status, 0);
  assert.deepEqual(controlSettings(parsePolicy(JSON.stringify(policy)), "acme/control").web, {
    experimental: true,
    favicon: "https://example.com/operations.svg",
  });

  policy["control-plane"].web.experimental = "true";
  const invalidExperimental = validate(JSON.stringify(policy));
  assert.notEqual(invalidExperimental.status, 0);
  assert.match(invalidExperimental.stderr, /control-plane\.web\.experimental must be a Boolean/);
  policy["control-plane"].web.experimental = true;

  for (const favicon of [
    "http://example.com/favicon.svg",
    "https://user@example.com/favicon.svg",
    "https://example.com/favicon.svg?version=1",
    "../favicon.svg",
  ]) {
    policy["control-plane"].web.favicon = favicon;
    const result = validate(JSON.stringify(policy));
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /control-plane\.web\.favicon must be an absolute HTTPS URL or \.\/ relative path/);
  }
});

test("control policy validates and exposes a campaign octicon", () => {
  const policyWithIcon = JSON.stringify({
    $schema: schema.$id,
    version: 1,
    "gh-aw-version": "v0.89.15",
    "control-plane": {
      campaigns: { dependabot: { icon: "dependabot" } },
    },
  });

  const policyWithInvalidIcon = JSON.stringify({
    version: 1,
    "gh-aw-version": "v0.89.15",
    "control-plane": {
      campaigns: { dependabot: { icon: "not-a-real-icon" } },
    },
  });

  assert.equal(validate(policyWithIcon).status, 0, validate(policyWithIcon).stderr);
  const invalidResult = validate(policyWithInvalidIcon);
  assert.notEqual(invalidResult.status, 0);
  assert.match(invalidResult.stderr, /control-plane\.campaigns\.dependabot\.icon must be one of/);

  assert.equal(
    controlSettings(parsePolicy(policyWithIcon), "acme/control").campaigns.dependabot.icon,
    "dependabot",
  );
});

test("control policy rejects removed campaign deployment settings as unknown keys", () => {
  for (const campaign of ["dashboard", "dependabot"]) {
    for (const deploy of [false, true, "false"]) {
      const policy = JSON.parse(minimalPolicy);
      policy["control-plane"].campaigns[campaign] = { deploy };

      const invalid = validate(JSON.stringify(policy));
      assert.notEqual(invalid.status, 0);
      assert.ok(invalid.stderr.includes(`unknown key control-plane.campaigns.${campaign}.deploy`), invalid.stderr);
    }
  }
});

test("control policy validates config-defined worker workflow identities", () => {
  const missingWorkflow = JSON.parse(minimalPolicy);
  delete missingWorkflow["control-plane"].campaigns.dependabot.workers["update-planner"].workflow;
  const duplicateWorkflow = JSON.parse(minimalPolicy);
  duplicateWorkflow["control-plane"].campaigns.dependabot.workers.secondary = {
    workflow: "dependabot-update-planner",
  };

  const missingResult = validate(JSON.stringify(missingWorkflow));
  assert.notEqual(missingResult.status, 0);
  assert.match(missingResult.stderr, /workers\.update-planner\.workflow has an invalid value/);

  const duplicateResult = validate(JSON.stringify(duplicateWorkflow));
  assert.notEqual(duplicateResult.status, 0);
  assert.match(duplicateResult.stderr, /workers must declare unique workflow identities/);
});

test("control policy registers the dashboard debug logging worker", () => {
  const policy = JSON.parse(readFileSync(join(root, ".github", "workflows", "cao.json"), "utf8"));
  assert.equal(
    policy["control-plane"].campaigns["self-care"].workers["dashboard-debug-logging"].workflow,
    "self-care-dashboard-debug-logging",
  );
});

test("control policy disables campaigns by absence and requires declared workers", () => {
  const absentCampaign = effective(minimalPolicy, { campaignName: "optimization" });
  const declaredWorker = effective(minimalPolicy, {
    role: "worker",
    worker: "update-planner",
    campaignName: "dependabot",
  });
  const policyWithoutWorker = JSON.parse(minimalPolicy);
  delete policyWithoutWorker["control-plane"].campaigns.dependabot.workers;
  const undeclaredWorker = effective(JSON.stringify(policyWithoutWorker), {
    role: "worker",
    worker: "update-planner",
  });
  const disabledWorker = effective(minimalPolicy.replace('"max-mode":"live"', '"enabled":false'), {
    role: "worker",
    worker: "update-planner",
  });
  const disabledWorkerOrchestrator = effective(
    minimalPolicy.replace('"max-mode":"live"', '"enabled":false'),
  );
  const disabledCampaignWithoutWorker = JSON.parse(minimalPolicy);
  disabledCampaignWithoutWorker["control-plane"].campaigns.dependabot = { enabled: false };
  const disabledCampaignWorker = effective(JSON.stringify(disabledCampaignWithoutWorker), {
    role: "worker",
    worker: "update-planner",
  });

  assert.equal(absentCampaign.output.reason, "campaign-undeclared");
  assert.equal(declaredWorker.output.authorized, true);
  assert.notEqual(undeclaredWorker.status, 0);
  assert.match(undeclaredWorker.stderr, /unknown worker: dependabot\/update-planner/);
  assert.equal(
    disabledWorkerOrchestrator.output.worker_policies["dependabot-update-planner"].enabled,
    false,
  );
  assert.equal(disabledWorker.output.reason, "worker-disabled");
  assert.equal(disabledCampaignWorker.output.reason, "campaign-disabled");
});

test("control policy intersects campaign mode, dispatch request, and worker ceiling", () => {
  const reviewRequest = effective(minimalPolicy, {
    role: "worker",
    worker: "update-planner",
    requestedMode: "review",
  });
  const reviewCeilingPolicy = minimalPolicy.replace('"max-mode":"live"', '"max-mode":"review"');
  const reviewCeiling = effective(reviewCeilingPolicy, {
    role: "worker",
    worker: "update-planner",
  });
  const widening = effective(reviewCeilingPolicy, {
    role: "worker",
    worker: "update-planner",
    requestedMode: "live",
  });

  assert.equal(reviewRequest.output.safe_output_mode, "review");
  assert.equal(reviewCeiling.output.safe_output_mode, "review");
  assert.notEqual(widening.status, 0);
  assert.match(widening.stderr, /safe_output_mode exceeds checked-in policy/);
});

test("workers inherit the resolved mode when max-mode is omitted", () => {
  const policy = JSON.stringify({
    version: 1,
    "gh-aw-version": "v0.89.15",
    "control-plane": {
      scope: { "allowed-repositories": ["acme/payments-api", "acme/storefront"] },
      campaigns: {
        dependabot: {
          mode: "review",
          targets: { "acme/payments-api": { mode: "live" } },
          workers: {
            "update-planner": {
              workflow: "dependabot-update-planner",
            },
          },
        },
      },
    },
  });

  const liveWorker = effective(policy, {
    role: "worker",
    worker: "update-planner",
    targetRepository: "acme/payments-api",
  });
  const reviewWorker = effective(policy, {
    role: "worker",
    worker: "update-planner",
    targetRepository: "acme/storefront",
  });

  assert.equal(liveWorker.status, 0, liveWorker.stderr);
  assert.equal(liveWorker.output.safe_output_mode, "live");
  assert.equal(reviewWorker.status, 0, reviewWorker.stderr);
  assert.equal(reviewWorker.output.safe_output_mode, "review");
});

test("control policy resolves exact campaign target modes", () => {
  const policy = JSON.stringify({
    $schema: schema.$id,
    version: 1,
    "gh-aw-version": "v0.89.15",
    "control-plane": {
      scope: { "allowed-repositories": ["acme/payments-api", "acme/storefront"] },
      campaigns: {
        dependabot: {
          mode: "review",
          targets: {
            "acme/payments-api": { mode: "live" },
          },
          workers: {
            "update-planner": {
              workflow: "dependabot-update-planner",
              "max-mode": "live",
            },
          },
        },
      },
    },
  });

  const defaultTarget = effective(policy, { targetRepository: "acme/storefront" });
  const liveTarget = effective(policy, { targetRepository: "ACME/payments-api" });
  const liveWorker = effective(policy, {
    role: "worker",
    worker: "update-planner",
    targetRepository: "acme/payments-api",
  });
  const reviewCeilingWorker = effective(policy.replace('"max-mode":"live"', '"max-mode":"review"'), {
    role: "worker",
    worker: "update-planner",
    targetRepository: "acme/payments-api",
  });
  const narrowedTarget = effective(policy, {
    targetRepository: "acme/payments-api",
    requestedMode: "review",
  });

  assert.equal(defaultTarget.status, 0, defaultTarget.stderr);
  assert.equal(defaultTarget.output.safe_output_mode, "review");
  assert.equal(liveTarget.status, 0, liveTarget.stderr);
  assert.equal(liveTarget.output.safe_output_mode, "live");
  assert.equal(liveWorker.status, 0, liveWorker.stderr);
  assert.equal(liveWorker.output.safe_output_mode, "live");
  assert.equal(reviewCeilingWorker.status, 0, reviewCeilingWorker.stderr);
  assert.equal(reviewCeilingWorker.output.safe_output_mode, "review");
  assert.deepEqual(liveTarget.output.target_policies, {
    "acme/payments-api": { mode: "live" },
  });
  assert.equal(narrowedTarget.status, 0, narrowedTarget.stderr);
  assert.equal(narrowedTarget.output.safe_output_mode, "review");
  assert.deepEqual(narrowedTarget.output.target_policies, {
    "acme/payments-api": { mode: "review" },
  });
});

test("control policy requires campaign targets to stay inside explicit scope", () => {
  const policy = JSON.stringify({
    version: 1,
    "gh-aw-version": "v0.89.15",
    "control-plane": {
      scope: { "allowed-repositories": ["acme/storefront"] },
      campaigns: {
        dependabot: {
          targets: {
            "acme/payments-api": { mode: "live" },
          },
        },
      },
    },
  });

  const result = validate(policy);

  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /campaign target acme\/payments-api is outside control-plane\.scope\.allowed-repositories/);
});

for (const [name, targets, error] of [
  ["an array", [], /control-plane\.campaigns\.dependabot\.targets must be a mapping/],
  ["a missing mode", { "acme/payments-api": {} }, /control-plane\.campaigns\.dependabot\.targets\.acme\/payments-api\.mode is required/],
  ["an unsupported field", { "acme/payments-api": { mode: "live", percentage: 10 } }, /unknown key control-plane\.campaigns\.dependabot\.targets\.acme\/payments-api\.percentage/],
  ["an invalid mode", { "acme/payments-api": { mode: "preview" } }, /control-plane\.campaigns\.dependabot\.targets\.acme\/payments-api\.mode must be review or live/],
  ["case-insensitive duplicates", { "acme/payments-api": { mode: "live" }, "ACME/PAYMENTS-API": { mode: "review" } }, /control-plane\.campaigns\.dependabot\.targets must contain unique repository names/],
]) {
  test(`control policy rejects campaign targets with ${name}`, () => {
    const result = validate(JSON.stringify({
      version: 1,
      "gh-aw-version": "v0.89.15",
      "control-plane": {
        scope: { "allowed-owners": ["acme"] },
        campaigns: { dependabot: { targets } },
      },
    }));

    assert.notEqual(result.status, 0);
    assert.match(result.stderr, error);
  });
}

test("control policy requires campaign targets to stay inside allowed owners", () => {
  const result = validate(JSON.stringify({
    version: 1,
    "gh-aw-version": "v0.89.15",
    "control-plane": {
      scope: { "allowed-owners": ["acme"] },
      campaigns: {
        dependabot: { targets: { "outside/payments-api": { mode: "live" } } },
      },
    },
  }));

  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /campaign target outside\/payments-api is outside control-plane\.scope\.allowed-owners/);
});

test("control policy permits dispatch limits to narrow but not widen policy", () => {
  const narrowed = effectiveWithLimits(minimalPolicy, 3, 50);
  const maxWidening = effectiveWithLimits(minimalPolicy, 9, "");
  const rolloutWidening = effectiveWithLimits(minimalPolicy, "", 101);

  assert.equal(narrowed.output.max_repositories, 3);
  assert.equal(narrowed.output.rollout_percent, 50);
  assert.match(maxWidening.stderr, /max_repositories exceeds checked-in policy/);
  assert.match(rolloutWidening.stderr, /rollout_percent must be an integer in 1..100/);
});

test("control policy accepts target-only authority in the version 1 shape", () => {
  const result = validate(JSON.stringify({
    version: 1,
    "target-authority": { campaigns: { optimization: { authority: "acme/control" } } },
  }));

  assert.equal(result.status, 0, result.stderr);
});

for (const [name, policy, error] of [
  ["legacy root bundles", '{"version":1,"bundles":{}}', /unknown key policy.bundles/],
  ["future versions", '{"version":2,"control-plane":{}}', /version must be an integer in 1..1/],
  ["unknown schema URI", '{"$schema":"https://example.com/policy.schema.json","version":1,"control-plane":{}}', /\$schema must be https:\/\/raw\.githubusercontent\.com/],
  ["unknown nested keys", '{"version":1,"gh-aw-version":"v0.89.15","control-plane":{"campaigns":{"dependabot":{"surprise":true}}}}', /unknown key control-plane.campaigns.dependabot.surprise/],
  ["duplicate keys", '{"version":1,"version":1,"control-plane":{}}', /duplicate mapping key: version/],
  ["malformed JSON", '{"version":1,}', /invalid policy JSON/],
  ["expressions", '{"version":1,"control-plane":{"scope":{"allowed-owners":["${{ github.repository_owner }}"]}}}', /must not contain a GitHub Actions expression/],
]) {
  test(`control policy rejects ${name}`, () => {
    const result = validate(policy);

    assert.notEqual(result.status, 0, `${name} unexpectedly succeeded`);
    assert.match(result.stderr, error);
  });
}
