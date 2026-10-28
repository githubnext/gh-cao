import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { inventoryApi } from "../helpers/inventory-api.mjs";
import { controlPolicy, root } from "../helpers/control-precompute.mjs";
import { condition, interpolate } from "../helpers/actions-runtime/expressions.mjs";
import { credentialFailure, inventoryScope, readProfile, verifyCredentials, withInventoryCredentials } from "../../.github/workflows/shared/inventory-credentials.mjs";
import { main } from "../../.github/workflows/shared/control.mjs";

const shared = parse(readFileSync(join(root, ".github/workflows/shared/control.md"), "utf8").split("---")[1]);
const steps = shared.jobs["pre-activation"]["pre-steps"];
const precomputeStep = steps.find(step => step.id === "cao_precompute");
const policy = (options = {}) => JSON.parse(controlPolicy({
  scope: { "allowed-repositories": ["acme/control", "acme/private-a", "acme/private-b"] },
  campaignPolicy: { mode: "live", "max-repositories": 3 },
  workerPolicy: { "max-mode": "live" },
  ...options,
}));
const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;

async function scenario({
  document = policy(),
  target = "acme/private-a",
  mode = "app",
  defaultTarget = "",
  eventName = "workflow_dispatch",
  env: extraEnv = {},
  api: options = {},
  role = "orchestrator",
  excludeControl = false,
  requestedMode = "live",
  mutateHandoff,
} = {}) {
  const api = await inventoryApi({ policy: document, ...options });
  const directory = mkdtempSync(join(tmpdir(), "cao-inventory-credentials-"));
  const before = { ...process.env }, beforeExit = process.exitCode;
  const globals = Object.fromEntries(["github", "inventoryGithub", "workflowGithub", "core", "getOctokit", "context"].map(name => [name, globalThis[name]]));
  const output = join(directory, "outputs");
  const summary = join(directory, "summary");
  const workflow = role === "direct" ? "direct-audit" : role === "standalone" ? "fleet-audit" : "__none__";
  const campaign = workflow === "__none__" ? "dependabot" : "__none__";
  writeFileSync(output, "");
  writeFileSync(summary, "");
  const context = {
    status: "success",
    env: { CAO_ROLE: role },
    vars: { GH_AW_GITHUB_AUTH_MODE: mode, GH_AW_GITHUB_READ_APP_ID: "read-app" },
    secrets: { GH_AW_GITHUB_READ_APP_PRIVATE_KEY: api.privatePem },
    inputs: {
      target_repo: target, safe_output_mode: requestedMode,
      ...(role === "worker" ? {
        safe_output_repo: target,
        ...(requestedMode === "debug" ? {} : {
          correlation_id: "123-1", central_repo: "acme/control",
          control_plane_run_url: "https://github.com/acme/control/actions/runs/123",
        }),
      } : {}),
    },
    github: {
      token: "workflow-token", repository: "acme/control", workflow_sha: api.sha,
      aw: { "import-inputs": {
        campaign, workflow, role, worker: role === "worker" ? "update-planner" : "__none__",
        dispatch_max: 3, orchestrator_credits: 0, worker_credits_per_target: 0,
        inventory_exclude_control: excludeControl,
      } },
    },
    steps: {
      cao_admission: { outputs: { authorized: "true" } },
      cao_control_source: { outputs: { runtime: join(root, ".github/workflows/shared/control.mjs") } },
      cao_pre_activation_app_token: { outputs: { token: "control-token" } },
    },
  };
  const actions = {
    github: api.getOctokit("workflow-token"),
    getOctokit: api.getOctokit,
    core: { info() {}, setSecret() {} },
    context: { payload: { sender: { login: "developer", type: "User" }, inputs: context.inputs } },
  };
  try {
    Object.assign(process.env, {
      GITHUB_ACTIONS: "true", GITHUB_REPOSITORY: "acme/control",
      GITHUB_SERVER_URL: "https://github.com", GITHUB_WORKFLOW_SHA: api.sha,
      GITHUB_EVENT_NAME: eventName, GITHUB_ACTOR: "developer", GITHUB_TRIGGERING_ACTOR: "developer",
      GITHUB_WORKFLOW_REF: `acme/control/.github/workflows/${workflow === "__none__" ? "dependabot" : workflow}.lock.yml@refs/heads/main`,
      GITHUB_RUN_ID: "123", GITHUB_RUN_ATTEMPT: "1",
      GITHUB_OUTPUT: output, GITHUB_STEP_SUMMARY: summary, RUNNER_TEMP: directory,
      CAO_CAMPAIGN: campaign, CAO_WORKFLOW: workflow, CAO_ROLE: role, CAO_WORKER: role === "worker" ? "update-planner" : "",
      CAO_TARGET_REPOSITORY: target, CAO_REQUESTED_MODE: requestedMode, CAO_SAFE_OUTPUT_REPOSITORY: target,
      CAO_DEFAULT_TARGET_REPOSITORY: defaultTarget,
      CAO_REQUESTED_SAFE_OUTPUT_REPOSITORY: context.inputs.safe_output_repo || "",
      CAO_CORRELATION_ID: context.inputs.correlation_id || "",
      CAO_CENTRAL_REPOSITORY: context.inputs.central_repo || "",
      CAO_CONTROL_PLANE_RUN_URL: context.inputs.control_plane_run_url || "",
    });
    process.exitCode = 0;
    await main(actions, ["admit"]);
    const admission = JSON.parse(readFileSync(join(directory, "cao/admission.json"), "utf8"));
    if (admission.authorized) {
      if (mutateHandoff) mutateHandoff(context, directory);
      Object.assign(process.env, Object.fromEntries(Object.entries(precomputeStep.env)
        .map(([key, value]) => [key, String(interpolate(value, context) ?? "")])), extraEnv);
      // Execute the actual github-script body with real Octokit clients, not a gh CLI mock.
      await new AsyncFunction("require", "core", "github", "context", "exec", "io", "getOctokit", precomputeStep.with.script)(
        createRequire(import.meta.url), actions.core, actions.github, actions.context, {}, {}, actions.getOctokit,
      ).catch(error => {
        assert.match(error.message, /control.mjs exited with code 1/);
      });
    }
    const precompute = admission.authorized ? JSON.parse(readFileSync("/tmp/gh-aw/agent/control-precompute.json", "utf8")) : null;
    return {
      admission, precompute, exitCode: process.exitCode,
      output: readFileSync(output, "utf8"), summary: readFileSync(summary, "utf8"),
      calls: api.calls, minted: api.minted, revoked: api.revoked,
    };
  } finally {
    for (const name of Object.keys(process.env)) if (!(name in before)) delete process.env[name];
    Object.assign(process.env, before);
    for (const [name, value] of Object.entries(globals)) {
      if (value === undefined) delete globalThis[name]; else globalThis[name] = value;
    }
    process.exitCode = beforeExit;
    await api.close();
    rmSync(directory, { recursive: true, force: true });
  }
}

test("actual github-script uses target-scoped inventory tokens and control-only source reads", async () => {
  const result = await scenario();
  assert.equal(result.exitCode, 0);
  assert.equal(result.admission.authorized, true);
  assert.equal(result.precompute.activation_ready, true);
  assert.deepEqual(result.precompute.candidate_repositories.map(repo => repo.full_name), ["acme/private-a"]);
  assert.deepEqual(result.minted.map(entry => entry.repositories), [["acme/private-a"]]);
  assert.deepEqual(result.revoked, ["inventory-1"]);
  const source = result.calls.filter(call => call.path.includes("/contents/") || call.path.endsWith("/actions/workflows"));
  assert.ok(source.every(call => ["workflow-token", "control-token"].includes(call.token)));
  assert.ok(source.filter(call => call.path.includes("/contents/")).every(call => call.query.ref === "1".repeat(40)));
  assert.ok(source.filter(call => call.path.endsWith("/actions/workflows")).every(call => call.token === "workflow-token"));
  assert.equal(result.calls.find(call => call.path === "/repos/acme/private-a").token, "inventory-1");
});

test("scheduled exact fleet excludes the control repository before token minting", async () => {
  const result = await scenario({
    target: "", excludeControl: true, document: policy({ inventory: { "max-scan-repositories": 2 } }),
  });
  assert.equal(result.exitCode, 0);
  assert.deepEqual(result.minted.map(entry => entry.repositories), [["acme/private-a", "acme/private-b"]]);
  assert.deepEqual(result.precompute.candidate_repositories.map(repo => repo.full_name), ["acme/private-a", "acme/private-b"]);
  assert.ok(!result.calls.some(call => /\/(?:orgs|users)\//.test(call.path)));
  assert.equal(result.precompute.effective_max_repos, 2);
  const explicit = await scenario({ document: policy({ inventory: { "max-scan-repositories": 1 } }) });
  assert.equal(explicit.precompute.activation_ready, true, "only the selected target consumes the scan bound");
});

test("repository-local scheduled inventory avoids unrelated PAT mappings", async () => {
  const source = parse(readFileSync(join(root, ".github/workflows/self-care.md"), "utf8").split("---")[1]);
  const defaultTarget = interpolate(source.env.CAO_DEFAULT_TARGET_REPOSITORY, {
    github: { repository: "acme/control" },
  });
  assert.equal(defaultTarget, "acme/control");
  const env = {
    CAO_CONTROL_READ_PAT: "control-pat", CAO_INVENTORY_READ_PAT: "control-pat",
    CAO_READ_PAT_REPOSITORIES: JSON.stringify({
      "acme/control": "CONTROL_PAT", "acme/private-a": "TARGET_PAT", "acme/private-b": "OTHER_PAT",
    }),
  };
  const result = await scenario({ target: "", defaultTarget, eventName: "schedule", mode: "pat", env });
  assert.equal(result.exitCode, 0);
  assert.equal(result.admission.authorized, true);
  assert.equal(result.precompute.activation_ready, true);
  assert.deepEqual(result.precompute.candidate_repositories.map(repo => repo.full_name), ["acme/control"]);
  assert.ok(!result.calls.some(call => /\/repos\/acme\/private-/.test(call.path)));
  assert.equal(result.minted.length, 0);
});

test("shared control keeps scheduled PAT orchestrators repository-local", async () => {
  const defaultTarget = ({ eventName = "schedule", authMode = "pat" } = {}) =>
    interpolate(shared.env.CAO_DEFAULT_TARGET_REPOSITORY, {
      github: {
        repository: "acme/control",
        event_name: eventName,
      },
      vars: { GH_AW_GITHUB_AUTH_MODE: authMode },
    });
  assert.equal(defaultTarget(), "acme/control");
  assert.equal(defaultTarget({ eventName: "workflow_dispatch" }), "");
  assert.equal(defaultTarget({ authMode: "app" }), "");
  const env = {
    CAO_CONTROL_READ_PAT: "control-pat", CAO_INVENTORY_READ_PAT: "control-pat",
    CAO_READ_PAT_REPOSITORIES: JSON.stringify({
      "acme/control": "CONTROL_PAT", "acme/private-a": "TARGET_PAT", "acme/private-b": "OTHER_PAT",
    }),
  };
  const result = await scenario({
    target: "", defaultTarget: defaultTarget(), eventName: "schedule", mode: "pat", env,
  });
  assert.equal(result.exitCode, 0);
  assert.equal(result.precompute.activation_ready, true);
  assert.deepEqual(result.precompute.candidate_repositories.map(repo => repo.full_name), ["acme/control"]);
  assert.ok(!result.calls.some(call => /\/repos\/acme\/private-/.test(call.path)));
});

test("Dependabot scheduled inventory stays repository-local without changing manual discovery", async () => {
  const source = parse(readFileSync(join(root, ".github/workflows/dependabot.md"), "utf8").split("---")[1]);
  const defaultTarget = eventName => interpolate(source.env.CAO_DEFAULT_TARGET_REPOSITORY, {
    github: { repository: "acme/control", event_name: eventName },
  });
  assert.equal(defaultTarget("schedule"), "acme/control");
  assert.equal(defaultTarget("workflow_dispatch"), "");
  const env = {
    CAO_CONTROL_READ_PAT: "control-pat", CAO_INVENTORY_READ_PAT: "control-pat",
    CAO_READ_PAT_REPOSITORIES: JSON.stringify({
      "acme/control": "CONTROL_PAT", "acme/private-a": "TARGET_PAT", "acme/private-b": "OTHER_PAT",
    }),
  };
  const result = await scenario({
    target: "", defaultTarget: defaultTarget("schedule"), eventName: "schedule", mode: "pat", env,
  });
  assert.equal(result.exitCode, 0);
  assert.equal(result.admission.authorized, true);
  assert.equal(result.precompute.activation_ready, true);
  assert.deepEqual(result.precompute.candidate_repositories.map(repo => repo.full_name), ["acme/control"]);
  assert.ok(!result.calls.some(call => /\/repos\/acme\/private-/.test(call.path)));
  assert.equal(result.minted.length, 0);
});

test("scheduled mixed-PAT fleet blocks activation before target reads with actionable diagnostics", async () => {
  const result = await scenario({
    target: "", eventName: "schedule", mode: "pat",
    env: {
      CAO_CONTROL_READ_PAT: "control-pat", CAO_INVENTORY_READ_PAT: "control-pat",
      CAO_LEGACY_READ_TOKEN: "never-disclose-legacy", CAO_WORKFLOW_TOKEN: "workflow-token",
      CAO_READ_PAT_REPOSITORIES: JSON.stringify({
        "acme/control": "CONTROL_PAT", "acme/private-a": "TARGET_PAT", "acme/private-b": "TARGET_PAT",
      }),
    },
  });
  assert.equal(result.admission.authorized, true);
  assert.equal(result.exitCode, 1);
  assert.equal(result.precompute.authorized, true);
  assert.equal(result.precompute.activation_ready, false);
  assert.equal(result.precompute.inventory_status, "incomplete");
  assert.equal(result.precompute.reason, "pat-fleet-unsupported");
  assert.equal(result.precompute.effective_max_repos, 0);
  assert.deepEqual(result.precompute.candidate_repositories, []);
  assert.match(result.output, /authorized=false/);
  const diagnostic = result.precompute.diagnostics.find(entry => entry.code === "pat-fleet-unsupported");
  assert.match(diagnostic.message, /acme\/control.*acme\/private-a.*GH_AW_GITHUB_READ_PAT_REPOSITORIES/);
  assert.match(diagnostic.message, /GH_AW_GITHUB_AUTH_MODE=app/);
  assert.match(diagnostic.message, /explicit in-scope target_repo/);
  assert.ok(diagnostic.message.includes("gh cao setup --repo acme/control"));
  assert.ok(diagnostic.message.includes("gh workflow run cao-validate.yml --repo acme/control -f verify-credentials=true"));
  assert.match(diagnostic.message, /docs\/authentication\.md#recover-from-pat-fleet-unsupported/);
  assert.match(diagnostic.message, /max_repos limits dispatch, not required inventory/);
  assert.ok(result.summary.includes(diagnostic.message));
  assert.match(result.summary, /Activation and dispatch: \*\*blocked\*\*/);
  assert.doesNotMatch(JSON.stringify(result.precompute) + result.summary, /control-pat|never-disclose-legacy|workflow-token/);
  assert.ok(!result.calls.some(call => /\/repos\/acme\/private-/.test(call.path)));
  assert.equal(result.minted.length, 0);
});

test("PAT inventory supports a shared-secret fleet and an explicit target with a separate control PAT", async () => {
  const mapping = {
    "acme/control": "CONTROL_PAT", "acme/private-a": "TARGET_PAT", "acme/private-b": "TARGET_PAT",
  };
  const env = {
    CAO_CONTROL_READ_PAT: "control-pat", CAO_INVENTORY_READ_PAT: "inventory-pat",
    CAO_READ_PAT_REPOSITORIES: JSON.stringify(mapping),
  };
  const targeted = await scenario({ mode: "pat", env });
  assert.equal(targeted.exitCode, 0);
  assert.equal(targeted.precompute.activation_ready, true);
  assert.deepEqual(targeted.precompute.candidate_repositories.map(repo => repo.full_name), ["acme/private-a"]);
  assert.equal(targeted.calls.find(call => call.path === "/repos/acme/private-a").token, "inventory-pat");

  const fleet = await scenario({
    target: "", eventName: "schedule", mode: "pat",
    env: {
      ...env, CAO_CONTROL_READ_PAT: "inventory-pat",
      CAO_READ_PAT_REPOSITORIES: JSON.stringify({ ...mapping, "acme/control": "TARGET_PAT" }),
    },
  });
  assert.equal(fleet.exitCode, 0);
  assert.equal(fleet.precompute.activation_ready, true);
  assert.deepEqual(fleet.precompute.candidate_repositories.map(repo => repo.full_name), Object.keys(mapping));
  assert.ok(!fleet.calls.some(call => /\/(?:orgs|users)\//.test(call.path)));
  assert.equal(fleet.minted.length, 0);
});

test("explicit inventory targets take precedence over workflow defaults and remain policy checked", async () => {
  const result = await scenario({ defaultTarget: "acme/control" });
  assert.equal(result.precompute.activation_ready, true);
  assert.deepEqual(result.precompute.candidate_repositories.map(repo => repo.full_name), ["acme/private-a"]);
  const denied = await scenario({ target: "", defaultTarget: "acme/not-enrolled" });
  assert.equal(denied.precompute.activation_ready, false);
  assert.equal(denied.minted.length, 0);
});

for (const [name, options, reason] of [
  ["missing App key", { env: { CAO_READ_APP_PRIVATE_KEY: "" } }, "app-credential-unavailable"],
  ["missing control App token", { env: { CAO_CONTROL_READ_TOKEN: "", CAO_LEGACY_READ_TOKEN: "inventory-pat" } }, "control-credential-unavailable"],
  ["insufficient App permissions", { api: { mintFailure: 403 } }, "permission-denied"],
  ["inaccessible target", { api: { failures: { "acme/private-a": { status: 404 } } } }, "repository-inaccessible"],
  ["expired credential", { api: { failures: { "acme/private-a": { status: 401 } } } }, "credential-rejected"],
  ["rate capacity", { api: { failures: { "acme/private-a": { status: 403, headers: { "x-ratelimit-remaining": "0" } } } } }, "api-capacity"],
  ["service failure", { api: { failures: { "acme/private-a": { status: 503 } } } }, "service-unavailable"],
  ["workflow token cannot read private targets", { mode: "workflow-token" }, "repository-inaccessible"],
  ["no installation reach", { api: { repositories: ["acme/control"] } }, "repository-inaccessible"],
  ["malformed metadata", { api: { metadata: { "acme/private-a": { id: null } } } }, "inventory-response-invalid"],
  ["renamed metadata", { api: { metadata: { "acme/private-a": { full_name: "acme/other" } } } }, "inventory-response-invalid"],
  ["failed cleanup", { api: { revokeFailure: 503 } }, "credential-cleanup-failed"],
]) {
  test(`inventory blocks activation and retains admission for ${name}`, async () => {
    const result = await scenario(options);
    assert.equal(result.admission.authorized, true);
    assert.equal(result.precompute.authorized, true);
    assert.equal(result.precompute.activation_ready, false);
    assert.equal(result.precompute.inventory_status, "incomplete");
    assert.equal(result.precompute.reason, reason);
    assert.deepEqual(result.precompute.candidate_repositories, []);
    assert.match(result.output, /authorized=false/);
    assert.match(result.summary, /Activation and dispatch: \*\*blocked\*\*/);
    assert.doesNotMatch(JSON.stringify(result.precompute) + result.summary, /never-disclose|inventory-\d|BEGIN PRIVATE KEY/);
    assert.equal(result.exitCode, 1);
  });
}

test("a later fleet failure never permits partial dispatch", async () => {
  const result = await scenario({ target: "", api: { failures: { "acme/private-b": { status: 404 } } } });
  assert.equal(result.precompute.effective_max_repos, 0);
  assert.deepEqual(result.precompute.candidate_repositories, []);
  assert.deepEqual(result.revoked, ["inventory-1"]);
});

test("cleanup failure retains the original inventory failure and both sanitized diagnostics", async () => {
  const result = await scenario({ api: { failures: { "acme/private-a": { status: 404 } }, revokeFailure: 503 } });
  assert.equal(result.precompute.reason, "repository-inaccessible");
  assert.deepEqual(result.precompute.diagnostics.map(diagnostic => diagnostic.code), ["repository-inaccessible", "credential-cleanup-failed"]);
  assert.doesNotMatch(JSON.stringify(result.precompute), /never-disclose/);
});

test("repository redirects cannot forward an inventory token into the control boundary", async () => {
  const result = await scenario({ api: { failures: {
    "acme/private-a": { status: 301, headers: { location: "/repos/acme/control" } },
  } } });
  assert.equal(result.precompute.activation_ready, false);
  assert.ok(!result.calls.some(call => call.path === "/repos/acme/control" && call.token === "inventory-1"));
});

for (const role of ["standalone", "direct"]) {
  test(`actual github-script isolates private ${role} inventory and retains its output contract`, async () => {
    const document = policy();
    document["control-plane"][role === "direct" ? "operations" : "workflows"] = {
      [role === "direct" ? "direct-audit" : "fleet-audit"]: { mode: "live", "max-repositories": 2 },
    };
    const result = await scenario({ role, document });
    assert.equal(result.exitCode, 0);
    assert.equal(result.precompute.activation_ready, true);
    assert.equal(result.precompute.control_role, role);
    assert.deepEqual(result.minted.map(entry => entry.repositories), [["acme/private-a"]]);
    assert.equal(result.precompute.candidate_repositories[0].safe_output_repo, "acme/private-a");
    assert.deepEqual(result.precompute.worker_workflows, []);
    if (role === "direct") {
      const tampered = await scenario({ role, document, mutateHandoff(context, directory) {
        const path = join(directory, "cao/effective-policy.json");
        const effective = JSON.parse(readFileSync(path, "utf8"));
        effective.authority_envelope.binding.policySha = "2".repeat(40);
        writeFileSync(path, JSON.stringify(effective));
      } });
      assert.equal(tampered.precompute.activation_ready, false);
      assert.equal(tampered.precompute.reason, "precompute-contract-invalid");
      assert.equal(tampered.minted.length, 0);
    }
  });
}

test("a genuine empty partition is complete, not a credential failure", async () => {
  const result = await scenario({
    target: "", document: policy({ inventory: { "cell-count": 10, "cell-index": 9 } }),
  });
  assert.equal(result.exitCode, 0);
  assert.equal(result.precompute.inventory_status, "complete");
  assert.equal(result.precompute.activation_ready, true);
  assert.equal(result.precompute.effective_max_repos, 0);
  assert.deepEqual(result.precompute.candidate_repositories, []);
});

test("unenrolled targets and unsupported owner discovery do not mint inventory tokens", async () => {
  for (const options of [
    { target: "acme/not-enrolled" },
    { target: "", document: policy({ scope: { "allowed-repositories": [] } }) },
    { target: "acme/control", excludeControl: true },
  ]) {
    const result = await scenario(options);
    assert.equal(result.precompute.activation_ready, false);
    assert.equal(result.minted.length, 0);
    assert.ok(!result.calls.some(call => call.path.endsWith("/installation")));
  }
});

test("disabled campaigns remain denied before credential resolution", async () => {
  const result = await scenario({ document: policy({ campaignPolicy: { enabled: false } }) });
  assert.equal(result.admission.authorized, false);
  assert.equal(result.precompute, null);
  assert.equal(result.minted.length, 0);
});

test("scope validation rejects malformed enrollment and never broadens authentication profiles", () => {
  assert.throws(() => inventoryScope({ allowed_owners: ["acme"], allowed_repositories: ["acme/*"] }), /exact OWNER/);
  assert.throws(() => inventoryScope({ allowed_owners: ["acme"], allowed_repositories: ["acme/.."] }), /exact OWNER/);
  assert.throws(() => readProfile({ CAO_AUTH_MODE: "other", CAO_LEGACY_READ_TOKEN: "fallback" }), /must select/);
  assert.equal(credentialFailure({ status: 404 }).code, "repository-inaccessible");
  assert.match(credentialFailure({ status: 404 }).message, /404 alone does not establish/);
});

test("worker live routing and staged human debug preserve their existing envelope contracts", async () => {
  const live = await scenario({ role: "worker" });
  assert.equal(live.exitCode, 0);
  assert.equal(live.precompute.safe_output_repo, "acme/private-a");
  assert.equal(live.precompute.correlation_id, "123-1");
  assert.equal(live.minted.length, 0, "workers do not mint fleet inventory credentials");
  const debug = await scenario({ role: "worker", requestedMode: "debug" });
  assert.equal(debug.exitCode, 0);
  assert.equal(debug.precompute.safe_outputs_staged, true);
  assert.equal(debug.precompute.launch_kind, "manual-debug");
  assert.equal(debug.precompute.correlation_id, "");
  const invalid = await scenario({ role: "worker", mutateHandoff(context) { context.inputs.correlation_id = "other"; } });
  assert.equal(invalid.precompute.activation_ready, false);
  assert.equal(invalid.precompute.reason, "precompute-contract-invalid");
  assert.match(invalid.precompute.repo_error, /correlation_id/);
});

test("missing worker enablement and mode ceilings cannot borrow inventory credentials", async () => {
  for (const options of [
    { role: "worker", document: policy({ workerPolicy: { enabled: false } }) },
    { document: policy({ campaignPolicy: { mode: "review" } }) },
  ]) {
    const result = await scenario(options);
    assert.equal(result.admission.authorized, false);
    assert.equal(result.minted.length, 0);
  }
});

test("App inventory scopes group owners and split tokens without an installation-wide grant", async () => {
  const repositories = ["acme/control", ...Array.from({ length: 501 }, (_, index) => `acme/private-${index}`), "octo/other"];
  const api = await inventoryApi({ policy: policy(), repositories });
  try {
    await withInventoryCredentials({
      repositories: repositories.slice(1), controlRepository: "acme/control", getOctokit: api.getOctokit,
      core: { setSecret() {} },
      env: {
        CAO_AUTH_MODE: "app", CAO_CONTROL_READ_TOKEN: "control-token",
        CAO_READ_APP_ID: "read-app", CAO_READ_APP_PRIVATE_KEY: api.privatePem,
      },
    }, async ({ inventory }) => {
      await inventory.request("GET /repos/acme/private-500");
      await inventory.request("GET /repos/octo/other");
    });
    assert.deepEqual(api.minted.map(entry => entry.repositories.length), [500, 1, 1]);
    assert.deepEqual(api.minted.flatMap(entry => entry.repositories), repositories.slice(1));
    assert.equal(api.revoked.length, 3);
  } finally {
    await api.close();
  }
});

test("PAT resolution separates control from target and rejects missing, mixed or unavailable mappings", async () => {
  const env = {
    CAO_AUTH_MODE: "pat", CAO_CONTROL_READ_PAT: "control-pat", CAO_INVENTORY_READ_PAT: "inventory-pat",
    CAO_INVENTORY_PAT_REPOSITORY: "acme/private-a",
    CAO_READ_PAT_REPOSITORIES: JSON.stringify({
      "acme/control": "CONTROL_PAT", "acme/private-a": "TARGET_PAT", "acme/private-b": "OTHER_PAT",
    }),
  };
  const api = await inventoryApi({ policy: policy() });
  try {
    const invoke = (repositories, changes = {}, operation = async () => {}) => withInventoryCredentials({
      repositories, controlRepository: "acme/control", getOctokit: api.getOctokit,
      core: { setSecret() {} }, env: { ...env, ...changes },
    }, operation);
    await invoke(["acme/private-a"], {}, async ({ control, inventory, workflow }) => {
      await control.request("GET /repos/acme/control/actions/workflows");
      await inventory.request("GET /repos/acme/private-a");
      await workflow.request("GET /repos/acme/control/actions/workflows");
    });
    assert.deepEqual(api.calls.filter(call => call.path.endsWith("/actions/workflows")).map(call => call.token),
      ["control-pat", "workflow-token"]);
    assert.equal(api.calls.find(call => call.path === "/repos/acme/private-a").token, "inventory-pat");
    await assert.rejects(invoke(["acme/private-b"]), /multiple PAT secrets/);
    await assert.rejects(invoke(["acme/private-a"], { CAO_INVENTORY_READ_PAT: "", CAO_WORKFLOW_TOKEN: "workflow-token" }), /credential is unavailable/);
    await assert.rejects(invoke(["acme/private-a"], { CAO_READ_PAT_REPOSITORIES: "{}" }), /mapping/);
    await assert.rejects(invoke(["acme/private-a"], { CAO_READ_PAT_REPOSITORIES: "[" }), /repository-to-secret-name/);
    assert.equal(api.minted.length, 0);
  } finally {
    await api.close();
  }
});

test("write prerequisite failures and token profiles remain explicitly incomplete, not publication proof", async () => {
  for (const mode of ["app", "workflow-token"]) {
    const api = await inventoryApi({ policy: policy(), outputPermission: "read" });
    try {
      const report = await verifyCredentials({
        settings: { allowed_owners: ["acme"], allowed_repositories: ["acme/control"] },
        controlRepository: "acme/control", workflowSha: api.sha, getOctokit: api.getOctokit,
        core: { setSecret() {} },
        env: {
          CAO_AUTH_MODE: mode, CAO_CONTROL_READ_TOKEN: "control-token", CAO_WORKFLOW_TOKEN: "workflow-token",
          CAO_READ_APP_ID: "read-app", CAO_READ_APP_PRIVATE_KEY: api.privatePem,
          CAO_WRITE_APP_ID: "write-app", CAO_WRITE_APP_PRIVATE_KEY: api.privatePem,
        },
      });
      assert.equal(report.ok, false);
      assert.equal(report.output_checks[0].code, mode === "app" ? "output-permission-missing" : "output-permission-unverified");
      assert.equal(report.issue_publication, "unverified");
    } finally {
      await api.close();
    }
  }
});

test("read-only verification reuses runtime credentials and checks write-App prerequisites without publication", async () => {
  const api = await inventoryApi({ policy: policy() });
  try {
    const report = await verifyCredentials({
      settings: { allowed_owners: ["acme"], allowed_repositories: ["acme/private-a", "acme/private-b"] },
      controlRepository: "acme/control", workflowSha: api.sha, getOctokit: api.getOctokit,
      core: { setSecret() {} },
      env: {
        CAO_AUTH_MODE: "app", CAO_CONTROL_READ_TOKEN: "control-token",
        CAO_READ_APP_ID: "read-app", CAO_READ_APP_PRIVATE_KEY: api.privatePem,
        CAO_WRITE_APP_ID: "write-app", CAO_WRITE_APP_PRIVATE_KEY: api.privatePem,
        CAO_VERIFY_OUTPUT_MODE: "live",
      },
    });
    assert.equal(report.ok, true);
    assert.equal(report.issue_publication, "unverified");
    assert.equal(report.output_checks.length, 2);
    assert.ok(api.calls.every(call => call.method === "GET" || call.path === "/installation/token" || /\/access_tokens$/.test(call.path)));
    assert.ok(api.calls.filter(call => call.method === "POST").every(call => call.token === "app:read-app"));
    assert.equal(api.revoked.length, api.minted.length);
  } finally {
    await api.close();
  }
});

test("inventory facade cannot use control, unenrolled, or write endpoints", async () => {
  const api = await inventoryApi({ policy: policy() });
  try {
    await withInventoryCredentials({
      repositories: ["acme/private-a"], controlRepository: "acme/control", getOctokit: api.getOctokit,
      core: { setSecret() {} },
      env: {
        CAO_AUTH_MODE: "app", CAO_CONTROL_READ_TOKEN: "control-token",
        CAO_READ_APP_ID: "read-app", CAO_READ_APP_PRIVATE_KEY: api.privatePem,
      },
    }, async ({ inventory }) => {
      for (const route of ["GET /repos/acme/control", "GET /repos/acme/not-enrolled", "POST /repos/acme/private-a/issues"]) {
        await assert.rejects(inventory.request(route), /outside its exact admitted/);
      }
    });
  } finally {
    await api.close();
  }
});

test("credential verification is opt-in and cannot reach the validation issue reporter", () => {
  const workflow = parse(readFileSync(join(root, ".github/workflows/cao-validate.yml"), "utf8"));
  assert.equal(workflow.on.workflow_dispatch.inputs["verify-credentials"].default, false);
  assert.match(workflow.jobs.credentials.if, /github.event_name == 'workflow_dispatch'.*inputs.verify-credentials == true/);
  assert.ok(Object.values(workflow.jobs.credentials.permissions).every(permission => permission === "read"));
  assert.match(workflow.jobs.report.if, /inputs.verify-credentials != true/);
  const token = steps.find(step => step.id === "cao_pre_activation_app_token");
  assert.equal(token.with.repositories, "${{ github.event.repository.name }}");
  assert.match(token.if, /cao_admission.outputs.authorized == 'true'/);
  assert.ok(steps.indexOf(token) > steps.findIndex(step => step.id === "cao_admission"));
  assert.ok(condition(precomputeStep.if, { status: "success", steps: { cao_admission: { outputs: { authorized: "true" } } } }));
});

test("verification wiring selects only the mapped control PAT without serializing unrelated secrets", () => {
  const source = readFileSync(join(root, ".github/workflows/cao-validate.yml"), "utf8");
  const workflow = parse(source);
  const step = workflow.jobs.credentials.steps.find(step => step.name === "Verify runtime credential boundaries");
  for (const mode of ["pat", "app", "workflow-token"]) {
    const env = Object.fromEntries(Object.entries(step.env).map(([name, value]) => [name, interpolate(value, {
      vars: {
        GH_AW_GITHUB_AUTH_MODE: mode,
        GH_AW_GITHUB_READ_PAT_REPOSITORIES: JSON.stringify({ "acme/control": "CONTROL_PAT", "acme/target": "TARGET_PAT" }),
      },
      secrets: { CONTROL_PAT: "control-pat", TARGET_PAT: "never-expose-target", UNRELATED: "never-expose-unrelated" },
      github: { repository: "acme/control", token: "workflow-token" },
      steps: { "control-token": { outputs: { token: "control-token" } } },
      inputs: {},
    })]));
    assert.equal(env.CAO_CONTROL_READ_PAT, mode === "pat" ? "control-pat" : "");
    assert.equal(env.CAO_INVENTORY_READ_PAT, mode === "pat" ? "control-pat" : "");
    assert.doesNotMatch(JSON.stringify(env), /never-expose/);
  }
  assert.doesNotMatch(source, /toJSON\s*\(\s*secrets\s*\)/i);
});
