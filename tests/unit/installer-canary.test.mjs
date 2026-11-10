import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { installerCanaryMatrix, checkCanaryWorkflowRuns, checkCanaryJobs } from "../../scripts/installer-canary.mjs";

const releases = [
  { tag_name: "v3.0.0", prerelease: false, draft: true },
  { tag_name: "not-a-version", prerelease: false, draft: false },
  { tag_name: "v2.1.0-rc.2", prerelease: true, draft: false },
  { tag_name: "v2.1.0-rc.1", prerelease: true, draft: false },
  { tag_name: "v2.0.0", prerelease: false, draft: false },
  { tag_name: "v1.9.0", prerelease: false, draft: false },
];

test("release canary retains all 16 real-release platform/channel/scenario combinations", () => {
  const { include } = installerCanaryMatrix(releases);
  assert.equal(include.length, 16);
  assert.deepEqual(installerCanaryMatrix([...releases].reverse()), { include });
  assert.equal(new Set(include.map(({ os, channel, scenario }) => `${os}/${channel}/${scenario}`)).size, 16);
  for (const row of include) {
    const current = row.channel === "stable" ? "v2.0.0" : "v2.1.0-rc.2";
    const previous = row.channel === "stable" ? "v1.9.0" : "v2.1.0-rc.1";
    assert.equal(row.required, row.scenario === "newer" ? previous : current);
    assert.equal(row.preinstalled, row.scenario === "missing" ? "" : row.scenario === "outdated" ? previous : current);
  }
  assert.throws(() => installerCanaryMatrix([]), /two published stable/);
  assert.throws(() => installerCanaryMatrix(releases.filter((release) => !release.prerelease)), /two published prerelease/);
});

const now = Date.parse("2026-10-22T18:00:00Z");
const server = "https://github.com";
const run = {
  status: "completed", conclusion: "success", created_at: "2026-10-22T17:00:00Z",
  html_url: `${server}/githubnext/gh-cao/actions/runs/123`,
};
test("deployed smoke requires fresh latest successful completed evidence, not an older success", () => {
  assert.equal(checkCanaryWorkflowRuns([run], { now }), run.html_url);
  assert.equal(checkCanaryWorkflowRuns([run, { ...run, status: "in_progress" }], { now }), run.html_url);
  assert.equal(checkCanaryWorkflowRuns([run, { ...run, conclusion: "skipped" }], { now }), run.html_url);
  for (const evidence of [
    [], [{ ...run, conclusion: "failure" }],
    [run, { ...run, created_at: "2026-10-22T17:30:00Z", conclusion: "failure" }],
    [{ ...run, created_at: "2026-10-20T17:00:00Z" }],
    [{ ...run, created_at: "invalid" }],
    [{ ...run, created_at: "2026-10-23T17:00:00Z" }],
    [{ ...run, html_url: "https://example.invalid" }],
  ]) assert.throws(() => checkCanaryWorkflowRuns(evidence, { now }));
});

test("canary is read-only and never dispatches, provisions, or archives repositories", () => {
  const workflow = readFileSync(".github/workflows/installer-canary.yml", "utf8");
  const script = readFileSync("scripts/installer-canary.mjs", "utf8");
  assert.doesNotMatch(workflow, /(?:contents|actions|issues|pull-requests): write|secrets\.|pull_request_target/);
  assert.doesNotMatch(script, /"workflow", "run"|--method|\/dispatches|archived|repo create/);
  assert.match(workflow, /github\.event_name != 'pull_request'/);
  assert.match(workflow, /node-version: 24/);
});

test("release canary scopes metadata authentication to the installation-test step", () => {
  const workflow = readFileSync(".github/workflows/installer-canary.yml", "utf8");
  assert.match(workflow, /- name: Test real release installation\n        env:\n          GH_TOKEN: \$\{\{ github\.token \}\}\n/);
  assert.match(workflow, /\npermissions:\n  contents: read\n/);
});

test("workflow success cannot mask skipped, absent or failed collection and browser jobs", () => {
  const jobs = [
    { name: "collect (org/repo)", conclusion: "success" },
    { name: "index", conclusion: "success" },
  ];
  checkCanaryJobs(jobs, [/^collect \(/, /^index$/]);
  assert.throws(() => checkCanaryJobs(jobs, [/^Test deployed dashboard$/]), /Missing/);
  for (const conclusion of ["skipped", "failure", null]) {
    assert.throws(() => checkCanaryJobs([...jobs, { name: "collect (other/repo)", conclusion }],
      [/^collect \(/, /^index$/]), /unsuccessful/);
  }
});
