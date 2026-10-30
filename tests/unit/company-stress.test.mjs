import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import test from "node:test";

const execute = promisify(execFile);
const launcher = resolve("scripts/company-stress.mjs");

test("company stress is opt-in and preserves the existing fleet command", async () => {
  const workflow = await readFile(".github/workflows/company-stress.yml", "utf8");
  assert.match(workflow, /workflow_dispatch:/);
  assert.match(workflow, /workflow_call:/);
  assert.doesNotMatch(workflow, /schedule:|pull_request:|secrets\.|gh aw run/);
  assert.match(workflow, /max_locks_per_transaction=1024/);
  assert.match(workflow, /if: always\(\)/);
  const pkg = JSON.parse(await readFile("package.json", "utf8"));
  assert.equal(pkg.scripts["test:stress:company"], "node scripts/company-stress.mjs");
  assert.match(pkg.scripts["test:stress:cao"], /TestGitHubSimulatorFleetStress/);
});

test("company launcher overrides every input and isolates credential environment", async () => {
  const directory = await mkdtemp(join(tmpdir(), "company-stress-"));
  try {
    await writeFile(join(directory, "go"), `#!/usr/bin/env node
import { writeFileSync } from 'node:fs';
if (process.env.GH_TOKEN || process.env.GITHUB_TOKEN || process.env.GH_ENTERPRISE_TOKEN) process.exit(2);
writeFileSync(process.env.HARNESS_TRACE, JSON.stringify({
  scenario: JSON.parse(process.env.CAO_COMPANY_SCENARIO),
  args: process.argv.slice(2),
  report: process.env.CAO_COMPANY_REPORT_DIR
}));
`, { mode: 0o700 });
    const trace = join(directory, "trace.json");
    await execute(process.execPath, [
      launcher, "--preset", "large", "--repositories", "7", "--developers", "3",
      "--prs-per-developer-per-day", "2", "--aw-runs-per-pr", "4", "--days", "2",
      "--aic-per-run", "0.25", "--seed", "1", "--distribution", "burst",
      "--timeout", "2m", "--report-dir", directory,
    ], { env: {
      ...process.env, PATH: `${directory}:${process.env.PATH}`, HARNESS_TRACE: trace,
      POSTGRES_URL: "postgres://127.0.0.1:5432/company",
      GH_TOKEN: "synthetic-fixture", GITHUB_TOKEN: "synthetic-fixture", GH_ENTERPRISE_TOKEN: "synthetic-fixture",
    } });
    const result = JSON.parse(await readFile(trace, "utf8"));
    assert.deepEqual(result.scenario, {
      version: 1, name: "large", repositories: 7, developers: 3, prsPerDeveloperPerDay: 2,
      awRunsPerPR: 4, days: 2, aicPerRun: 0.25, seed: 1, distribution: "burst",
    });
    assert.ok(result.args.includes("-timeout=2m"));
    assert.equal(result.report, directory);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("company launcher rejects remote PostgreSQL and leaves an incomplete report", async () => {
  const directory = await mkdtemp(join(tmpdir(), "company-stress-"));
  try {
    await assert.rejects(execute(process.execPath, [launcher, "--report-dir", directory], {
      env: { ...process.env, POSTGRES_URL: "postgres://example.invalid/company" },
    }), /numeric loopback/);
    const report = JSON.parse(await readFile(join(directory, "company-stress.json"), "utf8"));
    assert.equal(report.status, "incomplete");
    await assert.rejects(execute(process.execPath, [
      launcher, "--report-dir", directory, "--developers", "NaN",
    ]), /finite number/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
