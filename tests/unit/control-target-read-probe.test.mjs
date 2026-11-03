import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { APP_PROFILES } from "../../.github/workflows/shared/github-apps.mjs";
import {
  assertTargetInScope,
  classifyProbe,
  formatProbeSummary,
  probeTargetReadAccess,
  TARGET_READ_PROBES,
} from "../../scripts/probe-target-read-access.mjs";
import { controlPolicy, root, workflow } from "./workflow-contract.helpers.mjs";

const apiUrl = "https://api.example.test";

function mockFetch(responses) {
  const calls = [];
  const fetchImpl = async (url, options) => {
    calls.push({ url, authorization: options.headers.Authorization });
    const path = url.slice(apiUrl.length + 1).replace(/\?.*$/, "");
    const [status, body] = responses[path] ?? [500, { message: "unexpected" }];
    return { status, json: async () => body };
  };
  return { calls, fetchImpl };
}

const readable = {
  "repos/github/gh-aw": [200, { default_branch: "main", visibility: "public" }],
  "repos/github/gh-aw/dependabot/alerts": [200, [{ number: 1, security_advisory: { summary: "secret detail" } }]],
  "repos/github/gh-aw/code-scanning/alerts": [200, []],
  "repos/github/gh-aw/secret-scanning/alerts": [200, [{ number: 3, secret: "never-print" }]],
  "repos/github/gh-aw/commits/main/check-runs": [200, { total_count: 4, check_runs: [{}] }],
  "repos/github/gh-aw/commits/main/status": [200, { total_count: 2, statuses: [{}] }],
};

test("probe covers Dependabot, code-scanning, secret-scanning, checks, and statuses with read App permissions", () => {
  const readApp = APP_PROFILES.find((profile) => profile.role === "read");
  assert.deepEqual(TARGET_READ_PROBES.map(({ id }) => id), [
    "dependabot_alerts", "code_scanning_alerts", "secret_scanning_alerts", "check_runs", "statuses",
  ]);
  for (const probe of TARGET_READ_PROBES) {
    assert.equal(readApp.permissions[probe.permission], "read", `read App must grant ${probe.permission}`);
  }
});

test("probe reports readable target evidence without exposing alert contents", async () => {
  const { calls, fetchImpl } = mockFetch(readable);
  const report = await probeTargetReadAccess({ target: "github/gh-aw", token: "read-token", apiUrl, fetchImpl });

  assert.equal(report.ok, true);
  assert.equal(report.repository.outcome, "readable");
  assert.deepEqual(report.probes.map(({ id, outcome, sample_count }) => [id, outcome, sample_count]), [
    ["dependabot_alerts", "readable", 1],
    ["code_scanning_alerts", "readable", 0],
    ["secret_scanning_alerts", "readable", 1],
    ["check_runs", "readable", 4],
    ["statuses", "readable", 2],
  ]);
  assert.ok(calls.every(({ authorization }) => authorization === ["Bearer", "read-token"].join(" ")));
  assert.ok(calls.every(({ url }) => url.startsWith(`${apiUrl}/repos/github/gh-aw`)), "probe must stay on the target");
  const output = `${JSON.stringify(report)}${formatProbeSummary(report)}`;
  assert.doesNotMatch(output, /never-print|secret detail|read-token/);
});

test("probe fails when the read App cannot read an evidence source", async () => {
  const { fetchImpl } = mockFetch({
    ...readable,
    "repos/github/gh-aw/dependabot/alerts": [403, { message: "Resource not accessible by integration" }],
  });
  const report = await probeTargetReadAccess({ target: "github/gh-aw", token: "t", apiUrl, fetchImpl });

  assert.equal(report.ok, false);
  assert.equal(report.probes.find(({ id }) => id === "dependabot_alerts").outcome, "denied");
  assert.match(formatProbeSummary(report), /cannot read every target evidence source/);
});

test("probe distinguishes disabled features from denied access", async () => {
  const { fetchImpl } = mockFetch({
    ...readable,
    "repos/github/gh-aw/secret-scanning/alerts": [404, { message: "Secret scanning is disabled on this repository." }],
  });
  const report = await probeTargetReadAccess({ target: "github/gh-aw", token: "t", apiUrl, fetchImpl });

  assert.equal(report.ok, true);
  assert.equal(report.probes.find(({ id }) => id === "secret_scanning_alerts").outcome, "feature-disabled");
  assert.equal(classifyProbe(404, "Not Found"), "denied");
  assert.equal(classifyProbe(502, ""), "error");
});

test("probe fails closed when the target repository is not readable", async () => {
  const { calls, fetchImpl } = mockFetch({ "repos/github/gh-aw": [404, { message: "Not Found" }] });
  const report = await probeTargetReadAccess({ target: "github/gh-aw", token: "t", apiUrl, fetchImpl });

  assert.equal(report.ok, false);
  assert.equal(report.repository.outcome, "denied");
  assert.equal(calls.length, 1);
  await assert.rejects(probeTargetReadAccess({ target: "github", token: "t", apiUrl, fetchImpl }), /OWNER\/REPO/);
  await assert.rejects(probeTargetReadAccess({ target: "github/gh-aw", token: "", apiUrl, fetchImpl }), /token is required/);
});

test("probe only targets repositories inside the control-plane scope", () => {
  const scope = controlPolicy["control-plane"].scope;
  const settings = { allowed_owners: scope["allowed-owners"], allowed_repositories: scope["allowed-repositories"] };
  assert.doesNotThrow(() => assertTargetInScope(settings, "github/gh-aw"));
  assert.doesNotThrow(() => assertTargetInScope(settings, "GitHub/GH-AW"));
  assert.throws(() => assertTargetInScope(settings, "github/not-enrolled"), /outside the control-plane scope/);
  assert.throws(() => assertTargetInScope({ allowed_owners: ["acme"], allowed_repositories: [] }, "octo/x"), /outside/);
  assert.doesNotThrow(() => assertTargetInScope({ allowed_owners: ["acme"], allowed_repositories: [] }, "acme/x"));
});

test("probe workflow mints a target-scoped read App token after validating scope", () => {
  const config = parse(workflow("target-read-access-probe.yml"));
  assert.equal(config.on.workflow_dispatch.inputs.target.default, "github/gh-aw");
  assert.deepEqual(config.permissions, { contents: "read" });
  const steps = config.jobs.probe.steps;
  const checkout = steps.find((step) => step.name === "Checkout probe and control policy");
  assert.match(checkout.with["sparse-checkout"], /\/activity\/value-acquisition\.mjs/);
  assert.equal(checkout.with["sparse-checkout-cone-mode"], false);
  const scope = steps.findIndex((step) => step.run === "node scripts/probe-target-read-access.mjs --scope-only");
  const tokenStep = steps.find((step) => step.id === "read-app-token");
  const token = steps.indexOf(tokenStep);
  const probe = steps.findIndex((step) => step.run === "node scripts/probe-target-read-access.mjs");
  assert.ok(scope >= 0 && scope < token && token < probe);
  assert.equal(tokenStep.with["client-id"], "${{ vars.GH_AW_GITHUB_READ_APP_ID }}");
  assert.equal(tokenStep.with["private-key"], "${{ secrets.GH_AW_GITHUB_READ_APP_PRIVATE_KEY }}");
  assert.equal(tokenStep.with.owner, "${{ steps.scope.outputs.owner }}");
  assert.equal(tokenStep.with.repositories, "${{ steps.scope.outputs.repository }}");
  const permissions = Object.keys(tokenStep.with).filter((key) => key.startsWith("permission-")).sort();
  assert.deepEqual(
    permissions,
    [...new Set(TARGET_READ_PROBES.map(({ permission }) => `permission-${permission.replaceAll("_", "-")}`))].sort(),
  );
  assert.ok(Object.values(tokenStep.with).every((value) => !/WRITE/.test(String(value))));
  assert.match(readFileSync(join(root, "scripts", "probe-target-read-access.mjs"), "utf8"), /assertTargetInScope\(controlSettings/);
});
