import assert from "node:assert/strict";
import test from "node:test";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import "fake-indexeddb/auto";
import { ingestNormalizedJsonl } from "../../dashboard/site/src/data/ingest/coordinator.js";
import {
  deleteCanonicalDatabase,
  readCollection,
} from "../../dashboard/site/src/data/storage/indexeddb.js";
import {
  deployedActivityShardEntries,
  legacyPhaseJsonToJsonl,
} from "../e2e/dashboard-deployed-refresh-helpers.mjs";

const deployedManifestUrl = process.env.DASHBOARD_DATA_URL;
const artifactDirectory = process.env.DASHBOARD_DATA_DIR;
const transientDownloadStatuses = new Set([408, 429, 500, 502, 503, 504]);

async function fetchDeployedData(url) {
  const maximumAttempts = 4;
  for (let attempt = 1; ; attempt += 1) {
    try {
      const response = await fetch(url, { signal: AbortSignal.timeout(60_000) });
      if (response.ok || !transientDownloadStatuses.has(response.status) || attempt === maximumAttempts) {
        return response;
      }
      await response.body?.cancel();
    } catch (error) {
      if (attempt === maximumAttempts) throw error;
    }
    await new Promise((resolve) => setTimeout(resolve, 1_000 * 2 ** (attempt - 1)));
  }
}

async function ingestDeployedShards() {
  assert.ok(
    Boolean(deployedManifestUrl) !== Boolean(artifactDirectory),
    "Select exactly one operational source: DASHBOARD_DATA_URL (explicit manifest) or DASHBOARD_DATA_DIR (cao-activity-index Actions artifact).",
  );
  const load = async (name) => {
    if (artifactDirectory) return readFile(resolve(artifactDirectory, name));
    const url = name === "payload-hashes.json" ? deployedManifestUrl : new URL(name, deployedManifestUrl);
    const response = await fetchDeployedData(url);
    assert.equal(response.ok, true, `failed to download ${url}: ${response.status}`);
    return Buffer.from(await response.arrayBuffer());
  };
  const manifest = JSON.parse((await load("payload-hashes.json")).toString("utf8"));
  assert.notEqual(manifest.synthetic, true, "Synthetic demo data is not operational evidence.");
  assert.match(manifest["gh-aw-logs.sqlite"] ?? "", /^[a-f0-9]{64}$/i,
    "Operational evidence must include a checksum for the canonical SQLite snapshot.");
  for (const { name, sourceName, hash } of deployedActivityShardEntries(manifest)) {
    const content = await load(sourceName);
    assert.equal(createHash("sha256").update(content).digest("hex"), hash.toLowerCase(),
      `operational artifact checksum mismatch for ${sourceName}`);
    const chunks = sourceName.endsWith(".json")
      ? new Response(legacyPhaseJsonToJsonl(content)).body
      : new Response(content).body;
    await ingestNormalizedJsonl(indexedDB, chunks, {
      payloadIdentity: hash,
      payloadScope: name,
      expectedPhase: name.startsWith("gh-aw-logs-runs/") ? "runs" : "records",
    });
  }
}

test.before(async () => {
  await deleteCanonicalDatabase(indexedDB);
  await ingestDeployedShards();
});

test.after(async () => {
  await deleteCanonicalDatabase(indexedDB);
});

test("operational Activity snapshot populates canonical workflows, runs, and run records", async () => {
  const [repositories, workflows, runs, domains, tools, audits, issues] = await Promise.all([
    readCollection(indexedDB, "repositories"),
    readCollection(indexedDB, "workflows"),
    readCollection(indexedDB, "runs"),
    readCollection(indexedDB, "domains"),
    readCollection(indexedDB, "tools"),
    readCollection(indexedDB, "audits"),
    readCollection(indexedDB, "issues"),
  ]);
  for (const [table, entries] of Object.entries({ repositories, workflows, runs })) {
    console.error(`Canonical ${table} rows: ${entries.length}`);
    assert.ok(entries.length > 0, `canonical ${table} table must contain an entry`);
  }
  assert.ok(domains.length + tools.length + audits.length + issues.length > 0);
});

test("operational Activity snapshot ingests its firewall analysis", async () => {
  const ingestedFirewallEvents = (await readCollection(indexedDB, "domains")).filter(
    (event) => event.source === "firewall" && /^net_/.test(String(event.type)),
  );
  assert.ok(ingestedFirewallEvents.length > 0, "deployed gh-aw logs must ingest firewall events");
  assert.ok(
    ingestedFirewallEvents.every((event) => Number(event.requestCount) > 0),
    "ingested firewall events must retain positive request counts",
  );
});
