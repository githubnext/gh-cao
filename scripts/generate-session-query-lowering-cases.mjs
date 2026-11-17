// Generates server/internal/sessionarchive/testdata/lowering/cases.json: inputs
// and the outcomes the pinned upstream gh-aw graders produce for them. The Go
// test TestDuckDBLoweringEvidenceIntegration runs the reference SQL lowerings in
// testdata/lowering/*.sql on DuckDB and requires identical outcomes, except for
// declared divergences, which it requires to differ.
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
const needed = pins.sources.map((source) => source.name);

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

const toolGraderIds = ["tool-failure-count", "tool-success-rate", "trajectory-efficiency"];

// Unified session events. Every event is agent-phase unless provenance says otherwise.
const agent = (type, data, provenance = {}) => ({ type, data, provenance: { phase: "agent", component: "agent", ...provenance } });
const start = (toolName, extra = {}, provenance) => agent("tool.execution_start", { toolName, ...extra }, provenance);
const complete = (toolName, extra = {}, provenance) => agent("tool.execution_complete", { toolName, ...extra }, provenance);
const gateway = (type, data, provenance) => agent(type, { serverName: "github", ...data }, provenance);
const attempt = (attemptNumber, attemptId, strategy, path = "agent") =>
  agent("harness.attempt", { layer: "harness", attempt: attemptNumber, attemptId, ...(strategy ? { strategy } : {}) }, { component: "harness", path });

// Hand cases cover each reconciliation rule. `diverges` names the sequential
// FIFO-pointer behaviour the declarative plan in reconciliation.sql cannot
// express; the evidence test requires those cases to differ from upstream.
function reconciliationHandCases() {
  const ok = { success: true };
  const s1 = { sessionId: "s1" };
  return [
    { id: "empty", events: [] },
    { id: "id-pairs-out-of-order", events: [start("read", { toolCallId: "1" }), start("edit", { toolCallId: "2" }), complete("edit", { toolCallId: "2", ...ok }), complete("read", { toolCallId: "1", success: false })] },
    { id: "id-duplicate-start-latest-wins", events: [start("read", { toolCallId: "1" }), start("read", { toolCallId: "1" }), complete("read", { toolCallId: "1", ...ok })] },
    { id: "id-remerge-last-writer", events: [start("read", { toolCallId: "1" }), complete("read", { toolCallId: "1", isError: true }), complete("read", { toolCallId: "1", isError: false })] },
    { id: "id-orphan-remerge", events: [complete("read", { toolCallId: "9", status: "error" }), complete("read", { toolCallId: "9", status: "ok" })] },
    { id: "id-orphan-then-start", events: [complete("read", { toolCallId: "9", ...ok }), start("read", { toolCallId: "9" }), complete("read", { toolCallId: "9", ...ok })] },
    { id: "fifo-by-name", events: [start("read"), start("read"), complete("read", ok), complete("read", { isError: true })] },
    { id: "fifo-interleaved-names", events: [start("read"), start("edit"), start("read"), complete("edit", ok), complete("read", ok), complete("read", { error: "boom" })] },
    { id: "fifo-completion-before-start", events: [complete("read", ok), start("read")] },
    { id: "fifo-pointer-overshoot", events: [start("read"), complete("read", ok), complete("read", ok), start("read"), complete("read", ok)] },
    { id: "fifo-unmatched-id-takes-unnamed-start", events: [start("read"), complete("read", { toolCallId: "7", ...ok })] },
    { id: "fifo-null-error-overrides", events: [start("read"), complete("read", { error: "x" }), complete("read", { toolCallId: "z", error: null })] },
    { id: "incomplete-start", events: [start("read"), start("edit", { toolCallId: "1" })] },
    { id: "scopes-session-agent", events: [start("read", s1), start("read", { sessionId: "s2" }), start("read", { ...s1, agentId: "sub" }), complete("read", { sessionId: "s2", ...ok }), complete("read", { ...s1, agentId: "sub", ...ok })] },
    { id: "scopes-attempt", events: [start("read", {}, { attemptId: "a1" }), complete("read", ok, { attemptId: "a2" }), complete("read", ok, { attemptId: "a1" })] },
    { id: "non-agent-phase-ignored", events: [start("read", {}, { phase: "detection" }), complete("read", { success: false }, { phase: "detection" }), start("edit"), complete("edit", ok)] },
    { id: "status-failure", events: [start("read", { toolCallId: "1" }), complete("read", { toolCallId: "1", status: "failure" })] },
    { id: "gateway-tool-call", events: [gateway("mcp.tool_call", { toolName: "search", toolCallId: "g1", ...ok }), gateway("mcp.tool_call", { toolName: "search", toolCallId: "g2", isError: true })] },
    { id: "gateway-rpc-request-response", events: [gateway("mcp.rpc.request", { method: "tools/call", toolName: "search", rpcId: 3 }), gateway("mcp.rpc.response", { method: "tools/call", rpcId: 3, error: "denied" })] },
    { id: "gateway-request-only-incomplete", events: [gateway("mcp.rpc.request", { method: "tools/call", toolName: "search", rpcId: 4 })] },
    { id: "gateway-unnamed-before-named-skipped", events: [gateway("mcp.rpc.response", { method: "tools/call", rpcId: 5, ...ok }), gateway("mcp.rpc.request", { method: "tools/call", toolName: "search", rpcId: 5 })] },
    { id: "gateway-other-methods-ignored", events: [gateway("mcp.rpc.request", { method: "tools/list", rpcId: 6, toolName: "x" }), gateway("mcp.rpc.response", { method: "initialize", requestId: "r", toolName: "y" })] },
    { id: "gateway-no-id-per-event", events: [gateway("mcp.tool_call", { toolName: "search", ...ok }), gateway("mcp.tool_call", { toolName: "search", ...ok })] },
    { id: "gateway-request-id-string", events: [gateway("mcp.rpc.request", { method: "tools/call", toolName: "fetch", requestId: "r1" }), gateway("mcp.rpc.response", { requestId: "r1", ...ok })] },
    { id: "gateway-dedup-native", events: [start("search", { toolCallId: "7" }), complete("search", { toolCallId: "7", ...ok }), gateway("mcp.tool_call", { toolName: "search", toolCallId: "7", ...ok }), gateway("mcp.rpc.response", { method: "tools/call", toolName: "search", rpcId: 7, isError: true })] },
    { id: "gateway-dedup-respects-attempt", events: [start("search", { toolCallId: "7" }, { attemptId: "a1" }), complete("search", { toolCallId: "7", ...ok }, { attemptId: "a1" }), gateway("mcp.tool_call", { toolName: "search", toolCallId: "7", ...ok }, { attemptId: "a2" })] },
    {
      id: "resume-collapses-attempt-scopes",
      events: [
        attempt(1, "a1"), attempt(2, "a2", "resume"), attempt(3, "a3", "fresh"), attempt(4, "a4", "resume"),
        start("read", s1, { attemptId: "a1" }), complete("read", { ...s1, ...ok }, { attemptId: "a2" }),
        start("edit", s1, { attemptId: "a3" }), complete("edit", { ...s1, ...ok }, { attemptId: "a2" }), complete("edit", { ...s1, ...ok }, { attemptId: "a4" }),
      ],
    },
    {
      id: "resume-leading-resume-is-own-root",
      events: [attempt(1, "b1", "resume"), attempt(2, "b2", "resume"), start("read", s1, { attemptId: "b1" }), complete("read", { ...s1, ...ok }, { attemptId: "b2" })],
    },
    {
      id: "resume-requires-session-id",
      events: [attempt(1, "c1"), attempt(2, "c2", "resume"), start("read", {}, { attemptId: "c1" }), complete("read", ok, { attemptId: "c2" })],
    },
    {
      id: "resume-sorted-by-attempt-per-invocation",
      events: [
        attempt(2, "d2", "resume"), attempt(1, "d1"), attempt(1, "e1", undefined, "other"), attempt(2, "e2", "resume", "other"),
        start("read", s1, { attemptId: "d2" }), complete("read", { ...s1, ...ok }, { attemptId: "d1" }),
        start("edit", s1, { attemptId: "e1" }), complete("edit", { ...s1, ...ok }, { attemptId: "d1" }),
      ],
    },
    {
      id: "resume-duplicate-attempt-event",
      events: [attempt(1, "h1"), attempt(2, "h2", "resume"), attempt(3, "h1", "resume"), attempt(4, "h3", "resume"),
        start("read", s1, { attemptId: "h3" }), complete("read", { ...s1, ...ok }, { attemptId: "h1" }), complete("read", { ...s1, ...ok }, { attemptId: "h2" })],
    },
    {
      id: "resume-dedups-gateway",
      events: [attempt(1, "f1"), attempt(2, "f2", "resume"), start("search", { ...s1, toolCallId: "8" }, { attemptId: "f1" }),
        complete("search", { ...s1, toolCallId: "8", ...ok }, { attemptId: "f1" }), gateway("mcp.tool_call", { ...s1, toolName: "search", toolCallId: "8", ...ok }, { attemptId: "f2" })],
    },
    // The pointer skips calls already completed by ID and ID-mismatched calls,
    // then never revisits them: the pairing depends on sequential pointer state.
    { id: "fifo-skips-id-completed", diverges: "fifo-pointer-skip", events: [start("read", { toolCallId: "1" }), start("read"), complete("read", { toolCallId: "1", ...ok }), complete("read", ok)] },
    { id: "fifo-skips-mismatched-id", diverges: "fifo-pointer-skip", events: [start("read", { toolCallId: "1" }), start("read"), complete("read", { toolCallId: "9", ...ok }), complete("read", { toolCallId: "1", ...ok })] },
    { id: "fifo-skip-strands-start", diverges: "fifo-pointer-skip", events: [start("read", { toolCallId: "1" }), start("read"), start("read"), complete("read", { toolCallId: "9", ...ok }), complete("read", ok), complete("read", { toolCallId: "1", ...ok })] },
  ];
}

// Random traces stay within the declarative plan: per name, completions either
// carry an earlier start's ID or no ID at all, so the FIFO pointer never skips.
function reconciliationRandomCases() {
  const random = mulberry32(214);
  const pick = (items) => items[Math.floor(random() * items.length)];
  const outcomes = [{ success: true }, { success: false }, { isError: true }, { isError: false }, { status: "error" }, { status: "ok" }, { error: "x" }, {}];
  const cases = [];
  for (let k = 0; k < 60; k += 1) {
    const events = [];
    const scopes = [{}, { sessionId: "s1" }, { sessionId: "s1", agentId: "sub" }].slice(0, 1 + Math.floor(random() * 3));
    const names = ["read", "edit", "bash", "search"].slice(0, 1 + Math.floor(random() * 4));
    const idNames = new Set(names.filter(() => random() < 0.5));
    const started = [];
    let ids = 0;
    for (let n = Math.floor(random() * 30); n > 0; n -= 1) {
      const scope = pick(scopes);
      const name = pick(names);
      const roll = random();
      if (roll < 0.45) {
        const toolCallId = idNames.has(name) ? `t${ids++}` : undefined;
        started.push({ scope, name, toolCallId });
        events.push(start(name, { ...scope, ...(toolCallId ? { toolCallId } : {}) }));
      } else if (roll < 0.85) {
        const candidates = started.filter((call) => call.name === name && call.scope === scope);
        const target = idNames.has(name) ? pick(candidates.length ? candidates : [{}]) : {};
        if (idNames.has(name) && !target.toolCallId) {
          if (candidates.length === 0 && random() < 0.5) events.push(complete(name, { ...scope, toolCallId: `o${ids++}`, ...pick(outcomes) }));
          continue;
        }
        events.push(complete(name, { ...scope, ...(target.toolCallId ? { toolCallId: target.toolCallId } : {}), ...pick(outcomes) }));
      } else {
        const id = random() < 0.3 && started.some((call) => call.toolCallId) ? pick(started.filter((call) => call.toolCallId)).toolCallId : `g${ids++}`;
        const type = pick(["mcp.tool_call", "mcp.rpc.request", "mcp.rpc.response"]);
        const named = random() < 0.7 ? { toolName: pick(names) } : {};
        const rpc = type === "mcp.tool_call" ? { toolCallId: id } : { method: "tools/call", rpcId: id };
        events.push(gateway(type, { ...named, ...rpc, ...(type === "mcp.rpc.request" ? {} : pick(outcomes)) }));
      }
    }
    cases.push({ id: `random-${String(k).padStart(2, "0")}`, events });
  }
  return cases;
}

function reconciliationCases(builtin) {
  return [...reconciliationHandCases(), ...reconciliationRandomCases()].map(({ diverges, ...input }) => {
    const graded = Object.fromEntries(toolGraderIds.map((id) => [id, builtin[id](input.events, {})]));
    for (const value of Object.values(graded)) assert.ok(Number.isFinite(value), `${input.id}: upstream tool grader is not finite`);
    return { ...input, ...(diverges ? { diverges } : {}), expected: graded };
  });
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
  ],
  "reconciliation": [
${lines(fixture.reconciliation)}
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
  const load = createRequire(import.meta.url);
  const graders = load(join(temporary, "trajectory_graders.cjs")).TRAJECTORY_GRADERS;
  const builtin = load(join(temporary, "trace_graders.cjs")).BUILTIN_GRADERS;
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
    reconciliation: reconciliationCases(builtin),
  };
  mkdirSync(join(testdata, "lowering"), { recursive: true });
  writeFileSync(join(testdata, "lowering/cases.json"), render(fixture));
} finally {
  rmSync(temporary, { recursive: true, force: true });
}
