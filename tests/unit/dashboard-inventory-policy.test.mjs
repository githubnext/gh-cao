import assert from "node:assert/strict";
import test from "node:test";
import { buildInventoryDashboardSources } from "../../activity/inventory-sources.mjs";

const input = {
  repository: "githubnext/control",
  generatedAt: "2026-09-16T00:00:00Z",
  inventory: { bundles: [], workflows: [] },
};

test("inventory excludes internal campaigns", () => {
  const sources = buildInventoryDashboardSources({
    ...input,
    controlSettings: { campaigns: { activity: {}, dashboard: {} } },
  });
  assert.deepEqual(sources.campaigns.rows, []);
});

test("inventory keeps missing and invalid control settings empty", () => {
  for (const controlSettings of [null, "not-collected", {}]) {
    const sources = buildInventoryDashboardSources({ ...input, controlSettings });
    assert.deepEqual(sources.campaigns.rows, []);
    assert.deepEqual(sources["configuration-policy"].rows, []);
  }
});

test("inventory preserves policy evidence and its validation diagnostics", () => {
  const cases = [
    {
      settings: {
        policy_resolution: { status: "unavailable", reason: "policy file is invalid JSON" },
        policy_document: null,
        policy_source: "{",
      },
      row: {
        path: ".github/workflows/cao.json", document: null, raw: "{",
        diagnostics: [{
          severity: "error", path: ".github/workflows/cao.json",
          title: "Policy validation failed", detail: "policy file is invalid JSON",
        }],
      },
    },
    {
      settings: { policy_document: null, policy_source: "" },
      row: {
        path: ".github/workflows/cao.json", document: null, raw: "",
        diagnostics: [{
          severity: "warning", path: ".github/workflows/cao.json",
          title: "Policy validation status unavailable",
          detail: "The control policy was collected but not validated.",
        }],
      },
    },
    {
      settings: {
        policy_resolution: { status: "available", reason: "" },
        policy_document: { version: 1 },
        policy_source: '{\n  "version": 1\n}\n',
      },
      row: {
        path: ".github/workflows/cao.json", document: { version: 1 }, raw: '{\n  "version": 1\n}\n',
        diagnostics: [{
          severity: "valid", path: ".github/workflows/cao.json",
          title: "Policy is valid", detail: "The runtime policy resolver accepted this revision.",
        }],
      },
    },
  ];
  for (const { settings, row } of cases) {
    const sources = buildInventoryDashboardSources({ ...input, controlSettings: settings });
    assert.deepEqual(sources["configuration-policy"].rows, [row]);
  }
});

test("inventory includes registered campaigns without run history", () => {
  const sources = buildInventoryDashboardSources({
    ...input,
    inventory: { ...input.inventory, campaigns: [{ id: "repo-assist", name: "Repo Assist" }] },
    controlSettings: { campaigns: {} },
  });
  assert.deepEqual(sources.campaigns.rows.map((row) => ({
    campaign: row.campaign, name: row["campaign-name"], mode: row["campaign-mode"],
    workers: row["campaign-worker-count"],
  })), [{ campaign: "repo-assist", name: "Repo Assist", mode: "unknown", workers: 0 }]);
  assert.deepEqual(sources.workflows.rows, []);
});
