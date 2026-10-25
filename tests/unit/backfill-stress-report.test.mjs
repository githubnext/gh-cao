import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { BACKFILL_STRESS_REPOSITORIES, buildBackfillStressReport, readBackfillStressCases } from "../../scripts/report-backfill-stress.mjs";

function passedCase(repositories) {
  const expected = repositories * 7;
  return {
    repositories,
    status: { repositories, status: "success" },
    report: {
      repositories, runsPerDay: 1, horizonDays: 7, expectedRuns: expected, queuedRuns: expected,
      counts: { $runs: Math.min(expected, 200_000), $repositories: repositories, $workflows: repositories },
      backfillDurationMs: 125800, webhooksDuringBackfill: 100,
      webhooks: { failed: 0, accepted: 120, attempts: 120, retries: 2 },
    },
  };
}

const context = {
  repository: "owner/repo", serverUrl: "https://github.example.com",
  runId: 42, runAttempt: "1", sha: "abcdef", result: "success",
};

test("daily report requires all retained matrix cases and exact evidence", () => {
  const body = buildBackfillStressReport({ ...context, cases: BACKFILL_STRESS_REPOSITORIES.map(passedCase) });
  assert.match(body, /Daily synthetic backfill: PASSED/);
  assert.match(body, /50,000.*350000.*125\.8/);
  assert.match(body, /https:\/\/github\.example\.com\/owner\/repo\/actions\/runs\/42/);
  assert.doesNotMatch(body, /100,000/);
});

test("missing and malformed completion evidence never reports success", () => {
  for (const modify of [
    (entries) => entries.pop(),
    (entries) => { entries[2].report = null; },
    (entries) => { entries[2].report.queuedRuns--; },
    (entries) => { entries[2].report.counts.$runs++; },
    (entries) => { entries[2].report.webhooksDuringBackfill = 0; },
    (entries) => { entries[2].report.webhooks.failed = 1; },
    (entries) => { entries[2].status.status = "failure"; },
  ]) {
    const cases = BACKFILL_STRESS_REPOSITORIES.map(passedCase);
    modify(cases);
    assert.match(buildBackfillStressReport({ ...context, cases }), /FAILED \/ INCOMPLETE/);
  }
  assert.match(buildBackfillStressReport({
    ...context, result: "failure", cases: BACKFILL_STRESS_REPOSITORIES.map(passedCase),
  }), /FAILED \/ INCOMPLETE/);
});

test("artifact reader preserves separate case directories and exposes missing artifacts", (t) => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "backfill-report-"));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const first = passedCase(1000);
  const folder = path.join(directory, "postgres-backfill-stress-1000");
  fs.mkdirSync(folder);
  fs.writeFileSync(path.join(folder, "case-status.json"), JSON.stringify(first.status));
  fs.writeFileSync(path.join(folder, "stress-1000.json"), JSON.stringify(first.report));
  const cases = readBackfillStressCases(directory);
  assert.deepEqual(cases[0], first);
  assert.equal(cases[1].report, null);
  assert.equal(cases[2].status, null);
});

test("full stress workflow is daily-only and keeps issue-write outside the test jobs", () => {
  const workflow = fs.readFileSync(".github/workflows/backfill-stress.yml", "utf8");
  assert.match(workflow, /workflow_call:/);
  assert.doesNotMatch(workflow, /pull_request:|push:/);
  const daily = fs.readFileSync(".github/workflows/ci-daily.yml", "utf8");
  assert.match(daily, /uses: \.\/\.github\/workflows\/backfill-stress\.yml/);
  assert.match(workflow, /repositories: \[1000, 10000, 50000\]/);
  assert.match(workflow, /needs: backfill-stress/);
  assert.match(workflow, /issues: write/);
  assert.doesNotMatch(workflow.split("  report:")[0], /issues: write/);
});

test("daily stress includes the complete monthly agent budget scenario", () => {
  const workflow = fs.readFileSync(".github/workflows/backfill-stress.yml", "utf8");
  const pkg = JSON.parse(fs.readFileSync("package.json", "utf8"));
  assert.match(workflow, /monthly-budget-stress:/);
  assert.match(workflow, /30 days, 10000 repositories, \$25000\/day synthetic agent budget/);
  assert.match(workflow, /npm run test:stress:monthly-budget/);
  assert.match(workflow, /name: postgres-monthly-budget-stress/);
  assert.match(workflow, /FAILED \/ INCOMPLETE: no completed monthly report/);
  assert.doesNotMatch(workflow.split("  backfill-stress:")[0], /secrets\.|continue-on-error:/);
  assert.equal(pkg.scripts["test:stress:monthly-budget"],
    "CAO_MONTHLY_BUDGET_STRESS=1 go -C server test ./internal/postgresx -run '^TestMonthlyBudgetStress$' -count=1 -timeout=40m -v");
});
