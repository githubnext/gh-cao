import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";
import { operationalValueEvidenceJsonl } from "../../.github/skills/add-operational-value/scripts/export-evidence.mjs";
import { createNativeValueOutput, importNativeValueEvidence, readNativeValueEvidence } from "../../.github/skills/add-operational-value/scripts/prepare-native-value.mjs";

const root = path.resolve(import.meta.dirname, "../..");
const digest = bytes => createHash("sha256").update(bytes).digest("hex");

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
