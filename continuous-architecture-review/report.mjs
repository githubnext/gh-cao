import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const REPOSITORY_ROOT = fileURLToPath(new URL("../", import.meta.url));
const CHECKS = ["execution", "security", "reliability", "scalability", "tests", "legacy", "duplicates", "dependencies", "architecture"];
const ARCHITECTURE_CHECKS = ["architecture", "reliability", "scalability", "legacy", "duplicates", "dependencies"];
const COUNTS = ["securityBlockers", "duplicatePaths", "legacyComponents"];
const STATUSES = ["PASS", "WARN", "FAIL", "UNKNOWN"];
const TIMESTAMP = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,3})?Z$/;
const HISTORY_FILE = /^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}(?:\.\d{1,3})?Z\.json$/;

function object(value, keys, label) {
  if (value === null || typeof value !== "object" || Array.isArray(value) ||
      Object.keys(value).sort().join(",") !== [...keys].sort().join(",")) {
    throw new Error(`${label}: expected exactly ${keys.join(", ")}`);
  }
}

function line(value, label, nullable = false) {
  if (nullable && value === null) return null;
  if (typeof value !== "string" || value.length < 1 || value.length > 500 ||
      value !== value.trim() || /[\u0000-\u001f\u007f-\u009f]/u.test(value)) {
    throw new Error(`${label}: expected a nonempty, single-line reference (at most 500 characters)`);
  }
  return value;
}

function list(value, label) {
  if (!Array.isArray(value)) throw new Error(`${label}: expected an array`);
  return value.map((entry, index) => line(entry, `${label}[${index}]`));
}

export function validateEvidence(value) {
  object(value, ["campaign", "commit", "date", "checks", "counts", "findings", "nextActions"], "evidence");
  const campaign = line(value.campaign, "campaign");
  if (typeof value.commit !== "string" || !/^[0-9a-f]{40}$/i.test(value.commit)) {
    throw new Error("commit: expected a 40-character hexadecimal SHA");
  }
  if (typeof value.date !== "string" || !TIMESTAMP.test(value.date) ||
      !Number.isFinite(Date.parse(value.date)) ||
      new Date(value.date).toISOString().slice(0, 19) !== value.date.slice(0, 19)) {
    throw new Error("date: expected a valid ISO UTC timestamp");
  }
  object(value.checks, CHECKS, "checks");
  const checks = Object.fromEntries(CHECKS.map((name) => {
    const check = value.checks[name];
    object(check, name === "security" ? ["status", "critical", "evidence"] : ["status", "evidence"], `checks.${name}`);
    if (!STATUSES.includes(check.status)) throw new Error(`checks.${name}.status: invalid status`);
    const evidence = line(check.evidence, `checks.${name}.evidence`, true);
    if (name === "security" && check.critical !== true && check.critical !== false && check.critical !== null) {
      throw new Error("checks.security.critical: expected true, false, or null");
    }
    return [name, name === "security"
      ? { status: check.status, critical: check.critical, evidence }
      : { status: check.status, evidence }];
  }));
  object(value.counts, COUNTS, "counts");
  const counts = Object.fromEntries(COUNTS.map((name) => {
    const count = value.counts[name];
    if (count !== null && (!Number.isSafeInteger(count) || count < 0)) {
      throw new Error(`counts.${name}: expected a nonnegative safe integer or null`);
    }
    return [name, count];
  }));
  if (checks.security.critical === true &&
      (checks.security.status === "PASS" || counts.securityBlockers === 0)) {
    throw new Error("checks.security.critical: critical security finding contradicts PASS or zero observed blockers");
  }
  object(value.findings, ["improved", "regressed", "blocked"], "findings");
  const findings = Object.fromEntries(["improved", "regressed", "blocked"].map((name) =>
    [name, list(value.findings[name], `findings.${name}`)]));
  return {
    campaign, commit: value.commit.toLowerCase(), date: value.date,
    checks, counts, findings, nextActions: list(value.nextActions, "nextActions"),
  };
}

function markdown(value) {
  return String(value).replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")
    .replace(/[\\`*_{}[\]()#+.!|~-]/g, "\\$&");
}

function aggregate(statuses) {
  if (statuses.includes("FAIL")) return "FAIL";
  if (statuses.includes("UNKNOWN")) return "UNKNOWN";
  if (statuses.includes("WARN")) return "WARN";
  return "PASS";
}

function readiness(input) {
  const checks = input.checks;
  const statuses = ["execution", "security", "tests"].map((name) => checks[name].status);
  if (checks.security.critical === true || statuses.includes("FAIL")) return "FAIL";
  if (checks.security.critical === null ||
      ["execution", "security", "tests"].some((name) => checks[name].evidence === null)) {
    return "UNKNOWN";
  }
  return aggregate(statuses);
}

function gate(input) {
  const checks = input.checks;
  if (checks.security.critical === true || checks.security.status === "FAIL" ||
      checks.execution.status === "FAIL" || checks.tests.status === "FAIL") return "BLOCKED";
  if (readiness(input) === "UNKNOWN") return "UNKNOWN";
  if (CHECKS.some((name) => checks[name].status !== "PASS" || checks[name].evidence === null)) {
    return "CONDITIONAL";
  }
  return "READY";
}

function architecture(input) {
  return aggregate(ARCHITECTURE_CHECKS.map((name) =>
    input.checks[name].evidence === null ? "UNKNOWN" : input.checks[name].status));
}

function statusChange(current, previous) {
  if (!previous || current === "UNKNOWN" || previous === "UNKNOWN") return "—";
  const order = { FAIL: 0, WARN: 1, PASS: 2 };
  return current === previous ? "No change" : order[current] > order[previous] ? "Improved" : "Regressed";
}

function countChange(current, previous) {
  if (current === null || previous === null || previous === undefined) return "—";
  const delta = current - previous;
  return `${delta >= 0 ? "+" : ""}${delta} (${delta < 0 ? "improved" : delta > 0 ? "regressed" : "unchanged"})`;
}

function snapshotName(date) {
  return date.replaceAll(":", "-");
}

export function renderReport(evidence, history = []) {
  const current = validateEvidence(evidence);
  const snapshots = history.map(validateEvidence);
  const seen = new Set();
  for (const entry of snapshots) {
    if (entry.campaign !== current.campaign) throw new Error("Historical snapshot belongs to another campaign");
    const instant = Date.parse(entry.date);
    if (seen.has(instant)) throw new Error(`Duplicate historical timestamp: ${entry.date}`);
    seen.add(instant);
    if (instant === Date.parse(current.date) && JSON.stringify(entry) !== JSON.stringify(current)) {
      throw new Error(`Conflicting snapshot for ${current.date}`);
    }
  }
  const prior = snapshots.filter((entry) => Date.parse(entry.date) < Date.parse(current.date))
    .sort((a, b) => Date.parse(a.date) - Date.parse(b.date));
  const baseline = prior[0] ?? current;
  const previous = prior.at(-1);
  const statusRow = (title, select) => {
    const now = select(current);
    return `| ${title} | ${select(baseline)} | ${previous ? select(previous) : "—"} | ${now} | ${statusChange(now, previous && select(previous))} |`;
  };
  const countRow = (title, name) => {
    const display = (entry) => entry?.counts[name] ?? "—";
    return `| ${title} | ${display(baseline)} | ${display(previous)} | ${display(current)} | ` +
      `${countChange(current.counts[name], previous?.counts[name])} |`;
  };
  const findings = [
    ...current.findings.blocked.map((item) => `Blocked by: ${item}`),
    ...current.findings.regressed.map((item) => `Regressed: ${item}`),
    ...current.findings.improved.map((item) => `Improved: ${item}`),
  ].slice(0, 3);
  const bullets = (items, fallback) => items.length ? items.map((item) => `- ${markdown(item)}`) : [`- ${fallback}`];
  const actions = current.nextActions.slice(0, 3).map((item, index) => `${index + 1}. ${markdown(item)}`);
  const checkRows = (names) => names.map((name) =>
    `| ${name[0].toUpperCase()}${name.slice(1)} | ${current.checks[name].status} | ` +
    `${current.checks[name].evidence === null ? "—" : markdown(current.checks[name].evidence)} |`);
  const lines = [
    "# CAO Campaign Readiness", "",
    `**Campaign:** ${markdown(current.campaign)}`, "",
    `**Commit:** \`${current.commit}\``, "",
    `**Date:** ${current.date}`, "",
    `**Gate:** **${gate(current)}**`, "",
    "## Summary", "",
    "| Metric | Baseline | Previous | Current | Change |",
    "| --- | --- | --- | --- | --- |",
    statusRow("Campaign readiness", readiness),
    statusRow("Architecture health", architecture),
    countRow("Security blockers", "securityBlockers"),
    countRow("Duplicate paths", "duplicatePaths"),
    countRow("Legacy components", "legacyComponents"), "",
    "## Key Findings", "",
    ...bullets(findings, "No findings recorded."), "",
    "## Next Actions", "",
    ...(actions.length ? actions : ["No next actions recorded."]), "",
    "<details>",
    "<summary>Readiness checks</summary>", "",
    "| Check | Status | Evidence |", "| --- | --- | --- |",
    ...checkRows(["execution", "security", "tests"]), "",
    `Security critical: **${current.checks.security.critical === null ? "UNKNOWN" : current.checks.security.critical ? "YES" : "NO"}**.`,
    "</details>", "",
    "<details>",
    "<summary>Architecture and legacy cleanup</summary>", "",
    "| Check | Status | Evidence |", "| --- | --- | --- |",
    ...checkRows(ARCHITECTURE_CHECKS), "",
    "</details>", "",
    "<details>",
    "<summary>History and technical evidence</summary>", "",
    `Baseline: ${baseline.date} (\`${baseline.commit}\`).`,
    `Previous: ${previous ? `${previous.date} (\`${previous.commit}\`)` : "— (no earlier snapshot)"}.`,
    `Current snapshot: \`continuous-architecture-review/reports/${snapshotName(current.date)}.json\`.`, "",
    "Methodology: readiness aggregates execution, security, and tests; architecture health aggregates " +
      "architecture, reliability, scalability, legacy, duplicates, and dependencies. FAIL outranks UNKNOWN, " +
        "which outranks WARN, then PASS. Missing check evidence is treated as UNKNOWN for that check. " +
      "An UNKNOWN readiness status, critical security uncertainty, or missing readiness evidence makes the gate UNKNOWN; " +
      "security, execution, or test failure and critical security findings always BLOCKED. " +
      "Other non-PASS or unevidenced checks make the gate CONDITIONAL. " +
      "Status changes compare FAIL < WARN < PASS; UNKNOWN cannot be compared. " +
      "Counts are shown only when explicitly observed; a negative count change is an improvement. " +
      "The first dated snapshot is the baseline; the latest earlier snapshot is previous. " +
      "Historical Markdown snapshots are immutable: backfilled history that would change one fails closed.",
    "</details>", "",
  ];
  return lines.join("\n");
}

function assertNoSymlink(target) {
  const resolved = path.resolve(target);
  const root = path.parse(resolved).root;
  let segment = root;
  for (const part of resolved.slice(root.length).split(path.sep).filter(Boolean)) {
    segment = path.join(segment, part);
    try {
      if (fs.lstatSync(segment).isSymbolicLink()) throw new Error(`Symlink not allowed: ${segment}`);
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
    }
  }
}

function readHistory(directory, campaign) {
  if (!fs.existsSync(directory)) return [];
  return fs.readdirSync(directory).filter((name) => name.endsWith(".json")).map((name) => {
    if (!HISTORY_FILE.test(name)) throw new Error(`Invalid historical snapshot filename: ${name}`);
    const file = path.join(directory, name);
    assertNoSymlink(file);
    const entry = validateEvidence(JSON.parse(fs.readFileSync(file, "utf8")));
    if (`${snapshotName(entry.date)}.json` !== name || entry.campaign !== campaign) {
      throw new Error(`Invalid historical snapshot: ${file}`);
    }
    return entry;
  });
}

export function generateReport(evidencePath, outputRoot = REPOSITORY_ROOT) {
  assertNoSymlink(evidencePath);
  const current = validateEvidence(JSON.parse(fs.readFileSync(evidencePath, "utf8")));
  const root = path.resolve(outputRoot);
  const directory = path.join(root, "continuous-architecture-review", "reports");
  const reportPath = path.join(root, "campaign-readiness.md");
  const jsonPath = path.join(directory, `${snapshotName(current.date)}.json`);
  const historyPath = path.join(directory, `${snapshotName(current.date)}.md`);
  for (const target of [root, directory, reportPath, jsonPath, historyPath]) assertNoSymlink(target);
  const history = readHistory(directory, current.campaign);
  const canonical = `${JSON.stringify(current, null, 2)}\n`;
  const existing = history.find((entry) => Date.parse(entry.date) === Date.parse(current.date));
  if (existing && JSON.stringify(existing) !== JSON.stringify(current)) {
    throw new Error(`Conflicting snapshot for ${current.date}`);
  }
  if (existing && !fs.existsSync(jsonPath)) {
    throw new Error(`Conflicting snapshot timestamp for ${current.date}`);
  }
  const report = renderReport(current, history);
  if (fs.existsSync(historyPath) && fs.readFileSync(historyPath, "utf8") !== report) {
    throw new Error(`Conflicting historical report for ${current.date}`);
  }
  fs.mkdirSync(directory, { recursive: true });
  if (!existing) fs.writeFileSync(jsonPath, canonical, { flag: "wx" });
  if (!fs.existsSync(historyPath)) fs.writeFileSync(historyPath, report, { flag: "wx" });
  fs.writeFileSync(reportPath, report);
  return report;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  if (process.argv.length < 3 || process.argv.length > 4) {
    console.error("Usage: node continuous-architecture-review/report.mjs <evidence.json> [output-dir]");
    process.exitCode = 1;
  } else {
    try {
      generateReport(process.argv[2], process.argv[3]);
    } catch (error) {
      console.error(`Campaign readiness report: ${error.message}`);
      process.exitCode = 1;
    }
  }
}
