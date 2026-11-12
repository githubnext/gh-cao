import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { partitionUnitTests, unitTestAreas } from "../../scripts/run-unit-area.mjs";
import { testPlan } from "../../scripts/ci-test-plan.mjs";

test("every root unit test runs in exactly one topical area", () => {
  const files = readdirSync("tests/unit").filter((file) => file.endsWith(".test.mjs")).sort();
  const partition = partitionUnitTests(files);
  assert.deepEqual(Object.keys(partition), Object.keys(unitTestAreas));
  assert.deepEqual(Object.values(partition).flat().sort(), files);
  assert.ok(partition.activity.includes("activity-normalized-shards.test.mjs"));
  assert.ok(partition.documentation.includes("docs-workflow-ecosystem-layout.test.mjs"));
  assert.ok(partition.tooling.includes("azure-local-harness.test.mjs"));
  assert.ok(partition.tooling.includes("backfill-stress-report.test.mjs"));
  assert.ok(partition.tooling.includes("company-stress.test.mjs"));
  assert.ok(partition.tooling.includes("source-map-compat.test.mjs"));
  assert.ok(partition.tooling.includes("installer-canary.test.mjs"));
  assert.ok(partition.tooling.includes("cao-retired-activity.test.mjs"));
  assert.ok(partition.tooling.includes("local-artifacts.test.mjs"));
  assert.ok(partition.workflows.includes("build-release.test.mjs"));
  assert.throws(() => partitionUnitTests([...files, "unclassified.test.mjs"]), /exactly one area/);
});

test("CI runs every defined unit test area", () => {
  const workflow = readFileSync(".github/workflows/workflow-contracts.yml", "utf8").replaceAll("\r\n", "\n");
  assert.deepEqual(testPlan([], true).matrix.area, Object.keys(unitTestAreas));
  assert.equal(parse(workflow).jobs.unit.strategy.matrix, "${{ fromJSON(needs.changes.outputs.matrix) }}");
  assert.equal(parse(workflow).jobs.unit.steps.at(-1).run, 'npm run test:unit:area -- "$UNIT_AREA"');
  assert.match(
    workflow,
    /name: Install dashboard dependencies\n\s+if: matrix\.area == 'dashboard'\n\s+run: npm ci --prefix dashboard\/site$/m,
  );
});

test("terminal contract jobs build the dashboard's native dependencies before testing", () => {
  const workflow = parse(readFileSync(".github/workflows/workflow-contracts.yml", "utf8"));
  for (const [jobId, testCommand] of [
    ["unit", "npm run test:unit:area"],
    ["full-contracts", "npm run test:integration"],
  ]) {
    const steps = workflow.jobs[jobId].steps;
    const installIndex = steps.findIndex((step) => step.name === "Install dashboard dependencies");
    const testIndex = steps.findIndex((step) => step.run?.includes(testCommand));
    assert.ok(installIndex >= 0 && installIndex < testIndex, `${jobId} must install before testing`);
    assert.equal(steps[installIndex].run, "npm ci --prefix dashboard/site",
      `${jobId} must run node-pty's native build and the terminal preparation lifecycle`);
  }
});

test("dashboard unit contracts prepare native Go tests before the bounded contract runner", () => {
  const steps = parse(readFileSync(".github/workflows/workflow-contracts.yml", "utf8")).jobs.unit.steps;
  const go = steps.find((step) => step.uses?.startsWith("actions/setup-go@") && step.with?.["go-version-file"] === "go.mod");
  const build = steps.find((step) => step.name === "Prepare native dashboard contract build");
  const contracts = steps.find((step) => step.run?.includes("npm run test:unit:area"));
  assert.ok(go, "provision the native contract's Go toolchain and cache");
  assert.equal(go.with["go-version-file"], "go.mod");
  assert.equal(go.with["cache-dependency-path"], "go.sum");
  assert.ok(build, "compile native tests outside the contract's execution deadline");
  assert.equal(build.run, "go test -c -o /dev/null ./internal/cli");
  assert.ok(contracts);
  assert.ok(steps.indexOf(go) < steps.indexOf(build));
  assert.ok(steps.indexOf(build) < steps.indexOf(contracts));
  for (const step of [go, build]) {
    assert.equal(step.if, "matrix.area == 'dashboard'");
    assert.equal(step["continue-on-error"], undefined);
  }
});

test("PostgreSQL contracts use the host port discovered after the fixture container restarts", (t) => {
  const steps = parse(readFileSync(".github/workflows/workflow-contracts.yml", "utf8")).jobs.unit.steps;
  const prepare = steps.find((step) => step.name === "Prepare partition-heavy PostgreSQL fixtures");
  const contracts = steps.find((step) => step.run?.includes("npm run test:unit:area"));
  const directory = mkdtempSync(path.resolve(".ci-postgres-contract-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const output = path.join(directory, "output");
  writeFileSync(path.join(directory, "docker"), `#!/bin/sh
set -eu
case "$1" in
  restart) touch "$RESTART_MARKER" ;;
  inspect) test -f "$RESTART_MARKER"; printf '55440\\n' ;;
  exec) ;;
  *) exit 1 ;;
esac
`, { mode: 0o700 });
  writeFileSync(path.join(directory, "go"), "#!/bin/sh\nexit 0\n", { mode: 0o700 });
  execFileSync("bash", ["-e", "-o", "pipefail", "-c", prepare.run], {
    env: {
      ...process.env,
      PATH: `${directory}${path.delimiter}${process.env.PATH}`,
      POSTGRES_CONTAINER: "fixture",
      RESTART_MARKER: path.join(directory, "restarted"),
      GITHUB_OUTPUT: output,
    },
  });
  assert.equal(readFileSync(output, "utf8"), "port=55440\n");
  assert.ok(steps.indexOf(prepare) < steps.indexOf(contracts));
  assert.ok(contracts.env.POSTGRES_URL.includes(`steps.${prepare.id}.outputs.port`));
  assert.ok(!contracts.env.POSTGRES_URL.includes("job.services.postgres.ports"));
});
