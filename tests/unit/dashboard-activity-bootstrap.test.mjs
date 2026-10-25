import assert from "node:assert/strict";
import test from "node:test";
import { runInNewContext } from "node:vm";
import { parse } from "yaml";
import { workflow } from "./workflow-contract.helpers.mjs";

const { jobs } = parse(workflow("cao-dashboard.yml"));
const step = (name) => jobs.build.steps.find((candidate) => candidate.name === name);
const resolver = step("Resolve fallback activity run");

async function resolveActivity({ runs = [], jobSets = {}, artifactSets = {}, eventRun, matchedKey = "", error, jobError, artifactError } = {}) {
  const outputs = {};
  const notices = [];
  const summaries = [];
  const requests = [];
  const artifactRequests = [];
  const summary = {
    addHeading(text) { summaries.push(text); return this; },
    addRaw(text) { summaries.push(text); return this; },
    async write() { return this; },
  };
  await runInNewContext(`(async () => {\n${resolver.with.script}\n})()`, {
    process: { env: { ACTIVITY_CACHE_MATCHED_KEY: matchedKey } },
    context: {
      eventName: eventRun ? "workflow_run" : "workflow_dispatch",
      repo: { owner: "acme", repo: "control" },
      payload: { repository: { default_branch: "production" }, workflow_run: eventRun },
    },
    github: {
      async paginate(method, request) {
        const { run_id } = request;
        if (method === this.rest.actions.listWorkflowRunArtifacts) {
          artifactRequests.push(request);
          if (artifactError) throw artifactError;
          return artifactSets[run_id] ?? [{ name: "cao-activity-index", expired: false }];
        }
        if (jobError) throw jobError;
        return jobSets[run_id] ?? [{ name: "index", conclusion: "success" }];
      },
      rest: {
        actions: {
          listJobsForWorkflowRun() {},
          listWorkflowRunArtifacts() {},
          async listWorkflowRuns(request) {
            requests.push(request);
            if (error) throw error;
            return { data: { workflow_runs: runs.slice((request.page - 1) * 100, request.page * 100) } };
          },
        },
      },
    },
    core: {
      info() {},
      notice(message) { notices.push(message); },
      setOutput(name, value) { outputs[name] = value; },
      summary,
    },
  });
  return { outputs, notices, summaries, requests, artifactRequests };
}

test("dashboard reports inventory-only bootstrap when no Activity snapshot has been collected", async () => {
  const { outputs, notices, summaries, requests } = await resolveActivity();
  assert.equal(outputs["run-id"], undefined);
  assert.equal(notices.length, 1);
  assert.match(notices[0], /No successful CAO Activity collection run/);
  assert.match(summaries.join("\n"), /installed campaigns and configuration with an empty activity set/);
  assert.match(summaries.join("\n"), /no activity computations/);
  assert.match(summaries.join("\n"), /automatically/);
  assert.match(summaries.join("\n"), /CAO Activity.*manually/);
  assert.equal(requests[0].owner, "acme");
  assert.equal(requests[0].repo, "control");
  assert.equal(requests[0].workflow_id, "cao-activity.yml");
  assert.equal(requests[0].branch, "production");
  assert.equal(requests[0].status, "success");
  assert.equal(requests[0].per_page, 100);
});

test("dashboard still resolves a successful Activity run after a cache miss", async () => {
  const { outputs, notices, summaries } = await resolveActivity({ runs: [{ id: 12345 }] });
  assert.equal(outputs["run-id"], "12345");
  assert.deepEqual(notices, []);
  assert.deepEqual(summaries, []);
});

test("manual dashboard fallback skips missing and expired Activity artifacts", async () => {
  const { outputs, artifactRequests } = await resolveActivity({
    runs: [{ id: 10 }, { id: 11 }, { id: 12 }],
    artifactSets: {
      10: [{ name: "cao-activity-owner", expired: false }],
      11: [{ name: "cao-activity-index", expired: true }],
    },
  });
  assert.equal(outputs["run-id"], "12");
  assert.deepEqual(artifactRequests.map(({ run_id }) => run_id), [10, 11, 12]);
  assert.ok(artifactRequests.every(({ per_page }) => per_page === 100));
});

test("manual dashboard builds do not bootstrap over unavailable collected snapshots", async () => {
  for (const artifacts of [[], [{ name: "cao-activity-index", expired: true }]]) {
    await assert.rejects(resolveActivity({
      runs: [{ id: 10 }],
      artifactSets: { 10: artifacts },
    }), /No retained CAO Activity index artifact.*run CAO Activity/);
  }
});

const skippedJobs = [
  { name: "plan", conclusion: "skipped" },
  { name: "index", conclusion: "skipped" },
];

test("dashboard bootstraps when successful Activity runs skipped snapshot indexing", async () => {
  const { outputs, notices } = await resolveActivity({
    runs: [{ id: 10 }],
    jobSets: { 10: skippedJobs },
  });
  assert.equal(outputs["run-id"], undefined);
  assert.equal(notices.length, 1);
});

test("dashboard finds collected evidence beyond a page of skipped Activity runs", async () => {
  const skippedRuns = Array.from({ length: 100 }, (_, id) => ({ id }));
  const { outputs, requests } = await resolveActivity({
    runs: [...skippedRuns, { id: 12345 }],
    jobSets: Object.fromEntries(skippedRuns.map(({ id }) => [id, skippedJobs])),
  });
  assert.equal(outputs["run-id"], "12345");
  assert.deepEqual(requests.map(({ page }) => page), [1, 2]);
});

test("dashboard requires a successful index, not merely a successful workflow", async () => {
  const { outputs } = await resolveActivity({
    runs: [{ id: 10 }, { id: 11 }, { id: 12 }, { id: 13 }],
    jobSets: {
      10: [],
      11: [{ name: "index", conclusion: "failure" }],
      12: [{ name: "index", conclusion: "cancelled" }],
    },
  });
  assert.equal(outputs["run-id"], "13");
});

test("automatic dashboard cache misses use the triggering snapshot rather than a different successful run", async () => {
  const { outputs, notices, summaries, requests } = await resolveActivity({
    eventRun: { id: 12345, run_attempt: 2 },
    runs: [{ id: 54321 }],
  });
  assert.equal(outputs["run-id"], "12345");
  assert.deepEqual(requests, []);
  assert.deepEqual(notices, []);
  assert.deepEqual(summaries, []);
});

test("automatic dashboard builds fail closed when the triggering run has no successful index", async () => {
  for (const jobs of [[], skippedJobs, [{ name: "index", conclusion: "failure" }]]) {
    await assert.rejects(resolveActivity({
      eventRun: { id: 12345 },
      runs: [{ id: 54321 }],
      jobSets: { 12345: jobs },
    }), /CAO Activity run 12345 did not produce a successful index/);
  }
});

test("automatic dashboard builds verify exact cache identity even when Actions returns a primary-key prefix match", async () => {
  const eventRun = { id: 12345, run_attempt: 1 };
  const result = await resolveActivity({ eventRun, matchedKey: "cao-activity-v5-12345-1" });
  assert.deepEqual(result.outputs, {});
  assert.deepEqual(result.requests, []);
  await assert.rejects(resolveActivity({
    eventRun, matchedKey: "cao-activity-v5-12345-10",
  }), /Restored activity cache does not match the triggering run and attempt/);
});

test("automatic dashboard builds cannot restore a stale prefix cache, including on reruns", () => {
  const restore = step("Restore collected activity data").with;
  const evaluate = (value, eventName, attempt) => runInNewContext(
    value.trim().slice(3, -2).trim(),
    {
      github: {
        event_name: eventName,
        event: { workflow_run: { id: 12345, run_attempt: attempt } },
        run_id: 67890,
        run_attempt: 3,
      },
      format: (template, ...args) => template.replace(/\{(\d+)\}/g, (_, index) => args[index]),
    },
  );
  for (const attempt of [1, 2]) {
    assert.equal(evaluate(restore.key, "workflow_run", attempt), `cao-activity-v5-12345-${attempt}`);
    assert.equal(evaluate(restore["restore-keys"], "workflow_run", attempt), "");
  }
  assert.equal(evaluate(restore.key, "workflow_dispatch", 1), "cao-activity-v5-lookup-67890-3");
  assert.equal(evaluate(restore["restore-keys"], "workflow_dispatch", 1), "cao-activity-v5-");
});

test("dashboard does not disguise Activity lookup errors as an empty deployment", async () => {
  for (const status of [403, 404, 429, 500]) {
    const error = Object.assign(new Error(`GitHub request failed: ${status}`), { status });
    await assert.rejects(resolveActivity({ error }), (actual) => actual === error);
    await assert.rejects(resolveActivity({ runs: [{ id: 12345 }], jobError: error }), (actual) => actual === error);
    await assert.rejects(resolveActivity({ runs: [{ id: 12345 }], artifactError: error }), (actual) => actual === error);
    await assert.rejects(resolveActivity({ eventRun: { id: 12345 }, jobError: error }), (actual) => actual === error);
  }
});

test("dashboard bootstraps only when neither cache nor run is available and always builds", () => {
  assert.equal(resolver.if, "github.event_name == 'workflow_run' || steps.activity-cache.outputs.cache-matched-key == ''");
  const download = step("Download fallback activity data");
  assert.equal(download.if, "steps.activity-cache.outputs.cache-matched-key == '' && steps.activity-artifact-run.outputs.run-id != ''");
  assert.equal(download.with["run-id"], "${{ steps.activity-artifact-run.outputs.run-id }}");
  assert.equal(download["continue-on-error"], undefined);
  const bootstrap = step("Bootstrap empty activity data");
  assert.equal(bootstrap.if, "steps.activity-cache.outputs.cache-matched-key == '' && steps.activity-artifact-run.outputs.run-id == ''");
  assert.match(bootstrap.with.script, /dashboard\/bootstrap-activity\.mjs/);
  assert.match(bootstrap.with.script, /root: process\.env\.GITHUB_WORKSPACE/);
  assert.equal(bootstrap["continue-on-error"], undefined);

  const consumerNames = [
    "Install dashboard build dependencies",
    "Upgrade fallback activity data",
    "Validate restored activity data",
    "Repair activity database and apply retention",
    "Assemble Dashboard Language site",
    "Upload dashboard artifact",
  ];
  for (const name of consumerNames) {
    assert.equal(step(name).if, undefined, name);
    assert.equal(step(name)["continue-on-error"], undefined, name);
  }
  assert.ok(jobs.build.steps.indexOf(download) < jobs.build.steps.indexOf(step(consumerNames[0])));
  assert.ok(jobs.build.steps.indexOf(bootstrap) < jobs.build.steps.indexOf(step(consumerNames[0])));

  for (const [cacheKey, runId, expected] of [
    ["", "", true],
    ["cao-activity-v5-123", "", false],
    ["", "12345", false],
  ]) {
    const evaluate = (condition) => runInNewContext(condition
      .replaceAll("steps.activity-cache.outputs.cache-matched-key", "cacheKey")
      .replaceAll("steps.activity-artifact-run.outputs.run-id", "runId"), { cacheKey, runId });
    assert.equal(evaluate(resolver.if.replace("github.event_name == 'workflow_run' || ", "")), cacheKey === "");
    assert.equal(evaluate(download.if), cacheKey === "" && runId !== "");
    assert.equal(evaluate(bootstrap.if), expected);
  }
});

test("dashboard caches successful builds and preserves the Pages deployment policy", () => {
  const upload = step("Upload dashboard artifact");
  assert.equal(upload.with["if-no-files-found"], "error");
  assert.equal(jobs.cache.needs, "build");
  assert.equal(jobs.cache.if, undefined);
  assert.equal(jobs.pages.needs, "build");
  assert.equal(jobs.pages.if, "needs.build.outputs.deploy == 'true'");
  assert.deepEqual(jobs.deploy.needs, ["build", "pages"]);
  assert.equal(jobs.deploy.if, "needs.build.outputs.deploy == 'true'");
  assert.equal(jobs["notify-failure"].if,
    "${{ always() && (needs.build.result == 'failure' || needs.cache.result == 'failure' || needs.pages.result == 'failure' || needs.deploy.result == 'failure') }}");
});
