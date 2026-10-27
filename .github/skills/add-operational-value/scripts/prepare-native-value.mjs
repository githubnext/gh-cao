#!/usr/bin/env node

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { lstatSync, mkdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { validateOperationalValueEvidence } from "../../../../dashboard/site/src/data/model/operational-value-evidence.js";
import { adaptCachedGhAwJsonl } from "../../../../dashboard/site/src/data/adapters/gh-aw-logs.js";
import { normalize } from "../../../../dashboard/site/src/data/normalize/index.js";

const root = path.resolve(import.meta.dirname, "../../../..");
const hash = bytes => createHash("sha256").update(bytes).digest("hex");

export function createNativeValueOutput(directory) {
  const output = path.resolve(directory);
  assert.ok(output.startsWith(`${path.join(root, ".tmp")}${path.sep}`), "Output must be below this checkout's .tmp/");
  mkdirSync(path.join(root, ".tmp"), { recursive: true });
  let parent = root;
  for (const part of path.relative(root, path.dirname(output)).split(path.sep)) {
    parent = path.join(parent, part);
    assert.ok(lstatSync(parent).isDirectory(), "Output ancestors must be regular directories");
  }
  mkdirSync(output, { mode: 0o700 });
  return output;
}

export function readNativeValueEvidence(file, sha256) {
  assert.match(sha256, /^[a-f0-9]{64}$/, "An exact retained evidence digest is required");
  assert.ok(lstatSync(file).isFile() && statSync(file).size <= 16 * 1024 * 1024,
    "Bounded regular evidence file required");
  const envelope = readFileSync(file, "utf8");
  assert.equal(hash(envelope), sha256, "Retained evidence digest mismatch");
  return envelope;
}

/** Accept originals from any verified frozen workflow contract, never a report model or executable code. */
export function importNativeValueEvidence(envelope, output) {
  assert.ok(Buffer.byteLength(envelope) <= 16 * 1024 * 1024, "Evidence envelope exceeds publication budget");
  const transports = envelope.trim().split(/\r?\n/).map(line => JSON.parse(line));
  assert.ok(transports.length > 0 && transports.length <= 20000
    && transports.every(item => item.schema_version === 2), "Bounded original envelopes required");
  let packet;
  let latest;
  if (transports.length === 1 && transports[0].kind === "operational_value_evidence") {
    assert.ok(Buffer.byteLength(envelope) <= 8 * 1024 * 1024, "Evidence packet exceeds publication budget");
    packet = validateOperationalValueEvidence(transports[0].operational_value_evidence);
  } else {
    assert.ok(transports.every(item => item.kind === "operational_value"), "Do not mix native publication contracts");
    const values = normalize(adaptCachedGhAwJsonl(envelope).observations).operationalValues;
    assert.ok(values.length > 0, "Native measurements required");
    latest = values.filter(value => value['operational-value-role'] === 'primary')
      .sort((a, b) => Date.parse(b['window-end']) - Date.parse(a['window-end'])
        || Date.parse(b.timestamp) - Date.parse(a.timestamp))[0];
    assert.ok(latest && typeof latest.repository === "string"
      && /^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(latest['workflow-slug']),
    "A primary measurement with a workflow identity is required");
    assert.ok(values.every(value => value.repository === latest.repository
      && value['workflow-slug'] === latest['workflow-slug']), "Native preparation requires one exact repository/workflow");
  }
  const repository = packet?.repository ?? latest.repository;
  const workflow = packet?.workflow ?? latest['workflow-slug'];
  const workflowRoute = `#page-workflow-value?workflow=${encodeURIComponent(`${repository}:.github/workflows/${workflow}.md`)}`;
  const evidence = path.join(output, "evidence.jsonl");
  writeFileSync(evidence, envelope, { flag: "wx", mode: 0o600 });
  const database = path.join(output, "dashboard.sqlite");
  assert.ok(!lstatSync(database, { throwIfNoEntry: false }), "Native import requires an unused database path");
  const cao = (...args) => JSON.parse(execFileSync(process.execPath,
    [path.join(root, "activity/cao.mjs"), ...args], {
      cwd: root, encoding: "utf8", timeout: 120000, maxBuffer: 16 * 1024 * 1024,
      stdio: ["ignore", "pipe", "pipe"],
    }));
  const ingestStart = performance.now();
  const ingestion = cao("ingest-jsonl", "--database", database, "--input", evidence, "--retention-days", "all");
  const ingestMs = performance.now() - ingestStart;
  const queryStart = performance.now();
  const result = cao("query", packet ? "value-report-summary" : "value-current-measurements", "--database", database,
    "--dashboard", path.join(root, "dashboard/site/dashboard.json"));
  const queryMs = performance.now() - queryStart;
  writeFileSync(path.join(output, "native-query.json"), `${JSON.stringify(result, null, 2)}\n`, { flag: "wx", mode: 0o600 });
  return {
    kind: packet ? "operational-value-evidence" : "operational-values",
    ...(packet ? { reportId: packet.id, contractDigest: packet.contractDigest, facts: packet.facts.length }
      : { observationId: latest.id, evaluatorDigest: latest['evaluator-digest'], observations: transports.length }),
    evidenceSha256: hash(envelope), evidenceBytes: Buffer.byteLength(envelope),
    observedAt: packet?.observedAt ?? latest.timestamp,
    windowStart: packet?.windowStart ?? latest['window-start'], windowEnd: packet?.windowEnd ?? latest['window-end'],
    workflowRoute,
    reportRoute: packet ? `#page-value-report?report=${encodeURIComponent(packet.id)}`
      : `#page-value-measurement?measurement=${encodeURIComponent(latest.id)}`,
    databaseBytes: statSync(database).size, ingestion, ingestMs, queryMs,
  };
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const args = process.argv.slice(2);
  if (args.length === 1 && args[0] === "--help") {
    console.log("Usage: node .github/skills/add-operational-value/scripts/prepare-native-value.mjs --input EVIDENCE_JSONL --sha256 DIGEST --output NEW_DIRECTORY\nImport one verified original envelope into native SQLite. No acquisition, scoring, enrollment or deployment.");
  } else {
    const options = {};
    for (let i = 0; i < args.length; i += 2) {
      assert.ok(["--input", "--sha256", "--output"].includes(args[i]) && !options[args[i]]
        && args[i + 1] && !args[i + 1].startsWith("--"), "Expected input, digest and new output directory");
      options[args[i]] = args[i + 1];
    }
    assert.ok(options["--input"] && options["--sha256"] && options["--output"], "Input, digest and output are required");
    const envelope = readNativeValueEvidence(options["--input"], options["--sha256"]);
    const output = createNativeValueOutput(options["--output"]);
    const result = { schemaVersion: 1, mode: "retained-evidence-import",
      ...importNativeValueEvidence(envelope, output), processedAt: new Date().toISOString() };
    writeFileSync(path.join(output, "manifest.json"), `${JSON.stringify(result, null, 2)}\n`, { flag: "wx", mode: 0o600 });
    console.log(JSON.stringify({ output, reportId: result.reportId, reportRoute: result.reportRoute }, null, 2));
  }
}
