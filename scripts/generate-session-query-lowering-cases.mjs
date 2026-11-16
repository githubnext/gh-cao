// Generates server/internal/sessionarchive/testdata/lowering/cases.json: inputs
// and the outcomes the pinned upstream gh-aw graders produce for them. The Go
// test TestDuckDBLoweringEvidenceIntegration runs the reference SQL lowerings in
// testdata/lowering/*.sql on DuckDB and requires identical outcomes.
//
// Usage: node scripts/generate-session-query-lowering-cases.mjs [--source-dir DIR]
// Without --source-dir the pinned sources are fetched with `gh api`. Either way
// every source must match its blob and sha256 pin.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { verifyPinnedSource } from "./replay-session-archive-graders.mjs";

const root = fileURLToPath(new URL("../", import.meta.url));
const testdata = join(root, "server/internal/sessionarchive/testdata");
const pins = JSON.parse(readFileSync(join(testdata, "grader-source-pins.json"), "utf8"));
const needed = ["trajectory_graders.cjs", "builtin_graders.json", "grader_session.cjs"];

function loadSources(directory) {
  const sources = {};
  for (const name of needed) {
    const expected = pins.sources.find((source) => source.name === name);
    let content;
    if (directory) {
      content = readFileSync(join(directory, name));
      const blob = createHash("sha1").update(`blob ${content.length}\0`).update(content).digest("hex");
      assert.equal(blob, expected?.blob, `pinned upstream blob integrity: ${name}`);
      assert.equal(createHash("sha256").update(content).digest("hex"), expected.sha256, `pinned upstream sha256 integrity: ${name}`);
    } else {
      const metadata = JSON.parse(execFileSync("gh", [
        "api", `repos/${pins.upstreamRepository}/contents/actions/setup/js/${name}?ref=${pins.upstreamRevision}`,
      ], { cwd: root, encoding: "utf8", timeout: 30000 }));
      content = verifyPinnedSource(name, metadata, expected).content;
    }
    sources[name] = content;
  }
  return sources;
}

function regexLiteral(source, pattern, label) {
  const match = source.match(pattern);
  assert.ok(match, `upstream ${label} pattern not found at the pin`);
  return new RegExp(match[1], match[2]);
}

// Deterministic PRNG so regeneration is reproducible.
function mulberry32(seed) {
  return () => {
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const graderIds = ["recurrence-rate", "recurrence-determinism", "recurrence-laminarity", "recurrence-trapping-time"];

function outcome(result) {
  if (result.value !== null && result.value !== undefined) return { status: "measured", value: result.value };
  if (/^not applicable:/.test(result.message ?? "")) return { status: "not-applicable" };
  if (/^unavailable:/.test(result.message ?? "")) return { status: "unavailable" };
  throw new Error(`unclassified upstream result: ${JSON.stringify(result)}`);
}

function recurrenceCases(graders) {
  const random = mulberry32(21058);
  const cases = [
    { id: "empty", states: [] },
    { id: "one-visit", states: ["a"] },
    { id: "three-visits", states: ["a", "b", "a"] },
    { id: "four-distinct", states: ["a", "b", "c", "d"] },
    { id: "all-same", states: ["a", "a", "a", "a", "a"] },
    { id: "alternating", states: ["a", "b", "a", "b", "a", "b", "a"] },
    { id: "pairs", states: ["a", "a", "b", "b", "a", "a", "b", "b"] },
    { id: "runs", states: ["a", "a", "a", "b", "b", "b", "a", "a", "a", "c"] },
    { id: "unicode-ids", states: ["état", "日本", "état", "日本", "état", "x"] },
    { id: "dropped-elements", states: ["a", null, {}, { id: "" }, { id: 5 }, 42, "a", { id: "b" }, "b", "a"] },
    {
      id: "event-order",
      states: [{ id: "a", firstEventIndex: 30 }, { id: "b", firstEventIndex: 10 }, { id: "a", firstEventIndex: 20 }, { id: "b", firstEventIndex: 40 }, { id: "a" }],
      events: [{ index: 10 }, { index: 20 }, { index: 30 }, { index: 40 }],
    },
    {
      id: "order-ties-keep-position",
      states: [{ id: "b", firstEventIndex: 1 }, { id: "a", firstEventIndex: 1 }, "a", { id: "b", firstEventIndex: 0 }, { id: "a", firstEventIndex: 2 }],
      events: [{ index: 0 }, { index: 1 }, { index: 2 }],
    },
    {
      id: "duplicate-event-index-last-wins",
      // Last-wins orders b b a a a; first-wins would order a b b a a.
      states: [{ id: "a", firstEventIndex: 0 }, { id: "b", firstEventIndex: 1 }, { id: "b", firstEventIndex: 2 }, { id: "a", firstEventIndex: 3 }, { id: "a", firstEventIndex: 4 }],
      events: [{ index: 0 }, { index: 1 }, { index: 2 }, { index: 3 }, { index: 4 }, { index: 0 }, { note: "no index" }],
    },
    {
      id: "unmatched-and-negative-index",
      states: [{ id: "a", firstEventIndex: 100 }, { id: "b", firstEventIndex: -3 }, "a", { id: "b", firstEventIndex: 2 }, "a"],
      events: [{ index: 2 }],
    },
    // Declared divergence: upstream coerces with Number(), so null orders as 0
    // and "2" as 2. Strict S1 typing fails closed instead.
    { id: "null-order-index", states: [{ id: "a", firstEventIndex: null }, "b", "a", "b"], diverges: true },
    { id: "string-order-index", states: [{ id: "a", firstEventIndex: "2" }, "b", "a", "b"], events: [{ index: 2 }], diverges: true },
    { id: "string-event-index", states: [{ id: "a", firstEventIndex: 1 }, "b", "a", "b"], events: [{ index: "1" }], diverges: true },
  ];
  for (let k = 0; k < 40; k += 1) {
    const length = Math.floor(random() * 81);
    const alphabet = 1 + Math.floor(random() * 6);
    const states = Array.from({ length }, () => {
      const id = `s${Math.floor(random() * alphabet)}`;
      return random() < 0.2 ? { id } : id;
    });
    cases.push({ id: `random-${String(k).padStart(2, "0")}`, states });
  }
  const symbols = "abcdefgh";
  for (const [id, length] of [["cap-5000", 5000], ["over-cap-5001", 5001]]) {
    let sequence = "";
    let previous = "a";
    for (let i = 0; i < length; i += 1) {
      previous = random() < 0.4 ? previous : symbols[Math.floor(random() * symbols.length)];
      sequence += previous;
    }
    cases.push({ id, sequence });
  }
  return cases.map(({ diverges, ...input }) => {
    const states = input.states ?? [...input.sequence];
    const trace = { trajectoryIR: { states, ...(input.events ? { events: input.events } : {}) } };
    const upstream = Object.fromEntries(graderIds.map((id) => [id, outcome(graders[id](trace, {}))]));
    const expected = diverges ? Object.fromEntries(graderIds.map((id) => [id, { status: "unavailable" }])) : upstream;
    return { ...input, expected, ...(diverges ? { upstream } : {}) };
  });
}

function keywordCases(patterns) {
  const random = mulberry32(3);
  const words = ["retry", "Retrying", "RETRY", "retries", "retryingly", "re-try", "check", "CHECKS", "verify", "Verification",
    "policy", "approve", "approval", "guard", "guardrail", "confirm", "authorize", "authorization", "Authorization", "authorise"];
  const separators = ["", " ", "_", "-", ".", "\n", "\t", "1", "é", "ſ", "\u212a", "ı", "İ", "日", "ß", "'"];
  const texts = [
    "", "retry", "Retrying now", "will retry.", "retry_", "retry1", "éretry", "retryé", "pre-retry", "retrying\n",
    "ℝetry", "retryıng", "RETRYİNG", "check", "checK", "chec\u212a",
    // RE2 and Rust regex (?i) fold the Kelvin sign to k; a following ASCII word character exposes it.
    "chec\u212ax", "chec\u212a_policy", "chec\u212a1", "verifİcation", "ſecurity policy", "policy_check",
    "Policy-check passed", "approvals", "approve?", "日guard日", "authorization", "AUTHORISATION",
  ];
  for (let k = 0; k < 400; k += 1) {
    let text = "";
    const parts = 1 + Math.floor(random() * 4);
    for (let p = 0; p < parts; p += 1) {
      let word = words[Math.floor(random() * words.length)];
      if (random() < 0.3) {
        const at = Math.floor(random() * word.length);
        const swap = ["ſ", "\u212a", "ı", "İ", word[at].toUpperCase(), "é"][Math.floor(random() * 6)];
        word = word.slice(0, at) + swap + word.slice(at + 1);
      }
      text += separators[Math.floor(random() * separators.length)] + word;
    }
    texts.push(text + separators[Math.floor(random() * separators.length)]);
  }
  return [...new Set(texts)].map((text, index) => ({
    id: `k${String(index).padStart(3, "0")}`,
    text,
    retries: patterns.retries.test(text),
    guard: patterns.guard.test(text),
  }));
}

function render(fixture) {
  const lines = (items) => items.map((item) => `    ${JSON.stringify(item)}`).join(",\n");
  return `{
  "upstreamRepository": ${JSON.stringify(fixture.upstreamRepository)},
  "upstreamRevision": ${JSON.stringify(fixture.upstreamRevision)},
  "generator": "scripts/generate-session-query-lowering-cases.mjs",
  "upstreamPatterns": ${JSON.stringify(fixture.upstreamPatterns)},
  "recurrence": [
${lines(fixture.recurrence)}
  ],
  "keywords": [
${lines(fixture.keywords)}
  ]
}
`;
}

const flag = process.argv.indexOf("--source-dir");
const sources = loadSources(flag === -1 ? undefined : process.argv[flag + 1]);
mkdirSync(join(root, ".tmp"), { recursive: true });
const temporary = mkdtempSync(join(root, ".tmp", "lowering-cases-"));
try {
  for (const [name, content] of Object.entries(sources)) writeFileSync(join(temporary, name), content, { mode: 0o600 });
  const graders = createRequire(import.meta.url)(join(temporary, "trajectory_graders.cjs")).TRAJECTORY_GRADERS;
  const patterns = {
    retries: regexLiteral(String(sources["grader_session.cjs"]), /\/(\\bretry\(\?:ing\)\?\\b)\/([a-z]*)\.test\(/, "retry"),
    guard: regexLiteral(String(sources["trajectory_graders.cjs"]), /const guardPattern = \/(.+)\/([a-z]*);/, "guard"),
  };
  const fixture = {
    upstreamRepository: pins.upstreamRepository,
    upstreamRevision: pins.upstreamRevision,
    upstreamPatterns: Object.fromEntries(Object.entries(patterns).map(([key, value]) => [key, String(value)])),
    recurrence: recurrenceCases(graders),
    keywords: keywordCases(patterns),
  };
  mkdirSync(join(testdata, "lowering"), { recursive: true });
  writeFileSync(join(testdata, "lowering/cases.json"), render(fixture));
} finally {
  rmSync(temporary, { recursive: true, force: true });
}
