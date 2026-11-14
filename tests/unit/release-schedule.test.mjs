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
const mainSha = "c".repeat(40);
const green = { id: 10, workflow_id: 1, head_sha: sha, event: "push", status: "completed", conclusion: "success" };
const release = { id: 1, tag_name: "v1.0.0", draft: false, prerelease: false, published_at: "2026-11-10T00:00:00Z" };

async function resolve(options = {}) {
  const outputs = {};
  const calls = [];
  const pages = {
    releases: [release],
    tags: [{ name: "v1.0.0" }],
    runs: [green, { ...green, id: 11, workflow_id: 2, conclusion: "skipped" }],
    statuses: [],
    ...options.pages,
  };
  const endpoint = (name, data) => async (params) => {
    calls.push({ name, params });
    if (options.error === name) throw new Error("API unavailable");
    return { data: typeof data === "function" ? data(params) : data };
  };
  const compare = ({ basehead }) => basehead.startsWith(`${sha}...`)
    ? (options.containment || { status: "ahead", ahead_by: 2, behind_by: 0 })
    : (options.comparison || { status: "ahead", ahead_by: 3 });
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
        actions: { listWorkflowRunsForRepo: list("runs") },
        repos: {
          listReleases: list("releases"), listTags: list("tags"),
          listCommitStatusesForRef: list("statuses"),
          getCollaboratorPermissionLevel: endpoint("permission", { permission: options.role || "maintain" }),
          getBranch: endpoint("branch", { commit: { sha: options.mainSha || sha } }),
          getCommit: endpoint("commit", { sha: options.baselineSha || previousSha }),
          compareCommitsWithBasehead: endpoint("compare", compare),
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
  assert.deepEqual(config.jobs["resolve-version"].permissions, { actions: "read", contents: "read", statuses: "read" });
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
  assert.equal(calls.find(({ name }) => name === "statuses").params.ref, sha);
  assert.deepEqual(
    (({ head_sha, event }) => ({ head_sha, event }))(calls.find(({ name }) => name === "runs").params),
    { head_sha: sha, event: "push" },
  );
  assert.equal(calls.filter(({ name }) => name === "compare").length, 1, "unchanged main needs no containment check");
  assert.equal(calls.find(({ name }) => name === "compare").params.basehead, `${previousSha}...${sha}`);
});

test("scheduled release still ships its SHA after main advances past it", async () => {
  const { outputs, calls } = await resolve({ mainSha });
  assert.equal(outputs.release_tag, "v1.0.1");
  assert.equal(calls.find(({ name }) => name === "compare").params.basehead, `${sha}...${mainSha}`);
});

test("non-push agentic runs never gate the release, but newer push attempts do", async () => {
  assert.equal((await resolve({ pages: { runs: [
    { ...green, id: 1, conclusion: "failure" }, { ...green, id: 2 },
  ] } })).outputs.release_tag, "v1.0.1", "latest push run per workflow wins");
  assert.equal((await resolve({ pages: { runs: [
    { ...green, id: 2 }, { ...green, id: 3, conclusion: "failure" },
  ] } })).outputs.release_tag, undefined);
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
    ...[["removed from main", { status: "diverged", ahead_by: 1, behind_by: 1 }],
      ["main behind schedule", { status: "behind", ahead_by: 0, behind_by: 1 }],
      ["incomplete containment", { status: "ahead", ahead_by: 1 }],
    ].map(([name, containment]) => [name, { mainSha, containment }]),
    ["unverified main", { mainSha: "main" }],
    ["no releases", { pages: { releases: [] } }],
    ["only draft", { pages: { releases: [{ ...release, draft: true }] } }],
    ["missing publication time", { pages: { releases: [{ ...release, published_at: null }] } }],
    ["identical", { comparison: { status: "identical", ahead_by: 0 } }],
    ["behind", { comparison: { status: "behind", ahead_by: 0 } }],
    ["diverged", { comparison: { status: "diverged", ahead_by: 5 } }],
    ["incomplete comparison", { comparison: { status: "ahead" } }],
    ["no push runs or statuses", { pages: { runs: [] } }],
    ["wrong run SHA", { pages: { runs: [{ ...green, head_sha: previousSha }] } }],
    ["non-push run in results", { pages: { runs: [{ ...green, event: "schedule" }] } }],
    ...["failure", "neutral", "cancelled", "timed_out", "action_required", "stale", null]
      .map((conclusion) => [`run ${conclusion}`, { pages: { runs: [{ ...green, conclusion }] } }]),
    ...["queued", "in_progress", "waiting"].map((status) => [status, { pages: { runs: [{ ...green, status }] } }]),
    ...["pending", "failure", "error", undefined].map((state) => [
      `status ${state}`, { pages: { statuses: [{ id: 1, context: "legacy", state }] } },
    ]),
    ["failure on later flattened run page", { pages: { runs: [
      ...Array.from({ length: 100 }, (_, id) => ({ ...green, id, workflow_id: id })),
      { ...green, id: 100, workflow_id: 100, conclusion: "failure" },
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
  const { outputs } = await resolve({ pages: { runs: [], statuses: [
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
  for (const error of ["releases", "tags", "branch", "commit", "compare", "runs", "statuses"]) {
    await t.test(error, async () => assert.rejects(resolve({ error }), /API .*unavailable/));
  }
  await assert.rejects(resolve({ baselineSha: "main" }), /verified commit SHA/);
  await assert.rejects(resolve({ ref: "refs/heads/other" }), /must run from/);
  await assert.rejects(resolve({ pages: { statuses: [{ context: "ci", state: "success" }] } }), /Incomplete/);
  await assert.rejects(resolve({ pages: { runs: [{ ...green, workflow_id: undefined }] } }), /Incomplete/);
});

test("manual dispatch preserves maintainer authorization, bumps, and default-branch restriction", async () => {
  const manual = { eventName: "workflow_dispatch", pages: { runs: [] } };
  assert.equal((await resolve({ ...manual, bump: "minor" })).outputs.release_tag, "v1.1.0");
  assert.equal((await resolve({ ...manual, bump: "major", role: "admin" })).outputs.release_tag, "v2.0.0");
  assert.equal((await resolve(manual)).outputs.release_tag, "v1.0.1");
  await assert.rejects(resolve({ ...manual, role: "write" }), /maintain or admin/);
  await assert.rejects(resolve({ ...manual, ref: "refs/heads/feature" }), /must run from/);
  await assert.rejects(resolve({ fork: true }), /cannot run from forks/);
});
