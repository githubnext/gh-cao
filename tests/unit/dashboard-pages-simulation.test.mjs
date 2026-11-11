import assert from "node:assert/strict";
import { createHash, randomUUID } from "node:crypto";
import { mkdir, readFile, readdir, rm, symlink, writeFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { Readable } from "node:stream";
import { IDBFactory } from "fake-indexeddb";
import {
  assertSafePagesOutput, parsePagesArguments, prepareDashboardPages,
} from "../../scripts/simulate-dashboard-pages.mjs";
import { ingestNormalizedJsonl } from "../../dashboard/site/src/data/ingest/coordinator.js";
import { readCollection } from "../../dashboard/site/src/data/storage/indexeddb.js";
import { relationshipErrors } from "../../dashboard/site/src/data/model/schema.js";
import { isExpiredCanonicalRecord } from "../../dashboard/site/src/data/storage/retention.js";

const root = path.resolve(".");

async function workspace(t) {
  const directory = path.join(root, ".tmp", `pages-demo-test-${randomUUID()}`);
  await mkdir(directory, { recursive: true });
  t.after(() => rm(directory, { recursive: true, force: true }));
  return directory;
}

test("Pages accepts only generated sample data, with no fixture or real-data switch", async () => {
  for (const args of [["--fixture"], ["--activity-dir", "data"], ["--other"], ["--output"]]) {
    assert.throws(() => parsePagesArguments(args));
  }
  assert.deepEqual(parsePagesArguments([]), {});
  assert.deepEqual(parsePagesArguments(["--output", ".tmp/demo"]), { output: ".tmp/demo" });
  assert.deepEqual(parsePagesArguments(["--help"]), { help: true });
  await assert.rejects(prepareDashboardPages({ activityDir: "data" }), /synthetic data only/);
  await assert.rejects(prepareDashboardPages({ includeSqlite: true }), /synthetic data only/);
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
});

test("sample-only build produces verified canonical enterprise data and no operational exports", { timeout: 120_000 }, async (t) => {
  const directory = await workspace(t);
  const output = path.join(directory, "site");
  const now = Date.parse("2040-06-15T12:00:00.000Z");
  const result = await prepareDashboardPages({ output, now });
  assert.equal(result.synthetic, true);
  const marker = JSON.parse(await readFile(path.join(output, ".cao-pages-demo.json"), "utf8"));
  assert.equal(marker.synthetic, true);
  const names = await readdir(output);
  for (const name of ["gh-aw-logs.sqlite", "agent-summary.json", "payload-hashes.json",
    "gh-aw-logs-shards", "control-settings.json", "activity"]) assert.ok(!names.includes(name), name);
  const dashboard = JSON.parse(await readFile(path.join(output, "dashboard.json"), "utf8"));
  assert.equal(dashboard.dashboard.title, "CAO synthetic enterprise demo");
  assert.ok(dashboard.dashboard.callouts.some(callout => callout.id === "synthetic-demo" && !callout["visible-when"]));
  assert.equal(dashboard.dashboard["github-url-base"], "https://example.invalid");
  const html = await readFile(path.join(output, "index.html"), "utf8");
  assert.match(html, /dashboard-deployment" content="synthetic-demo"/);
  assert.match(html, /connect-src 'self'/);
  const data = path.join(output, "demo-data-v1");
  const inventory = JSON.parse(await readFile(path.join(data, "inventory-sources.json"), "utf8"));
  assert.equal(inventory.workflows.metadata["source-kind"], "synthetic");
  assert.equal(inventory.workflows.metadata["as-of"], new Date(now).toISOString());
  const manifest = JSON.parse(await readFile(path.join(data, "payload-hashes.json"), "utf8"));
  const indexedDB = new IDBFactory();
  const records = {};
  // Use the canonical production boundary, including run-before-detail ingestion.
  const shards = Object.entries(manifest).sort(([a], [b]) =>
    Number(b.startsWith("gh-aw-logs-runs/")) - Number(a.startsWith("gh-aw-logs-runs/")) || a.localeCompare(b));
  for (const [name, expected] of shards) {
    assert.match(name, /^gh-aw-logs-(?:runs|records)\/[^/]+\.jsonl$/);
    const bytes = await readFile(path.join(data, name));
    assert.equal(createHash("sha256").update(bytes).digest("hex"), expected);
    for (const entry of bytes.toString().trim().split("\n").map(line => JSON.parse(line))) {
      if (entry.kind === "record") (records[entry.collection] ??= []).push(entry.record);
    }
    await ingestNormalizedJsonl(indexedDB, Readable.from([bytes]), {
      payloadIdentity: expected, payloadScope: name, now, deferMaintenance: true,
      expectedPhase: name.startsWith("gh-aw-logs-runs/") ? "runs" : "records",
    });
  }
  assert.deepEqual(relationshipErrors(records), []);
  const runs = await readCollection(indexedDB, "runs");
  assert.equal(runs.length, 672);
  assert.equal((await readCollection(indexedDB, "repositories")).length, 12);
  assert.equal((await readCollection(indexedDB, "workflows")).length, 48);
  assert.ok(runs.some(run => run.conclusion === "failure"));
  assert.ok(runs.every(run => run.id.includes("sample-enterprise/")));
  const tools = await readCollection(indexedDB, "tools");
  assert.ok(tools.length > 0);
  assert.ok(tools.some(record => !isExpiredCanonicalRecord("tools", record, { now: now + 60_000 })));
  assert.match(await readFile(path.join(output, "llms.txt"), "utf8"), /not operational evidence/);
});
