#!/usr/bin/env node

import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { lstatSync, readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { gunzipSync } from "node:zlib";
import { fileDietEvents, readFileDietProof } from "./daily-file-diet.mjs";
import { operationalValueEvidenceJsonl } from "../scripts/export-evidence.mjs";
import { createArchiveReader, linkArchiveProducers } from "../scripts/session-archive.mjs";
import { validateOperationalValueEvidence } from "../../../../dashboard/site/src/data/model/operational-value-evidence.js";
import { createNativeValueOutput, importNativeValueEvidence, readNativeValueEvidence } from "../scripts/prepare-native-value.mjs";

const hash = bytes => createHash("sha256").update(bytes).digest("hex");
const json = (file, value) => writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`, { flag: "wx", mode: 0o600 });
const usage = `Usage: node .github/skills/add-operational-value/examples/native-file-diet.mjs
  (--proof DIRECTORY | --evidence FILE --sha256 DIGEST) --output NEW_DIRECTORY
  [--archive-server ORIGIN]

Recompute retained File Diet inventories or reuse a digest-verified original
envelope, then ingest it into native CAO SQLite and execute the shipped Value
query. No HTML renderer, GitHub acquisition, collection registration or deployment.
--archive-server requires --proof and existing approved private read access;
CAO_VALUE_ARCHIVE_TOKEN supplies a local bearer token, not an approval.
Archive linkage remains private and never promotes a session to accepted value.
Output must be a new directory under this checkout's .tmp/.`;

export function parseNativeFileDietArguments(args) {
  const options = {};
  for (let index = 0; index < args.length; index++) {
    if (args[index] === "--help") return { help: true };
    const key = { "--proof": "proof", "--evidence": "evidence", "--sha256": "sha256",
      "--output": "output", "--archive-server": "archiveServer" }[args[index]];
    assert.ok(key && !options[key] && args[index + 1] && !args[index + 1].startsWith("--"),
      `Invalid or duplicate option: ${args[index]}`);
    options[key] = args[++index];
  }
  assert.ok(Boolean(options.proof) !== Boolean(options.evidence), "Choose proof recomputation or evidence reuse");
  assert.ok(options.output && (!options.evidence || /^[a-f0-9]{64}$/.test(options.sha256)),
    "Output and exact reuse digest required");
  assert.ok(!options.sha256 || options.evidence, "--sha256 requires --evidence");
  assert.ok(!options.archiveServer || options.proof, "Archive linkage requires original producer identities");
  return options;
}

function proofManifest(directory) {
  const files = readdirSync(directory).sort();
  assert.ok(files.length <= 512 && files.includes("proof.json.gz"), "Bounded retained proof required");
  let compressedBytes = 0;
  let decodedBytes = 0;
  const parts = files.map(file => {
    assert.match(file, /^(?:proof|inventory-[a-f0-9]{40})\.json\.gz$/);
    const filename = path.join(directory, file);
    assert.ok(lstatSync(filename).isFile(), "Proof must contain regular files only");
    compressedBytes += statSync(filename).size;
    assert.ok(compressedBytes <= 16 * 1024 * 1024, "Compressed proof budget exceeded");
    const bytes = readFileSync(filename);
    decodedBytes += gunzipSync(bytes, { maxOutputLength: 16 * 1024 * 1024 }).length;
    assert.ok(decodedBytes <= 64 * 1024 * 1024, "Decoded proof budget exceeded");
    return { file, bytes: bytes.length, sha256: hash(bytes) };
  });
  return { sha256: hash(JSON.stringify(parts)), compressedBytes, decodedBytes, parts };
}

export async function prepareNativeFileDiet(options, { archiveReader } = {}) {
  const start = performance.now();
  const startedAt = new Date().toISOString();
  const output = createNativeValueOutput(options.output);
  let source;
  let ledger;
  let proof;
  let envelope;
  if (options.proof) {
    source = proofManifest(options.proof);
    const retained = readFileDietProof(options.proof);
    proof = retained.proof;
    ledger = fileDietEvents(retained);
    envelope = operationalValueEvidenceJsonl(ledger);
    source.inventoryRows = [...retained.inventories.values()].reduce((total, rows) => total + rows.length, 0);
    source.uniqueBlobs = proof.uniqueBlobs;
    json(path.join(output, "events.json"), ledger);
  } else {
    envelope = readNativeValueEvidence(options.evidence, options.sha256);
    source = { sha256: options.sha256, bytes: Buffer.byteLength(envelope) };
  }
  const transport = JSON.parse(envelope);
  assert.equal(transport.schema_version, 2);
  assert.equal(transport.kind, "operational_value_evidence");
  const packet = validateOperationalValueEvidence(transport.operational_value_evidence);
  assert.equal(packet.repository, "github/gh-aw");
  assert.equal(packet.workflow, "daily-file-diet");
  const recomputeMs = performance.now() - start;
  let archive = { state: "not-requested", generation: null, requests: 0, bytes: 0, eventRows: 0 };
  if (options.archiveServer || archiveReader) {
    assert.ok(proof && ledger, "Archive linkage requires retained producer evidence");
    const reader = archiveReader ?? createArchiveReader({ url: options.archiveServer,
      signal: AbortSignal.timeout(120000),
      headers: () => process.env.CAO_VALUE_ARCHIVE_TOKEN
        ? { Authorization: `Bearer ${process.env.CAO_VALUE_ARCHIVE_TOKEN}` } : {} });
    const outputs = new Set(ledger.outputs.items.map(item => item.id));
    archive = await linkArchiveProducers({ reader, repository: proof.repository,
      producers: proof.issues.filter(issue => outputs.has(issue.id)).map(issue => ({
        outputId: issue.id,
        runId: issue.run?.validation?.status === "verified-run-reference" ? issue.reference?.runId : undefined,
        attempt: issue.run?.validation?.attempt,
      })) });
    json(path.join(output, "archive-links.json"), archive);
  }
  const archiveMs = performance.now() - start - recomputeMs;
  const { ingestMs, queryMs, ...native } = importNativeValueEvidence(envelope, output);
  const manifest = {
    schemaVersion: 1, mode: options.proof ? "retained-inventory-recomputation" : "retained-evidence-reuse",
    startedAt, processedAt: new Date().toISOString(),
    ...native,
    source, archive,
    measurement: { recomputeMs, archiveMs, ingestMs, queryMs, totalMs: performance.now() - start,
      parentMaxRssKiB: process.resourceUsage().maxRSS,
      rssScope: "Parent Node process only; measure native children separately.",
      canonicalQueries: 1, githubAcquisitionCalls: 0, gitBlobReads: 0,
      note: "Single invocation with OS caches untouched; replay does not remeasure Git blobs or prove Parquet coverage." },
  };
  json(path.join(output, "manifest.json"), manifest);
  return manifest;
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const options = parseNativeFileDietArguments(process.argv.slice(2));
  if (options.help) console.log(usage);
  else {
    const result = await prepareNativeFileDiet(options);
    console.log(JSON.stringify({ reportId: result.reportId, evidenceSha256: result.evidenceSha256,
      output: path.resolve(options.output), measurement: result.measurement, archive: result.archive.state }, null, 2));
  }
}
