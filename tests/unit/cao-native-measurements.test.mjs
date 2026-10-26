import assert from "node:assert/strict";
import test from "node:test";
import { timelineMeasurementEnvelopes } from "../../.github/skills/add-operational-value/scripts/export-measurements.mjs";
import { adaptCachedGhAwJsonl } from "../../dashboard/site/src/data/adapters/gh-aw-logs.js";
import { normalize } from "../../dashboard/site/src/data/normalize/index.js";

function timeline(kind = "interval") {
  return {
    schemaVersion: 3, repository: "example/service", workflowSlug: "incident-response",
    sourcePath: ".github/workflows/incident-response.md",
    valueFunction: { sha256: "a".repeat(64), definition: {
      schemaVersion: 3, repository: "example/service", slug: "incident-response",
      sourcePath: ".github/workflows/incident-response.md", workflowName: "Incident response",
      adoption: { adoptedAt: "2026-01-01T00:00:00Z" }, evaluation: { mode: "attainment-only" },
      evidence: { repositories: ["example/service"], measurementKind: kind, window: { cadenceDays: 7 } },
      metrics: [{ id: "response-hours", role: "primary", name: "Response time", unit: "hours",
        direction: "decrease", formula: "Mean observed response duration", version: "reviewed-v1" }],
    } },
    snapshots: [
      { observedAt: "2026-10-08T00:00:00Z", window: { startAt: "2026-10-01T00:00:00Z", endAt: "2026-10-08T00:00:00Z" },
        evidence: { coverageStatus: "complete", maturityStatus: "matured" },
        metrics: { "response-hours": 0 }, provenance: [{ repository: "example/service", kind: "webhook-receipts", ref: "receipt-generation-1" }] },
      { observedAt: "2026-10-15T00:00:00Z", window: { startAt: "2026-10-08T00:00:00Z", endAt: "2026-10-15T00:00:00Z" },
        evidence: { coverageStatus: "partial", missingReason: "Incomplete retained deliveries" },
        metrics: { "response-hours": null }, provenance: [{ repository: "example/service", kind: "webhook-receipts", ref: "receipt-generation-2" }] },
    ],
  };
}

test("ordinary intervals and snapshots retain zero, missingness, periods and evaluator identity in canonical ingestion", () => {
  for (const kind of ["snapshot", "interval"]) {
    const input = timeline(kind);
    const original = structuredClone(input);
    const envelopes = timelineMeasurementEnvelopes(input);
    const batch = normalize(adaptCachedGhAwJsonl(envelopes.map(JSON.stringify).join("\n")).observations);
    assert.equal(batch.operationalValues.length, 2);
    assert.equal(batch.repositories.length, 1);
    assert.equal(batch.runs.length, 0);
    assert.deepEqual(batch.operationalValues.map(row => row.value), [0, null]);
    const latest = batch.operationalValues[1];
    assert.equal(latest["measurement-kind"], kind);
    assert.equal(latest["maturity-status"], "unavailable");
    assert.equal(latest["coverage-status"], "partial");
    assert.equal(latest["missing-reason"], "Incomplete retained deliveries");
    assert.equal(latest["window-start"], new Date(input.snapshots[1].window.startAt).toISOString());
    assert.equal(latest["window-end"], new Date(input.snapshots[1].window.endAt).toISOString());
    assert.equal(latest["evaluator-digest"], input.valueFunction.sha256);
    assert.equal(latest.valueId, "incident-response.response-hours");
    assert.deepEqual(input, original);
  }
});

test("native measurements deduplicate exact retries and fail closed on conflicting identities and repository substitutions", () => {
  const input = timeline();
  input.snapshots.push(structuredClone(input.snapshots[0]));
  assert.equal(timelineMeasurementEnvelopes(input).length, 2);
  input.snapshots.at(-1).metrics["response-hours"] = 3;
  assert.throws(() => timelineMeasurementEnvelopes(input), /Conflicting native observation/);
  for (const mutate of [
    data => { data.repository = "outside/service"; },
    data => { data.workflowSlug = "another"; },
    data => { data.snapshots[0].provenance[0].repository = "outside/service"; },
    data => { delete data.snapshots[0].metrics["response-hours"]; },
  ]) {
    const data = timeline();
    mutate(data);
    assert.throws(() => timelineMeasurementEnvelopes(data));
  }
});
