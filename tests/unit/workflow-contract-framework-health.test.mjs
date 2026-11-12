import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { runInNewContext } from "node:vm";
import { parse } from "yaml";
import { controlPolicy, root, workflow } from "./workflow-contract.helpers.mjs";

const source = workflow("cao-framework-health.md");
const config = parse(/^---\n([\s\S]*?)\n---/.exec(source)[1]);
const previewContract = JSON.parse(readFileSync(join(root, ".github/cao/private-preview.json"), "utf8"));
const directory = "/tmp/gh-aw/agent/framework-health";
const sha = "a".repeat(40);
const context = {
  repo: { owner: "githubnext", repo: "gh-cao" }, runId: 123,
  sha, ref: "refs/heads/main",
};
const marker = "<!-- framework-health:v1 repository=githubnext/gh-cao date=2026-11-09 -->";

function ownedReport({ runId = 100, attempt = 1, title = `[cao-framework-health] 2026-11-09 run ${runId}-${attempt}`,
  state = "closed", body = marker } = {}) {
  const execution = {
    repository: "githubnext/gh-cao", aw_workflow_id: "cao-framework-health",
    run_id: String(runId), run_attempt: String(attempt),
  };
  return {
    number: runId, title, state, user: { type: "Bot" },
    body: `${body}\n<!-- gh-aw-workflow-id: cao-framework-health -->\n<!-- gh-aw-execution: ${JSON.stringify(execution)} -->`,
  };
}

async function execute(name, { github = {}, files = new Map(), runContext = context, exec, env = {} } = {}) {
  const outputs = {};
  const fs = {
    mkdirSync() {},
    readFileSync(path) {
      assert.ok(files.has(path), `unexpected read: ${path}`);
      return files.get(path);
    },
    writeFileSync(path, value) { files.set(path, value); },
  };
  const step = [...config.steps, ...config["safe-outputs"].steps].find(step => step.name === name);
  await runInNewContext(`(async () => { ${step.with.script}\n })()`, {
    github, context: runContext, core: { setOutput(name, value) { outputs[name] = value; } },
    process: { env },
    require(name) {
      if (name === "node:fs") return fs;
      if (name === "node:crypto") return { createHash };
      assert.equal(name, "node:child_process");
      return { execFileSync: exec };
    },
  });
  return { files, outputs };
}

function historyAPI({ createdAt = "2026-11-10T07:00:00Z", reports = [], head = sha, attempt = 1,
  priorRuns = { 100: { created_at: "2026-11-10T06:00:00Z" } } } = {}) {
  return { rest: {
    repos: { async get() { return { data: { default_branch: "main" } }; } },
    actions: { async getWorkflowRun({ run_id }) {
      if (run_id === context.runId) {
        return { data: { head_sha: head, created_at: createdAt, run_attempt: attempt, html_url: "run-url" } };
      }
      assert.ok(Object.hasOwn(priorRuns, run_id), `unexpected prior run: ${run_id}`);
      return { data: priorRuns[run_id] };
    } },
    issues: { async listForRepo(parameters) {
      assert.equal(parameters.state, "all", "closed reports must participate in duplicate suppression");
      assert.equal(parameters.labels, "framework-health");
      assert.equal(parameters.sort, "created");
      assert.equal(parameters.direction, "desc");
      assert.ok(parameters.page <= 2, "history must remain bounded regardless of daily run count");
      return { data: reports.slice((parameters.page - 1) * 100, parameters.page * 100) };
    } },
  } };
}

async function evidenceFiles() {
  return (await execute("Fix scope, window and report history", { github: historyAPI() })).files;
}

async function previewFiles(contract = previewContract) {
  const files = await evidenceFiles();
  files.set(".github/cao/private-preview.json", JSON.stringify(contract));
  files.set(`${directory}/ci.json`, JSON.stringify({ workflows: [] }));
  files.set(`${directory}/components.json`, JSON.stringify({ components: [] }));
  return files;
}

function previewAPI({ candidateSha = "b".repeat(40), conclusion = "failure", partial = false } = {}) {
  const workflowIds = new Map();
  return { rest: {
    repos: { async getReleaseByTag({ tag }) { return { data: {
      tag_name: tag, draft: false, prerelease: true, immutable: true,
      published_at: "2026-11-09T12:00:00Z", assets: [{ name: "checksums.txt", digest: "sha256:example" }],
    } }; } },
    git: { async getRef() { return { data: { object: { type: "commit", sha: candidateSha } } }; } },
    actions: {
      async listWorkflowRuns(parameters) {
        assert.equal(parameters.branch, "main");
        assert.equal(parameters.per_page, 10);
        assert.equal(parameters.page, 1);
        if (!workflowIds.has(parameters.workflow_id)) workflowIds.set(parameters.workflow_id, workflowIds.size + 1);
        const id = workflowIds.get(parameters.workflow_id) + (parameters.head_sha ? 100 : 0);
        return { data: { total_count: 25, workflow_runs: [{
          id, head_sha: parameters.head_sha || sha, head_branch: "main",
          event: parameters.workflow_id === "release-promote.yml" ? "workflow_dispatch" : "push",
          run_attempt: 1, created_at: "2026-11-10T06:00:00Z",
          status: "completed", conclusion: parameters.head_sha ? "success" : conclusion,
          html_url: `https://github.com/githubnext/gh-cao/actions/runs/${id}`,
        }] } };
      },
      async listJobsForWorkflowRun({ page }) {
        const jobs = partial ? Array.from({ length: 100 }, (_, index) => ({
          id: page * 100 + index, name: "Integration", status: "completed", conclusion: "success",
        })) : [{
          id: 1, name: "Integration", status: "completed", conclusion: "success",
          steps: [{ name: "Real candidate deployment", status: "completed", conclusion: "skipped" }],
        }];
        return { data: { total_count: partial ? 250 : 1, jobs } };
      },
    },
  } };
}

test("framework reports freeze complete UTC windows for each run attempt", async () => {
  for (const [createdAt, date, end] of [
    ["2026-01-01T23:59:59Z", "2025-12-31", "2026-01-01T00:00:00.000Z"],
    ["2024-03-01T00:00:00Z", "2024-02-29", "2024-03-01T00:00:00.000Z"],
  ]) {
    const result = await execute("Fix scope, window and report history", { github: historyAPI({ createdAt }) });
    const data = JSON.parse(result.files.get(`${directory}/context.json`));
    assert.equal(data.date, date);
    assert.equal(data.report_window.start, `${date}T00:00:00.000Z`);
    assert.equal(data.report_window.end, end);
    assert.equal(Date.parse(data.ci_windows.current.end) - Date.parse(data.ci_windows.current.start), 7 * 86400000);
    assert.equal(data.ci_windows.previous.end, data.ci_windows.current.start);
    assert.equal(data.source_sha, sha);
    assert.equal(data.title, `${date} run 123-1`);
    assert.equal(data.duplicate, false);
  }
  for (const step of config.steps.slice(1)) {
    assert.equal(step.if, "${{ steps.framework_health.outputs.duplicate != 'true' }}");
  }
});

test("same-day dispatches and rerun attempts publish fresh reports but repeated attempts do not", async () => {
  for (const state of ["open", "closed"]) {
    for (const title of ["[cao-framework-health] 2026-11-09", "[cao-framework-health] 2026-11-09 run 100-1"]) {
      const result = await execute("Fix scope, window and report history", {
        github: historyAPI({ reports: [ownedReport({ state, title, body: "Report without a custom marker" })] }),
      });
      assert.equal(result.outputs.duplicate, false, "another run on the same UTC day must not suppress a new dispatch");
    }
    const retry = await execute("Fix scope, window and report history", {
      github: historyAPI({ attempt: 2, reports: [ownedReport({ state, runId: context.runId })] }),
    });
    assert.equal(retry.outputs.duplicate, false);
    assert.equal(JSON.parse(retry.files.get(`${directory}/context.json`)).title, "2026-11-09 run 123-2");
    const repeated = await execute("Fix scope, window and report history", {
      github: historyAPI({ attempt: 2, reports: [ownedReport({ state, runId: context.runId, attempt: 2 })] }),
    });
    assert.equal(repeated.outputs.duplicate, true);
    assert.equal(JSON.parse(repeated.files.get(`${directory}/context.json`)).duplicate, true);
  }
});

test("historical reruns cannot supersede newer runs even on the same day or without a custom marker", async () => {
  for (const created_at of ["2026-11-10T08:00:00Z", "2026-11-11T00:00:00Z"]) {
    const result = await execute("Fix scope, window and report history", {
      github: historyAPI({
        reports: [ownedReport({ state: "closed", title: "[cao-framework-health] 2026-11-09", body: "No custom marker" })],
        priorRuns: { 100: { created_at } },
      }),
    });
    assert.equal(result.outputs.duplicate, true, "publication order is bound to workflow creation, not issue/model dates");
  }
  const earlierAttempt = await execute("Fix scope, window and report history", {
    github: historyAPI({ reports: [ownedReport({ runId: context.runId, attempt: 2 })] }),
  });
  assert.equal(earlierAttempt.outputs.duplicate, true);
});

test("bounded report history does not become an indirect daily run cap", async () => {
  const reports = Array.from({ length: 201 }, () => ownedReport());
  const result = await execute("Fix scope, window and report history", { github: historyAPI({ reports }) });
  const data = JSON.parse(result.files.get(`${directory}/context.json`));
  assert.equal(result.outputs.duplicate, false);
  assert.deepEqual(data.report_history, { coverage: "partial", observed: 200 });
  assert.equal(data.reports.length, 14);
});

test("scope, revision, report collisions and inaccessible or invalid provenance fail closed", async () => {
  const title = "[cao-framework-health] 2026-11-09 run 123-1";
  for (const options of [
    { runContext: { ...context, repo: { owner: "acme", repo: "target" } } },
    { runContext: { ...context, ref: "refs/heads/feature" } },
    { github: historyAPI({ head: "b".repeat(40) }) },
    { github: historyAPI({ createdAt: "invalid" }) },
    { github: historyAPI({ attempt: null }) },
    { github: historyAPI({ attempt: 0 }) },
    { github: historyAPI({ reports: [{ title, user: { type: "User" } }] }) },
    { github: historyAPI({ reports: [{
      title, user: { type: "User" },
      body: "<!-- gh-aw-workflow-id: cao-framework-health -->",
    }] }) },
    { github: historyAPI({ reports: [{
      title, user: { type: "Bot" },
      body: "<!-- gh-aw-workflow-id: another-workflow -->",
    }] }) },
    { github: historyAPI({ reports: [{
      ...ownedReport(), body: "<!-- gh-aw-workflow-id: cao-framework-health -->",
    }] }) },
    { github: historyAPI({ reports: [{
      ...ownedReport(), body: "<!-- gh-aw-workflow-id: cao-framework-health -->\n<!-- gh-aw-execution: {invalid} -->",
    }] }) },
    { github: historyAPI({ reports: [ownedReport()], priorRuns: { 100: { created_at: "invalid" } } }) },
    { github: { rest: { repos: { async get() { throw new Error("403 forbidden"); } } } } },
  ]) {
    const files = new Map();
    await assert.rejects(execute("Fix scope, window and report history", { github: historyAPI(), files, ...options }));
    assert.equal(files.size, 0, "denied/ambiguous evidence cannot look like successful collection");
  }
});

test("CI comparisons preserve outcomes, pending runs, strict windows and skipped test steps", async () => {
  const row = (id, created_at, conclusion, extra = {}) => ({
    id, created_at, conclusion, status: "completed", event: "push", head_branch: "main",
    head_sha: sha, run_attempt: 2, html_url: `run-${id}`, ...extra,
  });
  const rows = [
    row(1, "2026-11-03T00:00:00Z", "success"),
    row(2, "2026-11-05T00:00:00Z", "failure"),
    row(3, "2026-11-06T00:00:00Z", "cancelled"),
    row(4, "2026-11-07T00:00:00Z", null, { status: "queued" }),
    row(5, "2026-10-27T00:00:00Z", "neutral"),
    row(6, "2026-11-02T00:00:00Z", "success"),
    row(7, "2026-11-10T00:00:00Z", "success"),
    row(8, "2026-10-26T23:59:59Z", "success"),
    row(9, "2026-11-09T00:00:00Z", "success", { head_branch: "feature" }),
    row(10, "2026-11-09T00:00:00Z", "success", { event: "pull_request" }),
  ];
  const result = await execute("Collect comparable CI evidence", { files: await evidenceFiles(), github: { rest: { actions: {
    async listWorkflowRuns(parameters) {
      assert.equal(parameters.branch, "main");
      assert.equal(parameters.event, "push");
      return { data: { total_count: rows.length, workflow_runs: rows } };
    },
    async listJobsForWorkflowRun(parameters) {
      assert.equal(parameters.run_id, 4);
      assert.equal(parameters.filter, "latest");
      return { data: { total_count: 1, jobs: [{
        name: "Conformance", status: "completed", conclusion: "success",
        steps: [{ name: "Real PostgreSQL", status: "completed", conclusion: "skipped" }],
      }] } };
    },
  } } } });
  const ci = JSON.parse(result.files.get(`${directory}/ci.json`));
  const chart = result.files.get(`${directory}/ci-chart.txt`);
  assert.match(chart, /Contracts C7\s+\|#{7}\s+\| 1\/3 \(33\.3%\)/);
  assert.ok(chart.split("\n").every(line => line.length < 80));
  for (const item of ci.workflows) {
    assert.equal(item.coverage, "complete");
    assert.equal(item.current.observed, 4);
    assert.equal(item.current.completed, 3);
    assert.equal(item.current.pending, 1);
    assert.equal(item.current.success_fraction, 1 / 3);
    assert.deepEqual(item.current.conclusions, { success: 1, failure: 1, cancelled: 1 });
    assert.equal(item.previous.success_fraction, 1 / 2);
    assert.equal(item.latest_jobs.jobs[0].steps[0].conclusion, "skipped");
    assert.equal(item.latest.attempt, 2);
  }
});

test("empty, unknown, overlapping and bounded CI evidence never yields a false green rate", async () => {
  for (const mode of ["empty", "unknown", "overlap", "truncated"]) {
    const files = await evidenceFiles();
    const calls = new Map();
    const result = await execute("Collect comparable CI evidence", { files, github: { rest: { actions: {
      async listWorkflowRuns({ workflow_id, page }) {
        calls.set(workflow_id, (calls.get(workflow_id) || 0) + 1);
        const rows = Array.from({ length: mode === "empty" ? 0 : mode === "unknown" ? 1 : 100 }, (_, index) => ({
          id: (mode === "overlap" ? 0 : page * 100) + index,
          created_at: "2026-11-04T00:00:00Z", status: "completed",
          conclusion: mode === "unknown" ? null : "success", event: "push", head_branch: "main",
        }));
        return { data: { total_count: mode === "overlap" || mode === "truncated" ? 900 : rows.length, workflow_runs: rows } };
      },
      async listJobsForWorkflowRun({ page }) {
        return { data: { total_count: 250, jobs: Array.from({ length: 100 }, (_, index) => ({ name: `${page}-${index}` })) } };
      },
    } } } });
    for (const item of JSON.parse(result.files.get(`${directory}/ci.json`)).workflows) {
      assert.equal(item.current.success_fraction, null, mode);
      assert.equal(item.previous.success_fraction, null, mode);
      assert.ok(calls.get(item.workflow) <= 6);
      if (mode === "overlap" || mode === "truncated") assert.equal(item.coverage, "partial");
      if (mode !== "empty") assert.equal(item.latest_jobs.coverage, "partial");
    }
    const chart = result.files.get(`${directory}/ci-chart.txt`);
    assert.ok(!chart.includes("#"), "unavailable fractions must not be plotted as measured values");
    assert.match(chart, /N\/A/);
  }
  const files = await evidenceFiles();
  await assert.rejects(execute("Collect comparable CI evidence", { files, github: { rest: { actions: {
    async listWorkflowRuns() { throw new Error("403 forbidden"); },
  } } } }), /403 forbidden/);
  assert.equal(files.has(`${directory}/ci.json`), false);
});

test("CI charts distinguish small non-zero success fractions from measured zero", async () => {
  for (const successes of [0, 1]) {
    const rows = Array.from({ length: 100 }, (_, index) => ({
      id: index, created_at: "2026-11-04T00:00:00Z", status: "completed",
      conclusion: index < successes ? "success" : "failure",
      event: "push", head_branch: "main", head_sha: sha,
    }));
    const result = await execute("Collect comparable CI evidence", { files: await evidenceFiles(), github: { rest: { actions: {
      async listWorkflowRuns() { return { data: { total_count: rows.length, workflow_runs: rows } }; },
      async listJobsForWorkflowRun() { return { data: { total_count: 0, jobs: [] } }; },
    } } } });
    const chart = result.files.get(`${directory}/ci-chart.txt`);
    assert.match(chart, successes === 1
      ? /Contracts C7\s+\|\. {19}\| 1\/100 \(1\.0%\)/
      : /Contracts C7\s+\| {20}\| 0\/100 \(0\.0%\)/);
    assert.ok(chart.split("\n").every(line => line.length < 80));
  }
});

test("exact-revision validation stays separate from closed-day CI trends", async () => {
  const files = await evidenceFiles();
  const result = await execute("Collect comparable CI evidence", { files, github: { rest: { actions: {
    async listWorkflowRuns(parameters) {
      if (!parameters.head_sha) return { data: { total_count: 0, workflow_runs: [] } };
      assert.equal(parameters.head_sha, sha);
      assert.equal(parameters.per_page, 1);
      return { data: { total_count: 1, workflow_runs: [{
        id: 100, head_sha: sha, head_branch: "main", event: "push",
        created_at: "2026-11-10T06:00:00Z", status: "completed", conclusion: "success",
      }] } };
    },
    async listJobsForWorkflowRun(parameters) {
      assert.equal(parameters.run_id, 100);
      return { data: { total_count: 1, jobs: [{ name: "Negative scope tests", conclusion: "success" }] } };
    },
  } } } });
  for (const item of JSON.parse(result.files.get(`${directory}/ci.json`)).workflows) {
    assert.equal(item.current.observed, 0);
    assert.equal(item.current.success_fraction, null);
    assert.equal(item.source_revision.sha, sha);
    assert.equal(item.source_revision.validation.jobs[0].conclusion, "success");
  }
});

test("preview observations retain unapproved scope, unassigned owners and no inferred candidate", async () => {
  const github = previewAPI();
  const list = github.rest.actions.listWorkflowRuns;
  github.rest.actions.listWorkflowRuns = parameters => {
    assert.equal(parameters.head_sha, undefined, "main is not an implicit candidate");
    return list(parameters);
  };
  const result = await execute("Collect preview acceptance evidence", { files: await previewFiles(), github });
  const packet = JSON.parse(result.files.get(`${directory}/evidence.json`));
  assert.equal(packet.contract.status, "proposed");
  assert.equal(packet.contract.candidate, null);
  assert.equal(packet.acceptance, "not-determined");
  assert.equal(packet.candidate_release.status, "unknown");
  assert.ok(packet.configuration_blockers.includes("candidate-missing"));
  assert.ok(packet.configuration_blockers.includes("scope-approval-missing"));
  assert.ok(packet.configuration_blockers.includes("recovery-or-retention-decision-missing"));
  assert.equal(packet.configuration_blockers.filter(value => value.startsWith("owner-missing:")).length, 8);
  assert.equal(packet.contract_sha256, createHash("sha256").update(JSON.stringify(previewContract)).digest("hex"));
  assert.equal(packet.signals.length, 7);
  for (const signal of packet.signals) {
    assert.equal(signal.candidate_head_match, null);
    assert.equal(signal.latest.conclusion, "failure");
    assert.equal(signal.latest.jobs[0].steps[0].conclusion, "skipped");
  }
  assert.equal(packet.report_version, 3);
  assert.equal(packet.rubric_version, 2);
  assert.ok(!("reports" in packet), "prior issue bodies are not retained as acceptance evidence");
  const changed = structuredClone(previewContract);
  changed.journeys[0].acceptance += " Changed boundary.";
  const revised = await execute("Collect preview acceptance evidence", { files: await previewFiles(changed), github });
  assert.notEqual(JSON.parse(revised.files.get(`${directory}/evidence.json`)).contract_sha256, packet.contract_sha256);
});

test("candidate evidence separates source matches from installation, deployed revision and promotion acceptance", async () => {
  const contract = { ...previewContract, candidate: { tag: "v0.1.29", sha: "b".repeat(40) } };
  const github = previewAPI();
  github.rest.git.getRef = async () => ({ data: { object: { type: "tag", sha } } });
  github.rest.git.getTag = async ({ tag_sha }) => {
    assert.equal(tag_sha, sha);
    return { data: { object: { type: "commit", sha: contract.candidate.sha } } };
  };
  const result = await execute("Collect preview acceptance evidence", { files: await previewFiles(contract), github });
  const packet = JSON.parse(result.files.get(`${directory}/evidence.json`));
  assert.equal(packet.source_sha, sha);
  assert.equal(packet.contract.candidate.sha, contract.candidate.sha);
  assert.equal(packet.candidate_release.status, "tag-matched");
  assert.equal(packet.candidate_release.tag_sha, contract.candidate.sha);
  assert.equal(packet.acceptance, "not-determined", "even all green candidate source runs cannot approve journeys");
  for (const signal of packet.signals) {
    assert.equal(signal.latest.head_sha, sha);
    assert.equal(signal.latest.conclusion, "failure", "do not replace latest failure with old candidate success");
    assert.equal(signal.candidate_head_match.head_sha, contract.candidate.sha);
    assert.equal(signal.candidate_head_match.conclusion, "success");
    assert.equal(signal.lookup_coverage.candidate, "bounded");
  }
  assert.match(packet.signals.find(row => row.workflow === "release-promote.yml").limitation, /not the promoted release SHA/);
});

test("release mismatches, missing workflows, cancelled and truncated checks cannot imply preview acceptance", async () => {
  const contract = { ...previewContract, candidate: { tag: "v0.1.29", sha: "b".repeat(40) } };
  const github = previewAPI({ candidateSha: "c".repeat(40), conclusion: "cancelled", partial: true });
  const list = github.rest.actions.listWorkflowRuns;
  github.rest.actions.listWorkflowRuns = parameters => {
    if (parameters.workflow_id === "release-promote.yml") throw Object.assign(new Error("not found"), { status: 404 });
    return list(parameters);
  };
  const result = await execute("Collect preview acceptance evidence", { files: await previewFiles(contract), github });
  const packet = JSON.parse(result.files.get(`${directory}/evidence.json`));
  assert.equal(packet.candidate_release.status, "mismatch");
  assert.ok(packet.configuration_blockers.includes("candidate-release-binding-unverified"));
  assert.equal(packet.signals[0].latest.conclusion, "cancelled");
  assert.equal(packet.signals[0].candidate_head_match.job_coverage, "partial");
  const promotion = packet.signals.find(row => row.workflow === "release-promote.yml");
  assert.equal(promotion.latest, null);
  assert.equal(promotion.candidate_head_match, null);
  assert.equal(promotion.lookup_coverage.latest, "unavailable");
  assert.equal(packet.acceptance, "not-determined");
  const missing = previewAPI();
  missing.rest.repos.getReleaseByTag = async () => { throw Object.assign(new Error("not found"), { status: 404 }); };
  const unavailable = await execute("Collect preview acceptance evidence", { files: await previewFiles(contract), github: missing });
  assert.equal(JSON.parse(unavailable.files.get(`${directory}/evidence.json`)).candidate_release.status, "unknown");
});

test("invalid preview contracts and denied evidence fail explicitly without a success-shaped packet", async () => {
  const invalid = [
    { ...previewContract, candidate: { tag: "main", sha } },
    { ...previewContract, candidate: { tag: "v1.0.0", sha: "short" } },
    { ...previewContract, status: "ready" },
    { ...previewContract, journeys: previewContract.journeys.slice(1) },
    { ...previewContract, journeys: previewContract.journeys.map(row => ({ ...row, owner: undefined })) },
    { ...previewContract, journeys: previewContract.journeys.map(row => ({ ...row, signals: ["outside-repository.yml"] })) },
  ];
  for (const contract of invalid) {
    const files = await previewFiles(contract);
    await assert.rejects(execute("Collect preview acceptance evidence", { files, github: previewAPI() }), /Invalid private-preview contract/);
    assert.equal(files.has(`${directory}/evidence.json`), false);
  }
  const files = await previewFiles();
  const github = previewAPI();
  github.rest.actions.listWorkflowRuns = async () => { throw Object.assign(new Error("403 forbidden"), { status: 403 }); };
  await assert.rejects(execute("Collect preview acceptance evidence", { files, github }), /403 forbidden/);
  assert.equal(files.has(`${directory}/evidence.json`), false);
});

test("wrong-SHA and pull-request observations cannot stand in for absent candidate evidence", async () => {
  const contract = { ...previewContract, candidate: { tag: "v0.1.29", sha: "b".repeat(40) } };
  const github = previewAPI();
  const list = github.rest.actions.listWorkflowRuns;
  github.rest.actions.listWorkflowRuns = async parameters => {
    const response = await list(parameters);
    if (parameters.head_sha) {
      const row = response.data.workflow_runs[0];
      response.data.workflow_runs = [
        { ...row, head_sha: sha },
        { ...row, event: "pull_request" },
        { ...row, head_branch: "feature" },
      ];
    }
    return response;
  };
  const result = await execute("Collect preview acceptance evidence", { files: await previewFiles(contract), github });
  const packet = JSON.parse(result.files.get(`${directory}/evidence.json`));
  assert.ok(packet.signals.every(row => row.candidate_head_match === null));
  assert.equal(packet.acceptance, "not-determined");
});

test("component fluctuation measures real frozen Git history without conflating source, tests and generation", async () => {
  const cwd = mkdtempSync(join(tmpdir(), "cao-framework-health-"));
  const git = (...args) => execFileSync("git", args, { cwd, encoding: "utf8" }).trim();
  const commit = (date) => {
    git("add", ".");
    execFileSync("git", ["-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "fixture"], {
      cwd, env: { ...process.env, GIT_AUTHOR_DATE: date, GIT_COMMITTER_DATE: date },
    });
  };
  try {
    git("init", "-q");
    mkdirSync(join(cwd, ".github/workflows/shared"), { recursive: true });
    writeFileSync(join(cwd, ".github/workflows/shared/policy.mjs"), "baseline\n");
    commit("2026-10-28T12:00:00Z");
    writeFileSync(join(cwd, ".github/workflows/shared/policy.mjs"), "baseline\nnew behavior\n");
    mkdirSync(join(cwd, "tests/unit"), { recursive: true });
    writeFileSync(join(cwd, "tests/unit/control-policy.test.mjs"), "positive\nnegative\n");
    mkdirSync(join(cwd, "server/spec/generated"), { recursive: true });
    writeFileSync(join(cwd, "server/spec/generated/openapi.json"), "{}\n");
    mkdirSync(join(cwd, "activity"));
    writeFileSync(join(cwd, "activity/collector.mjs"), "behavior\n".repeat(100));
    commit("2026-11-04T12:00:00Z");
    const files = await evidenceFiles();
    const data = JSON.parse(files.get(`${directory}/context.json`));
    files.set(`${directory}/context.json`, JSON.stringify({ ...data, source_sha: git("rev-parse", "HEAD") }));
    const result = await execute("Measure core component fluctuation", {
      files, exec(command, args, options) {
        assert.equal(command, "git");
        return execFileSync(command, args, { ...options, cwd });
      },
    });
    const components = JSON.parse(result.files.get(`${directory}/components.json`)).components;
    const control = components.find(item => item.id === "control");
    assert.equal(control.current.coverage, "complete");
    assert.equal(control.current.commits, 1);
    assert.equal(control.current.source_added, 1);
    assert.equal(control.current.test_added, 2);
    assert.equal(control.previous.source_added, 1);
    const hosting = components.find(item => item.id === "hosting");
    assert.equal(hosting.current.generated_files_changed, 1);
    assert.equal(hosting.current.source_added, 0);
    const chart = result.files.get(`${directory}/component-chart.txt`);
    assert.match(chart, /control\s+P7 \|\. {9}\| 1\s+C7 \|\. {9}\| 1/);
    assert.match(chart, /acquisition\s+P7 \| {10}\| 0\s+C7 \|#{10}\| 100/);
    assert.ok(chart.split("\n").every(line => line.length < 80));
    const claims = components.flatMap(item => item.claims);
    assert.equal(new Set(claims).size, 16);
    for (const claim of claims) assert.ok(source.includes(`\`${claim}\``), `missing assessed claim: ${claim}`);
    files.set(`${directory}/context.json`, JSON.stringify({ ...data, source_sha: sha }));
    await assert.rejects(execute("Measure core component fluctuation", {
      files, exec: () => "unexpected-sha",
    }), /complete history at the assessed SHA/);
  } finally {
    rmSync(cwd, { recursive: true, force: true });
  }
});

test("compiled framework reports remain local read-only observation with only deduplicated report writes", () => {
  const compiled = parse(workflow("cao-framework-health.lock.yml"));
  assert.equal(config.private, true);
  assert.equal(config["max-daily-ai-credits"], -1);
  assert.equal(config.on.schedule, "daily");
  assert.match(config.if, /github\.repository == 'githubnext\/gh-cao'/);
  assert.match(config.if, /github\.event\.repository\.default_branch/);
  assert.equal(config.imports, undefined);
  assert.equal(controlPolicy["control-plane"].workflows["cao-framework-health"], undefined);
  assert.equal(controlPolicy["control-plane"].campaigns["cao-framework-health"], undefined);
  assert.deepEqual(config.network.allowed, []);
  assert.equal(config.tools.edit, false);
  assert.equal(config.tools.github["github-token"], "${{ secrets.GITHUB_TOKEN }}");
  assert.deepEqual(config.tools.github.toolsets, ["repos", "pull_requests", "actions"]);
  assert.equal(config["safe-outputs"]["github-token"], "${{ secrets.GITHUB_TOKEN }}");
  assert.ok(!config.tools.bash.includes("*"));
  assert.equal(config.checkout["fetch-depth"], 0);
  assert.equal(config.permissions["copilot-requests"], "write");
  for (const [permission, access] of Object.entries(config.permissions)) {
    if (permission !== "copilot-requests") assert.equal(access, "read");
  }
  for (const [permission, access] of Object.entries(compiled.jobs.agent.permissions)) {
    if (permission !== "copilot-requests") assert.equal(access, "read");
  }
  assert.equal(compiled.jobs.safe_outputs.permissions.issues, "write");
  const handler = compiled.jobs.safe_outputs.steps.find(step => step.env?.GH_AW_SAFE_OUTPUTS_HANDLER_CONFIG);
  const outputs = JSON.parse(handler.env.GH_AW_SAFE_OUTPUTS_HANDLER_CONFIG);
  assert.equal(outputs.create_issue.max, 1);
  assert.equal(outputs.create_issue.close_older_issues, true);
  assert.equal(outputs.create_issue.deduplicate_by_title, true);
  assert.equal(config["safe-outputs"]["report-failure-as-issue"], false);
  assert.equal(config["safe-outputs"]["report-missing-tool"]["create-issue"], false);
  assert.equal(config["safe-outputs"]["report-incomplete"]["create-issue"], false);
  for (const job of Object.values(compiled.jobs)) {
    for (const step of job.steps || []) {
      assert.notEqual(step.env?.GH_AW_MISSING_TOOL_CREATE_ISSUE, "true",
        "missing-tool diagnostics must not create issues outside the bounded report handler");
    }
  }
  assert.equal(outputs.create_report_incomplete_issue, undefined);
  assert.equal(outputs.noop["report-as-issue"], "false");
  for (const forbidden of ["update_issue", "create_pull_request", "dispatch_workflow", "add_comment", "push_to_pull_request_branch"]) {
    assert.equal(outputs[forbidden], undefined);
  }
  assert.ok(compiled.on.schedule[0].cron);
  assert.ok(!compiled.jobs.activation.steps.some(step => step.id === "daily-ai-credits-workflow-guardrail"),
    "compiled runs must not be gated by daily AI Credit consumption");
  const compiledScope = compiled.jobs.agent.steps.find(step => step.name === "Fix scope, window and report history");
  assert.equal(compiledScope.with.script, config.steps[0].with.script);
  const artifact = config.steps.find(step => step.name === "Retain deterministic framework evidence");
  assert.equal(artifact.with["retention-days"], 90);
  assert.equal(artifact.with["if-no-files-found"], "error");
  assert.equal(artifact.with.name, "framework-health-evidence-${{ github.run_id }}-${{ github.run_attempt }}");
  assert.equal(artifact.with.path.trim().split("\n").length, 3);
  assert.ok(compiled.jobs.agent.steps.some(step =>
    step.name === artifact.name && /^actions\/upload-artifact@[a-f0-9]{40}$/.test(step.uses)));
  const architecture = readFileSync(join(root, "ARCHITECTURE.md"), "utf8");
  assert.match(architecture, /Daily core-health priorities/);
  assert.match(architecture, /cao-framework-health\.md/);
  assert.match(source, /changed definitions break\ncomparability/);
  assert.match(source, /A passing run at a different SHA does not validate this SHA/);
  assert.match(source, /mapped but not executed/);
  assert.match(source, /source reduction that removes a\nguarantee or tests is not a cleaner core/);
});

test("source review is required independently of candidate acceptance and incomplete baselines", () => {
  const review = source.split("## Required source-review pass\n")[1]?.split("\n## Private-preview acceptance first")[0];
  assert.ok(review, "the report must investigate source instead of only repeating collector metadata");
  assert.match(review, /missing candidate, unapproved scope or unassigned owner blocks journey\nacceptance, not review of the core at `source_sha`/);
  assert.match(review, /each component's `paths` in `components\.json`/);
  assert.match(review, /read its governing specification, implementation\nand representative behavioral tests/);
  assert.match(review, /all eight components and account for\nall 16 claims within the existing file budgets/);
  assert.match(review, /0\/16 mapped claims is not a baseline/);
  for (const field of ["Review", "Coverage", "Verdict", "Remaining proof"]) {
    assert.ok(review.includes(`**${field}:**`), `missing review field: ${field}`);
  }
  assert.match(review, /Reviewed source with no matching execution remains mapped but not executed/);
  assert.match(review, /Trace suite membership through the workflow and runner scripts/);
  assert.match(review, /Never manufacture an evidenced verdict to reduce unknowns/);
  assert.match(source, /if no component received substantive specification\/source\/test review, call\n`report_incomplete` and do not call `create_issue`/);
  const template = /````markdown\n([\s\S]*?)\n````/.exec(source)[1];
  assert.match(template, /Reviewed \/ 16, mapped \/ 16 and exact-revision execution \/ 16/);
  assert.match(template.split("### Executive assessment")[0], /^Core claims reviewed: \{reviewed count\}\/16$/m);
});

test("publication rejects missing, zero, duplicate and invalid review declarations before issue mutation", async () => {
  const path = "/tmp/framework-health-agent-output.json";
  const validate = output => execute("Validate core review before publication", {
    files: new Map([[path, JSON.stringify(output)]]), env: { GH_AW_AGENT_OUTPUT: path },
  });
  for (const body of [
    "Incomplete assessment; source and tests were not reviewed.",
    "Core claims reviewed: 0/16",
    "Core claims reviewed: 17/16",
    "Core claims reviewed: -1/16",
    "Core claims reviewed: 1.5/16",
    "Core claims reviewed: 1/15",
    "Core claims reviewed: 1/16\nCore claims reviewed: 16/16",
    "Core claims reviewed: 16/16\nCore claims reviewed: 0/16",
  ]) {
    await assert.rejects(validate({ items: [{ type: "create_issue", body }] }), /Refusing report/);
  }
  await assert.rejects(validate({ items: {} }), /Invalid agent output/);
  await assert.rejects(execute("Validate core review before publication"), /Missing agent output/);
  for (const count of [1, 8, 16]) {
    await validate({ items: [{
      type: "create_issue",
      body: `Core claims reviewed: ${count}/16\r\nAll verdicts unknown; tests mapped but not executed.`,
    }] });
  }
  for (const type of ["noop", "report_incomplete"]) await validate({ items: [{ type }] });

  const compiled = parse(workflow("cao-framework-health.lock.yml"));
  const steps = compiled.jobs.safe_outputs.steps;
  const guard = steps.findIndex(step => step.name === "Validate core review before publication");
  const handler = steps.findIndex(step => step.env?.GH_AW_SAFE_OUTPUTS_HANDLER_CONFIG);
  assert.ok(guard >= 0 && guard < handler, "validation must run before issue creation and close-older");
  assert.equal(steps[guard].env.GH_AW_AGENT_OUTPUT,
    "${{ steps.setup-agent-output-env.outputs.GH_AW_AGENT_OUTPUT }}");
  assert.equal(steps[guard].if, undefined);
  assert.equal(steps[guard]["continue-on-error"], undefined);
  assert.equal(steps[handler].if, undefined, "publication must retain default success gating");
});

test("failure investigation is bounded, read-only and does not publish raw evidence", () => {
  assert.equal(config.permissions.actions, "read");
  assert.match(source, /at most three failing\njobs already identified in the collected evidence/);
  assert.match(source, /at most 100 lines per job/);
  assert.match(source, /Follow the workflow's selectors and failing assertion before classifying\nharness versus product failure/);
  assert.match(source, /If the excerpt is insufficient, name the exact\njob\/step and next diagnostic rather than guessing/);
  assert.match(source, /Never publish raw logs,\ncredentials or private data/);
  assert.match(source, /Do not\nrecollect broad run history or download artifacts/);
});

test("executive reports retain a fixed opening, section order and non-nested progressive disclosure", () => {
  const template = /````markdown\n([\s\S]*?)\n````/.exec(source)?.[1];
  assert.ok(template);
  assert.match(template, /^\*\*CAO Private Preview - Daily Executive Evidence Report\*\*/);
  assert.match(template, /\*\*Reporting day:\*\*[\s\S]*\*\*Assessed revision:\*\*/);
  assert.match(template, /\| Candidate \|/);
  assert.match(template, /\*\*Profile:\*\*/);
  assert.match(template, /\*\*Purpose:\*\* Can an invited operator safely install, use, operate and leave/);
  assert.deepEqual([...template.matchAll(/^### (.+)$/gm)].map(match => match[1]), [
    "Executive assessment", "Preview journey scorecard", "Blockers and next increments",
    "Core health and stability", "Acceptance evidence and participant outcomes", "Method and provenance",
  ]);
  let depth = 0;
  for (const token of template.matchAll(/<details>|<\/details>/g)) {
    depth += token[0] === "<details>" ? 1 : -1;
    assert.ok(depth >= 0 && depth <= 1, "disclosures must be balanced and non-nested");
  }
  assert.equal(depth, 0);
  assert.equal([...template.matchAll(/<details>/g)].length, 3);
  assert.match(source, /copy `ci-chart\.txt` and `component-chart\.txt` verbatim/);
  assert.match(source, /never plot unknowns as zero/);
  assert.match(source, /Required unknowns block a recommendation to expand/);
  assert.match(source, /the report never invites participants, approves risk or declares launch readiness/);
  assert.match(source, /The template and collector tests do not validate model-written conclusions/);
  for (const journey of previewContract.journeys) {
    assert.equal(journey.owner, null);
    assert.ok(journey.acceptance);
  }
});

test("at-a-glance reports use labelled statuses, complete journey rows and short decision tables", async () => {
  const template = /````markdown\n([\s\S]*?)\n````/.exec(source)[1];
  const overview = template.split("### Core health and stability")[0];
  assert.match(template, /\*\*Report \/ rubric version:\*\* 3 \/ 2/);
  const files = await evidenceFiles();
  const metadata = JSON.parse(files.get(`${directory}/context.json`));
  assert.equal(metadata.report_version, 3);
  assert.equal(metadata.rubric_version, 2, "visual changes must not change acceptance criteria");
  assert.match(overview, /\| At a glance \| Today \|/);
  for (const field of ["Preview evidence", "Candidate", "Change", "Confidence"]) {
    assert.ok(overview.includes(`| ${field} |`), `missing summary field: ${field}`);
  }
  assert.match(overview, /> \[!NOTE\]\n> \*\*Next decision:\*\*/);
  assert.match(overview, /✅ Passed · ❌ Failed · ❔ Unknown/);
  const journeys = overview.split("### Preview journey scorecard")[1].split("### Blockers and next increments")[0];
  assert.match(journeys, /\| Operator step \| Evidence \| Next check \/ owner \|/);
  assert.deepEqual([...journeys.matchAll(/^\| [^|\n]+ \(`([^`]+)`\) \|/gm)].map(match => match[1]),
    previewContract.journeys.map(journey => journey.id));
  assert.match(overview, /\| Priority \/ journey \| Next action → closure check \| Owner \|/);
  assert.match(overview, /\| \{rank \/ journey ID\} \| \{next action → observable closure\} \| \{owner or unassigned\} \|/);
  assert.match(source, /Always pair each icon with its text label/);
  assert.match(source, /Unknown is not failed/);
  assert.match(source, /Report format changes alone do not invalidate/);
  assert.match(source, /at most 12 words per next check/);
  assert.doesNotMatch(source, /no emoji status indicators/);
});
