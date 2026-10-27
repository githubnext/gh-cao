import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { downloadDeployedDashboardData } from "../e2e/dashboard-view-data.mjs";
import { deployedActivityShardEntries } from "../e2e/dashboard-deployed-refresh-helpers.mjs";

test("capped preview selection retains both phases without exceeding the shard budget", () => {
  for (const extension of ["jsonl", "json"]) {
    const manifest = Object.fromEntries([
      ...Array.from({ length: 10 }, (_, index) => [`gh-aw-logs-runs/${index}.${extension}`, "a".repeat(64)]),
      [`gh-aw-logs-records/first.${extension}`, "b".repeat(64)],
      [`gh-aw-logs-records/second.${extension}`, "c".repeat(64)],
    ]);
    const all = deployedActivityShardEntries(manifest);
    for (const limit of [2, 5, 10, 11, 12, 20]) {
      const selected = deployedActivityShardEntries(manifest, limit);
      assert.equal(selected.length, Math.min(limit, all.length));
      assert.deepEqual(selected, limit <= 10
        ? [...all.slice(0, limit - 1), all[10]]
        : all.slice(0, limit));
      assert.ok(selected.every(({ name }) => name.endsWith(".jsonl")));
      assert.ok(selected.every(({ sourceName }) => sourceName.endsWith(`.${extension}`)));
    }
    assert.deepEqual(deployedActivityShardEntries(manifest, undefined), all);
  }
});

test("capped preview selection rejects limits or manifests that cannot include both phases", () => {
  const manifest = {
    "gh-aw-logs-runs/first.jsonl": "a".repeat(64),
    "gh-aw-logs-records/first.jsonl": "b".repeat(64),
  };
  for (const limit of [0, 1, -1, 2.5, NaN]) {
    assert.throws(() => deployedActivityShardEntries(manifest, limit), /limit must be at least 2/);
  }
  assert.throws(() => deployedActivityShardEntries({
    "gh-aw-logs-runs/first.jsonl": "a".repeat(64),
  }, 5), /no compacted record shards/);
  assert.throws(() => deployedActivityShardEntries({
    "gh-aw-logs-records/first.jsonl": "b".repeat(64),
  }, 5), /no compacted run-information shards/);
});

test("downloads canonical deployed dashboard inputs", async () => {
  const destination = await mkdtemp(join(tmpdir(), "dashboard-view-data-"));
  const requested = [];
  const fetcher = async (url) => {
    const requestUrl = String(url);
    requested.push(requestUrl);
    let content = '{"repositories":[]}';
    if (requestUrl.endsWith(".jsonl")) content = '{"kind":"run"}\n';
    if (requestUrl.endsWith("payload-hashes.json")) {
      content = `{"gh-aw-logs-runs/fixture.jsonl":"${"a".repeat(64)}"}`;
    }
    if (requestUrl.endsWith("memory/manifest.json")) {
      content = '{"campaigns":[{"campaign":"ambient-context","files":[{"path":"notes.md"},{"path":"%2E%2E%2Fescape.md"}]}]}';
    }
    if (requestUrl.endsWith("memory/ambient-context/notes.md")) content = "# Memory\n";
    return {
      ok: true,
      body: new Blob([content]).stream(),
      text: async () => {
        throw new Error("response must be streamed");
      },
      json: async () => JSON.parse(content),
    };
  };

  try {
    await downloadDeployedDashboardData(
      destination,
      "https://example.test/cao/payload-hashes.json",
      fetcher,
    );

    assert.deepEqual(requested, [
      "https://example.test/cao/payload-hashes.json",
      "https://example.test/cao/inventory-sources.json",
      "https://example.test/cao/memory/manifest.json",
      "https://example.test/cao/memory/ambient-context/notes.md",
      "https://example.test/cao/gh-aw-logs-runs/fixture.jsonl",
    ]);
    assert.equal(await readFile(join(destination, "gh-aw-logs-runs", "fixture.jsonl"), "utf8"), '{"kind":"run"}\n');
    assert.equal(
      await readFile(join(destination, "inventory-sources.json"), "utf8"),
      '{"repositories":[]}',
    );
    assert.equal(await readFile(join(destination, "memory", "ambient-context", "notes.md"), "utf8"), "# Memory\n");
  } finally {
    await rm(destination, { recursive: true });
  }
});

test("limits the downloaded activity shards when requested", async () => {
  const destination = await mkdtemp(join(tmpdir(), "dashboard-view-data-"));
  const requested = [];
  const fetcher = async (url) => {
    requested.push(String(url));
    if (String(url).endsWith("memory/manifest.json")) return { ok: false, status: 404 };
    const content = String(url).endsWith("payload-hashes.json")
      ? JSON.stringify({
          "gh-aw-logs-runs/first.jsonl": "a".repeat(64),
          "gh-aw-logs-runs/second.jsonl": "a".repeat(64),
          "gh-aw-logs-records/second.jsonl": "b".repeat(64),
        })
      : String(url).endsWith("inventory-sources.json")
        ? '{"repositories":[]}'
        : '{"kind":"record"}\n';
    return {
      ok: true,
      body: new Blob([content]).stream(),
      json: async () => JSON.parse(content),
    };
  };

  try {
    await downloadDeployedDashboardData(
      destination,
      "https://example.test/cao/payload-hashes.json",
      fetcher,
      2,
    );

    assert.deepEqual(requested, [
      "https://example.test/cao/payload-hashes.json",
      "https://example.test/cao/inventory-sources.json",
      "https://example.test/cao/memory/manifest.json",
      "https://example.test/cao/gh-aw-logs-runs/first.jsonl",
      "https://example.test/cao/gh-aw-logs-records/second.jsonl",
    ]);
    assert.equal(await readFile(join(destination, "gh-aw-logs-records", "second.jsonl"), "utf8"), '{"kind":"record"}\n');
  } finally {
    await rm(destination, { recursive: true });
  }
});

test("waits for both deployed dashboard downloads when one fails", async () => {
  const destination = await mkdtemp(join(tmpdir(), "dashboard-view-data-"));
  let inventoryFinished = false;
  const fetcher = async (url) => ({
    ok: true,
    body: String(url).endsWith("inventory-sources.json")
      ? new ReadableStream({
          async start(controller) {
            await new Promise((resolve) => setTimeout(resolve, 10));
            controller.enqueue(new TextEncoder().encode('{"repositories":[]}'));
            controller.close();
            inventoryFinished = true;
          },
        })
      : new ReadableStream({
          start(controller) {
            controller.error(new Error("activity download failed"));
          },
        }),
    json: async () => ({ "gh-aw-logs-runs/fixture.jsonl": "a".repeat(64) }),
  });

  try {
    await assert.rejects(
      downloadDeployedDashboardData(
        destination,
        "https://example.test/cao/payload-hashes.json",
        fetcher,
      ),
      /activity download failed/,
    );
    assert.equal(inventoryFinished, true);
  } finally {
    await rm(destination, { recursive: true });
  }
});

test("converts predecessor compacted phase JSON to current JSONL", async () => {
  const destination = await mkdtemp(join(tmpdir(), "dashboard-view-data-"));
  const legacyName = "gh-aw-logs-runs/legacy.json";
  const currentName = "gh-aw-logs-runs/legacy.jsonl";
  const fetcher = async (url) => {
    const value = String(url);
    const content = value.endsWith("payload-hashes.json")
      ? JSON.stringify({ [legacyName]: "a".repeat(64) })
      : value.endsWith("inventory-sources.json")
        ? '{"repositories":[]}'
        : JSON.stringify({
            schemaVersion: 13,
            ingestionVersion: 2,
            sourceRecords: 1,
            phase: "runs",
            batch: {
              campaigns: [],
              repositories: [{ id: "repository:acme%2Fcontrol" }],
              workflows: [],
              runs: [],
            },
          });
    return {
      ok: true,
      body: new Blob([content]).stream(),
      arrayBuffer: async () => new TextEncoder().encode(content).buffer,
      json: async () => JSON.parse(content),
    };
  };

  try {
    await downloadDeployedDashboardData(
      destination,
      "https://example.test/cao/payload-hashes.json",
      fetcher,
    );
    const lines = (await readFile(join(destination, currentName), "utf8"))
      .trim()
      .split("\n")
      .map((line) => JSON.parse(line));
    assert.deepEqual(lines[0], {
      kind: "metadata",
      schemaVersion: 13,
      ingestionVersion: 3,
      sourceRecords: 1,
      phase: "runs",
      records: 1,
    });
    assert.deepEqual(lines[1], {
      kind: "record",
      collection: "repositories",
      record: { id: "repository:acme%2Fcontrol" },
    });
  } finally {
    await rm(destination, { recursive: true });
  }
});
