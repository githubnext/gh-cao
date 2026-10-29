import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { pathToFileURL } from "node:url";
import { run } from "../../.github/skills/add-operational-value/scripts/common.mjs";

const root = path.resolve(import.meta.dirname, "../..");
const scripts = path.join(root, ".github/skills/add-operational-value/scripts");

test("script runner invokes Node modules without executable permissions or a shebang", (t) => {
  const temporary = mkdtempSync(path.join(os.tmpdir(), "value script runner-"));
  t.after(() => rmSync(temporary, { recursive: true, force: true }));
  const script = path.join(temporary, "script.mjs");
  writeFileSync(script, "console.log(JSON.stringify(process.argv.slice(2)));\n", { mode: 0o600 });
  assert.deepEqual(JSON.parse(run(script, ["argument with spaces"])), ["argument with spaces"]);
});

test("skill connects frozen intent, final acceptance, dated lineage and reusable reporting", () => {
  const skill = readFileSync(path.join(root, ".github/skills/add-operational-value/SKILL.md"), "utf8");
  assert.match(skill, /Intent defines what to measure; cadence tracks repository state/);
  assert.match(skill, /Do not sample only at accepted outputs/);
  assert.match(skill, /same elapsed follow-up/);
  assert.match(skill, /right-censored at the observation cutoff/);
  assert.match(skill, /ClosedEvent.closer/);
  assert.match(skill, /collectEvents/);
  assert.match(skill, /--report/);
  assert.match(skill, /examples\/daily-file-diet.mjs/);
  assert.match(skill, /examples\/acquire-file-diet.mjs/);
  assert.match(skill, /gh-aw-execution/);
  assert.match(skill, /Tracking IDs locate outputs, not accepted/);
  assert.match(skill, /closed issues are not automatically accepted implementations/i);
  assert.match(skill, /Missing lifecycle data is unknown, not zero/);
  assert.match(skill, /No review triggered/);
  assert.match(skill, /Overdue acceptance/);
  assert.match(skill, /Do not invent alert thresholds/);
  assert.match(skill, /automatically stage registration/);
  assert.match(skill, /--collector PATH --if-absent/);
  assert.match(skill, /Preserve existing registrations and explicit disables/);
  assert.match(skill, /not.*a current-evidence/);
  const events = JSON.parse(readFileSync(path.join(root, "tests/fixtures/operational-value/file-diet-events.json"), "utf8")).events;
  assert.equal(events.filter(event => !event.lineage.some(edge => edge.relation === "connected")
    && event.lineage.some(edge => edge.relation === "closed-by")).length, 26);
  assert.ok(events.every(event => event.lineage.some(edge => edge.relation === "closed-by")));
});

test("skill uses query-first evidence without limiting history to source retention", () => {
  const skill = readFileSync(
    path.join(root, ".github/skills/add-operational-value/SKILL.md"),
    "utf8",
  );
  assert.match(skill, /Use a declarative query over canonical CAO data/);
  assert.match(skill, /temporary checkout of the target repository at the immutable cutoff/);
  assert.match(skill, /Query retention is a source limitation, not the operational-value history window/);
  assert.match(skill, /contemporaneous accepted snapshot was preserved/);
});

test("skill separates frozen measurements, recent movement and adoption association", () => {
  const skill = readFileSync(path.join(root, ".github/skills/add-operational-value/SKILL.md"), "utf8");
  assert.match(skill, /immediately preceding equal-duration period/);
  assert.match(skill, /not a sustained trend/);
  assert.match(skill, /make no adoption-relative improvement claim/);
  assert.match(skill, /successful query or non-null score alone is not proof/);
  assert.match(skill, /never average ratios or add overlapping rolling totals/);
});

test("deterministic reports separate recent deterioration from adoption gain and never invent missing references", (t) => {
  mkdirSync(path.join(root, ".tmp"), { recursive: true });
  const temporary = mkdtempSync(path.join(root, ".tmp", "value-report-periods-"));
  t.after(() => rmSync(temporary, { recursive: true, force: true }));
  const source = path.join(root, "optimization/operational-value/optimization-token-optimizer.mjs");
  const modulePath = path.join(temporary, "value.mjs");
  const snapshot = (end, value, changes = {}) => ({
    observedAt: end,
    window: { startAt: new Date(Date.parse(end) - 7 * 86_400_000).toISOString(), endAt: end },
    provenance: [{ source: "fixture", cutoff: end }],
    evidence: {
      eligibleWorkflowCount: 1, successfulRunCount: 1, successfulRunAicTotal: value,
      concludedRunCount: 1, failedRunPercentagePointTotal: 0, cancelledRunPercentagePointTotal: 0,
      maturityStatus: "matured", coverageStatus: "complete", ...changes
    }
  });
  const base = [
    snapshot("2026-09-08T10:00:00Z", 20), snapshot("2026-09-17T10:00:00Z", 2),
    snapshot("2026-09-23T10:00:00Z", 1), snapshot("2026-09-24T10:00:00Z", 8)
  ];
  const build = (snapshots, mode = "baseline-comparable", endpoint = "2026-09-24T11:00:00Z") => {
    writeFileSync(modulePath, `
import * as original from ${JSON.stringify(pathToFileURL(source).href)};
export const definition = { ...original.definition, evaluation: { mode: ${JSON.stringify(mode)} } };
export const collectBatch = original.collectBatch;
export const scoreMetric = original.scoreMetric;
`);
    const input = path.join(temporary, "observations.json");
    const output = path.join(temporary, "timeline.json");
    writeFileSync(input, JSON.stringify({
      repository: "githubnext/gh-cao", evaluationMode: mode,
      adoptionAt: "2026-09-15T23:30:36Z", workflowName: "Optimization",
      window: { startAt: "2026-09-01T00:00:00Z", endAt: endpoint },
      evidenceCoverage: {}, snapshots, runs: []
    }));
    execFileSync(process.execPath, [path.join(scripts, "build-timeline.mjs"), modulePath, input, output]);
    return JSON.parse(readFileSync(output, "utf8"));
  };
  const report = build(base);
  assert.equal(report.schemaVersion, 3);
  assert.equal(report.metricReview.comparisonPolicy, "adjacent-period-v1");
  const primary = report.metricReview.metrics[0];
  assert.equal(primary.currentValue, 8);
  assert.equal(primary.recentComparison.nativeDelta, 6);
  assert.equal(primary.recentComparison.directionAwareGain, -6);
  assert.equal(primary.adoptionComparison.nativeDelta, -12);
  assert.equal(primary.adoptionComparison.directionAwareGain, 12);
  assert.equal(build(base.slice(1)).metricReview.metrics[0].adoptionComparison.status, "unavailable");
  assert.equal(build(base.slice(2)).metricReview.metrics[0].recentComparison.status, "unavailable");
  assert.equal(build(base.slice(-1)).metricReview.metrics[0].currentValue, 8);
  assert.equal(build(base.slice(1), "attainment-only").metricReview.metrics[0].recentComparison.nativeDelta, 6);
  assert.equal(build([...base.slice(0, -1), snapshot("2026-09-24T10:00:00Z", 8, { coverageStatus: "unknown" })])
    .metricReview.metrics[0].recentComparison.status, "unavailable");
  const missing = build([...base.slice(0, -1), snapshot("2026-09-24T10:00:00Z", 8, { eligibleWorkflowCount: 0 })])
    .metricReview.metrics[0];
  assert.equal(missing.currentValue, null);
  assert.equal(missing.recentComparison.nativeDelta, null);
  const withdrawn = [...base.slice(0, 2), {
    ...snapshot("2026-09-17T10:00:00Z", 2, { eligibleWorkflowCount: 0 }),
    observedAt: "2026-09-23T09:00:00Z"
  }, ...base.slice(2)];
  assert.equal(build(withdrawn).metricReview.metrics[0].recentComparison.status, "unavailable");
  assert.match(build(base, "baseline-comparable", "2026-09-25T10:00:00Z")
    .metricReview.metrics[0].recentComparison.reason, /stale/);
  for (const endpoint of ["2026-09-25T10:00:00Z", "2026-09-24T09:00:00Z"]) {
    const metric = build(base, "baseline-comparable", endpoint).metricReview.metrics[0];
    assert.equal(metric.currentValue, 8);
    assert.equal(metric.adoptionComparison.status, "unavailable");
    assert.match(metric.adoptionComparison.reason, /stale or future/);
  }
  const futureCorrection = [...base.slice(0, 2), {
    ...snapshot("2026-09-17T10:00:00Z", 20), observedAt: "2026-09-25T10:00:00Z"
  }, ...base.slice(2)];
  assert.equal(build(futureCorrection.toSorted((left, right) => Date.parse(left.observedAt) - Date.parse(right.observedAt)))
    .metricReview.metrics[0].recentComparison.status, "unavailable");
});
test("skill tests whether each measured phenomenon existed before adoption", () => {
  const skill = readFileSync(
    path.join(root, ".github/skills/add-operational-value/SKILL.md"),
    "utf8",
  );
  assert.match(skill, /Do not confuse workflow adoption with creation of the measured phenomenon/);
  assert.match(skill, /Do not derive the primary measure from a gh-aw workflow's own output/);
  assert.match(skill, /pre-existing repository problem/);
  assert.match(skill, /Count outcome attainment regardless of actor/);
  assert.match(skill, /security-alert backlog/);
  assert.match(skill, /antecedent-existence test/);
  assert.match(skill, /pre-existing-and-reconstructable/);
  assert.match(skill, /pre-existing-but-not-reconstructable/);
  assert.match(skill, /created-at-adoption/);
  assert.match(skill, /same native formula, unit, population, grain, filters, and window/);
  assert.match(skill, /“the workflow did not exist yet” is not sufficient/);
  assert.match(skill, /Attainment-only is a fallback after an explicit failed reconstruction/);
});

test("campaign workflow listing returns only direct manifest worker workflows", () => {
  const temporary = mkdtempSync(path.join(os.tmpdir(), "operational-value-campaign-list-"));
  mkdirSync(path.join(temporary, ".github/workflows"), { recursive: true });
  mkdirSync(path.join(temporary, "example"), { recursive: true });
  const workflow = (name, role) => `---
name: ${name}
imports:
  - uses: shared/control.md
    with:
      role: ${role}
---
# ${name}
`;
  writeFileSync(
    path.join(temporary, ".github/workflows/example.md"),
    workflow("example", "orchestrator"),
  );
  writeFileSync(
    path.join(temporary, ".github/workflows/example-worker.md"),
    workflow("example-worker", "worker"),
  );
  writeFileSync(
    path.join(temporary, ".github/workflows/unrelated.md"),
    workflow("unrelated", "worker"),
  );
  writeFileSync(path.join(temporary, "example/aw.yml"), `name: Example
includes:
  - ../aw.yml
  - .github/workflows/example.md
  - .github/workflows/example-worker.md
`);

  const output = execFileSync(
    process.execPath,
    [path.join(scripts, "list-workflows.mjs"), "--campaign", "example"],
    { cwd: temporary, encoding: "utf8" },
  );
  assert.equal(output, "example-worker\n");
  assert.equal(execFileSync(
    process.execPath,
    [path.join(scripts, "list-workflows.mjs"), "example-worker", "--campaign", "example"],
    { cwd: temporary, encoding: "utf8" },
  ), ".github/workflows/example-worker.md\n");
  assert.throws(() => execFileSync(
    process.execPath,
    [path.join(scripts, "list-workflows.mjs"), "example", "--campaign", "example"],
    { cwd: temporary, stdio: "pipe" },
  ), /workflow not found: example/);
  assert.throws(() => execFileSync(
    process.execPath,
    [path.join(scripts, "list-workflows.mjs"), "unrelated", "--campaign", "example"],
    { cwd: temporary, stdio: "pipe" },
  ), /workflow not found: unrelated/);
});

test("campaign wiring creates one standard adapter without unsupported manifest entries", () => {
  const temporary = mkdtempSync(path.join(os.tmpdir(), "operational-value-campaign-wire-"));
  mkdirSync(path.join(temporary, "example/operational-value"), { recursive: true });
  writeFileSync(path.join(temporary, "example/aw.yml"), `name: Example
includes:
  - ../aw.yml
  - .github/workflows/example.md
`);
  writeFileSync(
    path.join(temporary, "example/operational-value/example.mjs"),
    "export const definition = {};\n",
  );
  const command = [
    path.join(scripts, "wire-campaign.mjs"),
    "example",
    "example",
    "--root",
    temporary,
  ];
  execFileSync(process.execPath, command);
  execFileSync(process.execPath, command);

  assert.equal(
    readFileSync(path.join(temporary, "example/operational-value.mjs"), "utf8"),
    readFileSync(path.join(root, ".github/skills/add-operational-value/templates/campaign-operational-value.mjs"), "utf8"),
  );
  const manifest = readFileSync(path.join(temporary, "example/aw.yml"), "utf8");
  assert.equal(manifest.includes("operational-value.mjs"), false);
  assert.equal(manifest.includes("operational-value/example.mjs"), false);
  assert.equal(manifest.endsWith("\n"), true);

  writeFileSync(path.join(temporary, "example/operational-value.mjs"), "custom adapter\n");
  assert.throws(
    () => execFileSync(process.execPath, command, { stdio: "pipe" }),
    /already exists and is not the standard campaign adapter/,
  );
});

test("CAO verification accepts namespaced metrics for multiple repositories", () => {
  const temporary = mkdtempSync(path.join(os.tmpdir(), "operational-value-multi-repository-"));
  const source = path.join(root, "optimization/operational-value/optimization-token-optimizer.mjs");
  const valueModule = path.join(temporary, "example-workflow.mjs");
  const adapter = path.join(temporary, "operational-value.mjs");
  writeFileSync(valueModule, `
import * as original from ${JSON.stringify(pathToFileURL(source).href)};
export const definition = {
  ...original.definition,
  slug: "example-workflow",
  workflowName: "Example Workflow",
  evidence: {
    ...original.definition.evidence,
    repositories: ["github/gh-aw", "githubnext/gh-cao"]
  },
  metrics: original.definition.metrics.map(({rollup, ...metric}) => metric)
};
export const collectBatch = original.collectBatch;
export const scoreMetric = original.scoreMetric;
`);
  writeFileSync(adapter, `
const request = JSON.parse(await new Promise((resolve) => {
  let input = "";
  process.stdin.setEncoding("utf8");
  process.stdin.on("data", (chunk) => { input += chunk; });
  process.stdin.on("end", () => resolve(input));
}));
console.log(JSON.stringify({
  kind: "operational_value_definition",
  workflowSlug: "example-workflow",
  adoptedAt: "2026-09-15T23:30:36Z",
  evaluationMode: "baseline-comparable",
  cadenceDays: 1,
  repositories: ["github/gh-aw", "githubnext/gh-cao"],
  valueIds: [
    "example-workflow.aic-per-successful-run",
    "example-workflow.failure-rate-percent",
    "example-workflow.cancellation-rate-percent"
  ]
}));
for (const repository of request.repositories) {
  for (const valueId of [
    "example-workflow.aic-per-successful-run",
    "example-workflow.failure-rate-percent",
    "example-workflow.cancellation-rate-percent"
  ]) {
    const metricUnit = valueId.endsWith("aic-per-successful-run") ? "aic-per-run" : "percent";
    console.log(JSON.stringify({repository, valueId, value: 0.5, metricUnit, timestamp: request.timestamp}));
  }
}
`);

  const output = execFileSync(
    process.execPath,
    [
      path.join(scripts, "verify-value-function.mjs"),
      "--cao-adapter",
      adapter,
      valueModule,
    ],
    { encoding: "utf8" },
  );
  assert.equal(output, `verified ${valueModule}\n`);
});

test("CAO verification accepts explicit unavailable periods without accepting malformed null assessments", (t) => {
  mkdirSync(path.join(root, ".tmp"), { recursive: true });
  const temporary = mkdtempSync(path.join(root, ".tmp", "value-verification-periods-"));
  t.after(() => rmSync(temporary, { recursive: true, force: true }));
  const source = path.join(root, "optimization/operational-value/optimization-token-optimizer.mjs");
  const adapter = path.join(temporary, "operational-value.mjs");
  const assessment = {
    value: null,
    maturityStatus: "unavailable",
    windowStart: "2026-09-09T23:30:36Z",
    windowEnd: "2026-09-16T23:30:36Z",
    measurementKind: "interval",
    coverageStatus: "missing",
    missingReason: "no-eligible-opportunities",
  };
  const verify = (changes = {}) => {
    writeFileSync(adapter, `
import {readFileSync} from "node:fs";
import {definition} from ${JSON.stringify(pathToFileURL(source).href)};
const request = JSON.parse(readFileSync(0, "utf8"));
console.log(JSON.stringify({
  kind: "operational_value_definition", workflowSlug: definition.slug,
  adoptedAt: definition.adoption.adoptedAt, evaluationMode: definition.evaluation.mode,
  cadenceDays: definition.evidence.window.cadenceDays, repositories: definition.evidence.repositories,
  valueIds: definition.metrics.map(metric => definition.slug + "." + metric.id)
}));
for (const repository of request.repositories) {
  for (const metric of definition.metrics) {
    console.log(JSON.stringify({
      timestamp: request.timestamp, repository, valueId: definition.slug + "." + metric.id,
      metricUnit: metric.unit, ...${JSON.stringify({ ...assessment, ...changes })}
    }));
  }
}
`);
    return execFileSync(process.execPath, [
      path.join(scripts, "verify-value-function.mjs"), "--cao-adapter", adapter, source,
    ], { encoding: "utf8", stdio: "pipe" });
  };
  assert.match(verify(), /^verified /);
  for (const changes of [
    { missingReason: "" },
    { maturityStatus: "matured" },
    { windowStart: undefined, windowEnd: undefined, measurementKind: undefined, coverageStatus: undefined },
    { windowStart: "2026-09-17T23:30:36Z" },
    { windowEnd: "2026-09-17T23:30:36Z" },
    { coverageStatus: "invented" },
    { rollupNumerator: 0 },
    { rollupDenominator: 1 },
    { rollupNumerator: 0, rollupDenominator: 1 },
    { value: 0 },
  ]) {
    assert.throws(() => verify(changes));
  }
  assert.match(verify({
    value: 0, maturityStatus: "matured", rollupNumerator: 0, rollupDenominator: 1,
  }), /^verified /);
});

test("attainment evaluation uses a partial post-adoption window before maturity", () => {
  const temporary = mkdtempSync(path.join(os.tmpdir(), "operational-value-maturation-"));
  const source = path.join(root, "optimization/operational-value/optimization-token-optimizer.mjs");
  const valueModule = path.join(temporary, "maturation-test.mjs");
  const reports = path.join(temporary, "reports");
  writeFileSync(valueModule, `
import * as original from ${JSON.stringify(pathToFileURL(source).href)};
export const definition = {
  ...original.definition,
  slug: "maturation-test",
  sourcePath: ".github/workflows/maturation-test.md",
  evaluation: { mode: "attainment-only" }
};
export const collectBatch = (requests) => requests.map(() => ({
  evidence: {
    maturityStatus: "interim",
    dubious: true,
    eligibleWorkflowCount: 1,
    successfulRunCount: 1,
    successfulRunAicTotal: 0,
    concludedRunCount: 1,
    failedRunCount: 0,
    cancelledRunCount: 0,
    failedRunPercentagePointTotal: 0,
    cancelledRunPercentagePointTotal: 0
  },
  provenance: [{ repository: definition.repository, kind: "test-fixture", ref: definition.adoption.commit }]
}));
export const scoreMetric = original.scoreMetric;
`);

  execFileSync(
    process.execPath,
    [
      path.join(scripts, "evaluate.mjs"),
      "--function",
      valueModule,
      "--end",
      "2026-09-24T20:48:02Z",
      "--output-dir",
      reports,
      "--no-runs",
      "githubnext/gh-cao",
      "maturation-test",
    ],
    { cwd: root, stdio: "pipe" },
  );
  const timeline = JSON.parse(readFileSync(
    path.join(reports, "githubnext-gh-cao/maturation-test-timeline.json"),
    "utf8",
  ));
  assert.equal(
    timeline.snapshots[0].observedAt,
    "2026-09-16T23:30:36Z",
  );
  assert.equal(
    timeline.snapshots[0].window.startAt,
    timeline.valueFunction.definition.adoption.adoptedAt,
  );
  assert.equal(timeline.snapshots[0].window.endAt, "2026-09-16T23:30:36Z");
  assert.equal(timeline.snapshots[0].evidence.maturityStatus, "interim");
  assert.equal(timeline.snapshots[0].evidence.dubious, true);
  assert.equal(timeline.snapshots[0].metrics["aic-per-successful-run"], 0);
});
