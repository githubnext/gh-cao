import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import test from "node:test";
import { gzipSync } from "node:zlib";
import { fileDietEvents, readFileDietProof } from "../../.github/skills/add-operational-value/examples/daily-file-diet.mjs";
import { eventPulse } from "../../.github/skills/add-operational-value/scripts/pulse-model.mjs";
import { preserveFileDietSnapshots } from "../../.github/skills/add-operational-value/examples/acquire-file-diet.mjs";
import { operationalValueEvidenceJsonl } from "../../.github/skills/add-operational-value/scripts/export-evidence.mjs";
import { collectValueEvidence, definition, seedFileDietProof } from "../../.github/value-collectors/daily-file-diet.mjs";
import { runWorkflowValues } from "../../activity/workflow-values.mjs";

const root = path.resolve(import.meta.dirname, "../..");
const digest = value => createHash("sha256").update(JSON.stringify(value)).digest("hex");
const sha = digit => digit.repeat(40);
const file = (name, lines, blob) => ({ path: name, lines, bytes: lines * 2, blob: sha(blob) });
const adopted = sha("1"), parent = sha("2"), merged = sha("3");

function example() {
  const inventories = new Map([
    [adopted, [file("pkg/a.go", 1129, "a")]],
    [parent, [file("pkg/a.go", 1129, "a"), file("pkg/unrelated.go", 900, "b")]],
    [merged, [file("pkg/a.go", 248, "c"), file("pkg/replacement.go", 850, "d")]],
  ]);
  const issue = { id: "I_1", number: 1, title: "[file-diet] Refactor pkg/a.go",
    targetPath: "pkg/a.go", createdAt: "2026-01-02T00:00:00Z",
    reference: { runId: "99" }, run: { validation: { headSha: adopted } },
    receipt: { status: "missing" },
    timeline: { complete: true, events: [{ type: "connected", at: "2026-01-04T00:00:00Z",
      target: { kind: "pull-request", number: 2 } }] } };
  const pull = { id: "PR_2", number: 2, createdAt: "2026-01-03T00:00:00Z",
    mergedAt: "2026-01-05T00:00:00Z", preMergeSha: parent, mergeSha: merged,
    baseSha: adopted,
    graph: { id: "PR_2", state: "MERGED", mergedAt: "2026-01-05T00:00:00Z",
      mergeCommit: merged, closingLinksComplete: true, edges: [] },
    files: [{ path: "pkg/replacement.go", status: "added" }] };
  const proof = {
    schemaVersion: 1, repository: "github/gh-aw", sourcePath: ".github/workflows/daily-file-diet.md",
    aliases: ["github/gh-aw", "githubnext/gh-aw"],
    adoption: { commit: adopted, baselineCommit: sha("0"), at: "2026-01-01T00:00:00Z" },
    until: "2026-05-01T00:00:00Z", observedAt: "2026-05-01T01:00:00Z",
    discovery: [{ complete: true }], issues: [issue], pulls: [pull],
    snapshots: [{ at: "2026-01-01T00:00:00Z", commit: adopted },
      { at: "2026-05-01T00:00:00Z", commit: merged }],
  };
  return { proof, inventories };
}

function receipt(input) {
  const issue = input.proof.issues[0];
  const content = JSON.stringify({ type: "create_issue", number: issue.number,
    repo: "github/gh-aw", url: `https://github.com/github/gh-aw/issues/${issue.number}`,
    metadata: { node_id: issue.id } });
  issue.receipt = { status: "available", runId: 99, headSha: adopted, content,
    entrySha256: createHash("sha256").update(content).digest("hex"),
    archiveSha256: "a".repeat(64), digest: `sha256:${"a".repeat(64)}` };
}

function preserve(input, directory) {
  const proof = structuredClone(input.proof);
  proof.inventoryPartitions = [...input.inventories].map(([commit, files]) => {
    const inventory = { commit, files, inventoryDigest: digest(files) };
    const filename = `inventory-${commit}.json.gz`;
    writeFileSync(path.join(directory, filename), gzipSync(JSON.stringify(inventory)));
    return { file: filename, commit, sha256: digest(inventory) };
  });
  proof.uniqueBlobs = new Set([...input.inventories.values()].flatMap(files => files.map(file => file.blob))).size;
  writeFileSync(path.join(directory, "proof.json.gz"), gzipSync(JSON.stringify(proof)));
  return proof;
}

function temporary(t) {
  mkdirSync(path.join(root, ".tmp"), { recursive: true });
  const directory = mkdtempSync(path.join(root, ".tmp", "file-diet-replay-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return directory;
}

test("fresh acquisition retains every accepted snapshot cutoff without changing its immutable revision", () => {
  const retained = { snapshots: [{ at: "2026-01-01T00:00:00Z", commit: adopted },
    { at: "2026-05-01T00:00:00Z", commit: parent }] };
  const refreshed = { snapshots: [{ at: "2026-01-01T00:00:00Z", commit: adopted },
    { at: "2026-05-02T00:00:00Z", commit: merged }] };
  preserveFileDietSnapshots(refreshed, retained);
  assert.deepEqual(refreshed.snapshots, [...retained.snapshots, { at: "2026-05-02T00:00:00Z", commit: merged }]);
  assert.throws(() => preserveFileDietSnapshots({ snapshots: [
    { at: "2026-05-01T00:00:00Z", commit: merged },
  ] }, retained), /snapshot revision changed/);
});

test("the real reviewed File Diet collector loads in the isolated worker and reuses satisfied cadence", async t => {
  const directory = temporary(t);
  const ledger = JSON.parse(readFileSync(path.join(root, "tests/fixtures/operational-value/file-diet-events.json"), "utf8"));
  const output = path.join(directory, "evidence.jsonl");
  const policy = JSON.parse(readFileSync(path.join(root, ".github/workflows/cao.json"), "utf8"));
  const registration = policy["control-plane"].campaigns.activity["workflow-values"]
    .find((entry) => entry.repository === definition.repository && entry.workflow === definition.slug);
  delete registration.evidence;
  const policyPath = path.join(directory, "cao.json");
  writeFileSync(policyPath, JSON.stringify(policy));
  writeFileSync(output, operationalValueEvidenceJsonl(ledger));
  const result = await runWorkflowValues({
    root, policy: policyPath,
    repositories: [definition.repository], output, workspace: path.join(directory, "proofs"),
    observedAt: ledger.window.endAt,
    apiBudget: path.join(directory, "api-budget.json"), apiReserve: 100,
    apiExecute: () => ({ status: 0, stdout: JSON.stringify({ resources: { core: {
      limit: 5000, remaining: 4000, used: 1000, reset: 1790000000,
    } } }), stderr: "" }),
  });
  assert.deepEqual(result, { imported: 0, collected: 0, skipped: 1, deferred: 0, failed: 0, warnings: [] });
});

test("cold-start proof is pinned, evidence-only, bounded and validated before reuse", async t => {
  const directory = temporary(t);
  const source = path.join(directory, "source");
  mkdirSync(source);
  preserve(example(), source);
  const prefix = "docs/operational-value/proof/file-diet-lifetime";
  const tree = readdirSync(source).map(name =>
    `100644 blob ${sha("a")}\t${prefix}/${name}`).join("\n");
  const commands = [];
  const git = async (...args) => {
    commands.push(args);
    if (args.includes("ls-tree")) return { stdout: tree };
    if (args.includes("checkout")) cpSync(source, path.join(args[1], prefix), { recursive: true });
    return { stdout: "" };
  };
  const result = await seedFileDietProof(path.join(directory, "seed"), git);
  assert.equal(readFileDietProof(result).proof.until, example().proof.until);
  assert.ok(commands.some(args => args.includes("https://github.com/githubnext/gh-cao.git")));
  assert.ok(commands.filter(args => args.includes("fetch") || args.includes("checkout"))
    .every(args => args.includes("0782148c0af21f4f1d10b7cb5e81b571ed58c463")));
  assert.ok(commands.some(args => args.includes("sparse-checkout") && args.includes(`/${prefix}/`)));
  for (const invalid of [tree.replace("100644", "120000"), tree.replace("proof.json.gz", "script.mjs"),
    Array(1025).fill(tree.split("\n")[0]).join("\n")]) {
    let checkedOut = false;
    await assert.rejects(seedFileDietProof(path.join(directory, "invalid"), async (...args) => {
      if (args.includes("checkout")) checkedOut = true;
      return { stdout: args.includes("ls-tree") ? invalid : "" };
    }), /Historical proof/);
    assert.equal(checkedOut, false);
  }
  await assert.rejects(seedFileDietProof(path.join(directory, "oversized"), async (...args) => {
    if (args.includes("checkout")) {
      mkdirSync(path.join(args[1], prefix), { recursive: true });
      writeFileSync(path.join(args[1], prefix, "proof.json.gz"), Buffer.alloc(16 * 1024 * 1024 + 1));
    }
    return { stdout: args.includes("ls-tree") ? `100644 blob ${sha("a")}\t${prefix}/proof.json.gz` : "" };
  }), /byte budget/);
});

test("recurring File Diet collection reuses originals and only promotes validated cumulative proof", async t => {
  const directory = temporary(t);
  const input = example();
  input.proof.adoption = { commit: definition.adoption.commit, at: definition.adoption.adoptedAt,
    baselineCommit: sha("0") };
  input.inventories.set(definition.adoption.commit, input.inventories.get(adopted));
  input.proof.snapshots[0] = { at: definition.adoption.adoptedAt, commit: definition.adoption.commit };
  let seeds = 0;
  let seeded;
  const seed = directory => {
    seeds++;
    seeded = directory;
    mkdirSync(directory);
    preserve(input, directory);
    return directory;
  };
  let calls = 0;
  const acquire = ({ endAt, output, previous }) => {
    assert.equal(previous, calls ? path.join(directory, "latest", "proof") : seeded);
    assert.equal(readFileDietProof(previous).proof.until, input.proof.until);
    calls++;
    if (calls > 1) input.proof.snapshots.push({ at: endAt, commit: merged });
    input.proof.until = endAt;
    input.proof.observedAt = new Date(Date.parse(endAt) + 1000).toISOString();
    mkdirSync(path.join(output, "proof"), { recursive: true });
    preserve(input, path.join(output, "proof"));
    writeFileSync(path.join(output, "daily-file-diet-pulse-evidence.jsonl"),
      operationalValueEvidenceJsonl(fileDietEvents(readFileDietProof(path.join(output, "proof")))));
  };
  const request = { repository: definition.repository, workflow: definition.slug,
    observedAt: "2026-05-01T00:00:00.000Z", workspace: { directory } };
  const first = await collectValueEvidence(request, acquire, seed);
  const second = await collectValueEvidence({ ...request, observedAt: "2026-05-02T00:00:00.000Z",
    previous: first.operational_value_evidence }, acquire);
  assert.equal(second.operational_value_evidence.contractDigest, first.operational_value_evidence.contractDigest);
  assert.equal(seeds, 1, "Retained proof must replace the one-time historical seed.");
  const originals = first.operational_value_evidence.facts.filter(fact => fact["fact-kind"] === "snapshot");
  assert.ok(originals.every(fact => second.operational_value_evidence.facts.some(next =>
    JSON.stringify(next) === JSON.stringify(fact))));
  const accepted = readFileSync(path.join(directory, "latest", "daily-file-diet-pulse-evidence.jsonl"), "utf8");
  await assert.rejects(collectValueEvidence(request, () => { throw new Error("API budget exhausted"); }),
    /API budget exhausted/);
  await assert.rejects(collectValueEvidence({ ...request, observedAt: "2026-05-03T00:00:00.000Z",
    previous: second.operational_value_evidence }, options => {
    acquire(options);
    const filename = path.join(options.output, "daily-file-diet-pulse-evidence.jsonl");
    const envelope = JSON.parse(readFileSync(filename, "utf8"));
    envelope.operational_value_evidence.facts.find(fact => fact["fact-kind"] === "coverage")["coverage-status"] = "partial";
    writeFileSync(filename, JSON.stringify(envelope));
  }), /Incomplete acquisition/);
  assert.equal(readFileSync(path.join(directory, "latest", "daily-file-diet-pulse-evidence.jsonl"), "utf8"), accepted);
  await assert.rejects(collectValueEvidence({ ...request, repository: "org/other" }, acquire));
});

test("File Diet measures actual merge-parent inventories and replacement files, not output volume", () => {
  const input = example();
  const ledger = fileDietEvents(input);
  assert.equal(ledger.events.length, 1);
  assert.equal(ledger.outputs.items.length, 1);
  assert.deepEqual(ledger.outputs.items[0].implementationIds, ["PR_2"]);
  assert.equal(ledger.outputs.items[0].origin, "corroborated");
  assert.equal(ledger.outputs.items[0].state, "unknown", "Missing lifecycle history must not invent an open state.");
  assert.match(ledger.intent.sources[0].url, new RegExp(`/blob/${adopted}/`));
  assert.ok(ledger.intent.requirements.some(requirement => requirement.includes("public API")));
  assert.equal(ledger.resultLabel, "file reductions");
  assert.equal(ledger.events[0].value, 1);
  assert.equal(ledger.events[0].at, input.proof.pulls[0].mergedAt);
  assert.equal(ledger.events[0].acceptance.ref, merged);
  assert.match(ledger.events[0].evidence.find(ref => ref.label === "Before revision").url, new RegExp(`${parent}$`));
  assert.equal(eventPulse(ledger).metrics[0].receiptTotal, 0);
  assert.match(ledger.intent.acceptance, /full behavior-preserving acceptance remains unknown/);
  assert.deepEqual(ledger.repositoryState.observations.map(row => row.value), [1, 1]);
  assert.equal(ledger.repositoryState.observations.at(-1).ref, merged);
  assert.equal(ledger.checks.find(check => check.name === "Behavior preserved").status, "not-verified");
  assert.equal(ledger.checks.find(check => check.name === "Structural change measured").status, "verified");
  delete input.proof.pulls[0].preMergeSha;
  assert.throws(() => fileDietEvents(input), /Missing immutable inventory/);
});

test("snapshot counts expose the exact oversized files and accepted changes link directly to measured files", () => {
  const input = example();
  const ledger = fileDietEvents(input);
  for (const row of ledger.repositoryState.observations) {
    const files = input.inventories.get(row.ref).filter(file => file.lines >= 800);
    assert.equal(row.items.length, row.value);
    assert.deepEqual(row.items, files.map(file => ({
      label: `${file.path} (${file.lines} lines)`,
      url: `https://github.com/github/gh-aw/blob/${row.ref}/${file.path}#L800`,
    })));
  }
  const sources = ledger.events[0].evidence;
  assert.deepEqual(sources.filter(ref => ref.url.includes("/blob/")), [
    { label: "Before: pkg/a.go (1129 lines)", url: `https://github.com/github/gh-aw/blob/${parent}/pkg/a.go#L800` },
    { label: "After: pkg/a.go (248 lines)", url: `https://github.com/github/gh-aw/blob/${merged}/pkg/a.go#L1` },
    { label: "Added/renamed: pkg/replacement.go (850 lines)", url: `https://github.com/github/gh-aw/blob/${merged}/pkg/replacement.go#L800` },
  ]);
  assert.equal(sources.find(ref => ref.label === "Accepted change diff").url,
    `https://github.com/github/gh-aw/compare/${parent}...${merged}`);
  input.inventories.set(merged, input.inventories.get(merged).filter(file => file.path !== "pkg/a.go"));
  const removed = fileDietEvents(input);
  assert.ok(!removed.events[0].evidence.some(ref => ref.label.startsWith("After: pkg/a.go")),
    "A removed file must not acquire an invented after-file link.");
});

test("file permalinks encode real Git paths and reject traversal instead of linking to a different item", () => {
  const input = example();
  for (const files of input.inventories.values()) {
    for (const file of files) if (file.path === "pkg/a.go") file.path = "pkg/a #.go";
  }
  input.proof.issues[0].targetPath = "pkg/a #.go";
  const ledger = fileDietEvents(input);
  assert.equal(ledger.repositoryState.observations[0].items[0].url,
    `https://github.com/github/gh-aw/blob/${adopted}/pkg/a%20%23.go#L800`);
  input.inventories.get(adopted)[0].path = "pkg/../a.go";
  assert.throws(() => fileDietEvents(input), /Invalid immutable file reference/);
});

test("tracked auxiliary outputs and receipts for another object are not credited as deliveries", () => {
  for (const change of [
    issue => { issue.eligible = false; },
    issue => { issue.receipt = { status: "not-applied-to-output", reason: "Artifact created another issue" }; },
  ]) {
    const input = example();
    change(input.proof.issues[0]);
    const ledger = fileDietEvents(input);
    assert.equal(ledger.events.length, 0);
    assert.equal(ledger.checks.find(check => check.name === "Structural change measured").status, "not-verified");
    assert.ok(ledger.limitations.some(note => /auxiliary outputs/.test(note)));
    assert.equal(ledger.outputs.items.length, 0);
  }
});
test("unmerged proposals, later connections and disconnected links do not become deliveries", () => {
  for (const change of [
    input => { input.proof.pulls[0].mergedAt = null; },
    input => { input.proof.issues[0].timeline.events[0].at = "2026-01-06T00:00:00Z"; },
    input => { input.proof.issues[0].timeline.events.push({
      ...input.proof.issues[0].timeline.events[0], type: "disconnected", at: "2026-01-04T12:00:00Z",
    }); },
  ]) {
    const input = example();
    change(input);
    assert.equal(fileDietEvents(input).events.length, 0);
  }
});

test("retained undated closing declarations are explicit corroboration, not invented dated proof", () => {
  const input = example();
  input.proof.issues[0].timeline.events = [];
  input.proof.pulls[0].graph.edges = [{ relation: "declares-closing", target: { id: "I_1" } }];
  const ledger = fileDietEvents(input);
  assert.equal(ledger.events[0].attribution, "corroborated");
  assert.ok(ledger.limitations.some(note => /1 retained closing links lack a dated connection or closed-by event/.test(note)));
});

test("closed outputs are not accepted unless they link to a measured implementation", () => {
  const input = example();
  const first = input.proof.issues[0];
  first.graph = { historyComplete: true, events: [
    { type: "closed", at: "2026-01-06T00:00:00Z" },
    { type: "reopened", at: "2026-05-02T00:00:00Z" },
  ] };
  input.proof.issues.push({ ...structuredClone(first), id: "I_3", number: 3,
    title: "Another proposal", timeline: { complete: true, events: [] } });
  const model = eventPulse(fileDietEvents(input));
  assert.deepEqual(model.outputs.items.map(item => item.state), ["closed", "closed"]);
  assert.deepEqual([model.outputs.retainedCount, model.outputs.implementedOutputCount,
    model.outputs.implementationCount], [2, 1, 1]);
  assert.equal(model.outputs.items[1].implementationIds.length, 0);
  input.proof.issues[0].graph.events.push({ type: "reopened", at: "2026-02-01T00:00:00Z" });
  assert.equal(fileDietEvents(input).outputs.items[0].state, "open");
});

test("keyword-linked issues retain dated closer evidence even without connected events", () => {
  const input = example();
  const issue = input.proof.issues[0];
  issue.timeline.events = [];
  issue.graph = { edges: [
    { relation: "referenced-by", at: "2026-01-04T00:00:00Z", target: { id: "PR_2" } },
    { relation: "closed-by", at: "2026-01-05T00:00:01Z", target: { id: "PR_2" } },
  ] };
  input.proof.pulls[0].graph.edges = [{ relation: "declares-closing", target: { id: "I_1" } }];
  const ledger = fileDietEvents(input);
  assert.equal(ledger.events.length, 1);
  assert.equal(ledger.events[0].at, "2026-01-05T00:00:00Z");
  assert.deepEqual(ledger.events[0].lineage.map(edge => [edge.relation, edge.at]), [
    ["referenced-by", "2026-01-04T00:00:00Z"], ["closed-by", "2026-01-05T00:00:01Z"],
  ]);
  assert.ok(!ledger.limitations.some(note => note.includes("lack a dated")));
  assert.equal(eventPulse(ledger).metrics[0].observedTotal, 1);
  for (const change of [
    edge => { edge.at = "2026-05-02T00:00:00Z"; },
    edge => { edge.target.id = "another-PR"; },
  ]) {
    const altered = structuredClone(input);
    change(altered.proof.issues[0].graph.edges[1]);
    assert.ok(fileDietEvents(altered).limitations.some(note => note.includes("lack a dated")));
  }
});

test("missing discovery or relationship coverage cannot produce a complete-period zero", () => {
  for (const change of [
    input => { input.proof.discovery = []; },
    input => { input.proof.discovery[0].complete = false; },
    input => { input.proof.issues[0].timeline.complete = false; },
    input => { input.proof.pulls[0].graph.closingLinksComplete = false; },
  ]) {
    const input = example();
    change(input);
    const model = eventPulse(fileDietEvents(input));
    assert.equal(model.metrics[0].observedTotal, 1);
    assert.equal(model.metrics[0].completeTotal, null);
    assert.equal(model.signal.status, "insufficient-evidence");
  }
});

test("creation receipts strengthen attribution without changing measured value or delivery time", () => {
  const input = example();
  const before = fileDietEvents(input);
  receipt(input);
  const after = fileDietEvents(input);
  assert.equal(after.events[0].value, before.events[0].value);
  assert.equal(after.events[0].at, before.events[0].at);
  assert.equal(eventPulse(after).metrics[0].receiptTotal, 1);
  assert.equal(eventPulse(after).outputs.receiptCount, 1);
  input.proof.issues[0].receipt.runId = 100;
  assert.throws(() => fileDietEvents(input), /99/);
});

test("merged identities and acceptance revisions must agree", () => {
  for (const key of ["id", "state", "mergeCommit", "mergedAt"]) {
    const input = example();
    input.proof.pulls[0].graph[key] = "not-the-accepted-value";
    assert.throws(() => fileDietEvents(input), /mismatch|not merged/);
  }
});

test("preserved File Diet replay is self-contained and fails closed on altered input", t => {
  const directory = temporary(t);
  const input = example();
  receipt(input);
  const proof = preserve(input, directory);
  const restored = readFileDietProof(directory);
  assert.equal(fileDietEvents(restored).events[0].value, 1);
  const report = path.join(directory, "output", "pulse.html");
  execFileSync(process.execPath, [
    path.join(root, ".github/skills/add-operational-value/examples/daily-file-diet.mjs"), directory, report,
  ], { env: { ...process.env, PATH: "", GH_TOKEN: "", GITHUB_TOKEN: "" } });
  assert.match(readFileSync(report, "utf8"), /<!doctype html>/);
  const pulse = JSON.parse(readFileSync(report.replace(".html", ".json"), "utf8"));
  assert.equal(pulse.metrics[0].observedTotal, 1);
  assert.equal(pulse.metrics[0].receiptTotal, 1);
  assert.ok(JSON.parse(readFileSync(report.replace(".html", "-events.json"), "utf8")).intent);
  proof.inventoryPartitions[0].sha256 = "0".repeat(64);
  writeFileSync(path.join(directory, "proof.json.gz"), gzipSync(JSON.stringify(proof)));
  assert.throws(() => readFileDietProof(directory), /partition digest mismatch/);
});

test("inventory paths, native blob facts and repository identities cannot be substituted", t => {
  const directory = temporary(t);
  for (const change of [
    input => { input.proof.repository = "somewhere/else"; },
    input => { input.inventories.get(parent)[0].path = "pkg/a_test.go"; },
    input => { input.inventories.get(parent)[0].lines = 900; },
  ]) {
    const input = example();
    change(input);
    preserve(input, directory);
    assert.throws(() => readFileDietProof(directory), /AssertionError|Conflicting|Invalid/);
  }
});
