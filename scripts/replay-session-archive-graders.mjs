import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../", import.meta.url));
const revision = "5ac122295e5e839aa15a727ecf4762f3e680cc24";

export function verifyPinnedSource(name, metadata, expected) {
  assert.ok(expected, `missing upstream source pin: ${name}`);
  assert.equal(metadata.encoding, "base64");
  const content = Buffer.from(metadata.content, "base64");
  const blob = createHash("sha1").update(`blob ${content.length}\0`).update(content).digest("hex");
  const sha256 = createHash("sha256").update(content).digest("hex");
  assert.equal(blob, expected.blob, `pinned upstream blob integrity: ${name}`);
  assert.equal(sha256, expected.sha256, `pinned upstream sha256 integrity: ${name}`);
  assert.equal(metadata.sha, expected.blob, `upstream metadata blob integrity: ${name}`);
  return { content, blob, sha256 };
}

function replaySessionArchiveGraders() {
  const output = resolve(process.argv[2] ?? join(root, ".tmp/session-archive-grader-replay"));
  const manifest = JSON.parse(readFileSync(join(root, "server/internal/sessionarchive/testdata/evidence.json"), "utf8"));
  const pins = JSON.parse(readFileSync(join(root, "server/internal/sessionarchive/testdata/grader-source-pins.json"), "utf8"));
  assert.equal(manifest.upstreamRevision, revision);
  assert.equal(pins.upstreamRepository, manifest.upstreamRepository);
  assert.equal(pins.upstreamRevision, revision);
  const ids = manifest.candidates;
  const temporaryRoot = join(root, ".tmp");
  mkdirSync(temporaryRoot, { recursive: true });
  mkdirSync(output, { recursive: true, mode: 0o700 });
  const temporary = mkdtempSync(join(temporaryRoot, "grader-replay-"));
  const upstream = join(temporary, "upstream");
  mkdirSync(upstream, { mode: 0o700 });

  try {
    const sources = [];
    for (const { name, ...expected } of pins.sources) {
      const metadata = JSON.parse(execFileSync("gh", [
        "api", `repos/github/gh-aw/contents/actions/setup/js/${name}?ref=${revision}`,
      ], { cwd: root, encoding: "utf8", timeout: 30000 }));
      const { content, blob, sha256 } = verifyPinnedSource(name, metadata, expected);
      writeFileSync(join(upstream, name), content, { mode: 0o600 });
      sources.push({ name, blob, sha256 });
    }
    const command = ["-C", "server", "test", "./internal/sessionarchive", "-run", "^TestGraderReplayEvidence$", "-count=1", "-v"];
    const log = execFileSync("go", command, {
      cwd: root, encoding: "utf8", timeout: 180000,
      env: { ...process.env, CAO_SESSION_ARCHIVE_REQUIRE_DUCKDB: "1", CAO_SESSION_ARCHIVE_GRADER_REPLAY_DIR: temporary },
    });
    const cases = JSON.parse(readFileSync(join(temporary, "evidence.json"), "utf8"));
    writeFileSync(join(output, "evidence.json"), JSON.stringify(cases) + "\n", { mode: 0o600 });
    const require = createRequire(import.meta.url);
    const graders = require(join(upstream, "trace_graders.cjs"));
    const supported = new Set(["execution-step-count", "execution-duration", "working-set-rebuild-factor", "context-growth",
      "objective-coverage", "premature-termination-gap", "grounding-accuracy", "state-revisit-probability-rep",
      "recurrence-rate", "artifact-production", "dependency-order-violation-rate", "evidence-saturation-stopping-lag",
      "recurrence-laminarity", "recurrence-trapping-time"]);
    const recurrenceGraders = new Set(["recurrence-laminarity", "recurrence-trapping-time"]);
    const itemGraders = new Set(["objective-coverage", "premature-termination-gap", "grounding-accuracy", "state-revisit-probability-rep",
      "recurrence-rate"]);
    const usageGraders = new Set(["execution-step-count", "execution-duration", "working-set-rebuild-factor", "context-growth"]);
    // Item sources read the last agent-phase trajectory.ir, matching grader_session.cjs IR selection.
    function lastIR(evidence) {
      const irs = evidence.filter((event) => event.type === "trajectory.ir" && event.provenance?.phase === "agent");
      return irs.length ? irs.at(-1).data ?? {} : {};
    }
    function itemReason(id, evidence, omissions) {
      const ir = lastIR(evidence);
      const key = { "grounding-accuracy": "actions", "state-revisit-probability-rep": "states", "recurrence-rate": "states" }[id] ?? "objectives";
      if (omissions.includes(`data.${key}:excluded`) || omissions.includes("data:excluded")) return "excluded";
      if (key === "states" && omissions.includes("data.states:redacted")) return "redacted";
      const items = ir[key];
      if (Array.isArray(items) && items.length > 5000) return "oversized";
      if (key === "states") {
        const visits = (Array.isArray(items) ? items : []).filter((state) => typeof state === "string" ||
          state && typeof state === "object" && !Array.isArray(state) && typeof state.id === "string" && state.id !== "");
        return visits.length < 2 ? "fewer-than-two-visits" : "measured";
      }
      const record = (item) => item !== null && typeof item === "object" && !Array.isArray(item);
      if (!Array.isArray(items) || items.length === 0) return key === "actions" ? "no-actions" : "no-objectives";
      if (key === "actions") return items.every((action) => record(action) && typeof action.validAtIssueTime === "boolean") ? "measured" : "invalid-validity";
      if (!items.every(record)) return "malformed-objective";
      return items.some(({ satisfiedAtEventIndex: index }) => index !== null && index !== undefined && (!Number.isSafeInteger(index) || index < 0))
        ? "invalid-satisfaction-index" : "measured";
    }
    const record = (item) => item !== null && typeof item === "object" && !Array.isArray(item);
    const excluded = (omissions, key) => omissions.includes(`data.${key}:excluded`) || omissions.includes("data:excluded");
    const index = (value) => Number.isSafeInteger(value) && value >= 0;
    // Phase 5 reason ladders, in the order each reviewed definition evaluates them.
    function dependencyReason(ir, omissions) {
      const items = ir.objectives;
      if (excluded(omissions, "objectives")) return "excluded";
      if (Array.isArray(items) && items.length > 5000) return "oversized";
      if (!Array.isArray(items) || items.length === 0) return "no-objectives";
      const ids = items.map((item) => record(item) && typeof item.id === "string" ? item.id : "");
      if (ids.includes("") || new Set(ids).size !== ids.length) return "invalid-objective-ids";
      if (items.some(({ dependsOn }) => dependsOn !== undefined && dependsOn !== null && !Array.isArray(dependsOn))) return "invalid-dependencies";
      const dependents = items.filter(({ dependsOn }) => Array.isArray(dependsOn) && dependsOn.length > 0);
      if (dependents.length === 0) return "no-dependencies";
      if (dependents.flatMap(({ dependsOn }) => dependsOn).some((id) => typeof id !== "string" || !ids.includes(id))) return "unknown-dependency";
      return dependents.some(({ satisfiedAtEventIndex }) => index(satisfiedAtEventIndex)) ? "measured" : "no-completed-dependents";
    }
    function saturationReason(ir, omissions) {
      const { objectives: items, events } = ir;
      if (excluded(omissions, "objectives") || excluded(omissions, "events")) return "excluded";
      if (Array.isArray(items) && items.length > 5000 || Array.isArray(events) && events.length > 5000) return "oversized";
      if (!Array.isArray(items) || items.length === 0) return "no-objectives";
      // Upstream synthesizes events from agent events; the definition fails closed.
      if (!Array.isArray(events)) return "no-events";
      if (!items.every(record)) return "malformed-objective";
      const satisfied = items.map(({ satisfiedAtEventIndex }) => satisfiedAtEventIndex);
      if (satisfied.some((value) => value === null || value === undefined)) return "not-saturated";
      if (!satisfied.every(index)) return "invalid-satisfaction-index";
      if (!events.every((event) => record(event) && index(event.index))) return "invalid-event-index";
      const indexes = new Set(events.map((event) => event.index));
      return satisfied.every((value) => indexes.has(value)) ? "measured" : "satisfaction-event-missing";
    }
    // Visit order is exact only when every IR event index is its position and
    // every present firstEventIndex is a safe integer; otherwise the
    // definitions fail closed as "unordered". String "" states are visits
    // upstream but not archive-states visits, so they also fail closed.
    function recurrenceReason(ir) {
      const states = Array.isArray(ir.states) ? ir.states : [];
      const { events } = ir;
      const ordered = Array.isArray(events) && events.every((event, position) => record(event) && event.index === position) &&
        !states.includes("") && states.every((state) => !record(state) || !("firstEventIndex" in state) || Number.isSafeInteger(state.firstEventIndex));
      if (!ordered) return "unordered";
      if (states.length > 5000 || events.length > 5000) return "oversized";
      const visits = states.filter((state) => typeof state === "string" || record(state) && typeof state.id === "string" && state.id !== "");
      return visits.length < 4 ? "fewer-than-four-visits" : "measured";
    }
    function grade(events) {
      let lookups;
      try {
        lookups = graders.createSessionLookups(events);
      } catch (error) {
        return { stage: "lookup", error: error instanceof Error ? error.message : String(error), results: null };
      }
      return {
        stage: "graded",
        results: ids.map((id) => graders.runBuiltinGrader(id, events, graders.BUILTIN_META[id], lookups)),
      };
    }
    const replays = cases.map((scenario) => {
      assert.equal(scenario.schemaVersion, 3);
      assert.deepEqual(scenario.original, scenario.reconstructed, `${scenario.name}: original approved input preservation`);
      const original = grade(structuredClone(scenario.original));
      const reconstructed = grade(structuredClone(scenario.reconstructed));
      assert.deepEqual(original, reconstructed, `${scenario.name}: pinned grader replay`);
      assert.deepEqual(scenario.queries.map(({ id }) => id), ids);
      const companion = grade(structuredClone(scenario.companion));
      for (const query of scenario.queries) {
        if (!supported.has(query.id)) {
          assert.ok(query.fullError, `${query.id}: unsupported full query must report actual rejection`);
          continue;
        }
        assert.equal(query.fullError, "");
        assert.equal(query.fullResult.rows.length, 2);
        for (const [runId, evidence, upstreamResult] of [["123", scenario.original, original], ["456", scenario.companion, companion]]) {
          if (upstreamResult.stage === "lookup") {
            assert.equal(scenario.name, "original-preservation-fixture");
            // This intentionally malformed fixture proves preservation, not full metric equivalence.
            continue;
          }
          const expected = upstreamResult.results.find(({ id }) => id === query.id);
          const row = query.fullResult.rows.find((row) => row.RunId === runId);
          assert.ok(row, `${scenario.name}: exact trace/run-attempt result`);
          const omissions = runId === "123" ? scenario.omissions ?? [] : [];
          const phase5 = {
            "dependency-order-violation-rate": dependencyReason, "evidence-saturation-stopping-lag": saturationReason,
          }[query.id] ?? (recurrenceGraders.has(query.id) ? recurrenceReason : undefined);
          const phase5Reason = phase5?.(lastIR(evidence), omissions);
          // Declared fail-closed divergences: upstream may measure, the definition reports unavailable.
          if (phase5Reason === "unordered" || phase5Reason === "no-events") {
            assert.equal(row.value, null, `${scenario.name}/${runId}/${query.id}: fail-closed divergence`);
            assert.equal(row.availability, "unavailable");
          } else {
            assert.equal(row.value, expected.value, `${scenario.name}/${runId}/${query.id}: full upstream metric`);
            assert.equal(row.availability, expected.status === "unavailable" ? "unavailable" : "measured");
          }
          const lookups = graders.createSessionLookups(evidence);
          const selected = lookups.trace.tokenUsageEntries;
          let reason = itemGraders.has(query.id) ? itemReason(query.id, evidence, omissions) : phase5Reason;
          switch (query.id) {
            case "execution-step-count": reason = selected.length ? "measured" : "no-requests"; break;
            case "execution-duration": {
              const durations = selected.map(({ duration_ms }) => duration_ms);
              const sum = durations.reduce((a, b) => a + (b ?? 0), 0);
              const usable = durations.length > 0 && durations.every((n) => n !== null) && Number.isFinite(sum) && sum <= Number.MAX_SAFE_INTEGER;
              reason = usable ? "measured" : expected.value === null ? "duration-unavailable" : "execution-result-fallback";
              break;
            }
            case "working-set-rebuild-factor":
              reason = !selected.length ? "no-requests" : selected.some(({ input_tokens }) => input_tokens === null)
                ? "incomplete-input" : expected.value === null ? "zero-peak" : "measured";
              break;
            case "context-growth":
              reason = expected.value === null ? "incomplete-unsafe-or-zero-usage" : "measured";
              break;
            case "artifact-production": {
              reason = omissions.includes("data.status:excluded") ? "excluded" : omissions.includes("data.status:redacted") ? "redacted" : "measured";
              break;
            }
          }
          assert.equal(row.reason, reason, `${scenario.name}/${runId}/${query.id}: qualified lookup reason`);
          if (usageGraders.has(query.id)) {
            const proxy = evidence.some((event) => event.provenance.phase === "agent" && event.type === "firewall.token_usage");
            assert.equal(row.family, proxy ? "firewall.token_usage" : "usage.report");
          }
        }
      }
      return {
        name: scenario.name, schemaVersion: scenario.schemaVersion, events: scenario.original.length,
        approvedInputRoundTripEqual: true, pinnedReplayEqual: true,
        omissions: scenario.omissions, original, reconstructed, queries: scenario.queries,
      };
    });
    let kustoReplay = "unavailable: no approved real Kusto endpoint";
    if (process.env.CAO_KUSTO_EMULATOR_URL || process.env.CAO_SESSION_ARCHIVE_REQUIRE_KUSTO === "1") {
      const kustoLog = execFileSync("go", ["-C", "server", "test", "./internal/sessionarchiveblob",
        "-run", "^TestKustoUsageGraderReplay$", "-count=1", "-v"], {
        cwd: root, encoding: "utf8", timeout: 180000,
        env: { ...process.env, CAO_SESSION_ARCHIVE_REQUIRE_KUSTO: "1", CAO_SESSION_ARCHIVE_GRADER_REPLAY_DIR: temporary },
      });
      writeFileSync(join(output, "kusto-verification.log"), kustoLog, { mode: 0o600 });
      kustoReplay = "matched: same definitions and synthetic approved events through real Kusto; not live external-table acceptance";
    }
    const measured = replays.find(({ name }) => name === "measured");
    assert.equal(measured.original.stage, "graded");
    const matrix = ids.map((id) => {
      const result = measured.original.results.find((result) => result.id === id);
      const query = measured.queries.find((query) => query.id === id);
      return {
        id, approvedInputRoundTripEqual: true, pinnedReplayEqual: true,
        measuredValue: result.value, measuredStatus: result.status,
        fullPublicQuerySupported: supported.has(id), fullQueryError: query.fullError, blocker: query.blocker,
        results: Object.fromEntries(replays.map((scenario) => [
          scenario.name, scenario.original.results?.find((result) => result.id === id) ?? {
            stage: scenario.original.stage, error: scenario.original.error,
          },
        ])),
      };
    });
    const report = {
      upstreamRepository: "github/gh-aw", upstreamRevision: revision, sources, kustoReplay,
      verification: ["CAO_SESSION_ARCHIVE_REQUIRE_DUCKDB=1 go -C server test ./internal/sessionarchive -run '^TestGraderReplayEvidence$' -count=1",
        "node scripts/replay-session-archive-graders.mjs [private-report-directory]"],
      qualification: "Replay compares original approved events, not excluded or pre-redaction content. Fourteen full declarative metrics execute through real DuckDB, nine of them over trajectory.ir item sources, two (recurrence-rate, artifact-production) through two-stage query chaining and four (dependency-order-violation-rate, evidence-saturation-stopping-lag, recurrence-laminarity, recurrence-trapping-time) through trace-scoped joins and nested item sources, with the same definitions replayed through real Kusto when configured; the other six remain unsupported. Recurrence definitions fail closed as unordered, and saturation as no-events, where upstream may measure synthesized or non-positional events, strings or invalid firstEventIndex values, or empty string states. Item-source reasons are derived from the last agent-phase trajectory.ir and approved omissions. The intentionally malformed original-preservation fixture retains its upstream lookup error and cannot establish metric equivalence; its explicitly separate valid-envelope scenario is compared. Upstream emits unavailable status without reason codes; query reasons are separately checked against pinned lookup state. No missing provenance is filled. Live Azure acceptance is not exercised.",
      matrix, scenarios: replays,
    };
    writeFileSync(join(output, "report.json"), JSON.stringify(report, null, 2) + "\n", { mode: 0o600 });
    writeFileSync(join(output, "verification.log"), log, { mode: 0o600 });
    const text = [
      "# Parquet v2 pinned grader replay", "",
      `Upstream: github/gh-aw ${revision}. ${ids.length} candidates, ${replays.length} scenarios; every approved-input round trip and pinned replay matched.`, "",
      "| Grader | Measured value | Approved round trip | Pinned replay | Full public JSON metric |",
      "| --- | ---: | --- | --- | --- |",
      ...matrix.map((row) => `| ${row.id} | ${row.measuredValue ?? row.measuredStatus} | equal | equal | ${row.fullPublicQuerySupported ? "matched" : "unsupported"} |`),
      "",
      "## Full-query attempt blockers", "",
      ...matrix.filter((row) => !row.fullPublicQuerySupported).map((row) => `- ${row.id}: \`${row.fullQueryError}\`. ${row.blocker}`),
      "",
      "## Scenario results", "",
      ...replays.map((scenario) => scenario.original.stage === "lookup"
        ? `${scenario.name}: lookup error preserved: ${scenario.original.error}`
        : `${scenario.name}: ${scenario.original.results.map(({ id, value, status }) => `${id}=${value ?? status}`).join("; ")}`),
      "", report.qualification, "", kustoReplay,
    ].join("\n") + "\n";
    writeFileSync(join(output, "report.md"), text, { mode: 0o600 });
    process.stdout.write(text);
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  replaySessionArchiveGraders();
}
