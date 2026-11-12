#!/usr/bin/env node
// Validate and summarize the authoritative append-only campaign transaction logs.
import { readdirSync, readFileSync, existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const TRANSACTION_SCHEMA = "cao.eslint-rules.transaction";
export const SUPPORTED_SCHEMA_VERSIONS = [1];
export const TRANSACTION_KINDS = [
  "adoption-request",
  "lint-inventory",
  "repository-priority",
  "rule-candidate",
  "rule-normalization",
  "rule-outcome",
];

const IDENTIFIER_PATTERN = /^[a-z0-9][a-z0-9._-]{0,80}$/;
const LOG_BASENAME_PATTERN = /^[a-z0-9][a-z0-9._-]{0,239}$/;
const REPOSITORY_PATTERN = /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/;
const TIMESTAMP_PATTERN = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/;
const WORKER_PATTERN = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

export class RulesDatabaseError extends Error {}

function fail(message) {
  throw new RulesDatabaseError(message);
}

function requireString(record, field, where) {
  const value = record[field];
  if (typeof value !== "string" || value.length === 0) fail(`${where}: "${field}" must be a non-empty string`);
  return value;
}

function optionalString(record, field, where) {
  if (record[field] === undefined || record[field] === null) return "";
  if (typeof record[field] !== "string") fail(`${where}: "${field}" must be a string when present`);
  return record[field];
}

/** Reads every transaction log in the memory directory in deterministic order. */
export function readTransactionLogs(memoryDirectory) {
  const logDirectory = path.join(memoryDirectory, "transactions");
  if (!existsSync(logDirectory)) return [];
  const files = readdirSync(logDirectory, { withFileTypes: true })
    .filter((entry) => entry.isFile() && entry.name.endsWith(".jsonl"))
    .map((entry) => entry.name)
    .sort();
  return files.map((name) => {
    if (!LOG_BASENAME_PATTERN.test(name.slice(0, -".jsonl".length))) {
      fail(`transactions/${name}: log file name is not collision-safe`);
    }
    return { name, text: readFileSync(path.join(logDirectory, name), "utf8") };
  });
}

/** Stable JSON with lexicographically sorted object keys at every depth. */
export function canonicalJson(value) {
  if (Array.isArray(value)) return `[${value.map((item) => canonicalJson(item)).join(",")}]`;
  if (value !== null && typeof value === "object") {
    const entries = Object.keys(value)
      .sort()
      .filter((key) => value[key] !== undefined)
      .map((key) => `${JSON.stringify(key)}:${canonicalJson(value[key])}`);
    return `{${entries.join(",")}}`;
  }
  return JSON.stringify(value === undefined ? null : value);
}

function parseTransaction(raw, sourceFile, sourceLine) {
  const where = `transactions/${sourceFile}:${sourceLine}`;
  let record;
  try {
    record = JSON.parse(raw);
  } catch {
    fail(`${where}: line is not valid JSON`);
  }
  if (record === null || typeof record !== "object" || Array.isArray(record)) {
    fail(`${where}: line must be a JSON object`);
  }
  if (record.schema !== TRANSACTION_SCHEMA) {
    fail(`${where}: unexpected schema ${JSON.stringify(record.schema ?? null)}`);
  }
  if (!SUPPORTED_SCHEMA_VERSIONS.includes(record.schema_version)) {
    fail(`${where}: unsupported schema_version ${JSON.stringify(record.schema_version ?? null)}`);
  }
  const txnId = requireString(record, "txn_id", where);
  if (!IDENTIFIER_PATTERN.test(txnId)) fail(`${where}: "txn_id" is not collision-safe`);
  const recordedAt = requireString(record, "recorded_at", where);
  if (!TIMESTAMP_PATTERN.test(recordedAt) || Number.isNaN(Date.parse(recordedAt))) {
    fail(`${where}: "recorded_at" must be an ISO 8601 UTC timestamp`);
  }
  const worker = requireString(record, "worker", where);
  if (!WORKER_PATTERN.test(worker)) fail(`${where}: "worker" is not a slug`);
  const kind = requireString(record, "kind", where);
  if (!TRANSACTION_KINDS.includes(kind)) fail(`${where}: unknown kind ${JSON.stringify(kind)}`);
  const targetRepoValue = requireString(record, "target_repo", where);
  if (!REPOSITORY_PATTERN.test(targetRepoValue)) fail(`${where}: "target_repo" must be owner/repository`);
  const targetRepo = targetRepoValue.toLowerCase();
  const ruleKey = optionalString(record, "rule_key", where);
  if (!["lint-inventory", "repository-priority"].includes(kind) && !ruleKey) {
    fail(`${where}: "rule_key" is required for kind ${kind}`);
  }
  if (ruleKey && !IDENTIFIER_PATTERN.test(ruleKey)) fail(`${where}: "rule_key" is not collision-safe`);
  const payload = record.payload;
  if (payload === null || typeof payload !== "object" || Array.isArray(payload)) {
    fail(`${where}: "payload" must be a JSON object`);
  }
  return {
    txnId,
    recordedAt,
    worker,
    kind,
    ruleKey,
    targetRepo,
    centralRepo: optionalString(record, "central_repo", where),
    correlationId: optionalString(record, "correlation_id", where),
    runUrl: optionalString(record, "run_url", where),
    sourceFile,
    sourceLine,
    payload: canonicalJson(payload),
  };
}

/** Validates every transaction and returns them in deterministic order. */
export function collectTransactions(memoryDirectory) {
  const transactions = [];
  const seen = new Map();
  for (const log of readTransactionLogs(memoryDirectory)) {
    const lines = log.text.split("\n");
    for (let index = 0; index < lines.length; index += 1) {
      const raw = lines[index];
      if (raw.length === 0) continue;
      if (raw.trim().length === 0) fail(`transactions/${log.name}:${index + 1}: line contains only whitespace`);
      const transaction = parseTransaction(raw, log.name, index + 1);
      const previous = seen.get(transaction.txnId);
      if (previous) {
        fail(
          `transactions/${log.name}:${index + 1}: duplicate txn_id ${transaction.txnId} (first seen at transactions/${previous})`,
        );
      }
      seen.set(transaction.txnId, `${log.name}:${index + 1}`);
      transactions.push(transaction);
    }
  }
  transactions.sort(
    (left, right) =>
      left.recordedAt.localeCompare(right.recordedAt) ||
      left.txnId.localeCompare(right.txnId) ||
      left.sourceFile.localeCompare(right.sourceFile) ||
      left.sourceLine - right.sourceLine,
  );
  return transactions;
}

function summariseRules(transactions) {
  const rules = new Map();
  for (const transaction of transactions) {
    if (!transaction.ruleKey) continue;
    const rule = rules.get(transaction.ruleKey) || {
      ruleKey: transaction.ruleKey,
      firstSeenAt: transaction.recordedAt,
      lastSeenAt: transaction.recordedAt,
      candidates: 0,
      outcomes: 0,
      normalizations: 0,
      adoptionRequests: 0,
      targets: new Set(),
    };
    rule.lastSeenAt = transaction.recordedAt;
    rule.targets.add(transaction.targetRepo);
    if (transaction.kind === "rule-candidate") rule.candidates += 1;
    if (transaction.kind === "rule-outcome") rule.outcomes += 1;
    if (transaction.kind === "rule-normalization") rule.normalizations += 1;
    if (transaction.kind === "adoption-request") rule.adoptionRequests += 1;
    rules.set(transaction.ruleKey, rule);
  }
  return [...rules.values()].sort((left, right) => left.ruleKey.localeCompare(right.ruleKey));
}

function summariseInventory(transactions) {
  const inventory = new Map();
  for (const transaction of transactions) {
    if (transaction.kind !== "lint-inventory") continue;
    inventory.set(transaction.targetRepo, transaction);
  }
  return [...inventory.values()].sort((left, right) => left.targetRepo.localeCompare(right.targetRepo));
}

function summarisePriorities(transactions) {
  const priorities = new Map();
  for (const transaction of transactions) {
    if (transaction.kind !== "repository-priority") continue;
    priorities.set(transaction.targetRepo, transaction);
  }
  return [...priorities.values()].sort((left, right) => left.targetRepo.localeCompare(right.targetRepo));
}

export function summarizeMemory(memoryDirectory) {
  const transactions = collectTransactions(memoryDirectory);
  return {
    transactions: transactions.length,
    rules: summariseRules(transactions).map(({ targets, ...rule }) => ({ ...rule, targets: [...targets].sort() })),
    inventory: summariseInventory(transactions),
    priorities: summarisePriorities(transactions),
  };
}

function parseArguments(argv) {
  const options = { command: argv[0] || "", memoryDirectory: "" };
  for (let index = 1; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === "--memory") options.memoryDirectory = argv[++index] || "";
    else fail(`unknown argument ${JSON.stringify(argument)}`);
  }
  return options;
}

export function run(argv) {
  const options = parseArguments(argv);
  if (options.command !== "summary" && options.command !== "verify") {
    fail("usage: rules-db.mjs <summary|verify> --memory <directory>");
  }
  if (!options.memoryDirectory) fail("--memory is required");
  if (options.command === "verify") {
    return { command: "verify", transactions: collectTransactions(options.memoryDirectory).length };
  }
  return { command: "summary", ...summarizeMemory(options.memoryDirectory) };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    process.stdout.write(`${JSON.stringify(run(process.argv.slice(2)))}\n`);
  } catch (error) {
    process.stderr.write(`rules-db: ${error instanceof Error ? error.message : String(error)}\n`);
    process.exit(1);
  }
}
