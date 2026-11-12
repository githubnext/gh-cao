import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import test from "node:test";
import { parse } from "yaml";
import { goTestArea, goTestAreas, goTestPlan } from "../../scripts/ci-test-plan.mjs";

const workflow = parse(readFileSync(new URL("../../.github/workflows/cgo.yml", import.meta.url), "utf8"));
const jobs = workflow.jobs;

test("Go CI widens production and unknown changes to every server area", () => {
  for (const file of [
    "server/internal/server/server.go", "server/go.mod", "server/internal/new-package/worker.go",
    "internal/cli/cli.go", "internal/dashboardquery/engine.go", "go.sum",
    ".github/workflows/cgo.yml", "scripts/ci-test-plan.mjs", "unknown-input",
  ]) {
    assert.deepEqual(goTestPlan([file]).matrix.area, goTestAreas, file);
    assert.equal(goTestPlan([file]).integration, true, file);
  }
  for (const files of [[], ["server/internal/server/server_test.go"]]) {
    assert.deepEqual(goTestPlan(files, true).matrix.area, goTestAreas);
    assert.equal(goTestPlan(files, true).query, true);
  }
});

test("Go CI selects test-only areas and unions mixed changes", () => {
  const files = [
    "server/internal/server/server_test.go", "server/internal/hostedcli/catalog_test.go",
    "server/internal/postgresx/schema_test.go", "server/internal/operational/postgres/store_test.go",
  ];
  for (const [index, file] of files.entries()) {
    assert.deepEqual(goTestPlan([file]).matrix.area, [goTestAreas[index]]);
    assert.equal(goTestPlan([file]).query, false);
    assert.equal(goTestPlan([file]).integration, false);
  }
  assert.deepEqual(goTestPlan(files.slice(0, 2)).matrix.area, ["http", "hosted-cli"]);
  assert.deepEqual(goTestPlan([...files.slice(0, 1), "server/internal/model/model.go"]).matrix.area, goTestAreas);
  assert.equal(goTestPlan(["server/internal/simulator/actions_test.go"]).integration, true);
});

test("dashboard inputs retain native fixture coverage without running Go lint", () => {
  for (const file of ["dashboard/site/src/dashboard-app.js", "dashboard/site/dashboard-fragments/overview.json"]) {
    const plan = goTestPlan([file]);
    assert.deepEqual(plan.matrix.area, goTestAreas);
    assert.equal(plan.query, true);
    assert.equal(plan.lint, false);
  }
});

test("Go CI filters are wired before job setup and retain full manual coverage", () => {
  assert.equal(jobs.changes.steps.find((step) => step.id === "plan").run, "node scripts/ci-test-plan.mjs --go");
  assert.equal(jobs.changes.steps[0].with["fetch-depth"], 0);
  for (const [id, output] of [
    ["server-quality", "lint"], ["server-tests", "tests"], ["shared-query", "query"],
  ]) {
    assert.equal(jobs[id].needs, "changes");
    assert.equal(jobs[id].if, `needs.changes.outputs.${output} == 'true'`);
  }
  assert.equal(jobs["server-quality"].services, undefined);
  assert.equal(jobs["shared-query"].services, undefined);
  assert.equal(jobs["server-tests"].strategy["fail-fast"], false);
  assert.equal(jobs["server-tests"].strategy.matrix, "${{ fromJSON(needs.changes.outputs.matrix) }}");
  assert.match(jobs["server-integration"].if, /event_name != 'pull_request'/);
  assert.equal(jobs["server-checks"].name, "Go format, lint, and tests");
  assert.equal(jobs["server-checks"].if, "always()");
  assert.deepEqual(jobs["server-checks"].needs,
    ["changes", "server-quality", "server-tests", "server-integration", "shared-query"]);
  for (const event of ["pull_request", "push"]) {
    assert.ok(workflow.on[event].paths.includes("scripts/ci-test-plan.mjs"));
    assert.ok(workflow.on[event].paths.includes("scripts/run-unit-area.mjs"));
  }
});

test("Go unit jobs partition every package exactly once, including future packages", () => {
  const packages = [
    "internal/server", "internal/hostedcli", "internal/postgresx",
    "internal/query", "internal/operational/postgres", "cmd/cao-dashboard",
    "hosting", "postgres", "internal/future-package", "internal/server/child",
  ];
  const prefix = "github.com/githubnext/gh-cao/server/";
  const step = jobs["server-tests"].steps.find((step) => step.name === "Unit tests");
  const covered = [];
  for (const area of goTestAreas) {
    const result = spawnSync("bash", ["-c", `
      go() {
        if [[ "$3" == "list" ]]; then printf '%s\\n' "$MOCK_PACKAGES";
        else shift 6; printf '%s\\n' "$@"; fi
      }
      ${step.run}
    `], {
      encoding: "utf8",
      env: {
        ...process.env, MOCK_PACKAGES: packages.map((name) => prefix + name).join("\n"),
        GO_TEST_AREA: area, GO_TEST_MODE: "-short",
      },
    });
    assert.equal(result.status, 0, result.stderr);
    const selected = result.stdout.trim().split("\n").map((name) =>
      name.startsWith("./") ? name.slice(2) : name.slice(prefix.length));
    for (const name of selected) assert.equal(goTestArea(`server/${name}/file.go`), area);
    covered.push(...selected);
  }
  assert.deepEqual(covered.sort(), packages.sort());
  assert.match(step.env.POSTGRES_URL, /job\.services\.postgres/);
  assert.match(step.env.CAO_TEST_POSTGRES_URL, /job\.services\.postgres/);
  assert.match(step.env.GO_TEST_MODE, /'-short' \|\| '-count=1'/);
});

test("Go unit selection propagates package-list and test failures", () => {
  const step = jobs["server-tests"].steps.find((step) => step.name === "Unit tests");
  for (const area of [...goTestAreas, "unknown"]) {
    const result = spawnSync("bash", ["-c", `go() { return 23; }\n${step.run}`], {
      encoding: "utf8",
      env: { ...process.env, GO_TEST_AREA: area, GO_TEST_MODE: "-short" },
    });
    assert.equal(result.status, area === "unknown" ? 1 : 23);
  }
});

test("the stable Go check accepts intentional skips but rejects failures and cancellation", () => {
  const step = jobs["server-checks"].steps[0];
  for (const status of ["success", "skipped", "failure", "cancelled"]) {
    const result = spawnSync("bash", ["-c", step.run], {
      encoding: "utf8",
      env: {
        ...process.env,
        RESULTS: JSON.stringify({ changes: { result: "success" }, tests: { result: status } }),
      },
    });
    assert.equal(result.status, ["success", "skipped"].includes(status) ? 0 : 1);
  }
  const result = spawnSync("bash", ["-c", step.run], {
    encoding: "utf8",
    env: { ...process.env, RESULTS: JSON.stringify({ changes: { result: "skipped" } }) },
  });
  assert.equal(result.status, 1);
});
