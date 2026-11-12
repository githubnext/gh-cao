import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { selectCampaignLifecycleSuites } from "../../scripts/campaign-lifecycle-matrix.mjs";

const names = (files) => selectCampaignLifecycleSuites(files).map(({ name }) => name);

test("campaign lifecycle matrix selects only campaigns owning changed files", () => {
  assert.deepEqual(names(["uk-ai-advisory/dashboard.json"]), []);
  assert.deepEqual(names(["install-gh-cao.sh"]), ["root"]);
  assert.deepEqual(names([".github/workflows/daily-repo-status.md"]), ["root"]);
  assert.deepEqual(
    names([".github/workflows/shared/control.md"]),
    ["root", "CAO Evolution", "Dependabot", "Optimization"],
  );
  assert.deepEqual(
    names(["dashboard/site/index.html"]),
    ["root"],
  );
  assert.deepEqual(
    names([".github/workflows/graders/dependabot-update-planner-operational-value.sh"]),
    ["root", "Dependabot"],
  );
  assert.deepEqual(
    names([".github/workflows/graders/cao-evolution-compiler-security-operational-value.sh"]),
    ["root", "CAO Evolution"],
  );
  assert.deepEqual(
    names(["dependabot/.github/graders/dependabot-update-planner-operational-value.sh"]),
    ["Dependabot"],
  );
  assert.deepEqual(
    names(["optimization/operational-value/optimization-token-optimizer.mjs"]),
    ["Optimization"],
  );
  assert.deepEqual(
    names(["eu-cra-compliance/.github/graders/eu-cra-compliance-scope-classifier-operational-value.sh"]),
    [],
  );
});

test("campaign lifecycle matrix selects a campaign and its dependents when its manifest changes", () => {
  assert.deepEqual(names(["aw.yml"]), ["root"]);
  assert.deepEqual(names(["activity/aw.yml"]), []);
  assert.deepEqual(names([".github/workflows/shared/value-acquisition.mjs"]),
    ["root", "CAO Evolution", "Dependabot", "Optimization"]);
  assert.deepEqual(names(["software-development-practices/aw.yml"]), []);
  assert.deepEqual(names(["self-care/aw.yml"]), []);
});

test("campaign lifecycle matrix selects no campaigns for unrelated changes", () => {
  assert.deepEqual(names(["docs/index.mdx"]), []);
});

test("campaign lifecycle matrix selects all campaigns for manual runs", () => {
  const suites = selectCampaignLifecycleSuites(null);
  assert.equal(suites.length, 4);
  assert.equal(names(["tests/integration/campaign-lifecycle.test.mjs"]).length, 4);

  const source = readFileSync(new URL("../integration/campaign-lifecycle.test.mjs", import.meta.url), "utf8");
  const testNames = [...source.matchAll(/^test\("([^"]+)"/gm)].map((match) => match[1]);
  for (const suite of suites) {
    const matches = testNames.filter((testName) => new RegExp(suite["test-pattern"]).test(testName));
    assert.ok(matches.length >= 1, `${suite.name} must run at least one campaign lifecycle test`);
  }
});
