import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

import { generateReport, renderReport, validateEvidence } from "../../continuous-architecture-review/report.mjs";

const script = path.resolve("continuous-architecture-review/report.mjs");

function evidence(date = "2026-10-31T12:00:00Z") {
  const check = () => ({ status: "PASS", evidence: "evidence/review.md" });
  return {
    campaign: "Continuous architecture review",
    commit: "a".repeat(40),
    date,
    checks: {
      execution: check(),
      security: { ...check(), critical: false },
      reliability: check(),
      scalability: check(),
      tests: check(),
      legacy: check(),
      duplicates: check(),
      dependencies: check(),
      architecture: check(),
    },
    counts: { securityBlockers: null, duplicatePaths: null, legacyComponents: null },
    findings: { improved: [], regressed: [], blocked: [] },
    nextActions: [],
  };
}

function workspace(t) {
  const base = path.resolve("test-results");
  fs.mkdirSync(base, { recursive: true });
  const root = fs.mkdtempSync(path.join(base, "campaign-readiness-"));
  t.after(() => fs.rmSync(root, { force: true, recursive: true }));
  return root;
}

function snapshot(root, date) {
  return path.join(root, "continuous-architecture-review", "reports", `${date.replaceAll(":", "-")}.json`);
}

test("renders deterministic GFM summary and escapes untrusted references and findings", () => {
  const input = evidence();
  input.checks.architecture.evidence = "review/<script>|[bad](url).md";
  input.findings.blocked = ["<ScRiPt>alert(1)</sCrIpT>|table"];
  input.findings.regressed = ["regression", "another regression", "fourth finding"];
  input.nextActions = ["first", "second", "third", "fourth"];
  const report = renderReport(input);
  assert.equal(report, renderReport(input));
  assert.doesNotMatch(report, /[ \t]+$/m);
  assert.match(report, /^# CAO Campaign Readiness\n/m);
  assert.match(report, /\*\*Gate:\*\* \*\*READY\*\*/);
  assert.match(report, /\| Campaign readiness \| PASS \| — \| PASS \| — \|/);
  assert.match(report, /\| Security blockers \| — \| — \| — \| — \|/);
  assert.equal((report.match(/<details>/g) ?? []).length, 3);
  assert.match(report, /&lt;script&gt;/);
  assert.match(report, /- Blocked by: &lt;ScRiPt&gt;/);
  assert.doesNotMatch(report, /<script>|fourth finding|fourth\n/i);
  const actions = report.split("## Next Actions")[1].split("<details>")[0];
  assert.match(actions, /\n1\. first\n2\. second\n3\. third\n/);
  assert.doesNotMatch(actions, /^- /m);
  assert.equal(report.split("## Key Findings")[1].split("## Next Actions")[0].match(/^- /gm).length, 3);
});

test("no action evidence stays unnumbered rather than inventing an action", () => {
  const actions = renderReport(evidence()).split("## Next Actions")[1].split("<details>")[0];
  assert.match(actions, /No next actions recorded\./);
  assert.doesNotMatch(actions, /^(?:- |\d+\. )/m);
});

test("history uses the first dated snapshot as baseline, nearest earlier as previous, and never overwrites a rerun", (t) => {
  const root = workspace(t);
  const inputPath = path.join(root, "input.json");
  const write = (value) => {
    fs.writeFileSync(inputPath, JSON.stringify(value));
    return generateReport(inputPath, root);
  };
  const first = evidence("2026-10-01T00:00:00Z");
  first.checks.architecture.status = "FAIL";
  first.counts.duplicatePaths = 8;
  write(first);
  const original = fs.readFileSync(snapshot(root, first.date), "utf8");
  const middle = evidence("2026-10-20T00:00:00Z");
  middle.checks.architecture.status = "WARN";
  middle.counts.duplicatePaths = 5;
  write(middle);
  const newest = evidence("2026-10-31T12:00:00Z");
  newest.counts.duplicatePaths = 3;
  const report = write(newest);
  assert.match(report, /\| Architecture health \| FAIL \| WARN \| PASS \| Improved \|/);
  assert.match(report, /\| Duplicate paths \| 8 \| 5 \| 3 \| -2 \(improved\) \|/);
  assert.match(report, /Baseline: 2026-10-01T00:00:00Z/);
  assert.match(report, /Previous: 2026-10-20T00:00:00Z/);
  assert.equal(write(newest), report);
  assert.equal(fs.readFileSync(snapshot(root, first.date), "utf8"), original);
  assert.equal(fs.readFileSync(snapshot(root, newest.date), "utf8"), `${JSON.stringify(validateEvidence(newest), null, 2)}\n`);
  assert.equal(fs.readFileSync(path.join(root, "campaign-readiness.md"), "utf8"), report);
  assert.equal(fs.readFileSync(snapshot(root, newest.date).replace(/\.json$/, ".md"), "utf8"), report);
});

test("backfilled history cannot silently rewrite a prior Markdown snapshot or the current report", (t) => {
  const root = workspace(t);
  const inputPath = path.join(root, "input.json");
  const later = evidence("2026-11-02T00:00:00Z");
  fs.writeFileSync(inputPath, JSON.stringify(later));
  const oldReport = generateReport(inputPath, root);
  const oldSnapshot = fs.readFileSync(snapshot(root, later.date), "utf8");
  const oldMarkdown = fs.readFileSync(snapshot(root, later.date).replace(/\.json$/, ".md"), "utf8");
  const earlier = evidence("2026-11-01T00:00:00Z");
  fs.writeFileSync(inputPath, JSON.stringify(earlier));
  const report = generateReport(inputPath, root);
  assert.match(report, /Previous: — \(no earlier snapshot\)/);
  assert.match(report, /\| Campaign readiness \| PASS \| — \| PASS \| — \|/);
  fs.writeFileSync(inputPath, JSON.stringify(later));
  assert.throws(() => generateReport(inputPath, root), /Conflicting historical report/);
  assert.equal(fs.readFileSync(snapshot(root, later.date), "utf8"), oldSnapshot);
  assert.equal(fs.readFileSync(snapshot(root, later.date).replace(/\.json$/, ".md"), "utf8"), oldMarkdown);
  assert.equal(oldMarkdown, oldReport);
  assert.equal(fs.readFileSync(path.join(root, "campaign-readiness.md"), "utf8"), report);
});

test("previous selects the latest instant even with mixed fractional timestamp precision", () => {
  const first = evidence("2026-10-31T12:00:00.1Z");
  const second = evidence("2026-10-31T12:00:00.123Z");
  const current = evidence("2026-10-31T12:00:01Z");
  const report = renderReport(current, [second, first]);
  assert.match(report, /Baseline: 2026-10-31T12:00:00\.1Z/);
  assert.match(report, /Previous: 2026-10-31T12:00:00\.123Z/);
  assert.throws(() => renderReport(current, [
    evidence("2026-10-31T12:00:00Z"),
    evidence("2026-10-31T12:00:00.000Z"),
  ]), /Duplicate historical timestamp/);
});

test("unknown observations are not invented as zero or a clean gate", () => {
  const input = evidence();
  input.checks.security.critical = null;
  assert.match(renderReport(input), /\*\*Gate:\*\* \*\*UNKNOWN\*\*/);
  input.checks.security.critical = false;
  input.checks.tests.evidence = null;
  assert.match(renderReport(input), /\*\*Gate:\*\* \*\*UNKNOWN\*\*/);
  input.checks.tests.evidence = "evidence/test.md";
  input.checks.legacy.status = "UNKNOWN";
  assert.match(renderReport(input), /\*\*Gate:\*\* \*\*CONDITIONAL\*\*/);
  assert.match(renderReport(input), /\| Legacy components \| — \| — \| — \| — \|/);
});

test("critical or explicit security failure blocks regardless of missing blocker counts", () => {
  const input = evidence();
  input.checks.security.status = "FAIL";
  input.checks.security.critical = true;
  assert.match(renderReport(input), /\*\*Gate:\*\* \*\*BLOCKED\*\*/);
  assert.match(renderReport(input), /\| Campaign readiness \| FAIL \| — \| FAIL \| — \|/);
  input.checks.security.critical = false;
  input.counts.securityBlockers = 0;
  assert.match(renderReport(input), /\*\*Gate:\*\* \*\*BLOCKED\*\*/);
  input.checks.security.status = "PASS";
  input.checks.tests.status = "FAIL";
  assert.match(renderReport(input), /\*\*Gate:\*\* \*\*BLOCKED\*\*/);
  input.checks.tests.status = "PASS";
  input.checks.execution.status = "FAIL";
  assert.match(renderReport(input), /\*\*Gate:\*\* \*\*BLOCKED\*\*/);
});

test("explicit readiness failure outranks unknown critical status or missing other evidence", () => {
  for (const failed of ["security", "execution", "tests"]) {
    const input = evidence();
    input.checks[failed].status = "FAIL";
    input.checks.security.critical = null;
    input.checks.execution.evidence = null;
    input.checks.tests.evidence = null;
    const report = renderReport(input);
    assert.match(report, /\*\*Gate:\*\* \*\*BLOCKED\*\*/);
    assert.match(report, /\| Campaign readiness \| FAIL \| — \| FAIL \| — \|/);
  }
});

test("rejects contradictory critical evidence but preserves unknown counts and critical uncertainty", () => {
  const input = evidence();
  input.checks.security.critical = true;
  assert.throws(() => validateEvidence(input), /critical security finding contradicts PASS/);
  input.checks.security.status = "FAIL";
  assert.match(renderReport(input), /\*\*Gate:\*\* \*\*BLOCKED\*\*/);
  input.counts.securityBlockers = 0;
  assert.throws(() => validateEvidence(input), /zero observed blockers/);
  input.counts.securityBlockers = null;
  input.checks.security.critical = null;
  input.checks.security.status = "UNKNOWN";
  assert.match(renderReport(input), /\*\*Gate:\*\* \*\*UNKNOWN\*\*/);
});

test("invalid inputs and conflicting timestamps leave existing report and snapshot untouched", (t) => {
  const root = workspace(t);
  const inputPath = path.join(root, "input.json");
  const input = evidence();
  fs.writeFileSync(inputPath, JSON.stringify(input));
  const original = generateReport(inputPath, root);
  const originalSnapshot = fs.readFileSync(snapshot(root, input.date), "utf8");
  const invalid = [
    { ...input, commit: "short" },
    { ...input, date: "2026-02-31T00:00:00Z" },
    { ...input, checks: { ...input.checks, tests: { status: "PASS", evidence: "line\ninjected" } } },
    { ...input, checks: { ...input.checks, security: { status: "PASS", evidence: "review" } } },
    { ...input, counts: { ...input.counts, securityBlockers: -1 } },
    { ...input, counts: { ...input.counts, securityBlockers: 0.1 } },
    { ...input, checks: { ...input.checks, security: { ...input.checks.security, critical: true } } },
    {
      ...input,
      checks: { ...input.checks, security: { ...input.checks.security, status: "FAIL", critical: true } },
      counts: { ...input.counts, securityBlockers: 0 },
    },
    { ...input, unknown: true },
    { ...input, findings: { ...input.findings, improved: "not an array" } },
    { ...input, commit: "b".repeat(40) },
  ];
  for (const candidate of invalid) {
    fs.writeFileSync(inputPath, JSON.stringify(candidate));
    assert.throws(() => generateReport(inputPath, root));
    assert.equal(fs.readFileSync(path.join(root, "campaign-readiness.md"), "utf8"), original);
    assert.equal(fs.readFileSync(snapshot(root, input.date), "utf8"), originalSnapshot);
  }
});

test("malformed history and symlinked output are rejected without replacing the main report", (t) => {
  const root = workspace(t);
  const inputPath = path.join(root, "input.json");
  fs.writeFileSync(inputPath, JSON.stringify(evidence()));
  const report = generateReport(inputPath, root);
  const history = path.join(root, "continuous-architecture-review", "reports");
  fs.writeFileSync(path.join(history, "2026-10-01T00-00-00Z.json"), '{"date":"2026-10-01T00:00:00Z"}');
  assert.throws(() => generateReport(inputPath, root), /historical|expected exactly/i);
  assert.equal(fs.readFileSync(path.join(root, "campaign-readiness.md"), "utf8"), report);
  fs.rmSync(path.join(history, "2026-10-01T00-00-00Z.json"));
  fs.writeFileSync(path.join(history, "unexpected.json"), "{}");
  assert.throws(() => generateReport(inputPath, root), /Invalid historical snapshot filename/);
  fs.rmSync(path.join(history, "unexpected.json"));
  fs.rmSync(path.join(root, "campaign-readiness.md"));
  fs.symlinkSync(inputPath, path.join(root, "campaign-readiness.md"));
  assert.throws(() => generateReport(inputPath, root), /Symlink not allowed/);
});

test("symlinked evidence is rejected before reading without changing the report", (t) => {
  const root = workspace(t);
  const inputPath = path.join(root, "input.json");
  fs.writeFileSync(inputPath, JSON.stringify(evidence()));
  const original = generateReport(inputPath, root);
  const originalSnapshot = fs.readFileSync(snapshot(root, evidence().date), "utf8");
  const link = path.join(root, "linked-evidence.json");
  fs.symlinkSync(inputPath, link);
  assert.throws(() => generateReport(link, root), /Symlink not allowed/);
  assert.equal(fs.readFileSync(path.join(root, "campaign-readiness.md"), "utf8"), original);
  assert.equal(fs.readFileSync(snapshot(root, evidence().date), "utf8"), originalSnapshot);
});

test("CLI accepts an evidence path and output root and fails on malformed JSON", (t) => {
  const root = workspace(t);
  const inputPath = path.join(root, "input.json");
  fs.writeFileSync(inputPath, JSON.stringify(evidence()));
  const command = () => spawnSync(process.execPath, [script, inputPath, root], { encoding: "utf8" });
  assert.equal(command().status, 0);
  const original = fs.readFileSync(path.join(root, "campaign-readiness.md"), "utf8");
  fs.writeFileSync(inputPath, "{broken");
  assert.notEqual(command().status, 0);
  assert.equal(fs.readFileSync(path.join(root, "campaign-readiness.md"), "utf8"), original);
});
