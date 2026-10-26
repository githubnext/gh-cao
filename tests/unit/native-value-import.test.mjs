import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";
import { operationalValueEvidenceJsonl } from "../../.github/skills/add-operational-value/scripts/export-evidence.mjs";
import { createNativeValueOutput, importNativeValueEvidence, readNativeValueEvidence } from "../../.github/skills/add-operational-value/scripts/prepare-native-value.mjs";

const root = path.resolve(import.meta.dirname, "../..");
const digest = bytes => createHash("sha256").update(bytes).digest("hex");

test("ordinary reviewed batches evaluate and import natively without HTML or an event ledger", t => {
  mkdirSync(path.join(root, ".tmp"), { recursive: true });
  const temporary = mkdtempSync(path.join(root, ".tmp", "native-batch-import-"));
  t.after(() => rmSync(temporary, { recursive: true, force: true }));
  execFileSync(process.execPath, [
    ".github/skills/add-operational-value/scripts/evaluate.mjs", "--native",
    "--function", "tests/fixtures/operational-value/native-workflow.mjs", "--output-dir", temporary,
    "--end", "2026-10-15T00:00:00Z", "example/service", "incident-response",
  ], { cwd: root, stdio: "pipe" });
  const stem = path.join(temporary, "example-service/incident-response");
  assert.equal(existsSync(`${stem}-pulse.html`), false);
  assert.equal(existsSync(`${stem}-events.json`), false);
  const original = readFileSync(`${stem}-measurements.jsonl`, "utf8");
  const output = createNativeValueOutput(path.join(temporary, "native"));
  const result = importNativeValueEvidence(original, output);
  assert.equal(result.kind, "operational-values");
  assert.equal(result.observations, 2);
  assert.equal(result.observedAt, "2026-10-15T00:00:00.000Z");
  assert.equal(result.evidenceSha256, digest(original));
  assert.equal(result.reportRoute, `#page-value-measurement?measurement=${encodeURIComponent(result.observationId)}`);
  const query = JSON.parse(readFileSync(path.join(output, "native-query.json")));
  assert.equal(query.rows.length, 1);
  assert.equal(query.rows[0].value, null);
  assert.equal(query.rows[0]["measurement-label"], "Not measured");
  assert.equal(query.rows[0]["coverage-status"], "partial");
  const lines = original.trim().split("\n").map(JSON.parse);
  lines[1].operational_value.repository = "outside/service";
  assert.throws(() => importNativeValueEvidence(lines.map(JSON.stringify).join("\n"), temporary),
    /one exact repository\/workflow/);
});

test("any validated frozen workflow can use the native import boundary without File Diet assumptions", t => {
  mkdirSync(path.join(root, ".tmp"), { recursive: true });
  const temporary = mkdtempSync(path.join(root, ".tmp", "native-value-import-"));
  t.after(() => rmSync(temporary, { recursive: true, force: true }));
  const ledger = JSON.parse(readFileSync(path.join(root, "dashboard/site/test/fixtures/operational-value-ledger.json")));
  const original = operationalValueEvidenceJsonl(ledger);
  const file = path.join(temporary, "original.jsonl");
  writeFileSync(file, original);
  const output = createNativeValueOutput(path.join(temporary, "native"));
  const result = importNativeValueEvidence(readNativeValueEvidence(file, digest(original)), output);
  assert.equal(result.observedAt, ledger.observedAt);
  assert.equal(result.windowEnd, ledger.window.endAt);
  assert.equal(result.evidenceSha256, digest(original));
  assert.equal(readFileSync(path.join(output, "evidence.jsonl"), "utf8"), original);
  assert.ok(result.workflowRoute.includes(encodeURIComponent(`${ledger.repository}:${ledger.sourcePath}`)));
  const query = JSON.parse(readFileSync(path.join(output, "native-query.json")));
  assert.equal(query.rows.length, 1);
  assert.equal(query.rows[0]["output-count"], 1);
  assert.equal(query.rows[0]["native-result"], 1);
  assert.equal(query.rows[0]["complete-report"], false);
  assert.equal(query.rows[0]["recent-result"], null);
  assert.throws(() => createNativeValueOutput(output), /EEXIST/);
  assert.throws(() => readNativeValueEvidence(file, "0".repeat(64)), /digest mismatch/);
  const invalid = JSON.parse(original);
  invalid.operational_value_evidence.facts[0]["metric-version"] = "";
  assert.throws(() => importNativeValueEvidence(JSON.stringify(invalid), temporary), /Invalid operational value evidence/);
});
