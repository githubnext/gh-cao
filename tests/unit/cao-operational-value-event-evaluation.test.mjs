import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";

const root = path.resolve(import.meta.dirname, "../..");
const evaluator = path.join(root, ".github/skills/add-operational-value/scripts/evaluate.mjs");
const endAt = "2026-05-01T00:00:00Z";
const startAt = "2026-01-01T00:00:00Z";
const slug = "example-value";
const repository = "example/repository";

function fixture(t, { withEvents = true } = {}) {
  mkdirSync(path.join(root, ".tmp"), { recursive: true });
  const directory = mkdtempSync(path.join(root, ".tmp", "value-event-evaluation-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const module = path.join(directory, "value.mjs");
  const input = path.join(directory, "events.json");
  const calls = path.join(directory, "event-calls.txt");
  const reportDirectory = path.join(directory, "reports", "example-repository");
  const file = suffix => path.join(reportDirectory, `${slug}-${suffix}`);
  const ledger = {
    schemaVersion: 1, kind: "operational-value-events", repository, workflowSlug: slug,
    workflowName: "Example Value", sourcePath: `.github/workflows/${slug}.md`,
    intent: { goal: "Reduce the native repository backlog.", acceptance: "A linked change is merged." },
    observedAt: endAt, cadenceDays: 1, basis: "workflow-contribution",
    metric: { id: "delivered", name: "Delivered reductions", version: "1", unit: "files",
      direction: "increase", formula: "eligible before minus eligible after" },
    window: { startAt, endAt },
    coverage: [{ startAt, endAt, status: "complete" }], opportunities: 2,
    events: [{ id: "PR_1", at: "2026-02-01T00:00:00Z", value: 1,
      acceptance: { state: "accepted", at: "2026-02-01T00:00:00Z", ref: "merge-sha" },
      attribution: "corroborated", label: "Accepted reduction", detail: "Native first-parent change.",
      evidence: [{ label: "Merged PR", url: "https://github.com/example/repository/pull/1" }] }],
    limitations: ["Contribution is not exclusive causal credit."],
  };
  writeFileSync(input, JSON.stringify(ledger));
  writeFileSync(module, `
import { appendFileSync, readFileSync } from "node:fs";
export const definition = {
  schemaVersion: 3, repository: ${JSON.stringify(repository)}, slug: ${JSON.stringify(slug)},
  sourcePath: ".github/workflows/${slug}.md", workflowName: "Example Value",
  intent: { goal: "Reduce the native repository backlog." },
  adoption: { commit: "${"a".repeat(40)}", adoptedAt: ${JSON.stringify(startAt)} },
  evaluation: { mode: "attainment-only" },
  evidence: { key: "repository-backlog", repositories: [${JSON.stringify(repository)}],
    opportunity: "Eligible files", filters: [], collection: "Preserved immutable repository snapshots",
    measurementKind: "snapshot", window: { durationDays: 7, cadenceDays: 1, maturationDays: 0 } },
  model: { architecture: "Native repository state", recommendation: "Inspect the native backlog",
    presentation: { label: "Native state", betterLabel: "Lower backlog is better" } },
  summary: { nativeLabel: "Backlog" },
  metrics: [
    { id: "backlog", name: "Backlog", role: "primary", unit: "files", direction: "decrease",
      formula: "eligible file count", presentation: { name: "Backlog", legendLabel: "Backlog", transform: "identity" } },
    { ...${JSON.stringify(ledger.metric)}, role: "diagnostic",
      presentation: { name: "Delivered", legendLabel: "Delivered", transform: "identity" } }
  ],
  validationExamples: {
    targetAttained: { backlog: 1, delivered: 2 }, targetMissed: { backlog: 2, delivered: 1 },
    missing: null, malformed: {}
  }
};
export function scoreMetric(id, evidence) { return Number.isFinite(evidence?.[id]) ? evidence[id] : null; }
export async function collectBatch(requests) {
  return requests.map(() => ({ evidence: { backlog: 2, delivered: 1, coverageStatus: "complete", maturityStatus: "matured" },
    provenance: [{ repository: ${JSON.stringify(repository)}, kind: "fixture", ref: "${"b".repeat(40)}" }] }));
}
${withEvents ? `export async function collectEvents(request) {
  appendFileSync(${JSON.stringify(calls)}, JSON.stringify(request) + "\\n");
  return JSON.parse(readFileSync(${JSON.stringify(input)}, "utf8"));
}` : ""}
`);
  return {
    ledger, file,
    change: change => {
      const next = structuredClone(ledger);
      change(next);
      writeFileSync(input, JSON.stringify(next));
    },
    calls: () => existsSync(calls) ? readFileSync(calls, "utf8").trim().split("\n").map(JSON.parse) : [],
    read: suffix => JSON.parse(readFileSync(file(suffix), "utf8")),
    evaluate: (options = ["--report"], repo = repository) => spawnSync(process.execPath, [
      evaluator, "--function", module, "--output-dir", path.join(directory, "reports"), "--end", endAt,
      "--no-runs", ...options, repo, slug,
    ], { cwd: root, encoding: "utf8" }),
  };
}

test("evaluation binds a reusable pulse and accepted-event archive to the verified frozen module", t => {
  const f = fixture(t);
  let result = f.evaluate(["--report"], "Example/Repository");
  assert.equal(result.status, 0, result.stderr);
  for (const suffix of ["timeline.json", "definitions.md", "evidence-archive.json", "events.json", "pulse.html", "pulse.json"]) {
    assert.ok(existsSync(f.file(suffix)), suffix);
  }
  const timeline = f.read("timeline.json");
  const sha = timeline.valueFunction.sha256;
  assert.equal(f.read("events.json").valueFunctionSha256, sha);
  assert.equal(f.read("pulse.json").valueFunctionSha256, sha);
  assert.equal(f.read("pulse.json").metrics[0].completeTotal, 1);
  assert.equal(f.read("evidence-archive.json").functions[sha].eventLedger.events.length, 1);
  assert.deepEqual(f.calls(), [{ windowStart: startAt, windowEnd: endAt, observedAt: endAt }]);
  result = f.evaluate();
  assert.equal(result.status, 0, result.stderr);
  assert.equal(f.calls().length, 1, "A complete same-endpoint ledger must be reused.");
  result = f.evaluate([]);
  assert.equal(result.status, 0, result.stderr);
  assert.ok(f.read("evidence-archive.json").functions[sha].eventLedger, "Legacy runs must preserve the event archive.");
  result = f.evaluate(["--report", "--refresh"]);
  assert.equal(result.status, 0, result.stderr);
  assert.equal(f.calls().length, 2);
  f.change(ledger => { ledger.events[0].value = 2; });
  result = f.evaluate(["--report", "--refresh"]);
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /changed archived accepted facts/);
  assert.equal(f.read("events.json").events[0].value, 1, "Failed refresh must not replace published artifacts.");
  f.change(ledger => { ledger.events = []; });
  result = f.evaluate(["--report", "--refresh"]);
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /changed archived accepted facts/);
});

test("event contract and scope mismatches fail before artifacts are published", t => {
  const f = fixture(t);
  const changes = [
    ledger => { ledger.repository = "another/repository"; },
    ledger => { ledger.workflowSlug = "another-workflow"; },
    ledger => { ledger.sourcePath = ".github/workflows/another.md"; },
    ledger => { ledger.intent.goal = "A different outcome"; },
    ledger => { ledger.metric.id = "unregistered-metric"; },
    ledger => { ledger.metric.version = "2"; },
    ledger => { ledger.metric.formula = "activity volume"; },
    ledger => { ledger.metric.unit = "runs"; },
    ledger => { ledger.metric.direction = "decrease"; },
    ledger => { ledger.window.startAt = "2026-02-01T00:00:00Z"; },
    ledger => { ledger.window.endAt = "2026-04-30T00:00:00Z"; },
    ledger => { ledger.observedAt = "2026-05-02T00:00:00Z"; },
    ledger => { ledger.metric = { ...ledger.metric, id: "backlog", direction: "decrease",
      formula: "eligible file count", version: undefined }; },
  ];
  for (const change of changes) {
    f.change(change);
    const result = f.evaluate();
    assert.notEqual(result.status, 0, result.stdout);
    assert.match(result.stderr, /does not match the frozen value function or evaluation scope/);
    assert.ok(!existsSync(f.file("timeline.json")));
    assert.ok(!existsSync(f.file("pulse.html")));
  }
});

test("ordinary evaluation remains opt-in and report fallback renders the existing native timeline", t => {
  const f = fixture(t, { withEvents: false });
  let result = f.evaluate([]);
  assert.equal(result.status, 0, result.stderr);
  assert.ok(existsSync(f.file("timeline.json")));
  assert.ok(!existsSync(f.file("pulse.html")));
  result = f.evaluate();
  assert.equal(result.status, 0, result.stderr);
  assert.ok(existsSync(f.file("pulse.html")));
  assert.ok(!existsSync(f.file("events.json")));
  const pulse = f.read("pulse.json");
  assert.equal(pulse.metrics[0].kind, "snapshot");
  assert.equal(pulse.metrics[0].basis, "repository-outcome");
  assert.equal(pulse.metrics[0].cumulative.length, 0);
  const observations = readFileSync(f.file("measurements.jsonl"), "utf8").trim().split("\n").map(JSON.parse);
  assert.ok(observations.length > 0);
  assert.ok(observations.every(row => row.schema_version === 2 && row.kind === "operational_value"
    && row.operational_value.repository === repository
    && row.operational_value.workflow_slug === slug
    && row.operational_value.evaluator_digest === f.read("timeline.json").valueFunction.sha256));
});
