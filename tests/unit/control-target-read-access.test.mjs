import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readdirSync } from "node:fs";
import test from "node:test";
import { parse } from "yaml";
import { FINE_GRAINED_PAT_PROFILES } from "../../activity/authentication.mjs";
import { APP_PROFILES } from "../../.github/workflows/shared/github-apps.mjs";
import { targetReadCredential, workflow, workflowsDirectory } from "./workflow-contract.helpers.mjs";

// Control-plane contract: every worker reads its dispatched target's metadata
// (Dependabot alerts, code scanning, secret scanning, checks, statuses, ...)
// through the shared target-scoped read credential minted from the read App.

const TARGET_READ_CREDENTIAL = targetReadCredential;
const SECURITY_ALERT_ROUTE = /\/(?:dependabot|code-scanning|secret-scanning)\/alerts\b/;
const TOOLSET_PERMISSIONS = new Map([
  ["dependabot", "read_vulnerability_alerts"],
  ["code_security", "read_security_events"],
  ["secret_protection", "read_secret_scanning_alerts"],
]);

function frontmatter(source, name) {
  const match = /^---\n([\s\S]*?)\n---/.exec(source)?.[1];
  assert.ok(match, `${name} must have frontmatter`);
  return parse(match);
}

const controlSource = workflow("shared/control.md");
const control = frontmatter(controlSource, "shared/control.md");
const readInputs = Object.keys(control["import-schema"])
  .filter((input) => input.startsWith("read_") && input !== "read_repository")
  .sort();
const permissionName = (input) => input.slice("read_".length);
const actionPermission = (input) => `permission-${permissionName(input).replaceAll("_", "-")}`;

const controlledWorkflows = readdirSync(workflowsDirectory)
  .filter((name) => name.endsWith(".md"))
  .map((name) => {
    const source = workflow(name);
    if (!/uses: shared\/control\.md/.test(source)) return null;
    const config = frontmatter(source, name);
    const controlImport = config.imports.find((entry) => entry?.uses === "shared/control.md");
    const lock = parse(workflow(name.replace(/\.md$/, ".lock.yml")));
    return { name, config, controlImport, lock };
  })
  .filter(Boolean);
const workers = controlledWorkflows.filter(({ controlImport }) => controlImport.with.role === "worker");

test("target read credentials go directly to authentication inputs, never step outputs", () => {
  assert.equal(control.tools.github["github-token"], TARGET_READ_CREDENTIAL);
  const credential = control.jobs.agent["pre-steps"].find((step) => step.id === "cao_target_read_credential");
  assert.equal(credential.env.CAO_TARGET_READ_TOKEN, TARGET_READ_CREDENTIAL);
  assert.doesNotMatch(credential.run, /GITHUB_OUTPUT|GITHUB_ENV/);
  assert.match(credential.run, /echo "::add-mask::\$CAO_TARGET_READ_TOKEN"/);
  for (const token of ["", "fixture-target-read-token"]) {
    const result = spawnSync("bash", ["-c", credential.run], {
      encoding: "utf8",
      env: { ...process.env, CAO_AUTH_MODE: "app", CAO_TARGET_READ_TOKEN: token, TARGET_REPO: "acme/target" },
    });
    assert.equal(result.status, token ? 0 : 1);
    assert.equal(result.stdout, token ? `::add-mask::${token}\n` : "");
    if (token) {
      assert.equal(result.stderr, "");
    } else {
      assert.match(result.stderr, /No app read credential is configured for acme\/target/);
    }
  }
  for (const { name, lock } of controlledWorkflows) {
    assert.doesNotMatch(JSON.stringify(lock), /cao_target_read_credential\.outputs/, name);
  }
});

function agentStep(lock, id) {
  return lock.jobs.agent.steps.find((step) => step.id === id);
}

test("shared control exposes target security metadata as declarable read inputs", () => {
  for (const input of [
    "read_vulnerability_alerts",
    "read_security_events",
    "read_secret_scanning_alerts",
    "read_checks",
    "read_statuses",
  ]) {
    assert.ok(readInputs.includes(input), `shared control must accept ${input}`);
  }
  for (const input of readInputs) {
    assert.equal(control["import-schema"][input].default, "", `${input} must default to no access`);
    assert.ok(
      controlSource.includes(`${actionPermission(input)}: "\${{ github.aw.import-inputs.${input} }}"`),
      `shared control must resolve ${input} at compile time on the target-scoped read App token`,
    );
  }
});

test("read App grants every declarable target read permission without widening read PATs", () => {
  const readApp = APP_PROFILES.find((profile) => profile.role === "read");
  const readPat = FINE_GRAINED_PAT_PROFILES.find((profile) => profile.role === "read");
  assert.ok(readApp);
  assert.ok(readPat);

  for (const input of readInputs) {
    assert.equal(readApp.permissions[permissionName(input)], "read", `read App must grant ${permissionName(input)}`);
  }
  for (const permission of Object.keys(readPat.permissions)) {
    assert.equal(readApp.permissions[permission], "read", `read PAT permission ${permission} must also be granted by the read App`);
  }
  assert.ok(Object.values(readApp.permissions).every((level) => level === "read"), "read App must stay read-only");
  assert.ok(Object.values(readPat.permissions).every((level) => level === "read"), "read PAT must stay read-only");

  for (const { name, controlImport } of controlledWorkflows) {
    for (const [input, value] of Object.entries(controlImport.with)) {
      if (!input.startsWith("read_") || input === "read_repository") continue;
      assert.ok(readInputs.includes(input), `${name} declares unknown read input ${input}`);
      assert.equal(value, "read", `${name} must request only read access for ${input}`);
    }
  }
});

test("every worker mints its read credential for the dispatched target with its declared permissions", () => {
  assert.ok(workers.length > 0);
  for (const { name, controlImport, lock } of workers) {
    assert.equal(controlImport.with.read_repository, "${{ inputs.target_repo }}", `${name} must read its dispatched target`);

    const scope = agentStep(lock, "cao_target_read_scope");
    const appToken = agentStep(lock, "cao_target_read_app_token");
    const credential = agentStep(lock, "cao_target_read_credential");
    assert.ok(scope && appToken && credential, `${name} must compile the shared target read credential steps`);
    const steps = lock.jobs.agent.steps;
    assert.ok(steps.indexOf(scope) < steps.indexOf(appToken), name);
    assert.ok(steps.indexOf(appToken) < steps.indexOf(credential), name);

    assert.equal(scope.env.CAO_READ_REPOSITORY, "${{ inputs.target_repo }}", `${name} read scope must be the target`);
    for (const input of readInputs) {
      const declared = controlImport.with[input] === "read" ? "read" : "";
      assert.equal(
        appToken.with[actionPermission(input)],
        declared,
        `${name} must compile ${input} to "${declared}" on the read App token`,
      );
    }

    assert.match(appToken.uses, /^actions\/create-github-app-token@[0-9a-f]{40}$/, name);
    assert.match(appToken.with["client-id"], /GH_AW_GITHUB_READ_APP_ID/, `${name} must use the read App`);
    assert.match(appToken.with["private-key"], /GH_AW_GITHUB_READ_APP_PRIVATE_KEY/, `${name} must use the read App`);
    assert.doesNotMatch(JSON.stringify(appToken), /WRITE_APP/, `${name} must not read with the write App`);
    assert.equal(appToken.with.owner, "${{ steps.cao_target_read_scope.outputs.owner }}", name);
    assert.equal(appToken.with.repositories, "${{ steps.cao_target_read_scope.outputs.repository }}", name);

    assert.match(
      credential.env.CAO_TARGET_READ_TOKEN,
      /^\$\{\{ steps\.cao_target_read_app_token\.outputs\.token \|\| vars\.GH_AW_GITHUB_AUTH_MODE == 'pat' && secrets\[fromJSON\(vars\.GH_AW_GITHUB_READ_PAT_REPOSITORIES \|\| '\{\}'\)\[steps\.cao_target_read_scope\.outputs\.full_name\]\]/,
      `${name} must prefer the target-scoped read App token`,
    );
    assert.doesNotMatch(credential.env.CAO_TARGET_READ_TOKEN, /WRITE/, `${name} must not read with a write credential`);
  }
});

test("orchestrators keep their read credential on the control repository", () => {
  for (const { name, controlImport } of controlledWorkflows) {
    if (controlImport.with.role !== "orchestrator") continue;
    assert.equal(controlImport.with.read_repository, "${{ github.repository }}", name);
  }
});

test("workers exposing GitHub security toolsets declare the matching target read permission", () => {
  for (const { name, config, controlImport } of workers) {
    const toolsets = config.tools?.github?.toolsets ?? [];
    for (const [toolset, input] of TOOLSET_PERMISSIONS) {
      if (!toolsets.includes(toolset)) continue;
      assert.equal(controlImport.with[input], "read", `${name} exposes ${toolset} tools and must declare ${input}`);
    }
  }
});

test("worker steps that prefetch target security alerts use the target-scoped read credential", () => {
  const prefetching = [];
  for (const { name, controlImport, lock } of workers) {
    const steps = lock.jobs.agent.steps;
    const credentialIndex = steps.findIndex((step) => step.id === "cao_target_read_credential");
    for (const [index, step] of steps.entries()) {
      const script = step.with?.script;
      if (typeof script !== "string" || !SECURITY_ALERT_ROUTE.test(script)) continue;
      prefetching.push(name);
      assert.equal(step.with["github-token"], TARGET_READ_CREDENTIAL, `${name}: ${step.name} must read through the read App`);
      assert.ok(credentialIndex >= 0 && credentialIndex < index, `${name}: ${step.name} must run after credential resolution`);
      if (/\/dependabot\/alerts\b/.test(script)) {
        assert.equal(controlImport.with.read_vulnerability_alerts, "read", `${name} must declare read_vulnerability_alerts`);
      }
      if (/\/code-scanning\/alerts\b/.test(script)) {
        assert.equal(controlImport.with.read_security_events, "read", `${name} must declare read_security_events`);
      }
      if (/\/secret-scanning\/alerts\b/.test(script)) {
        assert.equal(controlImport.with.read_secret_scanning_alerts, "read", `${name} must declare read_secret_scanning_alerts`);
      }
    }
  }
  assert.ok(prefetching.includes("dependabot-update-planner.md"), "Dependabot planner must prefetch target alerts");
});

function dependabotPrefetch() {
  const { lock } = workers.find(({ name }) => name === "dependabot-update-planner.md");
  const step = lock.jobs.agent.steps.find((entry) => entry.name === "Fetch target Dependabot alert evidence");
  assert.ok(step, "missing Dependabot alert prefetch step");
  return step;
}

async function runDependabotPrefetch({
  target = "acme/service",
  control = "acme/control",
  targetPrivate = false,
  controlPrivate = true,
  precompute,
  pages = [[]],
  failure,
}) {
  const files = new Map([
    ["/tmp/gh-aw/agent/control-precompute.json", JSON.stringify(precompute ?? {
      authorized: true, control_role: "worker", target_repo: target,
    })],
  ]);
  const fs = {
    readFileSync: (path) => {
      if (!files.has(path)) throw new Error(`unexpected read ${path}`);
      return files.get(path);
    },
    writeFileSync: (path, value) => files.set(path, value),
    mkdirSync: () => {},
  };
  const readRequests = [];
  const actionRequests = [];
  const github = {
    request: async (route, parameters) => {
      readRequests.push({ route, ...parameters });
      if (route === "GET /repos/{owner}/{repo}") return { data: { private: targetPrivate } };
      if (failure) throw failure;
      return { data: pages[parameters.page - 1] ?? [] };
    },
  };
  const getOctokit = (token) => ({
    request: async (route, parameters) => {
      actionRequests.push({ token, route, ...parameters });
      return { data: { private: controlPrivate } };
    },
  });
  const warnings = [];
  const core = { info: () => {}, warning: (message) => warnings.push(message) };
  const run = new Function(
    "require", "process", "github", "getOctokit", "core",
    `return (async () => {\n${dependabotPrefetch().with.script}\n})();`,
  );
  await run(
    (id) => {
      assert.equal(id, "node:fs");
      return fs;
    },
    { env: { TARGET_REPO: target, CONTROL_REPO: control, GITHUB_ACTION_TOKEN: "workflow-token" } },
    github,
    getOctokit,
    core,
  );
  const evidence = files.get("/tmp/gh-aw/agent/dependabot-alerts.json");
  return { evidence: evidence && JSON.parse(evidence), readRequests, actionRequests, warnings };
}

test("Dependabot planner reads complete target alert evidence through the target read credential", async () => {
  const step = dependabotPrefetch();
  assert.equal(step.with["github-token"], TARGET_READ_CREDENTIAL);

  const alert = {
    number: 7,
    html_url: "https://github.com/acme/service/security/dependabot/7",
    dependency: { package: { ecosystem: "npm", name: "lodash" }, manifest_path: "package-lock.json", scope: "runtime" },
    security_advisory: { ghsa_id: "GHSA-xxxx-yyyy-zzzz", severity: "high", description: "omitted" },
    security_vulnerability: { vulnerable_version_range: "< 4.17.21", first_patched_version: { identifier: "4.17.21" } },
  };
  const { evidence, readRequests, actionRequests } = await runDependabotPrefetch({
    pages: [Array.from({ length: 100 }, () => alert), [alert]],
  });

  assert.deepEqual(readRequests.map(({ route, owner, repo, state, page }) => ({ route, owner, repo, state, page })), [
    { route: "GET /repos/{owner}/{repo}", owner: "acme", repo: "service", state: undefined, page: undefined },
    { route: "GET /repos/{owner}/{repo}/dependabot/alerts", owner: "acme", repo: "service", state: "open", page: 1 },
    { route: "GET /repos/{owner}/{repo}/dependabot/alerts", owner: "acme", repo: "service", state: "open", page: 2 },
  ]);
  assert.deepEqual(
    actionRequests.map(({ token, route, owner, repo }) => ({ token, route, owner, repo })),
    [{ token: "workflow-token", route: "GET /repos/{owner}/{repo}", owner: "acme", repo: "control" }],
    "the workflow token may only read control repository visibility",
  );
  assert.equal(evidence.target_repo, "acme/service");
  assert.equal(evidence.complete, true);
  assert.equal(evidence.alerts.length, 101);
  assert.deepEqual(evidence.alerts[0].security_advisory, { ghsa_id: "GHSA-xxxx-yyyy-zzzz", severity: "high" });
  assert.equal(evidence.alerts[0].dependency.manifest_path, "package-lock.json");
});

test("Dependabot planner records incomplete evidence when the read credential cannot read alerts", async () => {
  const failure = Object.assign(new Error("Resource not accessible by integration"), { status: 403 });
  const { evidence, warnings } = await runDependabotPrefetch({ failure });

  assert.equal(evidence.complete, false);
  assert.equal(evidence.alerts, null);
  assert.equal(evidence.unavailable_reason, "api_request_failed");
  assert.equal(warnings.length, 1);
});

test("Dependabot planner refuses to read alerts for a target other than the admitted worker target", async () => {
  await assert.rejects(
    runDependabotPrefetch({
      precompute: { authorized: true, control_role: "worker", target_repo: "acme/other" },
    }),
    /does not match the authorized worker target/,
  );
  await assert.rejects(
    runDependabotPrefetch({
      precompute: { authorized: true, control_role: "orchestrator", target_repo: "acme/service" },
    }),
    /does not match the authorized worker target/,
  );
});

test("Dependabot planner reads non-public target alerts only for a private control plane", async () => {
  const privateControl = await runDependabotPrefetch({ targetPrivate: true, controlPrivate: true, pages: [[{ number: 1 }]] });
  assert.equal(privateControl.evidence.complete, true);
  assert.equal(privateControl.evidence.alerts.length, 1);

  const publicTargetPublicControl = await runDependabotPrefetch({ controlPrivate: false, pages: [[{ number: 2 }]] });
  assert.equal(publicTargetPublicControl.evidence.complete, true);

  for (const visibility of [{ controlPrivate: false }, { controlPrivate: null }]) {
    const { evidence, readRequests, warnings } = await runDependabotPrefetch({ targetPrivate: true, ...visibility });
    assert.equal(evidence.complete, false);
    assert.equal(evidence.alerts, null);
    assert.equal(evidence.unavailable_reason, "non_public_target_requires_private_control_plane");
    assert.ok(readRequests.every(({ route }) => !route.endsWith("/dependabot/alerts")), "must not read private alerts");
    assert.equal(warnings.length, 1);
  }

  const unknownTarget = await runDependabotPrefetch({ targetPrivate: null, controlPrivate: false });
  assert.equal(unknownTarget.evidence.unavailable_reason, "non_public_target_requires_private_control_plane");
});
