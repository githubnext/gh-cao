import { strict as assert } from "node:assert";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { collectTransactions, summarizeMemory, RulesDatabaseError } from "../../eslint-rules/rules-db.mjs";

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const scriptSource = path.join(repositoryRoot, "eslint-rules/rules-db.mjs");

function transaction(overrides = {}) {
  return JSON.stringify({
    schema: "cao.eslint-rules.transaction",
    schema_version: 1,
    txn_id: "txn-0001",
    recorded_at: "2025-01-02T03:04:05Z",
    worker: "miner",
    kind: "rule-candidate",
    rule_key: "ts-no-floating-promise-in-handler",
    target_repo: "octo/app",
    central_repo: "octo/control",
    correlation_id: "corr-1",
    run_url: "https://github.com/octo/control/actions/runs/1",
    payload: { evidence_count: 3, confidence: "corroborated" },
    ...overrides,
  });
}

function memoryFixture(logs) {
  const directory = mkdtempSync(path.join(tmpdir(), "eslint-rules-db-"));
  mkdirSync(path.join(directory, "transactions"), { recursive: true });
  for (const [name, lines] of Object.entries(logs)) {
    writeFileSync(path.join(directory, "transactions", name), lines.length ? `${lines.join("\n")}\n` : "");
  }
  return directory;
}

const sampleLogs = {
  "orchestrator__octo__app__2025-01-01.jsonl": [
    transaction({
      txn_id: "txn-priority",
      recorded_at: "2024-12-31T23:00:00Z",
      worker: "orchestrator",
      kind: "repository-priority",
      rule_key: undefined,
      payload: { rank: 1, decision: "selected" },
    }),
  ],
  "miner__octo__app__2025-01-02.jsonl": [
    transaction({ txn_id: "txn-0002", recorded_at: "2025-01-02T09:00:00Z" }),
    transaction({ txn_id: "txn-0001", recorded_at: "2025-01-02T08:00:00Z" }),
  ],
  "inventory__octo__app__2025-01-01.jsonl": [
    transaction({
      txn_id: "txn-0000",
      recorded_at: "2025-01-01T00:00:00Z",
      worker: "inventory",
      kind: "lint-inventory",
      rule_key: undefined,
      payload: { eslint: "flat", package_manager: "npm" },
    }),
  ],
  "refiner__octo__app__2025-01-03.jsonl": [
    transaction({
      txn_id: "txn-0003",
      recorded_at: "2025-01-03T00:00:00Z",
      worker: "refiner",
      kind: "rule-outcome",
      payload: { classification: "false-positive", precision_blocked: true },
    }),
  ],
};

test("rules database orders transactions deterministically across logs", () => {
  const memory = memoryFixture(sampleLogs);
  try {
    const transactions = collectTransactions(memory);
    assert.deepEqual(
      transactions.map((entry) => entry.txnId),
      ["txn-priority", "txn-0000", "txn-0001", "txn-0002", "txn-0003"],
    );
    assert.equal(transactions[0].ruleKey, "");
  } finally {
    rmSync(memory, { recursive: true, force: true });
  }
});

test("campaign summaries are deterministic and preserve evidence counts", () => {
  const memory = memoryFixture(sampleLogs);
  try {
    const summary = summarizeMemory(memory);
    assert.deepEqual(summary, summarizeMemory(memory));
    assert.equal(summary.transactions, 5);
    assert.equal(summary.rules.length, 1);
    assert.equal(summary.rules[0].candidates, 2);
    assert.equal(summary.rules[0].outcomes, 1);
    assert.deepEqual(summary.rules[0].targets, ["octo/app"]);
    assert.equal(summary.inventory[0].payload, '{"eslint":"flat","package_manager":"npm"}');
    assert.equal(summary.priorities[0].payload, '{"decision":"selected","rank":1}');
  } finally {
    rmSync(memory, { recursive: true, force: true });
  }
});

const malformedCases = [
  ["invalid json", "{not json"],
  ["unknown schema", transaction({ schema: "cao.other" })],
  ["unsupported version", transaction({ schema_version: 2 })],
  ["unknown kind", transaction({ kind: "rule-deleted" })],
  ["non-object payload", transaction({ payload: [1, 2] })],
  ["unsafe rule key", transaction({ rule_key: "../escape" })],
  ["non-repository target", transaction({ target_repo: "app" })],
  ["loose timestamp", transaction({ recorded_at: "2025-01-02" })],
  ["missing rule key", transaction({ kind: "rule-outcome", rule_key: undefined })],
];

for (const [label, line] of malformedCases) {
  test(`rules database fails closed on ${label}`, () => {
    const memory = memoryFixture({ "miner__octo__app__2025-01-02.jsonl": [line] });
    try {
      assert.throws(() => summarizeMemory(memory), RulesDatabaseError);
    } finally {
      rmSync(memory, { recursive: true, force: true });
    }
  });
}

test("rules database rejects duplicate transaction identifiers", () => {
  const memory = memoryFixture({
    "miner__octo__app__2025-01-02.jsonl": [transaction(), transaction()],
  });
  try {
    assert.throws(
      () => collectTransactions(memory),
      (error) => error instanceof RulesDatabaseError && /duplicate txn_id txn-0001/.test(error.message),
    );
  } finally {
    rmSync(memory, { recursive: true, force: true });
  }
});

test("rules database accepts long valid log names and normalizes repository identity", () => {
  const owner = "o".repeat(39);
  const repository = "r".repeat(100);
  const target = `${owner}/${repository}`;
  const memory = memoryFixture({
    [`inventory__${owner}__${repository}.jsonl`]: [
      transaction({
        txn_id: "txn-long-repository",
        worker: "inventory",
        kind: "lint-inventory",
        rule_key: undefined,
        target_repo: target.toUpperCase(),
      }),
    ],
  });
  try {
    const [entry] = collectTransactions(memory);
    assert.equal(entry.targetRepo, target);
  } finally {
    rmSync(memory, { recursive: true, force: true });
  }
});

test("campaign summaries reject later malformed observations without changing source logs", () => {
  const memory = memoryFixture(sampleLogs);
  try {
    const input = path.join(memory, "transactions", "miner__octo__app__2025-01-04.jsonl");
    writeFileSync(input, "{broken\n");
    assert.throws(() => summarizeMemory(memory), RulesDatabaseError);
    assert.equal(readFileSync(input, "utf8"), "{broken\n");
  } finally {
    rmSync(memory, { recursive: true, force: true });
  }
});

test("campaign command line verifies and summarizes without a database", () => {
  const memory = memoryFixture(sampleLogs);
  try {
    const verified = JSON.parse(execFileSync("node", [scriptSource, "verify", "--memory", memory], { encoding: "utf8" }));
    assert.deepEqual(verified, { command: "verify", transactions: 5 });
    const built = JSON.parse(
      execFileSync("node", [scriptSource, "summary", "--memory", memory], { encoding: "utf8" }),
    );
    assert.equal(built.command, "summary");
    assert.equal(built.transactions, 5);
    assert.equal(/from "(?!node:)/.test(readFileSync(scriptSource, "utf8")), false);
  } finally {
    rmSync(memory, { recursive: true, force: true });
  }
});
