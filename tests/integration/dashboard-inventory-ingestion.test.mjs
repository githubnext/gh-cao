import assert from "node:assert/strict";
import test from "node:test";
import "fake-indexeddb/auto";
import {
  buildInventoryDashboardSources,
} from "../../dashboard/report/inventory-sources.mjs";
import { ingestDashboardSources } from "../../dashboard/site/src/data/ingest/coordinator.js";
import {
  deleteCanonicalDatabase,
  readCanonicalBatch,
} from "../../dashboard/site/src/data/storage/indexeddb.js";

test("discovered repositories flow from control scope into canonical storage", async () => {
  await deleteCanonicalDatabase(indexedDB);
  try {
    const generatedAt = "2026-09-13T00:00:00Z";
    const controlSettings = {
      allowed_owners: ["acme"],
      allowed_repositories: [],
      campaigns: {},
      policy_document: {
        "control-plane": { inventory: { "max-scan-repositories": 100 } },
      },
    };
    const discoveredRepositories = [
      { full_name: "acme/control", visibility: "private" },
      { full_name: "acme/payments", private: true },
      { full_name: "acme/storefront", visibility: "internal" },
    ];
    const sources = buildInventoryDashboardSources({
      repository: "acme/control",
      generatedAt,
      inventory: {
        generatedAt,
        bundles: [],
        workflows: [{
          id: "activity",
          name: "Activity",
          sourcePath: ".github/workflows/activity.md",
          compiled: true,
        }],
      },
      controlSettings,
      discoveredRepositories,
    });

    await ingestDashboardSources(indexedDB, sources);
    const canonical = await readCanonicalBatch(indexedDB);

    assert.deepEqual(
      canonical.repositories.map(({ owner, name }) => `${owner}/${name}`).sort(),
      ["acme/control", "acme/payments", "acme/storefront"],
    );
    assert.equal(
      canonical.repositories.find(({ owner, name }) => owner === "acme" && name === "control")?.visibility,
      "private",
    );
    assert.equal(canonical.workflows.length, 1);
  } finally {
    await deleteCanonicalDatabase(indexedDB);
  }
});

test("explicitly allowed repositories flow into canonical storage", async () => {
  await deleteCanonicalDatabase(indexedDB);
  try {
    const generatedAt = "2026-09-13T00:00:00Z";
    const controlSettings = {
      allowed_owners: [],
      allowed_repositories: ["acme/payments", "acme/storefront"],
      campaigns: {},
      policy_document: {
        "control-plane": { inventory: { "max-scan-repositories": 100 } },
      },
    };
    const discoveredRepositories = [
      { full_name: "acme/payments", visibility: "private" },
      { full_name: "acme/storefront", visibility: "internal" },
    ];
    const sources = buildInventoryDashboardSources({
      repository: "acme/control",
      generatedAt,
      inventory: { generatedAt, bundles: [], workflows: [] },
      controlSettings,
      discoveredRepositories,
    });

    await ingestDashboardSources(indexedDB, sources);
    const canonical = await readCanonicalBatch(indexedDB);

    assert.deepEqual(
      canonical.repositories.map(({ owner, name }) => `${owner}/${name}`).sort(),
      ["acme/control", "acme/payments", "acme/storefront"],
    );
  } finally {
    await deleteCanonicalDatabase(indexedDB);
  }
});
