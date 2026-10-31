import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import test from "node:test";
import { parse } from "yaml";
import { partitionUnitTests, unitTestAreas } from "../../scripts/run-unit-area.mjs";
import { testPlan } from "../../scripts/ci-test-plan.mjs";

test("every root unit test runs in exactly one topical area", () => {
  const files = readdirSync("tests/unit").filter((file) => file.endsWith(".test.mjs")).sort();
  const partition = partitionUnitTests(files);
  assert.deepEqual(Object.keys(partition), Object.keys(unitTestAreas));
  assert.deepEqual(Object.values(partition).flat().sort(), files);
  assert.ok(partition.documentation.includes("docs-workflow-ecosystem-layout.test.mjs"));
  assert.ok(partition.tooling.includes("azure-local-harness.test.mjs"));
  assert.ok(partition.tooling.includes("backfill-stress-report.test.mjs"));
  assert.ok(partition.tooling.includes("company-stress.test.mjs"));
  assert.ok(partition.tooling.includes("source-map-compat.test.mjs"));
  assert.ok(partition.tooling.includes("installer-canary.test.mjs"));
  assert.ok(partition.tooling.includes("maintainer-simulation.test.mjs"));
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
