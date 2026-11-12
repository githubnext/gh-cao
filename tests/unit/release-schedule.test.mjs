import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";
import { parse } from "yaml";
import { generatedJobs, workflow } from "./workflow-contract.helpers.mjs";

const source = readFileSync(new URL("../../.github/workflows/release.md", import.meta.url), "utf8");
const config = parse(/^---\n([\s\S]*?)\n---/.exec(source)[1]);
const script = config.jobs["resolve-version"].steps[0].with.script;
const sha = "a".repeat(40);
const previousSha = "b".repeat(40);
const currentRun = "https://github.com/acme/gh-cao/actions/runs/42";
const green = {
  head_sha: sha, status: "completed", conclusion: "success",
  details_url: "https://ci.example/check", app: { slug: "github-actions" },
};
const release = { id: 1, tag_name: "v1.0.0", draft: false, prerelease: false, published_at: "2026-11-10T00:00:00Z" };

async function resolve(options = {}) {
  const outputs = {};
  const calls = [];
  const pages = {
    releases: [release],
    tags: [{ name: "v1.0.0" }],
    checks: [green, { ...green, status: "in_progress", conclusion: null, details_url: `${currentRun}/job/123` }],
    statuses: [],
    ...options.pages,
  };
  const endpoint = (name, data) => async (params) => {
    calls.push({ name, params });
    if (options.error === name) throw new Error("API unavailable");
    return { data };
  };
  const list = (name) => Object.assign(() => {}, { endpointName: name });
  const result = runInNewContext(`(async () => { ${script} })()`, {
    process: { env: { RELEASE_BUMP: options.bump, TRIGGERING_ACTOR: "maintainer" } },
    context: {
      eventName: options.eventName || "schedule", sha, ref: options.ref || "refs/heads/main",
      serverUrl: "https://github.com", runId: 42, repo: { owner: "acme", repo: "gh-cao" },
      payload: { repository: { fork: options.fork || false, default_branch: "main" } },
    },
    github: {
      paginate: async (method, params) => {
        const name = method.endpointName;
        calls.push({ name, params });
        assert.equal(params.per_page, 100);
        if (options.error === name) throw new Error("API page unavailable");
        return pages[name];
      },
      rest: {
        checks: { listForRef: list("checks") },
        repos: {
          listReleases: list("releases"), listTags: list("tags"),
          listCommitStatusesForRef: list("statuses"),
          getCollaboratorPermissionLevel: endpoint("permission", { permission: options.role || "maintain" }),
          getBranch: endpoint("branch", { commit: { sha: options.mainSha || sha } }),
          getCommit: endpoint("commit", { sha: options.baselineSha || previousSha }),
          compareCommitsWithBasehead: endpoint("compare", options.comparison || { status: "ahead", ahead_by: 3 }),
        },
      },
    },
    core: { info() {}, warning() {}, setOutput(name, value) { outputs[name] = value; } },
  });
  await result;
  return { outputs, calls };
}

test("daily release defaults to patch and gates all version consumers in source and lock", () => {
  assert.equal(config.on.schedule, "daily");
  assert.equal(config.on.workflow_dispatch.inputs.bump.default, "patch");
  assert.deepEqual(config.jobs["resolve-version"].permissions, { checks: "read", contents: "read", statuses: "read" });
  const jobs = generatedJobs(workflow("release.lock.yml"));
  for (const name of ["prepare-release", "build-extension"]) {
    assert.equal(config.jobs[name].if, "needs.resolve-version.outputs.release_tag != ''");
    assert.match(jobs.get(name).block, /if:.*needs\.resolve-version\.outputs\.release_tag != ''/);
  }
});

test("scheduled release checks the exact main SHA and defaults to patch even with other input", async () => {
  const { outputs, calls } = await resolve({ bump: "major" });
  assert.equal(outputs.release_tag, "v1.0.1");
  assert.equal(calls.some(({ name }) => name === "permission"), false);
  for (const name of ["checks", "statuses"]) {
    assert.equal(calls.find((call) => call.name === name).params.ref, sha);
  }
  assert.equal(calls.find(({ name }) => name === "checks").params.filter, "latest");
  assert.equal(calls.find(({ name }) => name === "compare").params.basehead, `${previousSha}...${sha}`);
});

test("newest published release includes prereleases, ignores drafts and API ordering", async () => {
  const { outputs, calls } = await resolve({
    pages: { releases: [
      { ...release, id: 20, tag_name: "v99.0.0", draft: true, published_at: null },
      { ...release, id: 2, tag_name: "v1.0.1", prerelease: true, published_at: "2026-11-11T00:00:00Z" },
      release,
    ] },
  });
  assert.equal(calls.find(({ name }) => name === "commit").params.ref, "v1.0.1");
  assert.equal(outputs.release_tag, "v99.0.1", "existing version collision avoidance still includes drafts and tags");
});

test("scheduled release no-ops on incomplete, non-green, stale, and unchanged evidence", async (t) => {
  const cases = [
    ["changed main", { mainSha: previousSha }],
    ["no releases", { pages: { releases: [] } }],
    ["only draft", { pages: { releases: [{ ...release, draft: true }] } }],
    ["missing publication time", { pages: { releases: [{ ...release, published_at: null }] } }],
    ["identical", { comparison: { status: "identical", ahead_by: 0 } }],
    ["behind", { comparison: { status: "behind", ahead_by: 0 } }],
    ["diverged", { comparison: { status: "diverged", ahead_by: 5 } }],
    ["incomplete comparison", { comparison: { status: "ahead" } }],
    ["no checks or statuses", { pages: { checks: [] } }],
    ["only own checks", { pages: { checks: [{ ...green, details_url: currentRun }] } }],
    ["other release run pending", { pages: { checks: [
      green, { ...green, status: "in_progress", details_url: `${currentRun}0/job/1` },
    ] } }],
    ["another app cannot impersonate this run", { pages: { checks: [
      green, { ...green, app: { slug: "other-ci" }, status: "in_progress", details_url: `${currentRun}/job/1` },
    ] } }],
    ["wrong check SHA", { pages: { checks: [{ ...green, head_sha: previousSha }] } }],
    ...["failure", "neutral", "skipped", "cancelled", "timed_out", "action_required", null]
      .map((conclusion) => [`check ${conclusion}`, { pages: { checks: [{ ...green, conclusion }] } }]),
    ...["queued", "in_progress"].map((status) => [status, { pages: { checks: [{ ...green, status }] } }]),
    ...["pending", "failure", "error", undefined].map((state) => [
      `status ${state}`, { pages: { statuses: [{ id: 1, context: "legacy", state }] } },
    ]),
    ["failure on later flattened check page", { pages: { checks: [
      ...Array.from({ length: 100 }, () => green), { ...green, conclusion: "failure" },
    ] } }],
    ["failure on later flattened status page", { pages: { statuses: [
      ...Array.from({ length: 100 }, (_, id) => ({ id, context: `ci-${id}`, state: "success" })),
      { id: 101, context: "later-page", state: "failure" },
    ] } }],
  ];
  for (const [name, options] of cases) {
    await t.test(name, async () => assert.equal((await resolve(options)).outputs.release_tag, undefined));
  }
});

test("latest legacy status wins per context, while status-only success is valid", async () => {
  const { outputs } = await resolve({ pages: { checks: [], statuses: [
    { id: 3, context: "ci", state: "success" }, { id: 1, context: "ci", state: "failure" },
    { id: 2, context: "ci", state: "pending" },
  ] } });
  assert.equal(outputs.release_tag, "v1.0.1");
  const failed = await resolve({ pages: { statuses: [
    { id: 1, context: "ci", state: "success" }, { id: 2, context: "ci", state: "failure" },
  ] } });
  assert.equal(failed.outputs.release_tag, undefined);
});

test("API failures and malformed evidence fail closed", async (t) => {
  for (const error of ["releases", "tags", "branch", "commit", "compare", "checks", "statuses"]) {
    await t.test(error, async () => assert.rejects(resolve({ error }), /API .*unavailable/));
  }
  await assert.rejects(resolve({ baselineSha: "main" }), /verified commit SHA/);
  await assert.rejects(resolve({ ref: "refs/heads/other" }), /must run from/);
  await assert.rejects(resolve({ pages: { statuses: [{ context: "ci", state: "success" }] } }), /Incomplete/);
});

test("manual dispatch preserves maintainer authorization, bumps, and default-branch restriction", async () => {
  const manual = { eventName: "workflow_dispatch", pages: { checks: [] } };
  assert.equal((await resolve({ ...manual, bump: "minor" })).outputs.release_tag, "v1.1.0");
  assert.equal((await resolve({ ...manual, bump: "major", role: "admin" })).outputs.release_tag, "v2.0.0");
  assert.equal((await resolve(manual)).outputs.release_tag, "v1.0.1");
  await assert.rejects(resolve({ ...manual, role: "write" }), /maintain or admin/);
  await assert.rejects(resolve({ ...manual, ref: "refs/heads/feature" }), /must run from/);
  await assert.rejects(resolve({ fork: true }), /cannot run from forks/);
});
