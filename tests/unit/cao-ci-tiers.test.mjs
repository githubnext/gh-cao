import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { parse } from "yaml";
import { changedFiles, testPlan } from "../../scripts/ci-test-plan.mjs";
import { buildCIReport, reportCIFailure } from "../../scripts/ci-report.mjs";
import { unitTestAreas } from "../../scripts/run-unit-area.mjs";

const load = (name) => parse(readFileSync(`.github/workflows/${name}`, "utf8"));

test("documentation-only contract plans omit integration but unknown changes widen coverage", () => {
  assert.deepEqual(testPlan(["docs/glossary.md"]), {
    matrix: { area: ["documentation"] }, integration: false, documentation: true,
  });
  assert.deepEqual(testPlan(["new-campaign/aw.yml"]).matrix.area, Object.keys(unitTestAreas));
  assert.deepEqual(testPlan(["scripts/ci-test-plan.mjs"]).matrix.area, Object.keys(unitTestAreas));
  assert.deepEqual(testPlan(["package-lock.json"]).matrix.area, Object.keys(unitTestAreas));
  assert.deepEqual(testPlan(["tests/unit/workflow-contract-ci-triggers.test.mjs"]).matrix.area, ["workflows"]);
  assert.deepEqual(testPlan(["dashboard/report/agent-catalog.mjs"]).matrix.area, ["dashboard", "tooling"]);
  assert.deepEqual(testPlan(["server/internal/server/server.go"]).matrix.area, ["dashboard", "tooling"]);
  assert.deepEqual(testPlan(["docs/glossary.md", "dashboard/report/agent-catalog.mjs"]).matrix.area,
    ["dashboard", "tooling", "documentation"]);
  assert.deepEqual(testPlan(["docs/glossary.md"], true).matrix.area, Object.keys(unitTestAreas));
  assert.deepEqual(testPlan([]).matrix.area, Object.keys(unitTestAreas));
});

test("missing comparison bases select all areas and daily/manual events bypass diffs", () => {
  assert.equal(changedFiles("", "HEAD"), null);
  assert.equal(changedFiles("0".repeat(40), "HEAD"), null);
  assert.equal(changedFiles("f".repeat(40), "HEAD"), null);
  for (const event of ["schedule", "workflow_dispatch"]) {
    const result = spawnSync(process.execPath, ["scripts/ci-test-plan.mjs"], {
      encoding: "utf8",
      env: { ...process.env, GITHUB_EVENT_NAME: event, BASE_SHA: "not-a-ref", GITHUB_OUTPUT: "" },
    });
    assert.equal(result.status, 0, result.stderr);
    assert.ok(result.stdout.includes(`matrix=${JSON.stringify(testPlan([], true).matrix)}`));
  }
});

test("daily calls every suite at the same revision and reports even on failure", () => {
  const daily = load("ci-daily.yml");
  assert.equal(daily.on.schedule.length, 1);
  assert.equal(daily.concurrency["cancel-in-progress"], false);
  const suites = Object.entries(daily.jobs).filter(([, job]) => job.uses);
  assert.equal(suites.length, 15);
  for (const [id, job] of suites) {
    assert.match(job.uses, /^\.\/\.github\/workflows\/[^/]+\.yml$/);
    assert.equal(job.needs, "revision");
    assert.ok(Object.hasOwn(load(job.uses.split("/").at(-1)).on, "workflow_call"), id);
    const called = load(job.uses.split("/").at(-1));
    const ranks = { none: 0, read: 1, write: 2 };
    const granted = job.permissions ?? daily.permissions;
    for (const permissions of [called.permissions, ...Object.values(called.jobs).map((entry) => entry.permissions)]) {
      for (const [scope, access] of Object.entries(permissions ?? {})) {
        assert.ok((ranks[granted[scope]] ?? 0) >= ranks[access], `${id} requires ${scope}: ${access}`);
      }
    }
    assert.ok(daily.jobs.report.needs.includes(id), `${id} must appear in aggregate report`);
    assert.equal(job.secrets, undefined, "test suites do not inherit deployment secrets");
  }
  assert.equal(daily.jobs.report.if, "always()");
  assert.equal(daily.jobs.report.permissions.issues, "write");
  assert.equal(daily.permissions.issues, undefined);
  const publish = daily.jobs.report.steps.find((step) => step.name === "Publish suite outcomes and slowest jobs");
  assert.equal(publish.env.REPORT_FAILURE,
    "${{ github.ref == 'refs/heads/main' && needs.revision.result == 'success' }}");
  assert.match(publish.with.script, /if \(process\.env\.REPORT_FAILURE === 'true'\) await reportCIFailure/);
  assert.match(publish.with.script, /if \(report\.status !== 'success'\) core\.setFailed/);
  assert.deepEqual(daily.jobs.deployed.with, { full: true });
  assert.deepEqual(daily.jobs["company-stress"].with, { preset: "large" });
  assert.equal(daily.jobs["worker-memory"].needs, "revision");
  const memorySteps = daily.jobs["worker-memory"].steps;
  assert.ok(memorySteps.some((step) => step.uses?.startsWith("actions/setup-go@")));
  assert.ok(memorySteps.some((step) => step.run?.includes("npm --prefix dashboard/site ci")));
  for (const name of ["actions.yml", "dashboard-views.yml", "cao-bootstrap.yml",
    "cao-fleet-stress.yml", "company-stress.yml", "backfill-stress.yml", "dashboard-massive-scale.yml"]) {
    assert.equal(load(name).on.schedule, undefined, `${name} must not duplicate the daily schedule`);
  }
});

test("daily/manual campaign validation cannot pass with quota-limited evidence", () => {
  const steps = load("workflow-contracts.yml").jobs["campaign-lifecycle"].steps;
  for (const name of ["Check campaign API capacity", "Run campaign lifecycle integration"]) {
    const step = steps.find((entry) => entry.name === name);
    assert.equal(step.env.REQUIRE_COMPLETE,
      "${{ github.event_name == 'schedule' || github.event_name == 'workflow_dispatch' }}");
    assert.ok(step.run.includes('if [[ "$REQUIRE_COMPLETE" == "true" ]]; then exit 1; fi'));
  }
});

test("frequent synthetic UI checks stay separate from daily operational benchmarks", () => {
  const deployed = load("dashboard-deployed-integration.yml");
  assert.ok(deployed.on.schedule);
  assert.match(deployed.jobs.integration.name, /synthetic/i);
  assert.doesNotMatch(JSON.stringify(deployed.jobs.integration),
    /test:performance:dashboard-quer|dashboard:data:download|cao-activity-index|DASHBOARD_DATA_URL/);
  assert.equal(deployed.jobs["query-performance"], undefined);
  assert.equal(deployed.jobs["query-cost"].if, "inputs.full == true || (github.event_name != 'schedule' && vars.CAO_DASHBOARD_DATA_URL != '')");
  assert.equal(deployed.on.workflow_call.inputs.full.default, false);
  assert.deepEqual(load("ci-daily.yml").jobs.deployed.with, { full: true });
});

test("daily reporting counts runner time, ranks slow jobs, and rejects incomplete suites", () => {
  const jobs = [
    { name: "fast | job", status: "completed", conclusion: "success", started_at: "2026-01-01T00:00:00Z",
      completed_at: "2026-01-01T00:00:30Z", html_url: "https://example.com/jobs/1" },
    { name: "slow", status: "completed", conclusion: "failure", started_at: "2026-01-01T00:00:00Z",
      completed_at: "2026-01-01T00:02:00Z", html_url: "https://example.com/jobs/2" },
    { name: "skipped", status: "completed", conclusion: "skipped" },
    { name: "report", status: "in_progress" },
  ];
  const report = buildCIReport({ first: { result: "success" }, second: { result: "failure" } }, jobs, "https://example.com/run", "abc");
  assert.equal(report.status, "incomplete");
  assert.equal(report.runnerMinutes, 2.5);
  assert.deepEqual(report.jobs.map((job) => job.name), ["slow", "fast | job"]);
  assert.ok(report.markdown.includes("fast \\| job"));
  for (const result of ["failure", "cancelled", "skipped", undefined]) {
    assert.equal(buildCIReport({ suite: { result } }, [], "", "").status, "incomplete");
  }
  assert.equal(buildCIReport({}, [], "", "").status, "incomplete");
  assert.equal(buildCIReport({ suite: { result: "success" } }, [], "", "").status, "incomplete");
  assert.equal(buildCIReport({ suite: { result: "success" } }, jobs, "", "").status, "incomplete");
  assert.equal(buildCIReport({ suite: { result: "success" } }, [jobs[0]], "", "").status, "success");
});

test("daily validation opens one failure issue and updates it with the latest evidence", async () => {
  const repo = { owner: "octo", repo: "control" };
  const calls = [];
  const open = [{ number: 1, title: "Daily full validation failed", body: "unrelated" }];
  const github = {
    rest: {
      issues: {
        listForRepo: Symbol("list"),
        create: async (request) => {
          calls.push(["create", request]);
          open.push({ number: 2, ...request });
        },
        update: async (request) => calls.push(["update", request]),
      },
    },
    paginate: async (method, request) => {
      assert.equal(method, github.rest.issues.listForRepo);
      assert.deepEqual(request, { ...repo, state: "open", creator: "github-actions[bot]", per_page: 100 });
      return open;
    },
  };
  const report = buildCIReport({ server: { result: "failure" } }, [], "https://example.com/run", "first-sha");
  await reportCIFailure(github, repo, report);
  assert.equal(calls.length, 1);
  assert.equal(calls[0][0], "create");
  assert.equal(calls[0][1].title, "Daily full validation failed");
  assert.ok(calls[0][1].body.includes(report.markdown));
  const latest = buildCIReport({ server: { result: "cancelled" } }, [], "https://example.com/retry", "next-sha");
  await reportCIFailure(github, repo, latest);
  assert.deepEqual(calls[1], ["update", {
    ...repo, issue_number: 2, body: `<!-- cao-daily-validation-failure -->\n\n${latest.markdown}`,
  }]);
  // A copied marker in a PR must not turn a pull request into the failure tracker.
  open[1].pull_request = {};
  await reportCIFailure(github, repo, latest);
  assert.equal(calls[2][0], "create");
});

test("successful daily validation does not call the issue API", async () => {
  const job = {
    name: "server", status: "completed", conclusion: "success",
    started_at: "2026-01-01T00:00:00Z", completed_at: "2026-01-01T00:00:30Z",
  };
  const report = buildCIReport({ server: { result: "success" } }, [job], "", "");
  assert.equal(report.status, "success");
  await reportCIFailure({}, {}, report);
});

test("daily failure reporting propagates issue API errors", async () => {
  const error = new Error("issue API unavailable");
  const report = buildCIReport({ server: { result: "failure" } }, [], "", "");
  for (const operation of ["list", "create", "update"]) {
    const github = {
      rest: {
        issues: {
          listForRepo: Symbol("list"),
          create: async () => { throw error; },
          update: async () => { throw error; },
        },
      },
      paginate: async () => {
        if (operation === "list") throw error;
        return operation === "create" ? [] : [{ number: 2, body: "<!-- cao-daily-validation-failure -->" }];
      },
    };
    await assert.rejects(reportCIFailure(github, {}, report), error);
  }
});
