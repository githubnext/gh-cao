import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const control = join(root, ".github", "workflows", "shared", "control.mjs");
const schemaUri = "https://raw.githubusercontent.com/githubnext/gh-cao/main/.github/workflows/shared/cao.schema.json";

function policy() {
  return {
    $schema: schemaUri,
    version: 1,
    "gh-aw-version": "v0.89.20",
    "control-plane": {
      scope: {
        "allowed-owners": ["acme"],
        "allowed-repositories": ["acme/target"],
      },
      inventory: {
        "max-scan-repositories": 100,
        "cell-count": 2,
        "cell-index": 1,
        "batch-size": 50,
        "batch-index": 0,
      },
      web: {
        favicon: "https://example.com/favicon.svg",
      },
      defaults: {
        mode: "review",
        "max-repositories": 10,
        "rollout-percent": 75,
        "monthly-ai-credit-budget": 1000,
      },
      campaigns: {
        operations: {
          enabled: true,
          mode: "review",
          "max-repositories": 8,
          "rollout-percent": 50,
          "monthly-ai-credit-budget": 500,
          icon: "code",
          targets: {
            "acme/target": { mode: "live" },
          },
          workers: {
            auditor: {
              workflow: "operations-auditor",
              enabled: true,
              "max-mode": "live",
            },
          },
        },
      },
      publishing: {
        enabled: true,
        "control-repositories": ["acme/control"],
        reviewers: ["octocat"],
      },
    },
    "target-authority": {
      campaigns: {
        operations: { authority: "acme/control" },
      },
    },
  };
}

test("control.mjs reads the configured gh-aw compiler version", () => {
  const source = policy();
  const directory = mkdtempSync(join(tmpdir(), "cao-policy-"));
  const policyPath = join(directory, "cao.json");
  writeFileSync(policyPath, JSON.stringify(source));

  try {
    const result = run(["compiler-version", policyPath]);
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout, "v0.89.20\n");
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("control.mjs resolves deployment profiles for control settings", () => {
  const directory = mkdtempSync(join(tmpdir(), "cao-policy-profile-"));
  const policyPath = join(directory, "cao.json");
  const profilePath = join(directory, "cao.container.json");
  writeFileSync(policyPath, JSON.stringify(policy()));
  writeFileSync(profilePath, JSON.stringify({
    extends: "./cao.json",
    "control-plane": {
      web: {
        host: {
          target: {
            module: "container",
            name: "container",
          },
          "operational-store": {
            backend: "redis",
            "namespace-env": "CAO_OPERATIONAL_NAMESPACE",
            redis: {
              module: "local",
              "url-env": "REDIS_URL",
              "allow-private-plaintext": true,
              tls: {
                mode: "disabled",
              },
            },
          },
        },
      },
    },
  }));

  try {
    const result = run(["control-settings", profilePath]);
    assert.equal(result.status, 0, result.stderr);
    const settings = JSON.parse(result.stdout);
    assert.deepEqual(settings.allowed_repositories, ["acme/target"]);
    assert.equal(settings.web.host.target.name, "container");
    assert.equal(settings.web.host["operational-store"].backend, "redis");
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

function run(args, input = "", environment = {}) {
  return spawnSync(process.execPath, [control, ...args], {
    cwd: root,
    encoding: "utf8",
    input,
    env: {
      ...process.env,
      GITHUB_REPOSITORY: "acme/control",
      CAO_CAMPAIGN: "operations",
      CAO_ROLE: "orchestrator",
      CAO_WORKER: "",
      CAO_REQUESTED_MODE: "",
      CAO_REQUESTED_MAX_REPOSITORIES: "",
      CAO_REQUESTED_ROLLOUT_PERCENT: "",
      CAO_TARGET_REPOSITORY: "",
      ...environment,
    },
  });
}

function validate(source) {
  return run(["validate-policy", "-"], source);
}

function expectFailure(result, expected) {
  assert.notEqual(result.status, 0, `expected failure, received stdout:\n${result.stdout}`);
  assert.ok(
    result.stderr.includes(expected),
    `expected stderr to include ${JSON.stringify(expected)}, received:\n${result.stderr}`,
  );
}

test("control.mjs always logs policy validation without changing JSON output", () => {
  const source = policy();
  const result = validate(JSON.stringify(source));

  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(JSON.parse(result.stdout), source);
  assert.equal(result.stderr, [
    "[CAO policy] Parsing control policy.",
    "[CAO policy] Validated control policy.",
    "",
  ].join("\n"));
});

const rawPolicyViolations = [
  ["malformed JSON", '{"version":1,}', "invalid policy JSON"],
  ["non-mapping root", "[]", "policy root must be a mapping"],
  ["duplicate keys", '{"version":1,"version":1,"control-plane":{}}', "duplicate mapping key: version"],
  [
    "GitHub Actions expressions",
    '{"version":1,"control-plane":{"scope":{"allowed-owners":["${{ github.repository_owner }}"]}}}',
    "policy.control-plane.scope.allowed-owners[0] must not contain a GitHub Actions expression",
  ],
];

for (const [name, source, expected] of rawPolicyViolations) {
  test(`control.mjs validate-policy rejects ${name}`, () => {
    expectFailure(validate(source), expected);
  });
}

const policyViolations = [
  ["unknown root properties", (value) => { value.unexpected = true; }, "unknown key policy.unexpected"],
  ["unknown schema URIs", (value) => { value.$schema = "https://example.com/schema.json"; }, "$schema must be"],
  ["missing versions", (value) => { delete value.version; }, "version must be an integer in 1..1"],
  ["unsupported versions", (value) => { value.version = 2; }, "version must be an integer in 1..1"],
  ["documents without a role", (value) => {
    delete value["control-plane"];
    delete value["target-authority"];
  }, "policy requires control-plane or target-authority"],
  ["non-mapping control planes", (value) => { value["control-plane"] = []; }, "control-plane must be a mapping"],
  ["unknown control-plane properties", (value) => {
    value["control-plane"].unexpected = true;
  }, "unknown key control-plane.unexpected"],
  ["non-mapping scopes", (value) => { value["control-plane"].scope = []; }, "control-plane.scope must be a mapping"],
  ["unknown scope properties", (value) => {
    value["control-plane"].scope.unexpected = true;
  }, "unknown key control-plane.scope.unexpected"],
  ["non-array allowed owners", (value) => {
    value["control-plane"].scope["allowed-owners"] = "acme";
  }, "control-plane.scope.allowed-owners must be an array"],
  ["malformed allowed owners", (value) => {
    value["control-plane"].scope["allowed-owners"] = ["not/an-owner"];
  }, "control-plane.scope.allowed-owners has an invalid value"],
  ["duplicate allowed owners", (value) => {
    value["control-plane"].scope["allowed-owners"] = ["acme", "ACME"];
  }, "control-plane.scope.allowed-owners must contain unique values"],
  ["non-array allowed repositories", (value) => {
    value["control-plane"].scope["allowed-repositories"] = "acme/target";
  }, "control-plane.scope.allowed-repositories must be an array"],
  ["malformed allowed repositories", (value) => {
    value["control-plane"].scope["allowed-repositories"] = ["not-a-repository"];
  }, "control-plane.scope.allowed-repositories has an invalid value"],
  ["duplicate allowed repositories", (value) => {
    value["control-plane"].scope["allowed-repositories"] = ["acme/target", "ACME/TARGET"];
  }, "control-plane.scope.allowed-repositories must contain unique values"],
  ["non-mapping inventory", (value) => { value["control-plane"].inventory = []; }, "control-plane.inventory must be a mapping"],
  ["unknown inventory properties", (value) => {
    value["control-plane"].inventory.unexpected = 1;
  }, "unknown key control-plane.inventory.unexpected"],
  ["zero scan limits", (value) => {
    value["control-plane"].inventory["max-scan-repositories"] = 0;
  }, "control-plane.inventory.max-scan-repositories must be an integer in 1..100000"],
  ["oversized scan limits", (value) => {
    value["control-plane"].inventory["max-scan-repositories"] = 100001;
  }, "control-plane.inventory.max-scan-repositories must be an integer in 1..100000"],
  ["zero cell counts", (value) => {
    value["control-plane"].inventory["cell-count"] = 0;
  }, "control-plane.inventory.cell-count must be an integer in 1..1000"],
  ["oversized cell counts", (value) => {
    value["control-plane"].inventory["cell-count"] = 1001;
  }, "control-plane.inventory.cell-count must be an integer in 1..1000"],
  ["negative cell indexes", (value) => {
    value["control-plane"].inventory["cell-index"] = -1;
  }, "control-plane.inventory.cell-index must be an integer in >= 0"],
  ["cell indexes outside the cell count", (value) => {
    value["control-plane"].inventory["cell-index"] = 2;
  }, "control-plane.inventory.cell-index must be smaller than cell-count"],
  ["zero batch sizes", (value) => {
    value["control-plane"].inventory["batch-size"] = 0;
  }, "control-plane.inventory.batch-size must be an integer in 1..100000"],
  ["oversized batch sizes", (value) => {
    value["control-plane"].inventory["batch-size"] = 100001;
  }, "control-plane.inventory.batch-size must be an integer in 1..100000"],
  ["negative batch indexes", (value) => {
    value["control-plane"].inventory["batch-index"] = -1;
  }, "control-plane.inventory.batch-index must be an integer in >= 0"],
  ["non-mapping web settings", (value) => {
    value["control-plane"].web = [];
  }, "control-plane.web must be a mapping"],
  ["unknown web settings", (value) => {
    value["control-plane"].web.unexpected = true;
  }, "unknown key control-plane.web.unexpected"],
  ["insecure favicon URLs", (value) => {
    value["control-plane"].web.favicon = "http://example.com/favicon.svg";
  }, "control-plane.web.favicon must be an absolute HTTPS URL or ./ relative path"],
  ["favicon paths with traversal", (value) => {
    value["control-plane"].web.favicon = "./assets/../favicon.svg";
  }, "control-plane.web.favicon must be an absolute HTTPS URL or ./ relative path"],
  ["non-mapping defaults", (value) => { value["control-plane"].defaults = []; }, "control-plane.defaults must be a mapping"],
  ["unknown default properties", (value) => {
    value["control-plane"].defaults.unexpected = true;
  }, "unknown key control-plane.defaults.unexpected"],
  ["invalid default modes", (value) => {
    value["control-plane"].defaults.mode = "preview";
  }, "control-plane.defaults.mode must be review or live"],
  ["invalid default repository limits", (value) => {
    value["control-plane"].defaults["max-repositories"] = 0;
  }, "control-plane.defaults.max-repositories must be an integer in 1..1000"],
  ["invalid default rollout percentages", (value) => {
    value["control-plane"].defaults["rollout-percent"] = 101;
  }, "control-plane.defaults.rollout-percent must be an integer in 1..100"],
  ["negative default budgets", (value) => {
    value["control-plane"].defaults["monthly-ai-credit-budget"] = -1;
  }, "control-plane.defaults.monthly-ai-credit-budget must be an integer in >= 0"],
  ["non-mapping campaigns", (value) => { value["control-plane"].campaigns = []; }, "control-plane.campaigns must be a mapping"],
  ["malformed campaign slugs", (value) => {
    value["control-plane"].campaigns["Not Valid"] = {};
  }, "control-plane.campaigns.Not Valid has an invalid value"],
  ["non-mapping campaign policies", (value) => {
    value["control-plane"].campaigns.operations = [];
  }, "control-plane.campaigns.operations must be a mapping"],
  ["unknown campaign properties", (value) => {
    value["control-plane"].campaigns.operations.unexpected = true;
  }, "unknown key control-plane.campaigns.operations.unexpected"],
  ["non-boolean campaign switches", (value) => {
    value["control-plane"].campaigns.operations.enabled = "true";
  }, "control-plane.campaigns.operations.enabled must be a Boolean"],
  ["unknown campaign icons", (value) => {
    value["control-plane"].campaigns.operations.icon = "invalid";
  }, "control-plane.campaigns.operations.icon must be one of"],
  ["invalid campaign modes", (value) => {
    value["control-plane"].campaigns.operations.mode = "preview";
  }, "control-plane.campaigns.operations.mode must be review or live"],
  ["invalid campaign repository limits", (value) => {
    value["control-plane"].campaigns.operations["max-repositories"] = 1001;
  }, "control-plane.campaigns.operations.max-repositories must be an integer in 1..1000"],
  ["invalid campaign rollout percentages", (value) => {
    value["control-plane"].campaigns.operations["rollout-percent"] = 0;
  }, "control-plane.campaigns.operations.rollout-percent must be an integer in 1..100"],
  ["negative campaign budgets", (value) => {
    value["control-plane"].campaigns.operations["monthly-ai-credit-budget"] = -1;
  }, "control-plane.campaigns.operations.monthly-ai-credit-budget must be an integer in >= 0"],
  ["non-mapping campaign targets", (value) => {
    value["control-plane"].campaigns.operations.targets = [];
  }, "control-plane.campaigns.operations.targets must be a mapping"],
  ["duplicate campaign targets", (value) => {
    value["control-plane"].campaigns.operations.targets = {
      "acme/target": { mode: "live" },
      "ACME/TARGET": { mode: "review" },
    };
  }, "control-plane.campaigns.operations.targets must contain unique repository names"],
  ["malformed target repository names", (value) => {
    value["control-plane"].campaigns.operations.targets = { invalid: { mode: "live" } };
  }, "control-plane.campaigns.operations.targets.invalid has an invalid value"],
  ["non-mapping target policies", (value) => {
    value["control-plane"].campaigns.operations.targets["acme/target"] = [];
  }, "control-plane.campaigns.operations.targets.acme/target must be a mapping"],
  ["unknown target properties", (value) => {
    value["control-plane"].campaigns.operations.targets["acme/target"].unexpected = true;
  }, "unknown key control-plane.campaigns.operations.targets.acme/target.unexpected"],
  ["target policies without modes", (value) => {
    delete value["control-plane"].campaigns.operations.targets["acme/target"].mode;
  }, "control-plane.campaigns.operations.targets.acme/target.mode is required"],
  ["invalid target modes", (value) => {
    value["control-plane"].campaigns.operations.targets["acme/target"].mode = "preview";
  }, "control-plane.campaigns.operations.targets.acme/target.mode must be review or live"],
  ["targets outside allowed repositories", (value) => {
    value["control-plane"].campaigns.operations.targets = { "acme/other": { mode: "live" } };
  }, "campaign target acme/other is outside control-plane.scope.allowed-repositories"],
  ["targets outside allowed owners", (value) => {
    delete value["control-plane"].scope["allowed-repositories"];
    value["control-plane"].campaigns.operations.targets = { "outside/target": { mode: "live" } };
  }, "campaign target outside/target is outside control-plane.scope.allowed-owners"],
  ["non-mapping worker catalogs", (value) => {
    value["control-plane"].campaigns.operations.workers = [];
  }, "control-plane.campaigns.operations.workers must be a mapping"],
  ["malformed worker slugs", (value) => {
    value["control-plane"].campaigns.operations.workers["Not Valid"] = { workflow: "operations-worker" };
  }, "control-plane.campaigns.operations.workers.Not Valid has an invalid value"],
  ["non-mapping worker policies", (value) => {
    value["control-plane"].campaigns.operations.workers.auditor = [];
  }, "control-plane.campaigns.operations.workers.auditor must be a mapping"],
  ["unknown worker properties", (value) => {
    value["control-plane"].campaigns.operations.workers.auditor.unexpected = true;
  }, "unknown key control-plane.campaigns.operations.workers.auditor.unexpected"],
  ["workers without workflows", (value) => {
    delete value["control-plane"].campaigns.operations.workers.auditor.workflow;
  }, "control-plane.campaigns.operations.workers.auditor.workflow has an invalid value"],
  ["malformed worker workflows", (value) => {
    value["control-plane"].campaigns.operations.workers.auditor.workflow = "Not Valid";
  }, "control-plane.campaigns.operations.workers.auditor.workflow has an invalid value"],
  ["duplicate worker workflows", (value) => {
    value["control-plane"].campaigns.operations.workers.secondary = { workflow: "operations-auditor" };
  }, "control-plane.campaigns.operations.workers must declare unique workflow identities"],
  ["non-boolean worker switches", (value) => {
    value["control-plane"].campaigns.operations.workers.auditor.enabled = "true";
  }, "control-plane.campaigns.operations.workers.auditor.enabled must be a Boolean"],
  ["invalid worker mode ceilings", (value) => {
    value["control-plane"].campaigns.operations.workers.auditor["max-mode"] = "preview";
  }, "control-plane.campaigns.operations.workers.auditor.max-mode must be review or live"],
  ["non-mapping publishing policies", (value) => {
    value["control-plane"].publishing = [];
  }, "control-plane.publishing must be a mapping"],
  ["unknown publishing properties", (value) => {
    value["control-plane"].publishing.unexpected = true;
  }, "unknown key control-plane.publishing.unexpected"],
  ["non-boolean publishing switches", (value) => {
    value["control-plane"].publishing.enabled = "true";
  }, "control-plane.publishing.enabled must be a Boolean"],
  ["non-array publishing repositories", (value) => {
    value["control-plane"].publishing["control-repositories"] = "acme/control";
  }, "control-plane.publishing.control-repositories must be an array"],
  ["malformed publishing repositories", (value) => {
    value["control-plane"].publishing["control-repositories"] = ["invalid"];
  }, "control-plane.publishing.control-repositories has an invalid value"],
  ["duplicate publishing repositories", (value) => {
    value["control-plane"].publishing["control-repositories"] = ["acme/control", "ACME/CONTROL"];
  }, "control-plane.publishing.control-repositories must contain unique values"],
  ["non-array publishing reviewers", (value) => {
    value["control-plane"].publishing.reviewers = "octocat";
  }, "control-plane.publishing.reviewers must be an array"],
  ["malformed publishing reviewers", (value) => {
    value["control-plane"].publishing.reviewers = ["-invalid"];
  }, "control-plane.publishing.reviewers has an invalid value"],
  ["duplicate publishing reviewers", (value) => {
    value["control-plane"].publishing.reviewers = ["octocat", "OCTOCAT"];
  }, "control-plane.publishing.reviewers must contain unique values"],
  ["enabled publishing without reviewers", (value) => {
    delete value["control-plane"].publishing.reviewers;
  }, "control-plane.publishing.reviewers is required when publishing is enabled"],
  ["non-mapping target authority", (value) => {
    value["target-authority"] = [];
  }, "target-authority must be a mapping"],
  ["unknown target-authority properties", (value) => {
    value["target-authority"].unexpected = true;
  }, "unknown key target-authority.unexpected"],
  ["target authority without campaigns", (value) => {
    delete value["target-authority"].campaigns;
  }, "target-authority.campaigns must be a mapping"],
  ["non-mapping target-authority campaigns", (value) => {
    value["target-authority"].campaigns = [];
  }, "target-authority.campaigns must be a mapping"],
  ["malformed target-authority campaign slugs", (value) => {
    value["target-authority"].campaigns["Not Valid"] = { authority: "acme/control" };
  }, "target-authority.campaigns.Not Valid has an invalid value"],
  ["non-mapping target campaign policies", (value) => {
    value["target-authority"].campaigns.operations = [];
  }, "target-authority.campaigns.operations must be a mapping"],
  ["unknown target campaign properties", (value) => {
    value["target-authority"].campaigns.operations.unexpected = true;
  }, "unknown key target-authority.campaigns.operations.unexpected"],
  ["target campaigns without authorities", (value) => {
    delete value["target-authority"].campaigns.operations.authority;
  }, "target-authority.campaigns.operations.authority has an invalid value"],
  ["malformed target campaign authorities", (value) => {
    value["target-authority"].campaigns.operations.authority = "invalid";
  }, "target-authority.campaigns.operations.authority has an invalid value"],
];

for (const [name, mutate, expected] of policyViolations) {
  test(`control.mjs validate-policy rejects ${name}`, () => {
    const source = policy();
    mutate(source);
    expectFailure(validate(JSON.stringify(source)), expected);
  });
}

const effectivePolicyViolations = [
  ["unknown roles", { CAO_ROLE: "unknown" }, "role must be orchestrator, worker, or standalone"],
  ["workers without identities", { CAO_ROLE: "worker" }, "worker identity is required"],
  ["orchestrators with worker identities", { CAO_WORKER: "auditor" }, "worker identity is forbidden for orchestrators"],
  ["undeclared workers", { CAO_ROLE: "worker", CAO_WORKER: "unknown" }, "unknown worker: operations/unknown"],
  ["malformed target repositories", { CAO_TARGET_REPOSITORY: "invalid" }, "target_repo must use owner/repository form"],
  ["invalid requested modes", { CAO_REQUESTED_MODE: "preview" }, "safe_output_mode must be review or live"],
  ["requested mode widening", { CAO_REQUESTED_MODE: "live" }, "safe_output_mode exceeds checked-in policy"],
  ["invalid requested repository limits", { CAO_REQUESTED_MAX_REPOSITORIES: "0" }, "max_repositories must be an integer in 1..1000"],
  ["requested repository-limit widening", { CAO_REQUESTED_MAX_REPOSITORIES: "9" }, "max_repositories exceeds checked-in policy"],
  ["invalid requested rollout percentages", { CAO_REQUESTED_ROLLOUT_PERCENT: "0" }, "rollout_percent must be an integer in 1..100"],
  ["requested rollout widening", { CAO_REQUESTED_ROLLOUT_PERCENT: "51" }, "rollout_percent exceeds checked-in policy"],
];

for (const [name, environment, expected] of effectivePolicyViolations) {
  test(`control.mjs resolve-policy rejects ${name}`, () => {
    expectFailure(run(["resolve-policy", "-"], JSON.stringify(policy()), environment), expected);
  });
}

test("control.mjs control-settings rejects malformed control repository identity", () => {
  expectFailure(
    run(["control-settings", "-"], JSON.stringify(policy()), { GITHUB_REPOSITORY: "invalid" }),
    "GITHUB_REPOSITORY has an invalid value",
  );
});

test("control.mjs control-settings requires a control-plane policy", () => {
  const source = policy();
  delete source["control-plane"];
  expectFailure(run(["control-settings", "-"], JSON.stringify(source)), "control-plane is required");
});

test("control.mjs authority requires a declared campaign", () => {
  expectFailure(
    run(["authority", "-", "unknown"], JSON.stringify(policy())),
    "target authority does not declare campaign unknown",
  );
});

test("control.mjs rejects missing policy files", () => {
  expectFailure(run(["validate-policy", join(root, "missing-policy.json")]), "ENOENT");
});

test("control.mjs rejects unsupported command lines", () => {
  expectFailure(run([]), "usage: control.mjs");
});
