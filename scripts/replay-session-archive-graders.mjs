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
      let metadata;
      try {
        metadata = JSON.parse(execFileSync("gh", [
          "api", `repos/github/gh-aw/contents/actions/setup/js/${name}?ref=${revision}`,
        ], { cwd: root, encoding: "utf8", timeout: 30000, stdio: ["ignore", "pipe", "ignore"] }));
      } catch {
        // The immutable raw endpoint is usable when the API proxy is unavailable.
        // Integrity failures below never retry through another endpoint.
        const content = execFileSync("curl", ["--fail", "--silent", "--show-error", "--location", "--proto", "=https",
          `https://raw.githubusercontent.com/github/gh-aw/${revision}/actions/setup/js/${name}`],
        { cwd: root, timeout: 30000 });
        metadata = { encoding: "base64", content: content.toString("base64"), sha: expected.blob };
      }
      const { content, blob, sha256 } = verifyPinnedSource(name, metadata, expected);
      writeFileSync(join(upstream, name), content, { mode: 0o600 });
      sources.push({ name, blob, sha256 });
    }
    const command = ["-C", "server", "test", "./internal/sessionarchive", "-run", "^TestGraderReplayEvidence$", "-count=1", "-v"];
    const log = execFileSync("go", command, {
      cwd: root, encoding: "utf8", timeout: 300000,
      env: { ...process.env, CAO_SESSION_ARCHIVE_REQUIRE_DUCKDB: "1", CAO_SESSION_ARCHIVE_GRADER_REPLAY_DIR: temporary },
    });
    const cases = JSON.parse(readFileSync(join(temporary, "evidence.json"), "utf8"));
    writeFileSync(join(output, "evidence.json"), JSON.stringify(cases) + "\n", { mode: 0o600 });
    const require = createRequire(import.meta.url);
    const graders = require(join(upstream, "trace_graders.cjs"));
    const supported = new Set(["execution-step-count", "execution-duration", "working-set-rebuild-factor", "context-growth",
      "objective-coverage", "premature-termination-gap", "grounding-accuracy", "state-revisit-probability-rep",
      "recurrence-rate", "artifact-production", "dependency-order-violation-rate", "evidence-saturation-stopping-lag",
      "recurrence-laminarity", "recurrence-trapping-time", "recurrence-determinism",
      "exploration-error", "exploitation-error", "tool-output-consumption-rate",
      "code-search-recall", "code-read-precision", "code-edit-precision", "policy-near-miss"]);
    const recurrenceGraders = new Set(["recurrence-laminarity", "recurrence-trapping-time", "recurrence-determinism"]);
    const itemGraders = new Set(["objective-coverage", "premature-termination-gap", "grounding-accuracy", "state-revisit-probability-rep",
      "recurrence-rate"]);
    const usageGraders = new Set(["execution-step-count", "execution-duration", "working-set-rebuild-factor", "context-growth"]);
    const e1Graders = new Set(["exploration-error", "exploitation-error", "tool-output-consumption-rate"]);
    const codeGraders = new Set(["code-search-recall", "code-read-precision", "code-edit-precision"]);
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
    function e1Qualification(id, evidence, omissions) {
      const ir = lastIR(evidence);
      const keys = ["events", "states", "actions", "toolCalls", "observations", "resources", "provenanceEdges", "objectives", "reference"];
      const missing = keys.some((key) => excluded(omissions, key)) || omissions.includes("data.reference:redacted");
      const oversized = keys.some((key) => Array.isArray(ir[key]) && ir[key].length > 5000) ||
        record(ir.reference) && (Object.values(ir.reference).some((v) => Array.isArray(v) && v.length > 5000) ||
          Array.isArray(ir.reference.patch?.files) && ir.reference.patch.files.length > 5000);
      if (missing || oversized) return { blocked: true, reason: missing ? "incomplete-evidence" : "oversized" };
      const observations = (Array.isArray(ir.observations) ? ir.observations : []).filter(record);
      if (id === "tool-output-consumption-rate") {
        if (["observations", "toolCalls"].some((key) => omissions.includes(`data.${key}:redacted`))) return { blocked: true, reason: "redacted" };
        if (!observations.length) return { blocked: false, reason: "no-observations" };
        if (ir.toolCalls === undefined || ir.toolCalls === null) return { blocked: true, reason: "tool-calls-fallback" };
        const ids = new Set((Array.isArray(ir.toolCalls) ? ir.toolCalls : []).filter(record)
          .map(({ id }) => id).filter((id) => typeof id === "string" && id !== ""));
        return { blocked: false, reason: observations.some(({ sourceToolCallId }) => ids.has(sourceToolCallId)) ? "measured" : "no-tool-observations" };
      }
      const objectives = (Array.isArray(ir.objectives) ? ir.objectives : []).filter(record);
      const unmet = objectives.some(({ satisfiedAtEventIndex: index }) => index === undefined || index === null);
      const unsupportedRef = unmet && (Array.isArray(ir.events) ? ir.events : []).filter(record)
        .some((event) => event.kind === "state_change" && typeof event.ref !== "string");
      return { blocked: omissions.includes("data.events:redacted") || unsupportedRef };
    }
    // Phase 7 reason ladders. Upstream returns at the first failing action; every
    // failure is unavailable, so the definition's aggregate order changes only the reason.
    const normalizePath = (value) => value.trim().replace(/^\.\//, "").replace(/^\/+/, "");
    function codeQualification(id, evidence, omissions) {
      const ir = lastIR(evidence);
      const limits = e1Qualification("exploration-error", evidence, omissions);
      if (limits.blocked && (limits.reason === "incomplete-evidence" || limits.reason === "oversized")) return limits;
      const files = record(ir.reference) && record(ir.reference.patch) ? ir.reference.patch.files : undefined;
      if (!Array.isArray(files) || files.length === 0) return { blocked: false, reason: "no-reference-files" };
      if (files.some((file) => normalizePath(typeof file === "string" ? file : record(file) && typeof file.path === "string" ? file.path : "") === "")) {
        return { blocked: false, reason: "missing-reference-path" };
      }
      const key = id === "code-search-recall" ? "resources" : "actions";
      if (omissions.includes(`data.${key}:redacted`)) return { blocked: true, reason: "redacted" };
      const items = ir[key];
      if (!Array.isArray(items) || !items.every(record)) return { blocked: false, reason: `malformed-${key}` };
      if (key === "resources") return { blocked: false, reason: "measured" };
      if (items.some(({ type }) => typeof type !== "string" || type.trim() === "")) return { blocked: false, reason: "missing-type" };
      if (items.some(({ consequential }) => typeof consequential !== "boolean")) return { blocked: false, reason: "missing-consequential" };
      const edit = id === "code-edit-precision";
      if (edit && items.some(({ type, consequential }) => type === "edit" && !consequential)) return { blocked: false, reason: "non-consequential-edit" };
      const selected = items.filter(({ type, consequential }) => edit ? type === "edit" : !consequential && (type === "read" || type === "inspect"));
      if (selected.some(({ target }) => typeof target !== "string" || normalizePath(target) === "")) return { blocked: false, reason: "missing-target" };
      return { blocked: false, reason: selected.length ? "measured" : edit ? "no-edits" : "no-reads" };
    }
    // G1: the upstream guard keywords; archive-ir-events or, when the IR has no
    // events key, synthesized agent-phase agent-component event types.
    const guardPattern = /\b(check|verify|verification|policy|approval|approve|guard|confirm|authorize|authorization)\b/i;
    function policyNearMissReason(evidence, omissions) {
      const limits = e1Qualification("exploration-error", evidence, omissions);
      if (limits.blocked && (limits.reason === "incomplete-evidence" || limits.reason === "oversized")) return limits.reason;
      const ir = lastIR(evidence);
      const objectives = Array.isArray(ir.objectives) ? ir.objectives : [];
      if (!objectives.length) return "no-objectives";
      if (["objectives", "events"].some((key) => omissions.includes(`data.${key}:redacted`))) return "redacted";
      const events = "events" in ir ? (Array.isArray(ir.events) ? ir.events : [])
        : evidence.filter(({ provenance }) => provenance?.phase === "agent" && provenance?.component === "agent").map(({ type }) => ({ kind: type }));
      if (!events.some((event) => record(event) && event.kind === "safe_output")) return "no-safe-output";
      return objectives.some((objective) => record(objective) && typeof objective.description === "string" && guardPattern.test(objective.description))
        ? "measured" : "no-guard-objectives";
    }
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
          if (scenario.name === "e1-oversized-resources" && runId === "123" && !e1Graders.has(query.id) && !codeGraders.has(query.id) &&
            query.id !== "policy-near-miss") {
            // §4.2 already records that legacy definitions lack the sibling
            // collection guard. This regression gates E1, not a legacy retrofit.
            continue;
          }
          const omissions = runId === "123" ? scenario.omissions ?? [] : [];
          const phase5 = {
            "dependency-order-violation-rate": dependencyReason, "evidence-saturation-stopping-lag": saturationReason,
          }[query.id] ?? (recurrenceGraders.has(query.id) ? recurrenceReason : undefined);
          const phase5Reason = phase5?.(lastIR(evidence), omissions);
          const e1 = e1Graders.has(query.id) ? e1Qualification(query.id, evidence, omissions) : undefined;
          const codeState = codeGraders.has(query.id) ? codeQualification(query.id, evidence, omissions) : undefined;
          const itemState = itemGraders.has(query.id) ? itemReason(query.id, evidence, omissions) : undefined;
          const pnmReason = query.id === "policy-near-miss" ? policyNearMissReason(evidence, omissions) : undefined;
          // Declared fail-closed divergences: upstream may measure, the definition reports unavailable.
          if (phase5Reason === "unordered" || phase5Reason === "no-events" || e1?.blocked || codeState?.blocked ||
            itemState === "redacted" || itemState === "excluded" || pnmReason === "redacted" || pnmReason === "incomplete-evidence") {
            assert.equal(row.value, null, `${scenario.name}/${runId}/${query.id}: fail-closed divergence`);
            assert.equal(row.availability, "unavailable");
          } else {
            assert.equal(row.value, expected.value, `${scenario.name}/${runId}/${query.id}: full upstream metric`);
            assert.equal(row.availability, expected.status === "unavailable" ? "unavailable" : "measured");
          }
          const lookups = graders.createSessionLookups(evidence);
          const selected = lookups.trace.tokenUsageEntries;
          let reason = itemState ?? phase5Reason ?? pnmReason;
          if (e1) reason = query.id === "tool-output-consumption-rate" ? e1.reason : row.availability;
          if (codeState) reason = codeState.reason;
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
        "-run", "^TestKustoUsageGraderReplay$", "-count=1", "-timeout=10m", "-v"], {
        // Every definition runs once per scenario against the emulator; keep within the job's 25 minutes.
        cwd: root, encoding: "utf8", timeout: 660000, maxBuffer: 64 * 1024 * 1024,
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
      qualification: "Replay compares original approved events, not excluded or pre-redaction content. Twenty-two full declarative metrics execute through real DuckDB; four attempted candidates remain unsupported. E1 adds exploration-error, exploitation-error and tool-output-consumption-rate through trace-scoped joins and approved observation/tool-call sources, with nested consumer strings. Phase 7 adds code-search-recall, code-read-precision and code-edit-precision through JavaScript-trim and closed literal prefix normalization over typed reference patch file, resource and action sources; resource or action redaction fails closed. G1 adds policy-near-miss through the closed keyword-match guard set over the user-approved objective description; the safe_output check reads IR events or, when the key is absent, retained agent-phase agent-component event types, and objectives or events redaction fails closed. E1 fails closed on excluded guard inputs, relevant redaction, non-string state_change refs (upstream uses JavaScript JSON.stringify) and missing/null IR toolCalls requiring reconciliation. Harmless redaction preserves record counts, satisfaction nullness and consumer array lengths. The same definitions replay through real Kusto only when configured; fixtures alone do not prove hosted acceptance. Recurrence definitions fail closed as unordered, and saturation as no-events, where upstream may measure synthesized or non-positional events, strings or invalid firstEventIndex values, or empty string states. Item-source reasons are derived from the last agent-phase trajectory.ir and approved omissions. The intentionally malformed original-preservation fixture retains its upstream lookup error and cannot establish metric equivalence; its separate valid-envelope scenario is compared. Upstream emits unavailable status without reason codes; query reasons are checked separately. No missing provenance is filled. Live Azure acceptance is not exercised.",
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
