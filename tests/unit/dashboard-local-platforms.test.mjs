import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { posix } from "node:path";
import test from "node:test";
import { parse } from "yaml";

const workflow = parse(readFileSync(new URL("../../.github/workflows/dashboard-local-platforms.yml", import.meta.url), "utf8"));

test("local serving runs on native Linux, macOS, and Windows with real PostgreSQL", () => {
  assert.deepEqual(workflow.permissions, { contents: "read" });
  const job = workflow.jobs.serving;
  assert.equal(job.if, undefined);
  assert.equal(job.strategy["fail-fast"], false);
  const matrix = job.strategy.matrix.include;
  assert.deepEqual(matrix.filter((entry) => entry.backend === "postgres").map((entry) => entry.os),
    ["Linux", "macOS", "Windows"]);
  assert.ok(matrix.some((entry) => entry.os === "Linux" && entry.backend === "docker"));
  assert.ok(matrix.filter((entry) => entry.os !== "Linux").every((entry) => entry.backend === "postgres"));
  assert.equal(job.services, undefined, "hosted macOS/Windows do not support service containers");
  assert.ok(job.steps.every((step) => !step["continue-on-error"]));
  assert.equal(job.steps[0].with["persist-credentials"], false);
  const postgres = job.steps.find((step) => step.name === "Start native PostgreSQL");
  assert.equal(postgres.if, "matrix.backend == 'postgres'");
  assert.equal(postgres.id, "postgres");
  assert.equal(postgres.with.username, "cao_bootstrap");
  assert.equal(postgres.with.database, "cao_dashboard");
  assert.match(postgres.uses, /^ikalnytskyi\/action-setup-postgres@[a-f0-9]{40}$/);
  const build = job.steps.find((step) => step.name === "Build dashboard and native commands");
  assert.match(build.run, /generate-dashboard-ingest\.mjs --check/);
  assert.match(build.run, /go -C server build .*cao-dashboard/);
  assert.match(build.run, /go build .*gh-cao/);
  assert.equal(build.env.CAO_DASHBOARD_PROFILE, "server");
  const serving = job.steps.find((step) => step.name === "Test real local serving");
  assert.equal(serving.if, undefined);
  assert.equal(serving.env.CAO_DASHBOARD_SERVING_INTEGRATION, "1");
  assert.equal(serving.env.CAO_DASHBOARD_TEST_POSTGRES_URL, "${{ steps.postgres.outputs.connection-uri }}");
  assert.match(serving.run, /CAO_DASHBOARD_TEST_POSTGRES_URL\/localhost\/127\.0\.0\.1/);
  assert.match(serving.run, /\^TestDashboardServingIntegration\$/);
  for (const variable of ["CAO_DASHBOARD_TEST_SERVER", "CAO_DASHBOARD_TEST_CLI", "CAO_DASHBOARD_TEST_SITE"]) {
    assert.ok(serving.env[variable].startsWith("${{ github.workspace }}/"), variable);
  }
});

test("POSIX local serving exercises the checksum-verified DuckDB v2.0.0 session archive", () => {
  const steps = workflow.jobs.serving.steps;
  const duckdb = steps.findIndex((step) => step.name === "Install checksum-verified DuckDB v2.0.0");
  const serving = steps.findIndex((step) => step.name === "Test real local serving");
  assert.ok(duckdb >= 0 && duckdb < serving);
  const install = steps[duckdb];
  assert.equal(install.if, "runner.os != 'Windows'");
  assert.match(install.run, /releases\/download\/v2\.0\.0\/\$asset/);
  assert.match(install.run, /shasum -a 256 --check/);
  for (const digest of [/c6507206baf7793c6013dbd82368469a45f20dc0e17a6f92f204602f652ed2bc/,
    /859e1875d2a9515fbb4bf8871061437a14af82320da68c753f094c4b776348fe/,
    /8a8f67e7c7f7d1392fa63ba3971948391392fe2a25fca63e7008e33c50beb4c2/]) {
    assert.match(install.run, digest);
  }
  assert.match(install.run, /CAO_DASHBOARD_TEST_DUCKDB=\$duckdb" >> "\$GITHUB_ENV"/);
});

test("pull requests cover source, generated dashboard assets, and build inputs", () => {
  for (const event of ["pull_request", "push"]) {
    const patterns = workflow.on[event].paths;
    for (const file of [
      ".github/workflows/dashboard-local-platforms.yml", ".github/workflows/cao.json",
      ".github/workflows/shared/policy.mjs",
      "cmd/gh-cao/main.go", "internal/cli/dashboard_serve.go",
      "internal/dashboardingest/generated_bundle.go", "go.mod", "go.sum",
      "server/go.mod", "server/go.sum", "server/cmd/cao-dashboard/main.go",
      "server/internal/dashboardassets/resources/site/index.html",
      "dashboard/local-assets/bundle/cao-dashboard.exe",
      "dashboard/site/dashboard.json", "dashboard/site/src/agent/queries.generated.json",
      "dashboard/site/src/data/queries/database.json", "dashboard/site/package-lock.json",
      "scripts/generate-dashboard-ingest.mjs", "scripts/prepare-dashboard-embed.mjs",
      "scripts/build-release.sh", "package.json", "package-lock.json", ".npmrc",
      "tests/unit/dashboard-local-platforms.test.mjs",
    ]) {
      assert.ok(patterns.some((pattern) => posix.matchesGlob(file, pattern)), `${event}: ${file}`);
    }
    assert.ok(!patterns.some((pattern) => posix.matchesGlob("docs/glossary.md", pattern)));
  }
  assert.deepEqual(workflow.on.push.branches, ["main"]);
});

test("Docker Desktop coverage is explicit and cannot execute untrusted PRs on self-hosted runners", () => {
  const job = workflow.jobs["docker-desktop"];
  assert.equal(job.if,
    "github.event_name == 'workflow_dispatch' && inputs.docker_desktop && github.ref == 'refs/heads/main'");
  assert.equal(workflow.on.workflow_dispatch.inputs.docker_desktop.default, false);
  assert.equal(job.environment, "dashboard-desktop-tests");
  assert.deepEqual(job.strategy.matrix.include.map((entry) => entry.os), ["Windows", "macOS"]);
  for (const entry of job.strategy.matrix.include) {
    assert.ok(entry.runner.includes("self-hosted"));
    assert.ok(entry.runner.includes(entry.os));
    assert.ok(entry.runner.includes("cao-dashboard-docker-ephemeral"));
    assert.equal(entry.backend, "docker-explicit");
  }
  assert.deepEqual(job.steps, workflow.jobs.serving.steps, "run exactly the same serving test");
});
