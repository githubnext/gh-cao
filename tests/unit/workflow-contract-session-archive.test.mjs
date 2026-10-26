import assert from "node:assert/strict";
import { posix } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { workflow } from "./workflow-contract.helpers.mjs";

test("Parquet and session archive conformance has an independent read-only workflow", () => {
  const archive = parse(workflow("session-archive.yml"));
  const server = parse(workflow("cgo.yml"));
  assert.equal(archive.name, "Parquet and Session Archive CI");
  assert.deepEqual(archive.permissions, { contents: "read" });
  assert.deepEqual(Object.keys(archive.jobs), ["session-archive", "session-archive-kusto"]);
  assert.equal(server.jobs["session-archive"], undefined);
  assert.equal(server.jobs["session-archive-kusto"], undefined);
  assert.equal(server.on.workflow_dispatch?.inputs?.accept_kusto_emulator_eula, undefined);
  assert.notEqual(archive.concurrency.group, server.concurrency.group);
  assert.equal(archive.concurrency["cancel-in-progress"], "${{ github.event_name == 'pull_request' }}");
  assert.ok(Object.hasOwn(archive.on, "workflow_call"));
  assert.deepEqual(archive.on.push.branches, ["main"]);
  assert.deepEqual(archive.on.push.paths, archive.on.pull_request.paths);
  for (const file of [
    ".github/workflows/session-archive.yml",
    ".github/workflows/ci-daily.yml",
    "server/internal/sessionarchive/archive.go",
    "server/internal/sessionarchiveblob/query.go",
    "server/internal/server/session_archive_integration_test.go",
    "server/internal/collect/runner.go",
    "server/go.mod",
    "activity/cao.mjs",
    "dashboard/site/src/data/queries/database.json",
    "internal/dashboardquery/query.go",
    "scripts/test-kusto-emulator.sh",
    "specs/session-archive.md",
    "tests/unit/workflow-contract-session-archive.test.mjs",
  ]) {
    let matched = false;
    for (const pattern of archive.on.pull_request.paths) {
      const excluded = pattern.startsWith("!");
      if (posix.matchesGlob(file, excluded ? pattern.slice(1) : pattern)) matched = !excluded;
    }
    assert.ok(matched, file);
  }
  const daily = parse(workflow("ci-daily.yml"));
  assert.equal(daily.jobs["session-archive"].uses, "./.github/workflows/session-archive.yml");
  assert.equal(daily.jobs["session-archive"].needs, "revision");
  assert.ok(daily.jobs.report.needs.includes("session-archive"));
});

test("Parquet CI requires real DuckDB, production local backfill, and the million-event benchmark", () => {
  const job = parse(workflow("session-archive.yml")).jobs["session-archive"];
  assert.equal(job.if, undefined);
  assert.equal(job.needs, undefined);
  assert.equal(job.env.CAO_SESSION_ARCHIVE_REQUIRE_DUCKDB, "1");
  assert.equal(job["continue-on-error"], undefined);
  const install = job.steps.find(step => step.name === "Install checksum-verified DuckDB v1.4.3");
  assert.match(install.run, /releases\/download\/v1\.4\.3\/duckdb_cli-linux-amd64\.zip/);
  assert.match(install.run, /7a9e36141a71a353f54dfbeed607a5218c3425ce2d417610da08443259a12f7c .*sha256sum --check/);
  assert.match(install.run, /echo "DUCKDB_PATH=\$DUCKDB_PATH" >> "\$GITHUB_ENV"/);
  const integration = job.steps.find(step => step.name === "Verify production capture, Parquet, publication, and authenticated queries");
  assert.match(integration.run, /-list '\^TestSessionArchiveLocalBackfillIntegration\$' \| grep -Fx TestSessionArchiveLocalBackfillIntegration/);
  assert.match(integration.run, /go -C server test -race -json -count=1/);
  assert.match(integration.run, /\.\/internal\/sessionarchive \.\/internal\/sessionarchiveblob \.\/internal\/collect \.\/internal\/server/);
  assert.match(integration.run, /TestSessionArchive\.\*/);
  const benchmark = job.steps.find(step => step.name === "Measure and verify 1048576 approved events with real DuckDB");
  assert.equal(benchmark.env.CAO_SESSION_ARCHIVE_BENCHMARK, "1");
  assert.match(benchmark.run, /-run '\^TestDuckDBArchiveMillionEventBenchmark\$'/);
  for (const step of [install, integration, benchmark]) {
    assert.match(step.run, /set -euo pipefail/);
    assert.equal(step.if, undefined);
    assert.equal(step["continue-on-error"], undefined);
  }
  for (const step of [integration, benchmark]) {
    assert.match(step.run, /if grep -q '"Action":"skip"'[\s\S]*exit 1/);
  }
  const results = job.steps.find(step => step.name === "Retain archive conformance results");
  assert.equal(results.if, "always()");
  assert.equal(results.with["if-no-files-found"], "error");
  assert.equal(results.with.name, "session-archive-conformance");
});

test("real Kusto archive CI keeps explicit default-false EULA acceptance separate", () => {
  const archive = parse(workflow("session-archive.yml"));
  const acceptance = archive.on.workflow_dispatch.inputs.accept_kusto_emulator_eula;
  assert.equal(acceptance.type, "boolean");
  assert.equal(acceptance.default, false);
  const job = archive.jobs["session-archive-kusto"];
  assert.equal(job.if, "github.event_name == 'workflow_dispatch' && inputs.accept_kusto_emulator_eula");
  const run = job.steps.find(step => step.name === "Required functional membership conformance against the official engine");
  assert.deepEqual(run.env, { ACCEPT_EULA: "Y" });
  assert.match(run.run, /scripts\/test-kusto-emulator\.sh/);
  assert.equal(run["continue-on-error"], undefined);
});
