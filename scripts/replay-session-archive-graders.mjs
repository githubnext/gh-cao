import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../", import.meta.url));
const revision = "5ac122295e5e839aa15a727ecf4762f3e680cc24";
const output = resolve(process.argv[2] ?? join(root, ".tmp/session-archive-grader-replay"));
const manifest = JSON.parse(readFileSync(join(root, "server/internal/sessionarchive/testdata/evidence.json"), "utf8"));
assert.equal(manifest.upstreamRevision, revision);
const ids = manifest.candidates;
const temporaryRoot = join(root, ".tmp");
mkdirSync(temporaryRoot, { recursive: true });
mkdirSync(output, { recursive: true, mode: 0o700 });
const temporary = mkdtempSync(join(temporaryRoot, "grader-replay-"));
const upstream = join(temporary, "upstream");
mkdirSync(upstream, { mode: 0o700 });

try {
  const sources = [];
  for (const name of [
    "trace_graders.cjs", "trajectory_graders.cjs", "grader_session.cjs",
    "working_set_metrics.cjs", "builtin_graders.json", "error_codes.cjs",
  ]) {
    const metadata = JSON.parse(execFileSync("gh", [
      "api", `repos/github/gh-aw/contents/actions/setup/js/${name}?ref=${revision}`,
    ], { cwd: root, encoding: "utf8", timeout: 30000 }));
    assert.equal(metadata.encoding, "base64");
    const content = Buffer.from(metadata.content, "base64");
    const blob = createHash("sha1").update(`blob ${content.length}\0`).update(content).digest("hex");
    assert.equal(blob, metadata.sha, `pinned upstream blob integrity: ${name}`);
    writeFileSync(join(upstream, name), content, { mode: 0o600 });
    sources.push({ name, blob, sha256: createHash("sha256").update(content).digest("hex") });
  }
  const command = ["-C", "server", "test", "./internal/sessionarchive", "-run", "^TestGraderReplayEvidence$", "-count=1", "-v"];
  const log = execFileSync("go", command, {
    cwd: root, encoding: "utf8", timeout: 180000,
    env: { ...process.env, CAO_SESSION_ARCHIVE_REQUIRE_DUCKDB: "1", CAO_SESSION_ARCHIVE_GRADER_REPLAY_DIR: temporary },
  });
  const cases = JSON.parse(readFileSync(join(temporary, "evidence.json"), "utf8"));
  const require = createRequire(import.meta.url);
  const graders = require(join(upstream, "trace_graders.cjs"));
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
    assert.equal(scenario.schemaVersion, 2);
    assert.deepEqual(scenario.original, scenario.reconstructed, `${scenario.name}: original approved input preservation`);
    const original = grade(structuredClone(scenario.original));
    const reconstructed = grade(structuredClone(scenario.reconstructed));
    assert.deepEqual(original, reconstructed, `${scenario.name}: pinned grader replay`);
    assert.deepEqual(scenario.queries.map(({ id }) => id), ids);
    assert.ok(scenario.queries.every(({ fullError }) => fullError), "full metric probes must report their actual rejection");
    return {
      name: scenario.name, schemaVersion: scenario.schemaVersion, events: scenario.original.length,
      approvedInputRoundTripEqual: true, pinnedReplayEqual: true,
      omissions: scenario.omissions, original, reconstructed, queries: scenario.queries,
    };
  });
  const measured = replays.find(({ name }) => name === "measured");
  assert.equal(measured.original.stage, "graded");
  const matrix = ids.map((id) => {
    const result = measured.original.results.find((result) => result.id === id);
    const query = measured.queries.find((query) => query.id === id);
    return {
      id, approvedInputRoundTripEqual: true, pinnedReplayEqual: true,
      measuredValue: result.value, measuredStatus: result.status,
      fullPublicQuerySupported: false, fullQueryError: query.fullError, blocker: query.blocker,
      results: Object.fromEntries(replays.map((scenario) => [
        scenario.name, scenario.original.results?.find((result) => result.id === id) ?? {
          stage: scenario.original.stage, error: scenario.original.error,
        },
      ])),
    };
  });
  const report = {
    upstreamRepository: "github/gh-aw", upstreamRevision: revision, sources,
    verification: ["CAO_SESSION_ARCHIVE_REQUIRE_DUCKDB=1 go -C server test ./internal/sessionarchive -run '^TestGraderReplayEvidence$' -count=1",
      "node scripts/replay-session-archive-graders.mjs [private-report-directory]"],
    qualification: "Replay compares original approved events, not excluded or pre-redaction content. JSON component queries do not implement full grader formulas. No missing provenance is filled. Real Kusto/Azure acceptance is not exercised.",
    matrix, scenarios: replays,
  };
  writeFileSync(join(output, "report.json"), JSON.stringify(report, null, 2) + "\n", { mode: 0o600 });
  writeFileSync(join(output, "verification.log"), log, { mode: 0o600 });
  const text = [
    "# Parquet v2 pinned grader replay", "",
    `Upstream: github/gh-aw ${revision}. ${ids.length} candidates, ${replays.length} scenarios; every approved-input round trip and pinned replay matched.`, "",
    "| Grader | Measured value | Approved round trip | Pinned replay | Full public JSON metric |",
    "| --- | ---: | --- | --- | --- |",
    ...matrix.map((row) => `| ${row.id} | ${row.measuredValue ?? row.measuredStatus} | equal | equal | unsupported |`),
    "",
    "## Full-query attempt blockers", "",
    ...matrix.map((row) => `- ${row.id}: \`${row.fullQueryError}\`. ${row.blocker}`),
    "",
    "## Scenario results", "",
    ...replays.map((scenario) => scenario.original.stage === "lookup"
      ? `${scenario.name}: lookup error preserved: ${scenario.original.error}`
      : `${scenario.name}: ${scenario.original.results.map(({ id, value, status }) => `${id}=${value ?? status}`).join("; ")}`),
    "", report.qualification,
  ].join("\n") + "\n";
  writeFileSync(join(output, "report.md"), text, { mode: 0o600 });
  process.stdout.write(text);
} finally {
  rmSync(temporary, { recursive: true, force: true });
}
