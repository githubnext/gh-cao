import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { createHash, randomUUID } from "node:crypto";
import { copyFile, mkdir, readFile, readdir, rm, symlink, writeFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { promisify } from "node:util";
import {
  assertSafePagesOutput,
  PAGES_INGEST_TIMEOUT_MS,
  parsePagesArguments,
  prepareDashboardPages,
  runPagesPreparationCommand,
} from "../../scripts/simulate-dashboard-pages.mjs";
import { isExpiredCanonicalRecord } from "../../dashboard/site/src/data/storage/retention.js";

const execute = promisify(execFile);
const root = path.resolve(".");

async function workspace(t) {
  const directory = path.join(root, ".tmp", `pages-simulation-test-${randomUUID()}`);
  await mkdir(directory, { recursive: true });
  t.after(() => rm(directory, { recursive: true, force: true }));
  return directory;
}

async function runs(site) {
  const { stdout } = await execute(process.execPath, [
    "activity/cao.mjs", "query", "--database", path.join(site, "gh-aw-logs.sqlite"),
    "--collection", "runs",
  ], { cwd: root });
  return JSON.parse(stdout);
}

test("Pages simulation requires explicit fixture or snapshot mode", () => {
  for (const args of [[], ["--fixture", "--activity-dir", "data"], ["--other"], ["--output"],
    ["--fixture", "--fixture"], ["--activity-dir", "--fixture"]]) {
    assert.throws(() => parsePagesArguments(args));
  }
  assert.deepEqual(parsePagesArguments(["--fixture", "--output", ".tmp/demo"]), {
    fixture: true, output: ".tmp/demo",
  });
  assert.deepEqual(parsePagesArguments(["--activity-dir", "snapshot"]), { activityDir: "snapshot" });
  assert.deepEqual(parsePagesArguments(["--help"]), { help: true });
});

test("Pages ingestion has a bounded full-snapshot timeout and explicit bounded failure diagnostics", async () => {
  assert.equal(PAGES_INGEST_TIMEOUT_MS, 15 * 60 * 1000);
  await assert.rejects(
    runPagesPreparationCommand("canonical SQLite ingestion", [
      "-e", 'process.stderr.write("x".repeat(12000) + "failure-tail"); process.exit(7)',
    ]),
    error => {
      assert.match(error.message, /Pages canonical SQLite ingestion failed/);
      assert.match(error.message, /timeout=false; timeoutMs=900000; signal=none; code=7/);
      assert.match(error.message, /failure-tail$/);
      assert.ok(error.message.length < 9000);
      assert.equal(error.code, 7);
      return true;
    },
  );
  await assert.rejects(
    runPagesPreparationCommand("canonical SQLite ingestion", ["-e", "setTimeout(() => {}, 30000)"], { timeoutMs: 200 }),
    error => {
      assert.match(error.message, /timeout=true; timeoutMs=200; signal=SIGTERM/);
      assert.match(error.message, /stderr \(last 8192 characters\):/);
      return true;
    },
  );
});

test("Pages output refuses repository paths, unowned data, and symlink ancestors", async (t) => {
  const directory = await workspace(t);
  for (const output of [path.parse(root).root, root, path.dirname(root), path.join(root, ".tmp"),
    path.join(root, "dashboard"), path.join(root, "scripts")]) {
    await assert.rejects(assertSafePagesOutput(output), /dedicated directory/);
  }
  await writeFile(path.join(directory, "keep.txt"), "important");
  await assert.rejects(assertSafePagesOutput(directory), /not owned/);
  const link = `${directory}-link`;
  await symlink(directory, link);
  t.after(() => rm(link, { force: true }));
  await assert.rejects(assertSafePagesOutput(path.join(link, "output")), /symlinks/);
  assert.equal(await readFile(path.join(directory, "keep.txt"), "utf8"), "important");
  await assert.rejects(prepareDashboardPages({ output: path.join(directory, "output") }), /exactly one/);
  await assert.rejects(prepareDashboardPages({ fixture: true, includeSqlite: false }), /not a synthetic fixture/);
  await assert.rejects(prepareDashboardPages({ fixture: true, includeSqlite: null }), /must be a boolean/);
  await assert.rejects(prepareDashboardPages({ fixture: true, fixtureExperimental: null }), /fixtureExperimental/);
  await assert.rejects(prepareDashboardPages({ activityDir: directory, fixtureExperimental: true }), /fixtureExperimental/);
});

test("offline pipeline publishes real build assets, canonical SQLite, and verified phased payloads", { timeout: 120_000 }, async (t) => {
  const directory = await workspace(t);
  const output = path.join(directory, "site");
  const result = await prepareDashboardPages({ fixture: true, output });
  assert.equal(result.output, output);
  assert.equal(result.mode, "fixture");
  const marker = JSON.parse(await readFile(path.join(output, ".cao-pages-simulation.json"), "utf8"));
  assert.equal(marker.synthetic, true);
  assert.equal(marker.includeSqlite, true);
  assert.equal(marker.retention, "all");
  const dashboard = JSON.parse(await readFile(path.join(output, "dashboard.json"), "utf8"));
  assert.ok(!dashboard.dashboard.pages.some(({ id }) => id === "simulator-pull-request"));
  const names = await readdir(output);
  for (const name of ["index.html", "dashboard.json", "service-worker.js", "payload-hashes.json",
    "gh-aw-logs.sqlite", "inventory-sources.json", "agent-summary.json", "llms.txt"]) {
    assert.ok(names.includes(name), name);
  }
  assert.ok(!names.includes("gh-aw-logs-shards"));
  assert.ok(!names.includes("control-settings.json"));
  assert.ok(!names.includes("activity"));
  assert.ok((await readFile(path.join(output, "gh-aw-logs.sqlite"))).subarray(0, 16)
    .equals(Buffer.from("SQLite format 3\0")));
  const manifest = JSON.parse(await readFile(path.join(output, "payload-hashes.json"), "utf8"));
  for (const phase of ["runs", "records"]) {
    assert.ok(Object.keys(manifest).some(name => name.startsWith(`gh-aw-logs-${phase}/`)));
    assert.ok(!(await readdir(path.join(output, `gh-aw-logs-${phase}`))).includes(".payloads"));
  }
  for (const [name, expected] of Object.entries(manifest)) {
    assert.match(name, /^(?:gh-aw-logs\.sqlite|agent-summary\.json|gh-aw-logs-(?:runs|records)\/[^/]+\.jsonl)$/);
    assert.equal(createHash("sha256").update(await readFile(path.join(output, name))).digest("hex"), expected, name);
  }
  const recordShards = Object.keys(manifest).filter(name => name.startsWith("gh-aw-logs-records/"));
  const recordPayloads = await Promise.all(recordShards.map(name => readFile(path.join(output, name), "utf8")));
  assert.ok(recordPayloads.some(payload => payload.includes('"collection":"tools"')));
  const canonicalRuns = await runs(output);
  assert.equal(canonicalRuns.length, 1);
  assert.match(canonicalRuns[0].id, /example\/pages-fixture/);

  await t.test("supplied phased snapshots rebuild offline without mutating their source", async () => {
    const snapshot = path.join(directory, "snapshot");
    const rebuilt = path.join(directory, "rebuilt");
    await mkdir(snapshot);
    await writeFile(path.join(snapshot, "control-settings.json"), "{}\n");
    await copyFile(path.join(output, "inventory-sources.json"), path.join(snapshot, "inventory-sources.json"));
    await copyFile(path.join(output, "payload-hashes.json"), path.join(snapshot, "payload-hashes.json"));
    for (const name of Object.keys(manifest).filter(name => name.includes("/"))) {
      await mkdir(path.dirname(path.join(snapshot, name)), { recursive: true });
      await copyFile(path.join(output, name), path.join(snapshot, name));
    }
    await prepareDashboardPages({ activityDir: snapshot, output: rebuilt });
    assert.deepEqual(await runs(rebuilt), canonicalRuns);
    const snapshotMarker = JSON.parse(await readFile(path.join(rebuilt, ".cao-pages-simulation.json"), "utf8"));
    assert.equal(snapshotMarker.synthetic, false);
    assert.equal("fixtureGeneratedAt" in snapshotMarker, false);
    assert.equal((await readdir(snapshot)).includes("gh-aw-logs.sqlite"), false);
    const browserOutput = path.join(directory, "browser-only");
    const browserResult = await prepareDashboardPages({ activityDir: snapshot, output: browserOutput, includeSqlite: false });
    assert.equal(browserResult.includeSqlite, false);
    const browserNames = await readdir(browserOutput);
    assert.ok(browserNames.includes("index.html"));
    assert.ok(browserNames.includes("dashboard.json"));
    assert.ok(browserNames.includes("service-worker.js"));
    assert.ok(!browserNames.includes("gh-aw-logs.sqlite"));
    assert.ok(!browserNames.includes("agent-summary.json"));
    const browserManifest = JSON.parse(await readFile(path.join(browserOutput, "payload-hashes.json"), "utf8"));
    const shardManifest = Object.fromEntries(Object.entries(manifest).filter(([name]) => name.includes("/")));
    assert.deepEqual(browserManifest, shardManifest);
    for (const name of ["inventory-sources.json", ...Object.keys(shardManifest)]) {
      assert.deepEqual(await readFile(path.join(browserOutput, name)), await readFile(path.join(snapshot, name)));
      assert.deepEqual(await readFile(path.join(snapshot, name)), await readFile(path.join(output, name)));
    }
    assert.equal(JSON.parse(await readFile(path.join(browserOutput, ".cao-pages-simulation.json"), "utf8")).includeSqlite, false);
    const inventoryContents = await readFile(path.join(snapshot, "inventory-sources.json"));
    await writeFile(path.join(snapshot, "inventory-sources.json"), "{}\n");
    await assert.rejects(prepareDashboardPages({ activityDir: snapshot, output: browserOutput, includeSqlite: false }), /Invalid Activity inventory/);
    await writeFile(path.join(snapshot, "inventory-sources.json"), inventoryContents);
    const shard = Object.keys(manifest).find(name => name.includes("/"));
    await writeFile(path.join(snapshot, shard), "corrupt");
    await assert.rejects(prepareDashboardPages({ activityDir: snapshot, output: rebuilt }), /checksum mismatch/);
    await assert.rejects(prepareDashboardPages({ activityDir: snapshot, output: browserOutput, includeSqlite: false }), /checksum mismatch/);
    assert.deepEqual(JSON.parse(await readFile(path.join(browserOutput, "payload-hashes.json"), "utf8")), browserManifest);
    await writeFile(path.join(snapshot, shard), '{"kind":"metadata","phase":"runs","records":1,"schemaVersion":33,"ingestionVersion":5}\n');
    const invalidManifest = { ...manifest, [shard]: createHash("sha256").update(await readFile(path.join(snapshot, shard))).digest("hex") };
    await writeFile(path.join(snapshot, "payload-hashes.json"), JSON.stringify(invalidManifest));
    await assert.rejects(prepareDashboardPages({ activityDir: snapshot, output: browserOutput, includeSqlite: false }), /record count/);
    assert.deepEqual(await runs(rebuilt), canonicalRuns);
    await assert.rejects(prepareDashboardPages({ activityDir: rebuilt, output: path.join(rebuilt, "nested") }), /overlap/);
  });

  await t.test("supplied raw Activity runs are retained instead of silently replaced by the fixture", async () => {
    const snapshot = path.join(directory, "raw-snapshot");
    const rebuilt = path.join(directory, "raw-rebuilt");
    await mkdir(path.join(snapshot, "gh-aw-logs-shards"), { recursive: true });
    await copyFile(path.join(output, "inventory-sources.json"), path.join(snapshot, "inventory-sources.json"));
    await writeFile(path.join(snapshot, "control-settings.json"), "{}\n");
    await copyFile(path.join(root, "dashboard/site/test/fixtures/gh-aw-logs/cached-v2.jsonl"),
      path.join(snapshot, "gh-aw-logs-shards/supplied.jsonl"));
    await assert.rejects(prepareDashboardPages({ activityDir: snapshot, output: rebuilt, includeSqlite: false }), /not raw Activity shards/);
    await prepareDashboardPages({ activityDir: snapshot, output: rebuilt, fixtureNow: Date.parse("2050-01-01T00:00:00Z") });
    const suppliedRuns = await runs(rebuilt);
    assert.equal(suppliedRuns.length, 1);
    assert.match(suppliedRuns[0].id, /303/);
    assert.ok(!suppliedRuns[0].id.includes("pages-fixture"));
    assert.equal(suppliedRuns[0].completedAt, "2026-09-09T04:01:00Z");
    await writeFile(path.join(snapshot, "gh-aw-logs-shards/supplied.jsonl"), "invalid JSON\n");
    await assert.rejects(prepareDashboardPages({ activityDir: snapshot, output: rebuilt }));
    assert.deepEqual(await runs(rebuilt), suppliedRuns);
    assert.ok(!(await readdir(directory)).some(name => name.includes(".prepare-")));
  });
});

test("future fixture invocations retain populated canonical evidence under production browser retention", { timeout: 120_000 }, async (t) => {
  const directory = await workspace(t);
  const futureNow = Date.parse("2040-06-15T12:00:00.000Z");
  const output = path.join(directory, "future-fixture");
  await prepareDashboardPages({ fixture: true, output, fixtureNow: futureNow });
  const inventory = JSON.parse(await readFile(path.join(output, "inventory-sources.json"), "utf8"));
  assert.equal(inventory.workflows.metadata["as-of"], new Date(futureNow).toISOString());
  assert.equal(inventory.workflows.metadata["collection-operation"], "synthetic-pages-fixture");
  const manifest = JSON.parse(await readFile(path.join(output, "payload-hashes.json"), "utf8"));
  const records = [];
  for (const name of Object.keys(manifest).filter(name => name.endsWith(".jsonl"))) {
    records.push(...(await readFile(path.join(output, name), "utf8")).trim().split("\n")
      .map(line => JSON.parse(line)).filter(entry => entry.kind === "record"));
  }
  for (const collection of ["runs", "tools"]) {
    const evidence = records.filter(entry => entry.collection === collection);
    assert.ok(evidence.length > 0, `${collection} must stay populated`);
    for (const { record } of evidence) {
      assert.equal(isExpiredCanonicalRecord(collection, record, { now: futureNow + 60_000 }), false);
    }
  }
  assert.equal(isExpiredCanonicalRecord("tools", { timestamp: "2026-10-01T12:00:00.000Z" }, { now: futureNow }), true);
});
